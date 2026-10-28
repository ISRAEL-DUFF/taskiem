package matching

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/invoicing"
	"github.com/israel-duff/taskiem/engine/moneylog"
	"github.com/israel-duff/taskiem/engine/storefront"
)

// Result is what a payment was matched to: an invoice or an order.
type Result struct {
	Invoice *invoicing.Invoice
	Order   *storefront.Order
}

// Target names it as money_matches does: invoice:INV-0104, order:AK-7QX4KD.
func (r Result) Target() string {
	switch {
	case r.Invoice != nil:
		return TargetInvoice + ":" + r.Invoice.Number
	case r.Order != nil:
		return TargetOrder + ":" + r.Order.Reference
	}
	return ""
}

// orderErr says a storefront refusal as matching's (invoicing's) errors,
// which the API and chat already word.
func orderErr(err error) error {
	trim := func(e error) string {
		msg := err.Error()
		for _, p := range []string{e.Error() + ": ", e.Error()} {
			msg = strings.Replace(msg, p, "", 1)
		}
		return msg
	}
	switch {
	case errors.Is(err, storefront.ErrDecision):
		return fmt.Errorf("%w: %s", invoicing.ErrState, trim(storefront.ErrDecision))
	case errors.Is(err, storefront.ErrInvalid):
		return fmt.Errorf("%w: %s", invoicing.ErrInvalid, trim(storefront.ErrInvalid))
	case errors.Is(err, storefront.ErrNoOrder):
		return fmt.Errorf("%w: no such order", invoicing.ErrNotFound)
	}
	return err
}

func (m *Matcher) orders() (*storefront.Orders, error) {
	if m.Orders == nil {
		return nil, fmt.Errorf("%w: storefront orders are not available on this server", invoicing.ErrState)
	}
	return m.Orders, nil
}

// exactOrder counts a payment through an order's checkout page, or into
// the account number given for it (account), towards the order, along
// the path its own payment check takes, once.
func (m *Matcher) exactOrder(ctx context.Context, tenant uuid.UUID, e entry, order string, account *uuid.UUID) error {
	if e.AmountKobo <= 0 {
		return m.mark(ctx, tenant, e.ID, "unmatched")
	}
	reason := "paid through the order's checkout page"
	if account != nil {
		reason = "paid into the account number given for this order"
	}
	var c storefront.Counted
	err := db.InTenantTx(ctx, m.Invoices.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		var err error
		c, err = m.Orders.CountPayment(ctx, tx, tenant, order, storefront.PaymentIn{EntryID: &e.ID, AmountKobo: e.AmountKobo, How: "exact",
			By: "system:" + e.Channel, Reasons: []string{reason}, Account: account})
		if err != nil {
			return err
		}
		return matched(ctx, tx, tenant, e.ID, m.now())
	})
	if errors.Is(err, storefront.ErrDecision) || errors.Is(err, storefront.ErrInvalid) {
		// Paid by other money, cancelled or expired, or too little: a
		// person decides.
		m.log().Info("matching: exact order match refused", "tenant", tenant, "entry", e.ID, "err", err)
		return m.mark(ctx, tenant, e.ID, "unmatched")
	}
	if err != nil {
		return err
	}
	return m.Orders.Paid(ctx, tenant, c)
}

// matched marks an entry decided: its open suggestions close.
func matched(ctx context.Context, tx pgx.Tx, tenant, entryID uuid.UUID, now time.Time) error {
	if _, err := tx.Exec(ctx, `UPDATE money_suggestions SET status = 'closed', decided_at = $3 WHERE tenant_id = $1 AND entry_id = $2 AND status = 'open'`,
		tenant, entryID, now); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `INSERT INTO money_match_state (entry_id, tenant_id, state, considered_at) VALUES ($1, $2, 'matched', $3)
		ON CONFLICT (entry_id) DO UPDATE SET state = 'matched', considered_at = $3, held_reason = NULL`, entryID, tenant, now)
	return err
}

