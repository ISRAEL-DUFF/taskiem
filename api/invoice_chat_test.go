package api_test

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/connector"
	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/effects"
	"github.com/israel-duff/taskiem/engine/invoicing"
	"github.com/israel-duff/taskiem/engine/telegram/telegramtest"
	"github.com/israel-duff/taskiem/engine/whatsapp"
	"github.com/israel-duff/taskiem/engine/whatsapp/whatsapptest"
)

// chatRails is a payments rail that opens one-time accounts.
type chatRails struct {
	mu    sync.Mutex
	calls int
}

func (f *chatRails) Resolve(context.Context, uuid.UUID, string) (invoicing.Rail, error) {
	return invoicing.Rail{Connector: "paystack", Connection: "main", Name: "Paystack",
		Transfer: &connector.TransferFacts{OnLate: "returned", OnWrongAmount: "returned", MinMinutes: 15, MaxMinutes: 480}}, nil
}

func (f *chatRails) Key(_ invoicing.Rail, capability string, k effects.KeyInput) (string, error) {
	return "tsk_" + capability + "_" + k.Seed + "_" + k.StepID, nil
}

func (f *chatRails) Call(_ context.Context, _ uuid.UUID, r invoicing.Rail, capability string, in map[string]any, k effects.KeyInput) (map[string]any, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if capability != "collect_transfer" {
		return nil, errors.New("no such capability")
	}
	f.calls++
	pid, _ := f.Key(r, capability, k)
	return map[string]any{"payment_id": pid, "account_number": "9900112233", "bank_name": "Wema Bank", "account_name": "Shop / Ada",
		"amount_kobo": in["amount_kobo"], "expires_at": time.Now().Add(30 * time.Minute).UTC().Format(time.RFC3339)}, nil
}

// press delivers a tapped WhatsApp reply button and returns what went back.
func (w *reachWorld) press(t *testing.T, from, id string) []whatsapptest.Sent {
	t.Helper()
	before := len(w.graph.Sent(from))
	body := whatsapptest.Delivery(waPNID, from, fmt.Sprintf("wamid.press%d", w.seq.Add(1)), whatsapptest.ButtonReply(id, "x"))
	w.post(t, w.wa, body, "X-Hub-Signature-256", whatsapp.Sign(waSecret, body))
	return w.graph.Sent(from)[before:]
}

