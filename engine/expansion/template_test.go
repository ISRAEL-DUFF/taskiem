package expansion_test

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/israel-duff/taskiem/engine/expansion"
	"github.com/israel-duff/taskiem/engine/wd"
	"github.com/israel-duff/taskiem/engine/wdtest"
	"github.com/israel-duff/taskiem/templates"
)

// Three small callable templates: greet tells a name; welcome uses greet
// for each name and counts them; party uses welcome. loop uses itself.
var lib = map[string]string{
	"greet": `{"id":"greet","title":"Greet","summary":"S","description":"D","category":"notifications","tags":[],"connectors":["notify@1"],
	 "params":[{"name":"title","type":"string","title":"T","description":"T","required":false,"default":"Hello","example":"Hello"}],
	 "callable":true,
	 "outputs":[{"name":"said","type":"string","title":"Said","description":"What was said","value":"=steps.say.output.message"},
	            {"name":"loud","type":"boolean","title":"Loud","description":"Shouted too","value":"=steps.?shout.output.hasValue()"}],
	 "definition":{"schema":"wd/v1","id":"wf_greet","version":1,"name":"Greet","trigger":{"type":"manual"},
	  "inputs":{"schema":{"type":"object","required":["name"],"additionalProperties":false,"properties":{"name":{"type":"string"},"loud":{"type":"boolean"}}}},
	  "steps":[{"id":"say","type":"transform","config":{"output":{"message":"={{title}} + ', ' + trigger.body.name"}}},
	           {"id":"shout","type":"connector","connector":"notify@1","action":"tell","needs":["say"],"when":"=has(trigger.body.loud) && trigger.body.loud",
	            "input":{"title":"{{title}}","message":"=steps.say.output.message"}}]}}`,
	"welcome": `{"id":"welcome","title":"Welcome","summary":"S","description":"D","category":"notifications","tags":[],"connectors":["notify@1"],"params":[],
	 "callable":true,
	 "outputs":[{"name":"count","type":"integer","title":"Count","description":"How many","value":"=size(steps.each.output)"},
	            {"name":"first","type":"string","title":"First","description":"The first greeting","value":"=steps.each.output[0].hi.said"}],
	 "definition":{"schema":"wd/v1","id":"wf_welcome","version":1,"name":"Welcome","trigger":{"type":"manual"},
	  "inputs":{"schema":{"type":"object","properties":{"names":{"type":"array"}}}},
	  "steps":[{"id":"each","type":"foreach","config":{"items":"=trigger.body.names","steps":[
	    {"id":"hi","type":"template","template":"greet","params":{"title":"Welcome"},"input":{"name":"=item"}}]}}]}}`,
	"party": `{"id":"party","title":"Party","summary":"S","description":"D","category":"notifications","tags":[],"connectors":["notify@1"],"params":[],
	 "callable":true,
	 "outputs":[{"name":"guests","type":"integer","title":"Guests","description":"How many","value":"=steps.w.output.count"}],
	 "definition":{"schema":"wd/v1","id":"wf_party","version":1,"name":"Party","trigger":{"type":"manual"},
	  "inputs":{"schema":{"type":"object","properties":{"names":{"type":"array"}}}},
	  "steps":[{"id":"w","type":"template","template":"welcome","input":{"names":"=trigger.body.names"}}]}}`,
	"loop": `{"id":"loop","title":"Loop","summary":"S","description":"D","category":"notifications","tags":[],"connectors":[],"params":[],
	 "callable":true,
	 "outputs":[{"name":"x","type":"integer","title":"X","description":"X","value":"=steps.again.output.x"}],
	 "definition":{"schema":"wd/v1","id":"wf_loop","version":1,"name":"Loop","trigger":{"type":"manual"},
	  "inputs":{"schema":{"type":"object","properties":{}}},
	  "steps":[{"id":"again","type":"template","template":"loop"}]}}`,
	"plain": `{"id":"plain","title":"Plain","summary":"S","description":"D","category":"notifications","tags":[],"connectors":[],"params":[],
	 "definition":{"schema":"wd/v1","id":"wf_plain","version":1,"name":"Plain","trigger":{"type":"manual"},
	  "steps":[{"id":"t","type":"transform","config":{"output":{}}}]}}`,
}

