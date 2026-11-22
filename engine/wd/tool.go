package wd

import (
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/israel-duff/taskiem/engine/expr"
)

// Made tools (decision 0070): a workflow started on demand declares itself
// a tool with a top-level tool block. What it is called, the command that
// calls it, who it is for and what it answers with. It is not part of what
// a run does (decide() never reads it). Who may use it is the workflow's
// own Roles. The rules here need nothing but the definition; the ones that
// need the platform (a reserved word, a step that moves money, care) are
// in engine/wdcheck, and the one that needs the business (a command
// another deployed workflow has) is checked when a version is published.

// Tool is a workflow's tool block.
type Tool struct {
	Name    string   `json:"name"`
	Command string   `json:"command"`
	About   string   `json:"about"`
	Says    []string `json:"says,omitempty"`
	Icon    string   `json:"icon,omitempty"`
	// For is who may call it: members (the default), customers, linked.
	For    string  `json:"for,omitempty"`
	Answer *Answer `json:"answer,omitempty"`
}

// Audience is For, members when left out.
func (t *Tool) Audience() string {
	if t == nil || t.For == "" {
		return ToolForMembers
	}
	return t.For
}

const (
	ToolForMembers   = "members"
	ToolForCustomers = "customers"
	ToolForLinked    = "linked"
)

// Answer is what a tool's run says when it ends (made tools §7): a text,
// a card or a list, drawn from the run's outputs by the API, never in the
// run. Its expressions read outputs and, in a list's item and actions,
// item; nothing else.
type Answer struct {
	Type string `json:"type"` // text | card | list
	// text
	Text string `json:"text,omitempty"`
	// card (Title too for a list)
	Title   string       `json:"title,omitempty"`
	Ref     string       `json:"ref,omitempty"`
	State   *AnswerState `json:"state,omitempty"`
	Lines   []AnswerLine `json:"lines,omitempty"`
	Amount  string       `json:"amount,omitempty"`
	Buttons []Button     `json:"buttons,omitempty"`
	// list
	From    string      `json:"from,omitempty"`
	Item    *AnswerItem `json:"item,omitempty"`
	Empty   string      `json:"empty,omitempty"`
	Actions []Button    `json:"actions,omitempty"`
}

// AnswerState is a card's or a row's state pill.
type AnswerState struct {
	Text string `json:"text"`
	Tone string `json:"tone,omitempty"`
}

// AnswerLine is one labelled line of a card.
type AnswerLine struct {
	Label string `json:"label"`
	Value string `json:"value"`
}

// AnswerItem is how each row of a list answer reads.
type AnswerItem struct {
	Title string       `json:"title"`
	Sub   string       `json:"sub,omitempty"`
	State *AnswerState `json:"state,omitempty"`
}

// Button is a button on an answer (or, later, a message): it fills the
// form of the tool it names with its inputs; it does not run it.
type Button struct {
	Label  string         `json:"label"`
	Tool   string         `json:"tool"`
	Inputs map[string]any `json:"inputs,omitempty"`
	Once   bool           `json:"once,omitempty"`
}

// The answer's limits (made tools §7).
const (
	AnswerTextMax    = 1500
	AnswerCardLines  = 6
	AnswerCardButton = 3
	AnswerRowButtons = 2
	// CustomerInputs is how many flat inputs a customers' tool may ask:
	// its form is WhatsApp's, ten fields at most.
	CustomerInputs = 10
)

var (
	toolCommand = regexp.MustCompile(`^[a-z][a-z0-9-]{1,23}$`)
	// answerExprs read only what the completed run gave back.
	answerExprs = expr.MustNewWithRoots("outputs", "item")
)

// IsTool reports whether the definition declares itself a tool.
func (d *Definition) IsTool() bool { return d.Tool != nil }

// checkTool holds a tool block to the rules that need only the definition
// (made tools §5, §7, §11, §12).
func checkTool(d *Definition) []Problem {
	t := d.Tool
	if t == nil {
		return nil
	}
	var out []Problem
	add := func(path, format string, a ...any) {
		out = append(out, Problem{Path: path, Message: fmt.Sprintf(format, a...)})
	}
	if d.Trigger.Type != "manual" {
		add("/tool", "a tool is started by a person: its trigger must be manual, not %s", d.Trigger.Type)
	}
	if !toolCommand.MatchString(t.Command) {
		add("/tool/command", "a tool's command is 2 to 24 lowercase letters, digits and dashes, starting with a letter")
	}
	seen := map[string]bool{}
	for i, s := range t.Says {
		f := strings.Join(strings.Fields(strings.ToLower(s)), " ")
		if seen[f] {
			add(fmt.Sprintf("/tool/says/%d", i), "%q is said twice", s)
		}
		seen[f] = true
	}
	if t.For == ToolForLinked {
		add("/tool/for", "tools for linked businesses come later (made tools T7); use members or customers")
	}
	if t.Audience() == ToolForCustomers {
		out = append(out, customerInputs(d)...)
	}
	if t.Answer != nil {
		out = append(out, checkAnswer(d, t.Answer)...)
	}
	return out
}

