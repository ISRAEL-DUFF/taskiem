package api

import (
	"reflect"
	"testing"

	"github.com/israel-duff/taskiem/engine/links"
)

// Mentions read (mentions.go): the longest linked name after "@", what is
// asked of each, and the names that are no linked business.
func TestParseMentions(t *testing.T) {
	act := func(names ...string) []links.Link {
		out := make([]links.Link, len(names))
		for i, n := range names {
			out[i] = links.Link{Name: n, State: links.Active}
		}
		return out
	}
	type bt struct{ Business, Text string }
	parts := func(mo mentionOrder) []bt {
		out := []bt{}
		for _, p := range mo.Parts {
			out = append(out, bt{p.Business, p.Text})
		}
		return out
	}
	names := act("Dangote", "Dangote Stores", "Cherry Blocks")
	got := parseMentions("Buy 400 bags of cement from @Dangote Stores and 20 pieces of blocks from @cherry blocks, please", names)
	if want := []bt{{"Dangote Stores", "400 bags of cement"}, {"Cherry Blocks", "20 pieces of blocks"}}; !reflect.DeepEqual(parts(got), want) || len(got.Unknown) != 0 {
		t.Fatalf("two businesses: %+v", got)
	}
	if got := parseMentions("order 5 rice from @Dangote", names); !reflect.DeepEqual(parts(got), []bt{{"Dangote", "5 rice"}}) || len(got.Parts[0].Links) != 1 {
		t.Fatalf("the shorter name: %+v", got)
	}
	if got := parseMentions("10 bags from @Nobody and 2 from @Dangote", names); !reflect.DeepEqual(got.Unknown, []string{"Nobody"}) {
		t.Fatalf("not linked: %+v", got)
	}
	if got := parseMentions("mail me at ada@example.com", names); len(got.Parts)+len(got.Unknown) != 0 {
		t.Fatalf("an address is no mention: %+v", got)
	}
	// Characters whose lower case is another length in bytes (the Kelvin
	// sign, a dotted capital I) neither panic nor shift what follows.
	if got := parseMentions("3 bags from @KK and 2 rice from @Dangote", names); len(got.Unknown) != 1 || !reflect.DeepEqual(parts(got), []bt{{"Dangote", "2 rice"}}) {
		t.Fatalf("the Kelvin sign: %+v", got)
	}
	if got := parseMentions("İyi 5 rice from @İzmir Stores and 2 garri from @dangote", append(names, act("İzmir Stores")...)); !reflect.DeepEqual(parts(got), []bt{{"İzmir Stores", "İyi 5 rice"}, {"Dangote", "2 garri"}}) {
		t.Fatalf("a dotted capital I: %+v", got)
	}
	if got := parseMentions("2 from @KELVİN", append(names, act("Kelvin")...)); len(got.Parts) != 0 || len(got.Unknown) != 1 {
		t.Fatalf("İ is not i: %+v", got)
	}
	// The start of a name names it (docs/spec/direct-messages.md §3).
	long := act("Dangote Nigeria Ltd", "BUA Foods Plc")
	if got := parseMentions("Buy 20 bags of cement from @Dangote and 5 sugar from @bua", long); !reflect.DeepEqual(parts(got), []bt{{"Dangote Nigeria Ltd", "20 bags of cement"}, {"BUA Foods Plc", "5 sugar"}}) {
		t.Fatalf("the start of the name: %+v", got)
	}
	got = parseMentions("@Dangote price?", act("Dangote Cement", "Dangote Sugar"))
	if len(got.Parts) != 1 || len(got.Parts[0].Links) != 2 || got.Parts[0].Typed != "Dangote" || got.Parts[0].At != 0 || got.Parts[0].End != 8 {
		t.Fatalf("two that fit: %+v", got)
	}
}

func TestAskedItems(t *testing.T) {
	list := []links.PriceItem{
		{ItemID: "r", Name: "Rice, 50 kg", PriceKobo: 1},
		{ItemID: "g", Name: "Garri", PriceKobo: 1},
		{ItemID: "b1", Name: "Blocks, 6 inch", PriceKobo: 1},
		{ItemID: "b2", Name: "Blocks, 9 inch", PriceKobo: 1},
	}
	got, bad, which := askedItems("10 bags of Rice, 50 kg and 5 garri", list)
	if len(bad)+len(which) != 0 || len(got) != 2 || got[0].Item.ItemID != "r" || got[0].Quantity != 10 || got[1].Item.ItemID != "g" || got[1].Quantity != 5 {
		t.Fatalf("a name with a comma, and words: %+v %v %v", got, bad, which)
	}
	if _, _, which := askedItems("20 pieces of blocks", list); len(which) != 1 || len(which[0]) != 2 {
		t.Fatalf("which blocks: %v", which)
	}
	if _, bad, _ := askedItems("3 tins of milk", list); len(bad) != 1 {
		t.Fatalf("not on the list: %v", bad)
	}
	if got, _, _ := askedItems("1,200 garri", list); len(got) != 1 || got[0].Quantity != 1200 {
		t.Fatalf("a thousand: %+v", got)
	}
}
