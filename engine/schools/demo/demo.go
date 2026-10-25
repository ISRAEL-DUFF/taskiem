// Package demo seeds a demonstration school for staging and demos
// (docs/schools.md#try-it-with-a-demo-school): a session of three terms
// with the current one open, six classes, forty students in twenty-five
// families with made-up names and phone numbers in a reserved range, fee
// structures with one pass-through line, the term invoiced, students'
// account numbers, and a realistic mix of what a bursar sees: most
// families paid in full into their child's account, some paid part, some
// at the desk (one waiting for a second person), a discount waiting, two
// families who say they paid, a credit held for a disagreeing reference,
// a payment that went back and reopened its invoice, a promise, a plan
// in good standing and one in arrears, a clearance override, and
// yesterday's close.
//
// Everything goes through the engine's own services, the money along the
// one payment path (the money log, then matching), so what the demo
// shows is what a school would see. Money log rows are append-only, so
// a demo is made in a tenant of its own and is never removed: seeding
// is keyed by a marker in the tenant's audit log, so running it again
// changes nothing, and a tenant with students of its own is refused.
package demo

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/connector"
	"github.com/israel-duff/taskiem/engine/effects"
	"github.com/israel-duff/taskiem/engine/invoicing"
	"github.com/israel-duff/taskiem/engine/matching"
	"github.com/israel-duff/taskiem/engine/moneylog"
	"github.com/israel-duff/taskiem/engine/schools"
	"github.com/israel-duff/taskiem/engine/sender"
)

// Channel is the demo rail's connection name: a rail that lives in this
// package, issues an account number per student without any service
// behind it, and takes no real money. It is used when the business has
// no connection that can issue account numbers, or when asked.
const Channel = "demo/school"

// Prefix starts every demo student's admission number.
const Prefix = "DEMO/"

// AuditAction is the audit log action that marks a tenant seeded.
const AuditAction = "school.demo.seeded"

// ErrOtherStudents refuses a tenant that has students of its own.
var ErrOtherStudents = errors.New("demo: the tenant has students that are not the demo's; seed a tenant of its own")

// ErrCutShort is a tenant where a seed was started and not finished.
var ErrCutShort = errors.New("demo: an earlier seed of this tenant was cut short; seed a fresh tenant")

var fakeRail = invoicing.Rail{Connector: "demo", Connection: "school", Name: "Demo rail", Events: map[string]bool{},
	Caps: map[string]connector.CapabilityMap{"virtual_account": {Ignores: []string{"customer.email", "customer.phone", "customer.bvn", "customer.nin", "account"}}}}

// Rails is the business's rails with the demo rail beside them: Real may
// be nil (a server with no rails).
type Rails struct{ Real invoicing.CapabilityRails }

// Capable lists the real rails that map capability, and the demo rail.
func (r Rails) Capable(ctx context.Context, tenant uuid.UUID, capability string) ([]invoicing.Rail, error) {
	var out []invoicing.Rail
	if r.Real != nil {
		list, err := r.Real.Capable(ctx, tenant, capability)
		if err != nil {
			return nil, err
		}
		out = append(out, list...)
	}
	if fakeRail.Maps(capability) {
		out = append(out, fakeRail)
	}
	return out, nil
}

// ResolveChannel resolves the demo rail's channel, else the real rails'.
func (r Rails) ResolveChannel(ctx context.Context, tenant uuid.UUID, channel, capability string) (invoicing.Rail, error) {
	if channel == Channel {
		if !fakeRail.Maps(capability) {
			return invoicing.Rail{}, invoicing.ErrNoRail
		}
		return fakeRail, nil
	}
	if r.Real == nil {
		return invoicing.Rail{}, invoicing.ErrNoRail
	}
	return r.Real.ResolveChannel(ctx, tenant, channel, capability)
}

