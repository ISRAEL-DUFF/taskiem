package spend

import (
	"math/big"

	"github.com/google/uuid"
)

// Treatments of what was bought, as purchase_lines and item_links hold
// them (stock.Treatment): just recorded, added to stock, kept track of.
const (
	Record = "record"
	Stock  = "stock"
	Track  = "track"
)

// Part is a share of money out: its label (nil: Not labelled) and the
// treatment of what it bought. Before Split, Kobo is the part's weight (a
// label's share of an order or a bill); after, the money it takes.
type Part struct {
	LabelID   *uuid.UUID `json:"label_id,omitempty"`
	Treatment string     `json:"treatment"`
	Kobo      int64      `json:"kobo"`
}

// Split shares amount among parts in proportion to their weights (Kobo),
// in whole kobo rounded down; the kobo left over go to the part with the
// largest weight, the first of them on a tie. The parts returned, in the
// same order, add up to amount exactly. A weight below zero counts as
// none; with no weight at all, the first part takes everything.
func Split(amount int64, parts []Part) []Part {
	out := make([]Part, len(parts))
	copy(out, parts)
	if len(out) == 0 {
		return out
	}
	total := new(big.Int)
	largest := 0
	for i := range out {
		if out[i].Kobo < 0 {
			out[i].Kobo = 0
		}
		total.Add(total, big.NewInt(out[i].Kobo))
		if out[i].Kobo > out[largest].Kobo {
			largest = i
		}
	}
	if total.Sign() == 0 {
		for i := range out {
			out[i].Kobo = 0
		}
		out[0].Kobo = amount
		return out
	}
	left := amount
	a := big.NewInt(amount)
	for i := range out {
		// amount × weight overflows int64 for large orders: the product is
		// taken in full.
		n := new(big.Int).Mul(a, big.NewInt(out[i].Kobo))
		out[i].Kobo = n.Quo(n, total).Int64()
		left -= out[i].Kobo
	}
	out[largest].Kobo += left
	return out
}

// sameLabel reports whether two labels are the same, nil being Not
// labelled.
func sameLabel(a, b *uuid.UUID) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

// add puts weight on the part of a label and treatment, keeping the order
// in which parts first appear.
func add(parts []Part, label *uuid.UUID, treatment string, kobo int64) []Part {
	for i := range parts {
		if sameLabel(parts[i].LabelID, label) && parts[i].Treatment == treatment {
			parts[i].Kobo += kobo
			return parts
		}
	}
	return append(parts, Part{LabelID: label, Treatment: treatment, Kobo: kobo})
}
