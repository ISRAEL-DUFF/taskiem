package api_test

import (
	"strings"
	"testing"
)

// The chat's Invoices and Money tools (docs/spec/chat-five-more-flows.md,
// phase 2 additions): an invoice made, sent, paid in cash and cancelled
// from the chat, the money log listed with what each entry counts towards,
// money matched to an invoice by hand, and each change told as a diary line.
func TestChatInvoicesAndMoney(t *testing.T) {
	w := shopWorld(t)
	withStock(w.world)
	withLinkedOrders(w.world)
	seller, _, _, _ := linkedPair(t, w.waWorld)
	seller.must(200, "PUT", "/v1/settings/chat-tools", map[string]any{"chat_tools": true})
	cust := map[string]any{"name": "Bisi Okafor", "phone": "+2348030000556"}

	// A new invoice, sent.
	a := seller.must(201, "POST", "/v1/invoices", map[string]any{"customer": cust, "description": "Catering for 50", "amount_kobo": 4500000}, "Idempotency-Key", "m-a")
	seller.must(200, "POST", "/v1/invoices/"+a["id"].(string)+"/send", nil, "Idempotency-Key", "m-as")
	num := a["number"].(string)

	// Paid in cash: the entry, then matched to the invoice.
	e := seller.must(201, "POST", "/v1/money/entries", map[string]any{"kind": "cash", "direction": "in", "amount_kobo": 2000000, "description": "Payment for " + num, "channel": "cash"}, "Idempotency-Key", "m-pay")
	entry := e["entry_id"].(string)
	seller.must(200, "POST", "/v1/money/entries/"+entry+"/match", map[string]any{"invoice": num})
	inv := seller.must(200, "GET", "/v1/invoices/"+a["id"].(string), nil)["invoice"].(map[string]any)
	if inv["status"] != "part_paid" || inv["due_kobo"] != float64(2500000) {
		t.Fatalf("paid in part: %v", inv)
	}

	// An expense, and cash left to match.
	seller.must(201, "POST", "/v1/money/entries", map[string]any{"kind": "expense", "amount_kobo": 300000, "description": "Diesel", "vendor": "Total"}, "Idempotency-Key", "m-ex")
	loose := seller.must(201, "POST", "/v1/money/entries", map[string]any{"kind": "cash", "direction": "in", "amount_kobo": 2500000, "description": "Cash from Bisi"}, "Idempotency-Key", "m-loose")["entry_id"].(string)

	list := seller.must(200, "GET", "/v1/money/entries", nil)["entries"].([]any)
	if len(list) != 3 {
		t.Fatalf("the money log: %v", list)
	}
	byID := map[string]map[string]any{}
	for _, x := range list {
		m := x.(map[string]any)
		byID[m["id"].(string)] = m
	}
	if p := byID[entry]; p["towards"] != "invoice "+num || p["direction"] != "in" || p["amount_kobo"] != float64(2000000) || p["left_kobo"] != float64(0) {
		t.Fatalf("the payment: %v", p)
	}
	if first := list[0].(map[string]any); first["id"] != loose {
		t.Fatalf("newest first: %v", list)
	}
	for _, x := range list {
		if m := x.(map[string]any); m["kind"] == "expense" && (m["direction"] != "out" || m["vendor"] != "Total" || m["left_kobo"] != nil) {
			t.Fatalf("the expense: %v", m)
		}
	}
	// Paging: before the newest, the rest.
	if older := seller.must(200, "GET", "/v1/money/entries?limit=1&before="+list[0].(map[string]any)["when"].(string), nil)["entries"].([]any); len(older) != 1 {
		t.Fatalf("a page: %v", older)
	}
	seller.must(400, "GET", "/v1/money/entries?before=yesterday", nil)

	// The loose cash matched by hand: the invoice paid.
	seller.must(200, "POST", "/v1/money/entries/"+loose+"/match", map[string]any{"invoice": num})
	if inv := seller.must(200, "GET", "/v1/invoices/"+a["id"].(string), nil)["invoice"].(map[string]any); inv["status"] != "paid" {
		t.Fatalf("paid: %v", inv)
	}

	// Another invoice, cancelled.
	b := seller.must(201, "POST", "/v1/invoices", map[string]any{"customer": cust, "description": "Chairs", "amount_kobo": 100000}, "Idempotency-Key", "m-b")
	seller.must(200, "POST", "/v1/invoices/"+b["id"].(string)+"/void", map[string]any{"reason": "Made by mistake"})

	d := diaryOf(t, seller)
	for _, want := range []string{
		"Made invoice " + num + " for ₦45,000",
		"Sent invoice " + num,
		"Matched a payment to invoice " + num,
		"Added an expense of ₦3,000 to the money log",
		"Cancelled invoice " + b["number"].(string),
	} {
		found := false
		for _, l := range d {
			if strings.Contains(l, want) {
				found = true
			}
		}
		if !found {
			t.Errorf("diary %q missing from %q", want, d)
		}
	}

	// Only those who may see invoices see the money log.
	builder := addMember(t, w.world, seller, "build", "builder")
	builder.must(403, "GET", "/v1/money/entries", nil)
}
