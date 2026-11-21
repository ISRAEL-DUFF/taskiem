package api

import (
	"context"
	"log/slog"
	"testing"

	"github.com/google/uuid"

	"github.com/israel-duff/taskiem/engine/db/dbtest"
	"github.com/israel-duff/taskiem/engine/runtime"
)

// A use is counted even when the request it came with is cancelled before
// the count is written (review fix: the count's statements ran on the
// request's context).
func TestActionUsedOutlivesItsRequest(t *testing.T) {
	d := dbtest.New(t)
	ctx := context.Background()
	tenant, user := uuid.New(), uuid.New()
	if _, err := d.Admin.Exec(ctx, `INSERT INTO tenants (id, name, plan_id) VALUES ($1, 'Acme', gen_random_uuid())`, tenant); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Admin.Exec(ctx, `INSERT INTO users (id, email, name) VALUES ($1, 'ada@acme.test', 'Ada')`, user); err != nil {
		t.Fatal(err)
	}
	s := &Server{Store: &runtime.Store{Pool: d.App}, Logger: slog.New(slog.DiscardHandler)}
	c := &chat{p: &Principal{UserID: user, TenantID: tenant}, tenant: tenantRef{ID: tenant}}
	gone, cancel := context.WithCancel(ctx)
	cancel()
	s.actionUsed(gone, c, "riders.add")
	var n int
	if err := d.Admin.QueryRow(ctx, `SELECT coalesce(sum(uses), 0) FROM chat_action_uses WHERE tenant_id = $1 AND user_id = $2`, tenant, user).Scan(&n); err != nil || n != 1 {
		t.Fatalf("counted %d %v", n, err)
	}
}
