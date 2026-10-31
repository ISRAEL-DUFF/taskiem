package api

// link@1 (business links L6, docs/spec/business-links.md §11): what a
// workflow does between linked businesses, for the run's own business.
// Each action is the one a person takes on the web, as the system
// ("workflow:<run>"), under the same checks: a bill is paid through the
// business's bill payments and their approval; an order above the
// business's own limit waits for a person; an order over the customer's
// credit limit is accepted only by a person.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	linkconn "github.com/israel-duff/taskiem/connectors/link"
	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/effects"
	"github.com/israel-duff/taskiem/engine/links"
	"github.com/israel-duff/taskiem/engine/stock"
)

// LinkAction does one link@1 action (connectors/link).
func (s *Server) LinkAction(ctx context.Context, c linkconn.Call) (map[string]any, error) {
	p := &Principal{TenantID: c.Tenant, Run: c.Run, Permissions: map[string]bool{}}
	var out map[string]any
	var err error
	switch c.Action {
	case "get_bill":
		out, err = s.linkGetBill(ctx, p, c)
	case "list_bills":
		out, err = s.linkListBills(ctx, p, c)
	case "pay_bill":
		out, err = s.linkPayBill(ctx, p, c)
	case "get_order":
		out, err = s.linkGetOrder(ctx, p, c)
	case "order_step":
		out, err = s.linkOrderStep(ctx, p, c)
	case "price_of":
		out, err = s.linkPriceOf(ctx, p, c)
	case "low_orders":
		out, err = s.linkLowOrders(ctx, p, c)
	case "place_order":
		out, err = s.linkPlaceOrder(ctx, p, c)
	case "reply":
		out, err = s.linkReply(ctx, p, c)
	default:
		err = fmt.Errorf("%w: no action %q", links.ErrInvalid, c.Action)
	}
	return out, linkFatal(err)
}

// linkFatal marks a refusal no retry changes as fatal, in plain words.
func linkFatal(err error) error {
	var unlinked errUnlinkedItem
	switch {
	case err == nil:
		return nil
	case errors.Is(err, links.ErrNotFound), errors.Is(err, pgx.ErrNoRows):
		return fmt.Errorf("not found: %w", effects.ErrFatal)
	case errors.As(err, &unlinked):
		return fmt.Errorf("%s: %w", unlinked.Error(), effects.ErrFatal)
	case errors.Is(err, links.ErrInvalid), errors.Is(err, links.ErrState), errors.Is(err, errConflict), errors.Is(err, errBadRequest),
		errors.Is(err, stock.ErrPurchase), errors.Is(err, stock.ErrNoSupplier), errors.Is(err, stock.ErrNoItem), errors.Is(err, stock.ErrNotTracked):
		return fmt.Errorf("%s: %w", plainRefusal(err), effects.ErrFatal)
	}
	return err
}

func plainRefusal(err error) string {
	msg := err.Error()
	for _, e := range []error{links.ErrInvalid, links.ErrState, errConflict, errBadRequest} {
		msg = strings.TrimPrefix(msg, e.Error()+": ")
	}
	return msg
}

func inStr(in map[string]any, k string) string {
	v, _ := in[k].(string)
	return strings.TrimSpace(v)
}

func inInt(in map[string]any, k string) int64 {
	switch v := in[k].(type) {
	case float64:
		return int64(v)
	case int64:
		return v
	case int:
		return int64(v)
	case json.Number:
		n, _ := v.Int64()
		return n
	}
	return 0
}

// wfAudit appends an audit entry for a run's action.
func wfAudit(ctx context.Context, tx pgx.Tx, p *Principal, action, target string, detail map[string]any) error {
	if detail == nil {
		detail = map[string]any{}
	}
	raw, _ := json.Marshal(detail)
	_, err := tx.Exec(ctx, `SELECT taskiem_audit_append($1, 'system', $2, $3, $4, $5)`, p.TenantID, p.Actor(), action, target, raw)
	return err
}

