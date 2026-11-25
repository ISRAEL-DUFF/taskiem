package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/connectors/payrolla"
	"github.com/israel-duff/taskiem/engine/access"
	"github.com/israel-duff/taskiem/engine/connector"
	phonenum "github.com/israel-duff/taskiem/engine/phone"
	"github.com/israel-duff/taskiem/engine/saved"
)

// Staff and pay from Taskiem (P4b-2 of docs/spec/chat-pay-people.md):
// Payroll → Team (everyone in Payrolla, linked staff marked, pay shown to
// payroll.read), adding someone with their bank account checked, changing
// their pay from a date or their details (never the phone, and there is
// no job title), ending their employment (at once), a changed salary
// account held until another approver approves it with Payrolla's code,
// Payroll → Account changes, and a staff member's own My pay, where they
// ask for a new salary account and type the code Payrolla texts them.
//
// Nothing of pay or bank numbers is kept in Taskiem: each screen reads
// Payrolla when it opens. An account number passes through to Payrolla
// once and is not logged or audited. The audit says who asked for which
// held change (by Payrolla's id for it), so that whoever asked is not
// sent the approval, and is told how it was decided.

// Who may: the team is for payroll.read or payroll.prepare (which comes
// with payroll.read); account changes are also for payroll.approve.
const (
	PermPayrollTeam    = access.PayrollRead + "|" + access.PayrollPrepare
	PermPayrollChanges = access.PayrollRead + "|" + access.PayrollPrepare + "|" + access.PayrollApprove
)

func (s *Server) payrollTeamRoutes(r chi.Router) {
	r.Get("/payroll/banks", s.payrollBanks)
	r.With(s.need(PermPayrollTeam)).Get("/payroll/team", s.getPayrollTeam)
	r.With(s.need(PermPayrollTeam)).Get("/payroll/team/{employee}", s.getPayrollEmployee)
	r.With(s.need(PermPayrollPrepare), s.tenantWide).Post("/payroll/team", s.addPayrollEmployee)
	r.With(s.need(PermPayrollPrepare), s.tenantWide).Patch("/payroll/team/{employee}", s.changePayrollEmployee)
	r.With(s.need(PermPayrollPrepare), s.tenantWide).Post("/payroll/team/{employee}/bank", s.askPayrollBankChange)
	r.With(s.need(PermPayrollPrepare), s.tenantWide).Post("/payroll/team/{employee}/end", s.endPayrollEmployee)
	r.With(s.need(PermPayrollPrepare)).Post("/payroll/name-check", s.payrollNameCheck)
	r.With(s.need(PermPayrollChanges)).Get("/payroll/bank-changes", s.getPayrollBankChanges)
	r.With(s.need(PermPayrollApprove), s.tenantWide).Post("/payroll/bank-changes/{change}/code", s.startBankChangeCode)
	r.With(s.need(PermPayrollApprove), s.tenantWide).Post("/payroll/bank-changes/{change}/approve", s.approveBankChange)
	r.With(s.need(PermPayrollApprove), s.tenantWide).Post("/payroll/bank-changes/{change}/reject", s.rejectBankChange)
	// My pay: a staff member's own, linked to their Payrolla employee.
	r.Get("/payroll/me", s.getMyPay)
	r.With(s.tenantWide).Post("/payroll/me/bank-change", s.startMyBankChange)
	r.With(s.tenantWide).Post("/payroll/me/bank-change/confirm", s.confirmMyBankChange)
}

// --- banks ---

// payrollBank is a bank Payrolla pays into, by CBN's code.
type payrollBank struct {
	Code string `json:"code"`
	Name string `json:"name"`
}

// otherInstitutions are the banks offered by name whose CBN code is not a
// deposit money bank's (saved.CBNCode does not know them).
var otherInstitutions = map[string]string{
	"Kuda Microfinance Bank": "50211", "Moniepoint Microfinance Bank": "50515", "OPay Digital Services Limited": "999992",
	"PalmPay Limited": "999991", "Signature Bank": "106", "Taj Bank": "302", "Titan Trust Bank": "102",
}

// payrollBankList is the banks offered for a salary account, by name.
func payrollBankList() []payrollBank {
	out := make([]payrollBank, 0, len(saved.Banks))
	for _, n := range saved.Banks {
		code := saved.CBNCode("", n)
		if c, ok := otherInstitutions[n]; ok {
			code = c
		}
		if code != "" {
			out = append(out, payrollBank{Code: code, Name: n})
		}
	}
	return out
}

// bankName is a bank's name by its code, or the code.
func bankName(code string) string {
	for _, b := range payrollBankList() {
		if b.Code == code {
			return b.Name
		}
	}
	return code
}

// payrollBanks is GET /v1/payroll/banks: the banks a salary account can
// be at.
func (s *Server) payrollBanks(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"banks": payrollBankList()})
}

var tenDigitsRe = regexp.MustCompile(`^[0-9]{10}$`)

// bankIn is an account typed into a form: sent to Payrolla once, kept
// nowhere.
type bankIn struct {
	BankCode      string `json:"bank_code"`
	AccountNumber string `json:"account_number"`
}

func (b bankIn) clean() (payrolla.BankInput, error) {
	num := strings.Map(func(r rune) rune {
		if r == ' ' || r == '-' {
			return -1
		}
		return r
	}, b.AccountNumber)
	code := strings.TrimSpace(b.BankCode)
	if !slices.ContainsFunc(payrollBankList(), func(x payrollBank) bool { return x.Code == code }) {
		return payrolla.BankInput{}, refusePay(http.StatusBadRequest, "bank", "Choose the bank from the list.")
	}
	if !tenDigitsRe.MatchString(num) {
		return payrolla.BankInput{}, refusePay(http.StatusBadRequest, "account_number", "An account number is 10 digits.")
	}
	return payrolla.BankInput{BankCode: code, AccountNumber: num}, nil
}

