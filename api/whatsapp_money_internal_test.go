package api

import "testing"

func TestParseMoneyCommand(t *testing.T) {
	for _, c := range []struct {
		in      string
		want    chatMoney
		command bool
		ok      bool
	}{
		{"spent 12,000 on diesel", chatMoney{Kind: "expense", Direction: "out", AmountKobo: 1200000, Description: "diesel", Category: "fuel"}, true, true},
		{"Spent 12k on diesel at Total", chatMoney{Kind: "expense", Direction: "out", AmountKobo: 1200000, Description: "diesel", Vendor: "Total", Category: "fuel"}, true, true},
		{"expense 5000 fuel", chatMoney{Kind: "expense", Direction: "out", AmountKobo: 500000, Description: "fuel", Category: "fuel"}, true, true},
		{"expense ₦2,500.50 for shop rent", chatMoney{Kind: "expense", Direction: "out", AmountKobo: 250050, Description: "shop rent", Category: "rent"}, true, true},
		{"spent 1500 on airtime at MTN shop", chatMoney{Kind: "expense", Direction: "out", AmountKobo: 150000, Description: "airtime", Vendor: "MTN shop", Category: "phone"}, true, true},
		{"spent 3000 on keke fare", chatMoney{Kind: "expense", Direction: "out", AmountKobo: 300000, Description: "keke fare", Category: "transport"}, true, true},
		{"spent 900 on printer paper", chatMoney{Kind: "expense", Direction: "out", AmountKobo: 90000, Description: "printer paper", Category: "other"}, true, true},
		{"cash in 20,000 till", chatMoney{Kind: "cash", Direction: "in", AmountKobo: 2000000, Description: "till"}, true, true},
		{"Cash out 3,000 float", chatMoney{Kind: "cash", Direction: "out", AmountKobo: 300000, Description: "float"}, true, true},
		{"cash in 5k", chatMoney{Kind: "cash", Direction: "in", AmountKobo: 500000, Description: "Cash in"}, true, true},
		// Commands that cannot be read are answered with how.
		{"spent 12000", chatMoney{}, true, false},
		{"spent lots on diesel", chatMoney{}, true, false},
		{"spent", chatMoney{}, true, false},
		{"cash in", chatMoney{}, true, false},
		{"spent 0.50 on gum", chatMoney{}, true, false},
		// Not commands at all.
		{"expense report every friday", chatMoney{}, false, false},
		{"cash up", chatMoney{}, false, false},
		{"invoice Ada 25000", chatMoney{}, false, false},
		{"", chatMoney{}, false, false},
	} {
		got, command, ok := parseMoneyCommand(c.in)
		if command != c.command || ok != c.ok || (ok && got != c.want) {
			t.Errorf("%q: %+v %v %v", c.in, got, command, ok)
		}
	}
}
