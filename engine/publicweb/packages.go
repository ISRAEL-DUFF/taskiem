package publicweb

import (
	"net/url"
	"strconv"
	"strings"

	"github.com/israel-duff/taskiem/engine/storefront"
)

// Products in a form (docs/spec/forms-that-sell.md §5): an order form
// that sells one product in packages ("1 for ₦25,000, 2 for ₦45,000"), or
// a basket of some rows. The packages are the published form's: the page
// draws them, and the server prices the package chosen from them, never
// from what the browser sends. A package of N takes N of the item's stock,
// and one the stock cannot fill is greyed and refused. A product sold in
// sizes and colours asks one choice for all its units, or one for each,
// as the owner set.

// Form fields of the packages field.
const (
	packageField = "pkg"  // the package chosen: its quantity
	sizeField    = "pz"   // a size for all, or pz.<n> for unit n
	unitPrefix   = "pz."  // + the unit's number, from 1
	maxShownSize = 30     // sizes listed in a choice
	itemLineMax  = 160    // the product's line under its name
	nameMaxRunes = 80     // the product's name as the top shows it
	oneItemQuery = "item" // ?item=<row>: the product a form that takes any item starts with
)

// oneView is the product a form sells, drawn at the top (§5.3): its name,
// photo and line, with "Secure checkout" where it is paid through
// Taskiem's payment page.
type oneView struct {
	ID, Name, Line, Price string
	Small, Large          string
	Secure                bool
	SoldOut               bool
}

// packagesView is the packages field as the page draws it.
type packagesView struct {
	Title    string
	Item     string
	Packages []packageChoice
	// Sizes are the product's sizes and colours on sale, when it has
	// them; Each asks one per unit (Units, the most a package takes).
	Sizes    []sizeChoice
	SizeWord string
	Each     bool
	Units    []unitChoice
	Size     string // the size chosen for all
	Chosen   int
	SoldOut  bool
}

type packageChoice struct {
	Qty                 int
	Label, Price, Each  string
	PriceKobo           int64
	Short, Chosen, Free bool
}

type sizeChoice struct {
	ID, Name string
	Selected bool
}

type unitChoice struct {
	N      int
	Sizes  []sizeChoice
	Hidden bool
}

// findItem is an item of items by its row id.
func findItem(items []storefront.Item, id string) (storefront.Item, bool) {
	for _, it := range items {
		if it.ID == id {
			return it, true
		}
	}
	return storefront.Item{}, false
}

// oneItemView is the product at the top of a form that sells it.
func oneItemView(it storefront.Item, secure bool) *oneView {
	o := &oneView{ID: it.ID, Name: clipRunes(it.Name, nameMaxRunes), Line: clipRunes(firstLine(it.Description), itemLineMax), Small: it.Photo["320"], Large: it.Photo["800"],
		Secure: secure, SoldOut: !it.Available || it.SoldOut()}
	if o.Large == "" {
		o.Large = o.Small
	}
	if it.PriceKobo != nil {
		o.Price = storefront.FormatNaira(*it.PriceKobo)
	}
	return o
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexAny(s, "\r\n"); i >= 0 {
		s = s[:i]
	}
	return s
}

func clipRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return strings.TrimSpace(string(r[:n-1])) + "…"
}

// sizesLeft are the product's sizes on sale, and the most of any one and
// of all together that are left (-1: not counted).
func sizesLeft(it storefront.Item) (sizes []storefront.Item, most, all int64) {
	most, all = 0, 0
	counted := true
	for _, z := range storefront.SizeItems(it) {
		if !z.Available || z.PriceKobo == nil || z.SoldOut() {
			continue
		}
		sizes = append(sizes, z)
		if z.Stock == nil {
			counted = false
			continue
		}
		all += *z.Stock
		most = max(most, *z.Stock)
	}
	if !counted {
		return sizes, -1, -1
	}
	return sizes, most, all
}

// packagesFieldView draws a published packages field over the front's
// items; nil when the form has none.
func packagesFieldView(form []byte, items []storefront.Item) (*packagesView, storefront.PackagesField) {
	pf, ok := storefront.FormPackages(form)
	if !ok {
		return nil, pf
	}
	v := &packagesView{Title: "Choose a package", Each: pf.Each()}
	it, found := findItem(items, pf.Item)
	if !found || !it.Available || it.PriceKobo == nil {
		v.SoldOut = true
		return v, pf
	}
	v.Item = it.Name
	// How many one package may take: the item's own stock, or for a
	// product in sizes the most of one size (a choice for all) or of all
	// together (a choice for each).
	left := int64(-1)
	if it.Stock != nil {
		left = *it.Stock
	}
	if len(it.Sizes) > 0 {
		sizes, most, all := sizesLeft(it)
		left = most
		if pf.Each() {
			left = all
		}
		v.SizeWord = choiceWords(it)
		for _, z := range sizes {
			v.Sizes = append(v.Sizes, sizeChoice{ID: z.ID, Name: strings.TrimLeft(strings.TrimPrefix(z.Name, it.Name), " –-·,")})
			if len(v.Sizes) == maxShownSize {
				break
			}
		}
		if len(v.Sizes) == 0 {
			v.SoldOut = true
		}
	} else if it.SoldOut() || it.Variants > 0 {
		v.SoldOut = true
	}
	most := 0
	for _, p := range pf.Packages {
		label := p.Label
		if label == "" {
			// The price is beside it.
			label = strconv.Itoa(p.Qty) + " items"
			if p.Qty == 1 {
				label = "1 item"
			}
		}
		c := packageChoice{Qty: p.Qty, Label: label, Price: storefront.FormatNaira(p.PriceKobo), PriceKobo: p.PriceKobo,
			Short: v.SoldOut || (left >= 0 && int64(p.Qty) > left)}
		if p.Qty > 1 {
			c.Each = storefront.FormatNaira((p.UnitKobo()+99)/100*100) + " each" // to the naira
		}
		v.Packages = append(v.Packages, c)
		most = max(most, p.Qty)
	}
	if v.Each && len(v.Sizes) > 0 {
		for n := 1; n <= most; n++ {
			v.Units = append(v.Units, unitChoice{N: n, Sizes: append([]sizeChoice(nil), v.Sizes...)})
		}
	}
	return v, pf
}

