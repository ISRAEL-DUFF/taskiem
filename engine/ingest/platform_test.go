package ingest_test

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
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
	"github.com/israel-duff/taskiem/engine/secrets"
)

const platformLive, platformTest = "platform-live-secret", "platform-test-secret"

// platformWorld is Taskiem holding its own iswallet client (decision
// 0052): one subscription per mode, every business's wallet under it.
type platformWorld struct{ wagelyWorld }

func newPlatformWorld(t *testing.T) platformWorld {
	t.Helper()
	w := newWorld(t)
	if err := w.Registry.Register(iswallet.New(iswallet.Options{})); err != nil {
		t.Fatal(err)
	}
	w.Vault.Platform = secrets.PlatformClients{"iswallet": {
		"live": {"api_key": "isw_platform_live", "shared_client": "true"},
		"test": {"api_key": "isw_platform_test", "shared_client": "true"},
	}}
	w.h.Platform = map[string]ingest.Platform{"iswallet": {WebhookSecrets: map[string]string{"live": platformLive, "test": platformTest}}}
	return platformWorld{wagelyWorld{w}}
}

// turnOn makes a business's connection under Taskiem's client, as turning
// on "Receive money with Taskiem" does.
func (w platformWorld) turnOn(t *testing.T, tenant uuid.UUID, mode, wallet string) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	if err := db.InTenantTx(ctx, w.DB.App, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		var err error
		id, err = w.Vault.CreatePlatformConnectionTx(ctx, tx, tenant, "prod", "iswallet", "taskiem", mode, wallet, map[string]string{"wallet_ids": wallet}, "test")
		return err
	}); err != nil {
		t.Fatal(err)
	}
	return id
}

func (w platformWorld) send(t *testing.T, mode, secret string, ev map[string]any) (int, map[string]any) {
	t.Helper()
	body, _ := json.Marshal(ev)
	ts := strconv.FormatInt(time.Now().Unix(), 10)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(ts + "." + string(body)))
	req, _ := http.NewRequest("POST", w.srv.URL+"/hooks/platform/iswallet@1?mode="+mode, bytes.NewReader(body))
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

func credit(id, wallet string) map[string]any {
	return map[string]any{"id": id, "event_type": "wallet.credit.posted", "wallet_id": wallet,
		"data": map[string]any{"provider_reference": "PR-" + id, "amount": 500000, "amount_credited": 499500, "currency": "NGN",
			"virtual_account": map[string]any{"reference": "cus_" + id, "account_number": "0123456789"}}}
}

const creditWorkflow = `{"schema":"wd/v1","id":"wf_credit","version":1,"name":"credits","trigger":{"type":"connector_event","config":{"connector":"iswallet@1","trigger":"credit_event"}},
  "steps":[{"id":"a","type":"transform","config":{"output":"=trigger.body.wallet_id"}}]}`

