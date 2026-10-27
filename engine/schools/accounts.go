package schools

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/effects"
	"github.com/israel-duff/taskiem/engine/invoicing"
	"github.com/israel-duff/taskiem/engine/whatsapp"
)

// Students' account numbers (docs/spec/schools.md 6, B4;
// docs/schools.md#account-numbers). The school picks one prod connection
// whose rail maps virtual_account (its issuing connection, and on rails
// that hold several, the wallet the numbers pay into). Each active student
// is issued a permanent account number on it once: issuing again for the
// same student and connection answers the account already issued, because
// the rail's key is derived from the business, the student and the
// connection (AccountKey), and the issue is claimed on the student's row
// (school_student_accounts) before the rail is asked. The account is the
// student's customer account (invoicing.RegisterAccount, for the customer
// key:student:<id>), so B1's permanent-account rule matches its credits to
// the student's own term invoices, oldest first.
//
// Issuing in a batch is a job of up to MaxBatch students, planned first
// (who can be issued and who cannot, and why) and confirmed by the plan's
// hash. RunJobs works through running jobs; a job picked up after an
// interruption skips the students already issued and never asks twice for
// a student whose answer was lost on a rail where asking again could open
// a second account (an unsafe write: Paystack, Flutterwave): that student
// is left for a person to check (unknown).

// States of a student's account on a connection.
const (
	AccountIssuing = "issuing"
	AccountIssued  = "issued"
	AccountFailed  = "failed"
	AccountUnknown = "unknown"
)

// MaxBatch is the most students one batch issues.
const MaxBatch = 500

// MaxAttempts is how often a batch tries a student whose failure may pass
// on its own (the rail did not answer, on a rail where asking again is
// safe).
const MaxAttempts = 3

// claimFor is how long a claim on a student's row holds: an issue still
// "issuing" after it was cut short (the server stopped).
const claimFor = 2 * time.Minute

// Facts are what B0 recorded of a rail for students' accounts
// (docs/spec/schools-rail-facts.md), in plain words.
type Facts struct {
	// Requirement says what the rail needs, for the settings page.
	Requirement string `json:"requirement"`
	// Fee is what the rail charges, as documented: nothing per account is
	// documented by any rail mapped.
	Fee string `json:"fee,omitempty"`
	// NamesItself: what payers see is the rail's name for the account,
	// whatever Taskiem asks (iswallet's partner, Paystack).
	NamesItself bool `json:"names_itself"`
	// NameLimit is the rail's limit on the account's name; 0 when not
	// documented (DefaultNameLimit applies).
	NameLimit int `json:"name_limit,omitempty"`
	// OneAccountPerEmail: the rail opens one account per customer email
	// (Paystack), so siblings sharing a guardian's email cannot each have
	// one.
	OneAccountPerEmail bool `json:"one_account_per_email,omitempty"`
	// NeedsBVN: the rail needs a BVN or NIN for each account (Flutterwave):
	// typed in for each student, never kept, so not in a batch.
	NeedsBVN bool `json:"needs_bvn,omitempty"`
}

var railFacts = map[string]Facts{
	"iswallet": {Requirement: "iswallet issues numbers only once it holds the wallet's business director's BVN. Choose the wallet the numbers pay into. Its partner bank names each account itself.",
		Fee: "No fee per account is documented; iswallet's partner takes 1.4% of each payment (at most ₦2,000).", NamesItself: true},
	"paystack": {Requirement: "Paystack needs an email for each account and opens one account per email, so each student needs a guardian email no brother or sister shares. Paystack names each account \"your business name / the child's name\" itself, and only registered Nigerian businesses that have gone live can issue them (at most 1,000 unless Paystack raises it).",
		Fee: "No fee per account is documented; Paystack takes 1% of each payment (at most ₦300).", NamesItself: true, OneAccountPerEmail: true},
	"flutterwave": {Requirement: "Flutterwave needs a parent's BVN or NIN for each account, so its accounts are issued one at a time on each student's page, with the number typed in (Taskiem does not keep it). It also needs a guardian email.",
		Fee: "No fee per account is documented; Flutterwave takes 2% of each payment, plus VAT.", NeedsBVN: true},
	"moniepoint": {Requirement: "Moniepoint needs an email for each account (the guardian's) and shows the name Taskiem gives: the school's and the child's."},
}

// FactsOf are what is known of a rail; a rail B0 did not cover says what
// its mapping needs.
func FactsOf(r invoicing.Rail) Facts {
	if f, ok := railFacts[r.Connector]; ok {
		return f
	}
	f := Facts{Requirement: r.Name + " can issue account numbers."}
	if r.Needs("virtual_account", "customer.email") {
		f.Requirement = r.Name + " needs an email for each account (the guardian's)."
	}
	f.NamesItself = !r.Takes("virtual_account", "account_name")
	return f
}

// AccountKey is the key a student's account on a connection is issued
// under: student:<id>:acct:<connection>. The rail's reference (or
// idempotency key) is derived from it in the rail's own characters, so
// asking again answers the same account.
func AccountKey(tenant, student uuid.UUID, channel string) effects.KeyInput {
	return effects.KeyInput{TenantID: tenant.String(), Seed: StudentKey(student), StepID: "acct:" + channel}
}

// --- the setting ---

// Issuing is the school's issuing connection and the family-credit
// setting.
type Issuing struct {
	// Channel is the connection, as connector/name: "iswallet/main".
	Channel string `json:"connection"`
	// Account is the wallet the numbers pay into, on rails that hold
	// several (iswallet).
	Account string `json:"account,omitempty"`
	// SiblingsShareCredit: what a student's account takes beyond their own
	// invoices counts towards their brothers' and sisters' open invoices,
	// oldest first (invoicing.SharedWith). Off by default.
	SiblingsShareCredit bool `json:"siblings_share_credit"`
}

// GetIssuing reads the setting.
func GetIssuing(ctx context.Context, tx pgx.Tx, tenant uuid.UUID) (Issuing, error) {
	var out Issuing
	var ch, acct *string
	err := tx.QueryRow(ctx, `SELECT issuing_channel, issuing_account, siblings_share_credit FROM school_settings WHERE tenant_id = $1`, tenant).Scan(&ch, &acct, &out.SiblingsShareCredit)
	if errors.Is(err, pgx.ErrNoRows) {
		return out, nil
	}
	if ch != nil {
		out.Channel = *ch
	}
	if acct != nil {
		out.Account = *acct
	}
	return out, err
}

