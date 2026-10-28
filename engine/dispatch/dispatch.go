// Package dispatch is a business's own riders delivering its storefront
// orders (docs/spec/dispatch.md D2 to D5, decision 0055): an order ready
// to go is offered to one rider at a time with Accept and Can't, by
// rotation among the riders who cover its area (free ones first) or by
// the owner's hand; the rider who accepts holds it, and drives the
// delivery with three taps (Picked up, Arrived, Delivered), the customer
// told at each; an order paid on delivery is paid at the door by a
// one-time account number issued on arrival, or in cash, which the rider
// then owes the business.
//
// A delivery is one order offered to one rider. Only one rider holds an
// order, and only one offer for it is out at a time (unique indexes); a
// rider's buttons are signed for the business, the delivery and their
// contact (api), and every tap checks the delivery's state, so a button
// works once and a released order's buttons do nothing.
package dispatch

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/israel-duff/taskiem/engine/alerts"
	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/lang"
	"github.com/israel-duff/taskiem/engine/moneylog"
	"github.com/israel-duff/taskiem/engine/reach"
	"github.com/israel-duff/taskiem/engine/riders"
	"github.com/israel-duff/taskiem/engine/runtime"
	"github.com/israel-duff/taskiem/engine/sender"
	"github.com/israel-duff/taskiem/engine/storefront"
	"github.com/israel-duff/taskiem/engine/whatsapp"
)

var (
	// ErrNotReady: the order cannot be offered (not accepted, not paid when
	// paid online, not a delivery, or done).
	ErrNotReady = errors.New("dispatch: the order is not ready to dispatch")
	// ErrNoDelivery: no such delivery, or none for the order.
	ErrNoDelivery = errors.New("dispatch: no such delivery")
	// ErrMove: the tap does not fit the delivery now (already done, or
	// released).
	ErrMove = errors.New("dispatch: that does not fit the delivery now")
	// ErrInvalid refuses settings and inputs, in plain words.
	ErrInvalid = errors.New("dispatch: invalid")
)

// Alert kinds raised to the owner.
const (
	AlertAsk       = "dispatch.ask"
	AlertNoRider   = "dispatch.no_rider"
	AlertQuiet     = "dispatch.quiet_rider"
	AlertLowRating = "dispatch.low_rating"
)

// OutboxRider is the outbox kind of a message to a rider: the delivery in
// Run, the stage in Step.
const OutboxRider = "rider"

// Stages of the messages a rider gets.
const (
	StageOffer     = "offer"     // Accept, Can't
	StageHeld      = "held"      // it is yours: the drop; Picked up
	StageOnWay     = "on_way"    // Arrived
	StageArrived   = "arrived"   // Delivered
	StageDoor      = "door"      // the account number; Cash received, New number
	StagePaid      = "paid"      // paid, hand it over; Delivered
	StageUnpaid    = "unpaid"    // not paid yet; Cash received, New number
	StageDone      = "done"      // thank you (and send a photo when asked)
	StageCant      = "cant"      // it goes to another rider
	StageReleased  = "released"  // the owner took it back
	StageWithdrawn = "withdrawn" // the offer was taken back
)

// Dispatch is the businesses' deliveries.
type Dispatch struct {
	Pool   *pgxpool.Pool
	Vault  reach.Cipher
	Riders *riders.Riders
	Orders *storefront.Orders
	// Chat are the chat channels riders are reached on, in order of
	// preference (whatsapp, telegram).
	Chat []string
	// MoneyLog records cash received at the door.
	MoneyLog *moneylog.Log
	// Delivered tells an order's run its delivery is done: the signal the
	// dispatch-orders workflow waits for, and the owner's open approval
	// in the plain order workflow.
	Delivered func(ctx context.Context, tenant uuid.UUID, ord storefront.Order) error
	// StatusLink is an order's status page, where the customer rates the
	// delivery; "" leaves it out.
	StatusLink func(ctx context.Context, tenant uuid.UUID, reference string) string
	Logger     *slog.Logger
	Now        func() time.Time
}

func (d *Dispatch) now() time.Time {
	if d.Now != nil {
		return d.Now()
	}
	return time.Now()
}

func (d *Dispatch) log() *slog.Logger {
	if d.Logger != nil {
		return d.Logger
	}
	return slog.Default()
}

// Ready reports whether deliveries can be dispatched here.
func (d *Dispatch) Ready() bool {
	return d != nil && d.Pool != nil && d.Riders.Ready() && d.Orders != nil
}

func (d *Dispatch) tx(ctx context.Context, tenant uuid.UUID, fn func(pgx.Tx) error) error {
	return db.InTenantTx(ctx, d.Pool, []uuid.UUID{tenant}, fn)
}

// RunTeller tells runs: the runtime's store.
type RunTeller interface {
	DeliverSignalOnce(ctx context.Context, tenant uuid.UUID, env, event, correlation, dedupKey string, payload any) ([]uuid.UUID, bool, error)
	DecideApproval(ctx context.Context, ref runtime.RunRef, step, decision, decidedBy, channel string) error
}

// TellRun is Delivered over the store: the signal the dispatch-orders
// workflow waits for (storefront.delivered), and in the plain order
// workflow the owner's open "mark it delivered" approval, approved.
func TellRun(st RunTeller) func(ctx context.Context, tenant uuid.UUID, ord storefront.Order) error {
	return func(ctx context.Context, tenant uuid.UUID, ord storefront.Order) error {
		if _, _, err := st.DeliverSignalOnce(ctx, tenant, "prod", storefront.DeliveredSignal, ord.Reference, "delivered:"+ord.Reference,
			map[string]any{"reference": ord.Reference, "status": "delivered"}); err != nil {
			return err
		}
		if ord.RunID == nil {
			return nil
		}
		ref := runtime.RunRef{ID: *ord.RunID, TenantID: tenant}
		for _, step := range []string{"done", "done_paid"} {
			if err := st.DecideApproval(ctx, ref, step, "approved", "dispatch:delivered", "rider"); err != nil && !errors.Is(err, runtime.ErrNotFound) {
				return err
			}
		}
		return nil
	}
}

// --- settings ---

