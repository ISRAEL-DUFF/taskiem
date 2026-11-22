package stock_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/productstock"
	rt "github.com/israel-duff/taskiem/engine/runtime/runtimetest"
	"github.com/israel-duff/taskiem/engine/stock"
	"github.com/israel-duff/taskiem/engine/storefront"
)

// world is a business whose products (catalogue rows by id) are each
// counted by a stock item of their own.
type world struct {
	t   *testing.T
	env *rt.Env
	s   *stock.Stock
	tb  table
	// items is each product's stock item.
	items map[string]uuid.UUID
}

// table reads a product's count: its stock item's.
type table struct{ w *world }

func (f table) count(row string) int64 {
	f.w.t.Helper()
	var n int64
	f.w.in(func(tx pgx.Tx) error {
		return tx.QueryRow(context.Background(), `SELECT count FROM stock_items WHERE id = $1`, f.w.items[row]).Scan(&n)
	})
	return n
}

// newWorld makes a stock item for each product of counts, linked to it,
// and receives its count.
func newWorld(t *testing.T, counts map[string]int64) *world {
	w := &world{t: t, env: rt.New(t), items: map[string]uuid.UUID{}}
	w.tb = table{w: w}
	w.s = &stock.Stock{Pool: w.env.DB.App}
	for row, n := range counts {
		w.counted(row)
		if n > 0 {
			w.record(stock.Move{ItemID: row, Item: row, Reason: stock.Received, Delta: &n, Key: "seed-" + row})
		}
	}
	return w
}

// counted makes product row counted by a stock item of its own.
func (w *world) counted(row string) uuid.UUID {
	w.t.Helper()
	w.in(func(tx pgx.Tx) error {
		x, err := stock.CreateItemTx(context.Background(), tx, w.env.Tenant, stock.NewItem{Name: row, Actor: "user:owner"})
		if err != nil {
			return err
		}
		w.items[row] = x.ID
		return productstock.LinkTx(context.Background(), tx, w.env.Tenant, row, x.ID, 1)
	})
	return w.items[row]
}

func (w *world) in(fn func(pgx.Tx) error) {
	w.t.Helper()
	if err := db.InTenantTx(context.Background(), w.env.DB.App, []uuid.UUID{w.env.Tenant}, fn); err != nil {
		w.t.Fatal(err)
	}
}

func n(v int64) *int64 { return &v }

func ptr(s string) *string { return &s }

func (w *world) record(mv stock.Move) stock.Movement {
	w.t.Helper()
	if mv.Actor == "" {
		mv.Actor = "user:owner"
	}
	if mv.Key == "" {
		mv.Key = uuid.NewString()
	}
	var m stock.Movement
	w.in(func(tx pgx.Tx) error {
		var err error
		m, err = stock.RecordTx(context.Background(), tx, w.env.Tenant, mv)
		return err
	})
	return m
}

// apply reads a movement again: it was made with its count.
func (w *world) apply(id uuid.UUID) stock.Movement {
	w.t.Helper()
	var m stock.Movement
	w.in(func(tx pgx.Tx) error {
		var err error
		m, err = stock.GetTx(context.Background(), tx, w.env.Tenant, id)
		return err
	})
	return m
}

// ledger is the sum of a product's stock item's movements.
func (w *world) ledger(row string) int64 {
	w.t.Helper()
	var sum int64
	w.in(func(tx pgx.Tx) error {
		return tx.QueryRow(context.Background(), `SELECT COALESCE(sum(change), 0) FROM stock_movements WHERE stock_item_id = $1`, w.items[row]).Scan(&sum)
	})
	return sum
}

func (w *world) movements(item string) []stock.Movement {
	w.t.Helper()
	var list []stock.Movement
	w.in(func(tx pgx.Tx) error {
		var err error
		list, err = stock.ListTx(context.Background(), tx, w.env.Tenant, stock.Filter{ItemID: item})
		return err
	})
	return list
}

