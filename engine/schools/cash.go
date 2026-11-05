package schools

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

	"github.com/israel-duff/taskiem/engine/access"
	"github.com/israel-duff/taskiem/engine/invoicing"
	"github.com/israel-duff/taskiem/engine/matching"
	"github.com/israel-duff/taskiem/engine/moneylog"
	"github.com/israel-duff/taskiem/engine/notices"
)

// Cash at the desk, discounts and the second person (docs/spec/schools.md
// 8, B5; docs/schools.md#cash-and-transfers). A bursar posts money a
// family paid in cash, by transfer to the school's own account or by POS:
// it becomes a money log entry (cash, statement or pos_sale, entered)
// that is the student's money, counted towards their open term invoices
// oldest first along the one path a payment into their own account
// takes (matching.Matcher.CountCustomer), what is left their credit, and
// a receipt per part (receipts.go).
//
// At or above the school's cash threshold the posting is pending: a row
// of school_pending_postings and nothing else, until someone with
// school.approve who is not the requester approves it (then it is posted
// exactly once, under the row's lock) or declines it with a reason. A
// standing discount or a discount amendment at or above the discount
// threshold, and a term invoice held for such a discount, wait the same
// way, and are applied on approval. So does money a person entered on
// Money, matched by hand to an invoice at or above the cash threshold
// (KindCashMatch, recorded by engine/matching): approval makes the match.
//
// Both thresholds are weighed per student, not per request, so that
// splitting does not pass under them: cash with the money a person
// entered (desk, entered, uploaded) that already counts towards the
// student's invoices without a second person (matching.EnteredUnapproved),
// a discount or a reduction with the reductions of the student's
// invoices and the standing discounts given them without one
// (ReducedUnapproved), each within the window: since the current term
// was started, and at least the last 24 hours (matching.SchoolWindowSQL).
// What a second person approved is theirs and is not weighed again; a
// rail's money is never counted.

// Kinds of request.
const (
	KindCashPosting = "cash_posting"
	// KindCashMatch is money a person entered on Money (cash, or a
	// statement line), matched by hand to an invoice at or above the cash
	// threshold (matching.Held).
	KindCashMatch         = matching.RequestKind
	KindDiscount          = "discount"
	KindDiscountAmendment = "discount_amendment"
	KindTermInvoice       = "term_invoice"
)

// States of a request.
const (
	StatePending  = "pending"
	StatePosted   = "posted"
	StateDeclined = "declined"
)

// Ways money is paid at the desk.
var PaymentHows = []string{"cash", "transfer", "pos", "other"}

// ReasonPosted is the reason every part of a posting gives.
const ReasonPosted = "posted at the school's desk"

// PostingIn is money a family paid at the desk.
type PostingIn struct {
	// Student is the student's id or admission number.
	Student    string `json:"student"`
	AmountKobo int64  `json:"amount_kobo"`
	How        string `json:"how"`
	// Date is when it was paid (YYYY-MM-DD); today when empty.
	Date string `json:"date,omitempty"`
	Note string `json:"note,omitempty"`
	// Key makes a retry the same posting (the request's Idempotency-Key).
	Key string `json:"-"`
}

// PostedPart is one invoice a posting paid, with its receipt.
type PostedPart struct {
	InvoiceID     uuid.UUID `json:"invoice_id"`
	Number        string    `json:"number"`
	AmountKobo    int64     `json:"amount_kobo"`
	ReceiptID     uuid.UUID `json:"receipt_id,omitempty"`
	ReceiptNumber string    `json:"receipt_number,omitempty"`
}

// PostingResult is what an approved or direct posting did.
type PostingResult struct {
	EntryID    *uuid.UUID   `json:"entry_id,omitempty"`
	Parts      []PostedPart `json:"parts,omitempty"`
	CreditKobo int64        `json:"credit_kobo,omitempty"`
	// DiscountID, AmendmentID and InvoiceID are what the other kinds made.
	DiscountID    *uuid.UUID `json:"discount_id,omitempty"`
	AmendmentID   *uuid.UUID `json:"amendment_id,omitempty"`
	InvoiceID     *uuid.UUID `json:"invoice_id,omitempty"`
	InvoiceNumber string     `json:"invoice_number,omitempty"`
}