// linkNamed is the business's active link with the business named (its
// name on Taskiem, or the link's id): the same name first, then the only
// one starting with or holding it.
func linkNamed(ctx context.Context, tx pgx.Tx, name string) (links.Link, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return links.Link{}, fmt.Errorf("%w: name the linked business", links.ErrInvalid)
	}
	all, _, err := links.ListTx(ctx, tx)
	if err != nil {
		return links.Link{}, err
	}
	var active []links.Link
	for _, l := range all {
		if l.State == "active" {
			active = append(active, l)
		}
	}
	if id, err := uuid.Parse(name); err == nil {
		for _, l := range active {
			if l.ID == id {
				return l, nil
			}
		}
		return links.Link{}, fmt.Errorf("%w: no active link %s", links.ErrInvalid, name)
	}
	want := strings.ToLower(name)
	for _, l := range active {
		if strings.ToLower(l.Name) == want {
			return l, nil
		}
	}
	var found []links.Link
	for _, l := range active {
		if strings.Contains(strings.ToLower(l.Name), want) {
			found = append(found, l)
		}
	}
	switch len(found) {
	case 1:
		return found[0], nil
	case 0:
		return links.Link{}, fmt.Errorf("%w: no business linked with you is called %q", links.ErrInvalid, name)
	}
	names := make([]string, len(found))
	for i, l := range found {
		names[i] = l.Name
	}
	return links.Link{}, fmt.Errorf("%w: %q could be %s", links.ErrInvalid, name, strings.Join(names, ", "))
}

// --- bills ---

func billOut(b links.Bill) map[string]any {
	return map[string]any{"reference": b.Reference, "business": b.SupplierName, "link": b.LinkID.String(), "status": b.Status,
		"amount_kobo": b.AmountKobo, "paid_kobo": b.PaidKobo, "due_kobo": b.DueKobo, "due_on": b.DueOn,
		"payable": b.PayingKobo == 0 && !b.Disputed && b.DueKobo > 0 && b.PayAccountNumber != "" && b.Status != "void"}
}

func (s *Server) linkGetBill(ctx context.Context, p *Principal, c linkconn.Call) (map[string]any, error) {
	ref := inStr(c.Input, "reference")
	if ref == "" {
		return nil, fmt.Errorf("%w: a bill's reference is required", links.ErrInvalid)
	}
	var b links.Bill
	err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{p.TenantID}, func(tx pgx.Tx) error {
		var err error
		b, err = links.GetBillTx(ctx, tx, ref)
		return err
	})
	return billOut(b), err
}

func (s *Server) linkListBills(ctx context.Context, p *Principal, c linkconn.Call) (map[string]any, error) {
	limit := int(inInt(c.Input, "limit"))
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	business := strings.ToLower(inStr(c.Input, "business"))
	_, within := c.Input["due_within_days"]
	by := time.Now().In(lagosTZ).AddDate(0, 0, int(inInt(c.Input, "due_within_days"))).Format(time.DateOnly)
	var all []links.Bill
	if err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{p.TenantID}, func(tx pgx.Tx) error {
		var err error
		all, err = links.ListBillsTx(ctx, tx, "open", 200)
		return err
	}); err != nil {
		return nil, err
	}
	bills := []any{}
	var due int64
	for _, b := range all {
		switch {
		case b.PayingKobo > 0 || b.DueKobo <= 0:
			continue
		case business != "" && strings.ToLower(b.SupplierName) != business && !strings.Contains(strings.ToLower(b.SupplierName), business):
			continue
		case within && (b.DueOn == "" || b.DueOn > by):
			continue
		}
		if len(bills) == limit {
			break
		}
		bills = append(bills, billOut(b))
		due += b.DueKobo
	}
	return map[string]any{"bills": bills, "count": len(bills), "due_kobo": due}, nil
}

