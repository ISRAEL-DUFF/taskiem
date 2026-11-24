package storefront

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/israel-duff/taskiem/engine/connector"
	"github.com/israel-duff/taskiem/engine/datatables"
	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/egress"
	"github.com/israel-duff/taskiem/engine/productstock"
	"github.com/israel-duff/taskiem/engine/secrets"
)

// Stock is kept on stock items apart from the catalogue
// (docs/spec/what-a-business-buys.md §4, decision 0070): neither the
// catalogue table nor its copy holds a count. A product is counted by a
// stock item (product_stock), and how many are left of it is how many
// whole sales that stock item covers (its count over the product's uses).
// It is read when the copy is read: by the business through
// engine/productstock (Catalogue, CatalogueItems), by the public page and
// a linked buyer through taskiem_product_left (WithLeft), so a sale
// changes no copy.

// Mover records an accepted order's lines as stock movements (reason
// order), answering the items there were not enough of (engine/stock).
// Lines of one row are one movement.
type Mover interface {
	OrderLines(ctx context.Context, tenant, order uuid.UUID, reference string, lines []Line) (short []string, err error)
}

// Returner puts back the stock an order took when it ends with its goods
// still on the shelf: declined, cancelled, left refund due or not paid
// for in time (engine/stock), in the transaction that ends the order, once
// however often it is asked.
type Returner interface {
	ReturnOrderTx(ctx context.Context, tx pgx.Tx, tenant, order uuid.UUID, reference string) error
}

// returnStockTx puts an ended order's stock back, in the transaction that
// ended it.
func (o *Orders) returnStockTx(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, ord Order) error {
	r, ok := o.Moves.(Returner)
	if !ok || (ord.Kind != "order" && ord.Kind != "trade" && ord.Kind != "chat" && ord.Kind != "app") {
		return nil
	}
	return r.ReturnOrderTx(ctx, tx, tenant, ord.ID, ord.Reference)
}

// lowerStock takes an accepted order's quantities off the stock items of
// its products, and returns the items there were not enough of. Each is a
// movement keyed on the order and the row: a retried step lowers once.
func (o *Orders) lowerStock(ctx context.Context, tenant uuid.UUID, ord Order) ([]string, error) {
	short := []string{}
	if ord.Kind == "trade" {
		// A linked business's order (business links §7): its lines matched
		// to the catalogue's items are lowered, the rest are not ours.
		var mine []Line
		for _, l := range ord.Basket {
			if l.ID != "" {
				mine = append(mine, l)
			}
		}
		ord.Basket = mine
	}
	if o.Moves == nil || (ord.Kind != "order" && ord.Kind != "trade" && ord.Kind != "chat" && ord.Kind != "app") || len(ord.Basket) == 0 {
		return short, nil
	}
	more, err := o.Moves.OrderLines(ctx, tenant, ord.ID, ord.Reference, ord.Basket)
	if err != nil {
		return nil, err
	}
	return append(short, more...), nil
}

// LeftIDs is the ids how many are left is asked for of items: each item's,
// and each of its sizes'.
func LeftIDs(items []Item) []string {
	ids := make([]string, 0, len(items))
	for _, it := range items {
		ids = append(ids, it.ID)
		for _, z := range it.Sizes {
			ids = append(ids, z.ID)
		}
	}
	return ids
}

// WithLeft puts on items how many are left of each, by id (what
// productstock.LeftTx or taskiem_product_left answers): an item's own, and
// a product sold in sizes and colours each size's and theirs together
// (unknown when a size is not counted, as rollUpVariants has it). An item
// not in left is not counted, and keeps no count.
func WithLeft(items []Item, left map[string]int64) {
	for i := range items {
		it := &items[i]
		it.Stock = nil
		if len(it.Sizes) == 0 {
			if n, ok := left[it.ID]; ok {
				it.Stock = &n
			}
			continue
		}
		var sum int64
		all := true
		for j := range it.Sizes {
			z := &it.Sizes[j]
			z.Stock = nil
			n, ok := left[z.ID]
			if !ok {
				all = false
				continue
			}
			z.Stock = &n
			sum += n
		}
		if all {
			it.Stock = &sum
		}
	}
}

// withoutCounts is items as the copy keeps them: with no count.
func withoutCounts(items []Item) []Item {
	out := make([]Item, len(items))
	for i, it := range items {
		it.Stock = nil
		if len(it.Sizes) > 0 {
			sizes := make([]Size, len(it.Sizes))
			for j, z := range it.Sizes {
				z.Stock = nil
				sizes[j] = z
			}
			it.Sizes = sizes
		}
		out[i] = it
	}
	return out
}

// LeftTx puts on the business's items how many are left of each, read in
// its scope (productstock.LeftTx).
func LeftTx(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, items []Item) error {
	left, err := productstock.LeftTx(ctx, tx, tenant, LeftIDs(items))
	if err != nil {
		return err
	}
	WithLeft(items, left)
	return nil
}

