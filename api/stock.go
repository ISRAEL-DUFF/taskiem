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
	"github.com/israel-duff/taskiem/engine/links"
	"github.com/israel-duff/taskiem/engine/stock"
	"github.com/israel-duff/taskiem/engine/storefront"
)

// Stock (docs/spec/stock.md, docs/spec/what-a-business-buys.md §4;
// decisions 0057 and 0070), the web's and the API's door: the Stock page's
// items, the catalogue's products each with the stock item it is counted
// by, and the materials; movements; adding items, changing their details,
// and moving their stock. Counts are kept on stock items in Taskiem's
// database, never in the catalogue table. Chat (whatsapp_stock.go) goes
// through the same functions.

var (
	// errStockOff: the business has no catalogue yet.
	errStockOff = errors.New("stock: no catalogue yet")
	// errItemExists: an item of that name is there already.
	errItemExists = errors.New("stock: an item of that name is there already")
	// errNoItem: no item has that id.
	errNoItem = errors.New("stock: no such item")
)

// maxCategory is how long a category may be, in characters.
const maxCategory = 60

// categoryColumn is the column a catalogue gets the first time a category
// is set on one of its items (docs/spec/chat-categories-and-variants.md).
var categoryColumn = datatables.Column{Name: "category", Title: "Category", Type: "text"}

// errNoCategoryColumn: the catalogue is a table Taskiem may not change, so
// it cannot add the column itself; errCategoryColumnLimit: the table has as
// many columns as the plan allows.
var (
	errNoCategoryColumn    = errors.New("stock: add a category column to use categories")
	errCategoryColumnLimit = errors.New("stock: no room for a category column")
)

// stockCategories makes the catalogue hold categories, once: a text column
// named category is mapped if the table has one, else added (the plan's
// column limit allowing). The shop is answered as it now is.
func (s *Server) stockCategories(ctx context.Context, tenant uuid.UUID, actor string, audit stockAudit, sh stockShop) (stockShop, error) {
	if sh.cols().Category != "" {
		return sh, nil
	}
	t := sh.table
	m := sh.cols()
	for _, c := range t.Columns {
		if c.Name == categoryColumn.Name && c.Type == "text" {
			m.Category = c.Name
		}
	}
	if m.Category == "" {
		if datatables.Writable(t) != nil {
			return sh, errNoCategoryColumn
		}
		lim, err := s.Store.LimitsFor(ctx, tenant)
		if err != nil {
			return sh, err
		}
		if lim.MaxDataTableColumns > 0 && len(t.Columns) >= lim.MaxDataTableColumns {
			return sh, errCategoryColumnLimit
		}
		if err := s.addOwnColumn(ctx, tenant, actor, audit, &t, categoryColumn); err != nil {
			return sh, err
		}
		m.Category = categoryColumn.Name
	}
	if err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		if err := m.Check(tableColumns(t)); err != nil {
			return err
		}
		_, err := storefront.Save(ctx, tx, tenant, sh.sf.Slug, sh.sf.Draft, &t.ID, m, actor)
		return err
	}); err != nil {
		return sh, err
	}
	return s.stockShopOf(ctx, tenant)
}

// checkCategory trims a category and checks its length.
func checkCategory(c string) (string, error) {
	c = strings.Join(strings.Fields(c), " ")
	if len([]rune(c)) > maxCategory {
		return c, fmt.Errorf("%w: a category is up to %d characters", errBadRequest, maxCategory)
	}
	return c, nil
}

// stockShop is a business's catalogue: its storefront (a draft, when the
// shop has no page) and the table.
type stockShop struct {
	sf    storefront.Storefront
	table datatables.Table
}

func (sh stockShop) cols() storefront.Columns { return sh.sf.CatalogueColumns }

// stockShopOf is the business's catalogue, or errStockOff.
func (s *Server) stockShopOf(ctx context.Context, tenant uuid.UUID) (stockShop, error) {
	var sh stockShop
	err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		var err error
		if sh.sf, err = storefront.Get(ctx, tx, tenant); err != nil {
			return err
		}
		if sh.sf.CatalogueTable == nil {
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

// stockItems reads the business's items: the catalogue table's products,
// each with the stock item it is counted by, and the materials.
func (s *Server) stockItems(ctx context.Context, tenant uuid.UUID) (stockShop, []stock.Item, error) {
	sh, err := s.stockShopOf(ctx, tenant)
	if err != nil {
		return sh, nil, err
	}
	rows, err := s.readAllRows(ctx, tenant, sh.table, storefront.MaxItems+1)
	if err != nil {
		return sh, nil, err
	}
	var items []stock.Item
	err = db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		var err error
		items, err = stock.ItemsTx(ctx, tx, tenant, rows, sh.cols())
		return err
	})
	return sh, items, err
}

