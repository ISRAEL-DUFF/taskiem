package stock_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/stock"
)

// A stock item's barcode (Scan to count, device fields D2, migration
// 00590): set trimmed and cleared; one current item of a business has a
// code, in any case; a retired one keeps its code, which a current one may
// then take, and brought back it gives the code up; the database refuses
// a code too long or with control characters, and one taken, by itself.
func TestStockItemCode(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t, nil)
	a, b := w.counted("a"), w.counted("b")
	set := func(id uuid.UUID, code string) (stock.StockItem, error) {
		var x stock.StockItem
		err := db.InTenantTx(ctx, w.env.DB.App, []uuid.UUID{w.env.Tenant}, func(tx pgx.Tx) error {
			var err error
			x, err = stock.SetCodeTx(ctx, tx, w.env.Tenant, id, code)
			return err
		})
		return x, err
	}
	if x, err := set(a, " 6151100093217 "); err != nil || x.Code != "6151100093217" {
		t.Fatalf("set: %+v %v", x, err)
	}
	var taken *stock.CodeTaken
	if _, err := set(b, "6151100093217"); !errors.As(err, &taken) || taken.ID != a {
		t.Fatalf("a code a has, on b: %v", err)
	}
	if _, err := set(b, "abc"); err != nil {
		t.Fatal(err)
	}
	if _, err := set(a, "ABC"); !errors.As(err, &taken) || taken.ID != b {
		t.Fatalf("in another case: %v", err)
	}
	for _, bad := range []string{strings.Repeat("9", 65), "61\x0011", "61\t11"} {
		if _, err := set(a, bad); !errors.Is(err, stock.ErrInvalid) {
			t.Fatalf("%q: %v", bad, err)
		}
	}
	if x, err := set(b, ""); err != nil || x.Code != "" {
		t.Fatalf("cleared: %+v %v", x, err)
	}
	// The database itself.
	w.in(func(tx pgx.Tx) error {
		for _, q := range []string{
			`UPDATE stock_items SET code = repeat('9', 65) WHERE tenant_id = $1 AND id = $2`,
			`UPDATE stock_items SET code = E'61\n11' WHERE tenant_id = $1 AND id = $2`,
			`UPDATE stock_items SET code = '6151100093217' WHERE tenant_id = $1 AND id = $2`,
		} {
			if _, err := tx.Exec(ctx, "SAVEPOINT s"); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, q, w.env.Tenant, b); err == nil {
				t.Errorf("taken: %s", q)
			}
			if _, err := tx.Exec(ctx, "ROLLBACK TO SAVEPOINT s"); err != nil {
				return err
			}
		}
		return nil
	})
	// Retired, a keeps its code, and b may take it; brought back, a gives
	// it up.
	w.in(func(tx pgx.Tx) error { return stock.RetireItemTx(ctx, tx, w.env.Tenant, a, false) })
	if _, err := set(b, "6151100093217"); err != nil {
		t.Fatalf("a retired item's code: %v", err)
	}
	w.in(func(tx pgx.Tx) error { return stock.RetireItemTx(ctx, tx, w.env.Tenant, a, true) })
	w.in(func(tx pgx.Tx) error {
		x, err := stock.StockItemTx(ctx, tx, w.env.Tenant, a)
		if err == nil && (x.Code != "" || x.RetiredAt != nil) {
			t.Errorf("brought back with a taken code: %+v", x)
		}
		return err
	})
}
