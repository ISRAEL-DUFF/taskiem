package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/invoicing"
	"github.com/israel-duff/taskiem/engine/saved"
)

// Paying several people from the chat (P4; docs/spec/chat-pay-people.md,
// "WhatsApp, Telegram and Slack" and "Pay several people"): "pay my list"
// makes the person's collected list a batch (collectionBatch), and a list
// pasted under "pay these" is read a line at a time (readBatchLines) and
// prepared as a batch. A batch is never confirmed in text: the answer says
// how many people and how much, with a link to check and pay it with a
// passkey, then each row held out and each line not read, with why.

var (
	// "pay my list", "pay the list", "pay my list for wages".
	payMyListWords = regexp.MustCompile(`(?i)^\s*pay\s+(?:my|the)\s+list(?:\s+for\s+(.+?))?\s*[.!]?\s*$`)
	// "pay these", "pay this list", "pay these for wages:", a list's first
	// line.
	payTheseWords = regexp.MustCompile(`(?i)^\s*pay\s+(?:these|this\s+list)(?:\s+for\s+(.+?))?\s*[:.!]?\s*$`)
	// "@bayo 45k", "@bayo 45k for rice": a saved account by its name.
	payNameLine = regexp.MustCompile(`^\s*@(\S+)\s+(.+?)\s*$`)
	payLineFor  = regexp.MustCompile(`(?i)^(.*?)\s+for\s+(.+)$`)
)

// waPayList answers "pay my list" and a pasted list; false when the words
// are neither, or the person may not pay here (the words then pass on).
func (s *Server) waPayList(ctx context.Context, c *chat, text string) (bool, error) {
	if c.p == nil || c.p.UserID == uuid.Nil || c.tenant.ID == uuid.Nil || !c.p.Can(PermMoneyPay) {
		return false, nil
	}
	var lines []string
	for _, l := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		if strings.TrimSpace(l) != "" {
			lines = append(lines, strings.TrimSpace(l))
		}
	}
	if len(lines) == 0 {
		return false, nil
	}
	if m := payMyListWords.FindStringSubmatch(lines[0]); m != nil && len(lines) == 1 {
		return true, s.waPayCollection(ctx, c, m[1])
	}
	m := payTheseWords.FindStringSubmatch(lines[0])
	if m == nil {
		return false, nil
	}
	if len(lines) == 1 {
		return true, c.say(ctx, s, c.t("wa.pay.list.how"))
	}
	return true, s.waPayPasted(ctx, c, m[1], lines[1:])
}

// chatRequest is a request carrying the chat's person, for the code the
// web's handlers share (prepareBatchView).
func chatRequest(ctx context.Context, c *chat) (*http.Request, error) {
	return http.NewRequestWithContext(context.WithValue(ctx, ctxKey{}, c.p), http.MethodPost, "/", nil)
}

// waPayCollection makes the person's list (from messages) a batch, as
// POST /v1/pay-collection/batch does.
func (s *Server) waPayCollection(ctx context.Context, c *chat, what string) error {
	purpose := ""
	if strings.TrimSpace(what) != "" {
		purpose, _, _ = readPurpose(what)
	}
	r, err := chatRequest(ctx, c)
	if err != nil {
		return err
	}
	b, err := s.collectionBatch(r, "", purpose, "chat")
	var pr payRefused
	if errors.As(err, &pr) {
		return c.say(ctx, s, c.t("wa.pay.not", "reason", pr.msg))
	}
	if err != nil {
		return err
	}
	return s.waBatchPrepared(ctx, c, b, nil, "list")
}

// listLine is a pasted line not read, with why.
type listLine struct {
	line          int
	text, problem string
}

