package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/invoicing"
	"github.com/israel-duff/taskiem/engine/lang"
	"github.com/israel-duff/taskiem/engine/links"
	"github.com/israel-duff/taskiem/engine/stock"
	"github.com/israel-duff/taskiem/engine/storefront"
	"github.com/israel-duff/taskiem/engine/whatsapp"
)

// Linked orders, business links L2 (docs/spec/business-links.md §7): a
// purchase order to a supplier that is a linked business becomes an order
// in the seller's business, and the seller's steps flow back to it. The
// seller's door is /trade-orders; the buyer's is its purchase orders
// (cancel, accept the change, All came, Not all came). The worker writes
// sent orders into sellers' businesses, raises their invoices (which L1
// mirrors as bills), and settles receipts a day after delivery.

func (s *Server) linkOrderRoutes(r chi.Router) {
	manage := s.need(PermStorefrontManage)
	r.With(manage).Get("/trade-orders", s.listTradeOrders)
	r.With(manage).Get("/trade-orders/{id}", s.getTradeOrder)
	r.With(manage, s.tenantWide).Post("/trade-orders", s.placeTradeOrder)
	r.With(manage, s.tenantWide).Post("/trade-orders/{id}/{step}", s.tradeOrderStep)
	r.With(manage, s.tenantWide).Put("/trade-orders/{id}/lines/{line}/item", s.setTradeLineItem)
	r.With(s.need(PermLinkManage)).Get("/links/{id}/trade", s.getLinkTrade)
	r.With(s.need(PermLinkManage), s.tenantWide).Put("/links/{id}/trade", s.putLinkTrade)
	r.With(manage, s.tenantWide).Put("/stock/suppliers/{id}/link", s.linkSupplier)
	r.With(manage).Get("/stock/deliver-to", s.getDeliverTo)
	r.With(manage, s.tenantWide).Put("/stock/deliver-to", s.putDeliverTo)
	r.With(manage, s.tenantWide).Post("/stock/purchases/{id}/accept-change", s.acceptPurchaseChange)
	r.With(manage, s.tenantWide).Post("/stock/purchases/{id}/confirm-proposed", s.confirmProposed)
	r.With(s.need(PermTableEdit), s.tenantWide).Post("/stock/purchases/{id}/all-came", s.allCame)
	r.With(s.need(PermTableEdit), s.tenantWide).Post("/stock/purchases/{id}/not-all-came", s.notAllCame)
}

// orderErr answers a linked order's refusals with their reasons.
func orderErr(err error) error {
	if errors.Is(err, links.ErrNotFound) {
		return pgx.ErrNoRows
	}
	return billErr(err)
}

type tradeOrdersOut struct {
	Orders []links.Order `json:"orders"`
}

// listTradeOrders is GET /v1/trade-orders (?open=true for those still
// needing the business).
func (s *Server) listTradeOrders(w http.ResponseWriter, r *http.Request) {
	var out tradeOrdersOut
	if err := s.tx(r, func(tx pgx.Tx) error {
		var err error
		out.Orders, err = links.ListOrdersTx(r.Context(), tx, r.URL.Query().Get("open") == "true", 200)
		return err
	}); err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

type tradeOrderOut struct {
	Order links.Order `json:"order"`
}

func (s *Server) getTradeOrder(w http.ResponseWriter, r *http.Request) {
	var out tradeOrderOut
	if err := s.tx(r, func(tx pgx.Tx) error {
		var err error
		out.Order, err = links.GetOrderTx(r.Context(), tx, chi.URLParam(r, "id"), false)
		return err
	}); err != nil {
		s.fail(w, r, orderErr(err))
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// tradeOrderStep is POST /v1/trade-orders/{id}/{step}: the seller accepts,
// declines, changes, readies, dispatches, delivers or cancels an order,
// keeps it when asked to cancel, or agrees the buyer's count of a short
// delivery. The buyer's purchase order follows at once.
func (s *Server) tradeOrderStep(w http.ResponseWriter, r *http.Request) {
	step := chi.URLParam(r, "step")
	if !slices.Contains(links.SellerSteps, step) {
		s.fail(w, r, pgx.ErrNoRows)
		return
	}
	var in links.StepIn
	if r.ContentLength != 0 {
		if err := decodeBody(r, &in); err != nil {
			s.fail(w, r, err)
			return
		}
	}
	p := principalFrom(r.Context())
	o, short, err := s.sellerStep(r.Context(), p.TenantID, chi.URLParam(r, "id"), step, in, actorOf(p), p.Actor(), func(tx pgx.Tx, o links.Order) error {
		return auditTx(r, tx, "trade_order."+step, o.Reference, map[string]any{"order_id": o.ID, "buyer": o.Buyer})
	})
	if err != nil {
		s.fail(w, r, orderErr(err))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"order": o, "short": short})
}

// sellerStep takes a seller's step on an order from a linked business (the
// web, the API, chat, and accepting by itself): the linked order's own
// fields in one transaction (with audit, the caller's own record), then
// the order moved by the storefront order's own Decide (its stock, its
// rider, its run), then mirrored to the buyer at once. It answers the
// order, and the items there was too little stock of on accepting.
func (s *Server) sellerStep(ctx context.Context, tenant uuid.UUID, ref, step string, in links.StepIn, by links.Actor, actor string,
	audit func(tx pgx.Tx, o links.Order) error) (links.Order, []string, error) {
	if s.Orders == nil {
		return links.Order{}, nil, errors.New("storefront orders are not configured")
	}
	var o links.Order
	var decide string
	became := false
	var amended invoicing.Invoice
	err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		cur, err := links.GetOrderTx(ctx, tx, ref, true)
		if err != nil {
			return err
		}
		if audit != nil {
			if err := audit(tx, cur); err != nil {
				return err
			}
		}
		// Agreeing a short count of an order invoiced at acceptance takes
		// what did not come off the invoice.
		if step == links.StepAgree && cur.InvoiceID != nil && cur.ShortAgreedAt == nil && cur.ShortAt != nil && cur.ShortKobo() > 0 && s.Invoices != nil {
			a, err := s.Invoices.Amend(ctx, tx, tenant, cur.InvoiceID.String(), invoicing.AmendmentIn{Kind: invoicing.AmendDiscount,
				Description: "Not all came: " + cur.Reference, AmountKobo: cur.ShortKobo(), Reason: "Agreed with " + cur.BuyerName + "'s count of what came"}, actor)
			if err != nil && !errors.Is(err, invoicing.ErrState) {
				return err
			}
			became, amended = a.Became, a.Invoice
		}
		o, decide, err = links.DecideTx(ctx, tx, tenant, cur.ID.String(), step, in, by)
		return err
	})
	if err != nil {
		return o, nil, err
	}
	if became {
		if err := s.Invoices.Paid(ctx, tenant, amended); err != nil {
			s.Logger.Warn("links: an invoice paid by agreeing a count", "err", err)
		}
	}
	short := []string{}
	if decide != "" {
		out, err := s.Orders.Decide(ctx, tenant, o.OrderReference, decide, "", actor)
		if errors.Is(err, storefront.ErrDecision) {
			return o, nil, fmt.Errorf("%w: %s", links.ErrState, strings.TrimPrefix(err.Error(), storefront.ErrDecision.Error()+": "))
		}
		if err != nil {
			return o, nil, err
		}
		if sh, ok := out["short"].([]string); ok && sh != nil {
			short = sh
		}
	}
	return s.mirrorLinkedOrder(ctx, tenant, o.ID), short, nil
}

