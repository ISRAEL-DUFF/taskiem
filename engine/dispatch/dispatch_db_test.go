package dispatch_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/dispatch"
	"github.com/israel-duff/taskiem/engine/moneylog"
	"github.com/israel-duff/taskiem/engine/reach"
	"github.com/israel-duff/taskiem/engine/riders"
	rt "github.com/israel-duff/taskiem/engine/runtime/runtimetest"
	"github.com/israel-duff/taskiem/engine/sender"
	"github.com/israel-duff/taskiem/engine/storefront"
	"github.com/israel-duff/taskiem/templates"
)

type world struct {
	t         *testing.T
	env       *rt.Env
	orders    *storefront.Orders
	riders    *riders.Riders
	d         *dispatch.Dispatch
	sf        storefront.Storefront
	key       []byte
	delivered []string
	issued    int
	n         int
	// keys are the orders' status keys, by reference.
	keys map[string]string
}

func newWorld(t *testing.T) *world {
	env := rt.New(t)
	ctx := context.Background()
	hash := bytes.Repeat([]byte{8}, 32)
	msgs := &sender.Queue{Vault: env.Vault, HashKey: hash}
	w := &world{t: t, env: env, keys: map[string]string{}}
	w.key, _ = storefront.InboxKey(bytes.Repeat([]byte{4}, 32))
	w.orders = &storefront.Orders{Pool: env.DB.App, Vault: env.Vault, InboxKey: w.key, PreviewDomain: "shops.test", Messages: msgs,
		Start: env.Store.Start, Signal: env.Store.DeliverSignalOnce, MoneyLog: &moneylog.Log{Pool: env.DB.App, PII: env.Vault}}
	// Account numbers at the door, as invoicing issues them: open, for the
	// order, on the business's rail.
	w.orders.IssueTransfer = func(ctx context.Context, tenant, order uuid.UUID, connection string, amount int64, description string, payer map[string]any, minutes int) error {
		w.issued++
		_, err := env.DB.Admin.Exec(ctx, `INSERT INTO customer_invoice_accounts (id, tenant_id, order_id, seq, kind, connector, connection, payment_id, amount_kobo, status,
			account_number, bank_name, account_name, expires_at) VALUES ($1, $2, $3, $4, 'transfer', 'iswallet', 'taskiem', $5, $6, 'open', '9900000001', 'Wema Bank', 'ADA STORES',
			now() + make_interval(mins => $7))`, uuid.New(), tenant, order, w.issued, "tot_"+uuid.NewString()[:8], amount, minutes)
		return err
	}
	book := &reach.Book{Pool: env.DB.App, Vault: env.Vault, HashKey: hash}
	w.riders = &riders.Riders{Pool: env.DB.App, Vault: env.Vault, Book: book, Messages: msgs, PageBase: "https://forms.test"}
	w.d = &dispatch.Dispatch{Pool: env.DB.App, Vault: env.Vault, Riders: w.riders, Orders: w.orders, Chat: []string{"whatsapp", "telegram"},
		MoneyLog: &moneylog.Log{Pool: env.DB.App, PII: env.Vault},
		Delivered: func(_ context.Context, _ uuid.UUID, ord storefront.Order) error {
			w.delivered = append(w.delivered, ord.Reference)
			return nil
		},
		StatusLink: w.orders.StatusLink}
	w.orders.AfterPaid = w.d.OrderPaid
	lib := templates.Default()
	tpl, _ := lib.Get("storefront-order")
	def, err := tpl.Instantiate(map[string]any{}, "Storefront order")
	if err != nil {
		t.Fatal(err)
	}
	wf := env.Publish(t, string(def))
	w.in(func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `INSERT INTO triggers (id, tenant_id, workflow_id, version, environment, type, storefront_action) VALUES ($1, $2, $3, 1, 'prod', 'storefront', 'order')`,
			uuid.New(), env.Tenant, wf); err != nil {
			return err
		}
		if _, err := storefront.Save(ctx, tx, env.Tenant, "ada-stores", storefront.Card{Name: "Ada Stores", Address: "4 Akoka Road, Yaba"}, nil, storefront.Columns{}, "user:ada"); err != nil {
			return err
		}
		if _, err := storefront.SaveAction(ctx, tx, env.Tenant, storefront.Action{Kind: "order", Enabled: true, Payment: "after_accept",
			Options: storefront.ActionOptions{Delivery: true, PayOnDelivery: true, Connection: "iswallet/taskiem",
				Zones: []storefront.Zone{{Name: "Lekki", FeeKobo: 150000}, {Name: "Yaba", FeeKobo: 100000}}}}); err != nil {
			return err
		}
		var err error
		w.sf, err = storefront.Publish(ctx, tx, env.Tenant, "shops.test", "user:ada", false)
		return err
	})
	return w
}

