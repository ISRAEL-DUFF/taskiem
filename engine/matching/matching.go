// Package matching matches the money a business receives to its invoices
// and storefront orders (decision 0045 point 6; docs/spec/
// invoices-and-money-log.md 8, T5; docs/matching.md).
//
// Every money-in entry of the money log is looked at once. A payment into
// an account number or link Taskiem gave for an invoice, or through a
// storefront order's checkout page, is matched exactly, with no one asked.
// Any other gets up to three suggestions among the business's open
// invoices and orders, each with its reasons; a person confirms or rejects
// one, or, when the business has turned automatic matching on, a strong
// and clear suggestion is matched on its own. A match is a payment of the
// invoice (customer_invoice_payments) or of the order
// (storefront_order_payments): undone, never changed.
package matching

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/invoicing"
	"github.com/israel-duff/taskiem/engine/moneylog"
	"github.com/israel-duff/taskiem/engine/pii"
	"github.com/israel-duff/taskiem/engine/storefront"
)

// Thresholds of the score (0 to 100).
const (
	// Suggest is the least score offered as a suggestion.
	Suggest = 40
	// Auto is the least score matched without asking, when the business
	// has automatic matching on, and only when the next suggestion is at
	// least Margin below it.
	Auto   = 85
	Margin = 15
	// MaxSuggestions per payment.
	MaxSuggestions = 3
)

// Matcher looks at new money-in entries and matches or suggests.
type Matcher struct {
	Invoices *invoicing.Invoices
	// Orders are the storefront's orders; nil: orders are not matched.
	Orders *storefront.Orders
	PII    pii.Cipher
	Logger *slog.Logger
	Now    func() time.Time
	// Suggested, when set, is told of a payment that got suggestions and
	// was not matched on its own, with its new suggestions (ids set), best
	// first: chat asks the business's people (api). It is not told of
	// exact or automatic matches.
	Suggested func(ctx context.Context, tenant, entryID uuid.UUID, top []Suggestion)
	// Reversed, when set, is told in a reversal's transaction of the
	// invoice parts (customer_invoice_payments) undone because their money
	// went back to the payer (reversals.go): receipts (B5) are voided here.
	Reversed func(ctx context.Context, tx pgx.Tx, tenant, entry uuid.UUID, parts []uuid.UUID) error
	// Claims lists a school's "I have paid" claims beside the unmatched
	// money (unmatched.go); nil: none.
	Claims ClaimSource
}

// OutboxMatch is the chat outbox kind of a payment a member is asked to
// match (api).
const OutboxMatch = "match"

func (m *Matcher) now() time.Time {
	if m.Now != nil {
		return m.Now()
	}
	return time.Now()
}

func (m *Matcher) log() *slog.Logger {
	if m.Logger != nil {
		return m.Logger
	}
	return slog.Default()
}

// Run looks every 10 seconds until ctx ends.
func (m *Matcher) Run(ctx context.Context) error {
	for {
		if err := m.Tick(ctx); err != nil && ctx.Err() == nil {
			m.log().Error("matching", "err", err)
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(10 * time.Second):
		}
	}
}

// Tick looks at what is new for every tenant.
func (m *Matcher) Tick(ctx context.Context) error {
	rows, err := m.Invoices.Pool.Query(ctx, `SELECT tenant_id FROM taskiem_money_unconsidered_tenants()`)
	if err != nil {
		return err
	}
	tenants, err := pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
	if err != nil {
		return err
	}
	for _, t := range tenants {
		if err := m.Tenant(ctx, t); err != nil && ctx.Err() == nil {
			m.log().Warn("matching", "tenant", t, "err", err)
		}
	}
	// Money that went back to the payer: its matches undone (reversals.go).
	return m.Reversals(ctx)
}

// entry is a money-in entry as matching sees it. AmountKobo is what it
// counts for (moneylog.CountableKobo): what the payer sent.
type entry struct {
	ID            uuid.UUID
	When          time.Time
	AmountKobo    int64
	Channel       string
	Reference     string
	AccountNumber string
	Payer         string
	PayerAccount  string
	// Reversed is what of it went back to the payer already (a reversal
	// heard before the money was looked at); AmountKobo is net of it.
	Reversed int64
}

