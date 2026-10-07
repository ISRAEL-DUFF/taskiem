package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"strings"

	"github.com/israel-duff/taskiem/engine/ai"
	"github.com/israel-duff/taskiem/engine/ai/builder"
	"github.com/israel-duff/taskiem/engine/ai/repair"
	"github.com/israel-duff/taskiem/engine/connector"
	"github.com/israel-duff/taskiem/engine/wd"
	"github.com/israel-duff/taskiem/engine/wdcheck"
	"github.com/israel-duff/taskiem/engine/wdtest"
)

// The repair suite (B2's evaluation, evals/repair): recorded failures of
// real workflow definitions with the class a person expects. The rules
// classify every one; failures the rules are unsure of, and classes that
// patch, go to the model through the real repair pipeline, whose patch
// must pass the publishing checks, change the definition, and, where the
// fixture records the failing case, pass it.

// RepairCase is one line of a repair suite.
type RepairCase struct {
	ID   string   `json:"id"`
	Tags []string `json:"tags,omitempty"`
	// Definition is a path (relative to the fixture file) to a wd/v1
	// document, or the document itself.
	Definition json.RawMessage `json:"definition"`
	Failure    repair.Failure  `json:"failure"`
	// Run is the redacted run the model sees (optional).
	Run    any `json:"run,omitempty"`
	Expect struct {
		Class repair.Class `json:"class"`
		// Certain: the rules should decide alone (no model call needed
		// to classify).
		Certain bool `json:"certain"`
	} `json:"expect"`
	// Regression, when set, is a wd-test/v1 case reproducing the
	// failure; a patch must pass it.
	Regression json.RawMessage `json:"regression,omitempty"`
	Source     string          `json:"source,omitempty"`

	doc []byte
}

func loadRepair(path string) ([]RepairCase, error) {
	var out []RepairCase
	seen := map[string]bool{}
	err := readJSONL(path, func(file string, _ int, raw []byte) error {
		var c RepairCase
		if err := decodeStrict(raw, &c); err != nil {
			return err
		}
		if c.ID == "" || seen[c.ID] {
			return fmt.Errorf("every case needs a unique id")
		}
		seen[c.ID] = true
		if !contains(classNames(), string(c.Expect.Class)) {
			return fmt.Errorf("%s: unknown class %q", c.ID, c.Expect.Class)
		}
		var ref string
		if json.Unmarshal(c.Definition, &ref) == nil {
			doc, err := os.ReadFile(filepath.Join(filepath.Dir(file), ref)) //nolint:gosec // a fixture beside the suite
			if err != nil {
				return fmt.Errorf("%s: %w", c.ID, err)
			}
			c.doc = doc
		} else {
			c.doc = c.Definition
		}
		var buf bytes.Buffer
		if err := json.Compact(&buf, c.doc); err != nil {
			return fmt.Errorf("%s: definition: %w", c.ID, err)
		}
		c.doc = buf.Bytes()
		if _, err := wd.Load(c.doc); err != nil {
			return fmt.Errorf("%s: %w", c.ID, err)
		}
		if c.Source == "" {
			c.Source = "synthetic"
		}
		out = append(out, c)
		return nil
	})
	return out, err
}

func classNames() []string {
	out := make([]string, len(repair.Classes))
	for i, c := range repair.Classes {
		out[i] = string(c)
	}
	return out
}

// RepairResult is one repair case's outcome.
type RepairResult struct {
	ID           string       `json:"id"`
	Tags         []string     `json:"tags,omitempty"`
	Status       string       `json:"status"`
	Error        string       `json:"error,omitempty"`
	Expected     repair.Class `json:"expected"`
	RulesClass   repair.Class `json:"rules_class"`
	RulesCertain bool         `json:"rules_certain"`
	Class        repair.Class `json:"class"`
	ClassifiedBy string       `json:"classified_by"`
	ClassCorrect bool         `json:"class_correct"`
	// CertaintyCorrect: the rules were sure exactly when the fixture says
	// they should be.
	CertaintyCorrect bool `json:"certainty_correct"`
	ModelCalled      bool `json:"model_called"`
	// For classes that patch: the patch passes the publishing checks,
	// changes the definition, and passes the recorded failing case.
	PatchExpected bool     `json:"patch_expected"`
	PatchValid    bool     `json:"patch_valid"`
	PatchPassed   bool     `json:"patch_passed"`
	Attempts      int      `json:"attempts"`
	Model         string   `json:"model,omitempty"`
	Usage         ai.Usage `json:"usage"`
	CostUSD       *float64 `json:"cost_usd,omitempty"`
}

