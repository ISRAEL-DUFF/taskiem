package moneyreport

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// The Transactions page (docs/spec/transactions-page.md): the money log
// one movement to a row, as the log export and the summary count it (each
// movement's latest entry, the first entry's time and, when the latest
// has none, its amount), filtered, searched, paged, and summed up for the
// filter.

// Filter narrows the movements listed. Empty fields filter nothing.
type Filter struct {
	// From and To bound the first entry's time, [From, To).
	From, To *time.Time
	// Direction: in or out.
	Direction string
	// Channel: one channel exactly ("paystack/main", "cash").
	Channel string
	// Matched: "yes" for movements counted towards an invoice, order or
	// sale; "no" for settled naira money in counted towards none.
	Matched string
	// Search finds words in what was typed or the rail said (description,
	// vendor, category, note, reference, channel, the invoice, order or
	// sale it counts towards) and, when it reads as an amount ("4,500",
	// "₦4500.50"), movements of exactly that amount.
	Search string
	// Movement is one movement alone (an entry's own page).
	Movement *uuid.UUID
}

// ErrBadFilter is a filter the list refuses.
var ErrBadFilter = errors.New("moneyreport: bad filter")

// Movement is one row of the Transactions page.
type Movement struct {
	// ID is the entry that counts: the movement's first settled money in,
	// else its first entry. Matching it by hand takes this id.
	ID uuid.UUID `json:"id"`
	// MovementID is the movement's first entry; later statuses name it.
	MovementID  uuid.UUID `json:"movement"`
	Kind        string    `json:"kind"`
	Direction   string    `json:"direction"`
	AmountKobo  *int64    `json:"amount_kobo,omitempty"`
	FeeKobo     *int64    `json:"fee_kobo,omitempty"`
	Status      string    `json:"status"`
	Channel     string    `json:"channel,omitempty"`
	Reference   string    `json:"reference,omitempty"`
	When        time.Time `json:"when"`
	Description string    `json:"description,omitempty"`
	Vendor      string    `json:"vendor,omitempty"`
	Category    string    `json:"category,omitempty"`
	LeftKobo    *int64    `json:"left_kobo,omitempty"`
	Towards     string    `json:"towards,omitempty"`
	ToMatch     bool      `json:"to_match"`
	Currency    string    `json:"currency,omitempty"`
	// Statuses is how many entries the movement has (a payout pending,
	// then settled, is two).
	Statuses int `json:"statuses"`
}

// Totals sums up the movements a filter finds: settled and pending money
// in and out and the fees, naira only, as the Money page's summary does;
// Other counts those in another currency, in no sum.
type Totals struct {
	Count      int   `json:"count"`
	InSettled  int64 `json:"in_settled_kobo"`
	InPending  int64 `json:"in_pending_kobo"`
	OutSettled int64 `json:"out_settled_kobo"`
	OutPending int64 `json:"out_pending_kobo"`
	FeesKobo   int64 `json:"fees_kobo"`
	Other      int   `json:"other_currencies"`
}

// Cursor is where a page ends: the next page is the movements after it.
type Cursor struct {
	At time.Time
	ID uuid.UUID
}

// String is the cursor as the API hands it out.
func (c Cursor) String() string { return c.At.UTC().Format(time.RFC3339Nano) + "_" + c.ID.String() }

// ParseCursor reads a cursor String wrote.
func ParseCursor(s string) (Cursor, error) {
	at, id, ok := strings.Cut(s, "_")
	if !ok {
		return Cursor{}, fmt.Errorf("%w: the next page's mark is not one this list gave", ErrBadFilter)
	}
	t, err := time.Parse(time.RFC3339Nano, at)
	if err != nil {
		return Cursor{}, fmt.Errorf("%w: the next page's mark is not one this list gave", ErrBadFilter)
	}
	u, err := uuid.Parse(id)
	if err != nil {
		return Cursor{}, fmt.Errorf("%w: the next page's mark is not one this list gave", ErrBadFilter)
	}
	return Cursor{At: t, ID: u}, nil
}

