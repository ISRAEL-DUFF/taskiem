package main

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"unicode"

	"github.com/israel-duff/taskiem/engine/ai"
	"github.com/israel-duff/taskiem/engine/ai/builder"
	"github.com/israel-duff/taskiem/engine/ai/repair"
)

// heuristic is the evaluation's offline "model": it reads the context
// document the builder put in the prompt; when the goal sequences what two
// callable starting templates do, it composes them (compose); when a starting template is
// offered with a strong retrieval score it answers with that template and no parameters (the builder
// instantiates it and lists what is missing); otherwise it wires the
// best-matching action of up to two retrieved connectors into a manual
// workflow, behind an approval when one of them moves money. It is a
// deterministic floor for the report and a regression check of the
// pipeline, not an attempt at a model.
func heuristic(req ai.Request) (*ai.Response, error) {
	prompt := req.Messages[0].Text
	goal := strings.ToLower(builder.GoalFrom(prompt))
	ctxDoc, _ := builder.ContextFrom(prompt)
	if resp := compose(goal, ctxDoc); resp != nil {
		return resp, nil
	}
	if tpls, _ := ctxDoc["starting_templates"].([]any); len(tpls) > 0 && templateScore(tpls[0]) >= heuristicTemplateScore {
		t, _ := tpls[0].(map[string]any)
		id, _ := t["id"].(string)
		env, _ := json.Marshal(map[string]any{"summary": "Starts from a template.", "assumptions": []string{}, "workflow": "", "tests": []any{},
			"template": id, "template_params": "{}"})
		return &ai.Response{Text: string(env)}, nil
	}
	conns, _ := ctxDoc["relevant_connectors"].([]any)
	type pick struct {
		ref, action string
		write       bool
		category    string
		input       any
	}
	var picks []pick
	for _, raw := range conns {
		if len(picks) == 2 {
			break
		}
		c, _ := raw.(map[string]any)
		actions, _ := c["actions"].(map[string]any)
		names := make([]string, 0, len(actions))
		for n := range actions {
			names = append(names, n)
		}
		sort.Strings(names)
		best, bestScore := "", -1
		for _, n := range names {
			a, _ := actions[n].(map[string]any)
			score := 0
			for _, w := range tokens(n + " " + fmt.Sprint(a["title"])) {
				if strings.Contains(goal, w) {
					score += 2
				}
			}
			if a["class"] == "read" {
				score++ // prefer reads on a tie: safer
			}
			if score > bestScore {
				best, bestScore = n, score
			}
		}
		if best == "" {
			continue
		}
		a, _ := actions[best].(map[string]any)
		class, _ := a["class"].(string)
		cat, _ := c["category"].(string)
		ref, _ := c["ref"].(string)
		picks = append(picks, pick{ref: ref, action: best, write: class != "read", category: cat, input: builder.Sample(a["input"])})
	}
	var steps []map[string]any
	gate := false
	for _, p := range picks {
		if p.write && p.category == "payments" {
			gate = true
		}
	}
	if gate {
		steps = append(steps, map[string]any{"id": "approve", "type": "approval", "config": map[string]any{"role": "approver", "count": 1, "timeout": "24h"}})
	}
	var prev string
	for i, p := range picks {
		st := map[string]any{"id": fmt.Sprintf("step%d", i+1), "type": "connector", "connector": p.ref, "action": p.action, "input": p.input}
		var needs []string
		if prev != "" {
			needs = append(needs, prev)
		}
		if gate && p.write && p.category == "payments" {
			needs = append(needs, "approve")
			st["when"] = "=steps.approve.output.decision == 'approved'"
		}
		if len(needs) > 0 {
			st["needs"] = needs
		}
		steps = append(steps, st)
		prev = st["id"].(string)
	}
	if len(steps) == 0 {
		steps = append(steps, map[string]any{"id": "note", "type": "transform", "config": map[string]any{"output": map[string]any{"todo": "build by hand"}}})
	}
	def := map[string]any{"schema": "wd/v1", "id": "wf_draft", "version": 1, "name": "Draft", "trigger": map[string]any{"type": "manual"}, "steps": steps}
	doc, _ := json.Marshal(def)
	env, _ := json.Marshal(map[string]any{"summary": "Heuristic draft.", "assumptions": []string{}, "workflow": string(doc), "tests": []any{},
		"template": "", "template_params": ""})
	return &ai.Response{Text: string(env)}, nil
}

// sequenced are the words of a goal that asks for one piece of work after
// another.
var sequenced = regexp.MustCompile(`\b(then|after that|afterwards|once that is done|once it is done)\b`)

