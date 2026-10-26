package meta

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/israel-duff/taskiem/engine/connector"
	"github.com/israel-duff/taskiem/engine/conntest"
	"github.com/israel-duff/taskiem/engine/expr"
)

// The fixtures' clock: the person last wrote at 11:55, so the window is
// open until 11:55 tomorrow.
var fixed = func() time.Time { return time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC) }

// Every action passes the conformance kit on fixtures shaped after Meta's
// published examples (docs/integrations/messenger-and-instagram.md).
func TestConformance(t *testing.T) {
	for name, c := range map[string]*connector.Connector{"messenger": Messenger(Options{Now: fixed}), "instagram": Instagram(Options{Now: fixed})} {
		s, err := conntest.LoadDir("testdata/" + name)
		if err != nil {
			t.Fatal(err)
		}
		rep := conntest.Run(context.Background(), c, s, conntest.Options{})
		for _, r := range rep.Results {
			for _, p := range r.Problems {
				t.Errorf("%s %s: %s", name, r.Case, p)
			}
		}
		if len(rep.Uncovered)+len(rep.ReadWrites) > 0 {
			t.Errorf("%s: uncovered %v, reads that write %v", name, rep.Uncovered, rep.ReadWrites)
		}
	}
}

func TestRegisters(t *testing.T) {
	reg := connector.NewRegistry()
	for _, c := range []*connector.Connector{Messenger(Options{}), Instagram(Options{})} {
		if err := reg.Register(c); err != nil {
			t.Fatal(err)
		}
		m := c.Manifest
		o := m.Auth.OAuth2
		if m.ClientName() != "meta" || o.TokenMethod != "get" || len(o.LinkedFields) != 5 || !slices.Equal(m.Hosts(), []string{"graph.facebook.com"}) {
			t.Errorf("%s: client %s, %+v, hosts %v", m.ID, m.ClientName(), o, m.Hosts())
		}
		if spec := m.Triggers["messages"]; !spec.AppDelivered() || spec.Handshake == nil || len(spec.PII) == 0 {
			t.Errorf("%s: messages %+v", m.ID, spec)
		}
	}
	if Messenger(Options{}).Manifest.Auth.OAuth2.AccountField != "page_id" || Instagram(Options{}).Manifest.Auth.OAuth2.AccountField != "instagram_id" {
		t.Error("account fields")
	}
}

// fakeGraph is Meta's side of connecting: the long-lived token, the Pages
// the login granted, and subscribing one to the app.
type fakeGraph struct {
	pages      string
	subscribed []string
	exchanged  string
}

func (f *fakeGraph) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.URL.Path == "/oauth/access_token":
		q := r.URL.Query()
		if q.Get("grant_type") != "fb_exchange_token" || q.Get("client_secret") != "app-secret" {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = io.WriteString(w, `{"error":{"message":"bad","code":100}}`)
			return
		}
		f.exchanged = q.Get("fb_exchange_token")
		_, _ = io.WriteString(w, `{"access_token":"EAAlong-user","token_type":"bearer","expires_in":5183944}`)
	case r.URL.Path == "/me/accounts":
		if r.Header.Get("Authorization") != "Bearer EAAlong-user" {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = io.WriteString(w, `{"error":{"message":"Invalid OAuth access token.","code":190}}`)
			return
		}
		_, _ = io.WriteString(w, f.pages)
	case strings.HasSuffix(r.URL.Path, "/subscribed_apps") && r.Method == http.MethodPost:
		f.subscribed = append(f.subscribed, strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/"), "/subscribed_apps")+" "+r.URL.Query().Get("subscribed_fields")+" "+r.Header.Get("Authorization"))
		_, _ = io.WriteString(w, `{"success":true}`)
	default:
		http.NotFound(w, r)
	}
}

const onePage = `{"data":[{"id":"104123456789012","name":"Ada's Cakes","access_token":"EAApage","instagram_business_account":{"id":"17841400000000001","username":"ada.cakes"}}]}`