// A movement is made with its count, in one transaction; the count is
// always the sum of its movements, and the same key is the same movement.
func TestMovementMakesTheCount(t *testing.T) {
	w := newWorld(t, map[string]int64{"rice": 10})
	m := w.record(stock.Move{ItemID: "rice", Item: "Rice (bag)", Reason: stock.Received, Delta: n(50), Key: "receive-1"})
	if m.Status != "done" || *m.Before != 10 || *m.After != 60 || *m.Change != 50 || m.ItemID != w.items["rice"].String() {
		t.Fatalf("recorded: %+v", m)
	}
	if again := w.record(stock.Move{ItemID: "rice", Item: "Rice (bag)", Reason: stock.Received, Delta: n(50), Key: "receive-1"}); again.ID != m.ID {
		t.Fatalf("a second movement for one key: %s, %s", again.ID, m.ID)
	}
	if w.tb.count("rice") != 60 || w.ledger("rice") != 60 {
		t.Fatalf("count %d, ledger %d", w.tb.count("rice"), w.ledger("rice"))
	}
	// By its stock item's own id, the same.
	w.record(stock.Move{ItemID: w.items["rice"].String(), Item: "Rice", Reason: stock.Adjustment, Delta: n(-5), Note: "spoilt"})
	if w.tb.count("rice") != 55 || len(w.movements("rice")) != 3 || len(w.movements(w.items["rice"].String())) != 3 {
		t.Fatalf("by the stock item: %d, %d movements", w.tb.count("rice"), len(w.movements("rice")))
	}
}

// No count changes except through a movement (spec §12): the database
// refuses a count set by hand, a stock item made with one, and a movement
// that does not start from the count.
func TestCountChangesOnlyByAMovement(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t, map[string]int64{"rice": 4})
	id := w.items["rice"]
	for name, q := range map[string]string{
		"a count set by hand":        `UPDATE stock_items SET count = 99 WHERE id = '` + id.String() + `'`,
		"a stock item made with one": `INSERT INTO stock_items (id, tenant_id, name, count, created_by) VALUES (gen_random_uuid(), '` + w.env.Tenant.String() + `', 'Beans', 5, 'x')`,
		"a movement from another count": `INSERT INTO stock_movements (id, tenant_id, stock_item_id, item, reason, delta, count_before, count_after, change, actor, key)
			VALUES (gen_random_uuid(), '` + w.env.Tenant.String() + `', '` + id.String() + `', 'Rice', 'adjustment', 5, 0, 5, 5, 'x', 'k')`,
		"a movement changed": `UPDATE stock_movements SET change = 1`,
	} {
		err := db.InTenantTx(ctx, w.env.DB.App, []uuid.UUID{w.env.Tenant}, func(tx pgx.Tx) error {
			_, err := tx.Exec(ctx, q)
			return err
		})
		if err == nil {
			t.Errorf("%s was allowed", name)
		}
	}
	if w.tb.count("rice") != 4 || w.ledger("rice") != 4 {
		t.Fatalf("count %d, ledger %d", w.tb.count("rice"), w.ledger("rice"))
	}
	// Its other details change freely.
	w.in(func(tx pgx.Tx) error {
		_, err := stock.SetItemTx(ctx, tx, w.env.Tenant, id, stock.ItemSet{Unit: ptr("bag"), Low: n(2)})
		return err
	})
}

