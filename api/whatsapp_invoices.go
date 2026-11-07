package api

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/invoicing"
	"github.com/israel-duff/taskiem/engine/lang"
	"github.com/israel-duff/taskiem/engine/links"
	"github.com/israel-duff/taskiem/engine/pii"
	"github.com/israel-duff/taskiem/engine/sender"
	"github.com/israel-duff/taskiem/engine/whatsapp"
)

// Invoices from chat (decision 0045, T3): "invoice Ada 25,000 for 5 bags
// of rice" asks for Ada's phone number or email, shows the invoice and
// sends it on Yes. Members with invoice.manage only. The customer's name
// and address wait sealed in the session, as a run's inputs do.

// chatInvoice is an invoice being made in chat.
type chatInvoice struct {
	Name       map[string]any `json:"name"` // sealed
	AmountKobo int64          `json:"amount_kobo"`
	For        string         `json:"for,omitempty"`
	Contact    map[string]any `json:"contact,omitempty"` // sealed
	Email      bool           `json:"email,omitempty"`
	// LinkKey and LinkName are the linked business the invoice is for
	// (customer key link:<id>): it goes into their bills, with no phone or
	// email asked. Pick is the linked businesses the name could mean, while
	// the person says which.
	LinkKey  string       `json:"link_key,omitempty"`
	LinkName string       `json:"link_name,omitempty"`
	Pick     []linkedPick `json:"pick,omitempty"`
}

// linkedPick is a linked business an invoice's customer name could mean.
type linkedPick struct {
	Key  string `json:"key"`
	Name string `json:"name"`
}

// linkedCustomersNamed lists the linked businesses, active and buying from
// us, that a customer name typed in chat could mean: the one whose name is
// the same, else those whose names have every word of it.
func (s *Server) linkedCustomersNamed(ctx context.Context, c *chat, name string) ([]linkedPick, error) {
	var list []links.Link
	if err := chatTx(ctx, s, c, func(tx pgx.Tx) error {
		var err error
		list, _, err = links.ListTx(ctx, tx)
		return err
	}); err != nil {
		return nil, err
	}
	words := strings.Fields(strings.ToLower(name))
	if len(words) == 0 {
		return nil, nil
	}
	var all, exact []linkedPick
	for _, l := range list {
		if l.State != links.Active || l.Role != links.RoleCustomer && l.Role != links.RoleBoth {
			continue
		}
		lower := strings.ToLower(l.Name)
		if !slices.ContainsFunc(words, func(w string) bool { return !strings.Contains(lower, w) }) {
			p := linkedPick{Key: "link:" + l.Business.String(), Name: l.Name}
			all = append(all, p)
			if strings.EqualFold(strings.TrimSpace(l.Name), strings.TrimSpace(name)) {
				exact = append(exact, p)
			}
		}
	}
	if len(exact) == 1 {
		return exact, nil
	}
	return all, nil
}

var invoiceAmount = regexp.MustCompile(`^(?i)(?:₦|n|ngn)?\s*(\d{1,3}(?:,\d{3})+|\d+)(?:\.(\d{1,2}))?(k)?$`)

// parseInvoiceCommand reads "invoice <name> <amount> [for <what>]" (or
// "bill …"): the customer's name as typed, the amount in kobo, and what
// it is for.
func parseInvoiceCommand(text string) (name string, kobo int64, what string, ok bool) {
	fields := strings.Fields(text)
	if len(fields) < 3 {
		return "", 0, "", false
	}
	switch strings.ToLower(fields[0]) {
	case "invoice", "bill":
	default:
		return "", 0, "", false
	}
	return parseInvoiceArgs(fields[1:], nil)
}

// parseInvoiceArgs reads what follows the command word: "<name> <amount>
// [for <what>]", "for" being English's or one of forWords (another
// language's, folded: engine/lang ForWords).
func parseInvoiceArgs(rest, forWords []string) (name string, kobo int64, what string, ok bool) {
	for i := range rest {
		if isForWord(rest[i], forWords) && i+1 < len(rest) {
			what = strings.Join(rest[i+1:], " ")
			rest = rest[:i]
			break
		}
	}
	if len(rest) < 2 {
		return "", 0, "", false
	}
	m := invoiceAmount.FindStringSubmatch(rest[len(rest)-1])
	if m == nil {
		return "", 0, "", false
	}
	n, err := strconv.ParseInt(strings.ReplaceAll(m[1], ",", ""), 10, 64)
	if err != nil {
		return "", 0, "", false
	}
	kobo = n * 100
	if m[2] != "" {
		frac := m[2]
		if len(frac) == 1 {
			frac += "0"
		}
		k, _ := strconv.ParseInt(frac, 10, 64)
		kobo += k
	}
	if m[3] != "" {
		kobo *= 1000
	}
	name = strings.Join(rest[:len(rest)-1], " ")
	if name == "" || kobo < 100 || kobo > invoicing.MaxTotalKobo {
		return "", 0, "", false
	}
	return name, kobo, strings.TrimSpace(what), true
}

