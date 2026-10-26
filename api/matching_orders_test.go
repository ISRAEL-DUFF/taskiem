package api_test

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	sfconn "github.com/israel-duff/taskiem/connectors/storefront"
	"github.com/israel-duff/taskiem/engine/invoicing"
	"github.com/israel-duff/taskiem/engine/matching"
	"github.com/israel-duff/taskiem/engine/moneylog"
	"github.com/israel-duff/taskiem/engine/sender"
	"github.com/israel-duff/taskiem/engine/storefront"
	"github.com/israel-duff/taskiem/engine/whatsapp/whatsapptest"
	"github.com/israel-duff/taskiem/templates"
)

// Storefront orders over the matching API: suggested with the customer
// masked (revealed with pii.reveal), confirmed, listed on the order,
// undone, offered to match by hand, and matched by hand; the payer is
// remembered for the order's customer and forgotten.
func TestMatchingOrdersAPI(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t)
	owner := w.tenant(t, "Ada's Kitchen", "ada@ada.test")
	viewer := addMember(t, w, owner, "vic", "viewer")
	log, tenant, ref, another := orderToMatch(t, w, owner)

	m := &matching.Matcher{Invoices: w.srv.Invoices, Orders: w.srv.Orders, PII: w.env.Vault}
	if _, err := log.RecordEvent(ctx, tenant, "prod", "paystack", "main", "virtual_account.credited",
		map[string]any{"reference": "TRF " + ref, "amount_kobo": int64(300000), "payer_name": "BOLA ADE"}); err != nil {
		t.Fatal(err)
	}
	if err := m.Tick(ctx); err != nil {
		t.Fatal(err)
	}

	list := viewer.must(200, "GET", "/v1/money/suggestions", nil)["suggestions"].([]any)
	if len(list) != 1 {
		t.Fatalf("suggestions %v", list)
	}
	s := list[0].(map[string]any)
	if s["target_kind"] != "order" || s["target"] != "order:"+ref || s["number"] != ref || s["invoice_id"] != nil || s["order_id"] == nil ||
		s["customer"] != "B•••" || s["due_kobo"] != float64(300000) {
		t.Fatalf("suggestion %v", s)
	}
	viewer.must(403, "GET", "/v1/money/suggestions?reveal=true", nil)
	if c := owner.must(200, "GET", "/v1/money/suggestions?reveal=true", nil)["suggestions"].([]any)[0].(map[string]any)["customer"]; c != "Bola Ade" {
		t.Errorf("revealed %v", c)
	}

	paid := owner.must(200, "POST", "/v1/money/suggestions/"+s["id"].(string)+"/confirm", nil)
	pays := paid["payments"].([]any)
	if paid["target"] != "order:"+ref || paid["paid_at"] == nil || paid["customer"] != "B•••" || len(pays) != 1 || pays[0].(map[string]any)["how"] != "suggested" {
		t.Fatalf("confirmed %v", paid)
	}
	if one := owner.must(200, "GET", "/v1/storefront/orders/"+ref, nil); len(one["payments"].([]any)) != 1 {
		t.Errorf("order's payments %v", one["payments"])
	}
	viewer.must(403, "POST", "/v1/money/order-payments/"+pays[0].(map[string]any)["id"].(string)+"/undo", nil)
	undone := owner.must(200, "POST", "/v1/money/order-payments/"+pays[0].(map[string]any)["id"].(string)+"/undo", nil)
	if undone["paid_at"] != nil || len(undone["payments"].([]any)) != 2 {
		t.Fatalf("undone %v", undone)
	}

	// Back to be matched by hand, with the order offered.
	left := viewer.must(200, "GET", "/v1/money/unmatched", nil)
	waiting, orders := left["payments"].([]any), left["orders"].([]any)
	if len(waiting) != 1 || len(orders) != 1 || orders[0].(map[string]any)["number"] != ref || orders[0].(map[string]any)["customer"] != "B•••" {
		t.Fatalf("unmatched %v", left)
	}
	entry := waiting[0].(map[string]any)["entry_id"].(string)
	owner.must(400, "POST", "/v1/money/entries/"+entry+"/match", map[string]any{"invoice": "INV-0001", "order": ref})
	owner.must(404, "POST", "/v1/money/entries/"+entry+"/match", map[string]any{"order": "AK-NOPE00"})
	x := owner.must(200, "POST", "/v1/money/entries/"+entry+"/match", map[string]any{"order": ref})
	if x["target"] != "order:"+ref || x["paid_at"] == nil {
		t.Fatalf("by hand %v", x)
	}
	if l := owner.must(200, "GET", "/v1/money/unmatched", nil); len(l["payments"].([]any)) != 0 || len(l["orders"].([]any)) != 0 {
		t.Errorf("left %v", l)
	}

	// The payer is remembered for the order's customer: their next
	// payment suggests the customer's next order first, and can be
	// forgotten.
	var payer, customer string
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT id::text, customer_id FROM money_payers WHERE tenant_id = $1 AND kind = 'name'`, tenant).Scan(&payer, &customer); err != nil {
		t.Fatalf("remembered: %v", err)
	}
	var subject string
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT subject_id FROM storefront_orders WHERE tenant_id = $1 AND reference = $2`, tenant, ref).Scan(&subject); err != nil || subject != customer {
		t.Errorf("remembered for %q, want the order's customer %q (%v)", customer, subject, err)
	}
	next := another("Bola Ade", "+2348031234567", 500000)
	if _, err := log.RecordEvent(ctx, tenant, "prod", "paystack", "main", "virtual_account.credited",
		map[string]any{"reference": "TRF-AGAIN", "amount_kobo": int64(500000), "payer_name": "ADE BOLA"}); err != nil {
		t.Fatal(err)
	}
	if err := m.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	list = viewer.must(200, "GET", "/v1/money/suggestions", nil)["suggestions"].([]any)
	if len(list) != 1 {
		t.Fatalf("suggestions %v", list)
	}
	s = list[0].(map[string]any)
	reasons := s["reasons"].([]any)
	var remembered bool
	for _, r := range reasons {
		remembered = remembered || r == "this payer paid for this customer before"
	}
	if s["number"] != next || !remembered || s["score"].(float64) < 85 {
		t.Fatalf("the next order %v", s)
	}
	viewer.must(403, "DELETE", "/v1/money/payers/"+payer, nil)
	owner.must(200, "DELETE", "/v1/money/payers/"+payer, nil)
	owner.must(404, "DELETE", "/v1/money/payers/"+payer, nil)
}

