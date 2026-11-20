package saved

import (
	"errors"
	"testing"
)

func TestName(t *testing.T) {
	for typed, want := range map[string]string{
		"mum": "mum", "@Mum": "mum", " Bola Stores ": "bola-stores", "bank_details": "bank-details", "a1": "a1",
		"abcdefghijklmnopqrstuvwxyz012345": "abcdefghijklmnopqrstuvwxyz012345",
	} {
		if got, err := Name(typed); err != nil || got != want {
			t.Errorf("Name(%q) = %q, %v; want %q", typed, got, err, want)
		}
	}
	for _, typed := range []string{"", "@", "a", "-mum", "mum-", "bola--stores", "mum!", "abcdefghijklmnopqrstuvwxyz0123456", "ọmọ"} {
		if got, err := Name(typed); !errors.Is(err, ErrInvalid) {
			t.Errorf("Name(%q) = %q, %v; want refused", typed, got, err)
		}
	}
}

func TestTitle(t *testing.T) {
	if got, err := Title("  Bola   Stores "); err != nil || got != "Bola Stores" {
		t.Fatalf("%q %v", got, err)
	}
	if _, err := Title("  "); !errors.Is(err, ErrInvalid) || Plain(err) != "Give it a title, like Mum or Bola Stores." {
		t.Fatalf("%v / %q", err, Plain(err))
	}
}

func TestBankAccount(t *testing.T) {
	b, err := BankAccount{BankName: " Guaranty  Trust Bank", BankCode: "058", CodeFrom: "paystack/main", AccountNumber: "012 345-6789", Note: " rice "}.Clean()
	if err != nil {
		t.Fatal(err)
	}
	if b.AccountNumber != "0123456789" || b.BankName != "Guaranty Trust Bank" || b.Note != "rice" {
		t.Fatalf("%+v", b)
	}
	if h := b.Hint(); h != "GTBank ••6789" {
		t.Fatalf("hint %q", h)
	}
	d := b.Details()
	if _, ok := d["account_number"]; ok || d["last4"] != "6789" {
		t.Fatalf("details %v", d)
	}
	if back := FromDetails(d); back.BankCode != "058" || back.CodeFrom != "paystack/main" || back.AccountNumber != "" {
		t.Fatalf("%+v", back)
	}
	// A bank chosen by name alone keeps no list.
	if b, _ := (BankAccount{BankName: "Zenith Bank", CodeFrom: "x", AccountNumber: "1234567890"}).Clean(); b.CodeFrom != "" {
		t.Fatalf("%+v", b)
	}
	for _, bad := range []BankAccount{
		{BankName: "", AccountNumber: "0123456789"},
		{BankName: "Zenith Bank", AccountNumber: "12345"},
		{BankName: "Zenith Bank", AccountNumber: "01234567890"},
		{BankName: "Zenith Bank", BankCode: "05 8", AccountNumber: "0123456789"},
	} {
		if _, err := bad.Clean(); !errors.Is(err, ErrInvalid) {
			t.Errorf("%+v passed", bad)
		}
	}
}

func TestShortBank(t *testing.T) {
	for name, want := range map[string]string{
		"Guaranty Trust Bank": "GTBank", "United Bank for Africa": "UBA", "Zenith Bank": "Zenith Bank",
		"Sparkle Microfinance Bank Limited": "Sparkle Microfinance", "Bank": "Bank", "Some Bank PLC": "Some",
	} {
		if got := ShortBank(name); got != want {
			t.Errorf("ShortBank(%q) = %q, want %q", name, got, want)
		}
	}
}
