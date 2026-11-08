package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/israel-duff/taskiem/connectors/telegram"
	"github.com/israel-duff/taskiem/engine/ingest"
	"github.com/israel-duff/taskiem/engine/remote"
)

// fakeBot is the Bot API's webhook methods for one bot.
type fakeBot struct {
	mu      sync.Mutex
	srv     *httptest.Server
	url     string
	secret  string
	sets    int
	deletes int
	urls    map[string]bool // every URL ever set
}

func newFakeBot(t *testing.T) *fakeBot {
	f := &fakeBot{urls: map[string]bool{}}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		var b map[string]any
		_ = json.NewDecoder(r.Body).Decode(&b)
		res := any(true)
		switch {
		case strings.HasSuffix(r.URL.Path, "/getMe"):
			res = map[string]any{"id": 7, "is_bot": true, "first_name": "Shop", "username": "shop_bot"}
		case strings.HasSuffix(r.URL.Path, "/setWebhook"):
			f.url, _ = b["url"].(string)
			f.secret, _ = b["secret_token"].(string)
			f.sets++
			f.urls[f.url] = true
		case strings.HasSuffix(r.URL.Path, "/deleteWebhook"):
			f.url, f.secret = "", ""
			f.deletes++
		case strings.HasSuffix(r.URL.Path, "/getWebhookInfo"):
			res = map[string]any{"url": f.url, "pending_update_count": 0}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "result": res})
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeBot) webhook() (string, string, int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.url, f.secret, f.sets
}

func (f *fakeBot) setURLs() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.urls)
}

// telegramWorld is a tenant whose Telegram bot is the fake, with the hooks
// edge and a reconciler.
type telegramWorld struct {
	w     *world
	bot   *fakeBot
	rec   *remote.Reconciler
	edge  *httptest.Server
	owner *client
}

func newTelegramWorld(t *testing.T) *telegramWorld {
	w := newWorld(t)
	bot := newFakeBot(t)
	if err := w.env.Registry.Register(telegram.New(telegram.Options{BaseURL: bot.srv.URL})); err != nil {
		t.Fatal(err)
	}
	h := &ingest.Handler{Store: w.env.Store, Secrets: w.env.Vault, Connections: w.env.Vault, Registry: w.env.Registry}
	mux := http.NewServeMux()
	mux.Handle("/hooks/", http.StripPrefix("/hooks", h))
	edge := httptest.NewServer(mux)
	t.Cleanup(edge.Close)
	rec := &remote.Reconciler{Pool: w.env.DB.App, Vault: w.env.Vault, Registry: w.env.Registry, Egress: w.env.Egress,
		HooksURL: "https://hooks.taskiem.example/hooks", Check: time.Nanosecond}
	w.srv.Remote = rec
	return &telegramWorld{w: w, bot: bot, rec: rec, edge: edge, owner: w.tenant(t, "Mama Put", "owner@mamaput.test")}
}

// publish makes and publishes (to dev and prod) a workflow.
func (tw *telegramWorld) publish(name, trigger string, steps string) (string, map[string]any) {
	wf := tw.owner.must(201, "POST", "/v1/workflows", map[string]any{"name": name, "definition": json.RawMessage(`{"schema":"wd/v1","id":"wf_` + name + `","version":1,"name":"` + name + `",
	  "trigger":` + trigger + `,"steps":` + steps + `}`)})
	id := wf["id"].(string)
	return id, tw.owner.must(200, "POST", "/v1/workflows/"+id+"/versions/1/publish", nil)
}

