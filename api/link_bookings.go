package api

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/links"
	"github.com/israel-duff/taskiem/engine/storefront"
	"github.com/israel-duff/taskiem/engine/whatsapp"
)

// Book in a business's conversation (docs/spec/business-chat-tools.md §3,
// §6 item 3): the other business's services, as their storefront shows
// them, the free times from their availability, and a booking made
// through their storefront's booking. Everything is read in their
// business through the door (links.EnterTx), only for an active link, and
// only what their storefront shows: services marked to show on it,
// whether it is published or not (§7.1), and which times are free, never
// whose. A booking goes into their storefront's inbox as one from their
// page would, and is recorded there by the same worker: they see it in
// their bookings like any other, our business its customer. Then a
// message in the conversation says so to both sides.
//
// Looking (the services, the times) is anyone's who reads the
// conversation; booking writes in it and deals with the other business
// for ours, so it takes chat.trade, as a message does. Nothing on our side
// changes but the conversation and our note of the booking, so neither
// storefront.manage (our own storefront) nor table.edit (our tables) is
// asked for.

func (s *Server) linkBookingRoutes(r chi.Router) {
	r.Get("/link-chats/{link}/services", s.linkServices)
	r.Get("/link-chats/{link}/services/{id}/times", s.linkServiceTimes)
	r.With(s.need(PermChatTrade), s.tenantWide).Post("/link-chats/{link}/bookings", s.bookLinkService)
}

// linkService is one of their services as the Book sheet shows it.
type linkService struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Minutes     int    `json:"minutes"`
	PriceKobo   *int64 `json:"price_kobo,omitempty"`
	Description string `json:"description,omitempty"`
	Category    string `json:"category,omitempty"`
}

// theirBooking is what their storefront's booking offers, read in their
// business.
type theirBooking struct {
	name       string
	storefront uuid.UUID
	slug       string
	action     *uuid.UUID
	open       bool
	services   []storefront.Item
	avail      storefront.Availability
}

// errLookedOnly ends a transaction that only looked through the door: it
// is undone, so a look leaves nothing behind in either business.
var errLookedOnly = errors.New("looked only")

// throughDoor runs before in our business, then fn in the other side of an
// active link; the transaction is undone after: reading what their
// storefront shows leaves no trace, as reading their page does not.
func (s *Server) throughDoor(r *http.Request, link uuid.UUID, before func(tx pgx.Tx, l links.Link) error, fn func(tx pgx.Tx, l links.Link, other uuid.UUID) error) error {
	p := principalFrom(r.Context())
	err := s.tx(r, func(tx pgx.Tx) error {
		l, err := links.GetTx(r.Context(), tx, link)
		if err != nil {
			return err
		}
		if l.State != links.Active {
			return fmt.Errorf("%w: you are no longer linked with %s", links.ErrState, l.Name)
		}
		if before != nil {
			if err := before(tx, l); err != nil {
				return err
			}
		}
		other, err := links.EnterTx(r.Context(), tx, link, "link.booking", nil, links.Actor{Type: p.ActorType(), ID: p.Actor()})
		if err != nil {
			return err
		}
		if err := fn(tx, l, other); err != nil {
			return err
		}
		return errLookedOnly
	})
	if errors.Is(err, errLookedOnly) {
		return nil
	}
	return err
}

// theirBookingTx reads the booking of the business in scope (the other
// side, through the door).
func theirBookingTx(ctx context.Context, tx pgx.Tx, other uuid.UUID, name string) (theirBooking, error) {
	b := theirBooking{name: name}
	sf, err := storefront.Get(ctx, tx, other)
	if errors.Is(err, storefront.ErrNotFound) {
		return b, nil
	}
	if err != nil {
		return b, err
	}
	b.storefront, b.slug = sf.ID, sf.Slug
	if sf.PublishedSlug != nil && *sf.PublishedSlug != "" {
		b.slug = *sf.PublishedSlug
	}
	b.avail = sf.Availability.Effective(sf.Draft.Hours)
	items, err := storefront.Services(ctx, tx, other)
	if err != nil {
		return b, err
	}
	seen := map[string]bool{}
	for _, it := range items {
		if _, ok := storefront.ServiceOf(it); ok && !seen[it.ID] {
			seen[it.ID] = true
			b.services = append(b.services, it)
		}
	}
	var id uuid.UUID
	var on bool
	err = tx.QueryRow(ctx, `SELECT a.id, a.enabled AND COALESCE(f.enabled, false) FROM storefront_actions a
		LEFT JOIN storefront_fronts f ON f.id = a.front_id WHERE a.storefront_id = $1 AND a.kind = 'booking' AND a.builtin`, sf.ID).Scan(&id, &on)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return b, err
	}
	if err == nil {
		b.action = &id
		b.open = on && sf.Status != "closed" && sf.Status != "suspended" && sf.OrdersPaused == nil
	}
	return b, nil
}

