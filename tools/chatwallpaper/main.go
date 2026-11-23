// Command chatwallpaper draws the chat's wallpaper (docs/spec/mobile-app-look.md,
// "The chat"): small doodles of where business meets chat, packed close as
// WhatsApp's, on a square tile that repeats without a seam. One drawing for
// the web and the app:
//
//   - web/public/chat/wallpaper-light.svg and wallpaper-dark.svg, their
//     lines in the web's --chat-doodle colour of each theme (styles.css);
//   - mobile/assets/chat/wallpaper.svg, its lines black, tinted by the app
//     with the same token (chatDoodle, generated from styles.css).
//
// Run it from the repository root with `go run ./tools/chatwallpaper` after
// changing the doodles, the packing or --chat-doodle; a test fails when the
// files are stale.
package main

import (
	"fmt"
	"math"
	"math/rand/v2"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Size is the tile's side in CSS pixels (logical pixels in the app).
const Size = 400.0

// Seed fixes the scatter, so the tile is the same on every run.
const Seed = 7

// Out names each file written, from the repository root, and the colour of
// its lines ("" for black, tinted where it is shown).
var Out = []struct{ Path, Theme string }{
	{"web/public/chat/wallpaper-light.svg", "light"},
	{"web/public/chat/wallpaper-dark.svg", "dark"},
	{"mobile/assets/chat/wallpaper.svg", ""},
}

func main() {
	root := "."
	if len(os.Args) > 1 {
		root = os.Args[1]
	}
	files, err := Generate(root)
	if err != nil {
		fmt.Fprintln(os.Stderr, "chatwallpaper:", err)
		os.Exit(1)
	}
	for path, svg := range files {
		full := filepath.Join(root, path)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil { //nolint:gosec // a source directory
			fmt.Fprintln(os.Stderr, "chatwallpaper:", err)
			os.Exit(1)
		}
		if err := os.WriteFile(full, []byte(svg), 0o644); err != nil { //nolint:gosec // a source file
			fmt.Fprintln(os.Stderr, "chatwallpaper:", err)
			os.Exit(1)
		}
	}
}

// Generate returns each file's contents by its path.
func Generate(root string) (map[string]string, error) {
	css, err := os.ReadFile(filepath.Join(root, "web/src/styles.css")) //nolint:gosec // a source file
	if err != nil {
		return nil, err
	}
	// The light theme's --chat-doodle first, then the dark's.
	m := regexp.MustCompile(`--chat-doodle:\s*([^;]+);`).FindAllStringSubmatch(string(css), -1)
	if len(m) < 2 {
		return nil, fmt.Errorf("styles.css: --chat-doodle for light and dark not found")
	}
	colour := map[string]string{"light": strings.TrimSpace(m[0][1]), "dark": strings.TrimSpace(m[1][1]), "": "#000"}
	tile := Tile()
	files := map[string]string{}
	for _, o := range Out {
		files[o.Path] = strings.Replace(tile, `stroke="#000"`, fmt.Sprintf(`stroke="%s"`, colour[o.Theme]), 1)
	}
	return files, nil
}

type placed struct {
	x, y, r, scale, rot float64
	path                string
}

// Tile packs the doodles: each thrown at random and kept where it touches
// none placed before (measured round the tile's edges, so it repeats
// without a seam), every kind used before any repeats; until 20,000 throws
// in a row find no room.
func Tile() string {
	const (
		lo, hi = 0.42, 0.52 // a doodle's scale: 20 to 25 px
		gap    = 0.6        // the least room between two
		stroke = 1.15       // the line, in px
	)
	rnd := rand.New(rand.NewPCG(Seed, 0)) //nolint:gosec // a fixed scatter, not a secret
	var bag []string
	var all []placed
	for misses := 0; misses < 20000; {
		if len(bag) == 0 {
			for _, d := range Doodles {
				bag = append(bag, d.Path)
			}
			rnd.Shuffle(len(bag), func(i, j int) { bag[i], bag[j] = bag[j], bag[i] })
		}
		scale := lo + rnd.Float64()*(hi-lo)
		r := 48 * scale * 0.46
		x, y := rnd.Float64()*Size, rnd.Float64()*Size
		room := true
		for _, p := range all {
			dx := math.Abs(p.x - x)
			dx = math.Min(dx, Size-dx)
			dy := math.Abs(p.y - y)
			dy = math.Min(dy, Size-dy)
			if dx*dx+dy*dy < (p.r+r+gap)*(p.r+r+gap) {
				room = false
				break
			}
		}
		if !room {
			misses++
			continue
		}
		misses = 0
		all = append(all, placed{x, y, r, scale, -25 + rnd.Float64()*50, bag[len(bag)-1]})
		bag = bag[:len(bag)-1]
	}
	var b strings.Builder
	fmt.Fprintf(&b, "<svg xmlns=\"http://www.w3.org/2000/svg\" width=\"%g\" height=\"%g\" viewBox=\"0 0 %g %g\">\n", Size, Size, Size, Size)
	b.WriteString("<g fill=\"none\" stroke=\"#000\" stroke-linecap=\"round\" stroke-linejoin=\"round\">\n")
	for _, p := range all {
		// Where it runs over an edge, drawn again on the far side.
		for _, ox := range []float64{-Size, 0, Size} {
			for _, oy := range []float64{-Size, 0, Size} {
				cx, cy := p.x+ox, p.y+oy
				if cx+p.r < 0 || cx-p.r > Size || cy+p.r < 0 || cy-p.r > Size {
					continue
				}
				fmt.Fprintf(&b, "<path transform=\"rotate(%.1f %.1f %.1f) translate(%.1f %.1f) scale(%.3f)\" stroke-width=\"%.2f\" d=\"%s\"/>\n",
					p.rot, cx, cy, cx-24*p.scale, cy-24*p.scale, p.scale, stroke/p.scale, p.path)
			}
		}
	}
	b.WriteString("</g>\n</svg>\n")
	return b.String()
}
