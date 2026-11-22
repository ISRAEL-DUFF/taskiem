package stock

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/productstock"
)

// An order's lines on purchase_lines, each treated on its own (what a
// business buys §5, §6, §8; migration 00503): just recorded (the default),
// added to a stock item, or kept track of on Things we have. A line's
// treatment comes from what the business said, else its memory of the
// supplier's item (item_links for a linked supplier, supplier_items for one
// not on Taskiem), else Taskiem's suggestion, which the business confirms.

var (
	// ErrTrackAsk: a tracked line of more than TrackAsk asks first (§5.3):
	// the caller says so (LineChoice.TrackMany), or just records it.
	ErrTrackAsk = errors.New("stock: tracking that many asks first")
	// ErrTrackTooMany: a line of more than MaxTracked is not tracked one
	// by one; it is recorded, and one row added by hand for the lot.
	ErrTrackTooMany = errors.New("stock: too many to keep track of one by one")
	// ErrLineLocked: something on the line was received, so its treatment
	// stays as it is (§6, After it arrived).
	ErrLineLocked = errors.New("stock: the line's treatment is locked")
)

// TrackError says which line asks first, or cannot be tracked: the API
// turns it into "Put 500 nylon bags on Things we have, one row each?".
type TrackError struct {
	Err      error // ErrTrackAsk or ErrTrackTooMany
	Position int
	Item     string
	Quantity int64
}

func (e *TrackError) Error() string {
	if errors.Is(e.Err, ErrTrackTooMany) {
		return fmt.Sprintf("%d %s are too many to keep track of one by one (at most %d): just record them, and add one row for the lot on Things we have", e.Quantity, e.Item, MaxTracked)
	}
	return fmt.Sprintf("put %d %s on Things we have, one row each? Say so, or just record them", e.Quantity, e.Item)
}

func (e *TrackError) Unwrap() error { return e.Err }

// PurchaseLine is one line of an order, as the business sees it (a row of
// purchase_lines). The supplier never sees its treatment, stock item or
// label (§7).
type PurchaseLine struct {
	// ID is the line's own id; Position its place on the order (the
	// order's change and short are by it).
	ID       uuid.UUID `json:"id"`
	Position int       `json:"position"`
	// ItemID is how the order's steps name the line (receiving, chat): the
	// linked supplier's item, else the stock item of a stock line, else the
	// line's own id.
	ItemID   string `json:"item_id"`
	Item     string `json:"item"`
	Unit     string `json:"unit,omitempty"`
	Quantity int64  `json:"quantity"`
	// CostKobo is the price of one.
	CostKobo int64 `json:"cost_kobo"`
	Received int64 `json:"received"`
	// Delivered is what a linked supplier says it delivered, mirrored
	// from its order (docs/spec/business-links.md §7); nil otherwise.
	Delivered *int64 `json:"delivered,omitempty"`
	// Treatment is what happens when it arrives. StockItemID is set
	// exactly for a stock line: receiving moves it by the quantity times
	// Uses, at the cost over Uses (whole kobo), and sets its cost.
	Treatment   Treatment  `json:"treatment,omitempty"`
	StockItemID *uuid.UUID `json:"stock_item_id,omitempty"`
	Uses        int64      `json:"uses,omitempty"`
	LabelID     *uuid.UUID `json:"label_id,omitempty"`
	// SellerItemID is the linked supplier's own item, on an order to one.
	SellerItemID string `json:"seller_item_id,omitempty"`
	// Unconfirmed: the business has not said how to treat this item from
	// this supplier yet (no memory of it): Treatment and LabelID are
	// Taskiem's suggestion, for the screens to ask (§6).
	Unconfirmed bool `json:"unconfirmed,omitempty"`
	// TrackMany, on a line to draft: tracking more than TrackAsk was asked
	// and said yes to.
	TrackMany bool `json:"-"`
	// Suggested, on a line to draft: its Treatment is the caller's guess
	// (an own item of the same name), used only when the business has no
	// memory of the item, and not remembered.
	Suggested bool `json:"-"`
	order     uuid.UUID
	// known: the business has a memory of the item. toConfirm: its
	// remembered treatment asked for a yes no one could give when the line
	// was written (a supplier placed the order), so the line is recorded
	// meanwhile and reads Unconfirmed.
	known, toConfirm bool
}