// linkPayBill starts paying a bill through the business's bill payments,
// whose approver approves it. The same step again answers the payment it
// started.
func (s *Server) linkPayBill(ctx context.Context, p *Principal, c linkconn.Call) (map[string]any, error) {
	ref := inStr(c.Input, "reference")
	if ref == "" {
		return nil, fmt.Errorf("%w: a bill's reference is required", links.ErrInvalid)
	}
	by := "workflow:" + c.Key
	out := func(pay links.BillPayment) map[string]any {
		o := map[string]any{"payment_id": pay.ID.String(), "reference": pay.Reference, "amount_kobo": pay.AmountKobo, "status": "approving"}
		if pay.RunID != nil {
			o["run_id"] = pay.RunID.String()
		}
		return o
	}
	var before *links.BillPayment
	if err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{p.TenantID}, func(tx pgx.Tx) error {
		var pay links.BillPayment
		err := tx.QueryRow(ctx, `SELECT p.id, b.reference, p.amount_kobo, p.run_id FROM bill_payments p JOIN bills b ON b.id = p.bill_id
			WHERE p.tenant_id = $1 AND p.created_by = $2 ORDER BY p.created_at DESC LIMIT 1`, p.TenantID, by).Scan(&pay.ID, &pay.Reference, &pay.AmountKobo, &pay.RunID)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		before = &pay
		return err
	}); err != nil {
		return nil, err
	}
	if before != nil {
		return out(*before), nil
	}
	pay, err := s.startBillPay(ctx, p.TenantID, ref, inInt(c.Input, "amount_kobo"), by, p.Actor(), func(tx pgx.Tx, b links.Bill, pay links.BillPayment) error {
		return wfAudit(ctx, tx, p, "bill.pay", b.Reference, map[string]any{"amount_kobo": pay.AmountKobo, "payment": pay.ID})
	})
	if err != nil {
		return nil, err
	}
	return out(pay), nil
}

// --- orders ---

func (s *Server) linkGetOrder(ctx context.Context, p *Principal, c linkconn.Call) (map[string]any, error) {
	ref := inStr(c.Input, "reference")
	if ref == "" {
		return nil, fmt.Errorf("%w: an order's reference is required", links.ErrInvalid)
	}
	var out map[string]any
	err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{p.TenantID}, func(tx pgx.Tx) error {
		o, err := links.GetOrderTx(ctx, tx, ref, false)
		if err == nil {
			out = sellerOrderOut(o)
			return nil
		}
		if !errors.Is(err, links.ErrNotFound) {
			return err
		}
		x, err := stock.PurchaseByReferenceTx(ctx, tx, p.TenantID, ref)
		if errors.Is(err, pgx.ErrNoRows) || (err == nil && x.LinkID == nil) {
			return links.ErrNotFound
		}
		if err != nil {
			return err
		}
		status := x.Status
		if x.SellerStatus != nil && *x.SellerStatus != "" {
			status = *x.SellerStatus
		}
		lines := make([]any, len(x.Lines))
		for i, l := range x.Lines {
			lines[i] = map[string]any{"item": l.Item, "unit": l.Unit, "quantity": l.Quantity, "price_kobo": l.CostKobo}
		}
		out = map[string]any{"reference": x.Reference, "side": "buyer", "business": x.Supplier, "link": x.LinkID.String(), "status": status,
			"total_kobo": x.TotalKobo, "lines": lines, "over_limit": false}
		return nil
	})
	return out, err
}

func sellerOrderOut(o links.Order) map[string]any {
	lines := make([]any, len(o.Lines))
	for i, l := range o.Lines {
		lines[i] = map[string]any{"item": l.Item, "unit": l.Unit, "quantity": l.Quantity, "price_kobo": l.PriceKobo}
	}
	return map[string]any{"reference": o.Reference, "side": "seller", "business": o.BuyerName, "link": o.LinkID.String(), "status": o.Status,
		"total_kobo": o.TotalKobo, "lines": lines, "over_limit": o.OverLimitAt != nil}
}

// stepReached is the order's status once each step a run may take is
// done: the same step again answers the order as it is.
var stepReached = map[string][]string{
	links.StepAccept:     {links.OrderAccepted, links.OrderReady, links.OrderDispatched, links.OrderDelivered},
	links.StepDecline:    {links.OrderDeclined},
	links.StepReady:      {links.OrderReady, links.OrderDispatched, links.OrderDelivered},
	links.StepDispatched: {links.OrderDispatched, links.OrderDelivered},
}

