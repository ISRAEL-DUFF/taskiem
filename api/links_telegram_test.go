package api_test

import (
	"strings"
	"testing"

	"github.com/israel-duff/taskiem/engine/telegram/telegramtest"
)

// The link words on Telegram (L0, docs/spec/business-links.md §5.1 and
// §14): `links`, `link <name>` with the numbered pick and the role
// buttons, the request reaching the other business's owner with Accept,
// Decline and Block, and the acceptance told back; the same words as on
// WhatsApp (TestBusinessLinksByChat), through the Telegram world.
func TestBusinessLinksByTelegram(t *testing.T) {
	w := newTGWorld(t)
	ada := w.tenant(t, "Ada's Kitchen", "ada@tglinks.test")
	dangote := w.tenant(t, "Dangote Stores", "d@tglinks.test")
	const adaOn, dangoteOn int64 = 710001, 710002
	w.link(t, ada, adaOn, "ada_kitchen")
	w.link(t, dangote, dangoteOn, "dangote_stores")
	dangote.must(200, "PUT", "/v1/link-settings", map[string]any{"findable": true})

	sent := func(calls []telegramtest.Call) []telegramtest.Call {
		var out []telegramtest.Call
		for _, c := range calls {
			if c.Method == "sendMessage" {
				out = append(out, c)
			}
		}
		return out
	}
	// A Telegram button's data is an opaque signed code: a button is found
	// by its title.
	button := func(c telegramtest.Call, title string) string {
		for _, row := range c.Keyboard {
			for _, b := range row {
				if b.Text == title {
					return b.Data
				}
			}
		}
		return ""
	}
	press := func(account int64, c telegramtest.Call, data string) []telegramtest.Call {
		t.Helper()
		got := sent(w.update(t, account, func(id int64) []byte { return telegramtest.Callback(id, account, c.MessageID, "q", data) }))
		w.srv.WaitBackground() // the other side is told after answering
		return got
	}

	if r := w.say(t, dangoteOn, "links"); !strings.Contains(r.Text, "No linked businesses yet") {
		t.Fatalf("links: %q", r.Text)
	}
	if r := w.say(t, adaOn, "link Dangote"); !strings.Contains(r.Text, "1. Dangote Stores") {
		t.Fatalf("link: %q", r.Text)
	}
	ask := w.say(t, adaOn, "1")
	role := button(ask, "They supply us")
	if !strings.Contains(ask.Text, "How do you trade with Dangote Stores?") || role == "" {
		t.Fatalf("which role: %q %v", ask.Text, ask.Keyboard)
	}
	before := len(w.bot.Sent(dangoteOn))
	if r := press(adaOn, ask, role); len(r) != 1 || !strings.Contains(r[0].Text, "Asked Dangote Stores to link") {
		t.Fatalf("asked: %+v", r)
	}
	told := sent(w.bot.Sent(dangoteOn)[before:])
	if len(told) != 1 || !strings.Contains(told[0].Text, "Ada's Kitchen asks to link with Dangote Stores on Taskiem (they buy from you)") {
		t.Fatalf("Dangote told: %+v", told)
	}
	accept := button(told[0], "Accept")
	if accept == "" || button(told[0], "Decline") == "" || button(told[0], "Block") == "" {
		t.Fatalf("Accept, Decline and Block: %v", told[0].Keyboard)
	}
	// Ada cannot press Dangote's Accept: a Telegram button is signed to
	// the chat it was sent to, so from hers it has expired, and nothing
	// is linked.
	if r := press(adaOn, told[0], accept); len(r) != 1 || !strings.Contains(r[0].Text, "That button has expired") {
		t.Errorf("Ada pressed Accept: %+v", r)
	}
	if l := ada.must(200, "GET", "/v1/links", nil)["links"].([]any)[0].(map[string]any); l["state"] != "requested" {
		t.Errorf("after Ada's press: %v", l)
	}
	before = len(w.bot.Sent(adaOn))
	if r := press(dangoteOn, told[0], accept); len(r) != 1 || !strings.Contains(r[0].Text, "Linked with Ada's Kitchen") {
		t.Fatalf("accept: %+v", r)
	}
	if r := sent(w.bot.Sent(adaOn)[before:]); len(r) != 1 || !strings.Contains(r[0].Text, "Dangote Stores accepted the link with Ada's Kitchen") {
		t.Errorf("Ada told: %+v", r)
	}
	al := ada.must(200, "GET", "/v1/links", nil)["links"].([]any)[0].(map[string]any)
	dl := dangote.must(200, "GET", "/v1/links", nil)["links"].([]any)[0].(map[string]any)
	if al["state"] != "active" || al["role"] != "supplier" || dl["state"] != "active" || dl["role"] != "customer" {
		t.Errorf("linked: Ada %v, Dangote %v", al, dl)
	}
	if r := w.say(t, adaOn, "links"); !strings.Contains(r.Text, "Dangote Stores: linked, they supply you") {
		t.Errorf("Ada's links: %q", r.Text)
	}
	if r := w.say(t, dangoteOn, "links"); !strings.Contains(r.Text, "Ada's Kitchen: linked, they buy from you") {
		t.Errorf("Dangote's links: %q", r.Text)
	}
}
