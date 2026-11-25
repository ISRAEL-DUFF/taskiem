package api_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/israel-duff/taskiem/engine/formguard"
	"github.com/israel-duff/taskiem/engine/whatsapp"
	"github.com/israel-duff/taskiem/engine/whatsapp/whatsapptest"
)

// The review of forms that sell L4 (docs/spec/forms-that-sell.md §19,
// Review fixes), through the api and the public host.

const followWABA = "300000000000001"

// waStatusCode is WhatsApp's word on a message an own number sent, with
// an error code.
func waStatusCode(pnid, id, status string, code int) []byte {
	body, _ := json.Marshal(map[string]any{"object": "whatsapp_business_account", "entry": []any{map[string]any{"id": "1", "changes": []any{map[string]any{
		"field": "messages", "value": map[string]any{"messaging_product": "whatsapp", "metadata": map[string]any{"phone_number_id": pnid},
			"statuses": []any{map[string]any{"id": id, "status": status, "recipient_id": "2348030000000",
				"errors": []any{map[string]any{"code": code, "title": "Failed"}}}}}}}}}})
	return body
}

// waFollowWorld is a business with its own number (on Taskiem's app),
// whose account holds the follow-up's template approved as utility, and
// an order form that follows up.
type waFollowWorld struct {
	*sellWorld
	id, slug string
	seq      int
}

func newWAFollowWorld(t *testing.T) *waFollowWorld {
	t.Helper()
	w := &waFollowWorld{sellWorld: newSellWorld(t)}
	w.id, w.slug = w.sellerForm(t)
	w.ada.must(200, "PATCH", "/v1/storefront/actions/"+w.id, map[string]any{"followup_on": true})
	w.graph.AddNumber(pnA, tokA, "+234 800 000 0001")
	w.ada.must(200, "PUT", "/v1/whatsapp/number", map[string]any{"phone_number_id": pnA, "waba_id": followWABA, "access_token": tokA, "own_app": false})
	w.newHost(t)
	return w
}

// check asks Meta for the account's templates now.
func (w *waFollowWorld) check() { w.ada.must(200, "POST", "/v1/whatsapp/number/templates/check", nil) }

// form is the order form's page as a buyer opens it.
func (w *waFollowWorld) form(t *testing.T) string {
	t.Helper()
	return w.at(t, sellStoreAt, "/a/"+w.slug, nil).Body.String()
}

// leave keeps a contact typed on a page as keep.js does, and runs the
// follow-ups past the wait.
func (w *waFollowWorld) leave(t *testing.T, page, contact string, extra url.Values) {
	t.Helper()
	km := keepRe.FindStringSubmatch(page)
	if km == nil {
		t.Fatal("no keep on the page")
	}
	v := url.Values{"contact": {contact}, "pkg": {"1"}, "keep": {km[2]}, "website": {""}}
	if strings.Contains(page, "data-keep-wa") {
		v.Set("named", "wa")
	}
	for k, vs := range extra {
		v[k] = vs
	}
	time.Sleep(formguard.DefaultMinFill + 100*time.Millisecond)
	if c := w.at(t, sellStoreAt, km[1], v).Code; c != http.StatusNoContent {
		t.Fatalf("keep: %d", c)
	}
	w.followUps(t)
	if _, err := w.env.DB.Admin.Exec(context.Background(), `UPDATE form_left SET last_keep_at = now() - interval '31 minutes', claimed_until = NULL WHERE action_id = $1 AND followed_up_at IS NULL`, w.id); err != nil {
		t.Fatal(err)
	}
	w.followUps(t)
}

// record is a buyer's record: its channel, WhatsApp state and message id.
func (w *waFollowWorld) record(t *testing.T, mask string) (channel, state, waID string) {
	t.Helper()
	if err := w.env.DB.Admin.QueryRow(context.Background(), `SELECT COALESCE(channel, ''), COALESCE(wa_state, ''), COALESCE(wa_id, '') FROM form_left WHERE action_id = $1 AND contact_mask = $2`,
		w.id, mask).Scan(&channel, &state, &waID); err != nil {
		t.Fatal(err)
	}
	return channel, state, waID
}

// status delivers WhatsApp's status of a message to a hook.
func (w *waFollowWorld) status(t *testing.T, hook, secret, pnid, id string, code int) {
	t.Helper()
	body := waStatusCode(pnid, id, "failed", code)
	w.post(t, hook, body, "X-Hub-Signature-256", whatsapp.Sign(secret, body))
}

