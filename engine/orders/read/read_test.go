package read

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/israel-duff/taskiem/engine/ai"
	"github.com/israel-duff/taskiem/engine/storefront"
	"github.com/israel-duff/taskiem/engine/variants"
)

func kobo(n int64) *int64 { return &n }

// A food business's catalogue, and a shoe seller's product in sizes.
func catalogue() []storefront.Item {
	return []storefront.Item{
		{ID: "jp", Name: "Jollof rice plate", PriceKobo: kobo(450000), Available: true},
		{ID: "jc", Name: "Jollof rice cooler", PriceKobo: kobo(3800000), Available: true},
		{ID: "fr", Name: "Fried rice", PriceKobo: kobo(400000), Available: true},
		{ID: "ch", Name: "Chapman", PriceKobo: kobo(150000), Available: true, Stock: kobo(3)},
		{ID: "mp", Name: "Moi moi", Available: true},
		{ID: "pd", Name: "Pounded yam", PriceKobo: kobo(300000), Available: false},
		{ID: "sn", Name: "Sneakers", PriceKobo: kobo(2500000), Available: true,
			Choices: []variants.Choice{{Name: "Size", Values: []string{"42", "43"}}, {Name: "Colour", Values: []string{"Black", "White"}}},
			Sizes: []storefront.Size{
				{ID: "sn42b", Variant: map[string]string{"Size": "42", "Colour": "Black"}},
				{ID: "sn43b", Variant: map[string]string{"Size": "43", "Colour": "Black"}, Stock: kobo(0)},
				{ID: "sn42w", Variant: map[string]string{"Size": "42", "Colour": "White"}, PriceKobo: kobo(2700000)},
			}},
	}
}