func library(t *testing.T) expansion.Templates {
	t.Helper()
	fs := fstest.MapFS{}
	for id, doc := range lib {
		fs["library/"+id+".json"] = &fstest.MapFile{Data: []byte(doc)}
	}
	l, err := templates.Load(fs)
	if err != nil {
		t.Fatal(err)
	}
	return expansion.Library(l)
}

func caller(steps string) []byte {
	return []byte(`{"schema":"wd/v1","id":"wf_c","version":1,"name":"C","trigger":{"type":"manual"},"steps":` + steps + `}`)
}

func TestTemplateExpansion(t *testing.T) {
	doc := caller(`[
	  {"id":"names","type":"transform","config":{"output":{"list":["Ada","Bola"]}}},
	  {"id":"hi","type":"template","template":"greet","needs":["names"],"params":{"title":"Hi"},"input":{"name":"=steps.names.output.list[0]","loud":true},
	   "on_error":{"steps":[{"id":"hi_failed","type":"transform","config":{"output":{"why":"=steps.hi.error.message"}}}]}},
	  {"id":"after","type":"transform","needs":["hi"],"config":{"output":{"said":"=steps.hi.output.said"}}}]`)
	out, used, err := expansion.ExpandTemplates(doc, library(t))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(used, ",") != "greet" {
		t.Errorf("used %v", used)
	}
	def, err := wd.Load(out)
	if err != nil {
		t.Fatal(err)
	}
	body := def.Step("hi__body")
	if body == nil || body.Type != "branch" || strings.Join(body.Needs, ",") != "names" || body.OnError == nil {
		t.Fatalf("body: %+v", body)
	}
	if got := body.OnError.Steps[0].Transform.Output.(map[string]any)["why"]; got != "=steps.hi__body.error.message" {
		t.Errorf("the caller's on_error reads %v", got)
	}
	say, shout := def.Step("hi__say"), def.Step("hi__shout")
	if say == nil || shout == nil || strings.Join(say.Needs, ",") != "hi__input" || strings.Join(shout.Needs, ",") != "hi__say" {
		t.Fatalf("say %+v shout %+v", say, shout)
	}
	if *say.Origin != (wd.Origin{Template: "greet", Step: "hi", ID: "say"}) || say.Key() != "say" {
		t.Errorf("origin %+v", say.Origin)
	}
	if shout.When != "=has(steps.hi__input.output.loud) && steps.hi__input.output.loud" {
		t.Errorf("when %q", shout.When)
	}
	if shout.Input["message"] != "=steps.hi__say.output.message" || shout.Input["title"] != "Hi" {
		t.Errorf("input %v", shout.Input)
	}
	res := def.Step("hi")
	if res.Type != "transform" || strings.Join(res.Needs, ",") != "hi__body" {
		t.Fatalf("result %+v", res)
	}
	want := map[string]any{"said": "=steps.hi__body.output.steps.say.message", "loud": "=steps.hi__body.output.steps.?shout.hasValue()"}
	if got := res.Transform.Output; !equalJSON(got, want) {
		t.Errorf("outputs %v", got)
	}

	// It runs: the result is the template's outputs.
	reg := registry(t)
	r := wdtest.RunCase(def, reg, nil, wdtest.Case{Name: "runs",
		Mocks:  map[string]wdtest.Mocks{"hi__shout": {{Output: map[string]any{"people": 1}}}},
		Expect: wdtest.Expect{Status: "completed", Outputs: map[string]any{"hi": map[string]any{"said": "Hi, Ada", "loud": true}, "after": map[string]any{"said": "Hi, Ada"}}}})
	if !r.Passed() {
		t.Errorf("%v\n%s", r.Failures, strings.Join(r.Trace, "\n"))
	}
}

