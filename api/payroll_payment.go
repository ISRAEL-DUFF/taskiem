package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/connectors/payrolla"
	"github.com/israel-duff/taskiem/engine/connector"
	"github.com/israel-duff/taskiem/engine/invoicing"
	"github.com/israel-duff/taskiem/engine/lang"
	"github.com/israel-duff/taskiem/engine/pdf"
)

// After locking (P4b-4 of docs/spec/chat-pay-people.md; decision 0074):
//
//   - the run's Payment: Pays on 28 Nov, Waiting for money (short by
//     ₦120,000), Paying, Paid, or Paid, 2 not paid; each person paid, not
//     paid with Payrolla's reason, or returned by the bank (the run's
//     lines, payroll.go);
//   - Pay now before the date and Pay the failed ones again, each with
//     Payrolla's code (payroll.approve, and Payrolla's can_pay): the code
//     sheet and the chat's code-in-the-thread of P4b-3, with the actions
//     pay_now and retry_failed;
//   - payslips as Taskiem's own cards, the full breakdown, read from
//     Payrolla when opened and kept nowhere: anyone's from the run for
//     payroll.read only (preparing or approving is not enough), a linked
//     employee's own on My pay; Save as PDF, made when asked with the
//     business's name and not kept; Payrolla's 7-day link for someone not
//     on Taskiem.
//
// What it tells (payroll_payment_chat.go): the shortfall on the pay date
// (run.awaiting_funds, once a run), the morning warnings three days and
// one day before the pay date, who was paid and who not, and each linked
// employee's "Your payslip for November is ready".

func (s *Server) payrollPaymentRoutes(r chi.Router) {
	r.With(s.need(PermPayrollApprove), s.tenantWide).Post("/payroll/runs/{run}/pay-now", s.decidePayrollRun(payrolla.ActionPayNow))
	r.With(s.need(PermPayrollApprove), s.tenantWide).Post("/payroll/runs/{run}/retry-failed", s.decidePayrollRun(payrolla.ActionRetryFailed))
	// Anyone's payslip: payroll.read only.
	r.With(s.need(PermPayrollRead)).Get("/payroll/payslips/{payslip}", s.getPayslip)
	r.With(s.need(PermPayrollRead)).Get("/payroll/payslips/{payslip}/pdf", s.getPayslipPDF)
	r.With(s.need(PermPayrollRead), s.tenantWide).Post("/payroll/payslips/{payslip}/link", s.payslipLink)
	// One's own, as the employee linked to it.
	r.Get("/payroll/me/payslips", s.myPayslips)
	r.Get("/payroll/me/payslips/{payslip}", s.myPayslip)
	r.Get("/payroll/me/payslips/{payslip}/pdf", s.myPayslipPDF)
}

// retryNothing is Payrolla's refusal of sending again a run with nothing
// failed: NOTHING_TO_RETRY in its Phase B reply, INVALID_STATE in its
// OpenAPI file. Either is said "There are no failed payments to send
// again."
func retryNothing(action string, err error) error {
	if action == payrolla.ActionRetryFailed && payrolla.Code(err) == "INVALID_STATE" {
		return &payrolla.Error{Status: http.StatusConflict, Code: "NOTHING_TO_RETRY", Message: "no failed payments"}
	}
	return err
}

// paymentSays says a refusal after locking in words.
func (s *Server) paymentSays(ctx context.Context, l lang.Tag, cl *payrolla.Client, run, code string) error {
	refuse := func(c, id string, kv ...string) error {
		return runRefused{status: http.StatusConflict, code: c, msg: tr(l, id, kv...)}
	}
	switch code {
	case "AWAITING_FUNDS":
		if short := s.shortBy(ctx, cl, run); short > 0 {
			return runRefused{status: http.StatusConflict, code: "awaiting_funds", msg: tr(l, "payroll.refused.awaiting_funds", "short", invoicing.Naira(short)),
				extra: map[string]any{"short_by_kobo": short}}
		}
		return refuse("awaiting_funds", "payroll.refused.awaiting_funds_some")
	case "ALREADY_PAYING":
		return refuse("already_paying", "payroll.refused.already_paying")
	case "NOTHING_TO_RETRY":
		return refuse("nothing_to_retry", "payroll.refused.nothing_to_retry")
	case "PAYSLIP_NOT_FINAL":
		return refuse("payslip_not_final", "payroll.refused.payslip_not_final")
	}
	return refuse("payslip_links_off", "payroll.refused.payslip_links_off")
}

