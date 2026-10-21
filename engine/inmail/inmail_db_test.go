package inmail_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/forms"
	"github.com/israel-duff/taskiem/engine/ingest"
	"github.com/israel-duff/taskiem/engine/inmail"
	"github.com/israel-duff/taskiem/engine/media"
	rt "github.com/israel-duff/taskiem/engine/runtime/runtimetest"
	"github.com/israel-duff/taskiem/engine/wd"
)

type cleanScanner struct{}

func (cleanScanner) Scan(context.Context, []byte) (string, error) { return "", nil }

type memFiles struct {
	mu    sync.Mutex
	files map[string][]byte
}

func (f *memFiles) PutFile(_ context.Context, _ uuid.UUID, data []byte) (media.File, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	p := "files/" + uuid.NewString() + ".pdf"
	f.files[p] = data
	return media.File{Path: p, Type: "application/pdf", Size: int64(len(data))}, nil
}

func (f *memFiles) SignURL(_ context.Context, _ uuid.UUID, path string, ttl time.Duration) (string, time.Time, error) {
	return "https://storage.test/" + path, time.Now().Add(ttl), nil
}

const alerts = `{"schema":"wd/v1","id":"wf_alerts","version":1,"name":"Bank alerts",
 "trigger":{"type":"email","config":{"senders":["firstbank.example"],"attachments":"keep"}},
 "steps":[{"id":"read","type":"transform","config":{"output":{
   "paid":"=trigger.body.text.contains('Ada Stores')","files":"=size(trigger.body.attachments)",
   "aligned":"=trigger.email.auth.aligned","subject":"=trigger.body.subject"}}}]}`

type world struct {
	t    *testing.T
	env  *rt.Env
	svc  *inmail.Service
	keys forms.Keys
}

func newWorld(t *testing.T) *world {
	env := rt.New(t)
	keys, err := forms.NewKeys(bytes.Repeat([]byte{5}, 32))
	if err != nil {
		t.Fatal(err)
	}
	svc := &inmail.Service{Pool: env.DB.App, Keys: keys, Files: &memFiles{files: map[string][]byte{}}, Scanner: cleanScanner{},
		Start: env.Store.Start, Domain: "in.taskiem.test", AppURL: "https://app.test", OwnSenders: []string{"no-reply@taskiem.test"}}
	return &world{t: t, env: env, svc: svc, keys: keys}
}

func (w *world) deploy(doc string) uuid.UUID {
	w.t.Helper()
	wf := w.env.Publish(w.t, doc)
	def, err := wd.Load([]byte(doc))
	if err != nil {
		w.t.Fatal(err)
	}
	if err := db.InTenantTx(context.Background(), w.env.DB.App, []uuid.UUID{w.env.Tenant}, func(tx pgx.Tx) error {
		return ingest.SyncEnvironments(context.Background(), tx, w.env.Tenant, wf, []string{"prod"}, 1, def, w.env.Registry, time.Now())
	}); err != nil {
		w.t.Fatal(err)
	}
	return wf
}

// receive leaves a message in the inbox as the mail role does, through
// the database functions taskiem_public may call.
func (w *world) receive(public string, raw []byte, auth inmail.Auth, mailFrom string) bool {
	w.t.Helper()
	ctx := context.Background()
	st := inmail.DBStore{Pool: w.env.DB.PublicPool(w.t, 1)}
	ep, ok, err := st.Recipient(ctx, public)
	if err != nil || !ok {
		w.t.Fatalf("recipient %s: %v %v", public, ok, err)
	}
	id := uuid.Must(uuid.NewV7())
	meta := inmail.Meta{Reference: id.String(), Address: public + "@in.taskiem.test", MailFrom: mailFrom, RemoteIP: "192.0.2.1", ReceivedAt: time.Now(), Auth: auth}
	sm, err := w.keys.Seal(inmail.MetaAAD(id), meta)
	if err != nil {
		w.t.Fatal(err)
	}
	sr, err := w.keys.SealBytes(inmail.RawAAD(id), raw)
	if err != nil {
		w.t.Fatal(err)
	}
	h := sha256.Sum256(append(ep[:], raw...))
	stored, err := st.Receive(ctx, id, ep, h[:], sm, sr)
	if err != nil {
		w.t.Fatal(err)
	}
	return stored
}

