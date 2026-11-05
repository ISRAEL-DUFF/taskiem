package alerts

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/israel-duff/taskiem/engine/db/dbtest"
	"github.com/israel-duff/taskiem/engine/secrets"
)

// slowHooks answers every webhook post, after a pause for some hosts, and
// counts the posts of each alert.
type slowHooks struct {
	mu     sync.Mutex
	delay  map[string]time.Duration
	posted map[string]int       // Taskiem-Alert-Id → posts
	first  map[string]time.Time // host → first post
	hook   func(*http.Request)  // run once, on the first post
}

func (s *slowHooks) RoundTrip(r *http.Request) (*http.Response, error) {
	s.mu.Lock()
	hook := s.hook
	s.hook = nil
	s.posted[r.Header.Get("Taskiem-Alert-Id")]++
	if _, ok := s.first[r.URL.Hostname()]; !ok {
		s.first[r.URL.Hostname()] = time.Now()
	}
	d := s.delay[r.URL.Hostname()]
	s.mu.Unlock()
	if hook != nil {
		hook(r)
	}
	time.Sleep(d)
	return &http.Response{StatusCode: 200, Status: "200 OK", Body: io.NopCloser(strings.NewReader("")), Header: http.Header{}, Request: r}, nil
}

// webhookAlerts gives tenant a webhook channel to host and n alerts due on
// it.
func webhookAlerts(t *testing.T, d *dbtest.DB, v *secrets.Vault, tenant uuid.UUID, host string, n int) {
	t.Helper()
	ctx := context.Background()
	ch := uuid.New()
	if _, err := d.Admin.Exec(ctx, `INSERT INTO alert_channels (id, tenant_id, kind, name, config, created_by) VALUES ($1, $2, 'webhook', 'hook', $3, 'test')`,
		ch, tenant, `{"url": "https://`+host+`/hook"}`); err != nil {
		t.Fatal(err)
	}
	if _, err := v.Put(ctx, tenant, VaultEnv, SecretName(ch), []byte("signing-key"), "test"); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Admin.Exec(ctx, `WITH a AS (INSERT INTO alerts (id, tenant_id, kind, dedup_key, title, body)
		SELECT gen_random_uuid(), $1, 'test', 'k' || g, 'Alert ' || g, 'body' FROM generate_series(1, $3) g RETURNING id)
		INSERT INTO alert_deliveries (alert_id, channel_id, tenant_id) SELECT id, $2, $1 FROM a`, tenant, ch, n); err != nil {
		t.Fatal(err)
	}
}

// One tenant's slow webhook does not hold back another tenant's alerts,
// and a second scheduler working the same deliveries never posts one
// twice, even when the first is slow enough for its claim to lapse.
func TestAlertDeliveryAcrossTenantsAndReplicas(t *testing.T) {
	ctx := context.Background()
	d := dbtest.New(t)
	kms, err := secrets.NewLocalKMS(map[string][]byte{"root": bytes.Repeat([]byte{3}, 32)})
	if err != nil {
		t.Fatal(err)
	}
	v := &secrets.Vault{Pool: d.App, KMS: kms, RootKey: "root"}
	slow, fast := d.SeedTenant(t, nil), d.SeedTenant(t, nil)
	webhookAlerts(t, d, v, slow.ID, "slow.test", 20)
	webhookAlerts(t, d, v, fast.ID, "fast.test", 1)
	hooks := &slowHooks{delay: map[string]time.Duration{"slow.test": 200 * time.Millisecond}, posted: map[string]int{}, first: map[string]time.Time{}}
	a := &Alerter{Pool: d.App, Secrets: v, Client: &http.Client{Transport: hooks}}

	start := time.Now()
	if err := a.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	if took := hooks.first["fast.test"].Sub(start); took > time.Second {
		t.Errorf("the fast tenant's alert waited %s behind the slow tenant's", took)
	}
	var pending int
	_ = d.Admin.QueryRow(ctx, `SELECT count(*) FROM alert_deliveries WHERE status <> 'sent'`).Scan(&pending)
	if pending != 0 {
		t.Errorf("%d deliveries not sent", pending)
	}

	// Ten more for the slow tenant. While replica A posts the first, five
	// minutes pass (every other claim it could hold lapses) and replica B
	// delivers.
	webhookAlerts(t, d, v, slow.ID, "slow.test", 10)
	b := &Alerter{Pool: d.App, Secrets: v, Client: &http.Client{Transport: hooks}}
	hooks.mu.Lock()
	hooks.hook = func(r *http.Request) {
		if _, err := d.Admin.Exec(ctx, `UPDATE alert_deliveries SET next_attempt_at = now() - interval '1 second'
			WHERE status = 'pending' AND alert_id <> $1`, r.Header.Get("Taskiem-Alert-Id")); err != nil {
			t.Error(err)
		}
		if err := b.Deliver(ctx, slow.ID); err != nil {
			t.Error(err)
		}
	}
	hooks.mu.Unlock()
	if err := a.Deliver(ctx, slow.ID); err != nil {
		t.Fatal(err)
	}
	hooks.mu.Lock()
	defer hooks.mu.Unlock()
	for id, n := range hooks.posted {
		if n != 1 {
			t.Errorf("alert %s posted %d times", id, n)
		}
	}
	if len(hooks.posted) != 31 {
		t.Errorf("%d alerts posted, want 31", len(hooks.posted))
	}
}
