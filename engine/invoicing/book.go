package invoicing

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/lang"
	"github.com/israel-duff/taskiem/engine/pii"
)

// The debtor book (decision 0060; docs/spec/no-sheet-needed.md §6.4,
// R5): money owed is kept as invoices. A notebook debt ("Ada owes 5,000")
// is an invoice of kind credit, recorded: open and owed wherever an
// invoice is (matching, attribution oldest first, credit, the list and the
// statement count it) but never sent until the business sends it, and
// always taking part payments. Its customer may have no phone or email
// yet: a customer of the book is a record of their own (invoice_customers)
// under a key, book:<id>, that every line of theirs carries, so payments
// and credit group by it as a school's student:42 does.
//
// The customer's name is sealed under a personal-data subject of their
// own, beside a lower-cased search form of it (SearchForm) that the name
// lookup reads: a recorded exception to the personal-data rules (Q8),
// erased with the customer.

// BookKeyPrefix starts the key of a customer of the debtor book.
const BookKeyPrefix = "book:"

// BookCustomer is a customer of the debtor book, opened.
type BookCustomer struct {
	ID    uuid.UUID `json:"id"`
	Key   string    `json:"key"`
	Name  string    `json:"name"`
	Phone string    `json:"phone,omitempty"`
	Email string    `json:"email,omitempty"`

	subject string
}

// SearchForm is a name as the lookup keeps and compares it: trimmed, its
// spaces collapsed, lower case, without accents or tone marks (lang.Fold),
// so "ade" finds Adé.
func SearchForm(name string) string {
	return lang.Fold(name)
}