// LineChoice is how a line is to be treated, as the business says it.
type LineChoice struct {
	Treatment   Treatment  `json:"treatment"`
	StockItemID *uuid.UUID `json:"stock_item_id,omitempty"`
	Uses        int64      `json:"uses,omitempty"`
	LabelID     *uuid.UUID `json:"label_id,omitempty"`
	// TrackMany: tracking more than TrackAsk one by one, said yes to.
	TrackMany bool `json:"track_many,omitempty"`
}

// choice is the line's treatment as a LineChoice.
func (l PurchaseLine) choice() LineChoice {
	return LineChoice{Treatment: l.Treatment, StockItemID: l.StockItemID, Uses: l.Uses, LabelID: l.LabelID, TrackMany: l.TrackMany}
}

func (l *PurchaseLine) take(c LineChoice) {
	l.Treatment, l.StockItemID, l.LabelID, l.TrackMany = c.Treatment, c.StockItemID, c.LabelID, c.TrackMany
	l.Uses = max(c.Uses, 1)
	if c.Treatment != AddToStock {
		l.StockItemID, l.Uses = nil, 1
	}
}

// itemKey is the line's ItemID.
func (l PurchaseLine) itemKey() string {
	switch {
	case l.SellerItemID != "":
		return l.SellerItemID
	case l.Treatment == AddToStock && l.StockItemID != nil:
		return l.StockItemID.String()
	}
	return l.ID.String()
}

// checkChoiceTx checks a treatment for a line of n: a stock line names one
// of the business's stock items, with uses; a tracked one of more than
// TrackAsk was asked, and none of more than MaxTracked is tracked; a label
// is the business's own.
func checkChoiceTx(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, c *LineChoice, item string, position int, n int64) error {
	if c.Treatment == "" {
		c.Treatment = Record
	}
	if !c.Treatment.Valid() {
		return fmt.Errorf("%w: a line is just recorded (record), added to stock (stock) or kept track of (track)", ErrPurchase)
	}
	switch c.Treatment {
	case AddToStock:
		if c.StockItemID == nil {
			return fmt.Errorf("%w: say which stock item %s goes into", ErrPurchase, item)
		}
		c.Uses = max(c.Uses, 1)
		if c.Uses > MaxDelta {
			return fmt.Errorf("%w: how many of the stock item one %s is: 1 to %d", ErrPurchase, item, MaxDelta)
		}
		var ok bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM stock_items WHERE tenant_id = $1 AND id = $2)`, tenant, *c.StockItemID).Scan(&ok); err != nil {
			return err
		}
		if !ok {
			return fmt.Errorf("%w: that is not one of your stock items", ErrPurchase)
		}
	default:
		c.StockItemID, c.Uses = nil, 1
	}
	if c.Treatment == Track {
		if n > MaxTracked {
			return &TrackError{Err: ErrTrackTooMany, Position: position, Item: item, Quantity: n}
		}
		if n > TrackAsk && !c.TrackMany {
			return &TrackError{Err: ErrTrackAsk, Position: position, Item: item, Quantity: n}
		}
	}
	if c.LabelID != nil {
		var ok bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM spend_labels WHERE tenant_id = $1 AND id = $2)`, tenant, *c.LabelID).Scan(&ok); err != nil {
			return err
		}
		if !ok {
			return fmt.Errorf("%w: that is not one of your labels", ErrPurchase)
		}
	}
	return nil
}

// lineCols reads a line, with whether the business has a memory of it:
// item_links by the linked supplier's item, else supplier_items by the
// name.
var lineCols = `l.id, l.purchase_order_id, l.position, l.name, l.unit, l.quantity, l.cost_kobo, l.received, l.delivered, l.treatment, l.stock_item_id, l.uses,
	l.label_id, l.seller_item_id, l.to_confirm,
	CASE WHEN l.seller_item_id IS NOT NULL AND COALESCE(o.link_id, s.link_id) IS NOT NULL
	  THEN EXISTS (SELECT 1 FROM item_links m WHERE m.tenant_id = l.tenant_id AND m.link_id = COALESCE(o.link_id, s.link_id) AND m.seller_item_id = l.seller_item_id)
	  ELSE EXISTS (SELECT 1 FROM supplier_items m WHERE m.tenant_id = l.tenant_id AND m.supplier_id = o.supplier_id AND m.name_key = l.name_key) END`

