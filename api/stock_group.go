package api

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/links"
	"github.com/israel-duff/taskiem/engine/stock"
	"github.com/israel-duff/taskiem/engine/variants"
)

// Grouping existing items (docs/spec/chat-categories-and-variants.md,
// Part B, phase 5): a shop that already sells "Air Max 90 – 42 Black" and
// its look-alikes as separate products makes them one product with sizes
// and colours. Each item stays its own row, so its stock, history, sales,
// prices and links stay with it; it only gains its product and its values,
// and its name is written out in full ("Air Max 90 – 42, Black"). The
// product is a new row, keeping no stock of its own.

type groupIn struct {
	ItemID  string            `json:"item_id"`
	Variant map[string]string `json:"variant"`
}

// groupStockItems is POST /v1/stock/group, with an Idempotency-Key:
// {name, items: [{item_id, variant}]}. It answers the product and its
// sizes and colours.
func (s *Server) groupStockItems(w http.ResponseWriter, r *http.Request) {
	key, err := requiredKey(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	var in struct {
		Name string `json:"name"`
		// Choices is the choices' order ("Size", then "Colour"); absent,
		// as the first item's values name them, sorted.
		Choices []string  `json:"choices"`
		Items   []groupIn `json:"items"`
	}
	if err := decodeBody(r, &in); err != nil {
		s.fail(w, r, err)
		return
	}
	audit := func(tx pgx.Tx, action, target string, detail map[string]any) error {
		return auditTx(r, tx, action, target, detail)
	}
	prod, vs, err := s.groupSizes(r.Context(), principalFrom(r.Context()), audit, in.Name, in.Choices, in.Items, key)
	if err != nil {
		s.stockFail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"item": prod, "variants": vs})
}

// groupPlan is what a grouping asks, checked: the product's choices, each
// item's values fitted to them, and the price most of them have (the
// product's; the others keep theirs as their own).
type groupPlan struct {
	name    string
	choices []variants.Choice
	items   []stock.Item
	values  []map[string]string
	price   *int64
}

