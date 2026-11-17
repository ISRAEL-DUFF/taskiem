package api

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/israel-duff/taskiem/engine/storefront"
)

// The tools a customer has in a conversation (docs/spec/taskiem-for-
// everyone.md §8.3, §8.4), each through the customer door: their shop
// (the published catalogue at its prices, in sizes and colours, with
// stock where the page shows it), Book (services and free times), the
// person's own orders and bookings with the business, placing an order
// or a booking, and paying. An order placed here is a storefront order of
// the kind app, linked to the person, sealed under the business's subject
// for their phone, and goes through the business's order workflow as any
// order from its page does (the same inbox and worker).

// fewLeft is the stock at or under which the page says how few are left:
// the only stock a customer is shown.
const fewLeft = 5

// shopItem is an item of their shop as the customer sees it.
type shopItem struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	PriceKobo   *int64 `json:"price_kobo,omitempty"`
	Description string `json:"description,omitempty"`
	Category    string `json:"category,omitempty"`
	Photo       string `json:"photo,omitempty"`
	Available   bool   `json:"available"`
	// FewLeft is how many are left when the page says so (5 or fewer).
	FewLeft *int64 `json:"few_left,omitempty"`
	// Sizes are a product's sizes and colours on sale: an order names one.
	Sizes []shopSize `json:"sizes,omitempty"`
}

type shopSize struct {
	ID        string            `json:"id"`
	Variant   map[string]string `json:"variant"`
	PriceKobo *int64            `json:"price_kobo,omitempty"`
	Available bool              `json:"available"`
	FewLeft   *int64            `json:"few_left,omitempty"`
}

func fewOf(stock *int64) *int64 {
	if stock != nil && *stock > 0 && *stock <= fewLeft {
		return stock
	}
	return nil
}

func available(it storefront.Item) bool {
	return it.Available && (it.Stock == nil || *it.Stock > 0)
}

// theirShop is what the business's shop offers for ordering in the app.
type theirShop struct {
	Items []shopItem `json:"items"`
	// Open: orders are taken in the app now; Closed says why not.
	Open   bool   `json:"open"`
	Closed string `json:"closed,omitempty"`
	// How an order is had: pickup, delivery (to one of Zones, its fee
	// added), and when it is paid (at_order: at once, by its pay link;
	// after_accept: once they accept it; none).
	Pickup   bool              `json:"pickup"`
	Delivery bool              `json:"delivery"`
	Zones    []storefront.Zone `json:"zones"`
	Payment  string            `json:"payment"`
	action   *uuid.UUID
	slug     string
	sf       uuid.UUID
	all      []storefront.Item
}

// theirShopTx reads the business's shop, in its scope (through the door).
func theirShopTx(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, name string) (theirShop, error) {
	shop := theirShop{Items: []shopItem{}, Zones: []storefront.Zone{}, Payment: "none"}
	sf, err := storefront.Get(ctx, tx, tenant)
	if errors.Is(err, storefront.ErrNotFound) {
		shop.Closed = name + " has no shop in Taskiem yet. Ask them here in the chat."
		return shop, nil
	}
	if err != nil {
		return shop, err
	}
	if sf.Status != "published" {
		shop.Closed = name + " has no shop online right now. Ask them here in the chat."
		return shop, nil
	}
	shop.sf, shop.slug = sf.ID, sf.Slug
	if sf.PublishedSlug != nil && *sf.PublishedSlug != "" {
		shop.slug = *sf.PublishedSlug
	}
	items, err := storefront.Catalogue(ctx, tx, tenant)
	if err != nil {
		return shop, err
	}
	shop.all = items
	for _, it := range items {
		if it.VariantOf != "" || it.DurationMinutes > 0 {
			continue
		}
		x := shopItem{ID: it.ID, Name: it.Name, PriceKobo: it.PriceKobo, Description: it.Description, Category: it.Category, Photo: it.Photo["800"],
			Available: available(it), FewLeft: fewOf(it.Stock)}
		for _, sz := range it.Sizes {
			price := sz.PriceKobo
			if price == nil {
				price = it.PriceKobo
			}
			x.Sizes = append(x.Sizes, shopSize{ID: sz.ID, Variant: sz.Variant, PriceKobo: price, Available: sz.Stock == nil || *sz.Stock > 0, FewLeft: fewOf(sz.Stock)})
		}
		shop.Items = append(shop.Items, x)
	}
	var id uuid.UUID
	var on bool
	var payment string
	var raw []byte
	err = tx.QueryRow(ctx, `SELECT a.id, a.enabled AND COALESCE(f.enabled, false), a.payment, a.options FROM storefront_actions a
		LEFT JOIN storefront_fronts f ON f.id = a.front_id WHERE a.storefront_id = $1 AND a.kind = 'order' AND a.builtin`, sf.ID).Scan(&id, &on, &payment, &raw)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return shop, err
	}
	if err == nil && on && sf.OrdersPaused == nil {
		var opts storefront.ActionOptions
		_ = json.Unmarshal(raw, &opts)
		shop.action, shop.Open, shop.Payment = &id, true, payment
		shop.Pickup, shop.Delivery = opts.Pickup, opts.Delivery
		if opts.Delivery && opts.Zones != nil {
			shop.Zones = opts.Zones
		}
	}
	if !shop.Open {
		shop.Closed = name + " does not take orders in the app right now. Ask them here in the chat."
	}
	return shop, nil
}

