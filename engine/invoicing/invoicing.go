// Package invoicing is the invoices a business sends its customers
// (decision 0045; docs/spec/invoices-and-money-log.md 5; docs/invoices.md).
// They are not Taskiem's own billing invoices (engine/billing).
//
// An invoice is a platform record like a storefront order. Its status and
// what was paid are computed by the database from when it was sent or
// voided, its due date and its payments (migration 00197): nothing here,
// in the API or in a workflow can mark an invoice paid except a payment
// recorded against it, and a recorded payment is never changed, only
// undone by another row.
package invoicing

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/mail"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/pii"
	"github.com/israel-duff/taskiem/engine/secrets"
	"github.com/israel-duff/taskiem/engine/sender"
)

// PaidSignal is the signal a run waiting on an invoice receives when it is
// paid in full, correlated by the invoice's number.
const PaidSignal = "invoice.paid"

// Limits on what one invoice holds.
const (
	MaxLines     = 50
	MaxTotalKobo = 100_000_000_000 // ₦1bn
)

var (
	// ErrNotFound is an invoice the caller's tenant does not have.
	ErrNotFound = errors.New("invoicing: no such invoice")
	// ErrInvalid is an invoice or request that cannot be accepted; the
	// message says why.
	ErrInvalid = errors.New("invoicing: invalid")
	// ErrState refuses what the invoice's status does not allow.
	ErrState = errors.New("invoicing: not allowed in the invoice's status")
)

// Customer is who an invoice is for. Sealed at rest.
type Customer struct {
	Name  string `json:"name"`
	Phone string `json:"phone,omitempty"`
	Email string `json:"email,omitempty"`
}

// Line is one item: quantity times unit price.
type Line struct {
	Description string `json:"description"`
	Quantity    int64  `json:"quantity"`
	UnitKobo    int64  `json:"unit_kobo"`
}

// AmountKobo is the line's total.
func (l Line) AmountKobo() int64 { return l.Quantity * l.UnitKobo }

// Draft is what a person or workflow gives to create or change an invoice
// before it is sent. Either Lines or AmountKobo (a single line).
type Draft struct {
	Customer          Customer `json:"customer"`
	Lines             []Line   `json:"lines,omitempty"`
	AmountKobo        int64    `json:"amount_kobo,omitempty"`
	Description       string   `json:"description,omitempty"`
	DueOn             string   `json:"due_on,omitempty"` // YYYY-MM-DD
	Note              string   `json:"note,omitempty"`
	Connection        string   `json:"connection,omitempty"`
	FeePaidBy         string   `json:"fee_paid_by,omitempty"`
	AllowPartPayments *bool    `json:"allow_part_payments,omitempty"`
}

// Invoice is an invoice as the business sees it, the customer opened.
type Invoice struct {
	ID                uuid.UUID  `json:"id"`
	Number            string     `json:"number"`
	Status            string     `json:"status"`
	Customer          Customer   `json:"customer"`
	Lines             []Line     `json:"lines"`
	TotalKobo         int64      `json:"total_kobo"`
	PaidKobo          int64      `json:"paid_kobo"`
	DueKobo           int64      `json:"due_kobo"`
	Currency          string     `json:"currency"`
	DueOn             string     `json:"due_on,omitempty"`
	Note              string     `json:"note,omitempty"`
	Connection        string     `json:"connection"`
	FeePaidBy         string     `json:"fee_paid_by"`
	AllowPartPayments bool       `json:"allow_part_payments"`
	SentAt            *time.Time `json:"sent_at"`
	PaidAt            *time.Time `json:"paid_at"`
	VoidedAt          *time.Time `json:"voided_at"`
	VoidReason        string     `json:"void_reason,omitempty"`
	CreatedBy         string     `json:"created_by"`
	CreatedAt         time.Time  `json:"created_at"`
	Payments          []Payment  `json:"payments,omitempty"`

	customerEnv map[string]any
	tokenEnv    map[string]any
	subject     string
}

// Payment is one payment counted towards an invoice, or the undoing of one.
type Payment struct {
	ID         uuid.UUID  `json:"id"`
	AmountKobo int64      `json:"amount_kobo"`
	PaymentID  string     `json:"payment_id"`
	Connection string     `json:"connection,omitempty"`
	How        string     `json:"how"`
	By         string     `json:"by"`
	Reasons    []string   `json:"reasons"`
	Undoes     *uuid.UUID `json:"undoes,omitempty"`
	UndoneBy   *uuid.UUID `json:"undone_by,omitempty"`
	CreatedAt  time.Time  `json:"created_at"`
}

