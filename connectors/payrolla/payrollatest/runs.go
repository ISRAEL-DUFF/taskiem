package payrollatest

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"
)

// This month's run (P4b-3), added with Handle: runs made and worked out,
// each line's breakdown, bonuses and deductions, leaving someone out and
// putting them back, recalculating, cancelling, the funding wallet, and
// approving, rejecting and locking with a code sent to the linked
// approver's factor (Payrolla's Phase A and B replies, 05 and 06, and its
// OpenAPI file). Call ServeRuns once to switch it on.
//
// Payrolla's own rules: one regular run a period (DUPLICATE_RUN); changes
// only before approval (INVALID_STATE); only linked Owners and Approvers
// decide (APPROVER_NOT_LINKED, NOT_AN_APPROVER); with SeparationOfDuties,
// whoever prepared a run can't approve it (SOD_VIOLATION); locking is
// refused while someone in the run has no account (NO_PAYOUT_DESTINATION)
// and schedules the payment for the pay date. Events are queued and sent
// to the connection's webhooks by Flush.

// Worker is an employee as a run sees them: their monthly gross and
// whether they have an account to be paid into.
type Worker struct {
	ID         string
	First      string
	Last       string
	GrossKobo  int64
	NoAccount  bool
	Terminated bool
}

type fakeRun struct {
	id, start, end, payDate, status string
	preparedBy                      string
	created, updated                time.Time
	approved, locked                time.Time
	lines                           []map[string]any
	adjustments                     []map[string]any
	exclusions                      []map[string]any
	gross, net                      int64
	count                           int
}

type runChallenge struct {
	run, action string
}

type queued struct {
	conn *Conn
	typ  string
	data map[string]any
}

type runState struct {
	workers []*Worker
	runs    []*fakeRun
	codes   map[string]runChallenge
	queue   []queued
}

// SetWorkers sets who is employed, in order.
func (f *Fake) SetWorkers(ws ...Worker) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.rs().workers = nil
	for i := range ws {
		w := ws[i]
		if w.ID == "" {
			w.ID = f.id()
		}
		f.rs().workers = append(f.rs().workers, &w)
	}
}

// Workers are who is employed.
func (f *Fake) Workers() []Worker {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []Worker
	for _, w := range f.rs().workers {
		out = append(out, *w)
	}
	return out
}

// SetWorker changes one employee (an account added, pay raised).
func (f *Fake) SetWorker(w Worker) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i, x := range f.rs().workers {
		if x.ID == w.ID {
			f.rs().workers[i] = &w
		}
	}
}

// runsState is kept in a package map keyed by the fake, so the fake's own
// struct (P4b-1's) is unchanged.
var (
	statesMu   sync.Mutex
	runStates  = map[*Fake]*runState{}
	runConfigs = map[*Fake]*RunConfig{}
)

func (f *Fake) rs() *runState {
	statesMu.Lock()
	defer statesMu.Unlock()
	st := runStates[f]
	if st == nil {
		st = &runState{codes: map[string]runChallenge{}}
		runStates[f] = st
	}
	return st
}

// RunConfig is how the fake's company runs payroll.
type RunConfig struct {
	// ComputeError, when set, is why runs can't be worked out (the
	// company's statutory settings aren't complete): runs stay DRAFT.
	ComputeError string
	// SeparationOfDuties: whoever prepared a run can't approve it.
	SeparationOfDuties bool
	// HasWallet and WalletKobo are the funding wallet.
	HasWallet  bool
	WalletKobo int64
}

// Runs is the company's payroll settings as the fake has them, to change
// in a test.
func (f *Fake) Runs() *RunConfig {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.runConfig()
}

func (f *Fake) runConfig() *RunConfig {
	statesMu.Lock()
	defer statesMu.Unlock()
	c := runConfigs[f]
	if c == nil {
		c = &RunConfig{HasWallet: true}
		runConfigs[f] = c
	}
	return c
}

