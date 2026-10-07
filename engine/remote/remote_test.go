package remote_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/connectors/pgdock"
	"github.com/israel-duff/taskiem/connectors/pgdock/pgdocktest"
	"github.com/israel-duff/taskiem/connectors/whatsapp"
	"github.com/israel-duff/taskiem/engine/connector"
	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/ingest"
	"github.com/israel-duff/taskiem/engine/remote"
	rt "github.com/israel-duff/taskiem/engine/runtime/runtimetest"
	"github.com/israel-duff/taskiem/engine/wd"
)

var ctx = context.Background()

const (
	project = "7d2c9a10-5b4e-4f00-9c11-0000000000a1"
	token   = "pgd_taskiem_shop"
)

// graph is a fake WhatsApp Cloud API that records the messages sent.
type graph struct {
	*httptest.Server
	mu   sync.Mutex
	sent []map[string]any
}

func newGraph(t *testing.T) *graph {
	g := &graph{}
	g.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var m map[string]any
		_ = json.NewDecoder(r.Body).Decode(&m)
		g.mu.Lock()
		g.sent = append(g.sent, m)
		n := len(g.sent)
		g.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"messaging_product":"whatsapp","contacts":[{"input":"+2348012345678","wa_id":"2348012345678"}],"messages":[{"id":"wamid.T`+string(rune('0'+n))+`"}]}`)
	}))
	t.Cleanup(g.Close)
	return g
}

func (g *graph) messages() []map[string]any {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]map[string]any(nil), g.sent...)
}

type world struct {
	*rt.Env
	pg    *pgdocktest.Fake
	graph *graph
	edge  *httptest.Server
	down  atomic.Bool // Taskiem's edge is unavailable: 503
	rec   *remote.Reconciler
}

func newWorld(t *testing.T) *world {
	e := rt.New(t)
	w := &world{Env: e, pg: pgdocktest.New(t), graph: newGraph(t)}
	w.pg.AddProject(project, "shop")
	w.pg.AddTable(project, "orders", []string{"id"}, "id", "customer", "phone", "status", "total", "notes")
	w.pg.AddTable(project, "customers", []string{"id"}, "id", "name")
	w.pg.AddToken(pgdocktest.Token{Secret: token, ID: "tok-1", Name: "taskiem", Scopes: []string{"read", "write"}, ProjectIDs: []string{project}})
	for _, c := range []*connector.Connector{pgdock.New(pgdock.Options{BaseURL: w.pg.URL}), whatsapp.New(whatsapp.Options{BaseURL: w.graph.URL})} {
		if err := e.Registry.Register(c); err != nil {
			t.Fatal(err)
		}
	}
	err := db.InTenantTx(ctx, e.DB.App, []uuid.UUID{e.Tenant}, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO environments (tenant_id, name) VALUES ($1, 'dev'), ($1, 'prod')`, e.Tenant)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.Vault.CreateConnection(ctx, e.Tenant, "prod", "pgdock", "shop", "api_key", map[string]string{"token": token, "project_id": project}, "test"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Vault.CreateConnection(ctx, e.Tenant, "prod", "whatsapp", "main", "api_key", map[string]string{"access_token": "EAAtest", "phone_number_id": "106540352242922"}, "test"); err != nil {
		t.Fatal(err)
	}
	h := &ingest.Handler{Store: e.Store, Secrets: e.Vault, Connections: e.Vault, Registry: e.Registry, Egress: e.Egress}
	mux := http.NewServeMux()
	mux.Handle("/hooks/", http.StripPrefix("/hooks", h))
	w.edge = httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		if w.down.Load() {
			rw.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		mux.ServeHTTP(rw, r)
	}))
	t.Cleanup(w.edge.Close)
	w.rec = &remote.Reconciler{Pool: e.DB.App, Vault: e.Vault, Registry: e.Registry, Egress: e.Egress, HooksURL: w.edge.URL + "/hooks"}
	return w
}

func flow(tables, events string, extra string) string {
	return `{"schema":"wd/v1","id":"wf_newOrder","version":1,"name":"new order",
	  "trigger":{"type":"connector_event","config":{"connector":"pgdock@1","trigger":"row_changed","connection":"shop","events":` + events + `,
	    "options":{"tables":` + tables + extra + `}}},
	  "steps":[{"id":"notify","type":"connector","connector":"whatsapp@1","action":"send_text","connection":"main",
	    "input":{"to":"=trigger.body.record.phone","body":"='New order ' + string(trigger.body.record.id) + ' from ' + trigger.body.record.customer"}}]}`
}

