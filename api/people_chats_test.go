package api_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/peoplechats"
	"github.com/israel-duff/taskiem/engine/peoplesafety"
	"github.com/israel-duff/taskiem/engine/push"
)

// peopleWorld is a world with a business that sells in the app, and
// people who chat with each other.
type peopleWorld struct {
	*doorShopWorld
}

func newPeopleWorld(t *testing.T) *peopleWorld {
	t.Helper()
	w := newDoorShopWorld(t)
	withChatLinks(w.world)
	return &peopleWorld{w}
}

// person signs a person in with this number and gives them a name.
func (w *peopleWorld) person(t *testing.T, number, name string) (*client, uuid.UUID) {
	t.Helper()
	c, _ := w.personIn(t, number)
	id := personID(t, c)
	if _, err := w.env.DB.Admin.Exec(context.Background(), `UPDATE users SET name = $2 WHERE id = $1`, id, name); err != nil {
		t.Fatal(err)
	}
	return c, id
}

// older makes an account older than its first week.
func (w *peopleWorld) older(t *testing.T, id uuid.UUID) {
	t.Helper()
	if _, err := w.env.DB.Admin.Exec(context.Background(), `UPDATE users SET created_at = now() - interval '30 days' WHERE id = $1`, id); err != nil {
		t.Fatal(err)
	}
}

// chat starts a's chat with b and answers its id.
func chatWith(t *testing.T, a *client, b uuid.UUID) string {
	t.Helper()
	return a.must(201, "POST", "/v1/me/people", map[string]any{"person_id": b})["id"].(string)
}

func send(t *testing.T, c *client, chat, text string) map[string]any {
	t.Helper()
	return c.must(201, "POST", "/v1/me/people/"+chat+"/messages", map[string]any{"text": text})
}

func messagesOf(t *testing.T, c *client, chat string) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, m := range c.must(200, "GET", "/v1/me/people/"+chat+"/messages", nil)["messages"].([]any) {
		out = append(out, m.(map[string]any))
	}
	return out
}

func listOf(t *testing.T, c *client, path, key string) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, m := range c.must(200, "GET", path, nil)[key].([]any) {
		out = append(out, m.(map[string]any))
	}
	return out
}

// requestOf is the request from someone waiting for c (its chat id).
func requestOf(t *testing.T, c *client) string {
	t.Helper()
	r := listOf(t, c, "/v1/me/people/requests", "requests")
	if len(r) != 1 {
		t.Fatalf("requests: %v", r)
	}
	return r[0]["id"].(string)
}

