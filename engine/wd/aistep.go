package wd

import (
	"encoding/json"
	"fmt"
	"slices"
	"sort"
	"strings"
)

// checkAI enforces the ai step rules of the wd-v1 contract (decision
// 0050): the answer's schema is an object schema, which structured output
// needs; the fields allowed to reach the model with their personal data
// are fields of the step's input; and neither the prompt nor the input
// reads secrets, which would leave for the model. Which models a
// deployment offers, and whether the business allows AI steps, are
// checked when a version is saved (the API) and when the step runs.
func checkAI(d *Definition) []Problem {
	var out []Problem
	ids := make([]string, 0, len(d.byID))
	for id := range d.byID {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		s := d.byID[id]
		if s.Type != "ai" || s.AI == nil {
			continue
		}
		add := func(format string, args ...any) {
			out = append(out, Problem{Path: "step " + s.ID, Message: fmt.Sprintf(format, args...)})
		}
		var schema map[string]any
		if json.Unmarshal(s.AI.OutputSchema, &schema) != nil || schema["type"] != "object" {
			add("an ai step's output_schema is an object schema ({\"type\": \"object\", \"properties\": ...}): the answer is a JSON object")
		}
		for _, f := range s.AI.PersonalData {
			if _, ok := s.Input[f]; !ok {
				add("personal_data names %q, which is not a field of the step's input", f)
			}
		}
		readsSecrets := func(src string) bool {
			return strings.Contains(src, "secrets.") || strings.Contains(src, "secrets[")
		}
		if strings.HasPrefix(s.AI.Prompt, "=") && readsSecrets(s.AI.Prompt) {
			add("an ai step's prompt may not read secrets: it is sent to the AI model")
		}
		bad := false
		EachExpr(s.Input, func(src string) { bad = bad || readsSecrets(src) })
		if bad {
			add("an ai step's input may not read secrets: it is sent to the AI model")
		}
	}
	return out
}

// HasAI reports whether a definition has ai steps.
func (d *Definition) HasAI() bool {
	found := false
	d.Walk(func(s *Step) { found = found || s.Type == "ai" })
	return found
}

// AIPersonalData lists, by step id, the input fields each ai step lets
// reach the model with their personal data, for the audit record a
// publication writes.
func (d *Definition) AIPersonalData() map[string][]string {
	out := map[string][]string{}
	d.Walk(func(s *Step) {
		if s.Type == "ai" && s.AI != nil && len(s.AI.PersonalData) > 0 {
			out[s.ID] = slices.Clone(s.AI.PersonalData)
		}
	})
	return out
}

// AIModels lists the models a definition's ai steps name.
func (d *Definition) AIModels() []string {
	var out []string
	d.Walk(func(s *Step) {
		if s.Type == "ai" && s.AI != nil && s.AI.Model != "" && !slices.Contains(out, s.AI.Model) {
			out = append(out, s.AI.Model)
		}
	})
	sort.Strings(out)
	return out
}
