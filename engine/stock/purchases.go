package stock

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/israel-duff/taskiem/engine/alerts"
	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/moneylog"
	"github.com/israel-duff/taskiem/engine/pii"
	"github.com/israel-duff/taskiem/engine/reach"
	"github.com/israel-duff/taskiem/engine/sender"
	"github.com/israel-duff/taskiem/engine/storefront"
	"github.com/israel-duff/taskiem/engine/whatsapp"
)

// Suppliers and purchase orders (docs/spec/stock.md §6, K3): who a
// business buys from, the orders drafted from what is low, approved and
// sent to the supplier in chat with Confirm and Change, received with the
// stock raised, and paid as an expense in the money log.

var (
	// ErrSupplierExists: a current supplier has that name or phone.
	ErrSupplierExists = errors.New("stock: already a supplier")
	// ErrNoSupplier: no such supplier.
	ErrNoSupplier = errors.New("stock: no such supplier")
	// ErrNoPurchase: no such purchase order.
	ErrNoPurchase = errors.New("stock: no such purchase order")
	// ErrPurchase: the order cannot do that now (its status, a line).
	ErrPurchase = errors.New("stock: that does not fit the order")
	// ErrSecondApproval: the order is above the business's limit and was
	// drafted by the person approving it.
	ErrSecondApproval = errors.New("stock: a second person approves this order")
	// ErrTooSoon: the tap-in link was sent a moment ago.
	ErrTooSoon = errors.New("stock: the link was sent a moment ago")
)

// Purchase order statuses.
const (
	Drafted      = "drafted"
	Sent         = "sent"
	Confirmed    = "confirmed"
	PartReceived = "part_received"
	FullReceived = "received"
	Cancelled    = "cancelled"
)

// OutboxSupplier is the chat outbox's kind for a message to a supplier;
// its step is StageOrder.
const (
	OutboxSupplier = "supplier"
	StageOrder     = "order"
)

// The owner's alerts: a supplier asks to change an order, or confirms one.
const (
	AlertSupplier  = "stock.supplier_change"
	AlertConfirmed = "stock.supplier_confirmed"
)

// InviteEvery is how often a supplier's tap-in link may be sent again.
const InviteEvery = 10 * time.Minute

// Messenger queues an SMS (sender.Queue).
type Messenger interface {
	Enqueue(ctx context.Context, tx pgx.Tx, m sender.Message) (bool, error)
}

// Purchases keeps a business's suppliers and purchase orders.
type Purchases struct {
	Pool  *pgxpool.Pool
	Vault reach.Cipher
	// Book keys suppliers' phones (decision 0039) and finds where they
	// tapped in.
	Book *reach.Book
	// Chat is the chat apps an order is sent on (whatsapp, telegram).
	Chat []string
	// Messages sends the tap-in link, and an order to a supplier on no
	// chat app, by SMS; nil sends none.
	Messages Messenger
	// PageBase is the form host's address, where the business's tap-in
	// page is; "" sends no link.
	PageBase string
	// Stock sends the received movements to the table.
	Stock *Stock
	// Cost sets an item's cost price in the catalogue after a receipt;
	// nil leaves it.
	Cost func(ctx context.Context, tenant uuid.UUID, row string, kobo int64, key string) error
	Now  func() time.Time
}

func (p *Purchases) now() time.Time {
	if p.Now != nil {
		return p.Now()
	}
	return time.Now()
}

// Ready reports whether suppliers can be kept here.
func (p *Purchases) Ready() bool {
	return p != nil && p.Pool != nil && p.Vault != nil && p.Book.Ready()
}

func (p *Purchases) inTx(ctx context.Context, tenant uuid.UUID, fn func(pgx.Tx) error) error {
	return db.InTenantTx(ctx, p.Pool, []uuid.UUID{tenant}, fn)
}

// --- suppliers ---

// Supplier is a supplier as their business sees them.
type Supplier struct {
	ID    uuid.UUID `json:"id"`
	Name  string    `json:"name"`
	Phone string    `json:"phone"`
	// Reached is where an order goes: the chat app they tapped in on, or
	// sms until they do.
	Reached string `json:"reached"`
	// OwedKobo is what was received from them and not yet paid.
	OwedKobo int64 `json:"owed_kobo"`
	// Open is how many of their orders are not yet received or cancelled.
	Open      int        `json:"open"`
	InvitedAt *time.Time `json:"invited_at"`
	CreatedAt time.Time  `json:"created_at"`
	RetiredAt *time.Time `json:"retired_at"`
}

// MaxSupplierName is the most a supplier's name may be.
const MaxSupplierName = 80

func cleanSupplierName(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if s == "" || len([]rune(s)) > MaxSupplierName {
		return ""
	}
	return s
}

const supplierCols = `s.id, s.name, s.phone, s.invited_at, s.created_at, s.retired_at,
	COALESCE((SELECT ch.channel FROM reach_contacts c JOIN reach_channels ch ON ch.contact_id = c.id
	  WHERE c.tenant_id = s.tenant_id AND c.phone_hash = s.phone_hash AND ch.stopped_at IS NULL
	  ORDER BY ch.linked_at DESC LIMIT 1), 'sms'),
	COALESCE((SELECT sum(GREATEST(o.received_kobo - o.paid_kobo, 0)) FROM purchase_orders o WHERE o.supplier_id = s.id), 0)::bigint,
	(SELECT count(*) FROM purchase_orders o WHERE o.supplier_id = s.id AND o.status IN ('drafted', 'sent', 'confirmed', 'part_received'))::int`

func (p *Purchases) scanSuppliers(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, rows pgx.Rows) ([]Supplier, error) {
	type row struct {
		x     Supplier
		phone []byte
	}
	list, err := pgx.CollectRows(rows, func(c pgx.CollectableRow) (row, error) {
		var v row
		return v, c.Scan(&v.x.ID, &v.x.Name, &v.phone, &v.x.InvitedAt, &v.x.CreatedAt, &v.x.RetiredAt, &v.x.Reached, &v.x.OwedKobo, &v.x.Open)
	})
	if err != nil {
		return nil, err
	}
	out := make([]Supplier, 0, len(list))
	for _, v := range list {
		x := v.x
		x.Phone = pii.Erased
		var env map[string]any
		if err := json.Unmarshal(v.phone, &env); err != nil {
			return nil, err
		}
		got, err := p.Vault.OpenTx(ctx, tx, tenant, env)
		switch {
		case errors.Is(err, pii.ErrErased):
		case err != nil:
			return nil, err
		default:
			if s, ok := got.(string); ok {
				x.Phone = s
			}
		}
		out = append(out, x)
	}
	return out, nil
}