// Posting is a request as the pages and the approvals inbox show it: a
// cash posting (pending, posted or declined), or a discount, a discount
// amendment or a held term invoice waiting for a second person.
type Posting struct {
	ID              uuid.UUID      `json:"id"`
	Kind            string         `json:"kind"`
	Status          string         `json:"status"`
	StudentID       *uuid.UUID     `json:"student_id,omitempty"`
	AdmissionNumber string         `json:"admission_number,omitempty"`
	Name            string         `json:"name,omitempty"`
	Class           string         `json:"class,omitempty"`
	InvoiceID       *uuid.UUID     `json:"invoice_id,omitempty"`
	InvoiceNumber   string         `json:"invoice_number,omitempty"`
	TermID          *uuid.UUID     `json:"term_id,omitempty"`
	AmountKobo      int64          `json:"amount_kobo"`
	Details         map[string]any `json:"details"`
	RequestedBy     string         `json:"requested_by"`
	RequestedUser   *uuid.UUID     `json:"requested_user,omitempty"`
	RequestedEmail  string         `json:"requested_email,omitempty"`
	RequestedAt     time.Time      `json:"requested_at"`
	DecidedBy       string         `json:"decided_by,omitempty"`
	DecidedEmail    string         `json:"decided_email,omitempty"`
	DecidedAt       *time.Time     `json:"decided_at,omitempty"`
	DecisionReason  string         `json:"decision_reason,omitempty"`
	Result          *PostingResult `json:"result,omitempty"`
	// Yours: the caller asked for it, so they cannot decide it (set by the
	// API for the inbox).
	Yours bool `json:"yours,omitempty"`
	// Created is true when PostPayment made this posting in the call that
	// returned it, false for the same key answered again.
	Created bool `json:"-"`
}

// ApprovalNeeded is what a request at or above a threshold answers: the
// request was recorded and waits for a second person (it unwraps to
// ErrNeedsApproval).
type ApprovalNeeded struct {
	Request Posting
}

func (e *ApprovalNeeded) Error() string {
	return fmt.Sprintf("schools: needs approval: %s of %s waits for a second person's approval", kindWords[e.Request.Kind], invoicing.Naira(e.Request.AmountKobo))
}

func (e *ApprovalNeeded) Unwrap() error { return ErrNeedsApproval }

var kindWords = map[string]string{KindCashPosting: "a cash posting", KindCashMatch: "a match of money entered by hand", KindDiscount: "a discount", KindDiscountAmendment: "a discount on an invoice", KindTermInvoice: "a term invoice with a discount"}

// KindWords is a request's kind in plain words.
func KindWords(kind string) string { return kindWords[kind] }

// --- the cash threshold ---

// CashThreshold is the school's cash threshold in kobo: nil when unset
// (no posting needs a second person), 0 when every posting does.
func CashThreshold(ctx context.Context, tx pgx.Tx, tenant uuid.UUID) (*int64, error) {
	var k *int64
	err := tx.QueryRow(ctx, `SELECT cash_threshold_kobo FROM school_settings WHERE tenant_id = $1`, tenant).Scan(&k)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return k, err
}

// SetCashThreshold sets it (the owner's setting): nil turns it off, 0
// makes every posting wait. It reports the threshold before.
func SetCashThreshold(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, kobo *int64, by string) (*int64, error) {
	if kobo != nil && (*kobo < 0 || *kobo > invoicing.MaxTotalKobo) {
		return nil, invalid("the threshold is an amount in naira, 0 for every posting, or empty for none")
	}
	if err := need(ctx, tx, tenant); err != nil {
		return nil, err
	}
	was, err := CashThreshold(ctx, tx, tenant)
	if err != nil {
		return nil, err
	}
	_, err = tx.Exec(ctx, `UPDATE school_settings SET cash_threshold_kobo = $2, updated_by = $3, updated_at = now() WHERE tenant_id = $1`, tenant, kobo, by)
	return was, err
}

// --- posting ---

func (s *Schools) matcher() *matching.Matcher {
	if s.Matcher != nil {
		return s.Matcher
	}
	return &matching.Matcher{Invoices: s.Invoices, PII: s.Vault, Logger: s.Logger}
}

// userOf is the person behind an actor string (a user's id), if any.
func userOf(by string) *uuid.UUID {
	if id, err := uuid.Parse(by); err == nil {
		return &id
	}
	return nil
}

