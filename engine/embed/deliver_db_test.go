package embed

import (
	"context"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/israel-duff/taskiem/engine/db/dbtest"
)

type fixedKey struct{}

func (fixedKey) Get(context.Context, uuid.UUID, string, string) (string, error) {
	return "signing-key", nil
}

// slowPartners answers every post, after a pause for some hosts, and
// counts the posts of each delivery.
type slowPartners struct {
	mu     sync.Mutex
	delay  map[string]time.Duration
	posted map[string]int       // Taskiem-Delivery-Id → posts
	first  map[string]time.Time // host → first post
	hook   func(*http.Request)  // run once, on the first post
}

func (s *slowPartners) RoundTrip(r *http.Request) (*http.Response, error) {
	s.mu.Lock()
	hook := s.hook
	s.hook = nil
	s.posted[r.Header.Get("Taskiem-Delivery-Id")]++
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

// partnerEvents makes tenant a partner with an app posting to host, and
// queues n events for it.
func partnerEvents(t *testing.T, d *dbtest.DB, partner uuid.UUID, host string, n int) {
	t.Helper()
	ctx := context.Background()
	if _, err := d.Admin.Exec(ctx, `INSERT INTO partners (tenant_id, updated_by) VALUES ($1, 'test') ON CONFLICT DO NOTHING`, partner); err != nil {
		t.Fatal(err)
	}
	app := uuid.New()
	if _, err := d.Admin.Exec(ctx, `INSERT INTO embed_apps (id, tenant_id, name, webhook_url, created_by) VALUES ($1, $2, $3, $4, 'test')`,
		app, partner, "app "+app.String()[:8], "https://"+host+"/hook"); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Admin.Exec(ctx, `INSERT INTO partner_webhook_deliveries (id, tenant_id, app_id, event, dedup_key, payload)
		SELECT gen_random_uuid(), $1, $2, 'workflow.published', 'k' || g, '{}' FROM generate_series(1, $3) g`, partner, app, n); err != nil {
		t.Fatal(err)
	}
}

// One partner's slow endpoint does not hold back another partner's
// events, and a second scheduler working the same deliveries never posts
// one twice, even when the first is slow enough for its claim to lapse.
func TestPartnerDeliveryAcrossPartnersAndReplicas(t *testing.T) {
	ctx := context.Background()
	d := dbtest.New(t)
	slow, fast := d.SeedTenant(t, nil), d.SeedTenant(t, nil)
	partnerEvents(t, d, slow.ID, "slow.test", 20)
	partnerEvents(t, d, fast.ID, "fast.test", 1)
	posts := &slowPartners{delay: map[string]time.Duration{"slow.test": 200 * time.Millisecond}, posted: map[string]int{}, first: map[string]time.Time{}}
	a := &Webhooks{Pool: d.App, Secrets: fixedKey{}, Client: &http.Client{Transport: posts}}

	start := time.Now()
	if err := a.Tick(ctx); err != nil {
		t.Fatal(err)
	}

	if took := posts.first["fast.test"].Sub(start); took > time.Second {
		t.Errorf("the fast partner's event waited %s behind the slow partner's", took)
	}

	// Ten more for the slow partner. While replica A posts the first, five
	// minutes pass (every other claim it could hold lapses) and replica B
	// delivers.
	partnerEvents(t, d, slow.ID, "slow.test", 10)
	b := &Webhooks{Pool: d.App, Secrets: fixedKey{}, Client: &http.Client{Transport: posts}}
	posts.mu.Lock()
	posts.hook = func(r *http.Request) {
		if _, err := d.Admin.Exec(ctx, `UPDATE partner_webhook_deliveries SET next_attempt_at = now() - interval '1 second'
			WHERE status = 'pending' AND id <> $1`, r.Header.Get("Taskiem-Delivery-Id")); err != nil {
			t.Error(err)
		}
		if err := b.Deliver(ctx, slow.ID); err != nil {
			t.Error(err)
		}
	}
	posts.mu.Unlock()
	if err := a.Deliver(ctx, slow.ID); err != nil {
		t.Fatal(err)
	}
	posts.mu.Lock()
	defer posts.mu.Unlock()
	for id, n := range posts.posted {
		if n != 1 {
			t.Errorf("delivery %s posted %d times", id, n)
		}
	}
	if len(posts.posted) != 31 {
		t.Errorf("%d deliveries posted, want 31", len(posts.posted))
	}
	var left int
	_ = d.Admin.QueryRow(ctx, `SELECT count(*) FROM partner_webhook_deliveries WHERE status <> 'delivered'`).Scan(&left)
	if left != 0 {
		t.Errorf("%d deliveries not delivered", left)
	}
}