// choose marks the package (and sizes) a request names chosen; the first
// package the stock can fill is chosen when none is.
func (v *packagesView) choose(q url.Values) {
	if v == nil {
		return
	}
	want, _ := strconv.Atoi(q.Get(packageField))
	v.Chosen = 0
	for i := range v.Packages {
		v.Packages[i].Chosen = !v.Packages[i].Short && v.Packages[i].Qty == want
		if v.Packages[i].Chosen {
			v.Chosen = want
		}
	}
	if v.Chosen == 0 {
		for i := range v.Packages {
			if !v.Packages[i].Short {
				v.Packages[i].Chosen, v.Chosen = true, v.Packages[i].Qty
				break
			}
		}
	}
	v.Size = q.Get(sizeField)
	for i := range v.Sizes {
		v.Sizes[i].Selected = v.Sizes[i].ID == v.Size
	}
	for u := range v.Units {
		picked := q.Get(unitPrefix + strconv.Itoa(v.Units[u].N))
		for i := range v.Units[u].Sizes {
			v.Units[u].Sizes[i].Selected = v.Units[u].Sizes[i].ID == picked
		}
		v.Units[u].Hidden = v.Chosen > 0 && v.Units[u].N > v.Chosen
	}
}

// packageLines prices the package a form sends: the package by its
// quantity from the published field, the item and its sizes from the
// copy, each size's stock held to what is left. The package is one line,
// or with a size chosen for each unit a line per size, together the
// package at its price.
func (f *formData) packageLines(v url.Values, items []storefront.Item, sub *storefront.Submission) string {
	pf := f.pkgField
	qty, err := strconv.Atoi(strings.TrimSpace(v.Get(packageField)))
	p, ok := pf.Find(qty)
	if err != nil || !ok {
		return "Please choose a package."
	}
	it, found := findItem(items, pf.Item)
	if !found || !it.Available || it.PriceKobo == nil {
		return "This product is not available right now."
	}
	short := func(name string) string {
		return "Not enough " + name + " in stock for that package. Please choose a smaller one."
	}
	type take struct {
		item storefront.Item
		n    int
	}
	var takes []take
	add := func(z storefront.Item, n int) {
		for i := range takes {
			if takes[i].item.ID == z.ID {
				takes[i].n += n
				return
			}
		}
		takes = append(takes, take{z, n})
	}
	if len(it.Sizes) > 0 {
		sizes := storefront.SizeItems(it)
		size := func(id string) (storefront.Item, bool) {
			z, ok := findItem(sizes, id)
			return z, ok && z.Available && z.PriceKobo != nil && !z.SoldOut()
		}
		word := strings.ToLower(choiceWords(it))
		if pf.Each() {
			for n := 1; n <= qty; n++ {
				z, ok := size(v.Get(unitPrefix + strconv.Itoa(n)))
				if !ok {
					return "Please choose a " + word + " for item " + strconv.Itoa(n) + "."
				}
				add(z, 1)
			}
		} else {
			z, ok := size(v.Get(sizeField))
			if !ok {
				return "Please choose a " + word + "."
			}
			add(z, qty)
		}
	} else {
		if it.SoldOut() || it.Variants > 0 {
			return it.Name + " is sold out."
		}
		add(it, qty)
	}
	for _, x := range takes {
		if x.item.Stock != nil && int64(x.n) > *x.item.Stock {
			return short(x.item.Name)
		}
	}
	// Shares of the package's price, by units; the last takes what is
	// left, so the lines add up to the package exactly.
	left := p.PriceKobo
	for i, x := range takes {
		share := p.PriceKobo * int64(x.n) / int64(qty)
		if i == len(takes)-1 {
			share = left
		}
		left -= share
		sub.Basket = append(sub.Basket, storefront.Line{ID: x.item.ID, Name: x.item.Name, Quantity: x.n, PriceKobo: share / int64(x.n), LineKobo: share,
			PackageQty: qty, PackageKobo: p.PriceKobo})
	}
	sub.TotalKobo += p.PriceKobo
	return ""
}

// onlyItems keeps the items a basket names, in its order (nil: all).
func onlyItems(all []formItem, ids []string) []formItem {
	if len(ids) == 0 {
		return all
	}
	var out []formItem
	for _, id := range ids {
		for _, x := range all {
			if x.ID == id {
				x.N = len(out)
				out = append(out, x)
			}
		}
	}
	return out
}