// A credit to a business's wallet reaches that business only: not another
// under the same client, not one that lists the wallet on a connection of
// its own, and not a connection in the other mode.
func TestPlatformEventsReachOnlyTheWalletsBusiness(t *testing.T) {
	w := newPlatformWorld(t)
	acme := w.Tenant
	bolt, sly := w.subTenant(t, w.DB.SeedTenant(t, nil).ID), w.DB.SeedTenant(t, nil).ID
	if err := db.InTenantTx(ctx, w.DB.App, []uuid.UUID{sly}, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO environments (tenant_id, name) VALUES ($1, 'prod')`, sly)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	w.turnOn(t, acme, "live", "W-ACME")
	w.turnOn(t, bolt, "test", "W-BOLT")
	// Sly lists Acme's wallet on an iswallet connection of its own.
	w.connect(t, sly, map[string]string{"api_key": "isw_sly", "wallet_ids": "W-ACME", "webhook_secret": "sly"})
	acmeWF, boltWF, slyWF := w.subscribe(t, acme, creditWorkflow), w.subscribe(t, bolt, creditWorkflow), w.subscribe(t, sly, creditWorkflow)
	runs := func(tenant, wf uuid.UUID) int {
		t.Helper()
		return appWorld{w.world}.runsIn(t, tenant, wf)
	}

	st, out := w.send(t, "live", platformLive, credit("E1", "W-ACME"))
	if st != 200 || out["routed"] != float64(1) {
		t.Fatalf("acme's credit: %d %v", st, out)
	}
	if a, b, s := runs(acme, acmeWF), runs(bolt, boltWF), runs(sly, slyWF); a != 1 || b != 0 || s != 0 {
		t.Errorf("runs after acme's credit: acme %d, bolt %d, sly %d", a, b, s)
	}
	// Again: one event.
	w.send(t, "live", platformLive, credit("E1", "W-ACME"))
	if a := runs(acme, acmeWF); a != 1 {
		t.Errorf("a redelivery started %d runs", a)
	}
	// Bolt's wallet is under the test client: its live events go nowhere,
	// its test events reach it, and the live secret does not sign them.
	if _, out := w.send(t, "live", platformLive, credit("E2", "W-BOLT")); out["routed"] != float64(0) {
		t.Errorf("a live event for a test wallet: %v", out)
	}
	if st, _ := w.send(t, "test", platformLive, credit("E3", "W-BOLT")); st != 401 {
		t.Errorf("the live secret on the test subscription: %d", st)
	}
	if _, out := w.send(t, "test", platformTest, credit("E3", "W-BOLT")); out["routed"] != float64(1) || runs(bolt, boltWF) != 1 {
		t.Errorf("bolt's test credit: %v", out)
	}
	// A wallet nobody holds: acknowledged and counted.
	if st, out := w.send(t, "live", platformLive, credit("E4", "W-NOBODY")); st != 200 || out["routed"] != float64(0) {
		t.Errorf("an unknown wallet: %d %v", st, out)
	}
	var drops int
	if err := w.DB.Admin.QueryRow(ctx, `SELECT COALESCE(sum(hits), 0) FROM app_event_drops WHERE connector = 'iswallet@1'`).Scan(&drops); err != nil || drops != 2 {
		t.Errorf("drops counted: %d %v", drops, err)
	}
	// An event type no trigger sends is acknowledged; a payout's outcome
	// reaches the payout trigger.
	if st, out := w.send(t, "live", platformLive, map[string]any{"id": "E5", "event_type": "wallet.frozen", "wallet_id": "W-ACME"}); st != 200 || out["ignored"] != "wallet.frozen" {
		t.Errorf("an event no trigger sends: %d %v", st, out)
	}
	if st, out := w.send(t, "live", platformLive, outflow("E6", "wallet.outflow.confirmed", "W-ACME", "OUT-1")); st != 200 || out["routed"] != float64(1) {
		t.Errorf("a payout's outcome: %d %v", st, out)
	}
	if st, _ := w.send(t, "live", "wrong", credit("E7", "W-ACME")); st != 401 {
		t.Errorf("a wrong signature: %d", st)
	}
	// No client for a provider: no such URL.
	w.h.Platform = nil
	if st, _ := w.send(t, "live", platformLive, credit("E8", "W-ACME")); st != 404 {
		t.Errorf("no platform client: %d", st)
	}
}

// The vault hands a platform connection the platform's key for its mode,
// with only its own wallet, and nobody replaces what it holds.
func TestPlatformConnectionsCredentials(t *testing.T) {
	w := newPlatformWorld(t)
	id := w.turnOn(t, w.Tenant, "live", "W-ACME")
	creds, err := w.Vault.Credentials(ctx, w.Tenant, "prod", "iswallet", "taskiem")
	if err != nil || creds["api_key"] != "isw_platform_live" || creds["shared_client"] != "true" || creds["wallet_ids"] != "W-ACME" || creds["$mode"] != "live" {
		t.Fatalf("credentials: %v %v", creds, err)
	}
	err = db.InTenantTx(ctx, w.DB.App, []uuid.UUID{w.Tenant}, func(tx pgx.Tx) error {
		return w.Vault.ReplaceCredentialsTx(ctx, tx, w.Tenant, id, map[string]string{"api_key": "mine", "wallet_ids": "W-OTHER"}, "test")
	})
	if !errors.Is(err, secrets.ErrPlatformConnection) {
		t.Errorf("replacing a platform connection's credentials: %v", err)
	}
	// Nothing of the platform's key is stored with the connection.
	var stored int
	if err := w.DB.Admin.QueryRow(ctx, `SELECT count(*) FROM connections WHERE id = $1 AND platform = 'taskiem' AND mode = 'live'`, id).Scan(&stored); err != nil || stored != 1 {
		t.Errorf("the connection's row: %d %v", stored, err)
	}
	// A Taskiem without the client for the mode refuses to hand it out.
	w.Vault.Platform = secrets.PlatformClients{"iswallet": {"test": {"api_key": "isw_platform_test"}}}
	if _, err := w.Vault.Credentials(ctx, w.Tenant, "prod", "iswallet", "taskiem"); !errors.Is(err, secrets.ErrPlatformClient) {
		t.Errorf("no live client: %v", err)
	}
}
