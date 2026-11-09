package runtime_test

import (
	"strings"
	"testing"

	"github.com/israel-duff/taskiem/engine/pii"
	"github.com/israel-duff/taskiem/engine/runtime"
	rt "github.com/israel-duff/taskiem/engine/runtime/runtimetest"
)

const echoChild = `{"schema":"wd/v1","id":"wf_echo","version":1,"name":"Echo","trigger":{"type":"subflow"},
  "inputs":{"schema":{"type":"object","properties":{"note":{"type":"string"},"greet":{"type":"string"},"plain":{"type":"string"}}}},
  "steps":[{"id":"t","type":"transform","config":{"output":{"note":"=trigger.body.note","greet":"=trigger.body.greet","plain":"=trigger.body.plain"}}}],
  "outputs":[{"name":"note","type":"string","title":"Note","description":"The note","value":"=steps.t.output.note"},
             {"name":"plain","type":"string","title":"Plain","description":"Plain","value":"=steps.t.output.plain"}]}`

func rawEvents(t *testing.T, e *rt.Env, ref runtime.RunRef) string {
	t.Helper()
	var b strings.Builder
	for _, ev := range events(t, e, ref) {
		b.Write(ev.Payload)
	}
	return b.String()
}

func opened(t *testing.T, e *rt.Env, ref runtime.RunRef) string {
	t.Helper()
	h, err := e.Store.OpenedHistory(ctx, ref)
	if err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	for _, ev := range h {
		b.Write(ev.Payload)
	}
	return b.String()
}

// A child run of a customer's run keeps what it was given sealed under
// the customer, the parent's personal values where they were passed on
// and every text besides, so erasing the customer erases the child's copy
// too (decision 0062 P2).
func TestCustomerSubflowSealed(t *testing.T) {
	e := rt.New(t)
	child := e.Publish(t, echoChild)
	parent := e.Publish(t, wfDoc(`{"id":"sub","type":"subflow","config":{"workflow":"`+child.String()+`"},
	    "input":{"note":"=trigger.text","greet":"=\"Re: \" + trigger.text","plain":"=\"see you at the shop\""}},
	  {"id":"after","type":"transform","needs":["sub"],"config":{"output":"=steps.sub.output.outputs.note"}}`, ""))
	const ada, words = "2348031234567", "Meet me at 12 Admiralty Way"
	ref, _, err := e.Store.StartRun(ctx, runtime.StartRequest{
		TenantID: e.Tenant, WorkflowID: parent, Version: 1, Environment: "prod",
		Trigger:    map[string]any{"text": words, "from": ada},
		TriggerPII: []pii.Path{{Segments: []string{"text"}, Category: "other"}, {Segments: []string{"from"}, Category: "phone"}},
		Customer:   &runtime.Customer{Category: "phone", Value: ada},
	})
	if err != nil {
		t.Fatal(err)
	}
	e.Drain(t)
	if st := e.Status(t, ref); st != "completed" {
		t.Fatalf("parent: %s %s", st, types(events(t, e, ref)))
	}
	links, err := e.Store.Forks(ctx, ref)
	if err != nil || len(links.Subflows) != 1 {
		t.Fatalf("children: %+v %v", links, err)
	}
	childRef := runtime.RunRef{ID: links.Subflows[0], TenantID: e.Tenant}
	// The parent's literal text is the workflow's, not the customer's; the
	// child seals every text it is given.
	if h := rawEvents(t, e, ref); strings.Contains(h, "Admiralty") {
		t.Fatalf("the parent in the clear: %s", h)
	}
	if h := rawEvents(t, e, childRef); strings.Contains(h, "Admiralty") || strings.Contains(h, "see you at the shop") {
		t.Fatalf("the child in the clear: %s", h)
	}
	if h := opened(t, e, childRef); !strings.Contains(h, "Re: "+words) || !strings.Contains(h, "see you at the shop") {
		t.Fatalf("the child cannot read what it was given: %s", h)
	}
	subject, err := e.Vault.SubjectFor(ctx, e.Tenant, "phone", ada)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.Vault.Erase(ctx, e.Tenant, subject, "test"); err != nil {
		t.Fatal(err)
	}
	for _, r := range []runtime.RunRef{ref, childRef} {
		if h := opened(t, e, r); strings.Contains(h, "Admiralty") || strings.Contains(h, ada) {
			t.Fatalf("outlived the customer's erasure: %s", h)
		}
	}
	if h := opened(t, e, childRef); strings.Contains(h, "see you at the shop") {
		t.Fatalf("the child's input outlived the customer's erasure: %s", h)
	}
}

// A customer's run still open when they are erased carries on: a signal
// it waits for is delivered and the run ends, keeping nothing of it.
func TestCustomerRunAfterErasure(t *testing.T) {
	e := rt.New(t)
	wf := e.Publish(t, wfDoc(`{"id":"wait","type":"signal","config":{"event":"chat@1:messages","correlation":"=trigger.from","timeout":"1h"}},
	  {"id":"after","type":"transform","needs":["wait"],"config":{"output":"=steps.wait.output.text"}}`, ""))
	const ada = "2348031234567"
	ref, _, err := e.Store.StartRun(ctx, runtime.StartRequest{
		TenantID: e.Tenant, WorkflowID: wf, Version: 1, Environment: "prod",
		Trigger: map[string]any{"from": ada}, TriggerPII: []pii.Path{{Segments: []string{"from"}, Category: "phone"}},
		Customer: &runtime.Customer{Category: "phone", Value: ada},
	})
	if err != nil {
		t.Fatal(err)
	}
	e.Drain(t)
	subject, _ := e.Vault.SubjectFor(ctx, e.Tenant, "phone", ada)
	if err := e.Vault.Erase(ctx, e.Tenant, subject, "test"); err != nil {
		t.Fatal(err)
	}
	woke, _, err := e.Store.DeliverSignalPII(ctx, e.Tenant, "prod", "chat@1:messages", ada, "m-2",
		map[string]any{"from": ada, "text": "my new address is 3 Bode Thomas"}, []pii.Path{{Segments: []string{"from"}, Category: "phone"}, {Segments: []string{"text"}, Category: "other"}})
	if err != nil || len(woke) != 1 {
		t.Fatalf("delivery to an erased customer's run: %v %v", woke, err)
	}
	e.Drain(t)
	if st := e.Status(t, ref); st != "completed" {
		t.Fatalf("run: %s %s", st, types(events(t, e, ref)))
	}
	if h := rawEvents(t, e, ref) + opened(t, e, ref); strings.Contains(h, "Bode Thomas") || strings.Contains(h, ada) {
		t.Fatalf("kept: %s", h)
	}
}
