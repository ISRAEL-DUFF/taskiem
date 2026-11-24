package api

import "testing"

// A card a customer shared, opened in the business's copy: what it held,
// or once the customer was erased (nothing to open), that it is gone.
func TestSealedCard(t *testing.T) {
	if c := sealedCard("saved", `{"type":"saved","title":"Home","saved":{"kind":"place","name":"home","title":"Home","details":{},"lines":[],"sensitive":false}}`); c.Title != "Home" || c.Saved == nil || c.Sealed != nil {
		t.Fatalf("opened: %+v", c)
	}
	for _, raw := range []string{"", "not json"} {
		if c := sealedCard("saved", raw); c.Title != "Details no longer kept" || c.Saved != nil {
			t.Fatalf("%q: %+v", raw, c)
		}
	}
}
