package api_test

import (
	"strings"
	"testing"
)

// stockItemByName is a stock item of GET /v1/stock/stock-items by name.
func stockItemByName(t *testing.T, owner *client, name string) map[string]any {
	t.Helper()
	for _, x := range owner.must(200, "GET", "/v1/stock/stock-items", nil)["items"].([]any) {
		if x.(map[string]any)["name"] == name {
			return x.(map[string]any)
		}
	}
	t.Fatalf("no stock item %s", name)
	return nil
}

// PR review fix 2: counting in by a product's name moves its stock item
// as a purchase line of it does (stock.ReceiveTx): "Rice 1kg" uses 1000 g,
// so 10 bags are 10,000 g, and ₦1,500 a bag is ₦1.50 a gram. The stock
// item keeps its unit.
func TestCountingInAProductByItsUses(t *testing.T) {
	w := shopWorld(t)
	withStock(w.world)
	withPurchases(w.world)
	owner := w.tenant(t, "Ada Foods", "ada@foods.test")
	owner.must(200, "POST", "/v1/stock/setup", nil)
	rice := owner.must(201, "POST", "/v1/stock/items", map[string]any{"name": "Rice", "quantity": 500, "unit": "g", "material": true},
		"Idempotency-Key", "r1")["item"].(map[string]any)["id"].(string)
	added := owner.must(201, "POST", "/v1/stock/items", map[string]any{"name": "Rice 1kg", "quantity": 0, "price_kobo": 200000},
		"Idempotency-Key", "r2")["item"].(map[string]any)
	kg := added["id"].(string)
	owner.must(200, "PUT", "/v1/stock/products/"+kg+"/counted-by", map[string]any{"stock_item_id": rice, "uses": 1000})
	// Its own stock item from when it was added, a material now, named
	// apart.
	owner.must(200, "PATCH", "/v1/stock/items/"+added["stock_item_id"].(string), map[string]any{"name": "Old bags"})
	const on = "+2348010000231"
	w.bind(t, owner, on)

	if r := w.say(t, on, "add 10 bags of Rice 1kg at 1,500"); !strings.Contains(r, "Add 10 × 1000 gs to *Rice 1kg* (500 gs → 10500 gs)") {
		t.Fatalf("asked: %q", r)
	}
	if r := w.yes(t, on); !strings.Contains(r, "10500 gs") {
		t.Fatalf("done: %q", r)
	}
	x := stockItemByName(t, owner, "Rice")
	if x["count"] != float64(10500) || x["cost_kobo"] != float64(150) || x["unit"] != "g" {
		t.Fatalf("the rice after 10 bags: %v", x)
	}
	// received … with no open order counts in the same way.
	if r := w.say(t, on, "received 1 bag of Rice 1kg at 1,500"); !strings.Contains(r, "(10500 gs → 11500 gs)") {
		t.Fatalf("received: %q", r)
	}
	w.yes(t, on)
	if x := stockItemByName(t, owner, "Rice"); x["count"] != float64(11500) || x["cost_kobo"] != float64(150) {
		t.Fatalf("the rice after a bag received: %v", x)
	}
}

// PR review fix 3: POST /v1/stock/sales refuses a material, as chat's sold
// and stock@1 do: nothing is sold, nothing moves.
func TestSaleRefusesAMaterial(t *testing.T) {
	w := newTablesWorld(t)
	withStock(w.world)
	owner := w.tenant(t, "Ada Tailors", "ada@tailors.test")
	owner.must(200, "POST", "/v1/stock/setup", nil)
	thread := owner.must(201, "POST", "/v1/stock/items", map[string]any{"name": "Thread", "quantity": 4, "unit": "roll", "material": true},
		"Idempotency-Key", "t1")["item"].(map[string]any)["id"].(string)
	owner.must(400, "POST", "/v1/stock/sales", map[string]any{"paid_how": "cash", "amount_kobo": 50000,
		"lines": []any{map[string]any{"item_id": thread, "quantity": 1}}}, "Idempotency-Key", "s1")
	if x := stockItemByName(t, owner, "Thread"); x["count"] != float64(4) {
		t.Fatalf("the material moved: %v", x)
	}
}
