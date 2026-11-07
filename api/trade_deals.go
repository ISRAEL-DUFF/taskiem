package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/invoicing"
	"github.com/israel-duff/taskiem/engine/lang"
	"github.com/israel-duff/taskiem/engine/links"
	"github.com/israel-duff/taskiem/engine/stock"
	"github.com/israel-duff/taskiem/engine/whatsapp"
)

// Deals (chat tools phase 7, docs/spec/chat-tools.md §9): wholesale price
// deliberation between linked businesses, on the order flow. The buyer
// offers (POST /v1/supplier-prices/{link}/order with offer), the seller
// counters (its change) or accepts, the buyer counters back
// (POST /v1/stock/purchases/{id}/counter) or accepts the change. Those who
// work the orders without the storefront's say over prices (a cashier)
// may accept or counter an offer at or above the floor; below it their
// accept waits for the owner, and their counter is refused.

func (s *Server) tradeDealRoutes(r chi.Router) {
	manage := s.need(PermStorefrontManage)
	r.With(s.need(PermTableEdit), s.tenantWide).Post("/stock/purchases/{id}/counter", s.counterPurchase)
	r.With(manage).Get("/trade-floors", s.getTradeFloors)
	r.With(manage, s.tenantWide).Put("/trade-floors", s.putTradeFloor)
}

// personName is who a principal is, as a deal names them ("Tunde").
func personName(ctx context.Context, tx pgx.Tx, p *Principal) string {
	if p.UserID == uuid.Nil {
		return ""
	}
	var name, email string
	if err := tx.QueryRow(ctx, `SELECT name, email FROM users WHERE id = $1`, p.UserID).Scan(&name, &email); err != nil {
		return ""
	}
	if n := strings.TrimSpace(name); n != "" {
		return strings.Fields(n)[0]
	}
	return strings.Split(email, "@")[0]
}

// dealPrices is a counter's prices: one per line, or a total spread over
// the lines in proportion to the prices they stand at.
func dealPrices(cur []int64, quantities []int64, prices []int64, total int64) ([]int64, bool, error) {
	if total > 0 {
		out, err := links.SpreadTotal(cur, quantities, total)
		return out, true, err
	}
	if len(prices) != len(cur) {
		return nil, false, fmt.Errorf("%w: one price for each line, or a total", links.ErrInvalid)
	}
	return prices, false, nil
}

// counterPurchase is POST /v1/stock/purchases/{id}/counter: the buyer's
// counter to its supplier's change ({prices: [...]} one per line, or
// {total_kobo}, and a note), back to the supplier as an offer.
func (s *Server) counterPurchase(w http.ResponseWriter, r *http.Request) {
	id, err := idParam(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	var in struct {
		Prices    []int64 `json:"prices"`
		TotalKobo int64   `json:"total_kobo"`
		Note      string  `json:"note"`
	}
	if err := decodeBody(r, &in); err != nil {
		s.fail(w, r, err)
		return
	}
	p := principalFrom(r.Context())
	told, err := s.counter(r.Context(), p, id, in.Prices, in.TotalKobo, in.Note, func(tx pgx.Tx, x stock.Purchase) error {
		return auditTx(r, tx, "purchase.counter", x.Reference, map[string]any{"purchase_id": x.ID})
	})
	if err != nil {
		s.purchaseOrOrderFail(w, r, err)
		return
	}
	// The seller's side was written in the same transaction; the buyer's
	// copy follows from it at once (as an accepted change does).
	if told != nil {
		s.mirrorLinkedOrder(r.Context(), told.Tenant, told.Order)
	}
	s.tellOrder(r.Context(), told, links.StepIn{})
	s.purchaseAfter(w, r, id, nil)
}

// counter takes the buyer's counter (the web, the API and chat).
func (s *Server) counter(ctx context.Context, p *Principal, id uuid.UUID, prices []int64, total int64, note string, audit func(tx pgx.Tx, x stock.Purchase) error) (*links.Told, error) {
	var told *links.Told
	err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{p.TenantID}, func(tx pgx.Tx) error {
		x, err := stock.PurchaseTx(ctx, tx, p.TenantID, id, true)
		if err != nil {
			return err
		}
		if audit != nil {
			if err := audit(tx, x); err != nil {
				return err
			}
		}
		// The prices stand against the change's lines (its quantities).
		cur, qs := make([]int64, len(x.Change)), make([]int64, len(x.Change))
		for i, l := range x.Change {
			cur[i], qs[i] = l.CostKobo, l.Quantity
		}
		got, asTotal, err := dealPrices(cur, qs, prices, total)
		if err != nil {
			return err
		}
		told, err = links.CounterTx(ctx, tx, p.TenantID, id, got, asTotal, note, personName(ctx, tx, p), actorOf(p))
		return err
	})
	return told, err
}

