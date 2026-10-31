package main

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/netip"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/israel-duff/taskiem/engine/forms"
	"github.com/israel-duff/taskiem/engine/publicweb"
	"github.com/israel-duff/taskiem/engine/storefront"
)

// publicConfig is the public host's (docs/public-host.md): the server for
// storefronts and hosted forms, apart from the API and the edge.
type publicConfig struct {
	// Enabled runs the public host inside `--role all` too
	// (TASKIEM_PUBLIC_ENABLED). `--role public` always runs it.
	Enabled bool
	// Listen is the public listener (TASKIEM_PUBLIC_LISTEN, :8082).
	Listen string
	// DSN is the database the public host connects to
	// (TASKIEM_PUBLIC_DATABASE_URL, default TASKIEM_DATABASE_URL). In
	// production it names a login that is a member of taskiem_public only.
	DSN string
	// Role is set on every connection (TASKIEM_PUBLIC_DATABASE_ROLE,
	// taskiem_public).
	Role string
	// Domains are the public host's own domains: the preview domain and
	// the form host (TASKIEM_PUBLIC_DOMAINS, comma-separated). They can
	// never be added as an embed site.
	Domains []string
	// PoolSize caps its connections (TASKIEM_PUBLIC_DATABASE_POOL, 5), so
	// a flood of page views cannot exhaust Postgres for everything else.
	PoolSize int32
	// Key is the public host's secret (TASKIEM_PUBLIC_KEY, 32 bytes in
	// base64): form tokens are signed, and submissions sealed for the
	// inbox, with keys derived from it. Unset, storefront forms answer
	// "orders are not open yet".
	Key []byte
	// FormHost is the form host's name (TASKIEM_PUBLIC_FORM_HOST, default
	// forms.<first of Domains>): hosted forms are served there.
	FormHost string
	// AppURL is the web app's (TASKIEM_PUBLIC_URL), where members' forms
	// open.
	AppURL string
	// CNAME is the name businesses point their own subdomains at
	// (TASKIEM_PUBLIC_CNAME, default pages.<first of Domains>, which the
	// preview domain's wildcard record covers).
	CNAME string
	// Addresses are the public host's IPv4 and IPv6 addresses
	// (TASKIEM_PUBLIC_ADDRESSES, comma-separated), for businesses'
	// registrable domains, which cannot be a CNAME. Unset, owners are told
	// to use their DNS provider's ALIAS or CNAME flattening.
	Addresses []string
}

func publicConfigFromEnv() (publicConfig, error) {
	enabled, err := envBool("TASKIEM_PUBLIC_ENABLED", false)
	if err != nil {
		return publicConfig{}, err
	}
	c := publicConfig{
		Enabled:  enabled,
		Listen:   env("TASKIEM_PUBLIC_LISTEN", ":8082"),
		DSN:      os.Getenv("TASKIEM_PUBLIC_DATABASE_URL"),
		Role:     env("TASKIEM_PUBLIC_DATABASE_ROLE", "taskiem_public"),
		PoolSize: 5,
	}
	for _, d := range strings.Split(os.Getenv("TASKIEM_PUBLIC_DOMAINS"), ",") {
		if d = strings.ToLower(strings.TrimSpace(d)); d != "" {
			c.Domains = append(c.Domains, d)
		}
	}
	if v := strings.TrimSpace(os.Getenv("TASKIEM_PUBLIC_KEY")); v != "" {
		k, err := base64.StdEncoding.DecodeString(v)
		if err != nil {
			k, err = base64.RawURLEncoding.DecodeString(strings.TrimRight(v, "="))
		}
		if err != nil || len(k) != 32 {
			return c, errors.New("TASKIEM_PUBLIC_KEY: want 32 bytes in base64 (openssl rand -base64 32)")
		}
		c.Key = k
	}
	c.FormHost = strings.ToLower(strings.TrimSpace(os.Getenv("TASKIEM_PUBLIC_FORM_HOST")))
	if c.FormHost == "" && len(c.Domains) > 0 {
		c.FormHost = "forms." + c.Domains[0]
	}
	c.CNAME = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(os.Getenv("TASKIEM_PUBLIC_CNAME"))), ".")
	if c.CNAME == "" && len(c.Domains) > 0 {
		c.CNAME = "pages." + c.Domains[0]
	}
	for _, a := range strings.Split(os.Getenv("TASKIEM_PUBLIC_ADDRESSES"), ",") {
		if a = strings.TrimSpace(a); a == "" {
			continue
		}
		ip, err := netip.ParseAddr(a)
		if err != nil {
			return c, fmt.Errorf("TASKIEM_PUBLIC_ADDRESSES: %q is not an IP address", a)
		}
		c.Addresses = append(c.Addresses, ip.String())
	}
	c.AppURL = strings.TrimRight(strings.TrimSpace(os.Getenv("TASKIEM_PUBLIC_URL")), "/")
	if c.DSN == "" { // unset or empty: the platform's
		c.DSN = os.Getenv("TASKIEM_DATABASE_URL")
	}
	if v := os.Getenv("TASKIEM_PUBLIC_DATABASE_POOL"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 100 {
			return c, fmt.Errorf("TASKIEM_PUBLIC_DATABASE_POOL: %q is not a number from 1 to 100", v)
		}
		c.PoolSize = int32(n) //nolint:gosec // bounded above
	}
	return c, nil
}

