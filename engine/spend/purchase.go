package spend

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/moneylog"
)

// The expense of paying a purchase order (§8, The expense): one money-log
// expense per label on the order, each naming the supplier and the order,
// its category the label's name then and its label_id the label kept.
// A part payment is shared by Split, in proportion to each label's share
// of the order.

// orderLine is what paying reads of a purchase line.
type orderLine struct {
	name, unit string
	quantity   int64
	cost       int64
	treatment  string
	label      *uuid.UUID
}

func (l orderLine) words() string {
	w := strconv.FormatInt(l.quantity, 10)
	if l.unit != "" {
		w += " " + l.unit
	}
	return w + " " + l.name
}

// orderLinesTx reads an order's lines, in their order.
func orderLinesTx(ctx context.Context, tx pgx.Tx, tenant, order uuid.UUID) ([]orderLine, error) {
	rows, err := tx.Query(ctx, `SELECT name, unit, quantity, cost_kobo, treatment, label_id FROM purchase_lines
		WHERE tenant_id = $1 AND purchase_order_id = $2 ORDER BY position`, tenant, order)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (orderLine, error) {
		var l orderLine
		err := r.Scan(&l.name, &l.unit, &l.quantity, &l.cost, &l.treatment, &l.label)
		return l, err
	})
}

// keeper finds the label kept for each label id, once per id.
type keeper struct {
	tenant uuid.UUID
	seen   map[uuid.UUID]Label
}

func (k *keeper) kept(ctx context.Context, tx pgx.Tx, id *uuid.UUID) (*Label, error) {
	if id == nil {
		return nil, nil
	}
	if k.seen == nil {
		k.seen = map[uuid.UUID]Label{}
	}
	l, ok := k.seen[*id]
	if !ok {
		var err error
		if l, err = KeptTx(ctx, tx, k.tenant, *id); err != nil {
			return nil, err
		}
		k.seen[*id] = l
	}
	return &l, nil
}

// labelGroup is the lines of one label on an order.
type labelGroup struct {
	label *Label // nil: Not labelled
	kobo  int64
	words []string
}