// One-to-one chats between people (people-chatting §7): a stranger's first
// message waits as a request, read ticks wait for Accept, both copies are
// sealed under each person's key, replies, reactions, stars, places, files,
// delete for me and for everyone, mute, archive, delete the chat, the
// transfer warning, and You's one Chats list.
func TestPeopleChats(t *testing.T) {
	ctx := context.Background()
	w := newPeopleWorld(t)
	ada, adaID := w.person(t, "+2348031120001", "Ada Obi")
	bola, bolaID := w.person(t, "+2348031120002", "Bola Ade")

	chat := ada.must(201, "POST", "/v1/me/people", map[string]any{"person_id": bolaID})
	adaChat := chat["id"].(string)
	if chat["name"] != "Bola Ade" || chat["kind"] != "person" || chat["request"] != false || !strings.Contains(chat["kept"].(string), "not end-to-end encrypted") {
		t.Fatalf("started: %v", chat)
	}
	if again := ada.must(200, "POST", "/v1/me/people", map[string]any{"person_id": bolaID}); again["id"] != adaChat {
		t.Fatalf("started again: %v", again)
	}
	ada.must(404, "POST", "/v1/me/people", map[string]any{"person_id": uuid.New()})
	ada.must(404, "POST", "/v1/me/people", map[string]any{"person_id": adaID})
	ada.must(404, "POST", "/v1/me/people", map[string]any{"link_code": "nobodys-link"})
	// Nothing reaches Bola before a message.
	if r := listOf(t, bola, "/v1/me/people/requests", "requests"); len(r) != 0 {
		t.Fatalf("a request before any message: %v", r)
	}

	first := send(t, ada, adaChat, "Hello Bola, it's Ada from church")
	if first["status"] != "delivered" || first["mine"] != true {
		t.Fatalf("the first message: %v", first)
	}
	// It waits in Bola's Requests, not among their chats.
	bolaChat := requestOf(t, bola)
	if l := bola.must(200, "GET", "/v1/me/people", nil); len(l["chats"].([]any)) != 0 || l["requests"] != float64(1) {
		t.Fatalf("Bola's chats: %v", l)
	}
	if r := listOf(t, bola, "/v1/me/people/requests", "requests")[0]; r["name"] != "Ada Obi" || r["last"] != "Hello Bola, it's Ada from church" || r["unread"] != float64(1) {
		t.Fatalf("the request: %v", r)
	}
	if c := ada.must(200, "GET", "/v1/me/people/"+adaChat, nil); c["waiting"] != true {
		t.Fatalf("Ada's chat waits: %v", c)
	}

	// Sealed at rest under each person's key: neither copy holds the words.
	var plain int
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT count(*) FROM people_messages WHERE position(convert_to('Hello Bola', 'UTF8') in sealed) > 0`).Scan(&plain); err != nil || plain != 0 {
		t.Fatalf("words in the clear: %d %v", plain, err)
	}
	var copies, keys int
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT count(*), count(DISTINCT sealed) FROM people_messages WHERE id = $1`, first["id"]).Scan(&copies, &keys); err != nil || copies != 2 || keys != 2 {
		t.Fatalf("two copies sealed apart: %d %d %v", copies, keys, err)
	}
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT count(*) FROM person_keys WHERE person_id IN ($1, $2) AND sealed <> ''`, adaID, bolaID).Scan(&keys); err != nil || keys != 2 {
		t.Fatalf("each person's key: %d %v", keys, err)
	}

	// Read before accepting: Ada is not told.
	bola.must(204, "POST", "/v1/me/people/"+bolaChat+"/read", nil)
	if m := messagesOf(t, ada, adaChat)[0]; m["status"] != "delivered" {
		t.Fatalf("read shown before accepting: %v", m)
	}
	accepted := bola.must(200, "POST", "/v1/me/people/"+bolaChat+"/accept", nil)
	if accepted["request"] != false {
		t.Fatalf("accepted: %v", accepted)
	}
	bola.must(409, "POST", "/v1/me/people/"+bolaChat+"/accept", nil)
	if m := messagesOf(t, ada, adaChat)[0]; m["status"] != "read" {
		t.Fatalf("read after accepting: %v", m)
	}
	if c := ada.must(200, "GET", "/v1/me/people/"+adaChat, nil); c["waiting"] != false {
		t.Fatalf("Ada's chat no longer waits: %v", c)
	}

	// A reply, a reaction both see, a star only Ada sees.
	reply := bola.must(201, "POST", "/v1/me/people/"+bolaChat+"/messages", map[string]any{"text": "Hi Ada!", "reply_to": first["id"], "client_id": "b1"})
	if again := bola.must(200, "POST", "/v1/me/people/"+bolaChat+"/messages", map[string]any{"text": "Hi Ada!", "client_id": "b1"}); again["id"] != reply["id"] {
		t.Fatalf("a repeat: %v", again)
	}
	bola.must(400, "POST", "/v1/me/people/"+bolaChat+"/messages", map[string]any{"text": "x", "reply_to": uuid.New()})
	got := messagesOf(t, ada, adaChat)
	if len(got) != 2 || got[1]["body"] != "Hi Ada!" || got[1]["reply_to"] != first["id"] || got[1]["mine"] != false {
		t.Fatalf("Ada's copy: %v", got)
	}
	ada.must(200, "PUT", "/v1/me/people/"+adaChat+"/messages/"+reply["id"].(string), map[string]any{"reaction": "❤️", "starred": true})
	if m := messagesOf(t, bola, bolaChat)[1]; m["their_reaction"] != "❤️" || m["starred"] == true {
		t.Fatalf("Bola's copy of the reaction: %v", m)
	}
	if m := messagesOf(t, ada, adaChat)[1]; m["my_reaction"] != "❤️" || m["starred"] != true {
		t.Fatalf("Ada's own: %v", m)
	}
	if s := listOf(t, ada, "/v1/me/people/"+adaChat+"/messages?starred=1", "messages"); len(s) != 1 {
		t.Fatalf("starred: %v", s)
	}

	// The transfer warning on a person's message too.
	send(t, ada, adaChat, "Please send the 5000 to my account 0123456789 at GTBank")
	if m := messagesOf(t, bola, bolaChat); m[len(m)-1]["warn_transfer"] != true {
		t.Fatalf("no transfer warning: %v", m[len(m)-1])
	}
	// A place.
	loc := ada.must(201, "POST", "/v1/me/people/"+adaChat+"/messages", map[string]any{"location": map[string]any{"lat": 6.5244, "lng": 3.3792, "label": "Yaba market"}})
	if loc["kind"] != "location" || loc["location"].(map[string]any)["label"] != "Yaba market" {
		t.Fatalf("a location: %v", loc)
	}
	ada.must(400, "POST", "/v1/me/people/"+adaChat+"/messages", map[string]any{"location": map[string]any{"lat": 200, "lng": 3}})
	if c := listOf(t, bola, "/v1/me/people", "chats")[0]; c["last"] != "Location" {
		t.Fatalf("the list's last words: %v", c)
	}

	// A photo: scanned, checked, each copy sealed.
	photo := pngPhoto(t, 40)
	code, f := ada.postFile(t, "/v1/me/people/"+adaChat+"/files", "dress.png", photo, map[string]string{"text": "This one?"})
	if code != 201 || f["attachment"].(map[string]any)["kind"] != "photo" || f["body"] != "This one?" {
		t.Fatalf("a photo: %d %v", code, f)
	}
	if code, e := ada.postFile(t, "/v1/me/people/"+adaChat+"/files", "x.pdf", []byte("%PDF-1.4 EICAR"), nil); code != 422 {
		t.Fatalf("malware: %d %v", code, e)
	}
	req, _ := http.NewRequest("GET", w.base+"/v1/me/people/"+bolaChat+"/files/"+f["id"].(string), nil)
	req.Header.Set("Authorization", "Bearer "+bola.token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != 200 || !strings.HasPrefix(resp.Header.Get("Content-Type"), "image/") || len(body) < 16 {
		t.Fatalf("Bola's copy of the photo: %d %s", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT count(*) FROM people_files WHERE message_id = $1 AND position($2 in sealed) = 0`, f["id"], body[:16]).Scan(&plain); err != nil || plain != 2 {
		t.Fatalf("the files sealed: %d %v", plain, err)
	}

	// Delete for everyone: gone from Bola's copy too, nothing of it kept.
	ada.must(403, "DELETE", "/v1/me/people/"+adaChat+"/messages/"+reply["id"].(string)+"?for=everyone", nil)
	ada.must(204, "DELETE", "/v1/me/people/"+adaChat+"/messages/"+f["id"].(string)+"?for=everyone", nil)
	for _, c := range []struct {
		c    *client
		chat string
	}{{ada, adaChat}, {bola, bolaChat}} {
		var found bool
		for _, m := range messagesOf(t, c.c, c.chat) {
			if m["id"] == f["id"] {
				found = true
				if m["deleted"] != true || m["body"] != "This message was deleted" || m["attachment"] != nil {
					t.Fatalf("deleted for everyone: %v", m)
				}
			}
		}
		if !found {
			t.Fatal("a message deleted for everyone is shown as deleted")
		}
	}
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT (SELECT count(*) FROM people_messages WHERE id = $1 AND sealed IS NOT NULL) + (SELECT count(*) FROM people_files WHERE message_id = $1)`,
		f["id"]).Scan(&plain); err != nil || plain != 0 {
		t.Fatalf("kept after delete for everyone: %d %v", plain, err)
	}
	// Only within 24 hours.
	old := send(t, ada, adaChat, "An old message")
	if _, err := w.env.DB.Admin.Exec(ctx, `UPDATE people_messages SET created_at = now() - interval '25 hours' WHERE id = $1`, old["id"]); err != nil {
		t.Fatal(err)
	}
	ada.must(409, "DELETE", "/v1/me/people/"+adaChat+"/messages/"+old["id"].(string)+"?for=everyone", nil)
	// Delete for me: Ada's copy only.
	ada.must(204, "DELETE", "/v1/me/people/"+adaChat+"/messages/"+old["id"].(string), nil)
	if n := len(messagesOf(t, bola, bolaChat)); n != len(messagesOf(t, ada, adaChat))+1 {
		t.Fatalf("delete for me reached Bola: %d", n)
	}

	// Mute and archive are each person's own.
	bola.must(200, "PUT", "/v1/me/people/"+bolaChat, map[string]any{"muted": true, "archived": true})
	if c := ada.must(200, "GET", "/v1/me/people/"+adaChat, nil); c["muted"] != false || c["archived"] != false {
		t.Fatalf("Bola's settings reached Ada: %v", c)
	}
	if len(listOf(t, bola, "/v1/me/people?archived=1", "chats")) != 1 || len(listOf(t, bola, "/v1/me/people", "chats")) != 0 {
		t.Fatal("archived")
	}
	bola.must(200, "PUT", "/v1/me/people/"+bolaChat, map[string]any{"archived": false})

	// You's one Chats list: Ada's chat with the business and with Bola,
	// newest first, each marked.
	biz := ada.must(201, "POST", "/v1/me/chats", map[string]any{"business_id": w.biz, "from": "shop"})["id"].(string)
	ada.must(201, "POST", "/v1/me/chats/"+biz+"/messages", map[string]any{"text": "Do you have small chops?"})
	all := ada.must(200, "GET", "/v1/me/conversations", nil)
	items := all["chats"].([]any)
	if len(items) != 2 || items[0].(map[string]any)["kind"] != "business" || items[0].(map[string]any)["id"] != biz ||
		items[1].(map[string]any)["kind"] != "person" || items[1].(map[string]any)["id"] != adaChat || items[1].(map[string]any)["name"] != "Bola Ade" {
		t.Fatalf("one list: %v", all)
	}
	ada.must(204, "POST", "/v1/me/people/"+adaChat+"/read", nil)
	send(t, bola, bolaChat, "See you Sunday")
	items = ada.must(200, "GET", "/v1/me/conversations", nil)["chats"].([]any)
	if items[0].(map[string]any)["kind"] != "person" || items[0].(map[string]any)["unread"] != float64(1) || items[0].(map[string]any)["last"] != "See you Sunday" {
		t.Fatalf("the newest first: %v", items)
	}
	// The business chats alone are as before.
	if l := listOf(t, ada, "/v1/me/chats", "chats"); len(l) != 1 {
		t.Fatalf("/v1/me/chats: %v", l)
	}

	// Delete the chat for me: Ada's copy empties and hides; Bola's stays;
	// a new message brings it back.
	ada.must(204, "DELETE", "/v1/me/people/"+adaChat, nil)
	if len(listOf(t, ada, "/v1/me/people", "chats")) != 0 || len(messagesOf(t, ada, adaChat)) != 0 || len(messagesOf(t, bola, bolaChat)) == 0 {
		t.Fatal("deleted for me")
	}
	send(t, bola, bolaChat, "Are you there?")
	if l := listOf(t, ada, "/v1/me/people", "chats"); len(l) != 1 || l[0]["request"] != false {
		t.Fatalf("back after a new message, not a request: %v", l)
	}
}

// A third person never reaches a chat, nor a business people chats; the
// reach refuses another's chat and any business's transaction; API keys
// are refused.
func TestPeopleChatsIsolation(t *testing.T) {
	ctx := context.Background()
	w := newPeopleWorld(t)
	ada, _ := w.person(t, "+2348031120011", "Ada Obi")
	bola, bolaID := w.person(t, "+2348031120012", "Bola Ade")
	chidi, chidiID := w.person(t, "+2348031120013", "Chidi Eze")
	adaChat := chatWith(t, ada, bolaID)
	m := send(t, ada, adaChat, "Our secret plan")
	code, f := ada.postFile(t, "/v1/me/people/"+adaChat+"/files", "p.png", pngPhoto(t, 70), nil)
	if code != 201 {
		t.Fatalf("a photo: %v", f)
	}
	bolaChat := requestOf(t, bola)

	for _, p := range []string{"/v1/me/people/" + adaChat, "/v1/me/people/" + adaChat + "/messages", "/v1/me/people/" + bolaChat,
		"/v1/me/people/" + adaChat + "/files/" + f["id"].(string), "/v1/me/people/" + adaChat + "/messages/" + m["id"].(string) + "/card"} {
		chidi.must(404, "GET", p, nil)
	}
	chidi.must(404, "POST", "/v1/me/people/"+adaChat+"/messages", map[string]any{"text": "let me in"})
	chidi.must(404, "POST", "/v1/me/people/"+bolaChat+"/accept", nil)
	chidi.must(404, "DELETE", "/v1/me/people/"+adaChat+"/messages/"+m["id"].(string)+"?for=everyone", nil)
	chidi.must(404, "POST", "/v1/me/people/forward", map[string]any{"chat_id": adaChat, "message_id": m["id"], "to": []string{adaChat}})
	if l := chidi.must(200, "GET", "/v1/me/conversations", nil); len(l["chats"].([]any)) != 0 || l["requests"] != float64(0) {
		t.Fatalf("Chidi's list: %v", l)
	}

	count := func(tx pgx.Tx) (int, error) {
		var n int
		err := tx.QueryRow(ctx, `SELECT (SELECT count(*) FROM people_chats) + (SELECT count(*) FROM people_messages) + (SELECT count(*) FROM people_files)
			+ (SELECT count(*) FROM person_keys) + (SELECT count(*) FROM people_blocks)`).Scan(&n)
		return n, err
	}
	// Chidi's own transaction reads none of it.
	if err := db.InPersonTx(ctx, w.env.DB.App, chidiID, func(tx pgx.Tx) error {
		n, err := count(tx)
		if err == nil && n != 0 {
			return fmt.Errorf("Chidi reads %d rows", n)
		}
		if err != nil {
			return err
		}
		// Nor through the reach, with someone else's chat.
		_, err = savepointExec(ctx, tx, `SELECT taskiem_people_reach($1)`, adaChat)
		var pe *pgconn.PgError
		if !errors.As(err, &pe) || pe.Code != "P0002" {
			return fmt.Errorf("the reach with another's chat: %w", err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	// A business reads none, and cannot reach.
	if err := db.InTenantTx(ctx, w.env.DB.App, []uuid.UUID{w.biz}, func(tx pgx.Tx) error {
		n, err := count(tx)
		if err == nil && n != 0 {
			return fmt.Errorf("the business reads %d rows", n)
		}
		if err != nil {
			return err
		}
		_, err = savepointExec(ctx, tx, `SELECT taskiem_people_reach($1)`, adaChat)
		var pe *pgconn.PgError
		if !errors.As(err, &pe) || pe.Code != "42501" {
			return fmt.Errorf("the reach from a business: %w", err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	// Even a person's transaction with a business's door open reads none.
	if err := db.InPersonTx(ctx, w.env.DB.App, bolaID, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT taskiem_people_card_enter($1, $2, 'business')`, bolaID, w.biz); err != nil {
			return err
		}
		n, err := count(tx)
		if err == nil && n != 0 {
			return fmt.Errorf("through a door: %d rows", n)
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
	// The business's own session is its person's: none of Ada's.
	if l := w.owner.must(200, "GET", "/v1/me/people", nil); len(l["chats"].([]any)) != 0 {
		t.Fatalf("the owner's people chats: %v", l)
	}
	w.owner.must(404, "GET", "/v1/me/people/"+adaChat, nil)
	// An API key is refused.
	key := keyFor(t, w.owner, "prod", "run.read")
	key.must(403, "GET", "/v1/me/people", nil)
	key.must(403, "GET", "/v1/me/conversations", nil)
}

