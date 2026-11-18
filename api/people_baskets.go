package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/chatlinks"
	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/storefront"
)

// Ordering together (docs/spec/people-chatting.md §10.3, §11 A and C,
// §17 Q8; docs/spec/people-chatting-5b.md C with the founder's C1, C2
// and C3; decision 0069 points 8 and 9; migration 00486).
//
// "Order together from {business}", in a group or in a chat between two
// people, posts a shared basket card. Each member adds their own lines
// from the business's shop through the basket door (its sizes and
// colours, prices and stock as they are now); the holder (the starter,
// until they hand it on) chooses delivery or pick-up and the area, closes
// it (reopening it until it is placed) and places it through the door as
// their own order: the business sees one order from that person with a
// note of whose lines are whose, and never the group or the chat. The
// placer chooses how it is paid: "I'll pay all" (then a "Your share" card
// to each member in one step), or "Each pays their share" (each member's
// own pay link for their items and an equal part of the delivery; the
// placer may pay what is left for anyone). A business that takes no
// payment in the app: the placer pays all. After placing, the basket
// follows the order: its state, what was paid, its delivery; each member
// sees their share. Taskiem moves no money between people (0069 point 9).

const (
	basketLinesPerPerson = 20
	basketLinesMax       = 50 // an order's lines
)

// basketRoutes are the person's basket routes (basketPersonRoutes).
func (s *Server) basketRoutes(r chi.Router) {
	r = r.With(s.peopleHere)
	r.Post("/me/baskets", s.startBasket)
	r.Get("/me/baskets/{id}", s.getBasket)
	r.Put("/me/baskets/{id}", s.putBasket)
	r.Get("/me/baskets/{id}/shop", s.basketShop)
	r.Put("/me/baskets/{id}/mine", s.putMyBasketLines)
	r.Delete("/me/baskets/{id}/lines/{line}", s.deleteBasketLine)
	r.Post("/me/baskets/{id}/close", s.closeBasket)
	r.Post("/me/baskets/{id}/reopen", s.reopenBasket)
	r.Post("/me/baskets/{id}/cancel", s.cancelBasket)
	r.Put("/me/baskets/{id}/holder", s.handBasket)
	r.Post("/me/baskets/{id}/place", s.placeBasket)
	r.Get("/me/baskets/{id}/pay", s.payBasketShare)
	r.Post("/me/baskets/{id}/share-cards", s.sendBasketShareCards)
}

// basketPersonRoutes are the basket routes a personal session reaches.
var basketPersonRoutes = regexp.MustCompile(`^(POST /v1/me/baskets|(GET|PUT) /v1/me/baskets/[^/]+|GET /v1/me/baskets/[^/]+/(shop|pay)|PUT /v1/me/baskets/[^/]+/(mine|holder)|` +
	`DELETE /v1/me/baskets/[^/]+/lines/[^/]+|POST /v1/me/baskets/[^/]+/(close|reopen|cancel|place|share-cards))$`)

var (
	errBasketNotHolder = customerSendErr{http.StatusForbidden, "Only the person holding the basket can do this. Ask them, or ask them to hand it to you."}
	errBasketClosed    = customerSendErr{http.StatusConflict, "This basket is closed: ask the person holding it to reopen it."}
	errBasketDone      = customerSendErr{http.StatusConflict, "This basket's order was placed or cancelled: it can't be changed."}
)

// --- the basket as kept ---

type basketRow struct {
	ID, Business              uuid.UUID
	BusinessName              string
	Group, Conversation       *uuid.UUID
	Starter, Holder           uuid.UUID
	State                     string
	Fulfilment, Zone, Payment *string
	Reference                 *string
	PlacedBy                  *uuid.UUID
	PlacedKobo, DeliveryKobo  *int64
	CreatedAt                 time.Time
	ClosedAt, PlacedAt        *time.Time
}

const basketCols = `id, business_id, business_name, group_id, conversation_id, starter_id, holder_id, state, fulfilment, zone, payment,
	order_reference, placed_by, placed_kobo, delivery_kobo, created_at, closed_at, placed_at`

func scanBasket(r pgx.Row) (basketRow, error) {
	var b basketRow
	err := r.Scan(&b.ID, &b.Business, &b.BusinessName, &b.Group, &b.Conversation, &b.Starter, &b.Holder, &b.State, &b.Fulfilment, &b.Zone, &b.Payment,
		&b.Reference, &b.PlacedBy, &b.PlacedKobo, &b.DeliveryKobo, &b.CreatedAt, &b.ClosedAt, &b.PlacedAt)
	return b, err
}