// Tenant looks at up to 100 of a tenant's new money-in entries.
func (m *Matcher) Tenant(ctx context.Context, tenant uuid.UUID) error {
	var list []entry
	err := db.InTenantTx(ctx, m.Invoices.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT e.id, COALESCE(e.occurred_at, e.recorded_at), e.amount_kobo, e.fee_kobo, e.net, e.channel, e.reference,
				COALESCE(e.account_number, ''), e.payer, taskiem_money_entry_reversed(e.id)
			FROM money_entries e WHERE e.tenant_id = $1 AND e.direction = 'in' AND e.status = 'settled'
			  AND e.kind IN ('payment', 'account_credit', 'pos_sale', 'statement', 'cash')
			  AND NOT EXISTS (SELECT 1 FROM money_match_state s WHERE s.entry_id = e.id)
			ORDER BY e.recorded_at, e.id LIMIT 100`, tenant)
		if err != nil {
			return err
		}
		type raw struct {
			e     entry
			payer map[string]any
		}
		rs, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (raw, error) {
			var x raw
			var m moneylog.Entry
			err := r.Scan(&x.e.ID, &x.e.When, &m.AmountKobo, &m.FeeKobo, &m.Net, &x.e.Channel, &x.e.Reference, &x.e.AccountNumber, &x.payer, &x.e.Reversed)
			x.e.AmountKobo = moneylog.CountableKobo(m) - x.e.Reversed
			return x, err
		})
		if err != nil {
			return err
		}
		for _, x := range rs {
			if x.payer != nil && m.PII != nil {
				v, err := m.PII.OpenTx(ctx, tx, tenant, x.payer)
				if err != nil && !errors.Is(err, pii.ErrErased) {
					return err
				}
				if p, ok := v.(map[string]any); ok {
					x.e.Payer, _ = p["name"].(string)
					x.e.PayerAccount, _ = p["account"].(string)
				}
			}
			list = append(list, x.e)
		}
		return nil
	})
	if err != nil {
		return err
	}
	for _, e := range list {
		if err := m.consider(ctx, tenant, e); err != nil {
			return err
		}
	}
	return nil
}

// consider matches one entry exactly, or suggests, and remembers it did.
func (m *Matcher) consider(ctx context.Context, tenant uuid.UUID, e entry) error {
	if e.Reversed > 0 && e.AmountKobo <= 0 {
		// All of it went back to the payer before it was looked at.
		return m.hold(ctx, tenant, e.ID, "matched", HeldReversed)
	}
	// Exact: the reference is one Taskiem gave for an invoice.
	var account, invoice uuid.UUID
	err := db.InTenantTx(ctx, m.Invoices.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT id, invoice_id FROM customer_invoice_accounts WHERE tenant_id = $1 AND payment_id = $2 AND connector || '/' || connection = $3`,
			tenant, e.Reference, e.Channel).Scan(&account, &invoice)
	})
	switch {
	case err == nil:
		if e.AmountKobo <= 0 {
			return m.mark(ctx, tenant, e.ID, "unmatched")
		}
		err := m.Invoices.Received(ctx, tenant, invoice, invoicing.Received{PaymentID: e.Reference, AmountKobo: e.AmountKobo, Connection: e.Channel, Account: account})
		if errors.Is(err, invoicing.ErrState) {
			// A void invoice, or the money already counted elsewhere: a
			// person decides.
			m.log().Info("matching: exact match refused", "tenant", tenant, "entry", e.ID, "err", err)
			return m.mark(ctx, tenant, e.ID, "unmatched")
		}
		if err != nil {
			return err
		}
		return m.mark(ctx, tenant, e.ID, "matched")
	case !errors.Is(err, pgx.ErrNoRows):
		return err
	}
	// Exact: paid into a customer's permanent account (or held for a person
	// when the account and reference disagree).
	if done, err := m.permanent(ctx, tenant, e); done || err != nil {
		return err
	}
	// Exact: the reference is a storefront order's checkout page.
	if m.Orders != nil {
		var order string
		err := db.InTenantTx(ctx, m.Invoices.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
			var err error
			order, err = storefront.ForCheckout(ctx, tx, tenant, e.Channel, e.Reference)
			return err
		})
		switch {
		case err == nil:
			return m.exactOrder(ctx, tenant, e, order)
		case !errors.Is(err, storefront.ErrNoOrder):
			return err
		}
	}
	if e.AmountKobo <= 0 {
		return m.mark(ctx, tenant, e.ID, "unmatched")
	}

	// Suggested: the business's open invoices and orders, scored.
	var found []Suggestion
	var auto bool
	err = db.InTenantTx(ctx, m.Invoices.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		st, err := invoicing.GetSettings(ctx, tx, tenant)
		if err != nil {
			return err
		}
		auto = st.AutoMatch
		// Payers remembered: who this payer paid for before.
		remembered, err := m.remembered(ctx, tx, tenant, e.Payer, e.PayerAccount)
		if err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `SELECT id FROM customer_invoices WHERE tenant_id = $1 AND status IN ('sent', 'part_paid', 'overdue')
			ORDER BY sent_at DESC LIMIT 500`, tenant)
		if err != nil {
			return err
		}
		ids, err := pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
		if err != nil {
			return err
		}
		for _, id := range ids {
			x, err := m.Invoices.Get(ctx, tx, tenant, id.String())
			if err != nil {
				return err
			}
			if s, ok := ScoreRemembered(e.AmountKobo, e.Payer, e.Reference, e.When, x, remembered[x.CustomerID()]); ok {
				found = append(found, s)
			}
		}
		if m.Orders == nil {
			return nil
		}
		orders, err := m.Orders.Matchables(ctx, tx, tenant, 500)
		if err != nil {
			return err
		}
		for _, x := range orders {
			if s, ok := ScoreOrder(e.AmountKobo, e.Payer, e.Reference, e.When, x); ok {
				found = append(found, s)
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	sort.SliceStable(found, func(i, j int) bool { return found[i].Score > found[j].Score })
	if len(found) > MaxSuggestions {
		found = found[:MaxSuggestions]
	}
	if len(found) == 0 {
		return m.mark(ctx, tenant, e.ID, "unmatched")
	}
	var top uuid.UUID
	var fresh []Suggestion
	err = db.InTenantTx(ctx, m.Invoices.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		top, fresh = uuid.Nil, fresh[:0]
		for i, s := range found {
			reasons, _ := json.Marshal(s.Reasons)
			id := uuid.New()
			// A suggestion made before (and rejected) is not made again.
			tag, err := tx.Exec(ctx, `INSERT INTO money_suggestions (id, tenant_id, entry_id, invoice_id, order_id, score, reasons) VALUES ($1, $2, $3, $4, $5, $6, $7)
				ON CONFLICT DO NOTHING`, id, tenant, e.ID, s.InvoiceID, s.OrderID, s.Score, reasons)
			if err != nil {
				return err
			}
			if tag.RowsAffected() == 1 {
				s.ID, s.EntryID, s.Status = id, e.ID, "open"
				fresh = append(fresh, s)
				if i == 0 {
					top = id
				}
			}
		}
		_, err := tx.Exec(ctx, `INSERT INTO money_match_state (entry_id, tenant_id, state, considered_at) VALUES ($1, $2, 'suggested', $3)
			ON CONFLICT (entry_id) DO UPDATE SET state = 'suggested', considered_at = $3`, e.ID, tenant, m.now())
		return err
	})
	if err != nil {
		return err
	}
	clear := len(found) == 1 || found[0].Score-found[1].Score >= Margin
	if auto && top != uuid.Nil && found[0].Score >= Auto && clear {
		_, err := m.Confirm(ctx, tenant, top, "system:matching", true)
		if err == nil {
			return nil
		}
		if !errors.Is(err, invoicing.ErrState) {
			return err
		}
	}
	if m.Suggested != nil && len(fresh) > 0 {
		m.Suggested(ctx, tenant, e.ID, fresh)
	}
	return nil
}

