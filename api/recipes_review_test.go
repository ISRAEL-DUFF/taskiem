package api_test

import (
	"encoding/json"
	"strings"
	"sync"
	"testing"

	"github.com/israel-duff/taskiem/api"
	"github.com/israel-duff/taskiem/connectors/notify"
	"github.com/israel-duff/taskiem/engine/ai"
	"github.com/israel-duff/taskiem/engine/ai/builder"
)

// A page is called by its name as a tool is by its command: a recipe
// installed with a page's word gets the next free one, and a tool cannot
// be published with one (review M6, 3).
func TestToolCommandsAvoidPages(t *testing.T) {
	w := newWAWorld(t)
	owner := w.tenant(t, "Acme", "owner@toolpages.test")
	publishWF(t, owner, "Counts", toolDef("wf_counts", "Counts", "counts-now", "", `{}`,
		`[{"name":"open","type":"integer","title":"Open","description":"Open","value":"=steps.look.output.n"}]`,
		`{"type":"text","text":"done"}`, `{"id":"look","type":"transform","config":{"output":{"n":7}}}`))
	doc := map[string]any{"page": "page/v1", "name": "sales-today", "title": "Sales today",
		"blocks": []map[string]any{{"id": "open", "type": "figure", "label": "Open", "value": "output", "source": map[string]any{"tool": "counts-now", "output": "open"}}}}
	owner.must(201, "POST", "/v1/pages", map[string]any{"document": doc})

	got := owner.must(201, "POST", "/v1/templates/tool-daily-report/instantiate", map[string]any{"params": map[string]any{"command": "sales-today"}})
	if tool := got["tool"].(map[string]any); tool["command"] != "sales-today-2" || tool["changed"] != true {
		t.Errorf("a page's word: %v", tool)
	}
	wf := owner.must(201, "POST", "/v1/workflows", map[string]any{"name": "Sales today", "definition": toolDef("wf_sales", "Sales today", "sales-today", "", `{}`,
		`[{"name":"n","type":"integer","title":"N","description":"N","value":"=steps.look.output.n"}]`, `{"type":"text","text":"done"}`,
		`{"id":"look","type":"transform","config":{"output":{"n":7}}}`)})["id"].(string)
	if e := owner.must(409, "POST", "/v1/workflows/"+wf+"/versions/1/publish", nil); !strings.Contains(toJSON(e), "/sales-today is already the page Sales today") {
		t.Errorf("published over a page: %v", e)
	}
}

// Two installs of one recipe at once get two words: the check and the
// draft are held by one lock per business (review M6, 5).
func TestRecipeInstallsAtOnceGetTwoWords(t *testing.T) {
	w := newWorld(t)
	owner := w.tenant(t, "Acme", "owner@toolrace.test")
	words := make([]string, 4)
	var wg sync.WaitGroup
	for i := range words {
		wg.Add(1)
		go func() {
			defer wg.Done()
			st, out := owner.do("POST", "/v1/templates/tool-tell-the-team/instantiate", map[string]any{"params": map[string]any{"command": "tell-all"}})
			if st != 201 {
				t.Errorf("install: %d %v", st, out)
				return
			}
			words[i], _ = out["tool"].(map[string]any)["command"].(string)
		}()
	}
	wg.Wait()
	seen := map[string]bool{}
	for _, wd := range words {
		if seen[wd] {
			t.Errorf("two installs got %s: %v", wd, words)
		}
		seen[wd] = true
	}
}

