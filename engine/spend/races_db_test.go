package spend_test

import (
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/spend"
	"github.com/israel-duff/taskiem/engine/stock"
)

// A key paid once is paid, however the order is labelled since: an order
// paid under one label and relabelled into two puts its lines in two
// parts, purchase:<key> and purchase:<key>/2, and the second, never
// written, passed as a new payment. Through stock.PayTx too: the order's
// paid amount does not grow on the retry.
func TestPurchaseKeyAfterRelabel(t *testing.T) {
	w := newWorld(t)
	packaging, fuel := w.label("Packaging"), w.label("Fuel")
	pay := func(order uuid.UUID, key string) bool {
		var fresh bool
		w.in(func(tx pgx.Tx) error {
			var err error
			_, fresh, err = spend.RecordPurchaseTx(ctx, tx, w.log, w.tenant, spend.PurchasePay{Order: order, Reference: "P-DDD444", Supplier: "Singer Ltd",
				AmountKobo: 2_000_000, How: "cash", Channel: "cash", Key: key, Actor: "user:ada", When: time.Now()})
			return err
		})
		return fresh
	}
	order, _, _ := w.order("P-DDD444")
	w.line(order, 0, "Nylon bags", 100, 10_000, spend.Record, nil, &packaging.ID, nil)
	w.line(order, 1, "Diesel", 1, 1_000_000, spend.Record, nil, &packaging.ID, nil)
	if !pay(order, "k1") || len(w.entries()) != 1 {
		t.Fatalf("first: %+v", w.entries())
	}
	w.exec(`UPDATE purchase_lines SET label_id = $2 WHERE purchase_order_id = $1 AND position = 1`, order, fuel.ID)
	if pay(order, "k1") || len(w.entries()) != 1 {
		t.Errorf("again after relabelling: %+v", w.entries())
	}

	order2, _, _ := w.order("P-EEE555")
	w.line(order2, 0, "Nylon bags", 100, 10_000, spend.Record, nil, &packaging.ID, nil)
	w.line(order2, 1, "Diesel", 1, 1_000_000, spend.Record, nil, &packaging.ID, nil)
	payTx := func() int64 {
		w.in(func(tx pgx.Tx) error {
			_, err := stock.PayTx(ctx, tx, w.tenant, order2, 1_000_000, stock.Cash, "user:ada", "k2", w.log)
			return err
		})
		var paid int64
		if err := w.d.Admin.QueryRow(ctx, `SELECT paid_kobo FROM purchase_orders WHERE id = $1`, order2).Scan(&paid); err != nil {
			t.Fatal(err)
		}
		return paid
	}
	if paid := payTx(); paid != 1_000_000 {
		t.Fatalf("paid: %d", paid)
	}
	w.exec(`UPDATE purchase_lines SET label_id = $2 WHERE purchase_order_id = $1 AND position = 1`, order2, fuel.ID)
	if paid := payTx(); paid != 1_000_000 || len(w.entries()) != 2 {
		t.Fatalf("PayTx again after relabelling: paid %d, entries %+v", paid, w.entries())
	}
}

// A key that paid one order, sent to pay another (PR review fix): refused
// as used, not answered as paid while nothing is written; in cash or by
// transfer alike. The same order's retry is still answered as it was.
func TestPurchaseKeyOtherOrder(t *testing.T) {
	w := newWorld(t)
	packaging := w.label("Packaging")
	first, _, _ := w.order("P-FFF666")
	w.line(first, 0, "Nylon bags", 100, 10_000, spend.Record, nil, &packaging.ID, nil)
	second, _, _ := w.order("P-GGG777")
	w.line(second, 0, "Nylon bags", 100, 10_000, spend.Record, nil, &packaging.ID, nil)
	pay := func(order uuid.UUID, how string) error {
		return db.InTenantTx(ctx, w.d.App, []uuid.UUID{w.tenant}, func(tx pgx.Tx) error {
			_, err := stock.PayTx(ctx, tx, w.tenant, order, 500_000, how, "user:ada", "api-same", w.log)
			return err
		})
	}
	paid := func(order uuid.UUID) int64 {
		var n int64
		if err := w.d.Admin.QueryRow(ctx, `SELECT paid_kobo FROM purchase_orders WHERE id = $1`, order).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	if err := pay(first, stock.Cash); err != nil {
		t.Fatal(err)
	}
	if err := pay(first, stock.Cash); err != nil || paid(first) != 500_000 {
		t.Fatalf("the same order again: %v, paid %d", err, paid(first))
	}
	for _, how := range []string{stock.Cash, stock.Transfer} {
		if err := pay(second, how); !errors.Is(err, stock.ErrKeyUsed) || !strings.Contains(err.Error(), "paid another order") {
			t.Errorf("another order, %s: %v", how, err)
		}
	}
	if paid(second) != 0 || len(w.entries()) != 1 {
		t.Fatalf("written: paid %d, %+v", paid(second), w.entries())
	}
}

// race runs second in its own transaction while first's is open, and
// says whether second waited for a lock; then it commits first and gives
// second's error (second is committed when it has none).
func race(t *testing.T, w *world, first, second string, args1, args2 []any) (waited bool, err error) {
	t.Helper()
	tx1, err := w.d.Admin.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx1.Rollback(ctx) }()
	if _, err := tx1.Exec(ctx, first, args1...); err != nil {
		t.Fatal(err)
	}
	tx2, err := w.d.Admin.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx2.Rollback(ctx) }()
	var pid int
	if err := tx2.QueryRow(ctx, `SELECT pg_backend_pid()`).Scan(&pid); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := tx2.Exec(ctx, second, args2...)
		done <- err
	}()
	var err2 error
	finished := false
	for deadline := time.Now().Add(10 * time.Second); !finished && !waited && time.Now().Before(deadline); {
		select {
		case err2 = <-done:
			finished = true
			continue
		default:
		}
		var n int
		if err := w.d.Admin.QueryRow(ctx, `SELECT count(*) FROM pg_stat_activity WHERE pid = $1 AND wait_event_type = 'Lock'`, pid).Scan(&n); err != nil {
			t.Fatal(err)
		}
		waited = n > 0
		time.Sleep(10 * time.Millisecond)
	}
	if !finished && !waited {
		t.Fatal("the second neither finished nor waited")
	}
	if err := tx1.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if !finished {
		err2 = <-done
	}
	if err2 == nil {
		return waited, tx2.Commit(ctx)
	}
	return waited, err2
}

