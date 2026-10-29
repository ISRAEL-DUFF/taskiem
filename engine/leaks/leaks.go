// Package leaks finds money a business may be losing (decision 0045;
// docs/spec/invoices-and-money-log.md 9.1, T6; docs/leaks.md): money in
// that nothing matches after a day, overdue invoices, a payment recorded
// twice, an invoice paid more than it asked or after it was voided, a fee
// above the rate the owner agreed with a connection, and cash a rider owes
// beyond the business's limit.
//
// A leak is opened once per kind and subject (the entry or the invoice),
// shown in naira, can be assigned to a member, and is closed by a person
// as recovered (with the amount), written off or not a leak. A leak whose
// cause goes away before anyone closes it (the money is matched, the
// invoice paid) closes itself as resolved.
package leaks

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/israel-duff/taskiem/engine/db"
)

// UnmatchedAfter is how long money in may wait for a match before it is a
// leak.
const UnmatchedAfter = 24 * time.Hour

// Detector looks for leaks.
type Detector struct {
	Pool   *pgxpool.Pool
	Logger *slog.Logger
	Now    func() time.Time
}

func (d *Detector) now() time.Time {
	if d.Now != nil {
		return d.Now()
	}
	return time.Now()
}

func (d *Detector) log() *slog.Logger {
	if d.Logger != nil {
		return d.Logger
	}
	return slog.Default()
}

// Run looks every 15 minutes until ctx ends.
func (d *Detector) Run(ctx context.Context) error {
	for {
		if err := d.Tick(ctx); err != nil && ctx.Err() == nil {
			d.log().Error("leaks", "err", err)
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(15 * time.Minute):
		}
	}
}

// Tick looks at every tenant with money or invoices.
func (d *Detector) Tick(ctx context.Context) error {
	rows, err := d.Pool.Query(ctx, `SELECT tenant_id FROM taskiem_money_leak_tenants()`)
	if err != nil {
		return err
	}
	tenants, err := pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
	if err != nil {
		return err
	}
	for _, t := range tenants {
		if _, err := d.Tenant(ctx, t); err != nil && ctx.Err() == nil {
			d.log().Warn("leaks", "tenant", t, "err", err)
		}
	}
	return nil
}

// naira writes kobo in SQL as the pages do: ₦25,000 or ₦25,000.50.
const naira = `('₦' || to_char(floor(%[1]s / 100), 'FM999,999,999,990') || CASE WHEN %[1]s %% 100 = 0 THEN '' ELSE '.' || lpad((%[1]s %% 100)::text, 2, '0') END)`

func nairaSQL(expr string) string { return fmt.Sprintf(naira, expr) }

// stockCostSQL is a movement's item's cost: when it moved, else the last
// known; 0 when none was ever given.
const stockCostSQL = `COALESCE(m.cost_kobo, (SELECT c.cost_kobo FROM stock_movements c WHERE c.tenant_id = m.tenant_id AND c.item_id = m.item_id
	AND c.cost_kobo IS NOT NULL ORDER BY c.created_at DESC LIMIT 1), 0)`

