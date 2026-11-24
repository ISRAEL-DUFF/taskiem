package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"mime/multipart"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/israel-duff/taskiem/api"
	"github.com/israel-duff/taskiem/engine/voice"
)

// taskiemFile sends a file to Taskiem on the web.
func (c *client) taskiemFile(t *testing.T, name string, data []byte, text string) (int, map[string]any) {
	t.Helper()
	var b bytes.Buffer
	mw := multipart.NewWriter(&b)
	fw, _ := mw.CreateFormFile("file", name)
	_, _ = fw.Write(data)
	if text != "" {
		_ = mw.WriteField("text", text)
	}
	_ = mw.Close()
	return c.rawPost(t, "/v1/taskiem-chat/files", mw.FormDataContentType(), b.Bytes())
}

func uuidOf(t *testing.T, s string) uuid.UUID {
	t.Helper()
	id, err := uuid.Parse(s)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func jsonText(v any) string {
	raw, _ := json.Marshal(v)
	return string(raw)
}

// The Taskiem conversation on the web, with its history on every channel
// (L3, §5.5; migration 00295): what the owner says on WhatsApp and
// Taskiem's replies are in the web's Taskiem conversation; a message on
// the web is read as one typed on WhatsApp, its reply's buttons pressable
// there; the words are kept sealed, a code never; each person reads only
// their own; a message past its retention is purged.
func TestTaskiemConversation(t *testing.T) {
	ctx := context.Background()
	w := newWAWorld(t)
	owner := w.tenant(t, "Ada's Kitchen", "ada@history.test")
	const on = "+2348010000501"
	w.bind(t, owner, on)

	// On WhatsApp.
	reply := w.say(t, on, "cash in 5,000 till")
	if !strings.Contains(reply, "₦5,000") {
		t.Fatalf("on WhatsApp: %q", reply)
	}
	w.say(t, on, "no")
	hist := owner.must(200, "GET", "/v1/taskiem-chat", nil)["messages"].([]any)
	if len(hist) != 4 {
		t.Fatalf("four messages from WhatsApp: %v", hist)
	}
	first, second := hist[0].(map[string]any), hist[1].(map[string]any)
	if first["inbound"] != true || first["text"] != "cash in 5,000 till" || first["channel"] != "whatsapp" ||
		second["inbound"] != false || !strings.Contains(second["text"].(string), "₦5,000") {
		t.Fatalf("WhatsApp in the history: %v", hist)
	}
	if bs, _ := second["buttons"].([]any); len(bs) == 0 || bs[0].(map[string]any)["id"] != nil || bs[0].(map[string]any)["title"] == "" {
		t.Errorf("a kept button has its title, never its id: %v", second)
	}

	// On the web: the same words, the answer's buttons live.
	out := owner.must(200, "POST", "/v1/taskiem-chat", map[string]any{"text": "cash in 7,000 till"})["messages"].([]any)
	if len(out) != 2 || out[0].(map[string]any)["inbound"] != true || !strings.Contains(out[1].(map[string]any)["text"].(string), "₦7,000") {
		t.Fatalf("on the web: %v", out)
	}
	buttons := out[1].(map[string]any)["buttons"].([]any)
	yes := buttons[0].(map[string]any)
	if yes["id"] == nil || yes["id"] == "" {
		t.Fatalf("a live button: %v", buttons)
	}
	done := owner.must(200, "POST", "/v1/taskiem-chat", map[string]any{"reply": yes["id"], "text": yes["title"]})["messages"].([]any)
	if len(done) != 2 || done[0].(map[string]any)["kind"] != "button" || !strings.Contains(done[1].(map[string]any)["text"].(string), "₦7,000") {
		t.Fatalf("pressed on the web: %v", done)
	}
	var recorded int
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT count(*) FROM money_entries WHERE amount_kobo = 700000`).Scan(&recorded); err != nil || recorded != 1 {
		t.Errorf("recorded from the web: %d %v", recorded, err)
	}
	hist = owner.must(200, "GET", "/v1/taskiem-chat", nil)["messages"].([]any)
	if len(hist) != 8 || hist[4].(map[string]any)["channel"] != "web" {
		t.Fatalf("one history: %v", hist)
	}
	// Following it: what is newer than the last one seen.
	last := hist[5].(map[string]any)["created_at"].(string)
	if newer := owner.must(200, "GET", "/v1/taskiem-chat?after="+last, nil)["messages"].([]any); len(newer) != 2 {
		t.Errorf("after: %v", newer)
	}

	// Sealed: the words are not in the row.
	var plain int
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT count(*) FROM chat_history WHERE body::text LIKE '%cash in%'`).Scan(&plain); err != nil || plain != 0 {
		t.Errorf("sealed: %d %v", plain, err)
	}
	// A code is never kept.
	owner.must(200, "POST", "/v1/taskiem-chat", map[string]any{"text": "482193"})
	if got := owner.must(200, "GET", "/v1/taskiem-chat", nil)["messages"].([]any); len(got) < 8 || got[len(got)-1].(map[string]any)["text"] == "482193" {
		t.Errorf("a code kept: %v", got[len(got)-1])
	}
	for _, m := range owner.must(200, "GET", "/v1/taskiem-chat", nil)["messages"].([]any) {
		if m.(map[string]any)["text"] == "482193" {
			t.Errorf("a code kept: %v", m)
		}
	}

	// Each person reads only their own.
	viewer := addMember(t, w.world, owner, "vee", "viewer")
	if got := viewer.must(200, "GET", "/v1/taskiem-chat", nil)["messages"].([]any); len(got) != 0 {
		t.Errorf("another member's history: %v", got)
	}
	other := w.tenant(t, "Bola Builders", "bola@history.test")
	if got := other.must(200, "GET", "/v1/taskiem-chat", nil)["messages"].([]any); len(got) != 0 {
		t.Errorf("another business: %v", got)
	}
	owner.must(400, "POST", "/v1/taskiem-chat", map[string]any{"text": " "})

	// A voice note recorded on the web: transcribed and read as typed.
	w.srv.Voice = &api.VoiceSettings{Provider: &voice.Fake{Text: "status"}, Limits: voice.Limits{MaxBytes: 64 << 10, MaxDuration: 30 * time.Second}, Retention: time.Hour}
	owner.must(200, "PUT", "/v1/languages", map[string]any{"voice_notes": true})
	code, said := owner.taskiemFile(t, "voice-note.ogg", voice.TestOgg(3, 50), "")
	if code != 200 {
		t.Fatalf("a voice note: %d %v", code, said)
	}
	vm := said["messages"].([]any)
	if vm[0].(map[string]any)["kind"] != "voice" || !strings.Contains(vm[len(vm)-1].(map[string]any)["text"].(string), `I heard: "status"`) {
		t.Fatalf("heard on the web: %v", vm)
	}
	var webVoice int
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT count(*) FROM voice_transcripts WHERE channel = 'web' AND status = 'transcribed'`).Scan(&webVoice); err != nil || webVoice != 1 {
		t.Errorf("transcribed on the web: %d %v", webVoice, err)
	}
	// A photo with no caption: asked what it is, as on WhatsApp.
	code, said = owner.taskiemFile(t, "cake.jpg", photo(t), "")
	if code != 200 || !strings.Contains(jsonText(said), "caption") {
		t.Fatalf("a photo: %d %v", code, said)
	}
	if code, _ := owner.taskiemFile(t, "run.sh", []byte("#!/bin/sh\necho\n"), ""); code != 400 {
		t.Errorf("a script: %d", code)
	}

	// Past its retention: purged.
	if _, err := w.env.DB.Admin.Exec(ctx, `UPDATE chat_history SET expires_at = now() - interval '1 minute'`); err != nil {
		t.Fatal(err)
	}
	if err := w.srv.WhatsAppTick(ctx); err != nil {
		t.Fatal(err)
	}
	var left int
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT count(*) FROM chat_history`).Scan(&left); err != nil || left != 0 {
		t.Errorf("purged: %d %v", left, err)
	}
}