// orderToMatch gives the owner's business invoices, a storefront whose
// orders are taken without online payment, and one order of ₦3,000 from
// Bola Ade; it returns the money log, the business, the order, and a way
// to place another order (its reference).
func orderToMatch(t *testing.T, w *world, owner *client) (*moneylog.Log, uuid.UUID, string, func(name, phone string, kobo int64) string) {
	t.Helper()
	ctx := context.Background()
	w.srv.PublicHosts = []string{"shops.taskiem.test"}
	log := &moneylog.Log{Pool: w.env.DB.App, PII: w.env.Vault}
	w.srv.Invoices = &invoicing.Invoices{Pool: w.env.DB.App, Vault: w.env.Vault, PageBase: "https://forms.taskiem.test", MoneyLog: log,
		Messages: &sender.Queue{Vault: w.env.Vault, HashKey: bytes.Repeat([]byte{8}, 32)}, Signal: w.env.Store.DeliverSignalOnce}
	key, _ := storefront.InboxKey(bytes.Repeat([]byte{3}, 32))
	w.srv.Orders = &storefront.Orders{Pool: w.env.DB.App, Vault: w.env.Vault, InboxKey: key, PreviewDomain: "shops.taskiem.test", MoneyLog: log,
		Start: w.env.Store.Start, Signal: w.env.Store.DeliverSignalOnce}
	if err := w.env.Registry.Register(sfconn.New(w.srv.Orders)); err != nil {
		t.Fatal(err)
	}
	verifyPhone(t, w, owner, "phone", "business")
	owner.must(200, "PUT", "/v1/storefront", map[string]any{"slug": "ada-kitchen", "draft": map[string]any{"name": "Ada's Kitchen"}})
	tpl, _ := templates.Default().Get("storefront-order")
	def, err := tpl.Instantiate(map[string]any{}, "Orders")
	if err != nil {
		t.Fatal(err)
	}
	publishFlow(t, owner, string(def))
	owner.must(200, "PUT", "/v1/storefront/actions/order", map[string]any{"enabled": true, "payment": "none"})
	pub := owner.must(200, "POST", "/v1/storefront/publish", nil)
	sfID := uuid.MustParse(pub["id"].(string))
	tenant := tenantIDNamed(t, w, "Ada's Kitchen")

	var n byte = 7
	another := func(name, phone string, kobo int64) string {
		t.Helper()
		ref := storefront.NewReference("ada-kitchen")
		_, hash := storefront.NewStatusKey()
		ct, _ := storefront.SealSubmission(key, sfID, ref, storefront.Submission{Kind: "order", Payment: "none", Channel: "direct", Submitted: time.Now(),
			Customer: storefront.Customer{Name: name, Phone: phone}, TotalKobo: kobo, Fulfilment: "pickup",
			Basket: []storefront.Line{{ID: "1", Name: "Jollof rice", Quantity: 1, PriceKobo: kobo, LineKobo: kobo}}})
		var got *string
		if err := w.env.DB.PublicPool(t, 1).QueryRow(ctx, `SELECT taskiem_public_submit($1, 'order', $2, $3, $4, $5)`,
			sfID, ref, bytes.Repeat([]byte{n}, 32), hash, ct).Scan(&got); err != nil || got == nil {
			t.Fatalf("submit: %v %v", got, err)
		}
		n++
		if err := w.srv.Orders.Drain(ctx); err != nil {
			t.Fatal(err)
		}
		return ref
	}
	return log, tenant, another("Bola Ade", "+2348031234567", 300000), another
}