// Tenant opens the tenant's new leaks, updates the amounts of open ones,
// and resolves those whose cause went away. It returns how many it
// opened.
func (d *Detector) Tenant(ctx context.Context, tenant uuid.UUID) (int, error) {
	now := d.now()
	opened := 0
	err := db.InTenantTx(ctx, d.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		found := []struct{ kind, query string }{
			// Money in with nothing to match, after a day: payments,
			// credits to accounts and statement lines (POS sales and cash
			// are takings, not money owed).
			{"unmatched", `SELECT e.id::text, e.id, NULL::uuid, COALESCE(e.amount_kobo, 0),
				` + nairaSQL("COALESCE(e.amount_kobo, 0)") + ` || ' came in on ' || e.channel || ' on ' || to_char(COALESCE(e.occurred_at, e.recorded_at) AT TIME ZONE 'Africa/Lagos', 'DD Mon YYYY') || ' and matches no invoice'
				FROM money_entries e JOIN money_match_state s ON s.entry_id = e.id
				WHERE e.tenant_id = $1 AND e.direction = 'in' AND e.status = 'settled' AND e.kind IN ('payment', 'account_credit', 'statement')
				  AND s.state IN ('unmatched', 'suggested') AND e.recorded_at < $2
				  AND NOT EXISTS (SELECT 1 FROM customer_invoice_payments p WHERE p.entry_id = e.id AND p.undoes IS NULL
				                  AND NOT EXISTS (SELECT 1 FROM customer_invoice_payments u WHERE u.undoes = p.id))
				  -- Money for a voided invoice is its own leak (void_paid).
				  AND NOT EXISTS (SELECT 1 FROM customer_invoice_accounts a JOIN customer_invoices i ON i.id = a.invoice_id
				                  WHERE a.tenant_id = e.tenant_id AND a.payment_id = e.reference AND a.connector || '/' || a.connection = e.channel AND i.status = 'void')`},
			{"overdue", `SELECT i.id::text, NULL::uuid, i.id, i.total_kobo - i.paid_kobo,
				i.number || ' is overdue: ' || ` + nairaSQL("(i.total_kobo - i.paid_kobo)") + ` || ' still due since ' || to_char(i.due_on, 'DD Mon YYYY')
				FROM customer_invoices i WHERE i.tenant_id = $1 AND i.status = 'overdue'`},
			// The same reference and amount twice on one channel: one
			// movement the rail reported as two (each event is kept once by
			// its key, so two rows here are two kinds of event). Statement
			// lines are left out: the same line is kept once, and lines
			// without a reference repeat by nature (bank charges). A credit
			// to a customer's account number is known by its credit_id: the
			// reference is the account's, which takes many equal payments
			// (a student's fees).
			{"duplicate", `SELECT e.channel || '|' || e.direction || '|' || e.movement || '|' || e.amount_kobo, min(e.id::text)::uuid, NULL::uuid,
				e.amount_kobo * (count(*) - 1),
				` + nairaSQL("e.amount_kobo") + ` || ' with reference ' || e.movement || ' is on ' || e.channel || ' ' || count(*) || ' times'
				FROM (SELECT e.*, CASE WHEN e.kind = 'account_credit' THEN COALESCE(e.credit_id, e.reference) ELSE e.reference END AS movement
				      FROM money_entries e WHERE e.tenant_id = $1 AND e.status = 'settled' AND e.amount_kobo > 0
				        AND e.kind IN ('payment', 'account_credit', 'pos_sale', 'payout')) e
				GROUP BY e.channel, e.direction, e.movement, e.amount_kobo HAVING count(*) > 1`},
			{"overpaid", `SELECT i.id::text, NULL::uuid, i.id, i.paid_kobo - i.total_kobo,
				i.number || ' was paid ' || ` + nairaSQL("(i.paid_kobo - i.total_kobo)") + ` || ' more than it asked for'
				FROM customer_invoices i WHERE i.tenant_id = $1 AND i.paid_kobo > i.total_kobo`},
			// Money for a void invoice: into an account Taskiem gave for it.
			{"void_paid", `SELECT e.id::text, e.id, i.id, COALESCE(e.amount_kobo, 0),
				` + nairaSQL("COALESCE(e.amount_kobo, 0)") + ` || ' was paid for ' || i.number || ', which was voided'
				FROM money_entries e JOIN customer_invoice_accounts a ON a.tenant_id = e.tenant_id AND a.payment_id = e.reference AND a.connector || '/' || a.connection = e.channel
				JOIN customer_invoices i ON i.id = a.invoice_id
				WHERE e.tenant_id = $1 AND e.direction = 'in' AND e.status = 'settled' AND i.status = 'void'`},
			// A fee above the rate agreed for the connection.
			{"fee", `SELECT e.id::text, e.id, NULL::uuid, e.fee_kobo - x.expected,
				'a fee of ' || ` + nairaSQL("e.fee_kobo") + ` || ' on ' || ` + nairaSQL("e.amount_kobo") + ` || ' through ' || e.channel || ', where the agreed rate makes ' || ` + nairaSQL("x.expected") + `
				FROM money_entries e JOIN money_fee_rates r ON r.tenant_id = e.tenant_id AND r.channel = e.channel
				CROSS JOIN LATERAL (SELECT LEAST(COALESCE(r.cap_kobo, 9223372036854775807), (e.amount_kobo * r.percent_bp + 9999) / 10000 + r.flat_kobo) AS expected) x
				WHERE e.tenant_id = $1 AND e.fee_kobo IS NOT NULL AND e.amount_kobo IS NOT NULL AND e.fee_kobo > x.expected`},
			// Cash a rider took at the door and still owes, beyond the
			// business's limit (dispatch D4); the rider's name is sealed, so
			// Riders > Owed names them.
			{"rider_owes", `SELECT m.rider_id::text, NULL::uuid, NULL::uuid, sum(m.amount_kobo - m.paid_kobo),
				'a rider owes ' || ` + nairaSQL("sum(m.amount_kobo - m.paid_kobo)") + ` || ' in cash taken at the door since ' || to_char(min(m.created_at) AT TIME ZONE 'Africa/Lagos', 'DD Mon YYYY') || ', above your limit of ' || ` + nairaSQL("COALESCE(s.owe_limit_kobo, 2000000)") + `
				FROM rider_remittances m LEFT JOIN dispatch_settings s ON s.tenant_id = m.tenant_id
				WHERE m.tenant_id = $1 AND m.settled_at IS NULL
				GROUP BY m.rider_id, s.owe_limit_kobo HAVING sum(m.amount_kobo - m.paid_kobo) > COALESCE(s.owe_limit_kobo, 2000000)`},
			// Stock (stock K4): a sale of more than the shelf had, a count
			// below zero in the table, and a count's loss above the
			// business's limit (₦5,000 unless set), each valued at the
			// item's cost when it moved (else its last known cost; units
			// only where it has none). They stay until closed.
			{"stock_short", `SELECT m.id::text, NULL::uuid, NULL::uuid, m.short * ` + stockCostSQL + `,
				'sold ' || m.short || ' more ' || m.item || ' than the stock held' || COALESCE(' (' || m.reference || ')', '') || ', worth ' || ` + nairaSQL("(m.short * "+stockCostSQL+")") + ` || ' at cost'
				FROM stock_movements m WHERE m.tenant_id = $1 AND m.status = 'done' AND m.short > 0`},
			{"stock_negative", `SELECT m.id::text, NULL::uuid, NULL::uuid, -m.count_after * ` + stockCostSQL + `,
				m.item || ' went below zero (' || m.count_after || ') in your table, worth ' || ` + nairaSQL("(-m.count_after * "+stockCostSQL+")") + ` || ' at cost'
				FROM stock_movements m WHERE m.tenant_id = $1 AND m.status = 'done' AND m.count_after < 0`},
			{"stock_count", `SELECT m.id::text, NULL::uuid, NULL::uuid, -m.change * ` + stockCostSQL + `,
				'a count found ' || -m.change || ' ' || m.item || ' fewer than recorded' || COALESCE(' (' || m.reference || ')', '') || ', worth ' || ` + nairaSQL("(-m.change * "+stockCostSQL+")") + ` || ' at cost'
				FROM stock_movements m LEFT JOIN stock_settings ss ON ss.tenant_id = m.tenant_id
				WHERE m.tenant_id = $1 AND m.status = 'done' AND m.reason = 'count' AND m.change < 0
				  AND (` + stockCostSQL + ` = 0 OR -m.change * ` + stockCostSQL + ` > COALESCE(ss.count_limit_kobo, 500000))`},
		}
		current := map[string]map[string]bool{}
		for _, f := range found {
			args := []any{tenant}
			if strings.Contains(f.query, "$2") {
				args = append(args, now.Add(-UnmatchedAfter))
			}
			rows, err := tx.Query(ctx, f.query, args...)
			if err != nil {
				return fmt.Errorf("%s: %w", f.kind, err)
			}
			type hit struct {
				subject string
				entry   *uuid.UUID
				invoice *uuid.UUID
				amount  int64
				detail  string
			}
			hits, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (hit, error) {
				var h hit
				return h, r.Scan(&h.subject, &h.entry, &h.invoice, &h.amount, &h.detail)
			})
			if err != nil {
				return fmt.Errorf("%s: %w", f.kind, err)
			}
			current[f.kind] = map[string]bool{}
			for _, h := range hits {
				current[f.kind][h.subject] = true
				if len(h.detail) > 300 {
					h.detail = h.detail[:300]
				}
				tag, err := tx.Exec(ctx, `INSERT INTO money_leaks (id, tenant_id, kind, subject, entry_id, invoice_id, amount_kobo, detail, opened_at)
					VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9) ON CONFLICT (tenant_id, kind, subject) DO NOTHING`,
					uuid.New(), tenant, f.kind, h.subject, h.entry, h.invoice, h.amount, h.detail, now)
				if err != nil {
					return err
				}
				if tag.RowsAffected() == 1 {
					opened++
					continue
				}
				// Still open: the amount may have moved (a part payment).
				if _, err := tx.Exec(ctx, `UPDATE money_leaks SET amount_kobo = $4, detail = $5 WHERE tenant_id = $1 AND kind = $2 AND subject = $3 AND status = 'open'`,
					tenant, f.kind, h.subject, h.amount, h.detail); err != nil {
					return err
				}
			}
		}
		// Open leaks whose cause went away close themselves.
		rows, err := tx.Query(ctx, `SELECT id, kind, subject FROM money_leaks WHERE tenant_id = $1 AND status = 'open'`, tenant)
		if err != nil {
			return err
		}
		type open struct {
			id            uuid.UUID
			kind, subject string
		}
		list, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (open, error) {
			var o open
			return o, r.Scan(&o.id, &o.kind, &o.subject)
		})
		if err != nil {
			return err
		}
		for _, o := range list {
			if current[o.kind][o.subject] {
				continue
			}
			if _, err := tx.Exec(ctx, `UPDATE money_leaks SET status = 'resolved', closed_at = $3, closed_by = 'system:leaks' WHERE tenant_id = $1 AND id = $2`,
				tenant, o.id, now); err != nil {
				return err
			}
		}
		return nil
	})
	return opened, err
}