// Call issues an account on the demo rail (one per key, as rails do),
// or passes a real rail's call on.
func (r Rails) Call(ctx context.Context, tenant uuid.UUID, rail invoicing.Rail, capability string, in map[string]any, key effects.KeyInput) (map[string]any, error) {
	if rail.Connector != fakeRail.Connector {
		if r.Real == nil {
			return nil, invoicing.ErrNoRail
		}
		return r.Real.Call(ctx, tenant, rail, capability, in, key)
	}
	if capability != "virtual_account" {
		return nil, fmt.Errorf("demo rail: %s is not mapped", capability)
	}
	sum := sha256.Sum256([]byte("demo account " + key.TenantID + " " + key.Seed + " " + key.StepID))
	k := hex.EncodeToString(sum[:])
	var digits strings.Builder
	for _, b := range sum[:8] {
		digits.WriteByte('0' + b%10)
	}
	out := map[string]any{"account_number": "90" + digits.String(), "bank_name": "Demo Bank", "reference": "tsk_" + k[:20]}
	if name, _ := in["account_name"].(string); name != "" {
		out["account_name"] = name
	}
	return out, nil
}

// Seeder seeds demo schools.
type Seeder struct {
	Schools *schools.Schools
	// MoneyLog records the families' payments, as the rails' events would
	// (the one payment path with Matcher).
	MoneyLog *moneylog.Log
	Matcher  *matching.Matcher
	// Messages is the queue the school's messages go out on: the demo takes
	// each guardian's daily allowance on it before anything is sent, so
	// nothing is queued to the demo's reserved numbers. nil: nothing is
	// sent anyway.
	Messages *sender.Queue
	Now      func() time.Time
}

// Options say where and how to seed.
type Options struct {
	Tenant uuid.UUID
	// Rail is the connection that issues the students' accounts
	// (connector/name); empty or "fake" is the demo rail (Channel).
	Rail string
	// Wallet is the wallet the numbers pay into, on rails that need one.
	Wallet string
	// Owner, Bursar and Approver are who does what, as the API records
	// actors (a user's id, or any label in tests): the bursar posts the
	// cash and asks for the discount, the approver decides plans and
	// overrides, the owner sets up the school.
	Owner, Bursar, Approver string
}

// Result is what a seed made.
type Result struct {
	// Seeded is false when the tenant had the demo already (nothing done).
	Seeded bool
	// Rail is the connection the accounts were issued on.
	Rail                                  string
	Classes, Families, Students, Invoiced int
	// AccountsIssued counts the students with an account number.
	AccountsIssued int
	TermID         uuid.UUID
	Term           string
	// CloseDay is the day closed (yesterday, Lagos).
	CloseDay string
	// Reconciles is whether that close reconciles.
	Reconciles bool
}

func (s *Seeder) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

var lagos = func() *time.Location {
	loc, err := time.LoadLocation("Africa/Lagos")
	if err != nil {
		return time.FixedZone("WAT", 3600)
	}
	return loc
}()

// today is today's date in Lagos, at midnight UTC for date arithmetic.
func (s *Seeder) today() time.Time {
	t := s.now().In(lagos)
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
}

// at is 10 in the morning, Lagos, so many days ago.
func (s *Seeder) at(daysAgo int) time.Time {
	t := s.now().In(lagos)
	return time.Date(t.Year(), t.Month(), t.Day(), 10, 0, 0, 0, lagos).AddDate(0, 0, -daysAgo)
}

func day(t time.Time, days int) string { return t.AddDate(0, 0, days).Format(time.DateOnly) }

// --- the school ---

type class struct {
	name    string
	order   int
	tuition int64
}

var classes = []class{
	{"Primary 5", 50, 8_000_000}, {"Primary 6", 60, 8_500_000}, {"JSS 1", 70, 11_000_000},
	{"JSS 2", 80, 11_500_000}, {"JSS 3", 90, 12_000_000}, {"SS 1", 100, 14_000_000},
}

