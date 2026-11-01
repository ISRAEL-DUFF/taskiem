package publicweb

import (
	"context"
	"errors"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/israel-duff/taskiem/engine/storefront"
)

// Hire (docs/spec/fronts.md §6, F5): the dates-and-quantity field. The
// customer picks an item from the front's list, a first and a last day,
// and how many; the server prices it from the list's copy (the rate per
// day, times the days, times how many), and the database takes the claim
// with the submission, refusing more than is free for those days.

// MaxHireDays is the longest a hire may run, and HireAheadDays how far
// ahead it may start.
const (
	MaxHireDays   = 90
	HireAheadDays = 365
)

// ErrNotFree refuses a hire beyond what is free for its days.
var ErrNotFree = errors.New("publicweb: not that many are free for those days")

// Claim takes a hire's claim and records its sealed submission at once:
// ref is the first reference made for idem, ok false when the action is
// not open; ErrNotFree and ErrReferenceTaken refuse it.
type Claim func(ctx context.Context, action uuid.UUID, reference string, idem, statusHash, payload []byte, h storefront.Hired) (ref string, ok bool, err error)

// DBClaim claims with taskiem_public_claim.
func DBClaim(pool *pgxpool.Pool) Claim {
	return func(ctx context.Context, action uuid.UUID, reference string, idem, statusHash, payload []byte, h storefront.Hired) (string, bool, error) {
		var ref *string
		err := pool.QueryRow(ctx, `SELECT taskiem_public_claim($1, $2, $3, $4, $5, $6, $7, $8, $9)`, action, reference, idem, statusHash, payload,
			h.ItemID, h.Qty, h.From, h.Until).Scan(&ref)
		var pe *pgconn.PgError
		if errors.As(err, &pe) {
			switch {
			case pe.Code == "23505" && pe.ConstraintName == "storefront_inbox_reference_key":
				return "", false, ErrReferenceTaken
			case pe.Code == "23P01":
				return "", false, ErrNotFree
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

// hireView is the hire field as the form shows it.
type hireView struct {
	Items         []hireItem
	From, To, Qty string
	Min, Max      string // the dates the pickers allow
	Deposit       string // "half the price is paid to confirm", or ""
}

type hireItem struct {
	ID, Label string
	Selected  bool
}

// hireField is the field for a front's list: the items that can be
// hired (available, with a rate and some to hire).
func hireField(items []storefront.Item, now time.Time, depositPercent int) *hireView {
	h := &hireView{Qty: "1"}
	for _, it := range items {
		if it.Available && it.PriceKobo != nil && *it.PriceKobo > 0 && it.Capacity > 0 {
			h.Items = append(h.Items, hireItem{ID: it.ID, Label: it.Name + ", " + storefront.FormatNaira(*it.PriceKobo) + " a day"})
		}
	}
	today := now.In(storefront.Lagos)
	h.Min = today.Format(time.DateOnly)
	h.Max = today.AddDate(0, 0, HireAheadDays).Format(time.DateOnly)
	if depositPercent > 0 && depositPercent < 100 {
		h.Deposit = strconv.Itoa(depositPercent) + "% of the price is paid to confirm, the rest when the items come back."
	}
	return h
}

// keep copies the hire field's answers back into the form.
func (h *hireView) keep(v url.Values) {
	h.From, h.To = v.Get("hire_from"), v.Get("hire_to")
	if q := strings.TrimSpace(v.Get("hire_qty")); q != "" {
		h.Qty = q
	}
	for i := range h.Items {
		h.Items[i].Selected = h.Items[i].ID == v.Get("hire_item")
	}
}

// checkHire reads the hire field into the submission: the item from the
// list's copy, whole days in Lagos from the first to the last, how many,
// and the price at the item's rate per day; msg says what to fix.
func checkHire(v url.Values, items []storefront.Item, act storefront.PublicAction, now time.Time, sub *storefront.Submission) string {
	var it *storefront.Item
	for i := range items {
		if items[i].ID == v.Get("hire_item") {
			it = &items[i]
		}
	}
	if it == nil || !it.Available || it.PriceKobo == nil || *it.PriceKobo <= 0 || it.Capacity <= 0 {
		return "Choose what you want to hire."
	}
	from, err1 := time.ParseInLocation(time.DateOnly, strings.TrimSpace(v.Get("hire_from")), storefront.Lagos)
	to, err2 := time.ParseInLocation(time.DateOnly, strings.TrimSpace(v.Get("hire_to")), storefront.Lagos)
	if err1 != nil || err2 != nil {
		return "Choose the first and the last day of the hire."
	}
	today := now.In(storefront.Lagos)
	today = time.Date(today.Year(), today.Month(), today.Day(), 0, 0, 0, 0, storefront.Lagos)
	switch {
	case from.Before(today):
		return "The first day cannot be in the past."
	case to.Before(from):
		return "The last day is before the first."
	case from.After(today.AddDate(0, 0, HireAheadDays)):
		return "That is too far ahead to hire."
	}
	until := to.AddDate(0, 0, 1)
	days := int(until.Sub(from).Hours()/24 + 0.5)
	if days > MaxHireDays {
		return "A hire is at most " + strconv.Itoa(MaxHireDays) + " days."
	}
	qty, err := strconv.Atoi(strings.TrimSpace(v.Get("hire_qty")))
	if err != nil || qty < 1 {
		return "Say how many you want."
	}
	if qty > it.Capacity {
		return "There are only " + strconv.Itoa(it.Capacity) + " to hire."
	}
	rate := *it.PriceKobo
	total := rate * int64(days) * int64(qty)
	sub.Kind = "hire"
	sub.Hire = &storefront.Hired{ItemID: it.ID, Item: it.Name, Qty: qty, From: from, To: to, Until: until, Days: days}
	sub.Basket = []storefront.Line{{ID: it.ID, Name: it.Name, Quantity: qty * days, PriceKobo: rate, LineKobo: total}}
	sub.TotalKobo = total
	if act.DepositPercent > 0 && act.DepositPercent < 100 {
		sub.DepositKobo = (total*int64(act.DepositPercent) + 99) / 100
	}
	return ""
}

// recordHire seals a hire and takes its claim, with a new reference if
// the one drawn is taken.
func (s *Server) recordHire(ctx context.Context, sf uuid.UUID, slug string, act storefront.PublicAction, idem [32]byte, sub storefront.Submission) (string, bool, error) {
	if s.Claim == nil || act.ID == nil {
		return "", false, nil
	}
	hash := storefront.StatusHash(sub.StatusKey)
	for range 3 {
		ref := storefront.NewReference(slug)
		payload, err := storefront.SealSubmission(s.InboxKey, sf, ref, sub)
		if err != nil {
			return "", false, err
		}
		got, ok, err := s.Claim(ctx, *act.ID, ref, idem[:], hash, payload, *sub.Hire)
		if errors.Is(err, ErrReferenceTaken) {
			continue
		}
		return got, ok, err
	}
	return "", false, ErrReferenceTaken
}