// Settings are how a business dispatches.
type Settings struct {
	// Mode is rotation (offered to riders in turn) or ask (the owner is
	// asked who takes each order).
	Mode string `json:"mode"`
	// OfferMinutes is how long a rider has to answer an offer.
	OfferMinutes int `json:"offer_minutes"`
	// PickupMinutes and ArriveMinutes are when a quiet rider is raised:
	// accepted and not picked up, picked up and not arrived.
	PickupMinutes int `json:"pickup_minutes"`
	ArriveMinutes int `json:"arrive_minutes"`
	// Proof asks the rider for a photo when they deliver.
	Proof bool `json:"proof"`
	// OweLimitKobo is the cash a rider may owe before it is a leak.
	OweLimitKobo int64 `json:"owe_limit_kobo"`
	// RatingMark is the average rating below which a rider is raised.
	RatingMark float64 `json:"rating_mark"`
}

// DefaultSettings are a business's until it changes them.
var DefaultSettings = Settings{Mode: "rotation", OfferMinutes: 5, PickupMinutes: 20, ArriveMinutes: 90, OweLimitKobo: 2000000, RatingMark: 3}

// GetSettings reads the business's settings in tx.
func GetSettings(ctx context.Context, tx pgx.Tx, tenant uuid.UUID) (Settings, error) {
	s := DefaultSettings
	err := tx.QueryRow(ctx, `SELECT mode, offer_minutes, pickup_minutes, arrive_minutes, proof, owe_limit_kobo, rating_mark::float8
		FROM dispatch_settings WHERE tenant_id = $1`, tenant).Scan(&s.Mode, &s.OfferMinutes, &s.PickupMinutes, &s.ArriveMinutes, &s.Proof, &s.OweLimitKobo, &s.RatingMark)
	if errors.Is(err, pgx.ErrNoRows) {
		return DefaultSettings, nil
	}
	return s, err
}

