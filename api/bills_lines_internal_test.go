package api

import (
	"testing"

	"github.com/google/uuid"

	"github.com/israel-duff/taskiem/engine/invoicing"
	"github.com/israel-duff/taskiem/engine/links"
)

// A bill's lines name the seller's item and the buyer's order where the
// invoice raised them from the order's lines (what a business buys §8):
// an extra line, a discount, and an order line with no item of the
// seller's name neither.
func TestMatchBillLines(t *testing.T) {
	purchase := uuid.New()
	ordered := []links.OrderLine{
		{Item: "Rice", Unit: "bag", Quantity: 8, PriceKobo: 4_200_000, ItemID: "row-rice"},
		{Item: "Indomie", Quantity: 5, PriceKobo: 850_000},
		{Item: "Rice", Unit: "bag", Quantity: 2, PriceKobo: 4_200_000, ItemID: "row-rice-2"},
	}
	lines := []invoicing.Line{
		{Description: "Rice (bag)", Quantity: 8, UnitKobo: 4_200_000},
		{Description: "Indomie", Quantity: 5, UnitKobo: 850_000},
		{Description: "Rice (bag)", Quantity: 2, UnitKobo: 4_200_000},
		{Description: "Delivery", Quantity: 1, UnitKobo: 500_000},
		{Description: "Rice (bag)", Quantity: 1, UnitKobo: -100_000, Kind: invoicing.LineDiscount},
	}
	out, err := matchBillLines(lines, ordered, purchase)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"row-rice", "", "row-rice-2", "", ""}
	for i, w := range want {
		got, _ := out[i]["seller_item_id"].(string)
		if got != w || (w != "") != (out[i]["purchase_id"] == purchase) || out[i]["description"] != lines[i].Description {
			t.Errorf("line %d: %v", i, out[i])
		}
	}
	// An invoice with no order: its lines as they are.
	if out, _ := matchBillLines(lines[:1], nil, uuid.Nil); out[0]["seller_item_id"] != nil || out[0]["unit_kobo"] != float64(4_200_000) {
		t.Errorf("no order: %v", out)
	}
}
