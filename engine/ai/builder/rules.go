package builder

import (
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
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
			// The data's whole way to the output (review M6, 1): a step
			// the output reads, the steps its own inputs read, and so on.
			seen := map[string]bool{}
			ids := sortedKeys(refs.Steps)
			for len(ids) > 0 && from == "" {
				id := ids[0]
				ids = ids[1:]
				if seen[id] {
					continue
				}
				seen[id] = true
				st := steps[id]
				if st == nil {
					continue
				}
				if why := privateSource(reg, st); why != "" {
					from = fmt.Sprintf("step %q, %s", id, why)
					break
				}
				ids = append(ids, stepsRead(st)...)
			}
		}
		if from != "" {
			out = append(out, Warning{Kind: "policy", Rule: RuleCustomerToolPrivate, Path: fmt.Sprintf("/outputs/%d", i),
				Message: fmt.Sprintf("output %q is marked public, so every customer who uses the tool sees it, and it is read from %s; mark it public only if any customer may see it", o.Name, from)})
		}
	}
	return out
}

// privateSource says why a step's output may hold what a customer should
// not see: a connector whose output its manifest marks personal data (a
// table's rows), a payment rail's answer, another system's answer (an http
// step), code or a container (whose output cannot be read), another run's
// outputs, a person's answers (a form, a callback, a signal), or a saved
// thing or a setting read by the step; "" when none. A step inside a
// control step counts for it: a foreach's output is its body's.
func privateSource(reg connector.Lookup, st *wd.Step) string {
	switch st.Type {
	case "http":
		return "which reads another system's answer"
	case "code", "container":
		return "whose output Taskiem cannot read"
	case "capability":
		return "which reads a payment rail's answer"
	case "subflow", "template":
		return "which reads another workflow's outputs"
	case "form", "callback", "signal":
		return "which reads what a person or a system sent"
	}
	if personalOutput(reg, st) {
		return "whose output is personal data"
	}
	if readsKept(st) {
		return "which reads a saved thing or a setting of the business"
	}
	for _, sub := range st.Children() {
		for _, c := range sub {
			if why := privateSource(reg, c); why != "" {
				return why
			}
		}
	}
	return ""
}

// stepRef finds a step an expression reads: steps.<id> or steps.?<id>.
var stepRef = regexp.MustCompile(`\bsteps\.\??([a-z][a-z0-9_]*)`)

// stepsRead are the steps a step's expressions read, by id, in order.
func stepsRead(st *wd.Step) []string {
	raw, err := json.Marshal(st)
	if err != nil {
		return nil
	}
	var out []string
	seen := map[string]bool{}
	for _, m := range stepRef.FindAllSubmatch(raw, -1) {
		if id := string(m[1]); !seen[id] && id != st.ID {
			seen[id] = true
			out = append(out, id)
		}
	}
	return out
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	slices.Sort(out)
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
