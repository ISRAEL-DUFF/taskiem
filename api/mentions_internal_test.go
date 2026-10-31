package api

import (
	"reflect"
	"testing"

	"github.com/israel-duff/taskiem/engine/links"
)

// Mentions read (mentions.go): the longest linked name after "@", what is
// asked of each, and the names that are no linked business.
func TestParseMentions(t *testing.T) {
	names := []string{"Dangote", "Dangote Stores", "Cherry Blocks"}
	got := parseMentions("Buy 400 bags of cement from @Dangote Stores and 20 pieces of blocks from @cherry blocks, please", names)
	want := []mentionPart{{Business: "Dangote Stores", Text: "400 bags of cement"}, {Business: "Cherry Blocks", Text: "20 pieces of blocks"}}
	if !reflect.DeepEqual(got.Parts, want) || len(got.Unknown) != 0 {
		t.Fatalf("two businesses: %+v", got)
	}
	if got := parseMentions("order 5 rice from @Dangote", names); len(got.Parts) != 1 || got.Parts[0].Business != "Dangote" || got.Parts[0].Text != "5 rice" {
		t.Fatalf("the shorter name: %+v", got)
	}
	if got := parseMentions("10 bags from @Nobody and 2 from @Dangote", names); !reflect.DeepEqual(got.Unknown, []string{"Nobody"}) {
		t.Fatalf("not linked: %+v", got)
	}
	if got := parseMentions("mail me at ada@example.com", names); len(got.Parts)+len(got.Unknown) != 0 {
		t.Fatalf("an address is no mention: %+v", got)
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