func (w *world) in(fn func(pgx.Tx) error) {
	w.t.Helper()
	if err := db.InTenantTx(context.Background(), w.env.DB.App, []uuid.UUID{w.env.Tenant}, fn); err != nil {
		w.t.Fatal(err)
	}
}

// order makes an order to zone, paid as payment says, and accepts it.
func (w *world) order(zone, payment string) string {
	w.t.Helper()
	ctx := context.Background()
	ref := storefront.NewReference("ada-stores")
	key, hash := storefront.NewStatusKey()
	w.keys[ref] = key
	w.n++
	phone := fmt.Sprintf("+23480399%05d", w.n+int(time.Now().UnixNano()%50000)*2)
	sub := storefront.Submission{Kind: "order", Customer: storefront.Customer{Name: "Bola Ade", Phone: phone, Address: "12 Admiralty Way",
		Landmark: "opposite the Shoprite gate"}, Basket: []storefront.Line{{ID: "1", Name: "Jollof rice", Quantity: 2, PriceKobo: 250000, LineKobo: 500000}},
		TotalKobo: 650000, Zone: zone, DeliveryKobo: 150000, Fulfilment: "delivery", Payment: payment, Channel: "direct", Submitted: time.Now(), StatusKey: key}
	ct, err := storefront.SealSubmission(w.key, w.sf.ID, ref, sub)
	if err != nil {
		w.t.Fatal(err)
	}
	idem := make([]byte, 32)
	copy(idem, ref)
	var got *string
	if err := w.env.DB.PublicPool(w.t, 1).QueryRow(ctx, `SELECT taskiem_public_submit($1, 'order', $2, $3, $4, $5)`, w.sf.ID, ref, idem, hash, ct).Scan(&got); err != nil || got == nil {
		w.t.Fatalf("submit: %v %v", got, err)
	}
	if err := w.orders.Drain(ctx); err != nil {
		w.t.Fatal(err)
	}
	if _, err := w.orders.Decide(ctx, w.env.Tenant, ref, "accept", "", "user:ada"); err != nil {
		w.t.Fatal(err)
	}
	return ref
}

// rider adds a rider reached on WhatsApp (tapped in).
func (w *world) rider(name, phone string, zones ...string) riders.Rider {
	w.t.Helper()
	ctx := context.Background()
	w.in(func(tx pgx.Tx) error {
		_, err := w.riders.Book.LinkTx(ctx, tx, w.env.Tenant, phone, reach.WhatsApp, phone)
		return err
	})
	x, err := w.riders.Add(ctx, w.env.Tenant, riders.In{Name: &name, Phone: &phone, Zones: &zones}, "user:ada")
	if err != nil {
		w.t.Fatal(err)
	}
	return x
}

func (w *world) get(ref string) storefront.Order {
	w.t.Helper()
	var o storefront.Order
	w.in(func(tx pgx.Tx) error {
		var err error
		o, err = w.orders.GetOrder(context.Background(), tx, w.env.Tenant, ref)
		return err
	})
	return o
}

func (w *world) held(ref string) dispatch.Delivery {
	w.t.Helper()
	var x dispatch.Delivery
	w.in(func(tx pgx.Tx) error {
		o, err := w.orders.GetOrder(context.Background(), tx, w.env.Tenant, ref)
		if err != nil {
			return err
		}
		x, err = w.d.HeldTx(context.Background(), tx, w.env.Tenant, o.ID)
		return err
	})
	return x
}

// stages are the rider messages queued for a delivery, in order.
func (w *world) stages(delivery uuid.UUID) []string {
	w.t.Helper()
	rows, err := w.env.DB.Admin.Query(context.Background(), `SELECT step_id FROM whatsapp_outbox WHERE kind = 'rider' AND run_id = $1 ORDER BY created_at, id`, delivery)
	if err != nil {
		w.t.Fatal(err)
	}
	out, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		w.t.Fatal(err)
	}
	return out
}