// ServeRuns serves the run routes on the fake.
func (f *Fake) ServeRuns() {
	f.mu.Lock()
	f.rs()
	f.runConfig()
	f.mu.Unlock()
	for _, m := range []string{http.MethodGet, http.MethodPost, http.MethodDelete} {
		f.Handle(m, "/connect/runs", f.runsRoute)
	}
	f.Handle(http.MethodGet, "/connect/funding", f.fundingRoute)
	f.Handle(http.MethodGet, "/connect/employees/", f.employeeRoute)
}

// employeeRoute is GET /connect/employees/{id}, the one handler for it
// whether the fake serves runs (ServeRuns), staff and pay (NewTeam) or
// both: a run's worker first, then the Team's employee.
func (f *Fake) employeeRoute(w http.ResponseWriter, req *http.Request, c *Conn) {
	if f.workerRoute(w, req, c) {
		return
	}
	if t, ok := teams.Load(f); ok {
		t.(*Team).serve(w, req, c)
		return
	}
	writeErr(w, http.StatusNotFound, "NOT_FOUND", "no such employee")
}

// workerRoute answers for one employee as a run's page names them, and
// says whether it did.
func (f *Fake) workerRoute(w http.ResponseWriter, req *http.Request, c *Conn) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !has(c, "employees:read") {
		writeErr(w, http.StatusForbidden, "SCOPE_REQUIRED", "this connection needs employees:read")
		return true
	}
	id := strings.TrimPrefix(req.URL.Path, "/api/v1/connect/employees/")
	for _, x := range f.rs().workers {
		if x.ID == id {
			status, method := "ACTIVE", "BANK"
			if x.Terminated {
				status = "TERMINATED"
			}
			if x.NoAccount {
				method = "NONE"
			}
			writeData(w, http.StatusOK, map[string]any{"employee": map[string]any{"id": x.ID, "first_name": x.First, "last_name": x.Last, "email": "", "phone": "",
				"department": "", "status": status, "employment_type": "SALARIED", "start_date": "2026-01-01", "pay_method": method}})
			return true
		}
	}
	return false
}

// Flush sends the events queued since the last Flush, in order, to the
// webhooks of the connection that caused them.
func (f *Fake) Flush() {
	f.mu.Lock()
	q := f.rs().queue
	f.rs().queue = nil
	f.mu.Unlock()
	for _, e := range q {
		f.Deliver(e.conn, e.typ, e.data)
	}
}

// Queued are the types of the events waiting for Flush.
func (f *Fake) Queued() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for _, e := range f.rs().queue {
		out = append(out, e.typ)
	}
	return out
}

// RunStatus is a run's status now.
func (f *Fake) RunStatus(id string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if r := f.run(id); r != nil {
		return r.status
	}
	return ""
}

func (f *Fake) run(id string) *fakeRun {
	for _, r := range f.rs().runs {
		if r.id == id {
			return r
		}
	}
	return nil
}

func (f *Fake) emit(c *Conn, typ string, data map[string]any) {
	f.rs().queue = append(f.rs().queue, queued{conn: c, typ: typ, data: data})
}

func date(t time.Time) string { return t.Format("2006-01-02") }

func ts(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return t.UTC().Format(time.RFC3339)
}

func (r *fakeRun) summary() map[string]any {
	return map[string]any{"run_id": r.id, "period_start": r.start, "period_end": r.end, "pay_date": r.payDate, "run_type": "REGULAR",
		"status": r.status, "employee_count": r.count, "total_gross_kobo": r.gross, "total_net_kobo": r.net}
}

func (r *fakeRun) detail() map[string]any {
	d := r.summary()
	d["created_at"], d["updated_at"] = ts(r.created), ts(r.updated)
	if !r.approved.IsZero() {
		d["approved_at"] = ts(r.approved)
	}
	if !r.locked.IsZero() {
		d["locked_at"] = ts(r.locked)
	}
	if r.status == "COMPUTED" {
		d["approval_url"] = "https://payrolla.test/runs/" + r.id + "/review"
	}
	return d
}

