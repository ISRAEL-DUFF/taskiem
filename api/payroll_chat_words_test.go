package api_test

import (
	"context"
	"fmt"
	"net/netip"
	"strings"
	"testing"

	"github.com/israel-duff/taskiem/connectors/payrolla"
	"github.com/israel-duff/taskiem/connectors/payrolla/payrollatest"
	"github.com/israel-duff/taskiem/engine/egress"
	"github.com/israel-duff/taskiem/engine/invoicing"
	"github.com/israel-duff/taskiem/engine/whatsapp/whatsapptest"
)

// said, yesTo and oneBare are the replies without the business's name
// before them.
func said(t *testing.T, w *payrollaWorld, num, text string) string {
	t.Helper()
	return strings.TrimPrefix(w.say(t, num, text), "[Amara Foods] ")
}

func yesTo(t *testing.T, w *payrollaWorld, num string) string {
	t.Helper()
	return strings.TrimPrefix(w.yes(t, num), "[Amara Foods] ")
}

func oneBare(t *testing.T, got []whatsapptest.Sent) whatsapptest.Sent {
	t.Helper()
	m := one(t, got)
	m.Text = strings.TrimPrefix(m.Text, "[Amara Foods] ")
	return m
}

// namedMember adds someone to the business with a name, as the owner does.
func namedMember(t *testing.T, w *payrollaWorld, owner *client, email, name string, roles ...string) *client {
	t.Helper()
	owner.must(201, "POST", "/v1/members", map[string]any{"email": email, "name": name, "password": testPassword, "roles": roles})
	return w.login(t, email, testPassword)
}