// PostPayment posts money a family paid at the desk for a student, or,
// at or above the school's cash threshold, records it as pending and
// returns ApprovalNeeded. The same key again answers the same posting.
// Once this returns a posted Posting, call PostedAfter.
func (s *Schools) PostPayment(ctx context.Context, tenant uuid.UUID, in PostingIn, by string) (Posting, []matching.CountedPart, error) {
	if s.Invoices == nil {
		return Posting{}, nil, refused("invoices are not available on this server")
	}
	var out Posting
	var parts []matching.CountedPart
	var pending bool
	err := s.In(ctx, tenant, func(tx pgx.Tx) error {
		parts, pending = nil, false
		if err := need(ctx, tx, tenant); err != nil {
			return err
		}
		if in.Key != "" {
			var id uuid.UUID
			err := tx.QueryRow(ctx, `SELECT id FROM school_pending_postings WHERE tenant_id = $1 AND key = $2`, tenant, in.Key).Scan(&id)
			if err == nil {
				out, err = s.Posting(ctx, tx, tenant, id)
				pending = out.Status == StatePending
				return err
			}
			if !errors.Is(err, pgx.ErrNoRows) {
				return err
			}
		}
		st, err := s.student(ctx, tx, tenant, in.Student)
		if err != nil {
			return err
		}
		in.Note = strings.Join(strings.Fields(in.Note), " ")
		when := s.now()
		switch {
		case in.AmountKobo < 100 || in.AmountKobo > invoicing.MaxTotalKobo:
			return invalid("the amount is at least ₦1")
		case !slices.Contains(PaymentHows, in.How):
			return invalid("how is cash, transfer, pos or other")
		case len(in.Note) > 200:
			return invalid("the note is up to 200 characters")
		case in.Date != "":
			d, ok := dateOnly(in.Date)
			if !ok {
				return invalid("the date is written 2026-10-23")
			}
			if d.After(when.Add(24 * time.Hour)) {
				return invalid("the date is in the future")
			}
			when = d
		}
		threshold, err := CashThreshold(ctx, tx, tenant)
		if err != nil {
			return err
		}
		pending = threshold != nil && in.AmountKobo >= *threshold
		if threshold != nil && !pending {
			// With what desk and entered money already counts towards the
			// student's invoices without a second person (their open ones,
			// and any of theirs within the window): postings split under
			// the threshold add up (docs/schools.md#the-second-person).
			open, err := s.Invoices.OpenInvoicesOf(ctx, tx, tenant, "key:"+StudentKey(st.ID))
			if err != nil {
				return err
			}
			ids := make([]uuid.UUID, 0, len(open))
			for _, x := range open {
				ids = append(ids, x.ID)
			}
			prior, err := matching.EnteredUnapproved(ctx, tx, tenant, &st.ID, ids)
			if err != nil {
				return err
			}
			pending = prior+in.AmountKobo >= *threshold
		}
		status := StatePosted
		if pending {
			status = StatePending
		}
		details := map[string]any{"how": in.How, "date": when.Format(time.DateOnly), "note": in.Note}
		id, err := s.request(ctx, tx, tenant, KindCashPosting, status, &st.ID, nil, nil, in.AmountKobo, details, in.Key, by)
		if err != nil {
			return err
		}
		if !pending {
			if parts, err = s.post(ctx, tx, tenant, id, st, in.AmountKobo, in.How, when, in.Note, by); err != nil {
				return err
			}
		}
		if out, err = s.Posting(ctx, tx, tenant, id); err != nil {
			return err
		}
		out.Created = true
		return nil
	})
	if err != nil {
		return Posting{}, nil, err
	}
	if pending {
		return out, nil, &ApprovalNeeded{Request: out}
	}
	return out, parts, nil
}

// PostedAfter does what a posting's parts need once their transaction
// committed: a run waiting on an invoice a part paid is told.
func (s *Schools) PostedAfter(ctx context.Context, tenant uuid.UUID, parts []matching.CountedPart) error {
	return s.matcher().TellPaid(ctx, tenant, parts)
}

// request records a request (or a posting made at once).
func (s *Schools) request(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, kind, status string, student, invoice, term *uuid.UUID, amount int64, details map[string]any, key, by string) (uuid.UUID, error) {
	id := newID()
	raw, _ := json.Marshal(details)
	var k *string
	if key != "" {
		k = &key
	}
	_, err := tx.Exec(ctx, `INSERT INTO school_pending_postings (id, tenant_id, kind, status, student_id, invoice_id, term_id, amount_kobo, details, key, requested_by, requested_user)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)`, id, tenant, kind, status, student, invoice, term, amount, raw, k, by, userOf(by))
	if code, _ := pgCode(err); code == "23505" {
		return uuid.Nil, refused("that request was made already")
	}
	return id, err
}

