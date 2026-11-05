package storefront

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/db"
)

// Tickets (docs/spec/fronts.md §6, F6): a paid ticket order's seats, each
// with its own code, the order's reference and the seat number
// (AK-7QX4KD-2), sent to the customer and checked in once at the door.
// A refunded or cancelled order's codes no longer check in. The count of
// tickets sold is the event's claims (00288), released on refund.

// Ticket is one seat of a ticket order.
type Ticket struct {
	Code        string     `json:"code"`
	Seat        int        `json:"seat"`
	Of          int        `json:"of"`
	Reference   string     `json:"reference"`
	Event       string     `json:"event"`
	Starts      *time.Time `json:"starts,omitempty"`
	CheckedInAt *time.Time `json:"checked_in_at,omitempty"`
	CheckedBy   string     `json:"checked_in_by,omitempty"`
}

// TicketCode reads a ticket's code as people type it: a reference and a
// seat, in any case, with or without the dash.
var TicketCode = regexp.MustCompile(`(?i)^\s*([A-Z0-9]{2,4}-[A-Z2-9]{6})-?(\d{1,3})\s*$`)

// ErrNoTicket is a code no ticket of the business has.
var ErrNoTicket = errors.New("storefront: no such ticket")

// IssueTickets makes a paid ticket order's seats, once: asked again it
// gives the same codes.
func (o *Orders) IssueTickets(ctx context.Context, tenant uuid.UUID, reference string) ([]Ticket, Order, error) {
	var out []Ticket
	var ord Order
	err := db.InTenantTx(ctx, o.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		var err error
		if ord, err = scanOrder(tx.QueryRow(ctx, `SELECT `+orderCols+` FROM storefront_orders WHERE tenant_id = $1 AND reference = $2 FOR UPDATE`, tenant, reference)); err != nil {
			return err
		}
		if ord.Kind != "tickets" {
			return fmt.Errorf("%w: %s is not a ticket order", ErrDecision, reference)
		}
		if ord.PaidAt == nil {
			return fmt.Errorf("%w: %s is not paid", ErrDecision, reference)
		}
		switch ord.Status {
		case "cancelled", "refunded", "refund_due", "expired", "declined":
			return fmt.Errorf("%w: %s is %s", ErrDecision, reference, strings.ReplaceAll(ord.Status, "_", " "))
		}
		seats := 0
		for _, l := range ord.Basket {
			seats += l.Quantity
		}
		tag, err := tx.Exec(ctx, `INSERT INTO storefront_tickets (id, tenant_id, storefront_id, reference, seat, code)
			SELECT gen_random_uuid(), $1, $2, $3, n, $3 || '-' || n FROM generate_series(1, $4::int) n
			ON CONFLICT (reference, seat) DO NOTHING`, tenant, ord.storefront, reference, seats)
		if err != nil {
			return err
		}
		if tag.RowsAffected() > 0 {
			if err := audit(ctx, tx, tenant, "storefront.tickets_issued", reference, map[string]any{"seats": seats}); err != nil {
				return err
			}
		}
		out, err = ticketsOf(ctx, tx, ord)
		return err
	})
	return out, ord, err
}

func ticketsOf(ctx context.Context, tx pgx.Tx, ord Order) ([]Ticket, error) {
	rows, err := tx.Query(ctx, `SELECT code, seat, checked_in_at, COALESCE(checked_in_by, '') FROM storefront_tickets WHERE reference = $1 ORDER BY seat`, ord.Reference)
	if err != nil {
		return nil, err
	}
	list, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (Ticket, error) {
		t := Ticket{Reference: ord.Reference, Event: ord.ServiceName, Starts: ord.StartsAt}
		err := r.Scan(&t.Code, &t.Seat, &t.CheckedInAt, &t.CheckedBy)
		return t, err
	})
	for i := range list {
		list[i].Of = len(list)
	}
	return list, err
}

