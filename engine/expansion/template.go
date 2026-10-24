package expansion

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strings"

	"github.com/israel-duff/taskiem/engine/expr"
	"github.com/israel-duff/taskiem/engine/wd"
	"github.com/israel-duff/taskiem/templates"
)

// Template steps (decision 0047, wd-v1 template steps) use a callable
// template inside another workflow. Each is expanded, before capability
// steps, into:
//
//   - <id>__body, a branch with one path that always runs, holding
//     <id>__input (a transform giving the step's input) and the template's
//     steps, their ids prefixed <id>__; it takes the template step's needs,
//     when and on_error. A branch completes when its steps have run or
//     been skipped, so a template whose steps branch on an approval still
//     ends;
//   - <id>, a transform that needs the body and gives the template's
//     outputs, so steps.<id>.output is the template's result.
//
// The template's expressions read their run inputs (trigger.body) from
// <id>__input, and its steps by their prefixed ids. Each expanded step
// keeps its id inside the template as origin.id, the name its output has
// in its scope's results (wd.Step.Key), so the template's reads of a
// foreach iteration's or a handled step's outputs are unchanged.

// Templates finds a callable template by id: the library's, for a draft,
// or the one pinned when a version was saved.
type Templates func(id string) (*templates.Template, error)

// Library finds templates in lib.
func Library(lib *templates.Library) Templates {
	return func(id string) (*templates.Template, error) {
		t, ok := lib.Get(id)
		if !ok {
			return nil, fmt.Errorf("there is no template %q", id)
		}
		return t, nil
	}
}

// MaxTemplateDepth is how deep templates nest, counting the calling
// workflow: a workflow, a template it uses, and a template that one uses.
const MaxTemplateDepth = 3

// MaxStepID is the longest a step id may be (wd-v1 $defs.stepId).
const MaxStepID = 63

// HasTemplates reports whether a definition has template steps.
func HasTemplates(d *wd.Definition) bool { return d.HasTemplates() }

// ExpandTemplates expands every template step of doc, at every depth, and
// returns the expanded definition and the ids of the templates it used.
// It returns doc unchanged when there is no template step.
func ExpandTemplates(doc []byte, find Templates) ([]byte, []string, error) {
	d, err := wd.Load(doc)
	if err != nil {
		return nil, nil, err
	}
	if !d.HasTemplates() {
		return doc, nil, nil
	}
	root, err := expr.DecodeJSON(doc)
	if err != nil {
		return nil, nil, err
	}
	x := &templater{find: find, used: map[string]bool{}}
	m := root.(map[string]any)
	if m["steps"], err = x.steps(m["steps"].([]any), 1, nil); err != nil {
		return nil, nil, err
	}
	if err := checkIDs(m["steps"].([]any)); err != nil {
		return nil, nil, err
	}
	out, err := json.Marshal(m)
	if err != nil {
		return nil, nil, err
	}
	if _, err := wd.Load(out); err != nil {
		return nil, nil, fmt.Errorf("template expansion: %w", err)
	}
	used := make([]string, 0, len(x.used))
	for id := range x.used {
		used = append(used, id)
	}
	sort.Strings(used)
	return out, used, nil
}

// ExpandDefinition is ExpandTemplates for a loaded definition.
func ExpandDefinition(def *wd.Definition, find Templates) (*wd.Definition, error) {
	doc, err := json.Marshal(def)
	if err != nil {
		return nil, err
	}
	out, _, err := ExpandTemplates(doc, find)
	if err != nil {
		return nil, err
	}
	return wd.Load(out)
}

type templater struct {
	find Templates
	used map[string]bool
}

// steps expands the template steps of one list, and of every list nested
// in it. depth is the depth of the definition the list is in (1 for the
// calling workflow), and chain the templates being expanded around it.
func (x *templater) steps(list []any, depth int, chain []string) ([]any, error) {
	var out []any
	for _, raw := range list {
		st := raw.(map[string]any)
		if err := eachScope(st, func(l []any) ([]any, error) { return x.steps(l, depth, chain) }); err != nil {
			return nil, err
		}
		if st["type"] != "template" {
			out = append(out, st)
			continue
		}
		exp, err := x.template(st, depth, chain)
		if err != nil {
			return nil, err
		}
		out = append(out, exp...)
	}
	return out, nil
}