// OrderLabelsTx gives the lines added to stock that have no label the
// label Stock bought, found or made (§5.2: the default is applied when the
// order is paid, and kept on the line, so the line shows it from then on
// and reports find the line under it).
func OrderLabelsTx(ctx context.Context, tx pgx.Tx, tenant, order uuid.UUID, actor string) error {
	var bare bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM purchase_lines WHERE tenant_id = $1 AND purchase_order_id = $2
		AND treatment = 'stock' AND label_id IS NULL)`, tenant, order).Scan(&bare); err != nil || !bare {
		return err
	}
	l, _, err := FindOrMakeTx(ctx, tx, tenant, StockBought, actor)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `UPDATE purchase_lines SET label_id = $3 WHERE tenant_id = $1 AND purchase_order_id = $2 AND treatment = 'stock' AND label_id IS NULL`,
		tenant, order, l.ID)
	return err
}

// orderGroupsTx is an order's lines by the label kept for each, in the
// order each label first appears; an order with no lines is one group,
// Not labelled.
func orderGroupsTx(ctx context.Context, tx pgx.Tx, tenant, order uuid.UUID) ([]labelGroup, error) {
	lines, err := orderLinesTx(ctx, tx, tenant, order)
	if err != nil {
		return nil, err
	}
	k := keeper{tenant: tenant}
	var groups []labelGroup
	for _, l := range lines {
		kept, err := k.kept(ctx, tx, l.label)
		if err != nil {
			return nil, err
		}
		i := 0
		for i < len(groups) && !sameKept(groups[i].label, kept) {
			i++
		}
		if i == len(groups) {
			groups = append(groups, labelGroup{label: kept})
		}
		groups[i].kobo += l.quantity * l.cost
		groups[i].words = append(groups[i].words, l.words())
	}
	if len(groups) == 0 {
		groups = []labelGroup{{}}
	}
	return groups, nil
}

func sameKept(a, b *Label) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return a.ID == b.ID
}

// PurchasePay is a payment of a purchase order whose expense is written.
type PurchasePay struct {
	Order     uuid.UUID
	Reference string // the order's, P-7QX4KD
	Supplier  string
	// AmountKobo is what was paid; How cash or transfer
	// (purchase_payments.paid_how); Channel where the money moved (cash
	// or bank).
	AmountKobo int64
	How        string
	Channel    string
	// Key makes the payment once: the first entry's reference is
	// purchase:<key>, the others purchase:<key>/2, /3 ...
	Key   string
	Actor string
	When  time.Time
}

// Paid is one expense written for a payment.
type Paid struct {
	EntryID uuid.UUID  `json:"entry_id"`
	LabelID *uuid.UUID `json:"label_id,omitempty"`
	Kobo    int64      `json:"amount_kobo"`
}

// RecordPurchaseTx writes the expenses of a payment of an order, one per
// label on it (Not labelled lines together, as one with no label), each
// with its purchase_payments row; the parts add up exactly to what was
// paid, and a part that comes to no kobo writes nothing. fresh is false
// when anything was recorded under the payment's key already for this
// order (any part, in any channel, however the order is labelled now):
// nothing is written again. A key that paid something else (another
// order) is refused with ErrKeyUsed: the payment would otherwise be
// dropped while looking made (PR review fix).
func RecordPurchaseTx(ctx context.Context, tx pgx.Tx, log *moneylog.Log, tenant uuid.UUID, in PurchasePay) (paid []Paid, fresh bool, err error) {
	if log == nil || in.Key == "" || in.AmountKobo <= 0 {
		return nil, false, errors.New("spend: a payment needs the money log, a key and an amount")
	}
	// The parts' references follow the order's labels as they are now: an
	// order relabelled since the key was first paid groups its lines
	// otherwise, and a part not written before would pass as new. So a key
	// with anything written under it, in any part or channel, is paid.
	var done bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM money_entries WHERE tenant_id = $1 AND channel IN ('cash', 'bank') AND reference = ANY ($2))`,
		tenant, partRefs(in.Key)).Scan(&done); err != nil {
		return nil, false, err
	}
	if done {
		var ours bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM purchase_payments p JOIN money_entries e ON e.tenant_id = p.tenant_id AND e.id = p.entry_id
			WHERE p.tenant_id = $1 AND p.purchase_id = $2 AND e.reference = ANY ($3))`, tenant, in.Order, partRefs(in.Key)).Scan(&ours); err != nil {
			return nil, false, err
		}
		if !ours {
			return nil, false, ErrKeyUsed
		}
		return nil, false, nil
	}
	if err := OrderLabelsTx(ctx, tx, tenant, in.Order, in.Actor); err != nil {
		return nil, false, err
	}
	groups, err := orderGroupsTx(ctx, tx, tenant, in.Order)
	if err != nil {
		return nil, false, err
	}
	weights := make([]Part, len(groups))
	for i, g := range groups {
		weights[i].Kobo = g.kobo
	}
	shares := Split(in.AmountKobo, weights)
	n := 0
	for i, g := range groups {
		amount := shares[i].Kobo
		if amount <= 0 {
			continue
		}
		n++
		ref := "purchase:" + in.Key
		if n > 1 {
			ref += "/" + strconv.Itoa(n)
		}
		e := moneylog.Entry{Direction: "out", Kind: "expense", Status: "settled", Channel: in.Channel, Reference: ref, AmountKobo: &amount,
			OccurredAt: &in.When, Source: "entered", Vendor: in.Supplier, EnteredBy: in.Actor,
			Description: cut(fmt.Sprintf("Order %s: %s", in.Reference, strings.Join(g.words, ", ")), 300)}
		if g.label != nil {
			e.LabelID, e.Category = &g.label.ID, g.label.Name
		}
		id, made, err := log.RecordTx(ctx, tx, tenant, e)
		if err != nil {
			return nil, false, err
		}
		paid = append(paid, Paid{EntryID: id, LabelID: e.LabelID, Kobo: amount})
		if !made {
			continue
		}
		fresh = true
		if _, err := tx.Exec(ctx, `INSERT INTO purchase_payments (id, tenant_id, purchase_id, entry_id, amount_kobo, paid_how, paid_by) VALUES ($1, $2, $3, $4, $5, $6, $7)`,
			uuid.Must(uuid.NewV7()), tenant, in.Order, id, amount, in.How, in.Actor); err != nil {
			return nil, false, err
		}
	}
	return paid, fresh, nil
}

// partRefs is every reference a payment's parts may have: purchase:<key>,
// then /2 to /50 (an order has at most 50 lines, so as many labels).
func partRefs(key string) []string {
	refs := []string{"purchase:" + key}
	for n := 2; n <= 50; n++ {
		refs = append(refs, "purchase:"+key+"/"+strconv.Itoa(n))
	}
	return refs
}

// EntryPartsTx is what a purchase's expense bought, by treatment: the
// entry is found by its purchase_payments row, and its amount is shared
// among the treatments of its order's lines under the same label (the
// label kept, now), in proportion to their cost. ok is false for an
// entry that pays no purchase. An entry whose label no line has now (the
// lines were labelled again since) is shared by the whole order.
func EntryPartsTx(ctx context.Context, tx pgx.Tx, tenant, entry uuid.UUID, label *uuid.UUID, amount int64) (parts []Part, ok bool, err error) {
	var order uuid.UUID
	err = tx.QueryRow(ctx, `SELECT purchase_id FROM purchase_payments WHERE tenant_id = $1 AND entry_id = $2`, tenant, entry).Scan(&order)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	lines, err := orderLinesTx(ctx, tx, tenant, order)
	if err != nil {
		return nil, false, err
	}
	k := keeper{tenant: tenant}
	want, err := k.kept(ctx, tx, label)
	if err != nil && !errors.Is(err, ErrNoLabel) {
		return nil, false, err
	}
	var mine, all []Part
	for _, l := range lines {
		kept, err := k.kept(ctx, tx, l.label)
		if err != nil {
			return nil, false, err
		}
		all = add(all, label, l.treatment, l.quantity*l.cost)
		if sameKept(kept, want) {
			mine = add(mine, label, l.treatment, l.quantity*l.cost)
		}
	}
	if len(mine) == 0 {
		mine = all
	}
	if len(mine) == 0 {
		mine = []Part{{LabelID: label, Treatment: Record}}
	}
	return Split(amount, mine), true, nil
}
