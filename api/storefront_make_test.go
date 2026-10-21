package api_test

import (
	"context"
	"net/netip"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/israel-duff/taskiem/engine/egress"
	"github.com/israel-duff/taskiem/engine/media"
)

// A storefront with products and photos from the web editor alone
// (no-technical-steps N7): Make my catalogue makes and maps the table as
// chat does, items go in with a naira price, a photo is added, and the
// page shows them; nobody opens the Tables page or maps a column.
func TestMakeMyCatalogue(t *testing.T) {
	w := newTablesWorld(t)
	w.srv.PublicHosts = []string{"shops.taskiem.test"}
	store := fakeStorage(t)
	w.srv.Media = &media.Service{
		Credentials: func(context.Context, uuid.UUID) (map[string]string, error) {
			return map[string]string{"token": "xb_tok", "project_id": "prj_1", "base_url": store.URL, "api_url": store.URL}, nil
		},
		Guard: &egress.Guard{Blocked: func(netip.Addr) bool { return false }},
	}
	owner := w.tenant(t, "Ada's Kitchen", "ada@ada.test")
	verifyPhone(t, w.world, owner, "phone", "business")
	// The page comes first.
	owner.must(409, "POST", "/v1/storefront/catalogue/make", map[string]any{"kind": "products"})
	owner.must(200, "PUT", "/v1/storefront", map[string]any{"slug": "ada-kitchen", "draft": map[string]any{"name": "Ada's Kitchen"}})
	owner.must(400, "POST", "/v1/storefront/catalogue/make", map[string]any{"kind": "furniture"})

	made := owner.must(201, "POST", "/v1/storefront/catalogue/make", map[string]any{"kind": "products"})
	table := made["table_id"].(string)
	sf := owner.must(200, "GET", "/v1/storefront", nil)
	cols := sf["catalogue_columns"].(map[string]any)
	if sf["catalogue_table"] != table || cols["name"] != "name" || cols["price"] != "price" || cols["price_unit"] != "naira" || cols["photo"] != "photo" {
		t.Fatalf("the mapping: %v", sf)
	}
	// Again: the same table, nothing new.
	if again := owner.must(200, "POST", "/v1/storefront/catalogue/make", map[string]any{"kind": "products"}); again["table_id"] != table || again["made"] != false {
		t.Errorf("again: %v", again)
	}

	// An item as the editor adds it (the price in naira), then its photo.
	added := owner.must(201, "POST", "/v1/tables/"+table+"/rows", map[string]any{"row": map[string]any{"name": "Jollof rice", "price": 1500, "available": true}})
	row := added["rows"].([]any)[0].(map[string]any)["id"]
	if s, m := owner.rawPost(t, "/v1/storefront/catalogue/rows/"+jsonNum(row)+"/photo", "image/jpeg", photo(t)); s != 200 {
		t.Fatalf("photo: %d %v", s, m)
	}
	cat := owner.must(200, "POST", "/v1/storefront/catalogue/refresh", nil)
	items := cat["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("catalogue: %v", cat)
	}
	it := items[0].(map[string]any)
	if it["name"] != "Jollof rice" || it["price_kobo"] != float64(150000) || it["available"] != true || it["photo"] == nil {
		t.Fatalf("the item: %v", it)
	}
	pub := owner.must(200, "POST", "/v1/storefront/publish", nil)
	if pub["status"] != "published" {
		t.Fatalf("publish: %v", pub)
	}

	// Services the same way.
	svc := owner.must(201, "POST", "/v1/storefront/catalogue/make", map[string]any{"kind": "services"})
	owner.must(201, "POST", "/v1/tables/"+svc["table_id"].(string)+"/rows", map[string]any{"row": map[string]any{"name": "Haircut", "price": 2000, "duration_minutes": 30}})
	owner.must(200, "POST", "/v1/storefront/catalogue/refresh", nil)
	if s := owner.must(200, "GET", "/v1/storefront/services", nil); !strings.Contains(toJSON(s), "Haircut") {
		t.Errorf("services: %v", s)
	}

	// The checklist knows the page is made.
	if got := toJSON(owner.must(200, "GET", "/v1/onboarding", nil)["items"]); !strings.Contains(got, `{"done":true,"id":"page"}`) {
		t.Errorf("checklist: %s", got)
	}
	// Audited.
	if a := toJSON(owner.must(200, "GET", "/v1/audit?limit=100", nil)); !strings.Contains(a, "table.create") || !strings.Contains(a, "storefront.bookings_save") {
		t.Error("not audited")
	}
}
