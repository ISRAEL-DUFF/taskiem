package runtime_test

import (
	"context"
	"strings"
	"testing"

	"github.com/israel-duff/taskiem/engine/decide"
	"github.com/israel-duff/taskiem/engine/history"
	rt "github.com/israel-duff/taskiem/engine/runtime/runtimetest"
)

const formFlow = `{"schema":"wd/v1","id":"wf_kycform","version":1,"name":"kyc","trigger":{"type":"manual"},
  "inputs":{"schema":{"type":"object","properties":{"phone":{"type":"string","x-pii":"phone"},"name":{"type":"string","x-pii":"name"}}}},
  "steps":[
    {"id":"ask","type":"form","config":{"title":"Your bank","to":{"phone":"=trigger.body.phone"},"via":["sms"],
      "schema":{"type":"object","required":["bank"],"properties":{"bank":{"type":"string"}}},
      "prefill":{"name":"=trigger.body.name"},"timeout":"48h","remind":["24h"],"on_timeout":"ON"}},
    {"id":"after","type":"transform","needs":["ask"],"config":{"output":{"bank":"=has(steps.ask.output.bank) ? steps.ask.output.bank : 'none'"}}}]}`

func TestFormStep(t *testing.T) {
	ctx := context.Background()
	for _, onTimeout := range []string{"continue", "fail"} {
		e := rt.New(t)
		wf := e.Publish(t, strings.Replace(formFlow, `"ON"`, `"`+onTimeout+`"`, 1))
		run := e.Start(t, wf, map[string]any{"body": map[string]any{"phone": "+2348031234567", "name": "Bola Ade"}})
		e.Drain(t)
		req := decide.FormRequestID(run.ID.String(), "ask")
		var status, recipient, prefill string
		var remind []string
		if err := e.DB.Admin.QueryRow(ctx, `SELECT status, recipient::text, prefill::text, remind::text[] FROM form_requests WHERE id = $1`, req).Scan(&status, &recipient, &prefill, &remind); err != nil {
			t.Fatal(err)
		}
		if status != "pending" || strings.Contains(recipient, "8031234567") || strings.Contains(prefill, "Bola") || len(remind) != 1 || remind[0] != "24:00:00" {
			t.Fatalf("request: %s %s %s %v", status, recipient, prefill, remind)
		}
		if onTimeout == "fail" {
			// Answered: the step's output is the answers.
			if _, _, err := e.Store.DeliverSignalOnce(ctx, e.Tenant, "prod", history.FormEvent, req, "sub-1", map[string]any{"bank": "GTBank"}); err != nil {
				t.Fatal(err)
			}
			e.Drain(t)
			if st := e.Status(t, run); st != "completed" {
				t.Fatalf("answered: %s", st)
			}
			continue
		}
		// Not answered in time: on_timeout continue completes it.
		if _, err := e.DB.Admin.Exec(ctx, `UPDATE timers SET fire_at = now() WHERE run_id = $1`, run.ID); err != nil {
			t.Fatal(err)
		}
		e.Drain(t)
		if st := e.Status(t, run); st != "completed" {
			t.Fatalf("timed out: %s", st)
		}
	}

	// Cancelling the run withdraws the link.
	e := rt.New(t)
	wf := e.Publish(t, strings.Replace(formFlow, `"ON"`, `"fail"`, 1))
	run := e.Start(t, wf, map[string]any{"body": map[string]any{"phone": "+2348031234567", "name": "Bola"}})
	e.Drain(t)
	if err := e.Store.CancelRun(ctx, run, "test", true); err != nil {
		t.Fatal(err)
	}
	var status string
	_ = e.DB.Admin.QueryRow(ctx, `SELECT status FROM form_requests WHERE run_id = $1`, run.ID).Scan(&status)
	if status != "cancelled" {
		t.Fatalf("after cancelling: %s", status)
	}
}
