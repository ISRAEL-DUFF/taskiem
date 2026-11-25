package api_test

import (
	"context"
	"log/slog"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/israel-duff/taskiem/api"
	"github.com/israel-duff/taskiem/connectors/payrolla"
	"github.com/israel-duff/taskiem/connectors/payrolla/payrollatest"
	"github.com/israel-duff/taskiem/engine/ingest"
)

// Staff and pay from Taskiem (P4b-2) against the fake Payrolla: adding
// someone from the staff with their account checked, changing their pay
// and details, a changed account held and approved with a code by another
// approver, rejected with a note, an employee's own change from My pay,
// ending employment, each refusal in words, the events telling the right
// people, and nothing of bank numbers kept.
func TestPayrollTeam(t *testing.T) {
	w := newPayrollaWorld(t)
	f := w.fake
	team := payrollatest.NewTeam(f)
	team.AddAccount("058", "0123456789", "MUSA I. ALI")
	team.AddAccount("044", "2222222222", "MUSA I. ALI")
	team.AddAccount("057", "3333333333", "MUSA IBRAHIM ALI")
	team.AddAccount("033", "4444444444", "ADA OBI")
	owner := w.tenant(t, "Amara Foods", "ngozi@amara.test")
	musa := w.member(t, owner, "musa@amara.test", "admin")
	owner.must(201, "PUT", "/v1/roles/payroll", map[string]any{"permissions": []string{"payroll.read", "payroll.approve"}})
	owner.must(201, "PUT", "/v1/roles/preparer", map[string]any{"permissions": []string{"payroll.prepare"}})
	ada := w.member(t, owner, "ada@amara.test", "payroll")
	tunde := w.member(t, owner, "tunde@amara.test", "preparer")
	musaID, adaID, ownerID := personID(t, musa), personID(t, ada), personID(t, owner)

	// Before connecting: plain words.
	if e := owner.must(409, "GET", "/v1/payroll/team", nil); e["code"] != "reconnect" {
		t.Fatalf("not connected: %v", e)
	}
	w.connect(t, owner, map[string]any{"mode": "test"})
	connName := owner.must(200, "GET", "/v1/payroll/connection", nil)["connection"].(map[string]any)["name"].(string)
	ngoziA, adaA := "a0000000-0000-4000-8000-000000000001", "a0000000-0000-4000-8000-000000000003"
	f.AddApprover(payrollatest.Approver{UserID: ngoziA, Name: "Ngozi Eze", Email: "ngozi@amara.test", Role: "OWNER", CanApprove: true, CanPay: true, CanConfirm: true, LinkedAs: payrolla.ActingUser(ownerID)})
	f.AddApprover(payrollatest.Approver{UserID: adaA, Name: "Ada Obi", Email: "ada@amara.test", Role: "APPROVER", CanApprove: true, CanConfirm: true, LinkedAs: payrolla.ActingUser(adaID)})

	// Who may: musa (admin) has no payroll permission; Tunde prepares.
	musa.must(403, "GET", "/v1/payroll/team", nil)
	got := tunde.must(200, "GET", "/v1/payroll/team", nil)
	if may := got["may"].(map[string]any); may["prepare"] != true || may["approve"] != false || may["see_pay"] != true || len(got["team"].([]any)) != 0 {
		t.Fatalf("tunde's team: %v", got)
	}
	staff := got["staff"].([]any)
	if len(staff) != 4 {
		t.Fatalf("staff not yet in Payrolla: %v", staff)
	}
	if banks := owner.must(200, "GET", "/v1/payroll/banks", nil)["banks"].([]any); len(banks) < 25 {
		t.Fatalf("banks: %v", banks)
	}
	// No payments connection: Payrolla names the account when it is saved.
	if nc := tunde.must(200, "POST", "/v1/payroll/name-check", map[string]any{"bank_code": "058", "account_number": "0123456789"}); nc["checked"] != false {
		t.Fatalf("name check: %v", nc)
	}
	tunde.must(400, "POST", "/v1/payroll/name-check", map[string]any{"bank_code": "999", "account_number": "0123456789"})

	// Add Musa to payroll from the staff.
	add := map[string]any{"key": "add-1", "first_name": "Musa", "last_name": "Ali", "phone": "0801 111 2201", "start_date": "2026-12-01", "department": "Kitchen",
		"pay":  map[string]any{"gross_kobo": 45000000, "basic_kobo": 30000000, "housing_kobo": 10000000, "pensionable": true},
		"bank": map[string]any{"bank_code": "058", "account_number": "0000000001"}, "staff_user": musaID}
	musa.must(403, "POST", "/v1/payroll/team", add)
	ada.must(403, "POST", "/v1/payroll/team", add)
	if e := tunde.must(422, "POST", "/v1/payroll/team", add); e["code"] != "invalid_bank" || e["error"] != "The bank has no such account. Check the number and the bank." {
		t.Fatalf("invalid bank: %v", e)
	}
	add["bank"], add["key"] = map[string]any{"bank_code": "058", "account_number": "012 345 6789"}, "add-2"
	noContact := map[string]any{}
	for k, v := range add {
		noContact[k] = v
	}
	noContact["phone"] = ""
	if e := tunde.must(400, "POST", "/v1/payroll/team", noContact); e["code"] != "contact" {
		t.Fatalf("no contact: %v", e)
	}
	tooMuch := map[string]any{}
	for k, v := range add {
		tooMuch[k] = v
	}
	tooMuch["pay"] = map[string]any{"gross_kobo": 100, "basic_kobo": 200}
	tunde.must(400, "POST", "/v1/payroll/team", tooMuch)
	added := tunde.must(201, "POST", "/v1/payroll/team", add)["employee"].(map[string]any)
	emp := added["id"].(string)
	if b := added["bank"].(map[string]any); b["account_name"] != "MUSA I. ALI" || b["last4"] != "6789" || b["bank"] != "Guaranty Trust Bank" ||
		added["staff"].(map[string]any)["user_id"] != musaID.String() || added["pay"].(map[string]any)["gross_kobo"] != float64(45000000) {
		t.Fatalf("added: %v", added)
	}
	if e := team.Employee(emp); e.ExternalRef != payrolla.ActingUser(musaID) || e.Phone != "+2348011112201" {
		t.Fatalf("in Payrolla: %+v", e)
	}
	// The same staff member again: already in Payrolla, with who to open.
	add["key"] = "add-3"
	if e := tunde.must(409, "POST", "/v1/payroll/team", add); e["code"] != "already_in_payrolla" || e["employee_id"] != emp || e["error"] != "This person is already in Payrolla." {
		t.Fatalf("again: %v", e)
	}
	// The staff link is P4a's own.
	if st := owner.must(200, "GET", "/v1/payroll/staff", nil); !strings.Contains(string(jsonOf(t, st["staff"])), emp) {
		t.Fatalf("staff link: %v", st)
	}

	// Change pay from a date, and the department; never the phone or a
	// job title.
	ch := tunde.must(200, "PATCH", "/v1/payroll/team/"+emp, map[string]any{"key": "c-1", "department": "Sales", "pay": map[string]any{"gross_kobo": 50000000, "from": "2027-01-01"}})
	if ch["words"] != "Their new pay starts 1 January 2027." || ch["employee"].(map[string]any)["department"] != "Sales" {
		t.Fatalf("changed: %v", ch)
	}
	if e := tunde.must(422, "PATCH", "/v1/payroll/team/"+emp, map[string]any{"key": "c-2", "phone": "+2348099999999"}); e["error"] != "A phone number can't be changed from Taskiem. Change it in Payrolla." {
		t.Fatalf("phone: %v", e)
	}
	if e := tunde.must(422, "PATCH", "/v1/payroll/team/"+emp, map[string]any{"key": "c-3", "job_title": "Chef"}); e["code"] != "job_title" {
		t.Fatalf("job title: %v", e)
	}
	tunde.must(400, "PATCH", "/v1/payroll/team/"+emp, map[string]any{"key": "c-4"})
	// Pay is shown to payroll.read only: Ada has it, Musa's own My pay never shows it.
	if row := ada.must(200, "GET", "/v1/payroll/team/"+emp, nil)["employee"].(map[string]any); row["pay"] == nil {
		t.Fatalf("pay to payroll.read: %v", row)
	}

	// The owner asks for a new account for Musa: held for another approver.
	held := owner.must(200, "POST", "/v1/payroll/team/"+emp+"/bank", map[string]any{"key": "b-1", "bank_code": "044", "account_number": "2222222222"})
	if held["words"] != "Held until another approver approves it. They're paid into ••6789 until then." ||
		held["employee"].(map[string]any)["waiting_change"].(map[string]any)["last4"] != "2222" {
		t.Fatalf("held: %v", held)
	}
	changes := ada.must(200, "GET", "/v1/payroll/bank-changes", nil)["changes"].([]any)
	c1 := changes[0].(map[string]any)
	if len(changes) != 1 || c1["employee"] != "Musa Ali" || c1["account_name"] != "MUSA I. ALI" || c1["bank"] != "Access Bank" || c1["via"] != "in_taskiem" ||
		c1["asked_by"] == "" || c1["mine"] != false {
		t.Fatalf("waiting: %v", changes)
	}
	change := c1["id"].(string)
	if mine := owner.must(200, "GET", "/v1/payroll/bank-changes", nil)["changes"].([]any)[0].(map[string]any); mine["mine"] != true {
		t.Fatalf("mine: %v", mine)
	}
	// Payrolla's event: the linked approvers other than the owner are told.
	tenant := uuid.MustParse(tenantOf(t, owner))
	heard := api.PayrollaTeamHeard(w.env.DB.App, w.env.Vault, w.env.Registry, w.srv.Egress, slog.New(slog.DiscardHandler))
	// What reads Payrolla happens after the delivery is answered (its
	// request's context ended), and is waited for here.
	hear := func(typ string, extra map[string]any) {
		ctx, answered := context.WithCancel(context.Background())
		heard.Heard(ctx, ingest.HeardEvent{Tenant: tenant, Environment: "prod", Connector: "payrolla", Trigger: "connection_event", Connection: connName,
			Event: typ, Body: team.EventBody(typ, emp, extra)})
		answered()
		heard.Wait()
	}
	hear("employee.bank_change_requested", map[string]any{"change_id": change, "requested_via": "connect"})
	hear("employee.bank_change_requested", map[string]any{"change_id": change, "requested_via": "connect"})
	if n := told(t, w, adaID, "Account change waiting"); n != 1 {
		t.Fatalf("Ada told %d times", n)
	}
	if n := told(t, w, ownerID, "Account change waiting"); n != 0 {
		t.Fatalf("the owner, who asked, told %d times", n)
	}
	if body := noticeBody(t, w, adaID, "Account change waiting"); body != "Musa Ali's salary account change is waiting: MUSA I. ALI, Access Bank ••2222. Asked by Owner in Taskiem." {
		t.Fatalf("card: %q", body)
	}
	// Whoever asked can't approve it.
	if e := owner.must(403, "POST", "/v1/payroll/bank-changes/"+change+"/code", map[string]any{"key": "o-1"}); e["code"] != "same_person" ||
		e["error"] != "You asked for this account change, so someone else must approve it." {
		t.Fatalf("same person: %v", e)
	}
	tunde.must(403, "POST", "/v1/payroll/bank-changes/"+change+"/code", map[string]any{"key": "t-1"})
	sent := ada.must(202, "POST", "/v1/payroll/bank-changes/"+change+"/code", map[string]any{"key": "a-1"})
	chID := sent["challenge"].(map[string]any)["challenge_id"]
	if sent["sent_to"] != "Payrolla sent a code to your phone ending 4567." {
		t.Fatalf("sent: %v", sent)
	}
	if e := ada.must(422, "POST", "/v1/payroll/bank-changes/"+change+"/approve", map[string]any{"challenge_id": chID, "code": "000000", "key": "a-2"}); e["code"] != "code_invalid" {
		t.Fatalf("wrong code: %v", e)
	}
	code := team.Code(adaA)
	ok := ada.must(200, "POST", "/v1/payroll/bank-changes/"+change+"/approve", map[string]any{"challenge_id": chID, "code": code[:3] + " " + code[3:], "key": "a-3"})
	if ok["change"].(map[string]any)["via"] != "in_taskiem" || team.Employee(emp).AccountNumber != "2222222222" {
		t.Fatalf("approved: %v", ok)
	}
	if e := ada.must(409, "POST", "/v1/payroll/bank-changes/"+change+"/reject", map[string]any{"note": "late", "key": "a-4"}); e["code"] != "decided" {
		t.Fatalf("decided: %v", e)
	}
	hear("employee.bank_change_approved", map[string]any{"change_id": change})
	if told(t, w, ownerID, "Account change approved") != 1 || told(t, w, musaID, "Account change approved") != 1 {
		t.Fatal("whoever asked and the employee are told it was approved")
	}

	// A change asked in Payrolla, rejected with a note.
	other := team.RequestInPayrolla(emp, "057", "3333333333", ngoziA)
	ada.must(400, "POST", "/v1/payroll/bank-changes/"+other+"/reject", map[string]any{"note": " ", "key": "r-0"})
	if rj := ada.must(200, "POST", "/v1/payroll/bank-changes/"+other+"/reject", map[string]any{"note": "Call Musa first", "key": "r-1"}); rj["words"] != "Rejected. They're still paid into their current account." {
		t.Fatalf("rejected: %v", rj)
	}

	// My pay: Musa asks for his own new account.
	me := musa.must(200, "GET", "/v1/payroll/me", nil)
	if me["linked"] != true || me["me"].(map[string]any)["phone"] != "+2348011112201" || me["me"].(map[string]any)["bank"].(map[string]any)["last4"] != "2222" ||
		strings.Contains(string(jsonOf(t, me)), "gross") {
		t.Fatalf("my pay: %v", me)
	}
	if e := musa.must(409, "POST", "/v1/payroll/me/bank-change", map[string]any{"key": "m-0", "bank_code": "044", "account_number": "2222222222"}); e["error"] != "You're already paid into that account." {
		t.Fatalf("same account: %v", e)
	}
	start := musa.must(202, "POST", "/v1/payroll/me/bank-change", map[string]any{"key": "m-1", "bank_code": "057", "account_number": "3333333333"})
	if start["account_name"] != "MUSA IBRAHIM ALI" || start["last4"] != "3333" || start["sent_to"] != "Payrolla sent a code to your phone ending 2201." {
		t.Fatalf("my code: %v", start)
	}
	mch := start["challenge"].(map[string]any)["challenge_id"]
	if e := musa.must(410, "POST", "/v1/payroll/me/bank-change/confirm", map[string]any{"challenge_id": "20000000-0000-4000-8000-0000000000ff", "code": "123456", "key": "m-2"}); e["code"] != "code_expired" {
		t.Fatalf("no such code: %v", e)
	}
	if e := musa.must(422, "POST", "/v1/payroll/me/bank-change/confirm", map[string]any{"challenge_id": mch, "code": "000000", "key": "m-3"}); e["code"] != "code_invalid" {
		t.Fatalf("wrong: %v", e)
	}
	mine := musa.must(200, "POST", "/v1/payroll/me/bank-change/confirm", map[string]any{"challenge_id": mch, "code": team.Code(emp), "key": "m-4"})
	if mine["words"] != "Held for your employer to approve. You're paid into ••2222 until then." || mine["change"].(map[string]any)["via"] != "by_employee" {
		t.Fatalf("held for the employer: %v", mine)
	}
	if e := musa.must(410, "POST", "/v1/payroll/me/bank-change/confirm", map[string]any{"challenge_id": mch, "code": team.Code(emp), "key": "m-5"}); e["code"] != "code_used" {
		t.Fatalf("used: %v", e)
	}
	musa.must(202, "POST", "/v1/payroll/me/bank-change", map[string]any{"key": "m-6", "bank_code": "057", "account_number": "3333333333"})
	musa.must(202, "POST", "/v1/payroll/me/bank-change", map[string]any{"key": "m-7", "bank_code": "057", "account_number": "3333333333"})
	if e := musa.must(429, "POST", "/v1/payroll/me/bank-change", map[string]any{"key": "m-8", "bank_code": "057", "account_number": "3333333333"}); e["error"] != "Payrolla has sent you too many codes. Try again in 30 minutes." {
		t.Fatalf("too many: %v", e)
	}
	// The employee's own request: told to the approvers as theirs.
	var own string
	for _, c := range team.BankChanges() {
		if c.Via == "EMPLOYEE" && c.Status == "PENDING" {
			own = c.ID
		}
	}
	hear("employee.bank_change_requested", map[string]any{"change_id": own, "requested_via": "employee"})
	if !strings.Contains(noticeBody(t, w, ownerID, "Account change waiting"), "Asked by Musa themselves.") {
		t.Fatal("the owner is told the employee asked")
	}
	if ch := ada.must(200, "GET", "/v1/payroll/bank-changes", nil)["changes"].([]any)[0].(map[string]any); ch["via"] != "by_employee" {
		t.Fatalf("by the employee: %v", ch)
	}

	// Ada joins payroll with an email only: Payrolla can't text her a code.
	tunde.must(201, "POST", "/v1/payroll/team", map[string]any{"key": "add-ada", "first_name": "Ada", "last_name": "Obi", "email": "ada@amara.test", "start_date": "2026-12-01",
		"pay": map[string]any{"gross_kobo": 30000000}, "bank": map[string]any{"bank_code": "033", "account_number": "4444444444"}, "staff_user": adaID})
	if e := ada.must(422, "POST", "/v1/payroll/me/bank-change", map[string]any{"key": "d-1", "bank_code": "057", "account_number": "3333333333"}); e["code"] != "unreachable" {
		t.Fatalf("unreachable: %v", e)
	}
	// Tunde is not linked: no My pay.
	if got := tunde.must(200, "GET", "/v1/payroll/me", nil); got["linked"] != false {
		t.Fatalf("not linked: %v", got)
	}

	// Ending Musa's employment: at once, said so; then his account can't change.
	if e := tunde.must(400, "POST", "/v1/payroll/team/"+emp+"/end", map[string]any{"key": "e-1"}); !strings.Contains(e["error"].(string), "This takes effect today.") {
		t.Fatalf("end without yes: %v", e)
	}
	if ended := tunde.must(200, "POST", "/v1/payroll/team/"+emp+"/end", map[string]any{"key": "e-2", "confirm": "yes"}); ended["employee"].(map[string]any)["status"] != "left" {
		t.Fatalf("ended: %v", ended)
	}
	if e := musa.must(409, "POST", "/v1/payroll/me/bank-change", map[string]any{"key": "m-9", "bank_code": "033", "account_number": "4444444444"}); e["error"] != "This person has left, so their account can't be changed." {
		t.Fatalf("left: %v", e)
	}

	// Musa leaves the business in Taskiem: the owner is asked about Payrolla.
	owner.must(204, "POST", "/v1/staff/"+musaID.String()+"/remove", nil)
	if told(t, w, ownerID, "Still in Payrolla") != 1 {
		t.Fatal("the owner is asked whether to end Musa's employment in Payrolla")
	}

	// Every write said who did it and once; nothing of an account number
	// is kept in Taskiem.
	for _, wr := range f.Writes() {
		if wr.Key == "" || !strings.HasPrefix(wr.ActingUser, "tsk_user_") || strings.Contains(wr.Key, "add-") {
			t.Fatalf("write %+v", wr)
		}
		if strings.HasSuffix(wr.Path, "/bank-change") && wr.ActingUser != payrolla.ActingUser(musaID) && wr.ActingUser != payrolla.ActingUser(adaID) {
			t.Fatalf("an employee's own change acts as them: %+v", wr)
		}
	}
	for _, q := range []string{`SELECT count(*) FROM audit_log WHERE detail::text ~ '(0123456789|2222222222|3333333333|4444444444)'`,
		`SELECT count(*) FROM alerts WHERE body ~ '(0123456789|2222222222|3333333333|4444444444|45000000|50000000)'`,
		`SELECT count(*) FROM notifications WHERE body ~ '(0123456789|2222222222|3333333333|4444444444)'`} {
		var n int
		if err := w.env.DB.Admin.QueryRow(context.Background(), q).Scan(&n); err != nil || n != 0 {
			t.Fatalf("%s: %d %v", q, n, err)
		}
	}
}

// told counts a person's notifications with this title.
func told(t *testing.T, w *payrollaWorld, user uuid.UUID, title string) int {
	t.Helper()
	var n int
	if err := w.env.DB.Admin.QueryRow(context.Background(), `SELECT count(*) FROM notifications WHERE user_id = $1 AND title = $2`, user, title).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func noticeBody(t *testing.T, w *payrollaWorld, user uuid.UUID, title string) string {
	t.Helper()
	var b string
	_ = w.env.DB.Admin.QueryRow(context.Background(), `SELECT body FROM notifications WHERE user_id = $1 AND title = $2 ORDER BY updated_at DESC LIMIT 1`, user, title).Scan(&b)
	return b
}
