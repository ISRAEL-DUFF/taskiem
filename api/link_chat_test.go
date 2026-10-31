package api_test

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/israel-duff/taskiem/engine/invoicing"
	"github.com/israel-duff/taskiem/engine/sender"
)

// The conversation between linked businesses (L3, §5.5, §8): a message is
// in both businesses at once and on the other side's phones; it is read
// with a double tick; a reply quotes it; a bill arrives as a card in both
// conversations, and a reply quoting the card (from the web, or "ask …
// about INV-…" from the phone) is the bill's thread on both sides; a
// query is a card too. A member without chat.trade reads but cannot
// write; a muted conversation tells no phone; search finds what was said.
func TestLinkConversation(t *testing.T) {
	ctx := context.Background()
	w := newWAWorld(t)
	w.srv.Invoices = &invoicing.Invoices{Pool: w.env.DB.App, Vault: w.env.Vault, PageBase: "https://forms.taskiem.test",
		Messages: &sender.Queue{Vault: w.env.Vault, HashKey: bytes.Repeat([]byte{8}, 32)}, Signal: w.env.Store.DeliverSignalOnce}
	seller, buyer, _, buyerID := linkedPair(t, w)
	const sellerOn, buyerOn = "+2348010000391", "+2348010000392"
	w.bind(t, seller, sellerOn)
	w.bind(t, buyer, buyerOn)
	w.say(t, sellerOn, "links")
	w.say(t, buyerOn, "links")
	viewer := addMember(t, w.world, buyer, "vic", "viewer")
	link := buyer.must(200, "GET", "/v1/link-chats", nil)["conversations"].([]any)[0].(map[string]any)["link"].(string)
	sellerLink := seller.must(200, "GET", "/v1/link-chats", nil)["conversations"].([]any)[0].(map[string]any)["link"].(string)
	if link != sellerLink {
		t.Fatalf("one link, two sides: %s %s", link, sellerLink)
	}

	// The seller writes: it is in both conversations, and on the buyer's phone.
	before := len(w.graph.Sent(buyerOn))
	m := seller.must(201, "POST", "/v1/link-chats/"+link+"/messages", map[string]any{"text": "Good morning, rice is back in stock"})["message"].(map[string]any)
	if m["mine"] != true || m["sender_name"] != "Owner" || m["read_at"] != nil {
		t.Fatalf("sent: %v", m)
	}
	if told := w.lastTo(t, buyerOn, before); len(told) != 1 || !strings.Contains(told[0].Text, "[Dangote Stores]") || !strings.Contains(told[0].Text, "Owner: Good morning, rice is back in stock") {
		t.Errorf("the buyer's phone: %+v", told)
	}
	conv := buyer.must(200, "GET", "/v1/link-chats", nil)["conversations"].([]any)[0].(map[string]any)
	if conv["unread"] != float64(1) || conv["name"] != "Dangote Stores" || conv["last"].(map[string]any)["body"] != "Good morning, rice is back in stock" {
		t.Fatalf("the buyer's list: %v", conv)
	}
	got := buyer.must(200, "GET", "/v1/link-chats/"+link+"/messages", nil)["messages"].([]any)
	if len(got) != 1 || got[0].(map[string]any)["mine"] != false || got[0].(map[string]any)["id"] != m["id"] {
		t.Fatalf("the buyer's copy: %v", got)
	}

	// Read: the seller's double tick.
	if r := viewer.must(200, "POST", "/v1/link-chats/"+link+"/read", nil); r["read"] != float64(1) {
		t.Fatalf("read: %v", r)
	}
	if got := seller.must(200, "GET", "/v1/link-chats/"+link+"/messages", nil)["messages"].([]any); got[0].(map[string]any)["read_at"] == nil {
		t.Errorf("the double tick: %v", got)
	}

	// A member without chat.trade reads but does not write; a reply quotes.
	viewer.must(403, "POST", "/v1/link-chats/"+link+"/messages", map[string]any{"text": "hello"})
	buyer.must(400, "POST", "/v1/link-chats/"+link+"/messages", map[string]any{"text": "  "})
	r := buyer.must(201, "POST", "/v1/link-chats/"+link+"/messages", map[string]any{"text": "Send 20 bags", "reply_to": m["id"]})["message"].(map[string]any)
	if q := r["reply"].(map[string]any); q["pair_id"] != m["id"] || q["mine"] != false || q["body"] != "Good morning, rice is back in stock" {
		t.Fatalf("quoted: %v", r)
	}
	if got := seller.must(200, "GET", "/v1/link-chats/"+link+"/messages", nil)["messages"].([]any); len(got) != 2 ||
		got[1].(map[string]any)["reply"].(map[string]any)["mine"] != true {
		t.Fatalf("the reply on the seller's side: %v", got)
	}

	// A bill arrives as a card in both conversations.
	x := seller.must(201, "POST", "/v1/invoices", map[string]any{"customer": map[string]any{"name": "Ada's Kitchen", "key": "link:" + buyerID},
		"lines": []map[string]any{{"description": "Rice, 50kg", "quantity": 20, "unit_kobo": 4500000}}}, "Idempotency-Key", "c-1")
	number := x["number"].(string)
	seller.must(200, "POST", "/v1/invoices/"+x["id"].(string)+"/send", nil, "Idempotency-Key", "s-1")
	if _, err := w.srv.LinkMirrorTick(ctx); err != nil {
		t.Fatal(err)
	}
	w.srv.WaitBackground()
	card := func(c *client, doc string) map[string]any {
		t.Helper()
		for _, x := range c.must(200, "GET", "/v1/link-chats/"+link+"/messages", nil)["messages"].([]any) {
			if m := x.(map[string]any); m["kind"] == "card" && m["card"].(map[string]any)["doc"] == doc {
				return m
			}
		}
		t.Fatalf("no %s card", doc)
		return nil
	}
	bc, ic := card(buyer, "bill"), card(seller, "invoice")
	if bc["id"] != ic["id"] || bc["mine"] != false || ic["mine"] != true || bc["card"].(map[string]any)["reference"] != number ||
		bc["card"].(map[string]any)["event"] != "bill" || bc["card"].(map[string]any)["amount_kobo"] != float64(90000000) {
		t.Fatalf("the cards: %v %v", bc, ic)
	}

	// A reply quoting the card is the bill's thread, on both sides; so is
	// "ask … about INV-…" from the phone; a query is a card in it too.
	buyer.must(201, "POST", "/v1/link-chats/"+link+"/messages", map[string]any{"text": "Can we pay half now?", "reply_to": bc["id"]})
	if r := w.say(t, buyerOn, "ask dangote about "+strings.ToLower(number)+" and half on Friday"); !strings.Contains(r, "Sent to *Dangote Stores*") {
		t.Fatalf("ask: %q", r)
	}
	buyer.must(200, "POST", "/v1/bills/"+number+"/query", map[string]any{"reason": "Two bags were torn"})
	w.srv.WaitBackground()
	thread := func(c *client, doc string) []string {
		t.Helper()
		var out []string
		for _, x := range c.must(200, "GET", "/v1/link-chats/thread?doc="+doc+"&reference="+number, nil)["messages"].([]any) {
			m := x.(map[string]any)
			if m["kind"] == "card" {
				out = append(out, "card:"+m["card"].(map[string]any)["event"].(string))
			} else {
				out = append(out, m["body"].(string))
			}
		}
		return out
	}
	want := []string{"card:bill", "Can we pay half now?", "and half on Friday", "card:queried"}
	for _, side := range []struct {
		c   *client
		doc string
	}{{buyer, "bill"}, {seller, "invoice"}} {
		if got := thread(side.c, side.doc); strings.Join(got, "|") != strings.Join(want, "|") {
			t.Errorf("the %s's thread: %v", side.doc, got)
		}
	}
	if r := w.say(t, buyerOn, "ask nobody-at-all hello"); strings.Contains(r, "Sent to") {
		t.Errorf("ask nobody: %q", r)
	}

	// Muted, the seller's phone is not told; archived, it says so.
	seller.must(200, "PUT", "/v1/link-chats/"+link, map[string]any{"muted": true, "archived": true})
	before = len(w.graph.Sent(sellerOn))
	buyer.must(201, "POST", "/v1/link-chats/"+link+"/messages", map[string]any{"text": "Are you there?"})
	if told := w.lastTo(t, sellerOn, before); len(told) != 0 {
		t.Errorf("muted, told: %+v", told)
	}
	if c := seller.must(200, "GET", "/v1/link-chats", nil)["conversations"].([]any)[0].(map[string]any); c["muted"] != true || c["archived"] != true {
		t.Errorf("settings: %v", c)
	}
	viewer.must(403, "PUT", "/v1/link-chats/"+link, map[string]any{"muted": true})

	// Search: in a conversation, and across them.
	if got := seller.must(200, "GET", "/v1/link-chats/"+link+"/messages?q=half", nil)["messages"].([]any); len(got) != 2 {
		t.Errorf("search in: %v", got)
	}
	if got := buyer.must(200, "GET", "/v1/link-chats/search?q=torn", nil)["messages"].([]any); len(got) != 1 {
		t.Errorf("search across: %v", got)
	}

	// Ended, the conversation stays, and takes no more.
	buyer.must(200, "POST", "/v1/links/"+link+"/end", nil)
	buyer.must(409, "POST", "/v1/link-chats/"+link+"/messages", map[string]any{"text": "hello?"})
	if c := buyer.must(200, "GET", "/v1/link-chats", nil)["conversations"].([]any); len(c) != 1 || c[0].(map[string]any)["state"] != "ended" {
		t.Errorf("ended: %v", c)
	}
}
