package builder

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/israel-duff/taskiem/engine/connector"
	"github.com/israel-duff/taskiem/engine/wd"
	"github.com/israel-duff/taskiem/engine/wdtest"
)

// The dry run (spec 12.1 step 6) runs a draft through engine/wdtest: the
// real orchestrator over an in-memory history, every task step mocked, so
// nothing is sent anywhere. It runs a generated happy path (every step
// succeeds with an output sampled from its action's output schema, every
// approval approved) and the cases the model proposed.

// TestResult is one dry-run case's outcome.
type TestResult struct {
	Name     string   `json:"name"`
	Source   string   `json:"source"` // generated | model
	Passed   bool     `json:"passed"`
	Status   string   `json:"status"`
	Failures []string `json:"failures,omitempty"`
}

// TestFile is the tests attached to a proposal, as a wd-test/v1 file
// (without its workflow path, which saving fills in).
type TestFile struct {
	Schema   string                     `json:"schema"`
	Policies map[string]json.RawMessage `json:"policies,omitempty"`
	Cases    []wdtest.Case              `json:"cases"`
}

// HappyPath generates the case where everything succeeds.
func HappyPath(def *wd.Definition, reg connector.Lookup) wdtest.Case {
	c := wdtest.Case{
		Name:      "happy path (generated)",
		Mocks:     map[string]wdtest.Mocks{},
		Approvals: map[string]wdtest.ApprovalMock{},
		Signals:   map[string]wdtest.SignalMock{},
		Expect:    wdtest.Expect{Status: "completed"},
	}
	types := map[string]any{}
	for k, v := range def.RawTypes {
		var t any
		_ = json.Unmarshal(v, &t)
		types[k] = t
	}
	trig := map[string]any{"body": map[string]any{}}
	if def.RawInputs != nil {
		var s any
		_ = json.Unmarshal(def.RawInputs.Schema, &s)
		if body := sample(s, types, nil, 0); body != nil {
			trig["body"] = body
		}
	}
	if def.Trigger.Type == "connector_event" {
		if evs, ok := def.Trigger.Config["events"].([]any); ok && len(evs) > 0 {
			trig["event"] = evs[0]
		}
	}
	c.Trigger = trig
	// Tenant variables the definition reads get a placeholder value: the
	// dry run checks the workflow's shape, not the tenant's settings.
	if raw, err := json.Marshal(def.Steps); err == nil {
		for _, m := range envRef.FindAllStringSubmatch(string(raw), -1) {
			if c.Env == nil {
				c.Env = map[string]any{}
			}
			c.Env[m[1]] = "sample-" + m[1]
		}
	}
	var walk func(steps []*wd.Step)
	walk = func(steps []*wd.Step) {
		for _, st := range steps {
			switch st.Type {
			case "connector":
				var out any = map[string]any{}
				if con, ok := reg.Get(st.Connector); ok {
					if a, ok := con.Manifest.Actions[st.Action]; ok {
						if o := sample(rawAny(a.Output), nil, con.Manifest, 0); o != nil {
							out = o
						}
					}
				}
				c.Mocks[st.ID] = wdtest.Mocks{{Output: out}}
			case "http":
				c.Mocks[st.ID] = wdtest.Mocks{{Output: map[string]any{"status": 200, "body": map[string]any{}}}}
			case "code", "container":
				c.Mocks[st.ID] = wdtest.Mocks{{Output: map[string]any{}}}
			case "approval":
				c.Approvals[st.ID] = wdtest.ApprovalMock{Decision: "approved", By: "checker"}
			case "signal":
				c.Signals[st.ID] = wdtest.SignalMock{Payload: map[string]any{}}
			}
			for _, sub := range st.Children() {
				walk(sub)
			}
		}
	}
	walk(def.Steps)
	return c
}

var envRef = regexp.MustCompile(`\benv\.([A-Za-z_][A-Za-z0-9_]*)`)

func rawAny(raw json.RawMessage) any {
	var v any
	_ = json.Unmarshal(raw, &v)
	return v
}

