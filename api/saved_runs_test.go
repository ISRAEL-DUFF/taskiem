package api_test

import (
	"encoding/json"
	"strings"
	"testing"

	sfconn "github.com/israel-duff/taskiem/connectors/storefront"
)

const savedFlow = `{"schema":"wd/v1","id":"wf_saved","version":1,"name":"prices","trigger":{"type":"manual"},
  "steps":[{"id":"send","type":"transform","config":{"output":{
    "prices":"=saved.price_list.body",
    "pay":"=saved[\"supplier\"].bank_name + ' ' + saved[\"supplier\"].account_name",
    "kind":"=saved.supplier.kind"}}}]}`

// A business's saved things as a workflow's inputs (saved objects S3,
// §6.4): read when the run starts, so a change is used from the next run;
// an account number sealed in the run's history, shown only when revealed;
// a person's own saved thing of the name is never read.
func TestSavedReadByRuns(t *testing.T) {
	w := newWAWorld(t)
	owner := w.tenant(t, "Ada Foods", "ada@savedruns.test")
	prices := owner.must(201, "POST", "/v1/saved", map[string]any{"name": "price-list", "title": "Price list", "kind": "text",
		"details": map[string]any{"body": "Rice 50kg: 78,000"}})["saved"].(map[string]any)
	owner.must(201, "POST", "/v1/saved", map[string]any{"name": "supplier", "title": "Dangote", "kind": "bank_account",
		"bank_name": "GTBank", "account_number": "0123456789"})
	owner.must(201, "POST", "/v1/me/saved", map[string]any{"name": "secret", "title": "Mine", "kind": "text", "details": map[string]any{"body": "my own"}})

	out := owner.must(201, "POST", "/v1/workflows", map[string]any{"name": "prices", "definition": json.RawMessage(savedFlow)})
	if probs := out["problems"].([]any); len(probs) > 0 {
		t.Fatalf("problems: %v", probs)
	}
	wf := out["id"].(string)
	// Ready: what it reads is saved; a name nothing has is a need.
	if r := owner.must(200, "POST", "/v1/readiness", map[string]any{"definition": json.RawMessage(savedFlow)}); strings.Contains(toJSON(r), `"saved"`) {
		t.Fatalf("ready: %s", toJSON(r))
	}
	missing := strings.Replace(savedFlow, "saved.price_list.body", "saved.price_lists.body", 1)
	if r := toJSON(owner.must(200, "POST", "/v1/readiness", map[string]any{"definition": json.RawMessage(missing)})); !strings.Contains(r, `"kind":"saved"`) || !strings.Contains(r, "Save @price-lists on the Saved page") {
		t.Fatalf("a missing one: %s", r)
	}
	// One's own of the name is not the business's.
	own := strings.Replace(savedFlow, "saved.price_list.body", "saved.secret.body", 1)
	if r := toJSON(owner.must(200, "POST", "/v1/readiness", map[string]any{"definition": json.RawMessage(own)})); !strings.Contains(r, "Save @secret") {
		t.Fatalf("one's own: %s", r)
	}
	owner.must(200, "POST", "/v1/workflows/"+wf+"/versions/1/publish", nil)

	run := func() (string, map[string]any) {
		t.Helper()
		id := owner.must(201, "POST", "/v1/workflows/"+wf+"/runs", map[string]any{"input": map[string]any{}})["run_id"].(string)
		w.env.Drain(t)
		got := owner.must(200, "GET", "/v1/runs/"+id, nil)
		if st := got["run"].(map[string]any)["status"]; st != "completed" {
			t.Fatalf("run: %v\n%s", st, toJSON(got["events"]))
		}
		return id, got
	}
	id, got := run()
	if ev := toJSON(got["events"]); !strings.Contains(ev, "Rice 50kg: 78,000") || strings.Contains(ev, "0123456789") || !strings.Contains(ev, "bank_account") {
		t.Fatalf("the first run: %s", ev)
	}
	// An account number is not a run's (paying a saved account comes with
	// payments): not even revealed.
	if open := toJSON(owner.must(200, "GET", "/v1/runs/"+id+"?reveal=true", nil)); strings.Contains(open, "0123456789") || !strings.Contains(open, "GTBank") {
		t.Fatalf("revealed: %s", open)
	}
	// Changed: the next run reads it as it is now.
	owner.must(200, "PUT", "/v1/saved/"+prices["id"].(string), map[string]any{"details": map[string]any{"body": "Rice 50kg: 80,500"}})
	if _, got := run(); !strings.Contains(toJSON(got["events"]), "Rice 50kg: 80,500") {
		t.Fatalf("the next run: %s", toJSON(got["events"]))
	}
	// Removed since it was published: the start is refused, in words.
	owner.must(204, "DELETE", "/v1/saved/"+prices["id"].(string), nil)
	st, body := owner.do("POST", "/v1/workflows/"+wf+"/runs", map[string]any{"input": map[string]any{}})
	if st < 400 || !strings.Contains(toJSON(body), "@price-list, which the business has not saved") {
		t.Fatalf("removed: %d %v", st, body)
	}
}

