package api_test

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/israel-duff/taskiem/engine/push"
)

// Stage 5a's missing chat features (people-chatting "As built: stage 5a
// gaps, chat features (server)"): You's live connection, typing,
// presence on the connection, search, starred across chats, the trust line
// on the one Chats list, the push preview and places.

// liveStream opens c's live connection (GET /v1/me/live); query is added
// to its address. It answers the events and the connection's id (from its
// ready event).
func liveStream(t *testing.T, c *client, query string) (<-chan streamEvent, string, func()) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	req, _ := http.NewRequestWithContext(ctx, "GET", c.base+"/v1/me/live"+query, nil)
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Accept", "text/event-stream")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	if resp.StatusCode != 200 || resp.Header.Get("Content-Type") != "text/event-stream" {
		_ = resp.Body.Close()
		cancel()
		t.Fatalf("the live connection: %d %s", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
	events := make(chan streamEvent, 500)
	go readStream(resp.Body, events)
	ready := next(events, "ready", 5*time.Second, nil)
	if ready == nil {
		cancel()
		t.Fatal("no ready event")
	}
	conn, _ := ready.Data["connection"].(string)
	return events, conn, cancel
}

// drain takes what the stream said so far, waiting d.
func drain(events <-chan streamEvent, d time.Duration) []streamEvent {
	var out []streamEvent
	timeout := time.After(d)
	for {
		select {
		case ev, open := <-events:
			if !open {
				return out
			}
			out = append(out, ev)
		case <-timeout:
			return out
		}
	}
}

// await takes events for up to d until each condition has met one,
// answering the first event each met (nil for one that met none).
func await(events <-chan streamEvent, d time.Duration, conds ...func(streamEvent) bool) []*streamEvent {
	out := make([]*streamEvent, len(conds))
	left := len(conds)
	timeout := time.After(d)
	for left > 0 {
		select {
		case ev, open := <-events:
			if !open {
				return out
			}
			for i, ok := range conds {
				if out[i] == nil && ok(ev) {
					e := ev
					out[i] = &e
					left--
				}
			}
		case <-timeout:
			return out
		}
	}
	return out
}

func eventMessage(ev streamEvent) map[string]any {
	m, _ := ev.Data["message"].(map[string]any)
	return m
}

