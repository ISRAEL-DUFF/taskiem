package api

import (
	"context"
	"errors"
	"regexp"
	"slices"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/invoicing"
	"github.com/israel-duff/taskiem/engine/saved"
	"github.com/israel-duff/taskiem/engine/whatsapp"
)

// Paying from the chat (P1; docs/spec/chat-pay-people.md, "On WhatsApp,
// Telegram and Slack"): "send 45k to @bayo for a delivery", or "transfer
// 45k to 0123456789 gtbank for stock bought", prepares a payment, as the
// Pay topic's forms do (pay.person, pay.account). Nothing pays on words:
// the answer says who and how much (the bank's name for the account when it
// is known) and links to the payment, to confirm with a passkey.

var (
	// "send|pay|transfer <amount> to <who> [for <what>]"
	payToWords = regexp.MustCompile(`(?is)^\s*(?:send|pay|transfer)\s+(\S+)\s+to\s+(.+?)(?:\s+for\s+(.+?))?\s*$`)
	// "pay @who <amount> [for <what>]"
	payNameWords = regexp.MustCompile(`(?is)^\s*pay\s+(@\S+)\s+(\S+)(?:\s+for\s+(.+?))?\s*$`)
	payDigits    = regexp.MustCompile(`\d[\d -]{8,14}\d`)
)

// payCommand is what the words say to pay.
type payCommand struct {
	AmountKobo    int64
	Payee         string // a saved account's name
	AccountNumber string
	BankName      string
	Purpose       string
	PurposeNote   string
	Note          string
}

// readPayWords reads a payment from the words; ok false when they are not
// one.
func readPayWords(text string) (payCommand, bool) {
	var amount, who, what string
	if m := payNameWords.FindStringSubmatch(text); m != nil {
		amount, who, what = m[2], m[1], m[3]
	} else if m := payToWords.FindStringSubmatch(text); m != nil {
		amount, who, what = m[1], m[2], m[3]
	} else {
		return payCommand{}, false
	}
	kobo, ok := parseKobo(amount)
	if !ok {
		return payCommand{}, false
	}
	x := payCommand{AmountKobo: kobo}
	who = strings.TrimSpace(strings.TrimRight(who, ".!?"))
	if d := payDigits.FindString(who); d != "" {
		n, err := saved.AccountNumber(d)
		if err != nil {
			return payCommand{}, false
		}
		x.AccountNumber = n
		x.BankName = saved.BankNamed(strings.Replace(who, d, " ", 1))
	} else {
		name, err := saved.Name(who)
		if err != nil || strings.Contains(strings.TrimPrefix(who, "@"), " ") || strings.Trim(name, "0123456789") == "" {
			return payCommand{}, false
		}
		x.Payee = name
	}
	x.Purpose, x.PurposeNote, x.Note = readPurpose(what)
	return x, true
}

// purposeWords are the words that say what a payment is for, as the
// catalogue's choices and as people write them.
var purposeWords = map[string]string{
	"bill": "bill", "bills": "bill",
	"wages": "wages", "wage": "wages", "salary": "wages", "salaries": "wages",
	"delivery": "delivery", "deliveries": "delivery", "transport": "delivery", "dispatch": "delivery",
	"refund": "refund", "refunds": "refund",
	"stock": "stock", "goods": "stock", "restock": "stock",
	"other": "other",
}

// readPurpose reads "for <what>": a purpose by its first word ("a
// delivery", "stock bought"), the rest the note; or other, the words its
// own ("for the generator").
func readPurpose(what string) (purpose, purposeNote, note string) {
	what = strings.TrimSpace(strings.TrimRight(what, ".!?"))
	if what == "" {
		return "other", "", ""
	}
	if i := strings.Index(what, ":"); i >= 0 {
		what, note = strings.TrimSpace(what[:i]), strings.TrimSpace(what[i+1:])
	}
	words := strings.Fields(what)
	if len(words) > 1 && slices.Contains([]string{"a", "an", "the", "his", "her", "their", "my", "our"}, strings.ToLower(words[0])) {
		words = words[1:]
	}
	if p, ok := purposeWords[strings.ToLower(words[0])]; ok {
		rest := words[1:]
		if p == "stock" && len(rest) > 0 && strings.EqualFold(rest[0], "bought") {
			rest = rest[1:]
		}
		if len(rest) > 0 && note == "" {
			note = strings.Join(rest, " ")
		}
		return p, "", clipRunes(note, 100)
	}
	return "other", clipRunes(what, 80), clipRunes(note, 100)
}

