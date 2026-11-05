package links

import "testing"

// A supplier's bank is found by name among the banks a payments account
// pays, "Bank", "PLC" and punctuation set aside; two banks of the same
// name with different codes, or none, find nothing.
func TestBankCode(t *testing.T) {
	banks := []map[string]any{
		{"name": "Wema Bank Plc", "code": "035"},
		{"bank_name": "Guaranty Trust Bank", "bank_code": "058"},
		{"bankName": "First Bank of Nigeria", "bankCode": "011"},
		{"name": "Access Bank", "code": "044"},
		{"name": "Access Bank (Diamond)", "code": "063"},
	}
	for name, want := range map[string]string{"Wema Bank": "035", "WEMA BANK PLC": "035", "Guaranty Trust Bank Limited": "058", "First Bank": "011", "Access Bank": "044"} {
		if got, ok := BankCode(banks, name); !ok || got != want {
			t.Errorf("%s: %q %v, want %s", name, got, ok, want)
		}
	}
	for _, name := range []string{"Zenith Bank", "", "Bank"} {
		if got, ok := BankCode(banks, name); ok {
			t.Errorf("%s: found %q", name, got)
		}
	}
	twice := append(banks, map[string]any{"name": "Wema", "code": "999"})
	if got, ok := BankCode(twice, "Wema Bank"); ok {
		t.Errorf("two codes for one name: %q", got)
	}
}
