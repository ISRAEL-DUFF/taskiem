package schools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/access"
	"github.com/israel-duff/taskiem/engine/invoicing"
	"github.com/israel-duff/taskiem/engine/notices"
	"github.com/israel-duff/taskiem/engine/sender"
	"github.com/israel-duff/taskiem/engine/whatsapp"
)

// Chasing (docs/spec/schools.md 11, B7; docs/schools.md#debtors):
// Debtors lists each student with a balance on a sent term invoice, aged
// by days past the invoice's due date (the term's fees due date); the
// bursar selects at most fifty and a rung, and each student's family gets
// one message per guardian with a fresh family page link, in the school's
// own wording for the rung. A family with a promise to pay not yet due,
// or an approved plan in good standing, is skipped and said so; a family
// whose plan is in arrears is reminded by the days past the instalment it
// missed. One send request is one row of school_reminder_sends under its
// key, so a retry messages nobody twice, and a family gets one message
// per rung per day.

// --- aging ---

// Aging buckets: days past the due date, Lagos time.
const (
	BucketNotDue = "not_due"
	Bucket0to7   = "0_7"
	Bucket7to14  = "7_14"
	Bucket14to30 = "14_30"
	BucketOver30 = "over_30"
)

// Buckets are the aging buckets in order.
var Buckets = []string{BucketNotDue, Bucket0to7, Bucket7to14, Bucket14to30, BucketOver30}

// BucketWords are the buckets for people.
var BucketWords = map[string]string{BucketNotDue: "Not yet due", Bucket0to7: "0 to 7 days", Bucket7to14: "7 to 14 days", Bucket14to30: "14 to 30 days", BucketOver30: "Over 30 days"}

// BucketOf is the bucket of a number of days past due (0 or less: not
// yet due).
func BucketOf(days int) string {
	switch {
	case days <= 0:
		return BucketNotDue
	case days <= 7:
		return Bucket0to7
	case days <= 14:
		return Bucket7to14
	case days <= 30:
		return Bucket14to30
	}
	return BucketOver30
}

// RungForDays is the rung a scheduled reminder uses: 1 up to a week past
// due, 2 up to two weeks, 3 after.
func RungForDays(days int) int {
	switch {
	case days <= 7:
		return 1
	case days <= 14:
		return 2
	}
	return 3
}

var lagosOnce = sync.OnceValue(func() *time.Location {
	loc, err := time.LoadLocation("Africa/Lagos")
	if err != nil {
		return time.FixedZone("WAT", 3600)
	}
	return loc
})

// today is the date in Lagos, at midnight UTC for date arithmetic.
func (s *Schools) today() time.Time {
	t := s.now().In(lagosOnce())
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
}

// daysPast is how many days today is past a YYYY-MM-DD date; 0 for a
// date today or later, or none.
func daysPast(today time.Time, date string) int {
	d, ok := dateOnly(date)
	if !ok {
		return 0
	}
	n := int(today.Sub(d).Hours() / 24)
	if n < 0 {
		return 0
	}
	return n
}

// dateWords is "7 Sep 2026" for a YYYY-MM-DD date.
func dateWords(date string) string {
	d, ok := dateOnly(date)
	if !ok {
		return date
	}
	return d.Format("2 Jan 2006")
}

// --- the ladder's wording ---

// MaxWording is the longest a rung's wording may be.
const MaxWording = 600

// ReminderPlaceholders are what a rung's wording may name: {balance},
// {child}, {account_number}, {due_date} and {link}.
var ReminderPlaceholders = []string{"balance", "child", "account_number", "due_date", "link"}

// DefaultWording is each rung's wording until the school changes it
// (docs/spec/schools.md 11.1). The business's name goes before every
// message as it is sent, so none names the school.
var DefaultWording = map[int]string{
	1: "A friendly reminder: {balance} is outstanding on {child}'s school fees, due {due_date}. You can pay by transfer to the account number {account_number}. Your fees page: {link}",
	2: "{child}'s school fees of {balance} were due on {due_date} and are still outstanding. Please pay to the account number {account_number} this week, or ask for a payment plan on your fees page: {link}",
	3: "Final notice: {balance} is still outstanding on {child}'s school fees, which were due on {due_date}. Please pay to the account number {account_number} now or see the bursar; the school's next step follows. Your fees page: {link}",
}

var placeholderRe = regexp.MustCompile(`\{([a-z_]*)\}`)

// CheckWording checks a rung's wording: up to MaxWording characters, only
// the known placeholders; {link} is added at the end when missing, so
// every message carries the family's link.
func CheckWording(text string) (string, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return "", nil
	}
	if len([]rune(text)) > MaxWording {
		return "", invalid("a reminder's wording has up to %d characters", MaxWording)
	}
	for _, m := range placeholderRe.FindAllStringSubmatch(text, -1) {
		known := false
		for _, p := range ReminderPlaceholders {
			if m[1] == p {
				known = true
			}
		}
		if !known {
			return "", invalid("{%s} is not a placeholder; use {balance}, {child}, {account_number}, {due_date} or {link}", m[1])
		}
	}
	if !strings.Contains(text, "{link}") {
		text += " Your fees page: {link}"
	}
	return text, nil
}

