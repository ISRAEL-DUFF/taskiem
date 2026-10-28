package mailchimp

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/israel-duff/taskiem/engine/connector"
	"github.com/israel-duff/taskiem/engine/expr"
)

func TestServer(t *testing.T) {
	for creds, want := range map[[2]string]string{{"", "0123abcd-us6"}: "us6", {"us21", "token"}: "us21"} {
		got, err := server(map[string]string{"server": creds[0], "access_token": creds[1]})
		if err != nil || got != want {
			t.Errorf("server(%v) = %q %v", creds, got, err)
		}
	}
	if _, err := server(map[string]string{"access_token": "0123abcd-evil.example/"}); err == nil {
		t.Error("a suffix that is no server")
	}
	if h := New(Options{}).Manifest.Hosts(); len(h) != 2 || h[0] != "*.api.mailchimp.com" {
		t.Errorf("hosts %v", h)
	}
}

// Connecting reads the account's server from Mailchimp's metadata.
func TestLink(t *testing.T) {
	md := `{"dc":"us6","accountname":"Ada Cakes","api_endpoint":"https://us6.api.mailchimp.com","login":{"email":"owner@adacakes.ng"}}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "OAuth mc-granted" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_, _ = io.WriteString(w, md)
	}))
	defer srv.Close()
	c := New(Options{MetadataURL: srv.URL})
	creds, err := c.Link(context.Background(), connector.LinkCall{Credentials: map[string]string{"access_token": "mc-granted"}, Token: "mc-granted", HTTP: srv.Client()})
	if err != nil || creds["server"] != "us6" || creds["account_name"] != "Ada Cakes" || creds["access_token"] != "mc-granted" {
		t.Fatalf("creds %v: %v", creds, err)
	}
	md = `{"dc":"evil.example"}`
	if _, err := c.Link(context.Background(), connector.LinkCall{Token: "mc-granted", HTTP: srv.Client()}); !errors.Is(err, connector.ErrLinkRefused) {
		t.Errorf("a server that is none: %v", err)
	}
}

type fakeMailchimp struct {
	mu    sync.Mutex
	hooks map[string]map[string]map[string]any // audience: id: webhook
	n     int
}

func (f *fakeMailchimp) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/") // lists/{aud}/webhooks[/{id}]
	if r.URL.Path == "/lists" {
		var ls []any
		for a := range f.hooks {
			ls = append(ls, map[string]any{"id": a})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"lists": ls})
		return
	}
	if len(parts) < 3 || parts[0] != "lists" || parts[2] != "webhooks" {
		http.NotFound(w, r)
		return
	}
	aud := parts[1]
	if f.hooks[aud] == nil {
		f.hooks[aud] = map[string]map[string]any{}
	}
	switch {
	case len(parts) == 3 && r.Method == http.MethodPost:
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		f.n++
		id := "wh" + strconv.Itoa(f.n)
		body["id"] = id
		f.hooks[aud][id] = body
		_ = json.NewEncoder(w).Encode(map[string]any{"id": id, "url": body["url"], "events": body["events"], "signing_enabled": true, "signing_secret": "mcsig_secret"})
	case len(parts) == 3:
		var out []any
		for _, x := range f.hooks[aud] {
			out = append(out, x)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"webhooks": out})
	case f.hooks[aud][parts[3]] == nil:
		w.WriteHeader(http.StatusNotFound)
		_, _ = io.WriteString(w, `{"title":"Resource Not Found","status":404}`)
	case r.Method == http.MethodGet:
		_ = json.NewEncoder(w).Encode(f.hooks[aud][parts[3]])
	case r.Method == http.MethodDelete:
		delete(f.hooks[aud], parts[3])
		w.WriteHeader(http.StatusNoContent)
	}
}

func TestWebhooks(t *testing.T) {
	f := &fakeMailchimp{hooks: map[string]map[string]map[string]any{}}
	srv := httptest.NewServer(f)
	defer srv.Close()
	reg := New(Options{BaseURL: srv.URL}).Registrars["audience_events"]
	call := connector.RemoteCall{Credentials: map[string]string{"access_token": "tok", "server": "us6"}, HTTP: srv.Client()}
	spec := connector.RemoteSpec{Name: "taskiem-sub-9", URL: "https://hooks.example/hooks/t/connectors/mailchimp@1/audience_events?env=prod&subscription=sub-9", Events: []string{"unsubscribe", "cleaned"},
		Options: map[string]any{"audience_id": "a6b5da1054"}}
	ctx := context.Background()
	st, err := reg.Create(ctx, call, spec)
	if err != nil {
		t.Fatal(err)
	}
	x := f.hooks["a6b5da1054"]["wh1"]
	evs, _ := x["events"].(map[string]any)
	if st.ID != "a6b5da1054/wh1" || st.Secret != "mcsig_secret" || evs["unsubscribe"] != true || evs["subscribe"] != false || x["signing_enabled"] != true {
		t.Fatalf("created %+v, webhook %v", st, x)
	}
	if _, err := reg.Update(ctx, call, st.ID, spec); err != nil {
		t.Errorf("a matching webhook: %v", err)
	}
	spec.Events = []string{"unsubscribe"}
	if _, err := reg.Update(ctx, call, st.ID, spec); !errors.Is(err, connector.ErrNotFound) || len(f.hooks["a6b5da1054"]) != 0 {
		t.Errorf("a changed webhook: %v", err)
	}
	if _, err := reg.Create(ctx, call, spec); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := reg.Find(ctx, call, spec.Name); ok || err != nil || len(f.hooks["a6b5da1054"]) != 0 {
		t.Errorf("found after a crash: %v %v", ok, err)
	}
	if _, err := reg.Create(ctx, call, connector.RemoteSpec{Name: "x", URL: spec.URL}); err == nil {
		t.Error("a webhook without an audience")
	}
}

// A delivery, form-encoded and signed as Mailchimp signs, verifies, names
// its event and correlates on the person.
func TestDelivery(t *testing.T) {
	spec := New(Options{}).Manifest.Triggers["audience_events"]
	form := url.Values{"type": {"unsubscribe"}, "fired_at": {"2026-10-26 12:00:05"}, "data[action]": {"unsub"}, "data[reason]": {"manual"}, "data[list_id]": {"a6b5da1054"}, "data[email]": {"ada@example.com"}}
	body := []byte(form.Encode())
	ts := strconv.FormatInt(time.Now().Unix(), 10)
	mac := hmac.New(sha256.New, []byte("mcsig_secret"))
	mac.Write([]byte(ts + "."))
	mac.Write(body)
	h := http.Header{}
	h.Set("X-Mailchimp-Signature", "t="+ts+",v1="+hex.EncodeToString(mac.Sum(nil)))
	if err := connector.VerifyWebhook(spec.Verify, "mcsig_secret", h, body); err != nil {
		t.Fatalf("signature: %v", err)
	}
	if err := connector.VerifyWebhook(spec.Verify, "mcsig_secret", h, append(body, '&')); err == nil {
		t.Error("a changed body verified")
	}
	parsed := map[string]any{}
	for k, v := range form {
		parsed[k] = v[0]
	}
	e := expr.MustNewTriggerEngine("body", "headers", "query", "item")
	a := map[string]any{"body": parsed, "headers": map[string]any{}, "query": map[string]any{}}
	ev, err1 := e.Eval(spec.EventType, a)
	corr, err2 := e.Eval(spec.Correlation, a)
	dedup, err3 := e.Eval(spec.Dedup, a)
	if err := errors.Join(err1, err2, err3); err != nil {
		t.Fatal(err)
	}
	if ev != "unsubscribe" || corr != "ada@example.com" || dedup != "unsubscribe:ada@example.com:2026-10-26 12:00:05" {
		t.Errorf("event %v, correlation %v, dedup %v", ev, corr, dedup)
	}
	if hs := spec.Handshake; hs == nil || !hs.Open || hs.Method != "GET" {
		t.Errorf("handshake %+v", hs)
	}
}

// The audiences picker reads list_audiences' output as choices.
func TestAudiencesPicker(t *testing.T) {
	m := New(Options{}).Manifest
	got, err := m.Choices("audiences", map[string]any{"audiences": []any{map[string]any{"id": "a6b5da1054", "name": "Newsletter", "members": int64(118)}}})
	if err != nil || len(got) != 1 || got[0].ID != "a6b5da1054" || got[0].Label != "Newsletter (118 subscribed)" {
		t.Errorf("choices %+v: %v", got, err)
	}
	if err := connector.NewRegistry().Register(New(Options{})); err != nil {
		t.Fatal(err)
	}
}
