package main

import (
	"bytes"
	"embed"
	"encoding/json"
	"fmt"
	"html/template"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/yuin/goldmark/ast"

	"github.com/israel-duff/taskiem/api/openapi"
)

// Config is how the site is served.
type Config struct {
	Base   string // URL path the site lives under, "/" or "/docs/"
	Source string // URL prefix for repository files; "" unlinks them
}

//go:embed assets
var assets embed.FS

var layout = template.Must(template.ParseFS(assets, "assets/layout.html"))

// page is one published docs page.
type page struct {
	Name  string // "integrations/slack"
	Src   string // "integrations/slack.md", under docs/
	Title string
	doc   *mdDoc
}

// navItem and navSection feed the layout's navigation.
type navItem struct {
	Title, URL string
	Current    bool
}

type navSection struct {
	Title string
	Items []navItem
}

// entry is one search index record: a page or one of its sections.
type entry struct {
	Title   string `json:"t"`           // the page
	Section string `json:"s,omitempty"` // the heading, if a section
	URL     string `json:"u"`
	Text    string `json:"x"`
}

type site struct {
	cfg     Config
	files   map[string][]byte
	pages   map[string]*page // by name
	nav     [][]string       // page names per nav section; the API reference is added after
	api     []navItem
	entries []entry
}

// classify lists every Markdown file under docs/ by page name and says
// which are public. A file neither in nav nor internal is an error.
func classify(fsys fs.FS) (public []string, err error) {
	inNav := map[string]bool{}
	for _, sec := range nav {
		for _, p := range sec.Pages {
			inNav[p] = true
		}
	}
	err = fs.WalkDir(fsys, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(p, ".md") {
			return err
		}
		name := pageName(p)
		if bad, _ := isInternal(name); bad {
			return nil
		}
		dir := path.Dir(name)
		if !inNav[name] && !inNav[dir+"/*"] {
			return fmt.Errorf("docs/%s is neither public (nav in tools/docsite/pages.go) nor internal; decide which", p)
		}
		public = append(public, name)
		return nil
	})
	sort.Strings(public)
	return public, err
}

func build(root string, cfg Config) (map[string][]byte, error) {
	fsys := os.DirFS(filepath.Join(root, "docs"))
	names, err := classify(fsys)
	if err != nil {
		return nil, err
	}
	s := &site{cfg: cfg, files: map[string][]byte{}, pages: map[string]*page{}}
	titles := map[string]string{}
	for _, n := range names {
		src := sourceOf(fsys, n)
		raw, err := fs.ReadFile(fsys, src)
		if err != nil {
			return nil, err
		}
		p := &page{Name: n, Src: src, doc: parseMarkdown(raw)}
		p.Title = p.doc.title
		if p.Title == "" {
			p.Title = n
		}
		s.pages[n], titles[n] = p, p.Title
	}
	if s.nav, err = navPages(fsys, titles); err != nil {
		return nil, err
	}
	doc, err := openapi.Load()
	if err != nil {
		return nil, err
	}
	s.api = apiNav(cfg, doc)
	for _, n := range names {
		if err := s.renderPage(s.pages[n]); err != nil {
			return nil, fmt.Errorf("docs/%s: %w", s.pages[n].Src, err)
		}
	}
	if err := s.renderAPI(doc); err != nil {
		return nil, err
	}
	if err := s.renderHome(); err != nil {
		return nil, err
	}
	notFound := template.HTML(`<h1>Not found</h1><p>This page does not exist. Try the search above, or start from <a href="` + //nolint:gosec // the base is escaped
		template.HTMLEscapeString(cfg.Base) + `">the contents</a>.</p>`)
	if err := s.render("404.html", "Not found", "", notFound, ""); err != nil {
		return nil, err
	}
	idx, err := json.Marshal(s.entries)
	if err != nil {
		return nil, err
	}
	s.files["search.json"] = idx
	for _, a := range []string{"style.css", "search.js"} {
		b, err := assets.ReadFile("assets/" + a)
		if err != nil {
			return nil, err
		}
		s.files[a] = b
	}
	s.files["api/openapi.yaml"] = openapi.Source()
	return s.files, nil
}

// url is a page's address on the site.
func (s *site) url(name string) string { return s.cfg.Base + name }

