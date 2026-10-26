package ingest_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/connectors/mailchimp"
	"github.com/israel-duff/taskiem/connectors/meta"
	"github.com/israel-duff/taskiem/engine/connector"
	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/ingest"
	"github.com/israel-duff/taskiem/engine/runtime"
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

// A mode with its own app (QuickBooks' development app for sandbox
// companies; connector/v1 rule 20) sends to the same URL, signed with its
// own secret: either app's deliveries are taken, nobody else's.
func TestAppDeliveriesFromAModesOwnApp(t *testing.T) {
	w := newWorld(t)
	src := strings.Replace(workspaceManifest, "  type: oauth2\n", `  type: oauth2
  modes:
    - { mode: live, label: Live, base_url: https://workspace.example/api }
    - { mode: test, label: Test, base_url: https://sandbox.workspace.example/api, client: workspacesandbox }
`, 1)
	m := connector.MustParse([]byte(src))
	ping := connector.ActionFunc(func(context.Context, connector.Request) (connector.Response, error) { return connector.Response{}, nil })
	if err := w.Registry.Register(&connector.Connector{Manifest: m, Actions: map[string]connector.Action{"ping": ping}}); err != nil {
		t.Fatal(err)
	}
	w.h.Apps = map[string]ingest.App{"workspaceapp": {WebhookSecret: appSecret}, "workspacesandbox": {WebhookSecret: "sandbox-secret", VerifyToken: "sandbox-verify"}}
	aw := appWorld{w}
	aw.connect(t, w.Tenant, "prod", "main", "T-SANDBOX")
	wf := aw.subscribe(t, w.Tenant)
	for i, secret := range []string{appSecret, "sandbox-secret"} {
		if st, out := aw.send(t, secret, map[string]any{"type": "message", "event_id": fmt.Sprint("E", i), "team_id": "T-SANDBOX"}); st != 200 || out["routed"] != float64(1) {
			t.Fatalf("signed by app %d: %d %v", i, st, out)
		}
	}
	if st, _ := aw.send(t, "neither", map[string]any{"type": "message", "event_id": "E9", "team_id": "T-SANDBOX"}); st != 401 {
		t.Errorf("signed by neither: %d", st)
	}
	if n := aw.runsIn(t, w.Tenant, wf); n != 2 {
		t.Errorf("%d runs, want 2", n)
	}
	resp, err := http.Get(w.srv.URL + "/hooks/apps/workspace@1/events?hub.verify_token=sandbox-verify&hub.challenge=7")
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Errorf("the sandbox app's endpoint check: %d", resp.StatusCode)
	}
}

// A Facebook Page's message through Taskiem's Meta app (K5) starts its
// business's workflow with the sender and the text sealed in the run's
// history (connector/v1 rule 21), and reaches no other business.
func TestMessengerMessagesAreSealed(t *testing.T) {
	w := newWorld(t)
	if err := w.Registry.Register(meta.Messenger(meta.Options{})); err != nil {
		t.Fatal(err)
	}
	w.h.Apps = map[string]ingest.App{"meta": {WebhookSecret: "app-secret", VerifyToken: "verify-me"}}
	other := w.AddTenant(t)
	connect := func(tenant uuid.UUID, page string) {
		if err := db.InTenantTx(ctx, w.DB.App, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
			if _, err := tx.Exec(ctx, `INSERT INTO environments (tenant_id, name) VALUES ($1, 'prod') ON CONFLICT DO NOTHING`, tenant); err != nil {
				return err
			}
			id, err := w.Vault.CreateOAuthConnectionTx(ctx, tx, tenant, "prod", "messenger", "main", map[string]string{"page_id": page, "page_token": "EAA" + page}, nil, "test")
			if err != nil {
				return err
			}
			return secrets.SetAppAccountTx(ctx, tx, tenant, id, "meta", page)
		}); err != nil {
			t.Fatal(err)
		}
	}
	connect(w.Tenant, "104123456789012")
	connect(other, "204123456789012")
	doc := `{"schema":"wd/v1","id":"wf_dm","version":1,"name":"dm","trigger":{"type":"connector_event","config":{"connector":"messenger@1","trigger":"messages","events":["message"]}},
	  "steps":[{"id":"a","type":"transform","config":{"output":"=size(trigger.item.event.message.text)"}}]}`
	subscribe := func(tenant uuid.UUID) uuid.UUID {
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
	mine, theirs := subscribe(w.Tenant), subscribe(other)
	body := []byte(`{"object":"page","entry":[{"id":"104123456789012","time":1761480000000,"messaging":[{"sender":{"id":"6789012345678901"},"recipient":{"id":"104123456789012"},"timestamp":1761480000000,"message":{"mid":"m_1","text":"My address is 12 Admiralty Way"}}]}]}`)
	req, _ := http.NewRequest("POST", w.srv.URL+"/hooks/apps/messenger@1/messages", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Hub-Signature-256", "sha256="+sign(sha256.New, "app-secret", body))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("delivery: %d", resp.StatusCode)
	}
	aw := appWorld{w}
	if a, b := aw.runsIn(t, w.Tenant, mine), aw.runsIn(t, other, theirs); a != 1 || b != 0 {
		t.Fatalf("runs: mine %d, theirs %d", a, b)
	}
	var run uuid.UUID
	if err := db.InTenantTx(ctx, w.DB.App, []uuid.UUID{w.Tenant}, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT id FROM runs WHERE workflow_id = $1`, mine).Scan(&run)
	}); err != nil {
		t.Fatal(err)
	}
	evs, err := w.Store.RunHistory(ctx, runtime.RunRef{TenantID: w.Tenant, ID: run})
	if err != nil || len(evs) == 0 {
		t.Fatal(err)
	}
	raw := string(evs[0].Payload)
	if strings.Contains(raw, "Admiralty") || strings.Contains(raw, "6789012345678901") || !strings.Contains(raw, `"$pii"`) {
		t.Errorf("the message is in history in the clear: %s", raw)
	}
}

// Mailchimp checks an address with a plain GET before it saves a webhook
// (K8): an open handshake answers 200, reading and starting nothing; a
// trigger without one still refuses.
func TestOpenHandshake(t *testing.T) {
	w := newWorld(t)
	if err := w.Registry.Register(mailchimp.New(mailchimp.Options{})); err != nil {
		t.Fatal(err)
	}
	get := func(path string) int {
		resp, err := http.Get(w.srv.URL + "/hooks/" + w.Tenant.String() + path)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		return resp.StatusCode
	}
	if st := get("/connectors/mailchimp@1/audience_events?env=prod&subscription=1"); st != 200 {
		t.Errorf("Mailchimp's check: %d", st)
	}
	if st := get("/connectors/workspace@1/events?env=prod"); st != 404 {
		t.Errorf("a trigger with no GET check: %d", st)
	}
}
