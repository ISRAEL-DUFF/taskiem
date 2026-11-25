package api_test

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/israel-duff/taskiem/connectors/xenibase"
	"github.com/israel-duff/taskiem/connectors/xenibase/xenibasetest"
	"github.com/israel-duff/taskiem/engine/datatables"
	"github.com/israel-duff/taskiem/engine/whatsapp"
	"github.com/israel-duff/taskiem/engine/whatsapp/whatsapptest"
)

// onePageWorld is a business selling Smart Ear Cleaner (₦25,000) from its
// catalogue's own table, with Ada's WhatsApp verified; say sends Ada's
// words and answers the last reply.
type onePageWorld struct {
	*sellWorld
	say func(text string) string
}

const onePageWA = "+2348020000014"

func newOnePageWorld(t *testing.T) *onePageWorld {
	t.Helper()
	w := newSellWorld(t)
	pg := xenibasetest.New(t)
	pg.AddToken(xenibasetest.Token{Secret: tablesPlatformToken, Name: "platform", Scopes: []string{"read", "write", "admin"}, ExpiresAt: time.Now().Add(5 * 365 * 24 * time.Hour)})
	if err := w.env.Registry.Register(xenibase.New(xenibase.Options{BaseURL: pg.URL})); err != nil {
		t.Fatal(err)
	}
	w.srv.Tables = &datatables.Service{Pool: w.env.DB.App, Vault: w.env.Vault, Egress: w.env.Egress, Logger: w.srv.Logger,
		Platform: datatables.Platform{URL: pg.URL, Token: tablesPlatformToken, Org: pg.OrgID}, ConnectorURL: pg.URL, PollInterval: time.Millisecond}
	w.ada.must(202, "POST", "/v1/me/whatsapp", map[string]any{"number": onePageWA, "password": testPassword})
	w.ada.must(200, "POST", "/v1/me/whatsapp/verify", map[string]any{"code": codeRe.FindString(w.graph.Last(t, onePageWA).Text)})
	o := &onePageWorld{sellWorld: w}
	o.say = func(text string) string {
		t.Helper()
		got := w.whatsapp(t, onePageWA, text)
		if len(got) == 0 {
			t.Fatalf("%q: no answer", text)
		}
		return got[len(got)-1].Text
	}
	if r := o.say("add product Smart Ear Cleaner 25000"); !strings.Contains(r, "Add this product?") {
		t.Fatalf("add product: %q", r)
	}
	o.say("yes")
	return o
}

// onePages counts the business's One product fronts and lists their
// addresses.
func (w *onePageWorld) onePages(t *testing.T) (int, []string) {
	t.Helper()
	rows, err := w.env.DB.Admin.Query(context.Background(), `SELECT slug FROM storefront_fronts WHERE tenant_id = $1 AND kind = 'one-product' ORDER BY slug`, w.biz)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var slugs []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			t.Fatal(err)
		}
		slugs = append(slugs, s)
	}
	return len(slugs), slugs
}

