package schools_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/connector"
	"github.com/israel-duff/taskiem/engine/effects"
	"github.com/israel-duff/taskiem/engine/invoicing"
	"github.com/israel-duff/taskiem/engine/schools"
)

// fakeRails stands for the business's rails: iswallet (a wallet, safe to
// ask again: the same key answers the same account) and Paystack (an
// email per account, unsafe). It opens one account per key, as the rails
// do, and counts what it opened.
type fakeRails struct {
	mu     sync.Mutex
	asked  []map[string]any
	opened map[string]string // key -> account number
	fail   map[string]error  // by the child's name
}

var (
	iswalletRail = invoicing.Rail{Connector: "iswallet", Connection: "main", Name: "iswallet", Events: map[string]bool{},
		Caps: map[string]connector.CapabilityMap{
			"virtual_account": {Requires: []string{"account"}, Ignores: []string{"customer.email", "customer.phone", "customer.bvn", "customer.nin"}},
			"list_credits":    {Requires: []string{"account"}},
		}}
	paystackRail = invoicing.Rail{Connector: "paystack", Connection: "main", Name: "Paystack", Events: map[string]bool{"virtual_account.credited": true},
		Caps: map[string]connector.CapabilityMap{
			"virtual_account": {Requires: []string{"customer.email"}, Ignores: []string{"account_name", "account", "customer.bvn", "customer.nin"}, Unsafe: true},
			"list_credits":    {Unsupported: []string{"account"}},
		}}
)

func (f *fakeRails) Capable(_ context.Context, _ uuid.UUID, capability string) ([]invoicing.Rail, error) {
	var out []invoicing.Rail
	for _, r := range []invoicing.Rail{iswalletRail, paystackRail} {
		if r.Maps(capability) {
			out = append(out, r)
		}
	}
	return out, nil
}

func (f *fakeRails) ResolveChannel(ctx context.Context, tenant uuid.UUID, channel, capability string) (invoicing.Rail, error) {
	list, _ := f.Capable(ctx, tenant, capability)
	for _, r := range list {
		if r.Channel() == channel {
			return r, nil
		}
	}
	return invoicing.Rail{}, invoicing.ErrNoRail
}

func (f *fakeRails) Call(_ context.Context, _ uuid.UUID, r invoicing.Rail, capability string, in map[string]any, key effects.KeyInput) (map[string]any, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.asked = append(f.asked, in)
	name := in["customer"].(map[string]any)["name"].(string)
	if err := f.fail[name]; err != nil {
		return nil, err
	}
	sum := sha256.Sum256([]byte(key.TenantID + key.Seed + key.StepID))
	k := hex.EncodeToString(sum[:])
	if f.opened == nil {
		f.opened = map[string]string{}
	}
	number, ok := f.opened[k]
	if !ok {
		number = fmt.Sprintf("99%08d", len(f.opened)+1)
		f.opened[k] = number
	}
	out := map[string]any{"account_number": number, "bank_name": "Wema Bank", "reference": "tsk_" + k[:20]}
	if r.Connector == "paystack" {
		out["reference"] = "CUS_" + k[:12]
		out["account_name"] = "GREENFIELD/" + strings.ToUpper(name)
	}
	return out, nil
}

type accountWorld struct {
	*termWorld
	rails *fakeRails
}

func newAccountWorld(t *testing.T, channel, wallet string) *accountWorld {
	w := &accountWorld{termWorld: newTermWorld(t), rails: &fakeRails{fail: map[string]error{}}}
	w.s.Rails = w.rails
	w.in(func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `UPDATE tenants SET name = 'Greenfield Court Academy' WHERE id = $1`, w.env.Tenant)
		return err
	})
	in, err := w.s.CheckIssuing(ctx, w.env.Tenant, schools.Issuing{Channel: channel, Account: wallet})
	if err != nil {
		t.Fatal(err)
	}
	w.in(func(tx pgx.Tx) error {
		_, err := schools.SetIssuing(ctx, tx, w.env.Tenant, in, "user:ada")
		return err
	})
	return w
}

func (w *accountWorld) family(phone, email string) uuid.UUID {
	w.t.Helper()
	var f schools.Family
	w.in(func(tx pgx.Tx) error {
		var err error
		f, err = w.s.AddFamily(ctx, tx, w.env.Tenant, []schools.GuardianIn{{Name: "Guardian", Phone: phone, Email: email}}, "user:ada")
		return err
	})
	return f.ID
}