// RepairSummary aggregates a repair run.
type RepairSummary struct {
	Cases            int      `json:"cases"`
	Errors           int      `json:"errors"`
	ClassAccuracy    float64  `json:"class_accuracy"`
	RulesAccuracy    float64  `json:"rules_class_accuracy"`
	CertaintyCorrect float64  `json:"certainty_accuracy"`
	ModelCalls       int      `json:"model_calls"`
	Patches          int      `json:"patch_cases"`
	PatchValid       float64  `json:"patch_valid_rate"`
	PatchPassed      float64  `json:"patch_pass_rate"`
	Usage            ai.Usage `json:"usage"`
	CostUSD          *float64 `json:"cost_usd"`
}

// RepairReport is a repair evaluation.
type RepairReport struct {
	Kind     string           `json:"kind"` // repair
	Suite    string           `json:"suite"`
	Provider string           `json:"provider"`
	Model    string           `json:"model"`
	Summary  RepairSummary    `json:"summary"`
	ByClass  map[string]Group `json:"by_class"`
	Results  []RepairResult   `json:"results"`
}

func evaluateRepairs(ctx context.Context, prov ai.Provider, reg *connector.Registry, cases []RepairCase, prices map[string]Price) []RepairResult {
	out := make([]RepairResult, 0, len(cases))
	for _, c := range cases {
		out = append(out, runRepair(ctx, prov, reg, c, prices))
	}
	return out
}

func runRepair(ctx context.Context, prov ai.Provider, reg *connector.Registry, c RepairCase, prices map[string]Price) RepairResult {
	r := RepairResult{ID: c.ID, Tags: c.Tags, Status: statusOK, Expected: c.Expect.Class}
	def, _ := wd.Load(c.doc)
	cl := repair.Classify(c.Failure, def)
	r.RulesClass, r.RulesCertain = cl.Class, cl.Certain
	r.CertaintyCorrect = cl.Certain == c.Expect.Certain
	r.PatchExpected = c.Expect.Class.Patches()
	var regression *repairTest
	if len(c.Regression) > 0 {
		regression = &repairTest{raw: c.Regression}
	}
	rep := &repair.Repairer{
		Provider:   prov,
		Connectors: reg,
		Validate: func(doc []byte) []builder.Problem {
			var out []builder.Problem
			for _, p := range wdcheck.Check(doc, reg) {
				out = append(out, builder.Problem{Path: p.Path, Message: p.Message})
			}
			return out
		},
	}
	req := repair.Request{Failure: c.Failure, Classification: cl, Definition: json.RawMessage(c.doc), Run: c.Run,
		Check: func(doc []byte, test *wdtest.Case) repair.Check {
			return repairCheck(c.doc, doc, regression, test, reg)
		}}
	res, err := rep.Repair(ctx, req)
	if err != nil && !errors.Is(err, repair.ErrNoModel) {
		r.Status, r.Error = statusError, err.Error()
		return r
	}
	if res == nil {
		r.Status, r.Error = statusError, "no result"
		return r
	}
	r.Class, r.ClassifiedBy, r.Attempts = res.Class, res.ClassifiedBy, res.Attempts
	r.ModelCalled = res.Attempts > 0
	r.ClassCorrect = res.Class == c.Expect.Class
	r.Model, r.Usage = res.Model, res.Usage
	r.CostUSD = cost(prices, res.Model, res.Usage)
	if r.PatchExpected && len(res.Definition) > 0 {
		r.PatchValid = len(wdcheck.Check(res.Definition, reg)) == 0 && !sameDoc(c.doc, res.Definition)
		r.PatchPassed = r.PatchValid && res.Passed
	}
	return r
}

