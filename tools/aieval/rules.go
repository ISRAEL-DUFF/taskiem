package main

import (
	"encoding/json"
	"regexp"
	"sort"
	"strings"

	"github.com/israel-duff/taskiem/engine/ai/builder"
	"github.com/israel-duff/taskiem/engine/connector"
	"github.com/israel-duff/taskiem/engine/wd"
)

// A rule checks one property of a definition (spec 12.4: "required
// properties"); it returns whether it holds and, when not, why. Rules
// read the definition only: they are deterministic and free.
type rule func(def *wd.Definition, reg connector.Lookup) (bool, string)

// rules are the properties cases may require. Hygiene rules run on every
// case whether or not the case names them.
var rules = map[string]rule{
	"approval_before_payment": ruleApprovalBeforePayment,
	"idempotency":             ruleIdempotency,
	"webhook_auth":            ruleWebhookAuth,
	"dedup":                   ruleDedup,
	"bounded_concurrency":     ruleBoundedConcurrency,
	"approval_timeout":        ruleApprovalTimeout,
	"error_handling":          ruleErrorHandling,
	"reads_before_paying":     ruleReadsBeforePaying,
	"inputs_declared":         ruleInputsDeclared,
	"no_hardcoded_contacts":   ruleNoHardcodedContacts,
	"no_inline_secrets":       ruleNoInlineSecrets,
}

// hygiene rules apply to every case.
var hygiene = []string{"no_inline_secrets"}