func pct(n int64, p int64) int64 { return n * p / 1000 }

// compute works a run out: each employee still employed and not left out,
// their pay split into basic, housing and transport, bonuses added and
// deductions taken, then tax, pension and NHF.
func (f *Fake) compute(r *fakeRun) *map[string]any {
	if msg := f.runConfig().ComputeError; msg != "" {
		r.status, r.lines, r.gross, r.net, r.count = "DRAFT", nil, 0, 0, 0
		return &map[string]any{"code": "STATUTORY_SETTINGS_INCOMPLETE", "message": msg}
	}
	r.lines, r.gross, r.net, r.count = nil, 0, 0, 0
	for _, w := range f.rs().workers {
		if w.Terminated || slices.ContainsFunc(r.exclusions, func(x map[string]any) bool { return x["employee_id"] == w.ID }) {
			continue
		}
		g := w.GrossKobo
		items := []map[string]any{
			{"category": "EARNING", "code": "BASIC", "label": "Basic", "amount_kobo": pct(g, 600)},
			{"category": "EARNING", "code": "HOUSING", "label": "Housing", "amount_kobo": pct(g, 250)},
			{"category": "EARNING", "code": "TRANSPORT", "label": "Transport", "amount_kobo": g - pct(g, 600) - pct(g, 250)},
		}
		earn, ded := g, int64(0)
		for _, a := range r.adjustments {
			if a["employee_id"] != w.ID {
				continue
			}
			amt := a["amount_kobo"].(int64)
			if a["type"] == "BONUS" {
				earn += amt
				items = append(items, map[string]any{"category": "EARNING", "code": "BONUS", "label": "Bonus: " + a["reason"].(string), "amount_kobo": amt})
			} else {
				ded += amt
				items = append(items, map[string]any{"category": "VOLUNTARY_DEDUCTION", "code": "DEDUCTION", "label": a["reason"].(string), "amount_kobo": amt})
			}
		}
		taxable := earn - ded
		paye, pension, nhf := pct(taxable, 70), pct(g, 80), pct(pct(g, 600), 25)
		items = append(items,
			map[string]any{"category": "STATUTORY_DEDUCTION", "code": "PAYE", "label": "PAYE", "amount_kobo": paye},
			map[string]any{"category": "STATUTORY_DEDUCTION", "code": "PENSION_EE", "label": "Pension (employee)", "amount_kobo": pension},
			map[string]any{"category": "STATUTORY_DEDUCTION", "code": "NHF", "label": "NHF", "amount_kobo": nhf},
			map[string]any{"category": "EMPLOYER_CONTRIBUTION", "code": "PENSION_ER", "label": "Pension (employer)", "amount_kobo": pct(g, 100)})
		ded += paye + pension + nhf
		net := earn - ded
		r.lines = append(r.lines, map[string]any{"payslip_id": f.id(), "employee_id": w.ID, "first_name": w.First, "last_name": w.Last, "net_kobo": net,
			"disbursement_status": "PENDING", "gross_kobo": g, "total_earnings_kobo": earn, "total_deductions_kobo": ded, "items": items})
		r.gross += earn
		r.net += net
		r.count++
	}
	r.status = "COMPUTED"
	r.updated = f.Now()
	return nil
}

func (f *Fake) fundingRoute(w http.ResponseWriter, _ *http.Request, c *Conn) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !has(c, "payroll:read") {
		writeErr(w, http.StatusForbidden, "SCOPE_REQUIRED", "this connection needs payroll:read")
		return
	}
	cfg := f.runConfig()
	writeData(w, http.StatusOK, map[string]any{"funding": map[string]any{"has_wallet": cfg.HasWallet, "available_kobo": cfg.WalletKobo, "currency": "NGN"}})
}

