package api_test

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	sfconn "github.com/israel-duff/taskiem/connectors/storefront"
	"github.com/israel-duff/taskiem/connectors/xenibase"
	"github.com/israel-duff/taskiem/connectors/xenibase/xenibasetest"
	"github.com/israel-duff/taskiem/engine/datatables"
	"github.com/israel-duff/taskiem/engine/egress"
	"github.com/israel-duff/taskiem/engine/media"
	"github.com/israel-duff/taskiem/engine/sender"
	"github.com/israel-duff/taskiem/engine/storefront"
	"github.com/israel-duff/taskiem/engine/telegram/telegramtest"
	"github.com/israel-duff/taskiem/engine/whatsapp/whatsapptest"
	"github.com/israel-duff/taskiem/templates"
)

// The storefront from chat (docs/whatsapp.md#the-storefront): the set-up,
// the catalogue, hours, services, closing, orders and the QR code. Each
// test spreads its messages over several numbers: a number gets twenty
// messages, then one every three seconds.

const shopDomain = "shops.taskiem.test"

// shopWorld is a WhatsApp world with a fake Xenibase for tables, photo
// storage and a preview domain.
func shopWorld(t *testing.T) *waTablesWorld {
	t.Helper()
	w := newWATablesWorld(t)
	w.srv.PublicHosts = []string{shopDomain}
	withPhotos(t, w.world)
	return w
}

func withPhotos(t *testing.T, w *world) {
	store := fakeStorage(t)
	w.srv.Media = &media.Service{
		Credentials: func(context.Context, uuid.UUID) (map[string]string, error) {
			return map[string]string{"token": "xb_tok", "project_id": "prj_1", "base_url": store.URL, "api_url": store.URL}, nil
		},
		Guard: &egress.Guard{Blocked: func(netip.Addr) bool { return false }},
	}
}

