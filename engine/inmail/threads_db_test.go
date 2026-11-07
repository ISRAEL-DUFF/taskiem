package inmail_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	emailconn "github.com/israel-duff/taskiem/connectors/email"
	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/effects"
	"github.com/israel-duff/taskiem/engine/history"
	"github.com/israel-duff/taskiem/engine/inmail"
	"github.com/israel-duff/taskiem/engine/runtime"
	"github.com/israel-duff/taskiem/engine/sender"
)

// A ticket: the first email starts it, an acknowledgement goes out in
// the thread, and the run waits for two replies.
const tickets = `{"schema":"wd/v1","id":"wf_tickets","version":1,"name":"Tickets",
 "trigger":{"type":"email","config":{"threads":true}},
 "steps":[
  {"id":"ack","type":"connector","connector":"email@1","action":"send_in_thread","input":{
    "to":"=trigger.body.from.address","subject":"=trigger.body.subject","message":"We have your message.",
    "thread":"=has(trigger.email.thread) ? trigger.email.thread : (trigger.body.from.address.startsWith('ada') ? 'T-1042' : 'T-9')","in_reply_to":"=trigger.body.message_id"}},
  {"id":"answer","type":"signal","needs":["ack"],"config":{"event":"email.reply","correlation":"=steps.ack.output.thread","timeout":"7d"}},
  {"id":"again","type":"signal","needs":["answer"],"config":{"event":"email.reply","correlation":"=steps.ack.output.thread","timeout":"7d"}},
  {"id":"done","type":"transform","needs":["again"],"config":{"output":{
    "first":"=steps.answer.output.body.latest","by":"=steps.answer.output.email.matched_by",
    "second":"=steps.again.output.body.latest","by2":"=steps.again.output.email.matched_by"}}}]}`

type outbox struct {
	mu   sync.Mutex
	msgs [][]byte
}

func (o *outbox) Send(_ context.Context, _ string, _ []string, msg []byte) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.msgs = append(o.msgs, msg)
	return nil
}

type threadWorld struct {
	*world
	mail   *outbox
	worker *sender.Worker
}

func newThreadWorld(t *testing.T) *threadWorld {
	w := newWorld(t)
	w.svc.PII = w.env.Vault
	w.svc.Wake = w.env.Store.WakeWorkflow
	w.svc.Messages = &sender.Queue{Vault: w.env.Vault, HashKey: bytes.Repeat([]byte{3}, 32), PerHour: 50, PerDay: 100}
	if err := w.env.Registry.Register(emailconn.New(w.svc)); err != nil {
		t.Fatal(err)
	}
	mail := &outbox{}
	return &threadWorld{world: w, mail: mail,
		worker: &sender.Worker{Pool: w.env.DB.App, Vault: w.env.Vault, Mailer: mail, From: "no-reply@taskiem.test"}}
}

// receiveTo leaves a message for <public>+<tag>@ in the inbox.
func (w *threadWorld) receiveTo(public, tag string, raw []byte, auth inmail.Auth, mailFrom string) {
	w.t.Helper()
	ctx := context.Background()
	st := inmail.DBStore{Pool: w.env.DB.PublicPool(w.t, 1)}
	ep, ok, err := st.Recipient(ctx, public)
	if err != nil || !ok {
		w.t.Fatalf("recipient %s: %v %v", public, ok, err)
	}
	id := uuid.Must(uuid.NewV7())
	meta := inmail.Meta{Reference: id.String(), Address: public + "@in.taskiem.test", Tag: tag, MailFrom: mailFrom, RemoteIP: "192.0.2.1", ReceivedAt: time.Now(), Auth: auth}
	sm, err := w.keys.Seal(inmail.MetaAAD(id), meta)
	if err != nil {
		w.t.Fatal(err)
	}
	sr, err := w.keys.SealBytes(inmail.RawAAD(id), raw)
	if err != nil {
		w.t.Fatal(err)
	}
	h := sha256.Sum256(append(ep[:], raw...))
	if ok, err := st.Receive(ctx, id, ep, h[:], sm, sr); err != nil || !ok {
		w.t.Fatalf("receive: %v %v", ok, err)
	}
}

