package storefront

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/moneylog"
)

// Shares of an order (people ordering together; docs/spec/people-
// chatting.md §11 C, the founder's C2): an order one person placed for a
// group can be paid in parts, each member paying their own share by its
// own pay link (or the placer paying it for them). Each link is a part
// payment of the order (storefront_order_share_links, migration 00486),
// counted along the path every part takes (CountPayment); the order moves
// to paid when its parts reach what it costs. A share is named by an
// opaque key: the business sees parts of its order paid, never by whom.

// ErrNoShareLinks: the order is not paid by card through Taskiem (another
// rail, or no payment provider), so its shares cannot have links.
var ErrNoShareLinks = errors.New("storefront: this order's shares cannot be paid by link")

// ShareLinked is a share's pay link.
type ShareLinked struct {
	URL        string
	AmountKobo int64
	PaidAt     *time.Time
}

// shareRef is a share's checkout reference: the order's, and the share's.
func shareRef(order, key uuid.UUID) string {
	return paymentKey(order) + "_s" + hex.EncodeToString(key[:6])
}

// ShareLink is the pay link of one share of the order with this
// reference: made once per share (a later ask, by the member or by the
// placer for them, gives the same page), while the order can be paid now
// (PayableNow) and the share is no more than is left to pay. ErrDecision
// when it cannot be paid now; ErrNoShareLinks when it is not paid by card.
func (o *Orders) ShareLink(ctx context.Context, tenant uuid.UUID, reference string, key uuid.UUID, amount int64) (ShareLinked, error) {
	var ord Order
	var connection, slug string
	var out ShareLinked
	found := false
	read := func(tx pgx.Tx) error {
		err := tx.QueryRow(ctx, `SELECT payment_url, amount_kobo, paid_at FROM storefront_order_share_links WHERE tenant_id = $1 AND order_id = $2 AND share_key = $3`,
			tenant, ord.ID, key).Scan(&out.URL, &out.AmountKobo, &out.PaidAt)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		found = err == nil
		return err
	}
	err := db.InTenantTx(ctx, o.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		var err error
		if ord, err = o.GetOrder(ctx, tx, tenant, reference); err != nil {
			return err
		}
		if err := tx.QueryRow(ctx, `SELECT COALESCE(a.options->>'connection', ''), COALESCE(s.published_slug, s.slug) FROM storefront_orders o
			JOIN storefronts s ON s.id = o.storefront_id LEFT JOIN storefront_actions a ON a.id = o.action_id
			WHERE o.tenant_id = $1 AND o.id = $2`, tenant, ord.ID).Scan(&connection, &slug); err != nil {
			return err
		}
		return read(tx)
	})
	if err != nil || found {
		return out, err
	}
	if !ord.PayableNow() {
		return out, fmt.Errorf("%w: %s cannot be paid now", ErrDecision, ord.Reference)
	}
	if amount < 100 || amount > ord.BalanceKobo() {
		return out, fmt.Errorf("%w: a share is from ₦1 to what is left to pay on %s", ErrDecision, ord.Reference)
	}
	name, card := paystackConnection(connection)
	if !card || o.Payments == nil {
		return out, ErrNoShareLinks
	}
	url, ref, err := o.Payments.Create(ctx, tenant, name, PaymentRequest{Reference: shareRef(ord.ID, key), AmountKobo: amount,
		Email: o.payerEmail(ord.Customer, ord.Reference), CallbackURL: "https://" + slug + "." + o.PreviewDomain + "/",
		Metadata: map[string]any{"storefront_reference": ord.Reference, "share": true}})
	if err != nil {
		return out, err
	}
	err = db.InTenantTx(ctx, o.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `INSERT INTO storefront_order_share_links (id, tenant_id, order_id, share_key, amount_kobo, payment_url, payment_ref)
			VALUES ($1, $2, $3, $4, $5, $6, $7) ON CONFLICT (order_id, share_key) DO NOTHING`, uuid.New(), tenant, ord.ID, key, amount, url, ref)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 1 {
			if err := audit(ctx, tx, tenant, "storefront.share_link", ord.Reference, map[string]any{"amount_kobo": amount}); err != nil {
				return err
			}
		}
		return read(tx)
	})
	return out, err
}

// shareCheckMax is how many shares' links one check asks the provider
// about, per tenant: those asked longest ago first.
const shareCheckMax = 100

// shareOpenSQL: a share's link is asked about while its order is open (or
// closed within the hour, for a payment made just before) and, backing off,
// no sooner than 30 seconds after the last ask, doubling up to every 30
// minutes. A payment made after that is heard by the provider's event
// (matching: CountShare). As taskiem_storefront_payment_tenants.
const shareOpenSQL = `l.paid_at IS NULL AND l.refund_due_at IS NULL
	AND (o.status IN ('awaiting_payment', 'accepted', 'received', 'paid', 'dispatched', 'on_the_way', 'arrived') OR o.updated_at > now() - interval '1 hour')
	AND (l.checked_at IS NULL OR l.checked_at < now() - least(interval '30 seconds' * power(2, least(l.checks, 6)), interval '30 minutes'))`