// The Taskiem conversation is kept under its member's own subject: one key
// for all their messages (not one per message, which grew subject_keys
// without bound), and erasing the member erases it; a customer's phone
// typed in it is erased with that customer too.
func TestTaskiemHistoryIsTheMembers(t *testing.T) {
	ctx := context.Background()
	w := newWAWorld(t)
	owner := w.tenant(t, "Ada's Kitchen", "ada@keys.test")
	keys := func() int {
		t.Helper()
		var n int
		if err := w.env.DB.Admin.QueryRow(ctx, `SELECT count(*) FROM subject_keys`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	owner.must(200, "POST", "/v1/taskiem-chat", map[string]any{"text": "hello"})
	before := keys()
	for _, m := range []string{"what can you do", "help", "status", "thanks a lot"} {
		owner.must(200, "POST", "/v1/taskiem-chat", map[string]any{"text": m})
	}
	if after := keys(); after != before {
		t.Errorf("subject keys grew with the messages: %d to %d", before, after)
	}
	owner.must(200, "POST", "/v1/taskiem-chat", map[string]any{"text": "call Bola on 08031234567 about it"})
	var tenant, user string
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT tenant_id::text, user_id::text FROM chat_history LIMIT 1`).Scan(&tenant, &user); err != nil {
		t.Fatal(err)
	}
	texts := func() []string {
		t.Helper()
		var out []string
		for _, m := range owner.must(200, "GET", "/v1/taskiem-chat", nil)["messages"].([]any) {
			if m.(map[string]any)["inbound"] == true {
				out = append(out, m.(map[string]any)["text"].(string))
			}
		}
		return out
	}
	if got := texts(); len(got) != 6 || got[5] != "call Bola on 08031234567 about it" {
		t.Fatalf("history: %v", got)
	}
	erase := func(category, value string) {
		t.Helper()
		subj, err := w.env.Vault.SubjectFor(ctx, uuidOf(t, tenant), category, value)
		if err != nil {
			t.Fatal(err)
		}
		if err := w.env.Vault.Erase(ctx, uuidOf(t, tenant), subj, "user:test"); err != nil {
			t.Fatal(err)
		}
	}
	erase("phone", "08031234567")
	if got := texts(); got[0] != "hello" || got[5] != "[erased]" {
		t.Fatalf("after erasing the customer: %v", got)
	}
	erase("member", user)
	for _, x := range texts() {
		if x != "[erased]" {
			t.Fatalf("after erasing the member: %v", texts())
		}
	}
}

// A question Taskiem asked on the web can still be answered after the page
// is reloaded or opened elsewhere: the ids of its buttons are kept for the
// step's life, only for the newest answer, only for that person.
func TestTaskiemLiveButtons(t *testing.T) {
	ctx := context.Background()
	w := newWAWorld(t)
	owner := w.tenant(t, "Ada's Kitchen", "ada@live.test")
	hist := func(c *client) []any { return c.must(200, "GET", "/v1/taskiem-chat", nil)["messages"].([]any) }
	buttonsOf := func(m any) []any { b, _ := m.(map[string]any)["buttons"].([]any); return b }

	posted := owner.must(200, "POST", "/v1/taskiem-chat", map[string]any{"text": "cash in 7,000 till"})["messages"].([]any)
	asked := posted[1].(map[string]any)

	// Reloaded: the question's buttons come with their ids.
	got := hist(owner)
	last := got[len(got)-1].(map[string]any)
	if last["id"] != asked["id"] {
		t.Fatalf("the newest message is the question: %v", last)
	}
	bs := buttonsOf(last)
	if len(bs) == 0 || bs[0].(map[string]any)["id"] == nil || bs[0].(map[string]any)["id"] == "" {
		t.Fatalf("a button of the unanswered question has its id after a reload: %v", last)
	}
	// And following it (the stream's look and ?after=).
	since := got[0].(map[string]any)["created_at"].(string)
	newer := owner.must(200, "GET", "/v1/taskiem-chat?after="+since, nil)["messages"].([]any)
	if b := buttonsOf(newer[len(newer)-1]); len(b) == 0 || b[0].(map[string]any)["id"] == nil {
		t.Errorf("following it: %v", newer[len(newer)-1])
	}
	// It is pressable from the reloaded page: the step is still there.
	yes := bs[0].(map[string]any)
	done := owner.must(200, "POST", "/v1/taskiem-chat", map[string]any{"reply": yes["id"], "text": yes["title"]})["messages"].([]any)
	if !strings.Contains(done[len(done)-1].(map[string]any)["text"].(string), "₦7,000") {
		t.Fatalf("pressed after a reload: %v", done)
	}
	var recorded int
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT count(*) FROM money_entries WHERE amount_kobo = 700000`).Scan(&recorded); err != nil || recorded != 1 {
		t.Errorf("recorded: %d %v", recorded, err)
	}

	// Answered: the buttons are by title only again, and nothing is kept.
	for _, m := range hist(owner) {
		for _, b := range buttonsOf(m) {
			if b.(map[string]any)["id"] != nil {
				t.Errorf("an answered question keeps no ids: %v", m)
			}
		}
	}
	var kept int
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT count(*) FROM chat_live_buttons`).Scan(&kept); err != nil || kept != 0 {
		t.Errorf("rows kept after the answer: %d %v", kept, err)
	}
	// While it is kept it is sealed: the ids are not in the row.
	owner.must(200, "POST", "/v1/taskiem-chat", map[string]any{"text": "cash in 5,500 till"})
	var clear int
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT count(*) FROM chat_live_buttons WHERE buttons::text LIKE '%"id"%'`).Scan(&clear); err != nil || clear != 0 {
		t.Errorf("sealed: %d %v", clear, err)
	}

	// A newer question replaces the older; anything said on the web moves the
	// step on, and the older question's buttons are no longer to be pressed.
	asked8 := owner.must(200, "POST", "/v1/taskiem-chat", map[string]any{"text": "cash in 8,000 till"})["messages"].([]any)[1].(map[string]any)
	owner.must(200, "POST", "/v1/taskiem-chat", map[string]any{"text": "status"})
	for _, m := range hist(owner) {
		if m.(map[string]any)["id"] == asked8["id"] {
			for _, b := range buttonsOf(m) {
				if b.(map[string]any)["id"] != nil {
					t.Errorf("a question the person has moved past keeps its ids: %v", m)
				}
			}
		}
	}
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT count(*) FROM chat_live_buttons`).Scan(&kept); err != nil || kept > 1 {
		t.Errorf("one row at most: %d %v", kept, err)
	}

	// Only the person's own, and only while the step lives.
	owner.must(200, "POST", "/v1/taskiem-chat", map[string]any{"text": "cash in 9,000 till"})
	viewer := addMember(t, w.world, owner, "vee", "viewer")
	if got := hist(viewer); len(got) != 0 {
		t.Errorf("another member's: %v", got)
	}
	if _, err := w.env.DB.Admin.Exec(ctx, `UPDATE chat_live_buttons SET expires_at = now() - interval '1 second'`); err != nil {
		t.Fatal(err)
	}
	got = hist(owner)
	for _, b := range buttonsOf(got[len(got)-1]) {
		if b.(map[string]any)["id"] != nil {
			t.Errorf("an expired step keeps no ids: %v", got[len(got)-1])
		}
	}
	if err := w.srv.WhatsAppTick(ctx); err != nil {
		t.Fatal(err)
	}
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT count(*) FROM chat_live_buttons`).Scan(&kept); err != nil || kept != 0 {
		t.Errorf("purged: %d %v", kept, err)
	}
}
