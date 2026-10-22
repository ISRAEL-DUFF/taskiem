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
// (connector/v1 rule 16) and its payout events, or for await_payment its
// payment.received event.
var Waits = []string{"await_settlement", "await_reversal", "await_payment"}

// WaitInput is the one input a wait takes: the payment_id a collect_link
// answered for await_payment, else the payout_id a payment answered.
func WaitInput(uses string) string {
	if uses == "await_payment" {
		return "payment_id"
	}
	return "payout_id"
}

// MaxCapabilityStepID bounds a capability step's id, so the ids expansion
// gives a composite's earlier steps (<id>__<name>) stay valid step ids.
const MaxCapabilityStepID = 40

// IsWait reports whether uses is a wait: await_settlement, await_reversal
// or await_payment.
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
			want := WaitInput(s.Uses)
			for k := range s.Input {
				if k != want {
					add(s, "%s takes only %s, not %s", s.Uses, want, k)
				}
			}
			if _, ok := s.Input[want]; !ok {
				add(s, "%s needs %s: the payment to follow", s.Uses, want)
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
