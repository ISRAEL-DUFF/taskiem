package runtime

import (
	"context"
	"encoding/json"
	"fmt"

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