// busyTx is what is booked or held on their calendar between two times:
// how many at each time, never whose.
func busyTx(ctx context.Context, tx pgx.Tx, sf uuid.UUID, from, to time.Time) ([]storefront.Busy, error) {
	rows, err := tx.Query(ctx, `SELECT item_id, starts_at, until_at, count(*)::int FROM storefront_claims
		WHERE storefront_id = $1 AND block_id IS NULL AND (expires_at IS NULL OR expires_at > now()) AND starts_at < $3 AND $2 < until_at
		GROUP BY item_id, starts_at, until_at`, sf, from, to)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (storefront.Busy, error) {
		var b storefront.Busy
		return b, r.Scan(&b.Service, &b.Start, &b.Until, &b.Count)
	})
}

// bookingWindow is the span of their calendar that can be booked from now.
func bookingWindow(a storefront.Availability, now time.Time) (time.Time, time.Time) {
	t := now.In(storefront.Lagos)
	from := time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, storefront.Lagos)
	return from, from.AddDate(0, 0, a.AheadDays+2)
}

func (s *Server) bookingsRecorded() bool {
	return s.Orders != nil && len(s.Orders.InboxKey) > 0
}

func serviceOut(it storefront.Item) linkService {
	out := linkService{ID: it.ID, Name: it.Name, Minutes: it.DurationMinutes, Description: it.Description, Category: it.Category}
	if it.PriceKobo != nil && *it.PriceKobo > 0 {
		out.PriceKobo = it.PriceKobo
	}
	return out
}

type linkServicesOut struct {
	Services []linkService `json:"services"`
	// Open: they take bookings online now; Closed says so when not.
	Open   bool   `json:"open"`
	Closed string `json:"closed,omitempty"`
	// Phone is the number given to them with a booking: our storefront's,
	// where it has one.
	Phone string `json:"phone"`
}

