package dispatch

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/links"
	"github.com/israel-duff/taskiem/engine/riders"
	"github.com/israel-duff/taskiem/engine/storefront"
)

// A rider that is a business on Taskiem (docs/spec/chat-riders.md, decision
// 0065, phase 1): the business's delivery stays its record, and the rider
// receives it as an order in their own business too, through the link.
// Each step on the delivery, whoever took it and wherever (the rider's
// card or Orders tool, their WhatsApp buttons, the owner's hand), is
// mirrored to the rider's order so both sides see one card at one step.
// A rider with no business on Taskiem is reached as before.

// linkActor is who the door names for dispatch's crossings.
var linkActor = links.Actor{Type: "system", ID: "dispatch"}

// placeLinked offers a delivery just recorded to a linked rider as an
// order in their business, beside their chat app's message: the
// business's card first, in its own scope, then the rider's order and
// card through the door, the scope back on the business afterwards. A
// rider whose business has its links turned off, or whose link has ended,
// is offered by chat alone.
func (d *Dispatch) placeLinked(ctx context.Context, tx pgx.Tx, tenant, delivery uuid.UUID, r riders.Rider, ord storefront.Order, fee int64, expires time.Time, s Settings) ([]func() error, error) {
	if r.LinkID == nil || d.Vault == nil {
		return nil, nil
	}
	order := uuid.Must(uuid.NewV7())
	pair := uuid.Must(uuid.NewV7())
	if _, err := tx.Exec(ctx, `UPDATE deliveries SET link_order_id = $2 WHERE id = $1`, delivery, order); err != nil {
		return nil, err
	}
	var business string
	if err := tx.QueryRow(ctx, `SELECT name FROM tenants WHERE id = $1`, tenant).Scan(&business); err != nil {
		return nil, err
	}
	var pickup, pickupLink string
	if sf, err := storefront.Get(ctx, tx, tenant); err == nil {
		pickup, pickupLink = strings.TrimSpace(sf.Draft.Address), sf.Draft.MapLink()
	}
	// The rider's order and card first, through the door; the business's
	// card only once they have it, so a fallback leaves no card alone.
	_, err := links.PlaceDeliveryTx(ctx, tx, d.Vault, links.DeliveryIn{Link: *r.LinkID, DeliveryID: delivery, OrderID: order, Pair: pair, BusinessID: tenant,
		Business: business, Reference: ord.Reference, Pickup: pickup, PickupLink: pickupLink, Area: ord.Zone, FeeKobo: fee, Customer: ord.Customer,
		ProofRequired: s.Proof, DoorPayment: ord.Payment == "on_delivery", ExpiresAt: expires}, linkActor)
	if back := links.ReturnTx(ctx, tx, tenant); back != nil {
		return nil, back
	}
	var off *links.OtherOffError
	if errors.As(err, &off) || errors.Is(err, links.ErrNotFound) {
		// The order cannot reach them: the chat app's offer stands alone.
		d.log().Warn("dispatch: a linked rider's order could not be placed", "tenant", tenant, "rider", r.ID, "err", err)
		_, err = tx.Exec(ctx, `UPDATE deliveries SET link_order_id = NULL WHERE id = $1`, delivery)
		return nil, err
	}
	if err != nil {
		return nil, err
	}
	if err := links.CardTx(ctx, tx, tenant, *r.LinkID, pair, true, links.DocCard{Doc: links.DocDispatch, Event: StageOffer + "ed", Reference: ord.Reference,
		ID: delivery.String(), AmountKobo: &fee, Note: r.Name}); err != nil {
		return nil, err
	}
	// A dispatch company (phase 5): the order is offered on inside the
	// company to its own riders, by its rotation, in the company's scope.
	return d.companyOfferTx(ctx, tx, tenant, *r.LinkID, order)
}