// basketTx is a basket the person reaches (row-level security), locked
// for a writer.
func basketTx(ctx context.Context, tx pgx.Tx, idText string, lock bool) (basketRow, error) {
	id, err := uuid.Parse(idText)
	if err != nil {
		return basketRow{}, errDoorNone
	}
	q := `SELECT ` + basketCols + ` FROM people_baskets WHERE id = $1`
	if lock {
		q += ` FOR UPDATE`
	}
	b, err := scanBasket(tx.QueryRow(ctx, q, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return b, errDoorNone
	}
	return b, err
}

func (b basketRow) str(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

// changeable: lines and settings may change (open or closed).
func (b basketRow) changeable() error {
	switch b.State {
	case "open", "closed":
		return nil
	case "placing":
		return customerSendErr{http.StatusConflict, "This basket's order is being placed."}
	}
	return errBasketDone
}

type basketLine struct {
	ID        uuid.UUID
	Person    uuid.UUID
	ItemID    string
	Name      string
	Quantity  int
	PriceKobo int64
}

func basketLinesTx(ctx context.Context, tx pgx.Tx, basket uuid.UUID) ([]basketLine, error) {
	rows, err := tx.Query(ctx, `SELECT id, person_id, item_id, name, quantity, price_kobo FROM people_basket_lines WHERE basket_id = $1 ORDER BY created_at, id`, basket)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (basketLine, error) {
		var l basketLine
		return l, r.Scan(&l.ID, &l.Person, &l.ItemID, &l.Name, &l.Quantity, &l.PriceKobo)
	})
}

type basketShare struct {
	Person                 uuid.UUID
	Key                    uuid.UUID
	Items, Delivery, Total int64
	CardAt                 *time.Time
}

func basketSharesTx(ctx context.Context, tx pgx.Tx, basket uuid.UUID) ([]basketShare, error) {
	rows, err := tx.Query(ctx, `SELECT person_id, key, items_kobo, delivery_kobo, amount_kobo, card_at FROM people_basket_shares WHERE basket_id = $1`, basket)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (basketShare, error) {
		var x basketShare
		return x, r.Scan(&x.Person, &x.Key, &x.Items, &x.Delivery, &x.Total, &x.CardAt)
	})
}

