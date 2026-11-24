package api_test

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/israel-duff/taskiem/api"
	"github.com/israel-duff/taskiem/engine/ai"
	"github.com/israel-duff/taskiem/engine/ai/builder"
)

// describePageModel is the stand-in model for Describe it for pages, as
// describeToolModel is for tools: it reads the goal and the names it was
// told, and drafts a page of open tickets with how many wait and a button
// to add one, with the table and the tool it needs. Its first draft names
// its tool with a word of Taskiem's own, which the checks refuse; the
// correction takes another. Every prompt it is sent is kept.
type describePageModel struct {
	mu      sync.Mutex
	prompts []string
	t       *testing.T
}

func (m *describePageModel) fake() *ai.Fake {
	return &ai.Fake{Models: "claude-opus-5-5", Respond: func(req ai.Request) (*ai.Response, error) {
		m.mu.Lock()
		for _, msg := range req.Messages {
			m.prompts = append(m.prompts, msg.Text)
		}
		for _, sys := range req.System {
			m.prompts = append(m.prompts, sys.Text)
		}
		m.mu.Unlock()
		prompt := req.Messages[0].Text
		goal := builder.GoalFrom(prompt)
		ctxDoc, ok := builder.ContextFrom(prompt)
		if !ok || !strings.Contains(goal, "open tickets") {
			m.t.Errorf("the goal and the names: %q %v", goal, ctxDoc)
		}
		// Names and types only: a table is its name, title and columns.
		for _, raw := range ctxDoc["tables"].([]any) {
			tb := raw.(map[string]any)
			if len(tb) != 3 {
				m.t.Errorf("the model was told more of a table than its names and types: %v", tb)
			}
			for _, c := range tb["columns"].([]any) {
				if len(c.(map[string]any)) != 2 {
					m.t.Errorf("a column: %v", c)
				}
			}
		}
		command := "invoice" // one of Taskiem's own words: refused, then corrected
		if len(req.Messages) > 1 {
			command = "new-ticket"
		}
		page := `{"page":"page/v1","name":"open-tickets","title":"Open tickets","icon":"check","about":"What is open, and what waits",
		 "blocks":[
		  {"id":"open","type":"figure","label":"Open","value":"count","tone":"warn","lead":true,"source":{"table":"tickets","filters":[{"column":"status","op":"eq","value":"open"}]}},
		  {"id":"waiting","type":"figure","label":"Waiting","value":"count","source":{"table":"tickets","filters":[{"column":"status","op":"eq","value":"waiting"}]}},
		  {"id":"add","type":"buttons","actions":[{"label":"New ticket","tool":"` + command + `"}]},
		  {"id":"list","type":"list","title":"Open tickets","source":{"table":"tickets","filters":[{"column":"status","op":"eq","value":"open"}],"order":[{"column":"created_at","desc":true}]},
		   "item":{"title":"=row.title","sub":"=row.customer"},"empty":"Nothing is open"}]}`
		tools := `[{"command":"` + command + `","name":"New ticket","about":"Adds a ticket, open","kind":"add","table":"tickets","asks":["title","customer"],"sets":{"status":"open"},"icon":"list"}]`
		env, _ := json.Marshal(map[string]any{"summary": "Open tickets: how many are open and waiting, the open ones as a list, and New ticket to add one.",
			"page": page, "tools": tools,
			"tables": []any{map[string]any{"name": "tickets", "title": "Tickets", "columns": []any{
				map[string]any{"name": "title", "title": "Title", "type": "text", "required": true},
				map[string]any{"name": "customer", "title": "Customer", "type": "text", "required": false},
				map[string]any{"name": "status", "title": "Status", "type": "text", "required": false}}}}})
		return &ai.Response{Text: string(env)}, nil
	}}
}

