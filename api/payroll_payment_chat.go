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
	"unicode"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/israel-duff/taskiem/connectors/payrolla"
	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/ingest"
	"github.com/israel-duff/taskiem/engine/invoicing"
	"github.com/israel-duff/taskiem/engine/lang"
	"github.com/israel-duff/taskiem/engine/whatsapp"
)

// After locking, in the chat and told (P4b-4 of docs/spec/chat-pay-people.md):
//
//   - "pay payroll now" and "pay the failed salaries again" take
//     Payrolla's code in the WhatsApp or Telegram thread (P4b-3's
//     code-in-the-thread, actions pay_now and retry_failed); on the web and
//     in the app they show the payroll's card, whose sheet takes the code;
//   - "my payslip" and "my payslips" give a linked employee their own:
//     the card in the Taskiem chat on the web and in the app, a link on
//     WhatsApp, Telegram and Slack that opens after they sign in;
//   - told, from the outbox, on the web conversation and the person's
//     first chat app, its figures read from Payrolla when sent: the
//     shortfall on the pay date (run.awaiting_funds, once a run), the
//     morning warnings three days and one day before the pay date, who
//     was paid and who was not and why (run.completed,
//     run.partially_completed) with Pay the failed ones again, to those
//     who may approve and whoever prepared the run; and to each linked
//     employee only "Your payslip for November is ready"
//     (payslip.issued). No message carries one person's pay.

var (
	payrollPayNowWords = regexp.MustCompile(`(?i)^\s*pay\s+(?:the\s+)?(?:payroll|salaries)\s+now\s*[.!]?\s*$`)
	payrollRetryWords  = regexp.MustCompile(`(?i)^\s*pay\s+(?:the\s+)?failed\s+(?:ones|salaries|salary|payments|pay)\s+again\s*[.!]?\s*$`)
	payslipWords       = regexp.MustCompile(`(?i)^\s*(?:show\s+(?:me\s+)?)?my\s+pay\s?slip(s)?\s*[.?!]?\s*$`)
)

// payrollPayPick finds the newest payroll to pay now (locked, not paid
// yet) or to pay again (some not paid), and asks for the code; on the
// web, shows its card.
func (s *Server) payrollPayPick(ctx context.Context, c *chat, action string) error {
	if !c.p.Can(PermPayrollApprove) {
		return c.say(ctx, s, c.t("payroll.not_allowed"))
	}
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
		want := r.Status == "LOCKED"
		if action == payrolla.ActionRetryFailed {
			want = r.Status == "PARTIALLY_COMPLETED"
		}
		if !want {
			continue
		}
		if c.cards() {
			return s.sayPayrollCard(ctx, c, cl, r)
		}
		return s.payrollAskCode(ctx, c, r.ID, action)
	}
	if action == payrolla.ActionRetryFailed {
		return c.say(ctx, s, c.t("payroll.refused.nothing_to_retry"))
	}
	return c.say(ctx, s, c.t("payroll.pay.none_now"))
}

func (s *Server) myPayslipURL(id string) string {
	return strings.TrimRight(s.PublicURL, "/") + "/payroll/me/payslips/" + id
}

// myPayslipChat answers "my payslip" (the newest) and "my payslips" (the
// last six): cards in the Taskiem chat, links elsewhere.
func (s *Server) myPayslipChat(ctx context.Context, c *chat, all bool) error {
	emp, err := s.myEmployee(ctx, c.p)
	if err != nil {
		return err
	}
	if emp == "" {
		return c.say(ctx, s, c.t("payroll.payslip.not_linked"))
	}
	cl, ok, err := s.payrollClientFor(ctx, c)
	if !ok {
		return err
	}
	list, err := cl.EmployeePayslips(ctx, emp)
	if err != nil {
		_, err = s.sayRefused(ctx, c, s.runSays(ctx, c.lang.Tag, c.p, nil, cl, "", err))
		return err
	}
	if len(list) == 0 {
		return c.say(ctx, s, c.t("payroll.payslip.none"))
	}
	rows := myPayslipRows(c.lang.Tag, list)
	n := 1
	if all {
		n = min(6, len(rows))
	}
	rows = rows[:n]
	if c.cards() {
		if c.note != "" {
			note := c.note
			c.note = ""
			if err := s.waSay(ctx, c.ch, c.addr, c.tenant.Name, note); err != nil {
				return err
			}
		}
		for _, x := range rows {
			text := payslipCardText(x.ID, x.Period)
			id := s.keepHistory(ctx, c.tenant.ID, c.p.UserID, "web", false, "payslip", text, nil)
			if live := liveOf(ctx); live != nil && live.user == c.p.UserID {
				if id == uuid.Nil {
					id = uuid.Must(uuid.NewV7())
				}
				live.add(TaskiemMessage{ID: id, Channel: "web", Kind: "payslip", Text: text, CreatedAt: time.Now().UTC()})
			}
		}
		return nil
	}
	var b strings.Builder
	b.WriteString(c.t("payroll.payslip.links"))
	for _, x := range rows {
		b.WriteString("\n" + c.t("payroll.payslip.link_line", "period", x.Period, "url", s.myPayslipURL(x.ID)))
	}
	return c.say(ctx, s, b.String())
}