// SaveSettings checks and keeps them.
func SaveSettings(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, s Settings, actor string) (Settings, error) {
	switch {
	case s.Mode != "rotation" && s.Mode != "ask":
		return s, fmt.Errorf("%w: offering is rotation or ask", ErrInvalid)
	case s.OfferMinutes < 1 || s.OfferMinutes > 60:
		return s, fmt.Errorf("%w: a rider has 1 to 60 minutes to answer", ErrInvalid)
	case s.PickupMinutes < 5 || s.PickupMinutes > 240:
		return s, fmt.Errorf("%w: picking up is raised after 5 to 240 minutes", ErrInvalid)
	case s.ArriveMinutes < 10 || s.ArriveMinutes > 600:
		return s, fmt.Errorf("%w: arriving is raised after 10 to 600 minutes", ErrInvalid)
	case s.OweLimitKobo < 0:
		return s, fmt.Errorf("%w: the cash a rider may owe is ₦0 or more", ErrInvalid)
	case s.RatingMark < 1 || s.RatingMark > 5:
		return s, fmt.Errorf("%w: the rating mark is 1 to 5", ErrInvalid)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO dispatch_settings (tenant_id, mode, offer_minutes, pickup_minutes, arrive_minutes, proof, owe_limit_kobo, rating_mark)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8) ON CONFLICT (tenant_id) DO UPDATE SET mode = EXCLUDED.mode, offer_minutes = EXCLUDED.offer_minutes,
		  pickup_minutes = EXCLUDED.pickup_minutes, arrive_minutes = EXCLUDED.arrive_minutes, proof = EXCLUDED.proof, owe_limit_kobo = EXCLUDED.owe_limit_kobo,
		  rating_mark = EXCLUDED.rating_mark, updated_at = now()`,
		tenant, s.Mode, s.OfferMinutes, s.PickupMinutes, s.ArriveMinutes, s.Proof, s.OweLimitKobo, s.RatingMark); err != nil {
		return s, err
	}
	return s, audit(ctx, tx, tenant, "dispatch.settings_changed", tenant.String(), map[string]any{"by": actor, "mode": s.Mode})
}

// --- deliveries as the business sees them ---

// Delivery is one order offered to one rider, as the business sees it.
type Delivery struct {
	ID          uuid.UUID  `json:"id"`
	OrderID     uuid.UUID  `json:"order_id"`
	Reference   string     `json:"reference"`
	RiderID     uuid.UUID  `json:"rider_id"`
	Rider       string     `json:"rider"`
	RiderPhone  string     `json:"rider_phone"`
	Status      string     `json:"status"`
	OfferedBy   string     `json:"offered_by"`
	OfferedAt   time.Time  `json:"offered_at"`
	ExpiresAt   time.Time  `json:"expires_at"`
	AnsweredAt  *time.Time `json:"answered_at"`
	AcceptedAt  *time.Time `json:"accepted_at"`
	PickedUpAt  *time.Time `json:"picked_up_at"`
	ArrivedAt   *time.Time `json:"arrived_at"`
	DeliveredAt *time.Time `json:"delivered_at"`
	ReleasedAt  *time.Time `json:"released_at"`
	FeeKobo     int64      `json:"fee_kobo"`
	PaidHow     string     `json:"paid_how,omitempty"`
	Proof       bool       `json:"proof"`
	Location    string     `json:"location,omitempty"`
	Rating      *int       `json:"rating"`
	// Late is what a held delivery is late for: pickup or arrival; "".
	Late string `json:"late,omitempty"`
	// Zone is the order's delivery area.
	Zone string `json:"zone,omitempty"`
	// proofPath is the photo's place in the business's storage.
	proofPath string
}

// ProofPath is where the delivery's photo is kept ("" for none).
func (x Delivery) ProofPath() string { return x.proofPath }

const deliveryCols = `d.id, d.order_id, o.reference, d.rider_id, d.status, d.offered_by, d.offered_at, d.expires_at, d.answered_at, d.accepted_at,
	d.picked_up_at, d.arrived_at, d.delivered_at, d.released_at, d.fee_kobo, COALESCE(d.paid_how, ''), COALESCE(d.proof, ''), COALESCE(d.location, ''),
	d.rating, COALESCE(o.zone, '')`

func scanDelivery(r pgx.Row) (Delivery, error) {
	var x Delivery
	var rating *int16
	err := r.Scan(&x.ID, &x.OrderID, &x.Reference, &x.RiderID, &x.Status, &x.OfferedBy, &x.OfferedAt, &x.ExpiresAt, &x.AnsweredAt, &x.AcceptedAt,
		&x.PickedUpAt, &x.ArrivedAt, &x.DeliveredAt, &x.ReleasedAt, &x.FeeKobo, &x.PaidHow, &x.proofPath, &x.Location, &rating, &x.Zone)
	if errors.Is(err, pgx.ErrNoRows) {
		return x, ErrNoDelivery
	}
	if rating != nil {
		n := int(*rating)
		x.Rating = &n
	}
	x.Proof = x.proofPath != ""
	return x, err
}

// fill names the riders of deliveries and says which are late.
func (d *Dispatch) fill(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, list []Delivery) error {
	if len(list) == 0 {
		return nil
	}
	s, err := GetSettings(ctx, tx, tenant)
	if err != nil {
		return err
	}
	names := map[uuid.UUID]riders.Rider{}
	for i := range list {
		x := &list[i]
		r, ok := names[x.RiderID]
		if !ok {
			if r, err = d.Riders.GetTx(ctx, tx, tenant, x.RiderID); err != nil {
				return err
			}
			names[x.RiderID] = r
		}
		x.Rider, x.RiderPhone = r.Name, r.Phone
		x.Late = late(*x, s, d.now())
	}
	return nil
}

// late is what a held delivery is late for.
func late(x Delivery, s Settings, now time.Time) string {
	switch {
	case x.Status == "accepted" && x.AcceptedAt != nil && now.Sub(*x.AcceptedAt) > time.Duration(s.PickupMinutes)*time.Minute:
		return "pickup"
	case x.Status == "picked_up" && x.PickedUpAt != nil && now.Sub(*x.PickedUpAt) > time.Duration(s.ArriveMinutes)*time.Minute:
		return "arrival"
	}
	return ""
}

// ListFilter narrows deliveries: one order's, one rider's, or a day's
// (Lagos), the open ones, the late ones.
type ListFilter struct {
	Order *uuid.UUID
	Rider *uuid.UUID
	Day   string // 2006-01-02
	Open  bool
}

// ListTx lists deliveries, newest first, offers that came to nothing left
// out unless they are an order's.
func (d *Dispatch) ListTx(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, f ListFilter) ([]Delivery, error) {
	q := `SELECT ` + deliveryCols + ` FROM deliveries d JOIN storefront_orders o ON o.id = d.order_id WHERE d.tenant_id = $1`
	args := []any{tenant}
	if f.Order != nil {
		args = append(args, *f.Order)
		q += fmt.Sprintf(` AND d.order_id = $%d`, len(args))
	} else {
		q += ` AND d.status NOT IN ('declined', 'passed', 'withdrawn')`
	}
	if f.Rider != nil {
		args = append(args, *f.Rider)
		q += fmt.Sprintf(` AND d.rider_id = $%d`, len(args))
	}
	if f.Day != "" {
		args = append(args, f.Day)
		q += fmt.Sprintf(` AND (d.offered_at AT TIME ZONE 'Africa/Lagos')::date = $%d::date`, len(args))
	}
	if f.Open {
		q += ` AND d.status IN ('offered', 'accepted', 'picked_up', 'arrived')`
	}
	rows, err := tx.Query(ctx, q+` ORDER BY d.offered_at DESC LIMIT 300`, args...)
	if err != nil {
		return nil, err
	}
	list, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (Delivery, error) { return scanDelivery(r) })
	if err != nil {
		return nil, err
	}
	if list == nil {
		list = []Delivery{}
	}
	return list, d.fill(ctx, tx, tenant, list)
}

// GetTx reads one delivery.
func (d *Dispatch) GetTx(ctx context.Context, tx pgx.Tx, tenant, id uuid.UUID) (Delivery, error) {
	x, err := scanDelivery(tx.QueryRow(ctx, `SELECT `+deliveryCols+` FROM deliveries d JOIN storefront_orders o ON o.id = d.order_id
		WHERE d.tenant_id = $1 AND d.id = $2`, tenant, id))
	if err != nil {
		return x, err
	}
	one := []Delivery{x}
	err = d.fill(ctx, tx, tenant, one)
	return one[0], err
}

// HeldTx is the delivery that holds an order, or that is offered for it.
func (d *Dispatch) HeldTx(ctx context.Context, tx pgx.Tx, tenant, order uuid.UUID) (Delivery, error) {
	x, err := scanDelivery(tx.QueryRow(ctx, `SELECT `+deliveryCols+` FROM deliveries d JOIN storefront_orders o ON o.id = d.order_id
		WHERE d.tenant_id = $1 AND d.order_id = $2 AND d.status IN ('offered', 'accepted', 'picked_up', 'arrived', 'delivered')
		ORDER BY d.offered_at DESC LIMIT 1`, tenant, order))
	if err != nil {
		return x, err
	}
	one := []Delivery{x}
	err = d.fill(ctx, tx, tenant, one)
	return one[0], err
}

// --- offering ---

// Ready reports why an order cannot be dispatched, or "" when it can: an
// order (not a booking) for delivery, accepted, paid when it is paid
// online, and not yet with a rider.
func ReadyReason(o storefront.Order) string {
	switch {
	case o.Kind != "order":
		return "only orders are delivered"
	case o.Fulfilment != "delivery":
		return "it is for pickup"
	case o.Status != "accepted" && o.Status != "paid":
		return "it is " + strings.ReplaceAll(o.Status, "_", " ")
	case o.PaidAt == nil && (o.Payment == "at_order" || o.Payment == "after_accept") && o.Payable() >= 100:
		return "it is not paid yet"
	}
	return ""
}

// OfferIn is an offer: to one rider (by hand) or by rotation.
type OfferIn struct {
	Rider *uuid.UUID
	// FeeKobo sets this delivery's fee (default the rider's).
	FeeKobo *int64
	// Ask: a workflow offering; with the business set to ask, the owner is
	// asked who takes it instead.
	Ask bool
	By  string
}

// OfferOut is what came of an offer.
type OfferOut struct {
	// Status: offered (out to a rider), held (a rider has it already),
	// asked_owner, or no_rider (none to offer it to; the owner is told).
	Status   string    `json:"status"`
	Delivery *Delivery `json:"delivery,omitempty"`
	Rider    string    `json:"rider,omitempty"`
}

// Offer offers an order to a rider: the one named (the owner's choice,
// which withdraws an offer out by rotation), or the next by rotation. An
// order already held answers held; one already offered by rotation, the
// offer out.
func (d *Dispatch) Offer(ctx context.Context, tenant uuid.UUID, reference string, in OfferIn) (OfferOut, error) {
	var out OfferOut
	var tell []func() error
	err := d.tx(ctx, tenant, func(tx pgx.Tx) error {
		var err error
		out, tell, err = d.offerTx(ctx, tx, tenant, reference, in, nil)
		return err
	})
	if err != nil {
		return out, err
	}
	return out, run(tell)
}

func run(fns []func() error) error {
	var errs []error
	for _, f := range fns {
		errs = append(errs, f())
	}
	return errors.Join(errs...)
}

func (d *Dispatch) offerTx(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, reference string, in OfferIn, exclude []uuid.UUID) (OfferOut, []func() error, error) {
	var out OfferOut
	var after []func() error
	ord, err := d.Orders.GetOrder(ctx, tx, tenant, reference)
	if err != nil {
		return out, nil, err
	}
	if _, err := tx.Exec(ctx, `SELECT 1 FROM storefront_orders WHERE id = $1 FOR UPDATE`, ord.ID); err != nil {
		return out, nil, err
	}
	cur, err := d.HeldTx(ctx, tx, tenant, ord.ID)
	switch {
	case err == nil && cur.Status != "offered" && cur.Status != "delivered":
		out.Status, out.Delivery, out.Rider = "held", &cur, cur.Rider
		return out, nil, nil
	case err == nil && cur.Status == "offered":
		if in.Rider == nil || *in.Rider == cur.RiderID {
			out.Status, out.Delivery, out.Rider = "offered", &cur, cur.Rider
			return out, nil, nil
		}
		// The owner names another rider: the offer out is taken back.
		if _, err := tx.Exec(ctx, `UPDATE deliveries SET status = 'withdrawn', answered_at = now() WHERE id = $1`, cur.ID); err != nil {
			return out, nil, err
		}
		if err := d.queue(ctx, tx, tenant, cur.ID, cur.RiderID, StageWithdrawn, ""); err != nil {
			return out, nil, err
		}
	case err != nil && !errors.Is(err, ErrNoDelivery):
		return out, nil, err
	}
	if why := ReadyReason(ord); why != "" {
		return out, nil, fmt.Errorf("%w: %s %s", ErrNotReady, ord.Reference, why)
	}
	s, err := GetSettings(ctx, tx, tenant)
	if err != nil {
		return out, nil, err
	}
	var rider riders.Rider
	switch {
	case in.Rider != nil:
		if rider, err = d.Riders.GetTx(ctx, tx, tenant, *in.Rider); err != nil {
			return out, nil, err
		}
		if rider.RetiredAt != nil {
			return out, nil, fmt.Errorf("%w: %s is retired", ErrInvalid, rider.Name)
		}
	case in.Ask && s.Mode == "ask":
		out.Status = "asked_owner"
		return out, nil, d.askOwner(ctx, tx, tenant, ord, "")
	default:
		cands, err := d.candidates(ctx, tx, tenant, ord, exclude)
		if err != nil {
			return out, nil, err
		}
		if len(cands) == 0 {
			out.Status = "no_rider"
			return out, nil, d.askOwner(ctx, tx, tenant, ord, "no_rider")
		}
		rider = cands[0]
	}
	fee := rider.FeeKobo
	if in.FeeKobo != nil {
		if *in.FeeKobo < 0 || *in.FeeKobo > storefront.MaxZoneFeeKobo {
			return out, nil, fmt.Errorf("%w: a delivery fee is ₦0 to ₦100,000", ErrInvalid)
		}
		fee = *in.FeeKobo
	}
	by := in.By
	if by == "" {
		by = "rotation"
	}
	id := uuid.Must(uuid.NewV7())
	expires := d.now().Add(time.Duration(s.OfferMinutes) * time.Minute)
	if _, err := tx.Exec(ctx, `INSERT INTO deliveries (id, tenant_id, order_id, rider_id, status, offered_by, expires_at, fee_kobo)
		VALUES ($1, $2, $3, $4, 'offered', $5, $6, $7)`, id, tenant, ord.ID, rider.ID, by, expires, fee); err != nil {
		return out, nil, err
	}
	if err := d.queue(ctx, tx, tenant, id, rider.ID, StageOffer, ""); err != nil {
		return out, nil, err
	}
	if err := audit(ctx, tx, tenant, "dispatch.offered", ord.Reference, map[string]any{"rider": rider.ID, "by": by}); err != nil {
		return out, nil, err
	}
	x, err := d.GetTx(ctx, tx, tenant, id)
	if err != nil {
		return out, nil, err
	}
	out.Status, out.Delivery, out.Rider = "offered", &x, rider.Name
	return out, after, nil
}

// covers reports whether a rider covers an order's area: one with no
// areas covers all, an order with none is anyone's.
func covers(r riders.Rider, zone string) bool {
	if zone == "" || len(r.Zones) == 0 {
		return true
	}
	return slices.ContainsFunc(r.Zones, func(z string) bool { return strings.EqualFold(z, zone) })
}

// candidates are the riders an order may be offered to by rotation, best
// first: active riders reached on a chat app who cover its area, are not
// off or not answering, and were not offered it already; free ones
// first, then the least loaded (deliveries held for any business), then
// the one offered anything longest ago.
func (d *Dispatch) candidates(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, ord storefront.Order, exclude []uuid.UUID) ([]riders.Rider, error) {
	list, err := d.Riders.ListTx(ctx, tx, tenant, riders.Filter{ActiveOnly: true})
	if err != nil {
		return nil, err
	}
	tried := map[uuid.UUID]bool{}
	for _, id := range exclude {
		tried[id] = true
	}
	rows, err := tx.Query(ctx, `SELECT rider_id FROM deliveries WHERE tenant_id = $1 AND order_id = $2`, tenant, ord.ID)
	if err != nil {
		return nil, err
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
	if err != nil {
		return nil, err
	}
	for _, id := range ids {
		tried[id] = true
	}
	last := map[uuid.UUID]time.Time{}
	rows, err = tx.Query(ctx, `SELECT rider_id, max(offered_at) FROM deliveries WHERE tenant_id = $1 GROUP BY rider_id`, tenant)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var id uuid.UUID
		var at time.Time
		if err := rows.Scan(&id, &at); err != nil {
			rows.Close()
			return nil, err
		}
		last[id] = at
	}
	rows.Close()
	var out []riders.Rider
	for _, r := range list {
		if tried[r.ID] || r.Reached == "sms" || r.NotAnswering || r.Presence.State == "off" || !covers(r, ord.Zone) {
			continue
		}
		out = append(out, r)
	}
	slices.SortStableFunc(out, func(a, b riders.Rider) int {
		fa, fb := a.Presence.State == "free", b.Presence.State == "free"
		switch {
		case fa != fb:
			if fa {
				return -1
			}
			return 1
		case a.Presence.Open != b.Presence.Open:
			return a.Presence.Open - b.Presence.Open
		}
		return last[a.ID].Compare(last[b.ID])
	})
	return out, nil
}

// askOwner tells the owner an order waits for a rider: who covers its
// area and their state, and how to name one; why is no_rider when none
// took it.
func (d *Dispatch) askOwner(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, ord storefront.Order, why string) error {
	list, err := d.Riders.ListTx(ctx, tx, tenant, riders.Filter{ActiveOnly: true})
	if err != nil {
		return err
	}
	var lines []string
	for _, r := range list {
		if !covers(r, ord.Zone) {
			continue
		}
		state := r.Presence.Line()
		if r.NotAnswering {
			state = "not answering today"
		}
		if r.Reached == "sms" {
			state += ", has not tapped the link"
		}
		lines = append(lines, r.Name+": "+state)
	}
	where := ""
	if ord.Zone != "" {
		where = " to " + ord.Zone
	}
	title := "Who takes " + ord.Reference + where + "?"
	kind := AlertAsk
	if why == "no_rider" {
		title, kind = "No rider took "+ord.Reference+where, AlertNoRider
	}
	body := strings.Join(lines, "\n")
	if body == "" {
		body = "No rider covers this area yet. Add one under Storefront > Riders."
	}
	body += "\n\nReply: order " + ord.Reference + " to <rider's name>"
	var n int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM deliveries WHERE order_id = $1`, ord.ID).Scan(&n); err != nil {
		return err
	}
	_, err = alerts.Notify(ctx, tx, tenant, alerts.Alert{Kind: kind, Dedup: fmt.Sprintf("%s:%d", ord.ID, n), Title: title, Body: body,
		Link: "/storefront/orders/" + ord.Reference, Detail: map[string]any{"reference": ord.Reference}})
	return err
}

