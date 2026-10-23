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
		{lang.PCM, "I spend 12,000 for diesel at Total", chatMoney{Kind: "expense", Direction: "out", AmountKobo: 1200000, Description: "diesel", Vendor: "Total", Category: "fuel"}, true, true},
		{lang.PCM, "i don spend 1500 on airtime", chatMoney{Kind: "expense", Direction: "out", AmountKobo: 150000, Description: "airtime", Category: "phone"}, true, true},
		{lang.PCM, "cash enter 20,000 till", chatMoney{Kind: "cash", Direction: "in", AmountKobo: 2000000, Description: "till"}, true, true},
		{lang.PCM, "money comot 3k", chatMoney{Kind: "cash", Direction: "out", AmountKobo: 300000, Description: "Cash out"}, true, true},
		{lang.PCM, "I spend the whole day for market", chatMoney{}, false, false},
		{lang.YO, "Mo ná 12,000 fún diesel", chatMoney{Kind: "expense", Direction: "out", AmountKobo: 1200000, Description: "diesel", Category: "fuel"}, true, true},
		{lang.YO, "ìnáwó 5000 epo", chatMoney{Kind: "expense", Direction: "out", AmountKobo: 500000, Description: "epo", Category: "other"}, true, true},
		{lang.YO, "owo wole 20000", chatMoney{Kind: "cash", Direction: "in", AmountKobo: 2000000, Description: "Cash in"}, true, true},
		{lang.YO, "owó jáde 3,000 float", chatMoney{Kind: "cash", Direction: "out", AmountKobo: 300000, Description: "float"}, true, true},
		{lang.YO, "mo ná 12000", chatMoney{}, true, false},
		{lang.HA, "na kashe 12,000 don diesel", chatMoney{Kind: "expense", Direction: "out", AmountKobo: 1200000, Description: "diesel", Category: "fuel"}, true, true},
		{lang.HA, "kuɗi ya shigo 20,000", chatMoney{Kind: "cash", Direction: "in", AmountKobo: 2000000, Description: "Cash in"}, true, true},
		{lang.HA, "kudi ya fita 3000 float", chatMoney{Kind: "cash", Direction: "out", AmountKobo: 300000, Description: "float"}, true, true},
		{lang.HA, "na kashe kudi da yawa", chatMoney{}, false, false},
		{lang.IG, "emefuru m 12,000 maka diesel", chatMoney{Kind: "expense", Direction: "out", AmountKobo: 1200000, Description: "diesel", Category: "fuel"}, true, true},
		{lang.IG, "mmefu 2,500 rent", chatMoney{Kind: "expense", Direction: "out", AmountKobo: 250000, Description: "rent", Category: "rent"}, true, true},
		{lang.IG, "ego batara 20000 till", chatMoney{Kind: "cash", Direction: "in", AmountKobo: 2000000, Description: "till"}, true, true},
		{lang.IG, "ego pụrụ 3000", chatMoney{Kind: "cash", Direction: "out", AmountKobo: 300000, Description: "Cash out"}, true, true},
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
