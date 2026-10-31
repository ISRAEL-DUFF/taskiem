package links

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/israel-duff/taskiem/engine/db"
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
	// PayingKobo is a payment from Taskiem under way; SentKobo what the
	// bank confirmed the business sent from Taskiem (its own record).
	PayingKobo int64 `json:"paying_kobo"`
	SentKobo   int64 `json:"sent_kobo"`
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
	status, allow_part_payments, disputed, declared_paid_kobo, paying_kobo, sent_kobo, COALESCE(pay_account_number, ''), COALESCE(pay_bank_name, ''), COALESCE(pay_link, ''),
	mirrored_at, created_at`

func scanBill(r pgx.Row) (Bill, error) {
	var b Bill
	err := r.Scan(&b.ID, &b.LinkID, &b.Supplier, &b.SupplierName, &b.Reference, &b.Lines, &b.AmountKobo, &b.PaidKobo, &b.DueOn,
		&b.Status, &b.AllowParts, &b.Disputed, &b.DeclaredPaidKobo, &b.PayingKobo, &b.SentKobo, &b.PayAccountNumber, &b.PayBankName, &b.PayLink, &b.MirroredAt, &b.CreatedAt)
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

// BillPayment is a payment of a bill from Taskiem (§6.2).
type BillPayment struct {
	ID          uuid.UUID  `json:"id"`
	BillID      uuid.UUID  `json:"bill"`
	Reference   string     `json:"reference"`
	AmountKobo  int64      `json:"amount_kobo"`
	Token       string     `json:"token"`
	Connection  string     `json:"connection"`
	AccountName string     `json:"account_name,omitempty"`
	RunID       *uuid.UUID `json:"run_id,omitempty"`
	Status      string     `json:"status"`
	CreatedBy   string     `json:"created_by"`
	CreatedAt   time.Time  `json:"created_at"`
	FinishedAt  *time.Time `json:"finished_at,omitempty"`
}

// PayIn is a payment of a bill being started.
type PayIn struct {
	Bill          uuid.UUID
	AmountKobo    int64
	Connection    string
	BankCode      string
	AccountNumber string
	By            string
}

// StartBillPaymentTx records a payment of a bill under way and marks the
// bill paying, so a second Pay is refused until it is settled, refused or
// rejected. The token, the bill's reference and a short code, is what the
// payout's narration carries.
func StartBillPaymentTx(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, in PayIn) (BillPayment, error) {
	var b struct {
		ref                   string
		due, paying           int64
		status                string
		parts, disputed, void bool
	}
	err := tx.QueryRow(ctx, `SELECT reference, amount_kobo - paid_kobo, paying_kobo, status, allow_part_payments, disputed, voided FROM bills WHERE id = $1 FOR UPDATE`, in.Bill).
		Scan(&b.ref, &b.due, &b.paying, &b.status, &b.parts, &b.disputed, &b.void)
	if errors.Is(err, pgx.ErrNoRows) {
		return BillPayment{}, ErrNotFound
	}
	if err != nil {
		return BillPayment{}, err
	}
	switch {
	case b.void || b.status == "paid":
		return BillPayment{}, fmt.Errorf("%w: the bill is %s", ErrState, map[bool]string{true: "cancelled", false: "paid"}[b.void])
	case b.paying > 0:
		return BillPayment{}, fmt.Errorf("%w: a payment of this bill is already under way", ErrState)
	case b.disputed:
		return BillPayment{}, fmt.Errorf("%w: the bill is queried; settle the query with the supplier first", ErrState)
	case in.AmountKobo <= 0 || in.AmountKobo > b.due:
		return BillPayment{}, fmt.Errorf("%w: pay up to what is left, %d kobo", ErrInvalid, b.due)
	case in.AmountKobo < b.due && !b.parts:
		return BillPayment{}, fmt.Errorf("%w: the supplier takes this bill paid in full", ErrInvalid)
	}
	p := BillPayment{ID: uuid.Must(uuid.NewV7()), BillID: in.Bill, Reference: b.ref, AmountKobo: in.AmountKobo, Connection: in.Connection,
		Token: b.ref + "/" + strings.ToUpper(newCode()[:4]), Status: "pending", CreatedBy: in.By}
	if err := tx.QueryRow(ctx, `INSERT INTO bill_payments (id, tenant_id, bill_id, amount_kobo, token, connection, bank_code, account_number, created_by)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9) RETURNING created_at`,
		p.ID, tenant, p.BillID, p.AmountKobo, p.Token, p.Connection, in.BankCode, in.AccountNumber, p.CreatedBy).Scan(&p.CreatedAt); err != nil {
		return BillPayment{}, err
	}
	_, err = tx.Exec(ctx, `UPDATE bills SET paying_kobo = paying_kobo + $2 WHERE id = $1`, in.Bill, in.AmountKobo)
	return p, err
}

// SetPaymentRunTx records the run paying a payment.
func SetPaymentRunTx(ctx context.Context, tx pgx.Tx, payment, run uuid.UUID) error {
	_, err := tx.Exec(ctx, `UPDATE bill_payments SET run_id = $2 WHERE id = $1`, payment, run)
	return err
}

// FinishBillPaymentTx records how a payment went: paid (the bank
// confirmed; the business sent the money, and the bill counts it when the
// supplier's match does), failed or rejected. The bill is no longer
// paying. The same outcome twice changes nothing; a finished payment
// never changes.
func FinishBillPaymentTx(ctx context.Context, tx pgx.Tx, payment uuid.UUID, status, accountName string) (BillPayment, error) {
	if status != "paid" && status != "failed" && status != "rejected" {
		return BillPayment{}, fmt.Errorf("%w: status is paid, failed or rejected", ErrInvalid)
	}
	var p BillPayment
	err := tx.QueryRow(ctx, `SELECT p.id, p.bill_id, b.reference, p.amount_kobo, p.token, p.connection, COALESCE(p.account_name, ''), p.run_id, p.status,
		p.created_by, p.created_at, p.finished_at FROM bill_payments p JOIN bills b ON b.id = p.bill_id WHERE p.id = $1 FOR UPDATE OF p`, payment).
		Scan(&p.ID, &p.BillID, &p.Reference, &p.AmountKobo, &p.Token, &p.Connection, &p.AccountName, &p.RunID, &p.Status, &p.CreatedBy, &p.CreatedAt, &p.FinishedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return p, ErrNotFound
	}
	if err != nil || p.Status != "pending" {
		return p, err
	}
	if err := tx.QueryRow(ctx, `UPDATE bill_payments SET status = $2, account_name = COALESCE(NULLIF($3, ''), account_name), finished_at = now()
		WHERE id = $1 RETURNING finished_at`, payment, status, accountName).Scan(&p.FinishedAt); err != nil {
		return p, err
	}
	p.Status = status
	sent := int64(0)
	if status == "paid" {
		sent = p.AmountKobo
	}
	_, err = tx.Exec(ctx, `UPDATE bills SET paying_kobo = GREATEST(paying_kobo - $2, 0), sent_kobo = sent_kobo + $3 WHERE id = $1`, p.BillID, p.AmountKobo, sent)
	return p, err
}

// SettleStalePaymentsTx marks failed the payments of the business in scope
// whose run ended without recording them (failed, cancelled), so their
// bills are no longer paying.
func SettleStalePaymentsTx(ctx context.Context, tx pgx.Tx) error {
	rows, err := tx.Query(ctx, `SELECT p.id FROM bill_payments p JOIN runs r ON r.id = p.run_id
		WHERE p.status = 'pending' AND r.status IN ('failed', 'cancelled')`)
	if err != nil {
		return err
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
	if err != nil {
		return err
	}
	for _, id := range ids {
		if _, err := FinishBillPaymentTx(ctx, tx, id, "failed", ""); err != nil {
			return err
		}
	}
	return nil
}

// Payments records bill payments for the bills connector, each in the
// calling run's business.
type Payments struct{ Pool *pgxpool.Pool }

// FinishBillPayment is FinishBillPaymentTx in a transaction of tenant's.
func (p *Payments) FinishBillPayment(ctx context.Context, tenant, payment uuid.UUID, status, accountName string) (BillPayment, error) {
	var out BillPayment
	err := db.InTenantTx(ctx, p.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		var err error
		out, err = FinishBillPaymentTx(ctx, tx, payment, status, accountName)
		return err
	})
	return out, err
}

// bankWords are set aside when bank names are compared.
var bankWords = map[string]bool{"bank": true, "plc": true, "limited": true, "ltd": true, "nigeria": true, "of": true, "the": true}

func bankKey(name string) string {
	var out []string
	for _, w := range strings.FieldsFunc(strings.ToLower(name), func(r rune) bool { return (r < 'a' || r > 'z') && (r < '0' || r > '9') }) {
		if !bankWords[w] {
			out = append(out, w)
		}
	}
	return strings.Join(out, " ")
}

// BankCode finds a bank by name in a payments connection's list of banks
// (each a map with a name and a code, under the keys the rails use): the
// one whose name is the same once "Bank", "PLC", "Limited" and punctuation
// are set aside. Two matches, or none, find nothing.
func BankCode(banks []map[string]any, name string) (string, bool) {
	want := bankKey(name)
	if want == "" {
		return "", false
	}
	str := func(m map[string]any, keys ...string) string {
		for _, k := range keys {
			if v, ok := m[k]; ok && v != nil {
				return strings.TrimSpace(fmt.Sprint(v))
			}
		}
		return ""
	}
	code := ""
	for _, b := range banks {
		if bankKey(str(b, "name", "bank_name", "bankName")) != want {
			continue
		}
		c := str(b, "code", "bank_code", "bankCode", "nip_code", "nipCode")
		if c == "" || code != "" && code != c {
			return "", false
		}
		code = c
	}
	return code, code != ""
}

// Notice is what one side of a link tells the other about a document.
type Notice struct {
	ID         uuid.UUID `json:"id"`
	Link       uuid.UUID `json:"link"`
	From       string    `json:"from"`
	Kind       string    `json:"kind"` // declared, queried, resolved
	Reference  string    `json:"reference"`
	AmountKobo *int64    `json:"amount_kobo,omitempty"`
	How        string    `json:"how,omitempty"`
	Note       string    `json:"note,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
}

