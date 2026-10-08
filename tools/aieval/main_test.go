package main

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/israel-duff/taskiem/connectors/builtin"
	"github.com/israel-duff/taskiem/engine/ai"
	"github.com/israel-duff/taskiem/engine/ai/builder"
	"github.com/israel-duff/taskiem/engine/connector"
	"github.com/israel-duff/taskiem/engine/wd"
)

func registry(t *testing.T) *connector.Registry {
	t.Helper()
	reg := connector.NewRegistry()
	if err := builtin.Register(reg, builtin.Options{}); err != nil {
		t.Fatal(err)
	}
	return reg
}

// The suite (spec 12.4): at least 200 requests, every one well formed,
// spread over the operations and step shapes the builder must handle,
// naming only connectors that exist, and marked as synthetic seeds until
// people review them (AI2).
func TestSuiteSizeAndSchema(t *testing.T) {
	cases, err := load("../../evals/builder")
	if err != nil {
		t.Fatal(err)
	}
	if len(cases) < 200 {
		t.Fatalf("the suite has %d requests, want at least 200", len(cases))
	}
	reg := registry(t)
	groups, diffs, steps, props := map[string]int{}, map[string]int{}, map[string]int{}, map[string]int{}
	requests := map[string]string{}
	for _, c := range cases {
		groups[c.Tags[0]]++
		diffs[c.Difficulty]++
		for _, s := range c.Expect.Steps {
			steps[s]++
		}
		for _, p := range c.Expect.Properties {
			props[p]++
		}
		for _, ref := range c.Expect.Connectors {
			if _, ok := reg.Get(ref); !ok {
				t.Errorf("%s: connector %s is not in the catalogue", c.ID, ref)
			}
		}
		key := strings.ToLower(strings.Join(strings.Fields(c.Request), " "))
		if other, dup := requests[key]; dup {
			t.Errorf("%s repeats %s", c.ID, other)
		}
		requests[key] = c.ID
	}
	for _, g := range []string{"payouts", "collections", "reconciliation", "kyc", "notifications", "approvals", "reporting", "messaging", "multistep", "sme"} {
		if groups[g] < 15 {
			t.Errorf("group %s has %d cases, want at least 15", g, groups[g])
		}
	}
	for _, d := range difficulties {
		if diffs[d] < 30 {
			t.Errorf("%d %s cases, want at least 30", diffs[d], d)
		}
	}
	for _, s := range []string{"approval", "foreach", "branch", "parallel", "wait", "signal", "code", "http"} {
		if steps[s] < 3 {
			t.Errorf("only %d cases need a %s step", steps[s], s)
		}
	}
	for _, p := range ruleNames() {
		if p != "no_inline_secrets" && props[p] < 2 {
			t.Errorf("only %d cases require %s", props[p], p)
		}
	}
	files, _ := suiteFiles("../../evals/builder")
	for _, f := range files {
		raw, _ := os.ReadFile(f) //nolint:gosec // test fixture
		if !strings.Contains(strings.SplitN(string(raw), "\n", 2)[0], "SYNTHETIC SEEDS PENDING REVIEW BY PEOPLE") {
			t.Errorf("%s: the first line must mark the requests as synthetic seeds pending review", f)
		}
	}
}

func mustLoad(t *testing.T, doc string) *wd.Definition {
	t.Helper()
	def, err := wd.Load([]byte(doc))
	if err != nil {
		t.Fatal(err)
	}
	return def
}

const goodPayout = `{"schema":"wd/v1","id":"wf_good","version":1,"name":"Good","trigger":{"type":"webhook","config":{"path":"/p","auth":"hmac","dedup":"=trigger.body.id"}},
 "inputs":{"schema":{"type":"object","properties":{"rows":{"type":"array"}}}},
 "steps":[
  {"id":"balance","type":"connector","connector":"paystack@1","action":"check_balance","input":{}},
  {"id":"approve","type":"approval","needs":["balance"],"config":{"role":"finance","count":1,"timeout":"24h"}},
  {"id":"pay_all","type":"foreach","needs":["approve"],"when":"=steps.approve.output.decision == 'approved'","config":{"items":"=trigger.body.rows","max_concurrency":5,"steps":[
    {"id":"pay","type":"connector","connector":"paystack@1","action":"transfer","input":{"amount":"=item.amount","recipient":"=item.r"},"effect":{"idempotency_seed":"=item.id"},
     "on_error":{"steps":[{"id":"tell","type":"connector","connector":"termii@1","action":"send_sms","input":{"to":"=env.ops_phone","sms":"payout failed"}}]}}]}}]}`

