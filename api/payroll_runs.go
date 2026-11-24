package api

import (
	"context"
	"encoding/json"
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
	"github.com/israel-duff/taskiem/engine/invoicing"
	"github.com/israel-duff/taskiem/engine/lang"
)

// This month's run in Taskiem (P4b-3 of docs/spec/chat-pay-people.md;
// decision 0074): preparing it, each person's line with its full
// breakdown, a bonus or a deduction with a reason (gross: tax applies),
// leaving someone out and putting them back, recalculating, cancelling
// before approval, the funding wallet against the run's total, and
// approving, rejecting and locking, each step that commits money confirmed
// with Payrolla's own code, typed by the person. Locking schedules the
// payment: Payrolla pays everyone on the pay date.
//
// payroll.prepare prepares and changes a run, as the connected app with
// the person's name beside it; payroll.approve decides as the person's
// linked Payrolla user, whose role must allow it. Every write carries an
// Idempotency-Key made from the form's own id and the person acting
// (payroll_connect.go). Figures are read from Payrolla when shown; none is
// kept. The code is sent to Payrolla once and kept nowhere.

// runRefused is a refusal said in words, with what the screen needs to
// act on it (the run that already exists, Link my Payrolla account).
type runRefused struct {
	status int
	code   string
	msg    string
	extra  map[string]any
}

func (e runRefused) Error() string { return e.msg }

// runFail answers a run's refusal, or any other failure.
func (s *Server) runFail(w http.ResponseWriter, r *http.Request, err error) {
	var rr runRefused
	if errors.As(err, &rr) {
		out := map[string]any{"error": rr.msg, "code": rr.code}
		for k, v := range rr.extra {
			out[k] = v
		}
		writeJSON(w, rr.status, out)
		return
	}
	s.payFail(w, r, err)
}

// runStateWords is a run's status in a sentence's words ("it's now
// approved").
func runStateWords(l lang.Tag, status string) string {
	switch status {
	case "DRAFT", "COMPUTED", "APPROVED", "LOCKED", "DISBURSING", "COMPLETED", "PARTIALLY_COMPLETED", "CANCELLED", "FAILED":
		return tr(l, "payroll.state."+strings.ToLower(status))
	}
	return tr(l, "payroll.state.other")
}

// runSays says a refusal about a run in words (the plan's "Payrolla's
// refusals, in plain words"), in l; what it does not know is P4b-1's
// payrollaSays. cl and run, when given, are read for names: who else may
// approve, who has no account, what the run has moved on to.
func (s *Server) runSays(ctx context.Context, l lang.Tag, p *Principal, conn *payrollaConn, cl *payrolla.Client, run string, err error) error {
	if errors.Is(err, errBadRequest) {
		return err
	}
	code := payrolla.Code(err)
	refuse := func(status int, c, id string, kv ...string) error {
		return runRefused{status: status, code: c, msg: tr(l, id, kv...)}
	}
	switch code {
	case "SOD_VIOLATION":
		e := runRefused{status: http.StatusForbidden, code: "sod", msg: tr(l, "payroll.refused.sod")}
		if cl != nil {
			if who := s.otherApprovers(ctx, p, cl); len(who) > 0 {
				e.msg += " " + tr(l, "payroll.refused.sod_who", "who", strings.Join(who, ", "))
				e.extra = map[string]any{"approvers": who}
			}
		}
		return e
	case "APPROVER_NOT_LINKED":
		return runRefused{status: http.StatusConflict, code: "not_linked", msg: tr(l, "payroll.refused.not_linked"), extra: map[string]any{"link": "/payroll/settings"}}
	case "NOT_AN_APPROVER":
		return refuse(http.StatusForbidden, "not_an_approver", "payroll.refused.not_an_approver")
	case "CANNOT_PAY":
		return refuse(http.StatusForbidden, "cannot_pay", "payroll.refused.cannot_pay")
	case "PAYMENT_CONFIRMATION_NOT_SET_UP":
		return refuse(http.StatusConflict, "no_factor", "payroll.refused.no_factor")
	case "CODE_INVALID":
		return refuse(http.StatusUnprocessableEntity, "code_invalid", "payroll.refused.code_invalid")
	case "CODE_EXPIRED", "CHALLENGE_NOT_FOUND":
		return refuse(http.StatusGone, "code_expired", "payroll.refused.code_expired")
	case "CODE_LOCKED":
		return refuse(http.StatusGone, "code_locked", "payroll.refused.code_locked")
	case "CODE_USED":
		return refuse(http.StatusGone, "code_used", "payroll.refused.code_used")
	case "TOO_MANY_CODES":
		return refuse(http.StatusTooManyRequests, "too_many_codes", "payroll.refused.too_many_codes")
	case "INVALID_STATE":
		now := ""
		if cl != nil && run != "" {
			if r, rerr := cl.Run(ctx, run); rerr == nil {
				now = r.Status
			}
		}
		if now == "" {
			return refuse(http.StatusConflict, "invalid_state", "payroll.refused.invalid_state_unknown")
		}
		return runRefused{status: http.StatusConflict, code: "invalid_state", msg: tr(l, "payroll.refused.invalid_state", "state", runStateWords(l, now)), extra: map[string]any{"status": now}}
	case "NO_PAYOUT_DESTINATION":
		who := s.noAccountNames(ctx, cl, run, err)
		if len(who) == 0 {
			return refuse(http.StatusUnprocessableEntity, "no_account", "payroll.refused.no_account_some")
		}
		return runRefused{status: http.StatusUnprocessableEntity, code: "no_account", msg: tr(l, "payroll.refused.no_account", "who", strings.Join(who, ", ")),
			extra: map[string]any{"people": who}}
	case "SCOPE_REQUIRED":
		if isOwnerOf(p) {
			return refuse(http.StatusConflict, "scope_required", "payroll.refused.scope_owner")
		}
		return refuse(http.StatusConflict, "scope_required", "payroll.refused.scope_other")
	case "REASON_REQUIRED":
		return refuse(http.StatusUnprocessableEntity, "reason_required", "payroll.refused.reason")
	}
	return s.payrollaSays(ctx, p, conn, err)
}

