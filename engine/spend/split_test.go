package spend

import (
	"testing"

	"github.com/google/uuid"
)

func TestSplit(t *testing.T) {
	a, b, c := uuid.New(), uuid.New(), uuid.New()
	// The §12 order: ₦40,000 Stock bought, ₦12,000 Packaging, ₦180,000
	// Machines.
	order := []Part{{LabelID: &a, Kobo: 4_000_000}, {LabelID: &b, Kobo: 1_200_000}, {LabelID: &c, Kobo: 18_000_000}}
	sum := func(ps []Part) (n int64) {
		for _, p := range ps {
			n += p.Kobo
		}
		return n
	}
	// Paid in full: each label's own share.
	full := Split(23_200_000, order)
	if full[0].Kobo != 4_000_000 || full[1].Kobo != 1_200_000 || full[2].Kobo != 18_000_000 {
		t.Fatalf("in full: %+v", full)
	}
	// ₦100,000 of it: rounded down, the 2 kobo left over to Machines, the
	// largest share; the weights are not changed.
	part := Split(10_000_000, order)
	if part[0].Kobo != 1_724_137 || part[1].Kobo != 517_241 || part[2].Kobo != 7_758_622 || sum(part) != 10_000_000 {
		t.Fatalf("a part: %+v", part)
	}
	if order[2].Kobo != 18_000_000 {
		t.Fatal("Split changed its input")
	}
	// A tie goes to the first of the largest.
	tie := Split(100, []Part{{Kobo: 1}, {Kobo: 3}, {Kobo: 3}})
	if tie[0].Kobo != 14 || tie[1].Kobo != 44 || tie[2].Kobo != 42 {
		t.Fatalf("tie: %+v", tie)
	}
	// No weight: the first takes it all; a weight below zero is none.
	if z := Split(500, []Part{{Kobo: 0}, {Kobo: -5}}); z[0].Kobo != 500 || z[1].Kobo != 0 {
		t.Fatalf("no weight: %+v", z)
	}
	// Weights whose product with the amount overflows int64.
	big := Split(100_000_000_000, []Part{{Kobo: 5e17}, {Kobo: 5e17}})
	if big[0].Kobo != 50_000_000_000 || big[1].Kobo != 50_000_000_000 {
		t.Fatalf("large: %+v", big)
	}
}

func TestNamesAndSuggestions(t *testing.T) {
	if NameFrom("  diesel  for   the gen ") != "Diesel for the gen" || NameFrom("") != "" || NameFrom("ẹpo") != "Ẹpo" {
		t.Fatal("NameFrom")
	}
	if _, err := CleanName("   "); err == nil {
		t.Fatal("an empty name")
	}
	if _, err := CleanName(string(make([]rune, 61))); err == nil {
		t.Fatal("a long name")
	}
	m, s, f := uuid.New(), uuid.New(), uuid.New()
	list := []Label{{ID: f, Name: "Fuel", Uses: 9}, {ID: s, Name: "Staff welfare", Uses: 4}, {ID: m, Name: "Machines", Uses: 1}}
	// The business's own labels sharing a word come first, then the rest,
	// then a new name from the item's last word.
	// "Machines" is a label already: no new name.
	got := Suggest(list, "Singer sewing Machines", "", 4)
	if len(got) != 3 || got[0].Name != "Machines" || got[1].Name != "Fuel" || got[2].Name != "Staff welfare" || *got[0].LabelID != m {
		t.Fatalf("suggest: %+v", got)
	}
	got = Suggest(list, "Nylon bags", "Packaging", 3)
	if len(got) != 3 || got[0].Name != "Fuel" || got[1].Name != "Staff welfare" || got[2].Name != "Packaging" || got[2].LabelID != nil {
		t.Fatalf("suggest with a category: %+v", got)
	}
	got = Suggest(nil, "heavy duty sewing machine", "", 3)
	if len(got) != 1 || got[0].Name != "Machine" {
		t.Fatalf("suggest for a new business: %+v", got)
	}
}
