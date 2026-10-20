package storefront

import (
	"errors"
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
	"unicode"
)

// What an owner writes in chat (docs/spec/storefronts.md 5.1 and 6.8),
// read forgivingly: prices ("2000", "₦2,000", "2k", "2.5k"), opening
// hours ("mon-sat 8-6", "daily 9am-9pm", "mon-fri 9-6, sat 10-2, closed
// sunday"), a service's length ("30", "45min", "1h") and an address made
// from a business's name.

// MaxPriceNaira bounds a price written in chat.
const MaxPriceNaira = 100_000_000

var priceRe = regexp.MustCompile(`^(\d+(?:\.\d+)?)(k|m)?$`)

// ParsePrice reads a price in naira as kobo.
func ParsePrice(s string) (int64, bool) {
	s = strings.ToLower(strings.TrimSpace(s))
	for _, p := range []string{"₦", "ngn", "naira", "#", "n"} {
		if rest, ok := strings.CutPrefix(s, p); ok && rest != "" && (rest[0] >= '0' && rest[0] <= '9' || rest[0] == ' ') {
			s = rest
			break
		}
	}
	s = strings.TrimSuffix(strings.TrimSuffix(s, "naira"), "ngn")
	s = strings.ReplaceAll(strings.ReplaceAll(s, ",", ""), " ", "")
	m := priceRe.FindStringSubmatch(s)
	if m == nil {
		return 0, false
	}
	f, err := strconv.ParseFloat(m[1], 64)
	if err != nil {
		return 0, false
	}
	switch m[2] {
	case "k":
		f *= 1000
	case "m":
		f *= 1_000_000
	}
	if f <= 0 || f > MaxPriceNaira {
		return 0, false
	}
	return int64(math.Round(f * 100)), true
}

var durationRe = regexp.MustCompile(`^(\d+(?:\.\d+)?)(m|min|mins|minute|minutes|h|hr|hrs|hour|hours)?$`)

// ParseMinutes reads a service's length: "30", "45min", "1h", "1.5hrs".
func ParseMinutes(s string) (int, bool) {
	m := durationRe.FindStringSubmatch(strings.ToLower(strings.TrimSpace(s)))
	if m == nil {
		return 0, false
	}
	f, err := strconv.ParseFloat(m[1], 64)
	if err != nil {
		return 0, false
	}
	if strings.HasPrefix(m[2], "h") {
		f *= 60
	}
	n := int(math.Round(f))
	if n < 5 || n > 480 {
		return 0, false
	}
	return n, true
}

// ErrHours refuses opening hours that cannot be read.
var ErrHours = errors.New("storefront: opening hours not understood")

var (
	hoursDash  = regexp.MustCompile(`\s*(?:-|–|—|\bto\b|\btill\b|\buntil\b|\bthrough\b|\bthru\b)\s*`)
	hoursSplit = regexp.MustCompile(`[\s,;&/]+|\band\b`)
	clockRe    = regexp.MustCompile(`^(\d{1,2})(?:[:.](\d{2}))?(am|pm)?$`)
	allDay     = map[string]bool{"24h": true, "24hrs": true, "24hours": true, "24/7": true, "24-7": true, "24hr": true}
)

