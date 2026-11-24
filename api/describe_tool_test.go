package api_test

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/israel-duff/taskiem/api"
	"github.com/israel-duff/taskiem/engine/ai"
	"github.com/israel-duff/taskiem/engine/ai/builder"
	"github.com/israel-duff/taskiem/engine/whatsapp/whatsapptest"
)

// describeToolModel is the stand-in model for Describe it (build plan M6):
// it reads the goal and the context the builder gave it, finds the saved
// thing the goal names by @name among the names and kinds it was told,
// and drafts a tool that reads it, with its inputs, outputs and tool block
// together. A goal for customers is first drafted with an answer that
// shows an output not marked public, which the publishing checks refuse;
// the correction marks it public.
func describeToolModel(t *testing.T) *ai.Fake {
	return &ai.Fake{Models: "claude-opus-5-5", Respond: func(req ai.Request) (*ai.Response, error) {
		prompt := req.Messages[0].Text
		goal := builder.GoalFrom(prompt)
		ctxDoc, _ := builder.ContextFrom(prompt)
		name := ""
		things, _ := ctxDoc["saved_things"].([]any)
		for _, raw := range things {
			s := raw.(map[string]any)
			if _, has := s["title"]; has || len(s) > 2 {
				t.Errorf("the model was told more of a saved thing than its name and kind: %v", s)
			}
			if n, _ := s["name"].(string); strings.Contains(goal, "@"+n) {
				name = n
			}
		}
		if name == "" {
			t.Errorf("no saved thing named in %q among %v", goal, things)
		}
		key := strings.ReplaceAll(name, "-", "_")
		audience, command, public := "members", "prices-now", ""
		if strings.Contains(goal, "customer") {
			audience, command = "customers", "our-prices"
			if len(req.Messages) > 1 {
				public = `,"public":true`
			}
		}
		def := fmt.Sprintf(`{"schema":"wd/v1","id":"wf_prices","version":1,"name":"Our prices","trigger":{"type":"manual"},
		 "inputs":{"schema":{"type":"object","properties":{}}},
		 "outputs":[{"name":"prices","type":"string","title":"Prices","description":"The price list","value":"=steps.read.output.prices"%s}],
		 "tool":{"name":"Our prices","command":"%s","about":"Shows the price list","icon":"tag","for":"%s","answer":{"type":"text","text":"=outputs.prices"}},
		 "steps":[{"id":"read","type":"transform","name":"Read the price list","config":{"output":{"prices":"=saved.%s.body"}}}]}`, public, command, audience, key)
		env, _ := json.Marshal(map[string]any{"summary": "A tool, " + command + ", that answers with the price list.", "assumptions": []string{}, "tests": []any{},
			"template": "", "template_params": "", "workflow": def, "tables": []any{}})
		return &ai.Response{Text: string(env)}, nil
	}}
}

// Done when (build plan M6): Describe it produces a working tool from one
// sentence naming a saved thing. On the web: the draft is read back in
// plain steps with its tool, saved as a draft, published, and the tool
// answers with what the saved thing holds. In the chat: the same sentence
// is read back with its command and saved as a draft on yes. For
// customers: the answer shows public outputs only.
func TestDescribeItMakesAWorkingTool(t *testing.T) {
	w := newWAWorld(t)
	w.srv.AI = &api.AISettings{Provider: describeToolModel(t)}
	owner := w.tenant(t, "Ada Stores", "owner@describe.test")
	owner.must(201, "POST", "/v1/saved", map[string]any{"name": "price-list", "title": "This week's prices", "kind": "text",
		"details": map[string]any{"body": "Rice 45,000; Beans 12,000"}})

	// The web: one sentence.
	b := build(t, w.world, owner, map[string]any{"goal": "When I type prices-now, show me @price-list"})
	prop := b["proposal"].(map[string]any)
	if b["status"] != "proposed" || prop["valid_first_try"] != true || len(prop["problems"].([]any)) != 0 {
		t.Fatalf("build: %v", b)
	}
	back := toJSON(b["read_back"])
	for _, want := range []string{"Read the price list", "Use it from the chat with /prices-now: Our prices", "Your own people can use it", "It answers with words"} {
		if !strings.Contains(back, want) {
			t.Errorf("read back lacks %q: %s", want, back)
		}
	}
	saved := owner.must(201, "POST", "/v1/ai/builds/"+b["id"].(string)+"/save", map[string]any{})
	if saved["state"] != "draft" {
		t.Fatalf("save: %v", saved)
	}
	wf := saved["id"].(string)
	if got := owner.must(200, "GET", "/v1/tools", nil)["tools"].([]any); len(got) != 0 {
		t.Errorf("a draft is never a tool: %v", got)
	}
	owner.must(200, "POST", "/v1/workflows/"+wf+"/versions/1/publish", nil)
	got := owner.must(201, "POST", "/v1/tools/prices-now/start", map[string]any{"input": map[string]any{}})
	if got["care"] != "answers" {
		t.Errorf("it only reads, so it answers at once: %v", got)
	}
	w.env.Drain(t)
	if a := owner.must(200, "GET", "/v1/runs/"+got["run_id"].(string)+"/answer", nil); a["text"] != "Rice 45,000; Beans 12,000" {
		t.Errorf("answer: %v", a)
	}

	// The chat: the same kind of sentence, read back with its tool, saved
	// as a draft on yes.
	const on = "+2348010000071"
	w.bind(t, owner, on)
	before := len(w.graph.Sent(on))
	if r := w.say(t, on, "build when a customer asks, answer with our @price-list"); !strings.Contains(r, "Working on it") {
		t.Fatalf("start: %q", r)
	}
	msgs := w.lastTo(t, on, before+1)
	if len(msgs) != 1 {
		t.Fatalf("draft: %+v", msgs)
	}
	for _, want := range []string{"Use it from the chat with /our-prices: Our prices", "Your customers can use it, and see only what it marks for them",
		// The builder's warning in its own words (review M6, 1): the public
		// output is copied from a saved thing.
		"Every customer who uses it would see Prices, which comes from something you keep to yourself"} {
		if !strings.Contains(msgs[0].Text, want) {
			t.Errorf("chat read back lacks %q:\n%s", want, msgs[0].Text)
		}
	}
	if r := w.send(t, on, whatsapptest.ButtonReply("yes", "Yes, save draft")); len(r) == 0 || !strings.Contains(r[0].Text, "as a draft") {
		t.Fatalf("saved: %+v", r)
	}
	// Corrected to show public outputs only, as a customers' tool must.
	list := toJSON(owner.must(200, "GET", "/v1/workflows", nil))
	if !strings.Contains(list, "Our prices") {
		t.Fatalf("workflows: %s", list)
	}
	var cust map[string]any
	for _, raw := range owner.must(200, "GET", "/v1/workflows", nil)["workflows"].([]any) {
		m := raw.(map[string]any)
		if m["id"] != wf && m["name"] == "Our prices" {
			cust = m
		}
	}
	if cust == nil {
		t.Fatalf("the chat's draft: %s", list)
	}
	v := owner.must(200, "GET", "/v1/workflows/"+cust["id"].(string)+"/versions/1", nil)
	if def := toJSON(v["definition"]); !strings.Contains(def, `"public":true`) || !strings.Contains(def, `"for":"customers"`) {
		t.Errorf("customers' tool: %s", def)
	}
}
