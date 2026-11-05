package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/datatables"
	"github.com/israel-duff/taskiem/engine/storefront"
)

// A front's items, edited in place (docs/spec/fronts.md §7.1): the rows of
// the table its list shows, read and written through the same data tables
// connection as the catalogue's photos are, and mapped by the list's own
// columns. The home page's catalogue has its own screen; these work for any
// front with a list, home included.

// frontListOf reads a front's first list: its mapping and its table.
func (s *Server) frontListOf(r *http.Request) (storefront.ListConfig, datatables.Table, error) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		return storefront.ListConfig{}, datatables.Table{}, pgx.ErrNoRows
	}
	var lc storefront.ListConfig
	var t datatables.Table
	err = s.tx(r, func(tx pgx.Tx) error {
		tenant := principalFrom(r.Context()).TenantID
		fronts, err := storefront.Fronts(r.Context(), tx, tenant)
		if err != nil {
			return err
		}
		if !slices.ContainsFunc(fronts, func(f storefront.Front) bool { return f.ID == id }) {
			return storefront.ErrNotFound
		}
		blocks, err := storefront.Blocks(r.Context(), tx, id)
		if err != nil {
			return err
		}
		found := false
		for _, b := range blocks {
			var c storefront.ListConfig
			if b.Kind == "list" && json.Unmarshal(b.Config, &c) == nil && c.Table != nil {
				lc, found = c, true
				break
			}
		}
		if !found {
			return fmt.Errorf("%w: this front has no list of items", errBadRequest)
		}
		t, err = datatables.GetTable(r.Context(), tx, *lc.Table)
		return err
	})
	return lc, t, err
}

// listFrontItems answers GET /v1/storefront/fronts/{id}/items: the items as
// the table has them now, with their photos, and which of a name, price,
// description, category, availability and photo the list takes.
func (s *Server) listFrontItems(w http.ResponseWriter, r *http.Request) {
	lc, t, err := s.frontListOf(r)
	if err != nil {
		s.fail(w, r, storefrontErr(err))
		return
	}
	tenant := principalFrom(r.Context()).TenantID
	rows, err := s.readAllRows(r.Context(), tenant, t, storefront.MaxItems+1)
	if err != nil {
		s.failTables(w, r, err)
		return
	}
	var items []storefront.Item
	if err := s.tx(r, func(tx pgx.Tx) error {
		var err error
		items, err = storefront.Items(r.Context(), tx, tenant, rows, lc.Columns)
		return err
	}); err != nil {
		s.fail(w, r, storefrontErr(err))
		return
	}
	if items == nil {
		items = []storefront.Item{}
	}
	m := lc.Columns
	writeJSON(w, http.StatusOK, map[string]any{
		"table_id": t.ID,
		"items":    items,
		"takes": map[string]bool{"price": m.Price != "", "description": m.Description != "", "category": m.Category != "",
			"available": m.Available != "", "photo": m.Photo != ""},
	})
}

// frontItemReq is what an item is made or changed with; nil leaves it.
type frontItemReq struct {
	Name        *string  `json:"name"`
	PriceNaira  *float64 `json:"price_naira"`
	Description *string  `json:"description"`
	Category    *string  `json:"category"`
	Available   *bool    `json:"available"`
}

// row maps the request to the list's columns, checked by the table's own.
func (q frontItemReq) row(lc storefront.ListConfig, t datatables.Table, create bool) (map[string]any, error) {
	m := lc.Columns
	row := map[string]any{}
	if q.Name != nil {
		name := strings.TrimSpace(*q.Name)
		if n := utf8.RuneCountInString(name); n == 0 || n > 120 {
			return nil, fmt.Errorf("%w: an item's name is 1 to 120 characters", errBadRequest)
		}
		row[m.Name] = name
	} else if create {
		return nil, fmt.Errorf("%w: give the item a name", errBadRequest)
	}
	if q.PriceNaira != nil {
		if m.Price == "" {
			return nil, fmt.Errorf("%w: this list has no price", errBadRequest)
		}
		if *q.PriceNaira < 0 || *q.PriceNaira > 100_000_000 || math.IsNaN(*q.PriceNaira) {
			return nil, fmt.Errorf("%w: a price is 0 to 100,000,000 naira", errBadRequest)
		}
		row[m.Price] = priceCell(int64(math.Round(*q.PriceNaira*100)), m)
	}
	for _, f := range []struct {
		v    *string
		col  string
		what string
		max  int
	}{{q.Description, m.Description, "description", 1000}, {q.Category, m.Category, "category", 60}} {
		if f.v == nil {
			continue
		}
		if f.col == "" {
			return nil, fmt.Errorf("%w: this list has no %s", errBadRequest, f.what)
		}
		v := strings.TrimSpace(*f.v)
		if utf8.RuneCountInString(v) > f.max {
			return nil, fmt.Errorf("%w: a %s is at most %d characters", errBadRequest, f.what, f.max)
		}
		row[f.col] = v
	}
	if q.Available != nil {
		if m.Available == "" {
			return nil, fmt.Errorf("%w: this list has no availability", errBadRequest)
		}
		row[m.Available] = *q.Available
	} else if create && m.Available != "" {
		row[m.Available] = true
	}
	// A Hire item's count starts at one; the owner changes it in the list.
	if create && m.Capacity != "" {
		row[m.Capacity] = 1
	}
	checked, err := datatables.CheckRow(t.Columns, row, create, false)
	if err != nil {
		return nil, fmt.Errorf("%w: %s", errBadRequest, strings.TrimPrefix(err.Error(), "invalid: "))
	}
	return checked, nil
}

