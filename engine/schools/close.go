package schools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/invoicing"
)

// The proprietor (docs/spec/schools.md 13, B9; docs/schools.md#overview,
// #the-daily-close): the Overview reads a term's money as it stands
// (collected against expected, this week, outstanding, how much matched
// without a person, how parents paid, the debtors' aging from B7, the
// clearance totals from B8, pass-through apart, and the last close); the
// daily close is a report per school and Lagos day, taken exactly once
// (school_daily_closes, the key inserted before the report is computed,
// in the transaction that stores it) and never recomputed. Nothing in a
// close is swept up: each item that waits for someone, or does not
// reconcile, says where to fix it.

// --- the overview ---

// Ways parents paid, as the Overview groups them.
const (
	PaidByAccount  = "account"  // a credit to the student's own account number
	PaidByCard     = "card"     // a card or POS payment
	PaidByCash     = "cash"     // cash at the desk
	PaidByTransfer = "transfer" // a transfer to the school's own account, posted at the desk
	PaidByOther    = "other"    // a payment the money log has no entry for (the invoice page's one-time account)
)

// PaidByWords say each way for people.
var PaidByWords = map[string]string{PaidByAccount: "Account number", PaidByCard: "Card or POS", PaidByCash: "Cash", PaidByTransfer: "Transfer to the school's account", PaidByOther: "Other"}

// paidBys are the ways in the order the Overview lists them.
var paidBys = []string{PaidByAccount, PaidByCard, PaidByCash, PaidByTransfer, PaidByOther}

// HowPaid is what came in one way.
type HowPaid struct {
	How      string `json:"how"`
	Payments int    `json:"payments"`
	Kobo     int64  `json:"kobo"`
}

// Matched is how the term's counted money was matched: by the automatic
// path (a credit to the student's account, a suggestion taken without a
// person) or by a person (a suggestion confirmed, a match by hand, cash
// posted at the desk).
type Matched struct {
	AutoKobo   int64 `json:"auto_kobo"`
	PersonKobo int64 `json:"person_kobo"`
	// AutoPercent is the automatic share of the two, 0 to 100; 0 when
	// nothing was counted.
	AutoPercent int `json:"auto_percent"`
}

// CloseBrief is a close as the Overview and the list of closes show it.
type CloseBrief struct {
	ID         uuid.UUID `json:"id"`
	Day        string    `json:"day"`
	RanAt      time.Time `json:"ran_at"`
	RanBy      string    `json:"ran_by"`
	Reconciles bool      `json:"reconciles"`
	Problems   int       `json:"problems"`
}

// Overview is Schools > Overview for one term.
type Overview struct {
	TermID    uuid.UUID `json:"term_id"`
	Term      string    `json:"term"`
	TermState string    `json:"term_state"`
	Today     string    `json:"today"`
	// Enrolled and Invoiced count the term's students and those with a
	// sent invoice.
	Enrolled int `json:"enrolled"`
	Invoiced int `json:"invoiced"`
	// ExpectedKobo is what the term's sent invoices come to; ExpectedByNow
	// the part of it due by today; CollectedKobo what was counted towards
	// them; OutstandingKobo what is left; OverdueKobo what is left on
	// invoices past their due date.
	ExpectedKobo      int64 `json:"expected_kobo"`
	ExpectedByNowKobo int64 `json:"expected_by_now_kobo"`
	CollectedKobo     int64 `json:"collected_kobo"`
	OutstandingKobo   int64 `json:"outstanding_kobo"`
	OverdueKobo       int64 `json:"overdue_kobo"`
	// CollectedPercent is collected over expected, 0 to 100.
	CollectedPercent int `json:"collected_percent"`
	// ThisWeekKobo is what was counted since Monday, Lagos time.
	ThisWeekKobo int64 `json:"this_week_kobo"`
	// PassThroughKobo is what of the collected money is for others (exam
	// bodies), by body: kept apart from the school's own.
	PassThroughKobo int64                  `json:"pass_through_kobo"`
	PassThrough     []invoicing.BodyAmount `json:"pass_through"`
	Matched         Matched                `json:"matched"`
	HowPaid         []HowPaid              `json:"how_paid"`
	// TopDebtors are the five largest balances on Debtors (every term's
	// open invoices, as the Debtors page lists them); Aging their totals
	// by bucket; Debtors how many students owe, and how much.
	TopDebtors  []Debtor               `json:"top_debtors"`
	Aging       map[string]BucketTotal `json:"aging"`
	Debtors     int                    `json:"debtors"`
	DebtorsKobo int64                  `json:"debtors_kobo"`
	// Clearance counts the term's students by status (B8).
	Clearance ClearanceTotals `json:"clearance"`
	// LastClose is the latest daily close, when one was taken.
	LastClose *CloseBrief `json:"last_close,omitempty"`
}

