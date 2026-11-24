package payrolla

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
)

// This month's run from Taskiem (P4b-3 of docs/spec/chat-pay-people.md;
// Payrolla's Phase A and B replies, 05 and 06): preparing a run, each
// line's breakdown, bonuses and deductions, leaving someone out and
// putting them back, recalculating and cancelling (payroll:write); the
// funding wallet (payroll:read); and approving, rejecting and locking with
// the linked person's code (payroll:approve). Locking schedules the
// payment: Payrolla pays everyone on the pay date.

// Run is a run with its timeline (Payrolla's RunDetail).
type Run struct {
	ID             string `json:"run_id"`
	PeriodStart    string `json:"period_start"`
	PeriodEnd      string `json:"period_end"`
	PayDate        string `json:"pay_date"`
	RunType        string `json:"run_type"`
	Status         string `json:"status"`
	EmployeeCount  int64  `json:"employee_count"`
	TotalGrossKobo int64  `json:"total_gross_kobo"`
	TotalNetKobo   int64  `json:"total_net_kobo"`
	CreatedAt      string `json:"created_at,omitempty"`
	UpdatedAt      string `json:"updated_at,omitempty"`
	ApprovedAt     string `json:"approved_at,omitempty"`
	LockedAt       string `json:"locked_at,omitempty"`
}

// LineItem is one earning, deduction or employer contribution of a line.
type LineItem struct {
	Category   string `json:"category"`
	Code       string `json:"code"`
	Label      string `json:"label"`
	AmountKobo int64  `json:"amount_kobo"`
}

// Line is one employee's pay in a run, with its breakdown.
type Line struct {
	PayslipID          string     `json:"payslip_id"`
	EmployeeID         string     `json:"employee_id"`
	FirstName          string     `json:"first_name"`
	LastName           string     `json:"last_name"`
	NetKobo            int64      `json:"net_kobo"`
	DisbursementStatus string     `json:"disbursement_status"`
	Reference          string     `json:"reference,omitempty"`
	Reason             string     `json:"reason,omitempty"`
	GrossKobo          int64      `json:"gross_kobo"`
	EarningsKobo       int64      `json:"total_earnings_kobo"`
	DeductionsKobo     int64      `json:"total_deductions_kobo"`
	Items              []LineItem `json:"items"`
}

// Adjustment is a bonus or a deduction in a run (gross: tax and pension
// apply as usual).
type Adjustment struct {
	ID         string `json:"id"`
	EmployeeID string `json:"employee_id"`
	Type       string `json:"type"` // BONUS or DEDUCTION
	AmountKobo int64  `json:"amount_kobo"`
	Reason     string `json:"reason"`
	CreatedAt  string `json:"created_at"`
}

// Exclusion is someone left out of this run only.
type Exclusion struct {
	ID         string `json:"id"`
	EmployeeID string `json:"employee_id"`
	Reason     string `json:"reason"`
	CreatedAt  string `json:"created_at"`
}

// ComputeError is why Payrolla could not work a run out (the change was
// kept; fix the cause in Payrolla and recalculate).
type ComputeError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// Changed is a run as a change left it: the run, its lines, and why it
// could not be worked out, if it could not.
type Changed struct {
	Run          Run           `json:"run"`
	Lines        []Line        `json:"lines"`
	Adjustment   *Adjustment   `json:"adjustment,omitempty"`
	Exclusion    *Exclusion    `json:"exclusion,omitempty"`
	ComputeError *ComputeError `json:"compute_error,omitempty"`
}

// Payment is what is happening to a run's payment.
type Payment struct {
	Status       string `json:"status"`
	ScheduledFor string `json:"scheduled_for,omitempty"`
	TotalKobo    int64  `json:"total_kobo"`
	Paid         int64  `json:"paid"`
	Failed       int64  `json:"failed"`
	Returned     int64  `json:"returned"`
	Pending      int64  `json:"pending"`
}

// Decided is a run after a decision, and its payment.
type Decided struct {
	Run     Run     `json:"run"`
	Payment Payment `json:"payment"`
}

// Funding is the wallet Payrolla pays salaries from.
type Funding struct {
	HasWallet     bool   `json:"has_wallet"`
	AvailableKobo int64  `json:"available_kobo"`
	Currency      string `json:"currency"`
}

// Period is what a new run covers: a month ("2026-11") for a company paid
// monthly, or its first and last day; the pay date is the period's last
// day unless given.
type Period struct {
	Period      string `json:"period,omitempty"`
	PeriodStart string `json:"period_start,omitempty"`
	PeriodEnd   string `json:"period_end,omitempty"`
	PayDate     string `json:"pay_date,omitempty"`
}

func runPath(run string, rest ...string) string {
	p := "/connect/runs/" + url.PathEscape(run)
	for _, r := range rest {
		p += "/" + url.PathEscape(r)
	}
	return p
}