const lineFrom = ` FROM purchase_lines l JOIN purchase_orders o ON o.tenant_id = l.tenant_id AND o.id = l.purchase_order_id
	JOIN suppliers s ON s.tenant_id = o.tenant_id AND s.id = o.supplier_id`

func scanLine(r pgx.CollectableRow) (PurchaseLine, error) {
	var l PurchaseLine
	var seller *string
	var known bool
	if err := r.Scan(&l.ID, &l.order, &l.Position, &l.Item, &l.Unit, &l.Quantity, &l.CostKobo, &l.Received, &l.Delivered, &l.Treatment, &l.StockItemID, &l.Uses,
		&l.LabelID, &seller, &l.toConfirm, &known); err != nil {
		return l, err
	}
	if seller != nil {
		l.SellerItemID = *seller
	}
	l.known = known
	l.Unconfirmed = (!known || l.toConfirm) && l.Received == 0
	l.ItemID = l.itemKey()
	return l, nil
}

// linesOfTx is the lines of orders, by order, each in its order.
func linesOfTx(ctx context.Context, tx pgx.Tx, orders []uuid.UUID) (map[uuid.UUID][]PurchaseLine, error) {
	out := map[uuid.UUID][]PurchaseLine{}
	if len(orders) == 0 {
		return out, nil
	}
	rows, err := tx.Query(ctx, `SELECT `+lineCols+lineFrom+` WHERE l.purchase_order_id = ANY ($1) ORDER BY l.purchase_order_id, l.position`, orders)
	if err != nil {
		return nil, err
	}
	list, err := pgx.CollectRows(rows, scanLine)
	if err != nil {
		return nil, err
	}
	for _, l := range list {
		out[l.order] = append(out[l.order], l)
	}
	return out, nil
}

// LinesTx is an order's lines, in their order (the order is read in the
// transaction's scope, by id).
func LinesTx(ctx context.Context, tx pgx.Tx, order uuid.UUID) ([]PurchaseLine, error) {
	rows, err := tx.Query(ctx, `SELECT `+lineCols+lineFrom+` WHERE l.purchase_order_id = $1 ORDER BY l.position`, order)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, scanLine)
}

// insertLinesTx writes a new order's lines, in their order.
func insertLinesTx(ctx context.Context, tx pgx.Tx, tenant, order uuid.UUID, lines []PurchaseLine) error {
	for i, l := range lines {
		var seller *string
		if l.SellerItemID != "" {
			seller = &l.SellerItemID
		}
		if l.Treatment == "" {
			l.Treatment = Record
		}
		// The name's key is the engine's (NameKey), the one the memory of
		// a supplier's item is kept by.
		name := cut(l.Item, 200)
		if _, err := tx.Exec(ctx, `INSERT INTO purchase_lines (id, tenant_id, purchase_order_id, position, name, name_key, unit, quantity, cost_kobo, treatment,
			stock_item_id, uses, label_id, seller_item_id, to_confirm) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15)`,
			uuid.Must(uuid.NewV7()), tenant, order, i, name, NameKey(name), cut(l.Unit, 40), l.Quantity, l.CostKobo, l.Treatment, l.StockItemID, max(l.Uses, 1),
			l.LabelID, seller, l.toConfirm); err != nil {
			return err
		}
	}
	return nil
}