// Part is one part of a payment (a split, docs/spec/schools.md 7.2): an
// amount counted towards an invoice (its id or number) or a storefront
// order (its reference). Each part is a match of its own, undone on its
// own.
type Part struct {
	Invoice    string `json:"invoice,omitempty"`
	Order      string `json:"order,omitempty"`
	AmountKobo int64  `json:"amount_kobo"`
}

// MaxParts is the most parts one payment is split into.
const MaxParts = 20

// Split is what matching a payment in parts did: each part's invoice or
// order, what is left of the payment to match, and what of it was left as
// the customer's credit.
type Split struct {
	Parts []Result
	// PartKobo is what each part counts, in the order of Parts.
	PartKobo   []int64
	LeftKobo   int64
	CreditKobo int64
}

// SplitReason is the reason a part of a split gives, beside the
// suggestion's own for the invoice suggested; SplitSiblingReason the one
// a part for a sibling's invoice gives, where the school shares family
// credit (invoicing.SharedWith).
const (
	SplitReason        = "part of one payment split across the customer's open invoices"
	SplitSiblingReason = "part of one payment split across the family's open invoices, for a sibling (the school shares family credit)"
)

// counting is a match a person or matching asks for.
type counting struct {
	entry      uuid.UUID
	suggestion *uuid.UUID
	// parts: a single part of 0 is the rest of the payment.
	parts      []Part
	how, by    string
	reasons    []string
	creditRest bool
	// approved: a second person approved it (ApproveHeld); it is not
	// held again.
	approved bool
}

// count counts an entry in parts, in one transaction: each part towards
// its invoice or order (a further part towards an invoice the entry
// already counts towards adds to it), the suggestion confirmed, the rest
// left as the customer's credit or for a person, the payer remembered for
// the invoices' and orders' customers when a person decided. A run
// waiting on an invoice it paid is told after it commits, and an order's
// paid flow runs.
func (m *Matcher) count(ctx context.Context, tenant uuid.UUID, c counting) (Split, error) {
	var out Split
	var done []counted
	var held *Held
	err := db.InTenantTx(ctx, m.Invoices.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		var err error
		out, done, held, err = m.countTx(ctx, tx, tenant, c)
		return err
	})
	if err != nil {
		return out, err
	}
	if held != nil {
		// The request commits; nothing was counted.
		return out, held
	}
	return out, m.paid(ctx, tenant, done)
}

