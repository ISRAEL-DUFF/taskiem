// Command aieval runs the AI evaluation suites (spec 12.4, docs/ai.md#evaluation).
//
// The builder suite (evals/builder/*.jsonl, at least 200 requests) puts
// every request through the real builder pipeline (engine/ai/builder:
// retrieval with templates first, draft, publishing checks, self-
// correction, dry run) against the connectors compiled into this binary,
// and grades each draft deterministically: valid on the first try, valid
// after corrections, dry-run tests, the connectors, step types, trigger
// and properties the case requires (rules.go), and policy findings. A
// model-graded rubric ("does it do what was asked") is optional (-judge).
// The repair suite (evals/repair) checks failure classification and
// patches (-mode repair).
//
//	go run ./tools/aieval                                          # offline heuristic
//	go run ./tools/aieval -compare evals/builder/baseline.json     # the CI gate
//	go run ./tools/aieval -provider anthropic -min-gate 0.7 -out r.json -markdown r.md
//	go run ./tools/aieval -mode repair -compare evals/repair/baseline.json
//
// The fake provider is a keyword heuristic, not a model: it exercises the
// pipeline and the grader offline, deterministically, so a change to the
// pipeline, the prompt plumbing, the templates or the grader that makes
// it worse fails the comparison with the committed baseline. Nothing runs
// against a connector's provider: dry runs mock every step.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/israel-duff/taskiem/connectors/builtin"
	"github.com/israel-duff/taskiem/engine/ai"
	"github.com/israel-duff/taskiem/engine/connector"
)

func main() {
	if err := run(os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "aieval:", err)
		os.Exit(1)
	}
}

// errRegression fails the command when a comparison finds one.
var errRegression = errors.New("regression against the baseline")

type options struct {
	mode, suite, provider, model, tags string
	limit, reps, parallel              int
	caseTimeout                        time.Duration
	out, md, baselineOut, compare      string
	tolerance, minGate                 float64
	judge                              bool
	judgeProvider, judgeModel          string
	prices                             multiFlag
	verbose, skipWithoutKey            bool
}

func run(args []string, stdout io.Writer) error {
	var o options
	fs := flag.NewFlagSet("aieval", flag.ContinueOnError)
	fs.StringVar(&o.mode, "mode", "builder", "builder | repair")
	fs.StringVar(&o.suite, "suite", "", "a .jsonl file or a directory of them (default evals/<mode>)")
	fs.StringVar(&o.provider, "provider", "fake", "fake | anthropic | selfhosted (configured by TASKIEM_AI_* and ANTHROPIC_API_KEY)")
	fs.StringVar(&o.model, "model", "", "model id (default: TASKIEM_AI_MODEL or claude-opus-5-5); answers from another model are not scored")
	fs.StringVar(&o.tags, "tags", "", "comma-separated tags; run only cases with one of them")
	fs.IntVar(&o.limit, "limit", 0, "run at most this many cases")
	fs.IntVar(&o.reps, "reps", 1, "attempts per case (builder)")
	fs.IntVar(&o.parallel, "parallel", 1, "attempts in flight at once (builder)")
	fs.DurationVar(&o.caseTimeout, "case-timeout", 10*time.Minute, "wall-clock ceiling per attempt; a timeout is not scored")
	fs.StringVar(&o.out, "out", "", "write the full JSON report here")
	fs.StringVar(&o.md, "markdown", "", "write a Markdown report here")
	fs.StringVar(&o.baselineOut, "baseline-out", "", "write the report as a baseline (no timings) here")
	fs.StringVar(&o.compare, "compare", "", "compare with this baseline and fail on a regression beyond -tolerance")
	fs.Float64Var(&o.tolerance, "tolerance", 0.02, "allowed drop in any rate when comparing (0..1)")
	fs.Float64Var(&o.minGate, "min-gate", 0, "fail when the valid-and-test-passing-on-first-try rate is below this (0..1)")
	fs.Float64Var(&o.minGate, "min-first-try", 0, "alias of -min-gate")
	fs.BoolVar(&o.judge, "judge", false, "also grade with a model-graded rubric (builder)")
	fs.StringVar(&o.judgeProvider, "judge-provider", "anthropic", "the judge's provider: anthropic | selfhosted | fake")
	fs.StringVar(&o.judgeModel, "judge-model", "claude-sonnet-5-5", "the judge's model; never the model under test")
	fs.Var(&o.prices, "price", "model=in,out[,cache_read] in dollars per million tokens (repeatable; Anthropic's prices are built in)")
	fs.BoolVar(&o.verbose, "v", false, "print every case")
	fs.BoolVar(&o.skipWithoutKey, "skip-without-key", false, "with -provider anthropic: succeed without running when ANTHROPIC_API_KEY is not set")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if o.suite == "" {
		o.suite = "evals/" + o.mode
	}
	if o.skipWithoutKey && o.provider == "anthropic" && os.Getenv("ANTHROPIC_API_KEY") == "" {
		fmt.Fprintln(stdout, "aieval: ANTHROPIC_API_KEY is not set; skipping the real-model evaluation")
		return nil
	}
	prices, err := parsePrices(o.prices)
	if err != nil {
		return err
	}
	reg := connector.NewRegistry()
	if err := builtin.Register(reg, builtin.Options{}); err != nil {
		return err
	}
	switch o.mode {
	case "builder":
		return runBuilder(o, prices, reg, stdout)
	case "repair":
		return runRepairSuite(o, prices, reg, stdout)
	}
	return fmt.Errorf("unknown -mode %q", o.mode)
}

