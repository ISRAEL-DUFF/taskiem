// Package moneydigest sends a business's money summaries on a schedule
// (decision 0045; docs/spec/invoices-and-money-log.md 9.2, 9.3, T7;
// docs/money-reports.md):
//
//   - every morning from 08:00 Lagos, yesterday's money on the chat they
//     linked (WhatsApp before Telegram): money in, settled and pending, by
//     channel, money out, invoices paid and leaks opened, with a link to
//     the Money page. A day with no movement, no invoice paid and no leak
//     opened sends nothing.
//   - on the 1st of the month from 09:00 Lagos (or on the 2nd or 3rd, when
//     the scheduler was down), last month's summary by email, with a link
//     to the Money page's exports. Only when email is set up.
//
// Who gets them is the money group of Settings > Notifications
// (Recipients): active members who may read invoices, owners unless they
// turned a summary off and others when they turned it on.
//
// Each summary is taken once per business, kind and period in
// money_digests, in the transaction that queues it, so restarts and
// several schedulers never send one twice; its recipients are decided
// then, so a person who turns the daily summary on later that day gets the
// next one. The chat message is an outbox row (kind money_daily) naming
// the digest and the person; the api role renders and sends it (DailyText,
// DailyHead), and drops it when the person no longer Gets it.
package moneydigest

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/israel-duff/taskiem/engine/access"
	"github.com/israel-duff/taskiem/engine/alerts"
	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/invoicing"
	"github.com/israel-duff/taskiem/engine/lang"
	"github.com/israel-duff/taskiem/engine/moneyreport"
	"github.com/israel-duff/taskiem/engine/notices"
	"github.com/israel-duff/taskiem/engine/plain"
	"github.com/israel-duff/taskiem/engine/whatsapp"
)

// OutboxDaily is the chat outbox kind of the daily summary.
const OutboxDaily = "money_daily"

// Kinds of digest.
const (
	Daily   = "daily"
	Monthly = "monthly"
)

// When summaries go out, Lagos time.
const (
	DailyHour   = 8
	MonthlyHour = 9
	// MonthlyDays is how many days into the month last month's email may
	// still go out, when the scheduler was down on the 1st.
	MonthlyDays = 3
)

// Digester sends the summaries.
type Digester struct {
	Pool *pgxpool.Pool
	// Channels are the chat channels the deployment runs ("whatsapp",
	// "telegram"); none sends no daily summary.
	Channels []string
	// Mailer and From send the monthly email; either unset sends none.
	Mailer alerts.Mailer
	From   string
	// PublicURL is the web app's address, for the Money page's link.
	PublicURL string
	Interval  time.Duration // between looks; default 5 minutes
	Logger    *slog.Logger
	Now       func() time.Time
}

func (d *Digester) now() time.Time {
	if d.Now != nil {
		return d.Now()
	}
	return time.Now()
}

func (d *Digester) log() *slog.Logger {
	if d.Logger != nil {
		return d.Logger
	}
	return slog.Default()
}

// Link is the Money page.
func (d *Digester) Link() string {
	if d.PublicURL == "" {
		return ""
	}
	return strings.TrimRight(d.PublicURL, "/") + "/money"
}

// Run looks every Interval until ctx ends.
func (d *Digester) Run(ctx context.Context) error {
	every := d.Interval
	if every <= 0 {
		every = 5 * time.Minute
	}
	for {
		if r, err := d.Tick(ctx); err != nil && ctx.Err() == nil {
			d.log().Error("money digests", "err", err)
		} else if r.Daily+r.Monthly > 0 {
			d.log().Info("money digests sent", "daily", r.Daily, "monthly", r.Monthly)
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(every):
		}
	}
}

// Report is what one Tick sent: businesses whose daily summary was
// queued, and whose monthly email went out.
type Report struct {
	Daily, Monthly int
}

