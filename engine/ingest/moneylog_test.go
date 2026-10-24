package ingest_test

import (
	"crypto/sha512"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/moneylog"
)

type logged struct {
	kind, status, channel, reference, source string
	amount                                   *int64
	corrects                                 *uuid.UUID
}

func (w *world) moneyLog(t *testing.T) []logged {
	t.Helper()
	var out []logged
	err := db.InTenantTx(ctx, w.DB.App, []uuid.UUID{w.Tenant}, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT kind, status, channel, reference, source, amount_kobo, corrects FROM money_entries ORDER BY recorded_at, id`)
		if err != nil {
			return err
		}
		out, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (logged, error) {
			var l logged
			return l, r.Scan(&l.kind, &l.status, &l.channel, &l.reference, &l.source, &l.amount, &l.corrects)
		})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// The money log hears every normalised event of a connected rail in
// prod, with no workflow listening, once however often it is delivered;
// a later status of a payout is a new entry naming the first.
func TestMoneyLogHearsEveryEvent(t *testing.T) {
	w := newWorld(t)
	w.h.MoneyLog = &moneylog.Log{Pool: w.DB.App, PII: w.Vault}
	for _, env := range []string{"prod", "dev"} {
		if _, err := w.Vault.CreateConnection(ctx, w.Tenant, env, "paystack", "main", "api_key", map[string]string{"secret_key": "sk_test_x"}, "test"); err != nil {
			t.Fatal(err)
		}
	}
	deliver := func(trigger, env string, body []byte) {
		t.Helper()
		path := "/connectors/paystack@1/" + trigger
		if env != "" {
			path += "?env=" + env
		}
		if st, out := w.post(t, path, body, "x-paystack-signature", sign(sha512.New, "sk_test_x", body)); st != 202 {
			t.Fatalf("%s: %d %v", body, st, out)
		}
	}
	paid := []byte(`{"event":"charge.success","data":{"reference":"PAY-1","amount":250000,"currency":"NGN","paid_at":"2026-10-23T10:00:00Z"}}`)
	deliver("charge_event", "", paid)
	deliver("charge_event", "", paid) // a retry
	settled := []byte(`{"event":"transfer.success","data":{"reference":"T-1","amount":5000}}`)
	deliver("transfer_event", "", settled)
	deliver("transfer_event", "", []byte(`{"event":"transfer.reversed","data":{"reference":"T-1","amount":5000,"reason":"account closed"}}`))
	// Not prod: not the business's money.
	deliver("charge_event", "dev", []byte(`{"event":"charge.success","data":{"reference":"PAY-DEV","amount":100}}`))

	got := w.moneyLog(t)
	if len(got) != 3 {
		t.Fatalf("entries: %+v", got)
	}
	if g := got[0]; g.kind != "payment" || g.status != "settled" || g.channel != "paystack/main" || g.reference != "PAY-1" || g.source != "event" || g.amount == nil || *g.amount != 250000 {
		t.Errorf("payment: %+v", g)
	}
	if g := got[1]; g.kind != "payout" || g.status != "settled" || g.reference != "T-1" || g.corrects != nil {
		t.Errorf("payout: %+v", g)
	}
	if g := got[2]; g.kind != "payout" || g.status != "reversed" || g.corrects == nil {
		t.Errorf("reversal: %+v", g)
	}

	// Nobody changes or removes an entry, not even the app role.
	for _, q := range []string{`UPDATE money_entries SET amount_kobo = 1`, `DELETE FROM money_entries`, `UPDATE money_entries SET status = 'failed'`} {
		if err := db.InTenantTx(ctx, w.DB.App, []uuid.UUID{w.Tenant}, func(tx pgx.Tx) error { _, err := tx.Exec(ctx, q); return err }); err == nil {
			t.Errorf("the app role may %s", q)
		}
	}
}

// A Paystack refund, delivered to the payments address as Paystack sends
// every event, is money out of the business: one refund entry per status
// however often it is delivered, the later status naming the first.
func TestMoneyLogHearsRefunds(t *testing.T) {
	w := newWorld(t)
	w.h.MoneyLog = &moneylog.Log{Pool: w.DB.App, PII: w.Vault}
	if _, err := w.Vault.CreateConnection(ctx, w.Tenant, "prod", "paystack", "main", "api_key", map[string]string{"secret_key": "sk_test_x"}, "test"); err != nil {
		t.Fatal(err)
	}
	deliver := func(body []byte) {
		t.Helper()
		if st, out := w.post(t, "/connectors/paystack@1/charge_event", body, "x-paystack-signature", sign(sha512.New, "sk_test_x", body)); st != 202 {
			t.Fatalf("%s: %d %v", body, st, out)
		}
	}
	pending := []byte(`{"event":"refund.pending","data":{"status":"pending","transaction_reference":"PAY-9","refund_reference":null,"amount":"10000","currency":"NGN","processor":"instant-transfer","domain":"live"}}`)
	processed := []byte(`{"event":"refund.processed","data":{"status":"processed","transaction_reference":"PAY-9","refund_reference":"RF-1","amount":"10000","currency":"NGN","processor":"instant-transfer","domain":"live"}}`)
	deliver(pending)
	deliver(processed)
	deliver(processed) // a retry

	got := w.moneyLog(t)
	if len(got) != 2 {
		t.Fatalf("entries: %+v", got)
	}
	if g := got[0]; g.kind != "refund" || g.status != "pending" || g.reference != "PAY-9" || g.amount == nil || *g.amount != 10000 || g.corrects != nil {
		t.Errorf("pending: %+v", g)
	}
	if g := got[1]; g.kind != "refund" || g.status != "settled" || g.reference != "PAY-9" || g.source != "event" || g.corrects == nil {
		t.Errorf("processed: %+v", g)
	}
}
