package api_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/hex"
	"strings"
	"testing"

	"github.com/israel-duff/taskiem/engine/people"
	"github.com/israel-duff/taskiem/engine/peoplechats"
)

// Finding people ("As built: stage 5a, server (finding people)" in
// docs/spec/people-chatting.md): privacy towards people, contacts,
// lookups by number, a person's link and its page, contact matching,
// presence, and deleting an account.

type findWorld struct {
	*world
	sms *fakeSMS
}

func newFindWorld(t *testing.T) *findWorld {
	t.Helper()
	w, sms := phoneWorld(t)
	w.srv.People = peoplechats.JoinPeople(&people.People{Pool: w.env.DB.App, Key: bytes.Repeat([]byte{5}, 32), Base: w.base})
	w.srv.AppGetURL = "https://get.taskiem.test"
	return &findWorld{world: w, sms: sms}
}

// personNamed signs a new person in by phone and names them.
func (w *findWorld) personNamed(t *testing.T, number, name string) *client {
	t.Helper()
	c, _ := phoneIn(t, w.world, w.sms, number, nil)
	if _, err := w.env.DB.Admin.Exec(context.Background(), `UPDATE users SET name = $2 WHERE id = $1`, personID(t, c), name); err != nil {
		t.Fatal(err)
	}
	return c
}

func idOf(t *testing.T, c *client) string { return personID(t, c).String() }

// add finds other by number as c and adds them to c's contacts.
func (w *findWorld) add(t *testing.T, c *client, number string) map[string]any {
	t.Helper()
	f := c.must(200, "POST", "/v1/people/lookup", map[string]any{"phone": number})
	if f["found"] != true {
		t.Fatalf("not found: %v", f)
	}
	p := f["person"].(map[string]any)
	return c.must(201, "POST", "/v1/me/contacts", map[string]any{"person_id": p["id"], "add_token": f["add_token"], "from": "number"})
}

// The settings on You (§5.4): the founder's defaults, changed in part,
// refused when not one; an API key refused; a business session acts for
// its person.
func TestPeoplePrivacySettings(t *testing.T) {
	w := newFindWorld(t)
	tolu := w.personNamed(t, "+2348032000001", "Tolu")
	got := tolu.must(200, "GET", "/v1/me/privacy/people", nil)
	want := map[string]any{"find_by_number": "contacts", "find_from_contacts": true, "message": "everyone", "last_seen": "contacts", "read_ticks": true, "photo": "contacts", "show_number": false}
	for k, v := range want {
		if got[k] != v {
			t.Fatalf("default %s: %v", k, got)
		}
	}
	got = tolu.must(200, "PUT", "/v1/me/privacy/people", map[string]any{"find_by_number": "everyone", "show_number": true})
	if got["find_by_number"] != "everyone" || got["show_number"] != true || got["message"] != "everyone" {
		t.Fatalf("changed: %v", got)
	}
	tolu.must(400, "PUT", "/v1/me/privacy/people", map[string]any{"photo": "friends"})
	if got = tolu.must(200, "GET", "/v1/me/privacy/people", nil); got["photo"] != "contacts" {
		t.Fatalf("after a refusal: %v", got)
	}
	// A business's owner acts for themself; their API key is refused.
	owner := w.tenant(t, "Ada Stores", "ada@stores.test")
	owner.must(200, "PUT", "/v1/me/privacy/people", map[string]any{"last_seen": "nobody"})
	if got = owner.must(200, "GET", "/v1/me/privacy/people", nil); got["last_seen"] != "nobody" {
		t.Fatalf("a business session: %v", got)
	}
	key := keyFor(t, owner, "dev", "run.read")
	key.must(403, "GET", "/v1/me/privacy/people", nil)
	key.must(403, "POST", "/v1/people/lookup", map[string]any{"phone": "+2348032000001"})
}

