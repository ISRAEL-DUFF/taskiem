package api

import (
	"bytes"
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"math"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/connectors/payrolla"
	"github.com/israel-duff/taskiem/engine/csvsafe"
)

// Settings and the tax and pension schedules (P4b-5 of
// docs/spec/chat-pay-people.md; Payrolla's Phase C reply, section 3):
//
//   - GET /v1/payroll/settings: how the payroll is set up in Payrolla, in
//     plain words (how often people are paid, the tax state, separation of
//     duties, how it is funded, whether a PAYE tax number is on file, and
//     the deductions in force), read-only, with where to change it in
//     Payrolla;
//   - GET /v1/payroll/schedules: the PAYE, pension, NHF and NHIS schedules
//     of each payroll, to pay or paid, each with its total;
//   - GET /v1/payroll/schedules/{id}: one schedule with a line per person,
//     each pension PIN and PFA masked to its last four;
//   - POST …/{id}/reveal {passkey}: the PINs and PFAs in full, for a
//     passkey asked for payroll.reveal on that schedule, recorded as a
//     saved account's reveal is;
//   - POST …/{id}/download {passkey?}: a spreadsheet (CSV) made when asked
//     and kept nowhere; a pension schedule's needs the passkey too.
//
// Who: payroll.read (payroll.prepare and payroll.approve come with it).
// Schedules are treated like payslips: read from Payrolla when a screen
// asks, never in a message, never kept, never logged; the audit names the
// schedule and the person, never a PIN or an amount. Payrolla needs
// reports:read for schedules: without it an owner is asked to reconnect.

// payrollOpReveal is the step-up operation for a schedule's pension PINs
// in full (target: the schedule's id).
const payrollOpReveal = "payroll.reveal"

func (s *Server) payrollReportsRoutes(r chi.Router) {
	r.With(s.need(PermPayrollSee)).Get("/payroll/settings", s.getPayrollSettings)
	r.With(s.need(PermPayrollSee)).Get("/payroll/schedules", s.getPayrollSchedules)
	r.With(s.need(PermPayrollSee)).Get("/payroll/schedules/{id}", s.getPayrollSchedule)
	r.With(s.need(PermPayrollSee)).Post("/payroll/schedules/{id}/reveal", s.revealPayrollSchedule)
	r.With(s.need(PermPayrollSee)).Post("/payroll/schedules/{id}/download", s.downloadPayrollSchedule)
}

// --- settings ---

// settingRow is one line of the setup in words.
type settingRow struct {
	Key   string `json:"key"`
	Label string `json:"label"`
	Value string `json:"value"`
	// Warn: something Payrolla needs is missing (no PAYE tax number).
	Warn bool  `json:"warn,omitempty"`
	On   *bool `json:"on,omitempty"`
}

// payrollSetup is the setup as the screens and the chat say it.
type payrollSetup struct {
	Company    map[string]string `json:"company"`
	Rows       []settingRow      `json:"rows"`
	Deductions []settingRow      `json:"deductions"`
	// NotSetUp: Payrolla has no deductions set up yet.
	NotSetUp string `json:"not_set_up,omitempty"`
	Since    string `json:"since,omitempty"`
	// ChangeURL is Payrolla's settings page ("" when unknown: the words
	// say "in Payrolla").
	ChangeURL string `json:"change_url,omitempty"`
	// SeparationOfDuties is the setting itself, for the run's page.
	SeparationOfDuties bool `json:"separation_of_duties"`
}

func frequencyWords(f string) string {
	switch strings.ToUpper(f) {
	case "MONTHLY":
		return "Every month"
	case "WEEKLY":
		return "Every week"
	case "BIWEEKLY", "FORTNIGHTLY":
		return "Every two weeks"
	case "":
		return "Not set in Payrolla"
	}
	return strings.ToUpper(f[:1]) + strings.ToLower(strings.ReplaceAll(f[1:], "_", " "))
}

func fundingWords(m string) string {
	switch strings.ToUpper(m) {
	case "PREFUNDED":
		return "You add money to your Payrolla wallet before pay day"
	case "":
		return "Not set in Payrolla"
	}
	return "As Payrolla has it: " + strings.ToLower(strings.ReplaceAll(m, "_", " "))
}

