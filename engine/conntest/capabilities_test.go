package conntest_test

import (
	"context"
	"strings"
	"testing"

	"github.com/israel-duff/taskiem/engine/connector"
	"github.com/israel-duff/taskiem/engine/conntest"
)

// A rail whose payout events carry the payout id somewhere other than
// where its trigger correlates fails the suite: a wait would never end.
const rail = `
manifest: connector/v1
id: rail
version: 1.0.0
name: Rail
category: payments
kind: rail
auth: { type: none }
base_url: https://api.rail.example
actions:
  lookup:
    title: Look up
    class: read
    input: { type: object, required: [id], properties: { id: { type: string } } }
    output: { type: object, properties: { state: { type: string } } }
triggers:
  hook:
    type: webhook
    verify: { scheme: none }
    events: [paid]
    event_type: =body.event
    correlation: =body.ref
capabilities:
  get_payout:
    action: lookup
    input: { id: "=input.payout_id" }
    output: { status: "=output.state == 'done' ? 'settled' : 'sideways'" }
events:
  payout.settled: { trigger: hook, when: "=body.event == 'paid'", fields: { payout_id: "=body.id" } }
`

func TestCapabilityConformance(t *testing.T) {
	m, probs := connector.Parse([]byte(rail))
	if len(probs) > 0 {
		t.Fatal(probs)
	}
	lookup := connector.ActionFunc(func(_ context.Context, req connector.Request) (connector.Response, error) {
		resp, err := req.HTTP.Get(m.BaseURL + "/lookup/" + req.Input["id"].(string))
		if err != nil {
			return connector.Response{}, err
		}
		_ = resp.Body.Close()
		state := "done"
		if req.Input["id"] == "p2" {
			state = "odd"
		}
		return connector.Response{Output: map[string]any{"state": state}}, nil
	})
	c := &connector.Connector{Manifest: m, Actions: map[string]connector.Action{"lookup": lookup}}
	ex := func(id string) conntest.ExchangeRef {
		return conntest.ExchangeRef{Inline: &conntest.Exchange{Request: conntest.Request{Method: "GET", Path: "/lookup/" + id}, Response: conntest.Response{Status: 200, Body: map[string]any{}}}}
	}
	s := &conntest.CapabilitySuite{Cases: []conntest.CapabilityCase{
		{Name: "settled", Capability: "get_payout", Input: map[string]any{"payout_id": "p1"}, Exchanges: []conntest.ExchangeRef{ex("p1")}, Expect: map[string]any{"status": "settled"}},
		{Name: "not a status", Capability: "get_payout", Input: map[string]any{"payout_id": "p2"}, Exchanges: []conntest.ExchangeRef{ex("p2")}, Expect: map[string]any{}},
		{Name: "no input", Capability: "get_payout", Input: map[string]any{}, Error: "fatal"},
		{Name: "event", Trigger: "hook", Body: map[string]any{"event": "paid", "id": "p1", "ref": "other"}, Event: "payout.settled", Expect: map[string]any{"payout_id": "p1"}},
	}}
	rep := conntest.RunCapabilities(context.Background(), c, s, conntest.Options{})
	got := map[string]string{}
	for _, r := range rep.Results {
		got[r.Case] = strings.Join(r.Problems, "; ")
	}
	if got["settled"] != "" || got["no input"] != "" {
		t.Errorf("passing cases failed: %v", got)
	}
	if !strings.Contains(got["not a status"], "is not a get_payout output") {
		t.Errorf("an unknown status: %q", got["not a status"])
	}
	if !strings.Contains(got["event"], "the trigger correlates the delivery on other") {
		t.Errorf("a correlation mismatch: %q", got["event"])
	}
	if rep.Passed() || strings.Join(rep.Uncovered, ",") != "event payout.settled" {
		t.Errorf("uncovered %v", rep.Uncovered)
	}
}

// An event no capability of the rail leads to (a POS sale) is waited on
// by nothing: its trigger may correlate on something else, such as the
// payment request a sale answers.
func TestEventNoCapabilityWaitsOn(t *testing.T) {
	m, probs := connector.Parse([]byte(`
manifest: connector/v1
id: till
version: 1.0.0
name: Till
category: payments
kind: rail
auth: { type: none }
base_url: https://api.till.example
actions:
  ping:
    title: Ping
    class: read
    input: { type: object, properties: {} }
    output: { type: object, properties: {} }
triggers:
  sale:
    type: webhook
    verify: { scheme: none }
    events: [sale]
    event_type: '="sale"'
    correlation: =body.request
events:
  pos.sale: { trigger: sale, when: "=true", fields: { reference: "=body.id", amount_kobo: "=body.kobo", status: "='approved'" } }
`))
	if len(probs) > 0 {
		t.Fatal(probs)
	}
	c := &connector.Connector{Manifest: m, Actions: map[string]connector.Action{}}
	s := &conntest.CapabilitySuite{Cases: []conntest.CapabilityCase{
		{Name: "sale", Trigger: "sale", Body: map[string]any{"id": "t1", "kobo": 500, "request": "r1"}, Event: "pos.sale", Expect: map[string]any{"reference": "t1", "amount_kobo": 500}},
	}}
	rep := conntest.RunCapabilities(context.Background(), c, s, conntest.Options{})
	if !rep.Passed() {
		t.Errorf("%+v", rep.Results)
	}
}
