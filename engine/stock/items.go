package stock

import (
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"

	"github.com/israel-duff/taskiem/engine/storefront"
	"github.com/israel-duff/taskiem/engine/variants"
)

// Item is a catalogue row as stock sees it: the columns the mapping
// names, read from the table itself (the public copy holds no cost).
type Item struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Available bool   `json:"available"`
	// Stock is nil when the row holds no count.
	Stock     *int64 `json:"stock"`
	Unit      string `json:"unit,omitempty"`
	PriceKobo *int64 `json:"price_kobo,omitempty"`
	// CostKobo is the last price paid for one unit.
	CostKobo *int64 `json:"cost_kobo,omitempty"`
	Reorder  *int64 `json:"reorder_level,omitempty"`
	Target   *int64 `json:"target_level,omitempty"`
	Supplier string `json:"supplier,omitempty"`
	// Category is the owner's label for the kind of thing it is
	// ("Sneakers"), when the catalogue has a category column.
	Category string `json:"category,omitempty"`
	// Description is the item's words on the storefront, when mapped:
	// search reads it.
	Description string `json:"description,omitempty"`
	// Sizes and colours (engine/variants): a variant's product row and its
	// values; a product's choices. A product with variants keeps no stock
	// of its own: its count is theirs together.
	VariantOf string            `json:"variant_of,omitempty"`
	Variant   map[string]string `json:"variant,omitempty"`
	Choices   []variants.Choice `json:"choices,omitempty"`
	// OwnPrice is, on a product, the Keys of its sizes and colours with a
	// price of their own; the others follow the product's.
	OwnPrice []string `json:"own_price,omitempty"`
}

// HasSizes reports whether the item is a product sold in sizes and
// colours: it keeps no stock of its own, and is sold, counted and
// received through them.
func (it Item) HasSizes() bool { return len(it.Choices) > 0 }

// Retired reports whether the item is a size or colour taken off sale: it
// keeps its history, and is offered nowhere.
func (it Item) Retired() bool { return it.VariantOf != "" && !it.Available }

// Low reports whether the item is at or below its reorder level. A
// product with sizes and colours is never low itself: it keeps no stock of
// its own, and each of them is low or not.
func (it Item) Low() bool {
	return !it.HasSizes() && it.Stock != nil && it.Reorder != nil && *it.Stock <= *it.Reorder
}

// Suggest is how many to order of a low item: up to its target level,
// else back to twice its reorder level; 0 when it is not low.
func (it Item) Suggest() int64 {
	if !it.Low() {
		return 0
	}
	if it.Target != nil && *it.Target > *it.Stock {
		return *it.Target - *it.Stock
	}
	return max(1, 2**it.Reorder-*it.Stock)
}

// UnitName is the item's unit for n of them, plain: "bag", "bags"; ""
// when it has none.
func (it Item) UnitName(n int64) string {
	u := strings.TrimSpace(it.Unit)
	if u == "" || n == 1 || n == -1 {
		return u
	}
	return Plural(u)
}

// Plural is a unit word for more than one: bag, bags; box, boxes.
func Plural(u string) string {
	l := strings.ToLower(u)
	switch {
	case strings.HasSuffix(l, "s"), strings.HasSuffix(l, "x"), strings.HasSuffix(l, "ch"), strings.HasSuffix(l, "sh"):
		if strings.HasSuffix(l, "s") && !strings.HasSuffix(l, "ss") {
			return u
		}
		return u + "es"
	case strings.HasSuffix(l, "y") && len(l) > 1 && !strings.ContainsRune("aeiou", rune(l[len(l)-2])):
		return u[:len(u)-1] + "ies"
	}
	return u + "s"
}

// Quantity says n of an item: "12 bags", "1 carton", "4".
func (it Item) Quantity(n int64) string {
	if u := it.UnitName(n); u != "" {
		return strconv.FormatInt(n, 10) + " " + u
	}
	return strconv.FormatInt(n, 10)
}

// Items maps a catalogue table's rows to items by the mapping, by name.
// Rows without a name are left out.
func Items(rows []map[string]any, m storefront.Columns) []Item {
	var out []Item
	for _, r := range rows {
		it := Item{ID: fmt.Sprint(r["id"]), Name: str(r[m.Name]), Available: true}
		if it.Name == "" || it.ID == "" || it.ID == "<nil>" {
			continue
		}
		if m.Available != "" {
			if b, ok := r[m.Available].(bool); ok {
				it.Available = b
			}
		}
		it.Stock = whole(r, m.Stock)
		it.Reorder = whole(r, m.ReorderLevel)
		it.Target = whole(r, m.TargetLevel)
		if m.Unit != "" {
			it.Unit = str(r[m.Unit])
		}
		if m.Supplier != "" {
			it.Supplier = str(r[m.Supplier])
		}
		if m.Category != "" {
			it.Category = str(r[m.Category])
		}
		if m.Description != "" {
			it.Description = str(r[m.Description])
		}
		if m.VariantOf != "" {
			it.VariantOf = str(r[m.VariantOf])
		}
		if m.Variant != "" {
			it.Variant = variants.ParseVariant(str(r[m.Variant]))
		}
		if m.Choices != "" {
			st := variants.ParseSetup(str(r[m.Choices]))
			it.Choices, it.OwnPrice = st.Choices, st.OwnPrice
		}
		it.PriceKobo = money(r, m.Price, m.PriceUnit)
		it.CostKobo = money(r, m.CostPrice, m.PriceUnit)
		out = append(out, it)
	}
	sort.SliceStable(out, func(a, b int) bool { return strings.ToLower(out[a].Name) < strings.ToLower(out[b].Name) })
	return out
}

// SeenRows is each row's count as a refresh read it, for Outside: rows
// holding a whole count only.
func SeenRows(rows []map[string]any, m storefront.Columns) []Seen {
	if m.Stock == "" {
		return nil
	}
	var out []Seen
	for _, it := range Items(rows, m) {
		if it.Stock != nil {
			out = append(out, Seen{ItemID: it.ID, Item: it.Name, Count: *it.Stock})
		}
	}
	return out
}

// Low is the items at or below their reorder level, the lowest first
// against it.
func Low(items []Item) []Item {
	var out []Item
	for _, it := range items {
		if it.Low() {
			out = append(out, it)
		}
	}
	sort.SliceStable(out, func(a, b int) bool {
		return *out[a].Stock-*out[a].Reorder < *out[b].Stock-*out[b].Reorder
	})
	return out
}

func whole(r map[string]any, col string) *int64 {
	if col == "" {
		return nil
	}
	f, ok := num(r[col])
	if !ok || f != math.Trunc(f) || math.Abs(f) > 1e12 {
		return nil
	}
	n := int64(f)
	return &n
}

func money(r map[string]any, col, unit string) *int64 {
	if col == "" {
		return nil
	}
	f, ok := num(r[col])
	if !ok || f < 0 || f > 1e12 {
		return nil
	}
	if unit != "kobo" {
		f *= 100
	}
	k := int64(math.Round(f))
	return &k
}

func str(v any) string {
	switch x := v.(type) {
	case string:
		return strings.TrimSpace(x)
	case nil:
		return ""
	}
	return strings.TrimSpace(fmt.Sprint(v))
}

func num(v any) (float64, bool) {
	switch x := v.(type) {
	case float64:
		return x, true
	case int64:
		return float64(x), true
	case int:
		return float64(x), true
	case json.Number:
		f, err := x.Float64()
		return f, err == nil
	case string:
		f, err := strconv.ParseFloat(strings.TrimSpace(x), 64)
		return f, err == nil
	}
	return 0, false
}