// ReminderVars are what fills a rung's wording.
type ReminderVars struct {
	Balance       string
	Child         string
	AccountNumber string
	DueDate       string
	Link          string
}

// RenderReminder fills a rung's wording.
func RenderReminder(text string, v ReminderVars) string {
	r := strings.NewReplacer("{balance}", v.Balance, "{child}", v.Child, "{account_number}", v.AccountNumber, "{due_date}", v.DueDate, "{link}", v.Link)
	return strings.Join(strings.Fields(r.Replace(text)), " ")
}

// ReminderTemplateVars are the variables of the taskiem_fee_reminder
// WhatsApp template (docs/whatsapp.md#templates), which carries a
// reminder outside the 24-hour window: Meta approves fixed wording, so
// the school's own wording goes inside the window and by SMS only.
func ReminderTemplateVars(rung int, v ReminderVars, link string) map[string]string {
	words := fmt.Sprintf("reminder %d of 3", rung)
	if rung >= 3 {
		words = "final notice"
	}
	or := func(s string) string {
		if s == "" {
			return "-"
		}
		return s
	}
	return map[string]string{"rung": words, "balance": or(v.Balance), "child": or(v.Child), "due_date": or(v.DueDate), "account_number": or(v.AccountNumber), "link": link}
}

// ReminderSMS is the text an SMS gets: the wording with the naira sign
// written NGN, so the message stays in the SMS alphabet (a naira sign
// makes every character count double, docs/messages.md), and, when even
// so it would take more than three parts, a short form with the same
// facts.
func ReminderSMS(text string, v ReminderVars) string {
	plain := strings.ReplaceAll(RenderReminder(text, v), "₦", "NGN")
	if sender.Segments(strings.Repeat("x", 40)+plain) <= 3 {
		return plain
	}
	short := strings.ReplaceAll(fmt.Sprintf("School fees reminder: %s is outstanding for %s, due %s. Pay to the account number %s. Your fees page: %s", v.Balance, v.Child, v.DueDate, v.AccountNumber, v.Link), "₦", "NGN")
	return strings.Join(strings.Fields(short), " ")
}

// CustomWording is the school's own wording by rung: only the rungs it
// changed.
func CustomWording(ctx context.Context, tx pgx.Tx, tenant uuid.UUID) (map[int]string, error) {
	var raw map[string]string
	err := tx.QueryRow(ctx, `SELECT reminder_wording FROM school_settings WHERE tenant_id = $1`, tenant).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return map[int]string{}, nil
	}
	if err != nil {
		return nil, err
	}
	out := map[int]string{}
	for k, v := range raw {
		for r := 1; r <= 3; r++ {
			if k == fmt.Sprint(r) && v != "" {
				out[r] = v
			}
		}
	}
	return out, nil
}

// ReminderWording is each rung's wording as it is sent: the school's
// where set, the default otherwise.
func ReminderWording(ctx context.Context, tx pgx.Tx, tenant uuid.UUID) (map[int]string, error) {
	custom, err := CustomWording(ctx, tx, tenant)
	if err != nil {
		return nil, err
	}
	out := map[int]string{}
	for r := 1; r <= 3; r++ {
		out[r] = DefaultWording[r]
		if v := custom[r]; v != "" {
			out[r] = v
		}
	}
	return out, nil
}

// SetReminderWording sets the rungs given ("" takes a rung back to the
// default); it answers the school's own wording before.
func SetReminderWording(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, w map[int]string, by string) (map[int]string, error) {
	if err := need(ctx, tx, tenant); err != nil {
		return nil, err
	}
	was, err := CustomWording(ctx, tx, tenant)
	if err != nil {
		return nil, err
	}
	next := map[string]string{}
	for r, v := range was {
		next[fmt.Sprint(r)] = v
	}
	for r, v := range w {
		if r < 1 || r > 3 {
			return nil, invalid("a rung is 1, 2 or 3")
		}
		checked, err := CheckWording(v)
		if err != nil {
			return nil, err
		}
		if checked == "" {
			delete(next, fmt.Sprint(r))
		} else {
			next[fmt.Sprint(r)] = checked
		}
	}
	raw, _ := json.Marshal(next)
	_, err = tx.Exec(ctx, `UPDATE school_settings SET reminder_wording = $2, updated_by = $3, updated_at = now() WHERE tenant_id = $1`, tenant, raw, by)
	return was, err
}

// PlanGraceDays is how many days past an instalment's date a plan stays
// in good standing (0 by default).
func PlanGraceDays(ctx context.Context, tx pgx.Tx, tenant uuid.UUID) (int, error) {
	var n int
	err := tx.QueryRow(ctx, `SELECT plan_grace_days FROM school_settings WHERE tenant_id = $1`, tenant).Scan(&n)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, nil
	}
	return n, err
}