// Finding by number (§5.1): hidden by default from strangers (the same
// answer as no one, with the invite words and the finder's link), found
// once the owner adds the finder or chooses everyone; added with the
// token; contacts listed, renamed and removed; a bad number 400; fifty a
// day.
func TestPeopleLookupAndContacts(t *testing.T) {
	w := newFindWorld(t)
	tolu := w.personNamed(t, "+2348032000011", "Tolu")
	ada := w.personNamed(t, "+2348032000012", "Ada")
	f := tolu.must(200, "POST", "/v1/people/lookup", map[string]any{"phone": "0803 200 0012"})
	inv, _ := f["invite"].(map[string]any)
	if f["found"] != false || f["person"] != nil || inv == nil || !strings.Contains(inv["words"].(string), "/p/") || !strings.Contains(inv["words"].(string), "https://get.taskiem.test") {
		t.Fatalf("hidden from a stranger: %v", f)
	}
	nobody := tolu.must(200, "POST", "/v1/people/lookup", map[string]any{"phone": "+2348032999999"})
	if nobody["found"] != false || nobody["invite"].(map[string]any)["link"] != inv["link"] {
		t.Fatalf("no one, the same answer: %v", nobody)
	}
	if e := tolu.must(400, "POST", "/v1/people/lookup", map[string]any{"phone": "hello"}); e["phone_invalid"] != true {
		t.Fatalf("not a number: %v", e)
	}
	if me := tolu.must(200, "POST", "/v1/people/lookup", map[string]any{"phone": "+2348032000011"}); me["found"] != true || me["self"] != true || me["add_token"] != nil {
		t.Fatalf("oneself: %v", me)
	}
	ada.must(200, "PUT", "/v1/me/privacy/people", map[string]any{"find_by_number": "everyone"})
	f = tolu.must(200, "POST", "/v1/people/lookup", map[string]any{"phone": "+2348032000012"})
	p := f["person"].(map[string]any)
	if f["found"] != true || p["name"] != "Ada" || p["id"] != idOf(t, ada) || f["add_token"] == "" || p["photo"] != nil || p["last_seen_at"] != nil {
		t.Fatalf("found: %v", f)
	}
	// Adding needs the token, for the person it was given for.
	tolu.must(403, "POST", "/v1/me/contacts", map[string]any{"person_id": p["id"], "add_token": "nonsense", "from": "number"})
	ada.must(403, "POST", "/v1/me/contacts", map[string]any{"person_id": idOf(t, tolu), "add_token": f["add_token"], "from": "number"})
	tolu.must(400, "POST", "/v1/me/contacts", map[string]any{"person_id": p["id"], "add_token": f["add_token"], "from": "guess"})
	c := tolu.must(201, "POST", "/v1/me/contacts", map[string]any{"person_id": p["id"], "add_token": f["add_token"], "from": "number", "name": "Ada (work)"})
	if c["contact_name"] != "Ada (work)" || c["in_contacts"] != true || c["they_added_you"] != false || c["source"] != "number" {
		t.Fatalf("added: %v", c)
	}
	list := tolu.must(200, "GET", "/v1/me/contacts", nil)["contacts"].([]any)
	if len(list) != 1 || list[0].(map[string]any)["id"] != idOf(t, ada) {
		t.Fatalf("list: %v", list)
	}
	if c = tolu.must(200, "PUT", "/v1/me/contacts/"+idOf(t, ada), map[string]any{"name": nil}); c["contact_name"] != nil {
		t.Fatalf("renamed to none: %v", c)
	}
	ada.must(404, "PUT", "/v1/me/contacts/"+idOf(t, tolu), map[string]any{"name": "x"})
	ada.must(404, "DELETE", "/v1/me/contacts/"+idOf(t, tolu), nil)
	// Ada sees Tolu added her (what her "My contacts" means is her own
	// list, so not yet him).
	view := ada.must(200, "GET", "/v1/people/"+idOf(t, tolu), nil)
	if view["they_added_you"] != true || view["in_contacts"] != false || view["number"] != nil {
		t.Fatalf("Ada's view of Tolu: %v", view)
	}
	tolu.must(204, "DELETE", "/v1/me/contacts/"+idOf(t, ada), nil)
	if list = tolu.must(200, "GET", "/v1/me/contacts", nil)["contacts"].([]any); len(list) != 0 {
		t.Fatalf("after removing: %v", list)
	}
	tolu.must(404, "GET", "/v1/people/"+"00000000-0000-0000-0000-000000000000", nil)
	tolu.must(404, "GET", "/v1/people/not-an-id", nil)

	// Fifty a day per person, counted in the database: the fifty-first
	// is refused for the day.
	kemi := w.personNamed(t, "+2348032000013", "Kemi")
	if _, err := w.env.DB.Admin.Exec(context.Background(), `INSERT INTO people_find_counts (person_id, kind, day, n)
		VALUES ($1, 'number', (now() AT TIME ZONE 'Africa/Lagos')::date, $2)`, personID(t, kemi), people.NumberLookupsPerDay); err != nil {
		t.Fatal(err)
	}
	if e := kemi.must(429, "POST", "/v1/people/lookup", map[string]any{"phone": "+2348032000012"}); e["limit"] != "day" {
		t.Fatalf("the day's limit: %v", e)
	}
}

