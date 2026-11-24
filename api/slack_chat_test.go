package api_test

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/israel-duff/taskiem/api"
	"github.com/israel-duff/taskiem/connectors/paystack"
	"github.com/israel-duff/taskiem/engine/ai"
	"github.com/israel-duff/taskiem/engine/lang"
)

// event posts an Events API delivery as Slack does, signed with secret,
// and returns the status and body.
func (w *slackWorld) event(t *testing.T, body map[string]any, secret string) (int, string) {
	t.Helper()
	raw, _ := json.Marshal(body)
	ts := strconv.FormatInt(time.Now().Unix(), 10)
	m := hmac.New(sha256.New, []byte(secret))
	m.Write([]byte("v0:" + ts + ":" + string(raw)))
	req, _ := http.NewRequest("POST", strings.Replace(w.hook, "/interactions", "/events", 1), strings.NewReader(string(raw)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Slack-Request-Timestamp", ts)
	req.Header.Set("X-Slack-Signature", "v0="+hex.EncodeToString(m.Sum(nil)))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	out, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	w.srv.WaitBackground()
	return resp.StatusCode, string(out)
}

// dm is a direct message from a Slack user to the bot.
func dm(id, user, text string) map[string]any {
	return map[string]any{"type": "event_callback", "team_id": "T0ACME", "event_id": id,
		"event": map[string]any{"type": "message", "channel_type": "im", "user": user, "text": text, "channel": "D0" + user}}
}

// A DM to Taskiem's Slack bot from a linked member is the conversation
// WhatsApp and Telegram have; Slack's URL check is answered, a retried
// event is handled once, a stranger or the bot itself gets nothing, and
// any other event goes on to the slack@1 triggers.
func TestSlackConversation(t *testing.T) {
	w := newSlackWorld(t)
	owner := w.tenant(t, "Acme", "owner@acme.test")
	owner.must(201, "POST", "/v1/connections", map[string]any{"connector": "slack@1", "environment": "prod", "credentials": map[string]string{"bot_token": "xoxb-acme"}})
	w.fake.users["owner@acme.test"] = "U0OWNER"
	w.link(t, owner, "U0OWNER")

	if st, body := w.event(t, map[string]any{"type": "url_verification", "challenge": "3eZbrw1aBm2rZgRNFdxV2595E9CY3gmdALWMmHkvFXO7tYXAYM8P"}, slackSecret); st != 200 || body != "3eZbrw1aBm2rZgRNFdxV2595E9CY3gmdALWMmHkvFXO7tYXAYM8P" {
		t.Fatalf("url check: %d %q", st, body)
	}
	if st, _ := w.event(t, dm("Ev1", "U0OWNER", "help"), "not-the-secret"); st != 401 {
		t.Fatalf("a wrong signature: %d", st)
	}
	before := len(w.fake.to("U0OWNER"))
	if st, _ := w.event(t, dm("Ev1", "U0OWNER", "help"), slackSecret); st != 200 {
		t.Fatalf("dm: %d", st)
	}
	got := w.fake.to("U0OWNER")[before:]
	if len(got) != 1 || !strings.Contains(got[0]["text"].(string), "Pick a topic") || !strings.Contains(fmt.Sprint(got[0]["blocks"]), "value:topic:sales") || !strings.Contains(fmt.Sprint(got[0]["blocks"]), "value:topic::more@5") {
		t.Fatalf("help: %v", got)
	}
	// Slack retried it: nothing twice.
	w.event(t, dm("Ev1", "U0OWNER", "help"), slackSecret)
	if again := w.fake.to("U0OWNER")[before:]; len(again) != 1 {
		t.Fatalf("a retry answered: %d", len(again))
	}
	// A conversation's buttons are the person's answers: start a template.
	before = len(w.fake.to("U0OWNER"))
	if st := w.click(t, "U0OWNER", "taskiem.reply.0", "su:tpl:daily-reminder", slackSecret); st != 200 {
		t.Fatalf("a reply button: %d", st)
	}
	if got := w.fake.to("U0OWNER")[before:]; len(got) != 1 || !strings.Contains(got[0]["text"].(string), "What to remind you of") {
		t.Fatalf("the template: %v", got)
	}
	// Someone not linked, and the bot's own message, get nothing.
	before = len(w.fake.posts)
	w.event(t, dm("Ev2", "U0STRANGER", "help"), slackSecret)
	w.event(t, map[string]any{"type": "event_callback", "team_id": "T0ACME", "event_id": "Ev3",
		"event": map[string]any{"type": "message", "channel_type": "im", "bot_id": "B0BOT", "text": "hi"}}, slackSecret)
	if len(w.fake.posts) != before {
		t.Fatalf("answered a stranger or the bot: %v", w.fake.posts[before:])
	}
	// A channel message is a workflow's (no ingest here: acknowledged).
	if st, _ := w.event(t, map[string]any{"type": "event_callback", "team_id": "T0ACME", "event_id": "Ev4",
		"event": map[string]any{"type": "message", "channel_type": "channel", "user": "U0OWNER", "text": "hello"}}, slackSecret); st != 200 {
		t.Fatalf("a channel message: %d", st)
	}
}

// linkURL is a Slack message's link button, if any.
func linkURL(m map[string]any) string {
	blocks, _ := m["blocks"].([]any)
	for _, b := range blocks {
		bm, _ := b.(map[string]any)
		els, _ := bm["elements"].([]any)
		for _, e := range els {
			if u, ok := e.(map[string]any)["url"].(string); ok {
				return u
			}
		}
	}
	return ""
}

// Connecting from a Slack DM: the connect link comes as a button, and the
// chat is told when it is used.
func TestSlackConnect(t *testing.T) {
	w := newSlackWorld(t)
	ps := fakePaystack(t, "sk_test_good0000000000")
	if err := w.env.Registry.Register(paystack.New(paystack.Options{BaseURL: ps.URL})); err != nil {
		t.Fatal(err)
	}
	owner := w.tenant(t, "Acme", "owner@acme.test")
	owner.must(201, "POST", "/v1/connections", map[string]any{"connector": "slack@1", "environment": "prod", "credentials": map[string]string{"bot_token": "xoxb-acme"}})
	w.fake.users["owner@acme.test"] = "U0OWNER"
	w.link(t, owner, "U0OWNER")
	before := len(w.fake.to("U0OWNER"))
	w.event(t, dm("Ev10", "U0OWNER", "connect paystack"), slackSecret)
	got := w.fake.to("U0OWNER")[before:]
	if len(got) != 1 {
		t.Fatalf("link: %v", got)
	}
	m := connectLinkRe.FindStringSubmatch(linkURL(got[0]))
	if m == nil {
		t.Fatalf("no link button: %v", got[0])
	}
	before = len(w.fake.to("U0OWNER"))
	if st, out := connectPage(t, w.world, "POST", "/v1/connect/"+m[1], map[string]any{"credentials": map[string]string{"secret_key": "sk_test_good0000000000"}}); st != 201 {
		t.Fatalf("connected: %d %v", st, out)
	}
	w.srv.WaitBackground()
	if told := w.fake.to("U0OWNER")[before:]; len(told) != 1 || !strings.Contains(told[0]["text"].(string), "Paystack is connected") {
		t.Fatalf("told: %v", told)
	}
}

// Slack's menus (chat-menus.md §9): five buttons at most, then More…,
// which shows the next five; for the topics and for a topic's rows; a
// number picks among the buttons as sent.
func TestSlackMenuFiveThenMore(t *testing.T) {
	w := newSlackWorld(t)
	owner := w.tenant(t, "Acme", "owner@acme.test")
	owner.must(201, "POST", "/v1/connections", map[string]any{"connector": "slack@1", "environment": "prod", "credentials": map[string]string{"bot_token": "xoxb-acme"}})
	w.fake.users["owner@acme.test"] = "U0OWNER"
	w.link(t, owner, "U0OWNER")
	// buttons are the last message's buttons: their words and values.
	buttons := func() (titles, values []string) {
		t.Helper()
		got := w.fake.to("U0OWNER")
		blocks, _ := got[len(got)-1]["blocks"].([]any)
		for _, b := range blocks {
			if els, ok := b.(map[string]any)["elements"].([]any); ok {
				for _, e := range els {
					el := e.(map[string]any)
					titles = append(titles, el["text"].(map[string]any)["text"].(string))
					values = append(values, fmt.Sprint(el["value"]))
				}
			}
		}
		return titles, values
	}
	w.event(t, dm("Ev10", "U0OWNER", "help"), slackSecret)
	titles, values := buttons()
	if len(titles) != 6 || titles[5] != "More…" || values[5] != "topic::more@5" {
		t.Fatalf("topics: %v %v", titles, values)
	}
	w.click(t, "U0OWNER", "taskiem.reply.5", values[5], slackSecret)
	next, nextValues := buttons()
	if len(next) != 6 || next[0] == titles[0] || nextValues[5] != "topic::more@10" {
		t.Fatalf("the next five topics: %v %v", next, nextValues)
	}
	// A topic of seven: five, then More…, then the last two.
	w.event(t, dm("Ev11", "U0OWNER", "help saved"), slackSecret)
	titles, values = buttons()
	if len(titles) != 6 || titles[5] != "More…" || values[5] != "topic:saved:more@5" {
		t.Fatalf("a topic: %v %v", titles, values)
	}
	w.click(t, "U0OWNER", "taskiem.reply.5", values[5], slackSecret)
	rest, restValues := buttons()
	if len(rest) != 2 || !strings.HasPrefix(restValues[0], "act:saved.") {
		t.Fatalf("the rest: %v %v", rest, restValues)
	}
	// More… by its number.
	w.event(t, dm("Ev12", "U0OWNER", "help saved"), slackSecret)
	w.event(t, dm("Ev13", "U0OWNER", "6"), slackSecret)
	if again, _ := buttons(); len(again) != 2 || again[0] != rest[0] {
		t.Fatalf("More… by its number: %v", again)
	}
}

// A topic's More… on Slack after the model picked a row (review of H4):
// the picked row first, then the next page goes on from there, so no row
// is skipped and none shown twice; the same by its number.
func TestSlackMenuMoreKeepsThePick(t *testing.T) {
	w := newSlackWorld(t)
	w.srv.Languages = &api.LanguageSettings{Enabled: []lang.Tag{lang.EN}, Model: &ai.Fake{Respond: func(req ai.Request) (*ai.Response, error) {
		return &ai.Response{Text: `{"kind":"topic","id":"saved","pick":"saved.import","values":[],"command":"","reply":""}`}, nil
	}}}
	owner := w.tenant(t, "Acme", "owner@acme.test")
	owner.must(201, "POST", "/v1/connections", map[string]any{"connector": "slack@1", "environment": "prod", "credentials": map[string]string{"bot_token": "xoxb-acme"}})
	w.fake.users["owner@acme.test"] = "U0OWNER"
	w.link(t, owner, "U0OWNER")
	values := func() []string {
		t.Helper()
		got := w.fake.to("U0OWNER")
		blocks, _ := got[len(got)-1]["blocks"].([]any)
		var out []string
		for _, b := range blocks {
			if els, ok := b.(map[string]any)["elements"].([]any); ok {
				for _, e := range els {
					out = append(out, fmt.Sprint(e.(map[string]any)["value"]))
				}
			}
		}
		return out
	}
	for i, more := range []func(more string){
		func(more string) { w.click(t, "U0OWNER", "taskiem.reply.5", more, slackSecret) },
		func(string) { w.event(t, dm("Ev31", "U0OWNER", "6"), slackSecret) },
	} {
		w.event(t, dm(fmt.Sprintf("Ev2%d", i), "U0OWNER", "my saved stuff is a mess"), slackSecret)
		first := values()
		if len(first) != 6 || first[0] != "act:saved.import" || first[5] != "topic:saved:more@5~saved.import" {
			t.Fatalf("the first page: %v", first)
		}
		more(first[5])
		rest := values()
		seen := map[string]bool{}
		for _, v := range append(first[:5], rest...) {
			if seen[v] {
				t.Fatalf("%s twice: %v then %v", v, first, rest)
			}
			seen[v] = true
		}
		if len(rest) != 2 || len(seen) != 7 {
			t.Fatalf("a row skipped: %v then %v", first, rest)
		}
	}
}
