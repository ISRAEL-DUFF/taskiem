package api_test

import (
	"bytes"
	"context"
	"fmt"
	"mime/multipart"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/api"
	sfconn "github.com/israel-duff/taskiem/connectors/storefront"
	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/storefront"
	"github.com/israel-duff/taskiem/templates"
)

// answers is a CustomerAnswerHook that keeps what it hears.
type answers struct {
	mu  sync.Mutex
	got []api.CustomerAnswer
}

func (a *answers) BusinessAnswered(_ context.Context, x api.CustomerAnswer) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.got = append(a.got, x)
}

func (a *answers) list() []api.CustomerAnswer {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]api.CustomerAnswer(nil), a.got...)
}

// postFile posts a file to path as a multipart form.
func (c *client) postFile(t *testing.T, path, name string, data []byte, fields map[string]string) (int, map[string]any) {
	t.Helper()
	var b bytes.Buffer
	mw := multipart.NewWriter(&b)
	fw, _ := mw.CreateFormFile("file", name)
	_, _ = fw.Write(data)
	for k, v := range fields {
		_ = mw.WriteField(k, v)
	}
	_ = mw.Close()
	return c.rawPost(t, path, mw.FormDataContentType(), b.Bytes())
}

// doorShopWorld is a world with a business that sells in the app: its
// published shop (Jollof rice and Chapman, pickup or delivery to Yaba),
// open every hour, its order workflow, and files scanned.
type doorShopWorld struct {
	*waTablesWorld
	sms   *fakeSMS
	owner *client
	biz   uuid.UUID
}

func newDoorShopWorld(t *testing.T) *doorShopWorld {
	t.Helper()
	w := shopWorld(t)
	sms := &fakeSMS{}
	w.srv.CodeSMS = sms
	w.srv.LoginBurst = 100
	withLinkedOrders(w.world)
	w.srv.Orders.ChatNotify = w.srv.ChatNotify
	if err := w.env.Registry.Register(sfconn.New(w.srv.Orders)); err != nil {
		t.Fatal(err)
	}
	w.srv.Media = newProjectStorage(t).service() // each business its own storage
	w.srv.Media.Scanner = chatScanner{}
	owner := w.tenant(t, "Mama's Kitchen", "owner@mamas.test")
	verifyPhone(t, w.world, owner, "phone", "business")
	owner.must(200, "PUT", "/v1/storefront", map[string]any{"slug": "mamas-kitchen", "draft": map[string]any{"name": "Mama's Kitchen", "hours": hoursOpen(true)}})
	table := owner.must(201, "POST", "/v1/storefront/catalogue/make", map[string]any{"kind": "products"})["table_id"].(string)
	for _, it := range []map[string]any{{"name": "Jollof rice", "price": 4500, "available": true}, {"name": "Chapman", "price": 1500, "available": true}} {
		owner.must(201, "POST", "/v1/tables/"+table+"/rows", map[string]any{"row": it})
	}
	owner.must(200, "POST", "/v1/storefront/catalogue/refresh", nil)
	tpl, _ := templates.Default().Get("storefront-order")
	def, err := tpl.Instantiate(map[string]any{}, "Orders")
	if err != nil {
		t.Fatal(err)
	}
	publishFlow(t, owner, string(def))
	owner.must(200, "PUT", "/v1/storefront/actions/order", map[string]any{"enabled": true, "payment": "none",
		"options": map[string]any{"pickup": true, "delivery": true, "zones": []any{map[string]any{"name": "Yaba", "fee_kobo": 100000}}}})
	owner.must(200, "POST", "/v1/storefront/publish", nil)
	return &doorShopWorld{waTablesWorld: w, sms: sms, owner: owner, biz: tenantIDNamed(t, w.world, "Mama's Kitchen")}
}

// hoursOpen is opening hours open all day every day, or open only half a
// day away from now (so closed now).
func hoursOpen(now bool) map[string]any {
	// Open all day: to 23:59, and from 23:59 past midnight.
	spans := []any{map[string]any{"open": "00:00", "close": "23:59"}, map[string]any{"open": "23:59", "close": "00:30"}}
	if !now {
		h := (time.Now().In(storefront.Lagos).Hour() + 12) % 24
		spans = []any{map[string]any{"open": fmt.Sprintf("%02d:00", h), "close": fmt.Sprintf("%02d:30", h)}}
	}
	days := []any{}
	for range 7 {
		days = append(days, spans)
	}
	return map[string]any{"days": days}
}

