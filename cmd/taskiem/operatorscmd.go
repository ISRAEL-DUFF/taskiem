package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/israel-duff/taskiem/api"
	"github.com/israel-duff/taskiem/engine/ops"
)

const operatorsUsage = `usage:
  taskiem operators
      list operator accounts (the operator console, docs/operator-console.md)
  taskiem operators add EMAIL [--name NAME] [--ttl 24h]
      create an operator (or reactivate one) and print a one-time link to enrol a passkey
  taskiem operators enrol EMAIL [--ttl 24h]
      a new enrolment link: another passkey, or after a reset
  taskiem operators reset EMAIL
      remove an operator's passkeys and end their sessions (a lost device); then enrol again
  taskiem operators disable EMAIL
      stop an operator: their sessions and unused links end at once
Accounts are made here only, never through the API. Every change is recorded in the platform audit chain`

// operatorsCmd manages operator accounts (decision 0027).
func operatorsCmd(ctx context.Context, args []string, stdout io.Writer) error {
	cfg, err := loadConfig()
	if err != nil {
		return fmt.Errorf("operators: %w", err)
	}
	pool, err := openPool(ctx, cfg)
	if err != nil {
		return fmt.Errorf("operators: %w", err)
	}
	defer pool.Close()
	by := "cli:" + env("USER", "operator")
	if len(args) == 0 {
		list, err := ops.List(ctx, pool)
		if err != nil {
			return fmt.Errorf("operators: %w", err)
		}
		tw := tabwriter.NewWriter(stdout, 0, 4, 2, ' ', 0)
		fmt.Fprintln(tw, "EMAIL\tNAME\tSTATUS\tPASSKEYS\tSSO\tLAST SIGN-IN\tCREATED BY")
		for _, o := range list {
			last := "never"
			if o.LastSignIn != nil {
				last = o.LastSignIn.UTC().Format("2006-01-02 15:04")
			}
			fmt.Fprintf(tw, "%s\t%s\t%s\t%d\t%v\t%s\t%s\n", o.Email, o.Name, o.Status, o.Passkeys, o.SSO, last, o.CreatedBy)
		}
		return tw.Flush()
	}
	if len(args) < 2 {
		return errors.New(operatorsUsage)
	}
	verb, email := args[0], args[1]
	fs := flag.NewFlagSet("operators "+verb, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	name := fs.String("name", "", "the operator's name")
	ttl := fs.Duration("ttl", ops.EnrolTTL, "how long the enrolment link lasts (at most 7 days)")
	if err := fs.Parse(args[2:]); err != nil || fs.NArg() > 0 {
		return errors.New(operatorsUsage)
	}
	if *ttl <= 0 || *ttl > 7*24*time.Hour {
		return errors.New("operators: --ttl is between 1s and 168h")
	}
	switch verb {
	case "add":
		if _, err := ops.Add(ctx, pool, email, *name, by); err != nil {
			return fmt.Errorf("operators add: %w", err)
		}
		fmt.Fprintf(stdout, "operator %s is active\n", strings.ToLower(email))
		return printEnrol(ctx, stdout, email, cfg.PublicURL, *ttl, by, pool)
	case "enrol":
		return printEnrol(ctx, stdout, email, cfg.PublicURL, *ttl, by, pool)
	case "reset":
		n, err := ops.Reset(ctx, pool, email, by)
		if err != nil {
			return fmt.Errorf("operators reset: %w", err)
		}
		fmt.Fprintf(stdout, "removed %d passkey(s) of %s and ended their sessions; send a new link with taskiem operators enrol\n", n, strings.ToLower(email))
		return nil
	case "disable":
		if err := ops.Disable(ctx, pool, email, by); err != nil {
			return fmt.Errorf("operators disable: %w", err)
		}
		fmt.Fprintf(stdout, "operator %s is disabled; their sessions have ended\n", strings.ToLower(email))
		return nil
	}
	return errors.New(operatorsUsage)
}

func printEnrol(ctx context.Context, stdout io.Writer, email, publicURL string, ttl time.Duration, by string, pool *pgxpool.Pool) error {
	link, err := ops.Enrol(ctx, pool, email, publicURL, ttl, by)
	if err != nil {
		return fmt.Errorf("operators enrol: %w", err)
	}
	fmt.Fprintf(stdout, "enrolment link (one use, valid %s; give it to %s only, over a channel you trust):\n%s\n", ttl, strings.ToLower(email), link)
	return nil
}

// opsSettings is the operator console for the api role (TASKIEM_OPS_*);
// nil when TASKIEM_OPS_CONSOLE is false.
func opsSettings() (*api.OpsSettings, error) {
	if !envBool("TASKIEM_OPS_CONSOLE", true) {
		return nil, nil
	}
	s := &api.OpsSettings{}
	if v := os.Getenv("TASKIEM_OPS_SESSION_TTL"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil || d < 5*time.Minute || d > 8*time.Hour {
			return nil, fmt.Errorf("TASKIEM_OPS_SESSION_TTL must be a duration between 5m and 8h, not %q", v)
		}
		s.SessionTTL = d
	}
	if iss := os.Getenv("TASKIEM_OPS_OIDC_ISSUER"); iss != "" {
		c := &api.OpsOIDC{Issuer: strings.TrimRight(iss, "/"), ClientID: os.Getenv("TASKIEM_OPS_OIDC_CLIENT_ID"),
			ClientSecret: os.Getenv("TASKIEM_OPS_OIDC_CLIENT_SECRET"), Name: env("TASKIEM_OPS_OIDC_NAME", "single sign-on")}
		if !strings.HasPrefix(c.Issuer, "https://") || c.ClientID == "" || c.ClientSecret == "" {
			return nil, errors.New("TASKIEM_OPS_OIDC_ISSUER must be an https URL, with TASKIEM_OPS_OIDC_CLIENT_ID and TASKIEM_OPS_OIDC_CLIENT_SECRET")
		}
		s.OIDC = c
	}
	return s, nil
}
