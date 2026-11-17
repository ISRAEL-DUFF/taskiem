package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/chatlinks"
	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/phoneindex"
	"github.com/israel-duff/taskiem/engine/storefront"
)

const chatBase = "https://taskiem.test"

// withChatLinks gives a world chat links and the phone index.
func withChatLinks(w *world) {
	key := bytes.Repeat([]byte{8}, 32)
	w.srv.ChatLinks = &chatlinks.Links{Pool: w.env.DB.App, Key: key, Base: chatBase}
	w.srv.PhoneIndex = &phoneindex.Index{Pool: w.env.DB.App, Key: key, Vault: w.env.Vault}
}

// page fetches a server page (not JSON): its status and body.
func (w *world) pageAt(t *testing.T, path string) (int, string) {
	t.Helper()
	resp, err := http.Get(w.base + path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

// snapshotModel is the storefront's published model.
func snapshotModel(t *testing.T, w *world, tenant uuid.UUID) storefront.Model {
	t.Helper()
	var raw []byte
	if err := w.env.DB.Admin.QueryRow(context.Background(), `SELECT snap.model FROM storefronts s JOIN storefront_snapshots snap ON snap.storefront_id = s.id AND snap.version = s.published_version
		WHERE s.tenant_id = $1`, tenant).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var m storefront.Model
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

// A business's chat link (docs/spec/taskiem-for-everyone.md §8.2): the
// owner's link with its words and QR code, the public card for its code,
// "Make a new link" and the old one's 30 days, the settings, the
// storefront's Chat with us, and the page /m/{code} where no web app is
// served.
func TestChatLinks(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()
	anon := &client{t: t, base: w.base}
	owner := w.tenant(t, "Mama's Kitchen", "mama@mama.test")
	tenant := tenantUUID(t, owner)

	// Not set up: said so.
	if st, _ := owner.do("GET", "/v1/customers/chat-link", nil); st != 503 {
		t.Fatalf("no key: %d", st)
	}
	withChatLinks(w)
	w.srv.PublicHosts = []string{"shops.test"}
	w.srv.AppGetURL = "https://taskiem.test/app"

	got := owner.must(200, "GET", "/v1/customers/chat-link", nil)
	code, _ := got["code"].(string)
	if chatlinks.Clean(code) != code || got["url"] != chatBase+"/m/"+code || got["words"] != "Chat with Mama's Kitchen on Taskiem: "+chatBase+"/m/"+code {
		t.Fatalf("the link: %v", got)
	}
	settings := got["settings"].(map[string]any)
	if settings["takes_chats"] != true || settings["storefront_button"] != "both" || settings["updates_link"] != true || settings["link_in_updates"] != nil {
		t.Fatalf("settings: %v", settings)
	}
	if again := owner.must(200, "GET", "/v1/customers/chat-link", nil); again["code"] != code {
		t.Fatalf("the link changed: %v", again)
	}
	if svg := owner.raw(t, "GET", "/v1/customers/chat-link/qr"); !strings.Contains(svg, "<svg") {
		t.Fatalf("qr: %.80s", svg)
	}

	// Anyone with the link sees the business's card.
	card := anon.must(200, "GET", "/v1/chat-links/"+strings.ToUpper(code), nil)
	if card["name"] != "Mama's Kitchen" || card["takes_chats"] != true || card["link"] != chatBase+"/m/"+code || card["app"] != "https://taskiem.test/app" {
		t.Fatalf("card: %v", card)
	}
	if st, _ := anon.do("GET", "/v1/chat-links/abcdefgh23", nil); st != 404 {
		t.Fatalf("an unknown code: %d", st)
	}

	// The storefront offers Chat with us beside WhatsApp, by default.
	verifyPhone(t, w, owner, "phone", "business")
	owner.must(200, "PUT", "/v1/storefront", map[string]any{"slug": "mama-kitchen", "draft": map[string]any{"name": "Mama's Kitchen", "whatsapp": "+2348031234567",
		"hours": map[string]any{"days": [][]map[string]string{{{"open": "00:00", "close": "23:59"}}, {{"open": "00:00", "close": "23:59"}}, {{"open": "00:00", "close": "23:59"}},
			{{"open": "00:00", "close": "23:59"}}, {{"open": "00:00", "close": "23:59"}}, {{"open": "00:00", "close": "23:59"}}, {{"open": "00:00", "close": "23:59"}}}}}})
	owner.must(200, "POST", "/v1/storefront/publish", nil)
	if m := snapshotModel(t, w, tenant); m.ChatLink != chatBase+"/m/"+code || m.WhatsLink == "" {
		t.Fatalf("published: chat %q, WhatsApp %q", m.ChatLink, m.WhatsLink)
	}
	card = anon.must(200, "GET", "/v1/chat-links/"+code, nil)
	if card["open_now"] != true || card["shop"] != "https://mama-kitchen.shops.test" || card["hours"] == nil {
		t.Fatalf("card with a shop: %v", card)
	}

	// Instead of WhatsApp: the page is published again with it.
	set := owner.must(200, "PUT", "/v1/customers/chat-settings", map[string]any{"storefront_button": "taskiem"})
	if set["storefront_button"] != "taskiem" {
		t.Fatalf("settings: %v", set)
	}
	if m := snapshotModel(t, w, tenant); m.ChatLink == "" || m.WhatsLink != "" {
		t.Fatalf("instead of WhatsApp: chat %q, WhatsApp %q", m.ChatLink, m.WhatsLink)
	}
	if st, out := owner.do("PUT", "/v1/customers/chat-settings", map[string]any{"storefront_button": "telegram"}); st != 400 {
		t.Fatalf("a bad button: %d %v", st, out)
	}

	// Make a new link: the old one no longer works, and the page carries
	// the new one.
	fresh := owner.must(200, "POST", "/v1/customers/chat-link", nil)
	newCode, _ := fresh["code"].(string)
	if newCode == code || newCode == "" {
		t.Fatalf("new link: %v", fresh)
	}
	if st, out := anon.do("GET", "/v1/chat-links/"+code, nil); st != 410 || out["gone"] != true || !strings.Contains(out["error"].(string), "This link no longer works") {
		t.Fatalf("the old link: %d %v", st, out)
	}
	anon.must(200, "GET", "/v1/chat-links/"+newCode, nil)
	if m := snapshotModel(t, w, tenant); m.ChatLink != chatBase+"/m/"+newCode {
		t.Fatalf("the page's link: %q", m.ChatLink)
	}
	var audited int
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT count(*) FROM audit_log WHERE tenant_id = $1 AND action IN ('chat_link.replaced', 'chat_link.settings')`, tenant).Scan(&audited); err != nil || audited != 2 {
		t.Fatalf("audited: %d %v", audited, err)
	}

	// The page /m/{code}, with no web app served here.
	st, body := w.pageAt(t, "/m/"+newCode)
	if st != 200 || !strings.Contains(body, "Mama&#39;s Kitchen") || !strings.Contains(body, "Chat here in your browser") ||
		!strings.Contains(body, `href="https://taskiem.test/app"`) || !strings.Contains(body, "Get the Taskiem app") || !strings.Contains(body, "Open now") ||
		!strings.Contains(body, "/login?next=/m/"+newCode) {
		t.Fatalf("the page: %d %s", st, body)
	}
	if st, body := w.pageAt(t, "/m/"+code); st != 410 || !strings.Contains(body, "This link no longer works") {
		t.Fatalf("the old page: %d %s", st, body)
	}
	if st, _ := w.pageAt(t, "/m/abcdefgh23"); st != 404 {
		t.Fatalf("an unknown page: %d", st)
	}

	// Not taking chats: the card says so, and the page offers no chat.
	owner.must(200, "PUT", "/v1/customers/chat-settings", map[string]any{"takes_chats": false})
	if card := anon.must(200, "GET", "/v1/chat-links/"+newCode, nil); card["takes_chats"] != false {
		t.Fatalf("not taking chats: %v", card)
	}
	if _, body := w.pageAt(t, "/m/"+newCode); strings.Contains(body, "Chat here in your browser") || !strings.Contains(body, "not taking chats") {
		t.Fatalf("the page, not taking chats: %s", body)
	}
	if m := snapshotModel(t, w, tenant); m.ChatLink != "" || m.WhatsLink == "" {
		t.Fatalf("not taking chats, the page: chat %q, WhatsApp %q", m.ChatLink, m.WhatsLink)
	}
}

// Who pays for a sign-in code through a chat link (§15, founder's decision
// of 2026-11-17): nothing when it is sent, so someone asking for codes
// through a business's link costs it nothing; the business is charged once
// when a NEW account made with such a code starts a chat with it; an
// existing account signing in through the link is not charged; at most 50
// charged codes a business a day, then Taskiem pays.
func TestPhoneCodeThroughChatLink(t *testing.T) {
	w, sms := phoneWorld(t)
	ctx := context.Background()
	withChatLinks(w)
	owner := w.tenant(t, "Mama's Kitchen", "mama@mama.test")
	tenant := tenantUUID(t, owner)
	code := owner.must(200, "GET", "/v1/customers/chat-link", nil)["code"].(string)
	anon := &client{t: t, base: w.base}
	charges := func() (n, segments int) {
		t.Helper()
		if err := w.env.DB.Admin.QueryRow(ctx, `SELECT count(*), COALESCE(sum(segments), 0) FROM customer_message_charges WHERE tenant_id = $1 AND purpose = 'signin_code'`, tenant).Scan(&n, &segments); err != nil {
			t.Fatal(err)
		}
		return n, segments
	}
	// signIn asks for a code through the link (or none) and signs in, making
	// an account for a new number.
	signIn := func(number, link string) *client {
		t.Helper()
		body := map[string]any{"phone": number}
		if link != "" {
			body["chat_code"] = link
		}
		anon.must(202, "POST", "/v1/auth/phone/start", body)
		out := anon.must(200, "POST", "/v1/auth/phone/verify", map[string]any{"phone": number, "code": sms.code(t, number), "bearer": true})
		if out["status"] == "new" {
			out = anon.must(201, "POST", "/v1/auth/phone/create", map[string]any{"ticket": out["ticket"], "name": "Bola Ade", "adult": true, "terms": true, "bearer": true})
		}
		return &client{t: t, base: w.base, token: out["token"].(string)}
	}

	// Someone asks for codes for many numbers through the link: recorded
	// with each code, charged to no one.
	for i := range 5 {
		anon.must(202, "POST", "/v1/auth/phone/start", map[string]any{"phone": fmt.Sprintf("+23480312309%02d", 50+i), "chat_code": code})
	}
	var came *uuid.UUID
	var via string
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT came_from, sent_via FROM phone_codes WHERE number = '+2348031230950'`).Scan(&came, &via); err != nil || came == nil || *came != tenant || via != "sms" {
		t.Fatalf("came from: %v %q %v", came, via, err)
	}
	if n, _ := charges(); n != 0 {
		t.Fatalf("codes asked for through the link were charged: %d", n)
	}
	// Asked for again with no link: no longer the business's.
	anon.must(202, "POST", "/v1/auth/phone/start", map[string]any{"phone": "+2348031230950"})
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT came_from FROM phone_codes WHERE number = '+2348031230950'`).Scan(&came); err != nil || came != nil {
		t.Fatalf("came from, asked again with no link: %v %v", came, err)
	}

	// A new account made through the link: nothing yet; its first chat with
	// the business charges the code, once.
	bola := signIn("+2348031230901", code)
	if n, _ := charges(); n != 0 {
		t.Fatalf("charged before any chat: %d", n)
	}
	bola.must(201, "POST", "/v1/me/chats", map[string]any{"code": code})
	if n, seg := charges(); n != 1 || seg != 1 {
		t.Fatalf("a new account's first chat: %d charges, %d segments", n, seg)
	}
	bola.must(200, "POST", "/v1/me/chats", map[string]any{"code": code})
	if n, _ := charges(); n != 1 {
		t.Fatalf("charged again: %d", n)
	}
	// It counts in the business's SMS this month.
	var used int64
	if err := db.InTenantTx(ctx, w.env.DB.App, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT COALESCE(sum(segments), 0) FROM customer_message_charges WHERE tenant_id = $1 AND channel = 'sms'`, tenant).Scan(&used)
	}); err != nil || used != 1 {
		t.Fatalf("segments: %d %v", used, err)
	}

	// An existing account signing in through the link (a new device) and
	// chatting: Taskiem's.
	signIn("+2348031230902", "")
	ade := signIn("+2348031230902", code)
	ade.must(201, "POST", "/v1/me/chats", map[string]any{"code": code})
	if n, _ := charges(); n != 1 {
		t.Fatalf("an existing account was charged: %d", n)
	}

	// A new account made through the link that never chats with the
	// business: nothing (and a chat with another business charges no one).
	other := w.tenant(t, "Ada Stores", "ada@ada.test")
	otherCode := other.must(200, "GET", "/v1/customers/chat-link", nil)["code"].(string)
	cara := signIn("+2348031230903", code)
	cara.must(201, "POST", "/v1/me/chats", map[string]any{"code": otherCode})
	if n, _ := charges(); n != 1 {
		t.Fatalf("charged for a chat with another business: %d", n)
	}
	var otherCharges int
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT count(*) FROM customer_message_charges WHERE tenant_id = $1`, tenantUUID(t, other)).Scan(&otherCharges); err != nil || otherCharges != 0 {
		t.Fatalf("the other business was charged: %d %v", otherCharges, err)
	}

	// The day's cap: 50 charged codes; the 51st is Taskiem's, and does not
	// count against the business's SMS.
	if _, err := w.env.DB.Admin.Exec(ctx, `INSERT INTO customer_message_charges (id, tenant_id, channel, purpose, segments)
		SELECT gen_random_uuid(), $1, 'sms', 'signin_code', 1 FROM generate_series(1, 49)`, tenant); err != nil {
		t.Fatal(err)
	}
	dayo := signIn("+2348031230904", code)
	dayo.must(201, "POST", "/v1/me/chats", map[string]any{"code": code})
	if n, _ := charges(); n != 50 {
		t.Fatalf("over the cap: %d charges", n)
	}
	var charged *bool
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT s.charged FROM customer_signup_sources s JOIN user_phones p ON p.user_id = s.user_id WHERE p.number = '+2348031230904'`).Scan(&charged); err != nil || charged == nil || *charged {
		t.Fatalf("over the cap, settled as Taskiem's: %v %v", charged, err)
	}
}

