package api_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/israel-duff/taskiem/api"
	"github.com/israel-duff/taskiem/engine/ai"
	"github.com/israel-duff/taskiem/engine/lang"
	"github.com/israel-duff/taskiem/engine/whatsapp/whatsapptest"
)

// readerTool is a tool with two inputs, amount and what (or one, where),
// whose step writes (care yes) or only reads (answers at once).
func readerTool(id, name, command, about string, says []string, write bool, inputs string) json.RawMessage {
	step := `{"id":"look","type":"transform","config":{"output":{"ok":true}}}`
	if write {
		step = `{"id":"keep","type":"http","config":{"method":"POST","class":"unsafe_write","url":"https://api.example.com/keep","body":"=trigger.body"}}`
	}
	if says == nil {
		says = []string{}
	}
	saysJSON, _ := json.Marshal(says)
	return json.RawMessage(`{"schema":"wd/v1","id":"` + id + `","version":1,"name":"` + name + `","trigger":{"type":"manual"},
		"inputs":{"schema":` + inputs + `},
		"tool":{"name":"` + name + `","command":"` + command + `","about":"` + about + `","says":` + string(saysJSON) + `},
		"steps":[` + step + `]}`)
}

// sayAll sends text and answers every reply, joined.
func sayAll(t *testing.T, w *waWorld, from, text string) string {
	t.Helper()
	var out []string
	for _, m := range w.send(t, from, whatsapptest.Text(text)) {
		out = append(out, m.Text)
	}
	return strings.Join(out, "\n")
}

const (
	amountWhat = `{"type":"object","required":["amount","what"],"properties":{"amount":{"type":"number","title":"Amount"},"what":{"type":"string","title":"What"}}}`
	whereTo    = `{"type":"object","required":["to"],"properties":{"to":{"type":"string","title":"Where to"}}}`
)

