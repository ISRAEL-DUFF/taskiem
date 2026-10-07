package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/israel-duff/taskiem/api"
	"github.com/israel-duff/taskiem/engine/canary"
	"github.com/israel-duff/taskiem/engine/status"
)

// StatusConfig is the public status page (docs/reliability.md#status-page).
type StatusConfig struct {
	// Page serves /status on the api role (TASKIEM_STATUS_PAGE, default on).
	Page bool
	// Canary shows the canary's results and lets repeated failures mark
	// components degraded (TASKIEM_STATUS_CANARY, default on).
	Canary bool
	// Tokens admit operators to /v1/status/admin (TASKIEM_STATUS_TOKENS).
	Tokens []api.OperatorToken
}

func statusConfig() (StatusConfig, error) {
	c := StatusConfig{Page: envBool("TASKIEM_STATUS_PAGE", true), Canary: envBool("TASKIEM_STATUS_CANARY", true)}
	var err error
	c.Tokens, err = api.ParseOperatorTokens(os.Getenv("TASKIEM_STATUS_TOKENS"))
	if err != nil {
		return c, fmt.Errorf("TASKIEM_STATUS_TOKENS: %w", err)
	}
	return c, nil
}

// CanaryConfig is the synthetic probe the scheduler role runs when
// TASKIEM_CANARY_HOOK_URL is set (docs/reliability.md#synthetic-canary).
type CanaryConfig struct {
	HookURL, Secret, APIURL, APIKey string
	Interval, Timeout               time.Duration
}

// On reports whether the canary is configured.
func (c CanaryConfig) On() bool { return c.HookURL != "" }

