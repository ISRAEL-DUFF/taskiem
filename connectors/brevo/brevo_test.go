package brevo

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/israel-duff/taskiem/engine/connector"
	"github.com/israel-duff/taskiem/engine/expr"
)

type fakeBrevo struct {
	mu       sync.Mutex
	webhooks map[string]map[string]any
	n        int
}

func (f *fakeBrevo) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	id := strings.TrimPrefix(r.URL.Path, "/webhooks/")
	switch {
	case r.URL.Path == "/webhooks" && r.Method == http.MethodPost:
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		f.n++
		id := strconv.Itoa(400 + f.n)
		body["id"] = json.Number(id)
		f.webhooks[id] = body
		w.WriteHeader(http.StatusCreated)
		_, _ = io.WriteString(w, `{"id":`+id+`}`)
	case r.URL.Path == "/webhooks" && r.Method == http.MethodGet:
		var out []any
		for _, x := range f.webhooks {
			if x["type"] == r.URL.Query().Get("type") {
				out = append(out, x)
			}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"webhooks": out})
	case f.webhooks[id] == nil:
		w.WriteHeader(http.StatusNotFound)
		_, _ = io.WriteString(w, `{"code":"document_not_found","message":"Webhook not found"}`)
	case r.Method == http.MethodGet:
		_ = json.NewEncoder(w).Encode(f.webhooks[id])
	case r.Method == http.MethodDelete:
		delete(f.webhooks, id)
		w.WriteHeader(http.StatusNoContent)
	}
}

// A webhook is made with Taskiem's own bearer token; one that still
// matches is kept, one that does not is deleted to be made again, and one
// found after a crash is deleted.
func TestWebhooks(t *testing.T) {
	f := &fakeBrevo{webhooks: map[string]map[string]any{}}
	srv := httptest.NewServer(f)
	defer srv.Close()
	c := New(Options{BaseURL: srv.URL})
	reg := c.Registrars["marketing_events"]
	call := connector.RemoteCall{Credentials: map[string]string{"api_key": "xkeysib-test"}, HTTP: srv.Client()}
	spec := connector.RemoteSpec{Name: "taskiem-sub-1", URL: "https://hooks.example/hooks/t/connectors/brevo@1/marketing_events?subscription=1", Events: []string{"unsubscribed", "hardBounce"}}
	ctx := context.Background()
	st, err := reg.Create(ctx, call, spec)
	if err != nil {
		t.Fatal(err)
	}
	x := f.webhooks[st.ID]
	auth, _ := x["auth"].(map[string]any)
	if len(st.Secret) != 64 || auth["type"] != "bearer" || auth["token"] != st.Secret || x["type"] != "marketing" || x["description"] != spec.Name {
		t.Fatalf("created %+v, webhook %v", st, x)
	}
	if _, err := reg.Update(ctx, call, st.ID, spec); err != nil || len(f.webhooks) != 1 {
		t.Errorf("a matching webhook was not kept: %v", err)
	}
	spec.Events = []string{"unsubscribed"}
	if _, err := reg.Update(ctx, call, st.ID, spec); err == nil || len(f.webhooks) != 0 {
		t.Errorf("a changed webhook: %v, left %d", err, len(f.webhooks))
	}
	if _, err := reg.Create(ctx, call, spec); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := c.Registrars["transactional_events"].Find(ctx, call, spec.Name); ok || err != nil || len(f.webhooks) != 1 {
		t.Errorf("another type's webhook was touched: %d left, %v", len(f.webhooks), err)
	}
	if _, ok, err := reg.Find(ctx, call, spec.Name); ok || err != nil || len(f.webhooks) != 0 {
		t.Errorf("found after a crash: %v %v, left %d", ok, err, len(f.webhooks))
	}
	// A bearer token verifies Brevo's deliveries.
	h := http.Header{}
	h.Set("Authorization", "Bearer "+st.Secret)
	if err := connector.VerifyWebhook(c.Manifest.Triggers["marketing_events"].Verify, st.Secret, h, []byte(`{}`)); err != nil {
		t.Errorf("bearer: %v", err)
	}
}

// Brevo's deliveries name events in snake case; the triggers name them as
// its webhooks do.
func TestEventNames(t *testing.T) {
	m := New(Options{}).Manifest
	e := expr.MustNewTriggerEngine("body", "headers", "query", "item")
	for trigger, cases := range map[string]map[string]string{
		"transactional_events": {"hard_bounce": "hardBounce", "delivered": "delivered", "invalid_email": "invalid", "unique_opened": "uniqueOpened"},
		"marketing_events":     {"unsubscribe": "unsubscribed", "list_addition": "listAddition", "click": "click"},
	} {
		spec := m.Triggers[trigger]
		for in, want := range cases {
			body, _ := expr.DecodeJSON([]byte(`{"event":"` + in + `","email":"ada@example.com","message-id":"<m1@relay>","ts_event":1761480005,"camp_id":7}`))
			a := map[string]any{"body": body, "headers": map[string]any{}, "query": map[string]any{}}
			got, err := e.Eval(spec.EventType, a)
			if err != nil || got != want {
				t.Errorf("%s %s: %v %v, want %s", trigger, in, got, err, want)
			}
			if _, err := e.Eval(spec.Dedup, a); err != nil {
				t.Errorf("%s dedup: %v", trigger, err)
			}
			if !contains(spec.Events, want) {
				t.Errorf("%s: %s is not a declared event", trigger, want)
			}
		}
	}
}

func contains(l []string, s string) bool {
	for _, x := range l {
		if x == s {
			return true
		}
	}
	return false
}
