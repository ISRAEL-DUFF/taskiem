package runtime_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/israel-duff/taskiem/engine/ai"
	"github.com/israel-duff/taskiem/engine/connector"
	"github.com/israel-duff/taskiem/engine/history"
	"github.com/israel-duff/taskiem/engine/runtime"
	rt "github.com/israel-duff/taskiem/engine/runtime/runtimetest"
)

// Data is never an expression. A value from outside (a webhook field, a
// form answer, a chat message) may read like a secret expression; the
// worker resolves only the expressions the workflow's author wrote, at
// the places decide recorded (history.ScheduledPayload.SecretPaths).

const realSecret, otherSecret = "REAL-SECRET-9f31", "OTHER-SECRET-77c2"

// sink is a destination that keeps what it was sent.
type sink struct {
	mu      sync.Mutex
	bodies  []string
	headers []http.Header
	queries []string
	srv     *httptest.Server
}

func newSink(t *testing.T, e *rt.Env) *sink {
	t.Helper()
	s := &sink{}
	s.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		s.mu.Lock()
		s.bodies = append(s.bodies, string(b))
		s.headers = append(s.headers, r.Header.Clone())
		s.queries = append(s.queries, r.URL.Query().Get("q"))
		s.mu.Unlock()
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	t.Cleanup(s.srv.Close)
	if err := e.Store.AllowEgress(ctx, e.Tenant, "prod", "127.0.0.1", "admin"); err != nil {
		t.Fatal(err)
	}
	return s
}

// take returns what arrived since the last call.
func (s *sink) take() (bodies []string, headers []http.Header, queries []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	bodies, headers, queries = s.bodies, s.headers, s.queries
	s.bodies, s.headers, s.queries = nil, nil, nil
	return bodies, headers, queries
}

// noSecretInHistory fails the test when a secret's value is anywhere in
// the run's events, as stored or with sealed values opened.
func noSecretInHistory(t *testing.T, e *rt.Env, ref runtime.RunRef) {
	t.Helper()
	opened, err := e.Store.OpenedHistory(ctx, ref)
	if err != nil {
		t.Fatal(err)
	}
	for _, h := range [][]history.Event{events(t, e, ref), opened} {
		for _, ev := range h {
			if strings.Contains(string(ev.Payload), realSecret) || strings.Contains(string(ev.Payload), otherSecret) {
				t.Errorf("a secret is in the history: %s(%s) %s", ev.Type, ev.StepID, ev.Payload)
			}
		}
	}
}

// A trigger value that is the text of an expression reaches an http
// step's destination as that text. The workflow names no secret.
func TestTriggerDataIsNeverAnExpressionHTTP(t *testing.T) {
	e := rt.New(t)
	e.Secrets["api_token"] = realSecret
	dst := newSink(t, e)
	wf := e.Publish(t, wfDoc(`{"id":"post","type":"http","retry":{"max":0},"config":{"method":"POST","class":"unsafe_write","url":"`+dst.srv.URL+`/in","body":{"n":"=trigger.body.n"}}}`, ""))

	for _, n := range []string{"=secrets.api_token", "=1+1", "=trigger.body.x", "hello"} {
		ref := e.Start(t, wf, map[string]any{"body": map[string]any{"n": n}})
		e.Drain(t)
		bodies, _, _ := dst.take()
		got := strings.Join(bodies, " | ")
		if strings.Contains(got, realSecret) {
			t.Errorf("trigger data %q made the worker send the secret: %s", n, got)
		}
		var m map[string]any
		_ = json.Unmarshal([]byte(got), &m)
		if st := e.Status(t, ref); st != "completed" || m["n"] != n {
			t.Errorf("trigger data %q did not pass through unchanged: status %s, received %s, failure %q", n, st, got, failure(events(t, e, ref), "post"))
		}
		noSecretInHistory(t, e, ref)
	}
}