// SetPlanGraceDays sets it (0 to 60); it answers the setting before.
func SetPlanGraceDays(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, days int, by string) (int, error) {
	if days < 0 || days > 60 {
		return 0, invalid("the plan grace is 0 to 60 days")
	}
	if err := need(ctx, tx, tenant); err != nil {
		return 0, err
	}
	was, err := PlanGraceDays(ctx, tx, tenant)
	if err != nil {
		return 0, err
	}
	_, err = tx.Exec(ctx, `UPDATE school_settings SET plan_grace_days = $2, updated_by = $3, updated_at = now() WHERE tenant_id = $1`, tenant, days, by)
	return was, err
}

// --- debtors ---

// DebtorInvoice is one open invoice of a debtor.
type DebtorInvoice struct {
	InvoiceID uuid.UUID `json:"invoice_id"`
	Number    string    `json:"number"`
	TermID    uuid.UUID `json:"term_id"`
	Term      string    `json:"term"`
	DueOn     string    `json:"due_on,omitempty"`
	DueKobo   int64     `json:"due_kobo"`
}

// ReminderBrief is the last reminder a student's family got.
type ReminderBrief struct {
	Rung   int       `json:"rung"`
	At     time.Time `json:"at"`
	Nobody bool      `json:"nobody,omitempty"`
}

// Debtor is a student with a balance, as Debtors lists them.
type Debtor struct {
	StudentID       uuid.UUID  `json:"student_id"`
	AdmissionNumber string     `json:"admission_number"`
	Name            string     `json:"name"`
	Status          string     `json:"status"`
	ClassID         uuid.UUID  `json:"class_id"`
	Class           string     `json:"class"`
	FamilyID        *uuid.UUID `json:"family_id"`
	BalanceKobo     int64      `json:"balance_kobo"`
	// DueOn is the oldest open invoice's due date; DaysPastDue counts
	// whole days past it, Lagos time (0 when not yet due).
	DueOn       string          `json:"due_on,omitempty"`
	DaysPastDue int             `json:"days_past_due"`
	Bucket      string          `json:"bucket"`
	Invoices    []DebtorInvoice `json:"invoices"`
	// LastReminder is the last rung the family got, and when.
	LastReminder *ReminderBrief `json:"last_reminder,omitempty"`
	// Promise is the open promise, or the last one missed (PromiseMissed).
	Promise       *Promise `json:"promise,omitempty"`
	PromiseMissed bool     `json:"promise_missed,omitempty"`
	// Plan is the standing of the student's approved plan, when one is.
	Plan *PlanStanding `json:"plan,omitempty"`

	order int
}

// BucketTotal is how many students and how much in a bucket.
type BucketTotal struct {
	Students    int   `json:"students"`
	BalanceKobo int64 `json:"balance_kobo"`
}

// Debtors is the list, with its totals by bucket.
type Debtors struct {
	Debtors     []Debtor               `json:"debtors"`
	Totals      map[string]BucketTotal `json:"totals"`
	Students    int                    `json:"students"`
	BalanceKobo int64                  `json:"balance_kobo"`
	Today       string                 `json:"today"`
}

// DebtorFilter narrows Debtors. Has: promise (an open promise), missed
// (a missed promise), plan (a plan in good standing), arrears (a plan in
// arrears), none (no promise or plan).
type DebtorFilter struct {
	ClassID *uuid.UUID
	Bucket  string
	Has     string
}

// Debtors lists the students with a balance on a sent term invoice (this
// term's and earlier terms' still open), by class, with aging, the last
// reminder, any promise and the standing of any plan. The totals by
// bucket are those of the class filter, before the bucket and has
// filters.
func (s *Schools) Debtors(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, f DebtorFilter) (Debtors, error) {
	if err := need(ctx, tx, tenant); err != nil {
		return Debtors{}, err
	}
	if f.Bucket != "" && BucketWords[f.Bucket] == "" {
		return Debtors{}, invalid("bucket is not_due, 0_7, 7_14, 14_30 or over_30")
	}
	switch f.Has {
	case "", "promise", "missed", "plan", "arrears", "none":
	default:
		return Debtors{}, invalid("has is promise, missed, plan, arrears or none")
	}
	all, err := s.allDebtors(ctx, tx, tenant)
	if err != nil {
		return Debtors{}, err
	}
	out := Debtors{Debtors: []Debtor{}, Totals: map[string]BucketTotal{}, Today: s.today().Format(time.DateOnly)}
	for _, b := range Buckets {
		out.Totals[b] = BucketTotal{}
	}
	for _, d := range all {
		if f.ClassID != nil && d.ClassID != *f.ClassID {
			continue
		}
		t := out.Totals[d.Bucket]
		t.Students++
		t.BalanceKobo += d.BalanceKobo
		out.Totals[d.Bucket] = t
		out.Students++
		out.BalanceKobo += d.BalanceKobo
		if f.Bucket != "" && d.Bucket != f.Bucket {
			continue
		}
		switch f.Has {
		case "promise":
			if d.Promise == nil || d.Promise.Status != PromiseOpen {
				continue
			}
		case "missed":
			if !d.PromiseMissed {
				continue
			}
		case "plan":
			if d.Plan == nil || d.Plan.State != PlanGood {
				continue
			}
		case "arrears":
			if d.Plan == nil || d.Plan.State != PlanArrears {
				continue
			}
		case "none":
			if d.Promise != nil || d.Plan != nil {
				continue
			}
		}
		out.Debtors = append(out.Debtors, d)
	}
	return out, nil
}