// Leak is a leak as the pages show it.
type Leak struct {
	ID            uuid.UUID  `json:"id"`
	Kind          string     `json:"kind"`
	EntryID       *uuid.UUID `json:"entry_id,omitempty"`
	InvoiceID     *uuid.UUID `json:"invoice_id,omitempty"`
	AmountKobo    int64      `json:"amount_kobo"`
	Detail        string     `json:"detail"`
	Status        string     `json:"status"`
	AssignedTo    *uuid.UUID `json:"assigned_to,omitempty"`
	RecoveredKobo *int64     `json:"recovered_kobo,omitempty"`
	Note          string     `json:"note,omitempty"`
	OpenedAt      time.Time  `json:"opened_at"`
	ClosedAt      *time.Time `json:"closed_at,omitempty"`
	ClosedBy      string     `json:"closed_by,omitempty"`
}

const cols = `id, kind, entry_id, invoice_id, amount_kobo, detail, status, assigned_to, recovered_kobo, COALESCE(note, ''), opened_at, closed_at, COALESCE(closed_by, '')`

func scan(r pgx.Row) (Leak, error) {
	var l Leak
	err := r.Scan(&l.ID, &l.Kind, &l.EntryID, &l.InvoiceID, &l.AmountKobo, &l.Detail, &l.Status, &l.AssignedTo, &l.RecoveredKobo, &l.Note, &l.OpenedAt, &l.ClosedAt, &l.ClosedBy)
	return l, err
}

