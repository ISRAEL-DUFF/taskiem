package api_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/israel-duff/taskiem/api"
	"github.com/israel-duff/taskiem/engine/lang"
)

// Field prompts and the read-back of a built workflow follow the person's
// language; the tenant's own words (field titles, step names) do not change.
func TestWhatsAppLanguageInputsAndReadBack(t *testing.T) {
	w := newWAWorld(t)
	registerSME(t, w.env.Registry)
	w.srv.AI = &api.AISettings{Provider: waBuildModel(t, nil)}
	w.srv.Languages = &api.LanguageSettings{Enabled: []lang.Tag{lang.EN, lang.YO}}
	owner := w.tenant(t, "Acme", "owner@acme.test")
	const on = "+2348010000031"
	w.bind(t, owner, on)
	w.say(t, on, "language yoruba")
	wf := owner.must(201, "POST", "/v1/workflows", map[string]any{"name": "Pay supplier", "definition": json.RawMessage(waPayFlow)})["id"].(string)
	owner.must(200, "POST", "/v1/workflows/"+wf+"/versions/1/publish", nil)

	if r := w.say(t, on, "run pay"); !strings.Contains(r, yo("wa.field.enter_whole", "label", "Amount")) {
		t.Fatalf("prompt: %q", r)
	}
	if r := w.say(t, on, "lots"); !strings.Contains(r, yo("wa.field.not_whole")) || !strings.Contains(r, yo("wa.field.enter_whole", "label", "Amount")) {
		t.Errorf("refusal: %q", r)
	}
	if r := w.say(t, on, "5000"); !strings.Contains(r, yo("wa.field.enter", "label", "account_number")) {
		t.Errorf("next field: %q", r)
	}
	if r := w.say(t, on, "0123456789"); !strings.Contains(r, yo("wa.field.enter_one_of", "label", "currency", "options", "NGN, USD")) {
		t.Errorf("options: %q", r)
	}
	w.say(t, on, "cancel")

	before := len(w.graph.Sent(on))
	w.say(t, on, "build every morning text me good morning")
	got := w.lastTo(t, on, before+1)
	if len(got) != 1 {
		t.Fatalf("draft: %+v", got)
	}
	for _, want := range []string{"1. " + yo("wd.schedule.daily", "time", "08:00", "tz", "Africa/Lagos"), "2. Text the owner good morning (Termii)"} {
		if !strings.Contains(got[0].Text, want) {
			t.Errorf("read-back lacks %q:\n%s", want, got[0].Text)
		}
	}
	if strings.Contains(got[0].Text, "Every day") {
		t.Errorf("read-back in English:\n%s", got[0].Text)
	}
}

// With more than one language on, USSD callers choose theirs from the
// opening screen; Taskiem's own screens follow it, the menu stays the
// tenant's.
func TestUSSDLanguageMenu(t *testing.T) {
	r := newUSSDRig(t, 1)
	publishUSSD(t, r.owner, "bill", ussdBillFlow)
	const opening = "Acme bills\n1. Electricity\n2. Help"
	// One language: no entry.
	if _, body := r.dial(t, 0, "l-1", ussdPhone, ussdCode, ""); body != "CON "+opening {
		t.Fatalf("english only: %q", body)
	}
	r.edges[0].Languages = &api.LanguageSettings{Enabled: []lang.Tag{lang.EN, lang.YO}}
	list := "Choose your language:\n1. English\n2. Yoruba\n0. Back"
	steps := []struct{ text, want string }{
		{"", "CON " + opening + "\n0. Language"},
		{"0", "CON " + list},
		{"0*7", "CON Invalid choice. Try again.\n" + list},
		{"0*7*0", "CON " + opening + "\n0. Language"},
		{"0*7*0*1", "CON Meter number\n0. Back"},
	}
	for _, s := range steps {
		if _, body := r.dial(t, 0, "l-2", ussdPhone, ussdCode, s.text); body != s.want {
			t.Fatalf("%q: %q, want %q", s.text, body, s.want)
		}
	}
	chosen := "CON " + lang.ASCII(yo("ussd.language.set_draft", "language", "Yorùbá")) + "\n" + opening + "\n0. " + lang.ASCII(yo("ussd.language.entry"))
	if _, body := r.dial(t, 0, "l-3", ussdPhone, ussdCode, "0*2"); body != chosen {
		t.Fatalf("chosen: %q, want %q", body, chosen)
	}
	if _, body := r.dial(t, 0, "l-3", ussdPhone, ussdCode, "0*2*1"); body != "CON Meter number\n0. Back" {
		t.Fatalf("then the menu: %q", body)
	}
	r.wait()
	// Recorded for the number: its next session opens in Yoruba, another
	// number's in English.
	if _, body := r.dial(t, 0, "l-4", ussdPhone, ussdCode, ""); body != "CON "+opening+"\n0. Ede" {
		t.Fatalf("next session: %q", body)
	}
	if _, body := r.dial(t, 0, "l-5", "+2348039999999", ussdCode, ""); body != "CON "+opening+"\n0. Language" {
		t.Fatalf("another number: %q", body)
	}
	// Taskiem's own screens: a session that is not this number's.
	if _, body := r.dial(t, 0, "l-5", ussdPhone, ussdCode, "1"); body != "END "+lang.ASCII(yo("ussd.ended")) {
		t.Fatalf("own text: %q", body)
	}
}
