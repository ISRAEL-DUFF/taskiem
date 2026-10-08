package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/embed"
)

const partnerUsage = `usage: taskiem tenants partner TENANT_ID [--max-subtenants N] [--subtenant-runs-per-day N] [--subtenant-runs-per-month N]
                               [--capabilities white_label,custom_domains] [--disable]

Makes a tenant a partner, which may create sub-tenants and embed apps
through the partner admin API (docs/embedding.md), and sets its
partner-wide caps (0: no cap; a flag left out keeps its value). --disable
stops it being a partner: its sub-tenants stay, unreachable, and their
end-user tokens stop working. A sub-tenant cannot be a partner.
--capabilities sets what the partner's plan includes, replacing the list
("" for none): white_label (apps may leave out the platform's branding)
and custom_domains (apps may be served on the partner's own domains).
Every change is audited`

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
	capsFlag := fs.String("capabilities", "\x00", "")
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
	var caps []string
	setCaps := *capsFlag != "\x00"
	if setCaps {
		caps = []string{}
		for _, c := range strings.Split(*capsFlag, ",") {
			if c = strings.TrimSpace(c); c == "" {
				continue
			}
			if !slices.Contains(embed.Capabilities, c) {
				return fmt.Errorf("tenants partner: unknown capability %q (one of %s)", c, strings.Join(embed.Capabilities, ", "))
			}
			caps = append(caps, c)
		}
	}
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
	var limits [3]int64
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
		} else {
			if setCaps {
				if _, err := tx.Exec(ctx, `SELECT taskiem_set_partner_capabilities($1, $2, $3)`, tenant, caps, by); err != nil {
					return err
				}
			}
			if err := tx.QueryRow(ctx, `SELECT max_subtenants, subtenant_runs_per_day, subtenant_runs_per_month, capabilities FROM partners WHERE tenant_id = $1`, tenant).
				Scan(&limits[0], &limits[1], &limits[2], &caps); err != nil {
				return err
			}
		}
		detail, _ := json.Marshal(map[string]any{"max_subtenants": limits[0], "subtenant_runs_per_day": limits[1], "subtenant_runs_per_month": limits[2],
			"capabilities": caps})
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
	capList := strings.Join(caps, ",")
	if capList == "" {
		capList = "none"
	}
	fmt.Fprintf(stdout, "tenant %s is a partner: max_subtenants %d, subtenant_runs_per_day %d, subtenant_runs_per_month %d (0: no cap); capabilities %s\n",
		tenant, limits[0], limits[1], limits[2], capList)
	return nil
}
