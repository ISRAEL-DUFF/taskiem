package ingest_test

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/hex"
	"encoding/json"
	"hash"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/connectors/paystack"
	"github.com/israel-duff/taskiem/engine/connector"
	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/ingest"
	"github.com/israel-duff/taskiem/engine/runtime"
	rt "github.com/israel-duff/taskiem/engine/runtime/runtimetest"
	"github.com/israel-duff/taskiem/engine/wd"
)

var ctx = context.Background()

type world struct {
	*rt.Env
	srv *httptest.Server
	h   *ingest.Handler
}

func newWorld(t *testing.T) *world {
	e := rt.New(t)
	if err := e.Registry.Register(paystack.New(paystack.Options{})); err != nil {
		t.Fatal(err)
	}
	err := db.InTenantTx(ctx, e.DB.App, []uuid.UUID{e.Tenant}, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO environments (tenant_id, name) VALUES ($1, 'dev'), ($1, 'prod')`, e.Tenant)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	h := &ingest.Handler{Store: e.Store, Secrets: e.Vault, Connections: e.Vault, Registry: e.Registry}
	mux := http.NewServeMux()
	mux.Handle("/hooks/", http.StripPrefix("/hooks", h))
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return &world{Env: e, srv: srv, h: h}
}

// publish stores a version and registers its trigger, as the API does.
func (w *world) publish(t *testing.T, doc string) uuid.UUID {
	t.Helper()
	wf := w.Publish(t, doc)
	def, err := wd.Load([]byte(doc))
	if err != nil {
		t.Fatal(err)
	}
	err = db.InTenantTx(ctx, w.DB.App, []uuid.UUID{w.Tenant}, func(tx pgx.Tx) error {
		return ingest.Sync(ctx, tx, w.Tenant, wf, 1, def, w.Registry, time.Now())
	})
	if err != nil {
		t.Fatal(err)
	}
	return wf
}

func (w *world) post(t *testing.T, path string, body []byte, hdr ...string) (int, map[string]any) {
	t.Helper()
	req, _ := http.NewRequest("POST", w.srv.URL+"/hooks/"+w.Tenant.String()+path, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	for i := 0; i+1 < len(hdr); i += 2 {
		req.Header.Set(hdr[i], hdr[i+1])
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(resp.Body)
	var out map[string]any
	_ = json.Unmarshal(raw, &out)
	return resp.StatusCode, out
}

func sign(h func() hash.Hash, key string, body []byte) string {
	m := hmac.New(h, []byte(key))
	m.Write(body)
	return hex.EncodeToString(m.Sum(nil))
}

func (w *world) runCount(t *testing.T, wf uuid.UUID) int {
	t.Helper()
	var n int
	err := db.InTenantTx(ctx, w.DB.App, []uuid.UUID{w.Tenant}, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT count(*) FROM runs WHERE workflow_id = $1`, wf).Scan(&n)
	})
	if err != nil {
		t.Fatal(err)
	}
	return n
}

const hookFlow = `{"schema":"wd/v1","id":"wf_orders","version":1,"name":"orders",
  "trigger":{"type":"webhook","config":{"path":"/shop/orders","auth":"hmac","dedup":"=trigger.body.order_id"}},
  "inputs":{"schema":{"type":"object","required":["order_id"],"properties":{"order_id":{"type":"string"},"total":{"type":"integer"}}}},
  "steps":[{"id":"echo","type":"transform","config":{"output":{"order":"=trigger.body.order_id","src":"=trigger.headers['x-shop']"}}}]}`

