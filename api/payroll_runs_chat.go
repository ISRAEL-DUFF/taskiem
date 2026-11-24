package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/israel-duff/taskiem/connectors/payrolla"
	"github.com/israel-duff/taskiem/engine/connector"
	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/ingest"
	"github.com/israel-duff/taskiem/engine/invoicing"
	"github.com/israel-duff/taskiem/engine/lang"
	"github.com/israel-duff/taskiem/engine/whatsapp"
)

// This month's payroll in the chat (P4b-3 of docs/spec/chat-pay-people.md,
// "Confirming with Payrolla's code" and "WhatsApp, Telegram and Slack"):
//
//   - the run's card: in the Taskiem chat on the web and in the app a card
//     drawn live from Payrolla (its history keeps the run's id and period
//     only), with Approve, Reject and Open; on WhatsApp and Telegram the
//     same in words, with Approve and Reject buttons; on Slack a link to
//     the web, since a Slack thread is shared and a code is never typed
//     there;
//   - "payroll" (or "this month's payroll") shows it; "prepare december
//     payroll"; "approve payroll", "lock payroll" (or "lock and pay"),
//     "reject payroll, musa's bonus is wrong";
//   - Approve or Lock and pay on WhatsApp or Telegram asks Payrolla for a
//     code, and while one waits for that person in that thread (at most 10
//     minutes) a message of six digits, spaces allowed, is taken as the
//     code: sent to Payrolla once, never kept, logged or given to the
//     model; the conversation's history says "Code entered". Anything else
//     is answered as usual and the code keeps waiting; "stop" drops it.
//
// The approval card reaches those who may approve when Payrolla works a
// run out (Taskiem's own subscription, PayrollaHeard), on the web
// conversation and their first chat app; whoever prepared a run is told
// of a rejection with its note; a decision made in Payrolla is said with
// who made it; a locked run's payment date is told.

// payrollPrefix starts the payroll card's buttons: pr:a:<run> asks for a
// code to approve, pr:l:<run> to lock, pr:r:<run> says how to reject.
const payrollPrefix = "pr:"

var (
	payrollShowWords    = regexp.MustCompile(`(?i)^\s*(?:this\s+month'?s\s+|show\s+(?:me\s+)?(?:the\s+)?)?payroll\s*[.?!]?\s*$`)
	payrollApproveWords = regexp.MustCompile(`(?i)^\s*approve\s+(?:the\s+|this\s+month'?s\s+)?payroll\s*[.!]?\s*$`)
	payrollLockWords    = regexp.MustCompile(`(?i)^\s*lock\s+(?:the\s+)?payroll(?:\s+and\s+pay)?\s*[.!]?\s*$`)
	payrollRejectWords  = regexp.MustCompile(`(?i)^\s*reject\s+(?:the\s+)?payroll\b\s*[,:;.-]?\s*(.*?)\s*$`)
	payrollPrepareWords = regexp.MustCompile(`(?i)^\s*prepare\s+(?:the\s+)?(?:([a-z]+)(?:\s+(\d{4}))?\s+)?payroll\s*[.!]?\s*$`)
	payrollCodeWords    = regexp.MustCompile(`^\s*(?:\d\s*){6}$`)
)

// payrollWait is a code waiting in a thread.
type payrollWait struct {
	Challenge, Action, Run, Period string
	Expires                        time.Time
}

// codeWaitTTL is the longest a code waits in a thread.
const codeWaitTTL = 10 * time.Minute

