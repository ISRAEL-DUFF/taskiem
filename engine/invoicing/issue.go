package invoicing

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/effects"
)

// Issuing a customer's permanent account number on a rail
// (docs/matching.md#customers-own-account-numbers; docs/invoices.md): the
// one path every account number is issued on. IssueOnRail asks the rail
// (virtual_account) and reads its answer; who it is for and how the
// issue is claimed is the caller's: a school's students (engine/schools,
// which keeps a row per student and connection) or any customer of the
// business (IssueAccount below, the invoices@1 register_account action
// and the web app's "Give this customer an account number").

// IssueIn is what issuing a customer's account on a rail takes.
type IssueIn struct {
	Customer Customer
	// AccountName is the name asked for, where the rail takes one
	// (AccountName gives the business's and the customer's).
	AccountName string
	// Account is the wallet the number pays into, on rails that hold
	// several (iswallet).
	Account string
	// BVN or NIN is the customer's (for a student, a parent's), for rails
	// that need one per account (Flutterwave): sent to the rail, never
	// kept.
	BVN, NIN string
}

// Issued is a rail's answer for an account issued.
type Issued struct {
	AccountNumber string
	BankName      string
	// AccountName is the name the rail answered, when it says one.
	AccountName string
	// Reference is what the account's credits carry.
	Reference string
}

// IssueOnRail asks rail r for a permanent account for the customer, keyed
// by key (the same key answers the same account on rails that take one),
// and reads its answer. The rail's refusal is answered as it came
// (effects.ErrFatal, ErrRetryable, ErrUnknownOutcome...).
func IssueOnRail(ctx context.Context, rails CapabilityRails, tenant uuid.UUID, r Rail, in IssueIn, key effects.KeyInput) (Issued, error) {
	customer := map[string]any{"name": in.Customer.Name}
	if in.Customer.Email != "" {
		customer["email"] = in.Customer.Email
	}
	if in.Customer.Phone != "" {
		customer["phone"] = in.Customer.Phone
	}
	if in.BVN != "" {
		customer["bvn"] = in.BVN
	}
	if in.NIN != "" {
		customer["nin"] = in.NIN
	}
	req := map[string]any{"customer": customer}
	if in.AccountName != "" && r.Takes("virtual_account", "account_name") {
		req["account_name"] = in.AccountName
	}
	if in.Account != "" && r.Takes("virtual_account", "account") {
		req["account"] = in.Account
	}
	out, err := rails.Call(ctx, tenant, r, "virtual_account", req, key)
	if err != nil {
		return Issued{}, err
	}
	str := func(k string) string { v, _ := out[k].(string); return strings.TrimSpace(v) }
	return Issued{AccountNumber: str("account_number"), BankName: str("bank_name"), AccountName: str("account_name"), Reference: str("reference")}, nil
}

// CustomerAccountKey is the key a customer's account on a connection is
// issued under: customer:<id>:acct:<connection>, so asking again answers
// the same account on rails that take a key.
func CustomerAccountKey(tenant uuid.UUID, customer, channel string) effects.KeyInput {
	return effects.KeyInput{TenantID: tenant.String(), Seed: "customer:" + customer, StepID: "acct:" + channel}
}

// Issuer is a connection of the business that can issue customers'
// account numbers, and what its rail needs for each.
type Issuer struct {
	Connection string `json:"connection"`
	Rail       string `json:"rail"`
	// NeedsEmail: the rail opens no account without the customer's email.
	NeedsEmail bool `json:"needs_email"`
	// NeedsBVN: the rail needs the customer's BVN or NIN (typed in, never
	// kept).
	NeedsBVN bool `json:"needs_bvn"`
	// NeedsAccount: the wallet the numbers pay into must be chosen.
	NeedsAccount bool `json:"needs_account"`
	// NamesItself: payers see the rail's name for the account, whatever
	// is asked.
	NamesItself bool `json:"names_itself"`
}

