package api_test

import (
	"encoding/json"
	"net/url"
	"strings"
	"testing"
	"time"
)

// Staff use chat as their app (docs/spec/chat-tools.md §7), and the owner
// gets the 8am card. A cashier works a shift from chat alone: sells on
// WhatsApp and with the Sell tool, is told of a new order and accepts it,
// sees their tools and no more; a shift manager hands the shift over, and
// the owner is told; the owner's briefing is ready at 8am Lagos, and "how
// did we do this week?" is answered with a chart.
func TestCashierShift(t *testing.T) {
	w := shopWorld(t)
	withStock(w.world)
	withPurchases(w.world)
	withLinkedOrders(w.world)
	seller, buyer, _, _ := linkedPair(t, w.waWorld)
	const ownerOn, cashOn = "+2348010000911", "+2348010000912"
	w.bind(t, seller, ownerOn)
	w.say(t, ownerOn, "links")
	seller.must(200, "PUT", "/v1/settings/chat-tools", map[string]any{"chat_tools": true})
	seller.must(200, "POST", "/v1/stock/setup", nil)
	rice := seller.must(201, "POST", "/v1/stock/items", map[string]any{"name": "Rice", "quantity": 40, "unit": "bag", "price_kobo": 7800000, "reorder_level": 10},
		"Idempotency-Key", "r")["item"].(map[string]any)["id"].(string)
	cashier := addMember(t, w.world, seller, "cash", "cashier")
	w.bind(t, cashier, cashOn)

	// A sale on WhatsApp, and one from the Sell tool, each a diary line.
	if r := w.say(t, cashOn, "sold 2 bags of rice 156,000 cash"); !strings.Contains(r, "Sold 2 bags Rice for ₦156,000, in cash?") {
		t.Fatalf("a sale on WhatsApp: %q", r)
	}
	if r := w.yes(t, cashOn); !strings.Contains(r, "Recorded S-") {
		t.Fatalf("recorded: %q", r)
	}
	cashier.must(201, "POST", "/v1/stock/sales", map[string]any{"lines": []any{map[string]any{"item_id": rice, "quantity": 1}}, "paid_how": "pos"},
		"Idempotency-Key", "sell-1")
	if d := diaryOf(t, cashier); !hasLine(d, "Sold 1 Rice: ₦78,000 (POS)") {
		t.Fatalf("the sale's diary: %q", d)
	}
	// Their tools, and not the storefront's.
	if r := w.say(t, cashOn, "tools"); !strings.Contains(r, "Orders") || !strings.Contains(r, "Stock") || strings.Contains(r, "Price lists") {
		t.Fatalf("a cashier's tools: %q", r)
	}
	cashier.must(403, "GET", "/v1/stock/suppliers", nil)
	cashier.must(403, "GET", "/v1/price-lists", nil)
	cashier.must(403, "POST", "/v1/storefront/orders/X-1/refunded", nil)

	// A linked business orders: the cashier is told, sees the card, and
	// accepts it.
	buyer.must(200, "POST", "/v1/stock/setup", nil)
	bRice := buyer.must(201, "POST", "/v1/stock/items", map[string]any{"name": "Rice", "quantity": 1, "unit": "bag"}, "Idempotency-Key", "br")["item"].(map[string]any)["id"].(string)
	sup := buyer.must(201, "POST", "/v1/stock/suppliers", map[string]any{"name": "Dangote", "phone": "0803 123 4991"})["supplier"].(map[string]any)["id"].(string)
	link := buyer.must(200, "GET", "/v1/links", nil)["links"].([]any)[0].(map[string]any)["id"].(string)
	buyer.must(200, "PUT", "/v1/stock/suppliers/"+sup+"/link", map[string]any{"link_id": link})
	po := buyer.must(201, "POST", "/v1/stock/purchases", map[string]any{"supplier_id": sup, "lines": []any{map[string]any{"item_id": bRice, "quantity": 5}}},
		"Idempotency-Key", "po")["purchase"].(map[string]any)["id"].(string)
	before := len(w.graph.Sent(cashOn))
	ref := buyer.must(200, "POST", "/v1/stock/purchases/"+po+"/send", nil)["purchase"].(map[string]any)["reference"].(string)
	drainLinked(t, w.world)
	if told := w.lastTo(t, cashOn, before); len(told) == 0 || !strings.Contains(told[0].Text, ref) {
		t.Fatalf("the cashier told of the order: %+v", told)
	}
	var seen bool
	for _, x := range cashier.must(200, "GET", "/v1/chat-alerts", nil)["alerts"].([]any) {
		seen = seen || x.(map[string]any)["kind"] == "new_order" && x.(map[string]any)["reference"] == ref
	}
	if !seen {
		t.Fatal("no new order card for the cashier")
	}
	if r := w.say(t, cashOn, "orders"); strings.Contains(r, "not allowed") || strings.Contains(r, "can't") {
		t.Fatalf("the cashier's orders: %q", r)
	}
	cashier.must(200, "POST", "/v1/trade-orders/"+ref+"/accept", nil)
	if d := diaryOf(t, cashier); !hasLine(d, "Accepted order "+ref+" from Ada's Kitchen") {
		t.Fatalf("the accept's diary: %q", d)
	}

	// The shift handed over: the owner told, with the cash counted.
	manager := addMember(t, w.world, seller, "sam", "shift_manager")
	h := manager.must(200, "GET", "/v1/chat-handover", nil)
	sales := h["sales"].(map[string]any)
	if sales["total_kobo"] != float64(23400000) || sales["by_how"].(map[string]any)["cash"] != float64(15600000) || h["orders_open"] != float64(1) {
		t.Fatalf("the shift so far: %v", h)
	}
	before = len(w.graph.Sent(ownerOn))
	out := manager.must(200, "POST", "/v1/chat-handover", map[string]any{"cash_counted_kobo": 15000000, "note": "Till drawer sticks"})
	words := out["words"].(string)
	if !strings.Contains(words, "Sold ₦234,000 in 2 sales") || !strings.Contains(words, "Cash counted: ₦150,000, ₦6,000 short of the ₦156,000 sold") ||
		!strings.Contains(words, "Note: Till drawer sticks") {
		t.Fatalf("the handover: %q", words)
	}
	w.srv.WaitBackground()
	if told := w.lastTo(t, ownerOn, before); len(told) == 0 || !strings.Contains(told[0].Text, "sam handed the shift over") {
		t.Fatalf("the owner told: %+v", told)
	}
	manager.must(400, "POST", "/v1/chat-handover", map[string]any{"cash_counted_kobo": -1})

	// The owner's briefing: from 8am Lagos, not for a cashier.
	lagos, _ := time.LoadLocation("Africa/Lagos")
	at := func(hour int) func() time.Time {
		return func() time.Time {
			n := time.Now().In(lagos)
			return time.Date(n.Year(), n.Month(), n.Day(), hour, 5, 0, 0, lagos)
		}
	}
	w.srv.BriefingClock = at(7)
	if b := seller.must(200, "GET", "/v1/chat-briefing", nil); b["ready"] != false {
		t.Fatalf("before 8am: %v", b)
	}
	w.srv.BriefingClock = at(9)
	b := seller.must(200, "GET", "/v1/chat-briefing", nil)
	if b["ready"] != true || b["put_off"] != false || len(b["sales"].(map[string]any)["week"].([]any)) != 7 || b["owed_kobo"] == nil || b["low"] != float64(0) {
		t.Fatalf("the briefing: %v", b)
	}
	cashier.must(403, "GET", "/v1/chat-briefing", nil)
	seller.must(200, "POST", "/v1/chat-alerts/later", map[string]any{"key": b["key"], "hours": 24})
	if b := seller.must(200, "GET", "/v1/chat-briefing", nil); b["put_off"] != true {
		t.Fatalf("Got it: %v", b)
	}

	// "How did we do this week?": the words, and a chart card.
	said := seller.must(200, "POST", "/v1/taskiem-chat", map[string]any{"text": "how did we do this week?"})["messages"].([]any)
	var reply string
	for _, x := range said {
		if m := x.(map[string]any); m["inbound"] == false {
			reply += m["text"].(string)
		}
	}
	if !strings.Contains(reply, "Sales, the last 7 days: ₦234,000") {
		t.Fatalf("how did we do: %q", reply)
	}
	var chart map[string]any
	for _, x := range seller.must(200, "GET", "/v1/taskiem-chat", nil)["messages"].([]any) {
		if m := x.(map[string]any); m["kind"] == "chart" {
			_ = json.Unmarshal([]byte(m["text"].(string)), &chart)
		}
	}
	if chart == nil || chart["total_kobo"] != float64(23400000) || len(chart["bars"].([]any)) != 7 || chart["highlight"] != float64(-1) {
		t.Fatalf("the chart: %v", chart)
	}
	if r := w.say(t, ownerOn, "how did we do today"); !strings.Contains(r, "Sales today: ₦234,000") {
		t.Fatalf("on WhatsApp: %q", r)
	}
}

