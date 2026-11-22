package api

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/stock"
)

// What a business buys, U1 (docs/spec/what-a-business-buys.md §5, §6):
// a line's treatment changed and confirmed, the memory of a supplier not
// on Taskiem's items, and Things we have. The memory of a linked
// supplier's items is /v1/item-links (price_lists.go).

// setPurchaseLine is PATCH /v1/stock/purchases/{id}/lines/{position}:
// {treatment, stock_item_id, uses, label_id, track_many, remember}: how a
// line is treated, until anything on it is received (409 line_locked
// after). It confirms the line: the business's memory of the item is kept
// as this the first time, or with remember; otherwise only this order's
// line changes.
func (s *Server) setPurchaseLine(w http.ResponseWriter, r *http.Request) {
	if !s.purchasesReady(w) {
		return
	}
	id, err := idParam(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	pos, err := strconv.Atoi(chi.URLParam(r, "position"))
	if err != nil {
		s.fail(w, r, pgx.ErrNoRows)
		return
	}
	var in struct {
		stock.LineChoice
		Remember bool `json:"remember"`
	}
	if err := decodeBody(r, &in); err != nil {
		s.fail(w, r, err)
		return
	}
	p := principalFrom(r.Context())
	var x stock.Purchase
	if err := s.tx(r, func(tx pgx.Tx) error {
		var err error
		if x, err = stock.SetLineTx(r.Context(), tx, p.TenantID, id, pos, in.LineChoice, in.Remember, p.Actor()); err != nil {
			return err
		}
		return auditTx(r, tx, "purchase.line_set", x.Reference, map[string]any{"purchase_id": x.ID, "position": pos, "treatment": in.Treatment, "remember": in.Remember})
	}); err != nil {
		s.purchaseFail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"purchase": x})
}

