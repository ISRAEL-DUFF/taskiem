package stock

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
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
	"github.com/israel-duff/taskiem/engine/productstock"
	"github.com/israel-duff/taskiem/engine/reach"
	"github.com/israel-duff/taskiem/engine/sender"
	"github.com/israel-duff/taskiem/engine/spend"
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
	// ErrLinked: the order went through a link; the link's own steps
	// cancel it (engine/links).
	ErrLinked = errors.New("stock: the order went to a linked business")
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
	// Stock reads the items for drafting.
	Stock *Stock
	Now   func() time.Time
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
	// sms until they do; link when the supplier is a linked business
	// (docs/spec/business-links.md §7), whose orders go through the link.
	Reached string `json:"reached"`
	// LinkID is the link with the supplier's business, when it is one.
	LinkID *uuid.UUID `json:"link_id"`
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
	CASE WHEN s.link_id IS NOT NULL THEN 'link' ELSE COALESCE((SELECT ch.channel FROM reach_contacts c JOIN reach_channels ch ON ch.contact_id = c.id
	  WHERE c.tenant_id = s.tenant_id AND c.phone_hash = s.phone_hash AND ch.stopped_at IS NULL
	  ORDER BY ch.linked_at DESC LIMIT 1), 'sms') END, s.link_id,
	COALESCE((SELECT sum(GREATEST(o.received_kobo - o.paid_kobo, 0)) FROM purchase_orders o WHERE o.supplier_id = s.id AND s.link_id IS NULL), 0)::bigint,
	(SELECT count(*) FROM purchase_orders o WHERE o.supplier_id = s.id AND o.status IN ('drafted', 'sent', 'confirmed', 'part_received'))::int`

func (p *Purchases) scanSuppliers(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, rows pgx.Rows) ([]Supplier, error) {
	type row struct {
		x     Supplier
		phone []byte
	}
	list, err := pgx.CollectRows(rows, func(c pgx.CollectableRow) (row, error) {
		var v row
		return v, c.Scan(&v.x.ID, &v.x.Name, &v.phone, &v.x.InvitedAt, &v.x.CreatedAt, &v.x.RetiredAt, &v.x.Reached, &v.x.LinkID, &v.x.OwedKobo, &v.x.Open)
	})
	if err != nil {
		return nil, err
	}
	out := make([]Supplier, 0, len(list))
	for _, v := range list {
		x := v.x
		if len(v.phone) == 0 || string(v.phone) == "null" {
			// A linked business, reached through the link: no number.
			out = append(out, x)
			continue
		}
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

// LinkSupplierTx says a current supplier is a linked business (link), or
// no longer (nil): its orders then go through the link
// (docs/spec/business-links.md §7). The link must be active, and one
// supplier at a time names it.
func (p *Purchases) LinkSupplierTx(ctx context.Context, tx pgx.Tx, tenant, id uuid.UUID, link *uuid.UUID, actor string) (Supplier, error) {
	x, err := p.SupplierTx(ctx, tx, tenant, id)
	if err != nil {
		return Supplier{}, err
	}
	if x.RetiredAt != nil {
		return Supplier{}, fmt.Errorf("%w: %s is retired", ErrPurchase, x.Name)
	}
	if link != nil {
		var ok bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM taskiem_links_of() WHERE id = $1 AND state = 'active')`, *link).Scan(&ok); err != nil {
			return Supplier{}, err
		}
		if !ok {
			return Supplier{}, fmt.Errorf("%w: that is not one of your linked businesses", ErrPurchase)
		}
		var other string
		err := tx.QueryRow(ctx, `SELECT name FROM suppliers WHERE tenant_id = $1 AND link_id = $2 AND retired_at IS NULL AND id <> $3`, tenant, *link, id).Scan(&other)
		if err == nil {
			return Supplier{}, fmt.Errorf("%w: %s is that business already", ErrSupplierExists, other)
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return Supplier{}, err
		}
	}
	if link == nil && x.Phone == "" {
		return Supplier{}, fmt.Errorf("%w: %s was added from your link with them and has no phone number; add one before unlinking", ErrPurchase, x.Name)
	}
	if _, err := tx.Exec(ctx, `UPDATE suppliers SET link_id = $3, updated_at = now() WHERE tenant_id = $1 AND id = $2`, tenant, id, link); err != nil {
		return Supplier{}, err
	}
	if err := stockAudit(ctx, tx, tenant, "supplier.linked", x.Name, map[string]any{"supplier_id": id, "link": link, "by": actor}); err != nil {
		return Supplier{}, err
	}
	return p.SupplierTx(ctx, tx, tenant, id)
}

