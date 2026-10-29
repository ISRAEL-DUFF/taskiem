package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/datatables"
	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/stock"
	"github.com/israel-duff/taskiem/engine/storefront"
)

// Stock by chat (docs/spec/stock.md, decision 0057), the web's and the
// API's door: the Stock page's items, read from the catalogue table
// itself with the columns stock maps; movements; adding items, changing
// their stock columns, and moving their stock. Chat (whatsapp_stock.go)
// goes through the same functions.

var (
	// errStockOff: the business has no catalogue keeping stock yet.
	errStockOff = errors.New("stock: no catalogue keeps stock yet")
	// errItemExists: an item of that name is in the catalogue already.
	errItemExists = errors.New("stock: an item of that name is there already")
	// errNoItem: no item has that row id.
	errNoItem = errors.New("stock: no such item")
)

// stockColumns are the columns Keep my stock adds to a catalogue that
// lacks them, by the mapping field each is for.
var stockColumns = []struct {
	field func(*storefront.Columns) *string
	col   datatables.Column
}{
	{func(m *storefront.Columns) *string { return &m.Stock }, datatables.Column{Name: "stock", Title: "Stock", Type: "integer"}},
	{func(m *storefront.Columns) *string { return &m.Unit }, datatables.Column{Name: "unit", Title: "Unit", Type: "text"}},
	{func(m *storefront.Columns) *string { return &m.CostPrice }, datatables.Column{Name: "cost_price", Title: "Cost price (₦)", Type: "number"}},
	{func(m *storefront.Columns) *string { return &m.ReorderLevel }, datatables.Column{Name: "reorder_level", Title: "Reorder at", Type: "integer"}},
	{func(m *storefront.Columns) *string { return &m.TargetLevel }, datatables.Column{Name: "target_level", Title: "Order up to", Type: "integer"}},
	{func(m *storefront.Columns) *string { return &m.Supplier }, datatables.Column{Name: "supplier", Title: "Supplier", Type: "text"}},
}

// stockShop is a business's catalogue keeping stock: its storefront (a
// draft, when the shop has no page) and the table.
type stockShop struct {
	sf    storefront.Storefront
	table datatables.Table
}

func (sh stockShop) cols() storefront.Columns { return sh.sf.CatalogueColumns }

// stockShopOf is the business's catalogue keeping stock, or errStockOff.
func (s *Server) stockShopOf(ctx context.Context, tenant uuid.UUID) (stockShop, error) {
	var sh stockShop
	err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		var err error
		if sh.sf, err = storefront.Get(ctx, tx, tenant); err != nil {
			return err
		}
		if sh.sf.CatalogueTable == nil || sh.sf.CatalogueColumns.Stock == "" {
			return errStockOff
		}
		sh.table, err = datatables.GetTable(ctx, tx, *sh.sf.CatalogueTable)
		return err
	})
	if errors.Is(err, storefront.ErrNotFound) || errors.Is(err, datatables.ErrNotFound) {
		err = errStockOff
	}
	return sh, err
}

// stockItems reads the business's items from the catalogue table itself.
func (s *Server) stockItems(ctx context.Context, tenant uuid.UUID) (stockShop, []stock.Item, error) {
	sh, err := s.stockShopOf(ctx, tenant)
	if err != nil {
		return sh, nil, err
	}
	rows, err := s.readAllRows(ctx, tenant, sh.table, storefront.MaxItems+1)
	if err != nil {
		return sh, nil, err
	}
	return sh, stock.Items(rows, sh.cols()), nil
}

// stockItem reads one item by its row id.
func (s *Server) stockItem(ctx context.Context, tenant uuid.UUID, sh stockShop, id string) (stock.Item, error) {
	tr, err := s.tableRowsFor(ctx, tenant, sh.table, storefrontPurpose)
	if err != nil {
		return stock.Item{}, err
	}
	page, err := tr.call(ctx, s, "read_rows", map[string]any{"table": sh.table.Name, "limit": 1,
		"filters": []any{map[string]any{"column": "id", "op": "eq", "value": id}}}, "")
	if err != nil {
		return stock.Item{}, err
	}
	var rows []map[string]any
	for _, r := range rowsOf(page) {
		if m, ok := r.(map[string]any); ok {
			rows = append(rows, m)
		}
	}
	items := stock.Items(rows, sh.cols())
	if len(items) == 0 {
		return stock.Item{}, errNoItem
	}
	return items[0], nil
}

