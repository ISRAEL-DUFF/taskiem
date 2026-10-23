// Package matching matches the money a business receives to its invoices
// (decision 0045 point 6; docs/spec/invoices-and-money-log.md 8, T5;
// docs/matching.md).
//
// Every money-in entry of the money log is looked at once. A payment into
// an account number or link Taskiem gave for an invoice is matched
// exactly, with no one asked. Any other gets up to three suggestions
// among the business's open invoices, each with its reasons; a person
// confirms or rejects one, or, when the business has turned automatic
// matching on, a strong and clear suggestion is matched on its own. A
// match is a payment of the invoice (customer_invoice_payments): undone,
// never changed.
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
	"github.com/israel-duff/taskiem/engine/pii"
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
	PII      pii.Cipher
	Logger   *slog.Logger
	Now      func() time.Time
}

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
	return nil
}

// entry is a money-in entry as matching sees it.
type entry struct {
	ID         uuid.UUID
	When       time.Time
	AmountKobo int64
	Channel    string
	Reference  string
	Payer      string
}

// Tenant looks at up to 100 of a tenant's new money-in entries.
func (m *Matcher) Tenant(ctx context.Context, tenant uuid.UUID) error {
	var list []entry
	err := db.InTenantTx(ctx, m.Invoices.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT e.id, COALESCE(e.occurred_at, e.recorded_at), COALESCE(e.amount_kobo, 0), e.channel, e.reference, e.payer
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
			return x, r.Scan(&x.e.ID, &x.e.When, &x.e.AmountKobo, &x.e.Channel, &x.e.Reference, &x.payer)
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
	if e.AmountKobo <= 0 {
		return m.mark(ctx, tenant, e.ID, "unmatched")
	}

	// Suggested: the business's open invoices, scored.
	var found []Suggestion
	var auto bool
	err = db.InTenantTx(ctx, m.Invoices.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		st, err := invoicing.GetSettings(ctx, tx, tenant)
		if err != nil {
			return err
		}
		auto = st.AutoMatch
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
			if s, ok := Score(e.AmountKobo, e.Payer, e.Reference, e.When, x); ok {
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
	err = db.InTenantTx(ctx, m.Invoices.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		for i, s := range found {
			reasons, _ := json.Marshal(s.Reasons)
			id := uuid.New()
			tag, err := tx.Exec(ctx, `INSERT INTO money_suggestions (id, tenant_id, entry_id, invoice_id, score, reasons) VALUES ($1, $2, $3, $4, $5, $6)
				ON CONFLICT (entry_id, invoice_id) DO NOTHING`, id, tenant, e.ID, s.InvoiceID, s.Score, reasons)
			if err != nil {
				return err
			}
			if i == 0 && tag.RowsAffected() == 1 {
				top = id
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
		if _, err := m.Confirm(ctx, tenant, top, "system:matching", true); err != nil && !errors.Is(err, invoicing.ErrState) {
			return err
		}
	}
	return nil
}

func (m *Matcher) mark(ctx context.Context, tenant, entryID uuid.UUID, state string) error {
	return db.InTenantTx(ctx, m.Invoices.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO money_match_state (entry_id, tenant_id, state, considered_at) VALUES ($1, $2, $3, $4)
			ON CONFLICT (entry_id) DO UPDATE SET state = $3, considered_at = $4`, entryID, tenant, state, m.now())
		return err
	})
}

// Suggestion is an open invoice a payment may be for, and why.
type Suggestion struct {
	ID        uuid.UUID `json:"id,omitempty"`
	EntryID   uuid.UUID `json:"entry_id,omitempty"`
	InvoiceID uuid.UUID `json:"invoice_id"`
	Number    string    `json:"number"`
	Score     int       `json:"score"`
	Reasons   []string  `json:"reasons"`
	Status    string    `json:"status,omitempty"`
}

// Score rates an open invoice for a payment of amount from payer with
// reference, received at when; ok is false below Suggest, for a payment
// before the invoice was sent, or for an invoice with nothing due.
func Score(amount int64, payer, reference string, when time.Time, x invoicing.Invoice) (Suggestion, bool) {
	s := Suggestion{InvoiceID: x.ID, Number: x.Number}
	if x.DueKobo <= 0 || x.SentAt == nil || when.Before(x.SentAt.Add(-time.Hour)) {
		return s, false
	}
	switch {
	case amount == x.DueKobo:
		s.Score += 50
		s.Reasons = append(s.Reasons, "same amount as what is due on "+x.Number+" ("+invoicing.Naira(amount)+")")
	case x.AllowPartPayments && amount < x.DueKobo:
		s.Score += 15
		s.Reasons = append(s.Reasons, "part of what is due on "+x.Number+", which takes part payments")
	}
	if mentions(reference, x.Number) {
		s.Score += 40
		s.Reasons = append(s.Reasons, "the reference mentions "+x.Number)
	}
	switch n := nameMatch(payer, x.Customer.Name); n {
	case 2:
		s.Score += 35
		s.Reasons = append(s.Reasons, "the payer's name matches the customer's")
	case 1:
		s.Score += 20
		s.Reasons = append(s.Reasons, "the payer shares a name with the customer")
	}
	if s.Score == 0 {
		return s, false
	}
	days := int(when.Sub(*x.SentAt).Hours() / 24)
	switch {
	case days <= 0:
		s.Score += 15
		s.Reasons = append(s.Reasons, "the day "+x.Number+" was sent")
	case days <= 3:
		s.Score += 15
		s.Reasons = append(s.Reasons, fmt.Sprintf("%s after %s was sent", plural(days, "day"), x.Number))
	case days <= 14:
		s.Score += 10
		s.Reasons = append(s.Reasons, fmt.Sprintf("%s after %s was sent", plural(days, "day"), x.Number))
	case days <= 60:
		s.Score += 5
		s.Reasons = append(s.Reasons, fmt.Sprintf("%s after %s was sent", plural(days, "day"), x.Number))
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

// Confirm makes a suggestion a match: the payment counts towards its
// invoice, the entry's other suggestions close, and a run waiting on the
// invoice is told when it becomes paid. auto records it as matched
// without asking.
func (m *Matcher) Confirm(ctx context.Context, tenant, suggestion uuid.UUID, by string, auto bool) (invoicing.Invoice, error) {
	var x invoicing.Invoice
	var became bool
	err := db.InTenantTx(ctx, m.Invoices.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		var invoice, entryID uuid.UUID
		var status, channel, reference string
		var amount *int64
		var raw []byte
		err := tx.QueryRow(ctx, `SELECT s.invoice_id, s.entry_id, s.status, s.reasons, e.amount_kobo, e.channel, e.reference
			FROM money_suggestions s JOIN money_entries e ON e.id = s.entry_id WHERE s.tenant_id = $1 AND s.id = $2 FOR UPDATE OF s`, tenant, suggestion).
			Scan(&invoice, &entryID, &status, &raw, &amount, &channel, &reference)
		if errors.Is(err, pgx.ErrNoRows) {
			return invoicing.ErrNotFound
		}
		if err != nil {
			return err
		}
		if status != "open" {
			return fmt.Errorf("%w: this suggestion was %s", invoicing.ErrState, status)
		}
		var reasons []string
		_ = json.Unmarshal(raw, &reasons)
		how := "suggested"
		if auto {
			how = "suggested_auto"
		}
		if amount == nil || *amount <= 0 {
			return fmt.Errorf("%w: the payment has no amount", invoicing.ErrState)
		}
		x, became, err = m.Invoices.RecordPayment(ctx, tx, tenant, invoice.String(), invoicing.PaymentIn{AmountKobo: *amount, PaymentID: reference,
			EntryID: &entryID, Connection: channel, How: how, By: by, Reasons: reasons})
		if err != nil {
			return err
		}
		var pay uuid.UUID
		if err := tx.QueryRow(ctx, `SELECT id FROM customer_invoice_payments WHERE tenant_id = $1 AND entry_id = $2 AND invoice_id = $3 AND undoes IS NULL
			ORDER BY created_at DESC LIMIT 1`, tenant, entryID, invoice).Scan(&pay); err != nil {
			return err
		}
		now := m.now()
		if _, err := tx.Exec(ctx, `UPDATE money_suggestions SET status = 'confirmed', decided_by = $3, decided_at = $4, payment_id = $5 WHERE tenant_id = $1 AND id = $2`,
			tenant, suggestion, by, now, pay); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE money_suggestions SET status = 'closed', decided_at = $3 WHERE tenant_id = $1 AND entry_id = $2 AND status = 'open'`,
			tenant, entryID, now); err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `INSERT INTO money_match_state (entry_id, tenant_id, state, considered_at) VALUES ($1, $2, 'matched', $3)
			ON CONFLICT (entry_id) DO UPDATE SET state = 'matched', considered_at = $3`, entryID, tenant, now)
		return err
	})
	if err != nil || !became {
		return x, err
	}
	return x, m.Invoices.Paid(ctx, tenant, x)
}

// Reject turns a suggestion down: it is not offered again.
func (m *Matcher) Reject(ctx context.Context, tenant, suggestion uuid.UUID, by string) error {
	return db.InTenantTx(ctx, m.Invoices.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		var entryID uuid.UUID
		var status string
		err := tx.QueryRow(ctx, `SELECT entry_id, status FROM money_suggestions WHERE tenant_id = $1 AND id = $2 FOR UPDATE`, tenant, suggestion).Scan(&entryID, &status)
		if errors.Is(err, pgx.ErrNoRows) {
			return invoicing.ErrNotFound
		}
		if err != nil {
			return err
		}
		if status != "open" {
			return fmt.Errorf("%w: this suggestion was %s", invoicing.ErrState, status)
		}
		now := m.now()
		if _, err := tx.Exec(ctx, `UPDATE money_suggestions SET status = 'rejected', decided_by = $3, decided_at = $4 WHERE tenant_id = $1 AND id = $2`,
			tenant, suggestion, by, now); err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `UPDATE money_match_state SET state = 'unmatched', considered_at = $3 WHERE tenant_id = $1 AND entry_id = $2
			AND NOT EXISTS (SELECT 1 FROM money_suggestions s WHERE s.entry_id = $2 AND s.status = 'open')`, tenant, entryID, now)
		return err
	})
}