// SaveLinesTx writes what an order's steps change of its lines (the
// quantity, the price, what was received or delivered), by their ids, in
// the order given: a line left out is taken off the order, and those kept
// are numbered again from 0. Their treatments stay, but a tracked line
// grown past MaxTracked is recorded instead (nothing was received on it).
// The order is read in the transaction's scope, by id. An order keeps at
// least one line: an empty list is refused.
func SaveLinesTx(ctx context.Context, tx pgx.Tx, order uuid.UUID, lines []PurchaseLine) error {
	if len(lines) == 0 {
		return fmt.Errorf("%w: an order keeps at least one line; cancel it instead", ErrPurchase)
	}
	keep := make([]uuid.UUID, len(lines))
	for i, l := range lines {
		keep[i] = l.ID
	}
	if _, err := tx.Exec(ctx, `DELETE FROM purchase_lines WHERE purchase_order_id = $1 AND NOT (id = ANY ($2))`, order, keep); err != nil {
		return err
	}
	// Positions only move down (lines are only taken off), so each one's
	// new place is free when it moves there.
	for i, l := range lines {
		if _, err := tx.Exec(ctx, `UPDATE purchase_lines SET position = $3, quantity = $4::bigint, cost_kobo = $5, received = $6, delivered = $7,
			treatment = CASE WHEN treatment = 'track' AND $4::bigint > $8::bigint THEN 'record' ELSE treatment END
			WHERE purchase_order_id = $1 AND id = $2`, order, l.ID, i, l.Quantity, l.CostKobo, l.Received, l.Delivered, MaxTracked); err != nil {
			return err
		}
	}
	return nil
}

// --- the memory of a supplier's item (§6) ---

// Remembered is what the business remembers of a supplier's item, with
// its stock item's and label's names for the Suppliers page.
type Remembered struct {
	ItemMemory
	StockItem string `json:"stock_item,omitempty"`
	Label     string `json:"label,omitempty"`
}

const memoryCols = `m.treatment, m.stock_item_id, m.uses, m.label_id, m.updated_by, m.updated_at,
	COALESCE((SELECT i.name FROM stock_items i WHERE i.tenant_id = m.tenant_id AND i.id = m.stock_item_id), ''),
	COALESCE((SELECT b.name FROM spend_labels b WHERE b.tenant_id = m.tenant_id AND b.id = m.label_id), '')`

func scanMemory(r pgx.CollectableRow, extra ...any) (Remembered, error) {
	var m Remembered
	dest := append(extra, &m.Treatment, &m.StockItemID, &m.Uses, &m.LabelID, &m.UpdatedBy, &m.UpdatedAt, &m.StockItem, &m.Label)
	return m, r.Scan(dest...)
}

// ItemLinksTx is the business's memory of a linked supplier's items, by
// their names.
func ItemLinksTx(ctx context.Context, tx pgx.Tx, tenant, link uuid.UUID) ([]Remembered, error) {
	rows, err := tx.Query(ctx, `SELECT m.link_id, m.seller_item_id, m.seller_item, `+memoryCols+` FROM item_links m
		WHERE m.tenant_id = $1 AND m.link_id = $2 ORDER BY lower(m.seller_item), m.seller_item_id`, tenant, link)
	if err != nil {
		return nil, err
	}
	out, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (Remembered, error) {
		var link uuid.UUID
		var id, name string
		m, err := scanMemory(r, &link, &id, &name)
		m.LinkID, m.SellerItemID, m.Name = &link, id, name
		return m, err
	})
	if out == nil {
		out = []Remembered{}
	}
	return out, err
}

// SupplierItemsTx is the business's memory of the items of a supplier not
// on Taskiem, by their names.
func SupplierItemsTx(ctx context.Context, tx pgx.Tx, tenant, supplier uuid.UUID) ([]Remembered, error) {
	rows, err := tx.Query(ctx, `SELECT m.supplier_id, m.name_key, m.name, `+memoryCols+` FROM supplier_items m
		WHERE m.tenant_id = $1 AND m.supplier_id = $2 ORDER BY m.name_key`, tenant, supplier)
	if err != nil {
		return nil, err
	}
	out, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (Remembered, error) {
		var sup uuid.UUID
		var key, name string
		m, err := scanMemory(r, &sup, &key, &name)
		m.SupplierID, m.NameKey, m.Name = &sup, key, name
		return m, err
	})
	if out == nil {
		out = []Remembered{}
	}
	return out, err
}

