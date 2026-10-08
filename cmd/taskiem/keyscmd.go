package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"text/tabwriter"
	"time"

	"github.com/google/uuid"

	"github.com/israel-duff/taskiem/engine/byok"
	"github.com/israel-duff/taskiem/engine/egress"
	"github.com/israel-duff/taskiem/engine/runtime"
	"github.com/israel-duff/taskiem/engine/secrets"
)

const keysUsage = `usage: taskiem tenants keys TENANT_ID [status | rotate [--wait] | rewrap | check]

Shows a tenant's encryption keys: tenant key versions and what wraps them
(the platform KMS key, or the tenant's own key with bring your own key),
the customer key's health, re-wrapping progress and parked steps.

  rotate [--wait]  add a tenant key version; data keys are re-wrapped by the
                   scheduler's key job, or here with --wait
  rewrap           re-wrap now what is still under older versions
  check            check the tenant's keys now; on success, resume steps
                   parked while they were unavailable

Customer keys are brought, replaced and removed by the tenant's owners
(Settings > Encryption keys, or /v1/keys). Every action is audited in the
tenant's log as the operator; it needs the deployment's KMS settings`

// keysCmd is the operator's view of a tenant's keys (docs/byok.md).
func keysCmd(ctx context.Context, args []string, stdout io.Writer) error {
	if len(args) < 1 {
		return errors.New(keysUsage)
	}
	tenant, err := uuid.Parse(args[0])
	if err != nil {
		return fmt.Errorf("tenants keys: %q is not a tenant id\n%s", args[0], keysUsage)
	}
	action, wait := "status", false
	for _, a := range args[1:] {
		switch a {
		case "status", "rotate", "rewrap", "check":
			action = a
		case "--wait":
			wait = true
		default:
			return errors.New(keysUsage)
		}
	}
	if wait && action != "rotate" {
		return errors.New(keysUsage)
	}
	cfg, err := loadConfig()
	if err != nil {
		return fmt.Errorf("tenants keys: %w", err)
	}
	kms, err := cfg.kms()
	if err != nil {
		return fmt.Errorf("tenants keys: %w", err)
	}
	pool, err := openPool(ctx, cfg)
	if err != nil {
		return fmt.Errorf("tenants keys: %w", err)
	}
	defer pool.Close()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	if err := configureEgress(nil); err != nil {
		return fmt.Errorf("tenants keys: %w", err)
	}
	v := &secrets.Vault{Pool: pool, KMS: kms, RootKey: cfg.KMSKey, BYOKCacheTTL: cfg.Keys.CacheTTL, DestroyAfter: cfg.Keys.DestroyAfter,
		BYOK: &byok.Factory{Guard: &egress.Guard{Logger: log}, AllowPrivate: cfg.Keys.AllowPrivate}}
	by := "cli:" + env("USER", "operator")
	switch action {
	case "rotate":
		ver, err := v.Rotate(ctx, tenant, by)
		if err != nil {
			return fmt.Errorf("tenants keys rotate: %w", err)
		}
		fmt.Fprintf(stdout, "tenant key version %d is current; ", ver)
		if !wait {
			fmt.Fprintln(stdout, "the scheduler's key job re-wraps data keys onto it (or run: taskiem tenants keys "+tenant.String()+" rewrap)")
			fmt.Fprintln(stdout)
			break
		}
		fallthrough
	case "rewrap":
		r, err := v.RewrapAll(ctx, tenant)
		if err != nil {
			return fmt.Errorf("tenants keys rewrap: %w", err)
		}
		fmt.Fprintf(stdout, "re-wrapped %d data keys and %d subject keys onto version %d", r.Secrets, r.SubjectKeys, r.Version)
		if len(r.Retired) > 0 {
			fmt.Fprintf(stdout, "; retired versions %v", r.Retired)
		}
		if len(r.Destroyed) > 0 {
			fmt.Fprintf(stdout, "; destroyed versions %v", r.Destroyed)
		}
		fmt.Fprint(stdout, "\n\n")
	case "check":
		h, err := v.CheckKey(ctx, tenant, by)
		if err != nil {
			return fmt.Errorf("tenants keys check: %w", err)
		}
		if !h.OK {
			fmt.Fprintf(stdout, "the tenant key does not work: %s\n\n", h.Error)
			break
		}
		store := &runtime.Store{Pool: pool, Defaults: &cfg.Limits, Billing: cfg.Billing.On}
		n, err := store.ResumeKeyParked(ctx, tenant)
		if err != nil {
			return fmt.Errorf("tenants keys check: resume: %w", err)
		}
		fmt.Fprintf(stdout, "the tenant key works; resumed %d parked steps\n\n", n)
	}
	st, err := v.KeyStatus(ctx, tenant)
	if err != nil {
		return fmt.Errorf("tenants keys: %w", err)
	}
	printKeyStatus(stdout, st)
	return nil
}

func printKeyStatus(w io.Writer, st secrets.KeyStatus) {
	fmt.Fprintf(w, "mode: %s; current version %d; unwrapped keys cached for %s with a customer key\n", st.Mode, st.CurrentVersion, time.Duration(st.CacheTTLSeconds)*time.Second)
	if k := st.BYOK; k != nil {
		fmt.Fprintf(w, "customer key: %s, %s (%s); credentials %s\n", k.Description, k.Status, k.Provider, k.CredentialsDigest)
		if k.LastOKAt != nil {
			fmt.Fprintf(w, "  last worked %s\n", k.LastOKAt.UTC().Format(time.RFC3339))
		}
		if k.FailingSince != nil {
			fmt.Fprintf(w, "  failing since %s (%d checks): %s\n", k.FailingSince.UTC().Format(time.RFC3339), k.CheckFailures, k.LastError)
		}
	}
	for _, k := range st.RetiredBYOK {
		since := ""
		if k.RetiredAt != nil {
			since = " since " + k.RetiredAt.UTC().Format(time.RFC3339)
		}
		fmt.Fprintf(w, "retired customer key: %s%s\n", k.Description, since)
	}
	if r := st.Rewrap; r != nil && r.Phase == "destroy" {
		fmt.Fprintf(w, "older versions are retired; their wrapped material is destroyed after %s\n", r.NotBefore.UTC().Format(time.RFC3339))
	} else if r != nil {
		fmt.Fprintf(w, "re-wrapping (%s, since %s): %d data keys and %d subject keys left", r.Reason, r.Since.UTC().Format(time.RFC3339), r.Secrets, r.Subjects)
		if r.LastError != "" {
			fmt.Fprintf(w, "; last error: %s", r.LastError)
		}
		fmt.Fprintln(w)
	}
	if st.ParkedSteps > 0 {
		fmt.Fprintf(w, "parked steps waiting for the key: %d\n", st.ParkedSteps)
	}
	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "\nVERSION\tWRAPPED BY\tSTATE\tSECRETS\tSUBJECT KEYS\tCREATED")
	for _, v := range st.Versions {
		state := "current"
		switch {
		case v.DestroyedAt != nil:
			state = "destroyed"
		case v.RetiredAt != nil:
			state = "retired"
		case v.Version != st.CurrentVersion:
			state = "being replaced"
		}
		fmt.Fprintf(tw, "%d\t%s\t%s\t%d\t%d\t%s\n", v.Version, v.WrappedBy, state, v.Secrets, v.SubjectKeys, v.CreatedAt.UTC().Format(time.RFC3339))
	}
	_ = tw.Flush()
}
