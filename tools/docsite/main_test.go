package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

const repoRoot = "../.."

func TestSlugify(t *testing.T) {
	for in, want := range map[string]string{
		"Phase 4 — public launch (about 16 weeks)": "phase-4--public-launch-about-16-weeks",
		"P4-1 Billing":           "p4-1-billing",
		"wd/v1 contract":         "wdv1-contract",
		"What's new?":            "whats-new",
		"10. Custom domains":     "10-custom-domains",
		"snake_case and Ünïcödé": "snake_case-and-ünïcödé",
	} {
		if got := slugify(in); got != want {
			t.Errorf("slugify(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestHeadingIDs(t *testing.T) {
	d := parseMarkdown([]byte("# Title\n\n## Set `TASKIEM_DOCS_URL` up\n\n## Twice\n\n## Twice\n"))
	for _, id := range []string{"title", "set-taskiem_docs_url-up", "twice", "twice-1"} {
		if !d.hasAnchor(id) {
			t.Errorf("no heading id %q in %v", id, d.headings)
		}
	}
	if d.title != "Title" {
		t.Errorf("title %q", d.title)
	}
}

// TestCheckLinksFindsBroken runs the checker on a small repository.
func TestCheckLinksFindsBroken(t *testing.T) {
	root := t.TempDir()
	write := func(name, body string) {
		p := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("README.md", "[ok](docs/a.md#a-heading) [bad](docs/nope.md)\n")
	write("docs/a.md", "# A heading\n\n[self](#a-heading) [own bad](#nothing)\n\n| x | y |\n| - | - |\n| [cell](b.md#missing) | [dir](sub/) |\n\n"+
		"`[not a link](gone.md)`\n\n```\n[nor this](gone.md)\n```\n\n[out](../../etc/passwd) [web](https://example.com/x.md#y) [src](../go.mod#L3)\n")
	write("docs/b.md", "# B\n\n## Twice\n\n## Twice\n\n[dup](#twice-1)\n")
	write("docs/sub/README.md", "# Sub\n")
	write("go.mod", "module x\n")
	docs, err := repoDocs(root)
	if err != nil {
		t.Fatal(err)
	}
	got := checkLinks(root, docs)
	want := []string{
		"README.md:1: docs/nope.md: no such file",
		"docs/a.md:15: ../../etc/passwd: leaves the repository",
		"docs/a.md:3: #nothing: no heading with id #nothing",
		"docs/a.md:7: b.md#missing: no heading with id #missing",
	}
	if !slices.Equal(got, want) {
		t.Errorf("problems:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

// TestDocsLinks is the docs link check: every relative link and anchor in
// docs/, README.md and CONTRIBUTING.md resolves.
func TestDocsLinks(t *testing.T) {
	docs, err := repoDocs(repoRoot)
	if err != nil {
		t.Fatal(err)
	}
	if len(docs) < 50 {
		t.Fatalf("only %d docs found", len(docs))
	}
	for _, p := range checkLinks(repoRoot, docs) {
		t.Error(p)
	}
}

// TestEveryDocClassified: each doc is public or internal, and the nav is sound.
func TestEveryDocClassified(t *testing.T) {
	fsys := os.DirFS(filepath.Join(repoRoot, "docs"))
	public, err := classify(fsys)
	if err != nil {
		t.Fatal(err)
	}
	titles := map[string]string{}
	for _, p := range public {
		titles[p] = p
	}
	sections, err := navPages(fsys, titles)
	if err != nil {
		t.Fatal(err)
	}
	inNav := 0
	for _, s := range sections {
		inNav += len(s)
	}
	if inNav != len(public) {
		t.Errorf("%d public pages, %d in the navigation", len(public), inNav)
	}
	for _, x := range internal {
		if m, _ := filepathGlobDocs(x.pattern); len(m) == 0 {
			t.Errorf("internal pattern %q matches no doc", x.pattern)
		}
	}
}

func filepathGlobDocs(pattern string) ([]string, error) {
	m, err := filepath.Glob(filepath.Join(repoRoot, "docs", filepath.FromSlash(pattern)+".md"))
	if len(m) == 0 && err == nil {
		m, err = filepath.Glob(filepath.Join(repoRoot, "docs", filepath.FromSlash(pattern)))
	}
	return m, err
}

// TestHelpLinksArePages: the web app's help panels link to
// TASKIEM_DOCS_URL/<doc> (web/src/lib/onboarding.ts); each must be a page.
func TestHelpLinksArePages(t *testing.T) {
	src, err := os.ReadFile(filepath.Join(repoRoot, "web/src/lib/onboarding.ts"))
	if err != nil {
		t.Fatal(err)
	}
	docs := regexp.MustCompile(`\bdoc: "([^"]+)"`).FindAllStringSubmatch(string(src), -1)
	if len(docs) == 0 {
		t.Fatal("no help links found in web/src/lib/onboarding.ts")
	}
	public, err := classify(os.DirFS(filepath.Join(repoRoot, "docs")))
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range docs {
		if !slices.Contains(public, d[1]) {
			t.Errorf("help link %q is not a public docs page", d[1])
		}
	}
}

var hrefRE = regexp.MustCompile(`(?:href|src)="([^"]*)"`)

// TestBuild builds the site and checks its own links: every link within
// the site reaches a page, and every anchor a heading or operation on it.
func TestBuild(t *testing.T) {
	for _, cfg := range []Config{
		{Base: "/"},
		{Base: "/docs/", Source: "https://example.com/repo/blob/main/"},
		{Base: "/", Site: true, App: "https://app.example.ng", WhatsApp: "+234 800 000 0000", Email: "hello@example.ng"},
		{Base: "/taskiem/", Site: true},
	} {
		files, err := build(repoRoot, cfg)
		if err != nil {
			t.Fatal(err)
		}
		docs := "" // where the docs' files are
		if cfg.Site {
			docs = "docs/"
		}
		for _, f := range []string{"index.html", "404.html", "onboarding.html", "templates.html", "connector-sdk.html", "environments.html",
			"contracts.html", "contracts/wd-v1.html", "integrations/slack.html", "api.html", "api/workflows.html", "api/schemas.html",
			"api/openapi.yaml", "search.json", "style.css", "search.js"} {
			if _, ok := files[docs+f]; !ok {
				t.Errorf("%s: no %s", cfg.Base, docs+f)
			}
		}
		for _, f := range []string{"needs-people.html", "phase-4-status.html", "decisions/0022-self-serve-onboarding.html", "spec/architecture.html", "integrations/archive/payrolla.html"} {
			if _, ok := files[docs+f]; ok {
				t.Errorf("%s: internal page %s published", cfg.Base, f)
			}
		}
		if cfg.Site {
			for _, p := range wwwPages {
				if _, ok := files[p.Name+".html"]; !ok {
					t.Errorf("%s: no website page %s", cfg.Base, p.Name)
				}
			}
			if _, ok := files["www.css"]; !ok {
				t.Errorf("%s: no www.css", cfg.Base)
			}
			// A page for each service (K9), linked from the integrations
			// page, with what it does and the templates that use it.
			for page, wants := range map[string][]string{
				"integrations/mailchimp":  {"One click: sign in to Mailchimp", "Add or update a member", "Put people who opted in on a Mailchimp audience", cfg.Base + "docs/integrations/mailchimp"},
				"integrations/messenger":  {"Messages to your Facebook Page", cfg.Base + "docs/integrations/messenger-and-instagram"},
				"integrations/quickbooks": {"Copy an invoice to the books", "Uses QuickBooks or Zoho Books"},
				"integrations/paystack":   {"Paste the key from your Paystack account"},
				"integrations/schools":    {"there is nothing to connect"},
			} {
				body := string(files[page+".html"])
				for _, want := range wants {
					if !strings.Contains(body, want) {
						t.Errorf("%s: %s lacks %q", cfg.Base, page, want)
					}
				}
			}
			if !strings.Contains(string(files["integrations.html"]), `href="`+cfg.Base+`integrations/instagram"`) {
				t.Errorf("%s: the integrations page does not link Instagram's page", cfg.Base)
			}
			home := string(files["index.html"])
			for _, want := range []string{"Tell Taskiem what to do in your own words", cfg.Base + "docs/", "Paystack", cfg.Base + "img/logo.png", "material-symbols-outlined"} {
				if !strings.Contains(home, want) {
					t.Errorf("%s: home page lacks %q", cfg.Base, want)
				}
			}
			// The outside facts show only when given (docs/strategy/website.md, the claims register).
			for name, want := range map[string]bool{"wa.me/2348000000000": cfg.WhatsApp != "", "https://app.example.ng/signup": cfg.App != "", "start/guide?template=": cfg.App != "", "mailto:hello@example.ng": cfg.Email != "", "opening soon": cfg.App == "" && cfg.WhatsApp == ""} {
				all := home + string(files["templates.html"]) + string(files["contact.html"])
				if got := strings.Contains(all, name); got != want {
					t.Errorf("%s: website mentions %q: %v, want %v", cfg.Base, name, got, want)
				}
			}
		} else {
			for _, f := range []string{"how-it-works.html", "security.html", "www.css"} {
				if _, ok := files[f]; ok {
					t.Errorf("%s: website file %s built without Site", cfg.Base, f)
				}
			}
		}
		ids := map[string]map[string]bool{}
		idRE := regexp.MustCompile(`\bid="([^"]+)"`)
		for name, body := range files {
			if strings.HasSuffix(name, ".html") {
				m := map[string]bool{}
				for _, id := range idRE.FindAllStringSubmatch(string(body), -1) {
					m[id[1]] = true
				}
				ids[strings.TrimSuffix(name, ".html")] = m
			}
		}
		for name, body := range files {
			if !strings.HasSuffix(name, ".html") {
				continue
			}
			for _, h := range hrefRE.FindAllStringSubmatch(string(body), -1) {
				href := strings.ReplaceAll(h[1], "&amp;", "&")
				if strings.Contains(href, ".md") && !strings.HasPrefix(href, cfg.Source) {
					t.Errorf("%s%s: link %s still points at Markdown", cfg.Base, name, href)
				}
				if !strings.HasPrefix(href, cfg.Base) {
					continue
				}
				target, frag, _ := strings.Cut(strings.TrimPrefix(href, cfg.Base), "#")
				switch {
				case target == "" || strings.HasSuffix(target, "/"):
					target += "index"
				case strings.Contains(target, "."):
					if _, ok := files[target]; !ok {
						t.Errorf("%s%s: link %s: no file", cfg.Base, name, href)
					}
					continue
				}
				anchors, ok := ids[target]
				if !ok {
					t.Errorf("%s%s: link %s: no page", cfg.Base, name, href)
				} else if frag != "" && !anchors[frag] {
					t.Errorf("%s%s: link %s: no anchor", cfg.Base, name, href)
				}
			}
		}
		var idx []entry
		if err := json.Unmarshal(files[docs+"search.json"], &idx); err != nil {
			t.Fatal(err)
		}
		found := false
		for _, e := range idx {
			found = found || (strings.Contains(e.URL, "api/runs#startRun") && e.Section == "Start a run")
		}
		if len(idx) < 200 || !found {
			t.Errorf("search index: %d entries, start-run entry found %v", len(idx), found)
		}
	}
}

func TestPrepareRefusesForeignDirectory(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "precious.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := prepare(dir); err == nil {
		t.Fatal("prepare emptied a directory docsite did not write")
	}
	out := filepath.Join(t.TempDir(), "site")
	if err := prepare(out); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(out, "old.html"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := prepare(out); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(out, "old.html")); !os.IsNotExist(err) {
		t.Errorf("old output kept: %v", err)
	}
}

// TestPricingWords: plan limits and prices in the owner's words, and no
// price while deploy/plans.yaml holds placeholders.
func TestPricingWords(t *testing.T) {
	for n, want := range map[int64]string{500000: "₦5,000", 100000000: "₦1,000,000", 99900: "₦999", 0: "₦0"} {
		if got := naira(n); got != want {
			t.Errorf("naira(%d) = %q, want %q", n, got, want)
		}
	}
	p, err := loadPricing(repoRoot)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Plans) < 3 || p.TrialDays == 0 || p.TrialPlan == "" || len(p.Compare) < 5 {
		t.Fatalf("pricing: %+v", p)
	}
	for _, r := range p.Compare {
		if len(r.Cells) != len(p.Plans) {
			t.Errorf("compare row %q has %d cells for %d plans", r.Label, len(r.Cells), len(p.Plans))
		}
	}
	for _, pl := range p.Plans {
		if p.Placeholder && pl.Monthly != "" {
			t.Errorf("%s: a price %q while prices are placeholders", pl.ID, pl.Monthly)
		}
		if len(pl.Includes) == 0 {
			t.Errorf("%s: nothing included", pl.ID)
		}
	}
	// The website's three tiers (docs/strategy/website.md) are the plans file's.
	for id, want := range map[string]string{"starter": "₦15,000", "growth": "₦45,000", "enterprise": "₦180,000"} {
		found := false
		for _, pl := range p.Plans {
			if pl.ID == id {
				found = true
				if pl.Monthly != want {
					t.Errorf("%s: %s a month, the website says %s", id, pl.Monthly, want)
				}
			}
		}
		if !found {
			t.Errorf("no public plan %q", id)
		}
	}
}

// TestWebsiteRefusesABadNumber: -whatsapp must be a phone number.
func TestWebsiteRefusesABadNumber(t *testing.T) {
	err := buildWebsite(repoRoot, Config{Base: "/", Site: true, WhatsApp: "chat to us"}, "/docs/", nil, map[string][]byte{})
	if err == nil || !strings.Contains(err.Error(), "not a phone number") {
		t.Errorf("err = %v", err)
	}
}

// A template on rails names the rails that can run it, not every rail.
func TestTemplateRails(t *testing.T) {
	conns, err := loadConnectors(repoRoot)
	if err != nil {
		t.Fatal(err)
	}
	for uses, want := range map[string]string{
		"pay_bank,balance,resolve_account":          "Paystack, Moniepoint (Monnify), iswallet, Flutterwave or Anchor",
		"refund,get_payment":                        "Paystack",
		"virtual_account":                           "Paystack, Moniepoint (Monnify), iswallet or Flutterwave",
		"virtual_account.credited":                  "Paystack, Moniepoint (Monnify) or Flutterwave",
		"payment.received,virtual_account.credited": "Paystack, Moniepoint (Monnify) or Flutterwave",
	} {
		if got := railsFor(strings.Split(uses, ","), conns); got != want {
			t.Errorf("%s: %q, want %q", uses, got, want)
		}
	}
}
