package api_test

import (
	"strings"
	"testing"
)

// Mentions in the Taskiem conversation (L4, §5.5, §9): one message naming
// two linked businesses is read back as two orders, one per business,
// each from its price list at its prices; one yes sends both, and each
// arrives in its own business; a mention of a business not linked offers
// the link; the same on the web.
func TestMentionsOrderFromTwo(t *testing.T) {
	w := shopWorld(t)
	withStock(w.world)
	withPurchases(w.world)
	withLinkedOrders(w.world)
	dangote, buyer, _, _ := linkedPair(t, w.waWorld)
	const buyerOn = "+2348010000611"
	w.bind(t, buyer, buyerOn)
	w.say(t, buyerOn, "links")
	cherry := w.tenant(t, "Cherry Blocks", "c@mentions.test")
	cherry.must(200, "PUT", "/v1/link-settings", map[string]any{"findable": true})
	buyer.must(201, "POST", "/v1/links", map[string]any{"business": tenantOf(t, cherry), "role": "supplier"})
	cherryLink := cherry.must(200, "GET", "/v1/links", nil)["links"].([]any)[0].(map[string]any)["id"].(string)
	cherry.must(200, "POST", "/v1/links/"+cherryLink+"/accept", nil)
	w.srv.WaitBackground()

	// The buyer's items and suppliers, each the linked business.
	buyer.must(200, "POST", "/v1/stock/setup", nil)
	for _, it := range []map[string]any{{"name": "Cement", "unit": "bag"}, {"name": "Blocks", "unit": "piece"}} {
		it["quantity"] = 1
		buyer.must(201, "POST", "/v1/stock/items", it, "Idempotency-Key", "b-"+it["name"].(string))
	}
	linkOf := func(name string) string {
		for _, x := range buyer.must(200, "GET", "/v1/links", nil)["links"].([]any) {
			if l := x.(map[string]any); l["name"] == name {
				return l["id"].(string)
			}
		}
		t.Fatalf("no link with %s", name)
		return ""
	}
	for i, name := range []string{"Dangote Stores", "Cherry Blocks"} {
		sup := buyer.must(201, "POST", "/v1/stock/suppliers", map[string]any{"name": name, "phone": "0803 123 46" + string(rune('0'+i)) + "0"})["supplier"].(map[string]any)["id"].(string)
		buyer.must(200, "PUT", "/v1/stock/suppliers/"+sup+"/link", map[string]any{"link_id": linkOf(name)})
	}
	// Each seller's price list, shared with the buyer.
	share := func(seller *client, item, unit string, price int) string {
		t.Helper()
		seller.must(200, "POST", "/v1/stock/setup", nil)
		id := seller.must(201, "POST", "/v1/stock/items", map[string]any{"name": item, "quantity": 500, "unit": unit, "price_kobo": price},
			"Idempotency-Key", "s-"+item)["item"].(map[string]any)["id"].(string)
		list := seller.must(201, "POST", "/v1/price-lists", map[string]any{"name": "Trade"})["list"].(map[string]any)["id"].(string)
		seller.must(200, "PUT", "/v1/price-lists/"+list+"/items", map[string]any{"items": []any{map[string]any{"item_id": id, "price_kobo": price}}})
		l := seller.must(200, "GET", "/v1/links", nil)["links"].([]any)[0].(map[string]any)["id"].(string)
		seller.must(200, "PUT", "/v1/price-lists/"+list+"/links", map[string]any{"links": []string{l}})
		return id
	}
	sCement := share(dangote, "Cement", "bag", 950000)
	share(cherry, "Blocks", "piece", 45000)

	// Two businesses in one message: two orders, one yes.
	r := w.say(t, buyerOn, "Buy 400 bags of cement from @Dangote Stores and 20 pieces of blocks from @Cherry Blocks")
	if !strings.Contains(r, "2 orders, one per business") || !strings.Contains(r, "*Dangote Stores*") || !strings.Contains(r, "₦3,800,000") ||
		!strings.Contains(r, "*Cherry Blocks*") || !strings.Contains(r, "₦9,000") {
		t.Fatalf("read back: %q", r)
	}
	if r := w.say(t, buyerOn, "yes"); !strings.Contains(r, "Sent:") || !strings.Contains(r, "Dangote Stores") || !strings.Contains(r, "Cherry Blocks") {
		t.Fatalf("sent: %q", r)
	}
	drainLinked(t, w.world)
	for _, seller := range []*client{dangote, cherry} {
		if got := seller.must(200, "GET", "/v1/trade-orders", nil)["orders"].([]any); len(got) != 1 {
			t.Fatalf("an order each: %v", got)
		}
	}
	o := cherry.must(200, "GET", "/v1/trade-orders", nil)["orders"].([]any)[0].(map[string]any)
	if l := o["lines"].([]any)[0].(map[string]any); l["quantity"] != float64(20) || l["matched"] != "linked" || l["price_kobo"] != float64(45000) {
		t.Errorf("Cherry's order: %v", l)
	}

	// A business not linked: the link offered.
	if r := w.say(t, buyerOn, "Buy 5 bags of sand from @Bola Builders"); !strings.Contains(r, "not linked with *Bola*") || !strings.Contains(r, "link with Bola") {
		t.Fatalf("not linked: %q", r)
	}
	// Nothing to order.
	if r := w.say(t, buyerOn, "hello @Dangote Stores"); !strings.Contains(r, "Say how many") {
		t.Fatalf("nothing asked: %q", r)
	}

	// On the web: the same words, the same answer.
	out := buyer.must(200, "POST", "/v1/taskiem-chat", map[string]any{"text": "order 10 bags of cement from @Dangote Stores"})["messages"].([]any)
	if a := out[len(out)-1].(map[string]any)["text"].(string); !strings.Contains(a, "₦95,000") || !strings.Contains(a, "Send it to them?") {
		t.Fatalf("on the web: %v", out)
	}

	// In a conversation: "#" names the price list's items, "@" a linked
	// business, structured; the other side's copy names the business
	// without our link.
	dl := linkOf("Dangote Stores")
	items := buyer.must(200, "GET", "/v1/link-chats/"+dl+"/items", nil)
	if items["whose"] != "theirs" || items["items"].([]any)[0].(map[string]any)["item_id"] != sCement {
		t.Fatalf("what # names: %v", items)
	}
	if seen := dangote.must(200, "GET", "/v1/link-chats/"+dl+"/items", nil); seen["whose"] != "ours" {
		t.Fatalf("the seller's # names its own list: %v", seen)
	}
	cl := linkOf("Cherry Blocks")
	msg := buyer.must(201, "POST", "/v1/link-chats/"+dl+"/messages", map[string]any{"text": "Can I get 400 #Cement this week? @Cherry Blocks has the blocks.",
		"mentions": []any{map[string]any{"kind": "item", "item_id": sCement}, map[string]any{"kind": "business", "link": cl},
			map[string]any{"kind": "business", "link": dl}}})["message"].(map[string]any)
	ms := msg["mentions"].([]any)
	if len(ms) != 2 || ms[0].(map[string]any)["price_kobo"] != float64(950000) || ms[0].(map[string]any)["unit"] != "bag" ||
		ms[1].(map[string]any)["link"] != cl || ms[1].(map[string]any)["name"] != "Cherry Blocks" {
		t.Fatalf("mentions (one not in the text left out): %v", ms)
	}
	got := dangote.must(200, "GET", "/v1/link-chats/"+dl+"/messages", nil)["messages"].([]any)
	theirs := got[len(got)-1].(map[string]any)["mentions"].([]any)
	if len(theirs) != 2 || theirs[1].(map[string]any)["link"] != nil || theirs[1].(map[string]any)["name"] != "Cherry Blocks" {
		t.Fatalf("their copy: %v", theirs)
	}
	buyer.must(400, "POST", "/v1/link-chats/"+dl+"/messages", map[string]any{"text": "#Sand", "mentions": []any{map[string]any{"kind": "item", "item_id": "nope"}}})
	buyer.must(400, "POST", "/v1/link-chats/"+dl+"/messages", map[string]any{"text": "@x", "mentions": []any{map[string]any{"kind": "business", "link": "00000000-0000-0000-0000-000000000001"}}})
}