// fees are a class's items: the tuition first, then what every class
// shares; JSS 3 collects the BECE fee on NECO's behalf.
func fees(c class) []schools.FeeItemIn {
	items := []schools.FeeItemIn{
		{Name: "Tuition", AmountKobo: c.tuition, Kind: schools.Required},
		{Name: "PTA levy", AmountKobo: 500_000, Kind: schools.Required},
		{Name: "ICT levy", AmountKobo: 750_000, Kind: schools.Required},
		{Name: "Development levy", AmountKobo: 1_000_000, Kind: schools.Required},
		{Name: "Transport", AmountKobo: 2_500_000, Kind: schools.Optional},
	}
	if c.name == "JSS 3" {
		items = append(items, schools.FeeItemIn{Name: "BECE exam fee", AmountKobo: 1_800_000, Kind: schools.Required, PassThroughFor: "NECO"})
	}
	return items
}

// The people: made up, Nigerian, no one real. Phones are in the 0800
// range, which no mobile network assigns.
var surnames = []string{"Adeyemi", "Okonkwo", "Bello", "Eze", "Olawale", "Nwachukwu", "Abubakar", "Ogunleye", "Chukwu", "Musa", "Balogun", "Okafor", "Yusuf",
	"Adebayo", "Obi", "Ibrahim", "Afolabi", "Nnamdi", "Danjuma", "Oyelaran", "Igwe", "Suleiman", "Akinwande", "Udoh", "Lawal"}

var children = []string{"Tomiwa", "Chiamaka", "Amina", "Kelechi", "Ayomide", "Nneka", "Faruk", "Bisi", "Ifeanyi", "Halima", "Tunde", "Ngozi", "Sadiq", "Folake",
	"Emeka", "Zainab", "Dayo", "Adaeze", "Usman", "Yemisi", "Obinna", "Aisha", "Seun", "Chidera", "Hassan", "Temitope", "Uche", "Maryam", "Kunle", "Oluchi",
	"Abdul", "Tolu", "Ikenna", "Fatima", "Segun", "Chinwe", "Bashir", "Funmi", "Ebuka", "Rukayat"}

var guardians = []string{"Adebola", "Ifeoma", "Hauwa", "Chinedu", "Bukola", "Obiageli", "Garba", "Folasade", "Nkem", "Aliyu", "Ronke", "Chika", "Hadiza",
	"Olumide", "Amaka", "Sani", "Titilayo", "Ebere", "Ladi", "Morenike", "Chinyere", "Umar", "Kemi", "Uduak", "Rasheed"}

// student is one of the forty: their index decides their class and
// family. Thirty students are fifteen pairs of siblings; ten are only
// children.
type student struct {
	i      int
	number string
	name   string
	family int // 0 to 24
	class  int // index in classes
}

func roster() []student {
	out := make([]student, 0, len(children))
	for i := range children {
		fam := i / 2
		if i >= 30 {
			fam = 15 + (i - 30)
		}
		out = append(out, student{i: i, number: fmt.Sprintf("%s%03d", Prefix, i+1), name: children[i] + " " + surnames[fam], family: fam, class: i % len(classes)})
	}
	return out
}

func guardianOf(family int) schools.GuardianIn {
	return schools.GuardianIn{Name: guardians[family] + " " + surnames[family], Phone: fmt.Sprintf("0800%07d", 100+family), Relationship: "Parent"}
}

// What each student's family does (by the student's index).
const (
	firstPartPaid  = 24 // 24 to 27 paid part of the fees into the account
	firstCash      = 28 // 28 to 30 paid at the desk; 31 waits for a second person
	cashPending    = 31
	firstClaim     = 32 // 32 and 33 say they paid
	heldCredit     = 34 // a credit with the right reference into another account
	promised       = 35
	planGood       = 36
	planArrears    = 37
	overridden     = 38
	discountWaits  = 39
	reversed       = 23 // paid in full, then the money went back
	siblingDiscunt = 1  // the second child of the first family has a sibling discount
)

