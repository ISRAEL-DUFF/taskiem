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
		{"virtual_account.credited", map[string]any{"reference": "VA1", "amount_kobo": 700.0, "payer_name": "ADA OBI"}, "in", "account_credit", "settled", "VA1", "paystack/main|virtual_account.credited|VA1"},
		{"payout.settled", map[string]any{"payout_id": "T1"}, "out", "payout", "settled", "T1", "paystack/main|payout.settled|T1"},
		{"payout.failed", map[string]any{"payout_id": "T1", "reason": "no such account"}, "out", "payout", "failed", "T1", "paystack/main|payout.failed|T1"},
		{"pos.sale", map[string]any{"reference": "S1", "status": "declined", "amount_kobo": "150000"}, "in", "pos_sale", "failed", "S1", "paystack/main|pos.sale.failed|S1"},
	}
	for _, c := range cases {
		e, ok := FromEvent("paystack/main", c.event, c.fields)
		if !ok || e.Direction != c.dir || e.Kind != c.kind || e.Status != c.status || e.Reference != c.ref || e.Key() != c.key || e.check() != nil {
			t.Errorf("%s: %v %+v (key %s)", c.event, ok, e, e.Key())
		}
	}
	if e, _ := FromEvent("x/main", "virtual_account.credited", map[string]any{"reference": "VA", "amount_kobo": 700.0, "payer_name": "A"}); e.AmountKobo == nil || *e.AmountKobo != 700 || e.PayerName != "A" {
		t.Errorf("credit: %+v", e)
	}
	for _, bad := range []struct {
		event  string
		fields map[string]any
	}{{"payment.received", map[string]any{}}, {"something.else", map[string]any{"payment_id": "x"}}} {
		if _, ok := FromEvent("x/main", bad.event, bad.fields); ok {
			t.Errorf("%s logged", bad.event)
		}
	}
}
