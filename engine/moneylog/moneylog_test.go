package moneylog

import "testing"

func TestFromEvent(t *testing.T) {
	cases := []struct {
		event                       string
		fields                      map[string]any
		dir, kind, status, ref, key string
	}{
		{"payment.received", map[string]any{"payment_id": "P1", "amount_kobo": int64(5000)}, "in", "payment", "settled", "P1", "paystack/main|payment.received|P1"},
		{"payment.returned", map[string]any{"payment_id": "P1", "reason": "late"}, "in", "payment", "reversed", "P1", "paystack/main|payment.returned|P1"},
		{"virtual_account.credited", map[string]any{"reference": "VA1", "credit_id": "C1", "amount_kobo": 700.0, "payer_name": "ADA OBI"}, "in", "account_credit", "settled", "VA1", "paystack/main|virtual_account.credited|VA1|C1"},
		{"payout.settled", map[string]any{"payout_id": "T1"}, "out", "payout", "settled", "T1", "paystack/main|payout.settled|T1"},
		{"payout.failed", map[string]any{"payout_id": "T1", "reason": "no such account"}, "out", "payout", "failed", "T1", "paystack/main|payout.failed|T1"},
		{"pos.sale", map[string]any{"reference": "S1", "status": "declined", "amount_kobo": "150000"}, "in", "pos_sale", "failed", "S1", "paystack/main|pos.sale.failed|S1"},
		{"refund.pending", map[string]any{"payment_id": "P1", "amount_kobo": int64(10000)}, "out", "refund", "pending", "P1", "paystack/main|refund.pending|P1"},
		{"refund.processed", map[string]any{"payment_id": "P1", "refund_id": "R9", "amount_kobo": int64(10000)}, "out", "refund", "settled", "P1", "paystack/main|refund.processed|P1"},
		{"refund.failed", map[string]any{"payment_id": "P1"}, "out", "refund", "failed", "P1", "paystack/main|refund.failed|P1"},
	}
	for _, c := range cases {
		e, ok := FromEvent("paystack/main", c.event, c.fields)
		if !ok || e.Direction != c.dir || e.Kind != c.kind || e.Status != c.status || e.Reference != c.ref || e.Key() != c.key || e.check() != nil {
			t.Errorf("%s: %v %+v (key %s)", c.event, ok, e, e.Key())
		}
	}
	if e, _ := FromEvent("x/main", "virtual_account.credited", map[string]any{"reference": "VA", "credit_id": "C1", "amount_kobo": 700.0, "payer_name": "A"}); e.AmountKobo == nil || *e.AmountKobo != 700 || e.PayerName != "A" {
		t.Errorf("credit: %+v", e)
	}
	// Each credit to a permanent account is its own movement, with the
	// rail's fee and the payer's account.
	one, _ := FromEvent("x/main", "virtual_account.credited", map[string]any{"reference": "VA", "credit_id": "C1", "amount_kobo": int64(5000000),
		"fee_kobo": int64(30000), "payer_account": "XXXXXX0011", "occurred_at": "2026-10-23T09:00:00Z"})
	two, _ := FromEvent("x/main", "virtual_account.credited", map[string]any{"reference": "VA", "credit_id": "C2", "amount_kobo": int64(5000000)})
	if one.Key() != "x/main|virtual_account.credited|VA|C1" || two.Key() == one.Key() || one.Reference != "VA" {
		t.Errorf("credit keys: %s, %s", one.Key(), two.Key())
	}
	if one.FeeKobo == nil || *one.FeeKobo != 30000 || one.PayerAccount != "XXXXXX0011" || one.OccurredAt == nil || two.FeeKobo != nil {
		t.Errorf("credit fields: %+v", one)
	}
	for _, bad := range []struct {
		event  string
		fields map[string]any
	}{{"payment.received", map[string]any{}}, {"something.else", map[string]any{"payment_id": "x"}},
		// Without its credit_id, a credit would be keyed by the account alone.
		{"virtual_account.credited", map[string]any{"reference": "VA", "amount_kobo": 700.0}}} {
		if _, ok := FromEvent("x/main", bad.event, bad.fields); ok {
			t.Errorf("%s logged", bad.event)
		}
	}
}

func TestFromEventRefundNote(t *testing.T) {
	e, _ := FromEvent("paystack/main", "refund.processed", map[string]any{"payment_id": "P1", "refund_id": "R9"})
	if e.Note != "Refund of payment P1, refund R9" {
		t.Errorf("note %q", e.Note)
	}
	if e, _ := FromEvent("paystack/main", "refund.failed", map[string]any{"payment_id": "P1", "reason": "card closed"}); e.Note != "Refund of payment P1: card closed" {
		t.Errorf("note %q", e.Note)
	}
}

// A payout answered settled when made is keyed as its payout.settled
// event; any other answer is not recorded.
func TestFromPayout(t *testing.T) {
	e, ok := FromPayout("paystack/main", map[string]any{"amount_kobo": int64(500000), "currency": "NGN"}, map[string]any{"payout_id": "T1", "status": "settled", "fee_kobo": int64(1075)})
	ev, _ := FromEvent("paystack/main", "payout.settled", map[string]any{"payout_id": "T1"})
	if !ok || e.Key() != ev.Key() || e.Source != "step" || e.Direction != "out" || e.Kind != "payout" || e.Status != "settled" ||
		e.AmountKobo == nil || *e.AmountKobo != 500000 || e.FeeKobo == nil || *e.FeeKobo != 1075 || e.check() != nil {
		t.Errorf("%v %+v", ok, e)
	}
	for _, out := range []map[string]any{{"payout_id": "T1", "status": "pending"}, {"payout_id": "T1", "status": "failed"}, {"status": "settled"}} {
		if _, ok := FromPayout("paystack/main", nil, out); ok {
			t.Errorf("%v recorded", out)
		}
	}
}
