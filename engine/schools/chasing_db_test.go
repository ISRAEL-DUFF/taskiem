package schools_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/schools"
)

// chaseWorld is a school whose first term's fees were due in the past:
// two siblings in one family, a third student in another, a fourth with
// no family, all invoiced and sent; the clock is the test's.
type chaseWorld struct {
	*world
	j1         schools.Class
	term       schools.Term
	fam, other schools.Family
	a, b, c, d schools.StudentDetail
	clock      time.Time
}

const dueOn = "2026-09-07"

func newChaseWorld(t *testing.T) *chaseWorld {
	w := &chaseWorld{world: newWorld(t)}
	schools.WireReceipts(w.s.Invoices, w.s)
	w.clock = time.Date(2026, 9, 17, 9, 0, 0, 0, time.UTC) // ten days past due
	w.s.Now = func() time.Time { return w.clock }
	tn := w.env.Tenant
	w.j1 = w.class("JSS 1", "", 7)
	w.in(func(tx pgx.Tx) error {
		sess, err := w.s.AddSession(ctx, tx, tn, "2026/2027", "user:ada")
		if err != nil {
			return err
		}
		if w.term, err = w.s.AddTerm(ctx, tx, tn, sess.ID, schools.TermIn{Name: "First term", StartsOn: "2026-09-01", EndsOn: "2099-12-11", FeesDueOn: dueOn}, "user:ada"); err != nil {
			return err
		}
		if w.fam, err = w.s.AddFamily(ctx, tx, tn, []schools.GuardianIn{{Name: "Ngozi Eze", Phone: "08031234567"}}, "user:ada"); err != nil {
			return err
		}
		w.other, err = w.s.AddFamily(ctx, tx, tn, []schools.GuardianIn{{Name: "Bola Ade", Phone: "08039876543"}}, "user:ada")
		return err
	})
	add := func(name, number string, family *uuid.UUID) schools.StudentDetail {
		var d schools.StudentDetail
		w.in(func(tx pgx.Tx) error {
			var err error
			d, err = w.s.AddStudent(ctx, tx, tn, schools.StudentIn{Name: name, AdmissionNumber: number, ClassID: w.j1.ID, FamilyID: family}, "user:ada")
			return err
		})
		return d
	}
	w.a, w.b, w.c, w.d = add("Ebube Eze", "GCA/001", &w.fam.ID), add("Chidi Eze", "GCA/002", &w.fam.ID), add("Tolu Ade", "GCA/003", &w.other.ID), add("Nobody Known", "GCA/004", nil)
	w.in(func(tx pgx.Tx) error {
		if _, err := w.s.StartTerm(ctx, tx, tn, w.term.ID, "user:ada"); err != nil {
			return err
		}
		if _, err := w.s.SetFees(ctx, tx, tn, w.term.ID, w.j1.ID, []schools.FeeItemIn{{Name: "Tuition", AmountKobo: 4_000_000, Kind: schools.Required}}, "user:ada"); err != nil {
			return err
		}
		p, err := w.s.PlanTerm(ctx, tx, tn, w.term.ID)
		if err != nil {
			return err
		}
		_, err = w.s.InvoiceTerm(ctx, tx, tn, w.term.ID, p.Hash, false, "user:ada")
		return err
	})
	if _, failed, err := w.s.SendTerm(ctx, tn, w.term.ID, "user:ada"); err != nil || len(failed) != 0 {
		t.Fatalf("sending the term: %v %v", err, failed)
	}
	return w
}

func (w *chaseWorld) debtors(f schools.DebtorFilter) schools.Debtors {
	w.t.Helper()
	var out schools.Debtors
	w.in(func(tx pgx.Tx) error {
		var err error
		out, err = w.s.Debtors(ctx, tx, w.env.Tenant, f)
		return err
	})
	return out
}

func (w *chaseWorld) debtor(d schools.Debtors, id uuid.UUID) *schools.Debtor {
	for i := range d.Debtors {
		if d.Debtors[i].StudentID == id {
			return &d.Debtors[i]
		}
	}
	return nil
}

func (w *chaseWorld) remind(key string, rung int, students ...uuid.UUID) schools.Reminded {
	w.t.Helper()
	out, err := w.s.SendReminders(ctx, w.env.Tenant, schools.RemindIn{Students: students, Rung: rung, Key: key}, "user:ada")
	if err != nil {
		w.t.Fatalf("reminding: %v", err)
	}
	return out
}