func TestLinkMakesTheOnePagesConnection(t *testing.T) {
	f := &fakeGraph{pages: onePage}
	srv := httptest.NewServer(f)
	defer srv.Close()
	lc := connector.LinkCall{Credentials: map[string]string{}, Token: "EAAshort", ClientID: "123", ClientSecret: "app-secret", HTTP: srv.Client()}
	for _, c := range []*connector.Connector{Messenger(Options{BaseURL: srv.URL}), Instagram(Options{BaseURL: srv.URL})} {
		creds, err := c.Link(context.Background(), lc)
		if err != nil {
			t.Fatalf("%s: %v", c.Manifest.ID, err)
		}
		if creds["page_id"] != "104123456789012" || creds["page_token"] != "EAApage" || creds["instagram_id"] != "17841400000000001" || f.exchanged != "EAAshort" {
			t.Errorf("%s: %v", c.Manifest.ID, creds)
		}
		if _, kept := creds["user_token"]; kept {
			t.Errorf("%s keeps the user token", c.Manifest.ID)
		}
	}
	if len(f.subscribed) != 2 || !strings.HasPrefix(f.subscribed[0], "104123456789012 messages,messaging_postbacks") || !strings.HasSuffix(f.subscribed[0], "Bearer EAApage") {
		t.Errorf("subscribed %v", f.subscribed)
	}

	// Two Pages granted, or for Instagram none with an account: connect
	// again and choose one, in the person's words.
	f.pages = `{"data":[{"id":"1","name":"A","access_token":"x"},{"id":"2","name":"B","access_token":"y"}]}`
	if _, err := Messenger(Options{BaseURL: srv.URL}).Link(context.Background(), lc); !errors.Is(err, connector.ErrLinkRefused) || !strings.Contains(err.Error(), "choose exactly one") {
		t.Errorf("two Pages: %v", err)
	}
	f.pages = `{"data":[{"id":"1","name":"A","access_token":"x"}]}`
	if _, err := Instagram(Options{BaseURL: srv.URL}).Link(context.Background(), lc); !errors.Is(err, connector.ErrLinkRefused) || !strings.Contains(err.Error(), "Instagram account") {
		t.Errorf("no Instagram account: %v", err)
	}
}

// A delivery of several Pages' events is split into one event each,
// routed by its Page, named and correlated on the person, and verified
// with the app secret.
func TestAppDelivery(t *testing.T) {
	spec := Messenger(Options{}).Manifest.Triggers["messages"]
	body := []byte(`{"object":"page","entry":[
	  {"id":"104123456789012","time":1761480000000,"messaging":[
	    {"sender":{"id":"6789012345678901"},"recipient":{"id":"104123456789012"},"timestamp":1761480000000,"message":{"mid":"m_1","text":"Do you have it in blue?"}},
	    {"sender":{"id":"104123456789012"},"recipient":{"id":"6789012345678901"},"timestamp":1761480001000,"message":{"mid":"m_2","is_echo":true,"text":"Yes"}}]},
	  {"id":"204123456789012","time":1761480002000,"messaging":[
	    {"sender":{"id":"7789012345678901"},"recipient":{"id":"204123456789012"},"timestamp":1761480002000,"postback":{"title":"Buy","payload":"buy:42","mid":"m_3"}},
	    {"sender":{"id":"7789012345678901"},"recipient":{"id":"204123456789012"},"timestamp":1761480003000,"read":{"watermark":1761480001000}}]}]}`)
	mac := hmac.New(sha256.New, []byte("app-secret"))
	mac.Write(body)
	h := http.Header{}
	h.Set("X-Hub-Signature-256", "sha256="+hex.EncodeToString(mac.Sum(nil)))
	if err := connector.VerifyWebhook(spec.Verify, "app-secret", h, body); err != nil {
		t.Fatalf("signature: %v", err)
	}
	if err := connector.VerifyWebhook(spec.Verify, "another-app", h, body); err == nil {
		t.Fatal("another app's secret verified")
	}
	e := expr.MustNewTriggerEngine("body", "headers", "query", "item")
	parsed, err := expr.DecodeJSON(body)
	if err != nil {
		t.Fatal(err)
	}
	items, err := e.Eval(spec.Split, map[string]any{"body": parsed, "headers": map[string]any{}, "query": map[string]any{}})
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, it := range items.([]any) {
		a := map[string]any{"body": parsed, "headers": map[string]any{}, "query": map[string]any{}, "item": it}
		ev, err1 := e.Eval(spec.EventType, a)
		acct, err2 := e.Eval(spec.AccountKey, a)
		corr, err3 := e.Eval(spec.Correlation, a)
		dedup, err4 := e.Eval(spec.Dedup, a)
		if err := errors.Join(err1, err2, err3, err4); err != nil {
			t.Fatal(err)
		}
		got = append(got, ev.(string)+"@"+acct.(string)+"#"+corr.(string)+"/"+dedup.(string))
	}
	want := []string{"message@104123456789012#6789012345678901/m_1", "echo@104123456789012#6789012345678901/m_2",
		"postback@204123456789012#7789012345678901/m_3", "read@204123456789012#7789012345678901/7789012345678901:1761480003000"}
	if !slices.Equal(got, want) {
		t.Errorf("got %v\nwant %v", got, want)
	}
}
