package api

import (
	"strings"
	"testing"

	"github.com/israel-duff/taskiem/engine/lang"
)

// A form shows a field before the value its question names is known: in
// every language its label names no placeholder, and a sentence the chat
// alone needs ("send skip") is not its hint.
func TestAskLabelInEveryLanguage(t *testing.T) {
	var add chatAction
	for _, a := range chatActions {
		if a.ID == "riders.add" {
			add = a
		}
	}
	if add.ID == "" {
		t.Fatal("no riders.add action")
	}
	for _, l := range []lang.Tag{lang.EN, "ha", "ig", "pcm", "yo"} {
		for _, f := range add.Fields {
			label, hint := askLabel(l, add, f)
			if label == "" || strings.Contains(label, "{") {
				t.Errorf("%s %s: label %q", l, f.Key, label)
			}
			if skipRe.MatchString(hint) {
				t.Errorf("%s %s: hint keeps the chat's skip: %q", l, f.Key, hint)
			}
		}
	}
}
