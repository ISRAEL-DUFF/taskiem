package api_test

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/israel-duff/taskiem/engine/invoicing"
	"github.com/israel-duff/taskiem/engine/sender"
	"github.com/israel-duff/taskiem/engine/whatsapp/whatsapptest"
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

	// A reply quoting it on WhatsApp goes into the conversation, quoting it.
	var wamid string
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT message_id FROM link_chat_sent WHERE pair_id = $1 AND channel = 'whatsapp'`, m["id"]).Scan(&wamid); err != nil {
		t.Fatalf("the message sent to the phone was not kept: %v", err)
	}
	quoted := whatsapptest.Text("Lovely, keep 20 for us")
	quoted["context"] = map[string]any{"from": "15550001111", "id": wamid}
	if got := w.send(t, buyerOn, quoted); len(got) != 1 || !strings.Contains(got[0].Text, "Sent to *Dangote Stores*") {
		t.Fatalf("a quoted reply on WhatsApp: %+v", got)
	}
	if got := seller.must(200, "GET", "/v1/link-chats/"+link+"/messages", nil)["messages"].([]any); len(got) != 2 ||
		got[1].(map[string]any)["body"] != "Lovely, keep 20 for us" || got[1].(map[string]any)["reply"].(map[string]any)["pair_id"] != m["id"] {
		t.Fatalf("the quoted reply on the seller's side: %v", got)
	}
	// A reply quoting anything else is not taken for one.
	other := whatsapptest.Text("links")
	other["context"] = map[string]any{"from": "15550001111", "id": "wamid.unknown"}
	if got := w.send(t, buyerOn, other); len(got) != 1 || strings.Contains(got[0].Text, "Sent to") {
		t.Errorf("an unknown quote: %+v", got)
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
	if got := seller.must(200, "GET", "/v1/link-chats/"+link+"/messages", nil)["messages"].([]any); len(got) != 3 ||
		got[2].(map[string]any)["reply"].(map[string]any)["mine"] != true {
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

	// The seller settles the query by taking an amount off: the invoice is
	// amended, the bill follows, and the thread says so.
	seller.must(400, "POST", "/v1/invoices/"+x["id"].(string)+"/settle-query", map[string]any{"how": "discount", "amount_kobo": 900000})
	seller.must(200, "POST", "/v1/invoices/"+x["id"].(string)+"/settle-query", map[string]any{"how": "discount", "amount_kobo": 900000, "note": "Two bags were torn"})
	seller.must(409, "POST", "/v1/invoices/"+x["id"].(string)+"/settle-query", map[string]any{"how": "discount", "amount_kobo": 900000, "note": "again"})
	if _, err := w.srv.LinkMirrorTick(ctx); err != nil {
		t.Fatal(err)
	}
	w.srv.WaitBackground()
	if b := buyer.must(200, "GET", "/v1/bills/"+number, nil); b["due_kobo"] != float64(89100000) || b["disputed"] != false {
		t.Errorf("the bill after the query was settled: %v", b)
	}
	if got := thread(buyer, "bill"); len(got) != 5 || got[4] != "card:resolved" {
		t.Errorf("the thread after settling: %v", got)
	}
	last := buyer.must(200, "GET", "/v1/link-chats/thread?doc=bill&reference="+number, nil)["messages"].([]any)
	if c := last[len(last)-1].(map[string]any)["card"].(map[string]any); c["note"] != "Took ₦9,000 off: Two bags were torn" {
		t.Errorf("the settled card: %v", c)
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
	if got := buyer.must(200, "GET", "/v1/link-chats/search?q=torn", nil)["messages"].([]any); len(got) != 2 { // the query, and its settling
		t.Errorf("search across: %v", got)
	}

	// Ended, the conversation stays, and takes no more.
	buyer.must(200, "POST", "/v1/links/"+link+"/end", nil)
	buyer.must(409, "POST", "/v1/link-chats/"+link+"/messages", map[string]any{"text": "hello?"})
	if c := buyer.must(200, "GET", "/v1/link-chats", nil)["conversations"].([]any); len(c) != 1 || c[0].(map[string]any)["state"] != "ended" {
		t.Errorf("ended: %v", c)
	}
}

// A conversation is shown around one message (a quote, a search result):
// that message and those either side of it, oldest first; a message that
// is not in this conversation is not found.
func TestLinkMessagesAround(t *testing.T) {
	w := newWAWorld(t)
	seller, buyer, _, _ := linkedPair(t, w)
	link := buyer.must(200, "GET", "/v1/link-chats", nil)["conversations"].([]any)[0].(map[string]any)["link"].(string)
	var ids []string
	for _, text := range []string{"one", "two", "three", "four", "five", "six", "seven"} {
		m := seller.must(201, "POST", "/v1/link-chats/"+link+"/messages", map[string]any{"text": text})["message"].(map[string]any)
		ids = append(ids, m["id"].(string))
	}
	bodies := func(out map[string]any) []string {
		var got []string
		for _, m := range out["messages"].([]any) {
			got = append(got, m.(map[string]any)["body"].(string))
		}
		return got
	}
	// Centred on "four" with room for four: two older, it, and two newer
	// (limit 4: half 2, so "two", "three", "four", "five", "six").
	got := bodies(buyer.must(200, "GET", "/v1/link-chats/"+link+"/messages?around="+ids[3]+"&limit=4", nil))
	if want := "two three four five six"; strings.Join(got, " ") != want {
		t.Errorf("around four: %v, want %s", got, want)
	}
	// Near the start: nothing older to show.
	got = bodies(buyer.must(200, "GET", "/v1/link-chats/"+link+"/messages?around="+ids[0]+"&limit=4", nil))
	if want := "one two three"; strings.Join(got, " ") != want {
		t.Errorf("around one: %v, want %s", got, want)
	}
	// The seller's own copy has the same message by the same id.
	got = bodies(seller.must(200, "GET", "/v1/link-chats/"+link+"/messages?around="+ids[6]+"&limit=4", nil))
	if want := "five six seven"; strings.Join(got, " ") != want {
		t.Errorf("seller around seven: %v, want %s", got, want)
	}
	// Following: what is newer than a message, oldest first, a page at a
	// time, so a reader away for longer than a page misses none of it.
	var times []string
	for _, m := range buyer.must(200, "GET", "/v1/link-chats/"+link+"/messages", nil)["messages"].([]any) {
		times = append(times, m.(map[string]any)["created_at"].(string))
	}
	got = bodies(buyer.must(200, "GET", "/v1/link-chats/"+link+"/messages?after="+times[1]+"&limit=2", nil))
	if want := "three four"; strings.Join(got, " ") != want {
		t.Errorf("after two, two at a time: %v, want %s", got, want)
	}
	// Not a message here, and not an id.
	buyer.must(404, "GET", "/v1/link-chats/"+link+"/messages?around=0192e2e0-0000-7000-8000-000000000999", nil)
	buyer.must(400, "GET", "/v1/link-chats/"+link+"/messages?around=nope", nil)
}

// A send repeated with the same client id (the chat's Retry after a dropped
// connection) is the same message: answered with the first, never sent
// again: one message in each conversation, one told to the phone, and, for
// a file, one copy stored on each side.
func TestLinkSendClientID(t *testing.T) {
	w := newWAWorld(t)
	store := newProjectStorage(t)
	w.srv.Media = store.service()
	w.srv.Media.Scanner = chatScanner{}
	seller, buyer, sellerID, buyerID := linkedPair(t, w)
	const buyerOn = "+2348010000394"
	w.bind(t, buyer, buyerOn)
	w.say(t, buyerOn, "links")
	link := buyer.must(200, "GET", "/v1/link-chats", nil)["conversations"].([]any)[0].(map[string]any)["link"].(string)
	count := func(c *client) int {
		return len(c.must(200, "GET", "/v1/link-chats/"+link+"/messages", nil)["messages"].([]any))
	}

	const id = "0192e2e0-0000-7000-8000-0000000000a1"
	before := len(w.graph.Sent(buyerOn))
	first := seller.must(201, "POST", "/v1/link-chats/"+link+"/messages", map[string]any{"text": "Ten bags", "client_id": id})["message"].(map[string]any)
	again := seller.must(200, "POST", "/v1/link-chats/"+link+"/messages", map[string]any{"text": "Ten bags", "client_id": id})["message"].(map[string]any)
	if again["id"] != first["id"] {
		t.Fatalf("the repeat is the same message: %v then %v", first["id"], again["id"])
	}
	if a, b := count(seller), count(buyer); a != 1 || b != 1 {
		t.Fatalf("one message each side: seller %d, buyer %d", a, b)
	}
	if told := w.lastTo(t, buyerOn, before); len(told) != 1 {
		t.Errorf("the phone is told once: %+v", told)
	}
	// Another id is another message; no id, as before.
	seller.must(201, "POST", "/v1/link-chats/"+link+"/messages", map[string]any{"text": "Ten bags", "client_id": "0192e2e0-0000-7000-8000-0000000000a2"})
	seller.must(201, "POST", "/v1/link-chats/"+link+"/messages", map[string]any{"text": "Ten bags"})
	if a := count(seller); a != 3 {
		t.Errorf("three messages: %d", a)
	}
	// Not an id.
	seller.must(400, "POST", "/v1/link-chats/"+link+"/messages", map[string]any{"text": "x", "client_id": "nope"})

	// A file: one copy stored each side however many times it is sent.
	const fid = "0192e2e0-0000-7000-8000-0000000000b1"
	fields := map[string]string{"text": "The new bags", "client_id": fid}
	code, out := seller.sendFile(t, link, "bags.jpg", photo(t), fields)
	if code != 201 {
		t.Fatalf("file: %d %v", code, out)
	}
	code, again2 := seller.sendFile(t, link, "bags.jpg", photo(t), fields)
	if code != 200 || again2["message"].(map[string]any)["id"] != out["message"].(map[string]any)["id"] {
		t.Fatalf("the file repeated: %d %v", code, again2)
	}
	if s, b := store.in("prj_"+sellerID), store.in("prj_"+buyerID); len(s) != 1 || len(b) != 1 {
		t.Errorf("one copy each side: seller %v, buyer %v", s, b)
	}
	if a, b := count(seller), count(buyer); a != 4 || b != 4 {
		t.Errorf("four messages each: seller %d, buyer %d", a, b)
	}
	// The buyer cannot use the seller's id to learn of the message: its own
	// client ids are its own.
	buyer.must(201, "POST", "/v1/link-chats/"+link+"/messages", map[string]any{"text": "Thanks", "client_id": id})
	if b := count(buyer); b != 5 {
		t.Errorf("the buyer's id is its own: %d", b)
	}
}