// --- told ---

// payrollPaymentHeard queues what a payment's event tells people
// (Taskiem's own subscription, PayrollaHeard). It reports whether the
// event was a payment's.
func payrollaPaymentHeard(ctx context.Context, pool *pgxpool.Pool, log *slog.Logger, e ingest.HeardEvent) bool {
	ev, ok := payrolla.ReadPaymentEvent(e.Body)
	if !ok {
		return false
	}
	news := payrollNews{Run: ev.RunID, Period: periodWords(ev.Run.PeriodStart, ev.Run.PeriodEnd)}
	var key string
	switch ev.Type {
	case "run.awaiting_funds":
		// Once a run: Payrolla may say it again while it waits.
		news.Event, news.ShortKobo, key = "short", ev.ShortKobo, "short:"+ev.RunID
	case "run.completed", "run.partially_completed":
		news.Event, key = "paid", "paid:"+ev.ID
	case "payslip.issued":
		news.Event, news.Payslip, news.Employee, news.Period = "payslip", ev.Payslip, ev.Employee, cutRunes(ev.Period, 40)
		key = "payslip:" + ev.Payslip
	default:
		// run.disbursement_started and payout.*: the Payment and the card
		// read Payrolla when shown.
		return true
	}
	if err := db.InTenantTx(ctx, pool, []uuid.UUID{e.Tenant}, func(tx pgx.Tx) error {
		var users []uuid.UUID
		var err error
		if news.Event == "payslip" {
			u, err := linkedStaffTx(ctx, tx, e.Tenant, ev.Employee)
			if err != nil || u == uuid.Nil {
				return err
			}
			users = []uuid.UUID{u}
		} else if users, err = payrollTellWhoTx(ctx, tx, e.Tenant, ev.RunID); err != nil {
			return err
		}
		return queuePayrollNewsTx(ctx, tx, e.Tenant, users, news, key)
	}); err != nil {
		log.Warn("payrolla: telling of a payment", "tenant", e.Tenant, "event", ev.Type, "err", err)
	}
	return true
}

// payrollTellWhoTx is who hears of a run's payment: those who may
// approve, and whoever prepared it in Taskiem.
func payrollTellWhoTx(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, run string) ([]uuid.UUID, error) {
	users, err := holdersTx(ctx, tx, PermPayrollApprove)
	if err != nil {
		return nil, err
	}
	var actor string
	err = tx.QueryRow(ctx, `SELECT actor_id FROM audit_log WHERE tenant_id = $1 AND action = 'payroll.run_prepare' AND target = $2 ORDER BY at DESC LIMIT 1`,
		tenant, "payrolla-run:"+run).Scan(&actor)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return nil, err
	}
	if u, perr := uuid.Parse(actor); perr == nil && !slices.Contains(users, u) {
		users = append(users, u)
	}
	return users, nil
}

// queuePayrollNewsTx queues news to each of users on the web conversation
// and their first chat app, once per key.
func queuePayrollNewsTx(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, users []uuid.UUID, news payrollNews, key string) error {
	ref, _ := json.Marshal(news)
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
			it := whatsapp.OutboxItem{Tenant: tenant, Channel: ch, Kind: outboxPayroll, User: u, Reference: &r}
			if _, err := whatsapp.Enqueue(ctx, tx, it, "payroll:"+key+":"+u.String()+":"+ch); err != nil {
				return err
			}
		}
	}
	return nil
}

// lowerFirst is "account closed" from Payrolla's "Account closed".
func lowerFirst(s string) string {
	s = strings.TrimRight(strings.TrimSpace(s), ".")
	r := []rune(s)
	if len(r) > 1 && unicode.IsUpper(r[0]) && !unicode.IsUpper(r[1]) {
		r[0] = unicode.ToLower(r[0])
	}
	return string(r)
}

