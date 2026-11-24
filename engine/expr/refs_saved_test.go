package expr

import "testing"

func TestSavedRefs(t *testing.T) {
	e := MustNew()
	for src, want := range map[string]struct {
		names   []string
		unnamed bool
	}{
		`=saved.price_list.body`:                      {[]string{"price_list"}, false},
		`=saved["price-list"].body + saved.mum.phone`: {[]string{"price-list", "mum"}, false},
		`=saved.?price_list.orValue({}).body`:         {[]string{"price_list"}, false},
		`=saved[?"x-y"].orValue({})`:                  {[]string{"x-y"}, false},
		`=saved[trigger.body.n].body`:                 {nil, true},
		`=saved`:                                      {nil, true},
		`=has(saved.mum)`:                             {[]string{"mum"}, false},
	} {
		r, err := e.References(src)
		if err != nil {
			t.Fatalf("%s: %v", src, err)
		}
		if r.SavedUnnamed != want.unnamed || len(r.Saved) != len(want.names) {
			t.Errorf("%s: %v unnamed %v", src, r.Saved, r.SavedUnnamed)
		}
		for _, n := range want.names {
			if !r.Saved[n] {
				t.Errorf("%s: no %s in %v", src, n, r.Saved)
			}
		}
	}
}
