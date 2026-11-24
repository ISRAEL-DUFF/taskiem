package api

import (
	"strconv"
	"strings"

	"github.com/israel-duff/taskiem/engine/wd"
	"github.com/israel-duff/taskiem/engine/wdcheck"
)

// codeWords reports whether the message asked for as a form step's code
// (g-scan-wa) can be it: words typed, not a button, not one of Taskiem's
// own commands (nor a /command), not a bare number while a menu waits for
// one, and nothing else in the chat waiting on the message; a code's text
// as the server takes one (wd.ScanTextProblem). A barcode is often only
// digits, so a bare number is a code when no menu waits.
func codeWords(c *chat) bool {
	t := strings.TrimSpace(c.in.Text)
	if t == "" || c.in.Reply != "" || c.in.Location != "" || c.in.Image != nil || c.in.Document != nil || wd.ScanTextProblem(t) != "" {
		return false
	}
	if strings.HasPrefix(t, "/") {
		return false
	}
	first, _, _ := strings.Cut(strings.ToLower(t), " ")
	if wdcheck.ReservedCommand(first) {
		return false
	}
	_, numErr := strconv.ParseUint(strings.ReplaceAll(t, " ", ""), 10, 64)
	bare := numErr == nil
	switch {
	case c.state == stateIdle:
		return true
	case c.data.Menu != nil:
		// A menu waits for a number: a number is its, words may be a code.
		return !bare
	}
	return false // the chat is in the middle of something else
}
