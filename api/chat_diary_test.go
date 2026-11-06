package api_test

import (
	"strings"
	"testing"
)

// Diary lines (docs/spec/chat-tools.md §3): a change made with a chat tool
// is a line where the tool was used, on this side only, and is told on the
// person's own WhatsApp.
func TestChatDiary(t *testing.T) {
	w := newWAWorld(t)
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
