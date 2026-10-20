package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/israel-duff/taskiem/engine/publicweb"
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
}

func publicConfigFromEnv() (publicConfig, error) {
	c := publicConfig{
		Enabled:  envBool("TASKIEM_PUBLIC_ENABLED", false),
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
	c := config{
		DSN:           os.Getenv("TASKIEM_DATABASE_URL"),
		MetricsListen: env("TASKIEM_METRICS_LISTEN", ":9090"),
		TrustProxy:    envBool("TASKIEM_TRUST_PROXY", false),
	}
	var err error
	if c.Public, err = publicConfigFromEnv(); err != nil {
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

// publicTask opens the public host's own pool and returns its server, the
// task that serves it, and the pool's close.
func publicTask(ctx context.Context, cfg config, log *slog.Logger) (*publicweb.Server, func(context.Context) error, func(), error) {
	pc, err := pgxpool.ParseConfig(cfg.Public.DSN)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("TASKIEM_PUBLIC_DATABASE_URL: %w", err)
	}
	pc.MaxConns = cfg.Public.PoolSize
	pc.AfterConnect = afterConnect(cfg.Public.Role)
	pool, err := pgxpool.NewWithConfig(ctx, pc)
	if err != nil {
		return nil, nil, nil, err
	}
	pctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := pool.Ping(pctx); err != nil {
		pool.Close()
		return nil, nil, nil, fmt.Errorf("public host database: %w", err)
	}
	if cfg.DSN != "" && sameLogin(cfg.Public.DSN, cfg.DSN) {
		log.Warn("the public host connects with the same database login as the other roles; give it its own login, a member of taskiem_public only (docs/public-host.md)")
	}
	srv := &publicweb.Server{Lookup: publicweb.DBLookup(pool), Origins: publicweb.DBOrigins(pool), Storefronts: publicweb.DBStorefronts(pool), Logger: log.With("server", "public"), TrustProxy: cfg.TrustProxy}
	return srv, httpTask("public", cfg.Public.Listen, srv.Handler(), log), pool.Close, nil
}

// servePublic runs `--role public`: the public host and an internal
// listener for metrics, health and the proxy's TLS check, with nothing of
// the engine.
func servePublic(ctx context.Context, cfg config, log *slog.Logger) error {
	srv, task, closePool, err := publicTask(ctx, cfg, log)
	if err != nil {
		return fmt.Errorf("serve: %w", err)
	}
	defer closePool()
	runCtx, stopRun, draining := drainOnShutdown(ctx, cfg, log)
	defer stopRun()

	internal := http.NewServeMux()
	internal.Handle("/metrics", promhttp.Handler())
	internal.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("ok")) })
	internal.HandleFunc("/readyz", func(w http.ResponseWriter, _ *http.Request) {
		if draining.Load() {
			http.Error(w, "draining", http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write([]byte("ok"))
	})
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
