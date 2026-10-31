package matching

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"

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
		done, err = m.CountCustomer(ctx, tx, tenant, e.ID, acct.CustomerID(), CustomerCounting{Connection: e.Channel, How: "exact", By: "system:" + e.Channel,
			Reasons: []string{ReasonOwnAccount}, Note: e.Description, AmountKobo: e.AmountKobo})
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
	// Note is what the payment carried (the rail's note), and AmountKobo
	// what it was: for a linked business (link:<its id>), the invoice
	// named in the note, or by the pairing token of its payout of that
	// amount, is counted first (docs/spec/business-links.md §6.3).
	Note       string
	AmountKobo int64
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
	// A linked business's money goes first to the invoice it names: in the
	// note, or by its payout's pairing token when the rail dropped the
	// note. Oldest-first then passes over invoices it queried.
	linked := strings.HasPrefix(customer, "key:link:")
	var token string
	if linked {
		var named int
		if named, token = namedInvoice(list, c.Note); named < 0 && c.AmountKobo > 0 {
			if err := tx.QueryRow(ctx, `SELECT COALESCE(taskiem_link_payout_token($1, $2), '')`, customer, c.AmountKobo).Scan(&token); err != nil {
				return nil, err
			}
			named, _ = namedInvoice(list, token)
		}
		if named >= 0 {
			if err := count(list[named], ReasonNamed); err != nil {
				return nil, err
			}
			list = slices.Delete(slices.Clone(list), named, named+1)
		}
		list = slices.DeleteFunc(list, func(x invoicing.Invoice) bool { return x.Disputed })
	}
	reason := ReasonOldest
	for _, x := range list {
		if err := count(x, reason); err != nil {
			return nil, err
		}
		reason = ReasonNext
	}
	if linked && token != "" {
		// The buyer's payout and this credit are one transfer: the buyer's
		// payment is marked paired, through the one door into its side,
		// after everything here is written (the scope is the buyer's
		// from then on).
		if err := matched(ctx, tx, tenant, entryID, m.now()); err != nil {
			return nil, err
		}
		return done, pairPayout(ctx, tx, customer, token)
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

// ReasonNamed: the invoice a linked business's payment named, in its note
// or by its payout's pairing token.
const ReasonNamed = "the invoice the payment named"

// namedInvoice is the open invoice whose number note carries as a word of
// its own ("INV-0042/7QX4", "for inv-0042"), and the token it was found
// in; -1 when none, or more than one, is named.
func namedInvoice(list []invoicing.Invoice, note string) (int, string) {
	if note == "" {
		return -1, ""
	}
	words := strings.FieldsFunc(strings.ToUpper(note), func(r rune) bool { return r == ' ' || r == '/' || r == ',' || r == ';' || r == ':' })
	found := -1
	for i, x := range list {
		if slices.Contains(words, strings.ToUpper(x.Number)) {
			if found >= 0 {
				return -1, ""
			}
			found = i
		}
	}
	if found < 0 {
		return -1, ""
	}
	// A payout from Taskiem carries NUMBER/CODE, its pairing token.
	up, num := strings.ToUpper(note), strings.ToUpper(list[found].Number)
	if i := strings.Index(up, num+"/"); i >= 0 {
		rest := up[i+len(num)+1:]
		if n := strings.IndexFunc(rest, func(r rune) bool { return (r < 'A' || r > 'Z') && (r < '0' || r > '9') }); n != 0 {
			if n < 0 {
				n = len(rest)
			}
			return found, num + "/" + rest[:n]
		}
	}
	return found, ""
}

// pairPayout marks the linked buyer's payment that carried token as
// paired with this credit, entering the buyer's side (link:<buyer>)
// through taskiem_link_enter: the last write of the transaction.
func pairPayout(ctx context.Context, tx pgx.Tx, customer, token string) error {
	var link *uuid.UUID
	if err := tx.QueryRow(ctx, `SELECT id FROM taskiem_links_of() WHERE business = $1 AND state = 'active'`, strings.TrimPrefix(customer, "key:link:")).Scan(&link); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		return err
	}
	detail, _ := json.Marshal(map[string]any{"token": token})
	if _, err := tx.Exec(ctx, `SELECT taskiem_link_enter($1, 'system', 'matching', 'link.paired', $2)`, link, detail); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `UPDATE bill_payments SET paired_at = now() WHERE token = $1 AND paired_at IS NULL`, token)
	return err
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