// This month's changes said in the chat (the leftovers of P4b-3): a bonus,
// a deduction (its reason asked for), leaving someone out and putting them
// back, each asked yes with the person's take-home pay from Payrolla and
// said again after; no, a name no one has, a payroll not open for
// changes; and who may.
func TestPayrollChatRunChanges(t *testing.T) {
	ctx := context.Background()
	w := newPayrollaWorld(t)
	f := w.fake
	payrollWorkers(f)
	owner := w.tenant(t, "Amara Foods", "ngozi@amara.test")
	w.connect(t, owner, map[string]any{"mode": "test"})
	owner.must(201, "PUT", "/v1/roles/payroll", map[string]any{"permissions": []string{"payroll.read", "payroll.approve"}})
	owner.must(201, "PUT", "/v1/roles/clerk", map[string]any{"permissions": []string{"payroll.prepare"}})
	musa := w.member(t, owner, "musa@amara.test", "payroll")
	clerk := w.member(t, owner, "clerk@amara.test", "clerk")
	cashier := w.member(t, owner, "cashier@amara.test", "cashier")
	linkPayrolla(t, f, musa, musaPy)

	clerkNum, musaNum, cashNum := "+2348031119101", "+2348031119102", "+2348031119103"
	w.bind(t, clerk, clerkNum)
	w.bind(t, musa, musaNum)
	w.bind(t, cashier, cashNum)

	// Nothing prepared yet.
	if s := said(t, w, clerkNum, "bonus 100k to zainab for q4 target"); !strings.Contains(s, "There's no payroll open for changes. Say: prepare ") {
		t.Fatalf("none open: %q", s)
	}
	run := owner.must(201, "POST", "/v1/payroll/runs", map[string]any{"period": "2026-11", "key": "p"})["run"].(map[string]any)["run_id"].(string)
	view := owner.must(200, "GET", "/v1/payroll/runs/"+run, nil)
	before := lineByName(t, view, "Zainab Bello")["net_kobo"].(float64)

	// A bonus: asked yes with Zainab's take-home now; nothing sent before yes.
	ask := oneBare(t, w.send(t, clerkNum, whatsapptest.Text("bonus 100k to zainab for q4 target")))
	if !strings.Contains(ask.Text, "Add a ₦100,000 bonus for Zainab Bello in November 2026 payroll, for q4 target?") || !strings.Contains(ask.Text, "goes up by less than that") ||
		len(ask.Buttons) != 2 || !strings.HasPrefix(ask.Buttons[0].ID, "yes:") {
		t.Fatalf("asked: %+v", ask)
	}
	if adj := owner.must(200, "GET", "/v1/payroll/runs/"+run, nil)["adjustments"].([]any); len(adj) != 0 {
		t.Fatalf("sent before yes: %v", adj)
	}
	done := w.send(t, clerkNum, whatsapptest.ButtonReply(ask.Buttons[0].ID, "Yes"))
	if s := oneBare(t, done).Text; !strings.HasPrefix(s, "Done. Zainab Bello now takes home ₦") || !strings.Contains(s, "(it was "+invoicing.Naira(int64(before))+")") {
		t.Fatalf("done: %q", s)
	}
	view = owner.must(200, "GET", "/v1/payroll/runs/"+run, nil)
	if adj := view["adjustments"].([]any); len(adj) != 1 || adj[0].(map[string]any)["type"] != "bonus" || adj[0].(map[string]any)["name"] != "Zainab Bello" {
		t.Fatalf("the bonus: %v", adj)
	}
	if n := lineByName(t, view, "Zainab Bello")["net_kobo"].(float64); n <= before || n-before >= 10000000 {
		t.Fatalf("the bonus is before tax: %v -> %v", before, n)
	}
	// A Yes pressed again confirms nothing.
	if s := oneBare(t, w.send(t, clerkNum, whatsapptest.ButtonReply(ask.Buttons[0].ID, "Yes"))).Text; strings.HasPrefix(s, "Done.") {
		t.Fatalf("done twice: %q", s)
	}

	// A deduction, said no to; then without a reason: asked for it.
	if s := said(t, w, clerkNum, "deduct 20k from musa, advance repaid"); !strings.Contains(s, "Take ₦20,000 off Musa Ali's pay in November 2026 payroll, for advance repaid?") {
		t.Fatalf("deduction: %q", s)
	}
	if s := said(t, w, clerkNum, "no"); s != "Nothing changed." {
		t.Fatalf("no: %q", s)
	}
	if s := said(t, w, clerkNum, "deduct 20k from musa"); s != "What is it for? Say it in a few words, like: Q4 target." {
		t.Fatalf("why: %q", s)
	}
	if s := said(t, w, clerkNum, "Advance repaid"); !strings.Contains(s, "for Advance repaid? Their take-home pay is ₦") {
		t.Fatalf("with why: %q", s)
	}
	if s := yesTo(t, w, clerkNum); !strings.HasPrefix(s, "Done. Musa Ali now takes home ₦") {
		t.Fatalf("deducted: %q", s)
	}

	// Ada left out this month, and put back.
	w.srv.ResetLimiters()
	if s := said(t, w, clerkNum, "leave out ada this month, unpaid leave"); !strings.Contains(s, "Leave Ada Obi out of November 2026 payroll, for unpaid leave? They're paid nothing this month instead of ₦") {
		t.Fatalf("leave out: %q", s)
	}
	if s := yesTo(t, w, clerkNum); !strings.HasPrefix(s, "Done. Ada Obi is left out of November 2026 payroll. It now pays 2 people ₦") {
		t.Fatalf("left out: %q", s)
	}
	if ex := owner.must(200, "GET", "/v1/payroll/runs/"+run, nil)["exclusions"].([]any); len(ex) != 1 || ex[0].(map[string]any)["reason"] != "unpaid leave" {
		t.Fatalf("exclusions: %v", ex)
	}
	if s := said(t, w, clerkNum, "put zainab back"); s != "zainab isn't left out of November 2026 payroll." {
		t.Fatalf("not left out: %q", s)
	}
	// Payrolla refuses to name who is left out: said, never "not left out".
	f.Refuse("GET", "/connect/employees/"+adaE, 403, "SCOPE_REQUIRED")
	if s := said(t, w, clerkNum, "put ada back"); s != "Ask the owner to allow this in Payrolla." {
		t.Fatalf("name refused: %q", s)
	}
	f.Refuse("GET", "/connect/employees/"+adaE, 0, "")
	w.srv.ResetLimiters()
	if s := said(t, w, clerkNum, "put ada back"); s != "Put Ada Obi back in November 2026 payroll?" {
		t.Fatalf("put back: %q", s)
	}
	if s := yesTo(t, w, clerkNum); !strings.HasPrefix(s, "Done. Ada Obi is back in November 2026 payroll and takes home ₦") {
		t.Fatalf("back: %q", s)
	}
	// Words that ask, while a change waits, let it go.
	said(t, w, clerkNum, "give ada a 5k bonus for cleaning")
	if s := said(t, w, clerkNum, "leave ada out because sick"); !strings.Contains(s, "Leave Ada Obi out of November 2026 payroll, for sick?") {
		t.Fatalf("another change: %q", s)
	}
	said(t, w, clerkNum, "no")
	if s := said(t, w, clerkNum, "bonus 5k to bisi for cleaning"); s != "There's no one called bisi in November 2026 payroll." {
		t.Fatalf("no one: %q", s)
	}
	if s := said(t, w, clerkNum, "bonus 0k to ada for cleaning"); !strings.HasPrefix(s, "That amount doesn't look right.") {
		t.Fatalf("amount: %q", s)
	}

	// Who may: an approver prepares nothing; someone without payroll
	// gets no payroll question.
	if s := said(t, w, musaNum, "bonus 100k to zainab for q4 target"); s != "You can't do that to the payroll here. Ask the owner." {
		t.Fatalf("approver: %q", s)
	}
	writes := len(f.Writes())
	for _, m := range w.send(t, cashNum, whatsapptest.Text("bonus 100k to zainab for q4 target")) {
		if strings.Contains(m.Text, "Zainab") {
			t.Fatalf("the cashier asked: %q", m.Text)
		}
	}
	if s := said(t, w, cashNum, "add zainab to payroll"); s != "You can't do that to the payroll here. Ask the owner." {
		t.Fatalf("cashier add: %q", s)
	}
	if len(f.Writes()) != writes {
		t.Fatal("a write for someone who may not")
	}

	// After approval nothing more is open for changes.
	pressed := w.press(t, musaNum, "pr:a:"+run)
	if !strings.Contains(oneBare(t, pressed).Text, "Type it here to approve November 2026 payroll") {
		t.Fatalf("asked: %+v", pressed)
	}
	said(t, w, musaNum, f.Code(musaPy))
	if s := said(t, w, clerkNum, "bonus 10k to zainab for late"); !strings.Contains(s, "There's no payroll open for changes.") {
		t.Fatalf("approved: %q", s)
	}

	// The audit says who changed what, from the chat; never an amount.
	var n, amounts int
	_ = w.env.DB.Admin.QueryRow(ctx, `SELECT count(*) FROM audit_log WHERE action IN ('payroll.run_adjust', 'payroll.run_exclude', 'payroll.run_put_back') AND detail->>'via' = 'whatsapp'`).Scan(&n)
	_ = w.env.DB.Admin.QueryRow(ctx, `SELECT count(*) FROM audit_log WHERE action LIKE 'payroll.%' AND detail::text ~ '(10000000|2000000|100000)'`).Scan(&amounts)
	if n != 4 || amounts != 0 {
		t.Fatalf("audit: %d changes, %d with amounts", n, amounts)
	}
}

