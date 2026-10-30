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
	ReasonOwnLink    = "paid through the customer's own payment link"
	ReasonOldest     = "the customer's oldest open invoice"
	ReasonNext       = "what was left after the customer's older open invoices"
	// ReasonSibling: what was left after the student's own invoices, where
	// the school shares family credit (invoicing.SharedWith).
	ReasonSibling = "what was left after the customer's own open invoices, for a sibling (the school shares family credit)"
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
	var done []CountedPart
	err = db.InTenantTx(ctx, m.Invoices.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		if err := moneylog.LockEntry(ctx, tx, e.ID); err != nil {
			return err
		}
		if err := invoicing.MarkCustomers(ctx, tx, tenant, e.ID, acct.CustomerID(), &acct.ID, "account", "system:"+e.Channel); err != nil {
			return err
		}
		var err error
		done, err = m.CountCustomer(ctx, tx, tenant, e.ID, acct.CustomerID(), CustomerCounting{Connection: e.Channel, How: "exact", By: "system:" + e.Channel, Reasons: []string{ReasonOwnAccount}})
		return err
	})
	if err != nil {
		return true, err
	}
	return true, m.TellPaid(ctx, tenant, done)
}

// CustomerCounting is how a customer's money is counted towards their
// invoices (CountCustomer): the connection or channel it came on, how
// (exact for money paid into their own account, manual for cash a person
// posted for them), who, and the reasons every part gives before its own.
type CustomerCounting struct {
	Connection string
	How        string
	By         string
	Reasons    []string
}

// CountedPart is one part CountCustomer counted: the invoice as it is
// after it, the part's row and amount, and whether the part paid the
// invoice in full (TellPaid tells a run waiting on it, after commit).
type CountedPart struct {
	Invoice    invoicing.Invoice
	PartID     uuid.UUID
	AmountKobo int64
	Became     bool
}

// CountCustomer counts an entry that is a customer's money (marked so
// with invoicing.MarkCustomers, under the entry's lock) towards the
// customer's open invoices, oldest first, then, where the school shares
// family credit, their siblings' (invoicing.SharedWith), each one's oldest
// first, until the money runs out; what is left is the customer's credit.
// It is the one path a student's own account and cash posted at the desk
// share (docs/spec/schools.md 7.1, 8). The entry's suggestions, if any,
// close. Call TellPaid with what it returns once the transaction commits.
func (m *Matcher) CountCustomer(ctx context.Context, tx pgx.Tx, tenant, entryID uuid.UUID, customer string, c CustomerCounting) ([]CountedPart, error) {
	var done []CountedPart
	count := func(x invoicing.Invoice, reason string) error {
		left, ok, err := moneylog.EntryLeft(ctx, tx, entryID)
		if err != nil || !ok || left <= 0 {
			return err
		}
		part := min(left, x.DueKobo)
		if part <= 0 {
			return nil
		}
		id := entryID
		k, err := m.countPart(ctx, tx, tenant, Part{Invoice: x.ID.String()}, invoicing.PaymentIn{AmountKobo: part, PaymentID: id.String(), EntryID: &id,
			Connection: c.Connection, How: c.How, By: c.By, Reasons: append(append([]string{}, c.Reasons...), reason)})
		if err != nil {
			return err
		}
		done = append(done, CountedPart{Invoice: *k.Invoice, PartID: k.paymentID, AmountKobo: part, Became: k.became})
		return nil
	}
	list, err := m.Invoices.OpenInvoicesOf(ctx, tx, tenant, customer)
	if err != nil {
		return nil, err
	}
	reason := ReasonOldest
	for _, x := range list {
		if err := count(x, reason); err != nil {
			return nil, err
		}
		reason = ReasonNext
	}
	// Where the school shares family credit, what is left counts towards
	// the siblings' open invoices, each sibling's oldest first.
	shared, err := invoicing.SharedWith(ctx, tx, tenant, customer)
	if err != nil {
		return nil, err
	}
	for _, sib := range shared {
		list, err := m.Invoices.OpenInvoicesOf(ctx, tx, tenant, sib)
		if err != nil {
			return nil, err
		}
		for _, x := range list {
			if err := count(x, ReasonSibling); err != nil {
				return nil, err
			}
		}
	}
	// Matched: what is left is the customer's credit.
	return done, matched(ctx, tx, tenant, entryID, m.now())
}

// TellPaid tells the runs waiting on the invoices that CountCustomer's
// parts paid in full, after the transaction that counted them commits.
func (m *Matcher) TellPaid(ctx context.Context, tenant uuid.UUID, parts []CountedPart) error {
	for _, p := range parts {
		if p.Became {
			if err := m.Invoices.Paid(ctx, tenant, p.Invoice); err != nil {
				return err
			}
		}
	}
	return nil
}

// LinkPaid counts a payment the invoices' checker found through a
// customer's payment link (invoicing.Invoices.LinkReceived): the money is
// recorded in the log as the rail's payment event would be (one entry
// whichever is heard first) and counted as the customer's, exactly.
func (m *Matcher) LinkPaid(ctx context.Context, tenant, link uuid.UUID, r invoicing.Received) error {
	if m.Invoices.MoneyLog == nil {
		// Counted when the rail's event is heard and matched (exact).
		return nil
	}
	var entry uuid.UUID
	err := db.InTenantTx(ctx, m.Invoices.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		amount := r.AmountKobo
		var err error
		entry, _, err = m.Invoices.MoneyLog.RecordTx(ctx, tx, tenant, moneylog.Entry{Direction: "in", Kind: "payment", Status: "settled", Channel: r.Connection,
			Reference: r.PaymentID, Event: "payment.received", AmountKobo: &amount, Source: "lookup"})
		return err
	})
	if err != nil {
		return err
	}
	return m.linkEntry(ctx, tenant, entry, link, r.Account, r.Connection)
}

// linkEntry counts an entry paid through a customer's payment link
// towards the customer's open invoices, oldest first, the rest their
// credit, with no one asked: the reference is one Taskiem gave for that
// customer. Counting again finds nothing left of the entry.
func (m *Matcher) linkEntry(ctx context.Context, tenant, entry, link, account uuid.UUID, channel string) error {
	var done []CountedPart
	err := db.InTenantTx(ctx, m.Invoices.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		if err := moneylog.LockEntry(ctx, tx, entry); err != nil {
			return err
		}
		customer, err := invoicing.LinkCustomer(ctx, tx, tenant, link)
		if err != nil {
			return err
		}
		if err := invoicing.MarkCustomers(ctx, tx, tenant, entry, customer, nil, "link", "system:"+channel); err != nil {
			return err
		}
		if done, err = m.CountCustomer(ctx, tx, tenant, entry, customer, CustomerCounting{Connection: channel, How: "exact", By: "system:" + channel,
			Reasons: []string{ReasonOwnLink}}); err != nil {
			return err
		}
		if account != uuid.Nil {
			_, err = tx.Exec(ctx, `UPDATE customer_invoice_accounts SET status = 'paid', updated_at = now() WHERE tenant_id = $1 AND id = $2 AND customer_link_id = $3`,
				tenant, account, link)
		}
		return err
	})
	if err != nil {
		return err
	}
	return m.TellPaid(ctx, tenant, done)
}
