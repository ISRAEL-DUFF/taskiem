package lookback_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/connector"
	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/effects"
	"github.com/israel-duff/taskiem/engine/invoicing"
	"github.com/israel-duff/taskiem/engine/lookback"
	"github.com/israel-duff/taskiem/engine/moneylog"
	rt "github.com/israel-duff/taskiem/engine/runtime/runtimetest"
)

var ctx = context.Background()

var (
	paystack = invoicing.Rail{Connector: "paystack", Connection: "main", Name: "Paystack", Events: map[string]bool{"virtual_account.credited": true},
		Caps: map[string]connector.CapabilityMap{"list_credits": {Unsupported: []string{"account"}}}}
	iswallet = invoicing.Rail{Connector: "iswallet", Connection: "main", Name: "iswallet", Events: map[string]bool{},
		Caps: map[string]connector.CapabilityMap{"list_credits": {Requires: []string{"account"}}}}
)

// fakeRails lists the credits it is given, for any window.
type fakeRails struct {
	credits   []any
	err       error
	truncated bool
	calls     []map[string]any
}

func (f *fakeRails) Capable(context.Context, uuid.UUID, string) ([]invoicing.Rail, error) {
	return []invoicing.Rail{paystack, iswallet}, nil
}

func (f *fakeRails) ResolveChannel(_ context.Context, _ uuid.UUID, channel, _ string) (invoicing.Rail, error) {
	for _, r := range []invoicing.Rail{paystack, iswallet} {
		if r.Channel() == channel {
			return r, nil
		}
	}
	return invoicing.Rail{}, invoicing.ErrNoRail
}

func (f *fakeRails) Call(_ context.Context, _ uuid.UUID, _ invoicing.Rail, _ string, in map[string]any, _ effects.KeyInput) (map[string]any, error) {
	f.calls = append(f.calls, in)
	if f.err != nil {
		return nil, f.err
	}
	// Truncated only for the whole window: its quarters fit.
	since, _ := time.Parse(time.RFC3339, in["since"].(string))
	until, _ := time.Parse(time.RFC3339, in["until"].(string))
	if f.truncated && until.Sub(since) > 13*time.Hour {
		return map[string]any{"credits": f.credits[:1], "truncated": true}, nil
	}
	return map[string]any{"credits": f.credits}, nil
}

type world struct {
	t     *testing.T
	env   *rt.Env
	rails *fakeRails
	l     *lookback.Looker
	now   time.Time
}

func newWorld(t *testing.T) *world {
	env := rt.New(t)
	w := &world{t: t, env: env, rails: &fakeRails{}, now: time.Now().UTC().Truncate(time.Second)}
	w.l = &lookback.Looker{Pool: env.DB.App, Rails: w.rails, Log: &moneylog.Log{Pool: env.DB.App, PII: env.Vault}, Now: func() time.Time { return w.now }}
	return w
}

func (w *world) in(fn func(pgx.Tx) error) {
	w.t.Helper()
	if err := db.InTenantTx(ctx, w.env.DB.App, []uuid.UUID{w.env.Tenant}, fn); err != nil {
		w.t.Fatal(err)
	}
}

