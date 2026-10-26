// Package wdcheck validates a workflow definition against the wd/v1
// contract and against a platform: its connectors and actions must exist,
// its code must compile, and its trigger must be one the platform can serve.
// The API checks with it before publishing; the CLI checks with it offline.
package wdcheck

import (
	"errors"
	"fmt"
	"strings"

	"github.com/israel-duff/taskiem/engine/connector"
	"github.com/israel-duff/taskiem/engine/expansion"
	"github.com/israel-duff/taskiem/engine/ingest"
	"github.com/israel-duff/taskiem/engine/plain"
	"github.com/israel-duff/taskiem/engine/sandbox"
	"github.com/israel-duff/taskiem/engine/wd"
	"github.com/israel-duff/taskiem/templates"
)

// Problem is one validation failure.
type Problem struct {
	Path    string `json:"path"`
	Message string `json:"message"`
	// Plain says it for a business owner, naming the step by its name, and
	// Fix is where it is put right (engine/plain; no-technical-steps N5).
	Plain string     `json:"plain,omitempty"`
	Fix   *plain.Fix `json:"fix,omitempty"`
}

func pr(path, message string) Problem { return Problem{Path: path, Message: message} }

func (p Problem) String() string { return p.Path + ": " + p.Message }

// Check validates doc, its template steps expanded with the template
// library. Contract problems are reported alone: platform checks need a
// definition that parses. Each problem is also said in plain words.
func Check(doc []byte, reg connector.Lookup) []Problem {
	return CheckWith(doc, reg, expansion.Library(templates.Default()))
}

// CheckWith is Check with the templates a version pinned.
func CheckWith(doc []byte, reg connector.Lookup, find expansion.Templates) []Problem {
	out := check(doc, reg, find)
	steps := plain.StepsOf(doc)
	for i := range out {
		text, fix := plain.Problem(out[i].Path, out[i].Message, steps)
		out[i].Plain = text
		if fix.Path != "" {
			out[i].Fix = &fix
		}
	}
	return out
}

func check(doc []byte, reg connector.Lookup, find expansion.Templates) []Problem {
	var out []Problem
	for _, p := range wd.Validate(doc) {
		out = append(out, pr(p.Path, p.Message))
	}
	if len(out) > 0 {
		return out
	}
	def, err := wd.Load(doc)
	if err != nil {
		return []Problem{pr("/", err.Error())}
	}
	def.Walk(func(st *wd.Step) {
		if st.Origin != nil {
			out = append(out, pr("/steps/"+st.ID+"/origin", "set when a template step is expanded; write a template step instead"))
		}
	})
	if len(out) > 0 {
		return out
	}
	// A template's steps are checked as the workflow's own: what a
	// template needs or does cannot hide behind its step (decision 0047).
	if def.HasTemplates() {
		written := def
		expanded, _, err := expansion.ExpandTemplates(doc, find)
		var ref *expansion.Refusal
		switch {
		case errors.As(err, &ref):
			return []Problem{pr("/steps/"+ref.Step, ref.Message)}
		case err != nil:
			return []Problem{pr("/", err.Error())}
		}
		if def, err = wd.Load(expanded); err != nil {
			return []Problem{pr("/", err.Error())}
		}
		out = append(out, aroundProduct(written, def, reg)...)
	}
	var walk func(steps []*wd.Step)
	walk = func(steps []*wd.Step) {
		for _, st := range steps {
			path := "/steps/" + st.ID
			switch st.Type {
			case "connector":
				c, ok := reg.Get(st.Connector)
				switch {
				case !ok:
					out = append(out, pr(path, fmt.Sprintf("connector %q is not available", st.Connector)))
				case !hasAction(c, st.Action):
					out = append(out, pr(path, fmt.Sprintf("connector %q has no action %q", st.Connector, st.Action)))
				case st.Compensate != nil && !hasAction(c, st.Compensate.Action):
					out = append(out, pr(path+"/compensate", fmt.Sprintf("connector %q has no action %q", st.Connector, st.Compensate.Action)))
				case c.CheckInput != nil:
					for _, msg := range c.CheckInput(st.Action, st.Input) {
						out = append(out, pr(path+"/input", msg))
					}
				}
				if st.Capability != nil {
					out = append(out, pr(path+"/capability", "set when a capability step is expanded; write a capability step instead"))
				}
			case "capability":
				if !wd.IsWait(st.Uses) && !anyRail(reg, st.Uses) {
					out = append(out, pr(path, fmt.Sprintf("no rail Taskiem connects to can %s yet", st.Uses)))
				}
			case "code":
				if st.Code != nil {
					if _, err := sandbox.Compile(st.Code.Source, st.Code.Language); err != nil {
						out = append(out, pr(path, "code: "+err.Error()))
					}
				}
			}
			for _, sub := range st.Children() {
				walk(sub)
			}
		}
	}
	walk(def.Steps)
	if err := ingest.Check(def, reg); err != nil {
		out = append(out, pr("/trigger", err.Error()))
	}
	return out
}

func hasAction(c *connector.Connector, action string) bool {
	_, ok := c.Manifest.Actions[action]
	return ok
}

// anyRail reports whether a rail the platform has declares the capability.
func anyRail(reg connector.Lookup, uses string) bool {
	for _, c := range reg.List() {
		if _, ok := c.Manifest.Capabilities[uses]; ok && c.Manifest.KindOf() == "rail" {
			return true
		}
	}
	return false
}

// aroundProduct refuses, in a workflow around a product (decision 0044,
// milestone R7: the product pays, Taskiem tells, records and links), a
// template step whose template moves money (decision 0047).
func aroundProduct(written, expanded *wd.Definition, reg connector.Lookup) []Problem {
	product := ""
	isProduct := func(ref string) {
		if c, ok := reg.Get(ref); ok && c.Manifest.KindOf() == "product" && product == "" {
			product = c.Manifest.Name
		}
	}
	if written.Trigger.Type == "connector_event" {
		ref, _ := written.Trigger.Config["connector"].(string)
		isProduct(ref)
	}
	expanded.Walk(func(st *wd.Step) {
		if st.Connector != "" {
			isProduct(st.Connector)
		}
	})
	if product == "" {
		return nil
	}
	var out []Problem
	seen := map[string]bool{}
	expanded.Walk(func(st *wd.Step) {
		if st.Origin == nil || templates.StepEffect(st, reg) != templates.EffectMovesMoney {
			return
		}
		top, _, _ := strings.Cut(st.Origin.Step, "__")
		if seen[top] {
			return
		}
		seen[top] = true
		tpl := st.Origin.Template
		if t := written.Step(top); t != nil {
			tpl = t.Template
		}
		out = append(out, pr("/steps/"+top, fmt.Sprintf("this workflow works around %s, which makes the payments; the template %s moves money, so it cannot be a step here", product, tpl)))
	})
	return out
}