// ParseAmount reads what a person typed as naira ("4,500", "₦4500.50",
// "4500") as kobo; ok is false for anything else.
func ParseAmount(s string) (int64, bool) {
	s = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(s), "₦"))
	s = strings.ReplaceAll(s, ",", "")
	if s == "" || len(s) > 15 {
		return 0, false
	}
	whole, frac, dot := strings.Cut(s, ".")
	if whole == "" || (dot && (len(frac) == 0 || len(frac) > 2)) {
		return 0, false
	}
	for _, r := range whole + frac {
		if r < '0' || r > '9' {
			return 0, false
		}
	}
	n, err := strconv.ParseInt(whole, 10, 64)
	if err != nil {
		return 0, false
	}
	k := n * 100
	if frac != "" {
		f, _ := strconv.ParseInt((frac + "0")[:2], 10, 64)
		k += f
	}
	return k, true
}

// movementsFrom is every movement of the tenant ($1), its latest entry's
// status, kind, direction, channel and currency (as the summary takes
// them), the first entry's time, and what it counts towards, with the
// filters that are cheap to apply before the counting. The filters'
// arguments follow $1.
func movementsFrom(f Filter, args []any) (string, []any, error) {
	var where []string
	arg := func(v any) string {
		args = append(args, v)
		return "$" + strconv.Itoa(len(args))
	}
	if f.From != nil {
		where = append(where, "COALESCE(f.occurred_at, f.recorded_at) >= "+arg(*f.From))
	}
	if f.To != nil {
		where = append(where, "COALESCE(f.occurred_at, f.recorded_at) < "+arg(*f.To))
	}
	switch f.Direction {
	case "":
	case "in", "out":
		where = append(where, "m.direction = "+arg(f.Direction))
	default:
		return "", nil, fmt.Errorf("%w: direction is in or out", ErrBadFilter)
	}
	if f.Channel != "" {
		where = append(where, "m.channel = "+arg(f.Channel))
	}
	if f.Movement != nil {
		where = append(where, "f.id = "+arg(*f.Movement))
	}
	cond := ""
	if len(where) > 0 {
		cond = " AND " + strings.Join(where, " AND ")
	}
	// The movement's entries: x.id = movement or x.corrects = movement.
	sql := `WITH moves AS (
	SELECT DISTINCT ON (COALESCE(e.corrects, e.id)) e.*, COALESCE(e.corrects, e.id) AS movement
	  FROM money_entries e WHERE e.tenant_id = $1
	 ORDER BY COALESCE(e.corrects, e.id), e.recorded_at DESC, e.id DESC
), base AS (
	SELECT m.movement, m.kind, m.direction, m.status, m.channel, m.reference, m.currency,
	       COALESCE(m.amount_kobo, f.amount_kobo) AS amount, COALESCE(m.fee_kobo, f.fee_kobo) AS fee,
	       COALESCE(f.occurred_at, f.recorded_at) AS at,
	       COALESCE(m.description, f.description, '') AS description, COALESCE(m.vendor, f.vendor, '') AS vendor,
	       COALESCE(m.category, f.category, '') AS category, COALESCE(m.note, f.note, '') AS note,
	       (SELECT x.id FROM money_entries x WHERE x.tenant_id = $1 AND (x.id = m.movement OR x.corrects = m.movement)
	         ORDER BY (x.direction = 'in' AND x.status = 'settled') DESC, x.recorded_at, x.id LIMIT 1) AS pick,
	       (SELECT count(*) FROM money_entries x WHERE x.tenant_id = $1 AND (x.id = m.movement OR x.corrects = m.movement))::int AS statuses
	  FROM moves m JOIN money_entries f ON f.id = m.movement AND f.tenant_id = $1
	 WHERE TRUE` + cond + `
), mv AS (
	SELECT b.*,
	       CASE WHEN b.direction = 'in' THEN taskiem_money_entry_left(b.pick) END AS left_kobo,
	       COALESCE((SELECT string_agg(what, ', ' ORDER BY what) FROM (
	         SELECT DISTINCT 'invoice ' || i.number AS what FROM customer_invoice_payments p JOIN customer_invoices i ON i.id = p.invoice_id
	          WHERE p.tenant_id = $1 AND p.entry_id IN (SELECT x.id FROM money_entries x WHERE x.tenant_id = $1 AND (x.id = b.movement OR x.corrects = b.movement))
	            AND p.undoes IS NULL AND NOT EXISTS (SELECT 1 FROM customer_invoice_payments u WHERE u.undoes = p.id)
	         UNION
	         SELECT 'order ' || o.reference FROM storefront_order_payments p JOIN storefront_orders o ON o.id = p.order_id
	          WHERE p.tenant_id = $1 AND p.entry_id IN (SELECT x.id FROM money_entries x WHERE x.tenant_id = $1 AND (x.id = b.movement OR x.corrects = b.movement))
	            AND p.undoes IS NULL AND NOT EXISTS (SELECT 1 FROM storefront_order_payments u WHERE u.undoes = p.id)
	         UNION
	         SELECT 'sale ' || s.reference FROM stock_sales s
	          WHERE s.tenant_id = $1 AND s.entry_id IN (SELECT x.id FROM money_entries x WHERE x.tenant_id = $1 AND (x.id = b.movement OR x.corrects = b.movement))) t), '') AS towards,
	       EXISTS (SELECT 1 FROM money_match_state s WHERE s.entry_id = b.pick AND s.state = 'unmatched')
	         AND (taskiem_money_entry_left(b.pick) > 0 OR b.amount IS NULL AND taskiem_money_entry_counted(b.pick) = 0)
	         AND NOT EXISTS (SELECT 1 FROM money_entry_customers c WHERE c.entry_id = b.pick) AS to_match
	  FROM base b
)`
	return sql, args, nil
}