func (w *chaseWorld) promise(student, date, note string) (schools.Promise, error) {
	var p schools.Promise
	err := w.try(func(tx pgx.Tx) error {
		var err error
		p, err = w.s.RecordPromise(ctx, tx, w.env.Tenant, student, schools.PromiseIn{Date: date, Note: note}, "user:ada")
		return err
	})
	return p, err
}

func (w *chaseWorld) check() schools.Checked {
	w.t.Helper()
	c, err := w.s.CheckChasing(ctx, w.env.Tenant)
	if err != nil {
		w.t.Fatalf("checking: %v", err)
	}
	return c
}

func (w *chaseWorld) pay(number string, kobo int64) {
	w.t.Helper()
	if _, _, err := w.s.PostPayment(ctx, w.env.Tenant, schools.PostingIn{Student: number, AmountKobo: kobo, How: "cash", Note: "At the desk"}, "user:ada"); err != nil {
		w.t.Fatalf("paying: %v", err)
	}
}

func outcome(r schools.Reminded, id uuid.UUID) schools.RemindOutcome {
	for _, o := range r.Results {
		if o.StudentID == id {
			return o
		}
	}
	return schools.RemindOutcome{}
}

// Aging by days past the due date, Lagos time, with totals by bucket.
func TestAging(t *testing.T) {
	for days, want := range map[int]string{0: schools.BucketNotDue, 1: schools.Bucket0to7, 7: schools.Bucket0to7, 8: schools.Bucket7to14, 14: schools.Bucket7to14, 15: schools.Bucket14to30, 30: schools.Bucket14to30, 31: schools.BucketOver30} {
		if got := schools.BucketOf(days); got != want {
			t.Errorf("%d days: %s, want %s", days, got, want)
		}
	}
	for days, want := range map[int]int{0: 1, 7: 1, 8: 2, 14: 2, 15: 3, 60: 3} {
		if got := schools.RungForDays(days); got != want {
			t.Errorf("rung for %d days: %d, want %d", days, got, want)
		}
	}
	w := newChaseWorld(t)
	d := w.debtors(schools.DebtorFilter{})
	if len(d.Debtors) != 3 || d.Students != 3 || d.BalanceKobo != 12_000_000 || d.Totals[schools.Bucket7to14].Students != 3 || d.Totals[schools.Bucket0to7].Students != 0 {
		t.Fatalf("debtors %+v", d)
	}
	a := w.debtor(d, w.a.ID)
	if a == nil || a.DaysPastDue != 10 || a.Bucket != schools.Bucket7to14 || a.DueOn != dueOn || a.Name != "Ebube Eze" || a.Class != "JSS 1" || len(a.Invoices) != 1 || a.BalanceKobo != 4_000_000 || a.LastReminder != nil {
		t.Fatalf("a %+v", a)
	}
	// Not yet due before the date; over 30 days later; the filters.
	w.clock = time.Date(2026, 9, 6, 23, 30, 0, 0, time.UTC) // the 7th, 00:30 in Lagos: due today, not past
	if d := w.debtors(schools.DebtorFilter{}); d.Totals[schools.BucketNotDue].Students != 3 || d.Debtors[0].DaysPastDue != 0 {
		t.Fatalf("not due %+v", d.Totals)
	}
	w.clock = time.Date(2026, 10, 24, 9, 0, 0, 0, time.UTC)
	if d := w.debtors(schools.DebtorFilter{Bucket: schools.BucketOver30}); len(d.Debtors) != 3 || d.Debtors[0].DaysPastDue != 47 {
		t.Fatalf("over 30 %+v", d.Debtors)
	}
	if d := w.debtors(schools.DebtorFilter{Bucket: schools.Bucket0to7}); len(d.Debtors) != 0 || d.Students != 3 {
		t.Fatalf("bucket filter keeps the totals %+v", d)
	}
	j2 := w.class("JSS 2", "", 8)
	if d := w.debtors(schools.DebtorFilter{ClassID: &j2.ID}); d.Students != 0 || len(d.Debtors) != 0 {
		t.Fatalf("class filter %+v", d)
	}
	if err := w.try(func(tx pgx.Tx) error {
		_, err := w.s.Debtors(ctx, tx, w.env.Tenant, schools.DebtorFilter{Bucket: "soon"})
		return err
	}); !errors.Is(err, schools.ErrInvalid) {
		t.Errorf("bad bucket: %v", err)
	}
	// Paid in full, a student leaves the list.
	w.pay("GCA/003", 4_000_000)
	if d := w.debtors(schools.DebtorFilter{}); len(d.Debtors) != 2 || d.BalanceKobo != 8_000_000 {
		t.Fatalf("after paying %+v", d.Debtors)
	}
	// Part paid, the balance follows.
	w.pay("GCA/001", 1_000_000)
	if a := w.debtor(w.debtors(schools.DebtorFilter{}), w.a.ID); a == nil || a.BalanceKobo != 3_000_000 {
		t.Fatalf("part paid %+v", a)
	}
}

