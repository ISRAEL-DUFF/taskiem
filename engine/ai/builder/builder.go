// Package builder is the AI workflow builder (spec 12.1): from a goal, it
// retrieves context, has the model draft a definition under a structured
// output schema, validates the draft with the same checks as publishing
// plus policy rules, feeds problems back for up to three corrections,
// dry-runs the result with mocked connectors, and returns a proposal for a
// person to review.
//
// A proposal is only ever a proposal. The builder holds no database, no
// vault and no principal: it cannot save, publish, approve, read a secret
// or call a provider. Saving a proposal as a draft is the API's job, under
// the person's own permissions.
package builder

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/israel-duff/taskiem/engine/ai"
	"github.com/israel-duff/taskiem/engine/connector"
	"github.com/israel-duff/taskiem/engine/expansion"
	"github.com/israel-duff/taskiem/engine/wd"
	"github.com/israel-duff/taskiem/engine/wdtest"
	"github.com/israel-duff/taskiem/templates"
)

// Problem is a validation failure: the draft would not publish.
type Problem struct {
	Path    string `json:"path"`
	Message string `json:"message"`
}

// Warning is something the reviewer should know: a policy rule the draft
// breaks, a failing dry-run test, a correction loop cut short.
type Warning struct {
	Kind    string `json:"kind"` // policy | test | budget | refusal | truncated | table
	Rule    string `json:"rule,omitempty"`
	Path    string `json:"path,omitempty"`
	Message string `json:"message"`
}

// Request is one build.
type Request struct {
	Goal string
	// Base is the definition to modify, when changing an existing workflow.
	Base        json.RawMessage
	Environment string
	Context     Context
	// Language names the language the goal is written in ("Yoruba"), when
	// it is not English: the model is told, so it reads the goal as such
	// (spec 11.6).
	Language string
}

// Interaction is one model call, as recorded for audit: the request after
// redaction (what left the process), the response, and what came of it.
type Interaction struct {
	Round    int
	Kind     string // draft | correct
	Request  ai.Request
	Response *ai.Response
	// SystemDigest identifies the stable system prompt (it is large and
	// identical across calls, so it is recorded by digest).
	SystemDigest string
	// Outcome is what the builder did with the answer: valid, problems,
	// unparseable, refused, truncated, error.
	Outcome string
	Err     string
}

// Recorder keeps interactions (the API writes them to ai_interactions).
// A recording failure stops the build: nothing unlogged is acted on.
type Recorder interface {
	Record(ctx context.Context, it Interaction) error
}

// Meter enforces a budget before each call: it returns
// ai.ErrBudgetExhausted when the tenant's monthly budget is spent.
type Meter interface {
	Allow(ctx context.Context) error
}

// Builder runs the pipeline.
type Builder struct {
	Provider ai.Provider
	// Connectors are the tenant's connectors; the builder sees their
	// manifests only (ManifestOnly).
	Connectors connector.Lookup
	// Validate runs the platform's publishing checks (wdcheck). Nil runs
	// the wd/v1 contract checks only.
	Validate func(doc []byte) []Problem
	// CheckTable runs the platform's checks on a table a draft proposes
	// (names, types, the column limit). Nil runs the builder's own.
	CheckTable func(TableSpec) error
	Meter      Meter
	Recorder   Recorder
	// Progress reports stages: context, draft, validate, correct N, dry_run.
	Progress func(stage string)
	// MaxCorrections bounds the self-correction loop (default 3).
	MaxCorrections int
	MaxTokens      int
	Effort         string
	// MaxConnectors bounds how many connectors' schemas a prompt carries.
	MaxConnectors int
	// Templates is the SME template library offered as starting points
	// (retrieval context: templates first, then free drafting). Nil offers
	// none.
	Templates *templates.Library
	// Find finds a template step's template beyond the library: the
	// business's callable workflows (decision 0048). Nil is the library.
	Find expansion.Templates
}