// SuppliersTx lists the business's current suppliers by name, or the
// retired ones.
func (p *Purchases) SuppliersTx(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, retired bool) ([]Supplier, error) {
	rows, err := tx.Query(ctx, `SELECT `+supplierCols+` FROM suppliers s WHERE s.tenant_id = $1 AND (s.retired_at IS NOT NULL) = $2
		ORDER BY lower(s.name), s.id`, tenant, retired)
	if err != nil {
		return nil, err
	}
	return p.scanSuppliers(ctx, tx, tenant, rows)
}

// SupplierTx is one supplier.
func (p *Purchases) SupplierTx(ctx context.Context, tx pgx.Tx, tenant, id uuid.UUID) (Supplier, error) {
	rows, err := tx.Query(ctx, `SELECT `+supplierCols+` FROM suppliers s WHERE s.tenant_id = $1 AND s.id = $2`, tenant, id)
	if err != nil {
		return Supplier{}, err
	}
	list, err := p.scanSuppliers(ctx, tx, tenant, rows)
	if err != nil {
		return Supplier{}, err
	}
	if len(list) == 0 {
		return Supplier{}, ErrNoSupplier
	}
	return list[0], nil
}

// FindSupplier is the current supplier a name names: the exact name, else
// the one it starts, else the one containing it, case aside. Several, or
// none: ErrNoSupplier, with the names that matched.
func FindSupplier(list []Supplier, name string) (Supplier, []string, error) {
	n := strings.ToLower(strings.Join(strings.Fields(name), " "))
	if n == "" {
		return Supplier{}, nil, ErrNoSupplier
	}
	for _, test := range []func(string) bool{
		func(s string) bool { return s == n },
		func(s string) bool { return strings.HasPrefix(s, n) },
		func(s string) bool { return strings.Contains(s, n) },
	} {
		var hits []Supplier
		for _, x := range list {
			if x.RetiredAt == nil && test(strings.ToLower(x.Name)) {
				hits = append(hits, x)
			}
		}
		switch len(hits) {
		case 0:
			continue
		case 1:
			return hits[0], nil, nil
		default:
			names := make([]string, len(hits))
			for i, h := range hits {
				names[i] = h.Name
			}
			return Supplier{}, names, ErrNoSupplier
		}
	}
	return Supplier{}, nil, ErrNoSupplier
}

// AddSupplierTx adds a supplier by name and phone and sends them the
// tap-in link by SMS; a current supplier with the name or phone answers
// ErrSupplierExists with them.
func (p *Purchases) AddSupplierTx(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, name, phone, actor string) (Supplier, error) {
	clean := cleanSupplierName(name)
	if clean == "" {
		return Supplier{}, fmt.Errorf("%w: a supplier's name is 1 to %d characters", ErrPurchase, MaxSupplierName)
	}
	e164, err := p.Book.Phone(phone)
	if err != nil {
		return Supplier{}, fmt.Errorf("%w: %s is not a mobile number Taskiem can reach (like 0803 123 4567)", ErrPurchase, strings.TrimSpace(phone))
	}
	hash := p.Book.PhoneHash(e164)
	var id uuid.UUID
	err = tx.QueryRow(ctx, `SELECT id FROM suppliers WHERE tenant_id = $1 AND retired_at IS NULL AND (lower(name) = lower($2) OR phone_hash = $3) LIMIT 1`,
		tenant, clean, hash).Scan(&id)
	if err == nil {
		x, err := p.SupplierTx(ctx, tx, tenant, id)
		if err != nil {
			return Supplier{}, err
		}
		return x, fmt.Errorf("%w: %s (%s) is already your supplier", ErrSupplierExists, x.Name, x.Phone)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return Supplier{}, err
	}
	env, err := p.Vault.SealTx(ctx, tx, tenant, "phone", e164)
	if err != nil {
		return Supplier{}, err
	}
	id = uuid.Must(uuid.NewV7())
	if _, err := tx.Exec(ctx, `INSERT INTO suppliers (id, tenant_id, name, phone_hash, phone, created_by) VALUES ($1, $2, $3, $4, $5, $6)`,
		id, tenant, clean, hash, env, actor); err != nil {
		return Supplier{}, err
	}
	if err := stockAudit(ctx, tx, tenant, "supplier.added", clean, map[string]any{"supplier_id": id, "by": actor}); err != nil {
		return Supplier{}, err
	}
	x, err := p.SupplierTx(ctx, tx, tenant, id)
	if err != nil {
		return Supplier{}, err
	}
	if x.Reached == "sms" {
		if _, err := p.inviteTx(ctx, tx, tenant, x); err != nil {
			return Supplier{}, err
		}
		return p.SupplierTx(ctx, tx, tenant, id)
	}
	return x, nil
}

