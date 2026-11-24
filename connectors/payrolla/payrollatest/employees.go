package payrollatest

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"
)

// Staff and pay (P4b-2): the fake's employees, their held bank changes
// and an employee's own account change, served on a Fake through Handle
// as Payrolla's Phase A, B and C replies (05 to 07) and its OpenAPI file
// describe them. Account numbers are checked against the accounts the
// test says the banks have (AddAccount) and never come back: reads carry
// the bank's code, its name for the account and the last four digits.

// Employee is an employee in the fake.
type Employee struct {
	ID, FirstName, LastName, Email, Phone string
	Department, CostCenter, StartDate     string
	Status                                string // ACTIVE, INACTIVE, TERMINATED
	ExternalRef                           string
	BankCode, AccountNumber, AccountName  string
	Pay                                   *Pay
}

// Pay is monthly pay in kobo, from a date.
type Pay struct {
	Gross, Basic, Housing, Transport int64
	Pensionable                      bool
	EffectiveFrom                    string
}

// BankChange is a held account change.
type BankChange struct {
	ID, Employee                         string
	BankCode, AccountNumber, AccountName string
	Status                               string // PENDING, APPROVED, REJECTED, REPLACED
	Via                                  string // PAYROLLA, CONNECT, EMPLOYEE
	// RequestedBy is the X-Acting-User that asked; DecidedBy the one that
	// decided; Note a rejection's note.
	RequestedBy, DecidedBy, Note string
	RequestedAt, DecidedAt       time.Time
}

type teamChallenge struct {
	challenge
	change   string // a bank change being approved, or "" (an employee's own)
	employee string
	bank     BankInput
}

// BankInput is an account a request named.
type BankInput struct {
	BankCode      string `json:"bank_code"`
	AccountNumber string `json:"account_number"`
}

// Team is a Fake's employees and bank changes. Its state is guarded by
// the Fake's lock.
type Team struct {
	f          *Fake
	employees  []*Employee
	changes    []*BankChange
	accounts   map[string]string // bank code + "/" + number -> the bank's name for it
	challenges map[string]*teamChallenge
	texted     map[string][]time.Time // codes texted to an employee
}

var teams sync.Map // *Fake -> *Team

// NewTeam serves employees and bank changes on f, once; later calls
// return the same Team.
func NewTeam(f *Fake) *Team {
	t := &Team{f: f, accounts: map[string]string{}, challenges: map[string]*teamChallenge{}, texted: map[string][]time.Time{}}
	if got, loaded := teams.LoadOrStore(f, t); loaded {
		return got.(*Team)
	}
	// One employee is read through the fake's one handler for it, which
	// also knows a run's workers (ServeRuns); registered first, as the
	// first matching prefix serves.
	f.Handle(http.MethodGet, "/connect/employees/", f.employeeRoute)
	for _, m := range []string{http.MethodGet, http.MethodPost, http.MethodPatch} {
		f.Handle(m, "/connect/employees", t.serve)
		f.Handle(m, "/connect/bank-changes", t.serve)
	}
	return t
}

// AddAccount says a bank has an account, and its name for it.
func (t *Team) AddAccount(bankCode, number, name string) {
	t.f.mu.Lock()
	defer t.f.mu.Unlock()
	t.accounts[bankCode+"/"+number] = name
}

// AddEmployee adds an employee as if made in Payrolla, and returns their
// id.
func (t *Team) AddEmployee(e Employee) string {
	t.f.mu.Lock()
	defer t.f.mu.Unlock()
	if e.ID == "" {
		e.ID = t.f.id()
	}
	if e.Status == "" {
		e.Status = "ACTIVE"
	}
	if e.StartDate == "" {
		e.StartDate = "2026-01-05"
	}
	if e.AccountNumber != "" && e.AccountName == "" {
		e.AccountName = t.accounts[e.BankCode+"/"+e.AccountNumber]
	}
	t.employees = append(t.employees, &e)
	return e.ID
}

// Employee is an employee as they are now.
func (t *Team) Employee(id string) Employee {
	t.f.mu.Lock()
	defer t.f.mu.Unlock()
	if e := t.employee(id); e != nil {
		return *e
	}
	return Employee{}
}