// shareDue is a share's link to ask the provider about.
type shareDue struct {
	id                         uuid.UUID
	reference, ref, connection string
	amount                     int64
}

// checkShares asks the provider about the tenant's shares' pay links due
// a check (shareOpenSQL): a paid one is counted once (countShare); one
// not paid yet is asked again later, backing off.
func (o *Orders) checkShares(ctx context.Context, tenant uuid.UUID) error {
	if o.Payments == nil {
		return nil
	}
	var list []shareDue
	err := db.InTenantTx(ctx, o.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT l.id, o.reference, l.payment_ref, COALESCE(a.options->>'connection', ''), l.amount_kobo
			FROM storefront_order_share_links l JOIN storefront_orders o ON o.id = l.order_id LEFT JOIN storefront_actions a ON a.id = o.action_id
			WHERE l.tenant_id = $1 AND `+shareOpenSQL+` ORDER BY l.checked_at NULLS FIRST, l.created_at LIMIT `+fmt.Sprint(shareCheckMax), tenant)
		if err != nil {
			return err
		}
		list, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (shareDue, error) {
			var d shareDue
			return d, r.Scan(&d.id, &d.reference, &d.ref, &d.connection, &d.amount)
		})
		return err
	})
	if err != nil {
		return err
	}
	var errs []error
	for _, d := range list {
		name, card := paystackConnection(d.connection)
		if !card {
			continue
		}
		status, paid, err := o.Payments.Verify(ctx, tenant, name, d.ref)
		if err == nil && status == "paid" && paid >= d.amount {
			if _, err := o.countShare(ctx, tenant, d, paid, nil, nil); err != nil {
				errs = append(errs, err)
			}
			continue
		}
		if err != nil {
			errs = append(errs, err)
		}
		// Not paid yet (or the provider did not answer): asked again later.
		if err := db.InTenantTx(ctx, o.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
			_, err := tx.Exec(ctx, `UPDATE storefront_order_share_links SET checked_at = now(), checks = checks + 1 WHERE tenant_id = $1 AND id = $2`, tenant, d.id)
			return err
		}); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// ForShare names the share's link whose checkout page has this payment
// reference on channel ("paystack/main"); ErrNoOrder when none has.
func ForShare(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, channel, ref string) (uuid.UUID, error) {
	if ref == "" {
		return uuid.Nil, ErrNoOrder
	}
	var id uuid.UUID
	err := tx.QueryRow(ctx, `SELECT l.id FROM storefront_order_share_links l JOIN storefront_orders o ON o.id = l.order_id
		LEFT JOIN storefront_actions a ON a.id = o.action_id
		WHERE l.tenant_id = $1 AND l.payment_ref = $2 AND `+channelSQL+` = $3`, tenant, ref, channel).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, ErrNoOrder
	}
	return id, err
}

// CountShare counts a money log entry (the provider's event for a share's
// checkout page, ForShare) towards the share's order, as the payment check
// does (countShare): counted is false when the order could not take it
// (the share is then refund due). onCounted runs in the same transaction
// when it was counted (matching marks the entry matched).
func (o *Orders) CountShare(ctx context.Context, tenant, link, entry uuid.UUID, amount int64, onCounted func(tx pgx.Tx) error) (bool, error) {
	var d shareDue
	err := db.InTenantTx(ctx, o.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT l.id, o.reference, l.payment_ref, COALESCE(a.options->>'connection', ''), l.amount_kobo
			FROM storefront_order_share_links l JOIN storefront_orders o ON o.id = l.order_id LEFT JOIN storefront_actions a ON a.id = o.action_id
			WHERE l.tenant_id = $1 AND l.id = $2`, tenant, link).Scan(&d.id, &d.reference, &d.ref, &d.connection, &d.amount)
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return false, ErrNoOrder
	}
	if err != nil {
		return false, err
	}
	return o.countShare(ctx, tenant, d, amount, &entry, onCounted)
}

