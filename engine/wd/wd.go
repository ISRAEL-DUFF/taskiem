// Package wd validates Workflow Definitions against the wd/v1 contract:
// the JSON Schema plus the semantic rules in docs/contracts/wd-v1.md.
package wd

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/israel-duff/taskiem/engine/internal/schemacheck"
	"github.com/israel-duff/taskiem/schemas"
)

var schema = schemacheck.New("https://schemas.taskiem.dev/wd/v1.json", schemas.WDv1)

// Problem is one validation failure.
type Problem struct {
	Path    string // JSON pointer, e.g. /steps/2/needs
	Message string
}

func (p Problem) String() string { return p.Path + ": " + p.Message }

// Validate returns every problem found in a WD document; nil means valid.
// Structural (schema) problems are reported first; semantic checks run only
// on structurally valid documents.
func Validate(doc []byte) []Problem {
	if msgs := schema.Validate(doc); len(msgs) > 0 {
		out := make([]Problem, len(msgs))
		for i, m := range msgs {
			path, msg, _ := strings.Cut(m, ": ")
			out[i] = Problem{Path: path, Message: "schema: " + msg}
		}
		return out
	}
	var root map[string]any
	if err := json.Unmarshal(doc, &root); err != nil {
		return []Problem{{Path: "/", Message: err.Error()}}
	}
	v := &validator{ids: map[string]string{}}
	v.scope("/steps", root["steps"].([]any))
	v.secretsOutsideSteps(root)
	sort.SliceStable(v.problems, func(i, j int) bool { return v.problems[i].Path < v.problems[j].Path })
	if len(v.problems) > 0 {
		return v.problems
	}
	d, err := parse(doc)
	if err != nil {
		return []Problem{{Path: "/", Message: err.Error()}}
	}
	return checkExpressions(d)
}

type validator struct {
	ids      map[string]string // step id -> path of first definition
	problems []Problem
}

func (v *validator) add(path, format string, args ...any) {
	v.problems = append(v.problems, Problem{Path: path, Message: fmt.Sprintf(format, args...)})
}

// scope checks one list of steps: ids, needs, cycles, then recurses.
func (v *validator) scope(path string, steps []any) {
	local := map[string]int{}
	for i, s := range steps {
		st := s.(map[string]any)
		id := st["id"].(string)
		p := fmt.Sprintf("%s/%d", path, i)
		if first, dup := v.ids[id]; dup {
			v.add(p+"/id", "step id %q already defined at %s", id, first)
		} else {
			v.ids[id] = p
		}
		local[id] = i
	}
	graph := make(map[string][]string, len(steps))
	for i, s := range steps {
		st := s.(map[string]any)
		id := st["id"].(string)
		p := fmt.Sprintf("%s/%d", path, i)
		needs, _ := st["needs"].([]any)
		for j, n := range needs {
			ns := n.(string)
			if ns == id {
				v.add(fmt.Sprintf("%s/needs/%d", p, j), "step %q needs itself", id)
				continue
			}
			if _, ok := local[ns]; !ok {
				if _, elsewhere := v.ids[ns]; elsewhere {
					v.add(fmt.Sprintf("%s/needs/%d", p, j), "%q is not in the same scope as %q", ns, id)
				} else {
					v.add(fmt.Sprintf("%s/needs/%d", p, j), "unknown step %q", ns)
				}
				continue
			}
			graph[id] = append(graph[id], ns)
		}
		v.secretsInStep(p, st)
		v.children(p, st)
	}
	if cyc := findCycle(steps, graph); cyc != nil {
		v.add(path, "needs form a cycle: %s", strings.Join(cyc, " -> "))
	}
}

