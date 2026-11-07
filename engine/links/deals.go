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
)

// Deals (chat tools phase 7, docs/spec/chat-tools.md §9): wholesale price
// deliberation between linked businesses. A deal is an order whose prices
// are still being agreed. The buyer's offer is an order at its own prices
// (lines PricedByOffer); the seller counters with its change (StepChange);
// the buyer counters back (CounterTx), or accepts the change
// (AcceptChangeTx); the seller accepts an offer (StepAccept), its staff
// only at or above the floor (BelowFloor; the API asks the owner below
// it). Each step is kept, newest last, on each side's row; an offer
// lapses OfferFor after it was made.

// PricedByOffer is a price the buyer offered knowingly (a deal), not its
// last cost: the seller's side never reprices it.
const PricedByOffer = "offer"

// OfferFor is how long an offer or a counter stands.
const OfferFor = 48 * time.Hour

// DealStep is one offer or counter in a deal's history.
type DealStep struct {
	// By is "buyer" or "seller"; Who the person (or "WhatsApp").
	By        string     `json:"by"`
	Who       string     `json:"who,omitempty"`
	Lines     []DealLine `json:"lines"`
	TotalKobo int64      `json:"total_kobo"`
	// ListKobo is what the lines come to at the seller's prices (the
	// "was"); AsTotal: the side offered a total, spread over the lines.
	ListKobo int64     `json:"list_kobo,omitempty"`
	AsTotal  bool      `json:"as_total,omitempty"`
	Note     string    `json:"note,omitempty"`
	At       time.Time `json:"at"`
}

// DealLine is a line of a step.
type DealLine struct {
	Item      string `json:"item"`
	Quantity  int64  `json:"quantity"`
	PriceKobo int64  `json:"price_kobo"`
}

// IsOffer reports whether any line carries the buyer's offered price.
func IsOffer(lines []OrderLine) bool {
	for _, l := range lines {
		if l.PricedBy == PricedByOffer {
			return true
		}
	}
	return false
}

// listTotal is what lines come to at the seller's prices (each line's
// ListKobo, else its price).
func listTotal(lines []OrderLine) int64 {
	var t int64
	for _, l := range lines {
		p := l.ListKobo
		if p <= 0 {
			p = l.PriceKobo
		}
		t += l.Quantity * p
	}
	return t
}

// dealStep is a history entry for lines as they now stand.
func dealStep(by, who string, lines []OrderLine, asTotal bool, note string) DealStep {
	s := DealStep{By: by, Who: who, AsTotal: asTotal, Note: note, At: time.Now().UTC(), ListKobo: listTotal(lines)}
	for _, l := range lines {
		if l.Quantity == 0 {
			continue
		}
		s.Lines = append(s.Lines, DealLine{Item: l.Item, Quantity: l.Quantity, PriceKobo: l.PriceKobo})
		s.TotalKobo += l.Quantity * l.PriceKobo
	}
	return s
}

// MaxDealSteps is how many offers and counters a deal keeps.
const MaxDealSteps = 20

// withStep is a history with one more step (the oldest dropped past
// MaxDealSteps).
func withStep(history []DealStep, s DealStep) []byte {
	history = append(history, s)
	if len(history) > MaxDealSteps {
		history = history[len(history)-MaxDealSteps:]
	}
	raw, _ := json.Marshal(history)
	return raw
}

// SpreadTotal gives each line a price so the lines come to about total,
// in proportion to their prices now (to the kobo; a remainder under a
// naira a line). Lines with no quantity keep their price.
func SpreadTotal(prices []int64, quantities []int64, total int64) ([]int64, error) {
	if len(prices) != len(quantities) {
		return nil, fmt.Errorf("%w: one price for each line", ErrInvalid)
	}
	var now int64
	for i := range prices {
		now += prices[i] * quantities[i]
	}
	if total <= 0 || now <= 0 {
		return nil, fmt.Errorf("%w: a total more than ₦0", ErrInvalid)
	}
	out := make([]int64, len(prices))
	for i := range prices {
		if quantities[i] <= 0 {
			out[i] = prices[i]
			continue
		}
		// This line's share of the total, per unit.
		share := float64(prices[i]*quantities[i]) / float64(now) * float64(total)
		p := int64(share / float64(quantities[i]))
		if p < 1 {
			p = 1
		}
		out[i] = p
	}
	return out, nil
}

// Floors is the business in scope's lowest prices by its item.
func FloorsTx(ctx context.Context, tx pgx.Tx, tenant uuid.UUID) (map[string]int64, error) {
	rows, err := tx.Query(ctx, `SELECT item_id, floor_kobo FROM trade_floors WHERE tenant_id = $1`, tenant)
	if err != nil {
		return nil, err
	}
	out := map[string]int64{}
	for rows.Next() {
		var id string
		var f int64
		if err := rows.Scan(&id, &f); err != nil {
			rows.Close()
			return nil, err
		}
		out[id] = f
	}
	rows.Close()
	return out, rows.Err()
}

