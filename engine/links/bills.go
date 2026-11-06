package links

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
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
	// UncountedKobo is what the bank confirmed the business sent from
	// Taskiem that the supplier's figure does not show yet (its match has
	// not paired it, or the mirror has not brought the count back): it is
	// not due again, so the bill is not paid twice (uncountedSQL).
	UncountedKobo int64 `json:"uncounted_kobo"`
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
	// Paid already, before this mirror (a bill.paid is said once).
	var wasPaid bool
	if err := tx.QueryRow(ctx, `SELECT COALESCE((SELECT paid_kobo >= amount_kobo AND NOT voided FROM bills WHERE tenant_id = $1 AND link_id = $2 AND source_id = $3), false)`,
		buyer, m.Link, m.Source).Scan(&wasPaid); err != nil {
		return false, err
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
	if err != nil || m.Voided {
		return inserted, err
	}
	// For workflows (L6): a bill received, a bill paid.
	body := map[string]any{"link": m.Link, "business": m.SupplierName, "reference": m.Reference, "amount_kobo": m.AmountKobo,
		"paid_kobo": m.PaidKobo, "due_kobo": max(m.AmountKobo-m.PaidKobo, 0), "due_on": m.DueOn}
	if inserted {
		if err := QueueEventTx(ctx, tx, buyer, EventBillReceived, m.Source.String(), body); err != nil {
			return inserted, err
		}
	}
	if !wasPaid && m.PaidKobo >= m.AmountKobo {
		if err := QueueEventTx(ctx, tx, buyer, EventBillPaid, m.Source.String(), body); err != nil {
			return inserted, err
		}
	}
	return inserted, nil
}

// uncountedSQL is a bill's UncountedKobo: the payments the bank
// confirmed that the supplier's match has not paired, and, of those it
// paired, what the supplier's figure (paid_kobo) does not show yet, as
// the mirror brings it later. Read FROM bills, unaliased.
const uncountedSQL = `(SELECT u + GREATEST(bills.sent_kobo - u - bills.paid_kobo, 0) FROM (SELECT COALESCE(sum(p.amount_kobo), 0)::bigint AS u
	FROM bill_payments p WHERE p.bill_id = bills.id AND p.status = 'paid' AND p.paired_at IS NULL) x)`

const billCols = `id, link_id, supplier_tenant, supplier_name, reference, lines, amount_kobo, paid_kobo, COALESCE(to_char(due_on, 'YYYY-MM-DD'), ''),
	status, allow_part_payments, disputed, declared_paid_kobo, paying_kobo, sent_kobo, ` + uncountedSQL + `, COALESCE(pay_account_number, ''), COALESCE(pay_bank_name, ''), COALESCE(pay_link, ''),
	mirrored_at, created_at`