// Issuer is a connection that can issue students' accounts.
type Issuer struct {
	Connection string `json:"connection"`
	Rail       string `json:"rail"`
	Facts
	// NeedsAccount: the wallet the numbers pay into must be chosen.
	NeedsAccount bool `json:"needs_account"`
	// LookBack: its credits are read back every 15 minutes (list_credits).
	LookBack bool `json:"look_back"`
	// Webhook: its credits also arrive as events, when its webhook is set up.
	Webhook bool `json:"webhook"`
}

func issuerOf(r invoicing.Rail) Issuer {
	return Issuer{Connection: r.Channel(), Rail: r.Name, Facts: FactsOf(r), NeedsAccount: r.Needs("virtual_account", "account"),
		LookBack: r.Maps("list_credits"), Webhook: r.Events["virtual_account.credited"]}
}

// Issuers lists the business's prod connections that can issue students'
// accounts. None when the server has no rails.
func (s *Schools) Issuers(ctx context.Context, tenant uuid.UUID) ([]Issuer, error) {
	out := []Issuer{}
	if s.Rails == nil {
		return out, nil
	}
	list, err := s.Rails.Capable(ctx, tenant, "virtual_account")
	for _, r := range list {
		out = append(out, issuerOf(r))
	}
	return out, err
}

// CheckIssuing checks a setting before it is saved: the connection is an
// active prod connection that can issue accounts, with the wallet when
// its rail needs one.
func (s *Schools) CheckIssuing(ctx context.Context, tenant uuid.UUID, in Issuing) (Issuing, error) {
	in.Channel, in.Account = strings.TrimSpace(in.Channel), strings.TrimSpace(in.Account)
	if in.Channel == "" {
		in.Account = ""
		return in, nil
	}
	if s.Rails == nil {
		return in, refused("this server cannot reach payment services")
	}
	r, err := s.Rails.ResolveChannel(ctx, tenant, in.Channel, "virtual_account")
	if errors.Is(err, invoicing.ErrNoRail) {
		return in, invalid("%s is not an active connection in prod that can issue account numbers: connect it first, or choose another", in.Channel)
	}
	if err != nil {
		return in, err
	}
	switch {
	case r.Needs("virtual_account", "account") && in.Account == "":
		return in, invalid("%s needs the wallet the numbers pay into", r.Name)
	case !r.Takes("virtual_account", "account"):
		in.Account = ""
	case len(in.Account) > 200:
		return in, invalid("the wallet's id has up to 200 characters")
	}
	return in, nil
}

// SetIssuing saves a checked setting (CheckIssuing); it reports what it
// was before.
func SetIssuing(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, in Issuing, by string) (Issuing, error) {
	was, err := GetIssuing(ctx, tx, tenant)
	if err != nil {
		return was, err
	}
	_, err = tx.Exec(ctx, `UPDATE school_settings SET issuing_channel = NULLIF($2, ''), issuing_account = NULLIF($3, ''), siblings_share_credit = $4,
		updated_by = $5, updated_at = now() WHERE tenant_id = $1`, tenant, in.Channel, in.Account, in.SiblingsShareCredit, by)
	return was, err
}

// --- a student's accounts ---

// StudentAccount is a student's account number on a connection, or how
// issuing it went.
type StudentAccount struct {
	StudentID     uuid.UUID `json:"student_id"`
	Connection    string    `json:"connection"`
	Status        string    `json:"status"`
	AccountNumber string    `json:"account_number,omitempty"`
	BankName      string    `json:"bank_name,omitempty"`
	// AccountName is the name the rail answered, else the one asked for:
	// what parents should expect (personal data: it has the child's name).
	AccountName string `json:"account_name,omitempty"`
	// NamedByRail: the rail names the account itself (the name asked for
	// may not be what parents see).
	NamedByRail bool   `json:"named_by_rail"`
	Reference   string `json:"reference,omitempty"`
	Shortened   bool   `json:"shortened,omitempty"`
	Flagged     bool   `json:"flagged,omitempty"`
	// Error is why it failed, or why it is unknown, in the rail's words.
	Error     string    `json:"error,omitempty"`
	Attempts  int       `json:"attempts"`
	UpdatedAt time.Time `json:"updated_at"`
}

type accountSealed struct {
	Asked string `json:"asked,omitempty"` // the name Taskiem asked for
	Named string `json:"named,omitempty"` // the name the rail answered
}

const accountCols = `a.student_id, a.channel, a.status, COALESCE(c.account_number, ''), COALESCE(c.bank_name, ''), COALESCE(c.reference, ''),
	a.details, a.shortened, a.flagged, COALESCE(a.error, ''), a.attempts, a.updated_at`

func (s *Schools) scanAccounts(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, rows pgx.Rows) ([]StudentAccount, error) {
	type row struct {
		a   StudentAccount
		env map[string]any
	}
	got, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (row, error) {
		var x row
		a := &x.a
		return x, r.Scan(&a.StudentID, &a.Connection, &a.Status, &a.AccountNumber, &a.BankName, &a.Reference, &x.env, &a.Shortened, &a.Flagged, &a.Error, &a.Attempts, &a.UpdatedAt)
	})
	if err != nil {
		return nil, err
	}
	out := make([]StudentAccount, 0, len(got))
	for _, x := range got {
		var d accountSealed
		erased, err := s.open(ctx, tx, tenant, x.env, &d)
		if err != nil {
			return nil, err
		}
		x.a.AccountName = d.Named
		if x.a.AccountName == "" {
			x.a.AccountName = d.Asked
		}
		if erased {
			x.a.AccountName = Erased
		}
		connector, _, _ := strings.Cut(x.a.Connection, "/")
		x.a.NamedByRail = railFacts[connector].NamesItself
		out = append(out, x.a)
	}
	return out, nil
}