// deploy registers a version's trigger in prod as the API does, and
// reconciles.
func (w *world) deploy(t *testing.T, wf uuid.UUID, version int, doc string) {
	t.Helper()
	def, err := wd.Load([]byte(doc))
	if err != nil {
		t.Fatal(err)
	}
	err = db.InTenantTx(ctx, w.DB.App, []uuid.UUID{w.Tenant}, func(tx pgx.Tx) error {
		if version > 1 {
			sum := [32]byte{byte(version)}
			if _, err := tx.Exec(ctx, `INSERT INTO workflow_versions (workflow_id, version, tenant_id, definition, digest, state) VALUES ($1, $2, $3, $4, $5, 'published')`,
				wf, version, w.Tenant, doc, sum[:]); err != nil {
				return err
			}
		}
		return ingest.SyncEnvironments(ctx, tx, w.Tenant, wf, []string{"prod"}, version, def, w.Registry, time.Now())
	})
	if err != nil {
		t.Fatal(err)
	}
}

func (w *world) subs(t *testing.T) []remote.Status {
	t.Helper()
	var out []remote.Status
	err := db.InTenantTx(ctx, w.DB.App, []uuid.UUID{w.Tenant}, func(tx pgx.Tx) error {
		var err error
		out, err = remote.List(ctx, tx, "")
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func (w *world) tick(t *testing.T) {
	t.Helper()
	if _, err := w.rec.Tick(ctx); err != nil {
		t.Fatal(err)
	}
}

func (w *world) runs(t *testing.T, wf uuid.UUID) []map[string]any {
	t.Helper()
	var out []map[string]any
	err := db.InTenantTx(ctx, w.DB.App, []uuid.UUID{w.Tenant}, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT status FROM runs WHERE workflow_id = $1 ORDER BY id`, wf)
		if err != nil {
			return err
		}
		for rows.Next() {
			var st string
			if err := rows.Scan(&st); err != nil {
				return err
			}
			out = append(out, map[string]any{"status": st})
		}
		return rows.Err()
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// events is a workflow's run history, for failure messages.
func (w *world) events(t *testing.T, wf uuid.UUID) string {
	t.Helper()
	var b strings.Builder
	_ = db.InTenantTx(ctx, w.DB.App, []uuid.UUID{w.Tenant}, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT e.type, e.payload::text FROM run_events e JOIN runs r ON r.id = e.run_id WHERE r.workflow_id = $1 ORDER BY e.run_id, e.seq`, wf)
		if err != nil {
			return err
		}
		for rows.Next() {
			var typ, payload string
			if err := rows.Scan(&typ, &payload); err != nil {
				return err
			}
			b.WriteString("\n  " + typ + " " + payload)
		}
		return rows.Err()
	})
	return b.String()
}

func (w *world) only(t *testing.T) (remote.Status, *pgdocktest.Webhook) {
	t.Helper()
	subs := w.subs(t)
	if len(subs) != 1 {
		t.Fatalf("subscriptions %+v", subs)
	}
	s := subs[0]
	if s.RemoteID == nil {
		t.Fatalf("not created: %+v", s)
	}
	return s, w.pg.Hook(project, *s.RemoteID)
}

// The plan's acceptance test (P1-T8, without the write-back, which waits
// for P1-G1): a new orders row starts a workflow that sends a WhatsApp
// message; a rolled-back insert starts nothing; deliveries retried after
// Taskiem was unavailable, and deliveries PGDock repeats, are taken once.
func TestNewRowSendsWhatsAppOnce(t *testing.T) {
	w := newWorld(t)
	doc := flow(`["orders"]`, `["INSERT"]`, "")
	wf := w.Publish(t, doc)
	w.deploy(t, wf, 1, doc)
	if s := w.subs(t); len(s) != 1 || s[0].Health != "pending" || s[0].RemoteID != nil {
		t.Fatalf("declared before any call: %+v", s)
	}
	if len(w.pg.Requests) != 0 {
		t.Fatalf("the deploy called PGDock: %v", w.pg.Requests)
	}
	w.tick(t)
	sub, hook := w.only(t)
	if sub.Health != "ok" || hook == nil || hook.Name != remote.Name(sub.ID) || hook.Events[0] != "INSERT" || len(hook.Events) != 1 {
		t.Fatalf("subscription %+v, webhook %+v", sub, hook)
	}
	u, _ := url.Parse(hook.URL)
	if u.Query().Get("subscription") != sub.ID.String() || u.Query().Get("env") != "prod" || u.Query().Get("connection") != "shop" ||
		!strings.HasSuffix(u.Path, "/connectors/pgdock@1/row_changed") {
		t.Errorf("ingest URL %s", hook.URL)
	}
	if ok, _ := w.Vault.Exists(ctx, w.Tenant, remote.SecretEnv, remote.SecretName(sub.ID)); !ok {
		t.Error("the signing secret is not in the vault")
	}

	// A committed insert: one run, one message.
	w.pg.Begin(project).Insert("orders", map[string]any{"id": 7, "customer": "Ada", "phone": "+2348012345678", "status": "new", "total": 250000}).Commit()
	if n := w.pg.Deliver(); n != 1 {
		t.Fatalf("delivered %d: %+v", n, w.pg.Deliveries)
	}
	if runs := w.runs(t, wf); len(runs) != 1 {
		t.Fatalf("runs %v", runs)
	}
	w.Drain(t)
	msgs := w.graph.messages()
	if len(msgs) != 1 || msgs[0]["to"] != "+2348012345678" || msgs[0]["text"].(map[string]any)["body"] != "New order 7 from Ada" {
		t.Fatalf("messages %v; runs %v; %s", msgs, w.runs(t, wf), w.events(t, wf))
	}
	if runs := w.runs(t, wf); runs[0]["status"] != "completed" {
		t.Errorf("run %v", runs)
	}

	// A rolled-back insert sends nothing and starts nothing.
	w.pg.Begin(project).Insert("orders", map[string]any{"id": 8, "customer": "Bayo", "phone": "+2348000000000"}).Rollback()
	// An update is not subscribed to: PGDock is not asked for it.
	w.pg.Begin(project).Update("orders", map[string]any{"id": 7}, map[string]any{"status": "paid"}).Commit()
	if n := w.pg.Deliver(); n != 0 || len(w.runs(t, wf)) != 1 {
		t.Fatalf("rolled back or unsubscribed change delivered: %d", n)
	}

	// Taskiem is down: PGDock keeps the event and retries.
	w.down.Store(true)
	w.pg.Begin(project).Insert("orders", map[string]any{"id": 9, "customer": "Chi", "phone": "+2348011111111"}).Commit()
	w.pg.Begin(project).Insert("orders", map[string]any{"id": 10, "customer": "Dayo", "phone": "+2348022222222"}).Commit()
	for range 3 {
		if n := w.pg.Deliver(); n != 0 {
			t.Fatalf("delivered while down: %d", n)
		}
	}
	if q := w.pg.Queued(project, hook.ID); q != 2 {
		t.Fatalf("queued while down: %d", q)
	}
	w.down.Store(false)
	if n := w.pg.Deliver(); n != 2 {
		t.Fatalf("delivered after recovery: %d", n)
	}
	// PGDock delivers at least once: a repeat is acknowledged, not run again.
	last := w.pg.Hook(project, hook.ID).Delivered
	for _, ev := range last {
		st, err := w.pg.Redeliver(project, hook.ID, ev.ID)
		if err != nil || st != http.StatusAccepted {
			t.Fatalf("redelivery: %d %v", st, err)
		}
	}
	w.Drain(t)
	if runs := w.runs(t, wf); len(runs) != 3 {
		t.Fatalf("runs after retries and repeats: %d", len(runs))
	}
	if msgs := w.graph.messages(); len(msgs) != 3 || msgs[1]["to"] != "+2348011111111" || msgs[2]["to"] != "+2348022222222" {
		t.Errorf("messages %v", msgs)
	}
}

func post(t *testing.T, target string, body []byte, hdr http.Header) int {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, target, bytes.NewReader(body))
	req.Header = hdr
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	return resp.StatusCode
}