// Seed seeds the demo into the tenant, once. A tenant seeded already
// answers Result{Seeded: false}; a tenant with students of its own is
// refused (ErrOtherStudents).
func (s *Seeder) Seed(ctx context.Context, o Options) (Result, error) {
	var res Result
	if o.Tenant == uuid.Nil {
		return res, errors.New("demo: a tenant is needed")
	}
	if o.Owner == "" {
		o.Owner = "system:demo"
	}
	if o.Bursar == "" {
		o.Bursar = o.Owner
	}
	if o.Approver == "" {
		o.Approver = o.Owner
	}
	if o.Rail == "" || o.Rail == "fake" {
		o.Rail = Channel
	}
	sch := s.Schools
	tn := o.Tenant
	in := func(fn func(tx pgx.Tx) error) error { return sch.In(ctx, tn, fn) }

	// Once only, and never over a school's own students.
	var seeded, others, mine bool
	if err := in(func(tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM audit_log WHERE tenant_id = $1 AND action = $2)`, tn, AuditAction).Scan(&seeded); err != nil {
			return err
		}
		return tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM school_students WHERE tenant_id = $1 AND admission_number NOT LIKE $2),
			EXISTS (SELECT 1 FROM school_students WHERE tenant_id = $1 AND admission_number LIKE $2)`, tn, Prefix+"%").Scan(&others, &mine)
	}); err != nil {
		return res, err
	}
	switch {
	case seeded:
		return res, nil
	case others:
		return res, ErrOtherStudents
	case mine:
		return res, ErrCutShort
	}

	// The area, the thresholds, the rail.
	issuing, err := sch.CheckIssuing(ctx, tn, schools.Issuing{Channel: o.Rail, Account: o.Wallet})
	if err != nil {
		return res, err
	}
	cash, discount := int64(10_000_000), int64(5_000_000)
	if err := in(func(tx pgx.Tx) error {
		if _, err := schools.SetEnabled(ctx, tx, tn, true, o.Owner); err != nil {
			return err
		}
		if _, err := schools.SetIssuing(ctx, tx, tn, issuing, o.Owner); err != nil {
			return err
		}
		if _, err := schools.SetCashThreshold(ctx, tx, tn, &cash, o.Owner); err != nil {
			return err
		}
		_, err := schools.SetDiscountThreshold(ctx, tx, tn, discount, o.Owner)
		return err
	}); err != nil {
		return res, err
	}
	res.Rail = issuing.Channel

	// Classes, the session and its terms: the first term started six weeks
	// ago, its fees due three weeks ago, so Debtors has something to age.
	today := s.today()
	classIDs := make([]uuid.UUID, len(classes))
	var first schools.Term
	if err := in(func(tx pgx.Tx) error {
		for i, c := range classes {
			cl, err := sch.AddClass(ctx, tx, tn, schools.ClassIn{Name: c.name, Order: c.order}, o.Owner)
			if err != nil {
				return err
			}
			classIDs[i] = cl.ID
		}
		year := today.Year()
		if today.Month() < time.September {
			year--
		}
		sess, err := sch.AddSession(ctx, tx, tn, fmt.Sprintf("%d/%d", year, year+1), o.Owner)
		if err != nil {
			return err
		}
		if first, err = sch.AddTerm(ctx, tx, tn, sess.ID, schools.TermIn{Name: "First term", StartsOn: day(today, -42), EndsOn: day(today, 50), FeesDueOn: day(today, -20)}, o.Owner); err != nil {
			return err
		}
		if _, err = sch.AddTerm(ctx, tx, tn, sess.ID, schools.TermIn{Name: "Second term", StartsOn: day(today, 81), EndsOn: day(today, 162), FeesDueOn: day(today, 95)}, o.Owner); err != nil {
			return err
		}
		_, err = sch.AddTerm(ctx, tx, tn, sess.ID, schools.TermIn{Name: "Third term", StartsOn: day(today, 190), EndsOn: day(today, 274), FeesDueOn: day(today, 204)}, o.Owner)
		return err
	}); err != nil {
		return res, err
	}
	res.Classes = len(classes)

	// The students, as a bursar would bring them: a file, checked, then
	// saved. Families group by the guardian's phone.
	students := roster()
	var csv strings.Builder
	csv.WriteString("Name,Admission number,Class,Guardian name,Phone,Relationship\n")
	for _, st := range students {
		g := guardianOf(st.family)
		fmt.Fprintf(&csv, "%s,%s,%s,%s,%s,%s\n", st.name, st.number, classes[st.class].name, g.Name, g.Phone, g.Relationship)
	}
	if err := in(func(tx pgx.Tx) error {
		plan, err := sch.Import(ctx, tx, tn, csv.String(), "", o.Owner)
		if err != nil {
			return err
		}
		saved, err := sch.Import(ctx, tx, tn, csv.String(), plan.Hash, o.Owner)
		if err != nil {
			return err
		}
		if saved.Added != len(students) {
			return fmt.Errorf("demo: the import added %d students, not %d", saved.Added, len(students))
		}
		res.Students, res.Families = saved.Added, len(saved.Families)
		return nil
	}); err != nil {
		return res, err
	}
	ids := map[string]uuid.UUID{}
	families := map[int]uuid.UUID{}
	if err := in(func(tx pgx.Tx) error {
		list, _, err := sch.Students(ctx, tx, tn, schools.Filter{Limit: 1000})
		if err != nil {
			return err
		}
		for _, x := range list {
			ids[x.AdmissionNumber] = x.ID
		}
		for _, st := range students {
			x, err := sch.Student(ctx, tx, tn, st.number)
			if err != nil {
				return err
			}
			if x.FamilyID == nil {
				return fmt.Errorf("demo: %s has no family", st.number)
			}
			families[st.family] = *x.FamilyID
		}
		return nil
	}); err != nil {
		return res, err
	}

	// The term starts (everyone enrolled), the fees are set, some families
	// take transport, the first family's second child has a sibling
	// discount (under the threshold).
	transport := map[int]uuid.UUID{}
	if err := in(func(tx pgx.Tx) error {
		if _, err := sch.StartTerm(ctx, tx, tn, first.ID, o.Owner); err != nil {
			return err
		}
		for i, c := range classes {
			set, err := sch.SetFees(ctx, tx, tn, first.ID, classIDs[i], fees(c), o.Owner)
			if err != nil {
				return err
			}
			for _, it := range set.Fees.Items {
				if it.Kind == schools.Optional {
					transport[i] = it.ID
				}
			}
		}
		for _, st := range students {
			if st.i%5 == 2 {
				if _, _, err := sch.SetOptions(ctx, tx, tn, ids[st.number], first.ID, []uuid.UUID{transport[st.class]}, o.Owner); err != nil {
					return err
				}
			}
		}
		pct := 10
		_, _, err := sch.AddDiscount(ctx, tx, tn, ids[students[siblingDiscunt].number], schools.DiscountIn{Kind: "sibling", Percent: &pct, Reason: "Second child in the school"}, o.Owner)
		return err
	}); err != nil {
		return res, err
	}
	res.TermID = first.ID

	// The term is invoiced and sent. The guardians' daily message allowance
	// is taken first, so no SMS is queued to the demo's numbers.
	if err := in(func(tx pgx.Tx) error {
		if s.Messages != nil {
			for fam := range families {
				phone, err := schools.NormalisePhone(guardianOf(fam).Phone)
				if err != nil {
					return err
				}
				if err := s.hold(ctx, tx, phone); err != nil {
					return err
				}
			}
		}
		plan, err := sch.PlanTerm(ctx, tx, tn, first.ID)
		if err != nil {
			return err
		}
		made, err := sch.InvoiceTerm(ctx, tx, tn, first.ID, plan.Hash, false, o.Owner)
		if err != nil {
			return err
		}
		res.Invoiced, res.Term = made.Created, plan.Term.Name+" "+plan.Term.Session
		return nil
	}); err != nil {
		return res, err
	}
	if _, failed, err := sch.SendTerm(ctx, tn, first.ID, o.Owner); err != nil {
		return res, err
	} else if len(failed) > 0 {
		for id, why := range failed {
			return res, fmt.Errorf("demo: sending invoice %s: %s", id, why)
		}
	}

	// Account numbers, in one batch, as the Account numbers page does.
	if err := s.issueAccounts(ctx, tn, o.Owner); err != nil {
		return res, err
	}
	accounts := map[string]schools.StudentAccount{}
	if err := in(func(tx pgx.Tx) error {
		for _, st := range students {
			list, err := sch.StudentAccounts(ctx, tx, tn, ids[st.number])
			if err != nil {
				return err
			}
			for _, a := range list {
				if a.Status == schools.AccountIssued {
					accounts[st.number] = a
				}
			}
		}
		return nil
	}); err != nil {
		return res, err
	}
	res.AccountsIssued = len(accounts)
	if res.AccountsIssued != len(students) {
		return res, fmt.Errorf("demo: %d of %d students were issued an account number", res.AccountsIssued, len(students))
	}
	totals := map[string]int64{}
	if err := in(func(tx pgx.Tx) error {
		list, err := sch.TermInvoices(ctx, tx, tn, first.ID, nil, "")
		for _, ti := range list {
			totals[ti.AdmissionNumber] = ti.TotalKobo
		}
		return err
	}); err != nil {
		return res, err
	}

	// A payment plan for two families, asked for and approved three weeks
	// ago: one is paid as agreed (below), one is not.
	if err := s.plans(ctx, o, students, ids, families, totals); err != nil {
		return res, err
	}

	// The money: into the students' accounts (matched by the permanent-
	// account rule, receipts issued), part payments, a credit into the
	// wrong account, one that went back.
	credit := func(st student, kobo int64, daysAgo int, number, reference string) (uuid.UUID, error) {
		a := accounts[st.number]
		if number == "" {
			number = a.AccountNumber
		}
		if reference == "" {
			reference = a.Reference
		}
		when := s.at(daysAgo)
		id, _, err := s.MoneyLog.Record(ctx, tn, moneylog.Entry{Direction: "in", Kind: "account_credit", Status: "settled", Channel: a.Connection,
			Reference: reference, AccountNumber: number, Credit: fmt.Sprintf("demo-credit-%03d-%d", st.i+1, daysAgo), Event: "virtual_account.credited", Source: "event",
			AmountKobo: &kobo, OccurredAt: &when, PayerName: strings.ToUpper(guardianOf(st.family).Name), PayerAccount: fmt.Sprintf("0%09d", 100+st.family)})
		return id, err
	}
	var reversedCredit uuid.UUID
	for _, st := range students {
		switch {
		case st.i < firstPartPaid:
			id, err := credit(st, totals[st.number], (st.i*7)%28+1, "", "")
			if err != nil {
				return res, err
			}
			if st.i == reversed {
				reversedCredit = id
			}
		case st.i < firstCash:
			if _, err := credit(st, totals[st.number]/2, 3, "", ""); err != nil {
				return res, err
			}
		case st.i == heldCredit:
			if _, err := credit(st, 5_000_000, 2, "9000000000", ""); err != nil {
				return res, err
			}
		case st.i == planGood:
			if _, err := credit(st, 5_000_000, 7, "", ""); err != nil {
				return res, err
			}
		}
	}
	if err := s.Matcher.Tenant(ctx, tn); err != nil {
		return res, err
	}
	// The money went back: the credit is reversed, its receipt voided, the
	// invoice open again.
	rev := s.at(1)
	if _, _, err := s.MoneyLog.Record(ctx, tn, moneylog.Entry{Direction: "in", Kind: "account_credit", Status: "reversed", Channel: accounts[students[reversed].number].Connection,
		Reference: fmt.Sprintf("demo-reversal-%03d", reversed+1), Event: "virtual_account.reversed", Source: "event", Corrects: &reversedCredit, OccurredAt: &rev}); err != nil {
		return res, err
	}
	if err := s.Matcher.TenantReversals(ctx, tn); err != nil {
		return res, err
	}

	// At the desk: three postings counted at once, one at the threshold
	// waiting for a second person.
	for _, p := range []struct {
		i    int
		kobo int64
		how  string
		days int
		note string
	}{{28, 4_000_000, "cash", 1, ""}, {29, 6_000_000, "transfer", 2, "Transfer to the school's account, seen on the statement"}, {30, 3_000_000, "pos", 1, ""},
		{cashPending, 12_000_000, "cash", 0, "Brought in by the grandmother"}} {
		st := students[p.i]
		posting, parts, err := sch.PostPayment(ctx, tn, schools.PostingIn{Student: st.number, AmountKobo: p.kobo, How: p.how, Date: day(today, -p.days), Note: p.note, Key: "demo:cash:" + st.number}, o.Bursar)
		var needs *schools.ApprovalNeeded
		switch {
		case errors.As(err, &needs):
			continue
		case err != nil:
			return res, err
		case posting.Status != schools.StatePosted:
			return res, fmt.Errorf("demo: the posting for %s is %s", st.number, posting.Status)
		}
		if err := sch.PostedAfter(ctx, tn, parts); err != nil {
			return res, err
		}
	}

	// Two families say they paid; a hardship discount at the threshold
	// waits for a second person; a promise; an override.
	if err := in(func(tx pgx.Tx) error {
		for n, i := range []int{firstClaim, firstClaim + 1} {
			st := students[i]
			if _, err := sch.RecordClaim(ctx, tx, tn, families[st.family], schools.ClaimIn{StudentID: ids[st.number], AmountKobo: totals[st.number], PaidOn: day(today, -2-n),
				How: "transfer", Reference: fmt.Sprintf("GTB/%05d", 4400+i)}, "family"); err != nil {
				return err
			}
		}
		amount := int64(6_000_000)
		_, _, err := sch.AddDiscount(ctx, tx, tn, ids[students[discountWaits].number], schools.DiscountIn{Kind: "hardship", AmountKobo: &amount, Reason: "Father lost his job in August"}, o.Bursar)
		var needs *schools.ApprovalNeeded
		if !errors.As(err, &needs) {
			if err == nil {
				err = errors.New("demo: the hardship discount was given without a second person")
			}
			return err
		}
		if _, err := sch.RecordPromise(ctx, tx, tn, students[promised].number, schools.PromiseIn{Date: day(today, 5), Note: "Salary comes on the 25th"}, o.Bursar); err != nil {
			return err
		}
		_, err = sch.AddOverride(ctx, tx, tn, students[overridden].number, schools.OverrideIn{Reason: "Bereavement in the family"}, o.Approver)
		return err
	}); err != nil {
		return res, err
	}
	// The check the server runs every hour: the plan not paid as agreed is
	// in arrears.
	if _, err := sch.CheckChasing(ctx, tn); err != nil {
		return res, err
	}

	// Yesterday's close, and the marker.
	if err := in(func(tx pgx.Tx) error {
		c, _, err := sch.CloseDay(ctx, tx, tn, s.yesterday(), o.Owner)
		if err != nil {
			return err
		}
		res.CloseDay, res.Reconciles = c.Day, c.Reconciles
		_, err = tx.Exec(ctx, `SELECT taskiem_audit_append($1, 'system', $2, $3, $4, $5)`, tn, o.Owner, AuditAction, tn.String(),
			fmt.Sprintf(`{"students": %d, "families": %d, "rail": %q}`, res.Students, res.Families, res.Rail))
		return err
	}); err != nil {
		return res, err
	}
	res.Seeded = true
	return res, nil
}

