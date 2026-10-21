package ai

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// fakeChat answers chat completions: the statuses in fails first, then a
// proper answer.
func fakeChat(t *testing.T, fails []int, retryAfter string) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := int(calls.Add(1))
		if r.URL.Path != "/v1/chat/completions" || r.Header.Get("Authorization") != "Bearer sk-test" {
			t.Errorf("request %s %q", r.URL.Path, r.Header.Get("Authorization"))
		}
		if n <= len(fails) {
			if retryAfter != "" {
				w.Header().Set("Retry-After", retryAfter)
			}
			w.WriteHeader(fails[n-1])
			_, _ = w.Write([]byte(`{"error":{"message":"Rate limit exceeded: free-models-per-min","code":429}}`))
			return
		}
		_, _ = w.Write([]byte(`{"model":"m","choices":[{"message":{"role":"assistant","content":"{\"ok\":true}"},"finish_reason":"stop"}],"usage":{"prompt_tokens":3,"completion_tokens":2}}`))
	}))
	t.Cleanup(srv.Close)
	return srv, &calls
}

func provider(url string, retries int, slept *[]time.Duration) *SelfHosted {
	return NewSelfHosted(SelfHostedOptions{BaseURL: url + "/v1/", Model: "m", APIKey: "sk-test", Retries: retries,
		sleep: func(_ context.Context, d time.Duration) error { *slept = append(*slept, d); return nil }})
}

func TestSelfHostedRetries(t *testing.T) {
	req := Request{Messages: []Message{{Role: "user", Text: "hi"}}, Schema: map[string]any{"type": "object"}}

	// A rate limit, then a briefly unavailable server: tried again, answered.
	srv, calls := fakeChat(t, []int{429, 503}, "")
	var slept []time.Duration
	r, err := provider(srv.URL, 0, &slept).Complete(context.Background(), req)
	if err != nil || string(r.JSON) != `{"ok":true}` || calls.Load() != 3 || len(slept) != 2 || slept[0] != time.Second || slept[1] != 2*time.Second {
		t.Fatalf("retried: %v %v calls=%d slept=%v", r, err, calls.Load(), slept)
	}

	// Retry-After is honoured, at most 30 seconds.
	srv, _ = fakeChat(t, []int{429}, "120")
	slept = nil
	if _, err := provider(srv.URL, 0, &slept).Complete(context.Background(), req); err != nil || len(slept) != 1 || slept[0] != 30*time.Second {
		t.Fatalf("Retry-After: %v %v", err, slept)
	}

	// Always limited: it gives up after the retries, saying why.
	srv, calls = fakeChat(t, []int{429, 429, 429, 429, 429}, "0")
	slept = nil
	_, err = provider(srv.URL, 0, &slept).Complete(context.Background(), req)
	if err == nil || !strings.Contains(err.Error(), "status 429: Rate limit exceeded: free-models-per-min") || calls.Load() != 4 {
		t.Fatalf("gave up: %v calls=%d", err, calls.Load())
	}

	// A refusal that waiting does not change (400) is not tried again.
	srv, calls = fakeChat(t, []int{400}, "")
	slept = nil
	if _, err := provider(srv.URL, 0, &slept).Complete(context.Background(), req); err == nil || calls.Load() != 1 || len(slept) != 0 {
		t.Fatalf("400: %v calls=%d", err, calls.Load())
	}

	// Retries -1: once.
	srv, calls = fakeChat(t, []int{429}, "")
	slept = nil
	if _, err := provider(srv.URL, -1, &slept).Complete(context.Background(), req); err == nil || calls.Load() != 1 {
		t.Fatalf("no retries: %v calls=%d", err, calls.Load())
	}
}