// loadPublicConfig is the configuration `--role public` needs, and no
// more: it does not require TASKIEM_DATABASE_URL, the KMS or any other
// secret of the platform, so the public host's pods can be given only
// their own database login.
func loadPublicConfig() (config, error) {
	trustProxy, err := envBool("TASKIEM_TRUST_PROXY", false)
	if err != nil {
		return config{}, err
	}
	c := config{
		DSN:           os.Getenv("TASKIEM_DATABASE_URL"),
		MetricsListen: env("TASKIEM_METRICS_LISTEN", ":9090"),
		TrustProxy:    trustProxy,
	}
	if c.Public, err = publicConfigFromEnv(); err != nil {
		return c, err
	}
	if c.Mail, err = mailConfigFromEnv(); err != nil {
		return c, err
	}
	if c.Public.DSN == "" {
		return c, errors.New("TASKIEM_PUBLIC_DATABASE_URL (or TASKIEM_DATABASE_URL) is required")
	}
	if c.Shutdown, err = shutdownConfig(); err != nil {
		return c, err
	}
	return c, nil
}

// publicPool opens a pool on the public host's own login (as
// taskiem_public), which the mail role shares.
func publicPool(ctx context.Context, cfg config, log *slog.Logger) (*pgxpool.Pool, error) {
	pc, err := pgxpool.ParseConfig(cfg.Public.DSN)
	if err != nil {
		return nil, fmt.Errorf("TASKIEM_PUBLIC_DATABASE_URL: %w", err)
	}
	pc.MaxConns = cfg.Public.PoolSize
	pc.AfterConnect = afterConnect(cfg.Public.Role)
	pool, err := pgxpool.NewWithConfig(ctx, pc)
	if err != nil {
		return nil, err
	}
	pctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := pool.Ping(pctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("public host database: %w", err)
	}
	if cfg.DSN != "" && sameLogin(cfg.Public.DSN, cfg.DSN) {
		log.Warn("the public host connects with the same database login as the other roles; give it its own login, a member of taskiem_public only (docs/public-host.md)")
	}
	return pool, nil
}

