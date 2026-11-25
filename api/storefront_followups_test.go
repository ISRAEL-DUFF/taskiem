package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/formguard"
)

var (
	keepRe   = regexp.MustCompile(`data-keep="([^"]+)" data-keep-token="([A-Za-z0-9_-]+)"`)
	backRe   = regexp.MustCompile(`https://ada-stores\.shops\.test(/back/[A-Za-z0-9_-]{32})`)
	consentL = "If you leave before you send this, we may send you one message about this order"
)

// sellerForm adds and sets up an order form selling the ear cleaner in
// three packages, delivered to Lekki and Yaba, paid at the door.
func (w *sellWorld) sellerForm(t *testing.T) (id, slug string) {
	t.Helper()
	ada := w.ada
	made := ada.must(201, "POST", "/v1/storefront/fronts/"+w.homeFront(t)+"/actions", map[string]any{"kind": "order", "label": "Order the ear cleaner",
		"sells": map[string]any{"item": "ear", "packages": threePacks}})
	act := made["action"].(map[string]any)
	id, slug = act["id"].(string), act["slug"].(string)
	wf := ada.must(201, "POST", "/v1/storefront/actions/"+id+"/setup", nil)["workflow_id"].(string)
	ada.must(200, "POST", "/v1/workflows/"+wf+"/versions/1/publish", nil)
	ada.must(200, "PUT", "/v1/storefront/actions/"+id, map[string]any{"enabled": true, "label": "Order the ear cleaner", "payment": "after_accept", "options": map[string]any{
		"delivery": true, "pay_on_delivery": true, "zones": []map[string]any{{"name": "Lekki", "fee_kobo": 150000}, {"name": "Yaba", "fee_kobo": 0}}}})
	w.publishPage(t)
	return id, slug
}

