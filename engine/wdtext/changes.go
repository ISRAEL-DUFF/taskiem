package wdtext

import (
	"encoding/json"
	"slices"
	"strings"

	"github.com/israel-duff/taskiem/engine/connector"
	"github.com/israel-duff/taskiem/engine/lang"
	"github.com/israel-duff/taskiem/engine/wd"
	"github.com/israel-duff/taskiem/engine/wddiff"
)

// Changes says in plain words what changed from one version of a workflow
// to the next (made tools §17: "these are what an approver reads"), for
// the version history and the review of a publish: what a tool now is,
// says, is for and answers ("It now also says 'spent'", "Customers can now
// use it"), who may start it, and which steps were added, removed or
// changed, each step read as the read-back reads it. Names and phrases
// are the business's own words, kept as written. old nil (or empty) is a
// first version: nothing is said.
func Changes(oldDoc, newDoc json.RawMessage, reg connector.Lookup, t lang.Tag) []string {
	if len(oldDoc) == 0 || len(newDoc) == 0 {
		return []string{}
	}
	o, n := load(oldDoc), load(newDoc)
	if o == nil || n == nil {
		return []string{}
	}
	d := describer{reg: reg, lang: t}
	out := []string{}
	if o.Name != n.Name && n.Name != "" {
		out = append(out, d.t("wd.change.name", "name", plain(n.Name)))
	}
	out = append(out, d.toolChanges(o.Tool, n.Tool)...)
	if !slices.Equal(o.Roles, n.Roles) {
		if len(n.Roles) == 0 {
			out = append(out, d.t("wd.change.roles_all"))
		} else {
			out = append(out, d.t("wd.change.roles", "roles", strings.Join(n.Roles, ", ")))
		}
	}
	var steps []string
	for _, c := range wddiff.Diff(oldDoc, newDoc) {
		switch c {
		case "~ trigger":
			out = append(out, d.t("wd.change.trigger", "trigger", d.trigger(n.Trigger)))
		case "~ inputs", "~ types":
			out = appendOnce(out, d.t("wd.change.inputs"))
		case "~ outputs":
			out = append(out, d.t("wd.change.outputs"))
		case "~ settings", "~ description":
			out = appendOnce(out, d.t("wd.change.settings"))
		}
		if id, ok := strings.CutPrefix(c, "+ step "); ok {
			steps = append(steps, d.t("wd.change.step_added", "step", d.stepWords(n, id)))
		}
		if id, ok := strings.CutPrefix(c, "- step "); ok {
			steps = append(steps, d.t("wd.change.step_removed", "step", d.stepWords(o, id)))
		}
		if id, ok := strings.CutPrefix(c, "~ step "); ok {
			steps = append(steps, d.t("wd.change.step_changed", "step", d.stepWords(n, id)))
		}
	}
	return append(out, steps...)
}

// ToolChanges is what changed in a workflow's tool alone, in plain words.
func ToolChanges(old, next *wd.Tool, t lang.Tag) []string {
	return describer{lang: t}.toolChanges(old, next)
}

