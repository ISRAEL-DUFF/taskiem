package saved

import (
	"reflect"
	"testing"
)

func TestRead(t *testing.T) {
	for _, c := range []struct {
		in   string
		want Reading
	}{
		{"GTBank 0123456789", Reading{Kind: KindBankAccount, Details: map[string]string{"bank_name": "Guaranty Trust Bank", "account_number": "0123456789"}}},
		{"Pay to:\nAccount name: Ada Obi\nZenith Bank\n0123 456 789", Reading{Kind: KindBankAccount, Title: "Ada Obi",
			Details: map[string]string{"bank_name": "Zenith Bank", "account_number": "0123456789"}}},
		{"ADA OBI\n2034567891\nUBA", Reading{Kind: KindBankAccount, Title: "ADA OBI", Details: map[string]string{"bank_name": "United Bank for Africa", "account_number": "2034567891"}}},
		{"My sister's account: Zenith Bank 2034567891, Account name: Ngozi Obi", Reading{Kind: KindBankAccount, Title: "Ngozi Obi",
			Details: map[string]string{"bank_name": "Zenith Bank", "account_number": "2034567891"}}},
		// A number with no bank: the bank is asked for.
		{"acct 0123456789", Reading{Kind: KindBankAccount, Details: map[string]string{"account_number": "0123456789"}}},
		// A phone's 11 digits are not an account number.
		{"Call me on 0803 123 4567", Reading{Kind: KindPerson, Details: map[string]string{"phone": "+2348031234567", "note": "Call me on"}}},
		// The words beside the phone are kept as the note.
		{"call me at 0803 123 4567 tomorrow", Reading{Kind: KindPerson, Details: map[string]string{"phone": "+2348031234567", "note": "call me at tomorrow"}}},
		{"ada@obi.ng +234 803 123 4567", Reading{Kind: KindPerson, Details: map[string]string{"phone": "+2348031234567", "email": "ada@obi.ng"}}},
		{"12 Herbert Macaulay Way, Yaba, opposite the filling station", Reading{Kind: KindPlace,
			Details: map[string]string{"line": "12 Herbert Macaulay Way", "area": "Yaba", "landmark": "opposite the filling station"}}},
		{"Deliver to:\n4 Allen Avenue, Ikeja\nCall 08031120099 at the gate", Reading{Kind: KindPlace,
			Details: map[string]string{"line": "4 Allen Avenue", "area": "Ikeja", "phone": "+2348031120099", "note": "Deliver to: Call at the gate"}}},
		{"Address: Plot 5, Lekki Phase 1", Reading{Kind: KindPlace, Details: map[string]string{"line": "Plot 5", "area": "Lekki Phase 1"}}},
		{"No refunds after 7 days", Reading{Kind: KindText, Details: map[string]string{"body": "No refunds after 7 days"}}},
		// A street word with no house number, or none at all: not a place.
		{"I'll close the shop by 6", Reading{Kind: KindText, Details: map[string]string{"body": "I'll close the shop by 6"}}},
		{"I'm on my way", Reading{Kind: KindText, Details: map[string]string{"body": "I'm on my way"}}},
		// A number with no bank and no word of an account: text.
		{"Order ref 1234567890", Reading{Kind: KindText, Details: map[string]string{"body": "Order ref 1234567890"}}},
		// A number alone: the bank is asked for.
		{"0123456789", Reading{Kind: KindBankAccount, Details: map[string]string{"account_number": "0123456789"}}},
		// "name" without its colon is not a name given.
		{"Name your price\nUBA 2034567891", Reading{Kind: KindBankAccount, Details: map[string]string{"bank_name": "United Bank for Africa", "account_number": "2034567891"}}},
		// Thanks are not a name.
		{"Thank you sir\nZenith 2034567891", Reading{Kind: KindBankAccount, Details: map[string]string{"bank_name": "Zenith Bank", "account_number": "2034567891"}}},
		// An account number before another number, spaced apart.
		{"GTBank 0123456789 08031234567", Reading{Kind: KindBankAccount, Details: map[string]string{"bank_name": "Guaranty Trust Bank", "account_number": "0123456789"}}},
		{"Zenith 0123 456 789 4455", Reading{Kind: KindBankAccount, Details: map[string]string{"bank_name": "Zenith Bank", "account_number": "0123456789"}}},
	} {
		if got := Read(c.in); !reflect.DeepEqual(got, c.want) {
			t.Errorf("Read(%q) = %+v, want %+v", c.in, got, c.want)
		}
	}
}
