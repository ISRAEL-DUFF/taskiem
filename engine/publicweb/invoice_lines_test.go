package publicweb

import (
	"testing"

	"github.com/israel-duff/taskiem/engine/invoicing"
)

// A pass-through line says whom it is collected for; a discount and an
// amendment say why, with no unit price.
func TestInvoiceLineView(t *testing.T) {
	waec := invoiceLineView(invoicing.Line{Description: "WAEC fee", Quantity: 1, UnitKobo: 1_800_000, PassThroughFor: "WAEC"})
	if waec.Note != "Collected on behalf of WAEC." || waec.Each != "1 × ₦18,000" || waec.Amount != "₦18,000" {
		t.Fatalf("pass-through %+v", waec)
	}
	off := invoiceLineView(invoicing.Line{Description: "Sibling discount (10%)", Quantity: 1, UnitKobo: -1_000_000, Kind: invoicing.LineDiscount, Reason: "Second child"})
	if off.Note != "Second child" || off.Each != "" || off.Amount != "−₦10,000" {
		t.Fatalf("discount %+v", off)
	}
	added := invoiceLineView(invoicing.Line{Description: "Books", Quantity: 1, UnitKobo: 500_000, Amendment: "a1", Reason: "Lost"})
	if added.Note != "Added after the invoice was sent. Lost" {
		t.Fatalf("amendment %+v", added)
	}
}
