package publicweb

import (
	"bytes"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"fmt"
	"html/template"
	"io/fs"
	"mime"
	"net/http"
	"path"
	"strings"

	"github.com/israel-duff/taskiem/engine/brand"
)

// Each theme has its own templates (templates/<theme>/), every page in
// each; templates/shared/ holds what is the same in all of them: the
// head's tags and a form's hidden fields.
//
//go:embed templates
var templateFS embed.FS

//go:embed static/*
var staticFS embed.FS

// staticPrefix is where static files are served, each under a name that
// carries a hash of its content, so it is cached for good and a change is
// a new name.
const staticPrefix = "/_static/"

type asset struct {
	body  []byte
	ctype string
}

var (
	// assetNames maps a file's own name (site.css) to its hashed path.
	assetNames = map[string]string{}
	// assets holds each file by its hashed name (site.3f9a1c2b7e.css).
	assets = map[string]asset{}
	// themed holds each theme's templates by theme name.
	themed = map[string]*template.Template{}
)

func init() {
	entries, err := fs.ReadDir(staticFS, "static")
	if err != nil {
		panic(err)
	}
	// Files that are not stylesheets are named first, so a stylesheet can
	// point at them (a font) by their own names and be served with their
	// hashed ones.
	var sheets []fs.DirEntry
	for _, e := range entries {
		if path.Ext(e.Name()) == ".css" {
			sheets = append(sheets, e)
			continue
		}
		addAsset(e.Name(), mustRead(e.Name()))
	}
	for _, e := range sheets {
		body := mustRead(e.Name())
		for name, hashed := range assetNames {
			if path.Ext(name) == ".woff2" {
				body = bytes.ReplaceAll(body, []byte("url("+name+")"), []byte("url("+hashed+")"))
			}
		}
		addAsset(e.Name(), body)
	}
	funcs := template.FuncMap{
		"asset": func(name string) (string, error) {
			p, ok := assetNames[name]
			if !ok {
				return "", fmt.Errorf("publicweb: no static file %q", name)
			}
			return p, nil
		},
		"accent":  accent,
		"swatch":  swatch,
		"optkind": optkind,
		"add":     func(a, b int) int { return a + b },
	}
	for _, theme := range brand.Themes {
		themed[theme] = template.Must(template.New("").Funcs(funcs).ParseFS(templateFS, "templates/shared/*.html", "templates/"+theme+"/*.html"))
	}
}

func mustRead(name string) []byte {
	body, err := staticFS.ReadFile("static/" + name)
	if err != nil {
		panic(err)
	}
	return body
}

// addAsset names a static file by a hash of its content and keeps it.
func addAsset(name string, body []byte) {
	sum := sha256.Sum256(body)
	ext := path.Ext(name)
	hashed := strings.TrimSuffix(name, ext) + "." + hex.EncodeToString(sum[:5]) + ext
	ctype := mime.TypeByExtension(ext)
	if ext == ".woff2" {
		ctype = "font/woff2"
	}
	assetNames[name] = staticPrefix + hashed
	assets[hashed] = asset{body: body, ctype: ctype}
}

func serveStatic(w http.ResponseWriter, r *http.Request) {
	a, ok := assets[strings.TrimPrefix(r.URL.Path, staticPrefix)]
	if !ok || (r.Method != http.MethodGet && r.Method != http.MethodHead) {
		http.NotFound(w, r)
		return
	}
	h := w.Header()
	h.Set("Content-Type", a.ctype)
	h.Set("Cache-Control", "public, max-age=31536000, immutable")
	h.Set("Cross-Origin-Resource-Policy", "same-origin")
	_, _ = w.Write(a.body)
}

// pageData is what every page template is given. Look is the business's
// look (look.go); without one, the page is drawn in the default look.
type pageData struct {
	Title string
	Lang  string
	Look  look
	// BagHref is the order this page's bag belongs to, for the shop
	// looks' bag in the header ("" for none).
	BagHref string
}

// Brand is the page's look, the default when it has none.
func (p pageData) Brand() look {
	if p.Look.CSS == "" {
		return defaultLook()
	}
	return p.Look
}

func (p pageData) theme() string { return p.Brand().theme() }