// G5, done when: Describe it makes a working page from one sentence. The
// sentence is drafted (corrected once, as the checks refuse a word of
// Taskiem's own) into a page and what it would make, named before anything
// is made; the model is told the business's tables, tools and kinds of
// saved things by name and type only (no row, no saved thing, no value);
// made and published, the page draws, its New ticket opens the tool it
// made, and the ticket shows on the page.
func TestDescribeItMakesAWorkingPage(t *testing.T) {
	defer api.SetPageToolWait(0)()
	w := newTablesWorld(t)
	model := &describePageModel{t: t}
	w.srv.AI = &api.AISettings{Provider: model.fake()}
	owner := w.tenant(t, "Ada Stores", "owner@describe-page.test")

	// What the business has, whose values must never reach the model.
	jobs := owner.must(201, "POST", "/v1/tables", map[string]any{"name": "jobs", "title": "Jobs", "columns": []map[string]any{
		{"name": "what", "title": "What", "type": "text"}, {"name": "phone", "title": "Phone", "type": "phone"}}})["id"].(string)
	owner.must(201, "POST", "/v1/tables/"+jobs+"/rows", map[string]any{"rows": []map[string]any{{"what": "Fix the Zenith gate", "phone": "+2348099911122"}}})
	kind := owner.must(201, "POST", "/v1/saved-kinds", map[string]any{"name": "Generator", "fields": []map[string]any{
		{"label": "Size", "type": "text"}, {"label": "Serial", "type": "text", "sensitive": true}}})["kind"].(map[string]any)
	owner.must(201, "POST", "/v1/saved", map[string]any{"name": "big-gen", "title": "Mikano yard gen", "kind": "custom", "kind_id": kind["id"],
		"values": map[string]any{"size": "60kVA", "serial": "SN-77441199"}})

	st, out := owner.do("POST", "/v1/page-builds", map[string]any{"goal": ""})
	if st != 400 {
		t.Errorf("no goal: %d %v", st, out)
	}
	started := owner.must(202, "POST", "/v1/page-builds", map[string]any{"goal": "A page of open tickets with how many are waiting, and a button to add one"})
	w.srv.WaitBackground()
	b := owner.must(200, "GET", "/v1/page-builds/"+started["id"].(string), nil)
	prop, _ := b["proposal"].(map[string]any)
	if b["status"] != "proposed" || prop == nil || len(prop["problems"].([]any)) != 0 || prop["rounds"] != float64(2) || prop["valid_first_try"] != false {
		t.Fatalf("build: %v", b)
	}
	// It names what it would make, and nothing is made yet.
	review := toJSON(b["review"])
	for _, want := range []string{"The table Tickets: Title, Customer, Status", "/new-ticket (New ticket): adds a row to Tickets, asking for Title and Customer", `"title":"Open tickets"`, "Tickets: "} {
		if !strings.Contains(review, want) {
			t.Errorf("review lacks %q: %s", want, review)
		}
	}
	if n := len(owner.must(200, "GET", "/v1/tables", nil)["tables"].([]any)); n != 1 {
		t.Errorf("a draft makes nothing: %d tables", n)
	}
	if got := owner.must(200, "GET", "/v1/pages", nil)["pages"].([]any); len(got) != 0 {
		t.Errorf("a draft makes no page: %v", got)
	}

	// The model saw names and types only.
	all := strings.Join(model.prompts, "\n")
	for _, never := range []string{"Fix the Zenith gate", "2348099911122", "Mikano yard gen", "big-gen", "60kVA", "SN-77441199", "••"} {
		if strings.Contains(all, never) {
			t.Errorf("the model was told %q", never)
		}
	}
	for _, told := range []string{`"name": "jobs"`, `"name": "phone"`, `"type": "phone"`, `"name": "Generator"`, `"name": "serial"`} {
		if !strings.Contains(all, told) {
			t.Errorf("the model was not told %s", told)
		}
	}

	// Made and published: the page draws and its button works.
	made := owner.must(201, "POST", "/v1/page-builds/"+started["id"].(string)+"/make", map[string]any{"publish": true})
	if made["published"] != true || made["page"] != "open-tickets" || !strings.Contains(toJSON(made["tools"]), `"command":"new-ticket"`) {
		t.Fatalf("made: %v", made)
	}
	owner.must(409, "POST", "/v1/page-builds/"+started["id"].(string)+"/make", map[string]any{"publish": true})
	if again := owner.must(200, "GET", "/v1/page-builds/"+started["id"].(string), nil); again["status"] != "saved" || again["page_name"] != "open-tickets" {
		t.Errorf("after: %v", again)
	}
	pressTool(t, w, owner, "open-tickets", map[string]any{"block": "add", "action": 0}, map[string]any{"title": "Printer jam", "customer": "Bayo"})
	v := viewOf(t, owner, "open-tickets", nil)
	if blockOf(t, v, "open")["value"] != float64(1) || strings.Join(titles(blockOf(t, v, "list")), ",") != "Printer jam" {
		t.Errorf("the page after New ticket: %v", toJSON(v["blocks"]))
	}

	// Its model calls are the build's, as a workflow build's are.
	calls := owner.must(200, "GET", "/v1/ai/builds/"+started["id"].(string)+"/interactions", nil)
	if n := len(calls["interactions"].([]any)); n != 2 {
		t.Errorf("interactions: %d", n)
	}
	// A page build is no workflow build.
	owner.must(404, "GET", "/v1/ai/builds/"+started["id"].(string), nil)

	// Another business cannot read or make it.
	other := w.tenant(t, "Other", "other@describe-page.test")
	other.must(404, "GET", "/v1/page-builds/"+started["id"].(string), nil)
	if st, _ := other.do("POST", "/v1/page-builds/"+started["id"].(string)+"/make", nil); st != 404 {
		t.Errorf("another business made it: %d", st)
	}
}

