package stock

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/israel-duff/taskiem/engine/productstock"
	"github.com/israel-duff/taskiem/engine/storefront"
	"github.com/israel-duff/taskiem/engine/variants"
)

// MaxItems is how many current stock items a business may have (§10 Q9),
// apart from the catalogue's 500.
const MaxItems = 5000

// ErrTooMany: the business has MaxItems stock items.
var ErrTooMany = fmt.Errorf("%w: a business keeps at most %d stock items", ErrInvalid, MaxItems)

// StockItem is something the business counts (stock_items): its count is
// the sum of its movements.
type StockItem struct {
	ID       uuid.UUID `json:"id"`
	Name     string    `json:"name"`
	Unit     string    `json:"unit,omitempty"`
	Category string    `json:"category,omitempty"`
	Count    int64     `json:"count"`
	CostKobo *int64    `json:"cost_kobo,omitempty"`
	// Low is the level at or below which it is low (the reorder level).
	Low    *int64 `json:"low_level,omitempty"`
	Target *int64 `json:"target_level,omitempty"`
	// SupplierID is its usual supplier; Supplier their name.
	SupplierID *uuid.UUID `json:"supplier_id,omitempty"`
	Supplier   string     `json:"supplier,omitempty"`
	// GroupOf is the product a size's or colour's stock item belongs to.
	GroupOf string `json:"group_of,omitempty"`
	// Code is its barcode, for Scan to count (device fields D2); "" for
	// none. One current stock item of a business has a code.
	Code      string     `json:"code,omitempty"`
	CreatedBy string     `json:"created_by"`
	CreatedAt time.Time  `json:"created_at"`
	UpdatedAt time.Time  `json:"updated_at"`
	RetiredAt *time.Time `json:"retired_at,omitempty"`
}

const itemCols = `i.id, i.name, i.unit, i.category, i.count, i.cost_kobo, i.low_level, i.target_level, i.supplier_id, COALESCE(s.name, ''),
	COALESCE(i.group_of, ''), COALESCE(i.code, ''), i.created_by, i.created_at, i.updated_at, i.retired_at`

const itemFrom = ` FROM stock_items i LEFT JOIN suppliers s ON s.tenant_id = i.tenant_id AND s.id = i.supplier_id`

func scanItem(r pgx.Row) (StockItem, error) {
	var x StockItem
	err := r.Scan(&x.ID, &x.Name, &x.Unit, &x.Category, &x.Count, &x.CostKobo, &x.Low, &x.Target, &x.SupplierID, &x.Supplier,
		&x.GroupOf, &x.Code, &x.CreatedBy, &x.CreatedAt, &x.UpdatedAt, &x.RetiredAt)
	return x, err
}

// StockItemsTx lists the business's stock items by name; retired ones too
// when retired is true.
func StockItemsTx(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, retired bool) ([]StockItem, error) {
	rows, err := tx.Query(ctx, `SELECT `+itemCols+itemFrom+` WHERE i.tenant_id = $1 AND ($2 OR i.retired_at IS NULL) ORDER BY lower(i.name), i.id`, tenant, retired)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (StockItem, error) { return scanItem(r) })
}

