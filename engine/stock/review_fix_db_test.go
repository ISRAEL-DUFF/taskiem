package stock_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/productstock"
	"github.com/israel-duff/taskiem/engine/stock"
)

// PR review fix 1: a catalogue refresh drops only the links made before it
// started. A product linked while the catalogue is read (so missing from
// the read) stays counted; one gone from the catalogue before the refresh
// started is dropped. A link being made when the refresh starts holds it
// back until that link's transaction ends.
func TestRefreshKeepsLinksMadeDuringTheRead(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t, map[string]int64{"old": 1, "gone": 1})

	// A link being made (LinkTx's lock, held by a transaction not yet
	// ended): the refresh waits for it.
	held, err := w.env.DB.App.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer held.Rollback(ctx) //nolint:errcheck // ended below
	if _, err := held.Exec(ctx, `SELECT pg_advisory_xact_lock_shared(hashtextextended('taskiem product links ' || $1::text, 0))`, w.env.Tenant); err != nil {
		t.Fatal(err)
	}
	var started time.Time
	done := make(chan error, 1)
	go func() {
		done <- db.InTenantTx(ctx, w.env.DB.App, []uuid.UUID{w.env.Tenant}, func(tx pgx.Tx) error {
			var err error
			started, err = productstock.RefreshStartTx(ctx, tx, w.env.Tenant)
			return err
		})
	}()
	select {
	case err := <-done:
		t.Fatalf("the refresh started while a link was being made: %v", err)
	case <-time.After(300 * time.Millisecond):
	}
	if err := held.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}

	// The catalogue is read: "new" is linked while it is (so not in the
	// read), and "gone" was deleted from it.
	w.counted("new")
	var dropped int64
	w.in(func(tx pgx.Tx) error {
		var err error
		dropped, err = productstock.DropGoneTx(ctx, tx, w.env.Tenant, []string{"old"}, started)
		return err
	})
	var left map[string]int64
	w.in(func(tx pgx.Tx) error {
		var err error
		left, err = productstock.LeftTx(ctx, tx, w.env.Tenant, []string{"old", "gone", "new"})
		return err
	})
	if _, ok := left["new"]; !ok || dropped != 1 {
		t.Fatalf("a product linked during the read lost its link: dropped %d, left %v", dropped, left)
	}
	if _, ok := left["gone"]; ok {
		t.Fatalf("a product gone before the refresh kept its link: %v", left)
	}
	if _, ok := left["old"]; !ok {
		t.Fatalf("a product in the read lost its link: %v", left)
	}
}

// PR review fix: bringing back a retired stock item makes it current
// again, so it is held to the 5,000 a business keeps, as making one is.
func TestBringingBackHeldToTheLimit(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t, nil)
	old := w.counted("old")
	w.in(func(tx pgx.Tx) error {
		return stock.RetireItemTx(ctx, tx, w.env.Tenant, old, false)
	})
	w.in(func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO stock_items (id, tenant_id, name, created_by)
			SELECT gen_random_uuid(), $1, 'Item ' || n, 'test' FROM generate_series(1, $2::int) n`, w.env.Tenant, stock.MaxItems)
		return err
	})
	err := db.InTenantTx(ctx, w.env.DB.App, []uuid.UUID{w.env.Tenant}, func(tx pgx.Tx) error {
		return stock.RetireItemTx(ctx, tx, w.env.Tenant, old, true)
	})
	if !errors.Is(err, stock.ErrTooMany) {
		t.Fatalf("brought back past 5,000: %v", err)
	}
	// One retired: there is room for it again.
	w.in(func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `UPDATE stock_items SET retired_at = now() WHERE tenant_id = $1 AND name = 'Item 1'`, w.env.Tenant)
		return err
	})
	w.in(func(tx pgx.Tx) error {
		return stock.RetireItemTx(ctx, tx, w.env.Tenant, old, true)
	})
}
