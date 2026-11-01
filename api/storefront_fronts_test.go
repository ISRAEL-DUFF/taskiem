package api_test

import (
	"strings"
	"testing"

	sfconn "github.com/israel-duff/taskiem/connectors/storefront"
	"github.com/israel-duff/taskiem/engine/storefront"
)

// Fronts in the web app (docs/spec/fronts.md F3): a Food front is added
// by kind with its menu table, off, its order action off with no
// workflow; its address comes from its name and stays clear of the
// page's own routes; it is turned on, renamed and moved; its action is
// set up with a workflow naming it; one with a workflow is not removed,
// one without is.
func TestFronts(t *testing.T) {
	w := newTablesWorld(t)
	w.srv.PublicHosts = []string{"shops.taskiem.test"}
	w.srv.Orders = &storefront.Orders{Pool: w.env.DB.App, Vault: w.env.Vault}
	if err := w.env.Registry.Register(sfconn.New(w.srv.Orders)); err != nil {
		t.Fatal(err)
	}
	owner := w.tenant(t, "Ada's Kitchen", "ada@ada.test")
	verifyPhone(t, w.world, owner, "phone", "business")
	owner.must(409, "POST", "/v1/storefront/fronts", map[string]any{"name": "Snacks", "kind": "food"})
	owner.must(200, "PUT", "/v1/storefront", map[string]any{"slug": "ada-kitchen", "draft": map[string]any{"name": "Ada's Kitchen"}})

	list := owner.must(200, "GET", "/v1/storefront/fronts", nil)
	kinds := list["kinds"].([]any)
	if len(kinds) != 7 || kinds[0].(map[string]any)["name"] != "shop" || kinds[2].(map[string]any)["name"] != "hire" || kinds[3].(map[string]any)["name"] != "spaces" || kinds[6].(map[string]any)["name"] != "custom" {
		t.Fatalf("kinds: %v", kinds)
	}
	if fronts := list["fronts"].([]any); len(fronts) != 1 || fronts[0].(map[string]any)["slug"] != "" {
		t.Fatalf("the home page: %v", fronts)
	}
	owner.must(400, "POST", "/v1/storefront/fronts", map[string]any{"name": "Snacks", "kind": "rocket"})

	snacks := owner.must(201, "POST", "/v1/storefront/fronts", map[string]any{"name": "Snacks & Small Chops", "kind": "food"})
	acts := snacks["actions"].([]any)
	if snacks["slug"] != "snacks-small-chops" || snacks["enabled"] != false || snacks["kind"] != "food" || snacks["table_id"] == nil || len(acts) != 1 {
		t.Fatalf("the front: %v", snacks)
	}
	order := acts[0].(map[string]any)
	if order["kind"] != "order" || order["slug"] != "order" || order["enabled"] != false || order["builtin"] != false || order["workflow_id"] != nil {
		t.Fatalf("its order action: %v", order)
	}
	opts := order["options"].(map[string]any)
	if opts["delivery"] != true || opts["pickup"] != true {
		t.Fatalf("food delivers by default: %v", opts)
	}
	table := owner.must(200, "GET", "/v1/tables/"+snacks["table_id"].(string), nil)
	if table["name"] != "menu" {
		t.Fatalf("the menu table: %v", table["name"])
	}
	// A dish in the menu is copied for the page, apart from the catalogue
	// (one copy per table), and counted on the front.
	owner.must(201, "POST", "/v1/tables/"+snacks["table_id"].(string)+"/rows", map[string]any{"row": map[string]any{"name": "Puff puff", "price": 500, "available": true}})
	owner.must(200, "POST", "/v1/storefront/catalogue/refresh", nil)
	for _, f := range owner.must(200, "GET", "/v1/storefront/fronts", nil)["fronts"].([]any) {
		f := f.(map[string]any)
		if want := map[string]float64{"": 0, "snacks-small-chops": 1}[f["slug"].(string)]; f["items"] != want {
			t.Fatalf("%q shows %v items, want %v", f["slug"], f["items"], want)
		}
	}
	if cat := owner.must(200, "GET", "/v1/storefront/catalogue", nil)["items"].([]any); len(cat) != 0 {
		t.Fatalf("the catalogue took the menu: %v", cat)
	}
	// A second front of the same name gets the next address; a page's own
	// route is not an address.
	again := owner.must(201, "POST", "/v1/storefront/fronts", map[string]any{"name": "Snacks & small chops", "kind": "questions"})
	if again["slug"] != "snacks-small-chops-2" || again["table_id"] != nil {
		t.Fatalf("the second: %v", again)
	}
	id := again["id"].(string)
	if e := owner.must(400, "PATCH", "/v1/storefront/fronts/"+id, map[string]any{"slug": "report"}); !strings.Contains(e["error"].(string), "uses itself") {
		t.Fatalf("a reserved address: %v", e)
	}
	owner.must(400, "PATCH", "/v1/storefront/fronts/"+id, map[string]any{"slug": "snacks-small-chops"})
	moved := owner.must(200, "PATCH", "/v1/storefront/fronts/"+id, map[string]any{"name": "Ask us", "slug": "ask", "tagline": "Anything at all", "enabled": true})
	if moved["slug"] != "ask" || moved["name"] != "Ask us" || moved["tagline"] != "Anything at all" || moved["enabled"] != true {
		t.Fatalf("changed: %v", moved)
	}
	home := owner.must(200, "GET", "/v1/storefront/fronts", nil)["fronts"].([]any)[0].(map[string]any)
	owner.must(400, "PATCH", "/v1/storefront/fronts/"+home["id"].(string), map[string]any{"enabled": false})
	owner.must(400, "DELETE", "/v1/storefront/fronts/"+home["id"].(string), nil)

	// Set up: a workflow whose trigger names the action by id.
	setup := owner.must(201, "POST", "/v1/storefront/actions/"+order["id"].(string)+"/setup", nil)
	wf := setup["workflow_id"].(string)
	v1 := owner.must(200, "GET", "/v1/workflows/"+wf+"/versions/1", nil)
	cfg := v1["definition"].(map[string]any)["trigger"].(map[string]any)["config"].(map[string]any)
	if cfg["action_id"] != order["id"] {
		t.Fatalf("the trigger: %v", cfg)
	}
	owner.must(200, "POST", "/v1/workflows/"+wf+"/versions/1/publish", nil)
	if again := owner.must(200, "POST", "/v1/storefront/actions/"+order["id"].(string)+"/setup", nil); again["workflow_id"] != wf {
		t.Fatalf("set up again: %v", again)
	}
	// Orders by front: none from the front yet; a front that is not an id
	// is refused.
	if list := owner.must(200, "GET", "/v1/storefront/orders?status=all&front="+snacks["id"].(string), nil)["orders"].([]any); len(list) != 0 {
		t.Fatalf("the front's orders: %v", list)
	}
	owner.must(400, "GET", "/v1/storefront/orders?front=snacks", nil)
	owner.must(400, "DELETE", "/v1/storefront/fronts/"+snacks["id"].(string), nil)
	owner.must(204, "DELETE", "/v1/storefront/fronts/"+id, nil)
}
