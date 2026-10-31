package api

// Price lists and item links between linked businesses (business links
// L4, §9; engine/links/prices.go). The seller builds price lists on its
// own stock items and shares them with linked buyers; each buyer sees
// its supplier's prices, links its own items to the seller's, and orders
// from the list at those prices.

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/links"
	"github.com/israel-duff/taskiem/engine/stock"
)

func (s *Server) priceListRoutes(r chi.Router) {
	manage := s.need(PermStorefrontManage)
	// The seller's.
	r.With(manage).Get("/price-lists", s.listPriceLists)
	r.With(manage, s.tenantWide).Post("/price-lists", s.createPriceList)
	r.With(manage, s.tenantWide).Patch("/price-lists/{id}", s.renamePriceList)
	r.With(manage, s.tenantWide).Delete("/price-lists/{id}", s.deletePriceList)
	r.With(manage, s.tenantWide).Put("/price-lists/{id}/items", s.putPriceListItems)
	r.With(manage, s.tenantWide).Put("/price-lists/{id}/links", s.sharePriceList)
	r.With(manage).Get("/link-prices/{link}", s.getLinkPrices)
	r.With(manage, s.tenantWide).Put("/link-prices/{link}", s.putLinkPrices)
	// The buyer's.
	r.With(s.need(PermTableRead)).Get("/supplier-prices", s.listSupplierPrices)
	r.With(s.need(PermTableRead)).Get("/item-links/{link}", s.listItemLinks)
	r.With(manage, s.tenantWide).Put("/item-links/{link}", s.putItemLink)
	r.With(manage, s.tenantWide).Delete("/item-links/{link}/{item}", s.deleteItemLink)
	r.With(s.need(PermTableEdit), s.tenantWide).Post("/supplier-prices/{link}/order", s.orderFromPrices)
}

func priceListParam(r *http.Request) (uuid.UUID, error) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		return id, pgx.ErrNoRows
	}
	return id, nil
}

type priceListsOut struct {
	Lists []links.PriceList `json:"lists"`
}

// mirrorPrices brings each link's prices to its buyer, one link per
// transaction, after the seller's change was written; what fails stays
// on the link worker's list (kind prices) and is tried again.
func (s *Server) mirrorPrices(ctx context.Context, seller uuid.UUID, ls []uuid.UUID) {
	for _, l := range ls {
		if err := db.InTenantTx(context.WithoutCancel(ctx), s.Store.Pool, []uuid.UUID{seller}, func(tx pgx.Tx) error {
			return links.MirrorPricesTx(ctx, tx, seller, l)
		}); err != nil {
			s.Logger.Warn("links: mirroring prices", "tenant", seller, "link", l, "err", err)
		}
	}
}

// listPriceLists is GET /v1/price-lists: the business's price lists, their
// items and the links each is shared with.
func (s *Server) listPriceLists(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r.Context())
	var out priceListsOut
	if err := s.tx(r, func(tx pgx.Tx) error {
		var err error
		out.Lists, err = links.ListsTx(r.Context(), tx, p.TenantID)
		return err
	}); err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// createPriceList is POST /v1/price-lists: an empty list, by name.
func (s *Server) createPriceList(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Name string `json:"name"`
	}
	if err := decodeBody(r, &in); err != nil {
		s.fail(w, r, err)
		return
	}
	p := principalFrom(r.Context())
	var l links.PriceList
	if err := s.tx(r, func(tx pgx.Tx) error {
		var err error
		if l, err = links.CreateListTx(r.Context(), tx, p.TenantID, in.Name, p.Actor()); err != nil {
			return err
		}
		return auditTx(r, tx, "price_list.create", l.ID.String(), map[string]any{"name": l.Name})
	}); err != nil {
		s.fail(w, r, linkMessageErr(err))
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"list": l})
}

// renamePriceList is PATCH /v1/price-lists/{id}: its name.
func (s *Server) renamePriceList(w http.ResponseWriter, r *http.Request) {
	id, err := priceListParam(r)
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
	s.priceChange(w, r, "price_list.rename", id.String(), map[string]any{"name": in.Name}, func(tx pgx.Tx, tenant uuid.UUID) ([]uuid.UUID, error) {
		return links.RenameListTx(r.Context(), tx, tenant, id, in.Name)
	})
}