// List lists a tenant's leaks: status "open" (the default), "closed", or
// "all"; newest first.
func List(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, status string, limit int) ([]Leak, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	where := `status = 'open'`
	switch status {
	case "closed":
		where = `status <> 'open'`
	case "all":
		where = `true`
	}
	rows, err := tx.Query(ctx, `SELECT `+cols+` FROM money_leaks WHERE tenant_id = $1 AND `+where+` ORDER BY opened_at DESC, id LIMIT $2`, tenant, limit)
	if err != nil {
		return nil, err
	}
	out, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (Leak, error) { return scan(r) })
	if out == nil {
		out = []Leak{}
	}
	return out, err
}

// Totals are what the Money page shows of leaks.
type Totals struct {
	Open          int   `json:"open"`
	OpenKobo      int64 `json:"open_kobo"`
	RecoveredKobo int64 `json:"recovered_kobo"`
}

// Sum is a tenant's open leaks and the amount recovered.
func Sum(ctx context.Context, tx pgx.Tx, tenant uuid.UUID) (Totals, error) {
	var t Totals
	err := tx.QueryRow(ctx, `SELECT count(*) FILTER (WHERE status = 'open'), COALESCE(sum(amount_kobo) FILTER (WHERE status = 'open'), 0),
		COALESCE(sum(recovered_kobo) FILTER (WHERE status = 'recovered'), 0) FROM money_leaks WHERE tenant_id = $1`, tenant).Scan(&t.Open, &t.OpenKobo, &t.RecoveredKobo)
	return t, err
}

// Errors of the lifecycle.
var (
	ErrNotFound = errors.New("leaks: no such leak")
	ErrState    = errors.New("leaks: the leak is closed")
	ErrInvalid  = errors.New("leaks: invalid")
)