// companyOfferTx offers a delivery order just placed in a rider's business
// to that business's own riders by rotation (a dispatch company, phase 5),
// when it has any. Through the door, in the company's scope, under a
// savepoint: what goes wrong inside the company is logged, never the
// business's problem.
func (d *Dispatch) companyOfferTx(ctx context.Context, tx pgx.Tx, tenant, link, order uuid.UUID) ([]func() error, error) {
	l, err := links.GetTx(ctx, tx, link)
	if errors.Is(err, links.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	company := l.Business
	if _, err := links.EnterTx(ctx, tx, link, "link.delivery.offered", nil, linkActor); err != nil {
		if back := links.ReturnTx(ctx, tx, tenant); back != nil {
			return nil, back
		}
		return nil, nil // their links are off, or the link ended: the business's offer stands alone
	}
	var after []func() error
	err = func() error {
		var has bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM riders WHERE tenant_id = $1 AND retired_at IS NULL AND active)`, company).Scan(&has); err != nil || !has {
			return err
		}
		var ref string
		if err := tx.QueryRow(ctx, `SELECT reference FROM storefront_orders WHERE id = $1`, order).Scan(&ref); err != nil {
			return err
		}
		sp, err := tx.Begin(ctx)
		if err != nil {
			return err
		}
		_, more, err := d.offerTx(ctx, sp, company, ref, OfferIn{By: "rotation"}, nil)
		if err != nil {
			d.log().Warn("dispatch: a company's own offer", "company", company, "order", order, "err", err)
			return sp.Rollback(ctx)
		}
		after = more
		return sp.Commit(ctx)
	}()
	if back := links.ReturnTx(ctx, tx, tenant); back != nil {
		return nil, back
	}
	return after, err
}

// innerOfTx is a company's own delivery for a delivery order of its own
// (phase 5), live (offered or held), in the company's scope; ok false
// when there is none.
func (d *Dispatch) innerOfTx(ctx context.Context, tx pgx.Tx, company, order uuid.UUID) (Delivery, bool, error) {
	x, err := d.HeldTx(ctx, tx, company, order)
	if errors.Is(err, ErrNoDelivery) {
		return x, false, nil
	}
	if err != nil {
		return x, false, err
	}
	return x, x.Status != "delivered", nil
}

// withdrawInnerTx takes a company's own delivery back when the business's
// delivery behind it ended (withdrawn, passed, released, or the company
// said Can't): the rider offered or holding it is told, and the job is no
// longer theirs. In the company's scope.
func (d *Dispatch) withdrawInnerTx(ctx context.Context, tx pgx.Tx, company, order uuid.UUID, why string) error {
	x, ok, err := d.innerOfTx(ctx, tx, company, order)
	if err != nil || !ok {
		return err
	}
	stage, status, col := StageReleased, "released", "released_at"
	if x.Status == "offered" {
		stage, status, col = StageWithdrawn, "withdrawn", "answered_at"
	}
	if _, err := tx.Exec(ctx, `UPDATE deliveries SET status = $2, `+col+` = now() WHERE id = $1`, x.ID, status); err != nil {
		return err
	}
	if err := d.queue(ctx, tx, company, x.ID, x.RiderID, stage, ""); err != nil {
		return err
	}
	return audit(ctx, tx, company, "dispatch."+status, x.Reference, map[string]any{"rider": x.RiderID, "by": "system:link", "why": why})
}

// followInnerTx moves a company's own delivery along with a step the
// company took on the business's delivery by its own hand (its card or
// Orders tool, phase 5): the rider holding it is told as if they had
// tapped; a step that does not fit the inner delivery (not held yet) is
// left alone. In the company's scope.
func (d *Dispatch) followInnerTx(ctx context.Context, tx pgx.Tx, company, order uuid.UUID, action string) ([]func() error, error) {
	x, ok, err := d.innerOfTx(ctx, tx, company, order)
	if err != nil || !ok {
		return nil, err
	}
	if action == Accept || action == Cant {
		// The company's word on the business's offer: its own rider still
		// answers their own offer (Accept), or loses it (Cant).
		if action == Cant {
			return nil, d.withdrawInnerTx(ctx, tx, company, order, "cant")
		}
		return nil, nil
	}
	ord, err := d.Orders.GetOrder(ctx, tx, company, x.Reference)
	if err != nil {
		return nil, err
	}
	_, after, err := d.press(ctx, tx, company, x, ord, action, &pressOpt{pair: uuid.Must(uuid.NewV7())})
	if errors.Is(err, ErrMove) {
		return nil, nil
	}
	return after, err
}

// mirrorStep records a delivery's step on the linked rider's order, when
// there is one: the business's card in its own scope, then the rider's
// status and card through the door, and the scope back. ours says the
// business took the step (an offer withdrawn, a release) rather than the
// rider. A delivery with no order of the rider's is left alone.
func (d *Dispatch) mirrorStep(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, x Delivery, stage string, ours bool) error {
	if x.LinkOrderID == nil || d.Vault == nil {
		return nil
	}
	r, err := d.Riders.GetTx(ctx, tx, tenant, x.RiderID)
	if err != nil || r.LinkID == nil {
		return err
	}
	pair := uuid.Must(uuid.NewV7())
	if err := d.ourCard(ctx, tx, tenant, *r.LinkID, pair, x, stage, ours); err != nil {
		return err
	}
	// Our own name, read on our side: the other side cannot see our row.
	var business string
	if stage == "paid" {
		if err := tx.QueryRow(ctx, `SELECT name FROM tenants WHERE id = $1`, tenant).Scan(&business); err != nil {
			return err
		}
	}
	if _, err := links.EnterTx(ctx, tx, *r.LinkID, "link.delivery."+stage, nil, linkActor); err != nil {
		if back := links.ReturnTx(ctx, tx, tenant); back != nil {
			return back
		}
		var off *links.OtherOffError
		if errors.As(err, &off) || errors.Is(err, links.ErrNotFound) {
			return nil // the link ended, or their links are off: the delivery goes on without the mirror
		}
		return err
	}
	rider, err := scopeTx(ctx, tx, tenant)
	if err == nil {
		err = links.DeliveryStepTx(ctx, tx, d.Vault, rider, *x.LinkOrderID, stage, pair, !ours)
	}
	if err == nil && (stage == StageWithdrawn || stage == "passed" || stage == "released") {
		// A dispatch company's own delivery for it goes too (phase 5).
		err = d.withdrawInnerTx(ctx, tx, rider, *x.LinkOrderID, stage)
	}
	if err == nil && stage == "paid" {
		// The fee is the rider's income (phase 4), in their own books.
		err = d.riderIncomeTx(ctx, tx, rider, x, business, d.now())
	}
	if back := links.ReturnTx(ctx, tx, tenant); back != nil {
		return back
	}
	return err
}

// ourCard is the business's card for a delivery's step, in its own
// conversation with the rider.
func (d *Dispatch) ourCard(ctx context.Context, tx pgx.Tx, tenant, link, pair uuid.UUID, x Delivery, stage string, ours bool) error {
	fee := x.FeeKobo
	note := x.Rider
	if stage == "paid" && x.paidHow != "" {
		note = x.paidHow // how the fee was paid: bank or cash
	}
	return links.CardTx(ctx, tx, tenant, link, pair, ours, links.DocCard{Doc: links.DocDispatch, Event: stage, Reference: x.Reference, ID: x.ID.String(), AmountKobo: &fee, Note: note})
}

// scopeTx is the business the transaction is scoped to after the door
// opened: the other side, never the caller's own.
func scopeTx(ctx context.Context, tx pgx.Tx, mine uuid.UUID) (uuid.UUID, error) {
	var other uuid.UUID
	if err := tx.QueryRow(ctx, `SELECT (current_setting('app.tenant_scope', true)::uuid[])[1]`).Scan(&other); err != nil {
		return uuid.Nil, err
	}
	if other == mine || other == uuid.Nil {
		return uuid.Nil, fmt.Errorf("%w: the door did not open", links.ErrState)
	}
	return other, nil
}

// RiderStep takes a linked rider's step on a delivery from their own
// business (the card, the Orders tool): in the rider's scope it finds the
// business's delivery, enters the business's side and takes the step as
// their button would, then records it on their order. action is one of
// Accept, Cant, PickedUp, Arrived, Delivered.
func (d *Dispatch) RiderStep(ctx context.Context, rider uuid.UUID, orderRef, action string) (Tap, links.DeliveryRef, error) {
	var t Tap
	var ref links.DeliveryRef
	var after []func() error
	err := d.tx(ctx, rider, func(tx pgx.Tx) error {
		var err error
		t, ref, after, err = d.riderStepTx(ctx, tx, rider, orderRef, action, member{})
		return err
	})
	if err != nil {
		return t, ref, err
	}
	return t, ref, run(after)
}

// member is a dispatch company's rider who took a step (phase 5): named
// on the business's card and to the customer. Empty: the business took it
// itself, by hand.
type member struct{ name, phone string }

// riderStepTx is RiderStep in tx, the rider's scope. m is the company's
// rider who took the step; empty when the rider business took it by its
// own hand, in which case a company's own delivery for the order follows
// the step (phase 5).
func (d *Dispatch) riderStepTx(ctx context.Context, tx pgx.Tx, rider uuid.UUID, orderRef, action string, m member) (Tap, links.DeliveryRef, []func() error, error) {
	var t Tap
	ref, err := links.DeliveryOfTx(ctx, tx, orderRef)
	if err != nil {
		return t, ref, nil, err
	}
	pair := uuid.Must(uuid.NewV7())
	if _, err := links.EnterTx(ctx, tx, ref.Link, "link.delivery."+action, nil, linkActor); err != nil {
		return t, ref, nil, err
	}
	x, err := scanDelivery(tx.QueryRow(ctx, `SELECT `+deliveryCols+` FROM deliveries d JOIN storefront_orders o ON o.id = d.order_id
		WHERE d.tenant_id = $1 AND d.id = $2 FOR UPDATE OF d`, ref.Business, ref.DeliveryID))
	if err != nil {
		return t, ref, nil, err
	}
	if _, err := tx.Exec(ctx, `UPDATE riders SET not_answering_since = NULL, missed = 0 WHERE id = $1 AND not_answering_since IS NOT NULL`, x.RiderID); err != nil {
		return t, ref, nil, err
	}
	ord, err := d.Orders.GetOrder(ctx, tx, ref.Business, x.Reference)
	if err != nil {
		return t, ref, nil, err
	}
	t, after, err := d.press(ctx, tx, ref.Business, x, ord, action, &pressOpt{pair: pair, member: m.name, memberPhone: m.phone})
	if err != nil {
		return t, ref, nil, err
	}
	if back := links.ReturnTx(ctx, tx, rider); back != nil {
		return t, ref, nil, back
	}
	if !t.Moved {
		return t, ref, after, nil
	}
	if err := links.DeliveryStepTx(ctx, tx, d.Vault, rider, ref.OrderID, stageOf(action), pair, true); err != nil {
		return t, ref, nil, err
	}
	if m.name == "" {
		// By the rider business's own hand: its own rider's delivery for
		// the order, when it is a company, follows.
		more, err := d.followInnerTx(ctx, tx, rider, ref.OrderID, action)
		if err != nil {
			return t, ref, nil, err
		}
		after = append(after, more...)
	}
	return t, ref, after, nil
}

// DeliveryOf is the business's delivery a rider's order is for, read from
// the rider's business.
func (d *Dispatch) DeliveryOf(ctx context.Context, rider uuid.UUID, orderRef string) (links.DeliveryRef, error) {
	var ref links.DeliveryRef
	err := d.tx(ctx, rider, func(tx pgx.Tx) error {
		var err error
		ref, err = links.DeliveryOfTx(ctx, tx, orderRef)
		return err
	})
	return ref, err
}

// stageOf is the card's stage for a rider's action.
func stageOf(action string) string {
	switch action {
	case Accept:
		return "accepted"
	case Cash:
		return Delivered
	}
	return action
}

// pressOpt is how a step reached press: from the rider's own business
// (pair: the cards' pair, the rider's side written by the caller), or
// nil, from their button or word.
type pressOpt struct {
	pair uuid.UUID
	// member is the dispatch company's rider who took the step, named on
	// the business's card and to the customer, with their phone (phase 5).
	member, memberPhone string
}
