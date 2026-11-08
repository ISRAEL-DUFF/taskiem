package api

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/links"
	"github.com/israel-duff/taskiem/engine/stock"
	"github.com/israel-duff/taskiem/engine/variants"
)

// Sizes and colours in trade (docs/spec/chat-categories-and-variants.md,
// phase 4). A supplier lists a product once with its sizes; the buyer
// orders by its sizes, each a line of the supplier's own row. Before the
// order is drafted each such line is given the buyer's own size: the
// buyer's product is the one the line names, else the one linked to the
// supplier's product; its size is the one of the same values (42, Black),
// and a size it lacks is added to it (at 0, the delivery brings the
// stock in), so the goods come into the size they are. The size pairs are
// then linked as any item is, so the order reaches the supplier as its
// own sizes.

// errSizesDiffer: the buyer's product varies by other choices than the
// supplier's (Size alone against Size and Colour).
type errSizesDiffer struct{ mine string }

func (e errSizesDiffer) Error() string {
	return fmt.Sprintf("your %s comes in other choices than theirs: give it the same ones (in its sizes and colours), or choose another of your items", e.mine)
}

// errSizesFirst: the buyer's product keeps stock of its own and no sizes
// yet: its owner shares that stock out among its sizes first.
type errSizesFirst struct{ mine string }

func (e errSizesFirst) Error() string {
	return fmt.Sprintf("set up the sizes and colours of your %s first (in its form), so the stock you have goes into them; then order", e.mine)
}

// resolveSizeLines gives each line of a supplier's size its buyer's own
// size, making those the buyer's product lacks. Lines of other items are
// left as they are. audit records the sizes made.
func (s *Server) resolveSizeLines(ctx context.Context, p *Principal, audit stockAudit, link uuid.UUID, in []orderLineIn, key string) ([]orderLineIn, error) {
	var listed []links.PriceItem
	var mine map[string]string
	err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{p.TenantID}, func(tx pgx.Tx) error {
		got, err := links.SupplierPricesTx(ctx, tx, p.TenantID, link)
		if err != nil || len(got) == 0 {
			return err
		}
		listed = got[0].Items
		ls, err := links.ItemLinksTx(ctx, tx, p.TenantID, link)
		mine = map[string]string{}
		for _, l := range ls {
			mine[l.SellerItemID] = l.ItemID
		}
		return err
	})
	if err != nil || len(listed) == 0 {
		return in, err
	}
	flat := links.Flat(listed)
	products := map[string]links.PriceItem{}
	for _, it := range listed {
		products[it.ItemID] = it
	}
	// The lines by the supplier's product, in their order.
	var order []string
	groups := map[string][]int{}
	for i, l := range in {
		it, ok := flat[l.SellerItemID]
		if !ok || it.VariantOf == "" {
			continue
		}
		if groups[it.VariantOf] == nil {
			order = append(order, it.VariantOf)
		}
		groups[it.VariantOf] = append(groups[it.VariantOf], i)
	}
	if len(order) == 0 {
		return in, nil
	}
	out := append([]orderLineIn(nil), in...)
	for _, product := range order {
		_, items, err := s.stockItems(ctx, p.TenantID)
		if err != nil {
			return nil, err
		}
		own := map[string]stock.Item{}
		for _, it := range items {
			own[it.ID] = it
		}
		// The buyer's product: named on a line, else the linked one. A
		// line naming one of the buyer's own sizes keeps it.
		bp := ""
		for _, i := range groups[product] {
			if x, ok := own[out[i].ItemID]; ok && x.VariantOf == "" {
				bp = x.ID
			}
		}
		if bp == "" {
			bp = mine[product]
		}
		mineP, ok := own[bp]
		if !ok || mineP.VariantOf != "" {
			return nil, errUnlinkedItem{Item: products[product]}
		}
		// The sizes the lines want, and those the buyer's product has.
		has := map[string]stock.Item{}
		for _, v := range variantsOf(items, bp) {
			if v.Variant != nil {
				has[variants.Key(v.Variant)] = v
			}
		}
		var missing []map[string]string
		seen := map[string]bool{}
		for _, i := range groups[product] {
			if x, ok := own[out[i].ItemID]; ok && x.VariantOf == bp {
				continue
			}
			v := flat[out[i].SellerItemID].Variant
			k := variants.Key(v)
			if x, ok := has[k]; ok && x.Available || seen[k] {
				continue
			}
			seen[k] = true
			missing = append(missing, v)
		}
		if len(missing) > 0 {
			choices, cells, err := sizesWith(mineP, items, products[product].Choices, missing)
			if err != nil {
				return nil, err
			}
			if _, _, err := s.applySizes(ctx, p, audit, bp, choices, cells, key+"-sizes-"+product); err != nil {
				return nil, err
			}
			if _, items, err = s.stockItems(ctx, p.TenantID); err != nil {
				return nil, err
			}
			has = map[string]stock.Item{}
			for _, v := range variantsOf(items, bp) {
				if v.Variant != nil && v.Available {
					has[variants.Key(v.Variant)] = v
				}
			}
		}
		for _, i := range groups[product] {
			if x, ok := own[out[i].ItemID]; ok && x.VariantOf == bp {
				continue
			}
			x, ok := has[variants.Key(flat[out[i].SellerItemID].Variant)]
			if !ok {
				return nil, errUnlinkedItem{Item: flat[out[i].SellerItemID]}
			}
			out[i].ItemID = x.ID
		}
		// The products linked too, so the next order finds the buyer's.
		if mine[product] != bp {
			if err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{p.TenantID}, func(tx pgx.Tx) error {
				return links.SetItemLinkTx(ctx, tx, p.TenantID, link, bp, product, products[product].Name, p.Actor())
			}); err != nil {
				return nil, err
			}
		}
	}
	return out, nil
}

