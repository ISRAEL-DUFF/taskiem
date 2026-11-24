package api_test

import (
	"context"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/israel-duff/taskiem/connectors/payrolla"
	"github.com/israel-duff/taskiem/connectors/payrolla/payrollatest"
	"github.com/israel-duff/taskiem/engine/egress"
	"github.com/israel-duff/taskiem/engine/telegram/telegramtest"
	"github.com/israel-duff/taskiem/engine/whatsapp/whatsapptest"
)

const (
	ngoziPy = "a0000000-0000-4000-8000-000000000001"
	musaPy  = "a0000000-0000-4000-8000-000000000002"
	femiPy  = "a0000000-0000-4000-8000-000000000003"
	tundePy = "a0000000-0000-4000-8000-000000000004"
	zainabE = "e0000000-0000-4000-8000-000000000001"
	musaE   = "e0000000-0000-4000-8000-000000000002"
	adaE    = "e0000000-0000-4000-8000-000000000003"
)

func payrollWorkers(f *payrollatest.Fake) {
	f.SetWorkers(payrollatest.Worker{ID: zainabE, First: "Zainab", Last: "Bello", GrossKobo: 45000000},
		payrollatest.Worker{ID: musaE, First: "Musa", Last: "Ali", GrossKobo: 30000000},
		payrollatest.Worker{ID: adaE, First: "Ada", Last: "Obi", GrossKobo: 25000000})
	f.AddApprover(payrollatest.Approver{UserID: ngoziPy, Name: "Ngozi Eze", Email: "ngozi@amara.test", Role: "OWNER", CanApprove: true, CanPay: true, CanConfirm: true})
	f.AddApprover(payrollatest.Approver{UserID: musaPy, Name: "Musa Bello", Email: "musa@amara.test", Role: "APPROVER", CanApprove: true, CanConfirm: true})
	f.AddApprover(payrollatest.Approver{UserID: femiPy, Name: "Femi Ade", Email: "femi@amara.test", Role: "ADMIN", CanConfirm: true})
	f.AddApprover(payrollatest.Approver{UserID: tundePy, Name: "Tunde Bayo", Email: "tunde@amara.test", Role: "APPROVER", CanApprove: true})
}

// linkPayrolla links c to a Payrolla user with one code, through the API.
func linkPayrolla(t *testing.T, f *payrollatest.Fake, c *client, user string) {
	t.Helper()
	sent := c.must(202, "POST", "/v1/payroll/approvers/"+user+"/link", map[string]any{"key": "link-" + user})
	c.must(200, "POST", "/v1/payroll/approvers/"+user+"/link/confirm", map[string]any{"challenge_id": sent["challenge"].(map[string]any)["challenge_id"],
		"code": f.Code(user), "key": "link-c-" + user})
}

func lineByName(t *testing.T, view map[string]any, name string) map[string]any {
	t.Helper()
	for _, x := range view["lines"].([]any) {
		if l := x.(map[string]any); l["name"] == name {
			return l
		}
	}
	t.Fatalf("no line for %s: %v", name, view["lines"])
	return nil
}

func lastSent(t *testing.T, sent []whatsapptest.Sent) whatsapptest.Sent {
	t.Helper()
	if len(sent) == 0 {
		t.Fatal("nothing sent")
	}
	return sent[len(sent)-1]
}

