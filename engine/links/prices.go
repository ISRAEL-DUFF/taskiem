package links

// Price lists and item links between linked businesses (L4;
// docs/spec/business-links.md §9; migration 00280). The seller's list is
// built on its own stock items: per item the price a linked buyer pays, a
// minimum quantity and whether it is available. It is shared with links,
// one list per link, and a link may override a price. The buyer's view of
// it is a mirror in its own business, rewritten through the door by
// MirrorPricesTx whenever the list, its sharing or an override changes.
// An item link is the buyer's: which of its own items a seller's item is.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// MaxListItems caps a price list.
const MaxListItems = 500

// PriceList is one of the seller's price lists: its items, the links it
// is shared with, and each link's overrides.
type PriceList struct {
	ID        uuid.UUID   `json:"id"`
	Name      string      `json:"name"`
	Items     []PriceItem `json:"items"`
	Links     []uuid.UUID `json:"links"`
	UpdatedAt time.Time   `json:"updated_at"`
}

// PriceItem is an item on a price list, as the seller sets it and the
// buyer sees it.
type PriceItem struct {
	ItemID      string `json:"item_id"`
	Name        string `json:"name"`
	Unit        string `json:"unit,omitempty"`
	PriceKobo   int64  `json:"price_kobo"`
	MinQuantity int    `json:"min_quantity"`
	Available   bool   `json:"available"`
	// Overridden: this link's own price, not the list's (the seller's
	// view of one link).
	Overridden bool `json:"overridden,omitempty"`
}

// ListsTx is the business in scope's price lists, by name.
func ListsTx(ctx context.Context, tx pgx.Tx, tenant uuid.UUID) ([]PriceList, error) {
	rows, err := tx.Query(ctx, `SELECT id, name, updated_at FROM price_lists WHERE tenant_id = $1 ORDER BY lower(name)`, tenant)
	if err != nil {
		return nil, err
	}
	out, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (PriceList, error) {
		var l PriceList
		return l, r.Scan(&l.ID, &l.Name, &l.UpdatedAt)
	})
	if err != nil {
		return nil, err
	}
	for i := range out {
		if out[i].Items, err = listItemsTx(ctx, tx, out[i].ID); err != nil {
			return nil, err
		}
		rows, err := tx.Query(ctx, `SELECT link_id FROM price_list_shares WHERE tenant_id = $1 AND list_id = $2 ORDER BY shared_at`, tenant, out[i].ID)
		if err != nil {
			return nil, err
		}
		if out[i].Links, err = pgx.CollectRows(rows, pgx.RowTo[uuid.UUID]); err != nil {
			return nil, err
		}
	}
	if out == nil {
		out = []PriceList{}
	}
	return out, nil
}

func listItemsTx(ctx context.Context, tx pgx.Tx, list uuid.UUID) ([]PriceItem, error) {
	rows, err := tx.Query(ctx, `SELECT item_id, name, unit, price_kobo, min_quantity, available FROM price_list_items WHERE list_id = $1 ORDER BY position, lower(name)`, list)
	if err != nil {
		return nil, err
	}
	out, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (PriceItem, error) {
		var it PriceItem
		return it, r.Scan(&it.ItemID, &it.Name, &it.Unit, &it.PriceKobo, &it.MinQuantity, &it.Available)
	})
	if out == nil {
		out = []PriceItem{}
	}
	return out, err
}

// CreateListTx makes an empty price list.
func CreateListTx(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, name, by string) (PriceList, error) {
	name = strings.TrimSpace(name)
	if name == "" || len([]rune(name)) > 80 {
		return PriceList{}, fmt.Errorf("%w: a price list has a name of up to 80 characters", ErrInvalid)
	}
	l := PriceList{ID: uuid.Must(uuid.NewV7()), Name: name, Items: []PriceItem{}, Links: []uuid.UUID{}}
	err := tx.QueryRow(ctx, `INSERT INTO price_lists (id, tenant_id, name, created_by) VALUES ($1, $2, $3, $4) ON CONFLICT (tenant_id, name) DO NOTHING
		RETURNING updated_at`, l.ID, tenant, name, by).Scan(&l.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return l, fmt.Errorf("%w: there is a price list called %s already", ErrInvalid, name)
	}
	return l, err
}