// BankChanges are every bank change, in the order asked.
func (t *Team) BankChanges() []BankChange {
	t.f.mu.Lock()
	defer t.f.mu.Unlock()
	out := make([]BankChange, 0, len(t.changes))
	for _, c := range t.changes {
		out = append(out, *c)
	}
	return out
}

// RequestInPayrolla holds a bank change asked for in Payrolla's own
// screens by a Payrolla user (requestedBy: their id), and returns its id.
func (t *Team) RequestInPayrolla(employee, bankCode, number, requestedBy string) string {
	t.f.mu.Lock()
	defer t.f.mu.Unlock()
	return t.hold(t.employee(employee), BankInput{bankCode, number}, "PAYROLLA", requestedBy).ID
}

// Code is the last code Payrolla sent for a bank change or an employee's
// own change: to the approver (their Payrolla user id) or texted to the
// employee (their employee id).
func (t *Team) Code(to string) string {
	t.f.mu.Lock()
	defer t.f.mu.Unlock()
	var last *teamChallenge
	for _, c := range t.challenges {
		if c.user == to && (last == nil || c.id > last.id) {
			last = c
		}
	}
	if last == nil {
		return ""
	}
	return last.code
}

// ExpireCodes makes every waiting code lapse.
func (t *Team) ExpireCodes() {
	t.f.mu.Lock()
	defer t.f.mu.Unlock()
	for _, c := range t.challenges {
		c.expires = t.f.Now().Add(-time.Second)
	}
}

// EventData is an employee event's data as Payrolla sends it: the
// employee (never their account or pay) and extra (changed_fields,
// change_id, requested_via).
func (t *Team) EventData(employee string, extra map[string]any) map[string]any {
	t.f.mu.Lock()
	defer t.f.mu.Unlock()
	d := map[string]any{}
	if e := t.employee(employee); e != nil {
		v := employeeView(e, false)
		delete(v, "bank")
		delete(v, "pending_bank_change")
		d["employee"] = v
	}
	for k, v := range extra {
		d[k] = v
	}
	return d
}

// EventBody is a signed delivery's body for an employee event.
func (t *Team) EventBody(typ, employee string, extra map[string]any) []byte {
	b, _ := json.Marshal(map[string]any{"id": "evt_" + typ, "type": typ, "created_at": t.f.Now().UTC().Format(time.RFC3339), "api_version": "2026-11-01",
		"data": t.EventData(employee, extra)})
	return b
}

func (t *Team) employee(id string) *Employee {
	for _, e := range t.employees {
		if e.ID == id {
			return e
		}
	}
	return nil
}

func (t *Team) change(id string) *BankChange {
	for _, c := range t.changes {
		if c.ID == id {
			return c
		}
	}
	return nil
}

func last4(s string) string {
	if len(s) < 4 {
		return s
	}
	return s[len(s)-4:]
}

func changeView(c *BankChange) map[string]any {
	v := map[string]any{"id": c.ID, "employee_id": c.Employee, "bank_code": c.BankCode, "account_name": c.AccountName, "last4": last4(c.AccountNumber),
		"status": c.Status, "requested_via": c.Via, "requested_at": c.RequestedAt.UTC().Format(time.RFC3339)}
	if !c.DecidedAt.IsZero() {
		v["decided_at"] = c.DecidedAt.UTC().Format(time.RFC3339)
	}
	return v
}

func employeeView(e *Employee, pay bool) map[string]any {
	v := map[string]any{"id": e.ID, "first_name": e.FirstName, "last_name": e.LastName, "email": e.Email, "phone": e.Phone, "department": e.Department,
		"status": e.Status, "employment_type": "SALARIED", "start_date": e.StartDate, "pay_method": "NONE"}
	if e.ExternalRef != "" {
		v["external_ref"] = e.ExternalRef
	}
	if e.AccountNumber != "" {
		v["pay_method"] = "BANK"
		v["bank"] = map[string]any{"bank_code": e.BankCode, "account_name": e.AccountName, "last4": last4(e.AccountNumber)}
	}
	if pay && e.Pay != nil {
		v["pay"] = map[string]any{"gross_kobo": e.Pay.Gross, "basic_kobo": e.Pay.Basic, "housing_kobo": e.Pay.Housing, "transport_kobo": e.Pay.Transport,
			"pensionable": e.Pay.Pensionable, "effective_from": e.Pay.EffectiveFrom + "T00:00:00Z"}
	}
	return v
}

