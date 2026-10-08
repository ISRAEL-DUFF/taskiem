package ingest_test

import (
	"bytes"
	"context"
	"crypto/sha512"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/connector"
	"github.com/israel-duff/taskiem/engine/ingest"
	"github.com/israel-duff/taskiem/engine/runtime"
)

// A delivery verified with one environment's credentials reaches only runs
// in that environment, waiting or not yet waiting.
func TestSignalsStayInTheirEnvironment(t *testing.T) {
	w := newWorld(t)
	for env, key := range map[string]string{"prod": "sk_live_prod", "dev": "sk_test_dev"} {
		if _, err := w.Vault.CreateConnection(ctx, w.Tenant, env, "paystack", "main", "api_key", map[string]string{"secret_key": key}, "test"); err != nil {
			t.Fatal(err)
		}
	}
	settle := w.Publish(t, settleFlow)
	waiting := w.Start(t, settle, map[string]any{"body": map[string]any{"ref": "T-9"}})

	body := []byte(`{"event":"transfer.success","data":{"reference":"T-9","amount":5000}}`)
	st, out := w.post(t, "/connectors/paystack@1/transfer_event?env=dev", body, "x-paystack-signature", sign(sha512.New, "sk_test_dev", body))
	if st != 202 || out["signalled"] != float64(0) {
		t.Fatalf("dev delivery: %d %v", st, out)
	}
	if s := w.Status(t, waiting); s != "running" {
		t.Fatalf("a dev-signed delivery woke a prod run: %s", s)
	}
	// Buffered in dev, it is not handed to a prod run that starts waiting later.
	later := w.Start(t, settle, map[string]any{"body": map[string]any{"ref": "T-9"}})
	if s := w.Status(t, later); s != "running" {
		t.Fatalf("a prod run consumed a dev signal: %s", s)
	}
	// The same body delivered to prod is not a duplicate of the dev one.
	st, out = w.post(t, "/connectors/paystack@1/transfer_event?env=prod", body, "x-paystack-signature", sign(sha512.New, "sk_live_prod", body))
	if st != 202 || out["duplicate"] != false || out["signalled"] != float64(2) {
		t.Fatalf("prod delivery: %d %v", st, out)
	}
	for _, r := range []runtime.RunRef{waiting, later} {
		if s := w.Status(t, r); s != "completed" {
			t.Errorf("prod run %s: %s", r.ID, s)
		}
	}
}

// One tenant's due schedules cannot fill a batch, a schedule that fails does
// not stop the others, and one that can never fire again is disabled.
func TestSchedulesAreFairAndNeverStarve(t *testing.T) {
	w := newWorld(t)
	wf := w.publish(t, `{"schema":"wd/v1","id":"wf_daily","version":1,"name":"daily","trigger":{"type":"schedule","config":{"cron":"0 9 * * *"}},
	  "steps":[{"id":"a","type":"transform","config":{"output":1}}]}`)
	if _, err := w.DB.Admin.Exec(ctx, `UPDATE triggers SET next_fire_at = now() - interval '1 hour' WHERE workflow_id = $1`, wf); err != nil {
		t.Fatal(err)
	}
	// Another tenant with an older backlog, all failing (its definition does
	// not load), plus a schedule stored before "never fires" was refused.
	noisy := w.DB.SeedTenant(t, nil)
	for range 15 {
		if _, err := w.DB.Admin.Exec(ctx, `INSERT INTO triggers (id, tenant_id, workflow_id, version, environment, type, cron, timezone, next_fire_at)
			VALUES ($1, $2, $3, 1, 'prod', 'schedule', '0 9 * * *', 'UTC', now() - interval '2 hours')`, uuid.Must(uuid.NewV7()), noisy.ID, noisy.WorkflowID); err != nil {
			t.Fatal(err)
		}
	}
	never := uuid.Must(uuid.NewV7())
	if _, err := w.DB.Admin.Exec(ctx, `INSERT INTO triggers (id, tenant_id, workflow_id, version, environment, type, cron, timezone, next_fire_at)
		VALUES ($1, $2, $3, 1, 'prod', 'schedule', '0 0 30 2 *', 'UTC', '0001-01-01T00:00:00Z')`, never, noisy.ID, noisy.WorkflowID); err != nil {
		t.Fatal(err)
	}

	// A small batch still reaches the quiet tenant.
	rows, err := w.DB.App.Query(ctx, `SELECT tenant_id FROM taskiem_claim_due_schedules(5, 3)`)
	if err != nil {
		t.Fatal(err)
	}
	got, err := pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
	if err != nil {
		t.Fatal(err)
	}
	per := map[uuid.UUID]int{}
	for _, id := range got {
		per[id]++
	}
	if per[noisy.ID] > 3 || per[w.Tenant] != 1 {
		t.Fatalf("claimed %v: want at most 3 of the noisy tenant and the quiet one's", per)
	}
	if _, err := w.DB.Admin.Exec(ctx, `UPDATE triggers SET lease_until = NULL`); err != nil {
		t.Fatal(err)
	}

	c := &ingest.Cron{Store: w.Store, Logger: slog.New(slog.DiscardHandler)}
	for range 3 { // enough ticks for every noisy schedule to have been tried
		if _, err := c.Tick(ctx); err != nil {
			t.Fatalf("a failing schedule stopped the tick: %v", err)
		}
		if _, err := w.DB.Admin.Exec(ctx, `UPDATE triggers SET lease_until = NULL`); err != nil {
			t.Fatal(err)
		}
	}
	if n := w.runCount(t, wf); n != 1 {
		t.Errorf("quiet tenant's schedule fired %d times, want 1", n)
	}
	var disabled bool
	if err := w.DB.Admin.QueryRow(ctx, `SELECT next_fire_at = 'infinity' FROM triggers WHERE id = $1`, never).Scan(&disabled); err != nil || !disabled {
		t.Errorf("never-firing schedule not disabled: %v %v", disabled, err)
	}
}

