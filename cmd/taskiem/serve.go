package main

import (
	"context"
	"encoding/base64"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/israel-duff/taskiem/api"
	"github.com/israel-duff/taskiem/connectors/builtin"
	"github.com/israel-duff/taskiem/engine/alerts"
	"github.com/israel-duff/taskiem/engine/audit"
	"github.com/israel-duff/taskiem/engine/connector"
	"github.com/israel-duff/taskiem/engine/egress"
	"github.com/israel-duff/taskiem/engine/ingest"
	"github.com/israel-duff/taskiem/engine/pii"
	"github.com/israel-duff/taskiem/engine/runtime"
	"github.com/israel-duff/taskiem/engine/sandbox"
	"github.com/israel-duff/taskiem/engine/secrets"
	"github.com/israel-duff/taskiem/engine/telemetry"
	"github.com/israel-duff/taskiem/engine/wasmconn"
	"github.com/israel-duff/taskiem/engine/webauthn"
)

var roles = map[string]bool{"api": true, "edge": true, "orchestrator": true, "scheduler": true, "worker": true, "all": true}

// config is read from the environment (twelve-factor); see docs/operations.md.
type config struct {
	DSN, DBRole                       string
	Listen, EdgeListen, MetricsListen string
	KMS, LocalKey                     string
	OpenBaoAddr, OpenBaoToken, KMSKey string
	ArchiveDir, WebDir                string
	AnchorKey, AnchorDir              string
	SecureCookies, TrustProxy, Signup bool
	// PublicURL is where people reach the web app; passkeys are bound to it.
	PublicURL, PasskeyRPID string
	RequireAdminPasskeys   bool
	// SMTPURL and AlertFrom let alerts go out by email.
	SMTPURL, AlertFrom string
	Queues             []string
	Connectors         builtin.Options
	PoolSize           int32
	LoginBurst         int
}

func env(k, def string) string {
	if v, ok := os.LookupEnv(k); ok {
		return v
	}
	return def
}

func envBool(k string, def bool) bool {
	v, ok := os.LookupEnv(k)
	if !ok {
		return def
	}
	b, err := strconv.ParseBool(v)
	return err == nil && b
}

func loadConfig() (config, error) {
	c := config{
		DSN:           os.Getenv("TASKIEM_DATABASE_URL"),
		DBRole:        env("TASKIEM_DATABASE_ROLE", "taskiem_app"),
		Listen:        env("TASKIEM_LISTEN", ":8080"),
		EdgeListen:    env("TASKIEM_EDGE_LISTEN", ":8081"),
		MetricsListen: env("TASKIEM_METRICS_LISTEN", ":9090"),
		KMS:           env("TASKIEM_KMS", "local"),
		LocalKey:      os.Getenv("TASKIEM_LOCAL_KMS_KEY"),
		OpenBaoAddr:   os.Getenv("TASKIEM_OPENBAO_ADDR"),
		OpenBaoToken:  os.Getenv("TASKIEM_OPENBAO_TOKEN"),
		KMSKey:        env("TASKIEM_KMS_KEY", "taskiem"),
		ArchiveDir:    os.Getenv("TASKIEM_ARCHIVE_DIR"),
		PublicURL:     strings.TrimRight(os.Getenv("TASKIEM_PUBLIC_URL"), "/"),
		PasskeyRPID:   os.Getenv("TASKIEM_PASSKEY_RP_ID"),
		AnchorKey:     os.Getenv("TASKIEM_ANCHOR_KEY"),
		AnchorDir:     os.Getenv("TASKIEM_ANCHOR_DIR"),
		SMTPURL:       os.Getenv("TASKIEM_SMTP_URL"),
		AlertFrom:     os.Getenv("TASKIEM_ALERT_FROM"),
		WebDir:        os.Getenv("TASKIEM_WEB_DIR"),
		SecureCookies: envBool("TASKIEM_SECURE_COOKIES", true),
		TrustProxy:    envBool("TASKIEM_TRUST_PROXY", false),
		Signup:        envBool("TASKIEM_ALLOW_SIGNUP", false),
		Queues:        strings.Split(env("TASKIEM_WORKER_QUEUES", "connector,sandbox"), ","),
		Connectors: builtin.Options{
			PaystackURL: os.Getenv("TASKIEM_PAYSTACK_URL"),
			TermiiURL:   os.Getenv("TASKIEM_TERMII_URL"),
			DojahURL:    os.Getenv("TASKIEM_DOJAH_URL"),
			IswalletURL: os.Getenv("TASKIEM_ISWALLET_URL"),
			// Sandboxes and tests; Anchor and Lenco connections also pick
			// their provider's sandbox with environment=sandbox.
			FlutterwaveURL: os.Getenv("TASKIEM_FLUTTERWAVE_URL"),
			AnchorURL:      os.Getenv("TASKIEM_GETANCHOR_URL"), // not TASKIEM_ANCHOR_*: those are audit anchors
			LencoURL:       os.Getenv("TASKIEM_LENCO_URL"),
			BreetURL:       os.Getenv("TASKIEM_BREET_URL"),
			MoniepointURL:  os.Getenv("TASKIEM_MONIEPOINT_URL"),
			InterswitchURL: os.Getenv("TASKIEM_INTERSWITCH_URL"),
			S3URL:          os.Getenv("TASKIEM_S3_URL"),
		},
		PoolSize: 20,
	}
	// Administrators are held to passkeys wherever passkeys can work.
	c.RequireAdminPasskeys = envBool("TASKIEM_REQUIRE_ADMIN_PASSKEYS", c.PublicURL != "")
	if n, err := strconv.Atoi(os.Getenv("TASKIEM_LOGIN_BURST")); err == nil && n > 0 {
		c.LoginBurst = n
	}
	if n, err := strconv.Atoi(os.Getenv("TASKIEM_DATABASE_POOL")); err == nil && n > 0 {
		c.PoolSize = int32(n) //nolint:gosec // small operator-set value
	}
	if c.DSN == "" {
		return c, errors.New("TASKIEM_DATABASE_URL is required")
	}
	return c, nil
}

