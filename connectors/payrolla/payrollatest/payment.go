package payrollatest

import (
	"net/http"
	"slices"
	"strings"
	"time"
)

// After locking (P4b-4), switched on with ServeRuns: a locked run's
// payment (GET /connect/runs/{id}/payment), paid on the pay date
// (PayDay), short of money (AWAITING_FUNDS and run.awaiting_funds, paid
// once the wallet covers it), paid early with a code (pay_now) and its
// failed payments sent again with a code (retry_failed), each person's
// payout succeeding, failing (FailPayout) or returned by the bank
// (Reverse), and payslips as data, final once the run is locked
// (PAYSLIP_NOT_FINAL before), with an employee's own list and the 7-day
// link. Payrolla's rules: paying needs a linked person who may pay
// (CANNOT_PAY); pay_now is refused while the payment waits for funds
// (AWAITING_FUNDS) or has started (ALREADY_PAYING); retry_failed with
// nothing failed is INVALID_STATE (NOTHING_TO_RETRY in Payrolla's reply,
// not its OpenAPI file); returned payments are not resent.

type payState struct {
	status  string // SCHEDULED, AWAITING_FUNDS, PAYING, COMPLETED, PARTIALLY_COMPLETED
	fail    map[string]string
	started bool
}

var payStates = map[*fakeRun]*payState{}

// pay is a run's payment state (f.mu held).
func (f *Fake) pay(r *fakeRun) *payState {
	statesMu.Lock()
	defer statesMu.Unlock()
	p := payStates[r]
	if p == nil {
		p = &payState{fail: map[string]string{}}
		payStates[r] = p
	}
	return p
}

var failNext = map[*Fake]map[string]string{}

// FailPayout makes the next payout to an employee fail, with Payrolla's
// reason ("Account closed").
func (f *Fake) FailPayout(employee, reason string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	statesMu.Lock()
	defer statesMu.Unlock()
	if failNext[f] == nil {
		failNext[f] = map[string]string{}
	}
	failNext[f][employee] = reason
}

func (f *Fake) takeFailure(employee string) string {
	statesMu.Lock()
	defer statesMu.Unlock()
	reason := failNext[f][employee]
	delete(failNext[f], employee)
	return reason
}

func (f *Fake) servePayment() {
	f.Handle(http.MethodGet, "/connect/payslips/", f.payslipRoute)
	f.Handle(http.MethodPost, "/connect/payslips/", f.payslipRoute)
}

// paymentOf is a run's payment as Payrolla shows it (f.mu held).
func (f *Fake) paymentOf(r *fakeRun) map[string]any {
	out := map[string]any{"status": "NOT_SCHEDULED", "total_kobo": r.net, "paid": 0, "failed": 0, "returned": 0, "pending": 0}
	locked := r.status == "LOCKED" || r.status == "DISBURSING" || r.status == "COMPLETED" || r.status == "PARTIALLY_COMPLETED"
	if !locked {
		out["pending"] = r.count
		return out
	}
	p := f.pay(r)
	st := p.status
	if st == "" {
		st = "SCHEDULED"
	}
	out["status"], out["scheduled_for"] = st, r.payDate
	paid, failed, returned, pending := 0, 0, 0, 0
	for _, l := range r.lines {
		switch l["disbursement_status"] {
		case "CONFIRMED":
			paid++
		case "FAILED":
			failed++
		case "REVERSED":
			returned++
		default:
			pending++
		}
	}
	out["paid"], out["failed"], out["returned"], out["pending"] = paid, failed, returned, pending
	return out
}

// PaymentStatus is a run's payment status now.
func (f *Fake) PaymentStatus(run string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if r := f.run(run); r != nil {
		return f.paymentOf(r)["status"].(string)
	}
	return ""
}

// PayDay is the run's pay date coming: Payrolla pays everyone, or waits
// for money when the wallet is short (run.awaiting_funds, each time it
// looks).
func (f *Fake) PayDay(run string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	r := f.run(run)
	if r == nil || r.status != "LOCKED" {
		return
	}
	f.disburse(r, f.conns0())
}

