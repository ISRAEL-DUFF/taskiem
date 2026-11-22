package api

import (
	"context"
	"errors"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/invoicing"
	"github.com/israel-duff/taskiem/engine/lang"
	"github.com/israel-duff/taskiem/engine/moneylog"
	"github.com/israel-duff/taskiem/engine/spend"
	"github.com/israel-duff/taskiem/engine/whatsapp"
)

// Cash and expenses from chat (spec 7.4, T6): "spent 12,000 on diesel at
// Total", "paid 15,000 diesel", "expense 5000 fuel", "cash in 20,000
// till", "cash out 3,000 float". An expense is labelled by what it was
// for, in the business's own words (what a business buys §5.4, §6):
// "paid 15,000 diesel" is labelled Diesel, the label found (without
// case) or made when it is recorded. Members with invoice.manage only, as for the API's
// POST /v1/money/entries. The entry is shown and recorded on Yes, once:
// its key is the confirmation's nonce, so a Yes delivered twice records
// one entry. Money moves through "cash" (the log's default channel).
// The other languages' words ("mo ná 12,000 fún epo") are draft lists in
// engine/lang intents/<tag>.json; English's work in every language.

// chatMoney is cash or an expense waiting for Yes.
type chatMoney struct {
	Kind        string `json:"kind"`      // cash or expense
	Direction   string `json:"direction"` // in or out
	AmountKobo  int64  `json:"amount_kobo"`
	Description string `json:"description"`
	Vendor      string `json:"vendor,omitempty"`
	// Label is an expense's label: what it was for, with a capital
	// (spend.NameFrom), or the business's own spelling of a label it has.
	Label string `json:"label,omitempty"`
}

// parseKobo reads an amount as chat writes it: "12,000", "₦12,000.50",
// "N2500", "12k".
func parseKobo(tok string) (int64, bool) {
	m := invoiceAmount.FindStringSubmatch(tok)
	if m == nil {
		return 0, false
	}
	var kobo int64
	for _, d := range strings.ReplaceAll(m[1], ",", "") {
		kobo = kobo*10 + int64(d-'0')
		if kobo > invoicing.MaxTotalKobo {
			return 0, false
		}
	}
	kobo *= 100
	if m[2] != "" {
		frac := m[2]
		if len(frac) == 1 {
			frac += "0"
		}
		kobo += int64(frac[0]-'0')*10 + int64(frac[1]-'0')
	}
	if m[3] != "" {
		kobo *= 1000
	}
	if kobo < 100 || kobo > invoicing.MaxTotalKobo {
		return 0, false
	}
	return kobo, true
}

// parseMoneyCommand reads "spent|paid <amount> [on|for] <what> [at <vendor>]",
// "expense <amount> [on|for] <what> [at <vendor>]" and "cash in|out
// <amount> [<what>]". command is false for text that is none of them;
// ok is false for one of them that cannot be read.
func parseMoneyCommand(text string) (m chatMoney, command, ok bool) {
	fields := strings.Fields(text)
	if len(fields) == 0 {
		return m, false, false
	}
	var rest []string
	switch w := strings.ToLower(fields[0]); {
	case w == "spent" || w == "paid" || w == "expense":
		m.Kind, m.Direction, rest = "expense", "out", fields[1:]
	case w == "cash" && len(fields) > 1 && (strings.EqualFold(fields[1], "in") || strings.EqualFold(fields[1], "out")):
		m.Kind, m.Direction, rest = "cash", strings.ToLower(fields[1]), fields[2:]
	default:
		return m, false, false
	}
	if len(rest) == 0 {
		return m, true, false
	}
	m, isAmount, ok := parseMoneyArgs(m, rest, nil)
	if !isAmount {
		// "expense report every Friday" is not an expense.
		// Nor is "paid Ada's invoice": "paid" is an expense only with
		// an amount.
		return m, m.Kind != "expense" || strings.ToLower(fields[0]) == "spent", false
	}
	return m, true, ok
}

// parseMoneyWords reads an expense or cash from another language's
// command words (engine/lang intents/<tag>.json): "mo ná 12,000 fún epo".
// Only an amount right after the words makes it a command, so ordinary
// chat that starts with them ("I spend the whole day…") is left alone.
func parseMoneyWords(mm lang.Match) (m chatMoney, command, ok bool) {
	switch mm.Intent {
	case lang.IntentExpense:
		m.Kind, m.Direction = "expense", "out"
	case lang.IntentCashIn:
		m.Kind, m.Direction = "cash", "in"
	case lang.IntentCashOut:
		m.Kind, m.Direction = "cash", "out"
	default:
		return m, false, false
	}
	return parseMoneyArgs(m, strings.Fields(mm.Arg), lang.ForWords(mm.Language))
}

// parseMoneyArgs reads "<amount> [on|for] <what> [at <vendor>]" into m;
// isAmount is false when it does not start with an amount. forWords are
// another language's words for "for", folded.
func parseMoneyArgs(m chatMoney, rest, forWords []string) (_ chatMoney, isAmount, ok bool) {
	if len(rest) == 0 {
		return m, false, false
	}
	kobo, isAmount := parseKobo(rest[0])
	if !isAmount {
		return m, false, false
	}
	m.AmountKobo, rest = kobo, rest[1:]
	if len(rest) > 0 && (strings.EqualFold(rest[0], "on") || isForWord(rest[0], forWords)) {
		rest = rest[1:]
	}
	for i := len(rest) - 1; i > 0; i-- {
		if strings.EqualFold(rest[i], "at") && i+1 < len(rest) {
			m.Vendor = strings.Join(rest[i+1:], " ")
			rest = rest[:i]
			break
		}
	}
	m.Description = strings.TrimSpace(strings.Join(rest, " "))
	if m.Kind == "expense" {
		if m.Description == "" {
			return m, true, false
		}
		m.Label = spend.NameFrom(m.Description)
	} else if m.Description == "" {
		m.Description = "Cash " + m.Direction
	}
	if len(m.Description) > 200 || len(m.Vendor) > 120 {
		return m, true, false
	}
	return m, true, true
}

