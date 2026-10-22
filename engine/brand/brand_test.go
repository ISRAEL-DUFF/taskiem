package brand

import (
	"errors"
	"testing"
)

func TestCheckColor(t *testing.T) {
	for in, want := range map[string]string{"": DefaultColor, "#0B6E4F": "#0b6e4f", "0b6e4f": "#0b6e4f", "#abc": "#aabbcc", "pink": "#be185d", " Blue ": "#1d4ed8"} {
		got, err := CheckColor(in)
		if err != nil || got != want {
			t.Errorf("CheckColor(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, in := range []string{"red;}", "#12345", "rgb(1,2,3)", "url(x)", "#gggggg"} {
		if _, err := CheckColor(in); !errors.Is(err, ErrInvalid) {
			t.Errorf("CheckColor(%q) = %v; want refused", in, err)
		}
	}
}

func TestCheckTheme(t *testing.T) {
	for in, want := range map[string]string{"": "clean", "classic": "clean", "bold": "bold", "Editorial": "editorial"} {
		if got, err := CheckTheme(in); err != nil || got != want {
			t.Errorf("CheckTheme(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	if _, err := CheckTheme("neon"); !errors.Is(err, ErrInvalid) {
		t.Errorf("neon: %v", err)
	}
	if Theme("neon") != "clean" {
		t.Error("an unknown theme should read as the default")
	}
}

// TestPaletteReads: whatever colour is picked, the pairs a theme uses
// read at 4.5:1.
func TestPaletteReads(t *testing.T) {
	for _, c := range []string{"#ffffff", "#000000", "#ffeb3b", "#00e5ff", "#777777", "#0b6e4f", "#ff5722", "#76ff03", "#3f51b5", "#f8bbd0"} {
		p := PaletteOf(c)
		if got := Contrast(p.Brand, p.OnBrand); got < 4.5 {
			t.Errorf("%s: text on the colour is %.2f:1", c, got)
		}
		if got := Contrast(p.Text, "#ffffff"); got < 4.5 {
			t.Errorf("%s: the colour as text on white is %.2f:1 (%s)", c, got, p.Text)
		}
		if p.Brand != c {
			t.Errorf("%s: brand changed to %s", c, p.Brand)
		}
	}
	if p := PaletteOf("#0b6e4f"); p.Text != "#0b6e4f" || p.OnBrand != "#ffffff" {
		t.Errorf("a dark colour should be kept as text: %+v", p)
	}
	if p := PaletteOf("#ffeb3b"); p.OnBrand != "#141414" {
		t.Errorf("yellow takes dark text: %+v", p)
	}
}

func TestInitials(t *testing.T) {
	for in, want := range map[string]string{"Adunni Bakes": "AB", "glow hair studio": "GH", "Ọja": "Ọ", "  ": "", "24/7 Pharmacy": "2P"} {
		if got := Initials(in); got != want {
			t.Errorf("Initials(%q) = %q; want %q", in, got, want)
		}
	}
}
