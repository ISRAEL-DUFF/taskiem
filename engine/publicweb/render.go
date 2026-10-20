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
)

//go:embed templates/*.html
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
	pages  *template.Template
)

func init() {
	entries, err := fs.ReadDir(staticFS, "static")
	if err != nil {
		panic(err)
	}
	for _, e := range entries {
		body, err := staticFS.ReadFile("static/" + e.Name())
		if err != nil {
			panic(err)
		}
		sum := sha256.Sum256(body)
		ext := path.Ext(e.Name())
		hashed := strings.TrimSuffix(e.Name(), ext) + "." + hex.EncodeToString(sum[:5]) + ext
		assetNames[e.Name()] = staticPrefix + hashed
		assets[hashed] = asset{body: body, ctype: mime.TypeByExtension(ext)}
	}
	pages = template.Must(template.New("").Funcs(template.FuncMap{
		"asset": func(name string) (string, error) {
			p, ok := assetNames[name]
			if !ok {
				return "", fmt.Errorf("publicweb: no static file %q", name)
			}
			return p, nil
		},
	}).ParseFS(templateFS, "templates/*.html"))
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

// pageData is what every page template is given.
type pageData struct {
	Title string
	Lang  string
}

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
	var buf bytes.Buffer
	if err := pages.ExecuteTemplate(&buf, name, d); err != nil {
		s.Logger.Error("public host: render", "page", name, "err", err)
		http.Error(w, "Something went wrong", http.StatusInternalServerError)
		return
	}
	sum := sha256.Sum256(buf.Bytes())
	etag := `"` + hex.EncodeToString(sum[:12]) + `"`
	h := w.Header()
	h.Set("Content-Type", "text/html; charset=utf-8")
	h.Set("ETag", etag)
	if status == http.StatusOK {
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
