package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"text/tabwriter"

	"github.com/google/uuid"

	"github.com/israel-duff/taskiem/engine/runtime"
)

const poolsUsage = `usage:
  taskiem pools
      live workers by pool and queue, queue depth by pool, and every routing
  taskiem pools assign POOL (--tenant TENANT_ID | --plan PLAN) [--force]
      route a tenant's work (and its sub-tenants') or a plan's tenants to a dedicated worker pool;
      refused while no live worker serves POOL unless --force; audited
  taskiem pools unassign (--tenant TENANT_ID | --plan PLAN)
      remove that routing: back to the partner's, the plan's, or the shared pool; audited`

// poolsCmd routes tenants to dedicated worker pools (decision 0024).
func poolsCmd(ctx context.Context, args []string, stdout io.Writer) error {
	cmd := "list"
	if len(args) > 0 {
		cmd, args = args[0], args[1:]
	}
	var pool string
	if cmd == "assign" {
		if len(args) == 0 {
			return errors.New(poolsUsage)
		}
		pool, args = args[0], args[1:]
		if !runtime.ValidPool(pool) {
			return fmt.Errorf("pools assign: %q is not a pool name (lowercase letters, digits and dashes, up to 32)", pool)
		}
	}
	fs := flag.NewFlagSet("pools", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	tenantFlag := fs.String("tenant", "", "tenant id")
	plan := fs.String("plan", "", "plan id")
	force := fs.Bool("force", false, "assign even though no live worker serves the pool")
	if err := fs.Parse(args); err != nil || fs.NArg() > 0 {
		return errors.New(poolsUsage)
	}
	switch cmd {
	case "list":
		if *tenantFlag != "" || *plan != "" || *force {
			return errors.New(poolsUsage)
		}
	case "assign", "unassign":
		if (*tenantFlag == "") == (*plan == "") || (cmd == "unassign" && *force) {
			return errors.New(poolsUsage)
		}
	default:
		return errors.New(poolsUsage)
	}
	var tenant uuid.UUID
	if *tenantFlag != "" {
		var err error
		if tenant, err = uuid.Parse(*tenantFlag); err != nil {
			return fmt.Errorf("pools: %q is not a tenant id", *tenantFlag)
		}
	}
	cfg, err := loadConfig()
	if err != nil {
		return fmt.Errorf("pools: %w", err)
	}
	dbPool, err := openPool(ctx, cfg)
	if err != nil {
		return fmt.Errorf("pools: %w", err)
	}
	defer dbPool.Close()
	store := &runtime.Store{Pool: dbPool, Defaults: &cfg.Limits, Billing: cfg.Billing.On}
	by := "cli:" + env("USER", "operator")
	switch {
	case cmd == "list":
		return poolsList(ctx, store, stdout)
	case *plan != "":
		if err := store.AssignPlanPool(ctx, *plan, pool, by, *force); err != nil {
			return fmt.Errorf("pools %s: %w", cmd, err)
		}
		if pool == "" {
			fmt.Fprintf(stdout, "plan %s: routing removed; its tenants go to the shared pool unless routed themselves (recorded)\n", *plan)
		} else {
			fmt.Fprintf(stdout, "plan %s: its tenants' work goes to pool %s unless routed themselves (recorded); queued tasks move at once\n", *plan, pool)
		}
	default:
		if err := store.AssignTenantPool(ctx, tenant, pool, by, *force); err != nil {
			return fmt.Errorf("pools %s: %w", cmd, err)
		}
		now, err := store.TenantPool(ctx, tenant)
		if err != nil {
			return fmt.Errorf("pools %s: %w", cmd, err)
		}
		fmt.Fprintf(stdout, "tenant %s: work now goes to pool %s (audited); queued tasks move at once, running steps finish where they are\n", tenant, now)
	}
	return nil
}

func poolsList(ctx context.Context, store *runtime.Store, stdout io.Writer) error {
	live, err := store.LiveWorkers(ctx)
	if err != nil {
		return fmt.Errorf("pools: %w", err)
	}
	depth, err := store.PoolStats(ctx)
	if err != nil {
		return fmt.Errorf("pools: %w", err)
	}
	routes, err := store.PoolAssignments(ctx)
	if err != nil {
		return fmt.Errorf("pools: %w", err)
	}
	tw := tabwriter.NewWriter(stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "POOL\tQUEUE\tWORKERS\tREADY\tLEASED\tOLDEST READY")
	type key struct{ pool, queue string }
	seen := map[key]bool{}
	workers := map[key]int{}
	for _, l := range live {
		workers[key{l.Pool, l.Queue}] = l.Workers
	}
	for _, d := range depth {
		k := key{d.Pool, d.Queue}
		seen[k] = true
		warn := ""
		if workers[k] == 0 && d.Ready > 0 {
			warn = "  (no live worker)"
		}
		fmt.Fprintf(tw, "%s\t%s\t%d\t%d\t%d\t%.0fs%s\n", d.Pool, d.Queue, workers[k], d.Ready, d.Leased, d.OldestSeconds, warn)
	}
	for _, l := range live {
		if !seen[key{l.Pool, l.Queue}] {
			fmt.Fprintf(tw, "%s\t%s\t%d\t0\t0\t0s\n", l.Pool, l.Queue, l.Workers)
		}
	}
	_ = tw.Flush()
	fmt.Fprintln(stdout)
	if len(routes) == 0 {
		fmt.Fprintln(stdout, "no routing: every tenant is in the shared pool")
		return nil
	}
	tw = tabwriter.NewWriter(stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "ROUTED\tPOOL\tBY\tSINCE")
	for _, r := range routes {
		who := "plan " + r.Plan
		if r.Tenant != nil {
			who = "tenant " + r.Tenant.String()
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", who, r.Pool, r.AssignedBy, r.AssignedAt.UTC().Format("2006-01-02 15:04"))
	}
	return tw.Flush()
}
