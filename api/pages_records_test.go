package api_test

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/invoicing"
	"github.com/israel-duff/taskiem/engine/sender"
)

// recordsWorld is a world with data tables and invoices.
func recordsWorld(t *testing.T) *tablesWorld {
	t.Helper()
	w := newTablesWorld(t)
	w.srv.Invoices = &invoicing.Invoices{Pool: w.env.DB.App, Vault: w.env.Vault, PageBase: "https://forms.taskiem.test",
		Messages: &sender.Queue{Vault: w.env.Vault, HashKey: bytes.Repeat([]byte{8}, 32)}, Signal: w.env.Store.DeliverSignalOnce}
	return w
}

// addOrder puts an order in the business, its customer sealed as the
// shop's orders are.
func addOrder(t *testing.T, w *tablesWorld, c *client, ref, status, name, phone string, totalKobo int64) {
	t.Helper()
	ctx := context.Background()
	tenant := uuid.MustParse(tenantOf(t, c))
	if err := db.InTenantTx(ctx, w.env.DB.App, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		envPhone, err := w.env.Vault.SealTx(ctx, tx, tenant, "phone", phone)
		if err != nil {
			return err
		}
		subject := envPhone["subject"].(string)
		cust, err := w.env.Vault.SealUnderTx(ctx, tx, tenant, subject, "customer", map[string]any{"name": name, "phone": phone, "email": strings.ToLower(name[:3]) + "@example.com"})
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `INSERT INTO storefront_orders (id, tenant_id, kind, reference, status_hash, status, customer, subject_id, total_kobo, payment)
			VALUES ($1, $2, 'trade', $3, $4, $5, $6, $7, $8, 'none')`, uuid.Must(uuid.NewV7()), tenant, ref, bytes.Repeat([]byte{1}, 32), status, cust, subject, totalKobo)
		return err
	}); err != nil {
		t.Fatal(err)
	}
}

func setOrderStatus(t *testing.T, w *tablesWorld, c *client, ref, status string) {
	t.Helper()
	ctx := context.Background()
	tenant := uuid.MustParse(tenantOf(t, c))
	if err := db.InTenantTx(ctx, w.env.DB.App, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `UPDATE storefront_orders SET status = $2 WHERE reference = $1`, ref, status)
		return err
	}); err != nil {
		t.Fatal(err)
	}
}

func addInvoice(t *testing.T, c *client, name string, kobo int64, key string) {
	t.Helper()
	c.must(201, "POST", "/v1/invoices", map[string]any{"customer": map[string]any{"name": name, "phone": "+2348031234567"},
		"lines": []map[string]any{{"description": "Rice", "quantity": 1, "unit_kobo": kobo}}, "due_on": "2099-01-31"}, "Idempotency-Key", key)
}

var salesPage = map[string]any{"page": "page/v1", "name": "shop-desk", "title": "Shop desk",
	"filters": []any{map[string]any{"id": "q", "type": "search"}, map[string]any{"id": "st", "type": "choice", "column": "status",
		"options": []any{map[string]any{"value": "received"}, map[string]any{"value": "accepted"}}}},
	"blocks": []any{
		map[string]any{"id": "n", "type": "figure", "label": "Orders", "value": "count", "source": map[string]any{"records": "orders"}},
		map[string]any{"id": "owed", "type": "figure", "label": "Invoiced", "value": "sum", "of": "total", "naira": true, "source": map[string]any{"records": "invoices"}},
		map[string]any{"id": "orders", "type": "list", "title": "Orders",
			"source": map[string]any{"records": "orders", "order": []any{map[string]any{"column": "total", "desc": true}}, "uses": []any{"q", "st"}},
			"item":   map[string]any{"title": "=row.number", "sub": "=row.customer + ' · ' + row.phone", "state": map[string]any{"text": "=row.status"}}},
		map[string]any{"id": "inv", "type": "table", "title": "Invoices", "source": map[string]any{"records": "invoices"},
			"columns": []any{map[string]any{"column": "number"}, map[string]any{"column": "customer"}, map[string]any{"column": "total", "naira": true}}},
	}}

