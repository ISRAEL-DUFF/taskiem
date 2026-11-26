package api_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/api"
	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/whatsapp"
)

// editBody is a PATCH's body: the words and the count of edits seen.
func editBody(text string, edits int, extra ...any) map[string]any {
	b := map[string]any{"text": text, "edits": edits}
	for i := 0; i+1 < len(extra); i += 2 {
		b[extra[i].(string)] = extra[i+1]
	}
	return b
}

// clockAt sets the server's clock to d after now.
func clockAt(w *world, d time.Duration) {
	w.srv.SetPreviewClock(func() time.Time { return time.Now().Add(d) })
}

func count(t *testing.T, w *world, sql string, args ...any) int {
	t.Helper()
	var n int
	if err := w.env.DB.Admin.QueryRow(context.Background(), sql, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// Editing a message to a linked business (docs/spec/chat-previews-and-
// editing.md §3): by the member who sent it, within 30 minutes, on both
// copies; 409 when stale; each not_editable reason; the earlier wording in
// the sender's edit history only (the other business cannot read it); the
// audit entry; no push or notice; the changes feed; the buyer's WhatsApp
// sent "✏️ Edited" quoting the original.
func TestEditLinkMessage(t *testing.T) {
	ctx := context.Background()
	w := newWAWorld(t)
	seller, buyer, _, buyerID := linkedPair(t, w)
	const buyerOn = "+2348010000492"
	w.bind(t, buyer, buyerOn)
	w.say(t, buyerOn, "links")
	link := buyer.must(200, "GET", "/v1/link-chats", nil)["conversations"].([]any)[0].(map[string]any)["link"].(string)
	colleague := addMember(t, w.world, seller, "sam", "admin")
	path := func(id string) string { return "/v1/link-chats/" + link + "/messages/" + id }
	send := func(text string) string {
		t.Helper()
		return seller.must(201, "POST", "/v1/link-chats/"+link+"/messages", map[string]any{"text": text})["message"].(map[string]any)["id"].(string)
	}

	before := len(w.graph.Sent(buyerOn))
	id := send("Rice is ₦45,000 a bag")
	told := w.lastTo(t, buyerOn, before)
	if len(told) != 1 {
		t.Fatalf("the buyer's phone: %+v", told)
	}
	var wamid string
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT message_id FROM link_chat_sent WHERE pair_id = $1 AND channel = 'whatsapp'`, id).Scan(&wamid); err != nil {
		t.Fatal(err)
	}
	since := time.Now().Add(-time.Second).UTC().Format(time.RFC3339Nano)
	pushes := count(t, w.world, `SELECT count(*) FROM push_outbox`)
	notices := count(t, w.world, `SELECT count(*) FROM notifications`)

	// Within 30 minutes (29:59 by the server's clock): both copies change.
	clockAt(w.world, 29*time.Minute+59*time.Second)
	before = len(w.graph.Sent(buyerOn))
	m := seller.must(200, "PATCH", path(id), editBody("Rice is ₦44,000 a bag", 0))["message"].(map[string]any)
	if m["body"] != "Rice is ₦44,000 a bag" || m["edits"] != float64(1) || m["edited_at"] == nil {
		t.Fatalf("edited: %v", m)
	}
	// Who sent it goes with our copy only, for the menu's Edit; never the other business's.
	if m["sender"] == nil || m["sender"] == "" {
		t.Errorf("our copy has no sender: %v", m)
	}
	theirs := buyer.must(200, "GET", "/v1/link-chats/"+link+"/messages", nil)["messages"].([]any)
	if b := theirs[len(theirs)-1].(map[string]any); b["sender"] != nil {
		t.Errorf("the buyer's copy names our member: %v", b)
	}
	if b := theirs[len(theirs)-1].(map[string]any); b["id"] != id || b["body"] != "Rice is ₦44,000 a bag" || b["edited_at"] == nil || b["edits"] != float64(1) {
		t.Fatalf("the buyer's copy: %v", b)
	}
	// The buyer's phone: the follow-up, quoting what they were sent.
	if got := w.lastTo(t, buyerOn, before); len(got) != 1 || got[0].Text == "" || !strings.Contains(got[0].Text, "✏️ Edited: *Owner: Rice is ₦44,000 a bag*") || got[0].ReplyTo != wamid {
		t.Fatalf("the follow-up: %+v", got)
	}
	// No push, no notice; the chat list's last line is the new words.
	if count(t, w.world, `SELECT count(*) FROM push_outbox`) != pushes || count(t, w.world, `SELECT count(*) FROM notifications`) != notices {
		t.Errorf("an edit notified someone")
	}
	if last := buyer.must(200, "GET", "/v1/link-chats", nil)["conversations"].([]any)[0].(map[string]any)["last"].(map[string]any); last["body"] != "Rice is ₦44,000 a bag" {
		t.Errorf("the list's last line: %v", last)
	}
	// The changes the app syncs carry it.
	found := false
	for _, x := range buyer.must(200, "GET", "/v1/link-chats/changes?since="+since, nil)["messages"].([]any) {
		if x := x.(map[string]any); x["id"] == id && x["edited_at"] != nil {
			found = true
		}
	}
	if !found {
		t.Errorf("the edit is not in the changes")
	}

	// Stale: another device saw no edits.
	if r := seller.must(409, "PATCH", path(id), editBody("Old words", 0)); r["stale"] != true {
		t.Fatalf("stale: %v", r)
	}
	// Not someone else's: the other business, another member of ours.
	if r := buyer.must(403, "PATCH", path(id), editBody("Mine now", 1)); r["not_editable"] != "not_yours" {
		t.Fatalf("the buyer: %v", r)
	}
	if r := colleague.must(403, "PATCH", path(id), editBody("Mine now", 1)); r["not_editable"] != "not_yours" {
		t.Fatalf("a colleague: %v", r)
	}
	// The words follow a new message's rules; a # tag cannot be added.
	seller.must(422, "PATCH", path(id), editBody("  ", 1))
	seller.must(422, "PATCH", path(id), editBody(strings.Repeat("a", 4001), 1))
	seller.must(422, "PATCH", path(id), editBody("Rice #Rice", 1, "mentions", []any{map[string]any{"kind": "item", "item_id": "x"}}))
	// Again, any number of times.
	if m := seller.must(200, "PATCH", path(id), editBody("Rice is ₦43,500 a bag", 1))["message"].(map[string]any); m["edits"] != float64(2) {
		t.Fatalf("twice: %v", m)
	}
	// 30 minutes and one second: too old.
	clockAt(w.world, 30*time.Minute+time.Second)
	if r := seller.must(403, "PATCH", path(id), editBody("Too late", 2)); r["not_editable"] != "too_old" {
		t.Fatalf("too old: %v", r)
	}
	clockAt(w.world, 0)

	// The earlier wordings: the seller's history, never the buyer's; the
	// buyer's own scope cannot read them.
	hist := seller.must(200, "GET", path(id)+"/edits", nil)["edits"].([]any)
	if len(hist) != 2 || hist[0].(map[string]any)["words_before"] != "Rice is ₦45,000 a bag" || hist[1].(map[string]any)["words_before"] != "Rice is ₦44,000 a bag" {
		t.Fatalf("the history: %v", hist)
	}
	if got := buyer.must(200, "GET", path(id)+"/edits", nil)["edits"].([]any); len(got) != 0 {
		t.Fatalf("the buyer's history: %v", got)
	}
	if n := count(t, w.world, `SELECT count(*) FROM link_message_edits WHERE pair_id = $1 AND tenant_id = $2`, id, buyerID); n != 0 {
		t.Fatalf("kept on the buyer's side: %d", n)
	}
	var seen int
	if err := db.InTenantTx(ctx, w.env.DB.App, []uuid.UUID{uuid.MustParse(buyerID)}, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT count(*) FROM link_message_edits WHERE pair_id = $1`, id).Scan(&seen)
	}); err != nil || seen != 0 {
		t.Fatalf("the buyer reads the seller's history: %d %v", seen, err)
	}
	// Audited on both sides, with the history's id on the seller's.
	if n := count(t, w.world, `SELECT count(*) FROM audit_log WHERE action = 'link.message.edit' AND detail ->> 'message' = $1 AND detail ? 'edit'`, id); n != 4 {
		t.Fatalf("audit entries: %d", n)
	}

	// Each reason a message is not editable.
	for reason, set := range map[string]string{
		"card":          `kind = 'diary'`,
		"money":         `payment_id = gen_random_uuid()`,
		"voice":         `attachment = '{"kind": "voice", "type": "audio/ogg", "size": 10}'`,
		"forwarded":     `forwarded = true`,
		"deleted":       `hidden_at = now()`,
		"taskiem_acted": `acted_at = now()`,
	} {
		x := send("About the " + reason)
		if _, err := w.env.DB.Admin.Exec(ctx, `UPDATE link_messages SET `+set+` WHERE pair_id = $1 AND mine`, x); err != nil {
			t.Fatal(err)
		}
		if r := seller.must(403, "PATCH", path(x), editBody("Changed", 0)); r["not_editable"] != reason {
			t.Errorf("%s: %v", reason, r)
		}
	}
	seller.must(404, "PATCH", path(uuid.NewString()), editBody("Nothing", 0))
}

