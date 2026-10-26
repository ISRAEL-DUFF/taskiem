package api_test

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/israel-duff/taskiem/engine/whatsapp"
	"github.com/israel-duff/taskiem/engine/whatsapp/whatsapptest"
)

// The template registry for an own number (docs/whatsapp.md#templates):
// the Channels page lists each template's status in the number's WhatsApp
// Business Account, submits one, hears Meta's answer on the status
// webhook and from Check now; outside the window only an approved
// template goes, and the registry is the tenant's.
func TestWhatsAppTemplateRegistry(t *testing.T) {
	w := newWAWorld(t)
	ctx := context.Background()
	const wabaA = "300000000000001"
	w.graph.AddNumber(pnA, tokA, "+234 800 000 0001")
	w.graph.StrictTemplates(wabaA) // Meta holds nothing for Alpha yet
	alpha := w.tenant(t, "Alpha", "owner@alpha.test")
	beta := w.tenant(t, "Beta", "owner@beta.test")

	// Without an own number the page lists the library only.
	v := alpha.must(200, "GET", "/v1/whatsapp/number/templates", nil)
	if v["tracked"] != false || len(v["templates"].([]any)) != len(whatsapp.AllTemplates) {
		t.Fatalf("untracked: %v", v)
	}
	alpha.must(409, "POST", "/v1/whatsapp/number/templates/check", nil)

	alpha.must(200, "PUT", "/v1/whatsapp/number", map[string]any{"phone_number_id": pnA, "waba_id": wabaA, "access_token": tokA, "app_secret": secA, "verify_token": vtA})
	statusOf := func(c *client, name string) (string, map[string]any) {
		t.Helper()
		v := c.must(200, "GET", "/v1/whatsapp/number/templates", nil)
		for _, x := range v["templates"].([]any) {
			m := x.(map[string]any)
			if m["name"] == name {
				return m["status"].(string), v
			}
		}
		t.Fatalf("%s not listed: %v", name, v)
		return "", nil
	}
	st, v := statusOf(alpha, "taskiem_invoice")
	if st != "not_submitted" || v["tracked"] != true || !strings.Contains(v["note"].(string), "taskiem_invoice") {
		t.Fatalf("after connecting: %s %v", st, v)
	}

	// Submit: Meta answers pending, with its id; a second submission is
	// refused by Meta and said so.
	viewer := w.member(t, alpha, "viewer@alpha.test", "viewer")
	viewer.must(403, "POST", "/v1/whatsapp/number/templates/taskiem_invoice/submit", nil)
	alpha.must(404, "POST", "/v1/whatsapp/number/templates/nope/submit", nil)
	sub := alpha.must(200, "POST", "/v1/whatsapp/number/templates/taskiem_invoice/submit", nil)
	if sub["status"] != "pending" || sub["meta_id"] == "" || sub["submitted_at"] == nil {
		t.Fatalf("submitted: %v", sub)
	}
	def := w.graph.Submitted(wabaA)["taskiem_invoice"]
	raw := toJSON(def)
	if def["category"] != "UTILITY" || def["language"] != "en" || !strings.Contains(raw, `"url":"https://forms.taskiem.test/i/{{1}}"`) || !strings.Contains(raw, `"QUICK_REPLY"`) ||
		!strings.Contains(raw, `"body_text":[["Ada Stores","INV-0104","₦25,000"]]`) {
		t.Fatalf("submission: %s", raw)
	}
	alpha.must(400, "POST", "/v1/whatsapp/number/templates/taskiem_invoice/submit", nil)
	if !strings.Contains(toJSON(alpha.must(200, "GET", "/v1/audit", nil)), `"whatsapp.template.submit"`) {
		t.Error("submission not audited")
	}

	// Meta's answer on the status webhook, signed with Alpha's app secret.
	_, id := w.graph.Template(wabaA, "taskiem_invoice")
	body := whatsapptest.TemplateStatusDelivery(wabaA, "taskiem_invoice", "en", id, "APPROVED", "")
	if st, _ := w.post(t, "/channels/whatsapp/n/"+pnA, body, whatsapp.Sign(secA, body)); st != 200 {
		t.Fatalf("status webhook: %d", st)
	}
	if st, _ := statusOf(alpha, "taskiem_invoice"); st != "approved" {
		t.Fatalf("after the webhook: %s", st)
	}
	// A rejection carries the reason; the Channels page shows it.
	w.graph.SetTemplateStatus(wabaA, "taskiem_receipt", "PENDING", "")
	_, rid := w.graph.Template(wabaA, "taskiem_receipt")
	body = whatsapptest.TemplateStatusDelivery(wabaA, "taskiem_receipt", "en", rid, "REJECTED", "INVALID_FORMAT")
	w.post(t, "/channels/whatsapp/n/"+pnA, body, whatsapp.Sign(secA, body))
	st, v = statusOf(alpha, "taskiem_receipt")
	if st != "rejected" {
		t.Fatalf("rejected: %s", st)
	}
	for _, x := range v["templates"].([]any) {
		if m := x.(map[string]any); m["name"] == "taskiem_receipt" && m["reason"] != "INVALID_FORMAT" {
			t.Errorf("reason: %v", m)
		}
	}
	// An unsigned delivery, or one for an unknown account, changes nothing.
	body = whatsapptest.TemplateStatusDelivery(wabaA, "taskiem_receipt", "en", rid, "APPROVED", "")
	if st, _ := w.post(t, "/channels/whatsapp/n/"+pnA, body, whatsapp.Sign("wrong", body)); st != 401 {
		t.Errorf("unsigned: %d", st)
	}
	body = whatsapptest.TemplateStatusDelivery("300000000000099", "taskiem_receipt", "en", rid, "APPROVED", "")
	w.post(t, "/channels/whatsapp/n/"+pnA, body, whatsapp.Sign(secA, body))
	if st, _ := statusOf(alpha, "taskiem_receipt"); st != "rejected" {
		t.Errorf("after bad deliveries: %s", st)
	}

	// Check now asks Meta: what it lists is recorded, the rest not submitted.
	w.graph.SetTemplateStatus(wabaA, "taskiem_invoice", "APPROVED", "")
	w.graph.SetTemplateStatus(wabaA, "taskiem_receipt", "APPROVED", "")
	w.graph.SetTemplateStatus(wabaA, "taskiem_otp", "APPROVED", "")
	w.graph.SetTemplateStatus(wabaA, "taskiem_run_failed", "PAUSED", "")
	chk := alpha.must(200, "POST", "/v1/whatsapp/number/templates/check", nil)
	got := map[string]string{}
	for _, x := range chk["templates"].([]any) {
		m := x.(map[string]any)
		got[m["name"].(string)] = m["status"].(string)
	}
	if got["taskiem_receipt"] != "approved" || got["taskiem_otp"] != "approved" || got["taskiem_run_failed"] != "paused" || got["taskiem_invoice"] != "approved" || got["taskiem_alert"] != "not_submitted" {
		t.Fatalf("checked: %v", got)
	}
	// Beta sees nothing of Alpha's account.
	if bv := beta.must(200, "GET", "/v1/whatsapp/number/templates", nil); bv["tracked"] != false {
		t.Errorf("beta: %v", bv)
	}

	// Sending from Alpha's number outside the window: the approved code
	// goes; an alert whose template is paused is not sent, and said why.
	const na = "+2348011110001"
	w.bind(t, alpha, na)
	if s := w.graph.Last(t, na); s.Template != "taskiem_otp" || s.From != pnA {
		t.Fatalf("code: %+v", s)
	}
	own, err := w.wa.ForTenant(ctx, tenantID(t, alpha))
	if err != nil || !own.Own() || own.WABA != wabaA {
		t.Fatalf("own platform: %v %v", own, err)
	}
	before := len(w.graph.Sent(na))
	err = own.Notify(ctx, tenantID(t, alpha), []uuid.UUID{userID(t, alpha)}, "run_failed", "payroll failed", "x", "", map[string]any{"workflow": "payroll", "environment": "prod"})
	if err == nil || !strings.Contains(err.Error(), "not approved") || !strings.Contains(err.Error(), "paused") || len(w.graph.Sent(na)) != before {
		t.Fatalf("paused template: %v, %d sent", err, len(w.graph.Sent(na))-before)
	}
	w.graph.SetTemplateStatus(wabaA, "taskiem_run_failed", "APPROVED", "")
	alpha.must(200, "POST", "/v1/whatsapp/number/templates/check", nil)
	if err := own.Notify(ctx, tenantID(t, alpha), []uuid.UUID{userID(t, alpha)}, "run_failed", "payroll failed", "x", "", map[string]any{"workflow": "payroll", "environment": "prod"}); err != nil {
		t.Fatal(err)
	}
	if s := w.graph.Last(t, na); s.Template != "taskiem_run_failed" || s.From != pnA {
		t.Fatalf("approved template: %+v", s)
	}
	// The hourly poll covers own numbers too.
	w.graph.SetTemplateStatus(wabaA, "taskiem_alert", "APPROVED", "")
	if err := w.wa.PollTemplates(ctx); err != nil {
		t.Fatal(err)
	}
	if st, _ := statusOf(alpha, "taskiem_alert"); st != "approved" {
		t.Errorf("after the poll: %s", st)
	}

	// Disconnecting the number leaves the page untracked again.
	alpha.must(204, "DELETE", "/v1/whatsapp/number", nil)
	if v := alpha.must(200, "GET", "/v1/whatsapp/number/templates", nil); v["tracked"] != false {
		t.Errorf("after disconnecting: %v", v)
	}
}
