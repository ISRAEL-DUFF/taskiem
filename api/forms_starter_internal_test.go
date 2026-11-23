package api

import "testing"

// A typed reply is taken as a choice only when it could be one: never a
// bare number (a menu's) nor one of Taskiem's own commands.
func TestChoiceWords(t *testing.T) {
	for text, want := range map[string]bool{"Ikeja branch": true, "lekki": true, "2": false, "help": false, "balance": false, "": false, "invoice Ada 5k": false} {
		if got := choiceWords(text); got != want {
			t.Errorf("%q: %v", text, got)
		}
	}
	if choiceKey("Ikeja") == choiceKey("Lekki") || len(choiceKey("Ikeja")) != 12 {
		t.Error("choice keys")
	}
}