// publicTask opens the public host's own pool and returns its server, the
// task that serves it, and the pool, which the caller closes.
func publicTask(ctx context.Context, cfg config, log *slog.Logger) (*publicweb.Server, func(context.Context) error, *pgxpool.Pool, error) {
	pool, err := publicPool(ctx, cfg, log)
	if err != nil {
		return nil, nil, nil, err
	}
	srv := &publicweb.Server{Lookup: publicweb.DBLookup(pool), Origins: publicweb.DBOrigins(pool), Storefronts: publicweb.DBStorefronts(pool), Reports: publicweb.DBReports(pool), Brands: publicweb.DBBrands(pool),
		Submit: publicweb.DBSubmit(pool), OrderStatus: publicweb.DBOrderStatus(pool),
		Services: publicweb.DBServices(pool), Busy: publicweb.DBBusy(pool), Book: publicweb.DBBook(pool), Booking: publicweb.DBBookingStatus(pool),
		Change: publicweb.DBChange(pool), PriceChange: publicweb.DBPriceChange(pool),
		Forms: publicweb.DBForms(pool), Invoices: publicweb.DBInvoices(pool), InvoicePay: publicweb.DBInvoicePay{Pool: pool}, Customers: publicweb.DBCustomers(pool), CustomerPay: publicweb.DBCustomerPay{Pool: pool}, OrderPay: publicweb.DBOrderPay{Pool: pool}, OrderRate: publicweb.DBOrderRate{Pool: pool}, Receipts: publicweb.DBReceipts(pool), Families: publicweb.DBFamilies{Pool: pool}, Reach: publicweb.DBReach(pool), FormHost: cfg.Public.FormHost, AppURL: cfg.Public.AppURL,
		Logger: log.With("server", "public"), TrustProxy: cfg.TrustProxy}
	if len(cfg.Public.Domains) > 0 {
		srv.PreviewDomain = cfg.Public.Domains[0]
	}
	if k := cfg.Public.Key; k != nil {
		if srv.FormKey, err = storefront.FormKey(k); err != nil {
			pool.Close()
			return nil, nil, nil, fmt.Errorf("TASKIEM_PUBLIC_KEY: %w", err)
		}
		if srv.InboxKey, err = storefront.InboxKey(k); err != nil {
			pool.Close()
			return nil, nil, nil, fmt.Errorf("TASKIEM_PUBLIC_KEY: %w", err)
		}
		fk, err := forms.NewKeys(k)
		if err != nil {
			pool.Close()
			return nil, nil, nil, fmt.Errorf("TASKIEM_PUBLIC_KEY: %w", err)
		}
		srv.FormKeys = &fk
	} else {
		log.Warn("TASKIEM_PUBLIC_KEY is not set: storefront forms answer \"orders are not open yet\" and hosted forms are not open (docs/public-host.md)")
	}
	if srv.FormHost == "" {
		log.Warn("no form host: set TASKIEM_PUBLIC_DOMAINS or TASKIEM_PUBLIC_FORM_HOST to serve hosted forms (docs/public-host.md)")
	}
	return srv, httpTask("public", cfg.Public.Listen, srv.Handler(), log), pool, nil
}

// servePublic runs `--role public`: the public host and an internal
// listener for metrics, health and the proxy's TLS check, with nothing of
// the engine.
func servePublic(ctx context.Context, cfg config, log *slog.Logger) error {
	srv, task, pool, err := publicTask(ctx, cfg, log)
	if err != nil {
		return fmt.Errorf("serve: %w", err)
	}
	defer pool.Close()
	runCtx, stopRun, draining := drainOnShutdown(ctx, cfg, log)
	defer stopRun()

	internal := http.NewServeMux()
	internal.Handle("/metrics", promhttp.Handler())
	internal.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("ok")) })
	// Not ready without its database: every page and form needs it.
	internal.HandleFunc("/readyz", readiness(draining, pool))
	internal.HandleFunc("/tls/ask", srv.TLSAsk)
	profiling(internal, log) // TASKIEM_PPROF (pprof.go)
	return runTasks(runCtx, stopRun, []func(context.Context) error{task, httpTask("metrics", cfg.MetricsListen, internal, log)}, log)
}

// sameLogin reports whether two DSNs log in as the same user.
func sameLogin(a, b string) bool {
	ca, err := pgx.ParseConfig(a)
	if err != nil {
		return false
	}
	cb, err := pgx.ParseConfig(b)
	if err != nil {
		return false
	}
	return ca.User == cb.User && ca.Host == cb.Host && ca.Database == cb.Database
}
