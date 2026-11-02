package storefront_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/connectors/notify"
	"github.com/israel-duff/taskiem/engine/storefront"
	"github.com/israel-duff/taskiem/templates"
)

// Quotes (docs/spec/fronts.md §6, F7): a request with nothing in the
// basket starts its workflow; the owner prices it once, which puts it
// before the customer as a changed price with the deposit to pay;
// accepting it on the status page takes the deposit, and the balance is
// left for later.
func TestQuoteRequestedPricedAcceptedPaid(t *testing.T) {
	ctx := context.Background()
	w := newMatchWorld(t).world
	var action uuid.UUID
	w.in(func(tx pgx.Tx) error {
		f, err := storefront.CreateFront(ctx, tx, w.env.Tenant, storefront.NewFront{Name: "Tailoring", Kind: "quotes"})
		if err != nil {
			return err
		}
		if err := tx.QueryRow(ctx, `UPDATE storefront_fronts SET enabled = true WHERE id = $1 RETURNING (SELECT id FROM storefront_actions WHERE front_id = $1 AND kind = 'quote')`,
			f.ID).Scan(&action); err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `UPDATE storefront_actions SET enabled = true WHERE id = $1`, action)
		return err
	})
	told := &toldOwner{}
	if err := w.env.Registry.Register(notify.New(told)); err != nil {
		t.Fatal(err)
	}
	tpl, _ := templates.Default().Get("storefront-quote")
	def, err := tpl.Instantiate(map[string]any{}, "Quotes")
	if err != nil {
		t.Fatal(err)
	}
	wf := w.env.Publish(t, string(def))
	if _, err := w.env.DB.Admin.Exec(ctx, `INSERT INTO triggers (id, tenant_id, workflow_id, version, environment, type, storefront_action, storefront_action_id)
		VALUES ($1, $2, $3, 1, 'prod', 'storefront', 'quote', $4)`, uuid.New(), w.env.Tenant, wf, action); err != nil {
		t.Fatal(err)
	}

	// The request: plain answers, no basket, nothing to pay yet.
	ref := storefront.NewReference("ada-kitchen")
	key, hash := storefront.NewStatusKey()
	w.keys[ref] = key
	sub := storefront.Submission{Kind: "quote", Customer: storefront.Customer{Name: "Bola", Phone: "+2348031234567"},
		Answers: map[string]any{"details": "Aso ebi for 6, green lace", "needed_by": "2026-12-20"}, Channel: "direct", Submitted: time.Now(), StatusKey: key}
	ct, err := storefront.SealSubmission(w.key, w.sf.ID, ref, sub)
	if err != nil {
		t.Fatal(err)
	}
	idem := make([]byte, 32)
	copy(idem, ref)
	if _, err := w.env.DB.PublicPool(t, 1).Exec(ctx, `SELECT taskiem_public_submit_action($1, $2, $3, $4, $5)`, action, ref, idem, hash, ct); err != nil {
		t.Fatal(err)
	}
	w.drain()
	if len(told.msgs) != 1 || !strings.Contains(told.msgs[0].Text, "Aso ebi for 6, green lace (needed by 2026-12-20). Reply: quote "+ref) {
		t.Fatalf("the owner told: %+v", told.msgs)
	}
	o := w.order(ref)
	if o.Kind != "quote" || o.Status != "received" || o.Payment != "none" || o.RunID == nil {
		t.Fatalf("requested: %+v", o)
	}
	var details, by string
	w.in(func(tx pgx.Tx) error {
		list, err := w.orders.QuoteRequests(ctx, tx, w.env.Tenant)
		if err != nil || len(list) != 1 {
			t.Fatalf("waiting for a price: %v %v", list, err)
		}
		details, by = w.orders.QuoteDetails(ctx, tx, w.env.Tenant, list[0])
		return nil
	})
	if details != "Aso ebi for 6, green lace" || by != "2026-12-20" {
		t.Fatalf("what it asks: %q %q", details, by)
	}

	// Priced once: ₦45,000, half to accept, for the action's seven days.
	if _, err := w.orders.Quote(ctx, w.env.Tenant, ref, 50, "user:ada"); !errors.Is(err, storefront.ErrInvalid) {
		t.Fatalf("below ₦1: %v", err)
	}
	o, err = w.orders.Quote(ctx, w.env.Tenant, ref, 4500000, "user:ada")
	if err != nil || o.Status != "price_changed" || o.PriceChange == nil || o.PriceChange.TotalKobo != 4500000 || o.PriceChange.DepositKobo != 2250000 ||
		o.ExpiresAt == nil || o.ExpiresAt.Before(time.Now().Add(6*24*time.Hour)) {
		t.Fatalf("quoted: %+v %v", o, err)
	}
	if _, err := w.orders.Quote(ctx, w.env.Tenant, ref, 5000000, "user:ada"); !errors.Is(err, storefront.ErrDecision) {
		t.Fatalf("quoted twice: %v", err)
	}
	if _, err := w.orders.Quote(ctx, w.env.Tenant, w.submit("order", "after_accept", 1000), 5000000, "user:ada"); err == nil {
		t.Fatal("an order was quoted")
	}
	w.env.Drain(t)
	var signals int
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT count(*) FROM run_events WHERE run_id = $1 AND type = 'SignalReceived'`, *o.RunID).Scan(&signals); err != nil || signals != 1 {
		var why string
		_ = w.env.DB.Admin.QueryRow(ctx, `SELECT string_agg(type || ' ' || COALESCE(step_id, '') || ' ' || left(COALESCE(payload::text, ''), 300), E'\n' ORDER BY seq) FROM run_events WHERE run_id = $1`, *o.RunID).Scan(&why)
		t.Fatalf("the run told of the price: %d %v\n%s", signals, err, why)
	}

	// Accepted on the status page: the deposit is asked at once.
	if got := w.change(ref, key, "price_accept", 0, storefront.Item{ID: "x", Name: "x", DurationMinutes: 30}, time.Time{}); got != "ok" {
		t.Fatalf("accept: %s", got)
	}
	w.changes()
	o = w.order(ref)
	if o.Status != "awaiting_payment" || o.TotalKobo != 4500000 || o.DepositKobo == nil || *o.DepositKobo != 2250000 || o.PaymentURL == "" ||
		len(o.Basket) != 1 || !strings.HasPrefix(o.Basket[0].Name, "Quote ") {
		t.Fatalf("accepted: %+v", o)
	}
	w.pay.pay(storefront.PaymentKey(o.ID))
	if err := w.orders.CheckPayments(ctx); err != nil {
		t.Fatal(err)
	}
	if o = w.order(ref); o.PaidKobo != 2250000 || o.BalanceKobo() != 2250000 {
		t.Fatalf("deposit paid: paid %d balance %d due %d deposit %v", o.PaidKobo, o.BalanceKobo(), o.DueKobo(), *o.DepositKobo)
	}
	var status string
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT status FROM runs WHERE id = $1`, *o.RunID).Scan(&status); err != nil || (status != "running" && status != "waiting") {
		var why string
		_ = w.env.DB.Admin.QueryRow(ctx, `SELECT string_agg(type || ' ' || COALESCE(step_id, '') || ' ' || left(COALESCE(payload::text, ''), 300), E'\n' ORDER BY seq) FROM run_events WHERE run_id = $1`, *o.RunID).Scan(&why)
		t.Fatalf("the run: %s %v\n%s", status, err, why)
	}
}