func (s *Seeder) yesterday() time.Time { return s.today().AddDate(0, 0, -1) }

// hold takes a guardian's whole daily message allowance, so the demo's
// messages are left unsent, as the platform leaves any recipient's past
// their limit.
func (s *Seeder) hold(ctx context.Context, tx pgx.Tx, phone string) error {
	for range 40 {
		ok, err := s.Messages.TakeRecipient(ctx, tx, "sms", phone)
		if err != nil {
			return err
		}
		if !ok {
			return nil
		}
	}
	return errors.New("demo: a guardian's message allowance did not run out")
}

// issueAccounts plans, starts and works one batch to its end.
func (s *Seeder) issueAccounts(ctx context.Context, tn uuid.UUID, by string) error {
	var job schools.Job
	if err := s.Schools.In(ctx, tn, func(tx pgx.Tx) error {
		plan, err := s.Schools.PlanAccounts(ctx, tx, tn)
		if err != nil {
			return err
		}
		if len(plan.Cannot) > 0 {
			return fmt.Errorf("demo: %s cannot be issued an account on %s: %s", plan.Cannot[0].AdmissionNumber, plan.Issuer.Connection, plan.Cannot[0].Reason)
		}
		job, err = s.Schools.StartBatch(ctx, tx, tn, plan.Hash, by)
		return err
	}); err != nil {
		return err
	}
	for range 200 {
		if _, err := s.Schools.WorkJob(ctx, tn, job.ID, 50); err != nil {
			return err
		}
		var j schools.Job
		if err := s.Schools.In(ctx, tn, func(tx pgx.Tx) error {
			var err error
			j, err = s.Schools.Job(ctx, tx, tn, job.ID)
			return err
		}); err != nil {
			return err
		}
		if j.Status == "done" {
			if len(j.Problems) > 0 {
				return fmt.Errorf("demo: %s was not issued an account: %s", j.Problems[0].AdmissionNumber, j.Problems[0].Reason)
			}
			return nil
		}
	}
	return errors.New("demo: the account batch did not finish")
}

