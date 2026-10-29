package stock

import (
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"

	"github.com/israel-duff/taskiem/engine/storefront"
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
}

// Low reports whether the item is at or below its reorder level.
func (it Item) Low() bool {
	return it.Stock != nil && it.Reorder != nil && *it.Stock <= *it.Reorder
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