// isForWord is "for", or one of forWords typed with or without accents.
func isForWord(w string, forWords []string) bool {
	return strings.EqualFold(w, "for") || slices.Contains(forWords, lang.Fold(w))
}

// waInvoiceCommand starts an invoice from an idle chat; false when the
// text is not an invoice command.
func (s *Server) waInvoiceCommand(ctx context.Context, c *chat, text, cmd string) (bool, error) {
	if !strings.HasPrefix(cmd, "invoice") && !strings.HasPrefix(cmd, "bill ") {
		return false, nil
	}
	if s.Invoices == nil {
		return false, nil
	}
	if !c.p.Can(PermInvoiceManage) {
		return true, c.say(ctx, s, c.t("wa.invoice.denied", "org", c.tenant.Name))
	}
	name, kobo, what, ok := parseInvoiceCommand(text)
	return true, s.waInvoiceStart(ctx, c, name, kobo, what, ok)
}

// waInvoiceWords starts an invoice from another language's command words
// (engine/lang intents/<tag>.json): "ìwé owó Ada 25,000 fún ìrẹsì";
// false when invoices are off.
func (s *Server) waInvoiceWords(ctx context.Context, c *chat, m lang.Match) (bool, error) {
	if s.Invoices == nil {
		return false, nil
	}
	if !c.p.Can(PermInvoiceManage) {
		return true, c.say(ctx, s, c.t("wa.invoice.denied", "org", c.tenant.Name))
	}
	name, kobo, what, ok := parseInvoiceArgs(strings.Fields(m.Arg), lang.ForWords(m.Language))
	return true, s.waInvoiceStart(ctx, c, name, kobo, what, ok)
}

// waInvoiceStart asks for the customer's address of an invoice command
// read (ok), or says how to write one.
func (s *Server) waInvoiceStart(ctx context.Context, c *chat, name string, kobo int64, what string, ok bool) error {
	if !ok {
		return c.say(ctx, s, c.t("wa.invoice.how"))
	}
	inv := &chatInvoice{AmountKobo: kobo, For: what}
	if err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{c.tenant.ID}, func(tx pgx.Tx) error {
		var err error
		inv.Name, err = s.Store.PII.SealTx(ctx, tx, c.tenant.ID, "name", sender.CleanName(name))
		return err
	}); err != nil {
		return err
	}
	// A business the owner is linked with, who buys from them, is invoiced
	// as a bill in its own account: no phone or email to ask.
	linked, err := s.linkedCustomersNamed(ctx, c, name)
	if err != nil {
		return err
	}
	switch {
	case len(linked) == 1:
		inv.LinkKey, inv.LinkName = linked[0].Key, linked[0].Name
		return s.waInvoiceConfirmLinked(ctx, c, inv)
	case len(linked) > 1:
		inv.Pick = linked[:min(len(linked), 8)]
		if err := s.saveSession(ctx, c, stateCollecting, chatData{Invoice: inv}, waPendingTTL); err != nil {
			return err
		}
		var list []string
		for i, p := range inv.Pick {
			list = append(list, fmt.Sprintf("%d. %s", i+1, whatsapp.SafeText(p.Name)))
		}
		return c.say(ctx, s, c.t("wa.invoice.pick_linked", "name", whatsapp.SafeText(sender.CleanName(name)), "list", strings.Join(list, "\n")))
	}
	if err := s.saveSession(ctx, c, stateCollecting, chatData{Invoice: inv}, waPendingTTL); err != nil {
		return err
	}
	return c.say(ctx, s, c.t("wa.invoice.contact", "name", whatsapp.SafeText(sender.CleanName(name))))
}

// waInvoiceConfirmLinked shows an invoice for a linked business and waits
// for Yes.
func (s *Server) waInvoiceConfirmLinked(ctx context.Context, c *chat, inv *chatInvoice) error {
	inv.Pick = nil
	if err := s.saveSession(ctx, c, stateConfirming, chatData{Invoice: inv, Nonce: uuid.NewString()}, waPendingTTL); err != nil {
		return err
	}
	what := inv.For
	if what == "" {
		what = c.t("wa.invoice.amount_due")
	}
	return c.say(ctx, s, c.t("wa.invoice.confirm_linked", "name", whatsapp.SafeText(inv.LinkName), "amount", invoicing.Naira(inv.AmountKobo),
		"for", whatsapp.SafeText(what)), confirmButtons(c)...)
}