func (d describer) toolChanges(o, n *wd.Tool) []string {
	out := []string{}
	switch {
	case o == nil && n == nil:
		return out
	case o == nil:
		out = append(out, d.t("wd.change.tool_new", "command", "/"+n.Command))
		if n.Audience() != wd.ToolForMembers {
			out = append(out, d.forWords(n.Audience()))
		}
		if n.Answer != nil {
			out = append(out, d.t("wd.change.answer_now", "kind", d.answerKind(n.Answer.Type)))
		}
		return out
	case n == nil:
		return append(out, d.t("wd.change.tool_gone", "command", "/"+o.Command))
	}
	if o.Name != n.Name {
		out = append(out, d.t("wd.change.tool_name", "name", plain(n.Name)))
	}
	if o.Command != n.Command {
		out = append(out, d.t("wd.change.tool_command", "command", "/"+n.Command, "was", "/"+o.Command))
	}
	if o.About != n.About {
		out = append(out, d.t("wd.change.tool_about", "about", plain(n.About)))
	}
	for _, p := range n.Says {
		if !containsFold(o.Says, p) {
			out = append(out, d.t("wd.change.says_added", "phrase", plain(p)))
		}
	}
	for _, p := range o.Says {
		if !containsFold(n.Says, p) {
			out = append(out, d.t("wd.change.says_removed", "phrase", plain(p)))
		}
	}
	if o.Audience() != n.Audience() {
		out = append(out, d.forWords(n.Audience()))
	}
	if o.Icon != n.Icon {
		out = append(out, d.t("wd.change.tool_icon"))
	}
	switch {
	case o.Answer == nil && n.Answer != nil:
		out = append(out, d.t("wd.change.answer_now", "kind", d.answerKind(n.Answer.Type)))
	case o.Answer != nil && n.Answer == nil:
		out = append(out, d.t("wd.change.answer_gone"))
	case o.Answer != nil && o.Answer.Type != n.Answer.Type:
		out = append(out, d.t("wd.change.answer_now", "kind", d.answerKind(n.Answer.Type)))
	case o.Answer != nil && !sameJSON(o.Answer, n.Answer):
		out = append(out, d.t("wd.change.answer_changed"))
	}
	return out
}

// forWords is who may call a tool now, as a change.
func (d describer) forWords(audience string) string {
	switch audience {
	case wd.ToolForCustomers:
		return d.t("wd.change.for_customers")
	case wd.ToolForLinked:
		return d.t("wd.change.for_linked")
	}
	return d.t("wd.change.for_members")
}

// answerKind names an answer's type: words, a card, a list.
func (d describer) answerKind(typ string) string {
	switch typ {
	case "card", "list":
		return d.t("wd.change.answer." + typ)
	}
	return d.t("wd.change.answer.text")
}

// stepWords is a step of def by its id, as the read-back says it; its id
// when the step cannot be found.
func (d describer) stepWords(def *wd.Definition, id string) string {
	if st := findStep(def.Steps, id); st != nil {
		return strings.TrimSuffix(d.step(st), ":")
	}
	return id
}

func findStep(steps []*wd.Step, id string) *wd.Step {
	for _, st := range steps {
		if st == nil {
			continue
		}
		if st.ID == id {
			return st
		}
		var inner [][]*wd.Step
		if st.Foreach != nil {
			inner = append(inner, st.Foreach.Steps)
		}
		if st.Parallel != nil {
			for _, b := range st.Parallel.Branches {
				inner = append(inner, b.Steps)
			}
		}
		if st.Branch != nil {
			for _, p := range st.Branch.Paths {
				inner = append(inner, p.Steps)
			}
			if st.Branch.Default != nil {
				inner = append(inner, st.Branch.Default.Steps)
			}
		}
		if st.OnError != nil {
			inner = append(inner, st.OnError.Steps)
		}
		for _, list := range inner {
			if f := findStep(list, id); f != nil {
				return f
			}
		}
	}
	return nil
}

// load reads a version's definition, as published (an old one may not
// meet today's rules, so it is read as written when it does not).
func load(doc json.RawMessage) *wd.Definition {
	if d, err := wd.Load(doc); err == nil {
		return d
	}
	var d wd.Definition
	if json.Unmarshal(doc, &d) != nil {
		return nil
	}
	return &d
}

func containsFold(list []string, s string) bool {
	for _, x := range list {
		if strings.EqualFold(strings.TrimSpace(x), strings.TrimSpace(s)) {
			return true
		}
	}
	return false
}

func appendOnce(list []string, s string) []string {
	if slices.Contains(list, s) {
		return list
	}
	return append(list, s)
}

func sameJSON(a, b any) bool {
	ja, _ := json.Marshal(a)
	jb, _ := json.Marshal(b)
	return string(ja) == string(jb)
}