func TestWebhookVerifiesDedupsAndRecords(t *testing.T) {
	w := newWorld(t)
	wf := w.publish(t, hookFlow)
	if _, err := w.Vault.Put(ctx, w.Tenant, "prod", "webhook_wf_orders", []byte("whsec"), "test"); err != nil {
		t.Fatal(err)
	}
	body := []byte(`{"order_id":"A1","total":500}`)

	if st, _ := w.post(t, "/shop/orders", body); st != 401 {
		t.Errorf("unsigned: %d", st)
	}
	if st, _ := w.post(t, "/shop/orders", body, "X-Taskiem-Signature", "sha256="+sign(sha256.New, "wrong", body)); st != 401 {
		t.Errorf("wrong key: %d", st)
	}
	if st, _ := w.post(t, "/nope", body); st != 404 {
		t.Errorf("unknown path: %d", st)
	}
	sig := "sha256=" + sign(sha256.New, "whsec", body)
	st, first := w.post(t, "/shop/orders", body, "X-Taskiem-Signature", sig, "X-Shop", "lagos-1")
	if st != 202 || first["duplicate"] != false {
		t.Fatalf("first delivery: %d %v", st, first)
	}
	// A provider retry, with a different body that has the same order id.
	retry := []byte(`{"order_id":"A1","total":500,"attempt":2}`)
	st, again := w.post(t, "/shop/orders", retry, "X-Taskiem-Signature", "sha256="+sign(sha256.New, "whsec", retry))
	if st != 202 || again["run_id"] != first["run_id"] || again["duplicate"] != true {
		t.Fatalf("retry: %d %v", st, again)
	}
	bad := []byte(`{"total":1}`)
	if st, _ := w.post(t, "/shop/orders", bad, "X-Taskiem-Signature", "sha256="+sign(sha256.New, "whsec", bad)); st != 422 {
		t.Errorf("input not matching the schema: %d", st)
	}
	// dev has no secret, so its deliveries are refused.
	if st, _ := w.post(t, "/shop/orders?env=dev", body, "X-Taskiem-Signature", sig); st != 401 {
		t.Errorf("dev without a secret: %d", st)
	}
	if n := w.runCount(t, wf); n != 1 {
		t.Errorf("%d runs, want 1", n)
	}
	w.Drain(t)
	hist, err := w.Store.RunHistory(ctx, refOf(w, first))
	if err != nil {
		t.Fatal(err)
	}
	last := hist[len(hist)-1]
	if last.Type != "RunCompleted" || !bytes.Contains(hist[0].Payload, []byte(`"x-shop": "lagos-1"`)) || bytes.Contains(hist[0].Payload, []byte("signature")) {
		t.Errorf("history: %s ... %s", hist[0].Payload, last.Type)
	}
}

func TestWebhookCeiling(t *testing.T) {
	w := newWorld(t)
	w.h.Rate, w.h.Burst = 0.001, 2
	w.publish(t, `{"schema":"wd/v1","id":"wf_open","version":1,"name":"open","trigger":{"type":"webhook","config":{"path":"/open","auth":"none"}},
	  "steps":[{"id":"a","type":"transform","config":{"output":1}}]}`)
	codes := []int{}
	for i := 0; i < 3; i++ {
		st, _ := w.post(t, "/open", []byte(`{"i":`+string(rune('0'+i))+`}`))
		codes = append(codes, st)
	}
	if codes[0] != 202 || codes[1] != 202 || codes[2] != 429 {
		t.Errorf("statuses %v, want 202 202 429", codes)
	}
}

const settleFlow = `{"schema":"wd/v1","id":"wf_settle","version":1,"name":"settle","trigger":{"type":"manual"},
  "steps":[
    {"id":"wait","type":"signal","config":{"event":"paystack@1:transfer_event","correlation":"=trigger.body.ref","timeout":"1h"}},
    {"id":"out","type":"transform","needs":["wait"],"config":{"output":"=steps.wait.output.event"}}]}`

const onSuccessFlow = `{"schema":"wd/v1","id":"wf_receipt","version":1,"name":"receipt",
  "trigger":{"type":"connector_event","config":{"connector":"paystack@1","trigger":"transfer_event","events":["transfer.success"]}},
  "steps":[{"id":"a","type":"transform","config":{"output":"=trigger.body.data.reference"}}]}`