// doorShop is GET /v1/me/chats/{id}/shop: their shop.
func (s *Server) doorShop(w http.ResponseWriter, r *http.Request) {
	person, err := personOf(r)
	if err != nil {
		s.personFail(w, r, err)
		return
	}
	var shop theirShop
	err = s.doorOnce(r.Context(), person, chi.URLParam(r, "id"), "customer.shop", nil, func(tx pgx.Tx, c personChat) error {
		if err := nameOneTx(r.Context(), tx, &c); err != nil {
			return err
		}
		var err error
		shop, err = theirShopTx(r.Context(), tx, c.Business, c.Name)
		return err
	})
	if err != nil {
		s.personFail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, shop)
}

// doorServices is GET /v1/me/chats/{id}/services: the services they
// book, as their page shows them, and whether they take bookings now.
func (s *Server) doorServices(w http.ResponseWriter, r *http.Request) {
	person, err := personOf(r)
	if err != nil {
		s.personFail(w, r, err)
		return
	}
	var b theirBooking
	err = s.doorOnce(r.Context(), person, chi.URLParam(r, "id"), "customer.book", nil, func(tx pgx.Tx, c personChat) error {
		if err := nameOneTx(r.Context(), tx, &c); err != nil {
			return err
		}
		var err error
		b, err = theirBookingTx(r.Context(), tx, c.Business, c.Name)
		return err
	})
	if err != nil {
		s.personFail(w, r, err)
		return
	}
	out := map[string]any{"services": []linkService{}, "open": b.open && s.bookingsRecorded()}
	list := []linkService{}
	for _, it := range b.services {
		list = append(list, serviceOut(it))
	}
	out["services"] = list
	if out["open"] != true {
		out["closed"] = b.name + " does not take bookings in the app right now. Ask them here in the chat."
	}
	writeJSON(w, http.StatusOK, out)
}