// smsText is the latest SMS the business queued, opened.
func (w *sellWorld) smsText(t *testing.T) (string, int) {
	t.Helper()
	ctx := context.Background()
	var text string
	var n int
	if err := db.InTenantTx(ctx, w.env.DB.App, []uuid.UUID{w.biz}, func(tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM platform_messages WHERE channel = 'sms' AND dedup_key LIKE 'followup:%'`).Scan(&n); err != nil || n == 0 {
			return err
		}
		var raw []byte
		if err := tx.QueryRow(ctx, `SELECT body FROM platform_messages WHERE channel = 'sms' AND dedup_key LIKE 'followup:%' ORDER BY created_at DESC LIMIT 1`).Scan(&raw); err != nil {
			return err
		}
		var env map[string]any
		if err := json.Unmarshal(raw, &env); err != nil {
			return err
		}
		v, err := w.env.Vault.OpenTx(ctx, tx, w.biz, env)
		if err != nil {
			return err
		}
		text, _ = v.(map[string]any)["text"].(string)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return text, n
}

func (w *sellWorld) followUps(t *testing.T) {
	t.Helper()
	ctx := context.Background()
	if err := w.forms.DrainKeeps(ctx); err != nil {
		t.Fatal(err)
	}
	if err := w.forms.FollowUps(ctx); err != nil {
		t.Fatal(err)
	}
}

// Forms that sell L3, done when (docs/spec/forms-that-sell.md §11): a
// buyer who typed a phone and left gets one text after 30 minutes and
// finishes the order from its link; the owner sees it under Came back.
// On the way: the owner turns follow-ups on in chat, the form says so
// under the contact, a keep carries only the contact and the choice, the
// link's page uses nothing up until Finish my order is pressed, and a
// keep from a page already sent is refused.
func TestFormLeftHalfFilledEndToEnd(t *testing.T) {
	w := newSellWorld(t)
	ada := w.ada
	ctx := context.Background()
	id, slug := w.sellerForm(t)
	const adaWA = "+2348020000003"
	ada.must(202, "POST", "/v1/me/whatsapp", map[string]any{"number": adaWA, "password": testPassword})
	ada.must(200, "POST", "/v1/me/whatsapp/verify", map[string]any{"code": codeRe.FindString(w.graph.Last(t, adaWA).Text)})

	// Off: no consent line, nothing kept.
	page := w.at(t, sellStoreAt, "/a/"+slug, nil).Body.String()
	if strings.Contains(page, consentL) || strings.Contains(page, "data-keep") {
		t.Fatal("a form that does not follow up says it does")
	}

	// In chat: two order forms, so which one; then on for this one.
	if got := w.whatsapp(t, adaWA, "follow-ups on"); len(got) == 0 || !strings.Contains(got[len(got)-1].Text, "Which order form") {
		t.Fatalf("which: %+v", got)
	}
	got := w.whatsapp(t, adaWA, "follow-ups on for Order the ear cleaner")
	if len(got) == 0 || !strings.Contains(got[len(got)-1].Text, "Follow-ups are on for *Order the ear cleaner*") || !strings.Contains(got[len(got)-1].Text, "after 30 minutes") {
		t.Fatalf("on: %+v", got)
	}
	if got := w.whatsapp(t, adaWA, "follow-ups on for order the ear cleaner"); !strings.Contains(got[len(got)-1].Text, "on already") {
		t.Fatalf("on again: %+v", got)
	}
	left := ada.must(200, "GET", "/v1/storefront/actions/"+id+"/left", nil)
	if f := left["followups"].(map[string]any); f["on"] != true || f["wait_minutes"] != float64(30) {
		t.Fatalf("the settings: %v", left)
	}

	// Framed on the seller's site: the consent line under the contact, and
	// the page's keep token.
	if _, err := w.env.DB.Admin.Exec(ctx, `INSERT INTO embed_sites (id, tenant_id, origin, method, token, verified_at, created_by)
		VALUES (gen_random_uuid(), $1, $2, 'dns', 'taskiem-site-verify=abcdefghijklmnopqrstuvwxyz012345', now(), 'user:ada')`, w.biz, sellSite); err != nil {
		t.Fatal(err)
	}
	w.newHost(t)
	page = w.at(t, sellStoreAt, "/embed/a/"+slug, nil).Body.String()
	km := keepRe.FindStringSubmatch(page)
	if !strings.Contains(page, consentL) || km == nil || km[1] != "/embed/a/"+slug+"/keep" || !strings.Contains(page, "keep.") {
		t.Fatalf("the form: consent %v keep %v", strings.Contains(page, consentL), km)
	}
	keep := func(path string, v url.Values) int {
		t.Helper()
		return w.at(t, sellStoreAt, path, v).Code
	}
	time.Sleep(formguard.DefaultMinFill + 100*time.Millisecond)
	// Without its token, or with a made-up contact: nothing kept.
	if c := keep(km[1], url.Values{"contact": {"08035551234"}}); c != http.StatusNoContent {
		t.Fatalf("no token: %d", c)
	}
	if c := keep(km[1], url.Values{"keep": {km[2]}, "contact": {"not a phone"}}); c != http.StatusNoContent {
		t.Fatalf("no contact: %d", c)
	}
	// The buyer types their number, picks two to Lekki, and leaves. What
	// else the browser sends (a name, an address) is never read.
	if c := keep(km[1], url.Values{"keep": {km[2]}, "contact": {"0803 555 1234"}, "pkg": {"2"}, "zone": {"Lekki"}, "site": {sellSite},
		"name": {"Bola Ade"}, "address": {"12 Admiralty Way"}, "website": {""}}); c != http.StatusNoContent {
		t.Fatalf("the keep: %d", c)
	}
	w.followUps(t)
	var raw string
	var n int
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT count(*), max(choice::text || coalesce(site, '') || contact::text) FROM form_left WHERE action_id = $1`, id).Scan(&n, &raw); err != nil || n != 1 ||
		!strings.Contains(raw, `"package": 2`) || !strings.Contains(raw, "Lekki") || !strings.Contains(raw, sellSite) || strings.Contains(raw, "Bola") || strings.Contains(raw, "Admiralty") || strings.Contains(raw, "5551234") {
		t.Fatalf("the record: %d %s", n, raw)
	}
	// Within the wait: no text.
	if _, n := w.smsText(t); n != 0 {
		t.Fatal("a text within the wait")
	}
	// After 30 minutes: one text, by SMS, naming the product and package.
	if _, err := w.env.DB.Admin.Exec(ctx, `UPDATE form_left SET last_keep_at = now() - interval '31 minutes' WHERE action_id = $1`, id); err != nil {
		t.Fatal(err)
	}
	w.followUps(t)
	w.followUps(t)
	text, n := w.smsText(t)
	bm := backRe.FindStringSubmatch(text)
	if n != 1 || bm == nil || !strings.HasPrefix(text, "Ada Stores: You started an order for Smart Ear Cleaner (2 items, N45,000) and did not finish. Finish it here: https://") {
		t.Fatalf("the text: %d %q", n, text)
	}
	back := bm[1]

	// The link's page uses nothing up: opened twice (a link preview, then
	// the buyer), Finish my order is still offered.
	for range 2 {
		r := w.at(t, sellStoreAt, back, nil)
		if b := r.Body.String(); r.Code != 200 || !strings.Contains(b, "Finish my order") || !strings.Contains(b, `action="`+back+`"`) || !strings.Contains(b, "Stop these messages") {
			t.Fatalf("the link's page: %d %s", r.Code, b)
		}
	}
	// Finish my order: the form with two to Lekki chosen.
	r := w.at(t, sellStoreAt, back, url.Values{})
	loc := r.Header().Get("Location")
	if r.Code != http.StatusSeeOther || loc != "/a/"+slug+"?pkg=2&zone=Lekki" {
		t.Fatalf("finish: %d %q", r.Code, loc)
	}
	if again := w.at(t, sellStoreAt, back, url.Values{}); again.Code != http.StatusGone || !strings.Contains(again.Body.String(), "used already") {
		t.Fatalf("finish twice: %d", again.Code)
	}
	if b := w.at(t, sellStoreAt, back, nil).Body.String(); strings.Contains(b, "Finish my order") {
		t.Fatal("a used link offers Finish my order")
	}
	form := w.at(t, sellStoreAt, loc, nil).Body.String()
	if !regexp.MustCompile(`name="pkg" value="2"[^>]*checked`).MatchString(form) || !strings.Contains(form, `value="Lekki" data-fee="150000" selected`) {
		t.Fatalf("restored: %s", form)
	}
	km = keepRe.FindStringSubmatch(form)
	tok := sfTokenRe.FindStringSubmatch(form)
	time.Sleep(formguard.DefaultMinFill + 100*time.Millisecond)
	// They type their number again and order.
	sent := w.at(t, sellStoreAt, "/a/"+slug, url.Values{"token": {tok[1]}, "website": {""}, "name": {"Bola Ade"}, "phone": {"08035551234"},
		"address": {"12 Admiralty Way"}, "zone": {"Lekki"}, "pay": {"on_delivery"}, "pkg": {"2"}})
	if sent.Code != http.StatusSeeOther {
		t.Fatalf("ordered: %d %s", sent.Code, sent.Body.String())
	}
	// The page's keep, sent as it is left after the order: refused.
	keep(km[1], url.Values{"keep": {km[2]}, "contact": {"08035551234"}, "pkg": {"2"}})
	var inbox int
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT count(*) FROM form_left_inbox`).Scan(&inbox); err != nil || inbox != 0 {
		t.Fatalf("a keep after the order: %d %v", inbox, err)
	}
	if err := w.orders.Drain(ctx); err != nil {
		t.Fatal(err)
	}

	// The owner sees it under Came back, the contact masked.
	left = ada.must(200, "GET", "/v1/storefront/actions/"+id+"/left", nil)
	week := left["week"].(map[string]any)
	rows := left["left"].([]any)
	if week["left"] != float64(1) || week["followed"] != float64(1) || week["came_back"] != float64(1) || len(rows) != 1 {
		t.Fatalf("the owner's view: %v", left)
	}
	row := rows[0].(map[string]any)
	if row["contact"] != "… 1234" || row["channel"] != "sms" || row["package"] != float64(2) || row["zone"] != "Lekki" || row["product"] != "Smart Ear Cleaner" || row["came_back_at"] == nil {
		t.Fatalf("the row: %v", row)
	}
	if b, _ := json.Marshal(left); strings.Contains(string(b), "5551234") || strings.Contains(string(b), "Bola") {
		t.Fatalf("more than the mask: %s", b)
	}
	// The chat alert.
	if _, err := w.env.DB.Admin.Exec(ctx, `UPDATE tenants SET chat_tools = true WHERE id = $1`, w.biz); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, a := range ada.must(200, "GET", "/v1/chat-alerts", nil)["alerts"].([]any) {
		m := a.(map[string]any)
		found = found || (m["kind"] == "left" && m["title"] == "1 person left your order form today" && strings.HasPrefix(m["line"].(string), "1 was followed up") && m["id"] == id)
	}
	if !found {
		t.Fatal("no chat alert")
	}

	// Stop, from the link's page.
	if b := w.at(t, sellStoreAt, back+"/stop", url.Values{}).Body.String(); !strings.Contains(b, "no more messages like this from Ada Stores") {
		t.Fatalf("stop: %s", b)
	}
	// The settings: a wait out of range refused; off from chat.
	if e := ada.must(400, "PATCH", "/v1/storefront/actions/"+id, map[string]any{"followup_wait": 5}); !strings.Contains(e["error"].(string), "10 minutes to 24 hours") {
		t.Fatalf("wait: %v", e)
	}
	if f := ada.must(200, "PATCH", "/v1/storefront/actions/"+id, map[string]any{"followup_wait": 60})["followups"].(map[string]any); f["wait_minutes"] != float64(60) {
		t.Fatalf("wait 60: %v", f)
	}
	if got := w.whatsapp(t, adaWA, "follow-ups off for Order the ear cleaner"); !strings.Contains(got[len(got)-1].Text, "Follow-ups are off for *Order the ear cleaner*") {
		t.Fatalf("off: %+v", got)
	}
	w.newHost(t)
	if page := w.at(t, sellStoreAt, "/a/"+slug, nil).Body.String(); strings.Contains(page, consentL) {
		t.Fatal("the consent line once off")
	}
	// A hosted form that sells nothing has nothing to show.
	ada.must(404, "GET", "/v1/forms/"+uuid.NewString()+"/left", nil)
}

var _ = httptest.NewRecorder
