package publicweb

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/israel-duff/taskiem/engine/lru"
	"github.com/israel-duff/taskiem/engine/storefront"
)

// The booking pages' reads and writes (docs/spec/storefronts.md 6.9), each
// a narrow function the taskiem_public role may execute.

// ServicesPage is a storefront's services copy and its availability as
// set (Effective fills it from the card's hours).
type ServicesPage struct {
	Availability storefront.Availability
	Services     []storefront.Item
}

// Services reads a storefront's services; ok is false when it is not
// served.
type Services func(ctx context.Context, storefront uuid.UUID) (page ServicesPage, ok bool, err error)

// DBServices reads them with taskiem_public_storefront_services.
func DBServices(pool *pgxpool.Pool) Services {
	return func(ctx context.Context, id uuid.UUID) (ServicesPage, bool, error) {
		var raw []byte
		err := pool.QueryRow(ctx, `SELECT taskiem_public_storefront_services($1)`, id).Scan(&raw)
		if errors.Is(err, pgx.ErrNoRows) || (err == nil && raw == nil) {
			return ServicesPage{}, false, nil
		}
		if err != nil {
			return ServicesPage{}, false, err
		}
		var v struct {
			Availability storefront.Availability `json:"availability"`
			Services     []storefront.Item       `json:"services"`
		}
		if err := json.Unmarshal(raw, &v); err != nil {
			return ServicesPage{}, false, err
		}
		return ServicesPage{Availability: v.Availability, Services: v.Services}, true, nil
	}
}

// BusyTimes reads what is booked or held on a storefront's calendar
// between two times.
type BusyTimes func(ctx context.Context, storefront uuid.UUID, from, to time.Time) ([]storefront.Busy, error)

// DBBusy reads it with taskiem_public_busy.
func DBBusy(pool *pgxpool.Pool) BusyTimes {
	return func(ctx context.Context, id uuid.UUID, from, to time.Time) ([]storefront.Busy, error) {
		rows, err := pool.Query(ctx, `SELECT service_id, starts_at, until_at, n FROM taskiem_public_busy($1, $2, $3)`, id, from, to)
		if err != nil {
			return nil, err
		}
		return pgx.CollectRows(rows, func(r pgx.CollectableRow) (storefront.Busy, error) {
			var b storefront.Busy
			err := r.Scan(&b.Service, &b.Start, &b.Until, &b.Count)
			return b, err
		})
	}
}

// ErrSlotTaken: the time asked for is no longer free.
var ErrSlotTaken = errors.New("publicweb: the slot is taken")

// Book holds a booking's slot and records its sealed submission at once.
// As Submit, ref is the first reference made for idem and ok is false
// when the booking form is not open; ErrSlotTaken and ErrReferenceTaken
// refuse it.
type Book func(ctx context.Context, storefront uuid.UUID, reference string, idem, statusHash, payload []byte, slot storefront.Booked) (ref string, ok bool, err error)

// DBBook books with taskiem_public_book.
func DBBook(pool *pgxpool.Pool) Book {
	return func(ctx context.Context, id uuid.UUID, reference string, idem, statusHash, payload []byte, b storefront.Booked) (string, bool, error) {
		var ref *string
		err := pool.QueryRow(ctx, `SELECT taskiem_public_book($1, $2, $3, $4, $5, $6, $7, $8, $9)`, id, reference, idem, statusHash, payload,
			b.ServiceID, b.Start, b.Until, b.Capacity).Scan(&ref)
		var pe *pgconn.PgError
		if errors.As(err, &pe) {
			switch {
			case pe.Code == "23505" && pe.ConstraintName == "storefront_inbox_reference_key":
				return "", false, ErrReferenceTaken
			case pe.Code == "23P01":
				return "", false, ErrSlotTaken
			}
		}
		if err != nil {
			return "", false, err
		}
		if ref == nil {
			return "", false, nil
		}
		return *ref, true, nil
	}
}

// BookingState is what a booking's status and change pages may show.
type BookingState struct {
	Status      string
	ServiceID   string
	ServiceName string
	Start, End  time.Time
	TotalKobo   int64
	DepositKobo int64
	Payment     string
	PaymentURL  string
	Storefront  uuid.UUID
}

