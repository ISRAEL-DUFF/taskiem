package api

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strconv"
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
	remindSchedule = regexp.MustCompile(`(?i)\b(?:every|each|daily|weekly|monthly|yearly|tomorrow|tonight|today|later|soon|noon|midnight|morning|afternoon|evening|night|next\s+(?:week|month|year|time)|this\s+(?:week|month)|month\s+end|end\s+of\s+(?:the\s+)?(?:week|month)|at\s+\d|on\s+the\s+\d|\d{1,2}(?:st|nd|rd|th)\b|\d{1,2}\s*(?:am|pm)|\d{1,2}:\d{2}|o'?clock|in\s+(?:\d+|a|an|one|two|three|a\s+few|few)\s+(?:minutes?|hours?|days?|weeks?|months?)|monday|tuesday|wednesday|thursday|friday|saturday|sunday)\b`)
	// Words that say a date is meant: due, a birthday, an end.
	remindDue = regexp.MustCompile(`(?i)\b(?:due|birthday|expires?|expiry|ends?|renew(?:al|s)?|anniversary)\b`)
)

// remindHour is when a reminder is told on its day, Lagos time.
const remindHour = 8

// savedDateField is a date a saved thing holds: its field's key, its label
// and its value (2027-03-03), and whether it is sensitive (its date is
// then never said: "is due today").
type savedDateField struct {
	key, label, value string
	sensitive         bool
}