// Review of L4 (findings 1 and 2, and the missing tests): WhatsApp falls
// back to a text, in the same pass and once, when its answer is a clear
// no: WhatsApp refusing the send, the template paused, rejected or not
// approved in the business's account since the form was drawn, a
// marketing template held back past the plan's allowance, Meta filing
// the template as marketing since the form was drawn (the consent the
// form asked no longer covers it), in a category that is neither utility
// nor marketing, or holding other words.
func TestReviewL4WhatsAppFallsBackToText(t *testing.T) {
	w := newWAFollowWorld(t)
	ctx := context.Background()
	texts := func() int { _, n := w.smsText(t); return n }
	n := 0
	want := func(buyer, mask string) {
		t.Helper()
		n++
		if got := texts(); got != n || len(w.graph.Sent(buyer)) != 0 {
			t.Fatalf("%s: %d texts (want %d), %d on WhatsApp", mask, got, n, len(w.graph.Sent(buyer)))
		}
		if ch, st, _ := w.record(t, mask); ch != "sms" {
			t.Fatalf("%s: %s %s", mask, ch, st)
		}
	}

	page := w.form(t)
	if !strings.Contains(page, "data-keep-wa") {
		t.Fatal("the form does not name WhatsApp")
	}
	// A keep that does not say the form named WhatsApp (a page drawn
	// before, its script older): a text.
	w.leave(t, page, "08035555001", url.Values{"named": {""}})
	want("+2348035555001", "… 5001")

	// WhatsApp refuses the send (a 400 with its error): a text instead.
	page = w.form(t)
	w.graph.SetFailTo("+2348035556001", 131026)
	w.leave(t, page, "08035556001", nil)
	want("+2348035556001", "… 6001")
	if _, st, _ := w.record(t, "… 6001"); st != "texted" {
		t.Fatalf("refused: %s", st)
	}

	// The template paused, rejected, or no longer in the account after
	// the form was drawn: a text, WhatsApp never asked. (The account
	// lists only what it holds.)
	w.graph.StrictTemplates(followWABA)
	for i, status := range []string{"PAUSED", "REJECTED", ""} {
		page := w.form(t)
		w.graph.SetTemplateStatus(followWABA, followName, status, "")
		w.check()
		buyer := fmt.Sprintf("+234803555700%d", i)
		w.leave(t, page, "0"+buyer[4:], nil)
		want(buyer, "… 700"+fmt.Sprint(i))
		w.graph.SetTemplateStatus(followWABA, followName, "APPROVED", "")
		w.check()
	}

	// Meta filed it as marketing after the form told the buyer it may
	// write on WhatsApp: the consent follows Meta's category, so that
	// buyer, who ticked no box, gets a text.
	page = w.form(t)
	if !strings.Contains(page, consentWA) {
		t.Fatal("the utility line")
	}
	w.graph.SetTemplateMeta(followWABA, followName, "MARKETING", nil)
	w.check()
	tm := templateOf(t, w.ada, followName)
	if tm["meta_category"] != "marketing" || tm["category"] != "utility" {
		t.Fatalf("the owner's list: %v", tm)
	}
	if f := w.ada.must(200, "GET", "/v1/storefront/actions/"+w.id+"/left", nil)["followups"].(map[string]any); f["whatsapp_opt_in"] != true {
		t.Fatalf("marketing asks first: %v", f)
	}
	w.newHost(t) // a host of its own: one address names five contacts a form an hour
	w.leave(t, page, "08035558001", nil)
	want("+2348035558001", "… 8001")
	// The form drawn now asks with the box, its line text or email.
	w.newHost(t)
	if page = w.form(t); strings.Contains(page, consentWA) || !strings.Contains(page, optInWA) {
		t.Fatal("the form under a marketing template")
	}

	// A category that is neither (Meta's authentication, or one Taskiem
	// does not know): no WhatsApp; the form names it not, and a keep
	// saying it was drawn naming WhatsApp is not taken as said.
	w.graph.SetTemplateMeta(followWABA, followName, "AUTHENTICATION", nil)
	w.check()
	if f := w.ada.must(200, "GET", "/v1/storefront/actions/"+w.id+"/left", nil)["followups"].(map[string]any); f["whatsapp"] != nil || f["whatsapp_ready"] != nil {
		t.Fatalf("WhatsApp in another category: %v", f)
	}
	w.newHost(t)
	page = w.form(t)
	if strings.Contains(page, consentWA) || strings.Contains(page, optInWA) || strings.Contains(page, "data-keep-wa") {
		t.Fatal("the form promises WhatsApp in another category")
	}
	w.leave(t, page, "08035558002", url.Values{"named": {"wa"}, "wa": {"1"}})
	want("+2348035558002", "… 8002")

	// Its words not ours (another body): not ready either.
	w.graph.SetTemplateMeta(followWABA, followName, "UTILITY", []whatsapp.MetaComponent{{Type: "BODY", Text: "Buy more {{1}} {{2}}"}})
	w.check()
	if tm := templateOf(t, w.ada, followName); tm["matches"] != false {
		t.Fatalf("other words: %v", tm)
	}
	w.newHost(t)
	if strings.Contains(w.form(t), "data-keep-wa") {
		t.Fatal("the form promises WhatsApp for a template of other words")
	}

	// A marketing template held back past the plan's WhatsApp allowance
	// (ErrTemplateBlocked): the buyer who asked gets a text.
	w.graph.SetTemplateMeta(followWABA, followName, "MARKETING", []whatsapp.MetaComponent{}) // our words again
	w.check()
	w.newHost(t)
	if tm := templateOf(t, w.ada, followName); tm["meta_category"] != "marketing" || tm["matches"] != true {
		t.Fatalf("marketing: %v", tm)
	}
	if err := w.env.Store.SetLimits(ctx, w.biz, map[string]any{"whatsapp_templates_monthly": 1}, "test"); err != nil {
		t.Fatal(err)
	}
	if _, err := w.env.DB.Admin.Exec(ctx, `INSERT INTO whatsapp_template_usage (tenant_id, month, category, sent) VALUES ($1, date_trunc('month', now() AT TIME ZONE 'UTC')::date, 'marketing', 1)`, w.biz); err != nil {
		t.Fatal(err)
	}
	page = w.form(t)
	if !strings.Contains(page, optInWA) {
		t.Fatal("no box for WhatsApp")
	}
	w.leave(t, page, "08035559001", url.Values{"wa": {"1"}})
	want("+2348035559001", "… 9001")
}