// TemplateUse is the template a draft started from.
type TemplateUse struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	// Instantiated: the builder filled the template itself (the model
	// gave parameter values, not a workflow).
	Instantiated bool `json:"instantiated"`
	// Params are the values taken from the goal, checked against the
	// template; Missing are required parameters the goal did not state,
	// which the draft holds the template's example values for until a
	// person supplies them.
	Params  map[string]any `json:"params"`
	Missing []string       `json:"missing"`
}

// Proposal is the review a person sees.
type Proposal struct {
	Definition  json.RawMessage `json:"definition,omitempty"`
	Summary     string          `json:"summary"`
	Assumptions []string        `json:"assumptions"`
	// Problems are publishing checks the final draft still fails (empty:
	// it would publish).
	Problems []Problem    `json:"problems"`
	Warnings []Warning    `json:"warnings"`
	Tests    *TestFile    `json:"tests,omitempty"`
	Results  []TestResult `json:"results"`
	// Rounds is the number of model calls; ValidFirstTry is whether the
	// first draft passed every check.
	Rounds        int      `json:"rounds"`
	ValidFirstTry bool     `json:"valid_first_try"`
	Connectors    []string `json:"connectors"` // retrieved for the prompt
	// TemplatesOffered are the starting templates the prompt carried;
	// Template is the one the draft started from, if any.
	TemplatesOffered []string     `json:"templates_offered"`
	Template         *TemplateUse `json:"template,omitempty"`
	// Tables are the new data tables the draft uses, made when it is saved
	// (the person may change them first).
	Tables []TableSpec `json:"tables"`
	// Needs are what the draft needs before it can run (connections,
	// variables, secrets, tables, roles), found by the platform's
	// readiness check, each something the review offers to fix.
	Needs []Need   `json:"needs"`
	Usage ai.Usage `json:"usage"`
	Model string   `json:"model"`
}

// Need is one thing a workflow needs before it can run where it is
// published (no-technical-steps N3).
type Need struct {
	// Kind is connection, variable, secret, table, role, or field (a form
	// field asking for a chat id: problem chat_id).
	Kind string `json:"kind"`
	// Name is the connection's, variable's, secret's, table's or role's
	// name; "" for a connection a step does not name (the only one of its
	// service is used).
	Name string `json:"name"`
	// Connector and Service are a connection's connector id and its
	// display name.
	Connector string `json:"connector,omitempty"`
	Service   string `json:"service,omitempty"`
	// Problem is missing; for a connection also expired, disabled or
	// ambiguous; for a role, nobody (no member holds it: owners decide).
	Problem     string     `json:"problem"`
	Environment string     `json:"environment"`
	Steps       []NeedStep `json:"steps"`
	// Blocking needs stop a publish: a run would fail at once.
	Blocking bool `json:"blocking"`
	// Message says it in plain words.
	Message string `json:"message"`
	// Table is a missing table as the workflow uses it, for making it in
	// one tap (TableFor); absent when its columns cannot be told.
	Table *TableSpec `json:"table,omitempty"`
	// Capability, for a connection a capability step needs (decision
	// 0044): what its rail must do, and Rails the connector ids that can.
	// Problem may then also be cannot (the connection's rail cannot).
	Capability string   `json:"capability,omitempty"`
	Rails      []string `json:"rails,omitempty"`
}

// NeedStep is a step that has a need, by id and name.
type NeedStep struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// Valid reports whether the proposal passes the publishing checks.
func (p *Proposal) Valid() bool { return len(p.Definition) > 0 && len(p.Problems) == 0 }

// TestsPassed reports whether every dry-run case passed.
func (p *Proposal) TestsPassed() bool {
	for _, r := range p.Results {
		if !r.Passed {
			return false
		}
	}
	return len(p.Results) > 0
}

// PolicyViolations counts policy warnings.
func (p *Proposal) PolicyViolations() int {
	n := 0
	for _, w := range p.Warnings {
		if w.Kind == "policy" {
			n++
		}
	}
	return n
}

// ErrRefused means the model declined the request.
var ErrRefused = errors.New("the model declined this request")

