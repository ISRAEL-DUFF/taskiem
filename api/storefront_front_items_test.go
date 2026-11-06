package api_test

import (
	"fmt"
	"strings"
	"testing"

	sfconn "github.com/israel-duff/taskiem/connectors/storefront"
	"github.com/israel-duff/taskiem/engine/storefront"
)

// A front's items edited in place: added with a price, a description and a
// category, changed, given a photo (as the catalogue's are), hidden and
// deleted, the list read as the table has it now.
func TestFrontItemsInPlace(t *testing.T) {
	w := shopWorld(t)
	w.srv.Orders = &storefront.Orders{Pool: w.env.DB.App, Vault: w.env.Vault}
	if err := w.env.Registry.Register(sfconn.New(w.srv.Orders)); err != nil {
		t.Fatal(err)
	}
	owner := w.tenant(t, "Ada's Kitchen", "ada@ada.test")
	verifyPhone(t, w.world, owner, "phone", "business")
	owner.must(200, "PUT", "/v1/storefront", map[string]any{"slug": "ada-kitchen", "draft": map[string]any{"name": "Ada's Kitchen"}})
	snacks := owner.must(201, "POST", "/v1/storefront/fronts", map[string]any{"name": "Snacks", "kind": "food"})
	base := "/v1/storefront/fronts/" + snacks["id"].(string) + "/items"

	list := owner.must(200, "GET", base, nil)
	takes := list["takes"].(map[string]any)
	if len(list["items"].([]any)) != 0 || list["table_id"] != snacks["table_id"] || takes["price"] != true || takes["photo"] != true || takes["category"] != true {
		t.Fatalf("an empty front: %v", list)
	}

	// Added: shown as the table has it, in kobo.
	puff := owner.must(201, "POST", base, map[string]any{"name": "Puff puff", "price_naira": 500, "description": "Fried and warm", "category": "Snacks"})
	row := puff["id"].(string)
	items := owner.must(200, "GET", base, nil)["items"].([]any)
	it := items[0].(map[string]any)
	if len(items) != 1 || it["id"] != row || it["name"] != "Puff puff" || it["price_kobo"] != float64(50000) || it["description"] != "Fried and warm" ||
		it["category"] != "Snacks" || it["available"] != true {
		t.Fatalf("added: %v", items)
	}
	// Refused: a nameless item, a negative price, an unknown front, a
	// request that changes nothing.
	owner.must(400, "POST", base, map[string]any{"name": " "})
	owner.must(400, "POST", base, map[string]any{"price_naira": 100})
	owner.must(400, "POST", base, map[string]any{"name": "Chin chin", "price_naira": -5})
	owner.must(404, "GET", "/v1/storefront/fronts/00000000-0000-0000-0000-000000000000/items", nil)
	owner.must(400, "PATCH", base+"/"+row, map[string]any{})

	// Changed and hidden.
	owner.must(200, "PATCH", base+"/"+row, map[string]any{"price_naira": 600.5, "available": false})
	it = owner.must(200, "GET", base, nil)["items"].([]any)[0].(map[string]any)
	if it["price_kobo"] != float64(60050) || it["available"] != false {
		t.Fatalf("changed: %v", it)
	}
	owner.must(404, "PATCH", base+"/999", map[string]any{"price_naira": 1})

	// A photo: stored, put in the table, shown with the item.
	if s, _ := owner.rawPost(t, base+"/"+row+"/photo", "image/jpeg", []byte("<html>not a photo</html>")); s != 400 {
		t.Fatalf("not an image: %d", s)
	}
	s, m := owner.rawPost(t, base+"/"+row+"/photo", "image/jpeg", photo(t))
	if s != 200 || m["urls"].(map[string]any)["320"] == nil {
		t.Fatalf("photo: %d %v", s, m)
	}
	it = owner.must(200, "GET", base, nil)["items"].([]any)[0].(map[string]any)
	if p, _ := it["photo"].(map[string]any); p["800"] == nil || !strings.Contains(p["800"].(string), "/images/") {
		t.Fatalf("the photo is not on the item: %v", it)
	}
	// A second photo joins it; made the main one, then taken out.
	s, two := owner.rawPost(t, base+"/"+row+"/photo?add=1", "image/jpeg", photoN(t, 1))
	if s != 200 {
		t.Fatalf("a second photo: %d %v", s, two)
	}
	ids := func() string {
		return fmt.Sprint(owner.must(200, "GET", base, nil)["items"].([]any)[0].(map[string]any)["photo_ids"])
	}
	if got := ids(); got != fmt.Sprint([]any{m["id"], two["id"]}) {
		t.Fatalf("two photos: %s", got)
	}
	owner.must(204, "PUT", base+"/"+row+"/photo/main?media="+two["id"].(string), nil)
	if got := ids(); got != fmt.Sprint([]any{two["id"], m["id"]}) {
		t.Fatalf("made the main one: %s", got)
	}
	owner.must(204, "DELETE", base+"/"+row+"/photo?media="+two["id"].(string), nil)
	if got := ids(); got != fmt.Sprint([]any{m["id"]}) {
		t.Fatalf("taken out: %s", got)
	}
	// The copy the page reads follows the changes.
	for _, f := range owner.must(200, "GET", "/v1/storefront/fronts", nil)["fronts"].([]any) {
		if f := f.(map[string]any); f["slug"] == "snacks" && f["items"] != float64(1) {
			t.Fatalf("the front counts %v items", f["items"])
		}
	}

	// A front with no list has no items to edit.
	ask := owner.must(201, "POST", "/v1/storefront/fronts", map[string]any{"name": "Ask us", "kind": "questions"})
	owner.must(400, "GET", "/v1/storefront/fronts/"+ask["id"].(string)+"/items", nil)

	// Deleted.
	owner.must(204, "DELETE", base+"/"+row, nil)
	if n := len(owner.must(200, "GET", base, nil)["items"].([]any)); n != 0 {
		t.Fatalf("deleted but %d left", n)
	}
}