// StudentAccounts lists a student's accounts (and attempts), newest first.
func (s *Schools) StudentAccounts(ctx context.Context, tx pgx.Tx, tenant, student uuid.UUID) ([]StudentAccount, error) {
	rows, err := tx.Query(ctx, `SELECT `+accountCols+` FROM school_student_accounts a LEFT JOIN customer_accounts c ON c.id = a.account_id
		WHERE a.tenant_id = $1 AND a.student_id = $2 ORDER BY a.status = 'issued' DESC, a.updated_at DESC`, tenant, student)
	if err != nil {
		return nil, err
	}
	return s.scanAccounts(ctx, tx, tenant, rows)
}

// --- who can be issued ---

// target is a student as issuing sees them.
type target struct {
	student  Student
	email    string // the first guardian's email that has one
	phone    string
	state    string // their row on the connection: "" when none
	attempts int
	retry    bool
	claimed  *time.Time
}

// targets are the school's active students (or those of ids), with their
// guardians' contacts and their rows on channel.
func (s *Schools) targets(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, channel string, ids []uuid.UUID) ([]target, error) {
	where, args := `s.status = 'active'`, []any{}
	if ids != nil {
		where, args = `s.id = ANY ($2)`, []any{ids}
	}
	list, err := s.listStudents(ctx, tx, tenant, where, args...)
	if err != nil {
		return nil, err
	}
	fams, err := s.families(ctx, tx, tenant, nil)
	if err != nil {
		return nil, err
	}
	contact := map[uuid.UUID][2]string{}
	for _, f := range fams {
		var c [2]string
		for _, g := range f.Guardians {
			if c[0] == "" && g.Email != "" {
				c[0] = g.Email
			}
			if c[1] == "" && g.Phone != "" {
				c[1] = g.Phone
			}
		}
		contact[f.ID] = c
	}
	rows, err := tx.Query(ctx, `SELECT student_id, status, attempts, retry, claimed_at FROM school_student_accounts WHERE tenant_id = $1 AND channel = $2`, tenant, channel)
	if err != nil {
		return nil, err
	}
	type st struct {
		state    string
		attempts int
		retry    bool
		claimed  *time.Time
	}
	states := map[uuid.UUID]st{}
	for rows.Next() {
		var id uuid.UUID
		var x st
		if err := rows.Scan(&id, &x.state, &x.attempts, &x.retry, &x.claimed); err != nil {
			rows.Close()
			return nil, err
		}
		states[id] = x
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := make([]target, 0, len(list))
	for _, x := range list {
		t := target{student: x}
		if x.FamilyID != nil {
			c := contact[*x.FamilyID]
			t.email, t.phone = c[0], c[1]
		}
		if y, ok := states[x.ID]; ok {
			t.state, t.attempts, t.retry, t.claimed = y.state, y.attempts, y.retry, y.claimed
		}
		out = append(out, t)
	}
	return out, nil
}

// cannot says why a rail cannot issue a student's account in a batch, or
// "".
func cannot(r invoicing.Rail, t target, typed bool) string {
	f := FactsOf(r)
	switch {
	case t.student.Name == Erased || t.student.Name == "":
		return "the student's details were erased"
	case f.NeedsBVN && !typed:
		return r.Name + " needs a parent's BVN or NIN for each account: issue it on the student's page"
	case (r.Needs("virtual_account", "customer.email") || f.OneAccountPerEmail) && t.email == "":
		if t.student.FamilyID == nil {
			return r.Name + " needs an email for each account, and the student has no family yet: add their guardians on Families"
		}
		return r.Name + " needs an email for each account: add one to a guardian of the student's family"
	}
	return ""
}

// IssuePlan is what issuing accounts to every student without one would
// do: nothing happens until it is confirmed by its hash (StartBatch).
type IssuePlan struct {
	Issuer Issuer `json:"issuer"`
	// Active is how many active students the school has; Issued how many
	// of them have their account on the connection.
	Active int `json:"active"`
	Issued int `json:"already_issued"`
	// ToIssue is how many this batch issues (at most MaxBatch); Later how
	// many more can be issued by the next batch.
	ToIssue int `json:"to_issue"`
	Later   int `json:"later"`
	// Waiting are students whose account is being issued now, or whose
	// answer was lost on a rail where asking again could open a second
	// (unknown: check with the rail, then issue it on the student's page).
	Waiting []Waiting `json:"waiting"`
	// Cannot are the students who cannot be issued, and why.
	Cannot []Cannot `json:"cannot"`
	// Running is the batch still running, when one is: no other starts
	// until it ends.
	Running *uuid.UUID `json:"running,omitempty"`
	Hash    string     `json:"hash"`
	todo    []target
	account string
}

// Cannot is a student who cannot be issued an account, and why.
type Cannot struct {
	StudentID       uuid.UUID `json:"student_id"`
	AdmissionNumber string    `json:"admission_number"`
	Reason          string    `json:"reason"`
}

// Waiting is a student whose account is being issued or must be checked.
type Waiting struct {
	StudentID       uuid.UUID `json:"student_id"`
	AdmissionNumber string    `json:"admission_number"`
	Status          string    `json:"status"`
	Reason          string    `json:"reason,omitempty"`
}

// issuer is the school's issuing connection, resolved.
func (s *Schools) issuer(ctx context.Context, tx pgx.Tx, tenant uuid.UUID) (invoicing.Rail, Issuing, error) {
	st, err := GetIssuing(ctx, tx, tenant)
	if err != nil {
		return invoicing.Rail{}, st, err
	}
	if st.Channel == "" {
		return invoicing.Rail{}, st, refused("choose the connection that issues students' account numbers first, in Schools > Settings (only an owner can)")
	}
	if s.Rails == nil {
		return invoicing.Rail{}, st, refused("this server cannot reach payment services")
	}
	r, err := s.Rails.ResolveChannel(ctx, tenant, st.Channel, "virtual_account")
	if errors.Is(err, invoicing.ErrNoRail) {
		return r, st, refused("the issuing connection %s is no longer active in prod: reconnect it, or choose another in Schools > Settings", st.Channel)
	}
	return r, st, err
}

// PlanAccounts plans a batch: every active student without an account on
// the issuing connection, by admission number, at most MaxBatch.
func (s *Schools) PlanAccounts(ctx context.Context, tx pgx.Tx, tenant uuid.UUID) (IssuePlan, error) {
	if err := need(ctx, tx, tenant); err != nil {
		return IssuePlan{}, err
	}
	r, st, err := s.issuer(ctx, tx, tenant)
	if err != nil {
		return IssuePlan{}, err
	}
	return s.planAccounts(ctx, tx, tenant, r, st)
}

func (s *Schools) planAccounts(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, r invoicing.Rail, st Issuing) (IssuePlan, error) {
	p := IssuePlan{Issuer: issuerOf(r), Waiting: []Waiting{}, Cannot: []Cannot{}, account: st.Account}
	var running uuid.UUID
	if err := tx.QueryRow(ctx, `SELECT id FROM school_account_jobs WHERE tenant_id = $1 AND status = 'running' ORDER BY created_at LIMIT 1`, tenant).Scan(&running); err == nil {
		p.Running = &running
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return p, err
	}
	ts, err := s.targets(ctx, tx, tenant, r.Channel(), nil)
	if err != nil {
		return p, err
	}
	p.Active = len(ts)
	f := FactsOf(r)
	// Paystack opens one account per email: an email already a student's
	// on the connection, or two students' in this plan, is the first's.
	emailTaken := map[string]bool{}
	if f.OneAccountPerEmail {
		for _, t := range ts {
			if t.email != "" && (t.state == AccountIssued || t.state == AccountIssuing || t.state == AccountUnknown) {
				emailTaken[strings.ToLower(t.email)] = true
			}
		}
	}
	now := s.now()
	for _, t := range ts {
		switch {
		case t.state == AccountIssued:
			p.Issued++
			continue
		case t.state == AccountUnknown:
			p.Waiting = append(p.Waiting, Waiting{StudentID: t.student.ID, AdmissionNumber: t.student.AdmissionNumber, Status: t.state,
				Reason: "the answer from " + r.Name + " was lost: check whether " + r.Name + " opened an account, then issue it on the student's page"})
			continue
		case t.state == AccountIssuing && t.claimed != nil && now.Sub(*t.claimed) < claimFor:
			p.Waiting = append(p.Waiting, Waiting{StudentID: t.student.ID, AdmissionNumber: t.student.AdmissionNumber, Status: t.state})
			continue
		}
		if why := cannot(r, t, false); why != "" {
			p.Cannot = append(p.Cannot, Cannot{StudentID: t.student.ID, AdmissionNumber: t.student.AdmissionNumber, Reason: why})
			continue
		}
		if f.OneAccountPerEmail {
			e := strings.ToLower(t.email)
			if emailTaken[e] {
				p.Cannot = append(p.Cannot, Cannot{StudentID: t.student.ID, AdmissionNumber: t.student.AdmissionNumber,
					Reason: r.Name + " opens one account per email, and this student's guardian email is a brother's or sister's already: give a guardian another email"})
				continue
			}
			emailTaken[e] = true
		}
		if len(p.todo) < MaxBatch {
			p.todo = append(p.todo, t)
		} else {
			p.Later++
		}
	}
	p.ToIssue = len(p.todo)
	h := sha256.New()
	fmt.Fprintf(h, "%s %s\n", r.Channel(), st.Account)
	for _, t := range p.todo {
		fmt.Fprintf(h, "%s %d\n", t.student.ID, t.attempts)
	}
	p.Hash = hex.EncodeToString(h.Sum(nil))[:32]
	return p, nil
}

// emailTaken reports whether another student with an account (or one
// being issued) on channel has t's guardian email.
func (s *Schools) emailTaken(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, channel string, t target) (bool, error) {
	if t.email == "" {
		return false, nil
	}
	rows, err := tx.Query(ctx, `SELECT student_id FROM school_student_accounts WHERE tenant_id = $1 AND channel = $2 AND student_id <> $3
		AND status IN ('issued', 'issuing', 'unknown')`, tenant, channel, t.student.ID)
	if err != nil {
		return false, err
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
	if err != nil || len(ids) == 0 {
		return false, err
	}
	others, err := s.targets(ctx, tx, tenant, channel, ids)
	if err != nil {
		return false, err
	}
	for _, o := range others {
		if strings.EqualFold(o.email, t.email) {
			return true, nil
		}
	}
	return false, nil
}

// --- batches ---

// Job is a batch of students' accounts and how far it has got.
type Job struct {
	ID         uuid.UUID  `json:"id"`
	Connection string     `json:"connection"`
	Status     string     `json:"status"` // running or done
	Total      int        `json:"total"`
	Issued     int        `json:"issued"`
	Failed     int        `json:"failed"`
	Unknown    int        `json:"unknown"`
	Pending    int        `json:"pending"`
	CreatedBy  string     `json:"created_by"`
	CreatedAt  time.Time  `json:"created_at"`
	FinishedAt *time.Time `json:"finished_at,omitempty"`
	// Problems are the students not issued (failed or unknown), with the
	// rail's reason.
	Problems []AccountProblem `json:"problems"`
}

// AccountProblem is a student a batch did not issue, and why.
type AccountProblem struct {
	StudentID       uuid.UUID `json:"student_id"`
	AdmissionNumber string    `json:"admission_number"`
	Status          string    `json:"status"`
	Reason          string    `json:"reason"`
}

// StartBatch confirms a plan by its hash and starts its batch: refused
// when the plan changed since it was shown, or while another batch runs.
// The same plan confirmed twice is one batch.
func (s *Schools) StartBatch(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, hash, by string) (Job, error) {
	if err := lock(ctx, tx, tenant); err != nil {
		return Job{}, err
	}
	r, st, err := s.issuer(ctx, tx, tenant)
	if err != nil {
		return Job{}, err
	}
	var existing uuid.UUID
	if err := tx.QueryRow(ctx, `SELECT id FROM school_account_jobs WHERE tenant_id = $1 AND plan_hash = $2`, tenant, hash).Scan(&existing); err == nil {
		return s.Job(ctx, tx, tenant, existing)
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return Job{}, err
	}
	p, err := s.planAccounts(ctx, tx, tenant, r, st)
	if err != nil {
		return Job{}, err
	}
	switch {
	case p.Running != nil:
		return Job{}, refused("a batch is still issuing accounts: wait for it to finish")
	case hash != p.Hash:
		return Job{}, refused("the plan changed since it was shown (students, guardians or accounts): check it again")
	case p.ToIssue == 0:
		return Job{}, refused("there is nobody to issue an account to")
	}
	id := newID()
	if _, err := tx.Exec(ctx, `INSERT INTO school_account_jobs (id, tenant_id, channel, account, total, plan_hash, created_by) VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		id, tenant, r.Channel(), st.Account, p.ToIssue, p.Hash, by); err != nil {
		return Job{}, err
	}
	for i, t := range p.todo {
		if _, err := tx.Exec(ctx, `INSERT INTO school_account_job_students (job_id, tenant_id, student_id, position) VALUES ($1, $2, $3, $4)`, id, tenant, t.student.ID, i); err != nil {
			return Job{}, err
		}
	}
	return s.Job(ctx, tx, tenant, id)
}

// Job reads a batch and its progress.
func (s *Schools) Job(ctx context.Context, tx pgx.Tx, tenant, id uuid.UUID) (Job, error) {
	var j Job
	err := tx.QueryRow(ctx, `SELECT id, channel, status, total, created_by, created_at, finished_at FROM school_account_jobs WHERE tenant_id = $1 AND id = $2`, tenant, id).
		Scan(&j.ID, &j.Connection, &j.Status, &j.Total, &j.CreatedBy, &j.CreatedAt, &j.FinishedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return j, ErrNotFound
	}
	if err != nil {
		return j, err
	}
	j.Problems = []AccountProblem{}
	rows, err := tx.Query(ctx, `SELECT js.student_id, st.admission_number, COALESCE(a.status, ''), COALESCE(a.error, ''), COALESCE(a.retry, false), COALESCE(a.attempts, 0), a.job_id IS NOT DISTINCT FROM js.job_id
		FROM school_account_job_students js JOIN school_students st ON st.id = js.student_id
		LEFT JOIN school_student_accounts a ON a.tenant_id = js.tenant_id AND a.student_id = js.student_id AND a.channel = $3
		WHERE js.job_id = $2 AND js.tenant_id = $1 ORDER BY js.position`, tenant, id, j.Connection)
	if err != nil {
		return j, err
	}
	defer rows.Close()
	for rows.Next() {
		var p AccountProblem
		var retry bool
		var attempts int
		var ours bool
		if err := rows.Scan(&p.StudentID, &p.AdmissionNumber, &p.Status, &p.Reason, &retry, &attempts, &ours); err != nil {
			return j, err
		}
		switch {
		case p.Status == AccountIssued:
			j.Issued++
		case p.Status == AccountUnknown:
			j.Unknown++
			j.Problems = append(j.Problems, p)
		case p.Status == AccountFailed && (ours && (!retry || attempts >= MaxAttempts) || j.Status == "done"):
			j.Failed++
			j.Problems = append(j.Problems, p)
		default:
			j.Pending++
		}
	}
	return j, rows.Err()
}

// Jobs lists the school's latest batches, newest first.
func (s *Schools) Jobs(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, limit int) ([]Job, error) {
	rows, err := tx.Query(ctx, `SELECT id FROM school_account_jobs WHERE tenant_id = $1 ORDER BY created_at DESC LIMIT $2`, tenant, limit)
	if err != nil {
		return nil, err
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
	if err != nil {
		return nil, err
	}
	out := make([]Job, 0, len(ids))
	for _, id := range ids {
		j, err := s.Job(ctx, tx, tenant, id)
		if err != nil {
			return nil, err
		}
		out = append(out, j)
	}
	return out, nil
}

// RunJobs works on every running batch, each a step at a time (at most
// per students); call it every few seconds. Several servers may run it at
// once: each student is claimed on their row before the rail is asked.
func (s *Schools) RunJobs(ctx context.Context, per int) error {
	rows, err := s.Pool.Query(ctx, `SELECT tenant_id, job_id FROM taskiem_school_account_jobs()`)
	if err != nil {
		return err
	}
	type job struct{ tenant, id uuid.UUID }
	jobs, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (job, error) {
		var j job
		return j, r.Scan(&j.tenant, &j.id)
	})
	if err != nil {
		return err
	}
	for _, j := range jobs {
		if _, err := s.WorkJob(ctx, j.tenant, j.id, per); err != nil && ctx.Err() == nil {
			s.log().Warn("schools: issuing accounts", "tenant", j.tenant, "job", j.id, "err", err)
		}
	}
	return nil
}

// WorkJob issues up to per of a batch's students still to issue, in
// order, and marks it done when none is left. It reports how many it
// asked the rail for.
func (s *Schools) WorkJob(ctx context.Context, tenant, id uuid.UUID, per int) (int, error) {
	var channel, account, status string
	var todo []uuid.UUID
	var r invoicing.Rail
	err := s.In(ctx, tenant, func(tx pgx.Tx) error {
		todo = todo[:0]
		if err := tx.QueryRow(ctx, `SELECT channel, account, status FROM school_account_jobs WHERE tenant_id = $1 AND id = $2`, tenant, id).Scan(&channel, &account, &status); err != nil {
			return err
		}
		if status != "running" {
			return nil
		}
		// Still to do: no row, a failure that may pass (tried less than
		// MaxAttempts, a minute ago or more), or a claim that was cut short.
		rows, err := tx.Query(ctx, `SELECT js.student_id FROM school_account_job_students js
			LEFT JOIN school_student_accounts a ON a.tenant_id = js.tenant_id AND a.student_id = js.student_id AND a.channel = $3
			WHERE js.tenant_id = $1 AND js.job_id = $2
			  AND (a.status IS NULL
			    OR a.status = 'failed' AND a.job_id IS DISTINCT FROM $2
			    OR a.status = 'failed' AND a.retry AND a.attempts < $4 AND a.updated_at < now() - interval '1 minute'
			    OR a.status = 'issuing' AND (a.claimed_at IS NULL OR a.claimed_at < now() - $5::interval))
			ORDER BY js.position`, tenant, id, channel, MaxAttempts, fmt.Sprintf("%d seconds", int(claimFor.Seconds())))
		if err != nil {
			return err
		}
		todo, err = pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
		if err != nil {
			return err
		}
		if len(todo) > 0 {
			return nil
		}
		// Nothing left to ask: done once no student waits on a retry.
		var waiting bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM school_account_job_students js JOIN school_student_accounts a
			ON a.tenant_id = js.tenant_id AND a.student_id = js.student_id AND a.channel = $3
			WHERE js.job_id = $2 AND js.tenant_id = $1 AND (a.status = 'issuing' OR a.status = 'failed' AND a.retry AND a.attempts < $4))`, tenant, id, channel, MaxAttempts).Scan(&waiting); err != nil {
			return err
		}
		if !waiting {
			_, err = tx.Exec(ctx, `UPDATE school_account_jobs SET status = 'done', finished_at = now(), updated_at = now() WHERE tenant_id = $1 AND id = $2`, tenant, id)
		}
		return err
	})
	if err != nil || len(todo) == 0 {
		return 0, err
	}
	if s.Rails == nil {
		return 0, refused("this server cannot reach payment services")
	}
	r, err = s.Rails.ResolveChannel(ctx, tenant, channel, "virtual_account")
	if err != nil {
		return 0, err
	}
	n := 0
	for _, st := range todo[:min(len(todo), per)] {
		if ctx.Err() != nil {
			return n, ctx.Err()
		}
		_, err := s.issue(ctx, tenant, r, account, st, issueOpts{job: &id, by: "system:schools"})
		n++
		if err != nil && !errors.Is(err, ErrState) && !errors.Is(err, ErrInvalid) && !errors.Is(err, errIssue) {
			return n, err
		}
	}
	if err := s.In(ctx, tenant, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `UPDATE school_account_jobs SET updated_at = now() WHERE tenant_id = $1 AND id = $2`, tenant, id)
		return err
	}); err != nil {
		return n, err
	}
	return n, nil
}

