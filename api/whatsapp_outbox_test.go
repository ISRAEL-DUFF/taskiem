package api_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/israel-duff/taskiem/engine/alerts"
	"github.com/israel-duff/taskiem/engine/whatsapp/whatsapptest"
)

// due makes every pending outbox message due now, as if its backoff had
// passed.
func (w *waWorld) due(t *testing.T) {
	t.Helper()
	if _, err := w.env.DB.Admin.Exec(context.Background(), `UPDATE whatsapp_outbox SET next_attempt_at = now() WHERE status = 'pending'`); err != nil {
		t.Fatal(err)
	}
}

func (w *waWorld) outbox(t *testing.T, c *client, status string) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, m := range c.must(200, "GET", "/v1/whatsapp/outbox?status="+status, nil)["messages"].([]any) {
		out = append(out, m.(map[string]any))
	}
	return out
}

// Approval requests that fail to send are retried from the outbox with
// backoff, sent once when the Graph API recovers, and after the last
// attempt kept as dead letters people can see and send again.
func TestWhatsAppOutboxRetries(t *testing.T) {
	w := newWAWorld(t)
	owner := w.tenant(t, "Acme", "owner@acme.test")
	alice := w.member(t, owner, "alice@acme.test", "approver", "credit_officer")
	bob := w.member(t, owner, "bob@acme.test", "approver", "credit_officer")
	const an, bn = "+2348010000001", "+2348010000002"
	w.bind(t, alice, an)
	w.bind(t, bob, bn)
	wf := publishFlow(t, owner, waLoanFlow)

	// Bob's number fails for a while; Alice's works.
	w.graph.SetFailTo(bn, 131000)
	owner.must(201, "POST", "/v1/workflows/"+wf+"/runs", map[string]any{"input": map[string]any{"bvn": "22212345678", "amount": 5000, "account_number": "0123456789"}})
	w.tick(t)
	if n := countText(w.graph.Sent(an), "Approval needed"); n != 1 {
		t.Fatalf("alice got %d requests", n)
	}
	pending := w.outbox(t, owner, "pending")
	if len(pending) != 1 || pending[0]["email"] != "bob@acme.test" || pending[0]["kind"] != "approval" || pending[0]["attempts"] != float64(1) {
		t.Fatalf("pending: %v", pending)
	}
	errText, _ := pending[0]["last_error"].(string)
	if !strings.Contains(errText, "131000") || strings.Contains(errText, bn) || strings.Contains(errText, "22212345678") {
		t.Errorf("last error: %q", errText)
	}
	// Not due yet: nothing is sent again, to anyone.
	w.tick(t)
	if n := len(w.outbox(t, owner, "pending")); n != 1 || countText(w.graph.Sent(an), "Approval needed") != 1 {
		t.Fatal("retried before its backoff, or resent to alice")
	}
	// Recovered: the retry reaches Bob once, with working buttons.
	w.graph.SetFailTo(bn, 0)
	w.due(t)
	w.tick(t)
	if got := w.graph.Sent(bn); countText(got, "Approval needed") != 1 || len(got[len(got)-1].Payloads) != 2 {
		t.Fatalf("bob: %+v", got)
	}
	if countText(w.graph.Sent(an), "Approval needed") != 1 {
		t.Error("alice was asked again")
	}
	if len(w.outbox(t, owner, "pending")) != 0 || len(w.outbox(t, owner, "sent")) != 2 {
		t.Errorf("after recovery: pending %v sent %v", w.outbox(t, owner, "pending"), w.outbox(t, owner, "sent"))
	}
	w.due(t)
	w.tick(t)
	if countText(w.graph.Sent(bn), "Approval needed") != 1 {
		t.Error("a sent message was sent again")
	}

	// A second run: Bob's number never recovers; after the last attempt the
	// message is a dead letter.
	w.graph.SetFailTo(bn, 131000)
	owner.must(201, "POST", "/v1/workflows/"+wf+"/runs", map[string]any{"input": map[string]any{"bvn": "22212345679", "amount": 10, "account_number": "0123456780"}})
	for range 8 {
		w.tick(t)
		w.due(t)
	}
	dead := w.outbox(t, owner, "dead")
	if len(dead) != 1 || dead[0]["attempts"] != float64(8) || dead[0]["email"] != "bob@acme.test" {
		t.Fatalf("dead letters: %v", dead)
	}
	alice.must(403, "GET", "/v1/whatsapp/outbox", nil)
	// Sent again by hand once the number works.
	w.graph.SetFailTo(bn, 0)
	owner.must(204, "POST", "/v1/whatsapp/outbox/"+dead[0]["id"].(string)+"/retry", nil)
	owner.must(404, "POST", "/v1/whatsapp/outbox/"+dead[0]["id"].(string)+"/retry", nil)
	w.tick(t)
	if countText(w.graph.Sent(bn), "Approval needed") != 2 || len(w.outbox(t, owner, "dead")) != 0 {
		t.Error("the dead letter was not sent again")
	}
	if !strings.Contains(toJSON(owner.must(200, "GET", "/v1/audit", nil)), "whatsapp.outbox.retry") {
		t.Error("retry not audited")
	}
	// Decided meanwhile: a queued request is dropped, not sent.
	w.graph.SetFailTo(an, 131000)
	run3 := owner.must(201, "POST", "/v1/workflows/"+wf+"/runs", map[string]any{"input": map[string]any{"bvn": "22212345670", "amount": 11, "account_number": "0123456781"}})["run_id"].(string)
	w.tick(t)
	owner.must(200, "POST", "/v1/runs/"+run3+"/cancel", nil)
	w.graph.SetFailTo(an, 0)
	w.due(t)
	w.tick(t)
	if n := countText(w.graph.Sent(an), "Approval needed"); n != 2 { // the first two runs only
		t.Errorf("alice got %d requests", n)
	}
	if d := w.outbox(t, owner, "dropped"); len(d) != 1 || d[0]["email"] != "alice@acme.test" {
		t.Errorf("dropped: %v", d)
	}
	// Other tenants see none of it.
	other := w.tenant(t, "Other", "owner@other.test")
	if len(w.outbox(t, other, "sent")) != 0 {
		t.Error("another tenant sees this tenant's outbox")
	}
}