// stockAudit records what a person did, in the transaction given.
type stockAudit func(tx pgx.Tx, action, target string, detail map[string]any) error

// stockSetup makes the business's catalogue keep stock (Keep my stock):
// a draft storefront under the business's name when there is none, a
// products table with the stock columns when there is no catalogue, and
// the stock columns added and mapped where an existing catalogue lacks
// them (the stock column always; the others while the plan's column limit
// allows).
func (s *Server) stockSetup(ctx context.Context, tenant uuid.UUID, actor string, audit stockAudit) (stockShop, error) {
	inTx := func(fn func(pgx.Tx) error) error { return db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{tenant}, fn) }
	var sf storefront.Storefront
	var business string
	err := inTx(func(tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `SELECT name FROM tenants WHERE id = $1`, tenant).Scan(&business); err != nil {
			return err
		}
		var err error
		sf, err = storefront.Get(ctx, tx, tenant)
		if errors.Is(err, storefront.ErrNotFound) {
			if sf, err = saveBareDraft(ctx, tx, tenant, storefront.Card{Name: business}, nil, storefront.Columns{}, actor); err != nil {
				return err
			}
			return audit(tx, "storefront.save", sf.Slug, map[string]any{"for": "stock"})
		}
		return err
	})
	if err != nil {
		return stockShop{}, err
	}
	if sf.CatalogueTable == nil {
		t := datatables.Table{Name: "products", Title: "Products", Description: "The shop's items and their stock", Columns: productColumns}
		if err := s.makeOwnTable(ctx, tenant, actor, audit, &t, "products", "shop_products", "products_2", "products_3"); err != nil {
			return stockShop{}, err
		}
		m := productMapping
		err := inTx(func(tx pgx.Tx) error {
			if err := m.Check(tableColumns(t)); err != nil {
				return err
			}
			var err error
			if sf, err = storefront.Save(ctx, tx, tenant, sf.Slug, sf.Draft, &t.ID, m, actor); err != nil {
				return err
			}
			return audit(tx, "stock.setup", t.Name, map[string]any{"table_id": t.ID, "made": true})
		})
		if err != nil {
			return stockShop{}, err
		}
		if err := s.SyncStorefront(ctx, tenant); err != nil {
			s.Logger.Warn("stock: refreshing the catalogue", "tenant", tenant, "err", err)
		}
		return s.stockShopOf(ctx, tenant)
	}
	// An existing catalogue: map the stock columns it has, add those it
	// lacks.
	var t datatables.Table
	if err := inTx(func(tx pgx.Tx) error {
		var err error
		t, err = datatables.GetTable(ctx, tx, *sf.CatalogueTable)
		return err
	}); err != nil {
		return stockShop{}, err
	}
	if err := datatables.Writable(t); err != nil {
		return stockShop{}, err
	}
	lim, err := s.Store.LimitsFor(ctx, tenant)
	if err != nil {
		return stockShop{}, err
	}
	m := sf.CatalogueColumns
	var added []string
	for i, sc := range stockColumns {
		field := sc.field(&m)
		if *field != "" {
			continue
		}
		have, typ := "", ""
		for _, c := range t.Columns {
			if c.Name == sc.col.Name {
				have, typ = c.Name, c.Type
			}
		}
		if have != "" {
			if typ == sc.col.Type || sc.col.Type == "number" && typ == "integer" {
				*field = have
			}
			continue
		}
		if lim.MaxDataTableColumns > 0 && len(t.Columns) >= lim.MaxDataTableColumns {
			if i == 0 {
				return stockShop{}, fmt.Errorf("%w: the catalogue table has as many columns as the plan allows; add a whole-number stock column to it", datatables.ErrInvalid)
			}
			continue
		}
		if err := s.addOwnColumn(ctx, tenant, actor, audit, &t, sc.col); err != nil {
			return stockShop{}, err
		}
		*field = sc.col.Name
		added = append(added, sc.col.Name)
	}
	if m.Stock == "" {
		return stockShop{}, fmt.Errorf("%w: the catalogue's column named stock is not a whole number; map another stock column on the storefront page", datatables.ErrInvalid)
	}
	if err := inTx(func(tx pgx.Tx) error {
		if err := m.Check(tableColumns(t)); err != nil {
			return err
		}
		if _, err := storefront.Save(ctx, tx, tenant, sf.Slug, sf.Draft, &t.ID, m, actor); err != nil {
			return err
		}
		return audit(tx, "stock.setup", t.Name, map[string]any{"table_id": t.ID, "added": added})
	}); err != nil {
		return stockShop{}, err
	}
	if err := s.SyncStorefront(ctx, tenant); err != nil {
		s.Logger.Warn("stock: refreshing the catalogue", "tenant", tenant, "err", err)
	}
	return s.stockShopOf(ctx, tenant)
}