// sizesWith is the buyer's product's choices and grid with the sizes an
// order brings added: its sizes on sale as they are (counts left), each
// new one at 0. A product with no sizes yet takes the supplier's choices;
// one with other choices than the supplier's is refused.
func sizesWith(product stock.Item, items []stock.Item, theirs []variants.Choice, add []map[string]string) ([]variants.Choice, []variants.Cell, error) {
	var choices []variants.Choice
	if !product.HasSizes() && product.Stock != nil && *product.Stock > 0 {
		return nil, nil, errSizesFirst{mine: product.Name}
	}
	if product.HasSizes() {
		if len(product.Choices) != len(theirs) {
			return nil, nil, errSizesDiffer{mine: product.Name}
		}
		for _, c := range product.Choices {
			found := false
			for _, t := range theirs {
				if strings.EqualFold(c.Name, t.Name) {
					found = true
				}
			}
			if !found {
				return nil, nil, errSizesDiffer{mine: product.Name}
			}
			choices = append(choices, variants.Choice{Name: c.Name, Values: append([]string(nil), c.Values...)})
		}
	} else {
		for _, t := range theirs {
			choices = append(choices, variants.Choice{Name: t.Name})
		}
	}
	// Each value the new sizes have, in the buyer's choice of that name.
	cells := []variants.Cell{}
	for _, v := range add {
		cell := variants.Cell{Variant: map[string]string{}, Count: new(int64)}
		for ci := range choices {
			x := ""
			for k, val := range v {
				if strings.EqualFold(k, choices[ci].Name) {
					x = val
				}
			}
			if x == "" {
				return nil, nil, errSizesDiffer{mine: product.Name}
			}
			found := false
			for _, have := range choices[ci].Values {
				if strings.EqualFold(have, x) {
					x, found = have, true
				}
			}
			if !found {
				choices[ci].Values = append(choices[ci].Values, x)
			}
			cell.Variant[choices[ci].Name] = x
		}
		cells = append(cells, cell)
	}
	for _, v := range variantsOf(items, product.ID) {
		if v.Available && v.Variant != nil {
			cells = append(cells, variants.Cell{ID: v.ID, Variant: v.Variant})
		}
	}
	return choices, cells, nil
}
