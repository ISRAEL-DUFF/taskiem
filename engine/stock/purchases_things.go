package stock

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/moneylog"
)

// Things we have (what a business buys §5.3): the business's list of what
// it keeps track of, one row per thing received on a tracked line
// (ReceiveTx) or added by hand. A row marked sold, broken, given away or
// gone stays in the history and leaves the totals. A sale with a price is
// money in, labelled SaleCategory.

// SaleCategory is the money log's category of a thing we had, sold.
const SaleCategory = "Sale of things we had"

// ErrNoThing: no such row on Things we have.
var ErrNoThing = errors.New("stock: no such thing on Things we have")

// Thing is a row of Things we have, with the names of its label, supplier
// and order.
type Thing struct {
	KeptThing
	Label    string `json:"label,omitempty"`
	Supplier string `json:"supplier,omitempty"`
	Order    string `json:"order_reference,omitempty"`
}

const thingCols = `t.id, t.name, t.label_id, t.supplier_id, t.purchase_order_id, t.purchase_line_id, t.unit_no, t.cost_kobo, t.received_at, COALESCE(t.serial, ''),
	to_char(t.warranty_until, 'YYYY-MM-DD'), COALESCE(t.note, ''), t.status, t.status_at, t.sale_entry_id, t.created_by, t.created_at,
	COALESCE((SELECT COALESCE(k.name, b.name) FROM spend_labels b LEFT JOIN spend_labels k ON k.tenant_id = b.tenant_id AND k.id = b.merged_into
	  WHERE b.tenant_id = t.tenant_id AND b.id = t.label_id), ''),
	COALESCE((SELECT s.name FROM suppliers s WHERE s.tenant_id = t.tenant_id AND s.id = t.supplier_id), ''),
	COALESCE((SELECT o.reference FROM purchase_orders o WHERE o.tenant_id = t.tenant_id AND o.id = t.purchase_order_id), '')`

func scanThing(r pgx.CollectableRow) (Thing, error) {
	var t Thing
	err := r.Scan(&t.ID, &t.Name, &t.LabelID, &t.SupplierID, &t.PurchaseOrderID, &t.PurchaseLineID, &t.UnitNo, &t.CostKobo, &t.ReceivedAt, &t.Serial,
		&t.WarrantyUntil, &t.Note, &t.Status, &t.StatusAt, &t.SaleEntryID, &t.CreatedBy, &t.CreatedAt, &t.Label, &t.Supplier, &t.Order)
	return t, err
}

// MaxThings is the most rows ThingsTx lists; ThingTotalsTx counts them all.
const MaxThings = 2000

// ThingFilter narrows Things we have: All includes those no longer had
// (sold, broken, given away, gone); Order and Line keep the rows that came
// on that order or line.
type ThingFilter struct {
	All   bool
	Order *uuid.UUID
	Line  *uuid.UUID
}

const thingWhere = `t.tenant_id = $1 AND ($2 OR t.status = 'have') AND ($3::uuid IS NULL OR t.purchase_order_id = $3) AND ($4::uuid IS NULL OR t.purchase_line_id = $4)`

// ThingsTx lists Things we have, the newest first, up to MaxThings.
func ThingsTx(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, f ThingFilter) ([]Thing, error) {
	rows, err := tx.Query(ctx, `SELECT `+thingCols+` FROM kept_things t WHERE `+thingWhere+`
		ORDER BY COALESCE(t.received_at, t.created_at) DESC, t.id DESC LIMIT $5`, tenant, f.All, f.Order, f.Line, MaxThings)
	if err != nil {
		return nil, err
	}
	out, err := pgx.CollectRows(rows, scanThing)
	if out == nil {
		out = []Thing{}
	}
	return out, err
}

// ThingTotalsTx is the number and cost of the things had (status have)
// that the filter keeps, all of them, not only the rows ThingsTx lists.
func ThingTotalsTx(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, f ThingFilter) (int, int64, error) {
	var count int
	var total int64
	err := tx.QueryRow(ctx, `SELECT count(*)::int, COALESCE(sum(t.cost_kobo), 0)::bigint FROM kept_things t WHERE `+thingWhere+` AND t.status = 'have'`,
		tenant, f.All, f.Order, f.Line).Scan(&count, &total)
	return count, total, err
}

