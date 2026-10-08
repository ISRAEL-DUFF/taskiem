package main

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"sort"
	"strings"
	"text/tabwriter"

	"github.com/israel-duff/taskiem/engine/ai"
)

// Summary aggregates attempts. Rates are over scored attempts (ok and
// refused); errors, timeouts, truncations and model mismatches are
// counted apart.
type Summary struct {
	Cases     int `json:"cases"`
	Attempts  int `json:"attempts"`
	Scored    int `json:"scored"`
	Errors    int `json:"errors"`
	Timeouts  int `json:"timeouts"`
	Truncated int `json:"truncated"`
	Refusals  int `json:"refusals"`
	Mismatch  int `json:"model_mismatches"`

	ValidFirstTry float64 `json:"valid_first_try_rate"`
	Valid         float64 `json:"valid_after_corrections_rate"`
	TestPass      float64 `json:"test_pass_rate"`
	// Gate: valid and test-passing on the first try (G3: ≥ 70% against
	// the real model).
	Gate         float64 `json:"valid_first_try_and_tests_pass_rate"`
	Requirements float64 `json:"requirements_rate"`
	// Properties is the share of property checks that held.
	Properties float64 `json:"property_pass_rate"`
	Strict     float64 `json:"strict_rate"`

	PolicyViolations    int     `json:"policy_violations"`
	CasesWithViolations int     `json:"attempts_with_policy_violations"`
	ViolationRate       float64 `json:"policy_violation_rate"`
	ConnectorRecall     float64 `json:"connector_recall"`
	StepRecall          float64 `json:"step_recall"`
	TriggerAccuracy     float64 `json:"trigger_accuracy"`
	TemplateStarts      int     `json:"template_starts"`

	Usage ai.Usage `json:"usage"`
	// CostUSD is nil when any answering model's price is unknown.
	CostUSD *float64 `json:"cost_usd"`
	// NoiseFloor is roughly the 95% half-width of a pass rate at this
	// many scored attempts (1/sqrt(n)): differences smaller than it are
	// noise.
	NoiseFloor float64 `json:"noise_floor"`

	JudgeScored int      `json:"judge_scored,omitempty"`
	JudgePass   float64  `json:"judge_pass_rate,omitempty"`
	JudgeUsage  ai.Usage `json:"judge_usage,omitzero"`
	JudgeCost   *float64 `json:"judge_cost_usd,omitempty"`
}

// Group is a slice of the suite (a tag, a difficulty).
type Group struct {
	Cases        int     `json:"cases"`
	Gate         float64 `json:"valid_first_try_and_tests_pass_rate"`
	Requirements float64 `json:"requirements_rate"`
	Strict       float64 `json:"strict_rate"`
	Violations   int     `json:"policy_violations"`
}

// Report is the whole evaluation.
type Report struct {
	Kind       string           `json:"kind"` // builder
	Suite      string           `json:"suite"`
	Provider   string           `json:"provider"`
	Model      string           `json:"model"`
	JudgeModel string           `json:"judge_model,omitempty"`
	Reps       int              `json:"reps"`
	Summary    Summary          `json:"summary"`
	ByTag      map[string]Group `json:"by_tag"`
	ByDiff     map[string]Group `json:"by_difficulty"`
	ByProperty map[string]Group `json:"by_property"`
	Results    []Result         `json:"results"`
}

func rate(n, d int) float64 {
	if d == 0 {
		return 0
	}
	return math.Round(1e4*float64(n)/float64(d)) / 1e4
}

