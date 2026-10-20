package forms_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/decide"
	"github.com/israel-duff/taskiem/engine/forms"
	"github.com/israel-duff/taskiem/engine/ingest"
	"github.com/israel-duff/taskiem/engine/media"
	rt "github.com/israel-duff/taskiem/engine/runtime/runtimetest"
	"github.com/israel-duff/taskiem/engine/sender"
	"github.com/israel-duff/taskiem/engine/wd"
)

type fakeScanner struct{ bad string }

func (f fakeScanner) Scan(_ context.Context, data []byte) (string, error) {
	if f.bad != "" && bytes.Contains(data, []byte(f.bad)) {
		return "Eicar-Test-Signature", nil
	}
	return "", nil
}

type fakeFiles struct {
	mu    sync.Mutex
	files map[string][]byte
}

func (f *fakeFiles) PutFile(_ context.Context, _ uuid.UUID, data []byte) (media.File, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	p := "files/" + uuid.NewString() + ".pdf"
	f.files[p] = data
	return media.File{Path: p, Type: "application/pdf", Size: int64(len(data))}, nil
}

func (f *fakeFiles) SignURL(_ context.Context, _ uuid.UUID, path string, ttl time.Duration) (string, time.Time, error) {
	return "https://storage.test/" + path + "?sig=1", time.Now().Add(ttl), nil
}

type world struct {
	t   *testing.T
	env *rt.Env
	svc *forms.Service
}

func newWorld(t *testing.T) *world {
	env := rt.New(t)
	keys, err := forms.NewKeys(bytes.Repeat([]byte{5}, 32))
	if err != nil {
		t.Fatal(err)
	}
	svc := &forms.Service{Pool: env.DB.App, Vault: env.Vault, Keys: keys, Files: &fakeFiles{files: map[string][]byte{}}, Scanner: fakeScanner{bad: "EICAR"},
		Messages: &sender.Queue{Vault: env.Vault, HashKey: bytes.Repeat([]byte{8}, 32)},
		Start:    env.Store.Start, Signal: env.Store.DeliverSignalOnce, FormHost: "forms.shops.test", AppURL: "https://app.test"}
	return &world{t: t, env: env, svc: svc}
}

func (w *world) in(fn func(pgx.Tx) error) {
	w.t.Helper()
	if err := db.InTenantTx(context.Background(), w.env.DB.App, []uuid.UUID{w.env.Tenant}, fn); err != nil {
		w.t.Fatal(err)
	}
}

// deploy publishes a definition and registers its trigger as deploying does.
func (w *world) deploy(doc string) uuid.UUID {
	w.t.Helper()
	wf := w.env.Publish(w.t, doc)
	def, err := wd.Load([]byte(doc))
	if err != nil {
		w.t.Fatal(err)
	}
	w.in(func(tx pgx.Tx) error {
		return ingest.SyncEnvironments(context.Background(), tx, w.env.Tenant, wf, []string{"prod"}, 1, def, w.env.Registry, time.Now())
	})
	return wf
}

