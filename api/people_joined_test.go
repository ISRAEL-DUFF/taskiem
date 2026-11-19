package api_test

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/israel-duff/taskiem/engine/people"
	"github.com/israel-duff/taskiem/engine/peoplechats"
)

// The two streams of stage 5a joined ("As built: stage 5a, the two streams
// joined" in docs/spec/people-chatting.md): the people chats ask finding
// people who may message whom, screen strangers' messages with its checks,
// open a chat from a person's link, follow both people's read ticks, show
// the other person's card (photo, last seen) by their settings, add a
// contact on accepting, and show a deleted account as "Deleted account"
// everywhere.

func newJoinedWorld(t *testing.T) *peopleWorld {
	t.Helper()
	w := newPeopleWorld(t)
	w.srv.People = peoplechats.JoinPeople(&people.People{Pool: w.env.DB.App, Key: bytes.Repeat([]byte{7}, 32), Base: w.base})
	return w
}

// linkOf is c's person's link code.
func linkOf(t *testing.T, c *client) string {
	t.Helper()
	return c.must(200, "GET", "/v1/me/link", nil)["code"].(string)
}

// addByLink adds the owner of code to c's contacts, through their link.
func addByLink(t *testing.T, c *client, code string) {
	t.Helper()
	f := c.must(200, "GET", "/v1/people/links/"+code, nil)
	p := f["person"].(map[string]any)
	c.must(201, "POST", "/v1/me/contacts", map[string]any{"person_id": p["id"], "add_token": f["add_token"], "from": "link"})
}

func setPrivacy(t *testing.T, c *client, set map[string]any) {
	t.Helper()
	c.must(200, "PUT", "/v1/me/privacy/people", set)
}

func tickOf(t *testing.T, c *client, chat, body string) string {
	t.Helper()
	for _, m := range messagesOf(t, c, chat) {
		if m["body"] == body {
			s, _ := m["status"].(string)
			return s
		}
	}
	t.Fatalf("no message %q", body)
	return ""
}

