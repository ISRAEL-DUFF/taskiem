package wd

import (
	"fmt"
	"sort"

	"github.com/israel-duff/taskiem/engine/expr"
)

var engine = expr.MustNew()

// checkExpressions enforces the expression rules of the wd-v1 contract:
// every expression compiles; steps.<id> references only steps that are
// guaranteed complete (rule 5); item/index only inside a foreach; and an
// expression that reads secrets reads nothing else (rule 10).
func checkExpressions(d *Definition) []Problem {
	c := &exprChecker{d: d, onErrorOwner: map[string]*Step{}}
	for _, s := range d.byID {
		if s.OnError != nil {
			for _, n := range s.OnError.Steps {
				c.onErrorOwner[n.ID] = s
			}
		}
	}
	ids := make([]string, 0, len(d.byID))
	for id := range d.byID {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		c.step(d.byID[id])
	}
	return c.problems
}

type exprChecker struct {
	d            *Definition
	onErrorOwner map[string]*Step
	problems     []Problem
}

// allowed is the set of step ids whose outputs are settled before s runs.
func (c *exprChecker) allowed(s *Step) map[string]bool {
	out := map[string]bool{}
	var closure func(id string)
	scopeOf := c.siblings(s)
	closure = func(id string) {
		for _, n := range scopeOf[id] {
			if !out[n] {
				out[n] = true
				closure(n)
			}
		}
	}
	closure(s.ID)
	if owner := c.onErrorOwner[s.ID]; owner != nil {
		out[owner.ID] = true
		for k := range c.allowed(owner) {
			out[k] = true
		}
	} else if p := c.d.Parent(s.ID); p != nil {
		for k := range c.allowed(p) {
			out[k] = true
		}
	}
	return out
}

// siblings maps each step in s's scope to its needs.
func (c *exprChecker) siblings(s *Step) map[string][]string {
	var scope []*Step
	if owner := c.onErrorOwner[s.ID]; owner != nil {
		scope = owner.OnError.Steps
	} else if p := c.d.Parent(s.ID); p != nil {
		for _, sc := range p.Children() {
			for _, x := range sc {
				if x.ID == s.ID {
					scope = sc
				}
			}
		}
	} else {
		scope = c.d.Steps
	}
	m := map[string][]string{}
	for _, x := range scope {
		m[x.ID] = x.Needs
	}
	return m
}

func (c *exprChecker) inForeach(s *Step) bool {
	for p := c.d.Parent(s.ID); p != nil; p = c.d.Parent(p.ID) {
		if p.Type == "foreach" {
			return true
		}
	}
	return false
}

func (c *exprChecker) step(s *Step) {
	allowed := c.allowed(s)
	selfOK := map[string]bool{}
	for k := range allowed {
		selfOK[k] = true
	}
	selfOK[s.ID] = true

	check := func(where string, v any, extra map[string]bool) {
		walkExprs(v, func(src string) {
			refs, err := engine.References(src)
			if err != nil {
				c.add(s, where, "%v", err)
				return
			}
			if refs.Roots["secrets"] && len(refs.Roots) > 1 {
				c.add(s, where, "an expression that reads secrets may not read anything else: %s", src)
			}
			if (refs.Roots["item"] || refs.Roots["index"]) && !c.inForeach(s) {
				c.add(s, where, "item and index are only available inside a foreach: %s", src)
			}
			for id := range refs.Steps {
				if !extra[id] {
					if c.d.Step(id) == nil {
						c.add(s, where, "unknown step %q in %s", id, src)
					} else {
						c.add(s, where, "steps.%s may not have run before %s; add it to needs: %s", id, s.ID, src)
					}
				}
			}
		})
	}
	if s.When != "" {
		check("when", s.When, allowed)
	}
	check("input", s.Input, allowed)
	if s.Effect != nil {
		check("effect", s.Effect.IdempotencySeed, allowed)
	}
	if s.Compensate != nil {
		check("compensate", s.Compensate.Input, selfOK)
	}
	if len(s.RawConfig) > 0 {
		raw, _ := expr.DecodeJSON(s.RawConfig)
		cfg, _ := raw.(map[string]any)
		for k, v := range cfg {
			switch k {
			case "steps", "default", "branches":
				continue
			case "paths":
				for i, p := range v.([]any) {
					check(fmt.Sprintf("config.paths[%d].when", i), p.(map[string]any)["when"], allowed)
				}
				continue
			}
			check("config."+k, v, allowed)
		}
	}
}

func (c *exprChecker) add(s *Step, where, format string, args ...any) {
	c.problems = append(c.problems, Problem{Path: "step " + s.ID + " " + where, Message: fmt.Sprintf(format, args...)})
}

func walkExprs(v any, fn func(string)) {
	switch t := v.(type) {
	case string:
		if expr.IsExpr(t) {
			fn(t)
		}
	case map[string]any:
		for _, c := range t {
			walkExprs(c, fn)
		}
	case []any:
		for _, c := range t {
			walkExprs(c, fn)
		}
	}
}
