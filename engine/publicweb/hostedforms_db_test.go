package publicweb

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"golang.org/x/time/rate"

	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/formguard"
	"github.com/israel-duff/taskiem/engine/forms"
	"github.com/israel-duff/taskiem/engine/ingest"
	"github.com/israel-duff/taskiem/engine/media"
	rt "github.com/israel-duff/taskiem/engine/runtime/runtimetest"
	"github.com/israel-duff/taskiem/engine/sender"
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
	p := "files/" + uuid.NewString()
	f.files[p] = data
	return media.File{Path: p, Type: "application/pdf", Size: int64(len(data))}, nil
}

func (f *memFiles) SignURL(_ context.Context, _ uuid.UUID, path string, ttl time.Duration) (string, time.Time, error) {
	return "https://storage.test/" + path, time.Now().Add(ttl), nil
}

const hostedIntake = `{"schema":"wd/v1","id":"wf_apply","version":1,"name":"Apply",
 "trigger":{"type":"form","config":{"title":"Apply","access":"public","verify":"phone","once_per":"phone",
   "pages":[{"fields":["full_name","phone"]},{"fields":["payslip"],"show_if":"answers.full_name != ''"}],"after":{"mode":"message","status":true}}},
 "inputs":{"schema":{"type":"object","required":["full_name","phone"],"properties":{
  "full_name":{"type":"string","maxLength":100},"phone":{"type":"string","format":"phone-ng","x-pii":"phone"},
  "payslip":{"type":"string","format":"file","x-form":{"accept":["application/pdf"]}}}}},
 "steps":[{"id":"note","type":"transform","config":{"output":{"who":"=trigger.body.full_name","kind":"=trigger.form.respondent.kind","file":"=has(trigger.body.payslip) ? trigger.body.payslip.name : ''"}}}]}`