func (c config) kms() (secrets.KMS, error) {
	switch c.KMS {
	case "local":
		key, err := base64.StdEncoding.DecodeString(c.LocalKey)
		if err != nil || len(key) != 32 {
			return nil, errors.New("TASKIEM_LOCAL_KMS_KEY must be 32 bytes, base64 (openssl rand -base64 32); use TASKIEM_KMS=openbao in production")
		}
		return secrets.NewLocalKMS(map[string][]byte{c.KMSKey: key})
	case "openbao":
		if c.OpenBaoAddr == "" || c.OpenBaoToken == "" {
			return nil, errors.New("TASKIEM_OPENBAO_ADDR and TASKIEM_OPENBAO_TOKEN are required with TASKIEM_KMS=openbao")
		}
		return &secrets.OpenBaoTransit{Addr: c.OpenBaoAddr, Token: c.OpenBaoToken}, nil
	}
	return nil, fmt.Errorf("TASKIEM_KMS must be local or openbao, not %q", c.KMS)
}

func openPool(ctx context.Context, c config) (*pgxpool.Pool, error) {
	pc, err := pgxpool.ParseConfig(c.DSN)
	if err != nil {
		return nil, err
	}
	pc.MaxConns = c.PoolSize
	if c.DBRole != "" {
		role := pgx.Identifier{c.DBRole}.Sanitize() //nolint:misspell // pgx API
		pc.AfterConnect = func(ctx context.Context, conn *pgx.Conn) error {
			_, err := conn.Exec(ctx, "SET ROLE "+role)
			return err
		}
	}
	pool, err := pgxpool.NewWithConfig(ctx, pc)
	if err != nil {
		return nil, err
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("database: %w", err)
	}
	return pool, nil
}

// engine is everything the roles share.
type engine struct {
	cfg      config
	log      *slog.Logger
	pool     *pgxpool.Pool
	store    *runtime.Store
	vault    *secrets.Vault
	registry *connector.Registry
	// connectors loads tenants' own WebAssembly connectors.
	connectors *wasmconn.Source
}

func newEngine(ctx context.Context, cfg config, log *slog.Logger) (*engine, error) {
	kms, err := cfg.kms()
	if err != nil {
		return nil, err
	}
	pool, err := openPool(ctx, cfg)
	if err != nil {
		return nil, err
	}
	reg := connector.NewRegistry()
	if err := builtin.Register(reg, cfg.Connectors); err != nil {
		pool.Close()
		return nil, err
	}
	wrt, err := wasmconn.New(ctx, wasmconn.Limits{})
	if err != nil {
		pool.Close()
		return nil, err
	}
	small := cfg
	small.PoolSize = 2
	srcPool, err := openPool(ctx, small)
	if err != nil {
		pool.Close()
		return nil, err
	}
	src := &wasmconn.Source{Pool: srcPool, Runtime: wrt, Logger: log}
	reg.SetTenantSource(src.Connectors)
	vault := &secrets.Vault{Pool: pool, KMS: kms, RootKey: cfg.KMSKey}
	return &engine{cfg: cfg, log: log, pool: pool, registry: reg, vault: vault, connectors: src,
		store: &runtime.Store{Pool: pool, Registry: reg, PII: vault}}, nil
}

