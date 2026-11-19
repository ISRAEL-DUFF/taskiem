package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// The stage 5a review's fixes ("Review fixes" in docs/spec/
// people-chatting.md): two people writing at once, a small pool, a block
// nobody can detect, nothing crossing a block, the checks on cards and
// places, Mark as paid on a request, read ticks everywhere, the limits
// counted one after the other, a friend who stays a friend, a request
// whose recipient changed their mind, paged lists, a business's photo in
// the one list, empty chats, and a suspended account.

// doWithin is a request that gives up after d (for requests sent at
// once: a stuck one fails the test instead of hanging it).
func doWithin(c *client, d time.Duration, method, path string, body any) (int, map[string]any, error) {
	var rd io.Reader
	if body != nil {
		raw, _ := json.Marshal(body)
		rd = bytes.NewReader(raw)
	}
	req, err := http.NewRequest(method, c.base+path, rd)
	if err != nil {
		return 0, nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	resp, err := (&http.Client{Timeout: d}).Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	var out map[string]any
	raw, _ := io.ReadAll(resp.Body)
	_ = json.Unmarshal(raw, &out)
	return resp.StatusCode, out, nil
}

// many is a person whose account is made in the database, then signed in
// by phone (the API makes at most 10 accounts an hour from one address).
func (w *peopleWorld) many(t *testing.T, number, name string) (*client, uuid.UUID) {
	t.Helper()
	id := uuid.Must(uuid.NewV7())
	if _, err := w.env.DB.Admin.Exec(context.Background(), `WITH u AS (INSERT INTO users (id, email, name) VALUES ($1, $4, $3) RETURNING id)
		INSERT INTO user_phones (user_id, number) SELECT id, $2 FROM u`, id, number, name, id.String()+"@phone.taskiem.invalid"); err != nil {
		t.Fatal(err)
	}
	// Each signs in from an address of its own (127.0.x.y), as people on
	// their own phones do: one address asks for 20 codes an hour at most.
	n := manyFrom.Add(1)
	local := &net.TCPAddr{IP: net.IPv4(127, 0, byte(10+n/200), byte(1+n%200))}
	hc := &http.Client{Timeout: time.Minute, Transport: &http.Transport{DialContext: (&net.Dialer{LocalAddr: local}).DialContext}}
	post := func(path string, body any, want int) map[string]any {
		raw, _ := json.Marshal(body)
		resp, err := hc.Post(w.base+path, "application/json", bytes.NewReader(raw))
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = resp.Body.Close() }()
		var out map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&out)
		if resp.StatusCode != want {
			t.Fatalf("POST %s: %d %v", path, resp.StatusCode, out)
		}
		return out
	}
	post("/v1/auth/phone/start", map[string]any{"phone": number, "via": "sms"}, 202)
	out := post("/v1/auth/phone/verify", map[string]any{"phone": number, "code": w.sms.code(t, number), "bearer": true}, 200)
	tok, _ := out["token"].(string)
	c := &client{t: t, base: w.base, token: tok}
	if got := personID(t, c); got != id {
		t.Fatalf("signed in as %s: %v", got, out)
	}
	return c, id
}

// manyFrom numbers the addresses many signs in from.
var manyFrom atomic.Int32

// friends makes a's and b's chat accepted: a writes, b accepts (adding a
// to b's contacts) and writes back. Answers a's chat and b's.
func friends(t *testing.T, a *client, b *client, bID uuid.UUID) (string, string) {
	t.Helper()
	aChat := chatWith(t, a, bID)
	send(t, a, aChat, "Hi, it's me")
	bChat := requestOf(t, b)
	b.must(200, "POST", "/v1/me/people/"+bChat+"/accept", nil)
	send(t, b, bChat, "Hello you")
	return aChat, bChat
}

// messageOf is c's message with this body in chat.
func messageOf(t *testing.T, c *client, chat, body string) map[string]any {
	t.Helper()
	for _, m := range messagesOf(t, c, chat) {
		if m["body"] == body {
			return m
		}
	}
	t.Fatalf("no message %q", body)
	return nil
}

