package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/dispatch"
	"github.com/israel-duff/taskiem/engine/lang"
	"github.com/israel-duff/taskiem/engine/links"
	"github.com/israel-duff/taskiem/engine/stock"
	"github.com/israel-duff/taskiem/engine/storefront"
	"github.com/israel-duff/taskiem/engine/whatsapp"
)

// Chat tools phase 5 (docs/spec/chat-tools.md §7): the morning briefing,
// "how did we do?" with a chart, and a shift's handover. Each reads the
// same numbers the pages and the daily summaries do, in Lagos time, and
// shows only what the person may see.

// briefingHour is when the briefing is ready, Lagos time (as the morning
// money summary, engine/moneydigest).
const briefingHour = 8

// daySales is a day's sales total, for a chart's bar.
type daySales struct {
	Day   string `json:"day"`   // 2006-01-02
	Label string `json:"label"` // "Mon"
	Kobo  int64  `json:"kobo"`
}

// salesDays are the sales of the n days up to and including last, read
// in one go (a day's total is its sales' amounts, as stock.DayTx).
func salesDays(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, last time.Time, n int) ([]daySales, error) {
	end := time.Date(last.Year(), last.Month(), last.Day(), 0, 0, 0, 0, storefront.Lagos).AddDate(0, 0, 1)
	from := end.AddDate(0, 0, -n)
	sales, err := stock.SalesTx(ctx, tx, tenant, from, end)
	if err != nil {
		return nil, err
	}
	by := map[string]int64{}
	for _, x := range sales {
		by[x.SoldAt.In(storefront.Lagos).Format("2006-01-02")] += x.AmountKobo
	}
	out := make([]daySales, 0, n)
	for d := from; d.Before(end); d = d.AddDate(0, 0, 1) {
		k := d.Format("2006-01-02")
		out = append(out, daySales{Day: k, Label: d.Format("Mon"), Kobo: by[k]})
	}
	return out, nil
}

// lagosToday is the start of today in Lagos.
func lagosToday(now time.Time) time.Time {
	t := now.In(storefront.Lagos)
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, storefront.Lagos)
}

// changePct is how much a is up or down on b, in whole percent; nil
// with nothing to compare with.
func changePct(a, b int64) *int {
	if b <= 0 {
		return nil
	}
	v := int((a - b) * 100 / b)
	return &v
}

type briefing struct {
	// Ready is whether it is 8am yet; Day today's date; Key what Got it
	// puts off (one a day).
	Ready bool   `json:"ready"`
	Day   string `json:"day"`
	Key   string `json:"key"`
	// PutOff: the person has seen today's (Got it).
	PutOff bool `json:"put_off"`
	// Sales yesterday, the same day a week before, and the week's bars
	// (yesterday last); with stock to read.
	Sales *briefingSales `json:"sales,omitempty"`
	// OwedKobo is what customers owe (open invoices and the notebook's
	// debts); with invoices to read.
	OwedKobo *int64 `json:"owed_kobo,omitempty"`
	// Low is how many items are at or below their reorder level.
	Low *int `json:"low,omitempty"`
	// Deliveries and Bookings are today's.
	Deliveries *int `json:"deliveries,omitempty"`
	Bookings   *int `json:"bookings,omitempty"`
}

type briefingSales struct {
	YesterdayKobo int64      `json:"yesterday_kobo"`
	ChangePct     *int       `json:"change_pct"`
	Weekday       string     `json:"weekday"` // "Wednesday": what it is compared with
	Week          []daySales `json:"week"`
}

// mayBrief is who gets the morning briefing: those who run the business
// (the storefront or the money), not every member.
func mayBrief(p *Principal) bool {
	return p.Can(PermStorefrontManage) || p.Can(PermInvoiceManage)
}

