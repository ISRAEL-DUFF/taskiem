package main

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/israel-duff/taskiem/engine/egress"
	"github.com/israel-duff/taskiem/engine/forms"
	"github.com/israel-duff/taskiem/engine/inmail"
	"github.com/israel-duff/taskiem/engine/media"
)

// mailConfig is the mail role's (decision 0038, docs/email.md): the SMTP
// server that takes mail for workflows' email triggers.
type mailConfig struct {
	// Enabled runs the mail role inside `--role all` too
	// (TASKIEM_MAIL_ENABLED), as TASKIEM_PUBLIC_ENABLED does the public
	// host. `--role mail` always runs it. The api role needs only Domain.
	Enabled bool
	// Domain is the mail domain (TASKIEM_MAIL_DOMAIN, such as
	// in.taskiem.app): its MX record points at the mail role, and
	// workflows' addresses are <id>@Domain. Unset, email triggers can be
	// published but their addresses are not shown, and no mail is taken.
	Domain string
	// Listen is the SMTP listener (TASKIEM_MAIL_LISTEN, :2525). Mail
	// servers deliver to port 25: map it to this one.
	Listen string
	// Hostname is the name the server greets with (TASKIEM_MAIL_HOSTNAME,
	// default mx.<Domain>), which the MX record names.
	Hostname string
	// CertFile and KeyFile are the certificate offered with STARTTLS
	// (TASKIEM_MAIL_TLS_CERT, TASKIEM_MAIL_TLS_KEY); unset, a self-signed
	// one, which sending servers accept.
	CertFile, KeyFile string
}

var mailDomain = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?(\.[a-z0-9]([a-z0-9-]*[a-z0-9])?)+$`)

func mailConfigFromEnv() (mailConfig, error) {
	c := mailConfig{
		Enabled:  envBool("TASKIEM_MAIL_ENABLED", false),
		Domain:   strings.TrimSuffix(strings.ToLower(strings.TrimSpace(os.Getenv("TASKIEM_MAIL_DOMAIN"))), "."),
		Listen:   env("TASKIEM_MAIL_LISTEN", ":2525"),
		Hostname: strings.ToLower(strings.TrimSpace(os.Getenv("TASKIEM_MAIL_HOSTNAME"))),
		CertFile: strings.TrimSpace(os.Getenv("TASKIEM_MAIL_TLS_CERT")),
		KeyFile:  strings.TrimSpace(os.Getenv("TASKIEM_MAIL_TLS_KEY")),
	}
	if c.Domain != "" && !mailDomain.MatchString(c.Domain) {
		return c, fmt.Errorf("TASKIEM_MAIL_DOMAIN: %q is not a domain name", c.Domain)
	}
	if (c.CertFile == "") != (c.KeyFile == "") {
		return c, errors.New("TASKIEM_MAIL_TLS_CERT and TASKIEM_MAIL_TLS_KEY must both be set, or neither")
	}
	if c.Hostname == "" && c.Domain != "" {
		c.Hostname = "mx." + c.Domain
	}
	return c, nil
}

// emailService builds inbound email's api side, or nil without the public
// key (TASKIEM_PUBLIC_KEY), which the mail role seals messages with.
// Attachments are kept when a malware scanner is configured, as for forms.
func emailService(cfg config, e *engine, log *slog.Logger) (*inmail.Service, error) {
	if len(cfg.Public.Key) == 0 {
		return nil, nil
	}
	keys, err := forms.NewKeys(cfg.Public.Key)
	if err != nil {
		return nil, err
	}
	s := &inmail.Service{Pool: e.pool, Keys: keys, Start: e.store.Start, Domain: cfg.Mail.Domain, AppURL: cfg.PublicURL,
		Logger: log.With("component", "email")}
	s.Files = &media.Service{Credentials: e.tables().Credentials, Guard: &egress.Guard{Logger: log}, Logger: log}
	if addr := strings.TrimSpace(os.Getenv("TASKIEM_CLAMD_ADDR")); addr != "" {
		s.Scanner = &forms.ClamdScanner{Addr: addr}
	}
	for _, from := range []string{cfg.Messages.From, os.Getenv("TASKIEM_ALERT_FROM")} {
		if a := strings.TrimSpace(from); a != "" {
			if i := strings.LastIndexByte(a, '<'); i >= 0 {
				a = strings.Trim(a[i:], "<>")
			}
			s.OwnSenders = append(s.OwnSenders, a)
		}
	}
	return s, nil
}

// mailTask is the mail role's SMTP server on its own pool, connected as
// taskiem_public like the public host's: it may only check recipients and
// leave sealed messages in the inbox.
func mailTask(ctx context.Context, cfg config, log *slog.Logger) (func(context.Context) error, func(), error) {
	if cfg.Mail.Domain == "" {
		return nil, nil, errors.New("TASKIEM_MAIL_DOMAIN is required to take mail")
	}
	if len(cfg.Public.Key) == 0 {
		return nil, nil, errors.New("TASKIEM_PUBLIC_KEY is required to take mail: messages are sealed with it")
	}
	keys, err := forms.NewKeys(cfg.Public.Key)
	if err != nil {
		return nil, nil, err
	}
	pool, err := publicPool(ctx, cfg, log)
	if err != nil {
		return nil, nil, err
	}
	srv := &inmail.Server{Domain: cfg.Mail.Domain, Hostname: cfg.Mail.Hostname, Store: inmail.DBStore{Pool: pool}, Seal: keys,
		Resolver: net.DefaultResolver, Logger: log.With("server", "mail")}
	if cfg.Mail.CertFile != "" {
		cert, err := tls.LoadX509KeyPair(cfg.Mail.CertFile, cfg.Mail.KeyFile)
		if err != nil {
			pool.Close()
			return nil, nil, fmt.Errorf("TASKIEM_MAIL_TLS_CERT: %w", err)
		}
		srv.TLS = &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{cert}}
	}
	task := func(ctx context.Context) error {
		l, err := net.Listen("tcp", cfg.Mail.Listen)
		if err != nil {
			return fmt.Errorf("mail: %w", err)
		}
		log.Info("mail listening", "addr", cfg.Mail.Listen, "domain", cfg.Mail.Domain)
		return srv.Serve(ctx, l)
	}
	return task, pool.Close, nil
}

// serveMail runs `--role mail`: the SMTP server and an internal listener
// for metrics and health, with nothing of the engine.
func serveMail(ctx context.Context, cfg config, log *slog.Logger) error {
	task, closePool, err := mailTask(ctx, cfg, log)
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
	return runTasks(runCtx, stopRun, []func(context.Context) error{task, httpTask("metrics", cfg.MetricsListen, internal, log)}, log)
}

// emailDrainInterval is how often the api role takes received mail.
const emailDrainInterval = 2 * time.Second
