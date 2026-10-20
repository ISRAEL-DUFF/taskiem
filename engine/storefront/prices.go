package storefront

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/datatables"
	"github.com/israel-duff/taskiem/engine/db"
)

// The price check (docs/spec/storefronts.md 8.1): forms price from the
// copy, which may be up to a refresh behind the table. Before an order's
// or booking's run starts, its lines are priced again from the table; when
// a price moved or an item went, the order waits (price_changed) for the
// customer to confirm the new total from their status link.

// Live reads rows of a tenant's catalogue table (services false) or
// services table at Xenibase, mapped to items by id. A row gone is absent.
type Live interface {
	Items(ctx context.Context, tenant uuid.UUID, services bool, ids []string) (map[string]Item, error)
}

// PriceChange is an order at the table's prices: Gone names what can no
// longer be had.
type PriceChange struct {
	Basket      []Line   `json:"basket"`
	TotalKobo   int64    `json:"total_kobo"`
	DepositKobo int64    `json:"deposit_kobo,omitempty"`
	Gone        []string `json:"gone,omitempty"`
}

// PriceCheckGrace is how long a submission waits for the table to answer
// before it goes ahead at the copy's prices, with a note: an unreachable
// table must not lose the order.
const PriceCheckGrace = 10 * time.Minute

// reprice prices a submission from live rows; changed is false when
// everything is as the customer saw it.
func reprice(sub Submission, live map[string]Item, opts ActionOptions) (PriceChange, bool) {
	var pc PriceChange
	changed := false
	if sub.Booking != nil {
		it, ok := live[sub.Booking.ServiceID]
		if !ok || !it.Available {
			pc.Gone = []string{sub.Booking.ServiceName}
			return pc, true
		}
		pc.TotalKobo = 0
		if it.PriceKobo != nil {
			pc.TotalKobo = *it.PriceKobo
		}
		if pc.TotalKobo != sub.TotalKobo {
			changed = true
		}
		if sub.DepositKobo > 0 {
			pc.DepositKobo = opts.Deposit(pc.TotalKobo)
		}
		return pc, changed
	}
	for _, l := range sub.Basket {
		it, ok := live[l.ID]
		if !ok || !it.Available || it.PriceKobo == nil || it.SoldOut() {
			pc.Gone = append(pc.Gone, l.Name)
			changed = true
			continue
		}
		n := l
		n.PriceKobo = *it.PriceKobo
		n.LineKobo = n.PriceKobo * int64(n.Quantity)
		if n.PriceKobo != l.PriceKobo {
			changed = true
		}
		pc.Basket = append(pc.Basket, n)
		pc.TotalKobo += n.LineKobo
	}
	return pc, changed
}

// Items reads rows of the catalogue or services table through the
// tenant's data tables connection, mapped as the copy is (without photos).
func (x *XenibaseStock) Items(ctx context.Context, tenant uuid.UUID, services bool, ids []string) (map[string]Item, error) {
	var table string
	var m Columns
	err := db.InTenantTx(ctx, x.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		sf, err := Get(ctx, tx, tenant)
		if err != nil {
			return err
		}
		id, cols := sf.CatalogueTable, sf.CatalogueColumns
		if services {
			id, cols = sf.ServicesTable, sf.ServicesColumns
		}
		if id == nil {
			return nil
		}
		t, err := datatables.GetTable(ctx, tx, *id)
		if err != nil {
			return err
		}
		table, m = t.Name, cols
		return nil
	})
	if err != nil || table == "" || len(ids) == 0 {
		return map[string]Item{}, err
	}
	values := make([]any, len(ids))
	for i, id := range ids {
		values[i] = id
	}
	page, err := x.call(ctx, tenant, "read_rows", map[string]any{"table": table, "limit": len(ids),
		"filters": []any{map[string]any{"column": "id", "op": "in", "values": values}}}, "")
	if err != nil {
		return nil, err
	}
	var rows []map[string]any
	list, _ := page["rows"].([]any)
	for _, r := range list {
		if row, ok := r.(map[string]any); ok {
			rows = append(rows, row)
		}
	}
	m.Photo = "" // no photos: Items needs no transaction
	items, err := Items(ctx, nil, tenant, rows, m)
	if err != nil {
		return nil, err
	}
	out := make(map[string]Item, len(items))
	for _, it := range items {
		out[it.ID] = it
	}
	return out, nil
}
