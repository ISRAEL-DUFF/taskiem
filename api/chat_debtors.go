package api

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/invoicing"
	"github.com/israel-duff/taskiem/engine/matching"
	"github.com/israel-duff/taskiem/engine/moneylog"
	"github.com/israel-duff/taskiem/engine/sender"
	"github.com/israel-duff/taskiem/engine/whatsapp"
)

// The debtor book in chat (docs/spec/no-sheet-needed.md §6.4; decision
// 0060, R5). Money customers owe is kept as invoices (engine/invoicing
// book.go): "Ada owes 5,000" records a notebook debt, an invoice of kind
// credit that is open and owed but never sent; "Ada paid 2,000 cash"
// counts a payment towards Ada's open lines oldest first along the one
// path a customer's money takes (matching.Matcher.CountCustomer), the rest
// kept as her credit; "who owes me?" lists who owes what; "balance Ada"
// is her statement; "credit terms 14 days" sets how long customers have
// to pay. Each change is read back and written after yes, under a key from
// the conversation, so a yes delivered twice writes once.
//
// The credit terms are the owner's: the first debt recorded by a person
// who may change the invoice settings asks them, with no default; anyone
// else's debt is recorded with no due date until they are set.
//
// A transfer is counted by where the money went. Into a connected account
// (a rail whose credits Taskiem reads), nothing is posted: the rail's
// report is the record, and matching counts it when it arrives. Into any
// other account, it is entered now under that account's statement channel
// name, chosen from the business's statement channels or named once.

// chatDebt is a debt or a payment being recorded.
type chatDebt struct {
	// Op: owes, paid, terms or send.
	Op string `json:"op"`
	// Phase: pick (which customer), phone (a new customer's), terms (the
	// owner's credit terms), apply (the terms to open lines), how (cash or
	// transfer), account (which), account_name (a new one), confirm.
	Phase string `json:"phase"`
	// Name is the customer as written; Key the one chosen ("" for a new
	// one); Pick the keys offered, numbered from 1.
	Name string   `json:"name"`
	Key  string   `json:"key,omitempty"`
	Pick []string `json:"pick,omitempty"`
	// Phone is a new customer's phone, sealed.
	Phone       map[string]any `json:"phone,omitempty"`
	AmountKobo  int64          `json:"amount_kobo,omitempty"`
	Description string         `json:"description,omitempty"`
	// How: cash or transfer; Channel the account a transfer went into, and
	// Rail when that is a connected one.
	How      string   `json:"how,omitempty"`
	Channel  string   `json:"channel,omitempty"`
	Rail     bool     `json:"rail,omitempty"`
	Accounts []string `json:"accounts,omitempty"`
	// Days are the credit terms chosen (0: no fixed time) for terms.
	Days  int    `json:"days,omitempty"`
	Nonce string `json:"nonce,omitempty"`
}

var (
	owesRe     = regexp.MustCompile(`(?i)^(.{1,60}?)\s+(?:owes|owe|dey owe|go pay)(?:\s+me)?\s+(\S+)(?:\s+(?:for\s+)?(.{1,100}))?$`)
	custPaidRe = regexp.MustCompile(`(?i)^(.{1,60}?)\s+(?:paid|has paid|don pay)\s+(\S+)(?:\s+(cash|transfer|bank))?$`)
	whoOwesRe  = regexp.MustCompile(`(?i)^(?:who\s+(?:owes|owe|dey owe|don owe)\s+me|(?:my\s+)?debtors|people\s+who\s+owe\s+me|customers\s+who\s+owe(?:\s+me)?)$`)
	balanceRe  = regexp.MustCompile(`(?i)^(?:balance|statement)\s+(?:for\s+|of\s+)?(.{1,60})$`)
	termsRe    = regexp.MustCompile(`(?i)^credit\s+terms?\s+(?:(\d{1,3})\s*days?|(none|no fixed time|off))$`)
	sendBalRe  = regexp.MustCompile(`(?i)^send\s+(?:(.{1,60}?)\s+(?:her|his|their)\s+balance|(.{1,60}?)'s\s+balance|(?:the\s+)?balance\s+to\s+(.{1,60}))$`)
	custNumRe  = regexp.MustCompile(`(?i)^(.{1,60}?)(?:'s)?\s+(?:number|phone(?:\s+number)?)\s+(?:is\s+)?(\+?[\d][\d\s-]{6,20})$`)
	// notACustomer are words a sentence about the business itself starts
	// with: "I paid 5,000" is not a customer called I.
	notACustomer = map[string]bool{"i": true, "we": true, "me": true, "you": true, "he": true, "she": true, "they": true, "who": true}
)

// creditChoices are the credit terms offered, in days (0: no fixed time).
var creditChoices = []int{7, 14, 30, 0}