// otherApprovers are the names of the linked Payrolla Owners and
// Approvers other than p (as Payrolla has them): who can approve instead.
func (s *Server) otherApprovers(ctx context.Context, p *Principal, cl *payrolla.Client) []string {
	list, err := cl.Approvers(ctx)
	if err != nil {
		return nil
	}
	var names []string
	for _, a := range list {
		if u := a.LinkedUser(); u != uuid.Nil && u != p.UserID && a.CanApprove && strings.TrimSpace(a.Name) != "" {
			names = append(names, strings.TrimSpace(a.Name))
		}
	}
	return names
}

// noAccountNames are those NO_PAYOUT_DESTINATION named, by the run's
// lines.
func (s *Server) noAccountNames(ctx context.Context, cl *payrolla.Client, run string, err error) []string {
	var d struct {
		IDs []string `json:"employee_ids"`
	}
	_ = json.Unmarshal(payrolla.ErrorDetails(err), &d)
	if cl == nil || run == "" || len(d.IDs) == 0 {
		return nil
	}
	lines, lerr := cl.Lines(ctx, run)
	if lerr != nil {
		return nil
	}
	var out []string
	for _, l := range lines {
		if slices.Contains(d.IDs, l.EmployeeID) {
			out = append(out, runLineName(l))
		}
	}
	return out
}

func runLineName(l payrolla.Line) string {
	n := strings.TrimSpace(l.FirstName + " " + l.LastName)
	if n == "" {
		return "Someone"
	}
	return n
}

var monthNames = []string{"January", "February", "March", "April", "May", "June", "July", "August", "September", "October", "November", "December"}

// periodWords is "November 2026" for a run of one month, else "16 Nov to
// 15 Dec 2026".
func periodWords(start, end string) string {
	a, err := time.Parse("2006-01-02", start)
	if err != nil {
		return ""
	}
	b, err := time.Parse("2006-01-02", end)
	if err != nil || a.Month() == b.Month() && a.Year() == b.Year() {
		return monthNames[a.Month()-1] + " " + a.Format("2006")
	}
	return a.Format("2 Jan") + " to " + b.Format("2 Jan 2006")
}

// dayWords is "28 Nov" (with the year when it is not this one's).
func dayWords(d string) string {
	t, err := time.Parse("2006-01-02", strings.TrimSpace(d))
	if err != nil {
		return d
	}
	if t.Year() != time.Now().Year() {
		return t.Format("2 Jan 2006")
	}
	return t.Format("2 Jan")
}

func peopleWords(l lang.Tag, n int64) string {
	if n == 1 {
		return tr(l, "payroll.people_one")
	}
	return tr(l, "payroll.people", "n", fmt.Sprint(n))
}

// payrollItem is one line of a person's breakdown.
type payrollItem struct {
	Kind       string `json:"kind"` // earning, deduction or employer
	Code       string `json:"code"`
	Label      string `json:"label"`
	AmountKobo int64  `json:"amount_kobo"`
}

// itemLabel is a breakdown line in plain words ("Tax (PAYE)", "Pension
// (yours)").
func itemLabel(code, label string) string {
	switch code {
	case "BASIC":
		return "Basic"
	case "HOUSING":
		return "Housing"
	case "TRANSPORT":
		return "Transport"
	case "PAYE":
		return "Tax (PAYE)"
	case "PENSION_EE":
		return "Pension (yours)"
	case "PENSION_ER":
		return "Pension (employer's)"
	case "NHF":
		return "NHF"
	case "NHIS":
		return "Health insurance (NHIS)"
	}
	if strings.TrimSpace(label) != "" {
		return label
	}
	return strings.ReplaceAll(strings.ToLower(code), "_", " ")
}