// countTx is count in tx: what it counted, for paid once tx commits, or
// the request recorded when the match waits for a second person.
func (m *Matcher) countTx(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, c counting) (out Split, done []counted, held *Held, err error) {
	err = func() error {
		parts, reasons := c.parts, c.reasons
		var sugInvoice, sugOrder *uuid.UUID
		if c.suggestion != nil {
			var status string
			var raw []byte
			err := tx.QueryRow(ctx, `SELECT s.invoice_id, s.order_id, s.entry_id, s.status, s.reasons, COALESCE(o.reference, '')
				FROM money_suggestions s LEFT JOIN storefront_orders o ON o.id = s.order_id WHERE s.tenant_id = $1 AND s.id = $2 FOR UPDATE OF s`, tenant, *c.suggestion).
				Scan(&sugInvoice, &sugOrder, &c.entry, &status, &raw, new(string))
			if errors.Is(err, pgx.ErrNoRows) {
				return invoicing.ErrNotFound
			}
			if err != nil {
				return err
			}
			if status != "open" {
				return fmt.Errorf("%w: this suggestion was %s", invoicing.ErrState, status)
			}
			_ = json.Unmarshal(raw, &reasons)
			if len(parts) == 0 {
				p := Part{}
				if sugInvoice != nil {
					p.Invoice = sugInvoice.String()
				} else {
					if err := tx.QueryRow(ctx, `SELECT reference FROM storefront_orders WHERE tenant_id = $1 AND id = $2`, tenant, *sugOrder).Scan(&p.Order); err != nil {
						return err
					}
				}
				parts = []Part{p}
			}
		}
		var direction, status, channel, reference, source string
		var amount *int64
		err := tx.QueryRow(ctx, `SELECT direction, status, amount_kobo, channel, reference, source FROM money_entries WHERE tenant_id = $1 AND id = $2`, tenant, c.entry).
			Scan(&direction, &status, &amount, &channel, &reference, &source)
		if errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("%w: no such payment", invoicing.ErrNotFound)
		}
		if err != nil {
			return err
		}
		if direction != "in" || status != "settled" || amount == nil || *amount <= 0 {
			return fmt.Errorf("%w: only money received and settled, with its amount, can be matched to an invoice or order", invoicing.ErrState)
		}
		left, _, err := moneylog.LockLeft(ctx, tx, c.entry)
		if err != nil {
			return err
		}
		if err := checkParts(parts, left); err != nil {
			return err
		}
		// A part towards an invoice the entry already counts towards adds
		// to it (a person splitting again), except the rest with nothing
		// left: the same match again is then a no-op, and towards another
		// invoice or order it is refused with what it counts towards.
		add := true
		if len(parts) == 1 && parts[0].AmountKobo == 0 {
			add = left > 0
			parts = []Part{{Invoice: parts[0].Invoice, Order: parts[0].Order, AmountKobo: max(left, 1)}}
		}
		if !c.approved {
			// Money a person entered, towards a school's invoice at or
			// above its cash threshold: recorded for a second person, and
			// nothing counted (approval.go).
			if held, err = m.holdMatch(ctx, tx, tenant, c, source, *amount, parts); err != nil || held != nil {
				return err
			}
		}
		// The customer whose money this is, when the rest is left as
		// credit: the suggested invoice's, else the first part's. A part
		// for a sibling's invoice is allowed where the school shares
		// family credit (invoicing.SharedWith), as the exact path counts.
		var primary string
		if sugInvoice != nil {
			x, err := m.Invoices.Get(ctx, tx, tenant, sugInvoice.String())
			if err != nil {
				return err
			}
			primary = x.CustomerID()
		}
		var shared []string
		sharedWith := func() ([]string, error) {
			if shared != nil || primary == "" {
				return shared, nil
			}
			var err error
			if shared, err = invoicing.SharedWith(ctx, tx, tenant, primary); err != nil {
				return nil, err
			}
			if shared == nil {
				shared = []string{}
			}
			return shared, nil
		}
		var customers, payees []string
		var sugPayment, sugOrderPayment *uuid.UUID
		for _, p := range parts {
			why := reasons
			if len(parts) > 1 && c.suggestion != nil && !p.isSuggested(ctx, tx, tenant, sugInvoice, sugOrder) {
				why = []string{SplitReason}
				if p.Invoice != "" && primary != "" {
					y, err := m.Invoices.Get(ctx, tx, tenant, p.Invoice)
					if err != nil {
						return err
					}
					if y.CustomerID() != primary {
						sib, err := sharedWith()
						if err != nil {
							return err
						}
						if slices.Contains(sib, y.CustomerID()) {
							why = []string{SplitSiblingReason}
						}
					}
				}
			}
			k, err := m.countPart(ctx, tx, tenant, p, invoicing.PaymentIn{AmountKobo: p.AmountKobo, PaymentID: reference,
				EntryID: &c.entry, Connection: channel, How: c.how, By: c.by, Reasons: why, Add: add})
			if err != nil {
				return err
			}
			done = append(done, k)
			out.Parts, out.PartKobo = append(out.Parts, k.Result), append(out.PartKobo, p.AmountKobo)
			if o := k.Order; o != nil {
				payees = append(payees, o.CustomerID())
				if sugOrder != nil && *sugOrder == o.ID && k.paymentID != uuid.Nil {
					id := k.paymentID
					sugOrderPayment = &id
				}
				continue
			}
			x := k.Invoice
			customers = append(customers, x.CustomerID())
			payees = append(payees, x.CustomerID())
			if primary == "" {
				primary = x.CustomerID()
			}
			if sugInvoice != nil && *sugInvoice == x.ID {
				pay := k.paymentID
				if pay == uuid.Nil {
					// Counted already: the part counting it.
					if err := tx.QueryRow(ctx, `SELECT id FROM customer_invoice_payments WHERE tenant_id = $1 AND entry_id = $2 AND invoice_id = $3 AND undoes IS NULL
						ORDER BY created_at DESC LIMIT 1`, tenant, c.entry, x.ID).Scan(&pay); err != nil {
						return err
					}
				}
				sugPayment = &pay
			}
		}
		now := m.now()
		if c.suggestion != nil {
			if sugPayment == nil && sugOrderPayment == nil && sugInvoice != nil {
				// The suggested invoice took no part: the suggestion led
				// to the first part.
				if err := tx.QueryRow(ctx, `SELECT id FROM customer_invoice_payments WHERE tenant_id = $1 AND entry_id = $2 AND undoes IS NULL
					ORDER BY created_at DESC LIMIT 1`, tenant, c.entry).Scan(&sugPayment); err != nil && !errors.Is(err, pgx.ErrNoRows) {
					return err
				}
			}
			if _, err := tx.Exec(ctx, `UPDATE money_suggestions SET status = 'confirmed', decided_by = $3, decided_at = $4, payment_id = $5, order_payment_id = $6
				WHERE tenant_id = $1 AND id = $2`, tenant, *c.suggestion, c.by, now, sugPayment, sugOrderPayment); err != nil {
				return err
			}
		}
		rest, _, err := moneylog.EntryLeft(ctx, tx, c.entry)
		if err != nil {
			return err
		}
		if rest > 0 && c.creditRest {
			// Every part one customer's invoices, or their siblings'
			// where the school shares family credit: the rest is the
			// customer's (the primary's) credit.
			if len(customers) != len(parts) || primary == "" {
				return fmt.Errorf("%w: the rest can be left as credit only when every part is for one customer's invoices", invoicing.ErrInvalid)
			}
			for _, x := range customers {
				if x == primary {
					continue
				}
				sib, err := sharedWith()
				if err != nil {
					return err
				}
				if !slices.Contains(sib, x) {
					return fmt.Errorf("%w: the rest can be left as credit only when every part is for one customer's invoices", invoicing.ErrInvalid)
				}
			}
			if err := invoicing.MarkCustomers(ctx, tx, tenant, c.entry, primary, nil, "person", c.by); err != nil {
				return err
			}
			out.CreditKobo = rest
		} else {
			out.LeftKobo = max(rest, 0)
		}
		if c.how == "suggested" || c.how == "manual" {
			if err := m.remember(ctx, tx, tenant, c.entry, payees, c.by); err != nil {
				return err
			}
		}
		if out.LeftKobo == 0 {
			return matched(ctx, tx, tenant, c.entry, now)
		}
		// The rest waits for a person.
		if _, err := tx.Exec(ctx, `UPDATE money_suggestions SET status = 'closed', decided_at = $3 WHERE tenant_id = $1 AND entry_id = $2 AND status = 'open'`,
			tenant, c.entry, now); err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `INSERT INTO money_match_state (entry_id, tenant_id, state, considered_at) VALUES ($1, $2, 'unmatched', $3)
			ON CONFLICT (entry_id) DO UPDATE SET state = 'unmatched', considered_at = $3, held_reason = NULL`, c.entry, tenant, now)
		return err
	}()
	return out, done, held, err
}

