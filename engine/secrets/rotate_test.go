package secrets_test

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/israel-duff/taskiem/engine/db/dbtest"
	"github.com/israel-duff/taskiem/engine/secrets"
)

// Ten workers find a connection's access token expired at once. A provider
// that rotates refresh tokens (QuickBooks) revokes the chain when a spent
// one comes back, so exactly one refresh may happen: the others wait on
// the connection's lock, see the new tokens and use them.
func TestRotateCredentialsOnceAcrossWorkers(t *testing.T) {
	d := dbtest.New(t)
	v := localVault(t, d)
	a := d.SeedTenant(t, nil)
	if _, err := v.CreateConnection(ctx, a.ID, "prod", "quickbooks", "main", "oauth2", map[string]string{"refresh_token": "rt-1", "access_token": "at-0"}, "u1"); err != nil {
		t.Fatal(err)
	}
	var refreshes atomic.Int32
	var wg sync.WaitGroup
	got := make([]map[string]string, 10)
	errs := make([]error, 10)
	for i := range 10 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			// Each worker read the expired token before the race.
			stale := "at-0"
			got[i], errs[i] = v.RotateCredentials(ctx, a.ID, "prod", "quickbooks", "main", func(cur map[string]string) (map[string]string, error) {
				if cur["access_token"] != stale {
					return cur, nil // another worker refreshed: use theirs
				}
				refreshes.Add(1)
				if cur["refresh_token"] != "rt-1" {
					return nil, errors.New("a spent refresh token was sent")
				}
				next := map[string]string{}
				for k, val := range cur {
					next[k] = val
				}
				next["refresh_token"], next["access_token"] = "rt-2", "at-1"
				return next, nil
			})
		}()
	}
	wg.Wait()
	if n := refreshes.Load(); n != 1 {
		t.Fatalf("%d refreshes, want 1", n)
	}
	for i := range 10 {
		if errs[i] != nil || got[i]["access_token"] != "at-1" || got[i]["refresh_token"] != "rt-2" {
			t.Errorf("worker %d: %v %v", i, got[i], errs[i])
		}
	}
	// A restart reads the rotated tokens.
	again, err := v.Credentials(ctx, a.ID, "prod", "quickbooks", "main")
	if err != nil || again["refresh_token"] != "rt-2" || again["access_token"] != "at-1" {
		t.Errorf("stored: %v %v", again, err)
	}
	var audits int
	if err := d.Admin.QueryRow(ctx, `SELECT count(*) FROM audit_log WHERE tenant_id = $1 AND action = 'connection.refresh' AND detail::text NOT LIKE '%rt-%'`, a.ID).Scan(&audits); err != nil || audits != 1 {
		t.Errorf("connection.refresh audited %d times (%v), want once, without values", audits, err)
	}
	// A refresh the provider refuses stores nothing.
	if _, err := v.RotateCredentials(ctx, a.ID, "prod", "quickbooks", "main", func(map[string]string) (map[string]string, error) {
		return nil, errors.New("invalid_grant")
	}); err == nil {
		t.Error("a refused refresh succeeded")
	}
	if kept, _ := v.Credentials(ctx, a.ID, "prod", "quickbooks", "main"); kept["refresh_token"] != "rt-2" {
		t.Errorf("after a refused refresh: %v", kept)
	}
	if _, err := v.RotateCredentials(ctx, a.ID, "prod", "quickbooks", "other", func(c map[string]string) (map[string]string, error) { return c, nil }); !errors.Is(err, secrets.ErrNotFound) {
		t.Errorf("no such connection: %v", err)
	}
}
