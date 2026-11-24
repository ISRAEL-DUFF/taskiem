package telegram

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/israel-duff/taskiem/engine/connector"
	"github.com/israel-duff/taskiem/engine/effects"
)

// fakeAPI is the Bot API's webhook methods for one bot: info is what
// getWebhookInfo answers besides the url.
type fakeAPI struct {
	mu      sync.Mutex
	url     string
	secret  string
	info    map[string]any
	calls   []string
	deletes int
}

func (f *fakeAPI) serve(t *testing.T) *httptest.Server {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		var b map[string]any
		_ = json.NewDecoder(r.Body).Decode(&b)
		method := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
		f.calls = append(f.calls, method)
		res := any(true)
		switch method {
		case "setWebhook":
			f.url, _ = b["url"].(string)
			f.secret, _ = b["secret_token"].(string)
		case "deleteWebhook":
			f.url, f.secret = "", ""
			f.deletes++
		case "getWebhookInfo":
			m := map[string]any{"url": f.url, "pending_update_count": 0}
			for k, v := range f.info {
				m[k] = v
			}
			res = m
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "result": res})
	}))
	t.Cleanup(srv.Close)
	return srv
}

const (
	subA = "taskiem-0190f6a4-0000-7000-8000-00000000000a"
	subB = "taskiem-0190f6a4-0000-7000-8000-00000000000b"
)

func hookURL(name string) string {
	return "https://hooks.taskiem.example/hooks/t1/connectors/telegram@1/update?env=prod&subscription=" + strings.TrimPrefix(name, "taskiem-")
}

func registrar(t *testing.T, f *fakeAPI) (connector.Registrar, connector.RemoteCall) {
	srv := f.serve(t)
	c := New(Options{BaseURL: srv.URL})
	return c.Registrars["update"], connector.RemoteCall{Credentials: map[string]string{"bot_token": token}, HTTP: srv.Client()}
}

func TestWebhookRegistrar(t *testing.T) {
	f := &fakeAPI{}
	reg, call := registrar(t, f)
	ctx := context.Background()
	st, err := reg.Create(ctx, call, connector.RemoteSpec{Name: subA, URL: hookURL(subA)})
	if err != nil || st.ID != subA || st.Secret == "" || f.url != hookURL(subA) || f.secret != st.Secret {
		t.Fatalf("create %+v %v (bot holds %q)", st, err, f.url)
	}
	// Applied again, the secret is the same: it is derived, never shown.
	if s, _ := reg.RotateSecret(ctx, call, subA); s != st.Secret {
		t.Error("the secret changed")
	}
	if _, err := reg.Update(ctx, call, subA, connector.RemoteSpec{Name: subA, URL: hookURL(subA)}); err != nil || f.secret != st.Secret {
		t.Fatalf("update %v", err)
	}
	if _, ok, err := reg.Find(ctx, call, subA); !ok || err != nil {
		t.Errorf("find %v %v", ok, err)
	}
	if _, ok, _ := reg.Find(ctx, call, subB); ok {
		t.Error("found another subscription's webhook")
	}
	// Another subscription's Delete leaves the webhook alone.
	if err := reg.Delete(ctx, call, subB); err != nil || f.url == "" {
		t.Fatalf("delete of another %v (%q)", err, f.url)
	}
	if err := reg.Delete(ctx, call, subA); err != nil || f.url != "" || f.deletes != 1 {
		t.Fatalf("delete %v (%q, %d)", err, f.url, f.deletes)
	}
	// Gone, or someone else's: missing.
	if _, err := reg.Get(ctx, call, subA); !errors.Is(err, connector.ErrNotFound) {
		t.Errorf("get after delete: %v", err)
	}
	f.url = "https://elsewhere.example/bot"
	if _, err := reg.Get(ctx, call, subA); !errors.Is(err, connector.ErrNotFound) {
		t.Errorf("get of another's webhook: %v", err)
	}
}

// Gap 17: a webhook is failing while updates wait behind an error, or when
// the last error is recent though nothing waits; healthy otherwise.
func TestWebhookHealth(t *testing.T) {
	at := time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC)
	defer func(old func() time.Time) { now = old }(now)
	now = func() time.Time { return at }
	f := &fakeAPI{url: hookURL(subA)}
	reg, call := registrar(t, f)
	for _, c := range []struct {
		name string
		info map[string]any
		want string
	}{
		{"no error", nil, connector.RemoteHealthy},
		{"an old error, nothing pending", map[string]any{"last_error_message": "Connection timed out", "last_error_date": at.Add(-2 * time.Hour).Unix()}, connector.RemoteHealthy},
		{"an error with updates waiting", map[string]any{"last_error_message": "Wrong response from the webhook: 503", "last_error_date": at.Add(-3 * time.Hour).Unix(), "pending_update_count": 4}, connector.RemoteFailing},
		{"a recent error, nothing pending", map[string]any{"last_error_message": "Wrong response from the webhook: 401", "last_error_date": at.Add(-10 * time.Minute).Unix()}, connector.RemoteFailing},
		{"a recent error with no message", map[string]any{"last_error_date": at.Add(-time.Minute).Unix()}, connector.RemoteFailing},
		{"updates waiting, no error", map[string]any{"pending_update_count": 3}, connector.RemoteHealthy},
	} {
		f.info = c.info
		st, err := reg.Get(context.Background(), call, subA)
		if err != nil || st.Status != c.want {
			t.Errorf("%s: %+v %v, want %s", c.name, st, err, c.want)
		}
		if c.want == connector.RemoteFailing && !strings.HasPrefix(st.Reason, "Telegram could not deliver updates: ") {
			t.Errorf("%s: reason %q", c.name, st.Reason)
		}
	}
}

// One bot in two environments: a webhook another live subscription of the
// tenant holds is not taken; one that is not Taskiem's, or is being
// removed, is.
func TestWebhookHeldElsewhere(t *testing.T) {
	f := &fakeAPI{url: hookURL(subB)}
	reg, call := registrar(t, f)
	holder := "dev"
	var asked string
	call.Claimed = func(_ context.Context, u string) (string, error) { asked = u; return holder, nil }
	_, err := reg.Create(context.Background(), call, connector.RemoteSpec{Name: subA, URL: hookURL(subA)})
	if effects.Classify(err) != effects.KindFatal || !strings.Contains(err.Error(), "this bot already receives messages for dev; use one bot per environment") {
		t.Fatalf("create over another environment's webhook: %v", err)
	}
	if asked != hookURL(subB) || f.url != hookURL(subB) {
		t.Fatalf("asked about %q; the bot holds %q", asked, f.url)
	}
	if _, err := reg.Update(context.Background(), call, subA, connector.RemoteSpec{Name: subA, URL: hookURL(subA)}); effects.Classify(err) != effects.KindFatal {
		t.Fatalf("update over another environment's webhook: %v", err)
	}
	// Released there (or not Taskiem's): taken.
	holder = ""
	if _, err := reg.Create(context.Background(), call, connector.RemoteSpec{Name: subA, URL: hookURL(subA)}); err != nil || f.url != hookURL(subA) {
		t.Fatalf("create once released: %v (%q)", err, f.url)
	}
	// Its own webhook is never asked about.
	asked = ""
	holder = "dev"
	if _, err := reg.Update(context.Background(), call, subA, connector.RemoteSpec{Name: subA, URL: hookURL(subA)}); err != nil || asked != "" {
		t.Fatalf("re-applying its own: %v (asked %q)", err, asked)
	}
}
