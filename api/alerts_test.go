package api_test

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/israel-duff/taskiem/engine/alerts"
)

type fakeMail struct {
	mu   sync.Mutex
	sent []string
}

func (m *fakeMail) Send(_ context.Context, _ string, _ []string, msg []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sent = append(m.sent, string(msg))
	return nil
}

func (m *fakeMail) all() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]string(nil), m.sent...)
}

// sink records POSTs, answering with status.
type sink struct {
	mu     sync.Mutex
	bodies []string
	hdrs   []http.Header
	status int
	srv    *httptest.Server
}

func newSink(t *testing.T) *sink {
	s := &sink{status: 200}
	s.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		s.mu.Lock()
		s.bodies, s.hdrs = append(s.bodies, string(b)), append(s.hdrs, r.Header.Clone())
		st := s.status
		s.mu.Unlock()
		w.WriteHeader(st)
	}))
	t.Cleanup(s.srv.Close)
	return s
}

func (s *sink) got() ([]string, []http.Header) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.bodies...), append([]http.Header(nil), s.hdrs...)
}

func TestAlerts(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()
	owner := w.tenant(t, "Acme", "owner@acme.test")
	var tenant uuid.UUID
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT id FROM tenants WHERE name = 'Acme'`).Scan(&tenant); err != nil {
		t.Fatal(err)
	}
	mail, slack, hook := &fakeMail{}, newSink(t), newSink(t)
	al := &alerts.Alerter{Pool: w.env.Store.Pool, Secrets: w.env.Vault, Egress: w.env.Egress, Mailer: mail, From: "alerts@taskiem.test", PublicURL: "https://taskiem.test",
		Rewrite: strings.NewReplacer("https://hooks.slack.com", slack.srv.URL, "https://hooks.example.test", hook.srv.URL).Replace}
	w.srv.Alerts = al

	// Channels; a Slack URL elsewhere, or a plain-HTTP webhook, is refused.
	owner.must(400, "POST", "/v1/alerts/channels", map[string]any{"kind": "slack", "name": "x", "url": "https://evil.test/services/x"})
	owner.must(400, "POST", "/v1/alerts/channels", map[string]any{"kind": "webhook", "name": "x", "url": "http://hooks.example.test/x"})
	owner.must(400, "POST", "/v1/alerts/channels", map[string]any{"kind": "email", "name": "x", "to": []string{"not an address"}})
	email := owner.must(201, "POST", "/v1/alerts/channels", map[string]any{"kind": "email", "name": "Ops", "to": []string{"ops@acme.test"}})["id"].(string)
	sl := owner.must(201, "POST", "/v1/alerts/channels", map[string]any{"kind": "slack", "name": "#payments", "url": "https://hooks.slack.com/services/T0/B0/abc"})["id"].(string)
	wh := owner.must(201, "POST", "/v1/alerts/channels", map[string]any{"kind": "webhook", "name": "PagerDuty", "url": "https://hooks.example.test/taskiem"})
	key := wh["signing_key"].(string)
	if strings.Contains(toJSON(owner.must(200, "GET", "/v1/alerts/channels", nil)), "T0/B0/abc") {
		t.Error("the Slack webhook URL is shown after it was saved")
	}
	owner.must(200, "POST", "/v1/alerts/channels/"+sl+"/test", nil)
	if b, _ := slack.got(); len(b) != 1 || !strings.Contains(b[0], "Test alert from Taskiem") {
		t.Fatalf("slack test: %v", b)
	}

	// A rule for failed runs in prod.
	owner.must(400, "POST", "/v1/alerts/rules", map[string]any{"name": "x", "kind": "slow_run", "config": map[string]any{"threshold": "soon"}, "channel_ids": []string{email}})
	owner.must(400, "POST", "/v1/alerts/rules", map[string]any{"name": "x", "kind": "run_failed", "channel_ids": []string{uuid.NewString()}})
	owner.must(201, "POST", "/v1/alerts/rules", map[string]any{"name": "Prod failures", "kind": "run_failed", "config": map[string]any{"environment": "prod"},
		"channel_ids": []string{email, sl, wh["id"].(string)}})
	anchors := owner.must(201, "POST", "/v1/alerts/rules", map[string]any{"name": "Anchors", "kind": "audit_anchor", "channel_ids": []string{email}})["id"].(string)
	owner.must(200, "PUT", "/v1/alerts/rules/"+anchors, map[string]any{"name": "Audit anchors", "kind": "audit_anchor", "channel_ids": []string{email}})
	owner.must(404, "PUT", "/v1/alerts/rules/"+uuid.NewString(), map[string]any{"name": "x", "kind": "audit_anchor", "channel_ids": []string{email}})

	wf := publishFlow(t, owner, loanFlow)
	time.Sleep(10 * time.Millisecond)
	for _, env := range []string{"prod", "dev"} {
		if _, err := w.env.DB.Admin.Exec(ctx, `INSERT INTO runs (id, tenant_id, workflow_id, version, environment, status, started_at, ended_at)
			VALUES ($1, $2, $3, 1, $4, 'failed', now(), now())`, uuid.Must(uuid.NewV7()), tenant, wf, env); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := w.env.DB.Admin.Exec(ctx, `INSERT INTO audit_anchors (tenant_id, chain_seq, head_hash, anchored_at, key_id, signature) VALUES ($1, 7, $2, now(), 'k1', '\x01')`,
		tenant, make([]byte, 32)); err != nil {
		t.Fatal(err)
	}
	for range 2 { // the second tick finds nothing new
		if err := al.Tick(ctx); err != nil {
			t.Fatal(err)
		}
	}

	msgs := mail.all()
	if len(msgs) != 2 {
		t.Fatalf("emails: %d\n%s", len(msgs), strings.Join(msgs, "\n----\n"))
	}
	joined := strings.Join(msgs, "\n")
	if !strings.Contains(joined, "Subject: [Taskiem] loan failed in prod") || !strings.Contains(joined, "https://taskiem.test/runs/") {
		t.Errorf("failure email:\n%s", joined)
	}
	if !strings.Contains(joined, `"seq":7`) || !strings.Contains(joined, "taskiem audit verify --anchors") {
		t.Errorf("anchor email:\n%s", joined)
	}
	if b, _ := slack.got(); len(b) != 2 || !strings.Contains(b[1], "loan failed in prod") {
		t.Errorf("slack: %v", b)
	}
	bodies, hdrs := hook.got()
	if len(bodies) != 1 {
		t.Fatalf("webhook: %v", bodies)
	}
	sig := hdrs[0].Get("Taskiem-Signature")
	ts, v1, _ := strings.Cut(strings.TrimPrefix(sig, "t="), ",v1=")
	mac := hmac.New(sha256.New, []byte(key))
	mac.Write([]byte(ts + "." + bodies[0]))
	if hex.EncodeToString(mac.Sum(nil)) != v1 {
		t.Errorf("webhook signature %q does not verify", sig)
	}
	var payload map[string]any
	_ = json.Unmarshal([]byte(bodies[0]), &payload)
	if payload["kind"] != "run_failed" || payload["rule"] != "Prod failures" {
		t.Errorf("webhook payload: %v", payload)
	}

	// A failed delivery waits and retries; the list shows why.
	hook.mu.Lock()
	hook.status = 503
	hook.mu.Unlock()
	if _, err := w.env.DB.Admin.Exec(ctx, `INSERT INTO runs (id, tenant_id, workflow_id, version, environment, status, started_at, ended_at)
		VALUES ($1, $2, $3, 1, 'prod', 'failed', now(), now() + interval '1 second')`, uuid.Must(uuid.NewV7()), tenant, wf); err != nil {
		t.Fatal(err)
	}
	al.Now = func() time.Time { return time.Now().Add(2 * time.Second) }
	if err := al.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	list := owner.must(200, "GET", "/v1/alerts", nil)["alerts"].([]any)
	latest := toJSON(list[0])
	if !strings.Contains(latest, `"status":"pending"`) || !strings.Contains(latest, "503") || !strings.Contains(latest, `"status":"sent"`) {
		t.Errorf("latest alert: %s", latest)
	}
	if len(list) != 3 {
		t.Errorf("%d alerts recorded", len(list))
	}

	// Deleting a channel takes it out of rules.
	owner.must(204, "DELETE", "/v1/alerts/channels/"+wh["id"].(string), nil)
	if strings.Contains(toJSON(owner.must(200, "GET", "/v1/alerts/rules", nil)), wh["id"].(string)) {
		t.Error("a deleted channel is still on a rule")
	}
}

func TestAlertRuleKinds(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()
	owner := w.tenant(t, "Acme", "owner@acme.test")
	var tenant uuid.UUID
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT id FROM tenants WHERE name = 'Acme'`).Scan(&tenant); err != nil {
		t.Fatal(err)
	}
	mail := &fakeMail{}
	al := &alerts.Alerter{Pool: w.env.Store.Pool, Secrets: w.env.Vault, Mailer: mail, From: "alerts@taskiem.test"}
	ch := owner.must(201, "POST", "/v1/alerts/channels", map[string]any{"kind": "email", "name": "Ops", "to": []string{"ops@acme.test"}})["id"].(string)
	for kind, cfg := range map[string]map[string]any{
		"slow_run": {"threshold": "1h"}, "needs_reconciliation": {}, "stuck_approval": {"threshold": "24h"},
		"credential_expiry": {"threshold": "72h"}, "connector_drift": {},
	} {
		owner.must(201, "POST", "/v1/alerts/rules", map[string]any{"name": kind, "kind": kind, "config": cfg, "channel_ids": []string{ch}})
	}
	wf := publishFlow(t, owner, loanFlow)
	slow, stuck, recon := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	for _, q := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO runs (id, tenant_id, workflow_id, version, environment, status, started_at) VALUES ($1, $2, $3, 1, 'prod', 'running', now() - interval '2 hours')`, []any{slow, tenant, wf}},
		{`INSERT INTO runs (id, tenant_id, workflow_id, version, environment, status, started_at) VALUES ($1, $2, $3, 1, 'prod', 'waiting', now())`, []any{stuck, tenant, wf}},
		{`INSERT INTO approvals (tenant_id, run_id, step_id, role, requested_at) VALUES ($1, $2, 'ok', 'credit_officer', now() - interval '2 days')`, []any{tenant, stuck}},
		{`INSERT INTO runs (id, tenant_id, workflow_id, version, environment, status, started_at) VALUES ($1, $2, $3, 1, 'prod', 'needs_reconciliation', now())`, []any{recon, tenant, wf}},
		{`INSERT INTO connector_drift (tenant_id, connector, version, action, path, kind, expected, observed, first_seen) VALUES ($1, 'paystack@1', '1.0.0', 'transfer', '/data/status', 'enum', 'success|failed', 'reversed', now() + interval '1 second')`, []any{tenant}},
	} {
		if _, err := w.env.DB.Admin.Exec(ctx, q.sql, q.args...); err != nil {
			t.Fatal(err)
		}
	}
	owner.must(201, "POST", "/v1/api-keys", map[string]any{"name": "ci", "permissions": []string{"run.read"}, "expires_days": 2})
	al.Now = func() time.Time { return time.Now().Add(2 * time.Second) }
	if err := al.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(mail.all(), "\n")
	for _, want := range []string{"loan has been running over 1h0m0s in prod", "Approval waiting over 24h0m0s: loan", "loan needs reconciliation in prod",
		"paystack@1 changed its response to transfer", "The api key ci expires"} {
		if !strings.Contains(joined, want) {
			t.Errorf("no alert %q in:\n%s", want, joined)
		}
	}
	if n := len(mail.all()); n != 5 {
		t.Errorf("%d emails", n)
	}
}
