package wd

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/israel-duff/taskiem/engine/expr"
)

// Definition is a parsed, validated wd/v1 document.
type Definition struct {
	Schema      string   `json:"schema"`
	ID          string   `json:"id"`
	Version     int      `json:"version"`
	Name        string   `json:"name"`
	Description string   `json:"description,omitempty"`
	Trigger     Trigger  `json:"trigger"`
	Steps       []*Step  `json:"steps"`
	Settings    Settings `json:"settings"`
	RawInputs   *struct {
		Schema json.RawMessage `json:"schema"`
	} `json:"inputs,omitempty"`
	RawTypes map[string]json.RawMessage `json:"types,omitempty"`

	byID   map[string]*Step
	parent map[string]*Step // nested step id -> enclosing control (or on_error owner) step
}

type Trigger struct {
	Type   string          `json:"type"`
	Config map[string]any  `json:"-"`
	RawCfg json.RawMessage `json:"config,omitempty"`
}

type Settings struct {
	Timeout        string `json:"timeout,omitempty"`
	ConcurrencyKey string `json:"concurrency_key,omitempty"`
	MaxConcurrency int    `json:"max_concurrency,omitempty"`
	Retention      string `json:"retention,omitempty"`
}

type Retry struct {
	Max         int    `json:"max"`
	Backoff     string `json:"backoff,omitempty"`
	Initial     string `json:"initial,omitempty"`
	MaxDelay    string `json:"max_delay,omitempty"`
	MaxDuration string `json:"max_duration,omitempty"`
}

type SubFlow struct {
	Steps []*Step `json:"steps"`
}

type Effect struct {
	IdempotencySeed string `json:"idempotency_seed,omitempty"`
}

type Compensate struct {
	Action string          `json:"action"`
	Input  map[string]any  `json:"-"`
	RawIn  json.RawMessage `json:"input,omitempty"`
}

// Step is one node. Type-specific settings are parsed into the matching field.
type Step struct {
	ID          string          `json:"id"`
	Type        string          `json:"type"`
	Name        string          `json:"name,omitempty"`
	Description string          `json:"description,omitempty"`
	Needs       []string        `json:"needs,omitempty"`
	When        string          `json:"when,omitempty"`
	Retry       *Retry          `json:"retry,omitempty"`
	Timeout     string          `json:"timeout,omitempty"`
	OnError     *SubFlow        `json:"on_error,omitempty"`
	Connector   string          `json:"connector,omitempty"`
	Action      string          `json:"action,omitempty"`
	Connection  string          `json:"connection,omitempty"`
	Effect      *Effect         `json:"effect,omitempty"`
	Compensate  *Compensate     `json:"compensate,omitempty"`
	RawInput    json.RawMessage `json:"input,omitempty"`
	RawConfig   json.RawMessage `json:"config,omitempty"`

	Input     map[string]any   `json:"-"`
	HTTP      *HTTPConfig      `json:"-"`
	Code      *CodeConfig      `json:"-"`
	Branch    *BranchConfig    `json:"-"`
	Parallel  *ParallelConfig  `json:"-"`
	Foreach   *ForeachConfig   `json:"-"`
	Wait      *WaitConfig      `json:"-"`
	Signal    *SignalConfig    `json:"-"`
	Approval  *ApprovalConfig  `json:"-"`
	Transform *TransformConfig `json:"-"`
}

type HTTPConfig struct {
	Method            string         `json:"method"`
	URL               string         `json:"url"`
	Headers           map[string]any `json:"headers,omitempty"`
	Query             map[string]any `json:"query,omitempty"`
	Body              any            `json:"body,omitempty"`
	Class             string         `json:"class,omitempty"`
	IdempotencyHeader string         `json:"idempotency_header,omitempty"`
}

type CodeConfig struct {
	Language string      `json:"language"`
	Source   string      `json:"source,omitempty"`
	Module   string      `json:"module,omitempty"`
	Secrets  []string    `json:"secrets,omitempty"`
	Limits   *CodeLimits `json:"limits,omitempty"`
}