// TestHostedFormThroughTheDatabase fills in a form on the form host over
// the real functions of 00182, as taskiem_public: a code, two pages, a
// file; then the api role drains the inbox and the run starts.
func TestHostedFormThroughTheDatabase(t *testing.T) {
	env := rt.New(t)
	ctx := context.Background()
	keys, err := forms.NewKeys(testMaster)
	if err != nil {
		t.Fatal(err)
	}
	wf := env.Publish(t, hostedIntake)
	def, err := wd.Load([]byte(hostedIntake))
	if err != nil {
		t.Fatal(err)
	}
	if err := db.InTenantTx(ctx, env.DB.App, []uuid.UUID{env.Tenant}, func(tx pgx.Tx) error {
		return ingest.SyncEnvironments(ctx, tx, env.Tenant, wf, []string{"prod"}, 1, def, env.Registry, time.Now())
	}); err != nil {
		t.Fatal(err)
	}
	var public string
	if err := env.DB.Admin.QueryRow(ctx, `SELECT public_id FROM form_endpoints WHERE workflow_id = $1`, wf).Scan(&public); err != nil {
		t.Fatal(err)
	}

	pool := env.DB.PublicPool(t, 2)
	offset := time.Duration(0)
	s := &Server{Lookup: DBLookup(pool), Origins: DBOrigins(pool), Forms: DBForms(pool), FormHost: formHost, FormKeys: &keys,
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Views: formguard.NewLimiter(rate.Inf, 1, 100, time.Hour),
		FormSubmissions: formguard.NewLimiter(rate.Inf, 1, 100, time.Hour)}
	s.now = func() time.Time { return time.Now().Add(offset) }
	e := &hostEnv{t: t, s: s, h: s.Handler(), keys: keys}
	path := "/f/" + public

	body := must(t, e.get(path), 200, "<h1>Apply</h1>", "Your phone number")
	body = must(t, e.next(path+"/code", body, url.Values{"address": {"0803 123 4567"}}), 200, "We sent a code to •••4567")
	var sealed []byte
	if err := env.DB.Admin.QueryRow(ctx, `SELECT payload FROM form_codes ORDER BY created_at DESC LIMIT 1`).Scan(&sealed); err != nil {
		t.Fatal(err)
	}
	var endpoint uuid.UUID
	_ = env.DB.Admin.QueryRow(ctx, `SELECT id FROM form_endpoints WHERE public_id = $1`, public).Scan(&endpoint)
	var code forms.CodeRequest
	if err := keys.Open(forms.CodeAAD(endpoint.String()), sealed, &code); err != nil || code.Address != "+2348031234567" {
		t.Fatalf("the code: %+v %v", code, err)
	}
	must(t, e.next(path, body, url.Values{"do": {"verify"}, "code": {wrongCode(code.Code)}}), 422, "That code is not right")
	body = must(t, e.next(path, body, url.Values{"do": {"verify"}, "code": {code.Code}}), 200, `name="a.full_name"`)
	body = must(t, e.next(path, body, url.Values{"do": {"next"}, "a.full_name": {"Bola Ade"}, "a.phone": {"08031234567"}}), 200, `name="a.payslip"`)
	offset = 10 * time.Second
	sent := e.next(path, body, url.Values{"do": {"send"}}, upload{"a.payslip", "slip.pdf", pdf})
	done := must(t, sent, 200, "Thank you", "See what happens next")
	ref := refRe.FindStringSubmatch(done)[1]
	// A double tap answers with the same reference, and writes nothing more.
	again := must(t, e.next(path, body, url.Values{"do": {"send"}}, upload{"a.payslip", "slip.pdf", pdf}), 200)
	if refRe.FindStringSubmatch(again)[1] != ref {
		t.Fatalf("a repeat drew another reference: %s", again)
	}
	var n int
	_ = env.DB.Admin.QueryRow(ctx, `SELECT count(*) FROM form_inbox`).Scan(&n)
	if n != 1 {
		t.Fatalf("inbox rows: %d", n)
	}
	statusLink := regexpFind(t, done, `href="(/s/[^"]+)"`)
	must(t, e.get(htmlUnescape(statusLink)), 200, "Your answers have arrived")

	// The api role takes it, and the run starts with the answers.
	svc := &forms.Service{Pool: env.DB.App, Vault: env.Vault, Keys: keys, Files: &memFiles{files: map[string][]byte{}}, Scanner: cleanScanner{},
		Messages: &sender.Queue{Vault: env.Vault, HashKey: bytes.Repeat([]byte{8}, 32)}, Start: env.Store.Start, Signal: env.Store.DeliverSignalOnce, FormHost: formHost}
	time.Sleep(2100 * time.Millisecond) // the inbox waits 2 s for a submission's files
	if err := svc.Drain(ctx); err != nil {
		t.Fatal(err)
	}
	env.Drain(t)
	var status string
	var run *uuid.UUID
	if err := env.DB.Admin.QueryRow(ctx, `SELECT status, run_id FROM form_submissions WHERE reference = $1`, ref).Scan(&status, &run); err != nil || status != "started" || run == nil {
		t.Fatalf("submission: %s %v %v", status, run, err)
	}
	var out []byte
	if err := env.DB.Admin.QueryRow(ctx, `SELECT payload FROM run_events WHERE run_id = $1 AND type = 'StepCompleted' AND step_id = 'note'`, *run).Scan(&out); err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(out, []byte(`"who": "Bola Ade"`)) || !bytes.Contains(out, []byte(`"kind": "verified"`)) || !bytes.Contains(out, []byte(`"file": "slip.pdf"`)) {
		t.Fatalf("the run read: %s", out)
	}
	must(t, e.get(htmlUnescape(statusLink)), 200, "being handled")

	// once_per phone: the same phone again is refused.
	body = must(t, e.get(path), 200)
	body = must(t, e.next(path+"/code", body, url.Values{"address": {"08031234567"}}), 200)
	_ = env.DB.Admin.QueryRow(ctx, `SELECT payload FROM form_codes ORDER BY created_at DESC LIMIT 1`).Scan(&sealed)
	_ = keys.Open(forms.CodeAAD(endpoint.String()), sealed, &code)
	body = must(t, e.next(path, body, url.Values{"do": {"verify"}, "code": {code.Code}}), 200)
	body = must(t, e.next(path, body, url.Values{"do": {"next"}, "a.full_name": {"Bola Ade"}, "a.phone": {"08031234567"}}), 200)
	offset += 10 * time.Second
	must(t, e.next(path, body, url.Values{"do": {"send"}}, upload{"a.payslip", "slip.pdf", pdf}), 409, "You have already sent this form")
}

func wrongCode(c string) string {
	if c == "123456" {
		return "654321"
	}
	return "123456"
}

func regexpFind(t *testing.T, s, re string) string {
	t.Helper()
	m := regexp.MustCompile(re).FindStringSubmatch(s)
	if m == nil {
		t.Fatalf("no %s in %s", re, s)
	}
	return m[1]
}

func htmlUnescape(s string) string { return strings.ReplaceAll(s, "&amp;", "&") }
