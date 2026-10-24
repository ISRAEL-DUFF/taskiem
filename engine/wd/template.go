package wd

import (
	"fmt"
	"sort"
	"strings"
)

// MaxTemplateStepID bounds a template step's id. Expansion prefixes each
// of the template's step ids with it and two underscores, at most twice (a
// template may use a template: decision 0047). With the ids a callable
// template's own steps may have (MaxCalledStepID, MaxCalledCapabilityID),
// every expanded id stays a valid step id, and every capability step's
// stays within MaxCapabilityStepID, so its composite's ids fit too.
const MaxTemplateStepID = 14

// MaxCalledStepID bounds the step ids of a callable template.
const MaxCalledStepID = 63 - 2*(MaxTemplateStepID+2)

// MaxCalledCapabilityID bounds the capability step ids of a callable
// template.
const MaxCalledCapabilityID = MaxCapabilityStepID - 2*(MaxTemplateStepID+2)

// checkTemplates enforces the template step rules of the wd-v1 contract:
// the id leaves room for the prefixes, the parameters are values, and the
// step takes no retry or timeout of its own (the template's steps have
// theirs).
func checkTemplates(d *Definition) []Problem {
	var out []Problem
	ids := make([]string, 0, len(d.byID))
	for id := range d.byID {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		s := d.byID[id]
		if s.Type != "template" {
			continue
		}
		add := func(format string, args ...any) {
			out = append(out, Problem{Path: "step " + s.ID, Message: fmt.Sprintf(format, args...)})
		}
		if len(s.ID) > MaxTemplateStepID {
			add("a template step's id is at most %d characters", MaxTemplateStepID)
		}
		if s.Origin == nil && strings.Contains(s.ID, "__") {
			add("a template step's id may not contain __ (expansion uses it)")
		}
		if s.Retry != nil || s.Timeout != "" {
			add("a template step takes no retry or timeout: the template's own steps have theirs")
		}
		keys := make([]string, 0, len(s.Params))
		for k := range s.Params {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			if str, ok := s.Params[k].(string); ok && strings.HasPrefix(str, "=") {
				add("parameter %s is a value, never an expression: give the run's data in input", k)
			}
		}
	}
	return out
}

// HasTemplates reports whether a definition has template steps.
func (d *Definition) HasTemplates() bool {
	found := false
	d.Walk(func(s *Step) { found = found || s.Type == "template" })
	return found
}