// AddLinkedSupplierTx makes a linked business one of the business's
// suppliers, with no phone number: its orders and bills go through the
// link. An unlinked supplier of the same name becomes that business; one
// already linked to it is answered as it is.
func (p *Purchases) AddLinkedSupplierTx(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, name string, link uuid.UUID, actor string) (Supplier, error) {
	var ok bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM taskiem_links_of() WHERE id = $1 AND state = 'active')`, link).Scan(&ok); err != nil {
		return Supplier{}, err
	}
	if !ok {
		return Supplier{}, fmt.Errorf("%w: that is not one of your linked businesses", ErrPurchase)
	}
	var id uuid.UUID
	err := tx.QueryRow(ctx, `SELECT id FROM suppliers WHERE tenant_id = $1 AND link_id = $2 AND retired_at IS NULL`, tenant, link).Scan(&id)
	if err == nil {
		return p.SupplierTx(ctx, tx, tenant, id)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return Supplier{}, err
	}
	clean := cleanSupplierName(name)
	if clean == "" {
		return Supplier{}, fmt.Errorf("%w: a supplier's name is 1 to %d characters", ErrPurchase, MaxSupplierName)
	}
	var linked *uuid.UUID
	err = tx.QueryRow(ctx, `SELECT id, link_id FROM suppliers WHERE tenant_id = $1 AND retired_at IS NULL AND lower(name) = lower($2)`, tenant, clean).Scan(&id, &linked)
	switch {
	case err == nil && linked == nil:
		return p.LinkSupplierTx(ctx, tx, tenant, id, &link, actor)
	case err == nil:
		return Supplier{}, fmt.Errorf("%w: %s is another linked business already", ErrSupplierExists, clean)
	case !errors.Is(err, pgx.ErrNoRows):
		return Supplier{}, err
	}
	id = uuid.Must(uuid.NewV7())
	if _, err := tx.Exec(ctx, `INSERT INTO suppliers (id, tenant_id, name, link_id, created_by) VALUES ($1, $2, $3, $4, $5)`,
		id, tenant, clean, link, actor); err != nil {
		return Supplier{}, err
	}
	if err := stockAudit(ctx, tx, tenant, "supplier.added", clean, map[string]any{"supplier_id": id, "link": link, "by": actor}); err != nil {
		return Supplier{}, err
	}
	return p.SupplierTx(ctx, tx, tenant, id)
}

// activeLinkTx is the link a supplier's orders go through: the one it
// names, while active; nil otherwise.
func activeLinkTx(ctx context.Context, tx pgx.Tx, tenant, supplier uuid.UUID) (*uuid.UUID, error) {
	var link *uuid.UUID
	if err := tx.QueryRow(ctx, `SELECT link_id FROM suppliers WHERE tenant_id = $1 AND id = $2`, tenant, supplier).Scan(&link); err != nil || link == nil {
		return nil, err
	}
	var ok bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM taskiem_links_of() WHERE id = $1 AND state = 'active')`, *link).Scan(&ok); err != nil || !ok {
		return nil, err
	}
	return link, nil
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
	if p.Messages == nil || x.Phone == pii.Erased || x.Phone == "" {
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
	// CountLimitKobo: a count's loss worth more than this at cost is a
	// leak; nil, ₦5,000 (K4).
	CountLimitKobo *int64 `json:"count_limit_kobo"`
}

// SettingsTx reads the business's stock settings.
func SettingsTx(ctx context.Context, tx pgx.Tx, tenant uuid.UUID) (Settings, error) {
	var s Settings
	err := tx.QueryRow(ctx, `SELECT purchase_limit_kobo, count_limit_kobo FROM stock_settings WHERE tenant_id = $1`, tenant).Scan(&s.PurchaseLimitKobo, &s.CountLimitKobo)
	if errors.Is(err, pgx.ErrNoRows) {
		return s, nil
	}
	return s, err
}

// PutSettingsTx writes them.
func PutSettingsTx(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, s Settings, actor string) error {
	for _, v := range []*int64{s.PurchaseLimitKobo, s.CountLimitKobo} {
		if v != nil && (*v < 0 || *v > MaxPurchaseKobo) {
			return fmt.Errorf("%w: a limit is from ₦0 to ₦100,000,000", ErrPurchase)
		}
	}
	_, err := tx.Exec(ctx, `INSERT INTO stock_settings (tenant_id, purchase_limit_kobo, count_limit_kobo, updated_by) VALUES ($1, $2, $3, $4)
		ON CONFLICT (tenant_id) DO UPDATE SET purchase_limit_kobo = EXCLUDED.purchase_limit_kobo, count_limit_kobo = EXCLUDED.count_limit_kobo,
		  updated_by = EXCLUDED.updated_by, updated_at = now()`,
		tenant, s.PurchaseLimitKobo, s.CountLimitKobo, actor)
	if err != nil {
		return err
	}
	return stockAudit(ctx, tx, tenant, "stock.settings", "stock", map[string]any{"purchase_limit_kobo": s.PurchaseLimitKobo, "count_limit_kobo": s.CountLimitKobo,
		"by": actor})
}

// --- purchase orders ---

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
	// An order to a linked supplier (docs/spec/business-links.md §7): the
	// link, and the seller's side as mirrored (its status, a word, the
	// lines it proposes instead, when it delivered); a cancel asked after
	// acceptance, and Not all came, are ours.
	LinkID        *uuid.UUID     `json:"link_id,omitempty"`
	SellerStatus  *string        `json:"seller_status,omitempty"`
	SellerNote    *string        `json:"seller_note,omitempty"`
	Change        []PurchaseLine `json:"change,omitempty"`
	DeliveredAt   *time.Time     `json:"delivered_at,omitempty"`
	CancelAskedAt *time.Time     `json:"cancel_asked_at,omitempty"`
	ShortAt       *time.Time     `json:"short_at,omitempty"`
	// Short is our count of what came of each line (Not all came);
	// InvoicedAt when the supplier raised the order's invoice (mirrored).
	Short      []int64    `json:"short,omitempty"`
	InvoicedAt *time.Time `json:"invoiced_at,omitempty"`
	// A deal (chat tools phase 7): sent at our own prices; its offers and
	// counters as the supplier keeps them (mirrored), newest last; when
	// the latest lapses.
	Offer       bool            `json:"offer"`
	DealHistory json.RawMessage `json:"deal_history,omitempty"`
	DealUntil   *time.Time      `json:"deal_until,omitempty"`
}