// waInvoiceReply takes the customer's address, then Yes or No.
func (s *Server) waInvoiceReply(ctx context.Context, c *chat, text, cmd string) error {
	inv := c.data.Invoice
	name, err := s.openString(ctx, c, inv.Name)
	if err != nil {
		return err
	}
	if c.state == stateCollecting {
		// Which linked business was meant, by its number.
		if n, err := strconv.Atoi(strings.TrimSpace(text)); len(inv.Pick) > 0 && err == nil && n >= 1 && n <= len(inv.Pick) {
			inv.LinkKey, inv.LinkName = inv.Pick[n-1].Key, inv.Pick[n-1].Name
			return s.waInvoiceConfirmLinked(ctx, c, inv)
		}
		inv.Pick = nil // an address typed instead: someone else
		addr, isEmail := "", false
		if e, err := sender.NormaliseEmail(strings.TrimSpace(text)); err == nil && strings.Contains(text, "@") {
			addr, isEmail = e, true
		} else if p, err := sender.NormalisePhone(strings.TrimSpace(text), s.smsCountries()); err == nil {
			addr = p
		}
		if addr == "" {
			return c.say(ctx, s, c.t("wa.invoice.contact_again", "name", whatsapp.SafeText(name)))
		}
		if err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{c.tenant.ID}, func(tx pgx.Tx) error {
			cat := "phone"
			if isEmail {
				cat = "email"
			}
			var err error
			inv.Contact, err = s.Store.PII.SealTx(ctx, tx, c.tenant.ID, cat, addr)
			return err
		}); err != nil {
			return err
		}
		inv.Email = isEmail
		data := chatData{Invoice: inv, Nonce: uuid.NewString()}
		if err := s.saveSession(ctx, c, stateConfirming, data, waPendingTTL); err != nil {
			return err
		}
		what := inv.For
		if what == "" {
			what = c.t("wa.invoice.amount_due")
		}
		return c.say(ctx, s, c.t("wa.invoice.confirm", "name", whatsapp.SafeText(name), "to", maskAddress(addr), "amount", invoicing.Naira(inv.AmountKobo),
			"for", whatsapp.SafeText(what)), confirmButtons(c)...)
	}
	switch cmd {
	case "yes", "y", "send", "confirm":
	case "no", "n":
		if err := s.saveSession(ctx, c, stateIdle, chatData{}, 0); err != nil {
			return err
		}
		return c.say(ctx, s, c.t("wa.invoice.not_sent"))
	default:
		return c.say(ctx, s, c.t("wa.invoice.confirm_reminder"), confirmButtons(c)...)
	}
	if !c.p.Can(PermInvoiceManage) {
		return c.say(ctx, s, c.t("wa.invoice.denied", "org", c.tenant.Name))
	}
	var cust invoicing.Customer
	if inv.LinkKey != "" {
		// A linked business: its bills, with no address.
		name, cust = inv.LinkName, invoicing.Customer{Name: inv.LinkName, Key: inv.LinkKey}
	} else {
		addr, err := s.openString(ctx, c, inv.Contact)
		if err != nil {
			return err
		}
		cust = invoicing.Customer{Name: name, Phone: addr}
		if inv.Email {
			cust = invoicing.Customer{Name: name, Email: addr}
		}
	}
	svc := invoicing.Service{Invoices: s.Invoices}
	actor := "user:" + c.p.UserID.String()
	x, err := svc.CreateOnce(ctx, c.tenant.ID, "chat:"+c.data.Nonce, invoicing.Draft{Customer: cust, AmountKobo: inv.AmountKobo, Description: inv.For}, actor)
	if errors.Is(err, invoicing.ErrInvalid) {
		_ = s.saveSession(ctx, c, stateIdle, chatData{}, 0)
		return c.say(ctx, s, c.t("wa.invoice.refused", "reason", plainInvoiceError(err)))
	}
	if err != nil {
		return err
	}
	sent, err := s.Invoices.Send(ctx, c.tenant.ID, x.Number, actor, "chat:"+c.data.Nonce)
	if err != nil {
		return err
	}
	if err := s.saveSession(ctx, c, stateIdle, chatData{}, 0); err != nil {
		return err
	}
	if inv.LinkKey != "" {
		return c.say(ctx, s, c.t("wa.invoice.sent_linked", "number", x.Number, "name", whatsapp.SafeText(name), "amount", invoicing.Naira(x.TotalKobo)))
	}
	if len(sent.Queued) == 0 {
		return c.say(ctx, s, c.t("wa.invoice.saved_not_sent", "number", x.Number))
	}
	return c.say(ctx, s, c.t("wa.invoice.sent", "number", x.Number, "name", whatsapp.SafeText(name), "amount", invoicing.Naira(x.TotalKobo)))
}

func (s *Server) openString(ctx context.Context, c *chat, env map[string]any) (string, error) {
	var out string
	err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{c.tenant.ID}, func(tx pgx.Tx) error {
		v, err := s.Store.PII.OpenTx(ctx, tx, c.tenant.ID, env)
		if errors.Is(err, pii.ErrErased) {
			return nil
		}
		out, _ = v.(string)
		return err
	})
	return out, err
}

// maskAddress shows enough of a phone or email to recognise it.
func maskAddress(a string) string {
	if at := strings.IndexByte(a, '@'); at > 0 {
		return a[:1] + "•••" + a[at:]
	}
	if len(a) > 4 {
		return "•••" + a[len(a)-4:]
	}
	return a
}

// smsCountries are the calling codes SMS may go to: the deployment's, or
// Nigeria's.
func (s *Server) smsCountries() []string {
	if s.Invoices != nil && s.Invoices.Messages != nil && len(s.Invoices.Messages.Countries) > 0 {
		return s.Invoices.Messages.Countries
	}
	return []string{"234"}
}