func (w *world) alerts(kind string) int {
	w.t.Helper()
	var n int
	if err := w.env.DB.Admin.QueryRow(context.Background(), `SELECT count(*) FROM alerts WHERE tenant_id = $1 AND kind = $2`, w.env.Tenant, kind).Scan(&n); err != nil {
		w.t.Fatal(err)
	}
	return n
}

func (w *world) press(x dispatch.Delivery, action string) (dispatch.Tap, error) {
	return w.d.Press(context.Background(), w.env.Tenant, x.ID, action)
}

func (w *world) must(x dispatch.Delivery, action string) dispatch.Tap {
	w.t.Helper()
	t, err := w.press(x, action)
	if err != nil {
		w.t.Fatalf("%s: %v", action, err)
	}
	return t
}

// By rotation an order goes to the free riders covering its area first;
// Can't and no answer pass it on; with none left the owner is told. Three
// unanswered offers in a day mark a rider not answering, until they tap.
func TestOfferByRotation(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()
	musa := w.rider("Musa", "+2348031110001", "Lekki")
	tunde := w.rider("Tunde", "+2348031110002", "Lekki")
	w.rider("Bola", "+2348031110003", "Yaba")
	// Tunde said he is busy: free riders come first.
	if _, err := w.riders.Say(ctx, "+2348031110002", reach.WhatsApp, "+2348031110002", riders.Said{State: "busy", Until: time.Now().Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	ref := w.order("Lekki", "none")
	out, err := w.d.Offer(ctx, w.env.Tenant, ref, dispatch.OfferIn{Ask: true})
	if err != nil || out.Status != "offered" || out.Delivery.RiderID != musa.ID || out.Delivery.FeeKobo != musa.FeeKobo {
		t.Fatalf("offered %+v %v", out, err)
	}
	first := *out.Delivery
	if got := w.stages(first.ID); len(got) != 1 || got[0] != dispatch.StageOffer {
		t.Fatalf("stages %v", got)
	}
	// Offering again while it is out: the same offer.
	if again, _ := w.d.Offer(ctx, w.env.Tenant, ref, dispatch.OfferIn{Ask: true}); again.Delivery == nil || again.Delivery.ID != first.ID {
		t.Fatalf("again %+v", again)
	}
	// Musa can't: on to Tunde.
	w.must(first, dispatch.Cant)
	second := w.held(ref)
	if second.RiderID != tunde.ID || second.Status != "offered" {
		t.Fatalf("next %+v", second)
	}
	// Tunde does not answer: passed, and no one else covers Lekki.
	if _, err := w.env.DB.Admin.Exec(ctx, `UPDATE deliveries SET expires_at = now() - interval '1 second' WHERE id = $1`, second.ID); err != nil {
		t.Fatal(err)
	}
	if err := w.d.Sweep(ctx); err != nil {
		t.Fatal(err)
	}
	if w.alerts(dispatch.AlertNoRider) != 1 {
		t.Errorf("the owner was not told no rider took it")
	}
	if o := w.get(ref); o.Status != "accepted" {
		t.Errorf("order %s", o.Status)
	}
	// Two more unanswered offers today and Tunde is not answering.
	for range 2 {
		ref := w.order("Lekki", "none")
		out, err := w.d.Offer(ctx, w.env.Tenant, ref, dispatch.OfferIn{Rider: &tunde.ID, By: "user:ada"})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.env.DB.Admin.Exec(ctx, `UPDATE deliveries SET expires_at = now() - interval '1 second' WHERE id = $1`, out.Delivery.ID); err != nil {
			t.Fatal(err)
		}
		if err := w.d.Sweep(ctx); err != nil {
			t.Fatal(err)
		}
	}
	x, err := w.riders.Get(ctx, w.env.Tenant, tunde.ID)
	if err != nil || !x.NotAnswering {
		t.Fatalf("not answering: %+v %v", x, err)
	}
	// Named by hand anyway, he answers: back.
	ref = w.order("Lekki", "none")
	out, err = w.d.Offer(ctx, w.env.Tenant, ref, dispatch.OfferIn{Rider: &tunde.ID, By: "user:ada"})
	if err != nil {
		t.Fatal(err)
	}
	w.must(*out.Delivery, dispatch.Accept)
	if x, _ := w.riders.Get(ctx, w.env.Tenant, tunde.ID); x.NotAnswering {
		t.Error("still not answering after a tap")
	}
	// A rider who said off is offered nothing by rotation; a busy one is
	// offered after the free.
	if _, err := w.riders.Say(ctx, "+2348031110001", reach.WhatsApp, "+2348031110001", riders.Said{State: "off"}); err != nil {
		t.Fatal(err)
	}
	ref = w.order("Lekki", "none")
	if out, err := w.d.Offer(ctx, w.env.Tenant, ref, dispatch.OfferIn{}); err != nil || out.Status != "offered" || out.Delivery.RiderID != tunde.ID {
		t.Errorf("rotation with Musa off: %+v %v", out, err)
	}
}

// An order is held by exactly one rider: a second Accept changes nothing,
// offering it again answers held, and once released its rider's taps do
// nothing. An order not ready is refused.
func TestOneRiderHoldsAnOrder(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()
	musa := w.rider("Musa", "+2348031110001", "Lekki")
	tunde := w.rider("Tunde", "+2348031110002")
	// Paid after acceptance and not paid: not ready.
	ref := w.order("Lekki", "after_accept")
	if _, err := w.d.Offer(ctx, w.env.Tenant, ref, dispatch.OfferIn{Rider: &musa.ID}); !errors.Is(err, dispatch.ErrNotReady) {
		t.Fatalf("an unpaid order was offered: %v", err)
	}
	ref = w.order("Lekki", "none")
	out, err := w.d.Offer(ctx, w.env.Tenant, ref, dispatch.OfferIn{Rider: &musa.ID, By: "user:ada"})
	if err != nil {
		t.Fatal(err)
	}
	// The owner names Tunde instead: Musa's offer is withdrawn.
	out2, err := w.d.Offer(ctx, w.env.Tenant, ref, dispatch.OfferIn{Rider: &tunde.ID, By: "user:ada"})
	if err != nil || out2.Delivery.RiderID != tunde.ID {
		t.Fatalf("by hand: %+v %v", out2, err)
	}
	if _, err := w.press(*out.Delivery, dispatch.Accept); !errors.Is(err, dispatch.ErrMove) {
		t.Errorf("a withdrawn offer was accepted: %v", err)
	}
	tap := w.must(*out2.Delivery, dispatch.Accept)
	if !tap.Moved || tap.Delivery.Status != "accepted" {
		t.Fatalf("accept %+v", tap)
	}
	if again := w.must(*out2.Delivery, dispatch.Accept); again.Moved {
		t.Error("a second Accept moved it again")
	}
	if o := w.get(ref); o.Status != "dispatched" {
		t.Errorf("order %s", o.Status)
	}
	if held, _ := w.d.Offer(ctx, w.env.Tenant, ref, dispatch.OfferIn{Rider: &musa.ID}); held.Status != "held" {
		t.Errorf("offered while held: %+v", held)
	}
	// Tunde is busy now for any business he rides for.
	if x, _ := w.riders.Get(ctx, w.env.Tenant, tunde.ID); x.Presence.State != "busy" || x.Presence.Open != 1 {
		t.Errorf("presence %+v", x.Presence)
	}
	// Released: the order is ready again, Tunde's taps do nothing.
	if _, err := w.d.Release(ctx, w.env.Tenant, ref, "user:ada"); err != nil {
		t.Fatal(err)
	}
	if o := w.get(ref); o.Status != "accepted" {
		t.Errorf("released order %s", o.Status)
	}
	if _, err := w.press(*out2.Delivery, dispatch.PickedUp); !errors.Is(err, dispatch.ErrMove) {
		t.Errorf("a released rider picked up: %v", err)
	}
	if got := w.stages(out2.Delivery.ID); got[len(got)-1] != dispatch.StageReleased {
		t.Errorf("stages %v", got)
	}
}

// The rider's three taps run an order to completed, the customer told at
// each step; Delivered tells the order's run.
func TestDeliveryTaps(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()
	musa := w.rider("Musa Bello", "+2348031110001", "Lekki")
	ref := w.order("Lekki", "none")
	out, err := w.d.Offer(ctx, w.env.Tenant, ref, dispatch.OfferIn{})
	if err != nil {
		t.Fatal(err)
	}
	x := *out.Delivery
	if _, err := w.press(x, dispatch.PickedUp); !errors.Is(err, dispatch.ErrMove) {
		t.Errorf("picked up before accepting: %v", err)
	}
	w.must(x, dispatch.Accept)
	var v dispatch.View
	w.in(func(tx pgx.Tx) error {
		var err error
		v, err = w.d.ViewTx(ctx, tx, w.env.Tenant, x.ID)
		return err
	})
	if !v.Held() || v.Address != "12 Admiralty Way" || !strings.HasPrefix(v.CustomerPhone, "+23480399") || v.Customer != "Bola" || v.Pickup != "4 Akoka Road, Yaba" ||
		v.Landmark != "opposite the Shoprite gate" || v.Area != "Lekki" {
		t.Fatalf("view %+v", v)
	}
	sms := func() int {
		var n int
		if err := w.env.DB.Admin.QueryRow(ctx, `SELECT count(*) FROM platform_messages WHERE tenant_id = $1 AND dedup_key LIKE 'delivery:%'`, w.env.Tenant).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	w.must(x, dispatch.PickedUp)
	if o := w.get(ref); o.Status != "on_the_way" || sms() != 1 {
		t.Fatalf("picked up: %s, %d texts", o.Status, sms())
	}
	w.must(x, dispatch.Arrived)
	if o := w.get(ref); o.Status != "arrived" || sms() != 2 {
		t.Fatalf("arrived: %s, %d texts", o.Status, sms())
	}
	tap := w.must(x, dispatch.Delivered)
	if tap.Delivery.Status != "delivered" || w.get(ref).Status != "completed" || sms() != 3 || len(w.delivered) != 1 {
		t.Fatalf("delivered: %+v %s %d %v", tap.Delivery, w.get(ref).Status, sms(), w.delivered)
	}
	if again := w.must(x, dispatch.Delivered); again.Moved || len(w.delivered) != 1 {
		t.Error("a double Delivered did it twice")
	}
	if got := w.stages(x.ID); len(got) != 5 || got[1] != dispatch.StageHeld || got[2] != dispatch.StageOnWay || got[3] != dispatch.StageArrived || got[4] != dispatch.StageDone {
		t.Errorf("stages %v", got)
	}
	// The delivery is on the day's list, delivered, Musa's.
	w.in(func(tx pgx.Tx) error {
		list, err := w.d.ListTx(ctx, tx, w.env.Tenant, dispatch.ListFilter{Day: time.Now().In(time.FixedZone("WAT", 3600)).Format("2006-01-02")})
		if err != nil {
			return err
		}
		if len(list) != 1 || list[0].Rider != "Musa Bello" || list[0].RiderID != musa.ID || list[0].DeliveredAt == nil {
			t.Errorf("list %+v", list)
		}
		return nil
	})
}

// Paid on delivery: the account number is issued when the rider arrives,
// Delivered waits for the money, the payment tells the rider Paid; cash
// instead completes it and leaves what the rider owes, remitted oldest
// first.
func TestPayAtTheDoor(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()
	musa := w.rider("Musa", "+2348031110001")
	ref := w.order("Lekki", "on_delivery")
	o := w.get(ref)
	if o.Payment != "on_delivery" || dispatch.ReadyReason(o) != "" {
		t.Fatalf("order %+v: %q", o, dispatch.ReadyReason(o))
	}
	out, err := w.d.Offer(ctx, w.env.Tenant, ref, dispatch.OfferIn{})
	if err != nil {
		t.Fatal(err)
	}
	x := *out.Delivery
	w.must(x, dispatch.Accept)
	w.must(x, dispatch.PickedUp)
	w.must(x, dispatch.Arrived)
	if w.issued != 1 {
		t.Fatalf("issued %d", w.issued)
	}
	if got := w.stages(x.ID); got[len(got)-1] != dispatch.StageDoor {
		t.Fatalf("stages %v", got)
	}
	if _, err := w.press(x, dispatch.Delivered); !errors.Is(err, dispatch.ErrMove) {
		t.Fatalf("delivered unpaid: %v", err)
	}
	// The customer pays into it.
	var acct uuid.UUID
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT id FROM customer_invoice_accounts WHERE order_id = $1`, o.ID).Scan(&acct); err != nil {
		t.Fatal(err)
	}
	if err := w.orders.TransferReceived(ctx, w.env.Tenant, o.ID, "tot_door", "iswallet/taskiem", o.Payable(), acct); err != nil {
		t.Fatal(err)
	}
	if got := w.stages(x.ID); got[len(got)-1] != dispatch.StagePaid {
		t.Fatalf("after paying: %v", got)
	}
	tap := w.must(x, dispatch.Delivered)
	if tap.Delivery.PaidHow != "transfer" || w.get(ref).Status != "completed" {
		t.Fatalf("paid at the door: %+v %s", tap.Delivery, w.get(ref).Status)
	}

	// Cash: completed, paid in cash, owed by the rider.
	ref2 := w.order("Lekki", "on_delivery")
	out, err = w.d.Offer(ctx, w.env.Tenant, ref2, dispatch.OfferIn{Rider: &musa.ID})
	if err != nil {
		t.Fatal(err)
	}
	y := *out.Delivery
	w.must(y, dispatch.Accept)
	w.must(y, dispatch.PickedUp)
	w.must(y, dispatch.Arrived)
	tap = w.must(y, dispatch.Cash)
	o2 := w.get(ref2)
	if tap.Delivery.Status != "delivered" || tap.Delivery.PaidHow != "cash" || o2.Status != "completed" || o2.PaidAt == nil {
		t.Fatalf("cash: %+v %+v", tap.Delivery, o2)
	}
	var owed []dispatch.Owed
	w.in(func(tx pgx.Tx) error {
		var err error
		owed, err = w.d.OwedTx(ctx, tx, w.env.Tenant)
		return err
	})
	if len(owed) != 1 || owed[0].OwedKobo != o2.Payable() || owed[0].Rider != "Musa" {
		t.Fatalf("owed %+v", owed)
	}
	// A transfer naming the order pays it off; the rest by hand.
	half := o2.Payable() / 2
	if _, _, err := w.d.MoneyLog.RecordTx(ctx, mustTx(t, w), w.env.Tenant, moneylog.Entry{}); err == nil {
		t.Fatal("an empty entry was recorded")
	}
	w.in(func(tx pgx.Tx) error {
		_, _, err := w.d.MoneyLog.RecordTx(ctx, tx, w.env.Tenant, moneylog.Entry{Direction: "in", Kind: "statement", Status: "settled", Channel: "bank",
			Reference: "stmt-1", AmountKobo: &half, Source: "upload", Description: "TRF FROM MUSA " + ref2})
		return err
	})
	w.in(func(tx pgx.Tx) error {
		n, err := w.d.MatchRemittances(ctx, tx, w.env.Tenant)
		if n != 1 {
			t.Errorf("matched %d", n)
		}
		return err
	})
	w.in(func(tx pgx.Tx) error {
		left, err := w.d.RemitTx(ctx, tx, w.env.Tenant, musa.ID, o2.Payable(), "cash", nil, "user:ada")
		if !errors.Is(err, dispatch.ErrInvalid) {
			t.Errorf("more than owed: %d %v", left, err)
		}
		left, err = w.d.RemitTx(ctx, tx, w.env.Tenant, musa.ID, o2.Payable()-half, "cash", nil, "user:ada")
		if left != 0 {
			t.Errorf("left %d", left)
		}
		return err
	})
	w.in(func(tx pgx.Tx) error {
		var err error
		owed, err = w.d.OwedTx(ctx, tx, w.env.Tenant)
		if len(owed) != 0 {
			t.Errorf("still owed %+v", owed)
		}
		return err
	})
}

// mustTx is a transaction left open for a call expected to fail before it
// writes.
func mustTx(t *testing.T, w *world) pgx.Tx {
	t.Helper()
	tx, err := w.env.DB.App.Begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tx.Rollback(context.Background()) })
	return tx
}

// A rider holding a delivery for one business is busy for another, which
// sees no more than that.
func TestBusyAcrossBusinesses(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()
	musa := w.rider("Musa", "+2348031110001")
	other := w.env.AddTenant(t)
	name, phone := "Musa B", "+2348031110001"
	theirs, err := w.riders.Add(ctx, other, riders.In{Name: &name, Phone: &phone}, "user:bisi")
	if err != nil {
		t.Fatal(err)
	}
	ref := w.order("Lekki", "none")
	out, err := w.d.Offer(ctx, w.env.Tenant, ref, dispatch.OfferIn{Rider: &musa.ID})
	if err != nil {
		t.Fatal(err)
	}
	w.must(*out.Delivery, dispatch.Accept)
	x, err := w.riders.Get(ctx, other, theirs.ID)
	if err != nil || x.Presence.State != "busy" || x.Presence.Open != 1 {
		t.Fatalf("seen elsewhere: %+v %v", x.Presence, err)
	}
	if x.Presence.Line() != "busy, 1 delivery" {
		t.Errorf("line %q", x.Presence.Line())
	}
}
