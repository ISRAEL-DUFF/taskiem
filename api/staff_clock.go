package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/lang"
	"github.com/israel-duff/taskiem/engine/storefront"
)

// Clocking in and out (docs/spec/chat-five-more-flows.md, phase 3 plan,
// step 3). Anyone of a business says "in" and "out" in chat (WhatsApp,
// Telegram, the Taskiem chat); a shift manager's handover clocks them out;
// a location shared on WhatsApp goes with the clock-in. Measured against
// their usual hours (staff_profile.go): 15 minutes after a shift should
// start with no "in", those who run the business are told once; a shift
// left open 4 hours past its end is closed at its end ("closed by
// Taskiem"), one with no usual hours after 16 hours.

const (
	staffLateAfter   = 15 * time.Minute
	staffLateUntil   = 3 * time.Hour  // a start this long ago is not told late any more
	staffAutoClose   = 4 * time.Hour  // past a shift's usual end
	staffNoRotaClose = 16 * time.Hour // open this long with no usual hours
)

// staffClock is one shift, from in to out.
type staffClock struct {
	ID     uuid.UUID  `json:"id"`
	User   uuid.UUID  `json:"user_id"`
	Name   string     `json:"name,omitempty"`
	In     time.Time  `json:"in_at"`
	Out    *time.Time `json:"out_at,omitempty"`
	OutHow string     `json:"out_how,omitempty"`
	Where  string     `json:"in_where,omitempty"`
}

// shiftOn is a rota day's shift on a date (Lagos): its start and end, an
// end before the start being the next day.
func shiftOn(day time.Time, r rotaShift) (time.Time, time.Time) {
	at := func(hm string) time.Time {
		t, _ := time.ParseInLocation("15:04", hm, storefront.Lagos)
		return time.Date(day.Year(), day.Month(), day.Day(), t.Hour(), t.Minute(), 0, 0, storefront.Lagos)
	}
	start, end := at(r.Start), at(r.End)
	if !end.After(start) {
		end = end.AddDate(0, 0, 1)
	}
	return start, end
}

// rotaDayOf is a date's rota day ("mon").
func rotaDayOf(t time.Time) string { return rotaDays[(int(t.Weekday())+6)%7] }

// rotaShiftOn is a person's usual shift on a date, if they have one.
func rotaShiftOn(rota []rotaShift, day time.Time) (rotaShift, bool) {
	i := slices.IndexFunc(rota, func(r rotaShift) bool { return r.Day == rotaDayOf(day) })
	if i < 0 {
		return rotaShift{}, false
	}
	return rota[i], true
}

// openClockTx is a person's open shift, or nil.
func openClockTx(ctx context.Context, tx pgx.Tx, tenant, user uuid.UUID) (*staffClock, error) {
	var c staffClock
	err := tx.QueryRow(ctx, `SELECT id, user_id, in_at, COALESCE(in_where, '') FROM staff_clock WHERE tenant_id = $1 AND user_id = $2 AND out_at IS NULL FOR UPDATE`, tenant, user).
		Scan(&c.ID, &c.User, &c.In, &c.Where)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return &c, err
}

// clockInTx opens a shift, or answers the one already open (already).
func clockInTx(ctx context.Context, tx pgx.Tx, tenant, user uuid.UUID, how, where string, at time.Time) (*staffClock, bool, error) {
	open, err := openClockTx(ctx, tx, tenant, user)
	if err != nil || open != nil {
		return open, open != nil, err
	}
	c := &staffClock{ID: uuid.Must(uuid.NewV7()), User: user, In: at, Where: where}
	_, err = tx.Exec(ctx, `INSERT INTO staff_clock (id, tenant_id, user_id, in_at, in_how, in_where) VALUES ($1, $2, $3, $4, $5, NULLIF($6, ''))`,
		c.ID, tenant, user, at, how, where)
	return c, false, err
}

// clockOutTx closes a person's open shift at at; nil when none was open.
func clockOutTx(ctx context.Context, tx pgx.Tx, tenant, user uuid.UUID, how string, at time.Time) (*staffClock, error) {
	open, err := openClockTx(ctx, tx, tenant, user)
	if err != nil || open == nil {
		return nil, err
	}
	if at.Before(open.In) {
		at = open.In
	}
	if _, err := tx.Exec(ctx, `UPDATE staff_clock SET out_at = $2, out_how = $3 WHERE id = $1`, open.ID, at, how); err != nil {
		return nil, err
	}
	open.Out, open.OutHow = &at, how
	return open, nil
}