// allDebtors is every student with a balance, by class and admission
// number, with their aging, last reminder, promise and plan standing.
func (s *Schools) allDebtors(ctx context.Context, tx pgx.Tx, tenant uuid.UUID) ([]Debtor, error) {
	today := s.today()
	rows, err := tx.Query(ctx, `SELECT ti.student_id, s.admission_number, s.details, s.status, s.family_id, s.class_id, c.name, c.arm, c.class_order,
			i.id, i.number, ti.term_id, t.name || ' ' || ss.label, COALESCE(i.due_on::text, ''), i.total_kobo - i.paid_kobo
		FROM school_term_invoices ti JOIN customer_invoices i ON i.id = ti.invoice_id JOIN school_students s ON s.id = ti.student_id
		JOIN school_classes c ON c.id = s.class_id JOIN school_terms t ON t.id = ti.term_id JOIN school_sessions ss ON ss.id = t.session_id
		WHERE ti.tenant_id = $1 AND i.sent_at IS NOT NULL AND i.status IN ('sent', 'part_paid', 'overdue') AND i.total_kobo > i.paid_kobo
		ORDER BY c.class_order, c.name, c.arm, upper(s.admission_number), i.due_on NULLS LAST, i.number`, tenant)
	if err != nil {
		return nil, err
	}
	type row struct {
		d       Debtor
		details map[string]any
		arm     string
		inv     DebtorInvoice
	}
	list, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (row, error) {
		var x row
		err := r.Scan(&x.d.StudentID, &x.d.AdmissionNumber, &x.details, &x.d.Status, &x.d.FamilyID, &x.d.ClassID, &x.d.Class, &x.arm, &x.d.order,
			&x.inv.InvoiceID, &x.inv.Number, &x.inv.TermID, &x.inv.Term, &x.inv.DueOn, &x.inv.DueKobo)
		return x, err
	})
	if err != nil {
		return nil, err
	}
	var out []Debtor
	index := map[uuid.UUID]int{}
	for _, x := range list {
		i, ok := index[x.d.StudentID]
		if !ok {
			d := x.d
			d.Class = Label(d.Class, x.arm)
			var sd studentSealed
			erased, err := s.open(ctx, tx, tenant, x.details, &sd)
			if err != nil {
				return nil, err
			}
			d.Name = sd.Name
			if erased {
				d.Name = Erased
			}
			d.Invoices = []DebtorInvoice{}
			out = append(out, d)
			i = len(out) - 1
			index[d.StudentID] = i
		}
		d := &out[i]
		d.Invoices = append(d.Invoices, x.inv)
		d.BalanceKobo += x.inv.DueKobo
		if x.inv.DueOn != "" && (d.DueOn == "" || x.inv.DueOn < d.DueOn) {
			d.DueOn = x.inv.DueOn
		}
	}
	if len(out) == 0 {
		return []Debtor{}, nil
	}
	for i := range out {
		out[i].DaysPastDue = daysPast(today, out[i].DueOn)
		out[i].Bucket = BucketOf(out[i].DaysPastDue)
	}
	// The last reminder of each student.
	rows, err = tx.Query(ctx, `SELECT DISTINCT ON (student_id) student_id, rung, created_at, nobody FROM school_reminders WHERE tenant_id = $1 ORDER BY student_id, created_at DESC`, tenant)
	if err != nil {
		return nil, err
	}
	type last struct {
		student uuid.UUID
		b       ReminderBrief
	}
	lasts, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (last, error) {
		var l last
		return l, r.Scan(&l.student, &l.b.Rung, &l.b.At, &l.b.Nobody)
	})
	if err != nil {
		return nil, err
	}
	for _, l := range lasts {
		if i, ok := index[l.student]; ok {
			b := l.b
			out[i].LastReminder = &b
		}
	}
	// The open promise, or the last one missed.
	promises, err := s.Promises(ctx, tx, tenant, PromiseFilter{Latest: true})
	if err != nil {
		return nil, err
	}
	for _, p := range promises {
		if i, ok := index[p.StudentID]; ok && (p.Status == PromiseOpen || p.Status == PromiseMissed) {
			p := p
			out[i].Promise = &p
			out[i].PromiseMissed = p.Status == PromiseMissed
		}
	}
	// The standing of each approved plan.
	standings, err := s.planStandings(ctx, tx, tenant, uuid.Nil)
	if err != nil {
		return nil, err
	}
	for _, st := range standings {
		if i, ok := index[st.StudentID]; ok {
			st := st
			out[i].Plan = &st
		}
	}
	return out, nil
}