// Requests and their limits (§8.2): a new account few; an older one more;
// more as requests are accepted; fewer when they are deleted or blocked.
// A deleted request is not told to the sender; a later message is a
// request again. Messages a minute and new chats an hour.
func TestPeopleRequestsAndLimits(t *testing.T) {
	ctx := context.Background()
	w := newPeopleWorld(t)
	ada, adaID := w.person(t, "+2348031120021", "Ada Obi")
	var others []uuid.UUID
	var clients []*client
	for i := range 7 {
		c, id := w.person(t, fmt.Sprintf("+23480311201%02d", i), fmt.Sprintf("Person %d", i))
		others, clients = append(others, id), append(clients, c)
	}
	// A new account: two waiting at once.
	for i := range 2 {
		send(t, ada, chatWith(t, ada, others[i]), "Hello there")
	}
	third := chatWith(t, ada, others[2])
	if e := ada.must(429, "POST", "/v1/me/people/"+third+"/messages", map[string]any{"text": "Hello there"}); !strings.Contains(e["error"].(string), "waiting") {
		t.Fatalf("a third waiting: %v", e)
	}
	// More messages into a request already waiting are not new requests.
	zero := ada.must(200, "POST", "/v1/me/people", map[string]any{"person_id": others[0]})["id"].(string)
	send(t, ada, zero, "Are you there?")

	// Person 0 deletes it: Ada is not told; her chat still waits.
	req0 := requestOf(t, clients[0])
	clients[0].must(204, "POST", "/v1/me/people/"+req0+"/decline", nil)
	if r := listOf(t, clients[0], "/v1/me/people/requests", "requests"); len(r) != 0 {
		t.Fatalf("declined: %v", r)
	}
	toZero := ada.must(200, "POST", "/v1/me/people", map[string]any{"person_id": others[0]})
	if toZero["waiting"] != true {
		t.Fatalf("Ada is told of the decline: %v", toZero)
	}
	var outcome string
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT outcome FROM people_requests WHERE sender_id = $1 AND recipient_id = $2`, adaID, others[0]).Scan(&outcome); err != nil || outcome != "deleted" {
		t.Fatalf("the ledger: %q %v", outcome, err)
	}
	// One waiting now: a third goes, and a fourth does not.
	send(t, ada, third, "Hello there")
	ada.must(429, "POST", "/v1/me/people/"+chatWith(t, ada, others[3])+"/messages", map[string]any{"text": "Hello"})

	// An older account with many requests accepted has more; one whose
	// requests are mostly deleted or blocked has fewer. Twenty days old:
	// five waiting at once before what became of them
	// (peoplesafety.Allowance).
	if _, err := w.env.DB.Admin.Exec(ctx, `UPDATE users SET created_at = now() - interval '20 days' WHERE id = $1`, adaID); err != nil {
		t.Fatal(err)
	}
	if _, err := w.env.DB.Admin.Exec(ctx, `DELETE FROM people_requests WHERE sender_id = $1`, adaID); err != nil {
		t.Fatal(err)
	}
	if _, err := w.env.DB.Admin.Exec(ctx, `INSERT INTO people_requests (sender_id, recipient_id, sent_at, outcome, decided_at)
		SELECT $1, $2, now() - (g || ' days')::interval, 'deleted', now() FROM generate_series(2, 7) g`, adaID, others[6]); err != nil {
		t.Fatal(err)
	}
	// Refused six times out of six: one waiting at once.
	fresh := chatWith(t, ada, others[4])
	send(t, ada, fresh, "Hi")
	ada.must(429, "POST", "/v1/me/people/"+chatWith(t, ada, others[5])+"/messages", map[string]any{"text": "Hi"})
	if _, err := w.env.DB.Admin.Exec(ctx, `UPDATE people_requests SET outcome = 'accepted' WHERE sender_id = $1 AND outcome = 'deleted'`, adaID); err != nil {
		t.Fatal(err)
	}
	// Accepted six times: four waiting at once now.
	toFive := ada.must(200, "POST", "/v1/me/people", map[string]any{"person_id": others[5]})["id"].(string)
	send(t, ada, toFive, "Hi")

	// The recipient accepting by writing back.
	req4 := requestOf(t, clients[4])
	send(t, clients[4], req4, "Hi Ada")
	if r := listOf(t, clients[4], "/v1/me/people/requests", "requests"); len(r) != 0 {
		t.Fatalf("writing back accepts: %v", r)
	}
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT outcome FROM people_requests WHERE sender_id = $1 AND recipient_id = $2 ORDER BY sent_at DESC LIMIT 1`,
		adaID, others[4]).Scan(&outcome); err != nil || outcome != "accepted" {
		t.Fatalf("accepted by writing back: %q %v", outcome, err)
	}
	// Person 0, who deleted Ada's request, gets her next message as a
	// request again.
	send(t, ada, toZero["id"].(string), "Sorry to bother you again")
	if r := listOf(t, clients[0], "/v1/me/people/requests", "requests"); len(r) != 1 {
		t.Fatalf("a request again: %v", r)
	}

	// Messages a minute in one chat: 10 in the first week (20 after).
	bola, bolaID := w.person(t, "+2348031120030", "Bola Ade")
	accepted := chatWith(t, clients[4], bolaID)
	send(t, clients[4], accepted, "hi") // a request to Bola from person 4, who is new
	bolaChat := requestOf(t, bola)
	bola.must(200, "POST", "/v1/me/people/"+bolaChat+"/accept", nil)
	for i := range 10 {
		send(t, bola, bolaChat, fmt.Sprintf("message %d", i))
	}
	bola.must(429, "POST", "/v1/me/people/"+bolaChat+"/messages", map[string]any{"text": "one more"})

	// New chats an hour: 5 in the first week.
	carl, _ := w.person(t, "+2348031120031", "Carl")
	for i := range 5 {
		chatWith(t, carl, others[i])
	}
	carl.must(429, "POST", "/v1/me/people", map[string]any{"person_id": others[5]})
}

