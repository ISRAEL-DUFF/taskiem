package api

import (
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/israel-duff/taskiem/engine/wdtext"
)

// A long draft read back in the chat keeps its tool's lines (who may use
// it) whatever is cut, and never cuts inside a character (review M6, 6).
func TestPlainStepsKeepTheTool(t *testing.T) {
	var lines []wdtext.Line
	for i := 1; i <= 40; i++ {
		lines = append(lines, wdtext.Line{Number: strconv.Itoa(i), Text: strings.Repeat("Ọ̀ka ", 30) + "step " + strconv.Itoa(i)})
	}
	lines = append(lines,
		wdtext.Line{Number: "•", Text: "Use it from the chat with /our-prices: Our prices"},
		wdtext.Line{Number: "•", Text: "Your customers can use it, and see only what it marks for them"})
	more := func(n int) string { return strconv.Itoa(n) + " more steps" }
	out := plainStepsText("Our prices", lines, more)
	if !strings.HasSuffix(out, "Your customers can use it, and see only what it marks for them") || !strings.Contains(out, "/our-prices") {
		t.Errorf("the tool's lines went:\n%s", out)
	}
	if len(out) > plainMaxBytes || !utf8.ValidString(out) {
		t.Errorf("%d bytes, valid %v", len(out), utf8.ValidString(out))
	}
	if !strings.Contains(out, " more steps") {
		t.Errorf("the steps left out are not counted:\n%s", out)
	}
	// One step longer than a message: its start, whole characters.
	long := []wdtext.Line{{Number: "1", Text: strings.Repeat("ẹ", 4000)}, {Number: "•", Text: "Your own people can use it"}}
	if out := plainStepsText("Long", long, more); !utf8.ValidString(out) || len(out) > plainMaxBytes || !strings.HasSuffix(out, "Your own people can use it") {
		t.Errorf("long step: %d bytes, valid %v", len(out), utf8.ValidString(out))
	}
	// A short one is read whole, steps then the tool.
	short := []wdtext.Line{{Number: "1", Text: "When you start it"}, {Number: "2", Text: "Read the price list"}, {Number: "•", Text: "Your own people can use it"}}
	if out := plainStepsText("Prices", short, more); out != "*Prices*\n1. When you start it\n2. Read the price list\n• Your own people can use it" {
		t.Errorf("short:\n%q", out)
	}
}
