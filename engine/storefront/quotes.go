package storefront

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/db"
)

// Quotes (docs/spec/fronts.md §6, F7): a request for a price. The owner
// prices it (Quote); the order then waits for the customer as a changed
// price does (price_changed, with the quote as its basket and total), and
// the status page asks them to accept or decline it. Accepting takes the
// deposit at once (the order is paid at order from then on); declining
// cancels it; a quote not answered in time expires. The run waiting on
// the quote is told by the QuoteSignal.

// QuoteSignal tells a quote's run its price is set.
const QuoteSignal = "storefront.quote"

// Quote prices a quote request: priceKobo for the work, the action's
// deposit share paid on acceptance, standing for its quote_days.
func (o *Orders) Quote(ctx context.Context, tenant uuid.UUID, reference string, priceKobo int64, actor string) (Order, error) {
	if priceKobo < 100 || priceKobo > 100_000_000_00 {
		return Order{}, fmt.Errorf("%w: a quote is ₦1 to ₦100,000,000", ErrInvalid)
	}
	var ord Order
	var until time.Time
	err := db.InTenantTx(ctx, o.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		var err error
		if ord, err = scanOrder(tx.QueryRow(ctx, `SELECT `+orderCols+` FROM storefront_orders WHERE tenant_id = $1 AND reference = $2 FOR UPDATE`, tenant, reference)); err != nil {
			return err
		}
		if ord.Kind != "quote" {
			return fmt.Errorf("%w: %s is not a quote request", ErrDecision, reference)
		}
		if ord.Status != "received" {
			return fmt.Errorf("%w: %s is %s; a quote is sent once, to a request waiting for one", ErrDecision, reference, ord.Status)
		}
		var raw []byte
		if err := tx.QueryRow(ctx, `SELECT options FROM storefront_actions WHERE id = $1`, ord.action).Scan(&raw); err != nil {
			return err
		}
		var opts ActionOptions
		_ = json.Unmarshal(raw, &opts)
		days := opts.QuoteDays
		if days <= 0 {
			days = 7
		}
		until = o.now().Add(time.Duration(days) * 24 * time.Hour)
		pc := PriceChange{Basket: []Line{{ID: "quote", Name: "Quote " + reference, Quantity: 1, PriceKobo: priceKobo, LineKobo: priceKobo}}, TotalKobo: priceKobo}
		if d := opts.Deposit(priceKobo); d > 0 && d < priceKobo {
			pc.DepositKobo = d
		}
		change, _ := json.Marshal(pc)
		if _, err := tx.Exec(ctx, `UPDATE storefront_orders SET status = 'price_changed', price_change = $3, payment = 'at_order', expires_at = $4, decided_by = $5,
			decided_at = now(), updated_at = now() WHERE tenant_id = $1 AND id = $2`, tenant, ord.ID, change, until, actor); err != nil {
			return err
		}
		if err := audit(ctx, tx, tenant, "storefront.quote_sent", reference, map[string]any{"by": actor, "total_kobo": priceKobo, "deposit_kobo": pc.DepositKobo}); err != nil {
			return err
		}
		ord, err = o.GetOrder(ctx, tx, tenant, reference)
		return err
	})
	if err != nil {
		return ord, err
	}
	pay := ord.PriceChange.TotalKobo
	if ord.PriceChange.DepositKobo > 0 {
		pay = ord.PriceChange.DepositKobo
	}
	if o.Signal != nil {
		if _, _, err := o.Signal(ctx, tenant, "prod", QuoteSignal, reference, "quote:"+reference,
			map[string]any{"reference": reference, "status": "quoted", "total_kobo": ord.PriceChange.TotalKobo, "total": FormatNaira(ord.PriceChange.TotalKobo),
				"pay_now_kobo": pay, "pay_now": FormatNaira(pay), "until": Day(until)}); err != nil {
			return ord, err
		}
	}
	return ord, nil
}

// QuoteRequests are the quotes waiting for a price, oldest first.
func (o *Orders) QuoteRequests(ctx context.Context, tx pgx.Tx, tenant uuid.UUID) ([]Order, error) {
	rows, err := tx.Query(ctx, `SELECT `+orderCols+` FROM storefront_orders WHERE tenant_id = $1 AND kind = 'quote' AND status = 'received'
		ORDER BY created_at LIMIT 20`, tenant)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (Order, error) { return scanOrder(r) })
}