func (w *accountWorld) plan() schools.IssuePlan {
	w.t.Helper()
	var p schools.IssuePlan
	w.in(func(tx pgx.Tx) error {
		var err error
		p, err = w.s.PlanAccounts(ctx, tx, w.env.Tenant)
		return err
	})
	return p
}

func (w *accountWorld) start(hash string) (schools.Job, error) {
	var j schools.Job
	err := w.try(func(tx pgx.Tx) error {
		var err error
		j, err = w.s.StartBatch(ctx, tx, w.env.Tenant, hash, "user:ada")
		return err
	})
	return j, err
}

func (w *accountWorld) job(id uuid.UUID) schools.Job {
	w.t.Helper()
	var j schools.Job
	w.in(func(tx pgx.Tx) error {
		var err error
		j, err = w.s.Job(ctx, tx, w.env.Tenant, id)
		return err
	})
	return j
}

// The issuing connection must be one that can issue, with its wallet when
// its rail needs one.
func TestIssuingSetting(t *testing.T) {
	w := newAccountWorld(t, "iswallet/main", "wal_1")
	if _, err := w.s.CheckIssuing(ctx, w.env.Tenant, schools.Issuing{Channel: "iswallet/main"}); !errors.Is(err, schools.ErrInvalid) {
		t.Errorf("no wallet: %v", err)
	}
	if _, err := w.s.CheckIssuing(ctx, w.env.Tenant, schools.Issuing{Channel: "moniepoint/main"}); !errors.Is(err, schools.ErrInvalid) {
		t.Errorf("not connected: %v", err)
	}
	// Paystack takes no wallet: it is dropped.
	if in, err := w.s.CheckIssuing(ctx, w.env.Tenant, schools.Issuing{Channel: "paystack/main", Account: "x"}); err != nil || in.Account != "" {
		t.Errorf("paystack: %+v %v", in, err)
	}
	list, err := w.s.Issuers(ctx, w.env.Tenant)
	if err != nil || len(list) != 2 || !list[0].NeedsAccount || !list[1].OneAccountPerEmail || !list[1].Webhook || list[0].Webhook || !list[0].LookBack {
		t.Fatalf("issuers %+v %v", list, err)
	}
}

