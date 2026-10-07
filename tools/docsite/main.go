// Command docsite builds Taskiem's public docs site: the public pages of
// docs/ (pages.go says which, decision 0025), an API reference rendered
// from api/openapi/openapi.yaml, navigation and a static search index. It
// first checks every relative link and anchor in the repository's docs and
// fails on a broken one.
//
//	go run ./tools/docsite                   # check links, build into site/
//	go run ./tools/docsite -check            # check links only
//	go run ./tools/docsite -base /docs/ -source https://github.com/OWNER/REPO/blob/main/
//
// Pages are written as <name>.html and linked without the extension
// (/templates), the stable URLs the web app's help panels open
// (TASKIEM_DOCS_URL/<page>, docs/onboarding.md#in-product-help). Hosts
// that do not serve name.html for /name need a rewrite (docs/operations.md
// names one).
package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func main() {
	root := flag.String("root", ".", "the repository root")
	out := flag.String("out", "site", "where to write the site (made empty first)")
	base := flag.String("base", "/", "the URL path the site is served under")
	source := flag.String("source", "", "a URL prefix for repository files the site does not publish, e.g. https://github.com/OWNER/REPO/blob/main/; without it such links become plain text")
	check := flag.Bool("check", false, "only check the docs' links")
	flag.Parse()
	if err := run(*root, *out, *base, *source, *check); err != nil {
		fmt.Fprintln(os.Stderr, "docsite:", err)
		os.Exit(1)
	}
}

func run(root, out, base, source string, check bool) error {
	docs, err := repoDocs(root)
	if err != nil {
		return err
	}
	if probs := checkLinks(root, docs); len(probs) > 0 {
		for _, p := range probs {
			fmt.Fprintln(os.Stderr, p)
		}
		return fmt.Errorf("%d broken links", len(probs))
	}
	if check {
		fmt.Printf("docsite: %d files, links and anchors resolve\n", len(docs))
		return nil
	}
	if !strings.HasPrefix(base, "/") || !strings.HasSuffix(base, "/") {
		return errors.New("-base must start and end with /")
	}
	files, err := build(root, Config{Base: base, Source: source})
	if err != nil {
		return err
	}
	if err := prepare(out); err != nil {
		return err
	}
	for name, body := range files {
		p := filepath.Join(out, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil { //nolint:gosec // a public site
			return err
		}
		if err := os.WriteFile(p, body, 0o644); err != nil { //nolint:gosec // a public site
			return err
		}
	}
	fmt.Printf("docsite: wrote %d files to %s\n", len(files), out)
	return nil
}

// marker marks a directory docsite wrote, so it may empty it next time.
const marker = ".docsite"

// prepare empties the output directory, refusing one docsite did not make.
func prepare(out string) error {
	entries, err := os.ReadDir(out)
	switch {
	case errors.Is(err, os.ErrNotExist):
	case err != nil:
		return err
	case len(entries) > 0:
		if _, err := os.Stat(filepath.Join(out, marker)); err != nil {
			return fmt.Errorf("%s is not empty and was not written by docsite; choose another -out", out)
		}
		if err := os.RemoveAll(out); err != nil {
			return err
		}
	}
	if err := os.MkdirAll(out, 0o755); err != nil { //nolint:gosec // a public site
		return err
	}
	return os.WriteFile(filepath.Join(out, marker), []byte("written by go run ./tools/docsite\n"), 0o644) //nolint:gosec // a public site
}