// counted is a part counted towards an invoice or an order.
type counted struct {
	Result
	// paymentID is the payment row written; uuid.Nil when none was (the
	// entry counted towards it already).
	paymentID uuid.UUID
	// became: the payment made the invoice paid.
	became bool
	// order is what counting towards the order did.
	order storefront.Counted
}

// countPart counts a part of a payment towards its invoice or order,
// along the target's own CountPayment (both moneylog.Parts.Count
// underneath), in the caller's transaction. Pass what it returns to paid
// once the transaction commits.
func (m *Matcher) countPart(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, p Part, in invoicing.PaymentIn) (counted, error) {
	if p.Order != "" {
		orders, err := m.orders()
		if err != nil {
			return counted{}, err
		}
		k, err := orders.CountPayment(ctx, tx, tenant, p.Order, storefront.PaymentIn{EntryID: in.EntryID, AmountKobo: in.AmountKobo, How: in.How, By: in.By, Reasons: in.Reasons, Add: in.Add})
		if err != nil {
			return counted{}, orderErr(err)
		}
		o := k.Order
		return counted{Result: Result{Order: &o}, paymentID: k.PaymentID, order: k}, nil
	}
	k, err := m.Invoices.CountPayment(ctx, tx, tenant, p.Invoice, in)
	if err != nil {
		return counted{}, err
	}
	x := k.Invoice
	return counted{Result: Result{Invoice: &x}, paymentID: k.PaymentID, became: k.Became}, nil
}

