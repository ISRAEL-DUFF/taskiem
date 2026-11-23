package api

import (
	"testing"

	"github.com/israel-duff/taskiem/engine/lang"
)

// The other languages' expense and cash words (draft lists in
// engine/lang): only with an amount right after them.
func TestMoneyWordsPerLanguage(t *testing.T) {
	for _, c := range []struct {
		tag     lang.Tag
		in      string
		want    chatMoney
		command bool
		ok      bool
	}{
		{lang.PCM, "I spend 12,000 for diesel at Total", chatMoney{Kind: "expense", Direction: "out", AmountKobo: 1200000, Description: "diesel", Vendor: "Total", Label: "Diesel"}, true, true},
		{lang.PCM, "i don spend 1500 on airtime", chatMoney{Kind: "expense", Direction: "out", AmountKobo: 150000, Description: "airtime", Label: "Airtime"}, true, true},
		{lang.PCM, "cash enter 20,000 till", chatMoney{Kind: "cash", Direction: "in", AmountKobo: 2000000, Description: "till"}, true, true},
		{lang.PCM, "money comot 3k", chatMoney{Kind: "cash", Direction: "out", AmountKobo: 300000, Description: "Cash out"}, true, true},
		{lang.PCM, "I spend the whole day for market", chatMoney{}, false, false},
		{lang.YO, "Mo ná 12,000 fún diesel", chatMoney{Kind: "expense", Direction: "out", AmountKobo: 1200000, Description: "diesel", Label: "Diesel"}, true, true},
		{lang.YO, "ìnáwó 5000 epo", chatMoney{Kind: "expense", Direction: "out", AmountKobo: 500000, Description: "epo", Label: "Epo"}, true, true},
		{lang.YO, "owo wole 20000", chatMoney{Kind: "cash", Direction: "in", AmountKobo: 2000000, Description: "Cash in"}, true, true},
		{lang.YO, "owó jáde 3,000 float", chatMoney{Kind: "cash", Direction: "out", AmountKobo: 300000, Description: "float"}, true, true},
		{lang.YO, "mo ná 12000", chatMoney{}, true, false},
		{lang.HA, "na kashe 12,000 don diesel", chatMoney{Kind: "expense", Direction: "out", AmountKobo: 1200000, Description: "diesel", Label: "Diesel"}, true, true},
		{lang.HA, "kuɗi ya shigo 20,000", chatMoney{Kind: "cash", Direction: "in", AmountKobo: 2000000, Description: "Cash in"}, true, true},
		{lang.HA, "kudi ya fita 3000 float", chatMoney{Kind: "cash", Direction: "out", AmountKobo: 300000, Description: "float"}, true, true},
		{lang.HA, "na kashe kudi da yawa", chatMoney{}, false, false},
		{lang.IG, "emefuru m 12,000 maka diesel", chatMoney{Kind: "expense", Direction: "out", AmountKobo: 1200000, Description: "diesel", Label: "Diesel"}, true, true},
		{lang.IG, "mmefu 2,500 rent", chatMoney{Kind: "expense", Direction: "out", AmountKobo: 250000, Description: "rent", Label: "Rent"}, true, true},
		{lang.IG, "ego batara 20000 till", chatMoney{Kind: "cash", Direction: "in", AmountKobo: 2000000, Description: "till"}, true, true},
		{lang.IG, "ego pụrụ 3000", chatMoney{Kind: "cash", Direction: "out", AmountKobo: 300000, Description: "Cash out"}, true, true},
		// Bought, as English's "bought": the amount first or last; a
		// quantity in a unit is stock, not spending.
		{lang.PCM, "I buy snacks for 8,000", chatMoney{Kind: "expense", Direction: "out", AmountKobo: 800000, Description: "snacks", Label: "Snacks"}, true, true},
		{lang.PCM, "we don buy 2,500 airtime", chatMoney{Kind: "expense", Direction: "out", AmountKobo: 250000, Description: "airtime", Label: "Airtime"}, true, true},
		{lang.PCM, "i buy 10 bags of rice for 1,500", chatMoney{}, false, false},
		{lang.PCM, "i buy am yesterday", chatMoney{}, false, false},
		{lang.YO, "mo rà epo fún 5,000", chatMoney{Kind: "expense", Direction: "out", AmountKobo: 500000, Description: "epo", Label: "Epo"}, true, true},
		{lang.HA, "na saya 3,000 don mai", chatMoney{Kind: "expense", Direction: "out", AmountKobo: 300000, Description: "mai", Label: "Mai"}, true, true},
		{lang.IG, "m zụrụ 4,000 maka gas", chatMoney{Kind: "expense", Direction: "out", AmountKobo: 400000, Description: "gas", Label: "Gas"}, true, true},
	} {
		m, matched := lang.MatchIntent(c.in, []lang.Tag{c.tag, lang.EN})
		if !matched || m.Language != c.tag {
			t.Errorf("%s %q: matched %+v %v", c.tag, c.in, m, matched)
			continue
		}
		got, command, ok := parseMoneyWords(m)
		if command != c.command || ok != c.ok || (ok && got != c.want) {
			t.Errorf("%s %q: %+v %v %v", c.tag, c.in, got, command, ok)
		}
	}
}