// saveBareDraft saves a storefront draft at the first free address its
// name gives (a taken one tried inside a savepoint, so the next can be
// tried in the same transaction).
func saveBareDraft(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, card storefront.Card, table *uuid.UUID, cols storefront.Columns, actor string) (storefront.Storefront, error) {
	for _, slug := range storefront.SlugCandidates(card.Name, card.Area) {
		if _, err := storefront.CheckSlug(slug); err != nil {
			continue
		}
		sp, err := tx.Begin(ctx)
		if err != nil {
			return storefront.Storefront{}, err
		}
		sf, err := storefront.Save(ctx, sp, tenant, slug, card, table, cols, actor)
		if errors.Is(err, storefront.ErrSlugTaken) {
			_ = sp.Rollback(ctx)
			continue
		}
		if err != nil {
			_ = sp.Rollback(ctx)
			return storefront.Storefront{}, err
		}
		return sf, sp.Commit(ctx)
	}
	return storefront.Storefront{}, fmt.Errorf("%w: no free address for %q", storefront.ErrSlugTaken, card.Name)
}

// addOwnColumn adds a column to one of the business's own tables, as the
// API's addTableColumn does: in Xenibase, then the record.
func (s *Server) addOwnColumn(ctx context.Context, tenant uuid.UUID, actor string, audit stockAudit, t *datatables.Table, c datatables.Column) error {
	svc, err := s.tables()
	if err != nil {
		return err
	}
	if err := datatables.CheckColumn(&c); err != nil {
		return err
	}
	if err := svc.Apply(ctx, tenant, actor, datatables.AddColumnChange(t.Name, c)); err != nil {
		return err
	}
	return db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		if err := datatables.InsertColumn(ctx, tx, tenant, t.ID, c); err != nil {
			return err
		}
		if err := audit(tx, "table.column.add", t.Name+"."+c.Name, map[string]any{"table_id": t.ID, "type": c.Type, "for": "stock"}); err != nil {
			return err
		}
		var err error
		*t, err = datatables.GetTable(ctx, tx, t.ID)
		return err
	})
}

// stockMove records a movement with its audit entry, then sends it.
func (s *Server) stockMove(ctx context.Context, tenant uuid.UUID, mv stock.Move, audit stockAudit) (stock.Movement, error) {
	var m stock.Movement
	if err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		var err error
		if m, err = stock.RecordTx(ctx, tx, tenant, mv); err != nil {
			return err
		}
		detail := map[string]any{"item_id": mv.ItemID, "reason": mv.Reason, "movement_id": m.ID}
		if mv.Delta != nil {
			detail["change"] = *mv.Delta
		}
		if mv.SetTo != nil {
			detail["count"] = *mv.SetTo
		}
		return audit(tx, "stock.move", mv.Item, detail)
	}); err != nil {
		return m, err
	}
	out, err := s.Stock.Apply(ctx, tenant, m.ID)
	if err != nil || out.ID == uuid.Nil {
		// Recorded: the sweep sends it again.
		if err != nil {
			s.Logger.Warn("stock: sending a movement", "tenant", tenant, "movement", m.ID, "err", err)
		}
		return m, nil
	}
	return out, nil
}

// stockNew is an item to add: its name, how many there are, its unit,
// the price paid and sold at (kobo), its levels and supplier.
type stockNew struct {
	Name      string `json:"name"`
	Quantity  int64  `json:"quantity"`
	Unit      string `json:"unit"`
	CostKobo  *int64 `json:"cost_kobo"`
	PriceKobo *int64 `json:"price_kobo"`
	Reorder   *int64 `json:"reorder_level"`
	Target    *int64 `json:"target_level"`
	Supplier  string `json:"supplier"`
}