// A template inside a template (welcome uses greet for each name): ids
// carry both prefixes, and the keys inside a foreach stay the template's
// own.
func TestNestedTemplates(t *testing.T) {
	doc := caller(`[{"id":"p","type":"template","template":"welcome","input":{"names":["Ada","Bola","Chi"]}}]`)
	out, used, err := expansion.ExpandTemplates(doc, library(t))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(used, ",") != "greet,welcome" {
		t.Errorf("used %v", used)
	}
	def, err := wd.Load(out)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"p__body", "p__input", "p", "p__each", "p__hi__body", "p__hi__input", "p__hi__say", "p__hi"} {
		if def.Step(id) == nil {
			t.Errorf("no step %s", id)
		}
	}
	if o := def.Step("p__hi__say").Origin; *o != (wd.Origin{Template: "greet", Step: "p__hi", ID: "say"}) {
		t.Errorf("origin %+v", o)
	}
	if o := def.Step("p__hi").Origin; *o != (wd.Origin{Template: "welcome", Step: "p", ID: "hi"}) {
		t.Errorf("origin %+v", o)
	}
	r := wdtest.RunCase(def, registry(t), nil, wdtest.Case{Name: "nested",
		Expect: wdtest.Expect{Status: "completed", Outputs: map[string]any{
			"p":                     map[string]any{"count": 3, "first": "Welcome, Ada"},
			"p__each[1].p__hi":      map[string]any{"said": "Welcome, Bola", "loud": false},
			"p__each[2].p__hi__say": map[string]any{"message": "Welcome, Chi"},
		}}})
	if !r.Passed() {
		t.Errorf("%v\n%s", r.Failures, strings.Join(r.Trace, "\n"))
	}
}

func TestTemplateRefusals(t *testing.T) {
	find := library(t)
	for name, c := range map[string]struct{ steps, want string }{
		"unknown":      {`[{"id":"x","type":"template","template":"nope"}]`, `no template "nope"`},
		"not callable": {`[{"id":"x","type":"template","template":"plain"}]`, "not callable"},
		"cycle":        {`[{"id":"x","type":"template","template":"loop"}]`, "uses itself"},
		"missing":      {`[{"id":"x","type":"template","template":"greet"}]`, "needs name"},
		"extra":        {`[{"id":"x","type":"template","template":"greet","input":{"name":"A","age":3}}]`, "has age"},
		"bad param":    {`[{"id":"x","type":"template","template":"greet","params":{"title":5},"input":{"name":"A"}}]`, "title"},
		"too deep":     {`[{"id":"x","type":"template","template":"party","input":{"names":[]}}]`, "nest at most 3 deep, counting the workflow: the workflow uses party, which uses welcome, which cannot use greet"},
	} {
		_, _, err := expansion.ExpandTemplates(caller(c.steps), find)
		if c.want == "" {
			if err != nil {
				t.Errorf("%s: %v", name, err)
			}
			continue
		}
		var ref *expansion.Refusal
		if !errors.As(err, &ref) || ref.Step != "x" || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: %v, want a refusal of x with %q", name, err, c.want)
		}
	}
}

// A template step's id is bounded and its parameters are values.
func TestTemplateStepRules(t *testing.T) {
	for name, c := range map[string]struct{ step, want string }{
		"long id":    {`{"id":"a_very_long_step_id","type":"template","template":"greet"}`, "at most 14"},
		"underscore": {`{"id":"a__b","type":"template","template":"greet"}`, "may not contain __"},
		"expression": {`{"id":"a","type":"template","template":"greet","params":{"title":"=trigger.body.t"}}`, "never an expression"},
		"retry":      {`{"id":"a","type":"template","template":"greet","retry":{"max":3}}`, "no retry"},
	} {
		probs := wd.Validate(caller(`[` + c.step + `]`))
		if len(probs) == 0 || !strings.Contains(probs[0].Message, c.want) {
			t.Errorf("%s: %v, want %q", name, probs, c.want)
		}
	}
}

func equalJSON(a, b any) bool {
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	return string(x) == string(y)
}
