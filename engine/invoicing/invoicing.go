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
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/lang"
	"github.com/israel-duff/taskiem/engine/moneylog"
	"github.com/israel-duff/taskiem/engine/pii"
	"github.com/israel-duff/taskiem/engine/reach"
	"github.com/israel-duff/taskiem/engine/runtime"
	"github.com/israel-duff/taskiem/engine/secrets"
	"github.com/israel-duff/taskiem/engine/sender"
)

// PaidSignal is the signal a run waiting on an invoice receives when it is
// paid in full, correlated by the invoice's number. It resumes runs and
// starts none: a workflow that starts on an invoice being paid has the
// invoice trigger (TriggerType).
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
	// Key is the business's own key for the customer ("student:42"), when
	// it has one: invoices with the same key are one customer's. Without
	// it, a customer is their phone number (else email): CustomerID.
	Key string `json:"key,omitempty"`
}

// Line is one item: quantity times unit price.
type Line struct {
	Description string `json:"description"`
	Quantity    int64  `json:"quantity"`
	UnitKobo    int64  `json:"unit_kobo"`
	// PassThroughFor names the body a pass-through line is collected for
	// (WAEC, NECO): money the business collects on someone else's behalf,
	// never its own income (docs/money-reports.md#pass-through).
	PassThroughFor string `json:"pass_through_for,omitempty"`
	// Kind is empty for an item, LineDiscount for a discount and
	// LineRemoval for an amendment taking an earlier line out: the only
	// lines with a negative price.
	Kind string `json:"kind,omitempty"`
	// Reason says why a discount or an amendment was given.
	Reason string `json:"reason,omitempty"`
	// Amendment is the amendment that added the line to a sent invoice.
	Amendment string `json:"amendment,omitempty"`
}

// Kinds of line.
const (
	LineDiscount = "discount"
	LineRemoval  = "removal"
)

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
	ID                uuid.UUID `json:"id"`
	Number            string    `json:"number"`
	Status            string    `json:"status"`
	Customer          Customer  `json:"customer"`
	Lines             []Line    `json:"lines"`
	TotalKobo         int64     `json:"total_kobo"`
	PaidKobo          int64     `json:"paid_kobo"`
	DueKobo           int64     `json:"due_kobo"`
	Currency          string    `json:"currency"`
	DueOn             string    `json:"due_on,omitempty"`
	Note              string    `json:"note,omitempty"`
	Connection        string    `json:"connection"`
	FeePaidBy         string    `json:"fee_paid_by"`
	AllowPartPayments bool      `json:"allow_part_payments"`
	// Kind is invoice, or credit: a notebook debt recorded in chat,
	// recorded (RecordedAt) and open but not sent (book.go).
	Kind       string     `json:"kind"`
	RecordedAt *time.Time `json:"recorded_at,omitempty"`
	SentAt     *time.Time `json:"sent_at"`
	PaidAt     *time.Time `json:"paid_at"`
	VoidedAt   *time.Time `json:"voided_at"`
	VoidReason string     `json:"void_reason,omitempty"`
	CreatedBy  string     `json:"created_by"`
	CreatedAt  time.Time  `json:"created_at"`
	Payments   []Payment  `json:"payments,omitempty"`
	// Amendments are the changes made to it after it was sent, each also
	// a line (amendments.go).
	Amendments []Amendment `json:"amendments,omitempty"`

	customerEnv map[string]any
	tokenEnv    map[string]any
	subject     string
	customerID  string
}

// CustomerID is who the invoice is for, as matching knows customers:
// "key:<the business's key>" when the invoice has one, else the
// personal-data subject of the customer's phone (else email), a
// pseudonym. Invoices with the same CustomerID are one customer's.
func (x Invoice) CustomerID() string { return x.customerID }

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
	// FeeKobo is the rail's fee on the transfer this payment is, when the
	// rail said what it kept and the payment took the whole transfer: the
	// payer sent AmountKobo and the business received that less the fee.
	FeeKobo *int64 `json:"fee_kobo,omitempty"`
}

// Fee is the rail's fee on the payments counted towards the invoice, for
// its receipt: what was sent, what the fee was, and whether there is one
// to show. There is one only when every payment standing was a whole
// transfer whose fee the rail reported (a cash payment, a transfer split
// over invoices or a rail that does not say leaves it unknown).
func (x Invoice) Fee() (sent, fee int64, ok bool) {
	for _, p := range x.Payments {
		if p.Undoes != nil || p.UndoneBy != nil {
			continue
		}
		if p.FeeKobo == nil {
			return 0, 0, false
		}
		sent, fee = sent+p.AmountKobo, fee+*p.FeeKobo
	}
	return sent, fee, fee > 0 && fee < sent
}

