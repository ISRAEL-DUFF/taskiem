package storefront_test

import (
	"bytes"
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	sfconn "github.com/israel-duff/taskiem/connectors/storefront"
	"github.com/israel-duff/taskiem/engine/connector"
	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/runtime"
	rt "github.com/israel-duff/taskiem/engine/runtime/runtimetest"
	"github.com/israel-duff/taskiem/engine/sender"
	"github.com/israel-duff/taskiem/engine/storefront"
	"github.com/israel-duff/taskiem/templates"
)

// fakePay is a payment provider that pays what the test says.
type fakePay struct {
	mu      sync.Mutex
	created map[string]storefront.PaymentRequest
	paid    map[string]bool
}

func (f *fakePay) Create(_ context.Context, _ uuid.UUID, _ string, r storefront.PaymentRequest) (string, string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.created[r.Reference] = r
	return "https://checkout.paystack.test/" + r.Reference, r.Reference, nil
}

func (f *fakePay) Verify(_ context.Context, _ uuid.UUID, _ string, ref string) (string, int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.paid[ref] {
		return "paid", f.created[ref].AmountKobo, nil
	}
	return "pending", 0, nil
}

func (f *fakePay) pay(ref string) { f.mu.Lock(); f.paid[ref] = true; f.mu.Unlock() }

type world struct {
	t      *testing.T
	env    *rt.Env
	orders *storefront.Orders
	pay    *fakePay
	sf     storefront.Storefront
	wfs    map[string]uuid.UUID
	key    []byte
}

func (w *world) in(fn func(pgx.Tx) error) {
	w.t.Helper()
	if err := db.InTenantTx(context.Background(), w.env.DB.App, []uuid.UUID{w.env.Tenant}, fn); err != nil {
		w.t.Fatal(err)
	}
}

func newWorld(t *testing.T) *world {
	env := rt.New(t)
	ctx := context.Background()
	w := &world{t: t, env: env, pay: &fakePay{created: map[string]storefront.PaymentRequest{}, paid: map[string]bool{}}, wfs: map[string]uuid.UUID{}}
	master := bytes.Repeat([]byte{4}, 32)
	w.key, _ = storefront.InboxKey(master)
	w.orders = &storefront.Orders{Pool: env.DB.App, Vault: env.Vault, InboxKey: w.key, Payments: w.pay, PreviewDomain: "shops.test",
		Messages: &sender.Queue{Vault: env.Vault, HashKey: bytes.Repeat([]byte{8}, 32)},
		Start:    env.Store.Start, Signal: env.Store.DeliverSignalOnce}
	if err := env.Registry.Register(sfconn.New(w.orders)); err != nil {
		t.Fatal(err)
	}
	lib := templates.Default()
	for _, kind := range []string{"order", "enquiry"} {
		tpl, ok := lib.Get("storefront-" + kind)
		if !ok {
			t.Fatalf("no storefront-%s template", kind)
		}
		def, err := tpl.Instantiate(map[string]any{}, "Storefront "+kind)
		if err != nil {
			t.Fatal(err)
		}
		wf := env.Publish(t, string(def))
		w.wfs[kind] = wf
		w.in(func(tx pgx.Tx) error {
			_, err := tx.Exec(ctx, `INSERT INTO triggers (id, tenant_id, workflow_id, version, environment, type, storefront_action) VALUES ($1, $2, $3, 1, 'prod', 'storefront', $4)`,
				uuid.New(), env.Tenant, wf, kind)
			return err
		})
	}
	w.in(func(tx pgx.Tx) error {
		if _, err := storefront.Save(ctx, tx, env.Tenant, "ada-kitchen", storefront.Card{Name: "Ada's Kitchen"}, nil, storefront.Columns{}, "user:ada"); err != nil {
			return err
		}
		if _, err := storefront.SaveAction(ctx, tx, env.Tenant, storefront.Action{Kind: "order", Enabled: true, Payment: "after_accept"}); err != nil {
			return err
		}
		if _, err := storefront.SaveAction(ctx, tx, env.Tenant, storefront.Action{Kind: "enquiry", Enabled: true}); err != nil {
			return err
		}
		var err error
		w.sf, err = storefront.Publish(ctx, tx, env.Tenant, "shops.test", "user:ada")
		return err
	})
	return w
}