// --- issuing one ---

// IssueIn is what issuing one student's account takes beyond the student.
type IssueIn struct {
	// BVN or NIN is a parent's, for rails that need one per account
	// (Flutterwave): sent to the rail, never kept.
	BVN string `json:"bvn,omitempty"`
	NIN string `json:"nin,omitempty"`
	// Retry asks again for a student whose answer was lost on a rail
	// where asking again could open a second account: after a person
	// checked with the rail that none was opened.
	Retry bool `json:"retry,omitempty"`
}

// errIssue is a rail's refusal of one student, recorded on their row.
var errIssue = errors.New("schools: refused by the rail")

// IssueRefusal is a rail's refusal of one student, for the reason given
// (ErrIssue reports it; tests of callers).
func IssueRefusal(reason string) error { return fmt.Errorf("%w: %s", errIssue, reason) }

// IssueOne issues a student's account on the school's issuing connection,
// once: a student who has one answers it. A refusal is recorded on the
// student's row and answered as ErrIssue's message (the rail's words).
func (s *Schools) IssueOne(ctx context.Context, tenant uuid.UUID, idOrNumber string, in IssueIn, by string) (StudentAccount, error) {
	var r invoicing.Rail
	var st Issuing
	var student Student
	err := s.In(ctx, tenant, func(tx pgx.Tx) error {
		if err := need(ctx, tx, tenant); err != nil {
			return err
		}
		var err error
		if student, err = s.student(ctx, tx, tenant, idOrNumber); err != nil {
			return err
		}
		r, st, err = s.issuer(ctx, tx, tenant)
		return err
	})
	if err != nil {
		return StudentAccount{}, err
	}
	in.BVN, in.NIN = strings.TrimSpace(in.BVN), strings.TrimSpace(in.NIN)
	for _, v := range []string{in.BVN, in.NIN} {
		if v != "" && !elevenDigits(v) {
			return StudentAccount{}, invalid("a BVN or NIN has 11 digits")
		}
	}
	return s.issue(ctx, tenant, r, st.Account, student.ID, issueOpts{bvn: in.BVN, nin: in.NIN, retry: in.Retry, by: by})
}

