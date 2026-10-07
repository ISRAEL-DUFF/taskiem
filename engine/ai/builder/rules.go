package builder

import (
	"fmt"

	"github.com/israel-duff/taskiem/engine/connector"
	"github.com/israel-duff/taskiem/engine/wd"
)

// Policy rules the builder applies on top of the publishing checks (spec
// 12.1 step 4: "any money-moving step without an approval step is flagged
// by tenant policy"). Publishing does not enforce them, because a person
// may deliberately automate a payment without approval; the builder flags
// them so the reviewer decides with the warning in front of them.

// RulePaymentWithoutApproval flags a write action of a payments connector
// that no approval step precedes.
const RulePaymentWithoutApproval = "payment_write_without_approval"

// PolicyFindings lists policy rule violations in def.
func PolicyFindings(def *wd.Definition, reg connector.Lookup) []Warning {
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
			for _, sub := range st.Children() {
				walk(sub, p, gated || st.Type == "approval")
			}
		}
	}
	walk(def.Steps, "", false)
	return out
}

func moneyMoving(reg connector.Lookup, st *wd.Step) bool {
	c, ok := reg.Get(st.Connector)
	if !ok {
		return false
	}
	a, ok := c.Manifest.Actions[st.Action]
	return ok && c.Manifest.Category == "payments" && a.Class.IsWrite()
}