func itemKind(category string) string {
	switch category {
	case "EARNING":
		return "earning"
	case "EMPLOYER_CONTRIBUTION":
		return "employer"
	}
	return "deduction"
}

func itemsOf(v any) []payrollItem {
	out := []payrollItem{}
	for _, x := range anyList(v) {
		m, ok := x.(map[string]any)
		if !ok {
			continue
		}
		out = append(out, payrollItem{Kind: itemKind(strOf(m["category"])), Code: strOf(m["code"]), Label: itemLabel(strOf(m["code"]), strOf(m["label"])), AmountKobo: intOf(m["amount_kobo"])})
	}
	return out
}

// runPicture is a run as its page and its card show it, read from
// Payrolla now: the changes made to it, who is new, who left and whose
// pay changed since the last payroll, and the wallet against its total.
type runPicture struct {
	Adjustments []runAdjustment  `json:"adjustments"`
	Exclusions  []runExclusion   `json:"exclusions"`
	Changes     runChanges       `json:"changes"`
	Funding     *runFunding      `json:"funding,omitempty"`
	May         runMay           `json:"may"`
	Draft       string           `json:"not_worked_out,omitempty"`
	Linked      *payrollApprover `json:"-"`
}

type runAdjustment struct {
	ID         string `json:"id"`
	EmployeeID string `json:"employee_id"`
	Name       string `json:"name"`
	Type       string `json:"type"` // bonus or deduction
	AmountKobo int64  `json:"amount_kobo"`
	Reason     string `json:"reason"`
}

type runExclusion struct {
	ID         string `json:"id"`
	EmployeeID string `json:"employee_id"`
	Name       string `json:"name"`
	Reason     string `json:"reason"`
}

type runChanges struct {
	Since   string      `json:"since,omitempty"`
	New     []string    `json:"new"`
	Left    []string    `json:"left"`
	Changed []payChange `json:"pay_changed"`
}

type payChange struct {
	Name     string `json:"name"`
	FromKobo int64  `json:"from_kobo"`
	ToKobo   int64  `json:"to_kobo"`
}

type runFunding struct {
	HasWallet     bool   `json:"has_wallet"`
	AvailableKobo int64  `json:"available_kobo"`
	NeededKobo    int64  `json:"needed_kobo"`
	ShortByKobo   int64  `json:"short_by_kobo"`
	Words         string `json:"words,omitempty"`
}

// runMay is what this person may do to the run here, and what stops them.
type runMay struct {
	// Prepare: payroll.prepare and a connection that may write runs.
	Prepare bool `json:"prepare"`
	// Approve: payroll.approve and a connection that may approve (the
	// person's link is Payrolla's to check).
	Approve bool `json:"approve"`
	// Linked: the person is linked to a Payrolla user who may approve.
	Linked bool `json:"linked"`
	// Reconnect: the connection can't do it here (a key, or not allowed):
	// an owner reconnects.
	Reconnect bool `json:"reconnect"`
}

// openStatus is a run Taskiem may still change.
func openStatus(st string) bool { return st == "DRAFT" || st == "COMPUTED" }