// Two sales of the last bag at the same moment: one takes it, the other
// is recorded short, and the count never goes below zero.
func TestTwoSalesOfTheLastBag(t *testing.T) {
	w := newWorld(t, map[string]int64{"rice": 1})
	var wg sync.WaitGroup
	got := make([]stock.Movement, 2)
	for i := range got {
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := db.InTenantTx(context.Background(), w.env.DB.App, []uuid.UUID{w.env.Tenant}, func(tx pgx.Tx) error {
				var err error
				got[i], err = stock.RecordTx(context.Background(), tx, w.env.Tenant, stock.Move{ItemID: "rice", Item: "Rice", Reason: stock.Sale,
					Delta: n(-1), Actor: "user:owner", Key: uuid.NewString()})
				return err
			})
			if err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if got[0].Short+got[1].Short != 1 {
		t.Fatalf("sales: %+v %+v", got[0], got[1])
	}
	if w.tb.count("rice") != 0 || w.ledger("rice") != 0 {
		t.Fatalf("count %d, ledger %d", w.tb.count("rice"), w.ledger("rice"))
	}
}

// A count sets the stock; a decrease never takes it below zero.
func TestCountsAndFloors(t *testing.T) {
	w := newWorld(t, map[string]int64{"rice": 5})
	adj := w.record(stock.Move{ItemID: "rice", Item: "Rice", Reason: stock.Adjustment, Delta: n(-8), Note: "spoilt"})
	if *adj.After != 0 || *adj.Change != -5 || adj.Short != 3 || adj.Note != "spoilt" {
		t.Fatalf("adjustment: %+v", adj)
	}
	c := w.record(stock.Move{ItemID: "rice", Item: "Rice", Reason: stock.Count, SetTo: n(12)})
	if *c.After != 12 || *c.Change != 12 || w.ledger("rice") != 12 {
		t.Fatalf("count: %+v", c)
	}
}

func TestMovementsRefused(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t, map[string]int64{})
	for _, mv := range []stock.Move{
		{ItemID: "rice", Item: "Rice", Reason: "stolen", Delta: n(1)},
		{ItemID: "rice", Item: "Rice", Reason: "outside", Delta: n(1)},
		{ItemID: "rice", Item: "Rice", Reason: stock.Sale},
		{ItemID: "rice", Item: "Rice", Reason: stock.Sale, Delta: n(0)},
		{ItemID: "rice", Item: "Rice", Reason: stock.Sale, SetTo: n(3)},
		{ItemID: "rice", Item: "Rice", Reason: stock.Count, Delta: n(3)},
		{ItemID: "rice", Item: "Rice", Reason: stock.Count, SetTo: n(-1)},
		{ItemID: "", Item: "Rice", Reason: stock.Sale, Delta: n(-1)},
		// A stock item that is not the business's.
		{ItemID: uuid.NewString(), Item: "Rice", Reason: stock.Received, Delta: n(1)},
	} {
		mv.Actor, mv.Key = "user:owner", uuid.NewString()
		err := db.InTenantTx(ctx, w.env.DB.App, []uuid.UUID{w.env.Tenant}, func(tx pgx.Tx) error {
			_, err := stock.RecordTx(ctx, tx, w.env.Tenant, mv)
			return err
		})
		if !errors.Is(err, stock.ErrInvalid) {
			t.Errorf("%+v: %v", mv, err)
		}
	}
	// A product counted by no stock item: selling it moves nothing.
	err := db.InTenantTx(ctx, w.env.DB.App, []uuid.UUID{w.env.Tenant}, func(tx pgx.Tx) error {
		_, err := stock.RecordTx(ctx, tx, w.env.Tenant, stock.Move{ItemID: "jollof", Item: "Jollof", Reason: stock.Sale, Delta: n(-1), Actor: "x", Key: "k1"})
		return err
	})
	if !errors.Is(err, stock.ErrNotCounted) {
		t.Fatalf("a sale of a product not counted: %v", err)
	}
	// One received starts being counted, by a stock item of its name.
	m := w.record(stock.Move{ItemID: "garri", Item: "Garri", Reason: stock.Received, Delta: n(3)})
	w.in(func(tx pgx.Tx) error {
		c, ok, err := productstock.ItemOfTx(ctx, tx, w.env.Tenant, "garri")
		if err != nil || !ok || c.Count != 3 || c.StockItemID.String() != m.ItemID {
			t.Fatalf("garri: %+v %v %v", c, ok, err)
		}
		return nil
	})
}

// An order's lines lower the stock once each row, answering what was
// short; a product not counted moves nothing.
func TestOrderLines(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t, map[string]int64{"1": 3, "2": 1})
	order := uuid.New()
	lines := []storefront.Line{{ID: "1", Name: "Jollof rice", Quantity: 1}, {ID: "2", Name: "Chapman", Quantity: 2}, {ID: "1", Name: "Jollof rice", Quantity: 1},
		{ID: "3", Name: "Delivery", Quantity: 1}}
	short, err := w.s.OrderLines(ctx, w.env.Tenant, order, "AK-7QX4KD", lines)
	if err != nil || len(short) != 1 || short[0] != "Chapman" {
		t.Fatalf("short %v, %v", short, err)
	}
	// Retried: nothing moves twice.
	if short, err = w.s.OrderLines(ctx, w.env.Tenant, order, "AK-7QX4KD", lines); err != nil || len(short) != 1 {
		t.Fatalf("retried: %v, %v", short, err)
	}
	if w.tb.count("1") != 1 || w.tb.count("2") != 0 || w.ledger("1") != 1 {
		t.Fatalf("counts %d %d", w.tb.count("1"), w.tb.count("2"))
	}
	if m := w.movements("1")[0]; m.Reason != stock.Order || m.Reference != "AK-7QX4KD" || *m.Change != -2 {
		t.Fatalf("order movement: %+v", m)
	}
}