// getChatBriefing is GET /v1/chat-briefing: the person's morning briefing
// (chat tools on, from 8am Lagos).
func (s *Server) getChatBriefing(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r.Context())
	if p.UserID == uuid.Nil {
		s.fail(w, r, errNoPerson)
		return
	}
	if !mayBrief(p) {
		s.fail(w, r, fmt.Errorf("%w: the briefing is for those who run the business", errForbidden))
		return
	}
	ctx := r.Context()
	now := time.Now()
	if s.BriefingClock != nil {
		now = s.BriefingClock()
	}
	today := lagosToday(now)
	out := briefing{Day: today.Format("2006-01-02"), Ready: now.In(storefront.Lagos).Hour() >= briefingHour}
	out.Key = "briefing:" + out.Day
	var on bool
	if err := s.tx(r, func(tx pgx.Tx) error {
		var err error
		if on, err = chatToolsOn(ctx, tx, p.TenantID); err != nil || !on {
			return err
		}
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM chat_alerts_later WHERE tenant_id = $1 AND user_id = $2 AND alert_key = $3 AND until > now())`,
			p.TenantID, p.UserID, out.Key).Scan(&out.PutOff); err != nil {
			return err
		}
		if p.Can(PermTableRead) && s.Stock.Ready() {
			week, err := salesDays(ctx, tx, p.TenantID, today.AddDate(0, 0, -1), 7)
			if err != nil {
				return err
			}
			before, err := stock.DayTx(ctx, tx, p.TenantID, today.AddDate(0, 0, -8))
			if err != nil {
				return err
			}
			y := week[len(week)-1].Kobo
			out.Sales = &briefingSales{YesterdayKobo: y, ChangePct: changePct(y, before.TotalKobo), Weekday: today.AddDate(0, 0, -1).Format("Monday"), Week: week}
		}
		if p.Can(PermInvoiceRead) {
			var owed int64
			if err := tx.QueryRow(ctx, `SELECT COALESCE(sum(total_kobo - paid_kobo), 0)::bigint FROM customer_invoices
				WHERE tenant_id = $1 AND status IN ('recorded', 'sent', 'part_paid', 'overdue')`, p.TenantID).Scan(&owed); err != nil {
				return err
			}
			out.OwedKobo = &owed
		}
		if p.Can(PermOrders) && s.Dispatch.Ready() {
			list, err := s.Dispatch.ListTx(ctx, tx, p.TenantID, dispatch.ListFilter{Day: out.Day})
			if err != nil {
				return err
			}
			n := len(list)
			out.Deliveries = &n
		}
		if p.Can(PermStorefrontManage) && s.Orders != nil {
			list, err := s.Orders.Bookings(ctx, tx, p.TenantID, today, today.AddDate(0, 0, 1))
			if err != nil {
				return err
			}
			n := len(list)
			out.Bookings = &n
		}
		return nil
	}); err != nil {
		s.fail(w, r, err)
		return
	}
	if !on {
		s.fail(w, r, fmt.Errorf("%w: chat tools are off", errConflict))
		return
	}
	if p.Can(PermTableRead) && s.Stock.Ready() {
		if items, err := s.alertStockItems(ctx, p.TenantID); err == nil {
			n := 0
			for _, it := range items {
				if it.Low() {
					n++
				}
			}
			out.Low = &n
		}
	}
	writeJSON(w, http.StatusOK, out)
}

// handover is a shift so far (today, Lagos): what was sold and how, the
// orders still open, the deliveries out and done, and what is low.
type handover struct {
	Day   string            `json:"day"`
	Sales *handoverSales    `json:"sales,omitempty"`
	Open  *int              `json:"orders_open,omitempty"`
	Out   *handoverDelivery `json:"deliveries,omitempty"`
	Low   []string          `json:"low"`
}

type handoverSales struct {
	TotalKobo int64            `json:"total_kobo"`
	Count     int              `json:"count"`
	ByHow     map[string]int64 `json:"by_how"`
	Waiting   int              `json:"waiting"`
}

type handoverDelivery struct {
	Out  int `json:"out"`
	Done int `json:"done"`
}

func (s *Server) handoverOf(ctx context.Context, p *Principal) (handover, error) {
	today := lagosToday(time.Now())
	out := handover{Day: today.Format("2006-01-02"), Low: []string{}}
	err := db0(ctx, s, p.TenantID, func(tx pgx.Tx) error {
		if s.Stock.Ready() {
			day, err := stock.DayTx(ctx, tx, p.TenantID, today)
			if err != nil {
				return err
			}
			out.Sales = &handoverSales{TotalKobo: day.TotalKobo, Count: day.Count, ByHow: day.ByHow, Waiting: day.Waiting}
			if out.Sales.ByHow == nil {
				out.Sales.ByHow = map[string]int64{}
			}
		}
		if p.Can(PermOrders) {
			list, err := links.ListOrdersTx(ctx, tx, true, 200)
			if err != nil {
				return err
			}
			n := 0
			for _, o := range list {
				if o.Status == "received" || o.Status == "accepted" || o.Status == "ready" || o.Status == "changed" {
					n++
				}
			}
			// And the storefront's own customers' orders not done yet.
			var shop int
			if err := tx.QueryRow(ctx, `SELECT count(*) FROM storefront_orders WHERE tenant_id = $1 AND kind = 'order'
				AND status IN ('received', 'awaiting_payment', 'paid', 'accepted')`, p.TenantID).Scan(&shop); err != nil {
				return err
			}
			n += shop
			out.Open = &n
			if s.Dispatch.Ready() {
				ds, err := s.Dispatch.ListTx(ctx, tx, p.TenantID, dispatch.ListFilter{Day: out.Day})
				if err != nil {
					return err
				}
				var d handoverDelivery
				for _, x := range ds {
					switch x.Status {
					case "delivered":
						d.Done++
					case "accepted", "picked_up", "arrived", "offered":
						d.Out++
					}
				}
				out.Out = &d
			}
		}
		return nil
	})
	if err != nil {
		return out, err
	}
	if s.Stock.Ready() {
		if items, err := s.alertStockItems(ctx, p.TenantID); err == nil {
			for _, it := range items {
				if it.Low() {
					out.Low = append(out.Low, it.Name)
				}
			}
		}
	}
	return out, nil
}

// getChatHandover is GET /v1/chat-handover: the shift so far.
func (s *Server) getChatHandover(w http.ResponseWriter, r *http.Request) {
	out, err := s.handoverOf(r.Context(), principalFrom(r.Context()))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// handoverWords is a handover told: the sales and how paid, the cash
// counted against the cash sold, what is open and low, and the note.
func handoverWords(who string, h handover, cash *int64, note string) string {
	lines := []string{fmt.Sprintf("%s handed the shift over.", whatsapp.SafeText(who))}
	if h.Sales != nil {
		lines = append(lines, fmt.Sprintf("Sold %s in %d sales", naira(h.Sales.TotalKobo), h.Sales.Count))
		var hows []string
		for _, how := range []string{"cash", "transfer", "pos", "credit"} {
			if v := h.Sales.ByHow[how]; v > 0 {
				name := map[string]string{"cash": "cash", "transfer": "transfer", "pos": "POS", "credit": "on credit"}[how]
				hows = append(hows, fmt.Sprintf("%s %s", naira(v), name))
			}
		}
		if len(hows) > 0 {
			lines = append(lines, strings.Join(hows, " · "))
		}
		if cash != nil {
			want := h.Sales.ByHow["cash"]
			switch {
			case *cash == want:
				lines = append(lines, fmt.Sprintf("Cash counted: %s, as sold", naira(*cash)))
			case *cash < want:
				lines = append(lines, fmt.Sprintf("Cash counted: %s, %s short of the %s sold", naira(*cash), naira(want-*cash), naira(want)))
			default:
				lines = append(lines, fmt.Sprintf("Cash counted: %s, %s over the %s sold", naira(*cash), naira(*cash-want), naira(want)))
			}
		}
	}
	if h.Open != nil && *h.Open > 0 {
		lines = append(lines, fmt.Sprintf("%d orders still open", *h.Open))
	}
	if h.Out != nil && h.Out.Out > 0 {
		lines = append(lines, fmt.Sprintf("%d deliveries on the way", h.Out.Out))
	}
	if len(h.Low) > 0 {
		low := h.Low
		if len(low) > 5 {
			low = append(slices.Clone(low[:5]), fmt.Sprintf("%d more", len(h.Low)-5))
		}
		lines = append(lines, "Low: "+whatsapp.SafeText(strings.Join(low, ", ")))
	}
	if note = strings.TrimSpace(note); note != "" {
		lines = append(lines, "Note: "+whatsapp.SafeText(note))
	}
	return strings.Join(lines, "\n")
}

// postChatHandover is POST /v1/chat-handover {cash_counted_kobo, note}:
// the shift handed over, told to those who run the business (the bell
// and their chat), and a diary line for the person.
func (s *Server) postChatHandover(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r.Context())
	if p.UserID == uuid.Nil {
		s.fail(w, r, errNoPerson)
		return
	}
	var in struct {
		Cash *int64 `json:"cash_counted_kobo"`
		Note string `json:"note"`
	}
	if err := decodeBody(r, &in); err != nil {
		s.fail(w, r, err)
		return
	}
	if in.Cash != nil && *in.Cash < 0 || len([]rune(in.Note)) > 300 {
		s.fail(w, r, fmt.Errorf("%w: the cash counted is 0 or more, the note 300 characters at most", errBadRequest))
		return
	}
	// A handover is a shift manager's (or the owner's): those who run the
	// shift's money or the storefront, not every member who reads stock.
	if !p.Can(PermInvoiceManage) && !p.Can(PermStorefrontManage) {
		s.fail(w, r, fmt.Errorf("%w: handing a shift over is a shift manager's", errForbidden))
		return
	}
	var on bool
	if err := s.tx(r, func(tx pgx.Tx) error {
		var err error
		on, err = chatToolsOn(r.Context(), tx, p.TenantID)
		return err
	}); err != nil {
		s.fail(w, r, err)
		return
	}
	if !on {
		s.fail(w, r, fmt.Errorf("%w: chat tools are off", errConflict))
		return
	}
	if !s.limiter("handover:"+p.UserID.String(), 10*time.Minute, 3).Allow() {
		s.fail(w, r, fmt.Errorf("%w: the shift was handed over a moment ago", errConflict))
		return
	}
	h, err := s.handoverOf(r.Context(), p)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	who := "Someone"
	if err := s.tx(r, func(tx pgx.Tx) error {
		var name, email string
		if err := tx.QueryRow(r.Context(), `SELECT name, email FROM users WHERE id = $1`, p.UserID).Scan(&name, &email); err != nil {
			return err
		}
		if who = strings.TrimSpace(name); who == "" {
			who, _, _ = strings.Cut(email, "@")
		}
		var sold int64
		if h.Sales != nil {
			sold = h.Sales.TotalKobo
		}
		return auditTx(r, tx, "chat.handover", h.Day, map[string]any{"cash_counted_kobo": in.Cash, "sales_kobo": sold})
	}); err != nil {
		s.fail(w, r, err)
		return
	}
	words := handoverWords(who, h, in.Cash, in.Note)
	title, body, _ := strings.Cut(words, "\n")
	s.background(withoutUser(r.Context(), p.UserID), "handover", func(ctx context.Context) {
		s.tellHolders(ctx, p.TenantID, []string{PermStorefrontManage + "|" + PermInvoiceManage}, "handover:"+uuid.NewString(), "/chat?with=taskiem",
			func(_ lang.Tag) (string, string) { return title, body })
	})
	line := "Handed the shift over"
	if h.Sales != nil {
		line += ": " + naira(h.Sales.TotalKobo) + " sold"
	}
	s.diaryChange(r.Context(), p, line)
	writeJSON(w, http.StatusOK, map[string]any{"handover": h, "words": words})
}

// howDidWeDo is "how did we do?" and its like: the period it asks about.
var howDidWeDo = regexp.MustCompile(`^(?:@taskiem\s+)?(?:how did we do|how are we doing|how is business|how's business|sales)(?:\s+(today|yesterday|this week|last week|this month))?\s*\??$`)

// salesChart is the chart card's data, kept in the Taskiem conversation
// as a message of kind chart.
type salesChart struct {
	Title     string     `json:"title"`
	TotalKobo int64      `json:"total_kobo"`
	ChangePct *int       `json:"change_pct"`
	Than      string     `json:"than"` // what the change is against
	Bars      []daySales `json:"bars"`
	// Highlight is the bar the question is about (today, yesterday), or
	// -1 for the whole period.
	Highlight int `json:"highlight"`
}

// chartFor is the chart a period asks for, ending on today.
func chartFor(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, period string, today time.Time) (salesChart, error) {
	switch period {
	case "today", "yesterday":
		last := today
		if period == "yesterday" {
			last = today.AddDate(0, 0, -1)
		}
		bars, err := salesDays(ctx, tx, tenant, last, 7)
		if err != nil {
			return salesChart{}, err
		}
		before, err := stock.DayTx(ctx, tx, tenant, last.AddDate(0, 0, -7))
		if err != nil {
			return salesChart{}, err
		}
		v := bars[len(bars)-1].Kobo
		title := "Sales today"
		if period == "yesterday" {
			title = "Sales yesterday"
		}
		return salesChart{Title: title, TotalKobo: v, ChangePct: changePct(v, before.TotalKobo), Than: "last " + last.Format("Monday"), Bars: bars, Highlight: len(bars) - 1}, nil
	case "this month":
		n := today.Day()
		bars, err := salesDays(ctx, tx, tenant, today, n)
		if err != nil {
			return salesChart{}, err
		}
		var total int64
		for _, b := range bars {
			total += b.Kobo
		}
		// The same days of last month, to its last day at most.
		lastMonth := time.Date(today.Year(), today.Month(), 0, 0, 0, 0, 0, today.Location())
		before, err := salesDays(ctx, tx, tenant, lastMonth.AddDate(0, 0, min(n, lastMonth.Day())-lastMonth.Day()), min(n, lastMonth.Day()))
		if err != nil {
			return salesChart{}, err
		}
		var prev int64
		for _, b := range before {
			prev += b.Kobo
		}
		for i := range bars {
			bars[i].Label = bars[i].Day[8:]
		}
		return salesChart{Title: "Sales this month", TotalKobo: total, ChangePct: changePct(total, prev), Than: "the same days last month", Bars: bars, Highlight: -1}, nil
	}
	last := today
	title := "Sales, the last 7 days"
	if period == "last week" {
		last = today.AddDate(0, 0, -7)
		title = "Sales, the 7 days before"
	}
	bars, err := salesDays(ctx, tx, tenant, last, 7)
	if err != nil {
		return salesChart{}, err
	}
	before, err := salesDays(ctx, tx, tenant, last.AddDate(0, 0, -7), 7)
	if err != nil {
		return salesChart{}, err
	}
	var total, prev int64
	for i := range bars {
		total += bars[i].Kobo
		prev += before[i].Kobo
	}
	return salesChart{Title: title, TotalKobo: total, ChangePct: changePct(total, prev), Than: "the week before", Bars: bars, Highlight: -1}, nil
}

// chartWords is a chart in words, for the chat apps.
func chartWords(c salesChart) string {
	words := fmt.Sprintf("%s: %s", c.Title, naira(c.TotalKobo))
	if c.ChangePct != nil {
		switch {
		case *c.ChangePct > 0:
			words += fmt.Sprintf(", up %d%% on %s", *c.ChangePct, c.Than)
		case *c.ChangePct < 0:
			words += fmt.Sprintf(", down %d%% on %s", -*c.ChangePct, c.Than)
		default:
			words += ", the same as " + c.Than
		}
	}
	var days []string
	if c.Highlight < 0 && len(c.Bars) <= 7 {
		for _, b := range c.Bars {
			days = append(days, fmt.Sprintf("%s %s", b.Label, naira(b.Kobo)))
		}
		words += "\n" + strings.Join(days, " · ")
	}
	return words
}

// waHowDidWeDo answers "how did we do this week?" (and "@Taskiem how did
// we do?" from a business's conversation): the numbers in words on every
// channel, and a chart card in the Taskiem conversation.
func (s *Server) waHowDidWeDo(ctx context.Context, c *chat, cmd string) (bool, error) {
	m := howDidWeDo.FindStringSubmatch(cmd)
	if m == nil || c.p == nil || c.state != stateIdle || !c.p.Can(PermTableRead) || !s.Stock.Ready() {
		return false, nil
	}
	period := m[1]
	if period == "" {
		period = "this week"
	}
	var chart salesChart
	if err := chatTx(ctx, s, c, func(tx pgx.Tx) error {
		var err error
		chart, err = chartFor(ctx, tx, c.tenant.ID, period, lagosToday(time.Now()))
		return err
	}); err != nil {
		return true, err
	}
	if err := c.say(ctx, s, chartWords(chart)); err != nil {
		return true, err
	}
	if c.tools {
		if b, err := json.Marshal(chart); err == nil {
			s.keepHistory(ctx, c.tenant.ID, c.p.UserID, "web", false, "chart", string(b), nil)
		}
	}
	return true, nil
}