func (w *doorShopWorld) hours(t *testing.T, open bool) {
	t.Helper()
	w.owner.must(200, "PUT", "/v1/storefront", map[string]any{"slug": "mamas-kitchen", "draft": map[string]any{"name": "Mama's Kitchen", "hours": hoursOpen(open)}})
	// The hours customers see are the published page's.
	w.owner.must(200, "POST", "/v1/storefront/publish", nil)
}

// personIn makes a person with a phone (the code by SMS) and signs them in
// to their own side.
func (w *doorShopWorld) personIn(t *testing.T, number string) (*client, map[string]any) {
	t.Helper()
	anon := &client{t: t, base: w.base}
	anon.must(202, "POST", "/v1/auth/phone/start", map[string]any{"phone": number, "via": "sms"})
	out := anon.must(200, "POST", "/v1/auth/phone/verify", map[string]any{"phone": number, "code": w.sms.code(t, number), "bearer": true})
	if out["status"] == "new" {
		out = anon.must(201, "POST", "/v1/auth/phone/create", map[string]any{"ticket": out["ticket"], "name": "Tolu Ade", "adult": true, "terms": true, "bearer": true})
	}
	return &client{t: t, base: w.base, token: out["token"].(string)}, out
}

func personID(t *testing.T, c *client) uuid.UUID {
	t.Helper()
	return uuid.MustParse(c.must(200, "GET", "/v1/me", nil)["user"].(map[string]any)["id"].(string))
}