// waDebtCommand reads the debtor book's words in English; false for text
// that is none of them.
func (s *Server) waDebtCommand(ctx context.Context, c *chat, text, cmd string) (bool, error) {
	if s.Invoices == nil {
		return false, nil
	}
	t := strings.TrimSpace(strings.TrimRight(strings.TrimSpace(text), "?!."))
	switch {
	case whoOwesRe.MatchString(t):
		return true, s.waWhoOwes(ctx, c)
	case termsRe.MatchString(t):
		m := termsRe.FindStringSubmatch(t)
		days := 0
		if m[1] != "" {
			days, _ = strconv.Atoi(m[1])
		}
		return true, s.waCreditTerms(ctx, c, days)
	case balanceRe.MatchString(t):
		return true, s.waStatement(ctx, c, balanceRe.FindStringSubmatch(t)[1])
	case sendBalRe.MatchString(t):
		m := sendBalRe.FindStringSubmatch(t)
		return true, s.waSendBalance(ctx, c, m[1]+m[2]+m[3])
	}
	if m := custNumRe.FindStringSubmatch(t); m != nil && !notACustomer[strings.ToLower(m[1])] {
		return true, s.waCustomerPhone(ctx, c, m[1], m[2])
	}
	if m := owesRe.FindStringSubmatch(t); m != nil && !notACustomer[strings.ToLower(m[1])] {
		kobo, ok := parseKobo(m[2])
		if !ok {
			return false, nil
		}
		return true, s.waOwes(ctx, c, m[1], kobo, m[3])
	}
	if m := custPaidRe.FindStringSubmatch(t); m != nil && !notACustomer[strings.ToLower(m[1])] {
		kobo, ok := parseKobo(m[2])
		if !ok {
			return false, nil
		}
		how := strings.ToLower(m[3])
		if how == "bank" {
			how = "transfer"
		}
		return true, s.waCustomerPaid(ctx, c, m[1], kobo, how)
	}
	return false, nil
}

// waBookIn runs fn in the tenant's transaction.
func (s *Server) waBookIn(ctx context.Context, c *chat, fn func(pgx.Tx) error) error {
	return db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{c.tenant.ID}, fn)
}

// owedBy is what a customer owes on their open lines and invoices, and
// how many there are.
func owedBy(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, customer string) (int64, int, error) {
	var due int64
	var n int
	err := tx.QueryRow(ctx, `SELECT COALESCE(sum(total_kobo - paid_kobo), 0), count(*) FROM customer_invoices
		WHERE tenant_id = $1 AND customer_id = $2 AND status IN ('recorded', 'sent', 'part_paid', 'overdue')`, tenant, customer).Scan(&due, &n)
	return due, n, err
}

// waFindDebtor looks a name up in the book: the customers it names, opened.
func (s *Server) waFindDebtor(ctx context.Context, c *chat, name string) ([]invoicing.BookCustomer, error) {
	var found []invoicing.BookCustomer
	err := s.waBookIn(ctx, c, func(tx pgx.Tx) error {
		var err error
		found, err = s.Invoices.FindBookCustomers(ctx, tx, c.tenant.ID, name)
		return err
	})
	return found, err
}

// pickList numbers the customers offered, with what each owes, then
// "someone new" when a new one may be made.
func (s *Server) pickList(ctx context.Context, c *chat, found []invoicing.BookCustomer, withNew bool) (string, error) {
	var lines []string
	err := s.waBookIn(ctx, c, func(tx pgx.Tx) error {
		for i, f := range found {
			due, _, err := owedBy(ctx, tx, c.tenant.ID, "key:"+f.Key)
			if err != nil {
				return err
			}
			line := fmt.Sprintf("%d. %s", i+1, whatsapp.SafeText(f.Name))
			if f.Phone != "" {
				line += " (" + whatsapp.MaskValue("phone", f.Phone) + ")"
			}
			lines = append(lines, line+": "+c.t("wa.debt.owes_short", "amount", invoicing.Naira(due)))
		}
		return nil
	})
	if withNew {
		lines = append(lines, fmt.Sprintf("%d. %s", len(found)+1, c.t("wa.debt.someone_new")))
	}
	return strings.Join(lines, "\n"), err
}

func (s *Server) saveDebt(ctx context.Context, c *chat, d *chatDebt) error {
	return s.saveSession(ctx, c, stateCollecting, chatData{Debt: d}, waPendingTTL)
}

// --- owes ---

// waOwes answers "Ada owes 5,000".
func (s *Server) waOwes(ctx context.Context, c *chat, name string, kobo int64, desc string) error {
	if !c.p.Can(PermInvoiceManage) {
		return c.say(ctx, s, c.t("wa.debt.denied", "org", c.tenant.Name))
	}
	d := &chatDebt{Op: "owes", Name: strings.Join(strings.Fields(name), " "), AmountKobo: kobo, Description: strings.TrimSpace(desc)}
	found, err := s.waFindDebtor(ctx, c, d.Name)
	if err != nil {
		return err
	}
	switch len(found) {
	case 0:
		d.Phase = "phone"
		if err := s.saveDebt(ctx, c, d); err != nil {
			return err
		}
		return c.say(ctx, s, c.t("wa.debt.ask_phone", "name", whatsapp.SafeText(d.Name)))
	case 1:
		d.Key, d.Name = found[0].Key, found[0].Name
		return s.waOwesTerms(ctx, c, d)
	}
	return s.waDebtPick(ctx, c, d, found, true)
}

// waDebtPick offers the customers a name matches, by number.
func (s *Server) waDebtPick(ctx context.Context, c *chat, d *chatDebt, found []invoicing.BookCustomer, withNew bool) error {
	d.Phase, d.Pick = "pick", nil
	for _, f := range found {
		d.Pick = append(d.Pick, f.Key)
	}
	if err := s.saveDebt(ctx, c, d); err != nil {
		return err
	}
	list, err := s.pickList(ctx, c, found, withNew)
	if err != nil {
		return err
	}
	return c.say(ctx, s, c.t("wa.debt.which", "name", whatsapp.SafeText(d.Name), "list", list))
}

