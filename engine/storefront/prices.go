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

// Live reads rows of a tenant's table at Xenibase, mapped to items by id:
// the catalogue's, the services', or a front's list's (fronts F3). A row
// gone is absent.
type Live interface {
	Items(ctx context.Context, tenant uuid.UUID, from Source, ids []string) (map[string]Item, error)
}

// Source is where a submission's items come from: a front's list's table
// and mapping when set, else the services (Services) or the catalogue.
type Source struct {
	Services bool
	Table    *uuid.UUID
	Columns  Columns
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
		add, ok := it.rechoose(l.Options)
		if !ok {
			pc.Gone = append(pc.Gone, l.LineName())
			changed = true
			continue
		}
		n.PriceKobo = *it.PriceKobo + add
		n.LineKobo = n.PriceKobo * int64(n.Quantity)
		if n.PriceKobo != l.PriceKobo {
			changed = true
		}
		pc.Basket = append(pc.Basket, n)
		pc.TotalKobo += n.LineKobo
	}
	if len(pc.Basket) > 0 {
		pc.TotalKobo += sub.DeliveryKobo
	}
	// An order with a deposit keeps one at the new prices: without it, the
	// customer accepting the change would be asked for the whole total.
	if sub.DepositKobo > 0 && len(pc.Basket) > 0 {
		pc.DepositKobo = repricedDeposit(sub, pc.TotalKobo, opts)
	}
	return pc, changed
}

// repricedDeposit is an order's deposit at a new total: the action's
// percentage when it has one, else the share of the total the order's
// deposit was, rounded up to whole kobo as Deposit rounds.
func repricedDeposit(sub Submission, total int64, opts ActionOptions) int64 {
	if opts.DepositPercent > 0 && opts.DepositPercent < 100 {
		return opts.OrderDeposit(total)
	}
	if sub.TotalKobo <= 0 || sub.DepositKobo >= sub.TotalKobo {
		return 0
	}
	if d := (total*sub.DepositKobo + sub.TotalKobo - 1) / sub.TotalKobo; d >= MinDepositKobo && d < total {
		return d
	}
	return 0
}

// Items reads rows of the catalogue or services table through the
// tenant's data tables connection, mapped as the copy is (without photos).
func (x *XenibaseStock) Items(ctx context.Context, tenant uuid.UUID, from Source, ids []string) (map[string]Item, error) {
	var table string
	var m Columns
	err := db.InTenantTx(ctx, x.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		sf, err := Get(ctx, tx, tenant)
		if err != nil {
			return err
		}
		id, cols := sf.CatalogueTable, sf.CatalogueColumns
		switch {
		case from.Table != nil:
			id, cols = from.Table, from.Columns
		case from.Services:
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
