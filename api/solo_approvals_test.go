package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	sfconn "github.com/israel-duff/taskiem/connectors/storefront"
	"github.com/israel-duff/taskiem/engine/sender"
	"github.com/israel-duff/taskiem/engine/storefront"
	"github.com/israel-duff/taskiem/engine/whatsapp/whatsapptest"
)

// A one-person business does what an owner does (no-technical-steps N1):
// signs up, makes the page, turns orders on as the web app does
// (publishing the default workflow), a customer orders on the public host,
// and the owner accepts in chat and in the web app; the customer is told.
// Four-eyes still holds once a second person can decide, and under a
// policy that forbids it.
func TestSoleOwnerAcceptsOrders(t *testing.T) {
	w := newWAWorld(t)
	ctx := context.Background()
	key, _ := storefront.InboxKey(bytes.Repeat([]byte{3}, 32))
	w.srv.PublicHosts = []string{shopDomain}
	w.srv.Orders = &storefront.Orders{Pool: w.env.DB.App, Vault: w.env.Vault, InboxKey: key, PreviewDomain: shopDomain,
		Messages: &sender.Queue{Vault: w.env.Vault, HashKey: bytes.Repeat([]byte{8}, 32)},
		Start:    w.env.Store.Start, Signal: w.env.Store.DeliverSignalOnce, Cancel: w.env.Store.CancelRun}
	if err := w.env.Registry.Register(sfconn.New(w.srv.Orders)); err != nil {
		t.Fatal(err)
	}
	owner := w.tenant(t, "Ada's Kitchen", "ada@ada.test")
	verifyPhone(t, w.world, owner, "phone", "business")
	const on = "+2348010000041"
	w.bind(t, owner, on)
	owner.must(200, "PUT", "/v1/storefront", map[string]any{"slug": "ada-kitchen", "draft": map[string]any{"name": "Ada's Kitchen"}})

	// Turn orders on as the Forms page does: the template, published by
	// the owner, then the form.
	wf := owner.must(201, "POST", "/v1/templates/storefront-order/instantiate", map[string]any{"params": map[string]any{}, "name": "Orders"})["id"].(string)
	if st := owner.must(200, "POST", "/v1/workflows/"+wf+"/versions/1/publish", nil)["state"]; st != "published" {
		t.Fatalf("publish: %v", st)
	}
	owner.must(200, "PUT", "/v1/storefront/actions/order", map[string]any{"enabled": true, "payment": "none"})
	sfID := uuid.MustParse(owner.must(200, "POST", "/v1/storefront/publish", nil)["id"].(string))
	tenant := tenantOf(t, owner)

	order := func(name string) (ref, run string) {
		t.Helper()
		ref = storefront.NewReference("ada-kitchen")
		_, hash := storefront.NewStatusKey()
		ct, _ := storefront.SealSubmission(key, sfID, ref, storefront.Submission{Kind: "order", Payment: "none", Channel: "direct", Submitted: time.Now(),
			Basket: []storefront.Line{{ID: "1", Name: "Jollof rice", Quantity: 1, PriceKobo: 150000, LineKobo: 150000}}, TotalKobo: 150000, Fulfilment: "pickup",
			Customer: storefront.Customer{Name: name, Phone: "+2348031234567"}})
		var got *string
		if err := w.env.DB.PublicPool(t, 1).QueryRow(ctx, `SELECT taskiem_public_submit($1, 'order', $2, $3, $4, $5)`,
			sfID, ref, bytes.Repeat([]byte{7}, 32), hash, ct).Scan(&got); err != nil || got == nil {
			t.Fatalf("submit: %v %v", got, err)
		}
		if err := w.srv.Orders.Drain(ctx); err != nil {
			t.Fatal(err)
		}
		w.env.Drain(t)
		if err := w.env.DB.Admin.QueryRow(ctx, `SELECT run_id::text FROM storefront_orders WHERE reference = $1`, ref).Scan(&run); err != nil {
			t.Fatal(err)
		}
		return ref, run
	}
	status := func(ref string) string {
		t.Helper()
		var s string
		if err := w.env.DB.Admin.QueryRow(ctx, `SELECT status FROM storefront_orders WHERE reference = $1`, ref).Scan(&s); err != nil {
			t.Fatal(err)
		}
		return s
	}
	// The customer's messages (SMS) queued so far.
	told := func() int {
		t.Helper()
		var n int
		if err := w.env.DB.Admin.QueryRow(ctx, `SELECT count(*) FROM platform_messages WHERE tenant_id = $1 AND channel = 'sms' AND purpose = 'confirmation'`, tenant).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}

	// In chat: the request names the step, says the owner is the only
	// approver, and the owner's tap is recorded.
	ref1, _ := order("Bola Ade")
	sent := told()
	w.tick(t)
	req := w.graph.Last(t, on)
	if !strings.Contains(req.Text, "Approval needed: Orders · Accept or decline the order") || !strings.Contains(req.Text, "You are the only person who can approve this.") {
		t.Fatalf("the request: %q", req.Text)
	}
	got := w.send(t, on, whatsapptest.QuickReply(req.Payloads[0], "Approve")) // the window is closed: the template
	if len(got) != 1 || !strings.Contains(got[0].Text, "Recorded: you approved Accept or decline the order") || !strings.Contains(got[0].Text, "request is approved") {
		t.Fatalf("approve in chat: %+v", got)
	}
	w.env.Drain(t)
	if s := status(ref1); s != "accepted" && s != "paid" {
		t.Fatalf("after accepting in chat: %s", s)
	}
	if told() <= sent {
		t.Error("the customer was not told")
	}

	// In the web app: the inbox says so, and the decision is self-approved
	// on the record.
	ref2, run2 := order("Chika Obi")
	sent = told()
	var it map[string]any
	for _, a := range owner.must(200, "GET", "/v1/approvals", nil)["approvals"].([]any) {
		if a := a.(map[string]any); a["run_id"] == run2 {
			it = a
		}
	}
	if it["step_name"] != "Accept or decline the order" || it["sole_approver"] != true || it["run_id"] != run2 {
		t.Fatalf("inbox item: %v", it)
	}
	res := owner.must(200, "POST", "/v1/approvals/"+run2+"/decide", map[string]any{"decision": "approved"})
	if res["status"] != "approved" || res["self_approved"] != true {
		t.Fatalf("approve on the web: %v", res)
	}
	w.env.Drain(t)
	if s := status(ref2); s != "accepted" && s != "paid" {
		t.Fatalf("after accepting on the web: %s", s)
	}
	if told() <= sent {
		t.Error("the customer was not told")
	}
	audit := toJSON(owner.must(200, "GET", "/v1/audit?action=approval.decide", nil))
	if strings.Count(audit, `"self_approved":true`) != 2 {
		t.Errorf("self-approvals not audited: %s", audit)
	}
	var marked int
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT count(*) FROM approval_decisions WHERE tenant_id = $1 AND self_approved`, tenant).Scan(&marked); err != nil || marked != 2 {
		t.Errorf("decisions marked self-approved: %d %v", marked, err)
	}
	var events int
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT count(*) FROM run_events WHERE run_id = $1 AND type = 'ApprovalDecided' AND payload->>'self_approved' = 'true'`, run2).Scan(&events); err != nil || events != 1 {
		t.Errorf("the run's history: %d %v", events, err)
	}

	// A second owner joins: four-eyes holds again for the one who
	// published, and the newcomer decides.
	partner := w.member(t, owner, "tunde@ada.test", "owner")
	ref3, run3 := order("Dayo Ojo")
	for _, a := range owner.must(200, "GET", "/v1/approvals", nil)["approvals"].([]any) {
		if a := a.(map[string]any); a["sole_approver"] != false {
			t.Errorf("not the only approver now: %v", a)
		}
	}
	st, body := owner.do("POST", "/v1/approvals/"+run3+"/decide", map[string]any{"decision": "approved"})
	if st != 403 || !strings.Contains(toJSON(body), "maker") {
		t.Fatalf("the maker with a partner: %d %v", st, body)
	}
	if res := partner.must(200, "POST", "/v1/approvals/"+run3+"/decide", map[string]any{"decision": "approved"}); res["self_approved"] != false {
		t.Errorf("the partner's vote: %v", res)
	}
	w.env.Drain(t)
	if s := status(ref3); s != "accepted" && s != "paid" {
		t.Errorf("after the partner accepted: %s", s)
	}
}

