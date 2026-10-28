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

	"github.com/israel-duff/taskiem/engine/alerts"
	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/pii"
	"github.com/israel-duff/taskiem/engine/runtime"
)

// Bookings (docs/spec/storefronts.md 6.9) are orders of kind booking with
// a service and a time, holding their slot in storefront_holds. A run
// takes a booking to its appointment: the owner's decision (or automatic
// confirmation), the customer's confirmation, reminders the day before and
// two hours before as timer steps, and attended or no-show after. Moving a
// booking ends its run and starts another (event "rescheduled") with the
// new times, so its timers follow; cancelling ends it.

// Alert kinds the owner gets about bookings.
const (
	AlertBookingChanged = "storefront.booking_changed"
	AlertBookingsToday  = "storefront.bookings_today"
)

// SummaryHour is when, in Lagos, the owner is sent the day's bookings.
const SummaryHour = 7

// liveBooking are the statuses a booking holds its slot in.
var liveBooking = []string{"received", "accepted", "awaiting_payment", "paid"}

// When is a booking's time as customers read it: "Sat 24 Oct, 10:30".
func When(t time.Time) string { return t.In(Lagos).Format("Mon 2 Jan, 15:04") }

// bookingBody is trigger.body.booking: the service and time, and when the
// reminders fall (now, when already past: remind_day and remind_hours say
// whether to send them).
func (o *Orders) bookingBody(ord Order, autoConfirm bool) map[string]any {
	now := o.now()
	start, end := ord.StartsAt.In(Lagos), ord.EndsAt.In(Lagos)
	at := func(d time.Duration) (string, bool) {
		t := start.Add(-d)
		if t.Before(now) {
			return now.In(Lagos).Format(time.RFC3339), false
		}
		return t.Format(time.RFC3339), true
	}
	day, dayOK := at(24 * time.Hour)
	hours, hoursOK := at(2 * time.Hour)
	return map[string]any{"service_id": ord.ServiceID, "service": ord.ServiceName, "starts_at": start.Format(time.RFC3339), "ends_at": end.Format(time.RFC3339),
		"when": When(start), "auto_confirm": autoConfirm, "remind_day_at": day, "remind_day": dayOK, "remind_hours_at": hours, "remind_hours": hoursOK,
		"deposit_kobo": ord.Payable(), "status": ord.Status}
}

// linkOf is the customer's status link for an order (the storefront's page
// when the key is not there).
func (o *Orders) linkOf(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, ord Order) (string, error) {
	var env map[string]any
	var slug string
	if err := tx.QueryRow(ctx, `SELECT o.status_key, COALESCE(s.published_slug, s.slug) FROM storefront_orders o JOIN storefronts s ON s.id = o.storefront_id
		WHERE o.tenant_id = $1 AND o.id = $2`, tenant, ord.ID).Scan(&env, &slug); err != nil {
		return "", err
	}
	if env == nil {
		return "https://" + slug + "." + o.PreviewDomain + "/", nil
	}
	v, err := o.Vault.OpenTx(ctx, tx, tenant, env)
	if errors.Is(err, pii.ErrErased) {
		return "https://" + slug + "." + o.PreviewDomain + "/", nil
	}
	if err != nil {
		return "", err
	}
	key, _ := v.(string)
	return o.statusURL(slug, ord.Reference, key), nil
}

// statusKeyOf opens an order's status key ("" when gone).
func (o *Orders) statusKeyOf(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, id uuid.UUID) (string, error) {
	var env map[string]any
	if err := tx.QueryRow(ctx, `SELECT status_key FROM storefront_orders WHERE tenant_id = $1 AND id = $2`, tenant, id).Scan(&env); err != nil || env == nil {
		return "", err
	}
	v, err := o.Vault.OpenTx(ctx, tx, tenant, env)
	if errors.Is(err, pii.ErrErased) {
		return "", nil
	}
	key, _ := v.(string)
	return key, err
}

// endBooking ends a booking's run, if it has one, reversing what it did as
// any cancel does (decision 0054), and tells the customer (text empty:
// nobody).
func (o *Orders) endBooking(ctx context.Context, tenant uuid.UUID, reference string, run *uuid.UUID, text, key string) error {
	if run != nil && o.Cancel != nil {
		if err := o.Cancel(ctx, runtime.RunRef{ID: *run, TenantID: tenant}, "storefront", true); err != nil {
			return err
		}
	}
	return o.tell(ctx, tenant, reference, text, key)
}

