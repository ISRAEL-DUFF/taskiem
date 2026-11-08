package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/datatables"
	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/links"
	"github.com/israel-duff/taskiem/engine/stock"
	"github.com/israel-duff/taskiem/engine/storefront"
	"github.com/israel-duff/taskiem/engine/variants"
)

// Sizes and colours (docs/spec/chat-categories-and-variants.md, Part B):
// a product's choices make a grid, and each combination sold is a
// catalogue row of its own, pointing at the product's (variant_of), so
// stock, sales and orders work on it as on any item. The product keeps no
// stock of its own.

// variantColumns are the columns a catalogue gets the first time a
// product has sizes and colours.
var variantColumns = []struct {
	field func(*storefront.Columns) *string
	col   datatables.Column
}{
	{func(m *storefront.Columns) *string { return &m.VariantOf }, datatables.Column{Name: "variant_of", Title: "Size or colour of", Type: "text"}},
	{func(m *storefront.Columns) *string { return &m.Variant }, datatables.Column{Name: "variant", Title: "Size and colour", Type: "text"}},
	{func(m *storefront.Columns) *string { return &m.Choices }, datatables.Column{Name: "choices", Title: "Sizes and colours", Type: "text"}},
	// A size or colour taken off sale is kept, hidden: the catalogue needs
	// a column that says so (an empty cell is on sale).
	{func(m *storefront.Columns) *string { return &m.Available }, datatables.Column{Name: "available", Title: "Available", Type: "boolean"}},
}

// errNoVariantColumns: the catalogue cannot take the columns sizes and
// colours need (a table Taskiem may not change, or the plan's limit).
var errNoVariantColumns = errors.New("stock: no room for sizes and colours")

// stockVariantColumns makes the catalogue hold sizes and colours, once:
// columns of those names are mapped if the table has them, else added.
func (s *Server) stockVariantColumns(ctx context.Context, tenant uuid.UUID, actor string, audit stockAudit, sh stockShop) (stockShop, error) {
	m := sh.cols()
	if m.VariantOf != "" && m.Variant != "" && m.Choices != "" && m.Available != "" {
		return sh, nil
	}
	t := sh.table
	var missing []datatables.Column
	for _, vc := range variantColumns {
		f := vc.field(&m)
		if *f != "" {
			continue
		}
		for _, c := range t.Columns {
			if c.Name == vc.col.Name && c.Type == vc.col.Type {
				*f = c.Name
			}
		}
		if *f == "" {
			missing = append(missing, vc.col)
		}
	}
	if len(missing) > 0 {
		if datatables.Writable(t) != nil {
			return sh, errNoVariantColumns
		}
		lim, err := s.Store.LimitsFor(ctx, tenant)
		if err != nil {
			return sh, err
		}
		if lim.MaxDataTableColumns > 0 && len(t.Columns)+len(missing) > lim.MaxDataTableColumns {
			return sh, errNoVariantColumns
		}
		for _, c := range missing {
			if err := s.addOwnColumn(ctx, tenant, actor, audit, &t, c); err != nil {
				return sh, err
			}
			for _, vc := range variantColumns {
				if vc.col.Name == c.Name {
					*vc.field(&m) = c.Name
				}
			}
		}
	}
	if err := s.saveMapping(ctx, tenant, actor, sh, t, m); err != nil {
		return sh, err
	}
	return s.stockShopOf(ctx, tenant)
}

// saveMapping saves the catalogue's mapping, checked against its table.
func (s *Server) saveMapping(ctx context.Context, tenant uuid.UUID, actor string, sh stockShop, t datatables.Table, m storefront.Columns) error {
	return db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		if err := m.Check(tableColumns(t)); err != nil {
			return err
		}
		_, err := storefront.Save(ctx, tx, tenant, sh.sf.Slug, sh.sf.Draft, &t.ID, m, actor)
		return err
	})
}

// variantsOf is a product's variant rows, retired ones too.
func variantsOf(items []stock.Item, product string) []stock.Item {
	var out []stock.Item
	for _, it := range items {
		if it.VariantOf == product {
			out = append(out, it)
		}
	}
	return out
}

