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
// document the builder put in the prompt; when a starting template is
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