// paid runs what the parts' targets do on being paid, after the
// transaction that counted them commits: a run waiting on an invoice a
// part paid is told, then each order's paid flow runs.
func (m *Matcher) paid(ctx context.Context, tenant uuid.UUID, done []counted) error {
	for _, k := range done {
		if k.Invoice != nil && k.became {
			if err := m.Invoices.Paid(ctx, tenant, *k.Invoice); err != nil {
				return err
			}
		}
	}
	for _, k := range done {
		if k.Order != nil {
			if err := m.Orders.Paid(ctx, tenant, k.order); err != nil {
				return err
			}
		}
	}
	return nil
}

// isSuggested reports whether a part is for the suggestion's own invoice
// or order.
func (p Part) isSuggested(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, invoice, order *uuid.UUID) bool {
	var id uuid.UUID
	var err error
	switch {
	case p.Order != "" && order != nil:
		err = tx.QueryRow(ctx, `SELECT id FROM storefront_orders WHERE tenant_id = $1 AND reference = $2`, tenant, p.Order).Scan(&id)
		return err == nil && id == *order
	case p.Invoice != "" && invoice != nil:
		if u, perr := uuid.Parse(p.Invoice); perr == nil {
			return u == *invoice
		}
		err = tx.QueryRow(ctx, `SELECT id FROM customer_invoices WHERE tenant_id = $1 AND number = $2`, tenant, p.Invoice).Scan(&id)
		return err == nil && id == *invoice
	}
	return false
}

// checkParts refuses parts that cannot be counted: none or too many, a
// part naming no invoice or order or both, an amount below 1 kobo (but a
// single part of 0, the rest), one target twice in one request (give it
// one amount; a further part later adds to the first), or more than is
// left.
func checkParts(parts []Part, left int64) error {
	if len(parts) == 0 || len(parts) > MaxParts {
		return fmt.Errorf("%w: a payment is matched in 1 to %d parts", invoicing.ErrInvalid, MaxParts)
	}
	seen := map[string]bool{}
	var sum int64
	for i, p := range parts {
		if (p.Invoice == "") == (p.Order == "") {
			return fmt.Errorf("%w: part %d names an invoice or an order", invoicing.ErrInvalid, i+1)
		}
		if p.AmountKobo < 0 || p.AmountKobo == 0 && len(parts) > 1 {
			return fmt.Errorf("%w: part %d needs an amount", invoicing.ErrInvalid, i+1)
		}
		key := "invoice:" + p.Invoice
		if p.Order != "" {
			key = "order:" + p.Order
		}
		if seen[key] {
			return fmt.Errorf("%w: part %d names the same invoice or order as another", invoicing.ErrInvalid, i+1)
		}
		seen[key] = true
		sum += p.AmountKobo
	}
	if len(parts) > 1 && sum > left {
		return fmt.Errorf("%w: the parts add up to %s, more than the %s left of this payment", invoicing.ErrInvalid, invoicing.Naira(sum), invoicing.Naira(max(left, 0)))
	}
	return nil
}

func first(s Split) Result {
	if len(s.Parts) == 0 {
		return Result{}
	}
	return s.Parts[0]
}

// Confirm makes a suggestion a match: the rest of the payment (all of it,
// unless part was matched elsewhere) counts towards its invoice or order,
// the entry's other suggestions close, and a run waiting on the invoice is
// told when it becomes paid (an order's paid flow runs). auto records it
// as matched without asking; a person's confirmation remembers the payer.
func (m *Matcher) Confirm(ctx context.Context, tenant, suggestion uuid.UUID, by string, auto bool) (Result, error) {
	how := "suggested"
	if auto {
		how = "suggested_auto"
	}
	s, err := m.count(ctx, tenant, counting{suggestion: &suggestion, how: how, by: by})
	return first(s), err
}