// TopDebtors is how many debtors the Overview names.
const TopDebtors = 5

// weekStart is the Monday of today's week, Lagos time, at midnight UTC
// for date arithmetic (today is such a date).
func weekStart(today time.Time) time.Time {
	back := (int(today.Weekday()) + 6) % 7
	return today.AddDate(0, 0, -back)
}

// lagosStart is the instant a Lagos date (as today gives it) begins.
func lagosStart(d time.Time) time.Time {
	return time.Date(d.Year(), d.Month(), d.Day(), 0, 0, 0, 0, lagosOnce())
}

// paidBy is the way a counted part came, from its money log entry.
func paidBy(hasEntry bool, kind, customerHow string) string {
	switch {
	case !hasEntry:
		return PaidByOther
	case kind == "cash":
		return PaidByCash
	case kind == "pos_sale":
		return PaidByCard
	case kind == "statement":
		return PaidByTransfer
	case kind == "account_credit" || customerHow == "account":
		return PaidByAccount
	}
	return PaidByOther
}

// automatic says a part was counted by the automatic path.
func automatic(how string) bool { return how == "exact" || how == "suggested_auto" }

// Overview reads the term's money as it stands.
func (s *Schools) Overview(ctx context.Context, tx pgx.Tx, tenant, term uuid.UUID) (Overview, error) {
	if err := need(ctx, tx, tenant); err != nil {
		return Overview{}, err
	}
	t, err := s.Term(ctx, tx, tenant, term)
	if err != nil {
		return Overview{}, err
	}
	today := s.today()
	out := Overview{TermID: t.ID, Term: t.Name + " " + t.Session, TermState: t.State, Today: today.Format(time.DateOnly), Enrolled: t.Enrolled,
		Aging: map[string]BucketTotal{}, HowPaid: []HowPaid{}, TopDebtors: []Debtor{}, PassThrough: []invoicing.BodyAmount{}}
	day := today.Format(time.DateOnly)
	if err := tx.QueryRow(ctx, `SELECT count(*), COALESCE(sum(i.total_kobo), 0), COALESCE(sum(LEAST(i.paid_kobo, i.total_kobo)), 0),
			COALESCE(sum(CASE WHEN i.due_on IS NULL OR i.due_on <= $3::date THEN i.total_kobo ELSE 0 END), 0),
			COALESCE(sum(CASE WHEN i.due_on < $3::date THEN GREATEST(i.total_kobo - i.paid_kobo, 0) ELSE 0 END), 0)
		FROM school_term_invoices ti JOIN customer_invoices i ON i.id = ti.invoice_id
		WHERE ti.tenant_id = $1 AND ti.term_id = $2 AND i.sent_at IS NOT NULL AND i.status <> 'void'`, tenant, term, day).
		Scan(&out.Invoiced, &out.ExpectedKobo, &out.CollectedKobo, &out.ExpectedByNowKobo, &out.OverdueKobo); err != nil {
		return Overview{}, err
	}
	out.OutstandingKobo = out.ExpectedKobo - out.CollectedKobo
	if out.ExpectedKobo > 0 {
		out.CollectedPercent = int(out.CollectedKobo * 100 / out.ExpectedKobo)
	}
	// The parts counted towards the term's invoices, each with its entry.
	rows, err := tx.Query(ctx, `SELECT p.how, p.amount_kobo, p.created_at, e.id IS NOT NULL, COALESCE(e.kind, ''), COALESCE(mc.how, '')
		FROM customer_invoice_payments p JOIN school_term_invoices ti ON ti.invoice_id = p.invoice_id
		LEFT JOIN money_entries e ON e.id = p.entry_id LEFT JOIN money_entry_customers mc ON mc.entry_id = e.id
		WHERE ti.tenant_id = $1 AND ti.term_id = $2 AND p.undoes IS NULL AND NOT EXISTS (SELECT 1 FROM customer_invoice_payments u WHERE u.undoes = p.id)`, tenant, term)
	if err != nil {
		return Overview{}, err
	}
	week := lagosStart(weekStart(today))
	hows := map[string]*HowPaid{}
	for _, how := range paidBys {
		hows[how] = &HowPaid{How: how}
	}
	for rows.Next() {
		var how, kind, customerHow string
		var amount int64
		var at time.Time
		var hasEntry bool
		if err := rows.Scan(&how, &amount, &at, &hasEntry, &kind, &customerHow); err != nil {
			rows.Close()
			return Overview{}, err
		}
		if !at.Before(week) {
			out.ThisWeekKobo += amount
		}
		if automatic(how) {
			out.Matched.AutoKobo += amount
		} else {
			out.Matched.PersonKobo += amount
		}
		h := hows[paidBy(hasEntry, kind, customerHow)]
		h.Payments++
		h.Kobo += amount
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return Overview{}, err
	}
	if sum := out.Matched.AutoKobo + out.Matched.PersonKobo; sum > 0 {
		out.Matched.AutoPercent = int(out.Matched.AutoKobo * 100 / sum)
	}
	for _, how := range paidBys {
		if hows[how].Payments > 0 {
			out.HowPaid = append(out.HowPaid, *hows[how])
		}
	}
	// Pass-through: what of each invoice's payments reached lines
	// collected for others, the school's own lines paid first.
	rows, err = tx.Query(ctx, `SELECT i.lines, LEAST(i.paid_kobo, i.total_kobo) FROM school_term_invoices ti JOIN customer_invoices i ON i.id = ti.invoice_id
		WHERE ti.tenant_id = $1 AND ti.term_id = $2 AND i.status <> 'void' AND i.paid_kobo > 0 AND jsonb_path_exists(i.lines, '$[*].pass_through_for')`, tenant, term)
	if err != nil {
		return Overview{}, err
	}
	for rows.Next() {
		var raw []byte
		var paid int64
		if err := rows.Scan(&raw, &paid); err != nil {
			rows.Close()
			return Overview{}, err
		}
		var lines []invoicing.Line
		if err := json.Unmarshal(raw, &lines); err != nil {
			rows.Close()
			return Overview{}, err
		}
		for _, b := range invoicing.PassThroughCollected(lines, paid) {
			out.PassThroughKobo += b.Kobo
			out.PassThrough = addBodyAmount(out.PassThrough, b)
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return Overview{}, err
	}
	// Debtors and aging (B7), the largest balances first.
	d, err := s.Debtors(ctx, tx, tenant, DebtorFilter{})
	if err != nil {
		return Overview{}, err
	}
	out.Aging, out.Debtors, out.DebtorsKobo = d.Totals, d.Students, d.BalanceKobo
	sort.SliceStable(d.Debtors, func(i, j int) bool { return d.Debtors[i].BalanceKobo > d.Debtors[j].BalanceKobo })
	out.TopDebtors = append(out.TopDebtors, d.Debtors[:min(len(d.Debtors), TopDebtors)]...)
	// Clearance (B8).
	cl, err := s.TermClearance(ctx, tx, tenant, term, nil)
	if err != nil {
		return Overview{}, err
	}
	out.Clearance = cl.Totals
	closes, err := s.Closes(ctx, tx, tenant, "", "", 1)
	if err != nil {
		return Overview{}, err
	}
	if len(closes) > 0 {
		out.LastClose = &closes[0]
	}
	return out, nil
}

func addBodyAmount(list []invoicing.BodyAmount, b invoicing.BodyAmount) []invoicing.BodyAmount {
	for i := range list {
		if list[i].Body == b.Body {
			list[i].Kobo += b.Kobo
			return list
		}
	}
	return append(list, b)
}

// --- the daily close ---

// Kinds of item in a close: what waits for someone, and what does not
// reconcile.
const (
	// ItemUnmatched: a student's money the log holds that no invoice
	// counts yet (a credit held, or more than the fees).
	ItemUnmatched = "unmatched"
	// ItemPendingPosting: a cash posting waiting for a second person.
	ItemPendingPosting = "pending_posting"
	// ItemPendingDiscount: a discount, an amendment or a held term
	// invoice waiting for a second person.
	ItemPendingDiscount = "pending_discount"
	// ItemClaim: a family says it paid and the money was not found.
	ItemClaim = "claim"
	// ProblemCreditWithoutEntry: a payment counted as paid into a
	// student's account with no money log entry behind it.
	ProblemCreditWithoutEntry = "credit_without_entry"
	// ProblemReceiptNotVoided: a receipt whose payment was undone or went
	// back to the payer, still valid.
	ProblemReceiptNotVoided = "receipt_not_voided"
	// ProblemPartWithoutReceipt: a payment counted towards a term invoice
	// with no receipt.
	ProblemPartWithoutReceipt = "part_without_receipt"
)

// ItemWords say each kind for people.
var ItemWords = map[string]string{
	ItemUnmatched: "Money not matched", ItemPendingPosting: "Cash posting waiting for a second person", ItemPendingDiscount: "Discount waiting for a second person",
	ItemClaim: "A family says it paid", ProblemCreditWithoutEntry: "A payment with no money log entry", ProblemReceiptNotVoided: "A receipt that should be voided", ProblemPartWithoutReceipt: "A payment with no receipt",
}

// Where each kind is fixed, in the web app.
const (
	PathToMatch   = "/invoices/match"
	PathApprovals = "/approvals"
	PathReceipts  = "/schools/receipts"
)

// CloseSum counts money.
type CloseSum struct {
	Count int   `json:"count"`
	Kobo  int64 `json:"kobo"`
}

func (c *CloseSum) add(kobo int64) {
	c.Count++
	c.Kobo += kobo
}

// CloseItem is one thing in a close that waits for someone or does not
// reconcile, with where to fix it.
type CloseItem struct {
	Kind       string `json:"kind"`
	Message    string `json:"message"`
	Path       string `json:"path"`
	AmountKobo int64  `json:"amount_kobo,omitempty"`
	// Ref names what it is about: an invoice or receipt number, an
	// admission number, an entry's id. Never a name.
	Ref string `json:"ref,omitempty"`
}

// Close is a school's daily close: the day's money on students' accounts
// and at the desk, how it was matched, what waits, and what does not
// reconcile.
type Close struct {
	CloseBrief
	TermID *uuid.UUID `json:"term_id,omitempty"`
	Term   string     `json:"term,omitempty"`
	// MoneyIn is students' money that landed that day; IntoAccounts the
	// part paid into students' account numbers, AtTheDesk the part posted
	// at the desk (cash, a transfer to the school's account, POS).
	MoneyIn      CloseSum `json:"money_in"`
	IntoAccounts CloseSum `json:"into_accounts"`
	AtTheDesk    CloseSum `json:"at_the_desk"`
	// MatchedAuto and MatchedPerson are the payments counted that day
	// towards term invoices, by the automatic path and by a person.
	MatchedAuto   CloseSum `json:"matched_auto"`
	MatchedPerson CloseSum `json:"matched_person"`
	// Unmatched is students' money the log holds that no invoice counts,
	// as of the close.
	Unmatched     CloseSum    `json:"unmatched"`
	UnmatchedList []CloseItem `json:"unmatched_list"`
	// LookbackFound is the day's money the look-back found before any
	// webhook did.
	LookbackFound CloseSum `json:"lookback_found"`
	// Pending are the cash postings and discounts waiting for a second
	// person, as of the close; Claims the families' claims not yet found.
	Pending []CloseItem `json:"pending"`
	Claims  []CloseItem `json:"claims"`
	// Problems is what does not reconcile.
	ProblemList []CloseItem `json:"problem_list"`
}

// Day parses a close's day (YYYY-MM-DD).
func Day(s string) (time.Time, error) {
	d, ok := dateOnly(s)
	if !ok {
		return time.Time{}, invalid("day is a date, YYYY-MM-DD")
	}
	return d, nil
}

// Yesterday is yesterday's Lagos date, the day the nightly close closes.
func (s *Schools) Yesterday() time.Time { return s.today().AddDate(0, 0, -1) }

// closeKept reads a stored close.
func scanClose(r pgx.Row) (Close, error) {
	var c Close
	var day time.Time
	var raw []byte
	if err := r.Scan(&c.ID, &day, &c.RanAt, &c.RanBy, &c.Reconciles, &c.Problems, &raw); err != nil {
		return c, err
	}
	if err := json.Unmarshal(raw, &c); err != nil {
		return c, err
	}
	c.Day = day.Format(time.DateOnly)
	return c, nil
}

const closeCols = `id, day, ran_at, ran_by, reconciles, problems, report`

// CloseOf is the close of a day, when one was taken.
func (s *Schools) CloseOf(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, day time.Time) (Close, bool, error) {
	if err := need(ctx, tx, tenant); err != nil {
		return Close{}, false, err
	}
	c, err := scanClose(tx.QueryRow(ctx, `SELECT `+closeCols+` FROM school_daily_closes WHERE tenant_id = $1 AND day = $2`, tenant, day.Format(time.DateOnly)))
	if errors.Is(err, pgx.ErrNoRows) {
		return Close{}, false, nil
	}
	return c, err == nil, err
}

// Closes lists the closes taken between two days (YYYY-MM-DD, either
// empty), newest first, at most limit.
func (s *Schools) Closes(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, from, to string, limit int) ([]CloseBrief, error) {
	if err := need(ctx, tx, tenant); err != nil {
		return nil, err
	}
	for _, d := range []string{from, to} {
		if d != "" {
			if _, ok := dateOnly(d); !ok {
				return nil, invalid("from and to are dates, YYYY-MM-DD")
			}
		}
	}
	if limit <= 0 || limit > 400 {
		limit = 31
	}
	rows, err := tx.Query(ctx, `SELECT id, day, ran_at, ran_by, reconciles, problems FROM school_daily_closes
		WHERE tenant_id = $1 AND ($2 = '' OR day >= $2::date) AND ($3 = '' OR day <= $3::date) ORDER BY day DESC LIMIT $4`, tenant, from, to, limit)
	if err != nil {
		return nil, err
	}
	list, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (CloseBrief, error) {
		var b CloseBrief
		var day time.Time
		err := r.Scan(&b.ID, &day, &b.RanAt, &b.RanBy, &b.Reconciles, &b.Problems)
		b.Day = day.Format(time.DateOnly)
		return b, err
	})
	if list == nil {
		list = []CloseBrief{}
	}
	return list, err
}

