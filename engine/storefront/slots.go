package storefront

import (
	"fmt"
	"sort"
	"time"
)

// Bookings (docs/spec/storefronts.md 6.9): one calendar per business. A
// service's slots are its availability cut into its duration plus buffer,
// less what is booked or held. Bookings of one service at one start share
// it up to the service's capacity (a class); anything else overlapping
// blocks it.

// Availability is when the business takes bookings, in Lagos time.
type Availability struct {
	// Days are booking hours, Monday first; every day empty means the
	// card's opening hours.
	Days [7][]Span `json:"days"`
	// Off are dates with no bookings ("2026-12-25").
	Off []string `json:"off,omitempty"`
	// AheadDays is how far ahead people may book (default 30).
	AheadDays int `json:"ahead_days,omitempty"`
	// SoonestMinutes is how soon from now a booking may start (default 120).
	SoonestMinutes int `json:"soonest_minutes,omitempty"`
	// CutoffMinutes is how close to the start a customer may still change
	// or cancel (default 120).
	CutoffMinutes int `json:"cutoff_minutes,omitempty"`
	// AutoConfirm confirms bookings without asking the owner.
	AutoConfirm bool `json:"auto_confirm,omitempty"`
}

// Defaults and bounds of an Availability.
const (
	DefaultAheadDays = 30
	DefaultSoonest   = 120
	DefaultCutoff    = 120
	MaxAheadDays     = 180
	MaxDaysOff       = 400
	// HoldMinutes is how long a picked slot is held before the booking is
	// recorded or paid for.
	HoldMinutes = 10
)

// Check normalises the settings and refuses what cannot be offered.
func (a *Availability) Check() error {
	for d, spans := range a.Days {
		if len(spans) > 3 {
			return fmt.Errorf("%w: at most three booking periods a day", ErrInvalid)
		}
		for _, sp := range spans {
			if !hhmm.MatchString(sp.Open) || !hhmm.MatchString(sp.Close) || minutes(sp.Close) <= minutes(sp.Open) {
				return fmt.Errorf("%w: booking hours on %s must be like 09:00 to 18:00, ending the same day", ErrInvalid, dayNames[d])
			}
		}
	}
	if len(a.Off) > MaxDaysOff {
		return fmt.Errorf("%w: at most %d days off", ErrInvalid, MaxDaysOff)
	}
	seen := map[string]bool{}
	off := a.Off[:0]
	for _, d := range a.Off {
		if _, err := time.ParseInLocation(time.DateOnly, d, Lagos); err != nil {
			return fmt.Errorf("%w: day off %q is not a date like 2026-12-25", ErrInvalid, d)
		}
		if !seen[d] {
			seen[d] = true
			off = append(off, d)
		}
	}
	sort.Strings(off)
	a.Off = off
	for _, f := range []struct {
		v        *int
		name     string
		min, max int
	}{{&a.AheadDays, "how far ahead", 1, MaxAheadDays}, {&a.SoonestMinutes, "how soon", 0, 7 * 24 * 60}, {&a.CutoffMinutes, "the change cut-off", 0, 7 * 24 * 60}} {
		if *f.v < f.min || *f.v > f.max {
			if *f.v == 0 && f.min > 0 {
				continue // the default
			}
			return fmt.Errorf("%w: %s must be from %d to %d", ErrInvalid, f.name, f.min, f.max)
		}
	}
	return nil
}

// Effective fills what is unset: the card's opening hours (without spans
// past midnight) and the default window.
func (a Availability) Effective(card Hours) Availability {
	empty := true
	for _, d := range a.Days {
		if len(d) > 0 {
			empty = false
		}
	}
	if empty {
		for d, spans := range card.Days {
			for _, sp := range spans {
				if minutes(sp.Close) > minutes(sp.Open) {
					a.Days[d] = append(a.Days[d], sp)
				}
			}
		}
	}
	if a.AheadDays == 0 {
		a.AheadDays = DefaultAheadDays
	}
	if a.SoonestMinutes == 0 {
		a.SoonestMinutes = DefaultSoonest
	}
	if a.CutoffMinutes == 0 {
		a.CutoffMinutes = DefaultCutoff
	}
	return a
}

// Busy is time taken on the calendar: Count bookings or holds of Service
// starting at Start, keeping it until Until (the end plus the buffer).
type Busy struct {
	Service string
	Start   time.Time
	Until   time.Time
	Count   int
}

// Service is what a slot needs to know about a service.
type Service struct {
	ID       string
	Duration time.Duration
	Buffer   time.Duration
	Capacity int
}

// ServiceOf reads a service from its catalogue item; ok is false when it
// cannot be booked (no duration, or unavailable).
func ServiceOf(it Item) (Service, bool) {
	if !it.Available || it.DurationMinutes <= 0 || it.ID == "" {
		return Service{}, false
	}
	return Service{ID: it.ID, Duration: time.Duration(it.DurationMinutes) * time.Minute, Buffer: time.Duration(it.BufferMinutes) * time.Minute,
		Capacity: max(1, it.Capacity)}, true
}

// Days lists the dates (Lagos midnight) a booking may fall on, from today.
func (a Availability) Dates(now time.Time) []time.Time {
	day := midnight(now)
	var out []time.Time
	for i := 0; i <= a.AheadDays; i++ {
		d := day.AddDate(0, 0, i)
		if len(a.Days[weekday(d)]) > 0 && !a.off(d) {
			out = append(out, d)
		}
	}
	return out
}

// Slots lists the free starts of a service on day (any time that day, in
// Lagos), given what is busy.
func (a Availability) Slots(s Service, day, now time.Time, busy []Busy) []time.Time {
	d := midnight(day)
	if a.off(d) {
		return nil
	}
	soonest := now.Add(time.Duration(a.SoonestMinutes) * time.Minute)
	latest := midnight(now).AddDate(0, 0, a.AheadDays+1)
	step := s.Duration + s.Buffer
	var out []time.Time
	for _, sp := range a.Days[weekday(d)] {
		open := d.Add(time.Duration(minutes(sp.Open)) * time.Minute)
		closes := d.Add(time.Duration(minutes(sp.Close)) * time.Minute)
		for t := open; !t.Add(s.Duration).After(closes); t = t.Add(step) {
			if !t.Before(soonest) && t.Before(latest) && Free(s, t, busy) {
				out = append(out, t)
			}
		}
	}
	return out
}

// Offers reports whether start is one of the service's free slots.
func (a Availability) Offers(s Service, start, now time.Time, busy []Busy) bool {
	for _, t := range a.Slots(s, start, now, busy) {
		if t.Equal(start) {
			return true
		}
	}
	return false
}

// Free reports whether a booking of s at start fits beside busy: nothing
// overlapping it but bookings of the same service at the same start, fewer
// than its capacity. The database checks the same when it holds a slot.
func Free(s Service, start time.Time, busy []Busy) bool {
	until := start.Add(s.Duration + s.Buffer)
	same := 0
	for _, b := range busy {
		if !b.Start.Before(until) || !start.Before(b.Until) {
			continue
		}
		if b.Service != s.ID || !b.Start.Equal(start) {
			return false
		}
		same += b.Count
	}
	return same < s.Capacity
}

func (a Availability) off(d time.Time) bool {
	ds := d.Format(time.DateOnly)
	i := sort.SearchStrings(a.Off, ds)
	return i < len(a.Off) && a.Off[i] == ds
}

func midnight(t time.Time) time.Time {
	t = t.In(Lagos)
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, Lagos)
}

func weekday(t time.Time) int { return (int(t.In(Lagos).Weekday()) + 6) % 7 }