// Settings are a business's defaults for its invoices.
type Settings struct {
	Prefix            string `json:"prefix"`
	NextNumber        int    `json:"next_number"`
	Connection        string `json:"connection"`
	FeePaidBy         string `json:"fee_paid_by"`
	AllowPartPayments bool   `json:"allow_part_payments"`
}

// Invoices runs a business's invoices.
type Invoices struct {
	Pool     *pgxpool.Pool
	Vault    *secrets.Vault
	Messages *sender.Queue
	// PageBase is where invoice pages are served (the form host's URL);
	// empty sends no link.
	PageBase string
	// Signal delivers a signal (runtime.Store.DeliverSignalOnce).
	Signal func(ctx context.Context, tenant uuid.UUID, env, event, correlation, dedupKey string, payload any) ([]uuid.UUID, bool, error)
	Logger *slog.Logger
	Now    func() time.Time
}

func (s *Invoices) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func (s *Invoices) log() *slog.Logger {
	if s.Logger != nil {
		return s.Logger
	}
	return slog.Default()
}

func invalid(format string, a ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalid, fmt.Sprintf(format, a...))
}

// --- settings ---

// GetSettings reads the business's invoice settings, the defaults when it
// has none.
func GetSettings(ctx context.Context, tx pgx.Tx, tenant uuid.UUID) (Settings, error) {
	st := Settings{Prefix: "INV-", NextNumber: 1, Connection: "main", FeePaidBy: "business"}
	err := tx.QueryRow(ctx, `SELECT prefix, next_number, connection, fee_paid_by, allow_part_payments FROM customer_invoice_settings WHERE tenant_id = $1`, tenant).
		Scan(&st.Prefix, &st.NextNumber, &st.Connection, &st.FeePaidBy, &st.AllowPartPayments)
	if errors.Is(err, pgx.ErrNoRows) {
		return st, nil
	}
	return st, err
}

// SaveSettings sets the defaults. The next number may only move forward,
// so no number is ever given twice.
func SaveSettings(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, st Settings) (Settings, error) {
	cur, err := GetSettings(ctx, tx, tenant)
	if err != nil {
		return Settings{}, err
	}
	if !validPrefix(st.Prefix) {
		return Settings{}, invalid("the prefix may have up to 12 letters, digits, '-', '_' or '/'")
	}
	if st.NextNumber == 0 {
		st.NextNumber = cur.NextNumber
	}
	if st.NextNumber < cur.NextNumber {
		return Settings{}, invalid("the next number cannot go back below %d", cur.NextNumber)
	}
	if st.Connection == "" {
		st.Connection = "main"
	}
	if st.FeePaidBy != "business" && st.FeePaidBy != "customer" {
		return Settings{}, invalid("fee_paid_by is business or customer")
	}
	_, err = tx.Exec(ctx, `INSERT INTO customer_invoice_settings (tenant_id, prefix, next_number, connection, fee_paid_by, allow_part_payments)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (tenant_id) DO UPDATE SET prefix = $2, next_number = $3, connection = $4, fee_paid_by = $5, allow_part_payments = $6, updated_at = now()`,
		tenant, st.Prefix, st.NextNumber, st.Connection, st.FeePaidBy, st.AllowPartPayments)
	return st, err
}

func validPrefix(p string) bool {
	if len(p) > 12 {
		return false
	}
	for _, r := range p {
		letter := r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z'
		digit := r >= '0' && r <= '9'
		if !letter && !digit && r != '-' && r != '_' && r != '/' {
			return false
		}
	}
	return true
}

// nextNumber takes the business's next invoice number, in the caller's
// transaction: the settings row is locked until it commits.
func nextNumber(ctx context.Context, tx pgx.Tx, tenant uuid.UUID) (string, Settings, error) {
	if _, err := tx.Exec(ctx, `INSERT INTO customer_invoice_settings (tenant_id) VALUES ($1) ON CONFLICT (tenant_id) DO NOTHING`, tenant); err != nil {
		return "", Settings{}, err
	}
	var st Settings
	err := tx.QueryRow(ctx, `UPDATE customer_invoice_settings SET next_number = next_number + 1 WHERE tenant_id = $1
		RETURNING prefix, next_number - 1, connection, fee_paid_by, allow_part_payments`, tenant).
		Scan(&st.Prefix, &st.NextNumber, &st.Connection, &st.FeePaidBy, &st.AllowPartPayments)
	if err != nil {
		return "", Settings{}, err
	}
	return fmt.Sprintf("%s%04d", st.Prefix, st.NextNumber), st, nil
}

// --- reading ---