// planGroup checks a grouping before anything is written. resume is the
// product a grouping that stopped part way made (groupResume), or "": its
// name is not a clash, and items already pointing at it are taken.
func planGroup(name string, order []string, in []groupIn, items []stock.Item, resume string) (groupPlan, error) {
	g := groupPlan{name: strings.Join(strings.Fields(name), " ")}
	switch {
	case g.name == "":
		return g, fmt.Errorf("%w: give the product a name", errBadRequest)
	case len([]rune(g.name)) > 120:
		return g, fmt.Errorf("%w: a name is at most 120 characters", errBadRequest)
	case len(in) < 2:
		return g, fmt.Errorf("%w: choose at least two of your items to make one product", errBadRequest)
	case len(in) > variants.MaxVariants:
		return g, fmt.Errorf("%w: one product has at most %d sizes and colours", errBadRequest, variants.MaxVariants)
	}
	byID := map[string]stock.Item{}
	for _, it := range items {
		byID[it.ID] = it
		if it.VariantOf == "" && it.ID != resume && strings.EqualFold(it.Name, g.name) {
			return g, fmt.Errorf("%w: %s is already the name of one of your items: give the product another", errBadRequest, it.Name)
		}
	}
	// The choices, in the order the first item names them; their values
	// in the order the items bring them.
	var names []string
	vals := map[string][]string{}
	seen := map[string]bool{}
	for i, x := range in {
		it, ok := byID[x.ItemID]
		switch {
		case !ok || (resume != "" && it.ID == resume):
			return g, fmt.Errorf("%w: %q is not one of your items", errBadRequest, x.ItemID)
		case it.VariantOf != "" && it.VariantOf != resume:
			return g, fmt.Errorf("%w: %s is already a size or colour of another product", errBadRequest, it.Name)
		case it.HasSizes():
			return g, fmt.Errorf("%w: %s is already sold in sizes and colours", errBadRequest, it.Name)
		case seen[it.ID]:
			return g, fmt.Errorf("%w: %s is chosen twice", errBadRequest, it.Name)
		}
		seen[it.ID] = true
		v := map[string]string{}
		for k, val := range x.Variant {
			k, val = strings.Join(strings.Fields(k), " "), strings.Join(strings.Fields(val), " ")
			if k == "" || val == "" {
				return g, fmt.Errorf("%w: say each choice of %s (its size, its colour…)", errBadRequest, it.Name)
			}
			v[k] = val
		}
		if len(v) == 0 {
			return g, fmt.Errorf("%w: say the size or colour of %s", errBadRequest, it.Name)
		}
		if i == 0 {
			for k := range v {
				names = append(names, k)
			}
			slices.Sort(names) // a map has no order of its own
			// The order asked, where it names the same choices.
			if len(order) == len(names) {
				var asked []string
				for _, o := range order {
					o = strings.Join(strings.Fields(o), " ")
					if j := slices.IndexFunc(names, func(n string) bool { return strings.EqualFold(n, o) }); j >= 0 {
						asked = append(asked, names[j])
					}
				}
				if len(asked) == len(names) {
					names = asked
				}
			}
		}
		if len(v) != len(names) {
			return g, fmt.Errorf("%w: every item has the same choices (%s)", errBadRequest, strings.Join(names, ", "))
		}
		fitted := map[string]string{}
		for _, n := range names {
			got := ""
			for k, val := range v {
				if strings.EqualFold(k, n) {
					got = val
				}
			}
			if got == "" {
				return g, fmt.Errorf("%w: every item has the same choices (%s)", errBadRequest, strings.Join(names, ", "))
			}
			if !slices.ContainsFunc(vals[n], func(s string) bool { return strings.EqualFold(s, got) }) {
				vals[n] = append(vals[n], got)
			}
			fitted[n] = got
		}
		g.items = append(g.items, it)
		g.values = append(g.values, fitted)
	}
	cs := make([]variants.Choice, len(names))
	for i, n := range names {
		cs[i] = variants.Choice{Name: n, Values: vals[n]}
	}
	var err error
	if g.choices, err = variants.Check(cs); err != nil {
		return g, fmt.Errorf("%w: %s", errBadRequest, strings.TrimPrefix(err.Error(), "variants: invalid: "))
	}
	keys := map[string]string{}
	for i, v := range g.values {
		fv, ok := variants.Fit(v, g.choices)
		if !ok {
			return g, fmt.Errorf("%w: %s does not fit the choices", errBadRequest, g.items[i].Name)
		}
		k := variants.Key(fv)
		if other, twice := keys[k]; twice {
			return g, fmt.Errorf("%w: %s and %s would be the same size and colour (%s)", errBadRequest, other, g.items[i].Name, variants.Label(fv, g.choices))
		}
		keys[k] = g.items[i].Name
		g.values[i] = fv
	}
	// The product's price: the one most of them have.
	count := map[int64]int{}
	for _, it := range g.items {
		if it.PriceKobo != nil {
			count[*it.PriceKobo]++
		}
	}
	best := -1
	for p, n := range count {
		if n > best || (n == best && p < *g.price) {
			pp := p
			g.price, best = &pp, n
		}
	}
	return g, nil
}

