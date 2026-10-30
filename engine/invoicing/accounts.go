package invoicing

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/effects"
	"github.com/israel-duff/taskiem/engine/moneylog"
)

// Paying an invoice (decision 0045, T4): one-time account numbers
// (collect_transfer) and payment links (collect_link) issued for an
// invoice on request, from its page or chat, and their payments counted
// towards it when the rail reports them paid.

// Account is an account number or payment link issued for an invoice.
type Account struct {
	ID            uuid.UUID  `json:"id"`
	Seq           int        `json:"seq"`
	Kind          string     `json:"kind"` // transfer or link
	Connector     string     `json:"connector"`
	Connection    string     `json:"connection"`
	PaymentID     string     `json:"payment_id"`
	AmountKobo    int64      `json:"amount_kobo"`
	Status        string     `json:"status"` // requested, open, failed, paid, returned
	AccountNumber string     `json:"account_number,omitempty"`
	BankName      string     `json:"bank_name,omitempty"`
	AccountName   string     `json:"account_name,omitempty"`
	PaymentURL    string     `json:"payment_url,omitempty"`
	ExpiresAt     *time.Time `json:"expires_at,omitempty"`
	OnLate        string     `json:"on_late,omitempty"`
	OnWrongAmount string     `json:"on_wrong_amount,omitempty"`
	Error         string     `json:"error,omitempty"`
	CreatedAt     time.Time  `json:"created_at"`
}

// Limits on accounts and links per invoice and kind.
const (
	MaxPerHour    = 5
	MaxPerInvoice = 20
	// AccountMinutes is the lifetime asked for an account, kept within
	// the rail's range.
	AccountMinutes = 30
	// WatchAfterExpiry is how long an account is still checked for a
	// payment after it expires: a transfer started in time can land late.
	WatchAfterExpiry = 48 * time.Hour
	// WatchLinks is how long a payment link is checked.
	WatchLinks = 7 * 24 * time.Hour
	// MinPartKobo is the least a customer may pay of an invoice that
	// takes part payments (or the balance, when that is less).
	MinPartKobo = 100000
)

// ErrLimit refuses an account past the limits.
var ErrLimit = errors.New("invoicing: too many account numbers for this invoice")

const accountCols = `id, seq, kind, connector, connection, payment_id, amount_kobo, status, COALESCE(account_number, ''), COALESCE(bank_name, ''),
	COALESCE(account_name, ''), COALESCE(payment_url, ''), expires_at, COALESCE(on_late, ''), COALESCE(on_wrong_amount, ''), COALESCE(error, ''), created_at`

func scanAccount(r pgx.Row) (Account, error) {
	var a Account
	err := r.Scan(&a.ID, &a.Seq, &a.Kind, &a.Connector, &a.Connection, &a.PaymentID, &a.AmountKobo, &a.Status, &a.AccountNumber, &a.BankName,
		&a.AccountName, &a.PaymentURL, &a.ExpiresAt, &a.OnLate, &a.OnWrongAmount, &a.Error, &a.CreatedAt)
	return a, err
}

// Accounts lists the accounts and links issued for an invoice, newest
// first.
func Accounts(ctx context.Context, tx pgx.Tx, invoice uuid.UUID) ([]Account, error) {
	rows, err := tx.Query(ctx, `SELECT `+accountCols+` FROM customer_invoice_accounts WHERE invoice_id = $1 ORDER BY created_at DESC, seq DESC`, invoice)
	if err != nil {
		return nil, err
	}
	out, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (Account, error) { return scanAccount(r) })
	if out == nil {
		out = []Account{}
	}
	return out, err
}

// Open reports whether an account can still be paid into, at now.
func (a Account) Open(now time.Time) bool {
	return a.Status == "open" && (a.ExpiresAt == nil || a.ExpiresAt.After(now.Add(time.Minute)))
}