const cols = `id, number, status, customer, subject_id, lines, total_kobo, paid_kobo, currency, COALESCE(to_char(due_on, 'YYYY-MM-DD'), ''),
	COALESCE(note, ''), connection, fee_paid_by, allow_part_payments, sent_at, paid_at, voided_at, COALESCE(void_reason, ''), created_by, created_at, page_token`

func scan(r pgx.Row) (Invoice, error) {
	var x Invoice
	var cust, lines, token []byte
	err := r.Scan(&x.ID, &x.Number, &x.Status, &cust, &x.subject, &lines, &x.TotalKobo, &x.PaidKobo, &x.Currency, &x.DueOn,
		&x.Note, &x.Connection, &x.FeePaidBy, &x.AllowPartPayments, &x.SentAt, &x.PaidAt, &x.VoidedAt, &x.VoidReason, &x.CreatedBy, &x.CreatedAt, &token)
	if err != nil {
		return x, err
	}
	_ = json.Unmarshal(cust, &x.customerEnv)
	_ = json.Unmarshal(lines, &x.Lines)
	if len(token) > 0 {
		_ = json.Unmarshal(token, &x.tokenEnv)
	}
	x.DueKobo = max(x.TotalKobo-x.PaidKobo, 0)
	return x, nil
}

func (s *Invoices) open(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, x *Invoice) error {
	v, err := s.Vault.OpenTx(ctx, tx, tenant, x.customerEnv)
	if errors.Is(err, pii.ErrErased) {
		x.Customer = Customer{Name: "(erased)"}
		return nil
	}
	if err != nil {
		return err
	}
	raw, _ := json.Marshal(v)
	return json.Unmarshal(raw, &x.Customer)
}

// Get reads one invoice by id or number, the customer opened and its
// payments listed.
func (s *Invoices) Get(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, idOrNumber string) (Invoice, error) {
	q := `SELECT ` + cols + ` FROM customer_invoices WHERE tenant_id = $1 AND number = $2`
	arg := any(idOrNumber)
	if id, err := uuid.Parse(idOrNumber); err == nil {
		q, arg = `SELECT `+cols+` FROM customer_invoices WHERE tenant_id = $1 AND id = $2`, id
	}
	x, err := scan(tx.QueryRow(ctx, q, tenant, arg))
	if errors.Is(err, pgx.ErrNoRows) {
		return Invoice{}, ErrNotFound
	}
	if err != nil {
		return Invoice{}, err
	}
	if err := s.open(ctx, tx, tenant, &x); err != nil {
		return Invoice{}, err
	}
	x.Payments, err = payments(ctx, tx, x.ID)
	return x, err
}

func payments(ctx context.Context, tx pgx.Tx, invoice uuid.UUID) ([]Payment, error) {
	rows, err := tx.Query(ctx, `SELECT p.id, p.amount_kobo, p.payment_id, COALESCE(p.connection, ''), p.how, p.by, p.reasons, p.undoes, u.id, p.created_at
		FROM customer_invoice_payments p LEFT JOIN customer_invoice_payments u ON u.undoes = p.id
		WHERE p.invoice_id = $1 ORDER BY p.created_at, p.id`, invoice)
	if err != nil {
		return nil, err
	}
	out, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (Payment, error) {
		var p Payment
		var reasons []byte
		err := r.Scan(&p.ID, &p.AmountKobo, &p.PaymentID, &p.Connection, &p.How, &p.By, &reasons, &p.Undoes, &p.UndoneBy, &p.CreatedAt)
		_ = json.Unmarshal(reasons, &p.Reasons)
		if p.Reasons == nil {
			p.Reasons = []string{}
		}
		return p, err
	})
	if out == nil {
		out = []Payment{}
	}
	return out, err
}

// List reads the newest invoices, optionally of one status.
func (s *Invoices) List(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, status string, limit int) ([]Invoice, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := tx.Query(ctx, `SELECT `+cols+` FROM customer_invoices WHERE tenant_id = $1 AND ($2 = '' OR status = $2)
		ORDER BY created_at DESC, id LIMIT $3`, tenant, status, limit)
	if err != nil {
		return nil, err
	}
	list, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (Invoice, error) { return scan(r) })
	if err != nil {
		return nil, err
	}
	for i := range list {
		if err := s.open(ctx, tx, tenant, &list[i]); err != nil {
			return nil, err
		}
	}
	if list == nil {
		list = []Invoice{}
	}
	return list, nil
}

// --- writing ---