const badPayout = `{"schema":"wd/v1","id":"wf_bad","version":1,"name":"Bad","trigger":{"type":"webhook","config":{"path":"/p","auth":"none"}},
 "steps":[
  {"id":"approve","type":"approval","config":{"role":"finance","count":1}},
  {"id":"pay_all","type":"foreach","config":{"items":"=trigger.body.rows","steps":[
    {"id":"pay","type":"connector","connector":"paystack@1","action":"transfer","input":{"amount":"=item.amount","recipient":"=item.r"}}]}},
  {"id":"tell","type":"connector","needs":["pay_all"],"connector":"termii@1","action":"send_sms","input":{"to":"2348031234567","sms":"Bearer abcdefghijklmnopqrstuvwxyz0123"}}]}`

// Each rule holds on a hand-written good definition and fails on a bad one.
func TestRules(t *testing.T) {
	reg := registry(t)
	good, bad := mustLoad(t, goodPayout), mustLoad(t, badPayout)
	for _, name := range ruleNames() {
		if ok, why := rules[name](good, reg); !ok {
			t.Errorf("%s fails the good definition: %s", name, why)
		}
		if ok, _ := rules[name](bad, reg); ok {
			t.Errorf("%s passes the bad definition", name)
		}
	}
}

// grade on a known-good answer (the oracle) and a null answer: the grader
// passes the first and fails the second.
func TestGradeOracleAndNull(t *testing.T) {
	reg := registry(t)
	c := Case{ID: "x", Request: "pay rows after approval", Tags: []string{"payouts"}, Difficulty: "hard",
		Expect: Expect{Connectors: []string{"paystack@1"}, Steps: []string{"approval", "foreach"}, Trigger: "webhook",
			Properties: []string{"approval_before_payment", "idempotency", "webhook_auth", "bounded_concurrency", "reads_before_paying", "dedup"}}}
	answer := func(doc string) ai.Response {
		env, _ := json.Marshal(map[string]any{"summary": "s", "assumptions": []string{}, "workflow": doc, "tests": []any{}, "template": "", "template_params": ""})
		return ai.Response{Text: string(env)}
	}
	oracle := runCase(context.Background(), &ai.Fake{Script: []ai.Response{answer(goodPayout)}}, reg, c, 0, evalOptions{})
	if !oracle.Strict || !oracle.Gate || !oracle.Requirements || oracle.Status != statusOK {
		t.Fatalf("oracle: %+v", oracle)
	}
	null := runCase(context.Background(), &ai.Fake{Script: []ai.Response{answer(`{"schema":"wd/v1","id":"wf_n","version":1,"name":"n","trigger":{"type":"manual"},"steps":[{"id":"t","type":"transform","config":{"output":{}}}]}`)}}, reg, c, 0, evalOptions{})
	if null.Requirements || null.Strict || null.ConnectorRecall != 0 || null.TriggerOK {
		t.Fatalf("null: %+v", null)
	}
	// An unparseable answer is a draft that never became valid, not an
	// error of the harness.
	junk := runCase(context.Background(), &ai.Fake{Script: []ai.Response{{Text: "I don't know"}}}, reg, c, 0, evalOptions{})
	if junk.Status != statusOK || junk.Valid || junk.Gate {
		t.Fatalf("junk: %+v", junk)
	}
	// A provider failure is not scored; neither is an answer from another
	// model than the one asked for.
	broken := runCase(context.Background(), &ai.Fake{Respond: func(ai.Request) (*ai.Response, error) { return nil, errors.New("boom") }}, reg, c, 0, evalOptions{})
	if broken.Status != statusError || broken.scored() {
		t.Fatalf("broken: %+v", broken)
	}
	other := runCase(context.Background(), &ai.Fake{Script: []ai.Response{answer(goodPayout)}, Models: "claude-sonnet-5-5"}, reg, c, 0, evalOptions{WantModel: "claude-opus-5-5"})
	if other.Status != statusModelMismatch {
		t.Fatalf("mismatch: %+v", other)
	}
	s := summarise([]Result{oracle, null, broken, other})
	if s.Scored != 2 || s.Errors != 1 || s.Mismatch != 1 || s.Strict != 0.5 {
		t.Fatalf("summary: %+v", s)
	}
}

// The CI gate: the offline pipeline matches the committed baseline.
func TestBaselinesHold(t *testing.T) {
	if testing.Short() {
		t.Skip("runs the whole suite")
	}
	for _, args := range [][]string{
		{"-suite", "../../evals/builder", "-compare", "../../evals/builder/baseline.json", "-tolerance", "0"},
		{"-mode", "repair", "-suite", "../../evals/repair", "-compare", "../../evals/repair/baseline.json", "-tolerance", "0"},
	} {
		var out strings.Builder
		if err := run(args, &out); err != nil {
			t.Fatalf("%v: %v\n%s", args, err, out.String())
		}
		if !strings.Contains(out.String(), "no regression") {
			t.Errorf("%s", out.String())
		}
	}
}

