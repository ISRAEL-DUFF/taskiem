package api_test

import (
	"strings"
	"testing"

	"github.com/israel-duff/taskiem/engine/whatsapp/whatsapptest"
)

// A message straight to linked businesses from the Taskiem conversation
// (docs/spec/direct-messages.md): "..." first, on its own line or before the
// message, then "@business"; a lone "..." waits for the message; a
// mention names a business by the start of its name; nothing goes to
// anyone when a mention is not one linked business; without the marker
// a mention is read as before, with a hint.
func TestDirectMessages(t *testing.T) {
	w := shopWorld(t)
	withStock(w.world)
	withPurchases(w.world)
	withLinkedOrders(w.world)
	dangote, buyer, _, _ := linkedPair(t, w.waWorld)
	const buyerOn = "+2348010000711"
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
	bua := w.tenant(t, "BUA Foods Plc", "b@direct.test")
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
	sent := func(text, want string) {
		t.Helper()
		if r := w.say(t, buyerOn, text); !strings.Contains(r, "✓ Sent to") || !strings.Contains(r, want) {
			t.Fatalf("%q: %q", text, r)
		}
		w.srv.WaitBackground()
	}

	// The marker on the same line, on a line of its own, as a phone's "…";
	// "Dangote" names Dangote Stores.
	sent("... @Dangote how much is a bag of cement?", "*Dangote Stores*")
	sent("...\n@dangote is the depot open\ntoday?", "*Dangote Stores*")
	sent("…  Hi @Dangote, price of rice?", "*Dangote Stores*")
	want := []string{"how much is a bag of cement?", "is the depot open\ntoday?", "Hi Dangote, price of rice?"}
	if got := bodies(dangote); strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("what Dangote received: %q", got)
	}

	// A lone "..." waits for the message; a message without a mention
	// after it is read as usual.
	if r := w.say(t, buyerOn, "..."); !strings.Contains(r, "Now send the message") {
		t.Fatalf("a lone marker: %q", r)
	}
	sent("@Dangote one more thing", "*Dangote Stores*")
	w.say(t, buyerOn, "...")
	if r := w.say(t, buyerOn, "links"); strings.Contains(r, "Sent to") {
		t.Fatalf("no mention after the wait: %q", r)
	}

	// Two businesses: the same words, each its own message.
	sent("... @Dangote @BUA price of cement this week?", "*Dangote Stores*, *BUA Foods Plc*")
	if got := bodies(bua); len(got) != 1 || got[0] != "price of cement this week?" {
		t.Fatalf("what BUA received: %q", got)
	}
	// An amount after "@" is no business.
	sent("... @Dangote can you do cement @4500?", "*Dangote Stores*")
	if got := bodies(dangote); got[len(got)-1] != "can you do cement @4500?" {
		t.Fatalf("an amount: %q", got[len(got)-1])
	}
	n := len(bodies(dangote))

	// Nothing goes to anyone without one linked business per mention.
	for text, reply := range map[string]string{
		"... @ how much?":         "Who is it for?",
		"... @Nobody @Dangote hi": "not linked with *Nobody*",
		"... @Dangote":            "What do you want to say to *Dangote Stores*?",
	} {
		if r := w.say(t, buyerOn, text); !strings.Contains(r, reply) {
			t.Errorf("%q: %q", text, r)
		}
	}
	// "...and" is no marker, and "..." naming no one is read as before;
	// without the marker a mention is read as before, with the hint.
	for _, text := range []string{"...and that is all", "... links"} {
		if r := w.say(t, buyerOn, text); strings.Contains(r, "Sent to") || strings.Contains(r, "Who is it for") {
			t.Errorf("%q is read as before: %q", text, r)
		}
	}
	if r := w.say(t, buyerOn, "@Dangote how much is a bag of cement?"); !strings.Contains(r, "*Dangote Stores* shares no price list") ||
		!strings.Contains(r, "To send it to *Dangote Stores* as a message instead, start it with *...*") {
		t.Errorf("without the marker: %q", r)
	}
	if got := len(bodies(dangote)); got != n {
		t.Errorf("nothing more sent to Dangote: %d, was %d", got, n)
	}

	// Two businesses that "Dangote" could be: asked about, nothing sent.
	sugar := w.tenant(t, "Dangote Sugar", "s@direct.test")
	sugar.must(200, "PUT", "/v1/link-settings", map[string]any{"findable": true})
	buyer.must(201, "POST", "/v1/links", map[string]any{"business": tenantOf(t, sugar), "role": "supplier"})
	sugar.must(200, "POST", "/v1/links/"+sugar.must(200, "GET", "/v1/links", nil)["links"].([]any)[0].(map[string]any)["id"].(string)+"/accept", nil)
	w.srv.WaitBackground()
	if r := w.say(t, buyerOn, "... @Dangote price?"); !strings.Contains(r, "could be more than one") || !strings.Contains(r, "Dangote Sugar") {
		t.Errorf("ambiguous: %q", r)
	}
	sent("... @Dangote Sugar price?", "*Dangote Sugar*")
	if got := len(bodies(dangote)); got != n {
		t.Errorf("Dangote Stores was not sent the ambiguous one: %d, was %d", got, n)
	}

	// Someone who may not write to linked businesses sends nothing.
	viewer := addMember(t, w.world, buyer, "vic", "viewer")
	const viewerOn = "+2348010000712"
	w.bind(t, viewer, viewerOn)
	w.say(t, viewerOn, "links")
	if r := w.say(t, viewerOn, "... @Dangote Stores hello"); strings.Contains(r, "Sent to") {
		t.Errorf("a viewer: %q", r)
	}
}