// SetListItemsTx replaces a price list's items (each already checked to
// be one of the seller's own, with its name and unit) and puts the links
// it is shared with on the list to mirror. It answers those links.
func SetListItemsTx(ctx context.Context, tx pgx.Tx, tenant, list uuid.UUID, items []PriceItem) ([]uuid.UUID, error) {
	if err := ownList(ctx, tx, tenant, list); err != nil {
		return nil, err
	}
	if len(items) > MaxListItems {
		return nil, fmt.Errorf("%w: a price list has up to %d items", ErrInvalid, MaxListItems)
	}
	seen := map[string]bool{}
	for _, it := range items {
		if it.ItemID == "" || seen[it.ItemID] {
			return nil, fmt.Errorf("%w: each item once", ErrInvalid)
		}
		seen[it.ItemID] = true
		if it.PriceKobo <= 0 || it.MinQuantity < 1 {
			return nil, fmt.Errorf("%w: %s needs a price and a minimum of at least 1", ErrInvalid, it.Name)
		}
	}
	if _, err := tx.Exec(ctx, `DELETE FROM price_list_items WHERE list_id = $1`, list); err != nil {
		return nil, err
	}
	for i, it := range items {
		if _, err := tx.Exec(ctx, `INSERT INTO price_list_items (tenant_id, list_id, item_id, name, unit, price_kobo, min_quantity, available, position)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`, tenant, list, it.ItemID, cutRunes(it.Name, 120), cutRunes(it.Unit, 40), it.PriceKobo, it.MinQuantity, it.Available, i); err != nil {
			return nil, err
		}
	}
	if _, err := tx.Exec(ctx, `UPDATE price_lists SET updated_at = now() WHERE id = $1`, list); err != nil {
		return nil, err
	}
	return sharedDueTx(ctx, tx, tenant, list)
}

// RenameListTx renames a price list; the buyers it is shared with see
// the new name.
func RenameListTx(ctx context.Context, tx pgx.Tx, tenant, list uuid.UUID, name string) ([]uuid.UUID, error) {
	name = strings.TrimSpace(name)
	if name == "" || len([]rune(name)) > 80 {
		return nil, fmt.Errorf("%w: a price list has a name of up to 80 characters", ErrInvalid)
	}
	tag, err := tx.Exec(ctx, `UPDATE price_lists SET name = $3, updated_at = now() WHERE tenant_id = $1 AND id = $2`, tenant, list, name)
	if err != nil {
		if strings.Contains(err.Error(), "price_lists_tenant_id_name_key") {
			return nil, fmt.Errorf("%w: there is a price list called %s already", ErrInvalid, name)
		}
		return nil, err
	}
	if tag.RowsAffected() == 0 {
		return nil, ErrNotFound
	}
	return sharedDueTx(ctx, tx, tenant, list)
}

// DeleteListTx deletes a price list: withdrawn from every link.
func DeleteListTx(ctx context.Context, tx pgx.Tx, tenant, list uuid.UUID) ([]uuid.UUID, error) {
	links, err := sharedDueTx(ctx, tx, tenant, list)
	if err != nil {
		return nil, err
	}
	tag, err := tx.Exec(ctx, `DELETE FROM price_lists WHERE tenant_id = $1 AND id = $2`, tenant, list)
	if err != nil {
		return nil, err
	}
	if tag.RowsAffected() == 0 {
		return nil, ErrNotFound
	}
	return links, nil
}