func TestConnectorEventsSignalAndStart(t *testing.T) {
	w := newWorld(t)
	if _, err := w.Vault.CreateConnection(ctx, w.Tenant, "prod", "paystack", "main", "api_key", map[string]string{"secret_key": "sk_test_x"}, "test"); err != nil {
		t.Fatal(err)
	}
	settle := w.Publish(t, settleFlow)
	receipts := w.publish(t, onSuccessFlow)
	run := w.Start(t, settle, map[string]any{"body": map[string]any{"ref": "T-1"}})

	body := []byte(`{"event":"transfer.success","data":{"reference":"T-1","amount":5000}}`)
	if st, _ := w.post(t, "/connectors/paystack@1/transfer_event", body, "x-paystack-signature", "bad"); st != 401 {
		t.Errorf("bad signature: %d", st)
	}
	sig := sign(sha512.New, "sk_test_x", body)
	st, out := w.post(t, "/connectors/paystack@1/transfer_event", body, "x-paystack-signature", sig)
	if st != 202 || out["signalled"] != float64(1) || len(out["runs"].([]any)) != 1 {
		t.Fatalf("delivery: %d %v", st, out)
	}
	if s := w.Status(t, run); s != "completed" {
		h, _ := w.Store.RunHistory(ctx, run)
		t.Errorf("waiting run: %s %s", s, h[len(h)-2].Payload)
	}
	// Paystack retries: nothing happens twice.
	st, out = w.post(t, "/connectors/paystack@1/transfer_event", body, "x-paystack-signature", sig)
	if st != 202 || out["duplicate"] != true || out["signalled"] != float64(0) {
		t.Fatalf("retry: %d %v", st, out)
	}
	failed := []byte(`{"event":"transfer.failed","data":{"reference":"T-2"}}`)
	if st, _ := w.post(t, "/connectors/paystack@1/transfer_event", failed, "x-paystack-signature", sign(sha512.New, "sk_test_x", failed)); st != 202 {
		t.Errorf("failed event: %d", st)
	}
	if n := w.runCount(t, receipts); n != 1 {
		t.Errorf("receipt runs: %d, want 1 (success only, once)", n)
	}
	other := []byte(`{"event":"charge.success","data":{"reference":"T-3"}}`)
	if st, out := w.post(t, "/connectors/paystack@1/transfer_event", other, "x-paystack-signature", sign(sha512.New, "sk_test_x", other)); st != 202 || out["ignored"] == nil {
		t.Errorf("undeclared event: %d %v", st, out)
	}
}

func TestScheduleFiresOnceAndAdvances(t *testing.T) {
	w := newWorld(t)
	wf := w.publish(t, `{"schema":"wd/v1","id":"wf_payroll","version":1,"name":"payroll","trigger":{"type":"schedule","config":{"cron":"0 9 25 * *"}},
	  "steps":[{"id":"a","type":"transform","config":{"output":"=trigger.scheduled_time"}}]}`)
	var next time.Time
	err := db.InTenantTx(ctx, w.DB.App, []uuid.UUID{w.Tenant}, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT next_fire_at FROM triggers WHERE workflow_id = $1`, wf).Scan(&next)
	})
	if err != nil {
		t.Fatal(err)
	}
	lagos, _ := time.LoadLocation("Africa/Lagos")
	if l := next.In(lagos); l.Day() != 25 || l.Hour() != 9 || l.Minute() != 0 {
		t.Errorf("next fire %v, want the 25th at 09:00 Lagos", l)
	}
	due := time.Date(2026, 9, 25, 8, 0, 0, 0, time.UTC)
	setNext := func() {
		err := db.InTenantTx(ctx, w.DB.App, []uuid.UUID{w.Tenant}, func(tx pgx.Tx) error {
			_, err := tx.Exec(ctx, `UPDATE triggers SET next_fire_at = $2, lease_until = NULL WHERE workflow_id = $1`, wf, due)
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	setNext()
	c := &ingest.Cron{Store: w.Store}
	if n, err := c.Tick(ctx); err != nil || n != 1 {
		t.Fatalf("tick: %d %v", n, err)
	}
	if n, _ := c.Tick(ctx); n != 0 {
		t.Errorf("second tick fired %d", n)
	}
	// A crash before the schedule advanced: the fire repeats, the run does not.
	setNext()
	if _, err := c.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	if n := w.runCount(t, wf); n != 1 {
		t.Errorf("%d runs, want 1", n)
	}
	err = db.InTenantTx(ctx, w.DB.App, []uuid.UUID{w.Tenant}, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT next_fire_at FROM triggers WHERE workflow_id = $1`, wf).Scan(&next)
	})
	if err != nil || !next.After(time.Now()) {
		t.Errorf("schedule did not skip to the future: %v %v", next, err)
	}
}

func TestCheckRejectsUnavailableTriggers(t *testing.T) {
	w := newWorld(t)
	for doc, want := range map[string]bool{
		`{"type":"webhook","config":{"path":"/a","auth":"mtls"}}`:                                                               false,
		`{"type":"webhook","config":{"path":"/connectors/x","auth":"none"}}`:                                                    false,
		`{"type":"schedule","config":{"cron":"not a cron"}}`:                                                                    false,
		`{"type":"schedule","config":{"cron":"0 9 * * 1-5","timezone":"Mars/Olympus"}}`:                                         false,
		`{"type":"connector_event","config":{"connector":"paystack@1","trigger":"nope"}}`:                                       false,
		`{"type":"connector_event","config":{"connector":"paystack@1","trigger":"transfer_event","events":["charge.success"]}}`: false,
		`{"type":"polling","config":{}}`:                                                                                        false,
		`{"type":"schedule","config":{"cron":"0 9 * * 1-5"}}`:                                                                   true,
	} {
		def, err := wd.Load([]byte(`{"schema":"wd/v1","id":"wf_x","version":1,"name":"x","trigger":` + doc + `,"steps":[{"id":"a","type":"transform","config":{"output":1}}]}`))
		if err != nil {
			if want {
				t.Errorf("%s: %v", doc, err)
			}
			continue
		}
		if got := ingest.Check(def, w.Registry) == nil; got != want {
			t.Errorf("%s: ok=%v, want %v", doc, got, want)
		}
	}
}