// Blocking (§8.1): from a request, with a report; what they send stays on
// their side with one tick and nothing tells them; unblocking brings
// nothing sent meanwhile; a report needs consent and holds the last 20
// messages from the server's copy.
func TestPeopleBlocksAndReports(t *testing.T) {
	ctx := context.Background()
	w := newPeopleWorld(t)
	ada, adaID := w.person(t, "+2348031120041", "Ada Obi")
	w.older(t, adaID)
	bola, bolaID := w.person(t, "+2348031120042", "Bola Ade")
	adaChat := chatWith(t, ada, bolaID)
	for i := range 20 {
		send(t, ada, adaChat, fmt.Sprintf("Please answer me now %d", i))
	}
	req := requestOf(t, bola)
	bola.must(400, "POST", "/v1/me/people/"+req+"/report", map[string]any{"reason": "scam", "consent": false})
	bola.must(400, "POST", "/v1/me/people/"+req+"/report", map[string]any{"reason": "rude", "consent": true})
	out := bola.must(200, "POST", "/v1/me/people/"+req+"/block", map[string]any{"report": map[string]any{"reason": "scam", "note": "asks for money", "consent": true}})
	if out["blocked"] != true || out["reported"] != true || out["removed"] != true {
		t.Fatalf("block and report: %v", out)
	}
	// The report's messages are sealed under its own key (00480): read
	// them as a moderator would, through the box.
	var msgs []map[string]any
	var reported uuid.UUID
	var fromRequest bool
	var repID uuid.UUID
	var sealed []byte
	var boxKey, kmsKey string
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT id, reported_id, from_request, sealed, box_key, kms_key FROM people_reports WHERE person_id = $1`, bolaID).
		Scan(&repID, &reported, &fromRequest, &sealed, &boxKey, &kmsKey); err != nil {
		t.Fatal(err)
	}
	box, err := peoplesafety.OpenBox(ctx, w.srv.Vault, peoplesafety.ReportPurpose(repID), repID, boxKey, kmsKey)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := box.Open(peoplesafety.MessagesRow, sealed)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &msgs); err != nil || len(msgs) != 20 || reported != adaID || !fromRequest || msgs[0]["body"] != "Please answer me now 19" || msgs[19]["body"] != "Please answer me now 0" || msgs[0]["from_reporter"] != false {
		t.Fatalf("the report: %v %v %d %v", reported, fromRequest, len(msgs), msgs[0])
	}
	if r := listOf(t, bola, "/v1/me/people/requests", "requests"); len(r) != 0 {
		t.Fatalf("the request after blocking: %v", r)
	}
	if b := listOf(t, bola, "/v1/me/people/blocked", "people"); len(b) != 1 || b[0]["name"] != "Ada Obi" || b[0]["chat_id"] != req {
		t.Fatalf("blocked: %v", b)
	}
	var outcome string
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT outcome FROM people_requests WHERE sender_id = $1`, adaID).Scan(&outcome); err != nil || outcome != "blocked" {
		t.Fatalf("the ledger: %q %v", outcome, err)
	}

	// A while later, Ada writes again: one tick, nothing reaches Bola,
	// nothing tells Ada.
	if _, err := w.env.DB.Admin.Exec(ctx, `UPDATE people_messages SET created_at = created_at - interval '2 minutes' WHERE person_id = $1`, adaID); err != nil {
		t.Fatal(err)
	}
	m := send(t, ada, adaChat, "Hello?")
	if m["status"] != "sent" {
		t.Fatalf("one tick: %v", m)
	}
	if c := ada.must(200, "GET", "/v1/me/people/"+adaChat, nil); c["blocked"] != false || c["phone"] != nil {
		t.Fatalf("Ada is told: %v", c)
	}
	if len(listOf(t, bola, "/v1/me/people/requests", "requests")) != 0 || len(listOf(t, bola, "/v1/me/people", "chats")) != 0 {
		t.Fatal("a blocked person's message arrived")
	}
	// Bola cannot write to Ada while blocking her.
	bola.must(409, "POST", "/v1/me/people/"+req+"/messages", map[string]any{"text": "stop"})

	// Unblocked: what was sent meanwhile never arrives; the next is a
	// request again.
	bola.must(200, "DELETE", "/v1/me/people/"+req+"/block", nil)
	if len(listOf(t, bola, "/v1/me/people/blocked", "people")) != 0 {
		t.Fatal("still blocked")
	}
	send(t, ada, adaChat, "Hi again")
	r := listOf(t, bola, "/v1/me/people/requests", "requests")
	if len(r) != 1 || r[0]["id"] != req {
		t.Fatalf("a request again: %v", r)
	}
	for _, x := range messagesOf(t, bola, req) {
		if x["body"] == "Hello?" {
			t.Fatal("a message sent while blocked arrived")
		}
	}

	// A report of an accepted chat, from Ada's side.
	ada.must(201, "POST", "/v1/me/people/"+adaChat+"/report", map[string]any{"reason": "abuse", "consent": true})
}