// --- the rider's taps ---

// Actions a rider takes on a delivery.
const (
	Accept    = "accept"
	Cant      = "cant"
	PickedUp  = "picked_up"
	Arrived   = "arrived"
	Delivered = "delivered"
	Cash      = "cash"
	NewNumber = "new_number"
)

// Tap is what came of a rider's tap: the delivery as it is now; Already
// when it was done before (a second tap), Moved when it moved now.
type Tap struct {
	Delivery Delivery
	Moved    bool
}

// Press takes a rider's tap on a delivery of the business (their button
// or their word). by names the rider ("rider:<id>").
func (d *Dispatch) Press(ctx context.Context, tenant, delivery uuid.UUID, action string) (Tap, error) {
	var t Tap
	var after []func() error
	err := d.tx(ctx, tenant, func(tx pgx.Tx) error {
		x, err := scanDelivery(tx.QueryRow(ctx, `SELECT `+deliveryCols+` FROM deliveries d JOIN storefront_orders o ON o.id = d.order_id
			WHERE d.tenant_id = $1 AND d.id = $2 FOR UPDATE OF d`, tenant, delivery))
		if err != nil {
			return err
		}
		// Any tap is an answer: a rider marked not answering is back.
		if _, err := tx.Exec(ctx, `UPDATE riders SET not_answering_since = NULL, missed = 0 WHERE id = $1 AND not_answering_since IS NOT NULL`, x.RiderID); err != nil {
			return err
		}
		ord, err := d.Orders.GetOrder(ctx, tx, tenant, x.Reference)
		if err != nil {
			return err
		}
		t, after, err = d.press(ctx, tx, tenant, x, ord, action)
		return err
	})
	if err != nil {
		return t, err
	}
	return t, run(after)
}