// dealCheck is why an order's offer, or a counter's lines, are past what
// those without the storefront's say may agree (nothing: within).
func dealCheck(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, lines []links.OrderLine) ([]string, error) {
	floors, err := links.FloorsTx(ctx, tx, tenant)
	if err != nil {
		return nil, err
	}
	set, err := links.DealSettingsTx(ctx, tx, tenant)
	if err != nil {
		return nil, err
	}
	return links.BelowFloor(lines, floors, set), nil
}

// errOwnerAsked: an accept below the floor, waiting for the owner.
type errOwnerAsked struct{ why []string }

func (e errOwnerAsked) Error() string { return "waiting for the owner: " + strings.Join(e.why, "; ") }

// dealGate is the floor's rule for a step on an offer by those without
// the storefront's say over prices: an accept below the floor is put to
// the owner (errOwnerAsked, after it is noted and they are told); a
// counter below it is refused. Others' steps, and orders that are no
// deal, pass.
func (s *Server) dealGate(ctx context.Context, p *Principal, ref, step string, in links.StepIn) error {
	if p.Can(PermStorefrontManage) || (step != links.StepAccept && step != links.StepChange) {
		return nil
	}
	var o links.Order
	var why []string
	asked := false
	err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{p.TenantID}, func(tx pgx.Tx) error {
		var err error
		if o, err = links.GetOrderTx(ctx, tx, ref, true); err != nil {
			return err
		}
		if !links.IsOffer(o.Lines) {
			return nil
		}
		lines := o.Lines
		if step == links.StepChange {
			if len(in.Lines) != len(o.Lines) {
				return nil // the change's own check says so
			}
			lines = make([]links.OrderLine, len(o.Lines))
			for i, l := range o.Lines {
				l.Quantity, l.PriceKobo = in.Lines[i].Quantity, in.Lines[i].PriceKobo
				lines[i] = l
			}
		}
		if why, err = dealCheck(ctx, tx, p.TenantID, lines); err != nil || len(why) == 0 {
			return err
		}
		if step == links.StepChange {
			return nil
		}
		asked, err = links.AskOwnerTx(ctx, tx, o.ID, personName(ctx, tx, p))
		return err
	})
	if err != nil || len(why) == 0 {
		return err
	}
	if step == links.StepChange {
		return fmt.Errorf("%w: %s; the owner can go lower", errForbidden, strings.Join(why, "; "))
	}
	if asked {
		s.tellDealOwner(ctx, p, o, why)
	}
	return errOwnerAsked{why: why}
}

// tellDealOwner tells those who set the storefront's prices that staff
// would agree an offer below the floor: the bell and their chat, and the
// card in the Taskiem conversation (dealAlerts).
func (s *Server) tellDealOwner(ctx context.Context, p *Principal, o links.Order, why []string) {
	who := ""
	_ = db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{p.TenantID}, func(tx pgx.Tx) error {
		who = personName(ctx, tx, p)
		return nil
	})
	total := orderTotal(o.Lines)
	s.tellHolders(ctx, p.TenantID, []string{PermStorefrontManage}, "deal", "/chat?with=taskiem", func(l lang.Tag) (string, string) {
		t := tr(l, "deal.owner_title", "who", whatsapp.SafeText(who), "amount", invoicing.Naira(total), "business", whatsapp.SafeText(o.BuyerName))
		return t, tr(l, "deal.owner_body", "reference", o.OrderReference, "why", strings.Join(why, "; "))
	})
}