func (s *Server) linkOrderStep(ctx context.Context, p *Principal, c linkconn.Call) (map[string]any, error) {
	ref, step := inStr(c.Input, "reference"), inStr(c.Input, "step")
	reached, ok := stepReached[step]
	if !ok {
		return nil, fmt.Errorf("%w: a run may accept, decline, mark ready or dispatched", links.ErrInvalid)
	}
	var cur links.Order
	if err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{p.TenantID}, func(tx pgx.Tx) error {
		var err error
		cur, err = links.GetOrderTx(ctx, tx, ref, false)
		return err
	}); err != nil {
		return nil, err
	}
	if slices.Contains(reached, cur.Status) {
		return map[string]any{"reference": cur.Reference, "status": cur.Status}, nil
	}
	if step == links.StepAccept {
		// Checked now, not only when the order came: the balance moves.
		if c, o := s.creditCheck(ctx, p.TenantID, cur.ID.String()); o != nil && c.Over {
			cur.OverLimitAt = o.OverLimitAt
			if cur.OverLimitAt == nil {
				now := time.Now()
				cur.OverLimitAt = &now
			}
		}
	}
	if step == links.StepAccept && cur.OverLimitAt != nil {
		return nil, fmt.Errorf("%w: %s takes %s past the credit limit you set; a person accepts it on the order's page", links.ErrState, cur.Reference, cur.BuyerName)
	}
	o, _, err := s.sellerStep(ctx, p.TenantID, cur.ID.String(), step, links.StepIn{Note: inStr(c.Input, "note")}, actorOf(p), p.Actor(), func(tx pgx.Tx, o links.Order) error {
		return wfAudit(ctx, tx, p, "trade_order."+step, o.Reference, map[string]any{"order_id": o.ID, "buyer": o.Buyer})
	})
	if err != nil {
		return nil, err
	}
	return map[string]any{"reference": o.Reference, "status": o.Status}, nil
}

// --- prices and orders to linked suppliers ---

// priceListed finds an item on a supplier's list: its item id, its name,
// or the business's own item linked to it (by id or name).
func priceListed(items []links.PriceItem, linked []links.ItemLink, own map[string]stock.Item, want string) (links.PriceItem, bool) {
	w := strings.ToLower(strings.TrimSpace(want))
	for _, it := range items {
		if it.ItemID == want || strings.ToLower(it.Name) == w {
			return it, true
		}
	}
	for _, l := range linked {
		o, ok := own[l.ItemID]
		if l.ItemID != want && (!ok || strings.ToLower(o.Name) != w) {
			continue
		}
		for _, it := range items {
			if it.ItemID == l.SellerItemID {
				return it, true
			}
		}
	}
	// The only item on the list whose name holds the words.
	var found []links.PriceItem
	for _, it := range items {
		if w != "" && strings.Contains(strings.ToLower(it.Name), w) {
			found = append(found, it)
		}
	}
	if len(found) == 1 {
		return found[0], true
	}
	return links.PriceItem{}, false
}

func priceOut(it links.PriceItem) map[string]any {
	return map[string]any{"item": it.Name, "item_id": it.ItemID, "unit": it.Unit, "price_kobo": it.PriceKobo, "min_quantity": it.MinQuantity, "available": it.Available}
}

// ownItems are the business's stock items by id, none when it keeps no
// stock.
func (s *Server) ownItems(ctx context.Context, tenant uuid.UUID) (map[string]stock.Item, []stock.Item, error) {
	own := map[string]stock.Item{}
	if s.Stock == nil {
		return own, nil, nil
	}
	_, items, err := s.stockItems(ctx, tenant)
	if errors.Is(err, stock.ErrNotTracked) {
		return own, nil, nil
	}
	for _, it := range items {
		own[it.ID] = it
	}
	return own, items, err
}

func (s *Server) linkPriceOf(ctx context.Context, p *Principal, c linkconn.Call) (map[string]any, error) {
	own, _, err := s.ownItems(ctx, p.TenantID)
	if err != nil {
		return nil, err
	}
	var out map[string]any
	err = db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{p.TenantID}, func(tx pgx.Tx) error {
		l, err := linkNamed(ctx, tx, inStr(c.Input, "business"))
		if err != nil {
			return err
		}
		got, err := links.SupplierPricesTx(ctx, tx, p.TenantID, l.ID)
		if err != nil {
			return err
		}
		if len(got) == 0 {
			return fmt.Errorf("%w: %s shares no price list with you", links.ErrState, l.Name)
		}
		linked, err := links.ItemLinksTx(ctx, tx, p.TenantID, l.ID)
		if err != nil {
			return err
		}
		it, ok := priceListed(got[0].Items, linked, own, inStr(c.Input, "item"))
		if !ok {
			return fmt.Errorf("%w: %q is not on %s's price list", links.ErrInvalid, inStr(c.Input, "item"), l.Name)
		}
		out = priceOut(it)
		return nil
	})
	return out, err
}