// savedDates are the dates x holds: a made-up kind's date fields (all, the
// sensitive ones opened: values from customValuesTx; nil gives what lists
// show, the sensitive masked), a person's birthday.
func savedDates(x savedRow, all map[string]string) []savedDateField {
	var out []savedDateField
	switch x.Kind {
	case saved.KindCustom:
		values := all
		if values == nil {
			values = x.view(savedOwner{}, false).Values
		}
		for _, f := range x.Fields {
			if f.Type == "date" {
				out = append(out, savedDateField{f.Key, f.Label, values[f.Key], f.Sensitive})
			}
		}
	case saved.KindPerson:
		v, _ := x.Details["birthday"].(string)
		out = append(out, savedDateField{"birthday", "Birthday", v, false})
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

// datesTx are x's dates in full (a sensitive one opened), read in the
// owner's transaction.
func (s *Server) datesTx(ctx context.Context, tx pgx.Tx, o savedOwner, x savedRow) ([]savedDateField, error) {
	if x.Kind != saved.KindCustom {
		return savedDates(x, nil), nil
	}
	all, err := s.customValuesTx(ctx, tx, o, x)
	if err != nil {
		return nil, err
	}
	return savedDates(x, all), nil
}

// withoutName is the words with "@name" (and "@name's") taken out, so a
// field's words are not read in a name ("@phone-shop").
func withoutName(text, name string) string {
	re := regexp.MustCompile(`(?i)@` + regexp.QuoteMeta(name) + `(?:'s|’s)?`)
	return strings.ToLower(re.ReplaceAllString(text, " "))
}

// sayDay is a date as the chat says it: 3 Mar 2027.
func sayDay(d time.Time) string { return d.Format("2 Jan 2006") }

// waSavedReminders answers the reminder words; false when the words are
// none of them.
func (s *Server) waSavedReminders(ctx context.Context, c *chat, text string) (bool, error) {
	if c.p == nil || c.p.UserID == uuid.Nil {
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

// foundOwner is whose a found thing is, as the person in the chat
// reaches it.
func foundOwner(c *chat, f savedFound) savedOwner {
	if f.mine {
		return savedOwner{person: c.p.UserID, by: c.p.UserID}
	}
	return savedOwner{tenant: c.tenant.ID, by: c.p.UserID}
}

// dateAsked is the date field the words name, by its label (the longest
// first) or by a date word; nil when they name none.
func dateAsked(asked string, dates []savedDateField) *savedDateField {
	sort.SliceStable(dates, func(i, j int) bool { return len(dates[i].label) > len(dates[j].label) })
	for i := range dates {
		if labelAsked(asked, dates[i].label) {
			return &dates[i]
		}
	}
	return dateNamed(asked, dates)
}

// hasDate says whether any of the date fields holds a date.
func hasDate(dates []savedDateField) bool {
	for _, d := range dates {
		if d.value != "" {
			return true
		}
	}
	return false
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
	// The things of the name the words can be about: those holding a date
	// they name, or any date when they say "due".
	asked := withoutName(text, name)
	due := remindDue.MatchString(text)
	var fit, empty []savedFound
	datesOf := map[bool][]savedDateField{}
	for _, g := range got {
		o := foundOwner(c, g)
		var dates []savedDateField
		if err := s.savedTx(ctx, o, func(tx pgx.Tx) error {
			var err error
			dates, err = s.datesTx(ctx, tx, o, g.row)
			return err
		}); err != nil {
			return true, err
		}
		if len(dates) == 0 {
			continue
		}
		datesOf[g.mine] = dates
		// One holding the date over one whose field is empty.
		switch f := dateAsked(asked, dates); {
		case f != nil && f.value != "", f == nil && due && hasDate(dates):
			fit = append(fit, g)
		case f != nil || due:
			empty = append(empty, g)
		}
	}
	if len(fit) == 0 {
		fit = empty
	}
	if len(fit) == 0 {
		// Not about a date it holds: a workflow to build, maybe; with "due",
		// it holds none.
		if !due {
			return false, nil
		}
		return true, c.say(ctx, s, c.t("wa.remind.no_dates", "name", got[0].row.Name))
	}
	one, ok, err := s.savedPickOne(ctx, c, chatSavedPick{Kind: "remind", Text: text, Name: name}, fit)
	if !ok || err != nil {
		return true, err
	}
	x, o, dates := one.row, foundOwner(c, one), datesOf[one.mine]
	f := dateAsked(asked, dates)
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
		// One's own is told to one's own devices, tied to no business.
		_, err := tx.Exec(ctx, `INSERT INTO saved_reminders (id, person_id, tenant_id, object_id, field, remind_on, yearly, tell_tenant, tell_user)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $3, $8)
			ON CONFLICT (object_id, field, tell_user) DO UPDATE SET remind_on = EXCLUDED.remind_on, yearly = EXCLUDED.yearly,
				tell_tenant = EXCLUDED.tell_tenant,
				-- Set again: what was told already is not told again.
				last_sent = CASE WHEN saved_reminders.last_sent >= EXCLUDED.remind_on THEN saved_reminders.last_sent ELSE EXCLUDED.remind_on - 1 END`,
			uuid.Must(uuid.NewV7()), nullable(o.person), nullable(o.tenant), x.ID, f.key, on, yearly, c.p.UserID)
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
				for _, d := range savedDates(x, nil) {
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
		id, tellUser   uuid.UUID
		tellTenant     *uuid.UUID
		person, tenant *uuid.UUID
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
				// Kept counted from before its day, so it is told late, not
				// moved to its next year.
				_, err := tx.Exec(ctx, `UPDATE saved_reminders SET last_sent = COALESCE(last_sent, remind_on - 1), remind_on = $2 WHERE id = $1`, d.id, today.AddDate(0, 0, 1))
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
	var tellUser uuid.UUID
	var tellTenant *uuid.UUID
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
			if !member || tellTenant == nil || *tellTenant != o.tenant {
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
		dates, err := s.datesTx(ctx, tx, o, x)
		if err != nil {
			return err
		}
		var f *savedDateField
		for _, d := range dates {
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
		switch {
		case f.sensitive:
			// A sensitive date is never said, only that it has come.
			body = x.Title + ": " + f.label + " is due."
		case on.Equal(today):
			body = x.Title + ": " + f.label + " is today (" + sayDay(on) + ")."
		}
		return nil
	})
	if err != nil || title == "" {
		return err
	}
	if o.mine() {
		// One's own: to one's own devices and one's own chats on Taskiem's
		// number and bot, never through a business (no notice, message or
		// history of the business holds it: §5.3).
		if _, err := s.PersonPusher().PushPerson(ctx, tellUser, chatlinks.PersonPush{Title: title, Body: body, Link: "/you", Tag: "saved-reminder:" + id.String()}); err != nil {
			s.Logger.Warn("saved: pushing a reminder", "err", err)
		}
		s.tellOwnReminder(ctx, tellUser, title, body)
		return nil
	}
	if tellTenant != nil {
		s.tellSavedReminder(ctx, *tellTenant, tellUser, title, body)
	}
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

// tellOwnReminder sends one's own reminder to one's own chats: the
// WhatsApp number and Telegram account they linked, on Taskiem's own
// number and bot, tied to no business.
func (s *Server) tellOwnReminder(ctx context.Context, user uuid.UUID, title, body string) {
	msg := linkPhoneMessage(uuid.Nil, "", whatsapp.SafeText(title), whatsapp.SafeText(body), nil)
	if s.WhatsApp != nil {
		var number *string
		var verified *time.Time
		if err := s.Store.Pool.QueryRow(ctx, `SELECT number, verified_at FROM taskiem_wa_binding($1)`, user).Scan(&number, &verified); err != nil {
			s.Logger.Warn("saved: one's own number", "err", err)
		} else if number != nil && verified != nil {
			if _, err := s.WhatsApp.Send(ctx, *number, msg); err != nil {
				s.Logger.Warn("saved: one's own reminder on WhatsApp", "err", err)
			}
		}
	}
	if s.Telegram != nil {
		var id *int64
		var verified *time.Time
		if err := s.Store.Pool.QueryRow(ctx, `SELECT telegram_id, verified_at FROM taskiem_tg_binding($1)`, user).Scan(&id, &verified); err != nil {
			s.Logger.Warn("saved: one's own Telegram", "err", err)
		} else if id != nil && verified != nil {
			if _, err := s.Telegram.Send(ctx, "tg:"+strconv.FormatInt(*id, 10), msg); err != nil {
				s.Logger.Warn("saved: one's own reminder on Telegram", "err", err)
			}
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
func (s *Server) resetRemindersTx(ctx context.Context, tx pgx.Tx, o savedOwner, x savedRow, today time.Time) error {
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
	all, err := s.datesTx(ctx, tx, o, x)
	if err != nil {
		return err
	}
	dates := map[string]string{}
	for _, d := range all {
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
