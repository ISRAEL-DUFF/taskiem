package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/israel-duff/taskiem/engine/ai"
	"github.com/israel-duff/taskiem/engine/ai/builder"
	"github.com/israel-duff/taskiem/engine/connector"
	"github.com/israel-duff/taskiem/engine/wd"
	"github.com/israel-duff/taskiem/engine/wdcheck"
	"github.com/israel-duff/taskiem/templates"
)

// Statuses of an attempt. Only ok and refused attempts are scored:
// errors, timeouts, truncations and a model other than the one asked for
// are plumbing, counted apart and left out of every rate (eval-audit:
// infra failures are not model failures).
const (
	statusOK            = "ok"
	statusRefused       = "refused"
	statusError         = "error"
	statusTimeout       = "timeout"
	statusTruncated     = "truncated"
	statusModelMismatch = "model_mismatch"
)

// Result is one attempt at one case.
type Result struct {
	ID         string   `json:"id"`
	Rep        int      `json:"rep"`
	Tags       []string `json:"tags,omitempty"`
	Difficulty string   `json:"difficulty,omitempty"`
	Status     string   `json:"status"`
	Error      string   `json:"error,omitempty"`
	// What the pipeline itself reports.
	ValidFirstTry    bool `json:"valid_first_try"`
	Valid            bool `json:"valid"`
	TestsPassed      bool `json:"tests_passed"`
	PolicyViolations int  `json:"policy_violations"`
	Rounds           int  `json:"rounds"`
	// Gate is the G3 measure: valid and test-passing on the first try.
	Gate bool `json:"gate"`
	// What the grader checks against the case.
	ConnectorRecall float64         `json:"connector_recall"`
	StepRecall      float64         `json:"step_recall"`
	TriggerOK       bool            `json:"trigger_ok"`
	Properties      map[string]bool `json:"properties,omitempty"`
	// Requirements: every expected connector, step type, trigger and
	// property is there. Strict: gate, requirements and no policy
	// violation.
	Requirements bool     `json:"requirements"`
	Strict       bool     `json:"strict"`
	Missing      []string `json:"missing,omitempty"`
	Problems     []string `json:"problems,omitempty"`
	Template     string   `json:"template,omitempty"`
	// Cost: tokens from the provider's usage, the model that answered.
	Model   string   `json:"model,omitempty"`
	Usage   ai.Usage `json:"usage"`
	CostUSD *float64 `json:"cost_usd,omitempty"`
	Judge   *Verdict `json:"judge,omitempty"`
	Seconds float64  `json:"seconds,omitempty"`
}

func (r *Result) scored() bool { return r.Status == statusOK || r.Status == statusRefused }

// evalOptions configure a run.
type evalOptions struct {
	Reps        int
	Parallel    int
	CaseTimeout time.Duration
	// WantModel, when set, is the model asked for: an answer from another
	// model is a model_mismatch, not a score.
	WantModel string
	Judge     *Judge
	Prices    map[string]Price
}

func newBuilder(prov ai.Provider, reg *connector.Registry) *builder.Builder {
	return &builder.Builder{
		Provider:   prov,
		Connectors: reg,
		Templates:  templates.Default(),
		Validate: func(doc []byte) []builder.Problem {
			var out []builder.Problem
			for _, p := range wdcheck.Check(doc, reg) {
				out = append(out, builder.Problem{Path: p.Path, Message: p.Message})
			}
			return out
		},
	}
}

// evaluate runs every case Reps times through the real builder pipeline
// and grades each attempt. Results come back in case order.
func evaluate(ctx context.Context, prov ai.Provider, reg *connector.Registry, cases []Case, o evalOptions) []Result {
	reps := max(o.Reps, 1)
	par := max(o.Parallel, 1)
	results := make([]Result, len(cases)*reps)
	sem := make(chan struct{}, par)
	var wg sync.WaitGroup
	for i, c := range cases {
		for rep := range reps {
			wg.Add(1)
			sem <- struct{}{}
			go func(slot int, c Case, rep int) {
				defer wg.Done()
				defer func() { <-sem }()
				results[slot] = runCase(ctx, prov, reg, c, rep, o)
			}(i*reps+rep, c, rep)
		}
	}
	wg.Wait()
	return results
}

