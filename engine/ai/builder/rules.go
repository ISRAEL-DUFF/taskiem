package builder

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/israel-duff/taskiem/engine/capability"
	"github.com/israel-duff/taskiem/engine/connector"
	"github.com/israel-duff/taskiem/engine/expansion"
	"github.com/israel-duff/taskiem/engine/expr"
	"github.com/israel-duff/taskiem/engine/wd"
	"github.com/israel-duff/taskiem/templates"
)

// Policy rules the builder applies on top of the publishing checks (spec
// 12.1 step 4: "any money-moving step without an approval step is flagged
// by tenant policy"). Publishing does not enforce them, because a person
// may deliberately automate a payment without approval; the builder flags
// them so the reviewer decides with the warning in front of them.

// RulePaymentWithoutApproval flags a write action of a payments connector
// that no approval step precedes.
const RulePaymentWithoutApproval = "payment_write_without_approval"

// PolicyFindings lists policy rule violations in def: in its template
// steps' templates too (decision 0047), whose payments are the workflow's.
func PolicyFindings(def *wd.Definition, reg connector.Lookup) []Warning {
	return PolicyFindingsWith(def, reg, nil)
}

// PolicyFindingsWith is PolicyFindings with the templates a template step
// may name (nil: the library).
func PolicyFindingsWith(def *wd.Definition, reg connector.Lookup, find expansion.Templates) []Warning {
	if find == nil {
		find = expansion.Library(templates.Default())
	}
	if def.HasTemplates() {
		exp, err := expansion.ExpandDefinition(def, find)
		if err != nil {
			return nil // the publishing checks say why it does not expand
		}
		def = exp
	}
	var out []Warning
	// before holds, for the scope being walked, the approval steps that
	// precede the enclosing control step (inherited by nested scopes).
	var walk func(steps []*wd.Step, path string, inherited bool)
	walk = func(steps []*wd.Step, path string, inherited bool) {
		byID := map[string]*wd.Step{}
		for _, st := range steps {
			byID[st.ID] = st
		}
		memo := map[string]bool{}
		var approved func(id string, seen map[string]bool) bool
		approved = func(id string, seen map[string]bool) bool {
			if v, ok := memo[id]; ok {
				return v
			}
			if seen[id] {
				return false
			}
			seen[id] = true
			st := byID[id]
			if st == nil {
				return false
			}
			res := false
			for _, n := range st.Needs {
				if ns := byID[n]; ns != nil && ns.Type == "approval" {
					res = true
					break
				}
				if approved(n, seen) {
					res = true
					break
				}
			}
			memo[id] = res
			return res
		}
		for _, st := range steps {
			p := path + "/" + st.ID
			gated := inherited || approved(st.ID, map[string]bool{})
			if st.Type == "connector" && !gated && moneyMoving(reg, st) {
				out = append(out, Warning{Kind: "policy", Rule: RulePaymentWithoutApproval, Path: "/steps/" + st.ID,
					Message: fmt.Sprintf("step %q moves money (%s %s) but no approval step comes before it; add an approval step it needs, or confirm that this payment may run unapproved", st.ID, st.Connector, st.Action)})
			}
			if st.Type == "capability" && !gated && capabilityMovesMoney(st.Uses) {
				out = append(out, Warning{Kind: "policy", Rule: RulePaymentWithoutApproval, Path: "/steps/" + st.ID,
					Message: fmt.Sprintf("step %q moves money (%s) but no approval step comes before it; add an approval step it needs, or confirm that this payment may run unapproved", st.ID, st.Uses)})
			}
			for _, sub := range st.Children() {
				walk(sub, p, gated || st.Type == "approval")
			}
		}
	}
	walk(def.Steps, "", false)
	return append(out, customerToolFindings(def, reg)...)
}

// RuleCustomerToolPrivate flags a tool for customers whose answer shows
// an output marked public that is read from something the business keeps
// to itself: a saved thing, a tenant variable, or a step whose output its
// connector marks personal data (a table's rows, a customer's record).
// Publishing checks that the answer shows public outputs only (made tools
// §12); whether each public output is fit for a stranger is the business's
// call, so the builder puts it in front of the reviewer (made tools §17).
const RuleCustomerToolPrivate = "customer_tool_private_output"

// outputExprs reads an output's value.
var outputExprs = expr.MustNewWithRoots("steps", "trigger", "run", "env", "secrets", "saved")

func customerToolFindings(def *wd.Definition, reg connector.Lookup) []Warning {
	if def.Tool == nil || def.Tool.Audience() != wd.ToolForCustomers {
		return nil
	}
	steps := map[string]*wd.Step{}
	def.Walk(func(st *wd.Step) { steps[st.ID] = st })
	var out []Warning
	for i, o := range def.Outputs {
		if !o.Public {
			continue
		}
		refs, err := outputExprs.References(o.Value)
		if err != nil {
			continue
		}
		from := ""
		switch {
		case refs.Roots["saved"]:
			from = "a saved thing"
		case refs.Roots["env"] || refs.Roots["secrets"]:
			from = "a setting of the business"
		default:
			for id := range refs.Steps {
				st := steps[id]
				switch {
				case st == nil:
				case personalOutput(reg, st):
					from = fmt.Sprintf("step %q, whose output is personal data", id)
				case readsKept(st):
					from = fmt.Sprintf("step %q, which reads a saved thing or a setting of the business", id)
				}
				if from != "" {
					break
				}
			}
		}
		if from != "" {
			out = append(out, Warning{Kind: "policy", Rule: RuleCustomerToolPrivate, Path: fmt.Sprintf("/outputs/%d", i),
				Message: fmt.Sprintf("output %q is marked public, so every customer who uses the tool sees it, and it is read from %s; mark it public only if any customer may see it", o.Name, from)})
		}
	}
	return out
}

// keptRoot finds an expression reading the saved, env or secrets root.
var keptRoot = regexp.MustCompile(`"=[^"]*\b(saved|env|secrets)\s*[.\[]`)

// readsKept reports whether a step's expressions read a saved thing or a
// setting of the business (a transform that copies one for an output).
func readsKept(st *wd.Step) bool {
	raw, err := json.Marshal(st)
	return err == nil && keptRoot.Match(raw)
}

// personalOutput reports whether a connector step's action marks some of
// its output personal data.
func personalOutput(reg connector.Lookup, st *wd.Step) bool {
	if st.Type != "connector" {
		return false
	}
	c, ok := reg.Get(st.Connector)
	if !ok {
		return false
	}
	a, ok := c.Manifest.Actions[st.Action]
	if !ok {
		return false
	}
	for _, f := range a.PII {
		if strings.HasPrefix(f.Field, "output") {
			return true
		}
	}
	return false
}

func moneyMoving(reg connector.Lookup, st *wd.Step) bool {
	c, ok := reg.Get(st.Connector)
	if !ok {
		return false
	}
	a, ok := c.Manifest.Actions[st.Action]
	return ok && c.Manifest.Category == "payments" && a.Class.IsWrite()
}

// capabilityMovesMoney reports whether a capability sends money out: a
// payment or a refund, whichever rail does it.
func capabilityMovesMoney(uses string) bool {
	spec, ok := capability.Get(uses)
	return ok && (spec.IsPayout() || uses == "refund")
}