// You's live connection: a message reaches the other's connection, with
// its chat, and a new request says it is one; the read mark and a reaction
// reach the sender's; a chat's change reaches the person's own other
// devices, as does a message deleted for them; and nothing of anyone
// else's copy is ever on it: not the other's star, not a third person's
// chats.
func TestPeopleLive(t *testing.T) {
	w := newPeopleWorld(t)
	ada, adaID := w.person(t, "+2348031130001", "Ada Obi")
	bola, bolaID := w.person(t, "+2348031130002", "Bola Ade")
	chidi, _ := w.person(t, "+2348031130003", "Chidi Eze")
	adaChat, bolaChat := friends(t, ada, bola, bolaID)

	adaEv, _, closeAda := liveStream(t, ada, "")
	defer closeAda()
	bolaEv, _, closeBola := liveStream(t, bola, "")
	defer closeBola()
	bolaEv2, _, closeBola2 := liveStream(t, bola, "")
	defer closeBola2()
	chidiEv, _, closeChidi := liveStream(t, chidi, "")
	defer closeChidi()
	drain(adaEv, 1500*time.Millisecond) // what came before the test's own
	drain(bolaEv, 0)
	drain(bolaEv2, 0)

	sent := send(t, ada, adaChat, "Are you coming tonight?")
	id := sent["id"].(string)
	got := await(bolaEv, 5*time.Second,
		func(e streamEvent) bool { return e.Event == "message" && eventMessage(e)["id"] == id },
		func(e streamEvent) bool {
			c, _ := e.Data["chat"].(map[string]any)
			return e.Event == "chat" && e.Data["id"] == bolaChat && c != nil && c["last"] == "Are you coming tonight?"
		})
	ev := got[0]
	if ev == nil || ev.Data["chat_id"] != bolaChat || ev.Data["request"] != false || eventMessage(*ev)["body"] != "Are you coming tonight?" || eventMessage(*ev)["mine"] != false || ev.ID == "" {
		t.Fatalf("Bola's message event: %+v", ev)
	}
	if ev := got[1]; ev == nil || ev.Data["chat"].(map[string]any)["unread"] != float64(2) || ev.Data["hidden"] != false {
		t.Fatalf("Bola's chat event: %+v", ev)
	}
	// Ada's own connection has her copy (delivered).
	if ev := next(adaEv, "message", 5*time.Second, func(e streamEvent) bool { return eventMessage(e)["id"] == id }); ev == nil ||
		ev.Data["chat_id"] != adaChat || eventMessage(*ev)["mine"] != true {
		t.Fatalf("Ada's message event: %+v", ev)
	}
	// Bola reads it: Ada's tick turns read.
	bola.must(204, "POST", "/v1/me/people/"+bolaChat+"/read", nil)
	if ev := next(adaEv, "message", 5*time.Second, func(e streamEvent) bool { return eventMessage(e)["id"] == id && eventMessage(e)["status"] == "read" }); ev == nil {
		t.Fatal("the read mark did not reach Ada")
	}
	// Bola reacts: Ada sees their reaction.
	bola.must(200, "PUT", "/v1/me/people/"+bolaChat+"/messages/"+id, map[string]any{"reaction": "👍"})
	if ev := next(adaEv, "message", 5*time.Second, func(e streamEvent) bool { return eventMessage(e)["their_reaction"] == "👍" }); ev == nil {
		t.Fatal("the reaction did not reach Ada")
	}
	// Bola stars it: that is Bola's alone, on Bola's other device only.
	drain(adaEv, 1500*time.Millisecond)
	bola.must(200, "PUT", "/v1/me/people/"+bolaChat+"/messages/"+id, map[string]any{"starred": true})
	if ev := next(bolaEv2, "message", 5*time.Second, func(e streamEvent) bool { return eventMessage(e)["starred"] == true }); ev == nil {
		t.Fatal("the star did not reach Bola's other device")
	}
	for _, e := range drain(adaEv, 2500*time.Millisecond) {
		if e.Event == "message" && eventMessage(e)["starred"] == true {
			t.Fatalf("Bola's star on Ada's connection: %+v", e)
		}
	}
	// Muted on one device: the chat event reaches the other.
	bola.must(200, "PUT", "/v1/me/people/"+bolaChat, map[string]any{"muted": true})
	if ev := next(bolaEv2, "chat", 5*time.Second, func(e streamEvent) bool {
		c, _ := e.Data["chat"].(map[string]any)
		return c != nil && c["muted"] == true
	}); ev == nil {
		t.Fatal("muting did not reach Bola's other device")
	}
	// Deleted for Bola: gone on Bola's other device; Ada's copy stays.
	bola.must(204, "DELETE", "/v1/me/people/"+bolaChat+"/messages/"+id+"?for=me", nil)
	if ev := next(bolaEv2, "gone", 5*time.Second, func(e streamEvent) bool { return e.Data["id"] == id }); ev == nil || ev.Data["chat_id"] != bolaChat {
		t.Fatalf("gone: %+v", ev)
	}
	// Deleted for everyone by Ada: Bola's connection is told.
	second := send(t, ada, adaChat, "Wrong chat, sorry")
	ada.must(204, "DELETE", "/v1/me/people/"+adaChat+"/messages/"+second["id"].(string)+"?for=everyone", nil)
	if ev := next(bolaEv, "message", 5*time.Second, func(e streamEvent) bool {
		return eventMessage(e)["id"] == second["id"] && eventMessage(e)["deleted"] == true
	}); ev == nil {
		t.Fatal("deleted for everyone did not reach Bola")
	}

	// A stranger's first message: a request on Bola's connection.
	chidiChat := chatWith(t, chidi, bolaID)
	first := send(t, chidi, chidiChat, "Hello, I got your number from the church list")
	got = await(bolaEv, 5*time.Second,
		func(e streamEvent) bool { return e.Event == "message" && eventMessage(e)["id"] == first["id"] },
		func(e streamEvent) bool {
			c, _ := e.Data["chat"].(map[string]any)
			return e.Event == "chat" && c != nil && c["request"] == true
		})
	ev = got[0]
	if ev == nil || ev.Data["request"] != true {
		t.Fatalf("the request's message: %+v", ev)
	}
	reqChat := ev.Data["chat_id"].(string)
	if ev := got[1]; ev == nil || ev.Data["id"] != reqChat || ev.Data["chat"].(map[string]any)["name"] != "Chidi Eze" {
		t.Fatalf("the request's chat: %+v", ev)
	}
	// Bola deletes the request on one device: the other is told it is gone.
	bola.must(204, "POST", "/v1/me/people/"+reqChat+"/decline", nil)
	if ev := next(bolaEv2, "gone", 5*time.Second, func(e streamEvent) bool { return e.Data["chat_id"] == reqChat && e.Data["id"] == nil }); ev == nil {
		t.Fatal("the deleted request did not reach Bola's other device")
	}

	// Nothing of Ada's or Bola's chats is on Chidi's connection, and only
	// Ada's own copy on hers.
	for _, e := range drain(chidiEv, time.Second) {
		if e.Event == "message" || e.Event == "chat" {
			if e.Data["chat_id"] == adaChat || e.Data["chat_id"] == bolaChat || e.Data["id"] == adaChat || e.Data["id"] == bolaChat {
				t.Fatalf("someone else's chat on Chidi's connection: %+v", e)
			}
		}
	}
	for _, e := range drain(adaEv, time.Second) {
		if e.Data["chat_id"] == bolaChat || e.Data["id"] == bolaChat || e.Data["chat_id"] == reqChat {
			t.Fatalf("Bola's copy on Ada's connection: %+v", e)
		}
	}
	_ = adaID

	// A client that reconnects with Last-Event-ID is sent what it missed.
	closeBola2()
	missed := send(t, ada, adaChat, "While you were away")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", bola.base+"/v1/me/live", nil)
	req.Header.Set("Authorization", "Bearer "+bola.token)
	req.Header.Set("Last-Event-ID", fmt.Sprint(time.Now().Add(-5*time.Second).UnixMicro()))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	again := make(chan streamEvent, 200)
	go readStream(resp.Body, again)
	if ev := next(again, "message", 5*time.Second, func(e streamEvent) bool { return eventMessage(e)["id"] == missed["id"] }); ev == nil {
		t.Fatal("not sent what was missed")
	}

	// An API key is refused; a signed-out request too.
	if code, _, _ := rawGet(t, w.base+"/v1/me/live", ""); code != http.StatusUnauthorized {
		t.Fatalf("signed out: %d", code)
	}
}

