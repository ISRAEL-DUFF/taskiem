package invoicing

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/effects"
	"github.com/israel-duff/taskiem/engine/pii"
)

// A customer's payment link (decision 0060; docs/spec/no-sheet-needed.md
// §6.4, R5; migration 00274): one page per customer, /c/{token} on the
// form host, listing what they owe across their open invoices and taking
// a payment towards the total, part or whole. It is made once and kept
// with the customer, as a registered account number is; its account
// numbers and card links are the invoices' (Issue), and a payment through
// one is the customer's money, counted exactly towards their open
// invoices oldest first, the rest their credit (LinkReceived; engine/
// matching). No invoice is folded into another or voided.

// CustomerTokenHash is what finds a customer's page from its link's
// token.
func CustomerTokenHash(token string) []byte {
	h := sha256.Sum256([]byte("taskiem customer page " + token))
	return h[:]
}

// CustomerLink is the customer's payment link (customer as
// Invoice.CustomerID), made the first time and given again after; "" when
// this server serves no pages. Making one needs an open invoice of
// theirs: the token is sealed under its customer's subject.
func (s *Invoices) CustomerLink(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, customer, by string) (string, error) {
	if s.PageBase == "" {
		return "", nil
	}
	if customer == "" {
		return "", invalid("the invoice has no customer to link")
	}
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext('customer-link-make:' || $1::text || $2))`, tenant, customer); err != nil {
		return "", err
	}
	var raw []byte
	err := tx.QueryRow(ctx, `SELECT page_token FROM invoice_customer_links WHERE tenant_id = $1 AND customer_id = $2`, tenant, customer).Scan(&raw)
	switch {
	case err == nil:
		var env map[string]any
		_ = json.Unmarshal(raw, &env)
		v, err := s.Vault.OpenTx(ctx, tx, tenant, env)
		if errors.Is(err, pii.ErrErased) {
			return "", fmt.Errorf("%w: the customer was erased", ErrState)
		}
		if err != nil {
			return "", err
		}
		t, _ := v.(string)
		return s.customerURL(t), nil
	case !errors.Is(err, pgx.ErrNoRows):
		return "", err
	}
	open, err := s.OpenInvoicesOf(ctx, tx, tenant, customer)
	if err != nil {
		return "", err
	}
	if len(open) == 0 {
		return "", fmt.Errorf("%w: the customer owes nothing", ErrState)
	}
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	t := base64.RawURLEncoding.EncodeToString(b)
	env, err := s.Vault.SealUnderTx(ctx, tx, tenant, open[0].subject, "customer_link", t)
	if err != nil {
		return "", err
	}
	sealed, _ := json.Marshal(env)
	if _, err := tx.Exec(ctx, `INSERT INTO invoice_customer_links (id, tenant_id, customer_id, page_token, page_token_hash, created_by) VALUES ($1, $2, $3, $4, $5, $6)`,
		uuid.New(), tenant, customer, sealed, CustomerTokenHash(t), by); err != nil {
		return "", err
	}
	return s.customerURL(t), nil
}

func (s *Invoices) customerURL(token string) string {
	return strings.TrimRight(s.PageBase, "/") + "/c/" + token
}

// LinkCustomer is whose a customer link is (as Invoice.CustomerID).
func LinkCustomer(ctx context.Context, tx pgx.Tx, tenant, link uuid.UUID) (string, error) {
	var customer string
	err := tx.QueryRow(ctx, `SELECT customer_id FROM invoice_customer_links WHERE tenant_id = $1 AND id = $2`, tenant, link).Scan(&customer)
	return customer, err
}

// CustomerPaymentKey is the key of a customer link's seq'th account or
// card link of a kind.
func CustomerPaymentKey(tenant, link uuid.UUID, kind string, seq int) effects.KeyInput {
	return effects.KeyInput{TenantID: tenant.String(), Seed: "customer-link:" + link.String(), StepID: fmt.Sprintf("%s:%d", kind, seq)}
}

// IssueForCustomer gives a customer's link an account number (kind
// transfer) or a card link (kind link) for amountKobo (0: all they owe),
// as Issue gives an invoice one: any part of at least ₦1,000 (or what is
// left), an open one for the same amount given again, the invoices'
// limits per link and kind, and the row with the payment id written
// before the rail is asked. The rail is the oldest open invoice's.
func (s *Invoices) IssueForCustomer(ctx context.Context, tenant, link uuid.UUID, kind string, amountKobo int64) (Account, error) {
	if s.Rails == nil {
		return Account{}, fmt.Errorf("%w: payments are not set up on this server", ErrNoRail)
	}
	capab := map[string]string{"transfer": "collect_transfer", "link": "collect_link"}[kind]
	if capab == "" {
		return Account{}, invalid("kind is transfer or link")
	}
	var a Account
	var oldest Invoice
	var rail Rail
	var lines int
	fresh := false
	err := db.InTenantTx(ctx, s.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		// One at a time for a link, as its page's requests are.
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext('customer-link:' || $1::text))`, link); err != nil {
			return err
		}
		customer, err := LinkCustomer(ctx, tx, tenant, link)
		if err != nil {
			return err
		}
		open, err := s.OpenInvoicesOf(ctx, tx, tenant, customer)
		if err != nil {
			return err
		}
		var due int64
		for _, x := range open {
			due += x.DueKobo
		}
		if due <= 0 {
			return fmt.Errorf("%w: the customer owes nothing", ErrState)
		}
		oldest, lines = open[0], len(open)
		amount := amountKobo
		if amount == 0 {
			amount = due
		}
		if amount > due || amount < min(MinPartKobo, due) {
			return invalid("pay between %s and %s", Naira(min(MinPartKobo, due)), Naira(due))
		}
		rows, err := tx.Query(ctx, `SELECT `+accountCols+` FROM customer_invoice_accounts WHERE customer_link_id = $1 AND kind = $2 ORDER BY seq DESC`, link, kind)
		if err != nil {
			return err
		}
		list, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (Account, error) { return scanAccount(r) })
		if err != nil {
			return err
		}
		now := s.now()
		hour := 0
		for _, o := range list {
			if o.AmountKobo == amount && (o.Open(now) || o.Status == "requested" && o.CreatedAt.After(now.Add(-time.Minute))) {
				a = o
				return nil
			}
			if o.CreatedAt.After(now.Add(-time.Hour)) {
				hour++
			}
		}
		if hour >= MaxPerHour || len(list) >= MaxPerInvoice {
			return ErrLimit
		}
		if rail, err = s.Rails.Resolve(ctx, tenant, oldest.Connection); err != nil {
			return err
		}
		if kind == "transfer" && rail.Transfer == nil || kind == "link" && !rail.Link {
			return fmt.Errorf("%w: %s cannot %s", ErrNoRail, rail.Name, map[string]string{"transfer": "open account numbers for one payment", "link": "make payment links"}[kind])
		}
		seq := len(list) + 1
		pid, err := s.Rails.Key(rail, capab, CustomerPaymentKey(tenant, link, kind, seq))
		if err != nil {
			return err
		}
		a = Account{ID: uuid.New(), Seq: seq, Kind: kind, Connector: rail.Connector, Connection: rail.Connection, PaymentID: pid, AmountKobo: amount, Status: "requested", CreatedAt: now}
		if rail.Transfer != nil && kind == "transfer" {
			a.OnLate, a.OnWrongAmount = rail.Transfer.OnLate, rail.Transfer.OnWrongAmount
		}
		fresh = true
		_, err = tx.Exec(ctx, `INSERT INTO customer_invoice_accounts (id, tenant_id, customer_link_id, seq, kind, connector, connection, payment_id, amount_kobo, on_late, on_wrong_amount)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)`,
			a.ID, tenant, link, a.Seq, kind, a.Connector, a.Connection, a.PaymentID, amount, nullable(a.OnLate), nullable(a.OnWrongAmount))
		return err
	})
	if err != nil || !fresh {
		return a, err
	}
	in := map[string]any{"amount_kobo": a.AmountKobo, "customer": s.payer(oldest), "description": fmt.Sprintf("Balance: %d invoice(s), the oldest %s", lines, oldest.Number)}
	if kind == "transfer" {
		in["expires_minutes"] = min(max(AccountMinutes, rail.Transfer.MinMinutes), rail.Transfer.MaxMinutes)
	}
	return s.ask(ctx, tenant, a, rail, capab, in, CustomerPaymentKey(tenant, link, kind, a.Seq))
}

