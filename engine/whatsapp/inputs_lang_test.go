package whatsapp

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/israel-duff/taskiem/engine/lang"
)

func TestInputPromptsInLanguages(t *testing.T) {
	fs, err := InputFields(json.RawMessage(`{"type":"object","required":["n","ok","kind","note"],"properties":{
		"n":{"type":"integer","title":"Count"},"ok":{"type":"boolean","title":"Paid"},
		"kind":{"type":"string","enum":["a","b"]},"note":{"type":"string","description":"Say why."}}}`), nil)
	if err != nil {
		t.Fatal(err)
	}
	// English is as it was.
	want := []string{"Enter Count (a whole number)", "Enter Paid (yes or no)", "Enter kind (one of: a, b)", "Enter note\nSay why."}
	for i, f := range fs {
		if got := f.Prompt(); got != want[i] {
			t.Errorf("%s: %q, want %q", f.Name, got, want[i])
		}
	}
	if _, err := fs[0].Parse("lots"); err == nil || err.Error() != "that is not a whole number" {
		t.Errorf("english refusal: %v", err)
	}
	for _, info := range lang.All() {
		for _, f := range fs {
			p := f.PromptIn(info.Tag)
			if strings.Contains(p, "wa.field.") || !strings.Contains(p, map[string]string{"n": "Count", "ok": "Paid", "kind": "a, b", "note": "Say why."}[f.Name]) {
				t.Errorf("%s %s: %q", info.Tag, f.Name, p)
			}
		}
	}
	yo := lang.Default().Text(lang.YO, "wa.field.enter_whole", "label", "Count")
	if got := fs[0].PromptIn(lang.YO); got != yo || strings.Contains(got, "Enter") {
		t.Errorf("yoruba: %q", got)
	}
	// Yes and no in the person's language; English always works.
	for text, want := range map[string]bool{"bẹ́ẹ̀ni": true, "beeni": true, "rárá": false, "yes": true, "no": false} {
		if v, err := fs[1].ParseIn(text, lang.YO); err != nil || v != want {
			t.Errorf("%q: %v %v", text, v, err)
		}
	}
	if _, err := fs[1].ParseIn("bẹ́ẹ̀ni", lang.EN); err == nil {
		t.Error("Yoruba yes taken from an English speaker")
	}
	if _, err := fs[1].ParseIn("maybe", lang.YO); err == nil || err.Error() != lang.Default().Text(lang.YO, "wa.field.yes_no") {
		t.Errorf("yoruba refusal: %v", err)
	}
}
