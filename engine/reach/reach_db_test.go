package reach_test

import (
	"bytes"
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/reach"
	rt "github.com/israel-duff/taskiem/engine/runtime/runtimetest"
)

// Erasing a phone's subject leaves no keyed hash of the number behind:
// the platform-wide hash key and the small space of Nigerian numbers make
// a hash reversible. The person's contact is deleted with its channels;
// a rider's and a supplier's rows stay for their history, with a hash
// that matches nothing. Linked again, the number is someone new.
func TestErasureLeavesNoPhoneHash(t *testing.T) {
	e := rt.New(t)
	ctx := context.Background()
	book := &reach.Book{Pool: e.DB.App, Vault: e.Vault, HashKey: bytes.Repeat([]byte{9}, 32)}
	const phone = "+2348031234567"
	in := func(fn func(tx pgx.Tx) error) {
		t.Helper()
		if err := db.InTenantTx(ctx, e.DB.App, []uuid.UUID{e.Tenant}, fn); err != nil {
			t.Fatal(err)
		}
	}
	rider, supplier := uuid.New(), uuid.New()
	in(func(tx pgx.Tx) error {
		if _, err := book.LinkTx(ctx, tx, e.Tenant, phone, "telegram", "tg:1"); err != nil {
			return err
		}
		env, err := e.Vault.SealTx(ctx, tx, e.Tenant, "phone", phone)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO riders (id, tenant_id, phone_hash, phone, name, created_by) VALUES ($1, $2, $3, $4, $4, 'user:ada')`,
			rider, e.Tenant, book.PhoneHash(phone), env); err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `INSERT INTO suppliers (id, tenant_id, name, phone_hash, phone, created_by) VALUES ($1, $2, 'Bola Farms', $3, $4, 'user:ada')`,
			supplier, e.Tenant, book.PhoneHash(phone), env)
		return err
	})
	subj, err := e.Vault.SubjectFor(ctx, e.Tenant, "phone", phone)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.Vault.Erase(ctx, e.Tenant, subj, "user:ada"); err != nil {
		t.Fatal(err)
	}
	count := func(q string, args ...any) int {
		t.Helper()
		var n int
		if err := e.DB.Admin.QueryRow(ctx, q, args...).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	h := book.PhoneHash(phone)
	if n := count(`SELECT count(*) FROM reach_contacts WHERE phone_hash = $1`, h); n != 0 {
		t.Errorf("reach contacts still hashed: %d", n)
	}
	if n := count(`SELECT count(*) FROM reach_channels`); n != 0 {
		t.Errorf("reach channels left: %d", n)
	}
	for _, tbl := range []string{"riders", "suppliers"} {
		if n := count(`SELECT count(*) FROM `+tbl+` WHERE phone_hash = $1`, h); n != 0 {
			t.Errorf("%s still hashed: %d", tbl, n)
		}
	}
	if n := count(`SELECT count(*) FROM riders WHERE id = $1`, rider) + count(`SELECT count(*) FROM suppliers WHERE id = $1`, supplier); n != 2 {
		t.Errorf("the rider's and supplier's rows: %d", n)
	}
	in(func(tx pgx.Tx) error {
		_, err := book.LinkTx(ctx, tx, e.Tenant, phone, "telegram", "tg:1")
		return err
	})
	if n := count(`SELECT count(*) FROM reach_contacts WHERE phone_hash = $1`, h); n != 1 {
		t.Errorf("linked again: %d", n)
	}
}