func (f *Fake) changed(w http.ResponseWriter, status int, r *fakeRun, extra map[string]any, cerr *map[string]any) {
	out := map[string]any{"run": r.detail(), "lines": linesOf(r)}
	for k, v := range extra {
		out[k] = v
	}
	if cerr != nil {
		out["compute_error"] = *cerr
	}
	writeData(w, status, out)
}

func linesOf(r *fakeRun) []map[string]any {
	if r.lines == nil {
		return []map[string]any{}
	}
	return r.lines
}

func (f *Fake) runsRoute(w http.ResponseWriter, req *http.Request, c *Conn) {
	var raw []byte
	if req.Body != nil {
		raw = readAll(req)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	path := strings.TrimPrefix(req.URL.Path, "/api/v1")
	parts := strings.Split(strings.Trim(path, "/"), "/") // connect runs {id} ...
	acting := req.Header.Get("X-Acting-User")
	scope := "payroll:read"
	if req.Method != http.MethodGet {
		scope = "payroll:write"
		if len(parts) >= 4 && slices.Contains([]string{"confirmations", "approve", "lock", "reject"}, parts[3]) {
			scope = "payroll:approve"
		}
	}
	if !has(c, scope) {
		writeErr(w, http.StatusForbidden, "SCOPE_REQUIRED", "this connection needs "+scope)
		return
	}
	if len(parts) == 2 {
		switch req.Method {
		case http.MethodGet:
			list := []map[string]any{}
			runs := slices.Clone(f.rs().runs)
			sort.SliceStable(runs, func(i, j int) bool { return runs[i].start > runs[j].start })
			for _, r := range runs {
				list = append(list, r.summary())
			}
			writeData(w, http.StatusOK, map[string]any{"runs": list, "next_cursor": ""})
		case http.MethodPost:
			f.createRun(w, c, raw, acting)
		default:
			writeErr(w, http.StatusNotFound, "NOT_FOUND", "no such route")
		}
		return
	}
	r := f.run(parts[2])
	if r == nil {
		writeErr(w, http.StatusNotFound, "NOT_FOUND", "no such run")
		return
	}
	rest := strings.Join(parts[3:], "/")
	open := r.status == "DRAFT" || r.status == "COMPUTED"
	var in map[string]any
	_ = json.Unmarshal(raw, &in)
	switch {
	case req.Method == http.MethodGet && rest == "":
		writeData(w, http.StatusOK, map[string]any{"run": r.detail()})
	case req.Method == http.MethodGet && rest == "lines":
		writeData(w, http.StatusOK, map[string]any{"lines": linesOf(r)})
	case req.Method == http.MethodGet && rest == "adjustments":
		writeData(w, http.StatusOK, map[string]any{"adjustments": nonNilList(r.adjustments)})
	case req.Method == http.MethodGet && rest == "exclusions":
		writeData(w, http.StatusOK, map[string]any{"exclusions": nonNilList(r.exclusions)})
	case req.Method == http.MethodPost && (rest == "compute" || rest == "cancel"):
		if !open {
			writeErr(w, http.StatusConflict, "INVALID_STATE", "the run has moved on")
			return
		}
		if rest == "cancel" {
			r.status, r.updated = "CANCELLED", f.Now()
			f.emit(c, "run.cancelled", map[string]any{"run": r.summary()})
		} else if f.compute(r) == nil {
			f.emit(c, "run.computed", map[string]any{"run": r.summary(), "approval_url": "https://payrolla.test/runs/" + r.id + "/review"})
		}
		writeData(w, http.StatusOK, map[string]any{"run": r.detail()})
	case req.Method == http.MethodPost && len(parts) == 6 && parts[3] == "lines" && (parts[5] == "adjustments" || parts[5] == "exclude"):
		f.lineChange(w, c, r, parts[4], parts[5], in, open)
	case req.Method == http.MethodDelete && len(parts) == 5 && (parts[3] == "adjustments" || parts[3] == "exclusions"):
		if !open {
			writeErr(w, http.StatusConflict, "INVALID_STATE", "the run has moved on")
			return
		}
		list := &r.adjustments
		if parts[3] == "exclusions" {
			list = &r.exclusions
		}
		i := slices.IndexFunc(*list, func(x map[string]any) bool { return x["id"] == parts[4] })
		if i < 0 {
			writeErr(w, http.StatusNotFound, "NOT_FOUND", "no such change")
			return
		}
		gone := (*list)[i]
		*list = slices.Delete(*list, i, i+1)
		cerr := f.compute(r)
		extra := map[string]any{}
		if parts[3] == "adjustments" {
			extra["adjustment"] = gone
			f.emit(c, "run.adjusted", map[string]any{"run": r.summary(), "change": "removed", "adjustment": gone})
		} else {
			extra["exclusion"] = gone
			f.emit(c, "run.excluded", map[string]any{"run": r.summary(), "change": "removed", "exclusion_id": gone["id"], "employee_id": gone["employee_id"], "reason": gone["reason"]})
		}
		if cerr == nil {
			f.emit(c, "run.computed", map[string]any{"run": r.summary(), "approval_url": "https://payrolla.test/runs/" + r.id + "/review"})
		}
		f.changed(w, http.StatusOK, r, extra, cerr)
	case req.Method == http.MethodPost && rest == "confirmations":
		f.startRunCode(w, r, acting, in)
	case req.Method == http.MethodPost && (rest == "approve" || rest == "lock"):
		f.decide(w, c, r, rest, acting, req.Header.Get("X-Acting-User-Name"), in)
	case req.Method == http.MethodPost && rest == "reject":
		a := f.linked(acting)
		switch {
		case a == nil:
			writeErr(w, http.StatusForbidden, "APPROVER_NOT_LINKED", "link the acting person first")
		case !a.CanApprove:
			writeErr(w, http.StatusForbidden, "NOT_AN_APPROVER", "their role doesn't allow it")
		case r.status != "COMPUTED":
			writeErr(w, http.StatusConflict, "INVALID_STATE", "the run isn't waiting for approval")
		case strings.TrimSpace(fmt.Sprint(in["note"])) == "" || in["note"] == nil:
			writeErr(w, http.StatusUnprocessableEntity, "REASON_REQUIRED", "a note is required")
		default:
			r.status, r.updated = "DRAFT", f.Now()
			f.emit(c, "run.rejected", map[string]any{"run": r.summary(), "note": in["note"], "rejected_by": a.Name})
			f.emit(c, "approval.recorded", map[string]any{"subject": "run", "subject_id": r.id, "action": "rejected", "by": map[string]any{"user_id": a.UserID, "name": a.Name},
				"via": "connect", "app_name": "Taskiem", "run": r.summary()})
			writeData(w, http.StatusOK, map[string]any{"run": r.detail(), "payment": payment(r)})
		}
	default:
		writeErr(w, http.StatusNotFound, "NOT_FOUND", "no such route")
	}
}

func nonNilList(l []map[string]any) []map[string]any {
	if l == nil {
		return []map[string]any{}
	}
	return l
}

func readAll(req *http.Request) []byte {
	b, _ := io.ReadAll(req.Body)
	return b
}

func (f *Fake) createRun(w http.ResponseWriter, c *Conn, raw []byte, acting string) {
	var in struct {
		Period      string `json:"period"`
		PeriodStart string `json:"period_start"`
		PeriodEnd   string `json:"period_end"`
		PayDate     string `json:"pay_date"`
	}
	if err := json.Unmarshal(raw, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "INVALID_BODY", "unreadable")
		return
	}
	start, end := in.PeriodStart, in.PeriodEnd
	if in.Period != "" {
		m, err := time.Parse("2006-01", in.Period)
		if err != nil {
			writeErr(w, http.StatusUnprocessableEntity, "INVALID_PERIOD", "period is YYYY-MM")
			return
		}
		start, end = date(m), date(m.AddDate(0, 1, -1))
	}
	if _, err := time.Parse("2006-01-02", start); err != nil || end < start {
		writeErr(w, http.StatusUnprocessableEntity, "INVALID_PERIOD", "name the period")
		return
	}
	for _, r := range f.rs().runs {
		if r.start == start && r.status != "CANCELLED" {
			writeErr(w, http.StatusConflict, "DUPLICATE_RUN", "a run for this period already exists")
			return
		}
	}
	pay := in.PayDate
	if pay == "" {
		pay = end
	}
	r := &fakeRun{id: f.id(), start: start, end: end, payDate: pay, status: "DRAFT", preparedBy: acting, created: f.Now(), updated: f.Now()}
	f.rs().runs = append(f.rs().runs, r)
	cerr := f.compute(r)
	if cerr == nil {
		f.emit(c, "run.computed", map[string]any{"run": r.summary(), "approval_url": "https://payrolla.test/runs/" + r.id + "/review"})
	}
	out := map[string]any{"run": r.detail()}
	if cerr != nil {
		out["compute_error"] = *cerr
	}
	writeData(w, http.StatusCreated, out)
}

