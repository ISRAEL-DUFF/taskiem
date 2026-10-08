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
	"net/netip"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/israel-duff/taskiem/api"
	"github.com/israel-duff/taskiem/connectors/builtin"
	"github.com/israel-duff/taskiem/engine/ai"
	"github.com/israel-duff/taskiem/engine/alerts"
	"github.com/israel-duff/taskiem/engine/audit"
	"github.com/israel-duff/taskiem/engine/billing"
	"github.com/israel-duff/taskiem/engine/byok"
	"github.com/israel-duff/taskiem/engine/canary"
	"github.com/israel-duff/taskiem/engine/catalogue"
	"github.com/israel-duff/taskiem/engine/connector"
	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/egress"
	"github.com/israel-duff/taskiem/engine/embed"
	"github.com/israel-duff/taskiem/engine/httpsec"
	"github.com/israel-duff/taskiem/engine/ingest"
	"github.com/israel-duff/taskiem/engine/pii"
	"github.com/israel-duff/taskiem/engine/remote"
	"github.com/israel-duff/taskiem/engine/runtime"
	"github.com/israel-duff/taskiem/engine/sandbox"
	"github.com/israel-duff/taskiem/engine/secrets"
	"github.com/israel-duff/taskiem/engine/status"
	"github.com/israel-duff/taskiem/engine/telemetry"
	"github.com/israel-duff/taskiem/engine/wasmconn"
	"github.com/israel-duff/taskiem/engine/webauthn"
	"github.com/israel-duff/taskiem/engine/whatsapp"
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
	// HooksURL is where providers reach /hooks, for the subscriptions
	// Taskiem registers with them (TASKIEM_HOOKS_URL; default PublicURL +
	// "/hooks", where an all-in-one install serves them).
	HooksURL string
	// SMTPURL and AlertFrom let alerts go out by email.
	SMTPURL, AlertFrom string
	Queues             []string
	Cloud              cloudConfig
	Connectors         builtin.Options
	PoolSize           int32
	LoginBurst         int
	// Signup settings (docs/onboarding.md): signups per address a day
	// (TASKIEM_SIGNUP_PER_ADDRESS), extra blocked email domains
	// (TASKIEM_SIGNUP_BLOCKED_DOMAINS) and the help links' docs site
	// (TASKIEM_DOCS_URL).
	SignupPerAddress     int
	SignupBlockedDomains []string
	DocsURL              string
	// Limits are the platform's default plan limits (TASKIEM_DEFAULT_*).
	Limits runtime.Limits
	// WhatsApp is the platform number (TASKIEM_WHATSAPP_*); nil: off.
	WhatsApp *whatsapp.Config
	// AI is the model provider for AI building (TASKIEM_AI_*, docs/ai.md).
	AI ai.Config
	// Languages are languages beyond English and voice notes on WhatsApp,
	// USSD and SMS (TASKIEM_LANGUAGES, TASKIEM_TRANSCRIBE_*; languages.go).
	Languages LanguagesConfig
	// Billing turns plans, subscriptions and payments on (TASKIEM_BILLING=on,
	// docs/billing.md); off, every tenant is on the internal plan.
	Billing BillingConfig
	// Keys tunes customer keys and the key job (TASKIEM_BYOK_*,
	// TASKIEM_KEY_*; docs/byok.md).
	Keys KeysConfig
	// Shutdown is how the process stops on SIGTERM (docs/reliability.md).
	Shutdown ShutdownConfig
	// Status is the public status page and its admin API; Canary the
	// synthetic end-to-end probe (docs/reliability.md).
	Status StatusConfig
	Canary CanaryConfig
}

// ShutdownConfig orders a graceful stop (spec 15.4): readiness flips at
// once, the process keeps serving for Delay so load balancers stop sending
// it traffic, then servers stop accepting work and workers drain in-flight
// steps for up to WorkerDrain before releasing what they still hold.
// Delay + WorkerDrain + a few seconds must fit in the pod's grace period.
type ShutdownConfig struct {
	Delay       time.Duration // TASKIEM_SHUTDOWN_DELAY, default 0 (the chart sets 10s)
	WorkerDrain time.Duration // TASKIEM_WORKER_DRAIN, default 30s
}