// linkLowOrders is what the low list would order from linked suppliers at
// their prices: each low item linked to a supplier's list (the item's own
// supplier's first), for enough to reach its order-up-to level.
func (s *Server) linkLowOrders(ctx context.Context, p *Principal, c linkconn.Call) (map[string]any, error) {
	_, items, err := s.ownItems(ctx, p.TenantID)
	if err != nil {
		return nil, err
	}
	only := inStr(c.Input, "business")
	orders := []any{}
	notListed := []string{}
	err = db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{p.TenantID}, func(tx pgx.Tx) error {
		var onlyLink uuid.UUID
		if only != "" {
			l, err := linkNamed(ctx, tx, only)
			if err != nil {
				return err
			}
			onlyLink = l.ID
		}
		rows, err := tx.Query(ctx, `SELECT l->>'item_id' FROM purchase_orders o, jsonb_array_elements(o.lines) l
			WHERE o.tenant_id = $1 AND o.status IN ('drafted', 'sent', 'confirmed', 'part_received')`, p.TenantID)
		if err != nil {
			return err
		}
		ids, err := pgx.CollectRows(rows, pgx.RowTo[string])
		if err != nil {
			return err
		}
		lists, err := links.SupplierPricesTx(ctx, tx, p.TenantID, onlyLink)
		if err != nil {
			return err
		}
		type supplierList struct {
			list   links.SupplierPrices
			byItem map[string]links.PriceItem // our item id → their listed item
		}
		var sups []supplierList
		for _, l := range lists {
			linked, err := links.ItemLinksTx(ctx, tx, p.TenantID, l.LinkID)
			if err != nil {
				return err
			}
			listed := map[string]links.PriceItem{}
			for _, it := range l.Items {
				listed[it.ItemID] = it
			}
			sl := supplierList{list: l, byItem: map[string]links.PriceItem{}}
			for _, il := range linked {
				if it, ok := listed[il.SellerItemID]; ok && it.Available {
					sl.byItem[il.ItemID] = it
				}
			}
			sups = append(sups, sl)
		}
		type draft struct {
			sup   supplierList
			lines []any
			total int64
		}
		var drafts []*draft
		bySup := map[uuid.UUID]*draft{}
		for _, it := range stock.Low(items) {
			if slices.Contains(ids, it.ID) {
				continue
			}
			pick := -1
			for i, sl := range sups {
				if _, ok := sl.byItem[it.ID]; !ok {
					continue
				}
				if pick < 0 || strings.EqualFold(sl.list.Business, strings.TrimSpace(it.Supplier)) {
					pick = i
				}
			}
			if pick < 0 {
				if only == "" {
					notListed = append(notListed, it.Name)
				}
				continue
			}
			sl := sups[pick]
			listed := sl.byItem[it.ID]
			qty := max(it.Suggest(), int64(listed.MinQuantity), 1)
			d := bySup[sl.list.LinkID]
			if d == nil {
				d = &draft{sup: sl}
				bySup[sl.list.LinkID] = d
				drafts = append(drafts, d)
			}
			d.lines = append(d.lines, map[string]any{"item": listed.ItemID, "name": listed.Name, "own_item": it.Name, "unit": listed.Unit,
				"quantity": qty, "price_kobo": listed.PriceKobo})
			d.total += qty * listed.PriceKobo
		}
		for _, d := range drafts {
			orders = append(orders, map[string]any{"business": d.sup.list.Business, "link": d.sup.list.LinkID.String(), "items": d.lines, "total_kobo": d.total})
		}
		return nil
	})
	return map[string]any{"orders": orders, "count": len(orders), "not_listed": notListed}, err
}