func (in *stockNew) check() error {
	in.Name, in.Unit, in.Supplier = strings.TrimSpace(in.Name), strings.TrimSpace(in.Unit), strings.TrimSpace(in.Supplier)
	switch {
	case in.Name == "" || len([]rune(in.Name)) > 120:
		return fmt.Errorf("%w: an item's name is 1 to 120 characters", errBadRequest)
	case in.Quantity < 0 || in.Quantity > stock.MaxDelta:
		return fmt.Errorf("%w: the quantity is 0 to %d", errBadRequest, stock.MaxDelta)
	case len([]rune(in.Unit)) > 30 || len([]rune(in.Supplier)) > 120:
		return fmt.Errorf("%w: the unit is up to 30 characters, the supplier up to 120", errBadRequest)
	}
	for _, v := range []*int64{in.CostKobo, in.PriceKobo, in.Reorder, in.Target} {
		if v != nil && (*v < 0 || *v > 1e12) {
			return fmt.Errorf("%w: prices and levels are 0 or more", errBadRequest)
		}
	}
	return nil
}

// stockCells is an item's stock columns as the table holds them.
func stockCells(m storefront.Columns, unit string, cost, reorder, target *int64, supplier string) map[string]any {
	set := map[string]any{}
	if m.Unit != "" && unit != "" {
		set[m.Unit] = unit
	}
	if m.CostPrice != "" && cost != nil {
		set[m.CostPrice] = priceCell(*cost, m)
	}
	if m.ReorderLevel != "" && reorder != nil {
		set[m.ReorderLevel] = *reorder
	}
	if m.TargetLevel != "" && target != nil {
		set[m.TargetLevel] = *target
	}
	if m.Supplier != "" && supplier != "" {
		set[m.Supplier] = supplier
	}
	return set
}

// stockAdd adds an item to the catalogue with a count of 0, then records
// its quantity as received, so the count is explained from the start. An
// item not priced for sale is not shown on the page until it is.
func (s *Server) stockAdd(ctx context.Context, tenant uuid.UUID, actor, key string, in stockNew, audit stockAudit) (stock.Item, stock.Movement, error) {
	sh, items, err := s.stockItems(ctx, tenant)
	if err != nil {
		return stock.Item{}, stock.Movement{}, err
	}
	for _, it := range items {
		if matchKey(it.Name) == matchKey(in.Name) {
			// This key was adding it (a retry after a failure part way):
			// finish, rather than refuse the item it made.
			mine, err := s.stockAdding(ctx, tenant, key, in.Name)
			if err != nil {
				return it, stock.Movement{}, err
			}
			if !mine {
				return it, stock.Movement{}, fmt.Errorf("%w: %s", errItemExists, it.Name)
			}
			return s.stockAdded(ctx, tenant, actor, key, in, sh, it.ID, audit)
		}
	}
	if len(items) >= storefront.MaxItems {
		return stock.Item{}, stock.Movement{}, fmt.Errorf("%w: the catalogue holds at most %d items", errConflict, storefront.MaxItems)
	}
	m := sh.cols()
	row := stockCells(m, in.Unit, in.CostKobo, in.Reorder, in.Target, in.Supplier)
	row[m.Name], row[m.Stock] = in.Name, 0
	if m.Price != "" && in.PriceKobo != nil {
		row[m.Price] = priceCell(*in.PriceKobo, m)
	}
	if m.Available != "" {
		row[m.Available] = in.PriceKobo != nil
	}
	checked, err := datatables.CheckRow(sh.table.Columns, row, true, false)
	if err != nil {
		return stock.Item{}, stock.Movement{}, fmt.Errorf("%w: %s", errBadRequest, strings.TrimPrefix(err.Error(), "invalid: "))
	}
	tr, err := s.tableRowsFor(ctx, tenant, sh.table, storefrontPurpose)
	if err != nil {
		return stock.Item{}, stock.Movement{}, err
	}
	// What this key is adding, before the row is: a retry finds it.
	if err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		return audit(tx, "stock.item_adding", sh.table.Name, map[string]any{"key": key, "name": matchKey(in.Name)})
	}); err != nil {
		return stock.Item{}, stock.Movement{}, err
	}
	out, err := tr.call(ctx, s, "insert_rows", map[string]any{"table": sh.table.Name, "rows": []any{checked}}, key+"-row")
	if err != nil {
		return stock.Item{}, stock.Movement{}, err
	}
	id := ""
	if rows := rowsOf(out); len(rows) > 0 {
		if r, ok := rows[0].(map[string]any); ok && r["id"] != nil {
			id = cell(r["id"])
		}
	}
	if id == "" {
		return stock.Item{}, stock.Movement{}, errors.New("stock: the table gave no id for the new item")
	}
	return s.stockAdded(ctx, tenant, actor, key, in, sh, id, audit)
}