func issuerOf(r Rail) Issuer {
	return Issuer{Connection: r.Channel(), Rail: r.Name, NeedsEmail: r.Needs("virtual_account", "customer.email"),
		NeedsBVN:     r.Needs("virtual_account", "customer.bvn") || r.Needs("virtual_account", "customer.nin"),
		NeedsAccount: r.Needs("virtual_account", "account"), NamesItself: !r.Takes("virtual_account", "account_name")}
}

// accountRails are AccountRails, else Rails when they can issue accounts;
// nil without either.
func (s *Invoices) accountRails() CapabilityRails {
	if s.AccountRails != nil {
		return s.AccountRails
	}
	if r, ok := s.Rails.(CapabilityRails); ok {
		return r
	}
	return nil
}

// Issuers lists the business's active prod connections that can issue
// customers' account numbers. None when the server has no rails.
func (s *Invoices) Issuers(ctx context.Context, tenant uuid.UUID) ([]Issuer, error) {
	out := []Issuer{}
	rails := s.accountRails()
	if rails == nil {
		return out, nil
	}
	list, err := rails.Capable(ctx, tenant, "virtual_account")
	for _, r := range list {
		out = append(out, issuerOf(r))
	}
	return out, err
}

// AccountRequest is a customer's permanent account to give them: one the
// business already has for them (AccountNumber and Reference, registered
// as it is), or, with Issue, one issued on the connection.
type AccountRequest struct {
	Customer Customer `json:"customer"`
	// Channel is the connection, as connector/name: "paystack/main".
	Channel string `json:"connection"`
	// Issue asks the connection for the account.
	Issue bool `json:"issue,omitempty"`
	// AccountNumber, Reference and BankName are the account the business
	// has (not with Issue).
	AccountNumber string `json:"account_number,omitempty"`
	Reference     string `json:"reference,omitempty"`
	BankName      string `json:"bank_name,omitempty"`
	// AccountName is the name asked for when issuing, where the rail
	// takes one; the business's and the customer's when left out.
	AccountName string `json:"account_name,omitempty"`
	// Account is the wallet the number pays into, on rails that hold
	// several.
	Account string `json:"account,omitempty"`
	// BVN or NIN, for rails that need one per account; never kept.
	BVN string `json:"bvn,omitempty"`
	NIN string `json:"nin,omitempty"`
}

// checkCustomer is what every account's customer must have: a name and
// what their invoices know them by.
func checkCustomer(c Customer) error {
	switch {
	case c.Name == "" || len(c.Name) > 120:
		return invalid("the customer's name is required (up to 120 characters)")
	case c.Phone == "" && c.Email == "" && c.Key == "":
		return invalid("give the customer's phone number, email or key, as their invoices have it")
	case !ValidCustomerKey(c.Key):
		return invalid("the customer's key has up to 100 letters, digits and ':', '.', '_', '/' or '-'")
	}
	return nil
}

