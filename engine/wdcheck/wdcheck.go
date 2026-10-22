// Package wdcheck validates a workflow definition against the wd/v1
// contract and against a platform: its connectors and actions must exist,
// its code must compile, and its trigger must be one the platform can serve.
// The API checks with it before publishing; the CLI checks with it offline.
package wdcheck

import (
	"fmt"

	"github.com/israel-duff/taskiem/engine/connector"
	"github.com/israel-duff/taskiem/engine/ingest"
	"github.com/israel-duff/taskiem/engine/plain"
	"github.com/israel-duff/taskiem/engine/sandbox"
	"github.com/israel-duff/taskiem/engine/wd"
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

// Check validates doc. Contract problems are reported alone: platform checks
// need a definition that parses. Each problem is also said in plain words.
func Check(doc []byte, reg connector.Lookup) []Problem {
	out := check(doc, reg)
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

func check(doc []byte, reg connector.Lookup) []Problem {
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
