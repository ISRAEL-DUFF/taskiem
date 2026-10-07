package api_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/israel-duff/taskiem/engine/byok/byoktest"
)

func TestKeysAPI(t *testing.T) {
	w := newWorld(t)
	fake := byoktest.NewTransit(t)
	w.env.Vault.BYOK, w.env.Vault.BYOKCacheTTL = byoktest.Factory(fake.Server), time.Nanosecond
	owner := w.tenant(t, "Acme", "owner@acme.test")

	st := owner.must(200, "GET", "/v1/keys", nil)
	if s := st["status"].(map[string]any); s["mode"] != "platform" || st["plan_allows_byok"] != true {
		t.Fatalf("keys: %v", st)
	}
	owner.must(204, "PUT", "/v1/secrets/prod/api_token", map[string]any{"value": "tok-1"})

	cfg := map[string]any{"provider": "vault_transit", "address": fake.Server.URL, "key": "customer", "ca_cert": byoktest.CACert(fake.Server)}
	with := func(creds map[string]string, extra ...any) map[string]any {
		out := map[string]any{"credentials": creds}
		for k, v := range cfg {
			out[k] = v
		}
		for i := 0; i+1 < len(extra); i += 2 {
			out[extra[i].(string)] = extra[i+1]
		}
		return out
	}
	if code, out := owner.do("PUT", "/v1/keys/byok", with(map[string]string{"token": fake.Token}, "address", "http://plain.example")); code != 400 || out["code"] != "invalid_key_config" {
		t.Fatalf("bad config: %d %v", code, out)
	}
	if code, out := owner.do("PUT", "/v1/keys/byok", with(map[string]string{"token": "wrong"})); code != 422 || out["code"] != "key_verification_failed" {
		t.Fatalf("bad token: %d %v", code, out)
	}
	res := owner.must(201, "PUT", "/v1/keys/byok", with(map[string]string{"token": fake.Token}))
	if res["version"].(float64) != 2 {
		t.Fatalf("enable: %v", res)
	}
	st = owner.must(200, "GET", "/v1/keys", nil)
	if s := st["status"].(map[string]any); s["mode"] != "customer" || s["byok"].(map[string]any)["status"] != "active" {
		t.Fatalf("after enable: %v", st)
	}
	if strings.Contains(toJSON(st), fake.Token) || strings.Contains(toJSON(res), fake.Token) {
		t.Fatal("credentials returned by the API")
	}

	// Only key.manage holders (owners) see or change keys.
	owner.must(201, "POST", "/v1/members", map[string]any{"email": "admin@acme.test", "name": "Admin", "password": "correct horse battery", "roles": []string{"admin"}})
	admin := w.login(t, "admin@acme.test", "correct horse battery")
	if code, _ := admin.do("GET", "/v1/keys", nil); code != 403 {
		t.Fatalf("admin reads keys: %d", code)
	}
	if code, _ := admin.do("DELETE", "/v1/keys/byok", nil); code != 403 {
		t.Fatalf("admin removes the key: %d", code)
	}

	// Revoked: secret writes answer 503 key_unavailable; checks report it.
	fake.Revoke(true)
	if code, out := owner.do("PUT", "/v1/secrets/prod/other", map[string]any{"value": "x"}); code != 503 || out["code"] != "key_unavailable" {
		t.Fatalf("write with the key revoked: %d %v", code, out)
	}
	h := owner.must(200, "POST", "/v1/keys/byok/check", nil)["health"].(map[string]any)
	if h["ok"] != false || !strings.Contains(h["error"].(string), "permission denied") {
		t.Fatalf("check: %v", h)
	}
	fake.Revoke(false)
	h = owner.must(200, "POST", "/v1/keys/byok/check", nil)["health"].(map[string]any)
	if h["ok"] != true {
		t.Fatalf("check after restore: %v", h)
	}

	// New credentials must reach the same key.
	if code, out := owner.do("PUT", "/v1/keys/byok/credentials", map[string]any{"credentials": map[string]string{"role_id": fake.RoleID, "secret_id": "nope"}}); code != 422 {
		t.Fatalf("bad replacement: %d %v", code, out)
	}
	owner.must(200, "PUT", "/v1/keys/byok/credentials", map[string]any{"credentials": map[string]string{"role_id": fake.RoleID, "secret_id": fake.SecretID}})

	rot := owner.must(202, "POST", "/v1/keys/rotate", nil)
	if rot["version"].(float64) != 3 {
		t.Fatalf("rotate: %v", rot)
	}
	off := owner.must(200, "DELETE", "/v1/keys/byok", nil)
	if off["version"].(float64) != 4 {
		t.Fatalf("disable: %v", off)
	}
	if code, out := owner.do("DELETE", "/v1/keys/byok", nil); code != 404 || out["code"] != "no_customer_key" {
		t.Fatalf("second disable: %d %v", code, out)
	}
	audit := owner.must(200, "GET", "/v1/audit?limit=100", nil)
	for _, want := range []string{"key.byok_enabled", "key.byok_checked", "key.byok_credentials_replaced", "secret.rotate_key", "key.byok_disabled"} {
		if !strings.Contains(toJSON(audit), want) {
			t.Errorf("no %s in the audit log", want)
		}
	}
}

func TestKeysAPIPlanFeature(t *testing.T) {
	w := billingWorld(t)
	owner := w.tenant(t, "Acme", "owner@acme.test") // Growth trial: no byok
	if st := owner.must(200, "GET", "/v1/keys", nil); st["plan_allows_byok"] != false {
		t.Fatalf("keys: %v", st)
	}
	code, out := owner.do("PUT", "/v1/keys/byok", map[string]any{"provider": "vault_transit", "address": "https://bao.example", "key": "k", "credentials": map[string]string{"token": "t"}})
	if code != 402 || out["code"] != "plan_feature_required" || out["feature"] != "byok" {
		t.Fatalf("byok on growth: %d %v", code, out)
	}
	// Rotation is not a plan feature.
	owner.must(202, "POST", "/v1/keys/rotate", nil)

	// On Enterprise the key can be brought; moving to a plan without the
	// feature is then blocked until the tenant returns to the platform key.
	fake := byoktest.NewTransit(t)
	w.env.Vault.BYOK = byoktest.Factory(fake.Server)
	var tenant uuid.UUID
	if err := w.env.DB.Admin.QueryRow(context.Background(), `SELECT id FROM tenants WHERE name = 'Acme'`).Scan(&tenant); err != nil {
		t.Fatal(err)
	}
	if err := w.srv.Billing.Grant(context.Background(), tenant, "enterprise", nil, "test"); err != nil {
		t.Fatal(err)
	}
	owner.must(201, "PUT", "/v1/keys/byok", map[string]any{"provider": "vault_transit", "address": fake.Server.URL, "key": "customer",
		"ca_cert": byoktest.CACert(fake.Server), "credentials": map[string]string{"token": fake.Token}})
	// As if Enterprise were paid for rather than granted, so the tenant may
	// change it itself.
	if _, err := w.env.DB.Admin.Exec(context.Background(), `UPDATE subscriptions SET status = 'active', comp_until = NULL WHERE tenant_id = $1`, tenant); err != nil {
		t.Fatal(err)
	}
	st, out := owner.do("POST", "/v1/billing/plan", map[string]any{"plan": "growth"})
	if st != 409 || !strings.Contains(toJSON(out["blockers"]), "feature:byok") {
		t.Fatalf("downgrade with a customer key: %d %v", st, out)
	}
	owner.must(200, "DELETE", "/v1/keys/byok", nil)
}