// CloseDay takes the close of a day: the stored one when it was taken,
// else the key is taken and the report computed and stored in this
// transaction, so a second caller finds the row. A day not over yet
// (today or later, Lagos time) is refused: the close is of a whole day.
// It answers whether this call made the close.
func (s *Schools) CloseDay(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, day time.Time, by string) (Close, bool, error) {
	if err := need(ctx, tx, tenant); err != nil {
		return Close{}, false, err
	}
	day = time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, time.UTC)
	if !day.Before(s.today()) {
		return Close{}, false, refused("the day is not over yet: a close is of a whole day, Lagos time")
	}
	if day.Before(s.today().AddDate(-1, 0, 0)) {
		return Close{}, false, refused("a close looks back at most a year")
	}
	// The key first: a second scheduler, or a person asking at the same
	// moment, waits on the unique index and then reads this row.
	id := newID()
	tag, err := tx.Exec(ctx, `INSERT INTO school_daily_closes (id, tenant_id, day, reconciles, problems, report, ran_by, ran_at)
		VALUES ($1, $2, $3, false, 0, '{}', $4, $5) ON CONFLICT (tenant_id, day) DO NOTHING`, id, tenant, day.Format(time.DateOnly), by, s.now())
	if err != nil {
		return Close{}, false, err
	}
	if tag.RowsAffected() == 0 {
		c, _, err := s.CloseOf(ctx, tx, tenant, day)
		return c, false, err
	}
	c, err := s.computeClose(ctx, tx, tenant, day)
	if err != nil {
		return Close{}, false, err
	}
	c.ID, c.RanBy, c.RanAt, c.Day = id, by, s.now(), day.Format(time.DateOnly)
	c.Problems = len(c.ProblemList)
	c.Reconciles = c.Problems == 0
	raw, err := json.Marshal(c)
	if err != nil {
		return Close{}, false, err
	}
	// The row was taken empty; the guard lets an empty row be filled in,
	// once, and refuses every change after.
	if _, err := tx.Exec(ctx, `UPDATE school_daily_closes SET reconciles = $2, problems = $3, report = $4, ran_at = $5 WHERE id = $1`, id, c.Reconciles, c.Problems, raw, c.RanAt); err != nil {
		return Close{}, false, err
	}
	return c, true, nil
}

