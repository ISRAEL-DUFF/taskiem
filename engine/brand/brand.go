// Package brand is how a business's public pages look (N13, decision
// 0042): one of three themes, each with its own markup, and the
// business's colour, from which a palette that stays readable is made.
package brand

import (
	"errors"
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
)

// ErrInvalid wraps every refusal of a brand.
var ErrInvalid = errors.New("brand: invalid")

// Themes are the looks a business may pick; the first is the default.
// clean: light cards and a cover photo; bold: the colour fills the
// header, a photo grid; editorial: black and white, serif headings.
var Themes = []string{"clean", "bold", "editorial"}

// DefaultColor is Taskiem's green, for a business that picked none.
const DefaultColor = "#0b6e4f"

// Named are the colours storefronts offered before any colour could be
// picked (decision 0036), kept so a stored name still reads.
var Named = map[string]string{
	"green": "#0b6e4f", "blue": "#1d4ed8", "red": "#b91c1c", "orange": "#c2410c",
	"purple": "#6d28d9", "pink": "#be185d", "teal": "#0f766e", "black": "#111827",
}

// legacyThemes maps the storefront layouts before themes to a theme.
var legacyThemes = map[string]string{"classic": "clean", "bold": "bold"}

var hexColor = regexp.MustCompile(`^#?([0-9a-fA-F]{6}|[0-9a-fA-F]{3})$`)

// Theme is a theme by name: an old layout name reads as its theme, and
// anything else as the default.
func Theme(name string) string {
	t, err := CheckTheme(name)
	if err != nil {
		return Themes[0]
	}
	return t
}

// CheckTheme normalises a theme name and refuses one not offered; empty
// is the default, and an old layout name is its theme.
func CheckTheme(name string) (string, error) {
	n := strings.ToLower(strings.TrimSpace(name))
	if n == "" {
		return Themes[0], nil
	}
	if t, ok := legacyThemes[n]; ok {
		return t, nil
	}
	for _, t := range Themes {
		if t == n {
			return t, nil
		}
	}
	return "", fmt.Errorf("%w: theme must be one of %s", ErrInvalid, strings.Join(Themes, ", "))
}

// CheckColor normalises a colour to lower-case #rrggbb: a hex colour
// (#abc, #aabbcc, with or without #) or an old colour name. Empty is the
// default.
func CheckColor(c string) (string, error) {
	c = strings.ToLower(strings.TrimSpace(c))
	if c == "" {
		return DefaultColor, nil
	}
	if hex, ok := Named[c]; ok {
		return hex, nil
	}
	m := hexColor.FindStringSubmatch(c)
	if m == nil {
		return "", fmt.Errorf("%w: colour must be like #0b6e4f", ErrInvalid)
	}
	h := m[1]
	if len(h) == 3 {
		h = string([]byte{h[0], h[0], h[1], h[1], h[2], h[2]})
	}
	return "#" + h, nil
}

// Color is a colour as CheckColor reads it, or the default when it does
// not read.
func Color(c string) string {
	n, err := CheckColor(c)
	if err != nil {
		return DefaultColor
	}
	return n
}

// Brand is a business's look as stored (tenants.brand): Logo and Cover
// are storefront_media ids, with their URLs by width copied in when set.
type Brand struct {
	Theme     string            `json:"theme,omitempty"`
	Color     string            `json:"color,omitempty"`
	Logo      string            `json:"logo,omitempty"`
	LogoURLs  map[string]string `json:"logo_urls,omitempty"`
	Cover     string            `json:"cover,omitempty"`
	CoverURLs map[string]string `json:"cover_urls,omitempty"`
}

// Check normalises a brand's theme and colour in place.
func (b *Brand) Check() error {
	t, err := CheckTheme(b.Theme)
	if err != nil {
		return err
	}
	c, err := CheckColor(b.Color)
	if err != nil {
		return err
	}
	b.Theme, b.Color = t, c
	if b.Logo == "" {
		b.LogoURLs = nil
	}
	if b.Cover == "" {
		b.CoverURLs = nil
	}
	return nil
}

