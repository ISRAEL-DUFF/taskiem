package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"sort"
	"strings"
	"text/tabwriter"

	"github.com/google/uuid"

	"github.com/israel-duff/taskiem/engine/runtime"
)

const tenantsUsage = `usage: taskiem tenants limits TENANT_ID [--set KEY=VALUE]...

Shows a tenant's plan limits and usage; --set changes one (repeatable).
VALUE is a number, or "default" for the platform default (TASKIEM_DEFAULT_*).
Tenants cannot change their own limits; every change is audited`

// setFlags collects repeated --set KEY=VALUE.
type setFlags map[string]any

func (s setFlags) String() string { return "" }

func (s setFlags) Set(v string) error {
	k, val, ok := strings.Cut(v, "=")
	if !ok {
		return fmt.Errorf("%q: want KEY=VALUE", v)
	}
	parsed, err := runtime.ParseLimit(strings.TrimSpace(k), val)
	if err != nil {
		return err
	}
	s[strings.TrimSpace(k)] = parsed
	return nil
}

// tenantsCmd is the operator's view of tenants' plan limits (spec 16).
func tenantsCmd(ctx context.Context, args []string, stdout io.Writer) error {
	if len(args) > 0 && args[0] == "partner" {
		return partnerCmd(ctx, args[1:], stdout)
	}
	if len(args) < 2 || args[0] != "limits" {
		return errors.New(tenantsUsage)
	}
	tenant, err := uuid.Parse(args[1])
	if err != nil {
		return fmt.Errorf("tenants limits: %q is not a tenant id\n%s", args[1], tenantsUsage)
	}
	sets := setFlags{}
	fs := flag.NewFlagSet("tenants limits", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.Var(sets, "set", "KEY=VALUE (repeatable)")
	if err := fs.Parse(args[2:]); err != nil {
		return fmt.Errorf("tenants limits: %w\n%s", err, tenantsUsage)
	}
	if fs.NArg() > 0 {
		return errors.New(tenantsUsage)
	}
	cfg, err := loadConfig()
	if err != nil {
		return fmt.Errorf("tenants limits: %w", err)
	}
	pool, err := openPool(ctx, cfg)
	if err != nil {
		return fmt.Errorf("tenants limits: %w", err)
	}
	defer pool.Close()
	store := &runtime.Store{Pool: pool, Defaults: &cfg.Limits}
	if len(sets) > 0 {
		if err := store.SetLimits(ctx, tenant, sets, "cli:"+env("USER", "operator")); err != nil {
			return fmt.Errorf("tenants limits: %w", err)
		}
		keys := make([]string, 0, len(sets))
		for k := range sets {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		fmt.Fprintf(stdout, "updated %s for tenant %s (running processes pick it up within a minute)\n\n", strings.Join(keys, ", "), tenant)
	}
	v, err := store.ViewLimits(ctx, tenant)
	if err != nil {
		return fmt.Errorf("tenants limits: %w", err)
	}
	limits := limitsMap(v.Limits)
	tw := tabwriter.NewWriter(stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "LIMIT\tVALUE\tFROM")
	for _, k := range runtime.LimitKeys {
		from := "default"
		if _, ok := v.Overrides[k.Key]; ok {
			from = "tenant"
		}
		fmt.Fprintf(tw, "%s\t%v\t%s\n", k.Key, limits[k.Key], from)
	}
	_ = tw.Flush()
	u := v.Usage
	fmt.Fprintf(stdout, "\nusage: %d runs today, %d this month; %d running, %d queued; %d workflows, %d secrets, %d connections; %d AI tokens this month\n",
		u.RunsToday, u.RunsThisMonth, u.RunningRuns, u.QueuedRuns, u.Workflows, u.Secrets, u.Connections, u.AITokens)
	for _, h := range v.Hits {
		fmt.Fprintf(stdout, "reached %s on %s (%d times)\n", h.Limit, h.Day, h.Hits)
	}
	return nil
}

func limitsMap(l runtime.Limits) map[string]any {
	return map[string]any{
		"ingest_rate": l.IngestRate, "ingest_burst": l.IngestBurst, "ingest_ceiling": l.IngestCeiling, "ingest_ceiling_burst": l.IngestCeilingBurst,
		"max_running_runs": l.MaxRunningRuns, "max_queued_runs": l.MaxQueuedRuns, "runs_per_day": l.RunsPerDay, "runs_per_month": l.RunsPerMonth,
		"max_workflows": l.MaxWorkflows, "max_steps_per_run": l.MaxStepsPerRun, "worker_concurrency": l.WorkerConcurrency,
		"max_payload_bytes": l.MaxPayloadBytes, "max_secrets": l.MaxSecrets, "max_connections": l.MaxConnections,
		"ai_monthly_tokens": l.AIMonthlyTokens,
	}
}
