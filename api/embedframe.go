package api

import (
	"context"
	"errors"
	"html/template"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/israel-duff/taskiem/engine/httpsec"
)

// The embedded builder's delivery (spec 13.4 step 3; docs/embedding.md):
// the JavaScript bundle partners load with a script tag, the frame page an
// iframe shows, and custom domains that serve both, and the embed API, on
// a partner's own host name.

// embedParentHeader names, on requests from the frame page, the partner
// page that framed it: the origin the frame received its token from (it
// checks that against the app's origins before using the token). The API
// believes it only from the platform's own origin, where no one else's
// script runs.
const embedParentHeader = "X-Taskiem-Embed-Parent"

// embedBundleCache is the bundle's cache policy: its URL carries only the
// major version, so caches revalidate within minutes and a fix reaches
// every partner's page quickly.
const embedBundleCache = "public, max-age=300, stale-while-revalidate=86400"

// embedPages mounts /embed: the bundle and the frame page.
func (s *Server) embedPages(r chi.Router) {
	r.Get("/v1/{file}", s.embedBundle)
	r.Get("/frame", s.embedFrame)       // on an app's custom domain
	r.Get("/{app}/frame", s.embedFrame) // on any host
}

// embedBundle serves the bundle (taskiem.js) from EmbedDir. It is public:
// anyone may load it, from any page, and it holds nothing secret.
func (s *Server) embedBundle(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "file")
	if s.EmbedDir == "" || name != "taskiem.js" {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	f, err := os.Open(filepath.Join(s.EmbedDir, name)) //nolint:gosec // a fixed file name inside the configured directory
	if err != nil {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	defer func() { _ = f.Close() }()
	fi, err := f.Stat()
	if err != nil {
		s.fail(w, r, err)
		return
	}
	h := w.Header()
	h.Set("Content-Type", "text/javascript; charset=utf-8")
	h.Set("Cache-Control", embedBundleCache)
	// Partners' pages load it cross-origin, some with crossorigin and
	// integrity attributes, some under Cross-Origin-Embedder-Policy.
	h.Set("Access-Control-Allow-Origin", "*")
	h.Set("Cross-Origin-Resource-Policy", "cross-origin")
	http.ServeContent(w, r, name, fi.ModTime(), f)
}

var frameTemplate = template.Must(template.New("frame").Parse(`<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Automations</title>
<script src="/embed/v1/taskiem.js" defer></script>
<style>html,body{margin:0;height:100%}</style>
</head>
<body>
<taskiem-frame app="{{.App}}" view="{{.View}}" parent-origins="{{.Origins}}"></taskiem-frame>
</body>
</html>
`))

// embedFrame serves the frame page: the builder, or with ?view=runs the
// run list, for one app. It is the only page that may be framed, and only
// by the app's allowed origins; the token reaches it by postMessage from
// one of them, never in the URL.
func (s *Server) embedFrame(w http.ResponseWriter, r *http.Request) {
	app, ok := hostAppFrom(r.Context())
	if p := chi.URLParam(r, "app"); p != "" {
		id, err := uuid.Parse(p)
		if err != nil || (ok && id != app) {
			writeErr(w, http.StatusNotFound, "not found")
			return
		}
		app, ok = id, true
	}
	if !ok {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	var origins []string
	if err := s.Store.Pool.QueryRow(r.Context(), `SELECT taskiem_embed_app_origins($1)`, app).Scan(&origins); err != nil || origins == nil {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	// The page's own origin may not frame it: only partners' pages do.
	self := s.selfOrigin(r)
	ancestors := make([]string, 0, len(origins))
	for _, o := range origins {
		if o != self {
			ancestors = append(ancestors, o)
		}
	}
	view := "builder"
	if r.URL.Query().Get("view") == "runs" {
		view = "runs"
	}
	h := w.Header()
	httpsec.SetFrame(h, ancestors)
	h.Set("Content-Type", "text/html; charset=utf-8")
	h.Set("Cache-Control", "no-cache")
	h.Add("Vary", "Host")
	_ = frameTemplate.Execute(w, map[string]string{"App": app.String(), "View": view, "Origins": strings.Join(ancestors, " ")})
}

// selfOrigin is this request's own origin as the browser sees it: the
// public URL's for the platform's host, otherwise the Host it was sent to
// (https unless the connection is plain and no trusted proxy says so).
func (s *Server) selfOrigin(r *http.Request) string {
	host := strings.ToLower(r.Host)
	if s.PublicURL != "" {
		if u, err := url.Parse(s.PublicURL); err == nil && strings.EqualFold(u.Host, host) {
			return strings.ToLower(u.Scheme) + "://" + host
		}
	}
	scheme := "http"
	if r.TLS != nil || (s.TrustProxy && strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")) {
		scheme = "https"
	}
	if _, ok := hostAppFrom(r.Context()); ok {
		scheme = "https" // custom domains are served over https only
	}
	return scheme + "://" + host
}

// --- custom domains: Host to app ---

type hostAppKey struct{}

func hostAppFrom(ctx context.Context) (uuid.UUID, bool) {
	id, ok := ctx.Value(hostAppKey{}).(uuid.UUID)
	return id, ok
}

type hostEntry struct {
	app uuid.UUID // uuid.Nil: not a verified custom domain
	at  time.Time
}

// hostCacheTTL is how long a Host's mapping is reused. Verifying or
// removing a domain clears this replica's entry at once; others see it
// within the TTL.
const hostCacheTTL = 30 * time.Second

type hostCache struct {
	mu sync.Mutex
	m  map[string]hostEntry
}

func (c *hostCache) get(host string) (uuid.UUID, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.m[host]
	if !ok || time.Since(e.at) > hostCacheTTL {
		return uuid.Nil, false
	}
	return e.app, true
}

func (c *hostCache) put(host string, app uuid.UUID) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.m == nil || len(c.m) > 10000 {
		c.m = map[string]hostEntry{}
	}
	c.m[host] = hostEntry{app: app, at: time.Now()}
}

func (c *hostCache) forget(host string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.m, host)
}

// platformHost reports whether host is the platform's own (the public URL's
// host, an address, or localhost): it is never a custom domain.
func (s *Server) platformHost(host string) bool {
	if host == "" || host == "localhost" || net.ParseIP(host) != nil {
		return true
	}
	if s.PublicURL != "" {
		if u, err := url.Parse(s.PublicURL); err == nil && strings.EqualFold(u.Hostname(), host) {
			return true
		}
	}
	return false
}

// hostApp is the app a verified custom domain serves, if host is one.
func (s *Server) hostApp(ctx context.Context, host string) (uuid.UUID, error) {
	if app, ok := s.hosts.get(host); ok {
		return app, nil
	}
	var app *uuid.UUID
	if err := s.Store.Pool.QueryRow(ctx, `SELECT taskiem_embed_host_app($1)`, host).Scan(&app); err != nil {
		return uuid.Nil, err
	}
	id := uuid.Nil
	if app != nil {
		id = *app
	}
	s.hosts.put(host, id)
	return id, nil
}

// customDomains routes requests for an embed app's verified custom domain.
// Such a host serves the embed surface alone: the bundle, the app's frame
// page and its embed API. Everything else (the console, sign-in, the rest
// of the API) answers 404 there, so a partner's domain can never present
// the platform's own pages. Hosts that are not verified custom domains are
// served as before.
func (s *Server) customDomains(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host := strings.ToLower(r.Host)
		if h, _, err := net.SplitHostPort(host); err == nil {
			host = h
		}
		if s.platformHost(host) {
			next.ServeHTTP(w, r)
			return
		}
		app, err := s.hostApp(r.Context(), host)
		if err != nil && !errors.Is(err, context.Canceled) {
			s.Logger.Error("custom domain lookup", "host", host, "err", err)
			writeErr(w, http.StatusServiceUnavailable, "try again")
			return
		}
		if app == uuid.Nil {
			next.ServeHTTP(w, r)
			return
		}
		p := r.URL.Path
		switch {
		case p == "/healthz" || p == "/readyz",
			strings.HasPrefix(p, "/embed/"),
			strings.HasPrefix(p, "/v1/embed/"+app.String()+"/"):
			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), hostAppKey{}, app)))
		default:
			writeErr(w, http.StatusNotFound, "not found")
		}
	})
}