// Issue gives the invoice an account number (kind transfer) or a payment
// link (kind link) for amountKobo (0: what is due). An open one for the
// same amount is given again rather than a new one. The account's row,
// with the payment id the rail will report, is written before the rail
// is asked, so money paid into it is found even if the answer is lost.
func (s *Invoices) Issue(ctx context.Context, tenant, invoice uuid.UUID, kind string, amountKobo int64) (Account, error) {
	if s.Rails == nil {
		return Account{}, fmt.Errorf("%w: payments are not set up on this server", ErrNoRail)
	}
	capab := map[string]string{"transfer": "collect_transfer", "link": "collect_link"}[kind]
	if capab == "" {
		return Account{}, invalid("kind is transfer or link")
	}
	var a Account
	var x Invoice
	var rail Rail
	fresh := false
	err := db.InTenantTx(ctx, s.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT 1 FROM customer_invoices WHERE tenant_id = $1 AND id = $2 FOR UPDATE`, tenant, invoice); err != nil {
			return err
		}
		var err error
		if x, err = s.Get(ctx, tx, tenant, invoice.String()); err != nil {
			return err
		}
		switch x.Status {
		case "sent", "part_paid", "overdue":
		default:
			return fmt.Errorf("%w: invoice %s is %s", ErrState, x.Number, x.Status)
		}
		amount := amountKobo
		if amount == 0 {
			amount = x.DueKobo
		}
		if amount != x.DueKobo && (!x.AllowPartPayments || amount > x.DueKobo || amount < min(MinPartKobo, x.DueKobo)) {
			if !x.AllowPartPayments {
				return invalid("this invoice is paid in full: %s", Naira(x.DueKobo))
			}
			return invalid("pay between %s and %s", Naira(min(MinPartKobo, x.DueKobo)), Naira(x.DueKobo))
		}
		list, err := Accounts(ctx, tx, invoice)
		if err != nil {
			return err
		}
		now := s.now()
		hour, total := 0, 0
		for _, o := range list {
			if o.Kind != kind {
				continue
			}
			if o.AmountKobo == amount && (o.Open(now) || o.Status == "requested" && o.CreatedAt.After(now.Add(-time.Minute))) {
				a = o
				return nil
			}
			total++
			if o.CreatedAt.After(now.Add(-time.Hour)) {
				hour++
			}
		}
		if hour >= MaxPerHour || total >= MaxPerInvoice {
			return ErrLimit
		}
		if rail, err = s.Rails.Resolve(ctx, tenant, x.Connection); err != nil {
			return err
		}
		if kind == "transfer" && rail.Transfer == nil || kind == "link" && !rail.Link {
			return fmt.Errorf("%w: %s cannot %s", ErrNoRail, rail.Name, map[string]string{"transfer": "open account numbers for one payment", "link": "make payment links"}[kind])
		}
		seq := total + 1
		pid, err := s.Rails.Key(rail, capab, PaymentKey(tenant, invoice, kind, seq))
		if err != nil {
			return err
		}
		a = Account{ID: uuid.New(), Seq: seq, Kind: kind, Connector: rail.Connector, Connection: rail.Connection, PaymentID: pid, AmountKobo: amount, Status: "requested", CreatedAt: now}
		if rail.Transfer != nil && kind == "transfer" {
			a.OnLate, a.OnWrongAmount = rail.Transfer.OnLate, rail.Transfer.OnWrongAmount
		}
		fresh = true
		_, err = tx.Exec(ctx, `INSERT INTO customer_invoice_accounts (id, tenant_id, invoice_id, seq, kind, connector, connection, payment_id, amount_kobo, on_late, on_wrong_amount)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)`,
			a.ID, tenant, invoice, a.Seq, kind, a.Connector, a.Connection, a.PaymentID, amount, nullable(a.OnLate), nullable(a.OnWrongAmount))
		return err
	})
	if err != nil || !fresh {
		return a, err
	}
	in := map[string]any{"amount_kobo": a.AmountKobo, "customer": s.payer(x), "description": "Invoice " + x.Number}
	if kind == "transfer" {
		in["expires_minutes"] = min(max(AccountMinutes, rail.Transfer.MinMinutes), rail.Transfer.MaxMinutes)
	}
	return s.ask(ctx, tenant, a, rail, capab, in, PaymentKey(tenant, invoice, kind, a.Seq))
}

// ask asks the rail for an account or link whose row is written
// (requested), and records what it answered.
func (s *Invoices) ask(ctx context.Context, tenant uuid.UUID, a Account, rail Rail, capab string, in map[string]any, key effects.KeyInput) (Account, error) {
	out, err := s.Rails.Call(ctx, tenant, rail, capab, in, key)
	if err != nil {
		// Unknown outcome: the row stays requested and the checker looks
		// the payment id up; anything else failed and says why.
		status := "failed"
		if errors.Is(err, effects.ErrUnknownOutcome) || errors.Is(err, effects.ErrIndeterminate) {
			status = "requested"
		}
		_ = s.setAccount(ctx, tenant, a.ID, map[string]any{"status": status, "error": shortErr(err)})
		return a, err
	}
	set := map[string]any{"status": "open"}
	str := func(k string) string { v, _ := out[k].(string); return v }
	if v := str("payment_id"); v != "" && v != a.PaymentID {
		set["payment_id"] = v
		a.PaymentID = v
	}
	if a.Kind == "transfer" {
		a.AccountNumber, a.BankName, a.AccountName = str("account_number"), str("bank_name"), str("account_name")
		if t, err := time.Parse(time.RFC3339, str("expires_at")); err == nil {
			a.ExpiresAt = &t
			set["expires_at"] = t
		}
		set["account_number"], set["bank_name"], set["account_name"] = a.AccountNumber, a.BankName, nullable(a.AccountName)
	} else {
		a.PaymentURL = str("payment_url")
		set["payment_url"] = a.PaymentURL
	}
	a.Status = "open"
	if err := s.setAccount(ctx, tenant, a.ID, set); err != nil {
		return a, err
	}
	return a, nil
}

// OrderTransfer is what a storefront order's one-time account number is
// asked for with (N3): the storefront checks the order and says how much,
// on which connection, and for how long.
type OrderTransfer struct {
	Connection  string
	AmountKobo  int64
	Description string
	Payer       map[string]any // name, phone, email
	Minutes     int            // the window wanted, kept within the rail's range
}

// OrderPaymentKey is the key of an order's seq'th account number.
func OrderPaymentKey(tenant, order uuid.UUID, seq int) effects.KeyInput {
	return effects.KeyInput{TenantID: tenant.String(), Seed: "order:" + order.String(), StepID: fmt.Sprintf("transfer:%d", seq)}
}

// IssueForOrder gives a storefront order a one-time account number, as
// Issue gives an invoice one: an open one for the same amount is given
// again, the limits are the invoices', and the row with the payment id is
// written before the rail is asked. The caller has checked the order.
func (s *Invoices) IssueForOrder(ctx context.Context, tenant, order uuid.UUID, t OrderTransfer) (Account, error) {
	if s.Rails == nil {
		return Account{}, fmt.Errorf("%w: payments are not set up on this server", ErrNoRail)
	}
	if t.AmountKobo < 100 {
		return Account{}, invalid("nothing to pay")
	}
	var a Account
	var rail Rail
	fresh := false
	err := db.InTenantTx(ctx, s.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		// One at a time for an order.
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext('order-accounts:' || $1::text))`, order); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `SELECT `+accountCols+` FROM customer_invoice_accounts WHERE order_id = $1 AND kind = 'transfer' ORDER BY seq DESC`, order)
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
			if o.AmountKobo == t.AmountKobo && (o.Open(now) || o.Status == "requested" && o.CreatedAt.After(now.Add(-time.Minute))) {
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
		if rail, err = s.Rails.Resolve(ctx, tenant, t.Connection); err != nil {
			return err
		}
		if rail.Transfer == nil {
			return fmt.Errorf("%w: %s cannot open account numbers for one payment", ErrNoRail, rail.Name)
		}
		seq := len(list) + 1
		pid, err := s.Rails.Key(rail, "collect_transfer", OrderPaymentKey(tenant, order, seq))
		if err != nil {
			return err
		}
		a = Account{ID: uuid.New(), Seq: seq, Kind: "transfer", Connector: rail.Connector, Connection: rail.Connection, PaymentID: pid, AmountKobo: t.AmountKobo,
			Status: "requested", OnLate: rail.Transfer.OnLate, OnWrongAmount: rail.Transfer.OnWrongAmount, CreatedAt: now}
		fresh = true
		_, err = tx.Exec(ctx, `INSERT INTO customer_invoice_accounts (id, tenant_id, order_id, seq, kind, connector, connection, payment_id, amount_kobo, on_late, on_wrong_amount)
			VALUES ($1, $2, $3, $4, 'transfer', $5, $6, $7, $8, $9, $10)`,
			a.ID, tenant, order, a.Seq, a.Connector, a.Connection, a.PaymentID, a.AmountKobo, nullable(a.OnLate), nullable(a.OnWrongAmount))
		return err
	})
	if err != nil || !fresh {
		return a, err
	}
	in := map[string]any{"amount_kobo": a.AmountKobo, "customer": t.Payer, "description": t.Description,
		"expires_minutes": min(max(t.Minutes, rail.Transfer.MinMinutes), rail.Transfer.MaxMinutes)}
	return s.ask(ctx, tenant, a, rail, "collect_transfer", in, OrderPaymentKey(tenant, order, a.Seq))
}

