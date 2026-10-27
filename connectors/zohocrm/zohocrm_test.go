package zohocrm

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/israel-duff/taskiem/engine/connector"
	"github.com/israel-duff/taskiem/engine/expr"
)

// fakeCRM keeps watches as CRM does: each lasts until its expiry.
type fakeCRM struct {
	mu      sync.Mutex
	watches map[string]map[string]any // channel id: the watch as sent, with its expiry
	calls   []string
}

func (f *fakeCRM) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, r.Method+" "+r.URL.Path)
	if r.URL.Path != "/actions/watch" {
		http.NotFound(w, r)
		return
	}
	switch r.Method {
	case http.MethodGet:
		var out []any
		for id, x := range f.watches {
			out = append(out, map[string]any{"channel_id": json.Number(id), "notify_url": x["notify_url"], "events": []any{map[string]any{"channel_expiry": x["channel_expiry"], "resource_name": "Deals"}}})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"watch": out})
	case http.MethodPost, http.MethodPatch:
		var body struct {
			Watch []map[string]any `json:"watch"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		x := body.Watch[0]
		id := x["channel_id"].(string)
		if r.Method == http.MethodPatch {
			old, ok := f.watches[id]
			if !ok {
				w.WriteHeader(http.StatusBadRequest)
				_, _ = io.WriteString(w, `{"watch":[{"code":"INVALID_DATA","message":"invalid channel","status":"error"}]}`)
				return
			}
			for k, v := range x {
				old[k] = v
			}
			x = old
		}
		f.watches[id] = x
		_ = json.NewEncoder(w).Encode(map[string]any{"watch": []any{map[string]any{"code": "SUCCESS", "status": "success", "details": map[string]any{"events": []any{map[string]any{"channel_expiry": x["channel_expiry"], "resource_name": "Deals", "channel_id": id}}}}}})
	case http.MethodDelete:
		delete(f.watches, r.URL.Query().Get("channel_ids"))
		_, _ = io.WriteString(w, `{"watch":[{"code":"SUCCESS","status":"success"}]}`)
	}
}

func remoteCall(srv *httptest.Server) connector.RemoteCall {
	return connector.RemoteCall{HTTP: srv.Client(), Credentials: map[string]string{"access_token": "1000.a", "access_expires_at": "2099-01-01T00:00:00Z"}}
}

// A watch is made with Taskiem's own token, lasts six days, is renewed
// with a new expiry, found again by name after a crash (and made again,
// since its token was never kept), and stopped.
func TestWatches(t *testing.T) {
	f := &fakeCRM{watches: map[string]map[string]any{}}
	srv := httptest.NewServer(f)
	defer srv.Close()
	now := time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC)
	c := New(Options{BaseURL: srv.URL, Now: func() time.Time { return now }})
	reg := c.Registrars["record_changes"]
	spec := connector.RemoteSpec{Name: "taskiem-sub-1", URL: "https://hooks.example/hooks/t/connectors/zoho_crm@1/record_changes?subscription=1", Events: []string{"insert", "update"}, Options: map[string]any{"module": "Deals"}}
	ctx := context.Background()
	st, err := reg.Create(ctx, remoteCall(srv), spec)
	if err != nil {
		t.Fatal(err)
	}
	x := f.watches[st.ID]
	if st.ID != channelID(spec.Name) || len(st.Secret) != 48 || x["token"] != st.Secret || !st.ExpiresAt.Equal(now.Add(lifetime)) {
		t.Fatalf("created %+v, watch %v", st, x)
	}
	if evs, _ := x["events"].([]any); len(evs) != 2 || evs[0] != "Deals.create" || evs[1] != "Deals.edit" {
		t.Errorf("events %v", x["events"])
	}
	now = now.Add(5 * 24 * time.Hour)
	up, err := reg.Update(ctx, remoteCall(srv), st.ID, spec)
	if err != nil {
		t.Fatal(err)
	}
	if !up.ExpiresAt.Equal(now.Add(lifetime)) || f.watches[st.ID]["token"] != st.Secret {
		t.Errorf("renewed %+v, token kept %v", up, f.watches[st.ID]["token"] == st.Secret)
	}
	// A watch whose expiry passed is gone, and so is its update.
	now = now.Add(8 * 24 * time.Hour)
	if _, err := reg.Get(ctx, remoteCall(srv), st.ID); !strings.Contains(errString(err), "no watch") {
		t.Errorf("lapsed: %v", err)
	}
	now = now.Add(-8 * 24 * time.Hour)
	// Found by name after a crash: stopped, so it is made again.
	if _, ok, err := reg.Find(ctx, remoteCall(srv), spec.Name); err != nil || ok || len(f.watches) != 0 {
		t.Errorf("find: %v %v, watches %d", ok, err, len(f.watches))
	}
	if err := reg.Delete(ctx, remoteCall(srv), st.ID); err != nil {
		t.Errorf("deleting a watch already gone: %v", err)
	}
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// A notification is verified by the token it carries and split into one
// event for each record it names.
func TestNotifications(t *testing.T) {
	spec := New(Options{}).Manifest.Triggers["record_changes"]
	body := []byte(`{"server_time":1761480000000,"module":"Deals","resource_uri":"https://www.zohoapis.com/crm/v8/Deals","ids":["5843104000000425001","5843104000000425002"],"operation":"update","channel_id":"1000000068001","token":"the-watch-token"}`)
	if err := connector.VerifyWebhook(spec.Verify, "the-watch-token", http.Header{}, body); err != nil {
		t.Fatalf("token: %v", err)
	}
	for _, bad := range []string{"another-token", ""} {
		if err := connector.VerifyWebhook(spec.Verify, bad, http.Header{}, body); err == nil {
			t.Errorf("secret %q verified", bad)
		}
	}
	if err := connector.VerifyWebhook(spec.Verify, "the-watch-token", http.Header{}, []byte(`{"token":"the-watch-token"`)); err == nil {
		t.Error("a body that does not parse verified")
	}
	e := expr.MustNewTriggerEngine("body", "headers", "query", "item")
	parsed, _ := expr.DecodeJSON(body)
	items, err := e.Eval(spec.Split, map[string]any{"body": parsed, "headers": map[string]any{}, "query": map[string]any{}})
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, it := range items.([]any) {
		a := map[string]any{"body": parsed, "headers": map[string]any{}, "query": map[string]any{}, "item": it}
		ev, _ := e.Eval(spec.EventType, a)
		corr, _ := e.Eval(spec.Correlation, a)
		dedup, _ := e.Eval(spec.Dedup, a)
		got = append(got, ev.(string)+"#"+corr.(string)+"/"+dedup.(string))
	}
	if strings.Join(got, " ") != "update#5843104000000425001/5843104000000425001:update:1761480000000 update#5843104000000425002/5843104000000425002:update:1761480000000" {
		t.Errorf("got %v", got)
	}
}