// CreateRun prepares a run and works it out at once. A run Payrolla could
// not work out is still made (DRAFT), with its ComputeError;
// DUPLICATE_RUN when the period has one.
func (cl *Client) CreateRun(ctx context.Context, p Period, w Write) (Changed, error) {
	var out Changed
	err := cl.write(ctx, http.MethodPost, "/connect/runs", w, p, &out)
	return out, err
}

// Runs are the runs, newest first (one page).
func (cl *Client) Runs(ctx context.Context, status string, limit int) ([]Run, error) {
	q := url.Values{}
	if status != "" {
		q.Set("status", status)
	}
	if limit > 0 {
		q.Set("limit", itoa(limit))
	}
	path := "/connect/runs"
	if len(q) > 0 {
		path += "?" + q.Encode()
	}
	var out struct {
		Runs []Run `json:"runs"`
	}
	err := cl.c.do(ctx, cl.req, http.MethodGet, path, nil, nil, &out)
	return out.Runs, err
}

func itoa(n int) string {
	b, _ := json.Marshal(n)
	return string(b)
}

// Run is one run.
func (cl *Client) Run(ctx context.Context, run string) (Run, error) {
	var out struct {
		Run Run `json:"run"`
	}
	err := cl.c.do(ctx, cl.req, http.MethodGet, runPath(run), nil, nil, &out)
	return out.Run, err
}

// Lines are each employee's pay in a run, with the breakdown.
func (cl *Client) Lines(ctx context.Context, run string) ([]Line, error) {
	var out struct {
		Lines []Line `json:"lines"`
	}
	err := cl.c.do(ctx, cl.req, http.MethodGet, runPath(run, "lines"), nil, nil, &out)
	return out.Lines, err
}

// Adjustments are a run's bonuses and deductions.
func (cl *Client) Adjustments(ctx context.Context, run string) ([]Adjustment, error) {
	var out struct {
		Adjustments []Adjustment `json:"adjustments"`
	}
	err := cl.c.do(ctx, cl.req, http.MethodGet, runPath(run, "adjustments"), nil, nil, &out)
	return out.Adjustments, err
}

// Exclusions are who is left out of a run, and why.
func (cl *Client) Exclusions(ctx context.Context, run string) ([]Exclusion, error) {
	var out struct {
		Exclusions []Exclusion `json:"exclusions"`
	}
	err := cl.c.do(ctx, cl.req, http.MethodGet, runPath(run, "exclusions"), nil, nil, &out)
	return out.Exclusions, err
}

// RunEmployeeName is an employee's name as Payrolla has it (someone left
// out of a run is in none of its lines).
func (cl *Client) RunEmployeeName(ctx context.Context, employee string) (string, error) {
	var out struct {
		Employee struct {
			FirstName string `json:"first_name"`
			LastName  string `json:"last_name"`
		} `json:"employee"`
	}
	err := cl.c.do(ctx, cl.req, http.MethodGet, "/connect/employees/"+url.PathEscape(employee), nil, nil, &out)
	return strings.TrimSpace(out.Employee.FirstName + " " + out.Employee.LastName), err
}

// Funding is the wallet and what it holds.
func (cl *Client) Funding(ctx context.Context) (Funding, error) {
	var out struct {
		Funding Funding `json:"funding"`
	}
	err := cl.c.do(ctx, cl.req, http.MethodGet, "/connect/funding", nil, nil, &out)
	return out.Funding, err
}

// AddAdjustment adds a bonus or a deduction (typ BONUS or DEDUCTION) for
// one employee, and works the run out again.
func (cl *Client) AddAdjustment(ctx context.Context, run, employee, typ string, kobo int64, reason string, w Write) (Changed, error) {
	var out Changed
	err := cl.write(ctx, http.MethodPost, runPath(run, "lines", employee, "adjustments"), w,
		map[string]any{"type": typ, "amount_kobo": kobo, "reason": reason}, &out)
	return out, err
}

// RemoveAdjustment removes a bonus or a deduction.
func (cl *Client) RemoveAdjustment(ctx context.Context, run, adjustment string, w Write) (Changed, error) {
	var out Changed
	err := cl.write(ctx, http.MethodDelete, runPath(run, "adjustments", adjustment), w, nil, &out)
	return out, err
}

// Exclude leaves an employee out of this run only. ALREADY_EXCLUDED is
// taken as done: the run as it is.
func (cl *Client) Exclude(ctx context.Context, run, employee, reason string, w Write) (Changed, error) {
	var out Changed
	err := cl.write(ctx, http.MethodPost, runPath(run, "lines", employee, "exclude"), w, map[string]any{"reason": reason}, &out)
	if Code(err) == "ALREADY_EXCLUDED" {
		return cl.changedNow(ctx, run)
	}
	return out, err
}

func (cl *Client) changedNow(ctx context.Context, run string) (Changed, error) {
	r, err := cl.Run(ctx, run)
	if err != nil {
		return Changed{}, err
	}
	lines, err := cl.Lines(ctx, run)
	return Changed{Run: r, Lines: lines}, err
}

