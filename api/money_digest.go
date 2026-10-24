package api

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/moneydigest"
	"github.com/israel-duff/taskiem/engine/moneyreport"
	"github.com/israel-duff/taskiem/engine/telegram"
	"github.com/israel-duff/taskiem/engine/whatsapp"
)

// moneyDailyStale is how long a queued daily summary may wait: a summary
// of yesterday that could not go out within a day is dropped.
const moneyDailyStale = 24 * time.Hour

// sendMoneyDailyItem sends a queued daily money summary
// (moneydigest.OutboxDaily, docs/money-reports.md) to its person, its
// figures read from the money log now. Outside WhatsApp's 24-hour window
// it goes as the alert template, in the one line the template has.
// Dropped when the person turned it off or may no longer read invoices
// (moneydigest.Gets), or it waited too long.
func (s *Server) sendMoneyDailyItem(ctx context.Context, it whatsapp.OutboxItem) error {
	if it.Digest == nil || it.User == uuid.Nil {
		return fmt.Errorf("%w: incomplete", whatsapp.ErrNothingToSend)
	}
	var business string
	var sum moneyreport.Summary
	err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{it.Tenant}, func(tx pgx.Tx) error {
		var period, sentAt time.Time
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
		sum, err = moneyreport.Summarise(ctx, tx, it.Tenant, from, to)
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
	head := moneydigest.DailyHead(l, sum)
	if r := []rune(head); len(r) > 200 {
		head = string(r[:200]) + "…"
	}
	tplLink := link
	if tplLink == "" {
		tplLink = "-"
	}
	tpl := whatsapp.TplAlert
	m := whatsapp.Message{Text: ch.Prefix(business) + moneydigest.DailyText(l, sum, link), Template: &tpl,
		Vars: map[string]string{"tenant": business, "title": head, "link": tplLink}, Tenant: it.Tenant}
	_, err = ch.Send(ctx, addr, m)
	switch {
	case err == nil:
		return nil
	case errors.Is(err, telegram.ErrBlocked):
		return fmt.Errorf("%w: %w", whatsapp.ErrNothingToSend, err)
	}
	return fmt.Errorf("to %s: %w", whatsapp.MaskNumber(addr), err)
}
