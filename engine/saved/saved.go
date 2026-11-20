// Package saved is saved objects: things a person or a business keeps and
// calls up anywhere by @name (docs/spec/saved-objects.md; decision 0071).
//
// S0 of the build plan (docs/spec/build-plan-saved-tools-payments.md) made
// the bank account: its account number sealed and shown masked, the bank's
// name for it checked through a payments connection when one is there. S1
// adds the other kinds Taskiem knows (kinds.go), the picker and cards.
//
// This package holds what an object is and the rules for its fields; the
// store, its owners' rules and the name check are the API's (api/saved.go),
// on the tables of migration 00491.
package saved

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"unicode"
)

// Kinds Taskiem knows (saved-objects §4.1): the bank account (S0), and,
// from S1, a person, a place, a text and an account with a provider. Files
// and kinds an owner makes come later in S1.
const (
	KindBankAccount     = "bank_account"
	KindPerson          = "person"
	KindPlace           = "place"
	KindText            = "text"
	KindProviderAccount = "provider_account"
)

// Kinds are the kinds an object may be, in the order the Saved page lists
// them.
var Kinds = []string{KindBankAccount, KindPerson, KindPlace, KindText, KindProviderAccount}

// Check states: the bank's name for an account was given by a payments
// connection (checked), or has not been yet (unchecked).
const (
	Unchecked = "unchecked"
	Checked   = "checked"
	// NoCheck is the state of every kind but the bank account.
	NoCheck = "none"
)

// Limits (saved-objects §8, §15 6): a person keeps up to 500 objects; a
// business up to 2,000 on the lowest plan (higher plans come with plan
// limits).
const (
	MaxPerson   = 500
	MaxBusiness = 2000
	// MaxTitle and MaxNote are the longest title and note.
	MaxTitle = 80
	MaxNote  = 200
	// MaxBankName is the longest bank name.
	MaxBankName = 60
)

// ErrInvalid is a field that breaks a rule; the message says which, in
// plain words for the person.
var ErrInvalid = errors.New("saved: invalid")

func invalid(msg string) error { return fmt.Errorf("%w: %s", ErrInvalid, msg) }

// Plain is what a person reads for err: the words after the error's kind.
func Plain(err error) string {
	msg := err.Error()
	if i := strings.Index(msg, ErrInvalid.Error()+": "); i >= 0 {
		return msg[i+len(ErrInvalid.Error())+2:]
	}
	return msg
}

