package storefront

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/israel-duff/taskiem/engine/connector"
	"github.com/israel-duff/taskiem/engine/datatables"
	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/egress"
	"github.com/israel-duff/taskiem/engine/secrets"
)

// Stock reads and changes the stock column of a tenant's catalogue table,
// at Xenibase (the live table, not the copy).
type Stock interface {
	// Read returns a row's stock. tracked is false when the catalogue maps
	// no stock column, or the row is gone or holds no count.
	Read(ctx context.Context, tenant uuid.UUID, row string) (n int64, tracked bool, err error)
	// Change sets a row's stock to to only while it is still from; applied
	// is false when it had moved. Sending the same key again replays the
	// first answer rather than changing it twice.
	Change(ctx context.Context, tenant uuid.UUID, row string, from, to int64, key string) (applied bool, err error)
}

// Mover records an accepted order's lines as stock movements (reason
// order) and sends them to the table, answering the items there were not
// enough of (engine/stock). Lines of one row are one movement.
type Mover interface {
	OrderLines(ctx context.Context, tenant, order uuid.UUID, reference string, lines []Line) (short []string, err error)
}

// XenibaseStock is Stock over the tenant's data tables connection,
// through the connector and the egress guard, so any role can use it.
type XenibaseStock struct {
	Pool     *pgxpool.Pool
	Registry *connector.Registry
	Vault    *secrets.Vault
	Guard    *egress.Guard
}

// target finds the catalogue table and its stock column; column is empty
// when stock is not tracked.
func (x *XenibaseStock) target(ctx context.Context, tenant uuid.UUID) (table, column string, err error) {
	err = db.InTenantTx(ctx, x.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		sf, err := Get(ctx, tx, tenant)
		if err != nil || sf.CatalogueTable == nil || sf.CatalogueColumns.Stock == "" {
			return err
		}
		t, err := datatables.GetTable(ctx, tx, *sf.CatalogueTable)
		if err != nil {
			return err
		}
		if err := datatables.Writable(t); err != nil {
			return err // a kept table is never a catalogue (api/storefront.go): never write one
		}
		table, column = t.Name, sf.CatalogueColumns.Stock
		return nil
	})
	return table, column, err
}