// later adds the filters that need what a movement counts towards: matched
// or not, and the search.
func later(f Filter, args []any) (string, []any, error) {
	var where []string
	arg := func(v any) string {
		args = append(args, v)
		return "$" + strconv.Itoa(len(args))
	}
	switch f.Matched {
	case "":
	case "yes":
		where = append(where, "r.towards <> ''")
	case "no":
		where = append(where, "r.towards = '' AND r.direction = 'in' AND r.status = 'settled' AND r.currency = 'NGN'")
	default:
		return "", nil, fmt.Errorf("%w: matched is yes or no", ErrBadFilter)
	}
	if q := strings.TrimSpace(f.Search); q != "" {
		if len([]rune(q)) > 100 {
			return "", nil, fmt.Errorf("%w: a search is at most 100 characters", ErrBadFilter)
		}
		like := "%" + strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(q) + "%"
		p := arg(like)
		or := []string{"r.description ILIKE " + p, "r.vendor ILIKE " + p, "r.category ILIKE " + p, "r.note ILIKE " + p,
			"r.reference ILIKE " + p, "r.channel ILIKE " + p, "r.towards ILIKE " + p}
		if k, ok := ParseAmount(q); ok {
			or = append(or, "r.amount = "+arg(k))
		}
		where = append(where, "("+strings.Join(or, " OR ")+")")
	}
	if len(where) == 0 {
		return "", args, nil
	}
	return " AND " + strings.Join(where, " AND "), args, nil
}