// Deliveries to tenants that do not exist are refused; the edge answers
// with the security headers.
func TestUnknownTenantAndSecurityHeaders(t *testing.T) {
	w := newWorld(t)
	resp, err := http.Post(w.srv.URL+"/hooks/"+uuid.NewString()+"/anything", "application/json", bytes.NewReader([]byte(`{}`)))
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != 404 {
		t.Errorf("unknown tenant: %d", resp.StatusCode)
	}
	if csp := resp.Header.Get("Content-Security-Policy"); !strings.Contains(csp, "form-action 'self'") || resp.Header.Get("X-Content-Type-Options") != "nosniff" {
		t.Errorf("security headers: %v", resp.Header)
	}
}

var replayManifest = []byte(`
manifest: connector/v1
id: replay
version: 1.0.0
name: Replay test
description: A trigger whose dedup expression can come out empty.
category: messaging
auth:
  type: api_key
  fields:
    - { key: secret, label: Secret, secret: true }
base_url: https://replay.test
egress_hosts: [replay.test]
actions:
  noop: { title: No-op, class: read, input: { type: object, properties: {} }, output: { type: object, properties: {} } }
triggers:
  status:
    type: webhook
    verify: { scheme: header_secret, header: X-Secret, secret_field: secret }
    event_type: =body.kind
    dedup: '=has(body.id) ? body.id : ""'
    correlation: =body.ref
`)

// An empty dedup key falls back to the verified body's hash: a replayed
// delivery is still recognised.
func TestEmptyDedupFallsBackToTheBody(t *testing.T) {
	w := newWorld(t)
	if err := w.Registry.Register(&connector.Connector{Manifest: connector.MustParse(replayManifest), Actions: map[string]connector.Action{"noop": connector.ActionFunc(func(context.Context, connector.Request) (connector.Response, error) {
		return connector.Response{}, nil
	})}}); err != nil {
		t.Fatal(err)
	}
	if _, err := w.Vault.CreateConnection(ctx, w.Tenant, "prod", "replay", "main", "api_key", map[string]string{"secret": "s1"}, "test"); err != nil {
		t.Fatal(err)
	}
	deliver := func(body string) string {
		req, _ := http.NewRequest("POST", w.srv.URL+"/hooks/"+w.Tenant.String()+"/connectors/replay@1/status", bytes.NewReader([]byte(body)))
		req.Header.Set("X-Secret", "s1")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = resp.Body.Close() }()
		b, _ := io.ReadAll(resp.Body)
		return string(b)
	}
	if out := deliver(`{"kind":"paid","ref":"r1"}`); !strings.Contains(out, `"duplicate":false`) {
		t.Fatalf("first: %s", out)
	}
	if out := deliver(`{"kind":"paid","ref":"r1"}`); !strings.Contains(out, `"duplicate":true`) {
		t.Errorf("replay accepted again: %s", out)
	}
	if out := deliver(`{"kind":"failed","ref":"r1"}`); !strings.Contains(out, `"duplicate":false`) {
		t.Errorf("a different delivery: %s", out)
	}
}