// mirrorLinkedOrder mirrors a seller's order to its buyer's purchase order
// now (the worker would, in a few seconds), tells the buyer, and answers
// the order as it now is.
func (s *Server) mirrorLinkedOrder(ctx context.Context, seller, id uuid.UUID) links.Order {
	var told *links.Told
	if err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{seller}, func(tx pgx.Tx) error {
		var err error
		told, err = links.MirrorOrderTx(ctx, tx, seller, id)
		return err
	}); err != nil {
		s.Logger.Warn("links: mirroring an order (the worker tries again)", "tenant", seller, "err", err)
	}
	s.tellOrder(ctx, told, links.StepIn{})
	var o links.Order
	_ = db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{seller}, func(tx pgx.Tx) error {
		var err error
		o, err = links.GetOrderTx(ctx, tx, id.String(), false)
		return err
	})
	return o
}

// acceptedByBuyer moves a seller's order the buyer's step accepted (its
// change, or the seller's draft) as an acceptance moves any order: its
// stock lowered. The order is accepted already; Decide finishes it.
func (s *Server) acceptedByBuyer(ctx context.Context, told *links.Told) {
	if told == nil || s.Orders == nil {
		return
	}
	var ref string
	if err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{told.Tenant}, func(tx pgx.Tx) error {
		o, err := links.GetOrderTx(ctx, tx, told.Order.String(), false)
		ref = o.OrderReference
		return err
	}); err != nil {
		s.Logger.Warn("links: reading an accepted order", "err", err)
		return
	}
	if _, err := s.Orders.Decide(ctx, told.Tenant, ref, "accept", "", "link:"+told.From); err != nil {
		s.Logger.Warn("links: lowering stock for an order the buyer accepted", "tenant", told.Tenant, "err", err)
	}
	s.mirrorLinkedOrder(ctx, told.Tenant, told.Order)
}

func actorOf(p *Principal) links.Actor {
	t, id, _ := strings.Cut(p.Actor(), ":")
	if id == "" {
		return links.Actor{Type: "user", ID: t}
	}
	return links.Actor{Type: t, ID: id}
}