// Custom fronts (§6, F7): a front takes a blank form, whose questions the
// owner writes, and a question, call-back or quote form; list fields,
// files and Taskiem's own names are refused.
func TestBlankFormsOnAFront(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t)
	w.in(func(tx pgx.Tx) error {
		f, err := storefront.CreateFront(ctx, tx, w.env.Tenant, storefront.NewFront{Name: "Repairs", Kind: "custom"})
		if err != nil {
			return err
		}
		blank, err := storefront.AddFrontAction(ctx, tx, w.env.Tenant, f.ID, "form", "")
		if err != nil || blank.Kind != "enquiry" || blank.Slug != "form" || blank.Label != "Send" {
			t.Fatalf("blank: %+v %v", blank, err)
		}
		again, err := storefront.AddFrontAction(ctx, tx, w.env.Tenant, f.ID, "form", "Book a repair")
		if err != nil || again.Slug != "form-2" {
			t.Fatalf("a second: %+v %v", again, err)
		}
		q, err := storefront.AddFrontAction(ctx, tx, w.env.Tenant, f.ID, "quote", "")
		if err != nil || q.Payment != "after_accept" || q.Options.QuoteDays != 7 {
			t.Fatalf("quote: %+v %v", q, err)
		}
		if _, err := storefront.AddFrontAction(ctx, tx, w.env.Tenant, f.ID, "order", ""); !errors.Is(err, storefront.ErrInvalid) {
			t.Fatalf("an order form needs a list: %v", err)
		}
		if _, err := storefront.AddFrontAction(ctx, tx, w.env.Tenant, uuid.New(), "form", ""); !errors.Is(err, storefront.ErrNotFound) {
			t.Fatalf("no such front: %v", err)
		}
		good := []byte(`{"type":"object","required":["device"],"properties":{"device":{"type":"string","title":"What needs fixing?"},"urgent":{"type":"boolean","title":"Urgent"}}}`)
		if err := storefront.SetActionForm(ctx, tx, *blank.ID, good); err != nil {
			t.Fatal(err)
		}
		asked, err := storefront.AnswersAsked(ctx, tx, blank.ID)
		if err != nil || strings.Join(asked, ",") != "device,urgent" {
			t.Fatalf("asked: %v %v", asked, err)
		}
		for name, bad := range map[string]string{
			"a reserved name": `{"type":"object","properties":{"total":{"type":"string"}}}`,
			"a file":          `{"type":"object","properties":{"photo":{"type":"string","format":"file"}}}`,
			"a list field":    `{"type":"object","properties":{"basket":{"type":"array","x-form":{"widget":"basket","list":"catalogue"}}}}`,
			"not a form":      `[1, 2]`,
		} {
			if err := storefront.SetActionForm(ctx, tx, *blank.ID, []byte(bad)); !errors.Is(err, storefront.ErrInvalid) {
				t.Errorf("%s: %v", name, err)
			}
		}
		// Put back its kind's.
		if err := storefront.SetActionForm(ctx, tx, *blank.ID, nil); err != nil {
			t.Fatal(err)
		}
		var order uuid.UUID
		if err := tx.QueryRow(ctx, `SELECT id FROM storefront_actions WHERE kind = 'order' AND builtin`).Scan(&order); err != nil {
			return err
		}
		if err := storefront.SetActionForm(ctx, tx, order, good); !errors.Is(err, storefront.ErrInvalid) {
			t.Fatalf("an order form's questions: %v", err)
		}
		return nil
	})
}

// toldOwner records what a workflow tells the business.
type toldOwner struct {
	mu   sync.Mutex
	msgs []notify.Message
}

func (r *toldOwner) Tell(_ context.Context, m notify.Message) (notify.Result, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.msgs = append(r.msgs, m)
	return notify.Result{People: 1, Chat: 1}, nil
}
func (r *toldOwner) Text(context.Context, notify.Text) (bool, error) { return false, nil }
func (r *toldOwner) Message(context.Context, notify.Outside) (notify.Delivery, error) {
	return notify.Delivery{}, nil
}