// paymentNewsWords are a payment's news in words, read from Payrolla now;
// ok is false when it is no longer so (the money came, the wallet was
// topped up).
func (s *Server) paymentNewsWords(ctx context.Context, l lang.Tag, p *Principal, cl *payrolla.Client, r payrolla.Run, news payrollNews, channel string) (string, []whatsapp.Button, bool) {
	period := periodWords(r.PeriodStart, r.PeriodEnd)
	switch news.Event {
	case "short":
		pay, err := cl.RunPayment(ctx, r.ID)
		if err != nil || pay.Status != "AWAITING_FUNDS" {
			return "", nil, false
		}
		short := s.shortBy(ctx, cl, r.ID)
		if short <= 0 {
			short = news.ShortKobo
		}
		return tr(l, "payroll.told.short", "period", period, "short", invoicing.Naira(short)), nil, true
	case "warn3", "warn1":
		f, err := cl.Funding(ctx)
		if err != nil || r.Status != "APPROVED" && r.Status != "LOCKED" || f.HasWallet && f.AvailableKobo >= r.TotalNetKobo {
			return "", nil, false
		}
		return tr(l, "payroll.told."+news.Event, "period", period, "date", dayWords(r.PayDate), "available", invoicing.Naira(f.AvailableKobo),
			"needed", invoicing.Naira(r.TotalNetKobo), "short", invoicing.Naira(r.TotalNetKobo-f.AvailableKobo)), nil, true
	case "paid":
		lines, err := cl.Lines(ctx, r.ID)
		if err != nil {
			return "", nil, false
		}
		var who []string
		failed := 0
		for _, ln := range lines {
			st := lineState(ln.DisbursementStatus)
			if st != "failed" && st != "returned" {
				continue
			}
			if st == "failed" {
				failed++
			}
			n := runLineName(ln)
			if reason := lowerFirst(ln.Reason); reason != "" {
				n += " (" + reason + ")"
			}
			who = append(who, n)
		}
		total := len(lines)
		if len(who) == 0 {
			return tr(l, "payroll.told.paid_all", "period", period, "people", peopleWords(l, int64(total))), nil, true
		}
		var buttons []whatsapp.Button
		if failed > 0 && p.Can(PermPayrollApprove) && (channel == "whatsapp" || channel == "telegram") {
			buttons = []whatsapp.Button{{ID: payrollPrefix + "f:" + r.ID, Title: tr(l, "payroll.button.retry")}}
		}
		return tr(l, "payroll.told.paid_some", "period", period, "paid", fmt.Sprint(total-len(who)), "total", fmt.Sprint(total), "n", fmt.Sprint(len(who)),
			"who", whatsapp.SafeText(strings.Join(who, ", "))), buttons, true
	}
	return "", nil, false
}

// paymentCardWords is the payment's line on a run's card in words, past
// locking and not merely scheduled, with Pay the failed ones again.
func (s *Server) paymentCardWords(ctx context.Context, l lang.Tag, p *Principal, cl *payrolla.Client, r payrolla.Run, channel string) (string, []whatsapp.Button) {
	if !lockedStatus(r.Status) {
		return "", nil
	}
	pay, err := cl.RunPayment(ctx, r.ID)
	if err != nil || pay.Status == "SCHEDULED" {
		return "", nil
	}
	var short int64
	if pay.Status == "AWAITING_FUNDS" {
		short = s.shortBy(ctx, cl, r.ID)
	}
	words, _ := paymentWords(l, pay, r.PayDate, short)
	var buttons []whatsapp.Button
	if pay.Failed > 0 && pay.Status != "PAYING" && p.Can(PermPayrollApprove) && (channel == "whatsapp" || channel == "telegram") {
		buttons = []whatsapp.Button{{ID: payrollPrefix + "f:" + r.ID, Title: tr(l, "payroll.button.retry")}}
	}
	return words, buttons
}