// A person's own storefront orders (§13): found by their verified phone
// across businesses, only theirs; orders made before the phone was on the
// account are only counted until "These are mine" (nothing of them shown);
// a personal session reaches it.
func TestMyStorefrontOrders(t *testing.T) {
	w, sms := phoneWorld(t)
	ctx := context.Background()
	withChatLinks(w)
	orders := &storefront.Orders{Pool: w.env.DB.App, Vault: w.env.Vault, PhoneIndex: w.srv.PhoneIndex}
	mama := tenantUUID(t, w.tenant(t, "Mama's Kitchen", "mama@mama.test"))
	ada := tenantUUID(t, w.tenant(t, "Ada Stores", "ada@ada.test"))
	place := func(tenant uuid.UUID, phone string) storefront.Order {
		t.Helper()
		var o storefront.Order
		if err := db.InTenantTx(ctx, w.env.DB.App, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
			var err error
			o, err = orders.PlaceChatTx(ctx, tx, tenant, storefront.ChatOrder{Customer: storefront.Customer{Name: "Bola", Phone: phone},
				Lines: []storefront.Line{{ID: "1", Name: "Jollof rice", Quantity: 1, PriceKobo: 450000}}, Fulfilment: "pickup", Pay: storefront.ChatPayLater, By: "user:x"})
			return err
		}); err != nil {
			t.Fatal(err)
		}
		return o
	}
	const bola = "+2348031230911"
	early := place(mama, "0803 123 0911")
	place(ada, "+2348031230999") // someone else's
	time.Sleep(10 * time.Millisecond)
	me, _ := phoneIn(t, w, sms, bola, nil)
	later := place(ada, bola)

	got := me.must(200, "GET", "/v1/me/storefront-orders", nil)
	list := got["orders"].([]any)
	// The order made before the phone was on the account: a count only,
	// nothing of it (not its reference, state, total or business).
	if len(list) != 1 || got["waiting"] != true || got["waiting_count"] != float64(1) {
		t.Fatalf("orders: %v", got)
	}
	first := list[0].(map[string]any)
	if first["reference"] != later.Reference || first["business"] != "Ada Stores" || len(first) != 6 {
		t.Fatalf("orders: %v", list)
	}
	if b, _ := json.Marshal(got); strings.Contains(string(b), early.Reference) || strings.Contains(string(b), "Mama") {
		t.Fatalf("a waiting order shown: %s", b)
	}
	if one := me.must(200, "GET", "/v1/me/storefront-orders?business="+mama.String(), nil); len(one["orders"].([]any)) != 0 {
		t.Fatalf("one business's, waiting: %v", one)
	}
	me.must(200, "POST", "/v1/me/storefront-orders/confirm", nil)
	if got := me.must(200, "GET", "/v1/me/storefront-orders", nil); got["waiting"] != false || got["waiting_count"] != float64(0) || len(got["orders"].([]any)) != 2 {
		t.Fatalf("after These are mine: %v", got)
	}
	// One business's, chosen before the limit.
	one := me.must(200, "GET", "/v1/me/storefront-orders?limit=1&business="+mama.String(), nil)
	if l := one["orders"].([]any); len(l) != 1 || l[0].(map[string]any)["reference"] != early.Reference {
		t.Fatalf("one business's: %v", one)
	}
	// Another person sees none of them.
	other, _ := phoneIn(t, w, sms, "+2348031230912", nil)
	if got := other.must(200, "GET", "/v1/me/storefront-orders", nil); len(got["orders"].([]any)) != 0 {
		t.Fatalf("another person's: %v", got)
	}
}

// tenantUUID is a client's tenant, as an id.
func tenantUUID(t *testing.T, c *client) uuid.UUID { return uuid.MustParse(tenantOf(t, c)) }
