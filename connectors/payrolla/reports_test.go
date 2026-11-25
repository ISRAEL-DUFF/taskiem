package payrolla_test

import (
	"context"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/israel-duff/taskiem/connectors/payrolla"
	"github.com/israel-duff/taskiem/connectors/payrolla/payrollatest"
)

// The settings and schedule routes the fake serves are Payrolla's, with
// its error codes.
func TestFakeReportsFollowTheOpenAPIFile(t *testing.T) {
	raw, err := os.ReadFile("../../docs/integrations/payrolla-reference/api/payrolla-connect-v1-2026-11-24.yaml")
	if err != nil {
		t.Fatal(err)
	}
	spec := string(raw)
	for _, r := range payrollatest.ReportRoutes() {
		method, path, _ := strings.Cut(r, " ")
		block := regexp.MustCompile(`(?m)^  ` + regexp.QuoteMeta(path) + `:\n((?:    .*\n|\n)*)`).FindStringSubmatch(spec)
		if block == nil || !strings.Contains(block[1], "    "+strings.ToLower(method)+":") {
			t.Errorf("the fake serves %s, which Payrolla's OpenAPI file does not have", r)
		}
	}
	for _, c := range payrollatest.ReportErrorCodes() {
		if !strings.Contains(spec, c) {
			t.Errorf("the fake answers %s, which Payrolla's OpenAPI file does not name", c)
		}
	}
	for _, field := range []string{"paye_tin_set", "separation_of_duties", "funding_model", "pension_employee_rate", "nhis_enabled", "pension_pin", "pension_pfa", "pfa_code", "payroll_run_id"} {
		if !strings.Contains(spec, field) {
			t.Errorf("the client reads %s, which Payrolla's OpenAPI file does not have", field)
		}
	}
}

// The company's setup, and November's PAYE, pension and NHF schedules
// with a line per person; pension lines carry the PFA and PIN, one
// schedule a PFA; without reports:read Payrolla refuses.
func TestSettingsAndSchedules(t *testing.T) {
	f, cl := runWorld(t)
	f.ServeReports()
	f.Runs().SeparationOfDuties = true
	f.Reports().Pension["e0000000-0000-4000-8000-000000000001"] = payrollatest.PensionAccount{PFA: "STANBIC_IBTC", PIN: "PEN100012345678"}
	f.Reports().Pension["e0000000-0000-4000-8000-000000000002"] = payrollatest.PensionAccount{PFA: "STANBIC_IBTC", PIN: "PEN100087654321"}
	f.Reports().Pension["e0000000-0000-4000-8000-000000000003"] = payrollatest.PensionAccount{PFA: "ARM", PIN: "PEN200011112222"}
	ctx := context.Background()

	st, err := cl.Settings(ctx)
	if err != nil || st.Company.PayFrequency != "MONTHLY" || st.Company.TaxState != "Lagos" || !st.Company.SeparationOfDuties || !st.Company.PAYETINSet ||
		st.Statutory == nil || st.Statutory.PensionEmployeeRate != 0.08 || st.Statutory.NHIS {
		t.Fatalf("settings: %+v %v", st, err)
	}

	if list, err := cl.Remittances(ctx, ""); err != nil || len(list) != 0 {
		t.Fatalf("none before a run is locked: %+v %v", list, err)
	}
	run := f.AddLockedRun("2026-11", "")
	list, err := cl.Remittances(ctx, "PAYABLE")
	if err != nil || len(list) != 4 {
		t.Fatalf("schedules: %+v %v", list, err)
	}
	byAuth := map[string][]payrolla.Remittance{}
	for _, r := range list {
		if r.RunID != run || r.Period != "2026-11" {
			t.Fatalf("schedule: %+v", r)
		}
		byAuth[r.Authority] = append(byAuth[r.Authority], r)
	}
	if len(byAuth["STATE_IRS"]) != 1 || len(byAuth["PENCOM_PFA"]) != 2 || len(byAuth["NHF"]) != 1 || len(byAuth["NHIS"]) != 0 {
		t.Fatalf("by authority: %+v", byAuth)
	}
	paye, lines, err := cl.Remittance(ctx, byAuth["STATE_IRS"][0].ID)
	if err != nil || len(lines) != 3 || paye.EmployeeCount != 3 || lines[0].PIN != "" {
		t.Fatalf("PAYE: %+v %+v %v", paye, lines, err)
	}
	var sum int64
	for _, l := range lines {
		sum += l.AmountKobo
	}
	if sum != paye.TotalKobo || sum == 0 {
		t.Fatalf("PAYE lines add up to %d, not %d", sum, paye.TotalKobo)
	}
	for _, p := range byAuth["PENCOM_PFA"] {
		_, lines, err := cl.Remittance(ctx, p.ID)
		if err != nil || len(lines) == 0 || lines[0].PFA != p.PFACode || !strings.HasPrefix(lines[0].PIN, "PEN") {
			t.Fatalf("pension: %+v %+v %v", p, lines, err)
		}
	}
	if paid, _ := cl.Remittances(ctx, "PAID"); len(paid) != 0 {
		t.Fatalf("paid: %+v", paid)
	}
	f.Reports().Paid[run] = true
	if paid, _ := cl.Remittances(ctx, "PAID"); len(paid) != 4 || paid[0].PaidAt == "" {
		t.Fatalf("paid: %+v", paid)
	}
	if _, _, err := cl.Remittance(ctx, "00000000-0000-4000-8000-999999999999"); !payrolla.IsNotFound(err) {
		t.Fatalf("no such schedule: %v", err)
	}

	// The owner allowed less: settings still read, schedules refused.
	f.SetScopes(f.OAuthConn(), "payroll:read", "employees:read")
	if _, err := cl.Remittances(ctx, ""); payrolla.Code(err) != "SCOPE_REQUIRED" {
		t.Fatalf("without reports:read: %v", err)
	}
	if _, err := cl.Settings(ctx); err != nil {
		t.Fatalf("settings need no scope: %v", err)
	}
}