// An embedded app's end user installing a recipe gets the free word and
// its companion too (review M6, 2).
func TestEmbedInstallsARecipe(t *testing.T) {
	tw := newWATablesWorld(t)
	if err := tw.env.Registry.Register(notify.New(&recordTeller{})); err != nil {
		t.Fatal(err)
	}
	w := tw.world
	p := w.partner(t, "Wagely", "owner@embedrecipe.test")
	sub := p.sub(t, "Customer", nil)
	app, _ := p.app(t, map[string]any{"name": "api", "headless": true, "allowed_connectors": []string{"xenibase", "notify"},
		"allowed_templates":    []string{"tool-daily-report", "tool-look-up"},
		"end_user_permissions": []string{"workflow.read", "workflow.edit"}})
	u := p.endUser(t, w, app, sub, "cust-7", "", "workflow.read", "workflow.edit")
	first := u.call(201, "POST", "/templates/tool-daily-report/instantiate", map[string]any{"params": map[string]any{"command": "today"}})
	tool := first["tool"].(map[string]any)
	if tool["command"] != "today" || tool["companion"] == nil {
		t.Fatalf("first: %v", first)
	}
	again := u.call(201, "POST", "/templates/tool-daily-report/instantiate", map[string]any{"params": map[string]any{"command": "today"}})
	if tool := again["tool"].(map[string]any); tool["command"] != "today-2" || tool["changed"] != true {
		t.Errorf("again: %v", again)
	}
	// An app that does not allow what the companion uses refuses it.
	narrow, _ := p.app(t, map[string]any{"name": "narrow", "headless": true, "allowed_connectors": []string{"xenibase"},
		"allowed_templates": []string{"tool-daily-report"}, "end_user_permissions": []string{"workflow.read", "workflow.edit"}})
	n := p.endUser(t, w, narrow, sub, "cust-8", "", "workflow.read", "workflow.edit")
	if out := n.call(403, "POST", "/templates/tool-daily-report/instantiate", map[string]any{"params": map[string]any{"command": "today"}}); !strings.Contains(toJSON(out["not_allowed"]), "notify") {
		t.Errorf("the companion's connector: %v", out)
	}
}

// Describe it starting from a recipe: the draft is read back with the
// command it would be given and the schedule it brings, and saving it
// gives the free word and saves the companion beside it (review M6, 2).
func TestDescribeItFromARecipe(t *testing.T) {
	w := newWorld(t)
	w.srv.AI = &api.AISettings{Provider: &ai.Fake{Models: "claude-opus-5-5", Respond: func(req ai.Request) (*ai.Response, error) {
		ctxDoc, _ := builder.ContextFrom(req.Messages[0].Text)
		offered := toJSON(ctxDoc["starting_templates"])
		if !strings.Contains(offered, `"tool-daily-report"`) {
			t.Errorf("the recipe was not offered: %.300s", offered)
		}
		env, _ := json.Marshal(map[string]any{"summary": "A daily report.", "assumptions": []string{}, "tests": []any{}, "tables": []any{},
			"workflow": "", "template": "tool-daily-report", "template_params": `{"command":"sales-today","table":"sales"}`})
		return &ai.Response{Text: string(env)}, nil
	}}}
	owner := w.tenant(t, "Acme", "owner@describerecipe.test")
	// The word is taken by another draft.
	owner.must(201, "POST", "/v1/templates/tool-look-up/instantiate", map[string]any{"params": map[string]any{"command": "sales-today"}})

	b := build(t, w, owner, map[string]any{"goal": "a daily report tool for today's sales, with a button to the list every evening"})
	if b["status"] != "proposed" {
		t.Fatalf("build: %v", b)
	}
	back := toJSON(b["read_back"])
	for _, want := range []string{"Use it from the chat with /sales-today-2", "It comes with Daily report, a schedule whose button opens it"} {
		if !strings.Contains(back, want) {
			t.Errorf("read back lacks %q: %s", want, back)
		}
	}
	saved := owner.must(201, "POST", "/v1/ai/builds/"+b["id"].(string)+"/save", map[string]any{})
	tool, _ := saved["tool"].(map[string]any)
	if tool == nil || tool["command"] != "sales-today-2" || tool["changed"] != true || tool["companion"] == nil {
		t.Fatalf("saved: %v", saved)
	}
	v := owner.must(200, "GET", "/v1/workflows/"+saved["id"].(string)+"/versions/1", nil)
	if !strings.Contains(toJSON(v["definition"]), `"command":"sales-today-2"`) {
		t.Errorf("the draft's command: %v", v["definition"])
	}
	comp := tool["companion"].(map[string]any)
	cv := owner.must(200, "GET", "/v1/workflows/"+comp["id"].(string)+"/versions/1", nil)
	if !strings.Contains(toJSON(cv["definition"]), `"tool":"sales-today-2"`) {
		t.Errorf("the companion's button: %v", cv["definition"])
	}
}