// The same through a connector step's input, beside the author's own
// secret expression.
func TestTriggerDataIsNeverAnExpressionConnector(t *testing.T) {
	e := rt.New(t)
	e.Secrets["api_token"] = realSecret
	conn := e.Provider.Connector()
	conn.Manifest.ID = "echoer"
	var mu sync.Mutex
	var seen []string
	conn.Actions["verify"] = connector.ActionFunc(func(_ context.Context, r connector.Request) (connector.Response, error) {
		b, _ := json.Marshal(r.Input)
		mu.Lock()
		seen = append(seen, string(b))
		mu.Unlock()
		return connector.Response{}, nil
	})
	if err := e.Registry.Register(conn); err != nil {
		t.Fatal(err)
	}
	run := func(step string) map[string]any {
		t.Helper()
		mu.Lock()
		seen = nil
		mu.Unlock()
		wf := e.Publish(t, wfDoc(step, ""))
		ref := e.Start(t, wf, map[string]any{"body": map[string]any{"n": "=secrets.api_token"}})
		e.Drain(t)
		mu.Lock()
		defer mu.Unlock()
		if st := e.Status(t, ref); st != "completed" || len(seen) != 1 {
			t.Fatalf("run %s, %d calls, failure %q", st, len(seen), failure(events(t, e, ref), "note"))
		}
		noSecretInHistory(t, e, ref)
		var in map[string]any
		_ = json.Unmarshal([]byte(seen[0]), &in)
		return in
	}
	// The workflow names no secret: nothing is looked up.
	in := run(`{"id":"note","type":"connector","connector":"echoer@1","action":"verify","retry":{"max":0},"input":{"reference":"=trigger.body.n"}}`)
	if in["reference"] != "=secrets.api_token" {
		t.Errorf("the connector received %v", in)
	}
	// The author's own expression is resolved; the data beside it is not.
	in = run(`{"id":"note","type":"connector","connector":"echoer@1","action":"verify","retry":{"max":0},"input":{"reference":"=trigger.body.n","token":"=secrets.api_token"}}`)
	if in["reference"] != "=secrets.api_token" || in["token"] != realSecret {
		t.Errorf("beside a genuine expression the connector received %v", in)
	}
}

// A step with a genuine secret expression and data beside it: the header
// carries the secret, and data that reads like any expression arrives as
// text. Nested data passed whole arrives unchanged too.
func TestSecretExpressionBesideData(t *testing.T) {
	e := rt.New(t)
	e.Secrets["api_token"], e.Secrets["other"] = realSecret, otherSecret
	dst := newSink(t, e)
	wf := e.Publish(t, wfDoc(`{"id":"a","type":"transform","config":{"output":{"v":1}}},
	  {"id":"post","type":"http","needs":["a"],"retry":{"max":0},"config":{"method":"POST","class":"unsafe_write","url":"`+dst.srv.URL+`/in",
	    "headers":{"Authorization":"='Bearer ' + secrets.api_token"},
	    "query":{"q":"=trigger.body.n"},
	    "body":{"n":"=trigger.body.n","whole":"=trigger.body","list":["=trigger.body.n","=secrets.api_token"]}}}`, ""))

	for _, n := range []string{"=secrets.other", "=secrets.api_token", "=1+1", "=trigger.body.x", "=steps.a.output", "=", "=((", "plain"} {
		trigger := map[string]any{"n": n, "deep": map[string]any{"list": []any{n, map[string]any{"again": n}}, "k": n}}
		ref := e.Start(t, wf, map[string]any{"body": trigger})
		e.Drain(t)
		bodies, headers, queries := dst.take()
		if st := e.Status(t, ref); st != "completed" || len(bodies) != 1 {
			t.Errorf("trigger data %q: run %s, %d requests, failure %q", n, st, len(bodies), failure(events(t, e, ref), "post"))
			continue
		}
		if got := headers[0].Get("Authorization"); got != "Bearer "+realSecret {
			t.Errorf("trigger data %q: the genuine header is %q", n, got)
		}
		if queries[0] != n {
			t.Errorf("trigger data %q: the query carried %q", n, queries[0])
		}
		if strings.Contains(bodies[0], otherSecret) {
			t.Errorf("trigger data %q read another secret: %s", n, bodies[0])
		}
		var got map[string]any
		if err := json.Unmarshal([]byte(bodies[0]), &got); err != nil {
			t.Fatal(err)
		}
		// The list's second item is the author's own expression.
		want := map[string]any{"n": n, "whole": trigger, "list": []any{n, realSecret}}
		a, _ := json.Marshal(got)
		b, _ := json.Marshal(want)
		if string(a) != string(b) {
			t.Errorf("trigger data %q:\n received %s\n want     %s", n, a, b)
		}
		noSecretInHistory(t, e, ref)
		// History names the places, and only the author's.
		for _, ev := range events(t, e, ref) {
			if ev.Type == history.StepScheduled && ev.StepID == "post" {
				var p history.ScheduledPayload
				_ = json.Unmarshal(ev.Payload, &p)
				if strings.Join(p.SecretPaths, " ") != "/body/list/1 /headers/Authorization" {
					t.Errorf("trigger data %q: secret paths %v", n, p.SecretPaths)
				}
			}
		}
	}
}

