package api_test

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/israel-duff/taskiem/engine/egress"
)

// A conversation's message reaches the other side's phones outside
// WhatsApp's 24-hour window too, as the link message template (business
// links §5.5: "within seconds"), with Open to the Chat page; inside the
// window, as text.
func TestLinkMessageOutsideWindow(t *testing.T) {
	ctx := context.Background()
	w := newWAWorld(t)
	store := newProjectStorage(t)
	w.srv.Media = store.service()
	w.srv.Media.Scanner = chatScanner{}
	seller, buyer, _, _ := linkedPair(t, w)
	const buyerOn = "+2348010000395"
	w.bind(t, buyer, buyerOn)
	w.say(t, buyerOn, "links")
	link := buyer.must(200, "GET", "/v1/link-chats", nil)["conversations"].([]any)[0].(map[string]any)["link"].(string)

	// Inside the window: text.
	before := len(w.graph.Sent(buyerOn))
	seller.must(201, "POST", "/v1/link-chats/"+link+"/messages", map[string]any{"text": "Rice is back"})
	if told := w.lastTo(t, buyerOn, before); len(told) != 1 || told[0].Template != "" || !strings.Contains(told[0].Text, "[Dangote Stores]\nOwner: Rice is back") {
		t.Fatalf("inside the window: %+v", told)
	}

	// The window closed: the template, not nothing.
	if _, err := w.env.DB.Admin.Exec(ctx, `UPDATE whatsapp_contacts SET last_inbound_at = now() - interval '25 hours' WHERE number = $1`, buyerOn); err != nil {
		t.Fatal(err)
	}
	before = len(w.graph.Sent(buyerOn))
	seller.must(201, "POST", "/v1/link-chats/"+link+"/messages", map[string]any{"text": "Prices went up\nby ₦500 a bag"})
	told := w.lastTo(t, buyerOn, before)
	if len(told) != 1 || told[0].Template != "taskiem_link_message" || !strings.Contains(told[0].Text, "[Dangote Stores] has a message for you: Owner: Prices went up by ₦500 a bag") ||
		!strings.Contains(told[0].Text, "Tap Open") {
		t.Fatalf("outside the window: %+v", told)
	}
	// A voice note: what was sent.
	before = len(w.graph.Sent(buyerOn))
	if code, out := seller.sendFile(t, link, "note.ogg", oggVoice(), nil); code != 201 {
		t.Fatalf("voice: %d %v", code, out)
	}
	if told := w.lastTo(t, buyerOn, before); len(told) != 1 || told[0].Template != "taskiem_link_message" || !strings.Contains(told[0].Text, "has a message for you: Owner: (Voice note)") {
		t.Fatalf("a voice note outside the window: %+v", told)
	}
}

