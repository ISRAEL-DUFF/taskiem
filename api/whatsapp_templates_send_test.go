package api_test

import (
	"bytes"
	"context"
	"fmt"
	"net/url"
	"strings"
	"testing"

	"github.com/israel-duff/taskiem/engine/invoicing"
	"github.com/israel-duff/taskiem/engine/matching"
	"github.com/israel-duff/taskiem/engine/moneylog"
	"github.com/israel-duff/taskiem/engine/schools"
	"github.com/israel-duff/taskiem/engine/sender"
	"github.com/israel-duff/taskiem/engine/whatsapp"
	"github.com/israel-duff/taskiem/engine/whatsapp/whatsapptest"
)

// Sending outside WhatsApp's 24-hour window (docs/whatsapp.md#templates):
// each message goes as its template once the registry holds it approved
// in the number's account, and falls back as before until then.

const platformWABA = "300000000000100"

// gatePlatform tracks the platform number's account in the fake, holding
// nothing approved, so every template is refused until a test approves it.
func gatePlatform(t *testing.T, g *whatsapptest.Graph, wa *whatsapp.Platform) {
	t.Helper()
	wa.WABA = platformWABA
	g.StrictTemplates(platformWABA)
	if _, err := wa.CheckTemplates(context.Background()); err != nil {
		t.Fatal(err)
	}
}

// approve makes Meta hold a template approved and records it.
func approve(t *testing.T, g *whatsapptest.Graph, wa *whatsapp.Platform, name string) {
	t.Helper()
	g.SetTemplateStatus(platformWABA, name, "APPROVED", "")
	if _, err := wa.CheckTemplates(context.Background()); err != nil {
		t.Fatal(err)
	}
}

// closeWindow makes a number's last message older than 24 hours.
func closeWindow(t *testing.T, w *world, number string) {
	t.Helper()
	if _, err := w.env.DB.Admin.Exec(context.Background(), `UPDATE whatsapp_contacts SET last_inbound_at = now() - interval '25 hours' WHERE number = $1`, number); err != nil {
		t.Fatal(err)
	}
}

// linkChidi makes Chidi ask the business for updates on WhatsApp.
func linkChidi(t *testing.T, w *reachWorld, owner *client, number string) {
	t.Helper()
	pageURL, _ := owner.must(200, "GET", "/v1/reach", nil)["page_url"].(string)
	path := strings.TrimPrefix(pageURL, "https://"+reachFormHost)
	loc, _ := url.Parse(w.page(t, path+"/whatsapp", nil).Header().Get("Location"))
	if sent := w.whatsapp(t, number, loc.Query().Get("text")); len(sent) != 1 || !strings.Contains(sent[0].Text, "updates from") {
		t.Fatalf("Chidi links: %+v", sent)
	}
}

// quickReply delivers a tapped template quick-reply button.
func quickReply(t *testing.T, w *reachWorld, from, payload string) []whatsapptest.Sent {
	t.Helper()
	before := len(w.graph.Sent(from))
	body := whatsapptest.Delivery(waPNID, from, fmt.Sprintf("wamid.qr%d", w.seq.Add(1)), whatsapptest.QuickReply(payload, "x"))
	w.post(t, w.wa, body, "X-Hub-Signature-256", whatsapp.Sign(waSecret, body))
	return w.graph.Sent(from)[before:]
}

func droppedErrors(t *testing.T, c *client) string {
	t.Helper()
	return toJSON(c.must(200, "GET", "/v1/whatsapp/outbox?status=dropped", nil))
}

