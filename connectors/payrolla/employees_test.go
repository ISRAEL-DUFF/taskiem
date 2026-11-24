package payrolla_test

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/israel-duff/taskiem/connectors/payrolla"
	"github.com/israel-duff/taskiem/connectors/payrolla/payrollatest"
)

// The fake's employees and bank changes are Payrolla's OpenAPI file's.
func TestTeamFollowsTheOpenAPIFile(t *testing.T) {
	raw, err := os.ReadFile("../../docs/integrations/payrolla-reference/api/payrolla-connect-v1-2026-11-24.yaml")
	if err != nil {
		t.Fatal(err)
	}
	spec := string(raw)
	for _, r := range payrollatest.TeamRoutes() {
		method, path, _ := strings.Cut(r, " ")
		block := regexp.MustCompile(`(?m)^  ` + regexp.QuoteMeta(path) + `:\n((?:    .*\n|\n)*)`).FindStringSubmatch(spec)
		if block == nil || !strings.Contains(block[1], "    "+strings.ToLower(method)+":") {
			t.Errorf("the fake serves %s, which Payrolla's OpenAPI file does not have", r)
		}
	}
	for _, c := range payrollatest.TeamErrorCodes() {
		if !strings.Contains(spec, c) {
			t.Errorf("the fake answers %s, which Payrolla's OpenAPI file does not name", c)
		}
	}
	for _, field := range []string{"pending_bank_change", "account_name", "changed_fields", "requested_via", "effective_from"} {
		if !strings.Contains(spec, field) {
			t.Errorf("Payrolla's OpenAPI file no longer has %s", field)
		}
	}
}

func teamClient(t *testing.T) (*payrollatest.Fake, *payrollatest.Team, *payrolla.Client) {
	t.Helper()
	f := payrollatest.New(taskiemApp)
	t.Cleanup(f.Close)
	team := payrollatest.NewTeam(f)
	team.AddAccount("058", "0123456789", "ZAINAB A. BELLO")
	team.AddAccount("044", "2222222222", "ZAINAB A. BELLO")
	st := &store{creds: connectOAuth(t, f)}
	return f, team, payrolla.NewClient(newConnector(f), st.get(), http.DefaultClient, st.rotate)
}