// SetItemLinkTx remembers how to treat a linked supplier's item: orders
// drafted after it are filled from it; nothing drafted or received before
// changes.
func SetItemLinkTx(ctx context.Context, tx pgx.Tx, tenant, link uuid.UUID, sellerItem, name string, c LineChoice, actor string) error {
	name = cut(strings.Join(strings.Fields(name), " "), 120)
	if sellerItem == "" || len(sellerItem) > 100 || name == "" {
		return fmt.Errorf("%w: the supplier's item and its name", ErrPurchase)
	}
	c.TrackMany = true // the memory is of the item, not of a quantity
	if err := checkChoiceTx(ctx, tx, tenant, &c, name, 0, 1); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `INSERT INTO item_links (tenant_id, link_id, seller_item_id, seller_item, treatment, stock_item_id, uses, label_id, updated_by)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		ON CONFLICT (tenant_id, link_id, seller_item_id) DO UPDATE SET seller_item = EXCLUDED.seller_item, treatment = EXCLUDED.treatment,
		  stock_item_id = EXCLUDED.stock_item_id, uses = EXCLUDED.uses, label_id = EXCLUDED.label_id, updated_by = EXCLUDED.updated_by, updated_at = now()`,
		tenant, link, sellerItem, name, c.Treatment, c.StockItemID, c.Uses, c.LabelID, actor)
	if err != nil {
		return err
	}
	return stockAudit(ctx, tx, tenant, "item_link.set", name, map[string]any{"link": link, "seller_item": sellerItem, "treatment": c.Treatment, "by": actor})
}

// ClearItemLinkTx forgets a linked supplier's item: its next order asks
// again.
func ClearItemLinkTx(ctx context.Context, tx pgx.Tx, tenant, link uuid.UUID, sellerItem, actor string) error {
	tag, err := tx.Exec(ctx, `DELETE FROM item_links WHERE tenant_id = $1 AND link_id = $2 AND seller_item_id = $3`, tenant, link, sellerItem)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("%w: nothing is remembered of that item", ErrNoItem)
	}
	return stockAudit(ctx, tx, tenant, "item_link.removed", sellerItem, map[string]any{"link": link, "by": actor})
}

