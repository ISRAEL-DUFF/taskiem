package runtime_test

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/connector"
	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/effects"
	"github.com/israel-duff/taskiem/engine/history"
	"github.com/israel-duff/taskiem/engine/runtime"
	rt "github.com/israel-duff/taskiem/engine/runtime/runtimetest"
)

// railManifest is a rail with pay_bank (a queued payout), get_payout and
// payout events, as a real rail declares them (connector/v1 rule 16).
const railManifest = `
manifest: connector/v1
id: %s
version: 1.0.0
name: %s
category: payments
kind: rail
auth: { type: none }
base_url: https://api.rail.example
actions:
  send:
    title: Send
    class: idempotent_write
    idempotency: { field: ref, encoding: base32_lower, length: 32, prefix: "tsk_" }
    reconcile: lookup
    input:
      type: object
      required: [kobo, account, bank, holder]
      properties:
        kobo: { type: integer }
        account: { type: string }
        bank: { type: string }
        holder: { type: string }
        memo: { type: string }
        ref: { type: string }
    output: { type: object, properties: { id: { type: string }, state: { type: string }, charge: { type: integer } } }
    pii:
      - { field: account, category: account_number }
      - { field: holder, category: name }
  lookup:
    title: Look up
    class: read
    input: { type: object, required: [id], properties: { id: { type: string } } }
    output: { type: object, properties: { id: { type: string }, state: { type: string } } }
triggers:
  hook:
    type: webhook
    verify: { scheme: none }
    events: [done, bounced]
    event_type: =body.kind
    correlation: =body.id
capabilities:
  pay_bank:
    action: send
    input: { kobo: "=input.amount_kobo", account: "=input.account_number", bank: "=input.bank_code", holder: "=input.account_name", memo: "=input.narration" }
    output: { payout_id: "=output.id", status: "=output.state == 'done' ? 'settled' : 'pending'", fee_kobo: "=output.?charge" }
    fixed: { currency: NGN }
    unsupported: [source]
    settles_by: { trigger: hook, lookup: get_payout }
  get_payout:
    action: lookup
    input: { id: "=input.payout_id" }
    output: { status: "=output.state == 'done' ? 'settled' : output.state == 'bounced' ? 'failed' : 'pending'" }
events:
  payout.settled: { trigger: hook, when: "=body.kind == 'done'", fields: { payout_id: "=body.id" } }
  payout.failed: { trigger: hook, when: "=body.kind == 'bounced'", fields: { payout_id: "=body.id", reason: "=body.?why" } }
`

// rail is a fake rail that counts what it is asked to pay, by key.
type rail struct {
	mu    sync.Mutex
	paid  map[string]int // idempotency key -> times paid
	asked []map[string]any
}

func newRail(t *testing.T, e *rt.Env, id, name string) *rail {
	t.Helper()
	r := &rail{paid: map[string]int{}}
	m, probs := connector.Parse([]byte(strings.ReplaceAll(strings.Replace(railManifest, "%s", id, 1), "%s", name)))
	if len(probs) > 0 {
		t.Fatal(probs)
	}
	c := &connector.Connector{Manifest: m, Actions: map[string]connector.Action{
		"send": connector.ActionFunc(func(_ context.Context, req connector.Request) (connector.Response, error) {
			r.mu.Lock()
			defer r.mu.Unlock()
			if req.IdempotencyKey == "" {
				return connector.Response{}, effects.ErrFatal
			}
			r.paid[req.IdempotencyKey]++
			r.asked = append(r.asked, req.Input)
			return connector.Response{Output: map[string]any{"id": "po_" + req.IdempotencyKey[4:12], "state": "queued", "charge": 1075}}, nil
		}),
		"lookup": connector.ActionFunc(func(_ context.Context, req connector.Request) (connector.Response, error) {
			return connector.Response{Output: map[string]any{"id": req.Input["id"], "state": "queued"}}, nil
		}),
	}}
	if err := e.Registry.Register(c); err != nil {
		t.Fatal(err)
	}
	return r
}

