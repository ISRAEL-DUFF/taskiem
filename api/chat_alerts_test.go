package api_test

import (
	"context"
	"net/url"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/israel-duff/taskiem/engine/matching"
	"github.com/israel-duff/taskiem/engine/moneylog"
)

// Alerts you can act on (docs/spec/chat-tools.md §6): with chat tools on,
// what needs a person now, each kind only for those whose role may see it.
// Low stock with Reorder from the linked supplier's price list; an overdue
// invoice; a new order from a linked business; a payment not matched; an
// approval waiting, with who asked. Later puts a card off for its person
// only, and a card is gone once it is dealt with.
func TestChatAlerts(t *testing.T) {
	ctx := context.Background()
	w := shopWorld(t)
	withStock(w.world)
	withPurchases(w.world)
	withLinkedOrders(w.world)
	log := &moneylog.Log{Pool: w.env.DB.App, PII: w.env.Vault}
	w.srv.Invoices.MoneyLog = log
	seller, buyer, _, buyerID := linkedPair(t, w.waWorld)
	alerts := func(c *client) map[string]map[string]any {
		t.Helper()
		out := map[string]map[string]any{}
		for _, x := range c.must(200, "GET", "/v1/chat-alerts", nil)["alerts"].([]any) {
			a := x.(map[string]any)
			out[a["kind"].(string)+":"+a["title"].(string)] = a
		}
		return out
	}
	find := func(got map[string]map[string]any, kind, title string) map[string]any {
		t.Helper()
		for k, a := range got {
			if strings.HasPrefix(k, kind+":") && strings.Contains(a["title"].(string), title) {
				return a
			}
		}
		return nil
	}

	// The buyer's rice is low; the seller sells it on a shared list, and
	// the buyer has said once which of theirs it is.
	buyer.must(200, "POST", "/v1/stock/setup", nil)
	rice := buyer.must(201, "POST", "/v1/stock/items", map[string]any{"name": "Rice 50kg", "quantity": 4, "unit": "bag", "reorder_level": 10, "target_level": 24},
		"Idempotency-Key", "r")["item"].(map[string]any)["id"].(string)
	seller.must(200, "POST", "/v1/stock/setup", nil)
	sRice := seller.must(201, "POST", "/v1/stock/items", map[string]any{"name": "Rice, 50kg bag", "quantity": 40, "unit": "bag", "price_kobo": 7800000},
		"Idempotency-Key", "sr")["item"].(map[string]any)["id"].(string)
	sellerLink := seller.must(200, "GET", "/v1/links", nil)["links"].([]any)[0].(map[string]any)["id"].(string)
	list := seller.must(201, "POST", "/v1/price-lists", map[string]any{"name": "Trade"})["list"].(map[string]any)["id"].(string)
	seller.must(200, "PUT", "/v1/price-lists/"+list+"/items", map[string]any{"items": []any{map[string]any{"item_id": sRice, "price_kobo": 7800000, "min_quantity": 2}}})
	seller.must(200, "PUT", "/v1/price-lists/"+list+"/links", map[string]any{"links": []string{sellerLink}})
	link := buyer.must(200, "GET", "/v1/links", nil)["links"].([]any)[0].(map[string]any)["id"].(string)
	sup := buyer.must(201, "POST", "/v1/stock/suppliers", map[string]any{"name": "Dangote Stores", "phone": "0803 123 4888"})["supplier"].(map[string]any)["id"].(string)
	buyer.must(200, "PUT", "/v1/stock/suppliers/"+sup+"/link", map[string]any{"link_id": link})

	// Off: no alerts.
	if got := buyer.must(200, "GET", "/v1/chat-alerts", nil)["alerts"].([]any); len(got) != 0 {
		t.Fatalf("chat tools off: %v", got)
	}
	buyer.must(200, "PUT", "/v1/settings/chat-tools", map[string]any{"chat_tools": true})
	seller.must(200, "PUT", "/v1/settings/chat-tools", map[string]any{"chat_tools": true})

	// Low, with no item link yet: no Reorder.
	low := find(alerts(buyer), "low_stock", "Rice 50kg")
	if low == nil || low["line"] != "4 bags left · you reorder at 10" || low["id"] != rice || low["reorder"] != nil {
		t.Fatalf("low stock: %v", low)
	}
	// Ordered once, so linked; while that order is open there is no card
	// (dealt with). Cancelled: Reorder up to the target, at their price.
	first := buyer.must(201, "POST", "/v1/supplier-prices/"+link+"/order", map[string]any{"lines": []any{map[string]any{"seller_item_id": sRice, "quantity": 2, "item_id": rice}}},
		"Idempotency-Key", "o0")["purchase"].(map[string]any)
	if find(alerts(buyer), "low_stock", "Rice 50kg") != nil {
		t.Fatal("on order, still a low stock card")
	}
	buyer.must(200, "POST", "/v1/stock/purchases/"+first["id"].(string)+"/cancel", nil)
	low = find(alerts(buyer), "low_stock", "Rice 50kg")
	re, _ := low["reorder"].(map[string]any)
	if re == nil || re["link"] != link || re["supplier"] != "Dangote Stores" || re["seller_item_id"] != sRice || re["quantity"] != float64(20) || re["price_kobo"] != float64(7800000) {
		t.Fatalf("reorder: %v", low)
	}
	// A viewer sees it, without Reorder (they may not order).
	viewer := addMember(t, w.world, buyer, "vic", "viewer")
	if v := find(alerts(viewer), "low_stock", "Rice 50kg"); v == nil || v["reorder"] != nil {
		t.Fatalf("the viewer's low stock: %v", v)
	}

	// The order sent: a new order for the seller, not the buyer.
	po := buyer.must(201, "POST", "/v1/supplier-prices/"+link+"/order", map[string]any{"lines": []any{map[string]any{"seller_item_id": sRice, "quantity": 2, "item_id": rice}}},
		"Idempotency-Key", "o1")["purchase"].(map[string]any)
	buyer.must(200, "POST", "/v1/stock/purchases/"+po["id"].(string)+"/send", nil)
	drainLinked(t, w.world)
	ref := po["reference"].(string)
	order := find(alerts(seller), "new_order", ref)
	if order == nil || order["title"] != "New order "+ref+" from Ada's Kitchen" || order["amount_kobo"] != float64(15600000) {
		t.Fatalf("new order: %v", order)
	}
	if find(alerts(buyer), "new_order", ref) != nil {
		t.Fatal("the buyer told of their own order")
	}
	// Later: put off for the seller, still there for another of theirs.
	partner := addMember(t, w.world, seller, "sal", "owner")
	partner.must(200, "GET", "/v1/chat-alerts", nil)
	seller.must(400, "POST", "/v1/chat-alerts/later", map[string]any{"key": " "})
	seller.must(400, "POST", "/v1/chat-alerts/later", map[string]any{"key": "x", "hours": 100})
	seller.must(200, "POST", "/v1/chat-alerts/later", map[string]any{"key": order["key"]})
	if find(alerts(seller), "new_order", ref) != nil {
		t.Fatal("put off, still shown")
	}
	if find(alerts(partner), "new_order", ref) == nil {
		t.Fatal("put off for everyone")
	}
	// Accepted: gone for everyone.
	partner.must(200, "POST", "/v1/trade-orders/"+ref+"/accept", nil)
	if find(alerts(partner), "new_order", ref) != nil {
		t.Fatal("accepted, still a new order")
	}

	// An overdue invoice of the seller's.
	inv := seller.must(201, "POST", "/v1/invoices", map[string]any{"customer": map[string]any{"name": "Bola Ade", "phone": "+2348031234567"},
		"amount_kobo": 900000, "due_on": "2020-01-31"})
	seller.must(200, "POST", "/v1/invoices/"+inv["id"].(string)+"/send", nil, "Idempotency-Key", "s1")
	if _, err := w.srv.Invoices.Overdue(ctx); err != nil {
		t.Fatal(err)
	}
	over := find(alerts(seller), "overdue", inv["number"].(string))
	if over == nil || over["id"] != inv["id"] || !strings.Contains(over["line"].(string), "B•••") || !strings.Contains(over["line"].(string), "₦9,000 to pay") {
		t.Fatalf("overdue: %v", over)
	}
	if find(alerts(buyer), "overdue", inv["number"].(string)) != nil {
		t.Fatal("another business's invoice")
	}
	// A reminder: a diary line, and the card put off for a day.
	seller.must(200, "POST", "/v1/invoices/"+inv["id"].(string)+"/send", nil, "Idempotency-Key", "s2")
	seller.must(200, "POST", "/v1/chat-alerts/later", map[string]any{"key": over["key"], "hours": 24})
	if find(alerts(seller), "overdue", inv["number"].(string)) != nil {
		t.Fatal("reminded, still a card")
	}
	if d := diaryOf(t, seller); !hasLine(d, "Sent a reminder for invoice "+inv["number"].(string)) || !hasLine(d, "Sent invoice "+inv["number"].(string)) {
		t.Fatalf("the reminder's diary: %q", d)
	}

	// A payment that matches nothing.
	sellerTenant := uuid.MustParse(tenantOf(t, seller))
	if _, err := log.RecordEvent(ctx, sellerTenant, "prod", "paystack", "main", "virtual_account.credited",
		map[string]any{"credit_id": "C-9", "reference": "VA-9", "amount_kobo": 123400, "payer_name": ""}); err != nil {
		t.Fatal(err)
	}
	if err := (&matching.Matcher{Invoices: w.srv.Invoices, PII: w.env.Vault}).Tick(ctx); err != nil {
		t.Fatal(err)
	}
	u := find(alerts(seller), "unmatched", "₦1,234")
	if u == nil || u["id"] == "" || !strings.Contains(u["line"].(string), "VA-9") {
		t.Fatalf("unmatched: %v", alerts(seller))
	}
	// Match it: the open invoices, masked; matched, a diary line.
	open := seller.must(201, "POST", "/v1/invoices", map[string]any{"customer": map[string]any{"name": "Chidi Eze", "phone": "+2348031234568"}, "amount_kobo": 123400})
	seller.must(200, "POST", "/v1/invoices/"+open["id"].(string)+"/send", nil, "Idempotency-Key", "s3")
	opts := seller.must(200, "GET", "/v1/chat-alerts/match-options", nil)["options"].([]any)
	var mine map[string]any
	for _, o := range opts {
		if o.(map[string]any)["number"] == open["number"] {
			mine = o.(map[string]any)
		}
	}
	if mine == nil || mine["kind"] != "invoice" || mine["customer"] != "C•••" || mine["due_kobo"] != float64(123400) {
		t.Fatalf("match options: %v", opts)
	}
	viewer2 := addMember(t, w.world, seller, "val", "viewer")
	viewer2.must(403, "GET", "/v1/chat-alerts/match-options", nil)
	seller.must(200, "POST", "/v1/money/entries/"+u["id"].(string)+"/match", map[string]any{"invoice": open["number"]})
	if d := diaryOf(t, seller); !hasLine(d, "Matched a payment to invoice "+open["number"].(string)) {
		t.Fatalf("the match's diary: %q", d)
	}
	if find(alerts(addMember(t, w.world, seller, "bob", "builder")), "unmatched", "₦1,234") != nil {
		t.Fatal("a builder told of money")
	}

	// An approval asked for by a member: with who asked, for the owner.
	wf := publishFlow(t, buyer, payFlow)
	asker := addMember(t, w.world, buyer, "tunde", "operator")
	asker.must(201, "POST", "/v1/workflows/"+wf+"/runs", map[string]any{"input": map[string]any{"amount": 4500000, "account": "0123456789"}})
	w.env.Drain(t)
	ap := find(alerts(buyer), "approval", "")
	if ap == nil || ap["who"] != "tunde (operator)" || !strings.Contains(ap["line"].(string), "Asked by tunde (operator)") {
		t.Fatalf("approval: %v", alerts(buyer))
	}
	if a, _ := ap["approval"].(map[string]any); a == nil || a["subject"].(map[string]any)["account_name"] != "Kunle Supplies" {
		t.Fatalf("the approval's subject: %v", ap)
	}
	if find(alerts(viewer), "approval", "") != nil {
		t.Fatal("a viewer asked to approve")
	}
	// Approved: gone, and a diary line.
	ar := ap["approval"].(map[string]any)
	buyer.must(200, "POST", "/v1/approvals/"+ar["run_id"].(string)+"/"+ar["step_id"].(string), map[string]any{"decision": "approved"})
	if find(alerts(buyer), "approval", "") != nil {
		t.Fatal("approved, still a card")
	}
	if d := diaryOf(t, buyer); !hasLine(d, "Approved "+ar["step_name"].(string)+" ("+ar["workflow"].(string)+")") {
		t.Fatalf("the decision's diary: %q", d)
	}
	_ = buyerID
}

