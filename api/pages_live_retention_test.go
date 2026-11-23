package api_test

import (
	"context"
	"testing"
	"time"

	"github.com/israel-duff/taskiem/engine/storefront"
)

// What an open page watches is changed_at, never updated_at: taking a
// person's details off their ended orders when their account goes does
// not restart the orders' retention (taskiem_storefront_orders_forget
// reads updated_at), but does tell an open page.
func TestPagesLiveLeavesRetentionAlone(t *testing.T) {
	ctx := context.Background()
	w := ordersWorld(t)
	const number = "+2348031150401"
	ade, _ := w.personIn(t, number)
	adeID := personID(t, ade)
	o := w.placeByPhone(t, w.biz, number, storefront.ChatPayLater, &adeID)
	old := time.Now().Add(-400 * 24 * time.Hour).UTC().Truncate(time.Microsecond)
	if _, err := w.env.DB.Admin.Exec(ctx, `UPDATE storefront_orders SET status = 'completed', updated_at = $2 WHERE id = $1`, o.ID, old); err != nil {
		t.Fatal(err)
	}
	var before time.Time
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT changed_at FROM storefront_orders WHERE id = $1`, o.ID).Scan(&before); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{`DELETE FROM sessions WHERE user_id = $1`, `DELETE FROM user_phones WHERE user_id = $1`, `DELETE FROM users WHERE id = $1`} {
		if _, err := w.env.DB.Admin.Exec(ctx, q, adeID); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	var updated, changed time.Time
	var left bool
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT updated_at, changed_at, person_left FROM storefront_orders WHERE id = $1`, o.ID).Scan(&updated, &changed, &left); err != nil {
		t.Fatal(err)
	}
	if !left {
		t.Fatal("the order was not released")
	}
	if !updated.Equal(old) {
		t.Errorf("updated_at moved from %v to %v: retention restarted", old, updated)
	}
	if !changed.After(before) {
		t.Errorf("changed_at did not move: %v, %v", before, changed)
	}
}