// picture reads what a run's page and card show beyond the run and its
// lines; what Payrolla does not answer is left out.
func (s *Server) picture(ctx context.Context, l lang.Tag, p *Principal, cl *payrolla.Client, run payrolla.Run, lines []payrolla.Line) runPicture {
	pic := runPicture{Adjustments: []runAdjustment{}, Exclusions: []runExclusion{}, Changes: runChanges{New: []string{}, Left: []string{}, Changed: []payChange{}}}
	names := map[string]string{}
	for _, ln := range lines {
		names[ln.EmployeeID] = runLineName(ln)
	}
	// The last payroll before this one: who is new, who left, whose pay
	// changed.
	if runs, err := cl.Runs(ctx, "", 24); err == nil {
		var prev *payrolla.Run
		for i := range runs {
			x := runs[i]
			if x.ID == run.ID || x.PeriodEnd >= run.PeriodStart || x.Status == "CANCELLED" || x.Status == "DRAFT" {
				continue
			}
			if prev == nil || x.PeriodEnd > prev.PeriodEnd {
				prev = &x
			}
		}
		if prev != nil {
			if pl, err := cl.Lines(ctx, prev.ID); err == nil {
				pic.Changes.Since = periodWords(prev.PeriodStart, prev.PeriodEnd)
				was := map[string]payrolla.Line{}
				for _, x := range pl {
					was[x.EmployeeID] = x
					if _, ok := names[x.EmployeeID]; !ok {
						names[x.EmployeeID] = runLineName(x)
					}
				}
				now := map[string]bool{}
				for _, x := range lines {
					now[x.EmployeeID] = true
					w, ok := was[x.EmployeeID]
					switch {
					case !ok:
						pic.Changes.New = append(pic.Changes.New, runLineName(x))
					case w.GrossKobo != x.GrossKobo && w.GrossKobo > 0 && x.GrossKobo > 0:
						pic.Changes.Changed = append(pic.Changes.Changed, payChange{Name: runLineName(x), FromKobo: w.GrossKobo, ToKobo: x.GrossKobo})
					}
				}
				for _, x := range pl {
					if !now[x.EmployeeID] {
						pic.Changes.Left = append(pic.Changes.Left, runLineName(x))
					}
				}
			}
		}
	}
	if adj, err := cl.Adjustments(ctx, run.ID); err == nil {
		for _, a := range adj {
			pic.Adjustments = append(pic.Adjustments, runAdjustment{ID: a.ID, EmployeeID: a.EmployeeID, Name: names[a.EmployeeID], Type: strings.ToLower(a.Type), AmountKobo: a.AmountKobo, Reason: a.Reason})
		}
	}
	if ex, err := cl.Exclusions(ctx, run.ID); err == nil {
		for _, x := range ex {
			n := names[x.EmployeeID]
			if n == "" {
				if got, err := cl.RunEmployeeName(ctx, x.EmployeeID); err == nil && got != "" {
					n = got
				} else {
					n = tr(l, "payroll.someone")
				}
			}
			pic.Exclusions = append(pic.Exclusions, runExclusion{ID: x.ID, EmployeeID: x.EmployeeID, Name: n, Reason: x.Reason})
			// Left out this month is not "left".
			pic.Changes.Left = slices.DeleteFunc(pic.Changes.Left, func(s string) bool { return s == n })
		}
	}
	if run.Status == "DRAFT" || run.Status == "COMPUTED" || run.Status == "APPROVED" || run.Status == "LOCKED" {
		if f, err := cl.Funding(ctx); err == nil {
			rf := &runFunding{HasWallet: f.HasWallet, AvailableKobo: f.AvailableKobo, NeededKobo: run.TotalNetKobo}
			if run.TotalNetKobo > f.AvailableKobo {
				rf.ShortByKobo = run.TotalNetKobo - f.AvailableKobo
				rf.Words = tr(l, "payroll.short", "available", invoicing.Naira(f.AvailableKobo), "needed", invoicing.Naira(run.TotalNetKobo),
					"short", invoicing.Naira(rf.ShortByKobo), "date", dayWords(run.PayDate))
			}
			pic.Funding = rf
		}
	}
	if run.Status == "DRAFT" {
		pic.Draft = tr(l, "payroll.refused.not_worked_out")
	}
	// What this person may do here.
	scopes := []string{}
	if co, err := cl.Company(ctx); err == nil {
		scopes = co.Key.Scopes
	}
	canWrite, canApprove := slices.Contains(scopes, "payroll:write"), slices.Contains(scopes, "payroll:approve")
	pic.May.Prepare = p.Can(PermPayrollPrepare) && canWrite && openStatus(run.Status)
	pic.May.Approve = p.Can(PermPayrollApprove) && canApprove
	if (p.Can(PermPayrollPrepare) && !canWrite) || (p.Can(PermPayrollApprove) && !canApprove) {
		pic.May.Reconnect = true
	}
	if pic.May.Approve {
		if list, err := cl.Approvers(ctx); err == nil {
			for _, a := range list {
				if a.LinkedUser() == p.UserID && a.CanApprove {
					pic.May.Linked = true
				}
			}
		}
	}
	return pic
}

// runMore adds to a run's page (GET /v1/payroll/runs/{run}) what P4b-3
// shows: each line's breakdown (already in lines), the changes made, who
// is new or left, the wallet, and what this person may do. A connection
// that answers none of it leaves the page as P4a's.
func (s *Server) runMore(ctx context.Context, p *Principal, run string, out map[string]any) {
	cl, _, err := s.payrollaClient(ctx, p, "payroll.run")
	if err != nil {
		return
	}
	r, err := cl.Run(ctx, run)
	if err != nil {
		return
	}
	lines, err := cl.Lines(ctx, run)
	if err != nil {
		return
	}
	pic := s.picture(ctx, lang.EN, p, cl, r, lines)
	out["adjustments"] = pic.Adjustments
	out["exclusions"] = pic.Exclusions
	out["changes"] = pic.Changes
	if pic.Funding != nil {
		out["funding"] = pic.Funding
	}
	out["may"] = pic.May
	if pic.Draft != "" {
		out["not_worked_out"] = pic.Draft
	}
	// Deciding is here now: Payrolla's link only where it is not.
	if pic.May.Approve {
		delete(out, "approval_url")
	}
	if r.Status == "LOCKED" || r.Status == "DISBURSING" {
		out["pays_on"] = tr(lang.EN, "payroll.pays_on", "date", dayWords(r.PayDate))
	}
}

// runChange is what a run's writes take: the form's own id for the
// Idempotency-Key.
type runChange struct {
	Key string `json:"key"`
}