// Open lists the suggestions waiting for a person, newest payment first,
// with the payment's details.
func Open(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, limit int) ([]Waiting, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := tx.Query(ctx, `SELECT s.id, s.entry_id, s.invoice_id, i.number, s.score, s.reasons, e.amount_kobo, e.channel, e.reference,
			COALESCE(e.occurred_at, e.recorded_at), e.kind
		FROM money_suggestions s JOIN money_entries e ON e.id = s.entry_id JOIN customer_invoices i ON i.id = s.invoice_id
		WHERE s.tenant_id = $1 AND s.status = 'open' ORDER BY e.recorded_at DESC, s.score DESC LIMIT $2`, tenant, limit)
	if err != nil {
		return nil, err
	}
	out, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (Waiting, error) {
		var w Waiting
		var raw []byte
		err := r.Scan(&w.ID, &w.EntryID, &w.InvoiceID, &w.Number, &w.Score, &raw, &w.AmountKobo, &w.Channel, &w.Reference, &w.When, &w.Kind)
		_ = json.Unmarshal(raw, &w.Reasons)
		w.Status = "open"
		return w, err
	})
	if out == nil {
		out = []Waiting{}
	}
	return out, err
}

// Waiting is an open suggestion with the payment it is for.
type Waiting struct {
	Suggestion
	AmountKobo *int64    `json:"amount_kobo"`
	Channel    string    `json:"channel"`
	Reference  string    `json:"reference"`
	When       time.Time `json:"received_at"`
	Kind       string    `json:"kind"`
}

