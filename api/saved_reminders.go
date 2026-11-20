package api

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/chatlinks"
	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/notices"
	"github.com/israel-duff/taskiem/engine/saved"
	"github.com/israel-duff/taskiem/engine/whatsapp"
)

// Reminders from a saved thing's dates (saved objects S2; §6.3): "remind
// me when @car insurance is due" sets a reminder on @car's date field
// that the words name (by its label; a thing with one date needs none),
// told on that day in the morning (Lagos time) to the person who asked, in
// their Taskiem chat and in the app, and each year after unless they said
// "once". A person's birthday is a date too. "my reminders" lists them;
// "stop reminding me about @car" ends them. The date is read again from
// the field each time, so a renewed insurance moves its reminder; a thing
// removed takes its reminders with it (saved_reminders, migration 00499).

var (
	remindWords   = regexp.MustCompile(`(?i)^\s*remind\s+(?:me|us)\b.*@([a-z0-9][a-z0-9_-]{0,31})`)
	remindersList = regexp.MustCompile(`(?i)^\s*(?:my\s+)?reminders\s*\??\s*$`)
	remindStop    = regexp.MustCompile(`(?i)^\s*(?:stop|cancel)\s+(?:reminding\s+(?:me|us)|(?:the\s+)?reminders?)\s+(?:about|for|of|on)?\s*@([a-z0-9][a-z0-9_-]{0,31})`)
	remindOnce    = regexp.MustCompile(`(?i)\b(?:just|only)\s+once\b|\bthis\s+(?:time|year)\s+only\b`)
	// Words of a schedule ("every Friday", "at 9", "tomorrow"): a workflow
	// to build, not a date a saved thing holds.
	remindSchedule = regexp.MustCompile(`(?i)\b(?:every|each|daily|weekly|monthly|tomorrow|tonight|today|at\s+\d|in\s+\d+\s+(?:minutes?|hours?|days?|weeks?)|monday|tuesday|wednesday|thursday|friday|saturday|sunday)\b`)
	// Words that say a date is meant: due, a birthday, an end.
	remindDue = regexp.MustCompile(`(?i)\b(?:due|birthday|expires?|expiry|ends?|renew(?:al|s)?|anniversary)\b`)
)

// remindHour is when a reminder is told on its day, Lagos time.
const remindHour = 8

// savedDateField is a date a saved thing holds: its field's key, its label
// and its value (2027-03-03).
type savedDateField struct{ key, label, value string }

// savedDates are the dates x holds: a made-up kind's date fields, a
// person's birthday.
func savedDates(x savedRow) []savedDateField {
	var out []savedDateField
	switch x.Kind {
	case saved.KindCustom:
		values := x.view(savedOwner{}, false).Values
		for _, f := range x.Fields {
			if f.Type == "date" {
				out = append(out, savedDateField{f.Key, f.Label, values[f.Key]})
			}
		}
	case saved.KindPerson:
		v, _ := x.Details["birthday"].(string)
		out = append(out, savedDateField{"birthday", "Birthday", v})
	}
	return out
}

// nextOccurrence is the first day after `after` that a date falls on: the
// date itself, or (yearly) its day in a later year, never before the date
// itself; ok false when a date once has passed.
func nextOccurrence(v time.Time, yearly bool, after time.Time) (time.Time, bool) {
	if !yearly {
		return v, v.After(after)
	}
	d := time.Date(after.Year(), v.Month(), v.Day(), 0, 0, 0, 0, time.UTC)
	if !d.After(after) {
		// Built again for the next year, not added to: 29 Feb, a 1 Mar in
		// a year without one, is 29 Feb again in a year with one.
		d = time.Date(after.Year()+1, v.Month(), v.Day(), 0, 0, 0, 0, time.UTC)
	}
	if d.Before(v) {
		d = v
	}
	return d, true
}

// remindDay is the day it is in Lagos at now, as a date.
func remindDay(now time.Time) time.Time {
	l := now.In(lagosTZ)
	return time.Date(l.Year(), l.Month(), l.Day(), 0, 0, 0, 0, time.UTC)
}

// sayDay is a date as the chat says it: 3 Mar 2027.
func sayDay(d time.Time) string { return d.Format("2 Jan 2006") }