func (b *Builder) progress(s string) {
	if b.Progress != nil {
		b.Progress(s)
	}
}

// Build runs the pipeline. It returns an error only when there is nothing
// to review: the budget was spent before the first draft, the model
// declined it, the provider failed, or recording failed.
func (b *Builder) Build(ctx context.Context, req Request) (*Proposal, error) {
	if b.Provider == nil {
		return nil, ai.ErrNotConfigured
	}
	if strings.TrimSpace(req.Goal) == "" {
		return nil, errors.New("a goal is required")
	}
	provider := ai.Redacted(b.Provider) // every prompt is redacted before it leaves
	reg := ManifestOnly(b.Connectors)
	maxCorr := b.MaxCorrections
	if maxCorr <= 0 {
		maxCorr = 3
	}
	effort := b.Effort
	if effort == "" {
		effort = ai.DefaultEffort
	}
	maxTokens := b.MaxTokens
	if maxTokens <= 0 {
		maxTokens = ai.DefaultMaxTokens
	}

	b.progress("context")
	system := systemBlocks(Catalogue(reg))
	ctxDoc, refs, offered := b.contextDoc(req, reg)
	messages := []ai.Message{{Role: "user", Text: userPrompt(req.Goal, req.Language, req.Base, ctxDoc)}}
	digest := sha256.Sum256([]byte(system[0].Text + "\x00" + system[1].Text))
	sysDigest := hex.EncodeToString(digest[:])

	p := &Proposal{Connectors: refs, TemplatesOffered: offered, Problems: []Problem{}, Warnings: []Warning{}, Results: []TestResult{}, Assumptions: []string{},
		Tables: []TableSpec{}, Needs: []Need{}}
	var use *TemplateUse
	var env *Envelope
	var doc []byte
	var findings []Warning
	for round := 1; round <= maxCorr+1; round++ {
		kind := "draft"
		if round > 1 {
			kind = "correct"
			b.progress(fmt.Sprintf("correct %d", round-1))
		} else {
			b.progress("draft")
		}
		if b.Meter != nil {
			if err := b.Meter.Allow(ctx); err != nil {
				if round == 1 {
					return nil, err
				}
				p.Warnings = append(p.Warnings, Warning{Kind: "budget", Message: "the monthly AI budget ran out during self-correction; the last draft is shown as it stands"})
				break
			}
		}
		areq := ai.Request{System: system, Messages: messages, Schema: envelopeSchema, MaxTokens: maxTokens, Effort: effort}
		resp, err := provider.Complete(ctx, areq)
		it := Interaction{Round: round, Kind: kind, Request: ai.RedactRequest(areq), Response: resp, SystemDigest: sysDigest}
		if err != nil {
			it.Outcome, it.Err = "error", err.Error()
			if rerr := b.record(ctx, it); rerr != nil {
				return nil, rerr
			}
			if round == 1 || ctx.Err() != nil {
				return nil, err
			}
			p.Warnings = append(p.Warnings, Warning{Kind: "error", Message: "the model could not be reached during self-correction; the last draft is shown as it stands"})
			break
		}
		p.Rounds = round
		p.Usage = p.Usage.Add(resp.Usage)
		p.Model = resp.Model
		switch resp.StopReason {
		case ai.StopRefusal:
			it.Outcome = "refused"
			if rerr := b.record(ctx, it); rerr != nil {
				return nil, rerr
			}
			if env == nil {
				return p, ErrRefused
			}
			p.Warnings = append(p.Warnings, Warning{Kind: "refusal", Message: "the model declined to continue correcting; the last draft is shown as it stands"})
		case ai.StopMaxTokens:
			it.Outcome = "truncated"
			if rerr := b.record(ctx, it); rerr != nil {
				return nil, rerr
			}
			p.Warnings = append(p.Warnings, Warning{Kind: "truncated", Message: "the model's answer was cut off at the token limit"})
		}
		if resp.StopReason == ai.StopRefusal || resp.StopReason == ai.StopMaxTokens {
			break
		}

		var problems []Problem
		next, parseErr := parseEnvelope(resp)
		if parseErr != nil {
			problems = []Problem{{Path: "/", Message: parseErr.Error()}}
		} else {
			env = next
			doc = []byte(next.Workflow)
			use = nil
			if next.Template != "" {
				var tplProblems []Problem
				use, doc, tplProblems = b.fromTemplate(next, offered)
				problems = append(problems, tplProblems...)
			}
			b.progress("validate")
			if len(problems) == 0 {
				problems = b.validate(doc)
			}
			findings = nil
			if len(problems) == 0 {
				if def, err := wd.Load(doc); err == nil {
					findings = PolicyFindingsWith(def, reg, b.Find)
				}
			}
		}
		if parseErr != nil {
			it.Outcome = "unparseable"
		} else if len(problems)+len(findings) == 0 {
			it.Outcome = "valid"
		} else {
			it.Outcome = "problems"
		}
		if err := b.record(ctx, it); err != nil {
			return nil, err
		}
		if round == 1 {
			p.ValidFirstTry = parseErr == nil && len(problems) == 0
		}
		p.Problems = problems
		if len(problems)+len(findings) == 0 || round == maxCorr+1 {
			break
		}
		// Policy findings are fed back with the problems: the model may fix
		// them (an approval step), and any it leaves become warnings.
		fb := problems
		for _, f := range findings {
			fb = append(fb, Problem{Path: f.Path, Message: "policy " + f.Rule + ": " + f.Message})
		}
		messages = append(messages, ai.Message{Role: "assistant", Text: resp.Text}, ai.Message{Role: "user", Text: feedback(fb)})
	}
	if env == nil {
		if len(p.Problems) == 0 {
			p.Problems = []Problem{{Path: "/", Message: "the model returned no usable draft"}}
		}
		return p, nil
	}
	p.Summary = env.Summary
	if env.Assumptions != nil {
		p.Assumptions = env.Assumptions
	}
	p.Definition = json.RawMessage(doc)
	p.Template = use
	if use != nil && len(use.Missing) > 0 {
		p.Warnings = append(p.Warnings, Warning{Kind: "template", Message: fmt.Sprintf("the goal does not say %s; the draft holds the template's example values until you set them",
			strings.Join(use.Missing, ", "))})
	}
	if p.Problems == nil {
		p.Problems = []Problem{}
	}
	p.Warnings = append(p.Warnings, findings...)
	var tableWarnings []Warning
	p.Tables, tableWarnings = reviewTables(env.Tables, req.Context.Tables, doc, b.CheckTable)
	p.Warnings = append(p.Warnings, tableWarnings...)

	// Dry run, when the definition loads.
	if def, err := wd.Load(doc); err == nil && def != nil {
		b.progress("dry_run")
		var proposed []wdtest.Case
		for i, t := range env.Tests {
			if i == 3 {
				break
			}
			c, err := parseCase(t.Name, t.Case)
			if err != nil {
				p.Warnings = append(p.Warnings, Warning{Kind: "test", Message: fmt.Sprintf("proposed test %q is not a wd-test/v1 case: %v", t.Name, err)})
				continue
			}
			proposed = append(proposed, c)
		}
		policies := map[string]json.RawMessage{}
		for _, pol := range req.Context.Policies {
			policies[pol.Name] = pol.Document
		}
		file, results, err := DryRunWith(doc, reg, policies, proposed, b.Find)
		if err == nil {
			p.Tests, p.Results = &file, results
			for _, r := range results {
				if !r.Passed {
					p.Warnings = append(p.Warnings, Warning{Kind: "test", Message: fmt.Sprintf("dry run %q: %s", r.Name, strings.Join(r.Failures, "; "))})
				}
			}
		}
	}
	return p, nil
}

