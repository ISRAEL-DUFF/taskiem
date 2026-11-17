package phoneindex_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// The fill, as review found it should be: an order it cannot open is
// skipped and counted (the business still done); a business another
// replica is filling is left to it; a customer erased while the fill reads
// their order is not indexed; a business that failed waits its turn behind
// those not yet tried.
func TestBackfillReviewFixes(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	a, b := f.business("Mama's Kitchen"), f.business("Ada Stores")
	oa := f.order(a, "+2348031234567", 450000)
	bad := f.order(a, "+2348031234568", 450000)
	if _, err := f.env.DB.Admin.Exec(ctx, `UPDATE storefront_orders SET customer = jsonb_set(customer, '{ct}', '"bm90IGEgc2VhbA=="') WHERE id = $1`, bad.ID); err != nil {
		t.Fatal(err)
	}
	ob := f.order(b, "+2348039999999", 100000)
	indexed := func(id uuid.UUID) bool {
		t.Helper()
		var n int
		if err := f.env.DB.Admin.QueryRow(ctx, `SELECT count(*) FROM customer_phone_index WHERE order_id = $1`, id).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n == 1
	}

	// b is being filled by another replica: left to it.
	hold, err := f.env.DB.Admin.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer hold.Release()
	lockB := `SELECT pg_advisory_lock(hashtextextended('phone-index-backfill:' || $1::text, 0))`
	if _, err := hold.Exec(ctx, lockB, b); err != nil {
		t.Fatal(err)
	}
	if done, err := f.idx.Backfill(ctx, 100); err != nil || done != 1 {
		t.Fatalf("backfill: %d %v", done, err)
	}
	var in, skipped int
	if err := f.env.DB.Admin.QueryRow(ctx, `SELECT indexed, skipped FROM phone_index_backfill WHERE tenant_id = $1 AND done_at IS NOT NULL`, a).Scan(&in, &skipped); err != nil || in != 1 || skipped != 1 {
		t.Fatalf("a: indexed %d, skipped %d, %v", in, skipped, err)
	}
	if !indexed(oa.ID) || indexed(bad.ID) || indexed(ob.ID) {
		t.Fatalf("indexed: a %v, bad %v, b %v", indexed(oa.ID), indexed(bad.ID), indexed(ob.ID))
	}
	if _, err := hold.Exec(ctx, `SELECT pg_advisory_unlock(hashtextextended('phone-index-backfill:' || $1::text, 0))`, b); err != nil {
		t.Fatal(err)
	}

	// b's customer is being erased (their key's row held by the erasure)
	// when the fill reads the order: the fill waits, then sees the key gone
	// and indexes nothing.
	erase, err := hold.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer erase.Rollback(ctx) //nolint:errcheck
	if _, err := erase.Exec(ctx, `SELECT 1 FROM subject_keys WHERE tenant_id = $1 AND subject_id = $2 FOR UPDATE`, b, ob.CustomerID()); err != nil {
		t.Fatal(err)
	}
	type result struct {
		done int
		err  error
	}
	got := make(chan result, 1)
	go func() {
		done, err := f.idx.Backfill(ctx, 100)
		got <- result{done, err}
	}()
	select {
	case r := <-got:
		t.Fatalf("the fill did not wait for the erasure: %+v", r)
	case <-time.After(500 * time.Millisecond):
	}
	if _, err := erase.Exec(ctx, `UPDATE subject_keys SET wrapped_key = NULL, shredded_at = now() WHERE tenant_id = $1 AND subject_id = $2`, b, ob.CustomerID()); err != nil {
		t.Fatal(err)
	}
	if err := erase.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if r := <-got; r.err != nil || r.done != 1 {
		t.Fatalf("after the erasure: %+v", r)
	}
	if indexed(ob.ID) {
		t.Fatal("an erased customer's order was indexed")
	}

	// A business that failed waits an hour a try, behind those never tried.
	c, d := f.business("Failing"), f.business("Fresh")
	f.order(c, "+2348031111111", 1000)
	f.order(d, "+2348032222222", 1000)
	if _, err := f.env.DB.Admin.Exec(ctx, `INSERT INTO phone_index_backfill (tenant_id, done_at, tries, failed_at) VALUES ($1, NULL, 1, now())`, c); err != nil {
		t.Fatal(err)
	}
	due := func() []uuid.UUID {
		t.Helper()
		rows, err := f.env.DB.App.Query(ctx, `SELECT tenant_id FROM taskiem_phone_index_backfill_due(10)`)
		if err != nil {
			t.Fatal(err)
		}
		list, err := pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
		if err != nil {
			t.Fatal(err)
		}
		return list
	}
	if l := due(); len(l) != 1 || l[0] != d {
		t.Fatalf("due with c failing just now: %v", l)
	}
	if _, err := f.env.DB.Admin.Exec(ctx, `UPDATE phone_index_backfill SET failed_at = now() - interval '2 hours' WHERE tenant_id = $1`, c); err != nil {
		t.Fatal(err)
	}
	if l := due(); len(l) != 2 || l[0] != d || l[1] != c {
		t.Fatalf("due: %v (want %v then %v)", l, d, c)
	}
	if done, err := f.idx.Backfill(ctx, 100); err != nil || done != 2 {
		t.Fatalf("the rest: %d %v", done, err)
	}
	if l := due(); len(l) != 0 {
		t.Fatalf("due after: %v", l)
	}
}