// check validates a draft against the settings and returns its lines and
// total.
func check(d Draft) ([]Line, int64, error) {
	d.Customer.Name = strings.TrimSpace(d.Customer.Name)
	if d.Customer.Name == "" || len(d.Customer.Name) > 120 {
		return nil, 0, invalid("the customer's name is required (up to 120 characters)")
	}
	if d.Customer.Phone == "" && d.Customer.Email == "" {
		return nil, 0, invalid("give the customer's phone number or email, to send the invoice to")
	}
	if d.Customer.Email != "" {
		if a, err := mail.ParseAddress(d.Customer.Email); err != nil || a.Address != d.Customer.Email {
			return nil, 0, invalid("%q is not an email address", d.Customer.Email)
		}
	}
	lines := d.Lines
	if len(lines) == 0 {
		if d.AmountKobo <= 0 {
			return nil, 0, invalid("give the amount, or the items with their prices")
		}
		desc := strings.TrimSpace(d.Description)
		if desc == "" {
			desc = "Amount due"
		}
		lines = []Line{{Description: desc, Quantity: 1, UnitKobo: d.AmountKobo}}
	} else if d.AmountKobo != 0 {
		return nil, 0, invalid("give the amount or the items, not both")
	}
	if len(lines) > MaxLines {
		return nil, 0, invalid("an invoice has at most %d items", MaxLines)
	}
	var total int64
	for i, l := range lines {
		lines[i].Description = strings.TrimSpace(l.Description)
		if lines[i].Description == "" || len(lines[i].Description) > 200 {
			return nil, 0, invalid("item %d needs a description (up to 200 characters)", i+1)
		}
		if l.Quantity < 1 || l.Quantity > 1_000_000 || l.UnitKobo < 1 || l.UnitKobo > MaxTotalKobo {
			return nil, 0, invalid("item %d needs a quantity of at least 1 and a price of at least 1 kobo", i+1)
		}
		total += l.AmountKobo()
		if total > MaxTotalKobo {
			return nil, 0, invalid("the total is more than ₦1,000,000,000")
		}
	}
	if total < 100 {
		return nil, 0, invalid("the total must be at least ₦1")
	}
	if d.DueOn != "" {
		if _, err := time.Parse(time.DateOnly, d.DueOn); err != nil {
			return nil, 0, invalid("due_on is a date, YYYY-MM-DD")
		}
	}
	if len(d.Note) > 500 {
		return nil, 0, invalid("the note has at most 500 characters")
	}
	if d.FeePaidBy != "" && d.FeePaidBy != "business" && d.FeePaidBy != "customer" {
		return nil, 0, invalid("fee_paid_by is business or customer")
	}
	return lines, total, nil
}

