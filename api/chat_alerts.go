package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/invoicing"
	"github.com/israel-duff/taskiem/engine/links"
	"github.com/israel-duff/taskiem/engine/matching"
	"github.com/israel-duff/taskiem/engine/pii"
	"github.com/israel-duff/taskiem/engine/stock"
	"github.com/israel-duff/taskiem/engine/variants"
)

// Chat tools phase 4 (docs/spec/chat-tools.md §6): alerts you can act on,
// as cards at the end of the person's Taskiem conversation. Each is what
// needs them now, read when they look, so a card is gone once it is dealt
// with; each with its next step: low stock with Reorder (from the linked
// supplier's price list), an overdue invoice with Send reminder, a new order
// with Accept, an order waiting for a rider with Assign rider, a payment not
// yet matched with a picker of open invoices, and an approval with Approve
// and Decline (confirmed as its rules ask). Each kind only for those whose
// role may see it; Later puts a card off for its person alone.

// laterFor is how long Later puts a card off.
const laterFor = 4 * time.Hour

// maxChatAlerts is how many cards the conversation ends with at most.
const maxChatAlerts = 20

type chatAlert struct {
	// Key names the alert for Later: its kind and what it is about.
	Key  string    `json:"key"`
	Kind string    `json:"kind"` // low_stock | overdue | new_order | rider | unmatched | approval
	At   time.Time `json:"at"`
	// Title and Line are the card's words.
	Title      string `json:"title"`
	Line       string `json:"line"`
	AmountKobo *int64 `json:"amount_kobo,omitempty"`
	// ID and Reference are what it is about: the stock item, the invoice,
	// the order or the payment.
	ID        string `json:"id,omitempty"`
	Reference string `json:"reference,omitempty"`
	// Reorder is low stock's next step, when a linked supplier sells the
	// item and the person may order.
	Reorder *alertReorder `json:"reorder,omitempty"`
	// Approval is the approval waiting, as the Approvals page has it, and
	// Who asked for it ("Tunde (cashier)").
	Approval *approvalItem `json:"approval,omitempty"`
	Who      string        `json:"who,omitempty"`
	// Cells are a product's sizes and colours that are low, when the card
	// is about a product that has them: its stock form opens on them.
	Cells []string `json:"cells,omitempty"`
}

type alertReorder struct {
	Link         uuid.UUID `json:"link"`
	Supplier     string    `json:"supplier"`
	SellerItemID string    `json:"seller_item_id"`
	Quantity     int64     `json:"quantity"`
	PriceKobo    int64     `json:"price_kobo"`
}

// getChatAlerts is GET /v1/chat-alerts: the caller's alerts, oldest first.
// With chat tools off there are none.
func (s *Server) getChatAlerts(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r.Context())
	if p.UserID == uuid.Nil {
		s.fail(w, r, errNoPerson)
		return
	}
	ctx := r.Context()
	var on bool
	later := map[string]bool{}
	if err := s.tx(r, func(tx pgx.Tx) error {
		var err error
		if on, err = chatToolsOn(ctx, tx, p.TenantID); err != nil || !on {
			return err
		}
		rows, err := tx.Query(ctx, `SELECT alert_key FROM chat_alerts_later WHERE tenant_id = $1 AND user_id = $2 AND until > now()`, p.TenantID, p.UserID)
		if err != nil {
			return err
		}
		keys, err := pgx.CollectRows(rows, pgx.RowTo[string])
		for _, k := range keys {
			later[k] = true
		}
		return err
	}); err != nil {
		s.fail(w, r, err)
		return
	}
	out := []chatAlert{}
	if !on {
		writeJSON(w, http.StatusOK, map[string]any{"alerts": out})
		return
	}
	// Each kind on its own: one that cannot be read leaves the others.
	for _, kind := range []func(context.Context, *Principal) ([]chatAlert, error){
		s.lowStockAlerts, s.overdueAlerts, s.newOrderAlerts, s.riderAlerts, s.unmatchedAlerts, s.approvalAlerts, s.dealAlerts,
	} {
		got, err := kind(ctx, p)
		if err != nil {
			s.Logger.Warn("chat alerts", "tenant", p.TenantID, "err", err)
			continue
		}
		for _, a := range got {
			if !later[a.Key] {
				out = append(out, a)
			}
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"alerts": capAlerts(out)})
}