const strictPolicy = `{"rules":[{"when":"=true","levels":[{"role":"owner"}]}],"constraints":{"allow_sole_approver":false}}`

const strictFlow = `{"schema":"wd/v1","id":"wf_strict","version":1,"name":"strict","trigger":{"type":"manual"},
  "steps":[{"id":"ok","type":"approval","name":"Pay the supplier","config":{"policy":"strict","subject":{"amount":"=trigger.body.amount"}}},
           {"id":"done","type":"transform","needs":["ok"],"config":{"output":"=steps.ok.output.decision"}}]}`

// A policy can forbid the one person able to decide from approving what
// they made; without it, the same owner may.
func TestSoleApproverPolicy(t *testing.T) {
	w := newWorld(t)
	owner := w.tenant(t, "Acme", "owner@acme.test")
	owner.must(201, "PUT", "/v1/policies/strict", map[string]any{"document": json.RawMessage(strictPolicy)})
	wf := publishFlow(t, owner, strictFlow)
	run := owner.must(201, "POST", "/v1/workflows/"+wf+"/runs", map[string]any{"input": map[string]any{"amount": 10}})["run_id"].(string)
	it := owner.must(200, "GET", "/v1/approvals", nil)["approvals"].([]any)[0].(map[string]any)
	if it["sole_approver"] != false || it["step_name"] != "Pay the supplier" {
		t.Errorf("under a strict policy: %v", it)
	}
	st, body := owner.do("POST", "/v1/approvals/"+run+"/ok", map[string]any{"decision": "approved"})
	if st != 403 || !strings.Contains(toJSON(body), "maker") {
		t.Fatalf("strict policy: %d %v", st, body)
	}

	// The same flow without a policy: the owner is the only approver.
	loose := strings.Replace(strings.Replace(strictFlow, `"policy":"strict",`, `"role":"owner",`, 1), "wf_strict", "wf_loose", 1)
	wf2 := publishFlow(t, owner, loose)
	run2 := owner.must(201, "POST", "/v1/workflows/"+wf2+"/runs", map[string]any{"input": map[string]any{"amount": 10}})["run_id"].(string)
	if res := owner.must(200, "POST", "/v1/approvals/"+run2+"/ok", map[string]any{"decision": "approved"}); res["status"] != "approved" || res["self_approved"] != true {
		t.Fatalf("without a policy: %v", res)
	}
	// A member who holds a role an owner also counts for sees and decides
	// it: owners count for every role.
	other := `{"schema":"wd/v1","id":"wf_cfo","version":1,"name":"cfo","trigger":{"type":"manual"},
	  "steps":[{"id":"ok","type":"approval","config":{"role":"cfo"}}]}`
	wf3 := publishFlow(t, owner, other)
	run3 := owner.must(201, "POST", "/v1/workflows/"+wf3+"/runs", map[string]any{"input": map[string]any{}})["run_id"].(string)
	if res := owner.must(200, "POST", "/v1/approvals/"+run3+"/ok", map[string]any{"decision": "approved"}); res["status"] != "approved" {
		t.Fatalf("an owner for another role: %v", res)
	}
}
