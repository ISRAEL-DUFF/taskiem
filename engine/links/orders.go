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

	"github.com/israel-duff/taskiem/engine/pii"
	"github.com/israel-duff/taskiem/engine/storefront"
)

// Orders (L2, docs/spec/business-links.md §7): a purchase order to a
// supplier that is a linked business becomes an order in the seller's
// business: a storefront order of the kind trade, the buyer its customer,
// so it is on the seller's Orders page, starts its order workflow and is
// delivered by its riders like any order. What a linked order has beyond
// a storefront order (its lines with units and what was delivered, the
// seller's change, the buyer's cancel request and count, the invoice) is
// in link_orders beside it, and the order's basket and total are kept
// from its lines.
//
// The send is noted by a trigger and written by the worker (WriteOrderTx).
// The buyer's steps write the buyer's record, then the seller's through
// EnterTx, in one transaction. The seller's order moves by many paths (its
// steps here, the Orders page, a workflow, a rider), so each change to it
// is noted by a trigger and mirrored to the purchase order by the worker
// (MirrorOrderTx), which also says who to tell.

// Order statuses, as either side says them (the seller's order's status
// read through TradeStatus).
const (
	OrderProposed   = "proposed" // drafted by the seller for the buyer, waiting for its Confirm
	OrderReceived   = "received"
	OrderChanged    = "changed"
	OrderAccepted   = "accepted"
	OrderReady      = "ready"
	OrderDispatched = "dispatched"
	OrderDelivered  = "delivered"
	OrderDeclined   = "declined"
	OrderCancelled  = "cancelled"
)

// TradeStatus is a storefront order's status as a linked order says it:
// a rider's on the way and at the door are dispatched; completed is
// delivered; an accepted order the seller has ready is ready.
func TradeStatus(status string, ready bool) string {
	switch status {
	case "accepted", "paid":
		if ready {
			return OrderReady
		}
		return OrderAccepted
	case "dispatched", "on_the_way", "arrived":
		return OrderDispatched
	case "completed":
		return OrderDelivered
	case "declined":
		return OrderDeclined
	case "cancelled", "refund_due", "refunded", "expired", "no_show":
		return OrderCancelled
	}
	return status // proposed, received, changed
}

// live reports whether an order is accepted and not yet delivered.
func live(status string) bool {
	return status == OrderAccepted || status == OrderReady || status == OrderDispatched
}

// The seller's steps on an order.
const (
	StepAccept     = "accept"
	StepDecline    = "decline"
	StepChange     = "change"
	StepReady      = "ready"
	StepDispatched = "dispatched"
	StepDelivered  = "delivered"
	StepCancel     = "cancel" // the seller's own cancel, a withdrawn draft, or yes to the buyer's request
	StepKeep       = "keep"   // no to the buyer's request to cancel
	StepAgree      = "agree"  // the buyer's count of a short delivery agreed
)

// SellerSteps are the steps a seller may take, in the API's words.
var SellerSteps = []string{StepAccept, StepDecline, StepChange, StepReady, StepDispatched, StepDelivered, StepCancel, StepKeep, StepAgree}

// When an order's invoice is raised (trade_settings.invoice_at).
const (
	InvoiceAtAccepted  = "accepted"
	InvoiceAtDelivered = "delivered"
)

// ReceiptAfter is how long after a delivery the buyer's receipt settles by
// itself from the seller's count, when the buyer neither confirmed it nor
// said Not all came.
const ReceiptAfter = 24 * time.Hour

// OrderLine is one item of a linked order, as the seller sees it.
type OrderLine struct {
	Item      string `json:"item"`
	Unit      string `json:"unit,omitempty"`
	Quantity  int64  `json:"quantity"`
	PriceKobo int64  `json:"price_kobo"`
	// Delivered is what the seller delivered of it, once it has.
	Delivered *int64 `json:"delivered,omitempty"`
	// ItemID is the seller's own catalogue row it is, when matched:
	// Matched says how, by a link the seller confirmed ("linked") or by
	// the same name, for the seller to confirm ("name").
	ItemID  string `json:"item_id,omitempty"`
	Matched string `json:"matched,omitempty"`
}