// hoursWords is a length of time as people say it: "8 hours 3 minutes".
func hoursWords(d time.Duration) string {
	d = d.Round(time.Minute)
	h, m := int(d.Hours()), int(d.Minutes())%60
	switch {
	case h == 0:
		return plural(m, "minute")
	case m == 0:
		return plural(h, "hour")
	}
	return plural(h, "hour") + " " + plural(m, "minute")
}

func plural(n int, one string) string {
	if n == 1 {
		return "1 " + one
	}
	return fmt.Sprintf("%d %ss", n, one)
}

// clockAt is a time as a clock-in reply says it: "08:02".
func clockAt(t time.Time) string { return t.In(storefront.Lagos).Format("15:04") }

// --- in chat ---

// staffClockWords are what clocks in and out, said alone.
var (
	clockInWords  = []string{"in", "clock in", "clocking in", "i'm in", "im in", "i am in", "start shift", "start my shift"}
	clockOutWords = []string{"out", "clock out", "clocking out", "i'm out", "im out", "i am out", "end shift", "end my shift"}
	whoInWords    = []string{"who is in", "who's in", "whos in", "who is at work", "who is working"}
)

// waStaffClock answers "in", "out" and "who is in"; false for other words.
func (s *Server) waStaffClock(ctx context.Context, c *chat, cmd string) (bool, error) {
	cmd = strings.TrimRight(strings.TrimSpace(cmd), ".!?")
	switch {
	case slices.Contains(clockInWords, cmd):
		return true, s.waClock(ctx, c, true, "")
	case slices.Contains(clockOutWords, cmd):
		return true, s.waClock(ctx, c, false, "")
	case slices.Contains(whoInWords, cmd):
		return true, s.waWhoIsIn(ctx, c)
	}
	return false, nil
}

// waClock clocks the person in (with where, a maps link, when shared) or out.
func (s *Server) waClock(ctx context.Context, c *chat, in bool, where string) error {
	now := s.staffNow()
	how := "chat"
	if c.ch.Name() == "web" {
		how = "web"
	}
	var reply string
	err := chatTx(ctx, s, c, func(tx pgx.Tx) error {
		if in {
			open, already, err := clockInTx(ctx, tx, c.tenant.ID, c.p.UserID, how, where, now)
			if err != nil {
				return err
			}
			switch {
			case already && where != "" && open.Where == "":
				if _, err := tx.Exec(ctx, `UPDATE staff_clock SET in_where = $2 WHERE id = $1`, open.ID, where); err != nil {
					return err
				}
				reply = "Your location is with today's clock-in."
			case already:
				reply = fmt.Sprintf("You clocked in at %s. Send out when you finish.", clockAt(open.In))
			default:
				reply = fmt.Sprintf("Clocked in at %s. Have a good shift. Send out when you finish.", clockAt(now))
			}
			return nil
		}
		closed, err := clockOutTx(ctx, tx, c.tenant.ID, c.p.UserID, how, now)
		if err != nil {
			return err
		}
		if closed == nil {
			reply = "You are not clocked in. Send in when you start."
			return nil
		}
		reply = fmt.Sprintf("Clocked out at %s: %s today. Thank you.", clockAt(now), hoursWords(now.Sub(closed.In)))
		return nil
	})
	if err != nil {
		return err
	}
	return c.say(ctx, s, reply)
}

// clockPlace takes a location a person of a business shared: today's
// clock-in carries it, or it clocks them in. false when it is not theirs
// to take (no business, or they cannot be clocked).
func (s *Server) clockPlace(ctx context.Context, c *chat) (bool, error) {
	if c.in.Location == "" || c.tenant.ID == uuid.Nil || c.p == nil || c.p.UserID == uuid.Nil {
		return false, nil
	}
	return true, s.waClock(ctx, c, true, clip(c.in.Location, 200))
}

// waWhoIsIn is "who is in": for those who run the business.
func (s *Server) waWhoIsIn(ctx context.Context, c *chat) error {
	if !c.p.Can(PermMemberManage + "|" + PermInvoiceManage) {
		return c.say(ctx, s, "Only those who run the business see who is in.")
	}
	var now staffNow
	if err := chatTx(ctx, s, c, func(tx pgx.Tx) error {
		var err error
		now, err = staffNowTx(ctx, tx, c.tenant.ID, s.staffNow())
		return err
	}); err != nil {
		return err
	}
	return c.say(ctx, s, now.words())
}

// staffNow is who is in now and who is late, for the briefing, "who is
// in" and the Staff page.
type staffNow struct {
	In   []staffClock `json:"in"`
	Late []staffLate  `json:"late"`
}

// staffLate is someone whose shift should have started and who has not
// clocked in.
type staffLate struct {
	User uuid.UUID `json:"user_id"`
	Name string    `json:"name"`
	Due  string    `json:"due"` // "08:00"
}

