package notify

import (
	"errors"
	"testing"

	"github.com/israel-duff/taskiem/engine/effects"
)

// A message's saved file (build plan H6): its name and link at the end;
// none without one; a file the run could not read, or one that is no
// link, refused for good; a link written out refused at publish.
func TestMessageFile(t *testing.T) {
	in := map[string]any{"file": map[string]any{"file": "https://files.example/prices.pdf", "file_name": "prices.pdf", "kind": "file", "title": "Price list"}}
	if got := WithFile("This week's prices.\n", in); got != "This week's prices.\n\nprices.pdf: https://files.example/prices.pdf" {
		t.Errorf("with a file: %q", got)
	}
	if got := WithFile("Hello", map[string]any{}); got != "Hello" {
		t.Errorf("without: %q", got)
	}
	for _, bad := range []any{map[string]any{"file": ""}, map[string]any{"file": "ftp://x"}, "ftp://x", 7} {
		if _, err := FileLine(map[string]any{"file": bad}); !errors.Is(err, effects.ErrFatal) {
			t.Errorf("%v: %v", bad, err)
		}
	}
	if got := WithFile("Hi", map[string]any{"file": "https://x"}); got != "Hi\n\nThe file: https://x" {
		t.Errorf("a link alone: %q", got)
	}
	if got := checkInput("message", map[string]any{"file": "=saved.price_list"}); len(got) != 0 {
		t.Errorf("an expression: %v", got)
	}
	if got := checkInput("tell", map[string]any{"message": "x", "file": map[string]any{"file": "https://x"}}); len(got) != 1 {
		t.Errorf("written out: %v", got)
	}
	if got := checkInput("text", map[string]any{"file": "https://x"}); len(got) != 0 {
		t.Errorf("text takes no file: %v", got)
	}
}
