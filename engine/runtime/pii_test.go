package runtime_test

import (
	"strings"
	"testing"

	"github.com/israel-duff/taskiem/engine/decide"
	"github.com/israel-duff/taskiem/engine/history"
	"github.com/israel-duff/taskiem/engine/pii"
	rt "github.com/israel-duff/taskiem/engine/runtime/runtimetest"
	"github.com/israel-duff/taskiem/engine/wd"
)

const piiFlow = `{"schema":"wd/v1","id":"wf_kyc","version":1,"name":"kyc","trigger":{"type":"manual"},
  "inputs":{"schema":{"$ref":"#/types/Applicant"}},
  "types":{"Applicant":{"type":"object","properties":{
    "bvn":{"type":"string","x-pii":"bvn"},
    "contacts":{"type":"array","items":{"type":"object","properties":{"phone":{"type":"string","x-pii":"phone"}}}},
    "amount":{"type":"integer"}}}},
  "steps":[
    {"id":"copy","type":"transform","config":{"output":{"who":"=trigger.body.bvn","first_phone":"=trigger.body.contacts[0].phone","amount":"=trigger.body.amount"}}},
    {"id":"ok","type":"approval","needs":["copy"],"config":{"role":"officer","subject":{"bvn":"=trigger.body.bvn","amount":"=trigger.body.amount"}}},
    {"id":"check","type":"connector","connector":"fakepay@1","action":"verify","needs":["ok"],"input":{"reference":"=steps.copy.output.who"}}]}`

func TestDeclaredPIIIsSealedEverywhere(t *testing.T) {
	e := rt.New(t)
	wf := e.Publish(t, piiFlow)
	const bvn, phone = "22212345678", "2348012345678"
	ref := e.Start(t, wf, map[string]any{"body": map[string]any{"bvn": bvn, "amount": 150000, "contacts": []any{map[string]any{"phone": phone}}}})
	if err := e.Store.DecideApproval(ctx, ref, "ok", "approved", "checker", "web"); err != nil {
		t.Fatal(err)
	}
	e.Drain(t)

	raw := events(t, e, ref)
	for _, ev := range raw {
		if strings.Contains(string(ev.Payload), bvn) || strings.Contains(string(ev.Payload), phone) {
			t.Errorf("plaintext personal data in stored %s(%s): %s", ev.Type, ev.StepID, ev.Payload)
		}
	}
	var sealedSubject bool
	for _, ev := range raw {
		if ev.Type == history.ApprovalRequested && strings.Contains(string(ev.Payload), `"$pii": "bvn"`) {
			sealedSubject = true
		}
	}
	if !sealedSubject {
		t.Error("the copy of the BVN in the approval subject should be sealed by taint")
	}
	// The verify call itself received the plaintext (fakepay reports not found).
	opened, err := e.Store.OpenedHistory(ctx, ref)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, ev := range opened {
		if ev.Type == history.StepScheduled && ev.StepID == "check" && strings.Contains(string(ev.Payload), bvn) {
			found = true
		}
	}
	if !found {
		t.Error("opened history should show the BVN to callers allowed to see it")
	}
	def, _ := wd.Load([]byte(piiFlow))
	if err := decide.Verify(def, opened); err != nil {
		t.Errorf("replay over opened history: %v", err)
	}

	// Erasure: destroy the BVN subject's key.
	subject, err := e.Vault.SubjectFor(ctx, e.Tenant, "bvn", bvn)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.Vault.Erase(ctx, e.Tenant, subject, "dpo"); err != nil {
		t.Fatal(err)
	}
	after, err := e.Store.OpenedHistory(ctx, ref)
	if err != nil {
		t.Fatal(err)
	}
	for _, ev := range after {
		if strings.Contains(string(ev.Payload), bvn) {
			t.Errorf("erased BVN still readable in %s", ev.Type)
		}
	}
	if !strings.Contains(string(after[0].Payload), pii.Erased) || !strings.Contains(string(after[0].Payload), phone) {
		t.Errorf("after erasure the BVN reads %q and the phone (another subject) stays readable: %s", pii.Erased, after[0].Payload)
	}
}

// Personal data no schema declared is recognised in what a step returns,
// sealed before it is written, and still usable by later steps; log lines
// are masked.
func TestDetectedPIIIsSealed(t *testing.T) {
	e := rt.New(t)
	wf := e.Publish(t, wfDoc(`{"id":"lookup","type":"code","config":{"language":"typescript",
	    "source":"export default () => { console.log('found BVN 22298765432 for 08031234567'); return { customer: { bvn: '22298765432', phone: '+2348031234567', email: 'ada@example.ng' }, amount: 5000 }; }"}},
	  {"id":"use","type":"transform","needs":["lookup"],"config":{"output":{"contact":"=steps.lookup.output.customer.phone","amount":"=steps.lookup.output.amount"}}}`, ""))
	ref := e.Start(t, wf, map[string]any{})
	e.Drain(t)
	if st := e.Status(t, ref); st != "completed" {
		t.Fatalf("status %s: %s", st, types(events(t, e, ref)))
	}
	for _, ev := range events(t, e, ref) {
		for _, leak := range []string{"22298765432", "8031234567", "ada@example.ng"} {
			if strings.Contains(string(ev.Payload), leak) {
				t.Errorf("plaintext %s in stored %s(%s): %s", leak, ev.Type, ev.StepID, ev.Payload)
			}
		}
	}
	opened, err := e.Store.OpenedHistory(ctx, ref)
	if err != nil {
		t.Fatal(err)
	}
	var use string
	for _, ev := range opened {
		if ev.Type == history.StepCompleted && ev.StepID == "use" {
			use = string(ev.Payload)
		}
	}
	if !strings.Contains(use, "+2348031234567") || !strings.Contains(use, `"amount":5000`) {
		t.Errorf("a later step should see the value, and amounts stay plain: %s", use)
	}
	for _, ev := range opened {
		if ev.Type == history.StepCompleted && ev.StepID == "lookup" && !strings.Contains(string(ev.Payload), "found BVN [bvn] for [phone]") {
			t.Errorf("logs should be masked: %s", ev.Payload)
		}
	}
}