func clipRunes(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n])
	}
	return s
}

// waPayCommand prepares a payment from the words; false when they are not
// a payment, or the person may not pay here (the words then pass on).
func (s *Server) waPayCommand(ctx context.Context, c *chat, text string) (bool, error) {
	if c.p == nil || c.p.UserID == uuid.Nil || c.tenant.ID == uuid.Nil || !c.p.Can(PermMoneyPay) {
		return false, nil
	}
	cmd, ok := readPayWords(text)
	if !ok {
		return false, nil
	}
	if cmd.AccountNumber != "" && cmd.BankName == "" {
		return true, c.say(ctx, s, c.t("wa.pay.which_bank"))
	}
	in := payIn{Payee: cmd.Payee, AccountNumber: cmd.AccountNumber, BankName: cmd.BankName, AmountKobo: cmd.AmountKobo,
		Purpose: cmd.Purpose, PurposeNote: cmd.PurposeNote, Note: cmd.Note, From: "chat"}
	owner := slices.Contains(c.p.Roles, "owner")
	x, err := s.preparePayment(ctx, c.tenant.ID, c.p.UserID, owner, in, c.p.Actor())
	var pr payRefused
	if errors.As(err, &pr) && pr.code == "no_payee" && owner && cmd.Payee != "" {
		// An owner's own saved account, by its name.
		in.Mine = true
		x, err = s.preparePayment(ctx, c.tenant.ID, c.p.UserID, owner, in, c.p.Actor())
	}
	if errors.As(err, &pr) {
		return true, c.say(ctx, s, c.t("wa.pay.not", "reason", pr.msg))
	}
	if err != nil {
		return true, err
	}
	if err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{c.tenant.ID}, func(tx pgx.Tx) error {
		return waAudit(ctx, tx, c.ch.Name(), c.p, "payment.prepare", x.ID.String(),
			map[string]any{"amount_kobo": x.AmountKobo, "purpose": x.Purpose, "payee": x.Payee, "personal": x.Personal})
	}); err != nil {
		return true, err
	}
	if err := c.say(ctx, s, s.payPreparedWords(c, x)); err != nil {
		return true, err
	}
	return true, s.waOfferPayPin(ctx, c, x)
}

// payPreparedWords say a prepared payment, with what the person confirming
// should know, and where to confirm it.
func (s *Server) payPreparedWords(c *chat, x payment) string {
	to := x.Payee
	if x.AccountName != nil && *x.AccountName != "" && !strings.EqualFold(*x.AccountName, x.Payee) {
		to += " (" + *x.AccountName + ")"
	}
	what := paymentPurposeWords(x.Purpose)
	if x.PurposeNote != "" {
		what = x.PurposeNote
	}
	lines := []string{c.t("wa.pay.prepared", "amount", invoicing.Naira(x.AmountKobo), "to", whatsapp.SafeText(to), "bank", x.BankName, "account", x.Account, "for", whatsapp.SafeText(what))}
	if x.FirstPayment {
		lines = append(lines, c.t("wa.pay.first"))
	}
	if x.ChangedAccount {
		lines = append(lines, c.t("wa.pay.changed"))
	}
	if x.Again {
		lines = append(lines, c.t("wa.pay.again"))
	}
	if s.PublicURL != "" {
		lines = append(lines, c.t("wa.pay.confirm_link", "link", strings.TrimRight(s.PublicURL, "/")+"/payments/"+x.ID.String()))
	} else {
		lines = append(lines, c.t("wa.pay.confirm_app"))
	}
	return strings.Join(lines, "\n")
}