func canaryConfig() (CanaryConfig, error) {
	c := CanaryConfig{HookURL: os.Getenv("TASKIEM_CANARY_HOOK_URL"), Secret: os.Getenv("TASKIEM_CANARY_SECRET"),
		APIURL: strings.TrimRight(env("TASKIEM_CANARY_API_URL", os.Getenv("TASKIEM_PUBLIC_URL")), "/"), APIKey: os.Getenv("TASKIEM_CANARY_API_KEY"),
		Interval: time.Minute, Timeout: time.Minute}
	for _, d := range []struct {
		name     string
		into     *time.Duration
		min, max time.Duration
	}{
		{"TASKIEM_CANARY_INTERVAL", &c.Interval, 15 * time.Second, time.Hour},
		{"TASKIEM_CANARY_TIMEOUT", &c.Timeout, 5 * time.Second, 10 * time.Minute},
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
	if !c.On() {
		return c, nil
	}
	if c.Secret == "" || c.APIKey == "" || c.APIURL == "" {
		return c, errors.New("TASKIEM_CANARY_HOOK_URL needs TASKIEM_CANARY_SECRET, TASKIEM_CANARY_API_KEY and TASKIEM_CANARY_API_URL (or TASKIEM_PUBLIC_URL); taskiem canary setup prints them")
	}
	for _, u := range []string{c.HookURL, c.APIURL} {
		if p, err := url.Parse(u); err != nil || (p.Scheme != "https" && p.Scheme != "http") || p.Host == "" {
			return c, fmt.Errorf("canary URL %q is not an http(s) URL", u)
		}
	}
	return c, nil
}

func (c CanaryConfig) prober(log *slog.Logger) *canary.Prober {
	return &canary.Prober{HookURL: c.HookURL, Secret: c.Secret, APIURL: c.APIURL, APIKey: c.APIKey, Timeout: c.Timeout, Logger: log}
}

const statusUsage = `usage:
  taskiem status show                                  print the public page as JSON
  taskiem status list                                  incidents of the last 90 days, with who posted each update
  taskiem status open --title T --components api,runs [--impact degraded] [--status investigating] --message M
  taskiem status maintenance --title T --components C --from TIME --to TIME --message M   (RFC 3339 times)
  taskiem status update ID --status S [--impact I] --message M
  taskiem status resolve ID --message M                resolve an incident or complete maintenance
  taskiem status token NAME                            make an operator token for the admin API
components: api, webhooks, runs, scheduler, integration:<name>; impacts: degraded, partial_outage, major_outage
`

// statusCmd declares incidents and maintenance on the status page, as the
// operator ($USER), recorded in the append-only updates.
func statusCmd(ctx context.Context, args []string, stdout io.Writer) error {
	if len(args) == 0 {
		return errors.New(statusUsage)
	}
	if args[0] == "token" {
		if len(args) != 2 || !nameOK(args[1]) {
			return errors.New("usage: taskiem status token NAME (letters, digits, _)")
		}
		raw := make([]byte, 32)
		_, _ = rand.Read(raw)
		tok := "tks_" + hex.EncodeToString(raw)
		sum := sha256.Sum256([]byte(tok))
		fmt.Fprintf(stdout, "token (give it to %s; shown once):\n  %s\nadd to TASKIEM_STATUS_TOKENS on the api role (comma-separated):\n  %s:%s\n", args[1], tok, args[1], hex.EncodeToString(sum[:]))
		return nil
	}
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	pool, err := openPool(ctx, cfg)
	if err != nil {
		return err
	}
	defer pool.Close()
	actor := "cli:" + env("USER", "operator")
	fs := flag.NewFlagSet("status "+args[0], flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	title := fs.String("title", "", "")
	components := fs.String("components", "", "")
	impact := fs.String("impact", "", "")
	st := fs.String("status", "", "")
	message := fs.String("message", "", "")
	from := fs.String("from", "", "")
	to := fs.String("to", "", "")
	rest := args[1:]
	var id uuid.UUID
	if args[0] == "update" || args[0] == "resolve" {
		if len(rest) == 0 {
			return errors.New(statusUsage)
		}
		if id, err = uuid.Parse(rest[0]); err != nil {
			return fmt.Errorf("status %s: %q is not an incident id", args[0], rest[0])
		}
		rest = rest[1:]
	}
	if err := fs.Parse(rest); err != nil {
		return fmt.Errorf("%w\n%s", err, statusUsage)
	}
	split := func(v string) []string {
		var out []string
		for _, c := range strings.Split(v, ",") {
			if c = strings.TrimSpace(c); c != "" {
				out = append(out, c)
			}
		}
		return out
	}
	switch args[0] {
	case "show", "list":
		var v any
		if args[0] == "show" {
			v, err = status.Load(ctx, pool, time.Now(), status.Options{Canary: cfg.Status.Canary})
		} else {
			v, err = status.Incidents(ctx, pool, time.Now().AddDate(0, 0, -90), true)
		}
		if err != nil {
			return err
		}
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(v)
	case "open", "maintenance":
		d := status.Declaration{Kind: "incident", Title: *title, Components: split(*components), Status: *st, Impact: *impact, Message: *message}
		if args[0] == "maintenance" {
			d.Kind = "maintenance"
			start, err1 := time.Parse(time.RFC3339, *from)
			end, err2 := time.Parse(time.RFC3339, *to)
			if err1 != nil || err2 != nil {
				return errors.New("status maintenance: --from and --to are RFC 3339 times (2026-11-02T22:00:00+01:00)")
			}
			d.StartsAt, d.EndsAt = &start, &end
		}
		id, err := status.Open(ctx, pool, d, actor)
		if err != nil {
			return fmt.Errorf("status %s: %w", args[0], err)
		}
		fmt.Fprintf(stdout, "%s %s opened (recorded as %s)\n", d.Kind, id, actor)
		return nil
	case "update", "resolve":
		c := status.Change{Status: *st, Impact: *impact, Message: *message}
		if args[0] == "resolve" {
			c.Status, c.Impact = "resolved", "none"
			list, err := status.Incidents(ctx, pool, time.Time{}, false)
			if err != nil {
				return err
			}
			for _, i := range list {
				if i.ID == id && i.Kind == "maintenance" {
					c.Status = "completed"
				}
			}
		}
		if err := status.Post(ctx, pool, id, c, actor); err != nil {
			return fmt.Errorf("status %s: %w", args[0], err)
		}
		fmt.Fprintf(stdout, "%s: %s (recorded as %s)\n", id, c.Status, actor)
		return nil
	}
	return errors.New(statusUsage)
}

func nameOK(s string) bool {
	if s == "" || len(s) > 40 {
		return false
	}
	for _, r := range s {
		if !(r == '_' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9') {
			return false
		}
	}
	return true
}

const canaryUsage = `usage:
  taskiem canary setup [--url API] [--key OWNER_KEY] [--hooks-url URL] [--env prod]
        create (or refresh) the canary workflow in this internal tenant, its webhook key and a
        read-only API key, and print the settings for the scheduler role
  taskiem canary probe [--count N]   run probes now with the TASKIEM_CANARY_* settings; non-zero if one fails
  taskiem canary run                 probe every TASKIEM_CANARY_INTERVAL from outside the cluster,
                                     with metrics on TASKIEM_METRICS_LISTEN
`

// canaryCmd sets up and runs the synthetic probe (docs/reliability.md).
func canaryCmd(ctx context.Context, args []string, stdout io.Writer) error {
	if len(args) == 0 {
		return errors.New(canaryUsage)
	}
	switch args[0] {
	case "setup":
		return canarySetup(ctx, args[1:], stdout)
	case "probe", "run":
		fs := flag.NewFlagSet("canary "+args[0], flag.ContinueOnError)
		count := fs.Int("count", 1, "probes to run")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		c, err := canaryConfig()
		if err != nil {
			return err
		}
		if !c.On() {
			return errors.New("canary: set TASKIEM_CANARY_HOOK_URL, TASKIEM_CANARY_SECRET, TASKIEM_CANARY_API_URL and TASKIEM_CANARY_API_KEY (taskiem canary setup prints them)")
		}
		log := slog.New(slog.NewJSONHandler(os.Stderr, nil))
		p := c.prober(log)
		if args[0] == "run" {
			mux := http.NewServeMux()
			mux.Handle("/metrics", promhttp.Handler())
			mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("ok")) })
			go func() { _ = httpTask("metrics", env("TASKIEM_METRICS_LISTEN", ":9090"), mux, log)(ctx) }()
			return p.Run(ctx, c.Interval)
		}
		failed := 0
		for i := 0; i < *count && ctx.Err() == nil; i++ {
			r := p.Probe(ctx)
			if r.OK {
				fmt.Fprintf(stdout, "ok      run %s accepted in %s, finished in %s\n", r.RunID, r.Accept.Round(time.Millisecond), r.Complete.Round(time.Millisecond))
			} else {
				failed++
				fmt.Fprintf(stdout, "FAILED  at %s (%s) run %s\n", r.Stage, r.Detail, r.RunID)
			}
		}
		if failed > 0 {
			return fmt.Errorf("canary: %d of %d probes failed", failed, *count)
		}
		return nil
	}
	return errors.New(canaryUsage)
}

