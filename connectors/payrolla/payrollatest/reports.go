package payrollatest

import (
	"net/http"
	"strings"
	"sync"
	"time"
)

// Settings and the tax and pension schedules (P4b-5), added with Handle:
// GET /connect/settings (any connection) and GET /connect/remittances and
// /connect/remittances/{id} (reports:read), as Payrolla's Phase C reply
// (07, section 3) and its OpenAPI file have them. Call ServeReports once
// to switch them on (ServeRuns too, for the runs the schedules are of).
//
// The schedules are worked out from each run that was locked: PAYE to the
// state's revenue service, pension to each person's PFA (one schedule a
// PFA), NHF, and NHIS when it is in force. Each has a line per person;
// pension lines carry the PFA and the pension PIN.

// PensionAccount is where one employee's pension goes.
type PensionAccount struct {
	PFA, PIN string
}

// ReportConfig is the company's payroll setup as the fake has it.
type ReportConfig struct {
	TaxState     string
	RCNumber     string
	FundingModel string
	PAYETINSet   bool
	// NoStatutory: the deductions are not set up yet (statutory null).
	NoStatutory bool
	// NHIS puts health insurance in force (5% of basic, a schedule of its
	// own).
	NHIS bool
	// Pension is each employee's PFA and PIN, by employee id.
	Pension map[string]PensionAccount
	// Paid are the runs whose schedules have been paid.
	Paid map[string]bool
}

type reportState struct {
	cfg *ReportConfig
	ids map[string]string // run/authority/pfa -> the schedule's id
}

var (
	reportsMu    sync.Mutex
	reportStates = map[*Fake]*reportState{}
)

func (f *Fake) reports() *reportState {
	reportsMu.Lock()
	defer reportsMu.Unlock()
	st := reportStates[f]
	if st == nil {
		st = &reportState{cfg: &ReportConfig{TaxState: "Lagos", RCNumber: "RC1234567", FundingModel: "PREFUNDED", PAYETINSet: true,
			Pension: map[string]PensionAccount{}, Paid: map[string]bool{}}, ids: map[string]string{}}
		reportStates[f] = st
	}
	return st
}

// Reports is the company's setup as the fake has it, to change in a test.
func (f *Fake) Reports() *ReportConfig {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.reports().cfg
}

// SetScopes changes what a connection was allowed (the owner allowing
// less in Payrolla).
func (f *Fake) SetScopes(c *Conn, scopes ...string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	c.Scopes = scopes
}

// ServeReports serves the settings and the schedules.
func (f *Fake) ServeReports() {
	f.mu.Lock()
	f.reports()
	f.mu.Unlock()
	f.Handle(http.MethodGet, "/connect/settings", f.settingsRoute)
	f.Handle(http.MethodGet, "/connect/remittances", f.remittancesRoute)
}

// AddLockedRun makes a month's run, worked out and locked, as if it had
// been approved and locked in Payrolla; it returns the run's id.
func (f *Fake) AddLockedRun(period string, preparedBy string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	m, err := time.Parse("2006-01", period)
	if err != nil {
		panic("payrollatest: period is YYYY-MM")
	}
	r := &fakeRun{id: f.id(), start: date(m), end: date(m.AddDate(0, 1, -1)), payDate: date(m.AddDate(0, 1, -1)), status: "DRAFT", preparedBy: preparedBy,
		created: f.Now(), updated: f.Now()}
	f.rs().runs = append(f.rs().runs, r)
	f.compute(r)
	r.status, r.approved, r.locked = "LOCKED", f.Now(), f.Now()
	return r.id
}

func (f *Fake) settingsRoute(w http.ResponseWriter, _ *http.Request, _ *Conn) {
	f.mu.Lock()
	defer f.mu.Unlock()
	cfg := f.reports().cfg
	company := map[string]any{"id": f.CompanyID, "name": f.CompanyName, "rc_number": cfg.RCNumber, "tax_state": cfg.TaxState, "pay_frequency": "MONTHLY",
		"paye_tin_set": cfg.PAYETINSet, "separation_of_duties": f.runConfig().SeparationOfDuties, "funding_model": cfg.FundingModel, "kyb_verified": true}
	var statutory any
	if !cfg.NoStatutory {
		statutory = map[string]any{"paye_enabled": true, "pension_enabled": true, "pension_employee_rate": 0.08, "pension_employer_rate": 0.10,
			"nhf_enabled": true, "nhf_rate": 0.025, "nhis_enabled": cfg.NHIS, "effective_from": "2026-01-01T00:00:00Z"}
	}
	writeData(w, http.StatusOK, map[string]any{"company": company, "statutory": statutory})
}

type fakeRemittance struct {
	head  map[string]any
	lines []map[string]any
}