// linkServices is GET /v1/link-chats/{link}/services: their services
// that can be booked, as their storefront shows them, and whether they
// take bookings online now.
func (s *Server) linkServices(w http.ResponseWriter, r *http.Request) {
	link, err := linkParam(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	p := principalFrom(r.Context())
	var b theirBooking
	var phone string
	err = s.throughDoor(r, link, func(tx pgx.Tx, _ links.Link) error {
		phone, err = ourPhoneTx(r.Context(), tx, p.TenantID)
		return err
	}, func(tx pgx.Tx, l links.Link, other uuid.UUID) error {
		var err error
		b, err = theirBookingTx(r.Context(), tx, other, l.Name)
		return err
	})
	if err != nil {
		s.fail(w, r, linkMessageErr(err))
		return
	}
	out := linkServicesOut{Services: []linkService{}, Open: b.open && s.bookingsRecorded(), Phone: phone}
	for _, it := range b.services {
		out.Services = append(out.Services, serviceOut(it))
	}
	if !out.Open {
		out.Closed = b.name + " does not take bookings online right now. Ask them here in the chat."
	}
	writeJSON(w, http.StatusOK, out)
}

// ourPhoneTx is the phone number our storefront shows, if any: the number
// a booking gives the other business to reach us.
func ourPhoneTx(ctx context.Context, tx pgx.Tx, tenant uuid.UUID) (string, error) {
	sf, err := storefront.Get(ctx, tx, tenant)
	if errors.Is(err, storefront.ErrNotFound) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	if sf.Draft.Phone != "" {
		return sf.Draft.Phone, nil
	}
	return sf.Draft.WhatsApp, nil
}

type linkTimesOut struct {
	Service linkService `json:"service"`
	// Days are the dates with a free time ("2026-11-13"), Day the one
	// whose Times these are.
	Days  []string    `json:"days"`
	Day   string      `json:"day,omitempty"`
	Times []time.Time `json:"times"`
}

// linkServiceTimes is GET /v1/link-chats/{link}/services/{id}/times?day=:
// the days with a free time for one of their services, and the free times
// of one day (?day=, or the first with any).
func (s *Server) linkServiceTimes(w http.ResponseWriter, r *http.Request) {
	link, err := linkParam(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	day := r.URL.Query().Get("day")
	if day != "" {
		if _, err := time.ParseInLocation(time.DateOnly, day, storefront.Lagos); err != nil {
			writeErr(w, http.StatusBadRequest, "day is a date like 2026-11-13")
			return
		}
	}
	id := chi.URLParam(r, "id")
	out := linkTimesOut{Days: []string{}, Times: []time.Time{}}
	err = s.throughDoor(r, link, nil, func(tx pgx.Tx, l links.Link, other uuid.UUID) error {
		b, err := theirBookingTx(r.Context(), tx, other, l.Name)
		if err != nil {
			return err
		}
		it, svc, ok := findLinkService(b.services, id)
		if !ok {
			return pgx.ErrNoRows
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
		s.fail(w, r, linkMessageErr(err))
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func findLinkService(items []storefront.Item, id string) (storefront.Item, storefront.Service, bool) {
	for _, it := range items {
		if it.ID == id {
			svc, ok := storefront.ServiceOf(it)
			return it, svc, ok
		}
	}
	return storefront.Item{}, storefront.Service{}, false
}

// linkBooking is a booking we made with a linked business.
type linkBooking struct {
	Reference   string    `json:"reference"`
	ServiceID   string    `json:"service_id"`
	ServiceName string    `json:"service_name"`
	StartsAt    time.Time `json:"starts_at"`
	EndsAt      time.Time `json:"ends_at"`
	PriceKobo   *int64    `json:"price_kobo,omitempty"`
}

// Refusals of a booking, each in plain words with a code for the sheet.
var (
	errBookingTaken  = errors.New("that time was just taken; please choose another")
	errBookingTime   = errors.New("please choose one of the times shown")
	errBookingClosed = errors.New("they do not take bookings online right now")
)

// bookLinkService is POST /v1/link-chats/{link}/bookings, with an
// Idempotency-Key: {service_id, start, note, phone} books one of their
// services at a free time, our business the customer (its name, and the
// phone given or our storefront's). A repeat of the same key answers with
// the first booking.
func (s *Server) bookLinkService(w http.ResponseWriter, r *http.Request) {
	link, err := linkParam(r)
	if err != nil {
		s.fail(w, r, err)
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
		Phone     string    `json:"phone"`
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
	p := principalFrom(r.Context())
	var got linkBooking
	var first bool
	var name string
	for range 3 {
		got, first, name, err = s.bookLinkOnce(r, p, link, key, in.ServiceID, in.Start, in.Note, in.Phone)
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
		writeJSON(w, http.StatusConflict, map[string]string{"error": name + " does not take bookings online right now. Ask them here in the chat.", "code": "bookings_closed"})
		return
	case errors.Is(err, errPhoneNeeded):
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": "Give a phone number they can reach you on.", "code": "phone_needed"})
		return
	case err != nil:
		s.fail(w, r, linkMessageErr(err))
		return
	}
	if !first {
		writeJSON(w, http.StatusOK, map[string]any{"booking": got})
		return
	}
	// Both sides see it in the conversation, said once however often the
	// request is repeated.
	when := got.StartsAt.In(storefront.Lagos)
	text := fmt.Sprintf("Booked: %s, %s at %s (%s).", got.ServiceName, when.Format("Mon 2 Jan"), when.Format("15:04"), got.Reference)
	if in.Note != "" {
		text += " " + in.Note
	}
	cid := uuid.NewSHA1(uuid.NameSpaceOID, []byte("link-booking:"+p.TenantID.String()+":"+got.Reference))
	if _, err := s.sendLink(r.Context(), p, link, text, links.SendOpts{ClientID: &cid}); err != nil && !errors.Is(err, links.ErrDuplicate) {
		s.Logger.Warn("link booking: the message to the conversation was not sent", "tenant", p.TenantID, "link", link, "err", err)
	}
	writeJSON(w, http.StatusCreated, map[string]any{"booking": got})
}

var (
	errReferenceTaken = errors.New("reference taken")
	errPhoneNeeded    = errors.New("a phone is needed")
)

// bookLinkOnce books: a look at their side first (the service, whether
// they take bookings, whether the time is offered and free), then in one
// transaction our note of it (so a repeat finds it) and, through the door,
// their slot held and the booking in their storefront's inbox; the
// database says last whether the slot is still free. first is false for a
// repeat.
func (s *Server) bookLinkOnce(r *http.Request, p *Principal, link uuid.UUID, key, serviceID string, start time.Time, note, phone string) (linkBooking, bool, string, error) {
	ctx := r.Context()
	var got linkBooking
	var name, ours string
	repeat := false
	readOurs := func(tx pgx.Tx) (bool, error) {
		err := tx.QueryRow(ctx, `SELECT reference, service_id, service_name, starts_at, ends_at, price_kobo FROM link_bookings
			WHERE tenant_id = $1 AND link_id = $2 AND idem_key = $3`, p.TenantID, link, key).
			Scan(&got.Reference, &got.ServiceID, &got.ServiceName, &got.StartsAt, &got.EndsAt, &got.PriceKobo)
		if errors.Is(err, pgx.ErrNoRows) {
			return false, nil
		}
		return err == nil, err
	}
	err := s.tx(r, func(tx pgx.Tx) error {
		var err error
		if repeat, err = readOurs(tx); err != nil || repeat {
			return err
		}
		l, err := links.GetTx(ctx, tx, link)
		if err != nil {
			return err
		}
		name = l.Name
		if l.State != links.Active {
			return fmt.Errorf("%w: you are no longer linked with %s", links.ErrState, l.Name)
		}
		if err := tx.QueryRow(ctx, `SELECT name FROM tenants WHERE id = $1`, p.TenantID).Scan(&ours); err != nil {
			return err
		}
		if strings.TrimSpace(phone) == "" {
			phone, err = ourPhoneTx(ctx, tx, p.TenantID)
		}
		return err
	})
	if err != nil || repeat {
		return got, false, name, err
	}
	if strings.TrimSpace(phone) == "" {
		return got, false, name, errPhoneNeeded
	}
	number, err := whatsapp.Normalise(phone, s.defaultCountry())
	if err != nil {
		return got, false, name, errPhoneNeeded
	}

	var b theirBooking
	var it storefront.Item
	var svc storefront.Service
	if err := s.lookThrough(ctx, p, link, func(tx pgx.Tx, other uuid.UUID) error {
		var err error
		if b, err = theirBookingTx(ctx, tx, other, name); err != nil {
			return err
		}
		var ok bool
		if it, svc, ok = findLinkService(b.services, serviceID); !ok {
			return pgx.ErrNoRows
		}
		if !b.open || b.action == nil {
			return errBookingClosed
		}
		now := time.Now()
		if !b.avail.Offers(svc, start, now, nil) {
			return errBookingTime
		}
		from, to := bookingWindow(b.avail, now)
		busy, err := busyTx(ctx, tx, b.storefront, from, to)
		if err != nil {
			return err
		}
		if !storefront.Free(svc, start, busy) {
			return errBookingTaken
		}
		return nil
	}); err != nil {
		return got, false, name, err
	}

	ref := storefront.NewReference(b.slug)
	got = linkBooking{Reference: ref, ServiceID: it.ID, ServiceName: it.Name, StartsAt: start.UTC(), EndsAt: start.Add(svc.Duration).UTC()}
	if it.PriceKobo != nil && *it.PriceKobo > 0 {
		got.PriceKobo = it.PriceKobo
	}
	statusKey, hash := storefront.NewStatusKey()
	sub := storefront.Submission{Kind: "booking", Customer: storefront.Customer{Name: ours, Phone: number, Message: note}, Payment: "none", Channel: "link",
		StatusKey: statusKey, Submitted: time.Now().UTC(),
		Booking: &storefront.Booked{ServiceID: it.ID, ServiceName: it.Name, Start: start.UTC(), End: start.Add(svc.Duration).UTC(),
			Until: start.Add(svc.Duration + svc.Buffer).UTC(), Capacity: svc.Capacity}}
	if got.PriceKobo != nil {
		sub.TotalKobo = *got.PriceKobo
	}
	payload, err := storefront.SealSubmission(s.Orders.InboxKey, b.storefront, ref, sub)
	if err != nil {
		return got, false, name, err
	}
	idem := sha256.Sum256([]byte("link-booking:" + p.TenantID.String() + ":" + link.String() + ":" + key))
	err = s.tx(r, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `INSERT INTO link_bookings (tenant_id, link_id, idem_key, reference, service_id, service_name, starts_at, ends_at, price_kobo, created_by)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10) ON CONFLICT (tenant_id, link_id, idem_key) DO NOTHING`,
			p.TenantID, link, key, ref, it.ID, it.Name, got.StartsAt, got.EndsAt, got.PriceKobo, p.Actor())
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			// The same request, made twice at once: the first stands.
			repeat, err = readOurs(tx)
			return err
		}
		if _, err := links.EnterTx(ctx, tx, link, "link.booking", nil, links.Actor{Type: p.ActorType(), ID: p.Actor()}); err != nil {
			return err
		}
		var booked *string
		err = tx.QueryRow(ctx, `SELECT taskiem_link_book($1, $2, $3, $4, $5, $6, $7, $8, $9)`, *b.action, ref, idem[:], hash, payload,
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
		return nil
	})
	return got, err == nil && !repeat, name, err
}

// lookThrough reads the other side of a link in a transaction of its own,
// undone after: a look leaves nothing behind.
func (s *Server) lookThrough(ctx context.Context, p *Principal, link uuid.UUID, fn func(tx pgx.Tx, other uuid.UUID) error) error {
	err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{p.TenantID}, func(tx pgx.Tx) error {
		other, err := links.EnterTx(ctx, tx, link, "link.booking", nil, links.Actor{Type: p.ActorType(), ID: p.Actor()})
		if err != nil {
			return err
		}
		if err := fn(tx, other); err != nil {
			return err
		}
		return errLookedOnly
	})
	if errors.Is(err, errLookedOnly) {
		return nil
	}
	return err
}