// cardIn is c's last card message in chat.
func cardIn(t *testing.T, c *client, chat string) map[string]any {
	t.Helper()
	ms := messagesOf(t, c, chat)
	for i := len(ms) - 1; i >= 0; i-- {
		if ms[i]["kind"] == "card" {
			return ms[i]
		}
	}
	t.Fatal("no card")
	return nil
}

func splitCard(what string) map[string]any {
	return map[string]any{"card": map[string]any{"type": "split", "amount_kobo": 300000, "what": what,
		"pay_to": map[string]any{"bank": "GTBank", "account_number": "0123456789", "account_name": "Ada Obi"}}}
}

// Two people writing to each other at once (15 each): no deadlock, every
// message on both copies, each person's in the order they sent them.
func TestPeopleSendAtOnce(t *testing.T) {
	w := newJoinedWorld(t)
	ada, adaID := w.person(t, "+2348031130001", "Ada Obi")
	bola, bolaID := w.person(t, "+2348031130002", "Bola Ade")
	w.older(t, adaID)
	w.older(t, bolaID)
	adaChat, bolaChat := friends(t, ada, bola, bolaID)
	var wg sync.WaitGroup
	var mu sync.Mutex
	var failed []string
	run := func(c *client, chat, who string) {
		defer wg.Done()
		for i := range 15 {
			code, out, err := doWithin(c, time.Minute, "POST", "/v1/me/people/"+chat+"/messages", map[string]any{"text": fmt.Sprintf("%s %02d", who, i)})
			if err != nil || code != 201 {
				mu.Lock()
				failed = append(failed, fmt.Sprintf("%s %d: %d %v %v", who, i, code, out, err))
				mu.Unlock()
			}
		}
	}
	wg.Add(2)
	go run(ada, adaChat, "Ada")
	go run(bola, bolaChat, "Bola")
	wg.Wait()
	if len(failed) > 0 {
		t.Fatalf("%d of 30 failed: %v", len(failed), failed)
	}
	for _, side := range []struct {
		c    *client
		chat string
		me   string
	}{{ada, adaChat, "Ada"}, {bola, bolaChat, "Bola"}} {
		next := map[string]int{"Ada": 0, "Bola": 0}
		for _, m := range messagesOf(t, side.c, side.chat) {
			body := m["body"].(string)
			who, n, ok := strings.Cut(body, " ")
			if !ok || (who != "Ada" && who != "Bola") {
				continue
			}
			if want := fmt.Sprintf("%02d", next[who]); n != want {
				t.Fatalf("%s's copy: %s's %s came where %s was due", side.me, who, n, want)
			}
			next[who]++
			if who == side.me && m["status"] != "delivered" {
				t.Fatalf("%s's copy: %v", side.me, m)
			}
		}
		if next["Ada"] != 15 || next["Bola"] != 15 {
			t.Fatalf("%s's copy holds %v", side.me, next)
		}
	}
}