// sample builds a value satisfying a JSON Schema, as far as a sample can:
// enums take their first value, numbers their minimum, every declared
// property is filled. $ref resolves to the WD's types or a manifest's
// "#/actions/<name>/output|input".
func sample(s any, types map[string]any, m *connector.Manifest, depth int) any {
	sm, ok := s.(map[string]any)
	if !ok || depth > 8 {
		return nil
	}
	if ref, ok := sm["$ref"].(string); ok {
		if name, ok := strings.CutPrefix(ref, "#/types/"); ok {
			return sample(types[name], types, m, depth+1)
		}
		if rest, ok := strings.CutPrefix(ref, "#/actions/"); ok && m != nil {
			action, part, _ := strings.Cut(rest, "/")
			if a, ok := m.Actions[action]; ok {
				raw := a.Output
				if part == "input" {
					raw = a.Input
				}
				return sample(rawAny(raw), types, m, depth+1)
			}
		}
		return nil
	}
	if v, ok := sm["const"]; ok {
		return v
	}
	if v, ok := sm["default"]; ok {
		return v
	}
	if e, ok := sm["enum"].([]any); ok && len(e) > 0 {
		return e[0]
	}
	for _, k := range []string{"oneOf", "anyOf"} {
		if alts, ok := sm[k].([]any); ok && len(alts) > 0 {
			return sample(alts[0], types, m, depth+1)
		}
	}
	typ := sm["type"]
	if list, ok := typ.([]any); ok && len(list) > 0 {
		typ = list[0]
		for _, t := range list {
			if t != "null" {
				typ = t
				break
			}
		}
	}
	if typ == nil {
		if _, ok := sm["properties"]; ok {
			typ = "object"
		}
	}
	switch typ {
	case "object":
		out := map[string]any{}
		props, _ := sm["properties"].(map[string]any)
		keys := make([]string, 0, len(props))
		for k := range props {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			if v := sample(props[k], types, m, depth+1); v != nil {
				out[k] = v
			}
		}
		return out
	case "array":
		if v := sample(sm["items"], types, m, depth+1); v != nil {
			return []any{v}
		}
		return []any{}
	case "integer", "number":
		n := 1000.0
		if min, ok := sm["minimum"].(float64); ok && min > n {
			n = min
		}
		if mx, ok := sm["maximum"].(float64); ok && mx < n {
			n = mx
		}
		return n
	case "boolean":
		return true
	case "string":
		switch sm["format"] {
		case "date-time":
			return "2026-01-05T09:00:00Z"
		case "date":
			return "2026-01-05"
		case "email":
			return "someone@example.test"
		case "uri", "url":
			return "https://example.test/"
		}
		s := "sample"
		if n, ok := sm["minLength"].(float64); ok && int(n) > len(s) {
			s += strings.Repeat("x", int(n)-len(s))
		}
		// A pattern the plain sample misses: try the shapes patterns most
		// often ask for (digits of common lengths: phone numbers, BVNs,
		// account numbers).
		if p, ok := sm["pattern"].(string); ok {
			if re, err := regexp.Compile(p); err == nil && !re.MatchString(s) {
				for _, c := range []string{"00000000000", "0000000000", "2348000000000", "000000", "0000", "sample_1", "Sample1"} {
					if re.MatchString(c) {
						return c
					}
				}
			}
		}
		return s
	case "null":
		return nil
	}
	return nil
}

// DryRun runs the generated happy path and the proposed cases.
func DryRun(doc []byte, reg connector.Lookup, policies map[string]json.RawMessage, proposed []wdtest.Case) (TestFile, []TestResult, error) {
	def, err := wd.Load(doc)
	if err != nil {
		return TestFile{}, nil, err
	}
	reg = ManifestOnly(reg)
	used := usedPolicies(def, policies)
	file := TestFile{Schema: wdtest.Schema, Policies: used}
	cases := append([]wdtest.Case{HappyPath(def, reg)}, proposed...)
	var results []TestResult
	names := map[string]bool{}
	for i, c := range cases {
		source := "model"
		if i == 0 {
			source = "generated"
		}
		if c.Name == "" || names[c.Name] {
			c.Name = fmt.Sprintf("case %d", i+1)
		}
		names[c.Name] = true
		res := runCase(def, reg, used, c)
		results = append(results, TestResult{Name: c.Name, Source: source, Passed: res.Passed(), Status: res.Status, Failures: res.Failures})
		file.Cases = append(file.Cases, c)
	}
	return file, results, nil
}

// runCase runs one case, turning a panic in a malformed case into a
// failure: a proposal's tests are data from a model.
func runCase(def *wd.Definition, reg connector.Lookup, policies map[string]json.RawMessage, c wdtest.Case) (res wdtest.Result) {
	defer func() {
		if v := recover(); v != nil {
			res = wdtest.Result{Case: c.Name, Status: "error", Failures: []string{fmt.Sprintf("the case could not run: %v", v)}}
		}
	}()
	return wdtest.RunCaseWithPolicies(def, reg, nil, policies, c)
}

// usedPolicies returns the policies def's approval steps name.
func usedPolicies(def *wd.Definition, all map[string]json.RawMessage) map[string]json.RawMessage {
	out := map[string]json.RawMessage{}
	var walk func(steps []*wd.Step)
	walk = func(steps []*wd.Step) {
		for _, st := range steps {
			if st.Approval != nil && st.Approval.Policy != "" {
				if doc, ok := all[st.Approval.Policy]; ok {
					out[st.Approval.Policy] = doc
				}
			}
			for _, sub := range st.Children() {
				walk(sub)
			}
		}
	}
	walk(def.Steps)
	if len(out) == 0 {
		return nil
	}
	return out
}

// parseCase reads a model-proposed case.
func parseCase(name, raw string) (wdtest.Case, error) {
	var c wdtest.Case
	if err := json.Unmarshal([]byte(raw), &c); err != nil {
		return c, err
	}
	c.Name = name
	if c.Expect.Status == "" && len(c.Expect.Steps) == 0 && len(c.Expect.Outputs) == 0 {
		c.Expect.Status = "completed"
	}
	return c, nil
}

// Sample builds a value satisfying a JSON Schema (decoded), as the
// generated happy path does: for fixtures and the evaluation's fake model.
func Sample(schema any) any { return sample(schema, nil, nil, 0) }
