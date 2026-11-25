package wd

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/israel-duff/taskiem/engine/expr"
)

// Drawing a tool's answer from a run's outputs (made tools §7): the same
// for the API, which sends it to the person who called the tool, and for
// wd-test/v1, which checks the answer a test expects before the tool is
// published. A value that cannot be worked out is left out rather than
// failing the answer.

// DrawnAnswer is a tool's answer drawn from its run's outputs.
type DrawnAnswer struct {
	Type    string        `json:"type"`
	Text    string        `json:"text,omitempty"`
	Title   string        `json:"title,omitempty"`
	Ref     string        `json:"ref,omitempty"`
	State   *DrawnState   `json:"state,omitempty"`
	Lines   []DrawnLine   `json:"lines,omitempty"`
	Amount  *int64        `json:"amount_kobo,omitempty"`
	Buttons []DrawnButton `json:"buttons,omitempty"`
	Rows    []DrawnRow    `json:"rows,omitempty"`
	Empty   string        `json:"empty,omitempty"`
}

// DrawnState is a card's or a row's state, in words, with its tone.
type DrawnState struct {
	Text string `json:"text"`
	Tone string `json:"tone,omitempty"`
}

// DrawnLine is one line of a card.
type DrawnLine struct {
	Label string `json:"label"`
	Value string `json:"value"`
}

// DrawnButton is a button on an answer: the tool it opens and the values
// it fills. Token is set by whoever mints it (the API), never here.
type DrawnButton struct {
	Label  string         `json:"label"`
	Tool   string         `json:"tool"`
	Inputs map[string]any `json:"inputs,omitempty"`
	Once   bool           `json:"once,omitempty"`
	// Token presses it (POST /v1/tools/buttons/{token}); a button whose
	// tool is not there is left out of the answer.
	Token string `json:"token,omitempty"`
}

// DrawnRow is one row of a list answer.
type DrawnRow struct {
	Title   string        `json:"title"`
	Sub     string        `json:"sub,omitempty"`
	State   *DrawnState   `json:"state,omitempty"`
	Actions []DrawnButton `json:"actions,omitempty"`
}

var answerEngine = expr.MustNewWithRoots("outputs", "item")

// PublicOutputs are the outputs d marks public, and nothing else: a
// customer's answer is drawn from them alone, so an output written for
// staff never reaches a customer, at any depth, whatever the answer's
// expressions name.
func PublicOutputs(d *Definition, outputs map[string]any) map[string]any {
	out := map[string]any{}
	for _, o := range d.Outputs {
		if !o.Public {
			continue
		}
		if v, ok := outputs[o.Name]; ok {
			out[o.Name] = v
		}
	}
	return out
}

// DrawAnswer draws ans over a run's outputs. words passes each text the
// answer shows (to mask what looks personal); nil leaves them as they are.
func DrawAnswer(ans *Answer, outputs map[string]any, words func(string) string) DrawnAnswer {
	if words == nil {
		words = func(s string) string { return s }
	}
	a := DrawnAnswer{Type: ans.Type}
	act := map[string]any{"outputs": outputs}
	word := func(v string, act map[string]any) string { return words(AnswerWord(v, act)) }
	state := func(st *AnswerState, act map[string]any) *DrawnState {
		if st == nil {
			return nil
		}
		return &DrawnState{Text: word(st.Text, act), Tone: st.Tone}
	}
	buttons := func(bs []Button, act map[string]any) []DrawnButton {
		var out []DrawnButton
		for _, b := range bs {
			in := map[string]any{}
			for k, v := range b.Inputs {
				if got, err := answerEngine.Resolve(v, act, false); err == nil {
					if str, ok := got.(string); ok {
						got = words(str)
					}
					in[k] = got
				}
			}
			out = append(out, DrawnButton{Label: b.Label, Tool: b.Tool, Inputs: in, Once: b.Once})
		}
		return out
	}
	switch ans.Type {
	case "text":
		text := word(ans.Text, act)
		if r := []rune(text); len(r) > AnswerTextMax {
			text = string(r[:AnswerTextMax-1]) + "…"
		}
		a.Text = text
	case "card":
		a.Title, a.Ref, a.State = word(ans.Title, act), word(ans.Ref, act), state(ans.State, act)
		for _, l := range ans.Lines {
			a.Lines = append(a.Lines, DrawnLine{Label: l.Label, Value: word(l.Value, act)})
		}
		if ans.Amount != "" {
			if v, err := answerEngine.Resolve(ans.Amount, act, false); err == nil {
				if k, ok := toKobo(v); ok {
					a.Amount = &k
				}
			}
		}
		a.Buttons = buttons(ans.Buttons, act)
	case "list":
		a.Title, a.Empty = word(ans.Title, act), ans.Empty
		items, _ := outputs[ans.From].([]any)
		a.Rows = []DrawnRow{}
		for _, it := range items {
			ia := map[string]any{"outputs": outputs, "item": it}
			row := DrawnRow{Actions: buttons(ans.Actions, ia)}
			if ans.Item != nil {
				row.Title, row.Sub, row.State = word(ans.Item.Title, ia), word(ans.Item.Sub, ia), state(ans.Item.State, ia)
			}
			a.Rows = append(a.Rows, row)
		}
	}
	return a
}

// AnswerWord works out one of an answer's values as words: text as it
// is, an expression's result written plainly.
func AnswerWord(v string, act map[string]any) string {
	if !strings.HasPrefix(v, "=") {
		return v
	}
	got, err := answerEngine.Eval(v, act)
	if err != nil {
		return ""
	}
	switch x := got.(type) {
	case nil:
		return ""
	case string:
		return x
	case float64:
		if x == float64(int64(x)) {
			return fmt.Sprintf("%d", int64(x))
		}
		return fmt.Sprintf("%g", x)
	case int64:
		return fmt.Sprintf("%d", x)
	case bool:
		if x {
			return "yes"
		}
		return "no"
	}
	b, _ := json.Marshal(got)
	return string(b)
}

func toKobo(v any) (int64, bool) {
	switch x := v.(type) {
	case int64:
		return x, true
	case float64:
		return int64(x), true
	case int:
		return int64(x), true
	}
	return 0, false
}
