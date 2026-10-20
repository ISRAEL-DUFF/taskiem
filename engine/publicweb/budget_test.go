package publicweb

import (
	"io/fs"
	"regexp"
	"strings"
	"testing"
)

// Page budgets (docs/spec/storefronts.md 8.4): a public page must be usable
// within 3 seconds on 3G on a low-end phone, so its HTML and the files it
// loads are kept small. These are the ceilings; pages should sit well
// under them.
const (
	maxPageHTML  = 60 << 10 // per page, before images
	maxStylesTot = 15 << 10 // every stylesheet together
	maxScriptTot = 10 << 10 // every script together
)

func TestPageBudget(t *testing.T) {
	f := &fakeHosts{sites: map[string]Site{"ada.shops.test": {Kind: "storefront"}, "closed.shops.test": {Suspended: true}}}
	_, h := newServer(f)
	for _, host := range []string{"ada.shops.test", "closed.shops.test", "nobody.shops.test"} {
		if n := get(h, "GET", host, "/").Body.Len(); n > maxPageHTML {
			t.Errorf("%s: page is %d bytes, budget %d", host, n, maxPageHTML)
		}
	}
	var css, js int
	for name, a := range assets {
		switch {
		case strings.HasSuffix(name, ".css"):
			css += len(a.body)
		case strings.HasSuffix(name, ".js"):
			js += len(a.body)
		}
	}
	if css > maxStylesTot || js > maxScriptTot {
		t.Errorf("static files: css %d bytes (budget %d), js %d bytes (budget %d)", css, maxStylesTot, js, maxScriptTot)
	}
}

// policyBreaks are what the public CSP refuses in a page.
var policyBreaks = []*regexp.Regexp{
	regexp.MustCompile(`(?i)<script(\s[^>]*)?>\s*(?:[^<\s]|<[^/])`), // a script with a body, not just its end tag
	regexp.MustCompile(`(?i)<script(?:\s[^>]*)?\ssrc=["']?(https?:)?//`),
	regexp.MustCompile(`(?i)<style`),
	regexp.MustCompile(`(?i)\sstyle=`),
	regexp.MustCompile(`(?i)\son[a-z]+=`),
	regexp.MustCompile(`(?i)javascript:`),
}

// TestTemplatesKeepThePolicy: the public CSP allows no inline script,
// style or event handler, so a template that used one would break in the
// browser. Catch it here instead.
func TestTemplatesKeepThePolicy(t *testing.T) {
	err := fs.WalkDir(templateFS, "templates", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := templateFS.ReadFile(p)
		if err != nil {
			return err
		}
		for _, re := range policyBreaks {
			if re.Match(b) {
				t.Errorf("%s: %s is not allowed by the public CSP", p, re)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
