package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeTaskiem checks each delivery's signature, starts a run for it after
// a little while, and completes its reply step a little later.
type fakeTaskiem struct {
	mu   sync.Mutex
	runs map[string][]map[string]any // by sender, newest first
	n    int
	bad  int
}

func (f *fakeTaskiem) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.Method == "POST" && strings.HasPrefix(r.URL.Path, "/hooks/t1/connectors/whatsapp@1/messages"):
		body, _ := io.ReadAll(r.Body)
		if r.Header.Get("X-Hub-Signature-256") != Sign("s3cret", body) || r.URL.Query().Get("env") != "prod" {
			f.mu.Lock()
			f.bad++
			f.mu.Unlock()
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		var d struct {
			Entry []struct {
				Changes []struct {
					Value struct {
						Messages []map[string]any `json:"messages"`
					} `json:"value"`
				} `json:"changes"`
			} `json:"entry"`
		}
		_ = json.Unmarshal(body, &d)
		msg := d.Entry[0].Changes[0].Value.Messages[0]
		from := msg["from"].(string)
		f.mu.Lock()
		f.n++
		id := fmt.Sprintf("run-%d", f.n)
		now := time.Now()
		run := map[string]any{"id": id, "started_at": now.Add(20 * time.Millisecond), "replied": now.Add(70 * time.Millisecond), "type": msg["type"]}
		f.runs[from] = append([]map[string]any{run}, f.runs[from]...)
		f.mu.Unlock()
		w.WriteHeader(http.StatusOK)
	case r.Method == "GET" && r.URL.Path == "/v1/runs":
		if r.Header.Get("Authorization") != "Bearer tsk_key_test" || r.URL.Query().Get("workflow") != "wf1" {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		f.mu.Lock()
		defer f.mu.Unlock()
		out := []any{}
		if rs := f.runs[r.URL.Query().Get("correlation_key")]; len(rs) > 0 && time.Now().After(rs[0]["started_at"].(time.Time)) {
			out = append(out, map[string]any{"id": rs[0]["id"], "started_at": rs[0]["started_at"]})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"runs": out})
	case r.Method == "GET" && strings.HasPrefix(r.URL.Path, "/v1/runs/"):
		id := strings.TrimPrefix(r.URL.Path, "/v1/runs/")
		f.mu.Lock()
		defer f.mu.Unlock()
		for _, rs := range f.runs {
			for _, run := range rs {
				if run["id"] != id {
					continue
				}
				events := []any{map[string]any{"type": "RunStarted", "recorded_at": run["started_at"]}}
				status := "running"
				if at := run["replied"].(time.Time); time.Now().After(at) {
					events = append(events, map[string]any{"type": "StepCompleted", "step_id": "reply", "recorded_at": at})
					status = "completed"
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"run": map[string]any{"status": status}, "events": events})
				return
			}
		}
		w.WriteHeader(http.StatusNotFound)
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

func TestReplybench(t *testing.T) {
	f := &fakeTaskiem{runs: map[string][]map[string]any{}}
	srv := httptest.NewServer(f)
	defer srv.Close()
	c := config{base: srv.URL, tenant: "t1", env: "prod", appSecret: "s3cret", phoneID: "1065", apiKey: "tsk_key_test",
		workflow: "wf1", replyStep: "reply", n: 6, concurrency: 3, text: "Rice?", fromPrefix: "2349990", timeout: 5 * time.Second}
	res := run(context.Background(), c, srv.Client())
	senders := map[string]bool{}
	for _, r := range res {
		if r.err != nil {
			t.Fatalf("%s: %v", r.from, r.err)
		}
		senders[r.from] = true
		if r.toReply < 40*time.Millisecond || r.toReply > 60*time.Millisecond {
			t.Errorf("start to reply %v, want about 50ms", r.toReply)
		}
	}
	if len(senders) != 6 || f.bad != 0 {
		t.Fatalf("%d senders, %d refused deliveries", len(senders), f.bad)
	}
	out := report(res, c)
	if !strings.Contains(out, "6 text messages, 3 at once: 6 answered, 0 not") || !strings.Contains(out, "delivery to reply") {
		t.Fatalf("report:\n%s", out)
	}

	// One test number, one message at a time: each run told apart by id.
	c.from, c.concurrency, c.n, c.mediaID = "2348030000000", 1, 3, "media-1"
	res = run(context.Background(), c, srv.Client())
	for _, r := range res {
		if r.err != nil || r.from != "2348030000000" {
			t.Fatalf("%s: %v", r.from, r.err)
		}
	}
	if got := len(f.runs["2348030000000"]); got != 3 || f.runs["2348030000000"][0]["type"] != "audio" {
		t.Fatalf("%d runs from the test number", got)
	}
	if !strings.Contains(report(res, c), "3 voice messages") {
		t.Fatal(report(res, c))
	}

	// Clocks that differ are said.
	if out := report([]result{{from: "x", toStart: time.Second, toReply: time.Second, skew: -5 * time.Second}}, c); !strings.Contains(out, "differ by about 5s") {
		t.Fatalf("no skew note:\n%s", out)
	}
	if out := report([]result{{from: "x", toStart: time.Second, toReply: time.Second}}, c); strings.Contains(out, "differ") {
		t.Fatalf("a note without skew:\n%s", out)
	}
	// A wrong secret: the delivery is refused and said so.
	c.appSecret, c.n = "wrong", 1
	if res := run(context.Background(), c, srv.Client()); res[0].err == nil || !strings.Contains(res[0].err.Error(), "401") {
		t.Fatalf("a refused delivery: %v", res[0].err)
	}
}
