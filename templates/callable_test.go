package templates_test

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/israel-duff/taskiem/engine/expansion"
	"github.com/israel-duff/taskiem/engine/wd"
	"github.com/israel-duff/taskiem/engine/wdtest"
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

// Each callable template's own workflow tests pass with the template used
// as a step of another workflow (decision 0047: "the template tests run on
// the expanded definition"): the caller has the template's trigger and run
// inputs and one template step, the case's mocks and expectations name the
// expanded steps, and the step's output is the template's outputs.
func TestCallableTemplatesAsSteps(t *testing.T) {
	reg := registry(t)
	n := 0
	for _, tpl := range templates.Default().List() {
		if !tpl.Callable {
			continue
		}
		raw, err := os.ReadFile(filepath.Join("tests", tpl.ID+".test.json"))
		if err != nil {
			t.Errorf("%s: a callable template needs workflow tests: %v", tpl.ID, err)
			continue
		}
		var tt templateTests
		dec := json.NewDecoder(bytes.NewReader(raw))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&tt); err != nil {
			t.Fatal(err)
		}
		for _, c := range tt.Cases {
			n++
			t.Run(tpl.ID+"/"+c.Name, func(t *testing.T) {
				vals := tpl.Examples()
				for k, v := range tt.Params {
					vals[k] = v
				}
				for k, v := range c.Params {
					vals[k] = v
				}
				own, err := tpl.Instantiate(vals, "")
				if err != nil {
					t.Fatal(err)
				}
				var ownDef map[string]any
				if err := json.Unmarshal(own, &ownDef); err != nil {
					t.Fatal(err)
				}
				// The step's input gives the fields the case's trigger has.
				input := map[string]any{}
				if trig, ok := c.Trigger.(map[string]any); ok {
					if body, ok := trig["body"].(map[string]any); ok {
						for k := range body {
							input[k] = "=trigger.body['" + k + "']"
						}
					}
				}
				caller := map[string]any{"schema": "wd/v1", "id": "wf_caller", "version": 1, "name": "Caller",
					"trigger": ownDef["trigger"], "inputs": ownDef["inputs"],
					"steps": []any{
						map[string]any{"id": "t", "type": "template", "template": tpl.ID, "params": vals, "input": input},
						map[string]any{"id": "outputs", "type": "transform", "needs": []any{"t"}, "config": map[string]any{"output": outputTypes(tpl)}},
					}}
				doc, err := json.Marshal(caller)
				if err != nil {
					t.Fatal(err)
				}
				expanded, used, err := expansion.ExpandTemplates(doc, expansion.Library(templates.Default()))
				if err != nil {
					t.Fatal(err)
				}
				if len(used) != 1 || used[0] != tpl.ID {
					t.Errorf("used %v", used)
				}
				def, err := wd.Load(expanded)
				if err != nil {
					t.Fatal(err)
				}
				cs := prefixCase(c.Case, "t__")
				r := wdtest.RunCase(def, reg, tt.Env, cs)
				if !r.Passed() {
					t.Fatalf("%s\n  trace:\n    %s\n  errors: %v", strings.Join(r.Failures, "\n"), strings.Join(r.Trace, "\n    "), r.Errors)
				}
				if r.Status != "completed" {
					return
				}
				// The step's output is the template's outputs, each given
				// with the type it declares.
				want := map[string]any{}
				for _, o := range tpl.Outputs {
					want[o.Name] = true
				}
				cs.Expect.Steps = cloneMap(cs.Expect.Steps)
				cs.Expect.Steps["outputs"] = "completed"
				cs.Expect.Outputs = cloneMap(cs.Expect.Outputs)
				cs.Expect.Outputs["outputs"] = want
				if out := wdtest.RunCase(def, reg, tt.Env, cs); !out.Passed() {
					t.Errorf("outputs: %s\n  errors: %v", strings.Join(out.Failures, "\n"), out.Errors)
				}
			})
		}
	}
	if n == 0 {
		t.Fatal("no callable template has tests")
	}
}

// outputTypes is a transform's output that is true for each output of tpl
// the template step gave with its declared type.
func outputTypes(tpl *templates.Template) map[string]any {
	cel := map[string]string{"string": "string", "integer": "int", "number": "double", "boolean": "bool", "list": "list", "object": "map"}
	out := map[string]any{}
	for _, o := range tpl.Outputs {
		v := "steps.t.output." + o.Name
		check := "type(" + v + ") == " + cel[o.Type]
		if o.Type == "number" {
			// A whole number comes back from the run's history an int.
			check = "(" + check + " || type(" + v + ") == int)"
		}
		out[o.Name] = "=has(" + v + ") && " + check
	}
	return out
}

func cloneMap[V any](m map[string]V) map[string]V {
	out := make(map[string]V, len(m)+1)
	for k, v := range m {
		out[k] = v
	}
	return out
}

