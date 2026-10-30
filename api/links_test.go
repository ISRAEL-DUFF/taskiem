package api_test

import (
	"net/url"
	"strings"
	"testing"
)

// Business links, L0 (docs/spec/business-links.md §5): a business that
// lets itself be found is found by name, asked in chat with the role, and
// told with Accept, Decline and Block; Accept links both sides and tells
// the requester. A request by phone number answers the same words whether
// a business is behind it or not, and a decline is never told. Only
// owners and admins ask and answer.
func TestBusinessLinksByChat(t *testing.T) {
	w := newWAWorld(t)
	ada := w.tenant(t, "Ada's Kitchen", "ada@kitchen.test")
	dangote := w.tenant(t, "Dangote Stores", "d@dangote.test")
	cherry := w.tenant(t, "Cherry Blocks", "c@cherry.test")
	viewer := addMember(t, w.world, ada, "vic", "viewer")
	const adaOn, dangoteOn, cherryOn = "+2348010000301", "+2348010000302", "+2348010000303"
	w.bind(t, ada, adaOn)
	w.bind(t, dangote, dangoteOn)
	w.bind(t, cherry, cherryOn)

	// Found only once it chooses to be: no storefront, so off by default.
	search := func(c *client, q string) map[string]any {
		return c.must(200, "GET", "/v1/links/search?q="+url.QueryEscape(q), nil)
	}
	if got := search(ada, "dangote")["results"].([]any); len(got) != 0 {
		t.Fatalf("found before choosing to be: %v", got)
	}
	if st := dangote.must(200, "PUT", "/v1/link-settings", map[string]any{"findable": true}); st["findable_now"] != true {
		t.Fatalf("settings: %v", st)
	}
	got := search(ada, "dangote")["results"].([]any)
	if len(got) != 1 || got[0].(map[string]any)["name"] != "Dangote Stores" || got[0].(map[string]any)["state"] != "none" {
		t.Fatalf("search: %v", got)
	}
	// A phone number is never searched: one row that sends a request.
	if out := search(ada, "0803 123 4567"); len(out["results"].([]any)) != 0 || out["blind"].(map[string]any)["shown"] != "0803…4567" {
		t.Errorf("a phone searched: %v", out)
	}
	// A viewer sees, but may not ask.
	viewer.must(200, "GET", "/v1/links", nil)
	viewer.must(403, "POST", "/v1/links", map[string]any{"business": got[0].(map[string]any)["business"], "role": "supplier"})

	// Dangote's and Cherry's owners have written, so buttons reach them.
	for _, on := range []string{dangoteOn, cherryOn} {
		if r := w.say(t, on, "links"); !strings.Contains(r, "No linked businesses yet") {
			t.Fatalf("links: %q", r)
		}
	}
	// "who sells …", and "find …" that names no data table, search too.
	if r := w.say(t, adaOn, "who sells dangote?"); !strings.Contains(r, "1. *Dangote Stores*") || !strings.Contains(r, "Send the number") {
		t.Errorf("who sells: %q", r)
	}
	w.say(t, adaOn, "cancel")
	if r := w.say(t, adaOn, "find dangote stores"); !strings.Contains(r, "1. *Dangote Stores*") {
		t.Errorf("find: %q", r)
	}
	w.say(t, adaOn, "cancel")
	if r := w.say(t, adaOn, "who sells cement"); !strings.Contains(r, `No business on Taskiem that lets others find it matched "cement"`) {
		t.Errorf("found none: %q", r)
	}
	// Ada asks by chat: the list, the number, the role.
	if r := w.say(t, adaOn, "link Dangote"); !strings.Contains(r, "1. *Dangote Stores*") {
		t.Fatalf("link: %q", r)
	}
	ask := w.send(t, adaOn, map[string]any{"type": "text", "text": map[string]any{"body": "1"}})
	if len(ask) != 1 || !strings.Contains(ask[0].Text, "How do you trade with *Dangote Stores*?") || len(ask[0].Buttons) != 3 {
		t.Fatalf("which role: %+v", ask)
	}
	before := len(w.graph.Sent(dangoteOn))
	if r := w.press(t, adaOn, "lk:role:supplier"); len(r) != 1 || !strings.Contains(r[0].Text, "Asked *Dangote Stores* to link") {
		t.Fatalf("asked: %+v", r)
	}
	told := w.lastTo(t, dangoteOn, before)
	if len(told) != 1 || !strings.Contains(told[0].Text, "*Ada's Kitchen* asks to link with Dangote Stores on Taskiem (they buy from you)") || len(told[0].Buttons) != 3 {
		t.Fatalf("Dangote told: %+v", told)
	}
	accept := told[0].Buttons[0].ID
	if !strings.HasPrefix(accept, "lk:accept:") {
		t.Fatalf("buttons: %+v", told[0].Buttons)
	}
	// Ada cannot press Dangote's Accept.
	if r := w.press(t, adaOn, accept); len(r) != 1 || !strings.Contains(r[0].Text, "no longer waiting") {
		t.Errorf("Ada pressed Accept: %+v", r)
	}

	// Dangote accepts: both linked, Ada told.
	before = len(w.graph.Sent(adaOn))
	if r := w.press(t, dangoteOn, accept); len(r) != 1 || !strings.Contains(r[0].Text, "Linked with *Ada's Kitchen*") {
		t.Fatalf("accept: %+v", r)
	}
	if told := w.lastTo(t, adaOn, before); len(told) != 1 || !strings.Contains(told[0].Text, "*Dangote Stores* accepted the link with Ada's Kitchen") {
		t.Errorf("Ada told: %+v", told)
	}
	links := func(c *client) map[string]any { return c.must(200, "GET", "/v1/links", nil) }
	al := links(ada)["links"].([]any)[0].(map[string]any)
	dl := links(dangote)["links"].([]any)[0].(map[string]any)
	if al["state"] != "active" || al["role"] != "supplier" || dl["state"] != "active" || dl["role"] != "customer" {
		t.Errorf("linked: Ada %v, Dangote %v", al, dl)
	}
	if r := w.say(t, adaOn, "links"); !strings.Contains(r, "• *Dangote Stores*: linked, they supply you") {
		t.Errorf("Ada's links: %q", r)
	}
	// Asking again once linked sends nothing.
	if out := ada.must(201, "POST", "/v1/links", map[string]any{"business": al["business"], "role": "supplier"}); out["sent"] != false ||
		!strings.Contains(out["message"].(string), "already linked") {
		t.Errorf("asked when linked: %v", out)
	}

	// Blind, by phone: the same words for Cherry's owner's number and for
	// a number no business uses.
	nobody := ada.must(202, "POST", "/v1/links", map[string]any{"phone": "0809 000 0000", "role": "both"})
	before = len(w.graph.Sent(cherryOn))
	someone := ada.must(202, "POST", "/v1/links", map[string]any{"phone": "0801 000 0303", "role": "both"})
	if strings.ReplaceAll(nobody["message"].(string), "0809…0000", "X") != strings.ReplaceAll(someone["message"].(string), "0801…0303", "X") {
		t.Errorf("blind answers differ: %q / %q", nobody["message"], someone["message"])
	}
	cherryTold := w.lastTo(t, cherryOn, before)
	if len(cherryTold) != 1 || !strings.Contains(cherryTold[0].Text, "*Ada's Kitchen* asks to link with Cherry Blocks") {
		t.Fatalf("Cherry told: %+v", cherryTold)
	}
	if sent := links(ada)["sent"].([]any); len(sent) != 2 {
		t.Errorf("Ada's blind requests: %v", sent)
	}
	// Cherry declines: Ada hears nothing and still sees it sent.
	before = len(w.graph.Sent(adaOn))
	if r := w.press(t, cherryOn, strings.Replace(cherryTold[0].Buttons[0].ID, "accept", "decline", 1)); len(r) != 1 || !strings.Contains(r[0].Text, "Declined") {
		t.Fatalf("decline: %+v", r)
	}
	if told := w.lastTo(t, adaOn, before); len(told) != 0 {
		t.Errorf("Ada told of a decline: %+v", told)
	}
	if out := links(ada); len(out["links"].([]any)) != 1 || len(out["sent"].([]any)) != 2 {
		t.Errorf("after the decline, Ada sees %v", out)
	}

	// Ada ends the link; Dangote is told.
	before = len(w.graph.Sent(dangoteOn))
	if out := ada.must(200, "POST", "/v1/links/"+al["id"].(string)+"/end", nil); out["state"] != "ended" {
		t.Errorf("end: %v", out)
	}
	if told := w.lastTo(t, dangoteOn, before); len(told) != 1 || !strings.Contains(told[0].Text, "*Ada's Kitchen* ended the link with Dangote Stores") {
		t.Errorf("Dangote told of the end: %+v", told)
	}
	ada.must(409, "POST", "/v1/links/"+al["id"].(string)+"/end", nil)
	// Dangote blocks the ended link, and unblocks it: ended again.
	if out := dangote.must(200, "POST", "/v1/links/"+al["id"].(string)+"/block", nil); out["state"] != "blocked" {
		t.Errorf("block: %v", out)
	}
	if out := dangote.must(200, "POST", "/v1/links/"+al["id"].(string)+"/unblock", nil); out["state"] != "ended" {
		t.Errorf("unblock: %v", out)
	}
	// Ada asks Dangote again; Dangote blocks it and unblocks it: gone for
	// Dangote (204), still waiting for Ada, who is told neither.
	before = len(w.graph.Sent(adaOn))
	ada.must(201, "POST", "/v1/links", map[string]any{"business": al["business"], "role": "supplier"})
	w.srv.WaitBackground()
	dangote.must(200, "POST", "/v1/links/"+al["id"].(string)+"/block", nil)
	dangote.must(204, "POST", "/v1/links/"+al["id"].(string)+"/unblock", nil)
	if out := links(ada)["links"].([]any); len(out) != 1 || out[0].(map[string]any)["state"] != "requested" {
		t.Errorf("Ada after a block and an unblock: %v", out)
	}
	if out := links(dangote)["links"].([]any); len(out) != 0 {
		t.Errorf("Dangote after unblocking: %v", out)
	}
	if told := w.lastTo(t, adaOn, before); len(told) != 0 {
		t.Errorf("Ada told: %+v", told)
	}
}

