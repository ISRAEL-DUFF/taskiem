package api_test

import (
	"context"
	"strings"
	"testing"
)

// Reactions in the conversation (WhatsApp's): one of six emoji per member
// per message, seen by both businesses with the reactor's first name; a
// change replaces it, and taking it back removes it from both; the other
// side's phone is told once per emoji, not when muted; a first reaction
// counts against the plan's messages per day; a member who may not write
// cannot react; and stars are a member's own.
func TestLinkReactions(t *testing.T) {
	ctx := context.Background()
	w := newWAWorld(t)
	seller, buyer, _, _ := linkedPair(t, w)
	const buyerOn = "+2348010000395"
	w.bind(t, buyer, buyerOn)
	w.say(t, buyerOn, "links")
	viewer := addMember(t, w.world, buyer, "vic", "viewer")
	link := buyer.must(200, "GET", "/v1/link-chats", nil)["conversations"].([]any)[0].(map[string]any)["link"].(string)
	msgs := func(c *client) []map[string]any {
		var out []map[string]any
		for _, m := range c.must(200, "GET", "/v1/link-chats/"+link+"/messages", nil)["messages"].([]any) {
			out = append(out, m.(map[string]any))
		}
		return out
	}
	react := func(c *client, want int, id, emoji string) map[string]any {
		return c.must(want, "PUT", "/v1/link-chats/"+link+"/messages/"+id+"/reaction", map[string]any{"emoji": emoji})
	}
	first := func(c *client) map[string]any { return msgs(c)[0] }

	m := buyer.must(201, "POST", "/v1/link-chats/"+link+"/messages", map[string]any{"text": "Ten bags of rice"})["message"].(map[string]any)
	id := m["id"].(string)

	// The seller reacts: both see it, by first name; the buyer's phone is told.
	before := len(w.graph.Sent(buyerOn))
	out := react(seller, 200, id, "❤️")["reactions"].([]any)
	if len(out) != 1 || out[0].(map[string]any)["emoji"] != "❤️" || out[0].(map[string]any)["count"] != float64(1) || out[0].(map[string]any)["mine"] != true {
		t.Fatalf("the seller's reactions: %v", out)
	}
	seen := first(buyer)["reactions"].([]any)
	if len(seen) != 1 || seen[0].(map[string]any)["mine"] != nil || seen[0].(map[string]any)["by"].([]any)[0] != "Owner" {
		t.Fatalf("the buyer sees: %v", seen)
	}
	if told := w.lastTo(t, buyerOn, before); len(told) != 1 || !strings.Contains(told[0].Text, "[Dangote Stores]") || !strings.Contains(told[0].Text, "Owner reacted ❤️ to “Ten bags of rice”") {
		t.Fatalf("the buyer's phone: %+v", told)
	}

	// Changing it replaces it, and tells again; the same one again is nothing.
	before = len(w.graph.Sent(buyerOn))
	react(seller, 200, id, "🙏")
	react(seller, 200, id, "🙏")
	if r := first(seller)["reactions"].([]any); len(r) != 1 || r[0].(map[string]any)["emoji"] != "🙏" {
		t.Fatalf("changed: %v", r)
	}
	if told := w.lastTo(t, buyerOn, before); len(told) != 1 || !strings.Contains(told[0].Text, "reacted 🙏") {
		t.Errorf("told once for the change: %+v", told)
	}

	// Two people, two emoji; the same emoji counts both.
	react(buyer, 200, id, "👍")
	r := first(seller)["reactions"].([]any)
	if len(r) != 2 {
		t.Fatalf("two reactions: %v", r)
	}
	react(buyer, 200, id, "🙏")
	r = first(buyer)["reactions"].([]any)
	if len(r) != 1 || r[0].(map[string]any)["emoji"] != "🙏" || r[0].(map[string]any)["count"] != float64(2) || r[0].(map[string]any)["mine"] != true {
		t.Fatalf("one emoji, two people: %v", r)
	}

	// Not one emoji (words, markup, two emoji), not a message of this
	// conversation, and a member who may not write.
	for _, bad := range []string{"a", "<b>", "👍👍", "👍 ", ""} {
		if out := react(seller, 400, id, bad); out["error"] == nil {
			t.Errorf("%q is not one emoji: %v", bad, out)
		}
	}
	react(seller, 404, "0192e2e0-0000-7000-8000-000000000999", "👍")
	react(viewer, 403, id, "👍")

	// Taken back: gone from both, and nothing is told.
	before = len(w.graph.Sent(buyerOn))
	if r := seller.must(200, "DELETE", "/v1/link-chats/"+link+"/messages/"+id+"/reaction", nil)["reactions"].([]any); len(r) != 1 || r[0].(map[string]any)["count"] != float64(1) {
		t.Fatalf("taken back: %v", r)
	}
	seller.must(200, "DELETE", "/v1/link-chats/"+link+"/messages/"+id+"/reaction", nil) // none to take back
	if r := first(buyer)["reactions"].([]any); len(r) != 1 || r[0].(map[string]any)["count"] != float64(1) {
		t.Errorf("the buyer's view: %v", r)
	}
	if told := w.lastTo(t, buyerOn, before); len(told) != 0 {
		t.Errorf("taking a reaction back tells no one: %+v", told)
	}

	// A muted conversation tells no phone.
	buyer.must(200, "PUT", "/v1/link-chats/"+link, map[string]any{"muted": true})
	before = len(w.graph.Sent(buyerOn))
	react(seller, 200, id, "😂")
	if told := w.lastTo(t, buyerOn, before); len(told) != 0 {
		t.Errorf("muted, yet told: %+v", told)
	}

	// A reaction counts as a message against the plan's day: with a limit of
	// two (this message-less day, one reaction so far, one more), a third is
	// refused; changing one already given is not a new one.
	if err := w.env.Store.SetLimits(ctx, tenantID(t, seller), map[string]any{"link_messages_daily": 2}, "operator"); err != nil {
		t.Fatal(err)
	}
	m2 := buyer.must(201, "POST", "/v1/link-chats/"+link+"/messages", map[string]any{"text": "And beans"})["message"].(map[string]any)["id"].(string)
	seller.must(201, "POST", "/v1/link-chats/"+link+"/messages", map[string]any{"text": "On its way"}) // 1 message (+ the reaction = 2)
	if code, out := seller.do("PUT", "/v1/link-chats/"+link+"/messages/"+m2+"/reaction", map[string]any{"emoji": "👍"}); code != 429 || out["limit"] != "link_messages_daily" {
		t.Fatalf("over the day's limit: %d %v", code, out)
	}
	react(seller, 200, id, "👍") // a change of the one given: not a new one

	// Any one emoji is a reaction (the "+" of WhatsApp's pill), a flag or a
	// person with a skin tone too; it replaces the one before and both see it.
	for _, e := range []string{"🎉", "🇳🇬", "👩🏽‍💻"} {
		out := react(seller, 200, id, e)["reactions"].([]any)
		mine := ""
		for _, r := range out {
			if r.(map[string]any)["mine"] == true {
				mine = r.(map[string]any)["emoji"].(string)
			}
		}
		if mine != e {
			t.Errorf("%q not kept as mine: %v", e, out)
		}
	}
	react(seller, 200, id, "🙏") // back to what the rest of this test expects
}

