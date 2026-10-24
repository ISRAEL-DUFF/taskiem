package templates_test

import (
	"strings"
	"testing"
	"testing/fstest"

	"github.com/israel-duff/taskiem/engine/wd"
	"github.com/israel-duff/taskiem/templates"
)

// The first callable templates (decision 0047, open question 1), each with
// the effect computed from its steps.
func TestCallableTemplates(t *testing.T) {
	reg := registry(t)
	want := map[string]templates.Effect{
		"pay-a-list":               {Class: templates.EffectMovesMoney, WaitsForPerson: true, WaitsForEvent: true, Timeout: "7d"},
		"run-payroll":              {Class: templates.EffectMovesMoney, WaitsForPerson: true, WaitsForEvent: true, Timeout: "70d"},
		"send-invoice":             {Class: templates.EffectWrite, WaitsForEvent: true},
		"payrolla-payroll-summary": {Class: templates.EffectWrite, Timeout: "1h"},
		"payrolla-record-payroll":  {Class: templates.EffectWrite, Timeout: "1h"},
	}
	n := 0
	for _, tpl := range templates.Default().List() {
		if !tpl.Callable {
			continue
		}
		n++
		doc, err := tpl.Instantiate(tpl.Examples(), "")
		if err != nil {
			t.Fatal(err)
		}
		def, err := wd.Load(doc)
		if err != nil {
			t.Fatal(err)
		}
		got := templates.EffectOf(def, reg)
		if w, ok := want[tpl.ID]; ok && got != w {
			t.Errorf("%s: effect %+v, want %+v", tpl.ID, got, w)
		}
	}
	for id := range want {
		if tpl, ok := templates.Default().Get(id); !ok || !tpl.Callable {
			t.Errorf("%s is not callable", id)
		}
	}
	if n < len(want) {
		t.Errorf("%d callable templates, want at least %d", n, len(want))
	}
}

// A callable template is held to its interface when the library loads.
func TestCallableRules(t *testing.T) {
	const good = `{"id":"call","title":"Call","summary":"S","description":"D","category":"payments","tags":[],"connectors":[],"params":[],
	 "callable":true,
	 "outputs":[{"name":"n","type":"integer","title":"N","description":"N","value":"=steps.count.output.n"},
	            {"name":"told","type":"boolean","title":"Told","description":"T","value":"=steps.?tell.output.hasValue()"}],
	 "definition":{"schema":"wd/v1","id":"wf_call","version":1,"name":"Call","trigger":{"type":"manual"},
	  "inputs":{"schema":{"type":"object","properties":{"x":{"type":"integer"}}}},
	  "steps":[{"id":"count","type":"transform","config":{"output":{"n":"=trigger.body.x + 1","at":"=has(trigger.scheduled_time) ? trigger.scheduled_time : ''"}}},
	           {"id":"tell","type":"transform","needs":["count"],"when":"=steps.count.output.n > 2","config":{"output":{"ok":true}}},
	           {"id":"loop","type":"foreach","needs":["count"],"config":{"items":"=[1]","steps":[{"id":"inner","type":"transform","config":{"output":{"i":"=item"}}}]}}]}}`
	load := func(doc string) error {
		_, err := templates.Load(fstest.MapFS{"library/call.json": {Data: []byte(doc)}})
		return err
	}
	if err := load(good); err != nil {
		t.Fatal(err)
	}
	for name, c := range map[string]struct{ old, new, want string }{
		"outputs without callable": {`"callable":true,`, ``, "only for a callable"},
		"no run inputs":            {`"inputs":{"schema":{"type":"object","properties":{"x":{"type":"integer"}}}},`, ``, "needs run inputs"},
		"no outputs":               {`"outputs":[{"name":"n","type":"integer","title":"N","description":"N","value":"=steps.count.output.n"},` + "\n\t            " + `{"name":"told","type":"boolean","title":"Told","description":"T","value":"=steps.?tell.output.hasValue()"}],`, `"outputs":[],`, "at least one output"},
		"a step that may not run":  {`steps.?tell.output.hasValue()`, `steps.tell.output.ok`, "optional chain"},
		"a nested step":            {`=steps.count.output.n"}`, `=steps.inner.output.i"}`, "not a top-level step"},
		"the step's error":         {`=steps.count.output.n"}`, `=steps.count.error.message"}`, "not the step's"},
		"another root":             {`=steps.count.output.n"}`, `=env.x"}`, "not env"},
		"not an expression":        {`"value":"=steps.count.output.n"`, `"value":"steps.count.output.n"`, "must be an expression"},
		"an unknown type":          {`"type":"integer","title":"N"`, `"type":"money","title":"N"`, "unknown type"},
		"a trigger field untested": {`"=has(trigger.scheduled_time) ? trigger.scheduled_time : ''"`, `"=trigger.scheduled_time"`, "tests it with has()"},
	} {
		bad := strings.Replace(good, c.old, c.new, 1)
		if bad == good {
			t.Fatalf("%s: the case changes nothing", name)
		}
		if err := load(bad); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: %v, want %q", name, err, c.want)
		}
	}
}

func TestMayNotRun(t *testing.T) {
	tpl, _ := templates.Default().Get("pay-a-list")
	doc, err := tpl.Instantiate(tpl.Examples(), "")
	if err != nil {
		t.Fatal(err)
	}
	def, err := wd.Load(doc)
	if err != nil {
		t.Fatal(err)
	}
	may := templates.MayNotRun(def)
	for id, want := range map[string]bool{"read_list": false, "approve": false, "pay_all": true, "lines": true, "summary": true, "record": true, "tell_rejected": true} {
		if may[id] != want {
			t.Errorf("%s: may not run %v, want %v", id, may[id], want)
		}
	}
}