// SetFloorTx sets an item's lowest price (0: none).
func SetFloorTx(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, item string, kobo int64, by string) error {
	item = strings.TrimSpace(item)
	if item == "" || len(item) > 100 {
		return fmt.Errorf("%w: which item", ErrInvalid)
	}
	if kobo < 0 || kobo > 100000000000 {
		return fmt.Errorf("%w: a lowest price from ₦0", ErrInvalid)
	}
	if kobo == 0 {
		_, err := tx.Exec(ctx, `DELETE FROM trade_floors WHERE tenant_id = $1 AND item_id = $2`, tenant, item)
		return err
	}
	_, err := tx.Exec(ctx, `INSERT INTO trade_floors (tenant_id, item_id, floor_kobo, updated_by) VALUES ($1, $2, $3, $4)
		ON CONFLICT (tenant_id, item_id) DO UPDATE SET floor_kobo = EXCLUDED.floor_kobo, updated_by = EXCLUDED.updated_by, updated_at = now()`, tenant, item, kobo, by)
	return err
}

// DealSettings is how far a business's staff may go on a whole order.
type DealSettings struct {
	// MaxDiscountPct: the largest discount off the seller's prices on a
	// whole order its staff may agree (nil: no such limit).
	MaxDiscountPct *int `json:"max_discount_pct"`
}

// DealSettingsTx reads the business in scope's.
func DealSettingsTx(ctx context.Context, tx pgx.Tx, tenant uuid.UUID) (DealSettings, error) {
	var s DealSettings
	err := tx.QueryRow(ctx, `SELECT max_discount_pct FROM trade_deal_settings WHERE tenant_id = $1`, tenant).Scan(&s.MaxDiscountPct)
	if errors.Is(err, pgx.ErrNoRows) {
		return s, nil
	}
	return s, err
}

// SetDealSettingsTx writes them.
func SetDealSettingsTx(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, s DealSettings, by string) error {
	if s.MaxDiscountPct != nil && (*s.MaxDiscountPct < 1 || *s.MaxDiscountPct > 90) {
		return fmt.Errorf("%w: the largest discount is 1%% to 90%%", ErrInvalid)
	}
	_, err := tx.Exec(ctx, `INSERT INTO trade_deal_settings (tenant_id, max_discount_pct, updated_by) VALUES ($1, $2, $3)
		ON CONFLICT (tenant_id) DO UPDATE SET max_discount_pct = EXCLUDED.max_discount_pct, updated_by = EXCLUDED.updated_by, updated_at = now()`, tenant, s.MaxDiscountPct, by)
	return err
}

// BelowFloor says why an order's prices are past what staff may agree:
// a line under its item's lowest price, or the whole order further off
// the seller's prices than the largest discount. Nothing: within.
func BelowFloor(lines []OrderLine, floors map[string]int64, s DealSettings) []string {
	var why []string
	for _, l := range lines {
		if l.Quantity == 0 || l.ItemID == "" {
			continue
		}
		if f := floors[l.ItemID]; f > 0 && l.PriceKobo < f {
			why = append(why, fmt.Sprintf("%s at %s is under its lowest price, %s", l.Item, naira(l.PriceKobo), naira(f)))
		}
	}
	if s.MaxDiscountPct != nil {
		list, total := listTotal(lines), linesTotal(lines)
		if list > 0 && total < list && (list-total)*100 > list*int64(*s.MaxDiscountPct) {
			why = append(why, fmt.Sprintf("%s is more than %d%% off %s", naira(total), *s.MaxDiscountPct, naira(list)))
		}
	}
	return why
}

// AskOwnerTx marks an offer staff accepted below the floor: it waits for
// the owner (the API tells them). Again for the same order: no change.
func AskOwnerTx(ctx context.Context, tx pgx.Tx, order uuid.UUID, by string) (bool, error) {
	tag, err := tx.Exec(ctx, `UPDATE link_orders SET deal_owner_at = now(), deal_owner_by = $2, updated_at = now() WHERE order_id = $1 AND deal_owner_at IS NULL`, order, by)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() == 1, nil
}