// counted puts on each item counted by a stock item how many are left of
// it (productstock), before sizes are rolled up into their products, and
// the stock item's unit where one sale uses one of it. Items not counted
// keep no count.
func counted(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, items []Item) error {
	ids := make([]string, len(items))
	for i, it := range items {
		ids[i] = it.ID
	}
	counts, err := productstock.CountsTx(ctx, tx, tenant, ids)
	if err != nil {
		return err
	}
	for i := range items {
		if c, ok := counts[items[i].ID]; ok {
			n := c.Left()
			items[i].Stock = &n
			if c.Uses <= 1 {
				items[i].Unit = c.Unit
			}
		}
	}
	return nil
}

// XenibaseCatalogue reads a tenant's catalogue table at Xenibase (the
// live table, not the copy), through the connector and the egress guard,
// so any role can use it: what stock reads its products from outside the
// API (engine/stock), and the live price check (Live).
type XenibaseCatalogue struct {
	Pool     *pgxpool.Pool
	Registry *connector.Registry
	Vault    *secrets.Vault
	Guard    *egress.Guard
}

func (x *XenibaseCatalogue) call(ctx context.Context, tenant uuid.UUID, action string, in map[string]any, key string) (map[string]any, error) {
	reg, err := x.Registry.For(ctx, tenant.String())
	if err != nil {
		return nil, err
	}
	conn, ok := reg.Get(datatables.Connector + "@1")
	if !ok {
		return nil, errors.New("storefront: the Xenibase connector is not installed")
	}
	creds, err := x.Vault.Credentials(secrets.WithUse(ctx, secrets.Use{Kind: secrets.KindConnection, Purpose: "storefront.stock"}),
		tenant, datatables.Environment, conn.Manifest.ID, datatables.ConnectionName)
	if err != nil {
		return nil, err
	}
	g := x.Guard
	if g == nil {
		g = &egress.Guard{}
	}
	hc := g.Client(egress.Policy{Tenant: tenant.String(), Hosts: conn.Manifest.HostsFor(creds), Purpose: "connector:" + conn.Manifest.ID}, 30*time.Second)
	req := connector.Request{Input: in, Credentials: creds, HTTP: hc, Attempt: 1}
	if key != "" {
		req.IdempotencyKey, req.KeyFirstSent = key, time.Now()
	}
	resp, err := conn.Actions[action].Execute(ctx, req)
	if err != nil {
		return nil, err
	}
	if x.Pool != nil {
		// An open page over the catalogue is told (pages §9); failing
		// that, it is pulled to refresh.
		_ = datatables.MarkStepRowsChanged(ctx, x.Pool, tenant, action, in)
	}
	out, _ := resp.Output.(map[string]any)
	return out, nil
}

// catalogue finds the tenant's catalogue table and its mapping; table is
// empty when there is none.
func (x *XenibaseCatalogue) catalogue(ctx context.Context, tenant uuid.UUID) (table string, m Columns, err error) {
	err = db.InTenantTx(ctx, x.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		sf, err := Get(ctx, tx, tenant)
		if errors.Is(err, ErrNotFound) {
			return nil
		}
		if err != nil || sf.CatalogueTable == nil {
			return err
		}
		t, err := datatables.GetTable(ctx, tx, *sf.CatalogueTable)
		if err != nil {
			return err
		}
		table, m = t.Name, sf.CatalogueColumns
		return nil
	})
	return table, m, err
}

// Row reads one row of the tenant's catalogue table by its id (none when
// it is gone), with its mapping.
func (x *XenibaseCatalogue) Row(ctx context.Context, tenant uuid.UUID, id string) ([]map[string]any, Columns, error) {
	table, m, err := x.catalogue(ctx, tenant)
	if err != nil || table == "" {
		return nil, m, err
	}
	page, err := x.call(ctx, tenant, "read_rows", map[string]any{"table": table, "limit": 1,
		"filters": []any{map[string]any{"column": "id", "op": "eq", "value": id}}}, "")
	if err != nil {
		return nil, m, err
	}
	var out []map[string]any
	rows, _ := page["rows"].([]any)
	for _, r := range rows {
		if row, ok := r.(map[string]any); ok {
			out = append(out, row)
		}
	}
	return out, m, nil
}

// Rows reads every row of the tenant's catalogue table (up to MaxItems +
// 1), with its mapping: what stock reads products from, outside the API.
func (x *XenibaseCatalogue) Rows(ctx context.Context, tenant uuid.UUID) ([]map[string]any, Columns, error) {
	table, m, err := x.catalogue(ctx, tenant)
	if err != nil || table == "" {
		return nil, m, err
	}
	var out []map[string]any
	cursor := ""
	for len(out) <= MaxItems {
		in := map[string]any{"table": table, "limit": min(200, MaxItems+1-len(out)), "order": []any{map[string]any{"column": "id"}}}
		if cursor != "" {
			in["cursor"] = cursor
		}
		page, err := x.call(ctx, tenant, "read_rows", in, "")
		if err != nil {
			return nil, m, err
		}
		rows, _ := page["rows"].([]any)
		for _, r := range rows {
			if row, ok := r.(map[string]any); ok {
				out = append(out, row)
			}
		}
		next, _ := page["next_cursor"].(string)
		if next == "" || len(rows) == 0 {
			break
		}
		cursor = next
	}
	return out, m, nil
}

// PhoneIndexer writes an order's phone to the phone index, in the order's
// transaction (engine/phoneindex.Index).
type PhoneIndexer interface {
	WriteTx(ctx context.Context, tx pgx.Tx, tenant, order uuid.UUID, phone string) error
}