// RenameSupplierTx changes a current supplier's name. Their items keep the
// name the catalogue's supplier column holds: the caller changes them.
func (p *Purchases) RenameSupplierTx(ctx context.Context, tx pgx.Tx, tenant, id uuid.UUID, name, actor string) (Supplier, error) {
	clean := cleanSupplierName(name)
	if clean == "" {
		return Supplier{}, fmt.Errorf("%w: a supplier's name is 1 to %d characters", ErrPurchase, MaxSupplierName)
	}
	x, err := p.SupplierTx(ctx, tx, tenant, id)
	if err != nil {
		return Supplier{}, err
	}
	if x.RetiredAt != nil {
		return Supplier{}, fmt.Errorf("%w: %s is retired", ErrPurchase, x.Name)
	}
	var taken bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM suppliers WHERE tenant_id = $1 AND retired_at IS NULL AND lower(name) = lower($2) AND id <> $3)`,
		tenant, clean, id).Scan(&taken); err != nil {
		return Supplier{}, err
	}
	if taken {
		return Supplier{}, fmt.Errorf("%w: another supplier is called %s", ErrSupplierExists, clean)
	}
	if _, err := tx.Exec(ctx, `UPDATE suppliers SET name = $3, updated_at = now() WHERE tenant_id = $1 AND id = $2`, tenant, id, clean); err != nil {
		return Supplier{}, err
	}
	if err := stockAudit(ctx, tx, tenant, "supplier.renamed", clean, map[string]any{"supplier_id": id, "was": x.Name, "by": actor}); err != nil {
		return Supplier{}, err
	}
	return p.SupplierTx(ctx, tx, tenant, id)
}

// RetireSupplierTx retires a supplier; their orders stay.
func (p *Purchases) RetireSupplierTx(ctx context.Context, tx pgx.Tx, tenant, id uuid.UUID, actor string) (Supplier, error) {
	tag, err := tx.Exec(ctx, `UPDATE suppliers SET retired_at = now(), updated_at = now() WHERE tenant_id = $1 AND id = $2 AND retired_at IS NULL`, tenant, id)
	if err != nil {
		return Supplier{}, err
	}
	if tag.RowsAffected() == 0 {
		return Supplier{}, ErrNoSupplier
	}
	x, err := p.SupplierTx(ctx, tx, tenant, id)
	if err != nil {
		return Supplier{}, err
	}
	return x, stockAudit(ctx, tx, tenant, "supplier.retired", x.Name, map[string]any{"supplier_id": id, "by": actor})
}

// InviteSupplierTx sends a current supplier the tap-in link again (at most
// once in InviteEvery).
func (p *Purchases) InviteSupplierTx(ctx context.Context, tx pgx.Tx, tenant, id uuid.UUID, actor string) (Supplier, error) {
	x, err := p.SupplierTx(ctx, tx, tenant, id)
	if err != nil {
		return Supplier{}, err
	}
	if x.RetiredAt != nil {
		return Supplier{}, fmt.Errorf("%w: %s is retired", ErrPurchase, x.Name)
	}
	if x.InvitedAt != nil && p.now().Sub(*x.InvitedAt) < InviteEvery {
		return x, fmt.Errorf("%w: wait a few minutes before sending it again", ErrTooSoon)
	}
	if _, err := p.inviteTx(ctx, tx, tenant, x); err != nil {
		return Supplier{}, err
	}
	if err := stockAudit(ctx, tx, tenant, "supplier.invited", x.Name, map[string]any{"supplier_id": id, "by": actor}); err != nil {
		return Supplier{}, err
	}
	return p.SupplierTx(ctx, tx, tenant, id)
}

// tapIn is the business's tap-in page; "" when there is none.
func (p *Purchases) tapIn(ctx context.Context, tx pgx.Tx, tenant uuid.UUID) (string, error) {
	if p.PageBase == "" {
		return "", nil
	}
	id, err := reach.PageTx(ctx, tx, tenant, false)
	if err != nil {
		return "", err
	}
	return p.PageBase + "/u/" + id, nil
}

func businessName(ctx context.Context, tx pgx.Tx, tenant uuid.UUID) (string, error) {
	var name string
	err := tx.QueryRow(ctx, `SELECT name FROM tenants WHERE id = $1`, tenant).Scan(&name)
	return name, err
}

// inviteTx queues the tap-in link by SMS; false when it cannot be sent
// here.
func (p *Purchases) inviteTx(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, x Supplier) (bool, error) {
	if p.Messages == nil || x.Phone == pii.Erased {
		return false, nil
	}
	link, err := p.tapIn(ctx, tx, tenant)
	if err != nil || link == "" {
		return false, err
	}
	business, err := businessName(ctx, tx, tenant)
	if err != nil {
		return false, err
	}
	now := p.now()
	_, err = p.Messages.Enqueue(ctx, tx, sender.Message{Tenant: tenant, Channel: sender.SMS, Purpose: sender.PurposeNotice, To: x.Phone, SenderName: business,
		Text: business + " added you as a supplier. Tap to get their orders on WhatsApp and confirm them with one tap: " + link,
		Key:  fmt.Sprintf("supplier-invite:%s:%d", x.ID, now.Unix()/int64(InviteEvery/time.Second))})
	if errors.Is(err, sender.ErrAllowance) || errors.Is(err, sender.ErrInvalid) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	_, err = tx.Exec(ctx, `UPDATE suppliers SET invited_at = $2 WHERE id = $1`, x.ID, now)
	return err == nil, err
}

// --- settings ---

// Settings are a business's stock settings.
type Settings struct {
	// PurchaseLimitKobo: orders above it need a second person's approval;
	// nil, the owner approves any.
	PurchaseLimitKobo *int64 `json:"purchase_limit_kobo"`
}

// SettingsTx reads the business's stock settings.
func SettingsTx(ctx context.Context, tx pgx.Tx, tenant uuid.UUID) (Settings, error) {
	var s Settings
	err := tx.QueryRow(ctx, `SELECT purchase_limit_kobo FROM stock_settings WHERE tenant_id = $1`, tenant).Scan(&s.PurchaseLimitKobo)
	if errors.Is(err, pgx.ErrNoRows) {
		return s, nil
	}
	return s, err
}

// PutSettingsTx writes them.
func PutSettingsTx(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, s Settings, actor string) error {
	if s.PurchaseLimitKobo != nil && (*s.PurchaseLimitKobo < 0 || *s.PurchaseLimitKobo > MaxPurchaseKobo) {
		return fmt.Errorf("%w: the limit is from ₦0 to ₦100,000,000", ErrPurchase)
	}
	_, err := tx.Exec(ctx, `INSERT INTO stock_settings (tenant_id, purchase_limit_kobo, updated_by) VALUES ($1, $2, $3)
		ON CONFLICT (tenant_id) DO UPDATE SET purchase_limit_kobo = EXCLUDED.purchase_limit_kobo, updated_by = EXCLUDED.updated_by, updated_at = now()`,
		tenant, s.PurchaseLimitKobo, actor)
	if err != nil {
		return err
	}
	return stockAudit(ctx, tx, tenant, "stock.settings", "stock", map[string]any{"purchase_limit_kobo": s.PurchaseLimitKobo, "by": actor})
}

// --- purchase orders ---

// PurchaseLine is one item of an order.
type PurchaseLine struct {
	ItemID   string `json:"item_id"`
	Item     string `json:"item"`
	Unit     string `json:"unit,omitempty"`
	Quantity int64  `json:"quantity"`
	// CostKobo is the price of one, the item's last cost when drafted.
	CostKobo int64 `json:"cost_kobo"`
	Received int64 `json:"received"`
}

// Purchase is a purchase order.
type Purchase struct {
	ID           uuid.UUID      `json:"id"`
	Reference    string         `json:"reference"`
	SupplierID   uuid.UUID      `json:"supplier_id"`
	Supplier     string         `json:"supplier"`
	Lines        []PurchaseLine `json:"lines"`
	TotalKobo    int64          `json:"total_kobo"`
	Status       string         `json:"status"`
	Note         string         `json:"note,omitempty"`
	DraftedBy    string         `json:"drafted_by"`
	ApprovedBy   *string        `json:"approved_by"`
	SentAt       *time.Time     `json:"sent_at"`
	ConfirmedAt  *time.Time     `json:"confirmed_at"`
	ConfirmedHow *string        `json:"confirmed_how"`
	Counter      *string        `json:"counter"`
	CounterAt    *time.Time     `json:"counter_at"`
	ReceivedAt   *time.Time     `json:"received_at"`
	CancelledAt  *time.Time     `json:"cancelled_at"`
	ReceivedKobo int64          `json:"received_kobo"`
	PaidKobo     int64          `json:"paid_kobo"`
	// OwedKobo is what was received and not yet paid.
	OwedKobo  int64     `json:"owed_kobo"`
	CreatedAt time.Time `json:"created_at"`
}

// Open reports whether the order may still be received.
func (x Purchase) Open() bool {
	return x.Status == Drafted || x.Status == Sent || x.Status == Confirmed || x.Status == PartReceived
}

// Words says an order's items: "50 cartons Indomie, 20 bags Rice".
func (x Purchase) Words() string {
	parts := make([]string, len(x.Lines))
	for i, l := range x.Lines {
		parts[i] = (Item{Unit: l.Unit}).Quantity(l.Quantity) + " " + l.Item
	}
	return strings.Join(parts, ", ")
}

const purchaseCols = `o.id, o.reference, o.supplier_id, s.name, o.lines, o.total_kobo, o.status, o.note, o.drafted_by, o.approved_by, o.sent_at,
	o.confirmed_at, o.confirmed_how, o.counter, o.counter_at, o.received_at, o.cancelled_at, o.received_kobo, o.paid_kobo, o.created_at`

func scanPurchase(r pgx.Row) (Purchase, error) {
	var x Purchase
	var lines []byte
	err := r.Scan(&x.ID, &x.Reference, &x.SupplierID, &x.Supplier, &lines, &x.TotalKobo, &x.Status, &x.Note, &x.DraftedBy, &x.ApprovedBy, &x.SentAt,
		&x.ConfirmedAt, &x.ConfirmedHow, &x.Counter, &x.CounterAt, &x.ReceivedAt, &x.CancelledAt, &x.ReceivedKobo, &x.PaidKobo, &x.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return x, ErrNoPurchase
	}
	if err != nil {
		return x, err
	}
	x.OwedKobo = max(0, x.ReceivedKobo-x.PaidKobo)
	return x, json.Unmarshal(lines, &x.Lines)
}

// PurchaseTx is one order, by id; lock takes it for update.
func PurchaseTx(ctx context.Context, tx pgx.Tx, tenant, id uuid.UUID, lock bool) (Purchase, error) {
	q := `SELECT ` + purchaseCols + ` FROM purchase_orders o JOIN suppliers s ON s.id = o.supplier_id WHERE o.tenant_id = $1 AND o.id = $2`
	if lock {
		q += ` FOR UPDATE OF o`
	}
	return scanPurchase(tx.QueryRow(ctx, q, tenant, id))
}

// PurchaseByReferenceTx is one order, by its reference (P-7QX4KD).
func PurchaseByReferenceTx(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, ref string) (Purchase, error) {
	return scanPurchase(tx.QueryRow(ctx, `SELECT `+purchaseCols+` FROM purchase_orders o JOIN suppliers s ON s.id = o.supplier_id
		WHERE o.tenant_id = $1 AND o.reference = upper($2)`, tenant, strings.TrimSpace(ref)))
}

// PurchaseFilter narrows a list of orders.
type PurchaseFilter struct {
	Supplier *uuid.UUID
	// Open lists only those not received or cancelled.
	Open  bool
	Limit int
}

// PurchasesTx lists orders, the newest first.
func PurchasesTx(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, f PurchaseFilter) ([]Purchase, error) {
	if f.Limit <= 0 || f.Limit > 200 {
		f.Limit = 50
	}
	rows, err := tx.Query(ctx, `SELECT `+purchaseCols+` FROM purchase_orders o JOIN suppliers s ON s.id = o.supplier_id
		WHERE o.tenant_id = $1 AND ($2::uuid IS NULL OR o.supplier_id = $2)
		  AND (NOT $3 OR o.status IN ('drafted', 'sent', 'confirmed', 'part_received'))
		ORDER BY o.created_at DESC, o.id DESC LIMIT $4`, tenant, f.Supplier, f.Open, f.Limit)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (Purchase, error) { return scanPurchase(r) })
}

// MaxPurchaseKobo is the most an order, a payment or a limit may be:
// ₦100,000,000.
const MaxPurchaseKobo = 10_000_000_000

// MaxPurchaseLines is the most items an order holds.
const MaxPurchaseLines = 50

func checkLines(lines []PurchaseLine) (int64, error) {
	if len(lines) == 0 || len(lines) > MaxPurchaseLines {
		return 0, fmt.Errorf("%w: an order has 1 to %d items", ErrPurchase, MaxPurchaseLines)
	}
	seen := map[string]bool{}
	var total int64
	for _, l := range lines {
		if l.ItemID == "" || strings.TrimSpace(l.Item) == "" {
			return 0, fmt.Errorf("%w: each line names an item", ErrPurchase)
		}
		if seen[l.ItemID] {
			return 0, fmt.Errorf("%w: %s is on the order twice", ErrPurchase, l.Item)
		}
		seen[l.ItemID] = true
		if l.Quantity <= 0 || l.Quantity > MaxDelta {
			return 0, fmt.Errorf("%w: how many %s is a whole number from 1", ErrPurchase, l.Item)
		}
		if l.CostKobo < 0 || l.CostKobo > MaxPurchaseKobo {
			return 0, fmt.Errorf("%w: the price of %s is from ₦0", ErrPurchase, l.Item)
		}
		total += l.Quantity * l.CostKobo
	}
	if total > MaxPurchaseKobo {
		return 0, fmt.Errorf("%w: an order is at most ₦100,000,000", ErrPurchase)
	}
	return total, nil
}

// DraftIn is an order to draft.
type DraftIn struct {
	Supplier uuid.UUID
	Lines    []PurchaseLine
	Note     string
	Actor    string
	// Key drafts it once: the same key is the same order.
	Key string
}

// DraftTx drafts an order to a current supplier.
func DraftTx(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, in DraftIn) (Purchase, error) {
	if in.Key == "" {
		return Purchase{}, fmt.Errorf("%w: an idempotency key", ErrPurchase)
	}
	var id uuid.UUID
	err := tx.QueryRow(ctx, `SELECT id FROM purchase_orders WHERE tenant_id = $1 AND key = $2`, tenant, in.Key).Scan(&id)
	if err == nil {
		return PurchaseTx(ctx, tx, tenant, id, false)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return Purchase{}, err
	}
	for i := range in.Lines {
		in.Lines[i].Received = 0
		in.Lines[i].Item = cut(strings.TrimSpace(in.Lines[i].Item), 120)
	}
	total, err := checkLines(in.Lines)
	if err != nil {
		return Purchase{}, err
	}
	if len([]rune(in.Note)) > 500 {
		return Purchase{}, fmt.Errorf("%w: a note is up to 500 characters", ErrPurchase)
	}
	var supplier string
	err = tx.QueryRow(ctx, `SELECT name FROM suppliers WHERE tenant_id = $1 AND id = $2 AND retired_at IS NULL`, tenant, in.Supplier).Scan(&supplier)
	if errors.Is(err, pgx.ErrNoRows) {
		return Purchase{}, ErrNoSupplier
	}
	if err != nil {
		return Purchase{}, err
	}
	lines, _ := json.Marshal(in.Lines)
	id = uuid.Must(uuid.NewV7())
	ref := reference("P")
	if _, err := tx.Exec(ctx, `INSERT INTO purchase_orders (id, tenant_id, reference, supplier_id, lines, total_kobo, note, drafted_by, key)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`, id, tenant, ref, in.Supplier, lines, total, strings.TrimSpace(in.Note), in.Actor, in.Key); err != nil {
		return Purchase{}, err
	}
	if err := stockAudit(ctx, tx, tenant, "purchase.drafted", ref, map[string]any{"purchase_id": id, "supplier": supplier, "total_kobo": total, "by": in.Actor}); err != nil {
		return Purchase{}, err
	}
	return PurchaseTx(ctx, tx, tenant, id, false)
}

// DraftLowTx drafts an order per supplier for the low items naming a
// current supplier and on no open order, each for its suggested quantity
// at its last cost. skipped names the low items with no supplier Taskiem
// knows.
func (p *Purchases) DraftLowTx(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, items []Item, actor, key string) (drafted []Purchase, skipped []string, err error) {
	// Drafted already under this key: the same answer.
	rows, err := tx.Query(ctx, `SELECT o.id FROM purchase_orders o WHERE o.tenant_id = $1 AND o.key LIKE $2 || ':%' ORDER BY o.created_at, o.id`, tenant, key)
	if err != nil {
		return nil, nil, err
	}
	before, err := pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
	if err != nil {
		return nil, nil, err
	}
	if len(before) > 0 {
		for _, id := range before {
			x, err := PurchaseTx(ctx, tx, tenant, id, false)
			if err != nil {
				return nil, nil, err
			}
			drafted = append(drafted, x)
		}
		return drafted, nil, nil
	}
	suppliers, err := p.SuppliersTx(ctx, tx, tenant, false)
	if err != nil {
		return nil, nil, err
	}
	onOrder := map[string]bool{}
	rows, err = tx.Query(ctx, `SELECT l->>'item_id' FROM purchase_orders o, jsonb_array_elements(o.lines) l
		WHERE o.tenant_id = $1 AND o.status IN ('drafted', 'sent', 'confirmed', 'part_received')`, tenant)
	if err != nil {
		return nil, nil, err
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return nil, nil, err
	}
	for _, id := range ids {
		onOrder[id] = true
	}
	by := map[uuid.UUID][]PurchaseLine{}
	var order []uuid.UUID
	for _, it := range Low(items) {
		if onOrder[it.ID] {
			continue
		}
		s, _, err := FindSupplier(suppliers, it.Supplier)
		if it.Supplier == "" || err != nil || !strings.EqualFold(s.Name, strings.TrimSpace(it.Supplier)) {
			skipped = append(skipped, it.Name)
			continue
		}
		var cost int64
		if it.CostKobo != nil {
			cost = *it.CostKobo
		}
		if _, ok := by[s.ID]; !ok {
			order = append(order, s.ID)
		}
		by[s.ID] = append(by[s.ID], PurchaseLine{ItemID: it.ID, Item: it.Name, Unit: it.Unit, Quantity: it.Suggest(), CostKobo: cost})
	}
	for _, sid := range order {
		x, err := DraftTx(ctx, tx, tenant, DraftIn{Supplier: sid, Lines: by[sid], Actor: actor, Key: key + ":" + sid.String()})
		if err != nil {
			return nil, nil, err
		}
		drafted = append(drafted, x)
	}
	return drafted, skipped, nil
}

// SendTx approves a drafted order and sends it to its supplier: on the
// chat app they tapped in on, else by SMS with the tap-in link. Above the
// business's limit, the person who drafted it may not approve it, and a
// workflow may not send it.
func (p *Purchases) SendTx(ctx context.Context, tx pgx.Tx, tenant, id uuid.UUID, approver string) (Purchase, error) {
	x, err := PurchaseTx(ctx, tx, tenant, id, true)
	if err != nil {
		return x, err
	}
	if x.Status != Drafted {
		if x.Status == Sent || x.Status == Confirmed {
			return x, nil // sent already: the same answer
		}
		return x, fmt.Errorf("%w: %s is %s", ErrPurchase, x.Reference, StatusWords[x.Status])
	}
	s, err := SettingsTx(ctx, tx, tenant)
	if err != nil {
		return x, err
	}
	if s.PurchaseLimitKobo != nil && x.TotalKobo > *s.PurchaseLimitKobo {
		if strings.HasPrefix(approver, "workflow:") {
			return x, fmt.Errorf("%w: %s is above your limit of %s, so a person approves it", ErrSecondApproval, x.Reference, storefront.FormatNaira(*s.PurchaseLimitKobo))
		}
		if approver == x.DraftedBy {
			return x, fmt.Errorf("%w: %s is above your limit of %s, and you drafted it", ErrSecondApproval, x.Reference, storefront.FormatNaira(*s.PurchaseLimitKobo))
		}
	}
	if _, err := tx.Exec(ctx, `UPDATE purchase_orders SET status = 'sent', approved_by = $3, sent_at = now(), updated_at = now() WHERE tenant_id = $1 AND id = $2`,
		tenant, id, approver); err != nil {
		return x, err
	}
	if err := stockAudit(ctx, tx, tenant, "purchase.sent", x.Reference, map[string]any{"purchase_id": id, "total_kobo": x.TotalKobo, "by": approver}); err != nil {
		return x, err
	}
	if err := p.queueTx(ctx, tx, tenant, x); err != nil {
		return x, err
	}
	return PurchaseTx(ctx, tx, tenant, id, false)
}

// queueTx sends an order to its supplier: the chat outbox when they tapped
// in, else an SMS with the order and the tap-in link (their Confirm needs
// the chat app; until then the owner marks it confirmed).
func (p *Purchases) queueTx(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, x Purchase) error {
	if !p.Ready() {
		return nil
	}
	s, err := p.SupplierTx(ctx, tx, tenant, x.SupplierID)
	if err != nil {
		return err
	}
	key := "supplier:" + x.ID.String() + ":" + StageOrder
	t, ok, err := p.Book.FindTx(ctx, tx, tenant, uuid.Nil, s.Phone, p.Chat)
	if err != nil {
		return err
	}
	if ok && t.Contact != uuid.Nil {
		step := StageOrder
		_, err = whatsapp.Enqueue(ctx, tx, whatsapp.OutboxItem{Tenant: tenant, Channel: t.Channel, Kind: OutboxSupplier, Contact: &t.Contact,
			Run: &x.ID, Step: &step}, t.Channel+":"+key)
		return err
	}
	if p.Messages == nil || s.Phone == pii.Erased {
		return nil
	}
	business, err := businessName(ctx, tx, tenant)
	if err != nil {
		return err
	}
	text := "Order " + x.Reference + ": " + x.Words() + "."
	if link, err := p.tapIn(ctx, tx, tenant); err != nil {
		return err
	} else if link != "" {
		text += " Tap to confirm it on WhatsApp: " + link
	} else {
		text += " Please call or reply to confirm."
	}
	_, err = p.Messages.Enqueue(ctx, tx, sender.Message{Tenant: tenant, Channel: sender.SMS, Purpose: sender.PurposeNotice, To: s.Phone, SenderName: business,
		Text: text, Key: key})
	if errors.Is(err, sender.ErrAllowance) || errors.Is(err, sender.ErrInvalid) {
		return nil
	}
	return err
}

// ConfirmTx marks a sent order confirmed: by the supplier's tap, or by the
// owner when the supplier answered by phone or SMS.
func ConfirmTx(ctx context.Context, tx pgx.Tx, tenant, id uuid.UUID, how, actor string) (Purchase, error) {
	x, err := PurchaseTx(ctx, tx, tenant, id, true)
	if err != nil {
		return x, err
	}
	switch x.Status {
	case Confirmed, PartReceived, FullReceived:
		return x, nil
	case Sent:
	default:
		return x, fmt.Errorf("%w: %s is %s", ErrPurchase, x.Reference, StatusWords[x.Status])
	}
	if _, err := tx.Exec(ctx, `UPDATE purchase_orders SET status = 'confirmed', confirmed_at = now(), confirmed_how = $3, updated_at = now()
		WHERE tenant_id = $1 AND id = $2`, tenant, id, how); err != nil {
		return x, err
	}
	if err := stockAudit(ctx, tx, tenant, "purchase.confirmed", x.Reference, map[string]any{"purchase_id": id, "how": how, "by": actor}); err != nil {
		return x, err
	}
	return PurchaseTx(ctx, tx, tenant, id, false)
}

// CounterTx keeps the supplier's Change (what they can supply instead) on
// a sent or confirmed order and tells the owners.
func CounterTx(ctx context.Context, tx pgx.Tx, tenant, id uuid.UUID, text string) (Purchase, error) {
	text = cut(strings.TrimSpace(text), 1000)
	x, err := PurchaseTx(ctx, tx, tenant, id, true)
	if err != nil {
		return x, err
	}
	if x.Status != Sent && x.Status != Confirmed {
		return x, fmt.Errorf("%w: %s is %s", ErrPurchase, x.Reference, StatusWords[x.Status])
	}
	if text == "" {
		return x, fmt.Errorf("%w: say what you can supply", ErrPurchase)
	}
	if _, err := tx.Exec(ctx, `UPDATE purchase_orders SET counter = $3, counter_at = now(), updated_at = now() WHERE tenant_id = $1 AND id = $2`,
		tenant, id, text); err != nil {
		return x, err
	}
	if err := stockAudit(ctx, tx, tenant, "purchase.changed", x.Reference, map[string]any{"purchase_id": id, "by": "supplier"}); err != nil {
		return x, err
	}
	if _, err := alerts.Notify(ctx, tx, tenant, alerts.Alert{Kind: AlertSupplier, Dedup: x.ID.String() + ":" + p256(text),
		Title: x.Supplier + " asks to change order " + x.Reference,
		Body:  "You ordered " + x.Words() + ". " + x.Supplier + " says: " + text,
		Link:  "/storefront/stock"}); err != nil {
		return x, err
	}
	return PurchaseTx(ctx, tx, tenant, id, false)
}

// p256 is a short digest of text, to tell apart a supplier's changes.
func p256(text string) string {
	var h uint64 = 1469598103934665603
	for _, b := range []byte(text) {
		h ^= uint64(b)
		h *= 1099511628211
	}
	return fmt.Sprintf("%016x", h)
}

// CancelTx cancels an order nothing was received on.
func CancelTx(ctx context.Context, tx pgx.Tx, tenant, id uuid.UUID, actor string) (Purchase, error) {
	x, err := PurchaseTx(ctx, tx, tenant, id, true)
	if err != nil {
		return x, err
	}
	switch x.Status {
	case Cancelled:
		return x, nil
	case Drafted, Sent, Confirmed:
	default:
		return x, fmt.Errorf("%w: %s is %s; goods came in on it", ErrPurchase, x.Reference, StatusWords[x.Status])
	}
	if _, err := tx.Exec(ctx, `UPDATE purchase_orders SET status = 'cancelled', cancelled_at = now(), updated_at = now() WHERE tenant_id = $1 AND id = $2`,
		tenant, id); err != nil {
		return x, err
	}
	if err := stockAudit(ctx, tx, tenant, "purchase.cancelled", x.Reference, map[string]any{"purchase_id": id, "by": actor}); err != nil {
		return x, err
	}
	return PurchaseTx(ctx, tx, tenant, id, false)
}

// StatusWords says an order's status in words.
var StatusWords = map[string]string{
	Drafted: "a draft", Sent: "sent", Confirmed: "confirmed", PartReceived: "part received", FullReceived: "received", Cancelled: "cancelled",
}

// Cost is an item's cost price to set after a receipt.
type Cost struct {
	ItemID string
	Kobo   int64
}

// ReceiveTx records goods received on an order: a received movement per
// item naming the order, the lines' received quantities, the value
// received (owed to the supplier until paid), and the status, received
// once every line has come. got is how many came, by item id; key makes a
// receipt once.
func ReceiveTx(ctx context.Context, tx pgx.Tx, tenant, id uuid.UUID, got map[string]int64, actor, key string) (Purchase, []Movement, []Cost, error) {
	x, err := PurchaseTx(ctx, tx, tenant, id, true)
	if err != nil {
		return x, nil, nil, err
	}
	if x.Status != Sent && x.Status != Confirmed && x.Status != PartReceived {
		return x, nil, nil, fmt.Errorf("%w: %s is %s", ErrPurchase, x.Reference, StatusWords[x.Status])
	}
	if key == "" {
		return x, nil, nil, fmt.Errorf("%w: an idempotency key", ErrPurchase)
	}
	// A receipt already made answers as it did.
	var done bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM stock_movements WHERE tenant_id = $1 AND key LIKE $2 || ':%')`, tenant, "po:"+key).Scan(&done); err != nil {
		return x, nil, nil, err
	}
	if done {
		return x, nil, nil, nil
	}
	known := map[string]bool{}
	for _, l := range x.Lines {
		known[l.ItemID] = true
	}
	for item, n := range got {
		if !known[item] {
			return x, nil, nil, fmt.Errorf("%w: that item is not on %s", ErrPurchase, x.Reference)
		}
		if n < 0 || n > MaxDelta {
			return x, nil, nil, fmt.Errorf("%w: how many came is a whole number", ErrPurchase)
		}
	}
	var moves []Movement
	var costs []Cost
	var value int64
	all := true
	for i, l := range x.Lines {
		n := got[l.ItemID]
		if n > 0 {
			m, err := RecordTx(ctx, tx, tenant, Move{ItemID: l.ItemID, Item: l.Item, Reason: Received, Delta: &n, Reference: x.Reference,
				Note: "from " + x.Supplier, Actor: actor, Key: "po:" + key + ":" + l.ItemID})
			if err != nil {
				return x, nil, nil, err
			}
			moves = append(moves, m)
			x.Lines[i].Received += n
			value += n * l.CostKobo
			if l.CostKobo > 0 {
				costs = append(costs, Cost{ItemID: l.ItemID, Kobo: l.CostKobo})
			}
		}
		if x.Lines[i].Received < l.Quantity {
			all = false
		}
	}
	if len(moves) == 0 {
		return x, nil, nil, fmt.Errorf("%w: say how many came of at least one item", ErrPurchase)
	}
	status := PartReceived
	if all {
		status = FullReceived
	}
	lines, _ := json.Marshal(x.Lines)
	if _, err := tx.Exec(ctx, `UPDATE purchase_orders SET lines = $3, status = $4, received_kobo = received_kobo + $5,
		received_at = CASE WHEN $4 = 'received' THEN now() ELSE received_at END,
		confirmed_at = COALESCE(confirmed_at, now()), confirmed_how = COALESCE(confirmed_how, 'owner'), updated_at = now()
		WHERE tenant_id = $1 AND id = $2`, tenant, id, lines, status, value); err != nil {
		return x, nil, nil, err
	}
	if err := stockAudit(ctx, tx, tenant, "purchase.received", x.Reference, map[string]any{"purchase_id": id, "value_kobo": value, "status": status, "by": actor}); err != nil {
		return x, nil, nil, err
	}
	out, err := PurchaseTx(ctx, tx, tenant, id, false)
	return out, moves, costs, err
}