// orderTotal is what lines come to.
func orderTotal(lines []links.OrderLine) int64 {
	var t int64
	for _, l := range lines {
		t += l.Quantity * l.PriceKobo
	}
	return t
}

// keepDealPrices keeps an accepted offer's prices for the buyer for some
// days (Keep this price for them), as its own price over the list.
func (s *Server) keepDealPrices(ctx context.Context, p *Principal, o links.Order, days int) error {
	if days <= 0 {
		return nil
	}
	if days != 7 && days != 14 && days != 30 {
		return fmt.Errorf("%w: keep a price for 7, 14 or 30 days", links.ErrInvalid)
	}
	prices := map[string]int64{}
	for _, l := range o.Lines {
		if l.ItemID != "" && l.PriceKobo > 0 {
			prices[l.ItemID] = l.PriceKobo
		}
	}
	if len(prices) == 0 {
		return nil
	}
	if err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{p.TenantID}, func(tx pgx.Tx) error {
		return links.KeepPricesTx(ctx, tx, p.TenantID, o.LinkID, prices, time.Now().Add(time.Duration(days)*24*time.Hour), p.Actor())
	}); err != nil {
		return err
	}
	s.mirrorPrices(ctx, p.TenantID, []uuid.UUID{o.LinkID})
	return nil
}

// tradeFloorsOut is GET /v1/trade-floors.
type tradeFloorsOut struct {
	Floors         map[string]int64 `json:"floors"`
	MaxDiscountPct *int             `json:"max_discount_pct"`
}

// getTradeFloors is GET /v1/trade-floors: the lowest price of each item
// that has one, and the largest discount on a whole order.
func (s *Server) getTradeFloors(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r.Context())
	out := tradeFloorsOut{Floors: map[string]int64{}}
	if err := s.tx(r, func(tx pgx.Tx) error {
		var err error
		if out.Floors, err = links.FloorsTx(r.Context(), tx, p.TenantID); err != nil {
			return err
		}
		set, err := links.DealSettingsTx(r.Context(), tx, p.TenantID)
		out.MaxDiscountPct = set.MaxDiscountPct
		return err
	}); err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// putTradeFloor is PUT /v1/trade-floors: {item_id, floor_kobo} (0: none)
// for an item, and/or {max_discount_pct} (null: no such limit) when
// set_discount is true.
func (s *Server) putTradeFloor(w http.ResponseWriter, r *http.Request) {
	var in struct {
		ItemID         string `json:"item_id"`
		FloorKobo      int64  `json:"floor_kobo"`
		SetDiscount    bool   `json:"set_discount"`
		MaxDiscountPct *int   `json:"max_discount_pct"`
	}
	if err := decodeBody(r, &in); err != nil {
		s.fail(w, r, err)
		return
	}
	p := principalFrom(r.Context())
	if err := s.tx(r, func(tx pgx.Tx) error {
		if in.ItemID != "" {
			if err := links.SetFloorTx(r.Context(), tx, p.TenantID, in.ItemID, in.FloorKobo, p.Actor()); err != nil {
				return err
			}
		}
		if in.SetDiscount {
			if err := links.SetDealSettingsTx(r.Context(), tx, p.TenantID, links.DealSettings{MaxDiscountPct: in.MaxDiscountPct}, p.Actor()); err != nil {
				return err
			}
		}
		return auditTx(r, tx, "trade.floor", in.ItemID, map[string]any{"floor_kobo": in.FloorKobo, "max_discount_pct": in.MaxDiscountPct})
	}); err != nil {
		s.fail(w, r, linkErr(err))
		return
	}
	s.getTradeFloors(w, r)
}

