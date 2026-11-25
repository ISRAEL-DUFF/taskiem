package api_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/israel-duff/taskiem/engine/formguard"
	"github.com/israel-duff/taskiem/engine/whatsapp"
	"github.com/israel-duff/taskiem/engine/whatsapp/whatsapptest"
)

const (
	consentWA  = "on WhatsApp or by text or email, with a link to finish it. On WhatsApp, reply STOP to stop these messages"
	optInWA    = "Send that message on WhatsApp instead (reply STOP there to stop)"
	followName = "taskiem_finish_order"
)

// waStatus is WhatsApp's word on a message an own number sent.
func waStatus(pnid, id, status string) []byte {
	body, _ := json.Marshal(map[string]any{"object": "whatsapp_business_account", "entry": []any{map[string]any{"id": "1", "changes": []any{map[string]any{
		"field": "messages", "value": map[string]any{"messaging_product": "whatsapp", "metadata": map[string]any{"phone_number_id": pnid},
			"statuses": []any{map[string]any{"id": id, "status": status, "recipient_id": "2348030000000",
				"errors": []any{map[string]any{"code": 131026, "title": "Message undeliverable"}}}}}}}}}})
	return body
}

// Forms that sell L4, done when (docs/spec/forms-that-sell.md §11): a
// buyer on a business with its own WhatsApp number is followed up on
// WhatsApp with Finish my order, by the approved template only. Follow-ups
// go by text, email or WhatsApp, and nothing in the deployment's
// environment switches WhatsApp on (the founder, 25 November 2026): the
// world here is wired as serve wires it, with no follow-up setting. On
// the way: with no own number the consent line and the follow-up are
// L3's; with the built-in template approved in the business's own account
// the form says WhatsApp and STOP; not approved there, a text; the owner
// turning WhatsApp off, a text; the button opens the link on the form
// host, which leads to the storefront's page that uses nothing up; the
// owner sees which channel each follow-up used; the template's Stop and a
// typed STOP stop the business's follow-ups; a WhatsApp send reported
// failed is texted instead; and where Meta files the template as
// marketing, the form asks the buyer first.
func TestFormLeftOnWhatsAppEndToEnd(t *testing.T) {
	w := newSellWorld(t)
	ada := w.ada
	ctx := context.Background()
	id, slug := w.sellerForm(t)
	ada.must(200, "PATCH", "/v1/storefront/actions/"+id, map[string]any{"followup_on": true})
	// toOwn delivers a message to the business's own number.
	var seq int
	toOwn := func(from string, msg map[string]any) []whatsapptest.Sent {
		t.Helper()
		seq++
		before := len(w.graph.Sent(from))
		body := whatsapptest.Delivery(pnA, from, fmt.Sprintf("wamid.l4in%d", seq), msg)
		w.post(t, w.wa, body, "X-Hub-Signature-256", whatsapp.Sign(waSecret, body))
		return w.graph.Sent(from)[before:]
	}
	keep := func(page string, v url.Values) {
		t.Helper()
		km := keepRe.FindStringSubmatch(page)
		if km == nil {
			t.Fatal("no keep on the page")
		}
		v.Set("keep", km[2])
		v.Set("website", "")
		if strings.Contains(page, "data-keep-wa") {
			v.Set("named", "wa") // keep.js: the form named WhatsApp
		}
		time.Sleep(formguard.DefaultMinFill + 100*time.Millisecond)
		if c := w.at(t, sellStoreAt, km[1], v).Code; c != http.StatusNoContent {
			t.Fatalf("keep: %d", c)
		}
		w.followUps(t)
		if _, err := w.env.DB.Admin.Exec(ctx, `UPDATE form_left SET last_keep_at = now() - interval '31 minutes', claimed_until = NULL WHERE action_id = $1 AND followed_up_at IS NULL`, id); err != nil {
			t.Fatal(err)
		}
		w.followUps(t)
	}

	// No own number: the form says text or email, the owner sees no
	// WhatsApp, the buyer gets a text (as L3).
	w.newHost(t)
	page := w.at(t, sellStoreAt, "/a/"+slug, nil).Body.String()
	if !strings.Contains(page, consentL) || strings.Contains(page, "WhatsApp or by text") {
		t.Fatal("the consent line without an own number")
	}
	if f := ada.must(200, "GET", "/v1/storefront/actions/"+id+"/left", nil)["followups"].(map[string]any); f["whatsapp"] != nil || f["whatsapp_ready"] != nil || f["whatsapp_on"] != true {
		t.Fatalf("whatsapp without an own number: %v", f)
	}
	const textBuyer = "+2348035550001"
	keep(page, url.Values{"contact": {"0803 555 0001"}, "pkg": {"1"}})
	if _, n := w.smsText(t); n != 1 || len(w.graph.Sent(textBuyer)) != 0 {
		t.Fatalf("by text: %d sms, %d WhatsApp", n, len(w.graph.Sent(textBuyer)))
	}

	// The business connects its own number. Its account holds the
	// built-in template, not yet approved: no WhatsApp yet, a text.
	const waba = "300000000000001"
	w.graph.StrictTemplates(waba)
	w.graph.SetTemplateStatus(waba, followName, "PENDING", "")
	w.graph.AddNumber(pnA, tokA, "+234 800 000 0001")
	ada.must(200, "PUT", "/v1/whatsapp/number", map[string]any{"phone_number_id": pnA, "waba_id": waba, "access_token": tokA, "own_app": false})
	ada.must(200, "POST", "/v1/whatsapp/number/templates/check", nil)
	if tm := templateOf(t, ada, followName); tm["status"] != "pending" {
		t.Fatalf("the template before Meta approves it: %v", tm)
	}
	if f := ada.must(200, "GET", "/v1/storefront/actions/"+id+"/left", nil)["followups"].(map[string]any); f["whatsapp"] != nil || f["whatsapp_ready"] != nil {
		t.Fatalf("whatsapp before the template is approved: %v", f)
	}
	w.newHost(t)
	page = w.at(t, sellStoreAt, "/a/"+slug, nil).Body.String()
	if !strings.Contains(page, consentL) || strings.Contains(page, "data-keep-wa") {
		t.Fatal("the consent line before the template is approved")
	}
	const pendingBuyer = "+2348035550002"
	keep(page, url.Values{"contact": {"0803 555 0002"}, "pkg": {"1"}})
	if _, n := w.smsText(t); n != 2 || len(w.graph.Sent(pendingBuyer)) != 0 {
		t.Fatalf("not approved, by text: %d sms, %d WhatsApp", n, len(w.graph.Sent(pendingBuyer)))
	}

	// Meta approves it in the account: its registry has the template, as
	// Meta lists it, and the owner's follow-ups may go on WhatsApp.
	w.graph.SetTemplateStatus(waba, followName, "APPROVED", "")
	ada.must(200, "POST", "/v1/whatsapp/number/templates/check", nil)
	if tm := templateOf(t, ada, followName); tm["status"] != "approved" || tm["category"] != "utility" || tm["meta_category"] != "utility" || tm["matches"] != true {
		t.Fatalf("the follow-up's template in the number's registry: %v", tm)
	}
	f := ada.must(200, "GET", "/v1/storefront/actions/"+id+"/left", nil)["followups"].(map[string]any)
	if f["whatsapp"] != true || f["whatsapp_ready"] != true || f["whatsapp_on"] != true || f["whatsapp_opt_in"] != nil {
		t.Fatalf("the owner's settings: %v", f)
	}
	// The form says WhatsApp and STOP now.
	w.newHost(t)
	page = w.at(t, sellStoreAt, "/a/"+slug, nil).Body.String()
	if !strings.Contains(page, consentWA) || strings.Contains(page, optInWA) {
		t.Fatal("the consent line with an own number")
	}

	// A buyer types their number, picks two to Lekki and leaves: after the
	// wait, one WhatsApp message from the business's own number, by the
	// template, with Finish my order and Stop; no text.
	const buyer = "+2348035551234"
	keep(page, url.Values{"contact": {"0803 555 1234"}, "pkg": {"2"}, "zone": {"Lekki"}})
	sent := w.graph.Sent(buyer)
	if len(sent) != 1 {
		t.Fatalf("sent: %+v", sent)
	}
	m := sent[0]
	if m.From != pnA || m.Type != "template" || m.Template != followName || len(m.Payloads) != 1 || m.Payloads[0] != whatsapp.FollowupStopPayload ||
		len(m.URLParams) != 1 || len(m.URLParams[0]) != 32 ||
		m.Text != "[Ada Stores] You started an order for Smart Ear Cleaner (2 items, ₦45,000) and did not finish. Tap Finish my order to finish it, or Stop to get no more messages like this." {
		t.Fatalf("the WhatsApp message: %+v", m)
	}
	if _, n := w.smsText(t); n != 2 {
		t.Fatalf("a text beside WhatsApp: %d", n)
	}
	token := m.URLParams[0]

	// Finish my order opens the form host's /back/<token>: to the link on
	// the storefront, whose page uses nothing up; the post restores it.
	r := w.at(t, reachFormHost, "/back/"+token, nil)
	if r.Code != http.StatusFound || r.Header().Get("Location") != "https://ada-stores.shops.test/back/"+token {
		t.Fatalf("the form host's link: %d %q", r.Code, r.Header().Get("Location"))
	}
	if r := w.at(t, reachFormHost, "/back/nonesuchnonesuchnonesuchnonesuch", nil); r.Code != http.StatusNotFound {
		t.Fatalf("an unknown link: %d", r.Code)
	}
	if r := w.at(t, reachFormHost, "/back/"+token, url.Values{}); r.Code != http.StatusMethodNotAllowed {
		t.Fatalf("a post on the form host: %d", r.Code)
	}
	for range 2 {
		if b := w.at(t, sellStoreAt, "/back/"+token, nil).Body.String(); !strings.Contains(b, "Finish my order") {
			t.Fatal("the link's page used up")
		}
	}
	if r := w.at(t, sellStoreAt, "/back/"+token, url.Values{}); r.Code != http.StatusSeeOther || r.Header().Get("Location") != "/a/"+slug+"?pkg=2&zone=Lekki" {
		t.Fatalf("finish: %d %q", r.Code, r.Header().Get("Location"))
	}

	// The owner sees which channel each follow-up used.
	rows := ada.must(200, "GET", "/v1/storefront/actions/"+id+"/left", nil)["left"].([]any)
	channels := map[string]string{}
	for _, x := range rows {
		row := x.(map[string]any)
		channels[row["contact"].(string)], _ = row["channel"].(string)
	}
	if channels["… 1234"] != "whatsapp" || channels["… 0001"] != "sms" {
		t.Fatalf("the channels: %v", channels)
	}

	// The template's Stop: the business's follow-ups to the number stop,
	// and it is told so.
	got := toOwn(buyer, whatsapptest.QuickReply(whatsapp.FollowupStopPayload, "Stop"))
	if len(got) != 1 || got[0].Text != "You will get no more messages like this from Ada Stores." {
		t.Fatalf("stop: %+v", got)
	}
	var stops int
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT count(*) FROM form_followup_stops WHERE tenant_id = $1`, w.biz).Scan(&stops); err != nil || stops != 1 {
		t.Fatalf("stops: %d %v", stops, err)
	}

	// A number not on WhatsApp: WhatsApp reports the send failed, and it
	// goes by text, once. (Another buyer: a page of their own, as a page
	// keeps one contact.)
	const noWA = "+2348035552222"
	page = w.at(t, sellStoreAt, "/a/"+slug, nil).Body.String()
	keep(page, url.Values{"contact": {"08035552222"}, "pkg": {"1"}})
	if len(w.graph.Sent(noWA)) != 1 {
		t.Fatalf("not sent on WhatsApp: %+v", w.graph.Sent(noWA))
	}
	var waID string
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT wa_id FROM form_left WHERE action_id = $1 AND contact_mask = '… 2222'`, id).Scan(&waID); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		body := waStatus(pnA, waID, "failed")
		w.post(t, w.wa, body, "X-Hub-Signature-256", whatsapp.Sign(waSecret, body))
	}
	text, n := w.smsText(t)
	if n != 3 || !strings.Contains(text, "Smart Ear Cleaner (1 item, N25,000)") || !strings.Contains(text, "https://ada-stores.shops.test/back/") {
		t.Fatalf("the text after the failure: %d %q", n, text)
	}
	// A typed STOP from that number: followed up, no thread of theirs, so
	// it is answered as the follow-ups' stop.
	if got := toOwn(noWA, whatsapptest.Text("STOP")); len(got) != 1 || !strings.Contains(got[0].Text, "no more messages like this from Ada Stores") {
		t.Fatalf("typed stop: %+v", got)
	}
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT count(*) FROM form_followup_stops WHERE tenant_id = $1`, w.biz).Scan(&stops); err != nil || stops != 2 {
		t.Fatalf("stops: %d %v", stops, err)
	}

	// The owner turns WhatsApp off ("Also follow up on WhatsApp from my
	// number"): the form says text or email again, and a buyer is texted.
	f = ada.must(200, "PATCH", "/v1/storefront/actions/"+id, map[string]any{"followup_whatsapp": false})["followups"].(map[string]any)
	if f["whatsapp"] != nil || f["whatsapp_ready"] != true || f["whatsapp_on"] != false || f["on"] != true {
		t.Fatalf("WhatsApp turned off: %v", f)
	}
	w.newHost(t)
	page = w.at(t, sellStoreAt, "/a/"+slug, nil).Body.String()
	if !strings.Contains(page, consentL) || strings.Contains(page, "data-keep-wa") {
		t.Fatal("the consent line with WhatsApp turned off")
	}
	const offBuyer = "+2348035552323"
	keep(page, url.Values{"contact": {"08035552323"}, "pkg": {"1"}})
	if _, n := w.smsText(t); n != 4 || len(w.graph.Sent(offBuyer)) != 0 {
		t.Fatalf("WhatsApp off, by text: %d sms, %d WhatsApp", n, len(w.graph.Sent(offBuyer)))
	}
	// Turned off after a form named WhatsApp: that buyer is texted too.
	ada.must(200, "PATCH", "/v1/storefront/actions/"+id, map[string]any{"followup_whatsapp": true})
	w.newHost(t)
	page = w.at(t, sellStoreAt, "/a/"+slug, nil).Body.String()
	if !strings.Contains(page, consentWA) {
		t.Fatal("WhatsApp back on")
	}
	ada.must(200, "PATCH", "/v1/storefront/actions/"+id, map[string]any{"followup_whatsapp": false})
	const offLater = "+2348035552424"
	keep(page, url.Values{"contact": {"08035552424"}, "pkg": {"1"}})
	if _, n := w.smsText(t); n != 5 || len(w.graph.Sent(offLater)) != 0 {
		t.Fatalf("WhatsApp off since the form was drawn: %d sms, %d WhatsApp", n, len(w.graph.Sent(offLater)))
	}
	ada.must(200, "PATCH", "/v1/storefront/actions/"+id, map[string]any{"followup_whatsapp": true})

	// Meta files the template as marketing in the account: the consent
	// follows Meta's category, so the form asks, and only a buyer who
	// ticks the box is followed up on WhatsApp.
	w.graph.SetTemplateMeta(waba, followName, "MARKETING", nil)
	ada.must(200, "POST", "/v1/whatsapp/number/templates/check", nil)
	if f := ada.must(200, "GET", "/v1/storefront/actions/"+id+"/left", nil)["followups"].(map[string]any); f["whatsapp_opt_in"] != true || f["whatsapp"] != true {
		t.Fatalf("opt-in: %v", f)
	}
	w.newHost(t)
	page = w.at(t, sellStoreAt, "/a/"+slug, nil).Body.String()
	if !strings.Contains(page, consentL) || !strings.Contains(page, optInWA) || !strings.Contains(page, `name="wa" value="1"`) {
		t.Fatal("the opt-in box")
	}
	const notAsked, asked = "+2348035553333", "+2348035554444"
	keep(page, url.Values{"contact": {"08035553333"}, "pkg": {"1"}})
	page = w.at(t, sellStoreAt, "/a/"+slug, nil).Body.String()
	keep(page, url.Values{"contact": {"08035554444"}, "pkg": {"1"}, "wa": {"1"}})
	if len(w.graph.Sent(notAsked)) != 0 || len(w.graph.Sent(asked)) != 1 {
		t.Fatalf("opt-in: not asked %d, asked %d", len(w.graph.Sent(notAsked)), len(w.graph.Sent(asked)))
	}
	if _, n := w.smsText(t); n != 6 {
		t.Fatalf("the one not asked texted: %d", n)
	}
}