// listSupplierItems is GET /v1/stock/suppliers/{id}/items: what the
// business remembers of the items of a supplier not on Taskiem, by the
// names typed.
func (s *Server) listSupplierItems(w http.ResponseWriter, r *http.Request) {
	id, err := idParam(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	p := principalFrom(r.Context())
	var out []stock.Remembered
	if err := s.tx(r, func(tx pgx.Tx) error {
		var ok bool
		if err := tx.QueryRow(r.Context(), `SELECT EXISTS (SELECT 1 FROM suppliers WHERE tenant_id = $1 AND id = $2)`, p.TenantID, id).Scan(&ok); err != nil {
			return err
		}
		if !ok {
			return stock.ErrNoSupplier
		}
		var err error
		out, err = stock.SupplierItemsTx(r.Context(), tx, p.TenantID, id)
		return err
	}); err != nil {
		s.purchaseFail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": out})
}

// putSupplierItem is PUT /v1/stock/suppliers/{id}/items: {name, treatment,
// stock_item_id, uses, label_id}: how to treat an item of a supplier not
// on Taskiem from now on (its name matched without case or extra spaces).
func (s *Server) putSupplierItem(w http.ResponseWriter, r *http.Request) {
	id, err := idParam(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	var in struct {
		Name string `json:"name"`
		stock.LineChoice
	}
	if err := decodeBody(r, &in); err != nil {
		s.fail(w, r, err)
		return
	}
	p := principalFrom(r.Context())
	if err := s.tx(r, func(tx pgx.Tx) error {
		if err := stock.SetSupplierItemTx(r.Context(), tx, p.TenantID, id, in.Name, in.LineChoice, p.Actor()); err != nil {
			return err
		}
		return auditTx(r, tx, "supplier_item.set", id.String(), map[string]any{"name": in.Name, "treatment": in.Treatment})
	}); err != nil {
		s.purchaseFail(w, r, err)
		return
	}
	s.listSupplierItems(w, r)
}

// deleteSupplierItem is DELETE /v1/stock/suppliers/{id}/items?name=:
// forgets an item; its next order asks again.
func (s *Server) deleteSupplierItem(w http.ResponseWriter, r *http.Request) {
	id, err := idParam(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	name := r.URL.Query().Get("name")
	p := principalFrom(r.Context())
	if err := s.tx(r, func(tx pgx.Tx) error {
		if err := stock.ClearSupplierItemTx(r.Context(), tx, p.TenantID, id, name, p.Actor()); err != nil {
			return err
		}
		return auditTx(r, tx, "supplier_item.remove", id.String(), map[string]any{"name": name})
	}); err != nil {
		s.purchaseFail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// --- Things we have ---

// listThings is GET /v1/things: Things we have, the newest first, up to
// stock.MaxThings (all=true with those sold, broken, given away or gone,
// which stay in the history; order= and line= those that came on that
// purchase order or line); count and total_kobo of all those had that the
// filter keeps, and group=label them by label.
func (s *Server) listThings(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r.Context())
	q := r.URL.Query()
	f := stock.ThingFilter{All: q.Get("all") == "true"}
	for name, to := range map[string]**uuid.UUID{"order": &f.Order, "line": &f.Line} {
		if v := q.Get(name); v != "" {
			id, err := uuid.Parse(v)
			if err != nil {
				s.fail(w, r, fmt.Errorf("%w: %s is an id", errBadRequest, name))
				return
			}
			*to = &id
		}
	}
	group := q.Get("group")
	if group != "" && group != "label" {
		s.fail(w, r, fmt.Errorf("%w: group is label", errBadRequest))
		return
	}
	out := map[string]any{}
	if err := s.tx(r, func(tx pgx.Tx) error {
		list, err := stock.ThingsTx(r.Context(), tx, p.TenantID, f)
		if err != nil {
			return err
		}
		count, total, err := stock.ThingTotalsTx(r.Context(), tx, p.TenantID, f)
		if err != nil {
			return err
		}
		out["things"], out["count"], out["total_kobo"] = list, count, total
		if group == "label" {
			if out["groups"], err = stock.ThingGroupsTx(r.Context(), tx, p.TenantID, f); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		s.purchaseFail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// getThing is GET /v1/things/{id}.
func (s *Server) getThing(w http.ResponseWriter, r *http.Request) {
	id, err := idParam(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	var t stock.Thing
	if err := s.tx(r, func(tx pgx.Tx) error {
		var err error
		t, err = stock.ThingTx(r.Context(), tx, principalFrom(r.Context()).TenantID, id)
		return err
	}); err != nil {
		s.purchaseFail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"thing": t})
}

// addThing is POST /v1/things: something we have, added by hand (bought
// before Taskiem or outside it): name, label_id, supplier_id, cost_kobo,
// received_at, serial, warranty_until, note.
func (s *Server) addThing(w http.ResponseWriter, r *http.Request) {
	var in stock.NewThing
	if err := decodeBody(r, &in); err != nil {
		s.fail(w, r, err)
		return
	}
	p := principalFrom(r.Context())
	var t stock.Thing
	if err := s.tx(r, func(tx pgx.Tx) error {
		var err error
		if t, err = stock.AddThingTx(r.Context(), tx, p.TenantID, in, p.Actor()); err != nil {
			return err
		}
		return auditTx(r, tx, "thing.add", t.Name, map[string]any{"thing_id": t.ID})
	}); err != nil {
		s.purchaseFail(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"thing": t})
}

// patchThing is PATCH /v1/things/{id}: status (have, sold, broken, given,
// gone), serial, warranty_until, note, label_id (clear_label to take it
// off). Sold with sale_price_kobo (sale_paid_how cash or transfer) is
// money in, "Sale of things we had": it needs invoice.manage too, and is
// recorded once; the same sale again with the same Idempotency-Key (a
// retry) answers 200 with the row as it is.
func (s *Server) patchThing(w http.ResponseWriter, r *http.Request) {
	id, err := idParam(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	var in stock.ThingChange
	if err := decodeBody(r, &in); err != nil {
		s.fail(w, r, err)
		return
	}
	p := principalFrom(r.Context())
	if in.Sells() && !p.Can(PermInvoiceManage) {
		// A sale with a price is money in: entering money too.
		writeErr(w, http.StatusForbidden, "requires "+PermInvoiceManage)
		return
	}
	key := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if key == "" {
		key = id.String()
	}
	var t stock.Thing
	if err := s.tx(r, func(tx pgx.Tx) error {
		var err error
		if t, err = stock.ChangeThingTx(r.Context(), tx, p.TenantID, id, in, p.Actor(), key, s.moneyLog()); err != nil {
			return err
		}
		detail := map[string]any{"thing_id": id}
		if in.Status != nil {
			detail["status"] = *in.Status
		}
		return auditTx(r, tx, "thing.change", t.Name, detail)
	}); err != nil {
		s.purchaseFail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"thing": t})
}
