package runtimetest

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/israel-duff/taskiem/engine/connector"
	"github.com/israel-duff/taskiem/engine/history"
)

// StepRun is how one connector step ended when the engine ran it.
type StepRun struct {
	// Status is the run's: completed, failed or needs_reconciliation.
	Status string
	// Output is the step's recorded output, when it completed; Reconciled
	// says it came from the reconcile action, not from the send.
	Output     any
	Reconciled bool
	// Failures are the step's failed attempts in order (kind, message and
	// what the engine did next: retry, reconcile, park, fail).
	Failures []history.FailedPayload
	// Keys are the idempotency keys the engine derived, one per attempt
	// group: more than one means it sent the effect again under a new key.
	Keys []string
}

// RunConnectorStep runs one step of a real connector through the engine:
// the worker sends it, classifies what came back and, for an unknown
// outcome, drives the connector's reconcile action as it does in
// production, with the input the worker really builds. A connector's own
// tests call an action with the input they choose, which cannot show
// that the engine gives the reconcile action what it needs; this can.
//
// c is the connector with its BaseURL pointed at the test's fake provider
// (the harness lets loopback through its egress guard); creds become its
// prod connection "main". The step is retried up to three times. Set
// e.CallTimeout low before calling (reconcile waits out the call deadline
// of the attempt it reconciles); unset, this uses 500ms.
func (e *Env) RunConnectorStep(t testing.TB, c *connector.Connector, creds map[string]string, action string, input map[string]any) StepRun {
	t.Helper()
	ctx := context.Background()
	if e.CallTimeout == 0 {
		e.CallTimeout = 500 * time.Millisecond
	}
	if err := e.Registry.Register(c); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Vault.CreateConnection(ctx, e.Tenant, "prod", c.Manifest.ID, "main", c.Manifest.Auth.Type, creds, "test"); err != nil {
		t.Fatal(err)
	}
	step, err := json.Marshal(map[string]any{"id": "step", "type": "connector", "connector": c.Ref(), "action": action, "connection": "main",
		"input": input, "retry": map[string]any{"max": 3, "initial": "10ms", "backoff": "fixed"}})
	if err != nil {
		t.Fatal(err)
	}
	wf := e.Publish(t, `{"schema":"wd/v1","id":"wf_step","version":1,"name":"step","trigger":{"type":"manual"},"steps":[`+string(step)+`],"settings":{}}`)
	ref := e.Start(t, wf, map[string]any{})
	settled := func() bool {
		e.Drain(t)
		switch e.Status(t, ref) {
		case "completed", "failed", "needs_reconciliation":
			return true
		}
		return false
	}
	WaitFor(t, 30*time.Second, "the step to settle", settled)
	out := StepRun{Status: e.Status(t, ref)}
	h, err := e.Store.RunHistory(ctx, ref)
	if err != nil {
		t.Fatal(err)
	}
	for _, ev := range h {
		if ev.StepID != "step" {
			continue
		}
		switch ev.Type {
		case history.StepCompleted:
			var p history.CompletedPayload
			_ = json.Unmarshal(ev.Payload, &p)
			out.Output, out.Reconciled = p.Output, p.Reconciled
		case history.StepFailed:
			var p history.FailedPayload
			_ = json.Unmarshal(ev.Payload, &p)
			out.Failures = append(out.Failures, p)
		case history.EffectIntent:
			var p history.IntentPayload
			_ = json.Unmarshal(ev.Payload, &p)
			if len(out.Keys) == 0 || out.Keys[len(out.Keys)-1] != p.Key {
				out.Keys = append(out.Keys, p.Key)
			}
		}
	}
	return out
}