func (s *Server) getLinkTrade(w http.ResponseWriter, r *http.Request) {
	id, err := idParam(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	var out links.Trade
	if err := s.tx(r, func(tx pgx.Tx) error {
		if _, err := links.GetTx(r.Context(), tx, id); err != nil {
			return err
		}
		out, err = links.TradeTx(r.Context(), tx, principalFrom(r.Context()).TenantID, id)
		return err
	}); err != nil {
		s.fail(w, r, orderErr(err))
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// putLinkTrade is PUT /v1/links/{id}/trade: how the business trades with
// this linked business's orders: when it raises their invoice (accepted
// or delivered), and whether it accepts them by themselves. A PUT changes
// only what it names.
func (s *Server) putLinkTrade(w http.ResponseWriter, r *http.Request) {
	id, err := idParam(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	var in struct {
		InvoiceAt  *string `json:"invoice_at"`
		AutoAccept *bool   `json:"auto_accept"`
	}
	if err := decodeBody(r, &in); err != nil {
		s.fail(w, r, err)
		return
	}
	p := principalFrom(r.Context())
	var t links.Trade
	if err := s.tx(r, func(tx pgx.Tx) error {
		var err error
		if t, err = links.TradeTx(r.Context(), tx, p.TenantID, id); err != nil {
			return err
		}
		if in.InvoiceAt != nil {
			t.InvoiceAt = *in.InvoiceAt
		}
		if in.AutoAccept != nil {
			t.AutoAccept = *in.AutoAccept
		}
		if err := links.SetTradeTx(r.Context(), tx, p.TenantID, id, t, p.Actor()); err != nil {
			return err
		}
		return auditTx(r, tx, "link.trade_settings", id.String(), map[string]any{"invoice_at": t.InvoiceAt, "auto_accept": t.AutoAccept})
	}); err != nil {
		s.fail(w, r, orderErr(err))
		return
	}
	writeJSON(w, http.StatusOK, t)
}

// linkSupplier is PUT /v1/stock/suppliers/{id}/link: the supplier is a
// linked business (link_id), or no longer (null).
func (s *Server) linkSupplier(w http.ResponseWriter, r *http.Request) {
	if !s.purchasesReady(w) {
		return
	}
	id, err := idParam(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	var in struct {
		LinkID *uuid.UUID `json:"link_id"`
	}
	if err := decodeBody(r, &in); err != nil {
		s.fail(w, r, err)
		return
	}
	p := principalFrom(r.Context())
	var x stock.Supplier
	if err := s.tx(r, func(tx pgx.Tx) error {
		var err error
		if x, err = s.Purchases.LinkSupplierTx(r.Context(), tx, p.TenantID, id, in.LinkID, p.Actor()); err != nil {
			return err
		}
		return auditTx(r, tx, "supplier.link", x.Name, map[string]any{"supplier_id": x.ID, "link": in.LinkID})
	}); err != nil {
		s.purchaseFail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"supplier": x})
}

// purchaseAfter answers with the purchase order as it now is, read again
// in the business's own scope (the step's transaction ended in the
// other's).
func (s *Server) purchaseAfter(w http.ResponseWriter, r *http.Request, id uuid.UUID, extra map[string]any) {
	var x stock.Purchase
	if err := s.tx(r, func(tx pgx.Tx) error {
		var err error
		x, err = stock.PurchaseTx(r.Context(), tx, principalFrom(r.Context()).TenantID, id, false)
		return err
	}); err != nil {
		s.purchaseFail(w, r, err)
		return
	}
	out := map[string]any{"purchase": x}
	for k, v := range extra {
		out[k] = v
	}
	writeJSON(w, http.StatusOK, out)
}

// cancelLinkedPurchase cancels an order sent to a linked supplier: on both
// sides before the seller accepts it, else as a request the seller answers.
func (s *Server) cancelLinkedPurchase(w http.ResponseWriter, r *http.Request, id uuid.UUID) {
	p := principalFrom(r.Context())
	var how string
	var told *links.Told
	if err := s.tx(r, func(tx pgx.Tx) error {
		x, err := stock.PurchaseTx(r.Context(), tx, p.TenantID, id, true)
		if err != nil {
			return err
		}
		if err := auditTx(r, tx, "purchase.cancel", x.Reference, map[string]any{"purchase_id": x.ID, "linked": true}); err != nil {
			return err
		}
		how, told, err = links.CancelOrderTx(r.Context(), tx, p.TenantID, id, actorOf(p))
		return err
	}); err != nil {
		s.fail(w, r, orderErr(err))
		return
	}
	s.tellOrder(r.Context(), told, links.StepIn{})
	s.purchaseAfter(w, r, id, map[string]any{"cancel": how})
}

// acceptPurchaseChange is POST /v1/stock/purchases/{id}/accept-change: the
// linked supplier's change taken, and the order accepted as it proposed.
func (s *Server) acceptPurchaseChange(w http.ResponseWriter, r *http.Request) {
	id, err := idParam(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	p := principalFrom(r.Context())
	var told *links.Told
	if err := s.tx(r, func(tx pgx.Tx) error {
		x, err := stock.PurchaseTx(r.Context(), tx, p.TenantID, id, true)
		if err != nil {
			return err
		}
		if err := auditTx(r, tx, "purchase.accept_change", x.Reference, map[string]any{"purchase_id": x.ID}); err != nil {
			return err
		}
		told, err = links.AcceptChangeTx(r.Context(), tx, p.TenantID, id, actorOf(p))
		return err
	}); err != nil {
		s.purchaseOrOrderFail(w, r, err)
		return
	}
	s.acceptedByBuyer(r.Context(), told)
	s.tellOrder(r.Context(), told, links.StepIn{})
	s.purchaseAfter(w, r, id, nil)
}

func (s *Server) purchaseOrOrderFail(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, stock.ErrNoPurchase) || errors.Is(err, stock.ErrPurchase) {
		s.purchaseFail(w, r, err)
		return
	}
	s.fail(w, r, orderErr(err))
}

// allCame is POST /v1/stock/purchases/{id}/all-came: what a linked
// supplier delivered came, recorded as received, and the supplier told.
func (s *Server) allCame(w http.ResponseWriter, r *http.Request) {
	s.receiptStep(w, r, false)
}

// notAllCame is POST /v1/stock/purchases/{id}/not-all-came: what came of
// each line, by the buyer's count, recorded as received, and the
// supplier told, who agrees the count or talks to the buyer.
func (s *Server) notAllCame(w http.ResponseWriter, r *http.Request) {
	s.receiptStep(w, r, true)
}

func (s *Server) receiptStep(w http.ResponseWriter, r *http.Request, short bool) {
	if !s.purchasesReady(w) {
		return
	}
	id, err := idParam(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	var in struct {
		Counts []int64 `json:"counts"`
	}
	if short {
		if err := decodeBody(r, &in); err != nil {
			s.fail(w, r, err)
			return
		}
		if in.Counts == nil {
			s.fail(w, r, fmt.Errorf("%w: say how many came of each item", errBadRequest))
			return
		}
	}
	p := principalFrom(r.Context())
	told, err := s.settleReceipt(r.Context(), p.TenantID, id, in.Counts, actorOf(p), p.Actor(), func(tx pgx.Tx, ref string) error {
		return auditTx(r, tx, map[bool]string{false: "purchase.all_came", true: "purchase.not_all_came"}[short], ref, map[string]any{"purchase_id": id})
	})
	if err != nil {
		s.purchaseOrOrderFail(w, r, err)
		return
	}
	s.tellOrder(r.Context(), told, links.StepIn{Delivered: in.Counts})
	s.purchaseAfter(w, r, id, nil)
}

// settleReceipt records what came of a delivered linked order (the
// seller's count, or the buyer's when short is given) and settles the
// receipt with the seller, in one transaction; then sends the stock
// movements. audit writes the business's own record of who did it, before
// the transaction enters the seller's side.
func (s *Server) settleReceipt(ctx context.Context, tenant, id uuid.UUID, short []int64, by links.Actor, actor string, audit func(tx pgx.Tx, ref string) error) (*links.Told, error) {
	var told *links.Told
	var moves []stock.Movement
	var costs []stock.Cost
	key := "link-receipt-" + id.String()
	err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		got, ref, err := links.ToReceive(ctx, tx, id, short)
		if err != nil {
			return err
		}
		if audit != nil {
			if err := audit(tx, ref); err != nil {
				return err
			}
		}
		if len(got) > 0 {
			if _, moves, costs, err = stock.ReceiveTx(ctx, tx, tenant, id, got, actor, key); err != nil {
				return err
			}
		}
		told, err = links.ReceiptTx(ctx, tx, tenant, id, links.Receipt{Short: short}, by)
		return err
	})
	if err != nil {
		return nil, err
	}
	s.Purchases.Apply(ctx, tenant, moves, costs, key)
	return told, nil
}

// --- the worker ---

// LinkOrdersTick does the linked-order work due: orders to write into
// sellers' businesses, invoices to raise, receipts to settle after a day.
// It reports how many it did; one that cannot be done yet stays due.
func (s *Server) LinkOrdersTick(ctx context.Context) (int, error) {
	list, err := links.ClaimOrders(ctx, s.Store.Pool, 50)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, d := range list {
		var err error
		switch d.Kind {
		case "order":
			err = s.writeLinkedOrder(ctx, d.Tenant, d.ID)
		case "mirror":
			err = db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{d.Tenant}, func(tx pgx.Tx) error {
				told, err := links.MirrorOrderTx(ctx, tx, d.Tenant, d.ID)
				if err == nil {
					defer s.tellOrder(ctx, told, links.StepIn{})
				}
				return err
			})
		case "invoice":
			err = s.raiseOrderInvoice(ctx, d.Tenant, d.ID)
		case "receipt":
			err = s.settleReceiptLater(ctx, d.Tenant, d.ID)
		case "prices":
			err = db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{d.Tenant}, func(tx pgx.Tx) error {
				return links.MirrorPricesTx(ctx, tx, d.Tenant, d.ID)
			})
		}
		if err != nil {
			s.Logger.Warn("links: linked-order work", "kind", d.Kind, "tenant", d.Tenant, "id", d.ID, "err", err)
			continue
		}
		n++
	}
	return n, nil
}

func (s *Server) writeLinkedOrder(ctx context.Context, buyer, id uuid.UUID) error {
	if s.Orders == nil {
		return errors.New("storefront orders are not configured")
	}
	var told *links.Told
	err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{buyer}, func(tx pgx.Tx) error {
		var err error
		told, err = links.WriteOrderTx(ctx, tx, s.Vault, buyer, id)
		return err
	})
	if err != nil || told == nil {
		return err
	}
	// In the seller's own scope: its lines matched to its items (and its
	// prices); accepted by itself where it set that for the link; and its
	// order workflow started, as for any order.
	seller := told.Tenant
	o := s.matchTradeItems(ctx, seller, told.Order)
	if o != nil && o.AutoAccept && o.Status == links.OrderReceived && allPriced(o.Lines) {
		if _, _, err := s.sellerStep(ctx, seller, o.ID.String(), links.StepAccept, links.StepIn{}, links.Actor{Type: "system", ID: "link-orders"},
			"system:link-orders", nil); err == nil {
			told.Event = "auto_accepted"
		} else {
			s.Logger.Warn("links: accepting an order by itself", "tenant", seller, "err", err)
		}
	}
	note := ""
	if o != nil {
		note = linesWords(o.Lines) + ", " + storefront.FormatNaira(linesKobo(o.Lines))
		if err := s.Orders.StartTrade(ctx, seller, o.OrderReference); err != nil {
			s.Logger.Warn("links: starting the order workflow", "tenant", seller, "err", err)
		}
	}
	s.tellOrder(ctx, told, links.StepIn{Note: note})
	return nil
}

