package payrolla

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"

	"github.com/google/uuid"

	"github.com/israel-duff/taskiem/engine/connector"
)

// Staff and pay from Taskiem (P4b-2; Payrolla's Phase A, B and C replies,
// payrolla-reference/05 to 07): employees added, changed and ended, a
// changed salary account held for a second approver and decided with the
// approver's code, and an employee's own account change confirmed with a
// code Payrolla texts them. Bank account numbers go to Payrolla and never
// come back: reads carry the bank's code, its name for the account and
// the last four digits.

// MaskedBank is an account as Payrolla shows it.
type MaskedBank struct {
	BankCode    string `json:"bank_code"`
	AccountName string `json:"account_name"`
	Last4       string `json:"last4"`
}

// PayView is someone's current monthly pay (only to connections with
// payroll:read), in kobo.
type PayView struct {
	GrossKobo     int64  `json:"gross_kobo"`
	BasicKobo     int64  `json:"basic_kobo"`
	HousingKobo   int64  `json:"housing_kobo"`
	TransportKobo int64  `json:"transport_kobo"`
	Pensionable   bool   `json:"pensionable"`
	EffectiveFrom string `json:"effective_from"`
}

// BankChange is a new salary account held until an approver other than
// whoever asked approves it.
type BankChange struct {
	ID          string `json:"id"`
	EmployeeID  string `json:"employee_id"`
	BankCode    string `json:"bank_code"`
	AccountName string `json:"account_name"`
	Last4       string `json:"last4"`
	// Status is PENDING, APPROVED, REJECTED or REPLACED.
	Status string `json:"status"`
	// RequestedVia is PAYROLLA (in Payrolla), CONNECT (a connected app:
	// Taskiem) or EMPLOYEE (the employee, with their own code).
	RequestedVia string `json:"requested_via"`
	RequestedAt  string `json:"requested_at"`
	DecidedAt    string `json:"decided_at,omitempty"`
}

// Employee is an employee as the API reads them (EmployeeRecord).
type Employee struct {
	ID             string      `json:"id"`
	FirstName      string      `json:"first_name"`
	LastName       string      `json:"last_name"`
	Email          string      `json:"email"`
	Phone          string      `json:"phone"`
	Department     string      `json:"department"`
	Status         string      `json:"status"` // ACTIVE, INACTIVE, TERMINATED
	EmploymentType string      `json:"employment_type"`
	StartDate      string      `json:"start_date"`
	PayMethod      string      `json:"pay_method"`
	ExternalRef    string      `json:"external_ref,omitempty"`
	Bank           *MaskedBank `json:"bank,omitempty"`
	Pay            *PayView    `json:"pay,omitempty"`
	PendingBank    *BankChange `json:"pending_bank_change,omitempty"`
}

// Name is the employee's name as Payrolla has it.
func (e Employee) Name() string { return strings.TrimSpace(e.FirstName + " " + e.LastName) }

// StaffRef is the external_ref of an employee Taskiem adds from one of
// its staff: the person's own ActingUser (payrolla-reference/08, section
// 5), so the staff link is made at once and found again.
func StaffRef(user uuid.UUID) string { return ActingUser(user) }

// RefUser is the Taskiem person an external_ref names, or Nil.
func RefUser(ref string) uuid.UUID {
	id, err := uuid.Parse(strings.TrimPrefix(ref, "tsk_user_"))
	if err != nil || !strings.HasPrefix(ref, "tsk_user_") {
		return uuid.Nil
	}
	return id
}

// PayInput is monthly pay, in kobo; EffectiveFrom (YYYY-MM-DD) on a
// change, today when empty. Pensionable is sent only when set.
type PayInput struct {
	GrossKobo     int64  `json:"gross_kobo"`
	BasicKobo     int64  `json:"basic_kobo,omitempty"`
	HousingKobo   int64  `json:"housing_kobo,omitempty"`
	TransportKobo int64  `json:"transport_kobo,omitempty"`
	Pensionable   *bool  `json:"pensionable,omitempty"`
	EffectiveFrom string `json:"effective_from,omitempty"`
}

// BankInput is an account to be checked with the bank. It is sent to
// Payrolla and kept nowhere in Taskiem.
type BankInput struct {
	BankCode      string `json:"bank_code"`
	AccountNumber string `json:"account_number"`
}