// teamView adds the employee's waiting change.
func (t *Team) view(e *Employee, c *Conn) map[string]any {
	v := employeeView(e, has(c, "payroll:read"))
	for _, ch := range t.changes {
		if ch.Employee == e.ID && ch.Status == "PENDING" {
			v["pending_bank_change"] = changeView(ch)
		}
	}
	return v
}

func (t *Team) hold(e *Employee, b BankInput, via, by string) *BankChange {
	for _, old := range t.changes {
		if old.Employee == e.ID && old.Status == "PENDING" {
			old.Status = "REPLACED"
		}
	}
	c := &BankChange{ID: t.f.id(), Employee: e.ID, BankCode: b.BankCode, AccountNumber: b.AccountNumber, AccountName: t.accounts[b.BankCode+"/"+b.AccountNumber],
		Status: "PENDING", Via: via, RequestedBy: by, RequestedAt: t.f.Now()}
	t.changes = append(t.changes, c)
	return c
}

func strict(raw []byte, v any) (string, bool) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		if f, ok := strings.CutPrefix(err.Error(), "json: unknown field "); ok {
			return strings.Trim(f, `"`), false
		}
		return "", false
	}
	return "", true
}

type payInput struct {
	Gross         int64  `json:"gross_kobo"`
	Basic         int64  `json:"basic_kobo"`
	Housing       int64  `json:"housing_kobo"`
	Transport     int64  `json:"transport_kobo"`
	Pensionable   *bool  `json:"pensionable"`
	EffectiveFrom string `json:"effective_from"`
}

func (p *payInput) pay(today string) *Pay {
	out := &Pay{Gross: p.Gross, Basic: p.Basic, Housing: p.Housing, Transport: p.Transport, Pensionable: p.Pensionable == nil || *p.Pensionable, EffectiveFrom: p.EffectiveFrom}
	if out.EffectiveFrom == "" {
		out.EffectiveFrom = today
	}
	return out
}

func (t *Team) serve(w http.ResponseWriter, r *http.Request, c *Conn) {
	t.f.mu.Lock()
	defer t.f.mu.Unlock()
	raw := new(bytes.Buffer)
	_, _ = raw.ReadFrom(r.Body)
	path := strings.TrimPrefix(r.URL.Path, "/api/v1")
	parts := strings.Split(strings.Trim(path, "/"), "/") // connect, employees|bank-changes, {id}, …
	need := func(scope string) bool {
		if !has(c, scope) {
			writeErr(w, http.StatusForbidden, "SCOPE_REQUIRED", "this connection needs "+scope)
			return false
		}
		return true
	}
	if parts[1] == "bank-changes" {
		t.bankChanges(w, r, parts, raw.Bytes(), need)
		return
	}
	switch {
	case r.Method == http.MethodGet && len(parts) == 2:
		if !need("employees:read") {
			return
		}
		q := r.URL.Query()
		ref := q.Get("external_ref")
		if ref == "" {
			ref = q.Get("external_id")
		}
		list := []map[string]any{}
		for _, e := range t.employees {
			if ref != "" && e.ExternalRef != ref || q.Get("status") != "" && !slices.Contains(strings.Split(q.Get("status"), ","), e.Status) {
				continue
			}
			list = append(list, t.view(e, c))
		}
		writeData(w, http.StatusOK, map[string]any{"employees": list, "next_cursor": ""})
	case r.Method == http.MethodPost && len(parts) == 2:
		if need("employees:write") {
			t.add(w, raw.Bytes(), c)
		}
	case len(parts) >= 3:
		e := t.employee(parts[2])
		if e == nil {
			writeErr(w, http.StatusNotFound, "NOT_FOUND", "no such employee")
			return
		}
		switch {
		case r.Method == http.MethodGet && len(parts) == 3:
			if need("employees:read") {
				writeData(w, http.StatusOK, map[string]any{"employee": t.view(e, c)})
			}
		case r.Method == http.MethodPatch && len(parts) == 3:
			if need("employees:write") {
				t.patch(w, r, e, raw.Bytes(), c)
			}
		case r.Method == http.MethodPost && len(parts) == 4 && parts[3] == "terminate":
			if need("employees:write") {
				e.Status = "TERMINATED"
				writeData(w, http.StatusOK, map[string]any{"employee": t.view(e, c)})
			}
		case r.Method == http.MethodPost && len(parts) == 4 && parts[3] == "bank-change":
			if need("self_service") {
				t.startOwn(w, r, e, raw.Bytes())
			}
		case r.Method == http.MethodPost && len(parts) == 5 && parts[3] == "bank-change" && parts[4] == "confirm":
			if need("self_service") {
				t.confirmOwn(w, r, e, raw.Bytes())
			}
		default:
			writeErr(w, http.StatusNotFound, "NOT_FOUND", "no such route")
		}
	default:
		writeErr(w, http.StatusNotFound, "NOT_FOUND", "no such route")
	}
}