func allPriced(lines []links.OrderLine) bool {
	for _, l := range lines {
		if l.PriceKobo < 1 {
			return false
		}
	}
	return true
}

func linesKobo(lines []links.OrderLine) int64 {
	var t int64
	for _, l := range lines {
		t += l.Quantity * l.PriceKobo
	}
	return t
}

// linesWords says an order's lines: "10 bags Rice, 5 cartons Indomie".
func linesWords(lines []links.OrderLine) string {
	parts := make([]string, len(lines))
	for i, l := range lines {
		parts[i] = (stock.Item{Unit: l.Unit}).Quantity(l.Quantity) + " " + l.Item
	}
	return strings.Join(parts, ", ")
}

// sellerItems are a business's own catalogue items, for matching an
// order's lines; none when it keeps no stock on Taskiem.
func (s *Server) sellerItems(ctx context.Context, tenant uuid.UUID) []links.SellerItem {
	if s.Stock == nil || !s.Stock.Ready() {
		return nil
	}
	_, items, err := s.stockItems(ctx, tenant)
	if err != nil {
		return nil
	}
	out := make([]links.SellerItem, len(items))
	for i, it := range items {
		out[i] = links.SellerItem{ID: it.ID, Name: it.Name, Unit: it.Unit, PriceKobo: it.PriceKobo}
	}
	return out
}

