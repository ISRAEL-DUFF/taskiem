package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"slices"
	"sort"
	"time"

	"github.com/israel-duff/taskiem/connectors/iswallet"
	"github.com/israel-duff/taskiem/engine/connector"
	"github.com/israel-duff/taskiem/engine/egress"
)

const iswalletUsage = `usage: taskiem iswallet subscribe|status|unsubscribe [-mode live|test] [ID]

Taskiem's own iswallet client (decision 0052; docs/operations/iswallet-platform.md),
for the mode's key (TASKIEM_ISWALLET_CLIENT_KEY, or _TEST_CLIENT_KEY):
  subscribe     make the client's one subscription for the mode, every
                wallet event to TASKIEM_HOOKS_URL/platform/iswallet@1?mode=,
                and print its signing secret, shown once: set it as
                TASKIEM_ISWALLET_WEBHOOK_SECRET (or _TEST_WEBHOOK_SECRET)
  status ID     the subscription's state and iswallet's last delivery to us
  unsubscribe ID  remove it (after a new one is in place)`

// iswalletCmd is the operator's handle on the platform client's
// subscription: iswallet returns its signing secret once, at creation, so
// the operator keeps it as process configuration like the client's key.
func iswalletCmd(ctx context.Context, args []string, stdout io.Writer) error {
	if len(args) == 0 {
		return errors.New(iswalletUsage)
	}
	sub := args[0]
	fs := flag.NewFlagSet("iswallet", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	mode := fs.String("mode", "live", "live or test")
	if err := fs.Parse(args[1:]); err != nil {
		return errors.New(iswalletUsage)
	}
	rest := fs.Args()
	switch {
	case sub == "subscribe" && len(rest) == 0, (sub == "status" || sub == "unsubscribe") && len(rest) == 1:
	default:
		return errors.New(iswalletUsage)
	}
	if *mode != "live" && *mode != "test" {
		return errors.New(iswalletUsage)
	}
	cfg, err := loadConfig()
	if err != nil {
		return fmt.Errorf("iswallet: %w", err)
	}
	key := cfg.ISWallet.Keys[*mode]
	if key == "" {
		return fmt.Errorf("iswallet: no key for %s: set TASKIEM_ISWALLET%s_CLIENT_KEY", *mode, map[string]string{"live": "", "test": "_TEST"}[*mode])
	}
	c := iswallet.New(iswallet.Options{BaseURL: cfg.Connectors.IswalletURL, LiveURL: cfg.Connectors.IswalletURL, TestURL: cfg.Connectors.IswalletTestURL})
	creds := map[string]string{"api_key": key, connector.ModeKey: *mode}
	hc := (&egress.Guard{}).Client(egress.Policy{Tenant: "platform", Hosts: c.Manifest.HostsFor(creds), Purpose: "connector:iswallet"}, 20*time.Second)
	call := connector.RemoteCall{Credentials: creds, HTTP: hc}
	reg := c.Registrars["credit_event"]
	if reg == nil {
		return errors.New("iswallet: the connector has no registrar")
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	switch sub {
	case "subscribe":
		if cfg.HooksURL == "" {
			return errors.New("iswallet: set TASKIEM_HOOKS_URL (or TASKIEM_PUBLIC_URL): iswallet delivers to a public https address")
		}
		st, err := reg.Create(ctx, call, connector.RemoteSpec{Name: "taskiem-platform-" + *mode, URL: platformHookURL(cfg.HooksURL, *mode), Events: platformEvents(c.Manifest)})
		if err != nil {
			return fmt.Errorf("iswallet: subscribing: %w", err)
		}
		infix := map[string]string{"live": "", "test": "_TEST"}[*mode]
		_, _ = fmt.Fprintf(stdout, "subscription %s (%s) to %s\n\nIts signing secret, shown once. Set it on every process and restart them:\n\n  TASKIEM_ISWALLET%s_WEBHOOK_SECRET=%s\n",
			st.ID, st.Status, platformHookURL(cfg.HooksURL, *mode), infix, st.Secret)
		return nil
	case "status":
		st, err := reg.Get(ctx, call, rest[0])
		if err != nil {
			return fmt.Errorf("iswallet: reading %s: %w", rest[0], err)
		}
		_, _ = fmt.Fprintf(stdout, "subscription %s: %s\n", rest[0], st.Status)
		if st.Reason != "" {
			_, _ = fmt.Fprintln(stdout, st.Reason)
		}
		return nil
	default:
		if err := reg.Delete(ctx, call, rest[0]); err != nil {
			return fmt.Errorf("iswallet: removing %s: %w", rest[0], err)
		}
		_, _ = fmt.Fprintf(stdout, "subscription %s removed\n", rest[0])
		return nil
	}
}

// platformHookURL is where the platform client's subscription for a mode
// delivers.
func platformHookURL(hooks, mode string) string {
	return hooks + "/platform/iswallet@1?mode=" + mode
}

// platformEvents is every event the connector's routed triggers send: the
// platform's one subscription carries them all.
func platformEvents(m *connector.Manifest) []string {
	var out []string
	for _, t := range m.Triggers {
		if t.Route == nil {
			continue
		}
		for _, e := range t.Events {
			if !slices.Contains(out, e) {
				out = append(out, e)
			}
		}
	}
	sort.Strings(out)
	return out
}
