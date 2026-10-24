package runtime_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/connector"
	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/moneylog"
	rt "github.com/israel-duff/taskiem/engine/runtime/runtimetest"
)

// A payout the rail answers settled at once is in the money log with the
// step's outcome, keyed as the rail's payout.settled event would be: the
// event, if it comes later, is the same entry. A pending answer writes
// nothing (its event will).
func TestSettledPayoutReachesTheMoneyLog(t *testing.T) {
	e := rt.New(t)
	log := &moneylog.Log{Pool: e.DB.App, PII: e.Vault}
	e.Store.MoneyLog = log
	newRail(t, e, "testrail", "Test Rail")
	connect(t, e, "rail", "testrail")
	c, _ := e.Registry.Get("testrail@1")
	send := c.Actions["send"]
	c.Actions["send"] = connector.ActionFunc(func(ctx context.Context, req connector.Request) (connector.Response, error) {
		resp, err := send.Execute(ctx, req)
		if out, ok := resp.Output.(map[string]any); ok && err == nil && fmt.Sprint(req.Input["kobo"]) == "500000" {
			out["state"] = "done" // Bola's settles at once; Ada's is queued
		}
		return resp, err
	})
	wf := e.Publish(t, payrollWD)
	e.Start(t, wf, people)
	e.Drain(t)

	type row struct {
		dir, kind, status, channel, ref, event, source, currency string
		amount, fee                                              int64
	}
	read := func() []row {
		t.Helper()
		var out []row
		if err := db.InTenantTx(ctx, e.DB.App, []uuid.UUID{e.Tenant}, func(tx pgx.Tx) error {
			rows, err := tx.Query(ctx, `SELECT direction, kind, status, channel, reference, event, source, currency, amount_kobo, fee_kobo FROM money_entries`)
			if err != nil {
				return err
			}
			defer rows.Close()
			for rows.Next() {
				var r row
				if err := rows.Scan(&r.dir, &r.kind, &r.status, &r.channel, &r.ref, &r.event, &r.source, &r.currency, &r.amount, &r.fee); err != nil {
					return err
				}
				out = append(out, r)
			}
			return rows.Err()
		}); err != nil {
			t.Fatal(err)
		}
		return out
	}
	got := read()
	if len(got) != 1 {
		t.Fatalf("logged %d: %+v", len(got), got)
	}
	r := got[0]
	if r.dir != "out" || r.kind != "payout" || r.status != "settled" || r.channel != "testrail/rail" || r.event != "payout.settled" ||
		r.source != "step" || r.currency != "NGN" || r.amount != 500000 || r.fee != 1075 || r.ref == "" {
		t.Errorf("entry %+v", r)
	}

	// The rail's own event for it later is the same entry.
	if ok, err := log.RecordEvent(ctx, e.Tenant, "prod", "testrail", "rail", "payout.settled", map[string]any{"payout_id": r.ref, "amount_kobo": int64(500000)}); err != nil || !ok {
		t.Fatalf("event: %v %v", ok, err)
	}
	if got := read(); len(got) != 1 {
		t.Errorf("after the event, %d entries: %+v", len(got), got)
	}
}
