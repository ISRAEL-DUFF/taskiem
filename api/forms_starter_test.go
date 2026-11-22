package api_test

import (
	"context"
	"strings"
	"testing"

	"github.com/israel-duff/taskiem/engine/whatsapp/whatsapptest"
)

// A question inside a run (made tools M4): a tool started on WhatsApp asks
// its starter "Which branch?" there, a saved place among the choices as a
// button; pressed, the run goes on with it and answers.
func TestRunAsksItsStarter(t *testing.T) {
	w := deviceWorld(t)
	ctx := context.Background()
	owner := w.tenant(t, "Acme", "owner@acme.test")
	owner.must(201, "POST", "/v1/saved", map[string]any{"name": "ikeja", "title": "Ikeja branch", "kind": "place",
		"details": map[string]any{"line": "12 Allen Avenue", "area": "Ikeja"}})
	staff := addMember(t, w.world, owner, "staff", "operator")
	const number = "+2348031117777"
	w.bindWA(t, staff, number)
	publishFlow(t, owner, `{"schema":"wd/v1","id":"wf_count","version":1,"name":"Count stock","trigger":{"type":"manual"},
		"inputs":{"schema":{"type":"object","properties":{}}},
		"outputs":[{"name":"branch","type":"string","title":"Branch","description":"Where","value":"=steps.where.output.branch"}],
		"tool":{"name":"Count stock","command":"count-stock","about":"Count a branch's stock","answer":{"type":"text","text":"='Counting at ' + outputs.branch"}},
		"steps":[{"id":"where","type":"form","config":{"title":"Which branch?","to":"starter",
		  "schema":{"type":"object","required":["branch"],"properties":{"branch":{"type":"string","title":"Branch","x-saved":"place"}}}}}]}`)

	// Started on WhatsApp: it asks a person, so yes first.
	if got := lastText(w.waSend(t, number, whatsapptest.Text("count-stock"))); !strings.Contains(got, "Count stock") {
		t.Fatalf("summary: %q", got)
	}
	w.waSend(t, number, whatsapptest.ButtonReply("yes", "Yes, run it"))
	w.env.Drain(t)
	if err := w.forms.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	ask := w.graph.Last(t, number)
	if !strings.Contains(ask.Text, "Which branch?") || len(ask.Buttons) != 1 || ask.Buttons[0].Title != "Ikeja branch" {
		t.Fatalf("asked: %+v", ask)
	}
	if got := lastText(w.waSend(t, number, whatsapptest.ButtonReply(ask.Buttons[0].ID, "Ikeja branch"))); !strings.Contains(got, "Which branch?: Ikeja branch") {
		t.Fatalf("answered: %q", got)
	}
	w.env.Drain(t)
	w.tick(t)
	if got := w.graph.Last(t, number).Text; !strings.Contains(got, "Counting at Ikeja branch") {
		t.Errorf("answer: %q", got)
	}
	// Answered: the same button again says so.
	if got := lastText(w.waSend(t, number, whatsapptest.ButtonReply(ask.Buttons[0].ID, "Ikeja branch"))); !strings.Contains(got, "not waiting") {
		t.Errorf("again: %q", got)
	}
}