// addFrontItem answers POST /v1/storefront/fronts/{id}/items.
func (s *Server) addFrontItem(w http.ResponseWriter, r *http.Request) {
	var q frontItemReq
	if err := decodeBody(r, &q); err != nil {
		s.fail(w, r, err)
		return
	}
	lc, t, err := s.frontListOf(r)
	if err != nil {
		s.fail(w, r, storefrontErr(err))
		return
	}
	row, err := q.row(lc, t, true)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	tenant := principalFrom(r.Context()).TenantID
	tr, err := s.tableRowsFor(r.Context(), tenant, t, storefrontPurpose)
	if err != nil {
		s.failTables(w, r, err)
		return
	}
	out, err := tr.call(r.Context(), s, "insert_rows", map[string]any{"table": t.Name, "rows": []any{row}}, "front-item:"+uuid.NewString())
	if err != nil {
		s.failTables(w, r, err)
		return
	}
	id := ""
	if rows := rowsOf(out); len(rows) > 0 {
		if m, ok := rows[0].(map[string]any); ok {
			id = cell(m["id"])
		}
	}
	s.frontItemChanged(r, "storefront.item_add", t, id)
	writeJSON(w, http.StatusCreated, map[string]any{"id": id})
}

// frontItemKey reads the row's id of a request's path.
func frontItemKey(r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(chi.URLParam(r, "row"), 10, 64)
	return id, err == nil && id >= 1
}

// patchFrontItem answers PATCH /v1/storefront/fronts/{id}/items/{row}.
func (s *Server) patchFrontItem(w http.ResponseWriter, r *http.Request) {
	key, ok := frontItemKey(r)
	if !ok {
		s.fail(w, r, pgx.ErrNoRows)
		return
	}
	var q frontItemReq
	if err := decodeBody(r, &q); err != nil {
		s.fail(w, r, err)
		return
	}
	lc, t, err := s.frontListOf(r)
	if err != nil {
		s.fail(w, r, storefrontErr(err))
		return
	}
	row, err := q.row(lc, t, false)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if len(row) == 0 {
		s.fail(w, r, fmt.Errorf("%w: nothing to change", errBadRequest))
		return
	}
	tr, err := s.tableRowsFor(r.Context(), principalFrom(r.Context()).TenantID, t, storefrontPurpose)
	if err != nil {
		s.failTables(w, r, err)
		return
	}
	out, err := tr.call(r.Context(), s, "update_rows", map[string]any{"table": t.Name, "key": key, "set": row, "max_affected": 1}, "front-item:"+uuid.NewString())
	if err != nil {
		s.failTables(w, r, err)
		return
	}
	if countValue(out["affected"]) == 0 && len(rowsOf(out)) == 0 {
		writeErr(w, http.StatusNotFound, "no item "+strconv.FormatInt(key, 10))
		return
	}
	s.frontItemChanged(r, "storefront.item_change", t, strconv.FormatInt(key, 10))
	writeJSON(w, http.StatusOK, map[string]any{"id": strconv.FormatInt(key, 10)})
}