// The plan's messages per day (§13): counted against the business that
// sends, text and files alike, and refused as every other limit is; the
// other side's count is its own. And one person's sends are paced.
func TestLinkMessagesPerDay(t *testing.T) {
	ctx := context.Background()
	w := newWAWorld(t)
	store := newProjectStorage(t)
	w.srv.Media = store.service()
	w.srv.Media.Scanner = chatScanner{}
	seller, buyer, _, _ := linkedPair(t, w)
	link := buyer.must(200, "GET", "/v1/link-chats", nil)["conversations"].([]any)[0].(map[string]any)["link"].(string)
	if err := w.env.Store.SetLimits(ctx, tenantID(t, seller), map[string]any{"link_messages_daily": 2}, "operator"); err != nil {
		t.Fatal(err)
	}
	seller.must(201, "POST", "/v1/link-chats/"+link+"/messages", map[string]any{"text": "One"})
	if code, out := seller.sendFile(t, link, "bags.jpg", photo(t), map[string]string{"text": "Two"}); code != 201 {
		t.Fatalf("the second: %d %v", code, out)
	}
	code, out := seller.do("POST", "/v1/link-chats/"+link+"/messages", map[string]any{"text": "Three"})
	if code != 429 || out["code"] != "limit_exceeded" || out["limit"] != "link_messages_daily" || !strings.Contains(out["error"].(string), "at most 2 messages") {
		t.Fatalf("over the limit: %d %v", code, out)
	}
	held := len(store.in("prj_" + tenantID(t, seller).String()))
	if code, out := seller.sendFile(t, link, "bags.jpg", photo(t), nil); code != 429 {
		t.Fatalf("a file over the limit: %d %v", code, out)
	}
	if got := len(store.in("prj_" + tenantID(t, seller).String())); got != held {
		t.Errorf("a refused file stored: %d, had %d", got, held)
	}
	// The buyer's own limit is the plan's: it still writes.
	buyer.must(201, "POST", "/v1/link-chats/"+link+"/messages", map[string]any{"text": "Noted"})
	if got := len(buyer.must(200, "GET", "/v1/link-chats/"+link+"/messages", nil)["messages"].([]any)); got != 3 {
		t.Errorf("messages in the conversation: %d", got)
	}

	// Paced: a burst, then a moment between messages.
	for i := range 40 {
		code, out := buyer.do("POST", "/v1/link-chats/"+link+"/messages", map[string]any{"text": fmt.Sprintf("Fast %d", i)})
		if code == 429 {
			if !strings.Contains(out["error"].(string), "slow down") {
				t.Fatalf("paced: %v", out)
			}
			return
		}
		if code != 201 {
			t.Fatalf("message %d: %d %v", i, code, out)
		}
	}
	t.Error("forty messages in a moment, none paced")
}

// Link previews (§13, §16.4): fetched only for a message that can be
// sent (the link active), once per business for a page (one business's
// fetch never answers another's), and at most so many a minute per
// person.
func TestLinkPreviewGuarded(t *testing.T) {
	w := newWAWorld(t)
	w.srv.Egress = &egress.Guard{Blocked: func(netip.Addr) bool { return false }}
	w.srv.PreviewAnyPort()
	seller, buyer, _, _ := linkedPair(t, w)
	link := buyer.must(200, "GET", "/v1/link-chats", nil)["conversations"].([]any)[0].(map[string]any)["link"].(string)
	var hits atomic.Int32
	page := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = io.WriteString(w, `<html><head><title>Page `+r.URL.Path+`</title></head><body></body></html>`)
	}))
	t.Cleanup(page.Close)
	send := func(c *client, path string) map[string]any {
		t.Helper()
		return c.must(201, "POST", "/v1/link-chats/"+link+"/messages", map[string]any{"text": "See " + page.URL + path})["message"].(map[string]any)
	}
	// Each business fetches for itself; a business's second message with
	// the same page is answered from what it fetched.
	if m := send(seller, "/a"); m["preview"] == nil {
		t.Fatalf("no preview: %v", m)
	}
	send(buyer, "/a")
	send(seller, "/a")
	if n := hits.Load(); n != 2 {
		t.Fatalf("fetched %d times for two businesses", n)
	}
	// So many a minute for one person: the rest go without a preview.
	for i := range 9 {
		send(seller, fmt.Sprintf("/p%d", i))
	}
	if n := hits.Load(); n != 11 {
		t.Fatalf("fetched %d times", n)
	}
	if m := send(seller, "/one-too-many"); m["preview"] != nil || hits.Load() != 11 {
		t.Errorf("past the minute's previews: %v, %d fetches", m, hits.Load())
	}
	// An ended link: the message is refused before anything is fetched.
	buyer.must(200, "POST", "/v1/links/"+link+"/end", nil)
	if code, _ := buyer.do("POST", "/v1/link-chats/"+link+"/messages", map[string]any{"text": "See " + page.URL + "/after"}); code != 409 {
		t.Fatalf("an ended link: %d", code)
	}
	if n := hits.Load(); n != 11 {
		t.Errorf("fetched for a message that cannot be sent: %d", n)
	}
}
