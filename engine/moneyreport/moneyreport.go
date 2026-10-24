// Package moneyreport sums up and exports a business's money (decision
// 0045; docs/spec/invoices-and-money-log.md 9.2, 9.3, T7;
// docs/money-reports.md): a period's money in and out, settled and
// pending, by channel, with the fees and the effective fee rate, the
// invoices paid and the leaks; and the period's money log, invoices and
// an accounting journal as CSV.
//
// A movement can have several entries in the log (a payout that settled,
// then was reversed, names the first in corrects): every figure here is
// of each movement's latest entry, as the log's readers are told to take,
// with the first entry's amount when a later one has none (a reversal
// rarely says how much).
package moneyreport

import (
	"context"
	"encoding/csv"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Lagos is where days and months begin for a business's reports.
var Lagos = func() *time.Location {
	if l, err := time.LoadLocation("Africa/Lagos"); err == nil {
		return l
	}
	return time.FixedZone("WAT", 3600)
}()

// latest is each movement's latest entry: the rows of the period are the
// movements whose first entry was recorded in it.
const latest = `WITH moves AS (
	SELECT DISTINCT ON (COALESCE(e.corrects, e.id)) e.*, COALESCE(e.corrects, e.id) AS movement
	  FROM money_entries e WHERE e.tenant_id = $1
	 ORDER BY COALESCE(e.corrects, e.id), e.recorded_at DESC, e.id DESC
), firsts AS (
	SELECT id, COALESCE(occurred_at, recorded_at) AS at, amount_kobo FROM money_entries WHERE tenant_id = $1 AND corrects IS NULL
)
SELECT m.*, COALESCE(m.amount_kobo, f.amount_kobo, 0) AS amount FROM moves m JOIN firsts f ON f.id = m.movement WHERE f.at >= $2 AND f.at < $3`

// Channel is one channel's money in a period.
type Channel struct {
	Channel     string `json:"channel"`
	InSettled   int64  `json:"in_settled_kobo"`
	InPending   int64  `json:"in_pending_kobo"`
	OutSettled  int64  `json:"out_settled_kobo"`
	OutPending  int64  `json:"out_pending_kobo"`
	FeesKobo    int64  `json:"fees_kobo"`
	FeeRateBP   *int   `json:"fee_rate_bp,omitempty"` // fees over the money in they were taken on, in basis points
	Movements   int    `json:"movements"`
	FeeBaseKobo int64  `json:"-"`
}

// Summary is a period's money.
type Summary struct {
	From       time.Time `json:"from"`
	To         time.Time `json:"to"`
	InSettled  int64     `json:"in_settled_kobo"`
	InPending  int64     `json:"in_pending_kobo"`
	OutSettled int64     `json:"out_settled_kobo"`
	OutPending int64     `json:"out_pending_kobo"`
	FeesKobo   int64     `json:"fees_kobo"`
	FeeRateBP  *int      `json:"fee_rate_bp,omitempty"`
	Channels   []Channel `json:"channels"`
	// Invoices paid in the period: how many and what was paid on them.
	InvoicesPaid     int   `json:"invoices_paid"`
	InvoicesPaidKobo int64 `json:"invoices_paid_kobo"`
	// Leaks opened in the period, and those open now with the total
	// recovered so far.
	LeaksOpened     int   `json:"leaks_opened"`
	LeaksOpenedKobo int64 `json:"leaks_opened_kobo"`
	LeaksOpen       int   `json:"leaks_open"`
	LeaksOpenKobo   int64 `json:"leaks_open_kobo"`
	RecoveredKobo   int64 `json:"recovered_kobo"`
}

// Day is the Lagos day holding d, as [from, to).
func Day(d time.Time) (time.Time, time.Time) {
	d = d.In(Lagos)
	from := time.Date(d.Year(), d.Month(), d.Day(), 0, 0, 0, 0, Lagos)
	return from, from.AddDate(0, 0, 1)
}

// Month is the Lagos month holding d.
func Month(d time.Time) (time.Time, time.Time) {
	d = d.In(Lagos)
	from := time.Date(d.Year(), d.Month(), 1, 0, 0, 0, 0, Lagos)
	return from, from.AddDate(0, 1, 0)
}

// Summarise sums up the tenant's money in [from, to).
func Summarise(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, from, to time.Time) (Summary, error) {
	s := Summary{From: from, To: to, Channels: []Channel{}}
	rows, err := tx.Query(ctx, `SELECT m.channel, m.direction, m.status, m.amount, COALESCE(m.fee_kobo, 0), m.fee_kobo IS NOT NULL
		FROM (`+latest+`) m`, tenant, from, to)
	if err != nil {
		return s, err
	}
	by := map[string]*Channel{}
	for rows.Next() {
		var channel, dir, status string
		var amount, fee int64
		var hasFee bool
		if err := rows.Scan(&channel, &dir, &status, &amount, &fee, &hasFee); err != nil {
			rows.Close()
			return s, err
		}
		c := by[channel]
		if c == nil {
			c = &Channel{Channel: channel}
			by[channel] = c
		}
		c.Movements++
		switch {
		case dir == "in" && status == "settled":
			c.InSettled += amount
			if hasFee {
				c.FeeBaseKobo += amount
			}
		case dir == "in" && status == "pending":
			c.InPending += amount
		case dir == "out" && status == "settled":
			c.OutSettled += amount
		case dir == "out" && status == "pending":
			c.OutPending += amount
		}
		if status == "settled" {
			c.FeesKobo += fee
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return s, err
	}
	var feeBase int64
	for _, c := range by {
		c.FeeRateBP = rate(c.FeesKobo, c.FeeBaseKobo)
		s.InSettled += c.InSettled
		s.InPending += c.InPending
		s.OutSettled += c.OutSettled
		s.OutPending += c.OutPending
		s.FeesKobo += c.FeesKobo
		feeBase += c.FeeBaseKobo
		s.Channels = append(s.Channels, *c)
	}
	sort.Slice(s.Channels, func(i, j int) bool {
		a, b := s.Channels[i], s.Channels[j]
		if a.InSettled != b.InSettled {
			return a.InSettled > b.InSettled
		}
		return a.Channel < b.Channel
	})
	s.FeeRateBP = rate(s.FeesKobo, feeBase)
	if err := tx.QueryRow(ctx, `SELECT count(*), COALESCE(sum(paid_kobo), 0) FROM customer_invoices WHERE tenant_id = $1 AND status = 'paid' AND paid_at >= $2 AND paid_at < $3`,
		tenant, from, to).Scan(&s.InvoicesPaid, &s.InvoicesPaidKobo); err != nil {
		return s, err
	}
	err = tx.QueryRow(ctx, `SELECT count(*) FILTER (WHERE opened_at >= $2 AND opened_at < $3), COALESCE(sum(amount_kobo) FILTER (WHERE opened_at >= $2 AND opened_at < $3), 0),
		count(*) FILTER (WHERE status = 'open'), COALESCE(sum(amount_kobo) FILTER (WHERE status = 'open'), 0),
		COALESCE(sum(recovered_kobo) FILTER (WHERE status = 'recovered'), 0) FROM money_leaks WHERE tenant_id = $1`, tenant, from, to).
		Scan(&s.LeaksOpened, &s.LeaksOpenedKobo, &s.LeaksOpen, &s.LeaksOpenKobo, &s.RecoveredKobo)
	return s, err
}

func rate(fees, base int64) *int {
	if base <= 0 {
		return nil
	}
	bp := int((fees*10000 + base/2) / base)
	return &bp
}

// Naira writes kobo for a CSV: 25000.50, -1200.00.
func Naira(kobo int64) string {
	neg := kobo < 0
	if neg {
		kobo = -kobo
	}
	s := fmt.Sprintf("%d.%02d", kobo/100, kobo%100)
	if neg {
		s = "-" + s
	}
	return s
}

func day(t time.Time) string { return t.In(Lagos).Format("2006-01-02") }

// text guards a text cell against formula injection (CSV injection), as
// table exports do: a value a spreadsheet would run as a formula gets a
// leading apostrophe. Statement lines and rails' references are not
// Taskiem's words. Amounts are written by Naira and stay numbers.
func text(v string) string {
	if v != "" && strings.ContainsRune("=+-@\t\r", rune(v[0])) {
		return "'" + v
	}
	return v
}

// The invoices and storefront orders a movement is counted towards, each
// as its numbers joined by spaces (matches not undone).
const (
	matchedInvoices = `COALESCE((SELECT string_agg(i.number, ' ' ORDER BY i.number) FROM customer_invoice_payments p JOIN customer_invoices i ON i.id = p.invoice_id
			          WHERE p.entry_id = m.movement AND p.undoes IS NULL AND NOT EXISTS (SELECT 1 FROM customer_invoice_payments u WHERE u.undoes = p.id)), '')`
	matchedOrders = `COALESCE((SELECT string_agg(o.reference, ' ' ORDER BY o.reference) FROM storefront_order_payments p JOIN storefront_orders o ON o.id = p.order_id
			          WHERE p.entry_id = m.movement AND p.undoes IS NULL AND NOT EXISTS (SELECT 1 FROM storefront_order_payments u WHERE u.undoes = p.id)), '')`
	// What of a movement counts towards invoices, and towards orders: the
	// sums of its parts (a split counts towards several).
	invoicedKobo = `COALESCE((SELECT sum(p.amount_kobo) FROM customer_invoice_payments p
			          WHERE p.entry_id = m.movement AND p.undoes IS NULL AND NOT EXISTS (SELECT 1 FROM customer_invoice_payments u WHERE u.undoes = p.id)), 0)::bigint`
	orderedKobo = `COALESCE((SELECT sum(p.amount_kobo) FROM storefront_order_payments p
			          WHERE p.entry_id = m.movement AND p.undoes IS NULL AND NOT EXISTS (SELECT 1 FROM storefront_order_payments u WHERE u.undoes = p.id)), 0)::bigint`
	// Whether a movement is a customer's money (paid into their account,
	// or left as their credit): what no invoice counts is their credit.
	customersMoney = `EXISTS (SELECT 1 FROM money_entry_customers c WHERE c.entry_id = m.movement)`
)

// ExportLog writes the period's money log as CSV: one line per movement,
// its latest status, money in positive and out negative.
func ExportLog(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, from, to time.Time, w io.Writer) error {
	rows, err := tx.Query(ctx, `SELECT m.movement, COALESCE(m.occurred_at, m.recorded_at), m.recorded_at, m.direction, m.kind, m.status, m.amount,
			COALESCE(m.fee_kobo, 0), m.currency, m.channel, m.reference, COALESCE(m.description, m.note, ''), COALESCE(m.vendor, ''), COALESCE(m.category, ''), m.source,
			`+matchedInvoices+`, `+matchedOrders+`
		FROM (`+latest+`) m ORDER BY COALESCE(m.occurred_at, m.recorded_at), m.movement`, tenant, from, to)
	if err != nil {
		return err
	}
	defer rows.Close()
	cw := csv.NewWriter(w)
	_ = cw.Write([]string{"date", "entry_id", "direction", "kind", "status", "amount", "fee", "currency", "channel", "reference", "description", "vendor", "category", "source", "invoice", "order"})
	for rows.Next() {
		var id uuid.UUID
		var at, recorded time.Time
		var dir, kind, status, currency, channel, ref, desc, vendor, category, source, invoice, order string
		var amount, fee int64
		if err := rows.Scan(&id, &at, &recorded, &dir, &kind, &status, &amount, &fee, &currency, &channel, &ref, &desc, &vendor, &category, &source, &invoice, &order); err != nil {
			return err
		}
		if dir == "out" {
			amount = -amount
		}
		_ = cw.Write([]string{day(at), id.String(), dir, kind, status, Naira(amount), Naira(fee), currency, text(channel), text(ref), text(desc), text(vendor), text(category), source, text(invoice), text(order)})
	}
	if err := rows.Err(); err != nil {
		return err
	}
	cw.Flush()
	return cw.Error()
}

// ExportInvoices writes the invoices sent in the period as CSV. The
// customer's name is included only when names says so (a person allowed
// to see personal data); their phone and email never are.
func ExportInvoices(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, from, to time.Time, name func(id uuid.UUID) string, w io.Writer) error {
	rows, err := tx.Query(ctx, `SELECT id, number, status, sent_at, due_on, total_kobo, paid_kobo, paid_at, voided_at FROM customer_invoices
		WHERE tenant_id = $1 AND sent_at >= $2 AND sent_at < $3 ORDER BY sent_at, number`, tenant, from, to)
	if err != nil {
		return err
	}
	type row struct {
		id             uuid.UUID
		number, status string
		sent           time.Time
		due            *time.Time
		total, paid    int64
		paidAt, voided *time.Time
	}
	list, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (row, error) {
		var x row
		return x, r.Scan(&x.id, &x.number, &x.status, &x.sent, &x.due, &x.total, &x.paid, &x.paidAt, &x.voided)
	})
	if err != nil {
		return err
	}
	opt := func(t *time.Time) string {
		if t == nil {
			return ""
		}
		return day(*t)
	}
	cw := csv.NewWriter(w)
	_ = cw.Write([]string{"number", "status", "customer", "sent", "due", "total", "paid", "still_due", "paid_on", "voided_on"})
	for _, x := range list {
		dueOn := ""
		if x.due != nil {
			dueOn = x.due.Format("2006-01-02")
		}
		still := x.total - x.paid
		if still < 0 || x.status == "void" {
			still = 0
		}
		cust := ""
		if name != nil {
			// The rows are read: the transaction is free for the lookup.
			cust = name(x.id)
		}
		_ = cw.Write([]string{text(x.number), x.status, text(cust), day(x.sent), dueOn, Naira(x.total), Naira(x.paid), Naira(still), opt(x.paidAt), opt(x.voided)})
	}
	cw.Flush()
	return cw.Error()
}

// Journal account names, as a bookkeeping tool takes them.
const (
	AccountReceivable = "Accounts receivable"
	AccountSales      = "Sales"
	AccountUnmatched  = "Money in to identify"
	AccountFees       = "Bank and payment fees"
	AccountPayouts    = "Payouts"
	AccountRefunds    = "Refunds"
	AccountOwner      = "Owner's cash movements"
	// AccountCredit is what customers paid that no invoice counts yet:
	// owed back to them, or counted towards their next invoice.
	AccountCredit = "Customer credit"
)

// Bank is a channel as a journal account: "Bank: paystack/main", "Cash".
func Bank(channel string) string {
	if channel == "cash" {
		return "Cash"
	}
	return "Bank: " + channel
}

// Expense is an expense category as a journal account.
func Expense(category string) string {
	if category == "" {
		category = "other"
	}
	return "Expenses: " + strings.ToUpper(category[:1]) + category[1:]
}

// ExportJournal writes the period's settled movements as a double-entry
// journal: every movement two lines (or four with a fee) whose debits and
// credits balance. Money in matched to invoices settles receivables, each
// part of a split for what it counts; POS sales and money for storefront
// orders are sales; what is left of a customer's money is their credit;
// other money in waits to be identified. Money a rail settled net of its
// fee counts for what the payer sent: the bank gets what landed and the
// fee is a cost. Payouts and expenses leave the bank they went through.
func ExportJournal(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, from, to time.Time, w io.Writer) error {
	rows, err := tx.Query(ctx, `SELECT m.movement, COALESCE(m.occurred_at, m.recorded_at), m.direction, m.kind, m.amount, COALESCE(m.fee_kobo, 0),
			m.channel, m.reference, COALESCE(m.description, m.note, ''), COALESCE(m.category, ''),
			`+matchedInvoices+`, `+matchedOrders+`, `+invoicedKobo+`, `+orderedKobo+`, m.net, `+customersMoney+`
		FROM (`+latest+`) m WHERE m.status = 'settled' AND m.amount > 0
		ORDER BY COALESCE(m.occurred_at, m.recorded_at), m.movement`, tenant, from, to)
	if err != nil {
		return err
	}
	defer rows.Close()
	cw := csv.NewWriter(w)
	_ = cw.Write([]string{"date", "journal_id", "account", "debit", "credit", "description", "reference"})
	line := func(at time.Time, id uuid.UUID, account string, debit, credit int64, desc, ref string) {
		if debit <= 0 && credit <= 0 {
			return // a part of nothing (a split with no rest)
		}
		d, c := "", ""
		if debit > 0 {
			d = Naira(debit)
		}
		if credit > 0 {
			c = Naira(credit)
		}
		_ = cw.Write([]string{day(at), id.String(), text(account), d, c, text(desc), text(ref)})
	}
	for rows.Next() {
		var id uuid.UUID
		var at time.Time
		var dir, kind, channel, ref, desc, category, invoice, order string
		var amount, fee, invoiced, ordered int64
		var net, customers bool
		if err := rows.Scan(&id, &at, &dir, &kind, &amount, &fee, &channel, &ref, &desc, &category, &invoice, &order, &invoiced, &ordered, &net, &customers); err != nil {
			return err
		}
		bank := Bank(channel)
		if desc == "" {
			desc = strings.ReplaceAll(kind, "_", " ")
		}
		switch {
		case invoice != "":
			desc = "Invoice " + invoice
		case order != "":
			desc = "Order " + order
		}
		if dir == "in" {
			gross := amount
			if net {
				gross += fee
			}
			line(at, id, bank, amount, 0, desc, ref)
			if net && fee > 0 {
				line(at, id, AccountFees, fee, 0, "Fee: "+desc, ref)
			}
			if invoiced+ordered == 0 {
				other := AccountUnmatched
				switch {
				case customers:
					other = AccountCredit
				case kind == "pos_sale":
					other = AccountSales
				case kind == "cash":
					other = AccountOwner
				}
				line(at, id, other, 0, gross, desc, ref)
			} else {
				// Each kind of part for what it counts; the rest is the
				// customer's credit, or waits to be identified.
				rest := gross - min(invoiced+ordered, gross)
				line(at, id, AccountReceivable, 0, min(invoiced, gross), desc, ref)
				line(at, id, AccountSales, 0, min(ordered, gross-min(invoiced, gross)), desc, ref)
				if customers {
					line(at, id, AccountCredit, 0, rest, desc, ref)
				} else {
					line(at, id, AccountUnmatched, 0, rest, desc, ref)
				}
			}
			if !net && fee > 0 {
				line(at, id, AccountFees, fee, 0, "Fee: "+desc, ref)
				line(at, id, bank, 0, fee, "Fee: "+desc, ref)
			}
			continue
		}
		other := AccountPayouts
		switch kind {
		case "expense":
			other = Expense(category)
		case "refund":
			other = AccountRefunds
		case "cash":
			other = AccountOwner
		case "statement":
			other = AccountUnmatched
		}
		line(at, id, other, amount, 0, desc, ref)
		line(at, id, bank, 0, amount, desc, ref)
		if fee > 0 {
			line(at, id, AccountFees, fee, 0, "Fee: "+desc, ref)
			line(at, id, bank, 0, fee, "Fee: "+desc, ref)
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	cw.Flush()
	return cw.Error()
}

// Kinds of export.
var Exports = []string{"log", "invoices", "journal"}

// ParseDay reads a report date: 2026-10-23.
func ParseDay(s string) (time.Time, error) {
	return time.ParseInLocation("2006-01-02", s, Lagos)
}
