package stock_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/stock"
)

// g-label-merge (build-plan-gaps H2): a line's label is read with the
// line, following merged_into to the label kept, so an old order's card
// names the kept label; its label_id is never rewritten.
func TestLineLabelFollowsMerge(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t, nil)
	p := w.purchases()
	snacks, welfare := uuid.New(), uuid.New()
	for id, name := range map[uuid.UUID]string{snacks: "Snacks", welfare: "Staff welfare"} {
		if _, err := w.env.DB.Admin.Exec(ctx, `INSERT INTO spend_labels (id, tenant_id, name, created_by) VALUES ($1, $2, $3, 'user:owner')`,
			id, w.env.Tenant, name); err != nil {
			t.Fatal(err)
		}
	}
	var shop stock.Supplier
	var x stock.Purchase
	w.in(func(tx pgx.Tx) error {
		var err error
		if shop, err = p.AddSupplierTx(ctx, tx, w.env.Tenant, "Corner shop", "0803 123 4591", "user:owner"); err != nil {
			return err
		}
		x, err = stock.DraftTx(ctx, tx, w.env.Tenant, stock.DraftIn{Supplier: shop.ID, Actor: "user:owner", Key: "snacks", Lines: []stock.PurchaseLine{
			{Item: "Biscuits", Quantity: 4, CostKobo: 50000, Treatment: stock.Record, LabelID: &snacks},
			{Item: "Pens", Quantity: 2, CostKobo: 10000, Treatment: stock.Record},
		}})
		return err
	})
	if l := x.Lines[0]; l.Label != "Snacks" || x.Lines[1].Label != "" {
		t.Fatalf("before the merge: %q, %q", l.Label, x.Lines[1].Label)
	}
	if _, err := w.env.DB.Admin.Exec(ctx, `UPDATE spend_labels SET merged_into = $2 WHERE id = $1`, snacks, welfare); err != nil {
		t.Fatal(err)
	}
	var lines []stock.PurchaseLine
	w.in(func(tx pgx.Tx) error {
		var err error
		lines, err = stock.LinesTx(ctx, tx, x.ID)
		return err
	})
	if l := lines[0]; l.Label != "Staff welfare" || l.LabelID == nil || *l.LabelID != snacks {
		t.Fatalf("after the merge: %+v", l)
	}
	w.in(func(tx pgx.Tx) error {
		got, err := stock.PurchaseTx(ctx, tx, w.env.Tenant, x.ID, false)
		if err == nil && got.Lines[0].Label != "Staff welfare" {
			t.Errorf("the order's line: %q", got.Lines[0].Label)
		}
		return err
	})
}

// g-thing-extra: a row of Things we have changes its supplier (one of the
// business's, or none), audited; another's is refused.
func TestThingSupplierChanged(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t, nil)
	p := w.purchases()
	var gen stock.Thing
	var singer, lister stock.Supplier
	w.in(func(tx pgx.Tx) error {
		var err error
		if singer, err = p.AddSupplierTx(ctx, tx, w.env.Tenant, "Singer Ltd", "0803 123 4592", "user:owner"); err != nil {
			return err
		}
		if lister, err = p.AddSupplierTx(ctx, tx, w.env.Tenant, "Lister Power", "0803 123 4593", "user:owner"); err != nil {
			return err
		}
		gen, err = stock.AddThingTx(ctx, tx, w.env.Tenant, stock.NewThing{Name: "Generator", SupplierID: &singer.ID}, "user:owner")
		return err
	})
	change := func(c stock.ThingChange) (stock.Thing, error) {
		var out stock.Thing
		err := txErr(w, func(tx pgx.Tx) error {
			var err error
			out, err = stock.ChangeThingTx(ctx, tx, w.env.Tenant, gen.ID, c, "user:owner", "", nil)
			return err
		})
		return out, err
	}
	if got, err := change(stock.ThingChange{SupplierID: &lister.ID}); err != nil || got.Supplier != "Lister Power" || *got.SupplierID != lister.ID {
		t.Fatalf("changed: %+v %v", got, err)
	}
	other := uuid.New()
	if _, err := change(stock.ThingChange{SupplierID: &other}); !errors.Is(err, stock.ErrNoSupplier) {
		t.Fatalf("not ours: %v", err)
	}
	if _, err := change(stock.ThingChange{SupplierID: &singer.ID, ClearSupplier: true}); !errors.Is(err, stock.ErrPurchase) {
		t.Fatalf("both: %v", err)
	}
	if got, err := change(stock.ThingChange{ClearSupplier: true}); err != nil || got.Supplier != "" || got.SupplierID != nil {
		t.Fatalf("cleared: %+v %v", got, err)
	}
	// A change of something else leaves it as it is.
	note := "In the back room"
	if _, err := change(stock.ThingChange{SupplierID: &singer.ID}); err != nil {
		t.Fatal(err)
	}
	if got, err := change(stock.ThingChange{Note: &note}); err != nil || got.Supplier != "Singer Ltd" {
		t.Fatalf("a note: %+v %v", got, err)
	}
	var audited int
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT count(*) FROM audit_log WHERE tenant_id = $1 AND action = 'thing.changed' AND detail ? 'supplier_id'`,
		w.env.Tenant).Scan(&audited); err != nil || audited != 3 {
		t.Fatalf("audited: %d %v", audited, err)
	}
}