// ThingTx is one row.
func ThingTx(ctx context.Context, tx pgx.Tx, tenant, id uuid.UUID) (Thing, error) {
	rows, err := tx.Query(ctx, `SELECT `+thingCols+` FROM kept_things t WHERE t.tenant_id = $1 AND t.id = $2`, tenant, id)
	if err != nil {
		return Thing{}, err
	}
	t, err := pgx.CollectExactlyOneRow(rows, scanThing)
	if errors.Is(err, pgx.ErrNoRows) {
		return t, ErrNoThing
	}
	return t, err
}

// ThingGroup is the things had under one label: their number and cost.
// A merged label's are its kept label's.
type ThingGroup struct {
	LabelID   *uuid.UUID `json:"label_id"`
	Label     string     `json:"label"`
	Count     int        `json:"count"`
	TotalKobo int64      `json:"total_kobo"`
}

// ThingGroupsTx groups the things had (status have) that the filter keeps
// by label kept, all of them: the label with the most cost first; Not
// labelled last.
func ThingGroupsTx(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, f ThingFilter) ([]ThingGroup, error) {
	rows, err := tx.Query(ctx, `SELECT k.id, COALESCE(k.name, ''), count(*)::int, COALESCE(sum(t.cost_kobo), 0)::bigint
		FROM kept_things t
		LEFT JOIN spend_labels b ON b.tenant_id = t.tenant_id AND b.id = t.label_id
		LEFT JOIN spend_labels k ON k.tenant_id = t.tenant_id AND k.id = COALESCE(b.merged_into, b.id)
		WHERE `+thingWhere+` AND t.status = 'have'
		GROUP BY k.id, k.name ORDER BY k.id IS NULL, 4 DESC, 2, 1`, tenant, f.All, f.Order, f.Line)
	if err != nil {
		return nil, err
	}
	out, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (ThingGroup, error) {
		var g ThingGroup
		err := r.Scan(&g.LabelID, &g.Label, &g.Count, &g.TotalKobo)
		return g, err
	})
	if out == nil {
		out = []ThingGroup{}
	}
	return out, err
}

// NewThing is a row added by hand: anything bought before Taskiem or
// outside it ("Add something we have").
type NewThing struct {
	Name       string     `json:"name"`
	LabelID    *uuid.UUID `json:"label_id"`
	SupplierID *uuid.UUID `json:"supplier_id"`
	CostKobo   *int64     `json:"cost_kobo"`
	ReceivedAt *time.Time `json:"received_at"`
	Serial     string     `json:"serial"`
	// WarrantyUntil is a date, YYYY-MM-DD.
	WarrantyUntil string `json:"warranty_until"`
	Note          string `json:"note"`
}

func checkDate(s string) (*string, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, nil
	}
	if _, err := time.Parse("2006-01-02", s); err != nil {
		return nil, fmt.Errorf("%w: a warranty's end is a date, like 2027-06-30", ErrPurchase)
	}
	return &s, nil
}

func checkThingText(serial, note string) error {
	if len([]rune(serial)) > 100 {
		return fmt.Errorf("%w: a serial number is up to 100 characters", ErrPurchase)
	}
	if len([]rune(note)) > 500 {
		return fmt.Errorf("%w: a note is up to 500 characters", ErrPurchase)
	}
	return nil
}

func ownLabelTx(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, label *uuid.UUID) error {
	if label == nil {
		return nil
	}
	c := LineChoice{Treatment: Record, LabelID: label}
	return checkChoiceTx(ctx, tx, tenant, &c, "", 0, 1)
}