// eachScope replaces each list of steps nested in st by fn's result.
func eachScope(st map[string]any, fn func([]any) ([]any, error)) error {
	var err error
	if oe, ok := st["on_error"].(map[string]any); ok {
		if oe["steps"], err = fn(oe["steps"].([]any)); err != nil {
			return err
		}
	}
	cfg, _ := st["config"].(map[string]any)
	if cfg == nil {
		return nil
	}
	for _, key := range []string{"paths", "branches"} {
		list, _ := cfg[key].([]any)
		for _, p := range list {
			pm := p.(map[string]any)
			if pm["steps"], err = fn(pm["steps"].([]any)); err != nil {
				return err
			}
		}
	}
	if def, ok := cfg["default"].(map[string]any); ok {
		if def["steps"], err = fn(def["steps"].([]any)); err != nil {
			return err
		}
	}
	if st["type"] == "foreach" {
		if cfg["steps"], err = fn(cfg["steps"].([]any)); err != nil {
			return err
		}
	}
	return nil
}

func (x *templater) template(st map[string]any, depth int, chain []string) ([]any, error) {
	id, _ := st["id"].(string)
	tid, _ := st["template"].(string)
	refuse := func(format string, args ...any) error {
		return &Refusal{Step: id, Message: fmt.Sprintf(format, args...)}
	}
	if slices.Contains(chain, tid) {
		return nil, refuse("template %s uses itself (%s)", tid, strings.Join(append(slices.Clone(chain), tid), " uses "))
	}
	if depth >= MaxTemplateDepth {
		return nil, refuse("templates nest at most %d deep, counting the workflow: the workflow uses %s, which cannot use %s", MaxTemplateDepth, strings.Join(chain, ", which uses "), tid)
	}
	tpl, err := x.find(tid)
	if err != nil {
		return nil, refuse("%v", err)
	}
	if !tpl.Callable {
		return nil, refuse("template %s cannot be a step: it is not callable", tid)
	}
	params, _ := st["params"].(map[string]any)
	tdoc, err := tpl.Instantiate(params, "")
	if err != nil {
		return nil, refuse("template %s: %v", tid, err)
	}
	troot, err := expr.DecodeJSON(tdoc)
	if err != nil {
		return nil, err
	}
	tm := troot.(map[string]any)
	input, _ := st["input"].(map[string]any)
	if input == nil {
		input = map[string]any{}
	}
	if err := inputFits(tm, input); err != nil {
		return nil, refuse("template %s: %v", tid, err)
	}
	inner, err := x.steps(tm["steps"].([]any), depth+1, append(slices.Clone(chain), tid))
	if err != nil {
		// Said of this step: the one the calling workflow has.
		var ref *Refusal
		if errors.As(err, &ref) {
			return nil, &Refusal{Step: id, Message: ref.Message, Err: ref.Err}
		}
		return nil, err
	}
	x.used[tid] = true

	prefix := id + "__"
	inputID, bodyID := prefix+"input", prefix+"body"
	names := map[string]string{}
	collectIDs(inner, func(sid string) { names[sid] = prefix + sid })
	if names["input"] != "" || names["body"] != "" {
		return nil, refuse("template %s has a step named input or body, which its expansion uses", tid)
	}
	rw := &rewriter{names: names, input: inputID}
	for _, s := range inner {
		rw.step(s.(map[string]any), tid, id, true)
	}
	for _, s := range inner {
		sm := s.(map[string]any)
		if _, ok := sm["needs"]; !ok {
			sm["needs"] = []any{inputID}
		}
	}

	name, _ := st["name"].(string)
	if name == "" {
		name = tpl.Title
	}
	origin := map[string]any{"template": tid, "step": id}
	inputStep := map[string]any{"id": inputID, "type": "transform", "name": "Inputs for " + lowerFirst(name),
		"config": map[string]any{"output": input}, "origin": origin}
	body := map[string]any{"id": bodyID, "type": "branch", "name": name, "origin": origin,
		"config": map[string]any{"paths": []any{map[string]any{"name": "template", "when": "=true", "steps": append([]any{inputStep}, inner...)}}}}
	for _, k := range []string{"needs", "when", "description"} {
		if v, ok := st[k]; ok {
			body[k] = v
		}
	}
	if oe, ok := st["on_error"]; ok {
		// The calling workflow's on_error reads the failure of the body.
		body["on_error"] = (&rewriter{names: map[string]string{id: bodyID}}).value(oe)
	}
	outs := map[string]any{}
	for _, o := range tpl.Outputs {
		outs[o.Name] = outputExpr(o.Value, bodyID)
	}
	result := map[string]any{"id": id, "type": "transform", "name": name + ": result", "needs": []any{bodyID},
		"config": map[string]any{"output": outs}}
	return []any{body, result}, nil
}