// fakeChannel is a chat channel for editOnPhone: it records what it sent
// and edited, and refuses edits when told.
type fakeChannel struct {
	api.ChatChannel
	name     string
	failEdit bool
	sent     []whatsapp.Message
	edited   []whatsapp.Message
}

func (f *fakeChannel) Send(_ context.Context, _ string, m whatsapp.Message) (string, error) {
	f.sent = append(f.sent, m)
	return "x", nil
}

// fakeEditor is a fakeChannel that can change a message in place.
type fakeEditor struct{ *fakeChannel }

func (f fakeEditor) Edit(_ context.Context, _, _ string, m whatsapp.Message) error {
	if f.failEdit {
		return errors.New("message can't be edited")
	}
	f.edited = append(f.edited, m)
	return nil
}

// Telegram and Slack are changed in place; when that fails, and always on
// WhatsApp, the follow-up goes, quoting the original on WhatsApp.
func TestEditOnPhone(t *testing.T) {
	ctx := context.Background()
	edited, follow := whatsapp.Message{Text: "[Dangote] Musa: new"}, whatsapp.Message{Text: "✏️ Edited: *new*"}
	for _, name := range []string{"telegram", "slack"} {
		f := fakeEditor{&fakeChannel{name: name}}
		if in, err := api.EditOnPhone(ctx, f, name, "addr", "7", edited, follow); err != nil || !in || len(f.edited) != 1 || len(f.sent) != 0 {
			t.Fatalf("%s in place: %v %v %+v", name, in, err, f.fakeChannel)
		}
		f.failEdit = true
		if in, err := api.EditOnPhone(ctx, f, name, "addr", "7", edited, follow); err != nil || in || len(f.sent) != 1 || f.sent[0].Text != follow.Text || f.sent[0].ReplyTo != "" {
			t.Fatalf("%s when the edit fails: %v %v %+v", name, in, err, f.fakeChannel)
		}
	}
	wa := &fakeChannel{name: "whatsapp"}
	if in, err := api.EditOnPhone(ctx, wa, "whatsapp", "+234", "wamid.1", edited, follow); err != nil || in || len(wa.sent) != 1 || wa.sent[0].ReplyTo != "wamid.1" {
		t.Fatalf("whatsapp: %v %v %+v", in, err, wa)
	}
}