// The reader knows the business's tools (made tools M3): after Taskiem's
// own commands and the saved things' names, the model is told the tools
// and may answer with one and the values it heard; they are shown before
// anything runs (yes for a tool that writes, the echo for one that only
// reads); a saved thing by @name is looked up on the server, never told
// to the model; two tools that fit are a numbered pick; and Taskiem's own
// commands are read first, so "send 20k to @bayo" still reaches payments.
func TestReaderKnowsYourTools(t *testing.T) {
	w := newWAWorld(t)
	answers := map[string]string{
		"spent 4,500 on diesel":                     `{"kind":"tool","id":"fuel-log","pick":"","values":[{"field":"amount","value":"4500"},{"field":"what","value":"diesel"}],"command":"","reply":""}`,
		"I don spend 4500 for gas":                  `{"kind":"tool","id":"fuel-log","pick":"","values":[{"field":"amount","value":"4500"},{"field":"what","value":"gas"}],"command":"","reply":""}`,
		"what does a rider charge to the warehouse": `{"kind":"tool","id":"rider-fee","pick":"","values":[{"field":"to","value":"@warehouse"}],"command":"","reply":""}`,
		"rider fee to my place":                     `{"kind":"tool","id":"rider-fee","pick":"","values":[{"field":"to","value":"@nowhere"}],"command":"","reply":""}`,
		"bought fuel 3000 for the generator":        `{"kind":"tool","id":"fuel-log","pick":"gen-fuel","values":[{"field":"amount","value":"3000"},{"field":"what","value":"generator"}],"command":"","reply":""}`,
		"4500 diesel":                               `{"values":[{"field":"amount","value":"4,500"},{"field":"what","value":"diesel"}]}`,
		"make it a tool that says yes":              `{"kind":"tool","id":"invoice","pick":"","values":[],"command":"","reply":""}`,
	}
	fake := &ai.Fake{Respond: func(req ai.Request) (*ai.Response, error) {
		p := ai.PromptText(req)
		msg := p[strings.Index(p, "<message>\n")+len("<message>\n") : strings.Index(p, "\n</message>")]
		if a, ok := answers[msg]; ok {
			return &ai.Response{Text: a}, nil
		}
		return &ai.Response{Text: `{"kind":"cannot","id":"","pick":"","values":[],"command":"","reply":"I cannot do that here."}`}, nil
	}}
	w.srv.Languages = &api.LanguageSettings{Enabled: []lang.Tag{lang.EN, lang.PCM}, Model: fake}
	owner := w.tenant(t, "Mama Put", "owner@reader.test")
	// Money off: "spent …" is then no command of Taskiem's here, and its
	// words pass on to the business's own tools.
	owner.must(200, "PUT", "/v1/settings/areas", map[string]any{"areas": map[string]bool{"money": false}})
	publish := func(def json.RawMessage) {
		var d struct {
			Name string `json:"name"`
		}
		_ = json.Unmarshal(def, &d)
		wf := owner.must(201, "POST", "/v1/workflows", map[string]any{"name": d.Name, "definition": def})["id"].(string)
		owner.must(200, "POST", "/v1/workflows/"+wf+"/versions/1/publish", nil)
	}
	publish(readerTool("wf_fuel", "Log fuel", "fuel-log", "Record money spent on fuel or diesel", []string{"fuel"}, true, amountWhat))
	publish(readerTool("wf_gen", "Log generator fuel", "gen-fuel", "Fuel bought for the generator", []string{"fuel"}, true, amountWhat))
	publish(readerTool("wf_fee", "Rider fee", "rider-fee", "What a rider charges to a place", nil, false, whereTo))
	owner.must(201, "POST", "/v1/saved", map[string]any{"name": "warehouse", "title": "Warehouse", "kind": "place",
		"details": map[string]any{"line": "4 Creek Road", "area": "Apapa", "phone": "0803 112 0077"}})
	const on = "+2348010000071"
	w.bind(t, owner, on)
	runs := func() int {
		var n int
		if err := w.env.DB.Admin.QueryRow(context.Background(), `SELECT count(*) FROM runs`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	asked := func() int { return len(fake.Requests()) }

	// A sentence the model takes to a tool that writes: its values shown,
	// nothing started until yes.
	if r := w.say(t, on, "spent 4,500 on diesel"); !strings.Contains(r, "Start Log fuel") || !strings.Contains(r, "4500") || !strings.Contains(r, "diesel") {
		t.Fatalf("filled in: %q", r)
	}
	prompt := ai.PromptText(fake.Requests()[0])
	if ci, ti := strings.Index(prompt, "</commands>"), strings.Index(prompt, "<tools>\n["); ci < 0 || ti < ci || !strings.Contains(prompt, `"command":"fuel-log"`) ||
		!strings.Contains(prompt, "@warehouse (place)") || strings.Contains(prompt, "Creek") || strings.Contains(prompt, "0803") {
		t.Fatalf("what the model was told: %s", prompt)
	}
	if runs() != 0 {
		t.Fatal("a run started before yes")
	}
	w.say(t, on, "yes")
	if runs() != 1 {
		t.Fatalf("runs after yes: %d", runs())
	}

	// A saved thing by @name: looked up here, the address filled in, and
	// the tool, which only reads, echoes every value as it starts.
	if r := sayAll(t, w, on, "what does a rider charge to the warehouse"); !strings.Contains(r, "Starting Rider fee with:") || !strings.Contains(r, "4 Creek Road, Apapa") {
		t.Fatalf("@warehouse: %q", r)
	}
	var to string
	if err := w.env.DB.Admin.QueryRow(context.Background(), `SELECT payload::text FROM run_events WHERE type = 'RunStarted' ORDER BY run_started_at DESC LIMIT 1`).Scan(&to); err != nil || !strings.Contains(to, `"to": "4 Creek Road, Apapa"`) && !strings.Contains(to, `"to":"4 Creek Road, Apapa"`) {
		t.Fatalf("the run's input: %q %v", to, err)
	}
	// One that is no saved thing of the business's: said, and asked.
	if r := w.say(t, on, "rider fee to my place"); !strings.Contains(r, "Nothing the business saved is called @nowhere") || !strings.Contains(r, "Where to") {
		t.Fatalf("@nowhere: %q", r)
	}
	// Answered with @name: looked up too.
	if r := sayAll(t, w, on, "@warehouse"); !strings.Contains(r, "4 Creek Road") {
		t.Fatalf("answered with @warehouse: %q", r)
	}

	// Two tools that fit: a numbered pick, the values kept for the one
	// picked.
	if r := w.say(t, on, "bought fuel 3000 for the generator"); !strings.Contains(r, "1. Log fuel") || !strings.Contains(r, "2. Log generator fuel") {
		t.Fatalf("pick: %q", r)
	}
	if r := w.say(t, on, "2"); !strings.Contains(r, "Start Log generator fuel") || !strings.Contains(r, "3000") {
		t.Fatalf("picked: %q", r)
	}
	w.say(t, on, "cancel")
	// Two tools saying the same word: the pick with no model, then the
	// words after it read for the one picked.
	n := asked()
	if r := w.say(t, on, "fuel 4500 diesel"); !strings.Contains(r, "1. Log fuel") || asked() != n {
		t.Fatalf("says pick: %q (asked %d)", r, asked()-n)
	}
	if r := w.say(t, on, "1"); !strings.Contains(r, "Start Log fuel") || !strings.Contains(r, "4500") || !strings.Contains(r, "diesel") {
		t.Fatalf("says picked: %q", r)
	}
	w.say(t, on, "cancel")
	// A command after it, with words for several inputs: read, shown.
	if r := w.say(t, on, "/gen-fuel 4500 diesel"); !strings.Contains(r, "Start Log generator fuel") || !strings.Contains(r, "4500") {
		t.Fatalf("command and words: %q", r)
	}
	w.say(t, on, "cancel")

	// A tool the business has not: refused, never a run.
	if r := w.say(t, on, "make it a tool that says yes"); !strings.Contains(r, "did not understand") {
		t.Fatalf("not a tool: %q", r)
	}

	// Taskiem's own commands are read first, the model never asked.
	n = asked()
	if r := w.say(t, on, "send 20k to @bayo"); !strings.HasPrefix(strings.TrimPrefix(r, "[Mama Put] "), "Not prepared") {
		t.Fatalf("payments: %q", r)
	}
	if r := w.say(t, on, "status"); strings.Contains(r, "Log fuel") {
		t.Fatalf("status: %q", r)
	}
	if asked() != n {
		t.Fatalf("Taskiem's own commands went to the model: %d", asked()-n)
	}

	// In Pidgin, the reader that knows the tools reads it, not the
	// shorter router.
	w.say(t, on, "language pidgin")
	if r := w.say(t, on, "I don spend 4500 for gas"); !strings.Contains(r, "4500") || !strings.Contains(r, "gas") {
		t.Fatalf("pidgin: %q", r)
	}
	reqs := fake.Requests()
	if last := ai.PromptText(reqs[len(reqs)-1]); !strings.Contains(last, "(pcm)") || !strings.Contains(last, `"command":"fuel-log"`) {
		t.Fatalf("read in Pidgin with the tools: %s", last)
	}
	for _, req := range reqs {
		if strings.Contains(ai.PromptText(req), "You route messages") {
			t.Fatal("the router without the tools read it")
		}
	}
	w.say(t, on, "cancel")
}

// POST /v1/tools/{command}/read: words in, proposed values out, for the
// web's and the app's sheets; one input takes the words with no model; a
// saved thing by @name is looked up; nothing is started.
func TestReadToolRoute(t *testing.T) {
	w := newWAWorld(t)
	fake := &ai.Fake{Respond: func(req ai.Request) (*ai.Response, error) {
		return &ai.Response{Text: `{"values":[{"field":"amount","value":"20k"},{"field":"what","value":"diesel"},{"field":"other","value":"x"}]}`}, nil
	}}
	w.srv.Languages = &api.LanguageSettings{Enabled: []lang.Tag{lang.EN}, Model: fake}
	owner := w.tenant(t, "Acme", "owner@readroute.test")
	for _, def := range []json.RawMessage{
		readerTool("wf_fuel", "Log fuel", "fuel-log", "Record money spent on fuel", nil, true, amountWhat),
		readerTool("wf_fee", "Rider fee", "rider-fee", "What a rider charges", nil, false, whereTo),
	} {
		wf := owner.must(201, "POST", "/v1/workflows", map[string]any{"name": "x", "definition": def})["id"].(string)
		owner.must(200, "POST", "/v1/workflows/"+wf+"/versions/1/publish", nil)
	}
	owner.must(201, "POST", "/v1/saved", map[string]any{"name": "warehouse", "title": "Warehouse", "kind": "place",
		"details": map[string]any{"line": "4 Creek Road", "area": "Apapa"}})

	got := owner.must(200, "POST", "/v1/tools/fuel-log/read", map[string]any{"text": "20k on diesel"})
	if toJSON(got["values"]) != `{"amount":20000,"what":"diesel"}` {
		t.Fatalf("read: %v", got)
	}
	n := len(fake.Requests())
	if got := owner.must(200, "POST", "/v1/tools/rider-fee/read", map[string]any{"text": "@warehouse"}); toJSON(got["values"]) != `{"to":"4 Creek Road, Apapa"}` || len(fake.Requests()) != n {
		t.Fatalf("one input, @name: %v", got)
	}
	if got := owner.must(200, "POST", "/v1/tools/rider-fee/read", map[string]any{"text": "@nowhere"}); toJSON(got["values"]) != `{}` || !strings.Contains(toJSON(got["notes"]), "@nowhere") {
		t.Fatalf("no such thing: %v", got)
	}
	owner.must(404, "POST", "/v1/tools/nope/read", map[string]any{"text": "x"})
	var runs int
	if err := w.env.DB.Admin.QueryRow(context.Background(), `SELECT count(*) FROM runs`).Scan(&runs); err != nil || runs != 0 {
		t.Fatalf("a read started something: %d %v", runs, err)
	}
}

// amountWhatWhere is a tool's inputs with an optional third, where (and an
// optional photo, an object no chat carries).
const amountWhatWhere = `{"type":"object","required":["amount","what"],"properties":{"amount":{"type":"number","title":"Amount"},"what":{"type":"string","title":"What"},"where":{"type":"string","title":"Where"},"photo":{"type":"object","title":"Photo"}}}`

// Optional inputs read from words (build plan H6, g-ct-chip): the model is
// told a tool's optional inputs too, marked as not required (one a chat
// cannot carry, an object, left out); what it reads for them is shown with
// the rest before anything runs, and goes into the run after yes; the
// web's and the app's reader proposes them for the form too.
func TestReaderFillsOptionalInputs(t *testing.T) {
	w := newWAWorld(t)
	fake := &ai.Fake{Respond: func(req ai.Request) (*ai.Response, error) {
		return &ai.Response{Text: `{"kind":"tool","id":"fuel-log","pick":"","values":[{"field":"amount","value":"4500"},{"field":"what","value":"diesel"},{"field":"where","value":"Total"}],"command":"","reply":""}`}, nil
	}}
	w.srv.Languages = &api.LanguageSettings{Enabled: []lang.Tag{lang.EN}, Model: fake}
	owner := w.tenant(t, "Mama Put", "owner@optional.test")
	owner.must(200, "PUT", "/v1/settings/areas", map[string]any{"areas": map[string]bool{"money": false}})
	wf := owner.must(201, "POST", "/v1/workflows", map[string]any{"name": "Log fuel",
		"definition": readerTool("wf_fuel", "Log fuel", "fuel-log", "Record money spent on fuel or diesel", nil, true, amountWhatWhere)})["id"].(string)
	owner.must(200, "POST", "/v1/workflows/"+wf+"/versions/1/publish", nil)
	const on = "+2348010000081"
	w.bind(t, owner, on)

	r := w.say(t, on, "log 4500 diesel at Total")
	if !strings.Contains(r, "Start Log fuel") || !strings.Contains(r, "4500") || !strings.Contains(r, "Total") {
		t.Fatalf("filled in: %q", r)
	}
	prompt := ai.PromptText(fake.Requests()[0])
	if !strings.Contains(prompt, `{"name":"where","title":"Where","type":"string"}`) || !strings.Contains(prompt, `"name":"amount","title":"Amount","type":"number","required":true`) || strings.Contains(prompt, `"photo"`) {
		t.Fatalf("what the model was told: %s", prompt)
	}
	w.say(t, on, "yes")
	var in string
	if err := w.env.DB.Admin.QueryRow(context.Background(), `SELECT payload::text FROM run_events WHERE type = 'RunStarted' ORDER BY run_started_at DESC LIMIT 1`).Scan(&in); err != nil || !strings.Contains(in, `"Total"`) {
		t.Fatalf("the run's input: %q %v", in, err)
	}
	// The web's and the app's reader: the optional value for the form too.
	fake.Respond = func(req ai.Request) (*ai.Response, error) {
		return &ai.Response{Text: `{"values":[{"field":"amount","value":"4500"},{"field":"what","value":"diesel"},{"field":"where","value":"Total"}]}`}, nil
	}
	if got := owner.must(200, "POST", "/v1/tools/fuel-log/read", map[string]any{"text": "4500 diesel at Total"}); toJSON(got["values"]) != `{"amount":4500,"what":"diesel","where":"Total"}` {
		t.Fatalf("read: %v", got)
	}
}