// conns0 is the connection the run's events go to: the one that locked
// it, as the queue has it, else any.
func (f *Fake) conns0() *Conn {
	for _, q := range f.rs().queue {
		return q.conn
	}
	for _, c := range f.conns {
		if !c.Ended {
			return c
		}
	}
	for _, c := range f.keys {
		return c
	}
	return nil
}

var runConn = map[*fakeRun]*Conn{}

// disburse pays a locked run's people, or waits for money (f.mu held).
func (f *Fake) disburse(r *fakeRun, c *Conn) {
	statesMu.Lock()
	if rc := runConn[r]; rc != nil {
		c = rc
	}
	statesMu.Unlock()
	p := f.pay(r)
	cfg := f.runConfig()
	if !cfg.HasWallet || cfg.WalletKobo < r.net {
		p.status = "AWAITING_FUNDS"
		f.emit(c, "run.awaiting_funds", map[string]any{"run": r.summary(), "needed_kobo": r.net, "available_kobo": cfg.WalletKobo, "short_by_kobo": r.net - cfg.WalletKobo})
		return
	}
	p.status, r.status = "PAYING", "DISBURSING"
	f.emit(c, "run.disbursement_started", map[string]any{"run": r.summary(), "wallet_count": 0, "bank_count": r.count})
	f.payLines(r, c, func(map[string]any) bool { return true })
}

// payLines sends the payouts of the lines chosen and settles the run.
func (f *Fake) payLines(r *fakeRun, c *Conn, which func(map[string]any) bool) {
	cfg := f.runConfig()
	for _, l := range r.lines {
		if !which(l) {
			continue
		}
		emp, _ := l["employee_id"].(string)
		net, _ := l["net_kobo"].(int64)
		data := map[string]any{"run_id": r.id, "payslip_id": l["payslip_id"], "employee_id": emp, "amount_kobo": net, "method": "BANK", "reference": "NIP-" + f.id()[:8]}
		if reason := f.takeFailure(emp); reason != "" {
			l["disbursement_status"], l["reason"] = "FAILED", reason
			data["reason"] = reason
			f.emit(c, "payout.failed", data)
			continue
		}
		cfg.WalletKobo -= net
		l["disbursement_status"] = "CONFIRMED"
		delete(l, "reason")
		f.emit(c, "payout.succeeded", data)
	}
	f.settle(r, c)
}

func (f *Fake) settle(r *fakeRun, c *Conn) {
	p := f.pay(r)
	paid, failed := 0, 0
	for _, l := range r.lines {
		switch l["disbursement_status"] {
		case "CONFIRMED":
			paid++
		case "FAILED", "REVERSED":
			failed++
		}
	}
	typ := "run.completed"
	p.status, r.status = "COMPLETED", "COMPLETED"
	if failed > 0 {
		typ = "run.partially_completed"
		p.status, r.status = "PARTIALLY_COMPLETED", "PARTIALLY_COMPLETED"
	}
	r.updated = f.Now()
	f.emit(c, typ, map[string]any{"run": r.summary(), "paid_count": paid, "failed_count": failed})
	if !p.started {
		p.started = true
		period := periodOf(r.start)
		for _, l := range r.lines {
			f.emit(c, "payslip.issued", map[string]any{"payslip_id": l["payslip_id"], "employee_id": l["employee_id"], "run_id": r.id, "period": period})
		}
	}
}

func periodOf(start string) string {
	t, err := time.Parse("2006-01-02", start)
	if err != nil {
		return start
	}
	return t.Format("January 2006")
}

// TopUp adds money to the wallet; a payment waiting for funds is paid
// at once, as Payrolla does.
func (f *Fake) TopUp(kobo int64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.runConfig().WalletKobo += kobo
	for _, r := range f.rs().runs {
		if r.status == "LOCKED" && f.pay(r).status == "AWAITING_FUNDS" {
			f.disburse(r, f.conns0())
		}
	}
}

