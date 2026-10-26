package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/israel-duff/taskiem/connectors/notify"
	"github.com/israel-duff/taskiem/engine/invoicing"
	"github.com/israel-duff/taskiem/engine/schools"
	"github.com/israel-duff/taskiem/engine/sender"
)

// The receipt, the account number and the daily close as their own
// templates outside WhatsApp's 24-hour window (docs/whatsapp.md
// #templates): each goes as its template once the registry holds it
// approved, and falls back as before until then, the reason on the outbox
// message.

// schoolWithChidi is a school whose one student's guardian, Chidi, gets
// the school's updates on WhatsApp, his window closed and nothing
// approved yet: the family's id and the student's.
func schoolWithChidi(t *testing.T, w *reachWorld, owner *client, number string) (family, student string) {
	t.Helper()
	owner.must(200, "PUT", "/v1/settings/schools", map[string]any{"enabled": true})
	linkChidi(t, w, owner, number)
	closeWindow(t, w.world, number)
	gatePlatform(t, w.graph, w.srv.WhatsApp)
	owner.must(201, "POST", "/v1/schools/classes", map[string]any{"name": "JSS 1", "arm": "A", "order": 7})
	csv := "Name,Admission number,Class,Guardian name,Phone\nEbube Eze,GCA/001,JSS 1 A,Chidi Eze,0" + strings.TrimPrefix(number, "+234") + "\n"
	plan := owner.must(200, "POST", "/v1/schools/students/import", map[string]any{"csv": csv})
	owner.must(200, "POST", "/v1/schools/students/import", map[string]any{"csv": csv, "confirm": plan["hash"]})
	st := owner.must(200, "GET", "/v1/schools/students/GCA%2F001", nil)["student"].(map[string]any)
	return st["family"].(map[string]any)["id"].(string), st["id"].(string)
}