// countShare counts a share's payment towards its order once (a money
// log entry, recorded now when none is given, then CountPayment) and
// marks its link paid. A payment its order cannot take (lapsed or closed
// meanwhile, or more than is left) is never marked paid: the link is
// marked refund due, the business told on the order (its note and audit
// chain: send it back to whoever paid it), the member on the basket, and
// the money waits in the money log.
func (o *Orders) countShare(ctx context.Context, tenant uuid.UUID, d shareDue, paid int64, entry *uuid.UUID, onCounted func(tx pgx.Tx) error) (bool, error) {
	var c Counted
	counted, already := false, false
	err := db.InTenantTx(ctx, o.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		var paidAt, refundAt *time.Time
		if err := tx.QueryRow(ctx, `SELECT paid_at, refund_due_at FROM storefront_order_share_links WHERE tenant_id = $1 AND id = $2 FOR UPDATE`,
			tenant, d.id).Scan(&paidAt, &refundAt); err != nil {
			return err
		}
		if refundAt != nil {
			return nil // heard before, and refund due
		}
		if paidAt != nil {
			// Heard before (by the check, or the provider's event): this
			// entry counted already when it is the one that was counted.
			if entry == nil {
				return nil
			}
			var same bool
			if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM storefront_order_payments WHERE tenant_id = $1 AND entry_id = $2 AND undoes IS NULL)`,
				tenant, *entry).Scan(&same); err != nil || !same {
				return err
			}
			already = true
			if onCounted != nil {
				return onCounted(tx)
			}
			return nil
		}
		in := PaymentIn{AmountKobo: paid, How: "exact", By: "system:" + checkoutChannel(d.connection), Reasons: []string{"a share paid through its own pay link"}, EntryID: entry}
		if entry == nil && o.MoneyLog != nil {
			id, _, err := o.MoneyLog.RecordTx(ctx, tx, tenant, moneylog.Entry{Direction: "in", Kind: "payment", Status: "settled", Channel: checkoutChannel(d.connection),
				Reference: d.ref, Event: "payment.received", AmountKobo: &paid, Source: "lookup"})
			if err != nil {
				return err
			}
			in.EntryID = &id
		}
		// In a savepoint: an order that cannot take it leaves the money in
		// the log and the share refund due.
		sp, err := tx.Begin(ctx)
		if err != nil {
			return err
		}
		ord, err := o.GetOrder(ctx, sp, tenant, d.reference)
		if err == nil && paid > ord.BalanceKobo() {
			err = fmt.Errorf("%w: %s has %s left to pay", ErrDecision, d.reference, FormatNaira(ord.BalanceKobo()))
		}
		if err == nil {
			c, err = o.CountPayment(ctx, sp, tenant, d.reference, in)
		}
		if errors.Is(err, ErrDecision) {
			o.log().Warn("storefront: a share's payment was not counted", "reference", d.reference, "err", err)
			if err := sp.Rollback(ctx); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `UPDATE storefront_order_share_links SET refund_due_at = now() WHERE tenant_id = $1 AND id = $2`, tenant, d.id); err != nil {
				return err
			}
			note := "A share of " + FormatNaira(paid) + " was paid on " + o.now().In(Lagos).Format("2 Jan 15:04") +
				" that this order could not take: send it back to whoever paid it from your bank (the money log has the payment)."
			if _, err := tx.Exec(ctx, `UPDATE storefront_orders SET note = CASE WHEN COALESCE(note, '') = '' THEN $3 ELSE note || E'\n' || $3 END, updated_at = now()
				WHERE tenant_id = $1 AND reference = $2`, tenant, d.reference, note); err != nil {
				return err
			}
			return audit(ctx, tx, tenant, "storefront.share_refund_due", d.reference, map[string]any{"amount_kobo": paid})
		}
		if err != nil {
			return err
		}
		if err := sp.Commit(ctx); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE storefront_order_share_links SET paid_at = now() WHERE tenant_id = $1 AND id = $2`, tenant, d.id); err != nil {
			return err
		}
		counted = true
		if onCounted != nil {
			return onCounted(tx)
		}
		return nil
	})
	if err != nil || !counted {
		return counted || already, err
	}
	return true, o.Paid(ctx, tenant, c)
}

// refundDueTx tells the business, on an order moved to refund_due with
// only parts of it paid (shares of an order placed together, or parts
// matched to it), what to send back: its note, and its audit chain.
func (o *Orders) refundDueTx(ctx context.Context, tx pgx.Tx, tenant, id uuid.UUID, reference, why string) error {
	ord, err := scanOrder(tx.QueryRow(ctx, `SELECT `+orderCols+` FROM storefront_orders WHERE tenant_id = $1 AND id = $2`, tenant, id))
	if err != nil {
		return err
	}
	note := refundByHandNote(ord)
	if _, err := tx.Exec(ctx, `UPDATE storefront_orders SET note = $3, updated_at = now() WHERE tenant_id = $1 AND id = $2`, tenant, id, note); err != nil {
		return err
	}
	return audit(ctx, tx, tenant, "storefront.order_refund_due", reference, map[string]any{"why": why, "paid_kobo": ord.PaidKobo})
}

// sharesStatusLink answers the payment link of an order paid in shares
// (PaymentLink): no page for the whole of it, but its status page, the
// order waiting ShareTTL for its shares once accepted.
func (o *Orders) sharesStatusLink(ctx context.Context, tenant uuid.UUID, ord Order) (map[string]any, error) {
	if ord.Status == "accepted" && ord.Payment == "after_accept" {
		if err := db.InTenantTx(ctx, o.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
			_, err := tx.Exec(ctx, `UPDATE storefront_orders SET status = 'awaiting_payment', expires_at = $3, updated_at = now()
				WHERE tenant_id = $1 AND id = $2 AND status = 'accepted'`, tenant, ord.ID, o.now().Add(ShareTTL))
			return err
		}); err != nil {
			return nil, err
		}
	}
	m, err := o.statusLinkFor(ctx, tenant, ord)
	if err != nil {
		return nil, err
	}
	m["amount_kobo"], m["balance_kobo"] = ord.BalanceKobo(), ord.BalanceKobo()
	return m, nil
}
