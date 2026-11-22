package stock_test

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/connectors/xenibase"
	"github.com/israel-duff/taskiem/connectors/xenibase/xenibasetest"
	"github.com/israel-duff/taskiem/engine/datatables"
	"github.com/israel-duff/taskiem/engine/moneylog"
	"github.com/israel-duff/taskiem/engine/stock"
)

// The owner's project keeps read-only copies of the stock items and the
// movements (what-a-business-buys §4.1): each movement once, each stock
// item as it is now, changed in place when its count moves; and neither
// table is anyone else's to write.
func TestMirrorKeepsStockItemsAndMovements(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t, map[string]int64{"rice": 10})
	x := xenibasetest.New(t)
	now := time.Now()
	x.Now = func() time.Time { return now }
	x.AddToken(xenibasetest.Token{Secret: "xb_platform", ID: "tok", Name: "platform", Scopes: []string{"read", "write", "admin"}, ExpiresAt: now.Add(10 * 365 * 24 * time.Hour)})
	if err := w.env.Registry.Register(xenibase.New(xenibase.Options{BaseURL: x.URL})); err != nil {
		t.Fatal(err)
	}
	w.in(func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO environments (tenant_id, name) VALUES ($1, 'prod')`, w.env.Tenant)
		return err
	})
	tables := &datatables.Service{Pool: w.env.DB.App, Vault: w.env.Vault, Egress: w.env.Egress,
		Platform: datatables.Platform{URL: x.URL, Token: "xb_platform", Org: x.OrgID, Region: "lagos"}, PollInterval: time.Millisecond}
	rows := &moneylog.XenibaseRows{Registry: w.env.Registry, Vault: w.env.Vault, Guard: w.env.Egress}
	m := &stock.Mirror{Stock: w.s, Tables: tables, Rows: rows}
	read := func(table string) []map[string]any {
		t.Helper()
		got, err := rows.Call(ctx, w.env.Tenant, "read_rows", map[string]any{"table": table, "limit": 50}, "")
		if err != nil {
			t.Fatal(err)
		}
		var out []map[string]any
		for _, r := range got["rows"].([]any) {
			out = append(out, r.(map[string]any))
		}
		return out
	}
	// A material too.
	var fabric stock.StockItem
	w.in(func(tx pgx.Tx) error {
		var err error
		fabric, err = stock.CreateItemTx(ctx, tx, w.env.Tenant, stock.NewItem{Name: "Ankara fabric", Unit: "yard", Low: n(5), Actor: "user:owner"})
		return err
	})
	for range 2 {
		if err := m.Tenant(ctx, w.env.Tenant); err != nil {
			t.Fatal(err)
		}
	}
	items := read(stock.KeptItemsTable)
	if len(items) != 2 || len(read(stock.KeptTable)) != 1 {
		t.Fatalf("copied: %v, %v", items, read(stock.KeptTable))
	}
	// The rice sold: its row is changed in place, its movement added.
	w.record(stock.Move{ItemID: "rice", Item: "Rice", Reason: stock.Sale, Delta: n(-3)})
	if err := m.Tenant(ctx, w.env.Tenant); err != nil {
		t.Fatal(err)
	}
	items = read(stock.KeptItemsTable)
	counts := map[string]any{}
	for _, r := range items {
		counts[r["stock_item_id"].(string)] = r["count"]
	}
	if len(items) != 2 || jsonInt(counts[w.items["rice"].String()]) != 7 || jsonInt(counts[fabric.ID.String()]) != 0 || len(read(stock.KeptTable)) != 2 {
		t.Fatalf("after the sale: %v", items)
	}
	var left int
	w.in(func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT (SELECT count(*) FROM stock_items WHERE mirrored_at IS DISTINCT FROM updated_at)
			+ (SELECT count(*) FROM stock_movements WHERE mirrored_at IS NULL)`).Scan(&left)
	})
	if left != 0 {
		t.Fatalf("%d not copied", left)
	}
	// Kept by Taskiem: no one else writes them.
	for _, name := range []string{stock.KeptItemsTable, stock.KeptTable} {
		var keptBy string
		w.in(func(tx pgx.Tx) error {
			return tx.QueryRow(ctx, `SELECT COALESCE(kept_by, '') FROM data_tables WHERE tenant_id = $1 AND name = $2`, w.env.Tenant, name).Scan(&keptBy)
		})
		if keptBy == "" || !datatables.IsKeptName(name) {
			t.Errorf("%s is not kept (%q)", name, keptBy)
		}
	}
}

func jsonInt(v any) int64 {
	switch x := v.(type) {
	case float64:
		return int64(x)
	case int64:
		return x
	case int:
		return int64(x)
	}
	return -1
}
