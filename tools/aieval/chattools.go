package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/israel-duff/taskiem/engine/ai"
	"github.com/israel-duff/taskiem/engine/ai/understand"
	"github.com/israel-duff/taskiem/engine/lang"
)

// The reader and the business's own tools (made tools M3; evals/chat,
// kind tool): a message is read by the model that is told Taskiem's
// commands (the fixture's, as the chat writes them) and a business's
// tools (tools.json), and must reach the tool expected with the values
// expected, or, for a message one of Taskiem's own commands reads, no
// tool at all. In the chat Taskiem's word lists read such messages before
// the model; these cases check the model would not take them either.

// taskiemOwn is a tool case's expectation that no tool is answered.
const taskiemOwn = "taskiem"

// toolFixtures are tools.json: the commands the model is told, and the
// tool sets by name.
type toolFixtures struct {
	Commands string                           `json:"commands"`
	Sets     map[string][]understand.ToolInfo `json:"sets"`
}

func loadToolFixtures(dir string) (toolFixtures, error) {
	var f toolFixtures
	raw, err := os.ReadFile(filepath.Join(dir, "tools.json")) //nolint:gosec // the fixture beside the suite
	if errors.Is(err, os.ErrNotExist) {
		return f, nil
	}
	if err != nil {
		return f, err
	}
	if err := json.Unmarshal(raw, &f); err != nil {
		return f, fmt.Errorf("tools.json: %w", err)
	}
	return f, nil
}

// checkToolCase is a tool case's own check when loaded.
func checkToolCase(c ChatCase, f toolFixtures) error {
	set, ok := f.Sets[c.Tools]
	if !ok {
		return fmt.Errorf("%s: no tool set %q in tools.json", c.ID, c.Tools)
	}
	if c.Expect.Tool == taskiemOwn {
		if len(c.Expect.Values) > 0 {
			return fmt.Errorf("%s: a case for Taskiem's own commands expects no values", c.ID)
		}
		return nil
	}
	i := slices.IndexFunc(set, func(t understand.ToolInfo) bool { return t.Command == c.Expect.Tool })
	if i < 0 {
		return fmt.Errorf("%s: %q is no tool of set %q (or %q)", c.ID, c.Expect.Tool, c.Tools, taskiemOwn)
	}
	for k := range c.Expect.Values {
		if !slices.ContainsFunc(set[i].Inputs, func(in understand.ToolInput) bool { return in.Name == k }) {
			return fmt.Errorf("%s: %q is no input of %s", c.ID, k, c.Expect.Tool)
		}
	}
	return nil
}

// runToolCase asks the reader with the fixture's commands and tools.
func runToolCase(ctx context.Context, prov ai.Provider, c ChatCase, f toolFixtures, info lang.Info, r *ChatResult) *ai.Response {
	r.Expected = c.Expect.Tool + valuesKey(c.Expect.Values)
	res, resp, err := (&understand.Helper{Provider: prov}).Understand(ctx, c.Message, f.Commands, f.Sets[c.Tools], info)
	if err != nil && !errors.Is(err, understand.ErrUnusable) {
		r.Status, r.Error = statusError, err.Error()
		return resp
	}
	if res.Kind != understand.Tool {
		kind := res.Kind
		if kind == "" {
			kind = "unusable"
		}
		r.Got = taskiemOwn + ":" + kind
		r.Correct = c.Expect.Tool == taskiemOwn
		return resp
	}
	r.Got = res.ID + valuesKey(res.Values)
	if c.Expect.Tool != res.ID {
		return resp
	}
	r.Correct = true
	for k, want := range c.Expect.Values {
		if !strings.Contains(plainValue(res.Values[k]), plainValue(want)) {
			r.Correct = false
		}
	}
	return resp
}

// plainValue is a value as graded: lowercase, no thousands commas.
func plainValue(v string) string { return strings.ToLower(strings.ReplaceAll(v, ",", "")) }

