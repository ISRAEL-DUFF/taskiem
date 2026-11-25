package api_test

import (
	"context"
	"net/url"
	"testing"

	"github.com/google/uuid"

	"github.com/israel-duff/taskiem/engine/moneylog"
)

// The Transactions page over the API (docs/spec/transactions-page.md):
// the money log one movement to a row, filtered, searched, paged and
// summed up; one movement on its own with who paid masked until revealed.
func TestMoneyTransactionsAPI(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t)
	log := &moneylog.Log{Pool: w.env.DB.App, PII: w.env.Vault}
	owner := w.tenant(t, "Ada Foods", "ada@adafoods.test")
	viewer := addMember(t, w, owner, "vic", "viewer")
	builder := addMember(t, w, owner, "bob", "builder")
	tenant := tenantIDNamed(t, w, "Ada Foods")
	amount, fee := int64(2500000), int64(37500)
	if _, _, err := log.Record(ctx, tenant, moneylog.Entry{Direction: "in", Kind: "payment", Status: "settled", Channel: "paystack/main", Reference: "P-1",
		Event: "payment.received", AmountKobo: &amount, FeeKobo: &fee, Source: "event", PayerName: "Bola Ade", PayerAccount: "0123456789"}); err != nil {
		t.Fatal(err)
	}
	for _, ev := range []string{"payout.settled", "payout.reversed"} {
		if _, err := log.RecordEvent(ctx, tenant, "prod", "paystack", "main", ev, map[string]any{"payout_id": "T-1", "amount_kobo": int64(400000)}); err != nil {
			t.Fatal(err)
		}
	}
	owner.must(201, "POST", "/v1/money/entries", map[string]any{"kind": "expense", "amount_kobo": 300000, "description": "Diesel", "vendor": "Total"}, "Idempotency-Key", "t-ex")
	owner.must(201, "POST", "/v1/money/entries", map[string]any{"kind": "cash", "direction": "in", "amount_kobo": 150000, "description": "Cash from Bisi"}, "Idempotency-Key", "t-cash")

	get := func(c *client, q url.Values) map[string]any {
		t.Helper()
		q.Set("movements", "true")
		return c.must(200, "GET", "/v1/money/entries?"+q.Encode(), nil)
	}
	first := get(viewer, url.Values{})
	entries := first["entries"].([]any)
	if len(entries) != 4 || first["next"] != nil {
		t.Fatalf("four movements: %v", first)
	}
	totals := first["totals"].(map[string]any)
	if totals["count"] != float64(4) || totals["in_settled_kobo"] != float64(2650000) || totals["out_settled_kobo"] != float64(300000) || totals["fees_kobo"] != float64(37500) {
		t.Errorf("totals %v", totals)
	}
	if ch := first["channels"].([]any); len(ch) != 2 || ch[0] != "cash" || ch[1] != "paystack/main" {
		t.Errorf("channels %v", ch)
	}
	// The old list is unchanged: every entry, the reversal its own row.
	if old := viewer.must(200, "GET", "/v1/money/entries", nil)["entries"].([]any); len(old) != 5 {
		t.Errorf("the chat's list: %d", len(old))
	}

	// Filters and search.
	for name, c := range map[string]struct {
		q    url.Values
		want int
	}{
		"money out":   {url.Values{"direction": {"out"}}, 2},
		"cash":        {url.Values{"channel": {"cash"}}, 2},
		"paystack in": {url.Values{"channel": {"paystack/main"}, "direction": {"in"}}, 1},
		"not matched": {url.Values{"matched": {"no"}}, 2},
		"matched":     {url.Values{"matched": {"yes"}}, 0},
		"who":         {url.Values{"q": {"bisi"}}, 1},
		"amount":      {url.Values{"q": {"25,000"}}, 1},
		"last year":   {url.Values{"from": {"2025-01-01"}, "to": {"2025-01-31"}}, 0},
	} {
		r := get(viewer, c.q)
		if n := len(r["entries"].([]any)); n != c.want || r["totals"].(map[string]any)["count"] != float64(c.want) {
			t.Errorf("%s: %d rows, totals %v; want %d", name, n, r["totals"], c.want)
		}
	}
	for _, bad := range []string{"direction=up", "matched=maybe", "from=yesterday", "to=2026-13-01", "from=2026-10-02&to=2026-10-01", "cursor=nonsense"} {
		viewer.must(400, "GET", "/v1/money/entries?movements=true&"+bad, nil)
	}
	viewer.must(400, "GET", "/v1/money/entries?direction=in", nil)

	// Paging: one at a time; later pages carry no totals.
	page := get(viewer, url.Values{"limit": {"1"}})
	seen := map[string]bool{}
	for {
		for _, e := range page["entries"].([]any) {
			seen[e.(map[string]any)["movement"].(string)] = true
		}
		next, _ := page["next"].(string)
		if next == "" {
			break
		}
		page = get(viewer, url.Values{"limit": {"1"}, "cursor": {next}})
		if page["totals"] != nil {
			t.Fatalf("a later page with totals: %v", page)
		}
	}
	if len(seen) != 4 {
		t.Errorf("paged %d", len(seen))
	}

	// One movement: who paid masked, then revealed by someone allowed.
	var payment string
	for _, e := range entries {
		if m := e.(map[string]any); m["reference"] == "P-1" {
			payment = m["id"].(string)
		}
	}
	one := viewer.must(200, "GET", "/v1/money/entries/"+payment, nil)
	if one["payer"] != "B•••" || one["payer_account"] != "••6789" || one["revealed"] != false {
		t.Errorf("masked %v", one)
	}
	e := one["entry"].(map[string]any)
	if e["reference"] != "P-1" || e["fee_kobo"] != float64(37500) || len(e["history"].([]any)) != 1 || e["counts"] == nil {
		t.Errorf("the entry %v", e)
	}
	viewer.must(403, "GET", "/v1/money/entries/"+payment+"?reveal=true", nil)
	if shown := owner.must(200, "GET", "/v1/money/entries/"+payment+"?reveal=true", nil); shown["payer"] != "Bola Ade" || shown["payer_account"] != "0123456789" {
		t.Errorf("revealed %v", shown)
	}
	viewer.must(404, "GET", "/v1/money/entries/"+uuid.NewString(), nil)
	viewer.must(404, "GET", "/v1/money/entries/not-an-id", nil)

	// Another business sees none of it; those who may not see invoices, nothing.
	other := w.tenant(t, "Bee Ltd", "bee@bee.test")
	other.must(404, "GET", "/v1/money/entries/"+payment, nil)
	if l := other.must(200, "GET", "/v1/money/entries?movements=true", nil)["entries"].([]any); len(l) != 0 {
		t.Errorf("another business sees %v", l)
	}
	builder.must(403, "GET", "/v1/money/entries?movements=true", nil)
	builder.must(403, "GET", "/v1/money/entries/"+payment, nil)
}
