package ingest

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/connector"
	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/egress"
	"github.com/israel-duff/taskiem/engine/expr"
	"github.com/israel-duff/taskiem/engine/runtime"
	"github.com/israel-duff/taskiem/engine/secrets"
)

// Poller asks providers for the events a connector trigger's webhook would
// carry, for businesses that never set the webhook up in the provider's
// dashboard (no-technical-steps N4; a trigger's poll in its manifest). It
// runs in the scheduler role. Each record it finds becomes the delivery
// the provider would have sent, signed with the connection's own secret,
// and goes through Hooks like any other: the same checks, subscribers,
// signals and dedup, so an event the webhook also delivered is taken once.
type Poller struct {
	Store       *runtime.Store
	Registry    *connector.Registry
	Connections Connections
	// Hooks is the webhook handler (mounted at /hooks, without the prefix).
	Hooks  http.Handler
	Egress *egress.Guard
	Logger *slog.Logger
	// Now is the clock (tests).
	Now func() time.Time
}

// overlap is how far back each poll looks before where the last one got
// to, for records the provider lists a little late; dedup drops repeats.
const overlap = 10 * time.Minute

var pollExprs = expr.MustNewWithRoots("output", "item")

func (p *Poller) now() time.Time {
	if p.Now != nil {
		return p.Now()
	}
	return time.Now()
}

// Run polls until ctx ends.
func (p *Poller) Run(ctx context.Context) error {
	for {
		if _, err := p.Tick(ctx); err != nil && ctx.Err() == nil {
			p.log().Error("poller", "err", err)
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(time.Minute):
		}
	}
}

func (p *Poller) log() *slog.Logger {
	if p.Logger != nil {
		return p.Logger
	}
	return slog.Default()
}

type pollTarget struct {
	tenant                         uuid.UUID
	connector, trigger, connection string
}

// Tick polls every target that is due and returns how many events it
// delivered. One target's failure is recorded on it and does not stop
// the others.
func (p *Poller) Tick(ctx context.Context) (int, error) {
	rows, err := p.Store.Pool.Query(ctx, `SELECT tenant_id, connector, trigger_name, connection FROM taskiem_poll_targets()`)
	if err != nil {
		return 0, err
	}
	targets, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (pollTarget, error) {
		var t pollTarget
		return t, r.Scan(&t.tenant, &t.connector, &t.trigger, &t.connection)
	})
	if err != nil {
		return 0, err
	}
	n := 0
	for _, t := range targets {
		reg, err := p.Registry.For(ctx, t.tenant.String())
		if err != nil {
			return n, err
		}
		conn, ok := reg.Get(t.connector)
		if !ok {
			continue
		}
		spec, ok := conn.Manifest.Triggers[t.trigger]
		if !ok || spec.Poll == nil {
			continue
		}
		got, err := p.poll(ctx, t, conn, spec)
		n += got
		if err != nil {
			if ctx.Err() != nil {
				return n, err
			}
			p.log().Warn("poller: target failed", "tenant", t.tenant, "connector", t.connector, "trigger", t.trigger, "err", err)
		}
	}
	return n, nil
}

