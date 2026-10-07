package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/db"
)

const partnerUsage = `usage: taskiem tenants partner TENANT_ID [--max-subtenants N] [--subtenant-runs-per-day N] [--subtenant-runs-per-month N] [--disable]

Makes a tenant a partner, which may create sub-tenants and embed apps
through the partner admin API (docs/embedding.md), and sets its
partner-wide caps (0: no cap; a flag left out keeps its value). --disable
stops it being a partner: its sub-tenants stay, unreachable, and their
end-user tokens stop working. A sub-tenant cannot be a partner. Every change
is audited`

// partnerCmd is the operator's switch for partners (spec 13.1, 16).
func partnerCmd(ctx context.Context, args []string, stdout io.Writer) error {
	if len(args) < 1 {
		return errors.New(partnerUsage)
	}
	tenant, err := uuid.Parse(args[0])
	if err != nil {
		return fmt.Errorf("tenants partner: %q is not a tenant id\n%s", args[0], partnerUsage)
	}
	fs := flag.NewFlagSet("tenants partner", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	maxSubs := fs.Int("max-subtenants", -1, "")
	perDay := fs.Int64("subtenant-runs-per-day", -1, "")
	perMonth := fs.Int64("subtenant-runs-per-month", -1, "")
	disable := fs.Bool("disable", false, "")
	if err := fs.Parse(args[1:]); err != nil || fs.NArg() > 0 {
		return fmt.Errorf("tenants partner: bad arguments\n%s", partnerUsage)
	}
	opt := func(v int64) *int64 {
		if v < 0 {
			return nil
		}
		return &v
	}
	var subs *int64
	if *maxSubs >= 0 {
		v := int64(*maxSubs)
		subs = &v
	}
	day, month := opt(*perDay), opt(*perMonth)
	cfg, err := loadConfig()
	if err != nil {
		return fmt.Errorf("tenants partner: %w", err)
	}
	pool, err := openPool(ctx, cfg)
	if err != nil {
		return fmt.Errorf("tenants partner: %w", err)
	}
	defer pool.Close()
	by := "cli:" + env("USER", "operator")
	var caps [3]int64
	err = db.InTenantTx(ctx, pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		var exists bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM tenants WHERE id = $1)`, tenant).Scan(&exists); err != nil {
			return err
		}
		if !exists {
			return fmt.Errorf("no tenant %s", tenant)
		}
		if _, err := tx.Exec(ctx, `SELECT taskiem_set_partner($1, $2, $3, $4, $5, $6)`, tenant, !*disable, subs, day, month, by); err != nil {
			return err
		}
		action := "partner.enable"
		if *disable {
			action = "partner.disable"
		} else if err := tx.QueryRow(ctx, `SELECT max_subtenants, subtenant_runs_per_day, subtenant_runs_per_month FROM partners WHERE tenant_id = $1`, tenant).
			Scan(&caps[0], &caps[1], &caps[2]); err != nil {
			return err
		}
		detail, _ := json.Marshal(map[string]any{"max_subtenants": caps[0], "subtenant_runs_per_day": caps[1], "subtenant_runs_per_month": caps[2]})
		_, err := tx.Exec(ctx, `SELECT taskiem_audit_append($1, 'system', $2, $3, $4, $5)`, tenant, by, action, tenant.String(), detail)
		return err
	})
	if err != nil {
		return fmt.Errorf("tenants partner: %w", err)
	}
	if *disable {
		fmt.Fprintf(stdout, "tenant %s is no longer a partner\n", tenant)
		return nil
	}
	fmt.Fprintf(stdout, "tenant %s is a partner: max_subtenants %d, subtenant_runs_per_day %d, subtenant_runs_per_month %d (0: no cap)\n",
		tenant, caps[0], caps[1], caps[2])
	return nil
}
