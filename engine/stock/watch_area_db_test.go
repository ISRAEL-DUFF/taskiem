package stock_test

import (
	"context"
	"testing"
	"time"

	"github.com/israel-duff/taskiem/engine/stock"
)

// A business that turned Stock off is not watched: no low-stock alert is
// raised until it turns Stock on again.
func TestStockWatchStopsWhenStockIsOff(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t, map[string]int64{"rice": 2})
	w.s.Table = &catalogue{table: w.tb, reorder: map[string]int64{"rice": 5}}
	now := time.Now()
	watch := &stock.Watch{Stock: w.s, Pool: w.env.DB.App, Now: func() time.Time { return now }}
	if _, err := watch.Tenant(ctx, w.env.Tenant); err != nil {
		t.Fatal(err)
	}
	now = now.Add(8 * 24 * time.Hour)
	set := func(on bool) {
		t.Helper()
		if _, err := w.env.DB.Admin.Exec(ctx, `INSERT INTO business_areas (tenant_id, area, enabled, updated_by) VALUES ($1, 'stock', $2, 'test')
			ON CONFLICT (tenant_id, area) DO UPDATE SET enabled = $2`, w.env.Tenant, on); err != nil {
			t.Fatal(err)
		}
	}
	set(false)
	if n, err := watch.Tenant(ctx, w.env.Tenant); err != nil || n != 0 {
		t.Fatalf("with Stock off: %d alerts, %v", n, err)
	}
	set(true)
	if n, err := watch.Tenant(ctx, w.env.Tenant); err != nil || n != 1 {
		t.Fatalf("with Stock on again: %d alerts, %v", n, err)
	}
}