// Stars are a member's own: kept, listed newest first across conversations,
// never copied to the other side, and an ended link keeps them.
func TestLinkStars(t *testing.T) {
	w := newWAWorld(t)
	seller, buyer, _, _ := linkedPair(t, w)
	viewer := addMember(t, w.world, buyer, "vic", "viewer")
	link := buyer.must(200, "GET", "/v1/link-chats", nil)["conversations"].([]any)[0].(map[string]any)["link"].(string)
	a := seller.must(201, "POST", "/v1/link-chats/"+link+"/messages", map[string]any{"text": "Rice is back"})["message"].(map[string]any)["id"].(string)
	b := seller.must(201, "POST", "/v1/link-chats/"+link+"/messages", map[string]any{"text": "Price is 45000"})["message"].(map[string]any)["id"].(string)
	star := func(c *client, id string) { c.must(200, "PUT", "/v1/link-chats/"+link+"/messages/"+id+"/star", nil) }
	bodies := func(c *client, path string) []string {
		var out []string
		for _, m := range c.must(200, "GET", path, nil)["messages"].([]any) {
			out = append(out, m.(map[string]any)["body"].(string))
		}
		return out
	}
	starred := func(c *client, path string) []string {
		var out []string
		for _, m := range c.must(200, "GET", path, nil)["messages"].([]any) {
			if m.(map[string]any)["starred"] == true {
				out = append(out, m.(map[string]any)["body"].(string))
			}
		}
		return out
	}

	star(buyer, a)
	star(buyer, b)
	star(buyer, b) // again: still one
	if got := strings.Join(bodies(buyer, "/v1/link-chats/starred"), "|"); got != "Price is 45000|Rice is back" {
		t.Errorf("the buyer's starred, newest first: %s", got)
	}
	if got := starred(buyer, "/v1/link-chats/"+link+"/messages"); len(got) != 2 {
		t.Errorf("marked in the conversation: %v", got)
	}
	// Theirs alone: not the seller's, not another member's of the same business.
	if got := bodies(seller, "/v1/link-chats/starred"); len(got) != 0 {
		t.Errorf("the seller sees the buyer's stars: %v", got)
	}
	if got := starred(seller, "/v1/link-chats/"+link+"/messages"); len(got) != 0 {
		t.Errorf("the seller's thread shows stars: %v", got)
	}
	if got := bodies(viewer, "/v1/link-chats/starred"); len(got) != 0 {
		t.Errorf("another member sees the buyer's stars: %v", got)
	}
	// A viewer may star (it is theirs, and writes nothing to the trade).
	star(viewer, a)
	if got := bodies(viewer, "/v1/link-chats/starred"); len(got) != 1 {
		t.Errorf("the viewer's own: %v", got)
	}
	// Unstarred.
	buyer.must(200, "DELETE", "/v1/link-chats/"+link+"/messages/"+a+"/star", nil)
	if got := strings.Join(bodies(buyer, "/v1/link-chats/starred"), "|"); got != "Price is 45000" {
		t.Errorf("after unstarring: %s", got)
	}
	// Not a message of this conversation.
	buyer.must(404, "PUT", "/v1/link-chats/"+link+"/messages/0192e2e0-0000-7000-8000-000000000999/star", nil)
	// A message deleted for me is not in the list.
	buyer.must(204, "POST", "/v1/link-chats/"+link+"/messages/"+b+"/hide", nil)
	if got := bodies(buyer, "/v1/link-chats/starred"); len(got) != 0 {
		t.Errorf("a deleted message stays starred: %v", got)
	}
}