func ruleNames() []string {
	names := make([]string, 0, len(rules))
	for n := range rules {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// walk visits every step, nested ones included.
func walk(steps []*wd.Step, fn func(st *wd.Step)) {
	for _, st := range steps {
		fn(st)
		for _, sub := range st.Children() {
			walk(sub, fn)
		}
	}
}

func isPaymentWrite(reg connector.Lookup, st *wd.Step) bool {
	if st.Type != "connector" {
		return false
	}
	c, ok := reg.Get(st.Connector)
	if !ok {
		return false
	}
	a, ok := c.Manifest.Actions[st.Action]
	return ok && c.Manifest.Category == "payments" && a.Class.IsWrite()
}

// A payment write must exist and every one must be preceded by an
// approval (the builder's own policy rule, applied to the final draft).
func ruleApprovalBeforePayment(def *wd.Definition, reg connector.Lookup) (bool, string) {
	n := 0
	walk(def.Steps, func(st *wd.Step) {
		if isPaymentWrite(reg, st) {
			n++
		}
	})
	if n == 0 {
		return false, "no step moves money"
	}
	if f := builder.PolicyFindings(def, reg); len(f) > 0 {
		return false, f[0].Message
	}
	return true, ""
}

// Every payment write carries an idempotency seed, and every http write
// declared idempotent names its idempotency header.
func ruleIdempotency(def *wd.Definition, reg connector.Lookup) (bool, string) {
	why := ""
	n := 0
	walk(def.Steps, func(st *wd.Step) {
		switch {
		case isPaymentWrite(reg, st):
			n++
			if st.Effect == nil || st.Effect.IdempotencySeed == "" {
				why = "step " + st.ID + " moves money without effect.idempotency_seed"
			}
		case st.Type == "http" && st.HTTP != nil && st.HTTP.Class == "idempotent_write":
			n++
			if st.HTTP.IdempotencyHeader == "" && (st.Effect == nil || st.Effect.IdempotencySeed == "") {
				why = "http step " + st.ID + " is an idempotent write without an idempotency key"
			}
		}
	})
	if n == 0 {
		return false, "no write that needs an idempotency key"
	}
	return why == "", why
}

// A webhook trigger checks who is calling (connector events are verified
// by their connector).
func ruleWebhookAuth(def *wd.Definition, _ connector.Lookup) (bool, string) {
	switch def.Trigger.Type {
	case "connector_event":
		return true, ""
	case "webhook":
		if a, _ := def.Trigger.Config["auth"].(string); a != "" && a != "none" {
			return true, ""
		}
		return false, "the webhook accepts unauthenticated calls"
	}
	return false, "the trigger is not a webhook"
}

// The same event delivered twice starts one run: a webhook dedup
// expression, or a connector event (deduplicated by its connector).
func ruleDedup(def *wd.Definition, _ connector.Lookup) (bool, string) {
	switch def.Trigger.Type {
	case "connector_event":
		return true, ""
	case "webhook":
		if d, _ := def.Trigger.Config["dedup"].(string); d != "" {
			return true, ""
		}
		return false, "the webhook has no dedup expression"
	}
	// Other triggers deduplicate in a step: an idempotency seed on writes.
	ok := false
	walk(def.Steps, func(st *wd.Step) {
		if st.Effect != nil && st.Effect.IdempotencySeed != "" {
			ok = true
		}
	})
	if !ok {
		return false, "nothing stops a repeated event from acting twice"
	}
	return true, ""
}

func ruleBoundedConcurrency(def *wd.Definition, _ connector.Lookup) (bool, string) {
	n, why := 0, ""
	walk(def.Steps, func(st *wd.Step) {
		if st.Type == "foreach" {
			n++
			if st.Foreach == nil || st.Foreach.MaxConcurrency <= 0 {
				why = "foreach " + st.ID + " sets no max_concurrency"
			}
		}
	})
	if n == 0 {
		return false, "no foreach"
	}
	return why == "", why
}

func ruleApprovalTimeout(def *wd.Definition, _ connector.Lookup) (bool, string) {
	n, why := 0, ""
	walk(def.Steps, func(st *wd.Step) {
		if st.Type == "approval" {
			n++
			if st.Approval == nil || (st.Approval.Timeout == "" && st.Approval.Policy == "") {
				why = "approval " + st.ID + " can wait forever"
			}
		}
	})
	if n == 0 {
		return false, "no approval step"
	}
	return why == "", why
}

func ruleErrorHandling(def *wd.Definition, _ connector.Lookup) (bool, string) {
	ok := false
	walk(def.Steps, func(st *wd.Step) {
		if (st.OnError != nil && len(st.OnError.Steps) > 0) || (st.Compensate != nil && st.Compensate.Action != "") {
			ok = true
		}
	})
	if !ok {
		return false, "no on_error path or compensation"
	}
	return true, ""
}

// A read of a payments connector (balance, name enquiry, status) runs
// before the first payment write it guards.
func ruleReadsBeforePaying(def *wd.Definition, reg connector.Lookup) (bool, string) {
	reads := map[string]bool{}
	var payments []*wd.Step
	walk(def.Steps, func(st *wd.Step) {
		if st.Type != "connector" {
			return
		}
		c, ok := reg.Get(st.Connector)
		if !ok || c.Manifest.Category != "payments" {
			return
		}
		if a, ok := c.Manifest.Actions[st.Action]; ok && !a.Class.IsWrite() {
			reads[st.ID] = true
		}
		if isPaymentWrite(reg, st) {
			payments = append(payments, st)
		}
	})
	if len(payments) == 0 {
		return false, "no step moves money"
	}
	for _, p := range payments {
		if !dependsOnAny(def, p, reads) {
			return false, "step " + p.ID + " pays without a read of the provider before it"
		}
	}
	return true, ""
}

// dependsOnAny reports whether st (or a control step enclosing it)
// transitively needs one of ids.
func dependsOnAny(def *wd.Definition, st *wd.Step, ids map[string]bool) bool {
	seen := map[string]bool{}
	var visit func(s *wd.Step) bool
	visit = func(s *wd.Step) bool {
		if s == nil || seen[s.ID] {
			return false
		}
		seen[s.ID] = true
		for _, n := range s.Needs {
			if ids[n] || visit(def.Step(n)) {
				return true
			}
		}
		return visit(def.Parent(s.ID))
	}
	return visit(st)
}

func ruleInputsDeclared(def *wd.Definition, _ connector.Lookup) (bool, string) {
	if def.RawInputs == nil || len(def.RawInputs.Schema) == 0 {
		return false, "the workflow declares no inputs schema"
	}
	return true, ""
}

var (
	rePhone = regexp.MustCompile(`(^|[^0-9])(\+?234|0)[789][01][0-9]{8}([^0-9]|$)`)
	reEmail = regexp.MustCompile(`[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+\.[A-Za-z]{2,}`)
	// Literal secret shapes: provider keys, bearer tokens, long opaque
	// strings outside expressions.
	reSecret = regexp.MustCompile(`(sk|pk|rk)_(live|test)_[A-Za-z0-9]{8,}|FLWSECK[A-Za-z0-9_-]{8,}|Bearer [A-Za-z0-9._-]{16,}|xox[abp]-[A-Za-z0-9-]{10,}|AKIA[0-9A-Z]{16}`)
)

// literals returns every string literal in steps (expressions excluded).
func literals(def *wd.Definition) []string {
	raw, _ := json.Marshal(def.Steps)
	var v any
	_ = json.Unmarshal(raw, &v)
	var out []string
	var visit func(any)
	visit = func(x any) {
		switch t := x.(type) {
		case map[string]any:
			for _, e := range t {
				visit(e)
			}
		case []any:
			for _, e := range t {
				visit(e)
			}
		case string:
			if !strings.HasPrefix(t, "=") {
				out = append(out, t)
			}
		}
	}
	visit(v)
	return out
}

// Phone numbers and email addresses come from tenant variables or the
// run's data, not literals in the definition.
func ruleNoHardcodedContacts(def *wd.Definition, _ connector.Lookup) (bool, string) {
	for _, s := range literals(def) {
		if rePhone.MatchString(s) || reEmail.MatchString(s) {
			return false, "a phone number or email address is written into the workflow"
		}
	}
	return true, ""
}

func ruleNoInlineSecrets(def *wd.Definition, _ connector.Lookup) (bool, string) {
	raw, _ := json.Marshal(def)
	if reSecret.Match(raw) {
		return false, "a credential is written into the workflow"
	}
	return true, ""
}

// checkProperties evaluates the case's properties plus the hygiene rules.
func checkProperties(def *wd.Definition, reg connector.Lookup, want []string) (map[string]bool, []string) {
	names := append([]string{}, want...)
	for _, h := range hygiene {
		if !contains(names, h) {
			names = append(names, h)
		}
	}
	out := map[string]bool{}
	var why []string
	for _, n := range names {
		ok, reason := rules[n](def, reg)
		out[n] = ok
		if !ok {
			why = append(why, n+": "+reason)
		}
	}
	return out, why
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}
