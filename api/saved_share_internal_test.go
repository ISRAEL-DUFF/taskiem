package api

import "testing"

// A saved thing is named in a message as a whole word.
func TestNamesSaved(t *testing.T) {
	for _, c := range []struct {
		text, name string
		want       bool
	}{
		{"Pay to @mum", "mum", true},
		{"Pay to @Mum.", "mum", true},
		{"@mum, please", "mum", true},
		{"Pay to @mummy", "mum", false},
		{"Write to x@mum", "mum", false},
		{"Ask @our-accountant", "our-account", false},
		{"@our-account.x", "our-account", false},
		{"Ask @our-accountant or @our-account", "our-account", true},
		{"nothing", "mum", false},
		{"@", "", false},
	} {
		if got := namesSaved(c.text, c.name); got != c.want {
			t.Errorf("namesSaved(%q, %q) = %v", c.text, c.name, got)
		}
	}
}

// The words a template carries: on one line, a saved thing's lines kept
// apart.
func TestTemplateLine(t *testing.T) {
	for in, want := range map[string]string{
		"Still\nthere?": "Still there?",
		"Hi  there":     "Hi there",
		"Hi\n\nSee you": "Hi · See you",
		"Pay here\n\nOur account\nBank: GTBank\nAccount number: 0123456789": "Pay here · Our account · Bank: GTBank · Account number: 0123456789",
		"Note: we open at 9. Come early.\nThanks":                           "Note: we open at 9. Come early. · Thanks",
		"\n\n": "",
	} {
		if got := templateLine(in); got != want {
			t.Errorf("templateLine(%q) = %q, want %q", in, got, want)
		}
	}
}