func nullable(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// seal seals the customer under their phone's (else email's) subject.
func (s *Invoices) seal(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, c Customer) (map[string]any, string, error) {
	cat, key := "phone", c.Phone
	if key == "" {
		cat, key = "email", c.Email
	}
	env, err := s.Vault.SealTx(ctx, tx, tenant, cat, key)
	if err != nil {
		return nil, "", err
	}
	subject, _ := env["subject"].(string)
	sealed, err := s.Vault.SealUnderTx(ctx, tx, tenant, subject, "customer", c)
	return sealed, subject, err
}

// Create makes a draft invoice with the business's next number.
func (s *Invoices) Create(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, d Draft, by string) (Invoice, error) {
	return s.CreateOnce(ctx, tx, tenant, "", d, by)
}

// CreateOnce is Create under a key (a workflow step's): the same key again
// returns the invoice the first call made, so a retry takes no second
// number.
func (s *Invoices) CreateOnce(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, key string, d Draft, by string) (Invoice, error) {
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
	lines, total, err := check(d)
	if err != nil {
		return Invoice{}, err
	}
	number, st, err := nextNumber(ctx, tx, tenant)
	if err != nil {
		return Invoice{}, err
	}
	cust, subject, err := s.seal(ctx, tx, tenant, d.Customer)
	if err != nil {
		return Invoice{}, err
	}
	conn, fee, part := st.Connection, st.FeePaidBy, st.AllowPartPayments
	if d.Connection != "" {
		conn = d.Connection
	}
	if d.FeePaidBy != "" {
		fee = d.FeePaidBy
	}
	if d.AllowPartPayments != nil {
		part = *d.AllowPartPayments
	}
	custRaw, _ := json.Marshal(cust)
	linesRaw, _ := json.Marshal(lines)
	id := uuid.New()
	if _, err := tx.Exec(ctx, `INSERT INTO customer_invoices (id, tenant_id, number, customer, subject_id, lines, total_kobo, due_on, note,
		connection, fee_paid_by, allow_part_payments, created_by, created_key) VALUES ($1, $2, $3, $4, $5, $6, $7, $8::date, $9, $10, $11, $12, $13, $14)`,
		id, tenant, number, custRaw, subject, linesRaw, total, nullable(d.DueOn), nullable(d.Note), conn, fee, part, by, nullable(key)); err != nil {
		return Invoice{}, err
	}
	if err := audit(ctx, tx, tenant, by, "invoice.created", number, map[string]any{"total_kobo": total}); err != nil {
		return Invoice{}, err
	}
	return s.Get(ctx, tx, tenant, id.String())
}

// Update changes a draft. A sent invoice cannot be changed (void it and
// send another); the database refuses it too.
func (s *Invoices) Update(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, idOrNumber string, d Draft, by string) (Invoice, error) {
	cur, err := s.Get(ctx, tx, tenant, idOrNumber)
	if err != nil {
		return Invoice{}, err
	}
	if cur.Status != "draft" {
		return Invoice{}, fmt.Errorf("%w: invoice %s is %s; only a draft can be changed (void it and send another)", ErrState, cur.Number, cur.Status)
	}
	lines, total, err := check(d)
	if err != nil {
		return Invoice{}, err
	}
	cust, subject, err := s.seal(ctx, tx, tenant, d.Customer)
	if err != nil {
		return Invoice{}, err
	}
	conn, fee, part := cur.Connection, cur.FeePaidBy, cur.AllowPartPayments
	if d.Connection != "" {
		conn = d.Connection
	}
	if d.FeePaidBy != "" {
		fee = d.FeePaidBy
	}
	if d.AllowPartPayments != nil {
		part = *d.AllowPartPayments
	}
	custRaw, _ := json.Marshal(cust)
	linesRaw, _ := json.Marshal(lines)
	if _, err := tx.Exec(ctx, `UPDATE customer_invoices SET customer = $3, subject_id = $4, lines = $5, total_kobo = $6, due_on = $7::date, note = $8,
		connection = $9, fee_paid_by = $10, allow_part_payments = $11, updated_at = now() WHERE tenant_id = $1 AND id = $2`,
		tenant, cur.ID, custRaw, subject, linesRaw, total, nullable(d.DueOn), nullable(d.Note), conn, fee, part); err != nil {
		return Invoice{}, err
	}
	if err := audit(ctx, tx, tenant, by, "invoice.updated", cur.Number, map[string]any{"total_kobo": total}); err != nil {
		return Invoice{}, err
	}
	return s.Get(ctx, tx, tenant, cur.ID.String())
}

// Void cancels an invoice that is not paid. Payments recorded against it
// stay; money that still arrives for it is not counted.
func (s *Invoices) Void(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, idOrNumber, reason, by string) (Invoice, error) {
	cur, err := s.Get(ctx, tx, tenant, idOrNumber)
	if err != nil {
		return Invoice{}, err
	}
	reason = strings.TrimSpace(reason)
	if reason == "" || len(reason) > 300 {
		return Invoice{}, invalid("say why the invoice is void (up to 300 characters)")
	}
	switch cur.Status {
	case "void":
		return cur, nil
	case "paid":
		return Invoice{}, fmt.Errorf("%w: invoice %s is paid; refund the customer instead", ErrState, cur.Number)
	}
	if _, err := tx.Exec(ctx, `UPDATE customer_invoices SET voided_at = now(), void_reason = $3, updated_at = now() WHERE tenant_id = $1 AND id = $2`,
		tenant, cur.ID, reason); err != nil {
		return Invoice{}, err
	}
	if err := audit(ctx, tx, tenant, by, "invoice.voided", cur.Number, map[string]any{"reason": reason, "paid_kobo": cur.PaidKobo}); err != nil {
		return Invoice{}, err
	}
	return s.Get(ctx, tx, tenant, cur.ID.String())
}

// Sent is the outcome of sending an invoice.
type Sent struct {
	Invoice Invoice  `json:"invoice"`
	Link    string   `json:"link,omitempty"`
	Queued  []string `json:"queued"`
	// SMSOverAllowance: the SMS was not sent, the plan's allowance being used.
	SMSOverAllowance bool `json:"sms_over_allowance,omitempty"`
}

// Send sends an invoice to its customer by SMS and email, with its page's
// link, and marks it sent. Sending again resends the same link; a key
// makes each send's messages go once.
func (s *Invoices) Send(ctx context.Context, tenant uuid.UUID, idOrNumber, by, key string) (Sent, error) {
	var out Sent
	err := db.InTenantTx(ctx, s.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		cur, err := s.Get(ctx, tx, tenant, idOrNumber)
		if err != nil {
			return err
		}
		switch cur.Status {
		case "void":
			return fmt.Errorf("%w: invoice %s is void", ErrState, cur.Number)
		case "paid":
			return fmt.Errorf("%w: invoice %s is paid", ErrState, cur.Number)
		}
		token, err := s.token(ctx, tx, tenant, &cur)
		if err != nil {
			return err
		}
		if cur.SentAt == nil {
			if _, err := tx.Exec(ctx, `UPDATE customer_invoices SET sent_at = now(), updated_at = now() WHERE tenant_id = $1 AND id = $2`, tenant, cur.ID); err != nil {
				return err
			}
		}
		if key == "" {
			key = "send:" + cur.ID.String() + ":" + s.now().UTC().Format(time.RFC3339Nano)
		}
		if s.PageBase != "" {
			out.Link = strings.TrimRight(s.PageBase, "/") + "/i/" + token
		}
		var business string
		if err := tx.QueryRow(ctx, `SELECT name FROM tenants WHERE id = $1`, tenant).Scan(&business); err != nil {
			return err
		}
		out.Queued, out.SMSOverAllowance, err = s.deliver(ctx, tx, tenant, cur, business, sendText(business, cur, out.Link), "Invoice "+cur.Number+" from "+business, "invoice:"+key)
		if err != nil {
			return err
		}
		if err := audit(ctx, tx, tenant, by, "invoice.sent", cur.Number, map[string]any{"channels": out.Queued}); err != nil {
			return err
		}
		out.Invoice, err = s.Get(ctx, tx, tenant, cur.ID.String())
		return err
	})
	return out, err
}

