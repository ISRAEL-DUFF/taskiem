package ingest_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/connector"
	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/ingest"
	"github.com/israel-duff/taskiem/engine/secrets"
	"github.com/israel-duff/taskiem/engine/wd"
)

// A provider whose app sends every business's events to one URL, naming
// the workspace each is for (Slack's shape), with an endpoint check and an
// uninstall event.
const workspaceManifest = `manifest: connector/v1
id: workspace
version: 1.0.0
name: Workspace
category: productivity
auth:
  type: oauth2
  fields: [{ key: bot_token, label: Bot token, secret: true }, { key: team_id, label: Workspace }]
  oauth2:
    authorize_url: https://workspace.example/oauth/authorize
    token_url: https://workspace.example/oauth/token
    scopes: [chat]
    pkce: true
    client: workspaceapp
    account_field: team_id
    fields_from_token: { bot_token: access_token, team_id: team_id }
base_url: https://workspace.example/api
actions:
  ping: { title: Ping, class: read, input: { type: object } }
triggers:
  events:
    type: webhook
    delivery: app
    verify: { scheme: hmac_sha256, header: X-Workspace-Signature }
    event_type: =body.type
    dedup: =body.event_id
    account_key: =body.team_id
    revokes: "=body.type == 'app_uninstalled'"
    handshake: { method: GET, token_query: hub.verify_token, respond: "=query['hub.challenge']" }
`

const appSecret = "app-signing-secret"

type appWorld struct{ *world }

func newAppWorld(t *testing.T) appWorld {
	w := newWorld(t)
	m := connector.MustParse([]byte(workspaceManifest))
	ping := connector.ActionFunc(func(context.Context, connector.Request) (connector.Response, error) { return connector.Response{}, nil })
	if err := w.Registry.Register(&connector.Connector{Manifest: m, Actions: map[string]connector.Action{"ping": ping}}); err != nil {
		t.Fatal(err)
	}
	w.h.Apps = map[string]ingest.App{"workspaceapp": {WebhookSecret: appSecret, VerifyToken: "verify-me"}}
	return appWorld{w}
}

// connect makes a connection as the OAuth callback does through Taskiem's
// app: the credentials, and the workspace it is for.
func (w appWorld) connect(t *testing.T, tenant uuid.UUID, env, name, team string) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	if err := db.InTenantTx(ctx, w.DB.App, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		var err error
		if id, err = w.Vault.CreateOAuthConnectionTx(ctx, tx, tenant, env, "workspace", name, map[string]string{"bot_token": "xoxb-" + team, "team_id": team}, nil, "test"); err != nil {
			return err
		}
		return secrets.SetAppAccountTx(ctx, tx, tenant, id, "workspaceapp", team)
	}); err != nil {
		t.Fatal(err)
	}
	return id
}

func (w appWorld) subscribe(t *testing.T, tenant uuid.UUID) uuid.UUID {
	t.Helper()
	doc := `{"schema":"wd/v1","id":"wf_ws","version":1,"name":"ws","trigger":{"type":"connector_event","config":{"connector":"workspace@1","trigger":"events"}},
	  "steps":[{"id":"a","type":"transform","config":{"output":"=trigger.body.text"}}]}`
	wf := w.PublishIn(t, tenant, doc)
	def, err := wd.Load([]byte(doc))
	if err != nil {
		t.Fatal(err)
	}
	if err := db.InTenantTx(ctx, w.DB.App, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		return ingest.Sync(ctx, tx, tenant, wf, 1, def, w.Registry, time.Now())
	}); err != nil {
		t.Fatal(err)
	}
	return wf
}

