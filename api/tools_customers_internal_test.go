package api

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/israel-duff/taskiem/engine/lang"
	"github.com/israel-duff/taskiem/engine/wd"
)

// A customer's answer is drawn from the public outputs alone: an answer
// that names outputs not marked public (one the validator would refuse,
// deployed before, or changed by hand), at any depth, in a card's lines,
// its buttons' values, a list's rows and their buttons, shows none of
// them; the member's answer of the same run shows them.
func TestCustomerAnswerPublicOnly(t *testing.T) {
	d := &wd.Definition{
		Outputs: []wd.Output{
			{Name: "state", Type: "string", Public: true},
			{Name: "rows", Type: "list", Public: true},
			{Name: "staff", Type: "object"},
			{Name: "lines", Type: "list"},
			{Name: "cost", Type: "string"},
		},
	}
	outputs := func() map[string]any {
		return map[string]any{
			"state": "On its way",
			"rows":  []any{map[string]any{"name": "Jollof"}},
			"staff": map[string]any{"note": "SECRET-NOTE", "deep": map[string]any{"pin": "SECRET-PIN"}},
			"lines": []any{map[string]any{"name": "SECRET-ROW"}},
			"cost":  "SECRET-COST",
		}
	}
	card := &wd.Answer{Type: "card", Title: "=outputs.state", Ref: "=outputs.cost",
		Lines:   []wd.AnswerLine{{Label: "Note", Value: "=outputs.staff.note"}, {Label: "Pin", Value: "=outputs.staff.deep.pin"}, {Label: "All", Value: "=outputs"}},
		Buttons: []wd.Button{{Label: "Track", Tool: "track", Inputs: map[string]any{"ref": "=outputs.cost", "note": "=outputs.staff", "state": "=outputs.state"}}}}
	list := &wd.Answer{Type: "list", From: "lines", Title: "=outputs.cost", Item: &wd.AnswerItem{Title: "=item.name", Sub: "=outputs.staff.note"},
		Actions: []wd.Button{{Label: "Open", Tool: "track", Inputs: map[string]any{"ref": "=item.name"}}}}
	publicList := &wd.Answer{Type: "list", From: "rows", Item: &wd.AnswerItem{Title: "=item.name", Sub: "=outputs.cost"},
		Actions: []wd.Button{{Label: "Open", Tool: "track", Inputs: map[string]any{"ref": "=outputs.staff.deep.pin", "name": "=item.name"}}}}

	for _, ans := range []*wd.Answer{card, list, publicList} {
		var a madeAnswer
		drawAnswer(&a, ans, publicOutputs(d, outputs()))
		raw, _ := json.Marshal(a)
		if strings.Contains(string(raw), "SECRET") {
			t.Errorf("%s for a customer: %s", ans.Type, raw)
		}
		if words := customerAnswerWords(lang.EN, a); strings.Contains(words, "SECRET") {
			t.Errorf("%s in words: %s", ans.Type, words)
		}
		var m madeAnswer
		drawAnswer(&m, ans, outputs())
		if raw, _ := json.Marshal(m); ans != publicList && !strings.Contains(string(raw), "SECRET") {
			t.Errorf("%s for a member shows nothing of the rest: %s", ans.Type, raw)
		}
	}
	var a madeAnswer
	drawAnswer(&a, card, publicOutputs(d, outputs()))
	if a.Title != "On its way" || a.Buttons[0].Inputs["state"] != "On its way" {
		t.Errorf("public outputs left out: %+v", a)
	}
	drawAnswer(&a, publicList, publicOutputs(d, outputs()))
	if len(a.Rows) != 1 || a.Rows[0].Title != "Jollof" {
		t.Errorf("public rows: %+v", a.Rows)
	}
}

// A long list for a customer says how many more, without sending them to
// an app.
func TestCustomerAnswerLongList(t *testing.T) {
	a := madeAnswer{Type: "list", Title: "Slots"}
	for range 14 {
		a.Rows = append(a.Rows, answerRow{Title: "9:00"})
	}
	w := customerAnswerWords(lang.EN, a)
	if !strings.HasSuffix(w, "…and 4 more.") || strings.Contains(w, "app") || strings.Count(w, "9:00") != 10 {
		t.Errorf("words: %q", w)
	}
}

// A public menu's label gives a command as migration 00569 does.
func TestMenuCommand(t *testing.T) {
	taken := map[string]bool{"pay-a-supplier": true}
	for label, want := range map[string]string{
		"Pay a supplier":           "pay-a-supplier-2",
		"24/7 help":                "menu-24-7-help",
		"Ọ̀rọ̀":                    "item",
		"Check where my parcel is": "check-where-my-parcel",
		"  Book!  ":                "book",
	} {
		if got := menuCommand(label, func(c string) bool { return taken[c] }); got != want {
			t.Errorf("%q: %q, want %q", label, got, want)
		}
	}
}
