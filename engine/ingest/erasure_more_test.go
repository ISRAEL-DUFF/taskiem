package ingest_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/ai"
	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/history"
	"github.com/israel-duff/taskiem/engine/pii"
	"github.com/israel-duff/taskiem/engine/runtime"
)

// innerSubject is the subject of the envelope inside the customer's at a
// place in a run's RunStarted payload (pii.Under seals free text twice).
func innerSubject(t *testing.T, g *guardWorld, run uuid.UUID, path ...string) string {
	t.Helper()
	var raw []byte
	if err := g.DB.Admin.QueryRow(ctx, `SELECT payload FROM run_events WHERE run_id = $1 AND type = $2`, run, history.RunStarted).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatal(err)
	}
	for _, k := range path {
		m, _ := v.(map[string]any)
		v = m[k]
	}
	env, ok := v.(map[string]any)
	if !ok || !pii.IsEnvelope(env) {
		t.Fatalf("%v is not sealed: %v", path, v)
	}
	var inner any
	if err := db.InTenantTx(ctx, g.DB.App, []uuid.UUID{g.Tenant}, func(tx pgx.Tx) error {
		var err error
		inner, err = g.Vault.OpenTx(ctx, tx, g.Tenant, env)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	m, ok := inner.(map[string]any)
	if !ok || !pii.IsEnvelope(m) {
		t.Fatalf("%v holds no envelope inside the customer's: %v", path, inner)
	}
	s, _ := m["subject"].(string)
	return s
}

func rawHistory(t *testing.T, g *guardWorld, run uuid.UUID) string {
	t.Helper()
	var s string
	if err := g.DB.Admin.QueryRow(ctx, `SELECT string_agg(payload::text, ' ' ORDER BY seq) FROM run_events WHERE run_id = $1`, run).Scan(&s); err != nil {
		t.Fatal(err)
	}
	return s
}

func openedHistory(t *testing.T, g *guardWorld, run uuid.UUID) string {
	t.Helper()
	evs, err := g.Store.OpenedHistory(ctx, runtime.RunRef{TenantID: g.Tenant, ID: run})
	if err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	for _, ev := range evs {
		b.Write(ev.Payload)
		b.WriteByte(' ')
	}
	return b.String()
}

func runsOf(t *testing.T, g *guardWorld) []uuid.UUID {
	t.Helper()
	rows, err := g.DB.Admin.Query(ctx, `SELECT id FROM runs WHERE workflow_id = $1 ORDER BY started_at, id`, g.wf)
	if err != nil {
		t.Fatal(err)
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
	if err != nil {
		t.Fatal(err)
	}
	return ids
}

// waitingFlow answers with an AI step, then waits for the customer's next
// two messages (the first is the one that started it, buffered for it).
const waitingFlow = `[
  {"id":"ask","type":"ai","config":{"prompt":"=\"Answer the customer: \" + trigger.guard.text",
    "output_schema":{"type":"object","required":["reply"],"properties":{"reply":{"type":"string"}}}}},
  {"id":"w1","type":"signal","needs":["ask"],"config":{"event":"whatsapp@1:messages","correlation":"=trigger.guard.sender","timeout":"1h"}},
  {"id":"w2","type":"signal","needs":["w1"],"config":{"event":"whatsapp@1:messages","correlation":"=trigger.guard.sender","timeout":"1h"}},
  {"id":"a","type":"transform","needs":["w2"],"config":{"output":"=steps.ask.output.reply"}}]`

// A customer erased while a run of theirs waits for their next message:
// what they write next still starts a run (it is not refused for the old
// run's erased key), the old run carries on without keeping it, and
// nothing of the old run, its AI step's answer and record, or the
// messages delivered to it as signals, can be read (decision 0062 P2).
func TestCustomerErasedWhileTheirRunWaits(t *testing.T) {
	g := newGuardWorld(t, `true`, "", waitingFlow)
	g.AI = &ai.Fake{Respond: func(ai.Request) (*ai.Response, error) {
		return &ai.Response{Text: `{"reply":"We deliver to 12 Admiralty Way"}`, JSON: json.RawMessage(`{"reply":"We deliver to 12 Admiralty Way"}`),
			Usage: ai.Usage{InputTokens: 10, OutputTokens: 5}}, nil
	}}
	const ada = "2348031234567"
	if s := g.send(t, ada, text("My address is 12 Admiralty Way")); s["outcome"] != "answer" {
		t.Fatalf("first: %v", s)
	}
	// The message, buffered as a signal until the run waits for it, is
	// sealed there.
	var buffered string
	if err := g.DB.Admin.QueryRow(ctx, `SELECT COALESCE(string_agg(payload::text, ' '), '') FROM signals WHERE tenant_id = $1`, g.Tenant).Scan(&buffered); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buffered, `"$pii"`) || strings.Contains(buffered, "Admiralty") || strings.Contains(buffered, ada) || strings.Contains(buffered, `"Ada"`) {
		t.Fatalf("a buffered message in the clear: %s", buffered)
	}
	g.Drain(t)
	runs := runsOf(t, g)
	if len(runs) != 1 {
		t.Fatalf("runs: %v", runs)
	}
	old := runs[0]
	ref := runtime.RunRef{TenantID: g.Tenant, ID: old}
	if st := g.Status(t, ref); st == "completed" || st == "failed" {
		t.Fatalf("the run should wait: %s", st)
	}
	if h := rawHistory(t, g, old); strings.Contains(h, "Admiralty") || strings.Contains(h, ada) {
		t.Fatalf("the run's history in the clear: %s", h)
	}
	if h := openedHistory(t, g, old); !strings.Contains(h, "We deliver to 12 Admiralty Way") || !strings.Contains(h, "My address is 12 Admiralty Way") {
		t.Fatalf("the run cannot read its own history: %s", h)
	}
	record := func() (string, string) {
		var msgs, resp string
		if err := g.DB.Admin.QueryRow(ctx, `SELECT messages::text, COALESCE(response, '') FROM ai_interactions WHERE run_id = $1`, old).Scan(&msgs, &resp); err != nil {
			t.Fatal(err)
		}
		return msgs, resp
	}
	msgs, resp := record()
	if strings.Contains(msgs+resp, "Admiralty") || !strings.Contains(resp, `"$pii"`) || !strings.Contains(msgs, `"$pii"`) {
		t.Fatalf("the AI step's record in the clear: %s / %s", msgs, resp)
	}
	var env map[string]any
	if err := json.Unmarshal([]byte(resp), &env); err != nil {
		t.Fatal(err)
	}
	openRecord := func() (any, error) {
		var out any
		err := db.InTenantTx(ctx, g.DB.App, []uuid.UUID{g.Tenant}, func(tx pgx.Tx) error {
			var err error
			out, err = g.Vault.OpenTx(ctx, tx, g.Tenant, env)
			return err
		})
		return out, err
	}
	if v, err := openRecord(); err != nil || !strings.Contains(fmt.Sprint(v), "Admiralty") {
		t.Fatalf("the record does not open: %v %v", v, err)
	}

	subject, err := g.Vault.SubjectFor(ctx, g.Tenant, "phone", ada)
	if err != nil {
		t.Fatal(err)
	}
	if err := g.Vault.Erase(ctx, g.Tenant, subject, "test"); err != nil {
		t.Fatal(err)
	}

	// They write again: answered (a new run), and the old run, woken by
	// the message, finishes without keeping it.
	if s := g.send(t, ada, text("Hello again from 12 Admiralty Way")); s["outcome"] != "answer" {
		t.Fatalf("after erasure: %v", s)
	}
	g.Drain(t)
	if st := g.Status(t, ref); st != "completed" {
		t.Fatalf("the old run: %s", st)
	}
	if h := openedHistory(t, g, old); strings.Contains(h, "Admiralty") || strings.Contains(h, "Hello again") || strings.Contains(h, ada) {
		t.Fatalf("the old run's history outlived the erasure: %s", h)
	}
	if h := rawHistory(t, g, old); strings.Contains(h, "Hello again") {
		t.Fatalf("the message delivered to the old run is in the clear: %s", h)
	}
	if _, err := openRecord(); !errors.Is(err, pii.ErrErased) {
		t.Fatalf("the AI step's record outlived the erasure: %v", err)
	}
	runs = runsOf(t, g)
	if len(runs) != 2 {
		t.Fatalf("runs after erasure: %v", runs)
	}
	if h := openedHistory(t, g, runs[1]); !strings.Contains(h, "Hello again from 12 Admiralty Way") {
		t.Fatalf("the new run cannot read the new message: %s", h)
	}
	var customer string
	if err := g.DB.Admin.QueryRow(ctx, `SELECT customer_subject FROM runs WHERE id = $1`, runs[1]).Scan(&customer); err != nil || customer != subject+"~2" {
		t.Fatalf("the new run's customer %q (%v), want %s~2", customer, err, subject)
	}
}

// What a customer wrote is also sealed under its own subject inside the
// customer's: erasing that subject (another person named, a value
// erased) erases it while the customer is kept (decision 0062 P2).
func TestCustomerWordsErasedByTheirOwnSubject(t *testing.T) {
	g := newGuardWorld(t, `true`, "")
	const ada = "2348031234567"
	g.send(t, ada, text("Please call Bola Tinubu"))
	g.Drain(t)
	run := runsOf(t, g)[0]
	inner := innerSubject(t, g, run, "trigger", "guard", "text")
	if err := g.Vault.Erase(ctx, g.Tenant, inner, "test"); err != nil {
		t.Fatal(err)
	}
	if h := openedHistory(t, g, run); strings.Contains(h, "Bola Tinubu") {
		t.Fatalf("the words outlived their own subject's erasure: %s", h)
	}
	// The customer is still read: the run's record of who wrote is theirs.
	if h := openedHistory(t, g, run); !strings.Contains(h, ada) {
		t.Fatalf("the customer's number was erased with the words: %s", h)
	}
}

// A workflow with no customer guard subscribed to the same messages: after
// the customer is erased, their next message still starts it (their
// number sealed under the key they were given on coming back, or kept as
// erased), rather than the delivery being refused for ever.
func TestErasedCustomerAndAnUnguardedWorkflow(t *testing.T) {
	g := newGuardWorld(t, `true`, "")
	plain := g.publish(t, `{"schema":"wd/v1","id":"wf_plain","version":1,"name":"plain",
	  "trigger":{"type":"connector_event","config":{"connector":"whatsapp@1","trigger":"messages"}},
	  "steps":[{"id":"a","type":"transform","config":{"output":"=trigger.item.message.text.body"}}]}`)
	const ada = "2348031234567"
	g.send(t, ada, text("first"))
	g.Drain(t)
	subject, err := g.Vault.SubjectFor(ctx, g.Tenant, "phone", ada)
	if err != nil {
		t.Fatal(err)
	}
	if err := g.Vault.Erase(ctx, g.Tenant, subject, "test"); err != nil {
		t.Fatal(err)
	}
	if s := g.send(t, ada, text("second")); s["outcome"] != "answer" {
		t.Fatalf("after erasure: %v", s)
	}
	g.Drain(t)
	var n int
	if err := g.DB.Admin.QueryRow(ctx, `SELECT count(*) FROM runs WHERE workflow_id = $1 AND status = 'completed'`, plain).Scan(&n); err != nil || n != 2 {
		t.Fatalf("the unguarded workflow's runs: %d %v", n, err)
	}
}