// payer is what the rail is told of the customer. Rails that need an
// email (Paystack) get the invoice's own address at the page's domain
// when the customer gave none.
func (s *Invoices) payer(x Invoice) map[string]any {
	c := map[string]any{"name": x.Customer.Name}
	if x.Customer.Phone != "" {
		c["phone"] = x.Customer.Phone
	}
	email := x.Customer.Email
	if email == "" {
		host := "invoices.taskiem.invalid"
		if u, err := url.Parse(s.PageBase); err == nil && u.Hostname() != "" {
			host = u.Hostname()
		}
		email = "invoices+" + strings.ToLower(strings.NewReplacer("/", "", "-", "", "_", "").Replace(x.Number)) + "@" + host
	}
	c["email"] = email
	return c
}

func shortErr(err error) string {
	m := err.Error()
	if len(m) > 300 {
		m = m[:300]
	}
	return m
}

var accountColumns = map[string]bool{"status": true, "payment_id": true, "account_number": true, "bank_name": true, "account_name": true,
	"payment_url": true, "expires_at": true, "error": true, "checked_at": true}

func (s *Invoices) setAccount(ctx context.Context, tenant, id uuid.UUID, set map[string]any) error {
	return db.InTenantTx(ctx, s.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		q, args := `UPDATE customer_invoice_accounts SET updated_at = now()`, []any{tenant, id}
		for k, v := range set {
			if !accountColumns[k] {
				return fmt.Errorf("invoicing: no account column %q", k)
			}
			args = append(args, v)
			q += fmt.Sprintf(", %s = $%d", k, len(args))
		}
		_, err := tx.Exec(ctx, q+` WHERE tenant_id = $1 AND id = $2`, args...)
		return err
	})
}