// shortBy is how much the wallet lacks for a run's total, or 0.
func (s *Server) shortBy(ctx context.Context, cl *payrolla.Client, run string) int64 {
	if cl == nil || run == "" {
		return 0
	}
	r, err := cl.Run(ctx, run)
	if err != nil {
		return 0
	}
	f, err := cl.Funding(ctx)
	if err != nil || r.TotalNetKobo <= f.AvailableKobo {
		return 0
	}
	return r.TotalNetKobo - f.AvailableKobo
}

// paidNowWords says what Pay now or paying again did.
func paidNowWords(l lang.Tag, action string, d payrolla.Decided) string {
	if action == payrolla.ActionRetryFailed {
		return tr(l, "payroll.retried")
	}
	if d.Payment.Status == "AWAITING_FUNDS" {
		return tr(l, "payroll.refused.awaiting_funds_some")
	}
	return tr(l, "payroll.paid_now", "people", peopleWords(l, d.Run.EmployeeCount), "amount", invoicing.Naira(d.Run.TotalNetKobo))
}

// paymentWords is a payment's state in words, and its tone (ok, warn,
// bad, info): "Pays on 28 Nov", "Waiting for money: short by ₦120,000",
// "Paying", "Paid", "Paid, 2 not paid", "Not paid".
func paymentWords(l lang.Tag, pay payrolla.Payment, payDate string, short int64) (string, string) {
	switch pay.Status {
	case "SCHEDULED":
		d := pay.ScheduledFor
		if d == "" {
			d = payDate
		}
		return tr(l, "payroll.pays_on", "date", dayWords(d)), "info"
	case "AWAITING_FUNDS":
		if short > 0 {
			return tr(l, "payroll.pay.awaiting", "short", invoicing.Naira(short)), "warn"
		}
		return tr(l, "payroll.pay.awaiting_any"), "warn"
	case "PAYING":
		return tr(l, "payroll.pay.paying"), "info"
	case "COMPLETED":
		return tr(l, "payroll.pay.paid"), "ok"
	case "PARTIALLY_COMPLETED":
		return tr(l, "payroll.pay.paid_some", "n", fmt.Sprint(pay.Failed+pay.Returned)), "warn"
	case "FAILED":
		return tr(l, "payroll.pay.failed"), "bad"
	}
	return "", ""
}

// lockedStatus is a run past locking, whose payment there is to show.
func lockedStatus(st string) bool {
	switch st {
	case "LOCKED", "DISBURSING", "COMPLETED", "PARTIALLY_COMPLETED", "FAILED":
		return true
	}
	return false
}

// canPay says whether p is linked to a Payrolla user who may pay.
func canPay(ctx context.Context, p *Principal, cl *payrolla.Client) bool {
	list, err := cl.Approvers(ctx)
	if err != nil {
		return false
	}
	for _, a := range list {
		if a.LinkedUser() == p.UserID && a.CanPay {
			return true
		}
	}
	return false
}

// paymentMore adds a locked run's Payment to its page: the state in
// words, the counts, what this person may do (Pay now, Pay the failed
// ones again, open payslips).
func (s *Server) paymentMore(ctx context.Context, p *Principal, cl *payrolla.Client, r payrolla.Run, pic runPicture, out map[string]any) {
	if !lockedStatus(r.Status) {
		return
	}
	pay, err := cl.RunPayment(ctx, r.ID)
	if err != nil {
		return
	}
	var short int64
	if pay.Status == "AWAITING_FUNDS" {
		short = s.shortBy(ctx, cl, r.ID)
	}
	words, tone := paymentWords(lang.EN, pay, r.PayDate, short)
	pays := pic.May.Approve && canPay(ctx, p, cl)
	retry := pays && pay.Failed > 0 && (pay.Status == "COMPLETED" || pay.Status == "PARTIALLY_COMPLETED")
	out["payment"] = map[string]any{"status": pay.Status, "words": words, "tone": tone, "scheduled_for": pay.ScheduledFor, "total_kobo": pay.TotalKobo,
		"paid": pay.Paid, "failed": pay.Failed, "returned": pay.Returned, "pending": pay.Pending, "short_by_kobo": short,
		"may_pay_now": pays && pay.Status == "SCHEDULED", "may_retry": retry, "may_payslips": p.Can(PermPayrollRead),
		// Approvers who may not pay are told why before any code.
		"cannot_pay": pic.May.Approve && pic.May.Linked && !pays}
}

// --- payslips ---

