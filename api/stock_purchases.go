package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/stock"
)

// Suppliers and purchase orders (docs/spec/stock.md §6, K3): the web's
// and the API's door. A supplier is reached as riders are; an order is
// drafted (from the low list or by hand), approved and sent, confirmed,
// received with the stock raised, and paid as an expense. Chat's door,
// and the supplier's taps, are whatsapp_purchases.go.

func (s *Server) purchasesReady(w http.ResponseWriter) bool {
	if !s.Purchases.Ready() || !s.Stock.Ready() {
		writeErr(w, http.StatusServiceUnavailable, "suppliers are not available on this platform")
		return false
	}
	return true
}

// purchaseFail maps the purchases' errors to answers.
func (s *Server) purchaseFail(w http.ResponseWriter, r *http.Request, err error) {
	msg := err.Error()
	for _, p := range []string{"stock: that does not fit the order: ", "stock: already a supplier: ", "stock: a second person approves this order: ",
		"stock: the link was sent a moment ago: ", "stock: the line's treatment is locked: "} {
		msg = strings.ReplaceAll(msg, p, "")
	}
	// Tracking many asks first (409 track_ask: "Put 500 nylon bags on
	// Things we have, one row each?", with Just record them); more than
	// stock.MaxTracked cannot be tracked (422 track_too_many).
	var track *stock.TrackError
	if errors.As(err, &track) {
		status, code := http.StatusConflict, "track_ask"
		if errors.Is(err, stock.ErrTrackTooMany) {
			status, code = http.StatusUnprocessableEntity, "track_too_many"
		}
		writeJSON(w, status, map[string]any{"error": track.Error(), "code": code, "line": track.Position, "item": track.Item, "quantity": track.Quantity,
			"ask_over": stock.TrackAsk, "most": stock.MaxTracked})
		return
	}
	switch {
	case errors.Is(err, stock.ErrLineLocked):
		writeJSON(w, http.StatusConflict, map[string]any{"error": msg, "code": "line_locked"})
		return
	case errors.Is(err, stock.ErrNoThing), errors.Is(err, stock.ErrNoItem):
		err = pgx.ErrNoRows
	case errors.Is(err, stock.ErrNoSupplier), errors.Is(err, stock.ErrNoPurchase):
		err = pgx.ErrNoRows
	case errors.Is(err, stock.ErrPurchase):
		err = fmt.Errorf("%w: %s", errBadRequest, msg)
	case errors.Is(err, stock.ErrSupplierExists), errors.Is(err, stock.ErrTooSoon), errors.Is(err, stock.ErrSecondApproval):
		err = fmt.Errorf("%w: %s", errConflict, msg)
	case errors.Is(err, stock.ErrLinked):
		err = fmt.Errorf("%w: the order is with a linked business: record what came with All came or Not all came once it is delivered", errConflict)
	default:
		s.stockFail(w, r, err)
		return
	}
	s.fail(w, r, err)
}

func idParam(r *http.Request) (uuid.UUID, error) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		return id, pgx.ErrNoRows
	}
	return id, nil
}

// --- suppliers ---

type suppliersView struct {
	Suppliers []stock.Supplier `json:"suppliers"`
	Settings  stock.Settings   `json:"settings"`
}

