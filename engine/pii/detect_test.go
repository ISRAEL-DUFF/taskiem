package pii_test

import (
	"sort"
	"strings"
	"testing"

	"github.com/israel-duff/taskiem/engine/expr"
	"github.com/israel-duff/taskiem/engine/pii"
)

func detect(t *testing.T, doc string) map[string]string {
	t.Helper()
	v, err := expr.DecodeJSON([]byte(doc))
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for _, d := range pii.Detect(v) {
		out[d.Path] = d.Category
	}
	return out
}

func TestDetectsNigerianIdentifiers(t *testing.T) {
	got := detect(t, `{
	  "customer": {"bvn": "22212345678", "nin": "12345678901", "phone": "+2348031234567", "alt": "08121234567", "email": "ada@example.ng"},
	  "payout": {"bank_code": "058", "account_number_raw": "0000014579", "acct": "1234567890"},
	  "card": "4111 1111 1111 1111",
	  "phones": ["2349031234567"],
	  "bvn_number": 22212345679
	}`)
	want := map[string]string{
		"/customer/bvn": "bvn", "/customer/nin": "nin", "/customer/phone": "phone", "/customer/alt": "phone", "/customer/email": "email",
		"/payout/account_number_raw": "account_number", "/payout/acct": "account_number",
		"/card": "card", "/phones/0": "phone", "/bvn_number": "bvn",
	}
	for p, c := range want {
		if got[p] != c {
			t.Errorf("%s: got %q, want %q", p, got[p], c)
		}
	}
	if len(got) != len(want) {
		t.Errorf("unexpected detections: %v", keys(got))
	}
}

// Digit strings that are not personal stay readable.
func TestLeavesOrdinaryDataAlone(t *testing.T) {
	got := detect(t, `{
	  "amount": 22212345678, "amount_kobo": "5000000", "reference": "1234567890123456", "txn": "TXN-22212345",
	  "outflow_id": "out_01HZ", "count": 11, "period": "2026-01", "code": "000",
	  "account_name": "Ada Obi", "status": "pending", "id": "4111111111111112",
	  "payout": {"bank_code": "058", "number": "1234567891"}
	}`)
	if len(got) != 0 {
		t.Errorf("false positives: %v", got)
	}
}

func TestNUBANCheckDigit(t *testing.T) {
	// Bank 058, serial 000001457: 000058000001457 weighted 3,7,3... sums to
	// 130, so the check digit is 0.
	got := detect(t, `{"bank_code": "058", "x": "0000014570", "y": "0000014579", "acct_no": "0000014579"}`)
	if got["/x"] != "account_number" {
		t.Errorf("the check digit should identify the account: %v", got)
	}
	if got["/y"] != "" {
		t.Errorf("a 10-digit value whose check digit disagrees with the bank code: %v", got)
	}
	if got["/acct_no"] != "account_number" {
		t.Errorf("a hinted account is detected whatever its check digit: %v", got)
	}
}

func TestRedactFreeText(t *testing.T) {
	in := "verified BVN 22212345678 for ada@example.ng on 0803 123 4567? no: +2348031234567, card 4111-1111-1111-1111, ref 1234567890123456, amount 5000000"
	out := pii.Redact(in)
	for _, leak := range []string{"22212345678", "ada@example.ng", "+2348031234567", "4111-1111-1111-1111"} {
		if strings.Contains(out, leak) {
			t.Errorf("%q leaked: %s", leak, out)
		}
	}
	for _, keep := range []string{"1234567890123456", "5000000"} {
		if !strings.Contains(out, keep) {
			t.Errorf("%q should stay: %s", keep, out)
		}
	}
}

func keys(m map[string]string) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
