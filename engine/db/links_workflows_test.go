package db_test

import (
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/db/dbtest"
)

// A workflows workspace is no business to link with (00450,
// docs/spec/taskiem-for-everyone.md §18.2): a number names only its
// owner's business, even when the owner has a workspace too, and a link
// code or shop address never reaches a workspace.
func TestLinksSkipWorkflowsWorkspaces(t *testing.T) {
	d := dbtest.New(t)
	ada, dangote, flows := d.SeedTenant(t, nil), d.SeedTenant(t, nil), d.SeedTenant(t, nil)
	owner := uuid.Must(uuid.NewV7())
	const phone = "+2348031234590"
	if err := scoped(t, d, []uuid.UUID{dangote.ID, flows.ID}, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `INSERT INTO users (id, email) VALUES ($1, 'd@dangote-flows.test')`, owner); err != nil {
			return err
		}
		for _, tn := range []uuid.UUID{dangote.ID, flows.ID} {
			if _, err := tx.Exec(ctx, `INSERT INTO memberships (tenant_id, user_id, role) VALUES ($1, $2, 'owner')`, tn, owner); err != nil {
				return err
			}
		}
		_, err := tx.Exec(ctx, `INSERT INTO link_settings (tenant_id, findable, code, updated_by) VALUES ($1, true, 'wkflows2', 'test')`, flows.ID)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Admin.Exec(ctx, `INSERT INTO whatsapp_bindings (user_id, number) VALUES ($1, $2)`, owner, phone); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Admin.Exec(ctx, `UPDATE tenants SET kind = 'workflows' WHERE id = $1`, flows.ID); err != nil {
		t.Fatal(err)
	}
	var biz *uuid.UUID
	if err := d.Admin.QueryRow(ctx, `SELECT taskiem_link_phone_business($1)`, phone).Scan(&biz); err != nil || biz == nil || *biz != dangote.ID {
		t.Fatalf("the owner's number names their business only: %v %v", biz, err)
	}
	if n := count(t, d, []uuid.UUID{ada.ID}, `SELECT count(*) FROM taskiem_link_by_code('wkflows2')`); n != 0 {
		t.Errorf("a workspace's code found: %d", n)
	}
	err := scoped(t, d, []uuid.UUID{ada.ID}, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `SELECT taskiem_link_request(NULL, 'wkflows2', NULL, NULL, 'supplier', 'user', 'u1')`)
		return err
	})
	if err == nil {
		t.Errorf("a link request by a workspace's code was taken")
	}
	// The business on its own is no longer named once it too is workflows.
	if _, err := d.Admin.Exec(ctx, `UPDATE tenants SET kind = 'workflows' WHERE id = $1`, dangote.ID); err != nil {
		t.Fatal(err)
	}
	if err := d.Admin.QueryRow(ctx, `SELECT taskiem_link_phone_business($1)`, phone).Scan(&biz); err != nil || biz != nil {
		t.Fatalf("a number of workspaces only: %v %v", biz, err)
	}
}
