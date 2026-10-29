package api_test

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/api"
	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/lang"
	"github.com/israel-duff/taskiem/engine/reach"
	"github.com/israel-duff/taskiem/engine/sender"
	"github.com/israel-duff/taskiem/engine/stock"
	"github.com/israel-duff/taskiem/engine/whatsapp/whatsapptest"
)

// withPurchases keeps suppliers and purchase orders in a world with stock.
func withPurchases(w *world) *stock.Purchases {
	hash := bytes.Repeat([]byte{8}, 32)
	if w.srv.Reach == nil {
		w.srv.Reach = &reach.Book{Pool: w.env.Store.Pool, Vault: w.env.Vault, HashKey: hash}
	}
	if w.srv.Messages == nil {
		w.srv.Messages = &sender.Queue{Vault: w.env.Vault, HashKey: hash}
	}
	w.srv.Purchases = &stock.Purchases{Pool: w.env.DB.App, Vault: w.env.Vault, Book: w.srv.Reach, Chat: []string{"whatsapp"}, Messages: w.srv.Messages,
		PageBase: "https://forms.test", Stock: w.srv.Stock, Cost: w.srv.StockCost}
	return w.srv.Purchases
}

func (w *waWorld) press(t *testing.T, from, id string) []whatsapptest.Sent {
	t.Helper()
	return w.send(t, from, whatsapptest.ButtonReply(id, "x"))
}