// Forwarding (§8.2): marked forwarded, at most five chats at once, one
// at a time once forwarded many times; a share card is not forwarded.
func TestPeopleForwards(t *testing.T) {
	ctx := context.Background()
	w := newPeopleWorld(t)
	ada, adaID := w.person(t, "+2348031120051", "Ada Obi")
	w.older(t, adaID)
	var chats []string
	for i := range 7 {
		_, id := w.person(t, fmt.Sprintf("+23480311205%02d", i+10), fmt.Sprintf("Friend %d", i))
		chats = append(chats, chatWith(t, ada, id))
	}
	src := send(t, ada, chats[0], "Big news: free data today")
	ada.must(429, "POST", "/v1/me/people/forward", map[string]any{"chat_id": chats[0], "message_id": src["id"], "to": chats[1:7]})
	out := ada.must(200, "POST", "/v1/me/people/forward", map[string]any{"chat_id": chats[0], "message_id": src["id"], "to": chats[1:3]})
	if len(out["sent"].([]any)) != 2 || len(out["failed"].([]any)) != 0 {
		t.Fatalf("forwarded: %v", out)
	}
	fw := out["sent"].([]any)[0].(map[string]any)["message"].(map[string]any)
	if fw["forwarded"] != true || fw["forwarded_many"] == true || fw["body"] != "Big news: free data today" {
		t.Fatalf("a forward: %v", fw)
	}
	// The cap follows the mark: forwarded four times on its way, it is not
	// shown forwarded many times and goes to several chats at once (its
	// forwards are then marked); five times, it is shown so and goes to
	// one chat at a time.
	if _, err := w.env.DB.Admin.Exec(ctx, `UPDATE people_messages SET forwards = 4 WHERE id = $1`, fw["id"]); err != nil {
		t.Fatal(err)
	}
	if m := messagesOf(t, ada, chats[1]); m[len(m)-1]["forwarded_many"] == true {
		t.Fatalf("four times is not many: %v", m)
	}
	out = ada.must(200, "POST", "/v1/me/people/forward", map[string]any{"chat_id": chats[1], "message_id": fw["id"], "to": chats[3:5]})
	if m := out["sent"].([]any)[0].(map[string]any)["message"].(map[string]any); m["forwarded_many"] != true || len(out["sent"].([]any)) != 2 {
		t.Fatalf("marked forwarded many times: %v", out)
	}
	if _, err := w.env.DB.Admin.Exec(ctx, `UPDATE people_messages SET forwards = 5 WHERE id = $1`, fw["id"]); err != nil {
		t.Fatal(err)
	}
	if m := messagesOf(t, ada, chats[1]); m[len(m)-1]["forwarded_many"] != true {
		t.Fatalf("five times is many: %v", m)
	}
	if e := ada.must(429, "POST", "/v1/me/people/forward", map[string]any{"chat_id": chats[1], "message_id": fw["id"], "to": chats[3:5]}); e["forwarded_many"] != true {
		t.Fatalf("many times: %v", e)
	}
	ada.must(200, "POST", "/v1/me/people/forward", map[string]any{"chat_id": chats[1], "message_id": fw["id"], "to": chats[3:4]})
	// A photo forwarded is a copy of its own, sealed for each.
	code, f := ada.postFile(t, "/v1/me/people/"+chats[0]+"/files", "p.png", pngPhoto(t, 90), nil)
	if code != 201 {
		t.Fatal(f)
	}
	out = ada.must(200, "POST", "/v1/me/people/forward", map[string]any{"chat_id": chats[0], "message_id": f["id"], "to": chats[3:4]})
	if m := out["sent"].([]any)[0].(map[string]any)["message"].(map[string]any); m["attachment"] == nil || m["forwarded"] != true {
		t.Fatalf("a photo forwarded: %v", m)
	}
	// A share card is between two people.
	split := ada.must(201, "POST", "/v1/me/people/"+chats[0]+"/messages", map[string]any{"card": map[string]any{"type": "split", "amount_kobo": 300000, "what": "The cake",
		"pay_to": map[string]any{"bank": "GTBank", "account_number": "0123456789", "account_name": "Ada Obi"}}})
	ada.must(409, "POST", "/v1/me/people/forward", map[string]any{"chat_id": chats[0], "message_id": split["id"], "to": chats[1:2]})
	ada.must(400, "POST", "/v1/me/people/forward", map[string]any{"chat_id": chats[0], "message_id": src["id"], "to": []string{chats[1], chats[1]}})
}