func (n staffNow) words() string {
	if len(n.In) == 0 && len(n.Late) == 0 {
		return "No one is clocked in."
	}
	var lines []string
	if len(n.In) > 0 {
		var names []string
		for _, c := range n.In {
			names = append(names, c.Name+" (since "+clockAt(c.In)+")")
		}
		lines = append(lines, "In now: "+strings.Join(names, ", ")+".")
	}
	if len(n.Late) > 0 {
		var names []string
		for _, l := range n.Late {
			names = append(names, l.Name+" (due "+l.Due+")")
		}
		lines = append(lines, "Not in yet: "+strings.Join(names, ", ")+".")
	}
	return strings.Join(lines, "\n")
}

// staffPeopleRota is each member's name and usual hours.
type staffPeopleRota struct {
	User uuid.UUID
	Name string
	Rota []rotaShift
}

func staffRotasTx(ctx context.Context, tx pgx.Tx, tenant uuid.UUID) ([]staffPeopleRota, error) {
	rows, err := tx.Query(ctx, `SELECT u.id, CASE WHEN u.name = '' THEN 'Someone' ELSE u.name END, COALESCE(p.rota, '[]')
		FROM (SELECT DISTINCT user_id FROM memberships WHERE tenant_id = $1) m JOIN users u ON u.id = m.user_id
		LEFT JOIN staff_profiles p ON p.tenant_id = $1 AND p.user_id = u.id ORDER BY lower(u.name)`, tenant)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (staffPeopleRota, error) {
		var x staffPeopleRota
		var raw []byte
		if err := r.Scan(&x.User, &x.Name, &raw); err != nil {
			return x, err
		}
		return x, json.Unmarshal(raw, &x.Rota)
	})
}

// staffNowTx is who is in at now, and who is late (due 15 minutes ago or
// more, within three hours, and not in).
func staffNowTx(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, now time.Time) (staffNow, error) {
	out := staffNow{In: []staffClock{}, Late: []staffLate{}}
	people, err := staffRotasTx(ctx, tx, tenant)
	if err != nil {
		return out, err
	}
	rows, err := tx.Query(ctx, `SELECT id, user_id, in_at, COALESCE(in_where, '') FROM staff_clock WHERE tenant_id = $1 AND (out_at IS NULL OR in_at > $2)`, tenant, now.Add(-24*time.Hour))
	if err != nil {
		return out, err
	}
	recent, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (staffClock, error) {
		var c staffClock
		return c, r.Scan(&c.ID, &c.User, &c.In, &c.Where)
	})
	if err != nil {
		return out, err
	}
	var open map[uuid.UUID]bool
	if err := func() error {
		rows, err := tx.Query(ctx, `SELECT user_id FROM staff_clock WHERE tenant_id = $1 AND out_at IS NULL`, tenant)
		if err != nil {
			return err
		}
		ids, err := pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
		open = map[uuid.UUID]bool{}
		for _, id := range ids {
			open[id] = true
		}
		return err
	}(); err != nil {
		return out, err
	}
	today := now.In(storefront.Lagos)
	for _, p := range people {
		for _, c := range recent {
			if c.User == p.User && open[p.User] && c.Out == nil {
				c.Name = p.Name
				out.In = append(out.In, c)
				break
			}
		}
		if open[p.User] {
			continue
		}
		r, ok := rotaShiftOn(p.Rota, today)
		if !ok {
			continue
		}
		start, _ := shiftOn(today, r)
		if now.Before(start.Add(staffLateAfter)) || now.After(start.Add(staffLateUntil)) {
			continue
		}
		came := slices.ContainsFunc(recent, func(c staffClock) bool { return c.User == p.User && !c.In.Before(start.Add(-2*time.Hour)) })
		if !came {
			out.Late = append(out.Late, staffLate{User: p.User, Name: p.Name, Due: r.Start})
		}
	}
	return out, nil
}

// --- the job ---

// staffNow is the time the staff job and replies read (BriefingClock in tests).
func (s *Server) staffNow() time.Time {
	if s.BriefingClock != nil {
		return s.BriefingClock()
	}
	return time.Now()
}

// StaffTick does the staff job once: tells who is late, closes shifts left
// open. Run every minute.
func (s *Server) StaffTick(ctx context.Context) error {
	rows, err := s.Store.Pool.Query(ctx, `SELECT taskiem_staff_tenants()`)
	if err != nil {
		return err
	}
	tenants, err := pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
	if err != nil {
		return err
	}
	now := s.staffNow()
	for _, tenant := range tenants {
		if err := s.staffTickTenant(ctx, tenant, now); err != nil && ctx.Err() == nil {
			s.Logger.Warn("staff: the job", "tenant", tenant, "err", err)
		}
		if err := s.staffTaskTick(ctx, tenant, now); err != nil && ctx.Err() == nil {
			s.Logger.Warn("staff: the tasks job", "tenant", tenant, "err", err)
		}
	}
	return nil
}