// waSavedReminders answers the reminder words; false when the words are
// none of them.
func (s *Server) waSavedReminders(ctx context.Context, c *chat, text string) (bool, error) {
	if c.p == nil || c.p.UserID == uuid.Nil || c.tenant.ID == uuid.Nil {
		return false, nil
	}
	switch {
	case remindersList.MatchString(text):
		return true, s.waRemindersList(ctx, c)
	case remindStop.MatchString(text):
		return true, s.waRemindStop(ctx, c, remindStop.FindStringSubmatch(text)[1])
	case remindWords.MatchString(text):
		return s.waRemindSet(ctx, c, text, remindWords.FindStringSubmatch(text)[1])
	}
	return false, nil
}

// remindOwner is the thing a reminder is on, among what @name names: the
// business's over one's own of the name.
func remindOwner(c *chat, got []savedFound) (savedRow, savedOwner) {
	f := got[0]
	for _, g := range got {
		if !g.mine {
			f = g
		}
	}
	if f.mine {
		return f.row, savedOwner{person: c.p.UserID, by: c.p.UserID}
	}
	return f.row, savedOwner{tenant: c.tenant.ID, by: c.p.UserID}
}

// waRemindSet sets a reminder on the date the words name.
func (s *Server) waRemindSet(ctx context.Context, c *chat, text, typed string) (bool, error) {
	name, err := saved.Name(typed)
	if err != nil {
		return false, nil //nolint:nilerr // not a saved thing's name: not this
	}
	// "remind me every Friday to pay @landlord" is a workflow to build.
	if remindSchedule.MatchString(text) {
		return false, nil
	}
	got, err := s.savedFind(ctx, c, name)
	if err != nil || len(got) == 0 {
		// No saved thing by the name: the words go on (a workflow, maybe).
		return err != nil, err
	}
	x, o := remindOwner(c, got)
	dates := savedDates(x)
	if len(dates) == 0 {
		return true, c.say(ctx, s, c.t("wa.remind.no_dates", "name", x.Name))
	}
	// The date the words name, by its label; the only one when they name none.
	asked := strings.ToLower(text)
	sort.SliceStable(dates, func(i, j int) bool { return len(dates[i].label) > len(dates[j].label) })
	var f *savedDateField
	for i := range dates {
		if labelAsked(asked, dates[i].label) {
			f = &dates[i]
			break
		}
	}
	if f == nil {
		f = dateNamed(asked, dates)
	}
	// Neither a date's label nor "due" in the words: not about its dates.
	if f == nil && !remindDue.MatchString(text) {
		return false, nil
	}
	if f == nil && len(dates) == 1 {
		f = &dates[0]
	}
	if f == nil {
		labels := make([]string, len(dates))
		for i, d := range dates {
			labels[i] = d.label
		}
		slices.Sort(labels)
		return true, c.say(ctx, s, c.t("wa.remind.which", "name", x.Name, "list", strings.Join(labels, ", ")))
	}
	if f.value == "" {
		return true, c.say(ctx, s, c.t("wa.saved.field_empty", "name", x.Name, "label", f.label))
	}
	v, err := time.Parse(time.DateOnly, f.value)
	if err != nil {
		return true, c.say(ctx, s, c.t("wa.saved.field_empty", "name", x.Name, "label", f.label))
	}
	yearly := !remindOnce.MatchString(text)
	today := remindDay(s.staffNow())
	on, ok := nextOccurrence(v, yearly, today.AddDate(0, 0, -1))
	if !ok {
		return true, c.say(ctx, s, c.t("wa.remind.passed", "name", x.Name, "label", f.label, "date", sayDay(v)))
	}
	err = s.savedTx(ctx, o, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO saved_reminders (id, person_id, tenant_id, object_id, field, remind_on, yearly, tell_tenant, tell_user)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
			ON CONFLICT (object_id, field, tell_user) DO UPDATE SET remind_on = EXCLUDED.remind_on, yearly = EXCLUDED.yearly,
				tell_tenant = EXCLUDED.tell_tenant,
				-- Set again: what was told already is not told again.
				last_sent = CASE WHEN saved_reminders.last_sent >= EXCLUDED.remind_on THEN saved_reminders.last_sent ELSE EXCLUDED.remind_on - 1 END`,
			uuid.Must(uuid.NewV7()), nullable(o.person), nullable(o.tenant), x.ID, f.key, on, yearly, c.tenant.ID, c.p.UserID)
		return err
	})
	if err != nil {
		return true, err
	}
	key := "wa.remind.set"
	if !yearly {
		key = "wa.remind.set_once"
	}
	return true, c.say(ctx, s, c.t(key, "name", x.Name, "label", f.label, "date", sayDay(on)))
}

// savedReminderView is one of the person's reminders, as the chat lists
// them.
type savedReminderView struct {
	name, label string
	on          time.Time
	yearly      bool
}

// remindersOf are the person's reminders on what they reach: their own,
// and their business's that they asked for.
func (s *Server) remindersOf(ctx context.Context, c *chat) ([]savedReminderView, error) {
	var out []savedReminderView
	read := func(o savedOwner) error {
		return s.savedTx(ctx, o, func(tx pgx.Tx) error {
			rows, err := tx.Query(ctx, `SELECT object_id, field, remind_on, yearly FROM saved_reminders WHERE tell_user = $1 AND `+map[bool]string{true: "person_id IS NOT NULL", false: "tenant_id IS NOT NULL"}[o.mine()], c.p.UserID)
			if err != nil {
				return err
			}
			type rem struct {
				object uuid.UUID
				v      savedReminderView
			}
			list, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (rem, error) {
				var x rem
				return x, r.Scan(&x.object, &x.v.label, &x.v.on, &x.v.yearly)
			})
			if err != nil {
				return err
			}
			for _, r := range list {
				x, err := savedRowTx(ctx, tx, o, r.object, false)
				if errors.Is(err, pgx.ErrNoRows) {
					continue
				}
				if err != nil {
					return err
				}
				r.v.name = x.Name
				for _, d := range savedDates(x) {
					if d.key == r.v.label {
						r.v.label = d.label
					}
				}
				out = append(out, r.v)
			}
			return nil
		})
	}
	if !ownAppChat(c) {
		if err := read(savedOwner{person: c.p.UserID, by: c.p.UserID}); err != nil {
			return nil, err
		}
	}
	if err := read(savedOwner{tenant: c.tenant.ID, by: c.p.UserID}); err != nil {
		return nil, err
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].on.Before(out[j].on) })
	return out, nil
}

// waRemindersList says the person's reminders.
func (s *Server) waRemindersList(ctx context.Context, c *chat) error {
	list, err := s.remindersOf(ctx, c)
	if err != nil {
		return err
	}
	if len(list) == 0 {
		return c.say(ctx, s, c.t("wa.remind.none"))
	}
	lines := make([]string, len(list))
	for i, r := range list {
		every := ""
		if r.yearly {
			every = c.t("wa.remind.every_year")
		}
		lines[i] = fmt.Sprintf("• @%s: %s, %s%s", r.name, whatsapp.SafeText(r.label), sayDay(r.on), every)
	}
	return c.say(ctx, s, c.t("wa.remind.list", "list", strings.Join(lines, "\n")))
}

// waRemindStop ends the person's reminders on @name.
func (s *Server) waRemindStop(ctx context.Context, c *chat, typed string) error {
	name, err := saved.Name(typed)
	if err != nil {
		return c.say(ctx, s, c.t("wa.remind.none_for", "name", typed))
	}
	got, err := s.savedFind(ctx, c, name)
	if err != nil {
		return err
	}
	n := 0
	for _, f := range got {
		o := savedOwner{tenant: c.tenant.ID, by: c.p.UserID}
		if f.mine {
			o = savedOwner{person: c.p.UserID, by: c.p.UserID}
		}
		if err := s.savedTx(ctx, o, func(tx pgx.Tx) error {
			tag, err := tx.Exec(ctx, `DELETE FROM saved_reminders WHERE object_id = $1 AND tell_user = $2`, f.row.ID, c.p.UserID)
			n += int(tag.RowsAffected())
			return err
		}); err != nil {
			return err
		}
	}
	if n == 0 {
		return c.say(ctx, s, c.t("wa.remind.none_for", "name", name))
	}
	return c.say(ctx, s, c.t("wa.remind.stopped", "name", name))
}

// --- the tick ---

// SavedRemindersTick tells the reminders due today, from remindHour in the
// morning, Lagos time. Run every minute.
func (s *Server) SavedRemindersTick(ctx context.Context) error {
	now := s.staffNow()
	if now.In(lagosTZ).Hour() < remindHour {
		return nil
	}
	today := remindDay(now)
	rows, err := s.Store.Pool.Query(ctx, `SELECT id, person_id, tenant_id, tell_tenant, tell_user FROM taskiem_saved_reminders_due($1)`, today)
	if err != nil {
		return err
	}
	type due struct {
		id, tellTenant, tellUser uuid.UUID
		person, tenant           *uuid.UUID
	}
	list, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (due, error) {
		var d due
		return d, r.Scan(&d.id, &d.person, &d.tenant, &d.tellTenant, &d.tellUser)
	})
	if err != nil {
		return err
	}
	var errs []error
	for _, d := range list {
		o := savedOwner{by: d.tellUser}
		if d.person != nil {
			o.person = *d.person
		} else if d.tenant != nil {
			o.tenant = *d.tenant
		}
		if err := s.savedReminderDue(ctx, o, d.id, today); err != nil && ctx.Err() == nil {
			errs = append(errs, err)
			// Tried again tomorrow, so one that keeps failing never keeps the
			// others waiting behind it.
			if err := s.savedTx(ctx, o, func(tx pgx.Tx) error {
				_, err := tx.Exec(ctx, `UPDATE saved_reminders SET remind_on = $2 WHERE id = $1`, d.id, today.AddDate(0, 0, 1))
				return err
			}); err != nil {
				errs = append(errs, err)
			}
		}
	}
	return errors.Join(errs...)
}

// savedReminderDue tells one reminder if its date is today (read again
// from the field), and moves it to its next day; a reminder whose thing
// or date is gone ends.
func (s *Server) savedReminderDue(ctx context.Context, o savedOwner, id uuid.UUID, today time.Time) error {
	var title, body string
	var tellUser, tellTenant uuid.UUID
	err := s.savedTx(ctx, o, func(tx pgx.Tx) error {
		var object uuid.UUID
		var field string
		var yearly bool
		// Counted from the day after it was last told, or from its day.
		var after time.Time
		err := tx.QueryRow(ctx, `SELECT object_id, field, yearly, COALESCE(last_sent, remind_on - 1), tell_user, tell_tenant
			FROM saved_reminders WHERE id = $1 FOR UPDATE`, id).Scan(&object, &field, &yearly, &after, &tellUser, &tellTenant)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		end := func() error {
			_, err := tx.Exec(ctx, `DELETE FROM saved_reminders WHERE id = $1`, id)
			return err
		}
		// A business's reminder is for its members: one who left hears no
		// more of the business's things.
		if !o.mine() {
			var member bool
			if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM memberships WHERE tenant_id = $1 AND user_id = $2)`, o.tenant, tellUser).Scan(&member); err != nil {
				return err
			}
			if !member || tellTenant != o.tenant {
				return end()
			}
		}
		x, err := savedRowTx(ctx, tx, o, object, false)
		if errors.Is(err, pgx.ErrNoRows) {
			return end()
		}
		if err != nil {
			return err
		}
		var f *savedDateField
		for _, d := range savedDates(x) {
			if d.key == field {
				f = &d
			}
		}
		if f == nil || f.value == "" {
			return end()
		}
		v, err := time.Parse(time.DateOnly, f.value)
		if err != nil {
			return end()
		}
		on, ok := nextOccurrence(v, yearly, after)
		if !ok {
			return end()
		}
		if on.After(today) {
			// The date was changed to a later one: wait for it.
			_, err := tx.Exec(ctx, `UPDATE saved_reminders SET remind_on = $2 WHERE id = $1`, id, on)
			return err
		}
		next, again := nextOccurrence(v, yearly, today)
		if !again {
			if err := end(); err != nil {
				return err
			}
		} else if _, err := tx.Exec(ctx, `UPDATE saved_reminders SET last_sent = $2, remind_on = $3 WHERE id = $1`, id, today, next); err != nil {
			return err
		}
		title = "@" + x.Name + ": " + f.label
		body = x.Title + ": " + f.label + " is " + sayDay(on) + "."
		if on.Equal(today) {
			body = x.Title + ": " + f.label + " is today (" + sayDay(on) + ")."
		}
		return nil
	})
	if err != nil || title == "" {
		return err
	}
	if o.mine() {
		// One's own: to one's own devices only, never through a business
		// (no notice, message or history of the business holds it: §5.3).
		if _, err := s.PersonPusher().PushPerson(ctx, tellUser, chatlinks.PersonPush{Title: title, Body: body, Link: "/you", Tag: "saved-reminder:" + id.String()}); err != nil {
			s.Logger.Warn("saved: pushing a reminder", "err", err)
		}
		return nil
	}
	s.tellSavedReminder(ctx, tellTenant, tellUser, title, body)
	return nil
}