// pngPhoto is another photo than photo(t), so it is stored apart.
func pngPhoto(t *testing.T, shade uint8) []byte {
	img := image.NewRGBA(image.Rect(0, 0, 400, 300))
	for y := range 300 {
		for x := range 400 {
			img.Set(x, y, color.RGBA{R: shade, G: uint8(x / 2), B: uint8(y), A: 255}) //nolint:gosec // bounded
		}
	}
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

// yes confirms and returns the single reply.
func (w *waWorld) yes(t *testing.T, from string) string {
	t.Helper()
	got := w.send(t, from, whatsapptest.ButtonReply("yes", "Yes"))
	if len(got) != 1 {
		t.Fatalf("yes from %s: %d replies: %+v", from, len(got), got)
	}
	return got[0].Text
}

func storefrontRow(t *testing.T, w *world, slug string) (status string, version int, closed *string, draft string) {
	t.Helper()
	if err := w.env.DB.Admin.QueryRow(context.Background(), `SELECT status, published_version, closed_message, draft::text FROM storefronts WHERE slug = $1`, slug).
		Scan(&status, &version, &closed, &draft); err != nil {
		t.Fatal(err)
	}
	return
}

func TestWhatsAppMakeMyPage(t *testing.T) {
	w := shopWorld(t)
	owner := w.tenant(t, "Ada's Kitchen", "ada@ada.test")
	const on, vn, an, bn, cn = "+2348010000009", "+2348010000005", "+2348010000006", "+2348010000007", "+2348010000008"
	w.bind(t, owner, on)
	viewer := w.member(t, owner, "vic@ada.test", "viewer")
	w.bind(t, viewer, vn)
	for i, n := range []string{an, bn, cn} {
		w.bind(t, w.member(t, owner, fmt.Sprintf("adm%d@ada.test", i), "admin"), n)
	}

	// Who may: storefront.manage; help says so.
	if r := w.say(t, vn, "make my page"); !strings.Contains(r, "storefront.manage") {
		t.Errorf("viewer: %q", r)
	}
	if r := w.say(t, vn, "help"); strings.Contains(r, "*make my page*") {
		t.Errorf("viewer help: %q", r)
	}
	if r := w.say(t, on, "help"); !strings.Contains(r, "*make my page*") || !strings.Contains(r, "*add product Jollof rice 2000*") {
		t.Errorf("help: %q", r)
	}
	if r := w.say(t, on, "qr"); !strings.Contains(r, "no page yet") || !strings.Contains(r, "*make my page*") {
		t.Errorf("no page: %q", r)
	}

	// The set-up: one question at a time, each answer checked.
	if r := w.say(t, on, "make my page"); !strings.Contains(r, "Five short questions") || !strings.Contains(r, "1/5. What is your business called?") {
		t.Fatalf("start: %q", r)
	}
	if r := w.say(t, on, "Paystack Payments"); !strings.Contains(r, "does not fit") || !strings.Contains(r, "1/5") {
		t.Errorf("a protected name: %q", r)
	}
	if r := w.say(t, on, "Ada's Kitchen"); !strings.Contains(r, "2/5") {
		t.Fatalf("tagline asked: %q", r)
	}
	if r := w.say(t, on, "Home-cooked jollof, see www.ada.test"); !strings.Contains(r, "may not hold links") || !strings.Contains(r, "2/5") {
		t.Errorf("a link: %q", r)
	}
	if r := w.say(t, on, "Home-cooked jollof and small chops"); !strings.Contains(r, "3/5") {
		t.Fatalf("area asked: %q", r)
	}
	if r := w.say(t, on, "Yaba, Lagos"); !strings.Contains(r, "4/5") {
		t.Fatalf("hours asked: %q", r)
	}
	if r := w.say(t, on, "whenever we like"); !strings.Contains(r, "could not read those hours") {
		t.Errorf("bad hours: %q", r)
	}
	if r := w.say(t, on, "mon-sat 8-6, closed sunday"); !strings.Contains(r, "5/5. Send a photo") {
		t.Fatalf("photo asked: %q", r)
	}
	if r := w.say(t, on, "later"); !strings.Contains(r, "Send a photo, or reply *skip*") {
		t.Errorf("words for the photo: %q", r)
	}
	w.graph.AddMedia("logo-1", "image/png", pngPhoto(t, 200), 0)
	got := w.send(t, on, whatsapptest.Image("logo-1", "image/png", ""))
	if len(got) != 1 || len(got[0].Buttons) != 2 || got[0].Buttons[0].Title != "Yes, publish" {
		t.Fatalf("summary: %+v", got)
	}
	sum := got[0].Text
	for _, want := range []string{"saved as a draft", "*Ada's Kitchen*", "Home-cooked jollof and small chops", "Yaba, Lagos", "Mon–Sat 08:00–18:00; Sun closed",
		"(with your photo at the top)", "https://adas-kitchen." + shopDomain, "Publish it now?"} {
		if !strings.Contains(sum, want) {
			t.Errorf("summary: no %q in %q", want, sum)
		}
	}
	// A draft, with a products table mapped as its catalogue and the logo.
	status, version, _, draft := storefrontRow(t, w.world, "adas-kitchen")
	if status != "draft" || version != 0 || !strings.Contains(draft, `"logo": "`) || !strings.Contains(draft, `"area": "Yaba, Lagos"`) {
		t.Fatalf("draft: %s %d %s", status, version, draft)
	}
	var cols string
	if err := w.env.DB.Admin.QueryRow(context.Background(), `SELECT s.catalogue_columns::text FROM storefronts s JOIN data_tables d ON d.id = s.catalogue_table
		WHERE s.slug = 'adas-kitchen' AND d.name = 'products'`).Scan(&cols); err != nil || !strings.Contains(cols, `"price": "price"`) || !strings.Contains(cols, `"stock": "stock"`) {
		t.Fatalf("catalogue: %v %s", err, cols)
	}
	if n := len(w.rows(t, "products")); n != 0 {
		t.Fatalf("rows: %d", n)
	}

	// Yes publishes: the link, the QR code image, the status text.
	got = w.send(t, on, whatsapptest.ButtonReply("yes", "Yes, publish"))
	link := "https://adas-kitchen." + shopDomain
	if len(got) != 3 || !strings.Contains(got[0].Text, "Your page is live: "+link) ||
		got[1].Type != "image" || got[1].Image != link+"/qr.png" || !strings.Contains(got[1].Text, "Scan to open Ada's Kitchen: "+link) ||
		got[2].Text != "[Ada's Kitchen] Order from Ada's Kitchen here: "+link {
		t.Fatalf("published: %+v", got)
	}
	if status, version, _, _ := storefrontRow(t, w.world, "adas-kitchen"); status != "published" || version != 1 {
		t.Fatalf("published: %s %d", status, version)
	}
	if r := w.say(t, on, "my page"); !strings.Contains(r, "*Ada's Kitchen* is published: "+link) || !strings.Contains(r, "Orders today: 0") {
		t.Errorf("my page: %q", r)
	}
	if r := w.say(t, on, "make my page"); !strings.Contains(r, "is published") {
		t.Errorf("make my page again: %q", r)
	}
	got = w.send(t, on, whatsapptest.Text("qr"))
	if len(got) != 1 || got[0].Image != link+"/qr.png" || !strings.Contains(got[0].Text, link) || !strings.Contains(got[0].Text, waPublicURL+"/storefront") {
		t.Fatalf("qr: %+v", got)
	}

	// Products: typed, or a photo with a caption; shown, then added on yes.
	if r := w.say(t, vn, "add product Zobo 300"); !strings.Contains(r, "storefront.manage") {
		t.Errorf("viewer add: %q", r)
	}
	if r := w.say(t, an, "add product Jollof rice"); !strings.Contains(r, "*add product Jollof rice 2000*") {
		t.Errorf("no price: %q", r)
	}
	r := w.say(t, an, "add product Jollof rice ₦2,000")
	if !strings.Contains(r, "Add this product?\n*Jollof rice* ₦2,000") || strings.Contains(r, "with the photo") {
		t.Fatalf("product asked: %q", r)
	}
	if n := len(w.rows(t, "products")); n != 0 {
		t.Fatalf("rows before yes: %d", n)
	}
	if r := w.yes(t, an); !strings.Contains(r, "Added *Jollof rice* at ₦2,000.") || !strings.Contains(r, "on your page now") {
		t.Fatalf("added: %q", r)
	}
	if r := w.say(t, an, "add product jollof rice 2500"); !strings.Contains(r, "already in your catalogue at ₦2,000") {
		t.Errorf("twice: %q", r)
	}
	w.graph.AddMedia("photo-1", "image/jpeg", photo(t), 0)
	got = w.send(t, an, whatsapptest.Image("photo-1", "image/jpeg", "Fried rice 2.5k"))
	if len(got) != 1 || !strings.Contains(got[0].Text, "*Fried rice* ₦2,500 (with the photo)") || got[0].Buttons[0].Title != "Yes, add it" {
		t.Fatalf("photo product: %+v", got)
	}
	if r := w.yes(t, an); !strings.Contains(r, "Added *Fried rice* at ₦2,500.") {
		t.Fatalf("photo added: %q", r)
	}
	if r := w.send(t, an, whatsapptest.Image("photo-1", "image/jpeg", "")); len(r) != 1 || !strings.Contains(r[0].Text, "name and price as its caption") {
		t.Errorf("no caption: %+v", r)
	}
	rows := w.rows(t, "products")
	byName := map[string]map[string]any{}
	for _, row := range rows {
		byName[fmt.Sprint(row["name"])] = row
	}
	if len(rows) != 2 || fmt.Sprint(byName["Jollof rice"]["price"]) != "2000" || byName["Jollof rice"]["available"] != true || byName["Jollof rice"]["photo"] != nil ||
		fmt.Sprint(byName["Fried rice"]["price"]) != "2500" || byName["Fried rice"]["photo"] == nil {
		t.Fatalf("rows: %v", rows)
	}
	if _, err := uuid.Parse(fmt.Sprint(byName["Fried rice"]["photo"])); err != nil {
		t.Errorf("the photo column holds a media id: %v", byName["Fried rice"]["photo"])
	}

	// Changes: found by name (a unique prefix; a pick when several match),
	// old to new, on yes.
	if r := w.say(t, bn, "price jollof 2500"); !strings.Contains(r, "Change the price of *Jollof rice* from ₦2,000 to ₦2,500?") {
		t.Fatalf("price asked: %q", r)
	}
	if r := w.yes(t, bn); !strings.Contains(r, "*Jollof rice* is updated") {
		t.Fatalf("price: %q", r)
	}
	w.say(t, bn, "add product Jollof special 4000")
	w.yes(t, bn)
	if r := w.say(t, bn, "price jollof 3000"); !strings.Contains(r, "Which one?") || !strings.Contains(r, "1. Jollof rice") || !strings.Contains(r, "2. Jollof special") {
		t.Fatalf("pick: %q", r)
	}
	if r := w.say(t, bn, "7"); !strings.Contains(r, "Reply with a number") {
		t.Errorf("pick again: %q", r)
	}
	if r := w.say(t, bn, "2"); !strings.Contains(r, "*Jollof special* from ₦4,000 to ₦3,000") {
		t.Fatalf("picked: %q", r)
	}
	w.yes(t, bn)
	if r := w.say(t, bn, "hide FRIED"); !strings.Contains(r, "Hide *Fried rice*?") {
		t.Fatalf("hide asked: %q", r)
	}
	w.yes(t, bn)
	if r := w.say(t, bn, "stock jollof rice 12"); !strings.Contains(r, "*Jollof rice* from not counted to 12") {
		t.Fatalf("stock asked: %q", r)
	}
	w.yes(t, bn)
	if r := w.say(t, bn, "stock jollof rice lots"); !strings.Contains(r, "*stock Jollof rice 20*") {
		t.Errorf("bad stock: %q", r)
	}
	if r := w.say(t, bn, "hide pizza"); !strings.Contains(r, "Nothing in your catalogue is called pizza") {
		t.Errorf("unknown: %q", r)
	}
	if r := w.say(t, bn, "show fried rice"); !strings.Contains(r, "Show *Fried rice*") {
		t.Fatalf("show asked: %q", r)
	}
	if r := w.say(t, bn, "no"); !strings.Contains(r, "Nothing was changed") {
		t.Errorf("no: %q", r)
	}
	// The copy the page reads follows.
	cat := owner.must(200, "GET", "/v1/storefront/catalogue", nil)["items"].([]any)
	items := map[string]map[string]any{}
	for _, it := range cat {
		m := it.(map[string]any)
		items[m["name"].(string)] = m
	}
	if len(items) != 3 || items["Jollof rice"]["price_kobo"] != float64(250000) || items["Jollof rice"]["stock"] != float64(12) ||
		items["Jollof special"]["price_kobo"] != float64(300000) || items["Fried rice"]["available"] != false || items["Fried rice"]["photo"] == nil {
		t.Fatalf("catalogue: %v", cat)
	}

	// Hours (republished), closing with a message, reopening.
	if r := w.say(t, cn, "hours daily 9am-9pm"); !strings.Contains(r, "Mon–Sun 09:00–21:00") {
		t.Fatalf("hours asked: %q", r)
	}
	if r := w.yes(t, cn); !strings.Contains(r, "Opening hours saved") || !strings.Contains(r, "on your page now") {
		t.Fatalf("hours: %q", r)
	}
	if _, version, _, draft := storefrontRow(t, w.world, "adas-kitchen"); version != 2 || !strings.Contains(draft, `"close": "21:00"`) {
		t.Fatalf("hours republished: %d %s", version, draft)
	}
	if r := w.say(t, cn, "close shop see https://evil.test"); !strings.Contains(r, "no links") {
		t.Errorf("a link: %q", r)
	}
	if r := w.say(t, cn, "close shop today"); !strings.Contains(r, "Closed today. We open again tomorrow.") {
		t.Fatalf("close today asked: %q", r)
	}
	if r := w.say(t, cn, "no"); !strings.Contains(r, "Nothing was changed") {
		t.Fatalf("no: %q", r)
	}
	if r := w.say(t, cn, "close shop Back on Monday"); !strings.Contains(r, "Your page will say: Back on Monday") {
		t.Fatalf("close asked: %q", r)
	}
	if r := w.yes(t, cn); !strings.Contains(r, "Your shop is closed") {
		t.Fatalf("closed: %q", r)
	}
	if status, _, closed, _ := storefrontRow(t, w.world, "adas-kitchen"); status != "closed" || closed == nil || *closed != "Back on Monday" {
		t.Fatalf("closed: %s %v", status, closed)
	}
	if r := w.say(t, cn, "my page"); !strings.Contains(r, "is closed (Back on Monday)") || !strings.Contains(r, "*open shop*") {
		t.Errorf("my page closed: %q", r)
	}
	if r := w.say(t, cn, "open shop"); !strings.Contains(r, "Open your shop again?") {
		t.Fatalf("open asked: %q", r)
	}
	if r := w.yes(t, cn); !strings.Contains(r, "open again") {
		t.Fatalf("opened: %q", r)
	}
	if r := w.say(t, cn, "open shop"); !strings.Contains(r, "already open") {
		t.Errorf("open twice: %q", r)
	}

	// A service: a services table made and mapped, the row added.
	if r := w.say(t, cn, "add service Haircut"); !strings.Contains(r, "*add service Haircut 30 2000*") {
		t.Errorf("bad service: %q", r)
	}
	if r := w.say(t, cn, "add service Braids 2h 15k"); !strings.Contains(r, "*Braids*, 120 minutes, ₦15,000") {
		t.Fatalf("service asked: %q", r)
	}
	if r := w.yes(t, cn); !strings.Contains(r, "Added the service *Braids*") || !strings.Contains(r, "booking form") {
		t.Fatalf("service: %q", r)
	}
	srows := w.rows(t, "services")
	if len(srows) != 1 || srows[0]["name"] != "Braids" || fmt.Sprint(srows[0]["duration_minutes"]) != "120" || fmt.Sprint(srows[0]["price"]) != "15000" {
		t.Fatalf("services: %v", srows)
	}
	svc := owner.must(200, "GET", "/v1/storefront/services", nil)["items"].([]any)
	if len(svc) != 1 || svc[0].(map[string]any)["duration_minutes"] != float64(120) {
		t.Fatalf("services copy: %v", svc)
	}

	// Everything audited, as from the chat.
	audit := toJSON(owner.must(200, "GET", "/v1/audit?limit=200", nil))
	for _, a := range []string{"storefront.save", "storefront.publish", "table.create", "storefront.media_add", "storefront.item_add", "storefront.item_change",
		"storefront.close", "storefront.reopen", "storefront.bookings_save", "storefront.service_add"} {
		if !strings.Contains(audit, `"`+a+`"`) {
			t.Errorf("no %s in the audit log", a)
		}
	}
	var n int
	if err := w.env.DB.Admin.QueryRow(context.Background(), `SELECT count(*) FROM audit_log WHERE action LIKE 'storefront.item_%' AND detail->>'channel' = 'whatsapp'`).Scan(&n); err != nil || n < 6 {
		t.Errorf("chat audit: %d %v", n, err)
	}
}

// Without data tables the page is still made, without a catalogue.
func TestWhatsAppMakeMyPageWithoutTables(t *testing.T) {
	w := newWAWorld(t)
	w.srv.PublicHosts = []string{shopDomain}
	owner := w.tenant(t, "Bola Bakes", "bola@bola.test")
	const on = "+2348010000019"
	w.bind(t, owner, on)
	w.say(t, on, "make my page")
	w.say(t, on, "Bola Bakes")
	w.say(t, on, "skip")
	w.say(t, on, "skip")
	w.say(t, on, "skip")
	r := w.say(t, on, "skip")
	if !strings.Contains(r, "data tables are not available") || !strings.Contains(r, "saved without a product list") || !strings.Contains(r, "https://bola-bakes."+shopDomain) {
		t.Fatalf("summary: %q", r)
	}
	if got := w.send(t, on, whatsapptest.Text("yes")); len(got) != 3 || got[1].Image != "https://bola-bakes."+shopDomain+"/qr.png" {
		t.Fatalf("published: %+v", got)
	}
	if r := w.say(t, on, "price cake 300"); !strings.Contains(r, "no product list yet") {
		t.Errorf("no catalogue: %q", r)
	}
	if r := w.say(t, on, "add product Cake 3000"); !strings.Contains(r, "Add this product?") {
		t.Fatalf("product asked: %q", r)
	}
	if r := w.say(t, on, "yes"); !strings.Contains(r, "data tables are not available") {
		t.Errorf("product without tables: %q", r)
	}
	// Photos need storage.
	w.graph.AddMedia("p-9", "image/jpeg", photo(t), 0)
	if got := w.send(t, on, whatsapptest.Image("p-9", "image/jpeg", "Cake 3000")); len(got) != 1 || !strings.Contains(got[0].Text, "Photos cannot be stored here") {
		t.Errorf("no storage: %+v", got)
	}
}

func TestWhatsAppShopOrders(t *testing.T) {
	w := newWAWorld(t)
	ctx := context.Background()
	key, _ := storefront.InboxKey(bytes.Repeat([]byte{3}, 32))
	w.srv.PublicHosts = []string{shopDomain}
	w.srv.Orders = &storefront.Orders{Pool: w.env.DB.App, Vault: w.env.Vault, InboxKey: key, PreviewDomain: shopDomain,
		Messages: &sender.Queue{Vault: w.env.Vault, HashKey: bytes.Repeat([]byte{8}, 32)},
		Start:    w.env.Store.Start, Signal: w.env.Store.DeliverSignalOnce, Cancel: w.env.Store.CancelRun}
	if err := w.env.Registry.Register(sfconn.New(w.srv.Orders)); err != nil {
		t.Fatal(err)
	}
	owner := w.tenant(t, "Ada's Kitchen", "ada@ada.test")
	const on, vn = "+2348010000029", "+2348010000025"
	w.bind(t, owner, on)
	viewer := w.member(t, owner, "vic@ada.test", "viewer")
	w.bind(t, viewer, vn)
	owner.must(200, "PUT", "/v1/storefront", map[string]any{"slug": "ada-kitchen", "draft": map[string]any{"name": "Ada's Kitchen"}})
	tpl, _ := templates.Default().Get("storefront-order")
	def, err := tpl.Instantiate(map[string]any{}, "Orders")
	if err != nil {
		t.Fatal(err)
	}
	publishFlow(t, owner, string(def))
	owner.must(200, "PUT", "/v1/storefront/actions/order", map[string]any{"enabled": true, "payment": "none"})
	pub := owner.must(200, "POST", "/v1/storefront/publish", nil)
	sfID := uuid.MustParse(pub["id"].(string))
	var tenant uuid.UUID
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT tenant_id FROM storefronts WHERE id = $1`, sfID).Scan(&tenant); err != nil {
		t.Fatal(err)
	}
	ref := storefront.NewReference("ada-kitchen")
	_, hash := storefront.NewStatusKey()
	ct, _ := storefront.SealSubmission(key, sfID, ref, storefront.Submission{Kind: "order", Payment: "none", Channel: "direct", Submitted: time.Now(),
		Basket: []storefront.Line{{ID: "1", Name: "Jollof rice", Quantity: 2, PriceKobo: 150000, LineKobo: 300000}}, TotalKobo: 300000, Fulfilment: "pickup",
		Customer: storefront.Customer{Name: "Bola Ade", Phone: "+2348031234567"}})
	var got *string
	if err := w.env.DB.PublicPool(t, 1).QueryRow(ctx, `SELECT taskiem_public_submit($1, 'order', $2, $3, $4, $5)`,
		sfID, ref, bytes.Repeat([]byte{7}, 32), hash, ct).Scan(&got); err != nil || got == nil {
		t.Fatalf("submit: %v %v", got, err)
	}
	if err := w.srv.Orders.Drain(ctx); err != nil {
		t.Fatal(err)
	}

	if r := w.say(t, vn, "orders"); !strings.Contains(r, "storefront.manage") {
		t.Errorf("viewer: %q", r)
	}
	r := w.say(t, on, "orders")
	if !strings.Contains(r, "Open orders") || !strings.Contains(r, "*"+ref+"* · 2 × Jollof rice · ₦3,000 · waiting for you to accept · B••• •••4567") || strings.Contains(r, "Bola") {
		t.Fatalf("orders: %q", r)
	}
	r = w.say(t, on, "order "+strings.ToLower(ref))
	if !strings.Contains(r, "*"+ref+"*: waiting for you to accept") || !strings.Contains(r, "2 × Jollof rice  ₦3,000") || !strings.Contains(r, "pickup") ||
		!strings.Contains(r, "Total: ₦3,000") || !strings.Contains(r, "Customer: B••• •••4567") || strings.Contains(r, "4567 Bola") {
		t.Fatalf("order: %q", r)
	}
	if r := w.say(t, on, "order AK-NOPE00"); !strings.Contains(r, "no order AK-NOPE00") {
		t.Errorf("unknown: %q", r)
	}
	// Ready is for an accepted or paid order: the customer is told, the
	// status stays; delivered completes it.
	if r := w.say(t, on, "order "+ref+" ready"); !strings.Contains(r, "only an accepted or paid order") {
		t.Errorf("ready too soon: %q", r)
	}
	if _, err := w.srv.Orders.Decide(ctx, tenant, ref, "accept", "", "user:test"); err != nil {
		t.Fatal(err)
	}
	if r := w.say(t, on, "order "+ref+" ready"); !strings.Contains(r, "Tell the customer that order "+ref+" is ready?") {
		t.Fatalf("ready asked: %q", r)
	}
	if r := w.yes(t, on); !strings.Contains(r, "is being told that order "+ref+" is ready") {
		t.Fatalf("ready: %q", r)
	}
	var body string
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT body FROM platform_messages WHERE tenant_id = $1 AND dedup_key LIKE 'ready:%'`, tenant).Scan(&body); err != nil {
		var n int
		_ = w.env.DB.Admin.QueryRow(ctx, `SELECT count(*) FROM platform_messages WHERE tenant_id = $1`, tenant).Scan(&n)
		t.Fatalf("the customer's message: %v (%d queued)", err, n)
	}
	_ = body
	var status string
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT status FROM storefront_orders WHERE reference = $1`, ref).Scan(&status); err != nil || status != "accepted" {
		t.Fatalf("after ready: %s %v", status, err)
	}
	if r := w.say(t, on, "order "+ref+" delivered"); !strings.Contains(r, "Mark order "+ref+" as delivered or collected?") {
		t.Fatalf("delivered asked: %q", r)
	}
	if r := w.yes(t, on); !strings.Contains(r, "Order "+ref+" is completed") {
		t.Fatalf("delivered: %q", r)
	}
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT status FROM storefront_orders WHERE reference = $1`, ref).Scan(&status); err != nil || status != "completed" {
		t.Fatalf("after delivered: %s %v", status, err)
	}
	if r := w.say(t, on, "order "+ref+" collected"); !strings.Contains(r, "is completed and cannot be completed now") {
		t.Errorf("twice: %q", r)
	}
	if r := w.say(t, on, "orders"); !strings.Contains(r, "No open orders") {
		t.Errorf("none open: %q", r)
	}
	if r := w.say(t, on, "my page"); !strings.Contains(r, "Orders today: 1") {
		t.Errorf("today's orders: %q", r)
	}
	if !strings.Contains(toJSON(owner.must(200, "GET", "/v1/audit?limit=200", nil)), "storefront.order_ready") {
		t.Error("ready not audited")
	}
}

