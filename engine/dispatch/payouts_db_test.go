package dispatch_test

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/dispatch"
	"github.com/israel-duff/taskiem/engine/leaks"
	"github.com/israel-duff/taskiem/engine/publicweb"
	"github.com/israel-duff/taskiem/engine/storefront"
)

// deliver takes an order through to delivered by rider at fee; cash
// leaves it paid at the door in cash, owed by the rider.
func (w *world) deliver(rider uuid.UUID, fee int64, cash bool) string {
	w.t.Helper()
	payment := "none"
	if cash {
		payment = "on_delivery"
	}
	ref := w.order("Lekki", payment)
	out, err := w.d.Offer(context.Background(), w.env.Tenant, ref, dispatch.OfferIn{Rider: &rider, FeeKobo: &fee})
	if err != nil || out.Delivery == nil {
		w.t.Fatalf("offer: %+v %v", out, err)
	}
	x := *out.Delivery
	w.must(x, dispatch.Accept)
	w.must(x, dispatch.PickedUp)
	w.must(x, dispatch.Arrived)
	if cash {
		w.must(x, dispatch.Cash)
	} else {
		w.must(x, dispatch.Delivered)
	}
	return ref
}

func (w *world) lines() map[string]dispatch.PayLine {
	w.t.Helper()
	out := map[string]dispatch.PayLine{}
	w.in(func(tx pgx.Tx) error {
		list, err := w.d.PayLinesTx(context.Background(), tx, w.env.Tenant)
		for _, l := range list {
			out[l.Rider] = l
		}
		return err
	})
	return out
}

