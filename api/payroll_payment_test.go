package api_test

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/israel-duff/taskiem/engine/whatsapp/whatsapptest"
)

// approveAndLock approves a run (as approver, linked to approverPy) and
// locks it (as locker), each with Payrolla's code, through the API.
func approveAndLock(t *testing.T, w *payrollaWorld, run string, approver *client, approverPy string, locker *client, lockerPy string) {
	t.Helper()
	for _, step := range []struct {
		c      *client
		py, ac string
	}{{approver, approverPy, "approve"}, {locker, lockerPy, "lock"}} {
		sent := step.c.must(202, "POST", "/v1/payroll/runs/"+run+"/confirmations", map[string]any{"action": step.ac, "key": "c-" + step.ac + run})
		step.c.must(200, "POST", "/v1/payroll/runs/"+run+"/"+step.ac, map[string]any{"challenge_id": sent["challenge"].(map[string]any)["challenge_id"],
			"code": w.fake.Code(step.py), "key": "d-" + step.ac + run})
	}
}

func payslipGet(t *testing.T, w *payrollaWorld, c *client, path string) (int, http.Header, []byte) {
	t.Helper()
	req, _ := http.NewRequest("GET", w.base+path, nil)
	req.Header.Set("Authorization", "Bearer "+c.token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, resp.Header, b
}

// A locked run pays on its date: short on the day (said once, to the
// owner on WhatsApp), paid after a top-up with one person not paid (who
// and why, with Pay the failed ones again), sent again with a code typed
// in the thread; each linked employee gets only "Your payslip for
// November is ready" and opens their own card, never another's; the
// payer side opens payslips only with payroll.read; Save as PDF; the
// morning warnings three days and one day before; Pay now in the thread.
// Nothing of anyone's pay is kept in the audit or the outbox.
func TestPayrollAfterLocking(t *testing.T) {
	ctx := context.Background()
	w := newPayrollaWorld(t)
	f := w.fake
	payrollWorkers(f)
	f.Runs().WalletKobo = 100
	owner := w.tenant(t, "Amara Foods", "ngozi@amara.test")
	w.connect(t, owner, map[string]any{"mode": "test"})
	owner.must(201, "PUT", "/v1/roles/payroll", map[string]any{"permissions": []string{"payroll.read", "payroll.approve"}})
	owner.must(201, "PUT", "/v1/roles/paydecider", map[string]any{"permissions": []string{"payroll.approve"}})
	owner.must(201, "PUT", "/v1/roles/clerk", map[string]any{"permissions": []string{"payroll.prepare"}})
	musa := w.member(t, owner, "musa@amara.test", "payroll")
	tunde := w.member(t, owner, "tunde@amara.test", "paydecider")
	clerk := w.member(t, owner, "clerk@amara.test", "clerk")
	zainab := w.member(t, owner, "zainab@amara.test", "cashier")
	linkPayrolla(t, f, owner, ngoziPy)
	linkPayrolla(t, f, musa, musaPy)
	owner.must(200, "PUT", fmt.Sprintf("/v1/staff/%s/payrolla", personID(t, zainab)), map[string]any{"employee_id": zainabE})

	run := owner.must(201, "POST", "/v1/payroll/runs", map[string]any{"period": "2026-11", "key": "prep"})["run"].(map[string]any)["run_id"].(string)
	view := owner.must(200, "GET", "/v1/payroll/runs/"+run, nil)
	zSlip := lineByName(t, view, "Zainab Bello")["payslip_id"].(string)
	mSlip := lineByName(t, view, "Musa Ali")["payslip_id"].(string)
	zNet := int64(lineByName(t, view, "Zainab Bello")["net_kobo"].(float64))
	// Not a payslip until the payroll is locked.
	if e := owner.must(409, "GET", "/v1/payroll/payslips/"+zSlip, nil); e["code"] != "payslip_not_final" || e["error"] != "This payslip is ready once the payroll is locked." {
		t.Fatalf("not final: %v", e)
	}
	approveAndLock(t, w, run, musa, musaPy, owner, ngoziPy)

	// Scheduled: the owner may pay now; Musa may not pay in Payrolla.
	pay := owner.must(200, "GET", "/v1/payroll/runs/"+run, nil)["payment"].(map[string]any)
	if pay["status"] != "SCHEDULED" || pay["words"] != "Pays on 30 Nov" || pay["may_pay_now"] != true || pay["may_payslips"] != true {
		t.Fatalf("scheduled: %v", pay)
	}
	if mp := musa.must(200, "GET", "/v1/payroll/runs/"+run, nil)["payment"].(map[string]any); mp["may_pay_now"] != false || mp["cannot_pay"] != true {
		t.Fatalf("musa: %v", mp)
	}
	if e := musa.must(403, "POST", "/v1/payroll/runs/"+run+"/confirmations", map[string]any{"action": "pay_now", "key": "m-p"}); e["code"] != "cannot_pay" ||
		e["error"] != "In Payrolla you can't send payments. Ask the owner." {
		t.Fatalf("cannot pay: %v", e)
	}
	if e := owner.must(409, "POST", "/v1/payroll/runs/"+run+"/confirmations", map[string]any{"action": "retry_failed", "key": "o-r0"}); e["code"] != "nothing_to_retry" ||
		e["error"] != "There are no failed payments to send again." {
		t.Fatalf("nothing to retry: %v", e)
	}

	// The owner and Zainab on WhatsApp.
	num, zNum := "+2348031119101", "+2348031119102"
	w.bind(t, owner, num)
	w.send(t, num, whatsapptest.Text("hello"))
	w.send(t, num, whatsapptest.Text("cancel")) // the menu "hello" opened
	w.bind(t, zainab, zNum)
	w.send(t, zNum, whatsapptest.Text("hello"))
	w.send(t, zNum, whatsapptest.Text("cancel"))
	f.Flush()
	w.tick(t)

	// Pay day, the wallet short: said once, however often Payrolla says it.
	f.PayDay(run)
	f.Flush()
	w.tick(t)
	short := lastSent(t, w.graph.Sent(num))
	if !strings.Contains(short.Text, "November 2026 payroll is waiting for money: short by ₦") || !strings.Contains(short.Text, "Payrolla pays as soon as it's in.") {
		t.Fatalf("short: %+v", short)
	}
	n := len(w.graph.Sent(num))
	f.PayDay(run)
	f.Flush()
	w.tick(t)
	if len(w.graph.Sent(num)) != n {
		t.Fatalf("told twice: %+v", lastSent(t, w.graph.Sent(num)))
	}
	if p := owner.must(200, "GET", "/v1/payroll/runs/"+run, nil)["payment"].(map[string]any); p["status"] != "AWAITING_FUNDS" ||
		!strings.HasPrefix(p["words"].(string), "Waiting for money: short by ₦") {
		t.Fatalf("awaiting: %v", p)
	}
	if e := owner.must(409, "POST", "/v1/payroll/runs/"+run+"/confirmations", map[string]any{"action": "pay_now", "key": "o-p"}); e["code"] != "awaiting_funds" ||
		!strings.HasPrefix(e["error"].(string), "Your Payrolla wallet is short by ₦") {
		t.Fatalf("pay now while short: %v", e)
	}

	// Topped up: paid, Musa Ali's account closed.
	f.FailPayout(musaE, "Account closed")
	f.TopUp(200000000)
	f.Flush()
	w.tick(t)
	paid := lastSent(t, w.graph.Sent(num))
	if !strings.Contains(paid.Text, "November 2026 payroll: paid 2 of 3 people. 1 not paid: Musa Ali (account closed).") || len(paid.Buttons) != 1 ||
		paid.Buttons[0].ID != "pr:f:"+run {
		t.Fatalf("results: %+v", paid)
	}
	slip := lastSent(t, w.graph.Sent(zNum))
	if !strings.Contains(slip.Text, "Your payslip for November is ready") || !strings.Contains(slip.Text, "/payroll/me/payslips/"+zSlip) || strings.Contains(slip.Text, "₦") {
		t.Fatalf("payslip notice: %+v", slip)
	}
	if p := owner.must(200, "GET", "/v1/payroll/runs/"+run, nil); p["payment"].(map[string]any)["words"] != "Paid, 1 not paid" ||
		lineByName(t, p, "Musa Ali")["reason"] != "Account closed" || p["payment"].(map[string]any)["may_retry"] != true {
		t.Fatalf("paid some: %v", p["payment"])
	}

	// Pay the failed ones again, with the code typed in the thread.
	said := w.press(t, num, "pr:f:"+run)
	if len(said) != 1 || !strings.Contains(said[0].Text, "Type it here to send November 2026 payroll's failed payments again.") {
		t.Fatalf("asked: %+v", said)
	}
	code := f.Code(ngoziPy)
	if s := w.say(t, num, code); !strings.Contains(s, "Sent again. Payrolla pays each of them into their current account.") {
		t.Fatalf("sent again: %q", s)
	}
	if f.PaymentStatus(run) != "COMPLETED" {
		t.Fatalf("payment: %s", f.PaymentStatus(run))
	}
	if s := w.say(t, num, "pay the failed salaries again"); !strings.Contains(s, "There are no failed payments to send again.") {
		t.Fatalf("again: %q", s)
	}

	// Payslips: anyone's with payroll.read; approving or preparing is not enough.
	ps := owner.must(200, "GET", "/v1/payroll/payslips/"+zSlip, nil)["payslip"].(map[string]any)
	labels := ""
	for _, it := range ps["items"].([]any) {
		labels += it.(map[string]any)["label"].(string) + ";"
	}
	if ps["employer"].(map[string]any)["rc_number"] != "RC1234567" || ps["period"] != "November 2026" || !strings.Contains(labels, "Tax (PAYE);") ||
		ps["payment"].(map[string]any)["words"] != "Paid" {
		t.Fatalf("payslip: %v", ps)
	}
	tunde.must(403, "GET", "/v1/payroll/payslips/"+zSlip, nil)
	clerk.must(403, "GET", "/v1/payroll/payslips/"+zSlip, nil)
	zainab.must(403, "GET", "/v1/payroll/payslips/"+zSlip, nil)
	st, hdr, body := payslipGet(t, w, owner, "/v1/payroll/payslips/"+zSlip+"/pdf")
	if st != 200 || hdr.Get("Content-Type") != "application/pdf" || !strings.HasPrefix(string(body), "%PDF") || hdr.Get("Cache-Control") != "no-store" {
		t.Fatalf("pdf: %d %v %.20q", st, hdr, body)
	}
	if l := owner.must(201, "POST", "/v1/payroll/payslips/"+zSlip+"/link", map[string]any{"key": "lk"}); !strings.HasPrefix(l["url"].(string), "https://") {
		t.Fatalf("link: %v", l)
	}

	// Zainab's own: listed on My pay, opened, saved; never Musa's.
	mine := zainab.must(200, "GET", "/v1/payroll/me/payslips", nil)
	if list := mine["payslips"].([]any); mine["linked"] != true || len(list) != 1 || list[0].(map[string]any)["payslip_id"] != zSlip || list[0].(map[string]any)["period"] != "November 2026" {
		t.Fatalf("mine: %v", mine)
	}
	if own := zainab.must(200, "GET", "/v1/payroll/me/payslips/"+zSlip, nil)["payslip"].(map[string]any); own["net_kobo"] != float64(zNet) {
		t.Fatalf("own: %v", own)
	}
	zainab.must(404, "GET", "/v1/payroll/me/payslips/"+mSlip, nil)
	if st, _, body := payslipGet(t, w, zainab, "/v1/payroll/me/payslips/"+zSlip+"/pdf"); st != 200 || !strings.HasPrefix(string(body), "%PDF") {
		t.Fatalf("own pdf: %d", st)
	}
	if s := w.say(t, zNum, "my payslips"); !strings.Contains(s, "November 2026: ") || !strings.Contains(s, "/payroll/me/payslips/"+zSlip) || strings.Contains(s, "₦") {
		t.Fatalf("my payslips: %q", s)
	}
	web := zainab.must(200, "POST", "/v1/taskiem-chat", map[string]any{"text": "my payslip"})["messages"].([]any)
	if last := web[len(web)-1].(map[string]any); last["kind"] != "payslip" || !strings.Contains(last["text"].(string), zSlip) {
		t.Fatalf("web card: %v", web)
	}
	if s := w.say(t, num, "my payslip"); !strings.Contains(s, "You're not linked to payroll here yet.") {
		t.Fatalf("not linked: %q", s)
	}

	// December: approved and locked with the wallet short; warned in the
	// morning three days and one day before, once each; then paid now
	// with a code typed in the thread.
	f.Runs().WalletKobo = 100
	dec := owner.must(201, "POST", "/v1/payroll/runs", map[string]any{"period": "2026-12", "key": "dec"})["run"].(map[string]any)["run_id"].(string)
	approveAndLock(t, w, dec, musa, musaPy, owner, ngoziPy)
	f.Flush()
	w.tick(t)
	lagos, _ := time.LoadLocation("Africa/Lagos")
	if q, err := w.srv.PayrollFundsCheck(ctx, time.Date(2026, 12, 28, 6, 0, 0, 0, lagos)); err != nil || q != 0 {
		t.Fatalf("before the morning: %d %v", q, err)
	}
	for want := range []int{1, 0} {
		// Counted only when queued: the second check of the morning adds nothing.
		if q, err := w.srv.PayrollFundsCheck(ctx, time.Date(2026, 12, 28, 9, 0, 0, 0, lagos)); err != nil || q != 1-want {
			t.Fatalf("three days before: %d %v", q, err)
		}
		w.tick(t)
	}
	warned := 0
	for _, m := range w.graph.Sent(num) {
		if strings.Contains(m.Text, "December 2026 payroll pays on 31 Dec, in 3 days.") {
			warned++
		}
	}
	if warned != 1 {
		t.Fatalf("warned %d times: %+v", warned, lastSent(t, w.graph.Sent(num)))
	}
	if q, _ := w.srv.PayrollFundsCheck(ctx, time.Date(2026, 12, 29, 9, 0, 0, 0, lagos)); q != 0 {
		t.Fatalf("two days before: %d", q)
	}
	_, _ = w.srv.PayrollFundsCheck(ctx, time.Date(2026, 12, 30, 9, 0, 0, 0, lagos))
	w.tick(t)
	if m := lastSent(t, w.graph.Sent(num)); !strings.Contains(m.Text, "December 2026 payroll pays tomorrow, 31 Dec.") {
		t.Fatalf("a day before: %+v", m)
	}
	// Topped up: no warning.
	f.TopUp(500000000)
	if q, _ := w.srv.PayrollFundsCheck(ctx, time.Date(2026, 12, 30, 10, 0, 0, 0, lagos)); q != 0 {
		t.Fatalf("covered: %d", q)
	}
	if s := w.say(t, num, "pay payroll now"); !strings.Contains(s, "Type it here to pay December 2026 payroll now.") {
		t.Fatalf("pay now asked: %q", s)
	}
	if s := w.say(t, num, f.Code(ngoziPy)); !strings.Contains(s, "Paying now. Payrolla pays 3 people ₦") {
		t.Fatalf("paid now: %q", s)
	}
	if f.PaymentStatus(dec) != "COMPLETED" {
		t.Fatalf("december: %s", f.PaymentStatus(dec))
	}

	// Nothing of anyone's pay in the audit or the outbox; never a code.
	var leaks int
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT (SELECT count(*) FROM audit_log WHERE detail::text ~ ('net_kobo|gross_kobo|amount_kobo|' || $1 || '|' || $2))
		+ (SELECT count(*) FROM whatsapp_outbox WHERE coalesce(reach_reference, '') ~ ('net_kobo|gross_kobo|' || $1 || '|' || $2))`,
		fmt.Sprint(zNet), code).Scan(&leaks); err != nil || leaks != 0 {
		t.Fatalf("pay or a code kept %d times: %v", leaks, err)
	}
	for _, wr := range f.Writes() {
		if wr.Key == "" || !strings.HasPrefix(wr.ActingUser, "tsk_user_") {
			t.Fatalf("write %+v", wr)
		}
	}
}

// The morning check counts the payrolls paying before one with it: two
// that each fit the wallet but not together are warned of. Only a
// connection made in one step is checked; a key connection's business
// has the payrolla-payday-check template.
func TestPayrollFundsCheckTogether(t *testing.T) {
	ctx := context.Background()
	w := newPayrollaWorld(t)
	f := w.fake
	payrollWorkers(f)
	owner := w.tenant(t, "Amara Foods", "ngozi@amara.test")
	w.connect(t, owner, map[string]any{"mode": "test"})
	owner.must(201, "PUT", "/v1/roles/payroll", map[string]any{"permissions": []string{"payroll.read", "payroll.approve"}})
	musa := w.member(t, owner, "musa@amara.test", "payroll")
	linkPayrolla(t, f, owner, ngoziPy)
	linkPayrolla(t, f, musa, musaPy)
	// October's, late, pays on 30 Dec; December's on 31 Dec.
	oct := owner.must(201, "POST", "/v1/payroll/runs", map[string]any{"period": "2026-10", "pay_date": "2026-12-30", "key": "oct"})["run"].(map[string]any)
	dec := owner.must(201, "POST", "/v1/payroll/runs", map[string]any{"period": "2026-12", "key": "dec"})["run"].(map[string]any)
	approveAndLock(t, w, oct["run_id"].(string), musa, musaPy, owner, ngoziPy)
	approveAndLock(t, w, dec["run_id"].(string), musa, musaPy, owner, ngoziPy)
	octNet, decNet := int64(oct["total_net_kobo"].(float64)), int64(dec["total_net_kobo"].(float64))
	f.Runs().WalletKobo = max(octNet, decNet) + 100
	lagos, _ := time.LoadLocation("Africa/Lagos")

	// 28 Dec: December's pays in three days, after October's.
	if q, err := w.srv.PayrollFundsCheck(ctx, time.Date(2026, 12, 28, 9, 0, 0, 0, lagos)); err != nil || q != 1 {
		t.Fatalf("together: %d %v", q, err)
	}
	var ref string
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT reach_reference FROM whatsapp_outbox WHERE kind = 'payroll_run' AND reach_reference LIKE '%"warn3"%' LIMIT 1`).Scan(&ref); err != nil ||
		!strings.Contains(ref, `"run":"`+dec["run_id"].(string)+`"`) || !strings.Contains(ref, fmt.Sprintf(`"short_kobo":%d`, octNet+decNet-f.Runs().WalletKobo)) ||
		!strings.Contains(ref, fmt.Sprintf(`"needed_kobo":%d`, octNet+decNet)) {
		t.Fatalf("warned: %s %v", ref, err)
	}

	// A key connection: not checked (it would warn of both a day before).
	if _, err := w.env.DB.Admin.Exec(ctx, `UPDATE connections SET auth_type = 'api_key' WHERE connector = 'payrolla'`); err != nil {
		t.Fatal(err)
	}
	if q, err := w.srv.PayrollFundsCheck(ctx, time.Date(2026, 12, 30, 9, 0, 0, 0, lagos)); err != nil || q != 0 {
		t.Fatalf("a key connection: %d %v", q, err)
	}
	if _, err := w.env.DB.Admin.Exec(ctx, `UPDATE connections SET auth_type = 'oauth2' WHERE connector = 'payrolla'`); err != nil {
		t.Fatal(err)
	}
	if q, err := w.srv.PayrollFundsCheck(ctx, time.Date(2026, 12, 30, 9, 0, 0, 0, lagos)); err != nil || q != 1 {
		t.Fatalf("one step, a day before: %d %v", q, err)
	}
}
