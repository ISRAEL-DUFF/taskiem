package areas_test

import (
	"errors"
	"testing"

	"github.com/israel-duff/taskiem/engine/areas"
)

func state(on ...areas.Area) areas.State {
	s := areas.State{}
	for _, a := range on {
		s[a] = true
	}
	return s
}

func TestApplyDependencies(t *testing.T) {
	// Turning on the shop turns on orders.
	next, err := areas.Apply(state(), map[areas.Area]bool{areas.Storefront: true})
	if err != nil || !next.On(areas.Storefront) || !next.On(areas.Orders) {
		t.Fatalf("storefront on: %v %v", next, err)
	}
	// Schools needs money.
	next, err = areas.Apply(state(), map[areas.Area]bool{areas.Schools: true})
	if err != nil || !next.On(areas.Money) {
		t.Fatalf("schools on: %v %v", next, err)
	}
	// Orders cannot go off under a shop that is on.
	_, err = areas.Apply(state(areas.Storefront, areas.Orders), map[areas.Area]bool{areas.Orders: false})
	var blocked *areas.BlockedError
	if !errors.As(err, &blocked) || blocked.Area != areas.Storefront || blocked.Needed != areas.Orders {
		t.Fatalf("orders off under storefront: %v", err)
	}
	// Both off together is fine, in any order.
	next, err = areas.Apply(state(areas.Storefront, areas.Orders), map[areas.Area]bool{areas.Orders: false, areas.Storefront: false})
	if err != nil || next.On(areas.Storefront) || next.On(areas.Orders) {
		t.Fatalf("both off: %v %v", next, err)
	}
	// Asking for the shop on and orders off at once is a conflict, not a surprise.
	if _, err = areas.Apply(state(), map[areas.Area]bool{areas.Storefront: true, areas.Orders: false}); err == nil {
		t.Fatal("storefront on with orders off was accepted")
	}
	// The input is not changed.
	cur := state(areas.Stock)
	if _, err = areas.Apply(cur, map[areas.Area]bool{areas.Stock: false}); err != nil || !cur.On(areas.Stock) {
		t.Fatalf("input changed: %v %v", cur, err)
	}
}

func TestCatalogueAndDefaults(t *testing.T) {
	c := areas.Catalogue()
	if len(c) != len(areas.All) {
		t.Fatalf("catalogue: %d areas", len(c))
	}
	for _, i := range c {
		if i.Name == "" || i.Covers == "" || i.DependsOn == nil {
			t.Errorf("%s: incomplete %+v", i.Area, i)
		}
	}
	for _, a := range areas.All {
		if got, want := areas.DefaultOn(a), a != areas.Schools; got != want {
			t.Errorf("DefaultOn(%s) = %v", a, got)
		}
	}
	if _, ok := areas.Valid("billing"); ok {
		t.Error("billing is not an area")
	}
}
