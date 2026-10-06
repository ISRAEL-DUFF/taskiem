package runtime_test

import (
	"context"
	"strings"
	"testing"

	"github.com/israel-duff/taskiem/engine/connector"
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

const kycManifest = `
manifest: connector/v1
id: kyc
version: 1.0.0
name: KYC test
description: A lookup returning personal data.
category: identity
auth: { type: none, fields: [] }
base_url: https://kyc.test
egress_hosts: [kyc.test]
actions:
  lookup:
    title: Look someone up
    class: read
    input: { type: object, properties: { id_number: { type: string } } }
    output:
      type: object
      properties:
        found: { type: boolean }
        record:
          type: object
          properties:
            first_name: { type: string }
            date_of_birth: { type: string }
            addresses: { type: array, items: { type: object, properties: { line: { type: string } } } }
    pii:
      - { field: id_number, category: other }
      - { field: output.record.first_name, category: name }
      - { field: output.record.date_of_birth, category: other }
      - { field: output.record.addresses.*.line, category: address }
`

// What a KYC lookup returns about a person is sealed where the manifest
// says, though no detector recognises names or dates; later steps still
// read it.
func TestDeclaredOutputPIIIsSealed(t *testing.T) {
	e := rt.New(t)
	m := connector.MustParse([]byte(kycManifest))
	if err := e.Registry.Register(&connector.Connector{Manifest: m, Actions: map[string]connector.Action{
		"lookup": connector.ActionFunc(func(context.Context, connector.Request) (connector.Response, error) {
			return connector.Response{Output: map[string]any{"found": true, "record": map[string]any{
				"first_name": "Adaeze", "date_of_birth": "1990-04-17", "addresses": []any{map[string]any{"line": "14 Bode Thomas Street"}}}}}, nil
		})}}); err != nil {
		t.Fatal(err)
	}
	wf := e.Publish(t, `{"schema":"wd/v1","id":"wf_lookup","version":1,"name":"lookup","trigger":{"type":"manual"},
	  "steps":[{"id":"look","type":"connector","connector":"kyc@1","action":"lookup","input":{"id_number":"A123"}},
	    {"id":"greet","type":"transform","needs":["look"],"config":{"output":{"found":"=steps.look.output.found","initial":"=steps.look.output.record.first_name.substring(0, 1)"}}}]}`)
	ref := e.Start(t, wf, map[string]any{})
	e.Drain(t)
	if st := e.Status(t, ref); st != "completed" {
		t.Fatalf("status %s: %s", st, types(events(t, e, ref)))
	}
	for _, ev := range events(t, e, ref) {
		for _, secret := range []string{"Adaeze", "1990-04-17", "Bode Thomas"} {
			if strings.Contains(string(ev.Payload), secret) {
				t.Errorf("plaintext %q in stored %s(%s): %s", secret, ev.Type, ev.StepID, ev.Payload)
			}
		}
		if ev.Type == history.StepCompleted && ev.StepID == "look" && (!strings.Contains(string(ev.Payload), `"found": true`) && !strings.Contains(string(ev.Payload), `"found":true`)) {
			t.Errorf("an undeclared field was sealed: %s", ev.Payload)
		}
	}
	opened, err := e.Store.OpenedHistory(ctx, ref)
	if err != nil {
		t.Fatal(err)
	}
	ok := false
	for _, ev := range opened {
		if ev.Type == history.StepCompleted && ev.StepID == "look" && strings.Contains(string(ev.Payload), "Adaeze") && strings.Contains(string(ev.Payload), "Bode Thomas") {
			ok = true
		}
	}
	if !ok {
		t.Error("revealed history lacks the lookup's result")
	}
}