// stockItem reads one item by its id: a product's row id, or a stock
// item's (a material; the product it counts, for one that counts one).
func (s *Server) stockItem(ctx context.Context, tenant uuid.UUID, sh stockShop, id string) (stock.Item, error) {
	row := id
	if sid, err := uuid.Parse(id); err == nil {
		var x stock.StockItem
		var product string
		if err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
			var err error
			if x, err = stock.StockItemTx(ctx, tx, tenant, sid); err != nil {
				return err
			}
			err = tx.QueryRow(ctx, `SELECT product_id FROM product_stock WHERE tenant_id = $1 AND stock_item_id = $2 ORDER BY created_at LIMIT 1`, tenant, sid).Scan(&product)
			if errors.Is(err, pgx.ErrNoRows) {
				return nil
			}
			return err
		}); errors.Is(err, stock.ErrNoItem) {
			return stock.Item{}, errNoItem
		} else if err != nil {
			return stock.Item{}, err
		}
		if product == "" {
			return stock.FromStockItem(x), nil
		}
		row = product
	}
	tr, err := s.tableRowsFor(ctx, tenant, sh.table, storefrontPurpose)
	if err != nil {
		return stock.Item{}, err
	}
	page, err := tr.call(ctx, s, "read_rows", map[string]any{"table": sh.table.Name, "limit": 1,
		"filters": []any{map[string]any{"column": "id", "op": "eq", "value": row}}}, "")
	if err != nil {
		return stock.Item{}, err
	}
	var rows []map[string]any
	for _, r := range rowsOf(page) {
		if m, ok := r.(map[string]any); ok {
			rows = append(rows, m)
		}
	}
	var items []stock.Item
	if err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		var err error
		items, err = stock.ItemsTx(ctx, tx, tenant, rows, sh.cols())
		return err
	}); err != nil {
		return stock.Item{}, err
	}
	for _, it := range items {
		if it.ID == row {
			return it, nil
		}
	}
	return stock.Item{}, errNoItem
}

// stockAudit records what a person did, in the transaction given.
type stockAudit func(tx pgx.Tx, action, target string, detail map[string]any) error

// stockSetup readies a business to keep stock (Keep my stock): a draft
// storefront under the business's name when there is none, and a
// products table when there is no catalogue. Counts are kept on stock
// items, so an existing catalogue needs nothing more.
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
	if sf.CatalogueTable != nil {
		return s.stockShopOf(ctx, tenant)
	}
	t := datatables.Table{Name: "products", Title: "Products", Description: "The shop's products", Columns: productColumns}
	if err := s.makeOwnTable(ctx, tenant, actor, audit, &t, "products", "shop_products", "products_2", "products_3"); err != nil {
		return stockShop{}, err
	}
	m := productMapping
	if err := inTx(func(tx pgx.Tx) error {
		if err := m.Check(tableColumns(t)); err != nil {
			return err
		}
		var err error
		if sf, err = storefront.Save(ctx, tx, tenant, sf.Slug, sf.Draft, &t.ID, m, actor); err != nil {
			return err
		}
		return audit(tx, "stock.setup", t.Name, map[string]any{"table_id": t.ID, "made": true})
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

// stockMove records a movement, its count and its audit entry, in one
// transaction.
func (s *Server) stockMove(ctx context.Context, tenant uuid.UUID, mv stock.Move, audit stockAudit) (stock.Movement, error) {
	var m stock.Movement
	err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		var err error
		if m, err = stock.RecordTx(ctx, tx, tenant, mv); err != nil {
			return err
		}
		detail := map[string]any{"item_id": mv.ItemID, "stock_item_id": m.ItemID, "reason": mv.Reason, "movement_id": m.ID}
		if mv.Delta != nil {
			detail["change"] = *mv.Delta
		}
		if mv.SetTo != nil {
			detail["count"] = *mv.SetTo
		}
		return audit(tx, "stock.move", mv.Item, detail)
	})
	return m, err
}