// Reverse is a bank returning one person's payout after it succeeded.
func (f *Fake) Reverse(run, employee string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	r := f.run(run)
	if r == nil {
		return
	}
	c := f.conns0()
	for _, l := range r.lines {
		if l["employee_id"] == employee && l["disbursement_status"] == "CONFIRMED" {
			l["disbursement_status"], l["reason"] = "REVERSED", "Returned by the bank"
			f.emit(c, "payout.reversed", map[string]any{"run_id": r.id, "payslip_id": l["payslip_id"], "employee_id": employee, "amount_kobo": l["net_kobo"], "method": "BANK", "reference": ""})
		}
	}
	f.settle(r, c)
}

// payRefuse says why the acting person may not pay a run now (pay_now,
// retry_failed), and whether it did.
func (f *Fake) payRefuse(w http.ResponseWriter, r *fakeRun, action, acting string) bool {
	a := f.linked(acting)
	refuse := func(status int, code, msg string) bool {
		writeErr(w, status, code, msg)
		return true
	}
	switch {
	case a == nil:
		return refuse(http.StatusForbidden, "APPROVER_NOT_LINKED", "link the acting person first")
	case !a.CanPay:
		return refuse(http.StatusForbidden, "CANNOT_PAY", "their role doesn't allow paying")
	case !a.CanConfirm:
		return refuse(http.StatusForbidden, "PAYMENT_CONFIRMATION_NOT_SET_UP", "they have no factor to receive codes on")
	}
	st := f.paymentOf(r)["status"]
	if action == "pay_now" {
		switch {
		case st == "AWAITING_FUNDS":
			return refuse(http.StatusConflict, "AWAITING_FUNDS", "the payment waits for funds")
		case st == "PAYING" || st == "COMPLETED" || st == "PARTIALLY_COMPLETED":
			return refuse(http.StatusConflict, "ALREADY_PAYING", "the payment has started")
		case st != "SCHEDULED":
			return refuse(http.StatusConflict, "INVALID_STATE", "the run isn't locked")
		}
		return false
	}
	if !slices.ContainsFunc(r.lines, func(l map[string]any) bool { return l["disbursement_status"] == "FAILED" }) {
		// NOTHING_TO_RETRY is in Payrolla's Phase B reply but not its
		// OpenAPI file, which answers a run with nothing failed as any
		// run not at the step: INVALID_STATE. Taskiem says "There are no
		// failed payments to send again" for either.
		return refuse(http.StatusConflict, "INVALID_STATE", "no failed payments")
	}
	return false
}

// decidePay pays a run now or sends its failed payments again, with the
// code (f.mu held).
func (f *Fake) decidePay(w http.ResponseWriter, c *Conn, r *fakeRun, action, acting string, in map[string]any) {
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
		writeErr(w, http.StatusForbidden, "CODE_INVALID", "wrong code")
		return
	}
	if f.payRefuse(w, r, action, acting) {
		return
	}
	ch.used = true
	if action == "pay_now" {
		f.disburse(r, c)
	} else {
		r.status = "DISBURSING"
		f.pay(r).status = "PAYING"
		f.payLines(r, c, func(l map[string]any) bool { return l["disbursement_status"] == "FAILED" })
	}
	writeData(w, http.StatusOK, map[string]any{"run": r.detail(), "payment": f.paymentOf(r)})
}

func finalRun(r *fakeRun) bool {
	return r.status == "LOCKED" || r.status == "DISBURSING" || r.status == "COMPLETED" || r.status == "PARTIALLY_COMPLETED"
}

// payslipOf is a line as a final payslip (f.mu held).
func (f *Fake) payslipOf(r *fakeRun, l map[string]any) map[string]any {
	return map[string]any{"payslip_id": l["payslip_id"], "run_id": r.id, "period_start": r.start, "period_end": r.end, "pay_date": r.payDate,
		"employee":   map[string]any{"id": l["employee_id"], "first_name": l["first_name"], "last_name": l["last_name"], "department": "Sales"},
		"employer":   map[string]any{"name": f.CompanyName, "rc_number": "RC1234567"},
		"gross_kobo": l["gross_kobo"], "total_earnings_kobo": l["total_earnings_kobo"], "total_deductions_kobo": l["total_deductions_kobo"],
		"net_kobo": l["net_kobo"], "items": l["items"], "payment_status": l["disbursement_status"]}
}