func (d *Dispatch) press(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, x Delivery, ord storefront.Order, action string) (Tap, []func() error, error) {
	t := Tap{Delivery: x}
	var after []func() error
	set := func(status, col string, orderStatus string) error {
		q := `UPDATE deliveries SET status = $2, ` + col + ` = now(), answered_at = COALESCE(answered_at, now()) WHERE id = $1`
		if col == "answered_at" {
			q = `UPDATE deliveries SET status = $2, answered_at = now() WHERE id = $1`
		}
		if _, err := tx.Exec(ctx, q, x.ID, status); err != nil {
			return err
		}
		if orderStatus != "" {
			if _, err := tx.Exec(ctx, `UPDATE storefront_orders SET status = $2, updated_at = now() WHERE id = $1 AND status NOT IN ('completed', 'cancelled', 'declined', 'refund_due', 'refunded')`,
				ord.ID, orderStatus); err != nil {
				return err
			}
		}
		t.Moved = true
		return audit(ctx, tx, tenant, "dispatch."+status, ord.Reference, map[string]any{"rider": x.RiderID})
	}
	already := func(states ...string) bool { return slices.Contains(states, x.Status) }
	unpaid := ord.Payment == "on_delivery" && ord.PaidAt == nil
	switch action {
	case Accept:
		if already("accepted", "picked_up", "arrived", "delivered") {
			return t, nil, nil
		}
		if x.Status != "offered" {
			return t, nil, fmt.Errorf("%w: the offer for %s has passed", ErrMove, ord.Reference)
		}
		if why := ReadyReason(ord); why != "" && ord.Status != "dispatched" {
			return t, nil, fmt.Errorf("%w: %s %s", ErrMove, ord.Reference, why)
		}
		if err := set("accepted", "accepted_at", "dispatched"); err != nil {
			return t, nil, err
		}
		if _, err := tx.Exec(ctx, `UPDATE riders SET missed = 0 WHERE id = $1`, x.RiderID); err != nil {
			return t, nil, err
		}
		if err := d.queue(ctx, tx, tenant, x.ID, x.RiderID, StageHeld, ""); err != nil {
			return t, nil, err
		}
	case Cant:
		if already("declined") {
			return t, nil, nil
		}
		if x.Status != "offered" {
			return t, nil, fmt.Errorf("%w: the offer for %s has passed", ErrMove, ord.Reference)
		}
		if err := set("declined", "answered_at", ""); err != nil {
			return t, nil, err
		}
		if err := d.queue(ctx, tx, tenant, x.ID, x.RiderID, StageCant, ""); err != nil {
			return t, nil, err
		}
		if err := d.next(ctx, tx, tenant, x, ord); err != nil {
			return t, nil, err
		}
	case PickedUp:
		if already("picked_up", "arrived", "delivered") {
			return t, nil, nil
		}
		if x.Status != "accepted" {
			return t, nil, fmt.Errorf("%w: %s is not yours to pick up", ErrMove, ord.Reference)
		}
		if err := set("picked_up", "picked_up_at", "on_the_way"); err != nil {
			return t, nil, err
		}
		if err := d.queue(ctx, tx, tenant, x.ID, x.RiderID, StageOnWay, ""); err != nil {
			return t, nil, err
		}
		r, err := d.Riders.GetTx(ctx, tx, tenant, x.RiderID)
		if err != nil {
			return t, nil, err
		}
		after = append(after, func() error {
			return d.tellCustomer(ctx, tenant, ord, "customer.delivery_on_way", "on-way", "rider", firstName(r.Name), "phone", r.Phone)
		})
	case Arrived:
		if already("arrived", "delivered") {
			return t, nil, nil
		}
		if x.Status != "picked_up" && x.Status != "accepted" {
			return t, nil, fmt.Errorf("%w: %s is not on its way with you", ErrMove, ord.Reference)
		}
		if err := set("arrived", "arrived_at", "arrived"); err != nil {
			return t, nil, err
		}
		if unpaid {
			// Paid at the door: the one-time number is issued now, and the
			// rider and the customer get it once it is (door.go).
			after = append(after, func() error { return d.door(ctx, tenant, x.ID, ord, "") })
		} else {
			if err := d.queue(ctx, tx, tenant, x.ID, x.RiderID, StageArrived, ""); err != nil {
				return t, nil, err
			}
			after = append(after, func() error { return d.tellCustomer(ctx, tenant, ord, "customer.delivery_arrived", "arrived") })
		}
	case NewNumber:
		if x.Status != "arrived" || !unpaid {
			return t, nil, fmt.Errorf("%w: %s is not waiting for its payment at the door", ErrMove, ord.Reference)
		}
		seq := fmt.Sprint(d.now().Unix())
		after = append(after, func() error { return d.door(ctx, tenant, x.ID, ord, seq) })
	case Cash:
		if x.PaidHow == "cash" || already("delivered") {
			return t, nil, nil
		}
		if x.Status != "arrived" && x.Status != "picked_up" {
			return t, nil, fmt.Errorf("%w: %s is not at the door with you", ErrMove, ord.Reference)
		}
		if !unpaid {
			return t, nil, fmt.Errorf("%w: %s is paid already; hand it over and tap Delivered", ErrMove, ord.Reference)
		}
		c, err := d.cashTx(ctx, tx, tenant, x, ord)
		if err != nil {
			return t, nil, err
		}
		ord = c.Order
		after = append(after, func() error { return d.Orders.Paid(ctx, tenant, c) })
		fallthrough
	case Delivered:
		if already("delivered") {
			return t, nil, nil
		}
		if x.Status != "arrived" && x.Status != "picked_up" {
			return t, nil, fmt.Errorf("%w: %s is not at the door with you", ErrMove, ord.Reference)
		}
		if action == Delivered && unpaid {
			if err := d.queue(ctx, tx, tenant, x.ID, x.RiderID, StageUnpaid, fmt.Sprint(d.now().Unix())); err != nil {
				return t, nil, err
			}
			return t, nil, fmt.Errorf("%w: %s is not paid yet: take the payment first", ErrMove, ord.Reference)
		}
		if err := set("delivered", "delivered_at", ""); err != nil {
			return t, nil, err
		}
		if err := d.queue(ctx, tx, tenant, x.ID, x.RiderID, StageDone, ""); err != nil {
			return t, nil, err
		}
		after = append(after, func() error { return d.complete(ctx, tenant, x, ord) })
	default:
		return t, nil, fmt.Errorf("%w: %q", ErrMove, action)
	}
	nx, err := d.GetTx(ctx, tx, tenant, x.ID)
	if err != nil {
		return t, nil, err
	}
	t.Delivery = nx
	return t, after, nil
}

