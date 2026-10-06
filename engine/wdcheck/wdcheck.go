// Package wdcheck validates a workflow definition against the wd/v1
// contract and against a platform: its connectors and actions must exist,
// its code must compile, and its trigger must be one the platform can serve.
// The API checks with it before publishing; the CLI checks with it offline.
package wdcheck

import (
	"fmt"

	"github.com/israel-duff/taskiem/engine/connector"
	"github.com/israel-duff/taskiem/engine/ingest"
	"github.com/israel-duff/taskiem/engine/sandbox"
	"github.com/israel-duff/taskiem/engine/wd"
)

// Problem is one validation failure.
type Problem struct {
	Path    string `json:"path"`
	Message string `json:"message"`
}

func (p Problem) String() string { return p.Path + ": " + p.Message }

// Check validates doc. Contract problems are reported alone: platform checks
// need a definition that parses.
func Check(doc []byte, reg *connector.Registry) []Problem {
	var out []Problem
	for _, p := range wd.Validate(doc) {
		out = append(out, Problem{p.Path, p.Message})
	}
	if len(out) > 0 {
		return out
	}
	def, err := wd.Load(doc)
	if err != nil {
		return []Problem{{"/", err.Error()}}
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
					out = append(out, Problem{path, fmt.Sprintf("connector %q is not available", st.Connector)})
				case !hasAction(c, st.Action):
					out = append(out, Problem{path, fmt.Sprintf("connector %q has no action %q", st.Connector, st.Action)})
				case st.Compensate != nil && !hasAction(c, st.Compensate.Action):
					out = append(out, Problem{path + "/compensate", fmt.Sprintf("connector %q has no action %q", st.Connector, st.Compensate.Action)})
				}
			case "code":
				if st.Code != nil {
					if _, err := sandbox.Compile(st.Code.Source, st.Code.Language); err != nil {
						out = append(out, Problem{path, "code: " + err.Error()})
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
		out = append(out, Problem{"/trigger", err.Error()})
	}
	return out
}

func hasAction(c *connector.Connector, action string) bool {
	_, ok := c.Manifest.Actions[action]
	return ok
}