// rewriteLinks points a page's links at the site: public pages by their
// stable URL, anything else in the repository at the source URL, or
// unlinked without one.
func (s *site) rewriteLinks(p *page) {
	from := "docs/" + p.Src
	for _, l := range p.doc.links {
		t, ok := resolve(from, l.dest)
		if !ok {
			continue
		}
		dest := ""
		if rel, ok := strings.CutPrefix(t.path, "docs/"); ok {
			name := pageName(rel)
			if _, public := s.pages[name]; public && (strings.HasSuffix(rel, ".md") || s.isDirPage(rel)) {
				dest = s.url(name)
				if t.fragment != "" {
					dest += "#" + t.fragment
				}
			}
		}
		if dest == "" && s.cfg.Source != "" {
			dest = s.cfg.Source + t.path
			if t.fragment != "" {
				dest += "#" + t.fragment
			}
		}
		switch n := l.node.(type) {
		case *ast.Link:
			if dest == "" {
				unlink(n)
			} else {
				n.Destination = []byte(dest)
			}
		case *ast.Image:
			if dest != "" {
				n.Destination = []byte(dest)
			}
		}
	}
}

// isDirPage reports whether a docs directory link means its README page.
func (s *site) isDirPage(rel string) bool {
	p, ok := s.pages[strings.TrimSuffix(rel, "/")]
	return ok && strings.HasSuffix(p.Src, "/README.md")
}

func (s *site) renderPage(p *page) error {
	s.rewriteLinks(p)
	body, err := p.doc.render()
	if err != nil {
		return err
	}
	src := ""
	if s.cfg.Source != "" {
		src = s.cfg.Source + "docs/" + p.Src
	}
	s.index(p)
	return s.render(p.Name+".html", p.Title, p.Name, template.HTML(body), src) //nolint:gosec // goldmark escapes text and drops raw HTML
}

// index adds a page and each of its sections to the search index.
func (s *site) index(p *page) {
	cur := entry{Title: p.Title, URL: s.url(p.Name)}
	var text []string
	flush := func() {
		cur.Text = strings.Join(text, " ")
		if cur.Section != "" || cur.Text != "" {
			s.entries = append(s.entries, cur)
		}
		text = nil
	}
	for n := p.doc.root.FirstChild(); n != nil; n = n.NextSibling() {
		if h, ok := n.(*ast.Heading); ok && h.Level >= 2 && h.Level <= 3 {
			flush()
			id, _ := h.AttributeString("id")
			idb, _ := id.([]byte)
			cur = entry{Title: p.Title, Section: plainText(h, p.doc.src), URL: s.url(p.Name) + "#" + string(idb)}
			continue
		}
		if h, ok := n.(*ast.Heading); ok && h.Level == 1 {
			continue
		}
		if t := blockText(n, p.doc.src); t != "" {
			text = append(text, t)
		}
	}
	flush()
}

// render wraps content in the layout and adds it to the site.
func (s *site) render(file, title, current string, content template.HTML, source string) error {
	var secs []navSection
	for i, names := range s.nav {
		sec := navSection{Title: nav[i].Title}
		for _, n := range names {
			sec.Items = append(sec.Items, navItem{Title: s.pages[n].Title, URL: s.url(n), Current: n == current})
		}
		secs = append(secs, sec)
	}
	api := navSection{Title: "API reference"}
	for _, it := range s.api {
		it.Current = it.URL == s.url(current)
		api.Items = append(api.Items, it)
	}
	secs = append(secs, api)
	var buf bytes.Buffer
	err := layout.Execute(&buf, map[string]any{
		"Base": s.cfg.Base, "Title": title, "Nav": secs, "Content": content, "Source": source,
	})
	if err != nil {
		return err
	}
	s.files[file] = buf.Bytes()
	return nil
}

// renderHome is the contents page.
func (s *site) renderHome() error {
	var b strings.Builder
	b.WriteString(`<h1>Taskiem documentation</h1><p class="lead">A workflow automation engine that regulated businesses can trust with money movement: durable, replayable runs with exactly-once external effects, compliance controls built in, and first-class African integrations. These guides cover building and running workflows, connecting services, embedding Taskiem in your own product and running it yourself.</p><div class="cards">`)
	for i, names := range s.nav {
		fmt.Fprintf(&b, `<section class="card"><h2>%s</h2><ul>`, template.HTMLEscapeString(nav[i].Title))
		for _, n := range names {
			fmt.Fprintf(&b, `<li><a href="%s">%s</a></li>`, template.HTMLEscapeString(s.url(n)), template.HTMLEscapeString(s.pages[n].Title))
		}
		b.WriteString(`</ul></section>`)
	}
	b.WriteString(`<section class="card"><h2>API reference</h2><ul>`)
	for _, it := range s.api {
		fmt.Fprintf(&b, `<li><a href="%s">%s</a></li>`, template.HTMLEscapeString(it.URL), template.HTMLEscapeString(it.Title))
	}
	b.WriteString(`</ul></section></div>`)
	return s.render("index.html", "Taskiem documentation", "", template.HTML(b.String()), "") //nolint:gosec // escaped above
}