// Movements lists the tenant's movements the filter finds, newest first
// (by the first entry's time), at most limit, after the cursor when
// paging. next is set when there may be more.
func Movements(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, f Filter, after *Cursor, limit int) (list []Movement, next *Cursor, err error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	with, args, err := movementsFrom(f, []any{tenant})
	if err != nil {
		return nil, nil, err
	}
	cond, args, err := later(f, args)
	if err != nil {
		return nil, nil, err
	}
	if after != nil {
		args = append(args, after.At, after.ID)
		cond += fmt.Sprintf(" AND (r.at, r.movement) < ($%d, $%d)", len(args)-1, len(args))
	}
	args = append(args, limit+1)
	rows, err := tx.Query(ctx, with+`
SELECT r.pick, r.movement, r.kind, r.direction, r.amount, r.fee, r.status, r.channel, r.reference, r.at, r.description, r.vendor, r.category,
       r.left_kobo, r.towards, r.to_match, CASE WHEN r.currency <> 'NGN' THEN r.currency ELSE '' END, r.statuses
  FROM mv r WHERE TRUE`+cond+`
 ORDER BY r.at DESC, r.movement DESC LIMIT $`+strconv.Itoa(len(args)), args...)
	if err != nil {
		return nil, nil, err
	}
	list, err = pgx.CollectRows(rows, func(row pgx.CollectableRow) (Movement, error) {
		var m Movement
		return m, row.Scan(&m.ID, &m.MovementID, &m.Kind, &m.Direction, &m.AmountKobo, &m.FeeKobo, &m.Status, &m.Channel, &m.Reference, &m.When,
			&m.Description, &m.Vendor, &m.Category, &m.LeftKobo, &m.Towards, &m.ToMatch, &m.Currency, &m.Statuses)
	})
	if err != nil {
		return nil, nil, err
	}
	if len(list) > limit {
		list = list[:limit]
		last := list[len(list)-1]
		next = &Cursor{At: last.When, ID: last.MovementID}
	}
	if list == nil {
		list = []Movement{}
	}
	return list, next, nil
}

// SumMovements sums up every movement the filter finds.
func SumMovements(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, f Filter) (Totals, error) {
	var t Totals
	with, args, err := movementsFrom(f, []any{tenant})
	if err != nil {
		return t, err
	}
	cond, args, err := later(f, args)
	if err != nil {
		return t, err
	}
	sum := func(dir, status string) string {
		return fmt.Sprintf("COALESCE(sum(r.amount) FILTER (WHERE r.currency = 'NGN' AND r.direction = '%s' AND r.status = '%s'), 0)::bigint", dir, status)
	}
	err = tx.QueryRow(ctx, with+`
SELECT count(*)::int, `+sum("in", "settled")+`, `+sum("in", "pending")+`, `+sum("out", "settled")+`, `+sum("out", "pending")+`,
       COALESCE(sum(r.fee) FILTER (WHERE r.currency = 'NGN' AND r.status = 'settled'), 0)::bigint,
       (count(*) FILTER (WHERE r.currency <> 'NGN'))::int
  FROM mv r WHERE TRUE`+cond, args...).Scan(&t.Count, &t.InSettled, &t.InPending, &t.OutSettled, &t.OutPending, &t.FeesKobo, &t.Other)
	return t, err
}

// MovementChannels is every channel the tenant's log has money on, for
// the Transactions page's filter.
func MovementChannels(ctx context.Context, tx pgx.Tx, tenant uuid.UUID) ([]string, error) {
	rows, err := tx.Query(ctx, `SELECT DISTINCT channel FROM money_entries WHERE tenant_id = $1 ORDER BY channel LIMIT 100`, tenant)
	if err != nil {
		return nil, err
	}
	list, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if list == nil {
		list = []string{}
	}
	return list, err
}

// Status is one entry of a movement: what the rail (or a person) said,
// and when Taskiem heard it.
type Status struct {
	Status     string    `json:"status"`
	Kind       string    `json:"kind"`
	AmountKobo *int64    `json:"amount_kobo,omitempty"`
	At         time.Time `json:"at"`
	Source     string    `json:"source"`
}

// Counted is an invoice, order or sale a movement counts towards, and how
// much of it.
type Counted struct {
	What       string    `json:"what"` // invoice, order or sale
	ID         uuid.UUID `json:"id"`
	Number     string    `json:"number"` // INV-0104, the order's or sale's reference
	AmountKobo int64     `json:"amount_kobo"`
}