// complete completes a delivered order as the owner's "delivered" does,
// tells its run, and the customer, with the link to rate it.
func (d *Dispatch) complete(ctx context.Context, tenant uuid.UUID, x Delivery, ord storefront.Order) error {
	if _, err := d.Orders.Decide(ctx, tenant, ord.Reference, "complete", "", "rider:"+x.RiderID.String()); err != nil {
		return err
	}
	var errs []error
	if d.Delivered != nil {
		errs = append(errs, d.Delivered(ctx, tenant, ord))
	}
	link := ""
	if d.StatusLink != nil {
		link = d.StatusLink(ctx, tenant, ord.Reference)
	}
	key := "customer.delivery_done"
	if link != "" {
		key = "customer.delivery_done_rate"
	}
	errs = append(errs, d.tellCustomer(ctx, tenant, ord, key, "done", "link", link))
	return errors.Join(errs...)
}

// next offers an order to the next rider after one declined or passed:
// by rotation when it was offered by rotation; the owner is told when
// they named the rider.
func (d *Dispatch) next(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, x Delivery, ord storefront.Order) error {
	if x.OfferedBy != "rotation" && !strings.HasPrefix(x.OfferedBy, "workflow:") {
		r, err := d.Riders.GetTx(ctx, tx, tenant, x.RiderID)
		if err != nil {
			return err
		}
		_, err = alerts.Notify(ctx, tx, tenant, alerts.Alert{Kind: AlertNoRider, Dedup: x.ID.String(), Title: r.Name + " did not take " + ord.Reference,
			Body: "Offer it to another rider: order " + ord.Reference + " to <rider's name>, or from the order's page.", Link: "/storefront/orders/" + ord.Reference})
		return err
	}
	_, _, err := d.offerTx(ctx, tx, tenant, ord.Reference, OfferIn{By: "rotation"}, nil)
	if errors.Is(err, ErrNotReady) {
		return nil
	}
	return err
}