// prefixCase renames a case's instance keys for a template expanded under
// a step: pay_all[0].pay is p+pay_all[0].p+pay.
func prefixCase(c wdtest.Case, p string) wdtest.Case {
	key := func(k string) string {
		pre := ""
		if rest, ok := strings.CutPrefix(k, "compensate:"); ok {
			pre, k = "compensate:", rest
		}
		parts := strings.Split(k, ".")
		for i, s := range parts {
			parts[i] = p + s
		}
		return pre + strings.Join(parts, ".")
	}
	c.Mocks = renameKeys(c.Mocks, key)
	c.Signals = renameKeys(c.Signals, key)
	c.Approvals = renameKeys(c.Approvals, key)
	c.Forms = renameKeys(c.Forms, key)
	c.Expect.Steps = renameKeys(c.Expect.Steps, key)
	c.Expect.Outputs = renameKeys(c.Expect.Outputs, key)
	c.Expect.Inputs = renameKeys(c.Expect.Inputs, key)
	c.Expect.Calls = renameKeys(c.Expect.Calls, key)
	return c
}

func renameKeys[V any](m map[string]V, fn func(string) string) map[string]V {
	if m == nil {
		return nil
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make(map[string]V, len(m))
	for _, k := range keys {
		out[fn(k)] = m[k]
	}
	return out
}

// The id budget holds on every rail: each callable template, used two
// deep under template steps with the longest ids allowed, expands (its
// capability steps too, on each rail that can run it) into ids a step may
// have.
func TestCallableTemplatesFitTwoDeepOnEveryRail(t *testing.T) {
	reg := registry(t)
	long := strings.Repeat("s", wd.MaxTemplateStepID)
	for _, tpl := range templates.Default().List() {
		if !tpl.Callable {
			continue
		}
		t.Run(tpl.ID, func(t *testing.T) {
			// A wrapper template uses tpl; a workflow uses the wrapper.
			fs := fstest.MapFS{}
			for _, other := range templates.Default().List() {
				raw, err := os.ReadFile(filepath.Join("library", other.ID+".json"))
				if err != nil {
					t.Fatal(err)
				}
				fs["library/"+other.ID+".json"] = &fstest.MapFile{Data: raw}
			}
			own, err := tpl.Instantiate(tpl.Examples(), "")
			if err != nil {
				t.Fatal(err)
			}
			var ownDef map[string]any
			if err := json.Unmarshal(own, &ownDef); err != nil {
				t.Fatal(err)
			}
			input := map[string]any{}
			props, _ := ownDef["inputs"].(map[string]any)["schema"].(map[string]any)["properties"].(map[string]any)
			for k := range props {
				input[k] = "=trigger.body['" + k + "']"
			}
			first := tpl.Outputs[0].Name
			wrap := map[string]any{"id": "wrap", "title": "Wrap", "summary": "S", "description": "D", "category": tpl.Category, "tags": []any{},
				"connectors": tpl.Connectors, "params": []any{}, "callable": true,
				"outputs": []any{map[string]any{"name": "out", "type": tpl.Outputs[0].Type, "title": "Out", "description": "Out", "value": "=steps." + long + ".output." + first}},
				"definition": map[string]any{"schema": "wd/v1", "id": "wf_wrap", "version": 1, "name": "Wrap", "trigger": map[string]any{"type": "manual"},
					"inputs": ownDef["inputs"],
					"steps":  []any{map[string]any{"id": long, "type": "template", "template": tpl.ID, "params": tpl.Examples(), "input": input}}}}
			raw, err := json.Marshal(wrap)
			if err != nil {
				t.Fatal(err)
			}
			fs["library/wrap.json"] = &fstest.MapFile{Data: raw}
			lib, err := templates.Load(fs)
			if err != nil {
				t.Fatal(err)
			}
			caller, err := json.Marshal(map[string]any{"schema": "wd/v1", "id": "wf_caller", "version": 1, "name": "Caller",
				"trigger": map[string]any{"type": "manual"}, "inputs": ownDef["inputs"],
				"steps": []any{map[string]any{"id": long, "type": "template", "template": "wrap", "input": input}}})
			if err != nil {
				t.Fatal(err)
			}
			doc, _, err := expansion.ExpandTemplates(caller, expansion.Library(lib))
			if err != nil {
				t.Fatal(err)
			}
			def, err := wd.Load(doc)
			if err != nil {
				t.Fatal(err)
			}
			if !expansion.HasCapabilities(def) {
				return
			}
			rails := 0
			for _, c := range reg.List() {
				m := c.Manifest
				if m.KindOf() != "rail" {
					continue
				}
				out, _, err := expansion.Expand(doc, expansion.ResolverFor([]expansion.Connection{{Name: "main", Connector: m.ID}}, reg))
				if err != nil {
					continue // a rail that cannot do what the template does
				}
				rails++
				if _, err := wd.Load(out); err != nil {
					t.Errorf("%s: %v", m.ID, err)
				}
			}
			if rails == 0 {
				t.Error("expands on no rail")
			}
		})
	}
}