func (e *engine) hooks() *ingest.Handler {
	return &ingest.Handler{Store: e.store, Secrets: e.vault, Connections: e.vault, Registry: e.registry, Logger: e.log}
}

// serve runs one role, or all of them in one process, until ctx ends.
func serve(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	role := fs.String("role", env("TASKIEM_ROLE", "all"), "api, edge, orchestrator, scheduler, worker, or all")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if !roles[*role] {
		return fmt.Errorf("serve: unknown role %q", *role)
	}
	cfg, err := loadConfig()
	if err != nil {
		return fmt.Errorf("serve: %w", err)
	}
	log := slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{ReplaceAttr: redactAttr})).With("role", *role, "version", version)
	slog.SetDefault(log)
	shutdownTracing, err := telemetry.Setup(ctx, "taskiem", version)
	if err != nil {
		return err
	}
	defer func() { _ = shutdownTracing(context.Background()) }()
	e, err := newEngine(ctx, cfg, log)
	if err != nil {
		return fmt.Errorf("serve: %w", err)
	}
	defer e.pool.Close()
	defer e.connectors.Pool.Close()

	is := func(r string) bool { return *role == r || *role == "all" }
	alerter, err := cfg.alerter(e, log)
	if err != nil {
		return fmt.Errorf("serve: %w", err)
	}
	var tasks []func(context.Context) error
	if is("api") {
		srv := &api.Server{Store: e.store, Vault: e.vault, Registry: e.registry, Connectors: e.connectors, Logger: log,
			AllowSignup: cfg.Signup, SecureCookies: cfg.SecureCookies, TrustProxy: cfg.TrustProxy}
		if signer := cfg.anchorSigner(nil); signer != nil {
			srv.AnchorKey = signer.PublicKey()
		}
		rp, err := cfg.relyingParty()
		if err != nil {
			return err
		}
		srv.WebAuthn, srv.RequireAdminPasskeys = rp, cfg.RequireAdminPasskeys && rp.RPID != ""
		srv.PublicURL = cfg.PublicURL
		srv.Alerts = alerter
		srv.LoginBurst = cfg.LoginBurst
		// Checking a Python step compiles CPython first (seconds): do it now.
		go func() { _ = sandbox.InitPython() }()
		srv.Done = ctx.Done()
		if *role == "all" {
			srv.Ingest = e.hooks() // one listener for a small install
		}
		if cfg.WebDir != "" {
			srv.Static = api.SPA(cfg.WebDir)
		}
		tasks = append(tasks, httpTask("api", cfg.Listen, srv.Handler(), log), srv.RunGitSyncs)
	}
	if *role == "edge" {
		mux := http.NewServeMux()
		mux.Handle("/hooks/", http.StripPrefix("/hooks", e.hooks()))
		git := &api.Server{Store: e.store, Vault: e.vault, Registry: e.registry, Logger: log}
		mux.Handle("/git-hooks/", http.StripPrefix("/git-hooks", git.GitHooks()))
		mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("ok")) })
		tasks = append(tasks, httpTask("edge", cfg.EdgeListen, mux, log))
	}
	if is("scheduler") {
		s := &runtime.Scheduler{Store: e.store, Logger: log}
		if cfg.ArchiveDir != "" {
			s.Archiver = runtime.FileArchiver{Dir: cfg.ArchiveDir}
		} else {
			log.Warn("TASKIEM_ARCHIVE_DIR unset: runs past retention are kept, not purged")
		}
		cron := &ingest.Cron{Store: e.store, Logger: log}
		tasks = append(tasks, s.Run, cron.Run, alerter.Run)
		if signer := cfg.anchorSigner(log); signer != nil && cfg.AnchorDir != "" {
			a := &audit.Anchorer{Pool: e.pool, Signer: signer, Dir: cfg.AnchorDir, Logger: log}
			tasks = append(tasks, a.Run)
		} else {
			log.Warn("TASKIEM_ANCHOR_KEY or TASKIEM_ANCHOR_DIR unset: audit chain heads are not anchored outside the database")
		}
	}
	if *role == "orchestrator" {
		s := &runtime.Scheduler{Store: e.store, Logger: log, SweepOnly: true, Interval: 100 * time.Millisecond}
		tasks = append(tasks, s.Run)
	}
	if is("worker") {
		host, _ := os.Hostname()
		for _, q := range cfg.Queues {
			w := &runtime.Worker{Store: e.store, Registry: e.registry, Secrets: e.vault, Connections: e.vault,
				Egress: &egress.Guard{Logger: log}, ID: host + "/" + q + "/" + strconv.Itoa(os.Getpid()), Queue: strings.TrimSpace(q), Logger: log}
			tasks = append(tasks, w.Run)
		}
	}
	_ = prometheus.Register(telemetry.QueueCollector{Pool: e.pool}) // already registered when serve runs twice in one process (tests)
	tasks = append(tasks, httpTask("metrics", cfg.MetricsListen, promhttp.Handler(), log))

	log.Info("taskiem starting")
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var wg sync.WaitGroup
	errs := make(chan error, len(tasks))
	for _, t := range tasks {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := t(ctx); err != nil {
				errs <- err
				cancel() // one role failing stops the process; the supervisor restarts it
			}
		}()
	}
	wg.Wait()
	close(errs)
	log.Info("taskiem stopped")
	return errors.Join(collect(errs)...)
}

