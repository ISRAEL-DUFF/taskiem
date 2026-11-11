package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The app's theme is the web's: regenerated from the CSS it must be the
// file in the repository, or the web changed and the app was not told.
func TestThemeIsCurrent(t *testing.T) {
	root := filepath.Join("..", "..")
	want, err := Generate(root)
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(root, Out))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != want {
		t.Fatalf("%s is stale: run `go run ./tools/mobilegen` from the repository root", Out)
	}
}

// The app's API client is the API description's.
func TestAPIClientIsCurrent(t *testing.T) {
	root := filepath.Join("..", "..")
	want, err := GenerateAPI(root)
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(root, APIOut))
	if err != nil || string(got) != want {
		t.Fatalf("%s is stale: run `go run ./tools/mobilegen` from the repository root", APIOut)
	}
}

// The brand's files in the app are the web's, byte for byte.
func TestBrandIsCurrent(t *testing.T) {
	root := filepath.Join("..", "..")
	for _, f := range Brand {
		want, err := os.ReadFile(filepath.Join(root, BrandFrom, f))
		if err != nil {
			t.Fatal(err)
		}
		got, err := os.ReadFile(filepath.Join(root, BrandTo, f))
		if err != nil || string(got) != string(want) {
			t.Fatalf("%s/%s is not the web's: run `go run ./tools/mobilegen`", BrandTo, f)
		}
	}
}

func TestColours(t *testing.T) {
	for _, c := range []struct{ css, want string }{
		{"#0d3b2e", "FF0D3B2E"},
		{"#fff", "FFFFFFFF"},
		{"rgba(14, 26, 20, 0.38)", "610E1A14"},
		{"transparent", "00000000"},
		// The browser's color-mix: pine 9% into white, and into nothing.
		{"color-mix(in srgb, #0d3b2e 9%, #ffffff)", "FFE9EDEC"},
		{"color-mix(in srgb, #0d3b2e 9%, transparent)", "170D3B2E"},
	} {
		v, err := color(c.css)
		if err != nil {
			t.Fatalf("%s: %v", c.css, err)
		}
		if got := strings.ToUpper(strings.TrimPrefix(dartColor(v), "Color(0x")); got != c.want+")" {
			t.Errorf("%s: got %s, want %s", c.css, got, c.want)
		}
	}
}

func TestShadowsAndTheirBlur(t *testing.T) {
	s, err := shadows("0 24px 48px -16px rgba(14, 15, 12, 0.22), 0 0 0 1px #262724")
	if err != nil {
		t.Fatal(err)
	}
	if len(s) != 2 || s[0].y != 24 || s[0].blur != 48 || s[0].spread != -16 || s[1].spread != 1 {
		t.Fatalf("read as %+v", s)
	}
	// CSS blurs with deviation r/2, Flutter with 0.57735r + 0.5: the same
	// shadow needs a smaller radius in Flutter.
	if got := flutterBlur(48); got != 40.7 {
		t.Errorf("flutterBlur(48) = %v", got)
	}
	if flutterBlur(0) != 0 {
		t.Error("no blur stays none")
	}
}

func TestDarkTakesOverLightAndReadsVars(t *testing.T) {
	light, dark := rootBlocks(`
:root { --brand: #0d3b2e; --brand-dark: #c8f169; --accent: var(--brand); --t-quick: 150ms; }
:root[data-theme="dark"] { --accent: var(--brand-dark); }
:root[data-brand="seal"] { --brand: #1f2a6b; }
@media (prefers-color-scheme: dark) { :root[data-theme="system"] { --accent: #000; } }
`)
	tok, err := resolve(light, dark)
	if err != nil {
		t.Fatal(err)
	}
	if tok.light["--accent"].color.argb() != 0xFF0D3B2E || tok.dark["--accent"].color.argb() != 0xFFC8F169 {
		t.Fatalf("accent: light %08X dark %08X", tok.light["--accent"].color.argb(), tok.dark["--accent"].color.argb())
	}
	if tok.light["--t-quick"].ms != 150 {
		t.Fatal("durations are read")
	}
}

func TestMotionMustNotDifferInDark(t *testing.T) {
	light, dark := rootBlocks(`:root { --t-quick: 150ms; } :root[data-theme="dark"] { --t-quick: 200ms; }`)
	if _, err := resolve(light, dark); err == nil {
		t.Fatal("a duration that differs in dark is refused")
	}
}