// perKind is how many cards of one kind the conversation shows at most, so
// a long list of one (twenty items low) never pushes the others out.
const perKind = 5

// capAlerts keeps the newest perKind of each kind and maxChatAlerts in all,
// approvals first among what is kept, oldest first.
func capAlerts(all []chatAlert) []chatAlert {
	sort.SliceStable(all, func(i, j int) bool { return all[i].At.After(all[j].At) })
	n := map[string]int{}
	out := []chatAlert{}
	for _, pass := range []bool{true, false} {
		for _, a := range all {
			if (a.Kind == "approval") != pass || n[a.Kind] >= perKind || len(out) >= maxChatAlerts {
				continue
			}
			n[a.Kind]++
			out = append(out, a)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].At.Before(out[j].At) })
	return out
}

// postChatAlertLater is POST /v1/chat-alerts/later {key}: the card is put
// off for the caller, for a few hours.
func (s *Server) postChatAlertLater(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r.Context())
	if p.UserID == uuid.Nil {
		s.fail(w, r, errNoPerson)
		return
	}
	var in struct {
		Key string `json:"key"`
		// Hours it is put off for: 4 unless said (a reminder sent, 24).
		Hours int `json:"hours"`
	}
	if err := decodeBody(r, &in); err != nil {
		s.fail(w, r, err)
		return
	}
	if in.Key = strings.TrimSpace(in.Key); in.Key == "" || len(in.Key) > 200 {
		s.fail(w, r, fmt.Errorf("%w: key is the alert's key", errBadRequest))
		return
	}
	if in.Hours < 0 || in.Hours > 72 {
		s.fail(w, r, fmt.Errorf("%w: hours is 1 to 72", errBadRequest))
		return
	}
	until := time.Now().Add(laterFor)
	if in.Hours > 0 {
		until = time.Now().Add(time.Duration(in.Hours) * time.Hour)
	}
	if err := s.tx(r, func(tx pgx.Tx) error {
		_, err := tx.Exec(r.Context(), `INSERT INTO chat_alerts_later (tenant_id, user_id, alert_key, until) VALUES ($1, $2, $3, $4)
			ON CONFLICT (tenant_id, user_id, alert_key) DO UPDATE SET until = EXCLUDED.until`, p.TenantID, p.UserID, in.Key, until)
		return err
	}); err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"key": in.Key, "until": until})
}

