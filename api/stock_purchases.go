package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

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
		"stock: the link was sent a moment ago: "} {
		msg = strings.ReplaceAll(msg, p, "")
	}
	switch {
	case errors.Is(err, stock.ErrNoSupplier), errors.Is(err, stock.ErrNoPurchase):
		err = pgx.ErrNoRows
	case errors.Is(err, stock.ErrPurchase):
		err = fmt.Errorf("%w: %s", errBadRequest, msg)
	case errors.Is(err, stock.ErrSupplierExists), errors.Is(err, stock.ErrTooSoon), errors.Is(err, stock.ErrSecondApproval):
		err = fmt.Errorf("%w: %s", errConflict, msg)
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
	}
	if err := decodeBody(r, &in); err != nil {
		s.fail(w, r, err)
		return
	}
	p := principalFrom(r.Context())
	var x stock.Supplier
	if err := s.tx(r, func(tx pgx.Tx) error {
		var err error
		if x, err = s.Purchases.AddSupplierTx(r.Context(), tx, p.TenantID, in.Name, in.Phone, p.Actor()); err != nil {
			return err
		}
		return auditTx(r, tx, "supplier.add", x.Name, map[string]any{"supplier_id": x.ID})
	}); err != nil {
		s.purchaseFail(w, r, err)
		return
	}
	if err := s.supplyItems(r.Context(), p.TenantID, x.Name, in.Items, "sup-"+x.ID.String()); err != nil {
		s.Logger.Warn("stock: naming a supplier on items", "tenant", p.TenantID, "err", err)
	}
	writeJSON(w, http.StatusCreated, map[string]any{"supplier": x})
}

// supplyItems sets the catalogue's supplier on the items named (row ids).
func (s *Server) supplyItems(ctx context.Context, tenant uuid.UUID, name string, ids []string, key string) error {
	if len(ids) == 0 {
		return nil
	}
	sh, err := s.stockShopOf(ctx, tenant)
	if err != nil {
		return err
	}
	if sh.cols().Supplier == "" {
		return nil
	}
	for _, id := range ids {
		if err := s.stockSetCells(ctx, tenant, sh, id, key+"-"+id, map[string]any{sh.cols().Supplier: name}); err != nil {
			return err
		}
	}
	return nil
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
	var before, x stock.Supplier
	if err := s.tx(r, func(tx pgx.Tx) error {
		var err error
		if before, err = s.Purchases.SupplierTx(r.Context(), tx, p.TenantID, id); err != nil {
			return err
		}
		x, err = s.Purchases.RenameSupplierTx(r.Context(), tx, p.TenantID, id, in.Name, p.Actor())
		return err
	}); err != nil {
		s.purchaseFail(w, r, err)
		return
	}
	if before.Name != x.Name {
		if _, items, err := s.stockItems(r.Context(), p.TenantID); err == nil {
			var ids []string
			for _, it := range items {
				if strings.EqualFold(strings.TrimSpace(it.Supplier), before.Name) {
					ids = append(ids, it.ID)
				}
			}
			if err := s.supplyItems(r.Context(), p.TenantID, x.Name, ids, "sup-rename-"+x.ID.String()+"-"+strconv.Itoa(len(x.Name))); err != nil {
				s.Logger.Warn("stock: renaming a supplier on items", "tenant", p.TenantID, "err", err)
			}
		}
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

// draftPurchase is POST /v1/stock/purchases, with an Idempotency-Key: an
// order to a supplier, its lines (item ids, how many, the price of one,
// the item's last cost when absent).
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
		SupplierID uuid.UUID `json:"supplier_id"`
		Lines      []struct {
			ItemID   string `json:"item_id"`
			Quantity int64  `json:"quantity"`
			CostKobo *int64 `json:"cost_kobo"`
		} `json:"lines"`
		Note string `json:"note"`
	}
	if err := decodeBody(r, &in); err != nil {
		s.fail(w, r, err)
		return
	}
	p := principalFrom(r.Context())
	_, items, err := s.stockItems(r.Context(), p.TenantID)
	if err != nil {
		s.stockFail(w, r, err)
		return
	}
	byID := map[string]stock.Item{}
	for _, it := range items {
		byID[it.ID] = it
	}
	lines := make([]stock.PurchaseLine, 0, len(in.Lines))
	for _, l := range in.Lines {
		it, ok := byID[l.ItemID]
		if !ok {
			s.stockFail(w, r, errNoItem)
			return
		}
		var cost int64
		switch {
		case l.CostKobo != nil:
			cost = *l.CostKobo
		case it.CostKobo != nil:
			cost = *it.CostKobo
		}
		lines = append(lines, stock.PurchaseLine{ItemID: it.ID, Item: it.Name, Unit: it.Unit, Quantity: l.Quantity, CostKobo: cost})
	}
	var x stock.Purchase
	if err := s.tx(r, func(tx pgx.Tx) error {
		var err error
		x, err = stock.DraftTx(r.Context(), tx, p.TenantID, stock.DraftIn{Supplier: in.SupplierID, Lines: lines, Note: in.Note, Actor: p.Actor(), Key: "api-" + key})
		return err
	}); err != nil {
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

// purchaseStep runs a step on an order by its id.
func (s *Server) purchaseStep(w http.ResponseWriter, r *http.Request, action string, do func(ctx context.Context, tx pgx.Tx, tenant, id uuid.UUID, actor string) (stock.Purchase, error)) {
	if !s.purchasesReady(w) {
		return
	}
	id, err := idParam(r)
	if err != nil {
		s.fail(w, r, err)
		return
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
		return
	}
	if action == "purchase.send" {
		if err := s.waDrainOutbox(r.Context(), p.TenantID); err != nil {
			s.Logger.Warn("stock: sending an order", "err", err)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"purchase": x})
}

// sendPurchase is POST /v1/stock/purchases/{id}/send: approved and sent.
func (s *Server) sendPurchase(w http.ResponseWriter, r *http.Request) {
	s.purchaseStep(w, r, "purchase.send", s.Purchases.SendTx)
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
			linked = err == nil && x.LinkID != nil && x.Status != stock.Drafted
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

// StockCost sets an item's cost price in the catalogue (after goods are
// received on an order): stock.Purchases.Cost.
func (s *Server) StockCost(ctx context.Context, tenant uuid.UUID, row string, kobo int64, key string) error {
	sh, err := s.stockShopOf(ctx, tenant)
	if err != nil || sh.cols().CostPrice == "" {
		return err
	}
	set := stockCells(sh.cols(), "", &kobo, nil, nil, "")
	return s.stockSetCells(ctx, tenant, sh, row, key, set)
}
