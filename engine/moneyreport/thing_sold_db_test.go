package moneyreport_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/db/dbtest"
	"github.com/israel-duff/taskiem/engine/moneylog"
	"github.com/israel-duff/taskiem/engine/moneyreport"
	"github.com/israel-duff/taskiem/engine/spend"
	"github.com/israel-duff/taskiem/engine/stock"
)

// A thing we had, sold (§5.3, §5.4): bought to keep for ₦500,000, left out
// of costs then, and sold for ₦300,000 in the same month: profit counts
// the sale as price less cost, a loss of ₦200,000, not a gain of ₦300,000.
func TestSpendingThingSoldAtALoss(t *testing.T) {
	d := dbtest.New(t)
	tenant := d.SeedTenant(t, nil).ID
	log := &moneylog.Log{Pool: d.App}
	in := func(fn func(pgx.Tx) error) {
		t.Helper()
		if err := db.InTenantTx(ctx, d.App, []uuid.UUID{tenant}, fn); err != nil {
			t.Fatal(err)
		}
	}
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := d.Admin.Exec(ctx, q, args...); err != nil {
			t.Fatal(err)
		}
	}
	order, supplier, line := uuid.New(), uuid.New(), uuid.New()
	exec(`INSERT INTO suppliers (id, tenant_id, name, phone_hash, phone, created_by) VALUES ($1, $2, 'Singer Ltd', $3, '{}', 'user:ada')`, supplier, tenant, make([]byte, 32))
	exec(`INSERT INTO purchase_orders (id, tenant_id, reference, supplier_id, total_kobo, status, drafted_by, approved_by, sent_at, key)
		VALUES ($1, $2, 'P-AAA111', $3, 50000000, 'confirmed', 'user:ada', 'user:ada', now(), 'k')`, order, tenant, supplier)
	exec(`INSERT INTO purchase_lines (id, tenant_id, purchase_order_id, position, name, quantity, cost_kobo, treatment)
		VALUES ($1, $2, $3, 0, 'Generator', 1, 50000000, $4)`, line, tenant, order, spend.Track)
	in(func(tx pgx.Tx) error {
		_, _, err := spend.RecordPurchaseTx(ctx, tx, log, tenant, spend.PurchasePay{Order: order, Reference: "P-AAA111", Supplier: "Singer Ltd",
			AmountKobo: 50_000_000, How: "cash", Channel: "cash", Key: "k1", Actor: "user:ada", When: time.Now()})
		return err
	})
	thing := uuid.New()
	exec(`INSERT INTO kept_things (id, tenant_id, name, supplier_id, purchase_order_id, purchase_line_id, unit_no, cost_kobo, received_at, created_by)
		VALUES ($1, $2, 'Generator', $3, $4, $5, 1, 50000000, now(), 'user:ada')`, thing, tenant, supplier, order, line)
	from, to := moneyreport.Month(time.Now())
	report := func() moneyreport.Spending {
		t.Helper()
		var s moneyreport.Spending
		in(func(tx pgx.Tx) error {
			var err error
			s, err = moneyreport.SpendingOf(ctx, tx, tenant, from, to)
			return err
		})
		return s
	}
	if s := report(); s.ToKeepKobo != 50_000_000 || s.Profit.CostsKobo != 0 || s.Profit.ProfitKobo != 0 {
		t.Fatalf("bought to keep, left out: %+v", s)
	}
	sold, price := stock.KeptSold, int64(30_000_000)
	in(func(tx pgx.Tx) error {
		_, err := stock.ChangeThingTx(ctx, tx, tenant, thing, stock.ThingChange{Status: &sold, SalePriceKobo: &price}, "user:ada", "sell", log)
		return err
	})
	if s := report(); s.Profit.InKobo != 30_000_000 || s.Profit.CostsKobo != 50_000_000 || s.Profit.ProfitKobo != -20_000_000 {
		t.Fatalf("sold at a loss: %+v", s.Profit)
	}
	// Had again, it would be counted twice: its cost in profit as sold, and
	// on Things we have. It stays sold while its sale's money counts (PR
	// review fix); a note still changes.
	have, note := stock.KeptHave, "sold to Bayo"
	in(func(tx pgx.Tx) error {
		if _, err := stock.ChangeThingTx(ctx, tx, tenant, thing, stock.ThingChange{Status: &have}, "user:ada", "back", log); !errors.Is(err, stock.ErrPurchase) ||
			!strings.Contains(err.Error(), "stays sold") {
			t.Errorf("back to have: %v", err)
		}
		_, err := stock.ChangeThingTx(ctx, tx, tenant, thing, stock.ThingChange{Note: &note}, "user:ada", "note", log)
		return err
	})
	var status string
	if err := d.Admin.QueryRow(ctx, `SELECT status FROM kept_things WHERE id = $1`, thing).Scan(&status); err != nil || status != "sold" {
		t.Fatalf("status: %s %v", status, err)
	}
	if s := report(); s.Profit.CostsKobo != 50_000_000 || s.Profit.ProfitKobo != -20_000_000 {
		t.Fatalf("after: %+v", s.Profit)
	}
}