// ParseHours reads opening hours: groups of days, each followed by one or
// more times ("mon-fri 9-6 sat 10-2", "daily 9am-9pm", "weekdays
// 8:30-17:00"), with "closed" before days that are closed. Days never
// given a time are closed. Times without am or pm are read as a day's
// opening ("8-6" is 08:00 to 18:00); "24 hours" is all day.
func ParseHours(s string) (Hours, error) {
	var h Hours
	text := strings.ToLower(strings.TrimSpace(s))
	text = strings.ReplaceAll(text, "24 hours", "24hours")
	text = strings.ReplaceAll(text, "24 hrs", "24hrs")
	text = strings.ReplaceAll(text, "every day", "daily")
	text = strings.ReplaceAll(text, "all week", "daily")
	text = strings.ReplaceAll(text, "24/7", "24hours daily")
	text = hoursDash.ReplaceAllString(text, "-")
	var pending, last []int
	closed, seen := false, false
	for _, tok := range hoursSplit.Split(text, -1) {
		tok = strings.Trim(tok, ".:")
		switch tok {
		case "", "open", "opens", "opening", "from", "on", "at", "hours", "is", "we", "are", "every", "each", "the", "by", "-":
			continue
		case "closed", "close", "shut", "off":
			closed, pending = true, nil
			continue
		}
		if days, ok := hoursDays(tok); ok {
			if closed {
				for _, d := range days {
					h.Days[d] = nil
				}
				continue
			}
			pending = append(pending, days...)
			continue
		}
		span, ok := hoursSpan(tok)
		if !ok {
			return Hours{}, fmt.Errorf("%w: %q", ErrHours, tok)
		}
		closed = false
		days := pending
		if len(days) == 0 {
			days = last // a second opening for the same days ("mon-fri 9-1 2-6")
		}
		if len(days) == 0 && !seen {
			days = []int{0, 1, 2, 3, 4, 5, 6} // a time alone: every day
		}
		if len(days) == 0 {
			return Hours{}, fmt.Errorf("%w: which days is %q?", ErrHours, tok)
		}
		for _, d := range days {
			if len(h.Days[d]) >= 3 {
				return Hours{}, fmt.Errorf("%w: at most three openings a day", ErrHours)
			}
			h.Days[d] = append(h.Days[d], span)
		}
		last, pending, seen = days, nil, true
	}
	if !seen {
		return Hours{}, fmt.Errorf("%w: no opening times", ErrHours)
	}
	return h, nil
}

var dayIndex = map[string]int{"mon": 0, "tue": 1, "tues": 1, "wed": 2, "thu": 3, "thur": 3, "thurs": 3, "fri": 4, "sat": 5, "sun": 6}

// hoursDay reads one day: "mon", "monday", "tues".
func hoursDay(w string) (int, bool) {
	if d, ok := dayIndex[w]; ok {
		return d, true
	}
	if len(w) > 3 && strings.HasSuffix(w, "day") {
		d, ok := dayIndex[w[:3]]
		return d, ok
	}
	if len(w) >= 3 {
		if d, ok := dayIndex[w[:3]]; ok && strings.HasPrefix(dayNames[d], strings.ToUpper(w[:1])+w[1:]) {
			return d, true
		}
	}
	return 0, false
}

// hoursDays reads a day, a range of days ("mon-sat", "fri-mon") or a
// word for several ("daily", "weekdays", "weekends").
func hoursDays(w string) ([]int, bool) {
	switch w {
	case "daily", "everyday", "alldays", "all":
		return []int{0, 1, 2, 3, 4, 5, 6}, true
	case "weekdays", "weekday":
		return []int{0, 1, 2, 3, 4}, true
	case "weekends", "weekend":
		return []int{5, 6}, true
	}
	if a, b, ok := strings.Cut(w, "-"); ok {
		from, ok1 := hoursDay(a)
		to, ok2 := hoursDay(b)
		if !ok1 || !ok2 {
			return nil, false
		}
		var out []int
		for d := from; ; d = (d + 1) % 7 {
			out = append(out, d)
			if d == to {
				break
			}
		}
		return out, true
	}
	d, ok := hoursDay(w)
	if !ok {
		return nil, false
	}
	return []int{d}, true
}

type clockTime struct {
	h, m   int
	suffix string
	// h24 is a time written the 24-hour way ("09:00", "17"): never moved
	// to the afternoon.
	h24 bool
}

func readClock(s string) (clockTime, bool) {
	m := clockRe.FindStringSubmatch(s)
	if m == nil {
		return clockTime{}, false
	}
	h, _ := strconv.Atoi(m[1])
	mm := 0
	if m[2] != "" {
		mm, _ = strconv.Atoi(m[2])
	}
	if mm > 59 || h > 24 || m[3] != "" && (h < 1 || h > 12) {
		return clockTime{}, false
	}
	return clockTime{h: h, m: mm, suffix: m[3], h24: m[3] == "" && (h > 12 || len(m[1]) == 2 && m[2] != "")}, true
}