// Receive records goods received on an order, then sends the movements to
// the table and sets the items' cost prices; what is not sent is left for
// the sweep.
func (p *Purchases) Receive(ctx context.Context, tenant, id uuid.UUID, got map[string]int64, actor, key string) (Purchase, error) {
	var x Purchase
	var moves []Movement
	var costs []Cost
	err := p.inTx(ctx, tenant, func(tx pgx.Tx) error {
		var err error
		x, moves, costs, err = ReceiveTx(ctx, tx, tenant, id, got, actor, key)
		return err
	})
	if err != nil {
		return x, err
	}
	p.after(ctx, tenant, moves, costs, key)
	return x, nil
}

// after sends movements recorded and sets cost prices; failures are left
// for the sweep (movements) or the next receipt (costs).
func (p *Purchases) after(ctx context.Context, tenant uuid.UUID, moves []Movement, costs []Cost, key string) {
	if p.Stock != nil && p.Stock.Ready() {
		for _, m := range moves {
			_, _ = p.Stock.Apply(ctx, tenant, m.ID)
		}
	}
	if p.Cost != nil {
		for _, c := range costs {
			_ = p.Cost(ctx, tenant, c.ItemID, c.Kobo, "po-cost:"+key+":"+c.ItemID)
		}
	}
}