func (t *Team) add(w http.ResponseWriter, raw []byte, c *Conn) {
	var in struct {
		FirstName      string     `json:"first_name"`
		LastName       string     `json:"last_name"`
		Phone          string     `json:"phone"`
		Email          string     `json:"email"`
		EmploymentType string     `json:"employment_type"`
		StartDate      string     `json:"start_date"`
		Department     string     `json:"department"`
		CostCenter     string     `json:"cost_center"` //nolint:misspell // Payrolla's field
		ExternalRef    string     `json:"external_ref"`
		Pay            *payInput  `json:"pay"`
		Bank           *BankInput `json:"bank"`
	}
	if f, ok := strict(raw, &in); !ok {
		if f != "" {
			writeErr(w, http.StatusBadRequest, "UNKNOWN_FIELD", "unknown field "+f)
			return
		}
		writeErr(w, http.StatusBadRequest, "INVALID_BODY", "unreadable body")
		return
	}
	switch {
	case strings.TrimSpace(in.FirstName) == "" || strings.TrimSpace(in.LastName) == "":
		writeErr(w, http.StatusUnprocessableEntity, "NAME_REQUIRED", "first and last names are required")
		return
	case in.Phone == "" && in.Email == "":
		writeErr(w, http.StatusUnprocessableEntity, "NO_CONTACT_DETAILS", "a phone or an email is required")
		return
	case in.StartDate == "" || in.Pay != nil && in.Pay.Gross < 1:
		writeErr(w, http.StatusBadRequest, "INVALID_BODY", "start_date and a positive gross are required")
		return
	}
	for _, e := range t.employees {
		if in.ExternalRef != "" && e.ExternalRef == in.ExternalRef {
			writeErr(w, http.StatusConflict, "EXTERNAL_REF_TAKEN", "another employee has that external_ref")
			return
		}
		if in.Phone != "" && e.Phone == in.Phone {
			writeErr(w, http.StatusConflict, "PHONE_TAKEN", "another employee has that phone")
			return
		}
	}
	e := &Employee{ID: t.f.id(), FirstName: in.FirstName, LastName: in.LastName, Phone: in.Phone, Email: in.Email, StartDate: in.StartDate,
		Department: in.Department, CostCenter: in.CostCenter, ExternalRef: in.ExternalRef, Status: "ACTIVE"}
	if in.Bank != nil {
		name, ok := t.accounts[in.Bank.BankCode+"/"+in.Bank.AccountNumber]
		if !ok {
			writeErr(w, http.StatusUnprocessableEntity, "INVALID_BANK", "the bank has no such account")
			return
		}
		e.BankCode, e.AccountNumber, e.AccountName = in.Bank.BankCode, in.Bank.AccountNumber, name
	}
	if in.Pay != nil {
		e.Pay = in.Pay.pay(in.StartDate)
	}
	t.employees = append(t.employees, e)
	writeData(w, http.StatusCreated, map[string]any{"employee": t.view(e, c)})
}

