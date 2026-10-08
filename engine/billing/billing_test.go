package billing_test

import (
	"context"
	"crypto/hmac"
	"crypto/sha512"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/billing"
	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/runtime"
	rt "github.com/israel-duff/taskiem/engine/runtime/runtimetest"
)

var ctx = context.Background()

const secret = "sk_test_platform"

// fakePaystack is a Paystack test double: checkout, verify and
// charge_authorization, with payments completed by the test.
type fakePaystack struct {
	mu     sync.Mutex
	txns   map[string]map[string]any
	charge string // status charge_authorization answers (default success)
	srv    *httptest.Server
	calls  map[string]int
}

func newFakePaystack(t *testing.T) *fakePaystack {
	f := &fakePaystack{txns: map[string]map[string]any{}, calls: map[string]int{}, charge: "success"}
	mux := http.NewServeMux()
	auth := func(w http.ResponseWriter, r *http.Request) bool {
		if r.Header.Get("Authorization") != "Bearer "+secret {
			w.WriteHeader(401)
			return false
		}
		return true
	}
	mux.HandleFunc("POST /transaction/initialize", func(w http.ResponseWriter, r *http.Request) { //nolint:misspell // Paystack's path
		if !auth(w, r) {
			return
		}
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		ref := body["reference"].(string)
		f.mu.Lock()
		f.calls["checkout"]++
		f.txns[ref] = map[string]any{"id": 1, "reference": ref, "amount": body["amount"], "currency": "NGN", "status": "ongoing"}
		f.mu.Unlock()
		_ = json.NewEncoder(w).Encode(map[string]any{"status": true, "message": "ok", "data": map[string]any{
			"authorization_url": "https://checkout.paystack.test/" + ref, "access_code": "ac_" + ref[len(ref)-6:], "reference": ref}})
	})
	mux.HandleFunc("GET /transaction/verify/{ref}", func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		f.mu.Lock()
		defer f.mu.Unlock()
		f.calls["verify"]++
		tx, ok := f.txns[r.PathValue("ref")]
		if !ok {
			w.WriteHeader(404)
			_ = json.NewEncoder(w).Encode(map[string]any{"status": false, "message": "Transaction reference not found"})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"status": true, "data": tx})
	})
	mux.HandleFunc("POST /transaction/charge_authorization", func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		f.mu.Lock()
		defer f.mu.Unlock()
		f.calls["charge"]++
		ref := body["reference"].(string)
		tx := map[string]any{"id": 2, "reference": ref, "amount": body["amount"], "currency": "NGN", "status": f.charge, "channel": "card",
			"authorization": map[string]any{"authorization_code": body["authorization_code"], "channel": "card", "reusable": true, "last4": "4081", "brand": "visa", "exp_month": "12", "exp_year": "2030"}}
		f.txns[ref] = tx
		_ = json.NewEncoder(w).Encode(map[string]any{"status": true, "message": "Charge attempted", "data": tx})
	})
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

// pay completes a checkout: a card payment of amount (kobo).
func (f *fakePaystack) pay(ref string, amount int64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.txns[ref]["status"] = "success"
	f.txns[ref]["amount"] = amount
	f.txns[ref]["channel"] = "card"
	f.txns[ref]["authorization"] = map[string]any{"authorization_code": "AUTH_" + ref[len(ref)-6:], "channel": "card", "reusable": true,
		"last4": "4081", "brand": "visa", "exp_month": "12", "exp_year": "2030"}
}

func signed(body []byte) http.Header {
	m := hmac.New(sha512.New, []byte(secret))
	m.Write(body)
	h := http.Header{}
	h.Set("x-paystack-signature", hex.EncodeToString(m.Sum(nil)))
	return h
}

func chargeSuccess(ref string, amount int64) []byte {
	raw, _ := json.Marshal(map[string]any{"event": "charge.success", "data": map[string]any{"id": 9, "reference": ref, "amount": amount, "status": "success"}})
	return raw
}

type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) Now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *clock) add(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d).Truncate(time.Microsecond) // Postgres keeps microseconds
	c.mu.Unlock()
}

type mailer struct {
	mu   sync.Mutex
	sent []string
}

func (m *mailer) Send(_ context.Context, _ string, to []string, msg []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sent = append(m.sent, strings.Join(to, ",")+"|"+string(msg))
	return nil
}

func (m *mailer) count(substr string) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := 0
	for _, s := range m.sent {
		if strings.Contains(s, substr) {
			n++
		}
	}
	return n
}

type world struct {
	e     *rt.Env
	svc   *billing.Service
	pay   *fakePaystack
	clock *clock
	mail  *mailer
}

func newWorld(t *testing.T) *world {
	t.Helper()
	e := rt.New(t)
	e.Store.Billing = true
	cfg, err := billing.LoadConfig("../../deploy/plans.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if err := billing.Store(ctx, e.DB.App, cfg, "test"); err != nil {
		t.Fatal(err)
	}
	f := newFakePaystack(t)
	c := &clock{t: time.Now().UTC().Truncate(time.Microsecond)}
	m := &mailer{}
	svc := &billing.Service{Pool: e.DB.App, Store: e.Store, Config: cfg, Enabled: true, Default: "paystack",
		Providers: map[string]billing.Provider{"paystack": &billing.Paystack{SecretKey: secret, BaseURL: f.srv.URL}},
		Clock:     c.Now, Mailer: m, From: "billing@taskiem.test", PublicURL: "https://app.taskiem.test"}
	return &world{e: e, svc: svc, pay: f, clock: c, mail: m}
}

func (w *world) sub(t *testing.T, tenant uuid.UUID) *billing.Subscription {
	t.Helper()
	var s *billing.Subscription
	err := db.InTenantTx(ctx, w.e.DB.App, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		var err error
		_, s, err = w.svc.PlanOf(ctx, tx, tenant)
		return err
	})
	if err != nil || s == nil {
		t.Fatalf("subscription: %v %v", s, err)
	}
	return s
}

func (w *world) limits(t *testing.T, tenant uuid.UUID) runtime.Limits {
	t.Helper()
	w.e.Store.ForgetLimits(tenant)
	l, err := w.e.Store.LimitsFor(ctx, tenant)
	if err != nil {
		t.Fatal(err)
	}
	return l
}