// The ladder's wording: placeholders checked, the link always carried,
// the school's own over the defaults, rendered with the family's values.
func TestReminderWording(t *testing.T) {
	if _, err := schools.CheckWording("Pay {balance} by {tomorrow}"); !errors.Is(err, schools.ErrInvalid) || !strings.Contains(err.Error(), "{tomorrow}") {
		t.Errorf("unknown placeholder: %v", err)
	}
	if _, err := schools.CheckWording(strings.Repeat("x", 601)); !errors.Is(err, schools.ErrInvalid) {
		t.Errorf("too long: %v", err)
	}
	if got, err := schools.CheckWording("  Pay {balance} for {child}.  "); err != nil || got != "Pay {balance} for {child}. Your fees page: {link}" {
		t.Errorf("link added: %q %v", got, err)
	}
	got := schools.RenderReminder("Pay {balance} for {child} ({account_number}) by {due_date}: {link}", schools.ReminderVars{Balance: "₦40,000", Child: "Ebube", AccountNumber: "0123456789 (Wema Bank)", DueDate: "7 Sep 2026", Link: "https://x/s/t"})
	if got != "Pay ₦40,000 for Ebube (0123456789 (Wema Bank)) by 7 Sep 2026: https://x/s/t" {
		t.Errorf("rendered %q", got)
	}
	w := newWorld(t)
	w.in(func(tx pgx.Tx) error {
		all, err := schools.ReminderWording(ctx, tx, w.env.Tenant)
		if err != nil || all[1] != schools.DefaultWording[1] || all[3] != schools.DefaultWording[3] {
			t.Fatalf("defaults %v %v", all, err)
		}
		if _, err := schools.SetReminderWording(ctx, tx, w.env.Tenant, map[int]string{2: "{what}"}, "user:ada"); !errors.Is(err, schools.ErrInvalid) {
			t.Fatalf("bad wording: %v", err)
		}
		if _, err := schools.SetReminderWording(ctx, tx, w.env.Tenant, map[int]string{4: "x"}, "user:ada"); !errors.Is(err, schools.ErrInvalid) {
			t.Fatalf("rung 4: %v", err)
		}
		was, err := schools.SetReminderWording(ctx, tx, w.env.Tenant, map[int]string{1: "Hello {child}: {balance}"}, "user:ada")
		if err != nil || len(was) != 0 {
			t.Fatalf("set: %v %v", was, err)
		}
		all, _ = schools.ReminderWording(ctx, tx, w.env.Tenant)
		custom, _ := schools.CustomWording(ctx, tx, w.env.Tenant)
		if all[1] != "Hello {child}: {balance} Your fees page: {link}" || all[2] != schools.DefaultWording[2] || len(custom) != 1 {
			t.Fatalf("merged %v %v", all, custom)
		}
		if _, err := schools.SetReminderWording(ctx, tx, w.env.Tenant, map[int]string{1: ""}, "user:ada"); err != nil {
			t.Fatal(err)
		}
		if custom, _ = schools.CustomWording(ctx, tx, w.env.Tenant); len(custom) != 0 {
			t.Fatalf("reset %v", custom)
		}
		if _, err := schools.SetPlanGraceDays(ctx, tx, w.env.Tenant, 61, "user:ada"); !errors.Is(err, schools.ErrInvalid) {
			t.Fatalf("grace 61: %v", err)
		}
		if was, err := schools.SetPlanGraceDays(ctx, tx, w.env.Tenant, 5, "user:ada"); err != nil || was != 0 {
			t.Fatalf("grace: %d %v", was, err)
		}
		if n, _ := schools.PlanGraceDays(ctx, tx, w.env.Tenant); n != 5 {
			t.Fatalf("grace read %d", n)
		}
		return nil
	})
}

