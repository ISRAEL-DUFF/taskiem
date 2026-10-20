package api_test

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/israel-duff/taskiem/engine/storefront"
)

// Stock is lowered at Xenibase only while it still holds the count read,
// and a change sent again with its key lands once.
func TestStorefrontStock(t *testing.T) {
	w := newTablesWorld(t)
	w.srv.PublicHosts = []string{"shops.taskiem.test"}
	owner := w.tenant(t, "Ada's Kitchen", "ada@ada.test")
	verifyPhone(t, w.world, owner, "phone", "business") // publishing goes live at once (moderation.md)
	tbl := owner.must(201, "POST", "/v1/tables", map[string]any{"name": "menu", "columns": []map[string]any{
		{"name": "name", "type": "text", "required": true}, {"name": "price", "type": "number"}, {"name": "left", "type": "integer"}, {"name": "note", "type": "text"}}})
	table := tbl["id"].(string)
	added := owner.must(201, "POST", "/v1/tables/"+table+"/rows", map[string]any{"row": map[string]any{"name": "Jollof rice", "price": "1500", "left": 4}}, "Idempotency-Key", "r1")
	row := jsonNum(added["rows"].([]any)[0].(map[string]any)["id"])
	card := map[string]any{"name": "Ada's Kitchen"}
	owner.must(400, "PUT", "/v1/storefront", map[string]any{"slug": "ada-kitchen", "draft": card, "catalogue_table": table,
		"catalogue_columns": map[string]any{"name": "name", "stock": "note"}})
	sf := owner.must(200, "PUT", "/v1/storefront", map[string]any{"slug": "ada-kitchen", "draft": card, "catalogue_table": table,
		"catalogue_columns": map[string]any{"name": "name", "price": "price", "stock": "left"}})
	owner.must(200, "POST", "/v1/storefront/publish", nil)
	if items := owner.must(200, "GET", "/v1/storefront/catalogue", nil)["items"].([]any); items[0].(map[string]any)["stock"] != float64(4) {
		t.Fatalf("the copy has no stock: %v", items)
	}

	var tenant uuid.UUID
	if err := w.env.DB.Admin.QueryRow(context.Background(), `SELECT tenant_id FROM storefronts WHERE id = $1`, sf["id"]).Scan(&tenant); err != nil {
		t.Fatal(err)
	}
	st := &storefront.XenibaseStock{Pool: w.env.DB.App, Registry: w.env.Registry, Vault: w.env.Vault, Guard: w.env.Egress}
	ctx := context.Background()
	if n, ok, err := st.Read(ctx, tenant, row); err != nil || !ok || n != 4 {
		t.Fatalf("read: %d %v %v", n, ok, err)
	}
	if applied, err := st.Change(ctx, tenant, row, 5, 3, "tsk-sf-stale"); err != nil || applied {
		t.Fatalf("a change from a stale count: %v %v", applied, err)
	}
	for range 2 {
		if applied, err := st.Change(ctx, tenant, row, 4, 2, "tsk-sf-once"); err != nil || !applied {
			t.Fatalf("change: %v %v", applied, err)
		}
	}
	if n, _, _ := st.Read(ctx, tenant, row); n != 2 {
		t.Fatalf("stock %d, want 2", n)
	}
	if _, ok, err := st.Read(ctx, tenant, "999"); ok || err != nil {
		t.Fatalf("a missing row is tracked: %v %v", ok, err)
	}
}