// BalanceSent is what SendBalance did.
type BalanceSent struct {
	// DueKobo is what the customer owes in all; Numbers their open
	// invoices, oldest first; Sent the recorded lines it moved to sent.
	DueKobo int64    `json:"due_kobo"`
	Numbers []string `json:"numbers"`
	Sent    []string `json:"sent"`
	// Account is the customer's own account number the message named,
	// else Link their payment link (either may be empty: no way to pay
	// online, and the message says to ask the business).
	Account string   `json:"account,omitempty"`
	Link    string   `json:"link,omitempty"`
	Queued  []string `json:"queued"`
	// SMSOverAllowance: the SMS was left out, past the plan's allowance.
	SMSOverAllowance bool `json:"sms_over_allowance,omitempty"`
}

// SendBalance sends a customer one message with what they owe across
// their open invoices and one way to pay it: their own account number
// where they have one, else their payment link (CustomerLink). Their
// recorded lines are sent with it, together; nothing is folded or
// voided. It needs a phone or an email on the customer's invoices; a key
// makes the message go once.
func (s *Invoices) SendBalance(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, customer, by, key string) (BalanceSent, error) {
	out := BalanceSent{Numbers: []string{}, Sent: []string{}, Queued: []string{}}
	open, err := s.OpenInvoicesOf(ctx, tx, tenant, customer)
	if err != nil {
		return out, err
	}
	if len(open) == 0 {
		return out, fmt.Errorf("%w: the customer owes nothing", ErrState)
	}
	// The newest line carries the address given last.
	to := open[len(open)-1]
	if to.Customer.Phone == "" && to.Customer.Email == "" {
		return out, invalid("the customer has no phone number or email to send to")
	}
	for i, x := range open {
		if x.SentAt == nil {
			if _, err := tx.Exec(ctx, `UPDATE customer_invoices SET sent_at = now(), updated_at = now() WHERE tenant_id = $1 AND id = $2`, tenant, x.ID); err != nil {
				return out, err
			}
			if open[i], err = s.Get(ctx, tx, tenant, x.ID.String()); err != nil {
				return out, err
			}
			out.Sent = append(out.Sent, x.Number)
		}
		out.DueKobo += open[i].DueKobo
		out.Numbers = append(out.Numbers, x.Number)
	}
	accts, err := s.AccountsOf(ctx, tx, tenant, customer)
	if err != nil {
		return out, err
	}
	var pay string
	if len(accts) > 0 {
		a := accts[len(accts)-1]
		out.Account = a.AccountNumber
		pay = "Pay into your account number " + a.AccountNumber
		if a.BankName != "" {
			pay += " (" + a.BankName + ")"
		}
		pay += "."
	} else if out.Link, err = s.CustomerLink(ctx, tx, tenant, customer, by); err != nil {
		return out, err
	} else if out.Link != "" {
		pay = "See them and pay: " + out.Link
	}
	var business string
	if err := tx.QueryRow(ctx, `SELECT name FROM tenants WHERE id = $1`, tenant).Scan(&business); err != nil {
		return out, err
	}
	text := balanceText(business, open, out.DueKobo, pay)
	if key == "" {
		key = s.now().UTC().Format(time.RFC3339Nano)
	}
	if out.Queued, out.SMSOverAllowance, err = s.deliver(ctx, tx, tenant, to, business, text, "What you owe "+business, "balance:"+customer+":"+key); err != nil {
		return out, err
	}
	err = audit(ctx, tx, tenant, by, "invoice.balance_sent", open[0].Number, map[string]any{"invoices": out.Numbers, "sent": out.Sent,
		"due_kobo": out.DueKobo, "channels": out.Queued})
	return out, err
}