// PutBack puts an employee who was left out back in the run.
func (cl *Client) PutBack(ctx context.Context, run, exclusion string, w Write) (Changed, error) {
	var out Changed
	err := cl.write(ctx, http.MethodDelete, runPath(run, "exclusions", exclusion), w, nil, &out)
	return out, err
}

// Compute works a run out again (one not approved yet).
func (cl *Client) Compute(ctx context.Context, run string, w Write) (Run, error) {
	var out struct {
		Run Run `json:"run"`
	}
	err := cl.write(ctx, http.MethodPost, runPath(run, "compute"), w, map[string]any{}, &out)
	return out.Run, err
}

// Cancel cancels a run before it is approved.
func (cl *Client) Cancel(ctx context.Context, run string, w Write) (Run, error) {
	var out struct {
		Run Run `json:"run"`
	}
	err := cl.write(ctx, http.MethodPost, runPath(run, "cancel"), w, map[string]any{}, &out)
	return out.Run, err
}

// Run actions confirmed with a code.
const (
	ActionApprove = "approve"
	ActionLock    = "lock"
)

// StartRunConfirmation asks Payrolla to send the acting (linked) person a
// code for one action on a run. Payrolla checks first that it may be done
// (the run's state, the person's role, separation of duties).
func (cl *Client) StartRunConfirmation(ctx context.Context, run, action string, w Write) (Challenge, error) {
	var out struct {
		Challenge Challenge `json:"challenge"`
	}
	err := cl.write(ctx, http.MethodPost, runPath(run, "confirmations"), w, map[string]any{"action": action}, &out)
	return out.Challenge, err
}

// Decide approves or locks a run, pays it now or sends its failed
// payments again (payment.go), with the code the person typed. The code
// is sent once and kept nowhere. Locking schedules the payment.
func (cl *Client) Decide(ctx context.Context, run, action, challenge, code string, w Write) (Decided, error) {
	route := decidePath(action)
	if route == "" {
		return Decided{}, errors.New("payrolla: approve, lock, pay_now or retry_failed")
	}
	var out Decided
	err := cl.write(ctx, http.MethodPost, runPath(run, route), w, map[string]any{"challenge_id": challenge, "code": code}, &out)
	return out, err
}

// Reject sends a run back, with a note; no code (it moves no money).
func (cl *Client) Reject(ctx context.Context, run, note string, w Write) (Decided, error) {
	var out Decided
	err := cl.write(ctx, http.MethodPost, runPath(run, "reject"), w, map[string]any{"note": note}, &out)
	return out, err
}

// ErrorDetails is the details Payrolla gave with a refusal (for
// NO_PAYOUT_DESTINATION, the employees with no account), or nil.
func ErrorDetails(err error) json.RawMessage {
	var pe *Error
	if errors.As(err, &pe) {
		return pe.Details
	}
	return nil
}

// RunEvent is a run's event as Taskiem's own subscription hears it: what
// happened, to which run, and what Payrolla said with it.
type RunEvent struct {
	ID   string
	Type string
	Run  Run
	// ScheduledFor is when Payrolla pays (run.payment_scheduled).
	ScheduledFor string
	// Note and By are a rejection's note and who rejected it, or who
	// approved.
	Note, By string
	// Action, Via and ByName are approval.recorded's: approved, rejected
	// or locked; in Payrolla or in a connected app; and who.
	Action, Via string
	// Subject is approval.recorded's subject (run or bank_change).
	Subject string
}

// ReadRunEvent reads a run's event (run.*, approval.recorded about a
// run); ok is false for any other.
func ReadRunEvent(body []byte) (RunEvent, bool) {
	var e Event
	if json.Unmarshal(body, &e) != nil {
		return RunEvent{}, false
	}
	var d struct {
		Run          Run    `json:"run"`
		ScheduledFor string `json:"scheduled_for"`
		Note         string `json:"note"`
		RejectedBy   string `json:"rejected_by"`
		ApprovedBy   string `json:"approved_by"`
		Subject      string `json:"subject"`
		SubjectID    string `json:"subject_id"`
		Action       string `json:"action"`
		Via          string `json:"via"`
		By           struct {
			Name string `json:"name"`
		} `json:"by"`
	}
	_ = json.Unmarshal(e.Data, &d)
	out := RunEvent{ID: e.ID, Type: e.Type, Run: d.Run, ScheduledFor: d.ScheduledFor, Note: d.Note}
	switch e.Type {
	case "run.computed", "run.adjusted", "run.excluded", "run.approved", "run.rejected", "run.locked", "run.cancelled", "run.payment_scheduled":
		out.By = d.ApprovedBy
		if e.Type == "run.rejected" {
			out.By = d.RejectedBy
		}
	case "approval.recorded":
		if d.Subject != "run" {
			return RunEvent{}, false
		}
		out.Subject, out.Action, out.Via, out.By = d.Subject, d.Action, d.Via, d.By.Name
		if out.Run.ID == "" {
			out.Run.ID = d.SubjectID
		}
	default:
		return RunEvent{}, false
	}
	return out, out.Run.ID != ""
}