// ConfirmParts confirms a suggestion with the payment split in parts
// across invoices and orders (the suggested one among them, or not);
// creditRest leaves what the parts do not count as the customer's credit
// (every part must then be for one customer's invoices), else it waits for
// a person.
func (m *Matcher) ConfirmParts(ctx context.Context, tenant, suggestion uuid.UUID, parts []Part, creditRest bool, by string) (Split, error) {
	if len(parts) == 0 {
		return Split{}, fmt.Errorf("%w: give the parts", invoicing.ErrInvalid)
	}
	return m.count(ctx, tenant, counting{suggestion: &suggestion, parts: parts, how: "suggested", by: by, creditRest: creditRest})
}

// ConfirmSplit confirms a suggestion split as Propose offers it in chat:
// across the customer's open invoices oldest first (then their siblings',
// where the school shares family credit), the rest left as their credit.
func (m *Matcher) ConfirmSplit(ctx context.Context, tenant, suggestion uuid.UUID, by string) (Split, error) {
	var p Proposal
	err := db.InTenantTx(ctx, m.Invoices.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		var err error
		p, err = m.Propose(ctx, tx, tenant, suggestion, false)
		return err
	})
	if err != nil {
		return Split{}, err
	}
	var parts []Part
	for _, x := range p.Parts {
		if x.AmountKobo > 0 {
			parts = append(parts, x.Part)
		}
	}
	if len(parts) == 0 {
		s, err := m.count(ctx, tenant, counting{suggestion: &suggestion, how: "suggested", by: by})
		return s, err
	}
	return m.count(ctx, tenant, counting{suggestion: &suggestion, parts: parts, how: "suggested", by: by, creditRest: p.CreditKobo > 0})
}

// Match counts a money-in entry, or what is left of it, towards an
// invoice (its id or number) or an order ("order:<reference>") a person
// chose (how manual), closing the entry's open suggestions and remembering
// the payer.
func (m *Matcher) Match(ctx context.Context, tenant, entryID uuid.UUID, target, by string) (Result, error) {
	p := Part{Invoice: target}
	if order, ok := strings.CutPrefix(target, TargetOrder+":"); ok {
		p = Part{Order: order}
	}
	s, err := m.count(ctx, tenant, counting{entry: entryID, parts: []Part{p}, how: "manual", by: by, reasons: []string{"matched by hand"}})
	return first(s), err
}

// MatchParts counts a money-in entry in parts a person chose, as
// ConfirmParts does a suggestion.
func (m *Matcher) MatchParts(ctx context.Context, tenant, entryID uuid.UUID, parts []Part, creditRest bool, by string) (Split, error) {
	if len(parts) == 0 {
		return Split{}, fmt.Errorf("%w: give the parts", invoicing.ErrInvalid)
	}
	return m.count(ctx, tenant, counting{entry: entryID, parts: parts, how: "manual", by: by, reasons: []string{"matched by hand"}, creditRest: creditRest})
}

// ProposedPart is a part Propose offers, with its invoice's number and
// what is due on it; an amount of 0 is an invoice the person may still
// give part of the payment to. Sibling marks a sibling's invoice, where
// the school shares family credit.
type ProposedPart struct {
	Part
	Number  string `json:"number"`
	DueKobo int64  `json:"due_kobo"`
	Sibling bool   `json:"sibling,omitempty"`
}

// Proposal is how a payment could be split across a customer's open
// invoices (and their siblings', where the school shares family credit):
// each as much as is due, in order, until the money runs out, the rest
// of the invoices listed with 0 to choose from; what is left over would
// be the customer's credit. Over is true when the payment is more than
// is due on the suggested invoice alone (chat offers the split only then).
type Proposal struct {
	Parts      []ProposedPart `json:"parts"`
	CreditKobo int64          `json:"credit_kobo"`
	Over       bool           `json:"over"`
}