// submit writes a submission to the inbox as the public host does.
func (w *world) submit(kind, payment string, total int64) string {
	w.t.Helper()
	ref := storefront.NewReference("ada-kitchen")
	key, hash := storefront.NewStatusKey()
	sub := storefront.Submission{Kind: kind, Customer: storefront.Customer{Name: "Bola", Phone: "+2348031234567", Email: "bola@example.com"},
		Basket: []storefront.Line{{ID: "1", Name: "Jollof rice", Quantity: 2, PriceKobo: total / 2, LineKobo: total}}, TotalKobo: total,
		Fulfilment: "pickup", Payment: payment, Channel: "direct", Submitted: time.Now(), StatusKey: key}
	ct, err := storefront.SealSubmission(w.key, w.sf.ID, ref, sub)
	if err != nil {
		w.t.Fatal(err)
	}
	idem := make([]byte, 32)
	copy(idem, ref)
	var got *string
	if err := w.env.DB.PublicPool(w.t, 1).QueryRow(context.Background(), `SELECT taskiem_public_submit($1, $2, $3, $4, $5, $6)`,
		w.sf.ID, kind, ref, idem, hash, ct).Scan(&got); err != nil || got == nil || *got != ref {
		w.t.Fatalf("submit: %v %v", got, err)
	}
	return ref
}

func (w *world) order(ref string) storefront.Order {
	w.t.Helper()
	var o storefront.Order
	w.in(func(tx pgx.Tx) error {
		var err error
		o, err = w.orders.GetOrder(context.Background(), tx, w.env.Tenant, ref)
		return err
	})
	return o
}

func (w *world) decide(ref, step, decision string) {
	w.t.Helper()
	o := w.order(ref)
	if o.RunID == nil {
		w.t.Fatalf("%s has no run", ref)
	}
	if err := w.env.Store.DecideApproval(context.Background(), runRef(w, *o.RunID), step, decision, "owner", "whatsapp"); err != nil {
		w.t.Fatalf("decide %s %s: %v", ref, step, err)
	}
	w.env.Drain(w.t)
}

func runRef(w *world, id uuid.UUID) runtime.RunRef { return runtime.RunRef{ID: id, TenantID: w.env.Tenant} }

func (w *world) messages(ref string) int {
	w.t.Helper()
	var n int
	if err := w.env.DB.Admin.QueryRow(context.Background(), `SELECT count(*) FROM platform_messages WHERE dedup_key LIKE '%:sms'`).Scan(&n); err != nil {
		w.t.Fatal(err)
	}
	return n
}

func TestOrderPaidAfterAcceptance(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()
	ref := w.submit("order", "after_accept", 300000)
	if err := w.orders.Drain(ctx); err != nil {
		t.Fatal(err)
	}
	o := w.order(ref)
	if o.Status != "received" || o.RunID == nil || o.Customer.Phone != "+2348031234567" || o.TotalKobo != 300000 {
		t.Fatalf("recorded: %+v", o)
	}
	// The customer's details are sealed at rest.
	var raw string
	_ = w.env.DB.Admin.QueryRow(ctx, `SELECT customer::text FROM storefront_orders WHERE reference = $1`, ref).Scan(&raw)
	if bytes.Contains([]byte(raw), []byte("8031234567")) || bytes.Contains([]byte(raw), []byte("Bola")) {
		t.Fatalf("customer in the clear: %s", raw)
	}
	var inbox int
	_ = w.env.DB.Admin.QueryRow(ctx, `SELECT count(*) FROM storefront_inbox`).Scan(&inbox)
	if inbox != 0 {
		t.Fatal("the inbox kept the submission")
	}
	w.env.Drain(t)

	w.decide(ref, "decide", "approved")
	o = w.order(ref)
	if o.Status != "awaiting_payment" || o.PaymentURL == "" || o.DecidedAt == nil {
		t.Fatalf("after accepting: %+v", o)
	}
	if n := w.messages(ref); n != 1 {
		t.Fatalf("the payment link was not texted: %d messages", n)
	}
	w.pay.pay(storefront.PaymentKey(o.ID))
	if err := w.orders.CheckPayments(ctx); err != nil {
		t.Fatal(err)
	}
	w.env.Drain(t)
	if o = w.order(ref); o.Status != "paid" || o.PaidAt == nil {
		t.Fatalf("after paying: %+v", o)
	}
	w.decide(ref, "done_paid", "approved")
	if o = w.order(ref); o.Status != "completed" {
		t.Fatalf("after delivery: %+v", o)
	}
	if n := w.messages(ref); n != 2 {
		t.Fatalf("messages: %d, want the link and the confirmation", n)
	}
}