func (w *doorShopWorld) audits(t *testing.T, action string, actor uuid.UUID) int {
	t.Helper()
	var n int
	if err := w.env.DB.Admin.QueryRow(context.Background(), `SELECT count(*) FROM audit_log WHERE tenant_id = $1 AND action = $2 AND actor_type = 'person' AND actor_id = $3`,
		w.biz, action, actor.String()).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// The customer door (taskiem-for-everyone §8): a person starts a chat with
// a business from their own side; both copies of each message are kept,
// the business's sealed; replies, read marks and typing cross both ways;
// offers only after a yes, two a week; cards; their shop, an order of the
// kind app through the business's order workflow, its pay link; files
// checked and kept by each side; blocks both ways; a report; the closed
// reply, Needs you and the owner told; an area turned off; every entry
// audited in the business's chain.
func TestCustomerDoor(t *testing.T) {
	ctx := context.Background()
	w := newDoorShopWorld(t)
	owner := w.owner
	tolu, _ := w.personIn(t, "+2348031110001")
	toluID := personID(t, tolu)

	// Nothing yet; a business that is not one, and a link no one has.
	if got := tolu.must(200, "GET", "/v1/me/chats", nil); len(got["chats"].([]any)) != 0 || got["unread"] != float64(0) {
		t.Fatalf("chats: %v", got)
	}
	tolu.must(404, "POST", "/v1/me/chats", map[string]any{"business_id": uuid.NewString()})
	tolu.must(404, "POST", "/v1/me/chats", map[string]any{"code": "abcdefghjk"})
	tolu.must(400, "POST", "/v1/me/chats", map[string]any{})

	// Started from its shop: told what the business sees, asked about offers once.
	chat := tolu.must(201, "POST", "/v1/me/chats", map[string]any{"business_id": w.biz, "from": "shop"})
	id := chat["id"].(string)
	if chat["name"] != "Mama's Kitchen" || chat["ask_offers"] != true || chat["offers"] != "unasked" || !strings.Contains(chat["notice"].(string), "name and phone number") {
		t.Fatalf("started: %v", chat)
	}
	if again := tolu.must(200, "POST", "/v1/me/chats", map[string]any{"business_id": w.biz}); again["id"] != id {
		t.Fatalf("again: %v", again)
	}
	if w.audits(t, "customer.start", toluID) != 1 {
		t.Fatalf("the start is not audited in the business's chain")
	}

	// The business's side: one thread, in the app, with the person's name and phone.
	threads := owner.must(200, "GET", "/v1/customer-chats", nil)["chats"].([]any)
	if len(threads) != 1 {
		t.Fatalf("threads: %v", threads)
	}
	th := threads[0].(map[string]any)
	thread := th["id"].(string)
	if th["line"] != "app" || th["name"] != "Tolu Ade" || th["phone"] != "+2348031110001" || th["window_open"] != true || th["stopped"] != false {
		t.Fatalf("thread: %v", th)
	}

	// A message: in both copies, the business's sealed; the owner told.
	m := tolu.must(201, "POST", "/v1/me/chats/"+id+"/messages", map[string]any{"text": "Is the jollof ready?", "client_id": "m1"})
	if m["mine"] != true || m["kind"] != "text" {
		t.Fatalf("sent: %v", m)
	}
	if again := tolu.must(200, "POST", "/v1/me/chats/"+id+"/messages", map[string]any{"text": "Is the jollof ready?", "client_id": "m1"}); again["id"] != m["id"] {
		t.Fatalf("a repeat: %v", again)
	}
	bm := owner.must(200, "GET", "/v1/customer-chats/"+thread+"/messages", nil)["messages"].([]any)
	if len(bm) != 1 || bm[0].(map[string]any)["id"] != m["id"] || bm[0].(map[string]any)["body"] != "Is the jollof ready?" || bm[0].(map[string]any)["mine"] != false {
		t.Fatalf("the business's copy: %v", bm)
	}
	var clear int
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT count(*) FROM customer_messages WHERE tenant_id = $1 AND body::text LIKE '%jollof%'`, w.biz).Scan(&clear); err != nil || clear != 0 {
		t.Fatalf("kept in the clear: %d %v", clear, err)
	}
	var told int
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT count(*) FROM notifications WHERE tenant_id = $1 AND group_key = $2`, w.biz, "customer:"+thread).Scan(&told); err != nil || told == 0 {
		t.Fatalf("nobody told: %d %v", told, err)
	}

	// Typing, both ways.
	tolu.must(204, "POST", "/v1/me/chats/"+id+"/typing", nil)
	if got := owner.must(200, "GET", "/v1/customer-chats/"+thread, nil); got["typing"] != true || got["offers"] != "unasked" {
		t.Fatalf("typing seen: %v", got)
	}
	owner.must(204, "POST", "/v1/customer-chats/"+thread+"/typing", nil)
	if got := tolu.must(200, "GET", "/v1/me/chats/"+id, nil); got["typing"] != true || got["hours"].(map[string]any)["open"] != true {
		t.Fatalf("typing seen: %v", got)
	}

	// The reply: always a message (no window), in the person's copy.
	heard := &answers{}
	w.srv.CustomerAnswered = heard
	r := owner.must(201, "POST", "/v1/customer-chats/"+thread+"/messages", map[string]any{"text": "Yes, in 20 minutes", "client_id": "r1"})
	if r["delivery"] != "delivered" || r["sent_as"] != nil {
		t.Fatalf("reply: %v", r)
	}
	owner.must(200, "POST", "/v1/customer-chats/"+thread+"/messages", map[string]any{"text": "Yes, in 20 minutes", "client_id": "r1"})
	// The hook for the browser's SMS fallback (stream B) hears it once.
	if got := heard.list(); len(got) != 1 || got[0].Person != toluID || got[0].Business != w.biz || got[0].BusinessName != "Mama's Kitchen" ||
		got[0].Conversation.String() != id || got[0].Message.String() != r["id"] {
		t.Fatalf("heard: %+v", got)
	}
	pm := tolu.must(200, "GET", "/v1/me/chats/"+id+"/messages", nil)["messages"].([]any)
	if len(pm) != 2 || pm[1].(map[string]any)["body"] != "Yes, in 20 minutes" || pm[1].(map[string]any)["mine"] != false || pm[1].(map[string]any)["id"] != r["id"] {
		t.Fatalf("the person's copy: %v", pm)
	}
	if got := tolu.must(200, "GET", "/v1/me/chats", nil); got["unread"] != float64(1) {
		t.Fatalf("unread: %v", got)
	}
	// Read marks, both ways.
	tolu.must(204, "POST", "/v1/me/chats/"+id+"/read", nil)
	bm = owner.must(200, "GET", "/v1/customer-chats/"+thread+"/messages", nil)["messages"].([]any)
	if bm[1].(map[string]any)["delivery"] != "read" {
		t.Fatalf("the reply not read: %v", bm[1])
	}
	owner.must(204, "POST", "/v1/customer-chats/"+thread+"/read", nil)
	if got := tolu.must(200, "GET", "/v1/me/chats/"+id+"/messages", nil)["messages"].([]any); got[0].(map[string]any)["read_at"] == nil {
		t.Fatalf("the person's message not read: %v", got[0])
	}

	// Offers: none before a yes; two a week; a broadcast counts.
	if e := owner.must(409, "POST", "/v1/customer-chats/"+thread+"/messages", map[string]any{"text": "10% off today", "offer": true}); !strings.Contains(e["error"].(string), "not agreed") {
		t.Fatalf("an offer before a yes: %v", e)
	}
	if got := tolu.must(200, "PUT", "/v1/me/chats/"+id, map[string]any{"offers": true}); got["offers"] != "yes" || got["ask_offers"] != false {
		t.Fatalf("offers: %v", got)
	}
	owner.must(201, "POST", "/v1/customer-chats/"+thread+"/messages", map[string]any{"text": "10% off today", "offer": true})
	if got := owner.must(200, "POST", "/v1/customer-chats/offers", map[string]any{"text": "Free chapman with jollof", "client_id": "b1"}); got["sent"] != float64(1) {
		t.Fatalf("broadcast: %v", got)
	}
	owner.must(429, "POST", "/v1/customer-chats/"+thread+"/messages", map[string]any{"text": "Third", "offer": true})
	if got := owner.must(200, "POST", "/v1/customer-chats/offers", map[string]any{"text": "Again", "client_id": "b2"}); got["sent"] != float64(0) || got["skipped"] != float64(1) {
		t.Fatalf("a third offer by broadcast: %v", got)
	}
	pm = tolu.must(200, "GET", "/v1/me/chats/"+id+"/messages", nil)["messages"].([]any)
	if last := pm[len(pm)-1].(map[string]any); last["offer"] != true || last["body"] != "Free chapman with jollof" {
		t.Fatalf("the offer: %v", last)
	}
	// Order messages are not offers: they still go.
	owner.must(201, "POST", "/v1/customer-chats/"+thread+"/messages", map[string]any{"text": "Your rice is on the fire"})

	// Their shop, through the door.
	shop := tolu.must(200, "GET", "/v1/me/chats/"+id+"/shop", nil)
	items := shop["items"].([]any)
	if shop["open"] != true || shop["pickup"] != true || shop["delivery"] != true || len(items) != 2 || len(shop["zones"].([]any)) != 1 {
		t.Fatalf("shop: %v", shop)
	}
	ids := map[string]string{}
	for _, x := range items {
		ids[x.(map[string]any)["name"].(string)] = x.(map[string]any)["id"].(string)
	}
	if w.audits(t, "customer.shop", toluID) != 1 {
		t.Fatalf("the shop's look is not audited")
	}

	// A product card from the business.
	card := owner.must(201, "POST", "/v1/customer-chats/"+thread+"/messages", map[string]any{"card": map[string]any{"type": "product", "item_id": ids["Chapman"]}})
	if card["kind"] != "card" || card["card"].(map[string]any)["title"] != "Chapman" || card["card"].(map[string]any)["total_kobo"] != float64(150000) {
		t.Fatalf("a product card: %v", card)
	}
	owner.must(404, "POST", "/v1/customer-chats/"+thread+"/messages", map[string]any{"card": map[string]any{"type": "pay", "reference": "NOPE"}})

	// An order in the app: priced from the shop, of the kind app, through the order workflow.
	order := map[string]any{"lines": []any{map[string]any{"item_id": ids["Jollof rice"], "quantity": 2}}, "fulfilment": "delivery", "zone": "Yaba", "address": "12 Herbert Macaulay Way"}
	tolu.must(400, "POST", "/v1/me/chats/"+id+"/orders", order)
	tolu.must(400, "POST", "/v1/me/chats/"+id+"/orders", map[string]any{"lines": order["lines"], "fulfilment": "delivery", "zone": "Ikeja", "address": "x"}, "Idempotency-Key", "o0")
	o := tolu.must(201, "POST", "/v1/me/chats/"+id+"/orders", order, "Idempotency-Key", "o1")["order"].(map[string]any)
	ref := o["reference"].(string)
	if o["total_kobo"] != float64(1000000) || o["status"] != "sent" {
		t.Fatalf("order: %v", o)
	}
	if again := tolu.must(200, "POST", "/v1/me/chats/"+id+"/orders", order, "Idempotency-Key", "o1")["order"].(map[string]any); again["reference"] != ref {
		t.Fatalf("a repeat placed again: %v", again)
	}
	if err := w.srv.Orders.Drain(ctx); err != nil {
		t.Fatal(err)
	}
	so := owner.must(200, "GET", "/v1/storefront/orders/"+ref, nil)["order"].(map[string]any)
	if so["kind"] != "app" || so["run_id"] == nil || so["total_kobo"] != float64(1000000) {
		t.Fatalf("the business's order: %v", so)
	}
	var person *uuid.UUID
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT person_id FROM storefront_orders WHERE reference = $1`, ref).Scan(&person); err != nil || person == nil || *person != toluID {
		t.Fatalf("the order's person: %v %v", person, err)
	}
	mine := tolu.must(200, "GET", "/v1/me/chats/"+id+"/orders", nil)["orders"].([]any)
	if len(mine) != 1 || mine[0].(map[string]any)["reference"] != ref || mine[0].(map[string]any)["kind"] != "order" {
		t.Fatalf("your orders: %v", mine)
	}
	// Its card in both copies.
	bm = owner.must(200, "GET", "/v1/customer-chats/"+thread+"/messages", nil)["messages"].([]any)
	if last := bm[len(bm)-1].(map[string]any); last["kind"] != "card" || last["card"].(map[string]any)["reference"] != ref {
		t.Fatalf("the order card: %v", last)
	}
	// Paid when accepted: nothing to pay yet.
	tolu.must(409, "GET", "/v1/me/chats/"+id+"/orders/"+ref+"/pay", nil)
	tolu.must(404, "GET", "/v1/me/chats/"+id+"/orders/NOPE/pay", nil)
	// The business writes first about it.
	if got := owner.must(200, "POST", "/v1/customer-chats/app", map[string]any{"reference": ref}); got["id"] != thread {
		t.Fatalf("the thread of the order: %v", got)
	}
	owner.must(201, "POST", "/v1/customer-chats/"+thread+"/messages", map[string]any{"card": map[string]any{"type": "order", "reference": ref}})

	// Files: scanned, checked, and each side's own copy.
	f, fo := tolu.postFile(t, "/v1/me/chats/"+id+"/files", "rice.png", pngPhoto(t, 30), map[string]string{"text": "Like this"})
	if f != 201 || fo["kind"] != "media" || fo["attachment"].(map[string]any)["kind"] != "photo" {
		t.Fatalf("a photo: %d %v", f, fo)
	}
	if code, _, data := tolu.getRaw(t, "/v1/me/chats/"+id+"/files/"+fo["id"].(string)); code != 200 || len(data) == 0 {
		t.Fatalf("the person's copy: %d", code)
	}
	if code, _, data := owner.getRaw(t, "/v1/customer-chats/"+thread+"/files/"+fo["id"].(string)); code != 200 || len(data) == 0 {
		t.Fatalf("the business's copy: %d", code)
	}
	if code, e := tolu.postFile(t, "/v1/me/chats/"+id+"/files", "x.pdf", []byte("%PDF-1.4 EICAR"), nil); code != 422 {
		t.Fatalf("malware: %d %v", code, e)
	}
	if code, e := tolu.postFile(t, "/v1/me/chats/"+id+"/files", "x.txt", []byte("plain words"), nil); code != 400 {
		t.Fatalf("a text file: %d %v", code, e)
	}
	if code, e := owner.postFile(t, "/v1/customer-chats/"+thread+"/files", "menu.png", pngPhoto(t, 60), nil); code != 201 || e["attachment"] == nil {
		t.Fatalf("the business's photo: %d %v", code, e)
	}

	// The business blocks: the door is shut to the person.
	owner.must(204, "POST", "/v1/customer-chats/"+thread+"/block", nil)
	if e := tolu.must(403, "POST", "/v1/me/chats/"+id+"/messages", map[string]any{"text": "Hello?"}); e["blocked"] != true {
		t.Fatalf("blocked: %v", e)
	}
	tolu.must(403, "GET", "/v1/me/chats/"+id+"/shop", nil)
	if got := tolu.must(200, "GET", "/v1/me/chats/"+id, nil); got["blocked_you"] != true {
		t.Fatalf("blocked, as the person sees it: %v", got)
	}
	owner.must(409, "POST", "/v1/customer-chats/"+thread+"/messages", map[string]any{"text": "Sorry"})
	owner.must(204, "DELETE", "/v1/customer-chats/"+thread+"/block", nil)

	// The person blocks: the business is told they stopped messages; nothing is deleted.
	if got := tolu.must(200, "POST", "/v1/me/chats/"+id+"/block", nil); got["blocked"] != true {
		t.Fatalf("block: %v", got)
	}
	if e := owner.must(409, "POST", "/v1/customer-chats/"+thread+"/messages", map[string]any{"text": "Hello?"}); !strings.Contains(e["error"].(string), "stopped messages") {
		t.Fatalf("to a customer who blocked: %v", e)
	}
	if got := owner.must(200, "GET", "/v1/customer-chats/"+thread, nil); got["notice"] != "This customer has stopped messages" || got["stopped"] != true {
		t.Fatalf("the business's thread: %v", got)
	}
	if e := tolu.must(409, "POST", "/v1/me/chats/"+id+"/messages", map[string]any{"text": "x"}); e["you_blocked"] != true {
		t.Fatalf("writing after blocking: %v", e)
	}
	n := len(owner.must(200, "GET", "/v1/customer-chats/"+thread+"/messages", nil)["messages"].([]any))
	if n < 8 {
		t.Fatalf("messages deleted by the block: %d", n)
	}
	tolu.must(200, "DELETE", "/v1/me/chats/"+id+"/block", nil)

	// A report, only with consent.
	tolu.must(400, "POST", "/v1/me/chats/"+id+"/report", map[string]any{"reason": "spam", "consent": false})
	tolu.must(201, "POST", "/v1/me/chats/"+id+"/report", map[string]any{"reason": "spam", "note": "Too many offers", "consent": true})
	var sent int
	// Sealed under the report's own key where the server has a key service
	// (00481), counted in message_count; plain otherwise.
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT COALESCE(message_count, jsonb_array_length(messages)) FROM customer_reports WHERE person_id = $1`,
		toluID).Scan(&sent); err != nil || sent == 0 || sent > 20 {
		t.Fatalf("the report's messages: %d %v", sent, err)
	}

	// Mute and archive are the person's own.
	if got := tolu.must(200, "PUT", "/v1/me/chats/"+id, map[string]any{"muted": true}); got["muted"] != true {
		t.Fatalf("mute: %v", got)
	}

	// Closed by its hours: the closed reply, once a day, in the business's name.
	w.hours(t, false)
	tolu.must(201, "POST", "/v1/me/chats/"+id+"/messages", map[string]any{"text": "Are you there?"})
	tolu.must(201, "POST", "/v1/me/chats/"+id+"/messages", map[string]any{"text": "Hello?"})
	pm = tolu.must(200, "GET", "/v1/me/chats/"+id+"/messages", nil)["messages"].([]any)
	closed := 0
	for _, x := range pm {
		if strings.HasPrefix(x.(map[string]any)["body"].(string), "We're closed now. We open ") {
			closed++
		}
	}
	if closed != 1 {
		t.Fatalf("closed replies: %d", closed)
	}
	if got := tolu.must(200, "GET", "/v1/me/chats/"+id, nil); got["hours"].(map[string]any)["open"] != false {
		t.Fatalf("hours: %v", got["hours"])
	}
	// Its own words, or none.
	owner.must(400, "PUT", "/v1/settings/customer-chats", map[string]any{"closed_words": "see https://x.test"})
	if got := owner.must(200, "PUT", "/v1/settings/customer-chats", map[string]any{"closed_reply": false}); got["closed_reply"] != false || got["needs_you"] != true {
		t.Fatalf("settings: %v", got)
	}

	// Unanswered for 30 minutes while open: Needs you; after 2 hours, the owner is told.
	w.hours(t, true)
	owner.must(200, "PUT", "/v1/settings/chat-tools", map[string]any{"chat_tools": true})
	if _, err := w.env.DB.Admin.Exec(ctx, `UPDATE customer_conversations SET waiting_since = now() - interval '40 minutes', last_reply_at = now() - interval '1 day' WHERE id = $1`, thread); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, a := range owner.must(200, "GET", "/v1/chat-alerts", nil)["alerts"].([]any) {
		if a := a.(map[string]any); a["kind"] == "customer_waiting" && a["id"] == thread {
			found = true
		}
	}
	if !found {
		t.Fatalf("not on Needs you")
	}
	if _, err := w.env.DB.Admin.Exec(ctx, `UPDATE customer_conversations SET waiting_since = now() - interval '3 hours' WHERE id = $1`, thread); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := w.srv.CustomerChatsTick(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT count(*) FROM notifications WHERE tenant_id = $1 AND group_key = $2`, w.biz, "customer_waiting:"+thread).Scan(&told); err != nil || told != 1 {
		t.Fatalf("the owner told: %d %v", told, err)
	}

	// An area turned off: the door refuses what needs it, not the chat.
	owner.must(200, "PUT", "/v1/settings/areas", map[string]any{"areas": map[string]bool{"storefront": false}, "confirm": true})
	if e := tolu.must(409, "GET", "/v1/me/chats/"+id+"/shop", nil); e["area_off"] != true {
		t.Fatalf("shop with the area off: %v", e)
	}
	tolu.must(201, "POST", "/v1/me/chats/"+id+"/messages", map[string]any{"text": "Still there?"})

	// Every entry, in the business's chain.
	if w.audits(t, "customer.chat", toluID) < 5 || w.audits(t, "customer.order", toluID) < 1 || w.audits(t, "customer.block", toluID) != 2 {
		t.Fatalf("entries: chat %d, order %d, block %d", w.audits(t, "customer.chat", toluID), w.audits(t, "customer.order", toluID), w.audits(t, "customer.block", toluID))
	}
}

// Isolation (taskiem-for-everyone §8.4, §14): a person reads only their
// own copy and reaches only the business they chat with, through the
// door; the door and the way back refuse anyone else, a business not in
// scope, a person not in the transaction, and a workflows workspace; a
// business reads no other business's thread and no person's other chats.
func TestCustomerDoorIsolation(t *testing.T) {
	ctx := context.Background()
	w := newDoorShopWorld(t)
	other := w.tenant(t, "Bola Stores", "owner@bola.test")
	verifyPhone(t, w.world, other, "phone", "business")
	other.must(200, "PUT", "/v1/storefront", map[string]any{"slug": "bola-stores", "draft": map[string]any{"name": "Bola Stores"}})
	tpl, _ := templates.Default().Get("storefront-order")
	def, _ := tpl.Instantiate(map[string]any{}, "Orders")
	publishFlow(t, other, string(def))
	other.must(200, "PUT", "/v1/storefront/actions/order", map[string]any{"enabled": true, "payment": "none"})
	other.must(200, "POST", "/v1/storefront/publish", nil)
	bola := tenantIDNamed(t, w.world, "Bola Stores")

	tolu, _ := w.personIn(t, "+2348031110002")
	ada, _ := w.personIn(t, "+2348031110003")
	toluID, adaID := personID(t, tolu), personID(t, ada)
	tc := tolu.must(201, "POST", "/v1/me/chats", map[string]any{"business_id": w.biz})["id"].(string)
	tolu.must(201, "POST", "/v1/me/chats/"+tc+"/messages", map[string]any{"text": "Tolu's secret order"})
	ac := ada.must(201, "POST", "/v1/me/chats", map[string]any{"business_id": bola})["id"].(string)
	ada.must(201, "POST", "/v1/me/chats/"+ac+"/messages", map[string]any{"text": "Ada's words"})

	// A person never reads another's chat.
	for _, p := range []string{"", "/messages", "/shop", "/orders", "/services"} {
		ada.must(404, "GET", "/v1/me/chats/"+tc+p, nil)
	}
	ada.must(404, "POST", "/v1/me/chats/"+tc+"/messages", map[string]any{"text": "x"})
	ada.must(404, "POST", "/v1/me/chats/"+tc+"/block", nil)
	if got := ada.must(200, "GET", "/v1/me/chats", nil)["chats"].([]any); len(got) != 1 || got[0].(map[string]any)["name"] != "Bola Stores" {
		t.Fatalf("Ada's chats: %v", got)
	}

	// A business reads only its own threads.
	var thread string
	for _, c := range w.owner.must(200, "GET", "/v1/customer-chats", nil)["chats"].([]any) {
		thread = c.(map[string]any)["id"].(string)
	}
	other.must(404, "GET", "/v1/customer-chats/"+thread, nil)
	other.must(404, "GET", "/v1/customer-chats/"+thread+"/messages", nil)
	other.must(404, "POST", "/v1/customer-chats/"+thread+"/messages", map[string]any{"text": "x"})
	if got := other.must(200, "GET", "/v1/customer-chats", nil)["chats"].([]any); len(got) != 1 || got[0].(map[string]any)["name"] != "Tolu Ade" {
		// Ada's name is Tolu Ade too (phoneIn); it is her thread, not Tolu's.
		t.Fatalf("Bola's threads: %v", got)
	}

	// The doors themselves.
	app := w.env.DB.App
	sqlErr := func(err error) string {
		if err == nil {
			return ""
		}
		return err.Error()
	}
	// The person's own scope: their rows only.
	if err := db.InPersonTx(ctx, app, adaID, func(tx pgx.Tx) error {
		var n int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM person_messages`).Scan(&n); err != nil || n != 1 {
			return fmt.Errorf("Ada sees %d messages: %w", n, err)
		}
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM customer_conversations`).Scan(&n); err != nil || n != 0 {
			return fmt.Errorf("Ada sees %d business threads with no door: %w", n, err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	// Entering for someone else, or a business she has no chat with.
	if err := db.InPersonTx(ctx, app, adaID, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `SELECT taskiem_customer_enter($1, $2, 'customer.chat', '{}')`, toluID, w.biz)
		return err
	}); !strings.Contains(sqlErr(err), "for the person signed in") {
		t.Fatalf("entering as another: %v", err)
	}
	if err := db.InPersonTx(ctx, app, adaID, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `SELECT taskiem_customer_enter($1, $2, 'customer.shop', '{}')`, adaID, w.biz)
		return err
	}); !strings.Contains(sqlErr(err), "no conversation") {
		t.Fatalf("entering a business with no chat: %v", err)
	}
	if err := db.InPersonTx(ctx, app, adaID, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `SELECT taskiem_customer_enter($1, $2, 'customer.everything', '{}')`, adaID, bola)
		return err
	}); !strings.Contains(sqlErr(err), "no action") {
		t.Fatalf("an action not on the list: %v", err)
	}
	// Through the door into Bola Stores: Bola's rows and Ada's own, nothing of Mama's Kitchen.
	if err := db.InPersonTx(ctx, app, adaID, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT taskiem_customer_enter($1, $2, 'customer.chat', '{}')`, adaID, bola); err != nil {
			return err
		}
		var mine, theirs int
		if err := tx.QueryRow(ctx, `SELECT count(*) FILTER (WHERE tenant_id = $1), count(*) FILTER (WHERE tenant_id <> $1) FROM customer_conversations`, bola).Scan(&mine, &theirs); err != nil {
			return err
		}
		if mine != 1 || theirs != 0 {
			return fmt.Errorf("through the door: %d of Bola's, %d of others", mine, theirs)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	// The way back: only the business in scope, only to its own customer.
	if err := db.InTenantTx(ctx, app, []uuid.UUID{bola}, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `SELECT taskiem_customer_reach($1, $2, 'customer.message', '{}')`, bola, toluID)
		return err
	}); !strings.Contains(sqlErr(err), "no conversation") {
		t.Fatalf("Bola reaching Tolu: %v", err)
	}
	if err := db.InTenantTx(ctx, app, []uuid.UUID{bola}, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `SELECT taskiem_customer_reach($1, $2, 'customer.message', '{}')`, w.biz, toluID)
		return err
	}); !strings.Contains(sqlErr(err), "not in scope") {
		t.Fatalf("reaching for another business: %v", err)
	}
	// Reached, a business sees that person's chat with it only.
	if err := db.InTenantTx(ctx, app, []uuid.UUID{bola}, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT taskiem_customer_reach($1, $2, 'customer.read', '{}')`, bola, adaID); err != nil {
			return err
		}
		var n int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM person_conversations WHERE tenant_id <> $1`, bola).Scan(&n); err != nil || n != 0 {
			return fmt.Errorf("Ada's other chats seen: %d %w", n, err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	// A workflows workspace is no business to chat with.
	if _, err := w.env.DB.Admin.Exec(ctx, `UPDATE tenants SET kind = 'workflows' WHERE id = $1`, bola); err != nil {
		t.Fatal(err)
	}
	ada.must(404, "POST", "/v1/me/chats/"+ac+"/messages", map[string]any{"text": "x"})

	// A business session is a person too; an API key is not.
	if got := w.owner.must(200, "GET", "/v1/me/chats", nil); len(got["chats"].([]any)) != 0 {
		t.Fatalf("the owner's own chats: %v", got)
	}
	key := w.owner.must(201, "POST", "/v1/api-keys", map[string]any{"name": "k", "permissions": []string{"order.handle"}})["key"].(string)
	(&client{t: t, base: w.base, token: key}).must(403, "GET", "/v1/me/chats", nil)
}