// Propose offers a split for a suggestion of an invoice whenever there is
// something to split it across: the customer has another open invoice
// (or a sibling has, where the school shares family credit), or the
// payment is more than is due on the suggested one (Over). The parts are
// the customer's open invoices oldest first, the suggested one first
// instead when suggestedFirst (To match), then the siblings' oldest
// first, each given as much as is due while the money lasts. No parts
// for an order, or an invoice alone that the payment does not exceed.
func (m *Matcher) Propose(ctx context.Context, tx pgx.Tx, tenant, suggestion uuid.UUID, suggestedFirst bool) (Proposal, error) {
	var p Proposal
	var invoice *uuid.UUID
	var entry uuid.UUID
	err := tx.QueryRow(ctx, `SELECT invoice_id, entry_id FROM money_suggestions WHERE tenant_id = $1 AND id = $2`, tenant, suggestion).Scan(&invoice, &entry)
	if errors.Is(err, pgx.ErrNoRows) {
		return p, invoicing.ErrNotFound
	}
	if err != nil || invoice == nil {
		return p, err
	}
	left, ok, err := moneylog.EntryLeft(ctx, tx, entry)
	if err != nil || !ok || left <= 0 {
		return p, err
	}
	x, err := m.Invoices.Get(ctx, tx, tenant, invoice.String())
	if err != nil {
		return p, err
	}
	p.Over = left > x.DueKobo
	if x.CustomerID() == "" {
		return p, nil
	}
	list, err := m.Invoices.OpenInvoicesOf(ctx, tx, tenant, x.CustomerID())
	if err != nil {
		return p, err
	}
	if suggestedFirst {
		for i, y := range list {
			if y.ID == x.ID && i > 0 {
				list = append([]invoicing.Invoice{y}, append(list[:i:i], list[i+1:]...)...)
				break
			}
		}
	}
	type candidate struct {
		invoicing.Invoice
		sibling bool
	}
	all := make([]candidate, 0, len(list))
	for _, y := range list {
		all = append(all, candidate{Invoice: y})
	}
	shared, err := invoicing.SharedWith(ctx, tx, tenant, x.CustomerID())
	if err != nil {
		return p, err
	}
	for _, sib := range shared {
		list, err := m.Invoices.OpenInvoicesOf(ctx, tx, tenant, sib)
		if err != nil {
			return p, err
		}
		for _, y := range list {
			all = append(all, candidate{Invoice: y, sibling: true})
		}
	}
	if len(all) == 0 || len(all) == 1 && !p.Over {
		return p, nil
	}
	for _, y := range all {
		part := max(min(left, y.DueKobo), 0)
		p.Parts = append(p.Parts, ProposedPart{Part: Part{Invoice: y.Number, AmountKobo: part}, Number: y.Number, DueKobo: y.DueKobo, Sibling: y.sibling})
		left -= part
	}
	p.CreditKobo = max(left, 0)
	return p, nil
}

// Reject turns a suggestion down: it is not offered again.
func (m *Matcher) Reject(ctx context.Context, tenant, suggestion uuid.UUID, by string) error {
	return db.InTenantTx(ctx, m.Invoices.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		var entryID uuid.UUID
		var status string
		err := tx.QueryRow(ctx, `SELECT entry_id, status FROM money_suggestions WHERE tenant_id = $1 AND id = $2 FOR UPDATE`, tenant, suggestion).Scan(&entryID, &status)
		if errors.Is(err, pgx.ErrNoRows) {
			return invoicing.ErrNotFound
		}
		if err != nil {
			return err
		}
		if status != "open" {
			return fmt.Errorf("%w: this suggestion was %s", invoicing.ErrState, status)
		}
		now := m.now()
		if _, err := tx.Exec(ctx, `UPDATE money_suggestions SET status = 'rejected', decided_by = $3, decided_at = $4 WHERE tenant_id = $1 AND id = $2`,
			tenant, suggestion, by, now); err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `UPDATE money_match_state SET state = 'unmatched', considered_at = $3 WHERE tenant_id = $1 AND entry_id = $2
			AND NOT EXISTS (SELECT 1 FROM money_suggestions s WHERE s.entry_id = $2 AND s.status = 'open')`, tenant, entryID, now)
		return err
	})
}