// stockAdding says key was adding an item named name (stockAdd's record
// before its row).
func (s *Server) stockAdding(ctx context.Context, tenant uuid.UUID, key, name string) (bool, error) {
	var mine bool
	err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM audit_log WHERE tenant_id = $1 AND action = 'stock.item_adding'
			AND detail->>'key' = $2 AND detail->>'name' = $3)`, tenant, key, matchKey(name)).Scan(&mine)
	})
	return mine, err
}

// stockAdded finishes adding item id, whose row is in the table: its
// audit entry and its quantity as received, in one transaction, once;
// then sends the movement and reads the item.
func (s *Server) stockAdded(ctx context.Context, tenant uuid.UUID, actor, key string, in stockNew, sh stockShop, id string, audit stockAudit) (stock.Item, stock.Movement, error) {
	var mv stock.Movement
	if err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		var done bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM audit_log WHERE tenant_id = $1 AND action = 'storefront.item_add'
			AND detail->>'key' = $2 AND detail->>'row_id' = $3)`, tenant, key, id).Scan(&done); err != nil {
			return err
		}
		if !done {
			if err := audit(tx, "storefront.item_add", sh.table.Name, map[string]any{"table_id": sh.table.ID, "row_id": id, "for": "stock", "key": key}); err != nil {
				return err
			}
		}
		if in.Quantity <= 0 {
			return nil
		}
		q := in.Quantity
		var err error
		if mv, err = stock.RecordTx(ctx, tx, tenant, stock.Move{ItemID: id, Item: in.Name, Reason: stock.Received, Delta: &q, Actor: actor,
			Key: key + "-received", CostKobo: in.CostKobo}); err != nil || done {
			return err
		}
		return audit(tx, "stock.move", in.Name, map[string]any{"item_id": id, "reason": stock.Received, "movement_id": mv.ID, "change": q})
	}); err != nil {
		return stock.Item{}, mv, err
	}
	if mv.ID != uuid.Nil {
		if out, err := s.Stock.Apply(ctx, tenant, mv.ID); err != nil {
			s.Logger.Warn("stock: sending a movement", "tenant", tenant, "movement", mv.ID, "err", err) // recorded: the sweep sends it again
		} else if out.ID != uuid.Nil {
			mv = out
		}
	}
	if err := s.SyncStorefront(ctx, tenant); err != nil {
		s.Logger.Warn("stock: refreshing the catalogue", "tenant", tenant, "err", err)
	}
	it, err := s.stockItem(ctx, tenant, sh, id)
	return it, mv, err
}

// stockSetCells writes an item's columns other than its stock (its name,
// prices, unit, levels, supplier).
func (s *Server) stockSetCells(ctx context.Context, tenant uuid.UUID, sh stockShop, id, key string, set map[string]any) error {
	if len(set) == 0 {
		return nil
	}
	if _, ok := set[sh.cols().Stock]; ok {
		return fmt.Errorf("%w: the stock changes by movements only", errBadRequest)
	}
	checked, err := datatables.CheckRow(sh.table.Columns, set, false, false)
	if err != nil {
		return fmt.Errorf("%w: %s", errBadRequest, strings.TrimPrefix(err.Error(), "invalid: "))
	}
	rowID, err := strconv.ParseInt(id, 10, 64)
	if err != nil {
		return errNoItem
	}
	tr, err := s.tableRowsFor(ctx, tenant, sh.table, storefrontPurpose)
	if err != nil {
		return err
	}
	out, err := tr.call(ctx, s, "update_rows", map[string]any{"table": sh.table.Name, "key": rowID, "set": checked, "max_affected": 1}, key)
	if err != nil {
		return err
	}
	if len(rowsOf(out)) == 0 && countValue(out["affected"]) == 0 {
		return errNoItem
	}
	return nil
}

// --- the HTTP door ---

type stockItemView struct {
	stock.Item
	Low     bool  `json:"low"`
	Suggest int64 `json:"suggest,omitempty"`
	// Waiting is how many of its movements are still on their way to
	// the table.
	Waiting int `json:"waiting,omitempty"`
}