func diaryOf(t *testing.T, c *client) []string {
	t.Helper()
	var out []string
	for _, x := range c.must(200, "GET", "/v1/taskiem-chat", nil)["messages"].([]any) {
		if m := x.(map[string]any); m["kind"] == "diary" {
			out = append(out, m["text"].(string))
		}
	}
	return out
}

func hasLine(lines []string, want string) bool {
	for _, l := range lines {
		if l == want {
			return true
		}
	}
	return false
}

// A delivery order no rider has been asked to take is an alert, with
// Assign rider; offered, it is gone.
func TestChatAlertRider(t *testing.T) {
	w, ada := newDispatchWorld(t, "Ada Stores")
	ada.must(200, "PUT", "/v1/settings/chat-tools", map[string]any{"chat_tools": true})
	riderAlert := func(ref string) map[string]any {
		t.Helper()
		for _, x := range ada.must(200, "GET", "/v1/chat-alerts", nil)["alerts"].([]any) {
			if a := x.(map[string]any); a["kind"] == "rider" && a["reference"] == ref {
				return a
			}
		}
		return nil
	}
	// Accepted, to be paid before it goes: not ready for a rider.
	unpaid := w.order(t, "after_accept")
	// No riders yet: nothing to assign.
	ref := w.order(t, "none")
	if riderAlert(ref) != nil {
		t.Fatal("a rider alert with no riders")
	}
	path := strings.TrimPrefix(ada.must(200, "GET", "/v1/riders", nil)["tap_in_url"].(string), "https://"+reachFormHost)
	loc, _ := url.Parse(w.page(t, path+"/whatsapp", nil).Header().Get("Location"))
	const musa = "+2348031110071"
	w.whatsapp(t, musa, loc.Query().Get("text"))
	ada.must(201, "POST", "/v1/riders", map[string]any{"name": "Musa Bello", "phone": musa, "zones": []string{"Lekki"}, "fee_kobo": 100000})
	a := riderAlert(ref)
	if a == nil || a["title"] != "Order "+ref+" needs a rider" || !strings.Contains(a["line"].(string), "paid on delivery") {
		t.Fatalf("rider alert: %v", a)
	}
	if riderAlert(unpaid) != nil {
		t.Fatal("a rider alert for an order not paid yet")
	}
	out := ada.must(200, "POST", "/v1/storefront/orders/"+ref+"/offer", map[string]any{})
	if riderAlert(ref) != nil {
		t.Fatal("offered, still waiting for a rider")
	}
	if d := diaryOf(t, ada); out["status"] != "offered" || !hasLine(d, "Offered order "+ref+" to Musa Bello") {
		t.Fatalf("the offer's diary: %v %q", out, d)
	}
}
