package pii_test

import (
	"testing"

	"github.com/israel-duff/taskiem/engine/pii"
)

// Personal values a run holds are masked where a provider's error text
// repeats them exactly; nothing else in the text changes (S22).
func TestTaintRedactText(t *testing.T) {
	taint := pii.Taint{}
	taint.Add("Adaeze Okonkwo", "name")
	taint.Add("Adaeze", "name") // a shorter value inside a longer one
	taint.Add("12 Marina Road, Lagos", "address")
	taint.Add(int64(22212345678), "bvn")
	taint.Add("Ada", "name") // too short to look for
	taint.Add("", "name")    // ignored

	cases := []struct{ in, want string }{
		{"customer Adaeze Okonkwo not found", "customer [name] not found"},
		{"Adaeze, your BVN 22212345678 failed", "[name], your BVN [bvn] failed"},
		{"address 12 Marina Road, Lagos is not served", "address [address] is not served"},
		{"Ada is not a known customer", "Ada is not a known customer"},
		{"rate limited: try again later", "rate limited: try again later"},
		{"", ""},
	}
	for _, c := range cases {
		if got := taint.RedactText(c.in); got != c.want {
			t.Errorf("RedactText(%q) = %q, want %q", c.in, got, c.want)
		}
	}
	if got := (pii.Taint{}).RedactText("customer Adaeze Okonkwo"); got != "customer Adaeze Okonkwo" {
		t.Errorf("an empty taint changed the text: %q", got)
	}
	var none pii.Taint
	if got := none.RedactText("anything"); got != "anything" {
		t.Errorf("a nil taint changed the text: %q", got)
	}
}
