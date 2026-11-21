package api_test

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/israel-duff/taskiem/engine/reach"
	"github.com/israel-duff/taskiem/engine/riders"
)

// taskiemAll posts to the Taskiem chat (text, or a press's reply) and
// gives every message Taskiem answered.
func taskiemAll(t *testing.T, c *client, body map[string]any) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, x := range c.must(200, "POST", "/v1/taskiem-chat", body)["messages"].([]any) {
		if m := x.(map[string]any); m["inbound"] == false {
			out = append(out, m)
		}
	}
	return out
}

// menuOf is the menu among the answer's messages.
func menuOf(t *testing.T, msgs []map[string]any) map[string]any {
	t.Helper()
	for _, m := range msgs {
		if m["kind"] == "menu" {
			var menu map[string]any
			if err := json.Unmarshal([]byte(m["text"].(string)), &menu); err != nil {
				t.Fatal(err)
			}
			return menu
		}
	}
	t.Fatalf("no menu in %v", msgs)
	return nil
}

func textOf(msgs []map[string]any) string {
	var b strings.Builder
	for _, m := range msgs {
		if m["kind"] != "menu" {
			b.WriteString(m["text"].(string) + "\n")
		}
	}
	return b.String()
}

// Menus in the Taskiem chat (docs/spec/chat-menus.md, M1): help and a
// message nothing understood show the topics; "help riders" and a topic's
// word show its menu; a row pressed shows its answer, or asks step by
// step, choices offered, an answer checked, and does it on Yes; the
// catalogue is the person's own; the kept history keeps the menu.
func TestChatMenusInTheTaskiemChat(t *testing.T) {
	w := newWAWorld(t)
	book := &reach.Book{Pool: w.env.Store.Pool, Vault: w.env.Vault, HashKey: bytes.Repeat([]byte{8}, 32)}
	w.srv.Riders = &riders.Riders{Pool: w.env.DB.App, Vault: w.env.Vault, Book: book, PageBase: "https://" + reachFormHost}
	owner := w.tenant(t, "Ada's Kitchen", "ada@kitchen.test")

	// The catalogue: the person's own topics, each with its actions.
	cat := owner.must(200, "GET", "/v1/chat/actions", nil)["topics"].([]any)
	ids := map[string]bool{}
	for _, x := range cat {
		ids[x.(map[string]any)["id"].(string)] = true
	}
	if !ids["riders"] || !ids["staff"] {
		t.Fatalf("topics: %v", ids)
	}

	// help: the topics.
	if m := menuOf(t, taskiemAll(t, owner, map[string]any{"text": "help"})); m["topic"] != nil {
		t.Fatalf("help: %v", m)
	}
	// help all: every command, in words.
	if r := taskiemSays(t, owner, "help all"); !strings.Contains(r, "*status*") {
		t.Fatalf("help all: %q", r)
	}
	// A topic by name, and its word alone when no command reads it.
	if m := menuOf(t, taskiemAll(t, owner, map[string]any{"text": "help riders"})); m["topic"] != "riders" {
		t.Fatalf("help riders: %v", m)
	}
	if m := menuOf(t, taskiemAll(t, owner, map[string]any{"text": "staff"})); m["topic"] != "staff" {
		t.Fatalf("staff: %v", m)
	}
	// Not understood: the short answer, and the topics.
	got := taskiemAll(t, owner, map[string]any{"text": "zzq blorp"})
	if !strings.Contains(textOf(got), "did not understand") || menuOf(t, got)["topic"] != nil {
		t.Fatalf("not understood: %v", got)
	}

	// A row that shows something: its answer.
	if r := textOf(taskiemAll(t, owner, map[string]any{"text": "Your riders", "reply": "act:riders.list"})); !strings.Contains(r, "rider") {
		t.Fatalf("riders: %q", r)
	}

	// A row that asks step by step: a question, a wrong answer said why,
	// then Yes does it.
	if r := textOf(taskiemAll(t, owner, map[string]any{"text": "Add a rider", "reply": "act:riders.add"})); !strings.Contains(r, "rider's name") {
		t.Fatalf("first question: %q", r)
	}
	if r := taskiemSays(t, owner, "Musa Bello"); !strings.Contains(r, "Musa Bello's phone number") {
		t.Fatalf("second question: %q", r)
	}
	if r := taskiemSays(t, owner, "not a phone"); !strings.Contains(r, "doesn't look like a phone number") {
		t.Fatalf("wrong answer: %q", r)
	}
	taskiemSays(t, owner, "0803 123 4567")
	if r := taskiemSays(t, owner, "Yaba"); !strings.Contains(r, "*add rider Musa Bello •••4567 Yaba*") || !strings.Contains(r, "Shall I do it?") {
		t.Fatalf("shown for a Yes: %q", r)
	}
	if r := taskiemSays(t, owner, "yes"); !strings.Contains(r, "Musa Bello is now your rider") {
		t.Fatalf("done: %q", r)
	}

	// A choice from the business's own records, as a button: the riders.
	msgs := taskiemAll(t, owner, map[string]any{"text": "Give an order to a rider", "reply": "act:riders.give"})
	if r := textOf(msgs); !strings.Contains(r, "Which order?") {
		t.Fatalf("order question: %q", r)
	}
	msgs = taskiemAll(t, owner, map[string]any{"text": "TK-A1B2C3"})
	var offered []string
	for _, b := range msgs[0]["buttons"].([]any) {
		offered = append(offered, b.(map[string]any)["id"].(string))
	}
	if r := textOf(msgs); !strings.Contains(r, "Which rider?") || len(offered) != 1 || offered[0] != "ans:Musa Bello" {
		t.Fatalf("rider question: %q %v", r, offered)
	}
	if r := textOf(taskiemAll(t, owner, map[string]any{"text": "Musa Bello", "reply": "ans:Musa Bello"})); !strings.Contains(r, "*order tk-a1b2c3 to Musa Bello*") && !strings.Contains(r, "*order TK-A1B2C3 to Musa Bello*") {
		t.Fatalf("a choice pressed: %q", r)
	}
	if r := taskiemSays(t, owner, "no"); !strings.Contains(r, "OK, I left it.") {
		t.Fatalf("no: %q", r)
	}
	// cancel at any time.
	taskiemAll(t, owner, map[string]any{"text": "Add a rider", "reply": "act:riders.add"})
	if r := taskiemSays(t, owner, "cancel"); !strings.Contains(strings.ToLower(r), "cancel") {
		t.Fatalf("cancel: %q", r)
	}

	// The history keeps the menu, as a menu.
	found := false
	for _, x := range owner.must(200, "GET", "/v1/taskiem-chat", nil)["messages"].([]any) {
		if x.(map[string]any)["kind"] == "menu" {
			found = true
		}
	}
	if !found {
		t.Fatal("no menu kept")
	}

	// Not the person's to use: refused.
	viewer := w.member(t, owner, "vic@kitchen.test", "viewer")
	if r := textOf(taskiemAll(t, viewer, map[string]any{"text": "Add a rider", "reply": "act:riders.add"})); !strings.Contains(r, "isn't something you can do here") {
		t.Fatalf("viewer: %q", r)
	}
}

// On WhatsApp "help staff" is a numbered menu, answered by a number.
func TestChatMenusNumbered(t *testing.T) {
	w := newWAWorld(t)
	owner := w.tenant(t, "Ada's Kitchen", "ada@kitchen.test")
	const on = "+2348010000071"
	w.bind(t, owner, on)
	r := w.say(t, on, "help staff")
	if !strings.Contains(r, "*Staff and tasks*") || !strings.Contains(r, "2. Give someone a task") {
		t.Fatalf("menu: %q", r)
	}
	if r := w.say(t, on, "2"); !strings.Contains(r, "Who is it for?") {
		t.Fatalf("number: %q", r)
	}
	w.say(t, on, "cancel")
	// A message that is not a number lets the menu go.
	w.say(t, on, "help staff")
	if r := w.say(t, on, "status"); !strings.Contains(r, "No runs") {
		t.Fatalf("let go: %q", r)
	}
	// help on WhatsApp stays the list in words until its menus come (M3).
	if r := w.say(t, on, "help"); !strings.Contains(r, "You can send:") {
		t.Fatalf("help: %q", r)
	}
}
