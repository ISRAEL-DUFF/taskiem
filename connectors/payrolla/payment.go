package payrolla

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
)

// After locking (P4b-4 of docs/spec/chat-pay-people.md; Payrolla's Phase A
// and B replies, 05 and 06): what is happening to a run's payment
// (payroll:read), paying a locked run now and sending its failed payments
// again with the linked person's code (payroll:approve; Decide with
// ActionPayNow or ActionRetryFailed), and payslips as data
// (payslips:read) for Taskiem's own cards, with Payrolla's 7-day link
// kept for someone outside Taskiem (payslips:links).

// Payment actions confirmed with a code.
const (
	ActionPayNow      = "pay_now"
	ActionRetryFailed = "retry_failed"
)

// decidePath is the route of a run action confirmed with a code.
func decidePath(action string) string {
	switch action {
	case ActionApprove, ActionLock:
		return action
	case ActionPayNow:
		return "pay-now"
	case ActionRetryFailed:
		return "retry-failed"
	}
	return ""
}

// RunPayment is what is happening to a run's payment: NOT_SCHEDULED,
// SCHEDULED, AWAITING_FUNDS, PAYING, COMPLETED, PARTIALLY_COMPLETED or
// FAILED, with how many were paid, failed, returned and still to pay.
func (cl *Client) RunPayment(ctx context.Context, run string) (Payment, error) {
	var out struct {
		Payment Payment `json:"payment"`
	}
	err := cl.c.do(ctx, cl.req, http.MethodGet, runPath(run, "payment"), nil, nil, &out)
	return out.Payment, err
}

// PayslipEmployee is who a payslip is for, as when the run was locked.
type PayslipEmployee struct {
	ID          string `json:"id"`
	FirstName   string `json:"first_name"`
	LastName    string `json:"last_name"`
	Department  string `json:"department,omitempty"`
	ExternalRef string `json:"external_ref,omitempty"`
}

// Payslip is a final payslip as data: the period, the employee and the
// employer, each line, and the payment's status.
type Payslip struct {
	ID          string          `json:"payslip_id"`
	RunID       string          `json:"run_id"`
	PeriodStart string          `json:"period_start"`
	PeriodEnd   string          `json:"period_end"`
	PayDate     string          `json:"pay_date"`
	Employee    PayslipEmployee `json:"employee"`
	Employer    struct {
		Name     string `json:"name"`
		RCNumber string `json:"rc_number,omitempty"`
	} `json:"employer"`
	GrossKobo      int64      `json:"gross_kobo"`
	EarningsKobo   int64      `json:"total_earnings_kobo"`
	DeductionsKobo int64      `json:"total_deductions_kobo"`
	NetKobo        int64      `json:"net_kobo"`
	Items          []LineItem `json:"items"`
	PaymentStatus  string     `json:"payment_status"`
}

// PayslipSummary is one of an employee's final payslips, listed.
type PayslipSummary struct {
	ID                 string `json:"payslip_id"`
	RunID              string `json:"run_id"`
	Period             string `json:"period"`
	PayDate            string `json:"pay_date"`
	GrossKobo          int64  `json:"gross_kobo"`
	NetKobo            int64  `json:"net_kobo"`
	DisbursementStatus string `json:"disbursement_status"`
}

// Payslip is one final payslip; PAYSLIP_NOT_FINAL before its run is
// locked.
func (cl *Client) Payslip(ctx context.Context, id string) (Payslip, error) {
	var out struct {
		Payslip Payslip `json:"payslip"`
	}
	err := cl.c.do(ctx, cl.req, http.MethodGet, "/connect/payslips/"+url.PathEscape(id), nil, nil, &out)
	return out.Payslip, err
}

// EmployeePayslips are an employee's final payslips, newest first.
func (cl *Client) EmployeePayslips(ctx context.Context, employee string) ([]PayslipSummary, error) {
	var out struct {
		Payslips []PayslipSummary `json:"payslips"`
	}
	err := cl.c.do(ctx, cl.req, http.MethodGet, "/connect/employees/"+url.PathEscape(employee)+"/payslips", nil, nil, &out)
	if out.Payslips == nil {
		out.Payslips = []PayslipSummary{}
	}
	return out.Payslips, err
}

// PayslipLink is Payrolla's 7-day link to one payslip, for someone not
// on Taskiem: the employee confirms the last four digits of their phone
// before it opens. Treat it as a secret until it reaches them.
type PayslipLink struct {
	URL       string `json:"url"`
	ExpiresAt string `json:"expires_at"`
}

// CreatePayslipLink asks Payrolla for a payslip's 7-day link
// (PAYSLIP_LINKS_DISABLED until the owner allows it, PAYSLIP_NOT_FINAL
// before the run is locked).
func (cl *Client) CreatePayslipLink(ctx context.Context, id string, w Write) (PayslipLink, error) {
	var out PayslipLink
	err := cl.write(ctx, http.MethodPost, "/connect/payslips/"+url.PathEscape(id)+"/link", w, map[string]any{}, &out)
	return out, err
}

// PaymentEvent is a payment's event as Taskiem's own subscription hears
// it: run.awaiting_funds (with how short the wallet is),
// run.disbursement_started, run.completed and run.partially_completed
// (with how many were paid and not), payout.* (one person's), and
// payslip.issued.
type PaymentEvent struct {
	ID, Type string
	Run      Run
	// RunID is the run's id (for payout.* and payslip.issued, which carry
	// no run summary).
	RunID                                string
	NeededKobo, AvailableKobo, ShortKobo int64
	PaidCount, FailedCount               int64
	Payslip, Employee, Period, Reason    string
}

// ReadPaymentEvent reads a payment's event; ok is false for any other.
func ReadPaymentEvent(body []byte) (PaymentEvent, bool) {
	var e Event
	if json.Unmarshal(body, &e) != nil {
		return PaymentEvent{}, false
	}
	var d struct {
		Run       Run    `json:"run"`
		RunID     string `json:"run_id"`
		Needed    int64  `json:"needed_kobo"`
		Available int64  `json:"available_kobo"`
		Short     int64  `json:"short_by_kobo"`
		Paid      int64  `json:"paid_count"`
		Failed    int64  `json:"failed_count"`
		Payslip   string `json:"payslip_id"`
		Employee  string `json:"employee_id"`
		Period    string `json:"period"`
		Reason    string `json:"reason"`
	}
	switch e.Type {
	case "run.awaiting_funds", "run.disbursement_started", "run.completed", "run.partially_completed",
		"payout.succeeded", "payout.failed", "payout.reversed", "payslip.issued":
	default:
		return PaymentEvent{}, false
	}
	_ = json.Unmarshal(e.Data, &d)
	out := PaymentEvent{ID: e.ID, Type: e.Type, Run: d.Run, RunID: d.RunID, NeededKobo: d.Needed, AvailableKobo: d.Available, ShortKobo: d.Short,
		PaidCount: d.Paid, FailedCount: d.Failed, Payslip: d.Payslip, Employee: d.Employee, Period: d.Period, Reason: d.Reason}
	if out.RunID == "" {
		out.RunID = d.Run.ID
	}
	return out, out.RunID != ""
}