// Adding someone with their account checked, changing their pay and
// details (never the phone or a job title), and ending their employment.
func TestEmployees(t *testing.T) {
	f, team, cl := teamClient(t)
	ctx := context.Background()
	me := uuid.MustParse("5e000000-0000-4000-8000-000000000001")
	staff := uuid.MustParse("5e000000-0000-4000-8000-000000000002")
	w := func(key string) payrolla.Write { return payrolla.Write{Key: key, User: me, Name: "Ngozi Eze"} }
	yes := true

	in := payrolla.NewEmployee{FirstName: "Zainab", LastName: "Bello", Phone: "+2348011112201", StartDate: "2026-12-01", Department: "Operations",
		ExternalRef: payrolla.StaffRef(staff), Pay: &payrolla.PayInput{GrossKobo: 45000000, BasicKobo: 30000000, Pensionable: &yes},
		Bank: &payrolla.BankInput{BankCode: "058", AccountNumber: "0123456789"}}
	// The bank has no such account.
	bad := in
	bad.Bank = &payrolla.BankInput{BankCode: "058", AccountNumber: "0000000001"}
	if _, err := cl.AddEmployee(ctx, bad, w("a0")); payrolla.Code(err) != "INVALID_BANK" {
		t.Fatalf("invalid bank: %v", err)
	}
	noContact := in
	noContact.Phone = ""
	if _, err := cl.AddEmployee(ctx, noContact, w("a1")); payrolla.Code(err) != "NO_CONTACT_DETAILS" {
		t.Fatalf("no contact: %v", err)
	}
	e, err := cl.AddEmployee(ctx, in, w("a2"))
	if err != nil || e.ID == "" || e.Bank == nil || e.Bank.AccountName != "ZAINAB A. BELLO" || e.Bank.Last4 != "6789" || e.Pay == nil || e.Pay.GrossKobo != 45000000 ||
		e.ExternalRef != "tsk_user_"+staff.String() || payrolla.RefUser(e.ExternalRef) != staff {
		t.Fatalf("added: %+v %v", e, err)
	}
	if _, err := cl.AddEmployee(ctx, in, w("a3")); payrolla.Code(err) != "EXTERNAL_REF_TAKEN" {
		t.Fatalf("taken: %v", err)
	}
	if got, err := cl.EmployeeByRef(ctx, payrolla.StaffRef(staff)); err != nil || got == nil || got.ID != e.ID {
		t.Fatalf("by ref: %v %v", got, err)
	}
	// A pay change from a date, and a new department.
	dept := "Sales"
	ch, err := cl.ChangeEmployee(ctx, e.ID, payrolla.EmployeeChange{Department: &dept, Pay: &payrolla.PayInput{GrossKobo: 50000000, EffectiveFrom: "2027-01-01"}}, w("c1"))
	if err != nil || ch.Department != "Sales" || ch.Pay.GrossKobo != 50000000 || !strings.HasPrefix(ch.Pay.EffectiveFrom, "2027-01-01") {
		t.Fatalf("changed: %+v %v", ch, err)
	}
	// A new account is held, the old one paid meanwhile.
	ch, err = cl.ChangeEmployee(ctx, e.ID, payrolla.EmployeeChange{Bank: &payrolla.BankInput{BankCode: "044", AccountNumber: "2222222222"}}, w("c2"))
	if err != nil || ch.Bank.Last4 != "6789" || ch.PendingBank == nil || ch.PendingBank.Last4 != "2222" || ch.PendingBank.Status != "PENDING" || ch.PendingBank.RequestedVia != "CONNECT" {
		t.Fatalf("held: %+v %v", ch, err)
	}
	list, err := cl.Employees(ctx, "", 500)
	if err != nil || len(list) != 1 || list[0].PendingBank == nil {
		t.Fatalf("list: %v %v", list, err)
	}
	if _, err := cl.Employee(ctx, "00000000-0000-4000-8000-0000000000ff"); !payrolla.IsNotFound(err) {
		t.Fatalf("not found: %v", err)
	}
	ended, err := cl.EndEmployment(ctx, e.ID, w("t1"))
	if err != nil || ended.Status != "TERMINATED" || team.Employee(e.ID).Status != "TERMINATED" {
		t.Fatalf("ended: %+v %v", ended, err)
	}
	for _, wr := range f.Writes() {
		if wr.Key == "" || wr.ActingUser != "tsk_user_"+me.String() {
			t.Fatalf("a write without its headers: %+v", wr)
		}
	}
}

// AllEmployees reads every page (next_cursor), up to AllEmployeesPages;
// Employees stops at max.
func TestAllEmployeesPages(t *testing.T) {
	_, team, cl := teamClient(t)
	ctx := context.Background()
	team.SetPageSize(2)
	for i := range 7 {
		team.AddEmployee(payrollatest.Employee{FirstName: fmt.Sprintf("Worker%d", i), LastName: "Ade", Phone: "+234801111220" + fmt.Sprint(i)})
	}
	all, err := cl.AllEmployees(ctx, "")
	if err != nil || len(all) != 7 || all[6].FirstName != "Worker6" || team.Listed() != 4 {
		t.Fatalf("all: %d %v, %d pages", len(all), err, team.Listed())
	}
	if some, err := cl.Employees(ctx, "", 3); err != nil || len(some) != 3 {
		t.Fatalf("max: %d %v", len(some), err)
	}
	// A company past the bound: cut off at AllEmployeesPages pages.
	team.SetPageSize(1)
	for i := range payrolla.AllEmployeesPages {
		team.AddEmployee(payrollatest.Employee{FirstName: fmt.Sprintf("More%d", i), LastName: "Ade", Phone: "+2348011113" + fmt.Sprintf("%03d", i)})
	}
	before := team.Listed()
	if all, err := cl.AllEmployees(ctx, ""); err != nil || len(all) != payrolla.AllEmployeesPages || team.Listed()-before != payrolla.AllEmployeesPages {
		t.Fatalf("bounded: %d %v", len(all), err)
	}
}

