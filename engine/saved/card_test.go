package saved

import (
	"errors"
	"testing"
)

func TestCards(t *testing.T) {
	c := MakeCard(KindBankAccount, "mum", "Mum", map[string]any{"bank_name": "Guaranty Trust Bank", "bank_code": "000013", "code_from": "iswallet/taskiem", "last4": "2222"}, "0123452222")
	if !c.Sensitive || c.Details["account_number"] != "0123452222" || c.Details["last4"] != nil || c.Details["bank_code"] != "000013" {
		t.Fatalf("bank card: %+v", c)
	}
	if got := c.Plain(); got != "Mum\nBank: Guaranty Trust Bank\nAccount number: 0123452222" {
		t.Fatalf("plain: %q", got)
	}
	if err := c.Check(); err != nil {
		t.Fatal(err)
	}

	p := MakeCard(KindPlace, "warehouse", "Our warehouse", map[string]any{"line": "12 Herbert Macaulay Way", "area": "Yaba"}, "")
	if p.Sensitive || len(p.Lines) != 2 || p.Plain() != "Our warehouse\nAddress: 12 Herbert Macaulay Way\nArea: Yaba" {
		t.Fatalf("place card: %+v %q", p, p.Plain())
	}
	if MakeCard(KindPlace, "w", "W", map[string]any{"line": "1 A St", "phone": "+2348031234567"}, "").Sensitive != true {
		t.Fatal("a phone is sensitive")
	}
	if pr := MakeCard(KindProviderAccount, "meter", "Shop meter", map[string]any{"type": "electricity", "provider": "Ikeja Electric", "number": "4501"}, ""); pr.Plain() != "Shop meter\nElectricity: Ikeja Electric\nNumber: 4501" || pr.Details["type"] != "electricity" {
		t.Fatalf("provider card: %+v", pr)
	}
	if tx := MakeCard(KindText, "terms", "Terms", map[string]any{"body": "No refunds\nafter 7 days"}, ""); tx.Plain() != "Terms\nNo refunds\nafter 7 days" {
		t.Fatalf("text card: %q", tx.Plain())
	}
	for _, bad := range []Card{{Kind: "car", Title: "x"}, {Kind: KindText, Title: ""}} {
		if err := bad.Check(); !errors.Is(err, ErrInvalid) {
			t.Errorf("%+v passed", bad)
		}
	}
}
