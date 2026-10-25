package moneylog_test

import (
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/moneylog"
	rt "github.com/israel-duff/taskiem/engine/runtime/runtimetest"
)

// A paid account is remembered by its pseudonym, never its number; where
// both sides know the bank's code they must agree; another business's
// payments say nothing about this one's.
func TestAccountsPaid(t *testing.T) {
	e := rt.New(t)
	other := e.AddTenant(t)
	in := func(tenant uuid.UUID, fn func(pgx.Tx) error) {
		t.Helper()
		if err := db.InTenantTx(ctx, e.DB.App, []uuid.UUID{tenant}, fn); err != nil {
			t.Fatal(err)
		}
	}
	paid := func(tenant uuid.UUID, account, bank string) (bool, bool) {
		t.Helper()
		var p, k bool
		in(tenant, func(tx pgx.Tx) error {
			var err error
			p, k, err = moneylog.PaidBefore(ctx, tx, e.Vault, tenant, account, bank)
			return err
		})
		return p, k
	}
	if p, k := paid(e.Tenant, "0123456789", "058"); p || !k {
		t.Fatalf("before any payment: paid %v known %v", p, k)
	}
	in(e.Tenant, func(tx pgx.Tx) error {
		if err := moneylog.RememberAccount(ctx, tx, e.Vault, e.Tenant, "0123-456-789", "058", time.Now()); err != nil {
			return err
		}
		return moneylog.RememberAccount(ctx, tx, e.Vault, e.Tenant, "0123456789", "058", time.Now())
	})
	for _, c := range []struct {
		account, bank string
		want          bool
	}{
		{"0123456789", "058", true},
		{"0123 456 789", "", true}, // the digits decide when the bank is not known
		{"0123456789", "044", false},
		{"0987654321", "058", false},
	} {
		if p, _ := paid(e.Tenant, c.account, c.bank); p != c.want {
			t.Errorf("%s at %q: paid %v, want %v", c.account, c.bank, p, c.want)
		}
	}
	if p, _ := paid(other, "0123456789", "058"); p {
		t.Error("another business sees this one's payments")
	}
	if _, k := paid(e.Tenant, "12", ""); k {
		t.Error("two digits are not an account")
	}

	// One row, counted twice, and no account number in it.
	var times int
	var key, bank string
	if err := e.DB.Admin.QueryRow(ctx, `SELECT times, account_key, bank_code FROM payout_accounts WHERE tenant_id = $1`, e.Tenant).Scan(&times, &key, &bank); err != nil {
		t.Fatal(err)
	}
	if times != 2 || bank != "058" || key == "" || len(key) < 20 {
		t.Errorf("row: %d %q %q", times, key, bank)
	}
	if strings.Contains(key, "123456789") {
		t.Errorf("the key holds the number: %s", key)
	}
}