// A rider works a shift from chat alone: asks what they have, takes it,
// picks it up and delivers it, the next word to send told each time.
func TestRiderShift(t *testing.T) {
	w, ada := newDispatchWorld(t, "Ada Stores")
	path := strings.TrimPrefix(ada.must(200, "GET", "/v1/riders", nil)["tap_in_url"].(string), "https://"+reachFormHost)
	loc, _ := url.Parse(w.page(t, path+"/whatsapp", nil).Header().Get("Location"))
	const musa = "+2348031110081"
	w.whatsapp(t, musa, loc.Query().Get("text"))
	ada.must(201, "POST", "/v1/riders", map[string]any{"name": "Musa Bello", "phone": musa, "zones": []string{"Lekki"}, "fee_kobo": 100000})
	ref := w.order(t, "none")
	ada.must(200, "POST", "/v1/storefront/orders/"+ref+"/offer", map[string]any{})
	w.tick(t)
	jobs := func(want string) {
		t.Helper()
		w.whatsapp(t, musa, "my deliveries")
		if m := w.graph.Last(t, musa); !strings.Contains(m.Text, "Your deliveries now:") || !strings.Contains(m.Text, ref) || !strings.Contains(m.Text, want) {
			t.Fatalf("my deliveries (%s): %q", want, m.Text)
		}
	}
	jobs("Send ACCEPT or CAN'T")
	w.whatsapp(t, musa, "accept")
	jobs("Send PICKED UP")
	if m := w.graph.Last(t, musa); !strings.Contains(m.Text, "12 Admiralty Way") {
		t.Fatalf("the address once it is theirs: %q", m.Text)
	}
	w.whatsapp(t, musa, "picked up")
	jobs("Send ARRIVED at the door, or DELIVERED")
	w.whatsapp(t, musa, "delivered")
	w.tick(t)
	for _, x := range ada.must(200, "GET", "/v1/deliveries", nil)["deliveries"].([]any) {
		if d := x.(map[string]any); d["reference"] == ref && d["status"] != "delivered" {
			t.Fatalf("delivered from chat: %v", d)
		}
	}
}
