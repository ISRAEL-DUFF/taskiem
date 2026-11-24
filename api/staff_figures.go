package api

// One page per person (docs/spec/chat-five-more-flows.md, phase 3 plan,
// step 5): for a day, a week or a month, the hours they worked (and the
// days they were late or did not come), the sales they recorded and how
// they were paid, their handovers with the cash counted against the cash
// sold, the orders they worked, and their tasks. Owners and managers see
// anyone's; a person sees their own, and asks for it in chat ("my week").

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/storefront"
)

type figureHandover struct {
	At      time.Time `json:"at"`
	Counted *int64    `json:"counted_kobo,omitempty"`
	Cash    *int64    `json:"cash_kobo,omitempty"`
	// Diff is counted less the cash sold: below 0 short, above 0 over.
	Diff *int64 `json:"diff_kobo,omitempty"`
}

type staffFigures struct {
	Period  string   `json:"period"` // day, week or month
	From    string   `json:"from"`   // the first day (Lagos)
	To      string   `json:"to"`     // the last day, today at most
	Minutes int      `json:"minutes"`
	Shifts  int      `json:"shifts"`
	Late    []string `json:"late_days"`   // days they clocked in late
	Missed  []string `json:"missed_days"` // days they were due and never clocked in
	Sales   struct {
		Count int              `json:"count"`
		Total int64            `json:"total_kobo"`
		ByHow map[string]int64 `json:"by_how"`
	} `json:"sales"`
	Handovers []figureHandover `json:"handovers"`
	Orders    int              `json:"orders"`
	Tasks     struct {
		Done     int `json:"done"`
		DoneLate int `json:"done_late"`
		Cant     int `json:"cant"`
		Open     int `json:"open"`
		Late     int `json:"late"`
	} `json:"tasks"`
}

// periodFrom is the first day of the period ending today (Lagos): today,
// this week (from Monday), or this month.
func periodFrom(period string, now time.Time) (time.Time, bool) {
	d := now.In(storefront.Lagos)
	today := time.Date(d.Year(), d.Month(), d.Day(), 0, 0, 0, 0, storefront.Lagos)
	switch period {
	case "day", "":
		return today, true
	case "week":
		return today.AddDate(0, 0, -((int(today.Weekday()) + 6) % 7)), true
	case "month":
		return time.Date(d.Year(), d.Month(), 1, 0, 0, 0, 0, storefront.Lagos), true
	}
	return time.Time{}, false
}