func elevenDigits(s string) bool {
	if len(s) != 11 {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

type issueOpts struct {
	job      *uuid.UUID
	bvn, nin string
	retry    bool
	by       string
}

// issue issues one student's account on rail r (account: the wallet),
// claimed on their row first, so it is asked for once however often it is
// called.
func (s *Schools) issue(ctx context.Context, tenant uuid.UUID, r invoicing.Rail, account string, student uuid.UUID, o issueOpts) (StudentAccount, error) {
	channel := r.Channel()
	var t target
	var school, subject string
	var asked string
	var shortened, flagged bool
	var done *StudentAccount
	skipped := ""
	err := s.In(ctx, tenant, func(tx pgx.Tx) error {
		done, skipped = nil, ""
		if err := need(ctx, tx, tenant); err != nil {
			return err
		}
		// One issue of a student's account at a time: the row's lock.
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('taskiem school account ' || $1::text || ' ' || $2, 0))`, student, channel); err != nil {
			return err
		}
		ts, err := s.targets(ctx, tx, tenant, channel, []uuid.UUID{student})
		if err != nil {
			return err
		}
		if len(ts) == 0 {
			return ErrNotFound
		}
		t = ts[0]
		now := s.now()
		switch {
		case t.state == AccountIssued:
			list, err := s.StudentAccounts(ctx, tx, tenant, student)
			for i := range list {
				if list[i].Connection == channel {
					done = &list[i]
				}
			}
			return err
		case t.state == AccountIssuing && t.claimed != nil && now.Sub(*t.claimed) < claimFor:
			return refused("the student's account is being issued now")
		case t.state == AccountIssuing && r.Unsafe("virtual_account"):
			// Cut short on a rail where asking again could open a second
			// account: a person checks with the rail first.
			skipped = "the answer from " + r.Name + " was lost: check with " + r.Name + " whether it opened an account before trying again"
			return setAccountRow(ctx, tx, tenant, student, channel, AccountUnknown, skipped, false, o.job, o.by)
		case t.state == AccountUnknown && !o.retry:
			return refused("the answer from %s was lost: check with %s whether it opened an account, then try again", r.Name, r.Name)
		case t.student.Status != Active:
			return invalid("only active students are issued account numbers")
		}
		if why := cannot(r, t, o.bvn != "" || o.nin != ""); why != "" {
			if o.job != nil {
				skipped = why
				return setAccountRow(ctx, tx, tenant, student, channel, AccountFailed, why, false, o.job, o.by)
			}
			return invalid("%s", why)
		}
		if FactsOf(r).NeedsBVN && o.bvn == "" && o.nin == "" {
			return invalid("%s needs a parent's BVN or NIN for this account", r.Name)
		}
		if FactsOf(r).OneAccountPerEmail && o.job == nil {
			// A batch's plan checked this for its students already.
			taken, err := s.emailTaken(ctx, tx, tenant, channel, t)
			if err != nil {
				return err
			}
			if taken {
				return invalid("%s opens one account per email, and this student's guardian email is a brother's or sister's already: give a guardian another email", r.Name)
			}
		}
		if err := tx.QueryRow(ctx, `SELECT t.name, s.subject_id FROM tenants t, school_students s WHERE t.id = $1 AND s.tenant_id = $1 AND s.id = $2`, tenant, student).Scan(&school, &subject); err != nil {
			return err
		}
		limit := FactsOf(r).NameLimit
		asked, shortened, flagged = AccountName(school, t.student.Name, limit)
		// Claimed: no one else asks for it while this does.
		_, err = tx.Exec(ctx, `INSERT INTO school_student_accounts (tenant_id, student_id, channel, account, status, shortened, flagged, attempts, job_id, claimed_at, created_by)
			VALUES ($1, $2, $3, $4, 'issuing', $5, $6, 1, $7, $8, $9)
			ON CONFLICT (tenant_id, student_id, channel) DO UPDATE SET status = 'issuing', shortened = $5, flagged = $6, attempts = CASE WHEN $7::uuid IS NOT NULL AND school_student_accounts.job_id IS DISTINCT FROM $7 THEN 1 ELSE school_student_accounts.attempts + 1 END,
			  job_id = COALESCE($7, school_student_accounts.job_id), claimed_at = $8, error = NULL, retry = false, updated_at = now()`,
			tenant, student, channel, account, shortened, flagged, o.job, now, o.by)
		return err
	})
	switch {
	case err != nil:
		return StudentAccount{}, err
	case done != nil:
		return *done, nil
	case skipped != "":
		return StudentAccount{}, fmt.Errorf("%w: %s", errIssue, skipped) // recorded on the row
	}
	// The one path every customer's account number is issued on.
	nameAsked := r.Takes("virtual_account", "account_name")
	issued, callErr := invoicing.IssueOnRail(ctx, s.Rails, tenant, r, invoicing.IssueIn{Customer: invoicing.Customer{Name: t.student.Name, Email: t.email, Phone: t.phone},
		AccountName: asked, Account: account, BVN: o.bvn, NIN: o.nin}, AccountKey(tenant, student, channel))
	var result StudentAccount
	err = s.In(ctx, tenant, func(tx pgx.Tx) error {
		if callErr != nil {
			state, retry := AccountFailed, false
			switch {
			case errors.Is(callErr, effects.ErrUnknownOutcome) || errors.Is(callErr, effects.ErrIndeterminate) || errors.Is(callErr, context.DeadlineExceeded):
				if r.Unsafe("virtual_account") {
					state = AccountUnknown
				} else {
					retry = true // the same key answers the same account
				}
			case errors.Is(callErr, effects.ErrRetryable):
				retry = true
			}
			return setAccountRow(ctx, tx, tenant, student, channel, state, railReason(callErr), retry, o.job, o.by)
		}
		reg, err := s.Invoices.RegisterAccount(ctx, tx, tenant, invoicing.AccountIn{Customer: invoicing.Customer{Name: t.student.Name, Key: StudentKey(student)},
			Channel: channel, AccountNumber: issued.AccountNumber, Reference: issued.Reference, BankName: issued.BankName}, o.by)
		if err != nil {
			if errors.Is(err, invoicing.ErrInvalid) || errors.Is(err, invoicing.ErrState) {
				callErr = err
				return setAccountRow(ctx, tx, tenant, student, channel, AccountFailed, "Taskiem could not keep the account "+r.Name+" answered: "+plainInvoicing(err), false, o.job, o.by)
			}
			return err
		}
		d := accountSealed{Named: issued.AccountName}
		if nameAsked {
			d.Asked = asked
		}
		sealed, err := s.Vault.SealUnderTx(ctx, tx, tenant, subject, "student", d)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE school_student_accounts SET status = 'issued', account_id = $4, details = $5, error = NULL, retry = false, claimed_at = NULL, updated_at = now()
			WHERE tenant_id = $1 AND student_id = $2 AND channel = $3`, tenant, student, channel, reg.ID, sealed); err != nil {
			return err
		}
		// Its credits are read back (the look-back), on its wallet.
		if r.Maps("list_credits") {
			if _, err := tx.Exec(ctx, `INSERT INTO money_lookbacks (tenant_id, channel, account) VALUES ($1, $2, $3) ON CONFLICT DO NOTHING`, tenant, channel, account); err != nil {
				return err
			}
		}
		if err := audit(ctx, tx, tenant, o.by, "school.account.issue", student.String(), map[string]any{"connection": channel, "account": reg.ID.String(),
			"shortened": shortened, "flagged": flagged}); err != nil {
			return err
		}
		list, err := s.StudentAccounts(ctx, tx, tenant, student)
		for _, a := range list {
			if a.Connection == channel {
				result = a
			}
		}
		return err
	})
	if err != nil {
		return StudentAccount{}, err
	}
	if callErr != nil {
		return StudentAccount{}, fmt.Errorf("%w: %s", errIssue, railReason(callErr))
	}
	return result, nil
}