// NewEmployee is someone to add (EmployeeInput). A phone or an email is
// needed, for their payslip.
type NewEmployee struct {
	FirstName   string     `json:"first_name"`
	LastName    string     `json:"last_name"`
	Phone       string     `json:"phone,omitempty"`
	Email       string     `json:"email,omitempty"`
	StartDate   string     `json:"start_date"`
	Department  string     `json:"department,omitempty"`
	CostCenter  string     `json:"cost_center,omitempty"` //nolint:misspell // Payrolla's field
	ExternalRef string     `json:"external_ref,omitempty"`
	Pay         *PayInput  `json:"pay,omitempty"`
	Bank        *BankInput `json:"bank,omitempty"`
}

// EmployeeChange is what changes (EmployeePatch): only the fields set are
// sent. There is no phone (PHONE_CHANGE_NOT_AVAILABLE) and no job title
// (Payrolla keeps none). A Bank is held for an approver.
type EmployeeChange struct {
	FirstName   *string    `json:"first_name,omitempty"`
	LastName    *string    `json:"last_name,omitempty"`
	Email       *string    `json:"email,omitempty"`
	Department  *string    `json:"department,omitempty"`
	CostCenter  *string    `json:"cost_center,omitempty"` //nolint:misspell // Payrolla's field
	ExternalRef *string    `json:"external_ref,omitempty"`
	Pay         *PayInput  `json:"pay,omitempty"`
	Bank        *BankInput `json:"bank,omitempty"`
}

// Employees are the company's employees, all pages up to max (status:
// "ACTIVE,INACTIVE", or "" for all).
func (cl *Client) Employees(ctx context.Context, status string, max int) ([]Employee, error) {
	var all []Employee
	cursor := ""
	for len(all) < max {
		q := url.Values{"limit": {"100"}}
		if status != "" {
			q.Set("status", status)
		}
		if cursor != "" {
			q.Set("cursor", cursor)
		}
		var out struct {
			Employees  []Employee `json:"employees"`
			NextCursor string     `json:"next_cursor"`
		}
		if err := cl.c.do(ctx, cl.req, http.MethodGet, "/connect/employees?"+q.Encode(), nil, nil, &out); err != nil {
			return nil, err
		}
		all = append(all, out.Employees...)
		if cursor = out.NextCursor; cursor == "" {
			break
		}
	}
	if len(all) > max {
		all = all[:max]
	}
	return all, nil
}

// EmployeeByRef is the employee with this external_ref, if any.
func (cl *Client) EmployeeByRef(ctx context.Context, ref string) (*Employee, error) {
	var out struct {
		Employees []Employee `json:"employees"`
	}
	if err := cl.c.do(ctx, cl.req, http.MethodGet, "/connect/employees?"+url.Values{"external_ref": {ref}}.Encode(), nil, nil, &out); err != nil {
		return nil, err
	}
	for _, e := range out.Employees {
		if e.ExternalRef == ref {
			return &e, nil
		}
	}
	return nil, nil
}

func (cl *Client) employee(ctx context.Context, method, path string, w *Write, body any) (Employee, error) {
	var out struct {
		Employee Employee `json:"employee"`
	}
	var err error
	if w == nil {
		err = cl.c.do(ctx, cl.req, method, path, nil, nil, &out)
	} else {
		err = cl.write(ctx, method, path, *w, body, &out)
	}
	return out.Employee, err
}

// Employee is one employee.
func (cl *Client) Employee(ctx context.Context, id string) (Employee, error) {
	return cl.employee(ctx, http.MethodGet, "/connect/employees/"+url.PathEscape(id), nil, nil)
}

// AddEmployee adds someone; their account is checked with the bank first
// and applies at once (a new employee's first account is not held).
func (cl *Client) AddEmployee(ctx context.Context, in NewEmployee, w Write) (Employee, error) {
	return cl.employee(ctx, http.MethodPost, "/connect/employees", &w, in)
}

// ChangeEmployee changes what is set in ch. A new bank account comes back
// as PendingBank, held for an approver.
func (cl *Client) ChangeEmployee(ctx context.Context, id string, ch EmployeeChange, w Write) (Employee, error) {
	return cl.employee(ctx, http.MethodPatch, "/connect/employees/"+url.PathEscape(id), &w, ch)
}

// EndEmployment ends someone's employment now: Payrolla can't set a last
// day.
func (cl *Client) EndEmployment(ctx context.Context, id string, w Write) (Employee, error) {
	return cl.employee(ctx, http.MethodPost, "/connect/employees/"+url.PathEscape(id)+"/terminate", &w, map[string]any{})
}

