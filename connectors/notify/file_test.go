package notify

import (
	"errors"
	"strings"
	"testing"

	"github.com/israel-duff/taskiem/engine/effects"
	"github.com/israel-duff/taskiem/engine/sender"
)

// A message's saved file (build plan H6, and its review): only the saved
// root names one at publish; at run time only a saved file's reference
// is sent, never a link from anywhere; the line it adds is kept whole
// wherever the message is cut, and refused when it alone cannot fit.
func TestMessageFile(t *testing.T) {
	for _, ok := range []string{"=saved.price_list", `=saved["price-list"]`, "=saved.price_list.file", "= saved.x2 "} {
		if got := checkInput("message", map[string]any{"file": ok}); len(got) != 0 {
			t.Errorf("%s: %v", ok, got)
		}
	}
	for _, bad := range []any{"=trigger.body.url", "=steps.a.output.link", "https://x.test/a.pdf", map[string]any{"file": "https://x"}, "=saved.price_list + trigger.body.x"} {
		if got := checkInput("tell", map[string]any{"message": "x", "file": bad}); len(got) != 1 {
			t.Errorf("%v: %v", bad, got)
		}
	}
	if got := checkInput("text", map[string]any{"file": "https://x"}); len(got) != 0 {
		t.Errorf("text takes no file: %v", got)
	}

	ref := FileRefPrefix + "0193aaaa-0000-7000-8000-000000000001"
	for in, want := range map[string]any{"none": nil, "ref": ref, "thing": map[string]any{"file": ref, "file_name": "p.pdf", "kind": "file"}} {
		input := map[string]any{}
		if want != nil {
			input["file"] = want
		}
		got, err := FileRef(input)
		if err != nil || (want == nil) != (got == "") {
			t.Errorf("%s: %q %v", in, got, err)
		}
	}
	for _, bad := range []any{"https://x.test/a.pdf", map[string]any{"file": "https://x"}, FileRefPrefix + "nope", 7} {
		if _, err := FileRef(map[string]any{"file": bad}); !errors.Is(err, effects.ErrFatal) {
			t.Errorf("%v: %v", bad, err)
		}
	}

	line := FileLine("prices.pdf", "https://taskiem.test/v1/saved-files/"+strings.Repeat("a", 43))
	if got := WithFileLine("This week.\n", line); got != "This week.\n\n"+line {
		t.Errorf("with a line: %q", got)
	}
	// The template's one line: cut, the file's line whole.
	got, err := CutFor(strings.Repeat("word ", 200), " ", line, 500)
	if err != nil || len([]rune(got)) > 501 || !strings.HasSuffix(got, "… "+line) {
		t.Errorf("cut: %d %q %v", len([]rune(got)), got, err)
	}
	if _, err := CutFor("x", " ", strings.Repeat("y", 600), 500); err == nil {
		t.Error("a line that cannot fit was let through")
	}
	// The SMS: within its parts, the line whole after the message cut.
	sms, err := SMSTextWithFile("Ada Foods", "Prices", strings.Repeat("Rice is up. ", 80), line)
	if err != nil || !strings.HasSuffix(sms, "…\n"+line) || sender.Segments("Ada Foods: "+sms) > sender.MaxSegments {
		t.Errorf("sms: %q %v", sms, err)
	}
	if _, err := SMSTextWithFile("Ada Foods", "", "x", strings.Repeat("y", 600)); !errors.Is(err, effects.ErrFatal) {
		t.Errorf("too long for an SMS: %v", err)
	}
}
