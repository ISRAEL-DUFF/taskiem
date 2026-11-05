package api_test

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/israel-duff/taskiem/api"
	"github.com/israel-duff/taskiem/connectors/slack"
	"github.com/israel-duff/taskiem/engine/alerts"
	rt "github.com/israel-duff/taskiem/engine/runtime/runtimetest"
	"github.com/israel-duff/taskiem/engine/whatsapp"
)

const slackSecret = "8f742231b10e8888abcd99yyyzzz85a5"

// slackFake is Slack's Web API for one workspace: people by email, and
// every message the bot posts.
type slackFake struct {
	*httptest.Server
	mu    sync.Mutex
	users map[string]string // email -> user id
	posts []map[string]any
	// install is oauth.v2.access's answer to the code "good-code"
	// (slack_install_test.go); revoked the tokens given back; people the
	// users users.info knows (real_name, email).
	install map[string]any
	revoked []string
	people  map[string][2]string
}

func newSlackFake(t *testing.T) *slackFake {
	f := &slackFake{users: map[string]string{}, people: map[string][2]string{}}
	f.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/oauth.v2.access":
			form, _ := url.ParseQuery(string(raw))
			f.mu.Lock()
			defer f.mu.Unlock()
			if form.Get("code") != "good-code" || form.Get("code_verifier") == "" || form.Get("client_secret") != "slack-secret" || f.install == nil {
				_, _ = w.Write([]byte(`{"ok":false,"error":"invalid_code"}`))
				return
			}
			_ = json.NewEncoder(w).Encode(f.install)
			return
		case "/auth.revoke":
			form, _ := url.ParseQuery(string(raw))
			f.mu.Lock()
			f.revoked = append(f.revoked, form.Get("token"))
			f.mu.Unlock()
			_, _ = w.Write([]byte(`{"ok":true,"revoked":true}`))
			return
		}
		if r.Header.Get("Authorization") != "Bearer xoxb-acme" {
			_, _ = w.Write([]byte(`{"ok":false,"error":"invalid_auth"}`))
			return
		}
		f.mu.Lock()
		defer f.mu.Unlock()
		switch r.URL.Path {
		case "/users.info":
			form, _ := url.ParseQuery(string(raw))
			p, ok := f.people[form.Get("user")]
			if !ok {
				_, _ = w.Write([]byte(`{"ok":false,"error":"user_not_found"}`))
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "user": map[string]any{"id": form.Get("user"), "team_id": "T0ACME", "name": "x", "real_name": p[0],
				"profile": map[string]any{"real_name": p[0], "email": p[1]}}})
		case "/auth.test":
			_, _ = w.Write([]byte(`{"ok":true,"team":"Acme","team_id":"T0ACME","user":"taskiem","user_id":"U0BOT","bot_id":"B0BOT"}`))
		case "/users.lookupByEmail":
			form, _ := url.ParseQuery(string(raw))
			id, ok := f.users[form.Get("email")]
			if !ok {
				_, _ = w.Write([]byte(`{"ok":false,"error":"users_not_found"}`))
				return
			}
			_, _ = w.Write([]byte(`{"ok":true,"user":{"id":"` + id + `","team_id":"T0ACME","name":"x","profile":{}}}`))
		case "/chat.postMessage":
			var m map[string]any
			_ = json.Unmarshal(raw, &m)
			f.posts = append(f.posts, m)
			_, _ = w.Write([]byte(`{"ok":true,"channel":"D` + m["channel"].(string)[1:] + `","ts":"1700000000.` + strconv.Itoa(len(f.posts)) + `"}`))
		default:
			_, _ = w.Write([]byte(`{"ok":false,"error":"unknown_method"}`))
		}
	}))
	t.Cleanup(f.Close)
	return f
}

// to are the messages posted to a Slack user.
func (f *slackFake) to(user string) []map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []map[string]any
	for _, m := range f.posts {
		if m["channel"] == user {
			out = append(out, m)
		}
	}
	return out
}

// buttons are a message's button values.
func buttons(m map[string]any) []string {
	var out []string
	blocks, _ := m["blocks"].([]any)
	for _, b := range blocks {
		bm, _ := b.(map[string]any)
		els, _ := bm["elements"].([]any)
		for _, e := range els {
			em, _ := e.(map[string]any)
			if v, ok := em["value"].(string); ok {
				out = append(out, v)
			}
		}
	}
	return out
}

type slackWorld struct {
	*world
	fake *slackFake
	hook string
}

func newSlackWorld(t *testing.T) *slackWorld {
	e := rt.New(t)
	fake := newSlackFake(t)
	if err := e.Registry.Register(slack.New(slack.Options{BaseURL: fake.URL})); err != nil {
		t.Fatal(err)
	}
	srv := &api.Server{Store: e.Store, Vault: e.Vault, Registry: e.Registry, Connectors: e.Connectors, AllowSignup: true, Egress: e.Egress,
		Logger: slog.New(slog.DiscardHandler), Slack: &api.SlackApp{SigningSecret: slackSecret}, ChatSigner: whatsapp.Signer{Key: waKey}, PublicURL: waPublicURL}
	mux := http.NewServeMux()
	mux.HandleFunc("/channels/slack/interactions", srv.SlackHooks().ServeHTTP)
	mux.HandleFunc("/channels/slack/events", srv.SlackEvents().ServeHTTP)
	mux.Handle("/", specCheck(t, srv.Handler()))
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	return &slackWorld{world: &world{env: e, base: ts.URL, srv: srv}, fake: fake, hook: ts.URL + "/channels/slack/interactions"}
}