// tellSavedReminder tells a person a reminder: a notice in the app, and a
// message in their Taskiem chat on each chat app they use there (the web
// chat when none).
func (s *Server) tellSavedReminder(ctx context.Context, tenant, user uuid.UUID, title, body string) {
	type told struct{ channel, addr string }
	var to []told
	err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		if err := notices.AddTx(ctx, tx, tenant, []uuid.UUID{user}, notices.Notice{Kind: notices.Setup, Title: title, Body: body, Link: "/settings/saved"}); err != nil {
			return err
		}
		for _, channel := range []string{"whatsapp", "telegram", "slack"} {
			addrs, err := chatAddrsTx(ctx, tx, channel, []uuid.UUID{user})
			if err != nil {
				return err
			}
			if a, ok := addrs[user]; ok {
				to = append(to, told{channel, a})
			}
		}
		if len(to) == 0 {
			to = append(to, told{"web", webAddr(user)})
		}
		return nil
	})
	if err != nil {
		s.Logger.Warn("saved: telling a reminder", "tenant", tenant, "err", err)
		return
	}
	for _, t := range to {
		ch, err := s.chatChannelFor(ctx, t.channel, tenant)
		if err != nil {
			continue
		}
		if _, err := ch.Send(ctx, t.addr, linkPhoneMessage(tenant, ch.Prefix(""), whatsapp.SafeText(title), whatsapp.SafeText(body), nil)); err != nil {
			s.Logger.Warn("saved: a reminder's message", "tenant", tenant, "err", err)
		}
	}
}