type CodeLimits struct {
	MemoryMB int    `json:"memory_mb,omitempty"`
	CPU      string `json:"cpu,omitempty"`
}

type BranchPath struct {
	Name  string  `json:"name"`
	When  string  `json:"when"`
	Steps []*Step `json:"steps"`
}

type BranchConfig struct {
	Paths   []BranchPath `json:"paths"`
	Default *SubFlow     `json:"default,omitempty"`
}

type ParallelConfig struct {
	Branches []struct {
		Name  string  `json:"name"`
		Steps []*Step `json:"steps"`
	} `json:"branches"`
	Join           string `json:"join,omitempty"`
	MaxConcurrency int    `json:"max_concurrency,omitempty"`
}

type ForeachConfig struct {
	Items          string  `json:"items"`
	MaxConcurrency int     `json:"max_concurrency,omitempty"`
	MaxItems       int     `json:"max_items,omitempty"`
	Steps          []*Step `json:"steps"`
}

type WaitConfig struct {
	Duration string `json:"duration,omitempty"`
	Until    string `json:"until,omitempty"`
}

type SignalConfig struct {
	Event       string `json:"event"`
	Correlation string `json:"correlation"`
	Timeout     string `json:"timeout,omitempty"`
}

type ApprovalConfig struct {
	Policy    string          `json:"policy,omitempty"`
	Role      string          `json:"role,omitempty"`
	Count     int             `json:"count,omitempty"`
	Timeout   string          `json:"timeout,omitempty"`
	OnTimeout string          `json:"on_timeout,omitempty"`
	Subject   map[string]any  `json:"-"`
	RawSubj   json.RawMessage `json:"subject,omitempty"`
}

type TransformConfig struct {
	Output any `json:"-"`
}

// Load validates a WD document and parses it.
func Load(doc []byte) (*Definition, error) {
	if probs := Validate(doc); len(probs) > 0 {
		msgs := make([]string, len(probs))
		for i, p := range probs {
			msgs[i] = p.String()
		}
		return nil, fmt.Errorf("wd: invalid definition:\n  %s", strings.Join(msgs, "\n  "))
	}
	return parse(doc)
}

func parse(doc []byte) (*Definition, error) {
	var d Definition
	if err := json.Unmarshal(doc, &d); err != nil {
		return nil, err
	}
	if len(d.Trigger.RawCfg) > 0 {
		v, err := expr.DecodeJSON(d.Trigger.RawCfg)
		if err != nil {
			return nil, err
		}
		d.Trigger.Config, _ = v.(map[string]any)
	}
	d.byID = map[string]*Step{}
	d.parent = map[string]*Step{}
	if err := d.index(d.Steps, nil); err != nil {
		return nil, err
	}
	return &d, nil
}

