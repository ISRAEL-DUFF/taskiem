package api

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/dispatch"
	"github.com/israel-duff/taskiem/engine/invoicing"
	"github.com/israel-duff/taskiem/engine/lang"
	"github.com/israel-duff/taskiem/engine/moneydigest"
	"github.com/israel-duff/taskiem/engine/moneyreport"
	"github.com/israel-duff/taskiem/engine/stock"
	"github.com/israel-duff/taskiem/engine/telegram"
	"github.com/israel-duff/taskiem/engine/whatsapp"
)

// moneyDailyStale is how long a queued daily summary may wait: a summary
// of yesterday that could not go out within a day is dropped.
const moneyDailyStale = 24 * time.Hour

// sendMoneyDailyItem sends a queued daily money summary
// (moneydigest.OutboxDaily, docs/money-reports.md) to its person, its
// figures read from the money log now. Outside WhatsApp's 24-hour window
// it goes as the daily summary template with its Open button once Meta
// approved it in the number's account, else as the alert template, in
// the one line that template has. Dropped when the person turned it off
// or may no longer read invoices (moneydigest.Gets), or it waited too
// long.
func (s *Server) sendMoneyDailyItem(ctx context.Context, it whatsapp.OutboxItem) error {
	if it.Digest == nil || it.User == uuid.Nil {
		return fmt.Errorf("%w: incomplete", whatsapp.ErrNothingToSend)
	}
	var business string
	var sum moneyreport.Summary
	var school *moneydigest.SchoolSummary
	var riderDays []dispatch.RiderDay
	var sales stock.Day
	var shrink stock.Shrink
	var period time.Time
	err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{it.Tenant}, func(tx pgx.Tx) error {
		var sentAt time.Time
		if err := tx.QueryRow(ctx, `SELECT period, sent_at FROM money_digests WHERE id = $1 AND kind = 'daily'`, *it.Digest).Scan(&period, &sentAt); err != nil {
			return err
		}
		if time.Since(sentAt) > moneyDailyStale {
			return fmt.Errorf("%w: the summary waited more than a day", whatsapp.ErrNothingToSend)
		}
		ok, err := moneydigest.Gets(ctx, tx, it.Tenant, it.User, moneydigest.Daily)
		if err != nil {
			return err
		}
		if !ok {
			return fmt.Errorf("%w: the person turned the daily summary off, or may no longer read invoices", whatsapp.ErrNothingToSend)
		}
		if err := tx.QueryRow(ctx, `SELECT name FROM tenants WHERE id = $1`, it.Tenant).Scan(&business); err != nil {
			return err
		}
		from, to := moneyreport.Day(time.Date(period.Year(), period.Month(), period.Day(), 12, 0, 0, 0, moneyreport.Lagos))
		if sum, err = moneyreport.Summarise(ctx, tx, it.Tenant, from, to); err != nil {
			return err
		}
		if school, err = moneydigest.SchoolSummarise(ctx, tx, s.schoolsSvc(), it.Tenant, from, to); err != nil {
			return err
		}
		if s.Dispatch != nil {
			if riderDays, err = s.Dispatch.RiderDaysTx(ctx, tx, it.Tenant, from, to); err != nil {
				return err
			}
		}
		// The sales recorded by chat or the web (stock K2), and what was
		// lost (K4).
		if sales, err = stock.DayTx(ctx, tx, it.Tenant, from); err != nil {
			return err
		}
		shrink, err = stock.ShrinkTx(ctx, tx, it.Tenant, from, to)
		return err
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("%w: the summary is gone", whatsapp.ErrNothingToSend)
	}
	if err != nil {
		return err
	}
	ch, addr, err := s.chatAddrOf(ctx, it.Channel, it.Tenant, it.User)
	if err != nil {
		return err
	}
	l := s.chooseLang(ctx, it.Tenant, addr, "").Tag
	link := ""
	if s.PublicURL != "" {
		link = s.PublicURL + "/money"
	}
	full := moneydigest.DailyHead(l, sum)
	if line := moneydigest.SchoolLine(l, school); line != "" {
		full += " " + line
	}
	// What the business's Taskiem wallet holds, when it has one (W4).
	if bal, ok := s.receiveBalance(ctx, it.Tenant); ok {
		full += " " + tr(l, "wa.receive.balance", "amount", invoicing.Naira(bal))
	}
	head := full
	if r := []rune(head); len(r) > 200 {
		head = string(r[:200]) + "…"
	}
	tplLink := link
	if tplLink == "" {
		tplLink = "-"
	}
	day := time.Date(period.Year(), period.Month(), period.Day(), 12, 0, 0, 0, moneyreport.Lagos).Format("2 Jan 2006")
	text := ch.Prefix(business) + moneydigest.DailyTextWith(l, sum, school, "")
	// The sales recorded that day (stock K2).
	if line := salesDailyLine(l, sales); line != "" {
		text += "\n\n" + line
	}
	// What was lost to counts and adjustments that day, at cost (K4).
	if line := shrinkDailyLine(l, shrink); line != "" {
		text += "\n\n" + line
	}
	// What is low this morning, with how many to order (stock K3).
	if line := s.lowDailyLine(ctx, l, it.Tenant); line != "" {
		text += "\n\n" + line
	}
	// Linked suppliers' bills due soon or overdue (business links L5).
	if line := s.billsDueDailyLine(ctx, l, it.Tenant); line != "" {
		text += "\n\n" + line
	}
	// Today's deliveries and bookings, and the morning card in the
	// Taskiem chat (chat tools phase 5), before the riders and the link.
	if line := s.todayDailyLine(ctx, l, it.Tenant, period.AddDate(0, 0, 1)); line != "" {
		text += "\n\n" + line
	}
	// Each rider's day (dispatch D4), before the link.
	if len(riderDays) > 0 {
		text += "\n\n" + tr(l, "money.daily.riders")
		for _, x := range riderDays {
			text += "\n" + dispatch.RiderLine(l, x)
		}
	}
	if link != "" {
		text += "\n\n" + tr(l, "money.daily.link", "link", link)
	}
	// With the summary template not approved yet, the alert template
	// carries the first line, as before it existed.
	err = sendOwnTemplate(ctx, ch, addr, text, it.Tenant, whatsapp.TplDailySummary, map[string]string{"tenant": business, "day": day, "summary": strings.TrimPrefix(full, "Yesterday: ")},
		map[string]string{"tenant": business, "title": head, "link": tplLink})
	switch {
	case err == nil, errors.Is(err, whatsapp.ErrFellBack):
		return err
	case errors.Is(err, telegram.ErrBlocked):
		return fmt.Errorf("%w: %w", whatsapp.ErrNothingToSend, err)
	}
	return fmt.Errorf("to %s: %w", whatsapp.MaskNumber(addr), err)
}