// setStockVariants is PUT /v1/stock/items/{id}/variants, with an
// Idempotency-Key: a product's choices and the grid of what it sells.
// Rows are made, renamed, brought back and retired as the grid says
// (engine/variants); a kept one's count is a count. A new one's count is
// stock moved from the product while the product still holds some (the
// first time it is set up), else goods received. What the product held
// beyond what its sizes and colours took is counted off it, so its own
// count ends at 0. One without its own price takes the product's, and
// follows it. No choices takes them all off sale. Everything asked is
// checked before anything is written, and one save of a product's sizes
// and colours runs at a time.
func (s *Server) setStockVariants(w http.ResponseWriter, r *http.Request) {
	key, err := requiredKey(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	var in struct {
		Choices []variants.Choice `json:"choices"`
		Cells   []variants.Cell   `json:"cells"`
	}
	if err := decodeBody(r, &in); err != nil {
		s.fail(w, r, err)
		return
	}
	choices, err := variants.Check(in.Choices)
	if err != nil {
		s.fail(w, r, fmt.Errorf("%w: %s", errBadRequest, strings.TrimPrefix(err.Error(), "variants: invalid: ")))
		return
	}
	if len(choices) == 0 && len(in.Cells) > 0 {
		s.fail(w, r, fmt.Errorf("%w: sizes and colours need at least one choice", errBadRequest))
		return
	}
	for _, c := range in.Cells {
		if c.Count != nil && (*c.Count < 0 || *c.Count > stock.MaxDelta) {
			s.fail(w, r, fmt.Errorf("%w: a count is from 0 to %d", errBadRequest, stock.MaxDelta))
			return
		}
		if c.PriceKobo != nil && (*c.PriceKobo < 0 || *c.PriceKobo > maxVariantPriceKobo) {
			s.fail(w, r, fmt.Errorf("%w: a price is from ₦0 to %s", errBadRequest, storefront.FormatNaira(maxVariantPriceKobo)))
			return
		}
	}
	p := principalFrom(r.Context())
	ctx := r.Context()
	id := chi.URLParam(r, "id")
	unlock, err := s.lockSizes(ctx, p.TenantID, id)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	defer unlock()
	audit := func(tx pgx.Tx, action, target string, detail map[string]any) error {
		return auditTx(r, tx, action, target, detail)
	}
	sh, items, err := s.stockItems(ctx, p.TenantID)
	if err != nil {
		s.stockFail(w, r, err)
		return
	}
	var product *stock.Item
	for i := range items {
		if items[i].ID == id {
			product = &items[i]
		}
	}
	switch {
	case product == nil:
		s.stockFail(w, r, errNoItem)
		return
	case product.VariantOf != "":
		s.fail(w, r, fmt.Errorf("%w: this is a size or colour of another product: open that product", errBadRequest))
		return
	}
	before := variantsOf(items, id)
	have := make([]variants.Existing, len(before))
	onSale := 0
	for i, v := range before {
		have[i] = variants.Existing{ID: v.ID, Variant: v.Variant, Available: v.Available}
		if v.Available {
			onSale++
		}
	}
	plan, err := variants.MakePlan(product.Name, choices, in.Cells, have)
	if err != nil {
		s.fail(w, r, fmt.Errorf("%w: %s", errBadRequest, strings.TrimPrefix(err.Error(), "variants: invalid: ")))
		return
	}
	// The catalogue holds at most MaxItems rows, sizes and colours too.
	if len(items)+len(plan.Create) > storefront.MaxItems {
		s.fail(w, r, fmt.Errorf("%w: the catalogue holds at most %d items, each size and colour counted", errConflict, storefront.MaxItems))
		return
	}
	if sh, err = s.stockVariantColumns(ctx, p.TenantID, p.Actor(), audit, sh); err != nil {
		s.stockFail(w, r, err)
		return
	}
	m := sh.cols()
	// The rows to make, checked by the table before any is written.
	price := func(c variants.Cell) any {
		if c.PriceKobo != nil {
			return priceCell(*c.PriceKobo, m)
		}
		if product.PriceKobo != nil {
			return priceCell(*product.PriceKobo, m)
		}
		return nil
	}
	rows := make([]any, 0, len(plan.Create))
	for _, c := range plan.Create {
		row := stockCells(m, product.Unit, product.CostKobo, product.Reorder, product.Target, product.Supplier)
		row[m.Name] = variants.Name(product.Name, c.Variant, choices)
		row[m.Stock] = 0
		row[m.VariantOf] = id
		row[m.Variant] = variants.JSON(c.Variant)
		row[m.Available] = true
		if v := price(c); v != nil && m.Price != "" {
			row[m.Price] = v
		}
		if m.Category != "" && product.Category != "" {
			row[m.Category] = product.Category
		}
		checked, err := datatables.CheckRow(sh.table.Columns, row, true, false)
		if err != nil {
			s.fail(w, r, fmt.Errorf("%w: %s", errBadRequest, strings.TrimPrefix(err.Error(), "invalid: ")))
			return
		}
		rows = append(rows, checked)
	}
	tr, err := s.tableRowsFor(ctx, p.TenantID, sh.table, storefrontPurpose)
	if err != nil {
		s.stockFail(w, r, err)
		return
	}
	// The product: its choices, and which have a price of their own.
	setup := variants.Setup{Choices: choices}
	for _, c := range in.Cells {
		if v, ok := variants.Fit(c.Variant, choices); ok && c.PriceKobo != nil {
			setup.OwnPrice = append(setup.OwnPrice, variants.Key(v))
		}
	}
	if err := s.stockSetCells(ctx, p.TenantID, sh, id, "api-"+key+"-product", map[string]any{m.Choices: setupCell(setup)}); err != nil {
		s.stockFail(w, r, err)
		return
	}
	// What the product holds is shared out to the new rows, as far as it
	// goes; the rest of their counts came in.
	share := int64(0)
	if len(choices) > 0 && product.Stock != nil && *product.Stock > 0 {
		share = *product.Stock
	}
	moved := int64(0)
	if len(plan.Create) > 0 {
		out, err := tr.call(ctx, s, "insert_rows", map[string]any{"table": sh.table.Name, "rows": rows}, "api-"+key+"-rows")
		if err != nil {
			s.stockFail(w, r, err)
			return
		}
		made := rowsOf(out)
		for i, c := range plan.Create {
			if i >= len(made) {
				break
			}
			mr, _ := made[i].(map[string]any)
			rid := cell(mr["id"])
			if rid == "" || c.Count == nil || *c.Count <= 0 {
				continue
			}
			name := variants.Name(product.Name, c.Variant, choices)
			from := min(*c.Count, share-moved)
			if from > 0 {
				if _, err := s.stockMove(ctx, p.TenantID, stock.Move{ItemID: rid, Item: name, Reason: stock.Moved, Delta: &from,
					Note: "From " + product.Name, Actor: p.Actor(), Key: "api-" + key + "-from-" + strconv.Itoa(i), CostKobo: product.CostKobo}, audit); err != nil {
					s.stockFail(w, r, err)
					return
				}
				moved += from
			}
			if rest := *c.Count - from; rest > 0 {
				if _, err := s.stockMove(ctx, p.TenantID, stock.Move{ItemID: rid, Item: name, Reason: stock.Received,
					Delta: &rest, Note: "Sizes and colours set up", Actor: p.Actor(), Key: "api-" + key + "-in-" + strconv.Itoa(i), CostKobo: product.CostKobo}, audit); err != nil {
					s.stockFail(w, r, err)
					return
				}
			}
		}
	}
	if moved > 0 {
		out := -moved
		if _, err := s.stockMove(ctx, p.TenantID, stock.Move{ItemID: id, Item: product.Name, Reason: stock.Moved, Delta: &out,
			Note: "Into its sizes and colours", Actor: p.Actor(), Key: "api-" + key + "-product-out", CostKobo: product.CostKobo}, audit); err != nil {
			s.stockFail(w, r, err)
			return
		}
	}
	if share > moved {
		// Not among its sizes and colours: what the owner did not count
		// into them is off the shelf, and the count says so.
		zero := int64(0)
		if _, err := s.stockMove(ctx, p.TenantID, stock.Move{ItemID: id, Item: product.Name, Reason: stock.Count, SetTo: &zero,
			Note: "Not among its sizes and colours", Actor: p.Actor(), Key: "api-" + key + "-product-stock", CostKobo: product.CostKobo}, audit); err != nil {
			s.stockFail(w, r, err)
			return
		}
	}
	// Rows kept: their names, values and prices as the grid has them; a
	// count where it is given and differs.
	was := map[string]stock.Item{}
	for _, v := range before {
		was[v.ID] = v
	}
	for _, k := range plan.Keep {
		set := map[string]any{m.Name: k.Name, m.Variant: variants.JSON(k.Variant)}
		if v := price(k.Cell); v != nil && m.Price != "" {
			set[m.Price] = v
		}
		if k.Revive {
			set[m.Available] = true
		}
		if err := s.stockSetCells(ctx, p.TenantID, sh, k.ID, "api-"+key+"-keep-"+k.ID, set); err != nil {
			s.stockFail(w, r, err)
			return
		}
		old := was[k.ID]
		if k.Cell.Count != nil && (old.Stock == nil || *old.Stock != *k.Cell.Count) {
			if _, err := s.stockMove(ctx, p.TenantID, stock.Move{ItemID: k.ID, Item: k.Name, Reason: stock.Count, SetTo: k.Cell.Count,
				Note: "Counted in its sizes and colours", Actor: p.Actor(), Key: "api-" + key + "-count-" + k.ID, CostKobo: old.CostKobo}, audit); err != nil {
				s.stockFail(w, r, err)
				return
			}
		}
	}
	// Rows the grid no longer has: off sale, kept for their history.
	for _, rid := range plan.Retire {
		if err := s.stockSetCells(ctx, p.TenantID, sh, rid, "api-"+key+"-retire-"+rid, map[string]any{m.Available: false}); err != nil {
			s.stockFail(w, r, err)
			return
		}
	}
	if err := s.tx(r, func(tx pgx.Tx) error {
		return auditTx(r, tx, "stock.variants", product.Name, map[string]any{"item_id": id, "choices": choices,
			"made": len(plan.Create), "kept": len(plan.Keep), "retired": len(plan.Retire), "moved": moved})
	}); err != nil {
		s.fail(w, r, err)
		return
	}
	if err := s.SyncStorefront(ctx, p.TenantID); err != nil {
		s.Logger.Warn("stock: refreshing the catalogue", "tenant", p.TenantID, "err", err)
	}
	// On the price lists that hold the product itself, it is off sale:
	// buyers order its sizes and colours, put on the lists as they are.
	if len(choices) > 0 {
		var due []uuid.UUID
		if err := s.tx(r, func(tx pgx.Tx) error {
			var err error
			due, err = links.SetItemOffTx(ctx, tx, p.TenantID, id)
			return err
		}); err != nil {
			s.Logger.Warn("stock: a product with sizes on the price lists", "tenant", p.TenantID, "err", err)
		}
		s.mirrorPrices(ctx, p.TenantID, due)
	}
	_, after, err := s.stockItems(ctx, p.TenantID)
	if err != nil {
		s.stockFail(w, r, err)
		return
	}
	var prod stock.Item
	for _, it := range after {
		if it.ID == id {
			prod = it
		}
	}
	vs := variantsOf(after, id)
	n := 0
	for _, v := range vs {
		if v.Available {
			n++
		}
	}
	if n == 0 {
		if onSale > 0 {
			s.diaryChange(ctx, p, product.Name+" no longer comes in sizes and colours")
		}
	} else {
		s.diaryChange(ctx, p, fmt.Sprintf("%s now comes in %d %s", product.Name, n, sizesWord(n)))
	}
	writeJSON(w, http.StatusOK, map[string]any{"item": prod, "variants": vs})
}

// maxVariantPriceKobo bounds a size's or colour's own price: ₦10m.
const maxVariantPriceKobo = int64(stock.MaxDelta) * 1000

// errSizesBusy: another save of the same product's sizes and colours is
// running.
var errSizesBusy = fmt.Errorf("%w: its sizes and colours are being saved by someone else: try again in a moment", errConflict)

// lockSizes holds the product's sizes and colours for this save alone, on
// a connection of its own (the save calls the table between its
// transactions), and gives the unlock; errSizesBusy when another holds
// them.
func (s *Server) lockSizes(ctx context.Context, tenant uuid.UUID, product string) (func(), error) {
	conn, err := s.Store.Pool.Acquire(ctx)
	if err != nil {
		return nil, err
	}
	lock := "taskiem sizes " + tenant.String() + " " + product
	var got bool
	if err := conn.QueryRow(ctx, `SELECT pg_try_advisory_lock(hashtextextended($1, 0))`, lock).Scan(&got); err != nil {
		conn.Release()
		return nil, err
	}
	if !got {
		conn.Release()
		return nil, errSizesBusy
	}
	return func() {
		// The request's own context may be over: unlock regardless.
		if _, err := conn.Exec(context.WithoutCancel(ctx), `SELECT pg_advisory_unlock(hashtextextended($1, 0))`, lock); err != nil {
			conn.Conn().Close(context.WithoutCancel(ctx)) //nolint:errcheck // closing the connection ends its locks
		}
		conn.Release()
	}, nil
}

// setupCell is a product's choices as its column holds them; nil when it
// has none.
func setupCell(st variants.Setup) any {
	if len(st.Choices) == 0 {
		return nil
	}
	return variants.JSON(st)
}

// sizesWord says how many sizes and colours: "1 size or colour", "30
// sizes and colours".
func sizesWord(n int) string {
	if n == 1 {
		return "size or colour"
	}
	return "sizes and colours"
}

// moveStockGrid is POST /v1/stock/movements/grid, with an
// Idempotency-Key: goods received, or a count, for several of a product's
// sizes and colours at once, told as one diary line ("Stock in: Air Max
// 90, 7 sizes and colours, +36") rather than a line each.
func (s *Server) moveStockGrid(w http.ResponseWriter, r *http.Request) {
	if !s.Stock.Ready() {
		writeErr(w, http.StatusServiceUnavailable, "stock is not available on this platform")
		return
	}
	key, err := requiredKey(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	var in struct {
		Reason string `json:"reason"`
		Note   string `json:"note"`
		Lines  []struct {
			ItemID string `json:"item_id"`
			Change *int64 `json:"change"`
			Count  *int64 `json:"count"`
		} `json:"lines"`
	}
	if err := decodeBody(r, &in); err != nil {
		s.fail(w, r, err)
		return
	}
	if in.Reason != stock.Received && in.Reason != stock.Count {
		writeErr(w, http.StatusBadRequest, "reason is received or count")
		return
	}
	if len(in.Lines) == 0 || len(in.Lines) > variants.MaxVariants {
		writeErr(w, http.StatusBadRequest, fmt.Sprintf("1 to %d lines", variants.MaxVariants))
		return
	}
	p := principalFrom(r.Context())
	_, items, err := s.stockItems(r.Context(), p.TenantID)
	if err != nil {
		s.stockFail(w, r, err)
		return
	}
	byID := map[string]stock.Item{}
	for _, it := range items {
		byID[it.ID] = it
	}
	product := ""
	seen := map[string]bool{}
	for _, l := range in.Lines {
		it, ok := byID[l.ItemID]
		switch {
		case !ok:
			s.stockFail(w, r, errNoItem)
			return
		case seen[l.ItemID]:
			writeErr(w, http.StatusBadRequest, "each size or colour once")
			return
		case it.VariantOf == "" || (product != "" && it.VariantOf != product):
			writeErr(w, http.StatusBadRequest, "the lines are sizes and colours of one product")
			return
		case it.Retired():
			writeErr(w, http.StatusBadRequest, it.Name+" is no longer sold: bring it back in the product's sizes and colours first")
			return
		case in.Reason == stock.Received && (l.Change == nil || *l.Change <= 0 || *l.Change > stock.MaxDelta):
			writeErr(w, http.StatusBadRequest, "goods received are more than 0")
			return
		case in.Reason == stock.Count && (l.Count == nil || *l.Count < 0 || *l.Count > stock.MaxDelta):
			writeErr(w, http.StatusBadRequest, "a count is 0 or more")
			return
		}
		seen[l.ItemID] = true
		product = it.VariantOf
	}
	audit := func(tx pgx.Tx, action, target string, detail map[string]any) error {
		return auditTx(r, tx, action, target, detail)
	}
	moved := []stock.Movement{}
	var total int64
	for _, l := range in.Lines {
		it := byID[l.ItemID]
		note := strings.TrimSpace(in.Note)
		mv := stock.Move{ItemID: it.ID, Item: it.Name, Reason: in.Reason, Delta: l.Change, SetTo: l.Count, Note: note, Actor: p.Actor(),
			Key: "api-" + key + "-" + it.ID, CostKobo: it.CostKobo}
		m, err := s.stockMove(r.Context(), p.TenantID, mv, audit)
		if err != nil {
			s.stockFail(w, r, err)
			return
		}
		moved = append(moved, m)
		if l.Change != nil {
			total += *l.Change
		}
	}
	name := byID[product].Name
	n := len(in.Lines)
	if in.Reason == stock.Received {
		s.diaryChange(r.Context(), p, fmt.Sprintf("Stock in: %s, %d %s, +%d", name, n, sizesWord(n), total))
	} else {
		s.diaryChange(r.Context(), p, fmt.Sprintf("Stock of %s counted: %d %s", name, n, sizesWord(n)))
	}
	writeJSON(w, http.StatusOK, map[string]any{"movements": moved})
}

// sizesFollow passes a change of a product (set: its cells as written)
// on to its sizes and colours: a new name into theirs ("Air Max 95 – 42,
// Black"); its unit, cost, levels, supplier and category; its price to
// those without one of their own. It returns the ones whose category
// changed, for the price lists. key, when given, keys each write.
func (s *Server) sizesFollow(ctx context.Context, tenant uuid.UUID, sh stockShop, product stock.Item, name, key string, set map[string]any) []string {
	if !product.HasSizes() {
		return nil
	}
	m := sh.cols()
	_, items, err := s.stockItems(ctx, tenant)
	if err != nil {
		s.Logger.Warn("stock: its sizes and colours", "tenant", tenant, "item", product.ID, "err", err)
		return nil
	}
	own := variants.Setup{Choices: product.Choices, OwnPrice: product.OwnPrice}
	var recategorised []string
	for _, v := range variantsOf(items, product.ID) {
		vset := map[string]any{}
		for _, col := range []string{m.Unit, m.CostPrice, m.ReorderLevel, m.TargetLevel, m.Supplier, m.Category} {
			if x, ok := set[col]; ok && col != "" {
				vset[col] = x
			}
		}
		if x, ok := set[m.Price]; ok && m.Price != "" && !own.Owns(variants.Key(v.Variant)) {
			vset[m.Price] = x
		}
		if name != "" && name != product.Name && v.Variant != nil {
			vset[m.Name] = variants.Name(name, v.Variant, product.Choices)
		}
		if len(vset) == 0 {
			continue
		}
		k := ""
		if key != "" {
			k = key + "-size-" + v.ID
		}
		if err := s.stockSetCells(ctx, tenant, sh, v.ID, k, vset); err != nil {
			s.Logger.Warn("stock: a size or colour following its product", "tenant", tenant, "item", v.ID, "err", err)
			continue
		}
		if x, ok := vset[m.Category]; ok && x != v.Category {
			recategorised = append(recategorised, v.ID)
		}
	}
	return recategorised
}

// markOwnPrice records that a size or colour, priced on its own, keeps
// its price when its product's changes.
func (s *Server) markOwnPrice(ctx context.Context, tenant uuid.UUID, sh stockShop, size stock.Item) {
	if size.VariantOf == "" || size.Variant == nil {
		return
	}
	_, items, err := s.stockItems(ctx, tenant)
	if err != nil {
		s.Logger.Warn("stock: a size's own price", "tenant", tenant, "item", size.ID, "err", err)
		return
	}
	for _, it := range items {
		if it.ID != size.VariantOf {
			continue
		}
		st := variants.Setup{Choices: it.Choices, OwnPrice: it.OwnPrice}
		k := variants.Key(size.Variant)
		if st.Owns(k) || len(st.Choices) == 0 {
			return
		}
		st.OwnPrice = append(st.OwnPrice, k)
		if err := s.stockSetCells(ctx, tenant, sh, it.ID, "", map[string]any{sh.cols().Choices: setupCell(st)}); err != nil {
			s.Logger.Warn("stock: a size's own price", "tenant", tenant, "item", size.ID, "err", err)
		}
		return
	}
}