// putNoticeTx writes a notice into the business in scope: the other side,
// entered through EnterTx.
func putNoticeTx(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, n *Notice) error {
	n.ID = uuid.Must(uuid.NewV7())
	null := func(s string) *string {
		if s == "" {
			return nil
		}
		return &s
	}
	return tx.QueryRow(ctx, `INSERT INTO link_notices (id, tenant_id, link_id, from_name, kind, reference, amount_kobo, how, note)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9) RETURNING created_at`,
		n.ID, tenant, n.Link, n.From, n.Kind, n.Reference, n.AmountKobo, null(n.How), null(n.Note)).Scan(&n.CreatedAt)
}

// DeclarePaidTx records the buyer's own word that it paid a bill outside
// Taskiem (§6.2): declared_paid_kobo, never the seller's paid_kobo. The
// seller is told, through the one door: the transaction's last writes.
func DeclarePaidTx(ctx context.Context, tx pgx.Tx, buyer uuid.UUID, bill string, amountKobo int64, how, note string, by Actor) (Bill, Notice, uuid.UUID, error) {
	if amountKobo <= 0 || (how != "transfer" && how != "cash" && how != "other") || len(note) > 500 {
		return Bill{}, Notice{}, uuid.Nil, fmt.Errorf("%w: an amount, and how: transfer, cash or other", ErrInvalid)
	}
	b, err := GetBillTx(ctx, tx, bill)
	if err != nil {
		return b, Notice{}, uuid.Nil, err
	}
	if b.Status == "void" {
		return b, Notice{}, uuid.Nil, fmt.Errorf("%w: the supplier cancelled this invoice", ErrState)
	}
	if _, err := tx.Exec(ctx, `UPDATE bills SET declared_paid_kobo = declared_paid_kobo + $2 WHERE id = $1`, b.ID, amountKobo); err != nil {
		return b, Notice{}, uuid.Nil, err
	}
	b.DeclaredPaidKobo += amountKobo
	n, seller, err := tellOther(ctx, tx, buyer, b.LinkID, Notice{Kind: "declared", Reference: b.Reference, AmountKobo: &amountKobo, How: how, Note: note},
		"link.bill.declared", by, nil)
	return b, n, seller, err
}