// A person's link (§5.2): made once, its QR code, its public card (the
// photo only for everyone), the signed-in view with a token; replaced,
// the old one 410 with "no longer works", on the API and the page /p/.
func TestPersonLinkAndPage(t *testing.T) {
	w := newFindWorld(t)
	tolu := w.personNamed(t, "+2348032000021", "Tolu")
	ada := w.personNamed(t, "+2348032000022", "Ada")
	l := tolu.must(200, "GET", "/v1/me/link", nil)
	code := l["code"].(string)
	if l["url"] != w.base+"/p/"+code || l["qr"] != "/v1/me/link/qr" || !strings.Contains(l["words"].(string), l["url"].(string)) {
		t.Fatalf("link: %v", l)
	}
	if again := tolu.must(200, "GET", "/v1/me/link", nil); again["code"] != code {
		t.Fatalf("the same link: %v", again)
	}
	st, hdr, svg := tolu.getRaw(t, "/v1/me/link/qr")
	if st != 200 || hdr.Get("Content-Type") != "image/svg+xml" || !bytes.Contains(svg, []byte("<svg")) {
		t.Fatalf("qr: %d %s", st, hdr.Get("Content-Type"))
	}
	anon := &client{t: t, base: w.base}
	card := anon.must(200, "GET", "/v1/person-links/"+code, nil)
	if card["name"] != "Tolu" || card["photo"] != nil || card["link"] != l["url"] || card["app"] != "https://get.taskiem.test" {
		t.Fatalf("public card: %v", card)
	}
	if st, _ := tolu.putFile(t, "/v1/me/photo", "me.png", pngPhoto(t, 90)); st != 200 {
		t.Fatalf("photo: %d", st)
	}
	if card = anon.must(200, "GET", "/v1/person-links/"+code, nil); card["photo"] != nil {
		t.Fatalf("the photo is for contacts by default: %v", card)
	}
	anon.must(404, "GET", "/v1/person-links/"+code+"/photo", nil)
	tolu.must(200, "PUT", "/v1/me/privacy/people", map[string]any{"photo": "everyone"})
	card = anon.must(200, "GET", "/v1/person-links/"+code, nil)
	ph, _ := card["photo"].(map[string]any)
	if ph == nil || !strings.HasPrefix(ph["url"].(string), "/v1/person-links/"+code+"/photo") {
		t.Fatalf("shown to everyone: %v", card)
	}
	if st, hdr, _ := anon.getRaw(t, ph["url"].(string)); st != 200 || hdr.Get("Content-Type") != "image/jpeg" {
		t.Fatalf("the public photo: %d %s", st, hdr.Get("Content-Type"))
	}
	// Signed in: the person, with a token to add them, and their photo.
	f := ada.must(200, "GET", "/v1/people/links/"+code, nil)
	p := f["person"].(map[string]any)
	if f["found"] != true || p["id"] != idOf(t, tolu) || f["add_token"] == nil || p["photo"] == nil {
		t.Fatalf("by link: %v", f)
	}
	ada.must(201, "POST", "/v1/me/contacts", map[string]any{"person_id": p["id"], "add_token": f["add_token"], "from": "link"})
	if st, hdr, _ := ada.getRaw(t, p["photo"].(map[string]any)["url"].(string)); st != 200 || hdr.Get("Content-Type") != "image/jpeg" {
		t.Fatalf("the photo by id: %d", st)
	}
	if own := tolu.must(200, "GET", "/v1/people/links/"+code, nil); own["self"] != true || own["add_token"] != nil {
		t.Fatalf("one's own link: %v", own)
	}
	// The page.
	st, _, page := anon.getRaw(t, "/p/"+code)
	if st != 200 || !bytes.Contains(page, []byte("Tolu")) || !bytes.Contains(page, []byte("/login?next=/p/"+code)) {
		t.Fatalf("page: %d %s", st, page)
	}
	// Replaced: the old link no longer works.
	l2 := tolu.must(200, "POST", "/v1/me/link", nil)
	if l2["code"] == code {
		t.Fatalf("a new link: %v", l2)
	}
	if e := anon.must(410, "GET", "/v1/person-links/"+code, nil); e["gone"] != true {
		t.Fatalf("old card: %v", e)
	}
	if e := ada.must(410, "GET", "/v1/people/links/"+code, nil); e["gone"] != true {
		t.Fatalf("old by link: %v", e)
	}
	if st, _, page := anon.getRaw(t, "/p/"+code); st != 410 || !bytes.Contains(page, []byte("no longer works")) {
		t.Fatalf("old page: %d %s", st, page)
	}
	anon.must(404, "GET", "/v1/person-links/2222222222", nil)
	if st, _, _ := anon.getRaw(t, "/p/2222222222"); st != 404 {
		t.Fatalf("no one's page: %d", st)
	}
	if got := anon.must(200, "GET", "/v1/person-links/"+l2["code"].(string), nil); got["name"] != "Tolu" {
		t.Fatalf("the new card: %v", got)
	}
}