func (w appWorld) send(t *testing.T, secret string, ev map[string]any) (int, map[string]any) {
	t.Helper()
	body, _ := json.Marshal(ev)
	req, _ := http.NewRequest("POST", w.srv.URL+"/hooks/apps/workspace@1/events", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Workspace-Signature", "sha256="+sign(sha256.New, secret, body))
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

func (w appWorld) runsIn(t *testing.T, tenant, wf uuid.UUID) int {
	t.Helper()
	var n int
	if err := db.InTenantTx(ctx, w.DB.App, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT count(*) FROM runs WHERE workflow_id = $1`, wf).Scan(&n)
	}); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestAppEventsReachOnlyTheirAccountsConnections(t *testing.T) {
	w := newAppWorld(t)
	acme, bolt, both := w.Tenant, w.AddTenant(t), w.AddTenant(t)
	for _, tn := range []uuid.UUID{bolt, both} {
		if err := db.InTenantTx(ctx, w.DB.App, []uuid.UUID{tn}, func(tx pgx.Tx) error {
			_, err := tx.Exec(ctx, `INSERT INTO environments (tenant_id, name) VALUES ($1, 'prod') ON CONFLICT DO NOTHING`, tn)
			return err
		}); err != nil {
			t.Fatal(err)
		}
	}
	acmeConn := w.connect(t, acme, "prod", "main", "T-ACME")
	w.connect(t, bolt, "prod", "main", "T-BOLT")
	// One workspace connected by two businesses: both receive its events.
	w.connect(t, both, "prod", "main", "T-BOLT")
	acmeWF, boltWF, bothWF := w.subscribe(t, acme), w.subscribe(t, bolt), w.subscribe(t, both)

	st, out := w.send(t, appSecret, map[string]any{"type": "message", "event_id": "E1", "team_id": "T-ACME", "text": "hello acme"})
	if st != 200 || out["routed"] != float64(1) {
		t.Fatalf("acme's event: %d %v", st, out)
	}
	st, out = w.send(t, appSecret, map[string]any{"type": "message", "event_id": "E2", "team_id": "T-BOLT", "text": "hello bolt"})
	if st != 200 || out["routed"] != float64(2) {
		t.Fatalf("bolt's event: %d %v", st, out)
	}
	// A retry of the same event starts nothing new.
	w.send(t, appSecret, map[string]any{"type": "message", "event_id": "E1", "team_id": "T-ACME", "text": "hello acme"})
	if a, b, c := w.runsIn(t, acme, acmeWF), w.runsIn(t, bolt, boltWF), w.runsIn(t, both, bothWF); a != 1 || b != 1 || c != 1 {
		t.Errorf("runs: acme %d, bolt %d, both %d; want 1 each", a, b, c)
	}

	// An account nobody connected: acknowledged, counted, nothing stored.
	st, out = w.send(t, appSecret, map[string]any{"type": "message", "event_id": "E3", "team_id": "T-NOBODY", "text": "lost"})
	if st != 200 || out["routed"] != float64(0) {
		t.Errorf("an unknown account: %d %v", st, out)
	}
	var drops int
	if err := w.DB.Admin.QueryRow(ctx, `SELECT COALESCE(sum(hits), 0) FROM app_event_drops WHERE connector = 'workspace@1'`).Scan(&drops); err != nil || drops != 1 {
		t.Errorf("drops counted: %d %v", drops, err)
	}

	// A wrong signature, or the tenant's own secret: refused.
	if st, _ := w.send(t, "not-the-app", map[string]any{"type": "message", "event_id": "E4", "team_id": "T-ACME"}); st != 401 {
		t.Errorf("a wrong signature: %d", st)
	}

	// Uninstalled: acme's connection expires, and later events reach no one.
	if st, out := w.send(t, appSecret, map[string]any{"type": "app_uninstalled", "event_id": "E5", "team_id": "T-ACME"}); st != 200 {
		t.Fatalf("uninstall: %d %v", st, out)
	}
	var status string
	if err := w.DB.Admin.QueryRow(ctx, `SELECT status FROM connections WHERE id = $1`, acmeConn).Scan(&status); err != nil || status != "expired" {
		t.Errorf("after uninstall: %q %v", status, err)
	}
	if _, out := w.send(t, appSecret, map[string]any{"type": "message", "event_id": "E6", "team_id": "T-ACME", "text": "again"}); out["routed"] != float64(0) {
		t.Errorf("after uninstall, routed to %v", out["routed"])
	}
}

func TestAppHandshakeAndUnknownApps(t *testing.T) {
	w := newAppWorld(t)
	get := func(path string) (int, string) {
		resp, err := http.Get(w.srv.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = resp.Body.Close() }()
		raw, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(raw)
	}
	if st, body := get("/hooks/apps/workspace@1/events?hub.verify_token=verify-me&hub.challenge=42"); st != 200 || body != "42" {
		t.Errorf("the endpoint check: %d %q", st, body)
	}
	if st, _ := get("/hooks/apps/workspace@1/events?hub.verify_token=wrong&hub.challenge=42"); st != 403 {
		t.Errorf("a wrong verify token: %d", st)
	}
	if st, _ := get("/hooks/apps/paystack@1/charge_event"); st != 404 {
		t.Errorf("a trigger not delivered to an app: %d", st)
	}
	// No secret configured for the app: its URL takes nothing.
	w.h.Apps = nil
	if st, _ := w.send(t, appSecret, map[string]any{"type": "message", "event_id": "E1", "team_id": "T"}); st != 404 {
		t.Errorf("an app with no secret: %d", st)
	}
}

func TestAppTriggerRules(t *testing.T) {
	for name, c := range map[string]struct{ from, to, want string }{
		"no account field": {"    account_field: team_id\n", "", "needs auth.oauth2.account_field"},
		"no account key":   {"    account_key: =body.team_id\n", "", "needs account_key"},
		"query secret":     {"verify: { scheme: hmac_sha256, header: X-Workspace-Signature }", "verify: { scheme: query_secret, query: token, secret_field: bot_token }", "verified with the app's secret"},
	} {
		src := bytes.Replace([]byte(workspaceManifest), []byte(c.from), []byte(c.to), 1)
		_, probs := connector.Parse(src)
		joined := ""
		for _, p := range probs {
			joined += p + "; "
		}
		if !bytes.Contains([]byte(joined), []byte(c.want)) {
			t.Errorf("%s: %v, want %q", name, probs, c.want)
		}
	}
}
