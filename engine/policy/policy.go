// Package policy defines approval policies (spec 9.1): reusable objects an
// approval step names, holding ordered rules that choose who must approve,
// in which order, and with what assurance, from the data being approved.
//
//	{
//	  "rules": [
//	    {"when": "=subject.amount_kobo < 50000000", "levels": [{"role": "credit_officer"}]},
//	    {"when": "=subject.amount_kobo >= 50000000",
//	     "levels": [{"role": "credit_officer"}, {"role": "head_of_credit"}], "step_up": "totp"}
//	  ],
//	  "constraints": {"forbid_self_approval": true, "distinct_approvers": true},
//	  "timeout": "24h",
//	  "on_timeout": "escalate:head_of_operations"
//	}
//
// Policies are snapshotted into a run when it starts, so the orchestrator
// evaluates them deterministically and the history shows which version
// governed each decision.
package policy

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/israel-duff/taskiem/engine/expr"
	"github.com/israel-duff/taskiem/engine/wd"
)

// Policy is one version of an approval policy.
type Policy struct {
	Rules       []Rule      `json:"rules"`
	Constraints Constraints `json:"constraints"`
	Timeout     string      `json:"timeout,omitempty"`
	OnTimeout   string      `json:"on_timeout,omitempty"`
	Channels    []string    `json:"channels,omitempty"`
	Description string      `json:"description,omitempty"`
}

// Rule applies when its condition holds; the first matching rule wins.
type Rule struct {
	When   string  `json:"when,omitempty"` // CEL over subject; empty always matches
	Levels []Level `json:"levels"`
	// StepUp is the weakest second factor a decision under this rule takes:
	// "" (none), whatsapp_pin (the WhatsApp approval PIN, or stronger),
	// totp (an authenticator code or a passkey) or passkey.
	StepUp string `json:"step_up,omitempty"`
}

// Level is one stage of approval: count people holding role. Levels are
// approved in order.
type Level struct {
	Role  string `json:"role"`
	Count int    `json:"count,omitempty"` // default 1
}

// Constraints are separation-of-duties rules. Both default to true: a
// policy has to say so to relax them.
type Constraints struct {
	ForbidSelfApproval *bool `json:"forbid_self_approval,omitempty"`
	DistinctApprovers  *bool `json:"distinct_approvers,omitempty"`
}

// SelfApprovalForbidden reports whether makers are barred from approving.
func (c Constraints) SelfApprovalForbidden() bool {
	return c.ForbidSelfApproval == nil || *c.ForbidSelfApproval
}

// Distinct reports whether one person may approve only one level.
func (c Constraints) Distinct() bool { return c.DistinctApprovers == nil || *c.DistinctApprovers }

var (
	nameRe = regexp.MustCompile(`^[a-z][a-z0-9_]{0,62}$`)
	roleRe = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_.-]{0,62}$`)
	engine = expr.MustNewWithRoots("subject")
)

// ValidName reports whether s can name a policy.
func ValidName(s string) bool { return nameRe.MatchString(s) }

// Parse reads and validates a policy document.
func Parse(doc []byte) (*Policy, error) {
	dec := json.NewDecoder(bytes.NewReader(doc))
	dec.DisallowUnknownFields()
	var p Policy
	if err := dec.Decode(&p); err != nil {
		return nil, fmt.Errorf("policy: %w", err)
	}
	if err := p.Validate(); err != nil {
		return nil, err
	}
	return &p, nil
}

// Validate checks a policy's structure and expressions.
func (p *Policy) Validate() error {
	var errs []string
	add := func(f string, a ...any) { errs = append(errs, fmt.Sprintf(f, a...)) }
	if len(p.Rules) == 0 {
		add("at least one rule is required")
	}
	for i, r := range p.Rules {
		if r.When != "" {
			if !strings.HasPrefix(r.When, "=") {
				add("rules[%d].when must be an expression starting with =", i)
			} else if err := engine.Check(r.When); err != nil {
				add("rules[%d].when: %v", i, err)
			}
		}
		if len(r.Levels) == 0 {
			add("rules[%d] needs at least one level", i)
		}
		for j, l := range r.Levels {
			if !roleRe.MatchString(l.Role) {
				add("rules[%d].levels[%d].role %q is not a role name", i, j, l.Role)
			}
			if l.Count < 0 || l.Count > 20 {
				add("rules[%d].levels[%d].count must be between 1 and 20", i, j)
			}
		}
		switch r.StepUp {
		case "", "whatsapp_pin", "totp", "passkey":
		default:
			add("rules[%d].step_up must be whatsapp_pin, totp or passkey", i)
		}
	}
	if p.Timeout != "" {
		if _, err := wd.ParseDuration(p.Timeout); err != nil {
			add("timeout: %v", err)
		}
	}
	switch on := p.OnTimeout; {
	case on == "", on == "reject", on == "fail":
	case strings.HasPrefix(on, "escalate:") && roleRe.MatchString(strings.TrimPrefix(on, "escalate:")):
		if p.Timeout == "" {
			add("on_timeout needs a timeout")
		}
	default:
		add("on_timeout must be reject, fail, or escalate:<role>")
	}
	if len(errs) > 0 {
		return errors.New("policy: " + strings.Join(errs, "; "))
	}
	return nil
}

// ErrNoRule means no rule matched the subject: the approval cannot be
// routed, and the step fails rather than letting anyone approve.
var ErrNoRule = errors.New("no policy rule matches")

// Match returns the first rule whose condition holds for subject.
func (p *Policy) Match(subject any) (*Rule, error) {
	act := map[string]any{"subject": subject}
	for i := range p.Rules {
		r := &p.Rules[i]
		if r.When == "" {
			return r, nil
		}
		ok, err := engine.EvalBool(r.When, act)
		if err != nil {
			return nil, fmt.Errorf("rules[%d].when: %w", i, err)
		}
		if ok {
			return r, nil
		}
	}
	return nil, ErrNoRule
}

// Normalised returns levels with counts defaulted to 1.
func (r *Rule) Normalised() []Level {
	out := make([]Level, len(r.Levels))
	for i, l := range r.Levels {
		if l.Count <= 0 {
			l.Count = 1
		}
		out[i] = l
	}
	return out
}
