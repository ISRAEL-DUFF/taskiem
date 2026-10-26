package api_test

import (
	"bytes"
	"testing"

	"github.com/israel-duff/taskiem/engine/invoicing"
	"github.com/israel-duff/taskiem/engine/moneylog"
	"github.com/israel-duff/taskiem/engine/sender"
)

// A customer's own account number from an invoice (docs/matching.md
// "Customers' own account numbers"): the issuers the business has, one
// issued for the invoice's customer on the one path a school's students
// take (once per customer and connection), one registered by hand, and
// both listed on the customer's panel, masked.
func TestInvoiceCustomerAccounts(t *testing.T) {
	w := newWorld(t)
	log := &moneylog.Log{Pool: w.env.DB.App, PII: w.env.Vault}
	w.srv.Invoices = &invoicing.Invoices{Pool: w.env.DB.App, Vault: w.env.Vault, PageBase: "https://forms.taskiem.test", MoneyLog: log,
		Messages: &sender.Queue{Vault: w.env.Vault, HashKey: bytes.Repeat([]byte{8}, 32)}, Signal: w.env.Store.DeliverSignalOnce}
	rails := &accountRails{opened: map[string]string{}, fail: map[string]error{}}
	w.srv.AccountRails = rails
	owner := w.tenant(t, "Ada Foods", "ada@adafoods.test")
	viewer := addMember(t, w, owner, "vic", "viewer")

	viewer.must(403, "GET", "/v1/customer-accounts/issuers", nil)
	is := owner.must(200, "GET", "/v1/customer-accounts/issuers", nil)["issuers"].([]any)
	if len(is) != 1 || is[0].(map[string]any)["connection"] != "iswallet/main" || is[0].(map[string]any)["needs_account"] != true || is[0].(map[string]any)["rail"] != "iswallet" {
		t.Fatalf("issuers %v", is)
	}

	ada := map[string]any{"name": "Ada Obi", "phone": "+2348031230003"}
	x := owner.must(201, "POST", "/v1/invoices", map[string]any{"customer": ada, "amount_kobo": 200000})
	id := x["id"].(string)
	path := "/v1/invoices/" + id + "/customer-accounts"
	viewer.must(403, "POST", path, map[string]any{"connection": "iswallet/main", "issue": true, "account": "wal_1"})
	// A connection that cannot issue, and the wallet iswallet needs.
	owner.must(400, "POST", path, map[string]any{"connection": "paystack/main", "issue": true})
	owner.must(400, "POST", path, map[string]any{"connection": "iswallet/main", "issue": true})
	owner.must(404, "POST", "/v1/invoices/INV-9999/customer-accounts", map[string]any{"connection": "iswallet/main", "issue": true, "account": "wal_1"})
	a := owner.must(200, "POST", path, map[string]any{"connection": "iswallet/main", "issue": true, "account": "wal_1"})
	if a["channel"] != "iswallet/main" || a["account_number"] != "9900000001" || a["bank_name"] != "Wema Bank" || a["reference"] == "" || a["customer"].(map[string]any)["name"] != "A•••" {
		t.Fatalf("issued %v", a)
	}
	// Again: the same account, the rail not asked twice.
	again := owner.must(200, "POST", path, map[string]any{"connection": "iswallet/main", "issue": true, "account": "wal_1"})
	if again["id"] != a["id"] || rails.asked != 1 {
		t.Fatalf("again %v (asked %d)", again, rails.asked)
	}
	// Another invoice of the same customer: still the same account.
	y := owner.must(201, "POST", "/v1/invoices", map[string]any{"customer": ada, "amount_kobo": 300000})
	if z := owner.must(200, "POST", "/v1/invoices/"+y["id"].(string)+"/customer-accounts", map[string]any{"connection": "iswallet/main", "issue": true, "account": "wal_1"}); z["id"] != a["id"] {
		t.Fatalf("other invoice %v", z)
	}
	// By hand: the number and reference the business has.
	owner.must(400, "POST", path, map[string]any{"connection": "paystack/main", "account_number": "1234567890"})
	h := owner.must(200, "POST", path, map[string]any{"connection": "paystack/main", "account_number": "1234567890", "reference": "CUS_ada", "bank_name": "Titan"})
	if h["channel"] != "paystack/main" || h["account_number"] != "1234567890" {
		t.Fatalf("by hand %v", h)
	}
	owner.must(409, "POST", "/v1/invoices/"+y["id"].(string)+"/customer-accounts", map[string]any{"connection": "paystack/main", "account_number": "1234567890", "reference": "CUS_other"})
	// Both on the customer's panel, from any of their invoices.
	cust := viewer.must(200, "GET", "/v1/invoices/"+y["id"].(string)+"/customer", nil)
	accts := cust["accounts"].([]any)
	if len(accts) != 2 || accts[0].(map[string]any)["channel"] != "iswallet/main" || accts[1].(map[string]any)["account_number"] != "1234567890" {
		t.Fatalf("accounts %v", accts)
	}
	if n := schoolAudits(t, w, "Ada Foods", "invoice.customer_account"); n != 2 {
		t.Errorf("%d account audits", n)
	}
}
