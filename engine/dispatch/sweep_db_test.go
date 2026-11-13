package dispatch_test

import (
	"context"
	"testing"

	"github.com/israel-duff/taskiem/engine/dispatch"
)

// One expired offer that cannot be passed on does not hold up the rest of
// a business's sweep: the others are passed, and the failure is reported.
func TestOneBadOfferDoesNotBlockTheSweep(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t)
	w.rider("Musa Bello", "+2348031110001", "Lekki")
	w.rider("Chidi Okeke", "+2348031110002", "Yaba")
	bad, err := w.d.Offer(ctx, w.env.Tenant, w.order("Lekki", "none"), dispatch.OfferIn{})
	if err != nil {
		t.Fatal(err)
	}
	good, err := w.d.Offer(ctx, w.env.Tenant, w.order("Yaba", "none"), dispatch.OfferIn{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.env.DB.Admin.Exec(ctx, `UPDATE deliveries SET expires_at = now() - interval '1 minute' WHERE tenant_id = $1`, w.env.Tenant); err != nil {
		t.Fatal(err)
	}
	// The first offer cannot be passed: its change is refused.
	for _, q := range []string{
		`CREATE FUNCTION test_bad_offer() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'this delivery is broken'; END $$`,
		`CREATE TRIGGER test_bad_offer BEFORE UPDATE ON deliveries FOR EACH ROW WHEN (OLD.id = '` + bad.Delivery.ID.String() + `' AND NEW.status = 'passed')
			EXECUTE FUNCTION test_bad_offer()`,
	} {
		if _, err := w.env.DB.Admin.Exec(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() {
		_, _ = w.env.DB.Admin.Exec(ctx, `DROP TRIGGER IF EXISTS test_bad_offer ON deliveries`)
		_, _ = w.env.DB.Admin.Exec(ctx, `DROP FUNCTION IF EXISTS test_bad_offer()`)
	})
	if err := w.d.Sweep(ctx); err == nil {
		t.Error("the failed offer was not reported")
	}
	status := func(x *dispatch.Delivery) (s string) {
		if err := w.env.DB.Admin.QueryRow(ctx, `SELECT status FROM deliveries WHERE id = $1`, x.ID).Scan(&s); err != nil {
			t.Fatal(err)
		}
		return s
	}
	if status(good.Delivery) != "passed" || status(bad.Delivery) != "offered" {
		t.Fatalf("the good offer is %s and the bad one %s; want passed and offered", status(good.Delivery), status(bad.Delivery))
	}
}