// runWrite opens a client for a change to a run by p, and the write's
// headers: the key made from the connection, the route and the form's id.
func (s *Server) runWrite(ctx context.Context, p *Principal, route, client string) (*payrolla.Client, *payrollaConn, payrolla.Write, error) {
	cl, conn, err := s.payrollaClient(ctx, p, "payroll.run")
	if err != nil {
		return nil, conn, payrolla.Write{}, err
	}
	key, err := payrollaKey(conn, route, client)
	if err != nil {
		return nil, conn, payrolla.Write{}, err
	}
	return cl, conn, payrolla.Write{Key: key, User: p.UserID, Name: s.actingName(ctx, p)}, nil
}

// runAudit records who did what to which run (no amounts).
func (s *Server) runAudit(r *http.Request, action, run string, detail map[string]any) {
	if err := s.tx(r, func(tx pgx.Tx) error {
		return auditTx(r, tx, action, "payrolla-run:"+run, detail)
	}); err != nil {
		s.Logger.Warn("payroll: auditing", "action", action, "err", err)
	}
}

func runParam(r *http.Request) (string, bool) {
	run := strings.TrimSpace(chi.URLParam(r, "run"))
	_, err := uuid.Parse(run)
	return run, err == nil
}

var periodRe = regexp.MustCompile(`^\d{4}-(0[1-9]|1[0-2])$`)
var dateRe = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)

// changedOut is a run as a change left it, for the screens.
func changedOut(ch payrolla.Changed) map[string]any {
	out := map[string]any{"run": runOf(runMap(ch.Run))}
	if ch.ComputeError != nil {
		out["compute_error"] = tr(lang.EN, "payroll.refused.compute_error", "reason", strings.TrimRight(strings.TrimSpace(ch.ComputeError.Message), "."))
	}
	return out
}

func runMap(r payrolla.Run) map[string]any {
	return map[string]any{"run_id": r.ID, "period_start": r.PeriodStart, "period_end": r.PeriodEnd, "pay_date": r.PayDate, "run_type": r.RunType, "status": r.Status,
		"employee_count": float64(r.EmployeeCount), "total_gross_kobo": float64(r.TotalGrossKobo), "total_net_kobo": float64(r.TotalNetKobo)}
}

// preparePayrollRun is POST /v1/payroll/runs {period? (2026-11), period_start?,
// period_end?, pay_date?, key} (payroll.prepare): the run made and worked
// out. A period that has one already answers duplicate_run with its id; a
// run Payrolla could not work out is made all the same, with why.
func (s *Server) preparePayrollRun(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Period      string `json:"period"`
		PeriodStart string `json:"period_start"`
		PeriodEnd   string `json:"period_end"`
		PayDate     string `json:"pay_date"`
		Key         string `json:"key"`
	}
	if err := decodeBody(r, &in); err != nil {
		s.fail(w, r, err)
		return
	}
	ok := periodRe.MatchString(in.Period) || in.Period == "" && dateRe.MatchString(in.PeriodStart) && dateRe.MatchString(in.PeriodEnd)
	if !ok || in.PayDate != "" && !dateRe.MatchString(in.PayDate) {
		s.fail(w, r, fmt.Errorf("%w: name the month (2026-11), or the first and last day", errBadRequest))
		return
	}
	ctx := r.Context()
	p := principalFrom(ctx)
	out, err := s.prepare(ctx, p, payrolla.Period{Period: in.Period, PeriodStart: in.PeriodStart, PeriodEnd: in.PeriodEnd, PayDate: in.PayDate}, in.Key, lang.EN)
	if err != nil {
		s.runFail(w, r, err)
		return
	}
	s.runAudit(r, "payroll.run_prepare", out.Run.ID, map[string]any{"period": out.Run.PeriodStart})
	writeJSON(w, http.StatusCreated, changedOut(out))
}

// prepare makes a run for p; DUPLICATE_RUN names the period's run.
func (s *Server) prepare(ctx context.Context, p *Principal, period payrolla.Period, client string, l lang.Tag) (payrolla.Changed, error) {
	cl, conn, wr, err := s.runWrite(ctx, p, "run-prepare", client)
	if err != nil {
		if errors.Is(err, errPayrollaNotConnected) {
			return payrolla.Changed{}, s.payrollaSays(ctx, p, conn, err)
		}
		return payrolla.Changed{}, err
	}
	out, err := cl.CreateRun(ctx, period, wr)
	if payrolla.Code(err) == "DUPLICATE_RUN" {
		start := period.PeriodStart
		if period.Period != "" {
			start = period.Period + "-01"
		}
		e := runRefused{status: http.StatusConflict, code: "duplicate_run", msg: tr(l, "payroll.refused.duplicate", "period", periodWords(start, start))}
		if runs, lerr := cl.Runs(ctx, "", 24); lerr == nil {
			for _, x := range runs {
				if x.PeriodStart == start && x.Status != "CANCELLED" {
					e.extra = map[string]any{"run_id": x.ID}
				}
			}
		}
		return out, e
	}
	if err != nil {
		return out, s.runSays(ctx, l, p, conn, cl, "", err)
	}
	return out, nil
}

