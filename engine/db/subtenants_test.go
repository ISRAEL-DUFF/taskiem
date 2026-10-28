package db_test

import (
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/db/dbtest"
)

// TestSubtenantCapFallsBackToThePlan: a partner with no cap of its own
// (partners.max_subtenants 0) is held to its plan's partner.max_subtenants,
// as deploy/plans.yaml's Business plan reads; an operator's cap on the
// partner still wins, and a plan without a cap leaves it unlimited.
func TestSubtenantCapFallsBackToThePlan(t *testing.T) {
	d := dbtest.New(t)
	partner := d.SeedTenant(t, nil)
	if _, err := d.Admin.Exec(ctx, `INSERT INTO partners (tenant_id, updated_by) VALUES ($1, 'test')`, partner.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Admin.Exec(ctx, `INSERT INTO plans (id, name, tier, monthly_kobo, annual_kobo, partner, updated_by)
		VALUES ('business_test', 'Business', 'business', 0, 0, '{"max_subtenants": 2}', 'test')`); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Admin.Exec(ctx, `INSERT INTO subscriptions (tenant_id, plan_id, status, period_start, period_end, updated_by)
		VALUES ($1, 'business_test', 'active', now(), now() + interval '30 days', 'test')`, partner.ID); err != nil {
		t.Fatal(err)
	}
	create := func() error {
		return scoped(t, d, []uuid.UUID{partner.ID}, func(tx pgx.Tx) error {
			_, err := tx.Exec(ctx, `SELECT taskiem_partner_create_subtenant($1, 'sub', '', 'user', 'test', '{}')`, uuid.Must(uuid.NewV7()))
			return err
		})
	}
	for i := range 2 {
		if err := create(); err != nil {
			t.Fatalf("sub-tenant %d of the plan's 2: %v", i+1, err)
		}
	}
	if err := create(); sqlState(err) != "53400" {
		t.Fatalf("a third sub-tenant on a plan of 2: %v", err)
	}
	// The operator's own cap on the partner wins over the plan's.
	if _, err := d.Admin.Exec(ctx, `UPDATE partners SET max_subtenants = 3 WHERE tenant_id = $1`, partner.ID); err != nil {
		t.Fatal(err)
	}
	if err := create(); err != nil {
		t.Fatalf("third under the partner's cap of 3: %v", err)
	}
	if err := create(); sqlState(err) != "53400" {
		t.Fatalf("fourth over the partner's cap of 3: %v", err)
	}
	// No cap anywhere: no limit.
	if _, err := d.Admin.Exec(ctx, `UPDATE partners SET max_subtenants = 0 WHERE tenant_id = $1`, partner.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Admin.Exec(ctx, `UPDATE plans SET partner = '{}' WHERE id = 'business_test'`); err != nil {
		t.Fatal(err)
	}
	if err := create(); err != nil {
		t.Fatalf("no cap anywhere: %v", err)
	}
}