// matchTradeItems matches a new order's lines to the seller's own items
// and answers the order (nil when it cannot be read).
func (s *Server) matchTradeItems(ctx context.Context, seller, order uuid.UUID) *links.Order {
	items := s.sellerItems(ctx, seller)
	var o links.Order
	err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{seller}, func(tx pgx.Tx) error {
		var err error
		o, _, err = links.MatchItemsTx(ctx, tx, order.String(), items)
		return err
	})
	if err != nil {
		s.Logger.Warn("links: matching an order's items", "tenant", seller, "err", err)
		return nil
	}
	return &o
}

// raiseOrderInvoice raises the invoice for a linked order, in the seller's
// scope, to the buyer as a linked customer (so it mirrors as a bill), and
// sends it; one raised and not yet sent is sent again under the same key.
func (s *Server) raiseOrderInvoice(ctx context.Context, seller, id uuid.UUID) error {
	if s.Invoices == nil {
		return errors.New("invoices are not configured")
	}
	var invoice *uuid.UUID
	err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{seller}, func(tx pgx.Tx) error {
		o, err := links.GetOrderTx(ctx, tx, id.String(), true)
		if errors.Is(err, links.ErrNotFound) {
			return links.Undue(ctx, tx, seller, "invoice", id)
		}
		if err != nil {
			return err
		}
		if o.InvoiceID != nil {
			invoice = o.InvoiceID
			return nil
		}
		if o.Status == links.OrderCancelled || o.Status == links.OrderDeclined {
			return links.Undue(ctx, tx, seller, "invoice", id)
		}
		var lines []invoicing.Line
		for _, l := range o.InvoiceLines() {
			d := l.Item
			if l.Unit != "" {
				d += " (" + l.Unit + ")"
			}
			lines = append(lines, invoicing.Line{Description: d, Quantity: l.Quantity, UnitKobo: l.PriceKobo})
		}
		if len(lines) == 0 {
			return links.Undue(ctx, tx, seller, "invoice", id)
		}
		name := o.BuyerName
		if len([]rune(name)) > 120 {
			name = string([]rune(name)[:120])
		}
		x, err := s.Invoices.CreateOnce(ctx, tx, seller, "link-order:"+o.ID.String(), invoicing.Draft{
			Customer: invoicing.Customer{Name: name, Key: links.KeyPrefix + o.Buyer.String()},
			Lines:    lines, Note: "For order " + o.Reference + ".",
		}, "system:link-orders")
		if err != nil {
			return err
		}
		invoice = &x.ID
		return links.NoteInvoiceTx(ctx, tx, o.ID, x.ID)
	})
	if err != nil || invoice == nil {
		return err
	}
	if _, err := s.Invoices.Send(ctx, seller, invoice.String(), "system:link-orders", "link-order:"+id.String()); err != nil && !errors.Is(err, invoicing.ErrState) {
		return err
	}
	return db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{seller}, func(tx pgx.Tx) error {
		return links.SetInvoiceTx(ctx, tx, seller, id, *invoice)
	})
}

// settleReceiptLater settles a delivery's receipt from the seller's count,
// a day after it, when the buyer said neither All came nor Not all came.
func (s *Server) settleReceiptLater(ctx context.Context, buyer, id uuid.UUID) error {
	told, err := s.settleReceipt(ctx, buyer, id, nil, links.Actor{Type: "system", ID: "link-orders"}, "system:link-orders", nil)
	if errors.Is(err, links.ErrState) || errors.Is(err, links.ErrNotFound) {
		return db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{buyer}, func(tx pgx.Tx) error {
			return links.Undue(ctx, tx, buyer, "receipt", id)
		})
	}
	if err != nil {
		return err
	}
	s.tellOrder(ctx, told, links.StepIn{})
	return nil
}

// --- telling people ---