// -compare fails on a regression beyond the tolerance and names the cases
// that stopped passing; within the tolerance it passes.
func TestCompareDetectsRegression(t *testing.T) {
	dir := t.TempDir()
	base := filepath.Join(dir, "base.json")
	var out strings.Builder
	if err := run([]string{"-suite", "../../evals/builder", "-tags", "sme", "-baseline-out", base}, &out); err != nil {
		t.Fatal(err)
	}
	var rep Report
	raw, _ := os.ReadFile(base) //nolint:gosec // test output
	if err := json.Unmarshal(raw, &rep); err != nil {
		t.Fatal(err)
	}
	for _, r := range rep.Results {
		if r.Seconds != 0 {
			t.Fatal("a baseline carries no timings")
		}
	}
	// The same run against itself: no regression.
	if regs, lost := compareBuilder(rep, rep, 0); len(regs)+len(lost) > 0 {
		t.Fatalf("self comparison: %v %v", regs, lost)
	}
	// Two cases stop passing the gate.
	cur := rep
	cur.Results = append([]Result(nil), rep.Results...)
	flipped := 0
	for i := range cur.Results {
		if cur.Results[i].Gate && flipped < 2 {
			cur.Results[i].Gate, cur.Results[i].ValidFirstTry, cur.Results[i].Strict = false, false, false
			flipped++
		}
	}
	regs, lost := compareBuilder(rep, cur, 0.02)
	if len(regs) == 0 || len(lost) != 2 {
		t.Fatalf("regressions %v lost %v", regs, lost)
	}
	if regs2, _ := compareBuilder(rep, cur, 0.5); len(regs2) != 0 {
		t.Fatalf("within tolerance: %v", regs2)
	}
	// A new policy violation is a regression too.
	cur2 := rep
	cur2.Results = append([]Result(nil), rep.Results...)
	cur2.Results[0].PolicyViolations = 1
	if regs, _ := compareBuilder(rep, cur2, 0); len(regs) == 0 || regs[len(regs)-1].Measure != "policy_violation_rate" {
		t.Fatalf("policy: %v", regs)
	}
	// Through the command: a doctored baseline that is better than the
	// pipeline fails the run.
	better := rep
	better.Results = append([]Result(nil), rep.Results...)
	for i := range better.Results {
		better.Results[i].Requirements, better.Results[i].Strict = true, true
		for k := range better.Results[i].Properties {
			better.Results[i].Properties[k] = true
		}
	}
	doctored := filepath.Join(dir, "better.json")
	if err := writeJSON(doctored, better); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	err := run([]string{"-suite", "../../evals/builder", "-tags", "sme", "-compare", doctored}, &out)
	if !errors.Is(err, errRegression) || !strings.Contains(out.String(), "REGRESSION") {
		t.Fatalf("err %v\n%s", err, out.String())
	}
}

func TestReportsAndGate(t *testing.T) {
	dir := t.TempDir()
	js, md := filepath.Join(dir, "r.json"), filepath.Join(dir, "r.md")
	var out strings.Builder
	if err := run([]string{"-suite", "../../evals/builder", "-tags", "dogfood", "-out", js, "-markdown", md, "-reps", "2", "-parallel", "2"}, &out); err != nil {
		t.Fatal(err)
	}
	var rep Report
	raw, _ := os.ReadFile(js) //nolint:gosec // test output
	if err := json.Unmarshal(raw, &rep); err != nil {
		t.Fatal(err)
	}
	if rep.Summary.Cases != 4 || rep.Summary.Attempts != 8 || rep.Provider != "fake" || rep.ByTag["dogfood"].Cases != 4 || len(rep.ByDiff) == 0 {
		t.Fatalf("report: %+v", rep.Summary)
	}
	if rep.Summary.NoiseFloor != math.Round(1e4/math.Sqrt(8))/1e4 {
		t.Errorf("noise floor %v", rep.Summary.NoiseFloor)
	}
	mdText, _ := os.ReadFile(md) //nolint:gosec // test output
	for _, want := range []string{"G3 gate", "## By tag", "## By difficulty", "## By property", "offline heuristic"} {
		if !strings.Contains(string(mdText), want) {
			t.Errorf("markdown lacks %q", want)
		}
	}
	err := run([]string{"-suite", "../../evals/builder", "-limit", "1", "-min-gate", "1.01"}, &out)
	if err == nil || !strings.Contains(err.Error(), "below") {
		t.Fatalf("gate: %v", err)
	}
}

