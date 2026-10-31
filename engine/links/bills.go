package links

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Bills (L1, docs/spec/business-links.md §6): the buyer's copy of a linked
// supplier's invoice. The seller's figures (number, lines, total, due
// date, what its match counted paid, void) are mirrored from the invoice
// and never edited on the buyer's side; disputed and declared_paid_kobo
// are the buyer's own.

// KeyPrefix starts the customer key of a linked business on its
// supplier's invoices: link:<the buyer's business>.
const KeyPrefix = "link:"

// BuyerOf is the business a customer key names, when it is a link key.
func BuyerOf(key string) (uuid.UUID, bool) {
	rest, ok := strings.CutPrefix(key, KeyPrefix)
	if !ok {
		return uuid.Nil, false
	}
	id, err := uuid.Parse(rest)
	return id, err == nil
}

// Bill is a bill as the buyer sees it.
type Bill struct {
	ID               uuid.UUID       `json:"id"`
	LinkID           uuid.UUID       `json:"link"`
	Supplier         uuid.UUID       `json:"supplier"`
	SupplierName     string          `json:"supplier_name"`
	Reference        string          `json:"reference"`
	Lines            json.RawMessage `json:"lines"`
	AmountKobo       int64           `json:"amount_kobo"`
	PaidKobo         int64           `json:"paid_kobo"`
	DueKobo          int64           `json:"due_kobo"`
	DueOn            string          `json:"due_on,omitempty"`
	Status           string          `json:"status"`
	AllowParts       bool            `json:"allow_part_payments"`
	Disputed         bool            `json:"disputed"`
	DeclaredPaidKobo int64           `json:"declared_paid_kobo"`
	// The way to pay, the seller's: the account number it issued us, and
	// the invoice's page.
	PayAccountNumber string    `json:"pay_account_number,omitempty"`
	PayBankName      string    `json:"pay_bank_name,omitempty"`
	PayLink          string    `json:"pay_link,omitempty"`
	MirroredAt       time.Time `json:"mirrored_at"`
	CreatedAt        time.Time `json:"created_at"`
}

// Mirror is what a bill takes from its invoice.
type Mirror struct {
	Link         uuid.UUID
	Supplier     uuid.UUID
	SupplierName string
	Source       uuid.UUID
	Reference    string
	Lines        json.RawMessage
	AmountKobo   int64
	PaidKobo     int64
	DueOn        string // YYYY-MM-DD, or ""
	Voided       bool
	AllowParts   bool
	// The way to pay: the account the seller issued the buyer, and the
	// invoice's page.
	PayAccountNumber, PayBankName, PayLink string
}

// MirrorBillTx writes a bill from its invoice into the business in scope
// (the buyer's, entered through EnterTx). It reports whether the bill is
// new.
func MirrorBillTx(ctx context.Context, tx pgx.Tx, buyer uuid.UUID, m Mirror) (bool, error) {
	var due *string
	if m.DueOn != "" {
		due = &m.DueOn
	}
	null := func(s string) *string {
		if s == "" {
			return nil
		}
		return &s
	}
	var inserted bool
	err := tx.QueryRow(ctx, `INSERT INTO bills (id, tenant_id, link_id, supplier_tenant, supplier_name, source_id, reference, lines, amount_kobo, paid_kobo,
			due_on, voided, allow_part_payments, pay_account_number, pay_bank_name, pay_link)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11::date, $12, $13, $14, $15, $16)
		ON CONFLICT (tenant_id, link_id, source_id) DO UPDATE SET supplier_name = EXCLUDED.supplier_name, reference = EXCLUDED.reference,
			lines = EXCLUDED.lines, amount_kobo = EXCLUDED.amount_kobo, paid_kobo = EXCLUDED.paid_kobo, due_on = EXCLUDED.due_on,
			voided = EXCLUDED.voided, allow_part_payments = EXCLUDED.allow_part_payments, pay_account_number = EXCLUDED.pay_account_number,
			pay_bank_name = EXCLUDED.pay_bank_name, pay_link = EXCLUDED.pay_link, mirrored_at = now()
		RETURNING xmax = 0`,
		uuid.Must(uuid.NewV7()), buyer, m.Link, m.Supplier, m.SupplierName, m.Source, m.Reference, m.Lines, m.AmountKobo, m.PaidKobo,
		due, m.Voided, m.AllowParts, null(m.PayAccountNumber), null(m.PayBankName), null(m.PayLink)).Scan(&inserted)
	return inserted, err
}

const billCols = `id, link_id, supplier_tenant, supplier_name, reference, lines, amount_kobo, paid_kobo, COALESCE(to_char(due_on, 'YYYY-MM-DD'), ''),
	status, allow_part_payments, disputed, declared_paid_kobo, COALESCE(pay_account_number, ''), COALESCE(pay_bank_name, ''), COALESCE(pay_link, ''),
	mirrored_at, created_at`

func scanBill(r pgx.Row) (Bill, error) {
	var b Bill
	err := r.Scan(&b.ID, &b.LinkID, &b.Supplier, &b.SupplierName, &b.Reference, &b.Lines, &b.AmountKobo, &b.PaidKobo, &b.DueOn,
		&b.Status, &b.AllowParts, &b.Disputed, &b.DeclaredPaidKobo, &b.PayAccountNumber, &b.PayBankName, &b.PayLink, &b.MirroredAt, &b.CreatedAt)
	if b.Status != "void" && b.AmountKobo > b.PaidKobo {
		b.DueKobo = b.AmountKobo - b.PaidKobo
	}
	return b, err
}

// BillStatuses a list may be filtered by; "open" is every bill still to
// pay (open, part-paid, disputed).
var BillStatuses = []string{"open", "paid", "void", "all"}

// ListBillsTx is the business in scope's bills, those still to pay first
// by due date, then the rest newest first. status: open, paid, void or
// all.
func ListBillsTx(ctx context.Context, tx pgx.Tx, status string, limit int) ([]Bill, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	where := `TRUE`
	switch status {
	case "open", "":
		where = `status IN ('open', 'paying', 'part_paid', 'disputed')`
	case "paid":
		where = `status = 'paid'`
	case "void":
		where = `status = 'void'`
	}
	rows, err := tx.Query(ctx, `SELECT `+billCols+` FROM bills WHERE `+where+`
		ORDER BY status IN ('paid', 'void'), due_on NULLS LAST, created_at DESC LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	out, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (Bill, error) { return scanBill(r) })
	if out == nil {
		out = []Bill{}
	}
	return out, err
}

// GetBillTx is one of the business in scope's bills, by its id or its
// invoice's number (the newest when two suppliers used the same number).
func GetBillTx(ctx context.Context, tx pgx.Tx, idOrReference string) (Bill, error) {
	q := `SELECT ` + billCols + ` FROM bills WHERE reference = $1 ORDER BY created_at DESC LIMIT 1`
	var arg any = strings.ToUpper(strings.TrimSpace(idOrReference))
	if id, err := uuid.Parse(idOrReference); err == nil {
		q, arg = `SELECT `+billCols+` FROM bills WHERE id = $1`, id
	}
	b, err := scanBill(tx.QueryRow(ctx, q, arg))
	if errors.Is(err, pgx.ErrNoRows) {
		return b, ErrNotFound
	}
	return b, err
}
