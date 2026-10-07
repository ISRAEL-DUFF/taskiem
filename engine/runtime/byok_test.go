package runtime_test

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/israel-duff/taskiem/engine/byok/byoktest"
	"github.com/israel-duff/taskiem/engine/connector"
	"github.com/israel-duff/taskiem/engine/history"
	rt "github.com/israel-duff/taskiem/engine/runtime/runtimetest"
)

// A tenant whose customer key is revoked mid-flight: steps that need its
// secrets park (nothing is sent, nothing is lost, the run does not fail),
// even a code step with no retries and an unsafe write; once the key
// returns the key job resumes them without spending their retry budget
// and the run completes, each effect happening once (decision 0019).
func TestKeyUnavailableParksAndResumes(t *testing.T) {
	e := rt.New(t)
	e.VaultSecrets = true
	fake := byoktest.NewTransit(t)
	e.Vault.BYOK, e.Vault.BYOKCacheTTL = byoktest.Factory(fake.Server), time.Nanosecond // no caching: every use asks the customer's KMS

	man := strings.Replace(strings.Replace(rt.FakepayManifest, "id: fakepay", "id: keypay", 1),
		"auth: { type: none }", "auth: { type: api_key, fields: [{ key: secret_key, label: Secret key, secret: true }] }", 1)
	conn := e.Provider.Connector()
	conn.Manifest = connector.MustParse([]byte(man))
	if err := e.Registry.Register(conn); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Vault.CreateConnection(ctx, e.Tenant, "prod", "keypay", "main", "api_key", map[string]string{"secret_key": "sk_live_1"}, "admin"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Vault.Put(ctx, e.Tenant, "prod", "api_token", []byte("tok-1"), "admin"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := e.Vault.EnableBYOK(ctx, e.Tenant, fake.Config("customer"), map[string]string{"token": fake.Token}, "owner"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Vault.RewrapAll(ctx, e.Tenant); err != nil {
		t.Fatal(err)
	}

	src, _ := json.Marshal(`export default (input: any, host: any) => ({ n: host.secret("api_token").length })`)
	wf := e.Publish(t, wfDoc(`{"id":"notify","type":"connector","connector":"keypay@1","action":"notify","input":{"logical_id":"n1"}},
	  {"id":"calc","type":"code","config":{"language":"typescript","secrets":["api_token"],"source":`+string(src)+`}}`, ""))

	fake.Revoke(true)
	ref := e.Start(t, wf, map[string]any{})
	e.Drain(t)
	if st := e.Status(t, ref); st != "needs_reconciliation" {
		t.Fatalf("status %s: %s", st, types(events(t, e, ref)))
	}
	if n := e.Provider.Executions("n1"); n != 0 {
		t.Fatalf("the write was sent %d times without credentials", n)
	}
	parked := 0
	for _, ev := range events(t, e, ref) {
		if ev.Type != history.StepFailed {
			continue
		}
		var p history.FailedPayload
		_ = json.Unmarshal(ev.Payload, &p)
		if p.Error.Kind != history.KindKeyUnavailable || p.Error.Next != "park" || !strings.Contains(p.Error.Message, "encryption key is unavailable") {
			t.Errorf("%s: %+v", ev.StepID, p.Error)
		}
		parked++
	}
	if parked != 2 {
		t.Fatalf("%d steps parked, want 2", parked)
	}
	var rows int
	_ = e.DB.Admin.QueryRow(ctx, `SELECT count(*) FROM key_parked_steps WHERE run_id = $1`, ref.ID).Scan(&rows)
	if rows != 2 {
		t.Fatalf("%d parked rows", rows)
	}

	// The key returns: the key job's check succeeds, then it resumes.
	fake.Revoke(false)
	if h, err := e.Vault.CheckKey(ctx, e.Tenant, "system"); err != nil || !h.OK {
		t.Fatalf("check: %+v %v", h, err)
	}
	n, err := e.Store.ResumeKeyParked(ctx, e.Tenant)
	if err != nil || n != 2 {
		t.Fatalf("resume: %d %v", n, err)
	}
	e.Drain(t)
	if st := e.Status(t, ref); st != "completed" {
		t.Fatalf("after resume: %s: %s", st, types(events(t, e, ref)))
	}
	if n := e.Provider.Executions("n1"); n != 1 {
		t.Errorf("the write happened %d times", n)
	}
	_ = e.DB.Admin.QueryRow(ctx, `SELECT count(*) FROM key_parked_steps WHERE run_id = $1`, ref.ID).Scan(&rows)
	if rows != 0 {
		t.Errorf("%d parked rows left", rows)
	}
	// A second resume finds nothing.
	if n, err := e.Store.ResumeKeyParked(ctx, e.Tenant); err != nil || n != 0 {
		t.Errorf("second resume: %d %v", n, err)
	}
	var audited int
	_ = e.DB.Admin.QueryRow(ctx, `SELECT count(*) FROM audit_log WHERE tenant_id = $1 AND action = 'key.steps_resumed'`, e.Tenant).Scan(&audited)
	if audited != 1 {
		t.Errorf("%d key.steps_resumed entries", audited)
	}
}