// submit writes a sealed submission as the form host does.
func (w *world) submit(endpoint, request *uuid.UUID, sub forms.Submission, files ...[]byte) (string, error) {
	w.t.Helper()
	ref := "F-" + strings.ToUpper(uuid.NewString()[:8])
	sub.Submitted = time.Now()
	payload, err := w.svc.Keys.Seal(forms.SubmissionAAD(ref), sub)
	if err != nil {
		w.t.Fatal(err)
	}
	idem := sha256.Sum256([]byte(ref))
	hash := sha256.Sum256([]byte("status-" + ref))
	ctx := context.Background()
	pub := w.env.DB.PublicPool(w.t, 1)
	tx, err := pub.Begin(ctx)
	if err != nil {
		w.t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var inbox *uuid.UUID
	if err := tx.QueryRow(ctx, `SELECT taskiem_public_form_submit($1, $2, NULL, NULL, $3, $4, $5, $6)`, endpoint, request, ref, idem[:], hash[:], payload).Scan(&inbox); err != nil {
		return "", err
	}
	if inbox == nil {
		return "", errors.New("closed")
	}
	n := map[string]int{}
	for i, f := range files {
		field := sub.Files[i].Field
		sealed, _ := w.svc.Keys.SealBytes(forms.UploadAAD(ref, field, n[field]), f)
		n[field]++
		if _, err := tx.Exec(ctx, `SELECT taskiem_public_form_upload($1, $2, $3)`, *inbox, field, sealed); err != nil {
			return "", err
		}
	}
	return ref, tx.Commit(ctx)
}

func (w *world) drain() {
	w.t.Helper()
	time.Sleep(2100 * time.Millisecond) // the inbox waits 2 s for a submission's files
	if err := w.svc.Drain(context.Background()); err != nil {
		w.t.Fatal(err)
	}
	w.env.Drain(w.t)
}

const intake = `{"schema":"wd/v1","id":"wf_intake","version":1,"name":"Intake",
 "trigger":{"type":"form","config":{"title":"Apply","access":"public"}},
 "inputs":{"schema":{"type":"object","required":["full_name","phone"],"properties":{
  "full_name":{"type":"string","maxLength":100},"phone":{"type":"string","x-pii":"phone"},"payslip":{"type":"string","format":"file","x-form":{"accept":["application/pdf"]}}}}},
 "steps":[{"id":"note","type":"transform","config":{"output":{"who":"=trigger.body.full_name","kind":"=trigger.form.respondent.kind","file":"=has(trigger.body.payslip) ? trigger.body.payslip.name : ''"}}}]}`

func TestFormTriggerStartsARun(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()
	wf := w.deploy(intake)
	var endpoint uuid.UUID
	var public string
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT id, public_id FROM form_endpoints WHERE workflow_id = $1 AND environment = 'prod'`, wf).Scan(&endpoint, &public); err != nil {
		t.Fatal(err)
	}
	// The form host reads the live form by its public id.
	var page map[string]any
	if err := w.env.DB.PublicPool(t, 1).QueryRow(ctx, `SELECT taskiem_public_form($1)`, public).Scan(&page); err != nil || page["open"] != true || page["business"] == "" {
		t.Fatalf("public form: %v %v", page, err)
	}

	ref, err := w.submit(&endpoint, nil, forms.Submission{Answers: map[string]any{"full_name": "Bola Ade", "phone": "+2348031234567"},
		Respondent: forms.Respondent{Kind: "anonymous"}, Channel: "direct",
		Files: []forms.FileMeta{{Field: "payslip", Name: "payslip.pdf", Type: "application/pdf", Size: 9}}}, []byte("%PDF-1.4 x"))
	if err != nil {
		t.Fatal(err)
	}
	w.drain()
	var status, answers string
	var run *uuid.UUID
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT status, answers::text, run_id FROM form_submissions WHERE reference = $1`, ref).Scan(&status, &answers, &run); err != nil {
		t.Fatal(err)
	}
	if status != "started" || run == nil || strings.Contains(answers, "8031234567") || !strings.Contains(answers, "Bola Ade") {
		t.Fatalf("submission: %s %s %v", status, answers, run)
	}
	var out []byte
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT payload FROM run_events WHERE run_id = $1 AND type = 'StepCompleted' AND step_id = 'note'`, *run).Scan(&out); err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(out, []byte(`"who": "Bola Ade"`)) || !bytes.Contains(out, []byte(`"kind": "anonymous"`)) || !bytes.Contains(out, []byte(`"file": "payslip.pdf"`)) {
		t.Fatalf("the run read: %s", out)
	}
	var files int
	_ = w.env.DB.Admin.QueryRow(ctx, `SELECT count(*) FROM form_files`).Scan(&files)
	if files != 1 {
		t.Fatalf("files: %d", files)
	}

	// A file that fails the scan refuses the submission; nothing starts.
	bad, err := w.submit(&endpoint, nil, forms.Submission{Answers: map[string]any{"full_name": "Eve", "phone": "+2348031234568"}, Respondent: forms.Respondent{Kind: "anonymous"},
		Files: []forms.FileMeta{{Field: "payslip", Name: "x.pdf", Type: "application/pdf", Size: 5}}}, []byte("EICAR"))
	if err != nil {
		t.Fatal(err)
	}
	w.drain()
	var note string
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT status, COALESCE(note, '') FROM form_submissions WHERE reference = $1`, bad).Scan(&status, &note); err != nil || status != "refused" || !strings.Contains(note, "malware") {
		t.Fatalf("infected: %s %q %v", status, note, err)
	}

	// The workflow's message reaches the status link.
	if _, err := w.svc.Respond(ctx, w.env.Tenant, *run, "With our credit team"); err != nil {
		t.Fatal(err)
	}
	var msg string
	h := sha256.Sum256([]byte("status-" + ref))
	if err := w.env.DB.PublicPool(t, 1).QueryRow(ctx, `SELECT message FROM taskiem_public_form_status($1, $2)`, ref, h[:]).Scan(&msg); err != nil || msg != "With our credit team" {
		t.Fatalf("status: %q %v", msg, err)
	}

	// Paused, it takes nothing.
	w.in(func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `UPDATE form_endpoints SET status = 'paused' WHERE id = $1`, endpoint)
		return err
	})
	if _, err := w.submit(&endpoint, nil, forms.Submission{Answers: map[string]any{"full_name": "Late"}}); err == nil || err.Error() != "closed" {
		t.Fatalf("paused: %v", err)
	}
}