// token is the invoice's page token, made once and kept sealed so the
// same link can be sent again; only its hash finds the page.
func (s *Invoices) token(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, x *Invoice) (string, error) {
	if x.tokenEnv != nil {
		v, err := s.Vault.OpenTx(ctx, tx, tenant, x.tokenEnv)
		if err != nil {
			return "", err
		}
		if t, ok := v.(string); ok && t != "" {
			return t, nil
		}
	}
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	t := base64.RawURLEncoding.EncodeToString(b)
	env, err := s.Vault.SealUnderTx(ctx, tx, tenant, x.subject, "invoice_link", t)
	if err != nil {
		return "", err
	}
	raw, _ := json.Marshal(env)
	h := TokenHash(t)
	if _, err := tx.Exec(ctx, `UPDATE customer_invoices SET page_token = $3, page_token_hash = $4, updated_at = now() WHERE tenant_id = $1 AND id = $2`,
		tenant, x.ID, raw, h); err != nil {
		return "", err
	}
	return t, nil
}

// TokenHash is what finds an invoice's page from its link's token.
func TokenHash(token string) []byte {
	h := sha256.Sum256([]byte("taskiem invoice page " + token))
	return h[:]
}

// Naira formats kobo as naira for people: ₦25,000 or ₦25,000.50.
func Naira(kobo int64) string {
	n, k := kobo/100, kobo%100
	s := fmt.Sprint(n)
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	if k != 0 {
		return fmt.Sprintf("₦%s.%02d", s, k)
	}
	return "₦" + s
}

func sendText(business string, x Invoice, link string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Invoice %s from %s: %s", x.Number, business, Naira(x.TotalKobo-x.PaidKobo))
	if x.DueOn != "" {
		if d, err := time.Parse(time.DateOnly, x.DueOn); err == nil {
			fmt.Fprintf(&b, ", due %s", d.Format("2 Jan 2006"))
		}
	}
	b.WriteString(".")
	if link != "" {
		b.WriteString(" See it and pay: " + link)
	}
	return b.String()
}

// deliver queues a message to the invoice's customer by SMS and email.
func (s *Invoices) deliver(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, x Invoice, business, text, subject, key string) ([]string, bool, error) {
	if s.Messages == nil {
		return nil, false, errors.New("invoicing: messages to customers are not configured")
	}
	channels := []string{}
	over := false
	if x.Customer.Phone != "" {
		queued, err := s.Messages.Enqueue(ctx, tx, sender.Message{Tenant: tenant, Channel: sender.SMS, Purpose: sender.PurposeLink,
			To: x.Customer.Phone, SenderName: business, Text: text, Key: key + ":sms"})
		switch {
		case errors.Is(err, sender.ErrAllowance):
			over = true
		case err != nil:
			return nil, false, err
		case queued:
			channels = append(channels, "sms")
		}
	}
	if x.Customer.Email != "" {
		queued, err := s.Messages.Enqueue(ctx, tx, sender.Message{Tenant: tenant, Channel: sender.Email, Purpose: sender.PurposeLink,
			To: x.Customer.Email, SenderName: business, Subject: subject, Text: text, Key: key + ":email"})
		if err != nil {
			return nil, false, err
		}
		if queued {
			channels = append(channels, "email")
		}
	}
	return channels, over, nil
}

