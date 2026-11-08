package api_test

import (
	"bytes"
	"context"
	"encoding/json"
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
	mu     sync.Mutex
	srv    *httptest.Server
	url    string
	secret string
	sets   int
}

func newFakeBot(t *testing.T) *fakeBot {
	f := &fakeBot{}
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
		case strings.HasSuffix(r.URL.Path, "/deleteWebhook"):
			f.url, f.secret = "", ""
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

// Saving a Telegram connection sets the bot's webhook with no step by the
// owner (decision 0062 C7); a delivery with the secret Taskiem chose starts
// the workflow, one without it is refused; removing the connection removes
// the webhook.
func TestTelegramConnectionSetsItsWebhook(t *testing.T) {
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
	owner := w.tenant(t, "Mama Put", "owner@mamaput.test")
	conn := owner.must(201, "POST", "/v1/connections", map[string]any{"environment": "prod", "connector": "telegram@1", "name": "shop",
		"credentials": map[string]string{"bot_token": "7:shop-token"}})

	hook, secret, sets := bot.webhook()
	u, err := url.Parse(hook)
	if err != nil || !strings.HasPrefix(hook, "https://hooks.taskiem.example/hooks/") || !strings.HasSuffix(u.Path, "/connectors/telegram@1/update") ||
		u.Query().Get("subscription") == "" || u.Query().Get("env") != "prod" || len(secret) < 40 || sets != 1 {
		t.Fatalf("webhook %q secret %q (%d sets)", hook, secret, sets)
	}

	// A workflow on the bot's messages; a delivery to the registered URL
	// with the secret starts it.
	wf := owner.must(201, "POST", "/v1/workflows", map[string]any{"name": "tg", "definition": json.RawMessage(`{"schema":"wd/v1","id":"wf_tg","version":1,"name":"tg",
	  "trigger":{"type":"connector_event","config":{"connector":"telegram@1","trigger":"update","events":["message"]}},
	  "steps":[{"id":"a","type":"transform","config":{"output":"=trigger.body.message.text"}}]}`)})
	owner.must(200, "POST", "/v1/workflows/"+wf["id"].(string)+"/versions/1/publish", nil)
	send := func(token string) int {
		body := []byte(`{"update_id":1,"message":{"message_id":1,"date":1,"chat":{"id":42,"type":"private"},"text":"hello"}}`)
		req, _ := http.NewRequest("POST", edge.URL+"/hooks"+strings.TrimPrefix(u.Path, "/hooks")+"?"+u.RawQuery, bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Telegram-Bot-Api-Secret-Token", token)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		return resp.StatusCode
	}
	if code := send("forged"); code != 401 {
		t.Errorf("a forged delivery: %d", code)
	}
	if code := send(secret); code/100 != 2 {
		t.Fatalf("the delivery: %d", code)
	}
	var n int
	if err := w.env.DB.Admin.QueryRow(context.Background(), `SELECT count(*) FROM runs WHERE workflow_id = $1`, wf["id"]).Scan(&n); err != nil || n != 1 {
		t.Fatalf("runs %d %v", n, err)
	}

	// A health check finds the webhook in place and changes nothing.
	if _, err := rec.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, s2, _ := bot.webhook(); s2 != secret {
		t.Fatal("the secret changed on a check")
	}

	// Removed with the connection.
	owner.must(204, "DELETE", "/v1/connections/"+conn["id"].(string)+"?force=true", nil)
	if hook, _, _ := bot.webhook(); hook != "" {
		t.Errorf("the webhook outlived the connection: %s", hook)
	}
}