// CounterTx is the buyer's counter to the seller's change (chat tools
// phase 7): its own prices for the change's lines (the seller's
// quantities), back to the seller as an offer, in one transaction.
func CounterTx(ctx context.Context, tx pgx.Tx, buyer, id uuid.UUID, prices []int64, asTotal bool, note, who string, by Actor) (*Told, error) {
	note = strings.TrimSpace(note)
	if len([]rune(note)) > 500 {
		return nil, fmt.Errorf("%w: a word for them is up to 500 characters", ErrInvalid)
	}
	p, err := purchaseTx(ctx, tx, id)
	if err != nil {
		return nil, err
	}
	if p.Link == nil || p.SellerStatus != OrderChanged || p.Change == nil || p.Status != "sent" {
		return nil, fmt.Errorf("%w: %s has no answer of theirs to counter", ErrState, p.Reference)
	}
	if len(prices) != len(p.Change) {
		return nil, fmt.Errorf("%w: one price for each line", ErrInvalid)
	}
	var lines []purchaseLine
	for i, l := range p.Lines {
		if i >= len(p.Change) || p.Change[i].Quantity == 0 {
			continue
		}
		if prices[i] < 1 || prices[i] > 100000000000 {
			return nil, fmt.Errorf("%w: %s needs a price", ErrInvalid, l.Item)
		}
		l.Quantity, l.CostKobo = p.Change[i].Quantity, prices[i]
		lines = append(lines, l)
	}
	if len(lines) == 0 {
		return nil, fmt.Errorf("%w: nothing left to counter", ErrState)
	}
	// The buyer's side first: the door narrows the scope to the seller.
	raw, _ := json.Marshal(lines)
	var total int64
	for _, l := range lines {
		total += l.Quantity * l.CostKobo
	}
	if _, err := tx.Exec(ctx, `UPDATE purchase_orders SET lines = $2, total_kobo = $3, change = NULL, seller_status = 'received', offer = true, updated_at = now()
		WHERE id = $1`, id, raw, total); err != nil {
		return nil, err
	}
	if err := stockAuditTx(ctx, tx, buyer, "purchase.countered", p.Reference, id, by); err != nil {
		return nil, err
	}
	ok, err := active(ctx, tx, *p.Link)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, fmt.Errorf("%w: the link with this supplier has ended", ErrState)
	}
	name, err := businessName(ctx, tx, buyer)
	if err != nil {
		return nil, err
	}
	detail, _ := json.Marshal(map[string]any{"reference": p.Reference})
	seller, err := EnterTx(ctx, tx, *p.Link, "link.order.countered", detail, by)
	if err != nil {
		return nil, err
	}
	o, err := orderOf(ctx, tx, *p.Link, p.ID)
	if err != nil {
		return nil, err
	}
	if o.Status != OrderChanged || o.Change == nil {
		return nil, fmt.Errorf("%w: %s changed its answer; look again", ErrState, o.BuyerName)
	}
	var next []OrderLine
	for i, l := range o.Change {
		if l.Quantity == 0 {
			continue
		}
		list := l.ListKobo
		if list == 0 && i < len(o.Lines) {
			list = o.Lines[i].ListKobo
		}
		next = append(next, OrderLine{Item: l.Item, Unit: l.Unit, Quantity: l.Quantity, PriceKobo: prices[i], ItemID: l.ItemID, Matched: l.Matched,
			ProposedKobo: prices[i], PricedBy: PricedByOffer, ListKobo: list})
	}
	if err := putLines(ctx, tx, o.ID, next); err != nil {
		return nil, err
	}
	s := dealStep("buyer", who, next, asTotal, note)
	if _, err := tx.Exec(ctx, `UPDATE link_orders SET change = NULL, seller_note = NULL, deal_history = $2, deal_until = now() + $3 * interval '1 second',
		deal_owner_at = NULL, deal_owner_by = NULL, note = CASE WHEN $4 <> '' THEN $4 ELSE note END, updated_at = now() WHERE order_id = $1`,
		o.ID, withStep(o.DealHistory, s), int64(OfferFor/time.Second), note); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `UPDATE storefront_orders SET status = 'received', updated_at = now() WHERE id = $1`, o.ID); err != nil {
		return nil, err
	}
	return &Told{Tenant: seller, Event: "countered", Reference: p.Reference, From: name, Order: o.ID, Purchase: p.ID, Link: *p.Link, FromTenant: buyer}, nil
}

// lapsed reports whether an order's offer has lapsed (OfferFor gone
// since it was made); one with no deal never lapses.
func lapsed(o Order) bool {
	return o.DealUntil != nil && time.Now().After(*o.DealUntil) && IsOffer(o.Lines)
}

// dealJSON is a history as its column holds it: NULL for none.
func dealJSON(h []DealStep) []byte {
	if len(h) == 0 {
		return nil
	}
	raw, _ := json.Marshal(h)
	return raw
}

// CounterIn is a seller's counter as one total: spread over the order's
// lines in proportion to the seller's own prices (the price list's where
// the line has one, else the line's own).
func CounterIn(o Order, total int64, note string) (StepIn, error) {
	cur, qs := make([]int64, len(o.Lines)), make([]int64, len(o.Lines))
	for i, l := range o.Lines {
		cur[i], qs[i] = l.ListKobo, l.Quantity
		if cur[i] <= 0 {
			cur[i] = l.PriceKobo
		}
	}
	prices, err := SpreadTotal(cur, qs, total)
	if err != nil {
		return StepIn{}, err
	}
	in := StepIn{AsTotal: true, Note: note, Lines: make([]OrderLine, len(o.Lines))}
	for i, l := range o.Lines {
		in.Lines[i] = OrderLine{Item: l.Item, Quantity: l.Quantity, PriceKobo: prices[i]}
	}
	return in, nil
}
