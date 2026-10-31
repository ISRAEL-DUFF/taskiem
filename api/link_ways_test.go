package api_test

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"

	"github.com/israel-duff/taskiem/engine/egress"
	"github.com/israel-duff/taskiem/engine/invoicing"
	"github.com/israel-duff/taskiem/engine/sender"
)

// WhatsApp's ways in the conversation between linked businesses (L3,
// §5.5): the typing line; pinned chats first; delete for me (our copy
// hidden, theirs kept, a card never); forward (a message and a photo, to
// another linked business, marked Forwarded, the photo copied into each
// business's own storage); a link's preview, fetched once by the sender's
// side, on both copies.
func TestLinkConversationWays(t *testing.T) {
	ctx := context.Background()
	w := newWAWorld(t)
	w.srv.Invoices = &invoicing.Invoices{Pool: w.env.DB.App, Vault: w.env.Vault, PageBase: "https://forms.taskiem.test",
		Messages: &sender.Queue{Vault: w.env.Vault, HashKey: bytes.Repeat([]byte{8}, 32)}, Signal: w.env.Store.DeliverSignalOnce}
	store := newProjectStorage(t)
	w.srv.Media = store.service()
	w.srv.Egress = &egress.Guard{Blocked: func(netip.Addr) bool { return false }}
	w.srv.PreviewAnyPort()
	seller, buyer, sellerID, buyerID := linkedPair(t, w)
	link := buyer.must(200, "GET", "/v1/link-chats", nil)["conversations"].([]any)[0].(map[string]any)["link"].(string)
	// A third business the buyer is linked with.
	cherry := w.tenant(t, "Cherry Blocks", "c@ways.test")
	cherryID := tenantOf(t, cherry)
	cherry.must(200, "PUT", "/v1/link-settings", map[string]any{"findable": true})
	buyer.must(201, "POST", "/v1/links", map[string]any{"business": cherryID, "role": "supplier"})
	cl := cherry.must(200, "GET", "/v1/links", nil)["links"].([]any)[0].(map[string]any)["id"].(string)
	cherry.must(200, "POST", "/v1/links/"+cl+"/accept", nil)
	w.srv.WaitBackground()

	// The typing line: the seller types, the buyer sees it, the seller does not.
	seller.must(204, "POST", "/v1/link-chats/"+link+"/typing", nil)
	if out := buyer.must(200, "GET", "/v1/link-chats/"+link+"/messages", nil); out["typing"] != true {
		t.Errorf("the buyer sees typing: %v", out)
	}
	if out := seller.must(200, "GET", "/v1/link-chats/"+link+"/messages", nil); out["typing"] != nil {
		t.Errorf("the seller's own typing: %v", out)
	}
	conv := func(c *client, l string) map[string]any {
		t.Helper()
		for _, x := range c.must(200, "GET", "/v1/link-chats", nil)["conversations"].([]any) {
			if x.(map[string]any)["link"] == l {
				return x.(map[string]any)
			}
		}
		t.Fatalf("no conversation %s", l)
		return nil
	}
	if conv(buyer, link)["typing"] != true {
		t.Error("typing in the list")
	}

	// A link's preview, fetched once by the seller's side, on both copies.
	page := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = io.WriteString(w, `<html><head><title>Ignored</title><meta property="og:title" content="Rice, 50 kg &amp; more"><meta name="description" content="Fresh stock this week"></head><body>hi</body></html>`)
	}))
	t.Cleanup(page.Close)
	m := seller.must(201, "POST", "/v1/link-chats/"+link+"/messages", map[string]any{"text": "New prices: " + page.URL + "/prices."})["message"].(map[string]any)
	pv, _ := m["preview"].(map[string]any)
	if pv == nil || pv["title"] != "Rice, 50 kg & more" || pv["description"] != "Fresh stock this week" || pv["host"] != "127.0.0.1" || pv["url"] != page.URL+"/prices" {
		t.Fatalf("the preview: %v", m)
	}
	msgs := buyer.must(200, "GET", "/v1/link-chats/"+link+"/messages", nil)["messages"].([]any)
	if got := msgs[len(msgs)-1].(map[string]any); got["preview"].(map[string]any)["title"] != "Rice, 50 kg & more" {
		t.Errorf("the buyer's copy: %v", got)
	}

	// Pinned chats first: the buyer's Dangote chat is older than Cherry's.
	buyer.must(201, "POST", "/v1/link-chats/"+cl+"/messages", map[string]any{"text": "Do you have blocks?"})
	if first := buyer.must(200, "GET", "/v1/link-chats", nil)["conversations"].([]any)[0].(map[string]any); first["link"] != cl {
		t.Fatalf("newest first: %v", first)
	}
	buyer.must(200, "PUT", "/v1/link-chats/"+link, map[string]any{"pinned": true})
	if first := buyer.must(200, "GET", "/v1/link-chats", nil)["conversations"].([]any)[0].(map[string]any); first["link"] != link || first["pinned"] != true {
		t.Errorf("pinned first: %v", first)
	}
	buyer.must(200, "PUT", "/v1/link-chats/"+link, map[string]any{"pinned": false})
	if conv(buyer, link)["pinned"] != false {
		t.Error("unpinned")
	}

	// Forward: the seller's message, from the buyer's Dangote chat to Cherry.
	fw := buyer.must(201, "POST", "/v1/link-chats/"+link+"/forward", map[string]any{"message": m["id"], "to": []string{cl}})["messages"].([]any)
	if len(fw) != 1 || fw[0].(map[string]any)["forwarded"] != true || fw[0].(map[string]any)["link"] != cl || !strings.HasPrefix(fw[0].(map[string]any)["body"].(string), "New prices:") {
		t.Fatalf("forwarded: %v", fw)
	}
	cm := cherry.must(200, "GET", "/v1/link-chats/"+cl+"/messages", nil)["messages"].([]any)
	if got := cm[len(cm)-1].(map[string]any); got["forwarded"] != true || got["mine"] != false || got["preview"] == nil {
		t.Errorf("Cherry's copy: %v", got)
	}
	buyer.must(400, "POST", "/v1/link-chats/"+link+"/forward", map[string]any{"message": m["id"], "to": []string{link}})
	buyer.must(400, "POST", "/v1/link-chats/"+link+"/forward", map[string]any{"message": m["id"], "to": []string{}})
	seller.must(404, "POST", "/v1/link-chats/"+link+"/forward", map[string]any{"message": m["id"], "to": []string{cl}})

	// A photo forwarded: a copy in the buyer's storage and Cherry's.
	code, out := seller.sendFile(t, link, "bags.jpg", photo(t), map[string]string{"text": "The bags"})
	if code != 201 {
		t.Fatalf("photo: %d %v", code, out)
	}
	buyerFiles := len(store.in("prj_" + buyerID))
	pf := buyer.must(201, "POST", "/v1/link-chats/"+link+"/forward", map[string]any{"message": out["message"].(map[string]any)["id"], "to": []string{cl}})["messages"].([]any)[0].(map[string]any)
	if a := pf["attachment"].(map[string]any); a["kind"] != "photo" || pf["forwarded"] != true || pf["body"] != "The bags" {
		t.Fatalf("a photo forwarded: %v", pf)
	}
	if len(store.in("prj_"+buyerID)) != buyerFiles+1 || len(store.in("prj_"+cherryID)) != 1 {
		t.Errorf("a copy each: buyer %v, cherry %v", store.in("prj_"+buyerID), store.in("prj_"+cherryID))
	}
	cm = cherry.must(200, "GET", "/v1/link-chats/"+cl+"/messages", nil)["messages"].([]any)
	if code, _, data := cherry.getRaw(t, "/v1/link-chats/"+cl+"/files/"+cm[len(cm)-1].(map[string]any)["id"].(string)); code != 200 || len(data) == 0 {
		t.Errorf("Cherry opens its copy: %d", code)
	}

	// Delete for me: the buyer's copy hidden, the seller's kept.
	buyer.must(204, "POST", "/v1/link-chats/"+link+"/messages/"+m["id"].(string)+"/hide", nil)
	for _, x := range buyer.must(200, "GET", "/v1/link-chats/"+link+"/messages", nil)["messages"].([]any) {
		if x.(map[string]any)["id"] == m["id"] {
			t.Error("hidden from the buyer")
		}
	}
	kept := false
	for _, x := range seller.must(200, "GET", "/v1/link-chats/"+link+"/messages", nil)["messages"].([]any) {
		kept = kept || x.(map[string]any)["id"] == m["id"]
	}
	if !kept {
		t.Error("the seller keeps it")
	}
	buyer.must(404, "POST", "/v1/link-chats/"+link+"/forward", map[string]any{"message": m["id"], "to": []string{cl}})
	// A card is never hidden, nor forwarded.
	x := seller.must(201, "POST", "/v1/invoices", map[string]any{"customer": map[string]any{"name": "Ada's Kitchen", "key": "link:" + buyerID},
		"lines": []map[string]any{{"description": "Rice", "quantity": 2, "unit_kobo": 4500000}}}, "Idempotency-Key", "w-1")
	seller.must(200, "POST", "/v1/invoices/"+x["id"].(string)+"/send", nil, "Idempotency-Key", "w-2")
	if _, err := w.srv.LinkMirrorTick(ctx); err != nil {
		t.Fatal(err)
	}
	w.srv.WaitBackground()
	var card string
	for _, x := range buyer.must(200, "GET", "/v1/link-chats/"+link+"/messages", nil)["messages"].([]any) {
		if x.(map[string]any)["kind"] == "card" {
			card = x.(map[string]any)["id"].(string)
		}
	}
	if card == "" {
		t.Fatal("no card")
	}
	buyer.must(400, "POST", "/v1/link-chats/"+link+"/messages/"+card+"/hide", nil)
	buyer.must(400, "POST", "/v1/link-chats/"+link+"/forward", map[string]any{"message": card, "to": []string{cl}})
	_ = sellerID
}