// Editing in a person-to-person chat and a group: both copies sealed again
// and readable by each; the search finds the new words, not the old; the
// preview made again when the link changes; each reason; stale; a group's @
// mentions changed; no audit entry.
func TestEditPeopleAndGroupMessages(t *testing.T) {
	ctx := context.Background()
	w := newPeopleWorld(t)
	w.srv.Egress = previewGuard()
	w.srv.PreviewAnyPort()
	site := newPreviewSite(t)
	ada, _ := w.person(t, "+2348010000871", "Ada")
	bola, bolaID := w.person(t, "+2348010000872", "Bola")
	chat := chatWith(t, ada, bolaID)
	m := send(t, ada, chat, "Meet at the yellow gate "+site.URL+"/first")
	id := m["id"].(string)
	if m["edits"] != float64(0) || m["preview"].(map[string]any)["title"] != "Phones at Jumia /first" {
		t.Fatalf("sent: %v", m)
	}
	path := "/v1/me/people/" + chat + "/messages/" + id
	audits := count(t, w.world, `SELECT count(*) FROM audit_log`)

	e := ada.must(200, "PATCH", path, editBody("Meet at the blue gate "+site.URL+"/second", 0))
	if e["body"] != "Meet at the blue gate "+site.URL+"/second" || e["edits"] != float64(1) || e["edited_at"] == nil ||
		e["preview"].(map[string]any)["title"] != "Phones at Jumia /second" {
		t.Fatalf("edited: %v", e)
	}
	bolaChat := requestOf(t, bola)
	var theirs map[string]any
	for _, x := range messagesOf(t, bola, bolaChat) {
		if x["id"] == id {
			theirs = x
		}
	}
	if theirs == nil || !strings.HasPrefix(theirs["body"].(string), "Meet at the blue gate") || theirs["edited_at"] == nil || theirs["preview"].(map[string]any)["title"] != "Phones at Jumia /second" {
		t.Fatalf("Bola's copy: %v", theirs)
	}
	// Sealed: the new words are not in the clear on either copy.
	if n := count(t, w.world, `SELECT count(*) FROM people_messages WHERE id = $1 AND position(convert_to('blue', 'UTF8') in sealed) > 0`, id); n != 0 {
		t.Fatalf("the words are in the clear")
	}
	if n := count(t, w.world, `SELECT count(*) FROM people_files WHERE message_id = $1 AND kind = 'link_image'`, id); n != 2 {
		t.Fatalf("the new pictures: %d", n)
	}
	// The search: the new words, not the old.
	if got := resultBodies(searchOf(t, ada, "blue", "")); len(got) != 1 {
		t.Fatalf("search for the new words: %v", got)
	}
	if got := resultBodies(searchOf(t, ada, "yellow", "")); len(got) != 0 {
		t.Fatalf("search for the old words: %v", got)
	}
	if got := resultBodies(searchOf(t, bola, "blue", "")); len(got) != 1 {
		t.Fatalf("Bola's search: %v", got)
	}
	// Stale, someone else's, empty, too old.
	if r := ada.must(409, "PATCH", path, editBody("x", 0)); r["stale"] != true {
		t.Fatalf("stale: %v", r)
	}
	if r := bola.must(403, "PATCH", "/v1/me/people/"+bolaChat+"/messages/"+id, editBody("x", 1)); r["not_editable"] != "not_yours" {
		t.Fatalf("Bola: %v", r)
	}
	ada.must(422, "PATCH", path, editBody("", 1))
	clockAt(w.world, 30*time.Minute+time.Second)
	if r := ada.must(403, "PATCH", path, editBody("late", 1)); r["not_editable"] != "too_old" {
		t.Fatalf("too old: %v", r)
	}
	clockAt(w.world, 0)
	for reason, set := range map[string]string{
		"card":      `kind = 'location'`,
		"voice":     `kind = 'media', attachment = '{"kind": "voice", "type": "audio/ogg", "size": 10}'`,
		"forwarded": `forwards = 1`,
		"deleted":   `deleted_at = now(), sealed = NULL`,
	} {
		x := send(t, ada, chat, "About "+reason)["id"].(string)
		if _, err := w.env.DB.Admin.Exec(ctx, `UPDATE people_messages SET `+set+` WHERE id = $1`, x); err != nil {
			t.Fatal(err)
		}
		if r := ada.must(403, "PATCH", "/v1/me/people/"+chat+"/messages/"+x, editBody("Changed", 0)); r["not_editable"] != reason {
			t.Errorf("%s: %v", reason, r)
		}
	}

	// A group: its one copy, sealed under the group's key; @ mentions change.
	_, chidiID := w.person(t, "+2348010000873", "Chidi")
	gid, added := newGroup(t, ada, "Market", bolaID, chidiID)
	g := gsend(t, ada, gid, "Bring the yams")
	gpath := "/v1/me/groups/" + gid + "/messages/" + g["id"].(string)
	ge := ada.must(200, "PATCH", gpath, editBody("Bring the plantains, @Chidi", 0, "mentions", []string{chidiID.String()}))
	// Only a member now in the group is mentioned (one only invited is not).
	tagged, _ := ge["mentions"].([]any)
	if ge["body"] != "Bring the plantains, @Chidi" || ge["edits"] != float64(1) || ge["edited_at"] == nil || (len(tagged) == 1) != (added[chidiID] == "added") {
		t.Fatalf("the group edit: %v (Chidi %s)", ge, added[chidiID])
	}
	if n := count(t, w.world, `SELECT count(*) FROM group_messages WHERE id = $1 AND position(convert_to('plantains', 'UTF8') in sealed) > 0`, g["id"]); n != 0 {
		t.Fatalf("the group's words are in the clear")
	}
	// Another member may not; someone only invited does not see it.
	if added[bolaID] == "added" {
		if r := bola.must(403, "PATCH", gpath, editBody("x", 1)); r["not_editable"] != "not_yours" {
			t.Fatalf("another member: %v", r)
		}
	} else {
		bola.must(404, "PATCH", gpath, editBody("x", 1))
	}
	if r := ada.must(409, "PATCH", gpath, editBody("x", 0)); r["stale"] != true {
		t.Fatalf("a group's stale: %v", r)
	}
	if count(t, w.world, `SELECT count(*) FROM audit_log`) != audits {
		t.Errorf("people and group edits wrote an audit entry")
	}
}

