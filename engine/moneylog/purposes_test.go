package moneylog

import "testing"

// A payment's purpose as the money log's category, and its fee as a line
// of its own keyed apart from the payout (paying people H7).
func TestPurposeCategory(t *testing.T) {
	for _, c := range []struct{ purpose, note, want string }{
		{"delivery", "", "delivery"}, {"wages", "x", "wages"}, {"stock", "", "stock bought"}, {"bill", "", "bill"},
		{"refund", "", "refund"}, {"other", "Generator Fuel", "generator fuel"}, {"other", "", "other"},
	} {
		if got := PurposeCategory(c.purpose, c.note); got != c.want {
			t.Errorf("%s %q: %q, want %q", c.purpose, c.note, got, c.want)
		}
	}
	fee := FeeEntry("paystack/main", "po_1", 2500, "Fee: Paid Chidi")
	payout, _ := FromEvent("paystack/main", "payout.settled", map[string]any{"payout_id": "po_1", "amount_kobo": float64(4500000)})
	if fee.Key() == payout.Key() || fee.Kind != "fee" || fee.Category != CategoryBankCharges || *fee.AmountKobo != 2500 || fee.check() != nil {
		t.Fatalf("the fee's line: %+v (%s)", fee, fee.Key())
	}
}