func connect(t *testing.T, e *rt.Env, name, connectorID string) {
	t.Helper()
	err := db.InTenantTx(ctx, e.DB.App, []uuid.UUID{e.Tenant}, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO connections (id, tenant_id, environment, connector, name, auth_type) VALUES ($1, $2, 'prod', $3, $4, 'none')`,
			uuid.Must(uuid.NewV7()), e.Tenant, connectorID, name)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
}

const payrollWD = `{"schema":"wd/v1","id":"wf_pay","version":1,"name":"Pay","trigger":{"type":"manual"},"steps":[
  {"id":"pay_all","type":"foreach","config":{"items":"=trigger.people","max_concurrency":5,"steps":[
    {"id":"pay","type":"capability","uses":"pay_bank","connection":"rail",
     "input":{"amount_kobo":"=item.kobo","bank_code":"058","account_number":"=item.account","account_name":"=item.name","narration":"Salary"},
     "effect":{"idempotency_seed":"='payroll-1:' + item.id"},"retry":{"max":3,"initial":"10ms","backoff":"fixed"}},
    {"id":"settle","type":"capability","uses":"await_settlement","needs":["pay"],"timeout":"72h",
     "when":"=steps.pay.output.status == 'pending'","input":{"payout_id":"=steps.pay.output.payout_id"}}
  ]}}
]}`

var people = map[string]any{"people": []any{
	map[string]any{"id": "e1", "kobo": 500000, "account": "0123456789", "name": "Bola Ade"},
	map[string]any{"id": "e2", "kobo": 250000, "account": "0987654321", "name": "Ada Obi"},
}}

// A capability step runs as the rail's own action: its input mapped, its
// output the capability's, its key the rail's, and the wait ends on the
// rail's normalised events.
func TestCapabilityStepsRunOnTheRail(t *testing.T) {
	e := rt.New(t)
	r := newRail(t, e, "testrail", "Test Rail")
	connect(t, e, "rail", "testrail")
	wf := e.Publish(t, payrollWD)
	ref := e.Start(t, wf, people)
	e.Drain(t)
	if st := e.Status(t, ref); st != "waiting" && st != "running" {
		t.Fatalf("status %s: %s", st, types(events(t, e, ref)))
	}

	// The rail was asked in its own words, once per person.
	r.mu.Lock()
	if len(r.paid) != 2 || len(r.asked) != 2 {
		t.Fatalf("paid %v asked %v", r.paid, r.asked)
	}
	for k, n := range r.paid {
		if n != 1 {
			t.Errorf("key %s paid %d times", k, n)
		}
	}
	in := r.asked[0]
	if in["bank"] != "058" || in["memo"] != "Salary" || in["kobo"] == nil || in["currency"] != nil {
		t.Errorf("rail input %v", in)
	}
	r.mu.Unlock()

	// steps.pay.output is the capability's; the rail's answer is kept.
	var payouts []string
	for _, ev := range events(t, e, ref) {
		if ev.Type != history.StepCompleted || !strings.HasSuffix(ev.StepID, ".pay") {
			continue
		}
		var p struct {
			Output         map[string]any `json:"output"`
			ProviderOutput map[string]any `json:"provider_output"`
		}
		_ = json.Unmarshal(ev.Payload, &p)
		if p.Output["status"] != "pending" || p.Output["fee_kobo"] != float64(1075) || p.ProviderOutput["state"] != "queued" {
			t.Errorf("%s: %s", ev.StepID, ev.Payload)
		}
		payouts = append(payouts, p.Output["payout_id"].(string))
	}
	if len(payouts) != 2 {
		t.Fatalf("payouts %v", payouts)
	}

	// The run is pinned to its expansion.
	var digest []byte
	if err := db.InTenantTx(ctx, e.DB.App, []uuid.UUID{e.Tenant}, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT expansion FROM runs WHERE id = $1`, ref.ID).Scan(&digest)
	}); err != nil || len(digest) != 32 {
		t.Fatalf("expansion %x %v", digest, err)
	}

	// The rail's normalised events end each wait: one settles, one fails.
	if woke, err := e.Store.DeliverSignal(ctx, e.Tenant, "prod", "testrail@1:payout.settled", payouts[0], map[string]any{"event": "payout.settled", "fields": map[string]any{"payout_id": payouts[0]}}); err != nil || len(woke) != 1 {
		t.Fatalf("settled: %v %v", woke, err)
	}
	if woke, err := e.Store.DeliverSignal(ctx, e.Tenant, "prod", "testrail@1:payout.failed", payouts[1], map[string]any{"event": "payout.failed", "fields": map[string]any{"payout_id": payouts[1], "reason": "Account closed"}}); err != nil || len(woke) != 1 {
		t.Fatalf("failed: %v %v", woke, err)
	}
	e.Drain(t)
	if st := e.Status(t, ref); st != "completed" {
		t.Fatalf("status %s: %s", st, types(events(t, e, ref)))
	}
	got := map[string]string{}
	for _, ev := range events(t, e, ref) {
		if ev.Type == history.StepCompleted && strings.HasSuffix(ev.StepID, ".settle") {
			var p struct {
				Output map[string]any `json:"output"`
			}
			_ = json.Unmarshal(ev.Payload, &p)
			got[ev.StepID] = p.Output["status"].(string) + "/" + str(p.Output["reason"])
		}
	}
	if len(got) != 2 || !strings.Contains(strings.Join(values(got), " "), "settled/") || !strings.Contains(strings.Join(values(got), " "), "failed/Account closed") {
		t.Errorf("settle outcomes %v", got)
	}
}