// rawGet is a GET with a bearer token (none when empty), its status.
func rawGet(t *testing.T, u, token string) (int, string, error) {
	t.Helper()
	req, _ := http.NewRequest("GET", u, nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, "", err
	}
	defer func() { _ = resp.Body.Close() }()
	var b bytes.Buffer
	_, _ = b.ReadFrom(resp.Body)
	return resp.StatusCode, b.String(), nil
}

// typingRows is how many typing marks the person has now.
func (w *peopleWorld) typingRows(t *testing.T, person uuid.UUID) int {
	t.Helper()
	var n int
	if err := w.env.DB.Admin.QueryRow(context.Background(), `SELECT count(*) FROM people_typing WHERE person_id = $1 AND until > now()`, person).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// Typing (§7, §13): passed to the other's live connection within an
// accepted chat, gone after a few seconds or when stopped; never while
// either copy is a request; never across a block, either way; answered
// 204 whatever became of it, so the blocked person cannot tell.
func TestPeopleTyping(t *testing.T) {
	w := newPeopleWorld(t)
	ada, adaID := w.person(t, "+2348031130011", "Ada Obi")
	bola, bolaID := w.person(t, "+2348031130012", "Bola Ade")
	chidi, chidiID := w.person(t, "+2348031130013", "Chidi Eze")
	adaChat, bolaChat := friends(t, ada, bola, bolaID)

	bolaEv, _, closeBola := liveStream(t, bola, "")
	defer closeBola()
	adaEv, _, closeAda := liveStream(t, ada, "")
	defer closeAda()

	ada.must(204, "POST", "/v1/me/people/"+adaChat+"/typing", nil)
	if ev := next(bolaEv, "typing", 5*time.Second, func(e streamEvent) bool {
		l, _ := e.Data["typing"].([]any)
		return len(l) == 1 && l[0] == bolaChat
	}); ev == nil {
		t.Fatal("Bola did not see Ada typing")
	}
	// It goes after a few seconds by itself.
	if ev := next(bolaEv, "typing", 10*time.Second, func(e streamEvent) bool { return len(e.Data["typing"].([]any)) == 0 }); ev == nil {
		t.Fatal("typing did not end")
	}
	// Said again at once, it is not passed on again (at most every 3 s).
	ada.must(204, "POST", "/v1/me/people/"+adaChat+"/typing", map[string]any{"typing": true})
	var until1, until2 time.Time
	_ = w.env.DB.Admin.QueryRow(context.Background(), `SELECT until FROM people_typing WHERE person_id = $1`, bolaID).Scan(&until1)
	ada.must(204, "POST", "/v1/me/people/"+adaChat+"/typing", nil)
	_ = w.env.DB.Admin.QueryRow(context.Background(), `SELECT until FROM people_typing WHERE person_id = $1`, bolaID).Scan(&until2)
	if until1.IsZero() || !until1.Equal(until2) {
		t.Fatalf("typing said twice at once: %v %v", until1, until2)
	}
	// Stopped: gone at once; and a message sent ends it too.
	ada.must(204, "POST", "/v1/me/people/"+adaChat+"/typing", map[string]any{"typing": false})
	if n := w.typingRows(t, bolaID); n != 0 {
		t.Fatalf("typing after stopping: %d", n)
	}
	time.Sleep(3100 * time.Millisecond)
	ada.must(204, "POST", "/v1/me/people/"+adaChat+"/typing", nil)
	send(t, ada, adaChat, "On my way")
	if n := w.typingRows(t, bolaID); n != 0 {
		t.Fatalf("typing after the message: %d", n)
	}
	// Bola typing reaches Ada.
	bola.must(204, "POST", "/v1/me/people/"+bolaChat+"/typing", nil)
	if ev := next(adaEv, "typing", 5*time.Second, func(e streamEvent) bool { return len(e.Data["typing"].([]any)) == 1 }); ev == nil {
		t.Fatal("Ada did not see Bola typing")
	}

	// A request: neither the stranger's typing nor the recipient's goes.
	chidiChat := chatWith(t, chidi, bolaID)
	send(t, chidi, chidiChat, "Hello, is this Bola?")
	reqChat := requestOf(t, bola)
	chidi.must(204, "POST", "/v1/me/people/"+chidiChat+"/typing", nil)
	bola.must(204, "POST", "/v1/me/people/"+reqChat+"/typing", nil)
	time.Sleep(200 * time.Millisecond)
	var n int
	if err := w.env.DB.Admin.QueryRow(context.Background(), `SELECT count(*) FROM people_typing t JOIN people_chats c ON c.person_id = t.person_id AND c.id = t.chat_id
		WHERE c.peer_id IN ($1, $2) AND t.person_id IN ($1, $2)`, chidiID, bolaID).Scan(&n); err != nil || n != 0 {
		t.Fatalf("typing across a request: %d %v", n, err)
	}
	// Not someone else's chat: 404.
	chidi.must(404, "POST", "/v1/me/people/"+adaChat+"/typing", nil)

	// A block, either way: nothing goes, and the answer is the same 204.
	time.Sleep(3100 * time.Millisecond)
	bola.must(204, "POST", "/v1/me/people/"+bolaChat+"/typing", map[string]any{"typing": false})
	bola.must(200, "POST", "/v1/me/people/"+bolaChat+"/block", nil)
	ada.must(204, "POST", "/v1/me/people/"+adaChat+"/typing", nil) // Ada is blocked: told nothing
	bola.must(204, "POST", "/v1/me/people/"+bolaChat+"/typing", nil)
	time.Sleep(200 * time.Millisecond)
	if a, b := w.typingRows(t, adaID), w.typingRows(t, bolaID); a != 0 || b != 0 {
		t.Fatalf("typing across a block: Ada %d, Bola %d", a, b)
	}
	for _, e := range drain(bolaEv, 2500*time.Millisecond) {
		if e.Event == "typing" && len(e.Data["typing"].([]any)) > 0 {
			t.Fatalf("Bola saw typing from someone blocked: %+v", e)
		}
	}
	// And a typing mark made before the block is not shown after it.
	bola.must(200, "DELETE", "/v1/me/people/"+bolaChat+"/block", nil)
	time.Sleep(3100 * time.Millisecond)
	ada.must(204, "POST", "/v1/me/people/"+adaChat+"/typing", nil)
	if n := w.typingRows(t, bolaID); n != 1 {
		t.Fatalf("typing after unblocking: %d", n)
	}
	bola.must(200, "POST", "/v1/me/people/"+bolaChat+"/block", nil)
	drain(bolaEv, 1500*time.Millisecond)
	for _, e := range drain(bolaEv, 2*time.Second) {
		if e.Event == "typing" && len(e.Data["typing"].([]any)) > 0 {
			t.Fatalf("typing shown across a new block: %+v", e)
		}
	}
}

// Presence on the live connection (§5.4, §13): a connection in front is
// online; one that says it is away, or closes, is away with last seen then;
// and the other sees it only as both settings let them.
func TestPeopleLivePresence(t *testing.T) {
	w := newPeopleWorld(t)
	ada, _ := w.person(t, "+2348031130021", "Ada Obi")
	bola, bolaID := w.person(t, "+2348031130022", "Bola Ade")
	adaChat, bolaChat := friends(t, ada, bola, bolaID)
	_ = adaChat
	for _, c := range []*client{ada, bola} {
		c.must(200, "PUT", "/v1/me/privacy/people", map[string]any{"last_seen": "everyone"})
	}
	bolaEv, _, closeBola := liveStream(t, bola, "")
	defer closeBola()
	adaPresence := func(want func(p map[string]any) bool) map[string]any {
		t.Helper()
		var got map[string]any
		ev := next(bolaEv, "presence", 12*time.Second, func(e streamEvent) bool {
			for _, p := range e.Data["people"].([]any) {
				p := p.(map[string]any)
				if p["chat_id"] == bolaChat && want(p) {
					got = p
					return true
				}
			}
			return false
		})
		if ev == nil {
			return nil
		}
		return got
	}
	adaEv, conn, closeAda := liveStream(t, ada, "")
	if p := adaPresence(func(p map[string]any) bool { return p["online"] == true }); p == nil {
		t.Fatal("Ada not online while connected")
	}
	_ = adaEv
	// Ada's app goes to the background, keeping the connection: away.
	ada.must(204, "POST", "/v1/me/presence", map[string]any{"online": false, "connection": conn})
	p := adaPresence(func(p map[string]any) bool { return p["online"] == false })
	if p == nil || p["last_seen_at"] == nil {
		t.Fatalf("Ada not away: %v", p)
	}
	// Using the chats while away does not make her online.
	ada.must(200, "GET", "/v1/me/people", nil)
	if c := bola.must(200, "GET", "/v1/me/people/"+bolaChat, nil)["person"].(map[string]any); c["online"] != false {
		t.Fatalf("online while away: %v", c)
	}
	// Back in front.
	ada.must(204, "POST", "/v1/me/presence", map[string]any{"online": true, "connection": conn})
	if p := adaPresence(func(p map[string]any) bool { return p["online"] == true }); p == nil {
		t.Fatal("Ada not back online")
	}
	// The connection closes: away, last seen then.
	before := time.Now().Add(-time.Second)
	closeAda()
	p = adaPresence(func(p map[string]any) bool { return p["online"] == false })
	if p == nil {
		t.Fatal("Ada still online after closing")
	}
	if at, err := time.Parse(time.RFC3339Nano, p["last_seen_at"].(string)); err != nil || at.Before(before) {
		t.Fatalf("last seen: %v %v", p["last_seen_at"], err)
	}
	// Last seen "Nobody": Bola sees neither.
	ada.must(200, "PUT", "/v1/me/privacy/people", map[string]any{"last_seen": "nobody"})
	if p := adaPresence(func(p map[string]any) bool { return p["online"] == nil && p["last_seen_at"] == nil }); p == nil {
		t.Fatal("Ada's presence shown with Nobody")
	}
	// The heartbeat still works for clients without the connection.
	ada.must(200, "PUT", "/v1/me/privacy/people", map[string]any{"last_seen": "everyone"})
	ada.must(204, "POST", "/v1/me/presence", map[string]any{"online": true})
	if p := adaPresence(func(p map[string]any) bool { return p["online"] == true }); p == nil {
		t.Fatal("the heartbeat did not make Ada online")
	}
}

// searchOf is c's search for q (more query parameters in extra).
func searchOf(t *testing.T, c *client, q, extra string) map[string]any {
	t.Helper()
	return c.must(200, "GET", "/v1/me/people/search?q="+url.QueryEscape(q)+extra, nil)
}

func resultBodies(r map[string]any) []string {
	var out []string
	for _, x := range r["results"].([]any) {
		out = append(out, x.(map[string]any)["message"].(map[string]any)["body"].(string))
	}
	return out
}

// Search across the person's own people chats (§6.2): their own copy's
// words, a share's what and a place's label, without accents or case, by
// the beginnings of words; never another person's copy; not what they
// deleted, for themselves or for everyone, or a chat they deleted; the
// older messages indexed on the first search; a page at a time; and the
// index holds no words, only tokens keyed per person.
func TestPeopleSearch(t *testing.T) {
	ctx := context.Background()
	w := newPeopleWorld(t)
	ada, adaID := w.person(t, "+2348031130031", "Ada Obi")
	bola, bolaID := w.person(t, "+2348031130032", "Bola Ade")
	chidi, chidiID := w.person(t, "+2348031130033", "Chidi Eze")
	adaChat, bolaChat := friends(t, ada, bola, bolaID)

	m1 := send(t, ada, adaChat, "Meet me at the Ọ̀ṣun market on Saturday")
	send(t, bola, bolaChat, "What is the market price of tomatoes?")
	place := ada.must(201, "POST", "/v1/me/people/"+adaChat+"/messages", map[string]any{"location": map[string]any{"lat": 6.43, "lng": 3.47, "label": "Lekki Phase 1 gate"}})
	ada.must(201, "POST", "/v1/me/people/"+adaChat+"/messages", map[string]any{"card": map[string]any{"type": "split", "amount_kobo": 300000, "what": "Jollof for the party",
		"pay_to": map[string]any{"bank": "GTBank", "account_number": "0123456789", "account_name": "Ada Obi"}}})

	r := searchOf(t, ada, "osun", "")
	res := r["results"].([]any)
	if len(res) != 1 || r["more"] != false {
		t.Fatalf("Ada's osun: %v", r)
	}
	first := res[0].(map[string]any)
	if first["chat_id"] != adaChat || first["message"].(map[string]any)["id"] != m1["id"] || !strings.Contains(first["snippet"].(string), "Ọ̀ṣun market") ||
		first["chat"].(map[string]any)["name"] != "Bola Ade" {
		t.Fatalf("Ada's result: %v", first)
	}
	// Case, accents and beginnings.
	for _, q := range []string{"OSUN", "Ọ̀ṣun", "satur", "mark sat"} {
		if b := resultBodies(searchOf(t, ada, q, "")); len(b) != 1 || b[0] != "Meet me at the Ọ̀ṣun market on Saturday" {
			t.Fatalf("%q: %v", q, b)
		}
	}
	// The middle of a word is not a beginning; words in no message find nothing.
	for _, q := range []string{"arket", "sunday", "osun sunday"} {
		if b := resultBodies(searchOf(t, ada, q, "")); len(b) != 0 {
			t.Fatalf("%q: %v", q, b)
		}
	}
	// Both of the chat's market messages in Ada's copy, the newest first.
	if b := resultBodies(searchOf(t, ada, "market", "")); len(b) != 2 || b[0] != "What is the market price of tomatoes?" {
		t.Fatalf("market: %v", b)
	}
	// A place's label and a share's what.
	if r := searchOf(t, ada, "lekki gate", "")["results"].([]any); len(r) != 1 || r[0].(map[string]any)["message"].(map[string]any)["id"] != place["id"] ||
		r[0].(map[string]any)["snippet"] != "Lekki Phase 1 gate" {
		t.Fatalf("a place: %v", r)
	}
	if r := searchOf(t, bola, "jollof", "")["results"].([]any); len(r) != 1 || r[0].(map[string]any)["message"].(map[string]any)["kind"] != "card" ||
		r[0].(map[string]any)["chat_id"] != bolaChat {
		t.Fatalf("a share's what in Bola's copy: %v", r)
	}
	// Bola finds their own copy; Chidi finds nothing of theirs.
	if r := searchOf(t, bola, "osun", "")["results"].([]any); len(r) != 1 || r[0].(map[string]any)["chat_id"] != bolaChat {
		t.Fatalf("Bola's osun: %v", r)
	}
	for _, q := range []string{"osun", "market", "jollof", "lekki"} {
		if r := searchOf(t, chidi, q, "")["results"].([]any); len(r) != 0 {
			t.Fatalf("Chidi found %q: %v", q, r)
		}
	}
	// One chat only.
	chidiChat := chatWith(t, chidi, adaID)
	send(t, chidi, chidiChat, "Is the market open on Sunday?")
	if b := resultBodies(searchOf(t, ada, "market", "")); len(b) != 3 {
		t.Fatalf("market in all of Ada's chats: %v", b)
	}
	if b := resultBodies(searchOf(t, ada, "market", "&chat="+adaChat)); len(b) != 2 {
		t.Fatalf("market in one chat: %v", b)
	}
	ada.must(400, "GET", "/v1/me/people/search?q=a", nil)
	ada.must(400, "GET", "/v1/me/people/search?q=", nil)

	// The index holds no words: tokens only, each person's their own.
	var rows, plain int
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT count(*), count(*) FILTER (WHERE position(convert_to('osun', 'UTF8') in token) > 0 OR position(convert_to('mar', 'UTF8') in token) > 0
		OR position(convert_to('Sat', 'UTF8') in token) > 0) FROM people_search`).Scan(&rows, &plain); err != nil || rows == 0 || plain != 0 {
		t.Fatalf("index: %d rows, %d with words, %v", rows, plain, err)
	}
	var shared int
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT count(*) FROM people_search a JOIN people_search b ON a.token = b.token AND a.message_id = b.message_id
		WHERE a.person_id = $1 AND b.person_id = $2`, adaID, bolaID).Scan(&shared); err != nil || shared != 0 {
		t.Fatalf("Ada's and Bola's tokens alike: %d %v", shared, err)
	}
	var cols int
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT count(*) FROM information_schema.columns WHERE table_name = 'people_search' AND data_type = 'text'`).Scan(&cols); err != nil || cols != 0 {
		t.Fatalf("text columns in the index: %d %v", cols, err)
	}

	// Deleted for me: not found by Bola, and its tokens gone; Ada still finds hers.
	bola.must(204, "DELETE", "/v1/me/people/"+bolaChat+"/messages/"+m1["id"].(string)+"?for=me", nil)
	if b := resultBodies(searchOf(t, bola, "osun", "")); len(b) != 0 {
		t.Fatalf("deleted for me, found: %v", b)
	}
	countFor := func(person uuid.UUID, msg string) int {
		var n int
		if err := w.env.DB.Admin.QueryRow(ctx, `SELECT count(*) FROM people_search WHERE person_id = $1 AND message_id = $2`, person, msg).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	if countFor(bolaID, m1["id"].(string)) != 0 || countFor(adaID, m1["id"].(string)) == 0 {
		t.Fatal("the index after delete for me")
	}
	// Deleted for everyone: neither finds it, and neither index has it.
	m2 := send(t, ada, adaChat, "The password is pineapple")
	if b := resultBodies(searchOf(t, bola, "pineapple", "")); len(b) != 1 {
		t.Fatalf("pineapple before: %v", b)
	}
	ada.must(204, "DELETE", "/v1/me/people/"+adaChat+"/messages/"+m2["id"].(string)+"?for=everyone", nil)
	for _, c := range []*client{ada, bola} {
		if b := resultBodies(searchOf(t, c, "pineapple", "")); len(b) != 0 {
			t.Fatalf("deleted for everyone, found: %v", b)
		}
	}
	if countFor(adaID, m2["id"].(string)) != 0 || countFor(bolaID, m2["id"].(string)) != 0 {
		t.Fatal("the index after delete for everyone")
	}
	// A chat deleted: nothing of it found.
	bola.must(204, "DELETE", "/v1/me/people/"+bolaChat, nil)
	if b := resultBodies(searchOf(t, bola, "market", "")); len(b) != 0 {
		t.Fatalf("a deleted chat, found: %v", b)
	}

	// Messages from before the index: indexed on the first search.
	if _, err := w.env.DB.Admin.Exec(ctx, `DELETE FROM people_search WHERE person_id = $1`, adaID); err != nil {
		t.Fatal(err)
	}
	if b := resultBodies(searchOf(t, ada, "osun", "")); len(b) != 0 {
		t.Fatalf("found with no index: %v", b) // ready already: nothing to backfill
	}
	if _, err := w.env.DB.Admin.Exec(ctx, `DELETE FROM people_search_ready WHERE person_id = $1`, adaID); err != nil {
		t.Fatal(err)
	}
	if b := resultBodies(searchOf(t, ada, "osun", "")); len(b) != 1 {
		t.Fatalf("not backfilled: %v", b)
	}

	// A page at a time.
	dayo, dayoID := w.person(t, "+2348031130034", "Dayo Bello")
	dChat, _ := friends(t, chidi, dayo, dayoID)
	for i := range 33 {
		if i%8 == 0 {
			w.unhurry(t, chidiID)
		}
		send(t, chidi, dChat, fmt.Sprintf("Plantain order number %d", i))
	}
	page := searchOf(t, chidi, "plantain", "")
	if len(page["results"].([]any)) != 30 || page["more"] != true || page["next"] == nil {
		t.Fatalf("the first page: %d %v", len(page["results"].([]any)), page["more"])
	}
	if b := resultBodies(page); b[0] != "Plantain order number 32" {
		t.Fatalf("newest first: %v", b[0])
	}
	page2 := searchOf(t, chidi, "plantain", "&before="+url.QueryEscape(page["next"].(string)))
	if b := resultBodies(page2); len(b) != 3 || page2["more"] != false || b[2] != "Plantain order number 0" {
		t.Fatalf("the second page: %v %v", b, page2["more"])
	}

	// Deleting the account takes the index.
	_ = chidiID
	bola.must(202, "POST", "/v1/me/delete/start", map[string]any{"via": "sms"})
	bola.must(200, "POST", "/v1/me/delete", map[string]any{"confirm": true, "code": w.sms.code(t, "+2348031130032")})
	var left int
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT (SELECT count(*) FROM people_search WHERE person_id = $1) + (SELECT count(*) FROM people_search_ready WHERE person_id = $1)`, bolaID).Scan(&left); err != nil || left != 0 {
		t.Fatalf("index after deleting the account: %d %v", left, err)
	}
}