// post writes the posting's money log entry, makes it the student's
// money and counts it towards their open invoices oldest first (then
// their siblings' where the school shares family credit); the receipts
// are issued as each part is counted (the Counted hook). It records the
// result on the request's row.
func (s *Schools) post(ctx context.Context, tx pgx.Tx, tenant, id uuid.UUID, st Student, amount int64, how string, when time.Time, note, by string) ([]matching.CountedPart, error) {
	kind, channel := "cash", "cash"
	switch how {
	case "transfer", "other":
		kind, channel = "statement", "bank"
	case "pos":
		kind, channel = "pos_sale", "pos"
	}
	desc := "School fees: admission no. " + st.AdmissionNumber
	if note != "" {
		desc += ". " + note
	}
	log := &moneylog.Log{Pool: s.Pool, PII: s.Vault, Now: s.Now}
	entry, _, err := log.RecordTx(ctx, tx, tenant, moneylog.Entry{Direction: "in", Kind: kind, Status: "settled", Channel: channel, Reference: "posting:" + id.String(),
		Source: "entered", AmountKobo: &amount, OccurredAt: &when, Description: desc, EnteredBy: by})
	if err != nil {
		return nil, err
	}
	if err := moneylog.LockEntry(ctx, tx, entry); err != nil {
		return nil, err
	}
	customer := "key:" + StudentKey(st.ID)
	if err := invoicing.MarkCustomers(ctx, tx, tenant, entry, customer, nil, "person", by); err != nil {
		return nil, err
	}
	parts, err := s.matcher().CountCustomer(ctx, tx, tenant, entry, customer, matching.CustomerCounting{Connection: channel, How: "manual", By: by, Reasons: []string{ReasonPosted}})
	if err != nil {
		return nil, err
	}
	res := PostingResult{EntryID: &entry, Parts: []PostedPart{}}
	for _, p := range parts {
		pp := PostedPart{InvoiceID: p.Invoice.ID, Number: p.Invoice.Number, AmountKobo: p.AmountKobo}
		if r, ok, err := s.receiptOfPart(ctx, tx, tenant, p.PartID); err != nil {
			return nil, err
		} else if ok {
			pp.ReceiptID, pp.ReceiptNumber = r.ID, r.Number
		}
		res.Parts = append(res.Parts, pp)
	}
	if res.CreditKobo, err = invoicing.CreditOf(ctx, tx, tenant, customer); err != nil {
		return nil, err
	}
	return parts, s.finish(ctx, tx, tenant, id, res)
}

// finish records what a request did.
func (s *Schools) finish(ctx context.Context, tx pgx.Tx, tenant, id uuid.UUID, res PostingResult) error {
	raw, _ := json.Marshal(res)
	_, err := tx.Exec(ctx, `UPDATE school_pending_postings SET status = $3, result = $4 WHERE tenant_id = $1 AND id = $2`, tenant, id, StatePosted, raw)
	return checked(err)
}

// --- requests for a second person ---

// requestDiscount records a standing discount at or above the threshold
// as a request and answers ApprovalNeeded.
func (s *Schools) requestDiscount(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, st Student, in DiscountIn, worth int64, by string) error {
	details := map[string]any{"kind": in.Kind, "reason": in.Reason}
	if in.Percent != nil {
		details["percent"] = *in.Percent
	}
	if in.AmountKobo != nil {
		details["amount_kobo"] = *in.AmountKobo
	}
	id, err := s.request(ctx, tx, tenant, KindDiscount, StatePending, &st.ID, nil, nil, worth, details, "", by)
	if err != nil {
		return err
	}
	p, err := s.Posting(ctx, tx, tenant, id)
	if err != nil {
		return err
	}
	return &ApprovalNeeded{Request: p}
}