func (b *Builder) record(ctx context.Context, it Interaction) error {
	if b.Recorder == nil {
		return nil
	}
	if err := b.Recorder.Record(ctx, it); err != nil {
		return fmt.Errorf("recording the AI interaction: %w", err)
	}
	return nil
}

func (b *Builder) validate(doc []byte) []Problem {
	if b.Validate != nil {
		return b.Validate(doc)
	}
	var out []Problem
	for _, p := range wd.Validate(doc) {
		out = append(out, Problem{Path: p.Path, Message: p.Message})
	}
	return out
}

// parseEnvelope reads a structured answer.
func parseEnvelope(resp *ai.Response) (*Envelope, error) {
	raw := resp.JSON
	if raw == nil {
		raw = json.RawMessage(strings.TrimSpace(resp.Text))
	}
	var env Envelope
	dec := json.NewDecoder(bytes.NewReader(raw))
	if err := dec.Decode(&env); err != nil {
		return nil, fmt.Errorf("the answer is not the expected JSON: %w", err)
	}
	w := strings.TrimSpace(env.Workflow)
	if w == "" && env.Template != "" {
		env.Workflow = ""
		return &env, nil
	}
	if w == "" {
		return nil, errors.New("the answer has no workflow")
	}
	var buf bytes.Buffer
	if err := json.Compact(&buf, []byte(w)); err != nil {
		return nil, fmt.Errorf("the workflow field is not a JSON document: %w", err)
	}
	env.Workflow = buf.String()
	return &env, nil
}