func stateWords(st string) string {
	st = strings.TrimSpace(st)
	switch {
	case st == "":
		return "Not set in Payrolla"
	case strings.EqualFold(st, "FCT") || strings.Contains(strings.ToLower(st), "abuja"):
		return "Abuja (FCT)"
	case strings.HasSuffix(strings.ToLower(st), " state"):
		return st
	}
	return st + " State"
}

// percent is a rate (0.025) as words ("2.5%").
func percent(rate float64) string {
	return strconv.FormatFloat(math.Round(rate*10000)/100, 'f', -1, 64) + "%"
}

func boolPtr(b bool) *bool { return &b }

// setupOf says a company's setup in plain words.
func setupOf(st payrolla.Settings, change string) payrollSetup {
	c := st.Company
	out := payrollSetup{Company: map[string]string{"name": c.Name}, ChangeURL: change, SeparationOfDuties: c.SeparationOfDuties}
	if c.RCNumber != "" {
		out.Company["rc_number"] = c.RCNumber
	}
	sod := "Off: one person may prepare and approve a payroll"
	if c.SeparationOfDuties {
		sod = "On: whoever prepares a payroll can't approve it"
	}
	tin := settingRow{Key: "paye_tin", Label: "PAYE tax number", Value: "On file", On: boolPtr(c.PAYETINSet)}
	if !c.PAYETINSet {
		tin.Value, tin.Warn = "Not on file yet. Payrolla needs it to file your tax: add it in Payrolla", true
	}
	out.Rows = []settingRow{
		{Key: "pay_frequency", Label: "How often people are paid", Value: frequencyWords(c.PayFrequency)},
		{Key: "tax_state", Label: "State you pay tax to", Value: stateWords(c.TaxState)},
		{Key: "separation_of_duties", Label: "Someone else approves", Value: sod, On: boolPtr(c.SeparationOfDuties)},
		{Key: "funding", Label: "How payroll is paid for", Value: fundingWords(c.FundingModel)},
		tin,
	}
	out.Deductions = []settingRow{}
	if st.Statutory == nil {
		out.NotSetUp = "Payrolla has no deductions set up yet. Set them up in Payrolla."
		return out
	}
	d := st.Statutory
	inForce := func(on bool, words string) (string, *bool) {
		if !on {
			return "Not in force", boolPtr(false)
		}
		return words, boolPtr(true)
	}
	row := func(key, label string, on bool, words string) settingRow {
		v, o := inForce(on, words)
		return settingRow{Key: key, Label: label, Value: v, On: o}
	}
	out.Deductions = []settingRow{
		row("paye", "Tax (PAYE)", d.PAYE, "In force, by the tax tables"),
		row("pension", "Pension", d.Pension, percent(d.PensionEmployeeRate)+" from each person, "+percent(d.PensionEmployerRate)+" from you"),
		row("nhf", "Housing fund (NHF)", d.NHF, percent(d.NHFRate)+" of basic pay"),
		row("nhis", "Health insurance (NHIS)", d.NHIS, "In force"),
	}
	if t, err := time.Parse(time.RFC3339, d.EffectiveFrom); err == nil {
		out.Since = t.Format("2 Jan 2006")
	}
	return out
}

// reportsClient is the person's Payrolla client, or the refusal in words.
func (s *Server) reportsClient(ctx context.Context, p *Principal) (*payrolla.Client, error) {
	cl, conn, err := s.payrollaClient(ctx, p, "payroll.reports")
	if err != nil {
		if errors.Is(err, errPayrollaNotConnected) || payrolla.Ended(err) {
			return nil, s.payrollaSays(ctx, p, conn, err)
		}
		return nil, err
	}
	return cl, nil
}

// reportsSays says a refusal of the schedules: no reports:read is said in
// Payrolla's own words for the scope.
func (s *Server) reportsSays(ctx context.Context, p *Principal, err error) error {
	if payrolla.Code(err) == "SCOPE_REQUIRED" {
		if isOwnerOf(p) {
			return payRefused{http.StatusConflict, "scope_required", "Payrolla hasn't allowed Taskiem to read your tax and pension schedules. Reconnect Payrolla and allow Read statutory reports."}
		}
		return payRefused{http.StatusConflict, "scope_required", "Ask the owner to allow Read statutory reports in Payrolla."}
	}
	conn, _ := s.payrollaConnection(ctx, p.TenantID)
	return s.payrollaSays(ctx, p, conn, err)
}

