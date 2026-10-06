package api_test

import (
	"bufio"
	"net/http"
	"strings"
	"testing"
	"time"
)

// sse reads a server-sent event stream until it ends, returning each
// event as "id event type" lines (data reduced to its "type" field).
func sse(t *testing.T, c *client, path string, hdr ...string) []string {
	t.Helper()
	req, _ := http.NewRequest("GET", c.base+path, nil)
	req.Header.Set("Authorization", "Bearer "+c.token)
	for i := 0; i+1 < len(hdr); i += 2 {
		req.Header.Set(hdr[i], hdr[i+1])
	}
	cl := &http.Client{Timeout: 20 * time.Second}
	resp, err := cl.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if ct := resp.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("content type %q", ct)
	}
	var out []string
	var id, event, typ string
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		line := sc.Text()
		switch {
		case strings.HasPrefix(line, "id: "):
			id = strings.TrimPrefix(line, "id: ")
		case strings.HasPrefix(line, "event: "):
			event = strings.TrimPrefix(line, "event: ")
		case strings.HasPrefix(line, "data: "):
			if i := strings.Index(line, `"type":"`); i >= 0 {
				typ = strings.SplitN(line[i+8:], `"`, 2)[0]
			}
		case line == "" && event != "":
			out = append(out, strings.TrimSpace(id+" "+event+" "+typ))
			if event == "end" {
				return out
			}
			id, event, typ = "", "", ""
		}
	}
	return out
}

func TestRunStream(t *testing.T) {
	w := newWorld(t)
	owner := w.tenant(t, "Acme", "owner@acme.test")
	wf := publishFlow(t, owner, loanFlow)
	run := owner.must(201, "POST", "/v1/workflows/"+wf+"/runs", map[string]any{"input": map[string]any{"bvn": "22212345678", "amount": 5000}})["run_id"].(string)

	got := make(chan []string, 1)
	go func() { got <- sse(t, owner, "/v1/runs/"+run+"/stream") }()
	time.Sleep(500 * time.Millisecond) // the stream is open and has sent what exists
	owner.must(200, "POST", "/v1/runs/"+run+"/cancel", nil)
	var events []string
	select {
	case events = <-got:
	case <-time.After(15 * time.Second):
		t.Fatal("the stream did not end with the run")
	}
	if len(events) < 3 || events[0] != "1 run_event RunStarted" || events[len(events)-1] != "end" || !strings.HasSuffix(events[len(events)-2], "run_event RunCancelled") {
		t.Fatalf("stream: %v", events)
	}
	if strings.Contains(strings.Join(events, " "), "22212345678") {
		t.Error("the stream shows sealed personal data")
	}

	// Resuming after the first event sends the rest only.
	resumed := sse(t, owner, "/v1/runs/"+run+"/stream", "Last-Event-ID", "1")
	if len(resumed) != len(events)-1 || resumed[0] == events[0] {
		t.Errorf("resumed: %v (full: %v)", resumed, events)
	}

	// Another tenant cannot follow the run.
	other := w.tenant(t, "Other", "owner@other.test")
	other.must(404, "GET", "/v1/runs/"+run+"/stream", nil)
}
