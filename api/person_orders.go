package api

import (
	"context"
	"encoding/base64"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/storefront"
)

// Orders on You (docs/spec/taskiem-for-everyone.md §7, §13; migration
// 00465): every order and booking a person has with any business, newest
// first, paged. Two sources, each order once: those made in the app or
// the browser chat while signed in (storefront_orders.person_id), and the
// storefront orders and bookings made with their verified phone (the
// phone index) once they are theirs: made after the phone was on the
// account, or confirmed with "These are mine". Those still waiting are
// only counted (waiting_count).
//
// The list reaches across businesses only through taskiem_person_orders,
// which answers exactly what a row shows, for the person in scope. One
// order's detail and paying it go through taskiem_person_order_enter, a
// narrow door to one order of the person's in one business, audited in
// that business's chain (customer.order_detail, customer.pay); it needs
// no conversation with the business.

// orderPhoto is a business's photo, its sizes' addresses (as the chat's
// header has it: photo.urls).
type orderPhoto struct {
	URLs map[string]string `json:"urls"`
}

// personOrder is one row of Orders on You.
type personOrder struct {
	BusinessID uuid.UUID   `json:"business_id"`
	Business   string      `json:"business"`
	Photo      *orderPhoto `json:"photo,omitempty"`
	Reference  string      `json:"reference"`
	// Kind: order or booking (a service, a hire, a space, a class or
	// tickets: BookingType says which).
	Kind        string `json:"kind"`
	BookingType string `json:"booking_type,omitempty"`
	// State: the order's status, as the business's order has it.
	State string `json:"state"`
	// Summary: "2 × Jollof rice, 1 × Chapman", or a booking's service.
	Summary   string `json:"summary"`
	TotalKobo int64  `json:"total_kobo"`
	DueKobo   int64  `json:"due_kobo"`
	// Pay: something is due now and can be paid in the app;
	// GET /v1/me/orders/{business_id}/{reference}/pay is its link.
	Pay    bool      `json:"pay"`
	MadeAt time.Time `json:"made_at"`
	// Earlier: a storefront order found through the person's phone, made
	// before the phone was verified on this account (founder's decision
	// A): shown short only (business, reference, state, total, date),
	// with no summary, nothing due and no Pay, even after "These are
	// mine"; its detail is only this row, and it cannot be paid here.
	// "Chat about this" is how the person asks the business about it.
	Earlier bool `json:"earlier,omitempty"`
	// ChatID: the person's conversation with the business, when there is
	// one ("Chat about this"); else POST /v1/me/chats {business_id,
	// from: "order"} starts it.
	ChatID *uuid.UUID `json:"chat_id"`
}

// personOrderLine is a line of one order, as its customer sees it.
type personOrderLine struct {
	Name      string `json:"name"`
	Quantity  int    `json:"quantity"`
	PriceKobo int64  `json:"price_kobo"`
	LineKobo  int64  `json:"line_kobo"`
}

// personOrderAddress is the address on the order (copied there when it
// was placed): never the person's saved list.
type personOrderAddress struct {
	Address  string `json:"address"`
	Landmark string `json:"landmark,omitempty"`
}

// personOrderDetail is one order: its row, and what it holds.
type personOrderDetail struct {
	personOrder
	Lines        []personOrderLine   `json:"lines"`
	Fulfilment   string              `json:"fulfilment,omitempty"`
	Zone         string              `json:"zone,omitempty"`
	DeliveryKobo int64               `json:"delivery_kobo,omitempty"`
	PaidKobo     int64               `json:"paid_kobo"`
	Address      *personOrderAddress `json:"address,omitempty"`
	ServiceName  string              `json:"service_name,omitempty"`
	StartsAt     *time.Time          `json:"starts_at,omitempty"`
	EndsAt       *time.Time          `json:"ends_at,omitempty"`
	// Delivery: the rider's progress, once a rider holds it.
	Delivery *deliveryProgress `json:"delivery,omitempty"`
}

// personOrderRoutes are the person's routes (personalRoutes lists them).
func (s *Server) personOrderRoutes(r chi.Router) {
	r.Get("/me/orders", s.myOrders)
	r.Get("/me/orders/{business}/{reference}", s.myOrder)
	r.Get("/me/orders/{business}/{reference}/pay", s.myOrderPay)
}

// orderCursor is a page's place: the last row's date and reference.
func orderCursor(o personOrder) string {
	return base64.RawURLEncoding.EncodeToString([]byte(o.MadeAt.UTC().Format(time.RFC3339Nano) + "|" + o.Reference))
}