// todayLagos is the day it is in Lagos, at midnight.
func todayLagos() time.Time {
	l := time.Now().In(time.FixedZone("WAT", 3600))
	return time.Date(l.Year(), l.Month(), l.Day(), 0, 0, 0, 0, time.UTC)
}

// Saved things of one kind as a page's source: the business's own of a
// kind it made (never a person's Mine), as the business's list shows them
// to its people: in the order their date comes, a private value masked
// (never opened) in the view, its row's button and the chat's short form;
// an API key has none; another business's never.
func TestPagesSavedThings(t *testing.T) {
	defer api.SetPageToolWait(0)()
	w := newTablesWorld(t)
	owner := w.tenant(t, "Ada Power", "owner@saved-page.test")
	viewer := addMember(t, w.world, owner, "vic", "viewer")
	gen := owner.must(201, "POST", "/v1/saved-kinds", map[string]any{"name": "Generator", "fields": []map[string]any{
		{"label": "Size", "type": "text"}, {"label": "Next service", "type": "date"}, {"label": "Serial", "type": "text", "sensitive": true}}})["kind"].(map[string]any)
	add := func(name, title, size, next string) {
		owner.must(201, "POST", "/v1/saved", map[string]any{"name": name, "title": title, "kind": "custom", "kind_id": gen["id"],
			"values": map[string]any{"size": size, "next_service": next, "serial": "SN-" + name + "-99887766"}})
	}
	today := todayLagos()
	add("yard", "Yard gen", "60kVA", today.AddDate(0, 0, 3).Format("2006-01-02"))
	add("office", "Office gen", "5kVA", today.AddDate(0, 0, -2).Format("2006-01-02"))
	add("spare", "Spare gen", "2kVA", today.AddDate(0, 2, 0).Format("2006-01-02"))
	// A person's own Generator kind and thing: Mine, never the business's.
	mine := owner.must(201, "POST", "/v1/me/saved-kinds", map[string]any{"name": "Generator", "fields": []map[string]any{{"label": "Size", "type": "text"}}})["kind"].(map[string]any)
	owner.must(201, "POST", "/v1/me/saved", map[string]any{"name": "home-gen", "title": "Home gen", "kind": "custom", "kind_id": mine["id"], "values": map[string]any{"size": "3kVA"}})

	publishWF(t, owner, "Log a service", toolDef("wf_logService", "Log a service", "log-service", "",
		`{"what":{"type":"string","title":"What"},"serial":{"type":"string","title":"Serial"}}`,
		`[{"name":"what","type":"string","title":"What","description":"What","value":"=steps.say.output.what"}]`,
		`{"type":"text","text":"Logged."}`,
		`{"id":"say","type":"transform","config":{"output":{"what":"=trigger.body.what"}}}`))
	w.env.Drain(t)

	doc := map[string]any{"page": "page/v1", "name": "generators", "title": "Generators",
		"filters": []map[string]any{{"id": "q", "type": "search"}},
		"blocks": []map[string]any{
			{"id": "soon", "type": "figure", "label": "Due within a week", "value": "count", "source": map[string]any{"saved": "generator", "coming": "next_service", "within": 7}},
			{"id": "list", "type": "list", "title": "Generators", "source": map[string]any{"saved": "Generator", "coming": "next_service", "uses": []string{"q"}},
				"item":    map[string]any{"title": "=row.title", "sub": "=row.size + ' · ' + row.serial", "state": map[string]any{"text": "=row.coming"}},
				"detail":  map[string]any{"title": "=row.title", "lines": []map[string]any{{"label": "Serial", "value": "=row.serial"}, {"label": "Next service", "value": "=row.next_service"}}},
				"actions": []map[string]any{{"label": "Log a service", "tool": "log-service", "inputs": map[string]any{"what": "=row.title", "serial": "=row.serial"}}}},
		}}
	bad := map[string]any{"page": "page/v1", "name": "bad", "title": "Bad", "blocks": []map[string]any{
		{"id": "l", "type": "list", "source": map[string]any{"saved": "Generator", "coming": "serial"}, "item": map[string]any{"title": "=row.title"}},
		{"id": "m", "type": "list", "source": map[string]any{"saved": "Car"}, "item": map[string]any{"title": "=row.title"}}}}
	ps := toJSON(owner.must(200, "POST", "/v1/pages/validate", map[string]any{"document": bad})["problems"])
	for _, want := range []string{"kept private", "has not made"} {
		if !strings.Contains(ps, want) {
			t.Errorf("validate: want %q in %s", want, ps)
		}
	}
	choices := toJSON(owner.must(200, "GET", "/v1/page-choices", nil)["saved_kinds"])
	if !strings.Contains(choices, `"key":"next_service"`) || !strings.Contains(choices, `"private":true`) || strings.Contains(choices, "Yard gen") {
		t.Errorf("choices: %s", choices)
	}
	owner.must(201, "POST", "/v1/pages", map[string]any{"document": doc})
	owner.must(200, "POST", "/v1/pages/generators/publish", nil)

	for _, c := range []*client{owner, viewer} {
		v := viewOf(t, c, "generators", nil)
		all := toJSON(v)
		if strings.Contains(all, "99887766") || strings.Contains(all, "Home gen") {
			t.Errorf("a private value, or someone's Mine, on the page: %s", all)
		}
		if blockOf(t, v, "soon")["value"] != float64(2) {
			t.Errorf("due within a week, and the late: %v", blockOf(t, v, "soon"))
		}
		list := blockOf(t, v, "list")
		var got []string
		for _, r := range list["rows"].([]any) {
			m := r.(map[string]any)
			got = append(got, fmt.Sprintf("%s|%s|%s", m["title"], m["state"].(map[string]any)["text"], m["state"].(map[string]any)["tone"]))
		}
		if strings.Join(got, ";") != "Office gen|2 days ago|bad;Yard gen|In 3 days|warn;Spare gen|"+list["rows"].([]any)[2].(map[string]any)["state"].(map[string]any)["text"].(string)+"|plain" || list["count"] != float64(3) {
			t.Errorf("in the order they come: %v", got)
		}
		if sub := list["rows"].([]any)[1].(map[string]any)["sub"]; sub != "60kVA · ••7766" {
			t.Errorf("masked as the list shows it: %v", sub)
		}
	}
	if l := blockOf(t, viewOf(t, owner, "generators", map[string]any{"q": "office"}), "list"); strings.Join(titles(l), ",") != "Office gen" {
		t.Errorf("search: %v", titles(l))
	}

	// Its row's button: the title filled in, the private serial asked, never filled.
	row := blockOf(t, viewOf(t, owner, "generators", nil), "list")["rows"].([]any)[0].(map[string]any)
	pressed := owner.must(200, "POST", "/v1/pages/generators/act?filters=%7B%7D", map[string]any{"block": "list", "row": row["id"], "action": 0})
	inputs := toJSON(pressed["tool"].(map[string]any)["inputs"])
	if !strings.Contains(inputs, `"what":"Office gen"`) || strings.Contains(inputs, "serial") {
		t.Errorf("pressed: %v", inputs)
	}

	// An API key has no saved things.
	key := owner.must(201, "POST", "/v1/api-keys", map[string]any{"name": "ci", "permissions": []string{"run.read"}})
	kc := &client{t: t, base: w.base, token: key["key"].(string)}
	kv := kc.must(200, "GET", "/v1/pages/generators/view", nil)
	if l := blockOf(t, kv, "list"); l["unavailable"] == nil || l["rows"] != nil {
		t.Errorf("an API key: %v", l)
	}

	// Another business: none of these, and no kind of that name.
	other := w.tenant(t, "Other", "other@saved-page.test")
	other.must(201, "POST", "/v1/pages", map[string]any{"document": doc})
	if ps := toJSON(other.must(200, "GET", "/v1/pages/generators", nil)["problems"]); !strings.Contains(ps, "has not made") {
		t.Errorf("another business: %s", ps)
	}
}