func TestReceiptTemplateOutsideWindow(t *testing.T) {
	w := newReachWorld(t)
	ctx := context.Background()
	inv := &invoicing.Invoices{Pool: w.env.DB.App, Vault: w.env.Vault, PageBase: "https://" + reachFormHost, Messages: w.srv.Messages,
		Signal: w.env.Store.DeliverSignalOnce, Reach: w.srv.Reach, Chat: []string{"whatsapp", "telegram"}}
	w.srv.Invoices = inv
	rec := &schools.Schools{Pool: w.env.DB.App, Vault: w.env.Vault, Invoices: inv}
	schools.WireReceipts(inv, rec)
	owner := w.tenant(t, "Greenfield", "proprietor@greenfield.test")
	const chidi = "+2348039998888"
	schoolWithChidi(t, w, owner, chidi)
	tenant := tenantIDNamed(t, w.world, "Greenfield")

	// A started term with fees, invoiced and sent; each cash posting
	// counted towards the invoice issues a receipt.
	sess := owner.must(201, "POST", "/v1/schools/sessions", map[string]any{"label": "2026/2027"})
	term := owner.must(201, "POST", "/v1/schools/sessions/"+sess["id"].(string)+"/terms",
		map[string]any{"name": "First term", "starts_on": "2026-09-14", "ends_on": "2099-12-11", "fees_due_on": "2099-09-07"})
	tid := term["id"].(string)
	class := owner.must(200, "GET", "/v1/schools/classes", nil)["classes"].([]any)[0].(map[string]any)["id"].(string)
	owner.must(200, "POST", "/v1/schools/terms/"+tid+"/start", nil)
	owner.must(200, "PUT", "/v1/schools/terms/"+tid+"/fees/"+class, map[string]any{"items": []any{map[string]any{"name": "Tuition", "amount_kobo": 10_000_000, "kind": "required"}}})
	p := owner.must(200, "POST", "/v1/schools/terms/"+tid+"/invoice", map[string]any{})["plan"].(map[string]any)
	owner.must(200, "POST", "/v1/schools/terms/"+tid+"/invoice", map[string]any{"confirm": p["hash"], "send": true})
	w.tick(t)
	receipt := func(key string, kobo int) map[string]any {
		t.Helper()
		posted := owner.must(201, "POST", "/v1/schools/payments", map[string]any{"student": "GCA/001", "amount_kobo": kobo, "how": "cash"}, "Idempotency-Key", key)["posting"].(map[string]any)
		number := posted["result"].(map[string]any)["parts"].([]any)[0].(map[string]any)["receipt_number"].(string)
		return owner.must(200, "GET", "/v1/schools/receipts?number="+number, nil)["receipts"].([]any)[0].(map[string]any)
	}
	send := func(number string) schools.FamilySent {
		t.Helper()
		out, err := schools.Service{Schools: rec}.SendReceiptOnce(ctx, tenant, number, "test")
		if err != nil {
			t.Fatal(err)
		}
		return out.Sent
	}

	// Not approved: the guardian's chat message falls back to SMS, with
	// the same facts and the check link.
	first := receipt("post-1", 2_000_000)
	before := len(w.graph.Sent(chidi))
	if sent := send(first["number"].(string)); sent.Channels["whatsapp"] != 1 {
		t.Fatalf("sent %+v", sent)
	}
	// Once per receipt: asking again sends nothing more.
	if sent := send(first["number"].(string)); sent.Channels["whatsapp"] != 1 {
		t.Fatalf("sent again %+v", sent)
	}
	w.tick(t)
	if len(w.graph.Sent(chidi)) != before {
		t.Fatalf("sent without an approved template: %+v", w.graph.Sent(chidi)[before:])
	}
	if d := droppedErrors(t, owner); !strings.Contains(d, "not approved") || !strings.Contains(d, "sent by SMS instead") {
		t.Fatalf("dropped: %s", d)
	}
	var sms int
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT count(*) FROM platform_messages WHERE channel = 'sms' AND dedup_key LIKE 'family-fallback:%'`).Scan(&sms); err != nil || sms != 1 {
		t.Fatalf("SMS fallbacks: %d %v", sms, err)
	}

	// Approved: the receipt template, its variables and the check link's
	// token on the button.
	approve(t, w.graph, w.srv.WhatsApp, "taskiem_receipt")
	second := receipt("post-2", 3_000_000)
	send(second["number"].(string))
	w.tick(t)
	msg := w.graph.Last(t, chidi)
	want := "[Greenfield] Receipt " + second["number"].(string) + ": we received ₦30,000 from Ebube Eze towards INV-0001 (school fees). ₦50,000 is still left on it. Tap Check receipt to confirm it is valid."
	if msg.Type != "template" || msg.Template != "taskiem_receipt" || msg.Text != want || len(msg.URLParams) != 1 || "https://"+reachFormHost+"/r/"+msg.URLParams[0] != second["link"].(string) {
		t.Fatalf("the receipt template: %+v\nwant %q (link %v)", msg, want, second["link"])
	}
}

func TestAccountNumberTemplateOutsideWindow(t *testing.T) {
	w := newReachWorld(t)
	inv := &invoicing.Invoices{Pool: w.env.DB.App, Vault: w.env.Vault, PageBase: "https://" + reachFormHost, Messages: w.srv.Messages,
		Signal: w.env.Store.DeliverSignalOnce, Reach: w.srv.Reach, Chat: []string{"whatsapp", "telegram"}}
	w.srv.Invoices = inv
	w.srv.AccountRails = &accountRails{opened: map[string]string{}, fail: map[string]error{}}
	owner := w.tenant(t, "Greenfield", "proprietor@greenfield.test")
	const chidi = "+2348039998888"
	_, student := schoolWithChidi(t, w, owner, chidi)
	viewer := addMember(t, w.world, owner, "vic", "viewer")
	path := "/v1/schools/students/" + student + "/account/send"

	// No account number yet: refused; only managers send.
	owner.must(409, "POST", path, nil)
	owner.must(200, "PUT", "/v1/settings/schools", map[string]any{"enabled": true, "issuing": map[string]any{"connection": "iswallet/main", "account": "wal_1"}})
	issued := owner.must(200, "POST", "/v1/schools/students/GCA%2F001/account", map[string]any{})
	number := issued["account_number"].(string)
	viewer.must(403, "POST", path, nil)

	// Not approved: by SMS instead.
	before := len(w.graph.Sent(chidi))
	sent := owner.must(200, "POST", path, nil)
	if sent["channels"].(map[string]any)["whatsapp"] != float64(1) || sent["account_number"] != number {
		t.Fatalf("sent %v", sent)
	}
	w.tick(t)
	if len(w.graph.Sent(chidi)) != before {
		t.Fatalf("sent without an approved template: %+v", w.graph.Sent(chidi)[before:])
	}
	if d := droppedErrors(t, owner); !strings.Contains(d, "not approved") || !strings.Contains(d, "sent by SMS instead") {
		t.Fatalf("dropped: %s", d)
	}
	if n := schoolAudits(t, w.world, "Greenfield", "school.account.send"); n != 1 {
		t.Errorf("%d send audits", n)
	}

	// Approved: the account number template with the family page's token
	// on its button; each send from the page is a fresh message.
	approve(t, w.graph, w.srv.WhatsApp, "taskiem_account_number")
	owner.must(200, "POST", path, nil)
	w.tick(t)
	msg := w.graph.Last(t, chidi)
	if msg.Type != "template" || msg.Template != "taskiem_account_number" ||
		!strings.HasPrefix(msg.Text, "[Greenfield] Ebube Eze's own account number for payments is "+number+" at Wema Bank, in the name ") ||
		!strings.Contains(msg.Text, "Pay by transfer to it: the money is matched by itself and a receipt follows.") || len(msg.URLParams) != 1 {
		t.Fatalf("the account number template: %+v", msg)
	}
	var links int
	if err := w.env.DB.Admin.QueryRow(context.Background(), `SELECT count(*) FROM school_family_links WHERE purpose = 'account'`).Scan(&links); err != nil || links != 2 {
		t.Errorf("family links minted: %d %v", links, err)
	}
}

const closeFlow = `{"schema":"wd/v1","id":"wf_close","version":1,"name":"Close","trigger":{"type":"manual"},
  "inputs":{"schema":{"type":"object","properties":{"summary":{"type":"string"}}}},
  "steps":[
    {"id":"send","type":"connector","name":"Send the proprietor the close","connector":"notify@1","action":"tell",
     "input":{"to":"owner","title":"Daily close 2026-10-24","message":"='Daily close, 24 Oct 2026. ' + trigger.body.summary",
              "template":"taskiem_daily_close","template_vars":{"day":"24 Oct 2026","summary":"=trigger.body.summary"}}}]}`

// A Tell me step that names its template (the school's daily close) goes
// as that template outside the window once approved, and as the alert
// template until then, the reason on the message; a template a Tell me
// message cannot carry is refused when the workflow is published.
func TestDailyCloseTemplateOutsideWindow(t *testing.T) {
	w := newWAWorld(t)
	if err := w.env.Registry.Register(notify.New(&notify.Notifier{Pool: w.env.Store.Pool, Chat: []string{"whatsapp"},
		Messages: &sender.Queue{Vault: w.env.Vault, HashKey: bytes.Repeat([]byte{8}, 32)}})); err != nil {
		t.Fatal(err)
	}
	owner := w.tenant(t, "Greenfield", "proprietor@greenfield.test")
	const on = "+2348010000081"
	w.bind(t, owner, on)
	w.say(t, on, "help")
	closeWindow(t, w.world, on)
	gatePlatform(t, w.graph, w.wa)
	approve(t, w.graph, w.wa, "taskiem_alert")

	// Refused at publish: a family's template, or the wrong variables.
	bad := owner.must(201, "POST", "/v1/workflows", map[string]any{"name": "bad", "definition": json.RawMessage(strings.Replace(closeFlow, "taskiem_daily_close", "taskiem_receipt", 1))})
	if probs := toJSON(bad["problems"]); !strings.Contains(probs, "not one a Tell me message can carry") {
		t.Fatalf("a family's template published: %s", probs)
	}
	bad = owner.must(201, "POST", "/v1/workflows", map[string]any{"name": "bad", "definition": json.RawMessage(strings.Replace(closeFlow, `"day":"24 Oct 2026",`, "", 1))})
	if probs := toJSON(bad["problems"]); !strings.Contains(probs, "needs template_vars.day") {
		t.Fatalf("a variable missing published: %s", probs)
	}

	wf := publishFlow(t, owner, closeFlow)
	run := func(summary string) {
		t.Helper()
		owner.must(201, "POST", "/v1/workflows/"+wf+"/runs", map[string]any{"input": map[string]any{"summary": summary}})
		w.env.Drain(t)
		w.tick(t)
	}
	// Not approved: the alert template carries the title and the message,
	// and the message says why it went that way.
	run("₦150,000 in on students (2 payments); matched ₦150,000; not matched ₦0; everything reconciles.")
	msg := w.graph.Last(t, on)
	if msg.Type != "template" || msg.Template != "taskiem_alert" || !strings.Contains(msg.Text, "Daily close 2026-10-24: Daily close, 24 Oct 2026.") {
		t.Fatalf("without the close template: %+v", msg)
	}
	sent := toJSON(owner.must(200, "GET", "/v1/whatsapp/outbox?status=sent", nil))
	if !strings.Contains(sent, "sent another way") || !strings.Contains(sent, "as taskiem_alert, since taskiem_daily_close could not go") || !strings.Contains(sent, "not approved") {
		t.Fatalf("the reason on the message: %s", sent)
	}

	// Approved: the close template, the day and the summary, with Open.
	approve(t, w.graph, w.wa, "taskiem_daily_close")
	run("₦20,000 in on students (1 payments); matched ₦20,000; not matched ₦0; everything reconciles.")
	msg = w.graph.Last(t, on)
	want := "[Greenfield] Daily close, 24 Oct 2026: ₦20,000 in on students (1 payments); matched ₦20,000; not matched ₦0; everything reconciles. Tap Open for every item and where to fix it."
	if msg.Type != "template" || msg.Template != "taskiem_daily_close" || msg.Text != want {
		t.Fatalf("the close template: %+v\nwant %q", msg, want)
	}
}