// ErrIssue reports whether err is a rail's refusal of one student (its
// message is the rail's reason).
func ErrIssue(err error) bool { return errors.Is(err, errIssue) }

// setAccountRow records how issuing a student's account went: when it
// failed, the rail's reason. job and by name the batch and who asked, for
// a row not yet written.
func setAccountRow(ctx context.Context, tx pgx.Tx, tenant, student uuid.UUID, channel, state, reason string, retry bool, job *uuid.UUID, by string) error {
	if len(reason) > 300 {
		reason = reason[:300]
	}
	_, err := tx.Exec(ctx, `INSERT INTO school_student_accounts (tenant_id, student_id, channel, status, error, retry, job_id, created_by)
		VALUES ($1, $2, $3, $4, NULLIF($5, ''), $6, $7, $8)
		ON CONFLICT (tenant_id, student_id, channel) DO UPDATE SET status = $4, error = NULLIF($5, ''), retry = $6, claimed_at = NULL,
		  job_id = COALESCE($7, school_student_accounts.job_id), updated_at = now()`, tenant, student, channel, state, reason, retry, job, by)
	return err
}

// railReason is a rail's error in its own words, without the engine's
// classification.
func railReason(err error) string {
	m := err.Error()
	for _, suffix := range []string{": " + effects.ErrFatal.Error(), ": " + effects.ErrRetryable.Error(), ": " + effects.ErrUnknownOutcome.Error(), ": " + effects.ErrIndeterminate.Error()} {
		m = strings.ReplaceAll(m, suffix, "")
	}
	if len(m) > 300 {
		m = m[:300]
	}
	return m
}