// unhurry moves the person's own messages two minutes back, so a test
// may send more than a minute's worth.
func (w *peopleWorld) unhurry(t *testing.T, person uuid.UUID) {
	t.Helper()
	if _, err := w.env.DB.Admin.Exec(context.Background(), `UPDATE people_messages SET created_at = created_at - interval '2 minutes' WHERE person_id = $1 AND mine`, person); err != nil {
		t.Fatal(err)
	}
}

// Starred messages across the person's chats (§7), the most recently
// starred first, a page at a time, with each chat; not another's stars,
// not a deleted chat's.
func TestPeopleStarred(t *testing.T) {
	w := newPeopleWorld(t)
	ada, adaID := w.person(t, "+2348031130041", "Ada Obi")
	bola, bolaID := w.person(t, "+2348031130042", "Bola Ade")
	chidi, chidiID := w.person(t, "+2348031130043", "Chidi Eze")
	adaB, bolaChat := friends(t, ada, bola, bolaID)
	adaC, _ := friends(t, ada, chidi, chidiID)

	star := func(c *client, chat, id string) {
		c.must(200, "PUT", "/v1/me/people/"+chat+"/messages/"+id, map[string]any{"starred": true})
	}
	a1 := send(t, ada, adaB, "The address is 12 Allen Avenue")
	a2 := send(t, ada, adaC, "Bring the receipts")
	star(ada, adaC, a2["id"].(string))
	time.Sleep(10 * time.Millisecond)
	star(ada, adaB, a1["id"].(string))
	// Bola's own star is Bola's.
	star(bola, bolaChat, a1["id"].(string))

	l := ada.must(200, "GET", "/v1/me/people/starred", nil)
	items := l["starred"].([]any)
	if len(items) != 2 || l["more"] != false {
		t.Fatalf("Ada's starred: %v", l)
	}
	f := items[0].(map[string]any)
	if f["chat_id"] != adaB || f["message"].(map[string]any)["id"] != a1["id"] || f["chat"].(map[string]any)["name"] != "Bola Ade" || f["starred_at"] == nil ||
		items[1].(map[string]any)["chat_id"] != adaC {
		t.Fatalf("newest starred first, with the chat: %v", items)
	}
	if l := listOf(t, bola, "/v1/me/people/starred", "starred"); len(l) != 1 || l[0]["chat_id"] != bolaChat {
		t.Fatalf("Bola's starred: %v", l)
	}
	if l := listOf(t, chidi, "/v1/me/people/starred", "starred"); len(l) != 0 {
		t.Fatalf("Chidi's starred: %v", l)
	}
	// Unstarred: gone from the list.
	ada.must(200, "PUT", "/v1/me/people/"+adaC+"/messages/"+a2["id"].(string), map[string]any{"starred": false})
	if l := listOf(t, ada, "/v1/me/people/starred", "starred"); len(l) != 1 {
		t.Fatalf("after unstarring: %v", l)
	}
	// A page at a time.
	for i := range 51 {
		if i%8 == 0 {
			w.unhurry(t, adaID)
		}
		m := send(t, ada, adaC, fmt.Sprintf("note %d", i))
		star(ada, adaC, m["id"].(string))
	}
	page := ada.must(200, "GET", "/v1/me/people/starred", nil)
	if len(page["starred"].([]any)) != 50 || page["more"] != true {
		t.Fatalf("the first page: %d %v", len(page["starred"].([]any)), page["more"])
	}
	page2 := ada.must(200, "GET", "/v1/me/people/starred?before="+url.QueryEscape(page["next"].(string)), nil)
	if len(page2["starred"].([]any)) != 2 || page2["more"] != false {
		t.Fatalf("the second page: %v", page2)
	}
	// A chat deleted: its starred messages go from the list.
	ada.must(204, "DELETE", "/v1/me/people/"+adaC, nil)
	if l := listOf(t, ada, "/v1/me/people/starred", "starred"); len(l) != 1 || l[0]["chat_id"] != adaB {
		t.Fatalf("after deleting a chat: %v", l)
	}
}