// Tick sends what is due now: yesterday's summary after 08:00, last
// month's email on the first days of the month after 09:00.
func (d *Digester) Tick(ctx context.Context) (Report, error) {
	var r Report
	now := d.now().In(moneyreport.Lagos)
	today, _ := moneyreport.Day(now)
	var day, month *time.Time
	if now.Hour() >= DailyHour && len(d.Channels) > 0 {
		y := today.AddDate(0, 0, -1)
		day = &y
	}
	if d.Mailer != nil && d.From != "" && now.Day() <= MonthlyDays && (now.Day() > 1 || now.Hour() >= MonthlyHour) {
		this, _ := moneyreport.Month(now)
		m := this.AddDate(0, -1, 0)
		month = &m
	}
	if day == nil && month == nil {
		return r, nil
	}
	rows, err := d.Pool.Query(ctx, `SELECT tenant_id FROM taskiem_money_leak_tenants()`)
	if err != nil {
		return r, err
	}
	tenants, err := pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
	if err != nil {
		return r, err
	}
	// One business's failure is logged and tried again at the next look;
	// it never holds up the others.
	for _, t := range tenants {
		if day != nil {
			if sent, err := d.daily(ctx, t, *day); err != nil && ctx.Err() == nil {
				d.log().Warn("money digests: daily", "tenant", t, "err", err)
			} else if sent {
				r.Daily++
			}
		}
		if month != nil {
			if sent, err := d.monthly(ctx, t, *month); err != nil && ctx.Err() == nil {
				d.log().Warn("money digests: monthly", "tenant", t, "err", err)
			} else if sent {
				r.Monthly++
			}
		}
		if err := ctx.Err(); err != nil {
			return r, err
		}
	}
	return r, nil
}

// Quiet says nothing happened: no movement, no invoice paid, no leak
// opened.
func Quiet(s moneyreport.Summary) bool {
	moved := 0
	for _, c := range s.Channels {
		moved += c.Movements
	}
	return moved == 0 && s.InvoicesPaid == 0 && s.LeaksOpened == 0
}

// activeName is the tenant's name, or "" when it is not active.
func activeName(ctx context.Context, tx pgx.Tx, tenant uuid.UUID) (string, error) {
	var name, status string
	err := tx.QueryRow(ctx, `SELECT name, status FROM tenants WHERE id = $1`, tenant).Scan(&name, &status)
	if errors.Is(err, pgx.ErrNoRows) || status != "active" {
		return "", nil
	}
	return name, err
}