func scanBill(r pgx.Row) (Bill, error) {
	var b Bill
	err := r.Scan(&b.ID, &b.LinkID, &b.Supplier, &b.SupplierName, &b.Reference, &b.Lines, &b.AmountKobo, &b.PaidKobo, &b.DueOn,
		&b.Status, &b.AllowParts, &b.Disputed, &b.DeclaredPaidKobo, &b.PayingKobo, &b.SentKobo, &b.UncountedKobo, &b.PayAccountNumber, &b.PayBankName, &b.PayLink, &b.MirroredAt, &b.CreatedAt)
	// What was sent and is not counted yet is not due again.
	if b.Status != "void" && b.AmountKobo > b.PaidKobo+b.UncountedKobo {
		b.DueKobo = b.AmountKobo - b.PaidKobo - b.UncountedKobo
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
	return ListBillsPageTx(ctx, tx, status, limit, 0)
}

// ListBillsPageTx is ListBillsTx from offset on: a page of the list.
func ListBillsPageTx(ctx context.Context, tx pgx.Tx, status string, limit, offset int) ([]Bill, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
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
		ORDER BY status IN ('paid', 'void'), due_on NULLS LAST, created_at DESC, id LIMIT $1 OFFSET $2`, limit, offset)
	if err != nil {
		return nil, err
	}
	out, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (Bill, error) { return scanBill(r) })
	if out == nil {
		out = []Bill{}
	}
	return out, err
}

// ErrAmbiguous: a reference two suppliers used for open bills; say which
// supplier ("Dangote INV-0042"), or use the bill's id.
var ErrAmbiguous = errors.New("links: more than one bill")

// GetBillTx is one of the business in scope's bills, by its id or its
// invoice's number, with the supplier's name before the number when two
// suppliers used the same number ("Dangote INV-0042"). A number alone
// that two suppliers' open bills share is refused with ErrAmbiguous,
// naming them; a closed bill (paid, cancelled) gives way to an open one,
// and the newest wins among closed ones.
func GetBillTx(ctx context.Context, tx pgx.Tx, idOrReference string) (Bill, error) {
	if id, err := uuid.Parse(strings.TrimSpace(idOrReference)); err == nil {
		b, err := scanBill(tx.QueryRow(ctx, `SELECT `+billCols+` FROM bills WHERE id = $1`, id))
		if errors.Is(err, pgx.ErrNoRows) {
			return b, ErrNotFound
		}
		return b, err
	}
	words := strings.Fields(idOrReference)
	if len(words) == 0 {
		return Bill{}, ErrNotFound
	}
	ref, supplier := strings.ToUpper(words[len(words)-1]), strings.Join(words[:len(words)-1], " ")
	rows, err := tx.Query(ctx, `SELECT `+billCols+` FROM bills WHERE reference = $1 AND ($2 = '' OR supplier_name ILIKE '%' || $2 || '%')
		ORDER BY status IN ('paid', 'void'), created_at DESC`, ref, supplier)
	if err != nil {
		return Bill{}, err
	}
	list, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (Bill, error) { return scanBill(r) })
	if err != nil {
		return Bill{}, err
	}
	if len(list) == 0 {
		return Bill{}, ErrNotFound
	}
	var suppliers []string
	for _, b := range list {
		if b.Status != "paid" && b.Status != "void" && !slices.Contains(suppliers, b.SupplierName) {
			suppliers = append(suppliers, b.SupplierName)
		}
	}
	if len(suppliers) > 1 {
		return Bill{}, fmt.Errorf("%w: %s is a bill from %s; say which supplier, as \"%s %s\"", ErrAmbiguous, ref, strings.Join(suppliers, " and "), suppliers[0], ref)
	}
	return list[0], nil
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
	// Status: pending (started, waiting to be approved, sent or confirmed),
	// paid (the bank confirmed), failed, rejected (not approved), or
	// unknown (the money went, and the workflow ended without saying how it
	// ended: the bill stays paying until a person looks).
	Status    string    `json:"status"`
	CreatedBy string    `json:"created_by"`
	CreatedAt time.Time `json:"created_at"`
	// SentAt is when the rail took the money (the payout step answered
	// pending or settled); PairedAt when the supplier's matching paired it
	// with its credit, or the business confirmed the pair.
	SentAt     *time.Time `json:"sent_at,omitempty"`
	PairedAt   *time.Time `json:"paired_at,omitempty"`
	FinishedAt *time.Time `json:"finished_at,omitempty"`
	// SupplierName is the bill's supplier.
	SupplierName string `json:"supplier_name,omitempty"`
}

const paymentCols = `p.id, p.bill_id, b.reference, b.supplier_name, p.amount_kobo, p.token, p.connection, COALESCE(p.account_name, ''), p.run_id, p.status,
	p.created_by, p.created_at, p.sent_at, p.paired_at, p.finished_at`

func scanPayment(r pgx.Row) (BillPayment, error) {
	var p BillPayment
	err := r.Scan(&p.ID, &p.BillID, &p.Reference, &p.SupplierName, &p.AmountKobo, &p.Token, &p.Connection, &p.AccountName, &p.RunID, &p.Status,
		&p.CreatedBy, &p.CreatedAt, &p.SentAt, &p.PairedAt, &p.FinishedAt)
	return p, err
}

// PaymentsOfBillTx are a bill's payments from Taskiem, newest first.
func PaymentsOfBillTx(ctx context.Context, tx pgx.Tx, bill uuid.UUID) ([]BillPayment, error) {
	rows, err := tx.Query(ctx, `SELECT `+paymentCols+` FROM bill_payments p JOIN bills b ON b.id = p.bill_id WHERE p.bill_id = $1 ORDER BY p.created_at DESC`, bill)
	if err != nil {
		return nil, err
	}
	out, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (BillPayment, error) { return scanPayment(r) })
	if out == nil {
		out = []BillPayment{}
	}
	return out, err
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
// rejected; what the bank confirmed sent and the supplier has not counted
// yet is not due again, so the bill is not paid twice before its count
// comes back. The token, the bill's reference and a short code, is what the
// payout's narration carries.
func StartBillPaymentTx(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, in PayIn) (BillPayment, error) {
	var b struct {
		ref                   string
		due, paying, sent     int64
		status                string
		parts, disputed, void bool
	}
	err := tx.QueryRow(ctx, `SELECT reference, amount_kobo - paid_kobo, paying_kobo, `+uncountedSQL+`, status, allow_part_payments, disputed, voided
		FROM bills WHERE id = $1 FOR UPDATE`, in.Bill).
		Scan(&b.ref, &b.due, &b.paying, &b.sent, &b.status, &b.parts, &b.disputed, &b.void)
	if errors.Is(err, pgx.ErrNoRows) {
		return BillPayment{}, ErrNotFound
	}
	if err != nil {
		return BillPayment{}, err
	}
	left := b.due - b.sent
	switch {
	case b.void || b.status == "paid":
		return BillPayment{}, fmt.Errorf("%w: the bill is %s", ErrState, map[bool]string{true: "cancelled", false: "paid"}[b.void])
	case b.paying > 0:
		return BillPayment{}, fmt.Errorf("%w: a payment of this bill is already under way", ErrState)
	case b.sent > 0 && left <= 0:
		return BillPayment{}, fmt.Errorf("%w: you sent %d kobo for this bill that the supplier has not counted yet; confirm the payment was for this bill instead of paying again", ErrState, b.sent)
	case b.disputed:
		return BillPayment{}, fmt.Errorf("%w: the bill is queried; settle the query with the supplier first", ErrState)
	case in.AmountKobo <= 0 || in.AmountKobo > left:
		return BillPayment{}, fmt.Errorf("%w: pay up to what is left, %d kobo", ErrInvalid, left)
	case in.AmountKobo < left && !b.parts:
		return BillPayment{}, fmt.Errorf("%w: the supplier takes this bill paid in full", ErrInvalid)
	}
	p := BillPayment{ID: uuid.Must(uuid.NewV7()), BillID: in.Bill, Reference: b.ref, AmountKobo: in.AmountKobo, Connection: in.Connection,
		Status: "pending", CreatedBy: in.By}
	// The code is short, so a token may clash with an earlier payment's:
	// another is drawn, a few times.
	for try := 0; ; try++ {
		p.Token = b.ref + "/" + strings.ToUpper(newCode()[:4])
		sp, err := tx.Begin(ctx)
		if err != nil {
			return BillPayment{}, err
		}
		err = sp.QueryRow(ctx, `INSERT INTO bill_payments (id, tenant_id, bill_id, amount_kobo, token, connection, bank_code, account_number, created_by)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9) RETURNING created_at`,
			p.ID, tenant, p.BillID, p.AmountKobo, p.Token, p.Connection, in.BankCode, in.AccountNumber, p.CreatedBy).Scan(&p.CreatedAt)
		if err == nil {
			if err := sp.Commit(ctx); err != nil {
				return BillPayment{}, err
			}
			break
		}
		_ = sp.Rollback(ctx)
		var pe *pgconn.PgError
		if !errors.As(err, &pe) || pe.Code != "23505" || try >= 5 {
			return BillPayment{}, err
		}
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
// supplier's match does), failed, rejected (not approved), or unknown (the
// workflow ended after the money went without saying how: the bill stays
// paying until a person looks). The bill is no longer paying after any
// other outcome. The same outcome twice changes nothing; a paid, failed or
// rejected payment never changes, an unknown one takes paid or failed when
// it is known.
func FinishBillPaymentTx(ctx context.Context, tx pgx.Tx, payment uuid.UUID, status, accountName string) (BillPayment, error) {
	if status != "paid" && status != "failed" && status != "rejected" && status != "unknown" {
		return BillPayment{}, fmt.Errorf("%w: status is paid, failed, rejected or unknown", ErrInvalid)
	}
	p, err := scanPayment(tx.QueryRow(ctx, `SELECT `+paymentCols+` FROM bill_payments p JOIN bills b ON b.id = p.bill_id WHERE p.id = $1 FOR UPDATE OF p`, payment))
	if errors.Is(err, pgx.ErrNoRows) {
		return p, ErrNotFound
	}
	if err != nil || p.Status == status || (p.Status != "pending" && (p.Status != "unknown" || status == "rejected")) {
		return p, err
	}
	was := p.Status
	// Paid after all: the owner may be told again, of a pair not made.
	if err := tx.QueryRow(ctx, `UPDATE bill_payments SET status = $2, account_name = COALESCE(NULLIF($3, ''), account_name), finished_at = now(),
		sent_at = CASE WHEN $2 IN ('paid', 'unknown') THEN COALESCE(sent_at, now()) ELSE sent_at END,
		alerted_at = CASE WHEN $2 = 'paid' THEN NULL ELSE alerted_at END
		WHERE id = $1 RETURNING finished_at, sent_at`, payment, status, accountName).Scan(&p.FinishedAt, &p.SentAt); err != nil {
		return p, err
	}
	p.Status = status
	if status == "unknown" {
		return p, nil // the bill stays paying
	}
	sent := int64(0)
	if status == "paid" {
		sent = p.AmountKobo
	}
	if was == "unknown" && status == "failed" {
		// Nothing was sent after all.
		_, err = tx.Exec(ctx, `UPDATE bills SET paying_kobo = GREATEST(paying_kobo - $2, 0) WHERE id = $1`, p.BillID, p.AmountKobo)
		return p, err
	}
	_, err = tx.Exec(ctx, `UPDATE bills SET paying_kobo = GREATEST(paying_kobo - $2, 0), sent_kobo = sent_kobo + $3 WHERE id = $1`, p.BillID, p.AmountKobo, sent)
	return p, err
}

// MarkPayoutSentTx records that the rail took the money of the payment a
// run is paying (the payout step answered pending or settled), with the
// rail's payout id (payout_ref, the reference of the payout's money-log
// entry, whose description is the payout's narration, the bill's token),
// in the transaction that records the step: from here the payment is
// never failed by a sweep, whatever becomes of the run.
func MarkPayoutSentTx(ctx context.Context, tx pgx.Tx, tenant, run uuid.UUID, payoutRef string) error {
	_, err := tx.Exec(ctx, `UPDATE bill_payments SET sent_at = COALESCE(sent_at, now()), payout_ref = COALESCE(NULLIF($3, ''), payout_ref)
		WHERE tenant_id = $1 AND run_id = $2 AND status IN ('pending', 'unknown')`, tenant, run, payoutRef)
	return err
}

// SettleStalePaymentsTx settles the payments of the business in scope
// whose workflow ended without recording them: a run failed or cancelled
// before the money went (no sent_at), or no run started within ten
// minutes, is failed, and the bill is no longer paying. A run that ended
// after the money went makes the payment unknown: the bill stays paying,
// and the payments turned unknown are answered, for the owner to be told.
func SettleStalePaymentsTx(ctx context.Context, tx pgx.Tx) ([]BillPayment, error) {
	rows, err := tx.Query(ctx, `SELECT p.id, p.sent_at IS NOT NULL FROM bill_payments p LEFT JOIN runs r ON r.id = p.run_id
		WHERE p.status = 'pending' AND (r.status IN ('failed', 'cancelled') OR (p.run_id IS NULL AND p.created_at < now() - interval '10 minutes'))`)
	if err != nil {
		return nil, err
	}
	type stale struct {
		id   uuid.UUID
		sent bool
	}
	list, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (stale, error) {
		var x stale
		return x, r.Scan(&x.id, &x.sent)
	})
	if err != nil {
		return nil, err
	}
	var unknown []BillPayment
	for _, x := range list {
		status := "failed"
		if x.sent {
			status = "unknown"
		}
		p, err := FinishBillPaymentTx(ctx, tx, x.id, status, "")
		if err != nil {
			return nil, err
		}
		if x.sent {
			unknown = append(unknown, p)
		}
	}
	return unknown, nil
}

// UnpairedPayoutsTx are the business in scope's payouts the bank confirmed
// three days ago or more that no match has paired (§5.3), each answered
// once: they are marked told, and the owner is told of each.
func UnpairedPayoutsTx(ctx context.Context, tx pgx.Tx) ([]BillPayment, error) {
	rows, err := tx.Query(ctx, `UPDATE bill_payments p SET alerted_at = now() FROM bills b WHERE b.id = p.bill_id
		AND p.status = 'paid' AND p.paired_at IS NULL AND p.alerted_at IS NULL AND p.sent_at IS NOT NULL AND p.sent_at < now() - interval '3 days'
		RETURNING `+paymentCols)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (BillPayment, error) { return scanPayment(r) })
}

// UnknownPaymentsTx are the business in scope's payments whose outcome is
// unknown and whose owner has not been told, each answered once.
func UnknownPaymentsTx(ctx context.Context, tx pgx.Tx) ([]BillPayment, error) {
	rows, err := tx.Query(ctx, `UPDATE bill_payments p SET alerted_at = now() FROM bills b WHERE b.id = p.bill_id
		AND p.status = 'unknown' AND p.alerted_at IS NULL RETURNING `+paymentCols)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (BillPayment, error) { return scanPayment(r) })
}

// ConfirmPairTx is the business's own word that its payout paid the bill
// it was made for (§5.3), when the supplier's matching paired nothing in
// three days: the payment is marked paired, and the supplier is told,
// through the one door, which invoice the money was for, so its side can
// re-aim its match where the two disagree.
func ConfirmPairTx(ctx context.Context, tx pgx.Tx, buyer uuid.UUID, bill, payment string, by Actor) (BillPayment, Notice, uuid.UUID, error) {
	b, err := GetBillTx(ctx, tx, bill)
	if err != nil {
		return BillPayment{}, Notice{}, uuid.Nil, err
	}
	id, err := uuid.Parse(strings.TrimSpace(payment))
	if err != nil {
		return BillPayment{}, Notice{}, uuid.Nil, fmt.Errorf("%w: a payment's id", ErrInvalid)
	}
	p, err := scanPayment(tx.QueryRow(ctx, `SELECT `+paymentCols+` FROM bill_payments p JOIN bills b ON b.id = p.bill_id WHERE p.id = $1 AND p.bill_id = $2 FOR UPDATE OF p`, id, b.ID))
	if errors.Is(err, pgx.ErrNoRows) {
		return p, Notice{}, uuid.Nil, ErrNotFound
	}
	if err != nil {
		return p, Notice{}, uuid.Nil, err
	}
	switch {
	case p.PairedAt != nil:
		return p, Notice{}, uuid.Nil, fmt.Errorf("%w: the payment is paired already", ErrState)
	case p.Status != "paid" && p.Status != "unknown":
		return p, Notice{}, uuid.Nil, fmt.Errorf("%w: the bank has not confirmed this payment", ErrState)
	}
	if err := tx.QueryRow(ctx, `UPDATE bill_payments SET paired_at = now() WHERE id = $1 RETURNING paired_at`, p.ID).Scan(&p.PairedAt); err != nil {
		return p, Notice{}, uuid.Nil, err
	}
	amount := p.AmountKobo
	n, seller, err := tellOther(ctx, tx, buyer, b.LinkID, Notice{Kind: "paired", Reference: b.Reference, AmountKobo: &amount, Note: p.Token}, "link.pair.confirmed", by, nil)
	return p, n, seller, err
}

// Payments records bill payments for the bills connector, each in the
// calling run's business.
type Payments struct{ Pool *pgxpool.Pool }

// PayoutSent is the runtime's word, in the transaction that records a
// payout step's completion, that the rail took the money of the payment
// run is paying (runtime.Store.PayoutSent).
func (p *Payments) PayoutSent(ctx context.Context, tx pgx.Tx, tenant, run uuid.UUID, payoutRef string) error {
	return MarkPayoutSentTx(ctx, tx, tenant, run, payoutRef)
}

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
