package ingest_test

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	assistantconn "github.com/israel-duff/taskiem/connectors/assistant"
	"github.com/israel-duff/taskiem/connectors/telegram"
	"github.com/israel-duff/taskiem/connectors/whatsapp"
	"github.com/israel-duff/taskiem/engine/assistant"
	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/history"
	"github.com/israel-duff/taskiem/engine/ingest"
	"github.com/israel-duff/taskiem/engine/runtime"
	"github.com/israel-duff/taskiem/engine/wd"
)

type guardWorld struct {
	*world
	wf  uuid.UUID
	seq int
}

func newGuardWorld(t *testing.T, guard string, settings string, steps ...string) *guardWorld {
	t.Helper()
	w := newWorld(t)
	if err := w.Registry.Register(assistantconn.New(&assistant.Service{Pool: w.DB.App, Vault: w.Vault})); err != nil {
		t.Fatal(err)
	}
	if err := w.Registry.Register(whatsapp.New(whatsapp.Options{})); err != nil {
		t.Fatal(err)
	}
	if err := w.Registry.Register(telegram.New(telegram.Options{})); err != nil {
		t.Fatal(err)
	}
	if _, err := w.Vault.CreateConnection(ctx, w.Tenant, "prod", "whatsapp", "main", "api_key",
		map[string]string{"access_token": "EAA1", "phone_number_id": "106540352242922", "app_secret": "wa-secret"}, "test"); err != nil {
		t.Fatal(err)
	}
	if _, err := w.Vault.CreateConnection(ctx, w.Tenant, "prod", "telegram", "bot", "api_key",
		map[string]string{"bot_token": "1:x", "webhook_secret": "tg-secret"}, "test"); err != nil {
		t.Fatal(err)
	}
	if settings == "" {
		settings = "{}"
	}
	g := &guardWorld{world: w}
	body := `[{"id":"a","type":"transform","config":{"output":"=trigger.guard.outcome + '|' + trigger.guard.reason + '|' + string(trigger.guard.first) + '|' + trigger.guard.kind + '|' + string(size(trigger.guard.notice) > 0) + '|' + trigger.guard.reply"}}]`
	if len(steps) > 0 {
		body = steps[0]
	}
	g.wf = w.publish(t, `{"schema":"wd/v1","id":"wf_wa","version":1,"name":"wa","settings":`+settings+`,
	  "trigger":{"type":"connector_event","config":{"connector":"whatsapp@1","trigger":"messages","guard":`+guard+`}},
	  "steps":`+body+`}`)
	return g
}

// send delivers a WhatsApp message from a number; answers the guard's
// summary for the one event.
func (g *guardWorld) send(t *testing.T, from, msg string) map[string]any {
	t.Helper()
	g.seq++
	return g.sendID(t, fmt.Sprintf("wamid.G%d", g.seq), from, msg)
}

// sendID delivers a WhatsApp message with its id: the same id again is the
// provider's retry.
func (g *guardWorld) sendID(t *testing.T, id, from, msg string) map[string]any {
	t.Helper()
	body := []byte(fmt.Sprintf(`{"object":"whatsapp_business_account","entry":[{"id":"1","changes":[{"field":"messages","value":{"messaging_product":"whatsapp",
	  "metadata":{"display_phone_number":"15550783881","phone_number_id":"106540352242922"},
	  "contacts":[{"profile":{"name":"Ada"},"wa_id":%q}],
	  "messages":[{"from":%q,"id":%q,"timestamp":"1762600000",%s}]}}]}]}`, from, from, id, msg))
	st, out := g.post(t, "/connectors/whatsapp@1/messages?env=prod", body, "X-Hub-Signature-256", "sha256="+sign(sha256.New, "wa-secret", body))
	if st != 202 {
		t.Fatalf("delivery: %d %v", st, out)
	}
	evs, _ := out["events"].([]any)
	if len(evs) != 1 {
		t.Fatalf("events: %v", out)
	}
	ev, _ := evs[0].(map[string]any)
	gd, _ := ev["guard"].(map[string]any)
	return gd
}