// The one Chats list's business rows carry the business's trust line, the
// same as the business chat's head.
func TestPeopleListTrustLine(t *testing.T) {
	ctx := context.Background()
	w := newPeopleWorld(t)
	ada, _ := w.person(t, "+2348031130051", "Ada Obi")
	biz := ada.must(201, "POST", "/v1/me/chats", map[string]any{"business_id": w.biz, "from": "shop"})["id"].(string)
	ada.must(201, "POST", "/v1/me/chats/"+biz+"/messages", map[string]any{"text": "Do you deliver to Yaba?"})
	row := func() map[string]any {
		for _, it := range listOf(t, ada, "/v1/me/conversations", "chats") {
			if it["id"] == biz {
				return it
			}
		}
		t.Fatal("no business row")
		return nil
	}
	head := ada.must(200, "GET", "/v1/me/chats/"+biz, nil)["verified"].(map[string]any)
	got := row()["verified"].(map[string]any)
	if fmt.Sprint(got) != fmt.Sprint(head) {
		t.Fatalf("the list's trust %v, the head's %v", got, head)
	}
	// Older now: both say so alike.
	if _, err := w.env.DB.Admin.Exec(ctx, `UPDATE tenants SET created_at = now() - interval '100 days' WHERE id = $1`, w.biz); err != nil {
		t.Fatal(err)
	}
	head = ada.must(200, "GET", "/v1/me/chats/"+biz, nil)["verified"].(map[string]any)
	got = row()["verified"].(map[string]any)
	if got["months"] != float64(3) || fmt.Sprint(got) != fmt.Sprint(head) {
		t.Fatalf("after 100 days: the list's %v, the head's %v", got, head)
	}
	// A person's row has none.
	bola, bolaID := w.person(t, "+2348031130052", "Bola Ade")
	friends(t, ada, bola, bolaID)
	for _, it := range listOf(t, ada, "/v1/me/conversations", "chats") {
		if it["kind"] == "person" && it["verified"] != nil {
			t.Fatalf("a person with a trust line: %v", it)
		}
	}
}

