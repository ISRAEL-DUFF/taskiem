package api

import (
	"context"
	"errors"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/invoicing"
	"github.com/israel-duff/taskiem/engine/lang"
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
	if err := s.saveSession(ctx, c, stateCollecting, chatData{Invoice: inv}, waPendingTTL); err != nil {
		return err
	}
	return c.say(ctx, s, c.t("wa.invoice.contact", "name", whatsapp.SafeText(sender.CleanName(name))))
}

// waInvoiceReply takes the customer's address, then Yes or No.
func (s *Server) waInvoiceReply(ctx context.Context, c *chat, text, cmd string) error {
	inv := c.data.Invoice
	name, err := s.openString(ctx, c, inv.Name)
	if err != nil {
		return err
	}
	if c.state == stateCollecting {
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
	addr, err := s.openString(ctx, c, inv.Contact)
	if err != nil {
		return err
	}
	cust := invoicing.Customer{Name: name, Phone: addr}
	if inv.Email {
		cust = invoicing.Customer{Name: name, Email: addr}
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
