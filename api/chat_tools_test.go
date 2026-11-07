package api_test

import (
	"strings"
	"testing"

	"github.com/israel-duff/taskiem/engine/whatsapp/whatsapptest"
)

// Chat tools, phase 1 (docs/spec/chat-tools.md): with the switch on, a
// message's recipients decide who gets it. Linked businesses it names get
// it, read by no command; "@Taskiem", or naming none, is Taskiem's; "..."
// still works. Off, nothing changes.
func TestChatToolsRouting(t *testing.T) {
	w := shopWorld(t)
	withStock(w.world)
	withPurchases(w.world)
	withLinkedOrders(w.world)
	dangote, buyer, _, _ := linkedPair(t, w.waWorld)
	const buyerOn = "+2348010000721"
	w.bind(t, buyer, buyerOn)
	w.say(t, buyerOn, "links")
	link := func(c *client, other string) string {
		t.Helper()
		for _, x := range c.must(200, "GET", "/v1/link-chats", nil)["conversations"].([]any) {
			if m := x.(map[string]any); other == "" || m["name"] == other {
				return m["link"].(string)
			}
		}
		t.Fatalf("no conversation with %q", other)
		return ""
	}
	bua := w.tenant(t, "BUA Foods Plc", "b@tools.test")
	bua.must(200, "PUT", "/v1/link-settings", map[string]any{"findable": true})
	buyer.must(201, "POST", "/v1/links", map[string]any{"business": tenantOf(t, bua), "role": "supplier"})
	bua.must(200, "POST", "/v1/links/"+bua.must(200, "GET", "/v1/links", nil)["links"].([]any)[0].(map[string]any)["id"].(string)+"/accept", nil)
	w.srv.WaitBackground()
	bodies := func(c *client) []string {
		t.Helper()
		var out []string
		for _, x := range c.must(200, "GET", "/v1/link-chats/"+link(c, "")+"/messages", nil)["messages"].([]any) {
			if m := x.(map[string]any); m["kind"] != "card" {
				out = append(out, m["body"].(string))
			}
		}
		return out
	}
	notSent := func(text string) string {
		t.Helper()
		n, m := len(bodies(dangote)), len(bodies(bua))
		r := w.say(t, buyerOn, text)
		w.srv.WaitBackground()
		if strings.Contains(r, "Sent to") || len(bodies(dangote)) != n || len(bodies(bua)) != m {
			t.Fatalf("%q went to a business: %q", text, r)
		}
		return r
	}
	sent := func(text, want string) {
		t.Helper()
		if r := w.say(t, buyerOn, text); !strings.Contains(r, "✓ Sent to") || !strings.Contains(r, want) {
			t.Fatalf("%q: %q", text, r)
		}
		w.srv.WaitBackground()
	}

	// Off until turned on; only someone who manages members turns it on.
	if got := buyer.must(200, "GET", "/v1/settings/chat-tools", nil)["chat_tools"]; got != false {
		t.Fatalf("off at first: %v", got)
	}
	viewer := addMember(t, w.world, buyer, "vic", "viewer")
	viewer.must(403, "PUT", "/v1/settings/chat-tools", map[string]any{"chat_tools": true})
	if r := notSent("@Dangote how much is a bag of cement?"); !strings.Contains(r, "start it with *...*") {
		t.Fatalf("off, read as before: %q", r)
	}
	buyer.must(200, "PUT", "/v1/settings/chat-tools", map[string]any{"chat_tools": true})
	if got := viewer.must(200, "GET", "/v1/settings/chat-tools", nil)["chat_tools"]; got != true {
		t.Fatalf("on for everyone: %v", got)
	}

	// A business named: straight to it, no marker.
	sent("@Dangote how much is a bag of cement?", "*Dangote Stores*")
	sent("Hi @dangote, is the depot open\ntoday?", "*Dangote Stores*")
	sent("@Dangote @BUA price of cement this week?", "*Dangote Stores*, *BUA Foods Plc*")
	sent("... @Dangote the marker still works", "*Dangote Stores*")
	want := []string{"how much is a bag of cement?", "Hi dangote, is the depot open\ntoday?", "price of cement this week?", "the marker still works"}
	if got := bodies(dangote); strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("what Dangote received: %q", got)
	}
	if got := bodies(bua); len(got) != 1 || got[0] != "price of cement this week?" {
		t.Fatalf("what BUA received: %q", got)
	}

	// Taskiem's: "@Taskiem" (anywhere, any case), naming no one, or naming
	// what is no linked business.
	notSent("@Taskiem @Dangote how much is a bag of cement?")
	notSent("what does @dangote charge, @taskiem?")
	notSent("links")
	notSent("@Nobody hello")
	notSent("cement @4500")
	if r := notSent("@Taskiem"); r == "" {
		t.Fatal("a lone @Taskiem is answered")
	}

	// Two businesses that "Dangote" could be: asked about, nothing sent.
	sugar := w.tenant(t, "Dangote Sugar", "s@tools.test")
	sugar.must(200, "PUT", "/v1/link-settings", map[string]any{"findable": true})
	buyer.must(201, "POST", "/v1/links", map[string]any{"business": tenantOf(t, sugar), "role": "supplier"})
	sugar.must(200, "POST", "/v1/links/"+sugar.must(200, "GET", "/v1/links", nil)["links"].([]any)[0].(map[string]any)["id"].(string)+"/accept", nil)
	w.srv.WaitBackground()
	if r := notSent("@Dangote price?"); !strings.Contains(r, "could be more than one") {
		t.Fatalf("ambiguous: %q", r)
	}

	// Someone who may not write to linked businesses sends nothing.
	const viewerOn = "+2348010000722"
	w.bind(t, viewer, viewerOn)
	w.say(t, viewerOn, "links")
	n := len(bodies(dangote))
	if r := w.say(t, viewerOn, "@Dangote Stores hello"); strings.Contains(r, "Sent to") || len(bodies(dangote)) != n {
		t.Fatalf("a viewer: %q", r)
	}

	// The web's Taskiem conversation follows the same rule.
	buyer.must(200, "POST", "/v1/taskiem-chat", map[string]any{"text": "@Dangote Stores from the web"})
	w.srv.WaitBackground()
	if got := bodies(dangote); got[len(got)-1] != "from the web" {
		t.Fatalf("from the web's Taskiem conversation: %q", got[len(got)-1])
	}

	// A conversation with a business on the web: "@Taskiem" goes to the
	// person's own Taskiem conversation and to no business.
	dl, bl := link(buyer, "Dangote Stores"), link(buyer, "BUA Foods Plc")
	n = len(bodies(dangote))
	out := buyer.must(202, "POST", "/v1/link-chats/"+dl+"/messages", map[string]any{"text": "@Taskiem status"})
	if ms, _ := out["messages"].([]any); len(ms) == 0 {
		t.Fatalf("Taskiem's answer: %v", out)
	}
	if got := len(bodies(dangote)); got != n {
		t.Fatalf("Dangote was sent @Taskiem: %d, was %d", got, n)
	}
	// Another business named gets its own copy.
	m := len(bodies(bua))
	out = buyer.must(201, "POST", "/v1/link-chats/"+dl+"/messages", map[string]any{
		"text": "@BUA Foods Plc can you match this?", "mentions": []any{map[string]any{"kind": "business", "link": bl}}})
	also, _ := out["also"].([]any)
	if len(also) != 1 || also[0].(map[string]any)["name"] != "BUA Foods Plc" || also[0].(map[string]any)["sent"] != true {
		t.Fatalf("also: %v", out)
	}
	w.srv.WaitBackground()
	if got := bodies(bua); len(got) != m+1 || got[len(got)-1] != "@BUA Foods Plc can you match this?" {
		t.Fatalf("what BUA received: %q", got)
	}
	if got := len(bodies(dangote)); got != n+1 {
		t.Fatalf("Dangote got the message: %d, was %d", got, n)
	}

	// Off again: as before.
	buyer.must(200, "PUT", "/v1/settings/chat-tools", map[string]any{"chat_tools": false})
	notSent("@Dangote Stores off again")
	out = buyer.must(201, "POST", "/v1/link-chats/"+dl+"/messages", map[string]any{"text": "@Taskiem is just words"})
	if _, ok := out["also"]; ok {
		t.Fatalf("no also when off: %v", out)
	}
}