func summarise(results []Result) Summary {
	s := Summary{}
	cases := map[string]bool{}
	var first, valid, tests, gate, reqs, strict, propOK, propN, trig, judged, judgePass int
	var crec, srec float64
	costKnown := true
	total := 0.0
	jcostKnown, jtotal := true, 0.0
	for _, r := range results {
		cases[r.ID] = true
		s.Attempts++
		s.Usage = s.Usage.Add(r.Usage)
		if r.CostUSD != nil {
			total += *r.CostUSD
		} else if r.Usage.Total() > 0 {
			costKnown = false
		}
		switch r.Status {
		case statusError:
			s.Errors++
		case statusTimeout:
			s.Timeouts++
		case statusTruncated:
			s.Truncated++
		case statusModelMismatch:
			s.Mismatch++
		case statusRefused:
			s.Refusals++
		}
		if !r.scored() {
			continue
		}
		s.Scored++
		if r.ValidFirstTry {
			first++
		}
		if r.Valid {
			valid++
		}
		if r.TestsPassed {
			tests++
		}
		if r.Gate {
			gate++
		}
		if r.Requirements {
			reqs++
		}
		if r.Strict {
			strict++
		}
		if r.TriggerOK {
			trig++
		}
		if r.Template != "" {
			s.TemplateStarts++
		}
		for _, ok := range r.Properties {
			propN++
			if ok {
				propOK++
			}
		}
		s.PolicyViolations += r.PolicyViolations
		if r.PolicyViolations > 0 {
			s.CasesWithViolations++
		}
		crec += r.ConnectorRecall
		srec += r.StepRecall
		if r.Judge != nil {
			s.JudgeUsage = s.JudgeUsage.Add(r.Judge.Usage)
			if r.Judge.CostUSD != nil {
				jtotal += *r.Judge.CostUSD
			} else if r.Judge.Usage.Total() > 0 {
				jcostKnown = false
			}
			if r.Judge.Error == "" {
				judged++
				if r.Judge.Pass {
					judgePass++
				}
			}
		}
	}
	s.Cases = len(cases)
	n := s.Scored
	s.ValidFirstTry, s.Valid, s.TestPass, s.Gate = rate(first, n), rate(valid, n), rate(tests, n), rate(gate, n)
	s.Requirements, s.Strict, s.Properties = rate(reqs, n), rate(strict, n), rate(propOK, propN)
	s.ViolationRate, s.TriggerAccuracy = rate(s.CasesWithViolations, n), rate(trig, n)
	if n > 0 {
		s.ConnectorRecall = math.Round(1e4*crec/float64(n)) / 1e4
		s.StepRecall = math.Round(1e4*srec/float64(n)) / 1e4
		s.NoiseFloor = math.Round(1e4/math.Sqrt(float64(n))) / 1e4
	}
	if costKnown && s.Usage.Total() > 0 {
		c := math.Round(total*1e4) / 1e4
		s.CostUSD = &c
	}
	if judged > 0 {
		s.JudgeScored, s.JudgePass = judged, rate(judgePass, judged)
		if jcostKnown {
			c := math.Round(jtotal*1e4) / 1e4
			s.JudgeCost = &c
		}
	}
	return s
}

func groupOf(results []Result) Group {
	s := summarise(results)
	return Group{Cases: s.Cases, Gate: s.Gate, Requirements: s.Requirements, Strict: s.Strict, Violations: s.PolicyViolations}
}

// buildReport assembles the report from attempts.
func buildReport(suite, provider, model, judgeModel string, reps int, results []Result) Report {
	rep := Report{Kind: "builder", Suite: suite, Provider: provider, Model: model, JudgeModel: judgeModel, Reps: reps,
		Summary: summarise(results), ByTag: map[string]Group{}, ByDiff: map[string]Group{}, ByProperty: map[string]Group{}, Results: results}
	tags, diffs, props := map[string][]Result{}, map[string][]Result{}, map[string][]Result{}
	for _, r := range results {
		for _, t := range r.Tags {
			tags[t] = append(tags[t], r)
		}
		if r.Difficulty != "" {
			diffs[r.Difficulty] = append(diffs[r.Difficulty], r)
		}
		for p := range r.Properties {
			props[p] = append(props[p], r)
		}
	}
	for t, rs := range tags {
		rep.ByTag[t] = groupOf(rs)
	}
	for d, rs := range diffs {
		rep.ByDiff[d] = groupOf(rs)
	}
	for p, rs := range props {
		n, ok := 0, 0
		for _, r := range rs {
			if r.scored() {
				n++
				if r.Properties[p] {
					ok++
				}
			}
		}
		rep.ByProperty[p] = Group{Cases: n, Requirements: rate(ok, n)}
	}
	return rep
}

func pct(f float64) string { return fmt.Sprintf("%.1f%%", 100*f) }

func money(c *float64) string {
	if c == nil {
		return "n/a (price unknown)"
	}
	return fmt.Sprintf("$%.4f", *c)
}

