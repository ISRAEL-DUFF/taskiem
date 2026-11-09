package publicweb

import (
	"encoding/json"
	"net/url"
	"strconv"
	"strings"

	"github.com/israel-duff/taskiem/engine/storefront"
)

// Sizes and colours on the page (docs/spec/chat-categories-and-variants.md,
// phase 4): a product sold in them is picked on its own page, a choice at a
// time ("z.<product>.<choice>"), with a quantity under the product's id.
// The pick becomes a line of the size's own row before the bag is read, so
// the bag, the order and the stock it takes are the size's, as for any
// item; the form never lists the sizes one by one.

// pickField is the name of a product's choice on its page.
func pickField(product string, choice int) string {
	return "z." + product + "." + strconv.Itoa(choice)
}

// takeSizePicks turns each product's pick into its size's quantity, added
// to any already in the bag, and takes the pick away; a message when the
// product is not on sale, or a pick names no size on sale, or one with
// none left. It runs once per request (keepBasket): the pick is gone from
// v after it.
func (f *formData) takeSizePicks(v url.Values) string {
	msg := ""
	for _, p := range f.products {
		raw := strings.TrimSpace(v.Get(quantityField + p.ID))
		picked := map[string]string{}
		for i, c := range p.Choices {
			if x := strings.TrimSpace(v.Get(pickField(p.ID, i))); x != "" {
				picked[c.Name] = x
			}
			v.Del(pickField(p.ID, i))
		}
		if raw == "" {
			continue
		}
		v.Del(quantityField + p.ID)
		n, err := strconv.Atoi(raw)
		if err != nil || n <= 0 {
			continue
		}
		z, ok := p.FindSize(picked)
		switch {
		case !p.Available || p.PriceKobo == nil:
			if msg == "" {
				msg = p.Name + " is not available right now. Please choose something else."
			}
			continue
		case !ok:
			if msg == "" {
				msg = "Please choose a " + strings.ToLower(choiceWords(p)) + " for " + p.Name + "."
			}
			continue
		case z.SoldOut():
			if msg == "" {
				msg = z.Name(p) + " is sold out. Please choose another."
			}
			continue
		}
		if was, err := strconv.Atoi(v.Get(quantityField + z.ID)); err == nil && was > 0 {
			n += was
		}
		v.Set(quantityField+z.ID, strconv.Itoa(n))
	}
	return msg
}

// choiceWords names a product's choices: "Size and colour".
func choiceWords(p storefront.Item) string {
	names := make([]string, 0, len(p.Choices))
	for _, c := range p.Choices {
		names = append(names, strings.ToLower(c.Name))
	}
	switch len(names) {
	case 0:
		return "Size"
	case 1:
		return strings.ToUpper(names[0][:1]) + names[0][1:]
	}
	w := strings.Join(names[:len(names)-1], ", ") + " and " + names[len(names)-1]
	return strings.ToUpper(w[:1]) + w[1:]
}

// sizePick is a product's choices as its page asks them: each value with
// whether any size of it is left, and the photo of a colour that has one.
type sizePick struct {
	Input  string
	Name   string
	Kind   string // "size", "colour" or "" (optkind)
	Values []pickValue
}

type pickValue struct {
	Value string
	Out   bool
	// Photo and Large are a colour's own photo, small and for the main
	// place, when its sizes have one.
	Photo, Large string
	First        bool
}

// sizePicks are the product's choices for its page.
func sizePicks(p storefront.Item) []sizePick {
	out := make([]sizePick, 0, len(p.Choices))
	for i, c := range p.Choices {
		pk := sizePick{Input: pickField(p.ID, i), Name: c.Name, Kind: optkind(c.Name)}
		for j, x := range c.Values {
			pv := pickValue{Value: x, Out: true, First: j == 0}
			for _, z := range p.Sizes {
				if z.Variant[c.Name] != x {
					continue
				}
				if !z.SoldOut() {
					pv.Out = false
				}
				if pv.Photo == "" && z.Photo != nil {
					pv.Photo, pv.Large = z.Photo["320"], z.Photo["800"]
				}
			}
			pk.Values = append(pk.Values, pv)
		}
		out = append(out, pk)
	}
	return out
}

// sizeData is the sizes as the page's script reads them, to name the
// line of the size picked, show its price and hold its quantity to what
// is left: JSON, [id, price or null, left or null, [values…]] per size,
// the values in the choices' order (escaped as an attribute by the
// template, so a value may hold any character).
func sizeData(p storefront.Item) string {
	rows := make([][]any, 0, len(p.Sizes))
	for _, z := range p.Sizes {
		vals := make([]string, 0, len(p.Choices))
		for _, c := range p.Choices {
			vals = append(vals, z.Variant[c.Name])
		}
		rows = append(rows, []any{z.ID, z.PriceKobo, z.Stock, vals})
	}
	raw, err := json.Marshal(rows)
	if err != nil {
		return "[]"
	}
	return string(raw)
}

// anyLeft reports whether a product has a size or colour with some left.
func anyLeft(p storefront.Item) bool {
	for _, z := range p.Sizes {
		if !z.SoldOut() {
			return true
		}
	}
	return false
}

// pickable reports whether a product sold in sizes and colours can be
// ordered from its page: on sale, priced, with some size left.
func pickable(p storefront.Item) bool {
	return len(p.Sizes) > 0 && p.Available && p.PriceKobo != nil && anyLeft(p)
}