// linkPlaceOrder drafts an order to a linked supplier at its listed
// prices and sends it; above the business's own limit it stays drafted
// for a person.
func (s *Server) linkPlaceOrder(ctx context.Context, p *Principal, c linkconn.Call) (map[string]any, error) {
	if s.Purchases == nil {
		return nil, fmt.Errorf("%w: purchase orders are not available", links.ErrState)
	}
	own, _, err := s.ownItems(ctx, p.TenantID)
	if err != nil {
		return nil, err
	}
	raw, _ := c.Input["items"].([]any)
	if len(raw) == 0 {
		return nil, fmt.Errorf("%w: an order has at least one item", links.ErrInvalid)
	}
	var x stock.Purchase
	var business string
	err = db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{p.TenantID}, func(tx pgx.Tx) error {
		l, err := linkNamed(ctx, tx, inStr(c.Input, "business"))
		if err != nil {
			return err
		}
		business = l.Name
		got, err := links.SupplierPricesTx(ctx, tx, p.TenantID, l.ID)
		if err != nil {
			return err
		}
		if len(got) == 0 {
			return fmt.Errorf("%w: %s shares no price list with you", links.ErrState, l.Name)
		}
		linked, err := links.ItemLinksTx(ctx, tx, p.TenantID, l.ID)
		if err != nil {
			return err
		}
		lines := make([]orderLineIn, 0, len(raw))
		for _, r := range raw {
			m, _ := r.(map[string]any)
			name := inStr(m, "item")
			it, ok := priceListed(got[0].Items, linked, own, name)
			if !ok {
				return fmt.Errorf("%w: %q is not on %s's price list", links.ErrInvalid, name, l.Name)
			}
			line := orderLineIn{SellerItemID: it.ItemID, Quantity: inInt(m, "quantity")}
			// Named by one of the business's own items: that item, linked
			// to theirs the first time.
			for id, o := range own {
				if id == name || strings.EqualFold(o.Name, name) {
					line.ItemID = id
				}
			}
			lines = append(lines, line)
		}
		x, err = s.draftFromPricesTx(ctx, tx, p, l.ID, lines, own, inStr(c.Input, "note"), "link-"+c.Key)
		if err != nil {
			return err
		}
		return wfAudit(ctx, tx, p, "purchase.draft", x.Reference, map[string]any{"purchase_id": x.ID, "link": l.ID})
	})
	if err != nil {
		return nil, err
	}
	out := map[string]any{"reference": x.Reference, "business": business, "total_kobo": x.TotalKobo, "status": "drafted"}
	err = db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{p.TenantID}, func(tx pgx.Tx) error {
		y, err := s.Purchases.SendTx(ctx, tx, p.TenantID, x.ID, p.Actor())
		if err != nil {
			return err
		}
		out["status"] = "sent"
		return wfAudit(ctx, tx, p, "purchase.send", y.Reference, map[string]any{"purchase_id": y.ID})
	})
	if errors.Is(err, stock.ErrSecondApproval) {
		out["status"], out["reason"] = "drafted", plainRefusal(err)
		return out, nil
	}
	return out, err
}

// --- the conversation ---

func (s *Server) linkReply(ctx context.Context, p *Principal, c linkconn.Call) (map[string]any, error) {
	text := inStr(c.Input, "text")
	if text == "" {
		return nil, fmt.Errorf("%w: write something", links.ErrInvalid)
	}
	sender := "workflow:" + c.Key
	var link uuid.UUID
	var replyTo *uuid.UUID
	var before *links.Message
	err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{p.TenantID}, func(tx pgx.Tx) error {
		var id, l uuid.UUID
		err := tx.QueryRow(ctx, `SELECT pair_id, link_id FROM link_messages WHERE tenant_id = $1 AND sender_id = $2 LIMIT 1`, p.TenantID, sender).Scan(&id, &l)
		if err == nil {
			before = &links.Message{PairID: id, LinkID: l}
			return nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		if ref := strings.ToUpper(inStr(c.Input, "reference")); ref != "" {
			for _, doc := range []string{links.DocBill, links.DocInvoice, links.DocPurchase, links.DocOrder} {
				m, err := links.CardForTx(ctx, tx, p.TenantID, doc, ref)
				if err != nil {
					return err
				}
				if m != nil {
					link, replyTo = m.LinkID, &m.PairID
					return nil
				}
			}
			return fmt.Errorf("%w: no document %s in your conversations", links.ErrInvalid, ref)
		}
		l2, err := linkNamed(ctx, tx, inStr(c.Input, "business"))
		link = l2.ID
		return err
	})
	if err != nil {
		return nil, err
	}
	if before != nil {
		return map[string]any{"message_id": before.PairID.String(), "link": before.LinkID.String()}, nil
	}
	m, err := s.sendLinkAs(ctx, p, link, text, &links.Sender{ID: sender}, links.SendOpts{ReplyTo: replyTo})
	if err != nil {
		return nil, err
	}
	return map[string]any{"message_id": m.PairID.String(), "link": link.String()}, nil
}