func str(v any) string {
	s, _ := v.(string)
	return s
}

func values(m map[string]string) []string {
	var out []string
	for _, v := range m {
		out = append(out, v)
	}
	return out
}

// Changing the connection to another rail does not move a run in flight;
// a new run takes the new rail.
func TestCapabilityRunsKeepTheirRail(t *testing.T) {
	e := rt.New(t)
	first := newRail(t, e, "testrail", "Test Rail")
	second := newRail(t, e, "otherrail", "Other Rail")
	connect(t, e, "rail", "testrail")
	wf := e.Publish(t, payrollWD)
	inFlight := e.Start(t, wf, people)
	e.Drain(t)

	if err := db.InTenantTx(ctx, e.DB.App, []uuid.UUID{e.Tenant}, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `UPDATE connections SET connector = 'otherrail' WHERE name = 'rail'`)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	next := e.Start(t, wf, people)
	e.Drain(t)
	first.mu.Lock()
	second.mu.Lock()
	if len(first.paid) != 2 || len(second.paid) != 2 {
		t.Errorf("first rail paid %d, second %d", len(first.paid), len(second.paid))
	}
	first.mu.Unlock()
	second.mu.Unlock()
	// The in-flight run still waits on its own rail's events.
	for _, ev := range events(t, e, inFlight) {
		if ev.Type == history.StepScheduled && strings.Contains(string(ev.Payload), "otherrail") {
			t.Fatalf("the in-flight run moved rail: %s", ev.Payload)
		}
	}
	if st := e.Status(t, next); st != "waiting" && st != "running" {
		t.Errorf("next: %s", st)
	}
}

// A run whose connection cannot do the capability is refused at the start,
// in plain words.
func TestCapabilityRefusedAtStart(t *testing.T) {
	e := rt.New(t)
	connect(t, e, "rail", "fakepay")
	wf := e.Publish(t, payrollWD)
	_, _, err := e.Store.StartRun(ctx, startReq(e, wf))
	if err == nil || !strings.Contains(err.Error(), "cannot pay a bank account") {
		t.Fatalf("got %v", err)
	}
}

func startReq(e *rt.Env, wf uuid.UUID) runtime.StartRequest {
	return runtime.StartRequest{TenantID: e.Tenant, WorkflowID: wf, Version: 1, Environment: "prod", Trigger: people, Env: map[string]any{}}
}

// With no event by the deadline, the wait asks the rail: a payout still
// queued there is unconfirmed, and the run carries on.
func TestCapabilityWaitDeadlineLooksUp(t *testing.T) {
	e := rt.New(t)
	newRail(t, e, "testrail", "Test Rail")
	connect(t, e, "rail", "testrail")
	wf := e.Publish(t, strings.Replace(payrollWD, `"timeout":"72h"`, `"timeout":"200ms"`, 1))
	ref := e.Start(t, wf, map[string]any{"people": []any{map[string]any{"id": "e1", "kobo": 500000, "account": "0123456789", "name": "Bola Ade"}}})
	rt.WaitFor(t, 10*time.Second, "the deadline", func() bool {
		e.Drain(t)
		return e.Status(t, ref) == "completed"
	})
	for _, ev := range events(t, e, ref) {
		if ev.Type == history.StepCompleted && strings.HasSuffix(ev.StepID, ".settle") && !strings.Contains(string(ev.Payload), `"unconfirmed"`) {
			t.Errorf("settle: %s", ev.Payload)
		}
	}
}

// A payment retried after the rail's answer was lost is sent again under
// the same key, so the rail pays it once.
func TestCapabilityRetryKeepsTheKey(t *testing.T) {
	e := rt.New(t)
	r := newRail(t, e, "testrail", "Test Rail")
	connect(t, e, "rail", "testrail")
	keys := map[string]bool{}
	lost := 0
	c, _ := e.Registry.Get("testrail@1")
	send := c.Actions["send"]
	c.Actions["send"] = connector.ActionFunc(func(ctx context.Context, req connector.Request) (connector.Response, error) {
		r.mu.Lock()
		keys[req.IdempotencyKey] = true
		first := lost < 2
		lost++
		r.mu.Unlock()
		if first {
			return connector.Response{}, effects.ErrRetryable
		}
		return send.Execute(ctx, req)
	})
	wf := e.Publish(t, payrollWD)
	ref := e.Start(t, wf, people)
	rt.WaitFor(t, 10*time.Second, "the payments", func() bool {
		e.Drain(t)
		r.mu.Lock()
		defer r.mu.Unlock()
		return len(r.paid) == 2
	})
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(keys) != 2 {
		t.Errorf("%d keys for 2 people over %d attempts: %v (%s)", len(keys), lost, keys, types(events(t, e, ref)))
	}
}