// payslipCard is a payslip as Taskiem's own card shows it: the full
// breakdown, the employer and the payment.
type payslipCard struct {
	ID          string         `json:"payslip_id"`
	RunID       string         `json:"run_id"`
	Period      string         `json:"period"`
	Month       string         `json:"month"`
	PeriodStart string         `json:"period_start"`
	PayDate     string         `json:"pay_date"`
	Employee    map[string]any `json:"employee"`
	Employer    map[string]any `json:"employer"`
	Gross       int64          `json:"gross_kobo"`
	Earnings    int64          `json:"total_earnings_kobo"`
	Deductions  int64          `json:"total_deductions_kobo"`
	Net         int64          `json:"net_kobo"`
	Items       []payrollItem  `json:"items"`
	Payment     map[string]any `json:"payment"`
}

// payslipState is a payslip's payment in plain terms and words.
func payslipState(l lang.Tag, status string) (string, string) {
	switch status {
	case "CONFIRMED":
		return "paid", tr(l, "payroll.slip.paid")
	case "SENT":
		return "waiting", tr(l, "payroll.slip.sent")
	case "FAILED", "PERMANENTLY_FAILED":
		return "failed", tr(l, "payroll.slip.failed")
	case "REVERSED":
		return "returned", tr(l, "payroll.slip.returned")
	}
	return "waiting", tr(l, "payroll.slip.waiting")
}

func monthOf(start string) string {
	t, err := time.Parse("2006-01-02", start)
	if err != nil {
		return ""
	}
	return monthNames[t.Month()-1]
}

func payslipOf(l lang.Tag, ps payrolla.Payslip) payslipCard {
	items := []payrollItem{}
	for _, it := range ps.Items {
		items = append(items, payrollItem{Kind: itemKind(it.Category), Code: it.Code, Label: itemLabel(it.Code, it.Label), AmountKobo: it.AmountKobo})
	}
	state, words := payslipState(l, ps.PaymentStatus)
	return payslipCard{ID: ps.ID, RunID: ps.RunID, Period: periodWords(ps.PeriodStart, ps.PeriodEnd), Month: monthOf(ps.PeriodStart), PeriodStart: ps.PeriodStart,
		PayDate: ps.PayDate, Employee: map[string]any{"id": ps.Employee.ID, "name": strings.TrimSpace(ps.Employee.FirstName + " " + ps.Employee.LastName), "department": ps.Employee.Department},
		Employer: map[string]any{"name": ps.Employer.Name, "rc_number": ps.Employer.RCNumber}, Gross: ps.GrossKobo, Earnings: ps.EarningsKobo,
		Deductions: ps.DeductionsKobo, Net: ps.NetKobo, Items: items, Payment: map[string]any{"status": ps.PaymentStatus, "state": state, "words": words}}
}

func payslipParam(r *http.Request) (string, bool) {
	id := strings.TrimSpace(chi.URLParam(r, "payslip"))
	_, err := uuid.Parse(id)
	return id, err == nil
}

// readPayslip reads one payslip for p; own, when set, is the employee it
// must be (a linked employee's own), and a payslip of anyone else is not
// there for them.
func (s *Server) readPayslip(ctx context.Context, p *Principal, id, own string) (payrolla.Payslip, error) {
	cl, conn, err := s.payrollaClient(ctx, p, "payroll.payslip")
	if err != nil {
		return payrolla.Payslip{}, s.payrollaSays(ctx, p, conn, err)
	}
	ps, err := cl.Payslip(ctx, id)
	if errors.Is(err, connector.ErrNotFound) {
		return ps, pgx.ErrNoRows
	}
	if err != nil {
		return ps, s.runSays(ctx, lang.EN, p, conn, cl, "", err)
	}
	if own != "" && ps.Employee.ID != own {
		return payrolla.Payslip{}, pgx.ErrNoRows
	}
	return ps, nil
}

// getPayslip is GET /v1/payroll/payslips/{payslip} (payroll.read): anyone's
// payslip, opened from the run. Who opened it is recorded, with no pay.
func (s *Server) getPayslip(w http.ResponseWriter, r *http.Request) {
	s.payslipAnswer(w, r, false, false)
}

// getPayslipPDF is GET /v1/payroll/payslips/{payslip}/pdf (payroll.read).
func (s *Server) getPayslipPDF(w http.ResponseWriter, r *http.Request) {
	s.payslipAnswer(w, r, false, true)
}

// myPayslip is GET /v1/payroll/me/payslips/{payslip}: a linked employee's
// own; anyone else's is not there.
func (s *Server) myPayslip(w http.ResponseWriter, r *http.Request) {
	s.payslipAnswer(w, r, true, false)
}

