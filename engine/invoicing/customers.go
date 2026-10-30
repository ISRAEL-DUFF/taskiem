package invoicing

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/moneylog"
	"github.com/israel-duff/taskiem/engine/pii"
)

// Customers' permanent accounts and credit (decision 0046 points 5 to 7;
// docs/spec/schools.md 7; docs/matching.md#customers-own-account-numbers).
//
// A customer is who invoices are for: the business's own key for them
// (Customer.Key, "student:42") when it gives one, else their phone number
// (else email), as its personal-data subject: Invoice.CustomerID. A
// customer's permanent account (a virtual_account issued for them) is
// registered against that identity, so engine/matching can tell whose a
// credit into it is. What a customer paid and no invoice of theirs counts
// yet is their credit, counted towards the next invoice sent to them.

// CustomerAccount is a customer's permanent account number.
type CustomerAccount struct {
	ID uuid.UUID `json:"id"`
	// Channel is the connection the account is on, as the money log names
	// it: "paystack/main".
	Channel       string    `json:"channel"`
	AccountNumber string    `json:"account_number"`
	BankName      string    `json:"bank_name,omitempty"`
	Reference     string    `json:"reference"`
	Customer      Customer  `json:"customer"`
	CreatedBy     string    `json:"created_by"`
	CreatedAt     time.Time `json:"created_at"`
	customerID    string
}

// CustomerID is the account's customer, as Invoice.CustomerID.
func (a CustomerAccount) CustomerID() string { return a.customerID }

// AccountIn is an account to register for a customer.
type AccountIn struct {
	Customer      Customer `json:"customer"`
	Channel       string   `json:"channel"`
	AccountNumber string   `json:"account_number"`
	Reference     string   `json:"reference"`
	BankName      string   `json:"bank_name,omitempty"`
}

var (
	channelRE = regexp.MustCompile(`^[a-z0-9_]{1,60}/[A-Za-z0-9_.-]{1,100}$`)
	numberRE  = regexp.MustCompile(`^[0-9A-Za-z-]{4,40}$`)
)

// sealCustomer seals a customer under their phone's (else email's, else
// key's) subject, and says who they are (CustomerID).
func (s *Invoices) sealCustomer(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, c Customer) (map[string]any, string, error) {
	cat, key := "phone", c.Phone
	switch {
	case key != "":
	case c.Email != "":
		cat, key = "email", c.Email
	default:
		cat, key = "customer_key", c.Key
	}
	env, err := s.Vault.SealTx(ctx, tx, tenant, cat, key)
	if err != nil {
		return nil, "", err
	}
	subject, _ := env["subject"].(string)
	sealed, err := s.Vault.SealUnderTx(ctx, tx, tenant, subject, "customer", c)
	if err != nil {
		return nil, "", err
	}
	if c.Key != "" {
		return sealed, "key:" + c.Key, nil
	}
	return sealed, subject, nil
}