func (s *Server) payrollWaitOf(ctx context.Context, c *chat) (*payrollWait, error) {
	var w payrollWait
	err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{c.tenant.ID}, func(tx pgx.Tx) error {
		// One past its time is let go here.
		if _, err := tx.Exec(ctx, `DELETE FROM payroll_code_waits WHERE tenant_id = $1 AND user_id = $2 AND channel = $3 AND expires_at <= now()`,
			c.tenant.ID, c.p.UserID, c.ch.Name()); err != nil {
			return err
		}
		return tx.QueryRow(ctx, `SELECT challenge_id, action, run_id, period, expires_at FROM payroll_code_waits WHERE tenant_id = $1 AND user_id = $2 AND channel = $3`,
			c.tenant.ID, c.p.UserID, c.ch.Name()).Scan(&w.Challenge, &w.Action, &w.Run, &w.Period, &w.Expires)
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &w, nil
}

func (s *Server) setPayrollWait(ctx context.Context, c *chat, w payrollWait) error {
	return db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{c.tenant.ID}, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO payroll_code_waits (tenant_id, user_id, channel, challenge_id, action, run_id, period, expires_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
			ON CONFLICT (tenant_id, user_id, channel) DO UPDATE SET challenge_id = EXCLUDED.challenge_id, action = EXCLUDED.action, run_id = EXCLUDED.run_id,
				period = EXCLUDED.period, expires_at = EXCLUDED.expires_at, created_at = now()`,
			c.tenant.ID, c.p.UserID, c.ch.Name(), w.Challenge, w.Action, w.Run, w.Period, w.Expires)
		return err
	})
}

func (s *Server) dropPayrollWait(ctx context.Context, c *chat) error {
	return db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{c.tenant.ID}, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `DELETE FROM payroll_code_waits WHERE tenant_id = $1 AND user_id = $2 AND channel = $3`, c.tenant.ID, c.p.UserID, c.ch.Name())
		return err
	})
}

// codeThread is whether a code may be typed into this conversation:
// WhatsApp and Telegram (Slack's threads are shared; the web and the app
// have the code sheet).
func codeThread(c *chat) bool { return c.ch.Name() == "whatsapp" || c.ch.Name() == "telegram" }

// waPayroll answers the payroll's words, its card's buttons and a code
// typed while one waits; false when the message is none of them (or the
// person may not see the payroll), so it passes on.
func (s *Server) waPayroll(ctx context.Context, c *chat, text, cmd string) (bool, error) {
	if c.p == nil || c.p.UserID == uuid.Nil || c.tenant.ID == uuid.Nil || c.ch == nil {
		return false, nil
	}
	if codeThread(c) && c.in.Reply == "" {
		six := payrollCodeWords.MatchString(text)
		if six || cmd == "stop" || cmd == "cancel" {
			w, err := s.payrollWaitOf(ctx, c)
			if err != nil || w == nil {
				return false, err
			}
			if !six {
				if err := s.dropPayrollWait(ctx, c); err != nil {
					return true, err
				}
				return true, c.say(ctx, s, c.t("payroll.code.stopped"))
			}
			return true, s.payrollCodeTyped(ctx, c, w, text)
		}
	}
	if rest, ok := strings.CutPrefix(c.in.Reply, payrollPrefix); ok {
		what, run, _ := strings.Cut(rest, ":")
		if _, err := uuid.Parse(run); err != nil {
			return true, c.say(ctx, s, c.t("payroll.none"))
		}
		switch what {
		case "a":
			return true, s.payrollAskCode(ctx, c, run, payrolla.ActionApprove)
		case "l":
			return true, s.payrollAskCode(ctx, c, run, payrolla.ActionLock)
		case "r":
			return true, c.say(ctx, s, c.t("payroll.reject.how"))
		}
		return true, c.say(ctx, s, c.t("payroll.none"))
	}
	if c.state != stateIdle || c.in.Reply != "" || !c.p.Can(PermPayrollSee) {
		return false, nil
	}
	switch {
	case payrollShowWords.MatchString(text):
		return true, s.payrollShow(ctx, c)
	case payrollApproveWords.MatchString(text):
		return true, s.payrollPick(ctx, c, "COMPUTED", payrolla.ActionApprove)
	case payrollLockWords.MatchString(text):
		return true, s.payrollPick(ctx, c, "APPROVED", payrolla.ActionLock)
	}
	if m := payrollRejectWords.FindStringSubmatch(text); m != nil {
		return true, s.payrollReject(ctx, c, m[1])
	}
	if m := payrollPrepareWords.FindStringSubmatch(text); m != nil {
		return true, s.payrollPrepare(ctx, c, m[1], m[2])
	}
	return false, nil
}

// payrollClientFor is the chat person's Payrolla client, or the words that
// say why there is none (said already).
func (s *Server) payrollClientFor(ctx context.Context, c *chat) (*payrolla.Client, bool, error) {
	cl, conn, err := s.payrollaClient(ctx, c.p, "payroll.chat")
	if err != nil {
		var pr payRefused
		if e := s.payrollaSays(ctx, c.p, conn, err); errors.As(e, &pr) {
			return nil, false, c.say(ctx, s, pr.msg)
		}
		return nil, false, err
	}
	return cl, true, nil
}

// sayRefused says a run's refusal in the chat; false when err is not one.
func (s *Server) sayRefused(ctx context.Context, c *chat, err error, buttons ...whatsapp.Button) (bool, error) {
	var rr runRefused
	var pr payRefused
	switch {
	case errors.As(err, &rr):
		return true, c.say(ctx, s, rr.msg, buttons...)
	case errors.As(err, &pr):
		return true, c.say(ctx, s, pr.msg, buttons...)
	}
	return false, err
}

// payrollShow shows the latest payroll's card.
func (s *Server) payrollShow(ctx context.Context, c *chat) error {
	cl, ok, err := s.payrollClientFor(ctx, c)
	if !ok {
		return err
	}
	runs, err := cl.Runs(ctx, "", 10)
	if err != nil {
		_, err = s.sayRefused(ctx, c, s.runSays(ctx, c.lang.Tag, c.p, nil, cl, "", err))
		return err
	}
	for _, r := range runs {
		if r.Status != "CANCELLED" {
			return s.sayPayrollCard(ctx, c, cl, r)
		}
	}
	return c.say(ctx, s, c.t("payroll.no_run", "month", strings.ToLower(monthNames[time.Now().In(lagos()).Month()-1])))
}

// payrollPick finds the newest run at a step and asks for the action's
// code (or, on the web, shows its card, whose sheet takes the code).
func (s *Server) payrollPick(ctx context.Context, c *chat, status, action string) error {
	if !c.p.Can(PermPayrollApprove) {
		return c.say(ctx, s, c.t("payroll.not_allowed"))
	}
	cl, ok, err := s.payrollClientFor(ctx, c)
	if !ok {
		return err
	}
	runs, err := cl.Runs(ctx, status, 5)
	if err != nil {
		_, err = s.sayRefused(ctx, c, s.runSays(ctx, c.lang.Tag, c.p, nil, cl, "", err))
		return err
	}
	if len(runs) == 0 {
		return c.say(ctx, s, c.t("payroll.none"))
	}
	if c.cards() {
		return s.sayPayrollCard(ctx, c, cl, runs[0])
	}
	return s.payrollAskCode(ctx, c, runs[0].ID, action)
}

// payrollAskCode asks Payrolla to send the person a code for action on
// run, and waits for it in this thread. Slack gets a link to the web; the
// Taskiem chat on the web and in the app has the card's code sheet.
func (s *Server) payrollAskCode(ctx context.Context, c *chat, run, action string) error {
	if !c.p.Can(PermPayrollApprove) {
		return c.say(ctx, s, c.t("payroll.not_allowed"))
	}
	if !codeThread(c) {
		return c.say(ctx, s, c.t("payroll.slack", "url", s.payrollRunURL(run)))
	}
	cl, ok, err := s.payrollClientFor(ctx, c)
	if !ok {
		return err
	}
	r, err := cl.Run(ctx, run)
	if err != nil {
		_, err = s.sayRefused(ctx, c, s.runSays(ctx, c.lang.Tag, c.p, nil, cl, run, err))
		return err
	}
	_, conn, wr, err := s.runWrite(ctx, c.p, "chat-code:"+action+":"+run, uuid.NewString())
	if err != nil {
		return err
	}
	ch, err := cl.StartRunConfirmation(ctx, run, action, wr)
	if err != nil {
		_, err = s.sayRefused(ctx, c, s.runSays(ctx, c.lang.Tag, c.p, conn, cl, run, err))
		return err
	}
	period := periodWords(r.PeriodStart, r.PeriodEnd)
	until := time.Now().Add(codeWaitTTL)
	if t, err := time.Parse(time.RFC3339, ch.ExpiresAt); err == nil && t.Before(until) {
		until = t
	}
	if err := s.setPayrollWait(ctx, c, payrollWait{Challenge: ch.ID, Action: action, Run: run, Period: period, Expires: until}); err != nil {
		return err
	}
	s.payrollChatAudit(ctx, c, "payroll.run_code", run, map[string]any{"action": action, "channel": ch.Channel, "via": c.ch.Name()})
	words := codeWhere(c.lang.Tag, ch) + " "
	if action == payrolla.ActionLock {
		words += c.t("payroll.code.type_lock", "period", period, "date", dayWords(r.PayDate))
	} else {
		words += c.t("payroll.code.type_approve", "period", period)
	}
	if ch.Channel == "SMS" {
		words += "\n\n" + c.t("payroll.code.safer")
	}
	return c.say(ctx, s, words)
}

// payrollCodeTyped sends the code typed in the thread to Payrolla, once.
func (s *Server) payrollCodeTyped(ctx context.Context, c *chat, w *payrollWait, typed string) error {
	// The history keeps that a code was entered, never the code (the
	// message itself is not kept: it is only digits).
	s.keepHistory(ctx, c.tenant.ID, c.p.UserID, c.ch.Name(), true, "text", c.t("payroll.code.entered"), nil)
	code := codeOf(typed)
	d, err := s.decideRunCode(ctx, c.p, w.Run, w.Action, w.Challenge, code, uuid.NewString(), c.lang.Tag)
	if err != nil {
		var rr runRefused
		if errors.As(err, &rr) {
			switch rr.code {
			case "code_invalid":
				return c.say(ctx, s, rr.msg+" "+c.t("payroll.code.again"))
			case "code_expired", "code_locked", "code_used":
				if derr := s.dropPayrollWait(ctx, c); derr != nil {
					return derr
				}
				b := "a"
				if w.Action == payrolla.ActionLock {
					b = "l"
				}
				return c.say(ctx, s, rr.msg, whatsapp.Button{ID: payrollPrefix + b + ":" + w.Run, Title: c.t("payroll.button.new_code")})
			}
		}
		if derr := s.dropPayrollWait(ctx, c); derr != nil {
			return derr
		}
		_, err = s.sayRefused(ctx, c, err)
		return err
	}
	if err := s.dropPayrollWait(ctx, c); err != nil {
		return err
	}
	s.payrollChatAudit(ctx, c, "payroll.run_"+w.Action, w.Run, map[string]any{"confirmed_with_code": true, "via": c.ch.Name()})
	out := decidedOut(c.lang.Tag, w.Action, d)
	words, _ := out["words"].(string)
	if w.Action == payrolla.ActionApprove {
		return c.say(ctx, s, words, whatsapp.Button{ID: payrollPrefix + "l:" + w.Run, Title: c.t("payroll.button.lock")})
	}
	return c.say(ctx, s, words)
}

// payrollReject sends the newest payroll waiting for approval back, with
// the note; with no note, says how.
func (s *Server) payrollReject(ctx context.Context, c *chat, note string) error {
	if !c.p.Can(PermPayrollApprove) {
		return c.say(ctx, s, c.t("payroll.not_allowed"))
	}
	note = strings.TrimSpace(note)
	if note == "" {
		return c.say(ctx, s, c.t("payroll.reject.how"))
	}
	cl, ok, err := s.payrollClientFor(ctx, c)
	if !ok {
		return err
	}
	runs, err := cl.Runs(ctx, "COMPUTED", 5)
	if err != nil || len(runs) == 0 {
		if err != nil {
			_, err = s.sayRefused(ctx, c, s.runSays(ctx, c.lang.Tag, c.p, nil, cl, "", err))
			return err
		}
		return c.say(ctx, s, c.t("payroll.none"))
	}
	if _, err := s.rejectPayroll(ctx, c.p, runs[0].ID, cutRunes(note, 300), uuid.NewString(), c.lang.Tag); err != nil {
		_, err = s.sayRefused(ctx, c, err)
		return err
	}
	s.payrollChatAudit(ctx, c, "payroll.run_reject", runs[0].ID, map[string]any{"via": c.ch.Name()})
	return c.say(ctx, s, c.t("payroll.rejected"))
}

// payrollMonth reads "december", "dec 2026": the next such month from
// now (this one included), as Payrolla's period.
func payrollMonth(now time.Time, name, year string) (string, bool) {
	name = strings.ToLower(strings.TrimSpace(name))
	m := -1
	switch name {
	case "", "this", "this month's", "the":
		m = int(now.Month()) - 1
	case "next":
		m = int(now.Month()) % 12
		if m == 0 {
			now = now.AddDate(1, 0, 0)
		}
	default:
		for i, n := range monthNames {
			if strings.HasPrefix(strings.ToLower(n), name) && len(name) >= 3 {
				m = i
			}
		}
	}
	if m < 0 {
		return "", false
	}
	y := now.Year()
	if year != "" {
		_, _ = fmt.Sscan(year, &y)
	} else if name != "" && name != "this" && m+1 < int(now.Month())-1 {
		// "prepare january payroll" in November is next year's.
		y++
	}
	return fmt.Sprintf("%04d-%02d", y, m+1), true
}

// payrollPrepare prepares a month's payroll from the chat and shows its
// card; a month with one already shows that one's.
func (s *Server) payrollPrepare(ctx context.Context, c *chat, month, year string) error {
	if !c.p.Can(PermPayrollPrepare) {
		return c.say(ctx, s, c.t("payroll.not_allowed"))
	}
	period, ok := payrollMonth(time.Now().In(lagos()), month, year)
	if !ok {
		return c.say(ctx, s, c.t("payroll.prepare.which"))
	}
	cl, ok, err := s.payrollClientFor(ctx, c)
	if !ok {
		return err
	}
	out, err := s.prepare(ctx, c.p, payrolla.Period{Period: period}, "chat:"+period+":"+uuid.NewString(), c.lang.Tag)
	var rr runRefused
	if errors.As(err, &rr) && rr.code == "duplicate_run" {
		if id, _ := rr.extra["run_id"].(string); id != "" {
			if err := c.say(ctx, s, rr.msg); err != nil {
				return err
			}
			if r, rerr := cl.Run(ctx, id); rerr == nil {
				return s.sayPayrollCard(ctx, c, cl, r)
			}
			return nil
		}
	}
	if err != nil {
		_, err = s.sayRefused(ctx, c, err)
		return err
	}
	s.payrollChatAudit(ctx, c, "payroll.run_prepare", out.Run.ID, map[string]any{"period": out.Run.PeriodStart, "via": c.ch.Name()})
	if out.ComputeError != nil {
		return c.say(ctx, s, c.t("payroll.refused.compute_error", "reason", strings.TrimRight(strings.TrimSpace(out.ComputeError.Message), ".")))
	}
	if err := c.say(ctx, s, c.t("payroll.prepared", "period", periodWords(out.Run.PeriodStart, out.Run.PeriodEnd),
		"people", peopleWords(c.lang.Tag, out.Run.EmployeeCount), "amount", invoicing.Naira(out.Run.TotalNetKobo))); err != nil {
		return err
	}
	return s.sayPayrollCard(ctx, c, cl, out.Run)
}

// payrollChatAudit records a chat's change to a run.
func (s *Server) payrollChatAudit(ctx context.Context, c *chat, action, run string, detail map[string]any) {
	r, err := chatRequest(ctx, c)
	if err != nil {
		return
	}
	s.runAudit(r, action, run, detail)
}

func (s *Server) payrollRunURL(run string) string {
	base := strings.TrimRight(s.PublicURL, "/")
	return base + "/payroll/runs/" + run
}

// payrollCardMsg is a run's card as the Taskiem chat's history keeps it:
// Payrolla's id and the period only; the figures are read when shown.
type payrollCardMsg struct {
	RunID  string `json:"run_id"`
	Period string `json:"period"`
}

// sayPayrollCard shows a run's card: a card in the Taskiem chat on the
// web and in the app; elsewhere its words, with its buttons.
func (s *Server) sayPayrollCard(ctx context.Context, c *chat, cl *payrolla.Client, r payrolla.Run) error {
	if c.cards() {
		if c.note != "" {
			note := c.note
			c.note = ""
			if err := s.waSay(ctx, c.ch, c.addr, c.tenant.Name, note); err != nil {
				return err
			}
		}
		b, _ := json.Marshal(payrollCardMsg{RunID: r.ID, Period: periodWords(r.PeriodStart, r.PeriodEnd)})
		id := s.keepHistory(ctx, c.tenant.ID, c.p.UserID, "web", false, "payroll_run", string(b), nil)
		if live := liveOf(ctx); live != nil && live.user == c.p.UserID {
			if id == uuid.Nil {
				id = uuid.Must(uuid.NewV7())
			}
			live.add(TaskiemMessage{ID: id, Channel: "web", Kind: "payroll_run", Text: string(b), CreatedAt: time.Now().UTC()})
		}
		return nil
	}
	text, buttons, link := s.payrollCardWords(ctx, c.lang.Tag, c.p, cl, r, c.ch.Name())
	if c.ch.Name() == "telegram" {
		if c.note != "" {
			text, c.note = c.note+"\n\n"+text, ""
		}
		_, err := c.ch.Send(ctx, c.addr, whatsapp.Message{Text: c.ch.Prefix(c.tenant.Name) + text, Buttons: buttons, Link: &whatsapp.LinkButton{Title: c.t("payroll.button.open"), URL: link}})
		return err
	}
	return c.say(ctx, s, text+"\n\n"+c.t("payroll.card.open", "url", link), buttons...)
}

// payrollCardWords are a run's card in words: its period, people, total
// and pay day, what changed since last month, the wallet's shortfall and
// where it is; with Approve and Reject (or Lock and pay) for those who may
// decide it in a thread that takes codes.
func (s *Server) payrollCardWords(ctx context.Context, l lang.Tag, p *Principal, cl *payrolla.Client, r payrolla.Run, channel string) (string, []whatsapp.Button, string) {
	lines, _ := cl.Lines(ctx, r.ID)
	pic := s.picture(ctx, l, p, cl, r, lines)
	period := periodWords(r.PeriodStart, r.PeriodEnd)
	var b strings.Builder
	b.WriteString(tr(l, "payroll.card.title", "period", period))
	b.WriteString("\n" + tr(l, "payroll.card.line", "people", peopleWords(l, r.EmployeeCount), "amount", invoicing.Naira(r.TotalNetKobo), "date", dayWords(r.PayDate)))
	if len(pic.Changes.New) > 0 {
		b.WriteString("\n" + tr(l, "payroll.card.new", "who", whatsapp.SafeText(strings.Join(pic.Changes.New, ", "))))
	}
	if len(pic.Changes.Left) > 0 {
		b.WriteString("\n" + tr(l, "payroll.card.left", "who", whatsapp.SafeText(strings.Join(pic.Changes.Left, ", "))))
	}
	if len(pic.Changes.Changed) > 0 {
		var who []string
		for _, x := range pic.Changes.Changed {
			who = append(who, x.Name)
		}
		b.WriteString("\n" + tr(l, "payroll.card.changed", "who", whatsapp.SafeText(strings.Join(who, ", "))))
	}
	if pic.Funding != nil && pic.Funding.Words != "" {
		b.WriteString("\n" + pic.Funding.Words)
	}
	var state string
	switch r.Status {
	case "COMPUTED":
		state = tr(l, "payroll.card.waiting")
	case "APPROVED":
		state = tr(l, "payroll.card.approved")
	case "LOCKED", "DISBURSING":
		state = tr(l, "payroll.card.locked", "date", dayWords(r.PayDate))
	case "DRAFT":
		state = tr(l, "payroll.card.draft")
	default:
		state = tr(l, "payroll.card.done", "state", runStateWords(l, r.Status))
	}
	b.WriteString("\n" + state)
	var buttons []whatsapp.Button
	if p.Can(PermPayrollApprove) && (channel == "whatsapp" || channel == "telegram") {
		switch r.Status {
		case "COMPUTED":
			buttons = []whatsapp.Button{{ID: payrollPrefix + "a:" + r.ID, Title: tr(l, "payroll.button.approve")}, {ID: payrollPrefix + "r:" + r.ID, Title: tr(l, "payroll.button.reject")}}
		case "APPROVED":
			buttons = []whatsapp.Button{{ID: payrollPrefix + "l:" + r.ID, Title: tr(l, "payroll.button.lock")}}
		}
	}
	return b.String(), buttons, s.payrollRunURL(r.ID)
}

// --- told when Payrolla says a run changed ---

// outboxPayroll is the outbox kind of a run's card or news.
const outboxPayroll = "payroll_run"

// payrollNews is what an outbox item says about a run: its id and period
// (no figures: they are read from Payrolla when it is sent), the event,
// and a rejection's note and who decided.
type payrollNews struct {
	Run    string `json:"run"`
	Period string `json:"period"`
	Event  string `json:"event"` // card, rejected, approved, rejected_there, locked, scheduled
	Note   string `json:"note,omitempty"`
	By     string `json:"by,omitempty"`
}

// payrollaRunHeard queues what a run's event tells people (Taskiem's own
// subscription; PayrollaHeard): the approval card to those who may
// approve when a run is worked out; a rejection, with its note, to whoever
// prepared it; a decision made in Payrolla, and a locked run's pay date,
// to those who may approve (and whoever prepared it). The other events
// change nothing kept: cards and pages read the run when shown. It reports
// whether the event was a run's.
func payrollaRunHeard(ctx context.Context, pool *pgxpool.Pool, log *slog.Logger, e ingest.HeardEvent) bool {
	ev, ok := payrolla.ReadRunEvent(e.Body)
	if !ok {
		return false
	}
	news := payrollNews{Run: ev.Run.ID, Period: periodWords(ev.Run.PeriodStart, ev.Run.PeriodEnd)}
	approvers, preparer := false, false
	key := ev.Run.ID
	switch {
	case ev.Type == "run.computed" && ev.Run.Status == "COMPUTED":
		news.Event, approvers = "card", true
	case ev.Type == "run.rejected":
		news.Event, news.Note, news.By, preparer = "rejected", cutRunes(strings.TrimSpace(ev.Note), 300), cutRunes(ev.By, 120), true
		key = ev.ID
	case ev.Type == "approval.recorded" && ev.Via == "payrolla":
		news.By, approvers, key = cutRunes(ev.By, 120), true, ev.ID
		switch ev.Action {
		case "approved":
			news.Event = "approved"
		case "rejected":
			news.Event, preparer = "rejected_there", true
		case "locked":
			news.Event = "locked"
		default:
			return true
		}
	case ev.Type == "run.payment_scheduled":
		news.Event, approvers, preparer = "scheduled", true, true
	default:
		return true
	}
	ref, _ := json.Marshal(news)
	if err := db.InTenantTx(ctx, pool, []uuid.UUID{e.Tenant}, func(tx pgx.Tx) error {
		var users []uuid.UUID
		if approvers {
			got, err := holdersTx(ctx, tx, PermPayrollApprove)
			if err != nil {
				return err
			}
			users = append(users, got...)
		}
		if preparer {
			var actor string
			err := tx.QueryRow(ctx, `SELECT actor_id FROM audit_log WHERE tenant_id = $1 AND action = 'payroll.run_prepare' AND target = $2 ORDER BY at DESC LIMIT 1`,
				e.Tenant, "payrolla-run:"+ev.Run.ID).Scan(&actor)
			if err != nil && !errors.Is(err, pgx.ErrNoRows) {
				return err
			}
			if u, perr := uuid.Parse(actor); perr == nil && !slices.Contains(users, u) {
				users = append(users, u)
			}
		}
		for _, u := range users {
			first, err := firstChatTx(ctx, tx, u)
			if err != nil {
				return err
			}
			for _, ch := range []string{"web", first} {
				if ch == "" {
					continue
				}
				r := string(ref)
				it := whatsapp.OutboxItem{Tenant: e.Tenant, Channel: ch, Kind: outboxPayroll, User: u, Reference: &r}
				if _, err := whatsapp.Enqueue(ctx, tx, it, "payroll:"+news.Event+":"+key+":"+u.String()+":"+ch); err != nil {
					return err
				}
			}
		}
		return nil
	}); err != nil {
		log.Warn("payrolla: telling of a run", "tenant", e.Tenant, "event", ev.Type, "err", err)
	}
	return true
}

// sendPayrollItem sends a queued run's card or news, read from Payrolla
// now, if the person may still see the payroll.
func (s *Server) sendPayrollItem(ctx context.Context, it whatsapp.OutboxItem) error {
	var news payrollNews
	if it.Reference == nil || json.Unmarshal([]byte(*it.Reference), &news) != nil || news.Run == "" {
		return fmt.Errorf("%w: unreadable", whatsapp.ErrNothingToSend)
	}
	p, err := s.principalOf(ctx, it.Tenant, it.User)
	if err != nil || !p.Can(PermPayrollSee) {
		return fmt.Errorf("%w: no longer sees the payroll", whatsapp.ErrNothingToSend)
	}
	ch, addr, err := s.chatAddrOf(ctx, it.Channel, it.Tenant, it.User)
	if err != nil {
		return err
	}
	l := s.chooseLang(ctx, it.Tenant, addr, "").Tag
	var business string
	if err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{it.Tenant}, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT name FROM tenants WHERE id = $1`, it.Tenant).Scan(&business)
	}); err != nil {
		return err
	}
	cl, _, err := s.payrollaClient(ctx, p, "payroll.tell")
	if err != nil {
		return fmt.Errorf("%w: payroll is not connected", whatsapp.ErrNothingToSend)
	}
	r, err := cl.Run(ctx, news.Run)
	if errors.Is(err, connector.ErrNotFound) {
		return fmt.Errorf("%w: the run is gone", whatsapp.ErrNothingToSend)
	}
	if err != nil {
		return err
	}
	period := periodWords(r.PeriodStart, r.PeriodEnd)
	var title, body string
	var buttons []whatsapp.Button
	switch news.Event {
	case "card":
		if r.Status != "COMPUTED" {
			return fmt.Errorf("%w: decided already", whatsapp.ErrNothingToSend)
		}
		if it.Channel == "web" {
			b, _ := json.Marshal(payrollCardMsg{RunID: r.ID, Period: period})
			if s.keepHistory(ctx, it.Tenant, it.User, "web", false, "payroll_run", string(b), nil) == uuid.Nil {
				return fmt.Errorf("%w: not kept", whatsapp.ErrNothingToSend)
			}
			return nil
		}
		title, buttons, _ = s.payrollCardWords(ctx, l, p, cl, r, it.Channel)
		title, body, _ = strings.Cut(title, "\n")
		title = strings.Trim(title, "*")
	case "rejected":
		title = tr(l, "payroll.told.rejected", "by", news.By, "period", period, "note", news.Note)
	case "approved":
		title = tr(l, "payroll.told.approved", "period", period, "by", news.By)
	case "rejected_there":
		title = tr(l, "payroll.told.rejected_there", "period", period, "by", news.By)
	case "locked":
		title = tr(l, "payroll.told.locked", "period", period, "by", news.By)
	case "scheduled":
		title = tr(l, "payroll.told.scheduled", "period", period, "people", peopleWords(l, r.EmployeeCount), "amount", invoicing.Naira(r.TotalNetKobo), "date", dayWords(r.PayDate))
	default:
		return fmt.Errorf("%w: unknown news %q", whatsapp.ErrNothingToSend, news.Event)
	}
	link := s.payrollRunURL(r.ID)
	if it.Channel == "web" {
		_, err := ch.Send(ctx, addr, whatsapp.Message{Text: strings.TrimSpace(title + "\n" + body), Tenant: it.Tenant})
		return err
	}
	m := whatsapp.AlertMessage(business, "payroll", title, body, link, nil)
	m.Text = strings.TrimRight(m.Text, "\n")
	m.Tenant, m.Buttons = it.Tenant, buttons
	if ch.OwnTenant() != uuid.Nil {
		m.Text = strings.TrimPrefix(m.Text, "["+business+"] ")
	}
	if _, err := ch.Send(ctx, addr, m); err != nil {
		return fmt.Errorf("to %s: %w", whatsapp.MaskNumber(addr), err)
	}
	return nil
}