// The push preview is each device's own and set as it registers, for a
// person's message and a request alike (§6.2).
func TestPeoplePushPreviewOnRegister(t *testing.T) {
	w := newPeopleWorld(t)
	w.srv.Push = pushSender(t)
	w.srv.Push.Validate = func(string) error { return nil }
	svc := newPushService(t)
	ada, _ := w.person(t, "+2348031130061", "Ada Obi")
	bola, bolaID := w.person(t, "+2348031130062", "Bola Ade")
	quiet, loud := newPushBrowser(t), newPushBrowser(t)
	out := bola.must(200, "PUT", "/v1/me/push", map[string]any{"endpoint": svc.srv.URL + "/quiet", "keys": map[string]any{"p256dh": quiet.p256dh, "auth": quiet.au}, "preview": false})
	if d := out["devices"].([]any)[0].(map[string]any); d["preview"] != false {
		t.Fatalf("registered: %v", d)
	}
	bola.must(200, "PUT", "/v1/me/push", map[string]any{"endpoint": svc.srv.URL + "/loud", "keys": map[string]any{"p256dh": loud.p256dh, "auth": loud.au}})
	adaChat := chatWith(t, ada, bolaID)
	send(t, ada, adaChat, "Hello Bola, the code is 1234")
	pushSettle(t, w.srv)
	if p := quiet.decrypt(t, svc.bodies["/quiet"][0]); p.Kind != push.KindRequest || p.Title != "Taskiem" || p.Body != "You have a new message request" {
		t.Fatalf("a request without preview: %+v", p)
	}
	if p := loud.decrypt(t, svc.bodies["/loud"][0]); p.Title != "Ada Obi" || !strings.Contains(p.Body, "the code is 1234") {
		t.Fatalf("a request with preview: %+v", p)
	}
	req := requestOf(t, bola)
	bola.must(200, "POST", "/v1/me/people/"+req+"/accept", nil)
	send(t, ada, adaChat, "Thanks")
	pushSettle(t, w.srv)
	if p := quiet.decrypt(t, svc.bodies["/quiet"][1]); p.Kind != push.KindPerson || p.Title != "Taskiem" || p.Body != "Someone wrote to you" {
		t.Fatalf("a person's message without preview: %+v", p)
	}
}

