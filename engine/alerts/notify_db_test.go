package alerts

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/db/dbtest"
)

// Told once, even at once: two transactions notifying the same kind and
// dedup key together write one alert (alerts has no unique index when
// rule_id IS NULL). The second waits for the first, then sees its alert.
func TestNotifyOnceAtOnce(t *testing.T) {
	ctx := context.Background()
	d := dbtest.New(t)
	tenant := d.SeedTenant(t, nil).ID
	al := Alert{Kind: "stock.old_columns", Dedup: "stock.old_columns", Title: "Old columns", Body: "body"}
	tx1, err := d.App.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx1.Rollback(ctx) }()
	if _, err := tx1.Exec(ctx, "SELECT set_config('app.tenant_scope', $1, true)", db.Scope(tenant)); err != nil {
		t.Fatal(err)
	}
	if ok, err := Notify(ctx, tx1, tenant, al); err != nil || !ok {
		t.Fatalf("first: %v %v", ok, err)
	}
	type result struct {
		ok  bool
		err error
	}
	done := make(chan result, 1)
	go func() {
		var ok bool
		err := db.InTenantTx(ctx, d.App, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
			var err error
			ok, err = Notify(ctx, tx, tenant, al)
			return err
		})
		done <- result{ok, err}
	}()
	// The second has read no alert yet (the first's is not committed); it
	// must not go on to write its own.
	time.Sleep(300 * time.Millisecond)
	if err := tx1.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	r := <-done
	if r.err != nil || r.ok {
		t.Fatalf("second: told %v, %v", r.ok, r.err)
	}
	var n int
	if err := d.Admin.QueryRow(ctx, `SELECT count(*) FROM alerts WHERE tenant_id = $1 AND kind = $2`, tenant, al.Kind).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("alerts: %d", n)
	}
}