// getStock is GET /v1/stock: whether the catalogue keeps stock, and its
// items with their stock, low ones first.
func (s *Server) getStock(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r.Context())
	sh, items, err := s.stockItems(r.Context(), p.TenantID)
	if errors.Is(err, errStockOff) {
		out := map[string]any{"keeps_stock": false, "catalogue": nil, "items": []any{}}
		if sh.table.Name != "" {
			out["catalogue"] = sh.table.Name
		}
		writeJSON(w, http.StatusOK, out)
		return
	}
	if err != nil {
		s.stockFail(w, r, err)
		return
	}
	waiting := map[string]int{}
	var stuck []stock.Movement
	if err := s.tx(r, func(tx pgx.Tx) error {
		rows, err := tx.Query(r.Context(), `SELECT item_id, count(*) FROM stock_movements WHERE tenant_id = $1 AND status = 'pending' GROUP BY item_id`, p.TenantID)
		if err != nil {
			return err
		}
		for rows.Next() {
			var id string
			var n int
			if err := rows.Scan(&id, &n); err != nil {
				return err
			}
			waiting[id] = n
		}
		if err := rows.Err(); err != nil {
			return err
		}
		list, err := stock.ListTx(r.Context(), tx, p.TenantID, stock.Filter{Limit: 50})
		for _, m := range list {
			if m.Status == "pending" && m.Error != "" {
				stuck = append(stuck, m)
			}
		}
		return err
	}); err != nil {
		s.fail(w, r, err)
		return
	}
	views := make([]stockItemView, len(items))
	low := 0
	for i, it := range items {
		views[i] = stockItemView{Item: it, Low: it.Low(), Suggest: it.Suggest(), Waiting: waiting[it.ID]}
		if it.Low() {
			low++
		}
	}
	if stuck == nil {
		stuck = []stock.Movement{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"keeps_stock": true, "catalogue": sh.table.Name, "columns": sh.cols(), "items": views,
		"low": low, "stuck": stuck, "published": sh.sf.PublishedVersion > 0})
}

// listStockMovements is GET /v1/stock/movements?item=&reason=&since=&until=&limit=.
func (s *Server) listStockMovements(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	f := stock.Filter{ItemID: q.Get("item"), Reason: q.Get("reason")}
	if v := q.Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 500 {
			writeErr(w, http.StatusBadRequest, "limit is 1 to 500")
			return
		}
		f.Limit = n
	}
	for _, x := range []struct {
		name string
		to   **time.Time
	}{{"since", &f.Since}, {"until", &f.Until}} {
		if v := q.Get(x.name); v != "" {
			t, err := time.Parse(time.RFC3339, v)
			if err != nil {
				writeErr(w, http.StatusBadRequest, x.name+" is a time like 2026-10-29T00:00:00Z")
				return
			}
			*x.to = &t
		}
	}
	if f.Reason != "" && !validReason(f.Reason) {
		writeErr(w, http.StatusBadRequest, "reason is order, sale, received, adjustment, count or outside")
		return
	}
	var list []stock.Movement
	if err := s.tx(r, func(tx pgx.Tx) error {
		var err error
		list, err = stock.ListTx(r.Context(), tx, principalFrom(r.Context()).TenantID, f)
		return err
	}); err != nil {
		s.fail(w, r, err)
		return
	}
	if list == nil {
		list = []stock.Movement{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"movements": list})
}

func validReason(r string) bool {
	switch r {
	case stock.Order, stock.Sale, stock.Received, stock.Adjustment, stock.Count, stock.Outside:
		return true
	}
	return false
}

// setupStock is POST /v1/stock/setup: Keep my stock.
func (s *Server) setupStock(w http.ResponseWriter, r *http.Request) {
	if !s.Stock.Ready() {
		writeErr(w, http.StatusServiceUnavailable, "stock is not available on this platform")
		return
	}
	p := principalFrom(r.Context())
	sh, err := s.stockSetup(r.Context(), p.TenantID, p.Actor(), func(tx pgx.Tx, action, target string, detail map[string]any) error {
		return auditTx(r, tx, action, target, detail)
	})
	if err != nil {
		s.stockFail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"keeps_stock": true, "catalogue": sh.table.Name, "columns": sh.cols()})
}