// waOwesTerms asks the owner's credit terms once, before the first debt,
// of a person who may set them; else goes on to the read-back.
func (s *Server) waOwesTerms(ctx context.Context, c *chat, d *chatDebt) error {
	var st invoicing.Settings
	if err := s.waBookIn(ctx, c, func(tx pgx.Tx) error {
		var err error
		st, err = invoicing.GetSettings(ctx, tx, c.tenant.ID)
		return err
	}); err != nil {
		return err
	}
	if st.CreditTermsAt == nil && c.p.Can(PermInvoiceManage) {
		d.Phase = "terms"
		if err := s.saveDebt(ctx, c, d); err != nil {
			return err
		}
		return c.say(ctx, s, c.t("wa.debt.ask_terms", "list", termsList(c)))
	}
	return s.waOwesConfirm(ctx, c, d, st)
}

// termsList numbers the credit terms offered.
func termsList(c *chat) string {
	lines := make([]string, len(creditChoices))
	for i, days := range creditChoices {
		lines[i] = strconv.Itoa(i+1) + ". " + termsWords(c, days)
	}
	return strings.Join(lines, "\n")
}

func termsWords(c *chat, days int) string {
	if days == 0 {
		return c.t("wa.debt.no_fixed_time")
	}
	return c.t("wa.debt.days", "days", strconv.Itoa(days))
}

// waOwesConfirm reads the debt back: the customer, the amount, when it is
// due, and what they will owe in all.
func (s *Server) waOwesConfirm(ctx context.Context, c *chat, d *chatDebt, st invoicing.Settings) error {
	d.Phase, d.Nonce = "confirm", uuid.NewString()
	var before, credit int64
	if d.Key != "" {
		if err := s.waBookIn(ctx, c, func(tx pgx.Tx) error {
			var err error
			if before, _, err = owedBy(ctx, tx, c.tenant.ID, "key:"+d.Key); err != nil {
				return err
			}
			// Money they paid before and no line took counts towards it.
			credit, err = invoicing.CreditOf(ctx, tx, c.tenant.ID, "key:"+d.Key)
			return err
		}); err != nil {
			return err
		}
	}
	if err := s.saveDebt(ctx, c, d); err != nil {
		return err
	}
	due := c.t("wa.debt.no_due")
	if st.CreditTermsAt == nil {
		due = c.t("wa.debt.no_terms_yet")
	} else if on := invoicing.CreditDue(st, time.Now()); on != "" {
		due = c.t("wa.debt.due_on", "date", humanDate(on))
	}
	return c.say(ctx, s, c.t("wa.debt.confirm_owes", "name", whatsapp.SafeText(d.Name), "amount", invoicing.Naira(d.AmountKobo), "due", due,
		"total", invoicing.Naira(before+d.AmountKobo-min(credit, d.AmountKobo))), tableYesButtons(c)...)
}

// humanDate is a date as a message shows it: 13 Nov 2026.
func humanDate(on string) string {
	t, err := time.Parse(time.DateOnly, on)
	if err != nil {
		return on
	}
	return t.Format("2 Jan 2006")
}