// CheckIn lets a ticket in, once. already reports a ticket checked in
// before (Ticket says when); a refunded or cancelled order's tickets are
// refused.
func (o *Orders) CheckIn(ctx context.Context, tenant uuid.UUID, code, actor string) (t Ticket, already bool, err error) {
	m := TicketCode.FindStringSubmatch(code)
	if m == nil {
		return t, false, ErrNoTicket
	}
	code = strings.ToUpper(m[1]) + "-" + strings.TrimLeft(m[2], "0")
	err = db.InTenantTx(ctx, o.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		var ref string
		var at *time.Time
		if err := tx.QueryRow(ctx, `SELECT reference, checked_in_at FROM storefront_tickets WHERE tenant_id = $1 AND code = $2 FOR UPDATE`, tenant, code).
			Scan(&ref, &at); errors.Is(err, pgx.ErrNoRows) {
			return ErrNoTicket
		} else if err != nil {
			return err
		}
		ord, err := o.GetOrder(ctx, tx, tenant, ref)
		if err != nil {
			return err
		}
		switch ord.Status {
		case "cancelled", "refunded", "refund_due", "expired", "declined":
			return fmt.Errorf("%w: %s's order is %s", ErrDecision, code, strings.ReplaceAll(ord.Status, "_", " "))
		}
		if at == nil {
			if _, err := tx.Exec(ctx, `UPDATE storefront_tickets SET checked_in_at = now(), checked_in_by = $3 WHERE tenant_id = $1 AND code = $2`, tenant, code, actor); err != nil {
				return err
			}
			if err := audit(ctx, tx, tenant, "storefront.ticket_checked_in", code, map[string]any{"by": actor}); err != nil {
				return err
			}
		} else {
			already = true
		}
		list, err := ticketsOf(ctx, tx, ord)
		if err != nil {
			return err
		}
		for _, x := range list {
			if x.Code == code {
				t = x
			}
		}
		return nil
	})
	return t, already, err
}

// EventCount is an event or a class with its tickets or places, how many
// are taken (paid, or held while being paid for) and how many tickets
// are checked in.
type EventCount struct {
	Event     string `json:"event"`
	Front     string `json:"front"`
	Tickets   int    `json:"tickets"`
	Taken     int    `json:"taken"`
	CheckedIn int    `json:"checked_in"`
}

// TicketsLeft counts each event of the business's Tickets and Classes
// fronts from the lists' copies and the live claims; kind is "tickets"
// or "class".
func TicketsLeft(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, kind string) ([]EventCount, error) {
	rows, err := tx.Query(ctx, `SELECT c.item->>'name', f.name, COALESCE((c.item->>'capacity')::int, 0),
		  COALESCE((SELECT sum(h.qty) FROM storefront_claims h WHERE h.block_id = b.id AND h.item_id = c.row_id AND (h.expires_at IS NULL OR h.expires_at > now())), 0)::int,
		  (SELECT count(*) FROM storefront_tickets t JOIN storefront_orders o ON o.reference = t.reference
		    WHERE o.service_id = c.row_id AND o.front_id = f.id AND t.checked_in_at IS NOT NULL)::int
		FROM storefront_fronts f
		JOIN storefront_actions a ON a.front_id = f.id AND a.kind = $2
		JOIN LATERAL (SELECT x.id, x.config FROM storefront_blocks x WHERE x.front_id = f.id AND x.kind = 'list' ORDER BY x.position LIMIT 1) b ON true
		JOIN storefront_catalogue c ON c.storefront_id = f.storefront_id AND c.table_name = b.config->>'table_name'
		WHERE f.tenant_id = $1 AND COALESCE((c.item->>'available')::boolean, false)
		  AND (c.item->>'starts' IS NULL OR (c.item->>'starts')::timestamptz > now() - interval '1 day')
		ORDER BY c.item->>'starts' NULLS LAST, c.item->>'name'`, tenant, kind)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (EventCount, error) {
		var e EventCount
		err := r.Scan(&e.Event, &e.Front, &e.Tickets, &e.Taken, &e.CheckedIn)
		return e, err
	})
}