// TestInvoicesInChat (decision 0045 point 7): a customer who asked a
// business for updates on WhatsApp or Telegram gets its invoices there,
// and its buttons answer with an account number to pay into; nobody else
// can use them, and they do nothing more.
func TestInvoicesInChat(t *testing.T) {
	w := newReachWorld(t)
	ctx := context.Background()
	inv := &invoicing.Invoices{Pool: w.env.DB.App, Vault: w.env.Vault, PageBase: "https://" + reachFormHost, Messages: w.srv.Messages,
		Signal: w.env.Store.DeliverSignalOnce, Rails: &chatRails{}, Reach: w.srv.Reach, Chat: []string{"whatsapp", "telegram"}}
	inv.Answered = w.srv.InvoiceAnswered
	w.srv.Invoices = inv
	owner := w.tenant(t, "Ada Stores", "ada@ada.test")

	// Chidi asks for Ada's updates on WhatsApp, Bola on Telegram.
	pageURL, _ := owner.must(200, "GET", "/v1/reach", nil)["page_url"].(string)
	path := strings.TrimPrefix(pageURL, "https://"+reachFormHost)
	loc, _ := url.Parse(w.page(t, path+"/whatsapp", nil).Header().Get("Location"))
	const chidi = "+2348039998888"
	if sent := w.whatsapp(t, chidi, loc.Query().Get("text")); len(sent) != 1 || !strings.Contains(sent[0].Text, "updates from Ada Stores") {
		t.Fatalf("Chidi links: %+v", sent)
	}
	loc, _ = url.Parse(w.page(t, path+"/telegram", nil).Header().Get("Location"))
	const bola = int64(7002)
	w.telegram(t, bola, func(id int64) []byte { return telegramtest.Start(id, bola, "", loc.Query().Get("start")) })
	if got := w.telegram(t, bola, func(id int64) []byte { return telegramtest.ContactMessage(id, bola, "+2348031234567", bola) }); len(got) != 1 ||
		!strings.Contains(got[0].Text, "updates from Ada Stores") {
		t.Fatalf("Bola links: %+v", got)
	}

	// Ada sends Chidi an invoice: by SMS as before, and on WhatsApp with
	// its buttons.
	x := owner.must(201, "POST", "/v1/invoices", map[string]any{"customer": map[string]any{"name": "Chidi Eze", "phone": "0803 999 8888"}, "amount_kobo": 2500000})
	sent := owner.must(200, "POST", "/v1/invoices/"+x["id"].(string)+"/send", nil, "Idempotency-Key", "send-chidi")
	if q := toJSON(sent["queued"]); q != `["sms","whatsapp"]` {
		t.Fatalf("queued %s", q)
	}
	link := sent["link"].(string)
	w.tick(t)
	msg := w.graph.Last(t, chidi)
	if msg.Type != "interactive" || !strings.Contains(msg.Text, "Invoice INV-0001 from Ada Stores: ₦25,000 to pay") || len(msg.Buttons) != 2 ||
		msg.Buttons[0].Title != "Pay by transfer" || msg.Buttons[1].Title != "Open invoice" || strings.Contains(msg.Buttons[0].ID, chidi) {
		t.Fatalf("the invoice on WhatsApp: %+v", msg)
	}
	pay, open := msg.Buttons[0].ID, msg.Buttons[1].ID

	// Open invoice answers with its page.
	if got := w.press(t, chidi, open); len(got) != 1 || !strings.Contains(got[0].Text, link) {
		t.Fatalf("Open invoice: %+v", got)
	}
	// Pay by transfer leaves a request; the queue issues the account and
	// the answer comes in the chat.
	if got := w.press(t, chidi, pay); len(got) != 0 {
		t.Fatalf("Pay by transfer answered at once: %+v", got)
	}
	if err := inv.Drain(ctx); err != nil {
		t.Fatal(err)
	}
	msg = w.graph.Last(t, chidi)
	if !strings.Contains(msg.Text, "Account number: 9900112233") || !strings.Contains(msg.Text, "Bank: Wema Bank") || !strings.Contains(msg.Text, "Valid until ") ||
		!strings.Contains(msg.Text, "₦25,000") || len(msg.Buttons) != 2 || msg.Buttons[0].Title != "New account number" {
		t.Fatalf("the account: %+v", msg)
	}
	// New account number asks again: the open one is given again.
	w.press(t, chidi, msg.Buttons[0].ID)
	if err := inv.Drain(ctx); err != nil {
		t.Fatal(err)
	}
	if again := w.graph.Last(t, chidi); !strings.Contains(again.Text, "9900112233") {
		t.Fatalf("New account number: %+v", again)
	}

	// Someone else pressing Chidi's button, or an altered one, gets nothing
	// from the invoice, and no request is left.
	const eve = "+2348030000001"
	if got := w.press(t, eve, pay); len(got) != 1 || !strings.Contains(got[0].Text, "no longer works") {
		t.Fatalf("another number: %+v", got)
	}
	// Altered in the signature's first character, which always counts (the
	// last one's low bits are base64 padding, which decoding ignores).
	mac := strings.Index(pay, ".") + 1
	altered := pay[:mac] + map[bool]string{true: "B", false: "A"}[pay[mac] == 'A'] + pay[mac+1:]
	if got := w.press(t, chidi, altered); len(got) != 1 || !strings.Contains(got[0].Text, "no longer works") {
		t.Fatalf("altered: %+v", got)
	}
	var waiting int
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT count(*) FROM customer_invoice_requests`).Scan(&waiting); err != nil || waiting != 0 {
		t.Fatalf("requests left: %d %v", waiting, err)
	}
	// Anything else from Chidi is still only STOP, START and who sends.
	if got := w.whatsapp(t, chidi, "pay"); len(got) != 1 || !strings.Contains(got[0].Text, "updates from Ada Stores. Send STOP") {
		t.Fatalf("anything else: %+v", got)
	}

	// Paid in full: the button says so politely.
	tenant := tenantIDNamed(t, w.world, "Ada Stores")
	if err := db.InTenantTx(ctx, w.env.DB.App, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		_, _, err := inv.RecordPayment(ctx, tx, tenant, "INV-0001", invoicing.PaymentIn{AmountKobo: 2500000, PaymentID: "tsk_x", How: "manual", By: "test"})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if got := w.press(t, chidi, pay); len(got) != 1 || got[0].Text != "[Ada Stores] Invoice INV-0001 is paid. Thank you!" {
		t.Fatalf("paid: %+v", got)
	}

	// Bola gets hers on Telegram; the same buttons work there.
	y := owner.must(201, "POST", "/v1/invoices", map[string]any{"customer": map[string]any{"name": "Bola Ade", "phone": "08031234567"}, "amount_kobo": 1000000})
	if q := toJSON(owner.must(200, "POST", "/v1/invoices/"+y["id"].(string)+"/send", nil)["queued"]); q != `["sms","telegram"]` {
		t.Fatalf("queued %s", q)
	}
	w.tick(t)
	call := w.bot.Last(t, bola)
	if !strings.Contains(call.Text, "Invoice INV-0002 from Ada Stores: ₦10,000") || len(call.Keyboard) != 2 || call.Keyboard[0][0].Text != "Pay by transfer" {
		t.Fatalf("the invoice on Telegram: %+v", call)
	}
	data := call.Keyboard[0][0].Data
	w.telegram(t, bola, func(id int64) []byte { return telegramtest.Callback(id, bola, call.MessageID, "q1", data) })
	if err := inv.Drain(ctx); err != nil {
		t.Fatal(err)
	}
	if last := w.bot.Last(t, bola); !strings.Contains(last.Text, "9900112233") || !strings.Contains(last.Text, "₦10,000") {
		t.Fatalf("the account on Telegram: %+v", last)
	}
	// Void: the button says it can no longer be paid.
	owner.must(200, "POST", "/v1/invoices/"+y["id"].(string)+"/void", map[string]any{"reason": "Wrong price"})
	got := w.telegram(t, bola, func(id int64) []byte { return telegramtest.Callback(id, bola, call.MessageID, "q2", data) })
	if len(got) == 0 || !strings.Contains(got[len(got)-1].Text, "INV-0002 can no longer be paid") {
		t.Fatalf("void: %+v", got)
	}

	// Another business's invoice to Chidi goes by SMS only: Chidi asked
	// Ada for updates, not them.
	other := w.tenant(t, "Other", "o@other.test")
	z := other.must(201, "POST", "/v1/invoices", map[string]any{"customer": map[string]any{"name": "Chidi", "phone": chidi}, "amount_kobo": 500000})
	if q := toJSON(other.must(200, "POST", "/v1/invoices/"+z["id"].(string)+"/send", nil)["queued"]); q != `["sms"]` {
		t.Fatalf("another business queued %s", q)
	}
}