// payrollNameCheck is POST /v1/payroll/name-check {bank_code,
// account_number} (payroll.prepare): the bank's name for the account,
// through the business's payments connection, before it goes to Payrolla.
// Without one, Payrolla's own check names it when it is saved
// ({checked: false}).
func (s *Server) payrollNameCheck(w http.ResponseWriter, r *http.Request) {
	var in bankIn
	if err := decodeBody(r, &in); err != nil {
		s.fail(w, r, err)
		return
	}
	b, err := in.clean()
	if err != nil {
		s.payFail(w, r, err)
		return
	}
	ctx := r.Context()
	tenant := principalFrom(ctx).TenantID
	later := map[string]any{"checked": false, "words": "Payrolla checks the account with the bank when you save, and shows the bank's name for it."}
	rl, err := s.savedRail(ctx, tenant)
	if err != nil || rl == nil {
		writeJSON(w, http.StatusOK, later)
		return
	}
	acct, err := saved.BankAccount{BankName: bankName(b.BankCode), AccountNumber: b.AccountNumber}.Clean()
	if err != nil {
		s.payFail(w, r, refusePay(http.StatusBadRequest, "account_number", "An account number is 10 digits."))
		return
	}
	res, _, err := s.checkWithRail(ctx, tenant, *rl, acct)
	if err != nil {
		s.savedFail(w, r, err)
		return
	}
	if res == nil {
		writeJSON(w, http.StatusOK, later)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"checked": true, "account_name": res.AccountName, "bank": bankName(b.BankCode), "last4": saved.Last4(b.AccountNumber)})
}

// --- the team ---

// teamBank is an account as a screen shows it.
type teamBank struct {
	Bank        string `json:"bank"`
	BankCode    string `json:"bank_code"`
	AccountName string `json:"account_name"`
	Last4       string `json:"last4"`
}

func teamBankOf(b *payrolla.MaskedBank) *teamBank {
	if b == nil {
		return nil
	}
	return &teamBank{Bank: bankName(b.BankCode), BankCode: b.BankCode, AccountName: b.AccountName, Last4: b.Last4}
}

// teamChange is a held account change as a screen shows it.
type teamChange struct {
	ID          string `json:"id"`
	EmployeeID  string `json:"employee_id"`
	Employee    string `json:"employee,omitempty"`
	Bank        string `json:"bank"`
	AccountName string `json:"account_name"`
	Last4       string `json:"last4"`
	// Via is in_taskiem, by_employee or in_payrolla; AskedBy who asked,
	// when it was someone in Taskiem; Mine: this person asked.
	Via         string `json:"via"`
	AskedBy     string `json:"asked_by,omitempty"`
	Mine        bool   `json:"mine"`
	RequestedAt string `json:"requested_at"`
}

func changeOf(c *payrolla.BankChange) *teamChange {
	if c == nil {
		return nil
	}
	via := map[string]string{"CONNECT": "in_taskiem", "EMPLOYEE": "by_employee"}[c.RequestedVia]
	if via == "" {
		via = "in_payrolla"
	}
	return &teamChange{ID: c.ID, EmployeeID: c.EmployeeID, Bank: bankName(c.BankCode), AccountName: c.AccountName, Last4: c.Last4, Via: via, RequestedAt: c.RequestedAt}
}

// teamPay is someone's monthly pay, in kobo.
type teamPay struct {
	GrossKobo     int64  `json:"gross_kobo"`
	BasicKobo     int64  `json:"basic_kobo"`
	HousingKobo   int64  `json:"housing_kobo"`
	TransportKobo int64  `json:"transport_kobo"`
	Pensionable   bool   `json:"pensionable"`
	From          string `json:"from"`
}

// teamMember is an employee on Payroll → Team.
type teamMember struct {
	ID         string           `json:"id"`
	Name       string           `json:"name"`
	FirstName  string           `json:"first_name"`
	LastName   string           `json:"last_name"`
	Email      string           `json:"email,omitempty"`
	Phone      string           `json:"phone,omitempty"`
	Department string           `json:"department,omitempty"`
	StartDate  string           `json:"start_date,omitempty"`
	Status     string           `json:"status"` // active, inactive, left
	Bank       *teamBank        `json:"bank"`
	Pay        *teamPay         `json:"pay,omitempty"`
	Waiting    *teamChange      `json:"waiting_change"`
	Staff      *payrollStaffRef `json:"staff"`
}

func memberOf(e payrolla.Employee, pay bool, staff map[string]payrollStaffRef) teamMember {
	m := teamMember{ID: e.ID, Name: e.Name(), FirstName: e.FirstName, LastName: e.LastName, Email: e.Email, Phone: e.Phone, Department: e.Department,
		StartDate: e.StartDate, Status: map[string]string{"ACTIVE": "active", "INACTIVE": "inactive", "TERMINATED": "left"}[e.Status],
		Bank: teamBankOf(e.Bank), Waiting: changeOf(e.PendingBank)}
	if m.Status == "" {
		m.Status = strings.ToLower(e.Status)
	}
	if pay && e.Pay != nil {
		m.Pay = &teamPay{GrossKobo: e.Pay.GrossKobo, BasicKobo: e.Pay.BasicKobo, HousingKobo: e.Pay.HousingKobo, TransportKobo: e.Pay.TransportKobo,
			Pensionable: e.Pay.Pensionable, From: dateOf(e.Pay.EffectiveFrom)}
	}
	if m.Waiting != nil {
		m.Waiting.Employee = m.Name
	}
	if st, ok := staff[e.ID]; ok {
		m.Staff = &st
	}
	return m
}

func dateOf(s string) string {
	if len(s) >= 10 {
		return s[:10]
	}
	return s
}

// seesPay: payroll.read, or payroll.prepare which comes with it.
func seesPay(p *Principal) bool {
	return p.Permissions[PermPayrollRead] || p.Permissions[PermPayrollPrepare]
}

// teamMay is what the person may do on the team's screens.
func teamMay(p *Principal) map[string]bool {
	return map[string]bool{"prepare": p.Permissions[PermPayrollPrepare], "approve": p.Permissions[PermPayrollApprove], "see_pay": seesPay(p)}
}

// teamStaff is a staff member not yet in Payrolla, for Add to payroll.
type teamStaff struct {
	UserID uuid.UUID `json:"user_id"`
	Name   string    `json:"name"`
	Email  string    `json:"email,omitempty"`
	Phone  string    `json:"phone,omitempty"`
}