// dealAlerts are offers staff would accept below the floor, for those who
// set the storefront's prices: Approve accepts it, Decline declines it.
func (s *Server) dealAlerts(ctx context.Context, p *Principal) ([]chatAlert, error) {
	if !p.Can(PermStorefrontManage) {
		return nil, nil
	}
	var list []links.Order
	if err := db0(ctx, s, p.TenantID, func(tx pgx.Tx) error {
		var err error
		list, err = links.ListOrdersTx(ctx, tx, true, 200)
		return err
	}); err != nil {
		return nil, err
	}
	var out []chatAlert
	for _, o := range list {
		if o.Status != links.OrderReceived || o.DealOwnerAt == nil {
			continue
		}
		total := orderTotal(o.Lines)
		line := o.OrderReference
		if was := dealWas(o.Lines); was > total {
			line += " · was " + invoicing.Naira(was)
		}
		out = append(out, chatAlert{Key: "deal:" + o.ID.String() + ":" + o.DealOwnerAt.Format(time.RFC3339), Kind: "deal", At: *o.DealOwnerAt, ID: o.ID.String(),
			Reference: o.OrderReference, Who: o.DealOwnerBy, AmountKobo: &total,
			Title: fmt.Sprintf("%s would agree %s with %s", nameOr(o.DealOwnerBy, "Staff"), invoicing.Naira(total), o.BuyerName), Line: line + " · under your lowest price"})
	}
	return out, nil
}

// dealWas is what an offer's lines come to at the seller's prices.
func dealWas(lines []links.OrderLine) int64 {
	var t int64
	for _, l := range lines {
		p := l.ListKobo
		if p <= 0 {
			p = l.PriceKobo
		}
		t += l.Quantity * p
	}
	return t
}

func nameOr(s, or string) string {
	if strings.TrimSpace(s) == "" {
		return or
	}
	return s
}

// isOwnerAsked reports an accept put to the owner.
func isOwnerAsked(err error) ([]string, bool) {
	var e errOwnerAsked
	if errors.As(err, &e) {
		return e.why, true
	}
	return nil, false
}

// dealNote is an offer in a line: "₦780,000 (was ₦820,000) for 20 Rice,
// 25kg bag", its word after.
func dealNote(o links.Order) string {
	total := orderTotal(o.Lines)
	out := invoicing.Naira(total)
	if was := dealWas(o.Lines); was > total {
		out += " (was " + invoicing.Naira(was) + ")"
	}
	var items []string
	for _, l := range o.Lines {
		if l.Quantity > 0 {
			items = append(items, fmt.Sprintf("%d %s", l.Quantity, l.Item))
		}
	}
	if len(items) > 0 {
		out += " for " + strings.Join(items, ", ")
	}
	if len(o.DealHistory) > 0 {
		if n := o.DealHistory[len(o.DealHistory)-1].Note; n != "" {
			out += ". " + n
		}
	}
	return out
}

// dealCounterCmd is "counter 750k", "counter AD-7Q4K2P 750k", "my price
// 750k": a price for the order waiting on the one who sends it.
var dealCounterCmd = regexp.MustCompile(`(?i)^(?:counter|my price|offer)\s+(?:([a-z0-9]+-[a-z0-9-]+)\s+)?(\S+)$`)