// Taskiem's own records on a page (§5, G1): a page with a list of the
// business's orders and the sum of its invoices; the area's read
// permission decides who sees each part ("You cannot see orders."); the
// customer is masked for everyone and is never filtered or ordered by; no
// other business's orders are counted; an area turned off is refused at
// publishing and drawn as unavailable.
func TestPagesRecords(t *testing.T) {
	w := recordsWorld(t)
	owner := w.tenant(t, "Acme", "owner@acme.test")
	viewer := addMember(t, w.world, owner, "vic", "viewer")   // invoice.read, no orders
	cashier := addMember(t, w.world, owner, "cas", "cashier") // order.handle and invoice.read
	other := w.tenant(t, "Other", "owner@other.test")

	addOrder(t, w, owner, "AC-1", "received", "Bola Ade", "+2348031234567", 150000)
	addOrder(t, w, owner, "AC-2", "accepted", "Chi Obi", "+2348039876543", 990000)
	addOrder(t, w, other, "OT-1", "received", "Dele", "+2348030000000", 7700000)
	addInvoice(t, owner, "Bola Ade", 4500000, "i-1")
	addInvoice(t, owner, "Chi Obi", 500000, "i-2")
	addInvoice(t, other, "Dele", 99900000, "o-1")

	// The editor's list of views.
	recs := owner.must(200, "GET", "/v1/page-records", nil)["records"].([]any)
	if len(recs) != 2 || recs[0].(map[string]any)["name"] != "orders" || recs[0].(map[string]any)["on"] != true {
		t.Fatalf("records: %v", recs)
	}
	owner.must(201, "POST", "/v1/pages", map[string]any{"document": salesPage})
	pub := owner.must(200, "POST", "/v1/pages/shop-desk/publish", nil)
	if !strings.Contains(strings.Join(toStrings(pub["shows"]), "|"), "Orders: Number, Customer, Phone, Status") {
		t.Errorf("shows: %v", pub["shows"])
	}

	v := viewOf(t, cashier, "shop-desk", nil)
	if b := blockOf(t, v, "n"); b["shown"] != "2" {
		t.Errorf("count: %v", b)
	}
	if b := blockOf(t, v, "owed"); b["shown"] != "₦50,000" {
		t.Errorf("sum: %v", b)
	}
	list := blockOf(t, v, "orders")
	if got := titles(list); fmt.Sprint(got) != "[AC-2 AC-1]" || list["count"] != float64(2) {
		t.Fatalf("orders: %v", list)
	}
	row := list["rows"].([]any)[0].(map[string]any)
	if sub := row["sub"].(string); !strings.HasPrefix(sub, "C•••") || strings.Contains(sub, "Chi") || strings.Contains(sub, "9876543") {
		t.Errorf("not masked: %q", sub)
	}
	if row["link"] != "/storefront/orders/AC-2" {
		t.Errorf("link: %v", row["link"])
	}
	inv := blockOf(t, v, "inv")
	cells := inv["rows"].([]any)[0].(map[string]any)["cells"].([]any)
	if !strings.HasSuffix(cells[1].(string), "•••") || inv["columns"].([]any)[1].(map[string]any)["sortable"] != false {
		t.Errorf("invoices: %v", inv)
	}
	// The owner, holding table.read, is no reader of the customer either.
	if s, _ := owner.do("GET", "/v1/pages/shop-desk/blocks/inv?order=customer", nil); s != 400 {
		t.Errorf("ordered by the customer: %d", s)
	}
	// Filters on the server: a choice, and a search that never reads the customer.
	if got := titles(blockOf(t, viewOf(t, cashier, "shop-desk", map[string]any{"st": "accepted"}), "orders")); fmt.Sprint(got) != "[AC-2]" {
		t.Errorf("choice: %v", got)
	}
	if got := blockOf(t, viewOf(t, owner, "shop-desk", map[string]any{"q": "Chi"}), "orders"); len(got["rows"].([]any)) != 0 {
		t.Errorf("searched the customer: %v", got["rows"])
	}
	if got := titles(blockOf(t, viewOf(t, owner, "shop-desk", map[string]any{"q": "ac-1"}), "orders")); fmt.Sprint(got) != "[AC-1]" {
		t.Errorf("search: %v", got)
	}

	// A staff member without the orders' read permission: the plain
	// refusal in place of the orders, and the invoices drawn.
	v = viewOf(t, viewer, "shop-desk", nil)
	for _, id := range []string{"n", "orders"} {
		if b := blockOf(t, v, id); b["unavailable"] != "You cannot see orders." || b["rows"] != nil || b["shown"] != nil {
			t.Errorf("%s for the viewer: %v", id, b)
		}
	}
	if b := blockOf(t, v, "owed"); b["shown"] != "₦50,000" {
		t.Errorf("the viewer's invoices: %v", b)
	}
	// A page whose search names the customer, or which picks by the
	// customer's phone, is refused in plain words.
	bad := map[string]any{"page": "page/v1", "name": "bad-desk", "title": "Bad",
		"filters": []any{map[string]any{"id": "q", "type": "search", "columns": []any{"customer"}}},
		"blocks": []any{map[string]any{"id": "l", "type": "list", "title": "Orders", "source": map[string]any{"records": "orders", "uses": []any{"q"},
			"filters": []any{map[string]any{"column": "phone", "op": "eq", "value": "+2348031234567"}}}, "item": map[string]any{"title": "=row.number"}}}}
	ps := toStrings(problemsOf(owner.must(200, "POST", "/v1/pages/validate", map[string]any{"document": bad})))
	if !strings.Contains(strings.Join(ps, "|"), "cannot pick orders by Phone: it is personal") || !strings.Contains(strings.Join(ps, "|"), "Customer is personal") {
		t.Errorf("refusals: %v", ps)
	}

	// Another business sees none of it.
	if s, _ := other.do("GET", "/v1/pages/shop-desk/view", nil); s != 404 {
		t.Errorf("another business opened it: %d", s)
	}
	other.must(201, "POST", "/v1/pages", map[string]any{"document": salesPage})
	other.must(200, "POST", "/v1/pages/shop-desk/publish", nil)
	ov := viewOf(t, other, "shop-desk", nil)
	if blockOf(t, ov, "n")["shown"] != "1" || blockOf(t, ov, "owed")["shown"] != "₦999,000" || fmt.Sprint(titles(blockOf(t, ov, "orders"))) != "[OT-1]" {
		t.Errorf("other's page: %v", ov["blocks"])
	}

	// More: a cursor of Taskiem's own.
	for i := 3; i <= 27; i++ {
		addOrder(t, w, owner, fmt.Sprintf("AC-%d", i), "received", "Eze", "+2348030000001", int64(i))
	}
	list = blockOf(t, viewOf(t, cashier, "shop-desk", nil), "orders")
	if len(list["rows"].([]any)) != 25 || list["more"] == nil {
		t.Fatalf("first rows: %d %v", len(list["rows"].([]any)), list["more"])
	}
	more := cashier.must(200, "GET", "/v1/pages/shop-desk/blocks/orders?cursor="+list["more"].(string), nil)
	if got := titles(more); len(got) != 2 || more["more"] != nil {
		t.Errorf("more: %v %v", got, more["more"])
	}

	// Orders turned off: refused at publishing, drawn as unavailable.
	owner.must(200, "PUT", "/v1/settings/areas", map[string]any{"areas": map[string]bool{"storefront": false, "orders": false}})
	v = viewOf(t, cashier, "shop-desk", nil)
	if b := blockOf(t, v, "orders"); !strings.Contains(fmt.Sprint(b["unavailable"]), "turned off") || b["rows"] != nil {
		t.Errorf("area off: %v", b)
	}
	if b := blockOf(t, v, "owed"); b["shown"] != "₦50,000" {
		t.Errorf("invoices with orders off: %v", b)
	}
	ps = toStrings(problemsOf(owner.must(200, "POST", "/v1/pages/validate", map[string]any{"document": salesPage})))
	if !strings.Contains(strings.Join(ps, "|"), "orders, and Orders is turned off in Settings") {
		t.Errorf("area off at publishing: %v", ps)
	}
	imp := owner.must(201, "POST", "/v1/pages/import", map[string]any{"document": salesPage})
	if !strings.Contains(strings.Join(toStrings(imp["missing"]), "|"), "This business has Orders turned off, so its orders cannot be shown.") {
		t.Errorf("import: %v", imp["missing"])
	}
}

// problemsOf are a validation's problems' messages.
func problemsOf(out map[string]any) []any {
	var msgs []any
	for _, p := range out["problems"].([]any) {
		msgs = append(msgs, p.(map[string]any)["message"])
	}
	return msgs
}
