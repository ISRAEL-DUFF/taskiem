package api_test

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/invoicing"
)

// The first slice's trade, end to end between two businesses (docs/spec/
// chat-tools.md §5, phase 3), as the chat tools make it:
//  1. the seller changes the price on the buyer's own list, and a diary line
//     says so;
//  2. the buyer tags @Rice in their conversation (its card, with today's
//     price), and orders from the price list;
//  3. the seller sees the order's card and accepts it;
//  4. the invoice arrives as the buyer's bill, which the buyer pays on the
//     seller's checkout;
//  5. both see the order and the invoice, paid, in their history, with the
//     buyer's own steps as diary lines.
func TestChatTrade(t *testing.T) {
	ctx := context.Background()
	w := shopWorld(t)
	withStock(w.world)
	withPurchases(w.world)
	withLinkedOrders(w.world)
	seller, buyer, sellerID, _ := linkedPair(t, w.waWorld)
	seller.must(200, "PUT", "/v1/settings/chat-tools", map[string]any{"chat_tools": true})
	buyer.must(200, "PUT", "/v1/settings/chat-tools", map[string]any{"chat_tools": true})
	diary := func(c *client) []string {
		t.Helper()
		var out []string
		for _, x := range c.must(200, "GET", "/v1/taskiem-chat", nil)["messages"].([]any) {
			if m := x.(map[string]any); m["kind"] == "diary" {
				out = append(out, m["text"].(string))
			}
		}
		return out
	}
	has := func(lines []string, want string) bool {
		for _, l := range lines {
			if l == want {
				return true
			}
		}
		return false
	}
	cards := func(c *client, link, doc string) []map[string]any {
		t.Helper()
		var out []map[string]any
		for _, x := range c.must(200, "GET", "/v1/link-chats/"+link+"/messages", nil)["messages"].([]any) {
			if m := x.(map[string]any); m["kind"] == "card" && m["card"].(map[string]any)["doc"] == doc {
				out = append(out, m["card"].(map[string]any))
			}
		}
		return out
	}

	// The seller's rice on a list shared with the buyer; the buyer's own rice.
	seller.must(200, "POST", "/v1/stock/setup", nil)
	sRice := seller.must(201, "POST", "/v1/stock/items", map[string]any{"name": "Rice 50kg", "quantity": 40, "unit": "bag", "price_kobo": 8000000},
		"Idempotency-Key", "sr")["item"].(map[string]any)["id"].(string)
	sellerLink := seller.must(200, "GET", "/v1/links", nil)["links"].([]any)[0].(map[string]any)["id"].(string)
	list := seller.must(201, "POST", "/v1/price-lists", map[string]any{"name": "Trade prices"})["list"].(map[string]any)["id"].(string)
	seller.must(200, "PUT", "/v1/price-lists/"+list+"/items", map[string]any{"items": []any{map[string]any{"item_id": sRice, "price_kobo": 7600000, "min_quantity": 2}}})
	seller.must(200, "PUT", "/v1/price-lists/"+list+"/links", map[string]any{"links": []string{sellerLink}})
	seller.must(200, "PUT", "/v1/links/"+sellerLink+"/trade", map[string]any{"invoice_at": "accepted"})
	buyer.must(200, "POST", "/v1/stock/setup", nil)
	rice := buyer.must(201, "POST", "/v1/stock/items", map[string]any{"name": "Rice 50kg", "quantity": 1, "unit": "bag"},
		"Idempotency-Key", "r")["item"].(map[string]any)["id"].(string)
	link := buyer.must(200, "GET", "/v1/links", nil)["links"].([]any)[0].(map[string]any)["id"].(string)

	// 1. The buyer's price, changed from chat: a diary line says so.
	seller.must(200, "PUT", "/v1/link-prices/"+sellerLink, map[string]any{"prices": map[string]any{sRice: 7800000}})
	if d := diary(seller); !has(d, "Price of Rice 50kg for Ada's Kitchen: ₦76,000 → ₦78,000") {
		t.Fatalf("step 1, the seller's diary: %q", d)
	}

	// 2. The buyer tags @Rice 50kg: its card has the price today. Then the
	// Order sheet: the price list, the quantities, a supplier linked once,
	// and the order placed and sent.
	items := buyer.must(200, "GET", "/v1/link-chats/"+link+"/items", nil)
	if items["whose"] != "theirs" || items["items"].([]any)[0].(map[string]any)["price_kobo"] != float64(7800000) {
		t.Fatalf("step 2, what the buyer can tag: %v", items)
	}
	tagged := buyer.must(201, "POST", "/v1/link-chats/"+link+"/messages", map[string]any{"text": "Do you have @Rice 50kg this week?",
		"mentions": []any{map[string]any{"kind": "item", "item_id": sRice}}})["message"].(map[string]any)
	if m := tagged["mentions"].([]any)[0].(map[string]any); m["kind"] != "item" || m["price_kobo"] != float64(7800000) || m["unit"] != "bag" {
		t.Fatalf("step 2, the card's mention: %v", m)
	}
	prices := buyer.must(200, "GET", "/v1/supplier-prices?link="+link, nil)["suppliers"].([]any)
	if len(prices) != 1 || prices[0].(map[string]any)["items"].([]any)[0].(map[string]any)["price_kobo"] != float64(7800000) {
		t.Fatalf("step 2, the price list: %v", prices)
	}
	// Not one of the buyer's suppliers yet: the sheet adds and links one.
	order := map[string]any{"lines": []any{map[string]any{"seller_item_id": sRice, "quantity": 3, "item_id": rice}}, "note": "Before Friday"}
	buyer.must(409, "POST", "/v1/supplier-prices/"+link+"/order", order, "Idempotency-Key", "o0")
	sup := buyer.must(201, "POST", "/v1/stock/suppliers", map[string]any{"name": "Dangote Stores", "phone": "0803 123 4777"})["supplier"].(map[string]any)["id"].(string)
	buyer.must(200, "PUT", "/v1/stock/suppliers/"+sup+"/link", map[string]any{"link_id": link})
	po := buyer.must(201, "POST", "/v1/supplier-prices/"+link+"/order", order, "Idempotency-Key", "o1")["purchase"].(map[string]any)
	if po["total_kobo"] != float64(23400000) {
		t.Fatalf("step 2, drafted at the buyer's price: %v", po)
	}
	sent := buyer.must(200, "POST", "/v1/stock/purchases/"+po["id"].(string)+"/send", nil)["purchase"].(map[string]any)
	ref := sent["reference"].(string)
	if d := diary(buyer); !has(d, "Sent order "+ref+" to Dangote Stores: ₦234,000") {
		t.Fatalf("step 2, the buyer's diary: %q", d)
	}
	drainLinked(t, w.world)

	// 3. The seller sees the order's card, and accepts it from Orders.
	if cs := cards(seller, sellerLink, "order"); len(cs) == 0 || cs[0]["reference"] != ref || cs[0]["amount_kobo"] != float64(23400000) {
		t.Fatalf("step 3, the seller's card: %v", cs)
	}
	var mine []string
	for _, x := range seller.must(200, "GET", "/v1/trade-orders", nil)["orders"].([]any) {
		mine = append(mine, x.(map[string]any)["order_reference"].(string))
	}
	if len(mine) != 1 || mine[0] != ref {
		t.Fatalf("step 3, the Orders tool: %v", mine)
	}
	seller.must(200, "POST", "/v1/trade-orders/"+ref+"/accept", nil)
	if d := diary(seller); !has(d, "Accepted order "+ref+" from Ada's Kitchen") {
		t.Fatalf("step 3, the seller's diary: %q", d)
	}
	drainLinked(t, w.world)

	// 4. The invoice arrives as the buyer's bill, with its card, and is paid
	// on the seller's checkout (its page; the provider tells the seller's
	// Taskiem, which counts it).
	o := seller.must(200, "GET", "/v1/trade-orders/"+ref, nil)["order"].(map[string]any)
	if o["invoice_id"] == nil {
		t.Fatalf("step 4, invoiced at acceptance: %v", o)
	}
	inv := seller.must(200, "GET", "/v1/invoices/"+o["invoice_id"].(string), nil)["invoice"].(map[string]any)
	number := inv["number"].(string)
	if _, err := w.srv.LinkMirrorTick(ctx); err != nil {
		t.Fatal(err)
	}
	w.srv.WaitBackground()
	bills := buyer.must(200, "GET", "/v1/bills?status=all", nil)["bills"].([]any)
	if len(bills) != 1 {
		t.Fatalf("step 4, the bill: %v", bills)
	}
	bill := bills[0].(map[string]any)
	if bill["reference"] != number || bill["due_kobo"] != float64(23400000) || bill["link"] != link || !strings.Contains(bill["pay_link"].(string), "/i/") {
		t.Fatalf("step 4, the bill: %v", bill)
	}
	if cs := cards(buyer, link, "bill"); len(cs) == 0 || cs[len(cs)-1]["reference"] != number {
		t.Fatalf("step 4, the buyer's bill card: %v", cs)
	}
	if err := db.InTenantTx(ctx, w.env.DB.App, []uuid.UUID{uuid.MustParse(sellerID)}, func(tx pgx.Tx) error {
		_, _, err := w.srv.Invoices.RecordPayment(ctx, tx, uuid.MustParse(sellerID), number,
			invoicing.PaymentIn{AmountKobo: 23400000, PaymentID: "checkout-1", How: "exact", By: "provider"})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := w.srv.LinkMirrorTick(ctx); err != nil {
		t.Fatal(err)
	}
	w.srv.WaitBackground()

	// 5. Both see the order and the invoice in their history.
	if b := buyer.must(200, "GET", "/v1/bills/"+bill["id"].(string), nil); b["status"] != "paid" || b["due_kobo"] != float64(0) {
		t.Errorf("step 5, the buyer's bill: %v", b)
	}
	var found bool
	for _, x := range buyer.must(200, "GET", "/v1/stock/purchases", nil)["purchases"].([]any) {
		if p := x.(map[string]any); p["reference"] == ref && p["link_id"] == link {
			found = p["seller_status"] == "accepted"
		}
	}
	if !found {
		t.Errorf("step 5, the buyer's order, accepted")
	}
	if inv := seller.must(200, "GET", "/v1/invoices/"+o["invoice_id"].(string), nil)["invoice"].(map[string]any); inv["status"] != "paid" {
		t.Errorf("step 5, the seller's invoice: %v", inv["status"])
	}
	if o := seller.must(200, "GET", "/v1/trade-orders/"+ref, nil)["order"].(map[string]any); o["status"] != "accepted" {
		t.Errorf("step 5, the seller's order: %v", o["status"])
	}

}