func (w *world) drain() {
	w.t.Helper()
	if err := w.svc.Drain(context.Background()); err != nil {
		w.t.Fatal(err)
	}
	w.env.Drain(w.t)
}

func message(id, from, subject, extraHeaders string) []byte {
	return []byte(strings.ReplaceAll(`From: Alerts <`+from+`>
To: owner@gmail.com
Subject: `+subject+`
Message-ID: <`+id+`>
`+extraHeaders+`MIME-Version: 1.0
Content-Type: multipart/mixed; boundary="b"

--b
Content-Type: text/plain; charset=utf-8

Amount: 50,000.00
Narration: Payment from Ada Stores
--b
Content-Type: application/pdf; name="statement.pdf"
Content-Disposition: attachment; filename="statement.pdf"

%PDF-1.4 statement
--b
Content-Type: application/zip; name="x.zip"
Content-Disposition: attachment; filename="x.zip"

PK
--b--
`, "\n", "\r\n"))
}

func TestEmailStartsARun(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()
	wf := w.deploy(alerts)
	var public string
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT public_id FROM email_endpoints WHERE workflow_id = $1 AND environment = 'prod'`, wf).Scan(&public); err != nil {
		t.Fatal(err)
	}
	proven := inmail.Auth{SPF: "pass", DKIM: []string{"firstbank.example"}, Aligned: true}

	// Unknown addresses are not ours.
	if _, ok, _ := (inmail.DBStore{Pool: w.env.DB.PublicPool(t, 1)}).Recipient(ctx, "zzzzzzzzzzzz"); ok {
		t.Fatal("an unknown address is taken")
	}

	if !w.receive(public, message("a1@firstbank.example", "alerts@firstbank.example", "Credit Alert", ""), proven, "alerts@firstbank.example") {
		t.Fatal("refused")
	}
	// Delivered twice: kept once.
	w.receive(public, message("a1@firstbank.example", "alerts@firstbank.example", "Credit Alert", ""), proven, "alerts@firstbank.example")
	w.drain()

	var status string
	var run *uuid.UUID
	var files int
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT status, run_id, attachments FROM email_messages WHERE workflow_id = $1`, wf).Scan(&status, &run, &files); err != nil {
		t.Fatal(err)
	}
	if status != "started" || run == nil || files != 1 {
		t.Fatalf("message: %s %v %d", status, run, files)
	}
	var started, out []byte
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT payload FROM run_events WHERE run_id = $1 AND type = 'RunStarted'`, *run).Scan(&started); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(started, []byte("Ada Stores")) || bytes.Contains(started, []byte("alerts@firstbank.example")) || bytes.Contains(started, []byte("Credit Alert")) {
		t.Errorf("the message is in the run's history unsealed: %s", started)
	}
	if !bytes.Contains(started, []byte("x.zip")) && !bytes.Contains(started, []byte(`"skipped"`)) {
		t.Errorf("the skipped attachment is not listed: %s", started)
	}
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT payload FROM run_events WHERE run_id = $1 AND type = 'StepCompleted' AND step_id = 'read'`, *run).Scan(&out); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"paid": true`, `"files": 1`, `"aligned": true`} {
		if !bytes.Contains(out, []byte(want)) {
			t.Errorf("the run read %s, want %s", out, want)
		}
	}
	if bytes.Contains(out, []byte("Credit Alert")) {
		t.Errorf("the subject copied by a step is not sealed: %s", out)
	}
	var n int
	_ = w.env.DB.Admin.QueryRow(ctx, `SELECT count(*) FROM email_files`).Scan(&n)
	if n != 1 {
		t.Errorf("email files: %d", n)
	}
	var fileID uuid.UUID
	_ = w.env.DB.Admin.QueryRow(ctx, `SELECT id FROM email_files`).Scan(&fileID)
	link, err := w.svc.FileURL(ctx, w.env.Tenant, fileID.String(), 0)
	if err != nil || !strings.HasPrefix(link["url"].(string), "https://storage.test/") {
		t.Errorf("file url: %v %v", link, err)
	}

	// A sender not on the list, an unproven one, and an automatic reply
	// start nothing, and say why.
	w.receive(public, message("a2@other.example", "alerts@other.example", "Hi", ""), inmail.Auth{Aligned: true}, "alerts@other.example")
	w.receive(public, message("a3@firstbank.example", "alerts@firstbank.example", "Forged", ""), inmail.Auth{}, "alerts@firstbank.example")
	w.receive(public, message("a4@firstbank.example", "alerts@firstbank.example", "Away", "Auto-Submitted: auto-replied\n"), proven, "alerts@firstbank.example")
	w.drain()
	rows, err := w.env.DB.Admin.Query(ctx, `SELECT status, COALESCE(note, '') FROM email_messages WHERE workflow_id = $1 AND status = 'refused' ORDER BY received_at`, wf)
	if err != nil {
		t.Fatal(err)
	}
	notes, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (string, error) {
		var s, n string
		return n, r.Scan(&s, &n)
	})
	if err != nil || len(notes) != 3 || !strings.Contains(notes[0], "not one this workflow") || strings.Contains(notes[0], "proven") || !strings.Contains(notes[1], "could not be proven") ||
		!strings.Contains(notes[2], "automatic reply") {
		t.Fatalf("refusals: %q %v", notes, err)
	}

	// A sample message runs it too, whatever the senders list says.
	if _, note, err := w.svc.Sample(ctx, w.env.Tenant, endpointOf(t, w, wf), "Bank alerts"); err != nil || note != "" {
		t.Fatalf("sample: %q %v", note, err)
	}
}

func endpointOf(t *testing.T, w *world, wf uuid.UUID) uuid.UUID {
	var id uuid.UUID
	if err := w.env.DB.Admin.QueryRow(context.Background(), `SELECT id FROM email_endpoints WHERE workflow_id = $1`, wf).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func TestForwardingConfirmationIsShownNotRun(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()
	wf := w.deploy(alerts)
	var public string
	_ = w.env.DB.Admin.QueryRow(ctx, `SELECT public_id FROM email_endpoints WHERE workflow_id = $1`, wf).Scan(&public)
	raw := []byte(strings.ReplaceAll(`From: Gmail Team <forwarding-noreply@google.com>