// Staff by chat (the leftovers of P4b-2): "add … to payroll" gives the
// form (filled from the staff member, or empty), changing pay from a date
// and the department and ending employment ask yes first, a new salary
// account gives the form, an employee's own gives My pay; and who may.
func TestPayrollChatStaff(t *testing.T) {
	w := newPayrollaWorld(t)
	f := w.fake
	team := payrollatest.NewTeam(f)
	team.AddAccount("058", "0123456789", "MUSA I. ALI")
	team.AddAccount("044", "2222222222", "ZAINAB A. BELLO")
	owner := w.tenant(t, "Amara Foods", "ngozi@amara.test")
	w.connect(t, owner, map[string]any{"mode": "test"})
	owner.must(201, "PUT", "/v1/roles/preparer", map[string]any{"permissions": []string{"payroll.prepare"}})
	tunde := namedMember(t, w, owner, "tunde@amara.test", "Tunde Bayo", "preparer")
	musa := namedMember(t, w, owner, "musa@amara.test", "Musa Ali", "cashier")
	zainab := team.AddEmployee(payrollatest.Employee{FirstName: "Zainab", LastName: "Bello", Phone: "+2348011112299", Department: "Kitchen", BankCode: "044", AccountNumber: "2222222222",
		Pay: &payrollatest.Pay{Gross: 45000000, Basic: 30000000, Housing: 10000000, Pensionable: true, EffectiveFrom: "2026-01-01"}})
	num, musaNum := "+2348031119201", "+2348031119202"
	w.bind(t, tunde, num)
	w.bind(t, musa, musaNum)

	// Add: from the staff member; someone not on the staff; someone in
	// Payrolla already. A bank account is typed only in the form.
	if s := said(t, w, num, "add musa to payroll"); !strings.HasPrefix(s, "Add Musa Ali to payroll in this form") || !strings.HasSuffix(s, "/payroll/team?add="+personID(t, musa).String()) {
		t.Fatalf("add from staff: %q", s)
	}
	if s := said(t, w, num, "add Kemi Ade to payroll"); !strings.HasPrefix(s, "Kemi Ade isn't on your staff in Taskiem.") || !strings.HasSuffix(s, "/payroll/team?add=new") {
		t.Fatalf("add someone new: %q", s)
	}
	if s := said(t, w, num, "add zainab to payroll"); !strings.HasPrefix(s, "Zainab Bello is already on payroll.") {
		t.Fatalf("already: %q", s)
	}

	// Pay from a date: asked with the pay now; the parts kept.
	if s := said(t, w, num, "raise zainab's pay to 500k from 1 march 2027"); s != "Change Zainab Bello's monthly pay from ₦450,000 to ₦500,000, starting 1 March 2027? Their basic, housing and transport stay as they are." {
		t.Fatalf("pay asked: %q", s)
	}
	if team.Employee(zainab).Pay.Gross != 45000000 {
		t.Fatal("changed before yes")
	}
	if s := yesTo(t, w, num); s != "Done. Zainab Bello's new pay starts 1 March 2027." {
		t.Fatalf("pay done: %q", s)
	}
	if p := team.Employee(zainab).Pay; p.Gross != 50000000 || p.Basic != 30000000 || p.Housing != 10000000 || p.EffectiveFrom != "2027-03-01" {
		t.Fatalf("in Payrolla: %+v", p)
	}
	// Lower than the parts: the form, not a guess.
	if s := said(t, w, num, "change zainab's salary to 300k"); !strings.HasPrefix(s, "Zainab Bello's basic, housing and transport add up to more than ₦300,000.") {
		t.Fatalf("parts: %q", s)
	}
	if s := said(t, w, num, "raise zainab's pay to 500k from someday"); !strings.HasPrefix(s, "Say when the new pay starts") {
		t.Fatalf("a date not read: %q", s)
	}

	// The department.
	if s := said(t, w, num, "move zainab to sales"); s != "Move Zainab Bello to Sales?" {
		t.Fatalf("move: %q", s)
	}
	if s := yesTo(t, w, num); s != "Done. Zainab Bello is in Sales now." || team.Employee(zainab).Department != "Sales" {
		t.Fatalf("moved: %q", s)
	}
	for _, m := range w.send(t, num, whatsapptest.Text("move the generator to the shop")) {
		if strings.HasPrefix(m.Text, "Move ") {
			t.Fatalf("not payroll's words: %q", m.Text)
		}
	}
	// Payrolla's names are kept a few minutes: the next "move" lists no
	// one again.
	listed := team.Listed()
	w.send(t, num, whatsapptest.Text("move the chairs to the hall"))
	if team.Listed() != listed {
		t.Fatalf("listed again: %d -> %d", listed, team.Listed())
	}

	// A new account: the form.
	if s := said(t, w, num, "change zainab's salary account"); !strings.HasPrefix(s, "A new salary account is typed only in a form") {
		t.Fatalf("bank: %q", s)
	}

	// Musa added through the form (as the link opens it), then his own
	// account change goes to My pay.
	if s := said(t, w, musaNum, "change my salary account"); s != "You're not linked to payroll here yet. Ask your employer." {
		t.Fatalf("not linked: %q", s)
	}
	tunde.must(201, "POST", "/v1/payroll/team", map[string]any{"key": "add-musa", "first_name": "Musa", "last_name": "Ali", "phone": "08011112201", "start_date": "2026-12-01",
		"pay": map[string]any{"gross_kobo": 30000000}, "bank": map[string]any{"bank_code": "058", "account_number": "0123456789"}, "staff_user": personID(t, musa)})
	if s := said(t, w, musaNum, "change my salary account"); !strings.HasPrefix(s, "Change your salary account on My pay.") || !strings.HasSuffix(s, "/payroll/me") {
		t.Fatalf("my pay: %q", s)
	}
	// Two people called by one name are asked about, never guessed.
	team.AddEmployee(payrollatest.Employee{FirstName: "Musa", LastName: "Bello", Phone: "+2348011112298"})
	// Musa added through Taskiem: the names kept were forgotten.
	listed = team.Listed()
	if s := said(t, w, num, "move musa to sales"); s != "Which one: Musa Ali, Musa Bello? Say it again with their full name." || team.Listed() != listed+1 {
		t.Fatalf("which: %q", s)
	}

	// Ending employment: who may, the warning, yes.
	if s := said(t, w, musaNum, "end zainab's employment"); s != "You can't do that to the payroll here. Ask the owner." {
		t.Fatalf("cashier: %q", s)
	}
	if s := said(t, w, num, "end zainab's employment"); s != "End Zainab Bello's employment? This takes effect today. Payrolla can't set a last day." {
		t.Fatalf("end asked: %q", s)
	}
	if s := yesTo(t, w, num); s != "Zainab Bello's employment ended today." || team.Employee(zainab).Status != "TERMINATED" {
		t.Fatalf("ended: %q", s)
	}
	if s := said(t, w, num, "raise zainab's pay to 600k"); s != "Zainab Bello has left already." {
		t.Fatalf("left: %q", s)
	}
	if s := said(t, w, num, "remove bisi from payroll"); s != "There's no one called bisi in Payrolla." {
		t.Fatalf("no one: %q", s)
	}

	// Every write said who did it; no pay or account number in the audit.
	for _, wr := range f.Writes() {
		if wr.Key == "" || !strings.HasPrefix(wr.ActingUser, "tsk_user_") {
			t.Fatalf("write %+v", wr)
		}
	}
	var n, leaks int
	_ = w.env.DB.Admin.QueryRow(context.Background(), `SELECT count(*) FROM audit_log WHERE action IN ('payroll.employee_change', 'payroll.employee_end') AND detail->>'via' = 'whatsapp'`).Scan(&n)
	_ = w.env.DB.Admin.QueryRow(context.Background(), `SELECT count(*) FROM audit_log WHERE detail::text ~ '(50000000|0123456789|2222222222)'`).Scan(&leaks)
	if n != 3 || leaks != 0 {
		t.Fatalf("audit: %d changes, %d leaks", n, leaks)
	}
}

