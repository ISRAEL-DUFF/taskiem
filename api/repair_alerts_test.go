package api_test

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/israel-duff/taskiem/engine/alerts"
	"github.com/israel-duff/taskiem/engine/whatsapp"
)

// A repair proposal raises a repair_proposed alert on the rule's channels,
// once, masked (no explanation, diff, secrets or run data); the run page
// links the failed run and the run that resumed it, both ways.
func TestRepairAlertsAndForkLinks(t *testing.T) {
	rw := newRepairWorld(t, patcher{class: "data", replace: [2]string{missingCustomer, defaulted}}.respond)
	ctx := context.Background()
	mail, hook := &fakeMail{}, newSink(t)
	al := &alerts.Alerter{Pool: rw.env.Store.Pool, Secrets: rw.env.Vault, Egress: rw.env.Egress, Mailer: mail, From: "alerts@taskiem.test", PublicURL: "https://taskiem.test",
		Rewrite: strings.NewReplacer("https://hooks.example.test", hook.srv.URL).Replace}
	rw.srv.Alerts = al
	email := rw.owner.must(201, "POST", "/v1/alerts/channels", map[string]any{"kind": "email", "name": "Ops", "to": []string{"ops@acme.test"}})["id"].(string)
	wh := rw.owner.must(201, "POST", "/v1/alerts/channels", map[string]any{"kind": "webhook", "name": "Hook", "url": "https://hooks.example.test/taskiem"})["id"].(string)
	kinds := rw.owner.must(200, "GET", "/v1/alerts/rules", nil)["kinds"].([]any)
	if !slices.Contains(kinds, any("repair_proposed")) {
		t.Fatalf("kinds: %v", kinds)
	}
	rw.owner.must(201, "POST", "/v1/alerts/rules", map[string]any{"name": "Fixes", "kind": "repair_proposed", "config": map[string]any{"environment": "prod"},
		"channel_ids": []string{email, wh}})
	// Another environment's proposals are not this rule's.
	rw.owner.must(201, "POST", "/v1/alerts/rules", map[string]any{"name": "Dev fixes", "kind": "repair_proposed", "config": map[string]any{"environment": "dev"},
		"channel_ids": []string{email}})

	const secret = "sk_live_ALERT_MEMO_4410"
	rw.owner.must(204, "PUT", "/v1/secrets/prod/charge_memo", map[string]any{"value": secret})
	wf := rw.publish(t, stepCharge+","+stepVerify+","+stepNotify)
	run := rw.failRun(t, wf, map[string]any{"amount": 500, "id": "o-9", "email": "ada@example.com"}, "failed")
	p := rw.only(t, run)
	if p["status"] != "proposed" {
		t.Fatalf("proposal: %v", p)
	}
	al.Now = func() time.Time { return time.Now().Add(2 * time.Second) }
	for range 2 { // once only
		if err := al.Tick(ctx); err != nil {
			t.Fatal(err)
		}
	}
	msgs := mail.all()
	if len(msgs) != 1 {
		t.Fatalf("emails: %d\n%s", len(msgs), strings.Join(msgs, "\n---\n"))
	}
	if !strings.Contains(msgs[0], "Subject: [Taskiem] Fix proposed for loan in prod") || !strings.Contains(msgs[0], "https://taskiem.test/runs/"+run) {
		t.Errorf("email:\n%s", msgs[0])
	}
	bodies, _ := hook.got()
	if len(bodies) != 1 {
		t.Fatalf("webhooks: %v", bodies)
	}
	var payload map[string]any
	_ = json.Unmarshal([]byte(bodies[0]), &payload)
	detail, _ := payload["detail"].(map[string]any)
	if payload["kind"] != "repair_proposed" || detail["proposal_id"] != p["id"] || detail["class"] != "data" || detail["run_id"] != run {
		t.Errorf("payload: %v", payload)
	}
	for _, leak := range []string{secret, "ada@example.com", "Patched", "has(trigger.body.customer)"} {
		if strings.Contains(msgs[0]+bodies[0], leak) {
			t.Errorf("the alert carries %q", leak)
		}
	}
	// On WhatsApp the generic alert template carries the title only.
	m := whatsapp.AlertMessage("Acme", alerts.RepairProposed, "Fix proposed for loan in prod", "body", "https://taskiem.test/runs/"+run, detail)
	if m.Template == nil || m.Template.Name != whatsapp.TplAlert.Name || m.Vars["title"] != "Fix proposed for loan in prod" {
		t.Errorf("whatsapp: %+v", m)
	}

	// Accept, resume: the two runs link to each other.
	acc := rw.owner.must(200, "POST", "/v1/repairs/"+p["id"].(string)+"/accept", nil)
	resumed := acc["resumed_run_id"].(string)
	child := rw.owner.must(200, "GET", "/v1/runs/"+resumed, nil)["forks"].(map[string]any)
	if child["parent_run_id"] != run || child["resumed_step"] == nil || len(child["resumed_by"].([]any)) != 0 {
		t.Errorf("resumed run's forks: %v", child)
	}
	parent := rw.owner.must(200, "GET", "/v1/runs/"+run, nil)["forks"].(map[string]any)
	if parent["parent_run_id"] != nil || !slices.Equal(parent["resumed_by"].([]any), []any{resumed}) {
		t.Errorf("failed run's forks: %v", parent)
	}
}
