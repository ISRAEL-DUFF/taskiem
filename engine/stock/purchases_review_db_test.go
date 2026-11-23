package stock_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/stock"
)

// reviewSupplier is a supplier not on Taskiem, for the review's tests.
func reviewSupplier(t *testing.T, w *world, name, phone string) stock.Supplier {
	t.Helper()
	var s stock.Supplier
	w.in(func(tx pgx.Tx) error {
		var err error
		s, err = w.purchases().AddSupplierTx(context.Background(), tx, w.env.Tenant, name, phone, "user:owner")
		return err
	})
	return s
}

// placed writes an order of lines to supplier as someone else placed it
// (PutPlacedLinesTx, as a linked supplier's order is), and answers it.
func placed(t *testing.T, w *world, supplier uuid.UUID, lines ...stock.PurchaseLine) (stock.Purchase, error) {
	t.Helper()
	ctx := context.Background()
	var x stock.Purchase
	err := txErr(w, func(tx pgx.Tx) error {
		id := uuid.Must(uuid.NewV7())
		if _, err := tx.Exec(ctx, `INSERT INTO purchase_orders (id, tenant_id, reference, supplier_id, total_kobo, drafted_by, key)
			VALUES ($1, $2, $3, $4, 0, 'link:Them', $5)`, id, w.env.Tenant, "P-"+strings.ToUpper(uuid.NewString()[:6]), supplier, "placed-"+id.String()); err != nil {
			return err
		}
		if err := stock.PutPlacedLinesTx(ctx, tx, w.env.Tenant, id, supplier, lines, "link:Them"); err != nil {
			return err
		}
		var err error
		x, err = stock.PurchaseTx(ctx, tx, w.env.Tenant, id, false)
		return err
	})
	return x, err
}

// An order placed for the business (by a linked supplier) of more than
// TrackAsk of an item it remembers keeping track of is not refused (no
// one there can say yes, and the refusal would tell the supplier how the
// business treats the item, §7): the line is recorded and left
// unconfirmed; confirming it as it is keeps the memory. The business's
// own draft still asks.
func TestPlacedOverTrackAskIsLeftUnconfirmed(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t, nil)
	sup := reviewSupplier(t, w, "Chair Makers", "0803 123 4591")
	w.in(func(tx pgx.Tx) error {
		return stock.SetSupplierItemTx(ctx, tx, w.env.Tenant, sup.ID, "Plastic chair", stock.LineChoice{Treatment: stock.Track}, "user:owner")
	})
	x, err := placed(t, w, sup.ID, stock.PurchaseLine{Item: "Plastic chair", Quantity: 100, CostKobo: 500000})
	if err != nil {
		t.Fatalf("placing 100 of a remembered-tracked item: %v", err)
	}
	if l := x.Lines[0]; l.Treatment != stock.Record || !l.Unconfirmed {
		t.Fatalf("the line: recorded and unconfirmed, got %+v", l)
	}
	// A few of it are tracked as remembered, and confirmed.
	if x, err := placed(t, w, sup.ID, stock.PurchaseLine{Item: "Plastic chair", Quantity: 5, CostKobo: 500000}); err != nil ||
		x.Lines[0].Treatment != stock.Track || x.Lines[0].Unconfirmed {
		t.Fatalf("5 of it: %+v %v", x.Lines, err)
	}
	// Confirmed as it is: this order's line is recorded, the memory kept.
	w.in(func(tx pgx.Tx) error {
		var err error
		x, err = stock.ConfirmLinesTx(ctx, tx, w.env.Tenant, x.ID, nil, "user:owner")
		return err
	})
	if l := x.Lines[0]; l.Treatment != stock.Record || l.Unconfirmed {
		t.Fatalf("confirmed: %+v", l)
	}
	w.in(func(tx pgx.Tx) error {
		list, err := stock.SupplierItemsTx(ctx, tx, w.env.Tenant, sup.ID)
		if err == nil && (len(list) != 1 || list[0].Treatment != stock.Track) {
			t.Errorf("the memory: %+v", list)
		}
		return err
	})
	// The business drafting it itself is asked.
	err = txErr(w, func(tx pgx.Tx) error {
		_, err := stock.DraftTx(ctx, tx, w.env.Tenant, stock.DraftIn{Supplier: sup.ID, Actor: "user:owner", Key: "own",
			Lines: []stock.PurchaseLine{{Item: "Plastic chair", Quantity: 100, CostKobo: 500000}}})
		return err
	})
	if !errors.Is(err, stock.ErrTrackAsk) {
		t.Fatalf("the business's own draft: %v", err)
	}
}

