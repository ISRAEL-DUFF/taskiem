package invoicing

import (
	"reflect"
	"testing"
)

// The business's own lines are paid first, the bodies' last, in the order
// their lines appear; payments' shares add up to what was collected.
func TestPassThroughAllocation(t *testing.T) {
	lines := []Line{
		{Description: "Tuition", Quantity: 1, UnitKobo: 10_000},
		{Description: "WAEC", Quantity: 1, UnitKobo: 2_000, PassThroughFor: "WAEC"},
		{Description: "NECO", Quantity: 1, UnitKobo: 1_000, PassThroughFor: "NECO"},
		{Description: "Sibling discount", Quantity: 1, UnitKobo: -1_000, Kind: LineDiscount},
	}
	own, bodies := PassThrough(lines)
	if own != 9_000 || !reflect.DeepEqual(bodies, []BodyAmount{{"WAEC", 2_000}, {"NECO", 1_000}}) {
		t.Fatalf("own %d bodies %v", own, bodies)
	}
	if got := PassThroughCollected(lines, 8_000); got[0].Kobo != 0 || got[1].Kobo != 0 {
		t.Fatalf("part of own: %v", got)
	}
	if got := PassThroughCollected(lines, 10_500); got[0].Kobo != 1_500 || got[1].Kobo != 0 {
		t.Fatalf("into WAEC: %v", got)
	}
	var sum int64
	paid := int64(0)
	for _, p := range []int64{5_000, 4_500, 1_000, 1_500} {
		for _, b := range PassThroughShare(lines, paid, p) {
			sum += b.Kobo
		}
		paid += p
	}
	if sum != 3_000 {
		t.Fatalf("shares %d", sum)
	}
	if got := PassThroughShare(lines, 8_000, 2_000); !reflect.DeepEqual(got, []BodyAmount{{"WAEC", 1_000}}) {
		t.Fatalf("share %v", got)
	}
	// A line taken out nets out its body.
	out := append(append([]Line{}, lines...), Line{Description: "Taken out: NECO", Quantity: 1, UnitKobo: -1_000, PassThroughFor: "NECO", Kind: LineRemoval})
	if _, b := PassThrough(out); b[1].Kobo != 0 {
		t.Fatalf("removed %v", b)
	}
	if Naira(-50_000) != "−₦500" || Naira(150) != "₦1.50" {
		t.Fatalf("naira %q", Naira(-50_000))
	}
}