func printReport(w io.Writer, rep Report, verbose bool) {
	if verbose {
		tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
		fmt.Fprintln(tw, "CASE\tSTATUS\tFIRST TRY\tTESTS\tREQS\tPOLICY\tROUNDS\tNOTE")
		for _, r := range rep.Results {
			note := r.Error
			if note == "" && len(r.Missing) > 0 {
				note = "missing " + strings.Join(r.Missing, "; ")
			}
			if note == "" && len(r.Problems) > 0 {
				note = r.Problems[0]
			}
			if len(note) > 90 {
				note = note[:87] + "..."
			}
			fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%d\t%d\t%s\n", r.ID, r.Status, yes(r.ValidFirstTry), yes(r.TestsPassed), yes(r.Requirements),
				r.PolicyViolations, r.Rounds, note)
		}
		_ = tw.Flush()
		fmt.Fprintln(w)
	}
	s := rep.Summary
	fmt.Fprintf(w, "%s %s, %d cases × %d: valid on first try %s, valid after corrections %s, tests pass %s, valid and passing on first try %s (±%s), "+
		"requirements %s, properties %s, strict %s; %d policy violations; %d errors, %d refusals, %d truncated; %d tokens, cost %s\n",
		rep.Provider, rep.Model, s.Cases, max(rep.Reps, 1), pct(s.ValidFirstTry), pct(s.Valid), pct(s.TestPass), pct(s.Gate), pct(s.NoiseFloor),
		pct(s.Requirements), pct(s.Properties), pct(s.Strict), s.PolicyViolations, s.Errors+s.Timeouts+s.Mismatch, s.Refusals, s.Truncated,
		s.Usage.Total(), money(s.CostUSD))
}

func yes(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// markdown renders the report for people (a pull request comment, a job
// summary).
func markdown(rep Report) string {
	var b strings.Builder
	s := rep.Summary
	fmt.Fprintf(&b, "# AI builder evaluation\n\n`%s` on `%s` (%s), %d cases × %d reps, suite `%s`.\n\n", rep.Model, rep.Provider, sourceNote(rep), s.Cases, max(rep.Reps, 1), rep.Suite)
	b.WriteString("| Measure | Value |\n| --- | --- |\n")
	row := func(k, v string) { fmt.Fprintf(&b, "| %s | %s |\n", k, v) }
	row("Valid and test-passing on the first try (G3 gate)", fmt.Sprintf("**%s** (±%s)", pct(s.Gate), pct(s.NoiseFloor)))
	row("Valid on the first try", pct(s.ValidFirstTry))
	row("Valid after corrections", pct(s.Valid))
	row("Dry-run tests pass", pct(s.TestPass))
	row("Requirements met (connectors, steps, trigger, properties)", pct(s.Requirements))
	row("Property checks passed", pct(s.Properties))
	row("Strict (gate, requirements, no policy violation)", pct(s.Strict))
	row("Policy violations", fmt.Sprintf("%d in %d attempts", s.PolicyViolations, s.CasesWithViolations))
	row("Connector / step recall", pct(s.ConnectorRecall)+" / "+pct(s.StepRecall))
	row("Trigger accuracy", pct(s.TriggerAccuracy))
	row("Started from a template", fmt.Sprintf("%d", s.TemplateStarts))
	row("Not scored", fmt.Sprintf("%d errors, %d timeouts, %d truncated, %d other model; %d refusals (scored)", s.Errors, s.Timeouts, s.Truncated, s.Mismatch, s.Refusals))
	row("Tokens", fmt.Sprintf("%d (input %d, output %d, cache write %d, cache read %d)", s.Usage.Total(), s.Usage.InputTokens, s.Usage.OutputTokens,
		s.Usage.CacheCreationTokens, s.Usage.CacheReadTokens))
	row("Cost", money(s.CostUSD))
	if s.JudgeScored > 0 {
		row("Judge ("+rep.JudgeModel+")", fmt.Sprintf("%s pass of %d judged; cost %s", pct(s.JudgePass), s.JudgeScored, money(s.JudgeCost)))
	}
	b.WriteString("\n## By tag\n\n| Tag | Cases | Gate | Requirements | Strict | Policy violations |\n| --- | --- | --- | --- | --- | --- |\n")
	for _, t := range sortedKeys(rep.ByTag) {
		g := rep.ByTag[t]
		fmt.Fprintf(&b, "| %s | %d | %s | %s | %s | %d |\n", t, g.Cases, pct(g.Gate), pct(g.Requirements), pct(g.Strict), g.Violations)
	}
	b.WriteString("\n## By difficulty\n\n| Difficulty | Cases | Gate | Requirements | Strict |\n| --- | --- | --- | --- | --- |\n")
	for _, d := range difficulties {
		if g, ok := rep.ByDiff[d]; ok {
			fmt.Fprintf(&b, "| %s | %d | %s | %s | %s |\n", d, g.Cases, pct(g.Gate), pct(g.Requirements), pct(g.Strict))
		}
	}
	b.WriteString("\n## By property\n\n| Property | Checked | Held |\n| --- | --- | --- |\n")
	for _, p := range sortedKeys(rep.ByProperty) {
		g := rep.ByProperty[p]
		fmt.Fprintf(&b, "| %s | %d | %s |\n", p, g.Cases, pct(g.Requirements))
	}
	var failing []Result
	for _, r := range rep.Results {
		if r.scored() && !r.Strict {
			failing = append(failing, r)
		}
	}
	if len(failing) > 0 {
		fmt.Fprintf(&b, "\n## Not strict (%d)\n\n| Case | First try | Tests | Missing or problem |\n| --- | --- | --- | --- |\n", len(failing))
		for i, r := range failing {
			if i == 60 {
				fmt.Fprintf(&b, "| … | | | %d more in the JSON report |\n", len(failing)-i)
				break
			}
			note := strings.Join(r.Missing, "; ")
			if note == "" && len(r.Problems) > 0 {
				note = r.Problems[0]
			}
			note = strings.ReplaceAll(note, "|", "\\|")
			if len(note) > 140 {
				note = note[:137] + "..."
			}
			fmt.Fprintf(&b, "| %s | %s | %s | %s |\n", r.ID, yes(r.ValidFirstTry), yes(r.TestsPassed), note)
		}
	}
	return b.String()
}

func sourceNote(rep Report) string {
	if rep.Provider == "fake" {
		return "the offline heuristic: a regression check of the pipeline, not a model score"
	}
	return "a real model"
}

func writeJSON(path string, v any) error {
	raw, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(raw, '\n'), 0o600)
}

