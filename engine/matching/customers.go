package matching

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/invoicing"
	"github.com/israel-duff/taskiem/engine/moneylog"
)

// The permanent-account rule (decision 0046 point 5; docs/spec/schools.md
// 7.1; docs/matching.md#customers-own-account-numbers). A credit to a
// customer's permanent account, whose connection, account number and
// reference all agree with the account registered for them (a rail that
// does not say which account number was paid into, such as Flutterwave
// and Moniepoint, is taken at the reference it issued), is the
// customer's money: matched exactly, with no one asked, to their oldest
// open invoice, then the next, until it runs out. What is left is their
// credit, counted towards the next invoice sent to them
// (invoicing.Invoices.Send). A credit naming a registered account by its
// reference but paid into another account number, or into a registered
// account number with another reference, is held for a person with the
// reason, never posted.

// Reasons the permanent-account rule gives. They name no one.
const (
	ReasonOwnAccount = "paid into the customer's own account number"
	ReasonOldest     = "the customer's oldest open invoice"
	ReasonNext       = "what was left after the customer's older open invoices"
)

// Why a credit to a registered account is held.
const (
	HeldOtherAccount = "the reference names a customer's account, but the money was paid into another account number"
	HeldOtherRef     = "paid into a customer's account number, but with another account's reference"
	HeldNoAmount     = "paid into a customer's account, with no amount"
)

// permanent applies the rule to an entry; done is false when the entry
// names no registered account (it is then suggested as any other money).
func (m *Matcher) permanent(ctx context.Context, tenant uuid.UUID, e entry) (bool, error) {
	var acct invoicing.CustomerAccount
	var found bool
	err := db.InTenantTx(ctx, m.Invoices.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		var err error
		acct, found, err = m.Invoices.AccountFor(ctx, tx, tenant, e.Channel, e.Reference, e.AccountNumber)
		return err
	})
	if err != nil || !found {
		return false, err
	}
	switch {
	case acct.Reference == e.Reference && e.AccountNumber != "" && acct.AccountNumber != e.AccountNumber:
		return true, m.hold(ctx, tenant, e.ID, "unmatched", HeldOtherAccount)
	case acct.Reference != e.Reference:
		return true, m.hold(ctx, tenant, e.ID, "unmatched", HeldOtherRef)
	case e.AmountKobo <= 0:
		return true, m.hold(ctx, tenant, e.ID, "unmatched", HeldNoAmount)
	}
	var done []counted
	err = db.InTenantTx(ctx, m.Invoices.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		done = done[:0]
		if err := moneylog.LockEntry(ctx, tx, e.ID); err != nil {
			return err
		}
		if err := invoicing.MarkCustomers(ctx, tx, tenant, e.ID, acct.CustomerID(), &acct.ID, "account", "system:"+e.Channel); err != nil {
			return err
		}
		list, err := m.Invoices.OpenInvoicesOf(ctx, tx, tenant, acct.CustomerID())
		if err != nil {
			return err
		}
		reason := ReasonOldest
		for _, x := range list {
			left, ok, err := moneylog.EntryLeft(ctx, tx, e.ID)
			if err != nil {
				return err
			}
			if !ok || left <= 0 {
				break
			}
			part := min(left, x.DueKobo)
			if part <= 0 {
				continue
			}
			id := e.ID
			k, err := m.countPart(ctx, tx, tenant, Part{Invoice: x.ID.String()}, invoicing.PaymentIn{AmountKobo: part, PaymentID: id.String(), EntryID: &id,
				Connection: e.Channel, How: "exact", By: "system:" + e.Channel, Reasons: []string{ReasonOwnAccount, reason}})
			if err != nil {
				return err
			}
			done = append(done, k)
			reason = ReasonNext
		}
		// Matched: what is left is the customer's credit.
		return matched(ctx, tx, tenant, e.ID, m.now())
	})
	if err != nil {
		return true, err
	}
	return true, m.paid(ctx, tenant, done)
}