// NotifyCustomer sends the invoice's customer a message: {link} becomes
// the invoice's page link, {number} its number and {due} what is due.
func (s *Invoices) NotifyCustomer(ctx context.Context, tenant uuid.UUID, idOrNumber, text, subject, key string) (map[string]any, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil, invalid("no text")
	}
	out := map[string]any{}
	err := db.InTenantTx(ctx, s.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		x, err := s.Get(ctx, tx, tenant, idOrNumber)
		if err != nil {
			return err
		}
		if x.SentAt == nil {
			return fmt.Errorf("%w: invoice %s has not been sent", ErrState, x.Number)
		}
		if strings.Contains(text, "{link}") {
			t, err := s.token(ctx, tx, tenant, &x)
			if err != nil {
				return err
			}
			text = strings.ReplaceAll(text, "{link}", strings.TrimRight(s.PageBase, "/")+"/i/"+t)
		}
		text = strings.NewReplacer("{number}", x.Number, "{due}", Naira(x.DueKobo)).Replace(text)
		var business string
		if err := tx.QueryRow(ctx, `SELECT name FROM tenants WHERE id = $1`, tenant).Scan(&business); err != nil {
			return err
		}
		if subject == "" {
			subject = "Invoice " + x.Number + " from " + business
		}
		queued, over, err := s.deliver(ctx, tx, tenant, x, business, text, subject, "notice:"+key)
		if err != nil {
			return err
		}
		out["number"], out["queued"] = x.Number, queued
		if over {
			out["sms_over_allowance"] = true
		}
		return nil
	})
	return out, err
}

// --- payments ---

// PaymentIn is a payment to count towards an invoice.
type PaymentIn struct {
	AmountKobo int64
	PaymentID  string // the rail's payment id, or the money log's entry
	Connection string
	How        string // exact, suggested, suggested_auto or manual
	By         string
	Reasons    []string
}

// RecordPayment counts a payment towards an invoice, once: the same
// payment again is a no-op. Matching (T5) and one-time accounts (T4) call
// it; nothing else marks an invoice paid. When the invoice becomes paid,
// a run waiting on it is signalled after the transaction commits (the
// caller's commit: call Paid with what this returns).
func (s *Invoices) RecordPayment(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, idOrNumber string, p PaymentIn) (Invoice, bool, error) {
	cur, err := s.Get(ctx, tx, tenant, idOrNumber)
	if err != nil {
		return Invoice{}, false, err
	}
	if p.AmountKobo <= 0 || p.PaymentID == "" {
		return Invoice{}, false, invalid("a payment needs an amount and its id")
	}
	switch p.How {
	case "exact", "suggested", "suggested_auto", "manual":
	default:
		return Invoice{}, false, invalid("how is exact, suggested, suggested_auto or manual")
	}
	if cur.Status == "draft" {
		return Invoice{}, false, fmt.Errorf("%w: invoice %s has not been sent", ErrState, cur.Number)
	}
	reasons, _ := json.Marshal(nonNil(p.Reasons))
	tag, err := tx.Exec(ctx, `INSERT INTO customer_invoice_payments (id, tenant_id, invoice_id, amount_kobo, payment_id, connection, how, by, reasons)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9) ON CONFLICT (invoice_id, payment_id) WHERE undoes IS NULL DO NOTHING`,
		uuid.New(), tenant, cur.ID, p.AmountKobo, p.PaymentID, nullable(p.Connection), p.How, p.By, reasons)
	if err != nil {
		return Invoice{}, false, err
	}
	if tag.RowsAffected() == 0 {
		return cur, false, nil
	}
	if err := audit(ctx, tx, tenant, p.By, "invoice.payment", cur.Number, map[string]any{"amount_kobo": p.AmountKobo, "payment_id": p.PaymentID, "how": p.How}); err != nil {
		return Invoice{}, false, err
	}
	x, err := s.Get(ctx, tx, tenant, cur.ID.String())
	return x, x.Status == "paid" && cur.Status != "paid", err
}