// supplierOfTx is the current supplier of a name, for a stock item's
// usual supplier: nil for "" (none). A name that is none of the business's
// suppliers is refused: they are added first, with how to reach them.
func supplierOfTx(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, name string) (*uuid.UUID, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, nil
	}
	var id uuid.UUID
	err := tx.QueryRow(ctx, `SELECT id FROM suppliers WHERE tenant_id = $1 AND retired_at IS NULL AND lower(name) = lower($2)`, tenant, name).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("%w: %s is not one of your suppliers yet: add them on the Suppliers page first", errBadRequest, name)
	}
	return &id, err
}

// stockDetails is what to change of an item's stock item: its unit, cost,
// levels and supplier (by name: "" for none). nil leaves each as it is.
type stockDetails struct {
	Unit     *string
	CostKobo *int64
	Reorder  *int64
	Target   *int64
	Supplier *string
	// Name and Category, for a material (a product's are its catalogue
	// row's), or to follow a product renamed.
	Name     *string
	Category *string
}

func (d stockDetails) any() bool {
	return d.Unit != nil || d.CostKobo != nil || d.Reorder != nil || d.Target != nil || d.Supplier != nil || d.Name != nil || d.Category != nil
}

// stockSetDetailsTx changes an item's stock item, in tx: a product not
// counted yet starts being counted by one of its name.
func stockSetDetailsTx(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, it stock.Item, d stockDetails, actor string) (uuid.UUID, error) {
	var sid uuid.UUID
	if it.StockItemID != "" {
		sid = uuid.MustParse(it.StockItemID)
	} else {
		var err error
		if sid, err = stock.StartCountingTx(ctx, tx, tenant, it.ID, it.Name, "", actor); err != nil {
			return sid, err
		}
	}
	set := stock.ItemSet{Name: d.Name, Unit: d.Unit, Category: d.Category, CostKobo: d.CostKobo, Low: d.Reorder, Target: d.Target}
	if d.Supplier != nil {
		id, err := supplierOfTx(ctx, tx, tenant, *d.Supplier)
		if err != nil {
			return sid, err
		}
		set.SupplierID, set.ClearSupplier = id, id == nil
	}
	_, err := stock.SetItemTx(ctx, tx, tenant, sid, set)
	return sid, err
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
	Category  string `json:"category"`
	// Material: something counted and not sold (what-a-business-buys
	// §4.5, "Do you sell it?" No): a stock item with no product, nothing
	// added to the catalogue.
	Material bool `json:"material"`
	// GroupOf is, for a size's or colour's stock item, its product.
	GroupOf string `json:"-"`
}

func (in *stockNew) check() error {
	var err error
	if in.Category, err = checkCategory(in.Category); err != nil {
		return err
	}
	in.Name, in.Unit, in.Supplier = strings.TrimSpace(in.Name), strings.TrimSpace(in.Unit), strings.TrimSpace(in.Supplier)
	switch {
	case in.Name == "" || len([]rune(in.Name)) > 120:
		return fmt.Errorf("%w: an item's name is 1 to 120 characters", errBadRequest)
	case in.Quantity < 0 || in.Quantity > stock.MaxDelta:
		return fmt.Errorf("%w: the quantity is 0 to %d", errBadRequest, stock.MaxDelta)
	case len([]rune(in.Unit)) > 30 || len([]rune(in.Supplier)) > 120:
		return fmt.Errorf("%w: the unit is up to 30 characters, the supplier up to 120", errBadRequest)
	}
	if in.Material && in.PriceKobo != nil {
		return fmt.Errorf("%w: something not sold has no selling price", errBadRequest)
	}
	for _, v := range []*int64{in.CostKobo, in.PriceKobo, in.Reorder, in.Target} {
		if v != nil && (*v < 0 || *v > 1e12) {
			return fmt.Errorf("%w: prices and levels are 0 or more", errBadRequest)
		}
	}
	return nil
}