func TestJudge(t *testing.T) {
	dir := t.TempDir()
	js := filepath.Join(dir, "r.json")
	var out strings.Builder
	if err := run([]string{"-suite", "../../evals/builder", "-tags", "dogfood", "-judge", "-judge-provider", "fake", "-out", js}, &out); err != nil {
		t.Fatal(err)
	}
	var rep Report
	raw, _ := os.ReadFile(js) //nolint:gosec // test output
	if err := json.Unmarshal(raw, &rep); err != nil {
		t.Fatal(err)
	}
	if rep.JudgeModel != "fake-judge" || rep.Summary.JudgeScored != 4 || rep.Results[0].Judge == nil {
		t.Fatalf("judge: %+v", rep.Summary)
	}
	// Nothing to judge fails without a call.
	j := &Judge{Provider: &ai.Fake{Respond: func(ai.Request) (*ai.Response, error) { t.Fatal("called"); return nil, nil }}}
	if v := j.Grade(context.Background(), Case{}, &builder.Proposal{}, registry(t)); v.Pass {
		t.Fatal("an empty draft passed the judge")
	}
	// The judge is never the model under test.
	t.Setenv("TASKIEM_AI_BASE_URL", "http://127.0.0.1:1/v1")
	err := run([]string{"-suite", "../../evals/builder", "-limit", "1", "-provider", "selfhosted", "-model", "m", "-judge", "-judge-model", "m"}, &out)
	if err == nil || !strings.Contains(err.Error(), "must not be the model under test") {
		t.Fatalf("the model under test judged itself: %v", err)
	}
}

func TestRepairSuite(t *testing.T) {
	cases, err := loadRepair("../../evals/repair")
	if err != nil {
		t.Fatal(err)
	}
	classes := map[string]int{}
	for _, c := range cases {
		classes[string(c.Expect.Class)]++
	}
	for _, c := range classNames() {
		if classes[c] < 2 {
			t.Errorf("class %s has %d fixtures", c, classes[c])
		}
	}
	// The recorded failing case fails the original, fails the offline
	// heuristic's do-nothing patch, and passes a real fix (the oracle).
	reg := registry(t)
	var fixture RepairCase
	for _, c := range cases {
		if c.ID == "data-missing-trigger-field" {
			fixture = c
		}
	}
	reg2 := &repairTest{raw: fixture.Regression}
	if chk := repairCheck(fixture.doc, fixture.doc, reg2, nil, reg); chk.Passed {
		t.Fatal("an unchanged definition passed")
	}
	fixed := strings.Replace(string(fixture.doc), `"action":"send_sms",`, `"action":"send_sms","when":"=has(trigger.body.customer)",`, 1)
	if chk := repairCheck(fixture.doc, []byte(fixed), reg2, nil, reg); !chk.Passed {
		t.Fatalf("the fix failed: %v", chk.Feedback)
	}
	described := strings.Replace(string(fixture.doc), `"name":"Welcome"`, `"name":"Welcome","description":"x"`, 1)
	if chk := repairCheck(fixture.doc, []byte(described), reg2, nil, reg); chk.Passed {
		t.Fatal("a patch that fixes nothing passed")
	}
}

func TestCostAndSkip(t *testing.T) {
	prices, err := parsePrices([]string{"my-model=1,2"})
	if err != nil {
		t.Fatal(err)
	}
	u := ai.Usage{InputTokens: 1_000_000, OutputTokens: 100_000, CacheCreationTokens: 1_000_000, CacheReadTokens: 1_000_000}
	if c := cost(prices, "claude-opus-5-5-20261001", u); c == nil || math.Abs(*c-(4+2+5+0.2)) > 1e-9 {
		t.Errorf("opus cost %v", c)
	}
	if c := cost(prices, "my-model", ai.Usage{InputTokens: 1_000_000}); c == nil || *c != 1 {
		t.Errorf("custom price %v", c)
	}
	if c := cost(prices, "heuristic", u); c != nil {
		t.Errorf("an unknown price is not zero: %v", *c)
	}
	if _, err := parsePrices([]string{"bad"}); err == nil {
		t.Error("a malformed price accepted")
	}
	t.Setenv("ANTHROPIC_API_KEY", "")
	var out strings.Builder
	if err := run([]string{"-provider", "anthropic", "-skip-without-key"}, &out); err != nil || !strings.Contains(out.String(), "skipping") {
		t.Fatalf("skip: %v %s", err, out.String())
	}
}
