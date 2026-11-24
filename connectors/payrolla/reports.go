package payrolla

import (
	"context"
	"net/http"
	"net/url"
	"strings"
)

// Settings and the tax and pension schedules (P4b-5 of
// docs/spec/chat-pay-people.md; Payrolla's Phase C reply, 07, section 3):
// the company's payroll setup, read-only (any connection), and the PAYE,
// pension, NHF and NHIS schedules of each run with a line per person
// (reports:read). Pension schedules carry each person's PFA and pension
// PIN: they are read when a screen asks and kept nowhere.

// Settings is how the company runs payroll in Payrolla.
type Settings struct {
	Company struct {
		ID                 string `json:"id"`
		Name               string `json:"name"`
		RCNumber           string `json:"rc_number,omitempty"`
		TaxState           string `json:"tax_state,omitempty"`
		PayFrequency       string `json:"pay_frequency"`
		PAYETINSet         bool   `json:"paye_tin_set"`
		SeparationOfDuties bool   `json:"separation_of_duties"`
		FundingModel       string `json:"funding_model,omitempty"`
		KYBVerified        bool   `json:"kyb_verified"`
	} `json:"company"`
	// Statutory is nil until the company has set its deductions up.
	Statutory *Statutory `json:"statutory"`
}

// Statutory are the deductions in force: rates are fractions (0.08 is 8%).
type Statutory struct {
	PAYE                bool    `json:"paye_enabled"`
	Pension             bool    `json:"pension_enabled"`
	PensionEmployeeRate float64 `json:"pension_employee_rate"`
	PensionEmployerRate float64 `json:"pension_employer_rate"`
	NHF                 bool    `json:"nhf_enabled"`
	NHFRate             float64 `json:"nhf_rate"`
	NHIS                bool    `json:"nhis_enabled"`
	EffectiveFrom       string  `json:"effective_from,omitempty"`
}

// Remittance is one schedule: what one authority is owed for one run.
type Remittance struct {
	ID            string `json:"id"`
	RunID         string `json:"payroll_run_id"`
	Authority     string `json:"authority"` // STATE_IRS, PENCOM_PFA, NHF or NHIS
	Period        string `json:"period"`
	TotalKobo     int64  `json:"total_kobo"`
	Status        string `json:"status"` // DRAFT, GENERATED, PAYABLE, FILED or PAID
	DueDate       string `json:"due_date,omitempty"`
	PFACode       string `json:"pfa_code,omitempty"`
	EmployeeCount int64  `json:"employee_count"`
	FiledAt       string `json:"filed_at,omitempty"`
	PaidAt        string `json:"paid_at,omitempty"`
	FilingRef     string `json:"filing_ref,omitempty"`
	PaymentRef    string `json:"payment_ref,omitempty"`
}

// RemittanceLine is one person's part of a schedule; PFA and PIN on
// pension schedules only.
type RemittanceLine struct {
	EmployeeID string `json:"employee_id"`
	Name       string `json:"name"`
	PFA        string `json:"pension_pfa,omitempty"`
	PIN        string `json:"pension_pin,omitempty"`
	AmountKobo int64  `json:"amount_kobo"`
}

// Settings is the company's payroll setup.
func (cl *Client) Settings(ctx context.Context) (Settings, error) {
	var out Settings
	err := cl.c.do(ctx, cl.req, http.MethodGet, "/connect/settings", nil, nil, &out)
	return out, err
}

// Remittances are the schedules, all or those at one status (PAYABLE,
// PAID, …). SCOPE_REQUIRED without reports:read.
func (cl *Client) Remittances(ctx context.Context, status string) ([]Remittance, error) {
	path := "/connect/remittances"
	if status != "" {
		path += "?" + url.Values{"status": {status}}.Encode()
	}
	var out struct {
		Remittances []Remittance `json:"remittances"`
	}
	err := cl.c.do(ctx, cl.req, http.MethodGet, path, nil, nil, &out)
	return out.Remittances, err
}

// Remittance is one schedule with a line per person.
func (cl *Client) Remittance(ctx context.Context, id string) (Remittance, []RemittanceLine, error) {
	var out struct {
		Remittance Remittance       `json:"remittance"`
		Lines      []RemittanceLine `json:"lines"`
	}
	err := cl.c.do(ctx, cl.req, http.MethodGet, "/connect/remittances/"+url.PathEscape(id), nil, nil, &out)
	return out.Remittance, out.Lines, err
}

// SettingsURL is where the owner changes the payroll's setup: the
// settings page of the Payrolla sign-in app this connection's mode uses
// ("" when this Taskiem knows none).
func (cl *Client) SettingsURL() string {
	app := ""
	for _, h := range cl.c.m.Auth.HostModes {
		if h.Mode == cl.req.Mode() && h.AuthorizeURL != "" {
			app = h.AuthorizeURL
		}
	}
	if app == "" && cl.c.m.Auth.OAuth2 != nil {
		app = cl.c.m.Auth.OAuth2.AuthorizeURL
	}
	app = strings.TrimSuffix(strings.TrimRight(app, "/"), "/oauth/authorize")
	if !strings.HasPrefix(app, "https://") && !strings.HasPrefix(app, "http://") {
		return ""
	}
	return app + "/settings"
}