// BankChanges are the account changes waiting for an approver.
func (cl *Client) BankChanges(ctx context.Context) ([]BankChange, error) {
	var out struct {
		BankChanges []BankChange `json:"bank_changes"`
	}
	if err := cl.c.do(ctx, cl.req, http.MethodGet, "/connect/bank-changes", nil, nil, &out); err != nil {
		return nil, err
	}
	return out.BankChanges, nil
}

func (cl *Client) bankChange(ctx context.Context, path string, w Write, body any) (BankChange, error) {
	var out struct {
		BankChange BankChange `json:"bank_change"`
	}
	err := cl.write(ctx, http.MethodPost, path, w, body, &out)
	return out.BankChange, err
}

// StartBankChangeApproval asks Payrolla to send the acting person's linked
// approver a code for one held change. Whoever asked for it is refused
// (SAME_PERSON) unless they are the company's only approver.
func (cl *Client) StartBankChangeApproval(ctx context.Context, change string, w Write) (Challenge, error) {
	var out struct {
		Challenge Challenge `json:"challenge"`
	}
	err := cl.write(ctx, http.MethodPost, "/connect/bank-changes/"+url.PathEscape(change)+"/confirmations", w, map[string]any{}, &out)
	return out.Challenge, err
}

// ApproveBankChange approves a held change with the approver's code, sent
// once and kept nowhere.
func (cl *Client) ApproveBankChange(ctx context.Context, change, challenge, code string, w Write) (BankChange, error) {
	return cl.bankChange(ctx, "/connect/bank-changes/"+url.PathEscape(change)+"/approve", w, map[string]any{"challenge_id": challenge, "code": code})
}

// RejectBankChange rejects a held change with a short note; no code.
func (cl *Client) RejectBankChange(ctx context.Context, change, note string, w Write) (BankChange, error) {
	return cl.bankChange(ctx, "/connect/bank-changes/"+url.PathEscape(change)+"/reject", w, map[string]any{"note": note})
}

// EmployeeChallenge is a code Payrolla texted an employee for their own
// account change, with the bank's name for the new account to show them
// before they confirm.
type EmployeeChallenge struct {
	Challenge
	AccountName string `json:"account_name"`
	Last4       string `json:"last4"`
}

// StartOwnBankChange is an employee asking to be paid into a new account
// (self_service): Payrolla checks it with the bank and texts them a code.
// w's person is the employee.
func (cl *Client) StartOwnBankChange(ctx context.Context, employee string, bank BankInput, w Write) (EmployeeChallenge, error) {
	var out struct {
		Challenge EmployeeChallenge `json:"challenge"`
	}
	err := cl.write(ctx, http.MethodPost, "/connect/employees/"+url.PathEscape(employee)+"/bank-change", w, bank, &out)
	return out.Challenge, err
}

// ConfirmOwnBankChange confirms it with the code they were texted; the
// change is then held for an approver like any other.
func (cl *Client) ConfirmOwnBankChange(ctx context.Context, employee, challenge, code string, w Write) (BankChange, error) {
	return cl.bankChange(ctx, "/connect/employees/"+url.PathEscape(employee)+"/bank-change/confirm", w, map[string]any{"challenge_id": challenge, "code": code})
}

// EmployeeEvent is an employee event's data: employee.created, .updated
// (ChangedFields, without values), .terminated, and the bank change
// events (ChangeID; RequestedVia payrolla, connect or employee).
type EmployeeEvent struct {
	Type          string
	Employee      Employee
	ChangedFields []string
	ChangeID      string
	RequestedVia  string
}

// ReadEmployeeEvent reads an employee event; ok is false for any other.
func ReadEmployeeEvent(body []byte) (EmployeeEvent, bool) {
	var e Event
	if json.Unmarshal(body, &e) != nil || !strings.HasPrefix(e.Type, "employee.") {
		return EmployeeEvent{}, false
	}
	var d struct {
		Employee      Employee `json:"employee"`
		ChangedFields []string `json:"changed_fields"`
		ChangeID      string   `json:"change_id"`
		RequestedVia  string   `json:"requested_via"`
	}
	if json.Unmarshal(e.Data, &d) != nil {
		return EmployeeEvent{}, false
	}
	return EmployeeEvent{Type: e.Type, Employee: d.Employee, ChangedFields: d.ChangedFields, ChangeID: d.ChangeID, RequestedVia: strings.ToLower(d.RequestedVia)}, true
}

// IsNotFound says whether Payrolla has no such thing (a 404 without an
// explained code).
func IsNotFound(err error) bool { return errors.Is(err, connector.ErrNotFound) }
