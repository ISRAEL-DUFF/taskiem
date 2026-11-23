package spend

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// A linked supplier's bill (§8, A bill that does not match its order).
// Its lines are the supplier's invoice's (bills.lines), and need not
// match the order line for line. Each line mirrored from an invoice
// raised for a linked order names the supplier's own item
// (seller_item_id) and the buyer's order (purchase_id), as the mirror
// writes them (api mirrorInvoice):
//
//   - a bill line is matched to the order line of the same seller item:
//     its label and treatment are that line's, and the bill's price is
//     the amount;
//   - a bill line that matches no order line (an extra, a delivery
//     charge) is Not labelled, for the business to label later;
//   - an order line the bill does not have adds nothing;
//   - a bill with no order behind it is split by the item links of its
//     lines (item_links by the link and the seller item), and is
//     otherwise Not labelled.
//
// The bill's expense is the payout that pays it (business links §6.3:
// one movement, paired with the supplier's credit), so its split is read,
// not written: reports share each payout by BillPartsTx.

// BillLine is what the split reads of a bill's line.
type BillLine struct {
	Description string `json:"description"`
	Quantity    int64  `json:"quantity"`
	UnitKobo    int64  `json:"unit_kobo"`
	// SellerItemID is the supplier's own item the line is, and PurchaseID
	// the buyer's order it was invoiced for, when the mirror knows them.
	SellerItemID string     `json:"seller_item_id,omitempty"`
	PurchaseID   *uuid.UUID `json:"purchase_id,omitempty"`
}

// BillPartsTx is a bill's lines by label and treatment, each part's Kobo
// the bill's own amount for it: the weights its payments are shared by.
// Labels are the ones kept now.
func BillPartsTx(ctx context.Context, tx pgx.Tx, tenant, bill uuid.UUID) ([]Part, error) {
	var link uuid.UUID
	var raw []byte
	err := tx.QueryRow(ctx, `SELECT link_id, lines FROM bills WHERE tenant_id = $1 AND id = $2`, tenant, bill).Scan(&link, &raw)
	if err != nil {
		return nil, err
	}
	var lines []BillLine
	if err := json.Unmarshal(raw, &lines); err != nil {
		return nil, err
	}
	return BillLinesPartsTx(ctx, tx, tenant, link, lines)
}

// BillLinesPartsTx is BillPartsTx for lines read already, of a bill
// through link.
func BillLinesPartsTx(ctx context.Context, tx pgx.Tx, tenant, link uuid.UUID, lines []BillLine) ([]Part, error) {
	var order *uuid.UUID
	for _, l := range lines {
		if l.PurchaseID != nil {
			order = l.PurchaseID
			break
		}
	}
	k := keeper{tenant: tenant}
	var parts []Part
	for _, l := range lines {
		treatment, label := Record, (*uuid.UUID)(nil)
		if l.SellerItemID != "" {
			var t string
			var id *uuid.UUID
			var err error
			if order != nil {
				err = tx.QueryRow(ctx, `SELECT treatment, label_id FROM purchase_lines WHERE tenant_id = $1 AND purchase_order_id = $2 AND seller_item_id = $3
					ORDER BY position LIMIT 1`, tenant, *order, l.SellerItemID).Scan(&t, &id)
			} else {
				err = tx.QueryRow(ctx, `SELECT treatment, label_id FROM item_links WHERE tenant_id = $1 AND link_id = $2 AND seller_item_id = $3`,
					tenant, link, l.SellerItemID).Scan(&t, &id)
			}
			switch {
			case err == nil:
				treatment = t
				kept, err := k.kept(ctx, tx, id)
				if err != nil {
					return nil, err
				}
				if kept != nil {
					label = &kept.ID
				} else if t == Stock {
					// A line to stock with no label is Stock bought, as
					// when an order is paid, where the business has it.
					sb, err := FindTx(ctx, tx, tenant, StockBought)
					if err == nil {
						label = &sb.ID
					} else if !errors.Is(err, ErrNoLabel) {
						return nil, err
					}
				}
			case !errors.Is(err, pgx.ErrNoRows):
				return nil, err
			}
		}
		parts = add(parts, label, treatment, l.Quantity*l.UnitKobo)
	}
	return parts, nil
}

// BillLabelsTx gives a bill's lines added to stock the default label, as
// paying an order by hand does (OrderLabelsTx), when the bill is mirrored
// in: the lines of the order it was invoiced for that are added to stock
// with no label take Stock bought, and when a line with no order is
// added to stock by its item link with no label, Stock bought is made, so
// BillLinesPartsTx finds it. Without it a linked supplier's stock lines
// were Stock bought only where some order had been paid by hand (PR
// review fix).
func BillLabelsTx(ctx context.Context, tx pgx.Tx, tenant, link uuid.UUID, raw json.RawMessage, actor string) error {
	var lines []BillLine
	if err := json.Unmarshal(raw, &lines); err != nil {
		return err
	}
	var order *uuid.UUID
	var items []string
	for _, l := range lines {
		if l.PurchaseID != nil && order == nil {
			order = l.PurchaseID
		}
		if l.SellerItemID != "" {
			items = append(items, l.SellerItemID)
		}
	}
	if order != nil {
		return OrderLabelsTx(ctx, tx, tenant, *order, actor)
	}
	if len(items) == 0 {
		return nil
	}
	var bare bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM item_links WHERE tenant_id = $1 AND link_id = $2 AND seller_item_id = ANY ($3)
		AND treatment = 'stock' AND label_id IS NULL)`, tenant, link, items).Scan(&bare); err != nil || !bare {
		return err
	}
	_, _, err := FindOrMakeTx(ctx, tx, tenant, StockBought, actor)
	return err
}
