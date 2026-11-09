package links

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/pii"
	"github.com/israel-duff/taskiem/engine/storefront"
)

// Deliveries (docs/spec/chat-riders.md, decision 0065, phase 1): a rider
// that is a business on Taskiem, linked to the business it delivers for,
// receives a delivery as a linked order in its own business. The
// business's deliveries row stays the record of the delivery; the rider's
// order is how they receive it and answer it: one card on both sides,
// moved from the card, the Orders tool, or their WhatsApp buttons alike.
//
// The order is a trade order of the rider's with one line, the delivery
// at its fee; what a delivery carries beyond that (where to pick up, the
// area, whether a photo is asked, whether the customer pays at the door,
// when the offer runs out, the steps' times) is on link_orders. The
// customer's name, phone, address, landmark and pin are sealed with the
// order as its customer, and cleared once the delivery is done.

// The documents a delivery's cards name: the rider's order and the
// business's delivery.
const (
	DocDelivery = "delivery"
	DocDispatch = "dispatch"
)

// OrderArrived is a delivery order at the door (storefront status arrived).
const OrderArrived = "arrived"

// Delivery is what a linked order carries when it is a delivery.
type Delivery struct {
	DeliveryID    uuid.UUID  `json:"delivery_id"`
	Pickup        string     `json:"pickup,omitempty"`
	PickupLink    string     `json:"pickup_link,omitempty"`
	DropArea      string     `json:"drop_area,omitempty"`
	ProofRequired bool       `json:"proof_required"`
	DoorPayment   bool       `json:"door_payment"`
	ExpiresAt     *time.Time `json:"expires_at,omitempty"`
	PickedUpAt    *time.Time `json:"picked_up_at,omitempty"`
	ArrivedAt     *time.Time `json:"arrived_at,omitempty"`
	// Customer is who it goes to, shown to the rider while they hold it
	// (filled by the API from the sealed order).
	Customer *storefront.Customer `json:"customer,omitempty"`
}

// Held reports whether the rider holds the delivery: when the customer's
// details are theirs to see.
func (o Order) Held() bool {
	if o.Delivery == nil {
		return false
	}
	switch o.OrderStatus {
	case "accepted", "dispatched", "on_the_way", "arrived":
		return true
	}
	return false
}

// DeliveryIn is a delivery offered to a linked rider.
type DeliveryIn struct {
	Link       uuid.UUID
	DeliveryID uuid.UUID
	// OrderID is the rider's order, chosen by the caller so the business
	// can keep it on its delivery before entering the rider's side.
	OrderID uuid.UUID
	// Pair is the cards' pair; the business's card is the caller's.
	Pair uuid.UUID
	// BusinessID is the business offering it; Business its name; Reference
	// the order's.
	BusinessID                 uuid.UUID
	Business, Reference        string
	Pickup, PickupLink, Area   string
	FeeKobo                    int64
	Customer                   storefront.Customer
	ProofRequired, DoorPayment bool
	ExpiresAt                  time.Time
}

// PlaceDeliveryTx writes a delivery into the rider's business as an order,
// through the door (the caller's scope is the business's, its own rows
// written; the scope is the rider's afterwards, until ReturnTx). It
// answers the rider's business. A delivery already placed is left as it
// is.
func PlaceDeliveryTx(ctx context.Context, tx pgx.Tx, v Vault, in DeliveryIn, by Actor) (uuid.UUID, error) {
	detail, _ := json.Marshal(map[string]any{"reference": in.Reference, "delivery": in.DeliveryID})
	rider, err := EnterTx(ctx, tx, in.Link, "link.delivery.offered", detail, by)
	if err != nil {
		return uuid.Nil, err
	}
	var have uuid.UUID
	err = tx.QueryRow(ctx, `SELECT order_id FROM link_orders WHERE link_id = $1 AND source_id = $2`, in.Link, in.DeliveryID).Scan(&have)
	if err == nil {
		return rider, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return rider, err
	}
	var sf *uuid.UUID
	if err := tx.QueryRow(ctx, `SELECT s.id FROM storefronts s WHERE s.tenant_id = $1`, rider).Scan(&sf); err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return rider, err
	}
	customer, subject, err := sealDeliveryCustomer(ctx, tx, v, rider, in.Customer)
	if err != nil {
		return rider, err
	}
	_, hash := storefront.NewStatusKey()
	line := deliveryLine(in.Area, in.FeeKobo)
	b, _ := json.Marshal(basket([]OrderLine{line}))
	// The business's own order has the reference; the rider's is marked
	// as the delivery of it, then suffixed should that be taken too.
	ref := in.Reference + "-D"
	for try := 0; ; try++ {
		tag, err := tx.Exec(ctx, `INSERT INTO storefront_orders (id, tenant_id, storefront_id, kind, reference, status_hash, status, customer, subject_id, basket,
			total_kobo, fulfilment, payment, zone) VALUES ($1, $2, $3, 'trade', $4, $5, 'received', $6, $7, $8, $9, 'delivery', 'none', $10)
			ON CONFLICT (reference) DO NOTHING`, in.OrderID, rider, sf, ref, hash, customer, subject, b, in.FeeKobo, in.Area)
		if err != nil {
			return rider, err
		}
		if tag.RowsAffected() == 1 {
			break
		}
		if try == 4 {
			return rider, errors.New("links: no free reference for the delivery")
		}
		ref = in.Reference + "-D" + newReference()[2:4]
	}
	raw, _ := json.Marshal([]OrderLine{line})
	if _, err := tx.Exec(ctx, `INSERT INTO link_orders (order_id, tenant_id, link_id, buyer_tenant, buyer_name, source_id, purchase_reference, lines, note,
		delivery_id, pickup, pickup_link, drop_area, proof_required, door_payment, expires_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, '', $6, $9, $10, $11, $12, $13, $14)`,
		in.OrderID, rider, in.Link, in.BusinessID, in.Business, in.DeliveryID, in.Reference, raw, in.Pickup, in.PickupLink, in.Area, in.ProofRequired, in.DoorPayment, in.ExpiresAt); err != nil {
		return rider, err
	}
	fee := in.FeeKobo
	return rider, CardTx(ctx, tx, rider, in.Link, in.Pair, false, DocCard{Doc: DocDelivery, Event: "offered", Reference: in.Reference, ID: in.OrderID.String(), AmountKobo: &fee})
}