func TestInvoiceTemplateOutsideWindow(t *testing.T) {
	w := newReachWorld(t)
	ctx := context.Background()
	inv := &invoicing.Invoices{Pool: w.env.DB.App, Vault: w.env.Vault, PageBase: "https://" + reachFormHost, Messages: w.srv.Messages,
		Signal: w.env.Store.DeliverSignalOnce, Rails: &chatRails{}, Reach: w.srv.Reach, Chat: []string{"whatsapp", "telegram"}}
	inv.Answered = w.srv.InvoiceAnswered
	w.srv.Invoices = inv
	owner := w.tenant(t, "Ada Stores", "ada@ada.test")
	const chidi = "+2348039998888"
	linkChidi(t, w, owner, chidi)
	closeWindow(t, w.world, chidi)
	gatePlatform(t, w.graph, w.srv.WhatsApp)

	send := func(key string) string {
		x := owner.must(201, "POST", "/v1/invoices", map[string]any{"customer": map[string]any{"name": "Chidi Eze", "phone": "0803 999 8888"}, "amount_kobo": 2500000})
		sent := owner.must(200, "POST", "/v1/invoices/"+x["id"].(string)+"/send", nil, "Idempotency-Key", key)
		if q := toJSON(sent["queued"]); q != `["sms","whatsapp"]` {
			t.Fatalf("queued %s", q)
		}
		return sent["link"].(string)
	}
	// Not approved: the invoice went by SMS and email only, and the chat
	// message says why.
	before := len(w.graph.Sent(chidi))
	send("send-1")
	w.tick(t)
	if len(w.graph.Sent(chidi)) != before {
		t.Fatalf("sent without an approved template: %+v", w.graph.Sent(chidi)[before:])
	}
	if d := droppedErrors(t, owner); !strings.Contains(d, "not approved") || !strings.Contains(d, "SMS or email only") {
		t.Fatalf("dropped: %s", d)
	}

	// Approved: the template, with Pay by transfer carrying the signed
	// button and Open invoice the page's token.
	approve(t, w.graph, w.srv.WhatsApp, "taskiem_invoice")
	link := send("send-2")
	w.tick(t)
	msg := w.graph.Last(t, chidi)
	if msg.Type != "template" || msg.Template != "taskiem_invoice" || !strings.Contains(msg.Text, "[Ada Stores] Invoice INV-0002: ₦25,000 to pay") ||
		len(msg.Payloads) != 1 || !strings.HasPrefix(msg.Payloads[0], "iv1.") || len(msg.URLParams) != 1 || "https://"+reachFormHost+"/i/"+msg.URLParams[0] != link {
		t.Fatalf("the invoice template: %+v (link %s)", msg, link)
	}
	// Tapping Pay by transfer on the template works as the button did.
	if got := quickReply(t, w, chidi, msg.Payloads[0]); len(got) != 0 {
		t.Fatalf("Pay by transfer answered at once: %+v", got)
	}
	if err := inv.Drain(ctx); err != nil {
		t.Fatal(err)
	}
	if ans := w.graph.Last(t, chidi); !strings.Contains(ans.Text, "Account number: 9900112233") {
		t.Fatalf("the account: %+v", ans)
	}
}

func TestMatchTemplateOutsideWindow(t *testing.T) {
	ctx := context.Background()
	w := newWAWorld(t)
	log := &moneylog.Log{Pool: w.env.DB.App, PII: w.env.Vault}
	w.srv.Invoices = &invoicing.Invoices{Pool: w.env.DB.App, Vault: w.env.Vault, PageBase: "https://forms.taskiem.test", MoneyLog: log,
		Messages: &sender.Queue{Vault: w.env.Vault, HashKey: bytes.Repeat([]byte{8}, 32)}, Signal: w.env.Store.DeliverSignalOnce}
	owner := w.tenant(t, "Ada Foods", "ada@adafoods.test")
	const on = "+2348010000061"
	w.bind(t, owner, on)
	w.say(t, on, "help")
	closeWindow(t, w.world, on)
	gatePlatform(t, w.graph, w.wa)
	tenant := tenantIDNamed(t, w.world, "Ada Foods")
	m := &matching.Matcher{Invoices: w.srv.Invoices, PII: w.env.Vault, Suggested: w.srv.MatchSuggested}
	invoice := func(name string, kobo int) map[string]any {
		x := owner.must(201, "POST", "/v1/invoices", map[string]any{"customer": map[string]any{"name": name, "phone": "+2348031234567"}, "amount_kobo": kobo})
		owner.must(200, "POST", "/v1/invoices/"+x["id"].(string)+"/send", nil, "Idempotency-Key", "send-"+name)
		return x
	}
	heard := func(fields map[string]any) {
		t.Helper()
		if _, err := log.RecordEvent(ctx, tenant, "prod", "paystack", "main", "virtual_account.credited", fields); err != nil {
			t.Fatal(err)
		}
		if err := m.Tick(ctx); err != nil {
			t.Fatal(err)
		}
		w.tick(t)
	}
	first := invoice("Bola Ade", 900000)
	before := len(w.graph.Sent(on))
	heard(map[string]any{"reference": "VA-1", "amount_kobo": int64(900000), "payer_name": "BOLA ADE"})
	if len(w.graph.Sent(on)) != before {
		t.Fatalf("asked without an approved template: %+v", w.graph.Sent(on)[before:])
	}
	if d := droppedErrors(t, owner); !strings.Contains(d, "not approved") || !strings.Contains(d, "waits in the web app") {
		t.Fatalf("dropped: %s", d)
	}

	approve(t, w.graph, w.wa, "taskiem_match_question")
	second := invoice("Chidi Eze", 700000)
	heard(map[string]any{"reference": "VA-2", "amount_kobo": int64(700000), "payer_name": "CHIDI EZE"})
	q := w.graph.Last(t, on)
	if q.Type != "template" || q.Template != "taskiem_match_question" || !strings.HasPrefix(q.Text, "[Ada Foods] ₦7,000 came in on ") ||
		!strings.Contains(q.Text, "through Paystack (main). Is this for "+second["number"].(string)+", ₦7,000 due? ") ||
		!strings.Contains(q.Text, "Tap Yes, match or No") || len(q.Payloads) != 2 || !strings.HasPrefix(q.Payloads[0], "mt1.") {
		t.Fatalf("the question template: %+v", q)
	}
	if strings.Contains(strings.ToLower(q.Text), "chidi") {
		t.Errorf("the question names someone: %q", q.Text)
	}
	// Yes, match on the template matches it.
	got := w.send(t, on, whatsapptest.QuickReply(q.Payloads[0], "Yes, match"))
	if len(got) != 1 || !strings.Contains(got[0].Text, "Matched to "+second["number"].(string)) {
		t.Fatalf("yes: %+v", got)
	}
	if st := owner.must(200, "GET", "/v1/invoices/"+second["number"].(string), nil)["invoice"].(map[string]any)["status"]; st != "paid" {
		t.Errorf("status %v", st)
	}
	_ = first
}