// tellOrder tells the other side's people what a step did to an order, in
// the bell and in chat, in their own scope.
func (s *Server) tellOrder(ctx context.Context, t *links.Told, in links.StepIn) {
	if t == nil {
		return
	}
	link := "/purchases/" + t.Purchase.String()
	if sellerTold(t.Event) {
		link = "/trade-orders/" + t.Order.String()
	}
	told := *t
	s.background(ctx, "linked order", func(ctx context.Context) {
		var sent func(channel, addr, id string)
		if card := s.orderCards(ctx, told); card != uuid.Nil {
			sent = s.keepSent(ctx, told.Tenant, told.Link, card)
		}
		note := in.Note
		if note == "" || told.Event == "change" || told.Event == "delivered" || told.Event == "short" {
			note = s.orderNote(ctx, told, in)
		}
		perms := []string{PermStorefrontManage}
		if told.Event == "delivered" {
			perms = []string{PermTableEdit} // who records what came
		}
		s.tellHoldersSent(ctx, told.Tenant, perms, "order:"+told.Reference+":"+told.Event, link, func(l lang.Tag) (string, string) {
			return tr(l, "order.link."+told.Event+"_title", "from", told.From, "reference", told.Reference),
				strings.TrimSpace(tr(l, "order.link."+told.Event, "from", whatsapp.SafeText(told.From), "reference", told.Reference, "note", whatsapp.SafeText(note)))
		}, func(l lang.Tag) []whatsapp.Button { return orderButtons(l, told) }, sent)
	})
}

// orderPrefix starts a linked order's buttons in chat: lo:<step>:<id>,
// the seller's order for accept and decline, the buyer's purchase order
// for all_came.
const orderPrefix = "lo:"

// orderButtons are the buttons a linked order's message carries: Accept
// and Decline on a new order, All came on a delivery.
func orderButtons(l lang.Tag, t links.Told) []whatsapp.Button {
	switch t.Event {
	case "ordered":
		return []whatsapp.Button{
			{ID: orderPrefix + links.StepAccept + ":" + t.Order.String(), Title: tr(l, "order.link.button_accept")},
			{ID: orderPrefix + links.StepDecline + ":" + t.Order.String(), Title: tr(l, "order.link.button_decline")},
		}
	case "delivered":
		return []whatsapp.Button{{ID: orderPrefix + "all_came:" + t.Purchase.String(), Title: tr(l, "order.link.button_all_came")}}
	}
	return nil
}

// waOrderPress takes a linked order's button: Accept or Decline on a new
// order, by the seller's people; All came on a delivery, by the buyer's;
// for whichever of the person's businesses holds it.
func (s *Server) waOrderPress(ctx context.Context, c *chat, reply string) error {
	step, idText, _ := strings.Cut(strings.TrimPrefix(reply, orderPrefix), ":")
	id, err := uuid.Parse(idText)
	if err != nil || !slices.Contains([]string{links.StepAccept, links.StepDecline, "all_came"}, step) {
		return c.say(ctx, s, c.t("order.link.gone"))
	}
	tenants := append([]tenantRef{c.tenant}, slices.DeleteFunc(slices.Clone(c.tenants), func(t tenantRef) bool { return t.ID == c.tenant.ID })...)
	for _, t := range tenants {
		var ref string
		if err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{t.ID}, func(tx pgx.Tx) error {
			if step == "all_came" {
				x, err := stock.PurchaseTx(ctx, tx, t.ID, id, false)
				ref = x.Reference
				return err
			}
			o, err := links.GetOrderTx(ctx, tx, id.String(), false)
			ref = o.Reference
			return err
		}); errors.Is(err, links.ErrNotFound) || errors.Is(err, stock.ErrNoPurchase) {
			continue
		} else if err != nil {
			return err
		}
		perm := PermStorefrontManage
		if step == "all_came" {
			perm = PermTableEdit
		}
		p, err := s.principalOf(ctx, t.ID, c.p.UserID)
		if err != nil || !p.Can(perm) {
			return c.say(ctx, s, c.t("order.link.denied", "org", t.Name))
		}
		var told *links.Told
		if step == "all_came" {
			told, err = s.settleReceipt(ctx, t.ID, id, nil, actorOf(p), p.Actor(), func(tx pgx.Tx, ref string) error {
				return waAudit(ctx, tx, c.ch.Name(), p, "purchase.all_came", ref, map[string]any{"purchase_id": id})
			})
		} else {
			_, _, err = s.sellerStep(ctx, t.ID, id.String(), step, links.StepIn{}, actorOf(p), p.Actor(), func(tx pgx.Tx, o links.Order) error {
				return waAudit(ctx, tx, c.ch.Name(), p, "trade_order."+step, o.Reference, map[string]any{"order_id": id})
			})
		}
		switch {
		case errors.Is(err, links.ErrState):
			return c.say(ctx, s, c.t("order.link.gone"))
		case errors.Is(err, links.ErrInvalid):
			return c.say(ctx, s, c.t("order.link.open", "reason", strings.TrimPrefix(err.Error(), links.ErrInvalid.Error()+": ")))
		case err != nil:
			return err
		}
		s.tellOrder(ctx, told, links.StepIn{})
		return c.say(ctx, s, c.t("order.link.done_"+step, "reference", ref))
	}
	return c.say(ctx, s, c.t("order.link.gone"))
}

// sellerTold reports whether an event is told to the seller (else to the
// buyer).
func sellerTold(event string) bool {
	switch event {
	case "ordered", "auto_accepted", "cancelled_by_buyer", "cancel_asked", "change_accepted", "received", "short", "confirmed":
		return true
	}
	return false
}

