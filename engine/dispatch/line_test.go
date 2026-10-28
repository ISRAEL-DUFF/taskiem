package dispatch_test

import (
	"testing"

	"github.com/israel-duff/taskiem/engine/dispatch"
	"github.com/israel-duff/taskiem/engine/lang"
)

func TestRiderLine(t *testing.T) {
	r := 4.5
	for _, c := range []struct {
		x    dispatch.RiderDay
		want string
	}{
		{dispatch.RiderDay{Rider: "Musa", Delivered: 4, DoorKobo: 850000, CashKobo: 300000, OwedKobo: 300000, Rating: &r},
			"Musa: 4 delivered, ₦8,500 paid at the door, ₦3,000 cash, owes ₦3,000, rated 4.5 of 5"},
		{dispatch.RiderDay{Rider: "Tunde", RemittedKobo: 200000}, "Tunde: 0 delivered, ₦2,000 handed in"},
	} {
		if got := dispatch.RiderLine(lang.EN, c.x); got != c.want {
			t.Errorf("got %q, want %q", got, c.want)
		}
	}
}