func refOf(w *world, out map[string]any) runtime.RunRef {
	return runtime.RunRef{ID: uuid.MustParse(out["run_id"].(string)), TenantID: w.Tenant}
}

// handshakeConnector has a Meta-style GET check and a Slack-style POST one.
var handshakeManifest = []byte(`
manifest: connector/v1
id: shake
version: 1.0.0
name: Handshake test
description: Endpoint checks.
category: messaging
auth:
  type: api_key
  fields:
    - { key: secret, label: Secret, secret: true }
    - { key: verify_token, label: Verify token, secret: true }
base_url: https://shake.test
egress_hosts: [shake.test]
actions:
  noop: { title: No-op, class: read, input: { type: object, properties: {} }, output: { type: object, properties: {} } }
triggers:
  meta:
    type: webhook
    verify: { scheme: header_secret, header: X-Secret, secret_field: secret }
    handshake: { method: GET, token_query: hub.verify_token, secret_field: verify_token, respond: '=query["hub.challenge"]' }
    event_type: =body.kind
  slack:
    type: webhook
    verify: { scheme: header_secret, header: X-Secret, secret_field: secret }
    handshake: { method: POST, when: '=has(body.type) && body.type == "url_verification"', respond: =body.challenge }
    event_type: =body.type
`)

func TestConnectorHandshakes(t *testing.T) {
	w := newWorld(t)
	c := connector.MustParse(handshakeManifest)
	if err := w.Registry.Register(&connector.Connector{Manifest: c, Actions: map[string]connector.Action{"noop": connector.ActionFunc(func(context.Context, connector.Request) (connector.Response, error) {
		return connector.Response{}, nil
	})}}); err != nil {
		t.Fatal(err)
	}
	if _, err := w.Vault.CreateConnection(ctx, w.Tenant, "prod", "shake", "main", "api_key", map[string]string{"secret": "s1", "verify_token": "vt"}, "test"); err != nil {
		t.Fatal(err)
	}
	get := func(q string) (int, string) {
		resp, err := http.Get(w.srv.URL + "/hooks/" + w.Tenant.String() + "/connectors/shake@1/meta?" + q)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = resp.Body.Close() }()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(b)
	}
	if st, body := get("hub.mode=subscribe&hub.verify_token=vt&hub.challenge=12345"); st != 200 || body != "12345" {
		t.Errorf("GET challenge: %d %q", st, body)
	}
	if st, _ := get("hub.mode=subscribe&hub.verify_token=wrong&hub.challenge=12345"); st != 403 {
		t.Errorf("wrong token: %d", st)
	}

	post := func(body string, secret string) (int, string) {
		req, _ := http.NewRequest("POST", w.srv.URL+"/hooks/"+w.Tenant.String()+"/connectors/shake@1/slack", bytes.NewReader([]byte(body)))
		req.Header.Set("X-Secret", secret)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = resp.Body.Close() }()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(b)
	}
	if st, body := post(`{"type":"url_verification","challenge":"abc"}`, "s1"); st != 200 || body != "abc" {
		t.Errorf("POST challenge: %d %q", st, body)
	}
	if st, _ := post(`{"type":"url_verification","challenge":"abc"}`, "nope"); st != 401 {
		t.Errorf("unverified challenge answered: %d", st)
	}
	if st, body := post(`{"type":"event_callback"}`, "s1"); st != 202 || !bytes.Contains([]byte(body), []byte(`"event":"event_callback"`)) {
		t.Errorf("delivery: %d %s", st, body)
	}

	// Form-encoded deliveries arrive as fields.
	req, _ := http.NewRequest("POST", w.srv.URL+"/hooks/"+w.Tenant.String()+"/connectors/shake@1/slack", bytes.NewReader([]byte("type=delivery&id=7")))
	req.Header.Set("X-Secret", "s1")
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != 202 || !bytes.Contains(b, []byte(`"event":"delivery"`)) {
		t.Errorf("form delivery: %d %s", resp.StatusCode, b)
	}
}