func staffFiguresTx(ctx context.Context, tx pgx.Tx, tenant, user uuid.UUID, period string, now time.Time) (staffFigures, error) {
	var f staffFigures
	from, ok := periodFrom(period, now)
	if !ok {
		return f, fmt.Errorf("%w: the period is day, week or month", errBadRequest)
	}
	if period == "" {
		period = "day"
	}
	d := now.In(storefront.Lagos)
	end := time.Date(d.Year(), d.Month(), d.Day(), 0, 0, 0, 0, storefront.Lagos).AddDate(0, 0, 1)
	f.Period, f.From, f.To = period, from.Format("2006-01-02"), end.AddDate(0, 0, -1).Format("2006-01-02")
	f.Late, f.Missed, f.Handovers = []string{}, []string{}, []figureHandover{}
	f.Sales.ByHow = map[string]int64{}

	// Hours: each shift's time within the period, an open one up to now.
	rows, err := tx.Query(ctx, `SELECT in_at, COALESCE(out_at, $4) FROM staff_clock
		WHERE tenant_id = $1 AND user_id = $2 AND in_at < $5 AND COALESCE(out_at, $4) > $3 ORDER BY in_at`, tenant, user, from, now, end)
	if err != nil {
		return f, err
	}
	type span struct{ in, out time.Time }
	spans, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (span, error) {
		var x span
		return x, r.Scan(&x.in, &x.out)
	})
	if err != nil {
		return f, err
	}
	var total time.Duration
	firstIn := map[string]time.Time{}
	for _, x := range spans {
		in, out := x.in, x.out
		if in.Before(from) {
			in = from
		}
		if out.After(now) {
			out = now
		}
		if out.After(in) {
			total += out.Sub(in)
		}
		day := x.in.In(storefront.Lagos).Format("2006-01-02")
		if t, ok := firstIn[day]; !ok || x.in.Before(t) {
			firstIn[day] = x.in
		}
	}
	f.Minutes, f.Shifts = int(total.Minutes()), len(spans)

	// Late and missed days, by their usual hours.
	var rota []rotaShift
	var raw []byte
	if err := tx.QueryRow(ctx, `SELECT COALESCE((SELECT rota FROM staff_profiles WHERE tenant_id = $1 AND user_id = $2), '[]')`, tenant, user).Scan(&raw); err != nil {
		return f, err
	}
	if err := json.Unmarshal(raw, &rota); err != nil {
		return f, err
	}
	// Not before they joined, or the day they started when that is later.
	var joined time.Time
	var started *time.Time
	if err := tx.QueryRow(ctx, `SELECT min(m.created_at), (SELECT started_on::timestamp FROM staff_profiles WHERE tenant_id = $1 AND user_id = $2)
		FROM memberships m WHERE m.tenant_id = $1 AND m.user_id = $2`, tenant, user).Scan(&joined, &started); err != nil {
		return f, err
	}
	first := joined.In(storefront.Lagos)
	first = time.Date(first.Year(), first.Month(), first.Day(), 0, 0, 0, 0, storefront.Lagos)
	if started != nil {
		if st := time.Date(started.Year(), started.Month(), started.Day(), 0, 0, 0, 0, storefront.Lagos); st.After(first) {
			first = st
		}
	}
	for day := from; day.Before(end); day = day.AddDate(0, 0, 1) {
		if day.Before(first) {
			continue
		}
		r, ok := rotaShiftOn(rota, day)
		if !ok {
			continue
		}
		start, _ := shiftOn(day, r)
		key := day.Format("2006-01-02")
		in, came := firstIn[key]
		switch {
		case came && in.After(start.Add(staffLateAfter)):
			f.Late = append(f.Late, key)
		case !came && now.After(start.Add(staffLateUntil)):
			f.Missed = append(f.Missed, key)
		}
	}

	// Sales they recorded, by how they were paid.
	rows, err = tx.Query(ctx, `SELECT paid_how, count(*), COALESCE(sum(amount_kobo), 0) FROM stock_sales
		WHERE tenant_id = $1 AND actor = $2 AND sold_at >= $3 AND sold_at < $4 GROUP BY paid_how`, tenant, user.String(), from, end)
	if err != nil {
		return f, err
	}
	for rows.Next() {
		var how string
		var n int
		var kobo int64
		if err := rows.Scan(&how, &n, &kobo); err != nil {
			rows.Close()
			return f, err
		}
		f.Sales.Count += n
		f.Sales.Total += kobo
		f.Sales.ByHow[how] = kobo
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return f, err
	}

	// Handovers, and the orders they worked, from what they did.
	rows, err = tx.Query(ctx, `SELECT at, (detail->>'cash_counted_kobo')::bigint, (detail->>'cash_kobo')::bigint FROM audit_log
		WHERE tenant_id = $1 AND actor_id = $2 AND action = 'chat.handover' AND at >= $3 AND at < $4 ORDER BY at`, tenant, user.String(), from, end)
	if err != nil {
		return f, err
	}
	if f.Handovers, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (figureHandover, error) {
		var h figureHandover
		if err := r.Scan(&h.At, &h.Counted, &h.Cash); err != nil {
			return h, err
		}
		if h.Counted != nil && h.Cash != nil {
			d := *h.Counted - *h.Cash
			h.Diff = &d
		}
		return h, nil
	}); err != nil {
		return f, err
	}
	if err := tx.QueryRow(ctx, `SELECT count(DISTINCT target) FROM audit_log
		WHERE tenant_id = $1 AND actor_id = $2 AND at >= $3 AND at < $4
		  AND (action LIKE 'storefront.order%' OR action LIKE 'trade_order.%')`, tenant, user.String(), from, end).Scan(&f.Orders); err != nil {
		return f, err
	}

	// Tasks: done (and done late) and sent back in the period; open now.
	err = tx.QueryRow(ctx, `SELECT
		count(*) FILTER (WHERE state = 'done' AND done_by = $2 AND done_at >= $3 AND done_at < $4),
		count(*) FILTER (WHERE state = 'done' AND done_by = $2 AND done_at >= $3 AND done_at < $4 AND due_at IS NOT NULL AND done_at > due_at),
		count(*) FILTER (WHERE state = 'cant' AND COALESCE(taken_by, to_user) = $2 AND updated_at >= $3 AND updated_at < $4),
		count(*) FILTER (WHERE state = 'open' AND COALESCE(taken_by, to_user) = $2),
		count(*) FILTER (WHERE state = 'open' AND COALESCE(taken_by, to_user) = $2 AND due_at < $5)
		FROM staff_tasks WHERE tenant_id = $1`, tenant, user, from, end, now).Scan(&f.Tasks.Done, &f.Tasks.DoneLate, &f.Tasks.Cant, &f.Tasks.Open, &f.Tasks.Late)
	return f, err
}