// A held salary account change approved with Payrolla's code typed in the
// WhatsApp thread: listed with who asked; whoever asked refused while
// another approver exists; a wrong code asked again; an expired code, and
// one locked after too many tries, offer a new code; the right one
// approves; a wait past Taskiem's own ten minutes takes nothing; "stop";
// rejecting with a note; the code kept nowhere.
func TestPayrollChatAccountChangeCode(t *testing.T) {
	ctx := context.Background()
	w := newPayrollaWorld(t)
	f := w.fake
	team := payrollatest.NewTeam(f)
	team.AddAccount("058", "0123456789", "MUSA I. ALI")
	team.AddAccount("044", "2222222222", "MUSA I. ALI")
	team.AddAccount("057", "3333333333", "MUSA IBRAHIM ALI")
	owner := w.tenant(t, "Amara Foods", "ngozi@amara.test")
	w.connect(t, owner, map[string]any{"mode": "test"})
	owner.must(201, "PUT", "/v1/roles/payroll", map[string]any{"permissions": []string{"payroll.read", "payroll.approve"}})
	owner.must(201, "PUT", "/v1/roles/preparer", map[string]any{"permissions": []string{"payroll.prepare"}})
	ada := namedMember(t, w, owner, "ada@amara.test", "Ada Obi", "payroll")
	tunde := namedMember(t, w, owner, "tunde@amara.test", "Tunde Bayo", "preparer")
	ngoziA, adaA := "a0000000-0000-4000-8000-000000000001", "a0000000-0000-4000-8000-000000000003"
	f.AddApprover(payrollatest.Approver{UserID: ngoziA, Name: "Ngozi Eze", Email: "ngozi@amara.test", Role: "OWNER", CanApprove: true, CanPay: true, CanConfirm: true,
		LinkedAs: payrolla.ActingUser(personID(t, owner))})
	f.AddApprover(payrollatest.Approver{UserID: adaA, Name: "Ada Obi", Email: "ada@amara.test", Role: "APPROVER", CanApprove: true, CanConfirm: true,
		LinkedAs: payrolla.ActingUser(personID(t, ada))})
	emp := team.AddEmployee(payrollatest.Employee{FirstName: "Musa", LastName: "Ali", Phone: "+2348011112201", BankCode: "058", AccountNumber: "0123456789"})
	owner.must(200, "POST", "/v1/payroll/team/"+emp+"/bank", map[string]any{"key": "b-1", "bank_code": "044", "account_number": "2222222222"})
	change := team.BankChanges()[0].ID

	adaNum, ownerNum, tundeNum := "+2348031119301", "+2348031119302", "+2348031119303"
	w.bind(t, ada, adaNum)
	w.bind(t, owner, ownerNum)
	w.bind(t, tunde, tundeNum)

	// What waits, with who asked, and Approve for those who may.
	list := oneBare(t, w.send(t, adaNum, whatsapptest.Text("account changes")))
	if !strings.Contains(list.Text, "Account changes waiting:\nMusa Ali: MUSA I. ALI, Access Bank ••2222. Asked by Owner in Taskiem.") ||
		!strings.Contains(list.Text, "approve musa's account change") || len(list.Buttons) != 1 || list.Buttons[0].ID != "pr:b:"+change || list.Buttons[0].Title != "Approve Musa" {
		t.Fatalf("list: %+v", list)
	}
	if s := said(t, w, tundeNum, "account changes"); !strings.Contains(s, "Musa Ali: MUSA I. ALI") || strings.Contains(s, "To approve one") {
		t.Fatalf("a preparer sees them: %q", s)
	}

	// Who may approve: not the preparer; not whoever asked, while Ada can.
	if s := said(t, w, tundeNum, "approve musa's account change"); s != "You can't do that to the payroll here. Ask the owner." {
		t.Fatalf("preparer: %q", s)
	}
	if s := said(t, w, ownerNum, "approve musa's account change"); s != "You asked for this account change, so someone else must approve it." {
		t.Fatalf("same person: %q", s)
	}

	// Ada: the code, a wrong one, anything else answered as usual.
	if s := said(t, w, adaNum, "approve musa's account change"); !strings.HasPrefix(s, "Payrolla sent a code to your phone ending 4567. Type it here to approve Musa Ali's new salary account: MUSA I. ALI, Access Bank ••2222.") {
		t.Fatalf("asked: %q", s)
	}
	if s := said(t, w, adaNum, "000 000"); s != "That code isn't right. Check it and type it again. Type the code again, or say stop." {
		t.Fatalf("wrong: %q", s)
	}
	w.send(t, adaNum, whatsapptest.Text("status"))
	// Expired: a new code offered.
	team.ExpireCodes()
	exp := oneBare(t, w.send(t, adaNum, whatsapptest.Text(team.Code(adaA))))
	if exp.Text != "That code has expired. Send a new code." || len(exp.Buttons) != 1 || exp.Buttons[0].ID != "pr:b:"+change {
		t.Fatalf("expired: %+v", exp)
	}
	// Too many tries: locked, a new code offered.
	if s := oneBare(t, w.press(t, adaNum, "pr:b:"+change)).Text; !strings.Contains(s, "Type it here to approve Musa Ali's new salary account") {
		t.Fatalf("new code: %q", s)
	}
	for i := range 4 {
		if s := said(t, w, adaNum, "11111"+string(rune('0'+i))); !strings.HasPrefix(s, "That code isn't right.") {
			t.Fatalf("try %d: %q", i, s)
		}
	}
	locked := oneBare(t, w.send(t, adaNum, whatsapptest.Text("111119")))
	if !strings.HasPrefix(locked.Text, "Too many wrong tries for that code.") || len(locked.Buttons) != 1 {
		t.Fatalf("locked: %+v", locked)
	}
	// The right code approves, once.
	w.press(t, adaNum, "pr:b:"+change)
	code := team.Code(adaA)
	if s := said(t, w, adaNum, code[:3]+" "+code[3:]); s != "Approved. Payrolla pays Musa Ali into the new account from now on." {
		t.Fatalf("approved: %q", s)
	}
	if team.Employee(emp).AccountNumber != "2222222222" {
		t.Fatal("not approved in Payrolla")
	}
	for _, m := range w.send(t, adaNum, whatsapptest.Text(code)) {
		if strings.Contains(m.Text, "Approved") {
			t.Fatalf("a code taken twice: %q", m.Text)
		}
	}

	// Another change: Taskiem's own wait ends after ten minutes.
	w.srv.ResetLimiters()
	owner.must(200, "POST", "/v1/payroll/team/"+emp+"/bank", map[string]any{"key": "b-2", "bank_code": "057", "account_number": "3333333333"})
	if s := said(t, w, adaNum, "approve the account change"); !strings.Contains(s, "MUSA IBRAHIM ALI, Zenith Bank ••3333") {
		t.Fatalf("asked again: %q", s)
	}
	if _, err := w.env.DB.Admin.Exec(ctx, `UPDATE payroll_code_waits SET expires_at = now() - interval '1 second'`); err != nil {
		t.Fatal(err)
	}
	for _, m := range w.send(t, adaNum, whatsapptest.Text(team.Code(adaA))) {
		if strings.Contains(m.Text, "Approved") {
			t.Fatalf("a code taken after the wait: %q", m.Text)
		}
	}
	// "stop" drops a waiting code.
	said(t, w, adaNum, "approve musa's account change")
	if s := said(t, w, adaNum, "stop"); s != "Stopped. That code won't be used." {
		t.Fatalf("stop: %q", s)
	}
	// Rejecting: a note needed, no code.
	if s := said(t, w, adaNum, "reject musa's account change"); s != "To turn it down, say: reject musa's account change, and why." {
		t.Fatalf("how: %q", s)
	}
	if s := said(t, w, adaNum, "reject musa's account change, call musa first"); s != "Rejected. Musa Ali is still paid into their current account." {
		t.Fatalf("rejected: %q", s)
	}
	if s := said(t, w, adaNum, "account changes"); s != "No account changes are waiting." {
		t.Fatalf("none: %q", s)
	}

	// The code kept nowhere: not the waits, the audit or the outbox (the
	// conversation keeps "Code entered", sealed); no account number either.
	var waits, leaks int
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT count(*) FROM payroll_code_waits`).Scan(&waits); err != nil {
		t.Fatal(err)
	}
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT (SELECT count(*) FROM audit_log WHERE detail::text ~ ($1 || '|2222222222|3333333333'))
		+ (SELECT count(*) FROM whatsapp_outbox WHERE coalesce(reach_reference, '') LIKE '%' || $1 || '%')`, code).Scan(&leaks); err != nil {
		t.Fatal(err)
	}
	if waits != 0 || leaks != 0 {
		t.Fatalf("waits %d, leaks %d", waits, leaks)
	}
	var approved bool
	_ = w.env.DB.Admin.QueryRow(ctx, `SELECT detail->>'confirmed_with_code' = 'true' AND detail->>'own_request' = 'false' FROM audit_log WHERE action = 'payroll.bank_change_approve'`).Scan(&approved)
	if !approved {
		t.Fatal("the approval isn't audited as confirmed with a code")
	}
}