// click posts a button press as Slack does: a form whose payload is JSON,
// signed with the app's signing secret.
func (w *slackWorld) click(t *testing.T, user, actionID, value, secret string) int {
	t.Helper()
	pl, _ := json.Marshal(map[string]any{"type": "block_actions", "team": map[string]any{"id": "T0ACME"}, "user": map[string]any{"id": user},
		"actions": []any{map[string]any{"action_id": actionID, "value": value}}})
	body := "payload=" + url.QueryEscape(string(pl))
	ts := strconv.FormatInt(time.Now().Unix(), 10)
	m := hmac.New(sha256.New, []byte(secret))
	m.Write([]byte("v0:" + ts + ":" + body))
	req, _ := http.NewRequest("POST", w.hook, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("X-Slack-Request-Timestamp", ts)
	req.Header.Set("X-Slack-Signature", "v0="+hex.EncodeToString(m.Sum(nil)))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	w.srv.WaitBackground()
	return resp.StatusCode
}

func (w *slackWorld) link(t *testing.T, c *client, user string) {
	t.Helper()
	c.must(201, "POST", "/v1/me/slack", map[string]any{"password": testPassword})
	sent := w.fake.to(user)
	if len(sent) == 0 || len(buttons(sent[len(sent)-1])) != 1 || !strings.HasPrefix(buttons(sent[len(sent)-1])[0], "taskiem-link:") {
		t.Fatalf("no Confirm sent to %s: %v", user, sent)
	}
	if st := w.click(t, user, "taskiem.link.0", buttons(sent[len(sent)-1])[0], slackSecret); st != 200 {
		t.Fatalf("confirm: %d", st)
	}
	if info := c.must(200, "GET", "/v1/me/slack", nil); info["linked"] != true || info["workspace"] != "T0ACME" {
		t.Fatalf("after confirm: %v", info)
	}
}

func TestSlackApprovals(t *testing.T) {
	w := newSlackWorld(t)
	owner := w.tenant(t, "Acme", "owner@acme.test")
	owner.must(201, "POST", "/v1/connections", map[string]any{"connector": "slack@1", "environment": "prod", "credentials": map[string]string{"bot_token": "xoxb-acme"}})
	owner.must(201, "POST", "/v1/members", map[string]any{"email": "owner@acme.test", "roles": []string{"credit_officer", "approver"}})
	member := func(email string) *client {
		owner.must(201, "POST", "/v1/members", map[string]any{"email": email, "password": testPassword, "roles": []string{"approver", "credit_officer"}})
		return w.login(t, email, testPassword)
	}
	alice, bob := member("alice@acme.test"), member("bob@acme.test")
	w.fake.users["alice@acme.test"], w.fake.users["bob@acme.test"] = "U0ALICE", "U0BOB"

	// No one in the workspace with the owner's email.
	if st, out := owner.do("POST", "/v1/me/slack", map[string]any{"password": testPassword}); st != 409 || !strings.Contains(toJSON(out), "no one in your Slack workspace has the email owner@acme.test") {
		t.Errorf("an email not in Slack: %d %v", st, out)
	}
	owner.must(403, "POST", "/v1/me/slack", map[string]any{})
	w.link(t, alice, "U0ALICE")
	w.link(t, bob, "U0BOB")
	// A stale or foreign Confirm links no one.
	if st := w.click(t, "U0EVE", "taskiem.link.0", "taskiem-link:forged", slackSecret); st != 200 {
		t.Errorf("a forged confirm: %d", st)
	}
	if st := w.click(t, "U0ALICE", "taskiem.link.0", "x", "not-the-secret"); st != 401 {
		t.Errorf("a wrong signature: %d", st)
	}

	wf := publishFlow(t, owner, waLoanFlow)
	run := owner.must(201, "POST", "/v1/workflows/"+wf+"/runs", map[string]any{"input": map[string]any{"bvn": "22212345678", "amount": 5000, "account_number": "0123456789"}})["run_id"].(string)
	if err := w.srv.WhatsAppTick(t.Context()); err != nil {
		t.Fatal(err)
	}
	req := w.fake.to("U0ALICE")
	am := req[len(req)-1]
	text, _ := am["text"].(string)
	if len(buttons(am)) != 2 || !strings.HasPrefix(text, "Approval needed: loan · ok") || !strings.HasPrefix(buttons(am)[0], "tk1.") {
		t.Fatalf("alice's request: %v", am)
	}
	if strings.Contains(text, "22212345678") || strings.Contains(text, "0123456789") {
		t.Errorf("the request shows personal data: %q", text)
	}
	last := func(user string) string {
		got := w.fake.to(user)
		s, _ := got[len(got)-1]["text"].(string)
		return s
	}
	// Alice's button pressed by Bob decides nothing.
	w.click(t, "U0BOB", "taskiem.decide.0", buttons(am)[0], slackSecret)
	if r := last("U0BOB"); !strings.Contains(r, "sent to someone else") {
		t.Errorf("another person's button: %q", r)
	}
	// Someone in the workspace who never linked.
	w.click(t, "U0EVE", "taskiem.decide.0", buttons(am)[0], slackSecret)
	if got := w.fake.to("U0EVE"); len(got) != 0 {
		t.Errorf("an unlinked person was answered in a DM: %v", got)
	}
	w.click(t, "U0ALICE", "taskiem.decide.0", buttons(am)[0], slackSecret)
	if r := last("U0ALICE"); !strings.Contains(r, "Recorded: you approved ok") {
		t.Fatalf("alice approves: %q", r)
	}
	w.click(t, "U0ALICE", "taskiem.decide.1", buttons(am)[1], slackSecret)
	if r := last("U0ALICE"); !strings.Contains(r, "already answered") {
		t.Errorf("the other button of the pair: %q", r)
	}
	bobReq := w.fake.to("U0BOB")
	var bm map[string]any
	for _, m := range bobReq {
		if len(buttons(m)) == 2 {
			bm = m
		}
	}
	w.click(t, "U0BOB", "taskiem.decide.0", buttons(bm)[0], slackSecret)
	if r := last("U0BOB"); !strings.Contains(r, "request is approved") {
		t.Fatalf("bob approves: %q", r)
	}
	w.env.Drain(t)
	res := owner.must(200, "GET", "/v1/runs/"+run, nil)
	if st := res["run"].(map[string]any)["status"]; st != "completed" || !strings.Contains(toJSON(res["events"]), `"channel":"slack"`) {
		t.Fatalf("run: %v, decided on slack: %v", st, strings.Contains(toJSON(res["events"]), `"channel":"slack"`))
	}
	// Unlinking stops the requests.
	alice.must(204, "DELETE", "/v1/me/slack", nil)
	if info := alice.must(200, "GET", "/v1/me/slack", nil); info["linked"] != false {
		t.Errorf("after unlinking: %v", info)
	}
}

// A click on a workflow's own button is not Taskiem's to decide: it goes on
// to the slack@1 interactions trigger of Taskiem's app, body and signature
// as they came, where it is verified again and routed by workspace.
func TestSlackClicksForWorkflowsGoOnToTheTrigger(t *testing.T) {
	w := newSlackWorld(t)
	var got struct {
		path, sig string
		body      []byte
	}
	w.srv.Ingest = http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		got.path, got.sig = r.URL.Path, r.Header.Get("X-Slack-Signature")
		got.body, _ = io.ReadAll(r.Body)
		rw.WriteHeader(http.StatusOK)
	})
	if st := w.click(t, "U0ALICE", "approve_order", "taskiem-corr:order-42|yes", slackSecret); st != 200 {
		t.Fatalf("status %d", st)
	}
	if got.path != "/apps/slack@1/interactions" || !strings.HasPrefix(got.sig, "v0=") || !strings.Contains(string(got.body), "taskiem-corr%3Aorder-42") {
		t.Errorf("forwarded: %q %q %q", got.path, got.sig, got.body)
	}
}

