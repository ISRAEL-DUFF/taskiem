package api

import (
	"bytes"
	"encoding/base64"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/israel-duff/taskiem/engine/invoicing"
	"github.com/israel-duff/taskiem/engine/lang"
)

// An invoice button's payload is signed: it cannot be made, altered or
// moved to another business, invoice or contact without the key.
func TestInvoiceButtonPayload(t *testing.T) {
	key := bytes.Repeat([]byte{7}, 32)
	b := invoiceButton{Action: invoicePay, Tenant: uuid.New(), Invoice: uuid.New(), Contact: uuid.New()}
	p := signInvoiceButton(key, b)
	if len(p) > 256 || !strings.HasPrefix(p, "iv1.") {
		t.Fatalf("payload %q (%d)", p, len(p))
	}
	got, err := parseInvoiceButton(key, p)
	if err != nil || got != b {
		t.Fatalf("parsed %+v %v", got, err)
	}
	for _, a := range []byte{invoiceNew, invoiceOpen} {
		c := b
		c.Action = a
		if got, err := parseInvoiceButton(key, signInvoiceButton(key, c)); err != nil || got.Action != a {
			t.Fatalf("action %c: %+v %v", a, got, err)
		}
	}

	// Another invoice, contact or business in the body, under the same
	// signature, is refused; so is another key, an unknown action, junk.
	body, mac, _ := strings.Cut(strings.TrimPrefix(p, "iv1."), ".")
	raw, _ := base64.RawURLEncoding.DecodeString(body)
	for _, at := range []int{0, 1, 17, 33, 48} {
		x := bytes.Clone(raw)
		x[at] ^= 1
		if _, err := parseInvoiceButton(key, "iv1."+base64.RawURLEncoding.EncodeToString(x)+"."+mac); !errors.Is(err, errBadInvoiceButton) {
			t.Errorf("byte %d changed: %v", at, err)
		}
	}
	other := bytes.Repeat([]byte{8}, 32)
	unknown := b
	unknown.Action = 'x'
	for _, bad := range []string{
		signInvoiceButton(other, b),
		signInvoiceButton(key, unknown),
		"iv1.", "iv1.abc.def", "tk1." + body + "." + mac, p + "x", "", "yes",
	} {
		if _, err := parseInvoiceButton(key, bad); err == nil {
			t.Errorf("accepted %q", bad)
		}
	}
	// No key, no buttons: an empty key signs nothing that verifies.
	if _, err := parseInvoiceButton(nil, signInvoiceButton(nil, b)); err == nil {
		t.Error("accepted a payload signed with no key")
	}
}

// What Pay by transfer answers: the account and until when in Lagos, or
// why not with the page.
func TestInvoiceAnswerText(t *testing.T) {
	now := time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC)
	until := now.Add(35 * time.Minute) // 13:35 in Lagos
	x := invoicing.Invoice{Number: "INV-0007", Status: "sent", DueKobo: 2500000}
	a := invoicing.Account{Status: "open", AmountKobo: 2500000, AccountNumber: "9900112233", BankName: "Wema Bank", AccountName: "Shop / Ada", ExpiresAt: &until}
	link := "https://forms.taskiem.test/i/tok"

	text, actions := invoiceAnswerText(lang.EN, "Ada Foods", x, a, nil, link, now)
	for _, want := range []string{"₦25,000", "INV-0007", "Account number: 9900112233", "Bank: Wema Bank", "Account name: Shop / Ada", "Valid until 13:35"} {
		if !strings.Contains(text, want) {
			t.Errorf("account: %q lacks %q", text, want)
		}
	}
	if string(actions) != string([]byte{invoiceNew, invoiceOpen}) {
		t.Errorf("account buttons %q", actions)
	}
	// Expired by the time it is told: ask again.
	if text, actions := invoiceAnswerText(lang.EN, "Ada Foods", x, a, nil, link, until); !strings.Contains(text, "on its way") || string(actions) != "n" {
		t.Errorf("expired: %q %q", text, actions)
	}
	// Still being issued.
	if text, actions := invoiceAnswerText(lang.EN, "Ada Foods", x, invoicing.Account{Status: "requested"}, nil, link, now); !strings.Contains(text, "on its way") || string(actions) != "n" {
		t.Errorf("requested: %q %q", text, actions)
	}
	// Failures point to the page.
	if text, actions := invoiceAnswerText(lang.EN, "Ada Foods", x, invoicing.Account{}, errors.New("rail down"), link, now); !strings.HasSuffix(text, "\n"+link) || string(actions) != "o" {
		t.Errorf("failed: %q %q", text, actions)
	}
	if text, _ := invoiceAnswerText(lang.EN, "Ada Foods", x, invoicing.Account{}, invoicing.ErrLimit, link, now); !strings.Contains(text, "Too many") || !strings.HasSuffix(text, link) {
		t.Errorf("limit: %q", text)
	}
	// Paid or void: said politely, no buttons.
	paid := x
	paid.Status = "paid"
	if text, actions := invoiceAnswerText(lang.EN, "Ada Foods", paid, invoicing.Account{}, invoicing.ErrState, link, now); text != "Invoice INV-0007 is paid. Thank you!" || actions != nil {
		t.Errorf("paid: %q %q", text, actions)
	}
	void := x
	void.Status = "void"
	if text, actions := invoiceAnswerText(lang.EN, "Ada Foods", void, invoicing.Account{}, invoicing.ErrState, link, now); !strings.Contains(text, "Ask Ada Foods") || actions != nil {
		t.Errorf("void: %q %q", text, actions)
	}
	// Other languages fill in the same facts.
	if text, _ := invoiceAnswerText(lang.PCM, "Ada Foods", x, a, nil, link, now); !strings.Contains(text, "9900112233") || !strings.Contains(text, "13:35") {
		t.Errorf("pcm: %q", text)
	}
}