func (s *Server) staffTickTenant(ctx context.Context, tenant uuid.UUID, now time.Time) error {
	var late []staffLate
	err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		people, err := staffRotasTx(ctx, tx, tenant)
		if err != nil {
			return err
		}
		// Shifts left open: closed at their usual end, or after 16 hours.
		rows, err := tx.Query(ctx, `SELECT id, user_id, in_at FROM staff_clock WHERE tenant_id = $1 AND out_at IS NULL`, tenant)
		if err != nil {
			return err
		}
		open, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (staffClock, error) {
			var c staffClock
			return c, r.Scan(&c.ID, &c.User, &c.In)
		})
		if err != nil {
			return err
		}
		for _, c := range open {
			end := c.In.Add(staffNoRotaClose)
			wait := time.Duration(0)
			i := slices.IndexFunc(people, func(p staffPeopleRota) bool { return p.User == c.User })
			if i >= 0 {
				day := c.In.In(storefront.Lagos)
				if r, ok := rotaShiftOn(people[i].Rota, day); ok {
					_, e := shiftOn(day, r)
					if e.After(c.In) {
						end, wait = e, staffAutoClose
					}
				}
			}
			if now.Before(end.Add(wait)) {
				continue
			}
			if _, err := tx.Exec(ctx, `UPDATE staff_clock SET out_at = $2, out_how = 'auto' WHERE id = $1 AND out_at IS NULL`, c.ID, end); err != nil {
				return err
			}
		}
		state, err := staffNowTx(ctx, tx, tenant, now)
		if err != nil {
			return err
		}
		day := now.In(storefront.Lagos).Format("2006-01-02")
		for _, l := range state.Late {
			tag, err := tx.Exec(ctx, `INSERT INTO staff_notices (tenant_id, key) VALUES ($1, $2) ON CONFLICT DO NOTHING`, tenant, "late:"+l.User.String()+":"+day)
			if err != nil {
				return err
			}
			if tag.RowsAffected() == 1 {
				late = append(late, l)
			}
		}
		_, err = tx.Exec(ctx, `DELETE FROM staff_notices WHERE tenant_id = $1 AND sent_at < now() - interval '30 days'`, tenant)
		return err
	})
	if err != nil {
		return err
	}
	for _, l := range late {
		l := l
		s.tellHolders(ctx, tenant, []string{PermMemberManage + "|" + PermInvoiceManage}, "late:"+l.User.String(), "/staff/"+l.User.String(),
			func(lang.Tag) (string, string) {
				return l.Name + " has not clocked in", "Due at " + l.Due + "."
			})
	}
	return nil
}

// --- the web ---

// staffClockDay is GET /v1/staff/clock?day=YYYY-MM-DD: the shifts of a
// day (Lagos; today when none), who is in now and who is late.
func (s *Server) staffClockDay(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r.Context())
	now := s.staffNow()
	day := now.In(storefront.Lagos)
	if q := r.URL.Query().Get("day"); q != "" {
		d, err := time.ParseInLocation("2006-01-02", q, storefront.Lagos)
		if err != nil {
			s.fail(w, r, fmt.Errorf("%w: day is a date, like 2026-11-09", errBadRequest))
			return
		}
		day = d
	}
	from := time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, storefront.Lagos)
	var out struct {
		Day    string       `json:"day"`
		Shifts []staffClock `json:"shifts"`
		staffNow
	}
	out.Day = from.Format("2006-01-02")
	if err := s.tx(r, func(tx pgx.Tx) error {
		rows, err := tx.Query(r.Context(), `SELECT c.id, c.user_id, CASE WHEN u.name = '' THEN 'Someone' ELSE u.name END, c.in_at, c.out_at, COALESCE(c.out_how, ''), COALESCE(c.in_where, '')
			FROM staff_clock c JOIN users u ON u.id = c.user_id
			WHERE c.tenant_id = $1 AND c.in_at >= $2 AND c.in_at < $3 ORDER BY c.in_at`, p.TenantID, from, from.AddDate(0, 0, 1))
		if err != nil {
			return err
		}
		if out.Shifts, err = pgx.CollectRows(rows, func(row pgx.CollectableRow) (staffClock, error) {
			var c staffClock
			return c, row.Scan(&c.ID, &c.User, &c.Name, &c.In, &c.Out, &c.OutHow, &c.Where)
		}); err != nil {
			return err
		}
		out.staffNow, err = staffNowTx(r.Context(), tx, p.TenantID, now)
		return err
	}); err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}