// deletePriceList is DELETE /v1/price-lists/{id}: withdrawn from every
// buyer it was shared with.
func (s *Server) deletePriceList(w http.ResponseWriter, r *http.Request) {
	id, err := priceListParam(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.priceChange(w, r, "price_list.delete", id.String(), nil, func(tx pgx.Tx, tenant uuid.UUID) ([]uuid.UUID, error) {
		return links.DeleteListTx(r.Context(), tx, tenant, id)
	})
}

// putPriceListItems is PUT /v1/price-lists/{id}/items: the list's items,
// each one of the business's own stock items, with the price a linked
// buyer pays, a minimum quantity and whether it is available.
func (s *Server) putPriceListItems(w http.ResponseWriter, r *http.Request) {
	id, err := priceListParam(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	var in struct {
		Items []struct {
			ItemID      string `json:"item_id"`
			PriceKobo   int64  `json:"price_kobo"`
			MinQuantity int    `json:"min_quantity"`
			Available   *bool  `json:"available"`
		} `json:"items"`
	}
	if err := decodeBody(r, &in); err != nil {
		s.fail(w, r, err)
		return
	}
	p := principalFrom(r.Context())
	var own map[string]stock.Item
	if len(in.Items) > 0 {
		if !s.Stock.Ready() {
			s.stockFail(w, r, errStockOff)
			return
		}
		_, items, err := s.stockItems(r.Context(), p.TenantID)
		if err != nil {
			s.stockFail(w, r, err)
			return
		}
		own = map[string]stock.Item{}
		for _, it := range items {
			own[it.ID] = it
		}
	}
	list := make([]links.PriceItem, 0, len(in.Items))
	for _, x := range in.Items {
		it, ok := own[x.ItemID]
		if !ok {
			s.fail(w, r, fmt.Errorf("%w: %q is not one of your items", errBadRequest, x.ItemID))
			return
		}
		pi := links.PriceItem{ItemID: it.ID, Name: it.Name, Unit: it.Unit, PriceKobo: x.PriceKobo, MinQuantity: max(1, x.MinQuantity), Available: true}
		if x.Available != nil {
			pi.Available = *x.Available
		}
		list = append(list, pi)
	}
	s.priceChange(w, r, "price_list.items", id.String(), map[string]any{"items": len(list)}, func(tx pgx.Tx, tenant uuid.UUID) ([]uuid.UUID, error) {
		return links.SetListItemsTx(r.Context(), tx, tenant, id, list)
	})
}

// sharePriceList is PUT /v1/price-lists/{id}/links: the linked buyers it
// is shared with, exactly (a link has one list: one shared with another
// moves here; one not named is withdrawn).
func (s *Server) sharePriceList(w http.ResponseWriter, r *http.Request) {
	id, err := priceListParam(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	var in struct {
		Links []uuid.UUID `json:"links"`
	}
	if err := decodeBody(r, &in); err != nil {
		s.fail(w, r, err)
		return
	}
	if in.Links == nil {
		in.Links = []uuid.UUID{}
	}
	p := principalFrom(r.Context())
	s.priceChange(w, r, "price_list.share", id.String(), map[string]any{"links": in.Links}, func(tx pgx.Tx, tenant uuid.UUID) ([]uuid.UUID, error) {
		return links.ShareListTx(r.Context(), tx, tenant, id, in.Links, p.Actor())
	})
}

// priceChange writes a seller's change and its audit, answers the lists,
// and brings the changed links' prices to their buyers.
func (s *Server) priceChange(w http.ResponseWriter, r *http.Request, action, target string, detail map[string]any, fn func(pgx.Tx, uuid.UUID) ([]uuid.UUID, error)) {
	p := principalFrom(r.Context())
	var changed []uuid.UUID
	var out priceListsOut
	if err := s.tx(r, func(tx pgx.Tx) error {
		var err error
		if changed, err = fn(tx, p.TenantID); err != nil {
			return err
		}
		if err := auditTx(r, tx, action, target, detail); err != nil {
			return err
		}
		out.Lists, err = links.ListsTx(r.Context(), tx, p.TenantID)
		return err
	}); err != nil {
		s.fail(w, r, linkMessageErr(err))
		return
	}
	s.mirrorPrices(r.Context(), p.TenantID, changed)
	writeJSON(w, http.StatusOK, out)
}

type linkPricesOut struct {
	Link  uuid.UUID         `json:"link"`
	List  string            `json:"list,omitempty"`
	Items []links.PriceItem `json:"items"`
}

// getLinkPrices is GET /v1/link-prices/{link}: what one linked buyer sees,
// its own prices marked.
func (s *Server) getLinkPrices(w http.ResponseWriter, r *http.Request) {
	link, err := linkParam(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	p := principalFrom(r.Context())
	out := linkPricesOut{Link: link, Items: []links.PriceItem{}}
	if err := s.tx(r, func(tx pgx.Tx) error {
		if _, err := links.GetTx(r.Context(), tx, link); err != nil {
			return err
		}
		name, items, ok, err := links.LinkPricesTx(r.Context(), tx, p.TenantID, link)
		if ok {
			out.List, out.Items = name, items
		}
		return err
	}); err != nil {
		s.fail(w, r, linkMessageErr(err))
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// putLinkPrices is PUT /v1/link-prices/{link}: one linked buyer's own
// prices ({prices: {item: kobo}}; none for the list's).
func (s *Server) putLinkPrices(w http.ResponseWriter, r *http.Request) {
	link, err := linkParam(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	var in struct {
		Prices map[string]int64 `json:"prices"`
	}
	if err := decodeBody(r, &in); err != nil {
		s.fail(w, r, err)
		return
	}
	p := principalFrom(r.Context())
	out := linkPricesOut{Link: link, Items: []links.PriceItem{}}
	if err := s.tx(r, func(tx pgx.Tx) error {
		if err := links.SetOverridesTx(r.Context(), tx, p.TenantID, link, in.Prices, p.Actor()); err != nil {
			return err
		}
		if err := auditTx(r, tx, "price_list.link_prices", link.String(), map[string]any{"prices": len(in.Prices)}); err != nil {
			return err
		}
		name, items, ok, err := links.LinkPricesTx(r.Context(), tx, p.TenantID, link)
		if ok {
			out.List, out.Items = name, items
		}
		return err
	}); err != nil {
		s.fail(w, r, linkMessageErr(err))
		return
	}
	s.mirrorPrices(r.Context(), p.TenantID, []uuid.UUID{link})
	writeJSON(w, http.StatusOK, out)
}

type supplierPricesOut struct {
	Suppliers []links.SupplierPrices `json:"suppliers"`
}

// listSupplierPrices is GET /v1/supplier-prices: the prices linked
// suppliers share with the business (?link= for one).
func (s *Server) listSupplierPrices(w http.ResponseWriter, r *http.Request) {
	var link uuid.UUID
	if v := r.URL.Query().Get("link"); v != "" {
		var err error
		if link, err = uuid.Parse(v); err != nil {
			s.fail(w, r, fmt.Errorf("%w: link is a link's id", errBadRequest))
			return
		}
	}
	p := principalFrom(r.Context())
	var out supplierPricesOut
	if err := s.tx(r, func(tx pgx.Tx) error {
		var err error
		out.Suppliers, err = links.SupplierPricesTx(r.Context(), tx, p.TenantID, link)
		return err
	}); err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// listItemLinks is GET /v1/item-links/{link}: which of the business's own
// items each of a linked supplier's items is.
func (s *Server) listItemLinks(w http.ResponseWriter, r *http.Request) {
	link, err := linkParam(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	p := principalFrom(r.Context())
	var out []links.ItemLink
	if err := s.tx(r, func(tx pgx.Tx) error {
		var err error
		out, err = links.ItemLinksTx(r.Context(), tx, p.TenantID, link)
		return err
	}); err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"item_links": out})
}

// putItemLink is PUT /v1/item-links/{link}: {item_id, seller_item_id}:
// which of the business's own items a supplier's item (on its price list)
// is. Its orders then reach the supplier as that item.
func (s *Server) putItemLink(w http.ResponseWriter, r *http.Request) {
	link, err := linkParam(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	var in struct {
		ItemID       string `json:"item_id"`
		SellerItemID string `json:"seller_item_id"`
	}
	if err := decodeBody(r, &in); err != nil {
		s.fail(w, r, err)
		return
	}
	p := principalFrom(r.Context())
	own, err := s.ownItem(r.Context(), p.TenantID, in.ItemID)
	if err != nil {
		s.stockFail(w, r, err)
		return
	}
	if err := s.tx(r, func(tx pgx.Tx) error {
		theirs, err := sellerItemOn(r.Context(), tx, p.TenantID, link, in.SellerItemID)
		if err != nil {
			return err
		}
		if err := links.SetItemLinkTx(r.Context(), tx, p.TenantID, link, own.ID, theirs.ItemID, theirs.Name, p.Actor()); err != nil {
			return err
		}
		return auditTx(r, tx, "item_link.set", link.String(), map[string]any{"item": own.ID, "seller_item": theirs.ItemID})
	}); err != nil {
		s.fail(w, r, linkMessageErr(err))
		return
	}
	s.listItemLinks(w, r)
}

// deleteItemLink is DELETE /v1/item-links/{link}/{item}.
func (s *Server) deleteItemLink(w http.ResponseWriter, r *http.Request) {
	link, err := linkParam(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	item := chi.URLParam(r, "item")
	p := principalFrom(r.Context())
	if err := s.tx(r, func(tx pgx.Tx) error {
		if err := links.UnlinkItemTx(r.Context(), tx, p.TenantID, link, item); err != nil {
			return err
		}
		return auditTx(r, tx, "item_link.remove", link.String(), map[string]any{"item": item})
	}); err != nil {
		s.fail(w, r, linkMessageErr(err))
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ownItem is one of the business's own stock items.
func (s *Server) ownItem(ctx context.Context, tenant uuid.UUID, id string) (stock.Item, error) {
	if !s.Stock.Ready() {
		return stock.Item{}, errStockOff
	}
	_, items, err := s.stockItems(ctx, tenant)
	if err != nil {
		return stock.Item{}, err
	}
	for _, it := range items {
		if it.ID == id {
			return it, nil
		}
	}
	return stock.Item{}, errNoItem
}

// sellerItemOn is an item on the price list a linked supplier shares with
// the business in scope.
func sellerItemOn(ctx context.Context, tx pgx.Tx, tenant, link uuid.UUID, id string) (links.PriceItem, error) {
	got, err := links.SupplierPricesTx(ctx, tx, tenant, link)
	if err != nil {
		return links.PriceItem{}, err
	}
	for _, p := range got {
		for _, it := range p.Items {
			if it.ItemID == id {
				return it, nil
			}
		}
	}
	return links.PriceItem{}, fmt.Errorf("%w: that is not on their price list", links.ErrInvalid)
}

// orderLineIn is a line ordered from a supplier's price list: its item,
// how many, and, the first time, which of the business's own items it is.
type orderLineIn struct {
	SellerItemID string `json:"seller_item_id"`
	Quantity     int64  `json:"quantity"`
	ItemID       string `json:"item_id"`
}

// errUnlinkedItem names a price-list item no own item is linked to yet.
type errUnlinkedItem struct{ Item links.PriceItem }

func (e errUnlinkedItem) Error() string {
	return fmt.Sprintf("say which of your items %s is", e.Item.Name)
}

// draftFromPricesTx drafts a purchase order to a linked supplier from its
// price list, in the business in scope: each line at the list's price, at
// least its minimum, while available; each line's own item from the item
// link, or the one named (then linked). It answers the order, drafted.
func (s *Server) draftFromPricesTx(ctx context.Context, tx pgx.Tx, p *Principal, link uuid.UUID, in []orderLineIn, own map[string]stock.Item, note, key string) (stock.Purchase, error) {
	got, err := links.SupplierPricesTx(ctx, tx, p.TenantID, link)
	if err != nil {
		return stock.Purchase{}, err
	}
	if len(got) == 0 {
		return stock.Purchase{}, fmt.Errorf("%w: they share no price list with you", links.ErrState)
	}
	listed := map[string]links.PriceItem{}
	for _, it := range got[0].Items {
		listed[it.ItemID] = it
	}
	linked, err := links.ItemLinksTx(ctx, tx, p.TenantID, link)
	if err != nil {
		return stock.Purchase{}, err
	}
	mine := map[string]string{}
	for _, l := range linked {
		mine[l.SellerItemID] = l.ItemID
	}
	var supplier uuid.UUID
	err = tx.QueryRow(ctx, `SELECT id FROM suppliers WHERE tenant_id = $1 AND link_id = $2 AND retired_at IS NULL ORDER BY created_at LIMIT 1`, p.TenantID, link).Scan(&supplier)
	if errors.Is(err, pgx.ErrNoRows) {
		return stock.Purchase{}, fmt.Errorf("%w: mark %s as a supplier on Taskiem on the Suppliers page first", links.ErrState, got[0].Business)
	}
	if err != nil {
		return stock.Purchase{}, err
	}
	if len(in) == 0 {
		return stock.Purchase{}, fmt.Errorf("%w: an order has at least one line", links.ErrInvalid)
	}
	lines := make([]stock.PurchaseLine, 0, len(in))
	for _, l := range in {
		it, ok := listed[l.SellerItemID]
		switch {
		case !ok:
			return stock.Purchase{}, fmt.Errorf("%w: that is not on their price list", links.ErrInvalid)
		case !it.Available:
			return stock.Purchase{}, fmt.Errorf("%w: %s is not available now", links.ErrInvalid, it.Name)
		case l.Quantity < int64(it.MinQuantity):
			return stock.Purchase{}, fmt.Errorf("%w: order at least %d of %s", links.ErrInvalid, it.MinQuantity, it.Name)
		}
		item := mine[it.ItemID]
		if l.ItemID != "" && l.ItemID != item {
			if _, ok := own[l.ItemID]; !ok {
				return stock.Purchase{}, fmt.Errorf("%w: %q is not one of your items", links.ErrInvalid, l.ItemID)
			}
			if err := links.SetItemLinkTx(ctx, tx, p.TenantID, link, l.ItemID, it.ItemID, it.Name, p.Actor()); err != nil {
				return stock.Purchase{}, err
			}
			item = l.ItemID
		}
		if item == "" {
			return stock.Purchase{}, errUnlinkedItem{Item: it}
		}
		o, ok := own[item]
		if !ok {
			return stock.Purchase{}, errUnlinkedItem{Item: it}
		}
		lines = append(lines, stock.PurchaseLine{ItemID: o.ID, Item: o.Name, Unit: o.Unit, Quantity: l.Quantity, CostKobo: it.PriceKobo})
	}
	return stock.DraftTx(ctx, tx, p.TenantID, stock.DraftIn{Supplier: supplier, Lines: lines, Note: note, Actor: p.Actor(), Key: key})
}

// orderFromPrices is POST /v1/supplier-prices/{link}/order, with an
// Idempotency-Key: a purchase order drafted from a linked supplier's
// price list ({lines: [{seller_item_id, quantity, item_id}], note}), at
// its prices; sent as any order is (POST /v1/stock/purchases/{id}/send).
// A line whose item is not linked to one of the business's own names it
// (422, item: the supplier's item) unless item_id says which, which then
// links it.
func (s *Server) orderFromPrices(w http.ResponseWriter, r *http.Request) {
	if !s.purchasesReady(w) {
		return
	}
	link, err := linkParam(r)
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
		Lines []orderLineIn `json:"lines"`
		Note  string        `json:"note"`
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
	own := map[string]stock.Item{}
	for _, it := range items {
		own[it.ID] = it
	}
	var x stock.Purchase
	err = s.tx(r, func(tx pgx.Tx) error {
		var err error
		x, err = s.draftFromPricesTx(r.Context(), tx, p, link, in.Lines, own, strings.TrimSpace(in.Note), "prices-"+key)
		return err
	})
	var unlinked errUnlinkedItem
	switch {
	case errors.As(err, &unlinked):
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": unlinked.Error(), "code": "item_unlinked", "item": unlinked.Item})
		return
	case errors.Is(err, stock.ErrPurchase), errors.Is(err, stock.ErrNoSupplier):
		s.purchaseFail(w, r, err)
		return
	case err != nil:
		s.fail(w, r, linkMessageErr(err))
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"purchase": x})
}
