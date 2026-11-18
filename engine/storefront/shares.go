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

// checkShares asks the provider about the tenant's shares' pay links not
// yet paid (made in the last week): a paid one is a part of its order,
// counted once (a money log entry, then CountPayment), and the link marked
// paid. Money the order cannot take (closed meanwhile, or paid by other
// money) waits in the money log for a person, as the checkout's does.
func (o *Orders) checkShares(ctx context.Context, tenant uuid.UUID) error {
	if o.Payments == nil {
		return nil
	}
	type due struct {
		id                         uuid.UUID
		reference, ref, connection string
		amount                     int64
	}
	var list []due
	err := db.InTenantTx(ctx, o.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT l.id, o.reference, l.payment_ref, COALESCE(a.options->>'connection', ''), l.amount_kobo
			FROM storefront_order_share_links l JOIN storefront_orders o ON o.id = l.order_id LEFT JOIN storefront_actions a ON a.id = o.action_id
			WHERE l.tenant_id = $1 AND l.paid_at IS NULL AND l.created_at > now() - interval '7 days' ORDER BY l.created_at LIMIT 100`, tenant)
		if err != nil {
			return err
		}
		list, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (due, error) {
			var d due
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
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if status != "paid" || paid < d.amount {
			continue
		}
		var c Counted
		counted := false
		err = db.InTenantTx(ctx, o.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
			tag, err := tx.Exec(ctx, `UPDATE storefront_order_share_links SET paid_at = now() WHERE tenant_id = $1 AND id = $2 AND paid_at IS NULL`, tenant, d.id)
			if err != nil || tag.RowsAffected() == 0 {
				return err
			}
			in := PaymentIn{AmountKobo: paid, How: "exact", By: "system:" + checkoutChannel(d.connection), Reasons: []string{"a share paid through its own pay link"}}
			if o.MoneyLog != nil {
				id, _, err := o.MoneyLog.RecordTx(ctx, tx, tenant, moneylog.Entry{Direction: "in", Kind: "payment", Status: "settled", Channel: checkoutChannel(d.connection),
					Reference: d.ref, Event: "payment.received", AmountKobo: &paid, Source: "lookup"})
				if err != nil {
					return err
				}
				in.EntryID = &id
			}
			// In a savepoint: an order that cannot take it leaves the link
			// marked paid and the money in the log.
			sp, err := tx.Begin(ctx)
			if err != nil {
				return err
			}
			c, err = o.CountPayment(ctx, sp, tenant, d.reference, in)
			if errors.Is(err, ErrDecision) {
				o.log().Warn("storefront: a share's payment was not counted", "reference", d.reference, "err", err)
				return sp.Rollback(ctx)
			}
			if err != nil {
				return err
			}
			counted = true
			return sp.Commit(ctx)
		})
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if counted {
			if err := o.Paid(ctx, tenant, c); err != nil {
				errs = append(errs, err)
			}
		}
	}
	return errors.Join(errs...)
}
