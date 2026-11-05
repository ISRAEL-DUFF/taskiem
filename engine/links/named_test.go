package links

import (
	"slices"
	"testing"
)

func namesOf(ls []Link) []string {
	out := make([]string, len(ls))
	for i, l := range ls {
		out[i] = l.Name
	}
	return out
}

// A business is found by the start of its name (docs/spec/direct-messages.md §3).
func TestMentioned(t *testing.T) {
	act := func(names ...string) []Link {
		out := make([]Link, len(names))
		for i, n := range names {
			out[i] = Link{Name: n, State: Active}
		}
		return out
	}
	one := act("Dangote Nigeria Ltd", "BUA Foods Plc", "A&B Stores")
	for _, c := range []struct {
		text, want string
		n          int
	}{
		{"Dangote how much is cement?", "Dangote Nigeria Ltd", len("Dangote")},
		{"dangote nigeria is late", "Dangote Nigeria Ltd", len("dangote nigeria")},
		{"Dangote Nigeria Ltd. hi", "Dangote Nigeria Ltd", len("Dangote Nigeria Ltd.")},
		{"Dangote Ltd please", "Dangote Nigeria Ltd", len("Dangote Ltd")},
		{"DangoteNigeria hi", "Dangote Nigeria Ltd", len("DangoteNigeria")},
		{"Dangote, is the depot open", "Dangote Nigeria Ltd", len("Dangote")},
		{"BUA\nprice?", "BUA Foods Plc", len("BUA")},
		{"bua foods plc", "BUA Foods Plc", len("bua foods plc")},
		{"A&B Stores hi", "A&B Stores", len("A&B Stores")},
	} {
		got, n := Mentioned(one, c.text)
		if len(got) != 1 || got[0].Name != c.want || n != c.n {
			t.Errorf("%q: %v %d, want %s %d", c.text, namesOf(got), n, c.want, c.n)
		}
	}
	for _, text := range []string{"Nigeria hi", "Dang hi", "Ltd", "", " Dangote"} {
		if got, n := Mentioned(one, text); len(got) != 0 || n != 0 {
			t.Errorf("%q names nothing: %v %d", text, namesOf(got), n)
		}
	}
	// Two businesses that fit are both returned; more words decide.
	two := act("Dangote Cement", "Dangote Sugar")
	if got, _ := Mentioned(two, "Dangote price?"); !slices.Equal(namesOf(got), []string{"Dangote Cement", "Dangote Sugar"}) {
		t.Errorf("ambiguous: %v", namesOf(got))
	}
	if got, _ := Mentioned(two, "Dangote sugar is late"); !slices.Equal(namesOf(got), []string{"Dangote Sugar"}) {
		t.Errorf("decided by a word: %v", namesOf(got))
	}
	// A whole name beats one it only begins.
	both := act("Dangote", "Dangote Stores")
	if got, _ := Mentioned(both, "Dangote hi"); !slices.Equal(namesOf(got), []string{"Dangote"}) {
		t.Errorf("whole name: %v", namesOf(got))
	}
	if got, _ := Mentioned(both, "Dangote Stores hi"); !slices.Equal(namesOf(got), []string{"Dangote Stores"}) {
		t.Errorf("longer name: %v", namesOf(got))
	}
	// Ended links are not named; İ is not i.
	if got, _ := Mentioned([]Link{{Name: "Dangote", State: "ended"}}, "Dangote"); len(got) != 0 {
		t.Errorf("ended: %v", namesOf(got))
	}
	if got, _ := Mentioned(act("Kelvin"), "KELVİN"); len(got) != 0 {
		t.Errorf("İ: %v", namesOf(got))
	}
	if got := Named(one, "dangote"); len(got) != 1 {
		t.Errorf("Named: %v", namesOf(got))
	}
}