// read runs a message through Request, a fake model answering said, Parse
// and Match, as the server does.
func read(t *testing.T, text string, said Said) Draft {
	t.Helper()
	raw, _ := json.Marshal(said)
	model := &ai.Fake{Respond: func(r ai.Request) (*ai.Response, error) {
		return &ai.Response{JSON: raw}, nil
	}}
	req := Request(text, catalogue(), nil)
	resp, err := model.Complete(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	s, err := Parse(resp)
	if err != nil {
		t.Fatal(err)
	}
	return Match(s, catalogue())
}

func TestCorpus(t *testing.T) {
	cases := []struct {
		name  string
		text  string
		said  Said
		check func(t *testing.T, d Draft)
	}{
		{"a clear order, delivered", "2 fried rice and 1 chapman, deliver to 12 Herbert Macaulay by 1pm",
			Said{Orderable: true, Lines: []SaidLine{{Words: "2 fried rice", Item: "Fried rice", Quantity: 2}, {Words: "1 chapman", Item: "Chapman", Quantity: 1}},
				Fulfilment: "delivery", Address: "12 Herbert Macaulay", When: "by 1pm"},
			func(t *testing.T, d Draft) {
				if !d.Complete() || d.Total() != 2*400000+150000 {
					t.Fatalf("draft: %+v total %d", d, d.Total())
				}
				if d.Fulfilment != "delivery" || d.Address != "12 Herbert Macaulay" || d.When != "by 1pm" {
					t.Fatalf("delivery: %+v", d)
				}
			}},
		{"misspelt, the model names it", "2 frid rce pls",
			Said{Orderable: true, Lines: []SaidLine{{Words: "2 frid rce", Item: "Fried rice", Quantity: 2}}},
			func(t *testing.T, d Draft) {
				if d.Lines[0].ItemID != "fr" || d.Lines[0].Said != "2 frid rce" {
					t.Fatalf("%+v", d.Lines[0])
				}
			}},
		{"two that fit: a question, never a pick", "2 jolof",
			Said{Orderable: true, Lines: []SaidLine{{Words: "2 jollof", Quantity: 2}}},
			func(t *testing.T, d Draft) {
				l := d.Lines[0]
				if l.ItemID != "" || l.Ask == nil || l.Ask.Kind != AskWhich || len(l.Ask.Choices) != 2 {
					t.Fatalf("%+v", l)
				}
				if l.Ask.Text != "2 jollof: Jollof rice plate (₦4,500) or Jollof rice cooler (₦38,000)?" {
					t.Fatalf("question: %q", l.Ask.Text)
				}
				if d.Complete() {
					t.Fatal("complete with a question open")
				}
			}},
		{"misspelt, and two fit: still a question", "2 jolof",
			Said{Orderable: true, Lines: []SaidLine{{Words: "2 jolof", Quantity: 2}}},
			func(t *testing.T, d Draft) {
				if l := d.Lines[0]; l.ItemID != "" || l.Ask == nil || len(l.Ask.Choices) != 2 {
					t.Fatalf("%+v", l)
				}
			}},
		{"misspelt, one fits", "1 chapmn",
			Said{Orderable: true, Lines: []SaidLine{{Words: "1 chapmn", Quantity: 1}}},
			func(t *testing.T, d Draft) {
				if l := d.Lines[0]; l.ItemID != "ch" {
					t.Fatalf("%+v", l)
				}
			}},
		{"pidgin, quantity in words", "abeg send me two pack of fried rice",
			Said{Orderable: true, Lines: []SaidLine{{Words: "two pack of fried rice", Item: "Fried rice", Quantity: 2}}},
			func(t *testing.T, d Draft) {
				if d.Lines[0].ItemID != "fr" || d.Lines[0].Quantity != 2 {
					t.Fatalf("%+v", d.Lines[0])
				}
			}},
		{"no number: how many", "fried rice",
			Said{Orderable: true, Lines: []SaidLine{{Words: "fried rice", Item: "Fried rice"}}},
			func(t *testing.T, d Draft) {
				if l := d.Lines[0]; l.ItemID != "fr" || l.Ask == nil || l.Ask.Kind != AskHowMany || l.Ask.Text != "How many Fried rice?" {
					t.Fatalf("%+v", l)
				}
			}},
		{"a name the model made up is not taken", "1 shawarma",
			Said{Orderable: true, Lines: []SaidLine{{Words: "1 shawarma", Item: "Shawarma", Quantity: 1}}},
			func(t *testing.T, d Draft) {
				if l := d.Lines[0]; l.ItemID != "" || l.Ask != nil || l.Said != "1 shawarma" {
					t.Fatalf("%+v", l)
				}
				if d.Complete() || d.Total() != 0 {
					t.Fatal("an unknown line counted")
				}
			}},
		{"no size said: which size", "1 sneakers",
			Said{Orderable: true, Lines: []SaidLine{{Words: "1 sneakers", Item: "Sneakers", Quantity: 1}}},
			func(t *testing.T, d Draft) {
				l := d.Lines[0]
				if l.Ask == nil || l.Ask.Kind != AskSize || len(l.Ask.Choices) != 3 || l.Ask.Text != "Which Sneakers?" {
					t.Fatalf("%+v", l)
				}
				if *l.Ask.Choices[0].PriceKobo != 2500000 || *l.Ask.Choices[2].PriceKobo != 2700000 {
					t.Fatalf("prices: %+v", l.Ask.Choices)
				}
			}},
		{"size and colour said: that size", "sneakers 42 white",
			Said{Orderable: true, Lines: []SaidLine{{Words: "sneakers 42 white", Item: "Sneakers", Quantity: 1, Choices: []SaidChoice{{"Size", "42"}, {"Colour", "white"}}}}},
			func(t *testing.T, d Draft) {
				if l := d.Lines[0]; l.ItemID != "sn42w" || l.PriceKobo != 2700000 || l.Ask != nil {
					t.Fatalf("%+v", l)
				}
			}},
		{"a colour said: the sizes in it", "2 black sneakers",
			Said{Orderable: true, Lines: []SaidLine{{Words: "2 black sneakers", Item: "Sneakers", Quantity: 2, Choices: []SaidChoice{{"Colour", "black"}}}}},
			func(t *testing.T, d Draft) {
				l := d.Lines[0]
				if l.Ask == nil || len(l.Ask.Choices) != 2 || l.Ask.Choices[0].ItemID != "sn42b" || l.Ask.Choices[1].ItemID != "sn43b" {
					t.Fatalf("%+v", l.Ask)
				}
			}},
		{"more than is left: shown", "5 chapman",
			Said{Orderable: true, Lines: []SaidLine{{Words: "5 chapman", Item: "Chapman", Quantity: 5}}},
			func(t *testing.T, d Draft) {
				if l := d.Lines[0]; !l.Out || *l.Left != 3 {
					t.Fatalf("%+v", l)
				}
			}},
		{"not on sale: kept as written", "1 pounded yam",
			Said{Orderable: true, Lines: []SaidLine{{Words: "1 pounded yam", Item: "Pounded yam", Quantity: 1}}},
			func(t *testing.T, d Draft) {
				if l := d.Lines[0]; l.ItemID != "" {
					t.Fatalf("%+v", l)
				}
			}},
		{"no price: staff give one", "3 moi moi",
			Said{Orderable: true, Lines: []SaidLine{{Words: "3 moi moi", Item: "Moi moi", Quantity: 3}}},
			func(t *testing.T, d Draft) {
				if l := d.Lines[0]; l.ItemID != "mp" || !l.NoPrice || d.Complete() {
					t.Fatalf("%+v", l)
				}
			}},
		{"two orders in one message", "2 fried rice for me, and for my sister 1 jollof rice cooler",
			Said{Orderable: true, Lines: []SaidLine{{Words: "2 fried rice", Item: "Fried rice", Quantity: 2}, {Words: "1 jollof rice cooler", Item: "Jollof rice cooler", Quantity: 1}}},
			func(t *testing.T, d Draft) {
				if len(d.Lines) != 2 || d.Lines[1].ItemID != "jc" {
					t.Fatalf("%+v", d.Lines)
				}
			}},
		{"nothing orderable", "good afternoon, are you open today?",
			Said{Orderable: false},
			func(t *testing.T, d Draft) {
				if len(d.Lines) != 0 {
					t.Fatalf("%+v", d)
				}
			}},
		{"a wild quantity is not taken", "999999 chapman",
			Said{Orderable: true, Lines: []SaidLine{{Words: "999999 chapman", Item: "Chapman", Quantity: 999999}}},
			func(t *testing.T, d Draft) {
				if l := d.Lines[0]; l.Quantity != 0 || l.Ask == nil || l.Ask.Kind != AskHowMany {
					t.Fatalf("%+v", l)
				}
			}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) { c.check(t, read(t, c.text, c.said)) })
	}
}