// schedules are every locked run's schedules, newest run first.
func (f *Fake) schedules() []fakeRemittance {
	st := f.reports()
	var out []fakeRemittance
	runs := f.rs().runs
	for i := len(runs) - 1; i >= 0; i-- {
		r := runs[i]
		if r.locked.IsZero() || r.status == "CANCELLED" {
			continue
		}
		start, _ := time.Parse("2006-01-02", r.start)
		next := time.Date(start.Year(), start.Month()+1, 1, 0, 0, 0, 0, time.UTC)
		status := "PAYABLE"
		if st.cfg.Paid[r.id] {
			status = "PAID"
		}
		add := func(authority, pfa string, due int, lines []map[string]any) {
			if len(lines) == 0 {
				return
			}
			key := r.id + "/" + authority + "/" + pfa
			id := st.ids[key]
			if id == "" {
				id = f.id()
				st.ids[key] = id
			}
			var total int64
			for _, l := range lines {
				total += l["amount_kobo"].(int64)
			}
			h := map[string]any{"id": id, "payroll_run_id": r.id, "authority": authority, "period": r.start[:7], "total_kobo": total, "status": status,
				"due_date": next.AddDate(0, 0, due-1).Format(time.RFC3339), "employee_count": len(lines), "filed_at": nil, "paid_at": nil}
			if pfa != "" {
				h["pfa_code"] = pfa
			}
			if status == "PAID" {
				h["paid_at"] = next.Format(time.RFC3339)
				h["payment_ref"] = "PAY-" + id[len(id)-6:]
			}
			out = append(out, fakeRemittance{head: h, lines: lines})
		}
		var paye, nhf, nhis []map[string]any
		pension := map[string][]map[string]any{}
		var pfas []string
		for _, l := range r.lines {
			emp := l["employee_id"].(string)
			name := strings.TrimSpace(l["first_name"].(string) + " " + l["last_name"].(string))
			var amt = map[string]int64{}
			for _, it := range l["items"].([]map[string]any) {
				amt[it["code"].(string)] += it["amount_kobo"].(int64)
			}
			line := func(a int64) map[string]any {
				return map[string]any{"employee_id": emp, "name": name, "amount_kobo": a}
			}
			paye = append(paye, line(amt["PAYE"]))
			nhf = append(nhf, line(amt["NHF"]))
			if st.cfg.NHIS {
				nhis = append(nhis, line(pct(amt["BASIC"], 50)))
			}
			acc := st.cfg.Pension[emp]
			pl := line(amt["PENSION_EE"] + amt["PENSION_ER"])
			pl["pension_pfa"], pl["pension_pin"] = acc.PFA, acc.PIN
			if _, ok := pension[acc.PFA]; !ok {
				pfas = append(pfas, acc.PFA)
			}
			pension[acc.PFA] = append(pension[acc.PFA], pl)
		}
		add("STATE_IRS", "", 10, paye)
		for _, p := range pfas {
			add("PENCOM_PFA", p, 7, pension[p])
		}
		add("NHF", "", 10, nhf)
		add("NHIS", "", 10, nhis)
	}
	return out
}

func (f *Fake) remittancesRoute(w http.ResponseWriter, req *http.Request, c *Conn) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !has(c, "reports:read") {
		writeErr(w, http.StatusForbidden, "SCOPE_REQUIRED", "this connection needs reports:read")
		return
	}
	path := strings.Trim(strings.TrimPrefix(req.URL.Path, "/api/v1/connect/remittances"), "/")
	all := f.schedules()
	if path == "" {
		status := req.URL.Query().Get("status")
		list := []map[string]any{}
		for _, x := range all {
			if status == "" || x.head["status"] == status {
				list = append(list, x.head)
			}
		}
		writeData(w, http.StatusOK, map[string]any{"remittances": list})
		return
	}
	for _, x := range all {
		if x.head["id"] == path {
			lines := make([]map[string]any, 0, len(x.lines))
			for _, l := range x.lines {
				m := map[string]any{"employee_id": l["employee_id"], "name": l["name"], "amount_kobo": l["amount_kobo"]}
				if x.head["authority"] == "PENCOM_PFA" {
					m["pension_pfa"], m["pension_pin"] = l["pension_pfa"], l["pension_pin"]
				}
				lines = append(lines, m)
			}
			writeData(w, http.StatusOK, map[string]any{"remittance": x.head, "lines": lines})
			return
		}
	}
	writeErr(w, http.StatusNotFound, "NOT_FOUND", "no such remittance")
}

// ReportRoutes are the routes ServeReports adds, as METHOD /path with
// Payrolla's own parameter names, for checking against its OpenAPI file.
func ReportRoutes() []string {
	return []string{"GET /connect/settings", "GET /connect/remittances", "GET /connect/remittances/{remittanceID}"}
}

// ReportErrorCodes are the error codes the report routes answer with.
func ReportErrorCodes() []string { return []string{"SCOPE_REQUIRED", "NOT_FOUND"} }