// doorServiceTimes is GET /v1/me/chats/{id}/services/{service}/times?day=:
// the days with a free time, and one day's free times; never whose.
func (s *Server) doorServiceTimes(w http.ResponseWriter, r *http.Request) {
	person, err := personOf(r)
	if err != nil {
		s.personFail(w, r, err)
		return
	}
	day := r.URL.Query().Get("day")
	if day != "" {
		if _, err := time.ParseInLocation(time.DateOnly, day, storefront.Lagos); err != nil {
			writeErr(w, http.StatusBadRequest, "day is a date like 2026-11-13")
			return
		}
	}
	id := chi.URLParam(r, "service")
	out := linkTimesOut{Days: []string{}, Times: []time.Time{}}
	err = s.doorOnce(r.Context(), person, chi.URLParam(r, "id"), "customer.book", map[string]any{"service": id}, func(tx pgx.Tx, c personChat) error {
		b, err := theirBookingTx(r.Context(), tx, c.Business, "")
		if err != nil {
			return err
		}
		it, svc, ok := findLinkService(b.services, id)
		if !ok {
			return errDoorNone
		}
		out.Service = serviceOut(it)
		now := time.Now()
		from, to := bookingWindow(b.avail, now)
		busy, err := busyTx(r.Context(), tx, b.storefront, from, to)
		if err != nil {
			return err
		}
		for _, date := range b.avail.Dates(now) {
			slots := b.avail.Slots(svc, date, now, busy)
			if len(slots) == 0 {
				continue
			}
			key := date.Format(time.DateOnly)
			if key == day || (day == "" && len(out.Days) == 0) {
				out.Day = key
				for _, t := range slots {
					out.Times = append(out.Times, t.UTC())
				}
			}
			out.Days = append(out.Days, key)
		}
		return nil
	})
	if err != nil {
		s.personFail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// doorOrder is one of the person's orders or bookings with the business.
type doorOrder struct {
	Reference   string     `json:"reference"`
	Kind        string     `json:"kind"` // order or booking
	Status      string     `json:"status"`
	Summary     string     `json:"summary"`
	TotalKobo   int64      `json:"total_kobo"`
	DueKobo     int64      `json:"due_kobo"`
	ServiceName string     `json:"service_name,omitempty"`
	StartsAt    *time.Time `json:"starts_at,omitempty"`
	// Pay: something is due now; GET .../orders/{reference}/pay is its link.
	Pay bool `json:"pay"`
	// Delivery: the rider's progress, once a rider has taken it.
	Delivery  *deliveryProgress `json:"delivery,omitempty"`
	CreatedAt time.Time         `json:"created_at"`
}

// deliveryProgress is a delivery as its customer follows it: the rider's
// step (accepted, picked_up, arrived, delivered), in words, with when each
// step was reached. Nothing of the rider is shown.
type deliveryProgress struct {
	Status      string     `json:"status"`
	Line        string     `json:"line"`
	AcceptedAt  *time.Time `json:"accepted_at,omitempty"`
	PickedUpAt  *time.Time `json:"picked_up_at,omitempty"`
	ArrivedAt   *time.Time `json:"arrived_at,omitempty"`
	DeliveredAt *time.Time `json:"delivered_at,omitempty"`
}

// deliveryLines are the rider's steps in a customer's words.
var deliveryLines = map[string]string{
	"accepted":  "A rider is going to collect it",
	"picked_up": "On its way to you",
	"arrived":   "The rider is at your address",
	"delivered": "Delivered",
}

// deliveryProgressTx is an order's delivery as its customer follows it,
// nil until a rider holds it; the business (or the door into it) in scope.
func deliveryProgressTx(ctx context.Context, tx pgx.Tx, tenant, order uuid.UUID) (*deliveryProgress, error) {
	var d deliveryProgress
	err := tx.QueryRow(ctx, `SELECT status, accepted_at, picked_up_at, arrived_at, delivered_at FROM deliveries
		WHERE tenant_id = $1 AND order_id = $2 AND status IN ('accepted', 'picked_up', 'arrived', 'delivered')`, tenant, order).
		Scan(&d.Status, &d.AcceptedAt, &d.PickedUpAt, &d.ArrivedAt, &d.DeliveredAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	d.Line = deliveryLines[d.Status]
	return &d, nil
}

func doorOrderOf(o storefront.Order) doorOrder {
	kind := "order"
	if o.Kind == "booking" {
		kind = "booking"
	}
	x := doorOrder{Reference: o.Reference, Kind: kind, Status: o.Status, Summary: storefront.Summary(o.Basket), TotalKobo: o.DueKobo(), DueKobo: o.BalanceKobo(),
		ServiceName: o.ServiceName, StartsAt: o.StartsAt, CreatedAt: o.CreatedAt}
	if kind == "booking" {
		x.Summary = o.ServiceName
	}
	x.Pay = x.DueKobo > 0 && o.Payment != "none" && (o.Status == "awaiting_payment" || o.Status == "accepted" || o.Status == "received" || o.Status == "paid")
	return x
}

// doorOrders is GET /v1/me/chats/{id}/orders: the person's own orders and
// bookings with the business, placed in the app, newest first.
func (s *Server) doorOrders(w http.ResponseWriter, r *http.Request) {
	person, err := personOf(r)
	if err != nil {
		s.personFail(w, r, err)
		return
	}
	if s.Orders == nil {
		writeJSON(w, http.StatusOK, map[string]any{"orders": []doorOrder{}})
		return
	}
	out := []doorOrder{}
	err = s.doorOnce(r.Context(), person, chi.URLParam(r, "id"), "customer.orders", nil, func(tx pgx.Tx, c personChat) error {
		rows, err := tx.Query(r.Context(), `SELECT reference FROM storefront_orders WHERE tenant_id = $1 AND person_id = $2 ORDER BY created_at DESC LIMIT 50`, c.Business, person)
		if err != nil {
			return err
		}
		refs, err := pgx.CollectRows(rows, pgx.RowTo[string])
		if err != nil {
			return err
		}
		for _, ref := range refs {
			o, err := s.Orders.GetOrder(r.Context(), tx, c.Business, ref)
			if err != nil {
				return err
			}
			x := doorOrderOf(o)
			if o.Fulfilment == "delivery" {
				if x.Delivery, err = deliveryProgressTx(r.Context(), tx, c.Business, o.ID); err != nil {
					return err
				}
			}
			out = append(out, x)
		}
		return nil
	})
	if err != nil {
		s.personFail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"orders": out})
}

// doorPay is GET /v1/me/chats/{id}/orders/{reference}/pay: the pay link
// of one of the person's orders or bookings, while something is due.
func (s *Server) doorPay(w http.ResponseWriter, r *http.Request) {
	person, err := personOf(r)
	if err != nil {
		s.personFail(w, r, err)
		return
	}
	if s.Orders == nil {
		s.personFail(w, r, errDoorNone)
		return
	}
	ref := chi.URLParam(r, "reference")
	var link string
	var due int64
	err = s.doorOnce(r.Context(), person, chi.URLParam(r, "id"), "customer.pay", map[string]any{"reference": ref}, func(tx pgx.Tx, c personChat) error {
		var mine bool
		if err := tx.QueryRow(r.Context(), `SELECT person_id IS NOT DISTINCT FROM $3 FROM storefront_orders WHERE tenant_id = $1 AND reference = $2`,
			c.Business, ref, person).Scan(&mine); err != nil {
			return err
		}
		if !mine {
			return errDoorNone
		}
		o, err := s.Orders.GetOrder(r.Context(), tx, c.Business, ref)
		if err != nil {
			return err
		}
		if due = o.BalanceKobo(); due <= 0 {
			return customerSendErr{http.StatusConflict, "Nothing is due on this order."}
		}
		if o.Payment == "none" {
			return customerSendErr{http.StatusConflict, "They take payment for this outside the app. Ask them here how to pay."}
		}
		if o.Status == "received" && o.Payment == "after_accept" {
			return customerSendErr{http.StatusConflict, "You pay once they accept your order. They will tell you here."}
		}
		if link, err = s.Orders.ChatStatusLink(r.Context(), tx, c.Business, o); err != nil {
			return err
		}
		if link == "" {
			return customerSendErr{http.StatusConflict, "This order cannot be paid in the app. Ask them here in the chat."}
		}
		return nil
	})
	if err != nil {
		s.personFail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"reference": ref, "url": link, "due_kobo": due})
}

// orderLine is a line of an order placed in the app: an item, or one of a
// product's sizes and colours, and how many.
type orderLine struct {
	ItemID   string `json:"item_id"`
	Quantity int    `json:"quantity"`
}

// Refusals of an order placed in the app.
var (
	errAppOrdersClosed = errors.New("orders closed")
	errAppItem         = errors.New("an item")
)

// doorPlaceOrder is POST /v1/me/chats/{id}/orders, with an
// Idempotency-Key: {lines, fulfilment, zone, address, note}. The order is
// priced from their shop as it is now, and goes into their storefront's
// inbox as an order of the kind app, linked to the person, under the
// person's name and phone; their order workflow takes it from there. A
// repeat of the same key answers the first order. The conversation shows
// the order as a card, to both sides.
func (s *Server) doorPlaceOrder(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	person, err := personOf(r)
	if err != nil {
		s.personFail(w, r, err)
		return
	}
	key, err := requiredKey(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	var in struct {
		Lines      []orderLine `json:"lines"`
		Fulfilment string      `json:"fulfilment"`
		Zone       string      `json:"zone"`
		Address    string      `json:"address"`
		Note       string      `json:"note"`
	}
	if err := decodeBody(r, &in); err != nil {
		s.fail(w, r, err)
		return
	}
	in.Address, in.Note = strings.TrimSpace(in.Address), strings.TrimSpace(in.Note)
	switch {
	case len(in.Lines) == 0 || len(in.Lines) > 50:
		writeErr(w, http.StatusBadRequest, "an order has 1 to 50 lines")
		return
	case len([]rune(in.Address)) > 300, len([]rune(in.Note)) > 500:
		writeErr(w, http.StatusBadRequest, "the address is up to 300 characters, a note up to 500")
		return
	}
	if s.Orders == nil || len(s.Orders.InboxKey) == 0 {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "Orders cannot be placed here yet.", "code": "orders_closed"})
		return
	}
	var phone *string
	if err := s.Store.Pool.QueryRow(ctx, `SELECT taskiem_auth_phone_of($1)`, person).Scan(&phone); err != nil {
		s.fail(w, r, err)
		return
	}
	if phone == nil {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "Add your phone number first: the business needs it for your order.", "code": "phone_needed"})
		return
	}
	var out doorOrder
	var first bool
	for range 3 {
		out, first, err = s.placeAppOrder(ctx, person, chi.URLParam(r, "id"), key, *phone, in.Lines, in.Fulfilment, in.Zone, in.Address, in.Note)
		if !errors.Is(err, errReferenceTaken) {
			break
		}
	}
	var se customerSendErr
	switch {
	case errors.Is(err, errAppOrdersClosed):
		writeJSON(w, http.StatusConflict, map[string]string{"error": "They do not take orders in the app right now. Ask them here in the chat.", "code": "orders_closed"})
		return
	case errors.As(err, &se):
		writeJSON(w, se.code, map[string]string{"error": se.msg, "code": "order_refused"})
		return
	case err != nil:
		s.personFail(w, r, err)
		return
	}
	code := http.StatusCreated
	if !first {
		code = http.StatusOK
	}
	writeJSON(w, code, map[string]any{"order": out})
}