// RegisterAccount links a customer's permanent account to them, once:
// the same account for the same customer again returns it. An account
// (its reference or its number, on the connection) already registered
// for another customer, or with another number or reference, is refused.
// Issuing an account (IssueAccount, and schools@1 issuing students'
// accounts, B4) registers it here.
func (s *Invoices) RegisterAccount(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, a AccountIn, by string) (CustomerAccount, error) {
	a.Customer.Name = strings.TrimSpace(a.Customer.Name)
	a.Reference, a.AccountNumber = strings.TrimSpace(a.Reference), strings.TrimSpace(a.AccountNumber)
	if err := checkCustomer(a.Customer); err != nil {
		return CustomerAccount{}, err
	}
	switch {
	case !channelRE.MatchString(a.Channel):
		return CustomerAccount{}, invalid("channel names the connection the account is on, like paystack/main")
	case !numberRE.MatchString(a.AccountNumber):
		return CustomerAccount{}, invalid("the account number has 4 to 40 letters and digits")
	case a.Reference == "" || len(a.Reference) > 200:
		return CustomerAccount{}, invalid("the account's reference is required (up to 200 characters)")
	case len(a.BankName) > 120:
		return CustomerAccount{}, invalid("the bank's name has up to 120 characters")
	}
	sealed, customer, err := s.sealCustomer(ctx, tx, tenant, a.Customer)
	if err != nil {
		return CustomerAccount{}, err
	}
	// Registering one account twice at once: the second waits, then finds
	// the first.
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('taskiem customer account ' || $1::text || ' ' || $2, 0))`, tenant, a.Channel); err != nil {
		return CustomerAccount{}, err
	}
	var id uuid.UUID
	var number, reference, who string
	err = tx.QueryRow(ctx, `SELECT id, account_number, reference, customer_id FROM customer_accounts WHERE tenant_id = $1 AND channel = $2 AND (reference = $3 OR account_number = $4)
		ORDER BY reference = $3 DESC LIMIT 1`, tenant, a.Channel, a.Reference, a.AccountNumber).Scan(&id, &number, &reference, &who)
	switch {
	case err == nil && number == a.AccountNumber && reference == a.Reference && who == customer:
		return s.account(ctx, tx, tenant, id)
	case err == nil && who != customer:
		return CustomerAccount{}, fmt.Errorf("%w: that account is already another customer's", ErrState)
	case err == nil:
		return CustomerAccount{}, fmt.Errorf("%w: that account is registered with another account number or reference", ErrState)
	case !errors.Is(err, pgx.ErrNoRows):
		return CustomerAccount{}, err
	}
	raw, _ := json.Marshal(sealed)
	id = uuid.New()
	if _, err := tx.Exec(ctx, `INSERT INTO customer_accounts (id, tenant_id, customer_id, customer, channel, account_number, reference, bank_name, created_by)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`, id, tenant, customer, raw, a.Channel, a.AccountNumber, a.Reference, nullable(a.BankName), by); err != nil {
		return CustomerAccount{}, err
	}
	if err := audit(ctx, tx, tenant, by, "invoice.customer_account", a.Channel, map[string]any{"account": id.String()}); err != nil {
		return CustomerAccount{}, err
	}
	return s.account(ctx, tx, tenant, id)
}

const customerAccountCols = `id, channel, account_number, COALESCE(bank_name, ''), reference, customer, customer_id, created_by, created_at`

func (s *Invoices) scanAccount(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, r pgx.Row) (CustomerAccount, error) {
	var a CustomerAccount
	var cust map[string]any
	if err := r.Scan(&a.ID, &a.Channel, &a.AccountNumber, &a.BankName, &a.Reference, &cust, &a.customerID, &a.CreatedBy, &a.CreatedAt); err != nil {
		return a, err
	}
	var err error
	a.Customer, err = s.openCustomer(ctx, tx, tenant, cust)
	return a, err
}

func (s *Invoices) account(ctx context.Context, tx pgx.Tx, tenant, id uuid.UUID) (CustomerAccount, error) {
	return s.scanAccount(ctx, tx, tenant, tx.QueryRow(ctx, `SELECT `+customerAccountCols+` FROM customer_accounts WHERE tenant_id = $1 AND id = $2`, tenant, id))
}

// AccountsOf lists a customer's permanent accounts, oldest first.
func (s *Invoices) AccountsOf(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, customer string) ([]CustomerAccount, error) {
	rows, err := tx.Query(ctx, `SELECT `+customerAccountCols+` FROM customer_accounts WHERE tenant_id = $1 AND customer_id = $2 ORDER BY created_at, id`, tenant, customer)
	if err != nil {
		return nil, err
	}
	type sealed struct {
		a    CustomerAccount
		cust map[string]any
	}
	list, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (sealed, error) {
		var x sealed
		a := &x.a
		return x, r.Scan(&a.ID, &a.Channel, &a.AccountNumber, &a.BankName, &a.Reference, &x.cust, &a.customerID, &a.CreatedBy, &a.CreatedAt)
	})
	if err != nil {
		return nil, err
	}
	out := make([]CustomerAccount, 0, len(list))
	for _, x := range list {
		if x.a.Customer, err = s.openCustomer(ctx, tx, tenant, x.cust); err != nil {
			return nil, err
		}
		out = append(out, x.a)
	}
	return out, nil
}

func (s *Invoices) openCustomer(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, env map[string]any) (Customer, error) {
	var c Customer
	v, err := s.Vault.OpenTx(ctx, tx, tenant, env)
	switch {
	case errors.Is(err, pii.ErrErased):
		return Customer{Name: "(erased)"}, nil
	case err != nil:
		return c, err
	}
	raw, _ := json.Marshal(v)
	return c, json.Unmarshal(raw, &c)
}

// AccountFor finds the registered account a credit names, on channel: by
// its reference, else by its account number. ok is false when neither is
// registered.
func (s *Invoices) AccountFor(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, channel, reference, number string) (CustomerAccount, bool, error) {
	var id uuid.UUID
	err := tx.QueryRow(ctx, `SELECT id FROM customer_accounts WHERE tenant_id = $1 AND channel = $2 AND (reference = $3 OR ($4 <> '' AND account_number = $4))
		ORDER BY reference = $3 DESC LIMIT 1`, tenant, channel, reference, number).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return CustomerAccount{}, false, nil
	}
	if err != nil {
		return CustomerAccount{}, false, err
	}
	a, err := s.account(ctx, tx, tenant, id)
	return a, err == nil, err
}

// CreditOf is a customer's credit: what entries that are their money count
// for, less what of them counts towards invoices and orders.
func CreditOf(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, customer string) (int64, error) {
	var n int64
	err := tx.QueryRow(ctx, `SELECT COALESCE(sum(GREATEST(COALESCE(taskiem_money_entry_left(c.entry_id), 0), 0)), 0)::bigint FROM money_entry_customers c
		WHERE c.tenant_id = $1 AND c.customer_id = $2`, tenant, customer).Scan(&n)
	return n, err
}

// OpenInvoicesOf lists a customer's open invoices (recorded, sent,
// part-paid or overdue) oldest first: by due date (those without one by
// when they were sent, or recorded), then when sent and number. What a
// customer's money pays first.
func (s *Invoices) OpenInvoicesOf(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, customer string) ([]Invoice, error) {
	rows, err := tx.Query(ctx, `SELECT id FROM customer_invoices WHERE tenant_id = $1 AND customer_id = $2 AND status IN ('recorded', 'sent', 'part_paid', 'overdue')
		ORDER BY COALESCE(due_on, (COALESCE(sent_at, recorded_at) AT TIME ZONE 'Africa/Lagos')::date), COALESCE(sent_at, recorded_at), number LIMIT 200`, tenant, customer)
	if err != nil {
		return nil, err
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
	if err != nil {
		return nil, err
	}
	out := make([]Invoice, 0, len(ids))
	for _, id := range ids {
		x, err := s.Get(ctx, tx, tenant, id.String())
		if err != nil {
			return nil, err
		}
		out = append(out, x)
	}
	return out, nil
}

// MarkCustomers records that an entry is a customer's money (how account:
// paid into their account; person: a person matched part of it to their
// invoices and left the rest as their credit), once.
func MarkCustomers(ctx context.Context, tx pgx.Tx, tenant, entry uuid.UUID, customer string, account *uuid.UUID, how, by string) error {
	_, err := tx.Exec(ctx, `INSERT INTO money_entry_customers (entry_id, tenant_id, customer_id, account_id, how, by) VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (entry_id) DO NOTHING`, entry, tenant, customer, account, how, by)
	return err
}

// CreditReason is the reason a payment from a customer's credit gives.
const CreditReason = "the customer's credit from an earlier payment"

// FamilyShareReason is the reason a payment from a sibling's credit
// gives, where the school shares family credit (SharedWith).
const FamilyShareReason = "a sibling's credit from an earlier payment (the school shares family credit)"

// SharedWith lists the customers whose open invoices a customer's credit
// may also count towards, after the customer's own: for a student of a
// school (key:student:<id>, docs/schools.md) whose school shares family
// credit (Schools > Settings, off by default), the other students of
// their family, by admission number. None for anyone else.
func SharedWith(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, customer string) ([]string, error) {
	rest, ok := strings.CutPrefix(customer, "key:student:")
	if !ok {
		return nil, nil
	}
	student, perr := uuid.Parse(rest)
	if perr != nil {
		return nil, nil //nolint:nilerr // a key naming no student shares with no one
	}
	rows, err := tx.Query(ctx, `SELECT 'key:student:' || o.id::text FROM school_students s
		JOIN school_settings st ON st.tenant_id = s.tenant_id AND st.enabled AND st.siblings_share_credit
		JOIN school_students o ON o.tenant_id = s.tenant_id AND o.family_id = s.family_id AND o.id <> s.id
		WHERE s.tenant_id = $1 AND s.id = $2 ORDER BY upper(o.admission_number), o.id`, tenant, student)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowTo[string])
}

// applyCredit counts a customer's credit towards an invoice of theirs just
// sent, oldest money first, until it is paid or the credit runs out: each
// a part of the entry it came from (how exact). Where the school shares
// family credit, a sibling's credit counts after the customer's own
// (SharedWith). became is true when this paid it.
func (s *Invoices) applyCredit(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, x Invoice) (Invoice, int64, bool, error) {
	if x.customerID == "" || x.DueKobo <= 0 {
		return x, 0, false, nil
	}
	whose, err := SharedWith(ctx, tx, tenant, x.customerID)
	if err != nil {
		return x, 0, false, err
	}
	whose = append([]string{x.customerID}, whose...)
	rows, err := tx.Query(ctx, `SELECT c.entry_id, e.channel, c.customer_id = $3 FROM money_entry_customers c JOIN money_entries e ON e.id = c.entry_id
		WHERE c.tenant_id = $1 AND c.customer_id = ANY ($2) AND COALESCE(taskiem_money_entry_left(c.entry_id), 0) > 0
		ORDER BY c.customer_id = $3 DESC, COALESCE(e.occurred_at, e.recorded_at), e.id`, tenant, whose, x.customerID)
	if err != nil {
		return x, 0, false, err
	}
	type credit struct {
		entry   uuid.UUID
		channel string
		own     bool
	}
	list, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (credit, error) {
		var c credit
		return c, r.Scan(&c.entry, &c.channel, &c.own)
	})
	if err != nil {
		return x, 0, false, err
	}
	var applied int64
	became := false
	for _, c := range list {
		if x.DueKobo <= 0 {
			break
		}
		left, ok, err := moneylog.LockLeft(ctx, tx, c.entry)
		if err != nil {
			return x, applied, became, err
		}
		if !ok || left <= 0 {
			continue
		}
		part := min(left, x.DueKobo)
		entry := c.entry
		reason := CreditReason
		if !c.own {
			reason = FamilyShareReason
		}
		y, b, err := s.RecordPayment(ctx, tx, tenant, x.ID.String(), PaymentIn{AmountKobo: part, PaymentID: entry.String(), EntryID: &entry,
			Connection: c.channel, How: "exact", By: "system:credit", Reasons: []string{reason}})
		if err != nil {
			return x, applied, became, err
		}
		x, applied, became = y, applied+part, became || b
	}
	return x, applied, became, nil
}