// Order is an order from a linked business, as the seller sees it.
type Order struct {
	// ID is the storefront order's; OrderReference its reference (the
	// buyer's reference unless another order had it).
	ID             uuid.UUID `json:"id"`
	OrderReference string    `json:"order_reference"`
	// Purchase is the buyer's purchase order it mirrors (its id, in the
	// buyer's business).
	Purchase  uuid.UUID   `json:"-"`
	LinkID    uuid.UUID   `json:"link"`
	Buyer     uuid.UUID   `json:"buyer"`
	BuyerName string      `json:"buyer_name"`
	Reference string      `json:"reference"`
	Lines     []OrderLine `json:"lines"`
	TotalKobo int64       `json:"total_kobo"`
	Note      string      `json:"note,omitempty"`
	// Status is the order's as a linked order says it; OrderStatus the
	// storefront order's own.
	Status      string      `json:"status"`
	OrderStatus string      `json:"order_status"`
	Fulfilment  string      `json:"fulfilment,omitempty"`
	Change      []OrderLine `json:"change,omitempty"`
	SellerNote  string      `json:"seller_note,omitempty"`
	// Mirrored from the buyer: a cancel asked after acceptance; Not all
	// came, with what it counted of each line; its receipt settled.
	CancelAskedAt *time.Time `json:"cancel_asked_at,omitempty"`
	Short         []int64    `json:"short,omitempty"`
	ShortAt       *time.Time `json:"short_at,omitempty"`
	ReceiptAt     *time.Time `json:"receipt_at,omitempty"`
	ShortAgreedAt *time.Time `json:"short_agreed_at,omitempty"`
	InvoiceID     *uuid.UUID `json:"invoice_id,omitempty"`
	InvoiceAt     string     `json:"invoice_at"`
	AutoAccept    bool       `json:"auto_accept"`
	// PlacedBySeller: we drafted it for the buyer (proposed until it
	// confirms).
	PlacedBySeller bool       `json:"placed_by_seller"`
	DecidedBy      string     `json:"decided_by,omitempty"`
	AcceptedAt     *time.Time `json:"accepted_at,omitempty"`
	ReadyAt        *time.Time `json:"ready_at,omitempty"`
	DeliveredAt    *time.Time `json:"delivered_at,omitempty"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
}

// Told is the business a step tells, and what happened, for its people
// to be told in its own scope once the transaction commits.
type Told struct {
	Tenant    uuid.UUID
	Event     string // a seller's step, or "ordered", "auto_accepted", "proposed", "confirmed", "cancel_asked", "cancelled_by_buyer", "change_accepted", "received", "short", "cancel_agreed"
	Reference string
	From      string // the business that acted
	// Order is the seller's order, Purchase the buyer's: what the told
	// side opens.
	Order    uuid.UUID
	Purchase uuid.UUID
	// Link is the link it went through, FromTenant the business that
	// acted: the conversation both sides' cards go in (L3).
	Link       uuid.UUID
	FromTenant uuid.UUID
}

const orderCols = `s.id, s.reference, o.source_id, o.link_id, o.buyer_tenant, o.buyer_name, o.purchase_reference, o.lines, s.total_kobo, o.note,
	s.status, o.ready_at IS NOT NULL, COALESCE(s.fulfilment, ''), o.change, COALESCE(o.seller_note, ''), o.cancel_asked_at, o.short, o.short_at, o.receipt_at,
	o.short_agreed_at, o.invoice_id,
	COALESCE((SELECT t.invoice_at FROM trade_settings t WHERE t.tenant_id = o.tenant_id AND t.link_id = o.link_id), 'delivered'),
	COALESCE((SELECT t.auto_accept FROM trade_settings t WHERE t.tenant_id = o.tenant_id AND t.link_id = o.link_id), false), o.placed_by_seller,
	COALESCE(s.decided_by, ''), CASE WHEN s.status IN ('accepted', 'paid', 'dispatched', 'on_the_way', 'arrived', 'completed') THEN s.decided_at END,
	o.ready_at, o.delivered_at, s.created_at, GREATEST(o.updated_at, s.updated_at)`

const orderFrom = ` FROM link_orders o JOIN storefront_orders s ON s.id = o.order_id`

func scanOrder(r pgx.Row) (Order, error) {
	var o Order
	var ready bool
	var lines, change, short []byte
	err := r.Scan(&o.ID, &o.OrderReference, &o.Purchase, &o.LinkID, &o.Buyer, &o.BuyerName, &o.Reference, &lines, &o.TotalKobo, &o.Note,
		&o.OrderStatus, &ready, &o.Fulfilment, &change, &o.SellerNote, &o.CancelAskedAt, &short, &o.ShortAt, &o.ReceiptAt,
		&o.ShortAgreedAt, &o.InvoiceID, &o.InvoiceAt, &o.AutoAccept, &o.PlacedBySeller,
		&o.DecidedBy, &o.AcceptedAt, &o.ReadyAt, &o.DeliveredAt, &o.CreatedAt, &o.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return o, fmt.Errorf("%w: no such order", ErrNotFound)
	}
	if err != nil {
		return o, err
	}
	o.Status = TradeStatus(o.OrderStatus, ready)
	for _, f := range []struct {
		raw []byte
		to  any
	}{{lines, &o.Lines}, {change, &o.Change}, {short, &o.Short}} {
		if f.raw != nil {
			if err := json.Unmarshal(f.raw, f.to); err != nil {
				return o, err
			}
		}
	}
	return o, nil
}

// ListOrdersTx is the business in scope's orders from linked businesses,
// those still needing it first, then the newest.
func ListOrdersTx(ctx context.Context, tx pgx.Tx, open bool, limit int) ([]Order, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := tx.Query(ctx, `SELECT `+orderCols+orderFrom+`
		WHERE NOT $1 OR s.status IN ('proposed', 'received', 'changed', 'accepted', 'paid', 'dispatched', 'on_the_way', 'arrived')
		   OR (s.status = 'completed' AND o.short_at IS NOT NULL AND o.short_agreed_at IS NULL)
		ORDER BY s.status IN ('completed', 'declined', 'cancelled', 'refund_due', 'refunded', 'expired'), s.created_at DESC LIMIT $2`, open, limit)
	if err != nil {
		return nil, err
	}
	out, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (Order, error) { return scanOrder(r) })
	if out == nil {
		out = []Order{}
	}
	return out, err
}

// GetOrderTx is one of the business in scope's orders from linked
// businesses: by id, by the buyer's reference (P-7QX4KD), or by the
// order's own.
func GetOrderTx(ctx context.Context, tx pgx.Tx, ref string, lock bool) (Order, error) {
	q := `SELECT ` + orderCols + orderFrom + ` WHERE `
	var arg any
	if id, err := uuid.Parse(ref); err == nil {
		q, arg = q+`s.id = $1`, id
	} else {
		q, arg = q+`(o.purchase_reference = upper($1) OR s.reference = upper($1)) ORDER BY s.created_at DESC LIMIT 1`, strings.TrimSpace(ref)
	}
	if lock {
		q += ` FOR UPDATE OF o, s`
	}
	return scanOrder(tx.QueryRow(ctx, q, arg))
}

// Trade is how the business in scope trades with a linked business that
// buys from it: when an order's invoice is raised (at acceptance, or once
// the delivery is received), and whether its orders are accepted by
// themselves when they arrive priced.
type Trade struct {
	InvoiceAt  string `json:"invoice_at"`
	AutoAccept bool   `json:"auto_accept"`
}

// SetTradeTx sets how the business in scope trades with a link's orders.
func SetTradeTx(ctx context.Context, tx pgx.Tx, tenant, link uuid.UUID, t Trade, by string) error {
	if t.InvoiceAt != InvoiceAtAccepted && t.InvoiceAt != InvoiceAtDelivered {
		return fmt.Errorf("%w: the invoice is raised when an order is accepted or delivered", ErrInvalid)
	}
	if _, err := GetTx(ctx, tx, link); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `INSERT INTO trade_settings (tenant_id, link_id, invoice_at, auto_accept, updated_by) VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (tenant_id, link_id) DO UPDATE SET invoice_at = EXCLUDED.invoice_at, auto_accept = EXCLUDED.auto_accept,
			updated_by = EXCLUDED.updated_by, updated_at = now()`,
		tenant, link, t.InvoiceAt, t.AutoAccept, by)
	return err
}

// TradeTx is how the business in scope trades with a link's orders.
func TradeTx(ctx context.Context, tx pgx.Tx, tenant, link uuid.UUID) (Trade, error) {
	t := Trade{InvoiceAt: InvoiceAtDelivered}
	err := tx.QueryRow(ctx, `SELECT invoice_at, auto_accept FROM trade_settings WHERE tenant_id = $1 AND link_id = $2`, tenant, link).Scan(&t.InvoiceAt, &t.AutoAccept)
	if errors.Is(err, pgx.ErrNoRows) {
		err = nil
	}
	return t, err
}

// --- where a buyer's orders are delivered ---

// Vault seals and opens personal data (secrets.Vault).
type Vault interface {
	SealTx(ctx context.Context, tx pgx.Tx, t uuid.UUID, category string, value any) (map[string]any, error)
	SealUnderTx(ctx context.Context, tx pgx.Tx, t uuid.UUID, subject, category string, value any) (map[string]any, error)
	OpenTx(ctx context.Context, tx pgx.Tx, t uuid.UUID, env map[string]any) (any, error)
}

// DeliverTo is where a linked supplier delivers a business's orders: the
// address, and the number its rider calls at the door (E.164).
type DeliverTo struct {
	Address string `json:"address"`
	Phone   string `json:"phone"`
}

// DeliverToTx reads where the business in scope has its orders delivered;
// empty when it has said nothing (its suppliers' orders are then picked
// up).
func DeliverToTx(ctx context.Context, tx pgx.Tx, v Vault, tenant uuid.UUID) (DeliverTo, error) {
	var d DeliverTo
	var env map[string]any
	err := tx.QueryRow(ctx, `SELECT deliver_to FROM stock_settings WHERE tenant_id = $1`, tenant).Scan(&env)
	if errors.Is(err, pgx.ErrNoRows) || env == nil {
		return d, nil
	}
	if err != nil {
		return d, err
	}
	got, err := v.OpenTx(ctx, tx, tenant, env)
	if errors.Is(err, pii.ErrErased) {
		return d, nil // erased: as if never said
	}
	if err != nil {
		return d, err
	}
	raw, _ := json.Marshal(got)
	_ = json.Unmarshal(raw, &d)
	return d, nil
}

// SetDeliverToTx says where the business in scope has its orders
// delivered (an empty address: picked up), sealed.
func SetDeliverToTx(ctx context.Context, tx pgx.Tx, v Vault, tenant uuid.UUID, d DeliverTo, by string) error {
	d.Address = strings.Join(strings.Fields(d.Address), " ")
	if len([]rune(d.Address)) > 300 {
		return fmt.Errorf("%w: an address is up to 300 characters", ErrInvalid)
	}
	var env map[string]any
	if d.Address != "" {
		if d.Phone == "" {
			return fmt.Errorf("%w: give the number a rider calls at the door", ErrInvalid)
		}
		var err error
		if env, err = v.SealTx(ctx, tx, tenant, "deliver_to", d); err != nil {
			return err
		}
	}
	_, err := tx.Exec(ctx, `INSERT INTO stock_settings (tenant_id, deliver_to, updated_by) VALUES ($1, $2, $3)
		ON CONFLICT (tenant_id) DO UPDATE SET deliver_to = EXCLUDED.deliver_to, updated_by = EXCLUDED.updated_by, updated_at = now()`, tenant, env, by)
	return err
}

// --- the buyer's purchase order, as the link reads it ---

type purchaseLine struct {
	ItemID    string `json:"item_id"`
	Item      string `json:"item"`
	Unit      string `json:"unit,omitempty"`
	Quantity  int64  `json:"quantity"`
	CostKobo  int64  `json:"cost_kobo"`
	Received  int64  `json:"received"`
	Delivered *int64 `json:"delivered,omitempty"`
}

type purchase struct {
	ID            uuid.UUID
	Reference     string
	Link          *uuid.UUID
	Lines         []purchaseLine
	Total         int64
	Note          string
	Status        string
	SellerStatus  string
	Change        []purchaseLine
	DeliveredAt   *time.Time
	CancelAskedAt *time.Time
	ShortAt       *time.Time
	ShortAgreedAt *time.Time
}

func purchaseTx(ctx context.Context, tx pgx.Tx, id uuid.UUID) (purchase, error) {
	var p purchase
	var lines, change []byte
	err := tx.QueryRow(ctx, `SELECT id, reference, link_id, lines, total_kobo, note, status, COALESCE(seller_status, ''), change, delivered_at, cancel_asked_at,
		short_at, short_agreed_at FROM purchase_orders WHERE id = $1 FOR UPDATE`, id).Scan(&p.ID, &p.Reference, &p.Link, &lines, &p.Total, &p.Note, &p.Status,
		&p.SellerStatus, &change, &p.DeliveredAt, &p.CancelAskedAt, &p.ShortAt, &p.ShortAgreedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return p, fmt.Errorf("%w: no such purchase order", ErrNotFound)
	}
	if err != nil {
		return p, err
	}
	if change != nil {
		if err := json.Unmarshal(change, &p.Change); err != nil {
			return p, err
		}
	}
	return p, json.Unmarshal(lines, &p.Lines)
}

func linesTotal(lines []OrderLine) int64 {
	var t int64
	for _, l := range lines {
		t += l.Quantity * l.PriceKobo
	}
	return t
}

// active reports whether the business in scope's link is still active: a
// mirror goes through only then (EnterTx would abort the transaction).
func active(ctx context.Context, tx pgx.Tx, link uuid.UUID) (bool, error) {
	l, err := GetTx(ctx, tx, link)
	if errors.Is(err, ErrNotFound) {
		return false, nil
	}
	return err == nil && l.State == Active, err
}

func businessName(ctx context.Context, tx pgx.Tx, tenant uuid.UUID) (string, error) {
	var name string
	err := tx.QueryRow(ctx, `SELECT name FROM tenants WHERE id = $1`, tenant).Scan(&name)
	return name, err
}

// --- the seller's order ---

// basket is an order's lines as the storefront order's basket: its own
// item (the catalogue row stock is lowered from), or none.
func basket(lines []OrderLine) []storefront.Line {
	out := make([]storefront.Line, len(lines))
	for i, l := range lines {
		name := l.Item
		if l.Unit != "" {
			name += " (" + l.Unit + ")"
		}
		out[i] = storefront.Line{ID: l.ItemID, Name: name, Quantity: int(l.Quantity), PriceKobo: l.PriceKobo, LineKobo: l.Quantity * l.PriceKobo}
	}
	return out
}

// putLines writes an order's lines, and its basket and total from them.
func putLines(ctx context.Context, tx pgx.Tx, order uuid.UUID, lines []OrderLine) error {
	raw, _ := json.Marshal(lines)
	b, _ := json.Marshal(basket(lines))
	if _, err := tx.Exec(ctx, `UPDATE link_orders SET lines = $2, updated_at = now() WHERE order_id = $1`, order, raw); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `UPDATE storefront_orders SET basket = $2, total_kobo = $3, updated_at = now() WHERE id = $1`, order, b, linesTotal(lines))
	return err
}

// sealCustomer seals the buyer as the order's customer, in the seller's
// scope: its name, and where it has its orders delivered. Its subject is
// the delivery number's, or the buyer business's own.
func sealCustomer(ctx context.Context, tx pgx.Tx, v Vault, seller, buyer uuid.UUID, name string, d DeliverTo) (map[string]any, string, error) {
	var env map[string]any
	var err error
	if d.Phone != "" {
		env, err = v.SealTx(ctx, tx, seller, "phone", d.Phone)
	} else {
		env, err = v.SealTx(ctx, tx, seller, "business", KeyPrefix+buyer.String())
	}
	if err != nil {
		return nil, "", err
	}
	subject, _ := env["subject"].(string)
	c, err := v.SealUnderTx(ctx, tx, seller, subject, "customer", storefront.Customer{Name: name, Phone: d.Phone, Address: d.Address})
	return c, subject, err
}

func fulfilment(d DeliverTo) string {
	if d.Address != "" {
		return "delivery"
	}
	return "pickup"
}

// orderIn is a new order in the seller's business.
type orderIn struct {
	seller, link, buyer, source uuid.UUID
	buyerName, reference, note  string
	status                      string // received, or proposed
	placed                      bool
	lines                       []OrderLine
	deliver                     DeliverTo
}

// insertOrder writes a linked order into the seller's business (in its
// scope): the storefront order, under the buyer's reference unless
// another order has it, and its link_orders row. An order already written
// for the purchase order answers its id.
func insertOrder(ctx context.Context, tx pgx.Tx, v Vault, in orderIn) (uuid.UUID, error) {
	var id uuid.UUID
	err := tx.QueryRow(ctx, `SELECT order_id FROM link_orders WHERE link_id = $1 AND source_id = $2`, in.link, in.source).Scan(&id)
	if err == nil {
		return id, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return id, err
	}
	var sf *uuid.UUID
	if err := tx.QueryRow(ctx, `SELECT id FROM storefronts WHERE tenant_id = $1`, in.seller).Scan(&sf); err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return id, err
	}
	customer, subject, err := sealCustomer(ctx, tx, v, in.seller, in.buyer, in.buyerName, in.deliver)
	if err != nil {
		return id, err
	}
	_, hash := storefront.NewStatusKey() // no status page: the buyer follows its purchase order
	b, _ := json.Marshal(basket(in.lines))
	id = uuid.Must(uuid.NewV7())
	ref := in.reference
	for try := 0; ; try++ {
		tag, err := tx.Exec(ctx, `INSERT INTO storefront_orders (id, tenant_id, storefront_id, kind, reference, status_hash, status, customer, subject_id, basket,
			total_kobo, fulfilment, payment) VALUES ($1, $2, $3, 'trade', $4, $5, $6, $7, $8, $9, $10, $11, 'none')
			ON CONFLICT (reference) DO NOTHING`, id, in.seller, sf, ref, hash, in.status, customer, subject, b, linesTotal(in.lines), fulfilment(in.deliver))
		if err != nil {
			return id, err
		}
		if tag.RowsAffected() == 1 {
			break
		}
		if try == 4 {
			return id, errors.New("links: no free reference for the order")
		}
		ref = in.reference + "-" + newReference()[2:4]
	}
	raw, _ := json.Marshal(in.lines)
	_, err = tx.Exec(ctx, `INSERT INTO link_orders (order_id, tenant_id, link_id, buyer_tenant, buyer_name, source_id, purchase_reference, lines, note, placed_by_seller)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`, id, in.seller, in.link, in.buyer, in.buyerName, in.source, in.reference, raw, in.note, in.placed)
	return id, err
}

// WriteOrderTx is the worker's: a purchase order sent to a linked
// supplier, written into the supplier's business as an order. In the
// buyer's scope it takes the order off the list, reads where the buyer has
// its orders delivered and marks it received by the seller, then enters
// the seller's side. It tells no one when there is nothing to write
// (cancelled before it went, or the link no longer active: the order stays
// sent, unanswered, and says so).
func WriteOrderTx(ctx context.Context, tx pgx.Tx, v Vault, buyer, id uuid.UUID) (*Told, error) {
	if err := Undue(ctx, tx, buyer, "order", id); err != nil {
		return nil, err
	}
	p, err := purchaseTx(ctx, tx, id)
	if errors.Is(err, ErrNotFound) {
		return nil, nil
	}
	if err != nil || p.Link == nil || p.Status != "sent" || p.SellerStatus != "" {
		return nil, err
	}
	ok, err := active(ctx, tx, *p.Link)
	if err != nil {
		return nil, err
	}
	if !ok {
		_, err := tx.Exec(ctx, `UPDATE purchase_orders SET seller_note = 'The link with this supplier ended before the order reached it: cancel it, or send it again by chat.',
			updated_at = now() WHERE id = $1`, id)
		return nil, err
	}
	name, err := businessName(ctx, tx, buyer)
	if err != nil {
		return nil, err
	}
	deliver, err := DeliverToTx(ctx, tx, v, buyer)
	if err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `UPDATE purchase_orders SET seller_status = 'received', updated_at = now() WHERE id = $1`, id); err != nil {
		return nil, err
	}
	lines := make([]OrderLine, len(p.Lines))
	for i, l := range p.Lines {
		lines[i] = OrderLine{Item: l.Item, Unit: l.Unit, Quantity: l.Quantity, PriceKobo: l.CostKobo}
	}
	detail, _ := json.Marshal(map[string]any{"reference": p.Reference})
	seller, err := EnterTx(ctx, tx, *p.Link, "link.order.sent", detail, Actor{Type: "system", ID: "link-orders"})
	if err != nil {
		return nil, err
	}
	order, err := insertOrder(ctx, tx, v, orderIn{seller: seller, link: *p.Link, buyer: buyer, source: p.ID, buyerName: name, reference: p.Reference,
		note: p.Note, status: "received", lines: lines, deliver: deliver})
	if err != nil {
		return nil, err
	}
	return &Told{Tenant: seller, Event: "ordered", Reference: p.Reference, From: name, Order: order, Purchase: p.ID, Link: *p.Link, FromTenant: buyer}, nil
}

// StepIn is what a step carries: the lines a change proposes, what was
// delivered of each line, and a word for the other side.
type StepIn struct {
	Lines     []OrderLine `json:"lines,omitempty"`
	Delivered []int64     `json:"delivered,omitempty"`
	Note      string      `json:"note,omitempty"`
}

// Decisions DecideTx leaves to the storefront order's own Decide, once
// its transaction commits: accept, decline, cancel and complete move the
// order as any order moves (its stock, its rider, its run).
const (
	DecideAccept   = "accept"
	DecideDecline  = "decline"
	DecideCancel   = "cancel"
	DecideComplete = "complete"
)

// DecideTx takes a seller's step on an order from a linked business, in
// the seller's scope: what is the linked order's own (a change, what was
// delivered, ready, a word, the buyer's request kept, its count agreed) is
// written here; it answers the decision that moves the order itself
// (DecideAccept and the rest), "" when there is none. A step the order has
// already taken answers "" and changes nothing. The buyer's purchase order
// follows by the mirror.
func DecideTx(ctx context.Context, tx pgx.Tx, seller uuid.UUID, ref, step string, in StepIn, by Actor) (Order, string, error) {
	o, err := GetOrderTx(ctx, tx, ref, true)
	if err != nil {
		return o, "", err
	}
	in.Note = strings.TrimSpace(in.Note)
	if len([]rune(in.Note)) > 500 {
		return o, "", fmt.Errorf("%w: a word for them is up to 500 characters", ErrInvalid)
	}
	refuse := func() (Order, string, error) {
		return o, "", fmt.Errorf("%w: %s is %s", ErrState, o.Reference, o.Status)
	}
	note := func() error {
		if in.Note == "" {
			return nil
		}
		_, err := tx.Exec(ctx, `UPDATE link_orders SET seller_note = $2, updated_at = now() WHERE order_id = $1`, o.ID, in.Note)
		return err
	}
	decide := ""
	switch step {
	case StepAccept:
		switch {
		case live(o.Status) || o.Status == OrderDelivered:
			return o, "", nil
		case o.Status != OrderReceived:
			return refuse()
		}
		for _, l := range o.Lines {
			if l.PriceKobo < 1 {
				return o, "", fmt.Errorf("%w: %s has no price; change the order to give it yours", ErrInvalid, l.Item)
			}
		}
		decide = DecideAccept
	case StepDecline:
		switch o.Status {
		case OrderDeclined:
			return o, "", nil
		case OrderReceived, OrderChanged:
		default:
			return refuse()
		}
		if err := note(); err != nil {
			return o, "", err
		}
		decide = DecideDecline
	case StepChange:
		if o.Status != OrderReceived && o.Status != OrderChanged {
			return refuse()
		}
		if err := checkChange(o.Lines, in.Lines); err != nil {
			return o, "", err
		}
		change := make([]OrderLine, len(o.Lines))
		for i, l := range o.Lines {
			change[i] = OrderLine{Item: l.Item, Unit: l.Unit, Quantity: in.Lines[i].Quantity, PriceKobo: in.Lines[i].PriceKobo, ItemID: l.ItemID, Matched: l.Matched}
		}
		raw, _ := json.Marshal(change)
		if _, err := tx.Exec(ctx, `UPDATE link_orders SET change = $2, seller_note = NULLIF($3, ''), updated_at = now() WHERE order_id = $1`, o.ID, raw, in.Note); err != nil {
			return o, "", err
		}
		if _, err := tx.Exec(ctx, `UPDATE storefront_orders SET status = 'changed', updated_at = now() WHERE id = $1`, o.ID); err != nil {
			return o, "", err
		}
	case StepReady:
		switch {
		case o.Status == OrderReady || o.Status == OrderDispatched || o.Status == OrderDelivered:
			return o, "", nil
		case o.Status != OrderAccepted:
			return refuse()
		}
		if _, err := tx.Exec(ctx, `UPDATE link_orders SET ready_at = now(), seller_note = COALESCE(NULLIF($2, ''), seller_note), updated_at = now() WHERE order_id = $1`,
			o.ID, in.Note); err != nil {
			return o, "", err
		}
	case StepDispatched:
		switch {
		case o.Status == OrderDispatched || o.Status == OrderDelivered:
			return o, "", nil
		case o.Status != OrderAccepted && o.Status != OrderReady:
			return refuse()
		}
		if err := note(); err != nil {
			return o, "", err
		}
		if _, err := tx.Exec(ctx, `UPDATE storefront_orders SET status = 'dispatched', updated_at = now() WHERE id = $1`, o.ID); err != nil {
			return o, "", err
		}
	case StepDelivered:
		switch {
		case o.Status == OrderDelivered:
			return o, "", nil
		case !live(o.Status):
			return refuse()
		}
		got := in.Delivered
		if got == nil {
			got = make([]int64, len(o.Lines))
			for i, l := range o.Lines {
				got[i] = l.Quantity
			}
		}
		if len(got) != len(o.Lines) {
			return o, "", fmt.Errorf("%w: say what was delivered of each of the %d items", ErrInvalid, len(o.Lines))
		}
		some := false
		for i, n := range got {
			if n < 0 || n > 1_000_000 {
				return o, "", fmt.Errorf("%w: what was delivered of %s is a whole number", ErrInvalid, o.Lines[i].Item)
			}
			o.Lines[i].Delivered = &got[i]
			some = some || n > 0
		}
		if !some {
			return o, "", fmt.Errorf("%w: nothing delivered is a cancel, not a delivery", ErrInvalid)
		}
		raw, _ := json.Marshal(o.Lines)
		if _, err := tx.Exec(ctx, `UPDATE link_orders SET lines = $2, delivered_at = now(), seller_note = COALESCE(NULLIF($3, ''), seller_note), updated_at = now()
			WHERE order_id = $1`, o.ID, raw, in.Note); err != nil {
			return o, "", err
		}
		decide = DecideComplete
	case StepCancel:
		switch {
		case o.Status == OrderCancelled:
			return o, "", nil
		case !live(o.Status) && o.Status != OrderProposed:
			return refuse()
		}
		if err := note(); err != nil {
			return o, "", err
		}
		decide = DecideCancel
	case StepKeep:
		if o.CancelAskedAt == nil || !live(o.Status) {
			return o, "", fmt.Errorf("%w: %s was not asked to be cancelled", ErrState, o.Reference)
		}
		if _, err := tx.Exec(ctx, `UPDATE link_orders SET cancel_asked_at = NULL, seller_note = NULLIF($2, ''), updated_at = now() WHERE order_id = $1`,
			o.ID, in.Note); err != nil {
			return o, "", err
		}
	case StepAgree:
		if o.Status != OrderDelivered || o.ShortAt == nil {
			return o, "", fmt.Errorf("%w: %s has no count of a short delivery to agree", ErrState, o.Reference)
		}
		if o.ShortAgreedAt != nil {
			return o, "", nil
		}
		if _, err := tx.Exec(ctx, `UPDATE link_orders SET short_agreed_at = now(), seller_note = 'Agreed to your count of what came.', updated_at = now()
			WHERE order_id = $1`, o.ID); err != nil {
			return o, "", err
		}
		if o.InvoiceAt == InvoiceAtDelivered && o.InvoiceID == nil {
			if err := dueTx(ctx, tx, seller, "invoice", o.ID, 0); err != nil {
				return o, "", err
			}
		}
	default:
		return o, "", fmt.Errorf("%w: a step is one of %s", ErrInvalid, strings.Join(SellerSteps, ", "))
	}
	if err := orderAudit(ctx, tx, seller, "trade_order."+step, o.Reference, o.ID, by); err != nil {
		return o, "", err
	}
	o, err = GetOrderTx(ctx, tx, o.ID.String(), false)
	return o, decide, err
}

func orderAudit(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, action, ref string, id uuid.UUID, by Actor) error {
	raw, _ := json.Marshal(map[string]any{"order_id": id, "by": by.Type + ":" + by.ID})
	_, err := tx.Exec(ctx, `SELECT taskiem_audit_append($1, 'system', 'storefront', $2, $3, $4)`, tenant, action, ref, raw)
	return err
}

// --- the mirror ---

// MirrorOrderTx is the worker's: the seller's order, as it now is,
// mirrored to the buyer's purchase order. In the seller's scope it takes
// the order off the list (and puts an invoice raised at acceptance on it),
// then enters the buyer's side and writes the seller's status, word,
// change, what was delivered and the agreed prices; it answers who to
// tell of what changed, nil when nothing the buyer sees did.
func MirrorOrderTx(ctx context.Context, tx pgx.Tx, seller, id uuid.UUID) (*Told, error) {
	if err := Undue(ctx, tx, seller, "mirror", id); err != nil {
		return nil, err
	}
	o, err := GetOrderTx(ctx, tx, id.String(), false)
	if errors.Is(err, ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if (live(o.Status) || o.Status == OrderDelivered) && o.InvoiceAt == InvoiceAtAccepted && o.InvoiceID == nil {
		if err := dueTx(ctx, tx, seller, "invoice", o.ID, 0); err != nil {
			return nil, err
		}
	}
	ok, err := active(ctx, tx, o.LinkID)
	if err != nil || !ok {
		return nil, err
	}
	name, err := businessName(ctx, tx, seller)
	if err != nil {
		return nil, err
	}
	detail, _ := json.Marshal(map[string]any{"reference": o.Reference, "status": o.Status})
	buyer, err := EnterTx(ctx, tx, o.LinkID, "link.order.mirror", detail, Actor{Type: "system", ID: "link-orders"})
	if err != nil {
		return nil, err
	}
	p, err := purchaseTx(ctx, tx, o.Purchase)
	if errors.Is(err, ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	was := p.SellerStatus
	now := o.Status
	// The lines: the agreed prices once accepted, and what came once
	// delivered (all of each, when a rider delivered it).
	accepted := live(now) || now == OrderDelivered
	var total int64
	for i := range p.Lines {
		if i >= len(o.Lines) {
			break
		}
		if accepted {
			p.Lines[i].CostKobo = o.Lines[i].PriceKobo
		}
		if now == OrderDelivered {
			n := o.Lines[i].Quantity
			if o.Lines[i].Delivered != nil {
				n = *o.Lines[i].Delivered
			}
			p.Lines[i].Delivered = &n
		}
	}
	for _, l := range p.Lines {
		total += l.Quantity * l.CostKobo
	}
	var change []byte
	if now == OrderChanged && len(o.Change) == len(p.Lines) {
		pl := make([]purchaseLine, len(p.Lines))
		for i, l := range p.Lines {
			pl[i] = purchaseLine{ItemID: l.ItemID, Item: l.Item, Unit: l.Unit, Quantity: o.Change[i].Quantity, CostKobo: o.Change[i].PriceKobo}
		}
		change, _ = json.Marshal(pl)
	}
	raw, _ := json.Marshal(p.Lines)
	counter := ""
	if now == OrderChanged && was != OrderChanged {
		counter = changeWords(o.Lines, o.Change, o.SellerNote)
	}
	status := p.Status
	switch {
	case accepted && p.Status == "sent":
		status = "confirmed"
	case (now == OrderDeclined || now == OrderCancelled) && (p.Status == "sent" || p.Status == "confirmed" || p.Status == "drafted"):
		status = "cancelled"
	}
	keepAsk := o.CancelAskedAt != nil && live(now)
	if _, err := tx.Exec(ctx, `UPDATE purchase_orders SET seller_status = $2, seller_note = NULLIF($3, ''), change = $4, lines = $5,
		total_kobo = CASE WHEN $6 THEN $7 ELSE total_kobo END, status = $8,
		confirmed_at = CASE WHEN $8 = 'confirmed' THEN COALESCE(confirmed_at, now()) ELSE confirmed_at END,
		confirmed_how = CASE WHEN $8 = 'confirmed' THEN COALESCE(confirmed_how, 'supplier') ELSE confirmed_how END,
		cancelled_at = CASE WHEN $8 = 'cancelled' THEN COALESCE(cancelled_at, now()) ELSE cancelled_at END,
		delivered_at = CASE WHEN $9 THEN COALESCE(delivered_at, now()) ELSE delivered_at END,
		cancel_asked_at = CASE WHEN $10 THEN cancel_asked_at END,
		short_agreed_at = $11,
		counter = CASE WHEN $12 <> '' THEN $12 ELSE counter END, counter_at = CASE WHEN $12 <> '' THEN now() ELSE counter_at END,
		updated_at = now() WHERE id = $1`,
		p.ID, now, o.SellerNote, change, raw, accepted, total, status, now == OrderDelivered, keepAsk, o.ShortAgreedAt, counter); err != nil {
		return nil, err
	}
	if now == OrderDelivered && p.DeliveredAt == nil {
		if err := dueTx(ctx, tx, buyer, "receipt", p.ID, ReceiptAfter); err != nil {
			return nil, err
		}
	}
	event := ""
	switch {
	case now != was:
		switch now {
		case OrderChanged:
			event = StepChange
		case OrderAccepted:
			if was != OrderChanged && was != OrderProposed {
				event = StepAccept
			}
		case OrderReady:
			event = StepReady
		case OrderDispatched:
			event = StepDispatched
		case OrderDelivered:
			event = StepDelivered
		case OrderDeclined:
			event = StepDecline
		case OrderCancelled:
			switch {
			case p.Status == "cancelled": // the buyer cancelled it
			case p.CancelAskedAt != nil:
				event = "cancel_agreed"
			default:
				event = StepCancel
			}
		}
	case p.CancelAskedAt != nil && !keepAsk && live(now):
		event = StepKeep
	case o.ShortAgreedAt != nil && p.ShortAgreedAt == nil:
		event = StepAgree
	}
	if event == "" {
		return nil, nil
	}
	return &Told{Tenant: buyer, Event: event, Reference: o.Reference, From: name, Order: o.ID, Purchase: p.ID, Link: o.LinkID, FromTenant: seller}, nil
}

// checkChange checks the lines a seller proposes: one per line of the
// order, in its order, each a quantity from 0 (cannot supply) and a price,
// with something left to supply.
func checkChange(cur, next []OrderLine) error {
	if len(next) != len(cur) {
		return fmt.Errorf("%w: a change gives each of the %d items a quantity and a price", ErrInvalid, len(cur))
	}
	var total int64
	some := false
	for i, l := range next {
		if l.Quantity < 0 || l.Quantity > 1_000_000 {
			return fmt.Errorf("%w: how many %s is a whole number from 0", ErrInvalid, cur[i].Item)
		}
		if l.PriceKobo < 0 || l.PriceKobo > 10_000_000_000 || (l.Quantity > 0 && l.PriceKobo < 1) {
			return fmt.Errorf("%w: give %s a price", ErrInvalid, cur[i].Item)
		}
		total += l.Quantity * l.PriceKobo
		some = some || l.Quantity > 0
	}
	if !some {
		return fmt.Errorf("%w: a change that supplies nothing is a decline", ErrInvalid)
	}
	if total > 10_000_000_000 {
		return fmt.Errorf("%w: an order is at most ₦100,000,000", ErrInvalid)
	}
	return nil
}

// changeWords says a change in words, for the purchase order's counter
// (what an unlinked supplier's Change says too).
func changeWords(cur, next []OrderLine, note string) string {
	var parts []string
	for i, l := range next {
		if i >= len(cur) {
			break
		}
		was := cur[i]
		switch {
		case l.Quantity == 0:
			parts = append(parts, "no "+was.Item)
		case l.Quantity != was.Quantity && l.PriceKobo != was.PriceKobo:
			parts = append(parts, fmt.Sprintf("%d %s at %s", l.Quantity, was.Item, naira(l.PriceKobo)))
		case l.Quantity != was.Quantity:
			parts = append(parts, fmt.Sprintf("%d %s", l.Quantity, was.Item))
		case l.PriceKobo != was.PriceKobo:
			parts = append(parts, fmt.Sprintf("%s at %s", was.Item, naira(l.PriceKobo)))
		}
	}
	w := strings.Join(parts, ", ")
	if w == "" {
		w = "the same items"
	}
	if note != "" {
		w += ". " + note
	}
	if len([]rune(w)) > 1000 {
		w = string([]rune(w)[:1000])
	}
	return w
}

func naira(kobo int64) string {
	n := kobo / 100
	s := fmt.Sprint(n)
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	if k := kobo % 100; k != 0 {
		return fmt.Sprintf("₦%s.%02d", s, k)
	}
	return "₦" + s
}

// dueTx puts linked-order work on the list of the business in scope.
func dueTx(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, kind string, id uuid.UUID, after time.Duration) error {
	_, err := tx.Exec(ctx, `INSERT INTO link_orders_due (tenant_id, kind, object_id, due_at) VALUES ($1, $2, $3, now() + $4 * interval '1 second')
		ON CONFLICT (tenant_id, kind, object_id) DO UPDATE SET due_at = EXCLUDED.due_at, tries = 0`, tenant, kind, id, int64(after/time.Second))
	return err
}

// Undue takes linked-order work off the list of the business in scope.
func Undue(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, kind string, id uuid.UUID) error {
	_, err := tx.Exec(ctx, `DELETE FROM link_orders_due WHERE tenant_id = $1 AND kind = $2 AND object_id = $3`, tenant, kind, id)
	return err
}

// Due is linked-order work the worker claimed.
type Due struct {
	Tenant uuid.UUID
	Kind   string
	ID     uuid.UUID
}

// ClaimOrders claims linked-order work due, across businesses.
func ClaimOrders(ctx context.Context, q interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
}, limit int) ([]Due, error) {
	rows, err := q.Query(ctx, `SELECT tenant_id, kind, object_id FROM taskiem_link_orders_claim($1)`, limit)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (Due, error) {
		var d Due
		return d, r.Scan(&d.Tenant, &d.Kind, &d.ID)
	})
}

// --- the buyer's steps ---

// Buyer's answers to a cancel.
const (
	CancelDone  = "cancelled" // cancelled on both sides
	CancelAsked = "asked"     // accepted already: the seller is asked
)

// orderOf is the seller's order for a purchase order, in the seller's
// scope, locked.
func orderOf(ctx context.Context, tx pgx.Tx, link, source uuid.UUID) (Order, error) {
	return scanOrder(tx.QueryRow(ctx, `SELECT `+orderCols+orderFrom+` WHERE o.link_id = $1 AND o.source_id = $2 FOR UPDATE OF o, s`, link, source))
}

// CancelOrderTx cancels a purchase order sent to a linked supplier, in the
// buyer's scope: before the seller accepts it (or while it is a draft the
// seller proposed), on both sides; after, it asks the seller, who answers
// (StepCancel or StepKeep). Once anything is delivered or received,
// neither side cancels.
func CancelOrderTx(ctx context.Context, tx pgx.Tx, buyer, id uuid.UUID, by Actor) (string, *Told, error) {
	p, err := purchaseTx(ctx, tx, id)
	if err != nil {
		return "", nil, err
	}
	if p.Link == nil {
		return "", nil, fmt.Errorf("%w: %s did not go to a linked business", ErrInvalid, p.Reference)
	}
	if p.Status == "cancelled" {
		return CancelDone, nil, nil
	}
	proposed := p.Status == "drafted" && p.SellerStatus == OrderProposed
	if p.Status != "sent" && p.Status != "confirmed" && !proposed {
		return "", nil, fmt.Errorf("%w: %s has goods received on it", ErrState, p.Reference)
	}
	how := CancelDone
	switch p.SellerStatus {
	case "", OrderReceived, OrderChanged, OrderProposed:
		if _, err := tx.Exec(ctx, `UPDATE purchase_orders SET status = 'cancelled', cancelled_at = now(), seller_status = CASE WHEN seller_status IS NULL THEN NULL ELSE 'cancelled' END,
			change = NULL, updated_at = now() WHERE id = $1`, id); err != nil {
			return "", nil, err
		}
		if err := Undue(ctx, tx, buyer, "order", id); err != nil {
			return "", nil, err
		}
	case OrderAccepted, OrderReady, OrderDispatched:
		if p.CancelAskedAt != nil {
			return CancelAsked, nil, nil
		}
		how = CancelAsked
		if _, err := tx.Exec(ctx, `UPDATE purchase_orders SET cancel_asked_at = now(), updated_at = now() WHERE id = $1`, id); err != nil {
			return "", nil, err
		}
	default:
		return "", nil, fmt.Errorf("%w: %s was delivered", ErrState, p.Reference)
	}
	words := map[string]string{CancelDone: "cancelled", CancelAsked: "cancel_asked"}[how]
	if err := stockAuditTx(ctx, tx, buyer, "purchase."+words, p.Reference, id, by); err != nil {
		return "", nil, err
	}
	if p.SellerStatus == "" {
		return how, nil, nil // not yet in the seller's business: the worker finds it cancelled
	}
	ok, err := active(ctx, tx, *p.Link)
	if err != nil || !ok {
		return how, nil, err
	}
	name, err := businessName(ctx, tx, buyer)
	if err != nil {
		return "", nil, err
	}
	detail, _ := json.Marshal(map[string]any{"reference": p.Reference})
	seller, err := EnterTx(ctx, tx, *p.Link, "link.order."+words, detail, by)
	if err != nil {
		return "", nil, err
	}
	o, err := orderOf(ctx, tx, *p.Link, p.ID)
	if err != nil {
		return "", nil, err
	}
	if how == CancelDone {
		if o.Status != OrderReceived && o.Status != OrderChanged && o.Status != OrderProposed && o.Status != OrderCancelled {
			// Accepted a moment ago: the whole cancel is undone, and asked again.
			return "", nil, fmt.Errorf("%w: %s accepted %s a moment ago; ask again to cancel it", ErrState, name, p.Reference)
		}
		if _, err := tx.Exec(ctx, `UPDATE storefront_orders SET status = 'cancelled', decided_by = COALESCE(decided_by, $2), decided_at = COALESCE(decided_at, now()),
			updated_at = now() WHERE id = $1 AND status IN ('received', 'changed', 'proposed')`, o.ID, "link:"+name); err != nil {
			return "", nil, err
		}
		if _, err := tx.Exec(ctx, `UPDATE link_orders SET change = NULL, updated_at = now() WHERE order_id = $1`, o.ID); err != nil {
			return "", nil, err
		}
	} else if _, err := tx.Exec(ctx, `UPDATE link_orders SET cancel_asked_at = now(), updated_at = now() WHERE order_id = $1`, o.ID); err != nil {
		return "", nil, err
	}
	event := map[string]string{CancelDone: "cancelled_by_buyer", CancelAsked: "cancel_asked"}[how]
	return how, &Told{Tenant: seller, Event: event, Reference: p.Reference, From: name, Order: o.ID, Purchase: p.ID, Link: *p.Link, FromTenant: buyer}, nil
}

// AcceptChangeTx takes the seller's change on a purchase order, in the
// buyer's scope: its lines become the seller's (quantities and prices),
// and the order is accepted on both sides, as the seller proposed it.
// The seller's stock is lowered once it commits (the caller's).
func AcceptChangeTx(ctx context.Context, tx pgx.Tx, buyer, id uuid.UUID, by Actor) (*Told, error) {
	p, err := purchaseTx(ctx, tx, id)
	if err != nil {
		return nil, err
	}
	if p.Link == nil || p.SellerStatus != OrderChanged || p.Change == nil {
		return nil, fmt.Errorf("%w: %s has no change to accept", ErrState, p.Reference)
	}
	if p.Status != "sent" && p.Status != "confirmed" {
		return nil, fmt.Errorf("%w: %s is %s", ErrState, p.Reference, p.Status)
	}
	var lines []purchaseLine
	var total int64
	for i, l := range p.Lines {
		if i >= len(p.Change) || p.Change[i].Quantity == 0 {
			continue
		}
		l.Quantity, l.CostKobo = p.Change[i].Quantity, p.Change[i].CostKobo
		lines = append(lines, l)
		total += l.Quantity * l.CostKobo
	}
	raw, _ := json.Marshal(lines)
	if _, err := tx.Exec(ctx, `UPDATE purchase_orders SET lines = $2, total_kobo = $3, status = 'confirmed', confirmed_at = now(), confirmed_how = 'supplier',
		seller_status = 'accepted', change = NULL, updated_at = now() WHERE id = $1`, id, raw, total); err != nil {
		return nil, err
	}
	if err := stockAuditTx(ctx, tx, buyer, "purchase.change_accepted", p.Reference, id, by); err != nil {
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
	seller, err := EnterTx(ctx, tx, *p.Link, "link.order.change_accepted", detail, by)
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
	var kept []OrderLine
	for _, l := range o.Change {
		if l.Quantity > 0 {
			kept = append(kept, l)
		}
	}
	if err := putLines(ctx, tx, o.ID, kept); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `UPDATE link_orders SET change = NULL, updated_at = now() WHERE order_id = $1`, o.ID); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `UPDATE storefront_orders SET status = 'accepted', decided_by = $2, decided_at = now(), updated_at = now() WHERE id = $1`,
		o.ID, "link:"+name); err != nil {
		return nil, err
	}
	return &Told{Tenant: seller, Event: "change_accepted", Reference: p.Reference, From: name, Order: o.ID, Purchase: p.ID, Link: *p.Link, FromTenant: buyer}, nil
}

// Receipt is the buyer's word on a delivery: all came (Short nil), or Not
// all came, with what came of each line.
type Receipt struct {
	Short []int64
}

// ToReceive is what a delivered purchase order still has to receive, by
// item, from the seller's count: what the buyer's tap or the day's end
// records (stock.ReceiveTx). Short counts instead give what the buyer
// counted, less what it already recorded.
func ToReceive(ctx context.Context, tx pgx.Tx, id uuid.UUID, short []int64) (map[string]int64, string, error) {
	p, err := purchaseTx(ctx, tx, id)
	if err != nil {
		return nil, "", err
	}
	if p.Link == nil || p.SellerStatus != OrderDelivered {
		return nil, p.Reference, fmt.Errorf("%w: %s is not delivered yet", ErrState, p.Reference)
	}
	if p.ShortAt != nil {
		return nil, p.Reference, fmt.Errorf("%w: you said what came of %s already", ErrState, p.Reference)
	}
	if short != nil && len(short) != len(p.Lines) {
		return nil, p.Reference, fmt.Errorf("%w: say what came of each of the %d items", ErrInvalid, len(p.Lines))
	}
	got := map[string]int64{}
	for i, l := range p.Lines {
		n := int64(0)
		if l.Delivered != nil {
			n = *l.Delivered
		}
		if short != nil {
			if short[i] < 0 || short[i] > 1_000_000 {
				return nil, p.Reference, fmt.Errorf("%w: what came of %s is a whole number", ErrInvalid, l.Item)
			}
			n = short[i]
		}
		if n > l.Received {
			got[l.ItemID] = n - l.Received
		}
	}
	return got, p.Reference, nil
}

// ReceiptTx settles the buyer's receipt of a delivery, in the buyer's
// scope, after what came was recorded (stock.ReceiveTx, the same
// transaction): the day's wait taken off, Not all came kept, and the
// seller told through the door, where an invoice raised on delivery comes
// due (what came when it was all, or once the seller agrees the count).
func ReceiptTx(ctx context.Context, tx pgx.Tx, buyer, id uuid.UUID, r Receipt, by Actor) (*Told, error) {
	p, err := purchaseTx(ctx, tx, id)
	if err != nil {
		return nil, err
	}
	if p.Link == nil || p.SellerStatus != OrderDelivered || p.ShortAt != nil {
		return nil, fmt.Errorf("%w: %s has no delivery to settle", ErrState, p.Reference)
	}
	// The day's wait, put on the list at delivery, is what is settled:
	// gone, the receipt was settled already.
	tag, err := tx.Exec(ctx, `DELETE FROM link_orders_due WHERE tenant_id = $1 AND kind = 'receipt' AND object_id = $2`, buyer, id)
	if err != nil {
		return nil, err
	}
	if tag.RowsAffected() == 0 {
		return nil, fmt.Errorf("%w: you said what came of %s already", ErrState, p.Reference)
	}
	event := "received"
	if r.Short != nil {
		event = "short"
		if _, err := tx.Exec(ctx, `UPDATE purchase_orders SET short_at = now(), updated_at = now() WHERE id = $1`, id); err != nil {
			return nil, err
		}
	}
	ok, err := active(ctx, tx, *p.Link)
	if err != nil || !ok {
		return nil, err
	}
	name, err := businessName(ctx, tx, buyer)
	if err != nil {
		return nil, err
	}
	detail, _ := json.Marshal(map[string]any{"reference": p.Reference})
	seller, err := EnterTx(ctx, tx, *p.Link, "link.order."+event, detail, by)
	if err != nil {
		return nil, err
	}
	o, err := orderOf(ctx, tx, *p.Link, p.ID)
	if err != nil {
		return nil, err
	}
	if r.Short != nil {
		raw, _ := json.Marshal(r.Short)
		if _, err := tx.Exec(ctx, `UPDATE link_orders SET short = $2, short_at = now(), updated_at = now() WHERE order_id = $1`, o.ID, raw); err != nil {
			return nil, err
		}
	} else {
		if _, err := tx.Exec(ctx, `UPDATE link_orders SET receipt_at = now(), updated_at = now() WHERE order_id = $1`, o.ID); err != nil {
			return nil, err
		}
		if o.InvoiceAt == InvoiceAtDelivered && o.InvoiceID == nil {
			if err := dueTx(ctx, tx, seller, "invoice", o.ID, 0); err != nil {
				return nil, err
			}
		}
	}
	return &Told{Tenant: seller, Event: event, Reference: p.Reference, From: name, Order: o.ID, Purchase: p.ID, Link: *p.Link, FromTenant: buyer}, nil
}

// InvoiceLines is what an order's invoice charges: what was agreed when it
// is raised at acceptance; what came when raised on delivery (the
// seller's count, or the buyer's once agreed).
func (o Order) InvoiceLines() []OrderLine {
	var out []OrderLine
	for i, l := range o.Lines {
		n := l.Quantity
		if o.InvoiceAt == InvoiceAtDelivered {
			if l.Delivered != nil {
				n = *l.Delivered
			}
			if o.ShortAgreedAt != nil && i < len(o.Short) {
				n = o.Short[i]
			}
		}
		if n > 0 {
			out = append(out, OrderLine{Item: l.Item, Unit: l.Unit, Quantity: n, PriceKobo: l.PriceKobo})
		}
	}
	return out
}

// ShortKobo is what a short delivery the seller agreed takes off an order
// already invoiced at acceptance: the agreed price of what did not come.
func (o Order) ShortKobo() int64 {
	var t int64
	for i, l := range o.Lines {
		if i < len(o.Short) && o.Short[i] < l.Quantity {
			t += (l.Quantity - o.Short[i]) * l.PriceKobo
		}
	}
	return t
}

// SetInvoiceTx records the invoice raised for an order, in the seller's
// scope, and takes the work off the list.
func SetInvoiceTx(ctx context.Context, tx pgx.Tx, seller, order, invoice uuid.UUID) error {
	if _, err := tx.Exec(ctx, `UPDATE link_orders SET invoice_id = $2, updated_at = now() WHERE order_id = $1 AND invoice_id IS NULL`, order, invoice); err != nil {
		return err
	}
	return Undue(ctx, tx, seller, "invoice", order)
}

// NoteInvoiceTx keeps an invoice raised for an order before it is sent,
// so a retry sends the same one.
func NoteInvoiceTx(ctx context.Context, tx pgx.Tx, order, invoice uuid.UUID) error {
	_, err := tx.Exec(ctx, `UPDATE link_orders SET invoice_id = $2, updated_at = now() WHERE order_id = $1 AND invoice_id IS NULL`, order, invoice)
	return err
}

func stockAuditTx(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, action, ref string, id uuid.UUID, by Actor) error {
	raw, _ := json.Marshal(map[string]any{"purchase_id": id, "by": by.Type + ":" + by.ID})
	_, err := tx.Exec(ctx, `SELECT taskiem_audit_append($1, 'system', 'stock', $2, $3, $4)`, tenant, action, ref, raw)
	return err
}

// --- the seller's own items ---

// SellerItem is one of the seller's catalogue items, as matching reads it.
type SellerItem struct {
	ID        string
	Name      string
	Unit      string
	PriceKobo *int64
}

// nameKey is an item's name as a link remembers it: lowered, its spaces
// collapsed.
func nameKey(s string) string {
	k := strings.ToLower(strings.Join(strings.Fields(s), " "))
	if r := []rune(k); len(r) > 120 {
		k = string(r[:120])
	}
	return k
}

// MatchItemsTx matches a new order's lines to the seller's own items, in
// the seller's scope (§17): an item the seller said a name is, for this
// link, else its item of the same name, for the seller to confirm. A
// matched line takes the seller's price, when its item has one. It
// reports whether anything changed.
func MatchItemsTx(ctx context.Context, tx pgx.Tx, ref string, items []SellerItem) (Order, bool, error) {
	o, err := GetOrderTx(ctx, tx, ref, true)
	if err != nil || o.Status != OrderReceived {
		return o, false, err
	}
	byID, byName := map[string]SellerItem{}, map[string]SellerItem{}
	for _, it := range items {
		byID[it.ID] = it
		if _, twice := byName[nameKey(it.Name)]; twice {
			byName[nameKey(it.Name)] = SellerItem{} // two of that name: none is the one
			continue
		}
		byName[nameKey(it.Name)] = it
	}
	rows, err := tx.Query(ctx, `SELECT name_key, item_id FROM trade_item_links WHERE link_id = $1`, o.LinkID)
	if err != nil {
		return o, false, err
	}
	linked := map[string]string{}
	for rows.Next() {
		var k, id string
		if err := rows.Scan(&k, &id); err != nil {
			rows.Close()
			return o, false, err
		}
		linked[k] = id
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return o, false, err
	}
	changed := false
	for i, l := range o.Lines {
		if l.ItemID != "" {
			continue
		}
		it, how := SellerItem{}, ""
		if id, ok := linked[nameKey(l.Item)]; ok && byID[id].ID != "" {
			it, how = byID[id], "linked"
		} else if x := byName[nameKey(l.Item)]; x.ID != "" {
			it, how = x, "name"
		}
		if it.ID == "" {
			continue
		}
		o.Lines[i].ItemID, o.Lines[i].Matched = it.ID, how
		if it.PriceKobo != nil && *it.PriceKobo > 0 {
			o.Lines[i].PriceKobo = *it.PriceKobo
		}
		changed = true
	}
	if !changed {
		return o, false, nil
	}
	o.TotalKobo = linesTotal(o.Lines)
	return o, true, putLines(ctx, tx, o.ID, o.Lines)
}

// SetLineItemTx says which of the seller's own items a line of a new
// order is, in the seller's scope, and remembers it for the link's next
// orders. The line takes the item's price when it has one.
func SetLineItemTx(ctx context.Context, tx pgx.Tx, ref string, line int, it SellerItem, by string) (Order, error) {
	o, err := GetOrderTx(ctx, tx, ref, true)
	if err != nil {
		return o, err
	}
	if o.Status != OrderReceived {
		return o, fmt.Errorf("%w: %s is %s; its items are matched while it is new", ErrState, o.Reference, o.Status)
	}
	if line < 0 || line >= len(o.Lines) || it.ID == "" || strings.TrimSpace(it.Name) == "" {
		return o, fmt.Errorf("%w: no such line or item", ErrInvalid)
	}
	l := &o.Lines[line]
	l.ItemID, l.Matched = it.ID, "linked"
	if it.PriceKobo != nil && *it.PriceKobo > 0 {
		l.PriceKobo = *it.PriceKobo
	}
	name := it.Name
	if r := []rune(name); len(r) > 120 {
		name = string(r[:120])
	}
	if _, err := tx.Exec(ctx, `INSERT INTO trade_item_links (tenant_id, link_id, name_key, item_id, item, updated_by)
		SELECT tenant_id, link_id, $2, $3, $4, $5 FROM link_orders WHERE order_id = $1
		ON CONFLICT (tenant_id, link_id, name_key) DO UPDATE SET item_id = EXCLUDED.item_id, item = EXCLUDED.item, updated_by = EXCLUDED.updated_by, updated_at = now()`,
		o.ID, nameKey(l.Item), it.ID, name, by); err != nil {
		return o, err
	}
	o.TotalKobo = linesTotal(o.Lines)
	return o, putLines(ctx, tx, o.ID, o.Lines)
}

// --- orders the seller places for the buyer ---

const refChars = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"

// newReference is a purchase order reference, P-7QX4KD.
func newReference() string {
	id := uuid.New()
	b := make([]byte, 6)
	for i := range b {
		b[i] = refChars[int(id[i])%len(refChars)]
	}
	return "P-" + string(b)
}

// PlaceOrderTx drafts an order for a linked business that buys from us
// (a sales rep's phone order, §7), in the seller's scope: it is proposed
// here, and mirrors to the buyer as a purchase order to us awaiting its
// Confirm, going no further until then. The buyer must have marked us as
// its supplier (its supplier record names the link).
func PlaceOrderTx(ctx context.Context, tx pgx.Tx, v Vault, seller, link uuid.UUID, lines []OrderLine, note string, by Actor) (Order, *Told, error) {
	l, err := GetTx(ctx, tx, link)
	if err != nil {
		return Order{}, nil, err
	}
	if l.State != Active || l.Role == RoleSupplier {
		return Order{}, nil, fmt.Errorf("%w: %s does not buy from you on Taskiem", ErrState, l.Name)
	}
	note = strings.TrimSpace(note)
	if len([]rune(note)) > 500 {
		return Order{}, nil, fmt.Errorf("%w: a note is up to 500 characters", ErrInvalid)
	}
	if len(lines) == 0 || len(lines) > 50 {
		return Order{}, nil, fmt.Errorf("%w: an order has 1 to 50 items", ErrInvalid)
	}
	for i := range lines {
		x := &lines[i]
		x.Item = strings.Join(strings.Fields(x.Item), " ")
		x.Delivered = nil
		if x.Item == "" || len([]rune(x.Item)) > 120 {
			return Order{}, nil, fmt.Errorf("%w: each line names an item", ErrInvalid)
		}
		if x.Quantity < 1 || x.Quantity > 1_000_000 {
			return Order{}, nil, fmt.Errorf("%w: how many %s is a whole number from 1", ErrInvalid, x.Item)
		}
		if x.PriceKobo < 1 || x.PriceKobo > 10_000_000_000 {
			return Order{}, nil, fmt.Errorf("%w: give %s a price", ErrInvalid, x.Item)
		}
	}
	total := linesTotal(lines)
	if total > 10_000_000_000 {
		return Order{}, nil, fmt.Errorf("%w: an order is at most ₦100,000,000", ErrInvalid)
	}
	name, err := businessName(ctx, tx, seller)
	if err != nil {
		return Order{}, nil, err
	}
	po, ref := uuid.Must(uuid.NewV7()), newReference()
	id, err := insertOrder(ctx, tx, v, orderIn{seller: seller, link: link, buyer: l.Business, source: po, buyerName: l.Name, reference: ref,
		note: note, status: OrderProposed, placed: true, lines: lines})
	if err != nil {
		return Order{}, nil, err
	}
	if _, err := tx.Exec(ctx, `UPDATE storefront_orders SET decided_by = $2 WHERE id = $1`, id, by.Type+":"+by.ID); err != nil {
		return Order{}, nil, err
	}
	o, err := GetOrderTx(ctx, tx, id.String(), false)
	if err != nil {
		return o, nil, err
	}
	detail, _ := json.Marshal(map[string]any{"reference": ref})
	buyer, err := EnterTx(ctx, tx, link, "link.order.proposed", detail, by)
	if err != nil {
		return o, nil, err
	}
	var supplier uuid.UUID
	err = tx.QueryRow(ctx, `SELECT id FROM suppliers WHERE link_id = $1 AND retired_at IS NULL`, link).Scan(&supplier)
	if errors.Is(err, pgx.ErrNoRows) {
		return o, nil, fmt.Errorf("%w: %s has not marked you as its supplier on Taskiem yet; they do it under Suppliers, On Taskiem", ErrState, l.Name)
	}
	if err != nil {
		return o, nil, err
	}
	pl := make([]purchaseLine, len(lines))
	for i, x := range lines {
		pl[i] = purchaseLine{Item: x.Item, Unit: x.Unit, Quantity: x.Quantity, CostKobo: x.PriceKobo}
	}
	rawPL, _ := json.Marshal(pl)
	if _, err := tx.Exec(ctx, `INSERT INTO purchase_orders (id, tenant_id, reference, supplier_id, lines, total_kobo, note, drafted_by, key, link_id, seller_status)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, 'proposed')`,
		po, buyer, ref, supplier, rawPL, total, note, "link:"+name, "link-proposed:"+id.String(), link); err != nil {
		return o, nil, err
	}
	return o, &Told{Tenant: buyer, Event: "proposed", Reference: ref, From: name, Order: id, Purchase: po, Link: link, FromTenant: seller}, nil
}

// BuyerItem is one of the buyer's own items a proposed order's line is.
type BuyerItem struct {
	ID   string
	Name string
	Unit string
}

// ConfirmProposedTx confirms an order a linked supplier drafted for us, in
// the buyer's scope: each line is said to be one of our own items (what
// comes is received into it), and the order is accepted on both sides,
// as the supplier proposed it, with where we have orders delivered. The
// seller's stock is lowered once it commits (the caller's).
func ConfirmProposedTx(ctx context.Context, tx pgx.Tx, v Vault, buyer, id uuid.UUID, items []BuyerItem, approver string, by Actor) (*Told, error) {
	p, err := purchaseTx(ctx, tx, id)
	if err != nil {
		return nil, err
	}
	if p.Link == nil || p.SellerStatus != OrderProposed || p.Status != "drafted" {
		return nil, fmt.Errorf("%w: %s is not waiting for your Confirm", ErrState, p.Reference)
	}
	if len(items) != len(p.Lines) {
		return nil, fmt.Errorf("%w: say which of your items each of the %d lines is", ErrInvalid, len(p.Lines))
	}
	seen := map[string]bool{}
	for i, it := range items {
		if it.ID == "" || strings.TrimSpace(it.Name) == "" {
			return nil, fmt.Errorf("%w: say which of your items %s is", ErrInvalid, p.Lines[i].Item)
		}
		if seen[it.ID] {
			return nil, fmt.Errorf("%w: %s is on the order twice", ErrInvalid, it.Name)
		}
		seen[it.ID] = true
		p.Lines[i].ItemID, p.Lines[i].Item, p.Lines[i].Unit = it.ID, it.Name, it.Unit
	}
	deliver, err := DeliverToTx(ctx, tx, v, buyer)
	if err != nil {
		return nil, err
	}
	raw, _ := json.Marshal(p.Lines)
	if _, err := tx.Exec(ctx, `UPDATE purchase_orders SET lines = $2, status = 'confirmed', approved_by = $3, sent_at = now(), confirmed_at = now(),
		confirmed_how = 'supplier', seller_status = 'accepted', updated_at = now() WHERE id = $1`, id, raw, approver); err != nil {
		return nil, err
	}
	if err := stockAuditTx(ctx, tx, buyer, "purchase.confirmed", p.Reference, id, by); err != nil {
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
	seller, err := EnterTx(ctx, tx, *p.Link, "link.order.confirmed", detail, by)
	if err != nil {
		return nil, err
	}
	o, err := orderOf(ctx, tx, *p.Link, p.ID)
	if err != nil {
		return nil, err
	}
	if o.Status != OrderProposed {
		return nil, fmt.Errorf("%w: %s withdrew it", ErrState, o.BuyerName)
	}
	customer, subject, err := sealCustomer(ctx, tx, v, seller, buyer, name, deliver)
	if err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `UPDATE storefront_orders SET status = 'accepted', customer = $2, subject_id = $3, fulfilment = $4, decided_at = now(), updated_at = now()
		WHERE id = $1`, o.ID, customer, subject, fulfilment(deliver)); err != nil {
		return nil, err
	}
	return &Told{Tenant: seller, Event: "confirmed", Reference: p.Reference, From: name, Order: o.ID, Purchase: p.ID, Link: *p.Link, FromTenant: buyer}, nil
}
