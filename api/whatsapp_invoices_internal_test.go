package api

import "testing"

func TestParseInvoiceCommand(t *testing.T) {
	for _, c := range []struct {
		in   string
		name string
		kobo int64
		what string
		ok   bool
	}{
		{"invoice Ada 25,000 for 5 bags of rice", "Ada", 2500000, "5 bags of rice", true},
		{"Invoice Bola Ade ₦25,000.50 for rice", "Bola Ade", 2500050, "rice", true},
		{"bill Chi 10k", "Chi", 1000000, "", true},
		{"invoice Chi N2500.5", "Chi", 250050, "", true},
		{"invoice Chi ngn1,250", "Chi", 125000, "", true},
		{"invoice Ada", "", 0, "", false},
		{"invoice 25000", "", 0, "", false},
		{"invoice Ada lots for rice", "", 0, "", false},
		{"invoice Ada 0.50", "", 0, "", false},
		{"invoices", "", 0, "", false},
		{"pay Ada 25000", "", 0, "", false},
	} {
		name, kobo, what, ok := parseInvoiceCommand(c.in)
		if ok != c.ok || name != c.name || kobo != c.kobo || what != c.what {
			t.Errorf("%q: %q %d %q %v", c.in, name, kobo, what, ok)
		}
	}
}