// waDealCounter answers "counter …" on WhatsApp and Telegram (phase 7):
// the business's people price the order waiting on them, a total spread
// over its lines. The seller's side counters an offer (its change); the
// buyer's counters the seller's price. None, several (without the order
// named) or an amount that cannot be read: it says how.
func (s *Server) waDealCounter(ctx context.Context, c *chat, cmd string) (bool, error) {
	m := dealCounterCmd.FindStringSubmatch(strings.TrimSpace(cmd))
	if m == nil || c.p == nil || c.state != stateIdle {
		return false, nil
	}
	ref := strings.ToUpper(m[1])
	kobo, ok := parseKobo(m[2])
	if !ok {
		return true, c.say(ctx, s, c.t("deal.counter_how", "reference", nameOr(ref, "AD-1234")))
	}
	type waiting struct {
		ref      string
		purchase uuid.UUID // the buyer's side
		order    links.Order
	}
	var found []waiting
	if err := chatTx(ctx, s, c, func(tx pgx.Tx) error {
		if c.p.Can(PermOrders) {
			list, err := links.ListOrdersTx(ctx, tx, true, 200)
			if err != nil {
				return err
			}
			for _, o := range list {
				if o.Status == links.OrderReceived && links.IsOffer(o.Lines) && (ref == "" || o.OrderReference == ref || o.Reference == ref) {
					found = append(found, waiting{ref: o.OrderReference, order: o})
				}
			}
		}
		if c.p.Can(PermTableEdit) {
			rows, err := tx.Query(ctx, `SELECT id, reference FROM purchase_orders WHERE tenant_id = $1 AND seller_status = 'changed' AND status = 'sent'
				AND ($2 = '' OR upper(reference) = $2) ORDER BY updated_at DESC LIMIT 20`, c.tenant.ID, ref)
			if err != nil {
				return err
			}
			defer rows.Close()
			for rows.Next() {
				var w waiting
				if err := rows.Scan(&w.purchase, &w.ref); err != nil {
					return err
				}
				found = append(found, w)
			}
			return rows.Err()
		}
		return nil
	}); err != nil {
		return true, err
	}
	switch {
	case len(found) == 0:
		return true, c.say(ctx, s, c.t("deal.counter_none"))
	case len(found) > 1:
		refs := make([]string, len(found))
		for i, w := range found {
			refs[i] = w.ref
		}
		return true, c.say(ctx, s, c.t("deal.counter_which", "references", strings.Join(refs, ", "), "reference", found[0].ref))
	}
	w := found[0]
	if w.purchase != uuid.Nil {
		told, err := s.counter(ctx, c.p, w.purchase, nil, kobo, "", func(tx pgx.Tx, x stock.Purchase) error {
			return waAudit(ctx, tx, c.ch.Name(), c.p, "purchase.counter", x.Reference, map[string]any{"purchase_id": x.ID})
		})
		if err != nil {
			return true, s.dealSayErr(ctx, c, err)
		}
		if told != nil {
			s.mirrorLinkedOrder(ctx, told.Tenant, told.Order)
		}
		s.tellOrder(ctx, told, links.StepIn{})
		return true, c.say(ctx, s, c.t("deal.counter_sent", "amount", invoicing.Naira(kobo), "reference", w.ref))
	}
	// The seller's counter: the total over the offer's lines, in
	// proportion to the seller's own prices.
	o := w.order
	in, err := links.CounterIn(o, kobo, "")
	if err != nil {
		return true, s.dealSayErr(ctx, c, err)
	}
	if err := s.dealGate(ctx, c.p, o.ID.String(), links.StepChange, in); err != nil {
		return true, s.dealSayErr(ctx, c, err)
	}
	_ = chatTx(ctx, s, c, func(tx pgx.Tx) error { in.Who = personName(ctx, tx, c.p); return nil })
	if _, _, err := s.sellerStep(ctx, c.tenant.ID, o.ID.String(), links.StepChange, in, actorOf(c.p), c.p.Actor(), func(tx pgx.Tx, x links.Order) error {
		if err := orderStepAllowed(c.p, links.StepChange, x); err != nil {
			return err
		}
		return waAudit(ctx, tx, c.ch.Name(), c.p, "trade_order.change", x.Reference, map[string]any{"order_id": x.ID})
	}); err != nil {
		return true, s.dealSayErr(ctx, c, err)
	}
	return true, c.say(ctx, s, c.t("deal.counter_sent", "amount", invoicing.Naira(kobo), "reference", w.ref))
}

// dealSayErr says a deal's refusal in chat: its reason, else the error.
func (s *Server) dealSayErr(ctx context.Context, c *chat, err error) error {
	for _, e := range []error{errForbidden, links.ErrInvalid, links.ErrState} {
		if errors.Is(err, e) {
			return c.say(ctx, s, c.t("order.link.open", "reason", strings.TrimPrefix(strings.TrimPrefix(err.Error(), e.Error()), ": ")))
		}
	}
	return err
}