// Request is a request for an account or link waiting to be issued:
// for an invoice, a storefront order's account number (Order set), or a
// customer's payment link (Link set).
type Request struct {
	ID         uuid.UUID
	Tenant     uuid.UUID
	Invoice    uuid.UUID
	Order      *uuid.UUID
	Link       *uuid.UUID
	Kind       string
	AmountKobo int64
	Channel    string // page, whatsapp or telegram
	ReplyTo    map[string]any
}

// Drain issues what the invoice pages (and chat) asked for. Run it every
// second. A request that fails is dropped: the page shows the account's
// state, and the customer can ask again.
func (s *Invoices) Drain(ctx context.Context) error {
	rows, err := s.Pool.Query(ctx, `SELECT id, tenant_id, invoice_id, order_id, customer_link_id, kind, amount_kobo, channel, reply_to FROM taskiem_invoice_requests_claim(20)`)
	if err != nil {
		return err
	}
	reqs, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (Request, error) {
		var q Request
		var invoice *uuid.UUID
		var reply []byte
		err := r.Scan(&q.ID, &q.Tenant, &invoice, &q.Order, &q.Link, &q.Kind, &q.AmountKobo, &q.Channel, &reply)
		if invoice != nil {
			q.Invoice = *invoice
		}
		if len(reply) > 0 {
			_ = json.Unmarshal(reply, &q.ReplyTo)
		}
		return q, err
	})
	if err != nil {
		return err
	}
	for _, q := range reqs {
		if q.Order != nil {
			// A storefront order's: the storefront checks it and issues.
			if s.OrderRequested != nil {
				if err := s.OrderRequested(ctx, q.Tenant, *q.Order); err != nil {
					s.log().Warn("order account", "tenant", q.Tenant, "order", *q.Order, "err", err)
				}
			}
			if _, err := s.Pool.Exec(ctx, `SELECT taskiem_invoice_requests_done($1)`, q.ID); err != nil {
				return err
			}
			continue
		}
		if q.Link != nil {
			// A customer's page: what it asked for is on the page.
			if _, err := s.IssueForCustomer(ctx, q.Tenant, *q.Link, q.Kind, q.AmountKobo); err != nil {
				s.log().Warn("customer link account", "tenant", q.Tenant, "link", *q.Link, "kind", q.Kind, "err", err)
			}
			if _, err := s.Pool.Exec(ctx, `SELECT taskiem_invoice_requests_done($1)`, q.ID); err != nil {
				return err
			}
			continue
		}
		a, err := s.Issue(ctx, q.Tenant, q.Invoice, q.Kind, q.AmountKobo)
		if err != nil {
			s.log().Warn("invoice account", "tenant", q.Tenant, "invoice", q.Invoice, "kind", q.Kind, "err", err)
		}
		if s.Answered != nil && q.Channel != "page" {
			s.Answered(ctx, q, a, err)
		}
		if _, err := s.Pool.Exec(ctx, `SELECT taskiem_invoice_requests_done($1)`, q.ID); err != nil {
			return err
		}
	}
	return nil
}

