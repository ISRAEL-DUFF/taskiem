package stock

import (
	"context"
	"sort"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/productstock"
	"github.com/israel-duff/taskiem/engine/storefront"
)

// Show is which stock items the Stock page lists (what a business buys
// §4.5): all, those sold (a product is counted by them), or those not
// sold (materials).
type Show string

// The Stock page's filter.
const (
	ShowAll     Show = "all"
	ShowSold    Show = "sold"
	ShowNotSold Show = "not_sold"
)

// SoldAs is a product a stock item counts: one sale of it takes Uses of
// the stock item.
type SoldAs struct {
	ProductID string            `json:"product_id"`
	Name      string            `json:"name"`
	Uses      int64             `json:"uses"`
	PriceKobo *int64            `json:"price_kobo"`
	Variant   map[string]string `json:"variant"`
	Available bool              `json:"available"`
}

// StockItemRow is a row of the Stock page: a stock item, with the
// products it is sold as (none: a material, Not sold).
type StockItemRow struct {
	ID          uuid.UUID  `json:"id"`
	Name        string     `json:"name"`
	Unit        string     `json:"unit"`
	Category    string     `json:"category"`
	Count       int64      `json:"count"`
	CostKobo    *int64     `json:"cost_kobo"`
	LowLevel    *int64     `json:"low_level"`
	TargetLevel *int64     `json:"target_level"`
	SupplierID  *uuid.UUID `json:"supplier_id"`
	Supplier    string     `json:"supplier"`
	// GroupOf is the product a size's or colour's stock item belongs to;
	// GroupName that product's name.
	GroupOf   string `json:"group_of"`
	GroupName string `json:"group_name"`
	Low       bool   `json:"low"`
	// Suggest is how many to reorder of a low one (as Item.Suggest), nil
	// when it is not low.
	Suggest *int64 `json:"suggest"`
	// ValueKobo is its count at its cost, 0 without a cost or below zero.
	ValueKobo int64    `json:"value_kobo"`
	Retired   bool     `json:"retired"`
	SoldAs    []SoldAs `json:"sold_as"`
}

// StockItemRows lists the stock items as the Stock page does: each with
// the catalogue's products it counts (rows by the mapping, links by
// product id), filtered by show, retired ones only when retired is true;
// by group (its product's name) then name.
func StockItemRows(rows []map[string]any, m storefront.Columns, items []StockItem, links map[string]productstock.Counted, show Show, retired bool) []StockItemRow {
	products := map[string]Item{}
	for _, it := range Items(rows, m, nil, nil) {
		products[it.ID] = it
	}
	sold := map[uuid.UUID][]SoldAs{}
	for pid, c := range links {
		p, ok := products[pid]
		if !ok {
			continue
		}
		sold[c.StockItemID] = append(sold[c.StockItemID], SoldAs{ProductID: pid, Name: p.Name, Uses: c.Uses,
			PriceKobo: p.PriceKobo, Variant: p.Variant, Available: p.Available})
	}
	out := []StockItemRow{}
	for _, x := range items {
		if x.RetiredAt != nil && !retired {
			continue
		}
		s := sold[x.ID]
		if (show == ShowSold && len(s) == 0) || (show == ShowNotSold && len(s) > 0) {
			continue
		}
		if s == nil {
			s = []SoldAs{}
		}
		sort.Slice(s, func(a, b int) bool {
			if la, lb := strings.ToLower(s[a].Name), strings.ToLower(s[b].Name); la != lb {
				return la < lb
			}
			return s[a].ProductID < s[b].ProductID
		})
		r := StockItemRow{ID: x.ID, Name: x.Name, Unit: x.Unit, Category: x.Category, Count: x.Count, CostKobo: x.CostKobo,
			LowLevel: x.Low, TargetLevel: x.Target, SupplierID: x.SupplierID, Supplier: x.Supplier, GroupOf: x.GroupOf,
			Retired: x.RetiredAt != nil, SoldAs: s}
		if x.GroupOf != "" {
			r.GroupName = products[x.GroupOf].Name
		}
		if it := FromStockItem(x); !r.Retired && it.Low() {
			r.Low = true
			n := it.Suggest()
			r.Suggest = &n
		}
		if x.CostKobo != nil && x.Count > 0 {
			r.ValueKobo = x.Count * *x.CostKobo
		}
		out = append(out, r)
	}
	sort.SliceStable(out, func(a, b int) bool {
		ga, gb := strings.ToLower(out[a].GroupName), strings.ToLower(out[b].GroupName)
		if ga != gb {
			return ga < gb
		}
		return strings.ToLower(out[a].Name) < strings.ToLower(out[b].Name)
	})
	return out
}

// StockItemRowsTx is StockItemRows, reading the business's stock items
// and links in tx.
func StockItemRowsTx(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, rows []map[string]any, m storefront.Columns, show Show, retired bool) ([]StockItemRow, error) {
	items, err := StockItemsTx(ctx, tx, tenant, retired)
	if err != nil {
		return nil, err
	}
	links, err := productstock.CountsTx(ctx, tx, tenant, nil)
	if err != nil {
		return nil, err
	}
	return StockItemRows(rows, m, items, links, show, retired), nil
}