// ShareListTx shares a price list with exactly these links (each an
// active link of the business's, with a buyer it supplies): a link
// shared with another list moves to this one; a link no longer named is
// withdrawn. It answers every link whose prices changed.
func ShareListTx(ctx context.Context, tx pgx.Tx, tenant, list uuid.UUID, to []uuid.UUID, by string) ([]uuid.UUID, error) {
	if err := ownList(ctx, tx, tenant, list); err != nil {
		return nil, err
	}
	for _, l := range to {
		x, err := GetTx(ctx, tx, l)
		if err != nil {
			return nil, err
		}
		if x.State != Active {
			return nil, fmt.Errorf("%w: you are no longer linked with %s", ErrState, x.Name)
		}
	}
	before, err := sharedDueTx(ctx, tx, tenant, list)
	if err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM price_list_shares WHERE tenant_id = $1 AND (list_id = $2 OR link_id = ANY ($3))`, tenant, list, to); err != nil {
		return nil, err
	}
	for _, l := range to {
		if _, err := tx.Exec(ctx, `INSERT INTO price_list_shares (tenant_id, link_id, list_id, shared_by) VALUES ($1, $2, $3, $4)`, tenant, l, list, by); err != nil {
			return nil, err
		}
		if err := dueTx(ctx, tx, tenant, "prices", l, 0); err != nil {
			return nil, err
		}
	}
	return union(before, to), nil
}

// OverridesTx is a link's own prices, by the seller's item.
func OverridesTx(ctx context.Context, tx pgx.Tx, tenant, link uuid.UUID) (map[string]int64, error) {
	rows, err := tx.Query(ctx, `SELECT item_id, price_kobo FROM price_list_overrides WHERE tenant_id = $1 AND link_id = $2`, tenant, link)
	if err != nil {
		return nil, err
	}
	out := map[string]int64{}
	for rows.Next() {
		var id string
		var p int64
		if err := rows.Scan(&id, &p); err != nil {
			rows.Close()
			return nil, err
		}
		out[id] = p
	}
	rows.Close()
	return out, rows.Err()
}

// SetOverridesTx replaces a link's own prices (item -> price; none for the
// list's) and puts the link on the list to mirror.
func SetOverridesTx(ctx context.Context, tx pgx.Tx, tenant, link uuid.UUID, prices map[string]int64, by string) error {
	if _, err := GetTx(ctx, tx, link); err != nil {
		return err
	}
	if len(prices) > MaxListItems {
		return fmt.Errorf("%w: up to %d prices", ErrInvalid, MaxListItems)
	}
	if _, err := tx.Exec(ctx, `DELETE FROM price_list_overrides WHERE tenant_id = $1 AND link_id = $2`, tenant, link); err != nil {
		return err
	}
	for id, p := range prices {
		if id == "" || p <= 0 {
			return fmt.Errorf("%w: a price for a link is more than ₦0", ErrInvalid)
		}
		if _, err := tx.Exec(ctx, `INSERT INTO price_list_overrides (tenant_id, link_id, item_id, price_kobo, updated_by) VALUES ($1, $2, $3, $4, $5)`,
			tenant, link, id, p, by); err != nil {
			return err
		}
	}
	return dueTx(ctx, tx, tenant, "prices", link, 0)
}

// LinkPricesTx is the prices a link sees: its list's items at the list's
// prices, a link's own price over it (marked). ok is false when no list is
// shared with the link.
func LinkPricesTx(ctx context.Context, tx pgx.Tx, tenant, link uuid.UUID) (name string, items []PriceItem, ok bool, err error) {
	var list uuid.UUID
	err = tx.QueryRow(ctx, `SELECT l.id, l.name FROM price_list_shares s JOIN price_lists l ON l.id = s.list_id WHERE s.tenant_id = $1 AND s.link_id = $2`,
		tenant, link).Scan(&list, &name)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil, false, nil
	}
	if err != nil {
		return "", nil, false, err
	}
	if items, err = listItemsTx(ctx, tx, list); err != nil {
		return "", nil, false, err
	}
	over, err := OverridesTx(ctx, tx, tenant, link)
	if err != nil {
		return "", nil, false, err
	}
	for i := range items {
		if p, ok := over[items[i].ItemID]; ok {
			items[i].PriceKobo, items[i].Overridden = p, true
		}
	}
	return name, items, true, nil
}

// MirrorPricesTx rewrites the buyer's view of a link's prices from the
// seller's (the business in scope): its list, at the link's prices, or
// nothing when none is shared. Through the door, so the seller's reads
// come first; one link per transaction.
func MirrorPricesTx(ctx context.Context, tx pgx.Tx, seller, link uuid.UUID) error {
	if err := Undue(ctx, tx, seller, "prices", link); err != nil {
		return err
	}
	ok, err := active(ctx, tx, link)
	if err != nil || !ok {
		return err
	}
	name, items, shared, err := LinkPricesTx(ctx, tx, seller, link)
	if err != nil {
		return err
	}
	detail, _ := json.Marshal(map[string]any{"shared": shared, "items": len(items)})
	buyer, err := EnterTx(ctx, tx, link, "link.prices", detail, Actor{Type: "system", ID: "link-prices"})
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM supplier_prices WHERE tenant_id = $1 AND link_id = $2`, buyer, link); err != nil {
		return err
	}
	if !shared {
		_, err := tx.Exec(ctx, `DELETE FROM supplier_price_lists WHERE tenant_id = $1 AND link_id = $2`, buyer, link)
		return err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO supplier_price_lists (tenant_id, link_id, name) VALUES ($1, $2, $3)
		ON CONFLICT (tenant_id, link_id) DO UPDATE SET name = EXCLUDED.name, updated_at = now()`, buyer, link, name); err != nil {
		return err
	}
	for i, it := range items {
		if _, err := tx.Exec(ctx, `INSERT INTO supplier_prices (tenant_id, link_id, item_id, name, unit, price_kobo, min_quantity, available, position)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`, buyer, link, it.ItemID, it.Name, it.Unit, it.PriceKobo, it.MinQuantity, it.Available, i); err != nil {
			return err
		}
	}
	return nil
}

// SupplierPrices is a linked supplier's prices, as the buyer sees them.
type SupplierPrices struct {
	LinkID    uuid.UUID   `json:"link"`
	Business  string      `json:"business"`
	Name      string      `json:"name"`
	Items     []PriceItem `json:"items"`
	UpdatedAt time.Time   `json:"updated_at"`
}

// SupplierPricesTx is the prices linked suppliers share with the business
// in scope, one per link (link uuid.Nil: all).
func SupplierPricesTx(ctx context.Context, tx pgx.Tx, tenant, link uuid.UUID) ([]SupplierPrices, error) {
	rows, err := tx.Query(ctx, `SELECT link_id, name, updated_at FROM supplier_price_lists WHERE tenant_id = $1 AND ($2 = '00000000-0000-0000-0000-000000000000'::uuid OR link_id = $2)`,
		tenant, link)
	if err != nil {
		return nil, err
	}
	out, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (SupplierPrices, error) {
		var p SupplierPrices
		return p, r.Scan(&p.LinkID, &p.Name, &p.UpdatedAt)
	})
	if err != nil {
		return nil, err
	}
	list, _, err := ListTx(ctx, tx)
	if err != nil {
		return nil, err
	}
	names := map[uuid.UUID]string{}
	for _, l := range list {
		if l.State == Active {
			names[l.ID] = l.Name
		}
	}
	keep := out[:0]
	for _, p := range out {
		n, ok := names[p.LinkID]
		if !ok {
			continue // a link no longer active: its prices are not offered
		}
		p.Business = n
		rows, err := tx.Query(ctx, `SELECT item_id, name, unit, price_kobo, min_quantity, available FROM supplier_prices WHERE tenant_id = $1 AND link_id = $2
			ORDER BY position, lower(name)`, tenant, p.LinkID)
		if err != nil {
			return nil, err
		}
		if p.Items, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (PriceItem, error) {
			var it PriceItem
			return it, r.Scan(&it.ItemID, &it.Name, &it.Unit, &it.PriceKobo, &it.MinQuantity, &it.Available)
		}); err != nil {
			return nil, err
		}
		if p.Items == nil {
			p.Items = []PriceItem{}
		}
		keep = append(keep, p)
	}
	return keep, nil
}

// ItemLink is which of the buyer's own items a linked seller's item is.
type ItemLink struct {
	LinkID       uuid.UUID `json:"link"`
	ItemID       string    `json:"item_id"`
	SellerItemID string    `json:"seller_item_id"`
	SellerItem   string    `json:"seller_item"`
}

// ItemLinksTx is the business in scope's item links with a link.
func ItemLinksTx(ctx context.Context, tx pgx.Tx, tenant, link uuid.UUID) ([]ItemLink, error) {
	rows, err := tx.Query(ctx, `SELECT link_id, item_id, seller_item_id, seller_item FROM item_links WHERE tenant_id = $1 AND link_id = $2 ORDER BY lower(seller_item)`, tenant, link)
	if err != nil {
		return nil, err
	}
	out, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (ItemLink, error) {
		var l ItemLink
		return l, r.Scan(&l.LinkID, &l.ItemID, &l.SellerItemID, &l.SellerItem)
	})
	if out == nil {
		out = []ItemLink{}
	}
	return out, err
}

// SetItemLinkTx says which of the buyer's own items a seller's item is:
// one each way (a link made again replaces the old one, either way).
func SetItemLinkTx(ctx context.Context, tx pgx.Tx, tenant, link uuid.UUID, item, sellerItem, sellerName, by string) error {
	if item == "" || sellerItem == "" || strings.TrimSpace(sellerName) == "" {
		return fmt.Errorf("%w: your item and theirs", ErrInvalid)
	}
	if _, err := GetTx(ctx, tx, link); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM item_links WHERE tenant_id = $1 AND link_id = $2 AND (item_id = $3 OR seller_item_id = $4)`, tenant, link, item, sellerItem); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `INSERT INTO item_links (tenant_id, link_id, item_id, seller_item_id, seller_item, updated_by) VALUES ($1, $2, $3, $4, $5, $6)`,
		tenant, link, item, sellerItem, cutRunes(strings.TrimSpace(sellerName), 120), by)
	return err
}