func (m *Matcher) mark(ctx context.Context, tenant, entryID uuid.UUID, state string) error {
	return m.hold(ctx, tenant, entryID, state, "")
}

// hold marks an entry, with why it waits for a person when it does.
func (m *Matcher) hold(ctx context.Context, tenant, entryID uuid.UUID, state, reason string) error {
	return db.InTenantTx(ctx, m.Invoices.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO money_match_state (entry_id, tenant_id, state, considered_at, held_reason) VALUES ($1, $2, $3, $4, NULLIF($5, ''))
			ON CONFLICT (entry_id) DO UPDATE SET state = $3, considered_at = $4, held_reason = NULLIF($5, '')`, entryID, tenant, state, m.now(), reason)
		return err
	})
}

// Kinds of what a payment counts towards.
const (
	TargetInvoice = "invoice"
	TargetOrder   = "order"
)

// Suggestion is an open invoice or order a payment may be for, and why.
type Suggestion struct {
	ID      uuid.UUID `json:"id,omitempty"`
	EntryID uuid.UUID `json:"entry_id,omitempty"`
	// TargetKind is invoice or order; Target names it as money_matches
	// does: invoice:INV-0104, order:AK-7QX4KD.
	TargetKind string     `json:"target_kind"`
	Target     string     `json:"target"`
	InvoiceID  *uuid.UUID `json:"invoice_id"`
	OrderID    *uuid.UUID `json:"order_id,omitempty"`
	// Number is the invoice's number or the order's reference.
	Number  string   `json:"number"`
	Score   int      `json:"score"`
	Reasons []string `json:"reasons"`
	Status  string   `json:"status,omitempty"`
}

// candidate is an open invoice or order as scoring sees it.
type candidate struct {
	invoice, order *uuid.UUID
	number         string
	due            int64
	part           bool
	customer       string
	since          *time.Time // when it was sent (invoice) or placed (order)
	verb           string     // sent or placed
	remembered     string     // the payer paid for this customer before: by "account" or "name"
}

// Weights of a remembered payer (docs/matching.md#payer-memory): the
// paying account is surer than the name.
const (
	RememberedAccount = 75
	RememberedName    = 70
)

// Score rates an open invoice for a payment of amount from payer with
// reference, received at when; ok is false below Suggest, for a payment
// before the invoice was sent, or for an invoice with nothing due.
func Score(amount int64, payer, reference string, when time.Time, x invoicing.Invoice) (Suggestion, bool) {
	return ScoreRemembered(amount, payer, reference, when, x, "")
}

// ScoreRemembered is Score for a payer remembered as having paid for the
// invoice's customer before: by their paying "account" or their "name"
// (or "" for not remembered).
func ScoreRemembered(amount int64, payer, reference string, when time.Time, x invoicing.Invoice, remembered string) (Suggestion, bool) {
	id := x.ID
	return score(amount, payer, reference, when, candidate{invoice: &id, number: x.Number, due: x.DueKobo, part: x.AllowPartPayments,
		customer: x.Customer.Name, since: x.SentAt, verb: "sent", remembered: remembered})
}

// ScoreOrder rates an open storefront order as Score does an invoice:
// what is due is what is paid online (the deposit, or the total), the
// customer is the order's, and the days count from when it was placed.
// An order takes no part payments.
func ScoreOrder(amount int64, payer, reference string, when time.Time, x storefront.Order) (Suggestion, bool) {
	id, placed := x.ID, x.CreatedAt
	return score(amount, payer, reference, when, candidate{order: &id, number: x.Reference, due: x.Payable(), customer: x.Customer.Name, since: &placed, verb: "placed"})
}

func score(amount int64, payer, reference string, when time.Time, x candidate) (Suggestion, bool) {
	s := Suggestion{InvoiceID: x.invoice, OrderID: x.order, Number: x.number, TargetKind: TargetInvoice}
	if x.order != nil {
		s.TargetKind = TargetOrder
	}
	s.Target = s.TargetKind + ":" + x.number
	if x.due <= 0 || x.since == nil || when.Before(x.since.Add(-time.Hour)) {
		return s, false
	}
	switch {
	case amount == x.due:
		s.Score += 50
		s.Reasons = append(s.Reasons, "same amount as what is due on "+x.number+" ("+invoicing.Naira(amount)+")")
	case x.part && amount < x.due:
		s.Score += 15
		s.Reasons = append(s.Reasons, "part of what is due on "+x.number+", which takes part payments")
	}
	if mentions(reference, x.number) {
		s.Score += 40
		s.Reasons = append(s.Reasons, "the reference mentions "+x.number)
	}
	switch n := nameMatch(payer, x.customer); n {
	case 2:
		s.Score += 35
		s.Reasons = append(s.Reasons, "the payer's name matches the customer's")
	case 1:
		s.Score += 20
		s.Reasons = append(s.Reasons, "the payer shares a name with the customer")
	}
	switch x.remembered {
	case "account":
		s.Score += RememberedAccount
		s.Reasons = append(s.Reasons, "this paying account paid for this customer before")
	case "name":
		s.Score += RememberedName
		s.Reasons = append(s.Reasons, "this payer paid for this customer before")
	}
	if s.Score == 0 {
		return s, false
	}
	days := int(when.Sub(*x.since).Hours() / 24)
	switch {
	case days <= 0:
		s.Score += 15
		s.Reasons = append(s.Reasons, "the day "+x.number+" was "+x.verb)
	case days <= 3:
		s.Score += 15
		s.Reasons = append(s.Reasons, fmt.Sprintf("%s after %s was %s", plural(days, "day"), x.number, x.verb))
	case days <= 14:
		s.Score += 10
		s.Reasons = append(s.Reasons, fmt.Sprintf("%s after %s was %s", plural(days, "day"), x.number, x.verb))
	case days <= 60:
		s.Score += 5
		s.Reasons = append(s.Reasons, fmt.Sprintf("%s after %s was %s", plural(days, "day"), x.number, x.verb))
	}
	s.Score = min(s.Score, 100)
	return s, s.Score >= Suggest
}

func plural(n int, word string) string {
	if n == 1 {
		return "1 " + word
	}
	return fmt.Sprintf("%d %ss", n, word)
}

// Reasons never quote the payer or the customer: they are stored and
// shown as they are, and names are personal data.

// mentions reports whether a reference names an invoice number, ignoring
// case and punctuation: "inv0104", "INV-0104", "Payment INV/0104".
func mentions(reference, number string) bool {
	squash := func(s string) string {
		return strings.Map(func(r rune) rune {
			if unicode.IsLetter(r) || unicode.IsDigit(r) {
				return unicode.ToLower(r)
			}
			return -1
		}, s)
	}
	n := squash(number)
	return len(n) >= 4 && strings.Contains(squash(reference), n)
}

// nameMatch is 2 when every word of the customer's name is in the
// payer's (in any order: banks write "OBI ADA C"), 1 when they share a
// word of three letters or more, else 0.
func nameMatch(payer, customer string) int {
	words := func(s string) []string {
		return strings.FieldsFunc(strings.ToLower(s), func(r rune) bool { return !unicode.IsLetter(r) })
	}
	p := map[string]bool{}
	for _, w := range words(payer) {
		p[w] = true
	}
	c := words(customer)
	if len(p) == 0 || len(c) == 0 {
		return 0
	}
	all, some := true, false
	for _, w := range c {
		if p[w] {
			some = some || len(w) >= 3
		} else {
			all = false
		}
	}
	switch {
	case all:
		return 2
	case some:
		return 1
	}
	return 0
}