// Pushes on the person's own devices (§13): a person's message (kind
// person) and a request (kind request), with the device's preview setting
// and the person's choices; none for a muted chat or a blocked sender.
func TestPeoplePush(t *testing.T) {
	w := newPeopleWorld(t)
	w.srv.Push = pushSender(t)
	w.srv.Push.Validate = func(string) error { return nil }
	svc := newPushService(t)
	ada, _ := w.person(t, "+2348031120061", "Ada Obi")
	bola, bolaID := w.person(t, "+2348031120062", "Bola Ade")
	br := newPushBrowser(t)
	out := bola.must(200, "PUT", "/v1/me/push", map[string]any{"endpoint": svc.srv.URL + "/bola", "keys": map[string]any{"p256dh": br.p256dh, "auth": br.au}})
	devID := out["devices"].([]any)[0].(map[string]any)["id"].(string)

	adaChat := chatWith(t, ada, bolaID)
	send(t, ada, adaChat, "Hello Bola")
	pushSettle(t, w.srv)
	if svc.count("/bola") != 1 {
		t.Fatalf("pushes: %d", svc.count("/bola"))
	}
	req := requestOf(t, bola)
	p := br.decrypt(t, svc.bodies["/bola"][0])
	if p.Kind != push.KindRequest || p.Title != "Ada Obi" || p.Body != "Message request: Hello Bola" || p.Link != "/you/people/requests/"+req || p.Business != "" {
		t.Fatalf("a request's push: %+v", p)
	}
	// Requests turned off: none.
	bola.must(200, "PUT", "/v1/me/notifications", map[string]any{"requests": false})
	send(t, ada, adaChat, "Hello again")
	pushSettle(t, w.srv)
	if svc.count("/bola") != 1 {
		t.Fatal("pushed with requests off")
	}
	bola.must(200, "PUT", "/v1/me/notifications", map[string]any{"requests": true})
	bola.must(200, "POST", "/v1/me/people/"+req+"/accept", nil)
	send(t, ada, adaChat, "Thanks!")
	pushSettle(t, w.srv)
	p = br.decrypt(t, svc.bodies["/bola"][1])
	if p.Kind != push.KindPerson || p.Title != "Ada Obi" || p.Body != "Thanks!" || p.Link != "/you/people/"+req || p.Unread != 1 {
		t.Fatalf("a person's push: %+v", p)
	}
	// The device's preview off: neither the name nor the words.
	bola.must(200, "PATCH", "/v1/me/push/"+devID, map[string]any{"preview": false})
	send(t, ada, adaChat, "The code is 4412")
	pushSettle(t, w.srv)
	if p := br.decrypt(t, svc.bodies["/bola"][2]); p.Title != "Taskiem" || p.Body != "Someone wrote to you" {
		t.Fatalf("preview off: %+v", p)
	}
	// Muted: none.
	bola.must(200, "PUT", "/v1/me/people/"+req, map[string]any{"muted": true})
	send(t, ada, adaChat, "muted")
	pushSettle(t, w.srv)
	bola.must(200, "PUT", "/v1/me/people/"+req, map[string]any{"muted": false})
	// People's messages turned off: none.
	bola.must(200, "PUT", "/v1/me/notifications", map[string]any{"people": false})
	if n := bola.must(200, "GET", "/v1/me/notifications", nil); n["people"] != false || n["requests"] != true {
		t.Fatalf("the settings: %v", n)
	}
	send(t, ada, adaChat, "off")
	pushSettle(t, w.srv)
	bola.must(200, "PUT", "/v1/me/notifications", map[string]any{"people": true})
	// Blocked: none.
	bola.must(200, "POST", "/v1/me/people/"+req+"/block", nil)
	send(t, ada, adaChat, "blocked")
	pushSettle(t, w.srv)
	if svc.count("/bola") != 3 {
		t.Fatalf("pushes when muted, off or blocked: %d", svc.count("/bola"))
	}
}