// SetSupplierItemTx remembers how to treat an item of a supplier not on
// Taskiem, by its name as typed.
func SetSupplierItemTx(ctx context.Context, tx pgx.Tx, tenant, supplier uuid.UUID, name string, c LineChoice, actor string) error {
	name = cut(strings.Join(strings.Fields(name), " "), 200)
	if name == "" {
		return fmt.Errorf("%w: the item's name", ErrPurchase)
	}
	var ok bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM suppliers WHERE tenant_id = $1 AND id = $2)`, tenant, supplier).Scan(&ok); err != nil {
		return err
	}
	if !ok {
		return ErrNoSupplier
	}
	c.TrackMany = true
	if err := checkChoiceTx(ctx, tx, tenant, &c, name, 0, 1); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `INSERT INTO supplier_items (tenant_id, supplier_id, name_key, name, treatment, stock_item_id, uses, label_id, updated_by)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		ON CONFLICT (tenant_id, supplier_id, name_key) DO UPDATE SET name = EXCLUDED.name, treatment = EXCLUDED.treatment,
		  stock_item_id = EXCLUDED.stock_item_id, uses = EXCLUDED.uses, label_id = EXCLUDED.label_id, updated_by = EXCLUDED.updated_by, updated_at = now()`,
		tenant, supplier, NameKey(name), name, c.Treatment, c.StockItemID, c.Uses, c.LabelID, actor)
	if err != nil {
		return err
	}
	return stockAudit(ctx, tx, tenant, "supplier_item.set", name, map[string]any{"supplier_id": supplier, "treatment": c.Treatment, "by": actor})
}

// ClearSupplierItemTx forgets an item of a supplier not on Taskiem.
func ClearSupplierItemTx(ctx context.Context, tx pgx.Tx, tenant, supplier uuid.UUID, name, actor string) error {
	tag, err := tx.Exec(ctx, `DELETE FROM supplier_items WHERE tenant_id = $1 AND supplier_id = $2 AND name_key = $3`, tenant, supplier, NameKey(name))
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("%w: nothing is remembered of that item", ErrNoItem)
	}
	return stockAudit(ctx, tx, tenant, "supplier_item.removed", name, map[string]any{"supplier_id": supplier, "by": actor})
}

// memoryOfTx is what the business remembers of a line's item from a
// supplier: by the linked supplier's item when the line has one and the
// supplier is linked, else by its name.
func memoryOfTx(ctx context.Context, tx pgx.Tx, tenant, supplier uuid.UUID, link *uuid.UUID, l PurchaseLine) (LineChoice, bool, error) {
	var c LineChoice
	var err error
	if l.SellerItemID != "" && link != nil {
		err = tx.QueryRow(ctx, `SELECT treatment, stock_item_id, uses, label_id FROM item_links WHERE tenant_id = $1 AND link_id = $2 AND seller_item_id = $3`,
			tenant, *link, l.SellerItemID).Scan(&c.Treatment, &c.StockItemID, &c.Uses, &c.LabelID)
	} else {
		err = tx.QueryRow(ctx, `SELECT treatment, stock_item_id, uses, label_id FROM supplier_items WHERE tenant_id = $1 AND supplier_id = $2 AND name_key = $3`,
			tenant, supplier, NameKey(l.Item)).Scan(&c.Treatment, &c.StockItemID, &c.Uses, &c.LabelID)
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return c, false, nil
	}
	return c, err == nil, err
}

// rememberTx keeps a line's treatment as the memory of its item.
func rememberTx(ctx context.Context, tx pgx.Tx, tenant, supplier uuid.UUID, link *uuid.UUID, l PurchaseLine, actor string) error {
	c := l.choice()
	if l.SellerItemID != "" && link != nil {
		return SetItemLinkTx(ctx, tx, tenant, *link, l.SellerItemID, l.Item, c, actor)
	}
	return SetSupplierItemTx(ctx, tx, tenant, supplier, l.Item, c, actor)
}

// goSpaces is, as a regular expression class, every rune Go's
// unicode.IsSpace (so NameKey) takes for a space.
const goSpaces = `[\t\n\v\f\r \u0085   -     　]`

// mayBeKey is a SQL test, with $2 a NameKey, that col may have that key:
// every name whose NameKey it is passes (and some others, which the
// caller drops by NameKey).
func mayBeKey(col string) string {
	return fmt.Sprintf(`regexp_replace(lower(%s), '%s+', '', 'g') = regexp_replace(lower($2), '%s+', '', 'g')`, col, goSpaces, goSpaces)
}

// SuggestTx is Taskiem's guess at how to treat an item of a name the
// business has no memory of (§6), never a decision: a stock item of that
// name: add to it; the business has kept track of things of that name
// before: keep track of it; else just record it. The label is the one the
// business last gave a line of that name, if any.
func SuggestTx(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, name string) (LineChoice, error) {
	c := LineChoice{Treatment: Record, Uses: 1}
	key := NameKey(name)
	if key == "" {
		return c, nil
	}
	// Stock items and Things we have keep no key of their own: those whose
	// names may be it are found in SQL (spaces of any kind dropped, both
	// lowered there), and kept only when their NameKey is it.
	type named struct {
		ID   uuid.UUID
		Name string
	}
	byName := func(q string) ([]named, error) {
		rows, err := tx.Query(ctx, q, tenant, key)
		if err != nil {
			return nil, err
		}
		list, err := pgx.CollectRows(rows, pgx.RowToStructByPos[named])
		if err != nil {
			return nil, err
		}
		out := list[:0]
		for _, n := range list {
			if NameKey(n.Name) == key {
				out = append(out, n)
			}
		}
		return out, nil
	}
	items, err := byName(`SELECT id, name FROM stock_items WHERE tenant_id = $1 AND retired_at IS NULL AND ` + mayBeKey("name") + ` ORDER BY created_at LIMIT 20`)
	if err != nil {
		return c, err
	}
	var tracked bool
	if len(items) == 0 {
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM purchase_lines WHERE tenant_id = $1 AND treatment = 'track' AND name_key = $2)`, tenant, key).Scan(&tracked); err != nil {
			return c, err
		}
		if !tracked {
			kept, err := byName(`SELECT id, name FROM kept_things WHERE tenant_id = $1 AND ` + mayBeKey("name") + ` LIMIT 20`)
			if err != nil {
				return c, err
			}
			tracked = len(kept) > 0
		}
	}
	switch {
	case len(items) > 0:
		c.Treatment, c.StockItemID = AddToStock, &items[0].ID
	case tracked:
		c.Treatment = Track
	}
	err = tx.QueryRow(ctx, `SELECT label_id FROM purchase_lines WHERE tenant_id = $1 AND label_id IS NOT NULL AND name_key = $2
		ORDER BY created_at DESC LIMIT 1`, tenant, key).Scan(&c.LabelID)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return c, err
	}
	return c, nil
}