// Proposed reports whether a linked supplier drafted the order for us,
// waiting for our Confirm (docs/spec/business-links.md §7).
func (x Purchase) Proposed() bool {
	return x.SellerStatus != nil && *x.SellerStatus == "proposed" && x.Status == Drafted
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

const purchaseCols = `o.id, o.reference, o.supplier_id, s.name, o.total_kobo, o.status, o.note, o.drafted_by, o.approved_by, o.sent_at,
	o.confirmed_at, o.confirmed_how, o.counter, o.counter_at, o.received_at, o.cancelled_at, o.received_kobo, o.paid_kobo, o.created_at,
	o.link_id, o.seller_status, o.seller_note, o.change, o.delivered_at, o.cancel_asked_at, o.short_at, o.short, o.invoiced_at,
	o.offer, o.deal_history, o.deal_until`

func scanPurchase(r pgx.Row) (Purchase, error) {
	var x Purchase
	var change, short []byte
	err := r.Scan(&x.ID, &x.Reference, &x.SupplierID, &x.Supplier, &x.TotalKobo, &x.Status, &x.Note, &x.DraftedBy, &x.ApprovedBy, &x.SentAt,
		&x.ConfirmedAt, &x.ConfirmedHow, &x.Counter, &x.CounterAt, &x.ReceivedAt, &x.CancelledAt, &x.ReceivedKobo, &x.PaidKobo, &x.CreatedAt,
		&x.LinkID, &x.SellerStatus, &x.SellerNote, &change, &x.DeliveredAt, &x.CancelAskedAt, &x.ShortAt, &short, &x.InvoicedAt,
		&x.Offer, &x.DealHistory, &x.DealUntil)
	if errors.Is(err, pgx.ErrNoRows) {
		return x, ErrNoPurchase
	}
	if err != nil {
		return x, err
	}
	x.OwedKobo = max(0, x.ReceivedKobo-x.PaidKobo)
	if change != nil {
		if err := json.Unmarshal(change, &x.Change); err != nil {
			return x, err
		}
	}
	if short != nil {
		if err := json.Unmarshal(short, &x.Short); err != nil {
			return x, err
		}
	}
	return x, nil
}

// onePurchase reads an order and its lines.
func onePurchase(ctx context.Context, tx pgx.Tx, r pgx.Row) (Purchase, error) {
	x, err := scanPurchase(r)
	if err != nil {
		return x, err
	}
	x.Lines, err = LinesTx(ctx, tx, x.ID)
	if x.Lines == nil {
		x.Lines = []PurchaseLine{}
	}
	return x, err
}

// manyPurchases reads orders and their lines.
func manyPurchases(ctx context.Context, tx pgx.Tx, rows pgx.Rows, err error) ([]Purchase, error) {
	if err != nil {
		return nil, err
	}
	list, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (Purchase, error) { return scanPurchase(r) })
	if err != nil {
		return nil, err
	}
	ids := make([]uuid.UUID, len(list))
	for i, x := range list {
		ids[i] = x.ID
	}
	lines, err := linesOfTx(ctx, tx, ids)
	if err != nil {
		return nil, err
	}
	for i := range list {
		list[i].Lines = lines[list[i].ID]
		if list[i].Lines == nil {
			list[i].Lines = []PurchaseLine{}
		}
	}
	return list, nil
}

// PurchaseTx is one order, by id; lock takes it for update.
func PurchaseTx(ctx context.Context, tx pgx.Tx, tenant, id uuid.UUID, lock bool) (Purchase, error) {
	q := `SELECT ` + purchaseCols + ` FROM purchase_orders o JOIN suppliers s ON s.id = o.supplier_id WHERE o.tenant_id = $1 AND o.id = $2`
	if lock {
		q += ` FOR UPDATE OF o`
	}
	return onePurchase(ctx, tx, tx.QueryRow(ctx, q, tenant, id))
}

// PurchaseByReferenceTx is one order, by its reference (P-7QX4KD).
func PurchaseByReferenceTx(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, ref string) (Purchase, error) {
	return onePurchase(ctx, tx, tx.QueryRow(ctx, `SELECT `+purchaseCols+` FROM purchase_orders o JOIN suppliers s ON s.id = o.supplier_id
		WHERE o.tenant_id = $1 AND o.reference = upper($2)`, tenant, strings.TrimSpace(ref)))
}

// PurchaseFilter narrows a list of orders.
type PurchaseFilter struct {
	Supplier *uuid.UUID
	// Open lists only those not received or cancelled.
	Open  bool
	Limit int
	// Offset is where the page starts, newest first.
	Offset int
}

