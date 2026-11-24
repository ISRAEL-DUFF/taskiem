package dispatch_test

import (
	"context"
	"testing"

	"github.com/israel-duff/taskiem/engine/dispatch"
)

// A business that turned its Online shop off gets no alerts from the
// dispatch sweep (a quiet rider); turned on again, it does.
func TestSweepRaisesNothingWhenTheShopIsOff(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t)
	w.rider("Musa Bello", "+2348031110001", "Lekki")
	out, err := w.d.Offer(ctx, w.env.Tenant, w.order("Lekki", "none"), dispatch.OfferIn{})
	if err != nil {
		t.Fatal(err)
	}
	w.must(*out.Delivery, dispatch.Accept)
	if _, err := w.env.DB.Admin.Exec(ctx, `UPDATE deliveries SET accepted_at = now() - interval '6 hours' WHERE id = $1`, out.Delivery.ID); err != nil {
		t.Fatal(err)
	}
	set := func(on bool) {
		t.Helper()
		if _, err := w.env.DB.Admin.Exec(ctx, `INSERT INTO business_areas (tenant_id, area, enabled, updated_by) VALUES ($1, 'storefront', $2, 'test')
			ON CONFLICT (tenant_id, area) DO UPDATE SET enabled = $2`, w.env.Tenant, on); err != nil {
			t.Fatal(err)
		}
	}
	set(false)
	if err := w.d.Sweep(ctx); err != nil {
		t.Fatal(err)
	}
	if n := w.alerts(dispatch.AlertQuiet); n != 0 {
		t.Fatalf("%d alerts with the shop off", n)
	}
	set(true)
	if err := w.d.Sweep(ctx); err != nil {
		t.Fatal(err)
	}
	if n := w.alerts(dispatch.AlertQuiet); n != 1 {
		t.Fatalf("%d alerts with the shop on", n)
	}
}