// AddThingTx adds a row to Things we have by hand.
func AddThingTx(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, in NewThing, actor string) (Thing, error) {
	in.Name = strings.Join(strings.Fields(in.Name), " ")
	if in.Name == "" || len([]rune(in.Name)) > 200 {
		return Thing{}, fmt.Errorf("%w: say what it is, up to 200 characters", ErrPurchase)
	}
	if in.CostKobo != nil && (*in.CostKobo < 0 || *in.CostKobo > MaxPurchaseKobo) {
		return Thing{}, fmt.Errorf("%w: what it cost is from ₦0 to ₦100,000,000", ErrPurchase)
	}
	in.Serial, in.Note = strings.TrimSpace(in.Serial), strings.TrimSpace(in.Note)
	if err := checkThingText(in.Serial, in.Note); err != nil {
		return Thing{}, err
	}
	warranty, err := checkDate(in.WarrantyUntil)
	if err != nil {
		return Thing{}, err
	}
	if err := ownLabelTx(ctx, tx, tenant, in.LabelID); err != nil {
		return Thing{}, err
	}
	if in.SupplierID != nil {
		var ok bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM suppliers WHERE tenant_id = $1 AND id = $2)`, tenant, *in.SupplierID).Scan(&ok); err != nil {
			return Thing{}, err
		}
		if !ok {
			return Thing{}, ErrNoSupplier
		}
	}
	id := uuid.Must(uuid.NewV7())
	if _, err := tx.Exec(ctx, `INSERT INTO kept_things (id, tenant_id, name, label_id, supplier_id, cost_kobo, received_at, serial, warranty_until, note, created_by)
		VALUES ($1, $2, $3, $4, $5, $6, $7, NULLIF($8, ''), $9::date, NULLIF($10, ''), $11)`,
		id, tenant, in.Name, in.LabelID, in.SupplierID, in.CostKobo, in.ReceivedAt, in.Serial, warranty, in.Note, actor); err != nil {
		return Thing{}, err
	}
	if err := stockAudit(ctx, tx, tenant, "thing.added", in.Name, map[string]any{"thing_id": id, "by": actor}); err != nil {
		return Thing{}, err
	}
	return ThingTx(ctx, tx, tenant, id)
}

// ThingChange is what changes on a row; nil leaves a field as it is (an
// empty serial, warranty or note clears it; ClearLabel takes its label
// off).
type ThingChange struct {
	Status        *KeptStatus `json:"status"`
	Serial        *string     `json:"serial"`
	WarrantyUntil *string     `json:"warranty_until"`
	Note          *string     `json:"note"`
	LabelID       *uuid.UUID  `json:"label_id"`
	ClearLabel    bool        `json:"clear_label"`
	// SalePriceKobo, with Status sold: what it sold for, money in
	// labelled SaleCategory, paid in cash or by transfer (SalePaidHow).
	SalePriceKobo *int64 `json:"sale_price_kobo"`
	SalePaidHow   string `json:"sale_paid_how"`
}

// Sells reports whether the change records a sale's money.
func (c ThingChange) Sells() bool {
	return c.Status != nil && *c.Status == KeptSold && c.SalePriceKobo != nil
}

// SaleReference is the money log's reference of a thing's sale: one per
// thing, whatever it was paid in.
func SaleReference(id uuid.UUID) string { return "thing-sale:" + id.String() }

// ChangeThingTx changes a row: its status (sold, broken, given away, gone,
// or had again), serial, warranty, note or label. Sold with a price
// records the money in once (log), the row naming it and keeping key: the
// same sale with the same key again changes nothing and returns the row
// (a retry whose answer was lost); another is refused, recorded already.
// The row is locked first, so two sales at once are one after the other.
func ChangeThingTx(ctx context.Context, tx pgx.Tx, tenant, id uuid.UUID, c ThingChange, actor, key string, log *moneylog.Log) (Thing, error) {
	var saleKey string
	var saleKobo *int64
	err := tx.QueryRow(ctx, `SELECT COALESCE(t.sale_key, ''), (SELECT e.amount_kobo FROM money_entries e WHERE e.tenant_id = t.tenant_id AND e.id = t.sale_entry_id)
		FROM kept_things t WHERE t.tenant_id = $1 AND t.id = $2 FOR UPDATE OF t`, tenant, id).Scan(&saleKey, &saleKobo)
	if errors.Is(err, pgx.ErrNoRows) {
		return Thing{}, ErrNoThing
	}
	if err != nil {
		return Thing{}, err
	}
	t, err := ThingTx(ctx, tx, tenant, id)
	if err != nil {
		return t, err
	}
	if c.Sells() && t.SaleEntryID != nil && key != "" && saleKey == key && saleKobo != nil && *saleKobo == *c.SalePriceKobo {
		return t, nil
	}
	if c.Status != nil {
		switch *c.Status {
		case KeptHave, KeptSold, KeptBroken, KeptGiven, KeptGone:
		default:
			return t, fmt.Errorf("%w: a thing is had (have), sold, broken, given away (given) or gone", ErrPurchase)
		}
	}
	if c.SalePriceKobo != nil && !c.Sells() {
		return t, fmt.Errorf("%w: a price is for a thing marked sold", ErrPurchase)
	}
	// A thing sold with a price stays sold while its sale's money counts
	// (PR review fix): profit counts its cost as sold, so had again it
	// would be counted twice, there and on Things we have. The money stays
	// recorded; only a sale whose money no longer counts (reversed or
	// failed in the money log) may change.
	if c.Status != nil && *c.Status != KeptSold && t.Status == KeptSold && t.SaleEntryID != nil {
		var status string
		if err := tx.QueryRow(ctx, `SELECT status FROM money_entries WHERE tenant_id = $1 AND COALESCE(corrects, id) = $2
			ORDER BY recorded_at DESC, id DESC LIMIT 1`, tenant, *t.SaleEntryID).Scan(&status); err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return t, err
		}
		if status == "settled" || status == "pending" {
			return t, fmt.Errorf("%w: the sale of %s was recorded with its money, so it stays sold", ErrPurchase, t.Name)
		}
	}
	if c.Serial != nil {
		*c.Serial = strings.TrimSpace(*c.Serial)
	}
	if c.Note != nil {
		*c.Note = strings.TrimSpace(*c.Note)
	}
	if err := checkThingText(deref(c.Serial), deref(c.Note)); err != nil {
		return t, err
	}
	var warranty *string
	if c.WarrantyUntil != nil {
		if warranty, err = checkDate(*c.WarrantyUntil); err != nil {
			return t, err
		}
	}
	if err := ownLabelTx(ctx, tx, tenant, c.LabelID); err != nil {
		return t, err
	}
	var entry *uuid.UUID
	if c.Sells() {
		if t.SaleEntryID != nil {
			return t, fmt.Errorf("%w: the sale of %s was recorded already", ErrPurchase, t.Name)
		}
		amount := *c.SalePriceKobo
		if amount <= 0 || amount > MaxPurchaseKobo {
			return t, fmt.Errorf("%w: what it sold for is more than ₦0", ErrPurchase)
		}
		if c.SalePaidHow == "" {
			c.SalePaidHow = Cash
		}
		if c.SalePaidHow != Cash && c.SalePaidHow != Transfer {
			return t, fmt.Errorf("%w: paid in cash or by transfer", ErrPurchase)
		}
		if log == nil || key == "" || len(key) > 255 {
			return t, fmt.Errorf("%w: an idempotency key, up to 255 characters", ErrPurchase)
		}
		// One sale's money whatever it was paid in: the log's key holds the
		// channel, so look for the reference on any.
		var had bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM money_entries WHERE tenant_id = $1 AND channel IN ('cash', 'bank') AND reference = $2)`, tenant, SaleReference(id)).Scan(&had); err != nil {
			return t, err
		}
		if had {
			return t, fmt.Errorf("%w: the sale of %s was recorded already", ErrPurchase, t.Name)
		}
		channel := "cash"
		if c.SalePaidHow == Transfer {
			channel = "bank"
		}
		now := time.Now()
		// The money in: never the thing's own label (a machine sold is not
		// negative spending on machines), labelled by its category.
		e, _, err := log.RecordTx(ctx, tx, tenant, moneylog.Entry{Direction: "in", Kind: "cash", Status: "settled", Channel: channel,
			Reference: SaleReference(id), AmountKobo: &amount, OccurredAt: &now, Source: "entered", Category: SaleCategory,
			Description: cut("Sold: "+t.Name, 300), EnteredBy: actor})
		if err != nil {
			return t, err
		}
		entry = &e
	}
	if _, err := tx.Exec(ctx, `UPDATE kept_things SET status = COALESCE($3, status), serial = CASE WHEN $4 THEN NULLIF($5, '') ELSE serial END,
		warranty_until = CASE WHEN $6 THEN $7::date ELSE warranty_until END, note = CASE WHEN $8 THEN NULLIF($9, '') ELSE note END,
		label_id = CASE WHEN $10 THEN NULL WHEN $11::uuid IS NOT NULL THEN $11 ELSE label_id END, sale_entry_id = COALESCE($12, sale_entry_id),
		sale_key = CASE WHEN $12::uuid IS NOT NULL THEN $13 ELSE sale_key END
		WHERE tenant_id = $1 AND id = $2`, tenant, id, c.Status, c.Serial != nil, deref(c.Serial), c.WarrantyUntil != nil, warranty, c.Note != nil, deref(c.Note),
		c.ClearLabel, c.LabelID, entry, key); err != nil {
		return t, err
	}
	detail := map[string]any{"thing_id": id, "by": actor}
	if c.Status != nil {
		detail["status"] = *c.Status
	}
	if entry != nil {
		detail["sale_entry_id"], detail["sale_kobo"] = *entry, *c.SalePriceKobo
	}
	if err := stockAudit(ctx, tx, tenant, "thing.changed", t.Name, detail); err != nil {
		return t, err
	}
	return ThingTx(ctx, tx, tenant, id)
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