// getPayrollSettings is GET /v1/payroll/settings: the setup in words.
func (s *Server) getPayrollSettings(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	p := principalFrom(ctx)
	cl, err := s.reportsClient(ctx, p)
	if err != nil {
		s.payFail(w, r, err)
		return
	}
	st, err := cl.Settings(ctx)
	if err != nil {
		s.payFail(w, r, s.reportsSays(ctx, p, err))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"settings": setupOf(st, cl.SettingsURL()), "is_owner": isOwnerOf(p)})
}

// --- schedules ---

// scheduleView is a schedule as the screens show it.
type scheduleView struct {
	ID          string `json:"id"`
	RunID       string `json:"run_id"`
	Kind        string `json:"kind"` // paye, pension, nhf or nhis
	Title       string `json:"title"`
	To          string `json:"to"`
	Period      string `json:"period"`
	PeriodWords string `json:"period_words"`
	TotalKobo   int64  `json:"total_kobo"`
	People      int64  `json:"people"`
	State       string `json:"state"` // to_pay, paid or preparing
	StateWords  string `json:"state_words"`
	When        string `json:"when,omitempty"`
	Reference   string `json:"reference,omitempty"`
}

// scheduleLine is one person's part, with the PIN and PFA masked unless
// revealed.
type scheduleLine struct {
	EmployeeID string `json:"employee_id"`
	Name       string `json:"name"`
	AmountKobo int64  `json:"amount_kobo"`
	PFA        string `json:"pfa,omitempty"`
	PIN        string `json:"pin,omitempty"`
}

func scheduleKind(authority string) string {
	switch authority {
	case "STATE_IRS":
		return "paye"
	case "PENCOM_PFA":
		return "pension"
	case "NHF":
		return "nhf"
	case "NHIS":
		return "nhis"
	}
	return strings.ToLower(authority)
}

// lastFour masks a PIN or a PFA to its last four ("•••• 5678").
func lastFour(v string) string {
	r := []rune(strings.TrimSpace(v))
	switch {
	case len(r) == 0:
		return ""
	case len(r) <= 4:
		return "••••"
	}
	return "•••• " + string(r[len(r)-4:])
}

func monthWords(period string) string {
	t, err := time.Parse("2006-01", period)
	if err != nil {
		return period
	}
	return monthNames[t.Month()-1] + " " + t.Format("2006")
}

func dateWords(ts string) string {
	t, err := time.Parse(time.RFC3339, ts)
	if err != nil {
		return dayWords(ts)
	}
	return dayWords(t.Format("2006-01-02"))
}

func viewOf(rm payrolla.Remittance, taxState string) scheduleView {
	v := scheduleView{ID: rm.ID, RunID: rm.RunID, Kind: scheduleKind(rm.Authority), Period: rm.Period, PeriodWords: monthWords(rm.Period),
		TotalKobo: rm.TotalKobo, People: rm.EmployeeCount}
	switch v.Kind {
	case "paye":
		v.Title, v.To = "Tax (PAYE)", "The state's revenue service"
		if taxState != "" {
			v.To = stateWords(taxState) + " Internal Revenue Service"
		}
	case "pension":
		v.Title, v.To = "Pension", "Pension fund administrator"
		if rm.PFACode != "" {
			v.To += " " + lastFour(rm.PFACode)
		}
	case "nhf":
		v.Title, v.To = "Housing fund (NHF)", "Federal Mortgage Bank of Nigeria"
	case "nhis":
		v.Title, v.To = "Health insurance (NHIS)", "National Health Insurance Authority"
	default:
		v.Title, v.To = "Schedule", "As Payrolla has it"
	}
	switch rm.Status {
	case "PAID":
		v.State, v.StateWords = "paid", "Paid"
		if rm.PaidAt != "" {
			v.When = "Paid " + dateWords(rm.PaidAt)
		}
		v.Reference = rm.PaymentRef
	case "PAYABLE", "FILED":
		v.State, v.StateWords = "to_pay", "To pay"
		if rm.Status == "FILED" {
			v.StateWords = "Filed, to pay"
		}
		if rm.DueDate != "" {
			v.When = "Due " + dateWords(rm.DueDate)
		}
	default:
		v.State, v.StateWords = "preparing", "Being prepared"
	}
	return v
}