func TestDeliveryChecks(t *testing.T) {
	w := newWorld(t)
	doc := flow(`["orders"]`, `["INSERT"]`, "")
	wf := w.Publish(t, doc)
	w.deploy(t, wf, 1, doc)
	w.tick(t)
	sub, hook := w.only(t)
	body := []byte(`{"id":"evt_x_1","webhook":"` + hook.Name + `","table":"public.orders","type":"INSERT","record":{"id":1}}`)
	signed := func(secret string) http.Header {
		h := http.Header{}
		h.Set("Content-Type", "application/json")
		h.Set("PGDock-Event-Id", "evt_x_1")
		h.Set("PGDock-Signature", pgdocktest.Sign(time.Now(), body, secret))
		return h
	}
	if st := post(t, hook.URL, body, signed("whsec_wrong")); st != http.StatusUnauthorized {
		t.Errorf("wrong secret: %d", st)
	}
	stale := signed(hook.Secret)
	stale.Set("PGDock-Signature", pgdocktest.Sign(time.Now().Add(-6*time.Minute), body, hook.Secret))
	if st := post(t, hook.URL, body, stale); st != http.StatusUnauthorized {
		t.Errorf("replayed outside the window: %d", st)
	}
	// Rotation overlap (plan Q22): a header with the old and new signature.
	both := signed(hook.Secret)
	both.Set("PGDock-Signature", pgdocktest.Sign(time.Now(), body, "whsec_previous", hook.Secret))
	if st := post(t, hook.URL, body, both); st != http.StatusAccepted {
		t.Errorf("two v1 values: %d", st)
	}
	other := strings.Replace(hook.URL, sub.ID.String(), uuid.NewString(), 1)
	if st := post(t, other, body, signed(hook.Secret)); st != http.StatusNotFound {
		t.Errorf("unknown subscription: %d", st)
	}
	none := strings.Replace(hook.URL, "subscription="+sub.ID.String(), "", 1)
	if st := post(t, none, body, signed(hook.Secret)); st != http.StatusNotFound {
		t.Errorf("no subscription: %d", st)
	}
	dev := strings.Replace(hook.URL, "env=prod", "env=dev", 1)
	if st := post(t, dev, body, signed(hook.Secret)); st != http.StatusNotFound {
		t.Errorf("another environment: %d", st)
	}
	// Undeployed: the subscription is going, and its deliveries are gone for good.
	err := db.InTenantTx(ctx, w.DB.App, []uuid.UUID{w.Tenant}, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `DELETE FROM triggers WHERE workflow_id = $1`, wf); err != nil {
			return err
		}
		return remote.Release(ctx, tx, wf, []string{"prod"})
	})
	if err != nil {
		t.Fatal(err)
	}
	if st := post(t, hook.URL, body, signed(hook.Secret)); st != http.StatusGone {
		t.Errorf("released subscription: %d", st)
	}
}