// PurchasesTx lists orders, the newest first.
func PurchasesTx(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, f PurchaseFilter) ([]Purchase, error) {
	if f.Limit <= 0 || f.Limit > 200 {
		f.Limit = 50
	}
	rows, err := tx.Query(ctx, `SELECT `+purchaseCols+` FROM purchase_orders o JOIN suppliers s ON s.id = o.supplier_id
		WHERE o.tenant_id = $1 AND ($2::uuid IS NULL OR o.supplier_id = $2)
		  AND (NOT $3 OR o.status IN ('drafted', 'sent', 'confirmed', 'part_received'))
		ORDER BY o.created_at DESC, o.id DESC LIMIT $4 OFFSET $5`, tenant, f.Supplier, f.Open, f.Limit, max(f.Offset, 0))
	return manyPurchases(ctx, tx, rows, err)
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
	var total int64
	for _, l := range lines {
		if strings.TrimSpace(l.Item) == "" {
			return 0, fmt.Errorf("%w: each line names an item", ErrPurchase)
		}
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

// checkTwice refuses an item twice on an order: the same item of a
// linked supplier, or two lines by hand into the same stock item.
func checkTwice(lines []PurchaseLine) error {
	seen := map[string]bool{}
	for _, l := range lines {
		k := ""
		switch {
		case l.SellerItemID != "":
			k = "s:" + l.SellerItemID
		case l.Treatment == AddToStock && l.StockItemID != nil:
			k = "i:" + l.StockItemID.String()
		default:
			continue
		}
		if seen[k] {
			return fmt.Errorf("%w: %s is on the order twice", ErrPurchase, l.Item)
		}
		seen[k] = true
	}
	return nil
}

// PutNewLinesTx writes a new order's lines (the order is written
// already), each treated as said on it, else as remembered, else as
// suggested (treatLinesTx). A remembered tracked line of more than
// TrackAsk not said yes to (TrackMany) is a *TrackError for the caller to
// ask.
func PutNewLinesTx(ctx context.Context, tx pgx.Tx, tenant, order, supplier uuid.UUID, lines []PurchaseLine, actor string) error {
	return putNewLinesTx(ctx, tx, tenant, order, supplier, lines, actor, true)
}

// PutPlacedLinesTx is PutNewLinesTx for an order someone else placed for
// the business (a linked supplier), whom nothing about the business's
// treatment of its items is asked or told (§7): a line that would ask
// first is recorded and left unconfirmed, for the business to say.
func PutPlacedLinesTx(ctx context.Context, tx pgx.Tx, tenant, order, supplier uuid.UUID, lines []PurchaseLine, actor string) error {
	return putNewLinesTx(ctx, tx, tenant, order, supplier, lines, actor, false)
}

func putNewLinesTx(ctx context.Context, tx pgx.Tx, tenant, order, supplier uuid.UUID, lines []PurchaseLine, actor string, ask bool) error {
	link, err := supplierLinkTx(ctx, tx, tenant, supplier, &order)
	if err != nil {
		return err
	}
	if link != nil {
		// A line by hand into a stock item, to a linked supplier: the one
		// item of theirs remembered as going into it, so the order reaches
		// them as their own item.
		for i := range lines {
			l := &lines[i]
			if l.SellerItemID != "" || l.Treatment != AddToStock || l.StockItemID == nil {
				continue
			}
			rows, err := tx.Query(ctx, `SELECT seller_item_id FROM item_links WHERE tenant_id = $1 AND link_id = $2 AND stock_item_id = $3 LIMIT 2`,
				tenant, *link, *l.StockItemID)
			if err != nil {
				return err
			}
			ids, err := pgx.CollectRows(rows, pgx.RowTo[string])
			if err != nil {
				return err
			}
			if len(ids) == 1 {
				l.SellerItemID = ids[0]
			}
		}
	}
	if err := treatLinesTx(ctx, tx, tenant, supplier, link, lines, actor, ask); err != nil {
		return err
	}
	if err := checkTwice(lines); err != nil {
		return err
	}
	return insertLinesTx(ctx, tx, tenant, order, lines)
}

// DraftIn is an order to draft.
type DraftIn struct {
	Supplier uuid.UUID
	// Lines: each its item's name, unit, quantity and price; on an order
	// to a linked supplier its SellerItemID; and how it is treated, when
	// said (else as remembered, else as suggested).
	Lines []PurchaseLine
	Note  string
	Actor string
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
		in.Lines[i].Item = cut(strings.Join(strings.Fields(in.Lines[i].Item), " "), 120)
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
	id = uuid.Must(uuid.NewV7())
	ref := reference("P")
	if _, err := tx.Exec(ctx, `INSERT INTO purchase_orders (id, tenant_id, reference, supplier_id, total_kobo, note, drafted_by, key)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`, id, tenant, ref, in.Supplier, total, strings.TrimSpace(in.Note), in.Actor, in.Key); err != nil {
		return Purchase{}, err
	}
	if err := PutNewLinesTx(ctx, tx, tenant, id, in.Supplier, in.Lines, in.Actor); err != nil {
		return Purchase{}, err
	}
	if err := stockAudit(ctx, tx, tenant, "purchase.drafted", ref, map[string]any{"purchase_id": id, "supplier": supplier, "total_kobo": total, "by": in.Actor}); err != nil {
		return Purchase{}, err
	}
	return PurchaseTx(ctx, tx, tenant, id, false)
}

// LineOf is a line adding to an item's stock: a product's (its stock
// item, by its uses) or a material's, at its cost when cost is nil. A
// product counted by nothing yet starts being counted (StartCountingTx).
func LineOf(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, it Item, quantity int64, cost *int64, actor string) (PurchaseLine, error) {
	l := PurchaseLine{Item: it.Name, Unit: it.SaleUnit(), Quantity: quantity, Treatment: AddToStock, Uses: max(it.Uses, 1)}
	switch {
	case cost != nil:
		l.CostKobo = *cost
	case it.CostKobo != nil:
		// One of the line is one sale of the product: its uses.
		l.CostKobo = *it.SaleCostKobo()
	}
	sid, err := uuid.Parse(it.StockItemID)
	if err != nil {
		if it.HasSizes() {
			return l, fmt.Errorf("%w: %s comes in sizes and colours: order each one", ErrPurchase, it.Name)
		}
		if sid, err = StartCountingTx(ctx, tx, tenant, it.ID, it.Name, it.Unit, actor); err != nil {
			return l, err
		}
		l.Uses = 1
	}
	l.StockItemID = &sid
	return l, nil
}

// DraftLowTx drafts an order per supplier for the low items naming a
// current supplier and on no open order, each for its suggested quantity
// at its last cost, added to its stock item. skipped names the low items
// with no supplier Taskiem knows.
func (p *Purchases) DraftLowTx(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, items []Item, actor, key string) (drafted []Purchase, skipped []string, err error) {
	// Drafted already under this key: the same answer. Its orders are keyed
	// "<key>:<supplier id>", matched exactly (a key's _ or % is no
	// wildcard).
	key = FitKey(key)
	rows, err := tx.Query(ctx, `SELECT o.id FROM purchase_orders o WHERE o.tenant_id = $1 AND starts_with(o.key, $2 || ':') AND length(o.key) = length($2) + 37
		ORDER BY o.created_at, o.id`, tenant, key)
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
	// The stock items on an open order: a low stock item counting two
	// products is ordered once.
	onOrder, err := OnOrderTx(ctx, tx, tenant)
	if err != nil {
		return nil, nil, err
	}
	by := map[uuid.UUID][]PurchaseLine{}
	var order []uuid.UUID
	for _, it := range Low(items) {
		sid, perr := uuid.Parse(it.StockItemID)
		if it.OnOrder(onOrder) || perr != nil {
			continue
		}
		onOrder[it.StockItemID] = true
		s, _, err := FindSupplier(suppliers, it.Supplier)
		if it.Supplier == "" || err != nil || !strings.EqualFold(s.Name, strings.TrimSpace(it.Supplier)) {
			skipped = append(skipped, it.Name)
			continue
		}
		if _, ok := by[s.ID]; !ok {
			order = append(order, s.ID)
		}
		// What is low is the stock item: its line is counted in it, one of
		// the line one of the stock item, at its cost.
		var cost int64
		if it.CostKobo != nil {
			cost = *it.CostKobo
		}
		by[s.ID] = append(by[s.ID], PurchaseLine{Item: it.Name, Unit: it.Unit, Quantity: it.Suggest(), CostKobo: cost, Treatment: AddToStock, StockItemID: &sid, Uses: 1})
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
	if x.Proposed() {
		return x, fmt.Errorf("%w: %s drafted %s for you; confirm it on its page, saying which of your items each line is", ErrPurchase, x.Supplier, x.Reference)
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
	// A linked supplier's order goes through the link: the send notes it,
	// and the link worker writes it into the supplier's business
	// (docs/spec/business-links.md §7). A link no longer active sends it
	// by chat as before.
	link, err := activeLinkTx(ctx, tx, tenant, x.SupplierID)
	if err != nil {
		return x, err
	}
	if link == nil {
		var reachable bool
		if err := tx.QueryRow(ctx, `SELECT phone IS NOT NULL FROM suppliers WHERE tenant_id = $1 AND id = $2`, tenant, x.SupplierID).Scan(&reachable); err != nil {
			return x, err
		}
		if !reachable {
			return x, fmt.Errorf("%w: your link with %s has ended and Taskiem has no number for them; add one to send %s", ErrPurchase, x.Supplier, x.Reference)
		}
	}
	if _, err := tx.Exec(ctx, `UPDATE purchase_orders SET status = 'sent', approved_by = $3, sent_at = now(), link_id = $4, updated_at = now()
		WHERE tenant_id = $1 AND id = $2`, tenant, id, approver, link); err != nil {
		return x, err
	}
	if err := stockAudit(ctx, tx, tenant, "purchase.sent", x.Reference, map[string]any{"purchase_id": id, "total_kobo": x.TotalKobo, "by": approver,
		"linked": link != nil}); err != nil {
		return x, err
	}
	if link == nil {
		if err := p.queueTx(ctx, tx, tenant, x); err != nil {
			return x, err
		}
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
	if _, err := alerts.Notify(ctx, tx, tenant, alerts.Alert{Kind: AlertSupplier, Dedup: x.ID.String() + ":" + digest(text),
		Title: x.Supplier + " asks to change order " + x.Reference,
		Body:  "You ordered " + x.Words() + ". " + x.Supplier + " says: " + text,
		Link:  "/storefront/stock"}); err != nil {
		return x, err
	}
	return PurchaseTx(ctx, tx, tenant, id, false)
}

// digest is a short digest of text, to tell apart a supplier's changes.
func digest(text string) string {
	h := fnv.New64a()
	_, _ = h.Write([]byte(text))
	return fmt.Sprintf("%016x", h.Sum64())
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
	if x.LinkID != nil && (x.Status != Drafted || x.Proposed()) {
		return x, ErrLinked
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

// ReceiveTx records goods received on an order, each line by its
// treatment (what a business buys §5, §8): a stock line moves its stock
// item (a received movement naming the order) by how many came times its
// uses and sets the stock item's cost to the line's over its uses; a
// tracked line puts each unit that came on Things we have (kept_things,
// numbered on the line, each written once); a recorded line moves
// nothing. Then the lines' received quantities, the value received (owed
// to the supplier until paid), and the status, received once every line
// has come. got is how many came, by the lines' ItemID (or their own id,
// or a product counted by a stock line's stock item); key makes a receipt
// once: the same key again answers as it did, making nothing twice.
func ReceiveTx(ctx context.Context, tx pgx.Tx, tenant, id uuid.UUID, got map[string]int64, actor, key string) (Purchase, []Movement, error) {
	x, err := PurchaseTx(ctx, tx, tenant, id, true)
	if err != nil {
		return x, nil, err
	}
	if key == "" {
		return x, nil, fmt.Errorf("%w: an idempotency key", ErrPurchase)
	}
	key = FitKey(key)
	// A receipt already made answers as it did, whatever the order's
	// status now (the receipt may have completed it).
	if done, err := receiptMadeTx(ctx, tx, tenant, id, "received", key); err != nil || done {
		return x, nil, err
	}
	if x.Status != Sent && x.Status != Confirmed && x.Status != PartReceived {
		return x, nil, fmt.Errorf("%w: %s is %s", ErrPurchase, x.Reference, StatusWords[x.Status])
	}
	if x.LinkID != nil && x.SellerStatus != nil && !strings.HasPrefix(key, LinkReceiptKey) {
		// An order in a linked supplier's business is received by the
		// link's own steps (All came, Not all came, or the day's end from
		// the supplier's count), never by hand (business links §7).
		return x, nil, ErrLinked
	}
	came := make([]int64, len(x.Lines))
	for item, n := range got {
		i, err := lineFor(ctx, tx, tenant, x.Lines, item)
		if err != nil {
			return x, nil, err
		}
		if i < 0 {
			return x, nil, fmt.Errorf("%w: that item is not on %s", ErrPurchase, x.Reference)
		}
		if n < 0 || n > MaxDelta {
			return x, nil, fmt.Errorf("%w: how many came is a whole number", ErrPurchase)
		}
		came[i] += n
	}
	for i, l := range x.Lines {
		// An order takes what it ordered and no more: what came beyond it
		// would be owed to the supplier at the order's price with nobody
		// having agreed to buy it. It is recorded as goods received on
		// their own (or on a new order). A linked supplier's count is
		// taken as it is: that order is received by the link's own steps.
		left := max(0, l.Quantity-l.Received)
		if came[i] > left && !strings.HasPrefix(key, LinkReceiptKey) {
			if left == 0 {
				return x, nil, fmt.Errorf("%w: all the %s on %s has come already; record more as goods received on their own", ErrPurchase, l.Item, x.Reference)
			}
			return x, nil, fmt.Errorf("%w: only %d of %s are still to come on %s; record %d here, and the rest as goods received on their own",
				ErrPurchase, left, l.Item, x.Reference, left)
		}
	}
	var moves []Movement
	var value int64
	some, all := false, true
	now := time.Now()
	for i, l := range x.Lines {
		n := came[i]
		if n > 0 {
			some = true
			switch l.Treatment {
			case AddToStock:
				// The line's stock item, by the line's quantity times its
				// uses, at the cost of one of it.
				uses := max(l.Uses, 1)
				unit := l.CostKobo / uses
				m, err := RecordTx(ctx, tx, tenant, Move{ItemID: l.StockItemID.String(), Item: l.Item, Reason: Received, Delta: &n, Uses: uses, Reference: x.Reference,
					Note: "from " + x.Supplier, Actor: actor, Key: "po:" + key + ":" + l.ID.String(), CostKobo: positive(unit)})
				if err != nil {
					return x, nil, err
				}
				moves = append(moves, m)
				if unit > 0 {
					if _, err := tx.Exec(ctx, `UPDATE stock_items SET cost_kobo = $3 WHERE tenant_id = $1 AND id = $2`, tenant, *l.StockItemID, unit); err != nil {
						return x, nil, err
					}
				}
			case Track:
				// One row per unit that came, numbered after those received
				// before; a unit written already is not written again.
				for u := l.Received + 1; u <= min(l.Received+n, l.Quantity, MaxTracked); u++ {
					if _, err := tx.Exec(ctx, `INSERT INTO kept_things (id, tenant_id, name, label_id, supplier_id, purchase_order_id, purchase_line_id, unit_no,
						cost_kobo, received_at, created_by) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
						ON CONFLICT (tenant_id, purchase_line_id, unit_no) DO NOTHING`,
						uuid.Must(uuid.NewV7()), tenant, l.Item, l.LabelID, x.SupplierID, x.ID, l.ID, u, l.CostKobo, now, actor); err != nil {
						return x, nil, err
					}
				}
			}
			x.Lines[i].Received += n
			if _, err := tx.Exec(ctx, `UPDATE purchase_lines SET received = $3 WHERE tenant_id = $1 AND id = $2`, tenant, l.ID, x.Lines[i].Received); err != nil {
				return x, nil, err
			}
			value += n * l.CostKobo
		}
		if x.Lines[i].Received < l.Quantity {
			all = false
		}
	}
	if !some {
		return x, nil, fmt.Errorf("%w: say how many came of at least one item", ErrPurchase)
	}
	status := PartReceived
	if all {
		status = FullReceived
	}
	if _, err := tx.Exec(ctx, `UPDATE purchase_orders SET status = $3, received_kobo = received_kobo + $4,
		received_at = CASE WHEN $3 = 'received' THEN now() ELSE received_at END,
		confirmed_at = COALESCE(confirmed_at, now()), confirmed_how = COALESCE(confirmed_how, 'owner'), updated_at = now()
		WHERE tenant_id = $1 AND id = $2`, tenant, id, status, value); err != nil {
		return x, nil, err
	}
	if err := stockAudit(ctx, tx, tenant, "purchase.received", x.Reference, map[string]any{"purchase_id": id, "value_kobo": value, "status": status, "by": actor,
		"key": key}); err != nil {
		return x, nil, err
	}
	out, err := PurchaseTx(ctx, tx, tenant, id, false)
	return out, moves, err
}

// receiptMadeTx keeps the key of a receipt (kind received) or an
// adjustment (short_adjusted) of an order, in the transaction making it,
// and reports whether one was made under that key already: then nothing
// is kept, and the caller makes nothing. The row is the receipt's (it
// rolls back with it), on purchase_receipts' key (migration 00504).
func receiptMadeTx(ctx context.Context, tx pgx.Tx, tenant, order uuid.UUID, kind, key string) (bool, error) {
	tag, err := tx.Exec(ctx, `INSERT INTO purchase_receipts (tenant_id, purchase_order_id, kind, key) VALUES ($1, $2, $3, $4) ON CONFLICT DO NOTHING`,
		tenant, order, kind, key)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() == 0, nil
}

// LinkReceiptKey starts the idempotency key of a receipt made by the
// link's own steps on an order to a linked supplier: the only receipt
// such an order takes.
const LinkReceiptKey = "link-receipt-"

// AdjustShortTx takes off an order what was received above the buyer's
// count of a short delivery the linked supplier agreed (business links
// §17): for a stock line an adjustment movement for the difference, for a
// tracked line the rows of the units that did not come marked gone; the
// lines' received quantities and the value received brought down to the
// count. counts is the buyer's count per line, in order; key makes it
// once.
func AdjustShortTx(ctx context.Context, tx pgx.Tx, tenant, id uuid.UUID, counts []int64, actor, key string) (Purchase, []Movement, error) {
	x, err := PurchaseTx(ctx, tx, tenant, id, true)
	if err != nil {
		return x, nil, err
	}
	if key == "" {
		return x, nil, fmt.Errorf("%w: an idempotency key", ErrPurchase)
	}
	if len(counts) != len(x.Lines) {
		return x, nil, fmt.Errorf("%w: a count for each of the %d items", ErrPurchase, len(x.Lines))
	}
	key = FitKey(key)
	if done, err := receiptMadeTx(ctx, tx, tenant, id, "short_adjusted", key); err != nil || done {
		return x, nil, err
	}
	var moves []Movement
	changed := false
	var value int64
	for i, l := range x.Lines {
		over := l.Received - max(counts[i], 0)
		if over <= 0 {
			continue
		}
		switch l.Treatment {
		case AddToStock:
			delta := -over
			uses := max(l.Uses, 1)
			m, err := RecordTx(ctx, tx, tenant, Move{ItemID: l.StockItemID.String(), Item: l.Item, Reason: Adjustment, Delta: &delta, Uses: uses, Reference: x.Reference,
				Note: "not all came from " + x.Supplier + ", as agreed", Actor: actor, Key: "po:" + key + ":" + l.ID.String(), CostKobo: positive(l.CostKobo / uses)})
			if err != nil {
				return x, nil, err
			}
			moves = append(moves, m)
		case Track:
			if _, err := tx.Exec(ctx, `UPDATE kept_things SET status = 'gone', note = left(COALESCE(note || ' · ', '') || 'Did not come (short delivery agreed)', 500)
				WHERE tenant_id = $1 AND purchase_line_id = $2 AND unit_no > $3 AND status = 'have'`, tenant, l.ID, l.Received-over); err != nil {
				return x, nil, err
			}
		}
		x.Lines[i].Received -= over
		changed = true
		if _, err := tx.Exec(ctx, `UPDATE purchase_lines SET received = $3 WHERE tenant_id = $1 AND id = $2`, tenant, l.ID, x.Lines[i].Received); err != nil {
			return x, nil, err
		}
		value += over * l.CostKobo
	}
	if !changed {
		return x, nil, nil
	}
	if _, err := tx.Exec(ctx, `UPDATE purchase_orders SET received_kobo = GREATEST(0, received_kobo - $3), updated_at = now()
		WHERE tenant_id = $1 AND id = $2`, tenant, id, value); err != nil {
		return x, nil, err
	}
	if err := stockAudit(ctx, tx, tenant, "purchase.short_adjusted", x.Reference, map[string]any{"purchase_id": id, "value_kobo": value, "by": actor, "key": key}); err != nil {
		return x, nil, err
	}
	out, err := PurchaseTx(ctx, tx, tenant, id, false)
	return out, moves, err
}

// Receive records goods received on an order (ReceiveTx).
func (p *Purchases) Receive(ctx context.Context, tenant, id uuid.UUID, got map[string]int64, actor, key string) (Purchase, error) {
	var x Purchase
	err := p.inTx(ctx, tenant, func(tx pgx.Tx) error {
		var err error
		x, _, err = ReceiveTx(ctx, tx, tenant, id, got, actor, key)
		return err
	})
	return x, err
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
	key = FitKey(key)
	// A payment already recorded under this key answers as it did, even
	// when the order is paid up now.
	var made bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM purchase_payments p JOIN money_entries e ON e.id = p.entry_id
		WHERE p.tenant_id = $1 AND p.purchase_id = $2 AND e.reference = $3)`, tenant, id, "purchase:"+key).Scan(&made); err != nil {
		return x, err
	}
	if made {
		return x, nil
	}
	// An order is paid up to its total and no more: an amount beyond what
	// is left would be an expense nothing explains. (Paying before the
	// goods come is allowed, as it was: up to the order's total.)
	if rest := max(0, x.TotalKobo-x.PaidKobo); amount > rest {
		if rest == 0 {
			return x, fmt.Errorf("%w: %s is paid in full already", ErrPurchase, x.Reference)
		}
		return x, fmt.Errorf("%w: only %s is left to pay on %s", ErrPurchase, storefront.FormatNaira(rest), x.Reference)
	}
	channel := "cash"
	if how == Transfer {
		channel = "bank"
	}
	// One expense per label on the order, each with its payment row
	// (engine/spend; what a business buys §8, The expense).
	_, fresh, err := spend.RecordPurchaseTx(ctx, tx, log, tenant, spend.PurchasePay{Order: id, Reference: x.Reference, Supplier: x.Supplier,
		AmountKobo: amount, How: how, Channel: channel, Key: key, Actor: actor, When: time.Now()})
	if err != nil {
		return x, err
	}
	if !fresh {
		return x, nil
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
// confirmed or part received) with a line adding to the stock item an
// item is (a product's, or a material); ErrNoPurchase when none.
func OpenFromTx(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, supplierName, itemID string) (Purchase, error) {
	c, ok, err := productstock.ItemOfTx(ctx, tx, tenant, itemID)
	if err != nil {
		return Purchase{}, err
	}
	if !ok {
		return Purchase{}, ErrNoPurchase
	}
	return onePurchase(ctx, tx, tx.QueryRow(ctx, `SELECT `+purchaseCols+` FROM purchase_orders o JOIN suppliers s ON s.id = o.supplier_id
		WHERE o.tenant_id = $1 AND lower(s.name) = lower($2) AND o.status IN ('sent', 'confirmed', 'part_received')
		  AND EXISTS (SELECT 1 FROM purchase_lines l WHERE l.tenant_id = o.tenant_id AND l.purchase_order_id = o.id AND l.stock_item_id = $3)
		ORDER BY o.created_at, o.id LIMIT 1`, tenant, strings.TrimSpace(supplierName), c.StockItemID))
}

// UnpaidOfTx is a supplier's orders with something owed, the oldest
// first.
func UnpaidOfTx(ctx context.Context, tx pgx.Tx, tenant, supplier uuid.UUID) ([]Purchase, error) {
	rows, err := tx.Query(ctx, `SELECT `+purchaseCols+` FROM purchase_orders o JOIN suppliers s ON s.id = o.supplier_id
		WHERE o.tenant_id = $1 AND o.supplier_id = $2 AND o.status NOT IN ('drafted', 'cancelled')
		ORDER BY (o.received_kobo > o.paid_kobo) DESC, o.created_at, o.id LIMIT 20`, tenant, supplier)
	return manyPurchases(ctx, tx, rows, err)
}

// SupplierOrdersTx is the orders sent to the supplier whose phone hash is
// given (their chat contact's) that they may still confirm or change, the
// newest first: how a supplier's words find their order.
func SupplierOrdersTx(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, phoneHash []byte) ([]Purchase, error) {
	rows, err := tx.Query(ctx, `SELECT `+purchaseCols+` FROM purchase_orders o JOIN suppliers s ON s.id = o.supplier_id
		WHERE o.tenant_id = $1 AND s.phone_hash = $2 AND s.retired_at IS NULL AND o.status IN ('sent', 'confirmed')
		ORDER BY o.sent_at DESC, o.id DESC LIMIT 5`, tenant, phoneHash)
	return manyPurchases(ctx, tx, rows, err)
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

// positive is n, or nil when it is not above 0.
func positive(n int64) *int64 {
	if n <= 0 {
		return nil
	}
	return &n
}
