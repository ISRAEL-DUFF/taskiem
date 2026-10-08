package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/israel-duff/taskiem/engine/billing"
)

// billingWorld is a world with billing on and a Paystack double that
// starts checkouts.
func billingWorld(t *testing.T) *world {
	t.Helper()
	w := newWorld(t)
	cfg, err := billing.LoadConfig("../deploy/plans.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if err := billing.Store(context.Background(), w.env.DB.App, cfg, "test"); err != nil {
		t.Fatal(err)
	}
	ps := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/transaction/initialize" { //nolint:misspell // Paystack's path
			_ = json.NewEncoder(rw).Encode(map[string]any{"status": true, "data": map[string]any{"authorization_url": "https://checkout.paystack.test/x"}})
			return
		}
		rw.WriteHeader(404)
	}))
	t.Cleanup(ps.Close)
	w.env.Store.Billing = true
	w.srv.Billing = &billing.Service{Pool: w.env.DB.App, Store: w.env.Store, Config: cfg, Enabled: true, Default: "paystack",
		Providers: map[string]billing.Provider{"paystack": &billing.Paystack{SecretKey: "sk_test", BaseURL: ps.URL}}}
	return w
}

func TestBillingOffIsTheInternalPlan(t *testing.T) {
	w := newWorld(t)
	owner := w.tenant(t, "Acme", "owner@acme.test")
	st := owner.must(200, "GET", "/v1/billing/status", nil)
	if st["enabled"] != false || st["plan"] != billing.InternalPlanID {
		t.Fatalf("status: %v", st)
	}
	ov := owner.must(200, "GET", "/v1/billing", nil)
	if ov["enabled"] != false || ov["plan"].(map[string]any)["id"] != billing.InternalPlanID {
		t.Fatalf("overview: %v", ov)
	}
	// Every feature is on: SSO and custom roles pass the plan check.
	if st, out := owner.do("PUT", "/v1/roles/clerk", map[string]any{"permissions": []string{"run.read"}}); st == 402 {
		t.Fatalf("custom role refused with billing off: %v", out)
	}
	if st, _ := owner.do("POST", "/v1/billing/plan", map[string]any{"plan": "starter"}); st != 409 {
		t.Fatalf("plan change with billing off: %d", st)
	}
	me := owner.must(200, "GET", "/v1/me", nil)
	if !strings.Contains(toJSON(me["permissions"]), "billing.manage") {
		t.Fatalf("owners hold billing.manage: %v", me["permissions"])
	}
}