// Editing in a person's chat with a business, both sides: the person's
// message on both copies (not one Taskiem read an order from); the
// member's reply, audited as customer_chat.message.edit, on both copies;
// another member refused.
func TestEditDoorMessages(t *testing.T) {
	ctx := context.Background()
	w := newDoorShopWorld(t)
	owner := w.owner
	tolu, _ := w.personIn(t, "+2348031110081")
	id := tolu.must(201, "POST", "/v1/me/chats", map[string]any{"business_id": w.biz, "from": "shop"})["id"].(string)
	thread := owner.must(200, "GET", "/v1/customer-chats", nil)["chats"].([]any)[0].(map[string]any)["id"].(string)

	m := tolu.must(201, "POST", "/v1/me/chats/"+id+"/messages", map[string]any{"text": "Is the jollof ready?"})
	e := tolu.must(200, "PATCH", "/v1/me/chats/"+id+"/messages/"+m["id"].(string), editBody("Is the chapman ready?", 0))
	if e["body"] != "Is the chapman ready?" || e["edits"] != float64(1) || e["edited_at"] == nil {
		t.Fatalf("the person's edit: %v", e)
	}
	bm := owner.must(200, "GET", "/v1/customer-chats/"+thread+"/messages", nil)["messages"].([]any)
	if b := bm[len(bm)-1].(map[string]any); b["id"] != m["id"] || b["body"] != "Is the chapman ready?" || b["edited_at"] == nil {
		t.Fatalf("the business's copy: %v", b)
	}
	if n := count(t, w.world, `SELECT count(*) FROM customer_messages WHERE tenant_id = $1 AND body::text LIKE '%chapman%'`, w.biz); n != 0 {
		t.Fatalf("kept in the clear")
	}
	if r := tolu.must(409, "PATCH", "/v1/me/chats/"+id+"/messages/"+m["id"].(string), editBody("x", 0)); r["stale"] != true {
		t.Fatalf("stale: %v", r)
	}
	// Taskiem read an order from it: its words stay.
	if _, err := w.env.DB.Admin.Exec(ctx, `UPDATE customer_messages SET acted_at = now() WHERE id = $1`, m["id"]); err != nil {
		t.Fatal(err)
	}
	if r := tolu.must(403, "PATCH", "/v1/me/chats/"+id+"/messages/"+m["id"].(string), editBody("x", 1)); r["not_editable"] != "taskiem_acted" {
		t.Fatalf("acted: %v", r)
	}

	// The business's reply.
	rp := owner.must(201, "POST", "/v1/customer-chats/"+thread+"/messages", map[string]any{"text": "Yes, in 20 minutes"})
	rpath := "/v1/customer-chats/" + thread + "/messages/" + rp["id"].(string)
	re := owner.must(200, "PATCH", rpath, editBody("Yes, in 10 minutes", 0))
	if re["body"] != "Yes, in 10 minutes" || re["edits"] != float64(1) || re["edited_at"] == nil {
		t.Fatalf("the reply's edit: %v", re)
	}
	pm := tolu.must(200, "GET", "/v1/me/chats/"+id+"/messages", nil)["messages"].([]any)
	if p := pm[len(pm)-1].(map[string]any); p["id"] != rp["id"] || p["body"] != "Yes, in 10 minutes" || p["edited_at"] == nil {
		t.Fatalf("the person's copy of the reply: %v", p)
	}
	if n := count(t, w.world, `SELECT count(*) FROM audit_log WHERE tenant_id = $1 AND action = 'customer_chat.message.edit' AND detail ->> 'message' = $2`, w.biz, rp["id"]); n != 1 {
		t.Fatalf("audit entries: %d", n)
	}
	// Not the person's to edit, nor another member's.
	if r := tolu.must(403, "PATCH", "/v1/me/chats/"+id+"/messages/"+rp["id"].(string), editBody("x", 1)); r["not_editable"] != "not_yours" {
		t.Fatalf("the person edits the reply: %v", r)
	}
	colleague := addMember(t, w.world, owner, "kemi", "admin")
	if r := colleague.must(403, "PATCH", rpath, editBody("x", 1)); r["not_editable"] != "not_yours" {
		t.Fatalf("a colleague: %v", r)
	}
	clockAt(w.world, 31*time.Minute)
	if r := owner.must(403, "PATCH", rpath, editBody("x", 1)); r["not_editable"] != "too_old" {
		t.Fatalf("too old: %v", r)
	}
}
