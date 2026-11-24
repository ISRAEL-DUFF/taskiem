package api_test

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/chatlinks"
	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/push"
	"github.com/israel-duff/taskiem/engine/sender"
	"github.com/israel-duff/taskiem/engine/storefront"
)

// "Make a new link" and the chat settings publish the page again with only
// its chat button changed: the owner's unpublished draft stays
// unpublished, and nothing goes to the moderators again (stage 2 review).
func TestChatLinkRepublishKeepsTheDraft(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()
	withChatLinks(w)
	w.srv.PublicHosts = []string{"shops.test"}
	owner := w.tenant(t, "Mama's Kitchen", "mama@mama.test")
	tenant := tenantUUID(t, owner)
	verifyPhone(t, w, owner, "phone", "business")
	owner.must(200, "PUT", "/v1/storefront", map[string]any{"slug": "mama-kitchen", "draft": map[string]any{"name": "Mama's Kitchen", "whatsapp": "+2348031234567"}})
	owner.must(200, "POST", "/v1/storefront/publish", nil)
	reviews := func() int {
		t.Helper()
		var n int
		if err := w.env.DB.Admin.QueryRow(ctx, `SELECT count(*) FROM storefront_reviews WHERE tenant_id = $1`, tenant).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	before := reviews()

	// The owner works on a draft they have not published.
	owner.must(200, "PUT", "/v1/storefront", map[string]any{"slug": "mama-kitchen", "draft": map[string]any{"name": "Secret new name", "whatsapp": "+2348031234567"}})
	fresh := owner.must(200, "POST", "/v1/customers/chat-link", nil)["code"].(string)
	m := snapshotModel(t, w, tenant)
	if m.ChatLink != chatBase+"/m/"+fresh || m.Card.Name != "Mama's Kitchen" || m.WhatsLink == "" {
		t.Fatalf("after a new link: chat %q, name %q, WhatsApp %q", m.ChatLink, m.Card.Name, m.WhatsLink)
	}
	owner.must(200, "PUT", "/v1/customers/chat-settings", map[string]any{"storefront_button": "taskiem"})
	m = snapshotModel(t, w, tenant)
	if m.ChatLink == "" || m.WhatsLink != "" || m.Card.Name != "Mama's Kitchen" {
		t.Fatalf("after the button: chat %q, name %q, WhatsApp %q", m.ChatLink, m.Card.Name, m.WhatsLink)
	}
	if strings.Contains(string(jsonOf(t, m)), "Secret new name") {
		t.Fatal("the draft was published")
	}
	if reviews() != before {
		t.Fatalf("sent to the moderators again: %d, was %d", reviews(), before)
	}
	sf := owner.must(200, "GET", "/v1/storefront", nil)
	if sf["status"] != "published" || !strings.Contains(string(jsonOf(t, sf)), "Secret new name") {
		t.Fatalf("the storefront: %v", sf)
	}
	// Changing both settings at once, then one: neither undoes the other.
	owner.must(200, "PUT", "/v1/customers/chat-settings", map[string]any{"link_in_updates": false})
	if got := owner.must(200, "PUT", "/v1/customers/chat-settings", map[string]any{"takes_chats": true}); got["storefront_button"] != "taskiem" || got["link_in_updates"] != false {
		t.Fatalf("settings: %v", got)
	}
}

// With the Orders area off (and the shop with it), a business takes no
// chats: its link's card says so, no chat starts, and a chat already open
// takes no messages; on again, it takes them (stage 2 review).
func TestChatsWithOrdersOff(t *testing.T) {
	w := newDoorShopWorld(t)
	withChatLinks(w.world)
	code := w.owner.must(200, "GET", "/v1/customers/chat-link", nil)["code"].(string)
	anon := &client{t: t, base: w.base}
	tolu, _ := w.personIn(t, "+2348031110031")
	id := tolu.must(201, "POST", "/v1/me/chats", map[string]any{"code": code})["id"].(string)
	tolu.must(201, "POST", "/v1/me/chats/"+id+"/messages", map[string]any{"text": "Hello", "client_id": "h1"})

	w.owner.must(200, "PUT", "/v1/settings/areas", map[string]any{"areas": map[string]bool{"orders": false, "storefront": false}, "confirm": true})
	if card := anon.must(200, "GET", "/v1/chat-links/"+code, nil); card["takes_chats"] != false {
		t.Fatalf("the card with Orders off: %v", card)
	}
	ade, _ := w.personIn(t, "+2348031110032")
	if st, out := ade.do("POST", "/v1/me/chats", map[string]any{"code": code}); st < 400 {
		t.Fatalf("a chat started with Orders off: %d %v", st, out)
	}
	if st, out := tolu.do("POST", "/v1/me/chats/"+id+"/messages", map[string]any{"text": "Still there?", "client_id": "h2"}); st < 400 {
		t.Fatalf("a message with Orders off: %d %v", st, out)
	}
	// The door itself refuses, whatever the API does.
	if err := doorStartWith(context.Background(), w, personID(t, ade), w.biz, map[string]any{"via": "shop"}); err == nil {
		t.Fatal("the door opened a chat with Orders off")
	}

	w.owner.must(200, "PUT", "/v1/settings/areas", map[string]any{"areas": map[string]bool{"orders": true, "storefront": true}})
	if card := anon.must(200, "GET", "/v1/chat-links/"+code, nil); card["takes_chats"] != true {
		t.Fatalf("the card with Orders on: %v", card)
	}
	ade.must(201, "POST", "/v1/me/chats", map[string]any{"code": code})
}

// Push on a person's own side (founder's decision, 2026-11-17): a device
// registered from a personal session (or "for You" from a business session)
// has no business and stays whatever memberships the person has; a business
// message pushes "{business}: {first words}" to it, opening the chat on You,
// and no SMS goes; order updates push too; with no device, a reply from
// someone at the business goes by SMS (once an hour), Taskiem's automatic
// words never do.
func TestPushOnYou(t *testing.T) {
	ctx := context.Background()
	w := newDoorShopWorld(t)
	withChatLinks(w.world)
	w.srv.Push = pushSender(t)
	w.srv.Push.Validate = func(string) error { return nil }
	w.srv.Answered = &chatlinks.Tell{Pool: w.env.DB.App, Links: w.srv.ChatLinks, Messages: &sender.Queue{Vault: w.env.Vault, HashKey: bytes.Repeat([]byte{8}, 32)},
		Push: w.srv.PersonPusher()}
	svc := newPushService(t)
	sub := func(path string, b *pushBrowser) map[string]any {
		return map[string]any{"endpoint": svc.srv.URL + path, "keys": map[string]any{"p256dh": b.p256dh, "auth": b.au}, "label": "Safari on my phone"}
	}
	sms := func() int {
		t.Helper()
		var n int
		if err := w.env.DB.Admin.QueryRow(ctx, `SELECT count(*) FROM platform_messages WHERE tenant_id = $1 AND channel = 'sms' AND purpose = 'notice'`, w.biz).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}

	tolu, _ := w.personIn(t, "+2348031110041")
	id := tolu.must(201, "POST", "/v1/me/chats", map[string]any{"business_id": w.biz, "from": "shop"})["id"].(string)
	tolu.must(201, "POST", "/v1/me/chats/"+id+"/messages", map[string]any{"text": "Hello", "client_id": "h1"})
	var thread string
	for _, c := range w.owner.must(200, "GET", "/v1/customer-chats", nil)["chats"].([]any) {
		if c.(map[string]any)["name"] == "Tolu Ade" {
			thread = c.(map[string]any)["id"].(string)
		}
	}

	// The person's own device, from their personal session: no business,
	// the kind you only.
	br := newPushBrowser(t)
	out := tolu.must(200, "PUT", "/v1/me/push", sub("/tolu", br))
	if out["side"] != "you" || len(out["devices"].([]any)) != 1 || len(out["kinds"].([]any)) != 1 || out["kinds"].([]any)[0] != "you" {
		t.Fatalf("the person's own device: %v", out)
	}
	if k := out["devices"].([]any)[0].(map[string]any)["kinds"].([]any); len(k) != 1 || k[0] != "you" {
		t.Fatalf("its kinds: %v", k)
	}
	devID := out["devices"].([]any)[0].(map[string]any)["id"].(string)
	tolu.must(400, "PATCH", "/v1/me/push/"+devID, map[string]any{"kinds": []string{"business"}})
	var tenantOf *uuid.UUID
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT tenant_id FROM push_subscriptions WHERE id = $1`, devID).Scan(&tenantOf); err != nil || tenantOf != nil {
		t.Fatalf("the device's business: %v %v", tenantOf, err)
	}
	// The business sees none of it.
	if d := w.owner.must(200, "GET", "/v1/me/push", nil)["devices"].([]any); len(d) != 0 {
		t.Fatalf("the business sees the person's device: %v", d)
	}

	// A reply from the business: pushed, opening the chat on You; no SMS.
	w.owner.must(201, "POST", "/v1/customer-chats/"+thread+"/messages", map[string]any{"text": "Hi Tolu, your jollof rice is ready for pickup", "client_id": "a1"})
	pushSettle(t, w.srv)
	if svc.count("/tolu") != 1 {
		t.Fatalf("pushes: %d", svc.count("/tolu"))
	}
	p := br.decrypt(t, svc.bodies["/tolu"][0])
	if p.Title != "Mama's Kitchen" || !strings.HasPrefix(p.Body, "Hi Tolu, your jollof rice") || p.Link != "/you/chats/"+id || p.Business != "" || p.Kind != push.KindYou || p.Unread != 1 {
		t.Fatalf("the push: %+v", p)
	}
	if sms() != 0 {
		t.Fatalf("texted with a device: %d", sms())
	}
	// The preview off: only that a business wrote.
	tolu.must(200, "PATCH", "/v1/me/push/"+devID, map[string]any{"preview": false})
	w.owner.must(201, "POST", "/v1/customer-chats/"+thread+"/messages", map[string]any{"text": "Your total is 4500", "client_id": "a2"})
	pushSettle(t, w.srv)
	if p := br.decrypt(t, svc.bodies["/tolu"][1]); p.Title != "Taskiem" || strings.Contains(p.Body, "4500") || strings.Contains(p.Title, "Mama") {
		t.Fatalf("preview off: %+v", p)
	}
	tolu.must(200, "PATCH", "/v1/me/push/"+devID, map[string]any{"preview": true})

	// An automatic order update pushes too.
	ord := storefront.Order{}
	ref := placeAppOrder(t, w, tolu, id)
	if err := db.InTenantTx(ctx, w.env.DB.App, []uuid.UUID{w.biz}, func(tx pgx.Tx) error {
		var err error
		ord, err = w.srv.Orders.GetOrder(ctx, tx, w.biz, ref)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	pushSettle(t, w.srv)
	n := svc.count("/tolu")
	if err := w.srv.ChatNotify(ctx, w.biz, ord, "Your order is on its way."); err != nil {
		t.Fatal(err)
	}
	pushSettle(t, w.srv)
	if svc.count("/tolu") != n+1 {
		t.Fatalf("the order update was not pushed: %d, was %d", svc.count("/tolu"), n)
	}
	if p := br.decrypt(t, svc.bodies["/tolu"][n]); p.Body != "Your order is on its way." || p.Link != "/you/chats/"+id {
		t.Fatalf("the order update's push: %+v", p)
	}
	if sms() != 0 {
		t.Fatalf("texted: %d", sms())
	}

	// No device (an iPhone's browser not on the home screen): the order
	// update is not texted; a reply from someone at the business is, once
	// an hour.
	tolu.must(200, "DELETE", "/v1/me/push/"+devID, nil)
	if err := w.srv.ChatNotify(ctx, w.biz, ord, "Your order has arrived."); err != nil {
		t.Fatal(err)
	}
	w.srv.WaitBackground()
	if sms() != 0 {
		t.Fatalf("an automatic update was texted: %d", sms())
	}
	w.owner.must(201, "POST", "/v1/customer-chats/"+thread+"/messages", map[string]any{"text": "Are you coming?", "client_id": "a3"})
	w.owner.must(201, "POST", "/v1/customer-chats/"+thread+"/messages", map[string]any{"text": "We close at 6", "client_id": "a4"})
	w.srv.WaitBackground()
	if sms() != 1 {
		t.Fatalf("texted with no device: %d, want 1", sms())
	}

	// A business member's device for You (side=you, from the business
	// session) has no business, and stays when they lose their role.
	vic := addMember(t, w.world, w.owner, "vic", "viewer")
	vicID := personID(t, vic)
	vic.must(200, "PUT", "/v1/me/push", sub("/vic-business", newPushBrowser(t)))
	if got := vic.must(200, "PUT", "/v1/me/push?side=you", sub("/vic-you", newPushBrowser(t))); got["side"] != "you" || len(got["devices"].([]any)) != 1 {
		t.Fatalf("for You from a business session: %v", got)
	}
	w.owner.must(204, "DELETE", "/v1/members/"+vicID.String()+"/roles/viewer", nil)
	var business, own int
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT count(*) FILTER (WHERE tenant_id IS NOT NULL), count(*) FILTER (WHERE tenant_id IS NULL)
		FROM push_subscriptions WHERE user_id = $1`, vicID).Scan(&business, &own); err != nil || business != 0 || own != 1 {
		t.Fatalf("after losing the role: business %d, own %d, %v", business, own, err)
	}
}

// placeAppOrder places an order in the app through the person's chat and
// records it; its reference.
func placeAppOrder(t *testing.T, w *doorShopWorld, person *client, chat string) string {
	t.Helper()
	items := person.must(200, "GET", "/v1/me/chats/"+chat+"/shop", nil)["items"].([]any)
	var item string
	for _, x := range items {
		if x.(map[string]any)["name"] == "Jollof rice" {
			item = x.(map[string]any)["id"].(string)
		}
	}
	order := map[string]any{"lines": []any{map[string]any{"item_id": item, "quantity": 1}}, "fulfilment": "pickup"}
	ref := person.must(201, "POST", "/v1/me/chats/"+chat+"/orders", order, "Idempotency-Key", "push-o1")["order"].(map[string]any)["reference"].(string)
	if err := w.srv.Orders.Drain(context.Background()); err != nil {
		t.Fatal(err)
	}
	return ref
}