// computeClose reads the day's report.
func (s *Schools) computeClose(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, day time.Time) (Close, error) {
	from := lagosStart(day)
	to := from.AddDate(0, 0, 1)
	c := Close{UnmatchedList: []CloseItem{}, Pending: []CloseItem{}, Claims: []CloseItem{}, ProblemList: []CloseItem{}}
	if t, ok, err := s.CurrentTerm(ctx, tx, tenant); err != nil {
		return c, err
	} else if ok {
		c.TermID, c.Term = &t.ID, t.Name+" "+t.Session
	}
	// Students' money that landed that day, and what of it is still
	// unmatched now.
	rows, err := tx.Query(ctx, `SELECT e.id, e.kind, e.status, e.source, mc.how, mc.customer_id,
			taskiem_money_entry_countable(e.amount_kobo, e.fee_kobo, e.net), taskiem_money_entry_left(e.id),
			COALESCE(e.occurred_at, e.recorded_at) >= $2 AND COALESCE(e.occurred_at, e.recorded_at) < $3
		FROM money_entries e JOIN money_entry_customers mc ON mc.entry_id = e.id
		WHERE e.tenant_id = $1 AND e.direction = 'in' AND e.status = 'settled' AND mc.customer_id LIKE 'key:student:%'
		  AND (COALESCE(e.occurred_at, e.recorded_at) >= $2 AND COALESCE(e.occurred_at, e.recorded_at) < $3 OR taskiem_money_entry_left(e.id) > 0)
		ORDER BY COALESCE(e.occurred_at, e.recorded_at)`, tenant, from, to)
	if err != nil {
		return c, err
	}
	for rows.Next() {
		var id uuid.UUID
		var kind, status, source, how, customer string
		var countable, left int64
		var inDay bool
		if err := rows.Scan(&id, &kind, &status, &source, &how, &customer, &countable, &left, &inDay); err != nil {
			rows.Close()
			return c, err
		}
		if inDay {
			c.MoneyIn.add(countable)
			if how == "account" || kind == "account_credit" {
				c.IntoAccounts.add(countable)
			} else if kind == "cash" || kind == "statement" || kind == "pos_sale" {
				c.AtTheDesk.add(countable)
			}
			if source == "lookup" {
				c.LookbackFound.add(countable)
			}
		}
		if left > 0 {
			c.Unmatched.add(left)
			student := strings.TrimPrefix(customer, "key:student:")
			c.UnmatchedList = append(c.UnmatchedList, CloseItem{Kind: ItemUnmatched, AmountKobo: left, Ref: id.String(), Path: PathToMatch,
				Message: fmt.Sprintf("%s of a student's money (student %s) is not matched to any invoice", invoicing.Naira(left), shortID(student))})
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return c, err
	}
	// The payments counted that day towards term invoices, by how.
	rows, err = tx.Query(ctx, `SELECT p.how, p.amount_kobo FROM customer_invoice_payments p JOIN school_term_invoices ti ON ti.invoice_id = p.invoice_id
		WHERE ti.tenant_id = $1 AND p.undoes IS NULL AND p.created_at >= $2 AND p.created_at < $3
		  AND NOT EXISTS (SELECT 1 FROM customer_invoice_payments u WHERE u.undoes = p.id)`, tenant, from, to)
	if err != nil {
		return c, err
	}
	for rows.Next() {
		var how string
		var amount int64
		if err := rows.Scan(&how, &amount); err != nil {
			rows.Close()
			return c, err
		}
		if automatic(how) {
			c.MatchedAuto.add(amount)
		} else {
			c.MatchedPerson.add(amount)
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return c, err
	}
	// What waits for a second person, as of the close.
	pending, err := s.Postings(ctx, tx, tenant, PostingFilter{Status: StatePending, Limit: 500})
	if err != nil {
		return c, err
	}
	for _, p := range pending {
		item := CloseItem{Kind: ItemPendingDiscount, AmountKobo: p.AmountKobo, Ref: p.AdmissionNumber, Path: PathApprovals}
		switch p.Kind {
		case KindCashPosting:
			item.Kind = ItemPendingPosting
			item.Message = fmt.Sprintf("%s posted for %s (%s) waits for a second person since %s", invoicing.Naira(p.AmountKobo), p.AdmissionNumber, HowWordsDesk(str(p.Details, "how")), p.RequestedAt.In(lagosOnce()).Format("2 Jan"))
		case KindTermInvoice:
			item.Message = fmt.Sprintf("the term invoice of %s is held for a discount waiting for a second person", p.AdmissionNumber)
		default:
			item.Message = fmt.Sprintf("a discount of %s for %s waits for a second person", invoicing.Naira(p.AmountKobo), p.AdmissionNumber)
		}
		c.Pending = append(c.Pending, item)
	}
	// Families' claims not yet found.
	claims, err := s.Claims(ctx, tx, tenant, ClaimFilter{Status: "open", Limit: 500})
	if err != nil {
		return c, err
	}
	for _, cl := range claims {
		c.Claims = append(c.Claims, CloseItem{Kind: ItemClaim, AmountKobo: cl.AmountKobo, Ref: cl.AdmissionNumber, Path: PathToMatch,
			Message: fmt.Sprintf("the family of %s says it paid %s by %s on %s; the money is not found yet", cl.AdmissionNumber, invoicing.Naira(cl.AmountKobo), strings.ToLower(ClaimHowWords[cl.How]), dateWords(cl.PaidOn))})
	}
	// What does not reconcile, whenever it happened: a payment counted
	// as paid into a student's account with no entry behind it; a receipt
	// still valid for a payment undone or gone back; a payment with no
	// receipt.
	rows, err = tx.Query(ctx, `SELECT p.id, p.how, p.amount_kobo, p.entry_id IS NULL, i.id, i.number, s.admission_number, r.number, r.voided_at IS NOT NULL,
			EXISTS (SELECT 1 FROM customer_invoice_payments u WHERE u.undoes = p.id),
			p.entry_id IS NOT NULL AND EXISTS (SELECT 1 FROM money_reversals rv WHERE rv.entry_id = p.entry_id)
		FROM customer_invoice_payments p JOIN school_term_invoices ti ON ti.invoice_id = p.invoice_id JOIN customer_invoices i ON i.id = p.invoice_id
		JOIN school_students s ON s.id = ti.student_id LEFT JOIN school_receipts r ON r.part_id = p.id
		WHERE ti.tenant_id = $1 AND p.undoes IS NULL ORDER BY p.created_at`, tenant)
	if err != nil {
		return c, err
	}
	for rows.Next() {
		var part, invoice uuid.UUID
		var how, number, admission string
		var receipt *string
		var amount int64
		var noEntry, voided, undone, reversed bool
		if err := rows.Scan(&part, &how, &amount, &noEntry, &invoice, &number, &admission, &receipt, &voided, &undone, &reversed); err != nil {
			rows.Close()
			return c, err
		}
		invoicePath := "/invoices/" + invoice.String()
		switch {
		case receipt != nil && !voided && (undone || reversed):
			why := "was undone"
			if reversed {
				why = "went back to the payer"
			}
			c.ProblemList = append(c.ProblemList, CloseItem{Kind: ProblemReceiptNotVoided, AmountKobo: amount, Ref: *receipt, Path: PathReceipts + "?number=" + *receipt,
				Message: fmt.Sprintf("receipt %s (%s, %s) is still valid although its payment %s", *receipt, admission, invoicing.Naira(amount), why)})
		case undone:
			continue
		case noEntry && how == "exact":
			c.ProblemList = append(c.ProblemList, CloseItem{Kind: ProblemCreditWithoutEntry, AmountKobo: amount, Ref: number, Path: invoicePath,
				Message: fmt.Sprintf("%s counted towards %s (%s) as paid into the student's account has no money log entry", invoicing.Naira(amount), number, admission)})
		case receipt == nil:
			c.ProblemList = append(c.ProblemList, CloseItem{Kind: ProblemPartWithoutReceipt, AmountKobo: amount, Ref: number, Path: invoicePath,
				Message: fmt.Sprintf("%s counted towards %s (%s) has no receipt", invoicing.Naira(amount), number, admission)})
		}
	}
	rows.Close()
	return c, rows.Err()
}

// HowWordsDesk is how money was paid at the desk, for people.
func HowWordsDesk(how string) string {
	switch how {
	case "cash":
		return "cash"
	case "transfer":
		return "transfer"
	case "pos":
		return "POS"
	}
	return "another way"
}

// shortID is the first eight characters of an id, enough to find it.
func shortID(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

// CloseText is a close in words, for the message a template sends the
// proprietor (the channel puts the school's name before it): the day's
// figures, then everything that waits or does not reconcile with where to
// fix it (publicURL is the web app's address, before each path), the
// problems first. At most MaxCloseText characters.
func CloseText(c Close, publicURL string) string {
	n := invoicing.Naira
	var b strings.Builder
	fmt.Fprintf(&b, "Daily close, %s", dateWords(c.Day))
	if c.Term != "" {
		fmt.Fprintf(&b, " (%s)", c.Term)
	}
	b.WriteString(".\n")
	fmt.Fprintf(&b, "Money in on students: %s (%d payments): %s into account numbers, %s at the desk.\n", n(c.MoneyIn.Kobo), c.MoneyIn.Count, n(c.IntoAccounts.Kobo), n(c.AtTheDesk.Kobo))
	fmt.Fprintf(&b, "Matched to fees: %s by itself, %s by a person. Not matched: %s.\n", n(c.MatchedAuto.Kobo), n(c.MatchedPerson.Kobo), n(c.Unmatched.Kobo))
	if c.LookbackFound.Count > 0 {
		fmt.Fprintf(&b, "Found only by the look-back: %s (%d).\n", n(c.LookbackFound.Kobo), c.LookbackFound.Count)
	}
	fmt.Fprintf(&b, "Waiting for a second person: %d. Families saying they paid, not found: %d.\n", len(c.Pending), len(c.Claims))
	if c.Reconciles {
		b.WriteString("Everything reconciles.")
	} else {
		fmt.Fprintf(&b, "DOES NOT RECONCILE: %d problem(s).", c.Problems)
	}
	base := strings.TrimRight(publicURL, "/")
	lists := []struct {
		head  string
		items []CloseItem
	}{{"Problems", c.ProblemList}, {"Waiting", c.Pending}, {"Claims", c.Claims}, {"Not matched", c.UnmatchedList}}
	for _, l := range lists {
		if len(l.items) == 0 {
			continue
		}
		head := "\n\n" + l.head + ":"
		if b.Len()+len(head) > MaxCloseText-60 {
			break
		}
		b.WriteString(head)
		shown := 0
		for _, it := range l.items {
			line := "\n- " + it.Message + " → " + base + it.Path
			if b.Len()+len(line)+40 > MaxCloseText {
				break
			}
			b.WriteString(line)
			shown++
		}
		if left := len(l.items) - shown; left > 0 {
			fmt.Fprintf(&b, "\n- and %d more", left)
		}
	}
	return b.String()
}

// MaxCloseText caps the close's text (a notify@1 message takes 1,500
// characters).
const MaxCloseText = 1400

// --- running it ---

// Closed is what a nightly run did.
type Closed struct {
	Schools, Taken, NotReconciled int
}

// CloseYesterday closes yesterday (Lagos) for every school with the area
// on. Run it hourly from the invoice tick: a day is closed once, so
// running it often changes nothing but how soon after midnight.
func (s *Schools) CloseYesterday(ctx context.Context) (Closed, error) {
	rows, err := s.Pool.Query(ctx, `SELECT tenant_id FROM taskiem_schools_enabled_tenants()`)
	if err != nil {
		return Closed{}, err
	}
	tenants, err := pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
	if err != nil {
		return Closed{}, err
	}
	var out Closed
	day := s.Yesterday()
	for _, t := range tenants {
		out.Schools++
		var c Close
		var fresh bool
		err := s.In(ctx, t, func(tx pgx.Tx) error {
			var err error
			c, fresh, err = s.CloseDay(ctx, tx, t, day, "platform")
			return err
		})
		if err != nil {
			if ctx.Err() != nil {
				return out, nil //nolint:nilerr // stopping
			}
			s.log().Warn("schools: daily close", "tenant", t, "day", day.Format(time.DateOnly), "err", err)
			continue
		}
		if fresh {
			out.Taken++
			if !c.Reconciles {
				out.NotReconciled++
			}
		}
	}
	return out, nil
}

// --- the connector's reads ---

// CloseFor is the close of a day (YYYY-MM-DD; yesterday when empty),
// taken now when it was not yet, in its own transaction.
func (s Service) CloseFor(ctx context.Context, tenant uuid.UUID, day, by string) (Close, error) {
	d := s.Yesterday()
	if day != "" {
		var err error
		if d, err = Day(day); err != nil {
			return Close{}, err
		}
	}
	var out Close
	err := s.In(ctx, tenant, func(tx pgx.Tx) error {
		var err error
		out, _, err = s.CloseDay(ctx, tx, tenant, d, by)
		return err
	})
	return out, err
}

// OverviewOf is Overview in its own transaction.
func (s Service) OverviewOf(ctx context.Context, tenant, term uuid.UUID) (Overview, error) {
	var out Overview
	err := s.In(ctx, tenant, func(tx pgx.Tx) error {
		var err error
		out, err = s.Overview(ctx, tx, tenant, term)
		return err
	})
	return out, err
}