// waOwesNow records the debt agreed: a new customer first when there is
// one, then the line, under the read-back's key.
func (s *Server) waOwesNow(ctx context.Context, c *chat, d *chatDebt) error {
	var x invoicing.Invoice
	var total int64
	key := "chat:" + d.Nonce
	err := s.waBookIn(ctx, c, func(tx pgx.Tx) error {
		var found uuid.UUID
		err := tx.QueryRow(ctx, `SELECT id FROM customer_invoices WHERE tenant_id = $1 AND created_key = $2`, c.tenant.ID, key).Scan(&found)
		if err == nil {
			// Recorded already (a yes delivered twice).
			if x, err = s.Invoices.Get(ctx, tx, c.tenant.ID, found.String()); err != nil {
				return err
			}
			total, _, err = owedBy(ctx, tx, c.tenant.ID, x.CustomerID())
			return err
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		var cust invoicing.BookCustomer
		if d.Key == "" {
			phone := ""
			if d.Phone != nil {
				v, err := s.Store.PII.OpenTx(ctx, tx, c.tenant.ID, d.Phone)
				if err != nil {
					return err
				}
				phone, _ = v.(string)
			}
			if cust, err = s.Invoices.NewBookCustomer(ctx, tx, c.tenant.ID, d.Name, phone, c.p.Actor()); err != nil {
				return err
			}
		} else if cust, err = s.Invoices.BookCustomerByKey(ctx, tx, c.tenant.ID, d.Key); err != nil {
			return err
		}
		if x, err = s.Invoices.RecordCredit(ctx, tx, c.tenant.ID, key, cust, invoicing.CreditDraft{AmountKobo: d.AmountKobo, Description: d.Description}, c.p.Actor()); err != nil {
			return err
		}
		total, _, err = owedBy(ctx, tx, c.tenant.ID, x.CustomerID())
		return err
	})
	if errors.Is(err, invoicing.ErrInvalid) {
		_ = s.saveSession(ctx, c, stateIdle, chatData{}, 0)
		return c.say(ctx, s, c.t("wa.debt.refused", "reason", whatsapp.SafeText(strings.TrimPrefix(err.Error(), "invoicing: invalid: "))))
	}
	if err != nil {
		return err
	}
	if err := s.saveSession(ctx, c, stateIdle, chatData{}, 0); err != nil {
		return err
	}
	if x.Status == "paid" {
		// Paid from their credit as it was recorded.
		if err := s.Invoices.Paid(ctx, c.tenant.ID, x); err != nil {
			return err
		}
	}
	return c.say(ctx, s, c.t("wa.debt.recorded", "name", whatsapp.SafeText(d.Name), "amount", invoicing.Naira(d.AmountKobo), "number", x.Number,
		"total", invoicing.Naira(total)))
}

// --- paid ---

// waCustomerPaid answers "Ada paid 2,000 [cash|transfer]".
func (s *Server) waCustomerPaid(ctx context.Context, c *chat, name string, kobo int64, how string) error {
	if !c.p.Can(PermInvoiceManage) {
		return c.say(ctx, s, c.t("wa.debt.denied", "org", c.tenant.Name))
	}
	d := &chatDebt{Op: "paid", Name: strings.Join(strings.Fields(name), " "), AmountKobo: kobo, How: how}
	found, err := s.waFindDebtor(ctx, c, d.Name)
	if err != nil {
		return err
	}
	switch len(found) {
	case 0:
		return c.say(ctx, s, c.t("wa.debt.no_one", "name", whatsapp.SafeText(d.Name)))
	case 1:
		d.Key, d.Name = found[0].Key, found[0].Name
		return s.waPaidHow(ctx, c, d)
	}
	return s.waDebtPick(ctx, c, d, found, false)
}

// waPaidHow asks how they paid when the words did not say.
func (s *Server) waPaidHow(ctx context.Context, c *chat, d *chatDebt) error {
	switch d.How {
	case "cash":
		return s.waPaidConfirm(ctx, c, d)
	case "transfer":
		return s.waPaidAccount(ctx, c, d)
	}
	d.Phase = "how"
	if err := s.saveDebt(ctx, c, d); err != nil {
		return err
	}
	return c.say(ctx, s, c.t("wa.debt.ask_how", "name", whatsapp.SafeText(d.Name)))
}

// accountChoices are the accounts a transfer may have gone into: the
// connected ones (rail:<connector>/<connection>), then the statement
// channels the business has, then a new one.
func (s *Server) accountChoices(ctx context.Context, c *chat) ([]string, []string, error) {
	var keys, names []string
	if rails := s.accountRails(); rails != nil {
		list, err := rails.Capable(ctx, c.tenant.ID, "list_credits")
		if err != nil {
			s.Logger.Warn("chat: listing connected accounts", "err", err)
		}
		for _, r := range list {
			keys = append(keys, "rail:"+r.Connector+"/"+r.Connection)
			names = append(names, c.t("wa.debt.account_connected", "name", whatsapp.SafeText(r.Name), "connection", whatsapp.SafeText(r.Connection)))
		}
	}
	err := s.waBookIn(ctx, c, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT channel FROM money_statement_formats WHERE tenant_id = $1 ORDER BY channel LIMIT 8`, c.tenant.ID)
		if err != nil {
			return err
		}
		chans, err := pgx.CollectRows(rows, pgx.RowTo[string])
		for _, ch := range chans {
			keys = append(keys, ch)
			names = append(names, whatsapp.SafeText(ch))
		}
		return err
	})
	return keys, names, err
}

// waPaidAccount asks which account a transfer went into.
func (s *Server) waPaidAccount(ctx context.Context, c *chat, d *chatDebt) error {
	keys, names, err := s.accountChoices(ctx, c)
	if err != nil {
		return err
	}
	d.Phase, d.Accounts = "account", keys
	if err := s.saveDebt(ctx, c, d); err != nil {
		return err
	}
	lines := make([]string, 0, len(names)+1)
	for i, n := range names {
		lines = append(lines, strconv.Itoa(i+1)+". "+n)
	}
	lines = append(lines, strconv.Itoa(len(names)+1)+". "+c.t("wa.debt.account_new"))
	return c.say(ctx, s, c.t("wa.debt.ask_account", "name", whatsapp.SafeText(d.Name), "list", strings.Join(lines, "\n")))
}

// waPaidConfirm reads the payment back: what they owe after it, and what
// will be kept as their credit.
func (s *Server) waPaidConfirm(ctx context.Context, c *chat, d *chatDebt) error {
	var owed int64
	if err := s.waBookIn(ctx, c, func(tx pgx.Tx) error {
		var err error
		owed, _, err = owedBy(ctx, tx, c.tenant.ID, "key:"+d.Key)
		return err
	}); err != nil {
		return err
	}
	d.Phase, d.Nonce = "confirm", uuid.NewString()
	if err := s.saveDebt(ctx, c, d); err != nil {
		return err
	}
	how := c.t("wa.debt.how_cash")
	if d.How == "transfer" {
		how = c.t("wa.debt.how_transfer", "account", whatsapp.SafeText(d.Channel))
	}
	msg := c.t("wa.debt.confirm_paid", "name", whatsapp.SafeText(d.Name), "amount", invoicing.Naira(d.AmountKobo), "how", how,
		"total", invoicing.Naira(max(owed-d.AmountKobo, 0)))
	if extra := d.AmountKobo - owed; extra > 0 {
		msg += "\n" + c.t("wa.debt.extra_credit", "amount", invoicing.Naira(extra))
	}
	return c.say(ctx, s, msg, tableYesButtons(c)...)
}

// waPaidNow posts the payment agreed as a money log entry (cash, or a
// transfer entered under its account's channel), makes it the customer's
// money and counts it towards their open lines oldest first, the rest
// their credit.
func (s *Server) waPaidNow(ctx context.Context, c *chat, d *chatDebt) error {
	kind, channel := "cash", "cash"
	if d.How == "transfer" {
		kind, channel = "statement", d.Channel
	}
	customer := "key:" + d.Key
	var parts []matching.CountedPart
	var owed, credit int64
	m := s.matcher()
	err := s.waBookIn(ctx, c, func(tx pgx.Tx) error {
		now := time.Now()
		amount := d.AmountKobo
		entry, fresh, err := s.moneyLog().RecordTx(ctx, tx, c.tenant.ID, moneylog.Entry{Direction: "in", Kind: kind, Status: "settled", Channel: channel,
			Reference: "chat:" + d.Nonce, Source: "entered", AmountKobo: &amount, OccurredAt: &now, Description: "Paid by " + d.Name, EnteredBy: c.p.Actor()})
		if err != nil {
			return err
		}
		if fresh {
			if err := moneylog.LockEntry(ctx, tx, entry); err != nil {
				return err
			}
			if err := invoicing.MarkCustomers(ctx, tx, c.tenant.ID, entry, customer, nil, "person", c.p.Actor()); err != nil {
				return err
			}
			if parts, err = m.CountCustomer(ctx, tx, c.tenant.ID, entry, customer, matching.CustomerCounting{Connection: channel, How: "manual", By: c.p.Actor(),
				Reasons: []string{"recorded in chat"}}); err != nil {
				return err
			}
		}
		if owed, _, err = owedBy(ctx, tx, c.tenant.ID, customer); err != nil {
			return err
		}
		credit, err = invoicing.CreditOf(ctx, tx, c.tenant.ID, customer)
		return err
	})
	if errors.Is(err, moneylog.ErrInvalid) {
		_ = s.saveSession(ctx, c, stateIdle, chatData{}, 0)
		return c.say(ctx, s, c.t("wa.debt.refused", "reason", whatsapp.SafeText(strings.TrimPrefix(err.Error(), "moneylog: invalid entry: "))))
	}
	if err != nil {
		return err
	}
	if err := m.TellPaid(ctx, c.tenant.ID, parts); err != nil {
		s.Logger.Warn("chat: telling a payment", "err", err)
	}
	if err := s.saveSession(ctx, c, stateIdle, chatData{}, 0); err != nil {
		return err
	}
	msg := c.t("wa.debt.paid_recorded", "name", whatsapp.SafeText(d.Name), "amount", invoicing.Naira(d.AmountKobo), "total", invoicing.Naira(owed))
	if credit > 0 {
		msg += "\n" + c.t("wa.debt.credit_held", "amount", invoicing.Naira(credit))
	}
	return c.say(ctx, s, msg)
}

// --- the replies ---

// waDebtReply takes a message while a debt or payment is recorded.
func (s *Server) waDebtReply(ctx context.Context, c *chat, text, cmd string) error {
	d := c.data.Debt
	if !c.p.Can(PermInvoiceManage) {
		_ = s.saveSession(ctx, c, stateIdle, chatData{}, 0)
		return c.say(ctx, s, c.t("wa.debt.denied", "org", c.tenant.Name))
	}
	num := func() (int, bool) {
		n, err := strconv.Atoi(strings.TrimSpace(text))
		return n, err == nil
	}
	switch d.Phase {
	case "pick":
		n, ok := num()
		switch {
		case ok && n >= 1 && n <= len(d.Pick):
			d.Key = d.Pick[n-1]
			if err := s.waBookIn(ctx, c, func(tx pgx.Tx) error {
				cust, err := s.Invoices.BookCustomerByKey(ctx, tx, c.tenant.ID, d.Key)
				d.Name = cust.Name
				return err
			}); err != nil {
				return err
			}
			switch d.Op {
			case "paid":
				return s.waPaidHow(ctx, c, d)
			case "send":
				return s.waSendConfirm(ctx, c, d)
			}
			return s.waOwesTerms(ctx, c, d)
		case ok && n == len(d.Pick)+1 && d.Op == "owes":
			d.Key, d.Phase = "", "phone"
			if err := s.saveDebt(ctx, c, d); err != nil {
				return err
			}
			return c.say(ctx, s, c.t("wa.debt.ask_phone", "name", whatsapp.SafeText(d.Name)))
		}
		return c.say(ctx, s, c.t("wa.rows.pick_again", "max", strconv.Itoa(len(d.Pick)+boolInt(d.Op == "owes"))))
	case "phone":
		if !isSkip(text) {
			p, err := sender.NormalisePhone(strings.TrimSpace(text), s.smsCountries())
			if err != nil {
				return c.say(ctx, s, c.t("wa.debt.phone_bad"))
			}
			if err := s.waBookIn(ctx, c, func(tx pgx.Tx) error {
				env, err := s.Store.PII.SealTx(ctx, tx, c.tenant.ID, "phone", p)
				d.Phone = env
				return err
			}); err != nil {
				return err
			}
		}
		return s.waOwesTerms(ctx, c, d)
	case "terms":
		n, ok := num()
		if !ok || n < 1 || n > len(creditChoices) {
			return c.say(ctx, s, c.t("wa.debt.ask_terms", "list", termsList(c)))
		}
		return s.waSetTerms(ctx, c, d, creditChoices[n-1])
	case "apply":
		switch cmd {
		case "yes", "y", "ok", "okay":
			var n int
			if err := s.waBookIn(ctx, c, func(tx pgx.Tx) error {
				var err error
				n, err = invoicing.ApplyCreditTerms(ctx, tx, c.tenant.ID, c.p.Actor())
				return err
			}); err != nil {
				return err
			}
			if err := c.say(ctx, s, c.t("wa.debt.applied", "count", strconv.Itoa(n))); err != nil {
				return err
			}
		case "no", "n":
		default:
			return c.say(ctx, s, c.t("wa.rows.reminder"), tableYesButtons(c)...)
		}
		if d.Op == "owes" {
			return s.waOwesTerms(ctx, c, d)
		}
		return s.saveSession(ctx, c, stateIdle, chatData{}, 0)
	case "how":
		switch n, ok := num(); {
		case ok && n == 1, cmd == "cash":
			d.How = "cash"
		case ok && n == 2, cmd == "transfer", cmd == "bank":
			d.How = "transfer"
		default:
			return c.say(ctx, s, c.t("wa.debt.ask_how", "name", whatsapp.SafeText(d.Name)))
		}
		return s.waPaidHow(ctx, c, d)
	case "account":
		n, ok := num()
		switch {
		case ok && n >= 1 && n <= len(d.Accounts):
			acct := d.Accounts[n-1]
			if rail, isRail := strings.CutPrefix(acct, "rail:"); isRail {
				// The rail's report is the record: nothing is posted.
				_ = s.saveSession(ctx, c, stateIdle, chatData{}, 0)
				return c.say(ctx, s, c.t("wa.debt.transfer_rail", "name", whatsapp.SafeText(d.Name), "account", whatsapp.SafeText(rail)))
			}
			d.Channel = acct
			return s.waPaidConfirm(ctx, c, d)
		case ok && n == len(d.Accounts)+1:
			d.Phase = "account_name"
			if err := s.saveDebt(ctx, c, d); err != nil {
				return err
			}
			return c.say(ctx, s, c.t("wa.debt.ask_account_name"))
		}
		return c.say(ctx, s, c.t("wa.rows.pick_again", "max", strconv.Itoa(len(d.Accounts)+1)))
	case "account_name":
		ch := strings.ToLower(strings.TrimSpace(text))
		if !moneylog.ValidChannel(ch) {
			return c.say(ctx, s, c.t("wa.debt.account_name_bad"))
		}
		d.Channel = ch
		return s.waPaidConfirm(ctx, c, d)
	case "confirm":
		switch cmd {
		case "yes", "y", "ok", "okay", "record", "send":
			switch d.Op {
			case "paid":
				return s.waPaidNow(ctx, c, d)
			case "send":
				return s.waSendNow(ctx, c, d)
			}
			return s.waOwesNow(ctx, c, d)
		case "no", "n":
			_ = s.saveSession(ctx, c, stateIdle, chatData{}, 0)
			return c.say(ctx, s, c.t("wa.debt.not_recorded"))
		}
		return c.say(ctx, s, c.t("wa.rows.reminder"), tableYesButtons(c)...)
	}
	return s.saveSession(ctx, c, stateIdle, chatData{}, 0)
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// --- credit terms ---

// waCreditTerms answers "credit terms 14 days" (0: no fixed time).
func (s *Server) waCreditTerms(ctx context.Context, c *chat, days int) error {
	if !c.p.Can(PermInvoiceManage) {
		return c.say(ctx, s, c.t("wa.debt.denied", "org", c.tenant.Name))
	}
	if days < 0 || days > 365 {
		return c.say(ctx, s, c.t("wa.debt.terms_bad"))
	}
	return s.waSetTerms(ctx, c, &chatDebt{Op: "terms"}, days)
}

// waSetTerms keeps the terms and offers them to the open lines already
// recorded, if there are any.
func (s *Server) waSetTerms(ctx context.Context, c *chat, d *chatDebt, days int) error {
	var open int
	if err := s.waBookIn(ctx, c, func(tx pgx.Tx) error {
		var p *int
		if days > 0 {
			p = &days
		}
		if err := invoicing.SetCreditTerms(ctx, tx, c.tenant.ID, p, c.p.Actor()); err != nil {
			return err
		}
		var err error
		open, err = invoicing.OpenCreditLines(ctx, tx, c.tenant.ID)
		return err
	}); err != nil {
		return err
	}
	msg := c.t("wa.debt.terms_set", "terms", termsWords(c, days))
	if days == 0 {
		msg += " " + c.t("wa.debt.terms_none_note")
	}
	if open > 0 {
		d.Phase = "apply"
		if err := s.saveDebt(ctx, c, d); err != nil {
			return err
		}
		return c.say(ctx, s, msg+"\n\n"+c.t("wa.debt.ask_apply", "count", strconv.Itoa(open)), tableYesButtons(c)...)
	}
	if err := c.say(ctx, s, msg); err != nil {
		return err
	}
	if d.Op == "owes" {
		return s.waOwesTerms(ctx, c, d)
	}
	return s.saveSession(ctx, c, stateIdle, chatData{}, 0)
}

// --- reading ---

// waWhoOwes answers "who owes me?": customers with open lines and
// invoices, largest balance first, ten at most, with the total.
func (s *Server) waWhoOwes(ctx context.Context, c *chat) error {
	if !c.p.Can(PermInvoiceRead) {
		return c.say(ctx, s, c.t("wa.debt.denied_read", "org", c.tenant.Name))
	}
	type owing struct {
		customer string
		due      int64
		lines    int
		oldest   string
		invoice  uuid.UUID
	}
	var list []owing
	var total int64
	var count int
	var names []string
	err := s.waBookIn(ctx, c, func(tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `SELECT COALESCE(sum(total_kobo - paid_kobo), 0), count(DISTINCT customer_id) FROM customer_invoices
			WHERE tenant_id = $1 AND status IN ('recorded', 'sent', 'part_paid', 'overdue')`, c.tenant.ID).Scan(&total, &count); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `SELECT customer_id, sum(total_kobo - paid_kobo), count(*), COALESCE(to_char(min(due_on), 'YYYY-MM-DD'), ''),
			(array_agg(id ORDER BY created_at DESC))[1]
			FROM customer_invoices WHERE tenant_id = $1 AND status IN ('recorded', 'sent', 'part_paid', 'overdue')
			GROUP BY customer_id ORDER BY 2 DESC, 1 LIMIT 10`, c.tenant.ID)
		if err != nil {
			return err
		}
		list, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (owing, error) {
			var o owing
			return o, r.Scan(&o.customer, &o.due, &o.lines, &o.oldest, &o.invoice)
		})
		if err != nil {
			return err
		}
		for _, o := range list {
			x, err := s.Invoices.Get(ctx, tx, c.tenant.ID, o.invoice.String())
			if err != nil {
				return err
			}
			names = append(names, x.Customer.Name)
		}
		return nil
	})
	if err != nil {
		return err
	}
	if len(list) == 0 {
		return c.say(ctx, s, c.t("wa.debt.nobody"))
	}
	lines := []string{c.t("wa.debt.who_head", "total", invoicing.Naira(total), "count", strconv.Itoa(count))}
	for i, o := range list {
		line := c.t("wa.debt.who_line", "n", strconv.Itoa(i+1), "name", whatsapp.SafeText(names[i]), "amount", invoicing.Naira(o.due), "lines", strconv.Itoa(o.lines))
		if o.oldest != "" {
			line += " " + c.t("wa.debt.who_due", "date", humanDate(o.oldest))
		}
		lines = append(lines, line)
	}
	if count > len(list) {
		lines = append(lines, c.t("wa.debt.who_more", "count", strconv.Itoa(count-len(list))))
	}
	lines = append(lines, "", c.t("wa.debt.who_tail"))
	return c.say(ctx, s, strings.Join(lines, "\n"))
}

// waStatement answers "balance Ada": what she owes, line by line, her last
// payment and her credit.
func (s *Server) waStatement(ctx context.Context, c *chat, name string) error {
	if !c.p.Can(PermInvoiceRead) {
		return c.say(ctx, s, c.t("wa.debt.denied_read", "org", c.tenant.Name))
	}
	found, err := s.waFindDebtor(ctx, c, name)
	if err != nil {
		return err
	}
	switch len(found) {
	case 0:
		return c.say(ctx, s, c.t("wa.debt.no_one", "name", whatsapp.SafeText(name)))
	case 1:
	default:
		list, err := s.pickList(ctx, c, found, false)
		if err != nil {
			return err
		}
		return c.say(ctx, s, c.t("wa.debt.which_balance", "list", list))
	}
	cust := found[0]
	customer := "key:" + cust.Key
	var open []invoicing.Invoice
	var credit int64
	var last *invoicing.Payment
	err = s.waBookIn(ctx, c, func(tx pgx.Tx) error {
		var err error
		if open, err = s.Invoices.OpenInvoicesOf(ctx, tx, c.tenant.ID, customer); err != nil {
			return err
		}
		if credit, err = invoicing.CreditOf(ctx, tx, c.tenant.ID, customer); err != nil {
			return err
		}
		var id uuid.UUID
		err = tx.QueryRow(ctx, `SELECT p.invoice_id FROM customer_invoice_payments p JOIN customer_invoices i ON i.id = p.invoice_id
			WHERE i.tenant_id = $1 AND i.customer_id = $2 AND p.undoes IS NULL ORDER BY p.created_at DESC LIMIT 1`, c.tenant.ID, customer).Scan(&id)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		x, err := s.Invoices.Get(ctx, tx, c.tenant.ID, id.String())
		if err != nil {
			return err
		}
		for i := range x.Payments {
			p := x.Payments[i]
			if p.Undoes == nil && p.UndoneBy == nil && (last == nil || p.CreatedAt.After(last.CreatedAt)) {
				last = &p
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	var owed int64
	lines := []string{}
	for _, x := range open {
		owed += x.DueKobo
		line := "• " + x.Number + ": " + invoicing.Naira(x.DueKobo)
		if x.DueOn != "" {
			line += " " + c.t("wa.debt.who_due", "date", humanDate(x.DueOn))
		}
		lines = append(lines, line)
	}
	msg := c.t("wa.debt.statement_head", "name", whatsapp.SafeText(cust.Name), "amount", invoicing.Naira(owed))
	if len(lines) > 0 {
		msg += "\n" + strings.Join(lines, "\n")
	}
	if last != nil {
		msg += "\n" + c.t("wa.debt.statement_last", "amount", invoicing.Naira(last.AmountKobo), "date", last.CreatedAt.Format("2 Jan 2006"))
	}
	if credit > 0 {
		msg += "\n" + c.t("wa.debt.credit_held", "amount", invoicing.Naira(credit))
	}
	return c.say(ctx, s, msg)
}

// waCustomerPhone answers "Ada's number is 0803…": the phone on her record
// and her lines not yet sent.
func (s *Server) waCustomerPhone(ctx context.Context, c *chat, name, phone string) error {
	if !c.p.Can(PermInvoiceManage) {
		return c.say(ctx, s, c.t("wa.debt.denied", "org", c.tenant.Name))
	}
	p, err := sender.NormalisePhone(strings.TrimSpace(phone), s.smsCountries())
	if err != nil {
		return c.say(ctx, s, c.t("wa.debt.phone_bad"))
	}
	found, err := s.waFindDebtor(ctx, c, name)
	if err != nil {
		return err
	}
	if len(found) != 1 {
		if len(found) == 0 {
			return c.say(ctx, s, c.t("wa.debt.no_one", "name", whatsapp.SafeText(name)))
		}
		list, err := s.pickList(ctx, c, found, false)
		if err != nil {
			return err
		}
		return c.say(ctx, s, c.t("wa.debt.which_balance", "list", list))
	}
	if err := s.waBookIn(ctx, c, func(tx pgx.Tx) error {
		_, err := s.Invoices.SetBookPhone(ctx, tx, c.tenant.ID, found[0], p, c.p.Actor())
		return err
	}); err != nil {
		return err
	}
	return c.say(ctx, s, c.t("wa.debt.phone_set", "name", whatsapp.SafeText(found[0].Name), "phone", whatsapp.MaskValue("phone", p)))
}

// --- send a customer their balance ---

// waSendBalance answers "send Ada her balance": one message to Ada with
// what she owes and one way to pay it (invoicing.SendBalance), read back
// first.
func (s *Server) waSendBalance(ctx context.Context, c *chat, name string) error {
	if !c.p.Can(PermInvoiceManage) {
		return c.say(ctx, s, c.t("wa.debt.denied", "org", c.tenant.Name))
	}
	d := &chatDebt{Op: "send", Name: strings.Join(strings.Fields(name), " ")}
	found, err := s.waFindDebtor(ctx, c, d.Name)
	if err != nil {
		return err
	}
	switch len(found) {
	case 0:
		return c.say(ctx, s, c.t("wa.debt.no_one", "name", whatsapp.SafeText(d.Name)))
	case 1:
		d.Key, d.Name = found[0].Key, found[0].Name
		return s.waSendConfirm(ctx, c, d)
	}
	return s.waDebtPick(ctx, c, d, found, false)
}

// waSendConfirm reads the message back: what the customer owes, where it
// goes and how they can pay.
func (s *Server) waSendConfirm(ctx context.Context, c *chat, d *chatDebt) error {
	customer := "key:" + d.Key
	var open []invoicing.Invoice
	var own bool
	if err := s.waBookIn(ctx, c, func(tx pgx.Tx) error {
		var err error
		if open, err = s.Invoices.OpenInvoicesOf(ctx, tx, c.tenant.ID, customer); err != nil {
			return err
		}
		accts, err := s.Invoices.AccountsOf(ctx, tx, c.tenant.ID, customer)
		own = len(accts) > 0
		return err
	}); err != nil {
		return err
	}
	name := whatsapp.SafeText(d.Name)
	if len(open) == 0 {
		_ = s.saveSession(ctx, c, stateIdle, chatData{}, 0)
		return c.say(ctx, s, c.t("wa.debt.owes_nothing", "name", name))
	}
	last := open[len(open)-1].Customer
	to := maskContact(last.Phone)
	if to == "" {
		to = maskContact(last.Email)
	}
	if to == "" {
		_ = s.saveSession(ctx, c, stateIdle, chatData{}, 0)
		return c.say(ctx, s, c.t("wa.debt.send_no_address", "name", name))
	}
	var due int64
	var numbers []string
	recorded := 0
	for _, x := range open {
		due += x.DueKobo
		numbers = append(numbers, x.Number)
		if x.SentAt == nil {
			recorded++
		}
	}
	if len(numbers) > 5 {
		numbers = append(numbers[:5], "…")
	}
	pay := c.t("wa.debt.send_pay_none")
	switch {
	case own:
		pay = c.t("wa.debt.send_pay_account")
	case s.Invoices.PageBase != "":
		pay = c.t("wa.debt.send_pay_link")
	}
	sent := ""
	if recorded > 0 {
		sent = "\n" + c.t("wa.debt.send_recorded", "count", strconv.Itoa(recorded))
	}
	d.Phase, d.Nonce = "confirm", uuid.NewString()
	if err := s.saveDebt(ctx, c, d); err != nil {
		return err
	}
	return c.say(ctx, s, c.t("wa.debt.send_confirm", "name", name, "amount", invoicing.Naira(due), "lines", strings.Join(numbers, ", "), "to", to,
		"pay", pay)+sent, tableYesButtons(c)...)
}

// waSendNow sends it, once for the read-back's key.
func (s *Server) waSendNow(ctx context.Context, c *chat, d *chatDebt) error {
	var out invoicing.BalanceSent
	err := s.waBookIn(ctx, c, func(tx pgx.Tx) error {
		var err error
		out, err = s.Invoices.SendBalance(ctx, tx, c.tenant.ID, "key:"+d.Key, c.p.Actor(), "chat:"+d.Nonce)
		return err
	})
	if errors.Is(err, invoicing.ErrInvalid) || errors.Is(err, invoicing.ErrState) {
		_ = s.saveSession(ctx, c, stateIdle, chatData{}, 0)
		return c.say(ctx, s, c.t("wa.debt.send_refused", "reason", whatsapp.SafeText(strings.TrimPrefix(strings.TrimPrefix(err.Error(), "invoicing: invalid: "), "invoicing: "))))
	}
	if err != nil {
		return err
	}
	if err := s.saveSession(ctx, c, stateIdle, chatData{}, 0); err != nil {
		return err
	}
	name := whatsapp.SafeText(d.Name)
	if len(out.Queued) == 0 {
		return c.say(ctx, s, c.t("wa.debt.sent_nothing", "name", name))
	}
	channels := make([]string, len(out.Queued))
	for i, ch := range out.Queued {
		channels[i] = map[string]string{"sms": "SMS"}[ch]
		if channels[i] == "" {
			channels[i] = ch
		}
	}
	return c.say(ctx, s, c.t("wa.debt.sent", "name", name, "amount", invoicing.Naira(out.DueKobo), "channels", strings.Join(channels, " + ")))
}

// maskContact shows a phone or email as a message may: its last three
// digits, or the email's first letter and domain.
func maskContact(v string) string {
	if v == "" {
		return ""
	}
	if user, domain, ok := strings.Cut(v, "@"); ok {
		return maskName(user) + "@" + domain
	}
	if len(v) <= 3 {
		return "•••"
	}
	return "•••" + v[len(v)-3:]
}
