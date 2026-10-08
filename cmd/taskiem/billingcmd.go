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
	"time"

	"github.com/google/uuid"

	"github.com/israel-duff/taskiem/engine/billing"
	"github.com/israel-duff/taskiem/engine/runtime"
)

const billingUsage = `usage:
  taskiem billing plans [--file FILE] [--load]
      validate the plan catalogue (default $TASKIEM_BILLING_PLANS or deploy/plans.yaml) and print it;
      --load writes it to the database (plans missing from the file are retired, never deleted)
  taskiem billing grant TENANT_ID PLAN [--until YYYY-MM-DD]
      put a tenant on a plan without payment (design partners, comps; self_hosted for internal tenants);
      audited in the tenant's chain`

// billingCmd is the operator's side of billing (docs/billing.md).
func billingCmd(ctx context.Context, args []string, stdout io.Writer) error {
	if len(args) == 0 {
		return errors.New(billingUsage)
	}
	switch args[0] {
	case "plans":
		return billingPlans(ctx, args[1:], stdout)
	case "grant":
		return billingGrant(ctx, args[1:], stdout)
	}
	return errors.New(billingUsage)
}

func plansFile(flagValue string) string {
	if flagValue != "" {
		return flagValue
	}
	return env("TASKIEM_BILLING_PLANS", "deploy/plans.yaml")
}

func billingPlans(ctx context.Context, args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("billing plans", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	file := fs.String("file", "", "plan catalogue")
	load := fs.Bool("load", false, "write the catalogue to the database")
	if err := fs.Parse(args); err != nil || fs.NArg() > 0 {
		return errors.New(billingUsage)
	}
	path := plansFile(*file)
	c, err := billing.LoadConfig(path)
	if err != nil {
		return fmt.Errorf("billing plans: %s: %w", path, err)
	}
	printPlans(stdout, c)
	if c.PlaceholderPrices {
		fmt.Fprintln(stdout, "\nWARNING: prices are placeholders pending decision B1 (docs/needs-people.md); set real ones and placeholder_prices: false")
	}
	if !*load {
		fmt.Fprintf(stdout, "\n%s is valid (--load writes it to the database)\n", path)
		return nil
	}
	cfg, err := loadConfig()
	if err != nil {
		return fmt.Errorf("billing plans: %w", err)
	}
	pool, err := openPool(ctx, cfg)
	if err != nil {
		return fmt.Errorf("billing plans: %w", err)
	}
	defer pool.Close()
	if err := billing.Store(ctx, pool, c, "cli:"+env("USER", "operator")); err != nil {
		return fmt.Errorf("billing plans: %w", err)
	}
	fmt.Fprintf(stdout, "\nloaded %d plans (and %s) from %s\n", len(c.Plans), billing.InternalPlanID, path)
	return nil
}

func printPlans(w io.Writer, c *billing.Config) {
	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "PLAN\tTIER\tMONTHLY\tANNUAL\tPUBLIC\tFEATURES\tLIMITS")
	for _, p := range c.Plans {
		var feats []string
		for _, f := range billing.AllFeatures {
			if p.Has(f) {
				feats = append(feats, f)
			}
		}
		keys := make([]string, 0, len(p.Limits))
		for k := range p.Limits {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		lims := make([]string, len(keys))
		for i, k := range keys {
			lims[i] = fmt.Sprintf("%s=%v", k, p.Limits[k])
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%v\t%s\t%s\n", p.ID, p.Tier, billing.Naira(p.MonthlyKobo), billing.Naira(p.AnnualKobo), p.Public,
			strings.Join(feats, ","), strings.Join(lims, " "))
	}
	_ = tw.Flush()
	fmt.Fprintf(w, "\nVAT %.2f%% · trial %d days on %s · grace %d days · dunning on days %v · invoices %s-YYYY-NNNNNN\n",
		c.VATPercent, c.TrialDays, c.TrialPlan, c.GraceDays, c.DunningDays, c.InvoicePrefix)
}

func billingGrant(ctx context.Context, args []string, stdout io.Writer) error {
	if len(args) < 2 {
		return errors.New(billingUsage)
	}
	tenant, err := uuid.Parse(args[0])
	if err != nil {
		return fmt.Errorf("billing grant: %q is not a tenant id\n%s", args[0], billingUsage)
	}
	plan := args[1]
	fs := flag.NewFlagSet("billing grant", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	until := fs.String("until", "", "YYYY-MM-DD (UTC); empty: indefinitely")
	file := fs.String("file", "", "plan catalogue")
	if err := fs.Parse(args[2:]); err != nil || fs.NArg() > 0 {
		return errors.New(billingUsage)
	}
	var end *time.Time
	if *until != "" {
		t, err := time.Parse("2006-01-02", *until)
		if err != nil {
			return fmt.Errorf("billing grant: --until %q: want YYYY-MM-DD", *until)
		}
		end = &t
	}
	c, err := billing.LoadConfig(plansFile(*file))
	if err != nil {
		return fmt.Errorf("billing grant: %w", err)
	}
	cfg, err := loadConfig()
	if err != nil {
		return fmt.Errorf("billing grant: %w", err)
	}
	pool, err := openPool(ctx, cfg)
	if err != nil {
		return fmt.Errorf("billing grant: %w", err)
	}
	defer pool.Close()
	svc := &billing.Service{Pool: pool, Store: &runtime.Store{Pool: pool, Defaults: &cfg.Limits, Billing: true}, Config: c, Enabled: true}
	if err := svc.Grant(ctx, tenant, plan, end, "cli:"+env("USER", "operator")); err != nil {
		return fmt.Errorf("billing grant: %w", err)
	}
	when := "indefinitely"
	if end != nil {
		when = "until " + end.Format("2 Jan 2006")
	}
	fmt.Fprintf(stdout, "tenant %s is on %s %s, without payment (audited; running processes pick it up within a minute)\n", tenant, plan, when)
	return nil
}