func TestOrderDeclinedAndPaidAtOnce(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()

	// Declined: the customer is told.
	ref := w.submit("order", "after_accept", 150000)
	if err := w.orders.Drain(ctx); err != nil {
		t.Fatal(err)
	}
	w.env.Drain(t)
	w.decide(ref, "decide", "rejected")
	if o := w.order(ref); o.Status != "declined" {
		t.Fatalf("%+v", o)
	}

	// Paid at once: no run until paid; then accepted and confirmed.
	w.in(func(tx pgx.Tx) error {
		_, err := storefront.SaveAction(ctx, tx, w.env.Tenant, storefront.Action{Kind: "order", Enabled: true, Payment: "at_order"})
		return err
	})
	ref = w.submit("order", "at_order", 200000)
	if err := w.orders.Drain(ctx); err != nil {
		t.Fatal(err)
	}
	o := w.order(ref)
	if o.Status != "awaiting_payment" || o.RunID != nil || o.ExpiresAt == nil {
		t.Fatalf("before paying: %+v", o)
	}
	req := w.pay.created[storefront.PaymentKey(o.ID)]
	if req.AmountKobo != 200000 || req.Email != "bola@example.com" || req.CallbackURL == "" || !bytes.Contains([]byte(req.CallbackURL), []byte("https://ada-kitchen.shops.test/o/"+ref+"?k=")) {
		t.Fatalf("payment page: %+v", req)
	}
	if err := w.orders.CheckPayments(ctx); err != nil {
		t.Fatal(err)
	}
	if o = w.order(ref); o.Status != "awaiting_payment" {
		t.Fatal("an unpaid order moved on")
	}
	w.pay.pay(storefront.PaymentKey(o.ID))
	if err := w.orders.CheckPayments(ctx); err != nil {
		t.Fatal(err)
	}
	if o = w.order(ref); o.Status != "received" || o.RunID == nil || o.PaidAt == nil {
		t.Fatalf("after paying: %+v", o)
	}
	w.env.Drain(t)
	w.decide(ref, "decide", "approved")
	if o = w.order(ref); o.Status != "accepted" {
		t.Fatalf("accepted: %+v", o)
	}

	// Declining an order already paid leaves a refund due.
	ref = w.submit("order", "at_order", 100000)
	if err := w.orders.Drain(ctx); err != nil {
		t.Fatal(err)
	}
	w.pay.pay(storefront.PaymentKey(w.order(ref).ID))
	if err := w.orders.CheckPayments(ctx); err != nil {
		t.Fatal(err)
	}
	w.env.Drain(t)
	w.decide(ref, "decide", "rejected")
	if o = w.order(ref); o.Status != "refund_due" {
		t.Fatalf("declined after paying: %+v", o)
	}

	// Unpaid past its time: expired, and no run.
	ref = w.submit("order", "at_order", 100000)
	if err := w.orders.Drain(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := w.env.DB.Admin.Exec(ctx, `UPDATE storefront_orders SET expires_at = now() - interval '1 minute' WHERE reference = $1`, ref); err != nil {
		t.Fatal(err)
	}
	if err := w.orders.CheckPayments(ctx); err != nil {
		t.Fatal(err)
	}
	if o = w.order(ref); o.Status != "expired" || o.RunID != nil {
		t.Fatalf("expired: %+v", o)
	}
}

func TestEnquiryAndTheConnectorsBoundaries(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()
	ref := w.submit("enquiry", "none", 0)
	if err := w.orders.Drain(ctx); err != nil {
		t.Fatal(err)
	}
	w.env.Drain(t)
	if n := w.messages(ref); n != 1 {
		t.Fatalf("the enquiry was not acknowledged: %d", n)
	}
	w.decide(ref, "answered", "approved")
	if o := w.order(ref); o.Status != "completed" || o.Payment != "none" {
		t.Fatalf("%+v", o)
	}

	// The connector acts only inside a run, and only for the run's tenant.
	conn := sfconn.New(w.orders)
	in := map[string]any{"reference": ref, "decision": "cancel"}
	if _, err := conn.Actions["decide"].Execute(ctx, connector.Request{Input: in}); err == nil {
		t.Fatal("a decision outside a run")
	}
	other := w.env.AddTenant(t)
	octx := connector.WithCaller(ctx, connector.Caller{Tenant: other, Run: uuid.New(), Step: "x", Environment: "prod"})
	if _, err := conn.Actions["decide"].Execute(octx, connector.Request{Input: in}); !errors.Is(err, storefront.ErrNoOrder) {
		t.Fatalf("another tenant reached the order: %v", err)
	}
	if _, err := conn.Actions["notify_customer"].Execute(octx, connector.Request{Input: map[string]any{"reference": ref, "text": "hi"}}); !errors.Is(err, storefront.ErrNoOrder) {
		t.Fatalf("another tenant messaged the customer: %v", err)
	}

	// A disabled form takes nothing.
	w.in(func(tx pgx.Tx) error {
		_, err := storefront.SaveAction(ctx, tx, w.env.Tenant, storefront.Action{Kind: "enquiry", Enabled: false})
		return err
	})
	var got *string
	key, hash := storefront.NewStatusKey()
	_ = key
	ct, _ := storefront.SealSubmission(w.key, w.sf.ID, "AD-XXXXXX", storefront.Submission{Kind: "enquiry"})
	if err := w.env.DB.PublicPool(t, 1).QueryRow(ctx, `SELECT taskiem_public_submit($1, 'enquiry', 'AD-XXXXXX', $2, $3, $4)`,
		w.sf.ID, bytes.Repeat([]byte{1}, 32), hash, ct).Scan(&got); err != nil || got != nil {
		t.Fatalf("a disabled form took a submission: %v %v", got, err)
	}
}