func runCase(ctx context.Context, prov ai.Provider, reg *connector.Registry, c Case, rep int, o evalOptions) Result {
	r := Result{ID: c.ID, Rep: rep, Tags: c.Tags, Difficulty: c.Difficulty, Status: statusOK}
	cctx := ctx
	if o.CaseTimeout > 0 {
		var cancel context.CancelFunc
		cctx, cancel = context.WithTimeout(ctx, o.CaseTimeout)
		defer cancel()
	}
	start := time.Now()
	p, err := newBuilder(prov, reg).Build(cctx, builder.Request{Goal: c.Request})
	r.Seconds = time.Since(start).Seconds()
	if p != nil {
		r.Model, r.Usage, r.Rounds = p.Model, p.Usage, p.Rounds
		r.CostUSD = cost(o.Prices, p.Model, p.Usage)
	}
	switch {
	case errors.Is(err, builder.ErrRefused):
		r.Status, r.Error = statusRefused, err.Error()
		return r
	case err != nil && errors.Is(cctx.Err(), context.DeadlineExceeded):
		r.Status, r.Error = statusTimeout, err.Error()
		return r
	case err != nil:
		r.Status, r.Error = statusError, err.Error()
		return r
	}
	if o.WantModel != "" && p.Model != "" && !sameModel(o.WantModel, p.Model) {
		r.Status, r.Error = statusModelMismatch, fmt.Sprintf("asked for %s, answered by %s", o.WantModel, p.Model)
		return r
	}
	for _, w := range p.Warnings {
		if w.Kind == "truncated" {
			r.Status, r.Error = statusTruncated, w.Message
		}
	}
	grade(&r, c, p, reg)
	if o.Judge != nil && r.Status == statusOK {
		r.Judge = o.Judge.Grade(ctx, c, p, reg)
	}
	return r
}

// sameModel accepts a dated snapshot of the alias asked for.
func sameModel(want, got string) bool {
	return got == want || strings.HasPrefix(got, want+"-")
}

// grade fills the deterministic checks.
func grade(r *Result, c Case, p *builder.Proposal, reg connector.Lookup) {
	r.ValidFirstTry, r.Valid, r.TestsPassed = p.ValidFirstTry, p.Valid(), p.TestsPassed()
	r.PolicyViolations = p.PolicyViolations()
	r.Gate = r.ValidFirstTry && r.TestsPassed
	for _, pr := range p.Problems {
		r.Problems = append(r.Problems, pr.Path+": "+pr.Message)
	}
	if p.Template != nil {
		r.Template = p.Template.ID
	}
	def, err := wd.Load(p.Definition)
	if err != nil {
		r.Missing = append(r.Missing, "a loadable definition")
		return
	}
	usedC, usedS := map[string]bool{}, map[string]bool{}
	walk(def.Steps, func(st *wd.Step) {
		usedS[st.Type] = true
		if st.Connector != "" {
			usedC[st.Connector] = true
		}
	})
	if ref, ok := def.Trigger.Config["connector"].(string); ok {
		usedC[ref] = true
	}
	var missC, missS []string
	r.ConnectorRecall, missC = coverage(c.Expect.Connectors, usedC)
	r.StepRecall, missS = coverage(c.Expect.Steps, usedS)
	r.Missing = append(r.Missing, missC...)
	r.Missing = append(r.Missing, missS...)
	r.TriggerOK = c.Expect.Trigger == "" || def.Trigger.Type == c.Expect.Trigger
	if !r.TriggerOK {
		r.Missing = append(r.Missing, "trigger "+c.Expect.Trigger)
	}
	props, why := checkProperties(def, reg, c.Expect.Properties)
	r.Properties = props
	r.Missing = append(r.Missing, why...)
	propsOK := true
	for _, ok := range props {
		propsOK = propsOK && ok
	}
	r.Requirements = r.Valid && len(missC) == 0 && len(missS) == 0 && r.TriggerOK && propsOK
	r.Strict = r.Gate && r.Requirements && r.PolicyViolations == 0
}

// coverage is the share of want that used has, and what it lacks.
//
// (An earlier version returned a slice appended to by a closure that was
// called, and inlined, twice in the same return statement. Built with Go
// 1.26.0, the binary corrupted its heap and crashed in the GC on every
// full run; `go build -gcflags=-m=2` showed both inlined calls sharing one
// result temporary. A plain function fixed it; a minimal program did not
// reproduce the crash. Keep this a plain function.)
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