// A payout pays each rider their fees since the last less the cash they
// owe, once; a rider with no bank account waits; a failed or unapproved
// payout leaves the deliveries for next time.
func TestRiderPayouts(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()
	musa := w.rider("Musa", "+2348031110001", "Lekki")
	tunde := w.rider("Tunde", "+2348031110002", "Lekki")
	ngozi := w.rider("Ngozi", "+2348031110003", "Lekki")

	w.deliver(musa.ID, 400000, false)
	w.deliver(musa.ID, 400000, true) // Musa owes the order's cash, less than his fees
	w.deliver(tunde.ID, 80000, false)
	w.deliver(ngozi.ID, 50000, false)

	lines := w.lines()
	m := lines["Musa"]
	if m.Deliveries != 2 || m.FeesKobo != 800000 || m.OwedKobo == 0 || m.OwedKobo >= m.FeesKobo || m.Bank != "" {
		t.Fatalf("Musa: %+v", m)
	}
	if want := max(0, m.FeesKobo-m.OwedKobo); m.NetKobo != want {
		t.Fatalf("Musa's net %d, want %d", m.NetKobo, want)
	}
	if l := lines["Tunde"]; l.Deliveries != 1 || l.NetKobo != 80000 {
		t.Fatalf("Tunde: %+v", l)
	}

	// Bank accounts: checked, sealed, shown by the last four digits.
	w.in(func(tx pgx.Tx) error {
		if _, err := w.d.SetBankTx(ctx, tx, w.env.Tenant, tunde.ID, dispatch.Bank{BankCode: "035", AccountNumber: "12345"}, "user:ada"); err == nil {
			t.Error("a short account number was kept")
		}
		return nil
	})
	w.in(func(tx pgx.Tx) error {
		shown, err := w.d.SetBankTx(ctx, tx, w.env.Tenant, tunde.ID, dispatch.Bank{BankCode: "035", BankName: "Wema Bank", AccountNumber: "0123456789"}, "user:ada")
		if shown != "Wema Bank, ending 6789" {
			t.Errorf("shown %q", shown)
		}
		if err != nil {
			return err
		}
		_, err = w.d.SetBankTx(ctx, tx, w.env.Tenant, ngozi.ID, dispatch.Bank{BankCode: "058", AccountNumber: "0987654321"}, "user:ada")
		return err
	})
	var plain int
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT count(*) FROM riders WHERE bank::text LIKE '%0123456789%'`).Scan(&plain); err != nil || plain != 0 {
		t.Fatalf("an account number was kept in the clear: %d %v", plain, err)
	}

	// Musa's cash owed is less than his fees but he has no account: named,
	// left for next time.
	batch := uuid.New()
	b, err := w.d.PrepareBatch(ctx, w.env.Tenant, batch)
	if err != nil {
		t.Fatal(err)
	}
	if len(b.Payouts) != 2 || len(b.NotPaid) != 1 || b.NotPaid[0] != "Musa" || b.TotalKobo != 130000 {
		t.Fatalf("batch %+v", b)
	}
	again, err := w.d.PrepareBatch(ctx, w.env.Tenant, batch)
	if err != nil || len(again.Payouts) != 2 || again.Payouts[0].ID != b.Payouts[0].ID {
		t.Fatalf("again %+v %v", again, err)
	}
	by := map[string]dispatch.Payout{}
	for _, p := range b.Payouts {
		by[p.Rider] = p
	}
	if p := by["Tunde"]; p.AmountKobo != 80000 || p.AccountNumber != "0123456789" || p.BankCode != "035" {
		t.Fatalf("Tunde's payout %+v", p)
	}
	// Bound to this batch, the deliveries are not due again.
	if l := w.lines(); l["Tunde"].Deliveries != 0 || l["Musa"].Deliveries != 2 {
		t.Fatalf("lines while paying %+v", l)
	}

	// Tunde paid (the bank's name kept), Ngozi's failed: hers is due again.
	paid, failed, err := w.d.FinishBatch(ctx, w.env.Tenant, batch, []dispatch.Result{
		{PayoutID: by["Tunde"].ID, Status: "paid", AccountName: "TUNDE ADE"},
		{PayoutID: by["Ngozi"].ID, Status: "failed"}}, false)
	if err != nil || paid != 1 || failed != 1 {
		t.Fatalf("finish: %d %d %v", paid, failed, err)
	}
	if l := w.lines(); l["Ngozi"].Deliveries != 1 || l["Ngozi"].NetKobo != 50000 {
		t.Fatalf("Ngozi after failing: %+v", l["Ngozi"])
	}
	var tunde2 string
	w.in(func(tx pgx.Tx) error {
		r, err := w.riders.GetTx(ctx, tx, w.env.Tenant, tunde.ID)
		tunde2 = r.Bank
		return err
	})
	if tunde2 != "Wema Bank, ending 6789, TUNDE ADE" {
		t.Fatalf("Tunde's bank %q", tunde2)
	}
	if paid, failed, err := w.d.FinishBatch(ctx, w.env.Tenant, batch, nil, false); err != nil || paid+failed != 0 {
		t.Fatalf("finishing twice: %d %d %v", paid, failed, err)
	}

	// Musa's account set: his fees are paid less what he owes, which the
	// payout settles.
	w.in(func(tx pgx.Tx) error {
		_, err := w.d.SetBankTx(ctx, tx, w.env.Tenant, musa.ID, dispatch.Bank{BankCode: "044", AccountNumber: "1112223334"}, "user:ada")
		return err
	})
	b2, err := w.d.PrepareBatch(ctx, w.env.Tenant, uuid.New())
	if err != nil {
		t.Fatal(err)
	}
	var mp dispatch.Payout
	for _, p := range b2.Payouts {
		if p.Rider == "Musa" {
			mp = p
		}
	}
	if mp.FeesKobo != 800000 || mp.AmountKobo != m.NetKobo || mp.OwedKobo != m.OwedKobo {
		t.Fatalf("Musa's payout %+v (line %+v)", mp, m)
	}
	// Not approved: everything waits for next time, Musa still owes.
	if _, failed, err := w.d.FinishBatch(ctx, w.env.Tenant, b2.ID, nil, true); err != nil || failed != len(b2.Payouts) {
		t.Fatalf("cancel: %d %v", failed, err)
	}
	if l := w.lines(); l["Musa"].Deliveries != 2 || l["Musa"].OwedKobo != m.OwedKobo {
		t.Fatalf("Musa after a cancelled payout %+v", l["Musa"])
	}
	b3, err := w.d.PrepareBatch(ctx, w.env.Tenant, uuid.New())
	if err != nil {
		t.Fatal(err)
	}
	var results []dispatch.Result
	for _, p := range b3.Payouts {
		results = append(results, dispatch.Result{PayoutID: p.ID, Status: "paid"})
	}
	if paid, _, err := w.d.FinishBatch(ctx, w.env.Tenant, b3.ID, results, false); err != nil || paid != 2 {
		t.Fatalf("paying Musa and Ngozi: %d %v", paid, err)
	}
	if l := w.lines(); len(l) != 0 {
		t.Fatalf("nothing is due after paying: %+v", l)
	}
	w.in(func(tx pgx.Tx) error {
		owed, err := w.d.OwedTx(ctx, tx, w.env.Tenant)
		if len(owed) != 0 {
			t.Errorf("Musa's cash was not netted: %+v", owed)
		}
		list, err2 := w.d.PayoutsTx(ctx, tx, w.env.Tenant, 50)
		if len(list) != 6 {
			t.Errorf("payouts %d", len(list))
		}
		if err == nil {
			err = err2
		}
		return err
	})
}

// A rider whose cash owed covers their fees is settled at once, with
// nothing to pay.
func TestPayoutAllNetted(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()
	musa := w.rider("Musa", "+2348031110001", "Lekki")
	w.deliver(musa.ID, 50000, true)
	owed := w.lines()["Musa"].OwedKobo
	b, err := w.d.PrepareBatch(ctx, w.env.Tenant, uuid.New())
	if err != nil || len(b.Payouts) != 0 || len(b.NotPaid) != 0 {
		t.Fatalf("batch %+v %v", b, err)
	}
	l := w.lines()["Musa"]
	if l.Deliveries != 0 || l.OwedKobo != owed-50000 {
		t.Fatalf("after netting %+v (owed %d)", l, owed)
	}
}

// The customer rates a delivered order once, 1 to 5, from its status
// page (through the public role); the rider's average shows it.
func TestRateDelivery(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()
	musa := w.rider("Musa", "+2348031110001", "Lekki")
	pub := publicweb.DBOrderRate{Pool: w.env.DB.PublicPool(t, 1)}

	ref := w.order("Lekki", "none")
	hash := storefront.StatusHash(w.keys[ref])
	if _, ok, err := pub.Delivery(ctx, ref, hash); err != nil || ok {
		t.Fatalf("before an offer: %v %v", ok, err)
	}
	out, err := w.d.Offer(ctx, w.env.Tenant, ref, dispatch.OfferIn{Rider: &musa.ID})
	if err != nil {
		t.Fatal(err)
	}
	w.must(*out.Delivery, dispatch.Accept)
	if x, ok, err := pub.Delivery(ctx, ref, hash); err != nil || !ok || x.Status != "accepted" || x.CanRate {
		t.Fatalf("accepted: %+v %v %v", x, ok, err)
	}
	if ok, err := pub.Rate(ctx, ref, hash, 5); err != nil || ok {
		t.Fatalf("rated before delivery: %v %v", ok, err)
	}
	w.must(*out.Delivery, dispatch.PickedUp)
	w.must(*out.Delivery, dispatch.Arrived)
	w.must(*out.Delivery, dispatch.Delivered)
	if x, _, err := pub.Delivery(ctx, ref, hash); err != nil || !x.CanRate {
		t.Fatalf("delivered: %+v %v", x, err)
	}
	if ok, err := pub.Rate(ctx, ref, storefront.StatusHash("not-the-key"), 5); err != nil || ok {
		t.Fatalf("another key: %v %v", ok, err)
	}
	if ok, err := pub.Rate(ctx, ref, hash, 0); err != nil || ok {
		t.Fatalf("0: %v %v", ok, err)
	}
	if ok, err := pub.Rate(ctx, ref, hash, 4); err != nil || !ok {
		t.Fatalf("4: %v %v", ok, err)
	}
	if ok, err := pub.Rate(ctx, ref, hash, 1); err != nil || ok {
		t.Fatalf("rated twice: %v %v", ok, err)
	}
	if x, _, err := pub.Delivery(ctx, ref, hash); err != nil || x.Rating != 4 || x.CanRate {
		t.Fatalf("rated: %+v %v", x, err)
	}
	w.in(func(tx pgx.Tx) error {
		r, err := w.riders.GetTx(ctx, tx, w.env.Tenant, musa.ID)
		if r.Rating == nil || *r.Rating != 4 || r.Ratings != 1 {
			t.Errorf("Musa's rating %v of %d", r.Rating, r.Ratings)
		}
		return err
	})
}

// Cash a rider owes beyond the business's limit is a leak, which resolves
// itself when they hand it in.
func TestRiderOwesLeak(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()
	musa := w.rider("Musa", "+2348031110001", "Lekki")
	w.deliver(musa.ID, 50000, true)
	owed := w.lines()["Musa"].OwedKobo
	det := &leaks.Detector{Pool: w.env.DB.App}
	if n, err := det.Tenant(ctx, w.env.Tenant); err != nil || n != 0 {
		t.Fatalf("under the default limit: %d %v", n, err)
	}
	w.in(func(tx pgx.Tx) error {
		s, err := dispatch.GetSettings(ctx, tx, w.env.Tenant)
		if err != nil {
			return err
		}
		s.OweLimitKobo = owed - 100
		_, err = dispatch.SaveSettings(ctx, tx, w.env.Tenant, s, "user:ada")
		return err
	})
	if n, err := det.Tenant(ctx, w.env.Tenant); err != nil || n != 1 {
		t.Fatalf("over the limit: %d %v", n, err)
	}
	var amount int64
	var detail string
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT amount_kobo, detail FROM money_leaks WHERE kind = 'rider_owes' AND subject = $1 AND status = 'open'`, musa.ID.String()).
		Scan(&amount, &detail); err != nil || amount != owed || !strings.Contains(detail, "a rider owes ₦6,500 in cash taken at the door") {
		t.Fatalf("leak %d %q %v", amount, detail, err)
	}
	w.in(func(tx pgx.Tx) error {
		_, err := w.d.RemitTx(ctx, tx, w.env.Tenant, musa.ID, owed, "cash", nil, "user:ada")
		return err
	})
	if _, err := det.Tenant(ctx, w.env.Tenant); err != nil {
		t.Fatal(err)
	}
	var status string
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT status FROM money_leaks WHERE kind = 'rider_owes'`).Scan(&status); err != nil || status != "resolved" {
		t.Fatalf("after handing it in: %q %v", status, err)
	}
}