func (f *Fake) lineChange(w http.ResponseWriter, c *Conn, r *fakeRun, emp, what string, in map[string]any, open bool) {
	if !open {
		writeErr(w, http.StatusConflict, "INVALID_STATE", "the run has moved on")
		return
	}
	if !slices.ContainsFunc(f.rs().workers, func(x *Worker) bool { return x.ID == emp }) {
		writeErr(w, http.StatusNotFound, "NOT_FOUND", "no such employee")
		return
	}
	reason, _ := in["reason"].(string)
	if strings.TrimSpace(reason) == "" {
		writeErr(w, http.StatusUnprocessableEntity, "REASON_REQUIRED", "a reason is required")
		return
	}
	for k := range in {
		if !slices.Contains([]string{"type", "amount_kobo", "reason"}, k) {
			writeErr(w, http.StatusBadRequest, "UNKNOWN_FIELD", k+" is not accepted")
			return
		}
	}
	x := map[string]any{"id": f.id(), "employee_id": emp, "reason": reason, "created_at": ts(f.Now())}
	extra := map[string]any{}
	if what == "exclude" {
		if slices.ContainsFunc(r.exclusions, func(e map[string]any) bool { return e["employee_id"] == emp }) {
			writeErr(w, http.StatusConflict, "ALREADY_EXCLUDED", "they're already left out")
			return
		}
		r.exclusions = append(r.exclusions, x)
		extra["exclusion"] = x
	} else {
		typ, _ := in["type"].(string)
		amt, _ := in["amount_kobo"].(float64)
		if (typ != "BONUS" && typ != "DEDUCTION") || amt < 1 {
			writeErr(w, http.StatusBadRequest, "INVALID_BODY", "type BONUS or DEDUCTION, amount_kobo at least 1")
			return
		}
		x["type"], x["amount_kobo"] = typ, int64(amt)
		r.adjustments = append(r.adjustments, x)
		extra["adjustment"] = x
	}
	cerr := f.compute(r)
	if what == "exclude" {
		f.emit(c, "run.excluded", map[string]any{"run": r.summary(), "change": "added", "exclusion_id": x["id"], "employee_id": emp, "reason": reason})
	} else {
		f.emit(c, "run.adjusted", map[string]any{"run": r.summary(), "change": "added", "adjustment": x})
	}
	if cerr == nil {
		f.emit(c, "run.computed", map[string]any{"run": r.summary(), "approval_url": "https://payrolla.test/runs/" + r.id + "/review"})
	}
	f.changed(w, http.StatusCreated, r, extra, cerr)
}

