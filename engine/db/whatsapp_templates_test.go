package db_test

import (
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/db/dbtest"
)

// The platform number's template registry (whatsapp_templates rows with no
// tenant) is the platform's: every tenant reads it, none writes it. An own
// number's upsert that hits a platform row's key (a tenant that registered
// the platform's WABA id) must not take the row over (F36).
func TestTenantsCannotWritePlatformTemplates(t *testing.T) {
	d := dbtest.New(t)
	a := d.SeedTenant(t, nil)
	const waba = "100200300400"
	// The platform pool writes with an empty scope.
	if _, err := d.App.Exec(ctx, `SELECT taskiem_wa_platform_template_record($1, 'taskiem_otp', 'en', '999', 'approved', '', now())`, waba); err != nil {
		t.Fatal(err)
	}
	// What an own number's record() ran before (it moved the row to its
	// tenant), and what it runs now (it leaves another owner's row alone),
	// in its tenant's scope.
	for i, q := range []string{
		`INSERT INTO whatsapp_templates (waba_id, name, language, tenant_id, meta_id, status, reason, submitted_at, checked_at)
			VALUES ($1, 'taskiem_otp', 'en', $2, NULL, 'disabled', 'x', NULL, now())
			ON CONFLICT (waba_id, name, language) DO UPDATE SET tenant_id = EXCLUDED.tenant_id, status = EXCLUDED.status, reason = EXCLUDED.reason`,
		`INSERT INTO whatsapp_templates (waba_id, name, language, tenant_id, meta_id, status, reason, submitted_at, checked_at)
			VALUES ($1, 'taskiem_otp', 'en', $2, NULL, 'disabled', 'x', NULL, now())
			ON CONFLICT (waba_id, name, language) DO UPDATE SET meta_id = COALESCE(EXCLUDED.meta_id, whatsapp_templates.meta_id),
			  status = EXCLUDED.status, reason = EXCLUDED.reason, checked_at = now(), updated_at = now()
			WHERE whatsapp_templates.tenant_id = EXCLUDED.tenant_id`,
	} {
		var n int64
		err := scoped(t, d, []uuid.UUID{a.ID}, func(tx pgx.Tx) error {
			tag, err := tx.Exec(ctx, q, waba, a.ID)
			n = tag.RowsAffected()
			return err
		})
		if err == nil && n > 0 {
			t.Errorf("upsert %d: a tenant's upsert onto the platform's row changed it", i)
		}
	}
	for _, q := range []string{
		`INSERT INTO whatsapp_templates (waba_id, name, language, tenant_id, status) VALUES ('100200300400', 'taskiem_invite', 'en', NULL, 'approved')`,
		`UPDATE whatsapp_templates SET status = 'disabled' WHERE tenant_id IS NULL`,
		`DELETE FROM whatsapp_templates WHERE tenant_id IS NULL`,
	} {
		var n int64
		err := scoped(t, d, []uuid.UUID{a.ID}, func(tx pgx.Tx) error {
			tag, err := tx.Exec(ctx, q)
			n = tag.RowsAffected()
			return err
		})
		if err == nil && n > 0 {
			t.Errorf("%s: a tenant wrote %d platform rows", q, n)
		}
	}
	// The definer refuses a tenant's scope.
	err := scoped(t, d, []uuid.UUID{a.ID}, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `SELECT taskiem_wa_platform_template_record($1, 'taskiem_otp', 'en', NULL, 'disabled', '', NULL)`, waba)
		return err
	})
	if sqlState(err) != "42501" {
		t.Errorf("the platform's definer in a tenant scope: %v", err)
	}
	// The row is still the platform's, and still readable by the tenant.
	var tenant *uuid.UUID
	var status string
	if err := d.Admin.QueryRow(ctx, `SELECT tenant_id, status FROM whatsapp_templates WHERE waba_id = $1 AND name = 'taskiem_otp'`, waba).Scan(&tenant, &status); err != nil {
		t.Fatal(err)
	}
	if tenant != nil || status != "approved" {
		t.Errorf("platform row: tenant %v, status %s", tenant, status)
	}
	if n := count(t, d, []uuid.UUID{a.ID}, `SELECT count(*) FROM whatsapp_templates WHERE tenant_id IS NULL`); n != 1 {
		t.Errorf("tenant reads %d platform rows, want 1", n)
	}
	// The platform updates its own row again.
	if _, err := d.App.Exec(ctx, `SELECT taskiem_wa_platform_template_record($1, 'taskiem_otp', 'en', NULL, 'paused', 'q', NULL)`, waba); err != nil {
		t.Fatal(err)
	}
	if err := d.Admin.QueryRow(ctx, `SELECT status FROM whatsapp_templates WHERE waba_id = $1 AND name = 'taskiem_otp'`, waba).Scan(&status); err != nil || status != "paused" {
		t.Errorf("platform update: %s %v", status, err)
	}
	// A tenant's own rows stay its to write.
	if err := scoped(t, d, []uuid.UUID{a.ID}, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO whatsapp_templates (waba_id, name, language, tenant_id, status) VALUES ('555000111', 'taskiem_otp', 'en', $1, 'pending')`, a.ID)
		return err
	}); err != nil {
		t.Errorf("own row: %v", err)
	}
}