// send posts an update to the URL the bot holds, with token as its secret.
func (tw *telegramWorld) send(t *testing.T, hook, token string, update int) int {
	t.Helper()
	u, err := url.Parse(hook)
	if err != nil {
		t.Fatal(err)
	}
	body := []byte(fmt.Sprintf(`{"update_id":%d,"message":{"message_id":%d,"date":1,"chat":{"id":42,"type":"private"},"text":"hello"}}`, update, update))
	req, _ := http.NewRequest("POST", tw.edge.URL+"/hooks"+strings.TrimPrefix(u.Path, "/hooks")+"?"+u.RawQuery, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Telegram-Bot-Api-Secret-Token", token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	return resp.StatusCode
}

func (tw *telegramWorld) runs(t *testing.T, wf string) int {
	t.Helper()
	var n int
	if err := tw.w.env.DB.Admin.QueryRow(context.Background(), `SELECT count(*) FROM runs WHERE workflow_id = $1`, wf).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

const (
	tgOnUpdate      = `{"type":"connector_event","config":{"connector":"telegram@1","trigger":"update","events":["message"]%s}}`
	tgTransform     = `[{"id":"a","type":"transform","config":{"output":"=trigger.body.message.text"}}]`
	tgSendOnlySteps = `[{"id":"say","type":"connector","connector":"telegram@1","action":"send_message","connection":"shop","input":{"chat_id":"42","text":"hi"}}]`
)

// A Telegram bot's webhook is set only while a published workflow starts
// on its updates (option A of the V2 webhook review): saving or checking
// the connection, or publishing a workflow that only sends with it, leaves
// the bot alone; the first workflow on its updates sets it, a second
// shares it, and it is removed with the last.
func TestTelegramWebhookWhileWorkflowsUseIt(t *testing.T) {
	tw := newTelegramWorld(t)
	owner := tw.owner
	conn := owner.must(201, "POST", "/v1/connections", map[string]any{"environment": "prod", "connector": "telegram@1", "name": "shop",
		"credentials": map[string]string{"bot_token": "7:shop-token"}})
	owner.must(200, "POST", "/v1/connections/"+conn["id"].(string)+"/check", nil)
	if hook, _, sets := tw.bot.webhook(); hook != "" || sets != 0 {
		t.Fatalf("saving the connection set the webhook: %q (%d sets)", hook, sets)
	}

	// A workflow that only sends with the bot sets nothing.
	tw.publish("sender", `{"type":"manual"}`, tgSendOnlySteps)
	if _, err := tw.rec.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	if hook, _, sets := tw.bot.webhook(); hook != "" || sets != 0 {
		t.Fatalf("a send-only workflow set the webhook: %q (%d sets)", hook, sets)
	}

	// The first workflow on the bot's messages sets it, and the publish
	// answer shows it.
	wf1, pub := tw.publish("first", fmt.Sprintf(tgOnUpdate, `,"connection":"shop"`), tgTransform)
	hook, secret, sets := tw.bot.webhook()
	u, err := url.Parse(hook)
	if err != nil || !strings.HasPrefix(hook, "https://hooks.taskiem.example/hooks/") || !strings.HasSuffix(u.Path, "/connectors/telegram@1/update") ||
		u.Query().Get("subscription") == "" || u.Query().Get("env") != "prod" || len(secret) < 40 || sets != 1 {
		t.Fatalf("webhook %q secret %q (%d sets)", hook, secret, sets)
	}
	ok := false
	for _, s := range pub["remote_subscriptions"].([]any) {
		m := s.(map[string]any)
		ok = ok || m["environment"] == "prod" && m["workflow_id"] == nil && m["health"] == "ok"
	}
	if !ok {
		t.Errorf("the publish answer does not show the webhook: %v", pub["remote_subscriptions"])
	}
	if code := tw.send(t, hook, "forged", 1); code != 401 {
		t.Errorf("a forged delivery: %d", code)
	}
	if code := tw.send(t, hook, secret, 1); code/100 != 2 || tw.runs(t, wf1) != 1 {
		t.Fatalf("the delivery: %d, %d runs", code, tw.runs(t, wf1))
	}

	// A second workflow on the same bot (naming no connection: the only
	// one) shares the webhook; one update starts both.
	wf2, _ := tw.publish("second", fmt.Sprintf(tgOnUpdate, ""), tgTransform)
	if h2, s2, _ := tw.bot.webhook(); h2 != hook || s2 != secret || tw.bot.setURLs() != 1 {
		t.Fatalf("the second workflow changed the webhook: %q (%d URLs set)", h2, tw.bot.setURLs())
	}
	if code := tw.send(t, hook, secret, 2); code/100 != 2 || tw.runs(t, wf1) != 2 || tw.runs(t, wf2) != 1 {
		t.Fatalf("the shared delivery: %d, runs %d and %d", code, tw.runs(t, wf1), tw.runs(t, wf2))
	}

	// A health check changes nothing.
	if _, err := tw.rec.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	if h2, s2, _ := tw.bot.webhook(); h2 != hook || s2 != secret {
		t.Fatal("a check changed the webhook")
	}

	// Unpublishing one keeps it while the other is published.
	owner.must(200, "DELETE", "/v1/workflows/"+wf1+"/deployments/prod", nil)
	if _, err := tw.rec.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	if h2, _, _ := tw.bot.webhook(); h2 != hook {
		t.Fatalf("the webhook went with one of two workflows: %q", h2)
	}
	if code := tw.send(t, hook, secret, 3); code/100 != 2 || tw.runs(t, wf2) != 2 || tw.runs(t, wf1) != 2 {
		t.Fatalf("after one went: %d, runs %d and %d", code, tw.runs(t, wf1), tw.runs(t, wf2))
	}

	// Unpublishing the last removes it, and its address stops working.
	owner.must(200, "DELETE", "/v1/workflows/"+wf2+"/deployments/prod", nil)
	if h2, _, _ := tw.bot.webhook(); h2 != "" || tw.bot.deletes != 1 {
		t.Fatalf("the webhook outlived the workflows: %q", h2)
	}
	if code := tw.send(t, hook, secret, 4); code != 404 {
		t.Errorf("a delivery to the removed subscription: %d", code)
	}
	var live int
	if err := tw.w.env.DB.Admin.QueryRow(context.Background(), `SELECT count(*) FROM remote_subscriptions WHERE environment = 'prod'`).Scan(&live); err != nil || live != 0 {
		t.Fatalf("subscriptions left in prod: %d %v", live, err)
	}

	// Saving or checking the connection again sets nothing.
	owner.must(200, "POST", "/v1/connections/"+conn["id"].(string)+"/check", nil)
	if h2, _, _ := tw.bot.webhook(); h2 != "" {
		t.Fatalf("checking the connection set the webhook again: %q", h2)
	}

	// Deployed to prod again, then the connection removed: the webhook
	// goes with it.
	owner.must(201, "POST", "/v1/workflows/"+wf2+"/versions", map[string]any{"definition": json.RawMessage(`{"schema":"wd/v1","id":"wf_second","version":2,"name":"second",
	  "trigger":` + fmt.Sprintf(tgOnUpdate, "") + `,"steps":` + tgTransform + `}`)})
	owner.must(200, "POST", "/v1/workflows/"+wf2+"/versions/2/publish", nil)
	if h2, _, _ := tw.bot.webhook(); !strings.Contains(h2, "env=prod") {
		t.Fatalf("deploying again did not set the webhook: %q", h2)
	}
	owner.must(204, "DELETE", "/v1/connections/"+conn["id"].(string)+"?force=true", nil)
	if h2, _, _ := tw.bot.webhook(); h2 != "" {
		t.Errorf("the webhook outlived the connection: %s", h2)
	}
}

// A workflow deployed where its bot is not connected yet (dev, here): the
// webhook waits, shown as failed, and is set when the connection is made.
func TestTelegramWebhookWaitsForTheConnection(t *testing.T) {
	tw := newTelegramWorld(t)
	tw.owner.must(201, "POST", "/v1/connections", map[string]any{"environment": "prod", "connector": "telegram@1", "name": "shop",
		"credentials": map[string]string{"bot_token": "7:shop-token"}})
	wf, pub := tw.publish("early", fmt.Sprintf(tgOnUpdate, ""), tgTransform)
	health := map[string]string{}
	for _, s := range pub["remote_subscriptions"].([]any) {
		m := s.(map[string]any)
		health[m["environment"].(string)] = m["health"].(string)
		if m["environment"] == "dev" && !strings.Contains(fmt.Sprint(m["last_error"]), "no active telegram connection") {
			t.Errorf("dev's subscription: %v", m)
		}
	}
	if health["prod"] != "ok" || health["dev"] != "failed" {
		t.Fatalf("publish answered %v", pub["remote_subscriptions"])
	}
	// The same bot cannot serve both: prod lets it go first.
	tw.owner.must(200, "DELETE", "/v1/workflows/"+wf+"/deployments/prod", nil)
	if hook, _, _ := tw.bot.webhook(); hook != "" {
		t.Fatalf("prod kept the webhook: %q", hook)
	}
	tw.owner.must(201, "POST", "/v1/connections", map[string]any{"environment": "dev", "connector": "telegram@1", "name": "bot",
		"credentials": map[string]string{"bot_token": "7:shop-token"}})
	if hook, _, _ := tw.bot.webhook(); !strings.Contains(hook, "env=dev") {
		t.Fatalf("the webhook after connecting in dev: %q", hook)
	}
}

// One bot connected in two environments, each with a published workflow
// on it: the second is refused in plain words instead of taking the
// webhook back and forth, and takes it once the first is unpublished.
func TestTelegramBotInTwoEnvironments(t *testing.T) {
	tw := newTelegramWorld(t)
	for _, env := range []string{"prod", "dev"} {
		tw.owner.must(201, "POST", "/v1/connections", map[string]any{"environment": env, "connector": "telegram@1", "name": "shop",
			"credentials": map[string]string{"bot_token": "7:shop-token"}})
	}
	wf, _ := tw.publish("both", fmt.Sprintf(tgOnUpdate, `,"connection":"shop"`), tgTransform)
	for range 3 {
		if _, err := tw.rec.Tick(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	hook, _, sets := tw.bot.webhook()
	u, _ := url.Parse(hook)
	holder := u.Query().Get("env")
	other := map[string]string{"prod": "dev", "dev": "prod"}[holder]
	if other == "" || sets != 1 {
		t.Fatalf("the bot holds %q after %d sets", hook, sets)
	}
	var state, lastErr string
	if err := tw.w.env.DB.Admin.QueryRow(context.Background(), `SELECT state, COALESCE(last_error, '') FROM remote_subscriptions
		WHERE environment = $1 AND desired = 'present'`, other).Scan(&state, &lastErr); err != nil {
		t.Fatal(err)
	}
	if want := "this bot already receives messages for " + holder + "; use one bot per environment"; state != "failed" || !strings.Contains(lastErr, want) {
		t.Fatalf("the other environment's subscription: %s %q", state, lastErr)
	}
	// Shown on that environment's connection.
	for _, c := range tw.owner.must(200, "GET", "/v1/connections", nil)["connections"].([]any) {
		m := c.(map[string]any)
		if m["environment"] == other {
			if r, _ := m["remote"].(map[string]any); r == nil || r["health"] != "failed" {
				t.Errorf("connection in %s shows %v", other, m["remote"])
			}
		}
	}

	// Unpublished where it holds the webhook: the other environment takes it.
	tw.owner.must(200, "DELETE", "/v1/workflows/"+wf+"/deployments/"+holder, nil)
	if _, err := tw.rec.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	hook, secret, _ := tw.bot.webhook()
	if u, _ := url.Parse(hook); u == nil || u.Query().Get("env") != other {
		t.Fatalf("after unpublishing in %s the bot holds %q", holder, hook)
	}
	if code := tw.send(t, hook, secret, 1); code/100 != 2 || tw.runs(t, wf) != 1 {
		t.Fatalf("a delivery in %s: %d", other, code)
	}
}
