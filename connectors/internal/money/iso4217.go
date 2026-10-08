package money

import "strings"

// minorDigits is the ISO 4217 minor-unit exponent of the currencies mobile
// money connectors meet: the number of decimal places between a currency's
// major unit (shilling, cedi, franc) and the minor unit Taskiem amounts are
// counted in. Currencies with no minor unit in use (UGX, XAF, XOF, RWF,
// GNF) have 0, so their Taskiem amounts are whole francs or shillings.
var minorDigits = map[string]int{
	"BIF": 0, "CDF": 2, "EUR": 2, "GHS": 2, "GNF": 0, "KES": 2, "LRD": 2,
	"MGA": 2, "MWK": 2, "NGN": 2, "RWF": 0, "SCR": 2, "SSP": 2, "SZL": 2,
	"TZS": 2, "UGX": 0, "USD": 2, "XAF": 0, "XOF": 0, "ZAR": 2, "ZMW": 2,
}

// Scale returns the ISO 4217 minor-unit exponent of currency, and whether
// it is known. Connectors refuse amounts in currencies they cannot scale
// rather than guess.
func Scale(currency string) (int, bool) {
	n, ok := minorDigits[strings.ToUpper(strings.TrimSpace(currency))]
	return n, ok
}
