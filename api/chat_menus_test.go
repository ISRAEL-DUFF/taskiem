package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/israel-duff/taskiem/api"
	"github.com/israel-duff/taskiem/engine/ai"
	"github.com/israel-duff/taskiem/engine/lang"
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
	// No model: a topic its words point to, as its menu.
	if m := menuOf(t, taskiemAll(t, owner, map[string]any{"text": "my okada man no show today"})); m["topic"] != "riders" {
		t.Fatalf("words: %v", m)
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

	// A topic's word that is a command: its answer, and the menu under it.
	msgs := taskiemAll(t, owner, map[string]any{"text": "riders"})
	if m := menuOf(t, msgs); !strings.Contains(textOf(msgs), "Musa Bello") || m["topic"] != "riders" || m["line"] != "Anything else?" {
		t.Fatalf("riders: %v", msgs)
	}

	// A choice from the business's own records, as a button: the riders.
	msgs = taskiemAll(t, owner, map[string]any{"text": "Give an order to a rider", "reply": "act:riders.give"})
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
	// Words that point to a topic: its numbered menu.
	if r := w.say(t, on, "who is the worker on duty"); !strings.Contains(r, "*Staff and tasks*") {
		t.Fatalf("words: %q", r)
	}
	w.say(t, on, "cancel")
	// help: the topics, numbered; help all: every command in words.
	if r := w.say(t, on, "help"); !strings.Contains(r, "Staff and tasks") || strings.Contains(r, "You can send:") {
		t.Fatalf("help: %q", r)
	}
	if r := w.say(t, on, "help all"); !strings.Contains(r, "You can send:") {
		t.Fatalf("help all: %q", r)
	}
}