// studentBalance is what a student owes on sent term invoices now.
func studentBalance(ctx context.Context, tx pgx.Tx, tenant, student uuid.UUID) (int64, error) {
	var n int64
	err := tx.QueryRow(ctx, `SELECT COALESCE(sum(i.total_kobo - i.paid_kobo), 0) FROM school_term_invoices ti JOIN customer_invoices i ON i.id = ti.invoice_id
		WHERE ti.tenant_id = $1 AND ti.student_id = $2 AND i.sent_at IS NOT NULL AND i.status IN ('sent', 'part_paid', 'overdue')`, tenant, student).Scan(&n)
	return n, err
}

// --- reminders ---

// MaxRemind is the most students one send may reach: more than a bursar
// selects on purpose, far less than "select all" at a school of six
// hundred.
const MaxRemind = 50

// RemindIn is a send: the students chosen (at most MaxRemind), or, for a
// scheduled run, the debtors past due (of one class when ClassID is set),
// oldest due first, at most Limit; and the rung (0: by days past due).
type RemindIn struct {
	Students []uuid.UUID
	ClassID  *uuid.UUID
	Rung     int
	Limit    int
	// Key makes a retried request the same send.
	Key string
}

// Reminder outcomes.
const (
	RemindSent    = "sent"
	RemindSkipped = "skipped"
)

// RemindOutcome is what happened for one student.
type RemindOutcome struct {
	StudentID       uuid.UUID      `json:"student_id"`
	AdmissionNumber string         `json:"admission_number"`
	FamilyID        *uuid.UUID     `json:"family_id,omitempty"`
	Rung            int            `json:"rung,omitempty"`
	Status          string         `json:"status"`
	Reason          string         `json:"reason,omitempty"`
	BalanceKobo     int64          `json:"balance_kobo,omitempty"`
	Channels        map[string]int `json:"channels,omitempty"`
	Nobody          bool           `json:"nobody,omitempty"`
}

// Reminded is what a send did.
type Reminded struct {
	Key     string          `json:"key"`
	Sent    int             `json:"sent"`
	Skipped int             `json:"skipped"`
	ByRung  map[string]int  `json:"by_rung"`
	Results []RemindOutcome `json:"results"`
	// Replayed: the key was seen before; this is what the first send did.
	Replayed bool `json:"replayed,omitempty"`
}