// On Telegram: a photo with a caption becomes a product, and the QR code
// is sent as a photo.
func TestTelegramShop(t *testing.T) {
	w := newTGWorld(t)
	w.srv.PublicHosts = []string{shopDomain}
	withPhotos(t, w.world)
	pg := xenibasetest.New(t)
	pg.AddToken(xenibasetest.Token{Secret: tablesPlatformToken, Name: "platform", Scopes: []string{"read", "write", "admin"}, ExpiresAt: time.Now().Add(5 * 365 * 24 * time.Hour)})
	if err := w.env.Registry.Register(xenibase.New(xenibase.Options{BaseURL: pg.URL})); err != nil {
		t.Fatal(err)
	}
	w.srv.Tables = &datatables.Service{Pool: w.env.DB.App, Vault: w.env.Vault, Egress: w.env.Egress, Logger: w.srv.Logger,
		Platform: datatables.Platform{URL: pg.URL, Token: tablesPlatformToken, Org: pg.OrgID}, ConnectorURL: pg.URL, PollInterval: time.Millisecond}
	owner := w.tenant(t, "Ada's Kitchen", "ada@ada.test")
	const ada = int64(6001)
	w.link(t, owner, ada, "ada")
	owner.must(200, "PUT", "/v1/storefront", map[string]any{"slug": "ada-kitchen", "draft": map[string]any{"name": "Ada's Kitchen"}})
	owner.must(200, "POST", "/v1/storefront/publish", nil)

	if got := w.update(t, ada, func(id int64) []byte { return telegramtest.Message(id, ada, "", "/qr") }); len(got) != 0 {
		t.Fatalf("qr as text: %+v", got)
	}
	photos := w.bot.Photos(ada)
	if len(photos) != 1 || photos[0].Photo != "https://ada-kitchen."+shopDomain+"/qr.png" || !strings.Contains(photos[0].Text, "Scan to open Ada's Kitchen") {
		t.Fatalf("qr: %+v", photos)
	}

	w.bot.SetFile("P1", "photos/p1.jpg", photo(t), 0)
	got := w.update(t, ada, func(id int64) []byte { return telegramtest.PhotoMessage(id, ada, "P1", "Zobo ₦300") })
	if len(got) != 1 || !strings.Contains(got[0].Text, "Zobo ₦300 (with the photo)") || len(got[0].Keyboard) != 2 {
		t.Fatalf("photo product: %+v", got)
	}
	if r := w.say(t, ada, "yes"); !strings.Contains(r.Text, "Added Zobo at ₦300") {
		t.Fatalf("added: %+v", r)
	}
	var rows []map[string]any
	if p := pg.Projects(); len(p) == 1 {
		rows = pg.Rows(p[0], "products")
	}
	if len(rows) != 1 || rows[0]["name"] != "Zobo" || rows[0]["photo"] == nil {
		t.Fatalf("rows: %v", rows)
	}
	got = w.update(t, ada, func(id int64) []byte { return telegramtest.PhotoMessage(id, ada, "P1", "") })
	if len(got) != 1 || !strings.Contains(got[0].Text, "name and price as its caption") {
		t.Fatalf("no caption: %+v", got)
	}
}
