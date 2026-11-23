package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/israel-duff/taskiem/api"
	"github.com/israel-duff/taskiem/engine/ai"
	"github.com/israel-duff/taskiem/engine/dispatch"
	"github.com/israel-duff/taskiem/engine/lang"
	"github.com/israel-duff/taskiem/engine/reach"
	"github.com/israel-duff/taskiem/engine/riders"
	"github.com/israel-duff/taskiem/engine/storefront"
	"github.com/israel-duff/taskiem/engine/whatsapp/whatsapptest"
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
	w.srv.Dispatch = &dispatch.Dispatch{Pool: w.env.DB.App, Riders: w.srv.Riders, Orders: &storefront.Orders{Pool: w.env.DB.App, Vault: w.env.Vault}}
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

// On WhatsApp a menu is a list message (M3): its rows the topic's actions,
// chosen from the list or by a number; the topics are a list too, nine and
// More when there are more than ten; a choice with more than three choices
// is a list as well.
func TestChatMenusOnWhatsApp(t *testing.T) {
	w := newWAWorld(t)
	owner := w.tenant(t, "Ada's Kitchen", "ada@kitchen.test")
	const on = "+2348010000071"
	w.bind(t, owner, on)
	list := func(text string) whatsapptest.Sent {
		t.Helper()
		got := w.send(t, on, whatsapptest.Text(text))
		if len(got) != 1 || got[0].ListButton != "Choose" {
			t.Fatalf("%q: %+v", text, got)
		}
		return got[0]
	}
	m := list("help staff")
	if !strings.Contains(m.Text, "*Staff and tasks*") || len(m.List) != 3 || m.List[1].ID != "act:staff.give" || m.List[1].Title != "Give someone a task" ||
		m.List[1].Description != "With a time it should be done by" {
		t.Fatalf("menu: %+v", m)
	}
	// Chosen from the list.
	got := w.send(t, on, whatsapptest.ListReply("act:staff.give", "Give someone a task"))
	if len(got) != 1 || !strings.Contains(got[0].Text, "Who is it for?") {
		t.Fatalf("chosen: %+v", got)
	}
	w.say(t, on, "cancel")
	// Or by its number (it is first now: the most used come first).
	m = list("help staff")
	if m.List[0].ID != "act:staff.give" {
		t.Fatalf("most used first: %+v", m.List)
	}
	if r := w.say(t, on, "1"); !strings.Contains(r, "Who is it for?") {
		t.Fatalf("number: %q", r)
	}
	w.say(t, on, "cancel")
	// A message that is not a number lets the menu go.
	list("help staff")
	if r := w.say(t, on, "status"); !strings.Contains(r, "No runs") {
		t.Fatalf("let go: %q", r)
	}
	// Words that point to a topic: its list.
	if m := list("who is the worker on duty"); !strings.Contains(m.Text, "*Staff and tasks*") {
		t.Fatalf("words: %+v", m)
	}
	w.say(t, on, "cancel")
	// help: the topics; more than ten, nine and More, the rest behind it.
	m = list("help")
	if len(m.List) > 10 {
		t.Fatalf("topics: %d rows", len(m.List))
	}
	if n := len(m.List); n == 10 && m.List[9].ID == "topic:more" {
		rest := w.send(t, on, whatsapptest.ListReply("topic:more", "More…"))
		if len(rest) != 1 || len(rest[0].List) == 0 {
			t.Fatalf("more: %+v", rest)
		}
	}
	// help all: every command in words.
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

	// Some values: in the Taskiem chat, the action's row picked and filled
	// in, to open its form (M4).
	if m := menuOf(t, taskiemAll(t, owner, map[string]any{"text": "Musa can deliver for us"})); m["topic"] != "riders" || m["pick"] != "riders.add" ||
		m["values"].(map[string]any)["name"] != "Musa Bello" {
		t.Fatalf("some values: %v", m)
	}
	prompt := ai.PromptText(fake.Requests()[0])
	if !strings.Contains(prompt, "- riders.add: Add a rider") || !strings.Contains(prompt, "phone (phone)") || !strings.Contains(prompt, "Other commands:") {
		t.Fatalf("the catalogue was not given: %s", prompt)
	}

	// Every value: shown for a Yes, then done.
	if r := taskiemSays(t, owner, "Tunde will ride for us in Yaba, reach him on 0803 111 2222"); !strings.Contains(r, "*add rider Tunde •••2222 Yaba*") || !strings.Contains(r, "Shall I do it?") {
		t.Fatalf("every value: %q", r)
	}
	if r := taskiemSays(t, owner, "yes"); !strings.Contains(r, "Tunde is now your rider") {
		t.Fatalf("done: %q", r)
	}

	// A value of the wrong shape: left out, to be filled in.
	if m := menuOf(t, taskiemAll(t, owner, map[string]any{"text": "Kemi rides now, her line is abc"})); m["values"].(map[string]any)["phone"] != nil ||
		m["values"].(map[string]any)["name"] != "Kemi" {
		t.Fatalf("wrong shape: %v", m)
	}

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

// Forms in the app and on the web (chat-menus.md M4): the catalogue gives
// each action's fields (a label, a hint, the kind, the choices); the
// business's own choices come for its form; a form sent with every value
// does the action (its Save the yes), one missing a value says which.
func TestChatMenusForms(t *testing.T) {
	w := newWAWorld(t)
	book := &reach.Book{Pool: w.env.Store.Pool, Vault: w.env.Vault, HashKey: bytes.Repeat([]byte{8}, 32)}
	w.srv.Riders = &riders.Riders{Pool: w.env.DB.App, Vault: w.env.Vault, Book: book, PageBase: "https://" + reachFormHost}
	w.srv.Dispatch = &dispatch.Dispatch{Pool: w.env.DB.App, Riders: w.srv.Riders, Orders: &storefront.Orders{Pool: w.env.DB.App, Vault: w.env.Vault}}
	owner := w.tenant(t, "Ada's Kitchen", "ada@kitchen.test")

	var add map[string]any
	for _, x := range owner.must(200, "GET", "/v1/chat/actions", nil)["topics"].([]any) {
		for _, a := range x.(map[string]any)["actions"].([]any) {
			if a.(map[string]any)["id"] == "riders.add" {
				add = a.(map[string]any)
			}
		}
	}
	fields := add["fields"].([]any)
	phone, areas := fields[1].(map[string]any), fields[2].(map[string]any)
	if len(fields) != 3 || phone["label"] != "What's their phone number?" || phone["kind"] != "phone" || areas["label"] != "Which areas do they cover?" || areas["optional"] != true {
		t.Fatalf("fields: %v", fields)
	}

	// A value missing: said which.
	r := textOf(taskiemAll(t, owner, map[string]any{"text": "Add a rider: Musa Bello", "reply": `form:riders.add:{"name":"Musa Bello"}`}))
	if !strings.Contains(r, "Fill in: What's their phone number?") {
		t.Fatalf("missing: %q", r)
	}
	// Every value: done, with no Yes (the form's Save was it).
	r = textOf(taskiemAll(t, owner, map[string]any{"text": "Add a rider: Musa Bello, 0803 123 4567, Yaba",
		"reply": `form:riders.add:{"name":"Musa Bello","phone":"0803 123 4567","areas":"Yaba"}`}))
	if !strings.Contains(r, "Musa Bello is now your rider") {
		t.Fatalf("done: %q", r)
	}
	// The business's own choices for a form.
	ch := owner.must(200, "GET", "/v1/chat/actions/riders.give/choices", nil)["fields"].(map[string]any)
	if got := ch["rider"].([]any); len(got) != 1 || got[0] != "Musa Bello" {
		t.Fatalf("choices: %v", ch)
	}
	// Not the person's: refused.
	viewer := w.member(t, owner, "vic@kitchen.test", "viewer")
	viewer.must(404, "GET", "/v1/chat/actions/riders.give/choices", nil)
	if r := textOf(taskiemAll(t, viewer, map[string]any{"text": "x", "reply": `form:riders.add:{"name":"A","phone":"0803 123 4567"}`})); !strings.Contains(r, "isn't something you can do here") {
		t.Fatalf("viewer: %q", r)
	}
}

// A riders row is offered only where its command works (review fix): the
// deliveries need the dispatch running; without it, giving an order to a
// rider, taking it back and the day's deliveries are not in the menu,
// while the riders themselves are.
func TestChatMenusRidersNeedTheDispatch(t *testing.T) {
	w := newWAWorld(t)
	book := &reach.Book{Pool: w.env.Store.Pool, Vault: w.env.Vault, HashKey: bytes.Repeat([]byte{8}, 32)}
	w.srv.Riders = &riders.Riders{Pool: w.env.DB.App, Vault: w.env.Vault, Book: book, PageBase: "https://" + reachFormHost}
	owner := w.tenant(t, "Ada's Kitchen", "ada@kitchen.test")
	ids := map[string]bool{}
	for _, x := range owner.must(200, "GET", "/v1/chat/actions", nil)["topics"].([]any) {
		for _, a := range x.(map[string]any)["actions"].([]any) {
			ids[a.(map[string]any)["id"].(string)] = true
		}
	}
	if !ids["riders.add"] || !ids["riders.list"] || ids["riders.give"] || ids["riders.back"] || ids["riders.today"] || ids["riders.auto"] {
		t.Fatalf("riders: %v", ids)
	}
}

// "Which product?" offers the page's products (review fix), not only the
// items whose stock is counted.
func TestChatMenusProductChoices(t *testing.T) {
	w := newTablesWorld(t)
	withStock(w.world)
	owner := w.tenant(t, "Mama Put Stores", "ada@mama.test")
	owner.must(200, "POST", "/v1/stock/setup", nil)
	owner.must(201, "POST", "/v1/stock/items", map[string]any{"name": "Indomie", "quantity": 50}, "Idempotency-Key", "i1")
	ch := owner.must(200, "GET", "/v1/chat/actions/products.price/choices", nil)["fields"].(map[string]any)
	if got, _ := ch["name"].([]any); len(got) != 1 || got[0] != "Indomie" {
		t.Fatalf("choices: %v", ch)
	}
}

// How a menu ended (21 November 2026): a row pressed is what it chose, a
// menu the person passed by saying something else is passed, and its ✕
// closes one; the history says so in the menu's text, so it folds to a
// line saying what happened, and a menu that ended stays as it ended.
func TestChatMenusRememberHowTheyEnded(t *testing.T) {
	w := newWAWorld(t)
	owner := w.tenant(t, "Ada's Kitchen", "ada@kitchen.test")
	menus := func() []map[string]any {
		var out []map[string]any
		for _, x := range owner.must(200, "GET", "/v1/taskiem-chat", nil)["messages"].([]any) {
			if m := x.(map[string]any); m["kind"] == "menu" {
				var menu map[string]any
				if err := json.Unmarshal([]byte(m["text"].(string)), &menu); err != nil {
					t.Fatal(err)
				}
				menu["id"] = m["id"]
				out = append(out, menu)
			}
		}
		return out
	}

	// A row pressed: chose.
	taskiemAll(t, owner, map[string]any{"text": "help staff"})
	if ms := menus(); len(ms) != 1 || ms[0]["done"] != nil {
		t.Fatalf("an open menu: %v", ms)
	}
	taskiemAll(t, owner, map[string]any{"text": "Your tasks", "reply": "act:staff.mine"})
	if ms := menus(); ms[0]["done"] != "chose" || ms[0]["chose"] != "staff.mine" {
		t.Fatalf("chose: %v", ms[0])
	}

	// Something else said instead: passed.
	taskiemAll(t, owner, map[string]any{"text": "help"})
	taskiemAll(t, owner, map[string]any{"text": "zzq blorp"})
	ms := menus()
	if len(ms) != 3 || ms[1]["done"] != "passed" || ms[2]["done"] != nil {
		t.Fatalf("passed, and the newest open: %v", ms)
	}

	// Its ✕: closed; again changes nothing, and one that ended stays.
	owner.must(204, "POST", "/v1/taskiem-chat/menus/"+ms[2]["id"].(string)+"/close", nil)
	owner.must(204, "POST", "/v1/taskiem-chat/menus/"+ms[2]["id"].(string)+"/close", nil)
	owner.must(204, "POST", "/v1/taskiem-chat/menus/"+ms[0]["id"].(string)+"/close", nil)
	ms = menus()
	if ms[2]["done"] != "closed" || ms[0]["done"] != "chose" {
		t.Fatalf("closed: %v", ms)
	}
	// A row that opened a tool in the app: chose, said with the close.
	taskiemAll(t, owner, map[string]any{"text": "help sales"})
	ms = menus()
	owner.must(400, "POST", "/v1/taskiem-chat/menus/"+ms[3]["id"].(string)+"/close", map[string]any{"chose": "no.such"})
	owner.must(204, "POST", "/v1/taskiem-chat/menus/"+ms[3]["id"].(string)+"/close", map[string]any{"chose": "sales.sell"})
	if ms = menus(); ms[3]["done"] != "chose" || ms[3]["chose"] != "sales.sell" {
		t.Fatalf("chose with the close: %v", ms[3])
	}
	// Someone else's menu, or no menu: not found.
	other := w.tenant(t, "Bola's Bakery", "bola@bakery.test")
	other.must(404, "POST", "/v1/taskiem-chat/menus/"+ms[2]["id"].(string)+"/close", nil)
	owner.must(404, "POST", "/v1/taskiem-chat/menus/not-an-id/close", nil)
}

// A row pressed outside the Taskiem chat (menus-everywhere.md §3, E2): read
// in the Taskiem chat as there, kept in its history, the answer for a
// sheet; an action that takes values needs them; in a business's
// conversation, what Taskiem said is kept there as a note only this side
// sees, never the other's.
func TestChatActionRun(t *testing.T) {
	w := newWAWorld(t)
	seller, buyer, _, _ := linkedPair(t, w)
	run := func(c *client, status int, id string, body map[string]any) map[string]any {
		t.Helper()
		if body == nil {
			body = map[string]any{}
		}
		return c.must(status, "POST", "/v1/chat/actions/"+id+"/run", body)
	}

	// No values: as the row pressed.
	out := run(buyer, 200, "staff.mine", nil)
	said := out["messages"].([]any)
	if len(said) < 2 || said[0].(map[string]any)["inbound"] != true || said[0].(map[string]any)["text"] != "Your tasks" {
		t.Fatalf("staff.mine: %v", said)
	}
	if _, ok := out["note"]; ok {
		t.Fatalf("a note with no conversation: %v", out)
	}
	// Kept in the Taskiem chat.
	hist := buyer.must(200, "GET", "/v1/taskiem-chat", nil)["messages"].([]any)
	if !strings.Contains(fmt.Sprint(hist), "Your tasks") {
		t.Fatalf("history: %v", hist)
	}

	// Values: as its form saved; none, refused; one not its own, refused.
	run(buyer, 400, "staff.give", nil)
	run(buyer, 400, "staff.give", map[string]any{"values": map[string]any{"who": "x", "what": "y", "when": "z", "else": "w"}})
	out = run(buyer, 200, "staff.give", map[string]any{"values": map[string]any{"who": "Tunde", "what": "count the float by 7am"}})
	if txt := fmt.Sprint(out["messages"]); !strings.Contains(txt, "Give someone a task: Tunde, count the float by 7am") {
		t.Fatalf("staff.give: %v", txt)
	}

	// In a business's conversation: the note, ours only.
	link := buyer.must(200, "GET", "/v1/link-chats", nil)["conversations"].([]any)[0].(map[string]any)["link"].(string)
	out = run(buyer, 200, "staff.mine", map[string]any{"link": link})
	note := out["note"].(map[string]any)
	if note["kind"] != "taskiem" || note["asked"] != "Your tasks" || note["body"] == "" {
		t.Fatalf("the note: %v", note)
	}
	ours := buyer.must(200, "GET", "/v1/link-chats/"+link+"/messages", nil)["messages"].([]any)
	if last := ours[len(ours)-1].(map[string]any); last["id"] != note["id"] {
		t.Fatalf("our conversation: %v", last)
	}
	sl := seller.must(200, "GET", "/v1/link-chats", nil)["conversations"].([]any)[0].(map[string]any)["link"].(string)
	for _, m := range seller.must(200, "GET", "/v1/link-chats/"+sl+"/messages", nil)["messages"].([]any) {
		if m.(map[string]any)["kind"] == "taskiem" {
			t.Fatalf("the other business sees the note: %v", m)
		}
	}

	// Not an action, not theirs, not a conversation of theirs.
	run(buyer, 404, "no.such", nil)
	run(buyer, 404, "staff.mine", map[string]any{"link": "00000000-0000-4000-8000-000000000000"})
}
