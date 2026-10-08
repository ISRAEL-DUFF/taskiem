package audit_test

import (
	"bytes"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/audit"
	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/db/dbtest"
)

// The application role appends only through taskiem_audit_append, and only
// for tenants in its scope.
func TestAppRoleCannotForgeTheChain(t *testing.T) {
	d := dbtest.New(t)
	tn := d.SeedTenant(t, nil)
	other := d.SeedTenant(t, nil)
	appendEntries(t, d, tn.ID, 2)
	for name, sql := range map[string]string{
		"insert a row": `INSERT INTO audit_log (tenant_id, chain_seq, actor_type, actor_id, action, target, detail, prev_hash, hash, at)
			SELECT tenant_id, 3, 'user', 'u1', 'forged', 'x', '{}', hash, hash, now() FROM audit_log WHERE tenant_id = $1 AND chain_seq = 2`,
		"move the head": `UPDATE audit_chain_heads SET chain_seq = 1 WHERE tenant_id = $1`,
		"start a head":  `INSERT INTO audit_chain_heads (tenant_id, chain_seq, head_hash) VALUES ($1, 9, sha256('x'))`,
	} {
		err := db.InTenantTx(ctx, d.App, []uuid.UUID{tn.ID}, func(tx pgx.Tx) error {
			_, err := tx.Exec(ctx, sql, tn.ID)
			return err
		})
		if err == nil {
			t.Errorf("%s: allowed", name)
		}
	}
	// Appending for a tenant outside the caller's scope is refused.
	err := db.InTenantTx(ctx, d.App, []uuid.UUID{tn.ID}, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `SELECT taskiem_audit_append($1, 'user', 'u1', 'forged', 'x', '{}')`, other.ID)
		return err
	})
	if err == nil {
		t.Error("appended to another tenant's chain")
	}
	var bad *int64
	_ = db.InTenantTx(ctx, d.App, []uuid.UUID{tn.ID}, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT taskiem_audit_verify($1)`, tn.ID).Scan(&bad)
	})
	if bad != nil {
		t.Errorf("chain broken at %d", *bad)
	}
}

// The anchorer signs only a head the log reproduces.
func TestAnchorRefusesAHeadTheLogDoesNotReproduce(t *testing.T) {
	d := dbtest.New(t)
	tn := d.SeedTenant(t, nil)
	appendEntries(t, d, tn.ID, 3)
	signer, err := audit.NewSigner(bytes.Repeat([]byte{7}, 32))
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	x := &audit.Anchorer{Pool: d.App, Signer: signer, Dir: dir, Interval: time.Nanosecond}
	if n, err := x.Tick(ctx); err != nil || n != 1 {
		t.Fatalf("first anchor: %d %v", n, err)
	}
	appendEntries(t, d, tn.ID, 2)
	if _, err := d.Admin.Exec(ctx, `UPDATE audit_chain_heads SET head_hash = sha256('forged') WHERE tenant_id = $1`, tn.ID); err != nil {
		t.Fatal(err)
	}
	time.Sleep(10 * time.Millisecond)
	n, err := x.Tick(ctx)
	if n != 0 || !errors.Is(err, audit.ErrHeadMismatch) {
		t.Errorf("forged head: anchored %d, %v", n, err)
	}
	// An entry rewritten below the head, after the last anchor, is caught too.
	if _, err := d.Admin.Exec(ctx, `UPDATE audit_chain_heads h SET head_hash = l.hash FROM audit_log l WHERE l.tenant_id = h.tenant_id AND l.chain_seq = h.chain_seq AND h.tenant_id = $1`, tn.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Admin.Exec(ctx, `UPDATE audit_log SET detail = '{"amount": 1}' WHERE tenant_id = $1 AND chain_seq = 4`, tn.ID); err != nil {
		t.Fatal(err)
	}
	if n, err := x.Tick(ctx); n != 0 || !errors.Is(err, audit.ErrHeadMismatch) {
		t.Errorf("rewritten entry: anchored %d, %v", n, err)
	}
	if _, err := d.Admin.Exec(ctx, `UPDATE audit_log SET detail = '{"amount": 5000}' WHERE tenant_id = $1 AND chain_seq = 4`, tn.ID); err != nil {
		t.Fatal(err)
	}
	if n, err := x.Tick(ctx); n != 1 || err != nil {
		t.Errorf("restored chain: anchored %d, %v", n, err)
	}
}