// balanceText is the balance message: short, as an SMS, its first five
// invoices named.
func balanceText(business string, open []Invoice, due int64, pay string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s: you owe %s in all", business, Naira(due))
	var parts []string
	for i, x := range open {
		if i == 5 {
			parts = append(parts, fmt.Sprintf("%d more", len(open)-5))
			break
		}
		parts = append(parts, x.Number+" "+Naira(x.DueKobo))
	}
	fmt.Fprintf(&b, " (%s).", strings.Join(parts, ", "))
	if pay == "" {
		pay = "Ask us how to pay."
	}
	b.WriteString(" " + pay)
	return b.String()
}

// CustomerLinkFor is the payment link of an invoice's customer
// (CustomerLink) and what they owe on all their open invoices: the
// invoices connector's customer_link.
func (s Service) CustomerLinkFor(ctx context.Context, tenant uuid.UUID, number, by string) (string, int64, error) {
	var link string
	var due int64
	err := s.in(ctx, tenant, func(tx pgx.Tx) error {
		x, err := s.Get(ctx, tx, tenant, number)
		if err != nil {
			return err
		}
		if s.PageBase == "" {
			return fmt.Errorf("%w: this server serves no customer pages", ErrState)
		}
		if link, err = s.CustomerLink(ctx, tx, tenant, x.CustomerID(), by); err != nil {
			return err
		}
		open, err := s.OpenInvoicesOf(ctx, tx, tenant, x.CustomerID())
		for _, o := range open {
			due += o.DueKobo
		}
		return err
	})
	return link, due, err
}