// A receipt's key is kept on purchase_receipts in its transaction: the
// same key again makes nothing, for a receipt and for a short adjustment,
// on an order with no stock line too.
func TestReceiptKeysKept(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t, nil)
	p := w.purchases()
	sup := reviewSupplier(t, w, "Paper Co", "0803 123 4592")
	var x stock.Purchase
	w.in(func(tx pgx.Tx) error {
		var err error
		x, err = stock.DraftTx(ctx, tx, w.env.Tenant, stock.DraftIn{Supplier: sup.ID, Actor: "user:owner", Key: "paper",
			Lines: []stock.PurchaseLine{{Item: "Receipt paper", Quantity: 10, CostKobo: 1000, Treatment: stock.Record}}})
		return err
	})
	if _, err := w.env.DB.Admin.Exec(ctx, `UPDATE purchase_orders SET status = 'sent', approved_by = 'user:owner', sent_at = now() WHERE id = $1`, x.ID); err != nil {
		t.Fatal(err)
	}
	keys := func() (n int) {
		t.Helper()
		if err := w.env.DB.Admin.QueryRow(ctx, `SELECT count(*) FROM purchase_receipts WHERE tenant_id = $1 AND purchase_order_id = $2`, w.env.Tenant, x.ID).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	item := x.Lines[0].ItemID
	for range 2 {
		got, err := p.Receive(ctx, w.env.Tenant, x.ID, map[string]int64{item: 4}, "user:owner", "r-1")
		if err != nil {
			t.Fatal(err)
		}
		if got.Lines[0].Received != 4 {
			t.Fatalf("received: %d", got.Lines[0].Received)
		}
	}
	if keys() != 1 {
		t.Fatalf("receipt keys: %d", keys())
	}
	// A receipt refused keeps no key: the same key then receives.
	if _, err := p.Receive(ctx, w.env.Tenant, x.ID, map[string]int64{item: 0}, "user:owner", "r-2"); err == nil {
		t.Fatal("a receipt of nothing")
	}
	if got, err := p.Receive(ctx, w.env.Tenant, x.ID, map[string]int64{item: 6}, "user:owner", "r-2"); err != nil || got.Lines[0].Received != 10 {
		t.Fatalf("after a refusal, the same key: %+v %v", got.Lines, err)
	}
	for range 2 {
		w.in(func(tx pgx.Tx) error {
			_, _, err := stock.AdjustShortTx(ctx, tx, w.env.Tenant, x.ID, []int64{7}, "user:owner", "short-1")
			return err
		})
	}
	var got stock.Purchase
	w.in(func(tx pgx.Tx) error {
		var err error
		got, err = stock.PurchaseTx(ctx, tx, w.env.Tenant, x.ID, false)
		return err
	})
	if got.Lines[0].Received != 7 || got.ReceivedKobo != 7000 || keys() != 3 {
		t.Fatalf("adjusted once: %d received, %d kobo, %d keys", got.Lines[0].Received, got.ReceivedKobo, keys())
	}
}

// PR review check: a name of 200 letters of "İ" (which a full lowering
// makes two letters each) keys within the 200 the CHECK allows: the line is
// placed and its treatment remembered, never a 500.
func TestLongNameKeyFits(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t, nil)
	sup := reviewSupplier(t, w, "Istanbul Mama", "0803 123 4594")
	name := strings.Repeat("İ", 200)
	if k := stock.NameKey(name); utf8.RuneCountInString(k) > 200 || k != strings.TrimSpace(k) {
		t.Fatalf("the key: %d letters", utf8.RuneCountInString(k))
	}
	w.in(func(tx pgx.Tx) error {
		return stock.SetSupplierItemTx(ctx, tx, w.env.Tenant, sup.ID, name, stock.LineChoice{Treatment: stock.Track}, "user:owner")
	})
	x, err := placed(t, w, sup.ID, stock.PurchaseLine{Item: name, Quantity: 1, CostKobo: 100})
	if err != nil {
		t.Fatal(err)
	}
	if len(x.Lines) != 1 || x.Lines[0].Treatment != stock.Track {
		t.Fatalf("placed: %+v", x.Lines)
	}
}

// The memory of an item by its name, the line's own reading of it
// (Unconfirmed) and Taskiem's suggestion all key the name one way
// (NameKey): accents lowered, a non-breaking space a space.
func TestNameKeyOneWay(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t, nil)
	sup := reviewSupplier(t, w, "Market Mama", "0803 123 4593")
	const nbsp = " "
	w.in(func(tx pgx.Tx) error {
		if err := stock.SetSupplierItemTx(ctx, tx, w.env.Tenant, sup.ID, "Égusi seeds", stock.LineChoice{Treatment: stock.Track}, "user:owner"); err != nil {
			return err
		}
		return stock.SetSupplierItemTx(ctx, tx, w.env.Tenant, sup.ID, "Palm"+nbsp+"oil", stock.LineChoice{Treatment: stock.Track}, "user:owner")
	})
	x, err := placed(t, w, sup.ID, stock.PurchaseLine{Item: "ÉGUSI  seeds", Quantity: 2, CostKobo: 100}, stock.PurchaseLine{Item: "palm" + nbsp + "Oil", Quantity: 1, CostKobo: 100})
	if err != nil {
		t.Fatal(err)
	}
	for _, l := range x.Lines {
		if l.Treatment != stock.Track || l.Unconfirmed {
			t.Errorf("%q as remembered and confirmed: %+v", l.Item, l)
		}
	}
	// A stock item named with a non-breaking space is suggested for the
	// name typed with a plain one.
	stockItem := uuid.New()
	if _, err := w.env.DB.Admin.Exec(ctx, `INSERT INTO stock_items (id, tenant_id, name, created_by) VALUES ($1, $2, $3, 'user:owner')`,
		stockItem, w.env.Tenant, "Shea"+nbsp+"Butter"); err != nil {
		t.Fatal(err)
	}
	w.in(func(tx pgx.Tx) error {
		c, err := stock.SuggestTx(ctx, tx, w.env.Tenant, "shea butter")
		if err == nil && (c.Treatment != stock.AddToStock || c.StockItemID == nil || *c.StockItemID != stockItem) {
			t.Errorf("suggested: %+v", c)
		}
		if c, err := stock.SuggestTx(ctx, tx, w.env.Tenant, "shea butters"); err != nil || c.Treatment != stock.Record {
			t.Errorf("another name: %+v %v", c, err)
		}
		return err
	})
}