// UnlinkItemTx removes one of the buyer's item links.
func UnlinkItemTx(ctx context.Context, tx pgx.Tx, tenant, link uuid.UUID, item string) error {
	tag, err := tx.Exec(ctx, `DELETE FROM item_links WHERE tenant_id = $1 AND link_id = $2 AND item_id = $3`, tenant, link, item)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}

// sellerItemsOf is the buyer's item links with a link, by its own item.
func sellerItemsOf(ctx context.Context, tx pgx.Tx, tenant, link uuid.UUID) (map[string]string, error) {
	got, err := ItemLinksTx(ctx, tx, tenant, link)
	if err != nil {
		return nil, err
	}
	out := map[string]string{}
	for _, l := range got {
		out[l.ItemID] = l.SellerItemID
	}
	return out, nil
}

func ownList(ctx context.Context, tx pgx.Tx, tenant, list uuid.UUID) error {
	var ok bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM price_lists WHERE tenant_id = $1 AND id = $2)`, tenant, list).Scan(&ok); err != nil {
		return err
	}
	if !ok {
		return ErrNotFound
	}
	return nil
}

// sharedDueTx puts every link a list is shared with on the list to mirror,
// and answers them.
func sharedDueTx(ctx context.Context, tx pgx.Tx, tenant, list uuid.UUID) ([]uuid.UUID, error) {
	rows, err := tx.Query(ctx, `SELECT link_id FROM price_list_shares WHERE tenant_id = $1 AND list_id = $2`, tenant, list)
	if err != nil {
		return nil, err
	}
	links, err := pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
	if err != nil {
		return nil, err
	}
	for _, l := range links {
		if err := dueTx(ctx, tx, tenant, "prices", l, 0); err != nil {
			return nil, err
		}
	}
	return links, nil
}

func union(a, b []uuid.UUID) []uuid.UUID {
	seen := map[uuid.UUID]bool{}
	out := []uuid.UUID{}
	for _, x := range append(append([]uuid.UUID{}, a...), b...) {
		if !seen[x] {
			seen[x] = true
			out = append(out, x)
		}
	}
	return out
}