// addStockItem is POST /v1/stock/items, with an Idempotency-Key: an item
// and how many there are.
func (s *Server) addStockItem(w http.ResponseWriter, r *http.Request) {
	if !s.Stock.Ready() {
		writeErr(w, http.StatusServiceUnavailable, "stock is not available on this platform")
		return
	}
	key, err := requiredKey(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	var in stockNew
	if err := decodeBody(r, &in); err != nil {
		s.fail(w, r, err)
		return
	}
	if err := in.check(); err != nil {
		s.fail(w, r, err)
		return
	}
	p := principalFrom(r.Context())
	it, mv, err := s.stockAdd(r.Context(), p.TenantID, p.Actor(), "api-"+key, in, func(tx pgx.Tx, action, target string, detail map[string]any) error {
		return auditTx(r, tx, action, target, detail)
	})
	if errors.Is(err, errItemExists) {
		writeJSON(w, http.StatusConflict, map[string]any{"error": "an item named " + it.Name + " is there already: record goods received on it instead",
			"code": "item_exists", "item_id": it.ID})
		return
	}
	if err != nil {
		s.stockFail(w, r, err)
		return
	}
	out := map[string]any{"item": it}
	if mv.ID != uuid.Nil {
		out["movement"] = mv
	}
	writeJSON(w, http.StatusCreated, out)
}

// patchStockItem is PATCH /v1/stock/items/{id}: an item's name, prices,
// unit, levels or supplier. Its stock changes by movements only.
func (s *Server) patchStockItem(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Name      *string `json:"name"`
		Unit      *string `json:"unit"`
		CostKobo  *int64  `json:"cost_kobo"`
		PriceKobo *int64  `json:"price_kobo"`
		Reorder   *int64  `json:"reorder_level"`
		Target    *int64  `json:"target_level"`
		Supplier  *string `json:"supplier"`
	}
	if err := decodeBody(r, &in); err != nil {
		s.fail(w, r, err)
		return
	}
	key := ""
	if strings.TrimSpace(r.Header.Get("Idempotency-Key")) != "" {
		k, err := idempotencyKey(r)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		key = "api-" + k
	}
	p := principalFrom(r.Context())
	sh, err := s.stockShopOf(r.Context(), p.TenantID)
	if err != nil {
		s.stockFail(w, r, err)
		return
	}
	m := sh.cols()
	set := map[string]any{}
	put := func(col, what string, v any) error {
		if col == "" {
			return fmt.Errorf("%w: the catalogue has no %s column", errConflict, what)
		}
		set[col] = v
		return nil
	}
	var perr error
	for _, f := range []func() error{
		func() error {
			if in.Name == nil {
				return nil
			}
			n := strings.TrimSpace(*in.Name)
			if n == "" || len([]rune(n)) > 120 {
				return fmt.Errorf("%w: an item's name is 1 to 120 characters", errBadRequest)
			}
			return put(m.Name, "name", n)
		},
		func() error {
			if in.Unit == nil {
				return nil
			}
			if len([]rune(strings.TrimSpace(*in.Unit))) > 30 {
				return fmt.Errorf("%w: the unit is up to 30 characters", errBadRequest)
			}
			return put(m.Unit, "unit", strings.TrimSpace(*in.Unit))
		},
		func() error {
			if in.Supplier == nil {
				return nil
			}
			return put(m.Supplier, "supplier", strings.TrimSpace(*in.Supplier))
		},
		func() error {
			if in.CostKobo == nil {
				return nil
			}
			return put(m.CostPrice, "cost price", priceCell(*in.CostKobo, m))
		},
		func() error {
			if in.PriceKobo == nil {
				return nil
			}
			return put(m.Price, "price", priceCell(*in.PriceKobo, m))
		},
		func() error {
			if in.Reorder == nil {
				return nil
			}
			return put(m.ReorderLevel, "reorder level", *in.Reorder)
		},
		func() error {
			if in.Target == nil {
				return nil
			}
			return put(m.TargetLevel, "target level", *in.Target)
		},
	} {
		if perr = f(); perr != nil {
			break
		}
	}
	for _, v := range []*int64{in.CostKobo, in.PriceKobo, in.Reorder, in.Target} {
		if v != nil && *v < 0 {
			perr = fmt.Errorf("%w: prices and levels are 0 or more", errBadRequest)
		}
	}
	if perr != nil {
		s.fail(w, r, perr)
		return
	}
	id := chi.URLParam(r, "id")
	if err := s.stockSetCells(r.Context(), p.TenantID, sh, id, key, set); err != nil {
		s.stockFail(w, r, err)
		return
	}
	if err := s.tx(r, func(tx pgx.Tx) error {
		cols := make([]string, 0, len(set))
		for c := range set {
			cols = append(cols, c)
		}
		return auditTx(r, tx, "storefront.item_change", sh.table.Name, map[string]any{"table_id": sh.table.ID, "row_id": id, "columns": cols})
	}); err != nil {
		s.fail(w, r, err)
		return
	}
	if err := s.SyncStorefront(r.Context(), p.TenantID); err != nil {
		s.Logger.Warn("stock: refreshing the catalogue", "tenant", p.TenantID, "err", err)
	}
	it, err := s.stockItem(r.Context(), p.TenantID, sh, id)
	if err != nil {
		s.stockFail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"item": it})
}

