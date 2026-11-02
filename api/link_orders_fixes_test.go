package api_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/israel-duff/taskiem/engine/storefront"
)

// The prices of a linked order are the seller's side's, never the buyer's
// (docs/spec/business-links.md §7, §9, §16.3): a purchase order with a ₦1
// cost on a linked item arrives at the list's price, the buyer's proposal
// kept beside it; an order set to accept by itself is accepted only when
// every line is priced by the seller's side and at the price the buyer
// proposed, else it is the seller's change for the buyer to accept, and
// never when a line is priced by the buyer alone. The storefront's own
// paths accept no order with an unpriced line, and an invoice that cannot
// be raised is not tried for ever. A delivery is never more than was
// agreed. Not all came after the day is a dispute: no stock moves until
// the seller agrees, which takes what did not come off the invoice and
// off the buyer's stock. The order is received by the link's steps only,
// on the web and in chat.
func TestLinkedOrderFixes(t *testing.T) {
	ctx := context.Background()
	w := shopWorld(t)
	withStock(w.world)
	withPurchases(w.world)
	withLinkedOrders(w.world)
	seller, buyer, sellerID, _ := linkedPair(t, w.waWorld)
	const buyerOn = "+2348010000381"
	w.bind(t, buyer, buyerOn)
	w.say(t, buyerOn, "links")
	tick := func() { t.Helper(); drainLinked(t, w.world) }
	buyer.must(200, "POST", "/v1/stock/setup", nil)
	rice := buyer.must(201, "POST", "/v1/stock/items", map[string]any{"name": "Rice", "quantity": 3, "unit": "bag", "cost_kobo": 1},
		"Idempotency-Key", "r")["item"].(map[string]any)["id"].(string)
	beans := buyer.must(201, "POST", "/v1/stock/items", map[string]any{"name": "Beans", "quantity": 1, "unit": "bag", "cost_kobo": 100},
		"Idempotency-Key", "b")["item"].(map[string]any)["id"].(string)
	seller.must(200, "POST", "/v1/stock/setup", nil)
	sRice := seller.must(201, "POST", "/v1/stock/items", map[string]any{"name": "Rice", "quantity": 500, "unit": "bag", "price_kobo": 4500000},
		"Idempotency-Key", "sr")["item"].(map[string]any)["id"].(string)
	sup := buyer.must(201, "POST", "/v1/stock/suppliers", map[string]any{"name": "Dangote", "phone": "0803 123 4801", "items": []string{rice, beans}})["supplier"].(map[string]any)["id"].(string)
	link := buyer.must(200, "GET", "/v1/links", nil)["links"].([]any)[0].(map[string]any)["id"].(string)
	sellerLink := seller.must(200, "GET", "/v1/links", nil)["links"].([]any)[0].(map[string]any)["id"].(string)
	buyer.must(200, "PUT", "/v1/stock/suppliers/"+sup+"/link", map[string]any{"link_id": link})
	list := seller.must(201, "POST", "/v1/price-lists", map[string]any{"name": "Trade"})["list"].(map[string]any)["id"].(string)
	seller.must(200, "PUT", "/v1/price-lists/"+list+"/items", map[string]any{"items": []any{map[string]any{"item_id": sRice, "price_kobo": 4200000, "min_quantity": 1}}})
	seller.must(200, "PUT", "/v1/price-lists/"+list+"/links", map[string]any{"links": []string{sellerLink}})
	buyer.must(200, "PUT", "/v1/item-links/"+link, map[string]any{"item_id": rice, "seller_item_id": sRice})
	seller.must(200, "PUT", "/v1/links/"+sellerLink+"/trade", map[string]any{"auto_accept": true})
	purchase := func(id string) map[string]any {
		t.Helper()
		return buyer.must(200, "GET", "/v1/stock/purchases/"+id, nil)["purchase"].(map[string]any)
	}
	orderOf := func(ref string) map[string]any {
		t.Helper()
		return seller.must(200, "GET", "/v1/trade-orders/"+ref, nil)["order"].(map[string]any)
	}
	send := func(key string, lines ...map[string]any) (string, string) {
		t.Helper()
		id := buyer.must(201, "POST", "/v1/stock/purchases", map[string]any{"supplier_id": sup, "lines": lines}, "Idempotency-Key", key)["purchase"].(map[string]any)["id"].(string)
		ref := buyer.must(200, "POST", "/v1/stock/purchases/"+id+"/send", nil)["purchase"].(map[string]any)["reference"].(string)
		tick()
		return id, ref
	}
	buyerStock := func(name string) any {
		t.Helper()
		return itemsByName(t, buyer.must(200, "GET", "/v1/stock", nil))[name]["stock"]
	}

	// 1. A ₦1 cost on a linked item: the order is at the list's price, and,
	// another price than proposed, it is the seller's change, not accepted
	// by itself; the buyer accepts it.
	po, ref := send("p1", map[string]any{"item_id": rice, "quantity": 10})
	o := orderOf(ref)
	l := o["lines"].([]any)[0].(map[string]any)
	if l["price_kobo"] != float64(4200000) || l["proposed_kobo"] != float64(1) || l["priced_by"] != "list" || l["matched"] != "linked" {
		t.Fatalf("at the list's price: %v", l)
	}
	if o["status"] != "changed" {
		t.Fatalf("not accepted at another price than proposed: %v", o)
	}
	if p := purchase(po); p["seller_status"] != "changed" || p["change"].([]any)[0].(map[string]any)["cost_kobo"] != float64(4200000) {
		t.Fatalf("the change to the buyer: %v", p)
	}
	if p := buyer.must(200, "POST", "/v1/stock/purchases/"+po+"/accept-change", nil)["purchase"].(map[string]any); p["total_kobo"] != float64(42000000) {
		t.Fatalf("accepted at the list's price: %v", p)
	}
	w.srv.WaitBackground()
	if o := orderOf(ref); o["status"] != "accepted" || o["total_kobo"] != float64(42000000) {
		t.Fatalf("accepted: %v", o)
	}
	if got := itemsByName(t, seller.must(200, "GET", "/v1/stock", nil))["Rice"]["stock"]; got != float64(490) {
		t.Errorf("the seller's stock lowered on the buyer's acceptance: %v", got)
	}
	var due int
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT count(*) FROM link_orders_due WHERE kind = 'stock'`).Scan(&due); err != nil || due != 0 {
		t.Errorf("the stock's work done: %d %v", due, err)
	}
	// Ordered at the list's price, it is accepted by itself.
	po2, ref2 := send("p2", map[string]any{"item_id": rice, "quantity": 2, "cost_kobo": 4200000})
	if o := orderOf(ref2); o["status"] != "accepted" {
		t.Fatalf("accepted by itself at the proposed price: %v", o)
	}
	// A line the seller's side did not price (no such item of its own) is
	// never accepted by itself, whatever the buyer proposed.
	_, ref3 := send("p3", map[string]any{"item_id": beans, "quantity": 1, "cost_kobo": 100})
	o3 := orderOf(ref3)
	if o3["status"] != "received" || o3["lines"].([]any)[0].(map[string]any)["priced_by"] != nil {
		t.Fatalf("priced by the buyer alone, waiting: %v", o3)
	}

	// 3. The storefront's own path (a workflow's decision) accepts no
	// order with an unpriced line; an invoice that cannot be raised is not
	// tried for ever, and the seller is told.
	if _, err := w.env.DB.Admin.Exec(ctx, `UPDATE link_orders SET lines = jsonb_set(lines, '{0,price_kobo}', '0') WHERE order_id = $1`, o3["id"]); err != nil {
		t.Fatal(err)
	}
	if _, err := w.srv.Orders.Decide(ctx, uuid.MustParse(sellerID), o3["order_reference"].(string), "accept", "", "workflow:test"); !errors.Is(err, storefront.ErrDecision) ||
		!strings.Contains(err.Error(), "no price") {
		t.Fatalf("accepted unpriced: %v", err)
	}
	seller.must(400, "POST", "/v1/trade-orders/"+ref3+"/accept", nil)
	if _, err := w.env.DB.Admin.Exec(ctx, `INSERT INTO link_orders_due (tenant_id, kind, object_id) VALUES ($1, 'invoice', $2)`, sellerID, o3["id"]); err != nil {
		t.Fatal(err)
	}
	tick()
	var left, alerted int
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT count(*) FROM link_orders_due WHERE kind = 'invoice' AND object_id = $1`, o3["id"]).Scan(&left); err != nil || left != 0 {
		t.Errorf("the invoice not tried again: %d %v", left, err)
	}
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT count(*) FROM alerts WHERE tenant_id = $1 AND kind = 'link.order_invoice'`, sellerID).Scan(&alerted); err != nil || alerted != 1 {
		t.Errorf("the seller told: %d %v", alerted, err)
	}

	// 4. A delivery is never more than was agreed.
	seller.must(400, "POST", "/v1/trade-orders/"+ref2+"/delivered", map[string]any{"delivered": []int64{3}})
	seller.must(200, "POST", "/v1/trade-orders/"+ref2+"/delivered", map[string]any{"delivered": []int64{2}})
	w.srv.WaitBackground()
	buyer.must(400, "POST", "/v1/stock/purchases/"+po2+"/not-all-came", map[string]any{"counts": []int64{3}})

	// 2. Received by the link's steps only: the web's Receive and chat's
	// "received" are refused on a linked order (chat finds the oldest open
	// order from the supplier with the item).
	if r := w.say(t, buyerOn, "received 10 bags of rice"); !strings.Contains(r, "is not delivered yet") {
		t.Fatalf("chat on a linked order not delivered: %q", r)
	}
	seller.must(200, "POST", "/v1/trade-orders/"+ref+"/delivered", map[string]any{"delivered": []int64{10}})
	w.srv.WaitBackground()
	buyer.must(409, "POST", "/v1/stock/purchases/"+po+"/receive", map[string]any{"lines": []any{map[string]any{"item_id": rice, "quantity": 10}}}, "Idempotency-Key", "rc")
	if r := w.say(t, buyerOn, "received 10 bags of rice"); !strings.Contains(r, "delivered order "+ref+" through Taskiem") {
		t.Fatalf("chat on a delivered linked order: %q", r)
	}
	if got := buyerStock("Rice"); got != float64(3) {
		t.Fatalf("nothing received by hand: %v", got)
	}

	// 5. After the day, the seller's count is received; Not all came is
	// still a dispute, moving no stock; the seller's agreement takes what
	// did not come off the invoice and off the buyer's stock.
	if _, err := w.env.DB.Admin.Exec(ctx, `UPDATE link_orders_due SET due_at = now() WHERE kind = 'receipt' AND object_id = $1`, po); err != nil {
		t.Fatal(err)
	}
	tick()
	if p := purchase(po); p["status"] != "received" {
		t.Fatalf("received from the seller's count: %v", p)
	}
	if got := buyerStock("Rice"); got != float64(13) {
		t.Fatalf("stock by the seller's count: %v", got)
	}
	o = orderOf(ref)
	if o["invoice_id"] == nil {
		t.Fatalf("invoiced on delivery: %v", o)
	}
	buyer.must(409, "POST", "/v1/stock/purchases/"+po+"/all-came", nil)
	if p := buyer.must(200, "POST", "/v1/stock/purchases/"+po+"/not-all-came", map[string]any{"counts": []int64{7}})["purchase"].(map[string]any); p["short_at"] == nil {
		t.Fatalf("a dispute after the day: %v", p)
	}
	if got := buyerStock("Rice"); got != float64(13) {
		t.Fatalf("no stock moved by the dispute: %v", got)
	}
	if o := orderOf(ref); o["short"] == nil || o["short"].([]any)[0] != float64(7) {
		t.Fatalf("the seller sees the count: %v", o)
	}
	seller.must(200, "POST", "/v1/trade-orders/"+ref+"/agree", nil)
	inv := seller.must(200, "GET", "/v1/invoices/"+o["invoice_id"].(string), nil)["invoice"].(map[string]any)
	if inv["total_kobo"] != float64(29400000) { // 10 billed at ₦42,000, less the 3 that did not come
		t.Errorf("the invoice after the count agreed: %v", inv["total_kobo"])
	}
	tick()
	if got := buyerStock("Rice"); got != float64(10) {
		t.Errorf("the buyer's stock adjusted to its count: %v", got)
	}
	if p := purchase(po); p["lines"].([]any)[0].(map[string]any)["received"] != float64(7) || p["received_kobo"] != float64(29400000) {
		t.Errorf("the order adjusted: %v", p)
	}
}