// getPayrollSchedules is GET /v1/payroll/schedules: each payroll's
// schedules, newest first, to pay before paid.
func (s *Server) getPayrollSchedules(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	p := principalFrom(ctx)
	cl, err := s.reportsClient(ctx, p)
	if err != nil {
		s.payFail(w, r, err)
		return
	}
	list, err := cl.Remittances(ctx, "")
	if err != nil {
		s.payFail(w, r, s.reportsSays(ctx, p, err))
		return
	}
	state := ""
	if st, err := cl.Settings(ctx); err == nil {
		state = st.Company.TaxState
	}
	out := make([]scheduleView, 0, len(list))
	for _, rm := range list {
		out = append(out, viewOf(rm, state))
	}
	order := map[string]int{"paye": 0, "pension": 1, "nhf": 2, "nhis": 3}
	slices.SortStableFunc(out, func(a, b scheduleView) int {
		if a.Period != b.Period {
			return strings.Compare(b.Period, a.Period)
		}
		return order[a.Kind] - order[b.Kind]
	})
	writeJSON(w, http.StatusOK, map[string]any{"schedules": out})
}

// schedule reads one schedule, its lines masked or in full.
func (s *Server) schedule(ctx context.Context, p *Principal, id string, full bool) (scheduleView, []scheduleLine, error) {
	if _, err := uuid.Parse(id); err != nil {
		return scheduleView{}, nil, pgx.ErrNoRows
	}
	cl, err := s.reportsClient(ctx, p)
	if err != nil {
		return scheduleView{}, nil, err
	}
	rm, lines, err := cl.Remittance(ctx, id)
	if err != nil {
		return scheduleView{}, nil, s.reportsSays(ctx, p, err)
	}
	state := ""
	if scheduleKind(rm.Authority) == "paye" {
		if st, err := cl.Settings(ctx); err == nil {
			state = st.Company.TaxState
		}
	}
	out := make([]scheduleLine, 0, len(lines))
	for _, l := range lines {
		x := scheduleLine{EmployeeID: l.EmployeeID, Name: strings.TrimSpace(l.Name), AmountKobo: l.AmountKobo, PFA: lastFour(l.PFA), PIN: lastFour(l.PIN)}
		if full {
			x.PFA, x.PIN = strings.TrimSpace(l.PFA), strings.TrimSpace(l.PIN)
		}
		out = append(out, x)
	}
	return viewOf(rm, state), out, nil
}

// getPayrollSchedule is GET /v1/payroll/schedules/{id}: one schedule with
// a line per person, PINs and PFAs masked.
func (s *Server) getPayrollSchedule(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	v, lines, err := s.schedule(ctx, principalFrom(ctx), chi.URLParam(r, "id"), false)
	if err != nil {
		s.payFail(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]any{"schedule": v, "lines": lines, "masked": v.Kind == "pension"})
}

// schedulePasskey holds a schedule's PINs until the person confirms with a
// passkey asked for payroll.reveal on it; it answers the request and
// returns false otherwise.
func (s *Server) schedulePasskey(w http.ResponseWriter, r *http.Request, proof *credentialJSON, id string) bool {
	p := principalFrom(r.Context())
	if p.UserID == uuid.Nil || p.KeyID != uuid.Nil || p.EndUser != nil {
		writeJSON(w, http.StatusForbidden, map[string]any{"error": "Pension PINs are shown to a person who confirms with a passkey, not to an API key.", "step_up": "passkey"})
		return false
	}
	if proof == nil {
		writeJSON(w, http.StatusForbidden, map[string]any{"error": "Confirm with your passkey.", "step_up": "passkey", "operation": payrollOpReveal, "target": id})
		return false
	}
	if !s.passkeysOn(w) {
		return false
	}
	if _, err := s.verifyPasskey(r.Context(), *proof, "step_up", &p.UserID, stepUpScope(payrollOpReveal, id)); err != nil {
		if passkeyRefused(err) {
			writeJSON(w, http.StatusForbidden, map[string]any{"error": "That passkey did not confirm this. Try again.", "step_up": "passkey", "operation": payrollOpReveal, "target": id})
			return false
		}
		s.fail(w, r, err)
		return false
	}
	return true
}

