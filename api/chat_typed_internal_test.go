package api

import (
	"testing"

	"github.com/israel-duff/taskiem/engine/lang"
)

// Each action's own command, typed, is that action (chat-menus.md §6.1):
// the surest pattern wins ("order TK-A1B2C3 to Musa" is giving it to a
// rider, not looking it up); words that are no command are none; the
// other languages' commands count by their word lists.
func TestTypedCommandsAreTheirActions(t *testing.T) {
	for _, a := range chatActions {
		if len(typedPatternsOf()[a.ID]) == 0 {
			continue
		}
		// The command as the action makes it from its values.
		cmd := a.Shortcut
		if a.Make != nil {
			v := map[string]string{}
			for _, fl := range a.Fields {
				v[fl.Key] = "thing" + fl.Key
				if len(fl.Choices) > 0 {
					v[fl.Key] = fl.Choices[len(fl.Choices)-1]
				}
			}
			cmd = a.Make(v)
		}
		want := a.ID
		// Something new to count is typed as stock that came in: the
		// command says which, by whether the item is there.
		if a.ID == "stock.new" {
			want = "stock.add"
		}
		if got := typedActionID(cmd, lang.Match{}, false); got != want {
			t.Errorf("%q is %q, not %s", cmd, got, want)
		}
	}
	for text, want := range map[string]string{
		"invoice Ada 25,000 for 5 bags of rice": "money.invoice",
		"Invoice  Bayo 4k for a cake.":          "money.invoice",
		"order TK-ZZ9 delivered":                "orders.move",
		"order TK-ZZ9":                          "orders.one",
		"order from Dangote Depot":              "buying.order",
		"Musa paid 2,000 transfer":              "owe.paid",
		"add rider Kemi 0803 111 2222":          "riders.add",
		"how did we do today?":                  "sales.today",
		"hello there":                           "",
		"/handover":                             "",
		"@Tunde count the float by 7am":         "",
		"invoice":                               "",
	} {
		if got := typedActionID(text, lang.Match{}, false); got != want {
			t.Errorf("%q is %q, want %q", text, got, want)
		}
	}
	if typedPatternsOf()["staff.give"] != nil {
		t.Error("a command of values only has a pattern")
	}
	// Yoruba's invoice words, by the word lists' command.
	m, ok := lang.MatchIntent("ìwé owó Ada 25,000 fún ìrẹsì", []lang.Tag{lang.YO, lang.EN})
	if !ok || m.Intent != lang.IntentInvoice {
		t.Fatalf("yo: %+v %v", m, ok)
	}
	if got := typedActionID("ìwé owó Ada 25,000 fún ìrẹsì", m, ok); got != "money.invoice" {
		t.Errorf("yo invoice: %q", got)
	}
}
