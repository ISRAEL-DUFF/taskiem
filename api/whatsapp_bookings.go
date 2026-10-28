package api

import (
	"context"
	"errors"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/storefront"
	"github.com/israel-duff/taskiem/engine/whatsapp"
)

// The owner's booking commands in chat (docs/spec/storefronts.md 6.8):
// "bookings" and "today" list them; "booking <ref> cancel", "move <ref>
// to Sat 11:00" and "off 25 Dec" change them, each shown and confirmed
// with yes, as table rows are. They need storefront.manage.

// chatBooking is a booking change being confirmed.
type chatBooking struct {
	Action    string    `json:"action"` // cancel, move, off, refund or refunded
	Reference string    `json:"reference,omitempty"`
	Start     time.Time `json:"start,omitempty"` // move: the new start; off: the day
}

var (
	bookingRef    = `([a-z]{2}-[a-z0-9]{6})`
	bookingCancel = regexp.MustCompile(`^(?:booking ` + bookingRef + ` cancel|cancel booking ` + bookingRef + `)$`)
	bookingMove   = regexp.MustCompile(`^(?:move|move booking) ` + bookingRef + ` to (.+)$`)
	bookingOff    = regexp.MustCompile(`^(?:off|day off|take off) (.+)$`)
	orderRefund   = regexp.MustCompile(`^refund ` + bookingRef + `$`)
	// orderRefunded marks an order refunded by hand (N3).
	orderRefunded = regexp.MustCompile(`^refunded ` + bookingRef + `$`)
)

// waBookingCommand acts on a booking command; false when cmd is not one.
func (s *Server) waBookingCommand(ctx context.Context, c *chat, cmd string) (bool, error) {
	if s.Orders == nil {
		return false, nil
	}
	var b *chatBooking
	switch {
	case cmd == "bookings" || cmd == "my bookings" || cmd == "appointments":
		return true, s.waBookings(ctx, c, false)
	case cmd == "today" || cmd == "bookings today" || cmd == "today's bookings":
		return true, s.waBookings(ctx, c, true)
	case bookingCancel.MatchString(cmd):
		m := bookingCancel.FindStringSubmatch(cmd)
		b = &chatBooking{Action: "cancel", Reference: strings.ToUpper(m[1] + m[2])}
	case bookingMove.MatchString(cmd):
		m := bookingMove.FindStringSubmatch(cmd)
		at, ok := parseWhen(m[2], time.Now(), true)
		if !ok {
			return true, c.say(ctx, s, c.t("wa.booking.bad_time"))
		}
		b = &chatBooking{Action: "move", Reference: strings.ToUpper(m[1]), Start: at}
	case orderRefund.MatchString(cmd):
		b = &chatBooking{Action: "refund", Reference: strings.ToUpper(orderRefund.FindStringSubmatch(cmd)[1])}
	case orderRefunded.MatchString(cmd):
		b = &chatBooking{Action: "refunded", Reference: strings.ToUpper(orderRefunded.FindStringSubmatch(cmd)[1])}
	case bookingOff.MatchString(cmd):
		day, ok := parseWhen(bookingOff.FindStringSubmatch(cmd)[1], time.Now(), false)
		if !ok {
			return true, c.say(ctx, s, c.t("wa.booking.bad_day"))
		}
		b = &chatBooking{Action: "off", Start: day}
	default:
		return false, nil
	}
	if !c.p.Can(PermStorefrontManage) {
		return true, c.say(ctx, s, c.t("wa.booking.denied"))
	}
	return true, s.waBookingAsk(ctx, c, b)
}

