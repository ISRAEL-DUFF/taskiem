package audit_test

import (
	"bytes"
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/audit"
	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/db/dbtest"
)

var ctx = context.Background()

func export(t *testing.T, d *dbtest.DB, tenant uuid.UUID) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	if err := db.InTenantTx(ctx, d.App, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		return audit.Export(ctx, tx, tenant.String(), &buf)
	}); err != nil {
		t.Fatal(err)
	}
	return &buf
}

func TestIndependentVerifierMatchesPostgres(t *testing.T) {
	d := dbtest.New(t)
	tn := d.SeedTenant(t, nil)
	details := []string{
		`{}`,
		`{"ip": "10.0.0.1", "roles": ["approver", "credit_officer"]}`,
		`{"note": "quote \" backslash \\ newline \n tab \t bell \u0007", "naira": "₦5,000", "emoji": "✅"}`,
		`{"amount": 1.50, "big": 123456789012345678901234567890, "neg": -0.0, "exp": 1e3, "nested": {"z": 1, "aa": [true, false, null]}}`,
		`{"a": 1, "bb": 2, "ab": 3, "b": 4}`,
	}
	err := db.InTenantTx(ctx, d.App, []uuid.UUID{tn.ID}, func(tx pgx.Tx) error {
		for i, det := range details {
			if _, err := tx.Exec(ctx, `SELECT taskiem_audit_append($1, 'user', $2, 'test.action', $3, $4::jsonb)`, tn.ID, "user-"+string(rune('a'+i)), "target \"x\"", det); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	res, err := audit.Verify(export(t, d, tn.ID))
	if err != nil || res.FirstBroken != 0 {
		t.Fatalf("intact chain: %+v %v", res, err)
	}
	if res.Entries < int64(len(details)) {
		t.Fatalf("verified %d entries", res.Entries)
	}

	// Tampering with one entry's detail breaks it, and the database agrees.
	var seq int64
	_ = d.Admin.QueryRow(ctx, `SELECT chain_seq FROM audit_log WHERE tenant_id = $1 AND detail ? 'naira'`, tn.ID).Scan(&seq)
	if _, err := d.Admin.Exec(ctx, `UPDATE audit_log SET detail = detail || '{"naira": "₦50,000"}' WHERE tenant_id = $1 AND chain_seq = $2`, tn.ID, seq); err != nil {
		t.Fatal(err)
	}
	res, _ = audit.Verify(export(t, d, tn.ID))
	var sqlBroken *int64
	_ = db.InTenantTx(ctx, d.App, []uuid.UUID{tn.ID}, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT taskiem_audit_verify($1)`, tn.ID).Scan(&sqlBroken)
	})
	if res.FirstBroken != seq || sqlBroken == nil || *sqlBroken != seq {
		t.Errorf("tampered entry %d: verifier says %+v, SQL says %v", seq, res, sqlBroken)
	}
}

func TestVerifierCatchesTruncation(t *testing.T) {
	d := dbtest.New(t)
	tn := d.SeedTenant(t, nil)
	_ = db.InTenantTx(ctx, d.App, []uuid.UUID{tn.ID}, func(tx pgx.Tx) error {
		for i := 0; i < 3; i++ {
			if _, err := tx.Exec(ctx, `SELECT taskiem_audit_append($1, 'system', 'sys', 'x', 'y', '{}')`, tn.ID); err != nil {
				return err
			}
		}
		return nil
	})
	full := export(t, d, tn.ID).String()
	lines := bytes.Split([]byte(full), []byte("\n"))
	// Drop the last entry but keep the head line.
	cut := append(append([][]byte{}, lines[:len(lines)-3]...), lines[len(lines)-2])
	res, err := audit.Verify(bytes.NewReader(bytes.Join(cut, []byte("\n"))))
	if err != nil || res.FirstBroken == 0 {
		t.Errorf("truncated export verified: %+v %v", res, err)
	}
}