// Paid ways for a purchase.
var PaidWays = []string{Cash, Transfer}

// PayTx records a payment for an order: an expense in the money log,
// naming the supplier and the order, and the order's paid amount. key
// makes it once.
func PayTx(ctx context.Context, tx pgx.Tx, tenant, id uuid.UUID, amount int64, how, actor, key string, log *moneylog.Log) (Purchase, error) {
	x, err := PurchaseTx(ctx, tx, tenant, id, true)
	if err != nil {
		return x, err
	}
	if how != Cash && how != Transfer {
		return x, fmt.Errorf("%w: paid in cash or by transfer", ErrPurchase)
	}
	if amount <= 0 || amount > MaxPurchaseKobo {
		return x, fmt.Errorf("%w: the amount is more than ₦0", ErrPurchase)
	}
	if x.Status == Drafted || x.Status == Cancelled {
		return x, fmt.Errorf("%w: %s is %s", ErrPurchase, x.Reference, StatusWords[x.Status])
	}
	if key == "" || log == nil {
		return x, fmt.Errorf("%w: an idempotency key", ErrPurchase)
	}
	channel := "cash"
	if how == Transfer {
		channel = "bank"
	}
	now := time.Now()
	entry, fresh, err := log.RecordTx(ctx, tx, tenant, moneylog.Entry{Direction: "out", Kind: "expense", Status: "settled", Channel: channel,
		Reference: "purchase:" + key, AmountKobo: &amount, OccurredAt: &now, Source: "entered", Vendor: x.Supplier, Category: "stock",
		Description: cut("Order "+x.Reference+": "+x.Words(), 300), EnteredBy: actor})
	if err != nil {
		return x, err
	}
	if !fresh {
		return x, nil
	}
	if _, err := tx.Exec(ctx, `INSERT INTO purchase_payments (id, tenant_id, purchase_id, entry_id, amount_kobo, paid_how, paid_by) VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		uuid.Must(uuid.NewV7()), tenant, id, entry, amount, how, actor); err != nil {
		return x, err
	}
	if _, err := tx.Exec(ctx, `UPDATE purchase_orders SET paid_kobo = paid_kobo + $3, updated_at = now() WHERE tenant_id = $1 AND id = $2`, tenant, id, amount); err != nil {
		return x, err
	}
	if err := stockAudit(ctx, tx, tenant, "purchase.paid", x.Reference, map[string]any{"purchase_id": id, "amount_kobo": amount, "paid_how": how, "by": actor}); err != nil {
		return x, err
	}
	return PurchaseTx(ctx, tx, tenant, id, false)
}

// OpenFromTx is a supplier's oldest order still to be received (sent,
// confirmed or part received) holding item; ErrNoPurchase when none.
func OpenFromTx(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, supplierName, itemID string) (Purchase, error) {
	return scanPurchase(tx.QueryRow(ctx, `SELECT `+purchaseCols+` FROM purchase_orders o JOIN suppliers s ON s.id = o.supplier_id
		WHERE o.tenant_id = $1 AND lower(s.name) = lower($2) AND o.status IN ('sent', 'confirmed', 'part_received')
		  AND EXISTS (SELECT 1 FROM jsonb_array_elements(o.lines) l WHERE l->>'item_id' = $3)
		ORDER BY o.created_at, o.id LIMIT 1`, tenant, strings.TrimSpace(supplierName), itemID))
}

// UnpaidOfTx is a supplier's orders with something owed, the oldest
// first.
func UnpaidOfTx(ctx context.Context, tx pgx.Tx, tenant, supplier uuid.UUID) ([]Purchase, error) {
	rows, err := tx.Query(ctx, `SELECT `+purchaseCols+` FROM purchase_orders o JOIN suppliers s ON s.id = o.supplier_id
		WHERE o.tenant_id = $1 AND o.supplier_id = $2 AND o.status NOT IN ('drafted', 'cancelled')
		ORDER BY (o.received_kobo > o.paid_kobo) DESC, o.created_at, o.id LIMIT 20`, tenant, supplier)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (Purchase, error) { return scanPurchase(r) })
}

// SupplierOrdersTx is the orders sent to the supplier whose phone hash is
// given (their chat contact's) that they may still confirm or change, the
// newest first: how a supplier's words find their order.
func SupplierOrdersTx(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, phoneHash []byte) ([]Purchase, error) {
	rows, err := tx.Query(ctx, `SELECT `+purchaseCols+` FROM purchase_orders o JOIN suppliers s ON s.id = o.supplier_id
		WHERE o.tenant_id = $1 AND s.phone_hash = $2 AND s.retired_at IS NULL AND o.status IN ('sent', 'confirmed')
		ORDER BY o.sent_at DESC, o.id DESC LIMIT 5`, tenant, phoneHash)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (Purchase, error) { return scanPurchase(r) })
}

// SupplierPhoneHashTx is the phone hash of an order's supplier.
func SupplierPhoneHashTx(ctx context.Context, tx pgx.Tx, tenant, id uuid.UUID) ([]byte, error) {
	var h []byte
	err := tx.QueryRow(ctx, `SELECT s.phone_hash FROM purchase_orders o JOIN suppliers s ON s.id = o.supplier_id WHERE o.tenant_id = $1 AND o.id = $2`,
		tenant, id).Scan(&h)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNoPurchase
	}
	return h, err
}

// SortLines orders lines by item name, for a stable message.
func SortLines(lines []PurchaseLine) {
	sort.SliceStable(lines, func(i, j int) bool { return strings.ToLower(lines[i].Item) < strings.ToLower(lines[j].Item) })
}

// stockAudit appends an audit entry by the system.
func stockAudit(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, action, target string, detail map[string]any) error {
	raw, _ := json.Marshal(detail)
	_, err := tx.Exec(ctx, `SELECT taskiem_audit_append($1, 'system', 'stock', $2, $3, $4)`, tenant, action, target, raw)
	return err
}

// SupplierOrder is an order a supplier may still confirm or change, as
// their words to the platform number find it across businesses.
type SupplierOrder struct {
	Tenant    uuid.UUID
	Purchase  uuid.UUID
	Reference string
	Status    string
}

// SupplierOrders is the orders the sender may confirm or change: the
// supplier whose phone sent it (WhatsApp proves it; phone "" on Telegram,
// where the chat address's contact says who), the newest first.
func (p *Purchases) SupplierOrders(ctx context.Context, phone, channel, addr string) ([]SupplierOrder, error) {
	if !p.Ready() {
		return nil, nil
	}
	var h []byte
	if phone != "" {
		if e164, err := p.Book.Phone(phone); err == nil {
			h = p.Book.PhoneHash(e164)
		}
	}
	rows, err := p.Pool.Query(ctx, `SELECT tenant_id, purchase_id, reference, status FROM taskiem_supplier_orders($1, $2, $3)`,
		h, channel, p.Book.AddressHash(channel, addr))
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (SupplierOrder, error) {
		var o SupplierOrder
		return o, r.Scan(&o.Tenant, &o.Purchase, &o.Reference, &o.Status)
	})
}
