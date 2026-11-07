package api_test

import (
	"strings"
	"testing"
	"time"
)

// Diary lines (docs/spec/chat-tools.md §3): a change made with a chat tool
// is a line where the tool was used, on this side only, and is told on the
// person's own WhatsApp.
func TestChatDiary(t *testing.T) {
	w := newWAWorld(t)
	w.srv.DiaryWindow = 50 * time.Millisecond
	seller, buyer, _, _ := linkedPair(t, w)
	const buyerOn = "+2348010000731"
	w.bind(t, buyer, buyerOn)
	w.say(t, buyerOn, "links")
	link := buyer.must(200, "GET", "/v1/link-chats", nil)["conversations"].([]any)[0].(map[string]any)["link"].(string)
	line := map[string]any{"where": "taskiem", "text": "Price of Rice, 50kg bag: ₦76,000 → ₦78,000"}

	// Off: refused.
	buyer.must(409, "POST", "/v1/chat-diary", line)
	buyer.must(200, "PUT", "/v1/settings/chat-tools", map[string]any{"chat_tools": true})

	// In the Taskiem conversation: a line of its own kind, and told on WhatsApp.
	before := len(w.graph.Sent(buyerOn))
	got := buyer.must(201, "POST", "/v1/chat-diary", line)["line"].(map[string]any)
	if got["kind"] != "diary" || got["text"] != line["text"] {
		t.Fatalf("the line: %v", got)
	}
	time.Sleep(150 * time.Millisecond)
	w.srv.WaitBackground()
	told := w.graph.Sent(buyerOn)[before:]
	if len(told) != 1 || !strings.Contains(told[0].Text, "✓ Price of Rice, 50kg bag: ₦76,000 → ₦78,000") {
		t.Fatalf("told: %+v", told)
	}
	hist := buyer.must(200, "GET", "/v1/taskiem-chat", nil)["messages"].([]any)
	last := hist[len(hist)-1].(map[string]any)
	if last["kind"] != "diary" || last["inbound"] != false || last["text"] != line["text"] {
		t.Fatalf("in the history: %v", last)
	}

	// In a conversation with a business: our side only.
	got = buyer.must(201, "POST", "/v1/chat-diary", map[string]any{"where": link, "text": "Stock in: Rice, 25kg bag +20"})["line"].(map[string]any)
	if got["kind"] != "diary" || got["mine"] != true || got["body"] != "Stock in: Rice, 25kg bag +20" {
		t.Fatalf("the line: %v", got)
	}
	ours := buyer.must(200, "GET", "/v1/link-chats/"+link+"/messages", nil)["messages"].([]any)
	if m := ours[len(ours)-1].(map[string]any); m["kind"] != "diary" {
		t.Fatalf("our side: %v", m)
	}
	for _, x := range seller.must(200, "GET", "/v1/link-chats/"+link+"/messages", nil)["messages"].([]any) {
		if x.(map[string]any)["kind"] == "diary" || strings.Contains(x.(map[string]any)["body"].(string), "Stock in") {
			t.Fatalf("the other business saw a diary line: %v", x)
		}
	}
	// Nothing can be done to it that reaches the other side.
	id := got["id"].(string)
	buyer.must(404, "POST", "/v1/link-chats/"+link+"/messages/"+id+"/reactions", map[string]any{"emoji": "👍"})
	buyer.must(400, "POST", "/v1/link-chats/"+link+"/messages", map[string]any{"text": "about that", "reply_to": id})

	// Words and who may.
	buyer.must(400, "POST", "/v1/chat-diary", map[string]any{"where": "taskiem", "text": " "})
	buyer.must(400, "POST", "/v1/chat-diary", map[string]any{"where": "taskiem", "text": strings.Repeat("x", 301)})
	buyer.must(400, "POST", "/v1/chat-diary", map[string]any{"where": "elsewhere", "text": "x"})
	viewer := addMember(t, w.world, buyer, "vic", "viewer")
	viewer.must(403, "POST", "/v1/chat-diary", map[string]any{"where": link, "text": "x"})
	viewer.must(201, "POST", "/v1/chat-diary", map[string]any{"where": "taskiem", "text": "Looked at the prices"})
}