func TestTestEventsAndTruncatedRows(t *testing.T) {
	w := newWorld(t)
	doc := flow(`["orders"]`, `["INSERT","UPDATE"]`, `,"refetch_truncated":true`)
	wf := w.Publish(t, doc)
	w.deploy(t, wf, 1, doc)
	w.tick(t)
	sub, hook := w.only(t)

	// PGDock's "Send test event": acknowledged, recorded, no run.
	req, _ := http.NewRequest(http.MethodPost, w.pg.URL+"/api/v1/projects/"+project+"/webhooks/"+hook.ID+"/test", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	var res map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&res)
	_ = resp.Body.Close()
	if res["ok"] != true {
		t.Fatalf("test event: %v", res)
	}
	if len(w.runs(t, wf)) != 0 {
		t.Error("a test event started a run")
	}
	if s, _ := w.only(t); s.LastTestAt == nil || s.ID != sub.ID {
		t.Errorf("test not recorded: %+v", s)
	}

	// A change over the size limit arrives truncated; the trigger asked for
	// the row, which is fetched before the run starts.
	w.pg.TruncateAbove = 300
	w.pg.Begin(project).Insert("orders", map[string]any{"id": 11, "customer": "Efe", "phone": "+2348033333333", "notes": strings.Repeat("x", 400)}).Commit()
	if n := w.pg.Deliver(); n != 1 {
		t.Fatalf("delivered %d: %+v", n, w.pg.Deliveries)
	}
	if !strings.Contains(string(w.pg.Deliveries[len(w.pg.Deliveries)-1].Body), `"truncated":true`) {
		t.Fatal("the fake did not truncate")
	}
	w.Drain(t)
	msgs := w.graph.messages()
	if len(msgs) != 1 || msgs[0]["to"] != "+2348033333333" {
		t.Fatalf("messages after a refetched truncated event: %v", msgs)
	}
	// PGDock busy while refetching: the delivery is refused and retried.
	w.pg.Fail(pgdocktest.Failure{Method: http.MethodGet, PathPrefix: "/api/v1/projects/" + project + "/tables/", Status: 503})
	w.pg.Begin(project).Update("orders", map[string]any{"id": 11}, map[string]any{"status": "paid"}).Commit()
	if n := w.pg.Deliver(); n != 0 {
		t.Fatalf("delivered though the row could not be fetched: %d", n)
	}
	if n := w.pg.Deliver(); n != 1 {
		t.Fatalf("retry: %d", n)
	}
	if runs := w.runs(t, wf); len(runs) != 2 {
		t.Errorf("runs %d", len(runs))
	}
}