// myPayslipPDF is GET /v1/payroll/me/payslips/{payslip}/pdf.
func (s *Server) myPayslipPDF(w http.ResponseWriter, r *http.Request) {
	s.payslipAnswer(w, r, true, true)
}

func (s *Server) payslipAnswer(w http.ResponseWriter, r *http.Request, own, asPDF bool) {
	ctx := r.Context()
	p := principalFrom(ctx)
	id, ok := payslipParam(r)
	if !ok {
		s.fail(w, r, pgx.ErrNoRows)
		return
	}
	emp := ""
	if own {
		var err error
		if emp, err = s.myEmployee(ctx, p); err != nil || emp == "" {
			s.fail(w, r, errors.Join(err, pgx.ErrNoRows))
			return
		}
	}
	ps, err := s.readPayslip(ctx, p, id, emp)
	if err != nil {
		s.runFail(w, r, err)
		return
	}
	card := payslipOf(lang.EN, ps)
	if !own {
		action := "payroll.payslip_view"
		if asPDF {
			action = "payroll.payslip_pdf"
		}
		if err := s.tx(r, func(tx pgx.Tx) error {
			return auditTx(r, tx, action, "payrolla-payslip:"+ps.ID, map[string]any{"run": ps.RunID, "employee_id": ps.Employee.ID})
		}); err != nil {
			s.Logger.Warn("payroll: auditing a payslip", "err", err)
		}
	}
	if !asPDF {
		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, http.StatusOK, map[string]any{"payslip": card})
		return
	}
	business := s.businessName(ctx, p.TenantID)
	b, err := payslipPDF(business, card)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "application/pdf")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="payslip-%s.pdf"`, strings.ReplaceAll(strings.ToLower(card.Period), " ", "-")))
	_, _ = w.Write(b) //nolint:gosec // G705: a PDF Taskiem drew, served as application/pdf, not HTML
}

func (s *Server) businessName(ctx context.Context, tenant uuid.UUID) string {
	var name string
	_ = s.tenantTx(ctx, tenant, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT name FROM tenants WHERE id = $1`, tenant).Scan(&name)
	})
	return name
}

// payslipPDF makes a payslip's PDF with the business's name. It is made
// when asked and not kept.
func payslipPDF(business string, c payslipCard) ([]byte, error) {
	d, err := pdf.New("Payslip " + c.Period)
	if err != nil {
		return nil, err
	}
	const left, right = 32.0, pdf.A5Width - 32
	y := 48.0
	line := func(label, amount string, bold bool) {
		if y > pdf.A5Height-48 {
			d.NewPage()
			y = 48
		}
		for i, part := range d.Wrap(label, 10, bold, right-left-110) {
			d.Text(left, y, 10, part, bold)
			if i == 0 {
				d.TextRight(right, y, 10, amount, bold)
			}
			y += 14
		}
	}
	for _, part := range d.Wrap(business, 16, true, right-left) {
		d.Text(left, y, 16, part, true)
		y += 20
	}
	d.Text(left, y, 11, "Payslip for "+c.Period, false)
	y += 16
	employer, _ := c.Employer["name"].(string)
	rc, _ := c.Employer["rc_number"].(string)
	if employer != "" && employer != business {
		d.Text(left, y, 9, "Employer: "+employer, false)
		y += 12
	}
	if rc != "" {
		d.Text(left, y, 9, "RC number: "+rc, false)
		y += 12
	}
	name, _ := c.Employee["name"].(string)
	dept, _ := c.Employee["department"].(string)
	y += 6
	d.Text(left, y, 11, name, true)
	y += 14
	if dept != "" {
		d.Text(left, y, 9, dept, false)
		y += 12
	}
	if c.PayDate != "" {
		d.Text(left, y, 9, "Pay day: "+dayWords(c.PayDate), false)
		y += 12
	}
	y += 8
	d.Line(left, y, right, y, 0.5)
	y += 16
	for _, it := range c.Items {
		if it.Kind == "earning" {
			line(it.Label, invoicing.Naira(it.AmountKobo), false)
		}
	}
	line("Gross pay", invoicing.Naira(c.Earnings), true)
	y += 4
	for _, it := range c.Items {
		if it.Kind == "deduction" {
			line(it.Label, "−"+invoicing.Naira(it.AmountKobo), false)
		}
	}
	line("Total taken", "−"+invoicing.Naira(c.Deductions), true)
	y += 4
	d.Line(left, y-8, right, y-8, 0.5)
	y += 6
	line("Net pay", invoicing.Naira(c.Net), true)
	var employerItems []payrollItem
	for _, it := range c.Items {
		if it.Kind == "employer" {
			employerItems = append(employerItems, it)
		}
	}
	if len(employerItems) > 0 {
		y += 10
		line("The business also pays", "", false)
		for _, it := range employerItems {
			line(it.Label, invoicing.Naira(it.AmountKobo), false)
		}
	}
	y += 10
	if words, _ := c.Payment["words"].(string); words != "" {
		line(words, "", false)
	}
	return d.Bytes()
}