// Detail is one movement on its own page.
type Detail struct {
	Movement
	RecordedAt time.Time `json:"recorded_at"`
	Note       string    `json:"note,omitempty"`
	Source     string    `json:"source"`
	History    []Status  `json:"history"`
	Counts     []Counted `json:"counts"`
	// Payer is the sealed payer of the movement's first entry that has
	// one, for the caller to open; nil when the rail said none.
	Payer map[string]any `json:"-"`
}

// MovementOf finds the movement holding an entry (any of its statuses):
// ok is false when the tenant has no such entry.
func MovementOf(ctx context.Context, tx pgx.Tx, tenant, entry uuid.UUID) (d Detail, ok bool, err error) {
	var movement uuid.UUID
	err = tx.QueryRow(ctx, `SELECT COALESCE(corrects, id) FROM money_entries WHERE tenant_id = $1 AND id = $2`, tenant, entry).Scan(&movement)
	if errors.Is(err, pgx.ErrNoRows) {
		return d, false, nil
	}
	if err != nil {
		return d, false, err
	}
	list, _, err := Movements(ctx, tx, tenant, Filter{Movement: &movement}, nil, 1)
	if err != nil || len(list) == 0 {
		return d, false, err
	}
	d.Movement = list[0]
	rows, err := tx.Query(ctx, `SELECT status, kind, amount_kobo, recorded_at, source, COALESCE(note, ''), payer
		FROM money_entries WHERE tenant_id = $1 AND (id = $2 OR corrects = $2) ORDER BY recorded_at, id`, tenant, movement)
	if err != nil {
		return d, false, err
	}
	d.History = []Status{}
	for rows.Next() {
		var s Status
		var note string
		var payer map[string]any
		if err := rows.Scan(&s.Status, &s.Kind, &s.AmountKobo, &s.At, &s.Source, &note, &payer); err != nil {
			rows.Close()
			return d, false, err
		}
		if len(d.History) == 0 {
			d.RecordedAt, d.Source = s.At, s.Source
		}
		if note != "" {
			d.Note = note
		}
		if d.Payer == nil && payer != nil {
			d.Payer = payer
		}
		d.History = append(d.History, s)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return d, false, err
	}
	rows, err = tx.Query(ctx, `WITH es AS (SELECT id FROM money_entries WHERE tenant_id = $1 AND (id = $2 OR corrects = $2))
		SELECT 'invoice', i.id, i.number, sum(p.amount_kobo)::bigint FROM customer_invoice_payments p JOIN customer_invoices i ON i.id = p.invoice_id
		 WHERE p.tenant_id = $1 AND p.entry_id IN (SELECT id FROM es) AND p.undoes IS NULL
		   AND NOT EXISTS (SELECT 1 FROM customer_invoice_payments u WHERE u.undoes = p.id) GROUP BY i.id, i.number
		UNION ALL
		SELECT 'order', o.id, o.reference, sum(p.amount_kobo)::bigint FROM storefront_order_payments p JOIN storefront_orders o ON o.id = p.order_id
		 WHERE p.tenant_id = $1 AND p.entry_id IN (SELECT id FROM es) AND p.undoes IS NULL
		   AND NOT EXISTS (SELECT 1 FROM storefront_order_payments u WHERE u.undoes = p.id) GROUP BY o.id, o.reference
		UNION ALL
		SELECT 'sale', s.id, s.reference, s.amount_kobo FROM stock_sales s WHERE s.tenant_id = $1 AND s.entry_id IN (SELECT id FROM es)
		ORDER BY 1, 3`, tenant, movement)
	if err != nil {
		return d, false, err
	}
	d.Counts, err = pgx.CollectRows(rows, pgx.RowToStructByPos[Counted])
	if d.Counts == nil {
		d.Counts = []Counted{}
	}
	return d, err == nil, err
}
