package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/history"
	"github.com/israel-duff/taskiem/engine/wd"
)

// insertFormRequest records a form step's request in the transaction that
// schedules it (docs/spec/forms.md 6.7). The recipient and prefill are
// taken from the sealed payload, so personal values stay sealed; the api
// role sends the link (engine/forms).
func insertFormRequest(ctx context.Context, tx pgx.Tx, run runRow, step string, p history.ScheduledPayload, sealed any) error {
	f := p.Form
	id, err := uuid.Parse(f.Request)
	if err != nil {
		return fmt.Errorf("form request id: %w", err)
	}
	to, prefill := any(f.To), any(f.Prefill)
	if m, ok := sealed.(map[string]any); ok {
		if fm, ok := m["form"].(map[string]any); ok {
			to, prefill = fm["to"], fm["prefill"]
		}
	}
	if starter, _ := f.To["starter"].(bool); starter {
		// The person who started the run, in the chat they started it
		// from (made tools M4).
		var err error
		if to, err = starterOf(ctx, tx, run.ref.ID); err != nil {
			return err
		}
	}
	recipient, _ := json.Marshal(to)
	var pre []byte
	if prefill != nil {
		pre, _ = json.Marshal(prefill)
	}
	schema, _ := json.Marshal(f.Schema)
	remind := make([]string, 0, len(f.Remind))
	for _, r := range f.Remind {
		d, err := wd.ParseDuration(r)
		if err != nil {
			return err
		}
		remind = append(remind, fmt.Sprintf("%d seconds", int64(d.Seconds())))
	}
	expires, err := history.ParseTime(p.TimeoutAt)
	if err != nil {
		return err
	}
	var desc *string
	if f.Description != "" {
		desc = &f.Description
	}
	_, err = tx.Exec(ctx, `INSERT INTO form_requests (id, tenant_id, run_id, step_id, environment, recipient, title, description, schema, prefill, via, verify, remind, expires_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13::interval[], $14)
		ON CONFLICT (id) DO NOTHING`,
		id, run.ref.TenantID, run.ref.ID, step, run.env, recipient, f.Title, desc, schema, pre, f.Via, f.Verify, remind, expires)
	return err
}

// starterOf is who started run, as a form step's recipient: the member
// (an API key as its owner), the chat channel the run was started from,
// and starter: true. A subflow's run asks whoever started the run at the
// top of its chain, in that run's chat; a resumed run, the original's.
// Nobody (a schedule, an event, a
// workflow): no member, and the request says it cannot ask.
func starterOf(ctx context.Context, tx pgx.Tx, run uuid.UUID) (map[string]any, error) {
	out := map[string]any{"starter": true}
	for depth := 0; depth < 16; depth++ {
		var by, channel *string
		var parent *uuid.UUID
		var forked bool
		if err := tx.QueryRow(ctx, `SELECT r.started_by, (SELECT e.payload->'trigger'->>'channel' FROM run_events e WHERE e.run_id = r.id AND e.type = $2 ORDER BY e.seq LIMIT 1),
			r.parent_run_id, r.forked_from_seq IS NOT NULL
			FROM runs r WHERE r.id = $1`, run, history.RunStarted).Scan(&by, &channel, &parent, &forked); err != nil {
			return nil, err
		}
		// A failed run resumed as a fork: its starter is the original's,
		// not whoever accepted the repair.
		if forked && parent != nil {
			run = *parent
			continue
		}
		if by == nil {
			return out, nil
		}
		// A subflow's child: the parent's starter (subflow:<run>/<step>).
		if rest, ok := strings.CutPrefix(*by, "subflow:"); ok {
			parent, _, _ := strings.Cut(rest, "/")
			id, err := uuid.Parse(parent)
			if err != nil {
				return out, nil //nolint:nilerr // a parent no id names: nobody to ask
			}
			run = id
			continue
		}
		if channel != nil {
			out["channel"] = *channel
		}
		if u, err := uuid.Parse(*by); err == nil {
			out["member"] = u.String()
			return out, nil
		}
		if key, ok := strings.CutPrefix(*by, "key:"); ok {
			var owner *uuid.UUID
			err := tx.QueryRow(ctx, `SELECT owner_id FROM api_keys WHERE id::text = $1`, key).Scan(&owner)
			if err == nil && owner != nil {
				out["member"] = owner.String()
			}
		}
		return out, nil
	}
	return out, nil
}
