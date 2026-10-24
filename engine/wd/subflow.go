package wd

import (
	"fmt"
	"sort"
	"strings"
)

// MaxSubflowDepth is how many runs deep subflow steps may nest: a child
// of a child of a child... beyond it the step fails rather than start a
// run, so a chain of workflows that call each other cannot run away.
const MaxSubflowDepth = 5

// checkSubflows enforces the subflow step rules of the wd-v1 contract: a
// subflow step takes no retry or timeout of its own (the child run has
// its own), and its input reads no secrets, since the child's trigger is
// recorded in its history as any run's is. Which workflow it names, and
// whether its input fits that workflow's inputs, is checked against the
// business's workflows when the version is saved (the API) and when the
// step starts.
func checkSubflows(d *Definition) []Problem {
	var out []Problem
	ids := make([]string, 0, len(d.byID))
	for id := range d.byID {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		s := d.byID[id]
		if s.Type != "subflow" {
			continue
		}
		add := func(format string, args ...any) {
			out = append(out, Problem{Path: "step " + s.ID, Message: fmt.Sprintf(format, args...)})
		}
		if s.Retry != nil || s.Timeout != "" {
			add("a subflow step takes no retry or timeout: the workflow it runs has its own settings")
		}
		EachExpr(s.Input, func(src string) {
			if strings.Contains(src, "secrets.") || strings.Contains(src, "secrets[") {
				add("a subflow step's input may not read secrets: it is the child run's trigger, recorded in its history")
			}
		})
	}
	return out
}

// HasSubflows reports whether a definition has subflow steps.
func (d *Definition) HasSubflows() bool {
	found := false
	d.Walk(func(s *Step) { found = found || s.Type == "subflow" })
	return found
}