// treatLinesTx says how each line of a new order is treated: as said on
// it (checked, and remembered when the business had no memory of the
// item: the first time is the confirmation), else as remembered, else as
// suggested (to be confirmed). A remembered or suggested tracked line of
// more than MaxTracked is recorded, and a suggested one of more than
// TrackAsk too; a remembered one of more than TrackAsk asks first when
// the caller can ask (ask), else is recorded and left unconfirmed, for the
// business to say: whoever placed the order (a linked supplier) never
// learns how the business treats the item (§7).
func treatLinesTx(ctx context.Context, tx pgx.Tx, tenant, supplier uuid.UUID, link *uuid.UUID, lines []PurchaseLine, actor string, ask bool) error {
	for i := range lines {
		l := &lines[i]
		mem, known, err := memoryOfTx(ctx, tx, tenant, supplier, link, *l)
		if err != nil {
			return err
		}
		if l.Treatment != "" && l.Suggested && !known {
			// The caller's own guess, as a suggestion: not remembered.
			c := l.choice()
			if err := checkChoiceTx(ctx, tx, tenant, &c, l.Item, i, l.Quantity); err != nil {
				return err
			}
			l.take(c)
			continue
		}
		if l.Treatment != "" && !l.Suggested {
			c := l.choice()
			if err := checkChoiceTx(ctx, tx, tenant, &c, l.Item, i, l.Quantity); err != nil {
				return err
			}
			l.take(c)
			if !known {
				if err := rememberTx(ctx, tx, tenant, supplier, link, *l, actor); err != nil {
					return err
				}
			}
			continue
		}
		if !known {
			if mem, err = SuggestTx(ctx, tx, tenant, l.Item); err != nil {
				return err
			}
			if mem.Treatment == Track && l.Quantity > TrackAsk {
				mem.Treatment = Record
			}
		}
		if mem.Treatment == Track && l.Quantity > MaxTracked {
			mem.Treatment = Record
		}
		if known && !ask && mem.Treatment == Track && l.Quantity > TrackAsk && !l.TrackMany {
			mem.Treatment, l.toConfirm = Record, true
		}
		mem.TrackMany = l.TrackMany
		if err := checkChoiceTx(ctx, tx, tenant, &mem, l.Item, i, l.Quantity); err != nil {
			return err
		}
		l.take(mem)
	}
	return nil
}

// supplierLinkTx is the link an order's supplier is, if any: the order's
// own once sent, else the supplier's.
func supplierLinkTx(ctx context.Context, tx pgx.Tx, tenant, supplier uuid.UUID, order *uuid.UUID) (*uuid.UUID, error) {
	var link *uuid.UUID
	err := tx.QueryRow(ctx, `SELECT COALESCE((SELECT o.link_id FROM purchase_orders o WHERE o.tenant_id = $1 AND o.id = $3), s.link_id)
		FROM suppliers s WHERE s.tenant_id = $1 AND s.id = $2`, tenant, supplier, order).Scan(&link)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNoSupplier
	}
	return link, err
}