func readOrderCursor(c string) (*time.Time, *string, error) {
	if c == "" {
		return nil, nil, nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(c)
	if err != nil {
		return nil, nil, errBadRequest
	}
	at, ref, ok := strings.Cut(string(raw), "|")
	t, err := time.Parse(time.RFC3339Nano, at)
	if !ok || err != nil || ref == "" {
		return nil, nil, errBadRequest
	}
	return &t, &ref, nil
}

// personOrdersTx reads the person's orders (one business's, one order,
// after a cursor) through taskiem_person_orders.
func (s *Server) personOrdersTx(ctx context.Context, tx pgx.Tx, person uuid.UUID, business *uuid.UUID, ref *string, beforeAt *time.Time, beforeRef *string, limit int) ([]personOrder, error) {
	rows, err := tx.Query(ctx, `SELECT business_id, business, photo, reference, kind, booking_type, state, summary, total_kobo, due_kobo, pay, made_at, earlier
		FROM taskiem_person_orders($1, $2, $3, $4, $5, $6, $7)`, person, s.PhoneIndex.IndexKey(), business, ref, beforeAt, beforeRef, limit)
	if err != nil {
		return nil, err
	}
	list, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (personOrder, error) {
		var o personOrder
		var photo map[string]string
		var bt *string
		err := r.Scan(&o.BusinessID, &o.Business, &photo, &o.Reference, &o.Kind, &bt, &o.State, &o.Summary, &o.TotalKobo, &o.DueKobo, &o.Pay, &o.MadeAt, &o.Earlier)
		if bt != nil {
			o.BookingType = *bt
		}
		if len(photo) > 0 {
			o.Photo = &orderPhoto{URLs: photo}
		}
		return o, err
	})
	if err != nil {
		return nil, err
	}
	// "Chat about this": the person's own conversations, in their scope.
	ids := make([]uuid.UUID, 0, len(list))
	for _, o := range list {
		ids = append(ids, o.BusinessID)
	}
	chats := map[uuid.UUID]uuid.UUID{}
	if len(ids) > 0 {
		rows, err := tx.Query(ctx, `SELECT tenant_id, id FROM person_conversations WHERE person_id = $1 AND tenant_id = ANY ($2)`, person, ids)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var b, c uuid.UUID
			if err := rows.Scan(&b, &c); err != nil {
				rows.Close()
				return nil, err
			}
			chats[b] = c
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return nil, err
		}
	}
	for i := range list {
		if c, ok := chats[list[i].BusinessID]; ok {
			list[i].ChatID = &c
		}
	}
	return list, nil
}

// myOrders is GET /v1/me/orders?before=&limit=: Orders on You, newest
// first; next is the cursor of the next page (null on the last).
// waiting_count is how many orders made with the person's phone before it
// was on their account wait for "These are mine" (POST
// /v1/me/storefront-orders/confirm): nothing else of them is shown.
func (s *Server) myOrders(w http.ResponseWriter, r *http.Request) {
	person, err := personOf(r)
	if err != nil {
		s.personFail(w, r, err)
		return
	}
	limit := 30
	if v := r.URL.Query().Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 100 {
			writeErr(w, http.StatusBadRequest, "limit is 1 to 100")
			return
		}
		limit = n
	}
	beforeAt, beforeRef, err := readOrderCursor(r.URL.Query().Get("before"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "before is the next cursor of an earlier page")
		return
	}
	var list []personOrder
	waiting := 0
	err = db.InPersonTx(r.Context(), s.Store.Pool, person, func(tx pgx.Tx) error {
		var err error
		if list, err = s.personOrdersTx(r.Context(), tx, person, nil, nil, beforeAt, beforeRef, limit+1); err != nil {
			return err
		}
		if key := s.PhoneIndex.IndexKey(); key != nil {
			return tx.QueryRow(r.Context(), `SELECT taskiem_customer_phone_orders_waiting($1, $2)`, person, key).Scan(&waiting)
		}
		return nil
	})
	if err != nil {
		s.personFail(w, r, err)
		return
	}
	var next *string
	if len(list) > limit {
		list = list[:limit]
		c := orderCursor(list[limit-1])
		next = &c
	}
	if list == nil {
		list = []personOrder{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"orders": list, "next": next, "waiting_count": waiting})
}

// orderPath is the business and reference of /v1/me/orders/{business}/{reference}.
func orderPath(r *http.Request) (uuid.UUID, string, error) {
	b, err := uuid.Parse(chi.URLParam(r, "business"))
	ref := chi.URLParam(r, "reference")
	if err != nil || ref == "" || len(ref) > 80 {
		return uuid.Nil, "", errDoorNone
	}
	return b, ref, nil
}

// enterOrderTx goes through the order door into one of the person's
// orders, for one action (customer.order_detail or customer.pay).
func (s *Server) enterOrderTx(ctx context.Context, tx pgx.Tx, person, business uuid.UUID, ref, action string) error {
	_, err := tx.Exec(ctx, `SELECT taskiem_person_order_enter($1, $2, $3, $4, $5)`, person, s.PhoneIndex.IndexKey(), business, ref, action)
	return doorErr(err)
}

