package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	sfconn "github.com/israel-duff/taskiem/connectors/storefront"
	"github.com/israel-duff/taskiem/engine/storefront"
)

// Search everywhere finds saved things and services (menus-everywhere.md
// §6, g-search-saved): GET /v1/chat-refs with kind=saved finds the person's
// own saved things and their business's, by name or kind, answering only
// the name, the kind and whose it is (never the title, the hint, an
// account's number or anything it holds); not another person's, nor
// another business's; a member sees the business's and only their own.
// kind=service finds the business's services with their price and
// minutes. Without a kind, neither is searched (the picker as before).
func TestSearchFindsSavedAndServices(t *testing.T) {
	w := newTablesWorld(t)
	key, _ := storefront.InboxKey(bytes.Repeat([]byte{3}, 32))
	w.srv.Orders = &storefront.Orders{Pool: w.env.DB.App, Vault: w.env.Vault, InboxKey: key, PreviewDomain: "shops.taskiem.test",
		Start: w.env.Store.Start, Signal: w.env.Store.DeliverSignalOnce, Cancel: w.env.Store.CancelRun}
	if err := w.env.Registry.Register(sfconn.New(w.srv.Orders)); err != nil {
		t.Fatal(err)
	}
	owner := w.tenant(t, "Ada's Salon", "ada@ada.test")
	other := w.tenant(t, "Bola Stores", "bola@bola.test")

	// Saved things: the owner's own, the business's, and others'.
	owner.must(201, "POST", "/v1/me/saved", map[string]any{"name": "mum", "title": "Mum Adebayo", "bank_name": "Guaranty Trust Bank", "account_number": "2222222222"})
	owner.must(201, "POST", "/v1/saved", map[string]any{"name": "mum-shop", "kind": "place", "title": "Mum's shop", "details": map[string]any{"line": "4 Allen Avenue", "area": "Ikeja", "phone": "08031234567"}})
	owner.must(201, "POST", "/v1/saved", map[string]any{"name": "landlord", "title": "Landlord", "bank_name": "Zenith Bank", "account_number": "1234567890"})
	other.must(201, "POST", "/v1/saved", map[string]any{"name": "mummy", "kind": "text", "title": "Note", "details": map[string]any{"body": "secret words"}})
	other.must(201, "POST", "/v1/me/saved", map[string]any{"name": "mumsy", "kind": "text", "title": "Note", "details": map[string]any{"body": "secret words"}})

	search := func(c *client, q, kind string) []map[string]any {
		t.Helper()
		v := url.Values{"q": {q}}
		if kind != "" {
			v.Set("kind", kind)
		}
		var out []map[string]any
		for _, x := range c.must(200, "GET", "/v1/chat-refs?"+v.Encode(), nil)["refs"].([]any) {
			out = append(out, x.(map[string]any))
		}
		return out
	}
	names := func(refs []map[string]any) string {
		var n []string
		for _, r := range refs {
			n = append(n, r["name"].(string)+"/"+r["owner"].(string))
		}
		return strings.Join(n, ",")
	}
	got := search(owner, "mum", "saved")
	// Newest first, the person's own and the business's merged by date.
	if names(got) != "mum-shop/business,mum/mine" {
		t.Fatalf("mum: %s", names(got))
	}
	// Names and kinds only: nothing a saved thing holds or is titled.
	raw, _ := json.Marshal(got)
	for _, secret := range []string{"Adebayo", "2222", "Allen", "0803", "Mum's shop", "Guaranty", "hint", "title", "details"} {
		if strings.Contains(string(raw), secret) {
			t.Fatalf("%q in %s", secret, raw)
		}
	}
	if got[1]["saved_kind"] != "bank_account" || got[0]["saved_kind"] != "place" {
		t.Fatalf("kinds: %s", raw)
	}
	// By its kind's words.
	if n := names(search(owner, "bank account", "saved")); n != "mum/mine,landlord/business" && n != "landlord/business,mum/mine" {
		t.Fatalf("by kind: %s", n)
	}
	// A title is not searched (names and kinds only).
	if got := search(owner, "adebayo", "saved"); len(got) != 0 {
		t.Fatalf("by title: %v", got)
	}
	// A member: the business's, not the owner's own.
	cashier := addMember(t, w.world, owner, "tunde", "cashier")
	if n := names(search(cashier, "mum", "saved")); n != "mum-shop/business" {
		t.Fatalf("member: %s", n)
	}
	// Without a kind: the picker's kinds only, as before.
	for _, r := range search(owner, "mum", "") {
		if r["kind"] == "saved" || r["kind"] == "service" {
			t.Fatalf("searched without asking: %v", r)
		}
	}

	// Services: the business's, with their price and minutes.
	tbl := owner.must(201, "POST", "/v1/tables", map[string]any{"name": "services", "columns": []map[string]any{
		{"name": "name", "type": "text", "required": true}, {"name": "price", "type": "number"}, {"name": "minutes", "type": "integer"}}})
	table := tbl["id"].(string)
	owner.must(201, "POST", "/v1/tables/"+table+"/rows", map[string]any{"row": map[string]any{"name": "Haircut", "price": "3000", "minutes": 30}}, "Idempotency-Key", "s1")
	owner.must(201, "POST", "/v1/tables/"+table+"/rows", map[string]any{"row": map[string]any{"name": "Braiding class", "price": "5000", "minutes": 60}}, "Idempotency-Key", "s2")
	owner.must(200, "PUT", "/v1/storefront", map[string]any{"slug": "ada-salon", "draft": map[string]any{"name": "Ada's Salon"}})
	every := []any{}
	for range 7 {
		every = append(every, []any{map[string]any{"open": "09:00", "close": "17:00"}})
	}
	owner.must(200, "PUT", "/v1/storefront/bookings", map[string]any{"services_table": table,
		"services_columns": map[string]any{"name": "name", "price": "price", "duration": "minutes"}, "availability": map[string]any{"days": every}})
	svc := search(owner, "hair", "service")
	if len(svc) != 1 || svc[0]["name"] != "Haircut" || svc[0]["minutes"] != float64(30) || svc[0]["total_kobo"] != float64(300000) {
		t.Fatalf("services: %v", svc)
	}
	if svc := search(owner, "service", "service"); len(svc) != 2 {
		t.Fatalf("every service: %v", svc)
	}
	// Another business, and a member who may not manage the page: none.
	if svc := search(other, "hair", "service"); len(svc) != 0 {
		t.Fatalf("another business's services: %v", svc)
	}
	if svc := search(cashier, "hair", "service"); len(svc) != 0 {
		t.Fatalf("a cashier's services: %v", svc)
	}
	// Show more: three at a time, the cursor going on.
	page := owner.must(200, "GET", "/v1/chat-refs?q=mum&kind=saved&limit=1", nil)
	if len(page["refs"].([]any)) != 1 || page["cursor"] != "saved:1" {
		t.Fatalf("a page: %v", page)
	}
	next := owner.must(200, "GET", "/v1/chat-refs?q=mum&kind=saved&limit=1&cursor=saved:1", nil)
	if refs := next["refs"].([]any); len(refs) != 1 || refs[0].(map[string]any)["name"] != "mum" || next["cursor"] != "" {
		t.Fatalf("the next page: %v", next)
	}
}