// Regression is a measure that got worse than the baseline allows.
type Regression struct {
	Measure  string  `json:"measure"`
	Baseline float64 `json:"baseline"`
	Current  float64 `json:"current"`
}

// compareBuilder compares a run with a baseline over the cases both ran:
// any rate more than tol below the baseline, a policy violation rate more
// than tol above it, or more unscored attempts, is a regression. It also
// lists cases that passed the gate in the baseline and fail it now.
func compareBuilder(base, cur Report, tol float64) ([]Regression, []string) {
	in := map[string]bool{}
	for _, r := range base.Results {
		in[r.ID] = true
	}
	common := map[string]bool{}
	var curRes []Result
	for _, r := range cur.Results {
		if in[r.ID] {
			common[r.ID] = true
			curRes = append(curRes, r)
		}
	}
	var baseRes []Result
	basePass := map[string]bool{}
	for _, r := range base.Results {
		if common[r.ID] {
			baseRes = append(baseRes, r)
			if r.Gate {
				basePass[r.ID] = true
			}
		}
	}
	b, c := summarise(baseRes), summarise(curRes)
	var regs []Regression
	check := func(name string, bv, cv float64, higherIsWorse bool) {
		if (!higherIsWorse && cv < bv-tol-1e-9) || (higherIsWorse && cv > bv+tol+1e-9) {
			regs = append(regs, Regression{name, bv, cv})
		}
	}
	check("valid_first_try_rate", b.ValidFirstTry, c.ValidFirstTry, false)
	check("valid_after_corrections_rate", b.Valid, c.Valid, false)
	check("test_pass_rate", b.TestPass, c.TestPass, false)
	check("valid_first_try_and_tests_pass_rate", b.Gate, c.Gate, false)
	check("requirements_rate", b.Requirements, c.Requirements, false)
	check("property_pass_rate", b.Properties, c.Properties, false)
	check("strict_rate", b.Strict, c.Strict, false)
	check("policy_violation_rate", b.ViolationRate, c.ViolationRate, true)
	bu, cu := b.Attempts-b.Scored, c.Attempts-c.Scored
	if cu > bu {
		regs = append(regs, Regression{"unscored_attempts", float64(bu), float64(cu)})
	}
	var lost []string
	seen := map[string]bool{}
	for _, r := range curRes {
		if basePass[r.ID] && !r.Gate && !seen[r.ID] {
			seen[r.ID] = true
			lost = append(lost, r.ID)
		}
	}
	return regs, lost
}

func printComparison(w io.Writer, regs []Regression, lost []string, tol float64) {
	if len(regs) == 0 {
		fmt.Fprintf(w, "no regression against the baseline (tolerance %s)\n", pct(tol))
	} else {
		fmt.Fprintf(w, "REGRESSION against the baseline (tolerance %s):\n", pct(tol))
		for _, r := range regs {
			fmt.Fprintf(w, "  %s: %.4g -> %.4g\n", r.Measure, r.Baseline, r.Current)
		}
	}
	if len(lost) > 0 {
		fmt.Fprintf(w, "cases that passed the gate in the baseline and fail now: %s\n", strings.Join(lost, ", "))
	}
}