// waBookings lists today's bookings, or the next seven days'.
func (s *Server) waBookings(ctx context.Context, c *chat, today bool) error {
	if !c.p.Can(PermStorefrontManage) {
		return c.say(ctx, s, c.t("wa.booking.denied"))
	}
	now := time.Now().In(storefront.Lagos)
	from := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, storefront.Lagos)
	to, head, none := from.AddDate(0, 0, 7), c.t("wa.booking.list_week"), c.t("wa.booking.none_week")
	if today {
		to, head, none = from.AddDate(0, 0, 1), c.t("wa.booking.list_today"), c.t("wa.booking.none_today")
	}
	var list []storefront.Order
	if err := chatTx(ctx, s, c, func(tx pgx.Tx) error {
		var err error
		list, err = s.Orders.Bookings(ctx, tx, c.tenant.ID, from, to)
		return err
	}); err != nil {
		return err
	}
	if len(list) == 0 {
		return c.say(ctx, s, none)
	}
	lines := []string{head}
	day := ""
	for _, b := range list {
		at := b.StartsAt.In(storefront.Lagos)
		if d := at.Format("Mon 2 Jan"); !today && d != day {
			lines, day = append(lines, "", "*"+d+"*"), d
		}
		cust := b.Customer
		maskCustomer(&cust)
		line := "• " + at.Format("15:04") + " " + whatsapp.SafeText(b.ServiceName) + ", " + cust.Name + " " + cust.Phone + " (" + b.Reference + ")"
		if b.Status == "received" {
			line += " " + c.t("wa.booking.unconfirmed")
		}
		lines = append(lines, line)
	}
	return c.say(ctx, s, strings.Join(lines, "\n"))
}

// waBookingAsk shows the change and waits for yes.
func (s *Server) waBookingAsk(ctx context.Context, c *chat, b *chatBooking) error {
	var text string
	if b.Action == "off" {
		var n int
		err := chatTx(ctx, s, c, func(tx pgx.Tx) error {
			list, err := s.Orders.Bookings(ctx, tx, c.tenant.ID, b.Start, b.Start.AddDate(0, 0, 1))
			n = len(list)
			return err
		})
		if err != nil {
			return err
		}
		text = c.t("wa.booking.confirm_off", "day", b.Start.Format("Mon 2 Jan"), "count", strconv.Itoa(n))
	} else {
		var o storefront.Order
		err := chatTx(ctx, s, c, func(tx pgx.Tx) error {
			var err error
			o, err = s.Orders.GetOrder(ctx, tx, c.tenant.ID, b.Reference)
			return err
		})
		if b.Action == "refund" || b.Action == "refunded" {
			if errors.Is(err, storefront.ErrNoOrder) || (err == nil && o.Status != "refund_due") {
				return c.say(ctx, s, c.t("wa.booking.nothing_to_refund", "ref", b.Reference))
			}
			if err != nil {
				return err
			}
			total := storefront.FormatNaira(o.Payable())
			ask := c.t("wa.booking.confirm_refunded", "ref", b.Reference, "total", total)
			if b.Action == "refund" {
				if o.PaidBy != "card" {
					// Paid by bank transfer: Taskiem cannot send it back (N3).
					return c.say(ctx, s, c.t("wa.booking.refund_by_hand", "ref", b.Reference, "total", total))
				}
				ask = c.t("wa.booking.confirm_refund", "ref", b.Reference, "total", total)
			}
			if err := s.saveSession(ctx, c, stateConfirming, chatData{Booking: b}, waPendingTTL); err != nil {
				return err
			}
			return c.say(ctx, s, ask, bookingButtons(c)...)
		}
		if errors.Is(err, storefront.ErrNoOrder) || (err == nil && (o.Kind != "booking" || o.StartsAt == nil)) {
			return c.say(ctx, s, c.t("wa.booking.not_found", "ref", b.Reference))
		}
		if err != nil {
			return err
		}
		if b.Action == "cancel" {
			text = c.t("wa.booking.confirm_cancel", "ref", b.Reference, "service", whatsapp.SafeText(o.ServiceName), "when", storefront.When(*o.StartsAt))
		} else {
			text = c.t("wa.booking.confirm_move", "ref", b.Reference, "service", whatsapp.SafeText(o.ServiceName), "from", storefront.When(*o.StartsAt),
				"to", storefront.When(b.Start))
		}
	}
	if err := s.saveSession(ctx, c, stateConfirming, chatData{Booking: b}, waPendingTTL); err != nil {
		return err
	}
	return c.say(ctx, s, text, bookingButtons(c)...)
}

func bookingButtons(c *chat) []whatsapp.Button {
	return []whatsapp.Button{{ID: "yes", Title: c.t("button.yes_save")}, {ID: "cancel", Title: c.t("button.cancel")}}
}

