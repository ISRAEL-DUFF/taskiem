// Command mobilegen writes the phone app's theme from the web chat's own CSS,
// and its API client from the API description (api.go)
// (docs/spec/mobile-app.md §4.1): every colour token light and dark, the
// shadows, the sizes, and every motion token and curve, read from
// web/src/styles.css, kit.css and motion.css. The app is checked against the
// web chat, so its look comes from the same source. Run it from the
// repository root with `go run ./tools/mobilegen` after changing those files;
// a test fails when mobile/lib/theme/tokens.g.dart is stale.
package main

import (
	"fmt"
	"os"
	"path/filepath"
)

// Out is where the theme is written, from the repository root.
const Out = "mobile/lib/theme/tokens.g.dart"

// Sources are the web's CSS files the theme is read from, in the order the
// web loads them (a later file's token wins, as in the browser).
var Sources = []string{"web/src/styles.css", "web/src/kit.css", "web/src/motion.css"}

func main() {
	root := "."
	if len(os.Args) > 1 {
		root = os.Args[1]
	}
	src, err := Generate(root)
	if err != nil {
		fmt.Fprintln(os.Stderr, "mobilegen:", err)
		os.Exit(1)
	}
	out := filepath.Join(root, Out)
	if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil { //nolint:gosec // a source directory
		fmt.Fprintln(os.Stderr, "mobilegen:", err)
		os.Exit(1)
	}
	if err := os.WriteFile(out, []byte(src), 0o644); err != nil { //nolint:gosec // a source file
		fmt.Fprintln(os.Stderr, "mobilegen:", err)
		os.Exit(1)
	}
	client, err := GenerateAPI(root)
	if err != nil {
		fmt.Fprintln(os.Stderr, "mobilegen:", err)
		os.Exit(1)
	}
	if err := os.MkdirAll(filepath.Join(root, filepath.Dir(APIOut)), 0o755); err != nil { //nolint:gosec // a source directory
		fmt.Fprintln(os.Stderr, "mobilegen:", err)
		os.Exit(1)
	}
	if err := os.WriteFile(filepath.Join(root, APIOut), []byte(client), 0o644); err != nil { //nolint:gosec // a source file
		fmt.Fprintln(os.Stderr, "mobilegen:", err)
		os.Exit(1)
	}
	for _, f := range Brand {
		b, err := os.ReadFile(filepath.Join(root, BrandFrom, f))
		if err == nil {
			err = os.MkdirAll(filepath.Join(root, BrandTo), 0o755) //nolint:gosec // a source directory
		}
		if err == nil {
			err = os.WriteFile(filepath.Join(root, BrandTo, f), b, 0o644) //nolint:gosec // a source file
		}
		if err != nil {
			fmt.Fprintln(os.Stderr, "mobilegen:", err)
			os.Exit(1)
		}
	}
}

// The brand's own files, copied unchanged: the mark is never redrawn
// (docs/brand/screens.md), on the web or in the app.
const (
	BrandFrom = "web/public/brand/relay"
	BrandTo   = "mobile/assets/brand"
)

// Brand is the brand files the app shows.
var Brand = []string{"mark.svg", "wordmark.svg", "wordmark-reversed.svg"}

// Generate reads the web's CSS under root and answers the Dart theme.
func Generate(root string) (string, error) {
	var light, dark []decl
	for _, f := range Sources {
		b, err := os.ReadFile(filepath.Join(root, f))
		if err != nil {
			return "", err
		}
		l, d := rootBlocks(string(b))
		light = append(light, l...)
		dark = append(dark, d...)
	}
	t, err := resolve(light, dark)
	if err != nil {
		return "", err
	}
	return dart(t), nil
}
