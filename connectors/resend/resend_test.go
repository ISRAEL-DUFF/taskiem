package resend

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/israel-duff/taskiem/engine/connector"
)

type fakeResend struct {
	mu       sync.Mutex
	webhooks map[string]map[string]any
	n        int
}

func (f *fakeResend) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if r.Header.Get("Authorization") != "Bearer re_test" {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, `{"statusCode":401,"name":"validation_error","message":"API key is invalid"}`)
		return
	}
	id := strings.TrimPrefix(r.URL.Path, "/webhooks/")
	switch {
	case r.URL.Path == "/webhooks" && r.Method == http.MethodPost:
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		f.n++
		id := "4dd369bc-aa82-4ff3-97de-51483566c58" + string(rune('0'+f.n))
		body["id"], body["status"] = id, "enabled"
		f.webhooks[id] = body
		_ = json.NewEncoder(w).Encode(map[string]any{"object": "webhook", "id": id, "signing_secret": "whsec_" + strings.Repeat("A", 32)})
	case r.URL.Path == "/webhooks" && r.Method == http.MethodGet:
		var out []any
		for _, x := range f.webhooks {
			out = append(out, x)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"object": "list", "has_more": false, "data": out})
	case f.webhooks[id] == nil:
		w.WriteHeader(http.StatusNotFound)
		_, _ = io.WriteString(w, `{"statusCode":404,"name":"not_found","message":"Webhook not found"}`)
	case r.Method == http.MethodGet:
		_ = json.NewEncoder(w).Encode(f.webhooks[id])
	case r.Method == http.MethodPatch:
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		for k, v := range body {
			f.webhooks[id][k] = v
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"object": "webhook", "id": id})
	case r.Method == http.MethodDelete:
		delete(f.webhooks, id)
		_ = json.NewEncoder(w).Encode(map[string]any{"object": "webhook", "id": id, "deleted": true})
	}
}

// The webhook is made with the workflow's events and its Svix secret kept;
// it is updated in place (turned back on when paused), and one found after
// a crash is deleted so it is made again with a secret Taskiem keeps.
func TestWebhooks(t *testing.T) {
	f := &fakeResend{webhooks: map[string]map[string]any{}}
	srv := httptest.NewServer(f)
	defer srv.Close()
	reg := New(Options{BaseURL: srv.URL}).Registrars["email_events"]
	call := connector.RemoteCall{Credentials: map[string]string{"api_key": "re_test"}, HTTP: srv.Client()}
	spec := connector.RemoteSpec{Name: "taskiem-0192f0aa-0000-7000-8000-000000000001", URL: "https://hooks.example/hooks/t/connectors/resend@1/email_events?env=prod&subscription=0192f0aa-0000-7000-8000-000000000001",
		Events: []string{"email.complained", "email.bounced"}}
	ctx := context.Background()
	st, err := reg.Create(ctx, call, spec)
	if err != nil {
		t.Fatal(err)
	}
	x := f.webhooks[st.ID]
	if !strings.HasPrefix(st.Secret, "whsec_") || x["endpoint"] != spec.URL || len(x["events"].([]any)) != 2 || x["events"].([]any)[0] != "email.bounced" {
		t.Fatalf("created %+v, webhook %v", st, x)
	}
	x["status"] = "disabled"
	if got, _ := reg.Get(ctx, call, st.ID); got.Status != connector.RemotePaused {
		t.Errorf("a disabled webhook: %+v", got)
	}
	spec.Events = []string{"email.delivered"}
	if _, err := reg.Update(ctx, call, st.ID, spec); err != nil || f.webhooks[st.ID]["status"] != "enabled" || f.webhooks[st.ID]["events"].([]any)[0] != "email.delivered" {
		t.Errorf("updated %v: %v", f.webhooks[st.ID], err)
	}
	if _, ok, err := reg.Find(ctx, call, spec.Name); ok || err != nil || len(f.webhooks) != 0 {
		t.Errorf("found after a crash: %v %v, left %d", ok, err, len(f.webhooks))
	}
	if err := reg.Delete(ctx, call, st.ID); err != nil {
		t.Errorf("deleting one gone: %v", err)
	}
	if _, err := reg.Get(ctx, call, st.ID); err == nil {
		t.Error("a deleted webhook was read")
	}
}

func TestRegisters(t *testing.T) {
	c := New(Options{})
	if err := connector.NewRegistry().Register(c); err != nil {
		t.Fatal(err)
	}
	if spec := c.Manifest.Triggers["email_events"]; !spec.Remote() || spec.Verify.Scheme != "svix" {
		t.Errorf("email_events %+v", spec)
	}
}