// One student: the account is the student's customer account, named for
// the school and the child (shortened to fit), on the wallet; asking again
// answers it without asking the rail.
func TestIssueOneStudent(t *testing.T) {
	w := newAccountWorld(t, "iswallet/main", "wal_1")
	ebube := w.student("Ebube Eze", "GCA/001", w.j1, &w.fam.ID)
	a, err := w.s.IssueOne(ctx, w.env.Tenant, "GCA/001", schools.IssueIn{}, "user:ada")
	if err != nil {
		t.Fatal(err)
	}
	// iswallet shows payers the name asked for (27 October 2026).
	if a.Status != schools.AccountIssued || a.AccountNumber == "" || !a.Shortened || a.Flagged || a.NamedByRail || !strings.HasPrefix(a.Reference, "tsk_") {
		t.Fatalf("issued %+v", a)
	}
	if got := w.rails.asked[0]; got["account_name"] != "GREENFIELD COURT / EBUBE EZE" || got["account"] != "wal_1" {
		t.Errorf("asked %v", got)
	}
	again, err := w.s.IssueOne(ctx, w.env.Tenant, ebube.ID.String(), schools.IssueIn{}, "user:ada")
	if err != nil || again.AccountNumber != a.AccountNumber || len(w.rails.asked) != 1 {
		t.Fatalf("again %+v %v (asked %d)", again, err, len(w.rails.asked))
	}
	var customer string
	w.in(func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT customer_id FROM customer_accounts WHERE account_number = $1`, a.AccountNumber).Scan(&customer)
	})
	if customer != "key:student:"+ebube.ID.String() {
		t.Errorf("customer %q", customer)
	}
	if n := w.count(`SELECT count(*) FROM money_lookbacks WHERE channel = 'iswallet/main' AND account = 'wal_1'`); n != 1 {
		t.Errorf("%d look-back targets", n)
	}
	if n := w.count(`SELECT count(*) FROM audit_log WHERE action = 'school.account.issue'`); n != 1 {
		t.Errorf("%d audits", n)
	}
	// A withdrawn student is not issued one.
	w.student("Ada Obi", "GCA/002", w.j1, nil)
	w.in(func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `UPDATE school_students SET status = 'withdrawn' WHERE admission_number = 'GCA/002'`)
		return err
	})
	if _, err := w.s.IssueOne(ctx, w.env.Tenant, "GCA/002", schools.IssueIn{}, "user:ada"); !errors.Is(err, schools.ErrInvalid) {
		t.Errorf("withdrawn: %v", err)
	}
}

// A batch: planned first (Paystack: an email each, one account per
// email), confirmed by its hash, worked through in steps; a step cut short
// is picked up and never opens a second account; the rail's refusals are
// listed per student.
func TestBatchIsResumableAndExactlyOnce(t *testing.T) {
	w := newAccountWorld(t, "iswallet/main", "wal_1")
	for i := 1; i <= 5; i++ {
		w.student(fmt.Sprintf("Child %c", 'A'+i-1), fmt.Sprintf("GCA/%03d", i), w.j1, nil)
	}
	w.rails.fail["Child E"] = fmt.Errorf("iswallet KYC_REQUIRED: the director's BVN is missing: %w", effects.ErrFatal)
	p := w.plan()
	if p.ToIssue != 5 || len(p.Cannot) != 0 || p.Hash == "" || p.Issuer.Rail != "iswallet" {
		t.Fatalf("plan %+v", p)
	}
	if _, err := w.start("stale"); !errors.Is(err, schools.ErrState) {
		t.Errorf("a stale plan: %v", err)
	}
	j, err := w.start(p.Hash)
	if err != nil || j.Total != 5 || j.Pending != 5 {
		t.Fatalf("job %+v %v", j, err)
	}
	// The same plan again is the same batch.
	if again, err := w.start(p.Hash); err != nil || again.ID != j.ID {
		t.Fatalf("again %+v %v", again, err)
	}
	if _, err := w.s.WorkJob(ctx, w.env.Tenant, j.ID, 2); err != nil {
		t.Fatal(err)
	}
	if j = w.job(j.ID); j.Issued != 2 || j.Pending != 3 || j.Status != "running" {
		t.Fatalf("after a step %+v", j)
	}
	// The server stops while asking for the third: its claim is left.
	w.in(func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO school_student_accounts (tenant_id, student_id, channel, account, status, attempts, claimed_at, job_id, created_by)
			SELECT tenant_id, id, 'iswallet/main', 'wal_1', 'issuing', 1, now() - interval '10 minutes', $1, 'system:schools' FROM school_students WHERE admission_number = 'GCA/003'`, j.ID)
		return err
	})
	// The rail had opened it before the answer was lost.
	if _, err := w.rails.Call(ctx, w.env.Tenant, iswalletRail, "virtual_account", map[string]any{"customer": map[string]any{"name": "Child C"}},
		schools.AccountKey(w.env.Tenant, w.studentID("GCA/003"), "iswallet/main")); err != nil {
		t.Fatal(err)
	}
	for range 3 {
		if _, err := w.s.WorkJob(ctx, w.env.Tenant, j.ID, 10); err != nil {
			t.Fatal(err)
		}
	}
	j = w.job(j.ID)
	if j.Issued != 4 || j.Failed != 1 || j.Pending != 0 || j.Status != "done" || len(j.Problems) != 1 ||
		j.Problems[0].AdmissionNumber != "GCA/005" || j.Problems[0].Reason != "iswallet KYC_REQUIRED: the director's BVN is missing" {
		t.Fatalf("done %+v", j)
	}
	// Each student one account; the rail opened one per student.
	if n := w.count(`SELECT count(*) FROM customer_accounts`); n != 4 || len(w.rails.opened) != 4 {
		t.Fatalf("%d accounts, %d opened", n, len(w.rails.opened))
	}
	// The next plan offers the one that failed again, once the rail is fixed.
	delete(w.rails.fail, "Child E")
	p = w.plan()
	if p.ToIssue != 1 || p.Issued != 4 {
		t.Fatalf("next plan %+v", p)
	}
	j2, err := w.start(p.Hash)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.s.WorkJob(ctx, w.env.Tenant, j2.ID, 10); err != nil {
		t.Fatal(err)
	}
	if _, err := w.s.WorkJob(ctx, w.env.Tenant, j2.ID, 10); err != nil {
		t.Fatal(err)
	}
	if j2 = w.job(j2.ID); j2.Issued != 1 || j2.Status != "done" {
		t.Fatalf("second batch %+v", j2)
	}
}