// waBookingApply makes the confirmed change. Moves and cancels reach the
// customer through the order sweeper (storefront.ProcessChanges).
func (s *Server) waBookingApply(ctx context.Context, c *chat) error {
	b := c.data.Booking
	if err := s.saveSession(ctx, c, stateIdle, chatData{}, 0); err != nil {
		return err
	}
	if !c.p.Can(PermStorefrontManage) {
		return c.say(ctx, s, c.t("wa.booking.denied"))
	}
	actor := c.p.Actor()
	var err error
	switch b.Action {
	case "cancel":
		var o storefront.Order
		if o, err = s.Orders.OwnerCancel(ctx, c.tenant.ID, b.Reference, actor); err == nil {
			msg := c.t("wa.booking.cancelled", "ref", b.Reference)
			if o.Status == "refund_due" {
				msg += " " + c.t("wa.booking.refund_hint", "ref", b.Reference)
			}
			return c.say(ctx, s, msg)
		}
	case "refund":
		var o storefront.Order
		if o, err = s.Orders.Refund(ctx, c.tenant.ID, b.Reference, actor); err == nil {
			return c.say(ctx, s, c.t("wa.booking.refunded", "ref", b.Reference, "total", storefront.FormatNaira(o.Payable())))
		}
		if errors.Is(err, storefront.ErrRefund) {
			return c.say(ctx, s, c.t("wa.booking.refund_failed", "ref", b.Reference, "why", plainError(err)))
		}
		if errors.Is(err, storefront.ErrRefundByHand) {
			return c.say(ctx, s, c.t("wa.booking.refund_by_hand", "ref", b.Reference, "total", storefront.FormatNaira(o.Payable())))
		}
	case "refunded":
		var o storefront.Order
		if o, err = s.Orders.MarkRefunded(ctx, c.tenant.ID, b.Reference, actor, ""); err == nil {
			return c.say(ctx, s, c.t("wa.booking.marked_refunded", "ref", b.Reference, "total", storefront.FormatNaira(o.Payable())))
		}
	case "move":
		if _, err = s.Orders.OwnerMove(ctx, c.tenant.ID, b.Reference, b.Start, actor); err == nil {
			return c.say(ctx, s, c.t("wa.booking.moved", "ref", b.Reference, "to", storefront.When(b.Start)))
		}
		if errors.Is(err, storefront.ErrSlotTaken) {
			return c.say(ctx, s, c.t("wa.booking.taken", "to", storefront.When(b.Start)))
		}
	case "off":
		var added bool
		err = chatTx(ctx, s, c, func(tx pgx.Tx) error {
			var err error
			if added, err = storefront.AddDayOff(ctx, tx, c.tenant.ID, b.Start); err != nil || !added {
				return err
			}
			return waAudit(ctx, tx, c.ch.Name(), c.p, "storefront.day_off", b.Start.Format(time.DateOnly), nil)
		})
		if err == nil {
			if !added {
				return c.say(ctx, s, c.t("wa.booking.off_already", "day", b.Start.Format("Mon 2 Jan")))
			}
			return c.say(ctx, s, c.t("wa.booking.off_done", "day", b.Start.Format("Mon 2 Jan")))
		}
	}
	switch {
	case errors.Is(err, storefront.ErrNoOrder), errors.Is(err, storefront.ErrDecision):
		return c.say(ctx, s, c.t("wa.booking.not_found", "ref", b.Reference))
	case errors.Is(err, storefront.ErrInvalid):
		return c.say(ctx, s, plainError(err))
	}
	return err
}

func chatTx(ctx context.Context, s *Server, c *chat, fn func(pgx.Tx) error) error {
	return db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{c.tenant.ID}, fn)
}

var (
	weekdays = map[string]time.Weekday{"sun": time.Sunday, "mon": time.Monday, "tue": time.Tuesday, "tues": time.Tuesday, "wed": time.Wednesday,
		"thu": time.Thursday, "thur": time.Thursday, "thurs": time.Thursday, "fri": time.Friday, "sat": time.Saturday}
	months = map[string]time.Month{"jan": 1, "feb": 2, "mar": 3, "apr": 4, "may": 5, "jun": 6, "jul": 7, "aug": 8, "sep": 9, "sept": 9, "oct": 10, "nov": 11, "dec": 12}
	clock  = regexp.MustCompile(`^(\d{1,2})(?::(\d{2}))?\s*(am|pm)?$`)
)

