// Package storefront is a business's one-page public storefront (Phase 5,
// S2; docs/spec/storefronts.md, docs/storefronts.md): the card the owner
// edits, the catalogue mapped from a data table, publishing a snapshot the
// public host serves, and the copy of the catalogue's public columns that
// keeps page views off Xenibase.
package storefront

import (
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/israel-duff/taskiem/engine/publicnames"
	"github.com/israel-duff/taskiem/engine/sender"
)

// ErrInvalid wraps every refusal of a draft.
var ErrInvalid = errors.New("storefront: invalid")

// Templates are the page's two layouts: classic, a list that suits a menu
// or a price list; bold, a photo grid that suits a shop.
var Templates = []string{"classic", "bold"}

// Accents are the colours an owner may pick, each readable as text on
// white and as a button with white text.
var Accents = map[string]string{
	"green": "#0b6e4f", "blue": "#1d4ed8", "red": "#b91c1c", "orange": "#c2410c",
	"purple": "#6d28d9", "pink": "#be185d", "teal": "#0f766e", "black": "#111827",
}

// Lagos is the time zone opening hours are in.
var Lagos = func() *time.Location {
	l, err := time.LoadLocation("Africa/Lagos")
	if err != nil {
		return time.FixedZone("WAT", 3600)
	}
	return l
}()

// Card is what the owner edits about the business.
type Card struct {
	Name     string `json:"name"`
	Tagline  string `json:"tagline,omitempty"`
	About    string `json:"about,omitempty"`
	Logo     string `json:"logo,omitempty"` // a storefront_media id
	Area     string `json:"area,omitempty"`
	Address  string `json:"address,omitempty"`
	MapQuery string `json:"map_query,omitempty"` // what a map link searches for
	Phone    string `json:"phone,omitempty"`
	WhatsApp string `json:"whatsapp,omitempty"`
	Hours    Hours  `json:"hours"`
	Template string `json:"template"`
	Accent   string `json:"accent"`
	Language string `json:"language,omitempty"`
}

// Hours are opening hours, Monday first (Days[0]) to Sunday; a day with
// no spans is closed. Note is shown beside them ("Closed on public
// holidays").
type Hours struct {
	Days [7][]Span `json:"days"`
	Note string    `json:"note,omitempty"`
}

// Span is one opening, "08:00" to "18:30". Close before Open runs past
// midnight.
type Span struct {
	Open  string `json:"open"`
	Close string `json:"close"`
}

// Columns map a data table's columns to the catalogue: Name is required;
// Price is a number in PriceUnit (naira, or kobo); Photo holds a
// storefront_media id; Available is a boolean (absent: available);
// Stock is an integer count (absent: not tracked), lowered when an order
// is accepted; Sort orders items (absent: the table's order).
type Columns struct {
	Name        string `json:"name"`
	Price       string `json:"price,omitempty"`
	PriceUnit   string `json:"price_unit,omitempty"`
	Description string `json:"description,omitempty"`
	Photo       string `json:"photo,omitempty"`
	Available   string `json:"available,omitempty"`
	Category    string `json:"category,omitempty"`
	Stock       string `json:"stock,omitempty"`
	Sort        string `json:"sort,omitempty"`
}

var hhmm = regexp.MustCompile(`^([01][0-9]|2[0-3]):[0-5][0-9]$`)