// linked is the approver the acting person is linked to.
func (f *Fake) linked(acting string) *Approver {
	for _, a := range f.approvers {
		if a.LinkedAs != "" && a.LinkedAs == acting {
			return a
		}
	}
	return nil
}

// refuse says why the acting person may not do action on r now, or "".
func (f *Fake) refuse(w http.ResponseWriter, r *fakeRun, action, acting string) bool {
	a := f.linked(acting)
	refuse := func(status int, code, msg string) bool {
		writeErr(w, status, code, msg)
		return true
	}
	switch {
	case action != "approve" && action != "lock":
		return refuse(http.StatusBadRequest, "INVALID_BODY", "this fake confirms approve and lock")
	case a == nil:
		return refuse(http.StatusForbidden, "APPROVER_NOT_LINKED", "link the acting person first")
	case !a.CanApprove:
		return refuse(http.StatusForbidden, "NOT_AN_APPROVER", "their role doesn't allow it")
	case !a.CanConfirm:
		return refuse(http.StatusForbidden, "PAYMENT_CONFIRMATION_NOT_SET_UP", "they have no factor to receive codes on")
	case action == "approve" && r.status != "COMPUTED", action == "lock" && r.status != "APPROVED":
		return refuse(http.StatusConflict, "INVALID_STATE", "the run isn't at the step this action needs")
	case action == "approve" && f.runConfig().SeparationOfDuties && r.preparedBy == acting:
		return refuse(http.StatusForbidden, "SOD_VIOLATION", "they prepared this run")
	case action == "lock":
		var none []string
		for _, l := range r.lines {
			for _, x := range f.rs().workers {
				if x.ID == l["employee_id"] && x.NoAccount {
					none = append(none, x.ID)
				}
			}
		}
		if len(none) > 0 {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnprocessableEntity)
			_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]any{"code": "NO_PAYOUT_DESTINATION", "message": "someone has no account to be paid into",
				"details": map[string]any{"employee_ids": none}}})
			return true
		}
	}
	return false
}