// Reminders: at most fifty; one message per family with every child
// named; a promise not yet due skipped and said so; a student with
// nothing due skipped; once per key; one rung per family per day; the
// last reminder on Debtors; the scheduled pick oldest due first within
// the limit.
func TestReminders(t *testing.T) {
	w := newChaseWorld(t)
	tn := w.env.Tenant
	many := make([]uuid.UUID, 51)
	for i := range many {
		many[i] = w.a.ID
	}
	if _, err := w.s.SendReminders(ctx, tn, schools.RemindIn{Students: many, Rung: 1, Key: "k0"}, "user:ada"); !errors.Is(err, schools.ErrInvalid) || !strings.Contains(err.Error(), "at most 50") {
		t.Fatalf("51: %v", err)
	}
	if _, err := w.s.SendReminders(ctx, tn, schools.RemindIn{Students: []uuid.UUID{w.a.ID}, Rung: 1}, "user:ada"); !errors.Is(err, schools.ErrInvalid) {
		t.Fatalf("no key: %v", err)
	}
	if _, err := w.s.SendReminders(ctx, tn, schools.RemindIn{Students: []uuid.UUID{uuid.New()}, Rung: 1, Key: "k0"}, "user:ada"); !errors.Is(err, schools.ErrInvalid) {
		t.Fatalf("unknown student: %v", err)
	}
	if _, err := w.promise("GCA/003", "2026-09-25", "Salary on the 25th"); err != nil {
		t.Fatal(err)
	}
	r := w.remind("k1", 1, w.a.ID, w.b.ID, w.c.ID, w.d.ID)
	if r.Sent != 2 || r.Skipped != 2 || r.ByRung["1"] != 2 || r.Replayed || len(r.Results) != 4 {
		t.Fatalf("sent %+v", r)
	}
	if o := outcome(r, w.a.ID); o.Status != schools.RemindSent || o.Rung != 1 || o.Channels["sms"] != 1 || o.Nobody || o.BalanceKobo != 4_000_000 {
		t.Fatalf("a %+v", o)
	}
	if o := outcome(r, w.c.ID); o.Status != schools.RemindSkipped || o.Reason != "a promise to pay by 25 Sep 2026" {
		t.Fatalf("c %+v", o)
	}
	if o := outcome(r, w.d.ID); o.Status != schools.RemindSkipped || o.Reason != "nothing due" || o.Rung != 0 {
		t.Fatalf("d %+v", o)
	}
	// One message to the family's guardian, naming both children and
	// their balance together, with a fresh link.
	if n := w.count(`SELECT count(*) FROM school_family_messages WHERE purpose = 'reminder'`); n != 1 {
		t.Fatalf("%d family messages", n)
	}
	if n := w.count(`SELECT count(*) FROM school_family_links WHERE purpose = 'reminder'`); n != 1 {
		t.Fatalf("%d links", n)
	}
	w.in(func(tx pgx.Tx) error {
		var id uuid.UUID
		if err := tx.QueryRow(ctx, `SELECT id FROM school_family_messages WHERE purpose = 'reminder'`).Scan(&id); err != nil {
			return err
		}
		ft, ok, err := w.s.FamilyMessageOpen(ctx, tx, tn, id)
		text := ft.Text
		if err != nil || !ok || !strings.Contains(text, "₦80,000") || !strings.Contains(text, "Ebube Eze and Chidi Eze") || !strings.Contains(text, "https://forms.test/s/") || !strings.Contains(text, "7 Sep 2026") {
			t.Fatalf("text %q %v %v", text, ok, err)
		}
		// Outside WhatsApp's window the reminder template carries the same
		// facts, with the rung and the link for its button.
		if ft.Template == nil || ft.Template.Name != "taskiem_fee_reminder" || ft.Vars["balance"] != "₦80,000" || ft.Vars["child"] != "Ebube Eze and Chidi Eze" ||
			ft.Vars["due_date"] != "7 Sep 2026" || ft.Vars["rung"] != "reminder 1 of 3" || !strings.HasPrefix(ft.Vars["link"], "https://forms.test/s/") {
			t.Fatalf("template %+v", ft)
		}
		return nil
	})
	if n := w.count(`SELECT count(*) FROM school_reminders`); n != 2 {
		t.Fatalf("%d reminders", n)
	}
	// The same key again: the same answer, nothing more sent.
	if again := w.remind("k1", 1, w.a.ID, w.b.ID, w.c.ID, w.d.ID); !again.Replayed || again.Sent != 2 || w.count(`SELECT count(*) FROM school_reminders`) != 2 {
		t.Fatalf("replay %+v", again)
	}
	// The same rung the same day: skipped.
	if r := w.remind("k2", 1, w.a.ID); r.Sent != 0 || outcome(r, w.a.ID).Reason != "the family was sent reminder 1 today already" {
		t.Fatalf("twice a day %+v", r)
	}
	// Another rung goes; Debtors shows the last.
	if r := w.remind("k3", 2, w.a.ID); r.Sent != 1 || r.ByRung["2"] != 1 {
		t.Fatalf("rung 2 %+v", r)
	}
	d := w.debtors(schools.DebtorFilter{})
	if a := w.debtor(d, w.a.ID); a.LastReminder == nil || a.LastReminder.Rung != 2 || a.LastReminder.Nobody {
		t.Fatalf("last %+v", a.LastReminder)
	}
	if b := w.debtor(d, w.b.ID); b.LastReminder == nil || b.LastReminder.Rung != 1 {
		t.Fatalf("b's last %+v", b.LastReminder)
	}
	if c := w.debtor(d, w.c.ID); c.Promise == nil || c.Promise.Status != schools.PromiseOpen || c.Promise.Note != "Salary on the 25th" || c.PromiseMissed {
		t.Fatalf("c's promise %+v", c.Promise)
	}
	if d := w.debtors(schools.DebtorFilter{Has: "promise"}); len(d.Debtors) != 1 || d.Debtors[0].StudentID != w.c.ID {
		t.Fatalf("has promise %+v", d.Debtors)
	}
	// A scheduled run the next day: the rung by days past due, oldest due
	// first, at most the limit; the promise not counted.
	w.clock = w.clock.Add(24 * time.Hour)
	auto, err := w.s.SendReminders(ctx, tn, schools.RemindIn{Limit: 1, Key: "auto1"}, "workflow:x")
	if err != nil || auto.Sent != 1 || auto.ByRung["2"] != 1 || len(auto.Results) != 1 || outcome(auto, w.a.ID).Status != schools.RemindSent {
		t.Fatalf("auto %+v %v", auto, err)
	}
	// Without a limit: the promise is listed as skipped, and the family
	// reminded already today (for a sibling) is not sent the rung again.
	auto, err = w.s.SendReminders(ctx, tn, schools.RemindIn{Key: "auto2"}, "workflow:x")
	if err != nil || auto.Sent != 0 || auto.Skipped != 3 || outcome(auto, w.b.ID).Reason != "the family was sent reminder 2 today already" || outcome(auto, w.c.ID).Reason != "a promise to pay by 25 Sep 2026" || outcome(auto, w.a.ID).Reason != "the family was sent reminder 2 today already" {
		t.Fatalf("auto, no limit %+v %v", auto, err)
	}
	// A student's reminders, newest first.
	w.in(func(tx pgx.Tx) error {
		list, err := w.s.Reminders(ctx, tx, tn, w.a.ID, 10)
		if err != nil || len(list) != 3 || list[0].Rung != 2 || list[0].SentOn != "2026-09-18" || list[2].Rung != 1 {
			t.Fatalf("a's reminders %+v %v", list, err)
		}
		ch, err := w.s.StudentChasing(ctx, tx, tn, "GCA/001")
		if err != nil || ch.BalanceKobo != 4_000_000 || ch.DaysPastDue != 11 || len(ch.Reminders) != 3 || ch.Plan != nil {
			t.Fatalf("chasing %+v %v", ch, err)
		}
		return nil
	})
	// After the promise's date the reminder goes to C too.
	w.clock = time.Date(2026, 9, 26, 9, 0, 0, 0, time.UTC)
	if r := w.remind("k4", 3, w.c.ID); r.Sent != 1 {
		t.Fatalf("after the promise %+v", r)
	}
}