// Palette is what a page's colours are made from one colour: every pair a
// theme puts together is readable (WCAG AA, 4.5:1).
type Palette struct {
	// Brand is the colour as picked: buttons and colour blocks.
	Brand string `json:"brand"`
	// OnBrand is text on Brand: white, or near-black on a light colour.
	OnBrand string `json:"on_brand"`
	// Text is the colour as text or a link on white, darkened as far as
	// it must be.
	Text string `json:"text"`
	// Soft and Tint are the colour mixed into white (14% and 7%): chips,
	// placeholders, a highlighted row.
	Soft string `json:"soft"`
	Tint string `json:"tint"`
}

// PaletteOf makes the palette for a colour (normalised with Color).
func PaletteOf(c string) Palette {
	c = Color(c)
	r, g, b := rgb(c)
	p := Palette{Brand: c, OnBrand: "#ffffff"}
	// Every colour reads at 4.5:1 with white or with black; near-black is
	// kinder where it is enough.
	if l := lum(r, g, b); contrast(l, 1) < 4.5 {
		p.OnBrand = "#141414"
		if contrast(l, lum(0x14, 0x14, 0x14)) < 4.5 {
			p.OnBrand = "#000000"
		}
	}
	// Darken towards black until it reads on white.
	tr, tg, tb := r, g, b
	for i := 0; i < 40 && contrast(lum(tr, tg, tb), 1) < 4.5; i++ {
		tr, tg, tb = tr*0.92, tg*0.92, tb*0.92
	}
	p.Text = hex(tr, tg, tb)
	p.Soft = hex(mix(r, 0.14), mix(g, 0.14), mix(b, 0.14))
	p.Tint = hex(mix(r, 0.07), mix(g, 0.07), mix(b, 0.07))
	return p
}

// CSS is the stylesheet that gives a page its colours: custom properties
// every theme's stylesheet reads. It is served as a file, since the
// public pages allow no inline style.
func (p Palette) CSS() string {
	return ":root{--brand:" + p.Brand + ";--on-brand:" + p.OnBrand + ";--brand-text:" + p.Text +
		";--brand-soft:" + p.Soft + ";--brand-tint:" + p.Tint + "}\n"
}

// Initials are up to two letters from a name, for a page with no logo.
func Initials(name string) string {
	var out []rune
	for _, w := range strings.Fields(name) {
		for _, r := range w {
			if isLetterOrDigit(r) {
				out = append(out, []rune(strings.ToUpper(string(r)))...)
				break
			}
		}
		if len(out) == 2 {
			break
		}
	}
	return string(out)
}

func isLetterOrDigit(r rune) bool {
	return r >= '0' && r <= '9' || strings.ToUpper(string(r)) != strings.ToLower(string(r))
}

func rgb(c string) (r, g, b float64) {
	v, _ := strconv.ParseUint(c[1:], 16, 32)
	return float64(v >> 16 & 0xff), float64(v >> 8 & 0xff), float64(v & 0xff)
}

func hex(r, g, b float64) string {
	cl := func(x float64) int { return int(math.Max(0, math.Min(255, math.Round(x)))) }
	return fmt.Sprintf("#%02x%02x%02x", cl(r), cl(g), cl(b))
}

// mix is a channel mixed with white, share parts of it to 1-share white.
func mix(ch, share float64) float64 { return ch*share + 255*(1-share) }

func lum(r, g, b float64) float64 {
	f := func(c float64) float64 {
		c /= 255
		if c <= 0.03928 {
			return c / 12.92
		}
		return math.Pow((c+0.055)/1.055, 2.4)
	}
	return 0.2126*f(r) + 0.7152*f(g) + 0.0722*f(b)
}

func contrast(a, b float64) float64 {
	if a < b {
		a, b = b, a
	}
	return (a + 0.05) / (b + 0.05)
}

// Contrast is the WCAG contrast ratio of two colours.
func Contrast(a, b string) float64 {
	ar, ag, ab := rgb(Color(a))
	br, bg, bb := rgb(Color(b))
	return contrast(lum(ar, ag, ab), lum(br, bg, bb))
}
