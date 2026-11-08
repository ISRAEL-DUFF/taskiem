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
}

// errNoVariantColumns: the catalogue cannot take the columns sizes and
// colours need (a table Taskiem may not change, or the plan's limit).
var errNoVariantColumns = errors.New("stock: no room for sizes and colours")

// stockVariantColumns makes the catalogue hold sizes and colours, once:
// columns of those names are mapped if the table has them, else added.
func (s *Server) stockVariantColumns(ctx context.Context, tenant uuid.UUID, actor string, audit stockAudit, sh stockShop) (stockShop, error) {
	m := sh.cols()
	if m.VariantOf != "" && m.Variant != "" && m.Choices != "" {
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
			if c.Name == vc.col.Name && c.Type == "text" {
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
// (engine/variants); a new one's count comes in as goods received, a kept
// one's as a count; one without its own price takes the product's. The
// product's own stock, when it had some, moves into its sizes and colours
// (a count to 0). No choices takes them all off sale.
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
		for _, v := range []*int64{c.Count, c.PriceKobo} {
			if v != nil && (*v < 0 || *v > stock.MaxDelta*1000) {
				s.fail(w, r, fmt.Errorf("%w: counts and prices are 0 or more", errBadRequest))
				return
			}
		}
	}
	p := principalFrom(r.Context())
	audit := func(tx pgx.Tx, action, target string, detail map[string]any) error {
		return auditTx(r, tx, action, target, detail)
	}
	ctx := r.Context()
	sh, items, err := s.stockItems(ctx, p.TenantID)
	if err != nil {
		s.stockFail(w, r, err)
		return
	}
	id := chi.URLParam(r, "id")
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
	if sh, err = s.stockVariantColumns(ctx, p.TenantID, p.Actor(), audit, sh); err != nil {
		s.stockFail(w, r, err)
		return
	}
	m := sh.cols()
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
	tr, err := s.tableRowsFor(ctx, p.TenantID, sh.table, storefrontPurpose)
	if err != nil {
		s.stockFail(w, r, err)
		return
	}
	price := func(c variants.Cell) any {
		if c.PriceKobo != nil {
			return priceCell(*c.PriceKobo, m)
		}
		if product.PriceKobo != nil {
			return priceCell(*product.PriceKobo, m)
		}
		return nil
	}
	// The product: its choices; its own stock, when it had some, moves in.
	if err := s.stockSetCells(ctx, p.TenantID, sh, id, "api-"+key+"-product", map[string]any{m.Choices: choiceCell(choices)}); err != nil {
		s.stockFail(w, r, err)
		return
	}
	if onSale == 0 && len(choices) > 0 && product.Stock != nil && *product.Stock > 0 {
		zero := int64(0)
		if _, err := s.stockMove(ctx, p.TenantID, stock.Move{ItemID: id, Item: product.Name, Reason: stock.Count, SetTo: &zero,
			Note: "Moved into its sizes and colours", Actor: p.Actor(), Key: "api-" + key + "-product-stock", CostKobo: product.CostKobo}, audit); err != nil {
			s.stockFail(w, r, err)
			return
		}
	}
	// New rows, in one insert.
	if len(plan.Create) > 0 {
		rows := make([]any, 0, len(plan.Create))
		for _, c := range plan.Create {
			row := stockCells(m, product.Unit, product.CostKobo, product.Reorder, product.Target, product.Supplier)
			row[m.Name] = variants.Name(product.Name, c.Variant, choices)
			row[m.Stock] = 0
			row[m.VariantOf] = id
			row[m.Variant] = variants.JSON(c.Variant)
			if v := price(c); v != nil && m.Price != "" {
				row[m.Price] = v
			}
			if m.Available != "" {
				row[m.Available] = product.Available
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
			n := *c.Count
			if _, err := s.stockMove(ctx, p.TenantID, stock.Move{ItemID: rid, Item: variants.Name(product.Name, c.Variant, choices), Reason: stock.Received,
				Delta: &n, Note: "Sizes and colours set up", Actor: p.Actor(), Key: "api-" + key + "-in-" + strconv.Itoa(i), CostKobo: product.CostKobo}, audit); err != nil {
				s.stockFail(w, r, err)
				return
			}
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
		if k.Revive && m.Available != "" {
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
		if m.Available == "" {
			continue
		}
		if err := s.stockSetCells(ctx, p.TenantID, sh, rid, "api-"+key+"-retire-"+rid, map[string]any{m.Available: false}); err != nil {
			s.stockFail(w, r, err)
			return
		}
	}
	if err := s.tx(r, func(tx pgx.Tx) error {
		return auditTx(r, tx, "stock.variants", product.Name, map[string]any{"item_id": id, "choices": choices,
			"made": len(plan.Create), "kept": len(plan.Keep), "retired": len(plan.Retire)})
	}); err != nil {
		s.fail(w, r, err)
		return
	}
	if err := s.SyncStorefront(ctx, p.TenantID); err != nil {
		s.Logger.Warn("stock: refreshing the catalogue", "tenant", p.TenantID, "err", err)
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

// choiceCell is a product's choices as its column holds them; nil when
// it has none.
func choiceCell(cs []variants.Choice) any {
	if len(cs) == 0 {
		return nil
	}
	return variants.JSON(cs)
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