// BookingStatus reads a booking by its reference and status key's hash;
// ok is false when either is wrong or it is not a booking.
type BookingStatus func(ctx context.Context, reference string, statusHash []byte) (state BookingState, ok bool, err error)

// DBBookingStatus reads it with taskiem_public_booking.
func DBBookingStatus(pool *pgxpool.Pool) BookingStatus {
	return func(ctx context.Context, reference string, statusHash []byte) (BookingState, bool, error) {
		var b BookingState
		var url *string
		var deposit *int64
		err := pool.QueryRow(ctx, `SELECT status, service_id, service_name, starts_at, ends_at, total_kobo, payment, payment_url, storefront_id, deposit_kobo
			FROM taskiem_public_booking($1, $2)`, reference, statusHash).
			Scan(&b.Status, &b.ServiceID, &b.ServiceName, &b.Start, &b.End, &b.TotalKobo, &b.Payment, &url, &b.Storefront, &deposit)
		if errors.Is(err, pgx.ErrNoRows) {
			return BookingState{}, false, nil
		}
		if err != nil {
			return BookingState{}, false, err
		}
		if url != nil {
			b.PaymentURL = *url
		}
		if deposit != nil {
			b.DepositKobo = *deposit
		}
		return b, true, nil
	}
}

// Change outcomes, as taskiem_public_change answers.
const (
	ChangeOK         = "ok"
	ChangeTooLate    = "too_late"
	ChangeTaken      = "taken"
	ChangeNotAllowed = "not_allowed"
)

// Change applies a customer's change from their status link: cancel,
// move (to the slot given, read from the services copy), price_accept or
// price_decline. It answers one of the Change outcomes.
type Change func(ctx context.Context, reference string, statusHash []byte, action string, cutoffMinutes int, to *storefront.Booked) (string, error)

// DBChange applies it with taskiem_public_change.
func DBChange(pool *pgxpool.Pool) Change {
	return func(ctx context.Context, reference string, statusHash []byte, action string, cutoff int, to *storefront.Booked) (string, error) {
		var start, end, until *time.Time
		capacity := 0
		if to != nil {
			start, end, until, capacity = &to.Start, &to.End, &to.Until, to.Capacity
		}
		var got string
		err := pool.QueryRow(ctx, `SELECT taskiem_public_change($1, $2, $3, $4, $5, $6, $7, $8)`, reference, statusHash, action, cutoff, start, end, until, capacity).Scan(&got)
		return got, err
	}
}

// PriceOffer is the new total of an order or booking whose prices changed
// before it went ahead: Gone names what can no longer be had.
type PriceOffer struct {
	TotalKobo   int64
	DepositKobo int64
	Gone        []string
}

// PriceChange reads it; ok is false unless the order is waiting on its
// customer to confirm new prices.
type PriceChange func(ctx context.Context, reference string, statusHash []byte) (offer PriceOffer, ok bool, err error)

// DBPriceChange reads it with taskiem_public_price_change.
func DBPriceChange(pool *pgxpool.Pool) PriceChange {
	return func(ctx context.Context, reference string, statusHash []byte) (PriceOffer, bool, error) {
		var p PriceOffer
		err := pool.QueryRow(ctx, `SELECT total_kobo, deposit_kobo, gone FROM taskiem_public_price_change($1, $2)`, reference, statusHash).Scan(&p.TotalKobo, &p.DepositKobo, &p.Gone)
		if errors.Is(err, pgx.ErrNoRows) {
			return PriceOffer{}, false, nil
		}
		if err != nil {
			return PriceOffer{}, false, err
		}
		return p, true, nil
	}
}

type servicesEntry struct {
	page ServicesPage
	ok   bool
}

// services reads a storefront's services through a cache as the page's.
func (s *Server) services(ctx context.Context, id uuid.UUID) (ServicesPage, bool, error) {
	if s.Services == nil {
		return ServicesPage{}, false, nil
	}
	if s.servicesCache == nil {
		s.servicesCache = lru.New[uuid.UUID, servicesEntry](2000, HostTTL)
	}
	if e, ok := s.servicesCache.Get(id); ok {
		return e.page, e.ok, nil
	}
	p, ok, err := s.Services(ctx, id)
	if err != nil {
		return ServicesPage{}, false, err
	}
	s.servicesCache.Put(id, servicesEntry{p, ok})
	return p, ok, nil
}