func (f *Fake) startRunCode(w http.ResponseWriter, r *fakeRun, acting string, in map[string]any) {
	action, _ := in["action"].(string)
	if f.refuse(w, r, action, acting) {
		return
	}
	a := f.linked(acting)
	now := f.Now()
	recent := slices.DeleteFunc(slices.Clone(f.sent[acting]), func(t time.Time) bool { return now.Sub(t) >= codeWindow })
	if len(recent) >= codeLimit {
		writeErr(w, http.StatusTooManyRequests, "TOO_MANY_CODES", "more than 5 codes in 10 minutes")
		return
	}
	f.sent[acting] = append(recent, now)
	f.n++
	ch := &challenge{id: fmt.Sprintf("20000000-0000-4000-8000-%012d", f.n), user: a.UserID, acting: acting, code: fmt.Sprintf("%06d", 610000+f.n), expires: now.Add(codeTTL)}
	f.challenges[ch.id] = ch
	f.rs().codes[ch.id] = runChallenge{run: r.id, action: action}
	writeData(w, http.StatusAccepted, map[string]any{"challenge": map[string]any{"challenge_id": ch.id, "channel": a.Channel, "destination_hint": a.Hint, "expires_at": ch.expires.UTC().Format(time.RFC3339)}})
}

func payment(r *fakeRun) map[string]any {
	p := map[string]any{"status": "NOT_SCHEDULED", "total_kobo": r.net, "paid": 0, "failed": 0, "returned": 0, "pending": r.count}
	if r.status == "LOCKED" {
		p["status"], p["scheduled_for"] = "SCHEDULED", r.payDate
	}
	return p
}