// Open lists the suggestions waiting for a person, newest payment first,
// with the payment's details and what is due on the invoice or order.
// Customer is left for the caller to fill (and mask).
func Open(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, limit int) ([]Waiting, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := tx.Query(ctx, `SELECT s.id, s.entry_id, s.invoice_id, s.order_id, COALESCE(i.number, o.reference),
			CASE WHEN i.id IS NOT NULL THEN i.total_kobo - i.paid_kobo ELSE COALESCE(o.deposit_kobo, o.total_kobo) END,
			s.score, s.reasons, e.amount_kobo, taskiem_money_entry_left(e.id), e.channel, e.reference, COALESCE(e.occurred_at, e.recorded_at), e.kind
		FROM money_suggestions s JOIN money_entries e ON e.id = s.entry_id
			LEFT JOIN customer_invoices i ON i.id = s.invoice_id LEFT JOIN storefront_orders o ON o.id = s.order_id
		WHERE s.tenant_id = $1 AND s.status = 'open' ORDER BY e.recorded_at DESC, s.score DESC LIMIT $2`, tenant, limit)
	if err != nil {
		return nil, err
	}
	out, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (Waiting, error) {
		var w Waiting
		var raw []byte
		err := r.Scan(&w.ID, &w.EntryID, &w.InvoiceID, &w.OrderID, &w.Number, &w.DueKobo, &w.Score, &raw, &w.AmountKobo, &w.LeftKobo, &w.Channel, &w.Reference, &w.When, &w.Kind)
		_ = json.Unmarshal(raw, &w.Reasons)
		w.Status, w.TargetKind = "open", TargetInvoice
		if w.OrderID != nil {
			w.TargetKind = TargetOrder
		}
		w.Target = w.TargetKind + ":" + w.Number
		return w, err
	})
	if out == nil {
		out = []Waiting{}
	}
	return out, err
}

// Waiting is an open suggestion with the payment it is for.
type Waiting struct {
	Suggestion
	// DueKobo is what is due on the invoice, or paid online for the order.
	DueKobo int64 `json:"due_kobo"`
	// Customer is the invoice's or order's customer's name (masked by the
	// API unless revealed).
	Customer   string `json:"customer,omitempty"`
	AmountKobo *int64 `json:"amount_kobo"`
	// LeftKobo is what is left of the payment to match.
	LeftKobo  *int64    `json:"left_kobo"`
	Channel   string    `json:"channel"`
	Reference string    `json:"reference"`
	When      time.Time `json:"received_at"`
	Kind      string    `json:"kind"`
	// Split is how the payment could be split across the customer's open
	// invoices (the suggested one first, then oldest first, then
	// siblings' where the school shares family credit), offered whenever
	// there is more than one, or the payment is more than is due on the
	// suggested one (filled by the caller: Matcher.Propose).
	Split *Proposal `json:"split,omitempty"`
}

// OrderTarget is how Match names an order: "order:AK-7QX4KD".
func OrderTarget(reference string) string { return TargetOrder + ":" + reference }

// UndoOrderPayment undoes a payment counted towards an order (a wrong
// match): the order goes back to unpaid when that payment paid it, and is
// refused when the order went on to be fulfilled (storefront.Orders.UndoPayment).
// The money goes back to be matched (invoicing.ReopenPart): the
// suggestion that led to the match is not offered again, the payment's
// other suggestions are; the other parts of a split stay.
func (m *Matcher) UndoOrderPayment(ctx context.Context, tenant, payment uuid.UUID, by string) (storefront.Order, error) {
	var ord storefront.Order
	orders, err := m.orders()
	if err != nil {
		return ord, err
	}
	err = db.InTenantTx(ctx, m.Invoices.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		var entry uuid.UUID
		var err error
		if ord, entry, err = orders.UndoPayment(ctx, tx, tenant, payment, by); err != nil {
			return orderErr(err)
		}
		return invoicing.ReopenPart(ctx, tx, tenant, moneylog.OrderParts, moneylog.Part{ID: payment, Entry: &entry})
	})
	return ord, err
}