// The company's only approver may approve a change they asked for
// themselves, with their code and no passkey of Taskiem's (decision 0074,
// settled 5); the web chat sends the code to Account changes.
func TestPayrollChatOnlyApproverOwnChange(t *testing.T) {
	w := newPayrollaWorld(t)
	f := w.fake
	team := payrollatest.NewTeam(f)
	team.AddAccount("058", "0123456789", "MUSA I. ALI")
	team.AddAccount("044", "2222222222", "MUSA I. ALI")
	owner := w.tenant(t, "Amara Foods", "ngozi@amara.test")
	w.connect(t, owner, map[string]any{"mode": "test"})
	f.AddApprover(payrollatest.Approver{UserID: "a0000000-0000-4000-8000-000000000001", Name: "Ngozi Eze", Email: "ngozi@amara.test", Role: "OWNER", CanApprove: true, CanPay: true,
		CanConfirm: true, LinkedAs: payrolla.ActingUser(personID(t, owner))})
	emp := team.AddEmployee(payrollatest.Employee{FirstName: "Musa", LastName: "Ali", Phone: "+2348011112201", BankCode: "058", AccountNumber: "0123456789"})
	owner.must(200, "POST", "/v1/payroll/team/"+emp+"/bank", map[string]any{"key": "b-1", "bank_code": "044", "account_number": "2222222222"})

	web := owner.must(200, "POST", "/v1/taskiem-chat", map[string]any{"text": "approve musa's account change"})
	msgs := web["messages"].([]any)
	if last := msgs[len(msgs)-1].(map[string]any); !strings.Contains(last["text"].(string), "Approve it where you type Payrolla's code: ") ||
		!strings.Contains(last["text"].(string), "/payroll/account-changes") {
		t.Fatalf("web: %v", msgs)
	}

	num := "+2348031119401"
	w.bind(t, owner, num)
	if s := said(t, w, num, "approve musa's account change"); !strings.Contains(s, "Type it here to approve Musa Ali's new salary account") {
		t.Fatalf("asked: %q", s)
	}
	if s := said(t, w, num, team.Code("a0000000-0000-4000-8000-000000000001")); s != "Approved by the person who asked. Payrolla pays Musa Ali into the new account from now on." {
		t.Fatalf("own: %q", s)
	}
	if team.Employee(emp).AccountNumber != "2222222222" {
		t.Fatal("not approved")
	}
}