// November's payroll is prepared in Taskiem, a bonus added and one person
// left out, approved by another person with a code typed into the
// WhatsApp thread, and locked with a code in the app; the preparer cannot
// approve it with separation of duties on; nothing is done in Payrolla's
// own screens. Each of Payrolla's refusals is said in words, and the code
// is kept nowhere.
func TestPayrollRunInTaskiem(t *testing.T) {
	ctx := context.Background()
	w := newPayrollaWorld(t)
	f := w.fake
	payrollWorkers(f)
	f.Runs().SeparationOfDuties = true
	f.Runs().WalletKobo = 50000000
	owner := w.tenant(t, "Amara Foods", "ngozi@amara.test")
	back, _ := w.connect(t, owner, map[string]any{"mode": "test"})
	if back.Get("linked") == "" {
		t.Fatalf("connect: %v", back)
	}
	owner.must(201, "PUT", "/v1/roles/payroll", map[string]any{"permissions": []string{"payroll.read", "payroll.approve"}})
	owner.must(201, "PUT", "/v1/roles/clerk", map[string]any{"permissions": []string{"payroll.prepare"}})
	musa := w.member(t, owner, "musa@amara.test", "payroll")
	femi := w.member(t, owner, "femi@amara.test", "payroll")
	tunde := w.member(t, owner, "tunde@amara.test", "payroll")
	clerk := w.member(t, owner, "clerk@amara.test", "clerk")
	cashier := w.member(t, owner, "cashier@amara.test", "cashier")
	linkPayrolla(t, f, owner, ngoziPy)
	linkPayrolla(t, f, musa, musaPy)
	linkPayrolla(t, f, femi, femiPy)

	page := owner.must(200, "GET", "/v1/payroll", nil)
	if page["may_prepare"] != true || page["this_month"].(map[string]any)["period"] == "" {
		t.Fatalf("payroll page: %v", page)
	}
	if page := musa.must(200, "GET", "/v1/payroll", nil); page["may_prepare"] != false {
		t.Fatalf("an approver prepares nothing: %v", page)
	}
	cashier.must(403, "GET", "/v1/payroll", nil)
	cashier.must(403, "POST", "/v1/payroll/runs", map[string]any{"period": "2026-11", "key": "x"})
	musa.must(403, "POST", "/v1/payroll/runs", map[string]any{"period": "2026-11", "key": "x"})

	// Prepared (by the owner); a second for the month opens the first.
	got := owner.must(201, "POST", "/v1/payroll/runs", map[string]any{"period": "2026-11", "key": "prep-1"})
	run := got["run"].(map[string]any)["run_id"].(string)
	if got["run"].(map[string]any)["status"] != "COMPUTED" || got["run"].(map[string]any)["pay_date"] != "2026-11-30" {
		t.Fatalf("prepared: %v", got)
	}
	if e := owner.must(409, "POST", "/v1/payroll/runs", map[string]any{"period": "2026-11", "key": "prep-2"}); e["code"] != "duplicate_run" || e["run_id"] != run ||
		e["error"] != "There is already a payroll for November 2026." {
		t.Fatalf("duplicate: %v", e)
	}

	// Each person's line with its breakdown; the wallet is short.
	view := owner.must(200, "GET", "/v1/payroll/runs/"+run, nil)
	z := lineByName(t, view, "Zainab Bello")
	labels := ""
	for _, it := range z["items"].([]any) {
		labels += it.(map[string]any)["label"].(string) + ";"
	}
	if z["gross_kobo"] != float64(45000000) || !strings.Contains(labels, "Basic;") || !strings.Contains(labels, "Tax (PAYE);") || !strings.Contains(labels, "Pension (yours);") ||
		!strings.Contains(labels, "Pension (employer's);") {
		t.Fatalf("Zainab's line: %v", z)
	}
	fund := view["funding"].(map[string]any)
	if fund["short_by_kobo"].(float64) <= 0 || !strings.Contains(fund["words"].(string), "Your Payrolla wallet has ₦500,000.") || !strings.Contains(fund["words"].(string), "Add money before 30 Nov") {
		t.Fatalf("funding: %v", fund)
	}
	if may := view["may"].(map[string]any); may["prepare"] != true || may["approve"] != true || may["linked"] != true || view["approval_url"] != nil {
		t.Fatalf("may: %v", view)
	}
	before := z["net_kobo"].(float64)

	// A bonus (gross: tax applies) and a deduction, then one removed;
	// Ada left out, put back, and left out again.
	clerk.must(201, "POST", "/v1/payroll/runs/"+run+"/adjustments", map[string]any{"employee_id": zainabE, "type": "bonus", "amount_kobo": 10000000, "reason": "Q4 target", "key": "b-1"})
	owner.must(201, "POST", "/v1/payroll/runs/"+run+"/adjustments", map[string]any{"employee_id": musaE, "type": "deduction", "amount_kobo": 2000000, "reason": "Advance repaid", "key": "d-1"})
	musa.must(403, "POST", "/v1/payroll/runs/"+run+"/adjustments", map[string]any{"employee_id": musaE, "type": "bonus", "amount_kobo": 100, "reason": "me", "key": "m-1"})
	owner.must(400, "POST", "/v1/payroll/runs/"+run+"/adjustments", map[string]any{"employee_id": musaE, "type": "bonus", "amount_kobo": 5000, "reason": " ", "key": "d-2"})
	view = owner.must(200, "GET", "/v1/payroll/runs/"+run, nil)
	if n := lineByName(t, view, "Zainab Bello")["net_kobo"].(float64); n <= before || n-before >= 10000000 {
		t.Fatalf("the bonus is before tax: %v -> %v", before, n)
	}
	adj := view["adjustments"].([]any)
	if len(adj) != 2 || adj[0].(map[string]any)["name"] != "Zainab Bello" || adj[0].(map[string]any)["type"] != "bonus" {
		t.Fatalf("adjustments: %v", adj)
	}
	owner.must(200, "DELETE", "/v1/payroll/runs/"+run+"/adjustments/"+adj[1].(map[string]any)["id"].(string)+"?key=u-1", nil)
	out := owner.must(201, "POST", "/v1/payroll/runs/"+run+"/exclusions", map[string]any{"employee_id": adaE, "reason": "unpaid leave", "key": "x-1"})
	if out["run"].(map[string]any)["employee_count"] != float64(2) {
		t.Fatalf("left out: %v", out)
	}
	view = owner.must(200, "GET", "/v1/payroll/runs/"+run, nil)
	ex := view["exclusions"].([]any)
	if len(ex) != 1 || ex[0].(map[string]any)["name"] != "Ada Obi" || ex[0].(map[string]any)["reason"] != "unpaid leave" {
		t.Fatalf("exclusions: %v", ex)
	}
	owner.must(200, "DELETE", "/v1/payroll/runs/"+run+"/exclusions/"+ex[0].(map[string]any)["id"].(string)+"?key=p-1", nil)
	owner.must(201, "POST", "/v1/payroll/runs/"+run+"/exclusions", map[string]any{"employee_id": adaE, "reason": "unpaid leave", "key": "x-2"})
	owner.must(200, "POST", "/v1/payroll/runs/"+run+"/compute", map[string]any{"key": "c-1"})

	// The preparer can't approve it: Payrolla says so before any code,
	// naming who can.
	if e := owner.must(403, "POST", "/v1/payroll/runs/"+run+"/confirmations", map[string]any{"action": "approve", "key": "o-1"}); e["code"] != "sod" ||
		!strings.Contains(e["error"].(string), "You prepared this payroll, so someone else must approve it.") || !strings.Contains(e["error"].(string), "Musa") {
		t.Fatalf("sod: %v", e)
	}
	// Refusals in words: not linked, not an approver, no phone for codes,
	// not at that step.
	if e := tunde.must(409, "POST", "/v1/payroll/runs/"+run+"/confirmations", map[string]any{"action": "approve", "key": "t-1"}); e["code"] != "not_linked" ||
		e["link"] != "/payroll/settings" || !strings.HasPrefix(e["error"].(string), "Link your Payrolla account first.") {
		t.Fatalf("not linked: %v", e)
	}
	if e := femi.must(403, "POST", "/v1/payroll/runs/"+run+"/confirmations", map[string]any{"action": "approve", "key": "f-1"}); e["code"] != "not_an_approver" ||
		e["error"] != "In Payrolla you can't approve payroll. Ask the owner to make you an Approver in Payrolla." {
		t.Fatalf("not an approver: %v", e)
	}
	if e := musa.must(409, "POST", "/v1/payroll/runs/"+run+"/confirmations", map[string]any{"action": "lock", "key": "m-2"}); e["code"] != "invalid_state" ||
		e["error"] != "This payroll has moved on: it's now waiting for approval." {
		t.Fatalf("lock first: %v", e)
	}

	// The approval card reaches those who may approve, on the web and
	// their chat: Musa, on WhatsApp.
	num := "+2348031119001"
	w.bind(t, musa, num)
	w.send(t, num, whatsapptest.Text("hello")) // the window is open
	f.Flush()
	w.tick(t)
	card := lastSent(t, w.graph.Sent(num))
	if !strings.Contains(card.Text, "November 2026 payroll") || !strings.Contains(card.Text, "2 people") || !strings.Contains(card.Text, "short by") ||
		len(card.Buttons) != 2 || card.Buttons[0].ID != "pr:a:"+run {
		t.Fatalf("the card on WhatsApp: %+v", card)
	}
	var cards int
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT count(*) FROM chat_history WHERE kind = 'payroll_run' AND user_id = $1`, personID(t, musa)).Scan(&cards); err != nil || cards != 1 {
		rows, _ := w.env.DB.Admin.Query(ctx, `SELECT channel, status, coalesce(last_error, '') FROM whatsapp_outbox WHERE kind = 'payroll_run'`)
		for rows.Next() {
			var a, b, c string
			_ = rows.Scan(&a, &b, &c)
			t.Log(a, b, c)
		}
		rows.Close()
		t.Fatalf("the card in Musa's web conversation: %d %v", cards, err)
	}

	// Approve in the thread: a code, typed there; a wrong one asks again;
	// anything else is answered as usual and the code keeps waiting.
	said := w.press(t, num, "pr:a:"+run)
	if len(said) != 1 || !strings.Contains(said[0].Text, "Payrolla sent a code to your phone ending 4567. Type it here to approve November 2026 payroll.") ||
		!strings.Contains(said[0].Text, "authenticator app is safer") {
		t.Fatalf("asking for the code: %+v", said)
	}
	if s := w.say(t, num, "000 000"); !strings.Contains(s, "That code isn't right.") {
		t.Fatalf("a wrong code: %q", s)
	}
	w.send(t, num, whatsapptest.Text("status"))
	code := f.Code(musaPy)
	said = w.send(t, num, whatsapptest.Text(code[:3]+" "+code[3:]))
	if len(said) != 1 || !strings.HasSuffix(said[0].Text, "Approved. Lock and pay?") || len(said[0].Buttons) != 1 || said[0].Buttons[0].ID != "pr:l:"+run {
		t.Fatalf("approved: %+v", said)
	}
	// The code no longer waits: six digits are ordinary words again.
	for _, m := range w.send(t, num, whatsapptest.Text(code)) {
		if strings.Contains(m.Text, "Approved") {
			t.Fatalf("a code taken twice: %q", m.Text)
		}
	}
	var waits, leaks int
	_ = w.env.DB.Admin.QueryRow(ctx, `SELECT count(*) FROM payroll_code_waits`).Scan(&waits)
	_ = w.env.DB.Admin.QueryRow(ctx, `SELECT (SELECT count(*) FROM audit_log WHERE detail::text LIKE '%' || $1 || '%') + (SELECT count(*) FROM whatsapp_outbox WHERE coalesce(reach_reference, '') LIKE '%' || $1 || '%')`, code).Scan(&leaks)
	if waits != 0 || leaks != 0 {
		t.Fatalf("waits %d, the code found %d times", waits, leaks)
	}
	if after := owner.must(409, "POST", "/v1/payroll/runs/"+run+"/adjustments", map[string]any{"employee_id": zainabE, "type": "bonus", "amount_kobo": 100, "reason": "late", "key": "late"}); after["code"] != "invalid_state" ||
		after["error"] != "This payroll has moved on: it's now approved." {
		t.Fatalf("after approval: %v", after)
	}

	// Lock and pay in the app: refused while someone has no account,
	// naming them; then with a code; a wrong code, an expired one.
	ws := f.Workers()
	ws[0].NoAccount = true
	f.SetWorker(ws[0])
	if e := musa.must(422, "POST", "/v1/payroll/runs/"+run+"/confirmations", map[string]any{"action": "lock", "key": "l-0"}); e["code"] != "no_account" ||
		e["error"] != "Some people have no account to be paid into: Zainab Bello. Add their account, or leave them out this month." {
		t.Fatalf("no account: %v", e)
	}
	ws[0].NoAccount = false
	f.SetWorker(ws[0])
	sent := musa.must(202, "POST", "/v1/payroll/runs/"+run+"/confirmations", map[string]any{"action": "lock", "key": "l-1"})
	ch := sent["challenge"].(map[string]any)["challenge_id"]
	if e := musa.must(422, "POST", "/v1/payroll/runs/"+run+"/lock", map[string]any{"challenge_id": ch, "code": "111111", "key": "l-2"}); e["code"] != "code_invalid" {
		t.Fatalf("wrong: %v", e)
	}
	now := time.Now()
	f.Now = func() time.Time { return now.Add(6 * time.Minute) }
	if e := musa.must(410, "POST", "/v1/payroll/runs/"+run+"/lock", map[string]any{"challenge_id": ch, "code": f.Code(musaPy), "key": "l-3"}); e["code"] != "code_expired" {
		t.Fatalf("expired: %v", e)
	}
	f.Now = time.Now
	sent = musa.must(202, "POST", "/v1/payroll/runs/"+run+"/confirmations", map[string]any{"action": "lock", "key": "l-4"})
	done := musa.must(200, "POST", "/v1/payroll/runs/"+run+"/lock", map[string]any{"challenge_id": sent["challenge"].(map[string]any)["challenge_id"], "code": f.Code(musaPy), "key": "l-5"})
	if !strings.HasPrefix(done["words"].(string), "Locked. Payrolla pays 2 people ₦") || !strings.HasSuffix(done["words"].(string), " on 30 Nov.") ||
		done["payment"].(map[string]any)["status"] != "SCHEDULED" {
		t.Fatalf("locked: %v", done)
	}
	if f.RunStatus(run) != "LOCKED" {
		t.Fatal("not locked in Payrolla")
	}
	// The payment scheduled is told: "Payrolla pays … on 30 Nov".
	f.Flush()
	w.tick(t)
	if m := lastSent(t, w.graph.Sent(num)); !strings.Contains(m.Text, "November 2026 payroll is locked. Payrolla pays 2 people") {
		t.Fatalf("told it pays: %+v", m)
	}
	if view := musa.must(200, "GET", "/v1/payroll/runs/"+run, nil); view["pays_on"] != "Pays on 30 Nov" {
		t.Fatalf("pays on: %v", view["pays_on"])
	}

	// Every write said who did it and carried its key.
	for _, wr := range f.Writes() {
		if wr.Key == "" || !strings.HasPrefix(wr.ActingUser, "tsk_user_") || strings.Contains(wr.Body, `"code"`) && wr.Method == "GET" {
			t.Fatalf("write %+v", wr)
		}
	}

	// December: rejected in the thread with a note; whoever prepared it
	// (the clerk, on WhatsApp) is told.
	clerkNum := "+2348031119002"
	w.bind(t, clerk, clerkNum)
	w.send(t, clerkNum, whatsapptest.Text("hello"))
	dec := clerk.must(201, "POST", "/v1/payroll/runs", map[string]any{"period": "2026-12", "key": "dec-1"})["run"].(map[string]any)["run_id"].(string)
	if s := w.say(t, num, "reject payroll"); !strings.Contains(s, "To send it back, say: reject payroll, and why.") {
		t.Fatalf("how: %q", s)
	}
	if s := w.say(t, num, "reject payroll, Musa's deduction is wrong"); !strings.HasSuffix(s, "Sent back. Whoever prepared it is told, with your note.") {
		t.Fatalf("rejected: %q", s)
	}
	if f.RunStatus(dec) != "DRAFT" {
		t.Fatalf("not sent back: %s", f.RunStatus(dec))
	}
	f.Flush()
	w.tick(t)
	if m := lastSent(t, w.graph.Sent(clerkNum)); !strings.Contains(m.Text, "Musa Bello sent back December 2026 payroll: Musa's deduction is wrong") {
		t.Fatalf("the preparer told: %+v", m)
	}
	// Cancelled before approval; then nothing more can change it.
	clerk.must(200, "POST", "/v1/payroll/runs/"+dec+"/cancel", map[string]any{"key": "dec-x"})
	if e := clerk.must(409, "POST", "/v1/payroll/runs/"+dec+"/compute", map[string]any{"key": "dec-c"}); e["code"] != "invalid_state" || e["error"] != "This payroll has moved on: it's now cancelled." {
		t.Fatalf("after cancelling: %v", e)
	}

	// January can't be worked out: why, in words, with Recalculate.
	f.Runs().ComputeError = "Pension rates are not set"
	jan := owner.must(201, "POST", "/v1/payroll/runs", map[string]any{"period": "2027-01", "key": "jan-1"})
	if jan["compute_error"] != "Payrolla couldn't work out this payroll: Pension rates are not set. Fix it in Payrolla's settings, then Recalculate." {
		t.Fatalf("compute error: %v", jan)
	}
	janID := jan["run"].(map[string]any)["run_id"].(string)
	if v := owner.must(200, "GET", "/v1/payroll/runs/"+janID, nil); v["not_worked_out"] == nil {
		t.Fatalf("draft: %v", v)
	}
	f.Runs().ComputeError = ""
	if r := owner.must(200, "POST", "/v1/payroll/runs/"+janID+"/compute", map[string]any{"key": "jan-c"}); r["run"].(map[string]any)["status"] != "COMPUTED" {
		t.Fatalf("recalculated: %v", r)
	}

	// "stop" lets a waiting code go; too many codes is said in words.
	f.Flush()
	if s := w.press(t, num, "pr:a:"+janID); len(s) != 1 || !strings.Contains(s[0].Text, "Type it here to approve January 2027 payroll") {
		t.Fatalf("asking: %+v", s)
	}
	if s := w.say(t, num, "stop"); !strings.HasSuffix(s, "Stopped. That code won't be used.") {
		t.Fatalf("stop: %q", s)
	}
	limited := false
	for i := range 6 {
		st, e := musa.do("POST", "/v1/payroll/runs/"+janID+"/confirmations", map[string]any{"action": "approve", "key": "j-" + string(rune('a'+i))})
		if st == 429 {
			if e["code"] != "too_many_codes" || e["error"] != "Payrolla has sent you too many codes. Try again in 10 minutes." {
				t.Fatalf("too many: %v", e)
			}
			limited = true
			break
		}
	}
	if !limited {
		t.Fatal("no limit on codes")
	}
}

// On Telegram the card's Approve asks Payrolla for a code, typed in the
// thread; "lock payroll" the same; the Taskiem chat on the web shows the
// card, kept as the run's id and period and drawn live.
func TestPayrollRunOnTelegramAndTheWebChat(t *testing.T) {
	w := newTGWorld(t)
	w.srv.Egress = &egress.Guard{Blocked: func(netip.Addr) bool { return false }}
	f := payrollatest.New(payrollatest.Client{ID: "pyc", Secret: "pys"})
	t.Cleanup(f.Close)
	f.ServeRuns()
	payrollWorkers(f)
	if err := w.env.Registry.Register(payrolla.New(payrolla.Options{BaseURL: f.API(), TestURL: f.API(), LiveURL: f.API()})); err != nil {
		t.Fatal(err)
	}
	owner := w.tenant(t, "Amara Foods", "ngozi@amara.test")
	key := f.AddKey("payroll:read", "employees:read", "payroll:write", "payroll:approve")
	owner.must(201, "POST", "/v1/connections", map[string]any{"environment": "prod", "connector": "payrolla@1", "name": "main", "mode": "test",
		"credentials": map[string]string{"api_key": key}})
	linkPayrolla(t, f, owner, ngoziPy)
	run := owner.must(201, "POST", "/v1/payroll/runs", map[string]any{"period": "2026-11", "key": "p"})["run"].(map[string]any)["run_id"].(string)

	said := owner.must(200, "POST", "/v1/taskiem-chat", map[string]any{"text": "this month's payroll"})
	msgs := said["messages"].([]any)
	last := msgs[len(msgs)-1].(map[string]any)
	if last["kind"] != "payroll_run" || !strings.Contains(last["text"].(string), run) || !strings.Contains(last["text"].(string), "November 2026") {
		t.Fatalf("web card: %v", msgs)
	}

	const account = int64(7001)
	w.link(t, owner, account, "ngozi")
	card := w.say(t, account, "payroll")
	if !strings.Contains(card.Text, "November 2026 payroll") || !strings.Contains(card.Text, "3 people") || len(card.Keyboard) == 0 {
		t.Fatalf("card on Telegram: %+v", card)
	}
	got := w.update(t, account, func(id int64) []byte {
		return telegramtest.Callback(id, account, card.MessageID, "q1", card.Keyboard[0][0].Data)
	})
	asked := ""
	for _, c := range got {
		if c.Method == "sendMessage" {
			asked = c.Text
		}
	}
	if !strings.Contains(asked, "Type it here to approve November 2026 payroll") {
		t.Fatalf("asked: %+v", got)
	}
	if r := w.say(t, account, f.Code(ngoziPy)); !strings.Contains(r.Text, "Approved. Lock and pay?") {
		t.Fatalf("approved: %+v", r)
	}
	if r := w.say(t, account, "lock payroll"); !strings.Contains(r.Text, "Type it here to lock November 2026 payroll and pay everyone on 30 Nov.") {
		t.Fatalf("lock asked: %+v", r)
	}
	code := f.Code(ngoziPy)
	if r := w.say(t, account, code[:2]+" "+code[2:4]+" "+code[4:]); !strings.Contains(r.Text, "Locked. Payrolla pays 3 people") {
		t.Fatalf("locked: %+v", r)
	}
	if f.RunStatus(run) != "LOCKED" {
		t.Fatal("not locked")
	}
}