// waPayPasted reads a pasted list a line at a time and prepares the lines
// read as a batch (kind list), as the web's Paste a list and
// POST /v1/payment-batches do.
func (s *Server) waPayPasted(ctx context.Context, c *chat, what string, lines []string) error {
	if len(lines) > maxBatchLen {
		return c.say(ctx, s, c.t("wa.pay.not", "reason", fmt.Sprintf("Up to %d people a batch.", maxBatchLen)))
	}
	purpose, purposeNote := "", ""
	if strings.TrimSpace(what) != "" {
		purpose, purposeNote, _ = readPurpose(what)
	}
	// A line naming a saved account ("@bayo 45k") is read here; the rest
	// as the web reads a pasted list.
	type entry struct {
		row     *batchRow
		problem string
	}
	entries := make([]entry, len(lines))
	var accountLines []int
	for i, l := range lines {
		nm := payNameLine.FindStringSubmatch(l)
		if nm == nil || payDigitRun.MatchString(nm[2]) && len(digitsOf(nm[2])) >= 10 {
			accountLines = append(accountLines, i)
			continue
		}
		name, err := saved.Name(nm[1])
		if err != nil {
			entries[i].problem = err.Error()
			continue
		}
		rest, rowPurpose, rowNote := nm[2], purpose, purposeNote
		if f := payLineFor.FindStringSubmatch(rest); f != nil {
			rest = f[1]
			rowPurpose, rowNote, _ = readPurpose(f[2])
		}
		amount := readPayAmount(rest, "")
		if amount == 0 {
			if f := strings.Fields(rest); len(f) > 0 {
				if k, ok := parseKobo(f[0]); ok {
					amount = k
				}
			}
		}
		if amount == 0 {
			entries[i].problem = "How much? No amount on this line (like 45k)."
			continue
		}
		entries[i].row = &batchRow{payIn{Payee: name, AmountKobo: amount, Purpose: rowPurpose, PurposeNote: rowNote}}
	}
	if len(accountLines) > 0 {
		pasted := make([]string, len(accountLines))
		for j, i := range accountLines {
			pasted[j] = lines[i]
		}
		read, err := s.readBatchLines(ctx, c.p, strings.Join(pasted, "\n"))
		var pr payRefused
		if errors.As(err, &pr) {
			return c.say(ctx, s, c.t("wa.pay.not", "reason", pr.msg))
		}
		if err != nil {
			return err
		}
		for j, bl := range read {
			i := accountLines[j]
			switch {
			case bl.Problem != "":
				entries[i].problem = bl.Problem
			case bl.Payee != "":
				// Saved already: paid as the saved account, by its name
				// (the only way staff pay).
				entries[i].row = &batchRow{payIn{Payee: bl.Payee, AmountKobo: bl.AmountKobo, Purpose: purpose, PurposeNote: purposeNote}}
			default:
				entries[i].row = &batchRow{payIn{AccountNumber: bl.AccountNumber, BankName: bl.BankName, AmountKobo: bl.AmountKobo, Purpose: purpose, PurposeNote: purposeNote}}
			}
		}
	}
	var rows []batchRow
	var unread []listLine
	for i, e := range entries {
		if e.row != nil {
			rows = append(rows, *e.row)
		} else {
			unread = append(unread, listLine{line: i + 1, text: lines[i], problem: e.problem})
		}
	}
	if len(rows) == 0 {
		out := []string{c.t("wa.pay.list.none")}
		for _, u := range unread {
			out = append(out, c.t("wa.pay.list.unread", "line", strconv.Itoa(u.line), "text", u.text, "problem", u.problem))
		}
		return c.say(ctx, s, strings.Join(out, "\n"))
	}
	r, err := chatRequest(ctx, c)
	if err != nil {
		return err
	}
	b, _, err := s.prepareBatchView(r, rows, "From the chat", "list", nil, "chat")
	if err != nil {
		return err
	}
	return s.waBatchPrepared(ctx, c, b, unread, "pasted")
}

// waBatchPrepared answers a batch prepared from the chat: how many people
// and how much, where to check and pay it, then each row held out and
// each line not read, with why. Nothing is sent on words.
func (s *Server) waBatchPrepared(ctx context.Context, c *chat, b batchView, unread []listLine, from string) error {
	if err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{c.tenant.ID}, func(tx pgx.Tx) error {
		return waAudit(ctx, tx, c.ch.Name(), c.p, "payment.batch_chat", b.ID.String(),
			map[string]any{"from": from, "rows": b.Count, "held": len(b.Held), "unread": len(unread), "total_kobo": b.TotalKobo, "status": b.Status})
	}); err != nil {
		return err
	}
	var out []string
	if b.Status == "cancelled" || b.Count == 0 {
		out = append(out, c.t("wa.pay.list.none"))
	} else {
		people := c.t("wa.pay.list.people", "count", strconv.Itoa(b.Count))
		if b.Count == 1 {
			people = c.t("wa.pay.list.person")
		}
		if s.PublicURL != "" {
			out = append(out, c.t("wa.pay.list.ready_link", "people", people, "amount", invoicing.Naira(b.TotalKobo),
				"link", strings.TrimRight(s.PublicURL, "/")+"/payments/batches/"+b.ID.String()))
		} else {
			out = append(out, c.t("wa.pay.list.ready_app", "people", people, "amount", invoicing.Naira(b.TotalKobo)))
		}
	}
	for _, h := range b.Held {
		out = append(out, c.t("wa.pay.list.held", "who", h.Payee, "problem", h.Problem))
	}
	for _, u := range unread {
		out = append(out, c.t("wa.pay.list.unread", "line", strconv.Itoa(u.line), "text", u.text, "problem", u.problem))
	}
	return c.say(ctx, s, strings.Join(out, "\n"))
}

// digitsOf is the digits of s.
func digitsOf(s string) string {
	return strings.Map(func(r rune) rune {
		if r >= '0' && r <= '9' {
			return r
		}
		return -1
	}, s)
}
