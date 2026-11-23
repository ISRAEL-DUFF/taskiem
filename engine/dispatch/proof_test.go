package dispatch_test

import (
	"testing"

	"github.com/israel-duff/taskiem/engine/dispatch"
)

// A delivery's proof marks (device fields §6): a pin sent is the rider's
// word; a reading is compared with the customer's own pin.
func TestProofMarks(t *testing.T) {
	f := func(v float64) *float64 { return &v }
	door := "https://www.google.com/maps?q=6.428100,3.421900"
	for _, c := range []struct {
		p    dispatch.Proof
		pin  string
		want string
	}{
		{dispatch.Proof{}, door, dispatch.MarkNoLocation},
		{dispatch.Proof{Lat: f(6.4281), Lng: f(3.4219), WhereSource: "sent"}, door, dispatch.MarkSaidByRider},
		{dispatch.Proof{Lat: f(6.4282), Lng: f(3.4220), WhereSource: "device"}, door, dispatch.MarkAtDoor},
		{dispatch.Proof{Lat: f(6.4282), Lng: f(3.4220), WhereSource: "device", Coarse: true}, door, dispatch.MarkFar},
		{dispatch.Proof{Lat: f(6.4400), Lng: f(3.4219), WhereSource: "device"}, door, dispatch.MarkFar},
		{dispatch.Proof{Lat: f(6.4281), Lng: f(3.4219), WhereSource: "device"}, "", dispatch.MarkReadByPhone},
	} {
		if got := dispatch.MarkOf(c.p, c.pin); got != c.want {
			t.Errorf("%+v against %q: %s, want %s", c.p, c.pin, got, c.want)
		}
	}
	if d := dispatch.DistanceM(6.4281, 3.4219, 6.4381, 3.4219); d < 1100 || d > 1120 {
		t.Errorf("a hundredth of a degree of latitude: %.0f m", d)
	}
	if dispatch.MaskPoint(6.428149) != 6.428 {
		t.Errorf("masked: %v", dispatch.MaskPoint(6.428149))
	}
}
