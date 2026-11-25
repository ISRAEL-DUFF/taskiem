package api_test

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/israel-duff/taskiem/connectors/xenibase"
	"github.com/israel-duff/taskiem/connectors/xenibase/xenibasetest"
	"github.com/israel-duff/taskiem/engine/datatables"
	"github.com/israel-duff/taskiem/engine/formguard"
)

// Forms that sell L2, done when (docs/spec/forms-that-sell.md §11): "sell
// <product> on a page" makes the page in the video from a catalogue row.
// In chat the page is shown and made on yes: its front on the catalogue's
// own table, its order form selling the product in the three packages,
// delivered and paid at the door, its workflow from storefront-one-product
// published, all turned on and the page published. On the public host it
// has every part of §5.4; a buyer orders two from the form under it, and
// the order is in Orders. On the web the front reads what it sells, its
// settings change and are checked, and a second is added by the product.
func TestOneProductEndToEnd(t *testing.T) {
	w := newSellWorld(t)
	ada := w.ada
	ctx := context.Background()
	// Data tables, for the catalogue's own table.
	pg := xenibasetest.New(t)
	pg.AddToken(xenibasetest.Token{Secret: tablesPlatformToken, Name: "platform", Scopes: []string{"read", "write", "admin"}, ExpiresAt: time.Now().Add(5 * 365 * 24 * time.Hour)})
	if err := w.env.Registry.Register(xenibase.New(xenibase.Options{BaseURL: pg.URL})); err != nil {
		t.Fatal(err)
	}
	w.srv.Tables = &datatables.Service{Pool: w.env.DB.App, Vault: w.env.Vault, Egress: w.env.Egress, Logger: w.srv.Logger,
		Platform: datatables.Platform{URL: pg.URL, Token: tablesPlatformToken, Org: pg.OrgID}, ConnectorURL: pg.URL, PollInterval: time.Millisecond}
	const adaWA = "+2348020000013"
	ada.must(202, "POST", "/v1/me/whatsapp", map[string]any{"number": adaWA, "password": testPassword})
	ada.must(200, "POST", "/v1/me/whatsapp/verify", map[string]any{"code": codeRe.FindString(w.graph.Last(t, adaWA).Text)})
	say := func(text string) string {
		t.Helper()
		got := w.whatsapp(t, adaWA, text)
		if len(got) == 0 {
			t.Fatalf("%q: no answer", text)
		}
		return got[len(got)-1].Text
	}

	// The business keeps its products in a list of its own (the
	// catalogue's table), as one that adds a product in chat does.
	if r := say("add product Smart Ear Cleaner 25000"); !strings.Contains(r, "Add this product?") {
		t.Fatalf("add product: %q", r)
	}
	say("yes")
	ear := ""
	for _, it := range ada.must(200, "GET", "/v1/storefront/fronts/"+w.homeFront(t)+"/sellable", nil)["items"].([]any) {
		if m := it.(map[string]any); m["name"] == "Smart Ear Cleaner" {
			ear = m["id"].(string)
		}
	}
	if ear == "" {
		t.Fatal("the product is not on the list")
	}

	// Words that name no product, or packages that cannot be read.
	if r := say("sell Rice cooker on a page"); !strings.Contains(r, "No product called *Rice cooker*") {
		t.Fatalf("no such product: %q", r)
	}
	if r := say("sell Smart Ear Cleaner on a page 1 for 25000, 1 for 30000"); !strings.Contains(r, "Those packages cannot be used") {
		t.Fatalf("a quantity twice: %q", r)
	}
	// Shown, then made on yes.
	r := say("sell smart ear cleaner on a page 1 for 25000, 2 for 45,000, 3 for 65k")
	for _, want := range []string{"Make a page for *Smart Ear Cleaner*?", "1 item · ₦25,000; 2 items · ₦45,000; 3 items · ₦65,000", "paid at the door",
		"https://" + sellStoreAt + "/smart-ear-cleaner"} {
		if !strings.Contains(r, want) {
			t.Fatalf("confirm: no %q in %q", want, r)
		}
	}
	r = say("yes")
	if !strings.Contains(r, "Your page for *Smart Ear Cleaner* is on: https://"+sellStoreAt+"/smart-ear-cleaner") || !strings.Contains(r, "pay at the door") {
		t.Fatalf("made: %q", r)
	}
	// Once only.
	if r := say("add a page for Smart Ear Cleaner"); !strings.Contains(r, "has its own page already") {
		t.Fatalf("twice: %q", r)
	}
	var kind, tpl string
	var enabled bool
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT f.kind, f.enabled, COALESCE(v.template_id, '') FROM storefront_fronts f
		JOIN storefront_actions a ON a.front_id = f.id JOIN triggers t ON t.storefront_action_id = a.id AND t.environment = 'prod'
		JOIN workflow_versions v ON v.workflow_id = t.workflow_id AND v.version = t.version WHERE f.slug = 'smart-ear-cleaner'`).Scan(&kind, &enabled, &tpl); err != nil {
		t.Fatal(err)
	}
	if kind != "one-product" || !enabled || tpl != "storefront-one-product" {
		t.Fatalf("the front: %s %v %s", kind, enabled, tpl)
	}

	// The page in the video.
	page := w.at(t, sellStoreAt, "/smart-ear-cleaner", nil)
	body := page.Body.String()
	for _, want := range []string{`<p class="op-promise">Pay on delivery nationwide</p>`, `<h1 class="op-h">Smart Ear Cleaner</h1>`,
		`class="op-chips"`, "Pay when it arrives", "Pay on delivery", `class="packages"`, "<strong>2 items</strong>", "Lekki, ₦1,500 delivery",
		"ready to receive it in 2 to 4 days", `class="op-bar"`, "From ₦25,000", `action="/smart-ear-cleaner/a/order-now"`} {
		if !strings.Contains(body, want) {
			t.Errorf("the page: no %q", want)
		}
	}
	tok := sfTokenRe.FindStringSubmatch(body)
	if page.Code != 200 || tok == nil {
		t.Fatalf("the page: %d", page.Code)
	}
	time.Sleep(formguard.DefaultMinFill + 100*time.Millisecond)
	sent := w.at(t, sellStoreAt, "/smart-ear-cleaner/a/order-now", url.Values{"token": {tok[1]}, "website": {""}, "name": {"Bola Ade"}, "phone": {"0803 555 1234"},
		"address": {"12 Admiralty Way"}, "zone": {"Lekki"}, "pay": {"on_delivery"}, "pkg": {"2"}})
	if sent.Code != http.StatusSeeOther || !strings.HasPrefix(sent.Header().Get("Location"), "/o/") {
		t.Fatalf("ordered: %d %s", sent.Code, sent.Body.String())
	}
	ref := strings.TrimPrefix(strings.SplitN(sent.Header().Get("Location"), "?", 2)[0], "/o/")
	if err := w.orders.Drain(ctx); err != nil {
		t.Fatal(err)
	}
	w.env.Drain(t)
	o := ada.must(200, "GET", "/v1/storefront/orders/"+ref, nil)["order"].(map[string]any)
	if o["total_kobo"] != float64(4650000) || len(o["basket"].([]any)) != 1 || o["basket"].([]any)[0].(map[string]any)["package_qty"] != float64(2) {
		t.Fatalf("the order: %v", o)
	}

	// On the web: the front says what it sells; its settings change, and
	// a chip with an icon not of the set is refused.
	var front map[string]any
	for _, f := range ada.must(200, "GET", "/v1/storefront/fronts", nil)["fronts"].([]any) {
		if m := f.(map[string]any); m["slug"] == "smart-ear-cleaner" {
			front = m
		}
	}
	sells, _ := front["sells"].(map[string]any)
	if front["kind"] != "one-product" || sells == nil || sells["item"] != ear || sells["name"] != "Smart Ear Cleaner" || len(sells["packages"].([]any)) != 3 ||
		front["settings"].(map[string]any)["promise"] != "Pay on delivery nationwide" {
		t.Fatalf("the front on the web: %v", front)
	}
	kinds := ada.must(200, "GET", "/v1/storefront/fronts", nil)["kinds"].([]any)
	if kinds[1].(map[string]any)["name"] != "one-product" {
		t.Fatalf("kinds: %v", kinds)
	}
	id := front["id"].(string)
	ada.must(400, "PATCH", "/v1/storefront/fronts/"+id, map[string]any{"settings": map[string]any{"chips": []map[string]any{{"icon": "bank", "text": "CBN approved"}}}})
	got := ada.must(200, "PATCH", "/v1/storefront/fronts/"+id, map[string]any{"tagline": "See inside your ear and clean it safely.",
		"settings": map[string]any{"promise": "Free delivery today only", "stock_line": true, "chips": []map[string]any{{"icon": "shield", "text": "30-day warranty"}}}})
	if got["settings"].(map[string]any)["promise"] != "Free delivery today only" {
		t.Fatalf("saved: %v", got)
	}
	// Published again with them (the public host reads it within its
	// cache's 30 seconds).
	var model string
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT model::text FROM storefront_snapshots WHERE tenant_id = $1 ORDER BY version DESC LIMIT 1`, w.biz).Scan(&model); err != nil ||
		!strings.Contains(model, `"promise": "Free delivery today only"`) || !strings.Contains(model, "30-day warranty") || !strings.Contains(model, "See inside your ear") {
		t.Fatalf("published with its settings: %v %s", err, model)
	}

	// Added on the web from the product: off, with its order form off.
	ada.must(400, "POST", "/v1/storefront/fronts", map[string]any{"kind": "one-product", "name": "Ear cleaner again"})
	ada.must(400, "POST", "/v1/storefront/fronts", map[string]any{"kind": "one-product", "name": "Ear cleaner again", "item": "nope"})
	made := ada.must(201, "POST", "/v1/storefront/fronts", map[string]any{"kind": "one-product", "name": "Ear cleaner again", "item": ear,
		"packages": []map[string]any{{"qty": 2, "price_kobo": 5200000}}})
	if made["kind"] != "one-product" || made["enabled"] != false || len(made["actions"].([]any)) != 1 || made["actions"].([]any)[0].(map[string]any)["enabled"] != false ||
		len(made["warnings"].([]any)) != 1 || made["sells"].(map[string]any)["item"] != ear {
		t.Fatalf("added on the web: %v", made)
	}
}
