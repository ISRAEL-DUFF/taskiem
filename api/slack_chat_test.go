package api_test

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/israel-duff/taskiem/connectors/paystack"
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
	if len(got) != 1 || !strings.Contains(got[0]["text"].(string), "status") {
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