// QueryBillTx marks a bill queried (§8) and flags the seller's invoice so
// its oldest-first matching passes over it, telling the seller, through
// the one door.
func QueryBillTx(ctx context.Context, tx pgx.Tx, buyer uuid.UUID, bill, reason string, by Actor) (Bill, Notice, uuid.UUID, error) {
	reason = strings.TrimSpace(reason)
	if reason == "" || len(reason) > 500 {
		return Bill{}, Notice{}, uuid.Nil, fmt.Errorf("%w: say what is wrong with the bill (up to 500 characters)", ErrInvalid)
	}
	b, err := GetBillTx(ctx, tx, bill)
	if err != nil {
		return b, Notice{}, uuid.Nil, err
	}
	if b.Status == "void" || b.Status == "paid" {
		return b, Notice{}, uuid.Nil, fmt.Errorf("%w: the bill is %s", ErrState, map[bool]string{true: "cancelled", false: "paid"}[b.Status == "void"])
	}
	if b.Disputed {
		return b, Notice{}, uuid.Nil, fmt.Errorf("%w: the bill is queried already", ErrState)
	}
	if err := tx.QueryRow(ctx, `UPDATE bills SET disputed = true WHERE id = $1 RETURNING status`, b.ID).Scan(&b.Status); err != nil {
		return b, Notice{}, uuid.Nil, err
	}
	b.Disputed = true
	var source uuid.UUID
	if err := tx.QueryRow(ctx, `SELECT source_id FROM bills WHERE id = $1`, b.ID).Scan(&source); err != nil {
		return b, Notice{}, uuid.Nil, err
	}
	n, seller, err := tellOther(ctx, tx, buyer, b.LinkID, Notice{Kind: "queried", Reference: b.Reference, Note: reason}, "link.bill.queried", by,
		func(tx pgx.Tx) error {
			_, err := tx.Exec(ctx, `UPDATE customer_invoices SET disputed = true WHERE id = $1`, source)
			return err
		})
	return b, n, seller, err
}

