package variants

import (
	"errors"
	"strings"
	"testing"
)

func TestCheck(t *testing.T) {
	cs, err := Check([]Choice{{Name: " Size ", Values: []string{"40", " 41 ", "", "42"}}, {Name: "Colour", Values: []string{"Black", "White"}}})
	if err != nil || cs[0].Name != "Size" || strings.Join(cs[0].Values, ",") != "40,41,42" {
		t.Fatalf("tidied: %v %v", cs, err)
	}
	for _, bad := range [][]Choice{
		{{Name: "A", Values: []string{"1"}}, {Name: "B", Values: []string{"1"}}, {Name: "C", Values: []string{"1"}}, {Name: "D", Values: []string{"1"}}},
		{{Name: "", Values: []string{"1"}}},
		{{Name: "Size", Values: []string{"40"}}, {Name: "size", Values: []string{"41"}}},
		{{Name: "Size", Values: []string{"40", "40"}}},
		{{Name: "Size", Values: []string{" "}}},
		{{Name: "Size", Values: make([]string, 21)}},
	} {
		if _, err := Check(bad); !errors.Is(err, ErrInvalid) {
			t.Errorf("%v: %v", bad, err)
		}
	}
	many := make([]string, 21)
	for i := range many {
		many[i] = strings.Repeat("x", i+1)
	}
	if _, err := Check([]Choice{{Name: "Size", Values: many}}); !errors.Is(err, ErrInvalid) {
		t.Errorf("21 values: %v", err)
	}
}

func TestNamesAndKeys(t *testing.T) {
	cs := []Choice{{Name: "Size", Values: []string{"42"}}, {Name: "Colour", Values: []string{"Black"}}}
	v := map[string]string{"Colour": "Black", "Size": "42"}
	if n := Name("Air Max 90", v, cs); n != "Air Max 90 – 42, Black" {
		t.Errorf("name: %q", n)
	}
	if Key(v) != Key(map[string]string{"size": "42", "colour": "black"}) {
		t.Error("keys of one variant differ by case")
	}
	if got, ok := Fit(map[string]string{"size": "42", "COLOUR": "black"}, cs); !ok || got["Colour"] != "Black" || got["Size"] != "42" {
		t.Errorf("fit: %v %v", got, ok)
	}
	for _, v := range []map[string]string{{"Size": "42"}, {"Size": "43", "Colour": "Black"}, {"Size": "42", "Fit": "Slim"}} {
		if _, ok := Fit(v, cs); ok {
			t.Errorf("fits: %v", v)
		}
	}
	if ParseVariant(JSON(v))["Size"] != "42" || len(ParseChoices(JSON(cs))) != 2 || ParseChoices("") != nil || ParseVariant("{}") != nil {
		t.Error("round trip")
	}
}

func TestMakePlan(t *testing.T) {
	cs := []Choice{{Name: "Size", Values: []string{"41", "42", "43"}}, {Name: "Colour", Values: []string{"Black"}}}
	v := func(size string) map[string]string { return map[string]string{"Size": size, "Colour": "Black"} }
	have := []Existing{
		{ID: "7", Variant: v("41"), Available: true},
		{ID: "8", Variant: v("42"), Available: false}, // retired before
		{ID: "9", Variant: map[string]string{"Size": "40", "Colour": "Black"}, Available: true},
	}
	p, err := MakePlan("Air Max 90", cs, []Cell{
		{ID: "7", Variant: v("41")},
		{Variant: v("42")},          // matches the retired 8: brought back
		{ID: "9", Variant: v("43")}, // 40 corrected to 43: renamed
		{Variant: map[string]string{"size": "41", "colour": "black"}}, // 41 again
	}, have)
	if err == nil || !strings.Contains(err.Error(), "twice") {
		t.Fatalf("41 twice: %v %v", p, err)
	}
	p, err = MakePlan("Air Max 90", cs, []Cell{{ID: "7", Variant: v("41")}, {Variant: v("42")}, {ID: "9", Variant: v("43")}}, have)
	if err != nil || len(p.Create) != 0 || len(p.Retire) != 0 || len(p.Keep) != 3 {
		t.Fatalf("plan: %+v %v", p, err)
	}
	if !p.Keep[1].Revive || p.Keep[1].ID != "8" || p.Keep[2].Name != "Air Max 90 – 43, Black" {
		t.Errorf("kept: %+v", p.Keep)
	}
	// A grid without 41 retires it; a new size is made.
	p, _ = MakePlan("Air Max 90", cs, []Cell{{Variant: v("43")}}, have[:1])
	if len(p.Create) != 1 || len(p.Retire) != 1 || p.Retire[0] != "7" {
		t.Errorf("retire and make: %+v", p)
	}
	if _, err := MakePlan("X", cs, []Cell{{ID: "99", Variant: v("41")}}, have); !errors.Is(err, ErrInvalid) {
		t.Errorf("another product's row: %v", err)
	}
	if _, err := MakePlan("X", cs, []Cell{{Variant: map[string]string{"Size": "41"}}}, nil); !errors.Is(err, ErrInvalid) {
		t.Errorf("a value missing: %v", err)
	}
}
