package mailtext

import "testing"

// The cases of inbound email's own tests (engine/inmail) cover the many
// ways mail apps quote; these check the package answers on its own.
func TestLatest(t *testing.T) {
	cases := map[string]struct{ in, want string }{
		"gmail":          {"Yes, Friday.\r\n\r\nOn Mon, 19 Oct 2026 at 10:02, Ada <ada@x.example> wrote:\r\n> Can you deliver?\r\n", "Yes, Friday."},
		"quoted only":    {"Thanks\n> earlier\n", "Thanks"},
		"nothing quoted": {"Just a message.", "Just a message."},
	}
	for name, c := range cases {
		if got := Latest(c.in); got != c.want {
			t.Errorf("%s: Latest = %q, want %q", name, got, c.want)
		}
	}
}

func TestTextFromHTML(t *testing.T) {
	if got := TextFromHTML(`<div>Hello,<br>40 &amp; more</div><script>x()</script><ul><li>One</li></ul>`); got != "Hello,\n40 & more\n- One" {
		t.Errorf("text %q", got)
	}
}
