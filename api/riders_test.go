package api_test

import (
	"net/url"
	"strings"
	"testing"

	"github.com/israel-duff/taskiem/engine/riders"
)

// TestRiders (dispatch D1): a rider added by phone gets the tap-in link,
// taps in on WhatsApp and is reachable there; the same phone added by a
// second business is two riders and one presence, and off said once
// shows on both; owners list and add riders in chat; retired riders are
// kept.
func TestRiders(t *testing.T) {
	w := newReachWorld(t)
	w.srv.Riders = &riders.Riders{Pool: w.env.DB.App, Vault: w.env.Vault, Book: w.srv.Reach, Messages: w.srv.Messages, PageBase: "https://" + reachFormHost}
	ada := w.tenant(t, "Ada Stores", "ada@ada.test")
	bisi := w.tenant(t, "Bisi Foods", "bisi@bisi.test")

	// Ada sets her delivery areas and adds Musa: he is sent the link.
	ada.must(200, "PUT", "/v1/storefront", map[string]any{"slug": "ada-stores", "draft": map[string]any{"name": "Ada Stores"}})
	ada.must(200, "PUT", "/v1/storefront/actions/order", map[string]any{"payment": "after_accept", "options": map[string]any{"delivery": true,
		"zones": []any{map[string]any{"name": "Lekki", "fee_kobo": 150000}, map[string]any{"name": "Ajah", "fee_kobo": 200000}}}})
	texts := w.texts(t)
	musa := ada.must(201, "POST", "/v1/riders", map[string]any{"name": "Musa", "phone": "0803 123 4567", "zones": []string{"lekki"}, "fee_kobo": 100000})["rider"].(map[string]any)
	if musa["reached"] != "sms" || musa["invited_at"] == nil || toJSON(musa["zones"]) != `["Lekki"]` || w.texts(t) != texts+1 {
		t.Fatalf("added %v (%d texts)", musa, w.texts(t)-texts)
	}
	ada.must(409, "POST", "/v1/riders", map[string]any{"name": "Musa again", "phone": "08031234567"})
	ada.must(400, "POST", "/v1/riders", map[string]any{"name": "Tunde", "phone": "0809 111 2222", "zones": []string{"Ikeja"}})
	ada.must(400, "POST", "/v1/riders", map[string]any{"name": "Tunde", "phone": "12"})
	list := ada.must(200, "GET", "/v1/riders", nil)
	tapIn, _ := list["tap_in_url"].(string)
	if len(list["riders"].([]any)) != 1 || len(list["zones"].([]any)) != 2 || !strings.HasPrefix(tapIn, "https://"+reachFormHost+"/u/") {
		t.Fatalf("list %v", list)
	}
	id := musa["id"].(string)
	ada.must(409, "POST", "/v1/riders/"+id+"/invite", nil) // just sent

	// Musa taps it: WhatsApp proves his number, and he is told he rides.
	path := strings.TrimPrefix(tapIn, "https://"+reachFormHost)
	loc, _ := url.Parse(w.page(t, path+"/whatsapp", nil).Header().Get("Location"))
	const musaWA = "+2348031234567"
	if sent := w.whatsapp(t, musaWA, loc.Query().Get("text")); len(sent) != 1 || !strings.Contains(sent[0].Text, "You're set up as a rider for Ada Stores") {
		t.Fatalf("tapping in: %+v", sent)
	}
	if got := ada.must(200, "GET", "/v1/riders", nil)["riders"].([]any)[0].(map[string]any); got["reached"] != "whatsapp" {
		t.Fatalf("after tapping in: %v", got)
	}

	// Bisi adds the same phone from chat.
	const bisiWA = "+2348020000002"
	bisi.must(202, "POST", "/v1/me/whatsapp", map[string]any{"number": bisiWA, "password": testPassword})
	bisi.must(200, "POST", "/v1/me/whatsapp/verify", map[string]any{"code": codeRe.FindString(w.graph.Last(t, bisiWA).Text)})
	if got := w.whatsapp(t, bisiWA, "riders"); len(got) != 1 || !strings.Contains(got[0].Text, "no riders yet") {
		t.Fatalf("no riders: %+v", got)
	}
	if got := w.whatsapp(t, bisiWA, "add rider Musa Bello 0803 123 4567 Yaba, Surulere"); len(got) != 1 || !strings.Contains(got[0].Text, "Musa Bello is now your rider") {
		t.Fatalf("add rider: %+v", got)
	}
	if got := w.whatsapp(t, bisiWA, "add rider Musa"); len(got) != 1 || !strings.Contains(got[0].Text, "add rider Musa 0803 123 4567") {
		t.Fatalf("add rider without a phone: %+v", got)
	}
	if got := ada.must(200, "GET", "/v1/riders", nil)["riders"].([]any); len(got) != 1 {
		t.Fatalf("Ada sees Bisi's rider: %v", got)
	}

	// Off, once, for both.
	if got := w.whatsapp(t, musaWA, "OFF"); len(got) != 1 || !strings.Contains(got[0].Text, "You're off") {
		t.Fatalf("off: %+v", got)
	}
	if got := ada.must(200, "GET", "/v1/riders", nil)["riders"].([]any)[0].(map[string]any)["presence"].(map[string]any); got["state"] != "off" {
		t.Errorf("Ada sees %v", got)
	}
	if got := w.whatsapp(t, bisiWA, "riders"); len(got) != 1 || !strings.Contains(got[0].Text, "Musa Bello (Yaba, Surulere): off, by SMS") {
		t.Fatalf("Bisi's riders: %+v", got)
	}
	if got := w.whatsapp(t, musaWA, "busy till 3pm"); len(got) != 1 || !strings.Contains(got[0].Text, "busy until 15:00") {
		t.Fatalf("busy: %+v", got)
	}
	if got := w.whatsapp(t, bisiWA, "riders"); len(got) != 1 || !strings.Contains(got[0].Text, "busy till 15:00") {
		t.Fatalf("Bisi's riders while busy: %+v", got)
	}
	if got := w.whatsapp(t, musaWA, "on"); len(got) != 1 || !strings.Contains(got[0].Text, "You're on") {
		t.Fatalf("on: %+v", got)
	}
	// Someone who rides for no one is answered as before.
	if got := w.whatsapp(t, "+2348035550000", "off"); len(got) == 1 && strings.Contains(got[0].Text, "You're off") {
		t.Fatalf("a stranger was taken for a rider: %+v", got)
	}

	// Changed, then retired: kept, listed as retired.
	ch := ada.must(200, "PATCH", "/v1/riders/"+id, map[string]any{"zones": []string{"Lekki", "Ajah"}, "active": false})["rider"].(map[string]any)
	if toJSON(ch["zones"]) != `["Lekki","Ajah"]` || ch["active"] != false {
		t.Fatalf("changed %v", ch)
	}
	ada.must(400, "PATCH", "/v1/riders/"+id, map[string]any{"phone": "08090000000"})
	if r := ada.must(200, "POST", "/v1/riders/"+id+"/retire", nil)["rider"].(map[string]any); r["retired_at"] == nil {
		t.Fatalf("retired %v", r)
	}
	if got := ada.must(200, "GET", "/v1/riders", nil)["riders"].([]any); len(got) != 0 {
		t.Errorf("a retired rider listed: %v", got)
	}
	if got := ada.must(200, "GET", "/v1/riders?retired=true", nil)["riders"].([]any); len(got) != 1 {
		t.Errorf("retired: %v", got)
	}
	bisi.must(404, "POST", "/v1/riders/"+id+"/retire", nil)
}
