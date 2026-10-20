// Package publicweb is the public host: the server that renders storefronts
// and hosted forms for anyone on the internet (decisions 0036 and 0037,
// docs/spec/storefronts.md 8.3, 8.4 and 8.7).
//
// It runs as the `public` role, apart from the API and the edge, and reads
// the database only as taskiem_public through narrow functions. Every
// response carries the public security headers and no cookie; pages are
// rendered on the server and work without JavaScript; hosts are resolved
// through a bounded cache, so page views reach the database rarely and
// never the primary's tenant tables.
package publicweb

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/israel-duff/taskiem/engine/formguard"
	"github.com/israel-duff/taskiem/engine/httpsec"
	"github.com/israel-duff/taskiem/engine/lru"
)

// Site is what a host serves.
type Site struct {
	Tenant    uuid.UUID
	Kind      string // storefront or form
	ID        uuid.UUID
	Suspended bool
}

// Lookup resolves a lower-case host name to its site; ok is false for a
// host that is unknown or not yet verified.
type Lookup func(ctx context.Context, host string) (site Site, ok bool, err error)

// DBLookup resolves hosts with taskiem_public_host, on a pool connected as
// taskiem_public.
func DBLookup(pool *pgxpool.Pool) Lookup {
	return func(ctx context.Context, host string) (Site, bool, error) {
		var s Site
		var status string
		err := pool.QueryRow(ctx, `SELECT tenant_id, kind, site_id, status FROM taskiem_public_host($1)`, host).
			Scan(&s.Tenant, &s.Kind, &s.ID, &status)
		if errors.Is(err, pgx.ErrNoRows) {
			return Site{}, false, nil
		}
		if err != nil {
			return Site{}, false, err
		}
		s.Suspended = status != "active"
		return s, true, nil
	}
}

// Origins returns the verified websites that may frame a tenant's /embed/
// pages.
type Origins func(ctx context.Context, tenant uuid.UUID) ([]string, error)

// DBOrigins reads them with taskiem_public_embed_origins.
func DBOrigins(pool *pgxpool.Pool) Origins {
	return func(ctx context.Context, tenant uuid.UUID) ([]string, error) {
		var out []string
		err := pool.QueryRow(ctx, `SELECT taskiem_public_embed_origins($1)`, tenant).Scan(&out)
		return out, err
	}
}

// Cache sizes and lifetimes. Hosts are cached for a short while, so a
// suspension or a newly verified domain takes effect within HostTTL on
// every replica without any signalling between them; unknown hosts are
// cached too, more briefly, so a flood of made-up names costs one lookup
// each per MissTTL, and the bound keeps it from growing memory (S34).
const (
	HostCacheSize = 10000
	HostTTL       = 30 * time.Second
	MissTTL       = 10 * time.Second
	// ViewLimiterSize bounds the addresses tracked for page-view limits.
	ViewLimiterSize = 50000
)

type hostEntry struct {
	site Site
	ok   bool
	at   time.Time
}

// Server serves public pages.
type Server struct {
	Lookup Lookup
	Logger *slog.Logger
	// TrustProxy takes the client address from the last X-Forwarded-For
	// entry, as the API does: set it only behind a proxy that appends it.
	TrustProxy bool
	// Views limits page views per client address; nil uses
	// formguard.PageViewsPerIP. Generous on purpose: a mobile network puts
	// many phones behind one address (storefronts 7.5).
	Views *formguard.Limiter
	// Origins are the sites allowed to frame /embed/ pages; nil allows
	// none.
	Origins Origins
	// Storefronts read storefront pages; nil serves "Coming soon".
	Storefronts Storefronts

	hosts   *lru.Cache[string, hostEntry]
	origins *lru.Cache[uuid.UUID, []string]
	pages   *lru.Cache[uuid.UUID, pageEntry]
	now     func() time.Time
}

// Handler is the public listener's handler.
func (s *Server) Handler() http.Handler {
	if s.hosts == nil {
		s.hosts = lru.New[string, hostEntry](HostCacheSize, HostTTL)
	}
	if s.origins == nil {
		// As hosts: a site unverified stops framing within HostTTL.
		s.origins = lru.New[uuid.UUID, []string](HostCacheSize, HostTTL)
	}
	if s.now == nil {
		s.now = time.Now
	}
	if s.Logger == nil {
		s.Logger = slog.Default()
	}
	if s.Views == nil {
		b := formguard.PageViewsPerIP
		s.Views = formguard.NewLimiter(b.Rate, b.Burst, ViewLimiterSize, 10*time.Minute)
	}
	return s.headers(s.noCookies(http.HandlerFunc(s.serve)))
}

func (s *Server) headers(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		httpsec.SetPublic(w.Header())
		next.ServeHTTP(w, r)
	})
}

// noCookies drops any cookie a handler tries to set: a public page has
// nothing to remember about its visitor, and a cookie on a business's
// domain is one more thing a mistake could leak (storefronts 8.3).
func (s *Server) noCookies(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(&cookieless{ResponseWriter: w, log: s.Logger}, r)
	})
}

type cookieless struct {
	http.ResponseWriter
	log     *slog.Logger
	written bool
}