To: `+public+`@in.taskiem.test
Subject: (#123456789) Gmail Forwarding Confirmation - Receive Mail from owner@gmail.com
Message-ID: <c1@google.com>
Content-Type: text/plain

Confirmation code: 123456789
`, "\n", "\r\n"))
	w.receive(public, raw, inmail.Auth{DKIM: []string{"google.com"}, Aligned: true}, "forwarding-noreply@google.com")
	w.drain()
	var status string
	var run *uuid.UUID
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT status, run_id FROM email_messages WHERE workflow_id = $1`, wf).Scan(&status, &run); err != nil || status != "confirmation" || run != nil {
		t.Fatalf("message: %s %v %v", status, run, err)
	}
	var code string
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT confirmation->>'code' FROM email_endpoints WHERE workflow_id = $1`, wf).Scan(&code); err != nil || code != "123456789" {
		t.Fatalf("confirmation: %q %v", code, err)
	}
	var alerts int
	_ = w.env.DB.Admin.QueryRow(ctx, `SELECT count(*) FROM alerts WHERE kind = 'email_forwarding_confirmation'`).Scan(&alerts)
	if alerts != 1 {
		t.Errorf("owners not told: %d alerts", alerts)
	}
}

func TestClosedAddressTakesNothing(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()
	wf := w.deploy(alerts)
	var public string
	_ = w.env.DB.Admin.QueryRow(ctx, `SELECT public_id FROM email_endpoints WHERE workflow_id = $1`, wf).Scan(&public)
	// Republished with another trigger, the address closes.
	manual := strings.Replace(alerts, `"trigger":{"type":"email","config":{"senders":["firstbank.example"],"attachments":"keep"}}`, `"trigger":{"type":"manual"}`, 1)
	def, _ := wd.Load([]byte(manual))
	if err := db.InTenantTx(ctx, w.env.DB.App, []uuid.UUID{w.env.Tenant}, func(tx pgx.Tx) error {
		return ingest.SyncEnvironments(ctx, tx, w.env.Tenant, wf, []string{"prod"}, 1, def, w.env.Registry, time.Now())
	}); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := (inmail.DBStore{Pool: w.env.DB.PublicPool(t, 1)}).Recipient(ctx, public); ok {
		t.Error("a closed address still takes mail")
	}
}