// groupSizes makes the chosen items one product's sizes and colours: the
// product is added (named, with the first item's unit, category and
// supplier, and the price most of them have); each item is given its
// product and its values; then the product's grid is saved as any is
// (applySizes keeps each row, names it in full, and records the prices
// that differ as their own). Price lists that held the items one by one
// hold the product instead.
func (s *Server) groupSizes(ctx context.Context, p *Principal, audit stockAudit, name string, order []string, in []groupIn, key string) (stock.Item, []stock.Item, error) {
	// One grouping at a time in a business: two at once over the same
	// items would each make a product and take the items from the other.
	unlock, err := s.lockSizes(ctx, p.TenantID, "grouping")
	if err != nil {
		return stock.Item{}, nil, err
	}
	defer unlock()
	_, items, err := s.stockItems(ctx, p.TenantID)
	if err != nil {
		return stock.Item{}, nil, err
	}
	// A retry after the grouping was done: the items already point at it.
	if prod, vs, ok := groupedBefore(items, in); ok {
		return prod, vs, nil
	}
	resume, err := s.groupResume(ctx, p.TenantID, items, name, in, key)
	if err != nil {
		return stock.Item{}, nil, err
	}
	g, err := planGroup(name, order, in, items, resume)
	if err != nil {
		return stock.Item{}, nil, err
	}
	first := g.items[0]
	var prod stock.Item
	for _, it := range items {
		if it.ID == resume {
			prod = it
		}
	}
	if prod.ID == "" {
		if prod, _, err = s.stockAdd(ctx, p.TenantID, p.Actor(), key+"-product", stockNew{Name: g.name, Unit: first.Unit, PriceKobo: g.price,
			Category: first.Category, Supplier: first.Supplier, CostKobo: first.CostKobo}, audit); err != nil {
			return stock.Item{}, nil, err
		}
	}
	sh, _, err := s.stockItems(ctx, p.TenantID)
	if err != nil {
		return stock.Item{}, nil, err
	}
	if sh, err = s.stockVariantColumns(ctx, p.TenantID, p.Actor(), audit, sh); err != nil {
		return stock.Item{}, nil, err
	}
	m := sh.cols()
	// The product shows as its items did: on sale when one of them was,
	// with the first one's photo (each item keeps its own).
	set := map[string]any{}
	if m.Available != "" {
		set[m.Available] = slices.ContainsFunc(g.items, func(it stock.Item) bool { return it.Available })
	}
	if m.Photo != "" && prod.PhotoID == "" {
		if at := slices.IndexFunc(g.items, func(it stock.Item) bool { return it.PhotoID != "" }); at >= 0 {
			set[m.Photo] = g.items[at].PhotoID
		}
	}
	if len(set) > 0 {
		if err := s.stockSetCells(ctx, p.TenantID, sh, prod.ID, "api-"+key+"-product-shown", set); err != nil {
			return stock.Item{}, nil, err
		}
	}
	cells := make([]variants.Cell, len(g.items))
	for i, it := range g.items {
		if err := s.stockSetCells(ctx, p.TenantID, sh, it.ID, "api-"+key+"-into-"+it.ID,
			map[string]any{m.VariantOf: prod.ID, m.Variant: variants.JSON(g.values[i])}); err != nil {
			return stock.Item{}, nil, err
		}
		cells[i] = variants.Cell{ID: it.ID, Variant: g.values[i]}
		if it.PriceKobo != nil && (g.price == nil || *it.PriceKobo != *g.price) {
			cells[i].PriceKobo = it.PriceKobo
		}
	}
	pr, vs, err := s.applySizes(quietSizes(ctx), p, audit, prod.ID, g.choices, cells, key+"-sizes")
	if err != nil {
		return stock.Item{}, nil, err
	}
	ids := make([]string, len(g.items))
	for i, it := range g.items {
		ids[i] = it.ID
	}
	if err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{p.TenantID}, func(tx pgx.Tx) error {
		return audit(tx, "stock.items_grouped", pr.Name, map[string]any{"item_id": pr.ID, "items": ids})
	}); err != nil {
		return stock.Item{}, nil, err
	}
	s.diaryChange(ctx, p, fmt.Sprintf("Made one product of %d items: %s", len(ids), pr.Name))
	s.foldListsInto(ctx, p.TenantID, pr.ID, ids)
	return pr, vs, nil
}

// groupResume is the product a grouping that stopped part way made, so a
// retry finishes it rather than refuse its own product: the one the
// chosen items already point at, still without its sizes and named as
// asked; or, before any item pointed at it, the one this key was adding.
// "" when there is none.
func (s *Server) groupResume(ctx context.Context, tenant uuid.UUID, items []stock.Item, name string, in []groupIn, key string) (string, error) {
	byID := map[string]stock.Item{}
	for _, it := range items {
		byID[it.ID] = it
	}
	same := func(it stock.Item) bool {
		return it.ID != "" && it.VariantOf == "" && !it.HasSizes() && matchKey(it.Name) == matchKey(name)
	}
	for _, x := range in {
		if it, ok := byID[x.ItemID]; ok && it.VariantOf != "" {
			if prod := byID[it.VariantOf]; same(prod) {
				return prod.ID, nil
			}
			return "", nil
		}
	}
	for _, it := range items {
		if same(it) {
			mine, err := s.stockAdding(ctx, tenant, key+"-product", name)
			if err != nil || !mine {
				return "", err
			}
			return it.ID, nil
		}
	}
	return "", nil
}