// themedPage is every page's data: it says which theme draws it.
type themedPage interface{ theme() string }

// page renders a template in full before writing, so a template error is
// a clean 500 rather than half a page, and answers If-None-Match with 304.
func (s *Server) page(w http.ResponseWriter, r *http.Request, status int, name string, d pageData) {
	if d.Lang == "" {
		d.Lang = "en"
	}
	s.render(w, r, status, name+".html", d)
}

// render executes a template with any data, as page does.
func (s *Server) render(w http.ResponseWriter, r *http.Request, status int, name string, d any) {
	s.renderWith(w, r, status, name, d, true)
}

// renderPrivate renders a page no cache may keep or share: a form, whose
// token makes each copy one submission, and a status page.
func (s *Server) renderPrivate(w http.ResponseWriter, r *http.Request, status int, name string, d any) {
	s.renderWith(w, r, status, name, d, false)
}

func (s *Server) renderWith(w http.ResponseWriter, r *http.Request, status int, name string, d any, shared bool) {
	theme := brand.Themes[0]
	if t, ok := d.(themedPage); ok {
		theme = t.theme()
	}
	var buf bytes.Buffer
	if err := themed[theme].ExecuteTemplate(&buf, name, d); err != nil {
		s.Logger.Error("public host: render", "page", name, "err", err)
		http.Error(w, "Something went wrong", http.StatusInternalServerError)
		return
	}
	h := w.Header()
	h.Set("Content-Type", "text/html; charset=utf-8")
	if shared && status == http.StatusOK {
		sum := sha256.Sum256(buf.Bytes())
		etag := `"` + hex.EncodeToString(sum[:12]) + `"`
		h.Set("ETag", etag)
		// Browsers revalidate; a CDN may keep it briefly (storefronts 8.7).
		h.Set("Cache-Control", "public, max-age=0, s-maxage=30, stale-while-revalidate=300")
		if r.Header.Get("If-None-Match") == etag {
			w.WriteHeader(http.StatusNotModified)
			return
		}
	} else {
		h.Set("Cache-Control", "no-store")
	}
	w.WriteHeader(status)
	if r.Method != http.MethodHead {
		_, _ = w.Write(buf.Bytes())
	}
}

// accent splits a headline in two so a look can set the second part in
// its accent style: at the first comma ("Hot food," / "made fresh today."),
// else before the last word ("Book your" / "glow."). A one-word headline
// has no second part.
func accent(s string) [2]string {
	s = strings.TrimSpace(s)
	if i := strings.Index(s, ","); i >= 0 && i < len(s)-1 {
		return [2]string{s[:i+1], strings.TrimSpace(s[i+1:])}
	}
	if i := strings.LastIndex(s, " "); i > 0 {
		return [2]string{s[:i], s[i+1:]}
	}
	return [2]string{s, ""}
}

// swatchNames are the colour words a choice's dot can be drawn in; the
// stylesheet has a class for each (sw-black, sw-red, ...).
var swatchNames = map[string]bool{
	"black": true, "white": true, "grey": true, "gray": true, "red": true, "orange": true, "yellow": true, "green": true,
	"blue": true, "navy": true, "purple": true, "pink": true, "brown": true, "cream": true, "beige": true, "gold": true,
	"moss": true, "olive": true, "teal": true, "maroon": true, "burgundy": true, "ivory": true, "tan": true, "khaki": true,
}

// swatch is the class of a colour choice's dot ("Black" gives sw-black), or
// sw-none when the choice is not a colour the stylesheet knows.
func swatch(name string) string {
	w := strings.ToLower(strings.TrimSpace(name))
	if i := strings.IndexAny(w, " (/-"); i > 0 {
		w = w[:i]
	}
	if swatchNames[w] {
		return "sw-" + w
	}
	return "sw-none"
}

// optkind is what a group of choices is, so a look can draw it: "colour"
// (dots), "size" (square buttons), or "other".
func optkind(name string) string {
	w := strings.ToLower(name)
	switch {
	case strings.Contains(w, "colour"), strings.Contains(w, "color"):
		return "colour"
	case strings.Contains(w, "size"):
		return "size"
	}
	return "other"
}
