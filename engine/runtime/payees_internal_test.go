package runtime

import "testing"

// A payout step remembers only the payees money went to.
func TestPaidPayees(t *testing.T) {
	ada := map[string]any{"account_number": "0123456789"}
	bola := map[string]any{"account_number": "0987654321"}
	batch := map[string]any{"items": []any{ada, bola}}
	result := func(statuses ...string) map[string]any {
		var items []any
		for _, s := range statuses {
			items = append(items, map[string]any{"payout_id": "po", "status": s})
		}
		return map[string]any{"batch_id": "b", "items": items}
	}
	for _, c := range []struct {
		name    string
		uses    string
		in, out map[string]any
		want    int
	}{
		{"one paid", "pay_bank", ada, map[string]any{"status": "pending"}, 1},
		{"one failed", "pay_bank", ada, map[string]any{"status": "failed"}, 0},
		{"no output", "pay_bank", ada, nil, 0},
		{"batch all paid", "pay_many", batch, result("settled", "pending"), 2},
		{"batch one failed", "pay_many", batch, result("failed", "settled"), 1},
		{"batch result short", "pay_many", batch, result("settled"), 0},
	} {
		got := paidPayees(c.uses, c.in, c.out)
		if len(got) != c.want {
			t.Errorf("%s: %d payees, want %d", c.name, len(got), c.want)
		}
	}
	if got := paidPayees("pay_many", batch, result("failed", "settled")); len(got) != 1 || got[0]["account_number"] != "0987654321" {
		t.Errorf("the one that failed is remembered: %v", got)
	}
}
