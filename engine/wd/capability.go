package wd

import (
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/israel-duff/taskiem/engine/capability"
)

// Waits are the capability steps that follow a payment rather than make
// one: they need no capability of their own, only the rail's settles_by
// (connector/v1 rule 16) and its payout events.
var Waits = []string{"await_settlement", "await_reversal"}

// MaxCapabilityStepID bounds a capability step's id, so the ids expansion
// gives a composite's earlier steps (<id>__<name>) stay valid step ids.
const MaxCapabilityStepID = 40

// IsWait reports whether uses is await_settlement or await_reversal.
func IsWait(uses string) bool { return slices.Contains(Waits, uses) }

// checkCapabilities enforces the capability step rules of the wd-v1
// contract: the capability exists, the input names only its fields and
// gives every required one, and a wait names the payout it follows.
func checkCapabilities(d *Definition) []Problem {
	var out []Problem
	add := func(s *Step, format string, args ...any) {
		out = append(out, Problem{Path: "step " + s.ID, Message: fmt.Sprintf(format, args...)})
	}
	ids := make([]string, 0, len(d.byID))
	for id := range d.byID {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		s := d.byID[id]
		if s.Type != "capability" {
			continue
		}
		if len(s.ID) > MaxCapabilityStepID {
			add(s, "a capability step's id is at most %d characters", MaxCapabilityStepID)
		}
		if strings.Contains(s.ID, "__") {
			add(s, "a capability step's id may not contain __ (expansion uses it)")
		}
		if IsWait(s.Uses) {
			for k := range s.Input {
				if k != "payout_id" {
					add(s, "%s takes only payout_id, not %s", s.Uses, k)
				}
			}
			if _, ok := s.Input["payout_id"]; !ok {
				add(s, "%s needs payout_id: the payment to follow", s.Uses)
			}
			if s.Timeout == "" {
				add(s, "%s needs a timeout: how long to wait", s.Uses)
			}
			continue
		}
		spec, ok := capability.Get(s.Uses)
		if !ok {
			add(s, "unknown capability %q", s.Uses)
			continue
		}
		fields := capability.Fields(spec.Input)
		for k := range s.Input {
			if !slices.ContainsFunc(fields, func(f capability.Field) bool { return len(f.Path) == 1 && f.Path[0] == k }) {
				add(s, "%s has no input %q", s.Uses, k)
			}
		}
		for _, f := range fields {
			if len(f.Path) != 1 || !f.Required {
				continue
			}
			if _, ok := s.Input[f.Path[0]]; !ok {
				add(s, "%s needs %s", s.Uses, f.Name())
			}
		}
		// Literal values are checked against the capability's schema now;
		// expressions when the step runs.
		lit := map[string]any{}
		for k, v := range s.Input {
			if isLiteral(v) {
				lit[k] = v
			}
		}
		for _, msg := range spec.CheckInput(lit) {
			if !strings.Contains(msg, "missing propert") {
				add(s, "%s input: %s", s.Uses, msg)
			}
		}
	}
	return out
}

// isLiteral reports whether v holds no expression at any depth.
func isLiteral(v any) bool {
	lit := true
	walkExprs(v, func(string) { lit = false })
	return lit
}