// placeAppOrder places an order in one transaction through the door: the
// shop read as it is now, the lines priced, the sealed submission into
// the inbox, and the order's card in both copies of the conversation.
func (s *Server) placeAppOrder(ctx context.Context, person uuid.UUID, chatID, key, phone string, lines []orderLine, fulfilment, zone, address, note string) (doorOrder, bool, error) {
	var out doorOrder
	first := false
	err := s.doorOnce(ctx, person, chatID, "customer.order", map[string]any{"key": key}, func(tx pgx.Tx, c personChat) error {
		// One request of a key at a time: a repeat sent at once waits for
		// the first, then finds its order.
		if err := idemLockTx(ctx, tx, "app-order:"+person.String()+":"+c.Business.String()+":"+key); err != nil {
			return err
		}
		// The same request again: the order it placed, by its card.
		if again, err := personRepeat(ctx, tx, person, c.ID, "order:"+key); err != nil || again != nil {
			if again != nil && again.Card != nil {
				out = doorOrder{Reference: again.Card.Reference, Kind: "order", Status: "sent", Summary: again.Card.Line, CreatedAt: again.CreatedAt}
				if again.Card.TotalKobo != nil {
					out.TotalKobo = *again.Card.TotalKobo
				}
			}
			return err
		}
		if err := nameOneTx(ctx, tx, &c); err != nil {
			return err
		}
		shop, err := theirShopTx(ctx, tx, c.Business, c.Name)
		if err != nil {
			return err
		}
		if !shop.Open || shop.action == nil {
			return errAppOrdersClosed
		}
		byID := map[string]storefront.Item{}
		parent := map[string]storefront.Item{}
		for _, it := range shop.all {
			byID[it.ID] = it
		}
		for _, it := range shop.all {
			if it.VariantOf != "" {
				parent[it.ID] = byID[it.VariantOf]
			}
		}
		basket := make([]storefront.Line, 0, len(lines))
		wanted := map[string]int64{}
		var total int64
		for _, l := range lines {
			it, ok := byID[l.ItemID]
			if !ok || it.DurationMinutes > 0 || it.Variants > 0 {
				return customerSendErr{http.StatusUnprocessableEntity, "One of the items is not in their shop now (choose a size where there are sizes)."}
			}
			if l.Quantity < 1 || l.Quantity > 99 {
				return customerSendErr{http.StatusBadRequest, "Each line is 1 to 99 of an item."}
			}
			price := it.PriceKobo
			name := it.Name
			if p, ok := parent[it.ID]; ok {
				if price == nil {
					price = p.PriceKobo
				}
				if name == "" || name == p.Name {
					var vs []string
					for _, v := range it.Variant {
						vs = append(vs, v)
					}
					name = strings.TrimSpace(p.Name + " " + strings.Join(vs, " "))
				}
			}
			if !available(it) || price == nil || *price < 1 {
				return customerSendErr{http.StatusUnprocessableEntity, name + " is not available now."}
			}
			// The same item on two lines counts as one.
			wanted[it.ID] += int64(l.Quantity)
			if it.Stock != nil && wanted[it.ID] > *it.Stock {
				return customerSendErr{http.StatusUnprocessableEntity, fmt.Sprintf("Only %d of %s left.", *it.Stock, name)}
			}
			line := storefront.Line{ID: it.ID, Name: name, Quantity: l.Quantity, PriceKobo: *price, LineKobo: int64(l.Quantity) * *price}
			basket = append(basket, line)
			total += line.LineKobo
		}
		var fee int64
		switch {
		case fulfilment == "delivery" && shop.Delivery:
			if address == "" {
				return customerSendErr{http.StatusBadRequest, "Give the address to deliver to."}
			}
			if len(shop.Zones) > 0 {
				found := false
				for _, z := range shop.Zones {
					if z.Name == zone {
						fee, found = z.FeeKobo, true
					}
				}
				if !found {
					return customerSendErr{http.StatusBadRequest, "Choose one of the areas they deliver to."}
				}
			} else {
				zone = ""
			}
		case fulfilment == "pickup" && shop.Pickup:
			zone, address = "", ""
		case fulfilment == "" && !shop.Pickup && !shop.Delivery:
		default:
			return customerSendErr{http.StatusBadRequest, "Choose pickup or delivery, as they offer it."}
		}
		total += fee
		var name string
		if err := tx.QueryRow(ctx, `SELECT name FROM users WHERE id = $1`, person).Scan(&name); err != nil {
			return err
		}
		ref := storefront.NewReference(shop.slug)
		statusKey, hash := storefront.NewStatusKey()
		sub := storefront.Submission{Kind: "order", Customer: storefront.Customer{Name: name, Phone: phone, Address: address, Message: note},
			Basket: basket, TotalKobo: total, Fulfilment: fulfilment, Zone: zone, DeliveryKobo: fee, Payment: shop.Payment, Channel: "app",
			PersonID: person.String(), StatusKey: statusKey, Submitted: time.Now().UTC()}
		payload, err := storefront.SealSubmission(s.Orders.InboxKey, shop.sf, ref, sub)
		if err != nil {
			return err
		}
		idem := sha256.Sum256([]byte("app-order:" + person.String() + ":" + c.Business.String() + ":" + key))
		var got *string
		err = tx.QueryRow(ctx, `SELECT taskiem_customer_order($1, $2, $3, $4, $5)`, *shop.action, ref, idem[:], hash, payload).Scan(&got)
		var pe *pgconn.PgError
		if errors.As(err, &pe) && pe.Code == "23505" && pe.ConstraintName == "storefront_inbox_reference_key" {
			return errReferenceTaken
		}
		if err != nil {
			return err
		}
		if got == nil {
			return errAppOrdersClosed
		}
		out = doorOrder{Reference: *got, Kind: "order", Status: "sent", Summary: storefront.Summary(basket), TotalKobo: total, CreatedAt: time.Now().UTC()}
		first = *got == ref
		if !first {
			return nil
		}
		return s.doorCardTx(ctx, tx, c, person, "order:"+key, customerCard{Type: "order", Reference: ref, Title: "Order " + ref,
			Line: storefront.Summary(basket), Status: "received", TotalKobo: &total})
	})
	return out, first, err
}

