package api_test

import (
	"bytes"
	"mime/multipart"
	"net/url"
	"strings"
	"testing"
)

// A rider that is a business on Taskiem, through the API (docs/spec/
// chat-riders.md, phase 1): the business links with the rider's business
// and says which rider it is; an offer reaches the rider's business as an
// order with the delivery's details, the customer only once accepted; the
// rider's steps from their own business and their WhatsApp button move
// the business's delivery alike; Delivered with a photo keeps the proof
// in the business's storage; both conversations show the same cards.
func TestLinkedRiderByAPI(t *testing.T) {
	w, ada := newDispatchWorld(t, "Ada Stores")
	musaBiz := w.tenant(t, "Musa Rides", "musa@rides.test")
	musaID := tenantOf(t, musaBiz)

	// Linked: Ada asks Musa's business to supply her (deliveries).
	musaBiz.must(200, "PUT", "/v1/link-settings", map[string]any{"findable": true})
	ada.must(201, "POST", "/v1/links", map[string]any{"business": musaID, "role": "supplier"})
	l := musaBiz.must(200, "GET", "/v1/links", nil)["links"].([]any)[0].(map[string]any)
	musaBiz.must(200, "POST", "/v1/links/"+l["id"].(string)+"/accept", nil)
	w.srv.WaitBackground()
	link := l["id"].(string)

	// Musa taps in on WhatsApp and is Ada's rider, then named as the linked business.
	const musa = "+2348031110021"
	path := strings.TrimPrefix(ada.must(200, "GET", "/v1/riders", nil)["tap_in_url"].(string), "https://"+reachFormHost)
	loc, _ := url.Parse(w.page(t, path+"/whatsapp", nil).Header().Get("Location"))
	w.whatsapp(t, musa, loc.Query().Get("text"))
	m := ada.must(201, "POST", "/v1/riders", map[string]any{"name": "Musa Bello", "phone": musa, "zones": []string{"Lekki"}, "fee_kobo": 100000})["rider"].(map[string]any)
	rid := m["id"].(string)
	// A link that is not the business's own is refused; the real one is kept.
	ada.must(400, "PATCH", "/v1/riders/"+rid, map[string]any{"link_id": "00000000-0000-0000-0000-000000000001"})
	if r := ada.must(200, "PATCH", "/v1/riders/"+rid, map[string]any{"link_id": link})["rider"].(map[string]any); r["link_id"] != link {
		t.Fatalf("the rider's link: %v", r)
	}
	// The rider's business has nothing yet.
	if n := len(musaBiz.must(200, "GET", "/v1/trade-orders", nil)["orders"].([]any)); n != 0 {
		t.Fatalf("%d orders before any offer", n)
	}

	// The offer: Musa's WhatsApp as before, and an order in Musa's business.
	ref := w.order(t, "none")
	out := ada.must(200, "POST", "/v1/storefront/orders/"+ref+"/offer", map[string]any{})
	if out["status"] != "offered" || out["rider"] != "Musa Bello" {
		t.Fatalf("offer %v", out)
	}
	w.tick(t)
	if msg := w.graph.Last(t, musa); !strings.Contains(msg.Text, "A delivery for you: "+ref) || len(msg.Buttons) != 2 {
		t.Fatalf("the chat app's offer: %+v", msg)
	}
	orders := musaBiz.must(200, "GET", "/v1/trade-orders", nil)["orders"].([]any)
	if len(orders) != 1 {
		t.Fatalf("the rider's orders: %v", orders)
	}
	o := orders[0].(map[string]any)
	d, _ := o["delivery"].(map[string]any)
	if o["status"] != "received" || o["buyer_name"] != "Ada Stores" || o["reference"] != ref || d == nil || d["drop_area"] != "Lekki" || d["customer"] != nil ||
		o["total_kobo"].(float64) != 100000 {
		t.Fatalf("the rider's order: %v", o)
	}
	oid := o["id"].(string)
	// Not held: the customer is not shown.
	if got := musaBiz.must(200, "GET", "/v1/trade-orders/"+oid, nil)["order"].(map[string]any); got["delivery"].(map[string]any)["customer"] != nil {
		t.Fatalf("the customer before accepting: %v", got)
	}
	// A step that does not fit is refused in plain words.
	musaBiz.must(409, "POST", "/v1/trade-orders/"+oid+"/picked_up", nil)

	// Accepted from the rider's business: held, the customer theirs.
	got := musaBiz.must(200, "POST", "/v1/trade-orders/"+oid+"/accept", nil)["order"].(map[string]any)
	c, _ := got["delivery"].(map[string]any)["customer"].(map[string]any)
	if got["status"] != "accepted" || c == nil || c["name"] != "Bola Ade" || c["address"] != "12 Admiralty Way" {
		t.Fatalf("accepted: %v", got)
	}
	ds := ada.must(200, "GET", "/v1/storefront/orders/"+ref+"/deliveries", nil)["deliveries"].([]any)
	if len(ds) != 1 || ds[0].(map[string]any)["status"] != "accepted" || ds[0].(map[string]any)["link_order_id"] != oid {
		t.Fatalf("the business's delivery: %v", ds)
	}
	// Musa's chat app got the "it is yours" message with Picked up: the
	// button moves the order in their business too.
	w.tick(t)
	held := w.graph.Last(t, musa)
	if !strings.Contains(held.Text, ref+" is yours") || len(held.Buttons) != 1 || held.Buttons[0].Title != "Picked up" {
		t.Fatalf("held: %+v", held)
	}
	w.press(t, musa, held.Buttons[0].ID)
	if got := musaBiz.must(200, "GET", "/v1/trade-orders/"+oid, nil)["order"].(map[string]any); got["status"] != "dispatched" {
		t.Fatalf("after the button: %v", got["status"])
	}
	// Arrived from the business's side of the chat; Delivered with a photo.
	musaBiz.must(200, "POST", "/v1/trade-orders/"+oid+"/arrived", nil)
	var b bytes.Buffer
	mw := multipart.NewWriter(&b)
	fw, _ := mw.CreateFormFile("photo", "door.jpg")
	_, _ = fw.Write([]byte("\xff\xd8\xff photo"))
	_ = mw.Close()
	if code, res := musaBiz.rawPost(t, "/v1/trade-orders/"+oid+"/delivered", mw.FormDataContentType(), b.Bytes()); code != 200 {
		t.Fatalf("delivered with a photo: %d %v", code, res)
	}
	if len(w.proofs) != 1 {
		t.Fatalf("%d proofs kept", len(w.proofs))
	}
	got = musaBiz.must(200, "GET", "/v1/trade-orders/"+oid, nil)["order"].(map[string]any)
	if got["status"] != "delivered" || got["delivery"].(map[string]any)["customer"] != nil {
		t.Fatalf("delivered: %v", got)
	}
	order := ada.must(200, "GET", "/v1/storefront/orders/"+ref, nil)
	if v, ok := order["order"].(map[string]any); ok {
		order = v
	}
	if order["status"] != "completed" {
		t.Fatalf("the business's order: %v", order["status"])
	}
	ds = ada.must(200, "GET", "/v1/storefront/orders/"+ref+"/deliveries", nil)["deliveries"].([]any)
	if x := ds[0].(map[string]any); x["status"] != "delivered" || x["proof"] != true {
		t.Fatalf("the delivery: %v", x)
	}

	// One conversation, the same cards at the same steps on both sides.
	cards := func(c *client) []string {
		t.Helper()
		var out []string
		for _, x := range c.must(200, "GET", "/v1/link-chats/"+link+"/messages", nil)["messages"].([]any) {
			if m := x.(map[string]any); m["kind"] == "card" {
				card := m["card"].(map[string]any)
				out = append(out, card["doc"].(string)+":"+card["event"].(string))
			}
		}
		return out
	}
	wantAda := []string{"dispatch:offered", "dispatch:accepted", "dispatch:picked_up", "dispatch:arrived", "dispatch:delivered"}
	wantMusa := []string{"delivery:offered", "delivery:accepted", "delivery:picked_up", "delivery:arrived", "delivery:delivered"}
	if got := cards(ada); strings.Join(got, " ") != strings.Join(wantAda, " ") {
		t.Fatalf("Ada's cards: %v", got)
	}
	if got := cards(musaBiz); strings.Join(got, " ") != strings.Join(wantMusa, " ") {
		t.Fatalf("Musa's cards: %v", got)
	}
}