// listSuppliers is GET /v1/stock/suppliers (?retired=true for those
// retired), with the stock settings.
func (s *Server) listSuppliers(w http.ResponseWriter, r *http.Request) {
	if !s.purchasesReady(w) {
		return
	}
	p := principalFrom(r.Context())
	var out suppliersView
	err := s.tx(r, func(tx pgx.Tx) error {
		var err error
		if out.Suppliers, err = s.Purchases.SuppliersTx(r.Context(), tx, p.TenantID, r.URL.Query().Get("retired") == "true"); err != nil {
			return err
		}
		out.Settings, err = stock.SettingsTx(r.Context(), tx, p.TenantID)
		return err
	})
	if err != nil {
		s.purchaseFail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// addSupplier is POST /v1/stock/suppliers: a name and phone, and the
// items they supply (their catalogue rows' supplier set to the name).
func (s *Server) addSupplier(w http.ResponseWriter, r *http.Request) {
	if !s.purchasesReady(w) {
		return
	}
	var in struct {
		Name  string   `json:"name"`
		Phone string   `json:"phone"`
		Items []string `json:"items"`
		// LinkID: a linked business, added from the link with it; no phone
		// is asked (its orders go through the link).
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
		if in.LinkID != nil && strings.TrimSpace(in.Phone) == "" {
			x, err = s.Purchases.AddLinkedSupplierTx(r.Context(), tx, p.TenantID, in.Name, *in.LinkID, p.Actor())
		} else if x, err = s.Purchases.AddSupplierTx(r.Context(), tx, p.TenantID, in.Name, in.Phone, p.Actor()); err == nil && in.LinkID != nil {
			x, err = s.Purchases.LinkSupplierTx(r.Context(), tx, p.TenantID, x.ID, in.LinkID, p.Actor())
		}
		if err != nil {
			return err
		}
		return auditTx(r, tx, "supplier.add", x.Name, map[string]any{"supplier_id": x.ID})
	}); err != nil {
		s.purchaseFail(w, r, err)
		return
	}
	if err := s.supplyItems(r.Context(), p.TenantID, x.Name, in.Items, p.Actor()); err != nil {
		s.Logger.Warn("stock: naming a supplier on items", "tenant", p.TenantID, "err", err)
	}
	writeJSON(w, http.StatusCreated, map[string]any{"supplier": x})
}

// supplyItems makes the supplier named the usual one of the items named
// (their ids): their stock items', a product not counted yet starting
// to be.
func (s *Server) supplyItems(ctx context.Context, tenant uuid.UUID, name string, ids []string, actor string) error {
	if len(ids) == 0 {
		return nil
	}
	_, items, err := s.stockItems(ctx, tenant)
	if err != nil {
		return err
	}
	byID := make(map[string]stock.Item, len(items))
	for _, it := range items {
		byID[it.ID] = it
	}
	return db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		for _, id := range ids {
			it, ok := byID[id]
			if !ok {
				continue
			}
			if _, err := stockSetDetailsTx(ctx, tx, tenant, it, stockDetails{Supplier: &name}, actor); err != nil {
				return err
			}
		}
		return nil
	})
}

// patchSupplier is PATCH /v1/stock/suppliers/{id}: a new name, carried to
// the items naming the old one.
func (s *Server) patchSupplier(w http.ResponseWriter, r *http.Request) {
	if !s.purchasesReady(w) {
		return
	}
	id, err := idParam(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	var in struct {
		Name string `json:"name"`
	}
	if err := decodeBody(r, &in); err != nil {
		s.fail(w, r, err)
		return
	}
	p := principalFrom(r.Context())
	// Their stock items name them by id: the new name is theirs at once.
	var x stock.Supplier
	if err := s.tx(r, func(tx pgx.Tx) error {
		var err error
		x, err = s.Purchases.RenameSupplierTx(r.Context(), tx, p.TenantID, id, in.Name, p.Actor())
		return err
	}); err != nil {
		s.purchaseFail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"supplier": x})
}

func (s *Server) supplierAction(w http.ResponseWriter, r *http.Request, do func(context.Context, pgx.Tx, uuid.UUID, uuid.UUID, string) (stock.Supplier, error), action string) {
	if !s.purchasesReady(w) {
		return
	}
	id, err := idParam(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	p := principalFrom(r.Context())
	var x stock.Supplier
	if err := s.tx(r, func(tx pgx.Tx) error {
		var err error
		if x, err = do(r.Context(), tx, p.TenantID, id, p.Actor()); err != nil {
			return err
		}
		return auditTx(r, tx, action, x.Name, map[string]any{"supplier_id": x.ID})
	}); err != nil {
		s.purchaseFail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"supplier": x})
}

// retireSupplier is POST /v1/stock/suppliers/{id}/retire.
func (s *Server) retireSupplier(w http.ResponseWriter, r *http.Request) {
	s.supplierAction(w, r, s.Purchases.RetireSupplierTx, "supplier.retire")
}

// inviteSupplier is POST /v1/stock/suppliers/{id}/invite: the tap-in link
// by SMS again.
func (s *Server) inviteSupplier(w http.ResponseWriter, r *http.Request) {
	s.supplierAction(w, r, s.Purchases.InviteSupplierTx, "supplier.invite")
}

// getStockSettings is GET /v1/stock/settings.
func (s *Server) getStockSettings(w http.ResponseWriter, r *http.Request) {
	var out stock.Settings
	if err := s.tx(r, func(tx pgx.Tx) error {
		var err error
		out, err = stock.SettingsTx(r.Context(), tx, principalFrom(r.Context()).TenantID)
		return err
	}); err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"settings": out})
}