// Settings are a business's defaults for its invoices.
type Settings struct {
	Prefix            string `json:"prefix"`
	NextNumber        int    `json:"next_number"`
	Connection        string `json:"connection"`
	FeePaidBy         string `json:"fee_paid_by"`
	AllowPartPayments bool   `json:"allow_part_payments"`
	// AutoMatch: a strong suggestion is matched without asking (T5).
	AutoMatch bool `json:"auto_match"`
	// CreditDays is how long a customer has to pay a notebook debt (nil:
	// no fixed time), and CreditTermsAt when the owner answered: until
	// then no credit line has a due date (book.go). SaveSettings leaves
	// them as they are; SetCreditTerms sets them.
	CreditDays    *int       `json:"credit_days"`
	CreditTermsAt *time.Time `json:"credit_terms_at"`
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
	// Start starts a run (runtime.Store.Start): StartPaid starts the
	// workflows whose trigger is an invoice being paid. Nil starts none.
	Start func(ctx context.Context, req runtime.StartRequest) (runtime.Started, error)
	// Rails issues accounts and links and looks payments up (T4); nil:
	// invoices take no payments on their page.
	Rails Rails
	// AccountRails issue customers' permanent account numbers
	// (virtual_account, issue.go); nil: Rails, when it can.
	AccountRails CapabilityRails
	// Answered is told of a request from chat once it is issued (or
	// failed), to reply to the customer there.
	Answered func(ctx context.Context, r Request, a Account, err error)
	// OrderRequested issues what a storefront order's status page asked
	// for (N3): the storefront checks the order and calls IssueForOrder.
	// OrderReceived counts a payment the checker found into an order's
	// account towards the order. Without them, orders' requests are
	// dropped and their accounts only checked.
	OrderRequested func(ctx context.Context, tenant, order uuid.UUID) error
	OrderReceived  func(ctx context.Context, tenant, order uuid.UUID, r Received) error
	// LinkReceived counts a payment the checker found through a
	// customer's payment link (customerlink.go) towards their open
	// invoices (engine/matching LinkPaid). Without it such accounts are
	// only checked, and matching counts the payment when the rail's event
	// is heard.
	LinkReceived func(ctx context.Context, tenant, link uuid.UUID, r Received) error
	// MoneyLog records the payments the checker finds (T2); nil records
	// none.
	MoneyLog *moneylog.Log
	// Reach finds a customer who asked the business for updates on
	// WhatsApp or Telegram (decision 0039); Send then also queues the
	// invoice there, with its buttons, on one of Chat (the channels this
	// deployment runs). Nil: SMS and email only.
	Reach *reach.Book
	Chat  []string
	// Counted, when set, is told in the transaction of every part counted
	// towards an invoice (CountPayment), after it is written: a school
	// issues the payment's receipt here (docs/spec/schools.md 9). Undone
	// is told of a part undone (UndoPayment), a wrong match or money that
	// went back: the receipt is voided. Nil: nothing.
	Counted func(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, k Counted, p PaymentIn) error
	Undone  func(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, x Invoice, part uuid.UUID, by string) error
	// FamilyLink, when set, mints a school family's page link for an
	// invoice (docs/schools.md#the-family-page), "" for an invoice that is
	// not a school's: a school's invoice is sent with that link in place
	// of its own page's, and the payment event's body carries it as
	// family_link. purpose says what the link goes out with (invoice,
	// receipt). Nil: invoices carry their own page link only.
	FamilyLink func(ctx context.Context, tx pgx.Tx, tenant, invoice uuid.UUID, purpose string) (string, error)
	Logger     *slog.Logger
	Now        func() time.Time
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
	err := tx.QueryRow(ctx, `SELECT prefix, next_number, connection, fee_paid_by, allow_part_payments, auto_match, credit_days, credit_terms_at
		FROM customer_invoice_settings WHERE tenant_id = $1`, tenant).
		Scan(&st.Prefix, &st.NextNumber, &st.Connection, &st.FeePaidBy, &st.AllowPartPayments, &st.AutoMatch, &st.CreditDays, &st.CreditTermsAt)
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
	_, err = tx.Exec(ctx, `INSERT INTO customer_invoice_settings (tenant_id, prefix, next_number, connection, fee_paid_by, allow_part_payments, auto_match)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		ON CONFLICT (tenant_id) DO UPDATE SET prefix = $2, next_number = $3, connection = $4, fee_paid_by = $5, allow_part_payments = $6, auto_match = $7,
		updated_at = now()`,
		tenant, st.Prefix, st.NextNumber, st.Connection, st.FeePaidBy, st.AllowPartPayments, st.AutoMatch)
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
	COALESCE(note, ''), connection, fee_paid_by, allow_part_payments, sent_at, paid_at, voided_at, COALESCE(void_reason, ''), created_by, created_at, page_token, customer_id,
	kind, recorded_at`

func scan(r pgx.Row) (Invoice, error) {
	var x Invoice
	var cust, lines, token []byte
	err := r.Scan(&x.ID, &x.Number, &x.Status, &cust, &x.subject, &lines, &x.TotalKobo, &x.PaidKobo, &x.Currency, &x.DueOn,
		&x.Note, &x.Connection, &x.FeePaidBy, &x.AllowPartPayments, &x.SentAt, &x.PaidAt, &x.VoidedAt, &x.VoidReason, &x.CreatedBy, &x.CreatedAt, &token, &x.customerID,
		&x.Kind, &x.RecordedAt)
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
	if x.Payments, err = payments(ctx, tx, x.ID); err != nil {
		return Invoice{}, err
	}
	x.Amendments, err = amendments(ctx, tx, x.ID)
	return x, err
}

func payments(ctx context.Context, tx pgx.Tx, invoice uuid.UUID) ([]Payment, error) {
	// The fee is the money log entry's when the part took the whole of it
	// (the schools' receipts' rule, engine/schools).
	rows, err := tx.Query(ctx, `SELECT p.id, p.amount_kobo, p.payment_id, COALESCE(p.connection, ''), p.how, p.by, p.reasons, p.undoes, u.id, p.created_at,
		(SELECT e.fee_kobo FROM money_entries e WHERE e.id = p.entry_id AND e.amount_kobo = p.amount_kobo AND e.fee_kobo > 0 AND e.fee_kobo < e.amount_kobo)
		FROM customer_invoice_payments p LEFT JOIN customer_invoice_payments u ON u.undoes = p.id
		WHERE p.invoice_id = $1 ORDER BY p.created_at, p.id`, invoice)
	if err != nil {
		return nil, err
	}
	out, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (Payment, error) {
		var p Payment
		var reasons []byte
		err := r.Scan(&p.ID, &p.AmountKobo, &p.PaymentID, &p.Connection, &p.How, &p.By, &reasons, &p.Undoes, &p.UndoneBy, &p.CreatedAt, &p.FeeKobo)
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
	return s.ListOf(ctx, tx, tenant, status, "", limit)
}

// ListOf is List, optionally of one kind too (invoice, or credit: the
// debtor book's notebook debts).
func (s *Invoices) ListOf(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, status, kind string, limit int) ([]Invoice, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := tx.Query(ctx, `SELECT `+cols+` FROM customer_invoices WHERE tenant_id = $1 AND ($2 = '' OR status = $2) AND ($4 = '' OR kind = $4)
		ORDER BY created_at DESC, id LIMIT $3`, tenant, status, limit, kind)
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
	// A customer of the debtor book may have neither (book.go).
	if d.Customer.Phone == "" && d.Customer.Email == "" && !strings.HasPrefix(d.Customer.Key, BookKeyPrefix) {
		return nil, 0, invalid("give the customer's phone number or email, to send the invoice to")
	}
	if d.Customer.Email != "" {
		if a, err := mail.ParseAddress(d.Customer.Email); err != nil || a.Address != d.Customer.Email {
			return nil, 0, invalid("%q is not an email address", d.Customer.Email)
		}
	}
	if !ValidCustomerKey(d.Customer.Key) {
		return nil, 0, invalid("the customer's key has up to 100 letters, digits and ':', '.', '_', '/' or '-'")
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
		lines[i].PassThroughFor = strings.Join(strings.Fields(l.PassThroughFor), " ")
		lines[i].Reason = strings.TrimSpace(l.Reason)
		if lines[i].Description == "" || len(lines[i].Description) > 200 {
			return nil, 0, invalid("item %d needs a description (up to 200 characters)", i+1)
		}
		if l.Amendment != "" || l.Kind == LineRemoval {
			return nil, 0, invalid("item %d: amendments are made to a sent invoice (amend it)", i+1)
		}
		if len(lines[i].PassThroughFor) > 60 || len(lines[i].Reason) > 300 {
			return nil, 0, invalid("item %d: pass_through_for has up to 60 characters and a reason up to 300", i+1)
		}
		switch l.Kind {
		case "":
			if l.Quantity < 1 || l.Quantity > 1_000_000 || l.UnitKobo < 1 || l.UnitKobo > MaxTotalKobo {
				return nil, 0, invalid("item %d needs a quantity of at least 1 and a price of at least 1 kobo", i+1)
			}
		case LineDiscount:
			// A discount is the business's own: it is never collected for
			// someone else.
			if l.Quantity != 1 || l.UnitKobo > -1 || -l.UnitKobo > MaxTotalKobo || lines[i].PassThroughFor != "" {
				return nil, 0, invalid("item %d is a discount: a quantity of 1, a negative price, and not pass-through", i+1)
			}
		default:
			return nil, 0, invalid("item %d: kind is empty or discount", i+1)
		}
		total += l.AmountKobo()
		if total > MaxTotalKobo {
			return nil, 0, invalid("the total is more than ₦1,000,000,000")
		}
	}
	if total < 100 {
		return nil, 0, invalid("the total must be at least ₦1")
	}
	if own, _ := PassThrough(lines); own < 0 {
		return nil, 0, invalid("the discounts are more than the business's own items")
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

// ValidCustomerKey reports whether a business's key for a customer may be
// stored: empty (none), or up to 100 letters, digits and ":._/-".
func ValidCustomerKey(k string) bool {
	if len(k) > 100 {
		return false
	}
	for _, r := range k {
		ok := r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune(":._/-", r)
		if !ok {
			return false
		}
	}
	return true
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
		connection, fee_paid_by, allow_part_payments, created_by, created_key, customer_key) VALUES ($1, $2, $3, $4, $5, $6, $7, $8::date, $9, $10, $11, $12, $13, $14, $15)`,
		id, tenant, number, custRaw, subject, linesRaw, total, nullable(d.DueOn), nullable(d.Note), conn, fee, part, by, nullable(key), nullable(d.Customer.Key)); err != nil {
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
		connection = $9, fee_paid_by = $10, allow_part_payments = $11, customer_key = $12, updated_at = now() WHERE tenant_id = $1 AND id = $2`,
		tenant, cur.ID, custRaw, subject, linesRaw, total, nullable(d.DueOn), nullable(d.Note), conn, fee, part, nullable(d.Customer.Key)); err != nil {
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
	Invoice Invoice `json:"invoice"`
	Link    string  `json:"link,omitempty"`
	// FamilyLink is the school family page's link a school's invoice went
	// out with instead of its own page's (FamilyLink hook).
	FamilyLink string   `json:"family_link,omitempty"`
	Queued     []string `json:"queued"`
	// SMSOverAllowance: the SMS was not sent, the plan's allowance being used.
	SMSOverAllowance bool `json:"sms_over_allowance,omitempty"`
	// CreditKobo is how much of the customer's credit this send counted
	// towards the invoice.
	CreditKobo int64 `json:"credit_kobo,omitempty"`
}

// Send sends an invoice to its customer by SMS and email, with its page's
// link, and marks it sent. A customer who asked the business for updates
// on WhatsApp or Telegram also gets it there, with its buttons (chat.go). Sending again resends the same link; a key
// makes each send's messages go once.
func (s *Invoices) Send(ctx context.Context, tenant uuid.UUID, idOrNumber, by, key string) (Sent, error) {
	var out Sent
	var became bool
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
			if cur, err = s.Get(ctx, tx, tenant, cur.ID.String()); err != nil {
				return err
			}
		}
		// The customer's credit counts towards it first: the message says
		// what is left to pay.
		if cur, out.CreditKobo, became, err = s.applyCredit(ctx, tx, tenant, cur); err != nil {
			return err
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
		// A school's invoice goes out with the family page's link, which
		// shows every child's fees and links each open invoice's page.
		link, family := out.Link, false
		if s.FamilyLink != nil {
			fl, err := s.FamilyLink(ctx, tx, tenant, cur.ID, "invoice")
			if err != nil {
				return err
			}
			if fl != "" {
				link, family, out.FamilyLink = fl, true, fl
			}
		}
		text := sendText(business, cur, link)
		if family {
			text = familySendText(business, cur, link)
		}
		out.Queued, out.SMSOverAllowance, err = s.deliver(ctx, tx, tenant, cur, business, text, "Invoice "+cur.Number+" from "+business, "invoice:"+key)
		if err != nil {
			return err
		}
		ch, err := s.queueChat(ctx, tx, tenant, cur, "invoice:"+key)
		if err != nil {
			return err
		}
		if ch != "" {
			out.Queued = append(out.Queued, ch)
		}
		if err := audit(ctx, tx, tenant, by, "invoice.sent", cur.Number, map[string]any{"channels": out.Queued}); err != nil {
			return err
		}
		out.Invoice, err = s.Get(ctx, tx, tenant, cur.ID.String())
		return err
	})
	if err == nil && became {
		err = s.Paid(ctx, tenant, out.Invoice)
	}
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

// Naira formats kobo as naira for people: ₦25,000 or ₦25,000.50, and a
// negative amount −₦5,000.
func Naira(kobo int64) string {
	if kobo < 0 {
		return "−" + Naira(-kobo)
	}
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

// familySendText is a school's term invoice's message: the balance and the
// family page, where the fees, what was paid and how to pay are.
func familySendText(business string, x Invoice, link string) string {
	// Short: the SMS starts with the school's name, and ₦ makes every
	// character count double.
	var b strings.Builder
	if x.PaidKobo >= x.TotalKobo {
		fmt.Fprintf(&b, "Fees invoice %s: %s, paid from money you paid before. Nothing is due.", x.Number, Naira(x.TotalKobo))
	} else {
		fmt.Fprintf(&b, "Fees invoice %s: %s", x.Number, Naira(x.TotalKobo-x.PaidKobo))
		if x.PaidKobo > 0 {
			b.WriteString(" still to pay")
		}
		if x.DueOn != "" {
			if d, err := time.Parse(time.DateOnly, x.DueOn); err == nil {
				fmt.Fprintf(&b, " due %s", d.Format("2 Jan 2006"))
			}
		}
		b.WriteString(".")
	}
	if link != "" {
		b.WriteString(" See the fees, payments and how to pay: " + link)
	}
	_ = business
	return b.String()
}

func sendText(business string, x Invoice, link string) string {
	var b strings.Builder
	if x.PaidKobo >= x.TotalKobo {
		// Paid from the customer's credit as it was sent.
		fmt.Fprintf(&b, "Invoice %s from %s: %s, paid from money you paid before. Nothing is due.", x.Number, business, Naira(x.TotalKobo))
		if link != "" {
			b.WriteString(" See it: " + link)
		}
		return b.String()
	}
	fmt.Fprintf(&b, "Invoice %s from %s: %s", x.Number, business, Naira(x.TotalKobo-x.PaidKobo))
	if x.PaidKobo > 0 {
		b.WriteString(" still to pay")
	}
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
		case errors.Is(err, sender.ErrRecipientLimit):
			// The customer has had many messages this hour: this one is
			// left out, and the invoice is sent all the same.
		case err != nil:
			return nil, false, err
		case queued:
			channels = append(channels, "sms")
		}
	}
	if x.Customer.Email != "" {
		queued, err := s.Messages.Enqueue(ctx, tx, sender.Message{Tenant: tenant, Channel: sender.Email, Purpose: sender.PurposeLink,
			To: x.Customer.Email, SenderName: business, Subject: subject, Text: text, Key: key + ":email"})
		switch {
		case errors.Is(err, sender.ErrRecipientLimit):
		case err != nil:
			return nil, false, err
		case queued:
			channels = append(channels, "email")
		}
	}
	return channels, over, nil
}

// NotifyCustomer sends the invoice's customer a message: {link} becomes
// the invoice's page link, {number} its number, {due} what is due and
// {fee_line} a sentence on the bank's fee, in the business's language for
// its customers, when the rail said what it kept (Invoice.Fee), or nothing.
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
		if x.SentAt == nil && x.RecordedAt == nil {
			return fmt.Errorf("%w: invoice %s has not been sent", ErrState, x.Number)
		}
		// A notebook debt may have no phone or email yet: nothing to send
		// it to, which is not a failure (a receipt for cash on it).
		if x.Customer.Phone == "" && x.Customer.Email == "" {
			out["number"], out["queued"], out["sent"], out["reason"] = x.Number, []string{}, false, "no_address"
			return nil
		}
		if strings.Contains(text, "{link}") {
			t, err := s.token(ctx, tx, tenant, &x)
			if err != nil {
				return err
			}
			text = strings.ReplaceAll(text, "{link}", strings.TrimRight(s.PageBase, "/")+"/i/"+t)
		}
		var business string
		if err := tx.QueryRow(ctx, `SELECT name FROM tenants WHERE id = $1`, tenant).Scan(&business); err != nil {
			return err
		}
		feeLine := ""
		if strings.Contains(text, "{fee_line}") {
			if sent, fee, ok := x.Fee(); ok {
				feeLine = " " + lang.Default().Text(customerLanguage(ctx, tx, tenant), "invoice.receipt.fee_line",
					"sent", Naira(sent), "received", Naira(sent-fee), "business", business, "fee", Naira(fee))
			}
		}
		customerDue, customerLines := "", ""
		if strings.Contains(text, "{customer_due}") || strings.Contains(text, "{customer_lines}") {
			due, n, err := customerOverdue(ctx, tx, tenant, x.CustomerID())
			if err != nil {
				return err
			}
			customerDue, customerLines = Naira(due), strconv.Itoa(n)
		}
		// The customer's own payment link: one page of all they owe. Where
		// there is none to make (no pages, or nothing open), the invoice's.
		customerLink := ""
		if strings.Contains(text, "{customer_link}") {
			if customerLink, err = s.CustomerLink(ctx, tx, tenant, x.CustomerID(), "system:notify"); errors.Is(err, ErrState) || errors.Is(err, ErrInvalid) {
				err = nil
			}
			if err != nil {
				return err
			}
			if customerLink == "" && s.PageBase != "" {
				t, err := s.token(ctx, tx, tenant, &x)
				if err != nil {
					return err
				}
				customerLink = strings.TrimRight(s.PageBase, "/") + "/i/" + t
			}
		}
		text = strings.NewReplacer("{number}", x.Number, "{due}", Naira(x.DueKobo), "{fee_line}", feeLine,
			"{customer_due}", customerDue, "{customer_lines}", customerLines, "{customer_link}", customerLink).Replace(text)
		if subject == "" {
			subject = "Invoice " + x.Number + " from " + business
		}
		queued, over, err := s.deliver(ctx, tx, tenant, x, business, text, subject, "notice:"+key)
		if err != nil {
			return err
		}
		// A message about all the customer owes (a reminder per customer)
		// also goes on the chat app they asked for updates on.
		if customerLink != "" {
			ch, err := s.queueChatKind(ctx, tx, tenant, x, OutboxBalance, "notice:"+key)
			if err != nil {
				return err
			}
			if ch != "" {
				queued = append(queued, ch)
			}
		}
		out["number"], out["queued"], out["sent"] = x.Number, queued, len(queued) > 0
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
	// EntryID is the money log entry the payment counts (T5): a part of
	// it, counted towards the invoice once unless Add.
	EntryID    *uuid.UUID
	Connection string
	How        string // exact, suggested, suggested_auto or manual
	By         string
	Reasons    []string
	// Add is a further part of the entry towards an invoice it already
	// counts towards (a person splitting again): added to the first, a
	// row of its own (moneylog.Counting.Add).
	Add bool
}

// Counted is what counting a payment towards an invoice did. Pass the
// invoice to Paid once the transaction commits when Became.
type Counted struct {
	Invoice Invoice
	// PaymentID is the customer_invoice_payments row written (uuid.Nil
	// when the payment already counted towards the invoice).
	PaymentID uuid.UUID
	// Became is true when this payment made the invoice paid.
	Became bool
}

// RecordPayment counts a payment towards an invoice (CountPayment) and
// says whether that made it paid.
func (s *Invoices) RecordPayment(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, idOrNumber string, p PaymentIn) (Invoice, bool, error) {
	c, err := s.CountPayment(ctx, tx, tenant, idOrNumber, p)
	return c.Invoice, c.Became, err
}

// CountPayment counts a payment towards an invoice, once: the same
// payment again is a no-op, unless a further part of its entry is meant
// (Add). Matching (T5) and one-time accounts (T4) call
// it; nothing else marks an invoice paid. A payment with a money log
// entry is a part of it, counted along the path orders take too
// (moneylog.Parts.Count). When the invoice becomes paid, the runs of the
// workflows that start on it are queued in tx (once per invoice and
// workflow; StartPaid starts them), and a run waiting on it is signalled
// after the transaction commits (the caller's commit: call Paid with the
// invoice this returns).
func (s *Invoices) CountPayment(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, idOrNumber string, p PaymentIn) (Counted, error) {
	cur, err := s.Get(ctx, tx, tenant, idOrNumber)
	if err != nil {
		return Counted{}, err
	}
	if p.AmountKobo <= 0 || p.PaymentID == "" {
		return Counted{}, invalid("a payment needs an amount and its id")
	}
	switch p.How {
	case "exact", "suggested", "suggested_auto", "manual":
	default:
		return Counted{}, invalid("how is exact, suggested, suggested_auto or manual")
	}
	if cur.Status == "draft" {
		return Counted{}, fmt.Errorf("%w: invoice %s has not been sent", ErrState, cur.Number)
	}
	// One entry may count towards several invoices and orders (a split),
	// each part once, the parts never more than the entry counts for:
	// checked under the entry's lock, and by the database whoever writes
	// (00210).
	add := p.Add && p.EntryID != nil
	k, err := moneylog.InvoiceParts.Count(ctx, tx, moneylog.Counting{Tenant: tenant, Entry: p.EntryID, AmountKobo: p.AmountKobo, How: p.How, By: p.By, Reasons: p.Reasons, Add: add,
		Open:    func() (uuid.UUID, error) { return cur.ID, nil },
		Refused: func(why string) error { return fmt.Errorf("%w: %s", ErrState, why) },
		Decide: func() (moneylog.Cols, bool, error) {
			// The same payment counts towards an invoice once while it
			// is not undone; a further part of an entry adds to it.
			if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('taskiem invoice payment ' || $1::text || ' ' || $2, 0))`, cur.ID, p.PaymentID); err != nil {
				return nil, false, err
			}
			var counted bool
			if add {
				return moneylog.Cols{"payment_id": p.PaymentID, "connection": nullable(p.Connection)}, true, nil
			}
			if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM customer_invoice_payments p WHERE p.invoice_id = $1 AND p.payment_id = $2 AND p.undoes IS NULL
				AND NOT EXISTS (SELECT 1 FROM customer_invoice_payments u WHERE u.undoes = p.id))`, cur.ID, p.PaymentID).Scan(&counted); err != nil || counted {
				return nil, false, err
			}
			return moneylog.Cols{"payment_id": p.PaymentID, "connection": nullable(p.Connection)}, true, nil
		}})
	if err != nil {
		return Counted{}, err
	}
	if k.ID == uuid.Nil {
		return Counted{Invoice: cur}, nil
	}
	if err := audit(ctx, tx, tenant, p.By, "invoice.payment", cur.Number, map[string]any{"amount_kobo": p.AmountKobo, "payment_id": p.PaymentID, "how": p.How}); err != nil {
		return Counted{}, err
	}
	x, err := s.Get(ctx, tx, tenant, cur.ID.String())
	if err != nil {
		return Counted{}, err
	}
	became := x.Status == "paid" && cur.Status != "paid"
	out := Counted{Invoice: x, PaymentID: k.ID, Became: became}
	if s.Counted != nil {
		if err := s.Counted(ctx, tx, tenant, out, p); err != nil {
			return Counted{}, err
		}
	}
	// Workflows that start on a payment counted, and on an invoice being
	// paid: queued here, in the payment's transaction, so the payment and
	// its runs commit together (StartPayments and StartPaid start them).
	if err := queuePaymentStarts(ctx, tx, tenant, x.ID, k.ID); err != nil {
		return Counted{}, err
	}
	if became {
		if err := queuePaidStarts(ctx, tx, tenant, x.ID); err != nil {
			return Counted{}, err
		}
	}
	return out, nil
}

// UndoPayment undoes a counted payment (a wrong match): its row stays,
// and a row naming it takes it out of the invoice's total
// (moneylog.Parts.Undo). The money goes back to be matched (ReopenPart).
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
	part, undone, err := moneylog.InvoiceParts.Undo(ctx, tx, moneylog.Undoing{Tenant: tenant, Part: p.ID, By: by,
		Decide: func(moneylog.Part) (moneylog.Cols, error) {
			return moneylog.Cols{"payment_id": p.PaymentID, "connection": nullable(p.Connection)}, nil
		}})
	if errors.Is(err, moneylog.ErrNoPart) {
		return Invoice{}, ErrNotFound
	}
	if err != nil {
		return Invoice{}, err
	}
	if !undone {
		return s.Get(ctx, tx, tenant, cur.ID.String()) // undone meanwhile
	}
	if err := audit(ctx, tx, tenant, by, "invoice.payment_undone", cur.Number, map[string]any{"amount_kobo": p.AmountKobo, "payment_id": p.PaymentID}); err != nil {
		return Invoice{}, err
	}
	if err := ReopenPart(ctx, tx, tenant, moneylog.InvoiceParts, part); err != nil {
		return Invoice{}, err
	}
	x, err := s.Get(ctx, tx, tenant, cur.ID.String())
	if err != nil {
		return Invoice{}, err
	}
	if s.Undone != nil {
		if err := s.Undone(ctx, tx, tenant, x, p.ID, by); err != nil {
			return Invoice{}, err
		}
	}
	return x, nil
}

// ReopenPart puts the money of an undone part, of an invoice or an order,
// back to be matched (T5): the suggestion it came from is not offered
// again, and what its entry is now is decided again (ReopenEntry).
// Nothing for money with no entry.
func ReopenPart(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, parts moneylog.Parts, part moneylog.Part) error {
	if part.Entry == nil {
		return nil
	}
	if _, err := tx.Exec(ctx, `UPDATE money_suggestions SET status = 'rejected', decided_at = now() WHERE tenant_id = $1 AND `+parts.Suggestion+` = $2`, tenant, part.ID); err != nil {
		return err
	}
	return ReopenEntry(ctx, tx, tenant, *part.Entry)
}

// ReopenEntry decides what an entry is after one of its parts was undone.
// A customer's money (paid into their account, or left as their credit by
// a person) stays theirs while any of it counts, and what no longer counts
// is their credit; paid into their account, it stays theirs even when none
// does. Other money with parts still counted waits for a person for the
// rest (To match lists what is left). Money counting towards nothing goes
// back to be matched: its closed suggestions are offered again, and with
// none it waits for a person. ReopenPart calls it for an invoice's or an
// order's undone part.
func ReopenEntry(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, entry uuid.UUID) error {
	var how *string
	var counted int64
	if err := tx.QueryRow(ctx, `SELECT (SELECT how FROM money_entry_customers WHERE entry_id = $1), taskiem_money_entry_counted($1)`, entry).Scan(&how, &counted); err != nil {
		return err
	}
	switch {
	case how != nil && (*how == "account" || counted > 0):
		return nil
	case how != nil:
		// A person's credit with nothing counted: back to be matched.
		if _, err := tx.Exec(ctx, `DELETE FROM money_entry_customers WHERE tenant_id = $1 AND entry_id = $2`, tenant, entry); err != nil {
			return err
		}
	case counted > 0:
		// The rest, if any, waits for a person.
		_, err := tx.Exec(ctx, `INSERT INTO money_match_state (entry_id, tenant_id, state) VALUES ($1, $2,
			CASE WHEN COALESCE(taskiem_money_entry_left($1), 0) > 0 THEN 'unmatched' ELSE 'matched' END)
			ON CONFLICT (entry_id) DO UPDATE SET state = EXCLUDED.state, considered_at = now()`, entry, tenant)
		return err
	}
	return reopen(ctx, tx, tenant, &entry)
}

func reopen(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, entry *uuid.UUID) error {
	if _, err := tx.Exec(ctx, `UPDATE money_suggestions SET status = 'open', decided_at = NULL WHERE tenant_id = $1 AND entry_id = $2 AND status = 'closed'`, tenant, *entry); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `INSERT INTO money_match_state (entry_id, tenant_id, state) VALUES ($1, $2,
		CASE WHEN EXISTS (SELECT 1 FROM money_suggestions WHERE entry_id = $1 AND status = 'open') THEN 'suggested' ELSE 'unmatched' END)
		ON CONFLICT (entry_id) DO UPDATE SET state = EXCLUDED.state, considered_at = now()`, *entry, tenant)
	return err
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

// Listed is an invoice in a workflow's list: no customer details, which a
// run reaches through the invoice's number (notify_customer).
type Listed struct {
	Number    string     `json:"number"`
	Status    string     `json:"status"`
	TotalKobo int64      `json:"total_kobo"`
	DueKobo   int64      `json:"due_kobo"`
	DueOn     string     `json:"due_on,omitempty"`
	PaidAt    *time.Time `json:"paid_at,omitempty"`
	// DaysOverdue counts whole days past the due date, Lagos time (1 the
	// day after it); 0 for an invoice not overdue.
	DaysOverdue int `json:"days_overdue"`
}

// ListedCustomer is one customer with something overdue, for a workflow
// that reminds each customer once rather than once per invoice: an
// opaque id for the customer (never their details), the number of their
// oldest overdue invoice or debt (notify_customer reaches them through
// it), how many are overdue and what they owe on them, the oldest due
// date and the whole days since it, Lagos time.
type ListedCustomer struct {
	Customer    string `json:"customer"`
	Number      string `json:"number"`
	Lines       int    `json:"lines"`
	DueKobo     int64  `json:"due_kobo"`
	DueOn       string `json:"due_on"`
	DaysOverdue int    `json:"days_overdue"`
}

// ListCustomersOverdue lists the customers with overdue invoices or
// notebook debts, oldest due first: what one message per customer needs.
// A debt with no due date is never overdue, so never listed.
func (s Service) ListCustomersOverdue(ctx context.Context, tenant uuid.UUID, limit int) ([]ListedCustomer, error) {
	if limit <= 0 || limit > MaxListed {
		limit = 50
	}
	var out []ListedCustomer
	err := s.in(ctx, tenant, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT customer_id, (array_agg(number ORDER BY due_on, number))[1], count(*), sum(total_kobo - paid_kobo),
			to_char(min(due_on), 'YYYY-MM-DD'), ((now() AT TIME ZONE 'Africa/Lagos')::date - min(due_on))
			FROM customer_invoices WHERE tenant_id = $1 AND status IN ('recorded', 'sent', 'part_paid', 'overdue')
			AND due_on IS NOT NULL AND due_on < (now() AT TIME ZONE 'Africa/Lagos')::date
			GROUP BY customer_id ORDER BY min(due_on), customer_id LIMIT $2`, tenant, limit)
		if err != nil {
			return err
		}
		out, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (ListedCustomer, error) {
			var l ListedCustomer
			return l, r.Scan(&l.Customer, &l.Number, &l.Lines, &l.DueKobo, &l.DueOn, &l.DaysOverdue)
		})
		return err
	})
	if out == nil {
		out = []ListedCustomer{}
	}
	return out, err
}

