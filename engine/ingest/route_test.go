package ingest_test

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/connectors/iswallet"
	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/ingest"
	"github.com/israel-duff/taskiem/engine/runtime"
	"github.com/israel-duff/taskiem/engine/secrets"
	"github.com/israel-duff/taskiem/engine/wd"
)

// wagelyWorld is a partner (the world's tenant) holding iswallet's one
// subscription for its client, with sub-tenants per customer company.
type wagelyWorld struct {
	*world
}

func (w wagelyWorld) partner(t *testing.T, tenant uuid.UUID) {
	t.Helper()
	if err := db.InTenantTx(ctx, w.DB.App, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `SELECT taskiem_set_partner($1, true, 0, 0, 0, 'cli:test')`, tenant)
		return err
	}); err != nil {
		t.Fatal(err)
	}
}

func (w wagelyWorld) subTenant(t *testing.T, parent uuid.UUID) uuid.UUID {
	t.Helper()
	sub := w.DB.SeedTenant(t, &parent).ID
	if err := db.InTenantTx(ctx, w.DB.App, []uuid.UUID{sub}, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO environments (tenant_id, name) VALUES ($1, 'prod')`, sub)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	return sub
}

// connect stores an iswallet connection as the API does: credentials in
// the vault, its wallets indexed for routing.
func (w wagelyWorld) connect(t *testing.T, tenant uuid.UUID, creds map[string]string) {
	t.Helper()
	m := iswallet.New(iswallet.Options{}).Manifest
	if err := db.InTenantTx(ctx, w.DB.App, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		id, err := w.Vault.CreateConnectionTx(ctx, tx, tenant, "prod", "iswallet", "main", "api_key", creds, "test", "")
		if err != nil {
			return err
		}
		return secrets.SetRouteKeysTx(ctx, tx, tenant, id, m.RouteKeys(creds))
	}); err != nil {
		t.Fatal(err)
	}
}

// subscribe publishes a workflow started by an iswallet event in tenant.
func (w wagelyWorld) subscribe(t *testing.T, tenant uuid.UUID, doc string) uuid.UUID {
	t.Helper()
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

// wait starts a run in tenant waiting for a payout's outcome.
func (w wagelyWorld) wait(t *testing.T, tenant, wf uuid.UUID, outflow string) runtime.RunRef {
	t.Helper()
	ref, _, err := w.Store.StartRun(ctx, runtime.StartRequest{TenantID: tenant, WorkflowID: wf, Version: 1, Environment: "prod",
		Trigger: map[string]any{"body": map[string]any{"outflow_id": outflow}}})
	if err != nil {
		t.Fatal(err)
	}
	return ref
}

// deliver posts an iswallet event, signed with the subscription's secret,
// to tenant's URL.
func (w wagelyWorld) deliver(t *testing.T, tenant uuid.UUID, secret, trigger string, ev map[string]any) (int, map[string]any) {
	t.Helper()
	body, _ := json.Marshal(ev)
	ts := strconv.FormatInt(time.Now().Unix(), 10)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(ts + "." + string(body)))
	req, _ := http.NewRequest("POST", w.srv.URL+"/hooks/"+tenant.String()+"/connectors/iswallet@1/"+trigger+"?connection=main", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-iSpend-Timestamp", ts)
	req.Header.Set("X-iSpend-Signature", "sha256="+hex.EncodeToString(mac.Sum(nil)))
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

func outflow(id, typ, wallet, outflowID string) map[string]any {
	return map[string]any{"id": id, "event_type": typ, "schema_version": "v1", "wallet_id": wallet,
		"idempotency_key": typ + ":txn:" + wallet + ":sub_1",
		"data":            map[string]any{"outflow_id": outflowID, "wallet_id": wallet, "amount": 4500000, "fee": 5350, "currency": "NGN", "idempotency_key": "tsk_x"}}
}

func (w wagelyWorld) count(t *testing.T, q string, args ...any) int {
	t.Helper()
	var n int
	if err := w.DB.Admin.QueryRow(ctx, q, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

const settleIswallet = `{"schema":"wd/v1","id":"wf_settle","version":1,"name":"settle","trigger":{"type":"manual"},
  "steps":[
    {"id":"wait","type":"signal","config":{"event":"iswallet@1:outflow_event","correlation":"=trigger.body.outflow_id","timeout":"1h"}},
    {"id":"out","type":"transform","needs":["wait"],"config":{"output":"=steps.wait.output.event"}}]}`

const failedPayouts = `{"schema":"wd/v1","id":"wf_failed","version":1,"name":"failed payouts",
  "trigger":{"type":"connector_event","config":{"connector":"iswallet@1","trigger":"outflow_event","events":["wallet.outflow.failed"]}},
  "steps":[{"id":"a","type":"transform","config":{"output":"=trigger.body.wallet_id"}}]}`

// TestPartnerRoutesProviderEvents: one iswallet subscription at the
// partner; each payout event reaches only the sub-tenant whose connection
// lists the event's wallet, as if delivered at its own URL; a replay does
// nothing twice; an unknown wallet is kept and alerted, the partner's own
// wallets are not; another partner's sub-tenants are never reached.
func TestPartnerRoutesProviderEvents(t *testing.T) {
	w := wagelyWorld{newWorld(t)}
	if err := w.Registry.Register(iswallet.New(iswallet.Options{})); err != nil {
		t.Fatal(err)
	}
	wagely := w.Tenant
	w.partner(t, wagely)
	acme, bolt, idle := w.subTenant(t, wagely), w.subTenant(t, wagely), w.subTenant(t, wagely)
	other := w.DB.SeedTenant(t, nil).ID // another partner, also an iswallet client
	w.partner(t, other)
	otherSub := w.subTenant(t, other)

	w.connect(t, wagely, map[string]string{"api_key": "isw_wagely", "webhook_secret": "whsec_wagely", "wallet_ids": "w_funding"})
	w.connect(t, acme, map[string]string{"api_key": "isw_wagely", "shared_client": "true", "wallet_ids": "w_acme, w_acme2"})
	w.connect(t, bolt, map[string]string{"api_key": "isw_wagely", "shared_client": "true", "wallet_ids": "w_bolt"})
	w.connect(t, idle, map[string]string{"api_key": "isw_wagely", "shared_client": "true", "wallet_ids": "w_idle"})
	w.connect(t, other, map[string]string{"api_key": "isw_other", "webhook_secret": "whsec_other"})
	// The other partner's customer lists a wallet id equal to Acme's.
	w.connect(t, otherSub, map[string]string{"api_key": "isw_other", "shared_client": "true", "wallet_ids": "w_acme"})

	acmeSettle, boltSettle, otherSettle := w.PublishIn(t, acme, settleIswallet), w.PublishIn(t, bolt, settleIswallet), w.PublishIn(t, otherSub, settleIswallet)
	acmeRun, acmeLater := w.wait(t, acme, acmeSettle, "o_acme"), w.wait(t, acme, acmeSettle, "o_other")
	boltRun := w.wait(t, bolt, boltSettle, "o_acme") // same correlation, other company
	otherRun := w.wait(t, otherSub, otherSettle, "o_other")
	boltFailed := w.subscribe(t, bolt, failedPayouts)
	acmeFailed := w.subscribe(t, acme, failedPayouts)

	// Acme's payout confirmed: only Acme's run wakes.
	ev := outflow("evt_1", "wallet.outflow.confirmed", "w_acme", "o_acme")
	st, out := w.deliver(t, wagely, "whsec_wagely", "outflow_event", ev)
	if st != 202 || out["routed"] != float64(1) {
		t.Fatalf("acme's event: %d %v", st, out)
	}
	if s := w.Status(t, acmeRun); s != "completed" {
		t.Errorf("acme's waiting run: %s", s)
	}
	for name, ref := range map[string]runtime.RunRef{"bolt": boltRun, "acme's other": acmeLater, "other partner's": otherRun} {
		if s := w.Status(t, ref); s != "waiting" {
			t.Errorf("%s run: %s, want still waiting", name, s)
		}
	}
	// iswallet retries the delivery: nothing happens twice, nothing is audited twice.
	if st, out := w.deliver(t, wagely, "whsec_wagely", "outflow_event", ev); st != 202 || out["routed"] != float64(1) {
		t.Fatalf("replay: %d %v", st, out)
	}
	for _, tn := range []uuid.UUID{wagely, acme} {
		if n := w.count(t, `SELECT count(*) FROM audit_log WHERE tenant_id = $1 AND action = 'partner.event.route'`, tn); n != 1 {
			t.Errorf("route audits in %v: %d, want 1", tn, n)
		}
	}
	if n := w.count(t, `SELECT count(*) FROM audit_log WHERE tenant_id = $1 AND action = 'partner.event.route'`, bolt); n != 0 {
		t.Errorf("bolt's chain records %d routes", n)
	}
	var actor string
	if err := w.DB.Admin.QueryRow(ctx, `SELECT actor_id FROM audit_log WHERE tenant_id = $1 AND action = 'partner.event.route'`, acme).Scan(&actor); err != nil ||
		actor != "partner:"+wagely.String()+"/ingest:iswallet@1/outflow_event" {
		t.Errorf("acme's audit actor %q %v", actor, err)
	}

	// Bolt's payout failed: Bolt's subscribed workflow starts, Acme's does not.
	if st, out := w.deliver(t, wagely, "whsec_wagely", "outflow_event", outflow("evt_2", "wallet.outflow.failed", "w_bolt", "o_bolt")); st != 202 || out["routed"] != float64(1) {
		t.Fatalf("bolt's event: %d %v", st, out)
	}
	if n, m := w.count(t, `SELECT count(*) FROM runs WHERE workflow_id = $1`, boltFailed), w.count(t, `SELECT count(*) FROM runs WHERE workflow_id = $1`, acmeFailed); n != 1 || m != 0 {
		t.Errorf("failed-payout runs: bolt %d, acme %d; want 1, 0", n, m)
	}
	if n := w.count(t, `SELECT count(*) FROM runs WHERE tenant_id = $1`, wagely); n != 0 {
		t.Errorf("the partner ran %d runs of its customers' events", n)
	}

	// A wallet in no allow-list: kept for the partner and alerted, once.
	stray := outflow("evt_3", "wallet.outflow.reversed", "w_stray", "o_stray")
	for i := 0; i < 2; i++ {
		if st, out := w.deliver(t, wagely, "whsec_wagely", "outflow_event", stray); st != 202 || out["routed"] != float64(0) {
			t.Fatalf("stray event: %d %v", st, out)
		}
	}
	if n := w.count(t, `SELECT count(*) FROM partner_event_routes WHERE tenant_id = $1 AND status = 'unrouted' AND route_key = 'w_stray' AND dedup = 'evt_3'`, wagely); n != 1 {
		t.Errorf("unrouted records: %d", n)
	}
	if n := w.count(t, `SELECT count(*) FROM alerts WHERE tenant_id = $1 AND kind = 'event_unrouted'`, wagely); n != 1 {
		t.Errorf("unrouted alerts: %d", n)
	}
	// The partner's own funding wallet is its own, not lost.
	if st, out := w.deliver(t, wagely, "whsec_wagely", "outflow_event", outflow("evt_4", "wallet.outflow.confirmed", "w_funding", "o_f")); st != 202 || out["routed"] != float64(0) {
		t.Fatalf("funding wallet: %d %v", st, out)
	}
	if n := w.count(t, `SELECT count(*) FROM partner_event_routes WHERE tenant_id = $1`, wagely); n != 3 {
		t.Errorf("partner's route records: %d, want 3 (acme, bolt, stray)", n)
	}

	// The other partner's delivery for "w_acme" reaches its own customer,
	// never Wagely's Acme.
	if st, out := w.deliver(t, other, "whsec_other", "outflow_event", outflow("evt_5", "wallet.outflow.confirmed", "w_acme", "o_other")); st != 202 || out["routed"] != float64(1) {
		t.Fatalf("other partner: %d %v", st, out)
	}
	if s := w.Status(t, otherRun); s != "completed" {
		t.Errorf("other partner's customer's run: %s", s)
	}
	if s := w.Status(t, acmeLater); s != "waiting" {
		t.Errorf("acme's run woken by another partner's event: %s", s)
	}
	// A forged delivery (wrong secret) routes nothing.
	if st, _ := w.deliver(t, wagely, "whsec_wrong", "outflow_event", outflow("evt_6", "wallet.outflow.confirmed", "w_acme", "o_other")); st != 401 {
		t.Errorf("forged delivery: %d", st)
	}
	if s := w.Status(t, acmeLater); s != "waiting" {
		t.Errorf("acme's run woken by a forged event: %s", s)
	}

	// Transfers fire once per side: each side reaches its own company.
	for _, side := range []string{"w_acme2", "w_bolt"} {
		tr := map[string]any{"id": "evt_t_" + side, "event_type": "wallet.transfer.completed", "wallet_id": side,
			"data": map[string]any{"txn_id": "txn_9", "from_wallet_id": "w_acme2", "to_wallet_id": "w_bolt"}}
		if st, out := w.deliver(t, wagely, "whsec_wagely", "transfer_event", tr); st != 202 || out["routed"] != float64(1) {
			t.Fatalf("transfer side %s: %d %v", side, st, out)
		}
	}

	// A suspended customer: refused with 423 so iswallet retries later;
	// nothing is lost.
	if _, err := w.DB.Admin.Exec(ctx, `UPDATE tenants SET status = 'suspended' WHERE id = $1`, idle); err != nil {
		t.Fatal(err)
	}
	if st, _ := w.deliver(t, wagely, "whsec_wagely", "outflow_event", outflow("evt_7", "wallet.outflow.confirmed", "w_idle", "o_idle")); st != 423 {
		t.Errorf("suspended customer's event: %d, want 423", st)
	}

	// A tenant that is not a partner routes nothing, whatever its connections say.
	solo := w.DB.SeedTenant(t, nil).ID
	w.connect(t, solo, map[string]string{"api_key": "isw_solo", "webhook_secret": "whsec_solo", "wallet_ids": "w_solo"})
	if st, out := w.deliver(t, solo, "whsec_solo", "outflow_event", outflow("evt_8", "wallet.outflow.confirmed", "w_bolt", "o_x")); st != 202 || out["routed"] != float64(0) || out["signalled"] != float64(0) {
		t.Errorf("non-partner: %d %v", st, out)
	}
	if n := w.count(t, `SELECT count(*) FROM partner_event_routes WHERE tenant_id = $1`, solo); n != 0 {
		t.Errorf("non-partner records %d routes", n)
	}
}

// TestRouteFunctionIsNarrow: the definer routes only for the single partner
// in scope, and its records are the partner's alone.
func TestRouteFunctionIsNarrow(t *testing.T) {
	w := wagelyWorld{newWorld(t)}
	wagely := w.Tenant
	w.partner(t, wagely)
	acme := w.subTenant(t, wagely)
	w.connect(t, acme, map[string]string{"api_key": "k", "shared_client": "true", "wallet_ids": "w_acme"})
	route := func(scope []uuid.UUID) (int, error) {
		n := 0
		err := db.InTenantTx(ctx, w.DB.App, scope, func(tx pgx.Tx) error {
			return tx.QueryRow(ctx, `SELECT count(*) FROM taskiem_partner_route_event('prod', 'iswallet@1', 'outflow_event', 'wallet_ids', 'w_acme', 'wallet.outflow.confirmed', $1)`,
				uuid.NewString()).Scan(&n)
		})
		return n, err
	}
	// From the sub-tenant itself, or a scope of two: nothing.
	for name, scope := range map[string][]uuid.UUID{"sub-tenant": {acme}, "two tenants": {wagely, acme}} {
		if n, err := route(scope); err != nil || n != 0 {
			t.Errorf("%s: %d %v", name, n, err)
		}
	}
	if n, err := route([]uuid.UUID{wagely}); err != nil || n != 1 {
		t.Errorf("partner: %d %v", n, err)
	}
	// The sub-tenant cannot read the partner's records; nor can it write them.
	if err := db.InTenantTx(ctx, w.DB.App, []uuid.UUID{acme}, func(tx pgx.Tx) error {
		var n int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM partner_event_routes`).Scan(&n); err != nil || n != 0 {
			t.Errorf("sub-tenant sees %d route records (%v)", n, err)
		}
		_, err := tx.Exec(ctx, `INSERT INTO partner_event_routes (tenant_id, environment, connector, trigger_name, event, dedup, route_key, status) VALUES ($1, 'prod', 'x', 'y', 'z', 'd', 'k', 'unrouted')`, acme)
		return err
	}); err == nil {
		t.Error("the application role wrote a route record directly")
	}
	// Connection route keys are tenant-scoped like connections.
	if err := db.InTenantTx(ctx, w.DB.App, []uuid.UUID{wagely}, func(tx pgx.Tx) error {
		var n int
		err := tx.QueryRow(ctx, `SELECT count(*) FROM connection_route_keys`).Scan(&n)
		if err == nil && n != 0 {
			t.Errorf("partner reads %d of its sub-tenant's route keys directly", n)
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
}