// tell is a message the engine sends the customer itself; without
// customer messages on the deployment it is skipped, never retried for
// ever.
func (o *Orders) tell(ctx context.Context, tenant uuid.UUID, reference, text, key string) error {
	if text == "" {
		return nil
	}
	if o.Messages == nil {
		o.log().Warn("storefront: customer messages are not configured; not sent", "reference", reference)
		return nil
	}
	_, err := o.NotifyCustomer(ctx, tenant, reference, text, "", key)
	return err
}

// GetBooking is what a workflow reads of a booking: its status, service,
// time and payment page.
func (o *Orders) GetBooking(ctx context.Context, tenant uuid.UUID, reference string) (map[string]any, error) {
	var ord Order
	var auto bool
	err := db.InTenantTx(ctx, o.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		var err error
		if ord, err = scanOrder(tx.QueryRow(ctx, `SELECT `+orderCols+` FROM storefront_orders WHERE tenant_id = $1 AND reference = $2`, tenant, reference)); err != nil {
			return err
		}
		sf, err := Get(ctx, tx, tenant)
		auto = sf.Availability.AutoConfirm
		return err
	})
	if err != nil {
		return nil, err
	}
	if ord.Kind != "booking" {
		return nil, fmt.Errorf("%w: %s is not a booking", ErrDecision, reference)
	}
	out := o.bookingBody(ord, auto)
	out["reference"] = reference
	out["payment_url"] = ord.PaymentURL
	out["total"] = FormatNaira(ord.TotalKobo)
	out["paid"] = ord.PaidAt != nil
	return out, nil
}

// Bookings lists bookings starting between from and to that hold their
// slot, the customer opened, soonest first.
func (o *Orders) Bookings(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, from, to time.Time) ([]Order, error) {
	rows, err := tx.Query(ctx, `SELECT `+orderCols+` FROM storefront_orders WHERE tenant_id = $1 AND kind = 'booking' AND status = ANY ($2)
		AND starts_at >= $3 AND starts_at < $4 ORDER BY starts_at LIMIT 200`, tenant, liveBooking, from, to)
	if err != nil {
		return nil, err
	}
	list, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (Order, error) { return scanOrder(r) })
	if err != nil {
		return nil, err
	}
	for i := range list {
		if err := o.open(ctx, tx, tenant, &list[i]); err != nil {
			return nil, err
		}
	}
	return list, nil
}

// --- changes: the customer's (from their status link) and the owner's ---

// ErrSlotTaken refuses a move to a time that is not free.
var ErrSlotTaken = errors.New("storefront: that time is not free")

// OwnerCancel cancels a booking for the owner (from chat): the slot is
// freed at once; the run is ended and the customer told by ProcessChanges.
func (o *Orders) OwnerCancel(ctx context.Context, tenant uuid.UUID, reference, actor string) (Order, error) {
	var ord Order
	err := db.InTenantTx(ctx, o.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		var err error
		if ord, err = o.liveBookingTx(ctx, tx, tenant, reference); err != nil {
			return err
		}
		next := "cancelled"
		if ord.PaidAt != nil {
			next = "refund_due"
		}
		if _, err := tx.Exec(ctx, `UPDATE storefront_orders SET status = $3, decided_by = COALESCE(decided_by, $4), updated_at = now() WHERE tenant_id = $1 AND id = $2`,
			tenant, ord.ID, next, actor); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `DELETE FROM storefront_holds WHERE reference = $1`, reference); err != nil {
			return err
		}
		ord.Status = next
		return o.queueChange(ctx, tx, tenant, ord, "cancel", actor, nil)
	})
	return ord, err
}