func (f *Fake) decide(w http.ResponseWriter, c *Conn, r *fakeRun, action, acting, name string, in map[string]any) {
	id, _ := in["challenge_id"].(string)
	code, _ := in["code"].(string)
	ch, rc := f.challenges[id], f.rs().codes[id]
	switch {
	case ch == nil || ch.acting != acting || rc.run != r.id || rc.action != action:
		writeErr(w, http.StatusForbidden, "CODE_INVALID", "no such code for this action")
		return
	case ch.used:
		writeErr(w, http.StatusForbidden, "CODE_USED", "that code was already used")
		return
	case !f.Now().Before(ch.expires):
		writeErr(w, http.StatusForbidden, "CODE_EXPIRED", "that code has expired")
		return
	case ch.tries >= codeTries:
		writeErr(w, http.StatusForbidden, "CODE_LOCKED", "too many attempts")
		return
	case code != ch.code:
		ch.tries++
		if ch.tries >= codeTries {
			writeErr(w, http.StatusForbidden, "CODE_LOCKED", "too many attempts")
			return
		}
		writeErr(w, http.StatusForbidden, "CODE_INVALID", "wrong code")
		return
	}
	if f.refuse(w, r, action, acting) {
		return
	}
	ch.used = true
	a := f.linked(acting)
	by := map[string]any{"user_id": a.UserID, "name": a.Name}
	_ = name
	if action == "approve" {
		r.status, r.approved, r.updated = "APPROVED", f.Now(), f.Now()
		f.emit(c, "run.approved", map[string]any{"run": r.summary(), "approved_by": a.Name})
		f.emit(c, "approval.recorded", map[string]any{"subject": "run", "subject_id": r.id, "action": "approved", "by": by, "via": "connect", "app_name": "Taskiem", "run": r.summary()})
	} else {
		r.status, r.locked, r.updated = "LOCKED", f.Now(), f.Now()
		f.emit(c, "run.locked", map[string]any{"run": r.summary()})
		f.emit(c, "approval.recorded", map[string]any{"subject": "run", "subject_id": r.id, "action": "locked", "by": by, "via": "connect", "app_name": "Taskiem", "run": r.summary()})
		f.emit(c, "run.payment_scheduled", map[string]any{"run": r.summary(), "scheduled_for": r.payDate, "total_net_kobo": r.net})
	}
	writeData(w, http.StatusOK, map[string]any{"run": r.detail(), "payment": payment(r)})
}

// RunRoutes are the run routes ServeRuns adds, as METHOD /path with
// Payrolla's own parameter names, for checking against its OpenAPI file.
func RunRoutes() []string {
	return []string{
		"GET /connect/runs", "POST /connect/runs", "GET /connect/runs/{runID}", "GET /connect/runs/{runID}/lines",
		"GET /connect/runs/{runID}/adjustments", "GET /connect/runs/{runID}/exclusions",
		"POST /connect/runs/{runID}/lines/{employeeID}/adjustments", "DELETE /connect/runs/{runID}/adjustments/{adjustmentID}",
		"POST /connect/runs/{runID}/lines/{employeeID}/exclude", "DELETE /connect/runs/{runID}/exclusions/{exclusionID}",
		"POST /connect/runs/{runID}/compute", "POST /connect/runs/{runID}/cancel",
		"POST /connect/runs/{runID}/confirmations", "POST /connect/runs/{runID}/approve", "POST /connect/runs/{runID}/lock",
		"POST /connect/runs/{runID}/reject", "GET /connect/funding", "GET /connect/employees/{employeeID}",
	}
}

// RunErrorCodes are the error codes the run routes answer with.
func RunErrorCodes() []string {
	return []string{"INVALID_STATE", "DUPLICATE_RUN", "INVALID_PERIOD", "INVALID_BODY", "REASON_REQUIRED", "UNKNOWN_FIELD", "ALREADY_EXCLUDED",
		"APPROVER_NOT_LINKED", "NOT_AN_APPROVER", "PAYMENT_CONFIRMATION_NOT_SET_UP", "SOD_VIOLATION", "NO_PAYOUT_DESTINATION",
		"TOO_MANY_CODES", "CODE_INVALID", "CODE_USED", "CODE_EXPIRED", "CODE_LOCKED", "SCOPE_REQUIRED", "NOT_FOUND"}
}
