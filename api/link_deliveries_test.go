package api_test

import (
	"bytes"
	"context"
	"fmt"
	"mime/multipart"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/israel-duff/taskiem/connectors/iswallet"
	ridersconn "github.com/israel-duff/taskiem/connectors/riders"
	"github.com/israel-duff/taskiem/engine/connector"
	"github.com/israel-duff/taskiem/engine/dispatch"
	"github.com/israel-duff/taskiem/engine/secrets"
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

// TestRiderByPhoneLinksBusiness (docs/spec/chat-riders.md, phase 2): a
// rider added by phone whose number is a business on Taskiem has that
// business asked to link; once it says yes the rider is named as it, and
// a rider added before the link is named when the link is accepted.
func TestRiderByPhoneLinksBusiness(t *testing.T) {
	w, ada := newDispatchWorld(t, "Ada Stores")
	musaBiz := w.tenant(t, "Musa Rides", "musa@rides.test")
	tundeBiz := w.tenant(t, "Tunde Express", "tunde@express.test")
	const musa, tunde = "+2348031110031", "+2348031110032"
	// Their numbers are their businesses' owners' WhatsApp.
	for email, number := range map[string]string{"musa@rides.test": musa, "tunde@express.test": tunde} {
		if _, err := w.env.DB.Admin.Exec(t.Context(), `INSERT INTO whatsapp_bindings (user_id, number) SELECT id, $2 FROM users WHERE email = $1`, email, number); err != nil {
			t.Fatal(err)
		}
	}

	// Musa, added by phone: his business is asked to link.
	out := ada.must(201, "POST", "/v1/riders", map[string]any{"name": "Musa Bello", "phone": musa, "zones": []string{"Lekki"}, "fee_kobo": 100000})
	if out["link_requested"] != true || out["rider"].(map[string]any)["link_id"] != nil {
		t.Fatalf("adding a rider whose number is a business: %v", out)
	}
	asks := musaBiz.must(200, "GET", "/v1/links", nil)["links"].([]any)
	if len(asks) != 1 || asks[0].(map[string]any)["state"] != "asked_us" || asks[0].(map[string]any)["role"] != "customer" {
		t.Fatalf("the rider's business was asked: %v", asks)
	}
	// Added again: no second request, the same rider.
	ada.must(409, "POST", "/v1/riders", map[string]any{"name": "Musa Bello", "phone": musa})
	if n := len(musaBiz.must(200, "GET", "/v1/links", nil)["links"].([]any)); n != 1 {
		t.Fatalf("%d asks after adding twice", n)
	}
	// Yes: the rider is that business.
	link := asks[0].(map[string]any)["id"].(string)
	musaBiz.must(200, "POST", "/v1/links/"+link+"/accept", nil)
	w.srv.WaitBackground()
	rs := ada.must(200, "GET", "/v1/riders", nil)["riders"].([]any)
	if len(rs) != 1 || rs[0].(map[string]any)["link_id"] != link {
		t.Fatalf("the rider after the link: %v", rs)
	}

	// Tunde was linked with first (as any supplier), then added as a rider
	// by phone: named at once, no request.
	tundeBiz.must(200, "PUT", "/v1/link-settings", map[string]any{"findable": true})
	ada.must(201, "POST", "/v1/links", map[string]any{"business": tenantOf(t, tundeBiz), "role": "supplier"})
	tl := tundeBiz.must(200, "GET", "/v1/links", nil)["links"].([]any)[0].(map[string]any)
	tundeBiz.must(200, "POST", "/v1/links/"+tl["id"].(string)+"/accept", nil)
	w.srv.WaitBackground()
	out = ada.must(201, "POST", "/v1/riders", map[string]any{"name": "Tunde Ojo", "phone": tunde})
	if out["link_requested"] != false || out["rider"].(map[string]any)["link_id"] != tl["id"] {
		t.Fatalf("adding a rider already linked with: %v", out)
	}
	// Told: the rider's business has the request in its chats and alerts
	// (tellLinkAsked), as any link request.
	w.srv.WaitBackground()
	if n := len(musaBiz.must(200, "GET", "/v1/links", nil)["links"].([]any)); n != 1 {
		t.Fatalf("%d links on the rider's side", n)
	}
	// Past the day's blind requests, the rider is still added, with no request.
	for i := range 20 {
		if _, err := w.env.DB.Admin.Exec(t.Context(), `INSERT INTO link_blind_requests (id, tenant_id, kind, shown, address_hash, role, requested_by, expires_at)
			VALUES (gen_random_uuid(), $1, 'phone', '0803…0000', $2, 'supplier', 'user:test', now() + interval '30 days')`, w.biz, []byte(fmt.Sprintf("filler-%02d-%s", i, strings.Repeat("x", 22)))); err != nil {
			t.Fatal(err)
		}
	}
	w.tenant(t, "Chidi Logistics", "chidi@logistics.test")
	const chidi = "+2348031110034"
	if _, err := w.env.DB.Admin.Exec(t.Context(), `INSERT INTO whatsapp_bindings (user_id, number) SELECT id, $2 FROM users WHERE email = $1`, "chidi@logistics.test", chidi); err != nil {
		t.Fatal(err)
	}
	out = ada.must(201, "POST", "/v1/riders", map[string]any{"name": "Chidi Eze", "phone": chidi})
	if out["link_requested"] != false || out["rider"].(map[string]any)["link_id"] != nil {
		t.Fatalf("past the day's requests: %v", out)
	}
	// A rider whose number is nobody's business: added as before.
	out = ada.must(201, "POST", "/v1/riders", map[string]any{"name": "Bola Ade", "phone": "+2348031110033"})
	if out["link_requested"] != false || out["rider"].(map[string]any)["link_id"] != nil {
		t.Fatalf("a plain rider: %v", out)
	}
}

// TestRiderAvailability (docs/spec/chat-riders.md, phase 3): a rider's
// business says it is off, busy till a time or free from its own chat;
// the business that uses it sees that on its rider and Explore's card
// shows it; holding a delivery is busy whatever was said; a business no
// one uses as a rider has nothing to say yet.
func TestRiderAvailability(t *testing.T) {
	w, ada := newDispatchWorld(t, "Ada Stores")
	musaBiz := w.tenant(t, "Musa Rides", "musa@rides.test")
	musaBiz.must(200, "PUT", "/v1/business-profile", map[string]any{"name": "Musa Rides", "delivers": true, "vehicle": "bike"})
	musaBiz.must(200, "PUT", "/v1/link-settings", map[string]any{"findable": true})
	// Not anyone's rider yet: nothing to say.
	if got := musaBiz.must(200, "GET", "/v1/availability", nil); got["state"] != "free" || got["businesses"] != float64(0) {
		t.Fatalf("before any link: %v", got)
	}
	musaBiz.must(409, "PUT", "/v1/availability", map[string]any{"state": "off"})
	// The Explore card says nothing of a rider no one uses.
	card := func() map[string]any {
		t.Helper()
		for _, c := range ada.must(200, "GET", "/v1/explore?q=musa", nil)["results"].([]any) {
			if m := c.(map[string]any); m["name"] == "Musa Rides" {
				return m
			}
		}
		t.Fatal("no card")
		return nil
	}
	if c := card(); c["available"] != nil {
		t.Fatalf("a card before any link: %v", c)
	}
	// Linked and named as Ada's rider (as phase 1 does).
	ada.must(201, "POST", "/v1/links", map[string]any{"business": tenantOf(t, musaBiz), "role": "supplier"})
	l := musaBiz.must(200, "GET", "/v1/links", nil)["links"].([]any)[0].(map[string]any)
	musaBiz.must(200, "POST", "/v1/links/"+l["id"].(string)+"/accept", nil)
	w.srv.WaitBackground()
	const musa = "+2348031110041"
	// Musa taps in on WhatsApp (a rider reached by SMS alone is not offered).
	path := strings.TrimPrefix(ada.must(200, "GET", "/v1/riders", nil)["tap_in_url"].(string), "https://"+reachFormHost)
	loc, _ := url.Parse(w.page(t, path+"/whatsapp", nil).Header().Get("Location"))
	w.whatsapp(t, musa, loc.Query().Get("text"))
	m := ada.must(201, "POST", "/v1/riders", map[string]any{"name": "Musa Bello", "phone": musa, "zones": []string{"Lekki"}, "fee_kobo": 100000})["rider"].(map[string]any)
	ada.must(200, "PATCH", "/v1/riders/"+m["id"].(string), map[string]any{"link_id": l["id"]})
	rider := func() map[string]any {
		t.Helper()
		return ada.must(200, "GET", "/v1/riders", nil)["riders"].([]any)[0].(map[string]any)["presence"].(map[string]any)
	}
	if got := musaBiz.must(200, "GET", "/v1/availability", nil); got["state"] != "free" || got["businesses"] != float64(1) {
		t.Fatalf("linked: %v", got)
	}
	// Off: Ada's rider is off, the card says so, and an offer by rotation finds no one.
	if got := musaBiz.must(200, "PUT", "/v1/availability", map[string]any{"state": "off"}); got["state"] != "off" {
		t.Fatalf("off: %v", got)
	}
	if p := rider(); p["state"] != "off" {
		t.Errorf("Ada's rider when off: %v", p)
	}
	if c := card(); c["available"] != "off" {
		t.Errorf("the card when off: %v", c["available"])
	}
	ref := w.order(t, "none")
	if out := ada.must(200, "POST", "/v1/storefront/orders/"+ref+"/offer", map[string]any{}); out["status"] != "no_rider" {
		t.Errorf("offered to a rider who is off: %v", out)
	}
	// Busy till a time, then free again; a bad time and a time already
	// past today are refused (the words would take it as tomorrow's).
	musaBiz.must(400, "PUT", "/v1/availability", map[string]any{"state": "busy", "until": "soon"})
	if lagos, _ := time.LoadLocation("Africa/Lagos"); time.Now().In(lagos).Hour() >= 1 {
		musaBiz.must(400, "PUT", "/v1/availability", map[string]any{"state": "busy", "until": "00:30"})
	}
	// A plain Busy lasts an hour at least, whatever the clock says.
	if got := musaBiz.must(200, "PUT", "/v1/availability", map[string]any{"state": "busy"}); got["state"] != "busy" {
		t.Fatalf("plain busy: %v", got)
	} else if until, _ := time.Parse(time.RFC3339, got["until"].(string)); time.Until(until) < 59*time.Minute {
		t.Fatalf("plain busy ends too soon: %v", got["until"])
	}
	if got := musaBiz.must(200, "PUT", "/v1/availability", map[string]any{"state": "busy", "until": "11:59pm"}); got["state"] != "busy" || got["until"] == nil {
		t.Fatalf("busy: %v", got)
	}
	if p := rider(); p["state"] != "busy" {
		t.Errorf("Ada's rider when busy: %v", p)
	}
	if got := musaBiz.must(200, "PUT", "/v1/availability", map[string]any{"state": "free"}); got["state"] != "free" {
		t.Fatalf("free: %v", got)
	}
	if c := card(); c["available"] != "free" {
		t.Errorf("the card when free: %v", c["available"])
	}
	// Holding a delivery is busy, whatever was said.
	out := ada.must(200, "POST", "/v1/storefront/orders/"+ref+"/offer", map[string]any{})
	if out["status"] != "offered" {
		t.Fatalf("offer: %v", out)
	}
	oid := musaBiz.must(200, "GET", "/v1/trade-orders", nil)["orders"].([]any)[0].(map[string]any)["id"].(string)
	musaBiz.must(200, "POST", "/v1/trade-orders/"+oid+"/accept", nil)
	if got := musaBiz.must(200, "GET", "/v1/availability", nil); got["state"] != "busy" || got["open"] != float64(1) {
		t.Errorf("holding a delivery: %v", got)
	}
	if c := card(); c["available"] != "busy" {
		t.Errorf("the card while holding: %v", c["available"])
	}
}

// TestConfirmPaysPerDelivery (docs/spec/chat-riders.md, phase 4): riders
// are paid per delivery. The business's Confirm on a delivered delivery
// makes a payout of that one delivery and starts the pay-riders run; a
// rider with no bank account, or no paying set up, is refused in plain
// words; the paid card reaches both sides and the rider's money log once
// the run pays; cash the rider took at the door nets the fee; a fee handed
// over in cash is recorded as paid with no transfer.
func TestConfirmPaysPerDelivery(t *testing.T) {
	ctx := context.Background()
	w, ada := newDispatchWorld(t, "Ada Stores")
	musaBiz := w.tenant(t, "Musa Rides", "musa@rides.test")
	musaID := tenantOf(t, musaBiz)
	musaBiz.must(200, "PUT", "/v1/link-settings", map[string]any{"findable": true})
	ada.must(201, "POST", "/v1/links", map[string]any{"business": musaID, "role": "supplier"})
	l := musaBiz.must(200, "GET", "/v1/links", nil)["links"].([]any)[0].(map[string]any)
	musaBiz.must(200, "POST", "/v1/links/"+l["id"].(string)+"/accept", nil)
	w.srv.WaitBackground()
	link := l["id"].(string)
	const musa = "+2348031110051"
	path := strings.TrimPrefix(ada.must(200, "GET", "/v1/riders", nil)["tap_in_url"].(string), "https://"+reachFormHost)
	loc, _ := url.Parse(w.page(t, path+"/whatsapp", nil).Header().Get("Location"))
	w.whatsapp(t, musa, loc.Query().Get("text"))
	m := ada.must(201, "POST", "/v1/riders", map[string]any{"name": "Musa Bello", "phone": musa, "zones": []string{"Lekki"}, "fee_kobo": 100000})["rider"].(map[string]any)
	rid := m["id"].(string)
	ada.must(200, "PATCH", "/v1/riders/"+rid, map[string]any{"link_id": link})

	deliver := func(payment string) (string, string) {
		t.Helper()
		ref := w.order(t, payment)
		ada.must(200, "POST", "/v1/storefront/orders/"+ref+"/offer", map[string]any{})
		d := ada.must(200, "GET", "/v1/storefront/orders/"+ref+"/deliveries", nil)["deliveries"].([]any)[0].(map[string]any)
		id := uuid.MustParse(d["id"].(string))
		steps := []string{dispatch.Accept, dispatch.PickedUp, dispatch.Arrived}
		if payment == "on_delivery" {
			steps = append(steps, dispatch.Cash)
		} else {
			steps = append(steps, dispatch.Delivered)
		}
		for _, a := range steps {
			if _, err := w.srv.Dispatch.Press(ctx, w.biz, id, a); err != nil {
				t.Fatalf("%s: %v", a, err)
			}
		}
		return ref, id.String()
	}
	events := func(c *client) string {
		t.Helper()
		var out []string
		for _, x := range c.must(200, "GET", "/v1/link-chats/"+link+"/messages", nil)["messages"].([]any) {
			if msg := x.(map[string]any); msg["kind"] == "card" {
				out = append(out, msg["card"].(map[string]any)["event"].(string))
			}
		}
		return strings.Join(out, " ")
	}

	// Delivered, paid online: the fee waits on the business's word (a card
	// in the chat, with the chat tools on).
	ada.must(200, "PUT", "/v1/settings/chat-tools", map[string]any{"chat_tools": true})
	ref, _ := deliver("none")
	alerts := ada.must(200, "GET", "/v1/chat-alerts", nil)["alerts"].([]any)
	found := false
	for _, a := range alerts {
		if x := a.(map[string]any); x["kind"] == "confirm" && x["reference"] == ref {
			found = true
		}
	}
	if !found {
		t.Fatalf("no confirm alert: %v", alerts)
	}
	// No bank account yet: refused in plain words; in cash would do.
	if st, out := ada.do("POST", "/v1/storefront/orders/"+ref+"/confirm", map[string]any{}); st != 409 || !strings.Contains(fmt.Sprint(out["error"]), "no bank account") {
		t.Fatalf("no bank: %d %v", st, out)
	}
	// Ada's own WhatsApp, for the owner's words later (bound before the
	// authenticator, which the bank account needs).
	const adaWA = "+2348020000051"
	ada.must(202, "POST", "/v1/me/whatsapp", map[string]any{"number": adaWA, "password": testPassword})
	ada.must(200, "POST", "/v1/me/whatsapp/verify", map[string]any{"code": codeRe.FindString(w.graph.Last(t, adaWA).Text)})
	secret := enrol(t, ada)
	ada.must(200, "PUT", "/v1/riders/"+rid+"/bank", map[string]any{"bank_code": "058", "bank_name": "GTBank", "account_number": "0123456789", "totp": codeAfter(secret, 1)})
	// Paying riders not set up yet: refused, nothing confirmed.
	if st, out := ada.do("POST", "/v1/storefront/orders/"+ref+"/confirm", map[string]any{}); st != 409 || !strings.Contains(fmt.Sprint(out["error"]), "set up paying riders") {
		t.Fatalf("no payout set up: %d %v", st, out)
	}
	if d := ada.must(200, "GET", "/v1/storefront/orders/"+ref+"/deliveries", nil)["deliveries"].([]any)[0].(map[string]any); d["confirmed_at"] != nil || d["fee_paid"] != nil {
		t.Fatalf("confirmed though refused: %v", d)
	}
	// Set up (riders are paid from the Taskiem wallet, as the weekly test does).
	f := newFakeWallets(t)
	ops := &ridersconn.Late{}
	ops.Set(w.srv.Riders)
	ops.SetDispatch(w.srv.Dispatch)
	for _, c := range []*connector.Connector{iswallet.New(iswallet.Options{BaseURL: f.srv.URL, TestURL: f.srv.URL}), ridersconn.New(ops)} {
		if err := w.env.Registry.Register(c); err != nil {
			t.Fatal(err)
		}
	}
	w.env.Vault.Platform = secrets.PlatformClients{"iswallet": {"test": {"api_key": "isw_platform_test", "shared_client": "true"}}}
	ada.must(201, "POST", "/v1/settings/receive-money", map[string]any{"business_email": "ada@ada.test", "bvn": "22212345678"})
	ada.must(200, "PUT", "/v1/riders/pay/setup", map[string]any{"pay_day": 5, "pay_time": "17:30"})

	// Confirmed: a payout of this one delivery, its run started; both
	// sides see Confirmed; the fee is pending.
	out := ada.must(200, "POST", "/v1/storefront/orders/"+ref+"/confirm", map[string]any{})
	d := out["delivery"].(map[string]any)
	if d["confirmed_at"] == nil || d["fee_paid"] != "pending" || out["net_kobo"] != float64(100000) || out["run_id"] == nil {
		t.Fatalf("confirmed: %v", out)
	}
	ada.must(409, "POST", "/v1/storefront/orders/"+ref+"/confirm", map[string]any{})
	if got := events(ada); !strings.HasSuffix(got, "delivered confirmed") {
		t.Fatalf("Ada's cards: %s", got)
	}
	if got := events(musaBiz); !strings.HasSuffix(got, "delivered confirmed") {
		t.Fatalf("Musa's cards: %s", got)
	}
	var batch uuid.UUID
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT batch_id FROM rider_payouts WHERE tenant_id = $1 AND delivery_id = $2`, w.biz, d["id"]).Scan(&batch); err != nil {
		t.Fatal(err)
	}
	// The run pays it (here, as the connector's finish does): the paid
	// card on both sides, the fee in Musa's money log, the delivery paid.
	payouts := ada.must(200, "GET", "/v1/riders/pay", nil)["payouts"].([]any)
	if len(payouts) != 1 || payouts[0].(map[string]any)["status"] != "pending" {
		t.Fatalf("payouts: %v", payouts)
	}
	pid := uuid.MustParse(payouts[0].(map[string]any)["payout_id"].(string))
	if paid, _, err := w.srv.Dispatch.FinishBatch(ctx, w.biz, batch, []dispatch.Result{{PayoutID: pid, Status: "paid", AccountName: "MUSA BELLO"}}, false); err != nil || paid != 1 {
		t.Fatalf("finish: %d %v", paid, err)
	}
	if got := events(musaBiz); !strings.HasSuffix(got, "delivered confirmed paid") {
		t.Fatalf("Musa's cards after paying: %s", got)
	}
	if d := ada.must(200, "GET", "/v1/storefront/orders/"+ref+"/deliveries", nil)["deliveries"].([]any)[0].(map[string]any); d["fee_paid"] != "bank" {
		t.Fatalf("after paying: %v", d["fee_paid"])
	}
	var income int64
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT COALESCE(sum(amount_kobo), 0) FROM money_entries WHERE tenant_id = $1 AND direction = 'in' AND reference LIKE 'delivery-fee:%'`, musaID).Scan(&income); err != nil || income != 100000 {
		t.Fatalf("Musa's income: %d %v", income, err)
	}
	// Nothing is left for a weekly batch.
	if lines := ada.must(200, "GET", "/v1/riders/pay", nil)["lines"].([]any); len(lines) != 0 {
		t.Fatalf("still due: %v", lines)
	}

	// Cash at the door: Musa holds the order's cash; the fee comes off it
	// and nothing is paid by bank.
	ref2, _ := deliver("on_delivery")
	out = ada.must(200, "POST", "/v1/storefront/orders/"+ref2+"/confirm", map[string]any{})
	if out["net_kobo"] != float64(0) || out["run_id"] != nil || out["delivery"].(map[string]any)["fee_paid"] != "bank" {
		t.Fatalf("netted: %v", out)
	}
	// The fee came off what Musa owes: a remittance part netted from the payout.
	var netted int64
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT COALESCE(sum(amount_kobo), 0) FROM rider_remittance_parts WHERE tenant_id = $1 AND how = 'netted'`, w.biz).Scan(&netted); err != nil || netted != 100000 {
		t.Fatalf("netted: %d %v", netted, err)
	}
	if got := events(musaBiz); !strings.HasSuffix(got, "delivered paid") {
		t.Fatalf("Musa's cards after netting: %s", got)
	}

	// Handed over in cash: paid with no transfer, a cash payment out.
	ref3, _ := deliver("none")
	out = ada.must(200, "POST", "/v1/storefront/orders/"+ref3+"/confirm", map[string]any{"how": "cash"})
	if out["run_id"] != nil || out["delivery"].(map[string]any)["fee_paid"] != "cash" {
		t.Fatalf("in cash: %v", out)
	}
	var cashOut int64
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT COALESCE(sum(amount_kobo), 0) FROM money_entries WHERE tenant_id = $1 AND direction = 'out' AND channel = 'cash' AND reference LIKE 'rider-fee:%'`, w.biz).Scan(&cashOut); err != nil || cashOut != 100000 {
		t.Fatalf("the cash payment out: %d %v", cashOut, err)
	}
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT COALESCE(sum(amount_kobo), 0) FROM money_entries WHERE tenant_id = $1 AND direction = 'in' AND reference LIKE 'delivery-fee:%'`, musaID).Scan(&income); err != nil || income != 300000 {
		t.Fatalf("Musa's income after three: %d %v", income, err)
	}
	// Confirmed already: refused, over the API and from the owner's WhatsApp.
	ada.must(409, "POST", "/v1/storefront/orders/"+ref3+"/confirm", map[string]any{"how": "cash"})
	if got := w.whatsapp(t, adaWA, "confirm "+ref3); len(got) != 1 || !strings.Contains(got[0].Text, "could not confirm") {
		t.Fatalf("confirm twice from WhatsApp: %+v", got)
	}
	// And a fourth, confirmed from WhatsApp in cash.
	ref4, _ := deliver("none")
	if got := w.whatsapp(t, adaWA, "confirm "+ref4+" cash"); len(got) != 1 || !strings.Contains(got[0].Text, "paid ₦1,000.00 in cash") && !strings.Contains(got[0].Text, "in cash") {
		t.Fatalf("confirm in cash from WhatsApp: %+v", got)
	}
}

// TestDispatchCompany (docs/spec/chat-riders.md, phase 5): a dispatch
// company is a rider business with riders of its own. A business's offer
// reaches the company as a delivery order and is offered on inside the
// company by its own rotation; the company's rider's steps from their
// WhatsApp move the company's order and the business's delivery alike,
// named after the rider on the business's cards; the company confirms its
// own delivery to pay its rider, as any business does.
func TestDispatchCompany(t *testing.T) {
	w, ada := newDispatchWorld(t, "Ada Stores")
	swift := w.tenant(t, "Swift Dispatch", "swift@dispatch.test")
	swiftID := tenantOf(t, swift)
	swift.must(200, "PUT", "/v1/link-settings", map[string]any{"findable": true})
	ada.must(201, "POST", "/v1/links", map[string]any{"business": swiftID, "role": "supplier"})
	l := swift.must(200, "GET", "/v1/links", nil)["links"].([]any)[0].(map[string]any)
	swift.must(200, "POST", "/v1/links/"+l["id"].(string)+"/accept", nil)
	w.srv.WaitBackground()
	link := l["id"].(string)
	// Ada's rider is the company (named by hand; the company's own number
	// is reached by SMS, so Ada offers to it by name).
	const swiftPhone = "+2348031110061"
	m := ada.must(201, "POST", "/v1/riders", map[string]any{"name": "Swift Dispatch", "phone": swiftPhone, "zones": []string{"Lekki"}, "fee_kobo": 150000})["rider"].(map[string]any)
	ada.must(200, "PATCH", "/v1/riders/"+m["id"].(string), map[string]any{"link_id": link})
	// The company's own rider, Musa, on WhatsApp through the company's tap-in link.
	const musa = "+2348031110062"
	path := strings.TrimPrefix(swift.must(200, "GET", "/v1/riders", nil)["tap_in_url"].(string), "https://"+reachFormHost)
	loc, _ := url.Parse(w.page(t, path+"/whatsapp", nil).Header().Get("Location"))
	w.whatsapp(t, musa, loc.Query().Get("text"))
	swift.must(201, "POST", "/v1/riders", map[string]any{"name": "Musa Bello", "phone": musa, "fee_kobo": 80000})

	// Ada offers the order to the company: the company's order, and inside
	// the company an offer to Musa, picked up at Ada's.
	ref := w.order(t, "none")
	out := ada.must(200, "POST", "/v1/storefront/orders/"+ref+"/offer", map[string]any{"rider_id": m["id"]})
	if out["status"] != "offered" || out["rider"] != "Swift Dispatch" {
		t.Fatalf("offer %v", out)
	}
	w.tick(t)
	orders := swift.must(200, "GET", "/v1/trade-orders", nil)["orders"].([]any)
	if len(orders) != 1 || orders[0].(map[string]any)["status"] != "received" {
		t.Fatalf("the company's orders: %v", orders)
	}
	sref := orders[0].(map[string]any)["order_reference"].(string)
	inner := swift.must(200, "GET", "/v1/storefront/orders/"+sref+"/deliveries", nil)["deliveries"].([]any)
	if len(inner) != 1 || inner[0].(map[string]any)["status"] != "offered" || inner[0].(map[string]any)["rider"] != "Musa Bello" {
		t.Fatalf("the company's own offer: %v", inner)
	}
	offer := w.graph.Last(t, musa)
	if !strings.Contains(offer.Text, "A delivery for you: "+sref) || len(offer.Buttons) != 2 {
		t.Fatalf("Musa's offer: %+v", offer)
	}

	// Musa accepts: the company's order is accepted and Ada's delivery
	// too, her card naming Musa; then on to delivered, step by step.
	w.press(t, musa, offer.Buttons[0].ID)
	if got := swift.must(200, "GET", "/v1/trade-orders", nil)["orders"].([]any)[0].(map[string]any)["status"]; got != "accepted" {
		t.Fatalf("the company's order after Musa accepted: %v", got)
	}
	ds := ada.must(200, "GET", "/v1/storefront/orders/"+ref+"/deliveries", nil)["deliveries"].([]any)
	if ds[0].(map[string]any)["status"] != "accepted" {
		t.Fatalf("Ada's delivery: %v", ds)
	}
	cards := func(c *client) []map[string]any {
		t.Helper()
		var out []map[string]any
		for _, x := range c.must(200, "GET", "/v1/link-chats/"+link+"/messages", nil)["messages"].([]any) {
			if msg := x.(map[string]any); msg["kind"] == "card" {
				out = append(out, msg["card"].(map[string]any))
			}
		}
		return out
	}
	ac := cards(ada)
	if last := ac[len(ac)-1]; last["event"] != "accepted" || !strings.Contains(fmt.Sprint(last["note"]), "Musa Bello") {
		t.Fatalf("Ada's card after Musa accepted: %v", last)
	}
	w.tick(t)
	for _, want := range []string{"Picked up", "Arrived", "Delivered"} {
		msg := w.graph.Last(t, musa)
		var id string
		for _, b := range msg.Buttons {
			if b.Title == want {
				id = b.ID
			}
		}
		if id == "" {
			t.Fatalf("no %s button: %+v", want, msg)
		}
		w.press(t, musa, id)
		w.tick(t)
	}
	order := ada.must(200, "GET", "/v1/storefront/orders/"+ref, nil)
	if v, ok := order["order"].(map[string]any); ok {
		order = v
	}
	if order["status"] != "completed" {
		t.Fatalf("Ada's order: %v", order["status"])
	}
	// The company's order, as a rider's delivery order reads once done.
	if got := swift.must(200, "GET", "/v1/trade-orders", nil)["orders"].([]any)[0].(map[string]any)["status"]; got != "delivered" {
		t.Fatalf("the company's order at the end: %v", got)
	}
	var events []string
	for _, c := range cards(swift) {
		events = append(events, c["event"].(string))
	}
	if got := strings.Join(events, " "); got != "offered accepted picked_up arrived delivered" {
		t.Fatalf("the company's cards: %s", got)
	}
	// The company pays Musa for its own delivery as any business does.
	if got := swift.must(200, "POST", "/v1/storefront/orders/"+sref+"/confirm", map[string]any{"how": "cash"}); got["delivery"].(map[string]any)["fee_paid"] != "cash" {
		t.Fatalf("the company paying its rider: %v", got)
	}
}