func (w *world) chains() int {
	w.t.Helper()
	var n int
	if err := w.d.Admin.QueryRow(ctx, `SELECT count(*) FROM spend_labels l JOIN spend_labels k ON k.id = l.merged_into
		WHERE l.tenant_id = $1 AND k.merged_into IS NOT NULL`, w.tenant).Scan(&n); err != nil {
		w.t.Fatal(err)
	}
	return n
}

// Two merges at once make no chain (§5.4; 00505): merging L into K while
// K is merged into M, in either order, the second waits for the first
// and is refused.
func TestLabelMergeRace(t *testing.T) {
	w := newWorld(t)
	merge := `UPDATE spend_labels SET merged_into = $2 WHERE id = $1`
	// L into K first, then K into M.
	l, k, m := w.label("L1"), w.label("K1"), w.label("M1")
	waited, err := race(t, w, merge, merge, []any{l.ID, k.ID}, []any{k.ID, m.ID})
	if !waited || err == nil || !strings.Contains(err.Error(), "move them first") {
		t.Errorf("K into M while L goes into K: waited %v, %v", waited, err)
	}
	// K into M first, then L into K.
	l, k, m = w.label("L2"), w.label("K2"), w.label("M2")
	waited, err = race(t, w, merge, merge, []any{k.ID, m.ID}, []any{l.ID, k.ID})
	if !waited || err == nil || !strings.Contains(err.Error(), "not itself merged") {
		t.Errorf("L into K while K goes into M: waited %v, %v", waited, err)
	}
	if n := w.chains(); n != 0 {
		t.Fatalf("chains: %d", n)
	}
	// A merge alone still goes in, into the label kept.
	x := w.label("X")
	w.in(func(tx pgx.Tx) error {
		got, err := spend.MergeTx(ctx, tx, w.tenant, x.ID, k.ID)
		if err != nil || got.MergedInto == nil || *got.MergedInto != m.ID {
			t.Errorf("merged: %+v %v", got, err)
		}
		return nil
	})

	// MergeTx into a merged label locks the label kept for update, so it
	// waits for anyone holding it (here only shared, which the database's
	// own check does not wait for) and reads it again then.
	into, kept, y := w.label("Into"), w.label("Kept"), w.label("Y")
	w.exec(merge, into.ID, kept.ID)
	tx1, err := w.d.Admin.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx1.Rollback(ctx) }()
	if _, err := tx1.Exec(ctx, `SELECT 1 FROM spend_labels WHERE id = $1 FOR SHARE`, kept.ID); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		done <- db.InTenantTx(ctx, w.d.App, []uuid.UUID{w.tenant}, func(tx pgx.Tx) error {
			got, err := spend.MergeTx(ctx, tx, w.tenant, y.ID, into.ID)
			if err == nil && (got.MergedInto == nil || *got.MergedInto != kept.ID) {
				err = errors.New("not merged into the label kept")
			}
			return err
		})
	}()
	select {
	case err := <-done:
		t.Fatalf("MergeTx did not wait for the label kept: %v", err)
	case <-time.After(500 * time.Millisecond):
	}
	if err := tx1.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if n := w.chains(); n != 0 {
		t.Fatalf("chains: %d", n)
	}
}

// Two merges at once that lock the same labels in a different order (A
// into B while B is kept as C, which takes A, B then C; and C into E,
// which takes C, E, then moves B) do not deadlock: merges of a business
// take its merge lock first, one after the other (PR review fix).
func TestLabelMergesAtOnceNoDeadlock(t *testing.T) {
	w := newWorld(t)
	for round := range 20 {
		n := strconv.Itoa(round)
		a, b, c, e := w.label("A"+n), w.label("B"+n), w.label("C"+n), w.label("E"+n)
		w.exec(`UPDATE spend_labels SET merged_into = $2 WHERE id = $1`, b.ID, c.ID)
		start := make(chan struct{})
		errs := make(chan error, 2)
		for _, m := range [][2]uuid.UUID{{a.ID, b.ID}, {c.ID, e.ID}} {
			go func() {
				<-start
				errs <- db.InTenantTx(ctx, w.d.App, []uuid.UUID{w.tenant}, func(tx pgx.Tx) error {
					_, err := spend.MergeTx(ctx, tx, w.tenant, m[0], m[1])
					return err
				})
			}()
		}
		close(start)
		for range 2 {
			err := <-errs
			var pe *pgconn.PgError
			switch {
			case err == nil, errors.Is(err, spend.ErrLabel):
			case errors.As(err, &pe) && pe.Code == "40P01":
				t.Fatalf("round %d: deadlock: %v", round, err)
			case errors.As(err, &pe) && pe.Code == "23514": // refused as a chain
			default:
				t.Fatalf("round %d: %v", round, err)
			}
		}
		if n := w.chains(); n != 0 {
			t.Fatalf("round %d: chains %d", round, n)
		}
	}
}