// A photo captioned with a linked business goes to it, with chat tools on,
// no marker needed; with "@Taskiem" it is Taskiem's.
func TestChatToolsFiles(t *testing.T) {
	w := newWAWorld(t)
	store := newProjectStorage(t)
	w.srv.Media = store.service()
	w.srv.Media.Scanner = chatScanner{}
	_, buyer, _, _ := linkedPair(t, w)
	const buyerOn = "+2348010000723"
	w.bind(t, buyer, buyerOn)
	w.say(t, buyerOn, "links")
	buyer.must(200, "PUT", "/v1/settings/chat-tools", map[string]any{"chat_tools": true})
	reply := func(msg map[string]any) string {
		t.Helper()
		got := w.send(t, buyerOn, msg)
		if len(got) == 0 {
			t.Fatal("no reply")
		}
		w.srv.WaitBackground()
		return got[len(got)-1].Text
	}
	w.graph.AddMedia("img-1", "image/jpeg", photo(t), 0)
	if r := reply(whatsapptest.Image("img-1", "image/jpeg", "@Dangote the torn bags")); !strings.Contains(r, "✓ Sent to *Dangote Stores*") {
		t.Fatalf("a photo: %q", r)
	}
	if r := reply(whatsapptest.Image("img-1", "image/jpeg", "@Taskiem @Dangote the torn bags")); strings.Contains(r, "Sent to") {
		t.Fatalf("a photo for Taskiem: %q", r)
	}
	if r := reply(whatsapptest.Image("img-1", "image/jpeg", "new bags")); strings.Contains(r, "Sent to") {
		t.Fatalf("a photo naming no one: %q", r)
	}
}

