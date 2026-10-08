package whatsapp

import (
	"context"
	"errors"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/israel-duff/taskiem/engine/db"
)

// Template cost accounting (spec 16). Meta charges per template message
// delivered, by category (marketing, utility, authentication) and the
// recipient's country; free-form messages inside the 24-hour window are
// free, and so are utility templates sent inside it. Taskiem sends a
// template only outside the window, so every template it sends is
// counted: per tenant, UTC month and category, against the plan's
// allowance (whatsapp_templates_monthly). Beyond the allowance, templates
// are still sent and counted as overage for pass-through billing, except
// marketing ones, which are held back: approvals, step-up links, codes and
// alerts never wait on a bill.

// ErrTemplateBlocked: a blockable template beyond the allowance was not
// sent.
var ErrTemplateBlocked = errors.New("whatsapp: this month's template allowance is used up; marketing messages are held back")

// Meter counts templates (spec 16).
type Meter interface {
	// Allow reports whether a template may be sent now for the tenant.
	Allow(ctx context.Context, tenant uuid.UUID, t Template) (bool, error)
	// Record counts a template sent.
	Record(ctx context.Context, tenant uuid.UUID, t Template) error
}

// Category is a template's category as counted: authentication, utility
// or marketing.
func Category(t Template) string {
	switch c := strings.ToLower(t.Category); c {
	case "authentication", "marketing":
		return c
	}
	return "utility"
}

// Blockable reports whether a template may be held back beyond the
// allowance: marketing only.
func Blockable(t Template) bool { return Category(t) == "marketing" }

// UsageMeter keeps the counts in whatsapp_template_usage.
type UsageMeter struct {
	Pool *pgxpool.Pool
	// Allowance is a tenant's monthly allowance (0: none), from its plan
	// limits (sub-tenants inherit their partner's).
	Allowance func(ctx context.Context, tenant uuid.UUID) (int64, error)
}

const usageMonth = `date_trunc('month', now() AT TIME ZONE 'UTC')::date`

// Allow holds back a blockable template once the month's templates reach
// the allowance, and counts it as held back.
func (m *UsageMeter) Allow(ctx context.Context, tenant uuid.UUID, t Template) (bool, error) {
	if !Blockable(t) {
		return true, nil
	}
	allowance, err := m.Allowance(ctx, tenant)
	if err != nil || allowance <= 0 {
		return err == nil, err
	}
	ok := true
	err = db.InTenantTx(ctx, m.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		var used int64
		if err := tx.QueryRow(ctx, `SELECT COALESCE(sum(sent), 0)::bigint FROM whatsapp_template_usage WHERE tenant_id = $1 AND month = `+usageMonth, tenant).Scan(&used); err != nil {
			return err
		}
		if used < allowance {
			return nil
		}
		ok = false
		_, err := tx.Exec(ctx, `INSERT INTO whatsapp_template_usage (tenant_id, month, category, blocked) VALUES ($1, `+usageMonth+`, $2, 1)
			ON CONFLICT (tenant_id, month, category) DO UPDATE SET blocked = whatsapp_template_usage.blocked + 1, updated_at = now()`, tenant, Category(t))
		return err
	})
	return ok, err
}

// Record counts a template sent, as overage when the month's templates
// were already at the allowance.
func (m *UsageMeter) Record(ctx context.Context, tenant uuid.UUID, t Template) error {
	allowance, err := m.Allowance(ctx, tenant)
	if err != nil {
		return err
	}
	return db.InTenantTx(ctx, m.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		// One count at a time per tenant, so overage starts exactly at the
		// allowance.
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('whatsapp-templates/' || $1::text, 0))`, tenant); err != nil {
			return err
		}
		var used int64
		if err := tx.QueryRow(ctx, `SELECT COALESCE(sum(sent), 0)::bigint FROM whatsapp_template_usage WHERE tenant_id = $1 AND month = `+usageMonth, tenant).Scan(&used); err != nil {
			return err
		}
		over := 0
		if allowance > 0 && used >= allowance {
			over = 1
		}
		_, err := tx.Exec(ctx, `INSERT INTO whatsapp_template_usage (tenant_id, month, category, sent, over_allowance) VALUES ($1, `+usageMonth+`, $2, 1, $3)
			ON CONFLICT (tenant_id, month, category) DO UPDATE SET sent = whatsapp_template_usage.sent + 1,
			  over_allowance = whatsapp_template_usage.over_allowance + EXCLUDED.over_allowance, updated_at = now()`, tenant, Category(t), over)
		return err
	})
}