// deliveryLine is a delivery order's one line.
func deliveryLine(area string, fee int64) OrderLine {
	item := "Delivery"
	if area != "" {
		item = "Delivery to " + area
	}
	return OrderLine{Item: item, Unit: "delivery", Quantity: 1, PriceKobo: fee, ProposedKobo: fee, PricedBy: PricedBySeller}
}

// sealDeliveryCustomer seals who a delivery goes to, under the customer's
// phone as the subject (a business's key when there is none), in the
// rider's business.
func sealDeliveryCustomer(ctx context.Context, tx pgx.Tx, v Vault, rider uuid.UUID, c storefront.Customer) (map[string]any, string, error) {
	var env map[string]any
	var err error
	if c.Phone != "" {
		env, err = v.SealTx(ctx, tx, rider, "phone", c.Phone)
	} else {
		env, err = v.SealTx(ctx, tx, rider, "business", KeyPrefix+"delivery")
	}
	if err != nil {
		return nil, "", err
	}
	subject, _ := env["subject"].(string)
	sealed, err := v.SealUnderTx(ctx, tx, rider, subject, "customer", storefront.Customer{Name: c.Name, Phone: c.Phone, Address: c.Address, Landmark: c.Landmark, Pin: c.Pin})
	return sealed, subject, err
}

// DeliveryStage is a step of a delivery as the business's record names
// it (engine/dispatch): accepted, cant, picked_up, arrived, delivered,
// passed, withdrawn, released.
var deliveryStatus = map[string]string{
	"accepted":  "accepted",
	"picked_up": "dispatched",
	"arrived":   OrderArrived,
	"delivered": "completed",
	"cant":      "declined",
	"passed":    "cancelled",
	"withdrawn": "cancelled",
	"released":  "cancelled",
}

// DeliveryStepTx records a delivery's step on the rider's order, in the
// rider's scope: its status, the step's time, the customer's details
// cleared once the delivery is done, and the card in the conversation
// (mine: the rider took the step). A stage the order has passed changes
// nothing but the card.
func DeliveryStepTx(ctx context.Context, tx pgx.Tx, v Vault, rider, order uuid.UUID, stage string, pair uuid.UUID, mine bool) error {
	status, ok := deliveryStatus[stage]
	if !ok {
		return fmt.Errorf("%w: no delivery step %q", ErrInvalid, stage)
	}
	o, err := GetOrderTx(ctx, tx, order.String(), true)
	if err != nil {
		return err
	}
	if o.Delivery == nil {
		return fmt.Errorf("%w: %s is not a delivery", ErrState, o.Reference)
	}
	if _, err := tx.Exec(ctx, `UPDATE storefront_orders SET status = $2, decided_at = CASE WHEN $2 = 'accepted' THEN now() ELSE decided_at END,
		decided_by = CASE WHEN $2 = 'accepted' THEN 'rider' ELSE decided_by END, updated_at = now()
		WHERE id = $1 AND status NOT IN ('completed', 'cancelled', 'declined')`, o.ID, status); err != nil {
		return err
	}
	col := map[string]string{"picked_up": "picked_up_at", "arrived": "arrived_at", "delivered": "delivered_at"}[stage]
	if col != "" {
		if _, err := tx.Exec(ctx, `UPDATE link_orders SET `+col+` = COALESCE(`+col+`, now()), updated_at = now() WHERE order_id = $1`, o.ID); err != nil {
			return err
		}
	}
	if stage == "delivered" {
		one := int64(1)
		o.Lines[0].Delivered = &one
		raw, _ := json.Marshal(o.Lines)
		if _, err := tx.Exec(ctx, `UPDATE link_orders SET lines = $2 WHERE order_id = $1`, o.ID, raw); err != nil {
			return err
		}
	}
	if status == "completed" || status == "cancelled" || status == "declined" {
		// The customer's details leave the rider's side with the delivery;
		// the name stays, as the record of who it went to. An erased
		// customer has nothing left to clear.
		c, err := openDeliveryCustomer(ctx, tx, v, rider, o.ID)
		switch {
		case errors.Is(err, pii.ErrErased):
		case err != nil:
			return err
		default:
			sealed, _, err := sealDeliveryCustomer(ctx, tx, v, rider, storefront.Customer{Name: c.Name})
			if err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `UPDATE storefront_orders SET customer = $2 WHERE id = $1`, o.ID, sealed); err != nil {
				return err
			}
		}
	}
	fee := o.TotalKobo
	return CardTx(ctx, tx, rider, o.LinkID, pair, mine, DocCard{Doc: DocDelivery, Event: stage, Reference: o.Reference, ID: o.ID.String(), AmountKobo: &fee})
}