// adjustPayrollRun is POST /v1/payroll/runs/{run}/adjustments {employee_id, type
// (bonus or deduction), amount_kobo, reason, key} (payroll.prepare): a
// bonus or a deduction, before tax (tax and pension apply as usual).
func (s *Server) adjustPayrollRun(w http.ResponseWriter, r *http.Request) {
	run, ok := runParam(r)
	var in struct {
		Employee string `json:"employee_id"`
		Type     string `json:"type"`
		Amount   int64  `json:"amount_kobo"`
		Reason   string `json:"reason"`
		Key      string `json:"key"`
	}
	if err := decodeBody(r, &in); err != nil || !ok {
		s.fail(w, r, errors.Join(err, errBadRequest))
		return
	}
	typ := strings.ToUpper(strings.TrimSpace(in.Type))
	in.Reason = strings.TrimSpace(in.Reason)
	if _, err := uuid.Parse(in.Employee); err != nil || (typ != "BONUS" && typ != "DEDUCTION") || in.Amount < 100 || in.Amount > 1_000_000_000_00 ||
		in.Reason == "" || utf8.RuneCountInString(in.Reason) > 200 {
		s.fail(w, r, fmt.Errorf("%w: a bonus or a deduction needs the person, an amount and why", errBadRequest))
		return
	}
	ctx := r.Context()
	p := principalFrom(ctx)
	cl, conn, wr, err := s.runWrite(ctx, p, "run-adjust:"+run, in.Key)
	if err == nil {
		var ch payrolla.Changed
		if ch, err = cl.AddAdjustment(ctx, run, in.Employee, typ, in.Amount, in.Reason, wr); err == nil {
			s.runAudit(r, "payroll.run_adjust", run, map[string]any{"employee_id": in.Employee, "type": strings.ToLower(typ)})
			writeJSON(w, http.StatusCreated, changedOut(ch))
			return
		}
	}
	s.runFail(w, r, s.runSays(ctx, lang.EN, p, conn, cl, run, err))
}

// unadjustPayrollRun is DELETE /v1/payroll/runs/{run}/adjustments/{id}?key=
// (payroll.prepare).
func (s *Server) unadjustPayrollRun(w http.ResponseWriter, r *http.Request) {
	s.payrollRunRemove(w, r, "adjustments")
}

// putBackPayrollRun is DELETE /v1/payroll/runs/{run}/exclusions/{id}?key=
// (payroll.prepare): someone left out is put back.
func (s *Server) putBackPayrollRun(w http.ResponseWriter, r *http.Request) {
	s.payrollRunRemove(w, r, "exclusions")
}

func (s *Server) payrollRunRemove(w http.ResponseWriter, r *http.Request, what string) {
	run, ok := runParam(r)
	id := chi.URLParam(r, "id")
	if _, err := uuid.Parse(id); err != nil || !ok {
		s.fail(w, r, pgx.ErrNoRows)
		return
	}
	ctx := r.Context()
	p := principalFrom(ctx)
	cl, conn, wr, err := s.runWrite(ctx, p, "run-"+what+"-remove:"+run+":"+id, r.URL.Query().Get("key"))
	if err == nil {
		var ch payrolla.Changed
		if what == "adjustments" {
			ch, err = cl.RemoveAdjustment(ctx, run, id, wr)
		} else {
			ch, err = cl.PutBack(ctx, run, id, wr)
		}
		if err == nil {
			s.runAudit(r, "payroll.run_"+map[string]string{"adjustments": "unadjust", "exclusions": "put_back"}[what], run, nil)
			writeJSON(w, http.StatusOK, changedOut(ch))
			return
		}
	}
	s.runFail(w, r, s.runSays(ctx, lang.EN, p, conn, cl, run, err))
}

// excludePayrollRun is POST /v1/payroll/runs/{run}/exclusions {employee_id,
// reason, key} (payroll.prepare): left out of this month only.
func (s *Server) excludePayrollRun(w http.ResponseWriter, r *http.Request) {
	run, ok := runParam(r)
	var in struct {
		Employee string `json:"employee_id"`
		Reason   string `json:"reason"`
		Key      string `json:"key"`
	}
	if err := decodeBody(r, &in); err != nil || !ok {
		s.fail(w, r, errors.Join(err, errBadRequest))
		return
	}
	in.Reason = strings.TrimSpace(in.Reason)
	if _, err := uuid.Parse(in.Employee); err != nil || in.Reason == "" || utf8.RuneCountInString(in.Reason) > 200 {
		s.fail(w, r, fmt.Errorf("%w: say who, and why", errBadRequest))
		return
	}
	ctx := r.Context()
	p := principalFrom(ctx)
	cl, conn, wr, err := s.runWrite(ctx, p, "run-exclude:"+run, in.Key)
	if err == nil {
		var ch payrolla.Changed
		if ch, err = cl.Exclude(ctx, run, in.Employee, in.Reason, wr); err == nil {
			s.runAudit(r, "payroll.run_exclude", run, map[string]any{"employee_id": in.Employee})
			writeJSON(w, http.StatusCreated, changedOut(ch))
			return
		}
	}
	s.runFail(w, r, s.runSays(ctx, lang.EN, p, conn, cl, run, err))
}

