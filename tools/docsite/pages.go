package main

import (
	"fmt"
	"io/fs"
	"path"
	"path/filepath"
	"slices"
	"strings"
)

// Which docs are public (decision 0025). Every Markdown file under docs/
// is either listed in nav, and published, or matched by internal, and
// not; a test fails for a file that is neither, so a new doc is a choice.

// section is a group of pages in the site's navigation. A page is named
// by its path under docs/ without ".md" ("integrations/slack"); a name
// ending in "/*" stands for every public page in that directory, by
// title; "README" files are named by their directory ("contracts").
type section struct {
	Title string
	Pages []string
}

var nav = []section{
	{"Get started", []string{"onboarding", "templates", "dashboard", "environments", "cli"}},
	{"Build workflows", []string{"sdk", "code-steps", "container-steps", "ai", "alerts"}},
	{"Connectors", []string{"connector-sdk", "connector-submissions", "integrations/*"}},
	{"Channels", []string{"whatsapp", "ussd"}},
	{"Govern and secure", []string{"governance", "privacy", "compliance", "byok", "git"}},
	{"Embed Taskiem", []string{"embedding"}},
	{"Plans and billing", []string{"billing"}},
	{"Run it yourself", []string{"operations", "kubernetes", "cloud", "reliability", "operator-console"}},
	{"Contracts", []string{"contracts", "contracts/*"}},
}

// internal are the docs the site leaves out, by path pattern
// (path.Match, without ".md"), and why.
var internal = []struct{ pattern, why string }{
	{"phase-*-status", "build-plan progress for the team"},
	{"needs-people", "the team's list of decisions and work waiting on people"},
	{"pgdock-integration", "a delivery plan"},
	{"clean-room-policy", "how contributors work; it belongs with CONTRIBUTING.md"},
	{"spec/*", "the internal product specification and build plan"},
	{"security/*", "the threat model, self-review and pen-test scope"},
	{"decisions/*", "architecture decision records for the team"},
	{"integrations/iswallet-answers-*", "correspondence with a partner"},
	{"integrations/payrolla", "a guide addressed to one partner"},
}

// isInternal reports whether a docs page (path without ".md") stays out of the site.
func isInternal(name string) (bool, string) {
	for _, x := range internal {
		// A directory's README page is named by the directory.
		ok, _ := path.Match(x.pattern, name)
		readme, _ := path.Match(x.pattern, name+"/README")
		if ok || readme {
			return true, x.why
		}
	}
	return false, ""
}

// pageName is a docs-relative Markdown path's page name: "contracts/README.md" is "contracts".
func pageName(rel string) string {
	rel = filepath.ToSlash(strings.TrimSuffix(rel, ".md"))
	if d, f := path.Split(rel); f == "README" && d != "" {
		return strings.TrimSuffix(d, "/")
	}
	return rel
}

// sourceOf is the docs-relative Markdown path for a page name.
func sourceOf(fsys fs.FS, name string) string {
	if _, err := fs.Stat(fsys, name+".md"); err == nil {
		return name + ".md"
	}
	return name + "/README.md"
}

// navPages expands nav against the docs present: each section's pages in
// order, globs by title. It fails on a named page that does not exist, is
// internal, or appears twice.
func navPages(fsys fs.FS, titles map[string]string) ([][]string, error) {
	seen := map[string]bool{}
	var out [][]string
	for _, sec := range nav {
		var pages []string
		for _, p := range sec.Pages {
			var names []string
			if dir, ok := strings.CutSuffix(p, "/*"); ok {
				matches, err := fs.Glob(fsys, dir+"/*.md")
				if err != nil {
					return nil, err
				}
				for _, m := range matches {
					n := pageName(m)
					if bad, _ := isInternal(n); !bad && n != dir {
						names = append(names, n)
					}
				}
				slices.SortFunc(names, func(a, b string) int { return strings.Compare(strings.ToLower(titles[a]), strings.ToLower(titles[b])) })
			} else {
				if _, err := fs.Stat(fsys, sourceOf(fsys, p)); err != nil {
					return nil, fmt.Errorf("nav names %s, which is not in docs/", p)
				}
				if bad, _ := isInternal(p); bad {
					return nil, fmt.Errorf("nav names %s, which is internal", p)
				}
				names = []string{p}
			}
			for _, n := range names {
				if seen[n] {
					return nil, fmt.Errorf("nav names %s twice", n)
				}
				seen[n] = true
			}
			pages = append(pages, names...)
		}
		out = append(out, pages)
	}
	return out, nil
}
