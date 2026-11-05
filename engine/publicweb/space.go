package publicweb

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/israel-duff/taskiem/engine/storefront"
)

// Spaces (docs/spec/fronts.md §6, F5): a hall or a room booked on its
// list's own calendar, by the day (from the first day's opening to the
// last day's closing) or by the hour (on one day, within its hours). The
// database takes the claim with the submission, refusing a time that
// overlaps another booking of the space, with the list's setup time kept
// clear between them.

// spaceField is the field for a front's list of spaces: those that can
// be booked (available, with a rate), and the calendar's days or hours.
func spaceField(items []storefront.Item, cal storefront.Calendar, now time.Time, depositPercent int) *hireView {
	h := &hireView{Space: true, Hourly: cal.Grain == "hours", cal: cal, Qty: "1"}
	per := " a day"
	if h.Hourly {
		per = " an hour"
	}
	for _, it := range items {
		if it.Available && it.PriceKobo != nil && *it.PriceKobo > 0 {
			h.Items = append(h.Items, hireItem{ID: it.ID, Label: it.Name + ", " + storefront.FormatNaira(*it.PriceKobo) + per})
		}
	}
	today := now.In(storefront.Lagos)
	h.Min = today.Format(time.DateOnly)
	h.Max = today.AddDate(0, 0, HireAheadDays).Format(time.DateOnly)
	open, closes := cal.OpenHours()
	h.Opens = fmt.Sprintf("Open %02d:00 to %02d:00.", open, closes)
	if h.Hourly {
		for t := open; t < closes; t++ {
			h.FromHours = append(h.FromHours, hourOption{Value: strconv.Itoa(t), Label: fmt.Sprintf("%02d:00", t)})
		}
		for t := open + 1; t <= closes; t++ {
			h.ToHours = append(h.ToHours, hourOption{Value: strconv.Itoa(t), Label: fmt.Sprintf("%02d:00", t)})
		}
		if cal.MinHours > 1 {
			h.MinHours = "At least " + strconv.Itoa(cal.MinHours) + " hours."
		}
	}
	if depositPercent > 0 && depositPercent < 100 {
		h.Deposit = strconv.Itoa(depositPercent) + "% of the price is paid to confirm, the rest before the day."
	}
	return h
}

// checkSpace reads a space's field into the submission: the space from
// the list's copy, its times from the calendar, and the price at its rate
// per day or per hour; msg says what to fix.
func checkSpace(v url.Values, items []storefront.Item, act storefront.PublicAction, cal storefront.Calendar, now time.Time, sub *storefront.Submission) string {
	var it *storefront.Item
	for i := range items {
		if items[i].ID == v.Get("hire_item") {
			it = &items[i]
		}
	}
	if it == nil || !it.Available || it.PriceKobo == nil || *it.PriceKobo <= 0 {
		return "Choose the space you want."
	}
	open, closes := cal.OpenHours()
	today := now.In(storefront.Lagos)
	today = time.Date(today.Year(), today.Month(), today.Day(), 0, 0, 0, 0, storefront.Lagos)
	day := func(name string) (time.Time, bool) {
		d, err := time.ParseInLocation(time.DateOnly, strings.TrimSpace(v.Get(name)), storefront.Lagos)
		return d, err == nil
	}
	var start, until, last time.Time
	var units int
	unit := "day"
	if cal.Grain == "hours" {
		d, ok := day("hire_day")
		if !ok {
			return "Choose the day."
		}
		from, err1 := strconv.Atoi(v.Get("hire_start"))
		to, err2 := strconv.Atoi(v.Get("hire_end"))
		switch {
		case err1 != nil || err2 != nil || from < open || to > closes:
			return fmt.Sprintf("Choose the hours, between %02d:00 and %02d:00.", open, closes)
		case to <= from:
			return "The end is before the start."
		case to-from < cal.MinHours:
			return "Book at least " + strconv.Itoa(cal.MinHours) + " hours."
		}
		start = d.Add(time.Duration(from) * time.Hour)
		until = d.Add(time.Duration(to) * time.Hour)
		last, units, unit = d, to-from, "hour"
	} else {
		from, ok1 := day("hire_from")
		to, ok2 := day("hire_to")
		if !ok1 || !ok2 {
			return "Choose the first and the last day."
		}
		if to.Before(from) {
			return "The last day is before the first."
		}
		units = int(to.Sub(from).Hours()/24+0.5) + 1
		if units > MaxHireDays {
			return "A booking is at most " + strconv.Itoa(MaxHireDays) + " days."
		}
		start = from.Add(time.Duration(open) * time.Hour)
		until = to.Add(time.Duration(closes) * time.Hour)
		last = to
	}
	switch {
	case start.Before(today) || !start.After(now):
		return "That time has passed. Choose a later one."
	case start.After(today.AddDate(0, 0, HireAheadDays)):
		return "That is too far ahead to book."
	}
	rate := *it.PriceKobo
	total := rate * int64(units)
	sub.Kind = "space"
	sub.Hire = &storefront.Hired{ItemID: it.ID, Item: it.Name, Qty: 1, From: start, To: last, Until: until}
	if unit == "hour" {
		sub.Hire.Hours = units
	} else {
		sub.Hire.Days = units
	}
	sub.Basket = []storefront.Line{{ID: it.ID, Name: it.Name, Quantity: units, PriceKobo: rate, LineKobo: total, Unit: unit}}
	sub.TotalKobo = total
	sub.DepositKobo = 0
	if act.DepositPercent > 0 && act.DepositPercent < 100 {
		sub.DepositKobo = (total*int64(act.DepositPercent) + 99) / 100
	}
	return ""
}