// In chat, an order suggestion is worded as an order, and Yes, match
// pays it.
func TestWhatsAppMatchingOrder(t *testing.T) {
	ctx := context.Background()
	w := newWAWorld(t)
	owner := w.tenant(t, "Ada's Kitchen", "ada@ada.test")
	const on = "+2348010000051"
	w.bind(t, owner, on)
	w.say(t, on, "help")
	log, tenant, ref, _ := orderToMatch(t, w.world, owner)
	m := &matching.Matcher{Invoices: w.srv.Invoices, Orders: w.srv.Orders, PII: w.env.Vault, Suggested: w.srv.MatchSuggested}
	if _, err := log.RecordEvent(ctx, tenant, "prod", "paystack", "main", "virtual_account.credited",
		map[string]any{"reference": "TRF " + ref, "amount_kobo": int64(300000)}); err != nil {
		t.Fatal(err)
	}
	if err := m.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	w.tick(t)
	var q *whatsapptest.Sent
	for _, s := range w.graph.Sent(on) {
		if strings.Contains(s.Text, "came in on") {
			q = &s
		}
	}
	if q == nil || !strings.Contains(q.Text, "Is this for storefront order "+ref+", ₦3,000 to pay?") || !strings.Contains(q.Text, "the reference mentions "+ref) ||
		strings.Contains(q.Text, "Bola") || len(q.Buttons) != 2 {
		t.Fatalf("question %+v", q)
	}
	got := w.send(t, on, whatsapptest.ButtonReply(q.Buttons[0].ID, "x"))
	if len(got) != 1 || got[0].Text != "[Ada's Kitchen] Matched to order "+ref+". It is now paid." {
		t.Fatalf("pressed %+v", got)
	}
	if o := owner.must(200, "GET", "/v1/storefront/orders/"+ref, nil)["order"].(map[string]any); o["paid_at"] == nil {
		t.Errorf("not paid %v", o)
	}
}