// fromTemplate resolves a draft that names a starting template: the
// template must be one the prompt offered; with no workflow of its own,
// the template is instantiated with the parameter values the model read
// from the goal (values that do not fit are dropped and asked for like
// missing ones), required parameters still missing holding the template's
// examples.
func (b *Builder) fromTemplate(env *Envelope, offered []string) (*TemplateUse, []byte, []Problem) {
	doc := []byte(env.Workflow)
	if b.Templates == nil || !slices.Contains(offered, env.Template) {
		if env.Workflow == "" {
			return nil, doc, []Problem{{Path: "/template", Message: fmt.Sprintf("%q is not one of the starting templates offered; write the workflow instead", env.Template)}}
		}
		return nil, doc, nil // a template named in passing; the workflow stands on its own
	}
	tpl, _ := b.Templates.Get(env.Template)
	use := &TemplateUse{ID: tpl.ID, Title: tpl.Title, Params: map[string]any{}, Missing: []string{}}
	if env.Workflow != "" {
		return use, doc, nil
	}
	use.Instantiated = true
	values := map[string]any{}
	if strings.TrimSpace(env.TemplateParams) != "" {
		if err := json.Unmarshal([]byte(env.TemplateParams), &values); err != nil {
			return use, nil, []Problem{{Path: "/template_params", Message: "template_params is not a JSON object: " + err.Error()}}
		}
	}
	// Each value is checked on its own: one that does not fit is asked for
	// again rather than failing the draft.
	for k, v := range values {
		p, ok := tpl.Param(k)
		if !ok {
			continue
		}
		if c, err := p.Coerce(v); err == nil {
			use.Params[k] = c
		}
	}
	resolved, missing, _ := tpl.Resolve(use.Params)
	use.Missing = append(use.Missing, missing...)
	fill := map[string]any{}
	for k, v := range resolved {
		fill[k] = v
	}
	for _, m := range missing {
		p, _ := tpl.Param(m)
		fill[m] = p.Example
	}
	out, err := tpl.Instantiate(fill, "")
	if err != nil {
		return use, nil, []Problem{{Path: "/template_params", Message: err.Error()}}
	}
	return use, out, nil
}