// moveStock is POST /v1/stock/movements, with an Idempotency-Key: goods
// received, an adjustment (with a note), or a count.
func (s *Server) moveStock(w http.ResponseWriter, r *http.Request) {
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
		ItemID    string `json:"item_id"`
		Reason    string `json:"reason"`
		Change    *int64 `json:"change"`
		Count     *int64 `json:"count"`
		Note      string `json:"note"`
		Reference string `json:"reference"`
	}
	if err := decodeBody(r, &in); err != nil {
		s.fail(w, r, err)
		return
	}
	switch in.Reason {
	case stock.Received, stock.Adjustment, stock.Count:
	default:
		writeErr(w, http.StatusBadRequest, "reason is received, adjustment or count (sales and orders move stock themselves)")
		return
	}
	if in.Reason == stock.Received && in.Change != nil && *in.Change <= 0 {
		writeErr(w, http.StatusBadRequest, "goods received are more than 0")
		return
	}
	p := principalFrom(r.Context())
	sh, err := s.stockShopOf(r.Context(), p.TenantID)
	if err != nil {
		s.stockFail(w, r, err)
		return
	}
	it, err := s.stockItem(r.Context(), p.TenantID, sh, in.ItemID)
	if err != nil {
		s.stockFail(w, r, err)
		return
	}
	mv := stock.Move{ItemID: it.ID, Item: it.Name, Reason: in.Reason, Delta: in.Change, SetTo: in.Count, Note: in.Note, Reference: in.Reference,
		Actor: p.Actor(), Key: "api-" + key, CostKobo: it.CostKobo}
	m, err := s.stockMove(r.Context(), p.TenantID, mv, func(tx pgx.Tx, action, target string, detail map[string]any) error {
		return auditTx(r, tx, action, target, detail)
	})
	if err != nil {
		s.stockFail(w, r, err)
		return
	}
	status := http.StatusOK
	if m.Status == "pending" {
		status = http.StatusAccepted
	}
	writeJSON(w, status, map[string]any{"movement": m})
}

// requiredKey is the request's Idempotency-Key, which a stock change must
// carry.
func requiredKey(r *http.Request) (string, error) {
	if strings.TrimSpace(r.Header.Get("Idempotency-Key")) == "" {
		return "", fmt.Errorf("%w: an Idempotency-Key header is required", errBadRequest)
	}
	return idempotencyKey(r)
}

// stockFail answers stock's refusals in plain words.
func (s *Server) stockFail(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, errStockOff):
		writeJSON(w, http.StatusConflict, map[string]any{"error": "no catalogue keeps stock yet: press Keep my stock", "code": "stock_off"})
	case errors.Is(err, errNoItem):
		writeErr(w, http.StatusNotFound, "no such item")
	case errors.Is(err, stock.ErrInvalid):
		s.fail(w, r, fmt.Errorf("%w: %s", errBadRequest, strings.TrimPrefix(err.Error(), "stock: invalid: ")))
	case errors.Is(err, stock.ErrSaleInvalid):
		s.fail(w, r, fmt.Errorf("%w: %s", errBadRequest, strings.TrimPrefix(err.Error(), "stock: invalid sale: ")))
	case errors.Is(err, stock.ErrEntryTaken):
		s.fail(w, r, fmt.Errorf("%w: that terminal sale counts for something else already, or is not of that amount", errConflict))
	case errors.Is(err, storefront.ErrInvalid), errors.Is(err, storefront.ErrSlugTaken):
		s.fail(w, r, fmt.Errorf("%w: %s", errBadRequest, strings.TrimPrefix(strings.TrimPrefix(err.Error(), "storefront: "), "invalid: ")))
	case errors.Is(err, datatables.ErrKept), errors.Is(err, datatables.ErrUnavailable), errors.Is(err, datatables.ErrProvisioning),
		errors.Is(err, datatables.ErrProjectFailed), errors.Is(err, datatables.ErrInvalid), errors.Is(err, datatables.ErrConflict):
		s.failTables(w, r, err)
	default:
		s.fail(w, r, err)
	}
}