// An alert channel made from the Slack connection: its bot posts in the
// channel picked, with nothing pasted (K2).
func TestSlackAlertChannelFromTheConnection(t *testing.T) {
	w := newSlackWorld(t)
	w.srv.Alerts = &alerts.Alerter{Pool: w.env.Store.Pool, Secrets: w.env.Vault, Egress: w.env.Egress, PublicURL: "https://taskiem.test",
		Slack: &api.SlackAlerts{Server: w.srv}}
	owner := w.tenant(t, "Acme", "owner@acme.test")
	owner.must(400, "POST", "/v1/alerts/channels", map[string]any{"kind": "slack", "name": "#payments", "connection": "main", "channel": "C0PAY"})
	owner.must(201, "POST", "/v1/connections", map[string]any{"connector": "slack@1", "environment": "prod", "credentials": map[string]string{"bot_token": "xoxb-acme"}})
	owner.must(400, "POST", "/v1/alerts/channels", map[string]any{"kind": "slack", "name": "#payments", "connection": "main", "channel": "#payments"})
	ch := owner.must(201, "POST", "/v1/alerts/channels", map[string]any{"kind": "slack", "name": "#payments", "connection": "main", "channel": "C0PAY"})
	if cfg := ch["config"].(map[string]any); cfg["connection"] != "main" || cfg["channel"] != "C0PAY" {
		t.Errorf("config %v", cfg)
	}
	owner.must(200, "POST", "/v1/alerts/channels/"+ch["id"].(string)+"/test", nil)
	got := w.fake.to("C0PAY")
	if len(got) != 1 || !strings.Contains(got[0]["text"].(string), "Test alert from Taskiem") {
		t.Fatalf("posted: %v", got)
	}
}