func smsCount(t *testing.T, w *world, tenant uuid.UUID) int {
	t.Helper()
	var n int
	if err := w.env.DB.Admin.QueryRow(context.Background(), `SELECT count(*) FROM platform_messages WHERE tenant_id = $1 AND channel = 'sms'`, tenant).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// Suppliers and purchase orders (docs/spec/stock.md K3): a supplier added
// by chat with the items they supply; "order from" drafts what is low and
// sends it to the supplier's WhatsApp with Confirm and Change, which work
// for them only; their change and their confirm reach the owner; goods
// received by chat and the web raise the stock and set the cost; paid, it
// is an expense and what is owed falls; above the limit a second person
// sends an order.
func TestPurchaseOrdersByChat(t *testing.T) {
	ctx := context.Background()
	w := shopWorld(t)
	withStock(w.world)
	withPurchases(w.world)
	owner := w.tenant(t, "Mama Put Stores", "ada@mama.test")
	bisi := w.member(t, owner, "bisi@mama.test", "admin")
	viewer := addMember(t, w.world, owner, "vic", "viewer")
	const on, supplier = "+2348010000091", "+2348031234567"
	w.bind(t, owner, on)
	tenant := uuid.MustParse(tenantOf(t, owner))
	owner.must(200, "POST", "/v1/stock/setup", nil)
	rice := owner.must(201, "POST", "/v1/stock/items", map[string]any{"name": "Rice", "quantity": 3, "unit": "bag", "cost_kobo": 4000000, "reorder_level": 5,
		"target_level": 20}, "Idempotency-Key", "r")["item"].(map[string]any)["id"].(string)
	owner.must(201, "POST", "/v1/stock/items", map[string]any{"name": "Indomie", "quantity": 4, "unit": "carton", "cost_kobo": 850000, "reorder_level": 10,
		"target_level": 50}, "Idempotency-Key", "i")

	// The supplier, by chat; the items they supply carry their name.
	if r := w.say(t, on, "add supplier Dangote Depot 0803 123 4567 for rice, indomie"); !strings.Contains(r, "Add *Dangote Depot* as a supplier for Rice, Indomie?") {
		t.Fatalf("add: %q", r)
	}
	if r := w.yes(t, on); !strings.Contains(r, "Added Dangote Depot. Send *order from Dangote Depot*") {
		t.Fatalf("added: %q", r)
	}
	if n := smsCount(t, w.world, tenant); n != 1 {
		t.Fatalf("tap-in link: %d SMS", n)
	}
	if items := itemsByName(t, owner.must(200, "GET", "/v1/stock", nil)); items["Rice"]["supplier"] != "Dangote Depot" || items["Indomie"]["supplier"] != "Dangote Depot" {
		t.Fatalf("items: %v", items)
	}
	viewer.must(403, "GET", "/v1/stock/suppliers", nil)
	sups := owner.must(200, "GET", "/v1/stock/suppliers", nil)["suppliers"].([]any)
	if len(sups) != 1 || sups[0].(map[string]any)["reached"] != "sms" || sups[0].(map[string]any)["phone"] != supplier {
		t.Fatalf("suppliers: %v", sups)
	}
	// They tap in on WhatsApp (the message their link fills in opens the
	// conversation window).
	w.send(t, supplier, whatsapptest.Text("hello"))
	if err := db.InTenantTx(ctx, w.env.DB.App, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		_, err := w.srv.Reach.LinkTx(ctx, tx, tenant, supplier, "whatsapp", supplier)
		return err
	}); err != nil {
		t.Fatal(err)
	}

	// Order from them: what is low, drafted, sent on yes.
	r := w.say(t, on, "order from dangote")
	if !strings.Contains(r, "from *Dangote Depot*: 46 cartons Indomie, 17 bags Rice, ₦1,071,000. Send it to them?") {
		t.Fatalf("order: %q", r)
	}
	before := len(w.graph.Sent(supplier))
	if r := w.yes(t, on); !strings.Contains(r, "Sent P-") || !strings.Contains(r, "to Dangote Depot") {
		t.Fatalf("sent: %q", r)
	}
	got := w.lastTo(t, supplier, before)
	if len(got) != 1 || !strings.Contains(got[0].Text, "[Mama Put Stores] Order P-") || !strings.Contains(got[0].Text, "46 cartons Indomie, 17 bags Rice. Total ₦1,071,000") ||
		len(got[0].Buttons) != 2 || got[0].Buttons[0].Title != "Confirm" || got[0].Buttons[1].Title != "Change" {
		t.Fatalf("to the supplier: %+v", got)
	}
	confirm, change := got[0].Buttons[0].ID, got[0].Buttons[1].ID
	po := owner.must(200, "GET", "/v1/stock/purchases?open=true", nil)["purchases"].([]any)[0].(map[string]any)
	ref, id := po["reference"].(string), po["id"].(string)
	if po["status"] != "sent" || po["approved_by"] == nil {
		t.Fatalf("order: %v", po)
	}

	// Their buttons work for them only.
	if got := w.press(t, on, confirm); len(got) != 1 || !strings.Contains(got[0].Text, "can no longer be answered") {
		t.Fatalf("the owner pressing: %+v", got)
	}
	if got := w.press(t, supplier, change); len(got) != 1 || !strings.Contains(got[0].Text, "Send *change "+ref+"*") {
		t.Fatalf("change: %+v", got)
	}
	if r := w.say(t, supplier, "change "+ref+" only 10 bags of rice this week"); !strings.Contains(r, "Sent to Mama Put Stores: they will answer you about "+ref) {
		t.Fatalf("changed: %q", r)
	}
	if r := w.say(t, supplier, "CONFIRM"); !strings.Contains(r, "Mama Put Stores is told you confirmed "+ref) {
		t.Fatalf("confirmed: %q", r)
	}
	var alerts []string
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT array_agg(kind ORDER BY created_at) FROM alerts WHERE tenant_id = $1 AND kind LIKE 'stock.%'`, tenant).Scan(&alerts); err != nil ||
		len(alerts) != 2 || alerts[0] != stock.AlertSupplier || alerts[1] != stock.AlertConfirmed {
		t.Fatalf("the owner is told: %v %v", alerts, err)
	}
	po = owner.must(200, "GET", "/v1/stock/purchases", nil)["purchases"].([]any)[0].(map[string]any)
	if po["status"] != "confirmed" || po["confirmed_how"] != "supplier" || po["counter"] != "only 10 bags of rice this week" {
		t.Fatalf("after the supplier: %v", po)
	}

	// Goods received by chat, against the order (in Pidgin, after its
	// word); the rest from the web.
	w.srv.Languages = &api.LanguageSettings{Enabled: []lang.Tag{lang.EN, lang.PCM}}
	const bn = "+2348010000092"
	w.bind(t, bisi, bn)
	w.say(t, bn, "language pidgin")
	pcm := func(id string, kv ...string) string { return lang.Default().Text(lang.PCM, id, kv...) }
	if r := w.say(t, bn, "I don receive 10 bags of rice"); !strings.Contains(r, pcm("wa.received.confirm", "count", "10 bags", "item", "Rice", "reference", ref, "supplier", "Dangote Depot")) {
		t.Fatalf("received: %q", r)
	}
	if r := w.say(t, bn, "yes"); !strings.Contains(r, pcm("wa.received.done", "reference", ref, "status", pcm("purchase.status_part_received"))) {
		t.Fatalf("received: %q", r)
	}
	if r := w.say(t, on, "received 2 cartons of indomie"); !strings.Contains(r, "Received 2 cartons Indomie on order "+ref+" from Dangote Depot?") {
		t.Fatalf("received in English: %q", r)
	}
	w.say(t, on, "cancel")
	owner.must(400, "POST", "/v1/stock/purchases/"+id+"/receive", map[string]any{"lines": []any{map[string]any{"item_id": "nope", "quantity": 1}}}, "Idempotency-Key", "rcv-1")
	indomie := ""
	for _, l := range owner.must(200, "GET", "/v1/stock/purchases", nil)["purchases"].([]any)[0].(map[string]any)["lines"].([]any) {
		if l.(map[string]any)["item"] == "Indomie" {
			indomie = l.(map[string]any)["item_id"].(string)
		}
	}
	po = owner.must(200, "POST", "/v1/stock/purchases/"+id+"/receive", map[string]any{"lines": []any{map[string]any{"item_id": indomie, "quantity": 46},
		map[string]any{"item_id": rice, "quantity": 7}}}, "Idempotency-Key", "rcv-2")["purchase"].(map[string]any)
	if po["status"] != "received" || po["received_kobo"] != float64(107100000) {
		t.Fatalf("all received: %v", po)
	}
	if items := itemsByName(t, owner.must(200, "GET", "/v1/stock", nil)); items["Rice"]["stock"] != float64(20) || items["Indomie"]["stock"] != float64(50) ||
		items["Rice"]["cost_kobo"] != float64(4000000) {
		t.Fatalf("stock: %v", items)
	}

	// Paid by chat: an expense; what is owed falls.
	if r := w.say(t, on, "paid dangote 500,000 transfer"); !strings.Contains(r, "Paid *Dangote Depot* ₦500,000 by transfer, for "+ref+"?") {
		t.Fatalf("paid: %q", r)
	}
	if r := w.yes(t, on); !strings.Contains(r, "Recorded ₦500,000 paid to Dangote Depot for "+ref+". Still owed on it: ₦571,000.") {
		t.Fatalf("paid: %q", r)
	}
	if r := w.say(t, on, "suppliers"); !strings.Contains(r, "• Dangote Depot: 0 open orders, owed ₦571,000") {
		t.Fatalf("suppliers: %q", r)
	}
	owner.must(200, "POST", "/v1/stock/purchases/"+id+"/pay", map[string]any{"amount_kobo": 57100000, "paid_how": "cash"}, "Idempotency-Key", "pay-2")
	if out := owner.must(200, "GET", "/v1/stock/suppliers", nil)["suppliers"].([]any)[0].(map[string]any); out["owed_kobo"] != float64(0) {
		t.Fatalf("paid up: %v", out)
	}

	// Above the limit, the one who drafts it does not send it.
	owner.must(200, "PUT", "/v1/stock/settings", map[string]any{"purchase_limit_kobo": 100000}, "Idempotency-Key", "set")
	sid := sups[0].(map[string]any)["id"].(string)
	draft := owner.must(201, "POST", "/v1/stock/purchases", map[string]any{"supplier_id": sid, "lines": []any{map[string]any{"item_id": rice, "quantity": 2}}},
		"Idempotency-Key", "d-1")["purchase"].(map[string]any)
	if draft["total_kobo"] != float64(8000000) || draft["status"] != "drafted" {
		t.Fatalf("draft: %v", draft)
	}
	owner.must(409, "POST", "/v1/stock/purchases/"+draft["id"].(string)+"/send", nil)
	viewer.must(403, "POST", "/v1/stock/purchases/"+draft["id"].(string)+"/send", nil)
	if sent := bisi.must(200, "POST", "/v1/stock/purchases/"+draft["id"].(string)+"/send", nil)["purchase"].(map[string]any); sent["status"] != "sent" {
		t.Fatalf("second approval: %v", sent)
	}
	if out := owner.must(200, "POST", "/v1/stock/purchases/"+draft["id"].(string)+"/cancel", nil)["purchase"].(map[string]any); out["status"] != "cancelled" {
		t.Fatalf("cancel: %v", out)
	}
	// The draft from the low list: nothing is low now.
	if out := owner.must(200, "POST", "/v1/stock/purchases/low", nil, "Idempotency-Key", "low-1"); len(out["purchases"].([]any)) != 0 {
		t.Fatalf("low: %v", out)
	}
	if r := w.say(t, on, "help"); !strings.Contains(r, "*order from Dangote*") {
		t.Errorf("help: %q", r)
	}
}