// lowStockAlerts are the items at or below their reorder level; Reorder
// from the linked supplier that sells it, at their price, when the person
// may order (drafting and sending).
func (s *Server) lowStockAlerts(ctx context.Context, p *Principal) ([]chatAlert, error) {
	if !p.Can(PermTableRead) || s.Store == nil {
		return nil, nil
	}
	items, err := s.alertStockItems(ctx, p.TenantID)
	if errors.Is(err, errStockOff) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	// An item already on an open order is dealt with: no card.
	onOrder := map[string]bool{}
	if err := db0(ctx, s, p.TenantID, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT DISTINCT l->>'item_id' FROM purchase_orders o, jsonb_array_elements(o.lines) l
			WHERE o.tenant_id = $1 AND o.status IN ('drafted', 'sent', 'confirmed', 'part_received')`, p.TenantID)
		if err != nil {
			return err
		}
		ids, err := pgx.CollectRows(rows, pgx.RowTo[*string])
		for _, id := range ids {
			if id != nil {
				onOrder[*id] = true
			}
		}
		return err
	}); err != nil {
		return nil, err
	}
	var low []stock.Item
	// A product's sizes and colours are told as one card: "Air Max 90 is
	// running low: 42, Black and 43, Black".
	byProduct := map[string][]stock.Item{}
	var products []string
	for _, it := range items {
		if !it.Low() || onOrder[it.ID] || !it.Available && it.VariantOf != "" {
			continue
		}
		if it.VariantOf != "" {
			if byProduct[it.VariantOf] == nil {
				products = append(products, it.VariantOf)
			}
			byProduct[it.VariantOf] = append(byProduct[it.VariantOf], it)
			continue
		}
		low = append(low, it)
	}
	if len(low) == 0 && len(products) == 0 {
		return nil, nil
	}
	// Which linked supplier sells each of ours, and at what.
	type sells struct {
		supplier links.SupplierPrices
		item     links.PriceItem
	}
	from := map[string]sells{}
	if p.Can(PermTableEdit) && p.Can(PermStorefrontManage) {
		if err := db0(ctx, s, p.TenantID, func(tx pgx.Tx) error {
			lists, err := links.SupplierPricesTx(ctx, tx, p.TenantID, uuid.Nil)
			if err != nil {
				return err
			}
			for _, l := range lists {
				ls, err := links.ItemLinksTx(ctx, tx, p.TenantID, l.LinkID)
				if err != nil {
					return err
				}
				for _, il := range ls {
					for _, pi := range l.Items {
						if pi.ItemID == il.SellerItemID && pi.Available {
							if _, ok := from[il.ItemID]; !ok {
								from[il.ItemID] = sells{supplier: l, item: pi}
							}
						}
					}
				}
			}
			return nil
		}); err != nil {
			return nil, err
		}
	}
	now := time.Now()
	out := make([]chatAlert, 0, len(low))
	for _, it := range low {
		left := fmt.Sprintf("%d left", *it.Stock)
		if u := it.UnitName(*it.Stock); u != "" {
			left = fmt.Sprintf("%d %s left", *it.Stock, u)
		}
		if *it.Stock <= 0 {
			left = "None left"
		}
		a := chatAlert{Key: "low_stock:" + it.ID, Kind: "low_stock", At: now, ID: it.ID,
			Title: it.Name + " is running low", Line: fmt.Sprintf("%s · you reorder at %d", left, *it.Reorder)}
		if x, ok := from[it.ID]; ok {
			a.Reorder = &alertReorder{Link: x.supplier.LinkID, Supplier: x.supplier.Business, SellerItemID: x.item.ItemID,
				Quantity: max(it.Suggest(), int64(x.item.MinQuantity)), PriceKobo: x.item.PriceKobo}
		}
		out = append(out, a)
	}
	named := map[string]stock.Item{}
	for _, it := range items {
		named[it.ID] = it
	}
	for _, pid := range products {
		prod, ok := named[pid]
		if !ok {
			continue
		}
		cells := byProduct[pid]
		labels := make([]string, 0, len(cells))
		ids := make([]string, 0, len(cells))
		for _, c := range cells {
			labels = append(labels, variants.Label(c.Variant, prod.Choices))
			ids = append(ids, c.ID)
		}
		out = append(out, chatAlert{Key: "low_stock:" + pid, Kind: "low_stock", At: now, ID: pid, Cells: ids,
			Title: prod.Name + " is running low", Line: lowSizesLine(labels)})
	}
	return out, nil
}

// lowSizesLine says which of a product's sizes and colours are low:
// "42, Black is low", "42 and 43 are low", "40, 41, 42 and 3 more are low".
func lowSizesLine(labels []string) string {
	switch n := len(labels); {
	case n == 1:
		return labels[0] + " is low"
	case n <= 3:
		return strings.Join(labels[:n-1], "; ") + " and " + labels[n-1] + " are low"
	default:
		return strings.Join(labels[:3], "; ") + fmt.Sprintf(" and %d more are low", n-3)
	}
}

// alertStock keeps each business's stock items for a little while: the
// cards are read every minute by each open conversation, and reading the
// catalogue is the costly part.
var alertStock = struct {
	sync.Mutex
	m map[uuid.UUID]stockSnap
}{m: map[uuid.UUID]stockSnap{}}

type stockSnap struct {
	at    time.Time
	items []stock.Item
}

const alertStockFor = 30 * time.Second

func (s *Server) alertStockItems(ctx context.Context, tenant uuid.UUID) ([]stock.Item, error) {
	alertStock.Lock()
	snap, ok := alertStock.m[tenant]
	alertStock.Unlock()
	if ok && time.Since(snap.at) < alertStockFor {
		return snap.items, nil
	}
	_, items, err := s.stockItems(ctx, tenant)
	if err != nil {
		return nil, err
	}
	alertStock.Lock()
	for k, v := range alertStock.m {
		if time.Since(v.at) >= alertStockFor {
			delete(alertStock.m, k)
		}
	}
	alertStock.m[tenant] = stockSnap{at: time.Now(), items: items}
	alertStock.Unlock()
	return items, nil
}

// overdueAlerts are the business's invoices past their due date.
func (s *Server) overdueAlerts(ctx context.Context, p *Principal) ([]chatAlert, error) {
	if !p.Can(PermInvoiceRead) || s.Invoices == nil {
		return nil, nil
	}
	var list []invoicing.Invoice
	if err := db0(ctx, s, p.TenantID, func(tx pgx.Tx) error {
		var err error
		// Invoices, not the debtor notebook's entries (no one to remind).
		list, err = s.Invoices.ListOf(ctx, tx, p.TenantID, "overdue", "invoice", maxChatAlerts)
		return err
	}); err != nil {
		return nil, err
	}
	out := make([]chatAlert, 0, len(list))
	for _, x := range list {
		maskInvoice(&x)
		due := x.DueKobo
		at := x.CreatedAt
		line := x.Customer.Name + " · " + invoicing.Naira(due) + " to pay"
		if d, err := time.Parse("2006-01-02", x.DueOn); err == nil {
			at = d
			line += fmt.Sprintf(" · due %s", d.Format("2 Jan"))
		}
		out = append(out, chatAlert{Key: "overdue:" + x.ID.String(), Kind: "overdue", At: at, ID: x.ID.String(), Reference: x.Number,
			Title: "Invoice " + x.Number + " is overdue", Line: line, AmountKobo: &due})
	}
	return out, nil
}

// newOrderAlerts are the orders from linked businesses waiting for an
// answer.
func (s *Server) newOrderAlerts(ctx context.Context, p *Principal) ([]chatAlert, error) {
	if !p.Can(PermOrders) {
		return nil, nil
	}
	var list []links.Order
	if err := db0(ctx, s, p.TenantID, func(tx pgx.Tx) error {
		var err error
		list, err = links.ListOrdersTx(ctx, tx, true, 200)
		return err
	}); err != nil {
		return nil, err
	}
	var out []chatAlert
	for _, o := range list {
		// One waiting for the owner's OK is a deal card (dealAlerts).
		if o.Status != "received" || o.DealOwnerAt != nil {
			continue
		}
		total := o.TotalKobo
		title, line := "New order "+o.OrderReference+" from "+o.BuyerName, invoicing.Naira(total)
		if links.IsOffer(o.Lines) {
			// An offer (phase 7): what they offer against the seller's prices.
			title = o.BuyerName + " offers " + invoicing.Naira(total) + " for " + o.OrderReference
			if was := dealWas(o.Lines); was > total {
				line = "was " + invoicing.Naira(was)
			}
		}
		out = append(out, chatAlert{Key: "new_order:" + o.ID.String(), Kind: "new_order", At: o.CreatedAt, ID: o.ID.String(), Reference: o.OrderReference,
			Title: title, Line: line, AmountKobo: &total})
	}
	return out, nil
}

// riderAlerts are the storefront's paid orders for delivery that no rider
// has been asked to take, when the business has riders.
func (s *Server) riderAlerts(ctx context.Context, p *Principal) ([]chatAlert, error) {
	if !p.Can(PermOrders) || s.Dispatch == nil {
		return nil, nil
	}
	var out []chatAlert
	err := db0(ctx, s, p.TenantID, func(tx pgx.Tx) error {
		// Ready to go out, as dispatch has it (dispatch.ReadyReason): paid,
		// or nothing to pay before delivery.
		rows, err := tx.Query(ctx, `SELECT o.id, o.reference, o.total_kobo, o.created_at, o.paid_at IS NOT NULL FROM storefront_orders o
			WHERE o.tenant_id = $1 AND o.fulfilment = 'delivery' AND o.status IN ('paid', 'accepted') AND o.created_at > now() - interval '14 days'
			  AND (o.paid_at IS NOT NULL OR o.payment = 'none')
			  AND EXISTS (SELECT 1 FROM riders x WHERE x.tenant_id = $1 AND x.retired_at IS NULL AND x.active)
			  AND NOT EXISTS (SELECT 1 FROM deliveries d WHERE d.tenant_id = $1 AND d.order_id = o.id
			                  AND d.status IN ('offered', 'accepted', 'picked_up', 'arrived', 'delivered'))
			ORDER BY o.created_at LIMIT 20`, p.TenantID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var id uuid.UUID
			var ref string
			var total int64
			var at time.Time
			var paid bool
			if err := rows.Scan(&id, &ref, &total, &at, &paid); err != nil {
				return err
			}
			line := invoicing.Naira(total) + " · paid on delivery"
			if paid {
				line = "Paid " + invoicing.Naira(total) + " · for delivery"
			}
			out = append(out, chatAlert{Key: "rider:" + id.String(), Kind: "rider", At: at, ID: id.String(), Reference: ref,
				Title: "Order " + ref + " needs a rider", Line: line, AmountKobo: &total})
		}
		return rows.Err()
	})
	return out, err
}

// unmatchedAlerts are the payments that came in and are not matched to
// an invoice or order yet.
func (s *Server) unmatchedAlerts(ctx context.Context, p *Principal) ([]chatAlert, error) {
	if !p.Can(PermInvoiceRead) || s.Invoices == nil {
		return nil, nil
	}
	var list []matching.Payment
	if err := db0(ctx, s, p.TenantID, func(tx pgx.Tx) error {
		var err error
		list, err = matching.Unmatched(ctx, tx, p.TenantID, maxChatAlerts)
		return err
	}); err != nil {
		return nil, err
	}
	out := make([]chatAlert, 0, len(list))
	for _, x := range list {
		amount := x.LeftKobo
		if amount == nil {
			amount = x.AmountKobo
		}
		title := "A payment is not matched yet"
		if amount != nil {
			title = "A payment of " + invoicing.Naira(*amount) + " is not matched yet"
		}
		line := x.When.Format("2 Jan, 15:04")
		if x.Reference != "" {
			line = "Reference " + x.Reference + " · " + line
		}
		out = append(out, chatAlert{Key: "unmatched:" + x.EntryID.String(), Kind: "unmatched", At: x.When, ID: x.EntryID.String(),
			Reference: x.Reference, Title: title, Line: line, AmountKobo: amount})
	}
	return out, nil
}

// approvalAlerts are the approvals waiting for the caller, with who asked.
func (s *Server) approvalAlerts(ctx context.Context, p *Principal) ([]chatAlert, error) {
	if !p.mayDecideAny() {
		return nil, nil
	}
	var out []chatAlert
	err := db0(ctx, s, p.TenantID, func(tx pgx.Tx) error {
		items, raws, err := s.openApprovalsTx(ctx, tx, p)
		if err != nil {
			return err
		}
		for i := range items {
			a := items[i]
			var v any
			if len(raws[i]) > 0 {
				if err := json.Unmarshal(raws[i], &v); err != nil {
					return err
				}
			}
			if a.Subject, err = pii.Open(ctx, s.Store.PII, tx, p.TenantID, v, pii.Taint{}); err != nil {
				return err
			}
			if a.FirstPayment, err = s.firstPayment(ctx, tx, p.TenantID, a.Environment, a.Subject); err != nil {
				return err
			}
			who, err := runAsker(ctx, tx, p.TenantID, a.RunID)
			if err != nil {
				return err
			}
			title := a.StepName
			if title == "" {
				title = a.StepID
			}
			line := a.Workflow
			if who != "" {
				line = "Asked by " + who + " · " + a.Workflow
			}
			out = append(out, chatAlert{Key: "approval:" + a.RunID.String() + "/" + a.StepID, Kind: "approval", At: a.RequestedAt,
				ID: a.RunID.String(), Title: title, Line: line, Approval: &a, Who: who})
		}
		return nil
	})
	return out, err
}

// actorUser is a person as a run records who started it: their id, alone
// or as "user:<id>"; subflowOf, a run started by another run's step.
var (
	actorUser = regexp.MustCompile(`^(?:user:)?([0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12})$`)
	subflowOf = regexp.MustCompile(`^subflow:([0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12})/`)
)

// runAsker is who started a run, as "Tunde (cashier)": a person of the
// business, by their name and role; "" for a workflow, key or stranger.
func runAsker(ctx context.Context, tx pgx.Tx, tenant, run uuid.UUID) (string, error) {
	var by *string
	// A run a step of another started is asked for by whoever started that
	// one (a few levels at most).
	for range 4 {
		by = nil
		if err := tx.QueryRow(ctx, `SELECT started_by FROM runs WHERE id = $1`, run).Scan(&by); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return "", nil
			}
			return "", err
		}
		m := (*regexp.Regexp)(nil)
		if by != nil {
			m = subflowOf
		}
		if m == nil || !m.MatchString(*by) {
			break
		}
		run = uuid.MustParse(m.FindStringSubmatch(*by)[1])
	}
	if by == nil {
		return "", nil
	}
	found := actorUser.FindStringSubmatch(*by)
	if found == nil {
		return "", nil
	}
	id := found[1]
	var name, email, role string
	err := tx.QueryRow(ctx, `SELECT u.name, u.email, m.role FROM users u JOIN memberships m ON m.user_id = u.id AND m.tenant_id = $1
		WHERE u.id = $2 ORDER BY m.created_at LIMIT 1`, tenant, id).Scan(&name, &email, &role)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	if name = strings.TrimSpace(name); name == "" {
		name, _, _ = strings.Cut(email, "@")
	}
	first, _, _ := strings.Cut(name, " ")
	return fmt.Sprintf("%s (%s)", first, strings.ReplaceAll(role, "_", " ")), nil
}

// db0 runs f in the business's own scope.
func db0(ctx context.Context, s *Server, tenant uuid.UUID, f func(pgx.Tx) error) error {
	return db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{tenant}, f)
}

// matchOption is an invoice or order a payment can be matched to.
type matchOption struct {
	// Kind is invoice or order; Number what the match names (the invoice's
	// number, the order's reference).
	Kind     string `json:"kind"`
	Number   string `json:"number"`
	Customer string `json:"customer"`
	DueKobo  int64  `json:"due_kobo"`
}

// getMatchOptions is GET /v1/chat-alerts/match-options: every open invoice
// (not the debtor notebook's entries) and order money can be matched to,
// for Match it; names masked, as the money pages show them unrevealed.
func (s *Server) getMatchOptions(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r.Context())
	if !p.Can(PermInvoiceManage) {
		s.fail(w, r, fmt.Errorf("%w: matching money needs %s", errForbidden, PermInvoiceManage))
		return
	}
	if !s.invoicesOn(w, r) {
		return
	}
	out := []matchOption{}
	if err := s.tx(r, func(tx pgx.Tx) error {
		for _, st := range []string{"overdue", "part_paid", "sent", "recorded"} {
			list, err := s.Invoices.ListOf(r.Context(), tx, p.TenantID, st, "invoice", 500)
			if err != nil {
				return err
			}
			for _, x := range list {
				if x.DueKobo <= 0 {
					continue
				}
				maskInvoice(&x)
				out = append(out, matchOption{Kind: "invoice", Number: x.Number, Customer: x.Customer.Name, DueKobo: x.DueKobo})
			}
		}
		orders, err := s.matcher().OpenOrders(r.Context(), tx, p.TenantID, 200)
		for _, o := range orders {
			out = append(out, matchOption{Kind: "order", Number: o.Number, Customer: maskName(o.Customer), DueKobo: o.DueKobo})
		}
		return err
	}); err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"options": out})
}