// putStockSettings is PUT /v1/stock/settings: the limits given change,
// those left out stay (null clears one).
func (s *Server) putStockSettings(w http.ResponseWriter, r *http.Request) {
	var in map[string]json.RawMessage
	if err := decodeBody(r, &in); err != nil {
		s.fail(w, r, err)
		return
	}
	p := principalFrom(r.Context())
	var out stock.Settings
	if err := s.tx(r, func(tx pgx.Tx) error {
		var err error
		if out, err = stock.SettingsTx(r.Context(), tx, p.TenantID); err != nil {
			return err
		}
		for k, v := range in {
			var to **int64
			switch k {
			case "purchase_limit_kobo":
				to = &out.PurchaseLimitKobo
			case "count_limit_kobo":
				to = &out.CountLimitKobo
			default:
				return fmt.Errorf("%w: unknown setting %s", errBadRequest, k)
			}
			var n *int64
			if err := json.Unmarshal(v, &n); err != nil {
				return fmt.Errorf("%w: %s is an amount in kobo, or null", errBadRequest, k)
			}
			*to = n
		}
		return stock.PutSettingsTx(r.Context(), tx, p.TenantID, out, p.Actor())
	}); err != nil {
		s.purchaseFail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"settings": out})
}

// --- purchase orders ---

// listPurchases is GET /v1/stock/purchases (?open=true, ?supplier=).
func (s *Server) listPurchases(w http.ResponseWriter, r *http.Request) {
	if !s.purchasesReady(w) {
		return
	}
	f := stock.PurchaseFilter{Open: r.URL.Query().Get("open") == "true", Limit: 100}
	if v := r.URL.Query().Get("supplier"); v != "" {
		id, err := uuid.Parse(v)
		if err != nil {
			writeErr(w, http.StatusBadRequest, "supplier is a supplier's id")
			return
		}
		f.Supplier = &id
	}
	p := principalFrom(r.Context())
	var list []stock.Purchase
	if err := s.tx(r, func(tx pgx.Tx) error {
		var err error
		list, err = stock.PurchasesTx(r.Context(), tx, p.TenantID, f)
		return err
	}); err != nil {
		s.purchaseFail(w, r, err)
		return
	}
	if list == nil {
		list = []stock.Purchase{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"purchases": list})
}

// draftLineIn is a line of a purchase order to draft (what a business
// buys §5, §6): one of the business's own items whose stock it adds to
// (item_id: a product or a material), or anything by its name (item, with
// a unit); how many, the price of one (the item's last cost when
// absent); and how it is treated when said (treatment record, stock with
// stock_item_id and uses, or track; label_id; track_many to put more
// than 50 on Things we have), else as remembered, else as suggested.
type draftLineIn struct {
	ItemID   string `json:"item_id"`
	Item     string `json:"item"`
	Unit     string `json:"unit"`
	Quantity int64  `json:"quantity"`
	CostKobo *int64 `json:"cost_kobo"`
	stock.LineChoice
}

// draftLinesTx makes the lines to draft.
func draftLinesTx(ctx context.Context, tx pgx.Tx, p *Principal, in []draftLineIn, byID map[string]stock.Item) ([]stock.PurchaseLine, error) {
	lines := make([]stock.PurchaseLine, 0, len(in))
	for _, l := range in {
		var line stock.PurchaseLine
		if l.ItemID != "" {
			it, ok := byID[l.ItemID]
			if !ok {
				return nil, errNoItem
			}
			var err error
			if line, err = stock.LineOf(ctx, tx, p.TenantID, it, l.Quantity, l.CostKobo, p.Actor()); err != nil {
				return nil, err
			}
			if l.Treatment == "" {
				l.Treatment, l.StockItemID, l.Uses = line.Treatment, line.StockItemID, line.Uses
			}
		} else {
			line = stock.PurchaseLine{Item: l.Item, Unit: strings.TrimSpace(l.Unit), Quantity: l.Quantity}
			if l.CostKobo != nil {
				line.CostKobo = *l.CostKobo
			}
		}
		line.Treatment, line.StockItemID, line.Uses, line.LabelID, line.TrackMany = l.Treatment, l.StockItemID, l.Uses, l.LabelID, l.TrackMany
		lines = append(lines, line)
	}
	return lines, nil
}