// A photo or a file from the phone captioned "... @business" goes to the
// business with the caption's words, each side keeping its own copy, as
// from the Chat page; after a lone "...", "@business" in the caption is
// enough; a file is scanned first and nothing is stored when it fails; a
// photo captioned "..." naming no one is read as before.
func TestDirectFiles(t *testing.T) {
	w := newWAWorld(t)
	store := newProjectStorage(t)
	w.srv.Media = store.service()
	w.srv.Media.Scanner = chatScanner{}
	seller, buyer, sellerID, buyerID := linkedPair(t, w)
	const buyerOn = "+2348010000713"
	w.bind(t, buyer, buyerOn)
	w.say(t, buyerOn, "links")
	link := seller.must(200, "GET", "/v1/link-chats", nil)["conversations"].([]any)[0].(map[string]any)["link"].(string)
	sellerProject, buyerProject := "prj_"+sellerID, "prj_"+buyerID
	last := func() map[string]any {
		t.Helper()
		ms := seller.must(200, "GET", "/v1/link-chats/"+link+"/messages", nil)["messages"].([]any)
		return ms[len(ms)-1].(map[string]any)
	}
	reply := func(msg map[string]any) string {
		t.Helper()
		got := w.send(t, buyerOn, msg)
		if len(got) == 0 {
			t.Fatal("no reply")
		}
		w.srv.WaitBackground()
		return got[len(got)-1].Text
	}
	copies := func(want int) {
		t.Helper()
		if s, b := store.in(sellerProject), store.in(buyerProject); len(s) != want || len(b) != want {
			t.Fatalf("%d copies each: seller %v, buyer %v", want, s, b)
		}
	}
	w.graph.AddMedia("img-1", "image/jpeg", photo(t), 0)
	w.graph.AddMedia("doc-1", "application/pdf", []byte("%PDF-1.4\n1 0 obj << /Type /Catalog >> endobj\ntrailer << /Root 1 0 R >>\n%%EOF\n"), 0)
	w.graph.AddMedia("doc-bad", "application/pdf", []byte("%PDF-1.4\n1 0 obj << /Type /Catalog >> endobj EICAR-STANDARD-ANTIVIRUS-TEST-FILE"), 0)

	if r := reply(whatsapptest.Image("img-1", "image/jpeg", "... @Dangote the torn bags")); !strings.Contains(r, "✓ Sent to *Dangote Stores*") {
		t.Fatalf("a photo: %q", r)
	}
	m := last()
	if a, _ := m["attachment"].(map[string]any); a == nil || a["kind"] != "photo" || m["body"] != "the torn bags" || m["mine"] != false {
		t.Fatalf("the photo in Dangote's conversation: %v", m)
	}
	copies(1)

	// After a lone "...", the caption's "@business" is enough.
	w.say(t, buyerOn, "...")
	if r := reply(whatsapptest.Image("img-1", "image/jpeg", "@Dangote again")); !strings.Contains(r, "✓ Sent to *Dangote Stores*") {
		t.Fatalf("a photo after a lone marker: %q", r)
	}
	copies(2)

	// A file with no words, its name kept.
	if r := reply(whatsapptest.Document("doc-1", "application/pdf", "price.pdf", "... @Dangote")); !strings.Contains(r, "✓ Sent to *Dangote Stores*") {
		t.Fatalf("a file: %q", r)
	}
	if a, _ := last()["attachment"].(map[string]any); a == nil || a["kind"] != "file" || a["name"] != "price.pdf" {
		t.Fatalf("the file: %v", last())
	}
	copies(3)

	// A threat: not sent, nothing stored.
	if r := reply(whatsapptest.Document("doc-bad", "application/pdf", "x.pdf", "... @Dangote")); !strings.Contains(r, "Not sent: the file did not pass the malware check") {
		t.Fatalf("a threat: %q", r)
	}
	copies(3)
	// Naming no one: read as before, not sent.
	if r := reply(whatsapptest.Image("img-1", "image/jpeg", "... new bags")); strings.Contains(r, "Sent to") {
		t.Fatalf("no one named: %q", r)
	}
	// A business not linked: nothing sent.
	if r := reply(whatsapptest.Image("img-1", "image/jpeg", "... @Nobody look")); !strings.Contains(r, "not linked with *Nobody*") {
		t.Fatalf("not linked: %q", r)
	}
	copies(3)
}