// Release takes an order back from its rider (or withdraws its offer):
// the order is ready to offer again, the rider told.
func (d *Dispatch) Release(ctx context.Context, tenant uuid.UUID, reference, actor string) (Delivery, error) {
	var x Delivery
	err := d.tx(ctx, tenant, func(tx pgx.Tx) error {
		ord, err := d.Orders.GetOrder(ctx, tx, tenant, reference)
		if err != nil {
			return err
		}
		if x, err = d.HeldTx(ctx, tx, tenant, ord.ID); err != nil {
			return err
		}
		stage, status, col := StageReleased, "released", "released_at"
		switch x.Status {
		case "offered":
			stage, status, col = StageWithdrawn, "withdrawn", "answered_at"
		case "accepted", "picked_up", "arrived":
		default:
			return fmt.Errorf("%w: %s is delivered", ErrMove, reference)
		}
		if _, err := tx.Exec(ctx, `UPDATE deliveries SET status = $2, `+col+` = now() WHERE id = $1`, x.ID, status); err != nil {
			return err
		}
		back := "accepted"
		if ord.PaidAt != nil {
			back = "paid"
		}
		if _, err := tx.Exec(ctx, `UPDATE storefront_orders SET status = $2, updated_at = now() WHERE id = $1 AND status IN ('dispatched', 'on_the_way', 'arrived')`,
			ord.ID, back); err != nil {
			return err
		}
		if err := d.queue(ctx, tx, tenant, x.ID, x.RiderID, stage, ""); err != nil {
			return err
		}
		if err := audit(ctx, tx, tenant, "dispatch."+status, reference, map[string]any{"by": actor, "rider": x.RiderID}); err != nil {
			return err
		}
		x, err = d.GetTx(ctx, tx, tenant, x.ID)
		return err
	})
	return x, err
}

// --- the sweep: offers that ran out, quiet riders ---

// Sweep passes offers not answered in their window (on to the next
// rider), marks riders not answering after three in a day, and raises
// quiet riders and low ratings to the owner.
func (d *Dispatch) Sweep(ctx context.Context) error {
	rows, err := d.Pool.Query(ctx, `SELECT tenant_id FROM taskiem_dispatch_tenants()`)
	if err != nil {
		return err
	}
	tenants, err := pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
	if err != nil {
		return err
	}
	var errs []error
	for _, t := range tenants {
		if err := d.sweepTenant(ctx, t); err != nil {
			d.log().Warn("dispatch: sweep", "tenant", t, "err", err)
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func (d *Dispatch) sweepTenant(ctx context.Context, tenant uuid.UUID) error {
	return d.tx(ctx, tenant, func(tx pgx.Tx) error {
		s, err := GetSettings(ctx, tx, tenant)
		if err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `SELECT `+deliveryCols+` FROM deliveries d JOIN storefront_orders o ON o.id = d.order_id
			WHERE d.tenant_id = $1 AND d.status = 'offered' AND d.expires_at <= now() FOR UPDATE OF d`, tenant)
		if err != nil {
			return err
		}
		passed, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (Delivery, error) { return scanDelivery(r) })
		if err != nil {
			return err
		}
		for _, x := range passed {
			if _, err := tx.Exec(ctx, `UPDATE deliveries SET status = 'passed', answered_at = now() WHERE id = $1`, x.ID); err != nil {
				return err
			}
			// Three unanswered in a day (Lagos) and they are not answering.
			if _, err := tx.Exec(ctx, `UPDATE riders SET missed = CASE WHEN missed_on = (now() AT TIME ZONE 'Africa/Lagos')::date THEN missed + 1 ELSE 1 END,
				missed_on = (now() AT TIME ZONE 'Africa/Lagos')::date WHERE id = $1`, x.RiderID); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `UPDATE riders SET not_answering_since = now() WHERE id = $1 AND missed >= 3 AND not_answering_since IS NULL`, x.RiderID); err != nil {
				return err
			}
			if err := d.queue(ctx, tx, tenant, x.ID, x.RiderID, StageWithdrawn, ""); err != nil {
				return err
			}
			ord, err := d.Orders.GetOrder(ctx, tx, tenant, x.Reference)
			if err != nil {
				return err
			}
			if err := d.next(ctx, tx, tenant, x, ord); err != nil {
				return err
			}
		}
		held, err := d.ListTx(ctx, tx, tenant, ListFilter{Open: true})
		if err != nil {
			return err
		}
		for _, x := range held {
			switch x.Late {
			case "pickup":
				err = d.raiseQuiet(ctx, tx, tenant, x, "has not picked up", s.PickupMinutes)
			case "arrival":
				err = d.raiseQuiet(ctx, tx, tenant, x, "has not arrived with", s.ArriveMinutes)
			}
			if err != nil {
				return err
			}
		}
		if _, err := d.MatchRemittances(ctx, tx, tenant); err != nil {
			return err
		}
		return d.lowRatings(ctx, tx, tenant, s)
	})
}

