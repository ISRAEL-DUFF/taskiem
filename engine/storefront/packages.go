package storefront

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

// Packages (docs/spec/forms-that-sell.md §5.2): a field of an order form
// that sells one product in a few bundles, each a quantity at a price of
// its own ("1 for ₦25,000, 2 for ₦45,000"). A package's price is the
// price: it is kept with the action's form, published with the
// storefront, and the server prices a submission from the published form,
// never from what the browser sends.
//
//	{"type":"object","x-form":{"widget":"packages","list":"catalogue","item":"<row>",
//	  "packages":[{"qty":1,"price_kobo":2500000,"label":"1 item + free delivery"},{"qty":2,"price_kobo":4500000}],
//	  "choose":"all"}}
//
// The basket gains "items": the rows it shows, and only those.

// Limits on packages.
const (
	MaxPackages       = 6
	MaxPackageQty     = 20 // what one order may take of an item
	MaxPackageLabel   = 60
	MinPackageKobo    = 100
	MaxPackageKobo    = 10_000_000_000 // ₦100,000,000
	MaxBasketItems    = 50
	maxFieldReference = 64
)

// Package is one bundle: how many, at what price, and the owner's words
// for it (drawn from the quantity and price when there are none).
type Package struct {
	Qty       int    `json:"qty"`
	PriceKobo int64  `json:"price_kobo"`
	Label     string `json:"label,omitempty"`
}

// Title is the package as the buyer reads it: the owner's label, or
// "2 items · ₦45,000".
func (p Package) Title() string {
	if p.Label != "" {
		return p.Label
	}
	return PackageLabel(p.Qty, p.PriceKobo)
}

// PackageLabel is a package's label drawn from its quantity and price.
func PackageLabel(qty int, priceKobo int64) string {
	w := "items"
	if qty == 1 {
		w = "item"
	}
	return fmt.Sprintf("%d %s · %s", qty, w, FormatNaira(priceKobo))
}

// UnitKobo is what one costs in the package, rounded up to the kobo.
func (p Package) UnitKobo() int64 {
	if p.Qty <= 0 {
		return p.PriceKobo
	}
	return (p.PriceKobo + int64(p.Qty) - 1) / int64(p.Qty)
}

// PackagesField is a form's packages field, read.
type PackagesField struct {
	Name     string    `json:"name"`
	List     string    `json:"list"`
	Item     string    `json:"item"`
	Packages []Package `json:"packages"`
	// Choose is how a product sold in sizes and colours is chosen: "all"
	// (one choice for every unit, the default) or "each" (one per unit).
	Choose string `json:"choose,omitempty"`
}

// Find is the package of qty, if the field offers one.
func (f PackagesField) Find(qty int) (Package, bool) {
	for _, p := range f.Packages {
		if p.Qty == qty {
			return p, true
		}
	}
	return Package{}, false
}

// Each reports whether a product sold in sizes and colours asks one
// choice per unit.
func (f PackagesField) Each() bool { return f.Choose == "each" }

type xformField struct {
	Type  string `json:"type"`
	XForm struct {
		Widget   string          `json:"widget"`
		List     string          `json:"list"`
		Item     string          `json:"item"`
		Items    json.RawMessage `json:"items"`
		Packages json.RawMessage `json:"packages"`
		Choose   string          `json:"choose"`
	} `json:"x-form"`
}

func formProps(form []byte) map[string]xformField {
	var f struct {
		Properties map[string]xformField `json:"properties"`
	}
	if json.Unmarshal(form, &f) != nil {
		return nil
	}
	return f.Properties
}

// FormPackages is a form's packages field (ok false for none, or one that
// does not read).
func FormPackages(form []byte) (PackagesField, bool) {
	for _, name := range allFields(form) {
		p := formProps(form)[name]
		if p.XForm.Widget != "packages" {
			continue
		}
		out := PackagesField{Name: name, List: p.XForm.List, Item: p.XForm.Item, Choose: p.XForm.Choose}
		if json.Unmarshal(p.XForm.Packages, &out.Packages) != nil {
			return PackagesField{}, false
		}
		return out, true
	}
	return PackagesField{}, false
}

// BasketItems are the rows a form's basket shows, nil for every row.
func BasketItems(form []byte) []string {
	for _, name := range allFields(form) {
		p := formProps(form)[name]
		if p.XForm.Widget != "basket" || len(p.XForm.Items) == 0 {
			continue
		}
		var ids []string
		if json.Unmarshal(p.XForm.Items, &ids) != nil {
			return nil
		}
		return ids
	}
	return nil
}

// OnForm reports whether a basket whose form names ids (nil: every row)
// may order it: a row named, or a size or colour of one.
func OnForm(it Item, ids []string) bool {
	if len(ids) == 0 {
		return true
	}
	for _, id := range ids {
		if it.ID == id || (it.VariantOf != "" && it.VariantOf == id) {
			return true
		}
	}
	return false
}

// OneItem is the single product a form sells, when it names one: its
// packages field's item, or a basket of one row (§5.3). The page draws it
// at the top and never asks the buyer to pick a product.
func OneItem(form []byte) string {
	if p, ok := FormPackages(form); ok {
		return p.Item
	}
	if ids := BasketItems(form); len(ids) == 1 {
		return ids[0]
	}
	return ""
}