// StockItemTx reads one stock item; ErrNoItem when there is none.
func StockItemTx(ctx context.Context, tx pgx.Tx, tenant, id uuid.UUID) (StockItem, error) {
	x, err := scanItem(tx.QueryRow(ctx, `SELECT `+itemCols+itemFrom+` WHERE i.tenant_id = $1 AND i.id = $2`, tenant, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return x, ErrNoItem
	}
	return x, err
}

// NewItem is a stock item to make, at 0.
type NewItem struct {
	Name       string
	Unit       string
	Category   string
	CostKobo   *int64
	Low        *int64
	Target     *int64
	SupplierID *uuid.UUID
	GroupOf    string
	Actor      string
}

// CreateItemTx makes a stock item, counted at 0: its count comes from its
// movements.
func CreateItemTx(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, in NewItem) (StockItem, error) {
	in.Name = cut(strings.Join(strings.Fields(in.Name), " "), 200)
	if in.Name == "" {
		return StockItem{}, fmt.Errorf("%w: a stock item has a name", ErrInvalid)
	}
	actor := in.Actor
	if actor == "" {
		actor = "taskiem"
	}
	id := uuid.Must(uuid.NewV7())
	_, err := tx.Exec(ctx, `INSERT INTO stock_items (id, tenant_id, name, unit, category, cost_kobo, low_level, target_level, supplier_id, group_of, created_by)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, NULLIF($10, ''), $11)`,
		id, tenant, in.Name, cut(strings.TrimSpace(in.Unit), 30), cut(strings.TrimSpace(in.Category), 60), in.CostKobo, in.Low, in.Target, in.SupplierID,
		in.GroupOf, cut(actor, 200))
	var pe *pgconn.PgError
	if errors.As(err, &pe) && pe.Code == "23514" && strings.Contains(pe.Message, "5,000") {
		return StockItem{}, ErrTooMany
	}
	if err != nil {
		return StockItem{}, err
	}
	return StockItemTx(ctx, tx, tenant, id)
}

// ItemSet is what to change of a stock item; nil leaves a field as it is.
// Its count changes by movements only.
type ItemSet struct {
	Name     *string
	Unit     *string
	Category *string
	CostKobo *int64
	Low      *int64
	Target   *int64
	// Supplier is set by SupplierID; ClearSupplier removes it.
	SupplierID    *uuid.UUID
	ClearSupplier bool
	// GroupOf is the product a size's or colour's stock item belongs to.
	GroupOf *string
}

// SetItemTx changes a stock item's details.
func SetItemTx(ctx context.Context, tx pgx.Tx, tenant, id uuid.UUID, set ItemSet) (StockItem, error) {
	x, err := StockItemTx(ctx, tx, tenant, id)
	if err != nil {
		return x, err
	}
	if set.Name != nil {
		if n := cut(strings.Join(strings.Fields(*set.Name), " "), 200); n != "" {
			x.Name = n
		}
	}
	if set.Unit != nil {
		x.Unit = cut(strings.TrimSpace(*set.Unit), 30)
	}
	if set.Category != nil {
		x.Category = cut(strings.TrimSpace(*set.Category), 60)
	}
	if set.CostKobo != nil {
		x.CostKobo = set.CostKobo
	}
	if set.Low != nil {
		x.Low = set.Low
	}
	if set.Target != nil {
		x.Target = set.Target
	}
	if set.SupplierID != nil {
		x.SupplierID = set.SupplierID
	}
	if set.GroupOf != nil {
		x.GroupOf = *set.GroupOf
	}
	if set.ClearSupplier {
		x.SupplierID = nil
	}
	if _, err := tx.Exec(ctx, `UPDATE stock_items SET name = $3, unit = $4, category = $5, cost_kobo = $6, low_level = $7, target_level = $8, supplier_id = $9,
		group_of = NULLIF($10, '')
		WHERE tenant_id = $1 AND id = $2`, tenant, id, x.Name, x.Unit, x.Category, x.CostKobo, x.Low, x.Target, x.SupplierID, x.GroupOf); err != nil {
		return x, err
	}
	return StockItemTx(ctx, tx, tenant, id)
}

// MaxCode is the longest barcode a stock item keeps.
const MaxCode = 64

// CodeTaken is SetCodeTx's refusal of a code another current stock item
// has: Name is that item's.
type CodeTaken struct {
	ID   uuid.UUID
	Name string
}

func (e *CodeTaken) Error() string { return "the code is " + e.Name + "'s already" }

// SetCodeTx sets a stock item's barcode (Scan to count, device fields
// D2), trimmed; "" clears it. A code another current stock item of the
// business has, in any case, is refused with *CodeTaken; one longer than
// MaxCode or with control characters with ErrInvalid.
func SetCodeTx(ctx context.Context, tx pgx.Tx, tenant, id uuid.UUID, code string) (StockItem, error) {
	code = strings.TrimSpace(code)
	if len([]rune(code)) > MaxCode || strings.IndexFunc(code, unicode.IsControl) >= 0 {
		return StockItem{}, fmt.Errorf("%w: a barcode is up to %d characters, letters and digits", ErrInvalid, MaxCode)
	}
	x, err := StockItemTx(ctx, tx, tenant, id)
	if err != nil {
		return x, err
	}
	if code != "" && x.RetiredAt == nil {
		var other CodeTaken
		err := tx.QueryRow(ctx, `SELECT id, name FROM stock_items WHERE tenant_id = $1 AND id <> $2 AND retired_at IS NULL AND lower(code) = lower($3)`,
			tenant, id, code).Scan(&other.ID, &other.Name)
		if err == nil {
			return x, &other
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return x, err
		}
	}
	_, err = tx.Exec(ctx, `UPDATE stock_items SET code = NULLIF($3, '') WHERE tenant_id = $1 AND id = $2`, tenant, id, code)
	var pe *pgconn.PgError
	if errors.As(err, &pe) && pe.Code == "23505" && pe.ConstraintName == "stock_items_code" {
		// Taken at the same moment by another.
		return x, &CodeTaken{Name: "another item"}
	}
	if err != nil {
		return x, err
	}
	return StockItemTx(ctx, tx, tenant, id)
}

// RetireItemTx retires a stock item (a size or colour taken off sale): it
// keeps its count and movements, and is offered nowhere. back brings a
// retired one back.
func RetireItemTx(ctx context.Context, tx pgx.Tx, tenant, id uuid.UUID, back bool) error {
	q := `UPDATE stock_items SET retired_at = now() WHERE tenant_id = $1 AND id = $2 AND retired_at IS NULL`
	if back {
		// Brought back, it is current again: within MaxItems, counted
		// under the business's lock that making one takes (migration
		// 00501's trigger).
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('stock_items ' || $1::text, 0))`, tenant); err != nil {
			return err
		}
		var retired bool
		var current int
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM stock_items WHERE tenant_id = $1 AND id = $2 AND retired_at IS NOT NULL),
			(SELECT count(*) FROM stock_items WHERE tenant_id = $1 AND retired_at IS NULL)`, tenant, id).Scan(&retired, &current); err != nil {
			return err
		}
		if retired && current >= MaxItems {
			return ErrTooMany
		}
		// Its barcode, if a current item has taken it meanwhile, stays
		// with that one: one current item has a code.
		q = `UPDATE stock_items i SET retired_at = NULL,
			code = CASE WHEN EXISTS (SELECT 1 FROM stock_items o WHERE o.tenant_id = i.tenant_id AND o.retired_at IS NULL AND lower(o.code) = lower(i.code))
				THEN NULL ELSE i.code END
			WHERE i.tenant_id = $1 AND i.id = $2 AND i.retired_at IS NOT NULL`
	}
	_, err := tx.Exec(ctx, q, tenant, id)
	return err
}