// QuoteDetails is what a quote request asks, from its answers.
func (o *Orders) QuoteDetails(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, ord Order) (string, string) {
	rec, err := o.recordOf(ctx, tx, tenant, ord)
	if err != nil {
		return "", ""
	}
	details, _ := rec.answers["details"].(string)
	by, _ := rec.answers["needed_by"].(string)
	return details, by
}

// Custom fronts (fronts §6, F7): any front can take a form beyond its
// kind's: a question, a call-back, a quote request, or a blank form whose
// questions the owner writes. They are plain-question forms, the ones that
// need no list.

// AddableKinds are the forms AddFrontAction makes; "form" is a blank one
// (a question form with no questions of its own yet).
var AddableKinds = []string{"form", "enquiry", "callback", "quote"}

// AddFrontAction adds a form to a front, off.
func AddFrontAction(ctx context.Context, tx pgx.Tx, tenant, front uuid.UUID, kind, label string) (Action, error) {
	if !contains(AddableKinds, kind) {
		return Action{}, fmt.Errorf("%w: a front can take a blank form, a question, a call-back or a quote request", ErrInvalid)
	}
	var sf uuid.UUID
	if err := tx.QueryRow(ctx, `SELECT storefront_id FROM storefront_fronts WHERE id = $1`, front).Scan(&sf); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Action{}, ErrNotFound
		}
		return Action{}, err
	}
	form := []byte(nil)
	slugBase := kind
	if kind == "form" {
		kind, form = "enquiry", []byte(`{"type":"object","properties":{}}`)
	}
	if label == "" {
		label = defaultLabels[kind]
		if slugBase == "form" {
			label = "Send"
		}
	}
	if r := []rune(label); len(r) > 40 || hasLink(label) {
		return Action{}, fmt.Errorf("%w: a form's label is up to 40 characters, with no links", ErrInvalid)
	}
	payment := "none"
	opts := ActionOptions{}
	if kind == "quote" {
		payment, opts = "after_accept", ActionOptions{DepositPercent: 50, QuoteDays: 7}
	}
	raw, _ := json.Marshal(opts)
	var taken []string
	rows, err := tx.Query(ctx, `SELECT slug FROM storefront_actions WHERE front_id = $1`, front)
	if err != nil {
		return Action{}, err
	}
	if taken, err = pgx.CollectRows(rows, pgx.RowTo[string]); err != nil {
		return Action{}, err
	}
	slug := slugBase
	for n := 2; contains(taken, slug); n++ {
		slug = fmt.Sprintf("%s-%d", slugBase, n)
	}
	id := uuid.Must(uuid.NewV7())
	if _, err := tx.Exec(ctx, `INSERT INTO storefront_actions (id, storefront_id, front_id, tenant_id, kind, slug, builtin, enabled, label, payment, options, form, position)
		SELECT $1, $2, $3, $4, $5, $6, false, false, $7, $8, $9, $10, COALESCE(max(position), 0) + 1 FROM storefront_actions WHERE front_id = $3`,
		id, sf, front, tenant, kind, slug, label, payment, raw, form); err != nil {
		return Action{}, err
	}
	return Action{ID: &id, Slug: slug, FrontID: &front, Kind: kind, Label: label, Payment: payment, Options: opts}, nil
}

// SetActionForm sets a plain-question form's own questions (a JSON Schema
// object, as forms are written); nil puts back its kind's. A published
// page is checked when it is published again, so a question a live
// workflow reads cannot be dropped (§5.3).
func SetActionForm(ctx context.Context, tx pgx.Tx, id uuid.UUID, form []byte) error {
	var kind string
	if err := tx.QueryRow(ctx, `SELECT kind FROM storefront_actions WHERE id = $1`, id).Scan(&kind); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		return err
	}
	if kind != "enquiry" && kind != "callback" && kind != "quote" {
		return fmt.Errorf("%w: only a question, call-back or quote form takes questions of its own", ErrInvalid)
	}
	if len(form) > 64<<10 {
		return fmt.Errorf("%w: a form is up to 64 KB", ErrInvalid)
	}
	if err := CheckForm(form); err != nil {
		return err
	}
	for _, w := range Widgets {
		if HasWidget(form, w) {
			return fmt.Errorf("%w: a %s field needs a list; this form takes plain questions", ErrInvalid, w)
		}
	}
	var arg any
	if len(form) > 0 {
		arg = form
	}
	_, err := tx.Exec(ctx, `UPDATE storefront_actions SET form = $2 WHERE id = $1`, id, arg)
	return err
}