// orderNote says, for a change, a delivery or a short count, what the
// told side's own record now says, read in its own scope.
func (s *Server) orderNote(ctx context.Context, t links.Told, in links.StepIn) string {
	var note string
	_ = db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{t.Tenant}, func(tx pgx.Tx) error {
		if sellerTold(t.Event) {
			o, err := links.GetOrderTx(ctx, tx, t.Order.String(), false)
			if err != nil {
				return err
			}
			var parts []string
			for i, l := range o.Lines {
				if i < len(o.Short) {
					parts = append(parts, fmt.Sprintf("%d %s", o.Short[i], l.Item))
				}
			}
			note = strings.Join(parts, ", ")
			return nil
		}
		x, err := stock.PurchaseTx(ctx, tx, t.Tenant, t.Purchase, false)
		if err != nil {
			return err
		}
		word := ""
		if x.SellerNote != nil {
			word = *x.SellerNote
		}
		switch t.Event {
		case "change":
			if x.Counter != nil {
				note = *x.Counter
			}
		case "delivered":
			var parts []string
			for _, l := range x.Lines {
				if l.Delivered != nil {
					parts = append(parts, fmt.Sprintf("%d %s", *l.Delivered, l.Item))
				}
			}
			note = strings.Join(parts, ", ")
			if word != "" {
				note += ". " + word
			}
		case "proposed":
			note = in.Note
		default:
			note = word
		}
		return nil
	})
	return note
}

// TradeNotify tells a linked business what the seller's order workflow
// says to the order's customer (storefront.Orders.TradeNotify): in the
// bell and chat of the buyer's people, never by SMS.
func (s *Server) TradeNotify(ctx context.Context, seller uuid.UUID, ord storefront.Order, text string) error {
	var o links.Order
	var from string
	if err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{seller}, func(tx pgx.Tx) error {
		var err error
		if o, err = links.GetOrderTx(ctx, tx, ord.ID.String(), false); err != nil {
			return err
		}
		return tx.QueryRow(ctx, `SELECT name FROM tenants WHERE id = $1`, seller).Scan(&from)
	}); err != nil {
		return err
	}
	s.background(ctx, "linked order message", func(ctx context.Context) {
		s.tellHolders(ctx, o.Buyer, []string{PermStorefrontManage}, "order:"+o.Reference+":message:"+fmt.Sprint(len(text))+text[:min(len(text), 40)],
			"/purchases/"+o.Purchase.String(), func(l lang.Tag) (string, string) {
				return tr(l, "order.link.message_title", "from", from, "reference", o.Reference), whatsapp.SafeText(text)
			})
	})
	return nil
}

// deliverTo is GET and PUT /v1/stock/deliver-to: where linked suppliers
// deliver the business's orders (the address, and the number a rider
// calls); an empty address, picked up.
func (s *Server) getDeliverTo(w http.ResponseWriter, r *http.Request) {
	var d links.DeliverTo
	if err := s.tx(r, func(tx pgx.Tx) error {
		var err error
		d, err = links.DeliverToTx(r.Context(), tx, s.Vault, principalFrom(r.Context()).TenantID)
		return err
	}); err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, d)
}

func (s *Server) putDeliverTo(w http.ResponseWriter, r *http.Request) {
	if !s.purchasesReady(w) {
		return
	}
	var d links.DeliverTo
	if err := decodeBody(r, &d); err != nil {
		s.fail(w, r, err)
		return
	}
	if strings.TrimSpace(d.Address) != "" {
		e164, err := s.Purchases.Book.Phone(d.Phone)
		if err != nil {
			s.fail(w, r, fmt.Errorf("%w: %s is not a mobile number Taskiem can reach (like 0803 123 4567)", errBadRequest, strings.TrimSpace(d.Phone)))
			return
		}
		d.Phone = e164
	} else {
		d = links.DeliverTo{}
	}
	p := principalFrom(r.Context())
	if err := s.tx(r, func(tx pgx.Tx) error {
		if err := links.SetDeliverToTx(r.Context(), tx, s.Vault, p.TenantID, d, p.Actor()); err != nil {
			return err
		}
		return auditTx(r, tx, "stock.deliver_to", p.TenantID.String(), map[string]any{"delivered": d.Address != ""})
	}); err != nil {
		s.fail(w, r, orderErr(err))
		return
	}
	s.getDeliverTo(w, r)
}

// itemsByID are a business's own catalogue items, by row id.
func (s *Server) itemsByID(ctx context.Context, tenant uuid.UUID) (map[string]stock.Item, error) {
	if s.Stock == nil || !s.Stock.Ready() {
		return nil, fmt.Errorf("%w: keep your stock on Taskiem to choose your items", errConflict)
	}
	_, items, err := s.stockItems(ctx, tenant)
	if err != nil {
		return nil, err
	}
	out := make(map[string]stock.Item, len(items))
	for _, it := range items {
		out[it.ID] = it
	}
	return out, nil
}

