// Command aieval runs the AI builder's evaluation suite (spec 12.4): each
// request in evals/builder/*.jsonl goes through the real pipeline
// (engine/ai/builder: retrieval, draft, publishing checks, self-correction,
// dry run) against the connectors compiled into this binary, and the
// report gives the rates gate G3 and B3 measure: valid on the first try,
// valid and test-passing, and policy violations.
//
//	go run ./tools/aieval                                 # deterministic fake model
//	go run ./tools/aieval -provider anthropic -out r.json # Claude (ANTHROPIC_API_KEY)
//	go run ./tools/aieval -tags payments -min-first-try 0.7
//
// The fake model is a keyword heuristic, not a model: it exercises the
// pipeline and the report offline, and sets a floor any real model must
// beat. Nothing runs against a provider: dry runs mock every connector.
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/israel-duff/taskiem/connectors/builtin"
	"github.com/israel-duff/taskiem/engine/ai"
	"github.com/israel-duff/taskiem/engine/ai/builder"
	"github.com/israel-duff/taskiem/engine/connector"
	"github.com/israel-duff/taskiem/engine/wd"
	"github.com/israel-duff/taskiem/engine/wdcheck"
)

// Case is one line of a suite.
type Case struct {
	ID      string `json:"id"`
	Request string `json:"request"`
	Expect  struct {
		Connectors []string `json:"connectors,omitempty"`
		Steps      []string `json:"steps,omitempty"` // step types the workflow should use
	} `json:"expect"`
	Tags []string `json:"tags,omitempty"`
}

// Result is one case's outcome.
type Result struct {
	ID               string   `json:"id"`
	Tags             []string `json:"tags,omitempty"`
	Error            string   `json:"error,omitempty"`
	ValidFirstTry    bool     `json:"valid_first_try"`
	Valid            bool     `json:"valid"`
	TestsPassed      bool     `json:"tests_passed"`
	PolicyViolations int      `json:"policy_violations"`
	Rounds           int      `json:"rounds"`
	Tokens           int64    `json:"tokens"`
	ConnectorRecall  float64  `json:"connector_recall"`
	StepRecall       float64  `json:"step_recall"`
	Missing          []string `json:"missing,omitempty"`
	Problems         []string `json:"problems,omitempty"`
	Seconds          float64  `json:"seconds"`
}

// Report is the suite's summary.
type Report struct {
	Provider         string   `json:"provider"`
	Model            string   `json:"model"`
	Cases            int      `json:"cases"`
	Errors           int      `json:"errors"`
	ValidFirstTry    float64  `json:"valid_first_try_rate"`
	Valid            float64  `json:"valid_rate"`
	FirstTryAndTests float64  `json:"valid_first_try_and_tests_pass_rate"`
	TestPass         float64  `json:"test_pass_rate"`
	PolicyViolations int      `json:"policy_violations"`
	ConnectorRecall  float64  `json:"connector_recall"`
	StepRecall       float64  `json:"step_recall"`
	Tokens           int64    `json:"tokens"`
	Results          []Result `json:"results"`
}

func main() {
	if err := run(os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "aieval:", err)
		os.Exit(1)
	}
}