// Item is what the stock screens, chat and workflows list: a product of
// the catalogue (a row, by the mapping) with the stock item it is counted
// by, if any; or a material, a stock item no product is counted by. ID is
// the product's row id, or a material's stock item id; StockItemID is the
// stock item, "" for a product not counted. Both kinds of ID move stock
// (Move.ItemID).
type Item struct {
	ID          string `json:"id"`
	StockItemID string `json:"stock_item_id,omitempty"`
	// Material: a stock item that no product is counted by.
	Material  bool   `json:"material,omitempty"`
	Name      string `json:"name"`
	Available bool   `json:"available"`
	// Stock is nil when it is not counted: its stock item's count, in the
	// stock item's unit.
	Stock *int64 `json:"stock"`
	// Uses is, on a product counted by a stock item, how many of it one
	// sale takes (product_stock.uses): a "pack of 6" uses 6.
	Uses      int64  `json:"uses,omitempty"`
	Unit      string `json:"unit,omitempty"`
	PriceKobo *int64 `json:"price_kobo,omitempty"`
	// CostKobo is the last price paid for one unit.
	CostKobo   *int64     `json:"cost_kobo,omitempty"`
	Reorder    *int64     `json:"reorder_level,omitempty"`
	Target     *int64     `json:"target_level,omitempty"`
	Supplier   string     `json:"supplier,omitempty"`
	SupplierID *uuid.UUID `json:"supplier_id,omitempty"`
	// Code is its stock item's barcode (Scan to count), "" for none.
	Code string `json:"code,omitempty"`
	// Category is the owner's label for the kind of thing it is
	// ("Sneakers"): the catalogue's for a product, the stock item's for a
	// material.
	Category string `json:"category,omitempty"`
	// Description is the item's words on the storefront, when mapped:
	// search reads it.
	Description string `json:"description,omitempty"`
	// Sizes and colours (engine/variants): a variant's product row and its
	// values; a product's choices. A product with variants keeps no stock
	// of its own: its count is theirs together.
	VariantOf string            `json:"variant_of,omitempty"`
	Variant   map[string]string `json:"variant,omitempty"`
	Choices   []variants.Choice `json:"choices,omitempty"`
	// OwnPrice is, on a product, the Keys of its sizes and colours with a
	// price of their own; the others follow the product's.
	OwnPrice []string `json:"own_price,omitempty"`
	// PhotoID is the first entry of its photo column (a media id), when
	// the catalogue has one: a colour's photo on a size is read from it.
	PhotoID string `json:"-"`
}

// HasSizes reports whether the item is a product sold in sizes and
// colours: it keeps no stock of its own, and is sold, counted and
// received through them.
func (it Item) HasSizes() bool { return len(it.Choices) > 0 }