func text(s string) string { return fmt.Sprintf(`"type":"text","text":{"body":%q}`, s) }

// lastOutput is the step's output of the workflow's latest run.
func (g *guardWorld) lastOutput(t *testing.T) string {
	t.Helper()
	g.Drain(t)
	var run uuid.UUID
	if err := db.InTenantTx(ctx, g.DB.App, []uuid.UUID{g.Tenant}, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT id FROM runs WHERE workflow_id = $1 ORDER BY started_at DESC, id DESC LIMIT 1`, g.wf).Scan(&run)
	}); err != nil {
		t.Fatal(err)
	}
	ref := runtime.RunRef{TenantID: g.Tenant, ID: run}
	if st := g.Status(t, ref); st != "completed" {
		t.Fatalf("run %s", st)
	}
	return stepOutput(t, g.world, ref, "a")
}

func stepOutput(t *testing.T, w *world, ref runtime.RunRef, step string) string {
	t.Helper()
	evs, err := w.Store.OpenedHistory(ctx, ref)
	if err != nil {
		t.Fatal(err)
	}
	for _, ev := range evs {
		if ev.Type == history.StepCompleted && ev.StepID == step {
			var p struct {
				Output any `json:"output"`
			}
			if err := json.Unmarshal(ev.Payload, &p); err != nil {
				t.Fatal(err)
			}
			return fmt.Sprint(p.Output)
		}
	}
	t.Fatalf("step %s did not complete", step)
	return ""
}

// A business's assistant answers only customers' new private messages,
// tells a new customer what it is, honours STOP and START, a limit per
// customer, a request for a person, the kinds of message it reads, the
// owner's pause and Taskiem's switches (decision 0062 V6).
func TestCustomerGuard(t *testing.T) {
	g := newGuardWorld(t, `{"per_minute":3,"per_day":100}`, "")
	const ada, bola = "2348031234567", "2348039876543"

	// A new customer: answered, with the notice first.
	if s := g.send(t, ada, text("Hello, do you deliver?")); s["outcome"] != "answer" {
		t.Fatalf("first message: %v", s)
	}
	if out := g.lastOutput(t); !strings.HasPrefix(out, "answer||true|text|true|") {
		t.Fatalf("first: %s", out)
	}
	// A status report starts nothing.
	status := []byte(`{"object":"whatsapp_business_account","entry":[{"id":"1","changes":[{"field":"messages","value":{"messaging_product":"whatsapp",
	  "metadata":{"display_phone_number":"15550783881","phone_number_id":"106540352242922"},
	  "statuses":[{"id":"wamid.OUT1","status":"delivered","timestamp":"1762600001","recipient_id":"2348031234567"}]}}]}]}`)
	if st, out := g.post(t, "/connectors/whatsapp@1/messages?env=prod", status, "X-Hub-Signature-256", "sha256="+sign(sha256.New, "wa-secret", status)); st != 202 ||
		!strings.Contains(fmt.Sprint(out), "not_customer") {
		t.Fatalf("status: %d %v", st, out)
	}
	// STOP: confirmed once, then nothing until START.
	if s := g.send(t, ada, text("STOP")); s["outcome"] != "reply" {
		t.Fatalf("stop: %v", s)
	}
	if out := g.lastOutput(t); !strings.HasPrefix(out, "reply|stopped|false|text|false|OK. The assistant won't answer you") {
		t.Fatalf("stop: %s", out)
	}
	if s := g.send(t, ada, text("Are you there?")); s["dropped"] != assistant.DropStopped {
		t.Fatalf("after stop: %v", s)
	}
	if s := g.send(t, ada, text("start")); s["outcome"] != "reply" {
		t.Fatalf("start: %v", s)
	}
	if out := g.lastOutput(t); !strings.HasPrefix(out, "reply|started|") {
		t.Fatalf("start: %s", out)
	}

	// The limit: three a minute, told once, then quiet.
	for i := 1; i <= 3; i++ {
		if s := g.send(t, bola, text(fmt.Sprintf("question %d", i))); s["outcome"] != "answer" {
			t.Fatalf("message %d: %v", i, s)
		}
	}
	if s := g.send(t, bola, text("question 4")); s["outcome"] != "reply" {
		t.Fatalf("over the limit: %v", s)
	}
	if out := g.lastOutput(t); !strings.HasPrefix(out, "reply|limited|") {
		t.Fatalf("limited: %s", out)
	}
	if s := g.send(t, bola, text("question 5")); s["dropped"] != assistant.DropLimited {
		t.Fatalf("still over: %v", s)
	}
}

// A customer who asks for a person is handed over: the owners are told,
// and the assistant keeps their messages without answering; kinds of
// message it does not read get a fixed answer or nothing.
func TestCustomerGuardHandoverAndKinds(t *testing.T) {
	g := newGuardWorld(t, `true`, "")
	const ada = "2348031234567"
	if s := g.send(t, ada, `"type":"image","image":{"id":"img-1","mime_type":"image/jpeg"}`); s["outcome"] != "reply" {
		t.Fatalf("image: %v", s)
	}
	if out := g.lastOutput(t); !strings.HasPrefix(out, "reply|kind|true|image|true|Sorry, I can only read") {
		t.Fatalf("image: %s", out)
	}
	if s := g.send(t, ada, `"type":"sticker","sticker":{"id":"st-1"}`); s["dropped"] != assistant.DropKind {
		t.Fatalf("sticker: %v", s)
	}
	if s := g.send(t, ada, text("I want to talk to a person")); s["outcome"] != "reply" {
		t.Fatalf("person: %v", s)
	}
	if out := g.lastOutput(t); !strings.HasPrefix(out, "reply|handover|") {
		t.Fatalf("handover: %s", out)
	}
	var alerts int
	if err := g.DB.Admin.QueryRow(ctx, `SELECT count(*) FROM alerts WHERE tenant_id = $1 AND kind = $2 AND body LIKE '%ending 4567%'`, g.Tenant, assistant.AlertHandover).Scan(&alerts); err != nil || alerts != 1 {
		t.Fatalf("%d hand-over alerts (%v)", alerts, err)
	}
	if s := g.send(t, ada, text("Hello?")); s["outcome"] != "quiet" {
		t.Fatalf("while a person answers: %v", s)
	}
	if out := g.lastOutput(t); !strings.HasPrefix(out, "quiet|waiting|") {
		t.Fatalf("waiting: %s", out)
	}

	// The owner's pause: kept, not answered.
	if _, err := g.DB.Admin.Exec(ctx, `INSERT INTO tenant_channel_settings (tenant_id, assistant_paused_at, updated_by) VALUES ($1, now(), 'test')
		ON CONFLICT (tenant_id) DO UPDATE SET assistant_paused_at = now()`, g.Tenant); err != nil {
		t.Fatal(err)
	}
	if s := g.send(t, "2348030000001", text("Hi")); s["outcome"] != "quiet" {
		t.Fatalf("paused: %v", s)
	}
	if _, err := g.DB.Admin.Exec(ctx, `UPDATE tenant_channel_settings SET assistant_paused_at = NULL WHERE tenant_id = $1`, g.Tenant); err != nil {
		t.Fatal(err)
	}
	// Taskiem's stop for this business, then beta without the flag.
	if _, err := g.DB.Admin.Exec(ctx, `INSERT INTO assistant_controls (tenant_id, stopped_at, stopped_reason, updated_by) VALUES ($1, now(), 'abuse', 'op')`, g.Tenant); err != nil {
		t.Fatal(err)
	}
	if s := g.send(t, "2348030000002", text("Hi")); s["dropped"] != assistant.DropStoppedByUs {
		t.Fatalf("stopped by Taskiem: %v", s)
	}
	if _, err := g.DB.Admin.Exec(ctx, `UPDATE assistant_controls SET stopped_at = NULL WHERE tenant_id = $1`, g.Tenant); err != nil {
		t.Fatal(err)
	}
	g.h.Guard = &assistant.Guard{Mode: assistant.ModeBeta}
	if s := g.send(t, "2348030000003", text("Hi")); s["dropped"] != assistant.DropOff {
		t.Fatalf("beta, not flagged: %v", s)
	}
	if _, err := g.DB.Admin.Exec(ctx, `UPDATE assistant_controls SET beta = true WHERE tenant_id = $1`, g.Tenant); err != nil {
		t.Fatal(err)
	}
	if s := g.send(t, "2348030000003", text("Hi")); s["outcome"] != "answer" {
		t.Fatalf("beta, flagged: %v", s)
	}
}

// A Telegram bot added to a group answers no one there; a private chat is
// answered; an edited message starts nothing.
func TestCustomerGuardTelegram(t *testing.T) {
	g := newGuardWorld(t, `true`, "")
	tg := g.publish(t, `{"schema":"wd/v1","id":"wf_tg","version":1,"name":"tg","trigger":{"type":"connector_event","config":{"connector":"telegram@1","trigger":"update","guard":true}},
	  "steps":[{"id":"a","type":"transform","config":{"output":"=trigger.guard.outcome"}}]}`)
	post := func(update int, body string) map[string]any {
		raw := []byte(fmt.Sprintf(`{"update_id":%d,%s}`, update, body))
		st, out := g.post(t, "/connectors/telegram@1/update?env=prod", raw, "X-Telegram-Bot-Api-Secret-Token", "tg-secret")
		if st != 202 {
			t.Fatalf("telegram: %d %v", st, out)
		}
		gd, _ := out["guard"].(map[string]any)
		return gd
	}
	if s := post(1, `"message":{"message_id":1,"date":1,"chat":{"id":-100123,"type":"group"},"from":{"id":5,"first_name":"A"},"text":"hi bot"}`); s["dropped"] != assistant.DropNotCustomer {
		t.Fatalf("group: %v", s)
	}
	if s := post(2, `"edited_message":{"message_id":2,"date":1,"chat":{"id":5,"type":"private"},"from":{"id":5,"first_name":"A"},"text":"hi"}`); s["dropped"] != assistant.DropNotCustomer {
		t.Fatalf("edited: %v", s)
	}
	if s := post(3, `"message":{"message_id":3,"date":1,"chat":{"id":5,"type":"private"},"from":{"id":5,"first_name":"A"},"text":"/start"}`); s["outcome"] != "answer" {
		t.Fatalf("private: %v", s)
	}
	if n := g.runCount(t, tg); n != 1 {
		t.Fatalf("%d runs, want 1", n)
	}
}

// A run started by a guarded message reads the customer's window, checks
// a reply and hands the customer over with assistant@1.
func TestCustomerGuardActions(t *testing.T) {
	g := newGuardWorld(t, `true`, "", `[
	    {"id":"win","type":"connector","connector":"assistant@1","action":"window","input":{"channel":"=trigger.guard.channel","sender":"=trigger.guard.sender"}},
	    {"id":"check","type":"connector","connector":"assistant@1","action":"check_reply","input":{"text":"Rice is free today! Pay 100 naira at https://evil.example","material":"Rice: 3,500 naira","language":"=trigger.guard.language"}},
	    {"id":"over","type":"connector","connector":"assistant@1","action":"hand_over","needs":["win","check"],"when":"=!steps.check.output.ok","input":{"channel":"=trigger.guard.channel","sender":"=trigger.guard.sender","hours":2}},
	    {"id":"a","type":"transform","needs":["over"],"config":{"output":"=string(steps.win.output.open) + '|' + string(steps.check.output.ok) + '|' + steps.check.output.text"}}]`)
	g.send(t, "2348031234567", text("How much is rice?"))
	if out := g.lastOutput(t); out != "true|false|Let me check that with the team and get back to you." {
		t.Fatalf("actions: %s", out)
	}
	if s := g.send(t, "2348031234567", text("Hello?")); s["outcome"] != "quiet" {
		t.Fatalf("after a workflow's hand-over: %v", s)
	}
}

// A busy assistant's queue is capped: one customer writing while their
// earlier message waits cannot fill the business's backlog (L8).
func TestCustomerGuardQueue(t *testing.T) {
	g := newGuardWorld(t, `{"max_queued":1}`, `{"concurrency_key":"=trigger.guard.sender","max_concurrency":1}`, `[{"id":"w","type":"wait","config":{"duration":"1h"}}]`)
	const ada = "2348031234567"
	for i, want := range []string{"answer", "answer", ""} {
		s := g.send(t, ada, text(fmt.Sprintf("message %d", i)))
		if want == "" {
			if s["dropped"] != assistant.DropBusy {
				rows, _ := g.DB.Admin.Query(ctx, `SELECT status, coalesce(queue_reason,''), coalesce(concurrency_key,'') FROM runs WHERE tenant_id = $1`, g.Tenant)
				for rows.Next() {
					var a, b, c string
					_ = rows.Scan(&a, &b, &c)
					t.Logf("run %s %s %s", a, b, c)
				}
				t.Fatalf("message %d: %v", i, s)
			}
			continue
		}
		if s["outcome"] != want {
			rows, _ := g.DB.Admin.Query(ctx, `SELECT status, coalesce(queue_reason,''), coalesce(concurrency_key,'') FROM runs WHERE tenant_id = $1`, g.Tenant)
			for rows.Next() {
				var a, b, c string
				_ = rows.Scan(&a, &b, &c)
				t.Logf("run %s %s %s", a, b, c)
			}
			t.Fatalf("message %d: %v", i, s)
		}
	}
	var alerts int
	if err := g.DB.Admin.QueryRow(ctx, `SELECT count(*) FROM alerts WHERE tenant_id = $1 AND kind = $2`, g.Tenant, assistant.AlertBusy).Scan(&alerts); err != nil || alerts != 1 {
		t.Fatalf("%d busy alerts (%v)", alerts, err)
	}
}

// A provider's retry of a delivery replays the guard's decision: the
// message is counted once, and a second guarded workflow on the same
// channel is refused when it is published (found by the review of V6).
func TestCustomerGuardRetries(t *testing.T) {
	g := newGuardWorld(t, `{"per_minute":2}`, "")
	const ada = "2348031234567"
	for i := 0; i < 3; i++ {
		if s := g.sendID(t, "wamid.SAME", ada, text("hello")); s["outcome"] != "answer" {
			t.Fatalf("delivery %d: %v", i, s)
		}
	}
	if s := g.send(t, ada, text("second")); s["outcome"] != "answer" {
		t.Fatalf("the retries were counted: %v", s)
	}
	if n := g.runCount(t, g.wf); n != 2 {
		t.Fatalf("%d runs, want 2", n)
	}
	def := `{"schema":"wd/v1","id":"wf_two","version":1,"name":"two","trigger":{"type":"connector_event","config":{"connector":"whatsapp@1","trigger":"messages","guard":true}},
	  "steps":[{"id":"a","type":"transform","config":{"output":"=1"}}]}`
	wf := g.Publish(t, def)
	d, err := wd.Load([]byte(def))
	if err != nil {
		t.Fatal(err)
	}
	err = db.InTenantTx(ctx, g.DB.App, []uuid.UUID{g.Tenant}, func(tx pgx.Tx) error {
		return ingest.Sync(ctx, tx, g.Tenant, wf, 1, d, g.Registry, time.Now())
	})
	if err == nil || !strings.Contains(err.Error(), "already answers customers") {
		t.Fatalf("a second assistant on the channel: %v", err)
	}
}

// The notice goes with the first answer, even when the first message was
// dropped; a STOP written while assistants are off is kept.
func TestCustomerGuardNoticeAndStopWhileOff(t *testing.T) {
	g := newGuardWorld(t, `true`, "")
	const ada = "2348031234567"
	if s := g.send(t, ada, `"type":"sticker","sticker":{"id":"st-1"}`); s["dropped"] != assistant.DropKind {
		t.Fatalf("sticker: %v", s)
	}
	g.send(t, ada, text("Hello"))
	if out := g.lastOutput(t); !strings.HasPrefix(out, "answer||false|text|true|") {
		t.Fatalf("the notice was used up by a dropped message: %s", out)
	}
	g.send(t, ada, text("And another"))
	if out := g.lastOutput(t); !strings.HasPrefix(out, "answer||false|text|false|") {
		t.Fatalf("the notice went twice: %s", out)
	}
	g.h.Guard = &assistant.Guard{Mode: assistant.ModeOff}
	if s := g.send(t, ada, text("STOP")); s["dropped"] != assistant.DropOff {
		t.Fatalf("stop while off: %v", s)
	}
	g.h.Guard = &assistant.Guard{Mode: assistant.ModeOn}
	if s := g.send(t, ada, text("Hello?")); s["dropped"] != assistant.DropStopped {
		t.Fatalf("the STOP written while off was lost: %v", s)
	}
}

// Several quick messages get one answer (decision 0062 V10, L5): the run of
// an earlier message finds a newer one waiting and stays quiet; the newest
// answers, with what was written before it since the last answer.
func TestCustomerGuardGather(t *testing.T) {
	g := newGuardWorld(t, "true", "", `[
	  {"id":"g","type":"connector","connector":"assistant@1","action":"gather","input":{}},
	  {"id":"a","type":"transform","needs":["g"],"config":{"output":"=string(steps.g.output.latest) + '|' + steps.g.output.earlier_text"}}]`)
	const ada = "2348031234567"
	first := g.send(t, ada, text("Hello"))
	second := g.send(t, ada, text("Do you deliver to Ajah?"))
	// The notice for a new customer goes with the message that answers.
	if first["notice"] == "" || second["notice"] == "" {
		t.Fatalf("the notice: %v / %v", first["notice"], second["notice"])
	}
	g.Drain(t)
	outputs := func() []string {
		t.Helper()
		var runs []uuid.UUID
		if err := db.InTenantTx(ctx, g.DB.App, []uuid.UUID{g.Tenant}, func(tx pgx.Tx) error {
			rows, err := tx.Query(ctx, `SELECT id FROM runs WHERE workflow_id = $1 ORDER BY started_at, id`, g.wf)
			if err != nil {
				return err
			}
			runs, err = pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
			return err
		}); err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, id := range runs {
			out = append(out, stepOutput(t, g.world, runtime.RunRef{TenantID: g.Tenant, ID: id}, "a"))
		}
		return out
	}
	got := outputs()
	if len(got) != 2 || !((got[0] == "false|" && got[1] == "true|Hello") || (got[1] == "false|" && got[0] == "true|Hello")) {
		t.Fatalf("gathered: %v", got)
	}
	// What was answered is taken: the next message gathers nothing before it.
	g.send(t, ada, text("Thank you"))
	g.Drain(t)
	if got = outputs(); got[2] != "true|" {
		t.Fatalf("after an answer: %v", got)
	}
	// A STOP right after a message: the earlier run says nothing more.
	g.send(t, ada, text("Actually one more thing"))
	g.send(t, ada, text("STOP"))
	g.Drain(t)
	if got = outputs(); got[3] != "false|" {
		t.Fatalf("after STOP: %v", got)
	}
}
