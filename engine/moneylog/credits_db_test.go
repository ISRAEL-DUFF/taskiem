package moneylog_test

import (
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/moneylog"
	rt "github.com/israel-duff/taskiem/engine/runtime/runtimetest"
)

// A permanent account takes many credits under one reference: each is
// recorded once, on its own, and hearing one again (a redelivery, or the
// look-back finding it) changes nothing.
func TestCreditsToOneAccountAreEachRecordedOnce(t *testing.T) {
	e := rt.New(t)
	log := &moneylog.Log{Pool: e.DB.App, PII: e.Vault}
	credit := func(id string, fee int64) map[string]any {
		return map[string]any{"reference": "STU-1", "credit_id": id, "amount_kobo": int64(5000000), "fee_kobo": fee,
			"payer_name": "ADA OBI", "payer_account": "XXXXXX0011", "occurred_at": "2026-10-23T09:00:00Z"}
	}
	for _, c := range []map[string]any{credit("C1", 30000), credit("C2", 30000), credit("C1", 30000)} {
		if ok, err := log.RecordEvent(ctx, e.Tenant, "prod", "paystack", "main", "virtual_account.credited", c); err != nil || !ok {
			t.Fatal(ok, err)
		}
	}
	var n, corrects, fees int
	if err := db.InTenantTx(ctx, e.DB.App, []uuid.UUID{e.Tenant}, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT count(*), count(corrects), COALESCE(sum(fee_kobo), 0) FROM money_entries WHERE reference = 'STU-1'`).Scan(&n, &corrects, &fees)
	}); err != nil {
		t.Fatal(err)
	}
	if n != 2 || corrects != 0 || fees != 60000 {
		t.Errorf("entries %d, corrections %d, fees %d; want 2 entries, none correcting another, fees 60000", n, corrects, fees)
	}
}