func shutdownConfig() (ShutdownConfig, error) {
	c := ShutdownConfig{WorkerDrain: 30 * time.Second}
	for _, d := range []struct {
		name     string
		into     *time.Duration
		min, max time.Duration
	}{
		{"TASKIEM_SHUTDOWN_DELAY", &c.Delay, 0, 2 * time.Minute},
		{"TASKIEM_WORKER_DRAIN", &c.WorkerDrain, time.Second, 10 * time.Minute},
	} {
		v := os.Getenv(d.name)
		if v == "" {
			continue
		}
		dur, err := time.ParseDuration(v)
		if err != nil || dur < d.min || dur > d.max {
			return c, fmt.Errorf("%s must be a duration between %s and %s, not %q", d.name, d.min, d.max, v)
		}
		*d.into = dur
	}
	return c, nil
}

// KeysConfig is how tenant keys and customer keys (BYOK) behave.
type KeysConfig struct {
	// CacheTTL bounds how long a tenant key that depends on a customer key
	// stays unwrapped in a process (TASKIEM_BYOK_CACHE_TTL, default 5m, at
	// most 1h): revocation takes effect within it.
	CacheTTL time.Duration
	// CheckInterval is how often the key job checks keys, re-wraps and
	// resumes parked steps (TASKIEM_KEY_CHECK_INTERVAL, default 1m).
	CheckInterval time.Duration
	// DestroyAfter is how long a retired, unused tenant key version is kept
	// (TASKIEM_KEY_DESTROY_AFTER, default 24h).
	DestroyAfter time.Duration
	// AllowPrivate lets customer key services resolve to private addresses
	// (TASKIEM_BYOK_ALLOW_PRIVATE=on): dedicated single-tenant deployments
	// only.
	AllowPrivate bool
}

func keysConfig() (KeysConfig, error) {
	k := KeysConfig{CacheTTL: 5 * time.Minute, CheckInterval: time.Minute, DestroyAfter: 24 * time.Hour, AllowPrivate: envBool("TASKIEM_BYOK_ALLOW_PRIVATE", false)}
	for _, d := range []struct {
		name     string
		into     *time.Duration
		min, max time.Duration
	}{
		{"TASKIEM_BYOK_CACHE_TTL", &k.CacheTTL, time.Second, time.Hour},
		{"TASKIEM_KEY_CHECK_INTERVAL", &k.CheckInterval, 10 * time.Second, time.Hour},
		{"TASKIEM_KEY_DESTROY_AFTER", &k.DestroyAfter, time.Minute, 90 * 24 * time.Hour},
	} {
		v := os.Getenv(d.name)
		if v == "" {
			continue
		}
		dur, err := time.ParseDuration(v)
		if err != nil || dur < d.min || dur > d.max {
			return k, fmt.Errorf("%s must be a duration between %s and %s, not %q", d.name, d.min, d.max, v)
		}
		*d.into = dur
	}
	return k, nil
}

// BillingConfig is the platform's billing (operator credentials, never a
// tenant's connections).
type BillingConfig struct {
	On        bool
	PlansFile string
	Provider  string // default provider for checkouts
	// Paystack and Flutterwave credentials; a provider without a key is off.
	PaystackKey, PaystackURL                    string
	FlutterwaveKey, FlutterwaveHash, FlutterURL string
}

func billingConfig() (BillingConfig, error) {
	b := BillingConfig{PlansFile: env("TASKIEM_BILLING_PLANS", "deploy/plans.yaml"), Provider: env("TASKIEM_BILLING_PROVIDER", "paystack"),
		PaystackKey: os.Getenv("TASKIEM_BILLING_PAYSTACK_SECRET_KEY"), PaystackURL: os.Getenv("TASKIEM_BILLING_PAYSTACK_URL"),
		FlutterwaveKey: os.Getenv("TASKIEM_BILLING_FLUTTERWAVE_SECRET_KEY"), FlutterwaveHash: os.Getenv("TASKIEM_BILLING_FLUTTERWAVE_WEBHOOK_HASH"),
		FlutterURL: os.Getenv("TASKIEM_BILLING_FLUTTERWAVE_URL")}
	switch v := strings.ToLower(env("TASKIEM_BILLING", "off")); v {
	case "on", "true", "1":
		b.On = true
	case "off", "false", "0", "":
	default:
		return b, fmt.Errorf("TASKIEM_BILLING must be on or off, not %q", v)
	}
	return b, nil
}