// Match counts a money-in entry towards an invoice a person chose (how
// manual), closing the entry's open suggestions.
func (m *Matcher) Match(ctx context.Context, tenant, entryID uuid.UUID, invoice, by string) (invoicing.Invoice, error) {
	var x invoicing.Invoice
	var became bool
	err := db.InTenantTx(ctx, m.Invoices.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		var direction, status, channel, reference string
		var amount *int64
		err := tx.QueryRow(ctx, `SELECT direction, status, amount_kobo, channel, reference FROM money_entries WHERE tenant_id = $1 AND id = $2`, tenant, entryID).
			Scan(&direction, &status, &amount, &channel, &reference)
		if errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("%w: no such payment", invoicing.ErrNotFound)
		}
		if err != nil {
			return err
		}
		if direction != "in" || status != "settled" || amount == nil || *amount <= 0 {
			return fmt.Errorf("%w: only money received and settled can be matched to an invoice", invoicing.ErrState)
		}
		x, became, err = m.Invoices.RecordPayment(ctx, tx, tenant, invoice, invoicing.PaymentIn{AmountKobo: *amount, PaymentID: reference, EntryID: &entryID,
			Connection: channel, How: "manual", By: by, Reasons: []string{"matched by hand"}})
		if err != nil {
			return err
		}
		now := m.now()
		if _, err := tx.Exec(ctx, `UPDATE money_suggestions SET status = 'closed', decided_at = $3 WHERE tenant_id = $1 AND entry_id = $2 AND status = 'open'`,
			tenant, entryID, now); err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `INSERT INTO money_match_state (entry_id, tenant_id, state, considered_at) VALUES ($1, $2, 'matched', $3)
			ON CONFLICT (entry_id) DO UPDATE SET state = 'matched', considered_at = $3`, entryID, tenant, now)
		return err
	})
	if err != nil || !became {
		return x, err
	}
	return x, m.Invoices.Paid(ctx, tenant, x)
}
