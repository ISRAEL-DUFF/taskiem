package schools

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/invoicing"
	"github.com/israel-duff/taskiem/engine/pii"
	"github.com/israel-duff/taskiem/engine/whatsapp"
)

// Receipts (docs/spec/schools.md 9, B5; docs/schools.md#receipts): one
// receipt for every payment part counted towards a school's term invoice,
// however it was counted (a credit to the student's own account, a
// suggestion confirmed, a match by hand, a split part, cash posted at the
// desk). It is issued in the transaction that counts the part
// (invoicing.Invoices.Counted, wired by WireReceipts), once per part (the
// part is the key), numbered per business and term from a counter locked
// by its row (RCT-2026T1-0079: the session's first year, the term's
// number in it, the sequence), and voided, never deleted, when the part
// is undone (invoicing.Invoices.Undone) or its money went back
// (matching.Matcher.Reversed).
//
// Each receipt has a token of its own, unguessable and unrelated to its
// number: the QR on the PDF and the link a template sends open
// /r/<token> on the public host, which answers Valid or Voided, the
// school, the amount and the date, and nothing else. The number alone
// opens nothing, so the school's sequence cannot be walked.

// Receipt is a receipt as the pages show it.
type Receipt struct {
	ID               uuid.UUID `json:"id"`
	Number           string    `json:"number"`
	TermID           uuid.UUID `json:"term_id"`
	Term             string    `json:"term"`
	StudentID        uuid.UUID `json:"student_id"`
	AdmissionNumber  string    `json:"admission_number"`
	Name             string    `json:"name"`
	ClassID          uuid.UUID `json:"class_id"`
	Class            string    `json:"class"`
	InvoiceID        uuid.UUID `json:"invoice_id"`
	InvoiceNumber    string    `json:"invoice_number"`
	PartID           uuid.UUID `json:"part_id"`
	AmountKobo       int64     `json:"amount_kobo"`
	BalanceAfterKobo int64     `json:"balance_after_kobo"`
	// FeeKobo is the bank's fee on a transfer, when the rail reported one:
	// the payer sent AmountKobo and the school received that less the fee
	// (decision 0052: the partner's fee is passed to the payer).
	FeeKobo    int64      `json:"fee_kobo,omitempty"`
	How        string     `json:"how"`
	PaidAt     time.Time  `json:"paid_at"`
	IssuedBy   string     `json:"issued_by"`
	IssuedAt   time.Time  `json:"issued_at"`
	Voided     bool       `json:"voided"`
	VoidedAt   *time.Time `json:"voided_at,omitempty"`
	VoidReason string     `json:"void_reason,omitempty"`
	// Link is the public verification page (empty when the server has
	// no public host).
	Link string `json:"link,omitempty"`
}

// WireReceipts makes inv issue a receipt for every part counted towards
// one of the school's invoices and void it when the part is undone.
func WireReceipts(inv *invoicing.Invoices, s *Schools) {
	inv.Counted = s.IssueReceipt
	inv.Undone = s.VoidReceipt
	// A school's invoices carry the family page's link (B6): in the
	// message an invoice is sent with, and in the payment event's body.
	inv.FamilyLink = s.FamilyLinkForInvoice
}