func (t *Team) patch(w http.ResponseWriter, r *http.Request, e *Employee, raw []byte, c *Conn) {
	var in struct {
		FirstName   *string    `json:"first_name"`
		LastName    *string    `json:"last_name"`
		Email       *string    `json:"email"`
		Department  *string    `json:"department"`
		CostCenter  *string    `json:"cost_center"` //nolint:misspell // Payrolla's field
		ExternalRef *string    `json:"external_ref"`
		Pay         *payInput  `json:"pay"`
		Bank        *BankInput `json:"bank"`
	}
	if f, ok := strict(raw, &in); !ok {
		switch f {
		case "phone":
			writeErr(w, http.StatusUnprocessableEntity, "PHONE_CHANGE_NOT_AVAILABLE", "a phone number can't be changed through the API")
		case "":
			writeErr(w, http.StatusBadRequest, "INVALID_BODY", "unreadable body")
		default:
			writeErr(w, http.StatusBadRequest, "UNKNOWN_FIELD", "unknown field "+f)
		}
		return
	}
	if in.ExternalRef != nil {
		for _, x := range t.employees {
			if x != e && *in.ExternalRef != "" && x.ExternalRef == *in.ExternalRef {
				writeErr(w, http.StatusConflict, "EXTERNAL_REF_TAKEN", "another employee has that external_ref")
				return
			}
		}
	}
	if in.Bank != nil {
		if _, ok := t.accounts[in.Bank.BankCode+"/"+in.Bank.AccountNumber]; !ok {
			writeErr(w, http.StatusUnprocessableEntity, "INVALID_BANK", "the bank has no such account")
			return
		}
	}
	if in.Pay != nil && in.Pay.Gross < 1 {
		writeErr(w, http.StatusBadRequest, "INVALID_BODY", "a positive gross is required")
		return
	}
	set := func(dst *string, v *string) {
		if v != nil {
			*dst = *v
		}
	}
	set(&e.FirstName, in.FirstName)
	set(&e.LastName, in.LastName)
	set(&e.Email, in.Email)
	set(&e.Department, in.Department)
	set(&e.CostCenter, in.CostCenter)
	set(&e.ExternalRef, in.ExternalRef)
	if in.Pay != nil {
		e.Pay = in.Pay.pay(t.f.Now().UTC().Format("2006-01-02"))
	}
	if in.Bank != nil {
		t.hold(e, *in.Bank, "CONNECT", r.Header.Get("X-Acting-User"))
	}
	writeData(w, http.StatusOK, map[string]any{"employee": t.view(e, c)})
}

// Employees' own codes: 10 minutes, 5 tries, one use, at most 3 in 30
// minutes for an employee (Phase C).
const (
	ownTTL    = 10 * time.Minute
	ownLimit  = 3
	ownWindow = 30 * time.Minute
)

func (t *Team) startOwn(w http.ResponseWriter, r *http.Request, e *Employee, raw []byte) {
	var in BankInput
	if f, ok := strict(raw, &in); !ok {
		if f != "" {
			writeErr(w, http.StatusBadRequest, "UNKNOWN_FIELD", "unknown field "+f)
			return
		}
		writeErr(w, http.StatusBadRequest, "INVALID_BODY", "unreadable body")
		return
	}
	switch {
	case e.Status != "ACTIVE":
		writeErr(w, http.StatusConflict, "EMPLOYEE_NOT_ACTIVE", "the employee has left")
		return
	case e.Phone == "":
		writeErr(w, http.StatusUnprocessableEntity, "EMPLOYEE_UNREACHABLE", "no phone number on Payrolla")
		return
	}
	name, ok := t.accounts[in.BankCode+"/"+in.AccountNumber]
	switch {
	case !ok:
		writeErr(w, http.StatusUnprocessableEntity, "INVALID_BANK", "the bank has no such account")
		return
	case in.BankCode == e.BankCode && in.AccountNumber == e.AccountNumber:
		writeErr(w, http.StatusConflict, "SAME_ACCOUNT", "they're already paid into that account")
		return
	}
	now := t.f.Now()
	recent := slices.DeleteFunc(slices.Clone(t.texted[e.ID]), func(x time.Time) bool { return now.Sub(x) >= ownWindow })
	if len(recent) >= ownLimit {
		writeErr(w, http.StatusTooManyRequests, "TOO_MANY_CODES", "more than 3 codes in 30 minutes")
		return
	}
	t.texted[e.ID] = append(recent, now)
	t.f.n++
	ch := &teamChallenge{challenge: challenge{id: fmt.Sprintf("20000000-0000-4000-8000-%012d", t.f.n), user: e.ID, acting: r.Header.Get("X-Acting-User"),
		code: fmt.Sprintf("%06d", 731500+t.f.n), expires: now.Add(ownTTL)}, employee: e.ID, bank: in}
	t.challenges[ch.id] = ch
	writeData(w, http.StatusAccepted, map[string]any{"challenge": map[string]any{"challenge_id": ch.id, "channel": "SMS", "destination_hint": "•••• " + last4(e.Phone),
		"expires_at": ch.expires.UTC().Format(time.RFC3339), "account_name": name, "last4": last4(in.AccountNumber)}})
}