// contextDoc assembles the per-request context.
func (b *Builder) contextDoc(req Request, reg connector.Lookup) (map[string]any, []string, []string) {
	n := b.MaxConnectors
	if n <= 0 {
		n = 6
	}
	ranked := Rank(req.Goal, reg, req.Context.Connections)
	// A workflow being modified keeps its own connectors in view.
	var refs []string
	seen := map[string]bool{}
	if len(req.Base) > 0 {
		if def, err := wd.Load(req.Base); err == nil {
			var walk func(steps []*wd.Step)
			walk = func(steps []*wd.Step) {
				for _, st := range steps {
					if st.Type == "connector" && !seen[st.Connector] {
						if _, ok := reg.Get(st.Connector); ok {
							seen[st.Connector] = true
							refs = append(refs, st.Connector)
						}
					}
					for _, sub := range st.Children() {
						walk(sub)
					}
				}
			}
			walk(def.Steps)
		}
	}
	for _, r := range ranked {
		if len(refs) >= n {
			break
		}
		if !seen[r.Ref] {
			seen[r.Ref] = true
			refs = append(refs, r.Ref)
		}
	}
	offered := []string{}
	var tpls []map[string]any
	if b.Templates != nil && len(req.Base) == 0 {
		goal := req.Goal
		if req.Context.NamesLinked(goal) {
			// A business linked on Taskiem, named: the link templates
			// (business links L6) are offered as for "on Taskiem".
			goal += " on Taskiem"
		}
		for i, m := range b.Templates.Match(goal, 2) {
			tc := TemplateContext(m.Template)
			tc["match"] = map[string]any{"score": m.Score, "matched_words": m.Matched}
			tpls = append(tpls, tc)
			offered = append(offered, m.Template.ID)
			// The best template brings its connectors' schemas, after the
			// ranked ones: "bill a customer" names no connector, but
			// send-invoice is written with invoices@1.
			for _, ref := range m.Template.Connectors {
				if _, ok := reg.Get(ref); ok && i == 0 && !seen[ref] {
					seen[ref] = true
					refs = append(refs, ref)
				}
			}
		}
	}
	var details []map[string]any
	for _, ref := range refs {
		if c, ok := reg.Get(ref); ok {
			details = append(details, detail(c))
		}
	}
	doc := map[string]any{
		"relevant_connectors": details,
		"connections":         nonNil(req.Context.Connections),
		"data_tables":         nonNil(req.Context.Tables),
		"variables":           nonNil(req.Context.Variables),
		"approval_policies":   nonNil(req.Context.Policies),
		"roles":               nonNil(req.Context.Roles),
		"members":             req.Context.Members,
		"similar_workflows":   nonNil(similar(req.Goal, req.Context.Workflows, 3)),
	}
	if req.Environment != "" {
		doc["environment"] = req.Environment
	}
	if refs == nil {
		refs = []string{}
	}
	if b.Templates != nil && len(req.Base) == 0 {
		doc["starting_templates"] = nonNil(tpls)
	}
	if len(req.Context.CallableWorkflows) > 0 {
		doc["callable_workflows"] = req.Context.CallableWorkflows
	}
	if len(req.Context.Startable) > 0 {
		doc["startable_workflows"] = req.Context.Startable
	}
	if req.Context.AISteps {
		doc["ai_steps"] = true
	}
	return doc, refs, offered
}

// TemplateContext is how a template appears in a prompt: what it does,
// its parameters, the variables it reads, and its definition with
// {{markers}}.
func TemplateContext(t *templates.Template) map[string]any {
	params := make([]map[string]any, 0, len(t.Params))
	for _, p := range t.Params {
		x := map[string]any{"name": p.Name, "type": p.Type, "title": p.Title, "description": p.Description, "required": p.Required}
		if p.Default != nil {
			x["default"] = p.Default
		}
		if len(p.Enum) > 0 {
			x["enum"] = p.Enum
		}
		params = append(params, x)
	}
	vars := make([]string, 0, len(t.Variables))
	for _, v := range t.Variables {
		vars = append(vars, v.Name)
	}
	out := map[string]any{"id": t.ID, "title": t.Title, "description": t.Description, "connectors": t.Connectors,
		"variables": vars, "params": params, "definition": t.Definition}
	if t.Callable {
		// It can also be a template step (rule 18): what that step gives
		// back.
		outs := make([]map[string]any, 0, len(t.Outputs))
		for _, o := range t.Outputs {
			outs = append(outs, map[string]any{"name": o.Name, "type": o.Type, "description": o.Description})
		}
		out["callable"] = map[string]any{"outputs": outs}
	}
	return out
}

func nonNil[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}