// stockAdd adds a product to the catalogue, counted by a stock item of
// its own at 0, then records its quantity as received, so the count is
// explained from the start. A product not priced for sale is not shown on
// the page until it is.
func (s *Server) stockAdd(ctx context.Context, tenant uuid.UUID, actor, key string, in stockNew, audit stockAudit) (stock.Item, stock.Movement, error) {
	sh, items, err := s.stockItems(ctx, tenant)
	if err != nil {
		return stock.Item{}, stock.Movement{}, err
	}
	products := 0
	for _, it := range items {
		if !it.Material {
			products++
		}
		if matchKey(it.Name) == matchKey(in.Name) && in.Material {
			// This key made it (a retry): the same answer.
			if out, mv, err := s.stockAddMaterial(ctx, tenant, actor, key, in, audit, true); !errors.Is(err, errItemExists) {
				return out, mv, err
			}
			return it, stock.Movement{}, fmt.Errorf("%w: %s", errItemExists, it.Name)
		}
		if matchKey(it.Name) == matchKey(in.Name) {
			// This key was adding it (a retry after a failure part way):
			// finish, rather than refuse the item it made.
			mine, err := s.stockAdding(ctx, tenant, key, in.Name)
			if err != nil {
				return it, stock.Movement{}, err
			}
			if !mine || it.Material {
				return it, stock.Movement{}, fmt.Errorf("%w: %s", errItemExists, it.Name)
			}
			return s.stockAdded(ctx, tenant, actor, key, in, sh, it.ID, audit)
		}
	}
	if in.Material {
		return s.stockAddMaterial(ctx, tenant, actor, key, in, audit, false)
	}
	if products >= storefront.MaxItems {
		return stock.Item{}, stock.Movement{}, fmt.Errorf("%w: the catalogue holds at most %d items", errConflict, storefront.MaxItems)
	}
	if in.Category != "" {
		if sh, err = s.stockCategories(ctx, tenant, actor, audit, sh); err != nil {
			return stock.Item{}, stock.Movement{}, err
		}
	}
	m := sh.cols()
	row := map[string]any{m.Name: in.Name}
	if in.Category != "" {
		row[m.Category] = in.Category
	}
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
	// The supplier is one of the business's before the row is made.
	if in.Supplier != "" {
		if err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
			_, err := supplierOfTx(ctx, tx, tenant, in.Supplier)
			return err
		}); err != nil {
			return stock.Item{}, stock.Movement{}, err
		}
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

// stockAddMaterial makes a material: a stock item no product is counted
// by, with its details and its quantity as received, in one transaction;
// the same key again answers the one it made (replay: only that, else
// errItemExists). Nothing reaches the
// catalogue.
func (s *Server) stockAddMaterial(ctx context.Context, tenant uuid.UUID, actor, key string, in stockNew, audit stockAudit, replay bool) (stock.Item, stock.Movement, error) {
	var x stock.StockItem
	var mv stock.Movement
	err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		var made string
		err := tx.QueryRow(ctx, `SELECT detail->>'stock_item_id' FROM audit_log WHERE tenant_id = $1 AND action = 'stock.material_add' AND detail->>'key' = $2`,
			tenant, key).Scan(&made)
		switch {
		case err == nil:
			if x, err = stock.StockItemTx(ctx, tx, tenant, uuid.MustParse(made)); err != nil {
				return err
			}
		case errors.Is(err, pgx.ErrNoRows) && replay:
			return errItemExists
		case errors.Is(err, pgx.ErrNoRows):
			supplier, err := supplierOfTx(ctx, tx, tenant, in.Supplier)
			if err != nil {
				return err
			}
			if x, err = stock.CreateItemTx(ctx, tx, tenant, stock.NewItem{Name: in.Name, Unit: in.Unit, Category: in.Category, CostKobo: in.CostKobo,
				Low: in.Reorder, Target: in.Target, SupplierID: supplier, Actor: actor}); err != nil {
				return err
			}
			if err := audit(tx, "stock.material_add", x.Name, map[string]any{"key": key, "stock_item_id": x.ID}); err != nil {
				return err
			}
		default:
			return err
		}
		if in.Quantity <= 0 {
			return nil
		}
		q := in.Quantity
		mv, err = stock.RecordTx(ctx, tx, tenant, stock.Move{ItemID: x.ID.String(), Item: x.Name, Reason: stock.Received, Delta: &q, Actor: actor,
			Key: key + "-received", CostKobo: in.CostKobo})
		if err != nil {
			return err
		}
		x, err = stock.StockItemTx(ctx, tx, tenant, x.ID)
		return err
	})
	return stock.FromStockItem(x), mv, err
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