func (c clockTime) hour24() int {
	switch {
	case c.suffix == "am" && c.h == 12:
		return 0
	case c.suffix == "pm" && c.h < 12:
		return c.h + 12
	}
	return c.h
}

// hoursSpan reads one opening: "8-6", "9am-9pm", "8:30-17:00", "24hours".
func hoursSpan(w string) (Span, bool) {
	if allDay[w] {
		return Span{Open: "00:00", Close: "23:59"}, true
	}
	a, b, ok := strings.Cut(w, "-")
	if !ok {
		return Span{}, false
	}
	o, ok1 := readClock(a)
	c, ok2 := readClock(b)
	if !ok1 || !ok2 {
		return Span{}, false
	}
	oh, ch := o.hour24(), c.hour24()
	switch {
	case o.suffix == "" && c.suffix == "" && !o.h24 && !c.h24 && oh >= 1 && oh <= 5 && ch > oh && ch < 12:
		oh, ch = oh+12, ch+12 // "2-6": the afternoon
	case c.suffix == "" && !c.h24 && ch < 12 && ch*60+c.m <= oh*60+o.m:
		ch += 12 // "8-6", "9am-6": closes in the evening
	}
	if oh == 24 {
		oh = 0
	}
	if ch == 24 {
		ch = 0
	}
	sp := Span{Open: fmt.Sprintf("%02d:%02d", oh, o.m), Close: fmt.Sprintf("%02d:%02d", ch, c.m)}
	if sp.Open == sp.Close {
		return Span{}, false
	}
	return sp, true
}

// FormatHours writes opening hours on one line for a message: "Mon–Sat
// 08:00–18:00; Sun closed".
func FormatHours(h Hours) string {
	short := [7]string{"Mon", "Tue", "Wed", "Thu", "Fri", "Sat", "Sun"}
	key := func(d int) string {
		if len(h.Days[d]) == 0 {
			return "closed"
		}
		parts := make([]string, len(h.Days[d]))
		for i, sp := range h.Days[d] {
			parts[i] = sp.Open + "–" + sp.Close
		}
		return strings.Join(parts, ", ")
	}
	var out []string
	for d := 0; d < 7; {
		e := d
		for e+1 < 7 && key(e+1) == key(d) {
			e++
		}
		days := short[d]
		if e > d {
			days += "–" + short[e]
		}
		out = append(out, days+" "+key(d))
		d = e + 1
	}
	return strings.Join(out, "; ")
}

// SlugCandidates are addresses to try for a new storefront, best first:
// the name, the name and the area, then the name with a number.
func SlugCandidates(name, area string) []string {
	base := slugify(name, MaxSlug-3)
	if len(base) < 3 {
		base = strings.Trim(base+"-shop", "-")
	}
	out := []string{base}
	if a := slugify(area, MaxSlug-len(base)-1); a != "" {
		out = append(out, base+"-"+a)
	}
	for i := 2; i <= 30; i++ {
		out = append(out, base+"-"+strconv.Itoa(i))
	}
	return out
}

// MaxSlug is the longest slug.
const MaxSlug = 30

// slugify keeps letters and digits, joins words with hyphens and cuts at
// max characters ("Ada's Kitchen" is "adas-kitchen").
func slugify(s string, max int) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(s) {
		switch {
		case r == '\'' || r == '’':
		case r < unicode.MaxASCII && (unicode.IsLetter(r) || unicode.IsDigit(r)):
			if dash && b.Len() > 0 {
				b.WriteByte('-')
			}
			dash = false
			b.WriteRune(r)
		default:
			dash = true
		}
	}
	out := b.String()
	if max < 1 {
		return ""
	}
	if len(out) > max {
		out = strings.TrimRight(out[:max], "-")
	}
	return out
}