// deleteFrontItem answers DELETE /v1/storefront/fronts/{id}/items/{row}.
func (s *Server) deleteFrontItem(w http.ResponseWriter, r *http.Request) {
	key, ok := frontItemKey(r)
	if !ok {
		s.fail(w, r, pgx.ErrNoRows)
		return
	}
	_, t, err := s.frontListOf(r)
	if err != nil {
		s.fail(w, r, storefrontErr(err))
		return
	}
	tr, err := s.tableRowsFor(r.Context(), principalFrom(r.Context()).TenantID, t, storefrontPurpose)
	if err != nil {
		s.failTables(w, r, err)
		return
	}
	if _, err := tr.call(r.Context(), s, "delete_rows", map[string]any{"table": t.Name, "key": key, "max_affected": 1}, "front-item:"+uuid.NewString()); err != nil {
		s.failTables(w, r, err)
		return
	}
	s.frontItemChanged(r, "storefront.item_delete", t, strconv.FormatInt(key, 10))
	w.WriteHeader(http.StatusNoContent)
}

// setFrontItemPhoto answers POST /v1/storefront/fronts/{id}/items/{row}/photo
// (the image as the body): as the catalogue's photos are, for any front's
// list that has a photo column.
func (s *Server) setFrontItemPhoto(w http.ResponseWriter, r *http.Request) {
	key, ok := frontItemKey(r)
	if !ok {
		s.fail(w, r, pgx.ErrNoRows)
		return
	}
	lc, t, err := s.frontListOf(r)
	if err != nil {
		s.fail(w, r, storefrontErr(err))
		return
	}
	if lc.Columns.Photo == "" {
		s.fail(w, r, fmt.Errorf("%w: this list has no photo column", errBadRequest))
		return
	}
	tenant := principalFrom(r.Context()).TenantID
	keep, ok := s.photoKept(w, r, tenant, t, key, lc.Columns.Photo)
	if !ok {
		return
	}
	m, err := s.storeStorefrontImage(r)
	if err != nil {
		s.failImage(w, r, err)
		return
	}
	tr, err := s.tableRowsFor(r.Context(), tenant, t, storefrontPurpose)
	if err != nil {
		s.failTables(w, r, err)
		return
	}
	out, err := tr.call(r.Context(), s, "update_rows", map[string]any{"table": t.Name, "key": key,
		"set": map[string]any{lc.Columns.Photo: photoValue(keep, m.ID.String())}, "max_affected": 1}, "front-photo:"+m.ID.String()+":"+strconv.FormatInt(key, 10))
	if err != nil {
		s.failTables(w, r, err)
		return
	}
	if countValue(out["affected"]) == 0 && len(rowsOf(out)) == 0 {
		writeErr(w, http.StatusNotFound, "no item "+strconv.FormatInt(key, 10))
		return
	}
	s.frontItemChanged(r, "storefront.item_photo", t, strconv.FormatInt(key, 10))
	writeJSON(w, http.StatusOK, m)
}

// removeFrontItemPhoto answers DELETE /v1/storefront/fronts/{id}/items/{row}/photo?media=<id>.
func (s *Server) removeFrontItemPhoto(w http.ResponseWriter, r *http.Request) {
	key, ok := frontItemKey(r)
	if !ok {
		s.fail(w, r, pgx.ErrNoRows)
		return
	}
	lc, t, err := s.frontListOf(r)
	if err != nil {
		s.fail(w, r, storefrontErr(err))
		return
	}
	if lc.Columns.Photo == "" {
		s.fail(w, r, fmt.Errorf("%w: this list has no photo column", errBadRequest))
		return
	}
	if s.dropPhoto(w, r, principalFrom(r.Context()).TenantID, t, key, lc.Columns.Photo) {
		s.frontItemChanged(r, "storefront.item_photo_remove", t, strconv.FormatInt(key, 10))
		w.WriteHeader(http.StatusNoContent)
	}
}

// frontItemChanged audits a change to a front's items and refreshes the
// copies the page reads, as the catalogue's changes do (a failure to
// refresh is kept for the next refresh, not a failure of the change).
func (s *Server) frontItemChanged(r *http.Request, action string, t datatables.Table, row string) {
	p := principalFrom(r.Context())
	if err := s.tx(r, func(tx pgx.Tx) error {
		return auditTx(r, tx, action, t.Name, map[string]any{"table_id": t.ID, "row": row, "front": chi.URLParam(r, "id")})
	}); err != nil {
		s.Logger.Warn("front items: audit", "tenant", p.TenantID, "err", err)
	}
	if err := s.SyncStorefront(r.Context(), p.TenantID); err != nil && !errors.Is(err, r.Context().Err()) {
		s.Logger.Warn("front items: refreshing the copies", "tenant", p.TenantID, "err", err)
	}
}
