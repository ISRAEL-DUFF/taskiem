package builder

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/israel-duff/taskiem/engine/capability"
	"github.com/israel-duff/taskiem/engine/connector"
	"github.com/israel-duff/taskiem/engine/expansion"
	"github.com/israel-duff/taskiem/engine/wd"
	"github.com/israel-duff/taskiem/engine/wdtest"
	"github.com/israel-duff/taskiem/templates"
)

// The dry run (spec 12.1 step 6) runs a draft through engine/wdtest: the
// real orchestrator over an in-memory history, every task step mocked, so
// nothing is sent anywhere. It runs a generated happy path (every step
// succeeds with an output sampled from its action's output schema, every
// approval approved, every form answered) and the cases the model proposed.

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
	return HappyPathWith(def, reg, nil)
}

// HappyPathWith is HappyPath with the templates a template step may name
// (nil: the library).
func HappyPathWith(def *wd.Definition, reg connector.Lookup, find expansion.Templates) wdtest.Case {
	if find == nil {
		find = expansion.Library(templates.Default())
	}
	c := wdtest.Case{
		Name:      "happy path (generated)",
		Mocks:     map[string]wdtest.Mocks{},
		Approvals: map[string]wdtest.ApprovalMock{},
		Signals:   map[string]wdtest.SignalMock{},
		Forms:     map[string]wdtest.FormMock{},
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
	if def.Trigger.Type == "email" {
		// A message as an email trigger delivers it (decision 0038).
		trig["body"] = map[string]any{
			"from": map[string]any{"address": "sender@example.com", "name": "A sender"}, "to": []any{}, "cc": []any{}, "reply_to": []any{},
			"subject": "A sample email", "message_id": "sample@example.com", "date": "2026-01-01T09:00:00Z",
			"text": "The text of a sample email.", "latest": "The text of a sample email.", "html": "",
			"attachments": []any{map[string]any{"id": "00000000-0000-7000-8000-000000000000", "name": "sample.pdf", "type": "application/pdf", "size": 1000, "sha256": strings.Repeat("0", 64)}},
			"skipped":     []any{},
		}
		trig["email"] = map[string]any{"address": "sample@in.taskiem.app", "received_at": "2026-01-01T09:00:00Z",
			"auth": map[string]any{"spf": "pass", "dkim": []any{"example.com"}, "dmarc": "", "aligned": true}}
	}
	if def.Trigger.Type == "invoice" {
		// An invoice paid in full, as engine/invoicing.PaidTrigger gives it.
		trig["event"] = "paid"
		trig["body"] = map[string]any{"event": "paid", "id": "00000000-0000-7000-8000-000000000000", "number": "INV-0001", "status": "paid",
			"amount_kobo": 2500000, "amount": "₦25,000", "paid_kobo": 2500000, "currency": "NGN",
			"customer": map[string]any{"name": "A customer", "phone": "+2348030000000", "email": "customer@example.com"},
			"paid_at":  "2026-01-01T09:00:00Z", "link": "https://forms.example/i/sample"}
		if ev, _ := def.Trigger.Config["event"].(string); ev == "payment" {
			// Every payment counted: the invoice with the part counted.
			body := trig["body"].(map[string]any)
			trig["event"], body["event"], body["due_kobo"] = "payment", "payment", 0
			body["payment"] = map[string]any{"id": "00000000-0000-7000-8000-000000000001", "amount_kobo": 2500000, "amount": "₦25,000", "how": "exact", "paid_at": "2026-01-01T09:00:00Z"}
		}
	}
	if def.Trigger.Type == "form" {
		// What a submission carries beside the answers (forms 8.2).
		trig["form"] = map[string]any{"submission_id": "00000000-0000-7000-8000-000000000000", "reference": "F-SAMPLE23", "submitted_at": "2026-01-01T09:00:00Z",
			"respondent": map[string]any{"kind": "verified", "phone": "+2348030000000"}, "channel": "direct", "consents": []any{}}
	}
	if def.Trigger.Type == "capability_event" {
		// A normalised payment event: its name and sample fields.
		if evs, ok := def.Trigger.Config["events"].([]any); ok && len(evs) > 0 {
			name, _ := evs[0].(string)
			trig["event"] = name
			if spec, ok := capability.GetEvent(name); ok {
				trig["fields"] = sample(rawAny(spec.Fields), nil, nil, 0)
			}
		}
	}
	if def.Trigger.Type == "connector_event" {
		if evs, ok := def.Trigger.Config["events"].([]any); ok && len(evs) > 0 {
			trig["event"] = evs[0]
		}
		// The trigger's own example delivery, where its manifest gives one.
		ref, _ := def.Trigger.Config["connector"].(string)
		name, _ := def.Trigger.Config["trigger"].(string)
		if conn, ok := reg.Get(ref); ok {
			if ex := conn.Manifest.Triggers[name].Example; ex != nil {
				if ex.Body != nil {
					trig["body"] = ex.Body
				}
				if ex.Item != nil {
					trig["item"] = ex.Item
				}
			}
		}
	}
	c.Trigger = trig
	// A template step's steps are mocked as the workflow's own, by their
	// expanded ids (decision 0047).
	if def.HasTemplates() {
		if exp, err := expansion.ExpandDefinition(def, find); err == nil {
			def = exp
		}
	}
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
			case "capability":
				// The capability's output, the same on every rail
				// (schemas/capabilities-v1.json): a template is checked
				// without choosing one.
				var out any = map[string]any{}
				switch st.Uses {
				case "await_settlement":
					out = map[string]any{"status": "settled"}
				case "await_reversal":
					out = map[string]any{"status": "none"}
				case "await_payment":
					out = map[string]any{"status": "paid", "amount_kobo": int64(100000)}
				default:
					if spec, ok := capability.Get(st.Uses); ok {
						if o := sample(rawAny(spec.Output), nil, nil, 0); o != nil {
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
			case "callback":
				c.Signals[st.ID] = wdtest.SignalMock{Payload: map[string]any{"body": map[string]any{}, "query": map[string]any{}, "received_at": "2026-01-01T09:00:00Z"}}
			case "form":
				answers := map[string]any{}
				if st.Form != nil {
					// Through JSON, so numbers are float64 as sample reads them.
					raw, _ := json.Marshal(st.Form.Schema)
					if a, ok := sample(rawAny(raw), types, nil, 0).(map[string]any); ok {
						answers = a
					}
				}
				c.Forms[st.ID] = wdtest.FormMock{Answers: answers}
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
		// account numbers, a month).
		if p, ok := sm["pattern"].(string); ok {
			if re, err := regexp.Compile(p); err == nil && !re.MatchString(s) {
				for _, c := range []string{"00000000000", "0000000000", "2348000000000", "000000", "0000", "2026-01", "sample_1", "Sample1"} {
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
	return DryRunWith(doc, reg, policies, proposed, nil)
}

// DryRunWith is DryRun with the templates a template step may name (nil:
// the library).
func DryRunWith(doc []byte, reg connector.Lookup, policies map[string]json.RawMessage, proposed []wdtest.Case, find expansion.Templates) (TestFile, []TestResult, error) {
	def, err := wd.Load(doc)
	if err != nil {
		return TestFile{}, nil, err
	}
	reg = ManifestOnly(reg)
	used := usedPolicies(def, policies)
	file := TestFile{Schema: wdtest.Schema, Policies: used}
	cases := append([]wdtest.Case{HappyPathWith(def, reg, find)}, proposed...)
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
		res := runCase(def, reg, used, find, c)
		results = append(results, TestResult{Name: c.Name, Source: source, Passed: res.Passed(), Status: res.Status, Failures: res.Failures})
		file.Cases = append(file.Cases, c)
	}
	return file, results, nil
}

// runCase runs one case, turning a panic in a malformed case into a
// failure: a proposal's tests are data from a model.
func runCase(def *wd.Definition, reg connector.Lookup, policies map[string]json.RawMessage, find expansion.Templates, c wdtest.Case) (res wdtest.Result) {
	defer func() {
		if v := recover(); v != nil {
			res = wdtest.Result{Case: c.Name, Status: "error", Failures: []string{fmt.Sprintf("the case could not run: %v", v)}}
		}
	}()
	return wdtest.RunCaseWith(def, reg, nil, policies, find, c)
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

// TryDraft runs a draft once with sample data, for the workflow builder's
// Test: the generated happy path (every service step answered with a
// sample of its output, every approval approved, every form answered), so
// nothing is sent anywhere. trigger, when given, is what starts it instead
// of the sample; kept are outputs the builder kept for steps, which answer
// those steps instead of the samples. The case is returned with the result
// so the builder can show what each step was given.
func TryDraft(doc []byte, reg connector.Lookup, policies map[string]json.RawMessage, find expansion.Templates, trigger any, kept map[string]any) (wdtest.Case, wdtest.Result, error) {
	def, err := wd.Load(doc)
	if err != nil {
		return wdtest.Case{}, wdtest.Result{}, err
	}
	reg = ManifestOnly(reg)
	c := HappyPathWith(def, reg, find)
	c.Name = "test with sample data"
	c.Expect = wdtest.Expect{}
	if trigger != nil {
		c.Trigger = trigger
	}
	for id, out := range kept {
		if _, ok := c.Mocks[id]; ok {
			c.Mocks[id] = wdtest.Mocks{{Output: out}}
		}
	}
	return c, runCase(def, reg, usedPolicies(def, policies), find, c), nil
}