var linksOff = map[*Fake]bool{}

// PayslipLinksOff is the owner not allowing payslip links in Payrolla.
func (f *Fake) PayslipLinksOff(off bool) {
	statesMu.Lock()
	defer statesMu.Unlock()
	linksOff[f] = off
}

// payslipRoute is GET /connect/payslips/{id} and POST …/{id}/link.
func (f *Fake) payslipRoute(w http.ResponseWriter, req *http.Request, c *Conn) {
	f.mu.Lock()
	defer f.mu.Unlock()
	parts := strings.Split(strings.Trim(strings.TrimPrefix(req.URL.Path, "/api/v1"), "/"), "/") // connect payslips {id} [link]
	link := len(parts) == 4 && parts[3] == "link" && req.Method == http.MethodPost
	scope := "payslips:read"
	if link {
		scope = "payslips:links"
	}
	if !has(c, scope) {
		writeErr(w, http.StatusForbidden, "SCOPE_REQUIRED", "this connection needs "+scope)
		return
	}
	if len(parts) < 3 || !link && len(parts) != 3 {
		writeErr(w, http.StatusNotFound, "NOT_FOUND", "no such route")
		return
	}
	for _, r := range f.rs().runs {
		for _, l := range r.lines {
			if l["payslip_id"] != parts[2] {
				continue
			}
			if !finalRun(r) {
				writeErr(w, http.StatusConflict, "PAYSLIP_NOT_FINAL", "the run is not locked yet")
				return
			}
			if link {
				statesMu.Lock()
				off := linksOff[f]
				statesMu.Unlock()
				if off {
					writeErr(w, http.StatusForbidden, "PAYSLIP_LINKS_DISABLED", "the owner hasn't allowed payslip links")
					return
				}
				writeData(w, http.StatusCreated, map[string]any{"url": "https://payroller.test/p/" + f.id(), "expires_at": f.Now().Add(7 * 24 * time.Hour).UTC().Format(time.RFC3339)})
				return
			}
			writeData(w, http.StatusOK, map[string]any{"payslip": f.payslipOf(r, l)})
			return
		}
	}
	writeErr(w, http.StatusNotFound, "NOT_FOUND", "no such payslip")
}

// employeePayslips is GET /connect/employees/{id}/payslips: the final
// ones, newest first (f.mu held by the caller).
func (f *Fake) employeePayslips(w http.ResponseWriter, c *Conn, employee string) {
	if !has(c, "payslips:read") {
		writeErr(w, http.StatusForbidden, "SCOPE_REQUIRED", "this connection needs payslips:read")
		return
	}
	list := []map[string]any{}
	runs := slices.Clone(f.rs().runs)
	slices.SortFunc(runs, func(a, b *fakeRun) int { return strings.Compare(b.start, a.start) })
	for _, r := range runs {
		if !finalRun(r) {
			continue
		}
		for _, l := range r.lines {
			if l["employee_id"] == employee {
				list = append(list, map[string]any{"payslip_id": l["payslip_id"], "run_id": r.id, "period": r.start[:7], "pay_date": r.payDate + "T00:00:00Z",
					"gross_kobo": l["gross_kobo"], "net_kobo": l["net_kobo"], "disbursement_status": l["disbursement_status"]})
			}
		}
	}
	writeData(w, http.StatusOK, map[string]any{"payslips": list})
}

// PaymentRoutes are the routes after locking, as METHOD /path with
// Payrolla's own parameter names, for checking against its OpenAPI file.
func PaymentRoutes() []string {
	return []string{
		"GET /connect/runs/{runID}/payment", "POST /connect/runs/{runID}/pay-now", "POST /connect/runs/{runID}/retry-failed",
		"GET /connect/payslips/{payslipID}", "POST /connect/payslips/{payslipID}/link", "GET /connect/employees/{employeeID}/payslips",
	}
}

// PaymentErrorCodes are the error codes after locking.
func PaymentErrorCodes() []string {
	return []string{"CANNOT_PAY", "AWAITING_FUNDS", "ALREADY_PAYING", "PAYSLIP_NOT_FINAL", "PAYSLIP_LINKS_DISABLED"}
}