// inputFits checks a template step's input against the template's run
// inputs schema: every required input is given, and none it does not
// take. The values themselves are checked when they are known, at run
// time, by the template's own steps.
func inputFits(tdef map[string]any, input map[string]any) error {
	in, _ := tdef["inputs"].(map[string]any)
	schema, _ := in["schema"].(map[string]any)
	props, _ := schema["properties"].(map[string]any)
	req, _ := schema["required"].([]any)
	var missing, extra []string
	for _, r := range req {
		if s, _ := r.(string); s != "" {
			if _, ok := input[s]; !ok {
				missing = append(missing, s)
			}
		}
	}
	if ap, ok := schema["additionalProperties"].(bool); ok && !ap {
		for k := range input {
			if _, ok := props[k]; !ok {
				extra = append(extra, k)
			}
		}
	}
	sort.Strings(missing)
	sort.Strings(extra)
	switch {
	case len(missing) > 0:
		return fmt.Errorf("the input needs %s", strings.Join(missing, ", "))
	case len(extra) > 0:
		return fmt.Errorf("the input has %s, which the template does not take", strings.Join(extra, ", "))
	}
	return nil
}

// collectIDs calls fn with the id of every step in list, at any depth.
func collectIDs(list []any, fn func(string)) {
	var walk func(v any)
	walk = func(v any) {
		switch t := v.(type) {
		case map[string]any:
			if id, ok := t["id"].(string); ok && t["type"] != nil {
				fn(id)
			}
			for _, c := range t {
				walk(c)
			}
		case []any:
			for _, c := range t {
				walk(c)
			}
		}
	}
	walk(list)
}

// rewriter renames steps (their ids, needs, and every expression's reads
// of them) and makes trigger.body read input's output.
type rewriter struct {
	names map[string]string
	input string // the input transform's id; "" leaves trigger.body
	re    *regexp.Regexp
}

// step rewrites one step and every step nested in it, marking each with
// its origin: the template, the template step, and its id inside the
// template. A step a nested expansion already marked keeps its origin, its
// template step renamed.
func (r *rewriter) step(st map[string]any, tid, tstep string, mark bool) {
	for k, v := range st {
		switch k {
		case "id":
			old := v.(string)
			st[k] = r.names[old]
			if !mark {
				continue
			}
			if o, ok := st["origin"].(map[string]any); ok {
				if s, ok := o["step"].(string); ok && r.names[s] != "" {
					o["step"] = r.names[s]
				}
			} else {
				st["origin"] = map[string]any{"template": tid, "step": tstep, "id": old}
			}
		case "needs":
			list, _ := v.([]any)
			for i, n := range list {
				if s, ok := n.(string); ok && r.names[s] != "" {
					list[i] = r.names[s]
				}
			}
		case "origin":
		case "on_error":
			oe := v.(map[string]any)
			for _, s := range oe["steps"].([]any) {
				r.step(s.(map[string]any), tid, tstep, mark)
			}
			for ok, ov := range oe {
				if ok != "steps" {
					oe[ok] = r.value(ov)
				}
			}
		case "config":
			cfg, _ := v.(map[string]any)
			for ck, cv := range cfg {
				switch {
				case (ck == "paths" || ck == "branches") && (st["type"] == "branch" || st["type"] == "parallel"):
					for _, p := range cv.([]any) {
						pm := p.(map[string]any)
						for pk, pv := range pm {
							if pk == "steps" {
								for _, s := range pv.([]any) {
									r.step(s.(map[string]any), tid, tstep, mark)
								}
							} else {
								pm[pk] = r.value(pv)
							}
						}
					}
				case ck == "default" && st["type"] == "branch":
					for _, s := range cv.(map[string]any)["steps"].([]any) {
						r.step(s.(map[string]any), tid, tstep, mark)
					}
				case ck == "steps" && st["type"] == "foreach":
					for _, s := range cv.([]any) {
						r.step(s.(map[string]any), tid, tstep, mark)
					}
				default:
					cfg[ck] = r.value(cv)
				}
			}
		default:
			st[k] = r.value(v)
		}
	}
}

