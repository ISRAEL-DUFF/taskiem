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

// SentRefPrefix starts the reference of a customer of a sent invoice who
// is not in the book yet (BookCustomer.Ref): the name lookup offers them,
// and choosing one makes their record (AdoptCustomer).
const SentRefPrefix = "sent:"

// sentLookLimit caps the customers of sent invoices whose names one lookup
// opens, the most recently sent first.
const sentLookLimit = 200

// BookCustomer is a customer of the debtor book, opened; or, offered by the
// name lookup, a customer of a sent invoice not in the book yet (no ID and
// no Key).
type BookCustomer struct {
	ID uuid.UUID `json:"id"`
	// Key is the record's own, book:<id>, by which the chat names it.
	Key string `json:"key"`
	// CustomerID is who they are as their invoices group
	// (Invoice.CustomerID): "key:book:<id>" for a customer recorded with
	// no phone, whose lines carry that key; else the personal-data
	// subject of their phone, as an invoice sent to it is, and their lines
	// carry no key.
	CustomerID string `json:"customer_id"`
	Name       string `json:"name"`
	Phone      string `json:"phone,omitempty"`
	Email      string `json:"email,omitempty"`

	subject string
}

// Ref names the customer in a chat's pick: their record's Key, or
// sent:<customer id> for a customer of a sent invoice not in the book.
func (c BookCustomer) Ref() string {
	if c.Key != "" {
		return c.Key
	}
	return SentRefPrefix + c.CustomerID
}

// lineKey is the key a line recorded for them carries: the record's own
// when that is who they are, else none (they are their phone's).
func (c BookCustomer) lineKey() string {
	if c.Key != "" && c.CustomerID == "key:"+c.Key {
		return c.Key
	}
	return ""
}

// SearchForm is a name as the lookup keeps and compares it: trimmed, its
// spaces collapsed, lower case, without accents or tone marks (lang.Fold),
// so "ade" finds Adé.
func SearchForm(name string) string {
	return lang.Fold(name)
}

// NewBookCustomer records a customer of the book. With no phone, the name
// is sealed under a subject of the record's own (so two Adas are two
// people, erased apart) and the record's key is who they are. With a
// phone, they are that phone's customer, as an invoice sent to it is: the
// name and phone are sealed under the phone's subject, and a phone already
// in the book returns its record as it is.
func (s *Invoices) NewBookCustomer(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, name, phone, by string) (BookCustomer, error) {
	name = strings.Join(strings.Fields(name), " ")
	if name == "" || len(name) > 120 {
		return BookCustomer{}, invalid("the customer's name is required (up to 120 characters)")
	}
	if phone != "" {
		env, err := s.Vault.SealTx(ctx, tx, tenant, "phone", phone)
		if err != nil {
			return BookCustomer{}, err
		}
		subject, _ := env["subject"].(string)
		return s.bookRecord(ctx, tx, tenant, uuid.New(), subject, subject, name, phone, by, map[string]any{"phone": true})
	}
	id := uuid.New()
	key := BookKeyPrefix + id.String()
	// The subject is the record's own: sealing its key makes it.
	env, err := s.Vault.SealTx(ctx, tx, tenant, "customer_book", key)
	if err != nil {
		return BookCustomer{}, err
	}
	subject, _ := env["subject"].(string)
	return s.bookRecord(ctx, tx, tenant, id, subject, "key:"+key, name, "", by, map[string]any{"phone": false})
}

// bookRecord writes a customer's record, its name and phone sealed under
// subject; a record already kept for customerID (a phone's) is returned
// instead, unchanged.
func (s *Invoices) bookRecord(ctx context.Context, tx pgx.Tx, tenant, id uuid.UUID, subject, customerID, name, phone, by string, detail map[string]any) (BookCustomer, error) {
	if c, err := s.bookByCustomer(ctx, tx, tenant, customerID); err == nil || !errors.Is(err, ErrNotFound) {
		return c, err
	}
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
	tag, err := tx.Exec(ctx, `INSERT INTO invoice_customers (id, tenant_id, subject_id, customer_id, name, search, phone, created_by) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		ON CONFLICT (tenant_id, customer_id) DO NOTHING`, id, tenant, subject, customerID, nameRaw, SearchForm(name), phoneRaw, by)
	if err != nil {
		return BookCustomer{}, err
	}
	if tag.RowsAffected() == 0 {
		return s.bookByCustomer(ctx, tx, tenant, customerID)
	}
	key := BookKeyPrefix + id.String()
	if err := audit(ctx, tx, tenant, by, "invoice.customer.created", key, detail); err != nil {
		return BookCustomer{}, err
	}
	return BookCustomer{ID: id, Key: key, CustomerID: customerID, Name: name, Phone: phone, subject: subject}, nil
}