// DeliveryRef is what a rider's delivery order points at: the link, the
// business, and its delivery there.
type DeliveryRef struct {
	Link, Business, DeliveryID, OrderID uuid.UUID
	Reference                           string
	Status                              string
}

// DeliveryOfTx reads, in the rider's scope, the delivery a linked order
// is for (by the order's id or reference).
func DeliveryOfTx(ctx context.Context, tx pgx.Tx, ref string) (DeliveryRef, error) {
	o, err := GetOrderTx(ctx, tx, ref, false)
	if err != nil {
		return DeliveryRef{}, err
	}
	if o.Delivery == nil {
		return DeliveryRef{}, fmt.Errorf("%w: %s is not a delivery", ErrState, o.Reference)
	}
	return DeliveryRef{Link: o.LinkID, Business: o.Buyer, DeliveryID: o.Delivery.DeliveryID, OrderID: o.ID, Reference: o.Reference, Status: o.OrderStatus}, nil
}

// OpenDeliveryCustomerTx opens who a delivery order goes to, for the rider
// who holds it.
func OpenDeliveryCustomerTx(ctx context.Context, tx pgx.Tx, v Vault, rider, order uuid.UUID) (storefront.Customer, error) {
	return openDeliveryCustomer(ctx, tx, v, rider, order)
}

func openDeliveryCustomer(ctx context.Context, tx pgx.Tx, v Vault, rider, order uuid.UUID) (storefront.Customer, error) {
	var env map[string]any
	if err := tx.QueryRow(ctx, `SELECT customer FROM storefront_orders WHERE tenant_id = $1 AND id = $2`, rider, order).Scan(&env); err != nil {
		return storefront.Customer{}, err
	}
	got, err := v.OpenTx(ctx, tx, rider, env)
	if err != nil {
		return storefront.Customer{}, err
	}
	raw, _ := json.Marshal(got)
	var c storefront.Customer
	return c, json.Unmarshal(raw, &c)
}

// ReturnTx puts the transaction's scope back on the caller's own business
// after EnterTx took it to the other side: only ever the side the
// transaction began in, so the door's audit of the crossing stands and
// nothing is reached that the caller could not reach before.
func ReturnTx(ctx context.Context, tx pgx.Tx, tenant uuid.UUID) error {
	_, err := tx.Exec(ctx, `SELECT set_config('app.tenant_scope', $1, true)`, db.Scope(tenant))
	return err
}

// DeliveryRefTx is DeliveryOfTx by one small query, for the API's check
// of which path a step takes: ErrNotFound when the order is not a
// delivery of the business in scope.
func DeliveryRefTx(ctx context.Context, tx pgx.Tx, ref string) (DeliveryRef, error) {
	var r DeliveryRef
	q := `SELECT o.link_id, o.buyer_tenant, o.delivery_id, s.id, o.purchase_reference, s.status FROM link_orders o JOIN storefront_orders s ON s.id = o.order_id
		WHERE o.delivery_id IS NOT NULL AND `
	var row pgx.Row
	if id, err := uuid.Parse(ref); err == nil {
		row = tx.QueryRow(ctx, q+`s.id = $1`, id)
	} else {
		row = tx.QueryRow(ctx, q+`(s.reference = $1 OR o.purchase_reference = $1) ORDER BY s.created_at DESC LIMIT 1`, strings.ToUpper(strings.TrimSpace(ref)))
	}
	err := row.Scan(&r.Link, &r.Business, &r.DeliveryID, &r.OrderID, &r.Reference, &r.Status)
	if errors.Is(err, pgx.ErrNoRows) {
		return r, fmt.Errorf("%w: no such delivery", ErrNotFound)
	}
	return r, err
}