// A service of our own tagged in a conversation (chat tools): the message
// carries it with its price and how long it takes, on both sides; one that
// is not ours is refused.
func TestServiceMention(t *testing.T) {
	w := shopWorld(t)
	seller, buyer, _, _ := linkedPair(t, w.waWorld)
	verifyPhone(t, w.world, seller, "phone", "business")
	slug := strings.ToLower(strings.ReplaceAll(tenantOf(t, seller), "-", ""))[:12]
	seller.must(200, "PUT", "/v1/storefront", map[string]any{"slug": "mill-" + slug, "draft": map[string]any{"name": "The mill"}})
	svc := seller.must(201, "POST", "/v1/storefront/catalogue/make", map[string]any{"kind": "services"})
	seller.must(201, "POST", "/v1/tables/"+svc["table_id"].(string)+"/rows", map[string]any{"row": map[string]any{"name": "Rice milling", "price": 2500, "duration_minutes": 30}})
	seller.must(200, "POST", "/v1/storefront/catalogue/refresh", nil)
	id := seller.must(200, "GET", "/v1/storefront/services", nil)["items"].([]any)[0].(map[string]any)["id"].(string)
	link := seller.must(200, "GET", "/v1/link-chats", nil)["conversations"].([]any)[0].(map[string]any)["link"].(string)

	msg := seller.must(201, "POST", "/v1/link-chats/"+link+"/messages", map[string]any{
		"text": "@Rice milling is free this afternoon", "mentions": []any{map[string]any{"kind": "service", "service_id": id}}})["message"].(map[string]any)
	ms, _ := msg["mentions"].([]any)
	if len(ms) != 1 {
		t.Fatalf("mentions: %v", msg)
	}
	m := ms[0].(map[string]any)
	if m["kind"] != "service" || m["name"] != "Rice milling" || m["minutes"] != float64(30) || m["price_kobo"] != float64(250000) {
		t.Fatalf("the service: %v", m)
	}
	w.srv.WaitBackground()
	bl := buyer.must(200, "GET", "/v1/link-chats", nil)["conversations"].([]any)[0].(map[string]any)["link"].(string)
	got := buyer.must(200, "GET", "/v1/link-chats/"+bl+"/messages", nil)["messages"].([]any)
	if theirs := got[len(got)-1].(map[string]any)["mentions"].([]any)[0].(map[string]any); theirs["kind"] != "service" || theirs["minutes"] != float64(30) {
		t.Fatalf("their copy: %v", theirs)
	}
	// Not one of ours.
	seller.must(400, "POST", "/v1/link-chats/"+link+"/messages", map[string]any{
		"text": "@Rice milling", "mentions": []any{map[string]any{"kind": "service", "service_id": "nope"}}})
}