// An alert to a WhatsApp channel goes to each member once: when one
// member's message fails, only that one is retried.
func TestWhatsAppAlertRetriesPerMember(t *testing.T) {
	w := newWAWorld(t)
	ctx := context.Background()
	owner := w.tenant(t, "Acme", "owner@acme.test")
	bob := w.member(t, owner, "bob@acme.test", "viewer")
	const on, bn = "+2348010000009", "+2348010000002"
	w.bind(t, owner, on)
	w.bind(t, bob, bn)
	tenant := uuid.MustParse(owner.must(200, "GET", "/v1/me", nil)["tenant_id"].(string))
	al := &alerts.Alerter{Pool: w.env.Store.Pool, Secrets: w.env.Vault, WhatsApp: w.wa, PublicURL: waPublicURL}
	w.srv.Alerts = al
	ch := owner.must(201, "POST", "/v1/alerts/channels", map[string]any{"kind": "whatsapp", "name": "ops", "to": []string{"owner@acme.test", "bob@acme.test"}})["id"].(string)
	owner.must(201, "POST", "/v1/alerts/rules", map[string]any{"name": "fails", "kind": "run_failed", "channel_ids": []string{ch}})
	wf := publishFlow(t, owner, waLoanFlow)
	if _, err := w.env.DB.Admin.Exec(ctx, `INSERT INTO runs (id, tenant_id, workflow_id, version, environment, status, started_at, ended_at)
		VALUES ($1, $2, $3, 1, 'prod', 'failed', now(), now() + interval '1 second')`, uuid.Must(uuid.NewV7()), tenant, wf); err != nil {
		t.Fatal(err)
	}
	w.graph.SetFailTo(bn, 131000)
	al.Now = func() time.Time { return time.Now().Add(2 * time.Second) }
	if err := al.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	if n := countText(w.graph.Sent(on), "loan failed in prod"); n != 1 {
		t.Fatalf("owner got %d: %s %+v", n, toJSON(owner.must(200, "GET", "/v1/alerts", nil)), w.graph.Sent(""))
	}
	// The alert's delivery is done; Bob's message waits in the outbox.
	list := toJSON(owner.must(200, "GET", "/v1/alerts", nil))
	if !strings.Contains(list, `"status":"sent"`) {
		t.Errorf("alert delivery: %s", list)
	}
	if p := w.outbox(t, owner, "pending"); len(p) != 1 || p[0]["kind"] != "alert" || p[0]["email"] != "bob@acme.test" {
		t.Fatalf("pending: %v", p)
	}
	w.graph.SetFailTo(bn, 0)
	w.due(t)
	for range 2 {
		w.tick(t)
		if err := al.Tick(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if countText(w.graph.Sent(bn), "loan failed in prod") != 1 || countText(w.graph.Sent(on), "loan failed in prod") != 1 {
		t.Errorf("owner %d, bob %d", countText(w.graph.Sent(on), "loan failed in prod"), countText(w.graph.Sent(bn), "loan failed in prod"))
	}
}

// countText counts the messages whose text holds sub.
func countText(sent []whatsapptest.Sent, sub string) int {
	n := 0
	for _, s := range sent {
		if strings.Contains(s.Text, sub) {
			n++
		}
	}
	return n
}