func TestParseMoneyCommand(t *testing.T) {
	for _, c := range []struct {
		in      string
		want    chatMoney
		command bool
		ok      bool
	}{
		{"spent 12,000 on diesel", chatMoney{Kind: "expense", Direction: "out", AmountKobo: 1200000, Description: "diesel", Label: "Diesel"}, true, true},
		{"Spent 12k on diesel at Total", chatMoney{Kind: "expense", Direction: "out", AmountKobo: 1200000, Description: "diesel", Vendor: "Total", Label: "Diesel"}, true, true},
		{"expense 5000 fuel", chatMoney{Kind: "expense", Direction: "out", AmountKobo: 500000, Description: "fuel", Label: "Fuel"}, true, true},
		{"expense ₦2,500.50 for shop rent", chatMoney{Kind: "expense", Direction: "out", AmountKobo: 250050, Description: "shop rent", Label: "Shop rent"}, true, true},
		{"spent 1500 on airtime at MTN shop", chatMoney{Kind: "expense", Direction: "out", AmountKobo: 150000, Description: "airtime", Vendor: "MTN shop", Label: "Airtime"}, true, true},
		{"spent 3000 on keke fare", chatMoney{Kind: "expense", Direction: "out", AmountKobo: 300000, Description: "keke fare", Label: "Keke fare"}, true, true},
		{"spent 900 on printer paper", chatMoney{Kind: "expense", Direction: "out", AmountKobo: 90000, Description: "printer paper", Label: "Printer paper"}, true, true},
		// "paid 15,000 diesel" is labelled Diesel (what a business buys §6).
		{"paid 15,000 diesel", chatMoney{Kind: "expense", Direction: "out", AmountKobo: 1500000, Description: "diesel", Label: "Diesel"}, true, true},
		{"Paid 2,000 for staff lunch", chatMoney{Kind: "expense", Direction: "out", AmountKobo: 200000, Description: "staff lunch", Label: "Staff lunch"}, true, true},
		{"cash in 20,000 till", chatMoney{Kind: "cash", Direction: "in", AmountKobo: 2000000, Description: "till"}, true, true},
		{"Cash out 3,000 float", chatMoney{Kind: "cash", Direction: "out", AmountKobo: 300000, Description: "float"}, true, true},
		{"cash in 5k", chatMoney{Kind: "cash", Direction: "in", AmountKobo: 500000, Description: "Cash in"}, true, true},
		// Bought and buy are spending words with an amount, first or
		// last, labelled as paid's are.
		{"I bought snacks for 8,000", chatMoney{Kind: "expense", Direction: "out", AmountKobo: 800000, Description: "snacks", Label: "Snacks"}, true, true},
		{"we bought a birthday gift for Ada 15,000", chatMoney{Kind: "expense", Direction: "out", AmountKobo: 1500000, Description: "a birthday gift for Ada", Label: "Birthday gift for Ada"}, true, true},
		{"bought 12,000 diesel at Total", chatMoney{Kind: "expense", Direction: "out", AmountKobo: 1200000, Description: "diesel", Vendor: "Total", Label: "Diesel"}, true, true},
		{"Bought 2,000 on staff lunch", chatMoney{Kind: "expense", Direction: "out", AmountKobo: 200000, Description: "staff lunch", Label: "Staff lunch"}, true, true},
		{"buy 1500 airtime", chatMoney{Kind: "expense", Direction: "out", AmountKobo: 150000, Description: "airtime", Label: "Airtime"}, true, true},
		{"buy fuel for 5k", chatMoney{Kind: "expense", Direction: "out", AmountKobo: 500000, Description: "fuel", Label: "Fuel"}, true, true},
		{"bought printer paper at Shoprite 4,500", chatMoney{Kind: "expense", Direction: "out", AmountKobo: 450000, Description: "printer paper", Vendor: "Shoprite", Label: "Printer paper"}, true, true},
		{"we bought cement for 60,000 from Dangote", chatMoney{Kind: "expense", Direction: "out", AmountKobo: 6000000, Description: "cement", Vendor: "Dangote", Label: "Cement"}, true, true},
		{"we bought from Dangote 425,000", chatMoney{Kind: "expense", Direction: "out", AmountKobo: 42500000, Description: "Dangote", Label: "Dangote"}, true, true},
		{"bought 15,000 worth of fuel", chatMoney{Kind: "expense", Direction: "out", AmountKobo: 1500000, Description: "worth of fuel", Label: "Fuel"}, true, true},
		{"bought 5 snacks for 1,000", chatMoney{Kind: "expense", Direction: "out", AmountKobo: 100000, Description: "5 snacks", Label: "5 snacks"}, true, true},
		{"spent 5,000 on a birthday cake", chatMoney{Kind: "expense", Direction: "out", AmountKobo: 500000, Description: "a birthday cake", Label: "Birthday cake"}, true, true},
		{"bought 5,000", chatMoney{}, true, false},
		// Commands that cannot be read are answered with how.
		{"spent 12000", chatMoney{}, true, false},
		{"spent lots on diesel", chatMoney{}, true, false},
		{"spent", chatMoney{}, true, false},
		{"cash in", chatMoney{}, true, false},
		{"spent 0.50 on gum", chatMoney{}, true, false},
		// Not commands at all.
		{"expense report every friday", chatMoney{}, false, false},
		{"paid Ada's invoice", chatMoney{}, false, false},
		{"cash up", chatMoney{}, false, false},
		{"invoice Ada 25000", chatMoney{}, false, false},
		{"", chatMoney{}, false, false},
		// Bought with no amount is chat, as "paid Ada's invoice" is; a
		// quantity in a unit is stock counted in (stock.ParseCountIn).
		{"bought", chatMoney{}, false, false},
		{"I bought new shoes for Ada", chatMoney{}, false, false},
		{"buy rice tomorrow", chatMoney{}, false, false},
		{"bought 5 snacks", chatMoney{}, false, false},
		{"we bought 10 bags of rice at 1,500", chatMoney{}, false, false},
		{"bought 5 cartons of Indomie", chatMoney{}, false, false},
		{"buy 10 bags of rice", chatMoney{}, false, false},
		{"I buy 8,000 snacks", chatMoney{}, false, false},
		{"buyers 5,000", chatMoney{}, false, false},
	} {
		got, command, ok := parseMoneyCommand(c.in)
		if command != c.command || ok != c.ok || (ok && got != c.want) {
			t.Errorf("%q: %+v %v %v", c.in, got, command, ok)
		}
	}
}