func (w *world) target(channel, account string) lookback.Target {
	w.in(func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO money_lookbacks (tenant_id, channel, account) VALUES ($1, $2, $3)`, w.env.Tenant, channel, account)
		return err
	})
	return lookback.Target{Tenant: w.env.Tenant, Channel: channel, Account: account}
}

func (w *world) count(q string, args ...any) int {
	w.t.Helper()
	var n int
	if err := w.env.DB.Admin.QueryRow(ctx, q, args...).Scan(&n); err != nil {
		w.t.Fatal(err)
	}
	return n
}

func credit(id string, ago time.Duration, now time.Time) map[string]any {
	return map[string]any{"reference": "CUS_1", "credit_id": id, "amount_kobo": int64(2500000), "account_number": "9912345678",
		"payer_name": "EZE NGOZI", "occurred_at": now.Add(-ago).Format(time.RFC3339)}
}

// The look-back records credits the log did not have, once, with the key
// the webhook's would have: a credit heard both ways is one entry. A
// credit the webhook should have brought is told to the owners once a day.
func TestLookBackRecordsOnceAndTellsOfMissedWebhooks(t *testing.T) {
	w := newWorld(t)
	tg := w.target("paystack/main", "")
	// Heard by webhook already.
	heard, _ := moneylog.FromEvent("paystack/main", "virtual_account.credited", credit("T1", 3*time.Hour, w.now))
	if _, _, err := w.l.Log.Record(ctx, w.env.Tenant, heard); err != nil {
		t.Fatal(err)
	}
	w.rails.credits = []any{credit("T1", 3*time.Hour, w.now), credit("T2", 2*time.Hour, w.now), credit("T3", 5*time.Minute, w.now)}
	res, err := w.l.Look(ctx, tg)
	if err != nil || !res.Ran || res.Read != 3 || res.Found != 2 || res.Missed != 1 {
		t.Fatalf("look %+v %v", res, err)
	}
	if n := w.count(`SELECT count(*) FROM money_entries WHERE tenant_id = $1 AND kind = 'account_credit'`, w.env.Tenant); n != 3 {
		t.Fatalf("%d entries", n)
	}
	if n := w.count(`SELECT count(*) FROM money_entries WHERE tenant_id = $1 AND source = 'lookup' AND credit_id = 'T2'`, w.env.Tenant); n != 1 {
		t.Errorf("T2 recorded %d times by the look-back", n)
	}
	if n := w.count(`SELECT count(*) FROM alerts WHERE tenant_id = $1 AND kind = $2`, w.env.Tenant, lookback.AlertWebhookMissed); n != 1 {
		t.Fatalf("%d missed alerts", n)
	}
	// Not due again for 15 minutes.
	if res, err := w.l.Look(ctx, tg); err != nil || res.Ran {
		t.Fatalf("again at once %+v %v", res, err)
	}
	// Later the same day, a new missed credit: read, recorded, not told twice.
	w.now = w.now.Add(lookback.Every)
	w.rails.credits = append(w.rails.credits, credit("T4", 2*time.Hour, w.now))
	if res, err := w.l.Look(ctx, tg); err != nil || res.Found != 1 || res.Missed != 1 {
		t.Fatalf("later %+v %v", res, err)
	}
	if n := w.count(`SELECT count(*) FROM alerts WHERE tenant_id = $1 AND kind = $2`, w.env.Tenant, lookback.AlertWebhookMissed); n != 1 {
		t.Fatalf("%d missed alerts", n)
	}
	// Then the webhook delivers T4: the same entry.
	late, _ := moneylog.FromEvent("paystack/main", "virtual_account.credited", credit("T4", 2*time.Hour, w.now))
	if _, fresh, err := w.l.Log.Record(ctx, w.env.Tenant, late); err != nil || fresh {
		t.Fatalf("the webhook's T4 was new: %v", err)
	}
}

// Tick reads back each connection that is due and still active in prod.
func TestTickReadsActiveConnections(t *testing.T) {
	w := newWorld(t)
	w.target("paystack/main", "")
	w.target("iswallet/main", "wal_1") // not connected: left alone
	w.in(func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO connections (id, tenant_id, environment, connector, name, auth_type) VALUES ($1, $2, 'prod', 'paystack', 'main', 'none')`, uuid.New(), w.env.Tenant)
		return err
	})
	w.rails.credits = []any{credit("T1", time.Minute, w.now)}
	if err := w.l.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	if len(w.rails.calls) != 1 || w.count(`SELECT count(*) FROM money_entries WHERE tenant_id = $1`, w.env.Tenant) != 1 {
		t.Fatalf("%d calls", len(w.rails.calls))
	}
	// Not due again yet.
	if err := w.l.Tick(ctx); err != nil || len(w.rails.calls) != 1 {
		t.Fatalf("again: %v, %d calls", err, len(w.rails.calls))
	}
}

// iswallet maps no credit webhook: what the look-back finds is not a
// missed webhook; it needs the wallet to read.
func TestLookBackWithoutWebhook(t *testing.T) {
	w := newWorld(t)
	if res, err := w.l.Look(ctx, w.target("iswallet/main", "")); err != nil || res.Ran {
		t.Fatalf("no wallet %+v %v", res, err)
	}
	tg := w.target("iswallet/main", "wal_1")
	w.rails.credits = []any{credit("P1", 3*time.Hour, w.now)}
	res, err := w.l.Look(ctx, tg)
	if err != nil || res.Found != 1 || res.Missed != 0 || w.rails.calls[0]["account"] != "wal_1" {
		t.Fatalf("look %+v %v %v", res, err, w.rails.calls)
	}
	if n := w.count(`SELECT count(*) FROM alerts WHERE tenant_id = $1`, w.env.Tenant); n != 0 {
		t.Errorf("%d alerts", n)
	}
}

// A rail with more than one read holds is read again in quarters.
func TestLookBackReadsShorterWindows(t *testing.T) {
	w := newWorld(t)
	w.rails.truncated = true
	w.rails.credits = []any{credit("A", time.Hour, w.now), credit("B", 30*time.Hour, w.now)}
	res, err := w.l.Look(ctx, w.target("paystack/main", ""))
	if err != nil || len(w.rails.calls) != 5 || res.Found != 2 {
		t.Fatalf("look %+v %v (%d calls)", res, err, len(w.rails.calls))
	}
}

// Failing three times in a row raises an alert, once a day; a success
// clears the count.
func TestLookBackFailures(t *testing.T) {
	w := newWorld(t)
	tg := w.target("paystack/main", "")
	w.rails.err = errors.New("paystack: Invalid key")
	for i := range 4 {
		if _, err := w.l.Look(ctx, tg); err == nil {
			t.Fatalf("look %d did not fail", i)
		}
		w.now = w.now.Add(lookback.Every)
	}
	if n := w.count(`SELECT count(*) FROM alerts WHERE tenant_id = $1 AND kind = $2`, w.env.Tenant, lookback.AlertFailing); n != 1 {
		t.Fatalf("%d failing alerts", n)
	}
	w.rails.err = nil
	if _, err := w.l.Look(ctx, tg); err != nil {
		t.Fatal(err)
	}
	if n := w.count(`SELECT failures FROM money_lookbacks WHERE tenant_id = $1`, w.env.Tenant); n != 0 {
		t.Errorf("failures %d", n)
	}
}