// value rewrites every expression in v.
func (r *rewriter) value(v any) any {
	switch t := v.(type) {
	case map[string]any:
		for k, c := range t {
			t[k] = r.value(c)
		}
		return t
	case []any:
		for i, c := range t {
			t[i] = r.value(c)
		}
		return t
	case string:
		if strings.HasPrefix(t, "=") {
			return "=" + r.expr(t[1:])
		}
	}
	return v
}

// expr renames the steps an expression reads (steps.x, steps.?x,
// steps["x"]), and replaces its reads of trigger.body. Only the root
// steps is renamed: a field named steps (a branch's output) keeps its keys,
// which are the ids inside the template.
func (r *rewriter) expr(src string) string {
	if r.re == nil && len(r.names) > 0 {
		alts := make([]string, 0, len(r.names))
		for n := range r.names {
			alts = append(alts, regexp.QuoteMeta(n))
		}
		sort.Slice(alts, func(i, j int) bool { return len(alts[i]) > len(alts[j]) })
		r.re = regexp.MustCompile(`(^|[^.\w])(steps\s*(?:\.\s*\??\s*|\[\s*\??\s*["']))(` + strings.Join(alts, "|") + `)\b`)
	}
	if r.re != nil {
		src = r.re.ReplaceAllStringFunc(src, func(m string) string {
			sub := r.re.FindStringSubmatch(m)
			return sub[1] + sub[2] + r.names[sub[3]]
		})
	}
	if r.input != "" {
		src = triggerBody.ReplaceAllString(src, "${1}steps."+r.input+".output")
	}
	return src
}

var triggerBody = regexp.MustCompile(`(^|[^.\w])trigger\s*\.\s*\??\s*body\b`)

// outputExpr turns an output's reads of the template's top-level steps
// (steps.x.output, steps.?x.output, has(steps.x)) into reads of the body
// branch's results, where each is kept under its id inside the template.
func outputExpr(value, bodyID string) string {
	src := strings.TrimPrefix(value, "=")
	src = outputRead.ReplaceAllString(src, "${1}steps."+bodyID+".output.steps.${2}${3}")
	return "=" + src
}

var outputRead = regexp.MustCompile(`(^|[^.\w])steps\s*\.\s*(\??)\s*([a-z][a-z0-9_]*)(?:\s*\.\s*\??\s*output\b)?`)

// checkIDs refuses an expanded step id longer than a step id may be, or a
// capability step's id longer than MaxCapabilityStepID (its composite's
// steps add __<name> when it is expanded), naming the template step that
// made it.
func checkIDs(list []any) error {
	var bad error
	var walk func(v any)
	walk = func(v any) {
		if bad != nil {
			return
		}
		switch t := v.(type) {
		case map[string]any:
			id, ok := t["id"].(string)
			if ok && t["type"] != nil {
				o, _ := t["origin"].(map[string]any)
				limit := MaxStepID
				if t["type"] == "capability" {
					limit = wd.MaxCapabilityStepID
				}
				if len(id) > limit && o != nil {
					bad = &Refusal{Step: strings.SplitN(id, "__", 2)[0], Message: fmt.Sprintf("the step %s of template %s would be %s, longer than the %d characters it may have: give the template step a shorter id", o["id"], o["template"], id, limit)}
					return
				}
			}
			for _, c := range t {
				walk(c)
			}
		case []any:
			for _, c := range t {
				walk(c)
			}
		}
	}
	walk(list)
	return bad
}