// SendReminders sends the chosen students' families a reminder, once per
// key (docs/schools.md#the-ladder). A student with nothing due, no
// family, a promise not yet due, or a plan in good standing is skipped
// with why; a family that got the rung today already is not sent it
// again. A plan in arrears is reminded by the days past the instalment
// missed. The messages go out as the transaction commits.
func (s *Schools) SendReminders(ctx context.Context, tenant uuid.UUID, in RemindIn, by string) (Reminded, error) {
	in.Key = strings.TrimSpace(in.Key)
	if in.Key == "" || len(in.Key) > 200 {
		return Reminded{}, invalid("a send needs a key of up to 200 characters, so a retry sends nothing twice")
	}
	if in.Rung < 0 || in.Rung > 3 {
		return Reminded{}, invalid("the rung is 1, 2 or 3")
	}
	if len(in.Students) > MaxRemind {
		return Reminded{}, invalid("at most %d students per send, so a slip cannot message a whole school: %d were chosen", MaxRemind, len(in.Students))
	}
	if in.Limit <= 0 || in.Limit > MaxRemind {
		in.Limit = MaxRemind
	}
	var out Reminded
	err := s.In(ctx, tenant, func(tx pgx.Tx) error {
		if err := lock(ctx, tx, tenant); err != nil {
			return err
		}
		var raw []byte
		err := tx.QueryRow(ctx, `SELECT result FROM school_reminder_sends WHERE tenant_id = $1 AND key = $2`, tenant, in.Key).Scan(&raw)
		if err == nil {
			if err := json.Unmarshal(raw, &out); err != nil {
				return err
			}
			out.Replayed = true
			return nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		if out, err = s.remind(ctx, tx, tenant, in, by); err != nil {
			return err
		}
		raw, _ = json.Marshal(out)
		_, err = tx.Exec(ctx, `INSERT INTO school_reminder_sends (tenant_id, key, result, created_by) VALUES ($1, $2, $3, $4)`, tenant, in.Key, raw, by)
		return err
	})
	return out, err
}

// chosen is a debtor picked for a send, with the rung and why it is
// skipped, if it is.
type chosen struct {
	d      Debtor
	rung   int
	reason string
}

func (s *Schools) remind(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, in RemindIn, by string) (Reminded, error) {
	out := Reminded{Key: in.Key, ByRung: map[string]int{"1": 0, "2": 0, "3": 0}, Results: []RemindOutcome{}}
	all, err := s.allDebtors(ctx, tx, tenant)
	if err != nil {
		return out, err
	}
	byID := map[uuid.UUID]Debtor{}
	for _, d := range all {
		byID[d.StudentID] = d
	}
	today := s.today()
	// skipWhy is why a debtor is not reminded now, "" when they are.
	skipWhy := func(d Debtor) string {
		switch {
		case d.FamilyID == nil:
			return "no family to send to: add a guardian on Families"
		case d.Promise != nil && d.Promise.Status == PromiseOpen && daysPast(today, d.Promise.PromisedOn) == 0:
			return "a promise to pay by " + dateWords(d.Promise.PromisedOn)
		case d.Plan != nil && d.Plan.State == PlanGood:
			if d.Plan.NextDueOn != "" {
				return "a payment plan in good standing (next instalment " + dateWords(d.Plan.NextDueOn) + ")"
			}
			return "a payment plan in good standing"
		case d.Plan != nil && d.Plan.State == PlanSettled:
			return "a payment plan settled"
		}
		return ""
	}
	// rungFor is the rung a debtor gets: the one asked, else by the days
	// past due (past the instalment missed, for a plan in arrears).
	rungFor := func(d Debtor) int {
		if in.Rung > 0 {
			return in.Rung
		}
		days := d.DaysPastDue
		if d.Plan != nil && d.Plan.State == PlanArrears && len(d.Plan.Missed) > 0 {
			days = d.Plan.Missed[0].DaysPast
		}
		return RungForDays(days)
	}
	var picks []chosen
	if len(in.Students) > 0 {
		seen := map[uuid.UUID]bool{}
		for _, id := range in.Students {
			if seen[id] {
				continue
			}
			seen[id] = true
			d, ok := byID[id]
			if !ok {
				st, err := s.student(ctx, tx, tenant, id.String())
				if errors.Is(err, ErrNotFound) {
					return out, invalid("student %s is not one of the school's", id)
				}
				if err != nil {
					return out, err
				}
				picks = append(picks, chosen{d: Debtor{StudentID: st.ID, AdmissionNumber: st.AdmissionNumber, FamilyID: st.FamilyID}, reason: "nothing due"})
				continue
			}
			picks = append(picks, chosen{d: d, rung: rungFor(d), reason: skipWhy(d)})
		}
	} else {
		// A scheduled run: the debtors past due (or in arrears), oldest due
		// first, those skipped for a promise or a plan not counted.
		cands := make([]Debtor, 0, len(all))
		for _, d := range all {
			if in.ClassID != nil && d.ClassID != *in.ClassID {
				continue
			}
			if d.DaysPastDue == 0 && (d.Plan == nil || d.Plan.State != PlanArrears) {
				continue
			}
			cands = append(cands, d)
		}
		sort.SliceStable(cands, func(i, j int) bool {
			if cands[i].DueOn != cands[j].DueOn {
				return cands[i].DueOn < cands[j].DueOn
			}
			return strings.ToUpper(cands[i].AdmissionNumber) < strings.ToUpper(cands[j].AdmissionNumber)
		})
		n := 0
		for _, d := range cands {
			why := skipWhy(d)
			if why == "" {
				if n >= in.Limit {
					break
				}
				n++
			}
			picks = append(picks, chosen{d: d, rung: rungFor(d), reason: why})
		}
	}
	// By family, in the order chosen, one message per family and rung.
	type familySend struct {
		family uuid.UUID
		rung   int
		picks  []*chosen
	}
	var sends []*familySend
	byFamily := map[string]*familySend{}
	for i := range picks {
		c := &picks[i]
		if c.reason != "" {
			continue
		}
		k := c.d.FamilyID.String() + ":" + fmt.Sprint(c.rung)
		fs, ok := byFamily[k]
		if !ok {
			fs = &familySend{family: *c.d.FamilyID, rung: c.rung}
			byFamily[k] = fs
			sends = append(sends, fs)
		}
		fs.picks = append(fs.picks, c)
	}
	wording, err := ReminderWording(ctx, tx, tenant)
	if err != nil {
		return out, err
	}
	issuing, err := GetIssuing(ctx, tx, tenant)
	if err != nil {
		return out, err
	}
	outcomes := map[uuid.UUID]RemindOutcome{}
	for _, fs := range sends {
		// One message per rung per day per family, whatever the request.
		var already bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM school_reminders WHERE tenant_id = $1 AND family_id = $2 AND rung = $3 AND sent_on = $4)`,
			tenant, fs.family, fs.rung, today.Format(time.DateOnly)).Scan(&already); err != nil {
			return out, err
		}
		if already {
			for _, c := range fs.picks {
				c.reason = fmt.Sprintf("the family was sent reminder %d today already", fs.rung)
			}
			continue
		}
		v, err := s.reminderVars(ctx, tx, tenant, fs.picks, issuing.Channel)
		if err != nil {
			return out, err
		}
		text := wording[fs.rung]
		rung := fs.rung
		sent, err := s.SendFamily(ctx, tx, tenant, fs.family, FamilyMessage{Purpose: "reminder", Key: "reminder:" + in.Key + ":" + fs.family.String() + ":" + fmt.Sprint(fs.rung),
			Text:     func(link string) string { v.Link = link; return RenderReminder(text, v) },
			Short:    func(link string) string { v.Link = link; return ReminderSMS(text, v) },
			Template: &whatsapp.TplFeeReminder,
			Vars:     func(link string) map[string]string { return ReminderTemplateVars(rung, v, link) }}, by)
		if err != nil {
			return out, err
		}
		channels, _ := json.Marshal(sent.Channels)
		for _, c := range fs.picks {
			if _, err := tx.Exec(ctx, `INSERT INTO school_reminders (id, tenant_id, student_id, family_id, rung, balance_kobo, sent_on, channels, nobody, send_key, created_by)
				VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)`, newID(), tenant, c.d.StudentID, fs.family, fs.rung, c.d.BalanceKobo, today.Format(time.DateOnly), channels, sent.Nobody, in.Key, by); err != nil {
				return out, err
			}
			outcomes[c.d.StudentID] = RemindOutcome{Status: RemindSent, Channels: sent.Channels, Nobody: sent.Nobody}
		}
	}
	for _, c := range picks {
		o := RemindOutcome{StudentID: c.d.StudentID, AdmissionNumber: c.d.AdmissionNumber, FamilyID: c.d.FamilyID, Rung: c.rung, BalanceKobo: c.d.BalanceKobo, Status: RemindSkipped, Reason: c.reason}
		if done, ok := outcomes[c.d.StudentID]; ok {
			o.Status, o.Channels, o.Nobody, o.Reason = done.Status, done.Channels, done.Nobody, ""
			out.Sent++
			out.ByRung[fmt.Sprint(c.rung)]++
		} else {
			out.Skipped++
			if o.Reason == "" {
				o.Reason = "nothing due"
			}
			if c.reason == "nothing due" {
				o.Rung = 0
			}
		}
		out.Results = append(out.Results, o)
	}
	return out, nil
}

// reminderVars fills a family's message: the children named, their
// balance together, the earliest due date, and their account numbers on
// the school's issuing connection.
func (s *Schools) reminderVars(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, picks []*chosen, issuing string) (ReminderVars, error) {
	var v ReminderVars
	var names, accounts []string
	var balance int64
	due := ""
	for _, c := range picks {
		balance += c.d.BalanceKobo
		if c.d.DueOn != "" && (due == "" || c.d.DueOn < due) {
			due = c.d.DueOn
		}
		name := c.d.Name
		if name == "" || name == Erased {
			name = c.d.AdmissionNumber
		}
		names = append(names, name)
		list, err := s.StudentAccounts(ctx, tx, tenant, c.d.StudentID)
		if err != nil {
			return v, err
		}
		for _, a := range list {
			if a.Status == "issued" && a.AccountNumber != "" && (issuing == "" || a.Connection == issuing) {
				acct := a.AccountNumber
				if a.BankName != "" {
					acct += " (" + a.BankName + ")"
				}
				if len(picks) > 1 {
					acct = name + ": " + acct
				}
				accounts = append(accounts, acct)
				break
			}
		}
	}
	v.Balance = invoicing.Naira(balance)
	v.Child = joinNames(names)
	v.DueDate = dateWords(due)
	if due == "" {
		v.DueDate = "now"
	}
	v.AccountNumber = strings.Join(accounts, "; ")
	if v.AccountNumber == "" {
		v.AccountNumber = "on your fees page"
	}
	return v, nil
}

// joinNames is "Ebube", "Ebube and Chidi", "Ebube, Chidi and Ada".
func joinNames(names []string) string {
	switch len(names) {
	case 0:
		return ""
	case 1:
		return names[0]
	}
	return strings.Join(names[:len(names)-1], ", ") + " and " + names[len(names)-1]
}

// Reminder is a reminder sent for a student.
type Reminder struct {
	ID          uuid.UUID      `json:"id"`
	StudentID   uuid.UUID      `json:"student_id"`
	FamilyID    uuid.UUID      `json:"family_id"`
	Rung        int            `json:"rung"`
	BalanceKobo int64          `json:"balance_kobo"`
	SentOn      string         `json:"sent_on"`
	Channels    map[string]int `json:"channels"`
	Nobody      bool           `json:"nobody"`
	CreatedBy   string         `json:"created_by"`
	CreatedAt   time.Time      `json:"created_at"`
}

// Reminders lists a student's reminders, newest first.
func (s *Schools) Reminders(ctx context.Context, tx pgx.Tx, tenant, student uuid.UUID, limit int) ([]Reminder, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := tx.Query(ctx, `SELECT id, student_id, family_id, rung, balance_kobo, sent_on::text, channels, nobody, created_by, created_at FROM school_reminders
		WHERE tenant_id = $1 AND student_id = $2 ORDER BY created_at DESC LIMIT $3`, tenant, student, limit)
	if err != nil {
		return nil, err
	}
	list, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (Reminder, error) {
		var x Reminder
		err := r.Scan(&x.ID, &x.StudentID, &x.FamilyID, &x.Rung, &x.BalanceKobo, &x.SentOn, &x.Channels, &x.Nobody, &x.CreatedBy, &x.CreatedAt)
		if x.Channels == nil {
			x.Channels = map[string]int{}
		}
		return x, err
	})
	if list == nil {
		list = []Reminder{}
	}
	return list, err
}

// --- the student's page ---

// StudentChasing is what a student's page shows of chasing.
type StudentChasing struct {
	BalanceKobo int64           `json:"balance_kobo"`
	DueOn       string          `json:"due_on,omitempty"`
	DaysPastDue int             `json:"days_past_due"`
	Bucket      string          `json:"bucket"`
	Promises    []Promise       `json:"promises"`
	Reminders   []Reminder      `json:"reminders"`
	Plan        *PlanStanding   `json:"plan,omitempty"`
	Arrears     []PlanArrear    `json:"arrears"`
	Invoices    []DebtorInvoice `json:"invoices"`
}

// StudentChasing reads a student's balance, promises, reminders and plan
// standing.
func (s *Schools) StudentChasing(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, idOrNumber string) (StudentChasing, error) {
	if err := need(ctx, tx, tenant); err != nil {
		return StudentChasing{}, err
	}
	st, err := s.student(ctx, tx, tenant, idOrNumber)
	if err != nil {
		return StudentChasing{}, err
	}
	out := StudentChasing{Promises: []Promise{}, Reminders: []Reminder{}, Arrears: []PlanArrear{}, Invoices: []DebtorInvoice{}}
	all, err := s.allDebtors(ctx, tx, tenant)
	if err != nil {
		return out, err
	}
	for _, d := range all {
		if d.StudentID == st.ID {
			out.BalanceKobo, out.DueOn, out.DaysPastDue, out.Bucket, out.Invoices, out.Plan = d.BalanceKobo, d.DueOn, d.DaysPastDue, d.Bucket, d.Invoices, d.Plan
		}
	}
	if out.Bucket == "" {
		out.Bucket = BucketNotDue
	}
	if out.Plan == nil {
		if out.Plan, err = s.PlanStanding(ctx, tx, tenant, st.ID, uuid.Nil); err != nil {
			return out, err
		}
	}
	if out.Promises, err = s.Promises(ctx, tx, tenant, PromiseFilter{Student: &st.ID}); err != nil {
		return out, err
	}
	if out.Reminders, err = s.Reminders(ctx, tx, tenant, st.ID, 20); err != nil {
		return out, err
	}
	out.Arrears, err = s.Arrears(ctx, tx, tenant, st.ID)
	return out, err
}

// --- the checks ---

// Checked is what the chasing check did for a school.
type Checked struct {
	PromisesKept   int `json:"promises_kept"`
	PromisesMissed int `json:"promises_missed"`
	// Arrears is how many instalments were newly found missed.
	Arrears int `json:"arrears"`
}

// CheckChasing runs the chasing check for one school now (docs/schools.md
// #promises): promises past their date are kept or missed, each once; a
// plan's instalments past due plus the grace and not covered are marked
// in arrears, each once, and the bursars told.
func (s *Schools) CheckChasing(ctx context.Context, tenant uuid.UUID) (Checked, error) {
	var out Checked
	err := s.In(ctx, tenant, func(tx pgx.Tx) error {
		if err := need(ctx, tx, tenant); err != nil {
			return err
		}
		var err error
		if out.PromisesKept, out.PromisesMissed, err = s.checkPromises(ctx, tx, tenant); err != nil {
			return err
		}
		out.Arrears, err = s.checkPlans(ctx, tx, tenant)
		return err
	})
	return out, err
}

// CheckAllChasing runs the check for every school with the area on. Run
// it hourly: each promise and instalment is decided once, so running it
// often changes nothing but how soon.
func (s *Schools) CheckAllChasing(ctx context.Context) (Checked, error) {
	rows, err := s.Pool.Query(ctx, `SELECT tenant_id FROM taskiem_schools_enabled_tenants()`)
	if err != nil {
		return Checked{}, err
	}
	tenants, err := pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
	if err != nil {
		return Checked{}, err
	}
	var total Checked
	for _, t := range tenants {
		c, err := s.CheckChasing(ctx, t)
		if err != nil {
			if ctx.Err() != nil {
				return total, nil //nolint:nilerr // stopping
			}
			s.log().Warn("schools: chasing check", "tenant", t, "err", err)
			continue
		}
		total.PromisesKept += c.PromisesKept
		total.PromisesMissed += c.PromisesMissed
		total.Arrears += c.Arrears
	}
	return total, nil
}

// Holders are the active people holding a school permission through a
// built-in or a custom role.
func Holders(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, perm string) ([]uuid.UUID, error) {
	builtin, holding := access.BuiltinNames(), access.Holding(perm)
	rows, err := tx.Query(ctx, `SELECT DISTINCT ms.user_id FROM memberships ms JOIN users u ON u.id = ms.user_id
		WHERE ms.tenant_id = $1 AND u.status = 'active'
		  AND CASE WHEN ms.role = ANY ($2) THEN ms.role = ANY ($3)
		      ELSE EXISTS (SELECT 1 FROM roles r WHERE r.tenant_id = ms.tenant_id AND r.name = ms.role AND $4 = ANY (r.permissions)) END
		ORDER BY ms.user_id`, tenant, builtin, holding, perm)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
}

// tellBursars gives everyone who manages the school a notice.
func tellBursars(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, n notices.Notice) error {
	users, err := Holders(ctx, tx, tenant, access.SchoolManage)
	if err != nil || len(users) == 0 {
		return err
	}
	return notices.AddTx(ctx, tx, tenant, users, n)
}
