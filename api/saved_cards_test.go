package api_test

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

// Saved things shared in chats (saved objects S1, part 3; §6.2): a person's
// own @mum shared with a friend as a card, after a yes to sharing the
// account number, the friend unable to forward it, and only one's own
// shared with people; a business's account shared with a customer in the
// app as a card and on WhatsApp as plain lines; each share recorded.
func TestSavedCards(t *testing.T) {
	ctx := context.Background()
	w := newPeopleWorld(t)
	ada, adaID := w.person(t, "+2348031120081", "Ada Obi")
	bola, bolaID := w.person(t, "+2348031120082", "Bola Ade")
	adaChat := chatWith(t, ada, bolaID)
	send(t, ada, adaChat, "Hi")
	bolaChat := requestOf(t, bola)
	bola.must(200, "POST", "/v1/me/people/"+bolaChat+"/accept", nil)

	mum := ada.must(201, "POST", "/v1/me/saved", map[string]any{"name": "mum", "title": "Mum", "bank_name": "Guaranty Trust Bank", "account_number": "0123452222"})["saved"].(map[string]any)
	share := func(confirm bool) map[string]any {
		return map[string]any{"card": map[string]any{"type": "saved", "saved": map[string]any{"id": mum["id"], "owner": "mine", "confirm": confirm}}}
	}
	// The account number: asked first.
	if e := ada.must(409, "POST", "/v1/me/people/"+adaChat+"/messages", share(false)); e["confirm_share"] != true || e["error"] != "Share Mum's account details with them?" {
		t.Fatalf("asked: %v", e)
	}
	m := ada.must(201, "POST", "/v1/me/people/"+adaChat+"/messages", share(true))
	card := m["card"].(map[string]any)
	if m["kind"] != "card" || card["type"] != "saved" || card["title"] != "Mum" {
		t.Fatalf("shared: %v", m)
	}
	// Ada's own copy shows the number masked, in the answer and the list.
	if strings.Contains(fmt.Sprint(card), "0123452222") || !strings.Contains(fmt.Sprint(card), "••2222") {
		t.Fatalf("ada's copy: %v", card)
	}
	for _, x := range messagesOf(t, ada, adaChat) {
		if strings.Contains(fmt.Sprint(x["card"]), "0123452222") {
			t.Fatalf("ada's list: %v", x)
		}
	}
	// A saved list says what asks first.
	for _, x := range ada.must(200, "GET", "/v1/me/saved", nil)["saved"].([]any) {
		if x.(map[string]any)["id"] == mum["id"] && x.(map[string]any)["share_sensitive"] != true {
			t.Fatalf("mum asks first: %v", x)
		}
	}
	// A text goes in as words, not a card.
	note := ada.must(201, "POST", "/v1/me/saved", map[string]any{"name": "wifi", "title": "Wifi", "kind": "text", "details": map[string]any{"body": "The code is 1234"}})["saved"].(map[string]any)
	ada.must(400, "POST", "/v1/me/people/"+adaChat+"/messages", map[string]any{"card": map[string]any{"type": "saved", "saved": map[string]any{"id": note["id"], "owner": "mine"}}})
	// Bola has it in full, with Save this's fields; not to pass on.
	var got map[string]any
	for _, x := range messagesOf(t, bola, bolaChat) {
		if c, ok := x["card"].(map[string]any); ok && c["type"] == "saved" {
			got = x
		}
	}
	if got == nil {
		t.Fatal("bola has no card")
	}
	sc := got["card"].(map[string]any)["saved"].(map[string]any)
	if sc["details"].(map[string]any)["account_number"] != "0123452222" || sc["sensitive"] != true || !strings.Contains(fmt.Sprint(sc["lines"]), "0123452222") {
		t.Fatalf("bola's card: %v", sc)
	}
	bola.must(409, "POST", "/v1/me/people/forward", map[string]any{"chat_id": bolaChat, "message_id": got["id"], "to": []string{adaID.String()}})
	// Only one's own, and only live ones.
	ada.must(400, "POST", "/v1/me/people/"+adaChat+"/messages", map[string]any{"card": map[string]any{"type": "saved", "saved": map[string]any{"id": mum["id"], "owner": "business"}}})
	// One of Ada's own kinds: what is never shared left out of the card.
	pet := ada.must(201, "POST", "/v1/me/saved-kinds", map[string]any{"name": "Pet", "fields": []map[string]any{
		{"label": "Breed", "type": "text"}, {"label": "Microchip", "type": "text", "never_share": true}, {"label": "Vet's phone", "type": "phone"}}})["kind"].(map[string]any)
	rex := ada.must(201, "POST", "/v1/me/saved", map[string]any{"name": "rex", "title": "Rex", "kind": "custom", "kind_id": pet["id"],
		"values": map[string]any{"breed": "Boerboel", "microchip": "985112003456789", "vets_phone": "0803 555 0101"}})["saved"].(map[string]any)
	petShare := map[string]any{"card": map[string]any{"type": "saved", "saved": map[string]any{"id": rex["id"], "owner": "mine", "confirm": true}}}
	pc := ada.must(201, "POST", "/v1/me/people/"+adaChat+"/messages", petShare)["card"].(map[string]any)["saved"].(map[string]any)
	if pc["kind"] != "custom" || pc["kind_name"] != "Pet" || strings.Contains(fmt.Sprint(pc), "985112003456789") || !strings.Contains(fmt.Sprint(pc["lines"]), "Boerboel") {
		t.Fatalf("a pet's card: %v", pc)
	}
	ada.must(204, "DELETE", "/v1/me/saved/"+mum["id"].(string), nil)
	ada.must(404, "POST", "/v1/me/people/"+adaChat+"/messages", share(true))
	var shared int
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT count(*) FROM saved_object_events WHERE person_id = $1 AND action = 'shared'`, adaID).Scan(&shared); err != nil || shared != 2 {
		t.Fatalf("shares logged: %d %v", shared, err)
	}

	// The business's account, to its customers.
	acct := w.owner.must(201, "POST", "/v1/saved", map[string]any{"name": "bank-account", "title": "Our account", "bank_name": "Zenith Bank", "account_number": "1234567890"})["saved"].(map[string]any)
	reply := func(thread string) map[string]any {
		return w.owner.must(201, "POST", "/v1/customer-chats/"+thread+"/messages", map[string]any{"card": map[string]any{"type": "saved",
			"saved": map[string]any{"id": acct["id"], "owner": "business", "confirm": true}}})
	}
	// In the app: a card.
	tolu, _ := w.personIn(t, "+2348031120083")
	toluChat := tolu.must(201, "POST", "/v1/me/chats", map[string]any{"business_id": w.biz})["id"].(string)
	tolu.must(201, "POST", "/v1/me/chats/"+toluChat+"/messages", map[string]any{"text": "How do I pay?"})
	inApp := reply(w.threadOf(t, personID(t, tolu)))
	// The business's own copy is masked; the customer's has the number.
	if c, _ := inApp["card"].(map[string]any); c == nil || c["type"] != "saved" || strings.Contains(fmt.Sprint(c), "1234567890") || !strings.Contains(fmt.Sprint(c), "••7890") {
		t.Fatalf("in the app: %v", inApp)
	}
	var seen bool
	for _, x := range tolu.must(200, "GET", "/v1/me/chats/"+toluChat+"/messages", nil)["messages"].([]any) {
		if c, ok := x.(map[string]any)["card"].(map[string]any); ok && c["type"] == "saved" {
			seen = c["saved"].(map[string]any)["details"].(map[string]any)["account_number"] == "1234567890" && x.(map[string]any)["warn_transfer"] == true
		}
	}
	if !seen {
		t.Fatal("the customer has no card with the number, or no warning to check before paying")
	}
	// The customer's own, to the business: asked first (a phone), a card
	// the business reads, sealed in its copy as the customer's words are.
	home := tolu.must(201, "POST", "/v1/me/saved", map[string]any{"name": "home", "title": "Home", "kind": "place",
		"details": map[string]any{"line": "4 Allen Avenue", "area": "Ikeja", "phone": "+2348031120099"}})["saved"].(map[string]any)
	mineShare := func(confirm bool) map[string]any {
		return map[string]any{"card": map[string]any{"type": "saved", "saved": map[string]any{"id": home["id"], "owner": "mine", "confirm": confirm}}, "client_id": "home-1"}
	}
	if e := tolu.must(409, "POST", "/v1/me/chats/"+toluChat+"/messages", mineShare(false)); e["confirm_share"] != true || !strings.HasPrefix(fmt.Sprint(e["error"]), "Share Home's phone number and details with ") {
		t.Fatalf("customer asked: %v", e)
	}
	sentHome := tolu.must(201, "POST", "/v1/me/chats/"+toluChat+"/messages", mineShare(true))
	// The customer's own copy is masked.
	if c, _ := sentHome["card"].(map[string]any); sentHome["kind"] != "card" || c == nil || strings.Contains(fmt.Sprint(c), "+2348031120099") {
		t.Fatalf("customer's card: %v", sentHome)
	}
	tolu.must(200, "POST", "/v1/me/chats/"+toluChat+"/messages", mineShare(true)) // the same client_id: the first
	tolu.must(400, "POST", "/v1/me/chats/"+toluChat+"/messages", map[string]any{"card": map[string]any{"type": "saved", "saved": map[string]any{"id": acct["id"], "owner": "business"}}})
	var got2 map[string]any
	for _, x := range w.owner.must(200, "GET", "/v1/customer-chats/"+w.threadOf(t, personID(t, tolu))+"/messages", nil)["messages"].([]any) {
		if c, ok := x.(map[string]any)["card"].(map[string]any); ok && c["title"] == "Home" {
			got2 = c
		}
	}
	if got2 == nil || got2["sealed"] != nil || got2["saved"].(map[string]any)["details"].(map[string]any)["line"] != "4 Allen Avenue" {
		t.Fatalf("the business's copy: %v", got2)
	}
	var plain int
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT count(*) FROM customer_messages WHERE card::text LIKE '%2348031120099%' OR card::text LIKE '%Allen%'`).Scan(&plain); err != nil || plain != 0 {
		t.Fatalf("kept in the clear: %d %v", plain, err)
	}
	// On WhatsApp: the lines.
	w.withOwnNumber(t)
	w.onWhatsApp(t, "+2348031120084", "Your account number please")
	threads := w.whatsappThread(t)
	onWA := reply(threads[len(threads)-1])
	if onWA["card"] != nil || onWA["body"] != "Our account\nBank: Zenith Bank\nAccount number: 1234567890" {
		t.Fatalf("on WhatsApp: %v", onWA)
	}
	w.owner.must(409, "POST", "/v1/customer-chats/"+threads[len(threads)-1]+"/messages", map[string]any{"card": map[string]any{"type": "saved",
		"saved": map[string]any{"id": acct["id"], "owner": "business"}}})
}