// draftPurchase is POST /v1/stock/purchases, with an Idempotency-Key: an
// order to a supplier and its lines (draftLineIn). Each line answers its
// treatment, and unconfirmed when it is Taskiem's suggestion, for the
// business to confirm (PATCH …/lines/{position}).
func (s *Server) draftPurchase(w http.ResponseWriter, r *http.Request) {
	if !s.purchasesReady(w) {
		return
	}
	key, err := requiredKey(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	var in struct {
		SupplierID uuid.UUID     `json:"supplier_id"`
		Lines      []draftLineIn `json:"lines"`
		Note       string        `json:"note"`
	}
	if err := decodeBody(r, &in); err != nil {
		s.fail(w, r, err)
		return
	}
	p := principalFrom(r.Context())
	byID := map[string]stock.Item{}
	for _, l := range in.Lines {
		if l.ItemID != "" {
			_, items, err := s.stockItems(r.Context(), p.TenantID)
			if err != nil {
				s.stockFail(w, r, err)
				return
			}
			for _, it := range items {
				byID[it.ID] = it
			}
			break
		}
	}
	var x stock.Purchase
	if err := s.tx(r, func(tx pgx.Tx) error {
		lines, err := draftLinesTx(r.Context(), tx, p, in.Lines, byID)
		if err != nil {
			return err
		}
		x, err = stock.DraftTx(r.Context(), tx, p.TenantID, stock.DraftIn{Supplier: in.SupplierID, Lines: lines, Note: in.Note, Actor: p.Actor(), Key: "api-" + key})
		return err
	}); err != nil {
		if errors.Is(err, errNoItem) {
			s.stockFail(w, r, err)
			return
		}
		s.purchaseFail(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"purchase": x})
}

// draftLowPurchases is POST /v1/stock/purchases/low, with an
// Idempotency-Key: an order per supplier for the low items naming them.
func (s *Server) draftLowPurchases(w http.ResponseWriter, r *http.Request) {
	if !s.purchasesReady(w) {
		return
	}
	key, err := requiredKey(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	p := principalFrom(r.Context())
	_, items, err := s.stockItems(r.Context(), p.TenantID)
	if err != nil {
		s.stockFail(w, r, err)
		return
	}
	var drafted []stock.Purchase
	var skipped []string
	if err := s.tx(r, func(tx pgx.Tx) error {
		var err error
		drafted, skipped, err = s.Purchases.DraftLowTx(r.Context(), tx, p.TenantID, items, p.Actor(), "api-"+key)
		return err
	}); err != nil {
		s.purchaseFail(w, r, err)
		return
	}
	if drafted == nil {
		drafted = []stock.Purchase{}
	}
	if skipped == nil {
		skipped = []string{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"purchases": drafted, "no_supplier": skipped})
}

// getPurchase is GET /v1/stock/purchases/{id}: one order, with a linked
// supplier's side of it.
func (s *Server) getPurchase(w http.ResponseWriter, r *http.Request) {
	if !s.purchasesReady(w) {
		return
	}
	id, err := idParam(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	var x stock.Purchase
	if err := s.tx(r, func(tx pgx.Tx) error {
		var err error
		x, err = stock.PurchaseTx(r.Context(), tx, principalFrom(r.Context()).TenantID, id, false)
		return err
	}); err != nil {
		s.purchaseFail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"purchase": x})
}

// purchaseStep runs a step on an order by its id.
func (s *Server) purchaseStep(w http.ResponseWriter, r *http.Request, action string, do func(ctx context.Context, tx pgx.Tx, tenant, id uuid.UUID, actor string) (stock.Purchase, error)) (ok bool) {
	if !s.purchasesReady(w) {
		return false
	}
	id, err := idParam(r)
	if err != nil {
		s.fail(w, r, err)
		return false
	}
	p := principalFrom(r.Context())
	var x stock.Purchase
	if err := s.tx(r, func(tx pgx.Tx) error {
		var err error
		if x, err = do(r.Context(), tx, p.TenantID, id, p.Actor()); err != nil {
			return err
		}
		return auditTx(r, tx, action, x.Reference, map[string]any{"purchase_id": x.ID, "status": x.Status})
	}); err != nil {
		s.purchaseFail(w, r, err)
		return false
	}
	if action == "purchase.send" {
		if err := s.waDrainOutbox(r.Context(), p.TenantID); err != nil {
			s.Logger.Warn("stock: sending an order", "err", err)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"purchase": x})
	return true
}

// sendPurchase is POST /v1/stock/purchases/{id}/send: approved and sent.
func (s *Server) sendPurchase(w http.ResponseWriter, r *http.Request) {
	// Sent now, not before (a send again answers the same): a diary line.
	var drafted bool
	var x stock.Purchase
	ok := s.purchaseStep(w, r, "purchase.send", func(ctx context.Context, tx pgx.Tx, tenant, id uuid.UUID, actor string) (stock.Purchase, error) {
		was, err := stock.PurchaseTx(ctx, tx, tenant, id, false)
		if err != nil {
			return was, err
		}
		drafted = was.Status == stock.Drafted
		x, err = s.Purchases.SendTx(ctx, tx, tenant, id, actor)
		return x, err
	})
	if ok && drafted && x.Status != stock.Drafted {
		s.diaryChange(r.Context(), principalFrom(r.Context()), buyerStepWords("send", x.Reference, x.Supplier, x.TotalKobo))
	}
}

// confirmPurchase is POST /v1/stock/purchases/{id}/confirm: the supplier
// confirmed by phone or SMS.
func (s *Server) confirmPurchase(w http.ResponseWriter, r *http.Request) {
	s.purchaseStep(w, r, "purchase.confirm", func(ctx context.Context, tx pgx.Tx, tenant, id uuid.UUID, actor string) (stock.Purchase, error) {
		return stock.ConfirmTx(ctx, tx, tenant, id, "owner", actor)
	})
}

// cancelPurchase is POST /v1/stock/purchases/{id}/cancel. An order sent
// to a linked supplier is cancelled through the link (link_orders.go).
func (s *Server) cancelPurchase(w http.ResponseWriter, r *http.Request) {
	if id, err := idParam(r); err == nil && s.purchasesReady(w) {
		var linked bool
		if err := s.tx(r, func(tx pgx.Tx) error {
			x, err := stock.PurchaseTx(r.Context(), tx, principalFrom(r.Context()).TenantID, id, false)
			linked = err == nil && x.LinkID != nil && (x.Status != stock.Drafted || x.Proposed())
			return nil
		}); err != nil {
			s.fail(w, r, err)
			return
		}
		if linked {
			s.cancelLinkedPurchase(w, r, id)
			return
		}
	} else if err == nil {
		return
	}
	s.purchaseStep(w, r, "purchase.cancel", stock.CancelTx)
}

// receivePurchase is POST /v1/stock/purchases/{id}/receive, with an
// Idempotency-Key: how many came of each item.
func (s *Server) receivePurchase(w http.ResponseWriter, r *http.Request) {
	if !s.purchasesReady(w) {
		return
	}
	id, err := idParam(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	key, err := requiredKey(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	var in struct {
		Lines []struct {
			ItemID   string `json:"item_id"`
			Quantity int64  `json:"quantity"`
		} `json:"lines"`
	}
	if err := decodeBody(r, &in); err != nil {
		s.fail(w, r, err)
		return
	}
	got := map[string]int64{}
	for _, l := range in.Lines {
		got[l.ItemID] += l.Quantity
	}
	p := principalFrom(r.Context())
	x, err := s.Purchases.Receive(r.Context(), p.TenantID, id, got, p.Actor(), "api-"+key)
	if err != nil {
		s.purchaseFail(w, r, err)
		return
	}
	if err := s.tx(r, func(tx pgx.Tx) error {
		return auditTx(r, tx, "purchase.receive", x.Reference, map[string]any{"purchase_id": x.ID, "status": x.Status})
	}); err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"purchase": x})
}

// payPurchase is POST /v1/stock/purchases/{id}/pay, with an
// Idempotency-Key: an amount paid in cash or by transfer, an expense in
// the money log.
func (s *Server) payPurchase(w http.ResponseWriter, r *http.Request) {
	if !s.purchasesReady(w) {
		return
	}
	id, err := idParam(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	key, err := requiredKey(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	var in struct {
		AmountKobo int64  `json:"amount_kobo"`
		PaidHow    string `json:"paid_how"`
	}
	if err := decodeBody(r, &in); err != nil {
		s.fail(w, r, err)
		return
	}
	p := principalFrom(r.Context())
	var x stock.Purchase
	if err := s.tx(r, func(tx pgx.Tx) error {
		var err error
		if x, err = stock.PayTx(r.Context(), tx, p.TenantID, id, in.AmountKobo, in.PaidHow, p.Actor(), "api-"+key, s.moneyLog()); err != nil {
			return err
		}
		return auditTx(r, tx, "purchase.pay", x.Reference, map[string]any{"purchase_id": x.ID, "amount_kobo": in.AmountKobo, "paid_how": in.PaidHow})
	}); err != nil {
		s.purchaseFail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"purchase": x})
}