func TestRequestShowsTheCatalogueAndQuestions(t *testing.T) {
	q := []Question{{Kind: AskWhich, Text: "Which one: jollof?", Choices: []Choice{{ItemID: "jp", Name: "Jollof rice plate"}, {ItemID: "jc", Name: "Jollof rice cooler"}}}}
	r := Request("the plate", catalogue(), q)
	all := r.System[0].Text + r.System[1].Text
	for _, want := range []string{"- Jollof rice plate", "- Sneakers – 42, Black", "- Chapman"} {
		if !strings.Contains(all, want) {
			t.Fatalf("catalogue lacks %q", want)
		}
	}
	if strings.Contains(all, "Pounded yam") {
		t.Fatal("an item not on sale is shown")
	}
	if !strings.Contains(r.Messages[0].Text, "1. Which one: jollof? (choices: Jollof rice plate; Jollof rice cooler)") || !strings.HasSuffix(r.Messages[0].Text, "the plate") {
		t.Fatalf("message: %q", r.Messages[0].Text)
	}
	if r.Schema == nil || !r.System[1].Cache {
		t.Fatal("no schema, or the catalogue not cached")
	}
	if _, err := Parse(&ai.Response{Text: "sorry"}); err != ErrUnread {
		t.Fatalf("unread: %v", err)
	}
}

