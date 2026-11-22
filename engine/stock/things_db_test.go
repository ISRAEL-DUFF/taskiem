package stock_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/stock"
)

// Two sales of one thing at once (§5.3): the second waits for the first's
// row lock and is refused, recorded already, though it was paid another
// way (cash, then transfer): one money-log entry in, not two.
func TestThingSoldTwiceAtOnce(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t, nil)
	var gen stock.Thing
	w.in(func(tx pgx.Tx) error {
		var err error
		gen, err = stock.AddThingTx(ctx, tx, w.env.Tenant, stock.NewThing{Name: "Generator", CostKobo: n(25000000)}, "user:owner")
		return err
	})
	sold := stock.KeptSold
	sell := func(tx pgx.Tx, how, key string) error {
		_, err := stock.ChangeThingTx(ctx, tx, w.env.Tenant, gen.ID, stock.ThingChange{Status: &sold, SalePriceKobo: n(15000000), SalePaidHow: how},
			"user:owner", key, w.log())
		return err
	}
	firstDone, release, firstErr := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	go func() {
		firstErr <- db.InTenantTx(ctx, w.env.DB.App, []uuid.UUID{w.env.Tenant}, func(tx pgx.Tx) error {
			err := sell(tx, stock.Cash, "sell-cash")
			close(firstDone)
			<-release
			return err
		})
	}()
	<-firstDone
	secondErr := make(chan error, 1)
	go func() {
		secondErr <- db.InTenantTx(ctx, w.env.DB.App, []uuid.UUID{w.env.Tenant}, func(tx pgx.Tx) error {
			return sell(tx, stock.Transfer, "sell-transfer")
		})
	}()
	// The second waits on the first's lock (or, without one, finishes).
	var err2 error
	finished, waited := false, false
	for deadline := time.Now().Add(10 * time.Second); !finished && !waited && time.Now().Before(deadline); {
		select {
		case err2 = <-secondErr:
			finished = true
			continue
		default:
		}
		var n int
		if err := w.env.DB.Admin.QueryRow(ctx, `SELECT count(*) FROM pg_stat_activity WHERE datname = current_database() AND wait_event_type = 'Lock'`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		waited = n > 0
		time.Sleep(10 * time.Millisecond)
	}
	close(release)
	if err := <-firstErr; err != nil {
		t.Fatal(err)
	}
	if !finished {
		err2 = <-secondErr
	}
	if !waited || !errors.Is(err2, stock.ErrPurchase) {
		t.Errorf("the second sale: waited %v, %v", waited, err2)
	}
	var entries int
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT count(*) FROM money_entries WHERE tenant_id = $1 AND reference = $2`, w.env.Tenant,
		stock.SaleReference(gen.ID)).Scan(&entries); err != nil || entries != 1 {
		t.Fatalf("the sale's money: %d entries %v", entries, err)
	}
}

// Things we have counted over every row (§5.3): more rows than a list
// holds are all in count and total_kobo and in the groups; order= and
// line= keep the rows that came on them, and count and cost those had.
func TestThingTotalsAndFilters(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t, nil)
	tenant := w.env.Tenant
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := w.env.DB.Admin.Exec(ctx, q, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO kept_things (id, tenant_id, name, cost_kobo, created_by) SELECT gen_random_uuid(), $1, 'Chair ' || i, 1000, 'user:owner'
		FROM generate_series(1, $2::int) i`, tenant, stock.MaxThings+5)
	supplier, order, line, other := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	exec(`INSERT INTO suppliers (id, tenant_id, name, phone_hash, phone, created_by) VALUES ($1, $2, 'Singer Ltd', $3, '{}', 'user:owner')`, supplier, tenant, make([]byte, 32))
	exec(`INSERT INTO purchase_orders (id, tenant_id, reference, supplier_id, total_kobo, status, drafted_by, approved_by, sent_at, key)
		VALUES ($1, $2, 'P-AAA111', $3, 1, 'confirmed', 'user:owner', 'user:owner', now(), 'k')`, order, tenant, supplier)
	for i, l := range []uuid.UUID{line, other} {
		exec(`INSERT INTO purchase_lines (id, tenant_id, purchase_order_id, position, name, quantity, cost_kobo, treatment)
			VALUES ($1, $2, $3, $4, 'Sewing machine', 3, 500000, 'track')`, l, tenant, order, i)
	}
	// Three on the line (one gone), two on the other line.
	for i, x := range []struct {
		line   uuid.UUID
		unit   int
		status string
	}{{line, 1, "have"}, {line, 2, "have"}, {line, 3, "gone"}, {other, 1, "have"}, {other, 2, "have"}} {
		exec(`INSERT INTO kept_things (id, tenant_id, name, purchase_order_id, purchase_line_id, unit_no, cost_kobo, status, created_by)
			VALUES ($1, $2, 'Sewing machine', $3, $4, $5, $6, $7, 'user:owner')`, uuid.New(), tenant, order, x.line, x.unit, int64(500000+i), x.status)
	}
	w.in(func(tx pgx.Tx) error {
		list, err := stock.ThingsTx(ctx, tx, tenant, stock.ThingFilter{})
		if err != nil {
			return err
		}
		count, total, err := stock.ThingTotalsTx(ctx, tx, tenant, stock.ThingFilter{})
		if err != nil {
			return err
		}
		if len(list) != stock.MaxThings || count != stock.MaxThings+5+4 || total != int64(stock.MaxThings+5)*1000+500000*4+0+1+3+4 {
			t.Errorf("all: %d listed, %d had, %d", len(list), count, total)
		}
		groups, err := stock.ThingGroupsTx(ctx, tx, tenant, stock.ThingFilter{})
		if err != nil {
			return err
		}
		if len(groups) != 1 || groups[0].LabelID != nil || groups[0].Count != count || groups[0].TotalKobo != total {
			t.Errorf("groups: %+v", groups)
		}
		for _, c := range []struct {
			f           stock.ThingFilter
			rows, count int
			total       int64
		}{
			{stock.ThingFilter{Line: &line}, 2, 2, 1000001},
			{stock.ThingFilter{Line: &line, All: true}, 3, 2, 1000001},
			{stock.ThingFilter{Order: &order}, 4, 4, 2000008},
			{stock.ThingFilter{Order: &order, All: true}, 5, 4, 2000008},
		} {
			list, err := stock.ThingsTx(ctx, tx, tenant, c.f)
			if err != nil {
				return err
			}
			count, total, err := stock.ThingTotalsTx(ctx, tx, tenant, c.f)
			if err != nil {
				return err
			}
			if len(list) != c.rows || count != c.count || total != c.total {
				t.Errorf("%+v: %d rows, %d had, %d", c.f, len(list), count, total)
			}
		}
		return nil
	})
}
