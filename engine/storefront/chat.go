package storefront

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Orders from customers' messages (chat-five-more-flows.md, flow 1, phase
// 2): an order staff confirmed from what a customer wrote to the
// business's own number. It is an order of the kind chat, decided when it
// is placed (a person confirmed it), so payment matching, stock and
// dispatch work on it as on an order from the page; the customer is told
// in their WhatsApp thread, through ChatNotify, not by SMS.

// How a chat order is paid.
const (
	// ChatPayTransfer: the customer pays from the order's status page, by
	// transfer (an account number for the order) or card. Only for a
	// business with a storefront: the page is its.
	ChatPayTransfer = "transfer"
	// ChatPayOnDelivery: at the door, to the rider (delivery only).
	ChatPayOnDelivery = "on_delivery"
	// ChatPayLater: staff arrange it; money that comes in is matched to
	// the order as to any order waiting for its payment.
	ChatPayLater = "later"
)

// ChatHold is how long a chat order waits for its payment by transfer
// before it lapses.
const ChatHold = 48 * time.Hour

// ChatOrder is an order staff confirmed from a customer's message.
type ChatOrder struct {
	Customer     Customer // name, phone (E.164), address
	Lines        []Line   // each an item of the catalogue, priced
	Fulfilment   string   // delivery | pickup
	Zone         string   // where to, for a delivery
	DeliveryKobo int64
	Pay          string
	Note         string
	By           string // who confirmed it
}

// ErrNoPage is a chat order to be paid on a page the business does not
// have.
var ErrNoPage = errors.New("storefront: paying by transfer needs your shop's page")

// PlaceChatTx writes a chat order in the caller's transaction, decided:
// waiting for its payment when it is paid by transfer, accepted otherwise.
// Its stock is lowered after commit (LowerStock), at once unless it waits
// for a transfer, then when it is paid (Paid).
func (o *Orders) PlaceChatTx(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, in ChatOrder) (Order, error) {
	if len(in.Lines) == 0 || len(in.Lines) > 50 {
		return Order{}, fmt.Errorf("%w: an order has 1 to 50 lines", ErrInvalid)
	}
	if in.Customer.Phone == "" {
		return Order{}, fmt.Errorf("%w: the customer's number", ErrInvalid)
	}
	var total int64
	for i, l := range in.Lines {
		if l.ID == "" || l.Quantity < 1 || l.PriceKobo < 1 {
			return Order{}, fmt.Errorf("%w: every line is an item, with a quantity and a price", ErrInvalid)
		}
		in.Lines[i].LineKobo = int64(l.Quantity) * l.PriceKobo
		total += in.Lines[i].LineKobo
	}
	if in.Fulfilment != "delivery" && in.Fulfilment != "pickup" {
		return Order{}, fmt.Errorf("%w: delivery or pickup", ErrInvalid)
	}
	if in.Fulfilment != "delivery" {
		in.Zone, in.DeliveryKobo = "", 0
	}
	if in.DeliveryKobo < 0 || in.DeliveryKobo > MaxZoneFeeKobo {
		return Order{}, fmt.Errorf("%w: a delivery fee up to %s", ErrInvalid, FormatNaira(MaxZoneFeeKobo))
	}
	total += in.DeliveryKobo
	var sf *uuid.UUID
	var slug string
	if err := tx.QueryRow(ctx, `SELECT id, COALESCE(published_slug, slug) FROM storefronts WHERE tenant_id = $1`, tenant).Scan(&sf, &slug); err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return Order{}, err
	}
	status, payment := "accepted", "none"
	var expires *time.Time
	switch in.Pay {
	case ChatPayTransfer:
		if sf == nil {
			return Order{}, ErrNoPage
		}
		status, payment = "awaiting_payment", "at_order"
		e := o.now().Add(ChatHold)
		expires = &e
	case ChatPayOnDelivery:
		if in.Fulfilment != "delivery" {
			return Order{}, fmt.Errorf("%w: paying when it arrives is for a delivery", ErrInvalid)
		}
		payment = "on_delivery"
	case ChatPayLater:
	default:
		return Order{}, fmt.Errorf("%w: paid by transfer, when it arrives, or later", ErrInvalid)
	}
	envPhone, err := o.Vault.SealTx(ctx, tx, tenant, "phone", in.Customer.Phone)
	if err != nil {
		return Order{}, err
	}
	subject, _ := envPhone["subject"].(string)
	customer, err := o.Vault.SealUnderTx(ctx, tx, tenant, subject, "customer", in.Customer)
	if err != nil {
		return Order{}, err
	}
	key, hash := NewStatusKey()
	sealedKey, err := o.Vault.SealUnderTx(ctx, tx, tenant, subject, "status_key", key)
	if err != nil {
		return Order{}, err
	}
	basket, _ := json.Marshal(in.Lines)
	var zone *string
	var delivery *int64
	if in.Fulfilment == "delivery" {
		if z := strings.TrimSpace(in.Zone); z != "" {
			if r := []rune(z); len(r) > MaxZoneName {
				z = string(r[:MaxZoneName])
			}
			zone = &z
		}
		if in.DeliveryKobo > 0 {
			delivery = &in.DeliveryKobo
		}
	}
	var note *string
	if n := strings.TrimSpace(in.Note); n != "" {
		if r := []rune(n); len(r) > 500 {
			n = string(r[:500])
		}
		note = &n
	}
	id := uuid.Must(uuid.NewV7())
	var ref string
	for try := 0; ; try++ {
		ref = NewReference(slug)
		tag, err := tx.Exec(ctx, `INSERT INTO storefront_orders (id, tenant_id, storefront_id, kind, reference, status_hash, status, customer, subject_id, basket,
			total_kobo, fulfilment, payment, expires_at, status_key, note, zone, delivery_kobo, decided_by, decided_at)
			VALUES ($1, $2, $3, 'chat', $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, now())
			ON CONFLICT (reference) DO NOTHING`,
			id, tenant, sf, ref, hash, status, customer, subject, basket, total, in.Fulfilment, payment, expires, sealedKey, note, zone, delivery, in.By)
		if err != nil {
			return Order{}, err
		}
		if tag.RowsAffected() == 1 {
			break
		}
		if try == 4 {
			return Order{}, errors.New("storefront: no free reference for the order")
		}
	}
	if err := audit(ctx, tx, tenant, "storefront.order_confirmed", ref, map[string]any{"kind": "chat", "by": in.By, "pay": in.Pay}); err != nil {
		return Order{}, err
	}
	return o.GetOrder(ctx, tx, tenant, ref)
}

// ChatStatusLink is a chat order's status page (where it is paid), or ""
// when the business has no storefront.
func (o *Orders) ChatStatusLink(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, ord Order) (string, error) {
	if ord.storefront == uuid.Nil {
		return "", nil
	}
	return o.linkOf(ctx, tx, tenant, ord)
}