// A saved thing shared between linked businesses (saved objects S1, part
// 3): the seller's account as a tag in the words, asked first; the buyer
// has it as a card with the account number to copy; not shared unnamed.
func TestSavedLinkCards(t *testing.T) {
	w := newWAWorld(t)
	seller, buyer, _, _ := linkedPair(t, w)
	acct := seller.must(201, "POST", "/v1/saved", map[string]any{"name": "our-account", "title": "Dangote Stores account", "bank_name": "Zenith Bank", "account_number": "1234567890"})["saved"].(map[string]any)
	link := seller.must(200, "GET", "/v1/link-chats", nil)["conversations"].([]any)[0].(map[string]any)["link"].(string)
	theirs := buyer.must(200, "GET", "/v1/link-chats", nil)["conversations"].([]any)[0].(map[string]any)["link"].(string)
	msg := func(text string, confirm bool) map[string]any {
		return map[string]any{"text": text, "mentions": []map[string]any{{"kind": "saved", "saved": map[string]any{"id": acct["id"], "owner": "business", "confirm": confirm}}}}
	}
	if e := seller.must(409, "POST", "/v1/link-chats/"+link+"/messages", msg("Pay to @our-account", false)); e["confirm_share"] != true || e["error"] != "Share Dangote Stores account's account details with Ada's Kitchen?" {
		t.Fatalf("asked: %v", e)
	}
	seller.must(400, "POST", "/v1/link-chats/"+link+"/messages", msg("Pay to us", true))
	// Named as a whole word: @our-accountant is not @our-account, and it is
	// refused before anything is asked.
	seller.must(400, "POST", "/v1/link-chats/"+link+"/messages", msg("Ask @our-accountant", false))
	// Staff who may write to linked businesses but not manage saved things
	// do not share its account number.
	clerk := w.member(t, seller, "clerk@seller.test", "shift_manager")
	if e := clerk.must(403, "POST", "/v1/link-chats/"+link+"/messages", msg("Pay to @our-account", true)); !strings.Contains(fmt.Sprint(e["error"]), "Only those who manage") {
		t.Fatalf("clerk: %v", e)
	}
	sent := seller.must(201, "POST", "/v1/link-chats/"+link+"/messages", msg("Pay to @our-account.", true))["message"].(map[string]any)
	if ms, _ := sent["mentions"].([]any); len(ms) != 1 || ms[0].(map[string]any)["kind"] != "saved" || strings.Contains(fmt.Sprint(ms), "1234567890") {
		t.Fatalf("sent (the seller's copy masked): %v", sent)
	}
	var shares int
	if err := w.env.DB.Admin.QueryRow(context.Background(), `SELECT count(*) FROM audit_log WHERE target = $1 AND action = 'saved.shared'`, acct["id"]).Scan(&shares); err != nil || shares != 1 {
		t.Fatalf("shares recorded: %d %v", shares, err)
	}
	var card map[string]any
	for _, x := range buyer.must(200, "GET", "/v1/link-chats/"+theirs+"/messages", nil)["messages"].([]any) {
		for _, m := range x.(map[string]any)["mentions"].([]any) {
			if mm := m.(map[string]any); mm["kind"] == "saved" {
				card = mm["saved"].(map[string]any)
			}
		}
	}
	if card == nil || card["details"].(map[string]any)["account_number"] != "1234567890" || card["title"] != "Dangote Stores account" {
		t.Fatalf("the buyer's card: %v", card)
	}
}