// IssueReceipt is the Invoices.Counted hook: for a part counted towards
// a school's term invoice, a receipt, once.
func (s *Schools) IssueReceipt(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, k invoicing.Counted, p invoicing.PaymentIn) error {
	if k.PaymentID == uuid.Nil {
		return nil
	}
	var term, student, class uuid.UUID
	err := tx.QueryRow(ctx, `SELECT term_id, student_id, class_id FROM school_term_invoices WHERE tenant_id = $1 AND invoice_id = $2`, tenant, k.Invoice.ID).Scan(&term, &student, &class)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil // not a school's invoice
	}
	if err != nil {
		return err
	}
	var exists bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM school_receipts WHERE part_id = $1)`, k.PaymentID).Scan(&exists); err != nil || exists {
		return err
	}
	// The number: the session's first year, the term's place in it, and
	// the next of the term's counter, under its row's lock.
	var year string
	var nth int
	if err := tx.QueryRow(ctx, `SELECT left(ss.label, 4), (SELECT count(*) FROM school_terms o WHERE o.session_id = t.session_id AND (o.starts_on, o.id) <= (t.starts_on, t.id))::int
		FROM school_terms t JOIN school_sessions ss ON ss.id = t.session_id WHERE t.id = $1`, term).Scan(&year, &nth); err != nil {
		return err
	}
	var seq int
	if err := tx.QueryRow(ctx, `INSERT INTO school_receipt_counters (tenant_id, term_id, next) VALUES ($1, $2, 2)
		ON CONFLICT (tenant_id, term_id) DO UPDATE SET next = school_receipt_counters.next + 1 RETURNING next - 1`, tenant, term).Scan(&seq); err != nil {
		return err
	}
	number := fmt.Sprintf("RCT-%sT%d-%04d", year, nth, seq)
	paidAt := s.now()
	if p.EntryID != nil {
		var when *time.Time
		if err := tx.QueryRow(ctx, `SELECT COALESCE(occurred_at, recorded_at) FROM money_entries WHERE id = $1`, *p.EntryID).Scan(&when); err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		if when != nil {
			paidAt = *when
		}
	}
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return err
	}
	token := base64.RawURLEncoding.EncodeToString(b)
	// Sealed under the student's subject, as their details are: erasing
	// the student ends their receipts' links too.
	var env map[string]any
	if err := tx.QueryRow(ctx, `SELECT details FROM school_students WHERE tenant_id = $1 AND id = $2`, tenant, student).Scan(&env); err != nil {
		return err
	}
	subject, _ := env["subject"].(string)
	if subject == "" {
		return errors.New("schools: the student's details are not sealed")
	}
	sealed, err := s.Vault.SealUnderTx(ctx, tx, tenant, subject, "other", token)
	if err != nil {
		return err
	}
	balance := max(k.Invoice.TotalKobo-k.Invoice.PaidKobo, 0)
	if _, err := tx.Exec(ctx, `INSERT INTO school_receipts (id, tenant_id, number, term_id, student_id, class_id, invoice_id, part_id, amount_kobo, balance_after_kobo, how, paid_at, token_hash, token, issued_by)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15) ON CONFLICT (part_id) DO NOTHING`,
		newID(), tenant, number, term, student, class, k.Invoice.ID, k.PaymentID, p.AmountKobo, balance, p.How, paidAt, invoicing.ReceiptTokenHash(token), sealed, p.By); err != nil {
		return err
	}
	// A payment counted for the student answers an "I have paid" claim of
	// theirs (claims.go).
	return s.claimsCounted(ctx, tx, tenant, student, k.PaymentID, p.AmountKobo, p.By)
}

// VoidReceipt is the Invoices.Undone hook: the part's receipt, if any,
// no longer stands.
func (s *Schools) VoidReceipt(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, _ invoicing.Invoice, part uuid.UUID, by string) error {
	reason := "the payment was undone"
	if by == "system:reversal" {
		reason = "the money went back to the payer"
	}
	return voidReceipts(ctx, tx, tenant, []uuid.UUID{part}, reason)
}

// ReceiptsReversed is the matcher's Reversed hook: the receipts of parts
// undone because their money went back are voided.
func (s *Schools) ReceiptsReversed(ctx context.Context, tx pgx.Tx, tenant, _ uuid.UUID, parts []uuid.UUID) error {
	return voidReceipts(ctx, tx, tenant, parts, "the money went back to the payer")
}

func voidReceipts(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, parts []uuid.UUID, reason string) error {
	if len(parts) == 0 {
		return nil
	}
	_, err := tx.Exec(ctx, `UPDATE school_receipts SET voided_at = now(), void_reason = $3 WHERE tenant_id = $1 AND part_id = ANY ($2) AND voided_at IS NULL`, tenant, parts, reason)
	return err
}

// --- reading ---

const receiptCols = `r.id, r.number, r.term_id, t.name || ' ' || ss.label, r.student_id, s.admission_number, s.details,
	r.class_id, c.name || CASE WHEN c.arm = '' THEN '' ELSE ' ' || c.arm END, r.invoice_id, i.number, r.part_id, r.amount_kobo, r.balance_after_kobo, r.how, r.paid_at,
	r.issued_by, r.issued_at, r.voided_at, COALESCE(r.void_reason, ''), r.token,
	COALESCE((SELECT e.fee_kobo FROM customer_invoice_payments cp JOIN money_entries e ON e.id = cp.entry_id WHERE cp.id = r.part_id), 0)
	FROM school_receipts r JOIN school_terms t ON t.id = r.term_id JOIN school_sessions ss ON ss.id = t.session_id JOIN school_students s ON s.id = r.student_id
	JOIN school_classes c ON c.id = r.class_id JOIN customer_invoices i ON i.id = r.invoice_id`

func (s *Schools) scanReceipts(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, rows pgx.Rows, link bool) ([]Receipt, error) {
	type row struct {
		r       Receipt
		student map[string]any
		token   map[string]any
	}
	list, err := pgx.CollectRows(rows, func(cr pgx.CollectableRow) (row, error) {
		var x row
		err := cr.Scan(&x.r.ID, &x.r.Number, &x.r.TermID, &x.r.Term, &x.r.StudentID, &x.r.AdmissionNumber, &x.student, &x.r.ClassID, &x.r.Class, &x.r.InvoiceID, &x.r.InvoiceNumber,
			&x.r.PartID, &x.r.AmountKobo, &x.r.BalanceAfterKobo, &x.r.How, &x.r.PaidAt, &x.r.IssuedBy, &x.r.IssuedAt, &x.r.VoidedAt, &x.r.VoidReason, &x.token, &x.r.FeeKobo)
		return x, err
	})
	if err != nil {
		return nil, err
	}
	out := make([]Receipt, 0, len(list))
	for _, x := range list {
		r := x.r
		r.Voided = r.VoidedAt != nil
		var d studentSealed
		erased, err := s.open(ctx, tx, tenant, x.student, &d)
		if err != nil {
			return nil, err
		}
		r.Name = d.Name
		if erased {
			r.Name = Erased
		}
		if link && !erased && s.Invoices != nil && s.Invoices.PageBase != "" {
			v, err := s.Vault.OpenTx(ctx, tx, tenant, x.token)
			if err != nil && !errors.Is(err, pii.ErrErased) {
				return nil, err
			}
			if t, ok := v.(string); ok && t != "" {
				r.Link = invoicing.ReceiptLink(s.Invoices.PageBase, t)
			}
		}
		out = append(out, r)
	}
	return out, nil
}

// receiptOfPart is a part's receipt, voided or not.
func (s *Schools) receiptOfPart(ctx context.Context, tx pgx.Tx, tenant, part uuid.UUID) (Receipt, bool, error) {
	rows, err := tx.Query(ctx, `SELECT `+receiptCols+` WHERE r.tenant_id = $1 AND r.part_id = $2`, tenant, part)
	if err != nil {
		return Receipt{}, false, err
	}
	list, err := s.scanReceipts(ctx, tx, tenant, rows, false)
	if err != nil || len(list) == 0 {
		return Receipt{}, false, err
	}
	return list[0], true, nil
}

// ReceiptFilter narrows the receipts listed.
type ReceiptFilter struct {
	Student *uuid.UUID
	Term    *uuid.UUID
	Invoice *uuid.UUID
	// Number finds one receipt by its number (case does not matter).
	Number string
	Limit  int
}

// Receipts lists receipts, newest first, with their links.
func (s *Schools) Receipts(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, f ReceiptFilter) ([]Receipt, error) {
	if err := need(ctx, tx, tenant); err != nil {
		return nil, err
	}
	if f.Limit <= 0 || f.Limit > 500 {
		f.Limit = 100
	}
	rows, err := tx.Query(ctx, `SELECT `+receiptCols+` WHERE r.tenant_id = $1 AND ($2::uuid IS NULL OR r.student_id = $2) AND ($3::uuid IS NULL OR r.term_id = $3)
		AND ($4::uuid IS NULL OR r.invoice_id = $4) AND ($5 = '' OR upper(r.number) = upper($5))
		ORDER BY r.issued_at DESC, r.number DESC LIMIT $6`, tenant, f.Student, f.Term, f.Invoice, strings.TrimSpace(f.Number), f.Limit)
	if err != nil {
		return nil, err
	}
	list, err := s.scanReceipts(ctx, tx, tenant, rows, true)
	if list == nil {
		list = []Receipt{}
	}
	return list, err
}

// ReceiptByID is one receipt by its id or number, with its link.
func (s *Schools) ReceiptByID(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, idOrNumber string) (Receipt, error) {
	if err := need(ctx, tx, tenant); err != nil {
		return Receipt{}, err
	}
	where, arg := `upper(r.number) = upper($2)`, any(strings.TrimSpace(idOrNumber))
	if id, err := uuid.Parse(idOrNumber); err == nil {
		where, arg = `r.id = $2`, id
	}
	rows, err := tx.Query(ctx, `SELECT `+receiptCols+` WHERE r.tenant_id = $1 AND `+where, tenant, arg)
	if err != nil {
		return Receipt{}, err
	}
	list, err := s.scanReceipts(ctx, tx, tenant, rows, true)
	if err != nil {
		return Receipt{}, err
	}
	if len(list) == 0 {
		return Receipt{}, ErrNotFound
	}
	return list[0], nil
}

// --- sending a receipt to the family ---

// ReceiptWhat is what a receipt paid, for the family: the invoice's
// number and what it is for.
func ReceiptWhat(r Receipt) string { return r.InvoiceNumber + " (school fees)" }

// ReceiptRest is what is left on the invoice after the receipt, in one
// sentence.
func ReceiptRest(r Receipt) string {
	if r.BalanceAfterKobo > 0 {
		return invoicing.Naira(r.BalanceAfterKobo) + " is still left on it."
	}
	return "The invoice is paid in full."
}

// ReceiptWho is whom the receipt's payment was for, as its message names
// them: the student, or their admission number once their name is erased.
func ReceiptWho(r Receipt) string {
	if r.Name == "" || r.Name == Erased {
		return r.AdmissionNumber
	}
	return r.Name
}

// ReceiptVars are the taskiem_receipt template's variables
// (docs/whatsapp.md#templates): the number, the amount, who it is for,
// what it paid and what is left, with the receipt's own check link for
// the button (the family page's link is not what the button opens).
func ReceiptVars(r Receipt) map[string]string {
	return map[string]string{"number": r.Number, "amount": invoicing.Naira(r.AmountKobo), "who": ReceiptWho(r), "what": ReceiptWhat(r), "rest": ReceiptRest(r), "link": r.Link}
}

// ReceiptText is the message a family gets for a receipt, inside the
// window: the number, the amount and the invoice, what is left, the check
// link, and the family page's link, where the receipt and its PDF are.
func ReceiptText(r Receipt, familyLink string) string {
	s := fmt.Sprintf("Receipt %s: we received %s towards %s. %s", r.Number, invoicing.Naira(r.AmountKobo), ReceiptWhat(r), ReceiptRest(r))
	if r.FeeKobo > 0 && r.FeeKobo < r.AmountKobo {
		s += fmt.Sprintf(" You sent %s; %s reached the school after the bank's fee of %s.", invoicing.Naira(r.AmountKobo),
			invoicing.Naira(r.AmountKobo-r.FeeKobo), invoicing.Naira(r.FeeKobo))
	}
	if r.Link != "" {
		s += " Check the receipt: " + r.Link
	}
	if familyLink != "" {
		s += " Your fees page, with the receipt and its PDF: " + familyLink
	}
	return s
}

// ReceiptShort is the SMS: the same facts with the naira written NGN, so
// the message stays in the SMS alphabet (docs/messages.md), and the two
// links.
func ReceiptShort(r Receipt, familyLink string) string {
	s := fmt.Sprintf("Receipt %s: %s towards %s. %s", r.Number, invoicing.Naira(r.AmountKobo), r.InvoiceNumber, ReceiptRest(r))
	if r.Link != "" {
		s += " Check it: " + r.Link
	}
	if familyLink != "" {
		s += " Fees page: " + familyLink
	}
	return strings.ReplaceAll(s, "₦", "NGN")
}

// ReceiptSent is what sending a receipt did.
type ReceiptSent struct {
	Receipt Receipt    `json:"receipt"`
	Sent    FamilySent `json:"sent"`
}

// SendReceipt sends the student's family a receipt, in tx: on each
// guardian's chat app (as taskiem_receipt outside WhatsApp's window, the
// check link on its button) or by SMS, with a fresh family page link,
// exactly once per receipt whoever asks (the receipt is the key), so the
// school-receipt template's retry sends nothing twice. A voided receipt
// is refused, and a student in no family reaches nobody.
func (s *Schools) SendReceipt(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, r Receipt, by string) (FamilySent, error) {
	if r.Voided {
		return FamilySent{}, refused("receipt %s is voided: %s", r.Number, r.VoidReason)
	}
	var family *uuid.UUID
	if err := tx.QueryRow(ctx, `SELECT family_id FROM school_students WHERE tenant_id = $1 AND id = $2`, tenant, r.StudentID).Scan(&family); err != nil {
		return FamilySent{}, err
	}
	if family == nil {
		return FamilySent{Channels: map[string]int{}, Nobody: true}, nil
	}
	return s.SendFamily(ctx, tx, tenant, *family, FamilyMessage{Purpose: "receipt", Key: "receipt:" + r.ID.String(),
		Text:     func(link string) string { return ReceiptText(r, link) },
		Short:    func(link string) string { return ReceiptShort(r, link) },
		Template: &whatsapp.TplReceipt,
		Vars:     func(string) map[string]string { return ReceiptVars(r) }}, by)
}

// SendReceiptOnce sends a receipt (by id or number) to the family in a
// transaction of its own (SendReceipt).
func (s Service) SendReceiptOnce(ctx context.Context, tenant uuid.UUID, idOrNumber, by string) (ReceiptSent, error) {
	var out ReceiptSent
	err := s.In(ctx, tenant, func(tx pgx.Tx) error {
		var err error
		if out.Receipt, err = s.ReceiptByID(ctx, tx, tenant, idOrNumber); err != nil {
			return err
		}
		out.Sent, err = s.SendReceipt(ctx, tx, tenant, out.Receipt, by)
		return err
	})
	return out, err
}

// HowWords is how a receipt's payment was counted, for people.
func HowWords(how string) string {
	switch how {
	case "exact":
		return "Paid into the student's account"
	case "suggested", "suggested_auto":
		return "Matched to the invoice"
	case "manual":
		return "Received by the school"
	}
	return how
}