// A material (a stock item no product is counted by) is listed with the
// products, and is on the low list at its low level (spec §12).
func TestMaterialOnTheLowList(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t, map[string]int64{"1": 9})
	var fabric stock.StockItem
	w.in(func(tx pgx.Tx) error {
		var err error
		if fabric, err = stock.CreateItemTx(ctx, tx, w.env.Tenant, stock.NewItem{Name: "Ankara fabric", Unit: "yard", Low: n(10), CostKobo: n(150000), Actor: "user:owner"}); err != nil {
			return err
		}
		_, err = stock.SetItemTx(ctx, tx, w.env.Tenant, w.items["1"], stock.ItemSet{Low: n(2), CostKobo: n(1000)})
		return err
	})
	w.record(stock.Move{ItemID: fabric.ID.String(), Item: fabric.Name, Reason: stock.Received, Delta: n(4)})
	rows := []map[string]any{{"id": "1", "name": "Rice"}}
	var items []stock.Item
	w.in(func(tx pgx.Tx) error {
		var err error
		items, err = stock.ItemsTx(ctx, tx, w.env.Tenant, rows, storefront.Columns{Name: "name"})
		return err
	})
	if len(items) != 2 {
		t.Fatalf("items: %+v", items)
	}
	low := stock.Low(items)
	if len(low) != 1 || !low[0].Material || low[0].ID != fabric.ID.String() || *low[0].Stock != 4 || low[0].Suggest() != 16 || low[0].Unit != "yard" {
		t.Fatalf("low: %+v", low)
	}
	if v := stock.ValueKobo(items); v != 4*150000+9*1000 {
		t.Fatalf("value %d", v)
	}
	// The product is the stock item's, by its row id.
	for _, it := range items {
		if it.ID == "1" && (it.StockItemID != w.items["1"].String() || it.Material || *it.Stock != 9) {
			t.Fatalf("rice: %+v", it)
		}
	}
}

// A product deleted from the owner's catalogue: the refresh drops its
// link, and its stock item stays, a material with its count and history
// (spec §12).
func TestDeletedProductLeavesAMaterial(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t, map[string]int64{"1": 7, "2": 2})
	w.in(func(tx pgx.Tx) error {
		n, err := productstock.DropGoneTx(ctx, tx, w.env.Tenant, []string{"2"})
		if err != nil || n != 1 {
			t.Fatalf("dropped %d, %v", n, err)
		}
		return nil
	})
	var items []stock.Item
	w.in(func(tx pgx.Tx) error {
		var err error
		items, err = stock.ItemsTx(ctx, tx, w.env.Tenant, []map[string]any{{"id": "2", "name": "Beans"}}, storefront.Columns{Name: "name"})
		return err
	})
	var material *stock.Item
	for i := range items {
		if items[i].Material {
			material = &items[i]
		}
	}
	if len(items) != 2 || material == nil || material.ID != w.items["1"].String() || *material.Stock != 7 || len(w.movements(material.ID)) != 1 {
		t.Fatalf("items: %+v", items)
	}
	// Selling the product it was moves nothing now.
	err := db.InTenantTx(ctx, w.env.DB.App, []uuid.UUID{w.env.Tenant}, func(tx pgx.Tx) error {
		_, err := stock.RecordTx(ctx, tx, w.env.Tenant, stock.Move{ItemID: "1", Item: "Rice", Reason: stock.Sale, Delta: n(-1), Actor: "x", Key: "k"})
		return err
	})
	if !errors.Is(err, stock.ErrNotCounted) {
		t.Fatalf("a sale of a deleted product: %v", err)
	}
}

func TestKeptRow(t *testing.T) {
	at := time.Date(2026, 10, 29, 9, 0, 0, 0, time.UTC)
	r := stock.KeptRow(stock.Movement{ID: uuid.Nil, ItemID: "1", Item: "Rice", Reason: stock.Sale, Before: n(5), After: n(3), Change: n(-2),
		Reference: "sale", Actor: "user:owner", CreatedAt: at, DoneAt: &at})
	if r["change"] != int64(-2) || r["count_after"] != int64(3) || r["at"] != "2026-10-29T09:00:00Z" || r["short"] != nil {
		t.Fatalf("row: %v", r)
	}
	x := stock.KeptItemRow(stock.StockItem{ID: uuid.Nil, Name: "Ankara fabric", Unit: "yard", Count: 12, Low: n(5), UpdatedAt: at})
	if x["count"] != int64(12) || x["low_level"] != int64(5) || x["cost_kobo"] != nil || x["retired"] != false || x["changed_at"] != "2026-10-29T09:00:00Z" {
		t.Fatalf("item row: %v", x)
	}
}