// Places (§7): lat and lng in range, a label of up to 200 characters.
func TestPeopleLocationLimits(t *testing.T) {
	w := newPeopleWorld(t)
	ada, _ := w.person(t, "+2348031130071", "Ada Obi")
	bola, bolaID := w.person(t, "+2348031130072", "Bola Ade")
	adaChat, _ := friends(t, ada, bola, bolaID)
	for _, l := range []map[string]any{
		{"lat": 91, "lng": 3}, {"lat": -91, "lng": 3}, {"lat": 6, "lng": 181}, {"lat": 6, "lng": -181},
		{"lat": 6, "lng": 3, "label": strings.Repeat("x", 201)},
	} {
		ada.must(400, "POST", "/v1/me/people/"+adaChat+"/messages", map[string]any{"location": l})
	}
	ada.must(400, "POST", "/v1/me/people/"+adaChat+"/messages", map[string]any{"location": map[string]any{"lat": 6, "lng": 3},
		"card": map[string]any{"type": "split", "amount_kobo": 100, "what": "x"}})
	m := ada.must(201, "POST", "/v1/me/people/"+adaChat+"/messages", map[string]any{"location": map[string]any{"lat": 90, "lng": -180, "label": strings.Repeat("y", 200)}})
	if l := m["location"].(map[string]any); m["kind"] != "location" || l["label"] != strings.Repeat("y", 200) {
		t.Fatalf("a place at the edges: %v", m)
	}
}