// todayDailyLine is the morning's today: deliveries and bookings, and,
// with chat tools on, where the morning card is ("" with nothing to say).
func (s *Server) todayDailyLine(ctx context.Context, l lang.Tag, tenant uuid.UUID, day time.Time) string {
	var deliveries, bookings int
	var tools bool
	_ = db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		var err error
		if tools, err = chatToolsOn(ctx, tx, tenant); err != nil {
			return err
		}
		today := time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, moneyreport.Lagos)
		if s.Dispatch.Ready() {
			if list, err := s.Dispatch.ListTx(ctx, tx, tenant, dispatch.ListFilter{Day: today.Format("2006-01-02")}); err == nil {
				deliveries = len(list)
			}
		}
		if s.Orders != nil {
			if list, err := s.Orders.Bookings(ctx, tx, tenant, today, today.AddDate(0, 0, 1)); err == nil {
				bookings = len(list)
			}
		}
		return nil
	})
	var lines []string
	if deliveries > 0 || bookings > 0 {
		lines = append(lines, tr(l, "money.daily.today", "deliveries", fmt.Sprint(deliveries), "bookings", fmt.Sprint(bookings)))
	}
	if tools && s.PublicURL != "" {
		lines = append(lines, tr(l, "money.daily.card", "link", s.PublicURL+"/chat?with=taskiem"))
	}
	return strings.Join(lines, "\n")
}