// getPayrollTeam is GET /v1/payroll/team (payroll.read or
// payroll.prepare): everyone in Payrolla, those who left last, with their
// account masked, a waiting account change, the staff member they are in
// Taskiem, and pay to those who may see it; and the staff not yet in
// Payrolla, for Add to payroll.
func (s *Server) getPayrollTeam(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	p := principalFrom(ctx)
	cl, conn, err := s.payrollaClient(ctx, p, "payroll.team")
	if err != nil && !errors.Is(err, errPayrollaNotConnected) {
		s.fail(w, r, err)
		return
	}
	var emps []payrolla.Employee
	if err == nil {
		emps, err = cl.AllEmployees(ctx, "")
	}
	if err != nil {
		s.payFail(w, r, s.teamSays(ctx, p, conn, err, "employees"))
		return
	}
	links, err := s.payrollaLinks(ctx, p.TenantID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	out := make([]teamMember, 0, len(emps))
	for _, e := range emps {
		out = append(out, memberOf(e, seesPay(p), links))
	}
	slices.SortStableFunc(out, func(a, b teamMember) int {
		if (a.Status == "left") != (b.Status == "left") {
			if a.Status == "left" {
				return 1
			}
			return -1
		}
		return strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name))
	})
	var notIn []teamStaff
	if p.Permissions[PermPayrollPrepare] {
		linked := map[uuid.UUID]bool{}
		for _, l := range links {
			linked[l.UserID] = true
		}
		if err := s.tx(r, func(tx pgx.Tx) error {
			people, err := staffPeopleTx(ctx, tx, p, uuid.Nil, func(n string) string { return n })
			for _, m := range people {
				if !linked[m.UserID] {
					notIn = append(notIn, teamStaff{UserID: m.UserID, Name: m.Name, Email: emailOrNone(m.Email), Phone: m.Phone})
				}
			}
			return err
		}); err != nil {
			s.fail(w, r, err)
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"team": out, "staff": nonNil(notIn), "may": teamMay(p), "connection": conn.Name})
}

func emailOrNone(e string) string {
	if emailPlaceholder(e) {
		return ""
	}
	return e
}

// getPayrollEmployee is GET /v1/payroll/team/{employee}: one person.
func (s *Server) getPayrollEmployee(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	p := principalFrom(ctx)
	id := chi.URLParam(r, "employee")
	if _, err := uuid.Parse(id); err != nil {
		s.fail(w, r, pgx.ErrNoRows)
		return
	}
	cl, conn, err := s.payrollaClient(ctx, p, "payroll.team")
	if err != nil && !errors.Is(err, errPayrollaNotConnected) {
		s.fail(w, r, err)
		return
	}
	var e payrolla.Employee
	if err == nil {
		e, err = cl.Employee(ctx, id)
	}
	if err != nil {
		s.payFail(w, r, s.teamSays(ctx, p, conn, err, "employees"))
		return
	}
	links, err := s.payrollaLinks(ctx, p.TenantID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"employee": memberOf(e, seesPay(p), links), "may": teamMay(p)})
}

// teamPayIn is monthly pay as a form gives it, in kobo; From (YYYY-MM-DD) on
// a change, today when empty.
type teamPayIn struct {
	GrossKobo     int64  `json:"gross_kobo"`
	BasicKobo     int64  `json:"basic_kobo"`
	HousingKobo   int64  `json:"housing_kobo"`
	TransportKobo int64  `json:"transport_kobo"`
	Pensionable   *bool  `json:"pensionable"`
	From          string `json:"from"`
}

// payMax is ₦100,000,000 a month: more is a typing slip.
const payMax = 100_000_000_00

func (in *teamPayIn) clean(change bool) (*payrolla.PayInput, error) {
	if in == nil {
		return nil, nil
	}
	parts := in.BasicKobo + in.HousingKobo + in.TransportKobo
	switch {
	case in.GrossKobo < 1 || in.GrossKobo > payMax:
		return nil, refusePay(http.StatusBadRequest, "pay", "Give their monthly pay.")
	case in.BasicKobo < 0 || in.HousingKobo < 0 || in.TransportKobo < 0 || parts > in.GrossKobo:
		return nil, refusePay(http.StatusBadRequest, "pay", "Basic, housing and transport can't add up to more than the monthly pay.")
	}
	out := &payrolla.PayInput{GrossKobo: in.GrossKobo, BasicKobo: in.BasicKobo, HousingKobo: in.HousingKobo, TransportKobo: in.TransportKobo, Pensionable: in.Pensionable}
	if change && strings.TrimSpace(in.From) != "" {
		d, err := time.Parse("2006-01-02", strings.TrimSpace(in.From))
		if err != nil {
			return nil, refusePay(http.StatusBadRequest, "from", "Give the date the new pay starts.")
		}
		out.EffectiveFrom = d.Format("2006-01-02")
	}
	return out, nil
}

// trimmed is a form's text, trimmed, refused when longer than max.
func trimmed(v, field string, maxRunes int) (string, error) {
	v = strings.TrimSpace(v)
	if utf8.RuneCountInString(v) > maxRunes {
		return "", refusePay(http.StatusBadRequest, field, fmt.Sprintf("That is too long: %d characters at most.", maxRunes))
	}
	return v, nil
}

