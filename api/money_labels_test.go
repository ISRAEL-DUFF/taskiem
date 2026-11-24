package api_test

import (
	"bytes"
	"context"
	"testing"

	"github.com/israel-duff/taskiem/engine/invoicing"
	"github.com/israel-duff/taskiem/engine/moneylog"
	"github.com/israel-duff/taskiem/engine/sender"
)

// Spend labels over the API (what a business buys §5.4): made by typing
// them and found again without case, offered most used first with a
// suggestion, an expense entered with one by name or id, renamed and
// merged by those who manage money, and the spending report grouping by
// the label kept while the entries stay as they were.
func TestMoneyLabelsAPI(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t)
	log := &moneylog.Log{Pool: w.env.DB.App, PII: w.env.Vault}
	w.srv.Invoices = &invoicing.Invoices{Pool: w.env.DB.App, Vault: w.env.Vault, PageBase: "https://forms.taskiem.test", MoneyLog: log,
		Messages: &sender.Queue{Vault: w.env.Vault, HashKey: bytes.Repeat([]byte{8}, 32)}}
	owner := w.tenant(t, "Ada Foods", "ada@adafoods.test")
	viewer := addMember(t, w, owner, "vic", "viewer")

	snacks := owner.must(201, "POST", "/v1/money/labels", map[string]any{"name": "  Snacks "})
	if snacks["name"] != "Snacks" {
		t.Fatalf("made %v", snacks)
	}
	if again := owner.must(200, "POST", "/v1/money/labels", map[string]any{"name": "SNACKS"}); again["id"] != snacks["id"] {
		t.Fatalf("found again %v", again)
	}
	owner.must(400, "POST", "/v1/money/labels", map[string]any{"name": "  "})
	viewer.must(403, "POST", "/v1/money/labels", map[string]any{"name": "Fuel"})
	welfare := owner.must(201, "POST", "/v1/money/labels", map[string]any{"name": "Staff welfare"})

	// Expenses by the label's name (made: Diesel), by its id, and with an
	// unknown id; cash takes none.
	owner.must(201, "POST", "/v1/money/entries", map[string]any{"kind": "expense", "amount_kobo": 1500000, "description": "diesel", "label": "Diesel"}, "Idempotency-Key", "l-1")
	owner.must(201, "POST", "/v1/money/entries", map[string]any{"kind": "expense", "amount_kobo": 300000, "description": "Biscuits", "label_id": snacks["id"]}, "Idempotency-Key", "l-2")
	owner.must(201, "POST", "/v1/money/entries", map[string]any{"kind": "expense", "amount_kobo": 200000, "description": "Lunch", "label": "staff welfare"}, "Idempotency-Key", "l-3")
	owner.must(400, "POST", "/v1/money/entries", map[string]any{"kind": "expense", "amount_kobo": 200000, "description": "Lunch", "label_id": "00000000-0000-0000-0000-000000000001"})
	owner.must(400, "POST", "/v1/money/entries", map[string]any{"kind": "cash", "direction": "in", "amount_kobo": 200000, "description": "Till", "label": "Sales"})
	var cat string
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT category FROM money_entries e JOIN spend_labels l ON l.id = e.label_id WHERE l.name = 'Diesel'`).Scan(&cat); err != nil || cat != "Diesel" {
		t.Fatalf("labelled entry: %q %v", cat, err)
	}

	list := viewer.must(200, "GET", "/v1/money/labels?item=Diesel+20+litres&category=", nil)
	labels, sugg := list["labels"].([]any), list["suggestions"].([]any)
	if len(labels) != 3 || len(sugg) != 4 || sugg[0].(map[string]any)["name"] != "Diesel" || sugg[3].(map[string]any)["name"] != "Litres" || sugg[3].(map[string]any)["label_id"] != nil {
		t.Fatalf("list %v", list)
	}

	// Rename and merge: managers only.
	id := snacks["id"].(string)
	viewer.must(403, "PATCH", "/v1/money/labels/"+id, map[string]any{"name": "Treats"})
	owner.must(400, "PATCH", "/v1/money/labels/"+id, map[string]any{"name": "Diesel"})
	if r := owner.must(200, "PATCH", "/v1/money/labels/"+id, map[string]any{"name": "Treats"}); r["name"] != "Treats" {
		t.Fatalf("renamed %v", r)
	}
	viewer.must(403, "POST", "/v1/money/labels/"+id+"/merge", map[string]any{"into": welfare["id"]})
	owner.must(404, "POST", "/v1/money/labels/"+id+"/merge", map[string]any{"into": "00000000-0000-0000-0000-000000000001"})
	if r := owner.must(200, "POST", "/v1/money/labels/"+id+"/merge", map[string]any{"into": welfare["id"]}); r["merged_into"] != welfare["id"] {
		t.Fatalf("merged %v", r)
	}
	// Typing the merged label's name finds the label kept.
	if r := owner.must(200, "POST", "/v1/money/labels", map[string]any{"name": "treats"}); r["id"] != welfare["id"] {
		t.Fatalf("merged name %v", r)
	}

	rep := viewer.must(200, "GET", "/v1/money/spending", nil)
	by := map[string]float64{}
	for _, l := range rep["labels"].([]any) {
		m := l.(map[string]any)
		by[m["name"].(string)] = m["kobo"].(float64)
	}
	if len(by) != 2 || by["Staff welfare"] != 500000 || by["Diesel"] != 1500000 || rep["total_kobo"] != float64(2000000) {
		t.Fatalf("spending %v", rep)
	}
	// Nothing in the log was rewritten.
	var biscuits string
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT category FROM money_entries WHERE description = 'Biscuits'`).Scan(&biscuits); err != nil || biscuits != "Snacks" {
		t.Fatalf("the entry: %q %v", biscuits, err)
	}
}