func (w *world) invoices(t *testing.T, tenant uuid.UUID) []billing.Invoice {
	t.Helper()
	var out []billing.Invoice
	err := db.InTenantTx(ctx, w.e.DB.App, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		var err error
		out, err = billing.Invoices(ctx, tx, tenant, 50)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// checkoutAndPay subscribes the tenant to plan and pays by card through a
// signed webhook.
func (w *world) checkoutAndPay(t *testing.T, tenant uuid.UUID, plan string) billing.CheckoutResult {
	t.Helper()
	res, err := w.svc.Checkout(ctx, tenant, billing.CheckoutRequest{Plan: plan, Email: "finance@acme.test"}, "user:u1")
	if err != nil {
		t.Fatal(err)
	}
	w.pay.pay(res.Reference, res.Invoice.TotalKobo)
	body := chargeSuccess(res.Reference, res.Invoice.TotalKobo)
	if err := w.svc.HandleWebhook(ctx, "paystack", signed(body), body); err != nil {
		t.Fatal(err)
	}
	return res
}

func TestConfigValidation(t *testing.T) {
	c, err := billing.LoadConfig("../../deploy/plans.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if !c.PlaceholderPrices || c.VATBasisPoints() != 750 || c.TrialPlan != "growth" {
		t.Fatalf("config: %+v", c)
	}
	// Every plan's limits are known keys; the internal plan is built in.
	if p, ok := c.Plan(billing.InternalPlanID); !ok || !p.Has(billing.FeatureSSO) || len(p.Limits) != 0 {
		t.Fatalf("internal plan: %+v", p)
	}
	for _, bad := range []struct{ yaml, want string }{
		{"plans: [{id: a1, name: A, tier: starter, monthly_kobo: 1, annual_kobo: 1, limits: {max_widgets: 3}}]", "unknown limit"},
		{"plans: [{id: a1, name: A, tier: starter, monthly_kobo: 1, annual_kobo: 1, features: {teleport: true}}]", "unknown feature"},
		{"plans: [{id: a1, name: A, tier: platinum, monthly_kobo: 1, annual_kobo: 1}]", "tier"},
		{"plans: [{id: a1, name: A, tier: starter, monthly_kobo: -1, annual_kobo: 1}]", "negative"},
		{"plans: [{id: self_hosted, name: A, tier: starter, monthly_kobo: 1, annual_kobo: 1}]", "built in"},
		{"trial_plan: nope\nplans: [{id: a1, name: A, tier: starter, monthly_kobo: 1, annual_kobo: 1}]", "trial_plan"},
		{"vat_percent: 107\nplans: [{id: a1, name: A, tier: starter, monthly_kobo: 1, annual_kobo: 1}]", "vat_percent"},
		{"surprise: 1\nplans: []", "unknown field"},
	} {
		if _, err := billing.ParseConfig([]byte(bad.yaml)); err == nil || !strings.Contains(err.Error(), bad.want) {
			t.Errorf("%q: %v, want %q", bad.yaml, err, bad.want)
		}
	}
}

func TestMoney(t *testing.T) {
	// VAT 7.5%, half up to the kobo.
	if v := billing.VAT(6_000_000, 750); v != 450_000 {
		t.Errorf("VAT: %d", v)
	}
	if v := billing.VAT(1_001, 750); v != 75 { // 75.075
		t.Errorf("VAT rounding: %d", v)
	}
	if v := billing.VAT(1_002, 750); v != 75 { // 75.15
		t.Errorf("VAT rounding: %d", v)
	}
	if v := billing.VAT(1_000, 750); v != 75 {
		t.Errorf("VAT: %d", v)
	}
	if v := billing.VAT(1_007, 750); v != 76 { // 75.525
		t.Errorf("VAT half up: %d", v)
	}
	tot := billing.Compute([]billing.Line{{AmountKobo: 6_000_000}, {AmountKobo: -1_000_000}}, 750)
	if tot.Subtotal != 5_000_000 || tot.VAT != 375_000 || tot.Total != 5_375_000 || tot.CreditLeft != 0 {
		t.Errorf("totals: %+v", tot)
	}
	tot = billing.Compute([]billing.Line{{AmountKobo: 1_000}, {AmountKobo: -3_000}}, 750)
	if tot.Subtotal != 0 || tot.Total != 0 || tot.CreditLeft != 2_000 {
		t.Errorf("credit left: %+v", tot)
	}
	// Proration: half of a 30-day period unused is half the price.
	start := time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC)
	end := start.AddDate(0, 0, 30)
	if c := billing.Unused(6_000_000, start, end, start.AddDate(0, 0, 15)); c != 3_000_000 {
		t.Errorf("unused half: %d", c)
	}
	if c := billing.Unused(6_000_000, start, end, end.Add(time.Hour)); c != 0 {
		t.Errorf("unused after the end: %d", c)
	}
	if c := billing.Unused(6_000_000, start, end, start.Add(-time.Hour)); c != 6_000_000 {
		t.Errorf("unused before the start: %d", c)
	}
	// Periods: calendar months, clamped to the month's end.
	if e := billing.PeriodEnd(time.Date(2026, 1, 31, 10, 0, 0, 0, time.UTC), billing.IntervalMonthly); !e.Equal(time.Date(2026, 2, 28, 10, 0, 0, 0, time.UTC)) {
		t.Errorf("Jan 31 + 1 month: %v", e)
	}
	if e := billing.PeriodEnd(time.Date(2028, 2, 29, 0, 0, 0, 0, time.UTC), billing.IntervalAnnual); !e.Equal(time.Date(2029, 2, 28, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("leap day + 1 year: %v", e)
	}
	if s := billing.Naira(1_234_567_89); s != "₦1,234,567.89" {
		t.Errorf("naira: %s", s)
	}
}

// The state machine on a fake clock, without the database.
func TestDue(t *testing.T) {
	c := &billing.Config{GraceDays: 7, DunningDays: []int{0, 3, 5}}
	now := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	trialEnd := now.Add(14 * 24 * time.Hour)
	s := &billing.Subscription{Status: billing.StatusTrial, TrialEnd: &trialEnd, PeriodEnd: trialEnd}
	if a := s.Due(now, c); a != billing.ActNone {
		t.Fatalf("in trial: %q", a)
	}
	if a := s.Due(trialEnd, c); a != billing.ActRenew {
		t.Fatalf("trial over: %q", a)
	}
	s.CancelAtPeriodEnd = true
	if a := s.Due(trialEnd, c); a != billing.ActCancel {
		t.Fatalf("trial cancelled: %q", a)
	}
	s = &billing.Subscription{Status: billing.StatusActive, PeriodEnd: now}
	s.MarkPastDue(now)
	s.Schedule(c)
	if s.Status != billing.StatusPastDue || !s.NextActionAt.Equal(now) {
		t.Fatalf("past due: %+v", s)
	}
	for i, at := range []time.Time{now, now.AddDate(0, 0, 3), now.AddDate(0, 0, 5)} {
		if a := s.Due(at, c); a != billing.ActDun {
			t.Fatalf("dun %d: %q", i, a)
		}
		s.DunningAttempts++
		s.Schedule(c)
	}
	if a := s.Due(now.AddDate(0, 0, 6), c); a != billing.ActNone {
		t.Fatalf("between reminders: %q", a)
	}
	if a := s.Due(now.AddDate(0, 0, 7), c); a != billing.ActDegrade {
		t.Fatalf("grace over: %q", a)
	}
	s.Status = billing.StatusDegraded
	if !s.Refuses() || s.Due(now.AddDate(1, 0, 0), c) != billing.ActNone {
		t.Fatal("degraded waits for payment")
	}
	s.Activate(now, now.AddDate(0, 1, 0), billing.IntervalMonthly, now.AddDate(0, 2, 0))
	if s.Status != billing.StatusActive || !s.PeriodStart.Equal(now.AddDate(0, 2, 0)) || s.PastDueSince != nil {
		t.Fatalf("recovered long after: a fresh period from payment: %+v", s)
	}
}

func TestPlanLimitsAndOverrides(t *testing.T) {
	w := newWorld(t)
	tenant := w.e.Tenant
	// No subscription: the internal plan, the platform defaults.
	if l := w.limits(t, tenant); l.MaxRunningRuns != runtime.DefaultLimits().MaxRunningRuns || l.MaxRetentionDays != 0 {
		t.Fatalf("internal plan: %+v", l)
	}
	if err := w.svc.Grant(ctx, tenant, "starter", nil, "op"); err != nil {
		t.Fatal(err)
	}
	l := w.limits(t, tenant)
	if plan, status := l.Plan(); plan != "starter" || status != billing.StatusComped {
		t.Fatalf("plan: %s %s", plan, status)
	}
	if l.MaxRunningRuns != 5 || l.WorkerConcurrency != 5 || l.MaxRetentionDays != 7 || l.AIMonthlyTokens != 500_000 || l.WhatsAppTemplatesMonthly != 500 || l.MaxWorkflows != 20 {
		t.Fatalf("starter limits: %+v", l)
	}
	// Keys the plan leaves out keep the platform default.
	if l.MaxPayloadBytes != runtime.DefaultLimits().MaxPayloadBytes {
		t.Fatalf("default kept: %d", l.MaxPayloadBytes)
	}
	// Operator overrides still win over the plan.
	if err := w.e.Store.SetLimits(ctx, tenant, map[string]any{"max_running_runs": int64(9)}, "op"); err != nil {
		t.Fatal(err)
	}
	if l := w.limits(t, tenant); l.MaxRunningRuns != 9 || l.WorkerConcurrency != 5 {
		t.Fatalf("override over plan: %+v", l)
	}
	// Sub-tenants inherit the partner's plan (spec 13.1).
	sub := w.e.DB.SeedTenant(t, &tenant)
	if l := w.limits(t, sub.ID); l.WorkerConcurrency != 5 || l.MaxRunningRuns != 9 || l.MaxRetentionDays != 7 {
		t.Fatalf("sub-tenant: %+v", l)
	}
	if plan, _ := w.limits(t, sub.ID).Plan(); plan != "starter" {
		t.Fatalf("sub-tenant plan: %s", plan)
	}
	// Features follow the plan; a sub-tenant's are its partner's.
	var fe *billing.FeatureError
	if err := w.svc.Require(ctx, tenant, billing.FeatureSSO); !errors.As(err, &fe) || fe.Feature != "sso" {
		t.Fatalf("starter has no SSO: %v", err)
	}
	if err := w.svc.Require(ctx, sub.ID, billing.FeatureAI); err != nil {
		t.Fatalf("starter has AI: %v", err)
	}
	if err := w.svc.Grant(ctx, tenant, "enterprise", nil, "op"); err != nil {
		t.Fatal(err)
	}
	if err := w.svc.Require(ctx, sub.ID, billing.FeatureSSO); err != nil {
		t.Fatalf("enterprise has SSO: %v", err)
	}
	// Billing off: the internal plan whatever the database says.
	off := &billing.Service{Pool: w.e.DB.App, Store: w.e.Store}
	if err := off.Require(ctx, tenant, billing.FeatureBYOK); err != nil {
		t.Fatal(err)
	}
	w.e.Store.Billing = false
	if l := w.limits(t, tenant); l.MaxRunningRuns != 9 || l.WorkerConcurrency != runtime.DefaultLimits().WorkerConcurrency {
		t.Fatalf("billing off: defaults and overrides only: %+v", l)
	}
	// Grants are audited.
	var n int
	if err := w.e.DB.Admin.QueryRow(ctx, `SELECT count(*) FROM audit_log WHERE tenant_id = $1 AND action = 'billing.grant'`, tenant).Scan(&n); err != nil || n != 2 {
		t.Fatalf("grant audits: %d %v", n, err)
	}
}

const approvalFlow = `{"schema":"wd/v1","id":"wf_t","version":1,"name":"t","trigger":{"type":"manual"},
  "steps":[{"id":"ok","type":"approval","config":{"role":"officer"}}],"settings":{}}`

// trial -> past_due -> degraded -> recovered, on a fake clock; degraded
// refuses new runs but never blocks approvals of running ones.
func TestLifecycle(t *testing.T) {
	w := newWorld(t)
	tenant := w.e.Tenant
	if err := w.svc.Ensure(ctx, tenant, "test"); err != nil {
		t.Fatal(err)
	}
	s := w.sub(t, tenant)
	if s.Status != billing.StatusTrial || s.PlanID != "growth" {
		t.Fatalf("trial: %+v", s)
	}
	if l := w.limits(t, tenant); l.MaxRunningRuns != 25 {
		t.Fatalf("trial plan limits: %+v", l)
	}
	// Set the billing email the way a checkout would.
	if _, err := w.e.DB.Admin.Exec(ctx, `UPDATE subscriptions SET billing_email = 'finance@acme.test' WHERE tenant_id = $1`, tenant); err != nil {
		t.Fatal(err)
	}
	wf := w.e.Publish(t, approvalFlow)
	waiting := w.e.Start(t, wf, map[string]any{})

	// Trial over, no card: invoiced, past due, reminded once.
	w.clock.add(15 * 24 * time.Hour)
	if err := w.svc.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	s = w.sub(t, tenant)
	if s.Status != billing.StatusPastDue || s.DunningAttempts != 1 {
		t.Fatalf("after trial: %+v", s)
	}
	inv := w.invoices(t, tenant)
	if len(inv) != 1 || inv[0].Status != "open" || inv[0].Kind != "subscription" || inv[0].PlanID != "growth" {
		t.Fatalf("invoices: %+v", inv)
	}
	if inv[0].SubtotalKobo != 6_000_000 || inv[0].VATKobo != 450_000 || inv[0].TotalKobo != 6_450_000 {
		t.Fatalf("growth invoice with VAT: %+v", inv[0])
	}
	if w.mail.count("payment due for invoice "+inv[0].Number) != 1 {
		t.Fatalf("dunning email: %v", w.mail.sent)
	}
	// Past due still runs: grace period.
	if _, err := w.e.Store.Start(ctx, runtime.StartRequest{TenantID: tenant, WorkflowID: wf, Version: 1, Environment: "prod"}); err != nil {
		t.Fatalf("past due runs: %v", err)
	}
	// A second tick the same day sends nothing more.
	if err := w.svc.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	if w.mail.count("payment due") != 1 {
		t.Fatal("reminder repeated")
	}
	// Reminders on days 3 and 5, then degraded when the grace is over.
	for _, d := range []int{3, 2, 3} {
		w.clock.add(time.Duration(d) * 24 * time.Hour)
		if err := w.svc.Tick(ctx); err != nil {
			t.Fatal(err)
		}
	}
	s = w.sub(t, tenant)
	if s.Status != billing.StatusDegraded || s.DunningAttempts != 3 {
		t.Fatalf("degraded: %+v", s)
	}
	if w.mail.count("payment due") != 3 || w.mail.count("new runs paused") != 1 {
		t.Fatalf("emails: %d reminders, %d degraded", w.mail.count("payment due"), w.mail.count("new runs paused"))
	}
	w.e.Store.ForgetLimits(tenant)
	_, err := w.e.Store.Start(ctx, runtime.StartRequest{TenantID: tenant, WorkflowID: wf, Version: 1, Environment: "prod"})
	if le, ok := runtime.IsLimit(err); !ok || le.Code != "billing_degraded" {
		t.Fatalf("degraded start: %v", err)
	}
	// Running work continues: the approval is decided and the run ends.
	if err := w.e.Store.DecideApproval(ctx, waiting, "ok", "approved", "officer-1", "web"); err != nil {
		t.Fatalf("approval while degraded: %v", err)
	}
	w.e.Drain(t)
	if st := w.e.Status(t, waiting); st != "completed" {
		t.Fatalf("running run while degraded: %s", st)
	}
	ov, err := w.svc.Overview(ctx, tenant)
	if err != nil {
		t.Fatal(err)
	}
	if ov.Banner == nil || ov.Banner.Level != "error" || ov.OpenInvoice == nil {
		t.Fatalf("overview: %+v", ov)
	}

	// Paid: recovered at once, a fresh period from the payment.
	res, err := w.svc.Checkout(ctx, tenant, billing.CheckoutRequest{}, "user:u1")
	if err != nil {
		t.Fatal(err)
	}
	if res.Invoice.ID != inv[0].ID || !strings.HasPrefix(res.CheckoutURL, "https://checkout.paystack.test/") {
		t.Fatalf("pays the open invoice: %+v", res)
	}
	w.pay.pay(res.Reference, res.Invoice.TotalKobo)
	body := chargeSuccess(res.Reference, res.Invoice.TotalKobo)
	if err := w.svc.HandleWebhook(ctx, "paystack", signed(body), body); err != nil {
		t.Fatal(err)
	}
	s = w.sub(t, tenant)
	// The invoice's period still runs: it is the subscription's period.
	if s.Status != billing.StatusActive || s.PastDueSince != nil || s.CardLast4 != "4081" || !s.PeriodStart.Equal(inv[0].PeriodStart) {
		t.Fatalf("recovered: %+v", s)
	}
	w.e.Store.ForgetLimits(tenant)
	if _, err := w.e.Store.Start(ctx, runtime.StartRequest{TenantID: tenant, WorkflowID: wf, Version: 1, Environment: "prod"}); err != nil {
		t.Fatalf("recovered start: %v", err)
	}
	if w.mail.count("service restored") != 1 {
		t.Fatal("recovery email")
	}

	// Next period: renewed with the saved card, no email.
	w.clock.add(32 * 24 * time.Hour)
	if err := w.svc.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	s = w.sub(t, tenant)
	if s.Status != billing.StatusActive || w.pay.calls["charge"] != 1 {
		t.Fatalf("renewal by card: %+v, %d charges", s, w.pay.calls["charge"])
	}
	inv = w.invoices(t, tenant)
	if len(inv) != 2 || inv[0].Kind != "renewal" || inv[0].Status != "paid" || inv[1].Status != "paid" {
		t.Fatalf("renewal invoice: %+v", inv)
	}
	if !strings.HasSuffix(inv[1].Number, "-000001") || !strings.HasSuffix(inv[0].Number, "-000002") {
		t.Fatalf("numbering: %s %s", inv[1].Number, inv[0].Number)
	}

	// A declined renewal falls past due again.
	w.pay.charge = "failed"
	w.clock.add(32 * 24 * time.Hour)
	if err := w.svc.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	if s = w.sub(t, tenant); s.Status != billing.StatusPastDue {
		t.Fatalf("declined renewal: %+v", s)
	}
}

func TestWebhooks(t *testing.T) {
	w := newWorld(t)
	tenant := w.e.Tenant
	if err := w.svc.Ensure(ctx, tenant, "test"); err != nil {
		t.Fatal(err)
	}
	res, err := w.svc.Checkout(ctx, tenant, billing.CheckoutRequest{Plan: "starter", Email: "finance@acme.test"}, "user:u1")
	if err != nil {
		t.Fatal(err)
	}
	if res.Invoice.Kind != "subscription" || res.Invoice.TotalKobo != 1_612_500 { // 15,000 + 7.5% VAT
		t.Fatalf("checkout invoice: %+v", res.Invoice)
	}
	if got, ok := billing.TenantOfReference(res.Reference); !ok || got != tenant {
		t.Fatalf("reference %s", res.Reference)
	}
	w.pay.pay(res.Reference, res.Invoice.TotalKobo)
	body := chargeSuccess(res.Reference, res.Invoice.TotalKobo)

	// Bad signature: refused, nothing changes.
	bad := signed(body)
	bad.Set("x-paystack-signature", strings.Repeat("0", 128))
	if err := w.svc.HandleWebhook(ctx, "paystack", bad, body); !errors.Is(err, billing.ErrBadSignature) {
		t.Fatalf("bad signature: %v", err)
	}
	if err := w.svc.HandleWebhook(ctx, "paystack", http.Header{}, body); !errors.Is(err, billing.ErrBadSignature) {
		t.Fatalf("unsigned: %v", err)
	}
	if s := w.sub(t, tenant); s.Status != billing.StatusTrial {
		t.Fatalf("after bad webhooks: %s", s.Status)
	}
	// Valid: paid, active on starter.
	if err := w.svc.HandleWebhook(ctx, "paystack", signed(body), body); err != nil {
		t.Fatal(err)
	}
	s := w.sub(t, tenant)
	if s.Status != billing.StatusActive || s.PlanID != "starter" || s.TrialEnd != nil {
		t.Fatalf("paid: %+v", s)
	}
	// Duplicate delivery: a no-op (verified once, paid once).
	verifies := w.pay.calls["verify"]
	if err := w.svc.HandleWebhook(ctx, "paystack", signed(body), body); err != nil {
		t.Fatal(err)
	}
	if w.pay.calls["verify"] != verifies {
		t.Fatal("duplicate webhook re-verified")
	}
	var paid int
	if err := w.e.DB.Admin.QueryRow(ctx, `SELECT count(*) FROM audit_log WHERE tenant_id = $1 AND action = 'billing.invoice.paid'`, tenant).Scan(&paid); err != nil || paid != 1 {
		t.Fatalf("paid audits: %d %v", paid, err)
	}
	// Other events and references not ours are acknowledged and ignored.
	other, _ := json.Marshal(map[string]any{"event": "transfer.success", "data": map[string]any{"reference": res.Reference}})
	if err := w.svc.HandleWebhook(ctx, "paystack", signed(other), other); err != nil {
		t.Fatal(err)
	}
	foreign := chargeSuccess("order-123", 500)
	if err := w.svc.HandleWebhook(ctx, "paystack", signed(foreign), foreign); err != nil {
		t.Fatal(err)
	}

	// Amount mismatch: refused, the invoice stays open.
	t2 := w.e.AddTenant(t)
	res2, err := w.svc.Checkout(ctx, t2, billing.CheckoutRequest{Plan: "growth", Email: "b@acme.test"}, "user:u2")
	if err != nil {
		t.Fatal(err)
	}
	w.pay.pay(res2.Reference, 100)                                 // ₦1 paid against ₦64,500
	body2 := chargeSuccess(res2.Reference, res2.Invoice.TotalKobo) // the body claims the full amount: verification is what counts
	if err := w.svc.HandleWebhook(ctx, "paystack", signed(body2), body2); err != nil {
		t.Fatalf("mismatch is acknowledged: %v", err)
	}
	if s := w.sub(t, t2); s.Status != billing.StatusTrial {
		t.Fatalf("mismatch must not activate: %s", s.Status)
	}
	if inv := w.invoices(t, t2); inv[0].Status != "open" {
		t.Fatalf("mismatch invoice: %s", inv[0].Status)
	}
	var st string
	if err := w.e.DB.Admin.QueryRow(ctx, `SELECT status FROM billing_payments WHERE reference = $1`, res2.Reference).Scan(&st); err != nil || st != "mismatch" {
		t.Fatalf("payment: %s %v", st, err)
	}
}

func TestReconcile(t *testing.T) {
	w := newWorld(t)
	tenant := w.e.Tenant
	res, err := w.svc.Checkout(ctx, tenant, billing.CheckoutRequest{Plan: "growth", Email: "finance@acme.test", Channels: []string{"bank_transfer"}}, "user:u1")
	if err != nil {
		t.Fatal(err)
	}
	// Too recent: left for the webhook.
	if err := w.svc.Reconcile(ctx, 2*time.Minute); err != nil {
		t.Fatal(err)
	}
	if w.pay.calls["verify"] != 0 {
		t.Fatal("verified too early")
	}
	// Paid by transfer, webhook lost: reconciliation settles it.
	w.pay.pay(res.Reference, res.Invoice.TotalKobo)
	w.clock.add(5 * time.Minute)
	if err := w.svc.Reconcile(ctx, 2*time.Minute); err != nil {
		t.Fatal(err)
	}
	if s := w.sub(t, tenant); s.Status != billing.StatusActive {
		t.Fatalf("reconciled: %+v", s)
	}
	// A checkout never completed is abandoned after a day.
	t2 := w.e.AddTenant(t)
	res2, err := w.svc.Checkout(ctx, t2, billing.CheckoutRequest{Plan: "starter", Email: "x@acme.test"}, "user:u2")
	if err != nil {
		t.Fatal(err)
	}
	w.clock.add(25 * time.Hour)
	if err := w.svc.Reconcile(ctx, 2*time.Minute); err != nil {
		t.Fatal(err)
	}
	var st string
	if err := w.e.DB.Admin.QueryRow(ctx, `SELECT status FROM billing_payments WHERE reference = $1`, res2.Reference).Scan(&st); err != nil || st != "abandoned" {
		t.Fatalf("abandoned: %s %v", st, err)
	}
}

func TestPlanChanges(t *testing.T) {
	w := newWorld(t)
	tenant := w.e.Tenant
	w.checkoutAndPay(t, tenant, "growth")
	start := w.sub(t, tenant)

	// Upgrade halfway through: a new period at the business price, less
	// the unused half of growth; the saved card pays it at once.
	w.clock.add(start.PeriodEnd.Sub(start.PeriodStart) / 2)
	res, err := w.svc.ChangePlan(ctx, tenant, "business", "monthly", "user:u1")
	if err != nil {
		t.Fatal(err)
	}
	if res.Effective != "now" || res.Invoice == nil || res.Invoice.Status != "paid" {
		t.Fatalf("upgrade: %+v", res)
	}
	credit := billing.Unused(6_000_000, start.PeriodStart, start.PeriodEnd, w.clock.Now())
	if credit < 2_900_000 || credit > 3_100_000 {
		t.Fatalf("credit: %d", credit)
	}
	lines := res.Invoice.Lines
	if len(lines) != 2 || lines[0].AmountKobo != 25_000_000 || lines[1].Kind != "proration_credit" || lines[1].AmountKobo != -credit {
		t.Fatalf("upgrade lines: %+v", lines)
	}
	if res.Invoice.SubtotalKobo != 25_000_000-credit || res.Invoice.VATKobo != billing.VAT(25_000_000-credit, 750) {
		t.Fatalf("upgrade totals: %+v", res.Invoice)
	}
	s := w.sub(t, tenant)
	if s.PlanID != "business" || !s.PeriodStart.Equal(w.clock.Now()) {
		t.Fatalf("after upgrade: %+v", s)
	}
	if l := w.limits(t, tenant); l.MaxRunningRuns != 100 {
		t.Fatalf("business limits: %d", l.MaxRunningRuns)
	}

	// Downgrade refused while usage exceeds starter: 21 more workflows and
	// a custom role.
	err = db.InTenantTx(ctx, w.e.DB.App, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		for range 21 {
			if _, err := tx.Exec(ctx, `INSERT INTO workflows (id, tenant_id, name, created_by) VALUES ($1, $2, 'x', $2)`, uuid.Must(uuid.NewV7()), tenant); err != nil {
				return err
			}
		}
		_, err := tx.Exec(ctx, `INSERT INTO roles (tenant_id, name, permissions, created_by) VALUES ($1, 'clerk', '{run.read}', 'u')`, tenant)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = w.svc.ChangePlan(ctx, tenant, "starter", "monthly", "user:u1")
	var de *billing.DowngradeError
	if !errors.As(err, &de) {
		t.Fatalf("downgrade: %v", err)
	}
	got := map[string]billing.Blocker{}
	for _, b := range de.Blockers {
		got[b.Limit] = b
	}
	if b := got["max_workflows"]; b.Allowed != 20 || b.Used < 21 || !strings.Contains(b.Remove, "workflows") {
		t.Fatalf("workflow blocker: %+v", de.Blockers)
	}
	if _, ok := got["feature:sso"]; ok {
		t.Fatalf("no SSO in use: %+v", de.Blockers)
	}
	if b := got["feature:custom_roles"]; b.Used != 1 {
		t.Fatalf("custom roles blocker: %+v", de.Blockers)
	}

	// To growth (100 workflows; custom roles: none in growth either) once the role is gone.
	if _, err := w.e.DB.Admin.Exec(ctx, `DELETE FROM roles WHERE tenant_id = $1`, tenant); err != nil {
		t.Fatal(err)
	}
	res, err = w.svc.ChangePlan(ctx, tenant, "growth", "monthly", "user:u1")
	if err != nil {
		t.Fatal(err)
	}
	if res.Effective != "period_end" || res.Subscription.PendingPlanID != "growth" {
		t.Fatalf("downgrade scheduled: %+v", res)
	}
	if l := w.limits(t, tenant); l.MaxRunningRuns != 100 {
		t.Fatal("downgrade applied early")
	}
	// At period end the renewal is on growth.
	w.clock.t = res.Subscription.PeriodEnd.Add(time.Minute).Truncate(time.Microsecond)
	if err := w.svc.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	s = w.sub(t, tenant)
	if s.PlanID != "growth" || s.PendingPlanID != "" || s.Status != billing.StatusActive {
		t.Fatalf("after period end: %+v", s)
	}
	if inv := w.invoices(t, tenant); inv[0].PlanID != "growth" || inv[0].Lines[0].AmountKobo != 6_000_000 {
		t.Fatalf("renewal on growth: %+v", inv[0])
	}

	// Cancel at period end, resume, cancel again: ended at period end.
	if _, err := w.svc.Cancel(ctx, tenant, false, "user:u1"); err != nil {
		t.Fatal(err)
	}
	if _, err := w.svc.Cancel(ctx, tenant, true, "user:u1"); err != nil {
		t.Fatal(err)
	}
	if s := w.sub(t, tenant); s.CancelAtPeriodEnd {
		t.Fatal("resumed")
	}
	if _, err := w.svc.Cancel(ctx, tenant, false, "user:u1"); err != nil {
		t.Fatal(err)
	}
	w.clock.t = w.sub(t, tenant).PeriodEnd.Add(time.Minute)
	if err := w.svc.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	if s := w.sub(t, tenant); s.Status != billing.StatusCancelled {
		t.Fatalf("cancelled: %+v", s)
	}
	if !w.limits(t, tenant).BillingRefusesRuns() {
		t.Fatal("a cancelled subscription refuses new runs")
	}
	// Comped tenants change plans through the operator only.
	if err := w.svc.Grant(ctx, tenant, "business", nil, "op"); err != nil {
		t.Fatal(err)
	}
	if _, err := w.svc.ChangePlan(ctx, tenant, "starter", "monthly", "user:u1"); !errors.Is(err, billing.ErrConflict) {
		t.Fatalf("comped change: %v", err)
	}
}

func TestInvoicesAreImmutable(t *testing.T) {
	w := newWorld(t)
	tenant := w.e.Tenant
	res, err := w.svc.Checkout(ctx, tenant, billing.CheckoutRequest{Plan: "starter", Email: "f@acme.test"}, "user:u1")
	if err != nil {
		t.Fatal(err)
	}
	id := res.Invoice.ID
	// Not even the database superuser changes an issued invoice's amounts
	// or lines, or deletes it.
	for _, q := range []string{
		`UPDATE invoices SET total_kobo = 1, subtotal_kobo = 1, vat_kobo = 0 WHERE id = $1`,
		`UPDATE invoices SET lines = '[]' WHERE id = $1`,
		`UPDATE invoices SET number = 'TKM-1999-000001' WHERE id = $1`,
		`DELETE FROM invoices WHERE id = $1`,
	} {
		if _, err := w.e.DB.Admin.Exec(ctx, q, id); err == nil {
			t.Errorf("%s: allowed", q)
		}
	}
	// Status moves forward only.
	if _, err := w.e.DB.Admin.Exec(ctx, `UPDATE invoices SET status = 'void', closed_reason = 'test' WHERE id = $1`, id); err != nil {
		t.Fatal(err)
	}
	if _, err := w.e.DB.Admin.Exec(ctx, `UPDATE invoices SET status = 'open' WHERE id = $1`, id); err == nil {
		t.Error("void reopened")
	}
	// Numbers are gapless across tenants within a year.
	t2 := w.e.AddTenant(t)
	res2, err := w.svc.Checkout(ctx, t2, billing.CheckoutRequest{Plan: "starter", Email: "g@acme.test"}, "user:u2")
	if err != nil {
		t.Fatal(err)
	}
	year := w.clock.Now().Format("2006")
	if res.Invoice.Number != "TKM-"+year+"-000001" || res2.Invoice.Number != "TKM-"+year+"-000002" {
		t.Fatalf("numbers: %s %s", res.Invoice.Number, res2.Invoice.Number)
	}
	// The app role cannot reach the counter.
	err = db.InTenantTx(ctx, w.e.DB.App, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `UPDATE billing_invoice_counters SET last = 0`)
		return err
	})
	if err == nil {
		t.Error("app role reset the invoice counter")
	}
}

func TestWhatsAppOverageInvoiced(t *testing.T) {
	w := newWorld(t)
	tenant := w.e.Tenant
	w.checkoutAndPay(t, tenant, "starter")
	// Last month: 30 marketing and 10 utility templates beyond the allowance.
	last := time.Date(w.clock.Now().Year(), w.clock.Now().Month(), 1, 0, 0, 0, 0, time.UTC)
	if _, err := w.e.DB.Admin.Exec(ctx, `INSERT INTO whatsapp_template_usage (tenant_id, month, category, sent, over_allowance)
		VALUES ($1, $2, 'marketing', 530, 30), ($1, $2, 'utility', 10, 10)`, tenant, last); err != nil {
		t.Fatal(err)
	}
	w.clock.t = w.sub(t, tenant).PeriodEnd.Add(time.Minute)
	if err := w.svc.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	inv := w.invoices(t, tenant)[0]
	var wa int64
	for _, l := range inv.Lines {
		if l.Kind == "whatsapp_overage" {
			wa += l.AmountKobo
		}
	}
	if wa != 30*8000+10*1100 || inv.SubtotalKobo != 1_500_000+wa || inv.VATKobo != billing.VAT(inv.SubtotalKobo, 750) {
		t.Fatalf("overage lines: %+v", inv)
	}
	// Billed once.
	w.clock.t = w.sub(t, tenant).PeriodEnd.Add(time.Minute)
	if err := w.svc.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	for _, l := range w.invoices(t, tenant)[0].Lines {
		if l.Kind == "whatsapp_overage" {
			t.Fatalf("overage billed twice: %+v", l)
		}
	}
}

func TestSnapshotsAndPartnerAggregate(t *testing.T) {
	w := newWorld(t)
	partner := w.e.Tenant
	if err := w.svc.Grant(ctx, partner, "business", nil, "op"); err != nil {
		t.Fatal(err)
	}
	a, b := w.e.DB.SeedTenant(t, &partner), w.e.DB.SeedTenant(t, &partner)
	w.e.DB.StartRun(t, a)
	w.e.DB.StartRun(t, b)
	for _, sub := range []uuid.UUID{a.ID, b.ID} {
		if _, err := w.e.DB.Admin.Exec(ctx, `INSERT INTO tenant_usage (tenant_id, day, runs_started) VALUES ($1, (now() AT TIME ZONE 'UTC')::date, 3)`, sub); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.svc.SnapshotAll(ctx); err != nil {
		t.Fatal(err)
	}
	err := db.InTenantTx(ctx, w.e.DB.App, []uuid.UUID{partner}, func(tx pgx.Tx) error {
		agg, err := billing.PartnerAggregate(ctx, tx, partner, w.clock.Now().AddDate(0, 0, -1), w.clock.Now())
		if err != nil {
			return err
		}
		if len(agg) != 1 || agg[0].SubTenants != 2 || agg[0].RunsStarted != 6 || agg[0].StoredRuns != 2 {
			t.Fatalf("aggregate: %+v", agg)
		}
		// The partner sees no sub-tenant's own snapshot.
		var n int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM usage_snapshots WHERE tenant_id <> $1`, partner).Scan(&n); err != nil || n != 0 {
			t.Fatalf("leak: %d %v", n, err)
		}
		snaps, err := billing.Snapshots(ctx, tx, partner, 7)
		if err != nil {
			return err
		}
		if len(snaps) != 1 || snaps[0].Plan != "business" || snaps[0].Limits["max_running_runs"] != float64(100) {
			t.Fatalf("own snapshot: %+v", snaps)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	// Another tenant cannot read the partner's aggregate.
	other := w.e.AddTenant(t)
	err = db.InTenantTx(ctx, w.e.DB.App, []uuid.UUID{other}, func(tx pgx.Tx) error {
		agg, err := billing.PartnerAggregate(ctx, tx, partner, w.clock.Now().AddDate(0, 0, -1), w.clock.Now())
		if len(agg) != 0 {
			t.Fatalf("foreign aggregate: %+v", agg)
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestRetentionCappedByPlan(t *testing.T) {
	w := newWorld(t)
	tenant := w.e.Tenant
	if err := w.svc.Grant(ctx, tenant, "starter", nil, "op"); err != nil {
		t.Fatal(err)
	}
	// The workflow asks for a year; starter keeps 7 days.
	wf := w.e.Publish(t, `{"schema":"wd/v1","id":"wf_r","version":1,"name":"r","trigger":{"type":"manual"},
	  "steps":[{"id":"t","type":"transform","config":{"output":{"x":1}}}],"settings":{"retention":"365d"}}`)
	ref := w.e.Start(t, wf, map[string]any{})
	w.e.Drain(t)
	var days float64
	if err := w.e.DB.Admin.QueryRow(ctx, `SELECT extract(epoch FROM retain_until - ended_at) / 86400 FROM runs WHERE id = $1`, ref.ID).Scan(&days); err != nil {
		t.Fatal(err)
	}
	if days < 6.99 || days > 7.01 {
		t.Fatalf("retention: %.2f days", days)
	}
}

// TestInvoicePaidTwice: two payments of one invoice both complete (two
// checkouts in two tabs). The second is recorded for a refund and changes
// nothing: it must not rewind a change scheduled after the first (security
// review 2026-10-08, R1).
func TestInvoicePaidTwice(t *testing.T) {
	w := newWorld(t)
	tenant := w.e.Tenant
	if err := w.svc.Ensure(ctx, tenant, "test"); err != nil {
		t.Fatal(err)
	}
	if _, err := w.e.DB.Admin.Exec(ctx, `UPDATE subscriptions SET billing_email = 'finance@acme.test' WHERE tenant_id = $1`, tenant); err != nil {
		t.Fatal(err)
	}
	w.clock.add(15 * 24 * time.Hour) // trial over, no card: past due
	if err := w.svc.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	a, err := w.svc.Checkout(ctx, tenant, billing.CheckoutRequest{}, "user:u1")
	if err != nil {
		t.Fatal(err)
	}
	b, err := w.svc.Checkout(ctx, tenant, billing.CheckoutRequest{}, "user:u1")
	if err != nil {
		t.Fatal(err)
	}
	if a.Invoice.ID != b.Invoice.ID || a.Reference == b.Reference {
		t.Fatalf("two checkouts of one invoice: %s %s", a.Reference, b.Reference)
	}
	pay := func(res billing.CheckoutResult) {
		t.Helper()
		w.pay.pay(res.Reference, res.Invoice.TotalKobo)
		body := chargeSuccess(res.Reference, res.Invoice.TotalKobo)
		if err := w.svc.HandleWebhook(ctx, "paystack", signed(body), body); err != nil {
			t.Fatal(err)
		}
	}
	pay(a)
	if s := w.sub(t, tenant); s.Status != billing.StatusActive || s.PlanID != "growth" {
		t.Fatalf("first payment: %+v", s)
	}
	// A downgrade scheduled for the period's end.
	if res, err := w.svc.ChangePlan(ctx, tenant, "starter", "", "user:u1"); err != nil || res.Effective != "period_end" {
		t.Fatalf("downgrade: %+v %v", res, err)
	}
	before := w.sub(t, tenant)
	pay(b) // the second tab's payment completes
	after := w.sub(t, tenant)
	if after.PendingPlanID != "starter" || after.PlanID != before.PlanID || !after.PeriodEnd.Equal(before.PeriodEnd) || after.CreditKobo != before.CreditKobo {
		t.Fatalf("second payment changed the subscription: before %+v, after %+v", before, after)
	}
	var paid, unapplied int
	if err := w.e.DB.Admin.QueryRow(ctx, `SELECT count(*) FILTER (WHERE action = 'billing.invoice.paid'),
		count(*) FILTER (WHERE action = 'billing.payment.unapplied' AND target = $2 AND detail->>'reason' = 'already_paid')
		FROM audit_log WHERE tenant_id = $1`, tenant, b.Reference).Scan(&paid, &unapplied); err != nil {
		t.Fatal(err)
	}
	if paid != 1 || unapplied != 1 {
		t.Fatalf("audits: %d paid, %d unapplied (want 1 and 1)", paid, unapplied)
	}
	var st, ref string
	if err := w.e.DB.Admin.QueryRow(ctx, `SELECT p.status, i.paid_reference FROM billing_payments p JOIN invoices i ON i.id = p.invoice_id WHERE p.reference = $1`,
		b.Reference).Scan(&st, &ref); err != nil || st != "success" || ref != a.Reference {
		t.Fatalf("second payment %s, invoice paid by %s (%v)", st, ref, err)
	}
}

// TestDunningWaitsForPendingCharge: a renewal charge the bank has not
// answered yet is not charged again by the next reminder (R1).
func TestDunningWaitsForPendingCharge(t *testing.T) {
	w := newWorld(t)
	tenant := w.e.Tenant
	w.checkoutAndPay(t, tenant, "growth") // saves the card
	w.pay.charge = "send_otp"             // the renewal charge waits on the bank
	w.clock.add(32 * 24 * time.Hour)
	if err := w.svc.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	if s := w.sub(t, tenant); s.Status != billing.StatusPastDue || w.pay.calls["charge"] != 1 {
		t.Fatalf("renewal pending: %+v, %d charges", s, w.pay.calls["charge"])
	}
	for _, d := range []int{1, 3} {
		w.clock.add(time.Duration(d) * 24 * time.Hour)
		if err := w.svc.Tick(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if n := w.pay.calls["charge"]; n != 1 {
		t.Fatalf("the card was charged %d times while the first charge was pending", n)
	}
	if w.mail.count("payment due") == 0 {
		t.Fatal("no reminder while the charge was pending")
	}
}