// computePayrollRun is POST /v1/payroll/runs/{run}/compute {key}
// (payroll.prepare): Recalculate. A run still not worked out says so.
func (s *Server) computePayrollRun(w http.ResponseWriter, r *http.Request) {
	s.payrollRunStep(w, r, "compute")
}

// cancelPayrollRun is POST /v1/payroll/runs/{run}/cancel {key} (payroll.prepare):
// before approval only.
func (s *Server) cancelPayrollRun(w http.ResponseWriter, r *http.Request) {
	s.payrollRunStep(w, r, "cancel")
}

func (s *Server) payrollRunStep(w http.ResponseWriter, r *http.Request, step string) {
	run, ok := runParam(r)
	var in runChange
	if err := decodeBody(r, &in); err != nil || !ok {
		s.fail(w, r, errors.Join(err, errBadRequest))
		return
	}
	ctx := r.Context()
	p := principalFrom(ctx)
	cl, conn, wr, err := s.runWrite(ctx, p, "run-"+step+":"+run, in.Key)
	if err == nil {
		var got payrolla.Run
		if step == "cancel" {
			got, err = cl.Cancel(ctx, run, wr)
		} else {
			got, err = cl.Compute(ctx, run, wr)
		}
		if err == nil {
			s.runAudit(r, "payroll.run_"+step, run, nil)
			out := map[string]any{"run": runOf(runMap(got))}
			if step == "compute" && got.Status == "DRAFT" {
				out["compute_error"] = tr(lang.EN, "payroll.refused.not_worked_out")
			}
			writeJSON(w, http.StatusOK, out)
			return
		}
	}
	s.runFail(w, r, s.runSays(ctx, lang.EN, p, conn, cl, run, err))
}

// startPayrollRunCode is POST /v1/payroll/runs/{run}/confirmations {action
// (approve or lock), key} (payroll.approve): Payrolla checks the action may
// be done, then sends the person a code; where it went is said.
func (s *Server) startPayrollRunCode(w http.ResponseWriter, r *http.Request) {
	run, ok := runParam(r)
	var in struct {
		Action string `json:"action"`
		Key    string `json:"key"`
	}
	if err := decodeBody(r, &in); err != nil || !ok || (in.Action != payrolla.ActionApprove && in.Action != payrolla.ActionLock) {
		s.fail(w, r, errBadRequest)
		return
	}
	ctx := r.Context()
	p := principalFrom(ctx)
	cl, conn, wr, err := s.runWrite(ctx, p, "run-code:"+in.Action+":"+run, in.Key)
	if err == nil {
		var ch payrolla.Challenge
		if ch, err = cl.StartRunConfirmation(ctx, run, in.Action, wr); err == nil {
			s.runAudit(r, "payroll.run_code", run, map[string]any{"action": in.Action, "channel": ch.Channel})
			writeJSON(w, http.StatusAccepted, map[string]any{"challenge": ch, "sent_to": codeWhere(lang.EN, ch), "safer": ch.Channel == "SMS"})
			return
		}
	}
	s.runFail(w, r, s.runSays(ctx, lang.EN, p, conn, cl, run, err))
}

// codeWhere says where Payrolla sent a code.
func codeWhere(l lang.Tag, ch payrolla.Challenge) string {
	hint := strings.TrimSpace(strings.TrimLeft(ch.DestinationHint, "•· *"))
	switch ch.Channel {
	case "TOTP":
		return tr(l, "payroll.code.totp")
	case "SMS":
		if hint != "" {
			return tr(l, "payroll.code.sms", "hint", hint)
		}
		return tr(l, "payroll.code.sms_any")
	case "EMAIL":
		if ch.DestinationHint != "" {
			return tr(l, "payroll.code.email", "hint", ch.DestinationHint)
		}
		return tr(l, "payroll.code.email_any")
	}
	return tr(l, "payroll.code.any")
}

// codeOf is a typed code's digits (spaces and dashes allowed), or "".
func codeOf(typed string) string {
	code := strings.Map(func(r rune) rune {
		switch {
		case r >= '0' && r <= '9':
			return r
		case r == ' ' || r == '-':
			return -1
		}
		return 'x'
	}, typed)
	if len(code) < 4 || len(code) > 10 || strings.Contains(code, "x") {
		return ""
	}
	return code
}