// compose answers a goal that sequences work two strongly matched callable
// templates do (decision 0047) with a template step for each, in order,
// their required run inputs taken from the run's own: the floor for the
// composition suite.
func compose(goal string, ctxDoc map[string]any) *ai.Response {
	tpls, _ := ctxDoc["starting_templates"].([]any)
	if len(tpls) < 2 || !sequenced.MatchString(goal) {
		return nil
	}
	props, required := map[string]any{}, []string{}
	var steps []map[string]any
	for _, raw := range tpls[:2] {
		t, _ := raw.(map[string]any)
		id, _ := t["id"].(string)
		if _, callable := t["callable"]; !callable || templateScore(t) < heuristicTemplateScore {
			return nil
		}
		var def struct {
			Inputs struct {
				Schema struct {
					Properties map[string]any `json:"properties"`
					Required   []string       `json:"required"`
				} `json:"schema"`
			} `json:"inputs"`
		}
		raw, _ := json.Marshal(t["definition"])
		_ = json.Unmarshal(raw, &def)
		input := map[string]any{}
		for _, r := range def.Inputs.Schema.Required {
			props[r] = def.Inputs.Schema.Properties[r]
			required = append(required, r)
			input[r] = "=trigger.body." + r
		}
		// Required parameters without a default get a placeholder: the
		// person fills them in on review.
		params := map[string]any{}
		ps, _ := t["params"].([]any)
		for _, raw := range ps {
			p, _ := raw.(map[string]any)
			name, _ := p["name"].(string)
			if req, _ := p["required"].(bool); !req || p["default"] != nil || name == "" {
				continue
			}
			switch p["type"] {
			case "integer", "number":
				params[name] = 1
			case "boolean":
				params[name] = false
			default:
				params[name] = "Sample"
			}
		}
		step := map[string]any{"id": fmt.Sprintf("part%d", len(steps)+1), "type": "template", "template": id, "input": input}
		if len(params) > 0 {
			step["params"] = params
		}
		if len(steps) > 0 {
			step["needs"] = []string{steps[len(steps)-1]["id"].(string)}
		}
		steps = append(steps, step)
	}
	def := map[string]any{"schema": "wd/v1", "id": "wf_draft", "version": 1, "name": "Draft", "trigger": map[string]any{"type": "manual"}, "steps": steps}
	if len(required) > 0 {
		sort.Strings(required)
		def["inputs"] = map[string]any{"schema": map[string]any{"type": "object", "required": required, "properties": props}}
	}
	doc, _ := json.Marshal(def)
	env, _ := json.Marshal(map[string]any{"summary": "Composes two templates.", "assumptions": []string{}, "workflow": string(doc), "tests": []any{},
		"template": "", "template_params": ""})
	return &ai.Response{Text: string(env)}
}

// heuristicTemplateScore is the retrieval score above which the
// heuristic trusts a starting template (a model reads the template).
const heuristicTemplateScore = 20

func templateScore(t any) float64 {
	m, _ := t.(map[string]any)
	match, _ := m["match"].(map[string]any)
	s, _ := match["score"].(float64)
	return s
}

func tokens(s string) []string {
	var out []string
	for _, w := range strings.FieldsFunc(strings.ToLower(s), func(r rune) bool { return !unicode.IsLetter(r) }) {
		if len(w) >= 4 {
			out = append(out, w)
		}
	}
	return out
}

var reClassHint = regexp.MustCompile(`(?:of class|best guess is) ([a-z_]+)`)

// repairHeuristic is the offline repair "model": it keeps the class it
// is told (or the rules' guess), and for classes that patch returns the
// workflow with only its description changed. A floor: such a patch is
// valid and changes the definition but fixes nothing, so it fails any
// recorded failing case.
func repairHeuristic(req ai.Request) (*ai.Response, error) {
	prompt := req.Messages[0].Text
	class := repair.Logic
	if m := reClassHint.FindStringSubmatch(prompt); m != nil {
		class = repair.Class(m[1])
	}
	if class == repair.UnknownOutcome {
		class = repair.Logic
	}
	workflow := ""
	if class.Patches() {
		var def map[string]any
		if err := json.Unmarshal([]byte(repair.WorkflowFrom(prompt)), &def); err == nil {
			def["description"] = "Patched by the offline heuristic."
			raw, _ := json.Marshal(def)
			workflow = string(raw)
		}
	}
	env, _ := json.Marshal(map[string]any{"class": class, "explanation": "Offline heuristic.", "workflow": workflow,
		"test": map[string]any{"name": "", "case": ""}})
	return &ai.Response{Text: string(env)}, nil
}