// Promises: one open per student, from today on, withdrawn by hand; the
// check after the date finds it kept when the balance moved, missed
// when not, each once, and Debtors flags the missed one.
func TestPromises(t *testing.T) {
	w := newChaseWorld(t)
	tn := w.env.Tenant
	if _, err := w.promise("GCA/001", "2026-09-10", "yesterday"); !errors.Is(err, schools.ErrInvalid) {
		t.Fatalf("a past date: %v", err)
	}
	if _, err := w.promise("GCA/004", "2026-09-20", "nothing due"); !errors.Is(err, schools.ErrState) {
		t.Fatalf("nothing due: %v", err)
	}
	p, err := w.promise("GCA/001", "2026-09-17", "today")
	if err != nil || p.Status != schools.PromiseOpen || p.BalanceKobo != 4_000_000 || p.Note != "today" || p.AdmissionNumber != "GCA/001" {
		t.Fatalf("promise %+v %v", p, err)
	}
	if _, err := w.promise("GCA/001", "2026-09-20", "again"); !errors.Is(err, schools.ErrState) {
		t.Fatalf("a second open promise: %v", err)
	}
	w.in(func(tx pgx.Tx) error {
		got, err := w.s.WithdrawPromise(ctx, tx, tn, p.ID, "user:ada")
		if err != nil || got.Status != schools.PromiseWithdrawn || got.ResolvedBy != "user:ada" {
			t.Fatalf("withdrawn %+v %v", got, err)
		}
		if _, err := w.s.WithdrawPromise(ctx, tx, tn, p.ID, "user:ada"); !errors.Is(err, schools.ErrState) {
			t.Fatalf("withdrawn twice: %v", err)
		}
		return nil
	})
	// Two promises for the 20th: one family pays, the other does not.
	if _, err := w.promise("GCA/001", "2026-09-20", "salary"); err != nil {
		t.Fatal(err)
	}
	if _, err := w.promise("GCA/003", "2026-09-20", "salary"); err != nil {
		t.Fatal(err)
	}
	// Still the 20th: nothing decided.
	w.clock = time.Date(2026, 9, 20, 20, 0, 0, 0, time.UTC)
	if c := w.check(); c.PromisesKept+c.PromisesMissed != 0 {
		t.Fatalf("on the day %+v", c)
	}
	w.pay("GCA/001", 500_000)
	w.clock = time.Date(2026, 9, 21, 1, 0, 0, 0, time.UTC) // 02:00 in Lagos, the day after
	if c := w.check(); c.PromisesKept != 1 || c.PromisesMissed != 1 {
		t.Fatalf("the day after %+v", c)
	}
	if c := w.check(); c.PromisesKept+c.PromisesMissed != 0 {
		t.Fatalf("decided once %+v", c)
	}
	d := w.debtors(schools.DebtorFilter{})
	if c := w.debtor(d, w.c.ID); !c.PromiseMissed || c.Promise == nil || c.Promise.Status != schools.PromiseMissed {
		t.Fatalf("c missed %+v", c)
	}
	if a := w.debtor(d, w.a.ID); a.PromiseMissed || a.Promise != nil {
		t.Fatalf("a kept %+v", a.Promise)
	}
	if d := w.debtors(schools.DebtorFilter{Has: "missed"}); len(d.Debtors) != 1 || d.Debtors[0].StudentID != w.c.ID {
		t.Fatalf("has missed %+v", d.Debtors)
	}
	// Missed, the family is reminded again; a new promise can be made.
	if r := w.remind("k1", 2, w.c.ID); r.Sent != 1 {
		t.Fatalf("after missing %+v", r)
	}
	if _, err := w.promise("GCA/003", "2026-09-30", "end of the month"); err != nil {
		t.Fatal(err)
	}
	w.in(func(tx pgx.Tx) error {
		list, err := w.s.Promises(ctx, tx, tn, schools.PromiseFilter{Student: &w.c.ID})
		if err != nil || len(list) != 2 || list[0].Status != schools.PromiseOpen || list[1].Status != schools.PromiseMissed {
			t.Fatalf("c's promises %+v %v", list, err)
		}
		all, _ := w.s.Promises(ctx, tx, tn, schools.PromiseFilter{Status: schools.PromiseKept})
		if len(all) != 1 || all[0].StudentID != w.a.ID {
			t.Fatalf("kept %+v", all)
		}
		return nil
	})
}