func (d *Definition) index(steps []*Step, parent *Step) error {
	for _, s := range steps {
		if err := s.parse(); err != nil {
			return fmt.Errorf("step %s: %w", s.ID, err)
		}
		d.byID[s.ID] = s
		d.parent[s.ID] = parent
		for _, sub := range s.Children() {
			if err := d.index(sub, s); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *Step) parse() error {
	if len(s.RawInput) > 0 {
		v, err := expr.DecodeJSON(s.RawInput)
		if err != nil {
			return err
		}
		s.Input, _ = v.(map[string]any)
	}
	if s.Compensate != nil && len(s.Compensate.RawIn) > 0 {
		v, err := expr.DecodeJSON(s.Compensate.RawIn)
		if err != nil {
			return err
		}
		s.Compensate.Input, _ = v.(map[string]any)
	}
	if len(s.RawConfig) == 0 {
		return nil
	}
	var target any
	switch s.Type {
	case "http":
		s.HTTP = &HTTPConfig{}
		target = s.HTTP
	case "code":
		s.Code = &CodeConfig{}
		target = s.Code
	case "branch":
		s.Branch = &BranchConfig{}
		target = s.Branch
	case "parallel":
		s.Parallel = &ParallelConfig{}
		target = s.Parallel
	case "foreach":
		s.Foreach = &ForeachConfig{}
		target = s.Foreach
	case "wait":
		s.Wait = &WaitConfig{}
		target = s.Wait
	case "signal":
		s.Signal = &SignalConfig{}
		target = s.Signal
	case "approval":
		s.Approval = &ApprovalConfig{}
		target = s.Approval
	case "transform":
		v, err := expr.DecodeJSON(s.RawConfig)
		if err != nil {
			return err
		}
		s.Transform = &TransformConfig{Output: v.(map[string]any)["output"]}
		return nil
	default:
		return nil
	}
	if err := json.Unmarshal(s.RawConfig, target); err != nil {
		return err
	}
	// Re-decode value-bearing fields so integers stay int64.
	raw, err := expr.DecodeJSON(s.RawConfig)
	if err != nil {
		return err
	}
	m, _ := raw.(map[string]any)
	switch s.Type {
	case "http":
		s.HTTP.Headers, _ = m["headers"].(map[string]any)
		s.HTTP.Query, _ = m["query"].(map[string]any)
		s.HTTP.Body = m["body"]
	case "approval":
		s.Approval.Subject, _ = m["subject"].(map[string]any)
	}
	return nil
}

// Children returns the nested scopes of a step, in definition order.
func (s *Step) Children() [][]*Step {
	var out [][]*Step
	if s.OnError != nil {
		out = append(out, s.OnError.Steps)
	}
	switch {
	case s.Branch != nil:
		for _, p := range s.Branch.Paths {
			out = append(out, p.Steps)
		}
		if s.Branch.Default != nil {
			out = append(out, s.Branch.Default.Steps)
		}
	case s.Parallel != nil:
		for _, b := range s.Parallel.Branches {
			out = append(out, b.Steps)
		}
	case s.Foreach != nil:
		out = append(out, s.Foreach.Steps)
	}
	return out
}

// Step returns the step with the given id, in any scope.
func (d *Definition) Step(id string) *Step { return d.byID[id] }

// Parent returns the control step enclosing id, or nil at top level.
func (d *Definition) Parent(id string) *Step { return d.parent[id] }

// IsWrite reports whether the step may have external side effects; for
// connector steps this needs the manifest, so it reports false here.
func (s *Step) Queue() string {
	switch s.Type {
	case "code":
		return "sandbox"
	case "ai":
		return "ai"
	}
	return "connector"
}

var durRe = regexp.MustCompile(`([0-9]+)(ms|s|m|h|d)`)

// ParseDuration parses a WD duration ("90s", "1h30m", "7d").
func ParseDuration(s string) (time.Duration, error) {
	if s == "" || !durFull.MatchString(s) {
		return 0, fmt.Errorf("invalid duration %q", s)
	}
	var total time.Duration
	for _, m := range durRe.FindAllStringSubmatch(s, -1) {
		n, err := strconv.ParseInt(m[1], 10, 64)
		if err != nil {
			return 0, err
		}
		unit := map[string]time.Duration{"ms": time.Millisecond, "s": time.Second, "m": time.Minute, "h": time.Hour, "d": 24 * time.Hour}[m[2]]
		total += time.Duration(n) * unit
	}
	return total, nil
}

var durFull = regexp.MustCompile(`^([0-9]+(ms|s|m|h|d))+$`)

// InputsSchema returns the inputs schema and the named types it may
// reference, as JSON-compatible values (for PII paths and validation).
func (d *Definition) InputsSchema() (schema any, types map[string]any) {
	types = map[string]any{}
	for k, raw := range d.RawTypes {
		if v, err := expr.DecodeJSON(raw); err == nil {
			types[k] = v
		}
	}
	if d.RawInputs != nil && len(d.RawInputs.Schema) > 0 {
		schema, _ = expr.DecodeJSON(d.RawInputs.Schema)
	}
	return schema, types
}