func audit(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, actor, action, target string, detail map[string]any) error {
	raw, _ := json.Marshal(detail)
	kind := "user"
	if !strings.HasPrefix(actor, "user:") {
		kind = "system"
	}
	_, err := tx.Exec(ctx, `SELECT taskiem_audit_append($1, $2, $3, $4, $5, $6)`, tenant, kind, actor, action, target, raw)
	return err
}

// plainInvoicing is an invoicing error without its package's prefixes.
func plainInvoicing(err error) string {
	msg := err.Error()
	for _, p := range []string{"invoicing: invalid: ", "invoicing: not allowed in the invoice's status: ", "invoicing: "} {
		msg = strings.ReplaceAll(msg, p, "")
	}
	return msg
}

// --- sending the account number to the family ---

// IssuedAccount is a student's issued account: the one on the school's
// issuing connection, else any issued one; ok is false when they have
// none.
func (s *Schools) IssuedAccount(ctx context.Context, tx pgx.Tx, tenant, student uuid.UUID) (StudentAccount, bool, error) {
	list, err := s.StudentAccounts(ctx, tx, tenant, student)
	if err != nil {
		return StudentAccount{}, false, err
	}
	st, err := GetIssuing(ctx, tx, tenant)
	if err != nil {
		return StudentAccount{}, false, err
	}
	for _, want := range []string{st.Channel, ""} {
		for _, a := range list {
			if a.Status == "issued" && a.AccountNumber != "" && (want == "" || a.Connection == want) {
				return a, true, nil
			}
		}
	}
	return StudentAccount{}, false, nil
}