// repairTest is a fixture's recorded failing case.
type repairTest struct{ raw json.RawMessage }

// repairCheck stands in for the repair service's shadow run: the patch
// must load, change something, and pass the recorded failing case and the
// model's own test, with every step mocked.
func repairCheck(orig, doc []byte, regression *repairTest, model *wdtest.Case, reg connector.Lookup) repair.Check {
	def, err := wd.Load(doc)
	if err != nil {
		return repair.Check{Feedback: []string{err.Error()}}
	}
	if sameDoc(orig, doc) {
		return repair.Check{Feedback: []string{"the patch changes nothing"}}
	}
	var cases []wdtest.Case
	if regression != nil {
		var c wdtest.Case
		if err := json.Unmarshal(regression.raw, &c); err != nil {
			return repair.Check{Feedback: []string{"the fixture's regression case does not parse: " + err.Error()}}
		}
		if c.Name == "" {
			c.Name = "recorded failure"
		}
		cases = append(cases, c)
	}
	if model != nil {
		cases = append(cases, *model)
	}
	for _, c := range cases {
		if res := wdtest.RunCase(def, builder.ManifestOnly(reg), nil, c); !res.Passed() {
			return repair.Check{Feedback: append([]string{"case " + c.Name + " fails on the patch"}, res.Failures...)}
		}
	}
	return repair.Check{Passed: true}
}

func sameDoc(a, b []byte) bool {
	var x, y any
	if json.Unmarshal(a, &x) != nil || json.Unmarshal(b, &y) != nil {
		return false
	}
	ra, _ := json.Marshal(x)
	rb, _ := json.Marshal(y)
	return bytes.Equal(ra, rb)
}

func summariseRepairs(results []RepairResult) RepairSummary {
	s := RepairSummary{Cases: len(results)}
	var correct, rules, certain, valid, passed, scored int
	total, known := 0.0, true
	for _, r := range results {
		s.Usage = s.Usage.Add(r.Usage)
		if r.CostUSD != nil {
			total += *r.CostUSD
		} else if r.Usage.Total() > 0 {
			known = false
		}
		if r.Status != statusOK {
			s.Errors++
			continue
		}
		scored++
		if r.ClassCorrect {
			correct++
		}
		if r.RulesClass == r.Expected {
			rules++
		}
		if r.CertaintyCorrect {
			certain++
		}
		if r.ModelCalled {
			s.ModelCalls++
		}
		if r.PatchExpected {
			s.Patches++
			if r.PatchValid {
				valid++
			}
			if r.PatchPassed {
				passed++
			}
		}
	}
	s.ClassAccuracy, s.RulesAccuracy, s.CertaintyCorrect = rate(correct, scored), rate(rules, scored), rate(certain, scored)
	s.PatchValid, s.PatchPassed = rate(valid, s.Patches), rate(passed, s.Patches)
	if known && s.Usage.Total() > 0 {
		c := math.Round(total*1e4) / 1e4
		s.CostUSD = &c
	}
	return s
}

func buildRepairReport(suite, provider, model string, results []RepairResult) RepairReport {
	rep := RepairReport{Kind: "repair", Suite: suite, Provider: provider, Model: model, Summary: summariseRepairs(results), ByClass: map[string]Group{}, Results: results}
	by := map[string][]RepairResult{}
	for _, r := range results {
		by[string(r.Expected)] = append(by[string(r.Expected)], r)
	}
	for k, rs := range by {
		s := summariseRepairs(rs)
		rep.ByClass[k] = Group{Cases: s.Cases, Gate: s.ClassAccuracy, Requirements: s.PatchValid, Strict: s.PatchPassed}
	}
	return rep
}

