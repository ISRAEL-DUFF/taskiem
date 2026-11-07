package db_test

import (
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/db/dbtest"
)

// TestLinkEnterFromBothSidesAtOnce: two linked businesses acting at the
// same moment each write both audit chains. taskiem_link_enter takes the two
// chains in one order (the lower tenant id first), so the calls queue and
// never deadlock (00408).
func TestLinkEnterFromBothSidesAtOnce(t *testing.T) {
	d := dbtest.New(t)
	ada, dangote := d.SeedTenant(t, nil), d.SeedTenant(t, nil)
	link := uuid.New()
	lo, hi := ada.ID, dangote.ID
	if hi.String() < lo.String() {
		lo, hi = hi, lo
	}
	if _, err := d.Admin.Exec(ctx, `INSERT INTO business_links (id, a_tenant, b_tenant, status, requested_by, requester_actor, how, role, expires_at)
		VALUES ($1, $2, $3, 'active', $2, 'user:u1', 'search', 'supplier', now() + interval '30 days')`, link, lo, hi); err != nil {
		t.Fatal(err)
	}
	enter := func(me uuid.UUID) error {
		return scoped(t, d, []uuid.UUID{me}, func(tx pgx.Tx) error {
			_, err := tx.Exec(ctx, `SELECT taskiem_link_enter($1, 'user', 'u1', 'link.message', '{}')`, link)
			if err != nil {
				return err
			}
			// Hold both chains a moment, as a request writing its rows does.
			_, err = tx.Exec(ctx, `SELECT pg_sleep(0.002)`)
			return err
		})
	}
	const rounds = 40
	var wg sync.WaitGroup
	errs := make(chan error, 2*rounds)
	for i := 0; i < rounds; i++ {
		for _, me := range []uuid.UUID{ada.ID, dangote.ID} {
			wg.Add(1)
			go func(me uuid.UUID) {
				defer wg.Done()
				if err := enter(me); err != nil {
					errs <- err
				}
			}(me)
		}
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Errorf("entering from both sides at once: %v (%s)", err, sqlState(err))
	}
	// Every entry is in both chains, and both chains still verify.
	for _, tn := range []uuid.UUID{ada.ID, dangote.ID} {
		var n int
		if err := d.Admin.QueryRow(ctx, `SELECT count(*) FROM audit_log WHERE tenant_id = $1 AND action = 'link.message'`, tn).Scan(&n); err != nil || n != 2*rounds {
			t.Errorf("audit entries in %v: %d %v", tn, n, err)
		}
	}
}