// groupedBefore: every item already points at one product, by this
// grouping before (a retry); that product and its sizes.
func groupedBefore(items []stock.Item, in []groupIn) (stock.Item, []stock.Item, bool) {
	if len(in) == 0 {
		return stock.Item{}, nil, false
	}
	byID := map[string]stock.Item{}
	for _, it := range items {
		byID[it.ID] = it
	}
	product := ""
	for _, x := range in {
		it, ok := byID[x.ItemID]
		if !ok || it.VariantOf == "" || (product != "" && it.VariantOf != product) {
			return stock.Item{}, nil, false
		}
		product = it.VariantOf
	}
	prod, ok := byID[product]
	if !ok || !prod.HasSizes() {
		return stock.Item{}, nil, false
	}
	return prod, variantsOf(items, product), true
}

// foldListsInto puts the product on the price lists that held some of its
// items one by one, in place of them: at the price most of those rows
// had, the lowest of their minimums, available if any was; a size listed
// at another price keeps it as its own, and one the list did not hold, or
// held off sale, is off this list. Buyers are told.
func (s *Server) foldListsInto(ctx context.Context, tenant uuid.UUID, product string, ids []string) {
	_, items, err := s.stockItems(ctx, tenant)
	if err != nil {
		s.Logger.Warn("stock: a grouped product on the price lists", "tenant", tenant, "err", err)
		return
	}
	var prod stock.Item
	for _, it := range items {
		if it.ID == product {
			prod = it
		}
	}
	grouped := map[string]bool{}
	for _, id := range ids {
		grouped[id] = true
	}
	var due []uuid.UUID
	if err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		lists, err := links.ListsTx(ctx, tx, tenant)
		if err != nil {
			return err
		}
		for _, l := range lists {
			var held []links.PriceItem
			at := -1
			for i, it := range l.Items {
				if grouped[it.ItemID] {
					if at < 0 {
						at = i
					}
					held = append(held, it)
				}
			}
			if len(held) == 0 {
				continue
			}
			pi := foldedItem(prod, items, held)
			out := make([]links.PriceItem, 0, len(l.Items)-len(held)+1)
			for i, it := range l.Items {
				if i == at {
					out = append(out, pi)
				}
				if !grouped[it.ItemID] && it.ItemID != product {
					out = append(out, it)
				}
			}
			ls, err := links.SetListItemsTx(ctx, tx, tenant, l.ID, out)
			if err != nil {
				return err
			}
			due = append(due, ls...)
		}
		return nil
	}); err != nil {
		s.Logger.Warn("stock: a grouped product on the price lists", "tenant", tenant, "err", err)
		return
	}
	s.mirrorPrices(ctx, tenant, due)
}

// foldedItem is the product's row on a list from the rows its sizes had
// there.
func foldedItem(prod stock.Item, items []stock.Item, held []links.PriceItem) links.PriceItem {
	count := map[int64]int{}
	minQ, avail := held[0].MinQuantity, false
	var breaks []links.Break
	for _, h := range held {
		count[h.PriceKobo]++
		minQ = min(minQ, h.MinQuantity)
		avail = avail || h.Available
	}
	price, best := held[0].PriceKobo, 0
	for p, n := range count {
		if n > best || (n == best && p < price) {
			price, best = p, n
		}
	}
	for _, h := range held {
		if h.PriceKobo == price && len(h.Breaks) > 0 {
			breaks = h.Breaks
			break
		}
	}
	pi := links.PriceItem{ItemID: prod.ID, Name: prod.Name, Unit: prod.Unit, Category: prod.Category, PriceKobo: price,
		MinQuantity: max(1, minQ), Available: avail, Breaks: breaks}
	pi.Choices, pi.Sizes = listSizesOf(prod, items)
	was := map[string]links.PriceItem{}
	for _, h := range held {
		was[h.ItemID] = h
	}
	for i, z := range pi.Sizes {
		h, ok := was[z.ItemID]
		switch {
		case !ok || !h.Available:
			pi.Sizes[i].Available = false
		case h.PriceKobo != price:
			pi.Sizes[i].PriceKobo = h.PriceKobo
		}
	}
	return pi
}