// A held bank change: approved by another approver with their code;
// whoever asked can't approve it unless they are the only approver;
// rejected with a note.
func TestBankChangeApproval(t *testing.T) {
	f, team, cl := teamClient(t)
	ctx := context.Background()
	asker := uuid.MustParse("5e000000-0000-4000-8000-000000000001")
	approver := uuid.MustParse("5e000000-0000-4000-8000-000000000003")
	f.AddApprover(payrollatest.Approver{UserID: "a0000000-0000-4000-8000-000000000001", Name: "Ngozi Eze", Role: "OWNER", CanApprove: true, CanConfirm: true, LinkedAs: payrolla.ActingUser(asker)})
	f.AddApprover(payrollatest.Approver{UserID: "a0000000-0000-4000-8000-000000000002", Name: "Ada Obi", Role: "APPROVER", CanApprove: true, CanConfirm: true, LinkedAs: payrolla.ActingUser(approver)})
	emp := team.AddEmployee(payrollatest.Employee{FirstName: "Zainab", LastName: "Bello", Phone: "+2348011112201", BankCode: "058", AccountNumber: "0123456789"})
	if _, err := cl.ChangeEmployee(ctx, emp, payrolla.EmployeeChange{Bank: &payrolla.BankInput{BankCode: "044", AccountNumber: "2222222222"}},
		payrolla.Write{Key: "b1", User: asker, Name: "Ngozi Eze"}); err != nil {
		t.Fatal(err)
	}
	waiting, err := cl.BankChanges(ctx)
	if err != nil || len(waiting) != 1 || waiting[0].AccountName != "ZAINAB A. BELLO" || waiting[0].Last4 != "2222" {
		t.Fatalf("waiting: %v %v", waiting, err)
	}
	id := waiting[0].ID
	if _, err := cl.StartBankChangeApproval(ctx, id, payrolla.Write{Key: "b2", User: asker, Name: "Ngozi Eze"}); payrolla.Code(err) != "SAME_PERSON" {
		t.Fatalf("same person: %v", err)
	}
	stranger := uuid.MustParse("5e000000-0000-4000-8000-000000000009")
	if _, err := cl.StartBankChangeApproval(ctx, id, payrolla.Write{Key: "b3", User: stranger, Name: "Musa"}); payrolla.Code(err) != "APPROVER_NOT_LINKED" {
		t.Fatalf("not linked: %v", err)
	}
	aw := func(key string) payrolla.Write { return payrolla.Write{Key: key, User: approver, Name: "Ada Obi"} }
	c, err := cl.StartBankChangeApproval(ctx, id, aw("b4"))
	if err != nil || c.ID == "" {
		t.Fatalf("code: %v %v", c, err)
	}
	if _, err := cl.ApproveBankChange(ctx, id, c.ID, "000000", aw("b5")); payrolla.Code(err) != "CODE_INVALID" {
		t.Fatalf("wrong code: %v", err)
	}
	done, err := cl.ApproveBankChange(ctx, id, c.ID, team.Code("a0000000-0000-4000-8000-000000000002"), aw("b6"))
	if err != nil || done.Status != "APPROVED" || team.Employee(emp).AccountNumber != "2222222222" {
		t.Fatalf("approved: %v %v", done, err)
	}
	if _, err := cl.RejectBankChange(ctx, id, "not hers", aw("b7")); payrolla.Code(err) != "INVALID_STATE" {
		t.Fatalf("decided: %v", err)
	}
	// Another change, rejected with a note.
	id2 := team.RequestInPayrolla(emp, "058", "0123456789", "a0000000-0000-4000-8000-000000000001")
	if rj, err := cl.RejectBankChange(ctx, id2, "Call Zainab first", aw("b8")); err != nil || rj.Status != "REJECTED" {
		t.Fatalf("rejected: %v %v", rj, err)
	}
}