// sent sends what the outbox holds and returns the emails sent.
func (w *threadWorld) sent() []string {
	w.t.Helper()
	before := len(w.mail.msgs)
	if err := w.worker.Tick(context.Background()); err != nil {
		w.t.Fatal(err)
	}
	var out []string
	for _, m := range w.mail.msgs[before:] {
		out = append(out, string(m))
	}
	return out
}

func header(raw, name string) string {
	head, _, _ := strings.Cut(raw, "\r\n\r\n")
	head = strings.ReplaceAll(head, "\r\n ", " ") // unfolded
	for _, l := range strings.Split(head, "\r\n") {
		if k, v, ok := strings.Cut(l, ": "); ok && strings.EqualFold(k, name) {
			return v
		}
	}
	return ""
}

func customerMail(id, from, subject, threading, text string) []byte {
	return []byte(strings.ReplaceAll(`From: Ada <`+from+`>
To: help@ada-kitchen.example
Subject: `+subject+`
Message-ID: <`+id+`>
`+threading+`Content-Type: text/plain; charset=utf-8

`+text+`

On Friday, Ada's Kitchen wrote:
> We have your message.
`, "\n", "\r\n"))
}

func (w *threadWorld) runs(wf uuid.UUID) []uuid.UUID {
	w.t.Helper()
	rows, err := w.env.DB.Admin.Query(context.Background(), `SELECT id FROM runs WHERE workflow_id = $1 ORDER BY id`, wf)
	if err != nil {
		w.t.Fatal(err)
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
	if err != nil {
		w.t.Fatal(err)
	}
	return ids
}

// event is the opened payload of a run's event of a type (and step).
func (w *threadWorld) event(run uuid.UUID, typ, step string) map[string]any {
	w.t.Helper()
	h, err := w.env.Store.OpenedHistory(context.Background(), runtime.RunRef{ID: run, TenantID: w.env.Tenant})
	if err != nil {
		w.t.Fatal(err)
	}
	for _, e := range h {
		if e.Type == typ && (step == "" || e.StepID == step) {
			var v map[string]any
			_ = json.Unmarshal(e.Payload, &v)
			return v
		}
	}
	return nil
}

func (w *threadWorld) messageStatus(t *testing.T, wf uuid.UUID) []string {
	rows, err := w.env.DB.Admin.Query(context.Background(), `SELECT status || COALESCE(':' || run_id::text, '') FROM email_messages WHERE workflow_id = $1 ORDER BY received_at, id`, wf)
	if err != nil {
		t.Fatal(err)
	}
	out, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestEmailThreads(t *testing.T) {
	w := newThreadWorld(t)
	ctx := context.Background()
	wf := w.deploy(tickets)
	var public string
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT public_id FROM email_endpoints WHERE workflow_id = $1`, wf).Scan(&public); err != nil {
		t.Fatal(err)
	}
	proven := inmail.Auth{SPF: "pass", DKIM: []string{"customer.example"}, Aligned: true}
	const ada = "ada@customer.example"

	// 1. The first email opens the ticket; the run acknowledges it in the
	// thread and waits.
	w.receiveTo(public, "", customerMail("c0@customer.example", ada, "Help with my order", "", "Where is order 12?"), proven, ada)
	w.drain()
	runs := w.runs(wf)
	if len(runs) != 1 {
		t.Fatalf("runs: %d", len(runs))
	}
	run1 := runs[0]
	ack := w.event(run1, history.StepCompleted, "ack")
	out, _ := ack["output"].(map[string]any)
	msgID, _ := out["message_id"].(string)
	replyTo, _ := out["reply_to"].(string)
	if !strings.HasPrefix(msgID, "tk-") || !strings.HasPrefix(replyTo, public+"+t-1042-") || out["thread"] != "T-1042" {
		t.Fatalf("ack output: %v", out)
	}
	emails := w.sent()
	if len(emails) != 1 {
		t.Fatalf("sent %d", len(emails))
	}
	e := emails[0]
	if from := header(e, "From"); !strings.HasSuffix(from, ` via Taskiem" <no-reply@taskiem.test>`) {
		t.Errorf("From: %q", from)
	}
	for name, want := range map[string]string{
		"To":          ada,
		"Reply-To":    replyTo,
		"Message-ID":  "<" + msgID + ">",
		"In-Reply-To": "<c0@customer.example>",
		"References":  "<c0@customer.example>",
		"Subject":     "[#T-1042] Help with my order",
	} {
		if got := header(e, name); got != want {
			t.Errorf("%s: %q, want %q", name, got, want)
		}
	}
	tag := strings.TrimSuffix(strings.TrimPrefix(replyTo, public+"+"), "@in.taskiem.test")

	// 2. A reply to the tagged address wakes the run with the reply's
	// newest text; nothing new starts.
	w.receiveTo(public, tag, customerMail("r1@customer.example", ada, "Re: [#T-1042] Help with my order",
		"In-Reply-To: <"+msgID+">\nReferences: <c0@customer.example> <"+msgID+">\n", "It was order 12, for Saturday."), proven, ada)
	w.drain()
	if n := len(w.runs(wf)); n != 1 {
		t.Fatalf("a reply started a run: %d runs", n)
	}
	sig := w.event(run1, history.StepCompleted, "answer")
	o, _ := sig["output"].(map[string]any)
	body, _ := o["body"].(map[string]any)
	em, _ := o["email"].(map[string]any)
	if body["latest"] != "It was order 12, for Saturday." || em["matched_by"] != "tag" || em["thread"] != "T-1042" || body["in_reply_to"] != msgID {
		t.Fatalf("the reply the run got: %v", o)
	}
	var stored []byte
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT payload FROM run_events WHERE run_id = $1 AND type = 'SignalReceived' ORDER BY seq LIMIT 1`, run1).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(stored, []byte("order 12")) || bytes.Contains(stored, []byte(ada)) {
		t.Errorf("the reply is in history unsealed: %s", stored)
	}

	// 3. A reply to the bare address, found only by In-Reply-To, wakes
	// the second wait, and the run ends.
	w.receiveTo(public, "", customerMail("r2@customer.example", ada, "Re: Help", "In-Reply-To: <"+msgID+">\n", "Thanks, all good."), proven, ada)
	w.drain()
	if st := w.env.Status(t, runtime.RunRef{ID: run1, TenantID: w.env.Tenant}); st != "completed" {
		t.Fatalf("run 1: %s", st)
	}
	done := w.event(run1, history.StepCompleted, "done")["output"].(map[string]any)
	if done["first"] != "It was order 12, for Saturday." || done["by"] != "tag" || done["second"] != "Thanks, all good." || done["by2"] != "headers" {
		t.Fatalf("done: %v", done)
	}
	if got := w.messageStatus(t, wf); len(got) != 3 || got[0] != "started:"+run1.String() || got[1] != "replied:"+run1.String() || got[2] != "replied:"+run1.String() {
		t.Fatalf("messages: %q", got)
	}

	// 4. A guessed tag (the next ticket, this one's token) names nothing:
	// a new run, with no thread.
	w.receiveTo(public, "t-1043"+tag[len("t-1042"):], customerMail("x1@stranger.example", "eve@stranger.example", "Re: [#T-1043]", "", "Let me in."),
		inmail.Auth{SPF: "pass", DKIM: []string{"stranger.example"}, Aligned: true}, "eve@stranger.example")
	w.drain()
	runs = w.runs(wf)
	if len(runs) != 2 {
		t.Fatalf("runs: %d", len(runs))
	}
	trig := w.event(runs[1], history.RunStarted, "")["trigger"].(map[string]any)["email"].(map[string]any)
	if _, has := trig["thread"]; has || trig["tag"] != "t-1043"+tag[len("t-1042"):] {
		t.Fatalf("a forged tag found a thread: %v", trig)
	}

	// 5. A reply after the ticket's run ended starts a new run carrying
	// the thread, which acknowledges in the same thread and waits.
	w.receiveTo(public, tag, customerMail("r3@customer.example", ada, "Re: [#T-1042] Help", "", "One more thing."), proven, ada)
	w.drain()
	runs = w.runs(wf)
	if len(runs) != 3 {
		t.Fatalf("runs: %d", len(runs))
	}
	run3 := runs[2]
	trig = w.event(run3, history.RunStarted, "")["trigger"].(map[string]any)["email"].(map[string]any)
	if trig["thread"] != "T-1042" || trig["matched_by"] != "tag" {
		t.Fatalf("the reopening run: %v", trig)
	}
	emails = w.sent()
	if len(emails) != 2 {
		t.Fatalf("sent %d", len(emails))
	}
	reopened := emails[0] // run 3's acknowledgement; the other is run 2's, to the stranger
	if header(reopened, "To") != ada {
		reopened = emails[1]
	}
	if header(reopened, "Reply-To") != replyTo || header(reopened, "In-Reply-To") != "<r3@customer.example>" ||
		!strings.Contains(header(reopened, "References"), "<"+msgID+">") || header(reopened, "Subject") != "Re: [#T-1042] Help" {
		t.Fatalf("the reopened thread's email: %s", reopened)
	}

	// 6. The subject tag counts only from a proven sender Taskiem emailed
	// in that thread: unproven, it starts a run; proven, it wakes run 3.
	w.receiveTo(public, "", customerMail("s1@customer.example", ada, "[#T-1042] Another question", "", "Forged?"), inmail.Auth{}, ada)
	w.drain()
	if n := len(w.runs(wf)); n != 4 {
		t.Fatalf("an unproven subject tag: %d runs", n)
	}
	w.receiveTo(public, "", customerMail("s2@customer.example", ada, "[#T-1042] Another question", "", "By subject."), proven, ada)
	w.drain()
	if n := len(w.runs(wf)); n != 4 {
		t.Fatalf("a proven subject tag started a run: %d runs", n)
	}
	o = w.event(run3, history.StepCompleted, "answer")["output"].(map[string]any)
	if o["body"].(map[string]any)["latest"] != "By subject." || o["email"].(map[string]any)["matched_by"] != "subject" {
		t.Fatalf("by subject: %v", o)
	}

	// 7. Erasing the customer clears their pseudonym: the subject tag no
	// longer finds them.
	var subject string
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT recipient_subject FROM email_thread_messages WHERE run_id = $1`, run1).Scan(&subject); err != nil || subject == "" {
		t.Fatalf("recipient subject: %q %v", subject, err)
	}
	if err := w.env.Vault.Erase(ctx, w.env.Tenant, subject, "test"); err != nil {
		t.Fatal(err)
	}
	var left int
	_ = w.env.DB.Admin.QueryRow(ctx, `SELECT count(*) FROM email_thread_messages WHERE recipient_subject = $1`, subject).Scan(&left)
	if left != 0 {
		t.Errorf("the erased recipient is still found: %d rows", left)
	}
	w.receiveTo(public, "", customerMail("s3@customer.example", ada, "[#T-1042] After erasure", "", "Hello."), proven, ada)
	w.drain()
	if n := len(w.runs(wf)); n != 5 {
		t.Fatalf("the subject tag still matched an erased recipient: %d runs", n)
	}

	// 8. The thread's records go with the run that sent them, when it is
	// purged at the end of its retention.
	if _, err := w.env.DB.Admin.Exec(ctx, `UPDATE runs SET retain_until = now() - interval '1 second' WHERE id = $1`, run1); err != nil {
		t.Fatal(err)
	}
	if err := db.InTenantTx(ctx, w.env.DB.App, []uuid.UUID{w.env.Tenant}, func(tx pgx.Tx) error {
		var ok bool
		if err := tx.QueryRow(ctx, `SELECT taskiem_purge_run($1)`, run1).Scan(&ok); err != nil || !ok {
			return errors.Join(err, errors.New("not purged"))
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	_ = w.env.DB.Admin.QueryRow(ctx, `SELECT count(*) FROM email_thread_messages WHERE run_id = $1`, run1).Scan(&left)
	if left != 0 {
		t.Errorf("a purged run's thread records are kept: %d", left)
	}
}

// A reply that comes while the ticket's run is going but not waiting
// waits in the inbox until the run waits for it.
func TestEmailReplyHeldUntilTheRunWaits(t *testing.T) {
	w := newThreadWorld(t)
	ctx := context.Background()
	doc := strings.Replace(tickets, `{"id":"answer","type":"signal","needs":["ack"]`,
		`{"id":"pause","type":"wait","needs":["ack"],"config":{"duration":"1h"}},{"id":"answer","type":"signal","needs":["pause"]`, 1)
	doc = strings.Replace(doc, `{"id":"again","type":"signal","needs":["answer"]`,
		`{"id":"pause2","type":"wait","needs":["answer"],"config":{"duration":"1h"}},{"id":"again","type":"signal","needs":["pause2"]`, 1)
	wf := w.deploy(doc)
	var public string
	_ = w.env.DB.Admin.QueryRow(ctx, `SELECT public_id FROM email_endpoints WHERE workflow_id = $1`, wf).Scan(&public)
	proven := inmail.Auth{SPF: "pass", DKIM: []string{"customer.example"}, Aligned: true}
	const ada = "ada@customer.example"
	w.receiveTo(public, "", customerMail("c0@customer.example", ada, "Help", "", "Hi."), proven, ada)
	w.drain()
	run := w.runs(wf)[0]
	reply := w.event(run, history.StepCompleted, "ack")["output"].(map[string]any)["reply_to"].(string)
	tag := strings.TrimSuffix(strings.TrimPrefix(reply, public+"+"), "@in.taskiem.test")

	w.receiveTo(public, tag, customerMail("r1@customer.example", ada, "Re: [#T-1042] Help", "", "Early reply."), proven, ada)
	w.drain()
	var inbox int
	_ = w.env.DB.Admin.QueryRow(ctx, `SELECT count(*) FROM email_inbox`).Scan(&inbox)
	if inbox != 1 || len(w.runs(wf)) != 1 || len(w.messageStatus(t, wf)) != 1 {
		t.Fatalf("not held: inbox %d, runs %d, messages %q", inbox, len(w.runs(wf)), w.messageStatus(t, wf))
	}
	// The wait ends; the run waits for the reply; the held message is
	// looked at again and delivered.
	if _, err := w.env.DB.Admin.Exec(ctx, `UPDATE timers SET fire_at = now() WHERE run_id = $1`, run); err != nil {
		t.Fatal(err)
	}
	w.env.Drain(t)
	if _, err := w.env.DB.Admin.Exec(ctx, `UPDATE email_inbox SET claimed_until = NULL`); err != nil {
		t.Fatal(err)
	}
	w.drain()
	o := w.event(run, history.StepCompleted, "answer")
	if o == nil || o["output"].(map[string]any)["body"].(map[string]any)["latest"] != "Early reply." {
		t.Fatalf("the held reply was not delivered: %v", o)
	}
	_ = w.env.DB.Admin.QueryRow(ctx, `SELECT count(*) FROM email_inbox`).Scan(&inbox)
	if inbox != 0 || len(w.runs(wf)) != 1 {
		t.Fatalf("inbox %d, runs %d", inbox, len(w.runs(wf)))
	}

	// The run waits on its second pause. Held past HoldFor, a reply
	// starts a run of its own.
	w.receiveTo(public, tag, customerMail("r2@customer.example", ada, "Re: [#T-1042] Help", "", "Late reply."), proven, ada)
	w.svc.Now = func() time.Time { return time.Now().Add(inmail.HoldFor + time.Hour) }
	w.drain()
	if n := len(w.runs(wf)); n != 2 {
		t.Fatalf("a reply held too long: %d runs", n)
	}
}

func TestSendInThreadIdempotentAndChecked(t *testing.T) {
	w := newThreadWorld(t)
	ctx := context.Background()
	wf := w.deploy(tickets)
	var public string
	_ = w.env.DB.Admin.QueryRow(ctx, `SELECT public_id FROM email_endpoints WHERE workflow_id = $1`, wf).Scan(&public)
	const ada = "ada@customer.example"
	w.receiveTo(public, "", customerMail("c0@customer.example", ada, "Help", "", "Hi."), inmail.Auth{Aligned: true}, ada)
	w.drain()
	run := w.runs(wf)[0]
	send := inmail.ThreadEmail{Tenant: w.env.Tenant, Run: run, Environment: "prod", To: ada, Subject: "Update", Message: "Done.", Thread: "t-1042", Key: "tsk_retried"}
	a, err := w.svc.SendInThread(ctx, send)
	if err != nil {
		t.Fatal(err)
	}
	b, err := w.svc.SendInThread(ctx, send)
	if err != nil || a["message_id"] != b["message_id"] || a["subject"] != "[#T-1042] Update" {
		t.Fatalf("a retry: %v %v %v", a, b, err)
	}
	var queued, rows int
	_ = w.env.DB.Admin.QueryRow(ctx, `SELECT count(*) FROM platform_messages WHERE dedup_key = 'thread:tsk_retried'`).Scan(&queued)
	_ = w.env.DB.Admin.QueryRow(ctx, `SELECT count(*) FROM email_thread_messages WHERE message_id = $1`, a["message_id"]).Scan(&rows)
	if queued != 1 || rows != 1 {
		t.Fatalf("a retry queued %d emails and %d records", queued, rows)
	}
	// It answers the thread's last email when told nothing else.
	if a["in_reply_to"] == "" || !strings.HasPrefix(a["in_reply_to"].(string), "tk-") {
		t.Errorf("in_reply_to %v", a["in_reply_to"])
	}

	// Refused, for good: a key that is not one, a bad Message-ID, and a
	// workflow without threads.
	for name, mut := range map[string]func(*inmail.ThreadEmail){
		"thread":      func(e *inmail.ThreadEmail) { e.Thread = "T 1042" },
		"in_reply_to": func(e *inmail.ThreadEmail) { e.InReplyTo = "<x>\r\nBcc: e@x" },
		"to":          func(e *inmail.ThreadEmail) { e.To = "not an address" },
	} {
		e := send
		e.Key = "tsk_" + name
		mut(&e)
		if _, err := w.svc.SendInThread(ctx, e); !errors.Is(err, effects.ErrFatal) {
			t.Errorf("%s: %v", name, err)
		}
	}
	plain := w.deploy(alerts)
	other := w.env.Start(t, plain, map[string]any{"type": "manual"})
	e := send
	e.Run, e.Key = other.ID, "tsk_plain"
	if _, err := w.svc.SendInThread(ctx, e); !errors.Is(err, effects.ErrFatal) || !strings.Contains(err.Error(), "threads") {
		t.Errorf("without threads: %v", err)
	}
}

// Without threads nothing changes: a tagged message, even one in reply
// to another, starts a run as any message does; the run sees the tag and
// the threading headers, and no thread.
func TestNoThreadsStartsRunsAsBefore(t *testing.T) {
	w := newThreadWorld(t)
	ctx := context.Background()
	wf := w.deploy(`{"schema":"wd/v1","id":"wf_orders","version":1,"name":"Orders","trigger":{"type":"email"},
	 "steps":[{"id":"read","type":"transform","config":{"output":{"tag":"=trigger.email.tag","irt":"=trigger.body.in_reply_to","refs":"=size(trigger.body.references)","threaded":"=has(trigger.email.thread)"}}}]}`)
	var public string
	_ = w.env.DB.Admin.QueryRow(ctx, `SELECT public_id FROM email_endpoints WHERE workflow_id = $1`, wf).Scan(&public)
	for _, id := range []string{"a@x.example", "b@x.example"} {
		w.receiveTo(public, "shop-2", customerMail(id, "ada@customer.example", "Re: [#T-1] Order", "In-Reply-To: <tk-1@in.taskiem.test>\nReferences: <c0@x.example> <tk-1@in.taskiem.test>\n", "Hi."),
			inmail.Auth{Aligned: true}, "ada@customer.example")
	}
	w.drain()
	runs := w.runs(wf)
	if len(runs) != 2 {
		t.Fatalf("runs %d", len(runs))
	}
	out := w.event(runs[0], history.StepCompleted, "read")["output"].(map[string]any)
	if out["tag"] != "shop-2" || out["irt"] != "tk-1@in.taskiem.test" || out["refs"] != float64(2) || out["threaded"] != false {
		t.Fatalf("read: %v", out)
	}
}