// canarySetup uses an owner's API key of the internal canary tenant.
func canarySetup(ctx context.Context, args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("canary setup", flag.ContinueOnError)
	hooks := fs.String("hooks-url", strings.TrimRight(os.Getenv("TASKIEM_HOOKS_URL"), "/"), "where providers reach /hooks (default $TASKIEM_HOOKS_URL, else --url + /hooks)")
	envName := fs.String("env", "prod", "environment to publish in")
	remote := remoteFlags(fs)
	if err := fs.Parse(args); err != nil {
		return err
	}
	c, err := remote()
	if err != nil {
		return err
	}
	var me struct {
		TenantID string `json:"tenant_id"`
	}
	if err := c.do(ctx, "GET", "/v1/me", nil, &me); err != nil {
		return err
	}
	if me.TenantID == "" {
		return errors.New("canary setup: the key belongs to no tenant")
	}
	wf, err := c.workflowByKey(ctx, canary.WorkflowID)
	if err != nil {
		var created struct {
			ID string `json:"id"`
		}
		if err := c.do(ctx, "POST", "/v1/workflows", map[string]any{"name": "Taskiem canary", "definition": json.RawMessage(canary.Definition)}, &created); err != nil {
			return fmt.Errorf("canary setup: creating the workflow: %w", err)
		}
		wf = created.ID
		fmt.Fprintf(stdout, "created workflow %s (%s)\n", canary.WorkflowID, wf)
	} else {
		fmt.Fprintf(stdout, "workflow %s exists (%s): issuing a new webhook key and API key\n", canary.WorkflowID, wf)
	}
	raw := make([]byte, 32)
	_, _ = rand.Read(raw)
	secret := hex.EncodeToString(raw)
	if err := c.do(ctx, "PUT", "/v1/secrets/"+*envName+"/webhook_"+canary.WorkflowID, map[string]any{"value": secret}, nil); err != nil {
		return fmt.Errorf("canary setup: storing the webhook key: %w", err)
	}
	if err := c.do(ctx, "POST", "/v1/workflows/"+wf+"/versions/1/publish", nil, nil); err != nil {
		return fmt.Errorf("canary setup: publishing: %w", err)
	}
	var key struct {
		Key string `json:"key"`
	}
	if err := c.do(ctx, "POST", "/v1/api-keys", map[string]any{"name": "taskiem-canary", "permissions": []string{"run.read"}, "environment": *envName, "expires_days": 365}, &key); err != nil {
		return fmt.Errorf("canary setup: creating the read-only key: %w", err)
	}
	h := *hooks
	if h == "" {
		h = c.base + "/hooks"
	}
	fmt.Fprintf(stdout, `
Put these in the scheduler role's Secret (or the environment of taskiem canary run).
The key expires in 365 days: run setup again before then.

TASKIEM_CANARY_HOOK_URL=%s/%s%s?env=%s
TASKIEM_CANARY_SECRET=%s
TASKIEM_CANARY_API_URL=%s
TASKIEM_CANARY_API_KEY=%s
`, h, me.TenantID, canary.HookPath, url.QueryEscape(*envName), secret, c.base, key.Key)
	return nil
}