// decidePayrollRun is POST /v1/payroll/runs/{run}/approve and …/lock
// {challenge_id, code, key} (payroll.approve): the code goes to Payrolla
// once and is kept nowhere (not logged, not audited: the audit says a code
// confirmed it). Locking schedules the payment for the pay date.
func (s *Server) decidePayrollRun(action string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		run, ok := runParam(r)
		var in struct {
			Challenge string `json:"challenge_id"`
			Code      string `json:"code"`
			Key       string `json:"key"`
		}
		if err := decodeBody(r, &in); err != nil || !ok {
			s.fail(w, r, errors.Join(err, errBadRequest))
			return
		}
		ctx := r.Context()
		p := principalFrom(ctx)
		code := codeOf(in.Code)
		if code == "" {
			s.runFail(w, r, runRefused{status: http.StatusUnprocessableEntity, code: "code_invalid", msg: tr(lang.EN, "payroll.refused.code_invalid")})
			return
		}
		if _, err := uuid.Parse(in.Challenge); err != nil {
			s.runFail(w, r, runRefused{status: http.StatusGone, code: "code_expired", msg: tr(lang.EN, "payroll.refused.code_expired")})
			return
		}
		d, err := s.decideRunCode(ctx, p, run, action, in.Challenge, code, in.Key, lang.EN)
		if err != nil {
			s.runFail(w, r, err)
			return
		}
		s.runAudit(r, "payroll.run_"+action, run, map[string]any{"confirmed_with_code": true})
		writeJSON(w, http.StatusOK, decidedOut(lang.EN, action, d))
	}
}

// decide sends a code for action on run, made by p, to Payrolla once.
func (s *Server) decideRunCode(ctx context.Context, p *Principal, run, action, challenge, code, client string, l lang.Tag) (payrolla.Decided, error) {
	// Each attempt at a code is its own write.
	cl, conn, wr, err := s.runWrite(ctx, p, "run-"+action+":"+run+":"+challenge, client)
	if err == nil {
		var d payrolla.Decided
		if d, err = cl.Decide(ctx, run, action, challenge, code, wr); err == nil {
			return d, nil
		}
	}
	return payrolla.Decided{}, s.runSays(ctx, l, p, conn, cl, run, err)
}

// decidedOut is a decision's answer, with what to say: "Approved. Lock
// and pay?", or "Locked. Payrolla pays 48 people ₦9,200,000 on 28 Nov."
func decidedOut(l lang.Tag, action string, d payrolla.Decided) map[string]any {
	out := map[string]any{"run": runOf(runMap(d.Run)), "payment": d.Payment}
	if action == payrolla.ActionLock {
		date := d.Payment.ScheduledFor
		if date == "" {
			date = d.Run.PayDate
		}
		out["words"] = tr(l, "payroll.locked", "people", peopleWords(l, d.Run.EmployeeCount), "amount", invoicing.Naira(d.Run.TotalNetKobo), "date", dayWords(date))
	} else {
		out["words"] = tr(l, "payroll.approved")
	}
	return out
}

// rejectPayrollRun is POST /v1/payroll/runs/{run}/reject {note, key}
// (payroll.approve): no code (it moves no money), as the linked person.
func (s *Server) rejectPayrollRun(w http.ResponseWriter, r *http.Request) {
	run, ok := runParam(r)
	var in struct {
		Note string `json:"note"`
		Key  string `json:"key"`
	}
	if err := decodeBody(r, &in); err != nil || !ok {
		s.fail(w, r, errors.Join(err, errBadRequest))
		return
	}
	in.Note = strings.TrimSpace(in.Note)
	if in.Note == "" || utf8.RuneCountInString(in.Note) > 300 {
		s.runFail(w, r, runRefused{status: http.StatusBadRequest, code: "note_required", msg: tr(lang.EN, "payroll.refused.note")})
		return
	}
	ctx := r.Context()
	p := principalFrom(ctx)
	d, err := s.rejectPayroll(ctx, p, run, in.Note, in.Key, lang.EN)
	if err != nil {
		s.runFail(w, r, err)
		return
	}
	s.runAudit(r, "payroll.run_reject", run, nil)
	writeJSON(w, http.StatusOK, map[string]any{"run": runOf(runMap(d.Run)), "words": tr(lang.EN, "payroll.rejected")})
}

func (s *Server) rejectPayroll(ctx context.Context, p *Principal, run, note, client string, l lang.Tag) (payrolla.Decided, error) {
	cl, conn, wr, err := s.runWrite(ctx, p, "run-reject:"+run, client)
	if err == nil {
		var d payrolla.Decided
		if d, err = cl.Reject(ctx, run, note, wr); err == nil {
			return d, nil
		}
	}
	return payrolla.Decided{}, s.runSays(ctx, l, p, conn, cl, run, err)
}

// thisMonth is the month now as the Payroll page's This month panel shows
// it: its period, in words, and its run when there is one.
func thisMonth(now time.Time, runs []payrollRun) map[string]any {
	now = now.In(lagos())
	period := now.Format("2006-01")
	out := map[string]any{"period": period, "label": monthNames[now.Month()-1] + " " + now.Format("2006"), "month": monthNames[now.Month()-1]}
	for _, r := range runs {
		if strings.HasPrefix(r.PeriodStart, period) && r.Status != "CANCELLED" {
			out["run"] = r
			break
		}
	}
	return out
}