func (c *cookieless) WriteHeader(code int) {
	if !c.written {
		c.written = true
		if c.Header().Get("Set-Cookie") != "" {
			c.Header().Del("Set-Cookie")
			c.log.Error("public host: a handler set a cookie; dropped")
		}
	}
	c.ResponseWriter.WriteHeader(code)
}

func (c *cookieless) Write(b []byte) (int, error) {
	if !c.written {
		c.WriteHeader(http.StatusOK)
	}
	return c.ResponseWriter.Write(b)
}

func (s *Server) serve(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.URL.Path, staticPrefix) {
		serveStatic(w, r)
		return
	}
	// Static files are immutable and cheap, so only pages are counted.
	if !s.Views.Allow(s.ClientIP(r)) {
		w.Header().Set("Retry-After", "60")
		s.page(w, r, http.StatusTooManyRequests, "unavailable", pageData{Title: "Too many requests"})
		return
	}
	host := hostOf(r)
	site, ok, err := s.site(r.Context(), host)
	if err != nil {
		if !errors.Is(err, context.Canceled) {
			s.Logger.Error("public host lookup", "host", host, "err", err)
		}
		s.page(w, r, http.StatusServiceUnavailable, "unavailable", pageData{Title: "Try again shortly"})
		return
	}
	switch {
	case !ok:
		s.page(w, r, http.StatusNotFound, "notfound", pageData{Title: "Page not found"})
	case site.Suspended:
		s.page(w, r, http.StatusGone, "unavailable", pageData{Title: "This page is unavailable"})
	case r.Method != http.MethodGet && r.Method != http.MethodHead:
		w.Header().Set("Allow", "GET, HEAD")
		s.page(w, r, http.StatusMethodNotAllowed, "notfound", pageData{Title: "Page not found"})
	case r.URL.Path == "/embed" || strings.HasPrefix(r.URL.Path, "/embed/"):
		// The one kind of page another site may frame, and only the
		// organisation's verified websites. The views themselves arrive
		// with S6.
		origins, err := s.embedOrigins(r.Context(), site.Tenant)
		if err != nil {
			s.Logger.Error("public host: embed origins", "tenant", site.Tenant, "err", err)
			origins = nil // fail closed: nobody may frame it
		}
		httpsec.SetPublicEmbed(w.Header(), origins)
		s.page(w, r, http.StatusOK, "embedsetup", pageData{Title: "Coming soon"})
	case r.URL.Path != "/":
		s.page(w, r, http.StatusNotFound, "notfound", pageData{Title: "Page not found"})
	case site.Kind == "storefront":
		s.serveStorefront(w, r, site)
	default:
		// Form pages arrive with S7; until then a verified host says it is
		// being set up.
		s.page(w, r, http.StatusOK, "setup", pageData{Title: "Coming soon"})
	}
}

// site resolves host through the cache.
func (s *Server) site(ctx context.Context, host string) (Site, bool, error) {
	if e, hit := s.hosts.Get(host); hit && (e.ok || s.now().Sub(e.at) < MissTTL) {
		return e.site, e.ok, nil
	}
	site, ok, err := s.Lookup(ctx, host)
	if err != nil {
		return Site{}, false, err
	}
	s.hosts.Put(host, hostEntry{site: site, ok: ok, at: s.now()})
	return site, ok, nil
}

func (s *Server) embedOrigins(ctx context.Context, tenant uuid.UUID) ([]string, error) {
	if s.Origins == nil {
		return nil, nil
	}
	if o, ok := s.origins.Get(tenant); ok {
		return o, nil
	}
	o, err := s.Origins(ctx, tenant)
	if err != nil {
		return nil, err
	}
	s.origins.Put(tenant, o)
	return o, nil
}

// TLSAsk answers a proxy's on-demand TLS check (GET ?domain=<host>): 200
// for a host that serves, so the proxy may obtain its certificate, 404
// otherwise, so nobody can make the platform request certificates for
// names it does not serve. Mount it on an internal listener only.
func (s *Server) TLSAsk(w http.ResponseWriter, r *http.Request) {
	host := strings.ToLower(strings.TrimSuffix(r.URL.Query().Get("domain"), "."))
	if host == "" {
		http.Error(w, "domain is required", http.StatusBadRequest)
		return
	}
	_, ok, err := s.Lookup(r.Context(), host)
	switch {
	case err != nil:
		s.Logger.Error("public host: tls ask", "host", host, "err", err)
		http.Error(w, "try again", http.StatusServiceUnavailable)
	case !ok:
		http.Error(w, "unknown host", http.StatusNotFound)
	default:
		_, _ = w.Write([]byte("ok"))
	}
}

func hostOf(r *http.Request) string {
	host := strings.ToLower(r.Host)
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	return strings.TrimSuffix(host, ".")
}

// ClientIP is the request's client address: the last X-Forwarded-For
// entry when the server trusts its proxy, the peer address otherwise.
func (s *Server) ClientIP(r *http.Request) string {
	if s.TrustProxy {
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			hops := strings.Split(xff, ",")
			if ip := net.ParseIP(strings.TrimSpace(hops[len(hops)-1])); ip != nil {
				return ip.String()
			}
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