// setTradeLineItem is PUT /v1/trade-orders/{id}/lines/{line}/item: which
// of our items a new order's line is, remembered for the link's next
// orders (§17), the line taking our price.
func (s *Server) setTradeLineItem(w http.ResponseWriter, r *http.Request) {
	line, err := strconv.Atoi(chi.URLParam(r, "line"))
	if err != nil {
		s.fail(w, r, pgx.ErrNoRows)
		return
	}
	var in struct {
		ItemID string `json:"item_id"`
	}
	if err := decodeBody(r, &in); err != nil {
		s.fail(w, r, err)
		return
	}
	p := principalFrom(r.Context())
	items, err := s.itemsByID(r.Context(), p.TenantID)
	if err != nil {
		s.stockFail(w, r, err)
		return
	}
	it, ok := items[in.ItemID]
	if !ok {
		s.fail(w, r, fmt.Errorf("%w: that is not one of your items", errBadRequest))
		return
	}
	var out tradeOrderOut
	if err := s.tx(r, func(tx pgx.Tx) error {
		var err error
		if out.Order, err = links.SetLineItemTx(r.Context(), tx, chi.URLParam(r, "id"), line, links.SellerItem{ID: it.ID, Name: it.Name, Unit: it.Unit,
			PriceKobo: it.PriceKobo}, p.Actor()); err != nil {
			return err
		}
		return auditTx(r, tx, "trade_order.item", out.Order.Reference, map[string]any{"order_id": out.Order.ID, "line": line, "item_id": it.ID})
	}); err != nil {
		s.fail(w, r, orderErr(err))
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// placeTradeOrder is POST /v1/trade-orders: an order we draft for a
// linked business that buys from us (a phone order, §7). It reaches the
// buyer as a purchase order to us waiting for its Confirm. Each line is
// one of our items (item_id, at its price unless one is given) or named
// with its price.
func (s *Server) placeTradeOrder(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Link  uuid.UUID `json:"link"`
		Lines []struct {
			ItemID    string `json:"item_id"`
			Item      string `json:"item"`
			Unit      string `json:"unit"`
			Quantity  int64  `json:"quantity"`
			PriceKobo *int64 `json:"price_kobo"`
		} `json:"lines"`
		Note string `json:"note"`
	}
	if err := decodeBody(r, &in); err != nil {
		s.fail(w, r, err)
		return
	}
	p := principalFrom(r.Context())
	var items map[string]stock.Item
	lines := make([]links.OrderLine, len(in.Lines))
	for i, l := range in.Lines {
		x := links.OrderLine{Item: l.Item, Unit: l.Unit, Quantity: l.Quantity}
		if l.PriceKobo != nil {
			x.PriceKobo = *l.PriceKobo
		}
		if l.ItemID != "" {
			if items == nil {
				var err error
				if items, err = s.itemsByID(r.Context(), p.TenantID); err != nil {
					s.stockFail(w, r, err)
					return
				}
			}
			it, ok := items[l.ItemID]
			if !ok {
				s.fail(w, r, fmt.Errorf("%w: line %d is not one of your items", errBadRequest, i+1))
				return
			}
			x.Item, x.Unit, x.ItemID, x.Matched = it.Name, it.Unit, it.ID, "linked"
			if l.PriceKobo == nil && it.PriceKobo != nil {
				x.PriceKobo = *it.PriceKobo
			}
		}
		lines[i] = x
	}
	var out tradeOrderOut
	var told *links.Told
	if err := s.tx(r, func(tx pgx.Tx) error {
		if err := auditTx(r, tx, "trade_order.place", in.Link.String(), map[string]any{"lines": len(lines)}); err != nil {
			return err
		}
		var err error
		out.Order, told, err = links.PlaceOrderTx(r.Context(), tx, s.Vault, p.TenantID, in.Link, lines, in.Note, actorOf(p))
		return err
	}); err != nil {
		s.fail(w, r, orderErr(err))
		return
	}
	s.tellOrder(r.Context(), told, links.StepIn{Note: linesWords(out.Order.Lines) + ", " + storefront.FormatNaira(out.Order.TotalKobo)})
	writeJSON(w, http.StatusCreated, out)
}

// confirmProposed is POST /v1/stock/purchases/{id}/confirm-proposed: an
// order a linked supplier drafted for us, confirmed, each line said to be
// one of our items (items, in order). It is then accepted on both sides.
func (s *Server) confirmProposed(w http.ResponseWriter, r *http.Request) {
	if !s.purchasesReady(w) {
		return
	}
	id, err := idParam(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	var in struct {
		Items []string `json:"items"`
	}
	if err := decodeBody(r, &in); err != nil {
		s.fail(w, r, err)
		return
	}
	p := principalFrom(r.Context())
	byID, err := s.itemsByID(r.Context(), p.TenantID)
	if err != nil {
		s.stockFail(w, r, err)
		return
	}
	items := make([]links.BuyerItem, len(in.Items))
	for i, x := range in.Items {
		it, ok := byID[x]
		if !ok {
			s.fail(w, r, fmt.Errorf("%w: line %d is not one of your items", errBadRequest, i+1))
			return
		}
		items[i] = links.BuyerItem{ID: it.ID, Name: it.Name, Unit: it.Unit}
	}
	var told *links.Told
	if err := s.tx(r, func(tx pgx.Tx) error {
		x, err := stock.PurchaseTx(r.Context(), tx, p.TenantID, id, true)
		if err != nil {
			return err
		}
		if err := auditTx(r, tx, "purchase.confirm_proposed", x.Reference, map[string]any{"purchase_id": x.ID}); err != nil {
			return err
		}
		told, err = links.ConfirmProposedTx(r.Context(), tx, s.Vault, p.TenantID, id, items, p.Actor(), actorOf(p))
		return err
	}); err != nil {
		s.purchaseOrOrderFail(w, r, err)
		return
	}
	s.acceptedByBuyer(r.Context(), told)
	s.tellOrder(r.Context(), told, links.StepIn{})
	s.purchaseAfter(w, r, id, nil)
}
