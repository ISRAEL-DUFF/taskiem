package api

import "testing"

// "@Taskiem" (chat_tools.go): a word in any case, at the start or after a
// space or a mark, taken off with its comma or colon; the lines are kept.
func TestCutTaskiem(t *testing.T) {
	for _, c := range []struct {
		in, out string
		ok      bool
	}{
		{"@Taskiem status", "status", true},
		{"@taskiem, what do I owe @Dangote?", "what do I owe @Dangote?", true},
		{"what does @Dangote charge, @TASKIEM?", "what does @Dangote charge, ?", true},
		{"hi\n@Taskiem: summary\nof today", "hi\nsummary\nof today", true},
		{"(@Taskiem) hi", "() hi", true},
		{"@Taskiem", "hi", true},
		{"  @Taskiem  ", "hi", true},
		{"@TaskiemBot hi", "@TaskiemBot hi", false},
		{"mail me@taskiem.com", "mail me@taskiem.com", false},
		{"@Dangote hi", "@Dangote hi", false},
	} {
		out, ok := cutTaskiem(c.in)
		if ok != c.ok || out != c.out {
			t.Errorf("%q: %q %v, want %q %v", c.in, out, ok, c.out, c.ok)
		}
	}
}