// sendPayslipItem tells a linked employee their payslip is ready: the
// card in their Taskiem conversation; on a chat app only the words and a
// link that opens after they sign in. Never the figures.
func (s *Server) sendPayslipItem(ctx context.Context, it whatsapp.OutboxItem, news payrollNews) error {
	if news.Payslip == "" || news.Employee == "" {
		return fmt.Errorf("%w: unreadable", whatsapp.ErrNothingToSend)
	}
	var still uuid.UUID
	if err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{it.Tenant}, func(tx pgx.Tx) error {
		var err error
		still, err = linkedStaffTx(ctx, tx, it.Tenant, news.Employee)
		return err
	}); err != nil {
		return err
	}
	if still != it.User {
		return fmt.Errorf("%w: no longer that employee", whatsapp.ErrNothingToSend)
	}
	if it.Channel == "web" {
		if s.keepHistory(ctx, it.Tenant, it.User, "web", false, "payslip", payslipCardText(news.Payslip, news.Period), nil) == uuid.Nil {
			return fmt.Errorf("%w: not kept", whatsapp.ErrNothingToSend)
		}
		return nil
	}
	ch, addr, err := s.chatAddrOf(ctx, it.Channel, it.Tenant, it.User)
	if err != nil {
		return err
	}
	l := s.chooseLang(ctx, it.Tenant, addr, "").Tag
	month := news.Period
	if f := strings.Fields(month); len(f) > 0 {
		month = f[0]
	}
	business := s.businessName(ctx, it.Tenant)
	m := whatsapp.AlertMessage(business, "payroll", tr(l, "payroll.payslip.ready", "month", month), tr(l, "payroll.payslip.open"), s.myPayslipURL(news.Payslip), nil)
	m.Text = strings.TrimRight(m.Text, "\n")
	m.Tenant = it.Tenant
	if ch.OwnTenant() != uuid.Nil {
		m.Text = strings.TrimPrefix(m.Text, "["+business+"] ")
	}
	if _, err := ch.Send(ctx, addr, m); err != nil {
		return fmt.Errorf("to %s: %w", whatsapp.MaskNumber(addr), err)
	}
	return nil
}

// --- the morning check ---

// runPayrollFunds runs PayrollFundsCheck hourly: it warns in the morning,
// once a run and warning.
func (s *Server) runPayrollFunds(ctx context.Context) {
	every(ctx, time.Hour, func() {
		if _, err := s.PayrollFundsCheck(ctx, time.Now()); err != nil && ctx.Err() == nil {
			s.Logger.Error("payroll: the morning funds check", "err", err)
		}
	})
}

// PayrollFundsCheck is Taskiem's morning check of Payrolla's wallet: from
// 7am in Lagos, an approved or locked payroll paying in three days or in
// one day that the wallet does not cover is told, once each, to those who
// may approve and whoever prepared it (GET /connect/funding against its
// total). It replaces the payrolla-payday-check template's comparison
// with another connection's balance for businesses on this connection.
// It answers how many warnings it queued.
func (s *Server) PayrollFundsCheck(ctx context.Context, now time.Time) (int, error) {
	now = now.In(lagos())
	if now.Hour() < 7 {
		return 0, nil
	}
	rows, err := s.Store.Pool.Query(ctx, `SELECT DISTINCT tenant_id FROM taskiem_money_connections() WHERE connector = $1`, payrollaID)
	if err != nil {
		return 0, err
	}
	tenants, err := pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
	if err != nil {
		return 0, err
	}
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, lagos())
	queued := 0
	for _, t := range tenants {
		n, err := s.payrollFundsFor(ctx, t, today)
		if err != nil {
			if ctx.Err() != nil {
				return queued, nil //nolint:nilerr // stopping
			}
			s.Logger.Warn("payroll: the morning funds check", "tenant", t, "err", err)
		}
		queued += n
	}
	return queued, nil
}

func (s *Server) payrollFundsFor(ctx context.Context, tenant uuid.UUID, today time.Time) (int, error) {
	cl, _, err := s.payrollaClientAs(ctx, tenant, "platform", "payroll.funds_check")
	if errors.Is(err, errPayrollaNotConnected) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	runs, err := cl.Runs(ctx, "APPROVED,LOCKED", 20)
	if err != nil {
		return 0, err
	}
	var funding *payrolla.Funding
	queued := 0
	for _, r := range runs {
		if r.Status != "APPROVED" && r.Status != "LOCKED" {
			continue
		}
		pay, err := time.ParseInLocation("2006-01-02", r.PayDate, lagos())
		if err != nil {
			continue
		}
		days := int(pay.Sub(today).Hours()/24 + 0.5)
		if days != 3 && days != 1 {
			continue
		}
		if r.Status == "LOCKED" {
			if p, err := cl.RunPayment(ctx, r.ID); err != nil || p.Status != "SCHEDULED" {
				continue
			}
		}
		if funding == nil {
			f, err := cl.Funding(ctx)
			if err != nil {
				return queued, err
			}
			funding = &f
		}
		if funding.HasWallet && funding.AvailableKobo >= r.TotalNetKobo {
			continue
		}
		event := fmt.Sprintf("warn%d", days)
		news := payrollNews{Run: r.ID, Period: periodWords(r.PeriodStart, r.PeriodEnd), Event: event, ShortKobo: r.TotalNetKobo - funding.AvailableKobo}
		if err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
			users, err := payrollTellWhoTx(ctx, tx, tenant, r.ID)
			if err != nil {
				return err
			}
			return queuePayrollNewsTx(ctx, tx, tenant, users, news, event+":"+r.ID)
		}); err != nil {
			return queued, err
		}
		queued++
	}
	return queued, nil
}