// RequestDiscountAmendment looks at an amendment on a sent invoice that
// lowers its total (a discount, or a line taken out, which is a discount
// of the line's amount): under the school's discount threshold it returns
// nil and the caller amends the invoice; at or above it, it records the
// amendment as a request for a second person and returns ApprovalNeeded.
// What is weighed is the reduction with those the invoice took already
// without a second person and, on a student's invoice, with the student's
// other reductions and standing discounts given without one within the
// window (ReducedUnapproved): amendments and discounts split under the
// threshold, on one invoice or several, add up. An amendment that adds to
// the total returns nil.
func (s *Schools) RequestDiscountAmendment(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, idOrNumber string, in invoicing.AmendmentIn, by string) error {
	if s.Invoices == nil {
		return nil
	}
	limit, err := DiscountThreshold(ctx, tx, tenant)
	if err != nil || limit <= 0 {
		return err
	}
	x, err := s.Invoices.Get(ctx, tx, tenant, idOrNumber)
	if err != nil {
		return err
	}
	l, err := in.Plan(x)
	if err != nil {
		return err
	}
	reduction := -l.AmountKobo()
	if reduction <= 0 {
		return nil
	}
	var student *uuid.UUID
	var sid uuid.UUID
	if err := tx.QueryRow(ctx, `SELECT student_id FROM school_term_invoices WHERE invoice_id = $1`, x.ID).Scan(&sid); err == nil {
		student = &sid
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	prior, err := ReducedUnapproved(ctx, tx, tenant, student, &x.ID)
	if err != nil || prior+reduction < limit {
		return err
	}
	raw, _ := json.Marshal(in)
	var am map[string]any
	_ = json.Unmarshal(raw, &am)
	id, err := s.request(ctx, tx, tenant, KindDiscountAmendment, StatePending, student, &x.ID, nil, reduction, map[string]any{"amendment": am, "reason": in.Reason}, "", by)
	if err != nil {
		return err
	}
	p, err := s.Posting(ctx, tx, tenant, id)
	if err != nil {
		return err
	}
	return &ApprovalNeeded{Request: p}
}

// ReducedUnapproved is what was taken off without a second person
// approving it: by amendments (discounts and lines taken out) of the
// invoice given, whenever made, and, for a student, by amendments of any
// of the student's invoices and the standing discounts given them (at
// what each was worth when given, ended or not), within the window
// (matching.SchoolWindowSQL). Either may be nil.
func ReducedUnapproved(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, student, invoice *uuid.UUID) (int64, error) {
	var n int64
	//sqlcheck:built SchoolWindowSQL is a const and StudentInvoicesSQL is given the literal "$3"
	err := tx.QueryRow(ctx, `SELECT ((SELECT COALESCE(-sum(a.amount_kobo), 0) FROM customer_invoice_amendments a
		WHERE a.tenant_id = $1 AND a.amount_kobo < 0
		  AND (a.invoice_id = $2::uuid OR $3::uuid IS NOT NULL AND a.created_at >= `+matching.SchoolWindowSQL+` AND a.invoice_id IN (`+matching.StudentInvoicesSQL("$3")+`))
		  AND NOT EXISTS (SELECT 1 FROM school_pending_postings r WHERE r.tenant_id = a.tenant_id AND r.kind = $4 AND r.status = 'posted'
		                  AND r.result->>'amendment_id' = a.id::text))
		+ (SELECT COALESCE(sum(COALESCE(d.worth_kobo, d.amount_kobo, 0)), 0) FROM school_discounts d
		WHERE d.tenant_id = $1 AND d.student_id = $3::uuid AND d.created_at >= `+matching.SchoolWindowSQL+`
		  AND NOT EXISTS (SELECT 1 FROM school_pending_postings r WHERE r.tenant_id = d.tenant_id AND r.kind = $5 AND r.status = 'posted'
		                  AND r.result->>'discount_id' = d.id::text)))::bigint`, tenant, invoice, student, KindDiscountAmendment, KindDiscount).Scan(&n)
	return n, err
}

// requestTermInvoices records each student a term's plan held for a
// discount as a request, once per term and student while it waits.
func (s *Schools) requestTermInvoices(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, p TermPlan, send bool, by string) (int, error) {
	n := 0
	for _, ps := range p.NeedsApproval {
		var exists bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM school_pending_postings WHERE tenant_id = $1 AND kind = $2 AND term_id = $3 AND student_id = $4 AND status = 'pending')`,
			tenant, KindTermInvoice, p.Term.ID, ps.ID).Scan(&exists); err != nil {
			return n, err
		}
		if exists {
			continue
		}
		st := ps.ID
		term := p.Term.ID
		if _, err := s.request(ctx, tx, tenant, KindTermInvoice, StatePending, &st, nil, &term, ps.TotalKobo, map[string]any{"send": send, "class": ps.Class}, "", by); err != nil {
			return n, err
		}
		n++
	}
	return n, nil
}

// --- the inbox ---

const postingCols = `p.id, p.kind, p.status, p.student_id, COALESCE(s.admission_number, ''), s.details, COALESCE((SELECT c.name || CASE WHEN c.arm = '' THEN '' ELSE ' ' || c.arm END FROM school_classes c WHERE c.id = s.class_id), ''),
	p.invoice_id, COALESCE(i.number, ''), p.term_id, p.amount_kobo, p.details, p.requested_by, p.requested_user, COALESCE(ru.email, ''), p.requested_at,
	COALESCE(p.decided_by, ''), COALESCE(du.email, ''), p.decided_at, COALESCE(p.decision_reason, ''), p.result
	FROM school_pending_postings p LEFT JOIN school_students s ON s.id = p.student_id LEFT JOIN customer_invoices i ON i.id = p.invoice_id
	LEFT JOIN users ru ON ru.id = p.requested_user LEFT JOIN users du ON du.id = p.decided_user`

func (s *Schools) scanPostings(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, rows pgx.Rows) ([]Posting, error) {
	type row struct {
		p       Posting
		student map[string]any
		details []byte
		result  []byte
	}
	list, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (row, error) {
		var x row
		err := r.Scan(&x.p.ID, &x.p.Kind, &x.p.Status, &x.p.StudentID, &x.p.AdmissionNumber, &x.student, &x.p.Class, &x.p.InvoiceID, &x.p.InvoiceNumber, &x.p.TermID,
			&x.p.AmountKobo, &x.details, &x.p.RequestedBy, &x.p.RequestedUser, &x.p.RequestedEmail, &x.p.RequestedAt, &x.p.DecidedBy, &x.p.DecidedEmail, &x.p.DecidedAt, &x.p.DecisionReason, &x.result)
		return x, err
	})
	if err != nil {
		return nil, err
	}
	out := make([]Posting, 0, len(list))
	for _, x := range list {
		p := x.p
		p.Details = map[string]any{}
		_ = json.Unmarshal(x.details, &p.Details)
		if len(x.result) > 0 {
			p.Result = &PostingResult{}
			_ = json.Unmarshal(x.result, p.Result)
		}
		if x.student != nil {
			var d studentSealed
			erased, err := s.open(ctx, tx, tenant, x.student, &d)
			if err != nil {
				return nil, err
			}
			p.Name = d.Name
			if erased {
				p.Name = Erased
			}
		}
		out = append(out, p)
	}
	return out, nil
}

// Posting is one request.
func (s *Schools) Posting(ctx context.Context, tx pgx.Tx, tenant, id uuid.UUID) (Posting, error) {
	rows, err := tx.Query(ctx, `SELECT `+postingCols+` WHERE p.tenant_id = $1 AND p.id = $2`, tenant, id)
	if err != nil {
		return Posting{}, err
	}
	list, err := s.scanPostings(ctx, tx, tenant, rows)
	if err != nil {
		return Posting{}, err
	}
	if len(list) == 0 {
		return Posting{}, ErrNotFound
	}
	return list[0], nil
}

// PostingFilter narrows the requests listed.
type PostingFilter struct {
	Status  string
	Kind    string
	Student *uuid.UUID
	Limit   int
}

// Postings lists requests, newest first: the pending ones for the
// approvals inbox, a student's, the Payments page's.
func (s *Schools) Postings(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, f PostingFilter) ([]Posting, error) {
	if err := need(ctx, tx, tenant); err != nil {
		return nil, err
	}
	if f.Limit <= 0 || f.Limit > 500 {
		f.Limit = 100
	}
	rows, err := tx.Query(ctx, `SELECT `+postingCols+` WHERE p.tenant_id = $1 AND ($2 = '' OR p.status = $2) AND ($3 = '' OR p.kind = $3) AND ($4::uuid IS NULL OR p.student_id = $4)
		ORDER BY p.requested_at DESC, p.id DESC LIMIT $5`, tenant, f.Status, f.Kind, f.Student, f.Limit)
	if err != nil {
		return nil, err
	}
	list, err := s.scanPostings(ctx, tx, tenant, rows)
	if list == nil {
		list = []Posting{}
	}
	return list, err
}

// Approvers are the people who may decide the school's requests: those
// holding school.approve through a built-in role (owners) or a custom
// role, active.
func Approvers(ctx context.Context, tx pgx.Tx, tenant uuid.UUID) ([]uuid.UUID, error) {
	builtin, holding := access.BuiltinNames(), access.Holding(access.SchoolApprove)
	rows, err := tx.Query(ctx, `SELECT DISTINCT ms.user_id FROM memberships ms JOIN users u ON u.id = ms.user_id
		WHERE ms.tenant_id = $1 AND u.status = 'active'
		  AND CASE WHEN ms.role = ANY ($2) THEN ms.role = ANY ($3)
		      ELSE EXISTS (SELECT 1 FROM roles r WHERE r.tenant_id = ms.tenant_id AND r.name = ms.role AND $4 = ANY (r.permissions)) END
		ORDER BY ms.user_id`, tenant, builtin, holding, access.SchoolApprove)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
}

// --- deciding ---

// Decision is what deciding a request did, and what to do after the
// transaction commits (DecidedAfter).
type Decision struct {
	Request Posting
	parts   []matching.CountedPart
	paid    *invoicing.Invoice
	send    *uuid.UUID
	// matched is what an approved match does once it commits.
	matched func(context.Context) error
}

// Decide approves or declines a pending request, once, by someone other
// than its requester (four-eyes: never the person who asked). Approving
// posts the cash (entry, parts, receipts), gives the discount, amends the
// invoice or makes the held term invoice, exactly once; declining needs
// a reason and changes nothing. The requester is told in their
// notifications either way. Call DecidedAfter once this commits.
func (s *Schools) Decide(ctx context.Context, tx pgx.Tx, tenant, id uuid.UUID, approve bool, reason, by string) (Decision, error) {
	if err := need(ctx, tx, tenant); err != nil {
		return Decision{}, err
	}
	reason = strings.Join(strings.Fields(reason), " ")
	if len(reason) > 300 {
		return Decision{}, invalid("the reason is up to 300 characters")
	}
	if !approve && reason == "" {
		return Decision{}, invalid("say why it is declined")
	}
	if _, err := tx.Exec(ctx, `SELECT 1 FROM school_pending_postings WHERE tenant_id = $1 AND id = $2 FOR UPDATE`, tenant, id); err != nil {
		return Decision{}, err
	}
	p, err := s.Posting(ctx, tx, tenant, id)
	if err != nil {
		return Decision{}, err
	}
	if p.Status != StatePending {
		return Decision{}, refused("the request was decided already (%s)", p.Status)
	}
	user := userOf(by)
	if user != nil && p.RequestedUser != nil && *user == *p.RequestedUser || by == p.RequestedBy {
		return Decision{}, refused("the person who asked cannot decide their own request: a second person approves or declines it")
	}
	status := StateDeclined
	if approve {
		status = StatePosted
	}
	if _, err := tx.Exec(ctx, `UPDATE school_pending_postings SET status = $3, decided_by = $4, decided_user = $5, decided_at = now(), decision_reason = NULLIF($6, '')
		WHERE tenant_id = $1 AND id = $2`, tenant, id, status, by, user, reason); err != nil {
		return Decision{}, checked(err)
	}
	d := Decision{}
	if approve {
		if err := s.apply(ctx, tx, tenant, &d, p, by); err != nil {
			return Decision{}, err
		}
	}
	if d.Request, err = s.Posting(ctx, tx, tenant, id); err != nil {
		return Decision{}, err
	}
	return d, s.tellRequester(ctx, tx, tenant, d.Request)
}

// apply does what an approved request asked for.
func (s *Schools) apply(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, d *Decision, p Posting, by string) error {
	if s.Invoices == nil {
		return refused("invoices are not available on this server")
	}
	switch p.Kind {
	case KindCashPosting:
		st, err := s.student(ctx, tx, tenant, p.StudentID.String())
		if err != nil {
			return err
		}
		when, _ := dateOnly(str(p.Details, "date"))
		if when.IsZero() {
			when = s.now()
		}
		d.parts, err = s.post(ctx, tx, tenant, p.ID, st, p.AmountKobo, str(p.Details, "how"), when, str(p.Details, "note"), p.RequestedBy)
		return err
	case KindCashMatch:
		// The match as its requester asked for it, made now.
		sp, after, err := s.matcher().ApproveHeld(ctx, tx, tenant, p.Details, p.RequestedBy)
		if err != nil {
			return err
		}
		d.matched = after
		res := PostingResult{Parts: []PostedPart{}}
		if id, err := uuid.Parse(str(p.Details, "entry_id")); err == nil {
			res.EntryID = &id
		}
		for i, x := range sp.Parts {
			if x.Invoice != nil {
				res.Parts = append(res.Parts, PostedPart{InvoiceID: x.Invoice.ID, Number: x.Invoice.Number, AmountKobo: sp.PartKobo[i]})
			}
		}
		res.CreditKobo = sp.CreditKobo
		return s.finish(ctx, tx, tenant, p.ID, res)
	case KindDiscount:
		in := DiscountIn{Kind: str(p.Details, "kind"), Reason: str(p.Details, "reason")}
		if v, ok := p.Details["percent"].(float64); ok {
			n := int(v)
			in.Percent = &n
		}
		if v, ok := p.Details["amount_kobo"].(float64); ok {
			n := int64(v)
			in.AmountKobo = &n
		}
		disc, _, err := s.giveDiscount(ctx, tx, tenant, *p.StudentID, in, p.AmountKobo, p.RequestedBy)
		if err != nil {
			return err
		}
		return s.finish(ctx, tx, tenant, p.ID, PostingResult{DiscountID: &disc.ID})
	case KindDiscountAmendment:
		raw, _ := json.Marshal(p.Details["amendment"])
		var in invoicing.AmendmentIn
		if err := json.Unmarshal(raw, &in); err != nil {
			return err
		}
		am, err := s.Invoices.Amend(ctx, tx, tenant, p.InvoiceID.String(), in, p.RequestedBy)
		if err != nil {
			return err
		}
		if am.Became {
			d.paid = &am.Invoice
		}
		return s.finish(ctx, tx, tenant, p.ID, PostingResult{AmendmentID: &am.Amendment.ID, InvoiceID: &am.Invoice.ID, InvoiceNumber: am.Invoice.Number})
	case KindTermInvoice:
		inv, err := s.invoiceOne(ctx, tx, tenant, *p.TermID, *p.StudentID, by)
		if err != nil {
			return err
		}
		if send, _ := p.Details["send"].(bool); send {
			id := inv.ID
			d.send = &id
		}
		return s.finish(ctx, tx, tenant, p.ID, PostingResult{InvoiceID: &inv.ID, InvoiceNumber: inv.Number})
	}
	return refused("unknown request kind %s", p.Kind)
}

// invoiceOne makes one held student's term invoice, as the plan would
// have (their fees, options and discounts as they are now), exactly once
// under the term and student's key.
func (s *Schools) invoiceOne(ctx context.Context, tx pgx.Tx, tenant, termID, student uuid.UUID, by string) (invoicing.Invoice, error) {
	t, err := s.Term(ctx, tx, tenant, termID)
	if err != nil {
		return invoicing.Invoice{}, err
	}
	if t.State != TermCurrent {
		return invoicing.Invoice{}, refused("%s %s is no longer the current term", t.Name, t.Session)
	}
	st, err := s.student(ctx, tx, tenant, student.String())
	if err != nil {
		return invoicing.Invoice{}, err
	}
	class, err := studentClassFor(ctx, tx, tenant, student, termID)
	if err != nil {
		return invoicing.Invoice{}, err
	}
	items, err := feeItems(ctx, tx, tenant, termID, &class)
	if err != nil {
		return invoicing.Invoice{}, err
	}
	chosen, err := studentOptions(ctx, tx, tenant, student, termID)
	if err != nil {
		return invoicing.Invoice{}, err
	}
	ds, err := discounts(ctx, tx, tenant, student, false)
	if err != nil {
		return invoicing.Invoice{}, err
	}
	lines := buildLines(items, chosen, ds)
	var total int64
	for _, l := range lines {
		total += l.AmountKobo()
	}
	if total < 100 {
		return invoicing.Invoice{}, refused("there is nothing to pay after the discounts")
	}
	contacts, err := s.familyContacts(ctx, tx, tenant)
	if err != nil {
		return invoicing.Invoice{}, err
	}
	var g *Guardian
	if st.FamilyID != nil {
		g = contacts[*st.FamilyID]
	}
	if g == nil {
		return invoicing.Invoice{}, refused("the student has no guardian to send the invoice to")
	}
	cls, err := classes(ctx, tx, tenant)
	if err != nil {
		return invoicing.Invoice{}, err
	}
	label := ""
	for _, c := range cls {
		if c.ID == class {
			label = c.Label
		}
	}
	inv, err := s.Invoices.CreateOnce(ctx, tx, tenant, TermInvoiceKey(termID, student), invoicing.Draft{
		Customer: invoicing.Customer{Name: g.Name, Phone: g.Phone, Email: g.Email, Key: StudentKey(student)}, Lines: lines, DueOn: t.FeesDueOn, Note: TermNote(st.AdmissionNumber, label, t),
	}, by)
	if err != nil {
		return invoicing.Invoice{}, err
	}
	_, err = tx.Exec(ctx, `INSERT INTO school_term_invoices (tenant_id, term_id, student_id, class_id, invoice_id, created_by) VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT DO NOTHING`, tenant, termID, student, class, inv.ID, by)
	return inv, err
}

// tellRequester tells the person who asked how it was decided.
func (s *Schools) tellRequester(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, p Posting) error {
	if p.RequestedUser == nil {
		return nil
	}
	what := kindWords[p.Kind] + " of " + invoicing.Naira(p.AmountKobo)
	if p.AdmissionNumber != "" {
		what += " for " + p.AdmissionNumber
	}
	n := notices.Notice{Kind: "school_decision", Level: notices.Success, Title: "Approved: " + what, Link: "/schools/payments"}
	if p.Status == StateDeclined {
		n.Level, n.Title, n.Body = notices.Warning, "Declined: "+what, p.DecisionReason
	}
	switch p.Kind {
	case KindDiscount:
		n.Link = "/schools/students/" + p.StudentID.String()
	case KindDiscountAmendment:
		n.Link = "/invoices/" + p.InvoiceID.String()
	case KindCashMatch:
		n.Link = "/invoices/match"
		if p.InvoiceID != nil {
			n.Link = "/invoices/" + p.InvoiceID.String()
		}
	case KindTermInvoice:
		n.Link = "/schools/terms/" + p.TermID.String() + "/invoices"
	}
	return notices.AddTx(ctx, tx, tenant, []uuid.UUID{*p.RequestedUser}, n)
}

// DecidedAfter does what a decision needs once its transaction commits:
// runs waiting on invoices now paid are told, and a term invoice asked
// to be sent is sent.
func (s *Schools) DecidedAfter(ctx context.Context, tenant uuid.UUID, d Decision) error {
	if err := s.matcher().TellPaid(ctx, tenant, d.parts); err != nil {
		return err
	}
	if d.matched != nil {
		if err := d.matched(ctx); err != nil {
			return err
		}
	}
	if d.paid != nil {
		if err := s.Invoices.Paid(ctx, tenant, *d.paid); err != nil {
			return err
		}
	}
	if d.send != nil && d.Request.TermID != nil {
		_, err := s.Invoices.Send(ctx, tenant, d.send.String(), d.Request.DecidedBy, "term:"+d.Request.TermID.String()+":send:"+d.send.String())
		return err
	}
	return nil
}

func str(m map[string]any, k string) string {
	v, _ := m[k].(string)
	return v
}
