package status

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"html/template"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Handler serves the page as HTML (/status), JSON (/status.json) and an
// Atom feed (/status/feed.atom). Unauthenticated and cacheable: one
// computed page is shared by every request for TTL, and responses carry
// Cache-Control and an ETag, so a flood of visitors during an incident
// costs the database one query every TTL per replica.
type Handler struct {
	Pool    *pgxpool.Pool
	Options Options
	// PublicURL is where the page is reached (TASKIEM_PUBLIC_URL); feed
	// links are built from it, never from the request's Host.
	PublicURL string
	// TTL is how long a computed page is reused; default 15s. MaxAge is
	// what browsers and CDNs are told; default 30s.
	TTL, MaxAge time.Duration
	Logger      *slog.Logger
	// Now is the clock; nil uses time.Now.
	Now func() time.Time

	mu     sync.Mutex
	page   *Page
	loaded time.Time
}

func (h *Handler) now() time.Time {
	if h.Now != nil {
		return h.Now()
	}
	return time.Now()
}

// current returns the cached page, reloading it after TTL. When the
// database cannot be read it serves the last page it has, marked stale.
func (h *Handler) current(ctx context.Context) (*Page, bool, error) {
	ttl := h.TTL
	if ttl <= 0 {
		ttl = 15 * time.Second
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	now := h.now()
	if h.page != nil && now.Sub(h.loaded) < ttl {
		return h.page, false, nil
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	p, err := Load(ctx, h.Pool, now, h.Options)
	if err != nil {
		if h.Logger != nil {
			h.Logger.Error("status page: cannot load", "err", err)
		}
		if h.page != nil {
			return h.page, true, nil
		}
		return nil, false, err
	}
	h.page, h.loaded = p, now
	return p, false, nil
}

// Invalidate drops the cached page (after an operator posts an update).
func (h *Handler) Invalidate() {
	h.mu.Lock()
	h.page = nil
	h.mu.Unlock()
}

func (h *Handler) write(w http.ResponseWriter, r *http.Request, contentType string, body []byte, stale bool) {
	maxAge := h.MaxAge
	if maxAge <= 0 {
		maxAge = 30 * time.Second
	}
	sum := sha256.Sum256(body)
	etag := `"` + hex.EncodeToString(sum[:12]) + `"`
	hdr := w.Header()
	hdr.Set("Cache-Control", fmt.Sprintf("public, max-age=%d, stale-while-revalidate=%d, stale-if-error=86400", int(maxAge.Seconds()), int(maxAge.Seconds())))
	hdr.Set("ETag", etag)
	hdr.Set("Vary", "Accept-Encoding")
	if stale {
		hdr.Set("Warning", `110 - "status page is stale: the database cannot be read"`)
	}
	if match := r.Header.Get("If-None-Match"); match != "" && strings.Contains(match, etag) {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	hdr.Set("Content-Type", contentType)
	_, _ = w.Write(body)
}

func unavailable(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "no-store")
	http.Error(w, "status unavailable", http.StatusServiceUnavailable)
}

// JSON serves the page as JSON. Any origin may read it (an external status
// site or a customer's dashboard); it carries no credentials.
func (h *Handler) JSON(w http.ResponseWriter, r *http.Request) {
	p, stale, err := h.current(r.Context())
	if err != nil {
		unavailable(w)
		return
	}
	body, err := json.Marshal(p)
	if err != nil {
		unavailable(w)
		return
	}
	w.Header().Set("Access-Control-Allow-Origin", "*")
	h.write(w, r, "application/json", body, stale)
}

// HTML serves the page itself: no script, no cookies, no external assets.
func (h *Handler) HTML(w http.ResponseWriter, r *http.Request) {
	p, stale, err := h.current(r.Context())
	if err != nil {
		unavailable(w)
		return
	}
	var b bytes.Buffer
	if err := pageTmpl.Execute(&b, struct {
		*Page
		Stale bool
		Feed  string
	}{p, stale, h.PublicURL + "/status/feed.atom"}); err != nil {
		unavailable(w)
		return
	}
	h.write(w, r, "text/html; charset=utf-8", b.Bytes(), stale)
}

// Atom serves every update of the listed incidents as a feed entry.
func (h *Handler) Atom(w http.ResponseWriter, r *http.Request) {
	p, stale, err := h.current(r.Context())
	if err != nil {
		unavailable(w)
		return
	}
	body, err := Feed(p, h.PublicURL)
	if err != nil {
		unavailable(w)
		return
	}
	w.Header().Set("Access-Control-Allow-Origin", "*")
	h.write(w, r, "application/atom+xml; charset=utf-8", body, stale)
}

type atomLink struct {
	Href string `xml:"href,attr"`
	Rel  string `xml:"rel,attr,omitempty"`
}

type atomText struct {
	Type string `xml:"type,attr,omitempty"`
	Body string `xml:",chardata"`
}

type atomEntry struct {
	ID      string    `xml:"id"`
	Title   string    `xml:"title"`
	Updated string    `xml:"updated"`
	Link    *atomLink `xml:"link,omitempty"`
	Content atomText  `xml:"content"`
}

type atomFeed struct {
	XMLName xml.Name `xml:"http://www.w3.org/2005/Atom feed"`
	ID      string   `xml:"id"`
	Title   string   `xml:"title"`
	Updated string   `xml:"updated"`
	Author  struct {
		Name string `xml:"name"`
	} `xml:"author"`
	Links   []atomLink  `xml:"link"`
	Entries []atomEntry `xml:"entry"`
}

// Feed renders the page's incidents as Atom: one entry per update, newest
// first, at most 50.
func Feed(p *Page, publicURL string) ([]byte, error) {
	f := atomFeed{ID: "urn:taskiem:status", Title: "Taskiem status", Updated: p.GeneratedAt.Format(time.RFC3339)}
	f.Author.Name = "Taskiem"
	if publicURL != "" {
		f.Links = []atomLink{{Href: publicURL + "/status"}, {Href: publicURL + "/status/feed.atom", Rel: "self"}}
	}
	var all []Incident
	all = append(append(append(all, p.Active...), p.Upcoming...), p.Recent...)
	var entries []atomEntry
	for _, i := range all {
		for _, u := range i.Updates {
			e := atomEntry{
				ID:      fmt.Sprintf("urn:taskiem:status:%s:%d", i.ID, u.seq),
				Title:   fmt.Sprintf("%s: %s", i.Title, label(u.Status)),
				Updated: u.At.UTC().Format(time.RFC3339),
				Content: atomText{Type: "text", Body: fmt.Sprintf("%s (%s). Affects: %s.", u.Message, label(u.Impact), strings.Join(i.Components, ", "))},
			}
			if publicURL != "" {
				e.Link = &atomLink{Href: publicURL + "/status#" + i.ID.String()}
			}
			entries = append(entries, e)
		}
	}
	// Newest first across incidents.
	for a := 1; a < len(entries); a++ {
		for b := a; b > 0 && entries[b].Updated > entries[b-1].Updated; b-- {
			entries[b], entries[b-1] = entries[b-1], entries[b]
		}
	}
	if len(entries) > 50 {
		entries = entries[:50]
	}
	f.Entries = entries
	if len(entries) > 0 && entries[0].Updated > f.Updated {
		f.Updated = entries[0].Updated
	}
	out, err := xml.MarshalIndent(f, "", "  ")
	if err != nil {
		return nil, err
	}
	return append([]byte(xml.Header), out...), nil
}

// label turns an identifier into words: partial_outage → Partial outage.
func label(s string) string {
	s = strings.ReplaceAll(s, "_", " ")
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

var pageTmpl = template.Must(template.New("status").Funcs(template.FuncMap{
	"label": label,
	"when":  func(t time.Time) string { return t.UTC().Format("2 Jan 2006 15:04 UTC") },
	"whenp": func(t *time.Time) string {
		if t == nil {
			return ""
		}
		return t.UTC().Format("2 Jan 2006 15:04 UTC")
	},
	"pct": func(f float64) string { return fmt.Sprintf("%.2f%%", f*100) },
}).Parse(`<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<meta http-equiv="refresh" content="60">
<title>Taskiem status</title>
{{if .Feed}}<link rel="alternate" type="application/atom+xml" title="Taskiem status" href="{{.Feed}}">{{end}}
<style>
:root{--bg:#fff;--fg:#1b1f24;--muted:#5b6470;--line:#e3e6ea;--ok:#1a7f37;--maint:#0969da;--warn:#9a6700;--bad:#cf222e}
@media (prefers-color-scheme:dark){:root{--bg:#0d1117;--fg:#e6edf3;--muted:#9198a1;--line:#30363d;--ok:#3fb950;--maint:#4493f8;--warn:#d29922;--bad:#f85149}}
body{margin:0;background:var(--bg);color:var(--fg);font:16px/1.5 system-ui,-apple-system,"Segoe UI",sans-serif}
main{max-width:760px;margin:0 auto;padding:24px 16px}
h1{font-size:1.5rem;margin:0 0 16px}h2{font-size:1.1rem;margin:32px 0 8px}
.banner{padding:16px;border-radius:8px;color:#fff;font-weight:600}
.operational{background:var(--ok)}.maintenance{background:var(--maint)}.degraded{background:var(--warn)}.partial_outage,.major_outage{background:var(--bad)}
ul{list-style:none;padding:0;margin:0}li.c{display:flex;justify-content:space-between;gap:16px;padding:12px 0;border-bottom:1px solid var(--line)}
.c small{display:block;color:var(--muted)}.s-operational{color:var(--ok)}.s-maintenance{color:var(--maint)}.s-degraded{color:var(--warn)}.s-partial_outage,.s-major_outage{color:var(--bad)}
article{border:1px solid var(--line);border-radius:8px;padding:12px 16px;margin:8px 0}article h3{margin:0;font-size:1rem}
.meta{color:var(--muted);font-size:.875rem}p{margin:4px 0 8px;white-space:pre-wrap}
footer{margin-top:32px;color:var(--muted);font-size:.875rem}
</style>
</head>
<body><main>
<h1>Taskiem status</h1>
<div class="banner {{.Status}}">{{.Summary}}</div>
{{if .Stale}}<p class="meta">This page could not be refreshed; it shows the last known state.</p>{{end}}
<h2>Components</h2>
<ul>{{range .Components}}<li class="c"><span>{{.Name}}{{if .Description}}<small>{{.Description}}</small>{{end}}</span><span class="s-{{.Status}}">{{label .Status}}{{if .Automatic}} <small>(automatic check)</small>{{end}}</span></li>{{end}}</ul>
{{if .Active}}<h2>Now</h2>{{range .Active}}{{template "incident" .}}{{end}}{{end}}
{{if .Upcoming}}<h2>Scheduled maintenance</h2>{{range .Upcoming}}{{template "incident" .}}{{end}}{{end}}
<h2>Past incidents</h2>
{{if .Recent}}{{range .Recent}}{{template "incident" .}}{{end}}{{else}}<p class="meta">No incidents in the last 14 days.</p>{{end}}
<footer>
{{with .Canary}}{{if .LastAt}}Automatic end-to-end check: last run {{whenp .LastAt}}, {{if .LastOK}}passed{{else}}failed{{end}}; {{pct .Passed24h}} passed in the last 24 hours.<br>{{end}}{{end}}
Updated {{when .GeneratedAt}}. Also as <a href="/status.json">JSON</a>{{if .Feed}} and an <a href="{{.Feed}}">Atom feed</a>{{end}}.
</footer>
</main></body></html>
{{define "incident"}}<article id="{{.ID}}"><h3>{{.Title}}</h3>
<div class="meta">{{label .Kind}} · {{label .Status}}{{if .StartsAt}} · {{whenp .StartsAt}} to {{whenp .EndsAt}}{{end}} · affects {{range $i, $c := .Components}}{{if $i}}, {{end}}{{$c}}{{end}}</div>
{{range .Updates}}<p><strong>{{label .Status}}</strong> <span class="meta">{{when .At}}</span><br>{{.Message}}</p>{{end}}</article>{{end}}`))