// customerInputs: a customers' tool is asked by WhatsApp's form only, so
// its inputs are flat (no objects or lists) and ten at most.
func customerInputs(d *Definition) []Problem {
	schema, _ := d.InputsSchema()
	s, _ := schema.(map[string]any)
	props, _ := s["properties"].(map[string]any)
	var out []Problem
	if len(props) > CustomerInputs {
		out = append(out, Problem{Path: "/inputs", Message: fmt.Sprintf("a tool for customers asks at most %d things, in WhatsApp's form; this asks %d", CustomerInputs, len(props))})
	}
	names := make([]string, 0, len(props))
	for k := range props {
		names = append(names, k)
	}
	slices.Sort(names)
	for _, k := range names {
		p, _ := props[k].(map[string]any)
		if typ, _ := p["type"].(string); typ == "object" || typ == "array" {
			out = append(out, Problem{Path: "/inputs/schema/properties/" + k, Message: "a tool for customers asks only plain fields, which a form can carry: not a group or a list"})
		}
	}
	return out
}

// checkAnswer holds an answer to its shape's limits, and its expressions
// to the outputs the workflow declares (public ones only, for customers).
func checkAnswer(d *Definition, a *Answer) []Problem {
	var out []Problem
	add := func(path, format string, args ...any) {
		out = append(out, Problem{Path: path, Message: fmt.Sprintf(format, args...)})
	}
	if len(d.Outputs) == 0 {
		add("/tool/answer", "a tool that answers declares outputs: the answer is drawn from what the run gives back")
		return out
	}
	outputs := map[string]Output{}
	for _, o := range d.Outputs {
		outputs[o.Name] = o
	}
	customers := d.Tool.Audience() != ToolForMembers
	// reads checks one of the answer's values: text, or an expression
	// over outputs (and item, where withItem).
	reads := func(path, v string, withItem bool) {
		if !strings.HasPrefix(v, "=") {
			return
		}
		refs, err := answerExprs.References(v)
		if err != nil {
			add(path, "%v", err)
			return
		}
		if refs.Roots["item"] && !withItem {
			add(path, "item is a list's row: only a list's item and actions read it")
		}
		paths, err := answerExprs.Paths(v, "outputs")
		if err != nil {
			add(path, "%v", err)
			return
		}
		for _, p := range paths {
			if len(p) < 2 {
				add(path, "an answer names the output it shows: outputs.<name>, not outputs as a whole")
				continue
			}
			o, ok := outputs[p[1]]
			switch {
			case !ok:
				add(path, "no output is called %q", p[1])
			case customers && !o.Public:
				add(path, "%q is not marked public, and a tool for customers shows only public outputs", p[1])
			}
		}
	}
	buttons := func(path string, bs []Button, max int, withItem bool) {
		if len(bs) > max {
			add(path, "at most %d buttons here, not %d", max, len(bs))
		}
		for i, b := range bs {
			bp := fmt.Sprintf("%s/%d", path, i)
			if !toolCommand.MatchString(b.Tool) {
				add(bp+"/tool", "a button names a tool by its command")
			}
			for k, v := range b.Inputs {
				if s, ok := v.(string); ok {
					reads(bp+"/inputs/"+k, s, withItem)
				}
			}
		}
	}
	switch a.Type {
	case "text":
		if a.Text == "" {
			add("/tool/answer/text", "a text answer says something")
		}
		if !strings.HasPrefix(a.Text, "=") && len([]rune(a.Text)) > AnswerTextMax {
			add("/tool/answer/text", "an answer is at most %d characters", AnswerTextMax)
		}
		reads("/tool/answer/text", a.Text, false)
	case "card":
		reads("/tool/answer/title", a.Title, false)
		reads("/tool/answer/ref", a.Ref, false)
		reads("/tool/answer/amount", a.Amount, false)
		if a.State != nil {
			reads("/tool/answer/state/text", a.State.Text, false)
		}
		if len(a.Lines) > AnswerCardLines {
			add("/tool/answer/lines", "a card has at most %d lines, not %d", AnswerCardLines, len(a.Lines))
		}
		for i, l := range a.Lines {
			reads(fmt.Sprintf("/tool/answer/lines/%d/value", i), l.Value, false)
		}
		buttons("/tool/answer/buttons", a.Buttons, AnswerCardButton, false)
	case "list":
		o, ok := outputs[a.From]
		switch {
		case !ok:
			add("/tool/answer/from", "no output is called %q", a.From)
		case o.Type != "list":
			add("/tool/answer/from", "a list answer's rows come from an output of type list; %q is %s", a.From, o.Type)
		case customers && !o.Public:
			add("/tool/answer/from", "%q is not marked public, and a tool for customers shows only public outputs", a.From)
		}
		reads("/tool/answer/title", a.Title, false)
		if a.Item == nil {
			add("/tool/answer/item", "a list answer says how each row reads")
		} else {
			reads("/tool/answer/item/title", a.Item.Title, true)
			reads("/tool/answer/item/sub", a.Item.Sub, true)
			if a.Item.State != nil {
				reads("/tool/answer/item/state/text", a.Item.State.Text, true)
			}
		}
		buttons("/tool/answer/actions", a.Actions, AnswerRowButtons, true)
	}
	return out
}