func (w *accountWorld) studentID(number string) uuid.UUID {
	w.t.Helper()
	var id uuid.UUID
	w.in(func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT id FROM school_students WHERE admission_number = $1`, number).Scan(&id)
	})
	return id
}

// Paystack: a student without a guardian email cannot be issued, nor a
// second student whose guardian's email a sibling's account has; a lost
// answer on Paystack (an unsafe write) is never asked again on its own.
func TestPaystackPlanAndLostAnswers(t *testing.T) {
	w := newAccountWorld(t, "paystack/main", "")
	shared := w.family("08031230001", "eze@example.com")
	w.student("Ebube Eze", "GCA/001", w.j1, &shared)
	w.student("Chidi Eze", "GCA/002", w.j1, &shared)
	noEmail := w.family("08031230002", "")
	w.student("Ada Obi", "GCA/003", w.j1, &noEmail)
	w.student("Tunde Bello", "GCA/004", w.j1, nil)
	p := w.plan()
	if p.ToIssue != 1 || len(p.Cannot) != 3 {
		t.Fatalf("plan %+v", p)
	}
	reasons := map[string]string{}
	for _, c := range p.Cannot {
		reasons[c.AdmissionNumber] = c.Reason
	}
	if !strings.Contains(reasons["GCA/002"], "one account per email") || !strings.Contains(reasons["GCA/003"], "add one to a guardian") ||
		!strings.Contains(reasons["GCA/004"], "no family yet") {
		t.Fatalf("reasons %v", reasons)
	}
	j, err := w.start(p.Hash)
	if err != nil {
		t.Fatal(err)
	}
	// Cut short while asking: on Paystack it is left for a person.
	w.in(func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO school_student_accounts (tenant_id, student_id, channel, status, attempts, claimed_at, job_id, created_by)
			SELECT tenant_id, id, 'paystack/main', 'issuing', 1, now() - interval '10 minutes', $1, 'system:schools' FROM school_students WHERE admission_number = 'GCA/001'`, j.ID)
		return err
	})
	for range 2 {
		if _, err := w.s.WorkJob(ctx, w.env.Tenant, j.ID, 10); err != nil {
			t.Fatal(err)
		}
	}
	if j = w.job(j.ID); j.Unknown != 1 || j.Status != "done" || len(w.rails.asked) != 0 {
		t.Fatalf("lost answer %+v (asked %d)", j, len(w.rails.asked))
	}
	if _, err := w.s.IssueOne(ctx, w.env.Tenant, "GCA/001", schools.IssueIn{}, "user:ada"); !errors.Is(err, schools.ErrState) {
		t.Errorf("asked again without retry: %v", err)
	}
	// After checking with Paystack, retry.
	a, err := w.s.IssueOne(ctx, w.env.Tenant, "GCA/001", schools.IssueIn{Retry: true}, "user:ada")
	if err != nil || a.Status != schools.AccountIssued || a.AccountName != "GREENFIELD/EBUBE EZE" || !strings.HasPrefix(a.Reference, "CUS_") {
		t.Fatalf("retried %+v %v", a, err)
	}
	// Paystack names the account: no name was sent.
	if _, sent := w.rails.asked[0]["account_name"]; sent {
		t.Errorf("a name was sent to Paystack: %v", w.rails.asked[0])
	}
	// One by one too, a sibling's email is refused before Paystack is asked.
	if _, err := w.s.IssueOne(ctx, w.env.Tenant, "GCA/002", schools.IssueIn{}, "user:ada"); !errors.Is(err, schools.ErrInvalid) || len(w.rails.asked) != 1 {
		t.Errorf("sibling: %v", err)
	}
	// The rail's refusal of one student is recorded with its reason.
	okon := w.family("08031230003", "okon@example.com")
	efe := w.student("Efe Okon", "GCA/005", w.j1, &okon)
	w.rails.fail["Efe Okon"] = fmt.Errorf("paystack: Customer email is invalid: %w", effects.ErrFatal)
	if _, err := w.s.IssueOne(ctx, w.env.Tenant, "GCA/005", schools.IssueIn{}, "user:ada"); !schools.ErrIssue(err) || schools.Plain(err) != "paystack: Customer email is invalid" {
		t.Fatalf("refused: %v", err)
	}
	var list []schools.StudentAccount
	w.in(func(tx pgx.Tx) error {
		var err error
		list, err = w.s.StudentAccounts(ctx, tx, w.env.Tenant, efe.ID)
		return err
	})
	if len(list) != 1 || list[0].Status != schools.AccountFailed || list[0].Error != "paystack: Customer email is invalid" {
		t.Fatalf("recorded %+v", list)
	}
}