// AccountNumberVars are the taskiem_account_number template's variables
// (docs/whatsapp.md#templates): the child, the number, the bank and the
// name to expect, with the family page's link for the button.
func AccountNumberVars(child string, a StudentAccount, link string) map[string]string {
	or := func(s string) string {
		if s == "" || s == Erased {
			return "-"
		}
		return s
	}
	return map[string]string{"who": or(child), "account_number": a.AccountNumber, "bank": or(a.BankName), "account_name": or(a.AccountName), "link": link}
}

// AccountNumberText is the message a family gets with a child's account
// number: the number, the bank and the name they should see when they
// transfer, and the family page's link.
func AccountNumberText(child string, a StudentAccount, link string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Dear parent, %s's own account number for school fees is %s", child, a.AccountNumber)
	switch {
	case a.BankName != "" && a.AccountName != "":
		fmt.Fprintf(&b, " (%s, in the name %s)", a.BankName, a.AccountName)
	case a.BankName != "":
		fmt.Fprintf(&b, " (%s)", a.BankName)
	case a.AccountName != "":
		fmt.Fprintf(&b, " (in the name %s)", a.AccountName)
	}
	fmt.Fprintf(&b, ". Pay the fees by transfer to it: the money lands on %s's fees by itself, and a receipt follows.", child)
	if link != "" {
		b.WriteString(" Your fees page: " + link)
	}
	return b.String()
}

// AccountNumberShort is the SMS: the same facts in fewer words.
func AccountNumberShort(child string, a StudentAccount, link string) string {
	s := fmt.Sprintf("%s's own account number for school fees: %s", child, a.AccountNumber)
	if a.BankName != "" {
		s += ", " + a.BankName
	}
	if a.AccountName != "" {
		s += ", name " + a.AccountName
	}
	s += ". Pay by transfer to it and a receipt follows."
	if link != "" {
		s += " Fees page: " + link
	}
	return s
}

// AccountSent is what sending a student's account number did.
type AccountSent struct {
	Student Student        `json:"student"`
	Account StudentAccount `json:"account"`
	Sent    FamilySent     `json:"sent"`
}

// SendAccountNumber sends a student's family their account number, in
// tx: on each guardian's chat app (as taskiem_account_number outside
// WhatsApp's window, the family page's link on its button) or by SMS,
// once per key (a step's key, or a fresh one from the student's page). A
// student with no account number yet is refused; one in no family reaches
// nobody.
func (s *Schools) SendAccountNumber(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, st Student, key, by string) (AccountSent, error) {
	out := AccountSent{Student: st}
	a, ok, err := s.IssuedAccount(ctx, tx, tenant, st.ID)
	if err != nil {
		return out, err
	}
	if !ok {
		return out, refused("student %s has no account number yet", st.AdmissionNumber)
	}
	out.Account = a
	if st.FamilyID == nil {
		out.Sent = FamilySent{Channels: map[string]int{}, Nobody: true}
		return out, nil
	}
	child := st.Name
	if child == "" || child == Erased {
		child = st.AdmissionNumber
	}
	out.Sent, err = s.SendFamily(ctx, tx, tenant, *st.FamilyID, FamilyMessage{Purpose: "account", Key: "account:" + st.ID.String() + ":" + key,
		Text:     func(link string) string { return AccountNumberText(child, a, link) },
		Short:    func(link string) string { return AccountNumberShort(child, a, link) },
		Template: &whatsapp.TplAccountNumber,
		Vars:     func(link string) map[string]string { return AccountNumberVars(child, a, link) }}, by)
	return out, err
}

// SendAccountNumberOnce sends a student's (by id or admission number)
// account number to the family in a transaction of its own, once per key
// (SendAccountNumber).
func (s Service) SendAccountNumberOnce(ctx context.Context, tenant uuid.UUID, idOrNumber, key, by string) (AccountSent, error) {
	var out AccountSent
	err := s.In(ctx, tenant, func(tx pgx.Tx) error {
		if err := need(ctx, tx, tenant); err != nil {
			return err
		}
		st, err := s.student(ctx, tx, tenant, idOrNumber)
		if err != nil {
			return err
		}
		out, err = s.SendAccountNumber(ctx, tx, tenant, st, key, by)
		return err
	})
	return out, err
}