const bookCols = `id, customer_id, subject_id, name, phone, email`

// openSealed opens a sealed value kept as JSON: "" when there is none or
// its subject was erased.
func (s *Invoices) openSealed(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, raw []byte) (any, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	var env map[string]any
	if err := json.Unmarshal(raw, &env); err != nil {
		return nil, err
	}
	v, err := s.Vault.OpenTx(ctx, tx, tenant, env)
	if errors.Is(err, pii.ErrErased) {
		return nil, nil
	}
	return v, err
}

func (s *Invoices) scanBook(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, r pgx.Row) (BookCustomer, error) {
	var c BookCustomer
	var name, phone, email []byte
	if err := r.Scan(&c.ID, &c.CustomerID, &c.subject, &name, &phone, &email); err != nil {
		return c, err
	}
	c.Key = BookKeyPrefix + c.ID.String()
	open := func(raw []byte) (string, error) {
		v, err := s.openSealed(ctx, tx, tenant, raw)
		str, _ := v.(string)
		return str, err
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

func (s *Invoices) bookByCustomer(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, customerID string) (BookCustomer, error) {
	c, err := s.scanBook(ctx, tx, tenant, tx.QueryRow(ctx, `SELECT `+bookCols+` FROM invoice_customers WHERE tenant_id = $1 AND customer_id = $2`, tenant, customerID))
	if errors.Is(err, pgx.ErrNoRows) {
		return BookCustomer{}, ErrNotFound
	}
	return c, err
}

// BookCustomerByRef reads the customer a chat's pick names (Ref): a record
// of the book, or a customer of a sent invoice, as kept in the book when
// they are by now.
func (s *Invoices) BookCustomerByRef(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, ref string) (BookCustomer, error) {
	customer, ok := strings.CutPrefix(ref, SentRefPrefix)
	if !ok {
		return s.BookCustomerByKey(ctx, tx, tenant, ref)
	}
	if c, err := s.bookByCustomer(ctx, tx, tenant, customer); err == nil || !errors.Is(err, ErrNotFound) {
		return c, err
	}
	sent, err := s.sentCustomers(ctx, tx, tenant, customer)
	if err != nil {
		return BookCustomer{}, err
	}
	if len(sent) == 0 {
		return BookCustomer{}, ErrNotFound
	}
	return sent[0], nil
}

// AdoptCustomer puts a customer of a sent invoice in the book, chosen from
// a name lookup: a record under their phone's subject, with the name and
// phone of the invoice last sent to them, so lines recorded for them join
// the invoices sent to them as one balance. A record kept already is
// returned as it is.
func (s *Invoices) AdoptCustomer(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, customerID, by string) (BookCustomer, error) {
	if c, err := s.bookByCustomer(ctx, tx, tenant, customerID); err == nil || !errors.Is(err, ErrNotFound) {
		return c, err
	}
	sent, err := s.sentCustomers(ctx, tx, tenant, customerID)
	if err != nil {
		return BookCustomer{}, err
	}
	if len(sent) == 0 {
		return BookCustomer{}, ErrNotFound
	}
	c := sent[0]
	return s.bookRecord(ctx, tx, tenant, uuid.New(), customerID, customerID, c.Name, c.Phone, by, map[string]any{"phone": true, "from": "invoice"})
}

// sentCustomers opens the customers of sent invoices that the book does
// not keep: grouped by their phone (no key), each with the name and phone
// of the invoice last sent to them, the most recently sent first, at most
// sentLookLimit; only the one customer given, when one is.
func (s *Invoices) sentCustomers(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, only string) ([]BookCustomer, error) {
	rows, err := tx.Query(ctx, `SELECT customer_id, subject_id, customer FROM (
		SELECT DISTINCT ON (i.customer_id) i.customer_id, i.subject_id, i.customer, i.sent_at FROM customer_invoices i
		WHERE i.tenant_id = $1 AND i.customer_key IS NULL AND i.sent_at IS NOT NULL AND i.customer_id LIKE 'phone:%' AND ($2 = '' OR i.customer_id = $2)
		  AND NOT EXISTS (SELECT 1 FROM invoice_customers b WHERE b.tenant_id = i.tenant_id AND b.customer_id = i.customer_id)
		ORDER BY i.customer_id, i.sent_at DESC, i.id) last
		ORDER BY sent_at DESC, customer_id LIMIT $3`, tenant, only, sentLookLimit)
	if err != nil {
		return nil, err
	}
	type sealed struct {
		customer, subject string
		env               []byte
	}
	got, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (sealed, error) {
		var x sealed
		err := r.Scan(&x.customer, &x.subject, &x.env)
		return x, err
	})
	if err != nil {
		return nil, err
	}
	out := make([]BookCustomer, 0, len(got))
	for _, x := range got {
		v, err := s.openSealed(ctx, tx, tenant, x.env)
		if err != nil {
			return nil, err
		}
		raw, _ := json.Marshal(v)
		var cust Customer
		if v == nil || json.Unmarshal(raw, &cust) != nil || cust.Phone == "" || strings.TrimSpace(cust.Name) == "" {
			continue
		}
		out = append(out, BookCustomer{CustomerID: x.customer, Name: strings.Join(strings.Fields(cust.Name), " "), Phone: cust.Phone, Email: cust.Email, subject: x.subject})
	}
	return out, nil
}

// FindBookCustomers finds customers by name: those whose name is the
// words, else those whose name starts with them, else those whose name
// holds them; at most nine. The book's own come first, oldest first, then
// customers of sent invoices it does not keep (each grouped by their
// phone), the most recently sent first: their names are opened to be
// compared, never kept in a searchable form.
func (s *Invoices) FindBookCustomers(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, words string) ([]BookCustomer, error) {
	q := SearchForm(words)
	if q == "" {
		return nil, nil
	}
	sent, err := s.sentCustomers(ctx, tx, tenant, "")
	if err != nil {
		return nil, err
	}
	like := strings.NewReplacer(`\`, `\\`, "%", `\%`, "_", `\_`).Replace(q)
	for _, cond := range []struct {
		where string
		arg   string
		match func(string) bool
	}{
		{"search = $2", q, func(n string) bool { return n == q }},
		{`search LIKE $2 || '%'`, like, func(n string) bool { return strings.HasPrefix(n, q) }},
		{`search LIKE '%' || $2 || '%'`, like, func(n string) bool { return strings.Contains(n, q) }},
	} {
		rows, err := tx.Query(ctx, `SELECT id FROM invoice_customers WHERE tenant_id = $1 AND `+cond.where+` ORDER BY created_at, id LIMIT 9`, tenant, cond.arg)
		if err != nil {
			return nil, err
		}
		ids, err := pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
		if err != nil {
			return nil, err
		}
		var out []BookCustomer
		for _, id := range ids {
			c, err := s.scanBook(ctx, tx, tenant, tx.QueryRow(ctx, `SELECT `+bookCols+` FROM invoice_customers WHERE tenant_id = $1 AND id = $2`, tenant, id))
			if err != nil {
				return nil, err
			}
			out = append(out, c)
		}
		for _, c := range sent {
			if len(out) < 9 && cond.match(SearchForm(c.Name)) {
				out = append(out, c)
			}
		}
		if len(out) > 0 {
			return out, nil
		}
	}
	return nil, nil
}

// SetBookPhone gives a customer of the book recorded with no phone their
// phone number, on their record and on their lines not yet sent, so
// receipts and reminders can reach them. They stay who they were (their
// record's key): invoices sent to that number before stay apart.
func (s *Invoices) SetBookPhone(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, c BookCustomer, phone, by string) (BookCustomer, error) {
	if c.lineKey() == "" {
		return c, invalid("%s has a phone number in the book already", c.Name)
	}
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
	cust := Customer{Name: c.Name, Phone: c.Phone, Email: c.Email, Key: c.lineKey()}
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
		id, tenant, number, custRaw, c.subject, linesRaw, total, nullable(due), nst.Connection, nst.FeePaidBy, by, nullable(key), nullable(cust.Key)); err != nil {
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