// Saved things on WhatsApp: the page's short form lists them as the page
// does, a private value masked.
func TestPagesSavedThingsShortForm(t *testing.T) {
	w := newWAWorld(t)
	owner := w.tenant(t, "Ada Power", "owner@saved-short.test")
	gen := owner.must(201, "POST", "/v1/saved-kinds", map[string]any{"name": "Generator", "fields": []map[string]any{
		{"label": "Next service", "type": "date"}, {"label": "Serial", "type": "text", "sensitive": true}}})["kind"].(map[string]any)
	owner.must(201, "POST", "/v1/saved", map[string]any{"name": "yard", "title": "Yard gen", "kind": "custom", "kind_id": gen["id"],
		"values": map[string]any{"next_service": todayLagos().AddDate(0, 0, 1).Format("2006-01-02"), "serial": "SN-55443322"}})
	owner.must(201, "POST", "/v1/pages", map[string]any{"document": map[string]any{"page": "page/v1", "name": "generators", "title": "Generators",
		"blocks": []map[string]any{{"id": "list", "type": "list", "title": "Due", "source": map[string]any{"saved": "Generator", "coming": "next_service"},
			"item": map[string]any{"title": "=row.title", "sub": "=row.serial", "state": map[string]any{"text": "=row.coming"}}}}}})
	owner.must(200, "POST", "/v1/pages/generators/publish", nil)
	const on = "+2348010000081"
	w.bind(t, owner, on)
	got := w.say(t, on, "generators")
	if !strings.Contains(got, "*Due*") || !strings.Contains(got, "Yard gen") || strings.Contains(got, "55443322") {
		t.Errorf("short form: %q", got)
	}
}
