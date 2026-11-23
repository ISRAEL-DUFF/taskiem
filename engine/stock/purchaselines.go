package stock

import (
	"strings"
	"time"

	"github.com/google/uuid"
)

// The rows of purchases on the server, U1 (docs/spec/what-a-business-buys.md
// §5, §6, §8; migration 00503). Declared here for the engine and the API to
// build on; the order's lines are read and written through PurchaseLineRow.

// Treatment is what a purchase line does when it arrives (§5).
type Treatment string

// The treatments.
const (
	// Record: nothing moves; the receipt and the expense only (the default).
	Record Treatment = "record"
	// AddToStock: the line's stock item is counted in, at the line's cost.
	AddToStock Treatment = "stock"
	// Track: each unit becomes a row of Things we have (KeptThing).
	Track Treatment = "track"
)

// Valid reports whether t is one of the treatments.
func (t Treatment) Valid() bool { return t == Record || t == AddToStock || t == Track }

// A tracked line of more than TrackAsk asks first; one of more than
// MaxTracked is not tracked one by one (§5.3; a CHECK of purchase_lines).
const (
	TrackAsk   = 50
	MaxTracked = 200
)

// PurchaseLineRow is a row of purchase_lines: one line of an order.
type PurchaseLineRow struct {
	ID              uuid.UUID `json:"id"`
	PurchaseOrderID uuid.UUID `json:"purchase_order_id"`
	// Position is the line's place on the order, 0 to 49; the order's
	// change and short (JSON) are by it.
	Position int    `json:"position"`
	Name     string `json:"name"`
	Unit     string `json:"unit,omitempty"`
	Quantity int64  `json:"quantity"`
	// CostKobo is the price of one.
	CostKobo int64 `json:"cost_kobo"`
	Received int64 `json:"received"`
	// Delivered is what a linked supplier says it delivered, mirrored from
	// its order; nil otherwise.
	Delivered *int64    `json:"delivered,omitempty"`
	Treatment Treatment `json:"treatment"`
	// StockItemID is set exactly for a stock line: the stock item receiving
	// moves by the quantity times Uses, at the cost over Uses. Uses is 1
	// for any other line.
	StockItemID *uuid.UUID `json:"stock_item_id,omitempty"`
	Uses        int64      `json:"uses"`
	LabelID     *uuid.UUID `json:"label_id,omitempty"`
	// SellerItemID is the linked supplier's own item, on an order to one.
	SellerItemID *string   `json:"seller_item_id,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// SpendLabel is a row of spend_labels: one of the business's own labels.
// A merged label names the label kept (never one itself merged); reports
// follow it when they are read.
type SpendLabel struct {
	ID         uuid.UUID  `json:"id"`
	Name       string     `json:"name"`
	MergedInto *uuid.UUID `json:"merged_into,omitempty"`
	CreatedBy  string     `json:"created_by"`
	CreatedAt  time.Time  `json:"created_at"`
}

// MaxLabel is the longest a label's name is, in characters.
const MaxLabel = 60

// ItemMemory is what the buyer remembers of a supplier's item (§6): a row
// of item_links (a linked supplier's item, by SellerItemID) or of
// supplier_items (a supplier not on Taskiem, by NameKey).
type ItemMemory struct {
	// LinkID and SellerItemID for item_links; SupplierID and NameKey for
	// supplier_items.
	LinkID       *uuid.UUID `json:"link_id,omitempty"`
	SellerItemID string     `json:"seller_item_id,omitempty"`
	SupplierID   *uuid.UUID `json:"supplier_id,omitempty"`
	NameKey      string     `json:"name_key,omitempty"`
	// Name is the item's name as last seen: the supplier's (seller_item)
	// or as typed (name).
	Name        string     `json:"name"`
	Treatment   Treatment  `json:"treatment"`
	StockItemID *uuid.UUID `json:"stock_item_id,omitempty"`
	Uses        int64      `json:"uses"`
	LabelID     *uuid.UUID `json:"label_id,omitempty"`
	UpdatedBy   string     `json:"updated_by"`
	UpdatedAt   time.Time  `json:"updated_at"`
}

// NameKey is how supplier_items matches an item's name: lowered, spaces
// trimmed and single, so "Diesel" and "diesel " are one item. The table
// holds only keys made so (a CHECK). Go lowers letter for letter ("İ" is
// "i", not the two letters a full lowering makes), so a name of 200 keys
// in 200; the key is cut to the 200 the CHECK allows all the same, never
// ending in a space.
func NameKey(name string) string {
	return strings.TrimRight(cut(strings.ToLower(strings.Join(strings.Fields(name), " ")), 200), " ")
}

// KeptStatus is where a row of Things we have stands.
type KeptStatus string

// The statuses of a row of Things we have.
const (
	KeptHave   KeptStatus = "have"
	KeptSold   KeptStatus = "sold"
	KeptBroken KeptStatus = "broken"
	KeptGiven  KeptStatus = "given"
	KeptGone   KeptStatus = "gone"
)

// KeptThing is a row of kept_things: one thing on Things we have.
type KeptThing struct {
	ID      uuid.UUID  `json:"id"`
	Name    string     `json:"name"`
	LabelID *uuid.UUID `json:"label_id,omitempty"`
	// From whom, and the order and line it came on; none when added by
	// hand. UnitNo is which of the line's units it is, from 1: receiving
	// writes each once (unique on the line and the number).
	SupplierID      *uuid.UUID `json:"supplier_id,omitempty"`
	PurchaseOrderID *uuid.UUID `json:"purchase_order_id,omitempty"`
	PurchaseLineID  *uuid.UUID `json:"purchase_line_id,omitempty"`
	UnitNo          *int       `json:"unit_no,omitempty"`
	CostKobo        *int64     `json:"cost_kobo,omitempty"`
	ReceivedAt      *time.Time `json:"received_at,omitempty"`
	Serial          string     `json:"serial,omitempty"`
	// WarrantyUntil is a date, YYYY-MM-DD.
	WarrantyUntil *string    `json:"warranty_until,omitempty"`
	Note          string     `json:"note,omitempty"`
	Status        KeptStatus `json:"status"`
	StatusAt      time.Time  `json:"status_at"`
	// SaleEntryID is the money in when it was sold with a price.
	SaleEntryID *uuid.UUID `json:"sale_entry_id,omitempty"`
	CreatedBy   string     `json:"created_by"`
	CreatedAt   time.Time  `json:"created_at"`
}