func (x *XenibaseStock) call(ctx context.Context, tenant uuid.UUID, action string, in map[string]any, key string) (map[string]any, error) {
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
func (x *XenibaseStock) catalogue(ctx context.Context, tenant uuid.UUID) (table string, m Columns, err error) {
	err = db.InTenantTx(ctx, x.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		sf, err := Get(ctx, tx, tenant)
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
func (x *XenibaseStock) Row(ctx context.Context, tenant uuid.UUID, id string) ([]map[string]any, Columns, error) {
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
// 1), with its mapping: what stock reads items from, outside the API.
func (x *XenibaseStock) Rows(ctx context.Context, tenant uuid.UUID) ([]map[string]any, Columns, error) {
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

// Tracks reports whether the tenant's catalogue keeps stock: a catalogue
// table mapped with a stock column.
func (x *XenibaseStock) Tracks(ctx context.Context, tenant uuid.UUID) (bool, error) {
	_, col, err := x.target(ctx, tenant)
	if errors.Is(err, ErrNotFound) {
		return false, nil
	}
	return col != "", err
}

func (x *XenibaseStock) Read(ctx context.Context, tenant uuid.UUID, row string) (int64, bool, error) {
	table, col, err := x.target(ctx, tenant)
	if err != nil || col == "" {
		return 0, false, err
	}
	page, err := x.call(ctx, tenant, "read_rows", map[string]any{"table": table, "limit": 1, "select": "id," + col,
		"filters": []any{map[string]any{"column": "id", "op": "eq", "value": row}}}, "")
	if err != nil {
		return 0, false, err
	}
	rows, _ := page["rows"].([]any)
	if len(rows) == 0 {
		return 0, false, nil
	}
	m, _ := rows[0].(map[string]any)
	n, ok := num(m[col])
	return int64(n), ok, nil
}

func (x *XenibaseStock) Change(ctx context.Context, tenant uuid.UUID, row string, from, to int64, key string) (bool, error) {
	table, col, err := x.target(ctx, tenant)
	if err != nil || col == "" {
		return false, err
	}
	out, err := x.call(ctx, tenant, "update_rows", map[string]any{"table": table, "set": map[string]any{col: to},
		"max_affected": 1, "return": "minimal", "filters": []any{
			map[string]any{"column": "id", "op": "eq", "value": row},
			map[string]any{"column": col, "op": "eq", "value": from},
		}}, key)
	if err != nil {
		return false, err
	}
	n, _ := num(out["affected"])
	return n > 0, nil
}

// stockStep is one catalogue row's change for an order, recorded on the
// order before it is sent.
type stockStep struct {
	From      int64 `json:"from"`
	To        int64 `json:"to"`
	Done      bool  `json:"done"`
	Short     bool  `json:"short,omitempty"`
	Untracked bool  `json:"untracked,omitempty"`
}

// stockAttempts bounds how often a row is read again when other orders
// keep moving its stock.
const stockAttempts = 5

// lowerStock takes an accepted order's quantities off the stock, never
// below zero, and returns the items there were not enough of. Each change
// is recorded on the order before it is sent, so a retried step sends the
// same change with the same key: it lands once.
func (o *Orders) lowerStock(ctx context.Context, tenant uuid.UUID, ord Order) ([]string, error) {
	short := []string{}
	if o.Stock == nil || ord.Kind != "order" || len(ord.Basket) == 0 {
		return short, nil
	}
	var rec map[string]stockStep
	err := db.InTenantTx(ctx, o.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		var raw []byte
		if err := tx.QueryRow(ctx, `SELECT stock FROM storefront_orders WHERE tenant_id = $1 AND id = $2`, tenant, ord.ID).Scan(&raw); err != nil {
			return err
		}
		if raw != nil {
			return json.Unmarshal(raw, &rec)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if rec == nil {
		rec = map[string]stockStep{}
	}
	if o.Moves != nil {
		// Lines lowered before stock movements (recorded on the order) stay
		// lowered; the rest are movements.
		var lines []Line
		for _, l := range ord.Basket {
			if st, ok := rec[l.ID]; ok && st.Done {
				if st.Short {
					short = append(short, l.Name)
				}
				continue
			}
			lines = append(lines, l)
		}
		if len(lines) == 0 {
			return short, nil
		}
		more, err := o.Moves.OrderLines(ctx, tenant, ord.ID, ord.Reference, lines)
		if err != nil {
			return nil, err
		}
		return append(short, more...), nil
	}
	save := func() error {
		raw, _ := json.Marshal(rec)
		return db.InTenantTx(ctx, o.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
			_, err := tx.Exec(ctx, `UPDATE storefront_orders SET stock = $3, updated_at = now() WHERE tenant_id = $1 AND id = $2`, tenant, ord.ID, raw)
			return err
		})
	}
	for _, l := range ord.Basket {
		for attempt := 0; ; attempt++ {
			st, ok := rec[l.ID]
			if ok && st.Done {
				break
			}
			if attempt == stockAttempts {
				return nil, fmt.Errorf("the stock of %s kept changing; try again", l.Name)
			}
			if !ok {
				n, tracked, err := o.Stock.Read(ctx, tenant, l.ID)
				if err != nil {
					return nil, err
				}
				st = stockStep{From: n, To: max(0, n-int64(l.Quantity)), Short: n < int64(l.Quantity)}
				if !tracked {
					st = stockStep{Done: true, Untracked: true}
				}
				rec[l.ID] = st
				if err := save(); err != nil {
					return nil, err
				}
				if !tracked {
					break
				}
			}
			applied, err := o.Stock.Change(ctx, tenant, l.ID, st.From, st.To, stockKey(ord.ID, l.ID, st.From))
			if err != nil {
				return nil, err
			}
			if applied {
				st.Done = true
				rec[l.ID] = st
				if err := o.copyStock(ctx, tenant, ord.storefront, l.ID, st.To); err != nil {
					return nil, err
				}
			} else {
				delete(rec, l.ID) // another order moved it first: read it again
			}
			if err := save(); err != nil {
				return nil, err
			}
			if applied {
				break
			}
		}
		if rec[l.ID].Short {
			short = append(short, l.Name)
		}
	}
	return short, nil
}

// copyStock puts a row's new stock into the catalogue copy, so the page
// and forms show it at once rather than at the next refresh.
func (o *Orders) copyStock(ctx context.Context, tenant, sf uuid.UUID, row string, n int64) error {
	return db.InTenantTx(ctx, o.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `UPDATE storefront_catalogue SET item = jsonb_set(item, '{stock}', to_jsonb($4::bigint))
			WHERE tenant_id = $1 AND storefront_id = $2 AND row_id = $3`, tenant, sf, row, n)
		return err
	})
}

// stockKey is the Idempotency-Key of one change: the order, the row and
// the count it changes from.
func stockKey(order uuid.UUID, row string, from int64) string {
	h := sha256.Sum256([]byte(order.String() + "\x00" + row + "\x00" + strconv.FormatInt(from, 10)))
	return "tsk-sf-" + hex.EncodeToString(h[:16])
}