func TestRemoteLifecycle(t *testing.T) {
	w := newWorld(t)
	w.rec.Check = time.Nanosecond // health-check on every pass
	doc := flow(`["orders"]`, `["INSERT"]`, "")
	wf := w.Publish(t, doc)
	w.deploy(t, wf, 1, doc)

	// Taskiem crashed after PGDock created the webhook but before it
	// recorded it: the next pass finds it by name and adopts it, with a new
	// secret, rather than creating a second one.
	sub := w.subs(t)[0]
	reg := w.Registry
	c, _ := reg.Get("pgdock@1")
	rc := connector.RemoteCall{Credentials: map[string]string{"token": token, "project_id": project}, HTTP: http.DefaultClient}
	orphan, err := c.Registrars["row_changed"].Create(ctx, rc, connector.RemoteSpec{Name: remote.Name(sub.ID), URL: "https://stale.example/hooks", Options: map[string]any{"tables": []any{"customers"}}})
	if err != nil {
		t.Fatal(err)
	}
	w.tick(t)
	if hooks := w.pg.Webhooks(project); len(hooks) != 1 || hooks[0].ID != orphan.ID || hooks[0].Secret == orphan.Secret || hooks[0].Tables[0] != "orders" {
		t.Fatalf("adoption: %+v", hooks)
	}
	s, hook := w.only(t)
	if s.Health != "ok" || *s.RemoteID != orphan.ID {
		t.Fatalf("adopted %+v", s)
	}
	w.pg.Begin(project).Insert("orders", map[string]any{"id": 1, "phone": "+2348044444444"}).Commit()
	if w.pg.Deliver() != 1 {
		t.Fatal("the adopted webhook's deliveries do not verify")
	}

	// A redeploy changes the webhook in place.
	doc2 := flow(`["orders","customers"]`, `["INSERT","DELETE"]`, `,"columns":["status"]`)
	w.deploy(t, wf, 2, doc2)
	w.tick(t)
	h := w.pg.Hook(project, hook.ID)
	if len(w.pg.Webhooks(project)) != 1 || len(h.Tables) != 2 || len(h.Events) != 2 || h.Columns[0] != "status" {
		t.Fatalf("updated %+v", h)
	}

	// Drift at PGDock shows on the subscription: broken, paused, missing.
	w.pg.SetStatus(project, hook.ID, "broken", "the triggers on public.orders were dropped")
	w.tick(t)
	if s, _ := w.only(t); s.Health != "broken" || s.StatusReason == nil || !strings.Contains(*s.StatusReason, "dropped") {
		t.Errorf("broken: %+v", s)
	}
	// Republishing repairs it.
	repair(t, w, wf)
	if s, _ := w.only(t); s.Health != "ok" || w.pg.Hook(project, hook.ID).Status != "healthy" {
		t.Errorf("repaired: %+v", s)
	}
	w.pg.SetStatus(project, hook.ID, "paused", "50 failures in a row")
	w.tick(t)
	if s, _ := w.only(t); s.Health != "paused" {
		t.Errorf("paused: %+v", s)
	}
	repair(t, w, wf)
	if h := w.pg.Hook(project, hook.ID); !h.Enabled || h.Status != "healthy" {
		t.Errorf("resumed: %+v", h)
	}
	w.pg.RemoveWebhook(project, hook.ID)
	w.tick(t)
	if s, _ := w.only(t); s.Health != "missing" {
		t.Errorf("missing: %+v", s)
	}
	repair(t, w, wf)
	s, hook2 := w.only(t)
	if s.Health != "ok" || hook2 == nil || hook2.ID == hook.ID {
		t.Fatalf("recreated: %+v %+v", s, hook2)
	}

	// PGDock refuses (a token without write): the subscription shows the
	// error and is retried later, not dropped.
	w.pg.Fail(pgdocktest.Failure{Method: http.MethodPatch, PathPrefix: "/api/v1/projects/", Status: 403, Body: map[string]any{"code": "forbidden", "message": "the token lacks the write scope"}})
	repair(t, w, wf)
	if s, _ := w.only(t); s.Health != "failed" || s.LastError == nil || !strings.Contains(*s.LastError, "write scope") || s.NextAttempt == nil {
		t.Errorf("refused: %+v", s)
	}

	// Taken out of the environment: the webhook, its secret and the row go.
	err = db.InTenantTx(ctx, w.DB.App, []uuid.UUID{w.Tenant}, func(tx pgx.Tx) error {
		return remote.Release(ctx, tx, wf, []string{"prod"})
	})
	if err != nil {
		t.Fatal(err)
	}
	if s := w.subs(t); len(s) != 1 || s[0].Health != "removing" {
		t.Fatalf("releasing %+v", s)
	}
	w.tick(t)
	if hooks := w.pg.Webhooks(project); len(hooks) != 0 {
		t.Errorf("left at PGDock: %+v", hooks)
	}
	if s := w.subs(t); len(s) != 0 {
		t.Errorf("rows left %+v", s)
	}
	if ok, _ := w.Vault.Exists(ctx, w.Tenant, remote.SecretEnv, remote.SecretName(s.ID)); ok {
		t.Error("the secret was kept")
	}
	var actions []string
	err = db.InTenantTx(ctx, w.DB.App, []uuid.UUID{w.Tenant}, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT action FROM audit_log WHERE action LIKE 'remote_subscription.%' ORDER BY chain_seq`)
		if err != nil {
			return err
		}
		actions, err = pgx.CollectRows(rows, pgx.RowTo[string])
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(actions, ",") == "" || actions[0] != "remote_subscription.adopt" || actions[len(actions)-1] != "remote_subscription.delete" {
		t.Errorf("audit %v", actions)
	}
}

func repair(t *testing.T, w *world, wf uuid.UUID) {
	t.Helper()
	err := db.InTenantTx(ctx, w.DB.App, []uuid.UUID{w.Tenant}, func(tx pgx.Tx) error { return remote.Repair(ctx, tx, wf) })
	if err != nil {
		t.Fatal(err)
	}
	w.tick(t)
}

// A subscription released before Taskiem recorded the webhook it created
// is still deleted: found by name.
func TestReleaseBeforeRecordLeavesNothing(t *testing.T) {
	w := newWorld(t)
	doc := flow(`["orders"]`, `["INSERT"]`, "")
	wf := w.Publish(t, doc)
	w.deploy(t, wf, 1, doc)
	sub := w.subs(t)[0]
	c, _ := w.Registry.Get("pgdock@1")
	rc := connector.RemoteCall{Credentials: map[string]string{"token": token, "project_id": project}, HTTP: http.DefaultClient}
	if _, err := c.Registrars["row_changed"].Create(ctx, rc, connector.RemoteSpec{Name: remote.Name(sub.ID), URL: "https://h.example", Options: map[string]any{"tables": []any{"orders"}}}); err != nil {
		t.Fatal(err)
	}
	// Redeployed with a manual trigger: the remote one is released.
	manual := `{"schema":"wd/v1","id":"wf_newOrder","version":2,"name":"new order","trigger":{"type":"manual"},"steps":[{"id":"x","type":"transform","config":{"output":1}}]}`
	w.deploy(t, wf, 2, manual)
	w.tick(t)
	if hooks := w.pg.Webhooks(project); len(hooks) != 0 {
		t.Errorf("orphan left: %+v", hooks)
	}
	if s := w.subs(t); len(s) != 0 {
		t.Errorf("rows %+v", s)
	}
}

func TestOptionsAreChecked(t *testing.T) {
	w := newWorld(t)
	for doc, want := range map[string]string{
		flow(`[]`, `["INSERT"]`, ""):                        "tables",
		flow(`["orders; drop table x"]`, `["INSERT"]`, ""):  "tables",
		flow(`["orders"]`, `["INSERT"]`, `,"unknown":true`): "unknown",
		flow(`["orders"]`, `["UPSERT"]`, ""):                "does not send",
		strings.Replace(flow(`["orders"]`, `["INSERT"]`, ""), `"options":{"tables":["orders"]}`, `"options":{}`, 1): "tables",
	} {
		def, err := wd.Load([]byte(doc))
		if err != nil {
			t.Fatal(err)
		}
		if err := ingest.Check(def, w.Registry); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("want an error about %q, got %v", want, err)
		}
	}
}