// parseWhen reads a day ("today", "tomorrow", "sat", "saturday", "24 oct",
// "oct 24", "2026-10-24", "24/10") and, when withTime, a time after it
// ("11:00", "11am", "2:30pm", "14:30"), in Lagos. A weekday or a date
// without a year is the next one from now.
func parseWhen(s string, now time.Time, withTime bool) (time.Time, bool) {
	now = now.In(storefront.Lagos)
	f := strings.Fields(strings.ToLower(strings.TrimSpace(s)))
	hh, mm := 0, 0
	if withTime {
		if len(f) < 2 {
			return time.Time{}, false
		}
		t := f[len(f)-1]
		if (t == "am" || t == "pm") && len(f) >= 3 {
			t, f = f[len(f)-2]+t, f[:len(f)-1]
		}
		m := clock.FindStringSubmatch(t)
		if m == nil {
			return time.Time{}, false
		}
		hh, _ = strconv.Atoi(m[1])
		if m[2] != "" {
			mm, _ = strconv.Atoi(m[2])
		}
		switch m[3] {
		case "pm":
			if hh < 12 {
				hh += 12
			}
		case "am":
			if hh == 12 {
				hh = 0
			}
		}
		if hh > 23 || mm > 59 {
			return time.Time{}, false
		}
		f = f[:len(f)-1]
		if len(f) > 0 && f[len(f)-1] == "at" {
			f = f[:len(f)-1]
		}
	}
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, storefront.Lagos)
	day, ok := parseDay(f, today)
	if !ok {
		return time.Time{}, false
	}
	return day.Add(time.Duration(hh)*time.Hour + time.Duration(mm)*time.Minute), true
}

func parseDay(f []string, today time.Time) (time.Time, bool) {
	switch {
	case len(f) == 1 && f[0] == "today":
		return today, true
	case len(f) == 1 && f[0] == "tomorrow":
		return today.AddDate(0, 0, 1), true
	case len(f) == 1:
		if wd, ok := weekday(f[0]); ok {
			return today.AddDate(0, 0, (int(wd)-int(today.Weekday())+7)%7), true
		}
		if t, err := time.ParseInLocation(time.DateOnly, f[0], storefront.Lagos); err == nil {
			return t, true
		}
		if p := strings.Split(f[0], "/"); len(p) == 2 {
			d, e1 := strconv.Atoi(p[0])
			m, e2 := strconv.Atoi(p[1])
			if e1 == nil && e2 == nil && m >= 1 && m <= 12 {
				return nextDate(today, time.Month(m), d)
			}
		}
	case len(f) == 2:
		d, err := strconv.Atoi(strings.TrimRight(f[0], "stndrh"))
		mon := f[1]
		if err != nil {
			d, err = strconv.Atoi(strings.TrimRight(f[1], "stndrh"))
			mon = f[0]
		}
		if err != nil || len(mon) < 3 {
			return time.Time{}, false
		}
		if m, ok := months[mon[:3]]; ok {
			return nextDate(today, m, d)
		}
	}
	return time.Time{}, false
}

// weekday reads "sat", "saturday", "tues" and the like.
func weekday(w string) (time.Weekday, bool) {
	if wd, ok := weekdays[w]; ok {
		return wd, true
	}
	if len(w) > 3 && strings.HasSuffix(w, "day") {
		wd, ok := weekdays[w[:3]]
		return wd, ok
	}
	return 0, false
}

// nextDate is day d of month m, this year or, once past, next.
func nextDate(today time.Time, m time.Month, d int) (time.Time, bool) {
	t := time.Date(today.Year(), m, d, 0, 0, 0, 0, storefront.Lagos)
	if t.Month() != m {
		return time.Time{}, false // 31 Feb
	}
	if t.Before(today) {
		t = t.AddDate(1, 0, 0)
	}
	return t, true
}