// An employee asks for their own new account: Payrolla texts them a code
// naming it; it is then held like any other.
func TestOwnBankChange(t *testing.T) {
	f, team, cl := teamClient(t)
	ctx := context.Background()
	me := uuid.MustParse("5e000000-0000-4000-8000-000000000007")
	w := func(key string) payrolla.Write { return payrolla.Write{Key: key, User: me, Name: "Zainab Bello"} }
	emp := team.AddEmployee(payrollatest.Employee{FirstName: "Zainab", LastName: "Bello", Phone: "+2348011117801", BankCode: "058", AccountNumber: "0123456789"})
	noPhone := team.AddEmployee(payrollatest.Employee{FirstName: "Musa", LastName: "Ali", Email: "musa@amara.test"})
	left := team.AddEmployee(payrollatest.Employee{FirstName: "Ada", LastName: "Obi", Phone: "+2348011110000", Status: "TERMINATED"})
	want := payrolla.BankInput{BankCode: "044", AccountNumber: "2222222222"}
	for who, code := range map[string]string{noPhone: "EMPLOYEE_UNREACHABLE", left: "EMPLOYEE_NOT_ACTIVE"} {
		if _, err := cl.StartOwnBankChange(ctx, who, want, w("o-"+who)); payrolla.Code(err) != code {
			t.Fatalf("%s: %v", code, err)
		}
	}
	if _, err := cl.StartOwnBankChange(ctx, emp, payrolla.BankInput{BankCode: "058", AccountNumber: "0123456789"}, w("o0")); payrolla.Code(err) != "SAME_ACCOUNT" {
		t.Fatalf("same account: %v", err)
	}
	ch, err := cl.StartOwnBankChange(ctx, emp, want, w("o1"))
	if err != nil || ch.AccountName != "ZAINAB A. BELLO" || ch.Last4 != "2222" || ch.Channel != "SMS" || ch.DestinationHint != "•••• 7801" {
		t.Fatalf("texted: %+v %v", ch, err)
	}
	if _, err := cl.ConfirmOwnBankChange(ctx, emp, "20000000-0000-4000-8000-0000000000ff", "123456", w("o2")); !payrolla.IsNotFound(err) {
		t.Fatalf("no such challenge: %v", err)
	}
	held, err := cl.ConfirmOwnBankChange(ctx, emp, ch.ID, team.Code(emp), w("o3"))
	if err != nil || held.Status != "PENDING" || held.RequestedVia != "EMPLOYEE" {
		t.Fatalf("held: %+v %v", held, err)
	}
	if _, err := cl.ConfirmOwnBankChange(ctx, emp, ch.ID, team.Code(emp), w("o4")); payrolla.Code(err) != "CODE_USED" {
		t.Fatalf("used: %v", err)
	}
	ch, _ = cl.StartOwnBankChange(ctx, emp, want, w("o5"))
	team.ExpireCodes()
	if _, err := cl.ConfirmOwnBankChange(ctx, emp, ch.ID, team.Code(emp), w("o6")); payrolla.Code(err) != "CODE_EXPIRED" {
		t.Fatalf("expired: %v", err)
	}
	// Three codes in thirty minutes.
	if _, err := cl.StartOwnBankChange(ctx, emp, want, w("o7")); err != nil {
		t.Fatal(err)
	}
	if _, err := cl.StartOwnBankChange(ctx, emp, want, w("o8")); payrolla.Code(err) != "TOO_MANY_CODES" {
		t.Fatalf("too many: %v", err)
	}
	for _, wr := range f.Writes() {
		if wr.ActingUser != payrolla.ActingUser(me) {
			t.Fatalf("an employee's change acts as them: %+v", wr)
		}
	}
	// The events carry no account.
	body := team.EventBody("employee.bank_change_requested", emp, map[string]any{"change_id": held.ID, "requested_via": "employee"})
	ev, ok := payrolla.ReadEmployeeEvent(body)
	if !ok || ev.ChangeID != held.ID || ev.RequestedVia != "employee" || ev.Employee.ID != emp || ev.Employee.Bank != nil || strings.Contains(string(body), "2222") {
		t.Fatalf("event: %+v %s", ev, body)
	}
	if _, ok := payrolla.ReadEmployeeEvent([]byte(`{"type":"run.computed","data":{}}`)); ok {
		t.Fatal("a run event read as an employee's")
	}
}