// Forms that sell L2's review fixes, in chat: the packages read whole or
// refused naming the bit not understood (never junk read as packages, a
// package dropped, or a name split at a number); a price far below the
// catalogue's warned; "on a page of its own"; "make a page for …" a One
// product page only when it names a product; the address shown the one
// made, a taken one too; and two yeses at once make one page.
func TestOneProductChatFixes(t *testing.T) {
	w := newOnePageWorld(t)
	say := w.say

	// Junk after a package is named, not read as packages.
	if r := say("sell smart ear cleaner on a page 1 for 25k and free delivery for Lagos 2 at 3 for 7k"); !strings.Contains(r, "I did not understand *free delivery for Lagos 2 at*") {
		t.Fatalf("junk: %q", r)
	}
	if r := say("sell smart ear cleaner on a page 1 for 25k, 2 for 45 000"); !strings.Contains(r, "1 item · ₦25,000; 2 items · ₦45,000") {
		t.Fatalf("45 000: %q", r)
	}
	say("no")
	// A quantity out of range is refused, not dropped.
	if r := say("sell smart ear cleaner on a page 1 for 25k, 100 for 2m"); !strings.Contains(r, "Those packages cannot be used") {
		t.Fatalf("100 for 2m: %q", r)
	}
	// A price far below the catalogue's is warned on the confirmation.
	if r := say("sell smart ear cleaner on a page 1 for 25k, 2 for 45"); !strings.Contains(r, "Make a page for") || !strings.Contains(r, "under a quarter of its own price of ₦25,000") {
		t.Fatalf("far below: %q", r)
	}
	say("no")
	if r := say("sell Smart Ear Cleaner on a page of its own"); !strings.Contains(r, "Make a page for *Smart Ear Cleaner*?") {
		t.Fatalf("of its own: %q", r)
	}
	say("no")
	// "make a page for" words naming no product are not a One product
	// page; naming one, they are.
	if r := say("make a page for my bakery"); strings.Contains(r, "No product called") || strings.Contains(r, "Make a page for") {
		t.Fatalf("make a page for a non-product: %q", r)
	}
	if r := say("make a page for Smart Ear Cleaner 1 for 25000"); !strings.Contains(r, "Make a page for *Smart Ear Cleaner*?") {
		t.Fatalf("make a page for a product: %q", r)
	}
	say("no")

	// A front holds the product's address already: the confirmation shows
	// the one the page will have.
	w.ada.must(201, "POST", "/v1/storefront/fronts", map[string]any{"kind": "custom", "name": "Smart Ear Cleaner"})
	r := say("sell smart ear cleaner on a page")
	if !strings.Contains(r, "https://"+sellStoreAt+"/smart-ear-cleaner-2") {
		t.Fatalf("the address shown: %q", r)
	}
	// Two yeses at once (a webhook sent twice): one page. The question's
	// row is held while they arrive, so each reads the question before
	// either lets go of it, as two at once do.
	hold, err := w.env.DB.Admin.Begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := hold.Exec(context.Background(), `SELECT 1 FROM chat_sessions WHERE number = $1 FOR UPDATE`, onePageWA); err != nil {
		t.Fatal(err)
	}
	go func() {
		time.Sleep(2 * time.Second)
		_ = hold.Commit(context.Background())
	}()
	var wg sync.WaitGroup
	errs := make(chan error, 4)
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			body := whatsapptest.Delivery(waPNID, onePageWA, fmt.Sprintf("wamid.yes-at-once-%d", i), whatsapptest.Text("yes"))
			req, _ := http.NewRequest("POST", w.wa, bytes.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("X-Hub-Signature-256", whatsapp.Sign(waSecret, body))
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				errs <- err
				return
			}
			_ = resp.Body.Close()
			if resp.StatusCode != 200 {
				errs <- fmt.Errorf("yes %d: %d", i, resp.StatusCode)
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	if n, slugs := w.onePages(t); n != 1 || slugs[0] != "smart-ear-cleaner-2" {
		t.Fatalf("two yeses at once: %d pages %v", n, slugs)
	}
	var made int
	for _, m := range w.graph.Sent(onePageWA) {
		if strings.Contains(m.Text, "Your page for *Smart Ear Cleaner* is on: https://"+sellStoreAt+"/smart-ear-cleaner-2") {
			made++
		}
	}
	if made != 1 {
		t.Fatalf("told it was made %d times", made)
	}
}

// Under the approval rule the page's workflow is asked for, not
// published, and the page stays off.
func TestOneProductChatHeldForApproval(t *testing.T) {
	w := newOnePageWorld(t)
	w.ada.must(200, "PUT", "/v1/governance", map[string]any{"four_eyes_publish": true})
	if r := w.say("sell smart ear cleaner on a page"); !strings.Contains(r, "Make a page for *Smart Ear Cleaner*?") {
		t.Fatalf("confirm: %q", r)
	}
	if r := w.say("yes"); !strings.Contains(r, "Made the page for *Smart Ear Cleaner*. It stays off until its orders are set up") {
		t.Fatalf("held: %q", r)
	}
	var enabled, actionOn bool
	var requests int
	ctx := context.Background()
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT f.enabled, a.enabled FROM storefront_fronts f JOIN storefront_actions a ON a.front_id = f.id
		WHERE f.tenant_id = $1 AND f.kind = 'one-product'`, w.biz).Scan(&enabled, &actionOn); err != nil {
		t.Fatal(err)
	}
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT count(*) FROM publish_requests WHERE tenant_id = $1 AND status = 'pending'`, w.biz).Scan(&requests); err != nil {
		t.Fatal(err)
	}
	if enabled || actionOn || requests != 1 {
		t.Fatalf("held: front on %v, form on %v, %d requests", enabled, actionOn, requests)
	}
	if page := w.at(t, sellStoreAt, "/smart-ear-cleaner", nil); page.Code == 200 && strings.Contains(page.Body.String(), "op-h") {
		t.Fatal("the held page is drawn")
	}
}

// On the web: a One product front needs the page first (409, as every
// kind does) and Orders on (409); settings are a One product front's only.
func TestOneProductWebRefusals(t *testing.T) {
	w := newOnePageWorld(t)
	ear := ""
	for _, it := range w.ada.must(200, "GET", "/v1/storefront/fronts/"+w.homeFront(t)+"/sellable", nil)["items"].([]any) {
		if m := it.(map[string]any); m["name"] == "Smart Ear Cleaner" {
			ear = m["id"].(string)
		}
	}
	// A business with no page yet.
	bob := w.tenant(t, "Bob Shop", "bob@shop.test")
	got := bob.must(409, "POST", "/v1/storefront/fronts", map[string]any{"kind": "one-product", "name": "Ear cleaner", "item": "ear"})
	if !strings.Contains(fmt.Sprint(got), "make your page first") {
		t.Fatalf("no page: %v", got)
	}
	// Settings are a One product front's only.
	w.ada.must(400, "PATCH", "/v1/storefront/fronts/"+w.homeFront(t), map[string]any{"settings": map[string]any{"promise": "Pay on delivery"}})
	// Orders off (Settings keeps Orders on while the shop is, so the
	// stored switch is set as a business's from before that rule is).
	if _, err := w.env.DB.Admin.Exec(context.Background(), `INSERT INTO business_areas (tenant_id, area, enabled, updated_by) VALUES ($1, 'orders', false, 'user:ada')
		ON CONFLICT (tenant_id, area) DO UPDATE SET enabled = false`, w.biz); err != nil {
		t.Fatal(err)
	}
	got = w.ada.must(409, "POST", "/v1/storefront/fronts", map[string]any{"kind": "one-product", "name": "Ear cleaner", "item": ear})
	if !strings.Contains(fmt.Sprint(got), "Orders") {
		t.Fatalf("Orders off: %v", got)
	}
	if n, _ := w.onePages(t); n != 0 {
		t.Fatalf("made with Orders off: %d", n)
	}
}