// poll asks about one target when it is due, delivers what it found, and
// records where it got to.
func (p *Poller) poll(ctx context.Context, t pollTarget, conn *connector.Connector, spec connector.TriggerSpec) (int, error) {
	now := p.now()
	var since time.Time
	var polled *time.Time
	first := false
	err := db.InTenantTx(ctx, p.Store.Pool, []uuid.UUID{t.tenant}, func(tx pgx.Tx) error {
		err := tx.QueryRow(ctx, `SELECT since, polled_at FROM trigger_polls WHERE connector = $1 AND trigger_name = $2 AND connection = $3`,
			t.connector, t.trigger, t.connection).Scan(&since, &polled)
		if errors.Is(err, pgx.ErrNoRows) {
			// The first look starts now: nothing from before the workflow
			// was deployed is replayed.
			first = true
			_, err = tx.Exec(ctx, `INSERT INTO trigger_polls (tenant_id, connector, trigger_name, connection, since, polled_at) VALUES ($1, $2, $3, $4, $5, $5)
				ON CONFLICT DO NOTHING`, t.tenant, t.connector, t.trigger, t.connection, now)
		}
		return err
	})
	if err != nil || first {
		return 0, err
	}
	if polled != nil && now.Sub(*polled) < spec.Poll.Interval() {
		return 0, nil
	}
	n, pollErr := p.fetchAndDeliver(ctx, t, conn, spec, since.Add(-overlap))
	err = db.InTenantTx(ctx, p.Store.Pool, []uuid.UUID{t.tenant}, func(tx pgx.Tx) error {
		if pollErr != nil {
			_, err := tx.Exec(ctx, `UPDATE trigger_polls SET polled_at = $4, last_error = $5 WHERE connector = $1 AND trigger_name = $2 AND connection = $3`,
				t.connector, t.trigger, t.connection, now, clip(pollErr.Error()))
			return err
		}
		_, err := tx.Exec(ctx, `UPDATE trigger_polls SET since = $4, polled_at = $4, last_error = NULL WHERE connector = $1 AND trigger_name = $2 AND connection = $3`,
			t.connector, t.trigger, t.connection, now)
		return err
	})
	if pollErr != nil {
		return n, pollErr
	}
	return n, err
}

func clip(s string) string {
	if len(s) > 500 {
		return s[:500]
	}
	return s
}

// fetchAndDeliver runs the poll action from since and delivers each
// record as the provider's webhook would.
func (p *Poller) fetchAndDeliver(ctx context.Context, t pollTarget, conn *connector.Connector, spec connector.TriggerSpec, since time.Time) (int, error) {
	action, ok := conn.Actions[spec.Poll.Action]
	if !ok {
		return 0, fmt.Errorf("no action %s", spec.Poll.Action)
	}
	use := secrets.WithUse(ctx, secrets.Use{Kind: secrets.KindConnection, Purpose: "ingest.poll"})
	creds, err := p.Connections.Credentials(use, t.tenant, ScheduleEnvironment, conn.Manifest.ID, t.connection)
	if errors.Is(err, secrets.ErrNotFound) || errors.Is(err, secrets.ErrAmbiguous) {
		return 0, nil // nothing to ask with: readiness says so to the business
	}
	if err != nil {
		return 0, err
	}
	secret := creds[spec.Verify.SecretField]
	g := p.Egress
	if g == nil {
		g = &egress.Guard{Logger: p.Logger}
	}
	hc := g.Client(egress.Policy{Tenant: t.tenant.String(), Hosts: conn.Manifest.HostsFor(creds), Purpose: "connector:" + conn.Manifest.ID}, 30*time.Second)
	resp, err := action.Execute(ctx, connector.Request{Input: map[string]any{spec.Poll.Since: since.UTC().Format(time.RFC3339)}, Credentials: creds, HTTP: hc, Logger: p.Logger, Attempt: 1})
	if err != nil {
		return 0, err
	}
	v, err := pollExprs.Eval(spec.Poll.Items, map[string]any{"output": resp.Output})
	if err != nil {
		return 0, fmt.Errorf("items: %w", err)
	}
	items, _ := v.([]any)
	n := 0
	for _, item := range items {
		ev, err := pollExprs.Eval(spec.Poll.Event, map[string]any{"item": item})
		event, _ := ev.(string)
		if err != nil || event == "" {
			continue
		}
		body, err := json.Marshal(map[string]any{"event": event, "data": item})
		if err != nil {
			return n, err
		}
		header, sig, err := connector.SignBody(spec.Verify, secret, body)
		if err != nil {
			return n, err
		}
		path := "/" + t.tenant.String() + "/connectors/" + t.connector + "/" + t.trigger + "?env=" + ScheduleEnvironment
		if t.connection != "" {
			path += "&connection=" + t.connection
		}
		req := httptest.NewRequestWithContext(ctx, http.MethodPost, path, bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set(header, sig)
		rec := httptest.NewRecorder()
		p.Hooks.ServeHTTP(rec, req)
		if rec.Code >= 300 {
			return n, fmt.Errorf("delivering a polled %s: %d %s", event, rec.Code, clip(rec.Body.String()))
		}
		n++
	}
	return n, nil
}