func taken(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, kind string, period time.Time) (bool, error) {
	var ok bool
	err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM money_digests WHERE tenant_id = $1 AND kind = $2 AND period = $3)`, tenant, kind, period.Format("2006-01-02")).Scan(&ok)
	return ok, err
}

// take records the digest; false when another scheduler took it first.
func take(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, kind string, period time.Time, outcome string) (uuid.UUID, bool, error) {
	id := uuid.Must(uuid.NewV7())
	tag, err := tx.Exec(ctx, `INSERT INTO money_digests (id, tenant_id, kind, period, outcome) VALUES ($1, $2, $3, $4, $5) ON CONFLICT (tenant_id, kind, period) DO NOTHING`,
		id, tenant, kind, period.Format("2006-01-02"), outcome)
	return id, err == nil && tag.RowsAffected() == 1, err
}

// readers are the tenant's active members who may read invoices: a
// built-in role holding invoice.read (engine/access), or a custom role
// whose permissions say so. A business role may not.
func readers(ctx context.Context, tx pgx.Tx, tenant uuid.UUID) ([]uuid.UUID, error) {
	builtin, read := access.BuiltinNames(), access.Holding(access.InvoiceRead)
	rows, err := tx.Query(ctx, `SELECT DISTINCT ms.user_id FROM memberships ms JOIN users u ON u.id = ms.user_id
		WHERE ms.tenant_id = $1 AND u.status = 'active'
		  AND CASE WHEN ms.role = ANY ($2) THEN ms.role = ANY ($3)
		      ELSE EXISTS (SELECT 1 FROM roles r WHERE r.tenant_id = ms.tenant_id AND r.name = ms.role AND $4 = ANY (r.permissions)) END
		ORDER BY ms.user_id`, tenant, builtin, read, access.InvoiceRead)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
}

// Recipients are who get a business's summary of kind (Daily or Monthly)
// now: its active members who may read invoices and want it (Settings >
// Notifications, the money group: chat is the daily summary, email the
// monthly email; owners by default, others when they turned it on).
func Recipients(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, kind string) ([]uuid.UUID, error) {
	us, err := readers(ctx, tx, tenant)
	if err != nil || len(us) == 0 {
		return nil, err
	}
	daily, monthly, err := notices.Wants(ctx, tx, us, notices.GroupMoney)
	if kind == Daily {
		return daily, err
	}
	return monthly, err
}

// Gets says whether user still gets the business's summary of kind: what
// the outbox asks before sending one queued earlier, since the person may
// have turned it off, or lost invoice.read, since.
func Gets(ctx context.Context, tx pgx.Tx, tenant, user uuid.UUID, kind string) (bool, error) {
	us, err := Recipients(ctx, tx, tenant, kind)
	return slices.Contains(us, user), err
}

// daily queues yesterday's summary to each recipient with a linked chat.
func (d *Digester) daily(ctx context.Context, tenant uuid.UUID, day time.Time) (bool, error) {
	sent := false
	err := db.InTenantTx(ctx, d.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		name, err := activeName(ctx, tx, tenant)
		if err != nil || name == "" {
			return err
		}
		if done, err := taken(ctx, tx, tenant, Daily, day); err != nil || done {
			return err
		}
		s, err := moneyreport.Summarise(ctx, tx, tenant, day, day.AddDate(0, 0, 1))
		if err != nil {
			return err
		}
		if Quiet(s) {
			_, _, err = take(ctx, tx, tenant, Daily, day, "quiet")
			return err
		}
		us, err := Recipients(ctx, tx, tenant, Daily)
		if err != nil {
			return err
		}
		// Each person once, on the first channel they linked: WhatsApp
		// before Telegram.
		type to struct {
			user    uuid.UUID
			channel string
		}
		var list []to
		told := map[uuid.UUID]bool{}
		for _, ch := range []struct{ name, q string }{
			{"whatsapp", `SELECT user_id FROM taskiem_wa_numbers($1)`},
			{"telegram", `SELECT user_id FROM taskiem_tg_accounts($1)`},
		} {
			if !d.runs(ch.name) || len(us) == 0 {
				continue
			}
			rows, err := tx.Query(ctx, ch.q, us)
			if err != nil {
				return err
			}
			linked, err := pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
			if err != nil {
				return err
			}
			for _, u := range linked {
				if !told[u] {
					told[u] = true
					list = append(list, to{u, ch.name})
				}
			}
		}
		outcome := "sent"
		if len(list) == 0 {
			outcome = "no_one"
		}
		id, fresh, err := take(ctx, tx, tenant, Daily, day, outcome)
		if err != nil || !fresh {
			return err
		}
		for _, x := range list {
			digest := id
			key := "money_daily:" + id.String() + ":" + x.user.String()
			if x.channel != "whatsapp" {
				key = x.channel + ":" + key
			}
			if _, err := whatsapp.Enqueue(ctx, tx, whatsapp.OutboxItem{Tenant: tenant, Channel: x.channel, Kind: OutboxDaily, User: x.user, Digest: &digest}, key); err != nil {
				return err
			}
		}
		sent = len(list) > 0
		return nil
	})
	return sent, err
}

func (d *Digester) runs(channel string) bool {
	for _, c := range d.Channels {
		if c == channel {
			return true
		}
	}
	return false
}

// monthly emails last month's summary to each recipient with an email.
func (d *Digester) monthly(ctx context.Context, tenant uuid.UUID, month time.Time) (bool, error) {
	var to []string
	var subject, body string
	var id uuid.UUID
	err := db.InTenantTx(ctx, d.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		name, err := activeName(ctx, tx, tenant)
		if err != nil || name == "" {
			return err
		}
		if done, err := taken(ctx, tx, tenant, Monthly, month); err != nil || done {
			return err
		}
		s, err := moneyreport.Summarise(ctx, tx, tenant, month, month.AddDate(0, 1, 0))
		if err != nil {
			return err
		}
		if Quiet(s) {
			_, _, err = take(ctx, tx, tenant, Monthly, month, "quiet")
			return err
		}
		us, err := Recipients(ctx, tx, tenant, Monthly)
		if err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `SELECT DISTINCT email FROM users WHERE id = ANY ($1) AND COALESCE(email, '') <> '' ORDER BY email`, us)
		if err != nil {
			return err
		}
		emails, err := pgx.CollectRows(rows, pgx.RowTo[string])
		if err != nil {
			return err
		}
		outcome := "sent"
		if len(emails) == 0 {
			outcome = "no_one"
		}
		var fresh bool
		if id, fresh, err = take(ctx, tx, tenant, Monthly, month, outcome); err != nil || !fresh {
			return err
		}
		to = emails
		subject, body = MonthlyEmail(name, s, d.Link())
		return nil
	})
	if err != nil || len(to) == 0 {
		return false, err
	}
	// Sent after the transaction, one person at a time. When none could be
	// sent, the digest is let go so a later look tries again.
	ok := 0
	var errs []error
	for _, addr := range to {
		if err := d.Mailer.Send(ctx, d.From, []string{addr}, alerts.BuildEmail(d.From, []string{addr}, subject, body, "money-"+id.String())); err != nil {
			errs = append(errs, err)
			continue
		}
		ok++
	}
	if ok == 0 {
		if err := db.InTenantTx(ctx, d.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
			_, err := tx.Exec(ctx, `DELETE FROM money_digests WHERE id = $1`, id)
			return err
		}); err != nil {
			errs = append(errs, err)
		}
		return false, fmt.Errorf("emailing the summary: %w", errors.Join(errs...))
	}
	if len(errs) > 0 {
		d.log().Warn("money digests: emailing a person", "tenant", tenant, "err", errors.Join(errs...))
	}
	return true, nil
}

// --- the words ---

// Label is a channel as people know it: Paystack, Moniepoint POS, cash.
func Label(l lang.Tag, channel string) string {
	switch channel {
	case "cash":
		return lang.Default().Text(l, "money.channel.cash")
	case "upload":
		return lang.Default().Text(l, "money.channel.upload")
	}
	connector, _, _ := strings.Cut(channel, "/")
	if base, ok := strings.CutSuffix(connector, "_pos"); ok {
		return whatsapp.SafeText(plain.ServiceName(base) + " POS")
	}
	return whatsapp.SafeText(plain.ServiceName(connector))
}

// DailyHead is the daily summary in l in one line, without the business's
// name or the link: what the alert template carries outside WhatsApp's
// window.
func DailyHead(l lang.Tag, s moneyreport.Summary) string {
	c := lang.Default()
	in := c.Text(l, "money.daily.in", "amount", invoicing.Naira(s.InSettled))
	if s.InPending > 0 {
		in = c.Text(l, "money.daily.in_pending", "amount", invoicing.Naira(s.InSettled), "pending", invoicing.Naira(s.InPending))
	}
	// Money in by channel, the largest first, names alike added up.
	var names []string
	by := map[string]int64{}
	for _, ch := range s.Channels {
		if ch.InSettled <= 0 {
			continue
		}
		n := Label(l, ch.Channel)
		if _, ok := by[n]; !ok {
			names = append(names, n)
		}
		by[n] += ch.InSettled
	}
	var parts []string
	for i, n := range names {
		if i == 4 && len(names) > 5 {
			parts = append(parts, "…")
			break
		}
		parts = append(parts, n+" "+invoicing.Naira(by[n]))
	}
	if len(parts) > 0 {
		in += " — " + strings.Join(parts, ", ")
	}
	invoices := c.Text(l, "money.daily.invoices_none")
	switch {
	case s.InvoicesPaid == 1:
		invoices = c.Text(l, "money.daily.invoices_one", "amount", invoicing.Naira(s.InvoicesPaidKobo))
	case s.InvoicesPaid > 1:
		invoices = c.Text(l, "money.daily.invoices", "n", strconv.Itoa(s.InvoicesPaid), "amount", invoicing.Naira(s.InvoicesPaidKobo))
	}
	leaks := c.Text(l, "money.daily.leaks_none")
	switch {
	case s.LeaksOpened == 1:
		leaks = c.Text(l, "money.daily.leaks_one", "amount", invoicing.Naira(s.LeaksOpenedKobo))
	case s.LeaksOpened > 1:
		leaks = c.Text(l, "money.daily.leaks", "n", strconv.Itoa(s.LeaksOpened), "amount", invoicing.Naira(s.LeaksOpenedKobo))
	}
	return c.Text(l, "money.daily", "in", in, "out", invoicing.Naira(s.OutSettled), "invoices", invoices, "leaks", leaks)
}

// DailyText is the daily summary in l, without the business's name (the
// channel's prefix adds it), with the link to the Money page.
func DailyText(l lang.Tag, s moneyreport.Summary, link string) string {
	text := DailyHead(l, s)
	if link != "" {
		text += "\n\n" + lang.Default().Text(l, "money.daily.link", "link", link)
	}
	return text
}

func percent(bp *int) string {
	if bp == nil {
		return ""
	}
	return fmt.Sprintf(" (%d.%02d%% of the money in they were taken on)", *bp/100, *bp%100)
}

// MonthlyEmail is last month's email: its subject and plain-text body.
func MonthlyEmail(business string, s moneyreport.Summary, link string) (string, string) {
	month := s.From.In(moneyreport.Lagos).Format("January 2006")
	n := invoicing.Naira
	var b strings.Builder
	fmt.Fprintf(&b, "%s: money in %s\n\n", business, month)
	fmt.Fprintf(&b, "Money in: %s settled, %s pending\n", n(s.InSettled), n(s.InPending))
	fmt.Fprintf(&b, "Money out: %s settled, %s pending\n", n(s.OutSettled), n(s.OutPending))
	fmt.Fprintf(&b, "Fees: %s%s\n", n(s.FeesKobo), percent(s.FeeRateBP))
	if len(s.Channels) > 0 {
		b.WriteString("\nBy channel:\n")
		for _, c := range s.Channels {
			fmt.Fprintf(&b, "- %s (%s): in %s settled, %s pending; out %s settled, %s pending; fees %s\n",
				Label(lang.EN, c.Channel), whatsapp.SafeText(c.Channel), n(c.InSettled), n(c.InPending), n(c.OutSettled), n(c.OutPending), n(c.FeesKobo))
		}
	}
	fmt.Fprintf(&b, "\nInvoices paid: %d (%s)\n", s.InvoicesPaid, n(s.InvoicesPaidKobo))
	fmt.Fprintf(&b, "Leaks opened: %d (%s)\n", s.LeaksOpened, n(s.LeaksOpenedKobo))
	fmt.Fprintf(&b, "Leaks open now: %d (%s)\n", s.LeaksOpen, n(s.LeaksOpenKobo))
	fmt.Fprintf(&b, "Recovered so far: %s\n", n(s.RecoveredKobo))
	b.WriteString("\nEach movement is counted once, at its latest status. Times are Lagos time.\n")
	if link != "" {
		fmt.Fprintf(&b, "\nThe month's money log, invoices and accounting journal, as spreadsheets (CSV), are on the Money page:\n%s\n", link)
	}
	return fmt.Sprintf("[%s] money in %s", business, month), b.String()
}