func TestFamilyTemplateOutsideWindow(t *testing.T) {
	w := newReachWorld(t)
	inv := &invoicing.Invoices{Pool: w.env.DB.App, Vault: w.env.Vault, PageBase: "https://" + reachFormHost, Messages: w.srv.Messages,
		Signal: w.env.Store.DeliverSignalOnce, Reach: w.srv.Reach, Chat: []string{"whatsapp", "telegram"}}
	w.srv.Invoices = inv
	rec := &schools.Schools{Pool: w.env.DB.App, Vault: w.env.Vault, Invoices: inv}
	schools.WireReceipts(inv, rec)
	owner := w.tenant(t, "Greenfield", "proprietor@greenfield.test")
	owner.must(200, "PUT", "/v1/settings/schools", map[string]any{"enabled": true})
	const chidi = "+2348039998888"
	linkChidi(t, w, owner, chidi)
	closeWindow(t, w.world, chidi)
	gatePlatform(t, w.graph, w.srv.WhatsApp)

	owner.must(201, "POST", "/v1/schools/classes", map[string]any{"name": "JSS 1", "arm": "A", "order": 7})
	csv := "Name,Admission number,Class,Guardian name,Phone\nEbube Eze,GCA/001,JSS 1 A,Chidi Eze,08039998888\n"
	plan := owner.must(200, "POST", "/v1/schools/students/import", map[string]any{"csv": csv})
	owner.must(200, "POST", "/v1/schools/students/import", map[string]any{"csv": csv, "confirm": plan["hash"]})
	student := owner.must(200, "GET", "/v1/schools/students/GCA%2F001", nil)["student"].(map[string]any)
	family := student["family"].(map[string]any)["id"].(string)

	// Not approved: the guardian's chat message falls back to SMS.
	before := len(w.graph.Sent(chidi))
	sent := owner.must(200, "POST", "/v1/schools/families/"+family+"/link", map[string]any{"message": "Your fees page:"})
	if sent["channels"].(map[string]any)["whatsapp"] != float64(1) {
		t.Fatalf("sent %v", sent)
	}
	w.tick(t)
	if len(w.graph.Sent(chidi)) != before {
		t.Fatalf("sent without an approved template: %+v", w.graph.Sent(chidi)[before:])
	}
	if d := droppedErrors(t, owner); !strings.Contains(d, "not approved") || !strings.Contains(d, "sent by SMS instead") {
		t.Fatalf("dropped: %s", d)
	}

	// Approved: the link template, the staff's words and the link's token.
	approve(t, w.graph, w.srv.WhatsApp, "taskiem_family_link")
	sent = owner.must(200, "POST", "/v1/schools/families/"+family+"/link", map[string]any{"message": "Your fees page:"})
	w.tick(t)
	msg := w.graph.Last(t, chidi)
	link, _ := sent["link"].(string)
	if msg.Type != "template" || msg.Template != "taskiem_family_link" || !strings.Contains(msg.Text, "[Greenfield] Your fees page: Tap Open fees page") ||
		!strings.Contains(msg.Text, "The link works for 3 days.") || len(msg.URLParams) != 1 || "https://"+reachFormHost+"/s/"+msg.URLParams[0] != link {
		t.Fatalf("the link template: %+v (link %s)", msg, link)
	}
}
