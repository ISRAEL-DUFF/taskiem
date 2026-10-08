package ingest_test

import (
	"crypto/sha256"
	"testing"
)

// Verifying a webhook decrypts its key: the read is recorded, with no run.
func TestWebhookVerificationRecordsRead(t *testing.T) {
	w := newWorld(t)
	w.publish(t, hookFlow)
	if _, err := w.Vault.Put(ctx, w.Tenant, "prod", "webhook_wf_orders", []byte("whsec"), "test"); err != nil {
		t.Fatal(err)
	}
	body := []byte(`{"order_id":"A1","total":500}`)
	if st, _ := w.post(t, "/shop/orders", body, "X-Taskiem-Signature", "sha256="+sign(sha256.New, "whsec", body)); st != 202 {
		t.Fatalf("delivery: %d", st)
	}
	if st, _ := w.post(t, "/shop/orders", body, "X-Taskiem-Signature", "sha256="+sign(sha256.New, "wrong", body)); st != 401 {
		t.Fatalf("bad signature: %d", st)
	}
	rows, err := w.DB.Admin.Query(ctx, `SELECT kind, environment, name, purpose, run_id IS NULL, actor FROM secret_reads WHERE tenant_id = $1`, w.Tenant)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	n := 0
	for rows.Next() {
		var kind, env, name, purpose, actor string
		var noRun bool
		if err := rows.Scan(&kind, &env, &name, &purpose, &noRun, &actor); err != nil {
			t.Fatal(err)
		}
		if kind != "webhook" || env != "prod" || name != "webhook_wf_orders" || purpose != "ingest.verify" || !noRun || actor != "system" {
			t.Errorf("read: %s %s %s %s run-less=%v %s", kind, env, name, purpose, noRun, actor)
		}
		n++
	}
	// Both deliveries decrypted the key, whether or not they verified.
	if n != 2 {
		t.Errorf("%d reads, want 2", n)
	}
}