// stockAdded finishes adding product id, whose row is in the table: its
// audit entry, the stock item it is counted by with its details, and its
// quantity as received, in one transaction, once; then reads the item.
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
			d := stockDetails{Unit: &in.Unit, CostKobo: in.CostKobo, Reorder: in.Reorder, Target: in.Target, Supplier: &in.Supplier}
			sid, err := stockSetDetailsTx(ctx, tx, tenant, stock.Item{ID: id, Name: in.Name}, d, actor)
			if err != nil {
				return err
			}
			if in.GroupOf != "" {
				if _, err := tx.Exec(ctx, `UPDATE stock_items SET group_of = $3 WHERE tenant_id = $1 AND id = $2`, tenant, sid, in.GroupOf); err != nil {
					return err
				}
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
	if err := s.SyncStorefront(ctx, tenant); err != nil {
		s.Logger.Warn("stock: refreshing the catalogue", "tenant", tenant, "err", err)
	}
	it, err := s.stockItem(ctx, tenant, sh, id)
	return it, mv, err
}

// stockSetCells writes a product's columns of the catalogue (its name,
// price, whether it is shown, its category, its sizes).
func (s *Server) stockSetCells(ctx context.Context, tenant uuid.UUID, sh stockShop, id, key string, set map[string]any) error {
	if len(set) == 0 {
		return nil
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
	// Photo is, on a size or colour, its own photo (a colour's), read
	// from the table, so the owner sees it on a product not on the page.
	Photo storefront.Image `json:"photo,omitempty"`
}

// getStock is GET /v1/stock: whether the business keeps stock (has a
// catalogue), and its items with their stock (products and materials),
// with how many are low and what the stock is worth at cost.
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
	photos := map[string]storefront.Image{}
	if err := s.tx(r, func(tx pgx.Tx) error {
		for _, it := range items {
			if it.VariantOf == "" || it.PhotoID == "" {
				continue
			}
			img, err := storefront.MediaImage(r.Context(), tx, p.TenantID, it.PhotoID)
			if errors.Is(err, storefront.ErrInvalid) {
				continue
			}
			if err != nil {
				return err
			}
			photos[it.ID] = img
		}
		return nil
	}); err != nil {
		s.fail(w, r, err)
		return
	}
	views := make([]stockItemView, len(items))
	low := 0
	for i, it := range items {
		views[i] = stockItemView{Item: it, Low: it.Low(), Suggest: it.Suggest(), Photo: photos[it.ID]}
		if it.Low() {
			low++
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"keeps_stock": true, "catalogue": sh.table.Name, "columns": sh.cols(), "items": views,
		"low": low, "value_kobo": stock.ValueKobo(items), "published": sh.sf.PublishedVersion > 0})
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
		writeErr(w, http.StatusBadRequest, "reason is order, sale, received, adjustment, count or moved")
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
	case stock.Order, stock.Sale, stock.Received, stock.Adjustment, stock.Count, stock.Moved:
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
	line := "New product: " + it.Name
	if it.Material {
		line = "Now counting: " + it.Name
	}
	if it.PriceKobo != nil {
		line += ", " + naira(*it.PriceKobo)
		if it.Unit != "" {
			line += " a " + it.Unit
		}
	}
	s.diaryChange(r.Context(), principalFrom(r.Context()), line)
	writeJSON(w, http.StatusCreated, out)
}

