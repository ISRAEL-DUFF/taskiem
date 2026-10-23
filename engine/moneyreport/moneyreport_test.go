package moneyreport

import "testing"

func TestTextCellsAreInert(t *testing.T) {
	for in, want := range map[string]string{"=HYPERLINK(\"x\")": "'=HYPERLINK(\"x\")", "+234": "'+234", "-x": "'-x", "@SUM": "'@SUM", "TRF FROM ADA": "TRF FROM ADA", "": ""} {
		if got := text(in); got != want {
			t.Errorf("%q: %q, want %q", in, got, want)
		}
	}
	if Naira(-150075) != "-1500.75" || Naira(5) != "0.05" {
		t.Error("naira")
	}
}