// Cards (§10.1, §11 A): a product, a business, an order and a delivery are
// shared with only what is needed and opened live through the doors as the
// person opening it (an order only when it is theirs); share cards carry
// the payer's own account and are marked paid by either side.
func TestPeopleCards(t *testing.T) {
	ctx := context.Background()
	w := newPeopleWorld(t)
	ada, adaID := w.person(t, "+2348031120071", "Ada Obi")
	bola, bolaID := w.person(t, "+2348031120072", "Bola Ade")
	adaChat := chatWith(t, ada, bolaID)
	send(t, ada, adaChat, "Hi")
	bolaChat := requestOf(t, bola)
	bola.must(200, "POST", "/v1/me/people/"+bolaChat+"/accept", nil)

	// A product from the business's shop.
	bizChat := ada.must(201, "POST", "/v1/me/chats", map[string]any{"business_id": w.biz, "from": "shop"})["id"].(string)
	var item string
	for _, x := range ada.must(200, "GET", "/v1/me/chats/"+bizChat+"/shop", nil)["items"].([]any) {
		if x.(map[string]any)["name"] == "Jollof rice" {
			item = x.(map[string]any)["id"].(string)
		}
	}
	card := ada.must(201, "POST", "/v1/me/people/"+adaChat+"/messages", map[string]any{"card": map[string]any{"type": "product", "business_id": w.biz, "item_id": item}})
	c := card["card"].(map[string]any)
	if card["kind"] != "card" || c["title"] != "Jollof rice" || c["price_kobo"] == nil || c["business"] != "Mama's Kitchen" || c["item_id"] != item {
		t.Fatalf("a product card: %v", card)
	}
	ada.must(404, "POST", "/v1/me/people/"+adaChat+"/messages", map[string]any{"card": map[string]any{"type": "product", "business_id": w.biz, "item_id": "nope"}})
	before := w.audits(t, "customer.card", bolaID)
	open := bola.must(200, "GET", "/v1/me/people/"+bolaChat+"/messages/"+card["id"].(string)+"/card", nil)
	if live := open["live"].(map[string]any); live["product"].(map[string]any)["name"] != "Jollof rice" || live["yours"] != false {
		t.Fatalf("opened: %v", open)
	}
	if w.audits(t, "customer.card", bolaID) != before+1 {
		t.Fatal("opening a card is audited in the business's chain as the person")
	}

	// A business card.
	bc := ada.must(201, "POST", "/v1/me/people/"+adaChat+"/messages", map[string]any{"card": map[string]any{"type": "business", "business_id": w.biz}})
	if bc["card"].(map[string]any)["title"] != "Mama's Kitchen" {
		t.Fatalf("a business card: %v", bc)
	}
	if live := bola.must(200, "GET", "/v1/me/people/"+bolaChat+"/messages/"+bc["id"].(string)+"/card", nil)["live"].(map[string]any); live["business"].(map[string]any)["name"] != "Mama's Kitchen" {
		t.Fatalf("a business opened: %v", live)
	}

	// An order: only Ada's own can be shared; Bola sees only what was
	// shared, Ada her order live.
	ref := placeAppOrder(t, w.doorShopWorld, ada, bizChat)
	ada.must(404, "POST", "/v1/me/people/"+adaChat+"/messages", map[string]any{"card": map[string]any{"type": "order", "business_id": w.biz, "reference": "NOT-MINE"}})
	oc := ada.must(201, "POST", "/v1/me/people/"+adaChat+"/messages", map[string]any{"card": map[string]any{"type": "order", "business_id": w.biz, "reference": ref}})
	if c := oc["card"].(map[string]any); c["reference"] != ref || c["title"] != "Order "+ref || c["business"] != "Mama's Kitchen" || c["amount_kobo"] != nil {
		t.Fatalf("an order card: %v", oc)
	}
	theirs := bola.must(200, "GET", "/v1/me/people/"+bolaChat+"/messages/"+oc["id"].(string)+"/card", nil)
	if live := theirs["live"].(map[string]any); live["yours"] != false || live["order"] != nil {
		t.Fatalf("someone else's order opened: %v", theirs)
	}
	mine := ada.must(200, "GET", "/v1/me/people/"+adaChat+"/messages/"+oc["id"].(string)+"/card", nil)
	if live := mine["live"].(map[string]any); live["yours"] != true || live["order"].(map[string]any)["reference"] != ref {
		t.Fatalf("Ada's own order opened: %v", mine)
	}
	// A delivery before a rider has it.
	ada.must(409, "POST", "/v1/me/people/"+adaChat+"/messages", map[string]any{"card": map[string]any{"type": "delivery", "business_id": w.biz, "reference": ref}})

	// Chat on a shared card starts Bola's own chat with the business
	// (§10.2), from "shared"; the business sees a customer, not who
	// shared it.
	bola.must(400, "POST", "/v1/me/chats", map[string]any{"business_id": w.biz, "from": "elsewhere"})
	if c := bola.must(201, "POST", "/v1/me/chats", map[string]any{"business_id": w.biz, "from": "shared"}); c["name"] != "Mama's Kitchen" {
		t.Fatalf("a chat from a shared card: %v", c)
	}
	// A business that blocked Bola does not open for her.
	for _, x := range w.owner.must(200, "GET", "/v1/customer-chats", nil)["chats"].([]any) {
		if x.(map[string]any)["name"] == "Bola Ade" {
			w.owner.must(204, "POST", "/v1/customer-chats/"+x.(map[string]any)["id"].(string)+"/block", nil)
		}
	}
	if e := bola.must(403, "GET", "/v1/me/people/"+bolaChat+"/messages/"+card["id"].(string)+"/card", nil); e["blocked"] != true {
		t.Fatalf("blocked by the business: %v", e)
	}

	// Share cards: the payer's account typed, or saved on their side.
	ada.must(400, "POST", "/v1/me/people/"+adaChat+"/messages", map[string]any{"card": map[string]any{"type": "split", "amount_kobo": 300000, "what": "The cake"}})
	ada.must(400, "PUT", "/v1/me/payout-account", map[string]any{"bank": "GTBank", "account_number": "12345", "account_name": "Ada Obi"})
	if a := ada.must(200, "GET", "/v1/me/payout-account", nil); a["account"] != nil || a["checked"] != false {
		t.Fatalf("no account yet: %v", a)
	}
	ada.must(200, "PUT", "/v1/me/payout-account", map[string]any{"bank": "GTBank", "account_number": "0123 456 789", "account_name": "Ada Obi"})
	if a := ada.must(200, "GET", "/v1/me/payout-account", nil)["account"].(map[string]any); a["account_number"] != "0123456789" {
		t.Fatalf("the saved account: %v", a)
	}
	var plain int
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT count(*) FROM person_payout_accounts WHERE person_id = $1 AND position(convert_to('0123456789', 'UTF8') in sealed) = 0`, adaID).Scan(&plain); err != nil || plain != 1 {
		t.Fatalf("the account sealed: %d %v", plain, err)
	}
	split := ada.must(201, "POST", "/v1/me/people/"+adaChat+"/messages", map[string]any{"card": map[string]any{"type": "split", "amount_kobo": 300000, "what": "The cake"}})
	sc := split["card"].(map[string]any)
	if sc["title"] != "Your share: ₦3,000" || sc["pay_to"].(map[string]any)["account_name"] != "Ada Obi" || sc["paid"] == true {
		t.Fatalf("a share card: %v", split)
	}
	var got map[string]any
	for _, m := range messagesOf(t, bola, bolaChat) {
		if m["id"] == split["id"] {
			got = m
		}
	}
	if got == nil || got["warn_transfer"] == true || got["card"].(map[string]any)["what"] != "The cake" {
		t.Fatalf("Bola's share card: %v", got)
	}
	paid := bola.must(200, "POST", "/v1/me/people/"+bolaChat+"/messages/"+split["id"].(string)+"/paid", map[string]any{"paid": true})
	if pc := paid["card"].(map[string]any); pc["paid"] != true || pc["paid_by_me"] != true {
		t.Fatalf("marked paid: %v", paid)
	}
	for _, m := range messagesOf(t, ada, adaChat) {
		if m["id"] == split["id"] {
			if pc := m["card"].(map[string]any); pc["paid"] != true || pc["paid_by_me"] != false {
				t.Fatalf("Ada's copy marked paid: %v", m)
			}
		}
	}
	bola.must(409, "POST", "/v1/me/people/"+bolaChat+"/messages/"+oc["id"].(string)+"/paid", map[string]any{"paid": true})
	ada.must(204, "DELETE", "/v1/me/payout-account", nil)
}

// Deleting an account (§14): the person's copies, blocks, saved account,
// key and requests go; the other person keeps the messages, shown as from
// "Deleted account", and can write no more.
func TestPeopleAccountDeletion(t *testing.T) {
	ctx := context.Background()
	w := newPeopleWorld(t)
	ada, adaID := w.person(t, "+2348031120081", "Ada Obi")
	bola, bolaID := w.person(t, "+2348031120082", "Bola Ade")
	adaChat := chatWith(t, ada, bolaID)
	send(t, ada, adaChat, "Bye for now")
	bolaChat := requestOf(t, bola)
	bola.must(200, "POST", "/v1/me/people/"+bolaChat+"/accept", nil)
	send(t, bola, bolaChat, "Take care")
	ada.must(200, "PUT", "/v1/me/payout-account", map[string]any{"bank": "GTBank", "account_number": "0123456789", "account_name": "Ada Obi"})

	ada.must(202, "POST", "/v1/me/delete/start", map[string]any{"via": "sms"})
	ada.must(200, "POST", "/v1/me/delete", map[string]any{"confirm": true, "code": w.sms.code(t, "+2348031120081")})

	var n int
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT (SELECT count(*) FROM people_chats WHERE person_id = $1) + (SELECT count(*) FROM people_messages WHERE person_id = $1)
		+ (SELECT count(*) FROM person_keys WHERE person_id = $1) + (SELECT count(*) FROM person_payout_accounts WHERE person_id = $1)
		+ (SELECT count(*) FROM people_requests WHERE sender_id = $1 OR recipient_id = $1)`, adaID).Scan(&n); err != nil || n != 0 {
		t.Fatalf("Ada's side left: %d %v", n, err)
	}
	c := bola.must(200, "GET", "/v1/me/people/"+bolaChat, nil)
	if c["name"] != "Deleted account" || c["gone"] != true || c["contact"] != nil {
		t.Fatalf("Bola's chat: %v", c)
	}
	if m := messagesOf(t, bola, bolaChat); len(m) != 2 || m[0]["body"] != "Bye for now" {
		t.Fatalf("Bola keeps the messages: %v", m)
	}
	if l := listOf(t, bola, "/v1/me/conversations", "chats"); len(l) != 1 || l[0]["name"] != "Deleted account" || l[0]["gone"] != true {
		t.Fatalf("the list: %v", l)
	}
	if e := bola.must(409, "POST", "/v1/me/people/"+bolaChat+"/messages", map[string]any{"text": "Hello?"}); !strings.Contains(e["error"].(string), "deleted") {
		t.Fatalf("writing to a deleted account: %v", e)
	}
	// The chat Bola has answers; nothing new can start with a deleted account.
	if c := bola.must(200, "POST", "/v1/me/people", map[string]any{"person_id": adaID}); c["id"] != bolaChat {
		t.Fatalf("the chat Bola has: %v", c)
	}
	carl, _ := w.person(t, "+2348031120083", "Carl")
	carl.must(404, "POST", "/v1/me/people", map[string]any{"person_id": adaID})
}