// Contact matching (§5.3): the key, then only the people who let
// themselves be found from contacts; an old key 409; nothing kept.
func TestContactMatching(t *testing.T) {
	w := newFindWorld(t)
	tolu := w.personNamed(t, "+2348032000031", "Tolu")
	ada := w.personNamed(t, "+2348032000032", "Ada")
	bola := w.personNamed(t, "+2348032000033", "Bola")
	bola.must(200, "PUT", "/v1/me/privacy/people", map[string]any{"find_from_contacts": false})
	// Matching follows "find me by my number" too: Ada lets everyone find
	// her (the default, My contacts, would hide her from Tolu).
	ada.must(200, "PUT", "/v1/me/privacy/people", map[string]any{"find_by_number": "everyone"})
	k := tolu.must(200, "GET", "/v1/me/contacts/key", nil)
	key, err := base64.StdEncoding.DecodeString(k["key"].(string))
	// A new account sends 1000 hashes a day, not 5000.
	if err != nil || len(key) != 32 || k["max_batch"] != float64(500) || k["max_per_day"] != float64(1000) || !strings.Contains(k["words"].(string), "not from Taskiem itself") {
		t.Fatalf("key: %v", k)
	}
	h := func(n string) string { return hex.EncodeToString(people.ContactHash(key, n)) }
	got := tolu.must(200, "POST", "/v1/me/contacts/match", map[string]any{"key_id": k["key_id"],
		"hashes": []string{h("+2348032000031"), h("+2348032000032"), h("+2348032000033"), h("+2348032999999")}})
	ms := got["matches"].([]any)
	if len(ms) != 1 {
		t.Fatalf("matches: %v", ms)
	}
	m := ms[0].(map[string]any)
	if m["hash"] != h("+2348032000032") || m["person"].(map[string]any)["id"] != idOf(t, ada) || m["add_token"] == "" {
		t.Fatalf("Ada: %v", m)
	}
	tolu.must(201, "POST", "/v1/me/contacts", map[string]any{"person_id": idOf(t, ada), "add_token": m["add_token"], "from": "contacts"})
	if e := tolu.must(409, "POST", "/v1/me/contacts/match", map[string]any{"key_id": "k1", "hashes": []string{h("+2348032000032")}}); e["key_changed"] != true {
		t.Fatalf("an old key: %v", e)
	}
	tolu.must(400, "POST", "/v1/me/contacts/match", map[string]any{"key_id": k["key_id"], "hashes": []string{"abc"}})
	var n int
	if err := w.env.DB.Admin.QueryRow(context.Background(), `SELECT count(*) FROM people_find_counts WHERE kind = 'contacts' AND n = 4`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("only the count kept: %d %v", n, err)
	}
}