func TestPeopleJoined(t *testing.T) {
	ctx := context.Background()
	w := newJoinedWorld(t)
	ada, adaID := w.person(t, "+2348031120301", "Ada Obi")
	w.older(t, adaID)
	bola, bolaID := w.person(t, "+2348031120302", "Bola Ade")

	// The checks on a stranger's message: one that asks for a code is held
	// (refused, nothing reaches Bola); one with an account number is
	// delivered, marked.
	adaChat := chatWith(t, ada, bolaID)
	e := ada.must(403, "POST", "/v1/me/people/"+adaChat+"/messages", map[string]any{"text": "Please send me the code you received"})
	if !strings.Contains(e["error"].(string), "not sent") {
		t.Fatalf("held: %v", e)
	}
	if len(listOf(t, bola, "/v1/me/people/requests", "requests")) != 0 {
		t.Fatal("a held message arrived")
	}
	send(t, ada, adaChat, "Pay into my account 0123456789 at GTBank")
	bolaChat := requestOf(t, bola)
	if m := messagesOf(t, bola, bolaChat); len(m) != 1 || m[0]["warn"] != true {
		t.Fatalf("a warned message: %v", m)
	}
	var verdicts int
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT count(*) FROM people_screen_verdicts WHERE from_person = $1`, adaID).Scan(&verdicts); err != nil || verdicts != 2 {
		t.Fatalf("verdicts kept: %d %v", verdicts, err)
	}

	// Accepting adds Ada to Bola's contacts (from a chat), and read ticks
	// go between them once accepted.
	acc := bola.must(200, "POST", "/v1/me/people/"+bolaChat+"/accept", nil)
	if acc["contact"] != true {
		t.Fatalf("accepting adds a contact: %v", acc)
	}
	cs := listOf(t, bola, "/v1/me/contacts", "contacts")
	if len(cs) != 1 || cs[0]["id"] != adaID.String() || cs[0]["source"] != "chat" {
		t.Fatalf("Bola's contacts: %v", cs)
	}
	send(t, ada, adaChat, "Hello Bola")
	bola.must(204, "POST", "/v1/me/people/"+bolaChat+"/read", nil)
	if s := tickOf(t, ada, adaChat, "Hello Bola"); s != "read" {
		t.Fatalf("read: %s", s)
	}
	// Read ticks off on either side hide them, both ways.
	setPrivacy(t, bola, map[string]any{"read_ticks": false})
	if s := tickOf(t, ada, adaChat, "Hello Bola"); s != "delivered" {
		t.Fatalf("Bola's ticks off: %s", s)
	}
	send(t, ada, adaChat, "Are you there?")
	bola.must(204, "POST", "/v1/me/people/"+bolaChat+"/read", nil)
	setPrivacy(t, bola, map[string]any{"read_ticks": true})
	if s := tickOf(t, ada, adaChat, "Are you there?"); s != "delivered" {
		t.Fatalf("read while off is not told later: %s", s)
	}
	setPrivacy(t, ada, map[string]any{"read_ticks": false})
	if s := tickOf(t, ada, adaChat, "Hello Bola"); s != "delivered" {
		t.Fatalf("Ada's own ticks off: %s", s)
	}
	setPrivacy(t, ada, map[string]any{"read_ticks": true})
	if s := tickOf(t, ada, adaChat, "Hello Bola"); s != "read" {
		t.Fatalf("both on again: %s", s)
	}

	// Last seen, online, photo and number by the settings: Ada used her
	// chats (here now). Bola added Ada; until Ada adds Bola, her last seen
	// (contacts) is hidden from Bola, and her photo (contacts) too.
	if st, _ := ada.putFile(t, "/v1/me/photo", "me.png", pngPhoto(t, 90)); st != 200 {
		t.Fatalf("photo: %d", st)
	}
	p := bola.must(200, "GET", "/v1/me/people/"+bolaChat, nil)["person"].(map[string]any)
	if p["last_seen_at"] != nil || p["online"] != nil || p["photo"] != nil || p["in_contacts"] != true {
		t.Fatalf("hidden until Ada adds Bola: %v", p)
	}
	addByLink(t, ada, linkOf(t, bola))
	c := bola.must(200, "GET", "/v1/me/people/"+bolaChat, nil)
	p = c["person"].(map[string]any)
	if p["last_seen_at"] == nil || p["online"] != true || p["photo"] == nil || c["phone"] != nil {
		t.Fatalf("seen as a contact, the number hidden: %v", c)
	}
	setPrivacy(t, ada, map[string]any{"last_seen": "nobody", "show_number": true})
	c = bola.must(200, "GET", "/v1/me/people/"+bolaChat, nil)
	if p = c["person"].(map[string]any); p["last_seen_at"] != nil || p["online"] != nil || c["phone"] != "+2348031120301" {
		t.Fatalf("last seen nobody, the number shown: %v", c)
	}
	if l := listOf(t, bola, "/v1/me/conversations", "chats"); len(l) != 1 || l[0]["person"] == nil {
		t.Fatalf("the one list carries the card: %v", l)
	}

	// "Who can message me: Nobody" refuses a new chat.
	chidi, chidiID := w.person(t, "+2348031120303", "Chidi Eze")
	setPrivacy(t, chidi, map[string]any{"message": "nobody"})
	ada.must(403, "POST", "/v1/me/people", map[string]any{"person_id": chidiID})

	// "My contacts": only people Dayo added write, and not as a request.
	dayo, dayoID := w.person(t, "+2348031120304", "Dayo")
	setPrivacy(t, dayo, map[string]any{"message": "contacts"})
	chidi.must(403, "POST", "/v1/me/people", map[string]any{"person_id": dayoID})
	addByLink(t, dayo, linkOf(t, ada))
	toDayo := chatWith(t, ada, dayoID)
	send(t, ada, toDayo, "Hi Dayo")
	if l := listOf(t, dayo, "/v1/me/people", "chats"); len(l) != 1 || l[0]["request"] != false {
		t.Fatalf("a contact writes straight to the chats: %v", l)
	}
	if len(listOf(t, dayo, "/v1/me/people/requests", "requests")) != 0 {
		t.Fatal("a contact's message is not a request")
	}

	// A block is never told: Eke blocked Femi before any chat; Femi starts
	// one and writes, and sees one tick.
	femi, femiID := w.person(t, "+2348031120305", "Femi")
	eke, ekeID := w.person(t, "+2348031120306", "Eke")
	if _, err := w.env.DB.Admin.Exec(ctx, `INSERT INTO people_blocks (person_id, blocked_id) VALUES ($1, $2)`, ekeID, femiID); err != nil {
		t.Fatal(err)
	}
	toEke := chatWith(t, femi, ekeID)
	if m := send(t, femi, toEke, "Hello Eke"); m["status"] != "sent" {
		t.Fatalf("one tick: %v", m)
	}
	if c := femi.must(200, "GET", "/v1/me/people/"+toEke, nil); c["blocked"] != false {
		t.Fatalf("Femi is told: %v", c)
	}
	if len(listOf(t, eke, "/v1/me/people/requests", "requests")) != 0 || len(listOf(t, eke, "/v1/me/people", "chats")) != 0 {
		t.Fatal("a blocked person's message arrived")
	}

	// A chat started from a person's link; a replaced link answers 410, an
	// unknown one 404; writing back to a request adds the contact too.
	gbenga, gbengaID := w.person(t, "+2348031120307", "Gbenga")
	old := linkOf(t, gbenga)
	byLink := femi.must(201, "POST", "/v1/me/people", map[string]any{"link_code": old})
	if byLink["person_id"] != gbengaID.String() {
		t.Fatalf("from a link: %v", byLink)
	}
	gbenga.must(200, "POST", "/v1/me/link", nil)
	femi.must(410, "POST", "/v1/me/people", map[string]any{"link_code": old})
	femi.must(404, "POST", "/v1/me/people", map[string]any{"link_code": "nosuchcode"})
	send(t, femi, byLink["id"].(string), "Hi from your link")
	send(t, gbenga, requestOf(t, gbenga), "Hi Femi")
	if cs := listOf(t, gbenga, "/v1/me/contacts", "contacts"); len(cs) != 1 || cs[0]["id"] != femiID.String() {
		t.Fatalf("writing back adds the contact: %v", cs)
	}

	// Deleting Ada's account: "Deleted account" in Bola's chat, the one
	// list, by id; she leaves Bola's contacts.
	ada.must(202, "POST", "/v1/me/delete/start", map[string]any{"via": "sms"})
	ada.must(200, "POST", "/v1/me/delete", map[string]any{"confirm": true, "code": w.sms.code(t, "+2348031120301")})
	c = bola.must(200, "GET", "/v1/me/people/"+bolaChat, nil)
	if c["name"] != "Deleted account" || c["gone"] != true || c["person"] != nil || c["phone"] != nil {
		t.Fatalf("Bola's chat: %v", c)
	}
	if l := listOf(t, bola, "/v1/me/conversations", "chats"); len(l) != 1 || l[0]["name"] != "Deleted account" || l[0]["person"] != nil {
		t.Fatalf("the one list: %v", l)
	}
	if v := bola.must(200, "GET", "/v1/people/"+adaID.String(), nil); v["name"] != "Deleted account" || v["deleted"] != true {
		t.Fatalf("by id: %v", v)
	}
	if cs := listOf(t, bola, "/v1/me/contacts", "contacts"); len(cs) != 0 {
		t.Fatalf("Bola's contacts: %v", cs)
	}
	if l := listOf(t, dayo, "/v1/me/people", "chats"); len(l) != 1 || l[0]["name"] != "Deleted account" {
		t.Fatalf("Dayo's chat: %v", l)
	}
}
