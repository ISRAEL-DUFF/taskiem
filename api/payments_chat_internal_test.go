package api

import "testing"

// Payment words read as the Pay topic's commands make them and as people
// write them; what is not a payment is left alone.
func TestReadPayWords(t *testing.T) {
	for _, c := range []struct {
		in   string
		want payCommand
	}{
		{"send 45k to @chidi for a delivery", payCommand{AmountKobo: 4500000, Payee: "chidi", Purpose: "delivery"}},
		{"pay @chidi 45,000 for wages", payCommand{AmountKobo: 4500000, Payee: "chidi", Purpose: "wages"}},
		{"send 45k to @chidi for a delivery: rice to Ikeja", payCommand{AmountKobo: 4500000, Payee: "chidi", Purpose: "delivery", Note: "rice to Ikeja"}},
		{"send 2000 to chidi", payCommand{AmountKobo: 200000, Payee: "chidi", Purpose: "other"}},
		{"transfer 45k to 0123456789 gtbank for stock bought", payCommand{AmountKobo: 4500000, AccountNumber: "0123456789", BankName: "Guaranty Trust Bank", Purpose: "stock"}},
		{"send 5k to 0123 456 789 first bank for the generator", payCommand{AmountKobo: 500000, AccountNumber: "0123456789", BankName: "First Bank of Nigeria", Purpose: "other", PurposeNote: "the generator"}},
		{"send 5k to 0123456789", payCommand{AmountKobo: 500000, AccountNumber: "0123456789", Purpose: "other"}},
	} {
		got, ok := readPayWords(c.in)
		if !ok || got != c.want {
			t.Errorf("%q: %+v %v, want %+v", c.in, got, ok, c.want)
		}
	}
	for _, in := range []string{
		"send invoice to Ada 5000", "send 2 bags to Ada Obi", "pay TK-A1B2C3", "transfer stock to the shop",
		"send 5k to 01234 for rice", "pay @chidi", "send 5k to Ada Obi",
	} {
		if got, ok := readPayWords(in); ok {
			t.Errorf("%q read as a payment: %+v", in, got)
		}
	}
}