// CheckSlug normalises a slug and refuses reserved and protected ones.
func CheckSlug(s string) (string, error) {
	slug, err := publicnames.NormalizeSlug(s)
	if err == nil {
		err = publicnames.CheckSlug(slug)
	}
	if err != nil {
		return "", fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	return slug, nil
}

// Check normalises a card in place and refuses what may not be published.
func (c *Card) Check() error {
	c.Name = strings.TrimSpace(c.Name)
	if err := publicnames.CheckDisplayName(c.Name); err != nil {
		return fmt.Errorf("%w: the name: %w", ErrInvalid, err)
	}
	for _, f := range []struct {
		v    *string
		name string
		max  int
	}{{&c.Tagline, "tagline", 120}, {&c.About, "about", 1000}, {&c.Area, "area", 80}, {&c.Address, "address", 200},
		{&c.MapQuery, "map query", 200}, {&c.Hours.Note, "hours note", 120}} {
		*f.v = strings.TrimSpace(*f.v)
		if utf8.RuneCountInString(*f.v) > f.max {
			return fmt.Errorf("%w: the %s is longer than %d characters", ErrInvalid, f.name, f.max)
		}
		if hasLink(*f.v) {
			return fmt.Errorf("%w: the %s may not hold links or email addresses", ErrInvalid, f.name)
		}
	}
	for _, p := range []*string{&c.Phone, &c.WhatsApp} {
		if *p == "" {
			continue
		}
		n, err := sender.NormalisePhone(*p, []string{"234"})
		if err != nil {
			return fmt.Errorf("%w: %w", ErrInvalid, err)
		}
		*p = n
	}
	for d, spans := range c.Hours.Days {
		if len(spans) > 3 {
			return fmt.Errorf("%w: at most three openings a day", ErrInvalid)
		}
		for _, sp := range spans {
			if !hhmm.MatchString(sp.Open) || !hhmm.MatchString(sp.Close) || sp.Open == sp.Close {
				return fmt.Errorf("%w: opening hours on %s must be like 08:00 to 18:30", ErrInvalid, dayNames[d])
			}
		}
	}
	if c.Template == "" {
		c.Template = Templates[0]
	}
	if !contains(Templates, c.Template) {
		return fmt.Errorf("%w: template must be one of %s", ErrInvalid, strings.Join(Templates, ", "))
	}
	if c.Accent == "" {
		c.Accent = "green"
	}
	if _, ok := Accents[c.Accent]; !ok {
		return fmt.Errorf("%w: accent %q is not one of the colours offered", ErrInvalid, c.Accent)
	}
	if c.Language == "" {
		c.Language = "en"
	}
	return nil
}

// Check refuses a mapping that names no name column.
func (m *Columns) Check(known func(string) (typ string, ok bool)) error {
	if m.Name == "" {
		return fmt.Errorf("%w: the catalogue needs a name column", ErrInvalid)
	}
	if m.PriceUnit == "" {
		m.PriceUnit = "naira"
	}
	if m.PriceUnit != "naira" && m.PriceUnit != "kobo" {
		return fmt.Errorf("%w: price_unit is naira or kobo", ErrInvalid)
	}
	want := map[string][]string{
		m.Name: {"text"}, m.Description: {"text"}, m.Category: {"text"}, m.Photo: {"text"},
		m.Price: {"integer", "number"}, m.Sort: {"integer", "number"}, m.Available: {"boolean"}, m.Stock: {"integer"},
	}
	for col, types := range want {
		if col == "" {
			continue
		}
		typ, ok := known(col)
		if !ok {
			return fmt.Errorf("%w: the table has no column %q", ErrInvalid, col)
		}
		if !contains(types, typ) {
			return fmt.Errorf("%w: column %q is %s; it must be %s", ErrInvalid, col, typ, strings.Join(types, " or "))
		}
	}
	return nil
}

var linkRe = regexp.MustCompile(`(?i)(https?://|www\.|[a-z0-9._%+-]+@[a-z0-9.-]+\.[a-z]{2,})`)

func hasLink(s string) bool { return linkRe.MatchString(s) }

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

var dayNames = [7]string{"Monday", "Tuesday", "Wednesday", "Thursday", "Friday", "Saturday", "Sunday"}

// OpenNow reports whether the hours have the business open at t, and the
// day's spans for showing. Unset hours (every day empty) are not "closed":
// known is false and nothing is shown.
func (h Hours) OpenNow(t time.Time) (open, known bool) {
	t = t.In(Lagos)
	for _, d := range h.Days {
		if len(d) > 0 {
			known = true
		}
	}
	if !known {
		return false, false
	}
	day := (int(t.Weekday()) + 6) % 7 // Monday is 0
	mins := t.Hour()*60 + t.Minute()
	within := func(sp Span, m int) bool {
		o, c := minutes(sp.Open), minutes(sp.Close)
		if c > o {
			return m >= o && m < c
		}
		return m >= o // runs past midnight: the rest is yesterday's
	}
	for _, sp := range h.Days[day] {
		if within(sp, mins) {
			return true, true
		}
	}
	// Yesterday's openings that ran past midnight.
	for _, sp := range h.Days[(day+6)%7] {
		if o, c := minutes(sp.Open), minutes(sp.Close); c < o && mins < c {
			return true, true
		}
	}
	return false, true
}

func minutes(s string) int {
	if len(s) != 5 {
		return 0
	}
	return int(s[0]-'0')*600 + int(s[1]-'0')*60 + int(s[3]-'0')*10 + int(s[4]-'0')
}

// MapLink is a link to a map search for the card, or "": a link, never an
// embedded map (storefronts 6.1).
func (c Card) MapLink() string {
	q := c.MapQuery
	if q == "" && c.Address != "" {
		q = c.Name + ", " + c.Address
	}
	if q == "" {
		return ""
	}
	return "https://www.google.com/maps/search/?api=1&query=" + url.QueryEscape(q)
}

// WhatsAppLink is the business's wa.me chat link, or "".
func (c Card) WhatsAppLink() string {
	if c.WhatsApp == "" {
		return ""
	}
	return "https://wa.me/" + strings.TrimPrefix(c.WhatsApp, "+")
}