// Changes made anywhere (chat_changes.go): with chat tools on, a price,
// visibility, a new product, stock in, a count and a price list change on
// the pages are lines of the person's Taskiem conversation, told on their
// WhatsApp in one message when made together, and not at all once their
// "changes" setting is off; nothing with chat tools off.
func TestChangesTold(t *testing.T) {
	w := shopWorld(t)
	withStock(w.world)
	w.srv.DiaryWindow = 100 * time.Millisecond
	owner := w.tenant(t, "Bayo Rice", "bayo@changes.test")
	const on = "+2348010000741"
	w.bind(t, owner, on)
	w.say(t, on, "links")
	diary := func() []string {
		t.Helper()
		var out []string
		for _, x := range owner.must(200, "GET", "/v1/taskiem-chat", nil)["messages"].([]any) {
			if m := x.(map[string]any); m["kind"] == "diary" {
				out = append(out, m["text"].(string))
			}
		}
		return out
	}
	told := func() []string {
		t.Helper()
		time.Sleep(250 * time.Millisecond)
		w.srv.WaitBackground()
		var out []string
		for _, m := range w.graph.Sent(on) {
			if strings.Contains(m.Text, "✓") {
				out = append(out, m.Text)
			}
		}
		return out
	}

	// Off: changes are not told.
	owner.must(200, "POST", "/v1/stock/setup", nil)
	owner.must(201, "POST", "/v1/stock/items", map[string]any{"name": "Beans", "quantity": 1, "unit": "bag", "price_kobo": 1000000}, "Idempotency-Key", "b")
	if got := diary(); len(got) != 0 {
		t.Fatalf("told with chat tools off: %q", got)
	}

	owner.must(200, "PUT", "/v1/settings/chat-tools", map[string]any{"chat_tools": true})
	rice := owner.must(201, "POST", "/v1/stock/items", map[string]any{"name": "Rice", "quantity": 3, "unit": "bag", "price_kobo": 4500000},
		"Idempotency-Key", "r")["item"].(map[string]any)["id"].(string)
	owner.must(200, "PATCH", "/v1/stock/items/"+rice, map[string]any{"price_kobo": 4600000, "available": false})
	owner.must(200, "POST", "/v1/stock/movements", map[string]any{"item_id": rice, "reason": "received", "change": 5}, "Idempotency-Key", "m1")
	owner.must(200, "POST", "/v1/stock/movements", map[string]any{"item_id": rice, "reason": "count", "count": 10}, "Idempotency-Key", "m2")
	list := owner.must(201, "POST", "/v1/price-lists", map[string]any{"name": "Trade prices"})["list"].(map[string]any)["id"].(string)
	owner.must(200, "PUT", "/v1/price-lists/"+list+"/items", map[string]any{"items": []any{map[string]any{"item_id": rice, "price_kobo": 4200000, "min_quantity": 5}}})
	owner.must(200, "PUT", "/v1/price-lists/"+list+"/items", map[string]any{"items": []any{map[string]any{"item_id": rice, "price_kobo": 4300000, "min_quantity": 5}}})

	want := []string{
		"New product: Rice, ₦45,000 a bag",
		"Price of Rice: ₦45,000 → ₦46,000",
		"Rice is hidden from your storefront",
		"Stock in: Rice +5 bags (now 8 bags)",
		"Stock of Rice counted: 8 bags → 10 bags",
		"Price list Trade prices now holds 1 item",
		"Price list Trade prices: Rice ₦42,000 → ₦43,000",
	}
	if got := diary(); strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("the diary:\n got %q\nwant %q", got, want)
	}
	// Told together on WhatsApp, once.
	got := told()
	if len(got) != 1 || !strings.Contains(got[0], "✓ 7 changes:") || !strings.Contains(got[0], "• Stock in: Rice +5 bags (now 8 bags)") {
		t.Fatalf("told on WhatsApp: %q", got)
	}

	// The "changes" setting off: kept in the conversation, not told.
	owner.must(200, "PUT", "/v1/me/notification-settings", map[string]any{"settings": []any{map[string]any{"group": "changes", "chat": false, "email": false}}})
	owner.must(200, "PATCH", "/v1/stock/items/"+rice, map[string]any{"available": true})
	if d := diary(); d[len(d)-1] != "Rice is shown on your storefront again" {
		t.Fatalf("the last line: %q", d[len(d)-1])
	}
	if again := told(); len(again) != 1 {
		t.Fatalf("told with the setting off: %q", again)
	}
}