// customerOverdue is what a customer owes on their overdue invoices and
// debts, and how many there are.
func customerOverdue(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, customer string) (int64, int, error) {
	var due int64
	var n int
	err := tx.QueryRow(ctx, `SELECT COALESCE(sum(total_kobo - paid_kobo), 0), count(*) FROM customer_invoices
		WHERE tenant_id = $1 AND customer_id = $2 AND status IN ('recorded', 'sent', 'part_paid', 'overdue')
		AND due_on IS NOT NULL AND due_on < (now() AT TIME ZONE 'Africa/Lagos')::date`, tenant, customer).Scan(&due, &n)
	return due, n, err
}

// MaxListed is the most invoices one list returns.
const MaxListed = 200

// MaxPaidWithin is how far back paid invoices can be listed.
const MaxPaidWithin = 7 * 24 * time.Hour

// ListFor lists a tenant's invoices for a workflow: status "overdue"
// (sent or part paid and past the due date, oldest due first) or "paid"
// (paid within the last within, oldest paid first). Overdue is computed
// from the date here as the status is, so an invoice the hourly check has
// not reached yet is included.
func (s Service) ListFor(ctx context.Context, tenant uuid.UUID, status string, within time.Duration, limit int) ([]Listed, error) {
	if limit <= 0 || limit > MaxListed {
		limit = 50
	}
	var q string
	var args []any
	switch status {
	case "overdue":
		q = `SELECT number, 'overdue', total_kobo, paid_kobo, to_char(due_on, 'YYYY-MM-DD'), paid_at,
			((now() AT TIME ZONE 'Africa/Lagos')::date - due_on)
			FROM customer_invoices WHERE tenant_id = $1 AND status IN ('recorded', 'sent', 'part_paid', 'overdue')
			AND due_on IS NOT NULL AND due_on < (now() AT TIME ZONE 'Africa/Lagos')::date
			ORDER BY due_on, number LIMIT $2`
		args = []any{tenant, limit}
	case "paid":
		if within <= 0 || within > MaxPaidWithin {
			return nil, invalid("paid invoices are listed for the last 1 to 168 hours")
		}
		q = `SELECT number, status, total_kobo, paid_kobo, COALESCE(to_char(due_on, 'YYYY-MM-DD'), ''), paid_at, 0
			FROM customer_invoices WHERE tenant_id = $1 AND status = 'paid' AND paid_at > now() - make_interval(secs => $3)
			ORDER BY paid_at, number LIMIT $2`
		args = []any{tenant, limit, within.Seconds()}
	default:
		return nil, invalid("list overdue or paid invoices")
	}
	var out []Listed
	err := s.in(ctx, tenant, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, q, args...)
		if err != nil {
			return err
		}
		out, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (Listed, error) {
			var l Listed
			var paid int64
			err := r.Scan(&l.Number, &l.Status, &l.TotalKobo, &paid, &l.DueOn, &l.PaidAt, &l.DaysOverdue)
			l.DueKobo = max(l.TotalKobo-paid, 0)
			return l, err
		})
		return err
	})
	if out == nil {
		out = []Listed{}
	}
	return out, err
}

// customerLanguage is the language the business speaks to its customers
// in: its default chat language (Settings > Languages), English when it
// has none (no-technical-steps N6), as the storefront's.
func customerLanguage(ctx context.Context, tx pgx.Tx, tenant uuid.UUID) lang.Tag {
	var def string
	if err := tx.QueryRow(ctx, `SELECT default_language FROM tenant_channel_settings WHERE tenant_id = $1`, tenant).Scan(&def); err == nil && lang.Valid(lang.Tag(def)) {
		return lang.Tag(def)
	}
	return lang.EN
}
