package stock_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/stock"
)

// What a business buys §12, Purchases, on the server: each line treated
// on its own when it arrives, said once and remembered per supplier item.
func TestBuyingByTreatment(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t, nil)
	p := w.purchases()
	fabric := w.stockItem("Ankara fabric", "yard", 0, nil) // a material
	labels := map[string]uuid.UUID{}
	for _, name := range []string{"Machines", "Staff welfare", "Packaging"} {
		labels[name] = uuid.New()
		if _, err := w.env.DB.Admin.Exec(ctx, `INSERT INTO spend_labels (id, tenant_id, name, created_by) VALUES ($1, $2, $3, 'user:owner')`,
			labels[name], w.env.Tenant, name); err != nil {
			t.Fatal(err)
		}
	}
	label := func(name string) *uuid.UUID { id := labels[name]; return &id }
	var singer stock.Supplier
	w.in(func(tx pgx.Tx) error {
		var err error
		singer, err = p.AddSupplierTx(ctx, tx, w.env.Tenant, "Singer Ltd", "0803 123 4590", "user:owner")
		return err
	})
	draft := func(key string, lines ...stock.PurchaseLine) (stock.Purchase, error) {
		var x stock.Purchase
		err := txErr(w, func(tx pgx.Tx) error {
			var err error
			x, err = stock.DraftTx(ctx, tx, w.env.Tenant, stock.DraftIn{Supplier: singer.ID, Lines: lines, Actor: "user:owner", Key: key})
			return err
		})
		return x, err
	}
	mustDraft := func(key string, lines ...stock.PurchaseLine) stock.Purchase {
		t.Helper()
		x, err := draft(key, lines...)
		if err != nil {
			t.Fatal(err)
		}
		return x
	}
	send := func(x stock.Purchase) {
		t.Helper()
		if _, err := w.env.DB.Admin.Exec(ctx, `UPDATE purchase_orders SET status = 'sent', approved_by = 'user:owner', sent_at = now() WHERE id = $1`, x.ID); err != nil {
			t.Fatal(err)
		}
	}
	things := func(order uuid.UUID) int {
		t.Helper()
		var n int
		if err := w.env.DB.Admin.QueryRow(ctx, `SELECT count(*) FROM kept_things WHERE purchase_order_id = $1`, order).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}

	// A mixed order, each line said the first time: the machine kept track
	// of (Machines), snacks and nylon bags just recorded, fabric to stock.
	machine := stock.PurchaseLine{Item: "Singer heavy-duty sewing machine", Quantity: 1, CostKobo: 18000000, Treatment: stock.Track, LabelID: label("Machines")}
	snacks := stock.PurchaseLine{Item: "Snacks", Unit: "pack", Quantity: 10, CostKobo: 120000, Treatment: stock.Record, LabelID: label("Staff welfare")}
	bags := stock.PurchaseLine{Item: "Nylon bags", Quantity: 100, CostKobo: 12000, Treatment: stock.Record, LabelID: label("Packaging")}
	cloth := stock.PurchaseLine{Item: "Ankara fabric", Unit: "yard", Quantity: 30, CostKobo: 150000, Treatment: stock.AddToStock, StockItemID: &fabric}
	first := mustDraft("mixed", machine, snacks, bags, cloth)
	for _, l := range first.Lines {
		if l.Unconfirmed {
			t.Fatalf("said on drafting, confirmed: %+v", l)
		}
	}
	if l := first.Lines[0]; l.Treatment != stock.Track || l.LabelID == nil || *l.LabelID != labels["Machines"] || l.StockItemID != nil {
		t.Fatalf("the machine's line: %+v", l)
	}
	send(first)
	got, err := p.Receive(ctx, w.env.Tenant, first.ID, map[string]int64{first.Lines[0].ItemID: 1, first.Lines[1].ItemID: 10, first.Lines[2].ItemID: 100,
		fabric.String(): 30}, "user:owner", "all-came")
	if err != nil {
		t.Fatal(err)
	}
	// Stock moved only for the fabric, its cost set; one row on Things we
	// have, the machine, with its label, supplier and order.
	if n, cost := w.countOf(fabric); n != 30 || cost == nil || *cost != 150000 {
		t.Fatalf("the fabric: %d at %v", n, cost)
	}
	var moved int
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT count(*) FROM stock_movements WHERE tenant_id = $1`, w.env.Tenant).Scan(&moved); err != nil || moved != 1 {
		t.Fatalf("movements: %d %v", moved, err)
	}
	if got.Status != stock.FullReceived || things(first.ID) != 1 {
		t.Fatalf("received: %s, %d things", got.Status, things(first.ID))
	}
	var list []stock.Thing
	w.in(func(tx pgx.Tx) error {
		var err error
		list, err = stock.ThingsTx(ctx, tx, w.env.Tenant, stock.ThingFilter{})
		return err
	})
	if len(list) != 1 || list[0].Name != machine.Item || list[0].Label != "Machines" || list[0].Supplier != "Singer Ltd" || list[0].Order != first.Reference ||
		list[0].CostKobo == nil || *list[0].CostKobo != 18000000 || list[0].Status != stock.KeptHave {
		t.Fatalf("Things we have: %+v", list)
	}
	// The material reached no catalogue: no product is counted by it.
	var products int
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT count(*) FROM product_stock WHERE stock_item_id = $1`, fabric).Scan(&products); err != nil || products != 0 {
		t.Fatalf("the fabric is sold as %d products (%v)", products, err)
	}
	// The same receipt again makes nothing twice.
	if _, err := p.Receive(ctx, w.env.Tenant, first.ID, map[string]int64{fabric.String(): 30}, "user:owner", "all-came"); err != nil {
		t.Fatal(err)
	}
	if n, _ := w.countOf(fabric); n != 30 || things(first.ID) != 1 {
		t.Fatalf("received twice: fabric %d, %d things", n, things(first.ID))
	}
	// Received, the line's treatment is locked.
	if err := txErr(w, func(tx pgx.Tx) error {
		_, err := stock.SetLineTx(ctx, tx, w.env.Tenant, first.ID, 0, stock.LineChoice{Treatment: stock.Record}, false, "user:owner")
		return err
	}); !errors.Is(err, stock.ErrLineLocked) {
		t.Fatalf("a received line changed: %v", err)
	}

	// The next order of the same items asks nothing: filled as remembered
	// ("diesel " is "Diesel": names are matched without case or spaces).
	second := mustDraft("again", stock.PurchaseLine{Item: " singer HEAVY-duty  sewing machine", Quantity: 2, CostKobo: 18000000},
		stock.PurchaseLine{Item: "snacks", Quantity: 5, CostKobo: 120000}, stock.PurchaseLine{Item: "Ankara fabric", Quantity: 10, CostKobo: 150000})
	for i, want := range []stock.Treatment{stock.Track, stock.Record, stock.AddToStock} {
		if l := second.Lines[i]; l.Unconfirmed || l.Treatment != want {
			t.Fatalf("line %d, as remembered: %+v", i, l)
		}
	}
	if l := second.Lines[0]; l.LabelID == nil || *l.LabelID != labels["Machines"] {
		t.Fatalf("the label remembered: %+v", l)
	}
	// One order's line treated differently (a machine kept for the shop,
	// just recorded): only that line changes.
	differ := mustDraft("differ", stock.PurchaseLine{Item: machine.Item, Quantity: 1, CostKobo: 18000000, Treatment: stock.Record})
	if differ.Lines[0].Treatment != stock.Record {
		t.Fatalf("the line as said: %+v", differ.Lines[0])
	}
	w.in(func(tx pgx.Tx) error {
		x, err := stock.SetLineTx(ctx, tx, w.env.Tenant, second.ID, 1, stock.LineChoice{Treatment: stock.Track}, false, "user:owner")
		if err != nil || x.Lines[1].Treatment != stock.Track {
			t.Errorf("a line changed: %+v %v", x.Lines, err)
		}
		return nil
	})
	var remembered []stock.Remembered
	w.in(func(tx pgx.Tx) error {
		var err error
		remembered, err = stock.SupplierItemsTx(ctx, tx, w.env.Tenant, singer.ID)
		return err
	})
	byName := map[string]stock.Remembered{}
	for _, m := range remembered {
		byName[m.NameKey] = m
	}
	if len(remembered) != 4 || byName["singer heavy-duty sewing machine"].Treatment != stock.Track || byName["snacks"].Treatment != stock.Record ||
		byName["ankara fabric"].StockItem != "Ankara fabric" || byName["nylon bags"].Label != "Packaging" {
		t.Fatalf("the memory, unchanged by lines that differ: %+v", remembered)
	}

	// The memory changed (on the Suppliers page): orders after it only.
	w.in(func(tx pgx.Tx) error {
		return stock.SetSupplierItemTx(ctx, tx, w.env.Tenant, singer.ID, "Snacks", stock.LineChoice{Treatment: stock.Track, LabelID: label("Staff welfare")}, "user:owner")
	})
	w.in(func(tx pgx.Tx) error {
		x, err := stock.PurchaseTx(ctx, tx, w.env.Tenant, second.ID, false)
		if err != nil || x.Lines[1].Treatment != stock.Track || x.Lines[0].Treatment != stock.Track {
			t.Errorf("an order drafted before: %+v %v", x.Lines, err)
		}
		return nil
	})
	if later := mustDraft("later", stock.PurchaseLine{Item: "Snacks", Quantity: 3, CostKobo: 1}); later.Lines[0].Treatment != stock.Track {
		t.Fatalf("an order after the change: %+v", later.Lines[0])
	}

	// Not all came on a tracked line: rows for what came only.
	three := mustDraft("three", stock.PurchaseLine{Item: machine.Item, Quantity: 3, CostKobo: 18000000})
	send(three)
	if _, err := p.Receive(ctx, w.env.Tenant, three.ID, map[string]int64{three.Lines[0].ID.String(): 1}, "user:owner", "short"); err != nil {
		t.Fatal(err)
	}
	if n := things(three.ID); n != 1 {
		t.Fatalf("1 of 3 came: %d rows", n)
	}
	if _, err := p.Receive(ctx, w.env.Tenant, three.ID, map[string]int64{three.Lines[0].ID.String(): 2}, "user:owner", "rest"); err != nil || things(three.ID) != 3 {
		t.Fatalf("the rest came: %d rows, %v", things(three.ID), err)
	}
	var units []int
	w.in(func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT unit_no FROM kept_things WHERE purchase_order_id = $1 ORDER BY unit_no`, three.ID)
		if err != nil {
			return err
		}
		units, err = pgx.CollectRows(rows, pgx.RowTo[int])
		return err
	})
	if len(units) != 3 || units[0] != 1 || units[2] != 3 {
		t.Fatalf("each unit numbered once: %v", units)
	}

	// Tracking more than 50 asks first; more than 200 cannot be tracked.
	many := stock.PurchaseLine{Item: "Nylon bags, big", Quantity: 60, CostKobo: 100, Treatment: stock.Track}
	var track *stock.TrackError
	if _, err := draft("many", many); !errors.Is(err, stock.ErrTrackAsk) || !errors.As(err, &track) || track.Quantity != 60 {
		t.Fatalf("60 tracked: %v", err)
	}
	many.TrackMany = true
	if x, err := draft("many-yes", many); err != nil || x.Lines[0].Treatment != stock.Track {
		t.Fatalf("60 tracked, said yes to: %v", err)
	}
	many.Quantity = 300
	if _, err := draft("too-many", many); !errors.Is(err, stock.ErrTrackTooMany) {
		t.Fatalf("300 tracked: %v", err)
	}

	// Another supplier: nothing remembered, so a suggestion to confirm: a
	// stock item of that name, added to; a thing tracked before, tracked;
	// else recorded.
	var other stock.Supplier
	w.in(func(tx pgx.Tx) error {
		var err error
		other, err = p.AddSupplierTx(ctx, tx, w.env.Tenant, "Fabric Hub", "0803 123 4591", "user:owner")
		return err
	})
	w.in(func(tx pgx.Tx) error {
		x, err := stock.DraftTx(ctx, tx, w.env.Tenant, stock.DraftIn{Supplier: other.ID, Actor: "user:owner", Key: "suggest", Lines: []stock.PurchaseLine{
			{Item: "ankara fabric", Quantity: 5, CostKobo: 1}, {Item: machine.Item, Quantity: 1, CostKobo: 1}, {Item: "Diesel", Quantity: 1, CostKobo: 1}}})
		if err != nil {
			return err
		}
		if l := x.Lines[0]; !l.Unconfirmed || l.Treatment != stock.AddToStock || *l.StockItemID != fabric {
			t.Errorf("a stock item of the name: %+v", l)
		}
		if l := x.Lines[1]; !l.Unconfirmed || l.Treatment != stock.Track || l.LabelID == nil {
			t.Errorf("tracked before: %+v", l)
		}
		if l := x.Lines[2]; !l.Unconfirmed || l.Treatment != stock.Record {
			t.Errorf("anything else: %+v", l)
		}
		// Confirmed, it is remembered: the next order asks nothing.
		if x, err = stock.ConfirmLinesTx(ctx, tx, w.env.Tenant, x.ID, nil, "user:owner"); err != nil {
			return err
		}
		for _, l := range x.Lines {
			if l.Unconfirmed {
				t.Errorf("confirmed: %+v", l)
			}
		}
		return nil
	})

	// Things we have: added by hand, then sold with a price: money in,
	// Sale of things we had, once; it leaves the totals, kept in the
	// history.
	var gen stock.Thing
	w.in(func(tx pgx.Tx) error {
		var err error
		gen, err = stock.AddThingTx(ctx, tx, w.env.Tenant, stock.NewThing{Name: "Generator", LabelID: label("Machines"), CostKobo: n(25000000), Serial: "GX-1",
			WarrantyUntil: "2027-06-30"}, "user:owner")
		return err
	})
	if gen.Serial != "GX-1" || gen.WarrantyUntil == nil || *gen.WarrantyUntil != "2027-06-30" || gen.Label != "Machines" {
		t.Fatalf("added by hand: %+v", gen)
	}
	sold := stock.KeptSold
	sell := func(key string, price int64) (stock.Thing, error) {
		var x stock.Thing
		err := txErr(w, func(tx pgx.Tx) error {
			var err error
			x, err = stock.ChangeThingTx(ctx, tx, w.env.Tenant, gen.ID, stock.ThingChange{Status: &sold, SalePriceKobo: n(price)}, "user:owner", key, w.log())
			return err
		})
		return x, err
	}
	x, err := sell("sell-gen", 15000000)
	if err != nil || x.Status != stock.KeptSold || x.SaleEntryID == nil {
		t.Fatalf("sold: %+v %v", x, err)
	}
	// The same sale with the same key again (a retry whose answer was
	// lost) is the row as it is; another key, or another price, is
	// refused: recorded already.
	if again, err := sell("sell-gen", 15000000); err != nil || again.SaleEntryID == nil || *again.SaleEntryID != *x.SaleEntryID {
		t.Fatalf("the same sale again: %+v %v", again, err)
	}
	for _, c := range []struct {
		key   string
		price int64
	}{{"sell-gen-2", 15000000}, {"sell-gen", 16000000}} {
		if _, err := sell(c.key, c.price); !errors.Is(err, stock.ErrPurchase) {
			t.Fatalf("%+v: %v", c, err)
		}
	}
	var entries int
	var category, direction string
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT count(*), max(category), max(direction) FROM money_entries WHERE tenant_id = $1 AND reference LIKE 'thing-sale:%'`,
		w.env.Tenant).Scan(&entries, &category, &direction); err != nil || entries != 1 || category != stock.SaleCategory || direction != "in" {
		t.Fatalf("the sale's money: %d %q %q %v", entries, category, direction, err)
	}
	w.in(func(tx pgx.Tx) error {
		all, err := stock.ThingsTx(ctx, tx, w.env.Tenant, stock.ThingFilter{All: true})
		if err != nil {
			return err
		}
		had, err := stock.ThingsTx(ctx, tx, w.env.Tenant, stock.ThingFilter{})
		if err != nil {
			return err
		}
		groups, err := stock.ThingGroupsTx(ctx, tx, w.env.Tenant, stock.ThingFilter{All: true})
		if err != nil {
			return err
		}
		if len(all) != len(had)+1 || len(groups) != 1 || groups[0].Label != "Machines" || groups[0].Count != len(had) {
			t.Errorf("the sold generator in the history, out of the totals: %d of %d, %+v", len(had), len(all), groups)
		}
		return nil
	})
}