// SaleUnit is the unit a sale of the item is said in: its stock item's
// when one sale uses one of it, else none (the product is sold whole: "2
// Pack of 6").
func (it Item) SaleUnit() string {
	if it.Uses > 1 {
		return ""
	}
	return it.Unit
}

// SaleCostKobo is what one of the item costs at its stock item's cost
// price: the cost times its uses (a pack of 6 costs six bottles); nil when
// the stock item has no cost.
func (it Item) SaleCostKobo() *int64 {
	if it.CostKobo == nil {
		return nil
	}
	c := *it.CostKobo * max(it.Uses, 1)
	return &c
}

// Beyond reports whether selling n of the item takes more than its stock
// item has: n times its uses above the count. Such a sale is recorded all
// the same, with what it was short.
func (it Item) Beyond(n int64) bool {
	return it.Stock != nil && *it.Stock < n*max(it.Uses, 1)
}

// Retired reports whether the item is a size or colour taken off sale: it
// keeps its history, and is offered nowhere.
func (it Item) Retired() bool { return it.VariantOf != "" && !it.Available }

// Low reports whether the item is at or below its reorder level. A
// product with sizes and colours is never low itself: it keeps no stock of
// its own, and each of them is low or not.
func (it Item) Low() bool {
	return !it.HasSizes() && !it.Retired() && it.Stock != nil && it.Reorder != nil && *it.Stock <= *it.Reorder
}

// Suggest is how many to order of a low item: up to its target level,
// else back to twice its reorder level; 0 when it is not low.
func (it Item) Suggest() int64 {
	if !it.Low() {
		return 0
	}
	if it.Target != nil && *it.Target > *it.Stock {
		return *it.Target - *it.Stock
	}
	return max(1, 2**it.Reorder-*it.Stock)
}

// UnitName is the item's unit for n of them, plain: "bag", "bags"; ""
// when it has none.
func (it Item) UnitName(n int64) string {
	u := strings.TrimSpace(it.Unit)
	if u == "" || n == 1 || n == -1 {
		return u
	}
	return Plural(u)
}

// measures are units written as symbols, which stay the same for more
// than one: 1000 g, 2 kg, 5 l, never "gs".
var measures = map[string]bool{
	"g": true, "kg": true, "mg": true, "l": true, "ml": true, "cl": true,
	"m": true, "cm": true, "mm": true, "km": true, "oz": true, "lb": true,
	"ft": true, "in": true, "yd": true,
}

// Plural is a unit word for more than one: bag, bags; box, boxes; a
// measure's symbol stays as it is (g, kg, ml).
func Plural(u string) string {
	l := strings.ToLower(u)
	if measures[l] {
		return u
	}
	switch {
	case strings.HasSuffix(l, "s"), strings.HasSuffix(l, "x"), strings.HasSuffix(l, "ch"), strings.HasSuffix(l, "sh"):
		if strings.HasSuffix(l, "s") && !strings.HasSuffix(l, "ss") {
			return u
		}
		return u + "es"
	case strings.HasSuffix(l, "y") && len(l) > 1 && !strings.ContainsRune("aeiou", rune(l[len(l)-2])):
		return u[:len(u)-1] + "ies"
	}
	return u + "s"
}

// Quantity says n of an item: "12 bags", "1 carton", "4".
func (it Item) Quantity(n int64) string {
	if u := it.UnitName(n); u != "" {
		return strconv.FormatInt(n, 10) + " " + u
	}
	return strconv.FormatInt(n, 10)
}

// withStock puts a stock item's count and details on an item.
func (it *Item) withStock(x StockItem) {
	n := x.Count
	it.StockItemID, it.Stock, it.Unit = x.ID.String(), &n, x.Unit
	it.CostKobo, it.Reorder, it.Target = x.CostKobo, x.Low, x.Target
	it.Supplier, it.SupplierID = x.Supplier, x.SupplierID
	it.Code = x.Code
}

// FromStockItem is a material as an item.
func FromStockItem(x StockItem) Item {
	it := Item{ID: x.ID.String(), Material: true, Name: x.Name, Available: x.RetiredAt == nil, Category: x.Category}
	it.withStock(x)
	return it
}