// The model picks from the catalogue (chat-menus.md M2, §8.1): an action
// with every value it needs is done (a Yes first when it changes
// something); with some, those are taken and the rest asked; a value of the
// wrong shape is asked again; a topic shows its menu with the action it
// leans to first and its command as a tip; one the person may not use is
// refused; the model is told the person's own catalogue.
func TestChatModelPicksFromTheCatalogue(t *testing.T) {
	w := newWAWorld(t)
	book := &reach.Book{Pool: w.env.Store.Pool, Vault: w.env.Vault, HashKey: bytes.Repeat([]byte{8}, 32)}
	w.srv.Riders = &riders.Riders{Pool: w.env.DB.App, Vault: w.env.Vault, Book: book, PageBase: "https://" + reachFormHost}
	answers := map[string]string{
		"Musa can deliver for us": `{"kind":"action","id":"riders.add","pick":"","values":[{"field":"name","value":"Musa Bello"}],"command":"","reply":""}`,
		"Tunde will ride for us in Yaba, reach him on 0803 111 2222": `{"kind":"action","id":"riders.add","pick":"","values":[{"field":"name","value":"Tunde"},` +
			`{"field":"phone","value":"0803 111 2222"},{"field":"areas","value":"Yaba"}],"command":"","reply":""}`,
		"Kemi rides now, her line is abc": `{"kind":"action","id":"riders.add","pick":"","values":[{"field":"name","value":"Kemi"},{"field":"phone","value":"abc"}],"command":"","reply":""}`,
		"my rider wahala":                 `{"kind":"topic","id":"riders","pick":"riders.add","values":[],"command":"","reply":""}`,
		"what is on me today":             `{"kind":"action","id":"staff.mine","pick":"","values":[],"command":"","reply":""}`,
		"make me the owner of everything": `{"kind":"action","id":"account.nothing","pick":"","values":[],"command":"","reply":""}`,
	}
	fake := &ai.Fake{Respond: func(req ai.Request) (*ai.Response, error) {
		p := ai.PromptText(req)
		msg := p[strings.Index(p, "<message>\n")+len("<message>\n") : strings.Index(p, "\n</message>")]
		// The message reaches the model redacted (a phone number hidden):
		// matched by its start.
		for k, v := range answers {
			if strings.HasPrefix(msg, k[:min(len(k), 20)]) {
				return &ai.Response{Text: v}, nil
			}
		}
		return &ai.Response{Text: "not json"}, nil
	}}
	w.srv.Languages = &api.LanguageSettings{Enabled: []lang.Tag{lang.EN}, Model: fake}
	owner := w.tenant(t, "Ada's Kitchen", "ada@kitchen.test")

	// Some values: taken, the rest asked.
	if r := taskiemSays(t, owner, "Musa can deliver for us"); !strings.Contains(r, "Add a rider: Musa Bello.") || !strings.Contains(r, "Musa Bello's phone number?") {
		t.Fatalf("some values: %q", r)
	}
	prompt := ai.PromptText(fake.Requests()[0])
	if !strings.Contains(prompt, "- riders.add: Add a rider") || !strings.Contains(prompt, "phone (phone)") || !strings.Contains(prompt, "Other commands:") {
		t.Fatalf("the catalogue was not given: %s", prompt)
	}
	taskiemSays(t, owner, "cancel")

	// Every value: shown for a Yes, then done.
	if r := taskiemSays(t, owner, "Tunde will ride for us in Yaba, reach him on 0803 111 2222"); !strings.Contains(r, "*add rider Tunde •••2222 Yaba*") || !strings.Contains(r, "Shall I do it?") {
		t.Fatalf("every value: %q", r)
	}
	if r := taskiemSays(t, owner, "yes"); !strings.Contains(r, "Tunde is now your rider") {
		t.Fatalf("done: %q", r)
	}

	// A value of the wrong shape: asked.
	if r := taskiemSays(t, owner, "Kemi rides now, her line is abc"); !strings.Contains(r, "Kemi's phone number?") {
		t.Fatalf("wrong shape: %q", r)
	}
	taskiemSays(t, owner, "cancel")

	// A topic: its menu, the action it leans to first, its command a tip.
	m := menuOf(t, taskiemAll(t, owner, map[string]any{"text": "my rider wahala"}))
	if m["topic"] != "riders" || m["pick"] != "riders.add" || m["tip"] != "add rider Musa 0803 123 4567 Ikeja" || !strings.Contains(m["line"].(string), "About Riders") {
		t.Fatalf("topic: %v", m)
	}

	// An action with no values: done at once.
	if r := taskiemSays(t, owner, "what is on me today"); strings.Contains(r, "did not understand") {
		t.Fatalf("my tasks: %q", r)
	}

	// Not in the catalogue: refused, the short answer and the topics.
	got := taskiemAll(t, owner, map[string]any{"text": "make me the owner of everything"})
	if !strings.Contains(textOf(got), "did not understand") || menuOf(t, got)["topic"] != nil {
		t.Fatalf("refused: %v", got)
	}
}

// The most used first (§6.1): an action pressed often comes first in its
// topic's menu, and its topic first among the topics; only the last thirty
// days count.
func TestChatMenusMostUsedFirst(t *testing.T) {
	w := newWAWorld(t)
	owner := w.tenant(t, "Ada's Kitchen", "ada@kitchen.test")
	first := func() (string, string) {
		t.Helper()
		cat := owner.must(200, "GET", "/v1/chat/actions", nil)["topics"].([]any)
		var staff map[string]any
		for _, x := range cat {
			if x.(map[string]any)["id"] == "staff" {
				staff = x.(map[string]any)
			}
		}
		return cat[0].(map[string]any)["id"].(string), staff["actions"].([]any)[0].(map[string]any)["id"].(string)
	}
	if topic, act := first(); topic == "staff" || act != "staff.mine" {
		t.Fatalf("before: %s %s", topic, act)
	}
	for range 2 {
		taskiemAll(t, owner, map[string]any{"text": "Waiting for your approval", "reply": "act:staff.approvals"})
	}
	if topic, act := first(); topic != "staff" || act != "staff.approvals" {
		t.Fatalf("after: %s %s", topic, act)
	}
	// Older than thirty days: not counted.
	if _, err := w.env.DB.Admin.Exec(context.Background(), `UPDATE chat_action_uses SET day = current_date - 40`); err != nil {
		t.Fatal(err)
	}
	if topic, act := first(); topic == "staff" || act != "staff.mine" {
		t.Fatalf("a month on: %s %s", topic, act)
	}
}