// checkCode answers a code for ch; ok when it is right.
func (t *Team) checkCode(w http.ResponseWriter, ch *teamChallenge, code string) bool {
	switch {
	case ch.used:
		writeErr(w, http.StatusForbidden, "CODE_USED", "that code was already used")
	case !t.f.Now().Before(ch.expires):
		writeErr(w, http.StatusForbidden, "CODE_EXPIRED", "that code has expired")
	case ch.tries >= codeTries:
		writeErr(w, http.StatusForbidden, "CODE_LOCKED", "too many attempts")
	case code != ch.code:
		ch.tries++
		if ch.tries >= codeTries {
			writeErr(w, http.StatusForbidden, "CODE_LOCKED", "too many attempts")
			return false
		}
		writeErr(w, http.StatusForbidden, "CODE_INVALID", "wrong code")
	default:
		ch.used = true
		return true
	}
	return false
}

type codeInput struct {
	Challenge string `json:"challenge_id"`
	Code      string `json:"code"`
}

func (t *Team) confirmOwn(w http.ResponseWriter, r *http.Request, e *Employee, raw []byte) {
	var in codeInput
	_ = json.Unmarshal(raw, &in)
	ch := t.challenges[in.Challenge]
	if ch == nil || ch.change != "" || ch.employee != e.ID {
		// Payrolla's Phase C reply calls this CHALLENGE_NOT_FOUND; its
		// OpenAPI file answers it as any 404 (NotFound).
		writeErr(w, http.StatusNotFound, "NOT_FOUND", "no such code for this employee from this connection")
		return
	}
	if !t.checkCode(w, ch, in.Code) {
		return
	}
	writeData(w, http.StatusOK, map[string]any{"bank_change": changeView(t.hold(e, ch.bank, "EMPLOYEE", r.Header.Get("X-Acting-User")))})
}

// approverOf is the approver the acting person is linked to.
func (t *Team) approverOf(acting string) *Approver {
	for _, a := range t.f.approvers {
		if a.LinkedAs != "" && a.LinkedAs == acting {
			return a
		}
	}
	return nil
}