// idemLockTx holds an Idempotency-Key until the transaction ends.
func idemLockTx(ctx context.Context, tx pgx.Tx, key string) error {
	_, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, key)
	return err
}

// doorCardTx writes a card the person's own action made (an order, a
// booking) into both copies of the conversation, the door open.
func (s *Server) doorCardTx(ctx context.Context, tx pgx.Tx, c personChat, person uuid.UUID, clientID string, card customerCard) error {
	var thread uuid.UUID
	var subject string
	if err := tx.QueryRow(ctx, `SELECT id, subject_id FROM customer_conversations WHERE tenant_id = $1 AND person_id = $2 AND line = 'app'`,
		c.Business, person).Scan(&thread, &subject); err != nil {
		return err
	}
	if again, err := personRepeat(ctx, tx, person, c.ID, clientID); err != nil || again != nil {
		return err
	}
	text := card.Title
	if card.Line != "" {
		text += ": " + card.Line
	}
	_, _, err := s.writeAppMessageTx(ctx, tx, appMessage{id: uuid.Must(uuid.NewV7()), tenant: c.Business, thread: thread, subject: subject, person: person,
		fromPerson: true, kind: "card", text: text, card: &card, clientID: clientID})
	return err
}

// doorBook is POST /v1/me/chats/{id}/bookings, with an Idempotency-Key:
// {service_id, start, note} books one of their services at a free time,
// as their page's booking would, under the person's name and phone, and
// linked to them. A repeat of the key answers the first booking.
func (s *Server) doorBook(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	person, err := personOf(r)
	if err != nil {
		s.personFail(w, r, err)
		return
	}
	key, err := requiredKey(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	var in struct {
		ServiceID string    `json:"service_id"`
		Start     time.Time `json:"start"`
		Note      string    `json:"note"`
	}
	if err := decodeBody(r, &in); err != nil {
		s.fail(w, r, err)
		return
	}
	in.Note = strings.TrimSpace(in.Note)
	if len([]rune(in.Note)) > 500 {
		writeErr(w, http.StatusBadRequest, "a note is at most 500 characters")
		return
	}
	if !s.bookingsRecorded() {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "Bookings cannot be made here yet.", "code": "bookings_closed"})
		return
	}
	var phone *string
	if err := s.Store.Pool.QueryRow(ctx, `SELECT taskiem_auth_phone_of($1)`, person).Scan(&phone); err != nil {
		s.fail(w, r, err)
		return
	}
	if phone == nil {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "Add your phone number first: the business needs it for your booking.", "code": "phone_needed"})
		return
	}
	var got linkBooking
	var first bool
	for range 3 {
		got, first, err = s.bookInApp(ctx, person, chi.URLParam(r, "id"), key, *phone, in.ServiceID, in.Start, in.Note)
		if !errors.Is(err, errReferenceTaken) {
			break
		}
	}
	var pe *pgconn.PgError
	switch {
	case errors.As(err, &pe) && pe.Code == "23P01", errors.Is(err, errBookingTaken):
		writeJSON(w, http.StatusConflict, map[string]string{"error": "That time was just taken. Please choose another.", "code": "slot_taken"})
		return
	case errors.Is(err, errBookingTime):
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": "Please choose one of the times shown.", "code": "pick_time"})
		return
	case errors.Is(err, errBookingClosed):
		writeJSON(w, http.StatusConflict, map[string]string{"error": "They do not take bookings in the app right now. Ask them here in the chat.", "code": "bookings_closed"})
		return
	case err != nil:
		s.personFail(w, r, err)
		return
	}
	code := http.StatusCreated
	if !first {
		code = http.StatusOK
	}
	writeJSON(w, code, map[string]any{"booking": got})
}