// CheckPayments asks the rails about every account and link still
// waiting, at most every 30 seconds each, and counts what was paid
// towards its invoice. Run it every 30 seconds.
func (s *Invoices) CheckPayments(ctx context.Context) error {
	if s.Rails == nil {
		return nil
	}
	rows, err := s.Pool.Query(ctx, `SELECT tenant_id FROM taskiem_invoice_watch_tenants()`)
	if err != nil {
		return err
	}
	tenants, err := pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
	if err != nil {
		return err
	}
	for _, t := range tenants {
		if err := s.checkTenant(ctx, t); err != nil && ctx.Err() == nil {
			s.log().Warn("invoice payments", "tenant", t, "err", err)
		}
	}
	return nil
}

type watched struct {
	Account
	invoice, order, link *uuid.UUID
}

func (s *Invoices) checkTenant(ctx context.Context, tenant uuid.UUID) error {
	now := s.now()
	var list []watched
	err := db.InTenantTx(ctx, s.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT `+accountCols+`, invoice_id, order_id, customer_link_id FROM customer_invoice_accounts
			WHERE tenant_id = $1 AND status IN ('requested', 'open') AND (checked_at IS NULL OR checked_at < $2)
			ORDER BY checked_at NULLS FIRST LIMIT 50`, tenant, now.Add(-30*time.Second))
		if err != nil {
			return err
		}
		list, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (watched, error) {
			var w watched
			a := &w.Account
			err := r.Scan(&a.ID, &a.Seq, &a.Kind, &a.Connector, &a.Connection, &a.PaymentID, &a.AmountKobo, &a.Status, &a.AccountNumber, &a.BankName,
				&a.AccountName, &a.PaymentURL, &a.ExpiresAt, &a.OnLate, &a.OnWrongAmount, &a.Error, &a.CreatedAt, &w.invoice, &w.order, &w.link)
			return w, err
		})
		return err
	})
	if err != nil {
		return err
	}
	for _, w := range list {
		// Stop watching an account well after it expired, and a link after
		// a week: nothing more will come.
		end := w.CreatedAt.Add(WatchLinks)
		if w.ExpiresAt != nil {
			end = w.ExpiresAt.Add(WatchAfterExpiry)
		}
		if now.After(end) {
			_ = s.setAccount(ctx, tenant, w.ID, map[string]any{"status": "failed", "error": "not paid", "checked_at": now})
			continue
		}
		if err := s.check(ctx, tenant, w); err != nil {
			s.log().Warn("invoice payment lookup", "tenant", tenant, "payment", w.PaymentID, "err", err)
		}
	}
	return nil
}

// check looks one account's payment up and counts it when paid.
func (s *Invoices) check(ctx context.Context, tenant uuid.UUID, w watched) error {
	rail, err := s.Rails.Resolve(ctx, tenant, w.Connector+"/"+w.Connection)
	if err != nil {
		return err
	}
	if rail.Caps != nil && !rail.Maps("get_payment") {
		// A rail that cannot be asked (iswallet) says so by its webhook
		// alone: payment.received counts the payment. Marked checked, so
		// the other rails' accounts are polled in their turn.
		return s.setAccount(ctx, tenant, w.ID, map[string]any{"checked_at": s.now()})
	}
	out, err := s.Rails.Call(ctx, tenant, rail, "get_payment", map[string]any{"payment_id": w.PaymentID}, effects.KeyInput{})
	_ = s.setAccount(ctx, tenant, w.ID, map[string]any{"checked_at": s.now()})
	if err != nil {
		return err
	}
	if st, _ := out["status"].(string); st != "paid" {
		return nil
	}
	amount := w.AmountKobo
	if v, ok := out["amount_kobo"].(int64); ok && v > 0 {
		amount = v
	} else if v, ok := out["amount_kobo"].(float64); ok && v > 0 {
		amount = int64(v)
	}
	r := Received{PaymentID: w.PaymentID, AmountKobo: amount, Connection: w.Connector + "/" + w.Connection, Account: w.ID}
	switch {
	case w.order != nil && s.OrderReceived != nil:
		return s.OrderReceived(ctx, tenant, *w.order, r)
	case w.link != nil && s.LinkReceived != nil:
		return s.LinkReceived(ctx, tenant, *w.link, r)
	case w.invoice != nil:
		return s.Received(ctx, tenant, *w.invoice, r)
	}
	return nil
}

// Received is a payment a rail reported for an account or link.
type Received struct {
	PaymentID  string
	AmountKobo int64
	Connection string
	Account    uuid.UUID
}

// Received counts a payment into an invoice's account or link towards
// it, exactly (the reference is the one Taskiem gave), once, and tells a
// run waiting on the invoice when it becomes paid.
func (s *Invoices) Received(ctx context.Context, tenant, invoice uuid.UUID, r Received) error {
	var x Invoice
	var became bool
	err := db.InTenantTx(ctx, s.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		var entry *uuid.UUID
		if s.MoneyLog != nil {
			// The same movement the rail's payment.received event is: one
			// entry whichever is heard first, and the match names it.
			amount := r.AmountKobo
			id, _, err := s.MoneyLog.RecordTx(ctx, tx, tenant, moneylog.Entry{Direction: "in", Kind: "payment", Status: "settled", Channel: r.Connection,
				Reference: r.PaymentID, Event: "payment.received", AmountKobo: &amount, Source: "lookup"})
			if err != nil {
				return err
			}
			entry = &id
		}
		var err error
		x, became, err = s.RecordPayment(ctx, tx, tenant, invoice.String(), PaymentIn{AmountKobo: r.AmountKobo, PaymentID: r.PaymentID, EntryID: entry,
			Connection: r.Connection, How: "exact", By: "system:" + r.Connection, Reasons: []string{"paid into the account Taskiem gave for this invoice"}})
		if err != nil {
			return err
		}
		if r.Account != uuid.Nil {
			_, err = tx.Exec(ctx, `UPDATE customer_invoice_accounts SET status = 'paid', updated_at = now() WHERE tenant_id = $1 AND id = $2`, tenant, r.Account)
		}
		return err
	})
	if err != nil || !became {
		return err
	}
	return s.Paid(ctx, tenant, x)
}
