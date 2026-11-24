package runtime_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/israel-duff/taskiem/engine/connector"
	"github.com/israel-duff/taskiem/engine/effects"
	rt "github.com/israel-duff/taskiem/engine/runtime/runtimetest"
)

const slowManifest = `
manifest: connector/v1
id: slow
version: 1.0.0
name: Slow
category: other
auth: { type: none }
actions:
  long:
    title: A read that may take a while
    class: read
    call_timeout: 3
    input: { type: object }
  short:
    title: A read with the usual bound
    class: read
    input: { type: object }
`

// A read action that declares call_timeout gets that long (hearing a long
// voice note); one that does not keeps the worker's usual bound.
func TestReadActionCallTimeout(t *testing.T) {
	e := rt.New(t)
	e.CallTimeout = 300 * time.Millisecond
	slow := func(ctx context.Context, _ connector.Request) (connector.Response, error) {
		select {
		case <-time.After(time.Second):
			return connector.Response{Output: map[string]any{"done": true}}, nil
		case <-ctx.Done():
			return connector.Response{}, fmt.Errorf("cut short: %w", effects.ErrFatal)
		}
	}
	conn := &connector.Connector{Manifest: connector.MustParse([]byte(slowManifest)), Actions: map[string]connector.Action{
		"long": connector.ActionFunc(slow), "short": connector.ActionFunc(slow)}}
	if err := e.Registry.Register(conn); err != nil {
		t.Fatal(err)
	}
	for action, want := range map[string]string{"long": "completed", "short": "failed"} {
		wf := e.Publish(t, wfDoc(`{"id":"call","type":"connector","connector":"slow@1","action":"`+action+`","input":{}}`, ""))
		ref := e.Start(t, wf, map[string]any{})
		rt.WaitFor(t, 20*time.Second, "run to end", func() bool {
			e.Drain(t)
			st := e.Status(t, ref)
			return st == "completed" || st == "failed"
		})
		if st := e.Status(t, ref); st != want {
			t.Errorf("%s: %s, want %s", action, st, want)
		}
	}
}