// Looking at one order and asking for its pay link each write an entry in
// the business's audit chain, so they are limited per person: a burst of
// orderLooksBurst, then one every orderLooksEvery.
const (
	orderLooksEvery = 2 * time.Second
	orderLooksBurst = 30
)

// orderLookAllowed takes one of the person's looks at an order, or
// answers 429.
func (s *Server) orderLookAllowed(w http.ResponseWriter, person uuid.UUID) bool {
	if s.limiter("person-order:"+person.String(), orderLooksEvery, orderLooksBurst).Allow() {
		return true
	}
	writeErr(w, http.StatusTooManyRequests, "Too many tries. Wait a moment and try again.")
	return false
}

// errOrderEarlier refuses the detail's door and the pay link of an
// earlier order on the person's number (decision A).
var errOrderEarlier = errors.New("an order made with this number before it was on the account")

// myOrder is GET /v1/me/orders/{business}/{reference}: one of the
// person's orders, with its lines, the address on it, and the rider's
// progress. 403 blocked when the business blocked the person, 409
// area_off when it turned Orders off. An earlier order on the number
// (made before the phone was verified on this account) is its short row
// only, with earlier: true; no door is opened for it.
func (s *Server) myOrder(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	person, err := personOf(r)
	if err != nil {
		s.personFail(w, r, err)
		return
	}
	business, ref, err := orderPath(r)
	if err != nil {
		s.personFail(w, r, err)
		return
	}
	if !s.orderLookAllowed(w, person) {
		return
	}
	var out personOrderDetail
	err = db.InPersonTx(ctx, s.Store.Pool, person, func(tx pgx.Tx) error {
		list, err := s.personOrdersTx(ctx, tx, person, &business, &ref, nil, nil, 1)
		if err != nil {
			return err
		}
		if len(list) == 0 {
			return errDoorNone
		}
		out.personOrder = list[0]
		out.Lines = []personOrderLine{}
		if out.Earlier {
			// Decision A: an earlier order on the number is its row only,
			// and the door is not opened for it (nothing more is read).
			return nil
		}
		if err := s.enterOrderTx(ctx, tx, person, business, ref, "customer.order_detail"); err != nil {
			return err
		}
		if s.Orders == nil {
			return nil
		}
		o, err := s.Orders.GetOrder(ctx, tx, business, ref)
		if errors.Is(err, storefront.ErrNoOrder) {
			return errDoorNone
		}
		if err != nil {
			return err
		}
		for _, l := range o.Basket {
			out.Lines = append(out.Lines, personOrderLine{Name: l.LineName(), Quantity: l.Quantity, PriceKobo: l.PriceKobo, LineKobo: l.LineKobo})
		}
		out.Fulfilment, out.Zone, out.DeliveryKobo, out.PaidKobo = o.Fulfilment, o.Zone, o.DeliveryKobo, o.PaidKobo
		out.ServiceName, out.StartsAt, out.EndsAt = o.ServiceName, o.StartsAt, o.EndsAt
		if o.Fulfilment == "delivery" && (o.Customer.Address != "" || o.Customer.Landmark != "") {
			out.Address = &personOrderAddress{Address: o.Customer.Address, Landmark: o.Customer.Landmark}
		}
		if o.Fulfilment == "delivery" {
			if out.Delivery, err = deliveryProgressTx(ctx, tx, business, o.ID); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		s.personFail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// myOrderPay is GET /v1/me/orders/{business}/{reference}/pay: the pay
// link of one of the person's orders, through the door (customer.pay),
// while something is due; 409 with the reason otherwise.
func (s *Server) myOrderPay(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	person, err := personOf(r)
	if err != nil {
		s.personFail(w, r, err)
		return
	}
	business, ref, err := orderPath(r)
	if err != nil || s.Orders == nil {
		s.personFail(w, r, errDoorNone)
		return
	}
	if !s.orderLookAllowed(w, person) {
		return
	}
	var link string
	var due int64
	err = db.InPersonTx(ctx, s.Store.Pool, person, func(tx pgx.Tx) error {
		// An earlier order on the number is not paid here (decision A):
		// the door refuses it too (hint earlier).
		if list, err := s.personOrdersTx(ctx, tx, person, &business, &ref, nil, nil, 1); err != nil {
			return err
		} else if len(list) == 1 && list[0].Earlier {
			return errOrderEarlier
		}
		if err := s.enterOrderTx(ctx, tx, person, business, ref, "customer.pay"); err != nil {
			return err
		}
		var err error
		link, due, err = s.payLinkTx(ctx, tx, business, ref)
		return err
	})
	if err != nil {
		s.personFail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"reference": ref, "url": link, "due_kobo": due})
}