// Presence: the heartbeat and the goodbye; shown per the settings, hidden
// both ways.
func TestPeoplePresence(t *testing.T) {
	w := newFindWorld(t)
	tolu := w.personNamed(t, "+2348032000041", "Tolu")
	ada := w.personNamed(t, "+2348032000042", "Ada")
	ada.must(204, "POST", "/v1/me/presence", map[string]any{"online": true})
	if v := tolu.must(200, "GET", "/v1/people/"+idOf(t, ada), nil); v["last_seen_at"] != nil || v["online"] != nil {
		t.Fatalf("a stranger: %v", v)
	}
	ada.must(200, "PUT", "/v1/me/privacy/people", map[string]any{"last_seen": "everyone"})
	if v := tolu.must(200, "GET", "/v1/people/"+idOf(t, ada), nil); v["last_seen_at"] != nil {
		t.Fatalf("Tolu hides his own from Ada (contacts, and he has not added her): %v", v)
	}
	tolu.must(200, "PUT", "/v1/me/privacy/people", map[string]any{"last_seen": "everyone"})
	v := tolu.must(200, "GET", "/v1/people/"+idOf(t, ada), nil)
	if v["last_seen_at"] == nil || v["online"] != true {
		t.Fatalf("both everyone: %v", v)
	}
	ada.must(204, "POST", "/v1/me/presence", map[string]any{"online": false})
	if v = tolu.must(200, "GET", "/v1/people/"+idOf(t, ada), nil); v["online"] != false || v["last_seen_at"] == nil {
		t.Fatalf("away: %v", v)
	}
}

// Deleting an account (§14): the person leaves the others' contacts; their
// settings, link and presence go; their link names no one; they are a
// deleted account by id.
func TestDeleteAccountForgetsPeople(t *testing.T) {
	w := newFindWorld(t)
	const number = "+2348032000051"
	tolu := w.personNamed(t, number, "Tolu")
	ada := w.personNamed(t, "+2348032000052", "Ada")
	toluID := idOf(t, tolu)
	tolu.must(200, "PUT", "/v1/me/privacy/people", map[string]any{"find_by_number": "everyone"})
	w.add(t, ada, number)
	ada.must(200, "PUT", "/v1/me/privacy/people", map[string]any{"find_by_number": "everyone"})
	w.add(t, tolu, "+2348032000052")
	code := tolu.must(200, "GET", "/v1/me/link", nil)["code"].(string)
	tolu.must(204, "POST", "/v1/me/presence", map[string]any{"online": true})

	tolu.must(202, "POST", "/v1/me/delete/start", map[string]any{"via": "sms"})
	tolu.must(200, "POST", "/v1/me/delete", map[string]any{"confirm": true, "code": w.sms.code(t, number)})

	if list := ada.must(200, "GET", "/v1/me/contacts", nil)["contacts"].([]any); len(list) != 0 {
		t.Fatalf("Ada's contacts: %v", list)
	}
	v := ada.must(200, "GET", "/v1/people/"+toluID, nil)
	if v["deleted"] != true || v["name"] != "Deleted account" || v["last_seen_at"] != nil || v["number"] != nil {
		t.Fatalf("a deleted account: %v", v)
	}
	anon := &client{t: t, base: w.base}
	anon.must(404, "GET", "/v1/person-links/"+code, nil)
	if f := ada.must(200, "POST", "/v1/people/lookup", map[string]any{"phone": number}); f["found"] != false {
		t.Fatalf("the number: %v", f)
	}
	var n int
	if err := w.env.DB.Admin.QueryRow(context.Background(), `SELECT
		(SELECT count(*) FROM person_privacy WHERE person_id = $1) + (SELECT count(*) FROM person_links WHERE person_id = $1) +
		(SELECT count(*) FROM person_presence WHERE person_id = $1) + (SELECT count(*) FROM person_contacts WHERE person_id = $1 OR contact_id = $1) +
		(SELECT count(*) FROM people_find_counts WHERE person_id = $1)`, toluID).Scan(&n); err != nil || n != 0 {
		t.Fatalf("left behind: %d %v", n, err)
	}
	var left float64
	if err := w.env.DB.Admin.QueryRow(context.Background(), `SELECT (detail->>'contacts_left')::float FROM audit_log WHERE action = 'account.delete' AND actor_id = $1`, toluID).Scan(&left); err != nil || left != 1 {
		t.Fatalf("audited: %v %v", left, err)
	}
}