// templateOf is a template in the owner's list.
func templateOf(t *testing.T, c *client, name string) map[string]any {
	t.Helper()
	for _, x := range c.must(200, "GET", "/v1/whatsapp/number/templates", nil)["templates"].([]any) {
		if tm := x.(map[string]any); tm["name"] == name {
			return tm
		}
	}
	t.Fatalf("no template %s", name)
	return nil
}

// Review of L4 (finding 3, and the missing tests): WhatsApp's failures
// by kind and by whose they are. 131050 records a stop and texts
// nothing; a failure naming another business's message, or arriving on
// a hook that is not its number's, changes nothing; one on the business's
// own app's hook for its own number is texted once.
func TestReviewL4FailedStatuses(t *testing.T) {
	w := newWAFollowWorld(t)
	ctx := context.Background()
	stops := func() int {
		var n int
		if err := w.env.DB.Admin.QueryRow(ctx, `SELECT count(*) FROM form_followup_stops WHERE tenant_id = $1`, w.biz).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}

	// 131050: the buyer stopped the business's marketing on WhatsApp.
	w.leave(t, w.form(t), "08035561001", nil)
	_, st, waID := w.record(t, "… 1001")
	if st != "sent" || waID == "" {
		t.Fatalf("sent: %s %q", st, waID)
	}
	w.status(t, w.wa, waSecret, pnA, waID, 131050)
	if _, n := w.smsText(t); n != 0 || stops() != 1 {
		t.Fatalf("131050: %d texts, %d stops", n, stops())
	}
	if ch, st, _ := w.record(t, "… 1001"); ch != "whatsapp" || st != "stopped" {
		t.Fatalf("131050: %s %s", ch, st)
	}

	// Another business's message: its record untouched, nothing texted.
	w.tenant(t, "Bola Foods", "bola@bola.test")
	var other string
	if err := w.env.DB.Admin.QueryRow(ctx, `INSERT INTO form_left (id, tenant_id, action_id, contact, contact_hash, contact_kind, last_keep_at, expires_at, followed_up_at,
			outcome, channel, wa_id, wa_state)
		SELECT gen_random_uuid(), t.id, $1, '{}', sha256('bola'::bytea), 'phone', now(), now() + interval '7 days', now(), 'followed', 'whatsapp', 'wamid.bola', 'sent'
		  FROM tenants t WHERE t.name = 'Bola Foods' RETURNING id::text`, w.id).Scan(&other); err != nil {
		t.Fatal(err)
	}
	w.status(t, w.wa, waSecret, pnA, "wamid.bola", 131026)
	var ch, state string
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT channel, wa_state FROM form_left WHERE id = $1`, other).Scan(&ch, &state); err != nil || ch != "whatsapp" || state != "sent" {
		t.Fatalf("another business's record: %s %s %v", ch, state, err)
	}
	if _, n := w.smsText(t); n != 0 {
		t.Fatal("texted for another business's message")
	}

	// The number moves to the business's own app: a failure on Taskiem's
	// hook naming it is not its to take; on its own hook it is texted.
	w.leave(t, w.form(t), "08035561002", nil)
	_, _, waID = w.record(t, "… 1002")
	w.ada.must(200, "PUT", "/v1/whatsapp/number", map[string]any{"phone_number_id": pnA, "waba_id": followWABA, "access_token": tokA, "app_secret": secA, "verify_token": vtA})
	w.srv.WhatsApp.Forget()
	w.status(t, w.wa, waSecret, pnA, waID, 131026)
	if _, n := w.smsText(t); n != 0 {
		t.Fatal("texted from Taskiem's hook for an own app's number")
	}
	// The own app's hook, as the api serves it.
	ownHooks := httptest.NewServer(http.StripPrefix("/channels/whatsapp", w.srv.WhatsAppHooks()))
	t.Cleanup(ownHooks.Close)
	own := ownHooks.URL + "/channels/whatsapp/n/" + pnA
	w.status(t, own, secA, "200000000000999", waID, 131026)
	if _, n := w.smsText(t); n != 0 {
		t.Fatal("texted for another number on the own app's hook")
	}
	w.status(t, own, secA, pnA, waID, 131026)
	w.status(t, own, secA, pnA, waID, 131026)
	if text, n := w.smsText(t); n != 1 || !strings.Contains(text, "https://ada-stores.shops.test/back/") {
		t.Fatalf("the own app's failure: %d %q", n, text)
	}
}

// Review of L4 (missing test): a typed STOP from a number followed up on
// WhatsApp that also has a customer thread with the business: the stop
// is recorded and the number answered once (by the thread's STOP).
func TestReviewL4TypedStopWithAThread(t *testing.T) {
	w := newWAFollowWorld(t)
	ctx := context.Background()
	const buyer = "+2348035562001"
	w.leave(t, w.form(t), "08035562001", nil)
	if len(w.graph.Sent(buyer)) != 1 {
		t.Fatalf("not followed up on WhatsApp: %+v", w.graph.Sent(buyer))
	}
	say := func(text string) []whatsapptest.Sent {
		t.Helper()
		w.seq++
		before := len(w.graph.Sent(buyer))
		body := whatsapptest.Delivery(pnA, buyer, fmt.Sprintf("wamid.l4r%d", w.seq), whatsapptest.Text(text))
		w.post(t, w.wa, body, "X-Hub-Signature-256", whatsapp.Sign(waSecret, body))
		return w.graph.Sent(buyer)[before:]
	}
	say("Hello, is the ear cleaner still available?")
	var threads int
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT count(*) FROM customer_conversations WHERE tenant_id = $1`, w.biz).Scan(&threads); err != nil || threads != 1 {
		t.Fatalf("threads: %d %v", threads, err)
	}
	got := say("STOP")
	if len(got) != 1 {
		t.Fatalf("answered %d times: %+v", len(got), got)
	}
	var stops int
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT count(*) FROM form_followup_stops WHERE tenant_id = $1`, w.biz).Scan(&stops); err != nil || stops != 1 {
		t.Fatalf("stops: %d %v", stops, err)
	}
}

// Review of L4 (missing test): the template's button on the form host
// leads nowhere for a link past its days or a storefront no longer
// published.
func TestReviewL4BackOnTheFormHost(t *testing.T) {
	w := newWAFollowWorld(t)
	ctx := context.Background()
	w.leave(t, w.form(t), "08035563001", nil)
	sent := w.graph.Sent("+2348035563001")
	if len(sent) != 1 || len(sent[0].URLParams) != 1 {
		t.Fatalf("sent: %+v", sent)
	}
	token := sent[0].URLParams[0]
	if r := w.at(t, reachFormHost, "/back/"+token, nil); r.Code != http.StatusFound {
		t.Fatalf("the link: %d", r.Code)
	}
	// The storefront unpublished: not found.
	if _, err := w.env.DB.Admin.Exec(ctx, `UPDATE storefronts SET status = 'closed' WHERE tenant_id = $1`, w.biz); err != nil {
		t.Fatal(err)
	}
	if r := w.at(t, reachFormHost, "/back/"+token, nil); r.Code != http.StatusNotFound {
		t.Fatalf("an unpublished storefront: %d", r.Code)
	}
	if _, err := w.env.DB.Admin.Exec(ctx, `UPDATE storefronts SET status = 'published' WHERE tenant_id = $1`, w.biz); err != nil {
		t.Fatal(err)
	}
	// Past its days: expired.
	if _, err := w.env.DB.Admin.Exec(ctx, `UPDATE form_restores SET expires_at = now() - interval '1 minute' WHERE tenant_id = $1`, w.biz); err != nil {
		t.Fatal(err)
	}
	r := w.at(t, reachFormHost, "/back/"+token, nil)
	if r.Code != http.StatusNotFound || !strings.Contains(r.Body.String(), "This link has expired") {
		t.Fatalf("an expired link: %d", r.Code)
	}
}