func (v *validator) children(p string, st map[string]any) {
	if oe, ok := st["on_error"].(map[string]any); ok {
		v.scope(p+"/on_error/steps", oe["steps"].([]any))
	}
	cfg, _ := st["config"].(map[string]any)
	switch st["type"] {
	case "branch":
		names := map[string]bool{}
		for i, pth := range cfg["paths"].([]any) {
			pm := pth.(map[string]any)
			pp := fmt.Sprintf("%s/config/paths/%d", p, i)
			if n := pm["name"].(string); names[n] {
				v.add(pp+"/name", "duplicate path name %q", n)
			} else {
				names[n] = true
			}
			v.scope(pp+"/steps", pm["steps"].([]any))
		}
		if d, ok := cfg["default"].(map[string]any); ok {
			v.scope(p+"/config/default/steps", d["steps"].([]any))
		}
	case "parallel":
		names := map[string]bool{}
		for i, b := range cfg["branches"].([]any) {
			bm := b.(map[string]any)
			bp := fmt.Sprintf("%s/config/branches/%d", p, i)
			if n := bm["name"].(string); names[n] {
				v.add(bp+"/name", "duplicate branch name %q", n)
			} else {
				names[n] = true
			}
			v.scope(bp+"/steps", bm["steps"].([]any))
		}
	case "foreach":
		v.scope(p+"/config/steps", cfg["steps"].([]any))
	}
}

var secretsRef = regexp.MustCompile(`(^|[^A-Za-z0-9_.])secrets\.`)

// Places a step may read secrets (rule 10): only fields sent to the
// provider by that step, never anything recorded in history.
var secretAllowed = []string{"/input", "/config/headers", "/config/url", "/config/body", "/config/query"}

func (v *validator) secretsInStep(p string, st map[string]any) {
	for k, val := range st {
		switch k {
		case "on_error":
			continue // nested scopes are checked on their own
		case "config":
			switch st["type"] {
			case "branch", "parallel", "foreach":
				// only the control fields themselves; nested steps are checked separately
				cfg := val.(map[string]any)
				for ck, cv := range cfg {
					if ck == "paths" || ck == "branches" || ck == "steps" || ck == "default" {
						if ck == "paths" {
							for i, pth := range cv.([]any) {
								v.forbidSecrets(fmt.Sprintf("%s/config/paths/%d/when", p, i), pth.(map[string]any)["when"])
							}
						}
						continue
					}
					v.forbidSecrets(p+"/config/"+ck, cv)
				}
				continue
			}
		}
		v.walkSecrets(p+"/"+k, "/"+k, val)
	}
}

func (v *validator) walkSecrets(path, rel string, val any) {
	switch t := val.(type) {
	case map[string]any:
		for k, c := range t {
			v.walkSecrets(path+"/"+k, rel+"/"+k, c)
		}
	case []any:
		for i, c := range t {
			v.walkSecrets(fmt.Sprintf("%s/%d", path, i), fmt.Sprintf("%s/%d", rel, i), c)
		}
	case string:
		if !isSecretExpr(t) {
			return
		}
		for _, a := range secretAllowed {
			if rel == a || strings.HasPrefix(rel, a+"/") {
				return
			}
		}
		v.add(path, "secrets may only be used in input, config.url, config.headers, config.query or config.body")
	}
}

func (v *validator) forbidSecrets(path string, val any) { v.walkSecrets(path, "/forbidden", val) }

func (v *validator) secretsOutsideSteps(root map[string]any) {
	for _, k := range []string{"trigger", "settings", "inputs"} {
		if val, ok := root[k]; ok {
			v.forbidSecrets("/"+k, val)
		}
	}
}

func isSecretExpr(s string) bool {
	return strings.HasPrefix(s, "=") && secretsRef.MatchString(s[1:])
}

// findCycle returns one cycle in the needs graph, in step order, or nil.
func findCycle(steps []any, graph map[string][]string) []string {
	const (
		white = iota
		grey
		black
	)
	color := map[string]int{}
	var stack []string
	var cycle []string
	var visit func(n string) bool
	visit = func(n string) bool {
		color[n] = grey
		stack = append(stack, n)
		for _, m := range graph[n] {
			switch color[m] {
			case grey:
				for i := len(stack) - 1; i >= 0; i-- {
					if stack[i] == m {
						cycle = append(append([]string{}, stack[i:]...), m)
						return true
					}
				}
			case white:
				if visit(m) {
					return true
				}
			}
		}
		stack = stack[:len(stack)-1]
		color[n] = black
		return false
	}
	for _, s := range steps {
		id := s.(map[string]any)["id"].(string)
		if color[id] == white && visit(id) {
			return cycle
		}
	}
	return nil
}
