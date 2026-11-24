package ingest_test

import (
	"testing"

	"github.com/israel-duff/taskiem/engine/moneylog"
)

// Two iswallet wallet.credit.posted webhooks for two separate transfers
// (₦20,000 then ₦30,000; different txn_id and provider_reference) into the
// same one-time number (one tot_ reference), delivered to the platform
// webhook address as iswallet sends them: the money log holds both, two
// entries, ₦50,000. Either delivered again adds nothing.
func TestTwoTransfersIntoOneTimeNumber(t *testing.T) {
	w := newPlatformWorld(t)
	w.h.MoneyLog = &moneylog.Log{Pool: w.DB.App, PII: w.Vault}
	w.turnOn(t, w.Tenant, "live", "W-ACME")
	const ref = "tot_ab12cd34ef56gh78ij90kl12mn34op56"
	transfer := func(id, txn, provider string, amount, credited int, at string) map[string]any {
		return map[string]any{"id": id, "event_type": "wallet.credit.posted", "wallet_id": "W-ACME",
			"data": map[string]any{"txn_id": txn, "provider_reference": provider, "amount": amount, "amount_credited": credited, "currency": "NGN", "occurred_at": at,
				"virtual_account": map[string]any{"reference": ref, "account_number": "9907654321", "status": "active"}}}
	}
	first := transfer("evt_1", "t-1", "100004261027091402000001", 2000000, 1990000, "2026-10-27T09:14:02Z")
	second := transfer("evt_2", "t-2", "100004261027102000000002", 3000000, 2990000, "2026-10-27T10:20:00Z")
	// The last two are retries, under new event ids.
	retryFirst := transfer("evt_3", "t-1", "100004261027091402000001", 2000000, 1990000, "2026-10-27T09:14:02Z")
	retrySecond := transfer("evt_4", "t-2", "100004261027102000000002", 3000000, 2990000, "2026-10-27T10:20:00Z")
	for _, ev := range []map[string]any{first, second, retryFirst, retrySecond} {
		if st, out := w.send(t, "live", platformLive, ev); st != 200 {
			t.Fatalf("%v: %d %v", ev["id"], st, out)
		}
	}
	got := w.moneyLog(t)
	var sum int64
	for _, g := range got {
		if g.amount != nil {
			sum += *g.amount
		}
		if g.kind != "payment" || g.status != "settled" || g.reference != ref || g.corrects != nil {
			t.Errorf("entry: %+v", g)
		}
	}
	if len(got) != 2 || sum != 5000000 {
		t.Errorf("money log holds %d entries summing %d kobo; want 2 entries summing 5000000", len(got), sum)
	}
}
