package storefront_test

import (
	"context"
	"testing"

	"github.com/israel-duff/taskiem/engine/db/dbtest"
	"github.com/israel-duff/taskiem/engine/storefront"
)

// One rule for "can this be paid now" (taskiem-for-everyone §13): the Go
// function every pay link follows and the SQL one Orders on You's Pay
// follows agree on every state, way of paying and amount left.
func TestPayableNowMatchesSQL(t *testing.T) {
	d := dbtest.New(t)
	ctx := context.Background()
	statuses := []string{"received", "awaiting_payment", "paid", "accepted", "declined", "expired", "completed", "cancelled",
		"refund_due", "no_show", "price_changed", "refunded", "dispatched", "on_the_way", "arrived", "proposed", "changed", ""}
	payments := []string{"after_accept", "at_order", "none", "on_delivery", ""}
	payable := 0
	for _, st := range statuses {
		for _, pay := range payments {
			for _, left := range []int64{-100, 0, 1, 50000} {
				var sql bool
				if err := d.Admin.QueryRow(ctx, `SELECT taskiem_order_payable_now($1, NULLIF($2, ''), $3)`, st, pay, left).Scan(&sql); err != nil {
					t.Fatal(err)
				}
				if got := storefront.PayableNow(st, pay, left); got != sql {
					t.Errorf("PayableNow(%q, %q, %d) = %v, SQL says %v", st, pay, left, got, sql)
				}
				if sql {
					payable++
				}
			}
		}
	}
	// Spot checks: the closed states are never payable; a part-paid order is.
	for _, st := range []string{"cancelled", "declined", "expired", "refund_due", "refunded", "completed", "no_show"} {
		if storefront.PayableNow(st, "at_order", 50000) {
			t.Errorf("%s payable", st)
		}
	}
	if !storefront.PayableNow("paid", "at_order", 100) || storefront.PayableNow("received", "after_accept", 100) || !storefront.PayableNow("received", "at_order", 100) {
		t.Error("the open states")
	}
	if payable == 0 {
		t.Fatal("nothing payable at all")
	}
}