// getStaffFigures is GET /v1/staff/{user}/figures?period=day|week|month:
// anyone's for those who run the business; one's own for anyone.
func (s *Server) getStaffFigures(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r.Context())
	user, err := uuid.Parse(chi.URLParam(r, "user"))
	if err != nil {
		s.fail(w, r, pgx.ErrNoRows)
		return
	}
	if user != p.UserID && !p.Can(PermTaskGive) {
		s.fail(w, r, fmt.Errorf("%w: only those who run the business see someone else's figures", errForbidden))
		return
	}
	var f staffFigures
	if err := s.tx(r, func(tx pgx.Tx) error {
		var member bool
		if err := tx.QueryRow(r.Context(), `SELECT EXISTS (SELECT 1 FROM memberships WHERE tenant_id = $1 AND user_id = $2)`, p.TenantID, user).Scan(&member); err != nil {
			return err
		}
		if !member {
			return pgx.ErrNoRows
		}
		f, err = staffFiguresTx(r.Context(), tx, p.TenantID, user, r.URL.Query().Get("period"), s.staffNow())
		return err
	}); err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, f)
}

// figuresWords is the figures in chat: "Your week (from Mon 9 Nov): …".
func figuresWords(f staffFigures) string {
	name := map[string]string{"day": "Today", "week": "Your week", "month": "Your month"}[f.Period]
	from, _ := time.ParseInLocation("2006-01-02", f.From, storefront.Lagos)
	head := name
	if f.Period != "day" {
		head += " (from " + from.Format("Mon 2 Jan") + ")"
	}
	lines := []string{head + ":"}
	if f.Shifts == 0 {
		lines = append(lines, "Hours: none clocked.")
	} else {
		l := fmt.Sprintf("Hours: %s over %s", hoursWords(time.Duration(f.Minutes)*time.Minute), plural(f.Shifts, "shift"))
		if n := len(f.Late); n > 0 {
			l += ", late " + timesWords(n)
		}
		lines = append(lines, l+".")
	}
	if n := len(f.Missed); n > 0 {
		lines = append(lines, fmt.Sprintf("Did not clock in on %s you were due.", plural(n, "day")))
	}
	if f.Sales.Count > 0 {
		var how []string
		for _, k := range []string{"cash", "transfer", "pos"} {
			if v, ok := f.Sales.ByHow[k]; ok {
				how = append(how, map[string]string{"cash": "cash", "transfer": "transfer", "pos": "POS"}[k]+" "+naira(v))
			}
		}
		lines = append(lines, fmt.Sprintf("Sales: %s in %s (%s).", naira(f.Sales.Total), plural(f.Sales.Count, "sale"), strings.Join(how, ", ")))
	}
	if n := len(f.Handovers); n > 0 {
		var diff int64
		known := false
		for _, h := range f.Handovers {
			if h.Diff != nil {
				diff, known = diff+*h.Diff, true
			}
		}
		l := "Handovers: " + fmt.Sprint(n)
		switch {
		case !known:
			// Handed over before the cash sold was kept with it.
		case diff < 0:
			l += ", " + naira(-diff) + " short"
		case diff > 0:
			l += ", " + naira(diff) + " over"
		default:
			l += ", the cash right"
		}
		lines = append(lines, l+".")
	}
	if f.Orders > 0 {
		lines = append(lines, "Orders worked: "+fmt.Sprint(f.Orders)+".")
	}
	t := f.Tasks
	if t.Done+t.Open+t.Cant > 0 {
		var parts []string
		if t.Done > 0 {
			d := fmt.Sprintf("%d done", t.Done)
			if t.DoneLate > 0 {
				d += fmt.Sprintf(" (%d late)", t.DoneLate)
			}
			parts = append(parts, d)
		}
		if t.Open > 0 {
			o := fmt.Sprintf("%d open", t.Open)
			if t.Late > 0 {
				o += fmt.Sprintf(" (%d late)", t.Late)
			}
			parts = append(parts, o)
		}
		if t.Cant > 0 {
			parts = append(parts, fmt.Sprintf("%d sent back", t.Cant))
		}
		lines = append(lines, "Tasks: "+strings.Join(parts, ", ")+".")
	}
	return strings.Join(lines, "\n")
}

func timesWords(n int) string {
	switch n {
	case 1:
		return "once"
	case 2:
		return "twice"
	}
	return fmt.Sprintf("%d times", n)
}

var myFiguresWords = map[string]string{
	"my day": "day", "my today": "day", "my week": "week", "my month": "month",
	"how did i do": "week", "how did i do this week": "week", "how did i do today": "day", "how did i do this month": "month",
	"my hours": "week", "my figures": "week",
}

// waMyFigures answers "my week", "my day", "my month"; false for other words.
func (s *Server) waMyFigures(ctx context.Context, c *chat, cmd string) (bool, error) {
	period, ok := myFiguresWords[strings.TrimRight(strings.TrimSpace(cmd), "?.!")]
	if !ok || c.p == nil || c.tenant.ID == uuid.Nil || c.p.UserID == uuid.Nil {
		return false, nil
	}
	var f staffFigures
	if err := chatTx(ctx, s, c, func(tx pgx.Tx) error {
		var err error
		f, err = staffFiguresTx(ctx, tx, c.tenant.ID, c.p.UserID, period, s.staffNow())
		return err
	}); err != nil {
		return true, err
	}
	return true, c.say(ctx, s, figuresWords(f))
}
