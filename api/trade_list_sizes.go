package api

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/links"
	"github.com/israel-duff/taskiem/engine/stock"
)

// syncListSizes keeps the sizes and colours of the products the price
// lists hold as the catalogue has them, however it changed: a size taken
// off sale or brought back in its form, on WhatsApp, in the Tables tool
// or by a front's show and hide. It runs after each catalogue sync; a
// list whose sizes are already right is left alone and its buyers are not
// told again (links.SetListSizesTx).
func (s *Server) syncListSizes(ctx context.Context, tenant uuid.UUID) {
	if !s.Stock.Ready() {
		return
	}
	var held []string
	if err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		var err error
		held, err = links.ListedSizedTx(ctx, tx, tenant)
		return err
	}); err != nil || len(held) == 0 {
		if err != nil {
			s.Logger.Warn("stock: sizes on the price lists", "tenant", tenant, "err", err)
		}
		return
	}
	_, items, err := s.stockItems(ctx, tenant)
	if err != nil {
		s.Logger.Warn("stock: sizes on the price lists", "tenant", tenant, "err", err)
		return
	}
	s.setListSizes(ctx, tenant, items, held...)
}

// setListSizes gives the price lists holding these products their sizes
// on sale now, and tells the buyers of the lists that changed.
func (s *Server) setListSizes(ctx context.Context, tenant uuid.UUID, items []stock.Item, products ...string) {
	byID := map[string]stock.Item{}
	for _, it := range items {
		byID[it.ID] = it
	}
	var due []uuid.UUID
	if err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		for _, id := range products {
			it, ok := byID[id]
			if !ok {
				continue // gone from the catalogue: the list keeps what it had
			}
			choices, sizes := listSizesOf(it, items)
			ls, err := links.SetListSizesTx(ctx, tx, tenant, id, choices, sizes)
			if err != nil {
				return err
			}
			due = append(due, ls...)
		}
		return nil
	}); err != nil {
		s.Logger.Warn("stock: sizes on the price lists", "tenant", tenant, "err", err)
		return
	}
	s.mirrorPrices(ctx, tenant, due)
}

// sizeParents is each size's product, from the catalogue: a size's lowest
// price is its product's unless it has its own, whether or not a price
// list holds it now. Read before a transaction opens (the catalogue is a
// table of its own); dealCheck adds what the lists record.
func (s *Server) sizeParents(ctx context.Context, tenant uuid.UUID) map[string]string {
	out := map[string]string{}
	if !s.Stock.Ready() {
		return out
	}
	_, items, err := s.stockItems(ctx, tenant)
	if err != nil {
		s.Logger.Warn("stock: sizes for the lowest prices", "tenant", tenant, "err", err)
		return out
	}
	for _, it := range items {
		if it.VariantOf != "" {
			out[it.ID] = it.VariantOf
		}
	}
	return out
}