func TestAnswers(t *testing.T) {
	items := catalogue()
	d := Match(Said{Lines: []SaidLine{{Words: "2 jollof", Quantity: 2}, {Words: "fried rice", Item: "Fried rice"}, {Words: "sneakers", Item: "Sneakers", Quantity: 1}}}, items)
	if len(d.Open()) != 3 {
		t.Fatalf("open: %v", d.Open())
	}
	text := Questions(d)
	if !strings.HasPrefix(text, "1. 2 jollof: Jollof rice plate (₦4,500) or Jollof rice cooler (₦38,000)?\n2. How many Fried rice?\n3. Which Sneakers? Sneakers – 42, Black (₦25,000), Sneakers – 43, Black (₦25,000) or Sneakers – 42, White (₦27,000)") {
		t.Fatalf("questions: %q", text)
	}
	// "the plate", 3, and the second choice.
	if !ApplyAnswers(&d, Said{Answers: []SaidAnswer{{Question: 1, Answer: "plate"}, {Question: 2, Quantity: 3}, {Question: 3, Answer: "2"}}, Address: "Yaba", Fulfilment: "delivery"}, items) {
		t.Fatal("nothing changed")
	}
	if d.Lines[0].ItemID != "jp" || d.Lines[0].Quantity != 2 || d.Lines[1].Quantity != 3 || d.Lines[2].ItemID != "sn43b" {
		t.Fatalf("lines: %+v", d.Lines)
	}
	if !d.Lines[2].Out {
		t.Fatal("a sold-out size not shown as out")
	}
	if !d.Complete() || d.Address != "Yaba" || d.Fulfilment != "delivery" {
		t.Fatalf("draft: %+v", d)
	}
	// An answer that fits two choices, or none, settles nothing.
	d2 := Match(Said{Lines: []SaidLine{{Words: "jollof", Quantity: 1}}}, items)
	if Answer(&d2, 0, "jollof rice", 0, items) || Answer(&d2, 0, "7", 0, items) {
		t.Fatal("an unclear answer settled a question")
	}
	// Staff choose the cooler.
	if !Choose(&d2, 0, "jc", items) || d2.Lines[0].ItemID != "jc" || !d2.Complete() {
		t.Fatalf("%+v", d2.Lines[0])
	}
}

func TestOneOff(t *testing.T) {
	for a, want := range map[[2]string]bool{
		{"jolof", "jollof"}: true, {"chapmn", "chapman"}: true, {"rice", "rica"}: true, {"rice", "rice"}: true,
		{"jolf", "jollof"}: false, {"egg", "egs"}: false, {"plate", "plant"}: false, {"plate", "plane"}: true, {"fried", "bread"}: false,
	} {
		if got := oneOff(a[0], a[1]); got != want {
			t.Errorf("%v: %v", a, got)
		}
	}
}

func TestMightOrder(t *testing.T) {
	items := catalogue()
	for text, want := range map[string]bool{
		"2 jolof abeg":          true,
		"I want chapman":        true,
		"good morning":          false,
		"please send 2 plates":  false,
		"how far, una dey open": false,
	} {
		if got := MightOrder(text, items); got != want {
			t.Errorf("%q: %v", text, got)
		}
	}
}

func TestRefreshKeepsStaffPrices(t *testing.T) {
	items := catalogue()
	d := Match(Said{Lines: []SaidLine{{Words: "2 fried rice", Item: "Fried rice", Quantity: 2}, {Words: "1 chapman", Item: "Chapman", Quantity: 1}}}, items)
	d.Lines[1].PriceKobo = 100000
	*items[2].PriceKobo = 420000
	Refresh(&d, items, func(i int) bool { return i == 1 })
	if d.Lines[0].PriceKobo != 420000 || d.Lines[1].PriceKobo != 100000 {
		t.Fatalf("%+v", d.Lines)
	}
}