var nameRule = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,30}[a-z0-9]$`)

// Name is a name as a person types it, made ready to keep: without the @,
// lowercase, spaces and underscores as "-". It is refused unless it is 2
// to 32 lowercase letters, digits and single "-" between them.
func Name(typed string) (string, error) {
	n := strings.ToLower(strings.TrimSpace(typed))
	n = strings.TrimPrefix(n, "@")
	n = strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) || r == '_' {
			return '-'
		}
		return r
	}, n)
	switch {
	case n == "":
		return "", invalid("Give it a short name to call it up by, like @mum or @bayo.")
	case len(n) < 2 || len(n) > 32:
		return "", invalid("A name is 2 to 32 letters or numbers, like @mum or @bola-stores.")
	case !nameRule.MatchString(n) || strings.Contains(n, "--"):
		return "", invalid("A name uses only letters, numbers and \"-\" between them, like @bola-stores.")
	}
	return n, nil
}

// Title is the title as shown ("Mum", "Bola Stores"): spaces folded,
// 1 to 80 characters.
func Title(typed string) (string, error) {
	t := strings.Join(strings.Fields(typed), " ")
	switch {
	case t == "":
		return "", invalid("Give it a title, like Mum or Bola Stores.")
	case len([]rune(t)) > MaxTitle:
		return "", invalid("The title is too long.")
	}
	return t, nil
}

// BankAccount is a bank account object's details. AccountNumber is
// sensitive: it is sealed, and never kept in Details or sent to the AI.
type BankAccount struct {
	// BankName is the bank as the person chose it ("Guaranty Trust Bank").
	BankName string `json:"bank_name"`
	// BankCode is the bank's code in the list it was chosen from, and
	// CodeFrom that list: a payments connection ("iswallet/taskiem"), or
	// "" when it was chosen by name alone (no connection yet). A check
	// through another connection finds the bank by name in its own list.
	BankCode string `json:"bank_code,omitempty"`
	CodeFrom string `json:"code_from,omitempty"`
	// Note is the owner's own words ("pays for the rice").
	Note string `json:"note,omitempty"`
	// AccountNumber: the 10-digit NUBAN.
	AccountNumber string `json:"-"`
}

var tenDigits = regexp.MustCompile(`^[0-9]{10}$`)

// AccountNumber is an account number as typed, without spaces or "-",
// refused unless it is 10 digits.
func AccountNumber(typed string) (string, error) {
	n := strings.NewReplacer(" ", "", "-", "", ".", "").Replace(strings.TrimSpace(typed))
	if !tenDigits.MatchString(n) {
		return "", invalid("An account number is 10 digits.")
	}
	return n, nil
}

// Clean is the account made ready to keep, or why it is refused.
func (b BankAccount) Clean() (BankAccount, error) {
	var err error
	b.BankName = strings.Join(strings.Fields(b.BankName), " ")
	b.BankCode, b.CodeFrom = strings.TrimSpace(b.BankCode), strings.TrimSpace(b.CodeFrom)
	b.Note = strings.Join(strings.Fields(b.Note), " ")
	switch {
	case b.BankName == "":
		return b, invalid("Choose the bank.")
	case len([]rune(b.BankName)) > MaxBankName:
		return b, invalid("The bank's name is too long.")
	case len(b.BankCode) > 12 || strings.Trim(b.BankCode, "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz") != "":
		return b, invalid("Choose the bank from the list.")
	case len([]rune(b.Note)) > MaxNote:
		return b, invalid("The note is too long.")
	}
	if b.BankCode == "" {
		b.CodeFrom = ""
	}
	if b.AccountNumber, err = AccountNumber(b.AccountNumber); err != nil {
		return b, err
	}
	return b, nil
}

// Details are the account's fields that are not sensitive, as kept: the
// number only by its last four digits, as every list shows it.
func (b BankAccount) Details() map[string]any {
	d := map[string]any{"bank_name": b.BankName, "last4": Last4(b.AccountNumber)}
	if b.BankCode != "" {
		d["bank_code"], d["code_from"] = b.BankCode, b.CodeFrom
	}
	if b.Note != "" {
		d["note"] = b.Note
	}
	return d
}

// FromDetails reads an account's kept fields back (without its number).
func FromDetails(d map[string]any) BankAccount {
	s := func(k string) string { v, _ := d[k].(string); return v }
	return BankAccount{BankName: s("bank_name"), BankCode: s("bank_code"), CodeFrom: s("code_from"), Note: s("note")}
}

// Last4 is the end of an account number, as lists show it.
func Last4(number string) string {
	if len(number) <= 4 {
		return number
	}
	return number[len(number)-4:]
}

// Masked is an account number as everyone may see it: "••6789".
func Masked(number string) string { return "••" + Last4(number) }

// Hint is what a list shows for the account: the bank's short name and
// the masked number ("GTBank ••6789").
func (b BankAccount) Hint() string {
	return ShortBank(b.BankName) + " " + Masked(b.AccountNumber)
}

// ShortBank is a bank's name as a row has room for: the name people use
// for the best-known banks, else the name without "Bank", "PLC",
// "Limited" or "Nigeria" at its end.
func ShortBank(name string) string {
	if s, ok := shortNames[strings.ToLower(strings.Join(strings.Fields(name), " "))]; ok {
		return s
	}
	words := strings.Fields(name)
	for len(words) > 1 {
		switch strings.ToLower(strings.Trim(words[len(words)-1], ".,()")) {
		case "plc", "limited", "ltd", "nigeria", "bank":
			words = words[:len(words)-1]
			continue
		}
		break
	}
	return strings.Join(words, " ")
}

var shortNames = map[string]string{
	"guaranty trust bank":           "GTBank",
	"guaranty trust bank plc":       "GTBank",
	"united bank for africa":        "UBA",
	"united bank for africa plc":    "UBA",
	"first bank of nigeria":         "First Bank",
	"first bank of nigeria limited": "First Bank",
	"first city monument bank":      "FCMB",
	"stanbic ibtc bank":             "Stanbic IBTC",
	"standard chartered bank":       "Standard Chartered",
	"moniepoint microfinance bank":  "Moniepoint",
	"kuda microfinance bank":        "Kuda",
	"opay digital services limited": "OPay",
	"palmpay limited":               "PalmPay",
	"union bank of nigeria":         "Union Bank",
	"ecobank nigeria":               "Ecobank",
	"citibank nigeria":              "Citibank",
	"premiumtrust bank":             "PremiumTrust",
	"taj bank":                      "Taj Bank",
	"jaiz bank":                     "Jaiz Bank",
	"wema bank":                     "Wema Bank",
	"zenith bank":                   "Zenith Bank",
	"access bank":                   "Access Bank",
	"fidelity bank":                 "Fidelity Bank",
	"sterling bank":                 "Sterling Bank",
	"polaris bank":                  "Polaris Bank",
	"keystone bank":                 "Keystone Bank",
	"unity bank":                    "Unity Bank",
	"providus bank":                 "Providus Bank",
	"globus bank":                   "Globus Bank",
	"titan trust bank":              "Titan Trust",
	"lotus bank":                    "Lotus Bank",
	"parallex bank":                 "Parallex Bank",
	"suntrust bank":                 "SunTrust Bank",
	"optimus bank":                  "Optimus Bank",
	"signature bank":                "Signature Bank",
}

// Banks are the banks offered by name when the business has no payments
// connection to list its own (or for a person's own account): the
// commercial banks and the best-known digital banks, by the names the
// payments connections use. A bank chosen from here is found by name in a
// connection's own list when the account is checked.
var Banks = []string{
	"Access Bank", "Citibank Nigeria", "Ecobank Nigeria", "Fidelity Bank", "First Bank of Nigeria",
	"First City Monument Bank", "Globus Bank", "Guaranty Trust Bank", "Jaiz Bank", "Keystone Bank",
	"Kuda Microfinance Bank", "Lotus Bank", "Moniepoint Microfinance Bank", "OPay Digital Services Limited",
	"Optimus Bank", "PalmPay Limited", "Parallex Bank", "Polaris Bank", "PremiumTrust Bank", "Providus Bank",
	"Signature Bank", "Stanbic IBTC Bank", "Standard Chartered Bank", "Sterling Bank", "SunTrust Bank",
	"Taj Bank", "Titan Trust Bank", "Union Bank of Nigeria", "United Bank for Africa", "Unity Bank",
	"Wema Bank", "Zenith Bank",
}