// waMoneyCommand starts cash or an expense from an idle chat; false when
// the text is not one.
func (s *Server) waMoneyCommand(ctx context.Context, c *chat, text string) (bool, error) {
	m, command, ok := parseMoneyCommand(text)
	return s.waMoneyStart(ctx, c, m, command, ok)
}

// waMoneyWords is waMoneyCommand for another language's command words.
func (s *Server) waMoneyWords(ctx context.Context, c *chat, mm lang.Match) (bool, error) {
	m, command, ok := parseMoneyWords(mm)
	return s.waMoneyStart(ctx, c, m, command, ok)
}

func (s *Server) waMoneyStart(ctx context.Context, c *chat, m chatMoney, command, ok bool) (bool, error) {
	if !command {
		return false, nil
	}
	if !c.p.Can(PermInvoiceManage) {
		return true, c.say(ctx, s, c.t("wa.money.denied", "org", c.tenant.Name))
	}
	if !ok {
		return true, c.say(ctx, s, c.t("wa.money.how"))
	}
	if m.Kind == "expense" {
		// A label the business has already reads in its own spelling.
		if err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{c.tenant.ID}, func(tx pgx.Tx) error {
			l, err := spend.FindTx(ctx, tx, c.tenant.ID, m.Label)
			if err == nil {
				m.Label = l.Name
			} else if errors.Is(err, spend.ErrNoLabel) || errors.Is(err, spend.ErrLabel) {
				err = nil
			}
			return err
		}); err != nil {
			return true, err
		}
	}
	data := chatData{Money: &m, Nonce: uuid.NewString()}
	if err := s.saveSession(ctx, c, stateConfirming, data, waPendingTTL); err != nil {
		return true, err
	}
	return true, c.say(ctx, s, moneyConfirmText(c, m), moneyButtons(c)...)
}

func moneyConfirmText(c *chat, m chatMoney) string {
	amount, what := invoicing.Naira(m.AmountKobo), whatsapp.SafeText(m.Description)
	switch {
	case m.Kind == "cash" && m.Direction == "in":
		return c.t("wa.money.confirm_cash_in", "amount", amount, "what", what)
	case m.Kind == "cash":
		return c.t("wa.money.confirm_cash_out", "amount", amount, "what", what)
	case m.Vendor != "":
		return c.t("wa.money.confirm_expense_at", "amount", amount, "what", what, "vendor", whatsapp.SafeText(m.Vendor), "category", whatsapp.SafeText(m.Label))
	}
	return c.t("wa.money.confirm_expense", "amount", amount, "what", what, "category", whatsapp.SafeText(m.Label))
}

func moneyButtons(c *chat) []whatsapp.Button {
	return []whatsapp.Button{{ID: "yes", Title: c.t("button.yes")}, {ID: "no", Title: c.t("button.no")}}
}

// waMoneyReply records the entry on Yes, once for the confirmation.
func (s *Server) waMoneyReply(ctx context.Context, c *chat, cmd string) error {
	m := c.data.Money
	switch cmd {
	case "yes", "y", "confirm", "record":
	case "no", "n":
		if err := s.saveSession(ctx, c, stateIdle, chatData{}, 0); err != nil {
			return err
		}
		return c.say(ctx, s, c.t("wa.money.not_recorded"))
	default:
		return c.say(ctx, s, c.t("wa.money.confirm_reminder"), moneyButtons(c)...)
	}
	if !c.p.Can(PermInvoiceManage) {
		_ = s.saveSession(ctx, c, stateIdle, chatData{}, 0)
		return c.say(ctx, s, c.t("wa.money.denied", "org", c.tenant.Name))
	}
	in := moneylog.Entered{Kind: m.Kind, Direction: m.Direction, AmountKobo: m.AmountKobo, Description: m.Description, Vendor: m.Vendor,
		Key: "chat:" + c.data.Nonce}
	category := "cash"
	err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{c.tenant.ID}, func(tx pgx.Tx) error {
		if m.Kind == "expense" && m.Label != "" {
			// The label found or made at Yes: one typed by someone else
			// in between is the same label.
			l, _, err := spend.FindOrMakeTx(ctx, tx, c.tenant.ID, m.Label, c.p.Actor())
			if err != nil {
				return err
			}
			in.LabelID, in.Category, category = &l.ID, l.Name, l.Name
		}
		_, _, err := s.moneyLog().EnterTx(ctx, tx, c.tenant.ID, c.p.Actor(), in)
		return err
	})
	if errors.Is(err, moneylog.ErrInvalid) || errors.Is(err, spend.ErrLabel) {
		_ = s.saveSession(ctx, c, stateIdle, chatData{}, 0)
		reason := strings.TrimPrefix(strings.TrimPrefix(err.Error(), "moneylog: invalid entry: "), "spend: label: ")
		return c.say(ctx, s, c.t("wa.money.refused", "reason", reason))
	}
	if err != nil {
		return err
	}
	if err := s.saveSession(ctx, c, stateIdle, chatData{}, 0); err != nil {
		return err
	}
	id := "wa.money.recorded_out"
	if m.Direction == "in" {
		id = "wa.money.recorded_in"
	}
	return c.say(ctx, s, c.t(id, "amount", invoicing.Naira(m.AmountKobo), "category", whatsapp.SafeText(category)))
}