// IssueAccount issues a customer's permanent account on the connection
// (virtual_account) and registers it for them (RegisterAccount), once per
// customer and connection: a customer who has one there is answered it.
// The rail's key is derived from the customer and the connection
// (CustomerAccountKey), so a lost answer asked again gives the same
// account on rails that take a key.
func (s *Invoices) IssueAccount(ctx context.Context, tenant uuid.UUID, in AccountRequest, by string) (CustomerAccount, error) {
	rails := s.accountRails()
	if rails == nil {
		return CustomerAccount{}, fmt.Errorf("%w: this server cannot issue account numbers", ErrNoRail)
	}
	in.Customer.Name = strings.TrimSpace(in.Customer.Name)
	in.Channel, in.Account, in.AccountName = strings.TrimSpace(in.Channel), strings.TrimSpace(in.Account), strings.TrimSpace(in.AccountName)
	in.BVN, in.NIN = strings.TrimSpace(in.BVN), strings.TrimSpace(in.NIN)
	if err := checkCustomer(in.Customer); err != nil {
		return CustomerAccount{}, err
	}
	if in.Channel == "" {
		return CustomerAccount{}, invalid("name the connection to issue the account on, like paystack/main")
	}
	r, err := rails.ResolveChannel(ctx, tenant, in.Channel, "virtual_account")
	if errors.Is(err, ErrNoRail) {
		return CustomerAccount{}, invalid("%s is not an active connection in prod that can issue account numbers: connect one, or choose another", in.Channel)
	}
	if err != nil {
		return CustomerAccount{}, err
	}
	for _, v := range []string{in.BVN, in.NIN} {
		if v != "" && !elevenDigits(v) {
			return CustomerAccount{}, invalid("a BVN or NIN has 11 digits")
		}
	}
	switch {
	case r.Needs("virtual_account", "customer.email") && in.Customer.Email == "":
		return CustomerAccount{}, invalid("%s needs the customer's email for an account number", r.Name)
	case r.Needs("virtual_account", "customer.phone") && in.Customer.Phone == "":
		return CustomerAccount{}, invalid("%s needs the customer's phone number for an account number", r.Name)
	case (r.Needs("virtual_account", "customer.bvn") || r.Needs("virtual_account", "customer.nin")) && in.BVN == "" && in.NIN == "":
		return CustomerAccount{}, invalid("%s needs the customer's BVN or NIN for an account number", r.Name)
	case r.Needs("virtual_account", "account") && in.Account == "":
		return CustomerAccount{}, invalid("%s needs the wallet the number pays into", r.Name)
	case len(in.Account) > 200:
		return CustomerAccount{}, invalid("the wallet's id has up to 200 characters")
	}
	channel := r.Channel()
	var have *CustomerAccount
	var customer, business string
	err = db.InTenantTx(ctx, s.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		have = nil
		var err error
		if _, customer, err = s.sealCustomer(ctx, tx, tenant, in.Customer); err != nil {
			return err
		}
		list, err := s.AccountsOf(ctx, tx, tenant, customer)
		if err != nil {
			return err
		}
		for i := range list {
			if list[i].Channel == channel {
				have = &list[i]
				return nil
			}
		}
		return tx.QueryRow(ctx, `SELECT name FROM tenants WHERE id = $1`, tenant).Scan(&business)
	})
	if err != nil {
		return CustomerAccount{}, err
	}
	if have != nil {
		return *have, nil
	}
	if in.AccountName == "" {
		in.AccountName, _, _ = AccountName(business, in.Customer.Name, 0)
	}
	issued, err := IssueOnRail(ctx, rails, tenant, r, IssueIn{Customer: in.Customer, AccountName: in.AccountName, Account: in.Account, BVN: in.BVN, NIN: in.NIN},
		CustomerAccountKey(tenant, customer, channel))
	if err != nil {
		return CustomerAccount{}, err
	}
	if issued.Reference == "" {
		issued.Reference = issued.AccountNumber
	}
	var out CustomerAccount
	err = db.InTenantTx(ctx, s.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		var err error
		out, err = s.RegisterAccount(ctx, tx, tenant, AccountIn{Customer: in.Customer, Channel: channel, AccountNumber: issued.AccountNumber,
			Reference: issued.Reference, BankName: issued.BankName}, by)
		if err != nil {
			return err
		}
		// Its credits are read back (the look-back), on its wallet.
		if r.Maps("list_credits") {
			_, err = tx.Exec(ctx, `INSERT INTO money_lookbacks (tenant_id, channel, account) VALUES ($1, $2, $3) ON CONFLICT DO NOTHING`, tenant, channel, in.Account)
		}
		return err
	})
	return out, err
}

func elevenDigits(s string) bool {
	if len(s) != 11 {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

// GiveAccount gives a customer a permanent account: issues one on the
// connection (Issue), or registers the one given. What the invoices
// platform connector and the web app call.
func (s Service) GiveAccount(ctx context.Context, tenant uuid.UUID, in AccountRequest, by string) (CustomerAccount, error) {
	if in.Issue {
		return s.IssueAccount(ctx, tenant, in, by)
	}
	var out CustomerAccount
	err := s.in(ctx, tenant, func(tx pgx.Tx) error {
		var err error
		out, err = s.RegisterAccount(ctx, tx, tenant, AccountIn{Customer: in.Customer, Channel: in.Channel, AccountNumber: in.AccountNumber,
			Reference: in.Reference, BankName: in.BankName}, by)
		return err
	})
	return out, err
}