// Plans: an approved plan's instalments are the schedule; good standing
// while what was paid since approval covers the instalments past their
// date plus the grace; arrears from the first not covered, marked once
// by the check; reminders skip good standing and remind arrears by the
// days past the instalment; a family is told of a decision once.
func TestPlanStanding(t *testing.T) {
	w := newChaseWorld(t)
	tn := w.env.Tenant
	w.clock = time.Now().UTC()
	today := time.Now().In(time.FixedZone("WAT", 3600))
	day := func(n int) string { return today.AddDate(0, 0, n).Format(time.DateOnly) }
	var plan schools.PlanRequest
	w.in(func(tx pgx.Tx) error {
		req, err := w.s.RequestPlan(ctx, tx, tn, w.fam.ID, schools.PlanIn{StudentID: w.b.ID, Instalments: []schools.Instalment{{AmountKobo: 1_000_000, DueOn: day(2)}, {AmountKobo: 3_000_000, DueOn: day(40)}}, Message: "Please"})
		if err != nil {
			return err
		}
		plan, err = w.s.DecidePlan(ctx, tx, tn, req.ID, schools.PlanDecision{Approve: true}, "user:ada", nil)
		return err
	})
	standing := func() *schools.PlanStanding {
		var st *schools.PlanStanding
		w.in(func(tx pgx.Tx) error {
			var err error
			st, err = w.s.PlanStanding(ctx, tx, tn, w.b.ID, w.term.ID)
			return err
		})
		return st
	}
	st := standing()
	if st == nil || st.PlanID != plan.ID || st.State != schools.PlanGood || st.NextDueOn != day(2) || st.NextAmountKobo != 1_000_000 || st.PaidSinceKobo != 0 || len(st.Missed) != 0 {
		t.Fatalf("good %+v", st)
	}
	w.in(func(tx pgx.Tx) error {
		if none, err := w.s.PlanStanding(ctx, tx, tn, w.a.ID, uuid.Nil); err != nil || none != nil {
			t.Fatalf("a has no plan: %+v %v", none, err)
		}
		return nil
	})
	// In good standing, the family is skipped; Debtors says so.
	if r := w.remind("k1", 1, w.b.ID); r.Sent != 0 || !strings.HasPrefix(outcome(r, w.b.ID).Reason, "a payment plan in good standing (next instalment") {
		t.Fatalf("skipped for the plan %+v", r)
	}
	if d := w.debtors(schools.DebtorFilter{Has: "plan"}); len(d.Debtors) != 1 || d.Debtors[0].Plan == nil || d.Debtors[0].Plan.State != schools.PlanGood {
		t.Fatalf("has plan %+v", d.Debtors)
	}
	// Three days past the first instalment: arrears, unless the grace
	// covers it.
	w.clock = time.Now().UTC().AddDate(0, 0, 5)
	if st := standing(); st.State != schools.PlanArrears || len(st.Missed) != 1 || st.Missed[0].Instalment != 1 || st.Missed[0].ShortKobo != 1_000_000 || st.Missed[0].DaysPast != 3 || st.DueSoFarKobo != 1_000_000 || st.NextDueOn != day(40) {
		t.Fatalf("arrears %+v", st)
	}
	w.in(func(tx pgx.Tx) error { _, err := schools.SetPlanGraceDays(ctx, tx, tn, 5, "user:ada"); return err })
	if st := standing(); st.State != schools.PlanGood || st.GraceDays != 5 {
		t.Fatalf("within the grace %+v", st)
	}
	w.in(func(tx pgx.Tx) error { _, err := schools.SetPlanGraceDays(ctx, tx, tn, 0, "user:ada"); return err })
	// The check marks the instalment once.
	if c := w.check(); c.Arrears != 1 {
		t.Fatalf("marked %+v", c)
	}
	if c := w.check(); c.Arrears != 0 {
		t.Fatalf("marked once %+v", c)
	}
	w.in(func(tx pgx.Tx) error {
		list, err := w.s.Arrears(ctx, tx, tn, w.b.ID)
		if err != nil || len(list) != 1 || list[0].Instalment != 1 || list[0].ShortKobo != 1_000_000 || list[0].PlanID != plan.ID {
			t.Fatalf("arrears listed %+v %v", list, err)
		}
		return nil
	})
	// In arrears: reminded, by the days past the instalment (3: rung 1,
	// though the invoice is long past due).
	d := w.debtors(schools.DebtorFilter{Has: "arrears"})
	if len(d.Debtors) != 1 || d.Debtors[0].Plan.State != schools.PlanArrears {
		t.Fatalf("has arrears %+v", d.Debtors)
	}
	auto, err := w.s.SendReminders(ctx, tn, schools.RemindIn{Students: []uuid.UUID{w.b.ID}, Key: "auto"}, "workflow:x")
	if err != nil || auto.Sent != 1 || outcome(auto, w.b.ID).Rung != 1 {
		t.Fatalf("arrears reminded %+v %v", auto, err)
	}
	// Paying the instalment restores good standing; the mark stays.
	w.pay("GCA/002", 1_000_000)
	if st := standing(); st.State != schools.PlanGood || st.PaidSinceKobo != 1_000_000 || len(st.Missed) != 0 {
		t.Fatalf("paid up %+v", st)
	}
	if c := w.check(); c.Arrears != 0 {
		t.Fatalf("nothing new %+v", c)
	}
	// Paying the rest settles the plan.
	w.pay("GCA/002", 3_000_000)
	if st := standing(); st.State != schools.PlanSettled {
		t.Fatalf("settled %+v", st)
	}
	// A plan decided before the term started is not this term's.
	w.in(func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `UPDATE school_plan_requests SET decided_at = '2026-08-01' WHERE id = $1`, plan.ID); err != nil {
			return err
		}
		if st, err := w.s.PlanStanding(ctx, tx, tn, w.b.ID, w.term.ID); err != nil || st != nil {
			t.Fatalf("an old plan %+v %v", st, err)
		}
		if st, err := w.s.PlanStanding(ctx, tx, tn, w.b.ID, uuid.Nil); err != nil || st == nil {
			t.Fatalf("any plan %+v %v", st, err)
		}
		return nil
	})
}

