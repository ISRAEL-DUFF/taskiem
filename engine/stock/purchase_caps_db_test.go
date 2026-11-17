package stock_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/stock"
)

// An order takes no more goods than it ordered and no more money than its
// total: more is refused in plain words, and nothing is recorded.
func TestPurchaseRefusesMoreThanOrderedOrOwed(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t, map[string]int64{"rice": 0})
	p := w.purchases()
	var po stock.Purchase
	w.in(func(tx pgx.Tx) error {
		s, err := p.AddSupplierTx(ctx, tx, w.env.Tenant, "Dangote Depot", "08031234567", "user:owner")
		if err != nil {
			return err
		}
		if po, err = stock.DraftTx(ctx, tx, w.env.Tenant, stock.DraftIn{Supplier: s.ID, Lines: []stock.PurchaseLine{{ItemID: "rice", Item: "Rice", Quantity: 10, CostKobo: 4000000}},
			Actor: "user:owner", Key: "d"}); err != nil {
			return err
		}
		if po, err = p.SendTx(ctx, tx, w.env.Tenant, po.ID, "user:owner"); err != nil {
			return err
		}
		po, err = stock.ConfirmTx(ctx, tx, w.env.Tenant, po.ID, "owner", "user:owner")
		return err
	})
	if po.TotalKobo != 40000000 {
		t.Fatalf("order: %+v", po)
	}
	if _, err := p.Receive(ctx, w.env.Tenant, po.ID, map[string]int64{"rice": 12}, "user:owner", "r1"); !errors.Is(err, stock.ErrPurchase) || !strings.Contains(err.Error(), "10") {
		t.Fatalf("12 received on an order of 10: %v", err)
	}
	if w.tb.count("rice") != 0 {
		t.Fatalf("rice %d after a refused receipt", w.tb.count("rice"))
	}
	if _, err := p.Receive(ctx, w.env.Tenant, po.ID, map[string]int64{"rice": 6}, "user:owner", "r2"); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Receive(ctx, w.env.Tenant, po.ID, map[string]int64{"rice": 5}, "user:owner", "r3"); !errors.Is(err, stock.ErrPurchase) || !strings.Contains(err.Error(), "4") {
		t.Fatalf("5 received with 4 still to come: %v", err)
	}
	if got, err := p.Receive(ctx, w.env.Tenant, po.ID, map[string]int64{"rice": 4}, "user:owner", "r4"); err != nil || got.Status != stock.FullReceived || w.tb.count("rice") != 10 {
		t.Fatalf("the rest: %+v %v, rice %d", got, err, w.tb.count("rice"))
	}

	pay := func(amount int64, key string) (stock.Purchase, error) {
		var x stock.Purchase
		err := txErr(w, func(tx pgx.Tx) error {
			var err error
			x, err = stock.PayTx(ctx, tx, w.env.Tenant, po.ID, amount, stock.Cash, "user:owner", key, w.log())
			return err
		})
		return x, err
	}
	if _, err := pay(40000001, "p1"); !errors.Is(err, stock.ErrPurchase) || !strings.Contains(err.Error(), "₦400,000") {
		t.Fatalf("paying more than the order's total: %v", err)
	}
	if x, err := pay(30000000, "p2"); err != nil || x.PaidKobo != 30000000 {
		t.Fatalf("a part: %+v %v", x, err)
	}
	if _, err := pay(10000001, "p3"); !errors.Is(err, stock.ErrPurchase) || !strings.Contains(err.Error(), "₦100,000") {
		t.Fatalf("paying more than is left: %v", err)
	}
	// A payment retried is answered as it was, even now that it is paid up.
	if x, err := pay(10000000, "p4"); err != nil || x.PaidKobo != 40000000 {
		t.Fatalf("the rest: %+v %v", x, err)
	}
	if x, err := pay(10000000, "p4"); err != nil || x.PaidKobo != 40000000 {
		t.Fatalf("the rest, retried: %+v %v", x, err)
	}
	var paid int64
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT COALESCE(sum(amount_kobo), 0) FROM purchase_payments WHERE purchase_id = $1`, po.ID).Scan(&paid); err != nil || paid != 40000000 {
		t.Fatalf("payments recorded: %d %v", paid, err)
	}
}