func printRepairReport(w io.Writer, rep RepairReport) {
	s := rep.Summary
	fmt.Fprintf(w, "repair %s %s, %d cases: class accuracy %s (rules alone %s, certainty %s), %d model calls, patches valid %s and passing %s of %d; %d errors; %d tokens, cost %s\n",
		rep.Provider, rep.Model, s.Cases, pct(s.ClassAccuracy), pct(s.RulesAccuracy), pct(s.CertaintyCorrect), s.ModelCalls, pct(s.PatchValid), pct(s.PatchPassed),
		s.Patches, s.Errors, s.Usage.Total(), money(s.CostUSD))
}

func repairMarkdown(rep RepairReport) string {
	var b strings.Builder
	s := rep.Summary
	fmt.Fprintf(&b, "# AI repair evaluation\n\n`%s` on `%s`, %d cases, suite `%s`.\n\n| Measure | Value |\n| --- | --- |\n", rep.Model, rep.Provider, s.Cases, rep.Suite)
	fmt.Fprintf(&b, "| Class accuracy | **%s** |\n| Rules alone | %s |\n| Rules sure exactly when expected | %s |\n| Model calls | %d |\n| Patches valid | %s of %d |\n| Patches passing their checks | %s |\n| Errors | %d |\n| Tokens | %d |\n| Cost | %s |\n",
		pct(s.ClassAccuracy), pct(s.RulesAccuracy), pct(s.CertaintyCorrect), s.ModelCalls, pct(s.PatchValid), s.Patches, pct(s.PatchPassed), s.Errors, s.Usage.Total(), money(s.CostUSD))
	b.WriteString("\n## By expected class\n\n| Class | Cases | Class accuracy | Patch valid | Patch passing |\n| --- | --- | --- | --- | --- |\n")
	for _, k := range sortedKeys(rep.ByClass) {
		g := rep.ByClass[k]
		fmt.Fprintf(&b, "| %s | %d | %s | %s | %s |\n", k, g.Cases, pct(g.Gate), pct(g.Requirements), pct(g.Strict))
	}
	b.WriteString("\n## Cases\n\n| Case | Expected | Rules | Final | By | Patch |\n| --- | --- | --- | --- | --- | --- |\n")
	for _, r := range rep.Results {
		patch := "—"
		if r.PatchExpected {
			patch = fmt.Sprintf("valid %s, passing %s", yes(r.PatchValid), yes(r.PatchPassed))
		}
		fmt.Fprintf(&b, "| %s | %s | %s (%s) | %s | %s | %s |\n", r.ID, r.Expected, r.RulesClass, map[bool]string{true: "sure", false: "unsure"}[r.RulesCertain],
			r.Class, r.ClassifiedBy, patch)
	}
	return b.String()
}

// compareRepair: class accuracy, rules accuracy, certainty and patch
// rates may not drop by more than tol; errors may not grow.
func compareRepair(base, cur RepairReport, tol float64) []Regression {
	in := map[string]bool{}
	for _, r := range base.Results {
		in[r.ID] = true
	}
	var curRes, baseRes []RepairResult
	common := map[string]bool{}
	for _, r := range cur.Results {
		if in[r.ID] {
			curRes = append(curRes, r)
			common[r.ID] = true
		}
	}
	for _, r := range base.Results {
		if common[r.ID] {
			baseRes = append(baseRes, r)
		}
	}
	b, c := summariseRepairs(baseRes), summariseRepairs(curRes)
	var regs []Regression
	check := func(name string, bv, cv float64) {
		if cv < bv-tol-1e-9 {
			regs = append(regs, Regression{name, bv, cv})
		}
	}
	check("class_accuracy", b.ClassAccuracy, c.ClassAccuracy)
	check("rules_class_accuracy", b.RulesAccuracy, c.RulesAccuracy)
	check("certainty_accuracy", b.CertaintyCorrect, c.CertaintyCorrect)
	check("patch_valid_rate", b.PatchValid, c.PatchValid)
	check("patch_pass_rate", b.PatchPassed, c.PatchPassed)
	if c.Errors > b.Errors {
		regs = append(regs, Regression{"errors", float64(b.Errors), float64(c.Errors)})
	}
	return regs
}
