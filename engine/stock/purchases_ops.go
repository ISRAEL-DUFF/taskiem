package stock

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Purchases for workflows (stock@1): drafting from the low list or from
// lines, sending, receiving and listing, each on the run's own business.

// DraftAsk is an order a workflow asks for: lines of item ids and how
// many (their names, units and last costs from the catalogue), or none to
// draft from the low list, for one supplier (by name) or all.
type DraftAsk struct {
	Supplier string
	Lines    []PurchaseLine
	Note     string
	Actor    string
	Key      string
}

// DraftPurchase drafts the orders asked for. skipped names the low items
// with no supplier Taskiem knows.
func (p *Purchases) DraftPurchase(ctx context.Context, tenant uuid.UUID, a DraftAsk) ([]Purchase, []string, error) {
	if p.Stock == nil {
		return nil, nil, ErrNotTracked
	}
	items, err := p.Stock.Items(ctx, tenant)
	if err != nil {
		return nil, nil, err
	}
	var out []Purchase
	var skipped []string
	err = p.inTx(ctx, tenant, func(tx pgx.Tx) error {
		suppliers, err := p.SuppliersTx(ctx, tx, tenant, false)
		if err != nil {
			return err
		}
		var who *Supplier
		if strings.TrimSpace(a.Supplier) != "" {
			s, names, err := FindSupplier(suppliers, a.Supplier)
			if err != nil {
				if len(names) > 0 {
					return fmt.Errorf("%w: %q matches %s", ErrNoSupplier, a.Supplier, strings.Join(names, ", "))
				}
				return fmt.Errorf("%w: %q", ErrNoSupplier, a.Supplier)
			}
			who = &s
		}
		if len(a.Lines) == 0 {
			low := items
			if who != nil {
				low = nil
				for _, it := range items {
					if strings.EqualFold(strings.TrimSpace(it.Supplier), who.Name) {
						low = append(low, it)
					}
				}
			}
			out, skipped, err = p.DraftLowTx(ctx, tx, tenant, low, a.Actor, a.Key)
			return err
		}
		if who == nil {
			return fmt.Errorf("%w: name the supplier for the lines", ErrPurchase)
		}
		byID := make(map[string]Item, len(items))
		for _, it := range items {
			byID[it.ID] = it
		}
		lines := make([]PurchaseLine, 0, len(a.Lines))
		for _, l := range a.Lines {
			it, ok := byID[l.ItemID]
			if !ok {
				return ErrNoItem
			}
			cost := l.CostKobo
			if cost == 0 && it.CostKobo != nil {
				cost = *it.CostKobo
			}
			lines = append(lines, PurchaseLine{ItemID: it.ID, Item: it.Name, Unit: it.Unit, Quantity: l.Quantity, CostKobo: cost})
		}
		x, err := DraftTx(ctx, tx, tenant, DraftIn{Supplier: who.ID, Lines: lines, Note: a.Note, Actor: a.Actor, Key: a.Key})
		out = []Purchase{x}
		return err
	})
	return out, skipped, err
}

// SendPurchase approves and sends an order by its reference.
func (p *Purchases) SendPurchase(ctx context.Context, tenant uuid.UUID, ref, actor string) (Purchase, error) {
	var x Purchase
	err := p.inTx(ctx, tenant, func(tx pgx.Tx) error {
		o, err := PurchaseByReferenceTx(ctx, tx, tenant, ref)
		if err != nil {
			return err
		}
		x, err = p.SendTx(ctx, tx, tenant, o.ID, actor)
		return err
	})
	return x, err
}

// ReceivePurchase records goods received on an order by its reference.
func (p *Purchases) ReceivePurchase(ctx context.Context, tenant uuid.UUID, ref string, got map[string]int64, actor, key string) (Purchase, error) {
	var id uuid.UUID
	if err := p.inTx(ctx, tenant, func(tx pgx.Tx) error {
		o, err := PurchaseByReferenceTx(ctx, tx, tenant, ref)
		id = o.ID
		return err
	}); err != nil {
		return Purchase{}, err
	}
	return p.Receive(ctx, tenant, id, got, actor, key)
}

// ListPurchases lists orders, the newest first.
func (p *Purchases) ListPurchases(ctx context.Context, tenant uuid.UUID, f PurchaseFilter) ([]Purchase, error) {
	var out []Purchase
	err := p.inTx(ctx, tenant, func(tx pgx.Tx) error {
		var err error
		out, err = PurchasesTx(ctx, tx, tenant, f)
		return err
	})
	return out, err
}
