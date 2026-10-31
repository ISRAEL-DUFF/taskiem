package builder

import "testing"

// A goal naming a business linked on Taskiem is offered the link
// templates as one saying "on Taskiem" is (business links L6).
func TestNamesLinked(t *testing.T) {
	c := Context{Linked: []LinkedBusiness{{Name: "Dangote Stores", Role: "supplier"}, {Name: "Ada's Kitchen", Role: "customer"}}}
	for goal, want := range map[string]bool{
		"pay Dangote's bills every Friday after I approve": true,
		"tell me when ada orders":                          true,
		"pay my staff every Friday":                        false,
		"pay the dan bills":                                false,
	} {
		if got := c.NamesLinked(goal); got != want {
			t.Errorf("%q: %v, want %v", goal, got, want)
		}
	}
	if (Context{}).NamesLinked("pay Dangote") {
		t.Error("no links: nothing named")
	}
}