// A link code (§5.1, across the counter): every business has one, made
// the first time its settings are read; a business that is not found by
// search is still asked by its code, and told. Our own code, or one that
// does not exist, finds nothing.
func TestLinkCodes(t *testing.T) {
	w := newWAWorld(t)
	ada := w.tenant(t, "Ada's Kitchen", "ada@codes.test")
	cherry := w.tenant(t, "Cherry Blocks", "c@codes.test")
	const cherryOn = "+2348010000313"
	w.bind(t, cherry, cherryOn)
	if r := w.say(t, cherryOn, "links"); !strings.Contains(r, "No linked businesses yet") {
		t.Fatalf("links: %q", r)
	}

	st := cherry.must(200, "GET", "/v1/link-settings", nil)
	code, _ := st["code"].(string)
	if len(code) != 8 || st["findable_now"] != false || !strings.HasSuffix(st["url"].(string), "/l/"+code) {
		t.Fatalf("settings: %v", st)
	}
	if again := cherry.must(200, "GET", "/v1/link-settings", nil); again["code"] != code {
		t.Errorf("the code changed: %v", again)
	}
	if st, _, body := cherry.rawGet(t, "/v1/link-settings/qr"); st != 200 || !strings.Contains(body, "<svg") {
		t.Errorf("QR: %d %.60q", st, body)
	}
	cherry.must(404, "GET", "/v1/links/code/"+code, nil)
	ada.must(404, "GET", "/v1/links/code/zzzzzzzz", nil)

	// Not findable, but found by the code it handed out.
	if got := ada.must(200, "GET", "/v1/links/search?q=cherry", nil)["results"].([]any); len(got) != 0 {
		t.Fatalf("found by search: %v", got)
	}
	card := ada.must(200, "GET", "/v1/links/code/"+strings.ToUpper(code), nil)
	if card["name"] != "Cherry Blocks" || card["state"] != "none" {
		t.Fatalf("by code: %v", card)
	}
	before := len(w.graph.Sent(cherryOn))
	out := ada.must(201, "POST", "/v1/links", map[string]any{"code": code, "role": "supplier"})
	if out["sent"] != true || out["link"].(map[string]any)["state"] != "requested" {
		t.Fatalf("asked by code: %v", out)
	}
	if told := w.lastTo(t, cherryOn, before); len(told) != 1 || !strings.Contains(told[0].Text, "*Ada's Kitchen* asks to link with Cherry Blocks") {
		t.Errorf("Cherry told: %+v", told)
	}
	if card := ada.must(200, "GET", "/v1/links/code/"+code, nil); card["state"] != "requested" {
		t.Errorf("by code after asking: %v", card)
	}
	ada.must(400, "POST", "/v1/links", map[string]any{"code": code, "phone": "0803 123 4567", "role": "supplier"})
	ada.must(404, "POST", "/v1/links", map[string]any{"code": "zzzzzzzz", "role": "supplier"})
}