// A search for saved things holds one pooled connection at a time (review
// of H4): the person's own are read before the business's transaction
// opens, so with a pool of one it still answers rather than waiting on
// itself. Words are matched in the database: a % or an _ typed is itself,
// and "Show more" goes on past what one page read, every match, none twice.
func TestSearchSavedOneConnectionAtATime(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t)
	owner := w.tenant(t, "Ada's Salon", "ada@ada.test")
	owner.must(201, "POST", "/v1/me/saved", map[string]any{"name": "mum", "kind": "text", "title": "Note", "details": map[string]any{"body": "hello"}})
	owner.must(201, "POST", "/v1/saved", map[string]any{"name": "mum-shop", "kind": "text", "title": "Note", "details": map[string]any{"body": "hello"}})

	cfg := w.env.Store.Pool.Config()
	cfg.MaxConns, cfg.MinConns = 1, 0
	small, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer small.Close()
	was := w.srv.Store.Pool
	w.srv.Store.Pool = small
	done := make(chan map[string]any, 1)
	go func() {
		_, body := owner.do("GET", "/v1/chat-refs?q=mum&kind=saved", nil)
		done <- body
	}()
	select {
	case body := <-done:
		w.srv.Store.Pool = was
		if refs, _ := body["refs"].([]any); len(refs) != 2 {
			t.Fatalf("with a pool of one: %v", body)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("the search waited on a second connection")
	}

	// % and _ typed are themselves, not any letters (a name holds neither).
	for _, q := range []string{"m_m", "%25", "mu%25"} {
		if got := owner.must(200, "GET", "/v1/chat-refs?kind=saved&q="+q, nil)["refs"].([]any); len(got) != 0 {
			t.Fatalf("%s: %v", q, got)
		}
	}

	// More than one page reads: every match, by Show more, none twice.
	for i := range 230 {
		owner.must(201, "POST", "/v1/saved", map[string]any{"name": fmt.Sprintf("crate-%03d", i), "kind": "text", "title": "Note", "details": map[string]any{"body": "x"}})
	}
	seen := map[string]bool{}
	cursor := ""
	for range 20 {
		path := "/v1/chat-refs?q=crate&kind=saved&limit=50"
		if cursor != "" {
			path += "&cursor=" + cursor
		}
		page := owner.must(200, "GET", path, nil)
		for _, r := range page["refs"].([]any) {
			name := r.(map[string]any)["name"].(string)
			if seen[name] {
				t.Fatalf("%s twice", name)
			}
			seen[name] = true
		}
		if cursor, _ = page["cursor"].(string); cursor == "" {
			break
		}
	}
	if len(seen) != 230 {
		t.Fatalf("found %d of 230", len(seen))
	}
}