func valuesKey(v map[string]string) string {
	if len(v) == 0 {
		return ""
	}
	keys := make([]string, 0, len(v))
	for k := range v {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, len(keys))
	for i, k := range keys {
		parts[i] = k + "=" + v[k]
	}
	return " " + strings.Join(parts, " ")
}

// --- the offline heuristic ---

// heuristicOwn are words of Taskiem's own commands, in English and
// Pidgin, that the heuristic leaves to them.
var heuristicOwn = []string{"send ", "pay ", "invoice", "owe", "help", "status", "approvals", "sold ", "stock"}

var amountRe = regexp.MustCompile(`(?i)\b(\d[\d,]*(?:\.\d+)?)(k|m)?\b`)

// heuristicTool stands in for the reader given tools: Taskiem's own words
// first, then the tool whose words the message shares most, with a number
// for its number input, an @name or the word after "to"/"reach" for a
// place, and the word after "on"/"for" or a name for the rest.
func heuristicTool(msg string, tools []understand.ToolInfo) map[string]any {
	f := lang.Fold(msg)
	none := map[string]any{"kind": "cannot", "id": "", "pick": "", "values": []any{}, "command": "", "reply": "I cannot do that here."}
	for _, w := range heuristicOwn {
		if strings.Contains(" "+f+" ", " "+w) {
			return map[string]any{"kind": "command", "id": "", "pick": "", "values": []any{}, "command": msg, "reply": ""}
		}
	}
	stem := func(w string) string {
		if r := []rune(w); len(r) > 4 {
			return string(r[:4])
		}
		return w
	}
	said := map[string]bool{}
	for _, w := range strings.Fields(f) {
		if len([]rune(w)) >= 4 {
			said[stem(w)] = true
		}
	}
	best, bestN := -1, 0
	for i, t := range tools {
		n := 0
		seen := map[string]bool{}
		for _, w := range strings.FieldsFunc(lang.Fold(t.Command+" "+t.Name+" "+t.About), func(r rune) bool { return r == ' ' || r == '-' || r == ',' }) {
			if s := stem(w); len([]rune(w)) >= 4 && said[s] && !seen[s] {
				seen[s] = true
				n++
			}
		}
		if n > bestN {
			best, bestN = i, n
		}
	}
	if best < 0 {
		return none
	}
	t := tools[best]
	words := strings.Fields(msg)
	after := func(keys ...string) string {
		for i, w := range words {
			if slices.Contains(keys, strings.ToLower(w)) && i+1 < len(words) {
				return strings.Trim(words[i+1], "?.!,")
			}
		}
		return ""
	}
	var values []any
	for _, in := range t.Inputs {
		v := ""
		switch {
		case in.Type == "number" || in.Type == "integer":
			if m := amountRe.FindStringSubmatch(msg); m != nil {
				n, _ := strconv.ParseFloat(strings.ReplaceAll(m[1], ",", ""), 64)
				switch strings.ToLower(m[2]) {
				case "k":
					n *= 1e3
				case "m":
					n *= 1e6
				}
				v = strconv.FormatFloat(n, 'f', -1, 64)
			}
		case strings.Contains(msg, "@"):
			for _, w := range words {
				if strings.HasPrefix(w, "@") {
					v = strings.Trim(w, "?.!,")
				}
			}
		case capRe.MatchString(msg):
			v = capRe.FindString(msg)
		default:
			v = after("on", "for", "to", "reach")
		}
		if v != "" {
			values = append(values, map[string]string{"field": in.Name, "value": v})
		}
	}
	if values == nil {
		values = []any{}
	}
	return map[string]any{"kind": "tool", "id": t.Command, "pick": "", "values": values, "command": "", "reply": ""}
}

// heuristicToolsIn are the tools a prompt lists, if it lists any.
func heuristicToolsIn(prompt string) ([]understand.ToolInfo, bool) {
	raw, ok := between(prompt, "<tools>\n", "\n</tools>")
	if !ok {
		return nil, false
	}
	var tools []understand.ToolInfo
	if err := json.Unmarshal([]byte(raw), &tools); err != nil {
		return nil, false
	}
	return tools, true
}