func collect(ch <-chan error) []error {
	var out []error
	for err := range ch {
		out = append(out, err)
	}
	return out
}

// httpTask serves h on addr until ctx ends, then shuts down gracefully.
func httpTask(name, addr string, h http.Handler, log *slog.Logger) func(context.Context) error {
	return func(ctx context.Context) error {
		ln, err := net.Listen("tcp", addr)
		if err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
		srv := &http.Server{Handler: h, ReadHeaderTimeout: 10 * time.Second, ReadTimeout: 60 * time.Second, WriteTimeout: 120 * time.Second, IdleTimeout: 120 * time.Second}
		log.Info("listening", "server", name, "addr", ln.Addr().String())
		done := make(chan error, 1)
		go func() { done <- srv.Serve(ln) }()
		select {
		case err := <-done:
			return fmt.Errorf("%s: %w", name, err)
		case <-ctx.Done():
		}
		sctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		return srv.Shutdown(sctx)
	}
}

// redactAttr masks personal data in every string and error the process
// logs (spec 9.3: logs are redacted like the UI).
func redactAttr(_ []string, a slog.Attr) slog.Attr {
	switch a.Value.Kind() {
	case slog.KindString:
		a.Value = slog.StringValue(pii.Redact(a.Value.String()))
	case slog.KindAny:
		if err, ok := a.Value.Any().(error); ok {
			a.Value = slog.StringValue(pii.Redact(err.Error()))
		}
	}
	return a
}

// anchorSigner is the audit anchoring key, from TASKIEM_ANCHOR_KEY (a
// base64 32-byte Ed25519 seed); nil when unset or invalid.
// alerter delivers alerts; email needs TASKIEM_SMTP_URL and
// TASKIEM_ALERT_FROM.
func (c config) alerter(e *engine, log *slog.Logger) (*alerts.Alerter, error) {
	a := &alerts.Alerter{Pool: e.pool, Secrets: e.vault, Egress: &egress.Guard{Logger: log}, PublicURL: c.PublicURL, From: c.AlertFrom, Logger: log}
	if c.SMTPURL != "" {
		m, err := alerts.NewSMTPMailer(c.SMTPURL)
		if err != nil {
			return nil, err
		}
		if c.AlertFrom == "" {
			return nil, errors.New("TASKIEM_SMTP_URL is set but TASKIEM_ALERT_FROM is not")
		}
		a.Mailer = m
	}
	return a, nil
}

func (c config) anchorSigner(log *slog.Logger) *audit.Signer {
	if c.AnchorKey == "" {
		return nil
	}
	seed, err := base64.StdEncoding.DecodeString(c.AnchorKey)
	if err == nil {
		var s *audit.Signer
		if s, err = audit.NewSigner(seed); err == nil {
			return s
		}
	}
	if log != nil {
		log.Error("TASKIEM_ANCHOR_KEY must be 32 bytes, base64 (openssl rand -base64 32)", "err", err)
	}
	return nil
}

// relyingParty is the passkey configuration from TASKIEM_PUBLIC_URL.
func (c config) relyingParty() (webauthn.Config, error) {
	if c.PublicURL == "" {
		return webauthn.Config{}, nil
	}
	u, err := url.Parse(c.PublicURL)
	if err != nil || u.Host == "" || (u.Scheme != "https" && u.Hostname() != "localhost") {
		return webauthn.Config{}, fmt.Errorf("TASKIEM_PUBLIC_URL must be an https URL (or http://localhost), not %q", c.PublicURL)
	}
	rp := c.PasskeyRPID
	if rp == "" {
		rp = u.Hostname()
	}
	if u.Hostname() != rp && !strings.HasSuffix(u.Hostname(), "."+rp) {
		return webauthn.Config{}, fmt.Errorf("TASKIEM_PASSKEY_RP_ID %q must be the host of TASKIEM_PUBLIC_URL or a domain above it", rp)
	}
	return webauthn.Config{RPID: rp, RPName: "Taskiem", Origins: []string{u.Scheme + "://" + u.Host}}, nil
}