// Twenty first messages at once on a pool of four connections: each send
// holds one connection (finding people is asked in its transaction), so
// none waits for a second and all go through.
func TestPeopleSendOnASmallPool(t *testing.T) {
	ctx := context.Background()
	w := newJoinedWorld(t)
	bola, bolaID := w.person(t, "+2348031130101", "Bola Ade")
	var senders []*client
	var chats []string
	for i := range 20 {
		c, id := w.many(t, fmt.Sprintf("+23480311302%02d", i), fmt.Sprintf("Sender %d", i))
		w.older(t, id)
		senders = append(senders, c)
		chats = append(chats, chatWith(t, c, bolaID))
	}
	cfg := w.env.DB.App.Config()
	cfg.MaxConns, cfg.MinConns = 4, 0
	small, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer small.Close()
	orig := w.srv.Store.Pool
	w.srv.Store.Pool, w.srv.People.Pool = small, small
	defer func() { w.srv.Store.Pool, w.srv.People.Pool = orig, orig }()
	var wg sync.WaitGroup
	var mu sync.Mutex
	var failed []string
	for i := range senders {
		wg.Add(1)
		go func() {
			defer wg.Done()
			// Half warn (the checks keep a verdict in the same transaction).
			text := "Hello Bola, it's me"
			if i%2 == 1 {
				text = "Pay into my account 0123456789 at GTBank"
			}
			code, out, err := doWithin(senders[i], 90*time.Second, "POST", "/v1/me/people/"+chats[i]+"/messages", map[string]any{"text": text})
			if err != nil || code != 201 {
				mu.Lock()
				failed = append(failed, fmt.Sprintf("%d: %d %v %v", i, code, out, err))
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if len(failed) > 0 {
		t.Fatalf("failed: %v", failed)
	}
	if r := listOf(t, bola, "/v1/me/people/requests", "requests"); len(r) != 20 {
		t.Fatalf("requests: %d", len(r))
	}
}

// A block is never told: a blocked sender is answered exactly as one who
// is not (403 by "who can message me", a request or not, the request
// limits' 429), and only their message stays on their side (one tick).
func TestPeopleBlockNotTold(t *testing.T) {
	ctx := context.Background()
	w := newJoinedWorld(t)
	block := func(blocker, blocked uuid.UUID) {
		t.Helper()
		if _, err := w.env.DB.Admin.Exec(ctx, `INSERT INTO people_blocks (person_id, blocked_id) VALUES ($1, $2)`, blocker, blocked); err != nil {
			t.Fatal(err)
		}
	}
	free, freeID := w.person(t, "+2348031130301", "Free")
	held, heldID := w.person(t, "+2348031130302", "Held")
	w.older(t, freeID)
	w.older(t, heldID)
	n := 10
	// recipient makes a recipient for each sender (the held one's blocks
	// them) with the same settings, set by prep.
	recipient := func(prep func(c *client, sender *client)) (uuid.UUID, uuid.UUID) {
		n++
		a, aID := w.many(t, fmt.Sprintf("+23480311303%02d", n), "R")
		n++
		b, bID := w.many(t, fmt.Sprintf("+23480311303%02d", n), "R")
		prep(a, free)
		prep(b, held)
		block(bID, heldID)
		return aID, bID
	}
	type answer struct {
		Start, Send       int
		Waiting, Request  any
		Status            any
		StartErr, SendErr any
	}
	try := func(sender *client, to uuid.UUID, text string) answer {
		code, out := sender.do("POST", "/v1/me/people", map[string]any{"person_id": to})
		a := answer{Start: code, StartErr: out["error"]}
		if code != 201 {
			return a
		}
		chat := out["id"].(string)
		code, m := sender.do("POST", "/v1/me/people/"+chat+"/messages", map[string]any{"text": text})
		a.Send, a.SendErr, a.Status = code, m["error"], m["status"]
		c := sender.must(200, "GET", "/v1/me/people/"+chat, nil)
		a.Waiting, a.Request = c["waiting"], c["request"]
		return a
	}
	same := func(name string, want answer, to [2]uuid.UUID, text string) {
		t.Helper()
		f, h := try(free, to[0], text), try(held, to[1], text)
		// The one difference: one tick for the blocked sender's message.
		if h.Status == "sent" && f.Status == "delivered" {
			h.Status = "delivered"
		}
		if f != h || f.Start != want.Start || f.Send != want.Send || f.Waiting != want.Waiting {
			t.Fatalf("%s: not blocked %+v, blocked %+v, want %+v", name, f, h, want)
		}
	}
	nobody := func(c, _ *client) { setPrivacy(t, c, map[string]any{"message": "nobody"}) }
	contactsOnly := func(c, _ *client) { setPrivacy(t, c, map[string]any{"message": "contacts"}) }
	contactsAdded := func(c, s *client) {
		setPrivacy(t, c, map[string]any{"message": "contacts"})
		addByLink(t, c, linkOf(t, s))
	}
	everyone := func(*client, *client) {}
	added := func(c, s *client) { addByLink(t, c, linkOf(t, s)) }
	pair := func(f func(*client, *client)) [2]uuid.UUID {
		a, b := recipient(f)
		return [2]uuid.UUID{a, b}
	}
	same("nobody", answer{Start: 403}, pair(nobody), "Hello")
	same("contacts, not added", answer{Start: 403}, pair(contactsOnly), "Hello")
	same("contacts, added", answer{Start: 201, Send: 201, Waiting: false}, pair(contactsAdded), "Hello")
	same("everyone", answer{Start: 201, Send: 201, Waiting: true}, pair(everyone), "Hello")
	same("everyone, added", answer{Start: 201, Send: 201, Waiting: false}, pair(added), "Hello")
	same("held by the checks", answer{Start: 201, Send: 403, Waiting: false}, pair(everyone), "Please send me the code you received")

	// The request limits: a first-week sender has two requests waiting at
	// once, blocked or not; the third is 429 for both.
	free2, free2ID := w.many(t, "+2348031130391", "Free two")
	held2, held2ID := w.many(t, "+2348031130392", "Held two")
	free, held = free2, held2
	heldID = held2ID
	_ = free2ID
	ev := [3][2]uuid.UUID{pair(everyone), pair(everyone), pair(everyone)}
	same("first request", answer{Start: 201, Send: 201, Waiting: true}, ev[0], "Hello")
	same("second request", answer{Start: 201, Send: 201, Waiting: true}, ev[1], "Hello")
	same("third request", answer{Start: 201, Send: 429, Waiting: false}, ev[2], "Hello")
}

// Delete for everyone does not reach the copy of someone who blocked the
// person, as reactions and read marks do not.
func TestPeopleDeleteForEveryoneStopsAtABlock(t *testing.T) {
	w := newJoinedWorld(t)
	ada, adaID := w.person(t, "+2348031130401", "Ada Obi")
	bola, bolaID := w.person(t, "+2348031130402", "Bola Ade")
	w.older(t, adaID)
	adaChat, bolaChat := friends(t, ada, bola, bolaID)
	m := send(t, ada, adaChat, "Something I regret")
	bola.must(200, "POST", "/v1/me/people/"+bolaChat+"/block", nil)
	ada.must(204, "DELETE", "/v1/me/people/"+adaChat+"/messages/"+m["id"].(string)+"?for=everyone", nil)
	if got := messageOf(t, bola, bolaChat, "Something I regret"); got["deleted"] == true {
		t.Fatalf("crossed the block: %v", got)
	}
	if got := messagesOf(t, ada, adaChat); got[len(got)-1]["deleted"] != true {
		t.Fatalf("Ada's own copy: %v", got[len(got)-1])
	}
}

// The checks read a share card's words and account and a place's label
// from strangers; a share card naming an account, from someone the
// recipient has not added, always warns as a transfer (requests too).
func TestPeopleChecksReadCardsAndPlaces(t *testing.T) {
	ctx := context.Background()
	w := newJoinedWorld(t)
	ada, adaID := w.person(t, "+2348031130501", "Ada Obi")
	bola, bolaID := w.person(t, "+2348031130502", "Bola Ade")
	chidi, chidiID := w.person(t, "+2348031130503", "Chidi Eze")
	w.older(t, adaID)
	toBola := chatWith(t, ada, bolaID)
	ada.must(403, "POST", "/v1/me/people/"+toBola+"/messages", map[string]any{"location": map[string]any{"lat": 6.5, "lng": 3.4, "label": "Send me the code you received"}})
	if len(listOf(t, bola, "/v1/me/people/requests", "requests")) != 0 {
		t.Fatal("a held place arrived")
	}
	// A share card in a request: allowed, always warned.
	ada.must(201, "POST", "/v1/me/people/"+toBola+"/messages", splitCard("The cake"))
	bolaChat := requestOf(t, bola)
	if c := cardIn(t, bola, bolaChat); c["warn"] != true || c["warn_transfer"] != true {
		t.Fatalf("a share card from a stranger: %v", c)
	}
	// One asking a fee for a prize is held.
	toChidi := chatWith(t, ada, chidiID)
	ada.must(403, "POST", "/v1/me/people/"+toChidi+"/messages", splitCard("Processing fee to claim your prize"))
	_ = chidi
	var verdicts int
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT count(*) FROM people_screen_verdicts WHERE from_person = $1`, adaID).Scan(&verdicts); err != nil || verdicts != 3 {
		t.Fatalf("verdicts: %d %v", verdicts, err)
	}
	// Accepted (Bola adds Ada) and written back: no checks, no warning.
	bola.must(200, "POST", "/v1/me/people/"+bolaChat+"/accept", nil)
	send(t, bola, bolaChat, "Thanks")
	ada.must(201, "POST", "/v1/me/people/"+toBola+"/messages", splitCard("The drinks"))
	if c := cardIn(t, bola, bolaChat); c["warn"] == true || c["warn_transfer"] == true {
		t.Fatalf("a share card from a contact: %v", c)
	}
	// No longer in Bola's contacts: warned again, though they chat.
	bola.must(204, "DELETE", "/v1/me/contacts/"+adaID.String(), nil)
	ada.must(201, "POST", "/v1/me/people/"+toBola+"/messages", splitCard("The taxi"))
	if c := cardIn(t, bola, bolaChat); c["warn"] != true || c["warn_transfer"] != true {
		t.Fatalf("a share card from someone not added: %v", c)
	}
}

// Mark as paid on a request tells its sender nothing, and a mark made
// while it waits does not reach the recipient's copy either.
func TestPeoplePaidOnARequest(t *testing.T) {
	w := newJoinedWorld(t)
	ada, adaID := w.person(t, "+2348031130601", "Ada Obi")
	bola, bolaID := w.person(t, "+2348031130602", "Bola Ade")
	w.older(t, adaID)
	toBola := chatWith(t, ada, bolaID)
	card := ada.must(201, "POST", "/v1/me/people/"+toBola+"/messages", splitCard("The cake"))
	bolaChat := requestOf(t, bola)
	got := bola.must(200, "POST", "/v1/me/people/"+bolaChat+"/messages/"+card["id"].(string)+"/paid", map[string]any{"paid": true})
	if got["card"].(map[string]any)["paid"] != true {
		t.Fatalf("Bola's own copy: %v", got)
	}
	if c := cardIn(t, ada, toBola)["card"].(map[string]any); c["paid"] == true {
		t.Fatalf("the request's sender was told: %v", c)
	}
	ada.must(200, "POST", "/v1/me/people/"+toBola+"/messages/"+card["id"].(string)+"/paid", map[string]any{"paid": false})
	if c := cardIn(t, bola, bolaChat)["card"].(map[string]any); c["paid"] != true || c["paid_by_me"] != true {
		t.Fatalf("Bola's copy changed by the sender: %v", c)
	}
}

// Read ticks off hide a read everywhere the sender's message is answered:
// a repeated client_id and Mark as paid too. Reading a request tells its
// sender nothing.
func TestPeopleReadTicksEverywhere(t *testing.T) {
	w := newJoinedWorld(t)
	ada, adaID := w.person(t, "+2348031130701", "Ada Obi")
	bola, bolaID := w.person(t, "+2348031130702", "Bola Ade")
	chidi, chidiID := w.person(t, "+2348031130703", "Chidi Eze")
	w.older(t, adaID)

	// A request read: nothing told.
	toChidi := chatWith(t, ada, chidiID)
	send(t, ada, toChidi, "Hello Chidi")
	chidi.must(204, "POST", "/v1/me/people/"+requestOf(t, chidi)+"/read", nil)
	if m := messageOf(t, ada, toChidi, "Hello Chidi"); m["status"] != "delivered" || m["read_at"] != nil {
		t.Fatalf("a request read: %v", m)
	}

	adaChat, bolaChat := friends(t, ada, bola, bolaID)
	m := ada.must(201, "POST", "/v1/me/people/"+adaChat+"/messages", map[string]any{"text": "Did you see?", "client_id": "c-1"})
	card := ada.must(201, "POST", "/v1/me/people/"+adaChat+"/messages", splitCard("The cake"))
	bola.must(204, "POST", "/v1/me/people/"+bolaChat+"/read", nil)
	if s := tickOf(t, ada, adaChat, "Did you see?"); s != "read" {
		t.Fatalf("read: %s", s)
	}
	setPrivacy(t, bola, map[string]any{"read_ticks": false})
	again := ada.must(200, "POST", "/v1/me/people/"+adaChat+"/messages", map[string]any{"text": "Did you see?", "client_id": "c-1"})
	if again["id"] != m["id"] || again["status"] != "delivered" || again["read_at"] != nil {
		t.Fatalf("a repeat: %v", again)
	}
	paid := ada.must(200, "POST", "/v1/me/people/"+adaChat+"/messages/"+card["id"].(string)+"/paid", map[string]any{"paid": true})
	if paid["status"] != "delivered" || paid["read_at"] != nil {
		t.Fatalf("marked paid: %v", paid)
	}
}

// The limits are counted one after the other: first messages sent at once
// by a first-week account stay within its requests (two waiting), and
// warned messages at once within RepeatHold (three, then held).
func TestPeopleLimitsAtOnce(t *testing.T) {
	ctx := context.Background()
	w := newJoinedWorld(t)
	at := func(sender *client, chats []string, text string) map[int]int {
		var wg sync.WaitGroup
		var mu sync.Mutex
		codes := map[int]int{}
		for _, chat := range chats {
			wg.Add(1)
			go func() {
				defer wg.Done()
				code, _, err := doWithin(sender, time.Minute, "POST", "/v1/me/people/"+chat+"/messages", map[string]any{"text": text})
				if err != nil {
					code = -1
				}
				mu.Lock()
				codes[code]++
				mu.Unlock()
			}()
		}
		wg.Wait()
		return codes
	}
	newbie, _ := w.person(t, "+2348031130801", "New")
	var chats []string
	for i := range 5 {
		_, id := w.many(t, fmt.Sprintf("+23480311308%02d", i+10), "R")
		chats = append(chats, chatWith(t, newbie, id))
	}
	if codes := at(newbie, chats, "Hello there"); codes[201] != 2 || codes[429] != 3 {
		t.Fatalf("a first week's requests at once: %v", codes)
	}
	old, oldID := w.person(t, "+2348031130802", "Old")
	w.older(t, oldID)
	chats = nil
	for i := range 5 {
		_, id := w.many(t, fmt.Sprintf("+23480311308%02d", i+20), "R")
		chats = append(chats, chatWith(t, old, id))
	}
	if codes := at(old, chats, "Pay into my account 0123456789 at GTBank"); codes[201] != 3 || codes[403] != 2 {
		t.Fatalf("warned messages at once: %v", codes)
	}
	var warn, hold int
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT count(*) FILTER (WHERE verdict = 'warn'), count(*) FILTER (WHERE verdict = 'hold') FROM people_screen_verdicts WHERE from_person = $1`,
		oldID).Scan(&warn, &hold); err != nil || warn != 3 || hold != 2 {
		t.Fatalf("verdicts: %d warn, %d hold, %v", warn, hold, err)
	}
}

// A friend stays a friend: after "delete for me", the other has still
// written to the person, so the checks do not read their messages again.
func TestPeopleFriendAfterDeleteForMe(t *testing.T) {
	ctx := context.Background()
	w := newJoinedWorld(t)
	ada, adaID := w.person(t, "+2348031130901", "Ada Obi")
	bola, bolaID := w.person(t, "+2348031130902", "Bola Ade")
	w.older(t, adaID)
	adaChat, bolaChat := friends(t, ada, bola, bolaID)
	ada.must(204, "DELETE", "/v1/me/people/"+adaChat, nil)
	send(t, ada, adaChat, "Pay into my account 0123456789 at GTBank")
	if m := messageOf(t, bola, bolaChat, "Pay into my account 0123456789 at GTBank"); m["warn"] == true {
		t.Fatalf("a friend screened as a stranger: %v", m)
	}
	var verdicts int
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT count(*) FROM people_screen_verdicts WHERE from_person = $1`, adaID).Scan(&verdicts); err != nil || verdicts != 0 {
		t.Fatalf("verdicts: %d %v", verdicts, err)
	}
}

// A request waiting while its recipient changes "who can message me":
// refused now, the sender's next message stays on their side with one
// tick (as a blocked one; nothing else tells them); added as a contact,
// it is still a request until accepted.
func TestPeopleRequestAfterPrivacyChange(t *testing.T) {
	w := newJoinedWorld(t)
	ada, adaID := w.person(t, "+2348031131001", "Ada Obi")
	bola, bolaID := w.person(t, "+2348031131002", "Bola Ade")
	w.older(t, adaID)
	toBola := chatWith(t, ada, bolaID)
	send(t, ada, toBola, "Hello Bola")
	bolaChat := requestOf(t, bola)
	setPrivacy(t, bola, map[string]any{"message": "nobody"})
	if m := send(t, ada, toBola, "Are you there?"); m["status"] != "sent" {
		t.Fatalf("one tick: %v", m)
	}
	if ms := messagesOf(t, bola, bolaChat); len(ms) != 1 {
		t.Fatalf("delivered though refused: %v", ms)
	}
	setPrivacy(t, bola, map[string]any{"message": "contacts"})
	addByLink(t, bola, linkOf(t, ada))
	if m := send(t, ada, toBola, "Hello again"); m["status"] != "delivered" {
		t.Fatalf("a contact now: %v", m)
	}
	if requestOf(t, bola) != bolaChat {
		t.Fatal("no longer a request")
	}
}

// The lists page: 200 at a time with more and next (passed as before),
// newest first with ties in id order, unread for every chat; nothing
// twice, nothing left out.
func TestPeopleListsPage(t *testing.T) {
	ctx := context.Background()
	w := newJoinedWorld(t)
	ada, adaID := w.person(t, "+2348031131101", "Ada Obi")
	// 205 chats, three to a second (ties), and one unread message in the
	// oldest.
	if _, err := w.env.DB.Admin.Exec(ctx, `WITH u AS (
		  INSERT INTO users (id, email, name) SELECT gen_random_uuid(), 'peer' || i || '-' || $2 || '@x.test', 'Peer ' || i FROM generate_series(1, 205) i RETURNING id, name)
		INSERT INTO people_chats (id, person_id, peer_id, conversation_id, started_by_me, accepted_at, last_message_at)
		SELECT gen_random_uuid(), $1, u.id, taskiem_people_pair($1, u.id), true, now(), date_trunc('second', now()) - (substring(u.name from 6)::int / 3) * interval '1 second' FROM u`, adaID, adaID.String()); err != nil {
		t.Fatal(err)
	}
	if _, err := w.env.DB.Admin.Exec(ctx, `INSERT INTO people_messages (person_id, id, chat_id, conversation_id, mine, kind)
		SELECT c.person_id, gen_random_uuid(), c.id, c.conversation_id, false, 'text' FROM people_chats c WHERE c.person_id = $1
		ORDER BY c.last_message_at, c.id LIMIT 1`, adaID); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/v1/me/people", "/v1/me/conversations"} {
		first := ada.must(200, "GET", path, nil)
		if len(first["chats"].([]any)) != 200 || first["more"] != true || first["unread"] != float64(1) || first["next"] == nil {
			t.Fatalf("%s, the first page: %d %v %v %v", path, len(first["chats"].([]any)), first["more"], first["unread"], first["next"])
		}
		second := ada.must(200, "GET", path+"?before="+first["next"].(string), nil)
		if len(second["chats"].([]any)) != 5 || second["more"] != false || second["next"] != nil || second["unread"] != float64(1) {
			t.Fatalf("%s, the second page: %v", path, second)
		}
		seen := map[string]bool{}
		var last time.Time
		for _, c := range append(first["chats"].([]any), second["chats"].([]any)...) {
			m := c.(map[string]any)
			at, _ := time.Parse(time.RFC3339Nano, m["last_at"].(string))
			if seen[m["id"].(string)] || (!last.IsZero() && at.After(last)) {
				t.Fatalf("%s: twice or out of order: %v", path, m)
			}
			seen[m["id"].(string)], last = true, at
		}
		if len(seen) != 205 {
			t.Fatalf("%s: %d chats", path, len(seen))
		}
		ada.must(400, "GET", path+"?before=nonsense", nil)
	}
}

// A chat opened is not listed until its first message; a business chat in
// the one list carries the business's photo.
func TestPeopleListsEmptyChatsAndPhotos(t *testing.T) {
	ctx := context.Background()
	w := newJoinedWorld(t)
	ada, _ := w.person(t, "+2348031131201", "Ada Obi")
	_, bolaID := w.person(t, "+2348031131202", "Bola Ade")
	chat := chatWith(t, ada, bolaID)
	if l := listOf(t, ada, "/v1/me/people", "chats"); len(l) != 0 {
		t.Fatalf("an empty chat listed: %v", l)
	}
	if l := listOf(t, ada, "/v1/me/conversations", "chats"); len(l) != 0 {
		t.Fatalf("an empty chat in the one list: %v", l)
	}
	ada.must(200, "GET", "/v1/me/people/"+chat, nil)
	send(t, ada, chat, "Hello Bola")
	if l := listOf(t, ada, "/v1/me/people", "chats"); len(l) != 1 || l[0]["id"] != chat {
		t.Fatalf("listed from the first message: %v", l)
	}

	biz := ada.must(201, "POST", "/v1/me/chats", map[string]any{"business_id": w.biz, "from": "shop"})["id"].(string)
	ada.must(201, "POST", "/v1/me/chats/"+biz+"/messages", map[string]any{"text": "Do you have small chops?"})
	media := uuid.New()
	if _, err := w.env.DB.Admin.Exec(ctx, `INSERT INTO storefront_media (id, tenant_id, hash, width, height, type, urls, paths, moderation, created_by)
		VALUES ($1, $2, 'h', 10, 10, 'image/png', '{"320": "https://x.test/logo-320.png"}', '{}', 'approved', 'test')`, media, w.biz); err != nil {
		t.Fatal(err)
	}
	if _, err := w.env.DB.Admin.Exec(ctx, `UPDATE storefront_snapshots snap SET model = jsonb_set(snap.model, '{card,logo}', to_jsonb($2::text))
		FROM storefronts s WHERE s.id = snap.storefront_id AND snap.version = s.published_version AND s.tenant_id = $1`, w.biz, media.String()); err != nil {
		t.Fatal(err)
	}
	for _, it := range listOf(t, ada, "/v1/me/conversations", "chats") {
		if it["kind"] != "business" {
			continue
		}
		p, _ := it["photo"].(map[string]any)
		if p == nil || p["urls"].(map[string]any)["320"] != "https://x.test/logo-320.png" {
			t.Fatalf("the business's photo: %v", it)
		}
		return
	}
	t.Fatal("no business chat listed")
}

// A suspended account is not a deleted one: its chat keeps its name,
// marked suspended, and a message answers "can't get messages right now".
func TestPeopleSuspendedAccount(t *testing.T) {
	ctx := context.Background()
	w := newJoinedWorld(t)
	ada, adaID := w.person(t, "+2348031131301", "Ada Obi")
	bola, bolaID := w.person(t, "+2348031131302", "Bola Ade")
	w.older(t, adaID)
	adaChat, _ := friends(t, ada, bola, bolaID)
	if _, err := w.env.DB.Admin.Exec(ctx, `UPDATE users SET status = 'disabled' WHERE id = $1`, bolaID); err != nil {
		t.Fatal(err)
	}
	e := ada.must(409, "POST", "/v1/me/people/"+adaChat+"/messages", map[string]any{"text": "Are you there?"})
	if e["error"] != "This account can't get messages right now." {
		t.Fatalf("suspended: %v", e)
	}
	c := ada.must(200, "GET", "/v1/me/people/"+adaChat, nil)
	if c["name"] != "Bola Ade" || c["gone"] != false || c["suspended"] != true || c["person"] != nil {
		t.Fatalf("the chat: %v", c)
	}
	if l := listOf(t, ada, "/v1/me/conversations", "chats"); len(l) != 1 || l[0]["suspended"] != true || l[0]["name"] != "Bola Ade" {
		t.Fatalf("the one list: %v", l)
	}
	if _, err := w.env.DB.Admin.Exec(ctx, `UPDATE users SET status = 'active' WHERE id = $1`, bolaID); err != nil {
		t.Fatal(err)
	}
	send(t, ada, adaChat, "Welcome back")
}