// OwnerMove moves a booking for the owner (from chat) to start, if that
// time is free; the owner may move it outside the booking hours.
func (o *Orders) OwnerMove(ctx context.Context, tenant uuid.UUID, reference string, start time.Time, actor string) (Order, error) {
	var ord Order
	err := db.InTenantTx(ctx, o.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		var err error
		if ord, err = o.liveBookingTx(ctx, tx, tenant, reference); err != nil {
			return err
		}
		if !start.After(o.now()) {
			return fmt.Errorf("%w: the new time has passed", ErrInvalid)
		}
		it, ok, err := ServiceItem(ctx, tx, tenant, ord.ServiceID)
		if err != nil {
			return err
		}
		svc, bookable := ServiceOf(it)
		if !ok || !bookable {
			// The service went from the table: keep the booking's length.
			svc = Service{ID: ord.ServiceID, Duration: ord.EndsAt.Sub(*ord.StartsAt), Capacity: 1}
		}
		end, until := start.Add(svc.Duration), start.Add(svc.Duration+svc.Buffer)
		if _, err := tx.Exec(ctx, `SELECT taskiem_storefront_lock($1)`, ord.storefront); err != nil {
			return err
		}
		var free bool
		if err := tx.QueryRow(ctx, `SELECT taskiem_storefront_slot_free($1, $2, $3, $4, $5, $6)`, ord.storefront, reference, ord.ServiceID, start, until, svc.Capacity).Scan(&free); err != nil {
			return err
		}
		if !free {
			return ErrSlotTaken
		}
		from := *ord.StartsAt
		if _, err := tx.Exec(ctx, `UPDATE storefront_holds SET starts_at = $2, until_at = $3 WHERE reference = $1`, reference, start, until); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE storefront_orders SET starts_at = $3, ends_at = $4, version = version + 1, updated_at = now() WHERE tenant_id = $1 AND id = $2`,
			tenant, ord.ID, start, end); err != nil {
			return err
		}
		ord.StartsAt, ord.EndsAt = &start, &end
		ord.Version++
		return o.queueChange(ctx, tx, tenant, ord, "move", actor, map[string]any{"from": from, "to": start})
	})
	return ord, err
}

func (o *Orders) liveBookingTx(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, reference string) (Order, error) {
	ord, err := scanOrder(tx.QueryRow(ctx, `SELECT `+orderCols+` FROM storefront_orders WHERE tenant_id = $1 AND reference = $2 FOR UPDATE`, tenant, reference))
	if err != nil {
		return ord, err
	}
	if ord.Kind != "booking" || !contains(liveBooking, ord.Status) {
		return ord, fmt.Errorf("%w: %s is not a booking that can be changed (%s)", ErrDecision, reference, ord.Status)
	}
	return ord, nil
}

func (o *Orders) queueChange(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, ord Order, action, actor string, detail map[string]any) error {
	if detail == nil {
		detail = map[string]any{}
	}
	raw, _ := json.Marshal(detail)
	if _, err := tx.Exec(ctx, `INSERT INTO storefront_changes (id, tenant_id, order_id, action, by_whom, detail) VALUES ($1, $2, $3, $4, 'owner', $5)`,
		uuid.Must(uuid.NewV7()), tenant, ord.ID, action, raw); err != nil {
		return err
	}
	return audit(ctx, tx, tenant, "storefront.booking_"+action, ord.Reference, map[string]any{"by": actor, "channel": "chat"})
}

type change struct {
	id, tenant, order uuid.UUID
	action, by        string
	detail            map[string]any
}

// ProcessChanges moves runs on after changes: a cancelled booking's run
// ends, a moved one's ends and a new one starts at the new time, an order
// whose new prices were accepted goes ahead. The owner hears of the
// customer's changes, the customer of the owner's.
func (o *Orders) ProcessChanges(ctx context.Context) error {
	rows, err := o.Pool.Query(ctx, `SELECT id, tenant_id, order_id, action, by_whom, detail FROM taskiem_storefront_changes_claim(20)`)
	if err != nil {
		return err
	}
	list, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (change, error) {
		var c change
		return c, r.Scan(&c.id, &c.tenant, &c.order, &c.action, &c.by, &c.detail)
	})
	if err != nil {
		return err
	}
	var errs []error
	for _, c := range list {
		if err := o.applyChange(ctx, c); err != nil {
			errs = append(errs, fmt.Errorf("change %s: %w", c.id, err))
			continue
		}
		if _, err := o.Pool.Exec(ctx, `SELECT taskiem_storefront_change_done($1)`, c.id); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func (o *Orders) applyChange(ctx context.Context, c change) error {
	var ord Order
	var slug, connection, key string
	err := db.InTenantTx(ctx, o.Pool, []uuid.UUID{c.tenant}, func(tx pgx.Tx) error {
		var err error
		if ord, err = scanOrder(tx.QueryRow(ctx, `SELECT `+orderCols+` FROM storefront_orders WHERE tenant_id = $1 AND id = $2`, c.tenant, c.order)); err != nil {
			return err
		}
		if err := o.open(ctx, tx, c.tenant, &ord); err != nil {
			return err
		}
		if err := tx.QueryRow(ctx, `SELECT COALESCE(s.published_slug, s.slug), COALESCE(a.options->>'connection', '') FROM storefronts s
			LEFT JOIN storefront_actions a ON a.storefront_id = s.id AND a.kind = $2 WHERE s.id = $1`, ord.storefront, ord.Kind).Scan(&slug, &connection); err != nil {
			return err
		}
		if c.action == "price_accept" {
			if key, err = o.statusKeyOf(ctx, tx, c.tenant, ord.ID); err != nil {
				return err
			}
			return o.acceptPrices(ctx, tx, c.tenant, &ord)
		}
		return nil
	})
	if errors.Is(err, ErrNoOrder) {
		return nil
	}
	if err != nil {
		return err
	}
	switch c.action {
	case "price_accept":
		return o.carryOn(ctx, c.tenant, ord, slug, connection, ord.Customer, key)
	case "price_decline":
		return nil
	case "cancel":
		text := ""
		if c.by == "owner" {
			text = "Your booking " + ord.Reference + " (" + ord.ServiceName + ", " + When(*ord.StartsAt) + ") has been cancelled by the business."
			if ord.Status == "refund_due" {
				text += " Your payment will be refunded."
			}
		}
		if err := o.endBooking(ctx, c.tenant, ord.Reference, ord.RunID, text, "cancel:"+c.id.String()); err != nil {
			return err
		}
		if c.by == "customer" {
			body := ord.ServiceName + ", " + When(*ord.StartsAt) + "."
			if ord.Status == "refund_due" {
				body += " It was paid: reply *refund " + ord.Reference + "* in chat, or refund it from the order in the web app."
			}
			return o.tellOwner(ctx, c.tenant, c.id, "Booking "+ord.Reference+" cancelled by the customer", body)
		}
		return nil
	case "move":
		if err := o.endBooking(ctx, c.tenant, ord.Reference, ord.RunID, "", ""); err != nil {
			return err
		}
		if err := o.startRun(ctx, c.tenant, ord.Reference); err != nil {
			return err
		}
		if c.by == "customer" {
			from, _ := c.detail["from"].(string)
			body := ord.ServiceName + ": now " + When(*ord.StartsAt)
			if t, err := time.Parse(time.RFC3339Nano, from); err == nil {
				body += ", was " + When(t)
			}
			return o.tellOwner(ctx, c.tenant, c.id, "Booking "+ord.Reference+" moved by the customer", body+".")
		}
		return nil
	}
	return nil
}

// acceptPrices puts the confirmed prices on an order and makes it wait for
// its payment when it is paid at once.
func (o *Orders) acceptPrices(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, ord *Order) error {
	if ord.PriceChange == nil || ord.Status != "received" {
		return nil
	}
	pc := ord.PriceChange
	basket := ord.Basket
	if ord.Kind == "order" {
		basket = pc.Basket
	}
	raw, _ := json.Marshal(basket)
	var deposit *int64
	if pc.DepositKobo > 0 && pc.DepositKobo < pc.TotalKobo {
		deposit = &pc.DepositKobo
	}
	status := "received"
	var expires *time.Time
	payable := pc.TotalKobo
	if deposit != nil {
		payable = *deposit
	}
	if ord.Payment == "at_order" && payable >= 100 {
		status = "awaiting_payment"
		e := o.now().Add(PaymentTTL)
		expires = &e
	}
	if _, err := tx.Exec(ctx, `UPDATE storefront_orders SET basket = $3, total_kobo = $4, deposit_kobo = $5, price_change = NULL, status = $6, expires_at = $7,
		updated_at = now() WHERE tenant_id = $1 AND id = $2`, tenant, ord.ID, raw, pc.TotalKobo, deposit, status, expires); err != nil {
		return err
	}
	if ord.Kind == "booking" {
		if _, err := tx.Exec(ctx, `UPDATE storefront_holds SET expires_at = $2 WHERE reference = $1`, ord.Reference, expires); err != nil {
			return err
		}
	}
	ord.Basket, ord.TotalKobo, ord.DepositKobo, ord.PriceChange, ord.Status, ord.ExpiresAt = basket, pc.TotalKobo, deposit, nil, status, expires
	return audit(ctx, tx, tenant, "storefront.order_prices_accepted", ord.Reference, map[string]any{"total_kobo": pc.TotalKobo})
}

func (o *Orders) tellOwner(ctx context.Context, tenant, dedup uuid.UUID, title, body string) error {
	return db.InTenantTx(ctx, o.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		_, err := alerts.Notify(ctx, tx, tenant, alerts.Alert{Kind: AlertBookingChanged, Dedup: dedup.String(), Title: title, Body: body})
		return err
	})
}

// --- the sweeper ---

// CheckBookings applies changes, expires what waited too long (a booking
// the owner never answered before its time, new prices never confirmed),
// sends owners the day's bookings once a morning, and forgets old holds.
func (o *Orders) CheckBookings(ctx context.Context) error {
	errs := []error{o.ProcessChanges(ctx)}
	rows, err := o.Pool.Query(ctx, `SELECT tenant_id FROM taskiem_storefront_booking_tenants()`)
	if err != nil {
		return errors.Join(append(errs, err)...)
	}
	tenants, err := pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
	if err != nil {
		return errors.Join(append(errs, err)...)
	}
	for _, t := range tenants {
		if err := o.sweepTenant(ctx, t); err != nil {
			errs = append(errs, fmt.Errorf("tenant %s: %w", t, err))
		}
	}
	if _, err := o.Pool.Exec(ctx, `SELECT taskiem_storefront_holds_sweep()`); err != nil {
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

type stale struct {
	reference, kind string
	run             *uuid.UUID
}

func (o *Orders) sweepTenant(ctx context.Context, tenant uuid.UUID) error {
	var gone []stale
	var today []Order
	send := false
	now := o.now()
	err := db.InTenantTx(ctx, o.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `UPDATE storefront_orders SET status = 'expired', updated_at = now()
			WHERE tenant_id = $1 AND ((kind = 'booking' AND status = 'received' AND starts_at < $2) OR (status = 'price_changed' AND expires_at < $2))
			RETURNING reference, kind, run_id`, tenant, now)
		if err != nil {
			return err
		}
		if gone, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (stale, error) {
			var s stale
			return s, r.Scan(&s.reference, &s.kind, &s.run)
		}); err != nil {
			return err
		}
		for _, g := range gone {
			if _, err := tx.Exec(ctx, `DELETE FROM storefront_holds WHERE reference = $1`, g.reference); err != nil {
				return err
			}
			if err := audit(ctx, tx, tenant, "storefront.order_expired", g.reference, nil); err != nil {
				return err
			}
		}
		local := now.In(Lagos)
		if local.Hour() < SummaryHour {
			return nil
		}
		day := midnight(local)
		tag, err := tx.Exec(ctx, `UPDATE storefronts SET bookings_sent_on = $2 WHERE tenant_id = $1 AND (bookings_sent_on IS NULL OR bookings_sent_on < $2)
			AND EXISTS (SELECT 1 FROM storefront_actions a WHERE a.storefront_id = storefronts.id AND a.kind = 'booking' AND a.enabled)`, tenant, day.Format(time.DateOnly))
		if err != nil || tag.RowsAffected() == 0 {
			return err
		}
		send = true
		today, err = o.Bookings(ctx, tx, tenant, day, day.AddDate(0, 0, 1))
		return err
	})
	if err != nil {
		return err
	}
	var errs []error
	for _, g := range gone {
		if g.kind != "booking" {
			continue
		}
		if err := o.endBooking(ctx, tenant, g.reference, g.run, "Sorry, your booking "+g.reference+" could not be confirmed in time. You are welcome to book another time.", "unanswered:"+g.reference); err != nil {
			errs = append(errs, err)
		}
	}
	if send && len(today) > 0 {
		lines := make([]string, len(today))
		for i, b := range today {
			lines[i] = b.StartsAt.In(Lagos).Format("15:04") + " " + b.ServiceName + " (" + b.Reference + ")"
			if b.Status == "received" {
				lines[i] += ", not confirmed yet"
			}
		}
		title := fmt.Sprintf("Today's bookings: %d", len(today))
		err := db.InTenantTx(ctx, o.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
			_, err := alerts.Notify(ctx, tx, tenant, alerts.Alert{Kind: AlertBookingsToday, Dedup: now.In(Lagos).Format(time.DateOnly), Title: title,
				Body: strings.Join(lines, "\n")})
			return err
		})
		if err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}