// Items maps a catalogue table's rows to items by the mapping, each with
// the stock item it is counted by (links, by row id), and adds the
// materials: the stock items of stock no row is counted by (retired ones
// left out). Rows without a name are left out. By name.
func Items(rows []map[string]any, m storefront.Columns, stock []StockItem, links map[string]productstock.Counted) []Item {
	var out []Item
	byID := make(map[uuid.UUID]StockItem, len(stock))
	for _, x := range stock {
		byID[x.ID] = x
	}
	counting := map[uuid.UUID]bool{}
	for _, r := range rows {
		it := Item{ID: fmt.Sprint(r["id"]), Name: str(r[m.Name]), Available: true}
		if it.Name == "" || it.ID == "" || it.ID == "<nil>" {
			continue
		}
		if m.Available != "" {
			if b, ok := r[m.Available].(bool); ok {
				it.Available = b
			}
		}
		if m.Category != "" {
			it.Category = str(r[m.Category])
		}
		if m.Description != "" {
			it.Description = str(r[m.Description])
		}
		if m.VariantOf != "" {
			it.VariantOf = str(r[m.VariantOf])
		}
		if m.Variant != "" {
			it.Variant = variants.ParseVariant(str(r[m.Variant]))
		}
		if m.Choices != "" {
			st := variants.ParseSetup(str(r[m.Choices]))
			it.Choices, it.OwnPrice = st.Choices, st.OwnPrice
		}
		if m.Photo != "" {
			it.PhotoID = strings.TrimSpace(strings.Split(str(r[m.Photo]), ",")[0])
		}
		it.PriceKobo = money(r, m.Price, m.PriceUnit)
		if c, ok := links[it.ID]; ok {
			if x, ok := byID[c.StockItemID]; ok {
				it.withStock(x)
				it.Uses = c.Uses
				counting[x.ID] = true
			}
		}
		out = append(out, it)
	}
	for _, x := range stock {
		if !counting[x.ID] && x.RetiredAt == nil {
			out = append(out, FromStockItem(x))
		}
	}
	sort.SliceStable(out, func(a, b int) bool { return strings.ToLower(out[a].Name) < strings.ToLower(out[b].Name) })
	return out
}

// ItemsTx is Items, reading the business's stock items and links in tx.
func ItemsTx(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, rows []map[string]any, m storefront.Columns) ([]Item, error) {
	stock, err := StockItemsTx(ctx, tx, tenant, true)
	if err != nil {
		return nil, err
	}
	links, err := productstock.CountsTx(ctx, tx, tenant, nil)
	if err != nil {
		return nil, err
	}
	return Items(rows, m, stock, links), nil
}

// Low is the items at or below their reorder level, the lowest first
// against it.
func Low(items []Item) []Item {
	var out []Item
	for _, it := range items {
		if it.Low() {
			out = append(out, it)
		}
	}
	sort.SliceStable(out, func(a, b int) bool {
		return *out[a].Stock-*out[a].Reorder < *out[b].Stock-*out[b].Reorder
	})
	return out
}

// ValueKobo is what the items counted are worth at their cost prices
// (stock value): each stock item once, none below zero.
func ValueKobo(items []Item) int64 {
	var v int64
	seen := map[string]bool{}
	for _, it := range items {
		if it.StockItemID == "" || seen[it.StockItemID] || it.Stock == nil || it.CostKobo == nil || *it.Stock <= 0 {
			continue
		}
		seen[it.StockItemID] = true
		v = AddKobo(v, MulKobo(*it.Stock, *it.CostKobo))
	}
	return v
}

// MulKobo is a count at a cost, both at least 0, held at the largest
// int64 rather than overflowing: a count and a cost each up to a
// trillion multiply past it.
func MulKobo(count, cost int64) int64 {
	if count <= 0 || cost <= 0 {
		return 0
	}
	if count > math.MaxInt64/cost {
		return math.MaxInt64
	}
	return count * cost
}

// AddKobo adds two values of at least 0, held at the largest int64.
func AddKobo(a, b int64) int64 {
	if a > math.MaxInt64-b {
		return math.MaxInt64
	}
	return a + b
}

func money(r map[string]any, col, unit string) *int64 {
	if col == "" {
		return nil
	}
	f, ok := num(r[col])
	if !ok || f < 0 || f > 1e12 {
		return nil
	}
	if unit != "kobo" {
		f *= 100
	}
	k := int64(math.Round(f))
	return &k
}

func str(v any) string {
	switch x := v.(type) {
	case string:
		return strings.TrimSpace(x)
	case nil:
		return ""
	}
	return strings.TrimSpace(fmt.Sprint(v))
}

func num(v any) (float64, bool) {
	switch x := v.(type) {
	case float64:
		return x, true
	case int64:
		return float64(x), true
	case int:
		return float64(x), true
	case json.Number:
		f, err := x.Float64()
		return f, err == nil
	case string:
		f, err := strconv.ParseFloat(strings.TrimSpace(x), 64)
		return f, err == nil
	}
	return 0, false
}
