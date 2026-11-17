package csvsafe_test

import (
	"testing"

	"github.com/israel-duff/taskiem/engine/csvsafe"
)

func TestTextGuardsEveryFormulaStart(t *testing.T) {
	for _, v := range []string{"=1+1", "+234", "-5", "@SUM(A1)", "\t=1+1", "\r=1+1"} {
		got := csvsafe.Text(v)
		if got != "'"+v {
			t.Errorf("%q is written %q", v, got)
		}
		if back := csvsafe.Unguard(got); back != v {
			t.Errorf("%q reads back as %q", v, back)
		}
	}
	for _, v := range []string{"", "Ada", "'quoted", "1=1", "₦5,000"} {
		if got := csvsafe.Text(v); got != v {
			t.Errorf("%q is written %q", v, got)
		}
		if got := csvsafe.Unguard(v); got != v {
			t.Errorf("%q reads back as %q", v, got)
		}
	}
}