// On Telegram an account change's code is typed in the thread too.
func TestPayrollChatAccountChangeOnTelegram(t *testing.T) {
	w := newTGWorld(t)
	w.srv.Egress = &egress.Guard{Blocked: func(netip.Addr) bool { return false }}
	f := payrollatest.New(payrollatest.Client{ID: "pyc", Secret: "pys"})
	t.Cleanup(f.Close)
	f.ServeRuns()
	team := payrollatest.NewTeam(f)
	team.AddAccount("058", "0123456789", "MUSA I. ALI")
	team.AddAccount("044", "2222222222", "MUSA I. ALI")
	if err := w.env.Registry.Register(payrolla.New(payrolla.Options{BaseURL: f.API(), TestURL: f.API(), LiveURL: f.API()})); err != nil {
		t.Fatal(err)
	}
	owner := w.tenant(t, "Amara Foods", "ngozi@amara.test")
	key := f.AddKey("payroll:read", "employees:read", "employees:write", "payroll:write", "payroll:approve")
	owner.must(201, "POST", "/v1/connections", map[string]any{"environment": "prod", "connector": "payrolla@1", "name": "main", "mode": "test",
		"credentials": map[string]string{"api_key": key}})
	f.AddApprover(payrollatest.Approver{UserID: ngoziPy, Name: "Ngozi Eze", Email: "ngozi@amara.test", Role: "OWNER", CanApprove: true, CanPay: true, CanConfirm: true,
		LinkedAs: payrolla.ActingUser(personID(t, owner))})
	emp := team.AddEmployee(payrollatest.Employee{FirstName: "Musa", LastName: "Ali", Phone: "+2348011112201", BankCode: "058", AccountNumber: "0123456789"})
	owner.must(200, "POST", "/v1/payroll/team/"+emp+"/bank", map[string]any{"key": "b-1", "bank_code": "044", "account_number": "2222222222"})

	const account = int64(7101)
	w.link(t, owner, account, "ngozi")
	if r := w.say(t, account, "account changes"); !strings.Contains(r.Text, "Musa Ali: MUSA I. ALI, Access Bank ••2222.") {
		t.Fatalf("list: %+v", r)
	}
	if r := w.say(t, account, "approve musa's account change"); !strings.Contains(r.Text, "Type it here to approve Musa Ali's new salary account") {
		t.Fatalf("asked: %+v", r)
	}
	if r := w.say(t, account, "123 456"); !strings.Contains(r.Text, "That code isn't right.") {
		t.Fatalf("wrong: %+v", r)
	}
	if r := w.say(t, account, team.Code(ngoziPy)); !strings.Contains(r.Text, "Approved by the person who asked.") {
		t.Fatalf("approved: %+v", r)
	}
	if team.Employee(emp).AccountNumber != "2222222222" {
		t.Fatal("not approved")
	}
}