var fieldReference = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.:-]*$`)

// checkSelling checks a form's basket and packages fields: a packages
// field names a list and one of its rows, has one to six packages, each a
// quantity once and a price, with an optional label; a basket's items are
// rows, each once; a form sells by a basket or by packages, not both.
func checkSelling(form []byte) error {
	props := formProps(form)
	basket, packages := "", ""
	for _, name := range allFields(form) {
		p := props[name]
		switch p.XForm.Widget {
		case "basket":
			basket = name
			if len(p.XForm.Items) > 0 {
				var ids []string
				if err := json.Unmarshal(p.XForm.Items, &ids); err != nil || len(ids) == 0 {
					return fmt.Errorf("%w: a basket's items are the rows it shows, a list of one or more", ErrInvalid)
				}
				if len(ids) > MaxBasketItems {
					return fmt.Errorf("%w: a basket shows up to %d items", ErrInvalid, MaxBasketItems)
				}
				seen := map[string]bool{}
				for _, id := range ids {
					if !validReference(id) || seen[id] {
						return fmt.Errorf("%w: a basket names each of its items once, by its row", ErrInvalid)
					}
					seen[id] = true
				}
			}
		case "packages":
			packages = name
			if err := checkPackages(p); err != nil {
				return err
			}
		}
	}
	if basket != "" && packages != "" {
		return fmt.Errorf("%w: a form sells by a basket or by packages, not both", ErrInvalid)
	}
	return nil
}

func validReference(s string) bool {
	return len(s) <= maxFieldReference && fieldReference.MatchString(s)
}

func checkPackages(p xformField) error {
	if p.Type != "object" {
		return fmt.Errorf("%w: a packages field is an object", ErrInvalid)
	}
	if !validReference(p.XForm.List) {
		return fmt.Errorf("%w: a packages field names the list its product is on", ErrInvalid)
	}
	if !validReference(p.XForm.Item) {
		return fmt.Errorf("%w: a packages field names the one product it sells", ErrInvalid)
	}
	if p.XForm.Choose != "" && p.XForm.Choose != "all" && p.XForm.Choose != "each" {
		return fmt.Errorf("%w: sizes and colours are chosen once for all (all) or for each one (each)", ErrInvalid)
	}
	var list []Package
	dec := json.NewDecoder(strings.NewReader(string(p.XForm.Packages)))
	dec.DisallowUnknownFields()
	if len(p.XForm.Packages) == 0 || dec.Decode(&list) != nil {
		return fmt.Errorf("%w: packages are a list, each a quantity, a price in kobo and an optional label", ErrInvalid)
	}
	if len(list) == 0 || len(list) > MaxPackages {
		return fmt.Errorf("%w: a form offers one to %d packages", ErrInvalid, MaxPackages)
	}
	seen := map[int]bool{}
	for _, x := range list {
		if x.Qty < 1 || x.Qty > MaxPackageQty {
			return fmt.Errorf("%w: a package is 1 to %d items", ErrInvalid, MaxPackageQty)
		}
		if seen[x.Qty] {
			return fmt.Errorf("%w: two packages of %d: each quantity is offered once", ErrInvalid, x.Qty)
		}
		seen[x.Qty] = true
		if x.PriceKobo < MinPackageKobo || x.PriceKobo > MaxPackageKobo {
			return fmt.Errorf("%w: a package's price is from ₦1 to ₦100,000,000", ErrInvalid)
		}
		if l := x.Label; l != strings.TrimSpace(l) || len([]rune(l)) > MaxPackageLabel || hasLink(l) || strings.ContainsAny(l, "\n\r") {
			return fmt.Errorf("%w: a package's label is up to %d characters on one line, with no links", ErrInvalid, MaxPackageLabel)
		}
	}
	return nil
}

// PackageWarning says a package costs more per item than the product's
// own price; a warning when publishing, not a refusal (a bundle may carry
// a gift). Empty when it does not.
func PackageWarning(item string, p Package, priceKobo int64) string {
	if p.Qty <= 0 || priceKobo <= 0 || p.PriceKobo <= priceKobo*int64(p.Qty) {
		return ""
	}
	return fmt.Sprintf("The %s package of %s costs %s an item, more than its own price of %s.", PackageLabel(p.Qty, p.PriceKobo), item, FormatNaira(p.UnitKobo()), FormatNaira(priceKobo))
}

// PackagesForm is an order form that sells one product in packages, with
// the questions of extra (a JSON Schema object's properties, or nil).
func PackagesForm(list, item string, pkgs []Package, choose string, extra map[string]json.RawMessage) ([]byte, error) {
	props := map[string]any{}
	for k, v := range extra {
		props[k] = v
	}
	xf := map[string]any{"widget": "packages", "list": list, "item": item, "packages": pkgs}
	if choose != "" {
		xf["choose"] = choose
	}
	props["package"] = map[string]any{"type": "object", "title": "Choose a package", "x-form": xf}
	form, err := json.Marshal(map[string]any{"type": "object", "properties": props})
	if err != nil {
		return nil, err
	}
	return form, CheckForm(form)
}

// BasketForm is an order form whose basket shows only items (nil: every
// row), with the questions of extra.
func BasketForm(list string, items []string, extra map[string]json.RawMessage) ([]byte, error) {
	props := map[string]any{}
	for k, v := range extra {
		props[k] = v
	}
	xf := map[string]any{"widget": "basket", "list": list, "min": 1}
	if len(items) > 0 {
		xf["items"] = items
	}
	props["basket"] = map[string]any{"type": "array", "title": "Your order", "x-form": xf}
	form, err := json.Marshal(map[string]any{"type": "object", "properties": props})
	if err != nil {
		return nil, err
	}
	return form, CheckForm(form)
}