// billingService builds the billing service: the plan catalogue from the
// config file (synced to the database at start), the payment providers
// with keys, email for dunning.
func (e *engine) billingService(ctx context.Context, alerter *alerts.Alerter) (*billing.Service, error) {
	b := e.cfg.Billing
	svc := &billing.Service{Pool: e.pool, Store: e.store, Enabled: b.On, Logger: e.log, PublicURL: e.cfg.PublicURL, Providers: map[string]billing.Provider{}, Default: b.Provider}
	if alerter != nil {
		svc.Mailer, svc.From = alerter.Mailer, alerter.From
	}
	if !b.On {
		return svc, nil
	}
	c, err := billing.LoadConfig(b.PlansFile)
	if err != nil {
		return nil, fmt.Errorf("TASKIEM_BILLING_PLANS %s: %w", b.PlansFile, err)
	}
	if c.PlaceholderPrices {
		e.log.Warn("billing: plan prices are placeholders pending decision B1 (docs/needs-people.md)", "file", b.PlansFile)
	}
	if err := billing.Store(ctx, e.pool, c, "config:"+b.PlansFile); err != nil {
		return nil, fmt.Errorf("billing: loading plans: %w", err)
	}
	svc.Config = c
	if b.PaystackKey != "" {
		svc.Providers["paystack"] = &billing.Paystack{SecretKey: b.PaystackKey, BaseURL: b.PaystackURL}
	}
	if b.FlutterwaveKey != "" {
		svc.Providers["flutterwave"] = &billing.Flutterwave{SecretKey: b.FlutterwaveKey, WebhookHash: b.FlutterwaveHash, BaseURL: b.FlutterURL}
	}
	if _, ok := svc.Providers[b.Provider]; !ok {
		e.log.Warn("billing: the default payment provider has no key; checkouts will fail", "provider", b.Provider)
	}
	return svc, nil
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
		HooksURL:      strings.TrimRight(os.Getenv("TASKIEM_HOOKS_URL"), "/"),
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
			FlutterwaveURL:    os.Getenv("TASKIEM_FLUTTERWAVE_URL"),
			AnchorURL:         os.Getenv("TASKIEM_GETANCHOR_URL"), // not TASKIEM_ANCHOR_*: those are audit anchors
			LencoURL:          os.Getenv("TASKIEM_LENCO_URL"),
			BreetURL:          os.Getenv("TASKIEM_BREET_URL"),
			MoniepointURL:     os.Getenv("TASKIEM_MONIEPOINT_URL"),
			InterswitchURL:    os.Getenv("TASKIEM_INTERSWITCH_URL"),
			S3URL:             os.Getenv("TASKIEM_S3_URL"),
			AfricasTalkingURL: os.Getenv("TASKIEM_AFRICASTALKING_URL"),
			TelegramURL:       os.Getenv("TASKIEM_TELEGRAM_URL"),
			WhatsAppURL:       os.Getenv("TASKIEM_WHATSAPP_URL"),
			SlackURL:          os.Getenv("TASKIEM_SLACK_URL"),
			GmailURL:          os.Getenv("TASKIEM_GMAIL_URL"),
			GooglesheetsURL:   os.Getenv("TASKIEM_GOOGLESHEETS_URL"),
			GoogleTokenURL:    os.Getenv("TASKIEM_GOOGLE_TOKEN_URL"),
			OpayURL:           os.Getenv("TASKIEM_OPAY_URL"),
			RemitaURL:         os.Getenv("TASKIEM_REMITA_URL"),
			MonoURL:           os.Getenv("TASKIEM_MONO_URL"),
			PremblyURL:        os.Getenv("TASKIEM_PREMBLY_URL"),
			YouverifyURL:      os.Getenv("TASKIEM_YOUVERIFY_URL"),
			MpesaURL:          os.Getenv("TASKIEM_MPESA_URL"),
			MTNMoMoURL:        os.Getenv("TASKIEM_MTNMOMO_URL"),
			PGDockURL:         os.Getenv("TASKIEM_PGDOCK_URL"),
		},
		PoolSize: 20,
	}
	// Administrators are held to passkeys wherever passkeys can work.
	if c.HooksURL == "" && c.PublicURL != "" {
		c.HooksURL = c.PublicURL + "/hooks"
	}
	c.RequireAdminPasskeys = envBool("TASKIEM_REQUIRE_ADMIN_PASSKEYS", c.PublicURL != "")
	if n, err := strconv.Atoi(os.Getenv("TASKIEM_LOGIN_BURST")); err == nil && n > 0 {
		c.LoginBurst = n
	}
	if v := os.Getenv("TASKIEM_SIGNUP_PER_ADDRESS"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			return c, fmt.Errorf("TASKIEM_SIGNUP_PER_ADDRESS: %q is not a number (negative: no limit)", v)
		}
		c.SignupPerAddress = n
	}
	for _, d := range strings.Split(os.Getenv("TASKIEM_SIGNUP_BLOCKED_DOMAINS"), ",") {
		if d = strings.ToLower(strings.TrimSpace(d)); d != "" {
			c.SignupBlockedDomains = append(c.SignupBlockedDomains, d)
		}
	}
	c.DocsURL = strings.TrimRight(os.Getenv("TASKIEM_DOCS_URL"), "/")
	if n, err := strconv.Atoi(os.Getenv("TASKIEM_DATABASE_POOL")); err == nil && n > 0 {
		c.PoolSize = int32(n) //nolint:gosec // small operator-set value
	}
	limits, err := runtime.LimitsFromEnv(os.LookupEnv)
	if err != nil {
		return c, err
	}
	c.Limits = limits
	if c.WhatsApp, err = whatsapp.ConfigFromEnv(os.LookupEnv); err != nil {
		return c, err
	}
	if c.AI, err = ai.ConfigFromEnv(os.LookupEnv); err != nil {
		return c, err
	}
	if c.Billing, err = billingConfig(); err != nil {
		return c, err
	}
	if c.Keys, err = keysConfig(); err != nil {
		return c, err
	}
	if c.Shutdown, err = shutdownConfig(); err != nil {
		return c, err
	}
	if c.Status, err = statusConfig(); err != nil {
		return c, err
	}
	if c.Languages, err = languagesConfig(); err != nil {
		return c, err
	}
	if c.Canary, err = canaryConfig(); err != nil {
		return c, err
	}
	if c.Cloud, err = cloudConfigFromEnv(); err != nil {
		return c, err
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
	pc.AfterConnect = afterConnect(c.DBRole)
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

// afterConnect switches each new connection to the application role, so
// row-level security applies to everything it runs.
func afterConnect(dbRole string) func(context.Context, *pgx.Conn) error {
	if dbRole == "" {
		return nil
	}
	role := pgx.Identifier{dbRole}.Sanitize() //nolint:misspell // pgx API
	return func(ctx context.Context, conn *pgx.Conn) error {
		_, err := conn.Exec(ctx, "SET ROLE "+role)
		return err
	}
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
	// wa is the platform WhatsApp number, when configured.
	wa *whatsapp.Platform
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
	vault := &secrets.Vault{Pool: pool, KMS: kms, RootKey: cfg.KMSKey,
		BYOK:         &byok.Factory{Guard: &egress.Guard{Logger: log}, AllowPrivate: cfg.Keys.AllowPrivate},
		BYOKCacheTTL: cfg.Keys.CacheTTL, DestroyAfter: cfg.Keys.DestroyAfter}
	e := &engine{cfg: cfg, log: log, pool: pool, registry: reg, vault: vault, connectors: src,
		store: &runtime.Store{Pool: pool, Registry: reg, PII: vault, Defaults: &cfg.Limits, Billing: cfg.Billing.On}}
	if cfg.WhatsApp != nil {
		e.wa = whatsapp.New(pool, *cfg.WhatsApp, &egress.Guard{Logger: log}, log)
		// Tenants' own numbers (credentials in their vaults) and template
		// cost accounting against their plans (spec 11.4, 16).
		e.wa.Secrets = vault
		e.wa.Meter = &whatsapp.UsageMeter{Pool: pool, Allowance: func(ctx context.Context, t uuid.UUID) (int64, error) {
			l, err := e.store.LimitsFor(ctx, t)
			return l.WhatsAppTemplatesMonthly, err
		}}
	}
	return e, nil
}

// connectorLoopback lets a connector the operator pointed at a loopback
// address (TASKIEM_TERMII_URL=http://127.0.0.1:12727, a fake provider for
// browser tests) reach that address and port, and nothing else private.
// Only an IP literal counts: a name resolving to loopback is refused as
// ever.
func connectorLoopback(reg *connector.Registry, log *slog.Logger) map[string]string {
	var out map[string]string
	for _, c := range reg.List() {
		u, err := url.Parse(c.Manifest.BaseURL)
		if err != nil {
			continue
		}
		ip, err := netip.ParseAddr(u.Hostname())
		if err != nil || !ip.IsLoopback() {
			continue
		}
		port := u.Port()
		if port == "" {
			port = map[string]string{"http": "80", "https": "443"}[u.Scheme]
		}
		if out == nil {
			out = map[string]string{}
		}
		out["connector:"+c.Manifest.ID] = port
		log.Warn("a connector points at this machine: for tests only", "connector", c.Manifest.ID, "url", c.Manifest.BaseURL)
	}
	return out
}

func (e *engine) hooks() *ingest.Handler {
	return &ingest.Handler{Store: e.store, Secrets: e.vault, Connections: e.vault, Registry: e.registry, Logger: e.log,
		Egress: &egress.Guard{Logger: e.log}}
}

// remote keeps providers' subscriptions for remotely registered triggers
// in step with deployments (decision 0021).
func (e *engine) remote() *remote.Reconciler {
	return &remote.Reconciler{Pool: e.pool, Vault: e.vault, Registry: e.registry, Egress: &egress.Guard{Logger: e.log},
		HooksURL: e.cfg.HooksURL, Logger: e.log}
}

// aiSettings is AI building's model and limits (TASKIEM_AI_*), or nil when
// no model is configured. Tenants' monthly AI budgets apply wherever it is
// used (docs/ai.md).
func aiSettings(c ai.Config) (*api.AISettings, error) {
	prov, err := ai.New(c)
	if err != nil || prov == nil {
		return nil, err
	}
	return &api.AISettings{Provider: prov, Effort: c.Effort, MaxTokens: c.MaxTokens}, nil
}

// edgeServer is the edge role's server: git pushes, and WhatsApp and USSD
// callbacks. Building over WhatsApp uses the same model, effort, answer
// size and budgets as the api role.
func edgeServer(e *engine, cfg config, log *slog.Logger) (*api.Server, error) {
	srv := &api.Server{Store: e.store, Vault: e.vault, Registry: e.registry, Logger: log, WhatsApp: e.wa, PublicURL: cfg.PublicURL, TrustProxy: cfg.TrustProxy}
	var err error
	if srv.AI, err = aiSettings(cfg.AI); err != nil {
		return nil, err
	}
	if srv.Languages, srv.Voice, err = channelLanguages(cfg.Languages, cfg.AI, log); err != nil {
		return nil, err
	}
	return srv, nil
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
	if err := configureEgress(log); err != nil { // TASKIEM_EGRESS_PROXY (hardening.go)
		return fmt.Errorf("serve: %w", err)
	}
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
	for _, w := range dsnWarnings("TASKIEM_DATABASE_URL", cfg.DSN) {
		log.Warn(w)
	}
	db.RetryWindow = cfg.Cloud.RetryWindow

	is := func(r string) bool { return *role == r || *role == "all" }
	// Graceful shutdown (spec 15.4, docs/reliability.md): SIGTERM ends ctx;
	// readiness flips at once, the process keeps serving for the shutdown
	// delay so load balancers stop routing to it, and only then does runCtx
	// end, stopping servers and letting workers drain.
	var draining atomic.Bool
	telemetry.Draining.Set(0)
	runCtx, stopRun := context.WithCancel(context.WithoutCancel(ctx))
	defer stopRun()
	go func() {
		select {
		case <-ctx.Done():
		case <-runCtx.Done():
			return
		}
		draining.Store(true)
		telemetry.Draining.Set(1)
		log.Info("shutting down: not ready; draining", "delay", cfg.Shutdown.Delay.String(), "worker_drain", cfg.Shutdown.WorkerDrain.String())
		t := time.NewTimer(cfg.Shutdown.Delay)
		defer t.Stop()
		select {
		case <-t.C:
		case <-runCtx.Done():
		}
		stopRun()
	}()
	ready := func(w http.ResponseWriter, r *http.Request) {
		if draining.Load() {
			http.Error(w, "draining", http.StatusServiceUnavailable)
			return
		}
		pctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		if err := e.pool.Ping(pctx); err != nil {
			http.Error(w, "database unavailable", http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write([]byte("ok"))
	}
	alerter, err := cfg.alerter(e, log)
	if err != nil {
		return fmt.Errorf("serve: %w", err)
	}
	bill, err := e.billingService(ctx, alerter)
	if err != nil {
		return fmt.Errorf("serve: %w", err)
	}
	var tasks []func(context.Context) error
	if is("api") && cfg.Cloud.ReadDSN != "" {
		// Run lists and dashboards read from the replica while it keeps
		// up (decision 0024).
		replica, err := openReplica(ctx, cfg, e.pool, log)
		if err != nil {
			return fmt.Errorf("serve: %w", err)
		}
		defer replica.Pool.Close()
		e.store.Read = replica
		tasks = append(tasks, replica.Run)
	}
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
		srv.Remote = e.remote()
		srv.Alerts = alerter
		// Catalogue submissions are checked in a sandbox of their own,
		// started per submission (docs/connector-submissions.md).
		srv.Catalogue = &catalogue.Checker{}
		srv.LoginBurst = cfg.LoginBurst
		if err := harden(srv, cfg.PublicURL); err != nil { // HSTS, tenant code limits (hardening.go)
			return err
		}
		srv.SignupPerAddress, srv.SignupBlockedDomains, srv.DocsURL = cfg.SignupPerAddress, cfg.SignupBlockedDomains, cfg.DocsURL
		if srv.AI, err = aiSettings(cfg.AI); err != nil {
			return err
		} else if srv.AI != nil {
			log.Info("AI building on", "provider", srv.AI.Provider.Name(), "model", srv.AI.Provider.Model())
			// Self-repair works its queue where a model is configured (docs/ai.md).
			tasks = append(tasks, func(ctx context.Context) error { srv.RunRepairs(ctx, 5*time.Second); return nil })
		}
		// Checking a Python step compiles CPython first (seconds): do it now.
		go func() { _ = sandbox.InitPython() }()
		srv.Done = runCtx.Done()
		srv.Draining = draining.Load
		if cfg.Status.Page || len(cfg.Status.Tokens) > 0 {
			srv.Status = &api.StatusSettings{Tokens: cfg.Status.Tokens}
			if cfg.Status.Page {
				srv.Status.Page = &status.Handler{Pool: e.pool, PublicURL: cfg.PublicURL, Logger: log, Options: status.Options{Canary: cfg.Status.Canary}}
			}
		}
		if srv.Ops, err = opsSettings(); err != nil { // the operator console (operatorscmd.go)
			return err
		}
		if *role == "all" {
			srv.Ingest = e.hooks() // one listener for a small install
		}
		if cfg.WebDir != "" {
			srv.Static = api.SPA(cfg.WebDir)
			// The embedded builder's bundle (pnpm build writes it there).
			srv.EmbedDir = cfg.WebDir + "/embed/v1"
		}
		srv.WhatsApp = e.wa
		if srv.Languages, srv.Voice, err = channelLanguages(cfg.Languages, cfg.AI, log); err != nil {
			return err
		}
		srv.Billing = bill
		tasks = append(tasks, httpTask("api", cfg.Listen, srv.Handler(), log), srv.RunGitSyncs, srv.RunWhatsApp, srv.RunUSSD)
	}
	if *role == "edge" {
		mux := http.NewServeMux()
		mux.Handle("/hooks/", http.StripPrefix("/hooks", e.hooks()))
		git, err := edgeServer(e, cfg, log)
		if err != nil {
			return err
		}
		mux.Handle("/git-hooks/", http.StripPrefix("/git-hooks", git.GitHooks()))
		if e.wa != nil {
			// The platform number's webhook (docs/whatsapp.md), apart from
			// tenants' connector triggers under /hooks.
			wa := http.StripPrefix("/channels/whatsapp", git.WhatsAppHooks())
			mux.Handle("/channels/whatsapp", wa)
			mux.Handle("/channels/whatsapp/", wa)
		}
		// USSD aggregators' callbacks: the fast path (docs/ussd.md).
		mux.Handle("/channels/ussd/", http.StripPrefix("/channels/ussd", git.USSDHooks()))
		mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("ok")) })
		mux.HandleFunc("/readyz", ready)
		tasks = append(tasks, httpTask("edge", cfg.EdgeListen, httpsec.Headers(mux), log))
	}
	if is("scheduler") {
		s := &runtime.Scheduler{Store: e.store, Logger: log}
		if cfg.ArchiveDir != "" {
			s.Archiver = runtime.FileArchiver{Dir: cfg.ArchiveDir}
		} else {
			log.Warn("TASKIEM_ARCHIVE_DIR unset: runs past retention are kept, not purged")
		}
		cron := &ingest.Cron{Store: e.store, Logger: log}
		// Hourly digests of secret reads into each tenant's audit chain,
		// and their retention.
		digests := &secrets.ReadDigester{Pool: e.pool, Logger: log}
		// Partner webhooks: sub-tenants' run outcomes, publishes and usage.
		hooks := &embed.Webhooks{Pool: e.pool, Secrets: e.vault, Limits: e.store.LimitsFor, Egress: &egress.Guard{Logger: log}, Logger: log}
		// Billing: periods, dunning, payment reconciliation, usage snapshots.
		// Verified custom domains are re-verified (docs/embedding.md).
		domains := &embed.DomainChecker{Pool: e.pool, Logger: log}
		// Tenant keys: customer key health, re-wrapping after rotations,
		// and resuming steps parked while a key was unavailable (docs/byok.md).
		keys := &secrets.KeyJob{Vault: e.vault, Interval: cfg.Keys.CheckInterval, Resume: e.store.ResumeKeyParked, Logger: log,
			Unavailable: func(_ uuid.UUID, h secrets.Health) {
				by := "platform"
				if h.Customer {
					by = "customer"
				}
				telemetry.KeyChecksFailed.WithLabelValues(by).Inc()
			}}
		tasks = append(tasks, s.Run, cron.Run, alerter.Run, digests.Run, hooks.Run, bill.Run, domains.Run, e.remote().Run, keys.Run)
		if cfg.Canary.On() {
			// The synthetic end-to-end probe (docs/reliability.md).
			p := cfg.Canary.prober(log)
			p.Record = func(ctx context.Context, r canary.Result) error {
				return status.RecordProbe(ctx, e.pool, r.OK, r.Accept, r.Complete, r.Code())
			}
			tasks = append(tasks, func(ctx context.Context) error { return p.Run(ctx, cfg.Canary.Interval) })
			log.Info("canary on", "interval", cfg.Canary.Interval.String())
		}
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
		loopback := connectorLoopback(e.registry, log)
		prefix := host + "/"
		if cfg.Cloud.WorkerPool != runtime.SharedPool {
			prefix += cfg.Cloud.WorkerPool + "/"
		}
		for _, q := range cfg.Queues {
			w := &runtime.Worker{Store: e.store, Registry: e.registry, Secrets: e.vault, Connections: e.vault,
				Egress: &egress.Guard{Logger: log, Loopback: loopback}, ID: prefix + strings.TrimSpace(q) + "/" + strconv.Itoa(os.Getpid()),
				Queue: strings.TrimSpace(q), Pool: cfg.Cloud.WorkerPool, Logger: log,
				Drain: cfg.Shutdown.WorkerDrain}
			if w.Queue == "container" {
				// Container steps (spec 7.5): the sandbox runner and egress proxy.
				more, err := containerSetup(ctx, w, log)
				if err != nil {
					return fmt.Errorf("serve: container queue: %w", err)
				}
				tasks = append(tasks, more...)
			}
			tasks = append(tasks, w.Run)
		}
	}
	_ = prometheus.Register(telemetry.QueueCollector{Pool: e.pool}) // already registered when serve runs twice in one process (tests)
	_ = prometheus.Register(telemetry.PoolCollector{Pool: e.pool})
	// Every role serves metrics and a liveness check (/healthz) here, so
	// roles without the API can be probed too.
	metrics := http.NewServeMux()
	metrics.Handle("/metrics", promhttp.Handler())
	metrics.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("ok")) })
	metrics.HandleFunc("/readyz", ready)
	tasks = append(tasks, httpTask("metrics", cfg.MetricsListen, metrics, log))

	log.Info("taskiem starting")
	var wg sync.WaitGroup
	errs := make(chan error, len(tasks))
	for _, t := range tasks {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := t(runCtx); err != nil {
				errs <- err
				stopRun() // one role failing stops the process; the supervisor restarts it
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
	if e.wa != nil {
		a.WhatsApp = e.wa
	}
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