// Telling a family of a decision, once: the platform tells at decision
// time and marks the plan told; the template's safety net lists what was
// not told and sends it once.
func TestPlanDecisionsTold(t *testing.T) {
	w := newChaseWorld(t)
	tn := w.env.Tenant
	w.clock = time.Now().UTC()
	day := time.Now().AddDate(0, 0, 10).Format(time.DateOnly)
	var approved, declined schools.PlanRequest
	w.in(func(tx pgx.Tx) error {
		req, err := w.s.RequestPlan(ctx, tx, tn, w.fam.ID, schools.PlanIn{StudentID: w.a.ID, Instalments: []schools.Instalment{{AmountKobo: 4_000_000, DueOn: day}}})
		if err != nil {
			return err
		}
		if approved, err = w.s.DecidePlan(ctx, tx, tn, req.ID, schools.PlanDecision{Approve: true}, "user:ada", nil); err != nil {
			return err
		}
		if req, err = w.s.RequestPlan(ctx, tx, tn, w.other.ID, schools.PlanIn{StudentID: w.c.ID, Instalments: []schools.Instalment{{AmountKobo: 4_000_000, DueOn: day}}}); err != nil {
			return err
		}
		declined, err = w.s.DecidePlan(ctx, tx, tn, req.ID, schools.PlanDecision{Approve: false, Note: "Pay in full"}, "user:ada", nil)
		return err
	})
	// The platform tells the approved one at once.
	if sent, err := w.s.PlanDecidedAfter(ctx, tn, approved); err != nil || sent.Channels["sms"] != 1 {
		t.Fatalf("told at once %+v %v", sent, err)
	}
	list := func(f schools.PlanDecisionFilter) []schools.PlanRequest {
		var out []schools.PlanRequest
		w.in(func(tx pgx.Tx) error {
			var err error
			out, err = w.s.PlanDecisions(ctx, tx, tn, f)
			return err
		})
		return out
	}
	if got := list(schools.PlanDecisionFilter{Untold: true}); len(got) != 1 || got[0].ID != declined.ID || got[0].ToldAt != nil {
		t.Fatalf("untold %+v", got)
	}
	if got := list(schools.PlanDecisionFilter{}); len(got) != 2 || got[0].ID != approved.ID || got[0].ToldAt == nil {
		t.Fatalf("all %+v", got)
	}
	if got := list(schools.PlanDecisionFilter{Status: "declined", Untold: true}); len(got) != 1 {
		t.Fatalf("declined %+v", got)
	}
	// Telling the approved one again sends nothing; the declined one is
	// told once.
	if told, err := w.s.TellPlanDecision(ctx, tn, approved.ID); err != nil || !told.Told || len(told.Sent.Channels) != 0 {
		t.Fatalf("told before %+v %v", told, err)
	}
	told, err := w.s.TellPlanDecision(ctx, tn, declined.ID)
	if err != nil || told.Told || told.Sent.Channels["sms"] != 1 || told.Plan.Status != "declined" {
		t.Fatalf("told now %+v %v", told, err)
	}
	if told, err := w.s.TellPlanDecision(ctx, tn, declined.ID); err != nil || !told.Told {
		t.Fatalf("told once %+v %v", told, err)
	}
	if got := list(schools.PlanDecisionFilter{Untold: true}); len(got) != 0 {
		t.Fatalf("all told %+v", got)
	}
	if n := w.count(`SELECT count(*) FROM school_family_messages WHERE purpose = 'decision'`); n != 2 {
		t.Fatalf("%d decision messages", n)
	}
	var req schools.PlanRequest
	w.in(func(tx pgx.Tx) error {
		var err error
		req, err = w.s.RequestPlan(ctx, tx, tn, w.fam.ID, schools.PlanIn{StudentID: w.b.ID, Instalments: []schools.Instalment{{AmountKobo: 4_000_000, DueOn: day}}})
		return err
	})
	if _, err := w.s.TellPlanDecision(ctx, tn, req.ID); !errors.Is(err, schools.ErrState) {
		t.Fatalf("undecided: %v", err)
	}
}
