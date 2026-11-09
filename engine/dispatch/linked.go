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
func (d *Dispatch) placeLinked(ctx context.Context, tx pgx.Tx, tenant, delivery uuid.UUID, r riders.Rider, ord storefront.Order, fee int64, expires time.Time, s Settings) error {
	if r.LinkID == nil || d.Vault == nil {
		return nil
	}
	order := uuid.Must(uuid.NewV7())
	pair := uuid.Must(uuid.NewV7())
	if _, err := tx.Exec(ctx, `UPDATE deliveries SET link_order_id = $2 WHERE id = $1`, delivery, order); err != nil {
		return err
	}
	var business string
	if err := tx.QueryRow(ctx, `SELECT name FROM tenants WHERE id = $1`, tenant).Scan(&business); err != nil {
		return err
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
		return back
	}
	var off *links.OtherOffError
	if errors.As(err, &off) || errors.Is(err, links.ErrNotFound) {
		// The order cannot reach them: the chat app's offer stands alone.
		d.log().Warn("dispatch: a linked rider's order could not be placed", "tenant", tenant, "rider", r.ID, "err", err)
		_, err = tx.Exec(ctx, `UPDATE deliveries SET link_order_id = NULL WHERE id = $1`, delivery)
		return err
	}
	if err != nil {
		return err
	}
	return links.CardTx(ctx, tx, tenant, *r.LinkID, pair, true, links.DocCard{Doc: links.DocDispatch, Event: StageOffer + "ed", Reference: ord.Reference,
		ID: delivery.String(), AmountKobo: &fee, Note: r.Name})
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
		if ref, err = links.DeliveryOfTx(ctx, tx, orderRef); err != nil {
			return err
		}
		pair := uuid.Must(uuid.NewV7())
		if _, err := links.EnterTx(ctx, tx, ref.Link, "link.delivery."+action, nil, linkActor); err != nil {
			return err
		}
		x, err := scanDelivery(tx.QueryRow(ctx, `SELECT `+deliveryCols+` FROM deliveries d JOIN storefront_orders o ON o.id = d.order_id
			WHERE d.tenant_id = $1 AND d.id = $2 FOR UPDATE OF d`, ref.Business, ref.DeliveryID))
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE riders SET not_answering_since = NULL, missed = 0 WHERE id = $1 AND not_answering_since IS NOT NULL`, x.RiderID); err != nil {
			return err
		}
		ord, err := d.Orders.GetOrder(ctx, tx, ref.Business, x.Reference)
		if err != nil {
			return err
		}
		t, after, err = d.press(ctx, tx, ref.Business, x, ord, action, &pressOpt{pair: pair})
		if err != nil {
			return err
		}
		if back := links.ReturnTx(ctx, tx, rider); back != nil {
			return back
		}
		if !t.Moved {
			return nil
		}
		return links.DeliveryStepTx(ctx, tx, d.Vault, rider, ref.OrderID, stageOf(action), pair, true)
	})
	if err != nil {
		return t, ref, err
	}
	return t, ref, run(after)
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
}