// SetLineTx changes how a line of an order is treated, until anything on
// it is received (§6), and confirms it: the business's memory of the item
// is kept as this the first time, or whenever remember is set. Otherwise
// only this order's line changes.
func SetLineTx(ctx context.Context, tx pgx.Tx, tenant, id uuid.UUID, position int, c LineChoice, remember bool, actor string) (Purchase, error) {
	x, err := PurchaseTx(ctx, tx, tenant, id, true)
	if err != nil {
		return x, err
	}
	if position < 0 || position >= len(x.Lines) {
		return x, fmt.Errorf("%w: %s has no line %d", ErrPurchase, x.Reference, position)
	}
	l := x.Lines[position]
	if l.Received > 0 {
		return x, fmt.Errorf("%w: something of %s was received on %s; change the stock by a count, or the row on Things we have", ErrLineLocked, l.Item, x.Reference)
	}
	if x.Status == Cancelled || x.Status == FullReceived {
		return x, fmt.Errorf("%w: %s is %s", ErrPurchase, x.Reference, StatusWords[x.Status])
	}
	if err := checkChoiceTx(ctx, tx, tenant, &c, l.Item, position, l.Quantity); err != nil {
		return x, err
	}
	l.take(c)
	for _, o := range x.Lines {
		if o.ID != l.ID && o.SellerItemID == "" && l.SellerItemID == "" && l.Treatment == AddToStock && o.Treatment == AddToStock && *o.StockItemID == *l.StockItemID {
			return x, fmt.Errorf("%w: %s goes into that stock item already on %s", ErrPurchase, o.Item, x.Reference)
		}
	}
	if _, err := tx.Exec(ctx, `UPDATE purchase_lines SET treatment = $3, stock_item_id = $4, uses = $5, label_id = $6, to_confirm = false WHERE tenant_id = $1 AND id = $2`,
		tenant, l.ID, l.Treatment, l.StockItemID, l.Uses, l.LabelID); err != nil {
		return x, err
	}
	if remember || !l.known {
		link, err := supplierLinkTx(ctx, tx, tenant, x.SupplierID, &x.ID)
		if err != nil {
			return x, err
		}
		if err := rememberTx(ctx, tx, tenant, x.SupplierID, link, l, actor); err != nil {
			return x, err
		}
	}
	if err := stockAudit(ctx, tx, tenant, "purchase.line_set", x.Reference, map[string]any{"purchase_id": id, "position": position, "treatment": l.Treatment,
		"remember": remember, "by": actor}); err != nil {
		return x, err
	}
	return PurchaseTx(ctx, tx, tenant, id, false)
}

// ConfirmLinesTx says how the lines of an order are treated, one choice
// per line (nil: as it is), each as SetLineTx with remember off, and
// confirms every line the business had no memory of: its treatment, said
// or suggested, is remembered. Lines received on already are left.
func ConfirmLinesTx(ctx context.Context, tx pgx.Tx, tenant, id uuid.UUID, choices []*LineChoice, actor string) (Purchase, error) {
	x, err := PurchaseTx(ctx, tx, tenant, id, true)
	if err != nil {
		return x, err
	}
	if choices != nil && len(choices) != len(x.Lines) {
		return x, fmt.Errorf("%w: one answer for each of the %d lines", ErrPurchase, len(x.Lines))
	}
	for i, l := range x.Lines {
		if l.Received > 0 {
			continue
		}
		c := l.choice()
		if choices != nil && choices[i] != nil {
			c = *choices[i]
		} else if !l.Unconfirmed {
			continue
		} else {
			c.TrackMany = true // as it was drafted: checked then
		}
		if x, err = SetLineTx(ctx, tx, tenant, id, i, c, false, actor); err != nil {
			return x, err
		}
	}
	return PurchaseTx(ctx, tx, tenant, id, false)
}

// lineFor is the line of an order a receipt's key names: its ItemID or
// its own id; else, for a stock line, a product counted by its stock item.
func lineFor(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, lines []PurchaseLine, key string) (int, error) {
	for i, l := range lines {
		if l.ItemID == key || l.ID.String() == key {
			return i, nil
		}
	}
	c, ok, err := productstock.ItemOfTx(ctx, tx, tenant, key)
	if err != nil || !ok {
		return -1, err
	}
	for i, l := range lines {
		if l.StockItemID != nil && *l.StockItemID == c.StockItemID {
			return i, nil
		}
	}
	return -1, nil
}

// OnOrderTx is the stock items on an open order (drafted, sent, confirmed
// or part received), by id: what the low list need not order again.
func OnOrderTx(ctx context.Context, tx pgx.Tx, tenant uuid.UUID) (map[string]bool, error) {
	rows, err := tx.Query(ctx, `SELECT DISTINCT l.stock_item_id::text FROM purchase_lines l JOIN purchase_orders o ON o.tenant_id = l.tenant_id AND o.id = l.purchase_order_id
		WHERE l.tenant_id = $1 AND l.stock_item_id IS NOT NULL AND o.status IN ('drafted', 'sent', 'confirmed', 'part_received')`, tenant)
	if err != nil {
		return nil, err
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[string])
	out := make(map[string]bool, len(ids))
	for _, id := range ids {
		out[id] = true
	}
	return out, err
}

// OnOrder reports whether an item (a product or a material) is on an open
// order: its stock item is (OnOrderTx).
func (it Item) OnOrder(on map[string]bool) bool {
	return it.StockItemID != "" && on[it.StockItemID] || on[it.ID]
}
