package publicweb

import (
	"io/fs"
	"regexp"
	"strings"
	"testing"

	"github.com/israel-duff/taskiem/engine/brand"
)

// Page budgets (docs/spec/storefronts.md 8.4): a public page must be usable
// within 3 seconds on 3G on a low-end phone, so its HTML and the files it
// loads are kept small. These are the ceilings; pages should sit well
// under them.
const (
	maxPageHTML   = 60 << 10 // per page, before images
	maxStylesPage = 15 << 10 // the stylesheets a page loads: its theme's and its colour's
	maxScriptTot  = 10 << 10 // every script together
)

func TestPageBudget(t *testing.T) {
	f := &fakeHosts{sites: map[string]Site{"ada.shops.test": {Kind: "storefront"}, "closed.shops.test": {Suspended: true}}}
	_, h := newServer(f)
	for _, host := range []string{"ada.shops.test", "closed.shops.test", "nobody.shops.test"} {
		if n := get(h, "GET", host, "/").Body.Len(); n > maxPageHTML {
			t.Errorf("%s: page is %d bytes, budget %d", host, n, maxPageHTML)
		}
	}
	// A hosted form, with every kind of field.
	e := newFormHost(t)
	e.add("widgets00001", `{"title":"Everything"}`, widgetSchema)
	e.add("loan00000001", loanTrigger, loanSchema)
	for _, path := range []string{"/f/widgets00001", "/f/loan00000001", "/embed/f/loan00000001"} {
		if n := e.get(path).Body.Len(); n > maxPageHTML {
			t.Errorf("%s: page is %d bytes, budget %d", path, n, maxPageHTML)
		}
	}
	var js int
	for name, a := range assets {
		if strings.HasSuffix(name, ".js") {
			js += len(a.body)
		}
	}
	if js > maxScriptTot {
		t.Errorf("static files: js %d bytes (budget %d)", js, maxScriptTot)
	}
	// A page loads one theme's stylesheet, and its colour's.
	colour := len(brand.PaletteOf("#0b6e4f").CSS())
	for _, theme := range brand.Themes {
		a, ok := assets[strings.TrimPrefix(assetNames["theme-"+theme+".css"], staticPrefix)]
		if !ok {
			t.Fatalf("%s: no stylesheet", theme)
		}
		if n := len(a.body) + colour; n > maxStylesPage {
			t.Errorf("%s: stylesheets %d bytes, budget %d", theme, n, maxStylesPage)
		}
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