// scheduleAudit records who saw or took a schedule: never a PIN, a PFA or
// an amount.
func (s *Server) scheduleAudit(r *http.Request, action string, v scheduleView, lines int, confirmed string) {
	if err := s.tx(r, func(tx pgx.Tx) error {
		return auditTx(r, tx, action, "payrolla-schedule:"+v.ID, map[string]any{"kind": v.Kind, "period": v.Period, "people": lines, "confirmed": confirmed})
	}); err != nil {
		s.Logger.Warn("payroll: auditing a schedule", "action", action, "err", err)
	}
}

// revealPayrollSchedule is POST /v1/payroll/schedules/{id}/reveal
// {passkey}: a pension schedule's PINs and PFAs in full; recorded.
func (s *Server) revealPayrollSchedule(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id := chi.URLParam(r, "id")
	var in struct {
		Passkey *credentialJSON `json:"passkey,omitempty"`
	}
	if err := decodeOptional(r, &in); err != nil {
		s.fail(w, r, err)
		return
	}
	// Seen before the passkey is taken: a schedule that is not there, or
	// has nothing hidden, spends no passkey.
	v, _, err := s.schedule(ctx, principalFrom(ctx), id, false)
	if err != nil {
		s.payFail(w, r, err)
		return
	}
	if v.Kind != "pension" {
		writeErr(w, http.StatusBadRequest, "Nothing in this schedule is hidden: it is shown in full.")
		return
	}
	if !s.schedulePasskey(w, r, in.Passkey, id) {
		return
	}
	v, lines, err := s.schedule(ctx, principalFrom(ctx), id, true)
	if err != nil {
		s.payFail(w, r, err)
		return
	}
	s.scheduleAudit(r, "payroll.schedule.revealed", v, len(lines), "passkey")
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]any{"schedule": v, "lines": lines, "masked": false})
}

// downloadPayrollSchedule is POST /v1/payroll/schedules/{id}/download
// {passkey?}: the schedule as a spreadsheet (CSV), made now and kept
// nowhere. A pension schedule's carries the PINs, so it needs the passkey
// as revealing does; each download is recorded.
func (s *Server) downloadPayrollSchedule(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id := chi.URLParam(r, "id")
	var in struct {
		Passkey *credentialJSON `json:"passkey,omitempty"`
	}
	if err := decodeOptional(r, &in); err != nil {
		s.fail(w, r, err)
		return
	}
	v, _, err := s.schedule(ctx, principalFrom(ctx), id, false)
	if err != nil {
		s.payFail(w, r, err)
		return
	}
	pension := v.Kind == "pension"
	if pension && !s.schedulePasskey(w, r, in.Passkey, id) {
		return
	}
	v, lines, err := s.schedule(ctx, principalFrom(ctx), id, pension)
	if err != nil {
		s.payFail(w, r, err)
		return
	}
	var b bytes.Buffer
	cw := csv.NewWriter(&b)
	head := []string{"Name", "Amount (NGN)"}
	if pension {
		head = append(head, "PFA", "Pension PIN")
	}
	_ = cw.Write(head)
	for _, l := range lines {
		row := []string{csvsafe.Text(l.Name), fmt.Sprintf("%d.%02d", l.AmountKobo/100, l.AmountKobo%100)}
		if pension {
			row = append(row, csvsafe.Text(l.PFA), csvsafe.Text(l.PIN))
		}
		_ = cw.Write(row)
	}
	_ = cw.Write([]string{"Total", fmt.Sprintf("%d.%02d", v.TotalKobo/100, v.TotalKobo%100)})
	cw.Flush()
	confirmed := "sign-in"
	if pension {
		confirmed = "passkey"
	}
	s.scheduleAudit(r, "payroll.schedule.downloaded", v, len(lines), confirmed)
	w.Header().Set("Cache-Control", "no-store")
	name := strings.ToLower(strings.NewReplacer(" ", "-", "(", "", ")", "").Replace(v.Title + " " + v.PeriodWords))
	writeJSON(w, http.StatusOK, map[string]any{"filename": name + ".csv", "csv": b.String()})
}
