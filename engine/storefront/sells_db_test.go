package storefront_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/storefront"
)

// sellsWith gives the home order form form and publishes the page.
func (w *world) sellsWith(form []byte) {
	w.t.Helper()
	ctx := context.Background()
	w.in(func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `UPDATE storefront_actions SET form = $2 WHERE tenant_id = $1 AND kind = 'order' AND builtin`, w.env.Tenant, form); err != nil {
			return err
		}
		var err error
		w.sf, err = storefront.Publish(ctx, tx, w.env.Tenant, "shops.test", "user:ada", false)
		return err
	})
}

// sendLines puts an order of lines in the inbox, from a buyer told by
// email (a price change naming what went is longer than an SMS takes).
func (w *world) sendLines(lines ...storefront.Line) string {
	w.t.Helper()
	ref := storefront.NewReference("ada-kitchen")
	key, hash := storefront.NewStatusKey()
	w.keys[ref] = key
	total := int64(0)
	for _, l := range lines {
		total += l.LineKobo
	}
	return w.send(ref, hash, storefront.Submission{Kind: "order", Customer: storefront.Customer{Name: "Bola", Email: "bola@example.com"},
		Basket: lines, TotalKobo: total, Fulfilment: "pickup", Payment: "after_accept", Channel: "direct", Submitted: time.Now(), StatusKey: key})
}

// The price check holds an order to the form as published (forms that
// sell review): a basket line of a row the form's basket does not name is
// gone, whatever was posted; and a page that cannot be read leaves prices
// unchecked (tried again, then a note), never every package line gone.
func TestPriceCheckHoldsTheForm(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()
	live := &fakeLive{items: map[string]storefront.Item{
		"1": {ID: "1", Name: "Jollof rice", PriceKobo: kobo(150000), Available: true},
		"2": {ID: "2", Name: "Zobo", PriceKobo: kobo(50000), Available: true}}}
	w.orders.Live = live

	basket, err := storefront.BasketForm("catalogue", []string{"1"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	w.sellsWith(basket)
	ref := w.sendLines(storefront.Line{ID: "1", Name: "Jollof rice", Quantity: 1, PriceKobo: 150000, LineKobo: 150000},
		storefront.Line{ID: "2", Name: "Zobo", Quantity: 2, PriceKobo: 50000, LineKobo: 100000})
	w.drain()
	if o := w.order(ref); o.Status != "price_changed" || o.PriceChange == nil || strings.Join(o.PriceChange.Gone, ",") != "Zobo" ||
		len(o.PriceChange.Basket) != 1 || o.PriceChange.TotalKobo != 150000 {
		t.Fatalf("a row the basket does not offer: %+v %+v", o, o.PriceChange)
	}

	// A package order while the published page cannot be read.
	pkgs, err := storefront.PackagesForm("catalogue", "1", []storefront.Package{{Qty: 2, PriceKobo: 250000}}, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	w.sellsWith(pkgs)
	if _, err := w.env.DB.Admin.Exec(ctx, `UPDATE storefront_snapshots SET model = '"unreadable"'::jsonb WHERE storefront_id = $1`, w.sf.ID); err != nil {
		t.Fatal(err)
	}
	ref = w.sendLines(storefront.Line{ID: "1", Name: "Jollof rice", Quantity: 2, PriceKobo: 125000, LineKobo: 250000, PackageQty: 2, PackageKobo: 250000})
	if err := w.orders.Drain(ctx); err == nil {
		t.Fatal("drained without reading the page")
	}
	w.orders.Now = func() time.Time { return time.Now().Add(storefront.PriceCheckGrace + time.Minute) }
	if _, err := w.env.DB.Admin.Exec(ctx, `UPDATE storefront_inbox SET claimed_until = NULL`); err != nil {
		t.Fatal(err)
	}
	if err := w.orders.Drain(ctx); err != nil {
		t.Fatal(err)
	}
	if o := w.order(ref); o.Status == "price_changed" || o.Status == "cancelled" || !strings.HasPrefix(o.Note, "Prices were not checked") {
		t.Fatalf("unread page: %+v", o)
	}
}