// The policy finding people sets: who may message, whether as a request,
// contacts, numbers shown and the stranger checks.
type testPolicy struct {
	peoplechats.Stub
	nobody   map[uuid.UUID]bool
	contacts map[[2]uuid.UUID]bool
	shown    map[uuid.UUID]bool
	verdict  peoplechats.Verdict
}

func (p *testPolicy) MayMessage(_ context.Context, from, to uuid.UUID) (bool, bool, error) {
	if p.nobody[to] {
		return false, false, nil
	}
	return true, !p.contacts[[2]uuid.UUID{to, from}], nil
}

func (p *testPolicy) IsContact(_ context.Context, a, b uuid.UUID) (bool, error) {
	return p.contacts[[2]uuid.UUID{a, b}], nil
}

func (p *testPolicy) NumberShownTo(_ context.Context, owner, _ uuid.UUID) (bool, error) {
	return p.shown[owner], nil
}

func (p *testPolicy) ScreenStranger(context.Context, uuid.UUID, uuid.UUID, string) (peoplechats.Verdict, error) {
	return p.verdict, nil
}

// Stream B's policy, wired in: contacts write straight to chats, "nobody"
// refuses, numbers shown by choice, and the stranger checks' verdicts.
func TestPeoplePolicy(t *testing.T) {
	w := newPeopleWorld(t)
	pol := &testPolicy{nobody: map[uuid.UUID]bool{}, contacts: map[[2]uuid.UUID]bool{}, shown: map[uuid.UUID]bool{}}
	w.srv.ChatPolicy = pol
	ada, adaID := w.person(t, "+2348031120091", "Ada Obi")
	bola, bolaID := w.person(t, "+2348031120092", "Bola Ade")
	_, chidiID := w.person(t, "+2348031120093", "Chidi Eze")
	pol.contacts[[2]uuid.UUID{bolaID, adaID}] = true
	pol.shown[bolaID] = true
	pol.nobody[chidiID] = true

	adaChat := chatWith(t, ada, bolaID)
	send(t, ada, adaChat, "Hi Bola")
	if l := listOf(t, bola, "/v1/me/people", "chats"); len(l) != 1 || l[0]["request"] != false {
		t.Fatalf("a contact writes straight to the chats: %v", l)
	}
	c := ada.must(200, "GET", "/v1/me/people/"+adaChat, nil)
	if c["phone"] != "+2348031120092" {
		t.Fatalf("Bola shows her number: %v", c)
	}
	bolaChat := listOf(t, bola, "/v1/me/people", "chats")[0]["id"].(string)
	if c := bola.must(200, "GET", "/v1/me/people/"+bolaChat, nil); c["contact"] != true || c["phone"] != nil {
		t.Fatalf("Ada is Bola's contact, her number hidden: %v", c)
	}
	ada.must(403, "POST", "/v1/me/people", map[string]any{"person_id": chidiID})

	// The stranger checks: a warning, then a refusal.
	danC, danID := w.person(t, "+2348031120094", "Dan")
	dan := chatWith(t, ada, danID)
	pol.verdict = peoplechats.Verdict{Action: peoplechats.Warn}
	send(t, ada, dan, "You won a prize, send your PIN")
	if m := messagesOf(t, danC, requestOf(t, danC)); m[0]["warn"] != true {
		t.Fatalf("a warned message: %v", m)
	}
	pol.verdict = peoplechats.Verdict{Action: peoplechats.Refuse, Reason: "This message was not sent."}
	if e := ada.must(403, "POST", "/v1/me/people/"+dan+"/messages", map[string]any{"text": "send your PIN now"}); e["error"] != "This message was not sent." {
		t.Fatalf("refused: %v", e)
	}
}

// savepointExec runs one statement in a savepoint, so a refusal leaves the
// transaction usable.
func savepointExec(ctx context.Context, tx pgx.Tx, sql string, args ...any) (pgconn.CommandTag, error) {
	sp, err := tx.Begin(ctx)
	if err != nil {
		return pgconn.CommandTag{}, err
	}
	tag, err := sp.Exec(ctx, sql, args...)
	if err != nil {
		_ = sp.Rollback(ctx)
		return tag, err
	}
	return tag, sp.Commit(ctx)
}