func (t *Team) bankChanges(w http.ResponseWriter, r *http.Request, parts []string, raw []byte, need func(string) bool) {
	if r.Method == http.MethodGet && len(parts) == 2 {
		if !need("employees:read") {
			return
		}
		list := []map[string]any{}
		for _, c := range t.changes {
			if c.Status == "PENDING" {
				list = append(list, changeView(c))
			}
		}
		writeData(w, http.StatusOK, map[string]any{"bank_changes": list})
		return
	}
	if r.Method != http.MethodPost || len(parts) != 4 {
		writeErr(w, http.StatusNotFound, "NOT_FOUND", "no such route")
		return
	}
	if !need("payroll:approve") {
		return
	}
	bc := t.change(parts[2])
	if bc == nil {
		writeErr(w, http.StatusNotFound, "NOT_FOUND", "no such bank change")
		return
	}
	acting := r.Header.Get("X-Acting-User")
	a := t.approverOf(acting)
	switch {
	case a == nil:
		writeErr(w, http.StatusForbidden, "APPROVER_NOT_LINKED", "link the acting person first")
		return
	case !a.CanApprove:
		writeErr(w, http.StatusForbidden, "NOT_AN_APPROVER", "their Payrolla role doesn't allow it")
		return
	case bc.Status != "PENDING":
		writeErr(w, http.StatusConflict, "INVALID_STATE", "the change was decided")
		return
	}
	switch parts[3] {
	case "confirmations":
		approvers := 0
		for _, x := range t.f.approvers {
			if x.CanApprove {
				approvers++
			}
		}
		if (bc.RequestedBy == acting || bc.RequestedBy == a.UserID) && approvers > 1 {
			writeErr(w, http.StatusForbidden, "SAME_PERSON", "whoever asked for a bank change can't approve it")
			return
		}
		if !a.CanConfirm {
			writeErr(w, http.StatusForbidden, "PAYMENT_CONFIRMATION_NOT_SET_UP", "they have no factor to receive codes on")
			return
		}
		now := t.f.Now()
		recent := slices.DeleteFunc(slices.Clone(t.f.sent[acting]), func(x time.Time) bool { return now.Sub(x) >= codeWindow })
		if len(recent) >= codeLimit {
			writeErr(w, http.StatusTooManyRequests, "TOO_MANY_CODES", "more than 5 codes in 10 minutes")
			return
		}
		t.f.sent[acting] = append(recent, now)
		t.f.n++
		ch := &teamChallenge{challenge: challenge{id: fmt.Sprintf("30000000-0000-4000-8000-%012d", t.f.n), user: a.UserID, acting: acting,
			code: fmt.Sprintf("%06d", 615200+t.f.n), expires: now.Add(codeTTL)}, change: bc.ID}
		t.challenges[ch.id] = ch
		writeData(w, http.StatusAccepted, map[string]any{"challenge": map[string]any{"challenge_id": ch.id, "channel": a.Channel, "destination_hint": a.Hint,
			"expires_at": ch.expires.UTC().Format(time.RFC3339)}})
	case "approve":
		var in codeInput
		_ = json.Unmarshal(raw, &in)
		ch := t.challenges[in.Challenge]
		if ch == nil || ch.change != bc.ID || ch.acting != acting {
			writeErr(w, http.StatusForbidden, "CODE_INVALID", "no such code for this change")
			return
		}
		if !t.checkCode(w, ch, in.Code) {
			return
		}
		bc.Status, bc.DecidedBy, bc.DecidedAt = "APPROVED", acting, t.f.Now()
		if e := t.employee(bc.Employee); e != nil {
			e.BankCode, e.AccountNumber, e.AccountName = bc.BankCode, bc.AccountNumber, bc.AccountName
		}
		writeData(w, http.StatusOK, map[string]any{"bank_change": changeView(bc)})
	case "reject":
		var in struct {
			Note string `json:"note"`
		}
		_ = json.Unmarshal(raw, &in)
		bc.Status, bc.DecidedBy, bc.DecidedAt, bc.Note = "REJECTED", acting, t.f.Now(), in.Note
		writeData(w, http.StatusOK, map[string]any{"bank_change": changeView(bc)})
	default:
		writeErr(w, http.StatusNotFound, "NOT_FOUND", "no such route")
	}
}

// TeamRoutes are the routes a Team adds, as Routes lists the fake's own.
func TeamRoutes() []string {
	return []string{
		"GET /connect/employees", "POST /connect/employees", "GET /connect/employees/{employeeID}", "PATCH /connect/employees/{employeeID}",
		"POST /connect/employees/{employeeID}/terminate", "POST /connect/employees/{employeeID}/bank-change",
		"POST /connect/employees/{employeeID}/bank-change/confirm", "GET /connect/bank-changes",
		"POST /connect/bank-changes/{changeID}/confirmations", "POST /connect/bank-changes/{changeID}/approve", "POST /connect/bank-changes/{changeID}/reject",
	}
}

// TeamErrorCodes are the error codes a Team answers with.
func TeamErrorCodes() []string {
	return []string{"NOT_FOUND", "SCOPE_REQUIRED", "UNKNOWN_FIELD", "INVALID_BODY", "NAME_REQUIRED", "NO_CONTACT_DETAILS", "EXTERNAL_REF_TAKEN", "PHONE_TAKEN",
		"INVALID_BANK", "PHONE_CHANGE_NOT_AVAILABLE", "EMPLOYEE_NOT_ACTIVE", "EMPLOYEE_UNREACHABLE", "SAME_ACCOUNT", "TOO_MANY_CODES",
		"CODE_USED", "CODE_EXPIRED", "CODE_LOCKED", "CODE_INVALID", "APPROVER_NOT_LINKED", "NOT_AN_APPROVER", "INVALID_STATE", "SAME_PERSON",
		"PAYMENT_CONFIRMATION_NOT_SET_UP"}
}