func TestBillingAPI(t *testing.T) {
	w := billingWorld(t)
	owner := w.tenant(t, "Acme", "owner@acme.test")
	ov := owner.must(200, "GET", "/v1/billing", nil)
	sub := ov["subscription"].(map[string]any)
	if sub["status"] != "trial" || sub["plan"] != "growth" || ov["placeholder_prices"] != true || len(ov["plans"].([]any)) != 3 {
		t.Fatalf("overview: %v", ov)
	}
	if _, leaked := sub["authorization_code"]; leaked {
		t.Fatal("authorization code in the API")
	}

	// Growth has no SSO, custom roles or embedding: 402 with the feature.
	for _, c := range []struct{ method, path string }{
		{"POST", "/v1/sso"}, {"PUT", "/v1/roles/clerk"}, {"PUT", "/v1/scim"}, {"GET", "/v1/partner/"},
	} {
		st, out := owner.do(c.method, c.path, map[string]any{})
		if st != 402 || out["code"] != "plan_feature_required" {
			t.Errorf("%s %s: %d %v", c.method, c.path, st, out)
		}
	}
	// Reads of gated areas still work.
	owner.must(200, "GET", "/v1/sso", nil)
	owner.must(200, "GET", "/v1/ai/status", nil)

	// Downgrade blockers listed.
	var tenant uuid.UUID
	if err := w.env.DB.Admin.QueryRow(context.Background(), `SELECT id FROM tenants WHERE name = 'Acme'`).Scan(&tenant); err != nil {
		t.Fatal(err)
	}
	for range 21 {
		if _, err := w.env.DB.Admin.Exec(context.Background(), `INSERT INTO workflows (id, tenant_id, name, created_by) VALUES ($1, $2, 'x', $2)`, uuid.Must(uuid.NewV7()), tenant); err != nil {
			t.Fatal(err)
		}
	}
	st, out := owner.do("POST", "/v1/billing/plan", map[string]any{"plan": "starter"})
	if st != 409 || out["code"] != "downgrade_blocked" || !strings.Contains(toJSON(out["blockers"]), "max_workflows") {
		t.Fatalf("downgrade: %d %v", st, out)
	}
	// An upgrade during the trial is the plan tried.
	res := owner.must(200, "POST", "/v1/billing/plan", map[string]any{"plan": "business"})
	if res["effective"] != "now" {
		t.Fatalf("trial upgrade: %v", res)
	}
	// Business includes embedding: past the plan check (the partner API
	// itself then wants a partner API key).
	if st, out := owner.do("GET", "/v1/partner/", nil); st == 402 {
		t.Fatalf("business partner API: %v", out)
	}

	// Checkout returns the provider's page.
	co := owner.must(200, "POST", "/v1/billing/checkout", map[string]any{"plan": "growth", "interval": "annual", "email": "finance@acme.test"})
	if co["checkout_url"] != "https://checkout.paystack.test/x" || !strings.HasPrefix(co["reference"].(string), "tkm-"+tenant.String()) {
		t.Fatalf("checkout: %v", co)
	}
	inv := co["invoice"].(map[string]any)
	if inv["total_kobo"].(float64) != 64_500_000 || inv["interval"] != "annual" {
		t.Fatalf("annual growth with VAT: %v", inv)
	}
	html := owner.must(200, "GET", "/v1/billing/invoices/"+inv["id"].(string)+"?format=html", nil)
	if !strings.Contains(html["raw"].(string), "VAT (7.5%)") || !strings.Contains(html["raw"].(string), "₦645,000.00") {
		t.Fatalf("invoice html: %v", html)
	}

	// Unsigned webhooks are refused.
	anon := &client{t: t, base: w.base}
	if st, _ := anon.do("POST", "/v1/billing/webhooks/paystack", map[string]any{"event": "charge.success"}); st != 401 {
		t.Fatalf("unsigned webhook: %d", st)
	}
	if st, _ := anon.do("POST", "/v1/billing/webhooks/stripe", map[string]any{}); st != 404 {
		t.Fatalf("unknown provider: %d", st)
	}

	// Cancelling, and taking it back.
	owner.must(200, "POST", "/v1/billing/cancel", nil)
	owner.must(200, "POST", "/v1/billing/cancel", map[string]any{"resume": true})

	// Degraded: new runs answer 402; members without billing.manage cannot
	// see invoices but see the banner.
	wf := publishFlow(t, owner, `{"schema":"wd/v1","id":"wf_t","version":1,"name":"t","trigger":{"type":"manual"},"steps":[{"id":"t","type":"transform","config":{"output":{"x":1}}}]}`)
	if _, err := w.env.DB.Admin.Exec(context.Background(), `UPDATE subscriptions SET status = 'degraded' WHERE tenant_id = $1`, tenant); err != nil {
		t.Fatal(err)
	}
	w.env.Store.ForgetLimits(tenant)
	st, out = owner.do("POST", "/v1/workflows/"+wf+"/runs", map[string]any{"input": map[string]any{}})
	if st != 402 || out["code"] != "billing_degraded" {
		t.Fatalf("degraded start: %d %v", st, out)
	}
	owner.must(201, "POST", "/v1/members", map[string]any{"email": "dev@acme.test", "name": "Dev", "password": "correct horse battery", "roles": []string{"builder"}})
	dev := w.login(t, "dev@acme.test", "correct horse battery")
	if st, _ := dev.do("GET", "/v1/billing", nil); st != 403 {
		t.Fatalf("builder reads billing: %d", st)
	}
	bs := dev.must(200, "GET", "/v1/billing/status", nil)
	if bs["status"] != "degraded" || bs["banner"] == nil {
		t.Fatalf("status for members: %v", bs)
	}
}