func providerFor(name, model string, respond func(ai.Request) (*ai.Response, error), fakeModel string) (ai.Provider, error) {
	if name == "fake" {
		return &ai.Fake{Respond: respond, Models: fakeModel}, nil
	}
	lookup := func(k string) (string, bool) {
		switch k {
		case "TASKIEM_AI_PROVIDER":
			return name, true
		case "TASKIEM_AI_MODEL":
			if model != "" {
				return model, true
			}
		}
		return os.LookupEnv(k)
	}
	cfg, err := ai.ConfigFromEnv(lookup)
	if err != nil {
		return nil, err
	}
	return ai.New(cfg)
}

func runBuilder(o options, prices map[string]Price, reg *connector.Registry, stdout io.Writer) error {
	cases, err := load(o.suite)
	if err != nil {
		return err
	}
	cases = filter(cases, func(c Case) []string { return c.Tags }, o.tags, o.limit)
	if len(cases) == 0 {
		return errors.New("no cases")
	}
	prov, err := providerFor(o.provider, o.model, heuristic, "heuristic")
	if err != nil {
		return err
	}
	eo := evalOptions{Reps: o.reps, Parallel: o.parallel, CaseTimeout: o.caseTimeout, Prices: prices}
	if o.provider != "fake" {
		eo.WantModel = prov.Model()
	}
	judgeModel := ""
	if o.judge {
		if o.judgeProvider != "fake" && o.judgeModel == prov.Model() {
			return fmt.Errorf("the judge must not be the model under test (%s); pick another -judge-model", o.judgeModel)
		}
		jp, err := providerFor(o.judgeProvider, o.judgeModel, fakeJudge, "fake-judge")
		if err != nil {
			return fmt.Errorf("judge: %w", err)
		}
		eo.Judge = &Judge{Provider: jp, Prices: prices}
		judgeModel = jp.Model()
	}
	results := evaluate(context.Background(), prov, reg, cases, eo)
	rep := buildReport(o.suite, prov.Name(), prov.Model(), judgeModel, max(o.reps, 1), results)
	printReport(stdout, rep, o.verbose)
	if err := writeOutputs(o, rep, markdown(rep), func() any {
		base := rep
		base.Results = make([]Result, len(rep.Results))
		for i, r := range rep.Results {
			r.Seconds = 0
			base.Results[i] = r
		}
		return base
	}); err != nil {
		return err
	}
	var failure error
	if o.compare != "" {
		var base Report
		if err := readReport(o.compare, &base); err != nil {
			return err
		}
		if base.Kind != "" && base.Kind != "builder" {
			return fmt.Errorf("%s is a %s baseline", o.compare, base.Kind)
		}
		regs, lost := compareBuilder(base, rep, o.tolerance)
		printComparison(stdout, regs, lost, o.tolerance)
		if len(regs) > 0 {
			failure = errRegression
		}
	}
	if o.minGate > 0 && rep.Summary.Gate < o.minGate {
		return fmt.Errorf("valid and test-passing on the first try: %s, below %s", pct(rep.Summary.Gate), pct(o.minGate))
	}
	return failure
}

func runRepairSuite(o options, prices map[string]Price, reg *connector.Registry, stdout io.Writer) error {
	cases, err := loadRepair(o.suite)
	if err != nil {
		return err
	}
	cases = filter(cases, func(c RepairCase) []string { return c.Tags }, o.tags, o.limit)
	if len(cases) == 0 {
		return errors.New("no cases")
	}
	prov, err := providerFor(o.provider, o.model, repairHeuristic, "heuristic")
	if err != nil {
		return err
	}
	results := evaluateRepairs(context.Background(), prov, reg, cases, prices)
	rep := buildRepairReport(o.suite, prov.Name(), prov.Model(), results)
	printRepairReport(stdout, rep)
	if err := writeOutputs(o, rep, repairMarkdown(rep), func() any { return rep }); err != nil {
		return err
	}
	if o.compare != "" {
		var base RepairReport
		if err := readReport(o.compare, &base); err != nil {
			return err
		}
		if base.Kind != "repair" {
			return fmt.Errorf("%s is not a repair baseline", o.compare)
		}
		regs := compareRepair(base, rep, o.tolerance)
		printComparison(stdout, regs, nil, o.tolerance)
		if len(regs) > 0 {
			return errRegression
		}
	}
	return nil
}

func writeOutputs(o options, full any, md string, baseline func() any) error {
	if o.out != "" {
		if err := writeJSON(o.out, full); err != nil {
			return err
		}
	}
	if o.md != "" {
		if err := os.WriteFile(o.md, []byte(md), 0o600); err != nil {
			return err
		}
	}
	if o.baselineOut != "" {
		if err := writeJSON(o.baselineOut, baseline()); err != nil {
			return err
		}
	}
	return nil
}

func readReport(path string, v any) error {
	raw, err := os.ReadFile(path) //nolint:gosec // the baseline named on the command line
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, v)
}