// bookInApp books through the door in one transaction: the service and
// the time checked against their calendar as it is now, the slot held and
// the sealed booking into their inbox (taskiem_link_book, which acts for
// the business in scope), and the booking's card in the conversation.
func (s *Server) bookInApp(ctx context.Context, person uuid.UUID, chatID, key, phone, serviceID string, start time.Time, note string) (linkBooking, bool, error) {
	var got linkBooking
	first := false
	err := s.doorOnce(ctx, person, chatID, "customer.booking", map[string]any{"key": key, "service": serviceID}, func(tx pgx.Tx, c personChat) error {
		// One request of a key at a time: a repeat sent at once waits for
		// the first, then finds its booking (rather than its slot taken).
		if err := idemLockTx(ctx, tx, "app-booking:"+person.String()+":"+c.Business.String()+":"+key); err != nil {
			return err
		}
		// The same request again: the booking it made, by its card.
		if again, err := personRepeat(ctx, tx, person, c.ID, "booking:"+key); err != nil || again != nil {
			if again != nil && again.Card != nil {
				got = linkBooking{Reference: again.Card.Reference, ServiceID: again.Card.ServiceID, PriceKobo: again.Card.TotalKobo}
			}
			return err
		}
		idem := sha256.Sum256([]byte("app-booking:" + person.String() + ":" + c.Business.String() + ":" + key))
		b, err := theirBookingTx(ctx, tx, c.Business, "")
		if err != nil {
			return err
		}
		it, svc, ok := findLinkService(b.services, serviceID)
		if !ok {
			return errDoorNone
		}
		if !b.open || b.action == nil {
			return errBookingClosed
		}
		now := time.Now()
		if !b.avail.Offers(svc, start, now, nil) {
			return errBookingTime
		}
		var payment string
		if err := tx.QueryRow(ctx, `SELECT payment FROM storefront_actions WHERE id = $1`, *b.action).Scan(&payment); err != nil {
			return err
		}
		var name string
		if err := tx.QueryRow(ctx, `SELECT name FROM users WHERE id = $1`, person).Scan(&name); err != nil {
			return err
		}
		r := storefront.NewReference(b.slug)
		got = linkBooking{Reference: r, ServiceID: it.ID, ServiceName: it.Name, StartsAt: start.UTC(), EndsAt: start.Add(svc.Duration).UTC()}
		if it.PriceKobo != nil && *it.PriceKobo > 0 {
			got.PriceKobo = it.PriceKobo
		}
		statusKey, hash := storefront.NewStatusKey()
		sub := storefront.Submission{Kind: "booking", Customer: storefront.Customer{Name: name, Phone: phone, Message: note}, Payment: payment, Channel: "app",
			PersonID: person.String(), StatusKey: statusKey, Submitted: now.UTC(),
			Booking: &storefront.Booked{ServiceID: it.ID, ServiceName: it.Name, Start: start.UTC(), End: start.Add(svc.Duration).UTC(),
				Until: start.Add(svc.Duration + svc.Buffer).UTC(), Capacity: svc.Capacity}}
		if got.PriceKobo != nil {
			sub.TotalKobo = *got.PriceKobo
		}
		payload, err := storefront.SealSubmission(s.Orders.InboxKey, b.storefront, r, sub)
		if err != nil {
			return err
		}
		var booked *string
		err = tx.QueryRow(ctx, `SELECT taskiem_link_book($1, $2, $3, $4, $5, $6, $7, $8, $9)`, *b.action, r, idem[:], hash, payload,
			it.ID, sub.Booking.Start, sub.Booking.Until, svc.Capacity).Scan(&booked)
		var pe *pgconn.PgError
		if errors.As(err, &pe) && pe.Code == "23505" && pe.ConstraintName == "storefront_inbox_reference_key" {
			return errReferenceTaken
		}
		if err != nil {
			return err
		}
		if booked == nil {
			return errBookingClosed
		}
		if *booked != r {
			// The same request again: the first booking stands.
			got.Reference = *booked
			return nil
		}
		first = true
		when := start.In(storefront.Lagos)
		return s.doorCardTx(ctx, tx, c, person, "booking:"+key, customerCard{Type: "booking", Reference: r, Title: "Booking " + r,
			Line: it.Name + ", " + when.Format("Mon 2 Jan") + " at " + when.Format("15:04"), Status: "received", ServiceID: it.ID, TotalKobo: got.PriceKobo})
	})
	return got, first, err
}