// basketMembersTx is who is in the basket's group or chat now (nil when
// the person reaches it only as its placer or one with lines in it, after
// leaving), and the person's own chat's id for a chat between two.
func basketMembersTx(ctx context.Context, tx pgx.Tx, person uuid.UUID, b basketRow) ([]uuid.UUID, *uuid.UUID, error) {
	if b.Group != nil {
		var in bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM group_members WHERE group_id = $1 AND person_id = $2 AND left_at IS NULL)`, *b.Group, person).Scan(&in); err != nil || !in {
			return nil, nil, err
		}
		rows, err := tx.Query(ctx, `SELECT person_id FROM group_members WHERE group_id = $1 AND left_at IS NULL`, *b.Group)
		if err != nil {
			return nil, nil, err
		}
		ids, err := pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
		return ids, nil, err
	}
	var chat uuid.UUID
	var peer *uuid.UUID
	err := tx.QueryRow(ctx, `SELECT id, peer_id FROM people_chats WHERE person_id = $1 AND conversation_id = $2`, person, *b.Conversation).Scan(&chat, &peer)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	ids := []uuid.UUID{person}
	if peer != nil {
		var active bool
		if err := tx.QueryRow(ctx, `SELECT taskiem_people_writable($1)`, *peer).Scan(&active); err != nil {
			return nil, nil, err
		}
		if active {
			ids = append(ids, *peer)
		}
	}
	return ids, &chat, nil
}

// --- the shop, priced ---

// shopPrice is an item of the shop as a line names it.
type shopPrice struct {
	ok        bool // in the shop now, and orderable as named
	name      string
	price     *int64
	available bool
	stock     *int64
}

// shopIndex prices an item of the shop as placing an order does: an item,
// or one of a product's sizes and colours (a product with sizes is named
// by one of them).
func shopIndex(shop theirShop) func(id string) shopPrice {
	byID := map[string]storefront.Item{}
	for _, it := range shop.all {
		byID[it.ID] = it
	}
	return func(id string) shopPrice {
		it, ok := byID[id]
		if !ok || it.DurationMinutes > 0 || it.Variants > 0 {
			return shopPrice{}
		}
		p := shopPrice{ok: true, name: it.Name, price: it.PriceKobo, available: available(it), stock: it.Stock}
		if it.VariantOf != "" {
			parent := byID[it.VariantOf]
			if p.price == nil {
				p.price = parent.PriceKobo
			}
			if p.name == "" || p.name == parent.Name {
				keys := make([]string, 0, len(it.Variant))
				for k := range it.Variant {
					keys = append(keys, k)
				}
				sort.Strings(keys)
				vs := make([]string, 0, len(keys))
				for _, k := range keys {
					vs = append(vs, it.Variant[k])
				}
				p.name = strings.TrimSpace(parent.Name + " " + strings.Join(vs, " "))
			}
		}
		if p.price == nil || *p.price < 1 {
			p.available = false
		}
		return p
	}
}

// deliveryFee is the fee for the basket's way of having it, and whether
// the shop offers it as chosen.
func deliveryFee(shop theirShop, fulfilment, zone string) (int64, bool) {
	switch {
	case fulfilment == "delivery" && shop.Delivery:
		if len(shop.Zones) == 0 {
			return 0, true
		}
		for _, z := range shop.Zones {
			if z.Name == zone {
				return z.FeeKobo, true
			}
		}
		return 0, false
	case fulfilment == "pickup" && shop.Pickup:
		return 0, true
	case fulfilment == "" && !shop.Pickup && !shop.Delivery:
		return 0, true
	}
	return 0, false
}

// eachOffered: each member may pay their own share (C2): the business
// takes payment in the app, by card through Taskiem's pay links.
func eachOffered(shop theirShop) bool {
	return shop.card && (shop.Payment == "at_order" || shop.Payment == "after_accept")
}

// --- what the person sees ---

type basketLineOut struct {
	ID       uuid.UUID `json:"id"`
	ItemID   string    `json:"item_id"`
	Name     string    `json:"name"`
	Quantity int       `json:"quantity"`
	// PriceKobo is the shop's price now (as placed, once placed);
	// AddedPriceKobo the price when the line was added.
	PriceKobo      int64 `json:"price_kobo"`
	AddedPriceKobo int64 `json:"added_price_kobo"`
	PriceChanged   bool  `json:"price_changed"`
	LineKobo       int64 `json:"line_kobo"`
	// Available: in the shop now and not sold out (always true once
	// placed); FewLeft as the shop says it.
	Available bool   `json:"available"`
	FewLeft   *int64 `json:"few_left,omitempty"`
}

type basketShareOut struct {
	ItemsKobo    int64      `json:"items_kobo"`
	DeliveryKobo int64      `json:"delivery_kobo"`
	AmountKobo   int64      `json:"amount_kobo"`
	Paid         bool       `json:"paid"`
	PaidAt       *time.Time `json:"paid_at,omitempty"`
	CardSent     bool       `json:"card_sent"`
}

type basketPersonOut struct {
	PersonID  uuid.UUID       `json:"person_id"`
	Name      string          `json:"name"`
	You       bool            `json:"you"`
	Lines     []basketLineOut `json:"lines"`
	ItemsKobo int64           `json:"items_kobo"`
	Share     *basketShareOut `json:"share,omitempty"`
}

type basketShopOut struct {
	Open        bool              `json:"open"`
	Closed      string            `json:"closed,omitempty"`
	Pickup      bool              `json:"pickup"`
	Delivery    bool              `json:"delivery"`
	Zones       []storefront.Zone `json:"zones"`
	Payment     string            `json:"payment"`
	EachOffered bool              `json:"each_offered"`
}

type basketOrderOut struct {
	Reference    string            `json:"reference"`
	Status       string            `json:"status"`
	Line         string            `json:"line"`
	TotalKobo    int64             `json:"total_kobo"`
	PaidKobo     int64             `json:"paid_kobo"`
	LeftKobo     int64             `json:"left_kobo"`
	PaymentState string            `json:"payment_state"`
	PayableNow   bool              `json:"payable_now"`
	PayBy        *time.Time        `json:"pay_by,omitempty"`
	Delivery     *deliveryProgress `json:"delivery,omitempty"`
}

// basketOut is a basket as the person sees it (Basket in the API).
type basketOut struct {
	ID         uuid.UUID  `json:"id"`
	BusinessID uuid.UUID  `json:"business_id"`
	Business   string     `json:"business"`
	Where      string     `json:"where"` // group or person
	GroupID    *uuid.UUID `json:"group_id,omitempty"`
	ChatID     *uuid.UUID `json:"chat_id,omitempty"`
	// State: open (members add), closed (the holder may reopen or place),
	// placing, placed, cancelled.
	State      string    `json:"state"`
	StarterID  uuid.UUID `json:"starter_id"`
	HolderID   uuid.UUID `json:"holder_id"`
	Holder     string    `json:"holder"`
	YouHold    bool      `json:"you_hold"`
	HolderGone bool      `json:"holder_gone"`
	Fulfilment string    `json:"fulfilment,omitempty"`
	Zone       string    `json:"zone,omitempty"`
	// Payment once placed: all (the placer paid) or each.
	Payment      string            `json:"payment,omitempty"`
	Shop         *basketShopOut    `json:"shop,omitempty"`
	People       []basketPersonOut `json:"people"`
	ItemsKobo    int64             `json:"items_kobo"`
	DeliveryKobo int64             `json:"delivery_kobo"`
	TotalKobo    int64             `json:"total_kobo"`
	// Problems: what stands in the way of placing it, in words.
	Problems  []string        `json:"problems"`
	Order     *basketOrderOut `json:"order,omitempty"`
	PlacedBy  *uuid.UUID      `json:"placed_by,omitempty"`
	CreatedAt time.Time       `json:"created_at"`
	ClosedAt  *time.Time      `json:"closed_at,omitempty"`
	PlacedAt  *time.Time      `json:"placed_at,omitempty"`
}

// basketOrderSnap is taskiem_basket_order's answer.
type basketOrderSnap struct {
	Status    string     `json:"status"`
	Payment   string     `json:"payment"`
	TotalKobo int64      `json:"total_kobo"`
	DueKobo   int64      `json:"due_kobo"`
	PaidKobo  int64      `json:"paid_kobo"`
	ExpiresAt *time.Time `json:"expires_at"`
	Delivery  *struct {
		Status      string     `json:"status"`
		AcceptedAt  *time.Time `json:"accepted_at"`
		PickedUpAt  *time.Time `json:"picked_up_at"`
		ArrivedAt   *time.Time `json:"arrived_at"`
		DeliveredAt *time.Time `json:"delivered_at"`
	} `json:"delivery"`
	Shares map[string]*time.Time `json:"shares"`
}

// orderLines are an order's states in a member's words.
var orderLines = map[string]string{
	"awaiting_payment": "Waiting for payment",
	"received":         "Sent to the business",
	"accepted":         "Accepted by the business",
	"paid":             "Paid",
	"dispatched":       "On its way",
	"on_the_way":       "On its way",
	"arrived":          "Arrived",
	"completed":        "Done",
	"declined":         "Declined by the business",
	"cancelled":        "Cancelled",
	"expired":          "Not paid in time: the order lapsed",
	"price_changed":    "The price changed: the placer confirms it with the business",
	"refund_due":       "To be refunded by the business",
	"refunded":         "Refunded",
}

// basketData is what a view is made from.
type basketData struct {
	b       basketRow
	lines   []basketLine
	shares  []basketShare
	members []uuid.UUID
	chat    *uuid.UUID
	names   map[uuid.UUID]string
	order   *basketOrderSnap
	shop    *theirShop
}

// basketDataTx reads a basket for the person, in their own transaction.
func (s *Server) basketDataTx(ctx context.Context, tx pgx.Tx, person uuid.UUID, b basketRow) (basketData, error) {
	d := basketData{b: b}
	var err error
	if d.lines, err = basketLinesTx(ctx, tx, b.ID); err != nil {
		return d, err
	}
	if d.shares, err = basketSharesTx(ctx, tx, b.ID); err != nil {
		return d, err
	}
	if d.members, d.chat, err = basketMembersTx(ctx, tx, person, b); err != nil {
		return d, err
	}
	ids := []uuid.UUID{b.Holder, b.Starter}
	for _, l := range d.lines {
		ids = append(ids, l.Person)
	}
	for _, x := range d.shares {
		ids = append(ids, x.Person)
	}
	if d.names, _, err = s.groupNamesTx(ctx, tx, person, ids); err != nil {
		return d, err
	}
	if b.State == "placed" {
		var raw []byte
		if err := tx.QueryRow(ctx, `SELECT taskiem_basket_order($1)`, b.ID).Scan(&raw); err != nil {
			return d, err
		}
		if raw != nil {
			var o basketOrderSnap
			if err := json.Unmarshal(raw, &o); err != nil {
				return d, err
			}
			d.order = &o
		}
	}
	return d, nil
}

// basketShopTx reads the basket's business's shop through the basket door
// (a transaction of its own: nothing of the person's is read after it).
func basketShopTx(ctx context.Context, tx pgx.Tx, b basketRow) (theirShop, error) {
	if _, err := tx.Exec(ctx, `SELECT taskiem_basket_enter($1, 'shop')`, b.ID); err != nil {
		return theirShop{}, doorErr(err)
	}
	return theirShopTx(ctx, tx, b.Business, b.BusinessName)
}

// member is whether the person is in the basket's group or chat now (or
// nothing is known of who is: a placed basket seen after leaving).
func (d basketData) member(p uuid.UUID) bool {
	return d.members == nil || slices.Contains(d.members, p)
}

// counted are the lines that count: of people in the group or chat now
// (until placed; every line once placed).
func (d basketData) counted() []basketLine {
	if d.b.State == "placed" {
		return d.lines
	}
	out := []basketLine{}
	for _, l := range d.lines {
		if d.member(l.Person) {
			out = append(out, l)
		}
	}
	return out
}

// view makes the basket as the person sees it.
func (d basketData) view(person uuid.UUID) basketOut {
	b := d.b
	out := basketOut{ID: b.ID, BusinessID: b.Business, Business: b.BusinessName, State: b.State, StarterID: b.Starter, HolderID: b.Holder,
		Holder: d.names[b.Holder], YouHold: b.Holder == person, Fulfilment: b.str(b.Fulfilment), Zone: b.str(b.Zone), Payment: b.str(b.Payment),
		People: []basketPersonOut{}, Problems: []string{}, PlacedBy: b.PlacedBy, CreatedAt: b.CreatedAt, ClosedAt: b.ClosedAt, PlacedAt: b.PlacedAt}
	if b.Group != nil {
		out.Where, out.GroupID = "group", b.Group
	} else {
		out.Where, out.ChatID = "person", d.chat
	}
	open := b.State == "open" || b.State == "closed" || b.State == "placing"
	out.HolderGone = open && d.members != nil && !slices.Contains(d.members, b.Holder)
	var price func(string) shopPrice
	if d.shop != nil && open {
		sh := *d.shop
		out.Shop = &basketShopOut{Open: sh.Open, Closed: sh.Closed, Pickup: sh.Pickup, Delivery: sh.Delivery, Zones: sh.Zones, Payment: sh.Payment, EachOffered: eachOffered(sh)}
		if !sh.Open {
			out.Problems = append(out.Problems, sh.Closed)
		}
		price = shopIndex(sh)
	}
	lines := d.counted()
	wanted := map[string]int64{}
	for _, l := range lines {
		wanted[l.ItemID] += int64(l.Quantity)
	}
	byPerson := map[uuid.UUID]*basketPersonOut{}
	var order []uuid.UUID
	told := map[string]bool{}
	problem := func(p string) {
		if !told[p] {
			told[p] = true
			out.Problems = append(out.Problems, p)
		}
	}
	for _, l := range lines {
		lo := basketLineOut{ID: l.ID, ItemID: l.ItemID, Name: l.Name, Quantity: l.Quantity, PriceKobo: l.PriceKobo, AddedPriceKobo: l.PriceKobo, Available: true}
		if price != nil {
			p := price(l.ItemID)
			switch {
			case !p.ok:
				lo.Available = false
				problem(l.Name + " is no longer in their shop.")
			case !p.available:
				lo.Available = false
				problem(l.Name + " is sold out now.")
			default:
				lo.PriceKobo = *p.price
				lo.PriceChanged = lo.PriceKobo != l.PriceKobo
				if p.stock != nil && wanted[l.ItemID] > *p.stock {
					problem(fmt.Sprintf("Only %d of %s left: the basket wants %d.", *p.stock, l.Name, wanted[l.ItemID]))
				}
				lo.FewLeft = fewOf(p.stock)
			}
		}
		lo.LineKobo = int64(lo.Quantity) * lo.PriceKobo
		bp := byPerson[l.Person]
		if bp == nil {
			bp = &basketPersonOut{PersonID: l.Person, Name: d.names[l.Person], You: l.Person == person, Lines: []basketLineOut{}}
			byPerson[l.Person] = bp
			order = append(order, l.Person)
		}
		bp.Lines = append(bp.Lines, lo)
		bp.ItemsKobo += lo.LineKobo
		out.ItemsKobo += lo.LineKobo
	}
	// Shares, once placed: every one of them, lines or not.
	for _, x := range d.shares {
		bp := byPerson[x.Person]
		if bp == nil {
			bp = &basketPersonOut{PersonID: x.Person, Name: d.names[x.Person], You: x.Person == person, Lines: []basketLineOut{}}
			byPerson[x.Person] = bp
			order = append(order, x.Person)
		}
		sh := &basketShareOut{ItemsKobo: x.Items, DeliveryKobo: x.Delivery, AmountKobo: x.Total, CardSent: x.CardAt != nil}
		if d.order != nil {
			if at := d.order.Shares[x.Key.String()]; at != nil {
				sh.Paid, sh.PaidAt = true, at
			}
		}
		bp.Share = sh
	}
	for _, id := range order {
		out.People = append(out.People, *byPerson[id])
	}
	// The one holding it first, then as they came.
	sort.SliceStable(out.People, func(i, j int) bool { return out.People[i].PersonID == b.Holder && out.People[j].PersonID != b.Holder })
	switch {
	case b.State == "placed":
		if b.DeliveryKobo != nil {
			out.DeliveryKobo = *b.DeliveryKobo
		}
		if b.PlacedKobo != nil {
			out.TotalKobo = *b.PlacedKobo
		}
	case d.shop != nil:
		fee, ok := deliveryFee(*d.shop, out.Fulfilment, out.Zone)
		if !ok && d.shop.Open {
			switch {
			case out.Fulfilment == "delivery" && d.shop.Delivery:
				problem("Choose one of the areas they deliver to.")
			default:
				problem("Choose pick-up or delivery, as they offer it.")
			}
		}
		out.DeliveryKobo = fee
		out.TotalKobo = out.ItemsKobo + fee
	default:
		out.TotalKobo = out.ItemsKobo
	}
	if len(lines) == 0 && open {
		problem("Nothing in the basket yet.")
	}
	if o := d.order; o != nil && b.Reference != nil {
		x := basketOrderOut{Reference: *b.Reference, Status: o.Status, Line: orderLines[o.Status], TotalKobo: o.DueKobo, PaidKobo: o.PaidKobo,
			LeftKobo: max(o.DueKobo-o.PaidKobo, 0), PayableNow: storefront.PayableNow(o.Status, o.Payment, max(o.DueKobo-o.PaidKobo, 0))}
		if x.Line == "" {
			x.Line = strings.ReplaceAll(o.Status, "_", " ")
		}
		switch {
		case o.PaidKobo <= 0:
			x.PaymentState = "unpaid"
		case o.PaidKobo < o.DueKobo:
			x.PaymentState = "part_paid"
		default:
			x.PaymentState = "paid"
		}
		if o.Status == "awaiting_payment" {
			x.PayBy = o.ExpiresAt
		}
		if dv := o.Delivery; dv != nil {
			x.Delivery = &deliveryProgress{Status: dv.Status, Line: deliveryLines[dv.Status], AcceptedAt: dv.AcceptedAt, PickedUpAt: dv.PickedUpAt,
				ArrivedAt: dv.ArrivedAt, DeliveredAt: dv.DeliveredAt}
		}
		out.Order = &x
	}
	return out
}

// basketView is a basket as the person sees it now: read in their own
// transaction, with its business's shop through the basket door while it
// is not placed (prices, stock, how it is had).
func (s *Server) basketView(ctx context.Context, person, id uuid.UUID) (basketOut, error) {
	var d basketData
	err := db.InPersonTx(ctx, s.Store.Pool, person, func(tx pgx.Tx) error {
		b, err := basketTx(ctx, tx, id.String(), false)
		if err != nil {
			return err
		}
		d, err = s.basketDataTx(ctx, tx, person, b)
		return err
	})
	if err != nil {
		return basketOut{}, err
	}
	if d.b.State == "open" || d.b.State == "closed" || d.b.State == "placing" {
		err := db.InPersonTx(ctx, s.Store.Pool, person, func(tx pgx.Tx) error {
			shop, err := basketShopTx(ctx, tx, d.b)
			if err == nil {
				d.shop = &shop
			}
			return err
		})
		if errors.Is(err, errDoorNone) {
			// The business no longer shows itself: the basket as kept.
			err = nil
		}
		if err != nil {
			return basketOut{}, err
		}
	}
	return d.view(person), nil
}

// --- handlers ---

// startBasket is POST /v1/me/baskets {business_id, group_id | chat_id}:
// "Order together from {business}" posts a shared basket card in the group
// or the chat (C3: a chat between two people too), and answers the basket
// (201) with the card's message.
func (s *Server) startBasket(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	person, err := personOf(r)
	if err != nil {
		s.personFail(w, r, err)
		return
	}
	var in struct {
		BusinessID uuid.UUID  `json:"business_id"`
		GroupID    *uuid.UUID `json:"group_id"`
		ChatID     *uuid.UUID `json:"chat_id"`
	}
	if err := decodeBody(r, &in); err != nil {
		s.fail(w, r, err)
		return
	}
	if in.BusinessID == uuid.Nil || (in.GroupID == nil) == (in.ChatID == nil) {
		writeErr(w, http.StatusBadRequest, "give business_id, and group_id or chat_id")
		return
	}
	if !s.limiter("basket-start:"+person.String(), 6*time.Second, 10).Allow() {
		writeErr(w, http.StatusTooManyRequests, "You have started many baskets. Wait a moment and try again.")
		return
	}
	// The business, as its card shows it, and whether it takes orders in
	// the app now (through the card door).
	var card chatlinks.Card
	err = db.InPersonTx(ctx, s.Store.Pool, person, func(tx pgx.Tx) error {
		if err := enterCardTx(ctx, tx, person, in.BusinessID, "business"); err != nil {
			return err
		}
		var err error
		if card, err = chatlinks.CardTx(ctx, tx, in.BusinessID, s.previewDomain(), time.Now()); err != nil {
			return err
		}
		shop, err := theirShopTx(ctx, tx, in.BusinessID, card.Name)
		if err != nil {
			return err
		}
		if !shop.Open {
			return customerSendErr{http.StatusConflict, shop.Closed}
		}
		return nil
	})
	if err != nil {
		s.personFail(w, r, err)
		return
	}
	id := uuid.Must(uuid.NewV7())
	err = s.peopleTx(ctx, person, func(tx pgx.Tx) error {
		if err := notSuspendedTx(ctx, tx, person); err != nil {
			return err
		}
		var group, conv *uuid.UUID
		if in.GroupID != nil {
			g, err := groupTx(ctx, tx, person, in.GroupID.String(), false)
			if err != nil {
				return err
			}
			if !g.current() {
				return errGroupLeft
			}
			group = &g.ID
		} else {
			c, err := peopleCopyTx(ctx, tx, person, in.ChatID.String(), false)
			if err != nil {
				return err
			}
			switch {
			case c.Blocked:
				return errPeopleYouBlocked
			case c.RequestAt != nil || c.WaitingAt != nil || c.Peer == nil:
				return customerSendErr{http.StatusConflict, "Order together once you are chatting: after they accept your message, or you accept theirs."}
			}
			conv = &c.Conversation
		}
		_, err := tx.Exec(ctx, `INSERT INTO people_baskets (id, business_id, business_name, group_id, conversation_id, starter_id, holder_id)
			VALUES ($1, $2, $3, $4, $5, $6, $6)`, id, in.BusinessID, card.Name, group, conv, person)
		return err
	})
	if err != nil {
		s.personFail(w, r, err)
		return
	}
	biz := in.BusinessID
	d := peopleDraft{kind: "card", clientID: "basket:" + id.String(), card: &peopleCard{Type: "basket", Title: "Order together from " + card.Name,
		BusinessID: &biz, Business: card.Name, Area: card.Area, BasketID: &id}}
	var msg any
	if in.GroupID != nil {
		msg, _, err = s.groupSend(ctx, person, in.GroupID.String(), d, nil)
	} else {
		msg, _, err = s.peopleSend(ctx, person, in.ChatID.String(), d)
	}
	if err != nil {
		// Not posted (only admins post, a limit): the basket goes.
		_ = db.InPersonTx(context.WithoutCancel(ctx), s.Store.Pool, person, func(tx pgx.Tx) error {
			_, err := tx.Exec(ctx, `UPDATE people_baskets SET state = 'cancelled' WHERE id = $1`, id)
			return err
		})
		s.personFail(w, r, err)
		return
	}
	out, err := s.basketView(ctx, person, id)
	if err != nil {
		s.personFail(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"basket": out, "message": msg})
}

// getBasket is GET /v1/me/baskets/{id}: the basket as it is now.
func (s *Server) getBasket(w http.ResponseWriter, r *http.Request) {
	person, err := personOf(r)
	if err != nil {
		s.personFail(w, r, err)
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		s.personFail(w, r, errDoorNone)
		return
	}
	out, err := s.basketView(r.Context(), person, id)
	if err != nil {
		s.personFail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// basketShop is GET /v1/me/baskets/{id}/shop: the business's shop through
// the basket door, as the customer door shows it (sizes and colours,
// prices and stock now, pick-up, delivery and its areas).
func (s *Server) basketShop(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	person, err := personOf(r)
	if err != nil {
		s.personFail(w, r, err)
		return
	}
	var b basketRow
	err = db.InPersonTx(ctx, s.Store.Pool, person, func(tx pgx.Tx) error {
		b, err = basketTx(ctx, tx, chi.URLParam(r, "id"), false)
		return err
	})
	if err == nil {
		err = b.changeable()
	}
	var shop theirShop
	if err == nil {
		err = db.InPersonTx(ctx, s.Store.Pool, person, func(tx pgx.Tx) error {
			var err error
			shop, err = basketShopTx(ctx, tx, b)
			return err
		})
	}
	if err != nil {
		s.personFail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, shop)
}

// basketWrite runs a change to a basket in the person's transaction, the
// basket locked, then answers the basket as it is now.
func (s *Server) basketWrite(w http.ResponseWriter, r *http.Request, fn func(tx pgx.Tx, person uuid.UUID, d basketData) error) {
	ctx := r.Context()
	person, err := personOf(r)
	if err != nil {
		s.personFail(w, r, err)
		return
	}
	var id uuid.UUID
	err = s.peopleTx(ctx, person, func(tx pgx.Tx) error {
		if err := notSuspendedTx(ctx, tx, person); err != nil {
			return err
		}
		b, err := basketTx(ctx, tx, chi.URLParam(r, "id"), true)
		if err != nil {
			return err
		}
		id = b.ID
		d, err := s.basketDataTx(ctx, tx, person, b)
		if err != nil {
			return err
		}
		return fn(tx, person, d)
	})
	if err != nil {
		s.personFail(w, r, err)
		return
	}
	out, err := s.basketView(ctx, person, id)
	if err != nil {
		s.personFail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// holderTx refuses anyone but the basket's holder.
func (d basketData) holder(person uuid.UUID) error {
	if d.b.Holder != person {
		return errBasketNotHolder
	}
	return nil
}

// purgeTx takes away the lines of people who are no longer in the group
// or the chat (left, removed, or their account deleted), before the
// basket is closed or placed.
func (d *basketData) purgeTx(ctx context.Context, tx pgx.Tx) error {
	if d.members == nil {
		return nil
	}
	var gone []uuid.UUID
	for _, l := range d.lines {
		if !slices.Contains(d.members, l.Person) {
			gone = append(gone, l.ID)
		}
	}
	if len(gone) == 0 {
		return nil
	}
	_, err := tx.Exec(ctx, `DELETE FROM people_basket_lines WHERE basket_id = $1 AND id = ANY ($2)`, d.b.ID, gone)
	d.lines = d.counted()
	return err
}

// putBasket is PUT /v1/me/baskets/{id} {fulfilment?, zone?}: the holder
// chooses pick-up or delivery and the area (checked when it is placed).
func (s *Server) putBasket(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Fulfilment *string `json:"fulfilment"`
		Zone       *string `json:"zone"`
	}
	if err := decodeBody(r, &in); err != nil {
		s.fail(w, r, err)
		return
	}
	if in.Fulfilment != nil && *in.Fulfilment != "" && *in.Fulfilment != "pickup" && *in.Fulfilment != "delivery" {
		writeErr(w, http.StatusBadRequest, "fulfilment is pickup or delivery")
		return
	}
	if in.Zone != nil && len([]rune(*in.Zone)) > 120 {
		writeErr(w, http.StatusBadRequest, "a zone is up to 120 characters")
		return
	}
	s.basketWrite(w, r, func(tx pgx.Tx, person uuid.UUID, d basketData) error {
		if err := d.holder(person); err != nil {
			return err
		}
		if err := d.b.changeable(); err != nil {
			return err
		}
		f, z := d.b.Fulfilment, d.b.Zone
		if in.Fulfilment != nil {
			f = in.Fulfilment
			if *f == "" {
				f = nil
			}
		}
		if in.Zone != nil {
			z = in.Zone
		}
		if f == nil || *f != "delivery" {
			z = nil
		}
		_, err := tx.Exec(r.Context(), `UPDATE people_baskets SET fulfilment = $2, zone = $3 WHERE id = $1`, d.b.ID, f, z)
		return err
	})
}

// basketLineIn is a line a member asks for.
type basketLineIn struct {
	ItemID   string `json:"item_id"`
	Quantity int    `json:"quantity"`
}

// putMyBasketLines is PUT /v1/me/baskets/{id}/mine {lines}: the member's
// own lines, all of them (an empty list takes theirs out), while the
// basket is open; each checked against the shop now (in it, a size where
// it has sizes, not sold out, within the stock the whole basket wants).
func (s *Server) putMyBasketLines(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	person, err := personOf(r)
	if err != nil {
		s.personFail(w, r, err)
		return
	}
	var in struct {
		Lines []basketLineIn `json:"lines"`
	}
	if err := decodeBody(r, &in); err != nil {
		s.fail(w, r, err)
		return
	}
	if len(in.Lines) > basketLinesPerPerson {
		writeErr(w, http.StatusBadRequest, fmt.Sprintf("each person adds up to %d lines", basketLinesPerPerson))
		return
	}
	for _, l := range in.Lines {
		if l.ItemID == "" || len(l.ItemID) > 100 || l.Quantity < 1 || l.Quantity > 99 {
			writeErr(w, http.StatusBadRequest, "each line is an item_id and 1 to 99 of it")
			return
		}
	}
	// Read the basket and the others' lines, then the shop through the
	// door, then write under the basket's lock.
	var d basketData
	err = db.InPersonTx(ctx, s.Store.Pool, person, func(tx pgx.Tx) error {
		b, err := basketTx(ctx, tx, chi.URLParam(r, "id"), false)
		if err != nil {
			return err
		}
		d, err = s.basketDataTx(ctx, tx, person, b)
		return err
	})
	if err == nil && d.b.State != "open" {
		err = errBasketClosed
		if d.b.State != "closed" {
			err = d.b.changeable()
		}
	}
	if err == nil && !d.member(person) {
		err = errDoorNone
	}
	var shop theirShop
	if err == nil {
		err = db.InPersonTx(ctx, s.Store.Pool, person, func(tx pgx.Tx) error {
			var err error
			shop, err = basketShopTx(ctx, tx, d.b)
			return err
		})
	}
	if err == nil && !shop.Open {
		err = customerSendErr{http.StatusConflict, shop.Closed}
	}
	type priced struct {
		in    basketLineIn
		name  string
		price int64
	}
	var lines []priced
	if err == nil {
		price := shopIndex(shop)
		wanted := map[string]int64{}
		for _, l := range d.counted() {
			if l.Person != person {
				wanted[l.ItemID] += int64(l.Quantity)
			}
		}
		for _, l := range in.Lines {
			p := price(l.ItemID)
			switch {
			case !p.ok:
				err = customerSendErr{http.StatusUnprocessableEntity, "One of the items is not in their shop now (choose a size where there are sizes)."}
			case !p.available:
				err = customerSendErr{http.StatusUnprocessableEntity, p.name + " is not available now."}
			}
			if err != nil {
				break
			}
			wanted[l.ItemID] += int64(l.Quantity)
			if p.stock != nil && wanted[l.ItemID] > *p.stock {
				err = customerSendErr{http.StatusUnprocessableEntity, fmt.Sprintf("Only %d of %s left, with what the others want.", *p.stock, p.name)}
				break
			}
			lines = append(lines, priced{l, p.name, *p.price})
		}
	}
	if err != nil {
		s.personFail(w, r, err)
		return
	}
	s.basketWrite(w, r, func(tx pgx.Tx, person uuid.UUID, d basketData) error {
		if d.b.State != "open" {
			if d.b.State == "closed" {
				return errBasketClosed
			}
			return d.b.changeable()
		}
		others := 0
		for _, l := range d.counted() {
			if l.Person != person {
				others++
			}
		}
		if others+len(lines) > basketLinesMax {
			return customerSendErr{http.StatusConflict, fmt.Sprintf("A basket has up to %d lines in all.", basketLinesMax)}
		}
		if _, err := tx.Exec(ctx, `DELETE FROM people_basket_lines WHERE basket_id = $1 AND person_id = $2`, d.b.ID, person); err != nil {
			return err
		}
		for _, l := range lines {
			if _, err := tx.Exec(ctx, `INSERT INTO people_basket_lines (id, basket_id, person_id, item_id, quantity, name, price_kobo) VALUES ($1, $2, $3, $4, $5, $6, $7)`,
				uuid.Must(uuid.NewV7()), d.b.ID, person, l.in.ItemID, l.in.Quantity, l.name, l.price); err != nil {
				return err
			}
		}
		return nil
	})
}

// deleteBasketLine is DELETE /v1/me/baskets/{id}/lines/{line}: a member
// takes out one of their own lines while it is open; the holder takes out
// anyone's while it is not placed (an item sold out, say).
func (s *Server) deleteBasketLine(w http.ResponseWriter, r *http.Request) {
	s.basketWrite(w, r, func(tx pgx.Tx, person uuid.UUID, d basketData) error {
		if err := d.b.changeable(); err != nil {
			return err
		}
		id, err := uuid.Parse(chi.URLParam(r, "line"))
		if err != nil {
			return errDoorNone
		}
		i := slices.IndexFunc(d.lines, func(l basketLine) bool { return l.ID == id })
		if i < 0 {
			return errDoorNone
		}
		l := d.lines[i]
		switch {
		case person == d.b.Holder:
		case l.Person != person:
			return errBasketNotHolder
		case d.b.State != "open":
			return errBasketClosed
		}
		_, err = tx.Exec(r.Context(), `DELETE FROM people_basket_lines WHERE basket_id = $1 AND id = $2`, d.b.ID, id)
		return err
	})
}

// closeBasket is POST /v1/me/baskets/{id}/close: the holder closes it
// (C1): no one adds or changes lines until they reopen it or place it.
func (s *Server) closeBasket(w http.ResponseWriter, r *http.Request) {
	s.basketWrite(w, r, func(tx pgx.Tx, person uuid.UUID, d basketData) error {
		if err := d.holder(person); err != nil {
			return err
		}
		if d.b.State != "open" {
			return d.b.changeable()
		}
		if err := d.purgeTx(r.Context(), tx); err != nil {
			return err
		}
		if len(d.lines) == 0 {
			return customerSendErr{http.StatusConflict, "Nothing in the basket yet."}
		}
		_, err := tx.Exec(r.Context(), `UPDATE people_baskets SET state = 'closed', closed_at = now() WHERE id = $1`, d.b.ID)
		return err
	})
}

// reopenBasket is POST /v1/me/baskets/{id}/reopen: a closed basket is
// opened again, until it is placed (C1).
func (s *Server) reopenBasket(w http.ResponseWriter, r *http.Request) {
	s.basketWrite(w, r, func(tx pgx.Tx, person uuid.UUID, d basketData) error {
		if err := d.holder(person); err != nil {
			return err
		}
		if d.b.State != "closed" {
			if d.b.State == "open" {
				return nil
			}
			return d.b.changeable()
		}
		_, err := tx.Exec(r.Context(), `UPDATE people_baskets SET state = 'open', closed_at = NULL WHERE id = $1`, d.b.ID)
		return err
	})
}

// cancelBasket is POST /v1/me/baskets/{id}/cancel: the holder gives it
// up before it is placed.
func (s *Server) cancelBasket(w http.ResponseWriter, r *http.Request) {
	s.basketWrite(w, r, func(tx pgx.Tx, person uuid.UUID, d basketData) error {
		if err := d.holder(person); err != nil {
			return err
		}
		if err := d.b.changeable(); err != nil {
			return err
		}
		_, err := tx.Exec(r.Context(), `UPDATE people_baskets SET state = 'cancelled' WHERE id = $1`, d.b.ID)
		return err
	})
}

// handBasket is PUT /v1/me/baskets/{id}/holder {person_id}: the holder
// hands closing and placing to another member (C1). When the holder is no
// longer in the group or the chat, any member takes it themself.
func (s *Server) handBasket(w http.ResponseWriter, r *http.Request) {
	var in struct {
		PersonID uuid.UUID `json:"person_id"`
	}
	if err := decodeBody(r, &in); err != nil {
		s.fail(w, r, err)
		return
	}
	s.basketWrite(w, r, func(tx pgx.Tx, person uuid.UUID, d basketData) error {
		if err := d.b.changeable(); err != nil {
			return err
		}
		gone := d.members != nil && !slices.Contains(d.members, d.b.Holder)
		switch {
		case d.b.Holder == person:
		case gone && in.PersonID == person:
		default:
			return errBasketNotHolder
		}
		if d.members == nil || !slices.Contains(d.members, in.PersonID) {
			return customerSendErr{http.StatusConflict, "Hand it to someone in the group or the chat now."}
		}
		_, err := tx.Exec(r.Context(), `UPDATE people_baskets SET holder_id = $2 WHERE id = $1`, d.b.ID, in.PersonID)
		return err
	})
}