// ResolveQueryTx settles a buyer's query on an invoice, as its seller
// (§8): the invoice is matched oldest-first again, and the buyer's bill is
// no longer queried, through the one door.
func ResolveQueryTx(ctx context.Context, tx pgx.Tx, seller, invoice uuid.UUID, reference, buyerKey, note string, by Actor) (Notice, uuid.UUID, error) {
	buyer, ok := BuyerOf(buyerKey)
	if !ok {
		return Notice{}, uuid.Nil, fmt.Errorf("%w: the invoice is not to a linked business", ErrState)
	}
	tag, err := tx.Exec(ctx, `UPDATE customer_invoices SET disputed = false WHERE id = $1 AND disputed`, invoice)
	if err != nil {
		return Notice{}, uuid.Nil, err
	}
	if tag.RowsAffected() == 0 {
		return Notice{}, uuid.Nil, fmt.Errorf("%w: the invoice is not queried", ErrState)
	}
	list, _, err := ListTx(ctx, tx)
	if err != nil {
		return Notice{}, uuid.Nil, err
	}
	link := uuid.Nil
	for _, l := range list {
		if l.Business == buyer && l.State == Active {
			link = l.ID
		}
	}
	if link == uuid.Nil {
		return Notice{}, buyer, nil // the link ended: the bill stays as it was
	}
	n, _, err := tellOther(ctx, tx, seller, link, Notice{Kind: "resolved", Reference: reference, Note: strings.TrimSpace(note)}, "link.bill.resolved", by,
		func(tx pgx.Tx) error {
			_, err := tx.Exec(ctx, `UPDATE bills SET disputed = false WHERE link_id = $1 AND source_id = $2`, link, invoice)
			return err
		})
	return n, buyer, err
}

// tellOther enters the other side of link and writes the notice there,
// after then (a write of that event into the other side's own rows).
// The caller's own writes come first: the scope is the other side's
// from here on.
func tellOther(ctx context.Context, tx pgx.Tx, me, link uuid.UUID, n Notice, action string, by Actor, then func(pgx.Tx) error) (Notice, uuid.UUID, error) {
	if err := tx.QueryRow(ctx, `SELECT name FROM tenants WHERE id = $1`, me).Scan(&n.From); err != nil {
		return n, uuid.Nil, err
	}
	detail, _ := json.Marshal(map[string]any{"reference": n.Reference})
	other, err := EnterTx(ctx, tx, link, action, detail, by)
	if err != nil {
		return n, uuid.Nil, err
	}
	n.Link = link
	if then != nil {
		if err := then(tx); err != nil {
			return n, other, err
		}
	}
	return n, other, putNoticeTx(ctx, tx, other, &n)
}