// UndoPayment undoes a counted payment (a wrong match): its row stays,
// and a row naming it takes it out of the invoice's total.
func (s *Invoices) UndoPayment(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, idOrNumber string, payment uuid.UUID, by string) (Invoice, error) {
	cur, err := s.Get(ctx, tx, tenant, idOrNumber)
	if err != nil {
		return Invoice{}, err
	}
	var p *Payment
	for i := range cur.Payments {
		if cur.Payments[i].ID == payment && cur.Payments[i].Undoes == nil {
			p = &cur.Payments[i]
		}
	}
	if p == nil {
		return Invoice{}, ErrNotFound
	}
	if p.UndoneBy != nil {
		return cur, nil
	}
	_, err = tx.Exec(ctx, `INSERT INTO customer_invoice_payments (id, tenant_id, invoice_id, amount_kobo, payment_id, connection, how, by, undoes)
		VALUES ($1, $2, $3, $4, $5, $6, 'manual', $7, $8)`, uuid.New(), tenant, cur.ID, p.AmountKobo, p.PaymentID, nullable(p.Connection), by, p.ID)
	var pe *pgconn.PgError
	if errors.As(err, &pe) && pe.Code == "23505" {
		return s.Get(ctx, tx, tenant, cur.ID.String()) // undone meanwhile
	}
	if err != nil {
		return Invoice{}, err
	}
	if err := audit(ctx, tx, tenant, by, "invoice.payment_undone", cur.Number, map[string]any{"amount_kobo": p.AmountKobo, "payment_id": p.PaymentID}); err != nil {
		return Invoice{}, err
	}
	return s.Get(ctx, tx, tenant, cur.ID.String())
}

// Paid tells a run waiting on the invoice that it is paid. Call it after
// the transaction that recorded the payment commits.
func (s *Invoices) Paid(ctx context.Context, tenant uuid.UUID, x Invoice) error {
	if s.Signal == nil {
		return nil
	}
	_, _, err := s.Signal(ctx, tenant, "prod", PaidSignal, x.Number, "paid:"+x.ID.String(),
		map[string]any{"number": x.Number, "status": "paid", "total_kobo": x.TotalKobo, "paid_kobo": x.PaidKobo})
	return err
}

// Overdue moves sent and part-paid invoices past their due date to
// overdue, for every tenant. Run it daily (and at start).
func (s *Invoices) Overdue(ctx context.Context) (int, error) {
	rows, err := s.Pool.Query(ctx, `SELECT tenant_id, id, number FROM taskiem_customer_invoices_overdue()`)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	n := 0
	for rows.Next() {
		n++
	}
	if n > 0 {
		s.log().Info("invoices overdue", "count", n)
	}
	return n, rows.Err()
}

func nonNil(v []string) []string {
	if v == nil {
		return []string{}
	}
	return v
}

func audit(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, actor, action, target string, detail map[string]any) error {
	if detail == nil {
		detail = map[string]any{}
	}
	raw, _ := json.Marshal(detail)
	kind := "user"
	if !strings.HasPrefix(actor, "user:") {
		kind = "system"
	}
	_, err := tx.Exec(ctx, `SELECT taskiem_audit_append($1, $2, $3, $4, $5, $6)`, tenant, kind, actor, action, target, raw)
	return err
}

// Service runs each call in the tenant's own transaction: what the
// invoices platform connector and chat use.
type Service struct{ *Invoices }

func (s Service) in(ctx context.Context, tenant uuid.UUID, fn func(pgx.Tx) error) error {
	return db.InTenantTx(ctx, s.Pool, []uuid.UUID{tenant}, fn)
}

// CreateOnce creates (or, for a key seen before, finds) a draft.
func (s Service) CreateOnce(ctx context.Context, tenant uuid.UUID, key string, d Draft, by string) (Invoice, error) {
	var x Invoice
	err := s.in(ctx, tenant, func(tx pgx.Tx) error {
		var err error
		x, err = s.Invoices.CreateOnce(ctx, tx, tenant, key, d, by)
		return err
	})
	return x, err
}

// Read reads one invoice.
func (s Service) Read(ctx context.Context, tenant uuid.UUID, idOrNumber string) (Invoice, error) {
	var x Invoice
	err := s.in(ctx, tenant, func(tx pgx.Tx) error {
		var err error
		x, err = s.Get(ctx, tx, tenant, idOrNumber)
		return err
	})
	return x, err
}

// Cancel voids an invoice.
func (s Service) Cancel(ctx context.Context, tenant uuid.UUID, idOrNumber, reason, by string) (Invoice, error) {
	var x Invoice
	err := s.in(ctx, tenant, func(tx pgx.Tx) error {
		var err error
		x, err = s.Void(ctx, tx, tenant, idOrNumber, reason, by)
		return err
	})
	return x, err
}