const askFlow = `{"schema":"wd/v1","id":"wf_ask","version":1,"name":"Ask","trigger":{"type":"manual"},
 "inputs":{"schema":{"type":"object","properties":{"phone":{"type":"string","x-pii":"phone"},"name":{"type":"string","x-pii":"name"}}}},
 "steps":[{"id":"ask","type":"form","config":{"title":"Confirm your bank","to":{"phone":"=trigger.body.phone"},"via":["sms"],"verify":true,
   "schema":{"type":"object","required":["bank"],"properties":{"bank":{"type":"string"},"account_number":{"type":"string","x-pii":"account_number"}}},
   "prefill":{"name":"=trigger.body.name"},"remind":["1h"]}},
  {"id":"got","type":"transform","needs":["ask"],"config":{"output":{"bank":"=steps.ask.output.bank","who":"=steps.ask.output.respondent.kind"}}}]}`

func TestFormStepLinkAndAnswer(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()
	wf := w.env.Publish(t, askFlow)
	run := w.env.Start(t, wf, map[string]any{"body": map[string]any{"phone": "+2348031234567", "name": "Bola Ade"}})
	w.env.Drain(t)
	req := uuid.MustParse(decide.FormRequestID(run.ID.String(), "ask"))

	// The link goes by SMS, with the token the form host looks up.
	if err := w.svc.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	token := w.svc.Keys.RequestToken(req.String())
	var view map[string]any
	if err := w.env.DB.PublicPool(t, 1).QueryRow(ctx, `SELECT taskiem_public_form_request($1)`, forms.TokenHash(token)).Scan(&view); err != nil || view["status"] != "pending" {
		t.Fatalf("request view: %v %v", view, err)
	}
	var v forms.RequestView
	raw, _ := json.Marshal(view["public_view"])
	var b64 string
	_ = json.Unmarshal(raw, &b64)
	sealed, _ := decodeB64(b64)
	if err := w.svc.Keys.Open(forms.RequestViewAAD(req.String()), sealed, &v); err != nil || v.Prefill["name"] != "Bola Ade" || v.Hint != "•••4567" || v.Channel != "sms" {
		t.Fatalf("public view: %+v %v", v, err)
	}
	var queued int
	_ = w.env.DB.Admin.QueryRow(ctx, `SELECT count(*) FROM platform_messages WHERE dedup_key LIKE 'form:%:link:sms'`).Scan(&queued)
	if queued != 1 {
		t.Fatalf("links queued: %d", queued)
	}
	// A reminder an hour on; not before.
	if err := w.svc.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	w.svc.Now = func() time.Time { return time.Now().Add(61 * time.Minute) }
	if err := w.svc.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	_ = w.env.DB.Admin.QueryRow(ctx, `SELECT count(*) FROM platform_messages WHERE dedup_key LIKE 'form:%'`).Scan(&queued)
	if queued != 2 {
		t.Fatalf("with the reminder: %d", queued)
	}
	w.svc.Now = nil

	// A code for the request goes to its recipient.
	code := w.svc.Keys.CodeHash(req.String(), "123456")
	payload, _ := w.svc.Keys.Seal(forms.CodeAAD(req.String()), forms.CodeRequest{Code: "123456"})
	var ok bool
	if err := w.env.DB.PublicPool(t, 1).QueryRow(ctx, `SELECT taskiem_public_form_code($1, NULL, $2, $3)`, req, code, payload).Scan(&ok); err != nil || !ok {
		t.Fatalf("code: %v %v", ok, err)
	}
	if err := w.svc.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	_ = w.env.DB.Admin.QueryRow(ctx, `SELECT count(*) FROM platform_messages WHERE purpose = 'code'`).Scan(&queued)
	if queued != 1 {
		t.Fatalf("codes: %d", queued)
	}
	if err := w.env.DB.PublicPool(t, 1).QueryRow(ctx, `SELECT taskiem_public_form_code_check($1, NULL, $2)`, req, code).Scan(&ok); err != nil || !ok {
		t.Fatalf("code check: %v %v", ok, err)
	}

	// The answer completes the step; a second is refused.
	ref, err := w.submit(nil, &req, forms.Submission{Answers: map[string]any{"bank": "GTBank", "account_number": "0123456789"},
		Respondent: forms.Respondent{Kind: "request", Phone: "+2348031234567"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.submit(nil, &req, forms.Submission{Answers: map[string]any{"bank": "Zenith"}}); err == nil {
		t.Fatal("answered twice")
	} else if pe := (*pgconn.PgError)(nil); !errors.As(err, &pe) {
		t.Fatalf("second answer: %v", err)
	}
	w.drain()
	if st := w.env.Status(t, run); st != "completed" {
		t.Fatalf("run: %s", st)
	}
	var out []byte
	_ = w.env.DB.Admin.QueryRow(ctx, `SELECT payload FROM run_events WHERE run_id = $1 AND type = 'StepCompleted' AND step_id = 'got'`, run.ID).Scan(&out)
	if !bytes.Contains(out, []byte(`"bank": "GTBank"`)) || !bytes.Contains(out, []byte(`"who": "request"`)) {
		t.Fatalf("the run read: %s", out)
	}
	var status string
	_ = w.env.DB.Admin.QueryRow(ctx, `SELECT status FROM form_requests WHERE id = $1`, req).Scan(&status)
	var answers string
	_ = w.env.DB.Admin.QueryRow(ctx, `SELECT answers::text FROM form_submissions WHERE reference = $1`, ref).Scan(&answers)
	if status != "answered" || strings.Contains(answers, "0123456789") {
		t.Fatalf("after: %s %s", status, answers)
	}
}

// decodeB64 reads Postgres's base64 (with its line breaks).
func decodeB64(s string) ([]byte, error) {
	return base64.StdEncoding.DecodeString(strings.ReplaceAll(s, "\n", ""))
}

const pagedForm = `{"schema":"wd/v1","id":"wf_loan","version":1,"name":"Loan",
 "trigger":{"type":"form","config":{"title":"Loan","pages":[{"fields":["employed"]},{"fields":["employer"],"show_if":"answers.employed == true"}]}},
 "inputs":{"schema":{"type":"object","required":["employed","employer"],"properties":{"employed":{"type":"boolean"},"employer":{"type":"string"}}}},
 "steps":[{"id":"n","type":"transform","config":{"output":{"x":1}}}]}`

// A required field on a page its condition hides is not asked, so not
// required; an answer to it is dropped.
func TestHiddenPagesAreNotRequired(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()
	wf := w.deploy(pagedForm)
	var endpoint uuid.UUID
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT id FROM form_endpoints WHERE workflow_id = $1`, wf).Scan(&endpoint); err != nil {
		t.Fatal(err)
	}
	ref, err := w.submit(&endpoint, nil, forms.Submission{Answers: map[string]any{"employed": false, "employer": "sneaked in"}, Respondent: forms.Respondent{Kind: "anonymous"}})
	if err != nil {
		t.Fatal(err)
	}
	refused, err := w.submit(&endpoint, nil, forms.Submission{Answers: map[string]any{"employed": true}, Respondent: forms.Respondent{Kind: "anonymous"}})
	if err != nil {
		t.Fatal(err)
	}
	w.drain()
	var status, answers string
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT status, answers::text FROM form_submissions WHERE reference = $1`, ref).Scan(&status, &answers); err != nil || status != "started" || strings.Contains(answers, "sneaked") {
		t.Fatalf("hidden page: %s %s %v", status, answers, err)
	}
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT status FROM form_submissions WHERE reference = $1`, refused).Scan(&status); err != nil || status != "refused" {
		t.Fatalf("shown page, not answered: %s %v", status, err)
	}
}