// patchStockItem is PATCH /v1/stock/items/{id}: an item's name, prices,
// unit, levels, supplier, or whether it is shown. Its stock changes by movements only.
func (s *Server) patchStockItem(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Name      *string `json:"name"`
		Unit      *string `json:"unit"`
		CostKobo  *int64  `json:"cost_kobo"`
		PriceKobo *int64  `json:"price_kobo"`
		Reorder   *int64  `json:"reorder_level"`
		Target    *int64  `json:"target_level"`
		Supplier  *string `json:"supplier"`
		// Category is the kind of thing it is ("Sneakers"; "" for none):
		// the catalogue gets a category column the first time one is set.
		Category *string `json:"category"`
		// Available shows or hides it on the storefront and the price
		// lists it is on (chat tools' product form).
		Available *bool `json:"available"`
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
	id := chi.URLParam(r, "id")
	// What it was: for which record each change goes to, and the diary
	// line (chat_changes.go).
	before, err := s.stockItem(r.Context(), p.TenantID, sh, id)
	if err != nil {
		s.stockFail(w, r, err)
		return
	}
	id = before.ID
	if before.Material && (in.PriceKobo != nil || in.Available != nil) {
		s.fail(w, r, fmt.Errorf("%w: %s is not sold, so it has no price and is not on your storefront", errBadRequest, before.Name))
		return
	}
	if in.Category != nil {
		c, err := checkCategory(*in.Category)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		in.Category = &c
		if before.Material {
			// A material's category is its stock item's.
		} else if c != "" {
			audit := func(tx pgx.Tx, action, target string, detail map[string]any) error {
				return auditTx(r, tx, action, target, detail)
			}
			if sh, err = s.stockCategories(r.Context(), p.TenantID, p.Actor(), audit, sh); err != nil {
				s.stockFail(w, r, err)
				return
			}
		} else if sh.cols().Category == "" {
			in.Category = nil // none to clear
		}
	}
	m := sh.cols()
	// A product's own columns go to its catalogue row; what is counted
	// (unit, cost, levels, supplier), and a material's name and category,
	// to its stock item.
	set := map[string]any{}
	var d stockDetails
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
			if before.StockItemID != "" {
				d.Name = &n // its stock item follows its name
			}
			if before.Material {
				return nil
			}
			return put(m.Name, "name", n)
		},
		func() error {
			if in.Unit == nil {
				return nil
			}
			u := strings.TrimSpace(*in.Unit)
			if len([]rune(u)) > 30 {
				return fmt.Errorf("%w: the unit is up to 30 characters", errBadRequest)
			}
			d.Unit = &u
			return nil
		},
		func() error {
			if in.Supplier != nil {
				v := strings.TrimSpace(*in.Supplier)
				d.Supplier = &v
			}
			return nil
		},
		func() error {
			d.CostKobo, d.Reorder, d.Target = in.CostKobo, in.Reorder, in.Target
			return nil
		},
		func() error {
			if in.PriceKobo == nil {
				return nil
			}
			return put(m.Price, "price", priceCell(*in.PriceKobo, m))
		},
		func() error {
			if in.Available == nil {
				return nil
			}
			return put(m.Available, "available", *in.Available)
		},
		func() error {
			if in.Category == nil {
				return nil
			}
			if before.Material {
				d.Category = in.Category
				return nil
			}
			return put(m.Category, "category", *in.Category)
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
	if !before.Material {
		if err := s.stockSetCells(r.Context(), p.TenantID, sh, id, key, set); err != nil {
			s.stockFail(w, r, err)
			return
		}
	}
	if err := s.tx(r, func(tx pgx.Tx) error {
		if d.any() {
			sid, err := stockSetDetailsTx(r.Context(), tx, p.TenantID, before, d, p.Actor())
			if err != nil {
				return err
			}
			if err := auditTx(r, tx, "stock.item_change", before.Name, map[string]any{"stock_item_id": sid}); err != nil {
				return err
			}
		}
		if len(set) == 0 {
			return nil
		}
		cols := make([]string, 0, len(set))
		for c := range set {
			cols = append(cols, c)
		}
		return auditTx(r, tx, "storefront.item_change", sh.table.Name, map[string]any{"table_id": sh.table.ID, "row_id": id, "columns": cols})
	}); err != nil {
		s.stockFail(w, r, err)
		return
	}
	if err := s.SyncStorefront(r.Context(), p.TenantID); err != nil {
		s.Logger.Warn("stock: refreshing the catalogue", "tenant", p.TenantID, "err", err)
	}
	// Its sizes and colours follow it: name, details, and the price of
	// those without their own.
	newName := ""
	if n, ok := set[m.Name].(string); ok {
		newName = n
	}
	recategorised := s.sizesFollow(r.Context(), p.TenantID, sh, before, newName, key, set, d)
	if before.HasSizes() && newName != "" && newName != before.Name {
		s.refreshListSizes(r.Context(), p.TenantID, id)
	}
	if in.PriceKobo != nil && before.VariantOf != "" {
		s.markOwnPrice(r.Context(), p.TenantID, sh, before)
	}
	// Buyers see it under its new category too.
	if in.Category != nil && (*in.Category != before.Category || len(recategorised) > 0) {
		var due []uuid.UUID
		if err := s.tx(r, func(tx pgx.Tx) error {
			for _, item := range append([]string{id}, recategorised...) {
				d, err := links.SetItemCategoryTx(r.Context(), tx, p.TenantID, item, *in.Category)
				if err != nil {
					return err
				}
				due = append(due, d...)
			}
			return nil
		}); err != nil {
			s.Logger.Warn("stock: a category on the price lists", "tenant", p.TenantID, "err", err)
		}
		s.mirrorPrices(r.Context(), p.TenantID, due)
	}
	it, err := s.stockItem(r.Context(), p.TenantID, sh, id)
	if err != nil {
		s.stockFail(w, r, err)
		return
	}
	var lines []string
	if in.Name != nil && before.Name != "" && before.Name != it.Name {
		lines = append(lines, fmt.Sprintf("Renamed %s to %s", before.Name, it.Name))
	}
	if in.PriceKobo != nil && (before.PriceKobo == nil || *before.PriceKobo != *in.PriceKobo) {
		lines = append(lines, priceChangeWords(it.Name, before.PriceKobo, *in.PriceKobo))
	}
	if in.Category != nil && before.Category != it.Category {
		if it.Category == "" {
			lines = append(lines, it.Name+" is no longer in a category")
		} else {
			lines = append(lines, "Category of "+it.Name+": "+it.Category)
		}
	}
	if in.Available != nil && before.Available != *in.Available {
		if *in.Available {
			lines = append(lines, it.Name+" is shown on your storefront again")
		} else {
			lines = append(lines, it.Name+" is hidden from your storefront")
		}
	}
	s.diaryChange(r.Context(), p, lines...)
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
	s.diaryChange(r.Context(), p, moveWords(it, in.Reason, in.Change, in.Count))
	writeJSON(w, http.StatusOK, map[string]any{"movement": m})
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
		writeJSON(w, http.StatusConflict, map[string]any{"error": "Your stock is not kept on Taskiem yet. Turn it on with Keep my stock: Storefront, then Stock.", "code": "stock_off"})
	case errors.Is(err, errNoItem):
		writeErr(w, http.StatusNotFound, "no such item")
	case errors.Is(err, errNoVariantColumns):
		writeJSON(w, http.StatusConflict, map[string]any{"error": "Your products table has no room for sizes and colours. Remove a column you do not use on the Tables page, then try again.",
			"code": "no_variant_columns"})
	case errors.Is(err, errCategoryColumnLimit):
		writeJSON(w, http.StatusConflict, map[string]any{"error": "Your products table has as many columns as your plan allows: remove one you do not use on the Tables page, then set the category again.",
			"code": "no_category_column"})
	case errors.Is(err, errNoCategoryColumn):
		writeJSON(w, http.StatusConflict, map[string]any{"error": "Add a category column to use categories: on the Tables page, open your products table and add a text column called category.", "code": "no_category_column"})
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

// moveWords is a stock movement as a diary line: "Stock in: Rice +20 bags
// (now 24 bags)", "Stock of Rice counted: 4 bags → 6 bags".
func moveWords(it stock.Item, reason string, change, count *int64) string {
	switch {
	case reason == stock.Received && change != nil:
		line := fmt.Sprintf("Stock in: %s +%s", it.Name, qty(*change, it.Unit))
		if it.Stock != nil {
			line += fmt.Sprintf(" (now %s)", qty(*it.Stock+*change, it.Unit))
		}
		return line
	case reason == stock.Count && count != nil:
		was := "no count"
		if it.Stock != nil {
			was = qty(*it.Stock, it.Unit)
		}
		return fmt.Sprintf("Stock of %s counted: %s → %s", it.Name, was, qty(*count, it.Unit))
	case change != nil:
		sign := "+"
		if *change < 0 {
			sign = ""
		}
		return fmt.Sprintf("Stock of %s: %s%s", it.Name, sign, qty(*change, it.Unit))
	}
	return ""
}