// Account changes in a company of more than the employee list reads: the
// names of those it did not reach are read one by one; two Musas waiting
// get Approve buttons that read differently.
func TestPayrollChatAccountChangesPaged(t *testing.T) {
	w := newPayrollaWorld(t)
	f := w.fake
	team := payrollatest.NewTeam(f)
	team.AddAccount("058", "0123456789", "MUSA I. ALI")
	team.AddAccount("044", "2222222222", "MUSA I. ALI")
	team.AddAccount("058", "0123456788", "MUSA BELLO")
	team.AddAccount("044", "2222222223", "MUSA BELLO")
	owner := w.tenant(t, "Amara Foods", "ngozi@amara.test")
	w.connect(t, owner, map[string]any{"mode": "test"})
	owner.must(201, "PUT", "/v1/roles/payroll", map[string]any{"permissions": []string{"payroll.read", "payroll.approve"}})
	ada := namedMember(t, w, owner, "ada@amara.test", "Ada Obi", "payroll")
	f.AddApprover(payrollatest.Approver{UserID: "a0000000-0000-4000-8000-000000000001", Name: "Ngozi Eze", Email: "ngozi@amara.test", Role: "OWNER", CanApprove: true, CanPay: true,
		CanConfirm: true, LinkedAs: payrolla.ActingUser(personID(t, owner))})
	f.AddApprover(payrollatest.Approver{UserID: "a0000000-0000-4000-8000-000000000003", Name: "Ada Obi", Email: "ada@amara.test", Role: "APPROVER", CanApprove: true,
		CanConfirm: true, LinkedAs: payrolla.ActingUser(personID(t, ada))})
	// One person a page: the list reads AllEmployeesPages people, and
	// the Musas come after them.
	team.SetPageSize(1)
	for i := range payrolla.AllEmployeesPages {
		team.AddEmployee(payrollatest.Employee{FirstName: "Worker", LastName: string(rune('A' + i)), Phone: fmt.Sprintf("+234801111%04d", 3000+i)})
	}
	ali := team.AddEmployee(payrollatest.Employee{FirstName: "Musa", LastName: "Ali", Phone: "+2348011112201", BankCode: "058", AccountNumber: "0123456789"})
	bello := team.AddEmployee(payrollatest.Employee{FirstName: "Musa", LastName: "Bello", Phone: "+2348011112202", BankCode: "058", AccountNumber: "0123456788"})
	owner.must(200, "POST", "/v1/payroll/team/"+ali+"/bank", map[string]any{"key": "b-1", "bank_code": "044", "account_number": "2222222222"})
	owner.must(200, "POST", "/v1/payroll/team/"+bello+"/bank", map[string]any{"key": "b-2", "bank_code": "044", "account_number": "2222222223"})

	web := ada.must(200, "GET", "/v1/payroll/bank-changes", nil)["changes"].([]any)
	if len(web) != 2 || web[0].(map[string]any)["employee"] != "Musa Ali" || web[1].(map[string]any)["employee"] != "Musa Bello" {
		t.Fatalf("on the web: %v", web)
	}
	adaNum := "+2348031119401"
	w.bind(t, ada, adaNum)
	list := oneBare(t, w.send(t, adaNum, whatsapptest.Text("account changes")))
	if !strings.Contains(list.Text, "Musa Ali: MUSA I. ALI, Access Bank ••2222.") || !strings.Contains(list.Text, "Musa Bello: MUSA BELLO, Access Bank ••2223.") ||
		len(list.Buttons) != 2 || list.Buttons[0].Title != "Approve Musa A." || list.Buttons[1].Title != "Approve Musa B." {
		t.Fatalf("list: %+v", list)
	}
}
