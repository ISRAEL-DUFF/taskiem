package api_test

import (
	"bytes"
	"context"
	"encoding/json"
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
	if card["name"] != "Mama's Kitchen" || card["takes_chats"] != true || card["link"] != chatBase+"/m/"+code {
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

// A sign-in code asked for through a business's chat link is the
// business's to pay for (§15): recorded with the code and counted in its
// SMS; one asked for with no link, or an old one, is Taskiem's.
func TestPhoneCodeThroughChatLink(t *testing.T) {
	w, sms := phoneWorld(t)
	ctx := context.Background()
	withChatLinks(w)
	owner := w.tenant(t, "Mama's Kitchen", "mama@mama.test")
	tenant := tenantUUID(t, owner)
	code := owner.must(200, "GET", "/v1/customers/chat-link", nil)["code"].(string)
	anon := &client{t: t, base: w.base}
	const bola, ade = "+2348031230901", "+2348031230902"
	anon.must(202, "POST", "/v1/auth/phone/start", map[string]any{"phone": bola, "chat_code": code})
	if sms.count(bola) != 1 {
		t.Fatal("no code")
	}
	var came *uuid.UUID
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT came_from FROM phone_codes WHERE number = $1`, bola).Scan(&came); err != nil || came == nil || *came != tenant {
		t.Fatalf("came from: %v %v", came, err)
	}
	charges := func() int {
		var n int
		if err := w.env.DB.Admin.QueryRow(ctx, `SELECT count(*) FROM customer_message_charges WHERE tenant_id = $1 AND channel = 'sms' AND purpose = 'signin_code'`, tenant).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	if charges() != 1 {
		t.Fatalf("charged: %d", charges())
	}
	// No link: Taskiem's.
	anon.must(202, "POST", "/v1/auth/phone/start", map[string]any{"phone": ade})
	if charges() != 1 {
		t.Fatalf("charged with no link: %d", charges())
	}
	// A link replaced: Taskiem's.
	owner.must(200, "POST", "/v1/customers/chat-link", nil)
	anon.must(202, "POST", "/v1/auth/phone/start", map[string]any{"phone": ade, "chat_code": code})
	if charges() != 1 {
		t.Fatalf("charged for an old link: %d", charges())
	}
	// The business's SMS this month counts it.
	var used int64
	if err := db.InTenantTx(ctx, w.env.DB.App, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT COALESCE(sum(segments), 0) FROM customer_message_charges WHERE tenant_id = $1`, tenant).Scan(&used)
	}); err != nil || used != 1 {
		t.Fatalf("segments: %d %v", used, err)
	}
}

// A person's own storefront orders (§13): found by their verified phone
// across businesses, only theirs; orders made before the phone was on the
// account wait for "These are mine"; a personal session reaches it.
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
	if len(list) != 2 || got["waiting"] != true {
		t.Fatalf("orders: %v", got)
	}
	first, second := list[0].(map[string]any), list[1].(map[string]any)
	if first["reference"] != later.Reference || first["business"] != "Ada Stores" || first["waiting"] != false ||
		second["reference"] != early.Reference || second["business"] != "Mama's Kitchen" || second["waiting"] != true {
		t.Fatalf("orders: %v", list)
	}
	for _, o := range list {
		if len(o.(map[string]any)) != 7 {
			t.Fatalf("more than the order's reference, state, total, date and business: %v", o)
		}
	}
	one := me.must(200, "GET", "/v1/me/storefront-orders?business="+mama.String(), nil)
	if l := one["orders"].([]any); len(l) != 1 || l[0].(map[string]any)["reference"] != early.Reference {
		t.Fatalf("one business's: %v", one)
	}
	me.must(200, "POST", "/v1/me/storefront-orders/confirm", nil)
	if got := me.must(200, "GET", "/v1/me/storefront-orders", nil); got["waiting"] != false {
		t.Fatalf("after These are mine: %v", got)
	}
	// Another person sees none of them.
	other, _ := phoneIn(t, w, sms, "+2348031230912", nil)
	if got := other.must(200, "GET", "/v1/me/storefront-orders", nil); len(got["orders"].([]any)) != 0 {
		t.Fatalf("another person's: %v", got)
	}
}

// tenantUUID is a client's tenant, as an id.
func tenantUUID(t *testing.T, c *client) uuid.UUID { return uuid.MustParse(tenantOf(t, c)) }