// payslipLink is POST /v1/payroll/payslips/{payslip}/link {key}
// (payroll.read): Payrolla's 7-day link to the payslip, for someone not
// on Taskiem. The link is shown once and kept nowhere.
func (s *Server) payslipLink(w http.ResponseWriter, r *http.Request) {
	id, ok := payslipParam(r)
	var in struct {
		Key string `json:"key"`
	}
	if err := decodeBody(r, &in); err != nil || !ok {
		s.fail(w, r, errors.Join(err, errBadRequest))
		return
	}
	ctx := r.Context()
	p := principalFrom(ctx)
	cl, conn, wr, err := s.runWrite(ctx, p, "payslip-link:"+id, in.Key)
	if err == nil {
		var link payrolla.PayslipLink
		if link, err = cl.CreatePayslipLink(ctx, id, wr); err == nil {
			_ = s.tx(r, func(tx pgx.Tx) error {
				return auditTx(r, tx, "payroll.payslip_link", "payrolla-payslip:"+id, nil)
			})
			w.Header().Set("Cache-Control", "no-store")
			writeJSON(w, http.StatusCreated, map[string]any{"url": link.URL, "expires_at": link.ExpiresAt})
			return
		}
		if errors.Is(err, connector.ErrNotFound) {
			s.fail(w, r, pgx.ErrNoRows)
			return
		}
	}
	if errors.Is(err, errPayrollaNotConnected) {
		s.payFail(w, r, s.payrollaSays(ctx, p, conn, err))
		return
	}
	s.runFail(w, r, s.runSays(ctx, lang.EN, p, conn, cl, "", err))
}

// myPayslipRow is one of a person's own payslips, listed on My pay.
type myPayslipRow struct {
	ID      string `json:"payslip_id"`
	RunID   string `json:"run_id"`
	Period  string `json:"period"`
	Month   string `json:"month"`
	PayDate string `json:"pay_date"`
	Gross   int64  `json:"gross_kobo"`
	Net     int64  `json:"net_kobo"`
	State   string `json:"state"`
	Words   string `json:"words"`
}

func myPayslipRows(l lang.Tag, list []payrolla.PayslipSummary) []myPayslipRow {
	out := []myPayslipRow{}
	for _, x := range list {
		start := x.Period + "-01"
		state, words := payslipState(l, x.DisbursementStatus)
		pay := x.PayDate
		if len(pay) > 10 {
			pay = pay[:10]
		}
		out = append(out, myPayslipRow{ID: x.ID, RunID: x.RunID, Period: periodWords(start, start), Month: monthOf(start), PayDate: pay, Gross: x.GrossKobo,
			Net: x.NetKobo, State: state, Words: words})
	}
	return out
}

// myPayslips is GET /v1/payroll/me/payslips: a linked employee's own
// payslips, newest first ({linked: false} when they are not linked).
func (s *Server) myPayslips(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	p := principalFrom(ctx)
	emp, err := s.myEmployee(ctx, p)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if emp == "" {
		writeJSON(w, http.StatusOK, map[string]any{"linked": false, "payslips": []myPayslipRow{}})
		return
	}
	cl, conn, err := s.payrollaClient(ctx, p, "payroll.my_payslips")
	if err != nil {
		s.payFail(w, r, s.payrollaSays(ctx, p, conn, err))
		return
	}
	list, err := cl.EmployeePayslips(ctx, emp)
	if err != nil {
		s.runFail(w, r, s.runSays(ctx, lang.EN, p, conn, cl, "", err))
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]any{"linked": true, "payslips": myPayslipRows(lang.EN, list)})
}

// payslipCardMsg is a payslip's card as the Taskiem conversation keeps
// it: Payrolla's id and the period only; the figures are read when shown,
// for the person themselves.
type payslipCardMsg struct {
	PayslipID string `json:"payslip_id"`
	Period    string `json:"period"`
}

func payslipCardText(id, period string) string {
	b, _ := json.Marshal(payslipCardMsg{PayslipID: id, Period: period})
	return string(b)
}