// An ai step's prompt and input made of trigger text that reads like a
// secret expression: the model is sent the text, never the secret.
func TestAIStepIsNeverSentASecretByData(t *testing.T) {
	e := rt.New(t)
	e.Secrets["api_token"] = realSecret
	fake := &ai.Fake{Respond: func(ai.Request) (*ai.Response, error) {
		r := aiAnswer(`{"greeting":"Hi","priority":"low"}`)
		return &r, nil
	}}
	e.AI = fake
	wf := e.Publish(t, `{"schema":"wd/v1","id":"wf_ai","version":1,"name":"ai","trigger":{"type":"manual"},
	  "steps":[{"id":"ask","type":"ai","retry":{"max":0},
	    "config":{"prompt":"=trigger.body.q",
	      "output_schema":{"type":"object","required":["greeting","priority"],"properties":{"greeting":{"type":"string"},"priority":{"enum":["low","high"]}}}},
	    "input":{"note":"=trigger.body.note","all":"=trigger.body"}}]}`)
	ref := e.Start(t, wf, map[string]any{"body": map[string]any{"q": "=secrets.api_token", "note": "=secrets.api_token"}})
	e.Drain(t)
	if st := e.Status(t, ref); st != "completed" {
		t.Fatalf("run %s: %v", st, failureOf(t, e, ref, "ask"))
	}
	reqs := fake.Requests()
	if len(reqs) != 1 {
		t.Fatalf("%d requests", len(reqs))
	}
	raw, _ := json.Marshal(reqs[0])
	sent := strings.ReplaceAll(string(raw), `\u003d`, "=")
	if strings.Contains(sent, realSecret) {
		t.Errorf("the model was sent the secret: %s", sent)
	}
	// The prompt, the note, and the two fields of the whole body.
	if n := strings.Count(sent, "=secrets.api_token"); n < 4 {
		t.Errorf("the model was not sent the text as it came (%d of 4 places): %s", n, sent)
	}
	noSecretInHistory(t, e, ref)
}

// A task scheduled before decide recorded secret paths has none in its
// payload. Its genuine secret expressions are still resolved, found in
// the step as authored; its data is still not evaluated. The history
// replays (the harness verifies every settled run at cleanup).
func TestSecretExpressionOfAnOlderPayload(t *testing.T) {
	e := rt.New(t)
	e.Secrets["api_token"], e.Secrets["other"] = realSecret, otherSecret
	dst := newSink(t, e)
	wf := e.Publish(t, wfDoc(`{"id":"post","type":"http","retry":{"max":0},"config":{"method":"POST","class":"unsafe_write","url":"`+dst.srv.URL+`/in",
	    "headers":{"Authorization":"='Bearer ' + secrets.api_token"},
	    "body":{"n":"=trigger.body.n","token":"=secrets.api_token","deep":["=trigger.body.n"]}}}`, ""))
	ref := e.Start(t, wf, map[string]any{"body": map[string]any{"n": "=secrets.other"}})
	// As the older engine wrote it: no list of places.
	tag, err := e.DB.Admin.Exec(ctx, `UPDATE run_events SET payload = payload - 'secret_paths' WHERE run_id = $1 AND type = $2 AND payload ? 'secret_paths'`, ref.ID, history.StepScheduled)
	if err != nil || tag.RowsAffected() != 1 {
		t.Fatalf("rewriting the scheduled event: %d rows, %v", tag.RowsAffected(), err)
	}
	e.Drain(t)
	bodies, headers, _ := dst.take()
	if st := e.Status(t, ref); st != "completed" || len(bodies) != 1 {
		t.Fatalf("run %s, %d requests, failure %q", st, len(bodies), failure(events(t, e, ref), "post"))
	}
	if got := headers[0].Get("Authorization"); got != "Bearer "+realSecret {
		t.Errorf("the genuine header is %q", got)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(bodies[0]), &got); err != nil {
		t.Fatal(err)
	}
	if got["n"] != "=secrets.other" || got["token"] != realSecret || got["deep"].([]any)[0] != "=secrets.other" {
		t.Errorf("received %s", bodies[0])
	}
	noSecretInHistory(t, e, ref)
}