// NewBookCustomer records a customer of the book: the name sealed under a
// subject of their own (so two Adas are two people, erased apart), the
// phone sealed under it too when given.
func (s *Invoices) NewBookCustomer(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, name, phone, by string) (BookCustomer, error) {
	name = strings.Join(strings.Fields(name), " ")
	if name == "" || len(name) > 120 {
		return BookCustomer{}, invalid("the customer's name is required (up to 120 characters)")
	}
	id := uuid.New()
	key := BookKeyPrefix + id.String()
	// The subject is the record's own: sealing its key makes it.
	env, err := s.Vault.SealTx(ctx, tx, tenant, "customer_book", key)
	if err != nil {
		return BookCustomer{}, err
	}
	subject, _ := env["subject"].(string)
	nameEnv, err := s.Vault.SealUnderTx(ctx, tx, tenant, subject, "name", name)
	if err != nil {
		return BookCustomer{}, err
	}
	var phoneRaw []byte
	if phone != "" {
		pe, err := s.Vault.SealUnderTx(ctx, tx, tenant, subject, "phone", phone)
		if err != nil {
			return BookCustomer{}, err
		}
		phoneRaw, _ = json.Marshal(pe)
	}
	nameRaw, _ := json.Marshal(nameEnv)
	if _, err := tx.Exec(ctx, `INSERT INTO invoice_customers (id, tenant_id, subject_id, name, search, phone, created_by) VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		id, tenant, subject, nameRaw, SearchForm(name), phoneRaw, by); err != nil {
		return BookCustomer{}, err
	}
	if err := audit(ctx, tx, tenant, by, "invoice.customer.created", key, map[string]any{"phone": phone != ""}); err != nil {
		return BookCustomer{}, err
	}
	return BookCustomer{ID: id, Key: key, Name: name, Phone: phone, subject: subject}, nil
}

const bookCols = `id, subject_id, name, phone, email`

func (s *Invoices) scanBook(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, r pgx.Row) (BookCustomer, error) {
	var c BookCustomer
	var name, phone, email []byte
	if err := r.Scan(&c.ID, &c.subject, &name, &phone, &email); err != nil {
		return c, err
	}
	c.Key = BookKeyPrefix + c.ID.String()
	open := func(raw []byte) (string, error) {
		if len(raw) == 0 {
			return "", nil
		}
		var env map[string]any
		if err := json.Unmarshal(raw, &env); err != nil {
			return "", err
		}
		v, err := s.Vault.OpenTx(ctx, tx, tenant, env)
		if errors.Is(err, pii.ErrErased) {
			return "", nil
		}
		if err != nil {
			return "", err
		}
		str, _ := v.(string)
		return str, nil
	}
	var err error
	if c.Name, err = open(name); err != nil {
		return c, err
	}
	if c.Name == "" {
		c.Name = "(erased)"
	}
	if c.Phone, err = open(phone); err != nil {
		return c, err
	}
	c.Email, err = open(email)
	return c, err
}

// BookCustomerByKey reads a customer of the book by their key.
func (s *Invoices) BookCustomerByKey(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, key string) (BookCustomer, error) {
	id, err := uuid.Parse(strings.TrimPrefix(key, BookKeyPrefix))
	if err != nil || !strings.HasPrefix(key, BookKeyPrefix) {
		return BookCustomer{}, ErrNotFound
	}
	c, err := s.scanBook(ctx, tx, tenant, tx.QueryRow(ctx, `SELECT `+bookCols+` FROM invoice_customers WHERE tenant_id = $1 AND id = $2`, tenant, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return BookCustomer{}, ErrNotFound
	}
	return c, err
}

// FindBookCustomers finds customers of the book by name: those whose name
// is the words, else those whose name starts with them, else those whose
// name holds them; at most nine, oldest first.
func (s *Invoices) FindBookCustomers(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, words string) ([]BookCustomer, error) {
	q := SearchForm(words)
	if q == "" {
		return nil, nil
	}
	like := strings.NewReplacer(`\`, `\\`, "%", `\%`, "_", `\_`).Replace(q)
	for _, cond := range []struct {
		where string
		arg   string
	}{{"search = $2", q}, {`search LIKE $2 || '%'`, like}, {`search LIKE '%' || $2 || '%'`, like}} {
		rows, err := tx.Query(ctx, `SELECT id FROM invoice_customers WHERE tenant_id = $1 AND `+cond.where+` ORDER BY created_at, id LIMIT 9`, tenant, cond.arg)
		if err != nil {
			return nil, err
		}
		ids, err := pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
		if err != nil {
			return nil, err
		}
		if len(ids) == 0 {
			continue
		}
		out := make([]BookCustomer, 0, len(ids))
		for _, id := range ids {
			c, err := s.scanBook(ctx, tx, tenant, tx.QueryRow(ctx, `SELECT `+bookCols+` FROM invoice_customers WHERE tenant_id = $1 AND id = $2`, tenant, id))
			if err != nil {
				return nil, err
			}
			out = append(out, c)
		}
		return out, nil
	}
	return nil, nil
}

// SetBookPhone gives a customer of the book their phone number, on their
// record and on their lines not yet sent, so receipts and reminders can
// reach them.
func (s *Invoices) SetBookPhone(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, c BookCustomer, phone, by string) (BookCustomer, error) {
	pe, err := s.Vault.SealUnderTx(ctx, tx, tenant, c.subject, "phone", phone)
	if err != nil {
		return c, err
	}
	raw, _ := json.Marshal(pe)
	if _, err := tx.Exec(ctx, `UPDATE invoice_customers SET phone = $3, updated_at = now() WHERE tenant_id = $1 AND id = $2`, tenant, c.ID, raw); err != nil {
		return c, err
	}
	c.Phone = phone
	cust, err := s.Vault.SealUnderTx(ctx, tx, tenant, c.subject, "customer", Customer{Name: c.Name, Phone: c.Phone, Email: c.Email, Key: c.Key})
	if err != nil {
		return c, err
	}
	custRaw, _ := json.Marshal(cust)
	if _, err := tx.Exec(ctx, `UPDATE customer_invoices SET customer = $3, updated_at = now()
		WHERE tenant_id = $1 AND customer_key = $2 AND sent_at IS NULL AND status IN ('recorded', 'part_paid', 'overdue')`, tenant, c.Key, custRaw); err != nil {
		return c, err
	}
	return c, audit(ctx, tx, tenant, by, "invoice.customer.phone", c.Key, map[string]any{})
}

// CreditDraft is a notebook debt to record: an amount (one line) or lines,
// and a due date when one was given (else the credit terms').
type CreditDraft struct {
	AmountKobo  int64
	Description string
	Lines       []Line
	DueOn       string
}

// RecordCredit records a notebook debt for a customer of the book: an
// invoice of kind credit, recorded and not sent, taking part payments,
// due after the credit terms in force (or on the date given; none without
// either). Under a key (a chat message's), the same key again returns the
// line the first call made, so a retry records nothing twice.
func (s *Invoices) RecordCredit(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, key string, c BookCustomer, d CreditDraft, by string) (Invoice, error) {
	if key != "" {
		var id uuid.UUID
		err := tx.QueryRow(ctx, `SELECT id FROM customer_invoices WHERE tenant_id = $1 AND created_key = $2`, tenant, key).Scan(&id)
		if err == nil {
			return s.Get(ctx, tx, tenant, id.String())
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return Invoice{}, err
		}
	}
	if c.subject == "" || !strings.HasPrefix(c.Key, BookKeyPrefix) {
		return Invoice{}, invalid("a notebook debt is a customer of the book's")
	}
	cust := Customer{Name: c.Name, Phone: c.Phone, Email: c.Email, Key: c.Key}
	lines, total, err := check(Draft{Customer: cust, Lines: d.Lines, AmountKobo: d.AmountKobo, Description: d.Description, DueOn: d.DueOn})
	if err != nil {
		return Invoice{}, err
	}
	st, err := GetSettings(ctx, tx, tenant)
	if err != nil {
		return Invoice{}, err
	}
	due := d.DueOn
	if due == "" {
		due = CreditDue(st, s.now())
	}
	number, nst, err := nextNumber(ctx, tx, tenant)
	if err != nil {
		return Invoice{}, err
	}
	sealed, err := s.Vault.SealUnderTx(ctx, tx, tenant, c.subject, "customer", cust)
	if err != nil {
		return Invoice{}, err
	}
	custRaw, _ := json.Marshal(sealed)
	linesRaw, _ := json.Marshal(lines)
	id := uuid.New()
	if _, err := tx.Exec(ctx, `INSERT INTO customer_invoices (id, tenant_id, number, customer, subject_id, lines, total_kobo, due_on,
		connection, fee_paid_by, allow_part_payments, created_by, created_key, customer_key, kind, recorded_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8::date, $9, $10, true, $11, $12, $13, 'credit', now())`,
		id, tenant, number, custRaw, c.subject, linesRaw, total, nullable(due), nst.Connection, nst.FeePaidBy, by, nullable(key), c.Key); err != nil {
		return Invoice{}, err
	}
	if err := audit(ctx, tx, tenant, by, "invoice.recorded", number, map[string]any{"total_kobo": total, "due_on": due}); err != nil {
		return Invoice{}, err
	}
	x, err := s.Get(ctx, tx, tenant, id.String())
	if err != nil {
		return Invoice{}, err
	}
	// What the customer paid before and no line of theirs took is their
	// credit: it counts towards this one, as towards an invoice sent. A
	// line it pays in full is paid; tell its waiters with Paid once the
	// transaction commits.
	x, _, _, err = s.applyCredit(ctx, tx, tenant, x)
	return x, err
}

// CreditDue is the due date a credit line recorded on day takes from the
// terms: day plus the business's credit days, Lagos time; "" with no
// terms answered or no fixed time.
func CreditDue(st Settings, day time.Time) string {
	if st.CreditDays == nil {
		return ""
	}
	lagos, err := time.LoadLocation("Africa/Lagos")
	if err != nil {
		lagos = time.FixedZone("WAT", 3600)
	}
	return day.In(lagos).AddDate(0, 0, *st.CreditDays).Format(time.DateOnly)
}

// SetCreditTerms keeps the owner's answer to how long customers have to
// pay a notebook debt: days (1 to 365), or nil for no fixed time. Lines
// already recorded keep their due dates (ApplyCreditTerms moves them).
func SetCreditTerms(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, days *int, by string) error {
	if days != nil && (*days < 1 || *days > 365) {
		return invalid("credit terms are 1 to 365 days, or no fixed time")
	}
	if _, err := tx.Exec(ctx, `INSERT INTO customer_invoice_settings (tenant_id, credit_days, credit_terms_at) VALUES ($1, $2, now())
		ON CONFLICT (tenant_id) DO UPDATE SET credit_days = $2, credit_terms_at = now(), updated_at = now()`, tenant, days); err != nil {
		return err
	}
	detail := map[string]any{"credit_days": nil}
	if days != nil {
		detail["credit_days"] = *days
	}
	return audit(ctx, tx, tenant, by, "invoice.credit_terms", "settings", detail)
}

// OpenCreditLines counts the notebook debts not yet sent that are open.
func OpenCreditLines(ctx context.Context, tx pgx.Tx, tenant uuid.UUID) (int, error) {
	var n int
	err := tx.QueryRow(ctx, `SELECT count(*) FROM customer_invoices WHERE tenant_id = $1 AND kind = 'credit' AND sent_at IS NULL
		AND status IN ('recorded', 'part_paid', 'overdue')`, tenant).Scan(&n)
	return n, err
}

// ApplyCreditTerms dates the open notebook debts not yet sent by the terms
// in force, each from the day it was recorded (no fixed time takes their
// due dates away), as one change: how many it moved.
func ApplyCreditTerms(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, by string) (int, error) {
	st, err := GetSettings(ctx, tx, tenant)
	if err != nil {
		return 0, err
	}
	if st.CreditTermsAt == nil {
		return 0, fmt.Errorf("%w: no credit terms are set", ErrState)
	}
	tag, err := tx.Exec(ctx, `UPDATE customer_invoices SET due_on = CASE WHEN $2::int IS NULL THEN NULL
		ELSE (recorded_at AT TIME ZONE 'Africa/Lagos')::date + $2::int END, updated_at = now()
		WHERE tenant_id = $1 AND kind = 'credit' AND sent_at IS NULL AND status IN ('recorded', 'part_paid', 'overdue')`, tenant, st.CreditDays)
	if err != nil {
		return 0, err
	}
	n := int(tag.RowsAffected())
	return n, audit(ctx, tx, tenant, by, "invoice.credit_terms.applied", "settings", map[string]any{"lines": n})
}
