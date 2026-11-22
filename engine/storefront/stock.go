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
// (docs/spec/what-a-business-buys.md §4, decision 0070): the catalogue
// holds no count. A product is counted by a stock item (product_stock),
// and how many are left of it is that stock item's count, read through
// engine/productstock into the catalogue copy when it is refreshed and
// whenever a movement changes it (CopyCount).

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

// CopyCount puts how many are left of a product into the catalogue copy,
// so the page and forms show it at once rather than at the next refresh;
// a size's or colour's into its product's entry too (CopySizeCount).
// Stock is kept on the catalogue's table only (fronts §5.2): a front's
// list from another table may share a row's id.
func CopyCount(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, row string, n int64) error {
	_, err := tx.Exec(ctx, `UPDATE storefront_catalogue c SET item = jsonb_set(item, '{stock}', to_jsonb(GREATEST($3::bigint, 0)))
		WHERE c.tenant_id = $1 AND c.row_id = $2 AND c.table_name = COALESCE((SELECT d.name FROM storefronts s JOIN data_tables d ON d.id = s.catalogue_table WHERE s.id = c.storefront_id), '')`, tenant, row, n)
	if err != nil {
		return err
	}
	return CopySizeCount(ctx, tx, tenant, row, n)
}

// CopySizeCount puts a size's or colour's new count into its product's
// entry in the catalogue copy (the sizes are carried on the product, not
// as entries of their own), and the product's count, theirs together
// (unknown when a size keeps no count, as rollUpVariants has it).
// Like the item's own count, only on the catalogue's table: a front's list
// from another table may carry a size of the same row id.
func CopySizeCount(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, row string, n int64) error {
	_, err := tx.Exec(ctx, `UPDATE storefront_catalogue c SET item = jsonb_set(x.item, '{stock}',
			COALESCE((SELECT CASE WHEN bool_and(z ? 'stock') THEN to_jsonb(sum((z->>'stock')::bigint)) END FROM jsonb_array_elements(x.item->'sizes') z), 'null'::jsonb))
		FROM (SELECT c2.storefront_id, c2.table_name, c2.row_id, jsonb_set(c2.item, '{sizes}', (SELECT jsonb_agg(CASE WHEN z->>'id' = $2 THEN jsonb_set(z, '{stock}', to_jsonb(GREATEST($3::bigint, 0))) ELSE z END ORDER BY o)
				FROM jsonb_array_elements(c2.item->'sizes') WITH ORDINALITY AS a(z, o))) AS item
			FROM storefront_catalogue c2 WHERE c2.tenant_id = $1 AND c2.item->'sizes' @> jsonb_build_array(jsonb_build_object('id', $2::text))
				AND c2.table_name = COALESCE((SELECT d.name FROM storefronts s JOIN data_tables d ON d.id = s.catalogue_table WHERE s.id = c2.storefront_id), '')) x
		WHERE c.storefront_id = x.storefront_id AND c.table_name = x.table_name AND c.row_id = x.row_id`, tenant, row, n)
	return err
}

// counted puts on each item counted by a stock item how many are left of
// it and the stock item's unit (productstock), before sizes are rolled up
// into their products. Items not counted keep no count.
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
			items[i].Stock, items[i].Unit = &n, c.Unit
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