// Assign gives an open leak to a member (uuid.Nil: to no one).
func Assign(ctx context.Context, tx pgx.Tx, tenant, id, member uuid.UUID) (Leak, error) {
	var to *uuid.UUID
	if member != uuid.Nil {
		to = &member
	}
	return change(ctx, tx, tenant, id, func() (string, []any) {
		return `assigned_to = $3`, []any{to}
	})
}

// Close closes an open leak: recovered (with the amount got back),
// written_off or not_a_leak, with an optional note.
func Close(ctx context.Context, tx pgx.Tx, tenant, id uuid.UUID, status string, recoveredKobo *int64, note, by string, now time.Time) (Leak, error) {
	switch status {
	case "recovered":
		if recoveredKobo == nil || *recoveredKobo <= 0 {
			return Leak{}, fmt.Errorf("%w: say how much was recovered", ErrInvalid)
		}
	case "written_off", "not_a_leak":
		recoveredKobo = nil
	default:
		return Leak{}, fmt.Errorf("%w: a leak is closed as recovered, written_off or not_a_leak", ErrInvalid)
	}
	if len(note) > 500 {
		return Leak{}, fmt.Errorf("%w: the note is at most 500 characters", ErrInvalid)
	}
	return change(ctx, tx, tenant, id, func() (string, []any) {
		return `status = $3, recovered_kobo = $4, note = NULLIF($5, ''), closed_at = $6, closed_by = $7`, []any{status, recoveredKobo, note, now, by}
	})
}

func change(ctx context.Context, tx pgx.Tx, tenant, id uuid.UUID, set func() (string, []any)) (Leak, error) {
	l, err := scan(tx.QueryRow(ctx, `SELECT `+cols+` FROM money_leaks WHERE tenant_id = $1 AND id = $2 FOR UPDATE`, tenant, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return Leak{}, ErrNotFound
	}
	if err != nil {
		return Leak{}, err
	}
	if l.Status != "open" {
		return Leak{}, fmt.Errorf("%w (%s)", ErrState, l.Status)
	}
	q, args := set()
	if _, err := tx.Exec(ctx, `UPDATE money_leaks SET `+q+` WHERE tenant_id = $1 AND id = $2`, append([]any{tenant, id}, args...)...); err != nil {
		return Leak{}, err
	}
	return scan(tx.QueryRow(ctx, `SELECT `+cols+` FROM money_leaks WHERE tenant_id = $1 AND id = $2`, tenant, id))
}

// FeeRate is the fee agreed with a connection: percent (in basis points
// of the amount) plus a flat fee, up to a cap.
type FeeRate struct {
	Channel   string `json:"channel"`
	PercentBP int    `json:"percent_bp"`
	FlatKobo  int64  `json:"flat_kobo"`
	CapKobo   *int64 `json:"cap_kobo,omitempty"`
}

// FeeRates lists a tenant's agreed fees.
func FeeRates(ctx context.Context, tx pgx.Tx, tenant uuid.UUID) ([]FeeRate, error) {
	rows, err := tx.Query(ctx, `SELECT channel, percent_bp, flat_kobo, cap_kobo FROM money_fee_rates WHERE tenant_id = $1 ORDER BY channel`, tenant)
	if err != nil {
		return nil, err
	}
	out, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (FeeRate, error) {
		var f FeeRate
		return f, r.Scan(&f.Channel, &f.PercentBP, &f.FlatKobo, &f.CapKobo)
	})
	if out == nil {
		out = []FeeRate{}
	}
	return out, err
}

// SetFeeRate sets the fee agreed for a connection.
func SetFeeRate(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, f FeeRate) error {
	if f.Channel == "" || len(f.Channel) > 200 || f.PercentBP < 0 || f.PercentBP > 10000 || f.FlatKobo < 0 || (f.CapKobo != nil && *f.CapKobo < 0) {
		return fmt.Errorf("%w: a rate needs its connection, a percent from 0 to 100, and amounts not below zero", ErrInvalid)
	}
	_, err := tx.Exec(ctx, `INSERT INTO money_fee_rates (tenant_id, channel, percent_bp, flat_kobo, cap_kobo) VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (tenant_id, channel) DO UPDATE SET percent_bp = $3, flat_kobo = $4, cap_kobo = $5, updated_at = now()`,
		tenant, f.Channel, f.PercentBP, f.FlatKobo, f.CapKobo)
	return err
}