// plans asks for and approves two payment plans as of three weeks ago:
// the first instalment of each fell due since; one family paid it (the
// credit recorded later), the other did not.
func (s *Seeder) plans(ctx context.Context, o Options, students []student, ids map[string]uuid.UUID, families map[int]uuid.UUID, totals map[string]int64) error {
	sch := s.Schools
	then := s.now().AddDate(0, 0, -21)
	was := sch.Now
	sch.Now = func() time.Time { return then }
	defer func() { sch.Now = was }()
	today := s.today()
	return sch.In(ctx, o.Tenant, func(tx pgx.Tx) error {
		for _, p := range []struct {
			i     int
			first int // days from today the first instalment fell due
			msg   string
		}{{planGood, -5, "Half now, the rest after the harvest"}, {planArrears, -10, "Please allow two parts"}} {
			st := students[p.i]
			total := totals[st.number]
			req, err := sch.RequestPlan(ctx, tx, o.Tenant, families[st.family], schools.PlanIn{StudentID: ids[st.number], Message: p.msg,
				Instalments: []schools.Instalment{{AmountKobo: 5_000_000, DueOn: day(today, p.first)}, {AmountKobo: total - 5_000_000, DueOn: day(today, 30)}}})
			if err != nil {
				return err
			}
			if _, err := sch.DecidePlan(ctx, tx, o.Tenant, req.ID, schools.PlanDecision{Approve: true, Note: "Agreed: pay the first part before the exams"}, o.Approver, userOf(o.Approver)); err != nil {
				return err
			}
		}
		return nil
	})
}

// userOf is the user behind an actor written as the API writes one (a
// bare id), or nil.
func userOf(by string) *uuid.UUID {
	if id, err := uuid.Parse(by); err == nil {
		return &id
	}
	return nil
}