// dateWordsAny are words a date's label shares with many others: they name
// none of them alone.
var dateWordsAny = map[string]bool{"due": true, "date": true, "day": true, "on": true, "of": true, "the": true, "next": true, "last": true, "ends": true, "end": true}

// dateNamed is the date whose label's own words ("insurance" of "Insurance
// due") the words hold most of; none when no date's are held, or two hold
// as many.
func dateNamed(asked string, dates []savedDateField) *savedDateField {
	best, bestN, tie := -1, 0, false
	for i, d := range dates {
		n := 0
		for _, w := range strings.Fields(strings.ToLower(d.label)) {
			if !dateWordsAny[w] && labelAsked(asked, w) {
				n++
			}
		}
		switch {
		case n > bestN:
			best, bestN, tie = i, n, false
		case n == bestN && n > 0:
			tie = true
		}
	}
	if best < 0 || tie {
		return nil
	}
	return &dates[best]
}

// resetRemindersTx moves the reminders on a saved thing to its dates as
// they are now (it was just changed): a date moved earlier is told on its
// new day, not on the old one; a date gone ends them.
func resetRemindersTx(ctx context.Context, tx pgx.Tx, x savedRow, today time.Time) error {
	rows, err := tx.Query(ctx, `SELECT id, field, yearly, last_sent FROM saved_reminders WHERE object_id = $1 FOR UPDATE`, x.ID)
	if err != nil {
		return err
	}
	type rem struct {
		id       uuid.UUID
		field    string
		yearly   bool
		lastSent *time.Time
	}
	list, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (rem, error) {
		var m rem
		return m, r.Scan(&m.id, &m.field, &m.yearly, &m.lastSent)
	})
	if err != nil || len(list) == 0 {
		return err
	}
	dates := map[string]string{}
	for _, d := range savedDates(x) {
		dates[d.key] = d.value
	}
	for _, m := range list {
		v, err := time.Parse(time.DateOnly, dates[m.field])
		after := today.AddDate(0, 0, -1)
		if m.lastSent != nil && m.lastSent.After(after) {
			after = *m.lastSent
		}
		on, ok := time.Time{}, false
		if err == nil {
			on, ok = nextOccurrence(v, m.yearly, after)
		}
		if !ok {
			if _, err := tx.Exec(ctx, `DELETE FROM saved_reminders WHERE id = $1`, m.id); err != nil {
				return err
			}
			continue
		}
		if _, err := tx.Exec(ctx, `UPDATE saved_reminders SET remind_on = $2 WHERE id = $1`, m.id, on); err != nil {
			return err
		}
	}
	return nil
}