// addPayrollEmployee is POST /v1/payroll/team {key, first_name, last_name,
// phone?, email?, start_date, department?, cost centre?, pay, bank?,
// staff_user?} (payroll.prepare): someone added to Payrolla with their
// account checked by the bank (its name comes back). Added from a staff
// member (Add to payroll), they get the staff member's id as their
// external_ref and the staff link is made at once.
func (s *Server) addPayrollEmployee(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Key        string     `json:"key"`
		FirstName  string     `json:"first_name"`
		LastName   string     `json:"last_name"`
		Phone      string     `json:"phone"`
		Email      string     `json:"email"`
		StartDate  string     `json:"start_date"`
		Department string     `json:"department"`
		CostCenter string     `json:"cost_center"` //nolint:misspell // Payrolla's field
		Pay        *teamPayIn `json:"pay"`
		Bank       *bankIn    `json:"bank"`
		StaffUser  *uuid.UUID `json:"staff_user"`
	}
	if err := decodeBody(r, &in); err != nil {
		s.fail(w, r, err)
		return
	}
	ctx := r.Context()
	p := principalFrom(ctx)
	ne, err := func() (payrolla.NewEmployee, error) {
		var ne payrolla.NewEmployee
		var err error
		for _, f := range []struct {
			dst          *string
			v, field     string
			maxRunes     int
			required     bool
			requiredText string
		}{
			{&ne.FirstName, in.FirstName, "first_name", 80, true, "Give their first name."},
			{&ne.LastName, in.LastName, "last_name", 80, true, "Give their last name."},
			{&ne.Email, in.Email, "email", 254, false, ""},
			{&ne.Department, in.Department, "department", 80, false, ""},
			{&ne.CostCenter, in.CostCenter, "cost_center", 80, false, ""}, //nolint:misspell // Payrolla's field
		} {
			if *f.dst, err = trimmed(f.v, f.field, f.maxRunes); err != nil {
				return ne, err
			}
			if f.required && *f.dst == "" {
				return ne, refusePay(http.StatusBadRequest, f.field, f.requiredText)
			}
		}
		if ph := strings.TrimSpace(in.Phone); ph != "" {
			n, err := phonenum.Normalise(ph)
			if err != nil {
				return ne, refusePay(http.StatusBadRequest, "phone", "That phone number doesn't look right.")
			}
			ne.Phone = n
		}
		if ne.Email != "" && !strings.Contains(ne.Email, "@") {
			return ne, refusePay(http.StatusBadRequest, "email", "That email doesn't look right.")
		}
		if ne.Phone == "" && ne.Email == "" {
			return ne, refusePay(http.StatusBadRequest, "contact", "Give a phone number or an email, for their payslip.")
		}
		d, err := time.Parse("2006-01-02", strings.TrimSpace(in.StartDate))
		if err != nil {
			return ne, refusePay(http.StatusBadRequest, "start_date", "Give the date they start.")
		}
		ne.StartDate = d.Format("2006-01-02")
		if in.Pay == nil {
			return ne, refusePay(http.StatusBadRequest, "pay", "Give their monthly pay.")
		}
		if ne.Pay, err = in.Pay.clean(false); err != nil {
			return ne, err
		}
		if in.Bank != nil {
			b, err := in.Bank.clean()
			if err != nil {
				return ne, err
			}
			ne.Bank = &b
		}
		return ne, nil
	}()
	if err != nil {
		s.payFail(w, r, err)
		return
	}
	if in.StaffUser != nil {
		if err := s.tx(r, func(tx pgx.Tx) error { return payrollaMemberTx(ctx, tx, p.TenantID, *in.StaffUser) }); err != nil {
			s.payFail(w, r, err)
			return
		}
		ne.ExternalRef = payrolla.StaffRef(*in.StaffUser)
	}
	cl, conn, err := s.payrollaClient(ctx, p, "payroll.team.add")
	if err != nil && !errors.Is(err, errPayrollaNotConnected) {
		s.fail(w, r, err)
		return
	}
	if err == nil {
		var key string
		if key, err = payrollaKey(conn, "employee-add", in.Key); err != nil {
			s.fail(w, r, err)
			return
		}
		var e payrolla.Employee
		if e, err = cl.AddEmployee(ctx, ne, payrolla.Write{Key: key, User: p.UserID, Name: s.actingName(ctx, p)}); err == nil {
			s.forgetPayrollNames(p.TenantID)
			if in.StaffUser != nil {
				if err := s.linkStaffToEmployee(r, *in.StaffUser, e); err != nil {
					s.payFail(w, r, err)
					return
				}
			}
			_ = s.tx(r, func(tx pgx.Tx) error {
				return auditTx(r, tx, "payroll.employee_add", "payrolla_employee:"+e.ID, map[string]any{"from_staff": in.StaffUser != nil})
			})
			links, _ := s.payrollaLinks(ctx, p.TenantID)
			writeJSON(w, http.StatusCreated, map[string]any{"employee": memberOf(e, seesPay(p), links)})
			return
		}
		if payrolla.Code(err) == "EXTERNAL_REF_TAKEN" {
			// Open them: the one Payrolla already has.
			if had, herr := cl.EmployeeByRef(ctx, ne.ExternalRef); herr == nil && had != nil {
				writeJSON(w, http.StatusConflict, map[string]any{"error": "This person is already in Payrolla.", "code": "already_in_payrolla", "employee_id": had.ID})
				return
			}
		}
	}
	s.payFail(w, r, s.teamSays(ctx, p, conn, err, "employees"))
}