func run(args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("aieval", flag.ContinueOnError)
	suite := fs.String("suite", "evals/builder", "a .jsonl file or a directory of them")
	provider := fs.String("provider", "fake", "fake | anthropic | selfhosted (configured by TASKIEM_AI_* and ANTHROPIC_API_KEY)")
	model := fs.String("model", "", "model id (default: TASKIEM_AI_MODEL or claude-opus-5-5)")
	tags := fs.String("tags", "", "comma-separated tags; run only cases with one of them")
	limit := fs.Int("limit", 0, "run at most this many cases")
	out := fs.String("out", "", "write the full JSON report here")
	minFirst := fs.Float64("min-first-try", 0, "fail when the valid-and-tests-pass-on-first-try rate is below this (0..1)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	cases, err := load(*suite)
	if err != nil {
		return err
	}
	cases = filter(cases, *tags, *limit)
	if len(cases) == 0 {
		return errors.New("no cases")
	}
	reg := connector.NewRegistry()
	if err := builtin.Register(reg, builtin.Options{}); err != nil {
		return err
	}
	prov, err := providerFor(*provider, *model)
	if err != nil {
		return err
	}
	rep := evaluate(context.Background(), prov, reg, cases)
	printReport(stdout, rep)
	if *out != "" {
		raw, _ := json.MarshalIndent(rep, "", "  ")
		if err := os.WriteFile(*out, raw, 0o600); err != nil {
			return err
		}
	}
	if *minFirst > 0 && rep.FirstTryAndTests < *minFirst {
		return fmt.Errorf("valid and test-passing on the first try: %.0f%%, below %.0f%%", 100*rep.FirstTryAndTests, 100**minFirst)
	}
	return nil
}

func providerFor(name, model string) (ai.Provider, error) {
	if name == "fake" {
		return &ai.Fake{Respond: heuristic, Models: "heuristic"}, nil
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

func load(path string) ([]Case, error) {
	files := []string{path}
	if st, err := os.Stat(path); err == nil && st.IsDir() {
		files, err = filepath.Glob(filepath.Join(path, "*.jsonl"))
		if err != nil {
			return nil, err
		}
		sort.Strings(files)
	}
	var out []Case
	seen := map[string]bool{}
	for _, f := range files {
		fh, err := os.Open(f) //nolint:gosec // the suite named on the command line
		if err != nil {
			return nil, err
		}
		sc := bufio.NewScanner(fh)
		sc.Buffer(make([]byte, 1<<20), 1<<20)
		line := 0
		for sc.Scan() {
			line++
			text := strings.TrimSpace(sc.Text())
			if text == "" || strings.HasPrefix(text, "//") {
				continue
			}
			var c Case
			dec := json.NewDecoder(strings.NewReader(text))
			dec.DisallowUnknownFields()
			if err := dec.Decode(&c); err != nil {
				_ = fh.Close()
				return nil, fmt.Errorf("%s:%d: %w", f, line, err)
			}
			if c.ID == "" || c.Request == "" || seen[c.ID] {
				_ = fh.Close()
				return nil, fmt.Errorf("%s:%d: every case needs a unique id and a request", f, line)
			}
			seen[c.ID] = true
			out = append(out, c)
		}
		_ = fh.Close()
		if err := sc.Err(); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func filter(cases []Case, tags string, limit int) []Case {
	if tags != "" {
		want := map[string]bool{}
		for _, t := range strings.Split(tags, ",") {
			want[strings.TrimSpace(t)] = true
		}
		var kept []Case
		for _, c := range cases {
			for _, t := range c.Tags {
				if want[t] {
					kept = append(kept, c)
					break
				}
			}
		}
		cases = kept
	}
	if limit > 0 && len(cases) > limit {
		cases = cases[:limit]
	}
	return cases
}

func evaluate(ctx context.Context, prov ai.Provider, reg *connector.Registry, cases []Case) Report {
	b := &builder.Builder{
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
	rep := Report{Provider: prov.Name(), Model: prov.Model(), Cases: len(cases)}
	var first, valid, both, tests, crec, srec float64
	for _, c := range cases {
		start := time.Now()
		r := Result{ID: c.ID, Tags: c.Tags}
		p, err := b.Build(ctx, builder.Request{Goal: c.Request})
		r.Seconds = time.Since(start).Seconds()
		if err != nil {
			r.Error = err.Error()
			rep.Errors++
			rep.Results = append(rep.Results, r)
			continue
		}
		r.ValidFirstTry, r.Valid, r.TestsPassed = p.ValidFirstTry, p.Valid(), p.TestsPassed()
		r.PolicyViolations, r.Rounds, r.Tokens = p.PolicyViolations(), p.Rounds, p.Usage.Total()
		for _, pr := range p.Problems {
			r.Problems = append(r.Problems, pr.Path+": "+pr.Message)
		}
		r.ConnectorRecall, r.StepRecall, r.Missing = recall(p.Definition, c)
		if r.ValidFirstTry {
			first++
		}
		if r.Valid {
			valid++
		}
		if r.TestsPassed {
			tests++
		}
		if r.ValidFirstTry && r.TestsPassed {
			both++
		}
		crec += r.ConnectorRecall
		srec += r.StepRecall
		rep.PolicyViolations += r.PolicyViolations
		rep.Tokens += r.Tokens
		rep.Results = append(rep.Results, r)
	}
	n := float64(len(cases))
	rep.ValidFirstTry, rep.Valid, rep.FirstTryAndTests, rep.TestPass = first/n, valid/n, both/n, tests/n
	rep.ConnectorRecall, rep.StepRecall = crec/n, srec/n
	return rep
}

// recall measures how many expected connectors and step types the draft
// uses.
func recall(doc []byte, c Case) (float64, float64, []string) {
	usedC, usedS := map[string]bool{}, map[string]bool{}
	if def, err := wd.Load(doc); err == nil {
		var walk func(steps []*wd.Step)
		walk = func(steps []*wd.Step) {
			for _, st := range steps {
				usedS[st.Type] = true
				if st.Connector != "" {
					usedC[st.Connector] = true
				}
				for _, sub := range st.Children() {
					walk(sub)
				}
			}
		}
		walk(def.Steps)
		if ref, ok := def.Trigger.Config["connector"].(string); ok {
			usedC[ref] = true
		}
	}
	cr, missC := coverage(c.Expect.Connectors, usedC)
	sr, missS := coverage(c.Expect.Steps, usedS)
	return cr, sr, append(missC, missS...)
}

// coverage is the share of want that used has, and what it lacks.
//
// (An earlier version appended to a slice captured by a closure called in
// the same return statement that read the slice; with Go 1.26.0 that
// returned a header to a dead stack-allocated backing array and corrupted
// the heap. Keep slices built here plainly returned.)
func coverage(want []string, used map[string]bool) (float64, []string) {
	if len(want) == 0 {
		return 1, nil
	}
	missing := make([]string, 0, len(want))
	for _, w := range want {
		if !used[w] {
			missing = append(missing, w)
		}
	}
	return float64(len(want)-len(missing)) / float64(len(want)), missing
}

func printReport(w io.Writer, rep Report) {
	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "CASE\tFIRST TRY\tVALID\tTESTS\tPOLICY\tROUNDS\tRECALL\tNOTE")
	for _, r := range rep.Results {
		note := r.Error
		if note == "" && len(r.Missing) > 0 {
			note = "missing " + strings.Join(r.Missing, ", ")
		}
		if note == "" && len(r.Problems) > 0 {
			note = r.Problems[0]
		}
		if len(note) > 80 {
			note = note[:77] + "..."
		}
		fmt.Fprintf(tw, "%s\t%v\t%v\t%v\t%d\t%d\t%.0f%%/%.0f%%\t%s\n", r.ID, yes(r.ValidFirstTry), yes(r.Valid), yes(r.TestsPassed),
			r.PolicyViolations, r.Rounds, 100*r.ConnectorRecall, 100*r.StepRecall, note)
	}
	_ = tw.Flush()
	fmt.Fprintf(w, "\n%s %s, %d cases (%d errors): valid on first try %.0f%%, valid %.0f%%, tests pass %.0f%%, valid and passing on first try %.0f%%; %d policy violations; connector recall %.0f%%, step recall %.0f%%; %d tokens\n",
		rep.Provider, rep.Model, rep.Cases, rep.Errors, 100*rep.ValidFirstTry, 100*rep.Valid, 100*rep.TestPass, 100*rep.FirstTryAndTests,
		rep.PolicyViolations, 100*rep.ConnectorRecall, 100*rep.StepRecall, rep.Tokens)
}

func yes(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}
