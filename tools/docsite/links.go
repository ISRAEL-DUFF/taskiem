package main

import (
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
)

// repoDocs reads every Markdown file the link checker covers: docs/**
// and the repository's top-level README.md and CONTRIBUTING.md. Keys are
// slash-separated paths from the repository root.
func repoDocs(root string) (map[string]*mdDoc, error) {
	out := map[string]*mdDoc{}
	err := filepath.WalkDir(filepath.Join(root, "docs"), func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(p, ".md") {
			return err
		}
		return readDoc(root, p, out)
	})
	if err != nil {
		return nil, err
	}
	for _, f := range []string{"README.md", "CONTRIBUTING.md"} {
		if _, err := os.Stat(filepath.Join(root, f)); err == nil {
			if err := readDoc(root, filepath.Join(root, f), out); err != nil {
				return nil, err
			}
		}
	}
	return out, nil
}

func readDoc(root, p string, out map[string]*mdDoc) error {
	src, err := os.ReadFile(p) //nolint:gosec // the repository's own docs
	if err != nil {
		return err
	}
	rel, err := filepath.Rel(root, p)
	if err != nil {
		return err
	}
	out[filepath.ToSlash(rel)] = parseMarkdown(src)
	return nil
}

// target is where a relative link points: a repository path and a fragment.
type target struct {
	path     string // slash-separated, from the repository root; "" for the same file
	fragment string
}

// resolve reads a link in the file from (a repository path). External
// links (with a scheme, or protocol-relative) are not resolved.
func resolve(from, dest string) (target, bool) {
	if dest == "" || strings.HasPrefix(dest, "//") {
		return target{}, false
	}
	u, err := url.Parse(dest)
	if err != nil || u.Scheme != "" {
		return target{}, false
	}
	p, err := url.PathUnescape(u.EscapedPath())
	if err != nil {
		p = u.Path
	}
	t := target{fragment: u.Fragment}
	switch {
	case p == "":
		t.path = from
	case strings.HasPrefix(p, "/"):
		t.path = path.Clean(strings.TrimPrefix(p, "/"))
	default:
		t.path = path.Clean(path.Join(path.Dir(from), p))
	}
	return t, true
}

// checkLinks checks every relative link and anchor in the docs: the file
// or directory exists inside the repository, and a fragment into a
// Markdown file names one of its headings. It returns one line per
// broken link, sorted.
func checkLinks(root string, docs map[string]*mdDoc) []string {
	var probs []string
	for from, d := range docs {
		for _, l := range d.links {
			t, ok := resolve(from, l.dest)
			if !ok {
				continue
			}
			where := fmt.Sprintf("%s:%d: %s", from, l.line, l.dest)
			if t.path == ".." || strings.HasPrefix(t.path, "../") {
				probs = append(probs, where+": leaves the repository")
				continue
			}
			info, err := os.Stat(filepath.Join(root, filepath.FromSlash(t.path)))
			if err != nil {
				probs = append(probs, where+": no such file")
				continue
			}
			if t.fragment == "" || info.IsDir() || !strings.HasSuffix(t.path, ".md") {
				continue // line anchors (#L10) into source files are not checked
			}
			td, ok := docs[t.path]
			if !ok {
				probs = append(probs, where+": not a checked Markdown file")
				continue
			}
			if !td.hasAnchor(t.fragment) {
				probs = append(probs, where+": no heading with id #"+t.fragment)
			}
		}
	}
	sort.Strings(probs)
	return probs
}
