package stock

import (
	"context"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/moneydigest"
	"github.com/israel-duff/taskiem/engine/storefront"
	"github.com/israel-duff/taskiem/engine/whatsapp"
)

// The evening total (docs/spec/stock.md §5): once a day, at the business's
// closing time (the storefront's hours for the day, else EveningHour), the
// people who get the money summary in chat are told the day's sales. The
// message is written when it is sent (the API's outbox), from the sales
// then; this queues it once a day, recorded in money_digests.

// OutboxEvening is the chat outbox kind of the evening total.
const OutboxEvening = "sales_evening"

// EveningKind is its money_digests kind.
const EveningKind = "sales_evening"

// EveningHour is when the evening total goes, Lagos time, for a business
// whose hours say nothing for the day.
const EveningHour = 21

// Evening queues each business's evening total.
type Evening struct {
	Pool *pgxpool.Pool
	// Channels are the chat channels the deployment runs ("whatsapp",
	// "telegram"); none sends nothing.
	Channels []string
	Interval time.Duration // between looks; default 5 minutes
	Logger   *slog.Logger
	Now      func() time.Time
}

func (e *Evening) now() time.Time {
	if e.Now != nil {
		return e.Now()
	}
	return time.Now()
}

func (e *Evening) log() *slog.Logger {
	if e.Logger != nil {
		return e.Logger
	}
	return slog.Default()
}

// Run looks every Interval until ctx ends.
func (e *Evening) Run(ctx context.Context) error {
	every := e.Interval
	if every <= 0 {
		every = 5 * time.Minute
	}
	for {
		if n, err := e.Tick(ctx); err != nil && ctx.Err() == nil {
			e.log().Error("stock: evening totals", "err", err)
		} else if n > 0 {
			e.log().Info("stock: evening totals queued", "businesses", n)
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(every):
		}
	}
}

// Closes is when a business closes on t's Lagos day: its last opening's
// close, else EveningHour. An opening past midnight closes at 23:30.
func Closes(h storefront.Hours, t time.Time) time.Time {
	t = t.In(storefront.Lagos)
	day := time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, storefront.Lagos)
	at := day.Add(EveningHour * time.Hour)
	spans := h.Days[(int(t.Weekday())+6)%7]
	if len(spans) == 0 {
		return at
	}
	last := spans[len(spans)-1]
	oh, om, ok1 := hhmm(last.Open)
	ch, cm, ok2 := hhmm(last.Close)
	if !ok1 || !ok2 {
		return at
	}
	if ch*60+cm <= oh*60+om {
		return day.Add(23*time.Hour + 30*time.Minute)
	}
	return day.Add(time.Duration(ch)*time.Hour + time.Duration(cm)*time.Minute)
}

func hhmm(s string) (int, int, bool) {
	h, m, ok := strings.Cut(s, ":")
	if !ok {
		return 0, 0, false
	}
	hh, err1 := strconv.Atoi(h)
	mm, err2 := strconv.Atoi(m)
	return hh, mm, err1 == nil && err2 == nil && hh >= 0 && hh < 24 && mm >= 0 && mm < 60
}

// Tick queues what is due now; it answers how many businesses it queued.
func (e *Evening) Tick(ctx context.Context) (int, error) {
	if len(e.Channels) == 0 {
		return 0, nil
	}
	rows, err := e.Pool.Query(ctx, `SELECT tenant_id FROM taskiem_stock_tenants()`)
	if err != nil {
		return 0, err
	}
	tenants, err := pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
	if err != nil {
		return 0, err
	}
	n := 0
	for _, t := range tenants {
		sent, err := e.Tenant(ctx, t)
		if err != nil && ctx.Err() == nil {
			e.log().Warn("stock: evening total", "tenant", t, "err", err)
		}
		if sent {
			n++
		}
	}
	return n, ctx.Err()
}

// Tenant queues a business's evening total when it has closed and it was
// not queued today: to each person who gets the money summary in chat,
// once, on the first channel they linked. A day with no sales and no
// terminal sale is recorded quiet, and nothing is sent.
func (e *Evening) Tenant(ctx context.Context, tenant uuid.UUID) (bool, error) {
	now := e.now().In(storefront.Lagos)
	sent := false
	err := db.InTenantTx(ctx, e.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		sf, err := storefront.Get(ctx, tx, tenant)
		if err != nil {
			return err
		}
		if now.Before(Closes(sf.Draft.Hours, now)) {
			return nil
		}
		day := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, storefront.Lagos)
		var done bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM money_digests WHERE tenant_id = $1 AND kind = $2 AND period = $3)`,
			tenant, EveningKind, day.Format("2006-01-02")).Scan(&done); err != nil || done {
			return err
		}
		d, err := DayTx(ctx, tx, tenant, day)
		if err != nil {
			return err
		}
		outcome := "sent"
		var list []recipient
		if d.Count == 0 && len(d.Unsold) == 0 {
			outcome = "quiet"
		} else {
			if list, err = e.recipients(ctx, tx, tenant); err != nil {
				return err
			}
			if len(list) == 0 {
				outcome = "no_one"
			}
		}
		id := uuid.Must(uuid.NewV7())
		tag, err := tx.Exec(ctx, `INSERT INTO money_digests (id, tenant_id, kind, period, outcome) VALUES ($1, $2, $3, $4, $5)
			ON CONFLICT (tenant_id, kind, period) DO NOTHING`, id, tenant, EveningKind, day.Format("2006-01-02"), outcome)
		if err != nil || tag.RowsAffected() == 0 {
			return err
		}
		for _, r := range list {
			digest := id
			key := "sales_evening:" + id.String() + ":" + r.user.String()
			if r.channel != "whatsapp" {
				key = r.channel + ":" + key
			}
			if _, err := whatsapp.Enqueue(ctx, tx, whatsapp.OutboxItem{Tenant: tenant, Channel: r.channel, Kind: OutboxEvening, User: r.user, Digest: &digest}, key); err != nil {
				return err
			}
		}
		sent = len(list) > 0
		return nil
	})
	return sent, err
}

type recipient struct {
	user    uuid.UUID
	channel string
}

// recipients are who get the money summary in chat (moneydigest), each
// once on the first channel they linked: WhatsApp before Telegram.
func (e *Evening) recipients(ctx context.Context, tx pgx.Tx, tenant uuid.UUID) ([]recipient, error) {
	us, err := moneydigest.Recipients(ctx, tx, tenant, moneydigest.Daily)
	if err != nil || len(us) == 0 {
		return nil, err
	}
	var list []recipient
	told := map[uuid.UUID]bool{}
	for _, ch := range []struct{ name, q string }{
		{"whatsapp", `SELECT user_id FROM taskiem_wa_numbers($1)`},
		{"telegram", `SELECT user_id FROM taskiem_tg_accounts($1)`},
	} {
		if !e.runs(ch.name) {
			continue
		}
		rows, err := tx.Query(ctx, ch.q, us)
		if err != nil {
			return nil, err
		}
		linked, err := pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
		if err != nil {
			return nil, err
		}
		for _, u := range linked {
			if !told[u] {
				told[u] = true
				list = append(list, recipient{u, ch.name})
			}
		}
	}
	return list, nil
}

func (e *Evening) runs(channel string) bool {
	for _, c := range e.Channels {
		if c == channel {
			return true
		}
	}
	return false
}