// linkStaffToEmployee makes the staff link P4a's owners make by hand, for
// an employee Taskiem added from that staff member (external_ref).
func (s *Server) linkStaffToEmployee(r *http.Request, user uuid.UUID, e payrolla.Employee) error {
	ctx := r.Context()
	p := principalFrom(ctx)
	name := clipRunes(e.Name(), payrollaNameMax)
	return s.tx(r, func(tx pgx.Tx) error {
		if err := payrollaMemberTx(ctx, tx, p.TenantID, user); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO staff_profiles (tenant_id, user_id, payrolla_employee_id, payrolla_name, payrolla_linked_by, payrolla_linked_at, updated_by)
			VALUES ($1, $2, $3, $4, $5, now(), $5)
			ON CONFLICT (tenant_id, user_id) DO UPDATE SET payrolla_employee_id = EXCLUDED.payrolla_employee_id, payrolla_name = EXCLUDED.payrolla_name,
				payrolla_linked_by = EXCLUDED.payrolla_linked_by, payrolla_linked_at = EXCLUDED.payrolla_linked_at`,
			p.TenantID, user, e.ID, name, p.Actor()); err != nil {
			return err
		}
		return auditTx(r, tx, "staff.payrolla", user.String(), map[string]any{"user_id": user, "employee_id": e.ID, "added": true})
	})
}

// changePayrollEmployee is PATCH /v1/payroll/team/{employee} {key,
// first_name?, last_name?, email?, department?, cost centre?, pay?}
// (payroll.prepare): only what is sent changes; pay from a date (today by
// default). A phone or a job title is refused in words: Payrolla takes
// neither.
func (s *Server) changePayrollEmployee(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Key        string     `json:"key"`
		FirstName  *string    `json:"first_name"`
		LastName   *string    `json:"last_name"`
		Email      *string    `json:"email"`
		Department *string    `json:"department"`
		CostCenter *string    `json:"cost_center"` //nolint:misspell // Payrolla's field
		Pay        *teamPayIn `json:"pay"`
		Phone      *string    `json:"phone"`
		JobTitle   *string    `json:"job_title"`
	}
	if err := decodeBody(r, &in); err != nil {
		s.fail(w, r, err)
		return
	}
	ctx := r.Context()
	p := principalFrom(ctx)
	id := chi.URLParam(r, "employee")
	if _, err := uuid.Parse(id); err != nil {
		s.fail(w, r, pgx.ErrNoRows)
		return
	}
	switch {
	case in.Phone != nil:
		s.payFail(w, r, refusePay(http.StatusUnprocessableEntity, "phone_change", "A phone number can't be changed from Taskiem. Change it in Payrolla."))
		return
	case in.JobTitle != nil:
		s.payFail(w, r, refusePay(http.StatusUnprocessableEntity, "job_title", "Payrolla keeps no job title. Use the department instead."))
		return
	}
	var ch payrolla.EmployeeChange
	var fields []string
	for _, f := range []struct {
		dst      **string
		v        *string
		field    string
		required bool
	}{
		{&ch.FirstName, in.FirstName, "first_name", true}, {&ch.LastName, in.LastName, "last_name", true}, {&ch.Email, in.Email, "email", false},
		{&ch.Department, in.Department, "department", false}, {&ch.CostCenter, in.CostCenter, "cost_center", false}, //nolint:misspell // Payrolla's field
	} {
		if f.v == nil {
			continue
		}
		v, err := trimmed(*f.v, f.field, 254)
		if err == nil && f.required && v == "" {
			err = refusePay(http.StatusBadRequest, f.field, "A name can't be empty.")
		}
		if err == nil && f.field == "email" && v != "" && !strings.Contains(v, "@") {
			err = refusePay(http.StatusBadRequest, "email", "That email doesn't look right.")
		}
		if err != nil {
			s.payFail(w, r, err)
			return
		}
		*f.dst = &v
		fields = append(fields, f.field)
	}
	var err error
	if ch.Pay, err = in.Pay.clean(true); err != nil {
		s.payFail(w, r, err)
		return
	}
	if ch.Pay != nil {
		fields = append(fields, "pay")
	}
	if len(fields) == 0 {
		s.payFail(w, r, refusePay(http.StatusBadRequest, "nothing", "Nothing to change."))
		return
	}
	cl, conn, err := s.payrollaClient(ctx, p, "payroll.team.change")
	if err != nil && !errors.Is(err, errPayrollaNotConnected) {
		s.fail(w, r, err)
		return
	}
	if err == nil {
		var key string
		if key, err = payrollaKey(conn, "employee-change:"+id, in.Key); err != nil {
			s.fail(w, r, err)
			return
		}
		var e payrolla.Employee
		if e, err = cl.ChangeEmployee(ctx, id, ch, payrolla.Write{Key: key, User: p.UserID, Name: s.actingName(ctx, p)}); err == nil {
			s.forgetPayrollNames(p.TenantID)
			_ = s.tx(r, func(tx pgx.Tx) error {
				return auditTx(r, tx, "payroll.employee_change", "payrolla_employee:"+id, map[string]any{"fields": fields})
			})
			if ch.FirstName != nil || ch.LastName != nil {
				s.renameLinked(ctx, p.TenantID, e)
			}
			links, _ := s.payrollaLinks(ctx, p.TenantID)
			out := map[string]any{"employee": memberOf(e, seesPay(p), links)}
			if ch.Pay != nil {
				from := ch.Pay.EffectiveFrom
				if from == "" {
					from = time.Now().Format("2006-01-02")
				}
				out["words"] = "Their new pay starts " + payDateWords(from) + "."
			}
			writeJSON(w, http.StatusOK, out)
			return
		}
	}
	s.payFail(w, r, s.teamSays(ctx, p, conn, err, "employees"))
}

func payDateWords(d string) string {
	t, err := time.Parse("2006-01-02", d)
	if err != nil {
		return d
	}
	return t.Format("2 January 2006")
}

// renameLinked keeps the name Payrolla gave a linked staff member's
// employee (staff_profiles.payrolla_name) as Payrolla now has it.
func (s *Server) renameLinked(ctx context.Context, tenant uuid.UUID, e payrolla.Employee) {
	_ = s.tenantTx(ctx, tenant, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `UPDATE staff_profiles SET payrolla_name = $3 WHERE tenant_id = $1 AND payrolla_employee_id = $2`, tenant, e.ID, clipRunes(e.Name(), payrollaNameMax))
		return err
	})
}

// askPayrollBankChange is POST /v1/payroll/team/{employee}/bank {key,
// bank_code, account_number} (payroll.prepare): a new salary account,
// checked by the bank and held until an approver other than this person
// approves it. They are paid into their current account meanwhile.
func (s *Server) askPayrollBankChange(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Key string `json:"key"`
		bankIn
	}
	if err := decodeBody(r, &in); err != nil {
		s.fail(w, r, err)
		return
	}
	ctx := r.Context()
	p := principalFrom(ctx)
	id := chi.URLParam(r, "employee")
	if _, err := uuid.Parse(id); err != nil {
		s.fail(w, r, pgx.ErrNoRows)
		return
	}
	b, err := in.clean()
	if err != nil {
		s.payFail(w, r, err)
		return
	}
	cl, conn, err := s.payrollaClient(ctx, p, "payroll.team.bank")
	if err != nil && !errors.Is(err, errPayrollaNotConnected) {
		s.fail(w, r, err)
		return
	}
	if err == nil {
		var key string
		if key, err = payrollaKey(conn, "employee-bank:"+id, in.Key); err != nil {
			s.fail(w, r, err)
			return
		}
		var e payrolla.Employee
		if e, err = cl.ChangeEmployee(ctx, id, payrolla.EmployeeChange{Bank: &b}, payrolla.Write{Key: key, User: p.UserID, Name: s.actingName(ctx, p)}); err == nil {
			if e.PendingBank == nil {
				s.payFail(w, r, refusePay(http.StatusBadGateway, "payrolla_refused", "Payrolla didn't take that. Try again in a minute."))
				return
			}
			_ = s.tx(r, func(tx pgx.Tx) error {
				return auditTx(r, tx, "payroll.bank_change_ask", bankChangeTarget(e.PendingBank.ID), map[string]any{"employee_id": id})
			})
			links, _ := s.payrollaLinks(ctx, p.TenantID)
			out := map[string]any{"employee": memberOf(e, seesPay(p), links), "words": heldWords("Held until another approver approves it.", "They're", e.Bank)}
			writeJSON(w, http.StatusOK, out)
			return
		}
	}
	s.payFail(w, r, s.teamSays(ctx, p, conn, err, "employees"))
}

// bankChangeTarget is the audit's target for a held change.
func bankChangeTarget(id string) string { return "payrolla_bank_change:" + id }

// heldWords: "Held … They're paid into ••6789 until then."
func heldWords(first, who string, current *payrolla.MaskedBank) string {
	if current == nil || current.Last4 == "" {
		return first
	}
	return first + " " + who + " paid into ••" + current.Last4 + " until then."
}

// endPayrollEmployee is POST /v1/payroll/team/{employee}/end {key,
// confirm: "yes"} (payroll.prepare): their employment ends today, as
// Payrolla can't set a last day.
func (s *Server) endPayrollEmployee(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Key     string `json:"key"`
		Confirm string `json:"confirm"`
	}
	if err := decodeBody(r, &in); err != nil {
		s.fail(w, r, err)
		return
	}
	ctx := r.Context()
	p := principalFrom(ctx)
	id := chi.URLParam(r, "employee")
	if _, err := uuid.Parse(id); err != nil {
		s.fail(w, r, pgx.ErrNoRows)
		return
	}
	if !strings.EqualFold(strings.TrimSpace(in.Confirm), "yes") {
		s.payFail(w, r, refusePay(http.StatusBadRequest, "confirm", "This takes effect today. Payrolla can't set a last day. Say yes to end their employment."))
		return
	}
	cl, conn, err := s.payrollaClient(ctx, p, "payroll.team.end")
	if err != nil && !errors.Is(err, errPayrollaNotConnected) {
		s.fail(w, r, err)
		return
	}
	if err == nil {
		var key string
		if key, err = payrollaKey(conn, "employee-end:"+id, in.Key); err != nil {
			s.fail(w, r, err)
			return
		}
		var e payrolla.Employee
		if e, err = cl.EndEmployment(ctx, id, payrolla.Write{Key: key, User: p.UserID, Name: s.actingName(ctx, p)}); err == nil {
			s.forgetPayrollNames(p.TenantID)
			_ = s.tx(r, func(tx pgx.Tx) error {
				return auditTx(r, tx, "payroll.employee_end", "payrolla_employee:"+id, nil)
			})
			links, _ := s.payrollaLinks(ctx, p.TenantID)
			writeJSON(w, http.StatusOK, map[string]any{"employee": memberOf(e, seesPay(p), links), "words": e.Name() + "'s employment ended today."})
			return
		}
	}
	s.payFail(w, r, s.teamSays(ctx, p, conn, err, "employees"))
}

// --- account changes ---

// askedBy is who in Taskiem asked for each held change, by its id, from
// the audit: their user id and name.
func (s *Server) askedBy(ctx context.Context, tenant uuid.UUID, ids []string) (map[string]payrollStaffRef, error) {
	out := map[string]payrollStaffRef{}
	if len(ids) == 0 {
		return out, nil
	}
	targets := make([]string, len(ids))
	for i, id := range ids {
		targets[i] = bankChangeTarget(id)
	}
	err := s.tenantTx(ctx, tenant, func(tx pgx.Tx) error {
		return askedByTx(ctx, tx, tenant, targets, out)
	})
	return out, err
}

func askedByTx(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, targets []string, out map[string]payrollStaffRef) error {
	rows, err := tx.Query(ctx, `SELECT DISTINCT ON (a.target) a.target, u.id, u.name FROM audit_log a JOIN users u ON u.id::text = taskiem_actor_human(a.actor_id)
		WHERE a.tenant_id = $1 AND a.action IN ('payroll.bank_change_ask', 'payroll.my_bank_change') AND a.target = ANY ($2) ORDER BY a.target, a.chain_seq`, tenant, targets)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var target string
		var ref payrollStaffRef
		if err := rows.Scan(&target, &ref.UserID, &ref.Name); err != nil {
			return err
		}
		out[strings.TrimPrefix(target, "payrolla_bank_change:")] = ref
	}
	return rows.Err()
}

// getPayrollBankChanges is GET /v1/payroll/bank-changes: the account
// changes waiting for an approver, each with the employee, the bank's name
// for the new account and its last four digits, and who asked and how.
func (s *Server) getPayrollBankChanges(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	p := principalFrom(ctx)
	cl, conn, err := s.payrollaClient(ctx, p, "payroll.bank_changes")
	if err != nil && !errors.Is(err, errPayrollaNotConnected) {
		s.fail(w, r, err)
		return
	}
	var list []payrolla.BankChange
	var names map[string]string
	if err == nil {
		if list, err = cl.BankChanges(ctx); err == nil && len(list) > 0 {
			names, _, err = changeNames(ctx, cl, list)
		}
	}
	if err != nil {
		s.payFail(w, r, s.teamSays(ctx, p, conn, err, "employees"))
		return
	}
	ids := make([]string, 0, len(list))
	for _, c := range list {
		ids = append(ids, c.ID)
	}
	asked, err := s.askedBy(ctx, p.TenantID, ids)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	out := make([]teamChange, 0, len(list))
	for i := range list {
		c := changeOf(&list[i])
		c.Employee = names[c.EmployeeID]
		if a, ok := asked[c.ID]; ok {
			c.AskedBy, c.Mine = a.Name, a.UserID == p.UserID
		}
		out = append(out, *c)
	}
	writeJSON(w, http.StatusOK, map[string]any{"changes": out, "may": teamMay(p)})
}

// changeID is the {change} in the path, a uuid.
func changeID(r *http.Request) (string, bool) {
	id := chi.URLParam(r, "change")
	_, err := uuid.Parse(id)
	return id, err == nil
}

// startBankChangeCode is POST /v1/payroll/bank-changes/{change}/code {key}
// (payroll.approve): Payrolla sends the person's linked approver a code
// for approving one held change. Whoever asked is refused (SAME_PERSON),
// unless they are the company's only approver.
func (s *Server) startBankChangeCode(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Key string `json:"key"`
	}
	if err := decodeBody(r, &in); err != nil {
		s.fail(w, r, err)
		return
	}
	ctx := r.Context()
	p := principalFrom(ctx)
	id, ok := changeID(r)
	if !ok {
		s.fail(w, r, pgx.ErrNoRows)
		return
	}
	cl, conn, err := s.payrollaClient(ctx, p, "payroll.bank_change.code")
	if err != nil && !errors.Is(err, errPayrollaNotConnected) {
		s.fail(w, r, err)
		return
	}
	if err == nil {
		var key string
		if key, err = payrollaKey(conn, "bank-change-code:"+id, in.Key); err != nil {
			s.fail(w, r, err)
			return
		}
		var ch payrolla.Challenge
		if ch, err = cl.StartBankChangeApproval(ctx, id, payrolla.Write{Key: key, User: p.UserID, Name: s.actingName(ctx, p)}); err == nil {
			_ = s.tx(r, func(tx pgx.Tx) error {
				return auditTx(r, tx, "payroll.bank_change_code", bankChangeTarget(id), map[string]any{"channel": ch.Channel})
			})
			writeJSON(w, http.StatusAccepted, map[string]any{"challenge": ch, "sent_to": channelWords(ch), "safer": ch.Channel == "SMS"})
			return
		}
	}
	s.payFail(w, r, s.teamSays(ctx, p, conn, err, "approve"))
}

// typedCode is a code as typed: its digits, spaces and dashes dropped;
// ok is false for anything else.
func typedCode(code string) (string, bool) {
	c := strings.Map(func(r rune) rune {
		switch {
		case r >= '0' && r <= '9':
			return r
		case r == ' ' || r == '-':
			return -1
		}
		return 'x'
	}, code)
	return c, len(c) >= 4 && len(c) <= 10 && !strings.Contains(c, "x")
}

// approveBankChange is POST /v1/payroll/bank-changes/{change}/approve
// {challenge_id, code, key} (payroll.approve): the code goes to Payrolla
// once and is kept nowhere.
func (s *Server) approveBankChange(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Challenge string `json:"challenge_id"`
		Code      string `json:"code"`
		Key       string `json:"key"`
	}
	if err := decodeBody(r, &in); err != nil {
		s.fail(w, r, err)
		return
	}
	code, ok := typedCode(in.Code)
	if !ok {
		s.payFail(w, r, refusePay(http.StatusUnprocessableEntity, "code_invalid", "That code isn't right. Check it and type it again."))
		return
	}
	ctx := r.Context()
	p := principalFrom(ctx)
	id, ok := changeID(r)
	if !ok {
		s.fail(w, r, pgx.ErrNoRows)
		return
	}
	if _, err := uuid.Parse(in.Challenge); err != nil {
		s.payFail(w, r, refusePay(http.StatusGone, "code_expired", "That code has expired."))
		return
	}
	cl, conn, err := s.payrollaClient(ctx, p, "payroll.bank_change.approve")
	if err != nil && !errors.Is(err, errPayrollaNotConnected) {
		s.fail(w, r, err)
		return
	}
	if err == nil {
		var key string
		// Each attempt at a code gets its own key.
		if key, err = payrollaKey(conn, "bank-change-approve:"+id+":"+in.Challenge, in.Key); err != nil {
			s.fail(w, r, err)
			return
		}
		var c payrolla.BankChange
		if c, err = cl.ApproveBankChange(ctx, id, in.Challenge, code, payrolla.Write{Key: key, User: p.UserID, Name: s.actingName(ctx, p)}); err == nil {
			asked, _ := s.askedBy(ctx, p.TenantID, []string{id})
			own := asked[id].UserID == p.UserID
			_ = s.tx(r, func(tx pgx.Tx) error {
				return auditTx(r, tx, "payroll.bank_change_approve", bankChangeTarget(id), map[string]any{"confirmed_with_code": true, "own_request": own})
			})
			words := "Approved. Payrolla pays them into the new account from now on."
			if own {
				words = "Approved by the person who asked. Payrolla pays them into the new account from now on."
			}
			writeJSON(w, http.StatusOK, map[string]any{"change": changeOf(&c), "words": words})
			return
		}
	}
	s.payFail(w, r, s.teamSays(ctx, p, conn, err, "approve"))
}

// rejectBankChange is POST /v1/payroll/bank-changes/{change}/reject
// {note, key} (payroll.approve): no code, as it moves no money; the
// current account stays.
func (s *Server) rejectBankChange(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Note string `json:"note"`
		Key  string `json:"key"`
	}
	if err := decodeBody(r, &in); err != nil {
		s.fail(w, r, err)
		return
	}
	note := strings.TrimSpace(in.Note)
	if note == "" || utf8.RuneCountInString(note) > 300 {
		s.payFail(w, r, refusePay(http.StatusBadRequest, "note", "Say in a few words why (300 characters at most)."))
		return
	}
	ctx := r.Context()
	p := principalFrom(ctx)
	id, ok := changeID(r)
	if !ok {
		s.fail(w, r, pgx.ErrNoRows)
		return
	}
	cl, conn, err := s.payrollaClient(ctx, p, "payroll.bank_change.reject")
	if err != nil && !errors.Is(err, errPayrollaNotConnected) {
		s.fail(w, r, err)
		return
	}
	if err == nil {
		var key string
		if key, err = payrollaKey(conn, "bank-change-reject:"+id, in.Key); err != nil {
			s.fail(w, r, err)
			return
		}
		var c payrolla.BankChange
		if c, err = cl.RejectBankChange(ctx, id, note, payrolla.Write{Key: key, User: p.UserID, Name: s.actingName(ctx, p)}); err == nil {
			_ = s.tx(r, func(tx pgx.Tx) error {
				return auditTx(r, tx, "payroll.bank_change_reject", bankChangeTarget(id), nil)
			})
			writeJSON(w, http.StatusOK, map[string]any{"change": changeOf(&c), "words": "Rejected. They're still paid into their current account."})
			return
		}
	}
	s.payFail(w, r, s.teamSays(ctx, p, conn, err, "approve"))
}

// --- My pay ---

// myEmployee is the Payrolla employee the person is linked to, or "".
func (s *Server) myEmployee(ctx context.Context, p *Principal) (string, error) {
	var emp string
	err := s.tenantTx(ctx, p.TenantID, func(tx pgx.Tx) error {
		sp, err := staffPayrollaTx(ctx, tx, p.TenantID, p.UserID)
		if sp != nil {
			emp = sp.EmployeeID
		}
		return err
	})
	return emp, err
}

// getMyPay is GET /v1/payroll/me: a staff member's own record in Payrolla
// ({linked: false} when they are not linked to an employee): their
// account masked, a waiting change, and their phone as Payrolla has it.
// No pay: that is the payslip's.
func (s *Server) getMyPay(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	p := principalFrom(ctx)
	emp, err := s.myEmployee(ctx, p)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if emp == "" {
		writeJSON(w, http.StatusOK, map[string]any{"linked": false})
		return
	}
	cl, conn, err := s.payrollaClient(ctx, p, "payroll.me")
	if err != nil && !errors.Is(err, errPayrollaNotConnected) {
		s.fail(w, r, err)
		return
	}
	var e payrolla.Employee
	if err == nil {
		e, err = cl.Employee(ctx, emp)
	}
	if err != nil {
		if errors.Is(err, connector.ErrNotFound) {
			writeJSON(w, http.StatusOK, map[string]any{"linked": false})
			return
		}
		s.payFail(w, r, s.teamSays(ctx, p, conn, err, "self"))
		return
	}
	m := memberOf(e, false, nil)
	writeJSON(w, http.StatusOK, map[string]any{"linked": true, "me": map[string]any{"name": m.Name, "phone": m.Phone, "status": m.Status, "bank": m.Bank,
		"waiting_change": m.Waiting}})
}

// startMyBankChange is POST /v1/payroll/me/bank-change {key, bank_code,
// account_number}: Payrolla checks the account with the bank and texts
// the person a code naming it; the bank's name for it is shown before
// they confirm.
func (s *Server) startMyBankChange(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Key string `json:"key"`
		bankIn
	}
	if err := decodeBody(r, &in); err != nil {
		s.fail(w, r, err)
		return
	}
	ctx := r.Context()
	p := principalFrom(ctx)
	b, err := in.clean()
	if err != nil {
		s.payFail(w, r, err)
		return
	}
	emp, err := s.myEmployee(ctx, p)
	if err == nil && emp == "" {
		err = refusePay(http.StatusConflict, "not_linked_staff", "You're not linked to Payrolla in this business yet. Ask the owner.")
	}
	if err != nil {
		s.payFail(w, r, err)
		return
	}
	cl, conn, err := s.payrollaClient(ctx, p, "payroll.me.bank_change")
	if err != nil && !errors.Is(err, errPayrollaNotConnected) {
		s.fail(w, r, err)
		return
	}
	if err == nil {
		var key string
		if key, err = payrollaKey(conn, "my-bank-change:"+emp, in.Key); err != nil {
			s.fail(w, r, err)
			return
		}
		var ch payrolla.EmployeeChallenge
		if ch, err = cl.StartOwnBankChange(ctx, emp, b, payrolla.Write{Key: key, User: p.UserID, Name: s.actingName(ctx, p)}); err == nil {
			writeJSON(w, http.StatusAccepted, map[string]any{"challenge": ch.Challenge, "account_name": ch.AccountName, "last4": ch.Last4, "bank": bankName(b.BankCode),
				"sent_to": channelWords(ch.Challenge)})
			return
		}
	}
	s.payFail(w, r, s.teamSays(ctx, p, conn, err, "self"))
}

// confirmMyBankChange is POST /v1/payroll/me/bank-change/confirm
// {challenge_id, code, key}: the change is then held for the employer to
// approve; the code goes to Payrolla once and is kept nowhere.
func (s *Server) confirmMyBankChange(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Challenge string `json:"challenge_id"`
		Code      string `json:"code"`
		Key       string `json:"key"`
	}
	if err := decodeBody(r, &in); err != nil {
		s.fail(w, r, err)
		return
	}
	code, ok := typedCode(in.Code)
	if !ok {
		s.payFail(w, r, refusePay(http.StatusUnprocessableEntity, "code_invalid", "That code isn't right. Check it and type it again."))
		return
	}
	ctx := r.Context()
	p := principalFrom(ctx)
	if _, err := uuid.Parse(in.Challenge); err != nil {
		s.payFail(w, r, refusePay(http.StatusGone, "code_expired", "That code has expired."))
		return
	}
	emp, err := s.myEmployee(ctx, p)
	if err == nil && emp == "" {
		err = refusePay(http.StatusConflict, "not_linked_staff", "You're not linked to Payrolla in this business yet. Ask the owner.")
	}
	if err != nil {
		s.payFail(w, r, err)
		return
	}
	cl, conn, err := s.payrollaClient(ctx, p, "payroll.me.bank_change")
	if err != nil && !errors.Is(err, errPayrollaNotConnected) {
		s.fail(w, r, err)
		return
	}
	if err == nil {
		var key string
		if key, err = payrollaKey(conn, "my-bank-confirm:"+emp+":"+in.Challenge, in.Key); err != nil {
			s.fail(w, r, err)
			return
		}
		var c payrolla.BankChange
		if c, err = cl.ConfirmOwnBankChange(ctx, emp, in.Challenge, code, payrolla.Write{Key: key, User: p.UserID, Name: s.actingName(ctx, p)}); err == nil {
			_ = s.tx(r, func(tx pgx.Tx) error {
				return auditTx(r, tx, "payroll.my_bank_change", bankChangeTarget(c.ID), map[string]any{"employee_id": emp, "confirmed_with_code": true})
			})
			var current *payrolla.MaskedBank
			if e, err := cl.Employee(ctx, emp); err == nil {
				current = e.Bank
			}
			writeJSON(w, http.StatusOK, map[string]any{"change": changeOf(&c), "words": heldWords("Held for your employer to approve.", "You're", current)})
			return
		}
		// A code Payrolla has no record of for this employee.
		if payrolla.IsNotFound(err) {
			err = &payrolla.Error{Status: http.StatusNotFound, Code: "CHALLENGE_NOT_FOUND"}
		}
	}
	s.payFail(w, r, s.teamSays(ctx, p, conn, err, "self"))
}

// --- Payrolla's refusals, in plain words ---

// scopeWords are what an owner allows in Payrolla, in its own words.
var scopeWords = map[string]string{"employees": "Manage team", "approve": "Approve and pay", "self": "Employee self-service"}

// teamSays is payrollaSays with the team's own refusals. what is the
// kind of request (employees, approve or self) for which of Payrolla's
// permissions it needs and how many codes an hour it allows.
func (s *Server) teamSays(ctx context.Context, p *Principal, conn *payrollaConn, err error, what string) error {
	type word struct {
		status     int
		code, text string
	}
	words := map[string]word{
		"SAME_PERSON":                {http.StatusForbidden, "same_person", "You asked for this account change, so someone else must approve it."},
		"EXTERNAL_REF_TAKEN":         {http.StatusConflict, "already_in_payrolla", "This person is already in Payrolla."},
		"PHONE_CHANGE_NOT_AVAILABLE": {http.StatusUnprocessableEntity, "phone_change", "A phone number can't be changed from Taskiem. Change it in Payrolla."},
		"PHONE_TAKEN":                {http.StatusConflict, "phone_taken", "Someone in Payrolla already has that phone number."},
		"INVALID_BANK":               {http.StatusUnprocessableEntity, "invalid_bank", "The bank has no such account. Check the number and the bank."},
		"NO_CONTACT_DETAILS":         {http.StatusUnprocessableEntity, "contact", "Give a phone number or an email, for their payslip."},
		"NAME_REQUIRED":              {http.StatusUnprocessableEntity, "name", "Give their first and last names."},
		"EMPLOYEE_UNREACHABLE":       {http.StatusUnprocessableEntity, "unreachable", "Payrolla has no phone number for you, so it can't send you a code. Ask your employer to change your account."},
		"SAME_ACCOUNT":               {http.StatusConflict, "same_account", "You're already paid into that account."},
		"EMPLOYEE_NOT_ACTIVE":        {http.StatusConflict, "left", "This person has left, so their account can't be changed."},
		"INVALID_STATE":              {http.StatusConflict, "decided", "This account change was already decided."},
	}
	if what == "self" {
		words["TOO_MANY_CODES"] = word{http.StatusTooManyRequests, "too_many_codes", "Payrolla has sent you too many codes. Try again in 30 minutes."}
	}
	code := payrolla.Code(err)
	if wd, ok := words[code]; ok {
		return refusePay(wd.status, wd.code, wd.text)
	}
	if code == "SCOPE_REQUIRED" && isOwnerOf(p) {
		return refusePay(http.StatusConflict, "scope_required", "Payrolla hasn't allowed Taskiem to do this. Reconnect Payrolla and allow "+scopeWords[what]+".")
	}
	if errors.Is(err, connector.ErrNotFound) && code == "" || code == "NOT_FOUND" {
		return refusePay(http.StatusNotFound, "not_found", "Payrolla has no such person or account change. It may have been removed.")
	}
	return s.payrollaSays(ctx, p, conn, err)
}