// S3's done-when: "every Monday, send @price-list to the customers who
// ordered last week": storefront@1 recent_customers lists them, each once,
// and each is sent the saved text as it is when the run starts.
func TestSavedPriceListToRecentCustomers(t *testing.T) {
	w, ada := newDispatchWorld(t, "Ada Stores")
	if err := w.env.Registry.Register(sfconn.New(w.orders)); err != nil {
		t.Fatal(err)
	}
	w.order(t, "none")
	w.order(t, "none")
	ada.must(201, "POST", "/v1/saved", map[string]any{"name": "price-list", "title": "Price list", "kind": "text",
		"details": map[string]any{"body": "Jollof rice: 2,500 a plate"}})
	flow := `{"schema":"wd/v1","id":"wf_prices","version":1,"name":"weekly prices","trigger":{"type":"manual"},
	  "steps":[{"id":"who","type":"connector","connector":"storefront@1","action":"recent_customers","input":{"days":7}},
	    {"id":"each","type":"foreach","needs":["who"],"config":{"items":"=steps.who.output.customers","steps":[
	      {"id":"send","type":"transform","config":{"output":{"to":"=item.phone","text":"=saved.price_list.body"}}}]}}]}`
	out := ada.must(201, "POST", "/v1/workflows", map[string]any{"name": "weekly prices", "definition": json.RawMessage(flow)})
	if probs := out["problems"].([]any); len(probs) > 0 {
		t.Fatalf("problems: %v", probs)
	}
	wf := out["id"].(string)
	// Listing customers' phones is published only by those who may handle
	// orders.
	ada.must(201, "PUT", "/v1/roles/builder_only", map[string]any{"permissions": []string{"workflow.read", "workflow.edit", "workflow.publish"}})
	ada.must(201, "POST", "/v1/members", map[string]any{"email": "builder@prices.test", "password": testPassword, "roles": []string{"builder_only"}})
	builder := w.login(t, "builder@prices.test", testPassword)
	if st, body := builder.do("POST", "/v1/workflows/"+wf+"/versions/1/publish", nil); st != 403 || !strings.Contains(toJSON(body), "may handle orders") {
		t.Fatalf("a builder publishing: %d %v", st, body)
	}
	ada.must(200, "POST", "/v1/workflows/"+wf+"/versions/1/publish", nil)
	run := ada.must(201, "POST", "/v1/workflows/"+wf+"/runs", map[string]any{"input": map[string]any{}})["run_id"].(string)
	w.env.Drain(t)
	got := ada.must(200, "GET", "/v1/runs/"+run, nil)
	ev := toJSON(got["events"])
	if st := got["run"].(map[string]any)["status"]; st != "completed" || strings.Count(ev, "Jollof rice: 2,500 a plate") < 2 {
		t.Fatalf("run %v: %s", st, ev)
	}
	if strings.Contains(ev, "+234803") {
		t.Errorf("a customer's phone in the run's history: %s", ev)
	}
}