func (d *Dispatch) raiseQuiet(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, x Delivery, what string, minutes int) error {
	_, err := alerts.Notify(ctx, tx, tenant, alerts.Alert{Kind: AlertQuiet, Dedup: x.ID.String() + ":" + x.Late,
		Title: fmt.Sprintf("%s %s %s in %d minutes", x.Rider, what, x.Reference, minutes),
		Body:  "Call them on " + x.RiderPhone + ", or take the order back and offer it to another rider: release " + x.Reference,
		Link:  "/storefront/orders/" + x.Reference, Detail: map[string]any{"reference": x.Reference, "late": x.Late}})
	return err
}

// lowRatings raises a rider whose last ten ratings average below the mark,
// once a week.
func (d *Dispatch) lowRatings(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, s Settings) error {
	rows, err := tx.Query(ctx, `SELECT rider_id, avg(rating)::float8, count(*) FROM (
		  SELECT rider_id, rating, row_number() OVER (PARTITION BY rider_id ORDER BY rated_at DESC) AS n FROM deliveries
		   WHERE tenant_id = $1 AND rating IS NOT NULL) r WHERE n <= 10 GROUP BY rider_id HAVING count(*) >= 3 AND avg(rating) < $2`, tenant, s.RatingMark)
	if err != nil {
		return err
	}
	type low struct {
		id  uuid.UUID
		avg float64
		n   int
	}
	list, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (low, error) {
		var x low
		return x, r.Scan(&x.id, &x.avg, &x.n)
	})
	if err != nil {
		return err
	}
	year, week := d.now().ISOWeek()
	for _, l := range list {
		r, err := d.Riders.GetTx(ctx, tx, tenant, l.id)
		if err != nil {
			return err
		}
		if _, err := alerts.Notify(ctx, tx, tenant, alerts.Alert{Kind: AlertLowRating, Dedup: fmt.Sprintf("%s:%d-%d", l.id, year, week),
			Title: fmt.Sprintf("%s's deliveries are rated %.1f of 5", r.Name, l.avg),
			Body:  fmt.Sprintf("The last %d ratings customers gave %s average %.1f, below your mark of %.1f.", l.n, r.Name, l.avg, s.RatingMark),
			Link:  "/storefront/riders"}); err != nil {
			return err
		}
	}
	return nil
}

// --- messages ---

// queue puts a message to a rider in the outbox, on the chat app they
// tapped in on; a rider not on one gets it by SMS (offers only: their
// taps need the chat app). seq tells apart repeats of a stage.
func (d *Dispatch) queue(ctx context.Context, tx pgx.Tx, tenant, delivery, rider uuid.UUID, stage, seq string) error {
	r, err := d.Riders.GetTx(ctx, tx, tenant, rider)
	if err != nil {
		return err
	}
	t, ok, err := d.Riders.Book.FindTx(ctx, tx, tenant, uuid.Nil, r.Phone, d.Chat)
	if err != nil {
		return err
	}
	key := "rider:" + delivery.String() + ":" + stage
	if seq != "" {
		key += ":" + seq
	}
	if !ok || t.Contact == uuid.Nil {
		if stage != StageOffer && stage != StageHeld {
			return nil
		}
		return d.smsOffer(ctx, tx, tenant, r, delivery, stage, key)
	}
	step := stage
	_, err = whatsapp.Enqueue(ctx, tx, whatsapp.OutboxItem{Tenant: tenant, Channel: t.Channel, Kind: OutboxRider, Contact: &t.Contact,
		Run: &delivery, Step: &step}, outboxKey(t.Channel, key))
	return err
}

func outboxKey(channel, key string) string {
	if channel == "" || channel == reach.WhatsApp {
		return key
	}
	return channel + ":" + key
}

// smsOffer tells a rider not on a chat app of an offer by SMS, with the
// link to tap: their answer needs the chat app.
func (d *Dispatch) smsOffer(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, r riders.Rider, delivery uuid.UUID, stage, key string) error {
	m := d.Riders.Messages
	if m == nil {
		return nil
	}
	link, err := d.Riders.TapIn(ctx, tx, tenant)
	if err != nil {
		return err
	}
	var business, reference string
	if err := tx.QueryRow(ctx, `SELECT t.name, o.reference FROM deliveries x JOIN storefront_orders o ON o.id = x.order_id JOIN tenants t ON t.id = x.tenant_id
		WHERE x.id = $1`, delivery).Scan(&business, &reference); err != nil {
		return err
	}
	text := "A delivery for you: " + reference + ". Tap to get it on WhatsApp and answer: " + link
	if stage == StageHeld {
		text = reference + " is yours. Tap to get its details on WhatsApp: " + link
	}
	_, err = m.Enqueue(ctx, tx, sender.Message{Tenant: tenant, Channel: sender.SMS, Purpose: sender.PurposeNotice, To: r.Phone, SenderName: business,
		Text: text, Key: key})
	if errors.Is(err, sender.ErrAllowance) || errors.Is(err, sender.ErrInvalid) {
		return nil
	}
	return err
}

// tellCustomer sends the customer one of Taskiem's messages about their
// delivery, in the business's language, by SMS (and email).
func (d *Dispatch) tellCustomer(ctx context.Context, tenant uuid.UUID, ord storefront.Order, id, key string, kv ...string) error {
	if d.Orders.Messages == nil {
		return nil
	}
	l := d.Orders.CustomerLanguage(ctx, tenant)
	text := lang.Default().Text(l, id, append([]string{"reference", ord.Reference}, kv...)...)
	_, err := d.Orders.NotifyCustomer(ctx, tenant, ord.Reference, text, "", "delivery:"+key+":"+ord.Reference)
	if errors.Is(err, sender.ErrInvalid) || errors.Is(err, sender.ErrAllowance) || errors.Is(err, sender.ErrRecipientLimit) {
		d.log().Warn("dispatch: the customer was not told", "reference", ord.Reference, "err", err)
		return nil
	}
	return err
}

// firstName is a name's first word.
func firstName(s string) string {
	if f := strings.Fields(s); len(f) > 0 {
		return f[0]
	}
	return s
}

func audit(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, action, target string, detail map[string]any) error {
	raw, _ := json.Marshal(detail)
	_, err := tx.Exec(ctx, `SELECT taskiem_audit_append($1, 'system', 'dispatch', $2, $3, $4)`, tenant, action, target, raw)
	return err
}
