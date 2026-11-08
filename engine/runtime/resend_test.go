package runtime_test

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/israel-duff/taskiem/engine/connector"
	"github.com/israel-duff/taskiem/engine/effects"
	"github.com/israel-duff/taskiem/engine/runtime"
	rt "github.com/israel-duff/taskiem/engine/runtime/runtimetest"
)

const chatManifest = `
manifest: connector/v1
id: chatty
version: 1.0.0
name: Chatty
category: messaging
auth: { type: none }
actions:
  reply:
    title: Send a reply
    class: unsafe_write
    resend_if_lost: true
    compensate: null
    input: { type: object, properties: { text: { type: string } } }
`

// A chat reply whose answer was lost is sent once more when the step asks
// for it and the action allows it (decision 0062 D11); a second loss
// parks; without the step's request the first loss parks, as before.
func TestLostChatReplyIsSentOnceMore(t *testing.T) {
	e := rt.New(t)
	var calls atomic.Int32
	var lose atomic.Int32 // how many sends lose their answer
	conn := &connector.Connector{Manifest: connector.MustParse([]byte(chatManifest)), Actions: map[string]connector.Action{
		"reply": connector.ActionFunc(func(context.Context, connector.Request) (connector.Response, error) {
			n := calls.Add(1)
			if n <= lose.Load() {
				return connector.Response{}, fmt.Errorf("connection reset after sending: %w", effects.ErrUnknownOutcome)
			}
			return connector.Response{Output: map[string]any{"message_id": "m1"}}, nil
		}),
	}}
	if err := e.Registry.Register(conn); err != nil {
		t.Fatal(err)
	}
	run := func(asks bool) string {
		effect := ""
		if asks {
			effect = `,"effect":{"resend_if_lost":true}`
		}
		wf := e.Publish(t, wfDoc(`{"id":"reply","type":"connector","connector":"chatty@1","action":"reply","input":{"text":"Hi"},"retry":{"max":3,"initial":"10ms","backoff":"fixed"}`+effect+`}`, ""))
		ref := e.Start(t, wf, map[string]any{})
		// The resend waits its retry delay (10 ms): drain until it settles.
		for i := 0; i < 50; i++ {
			e.Drain(t)
			if st := e.Status(t, ref); st != "running" {
				return st
			}
			time.Sleep(20 * time.Millisecond)
		}
		return e.Status(t, ref)
	}

	calls.Store(0)
	lose.Store(1)
	if st := run(true); st != "completed" || calls.Load() != 2 {
		t.Fatalf("one lost answer: %s after %d sends, want completed after 2", st, calls.Load())
	}
	calls.Store(0)
	lose.Store(2)
	if st := run(true); st != "needs_reconciliation" || calls.Load() != 2 {
		t.Fatalf("two lost answers: %s after %d sends, want parked after 2", st, calls.Load())
	}
	calls.Store(0)
	lose.Store(1)
	if st := run(false); st != "needs_reconciliation" || calls.Load() != 1 {
		t.Fatalf("without the request: %s after %d sends, want parked after 1", st, calls.Load())
	}
}

// A worker that died after sending, before recording the answer, leaves an
// open intent: the next worker sends the chat reply once more when the step
// asks for it, and parks it when it does not.
func TestReplyLostInACrashIsSentOnceMore(t *testing.T) {
	e := rt.New(t)
	var calls atomic.Int32
	conn := &connector.Connector{Manifest: connector.MustParse([]byte(chatManifest)), Actions: map[string]connector.Action{
		"reply": connector.ActionFunc(func(context.Context, connector.Request) (connector.Response, error) {
			calls.Add(1)
			return connector.Response{Output: map[string]any{"message_id": "m1"}}, nil
		}),
	}}
	if err := e.Registry.Register(conn); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		asks  bool
		want  string
		sends int32
	}{{true, "completed", 2}, {false, "needs_reconciliation", 1}} {
		calls.Store(0)
		effect := ""
		if c.asks {
			effect = `,"effect":{"resend_if_lost":true}`
		}
		wf := e.Publish(t, wfDoc(`{"id":"reply","type":"connector","connector":"chatty@1","action":"reply","input":{"text":"Hi"}`+effect+`}`, ""))
		ref := e.Start(t, wf, map[string]any{})
		crashing := e.Worker("crashy")
		crashing.Lease = 300 * time.Millisecond
		crashing.Hooks = &runtime.Hooks{AfterCall: func(string, int) error { return errors.New("kill -9") }}
		if _, err := crashing.RunOnce(ctx); err != nil {
			t.Fatal(err)
		}
		time.Sleep(400 * time.Millisecond) // the lease expires
		e.Drain(t)
		if st := e.Status(t, ref); st != c.want || calls.Load() != c.sends {
			t.Errorf("asks %v: %s after %d sends, want %s after %d: %s", c.asks, st, calls.Load(), c.want, c.sends, types(events(t, e, ref)))
		}
	}
}
