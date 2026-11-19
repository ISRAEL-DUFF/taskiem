// Package discover is Discover (docs/spec/taskiem-for-everyone.md §10,
// migrations 00461, 00462 and 00466): the public finding businesses in the
// Taskiem app, kept apart from Explore (businesses finding businesses).
//
// A business is listed when its switch, "Found by the public in the
// Taskiem app", is on: on by default for a business with a published
// storefront, off for every other, never for a workflows workspace. Its
// listing (a name, a category, an area, a photo and hours, mostly from its
// published storefront and its profile) is shown only when it has all of
// these; its settings say what is missing.
//
// The listing is written to a search index (discover_index) from the
// business's own transaction (RefreshTx), whenever its settings change and,
// for every other source (its storefront, profile, photos, chat settings,
// areas, catalogue), by a sweep over the businesses the database marked
// (Sweep). Every writer of a business's listing holds its Discover lock
// (lockTx) from before it reads the settings until it commits, so an
// older read never overwrites a newer choice. Whether a listing may be
// shown (turned off, removed, its page suspended, hidden for reports) is
// kept in the index row itself by the database, so the search filters on
// a plain column. The public read it only through the definers
// taskiem_discover_search and taskiem_discover_profile, which return
// listing columns alone and only for listings that may be shown. A
// person's place, given for one search, is an argument and nothing more:
// searches are not kept.
package discover

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/areas"
	"github.com/israel-duff/taskiem/engine/chatlinks"
	"github.com/israel-duff/taskiem/engine/links"
	"github.com/israel-duff/taskiem/engine/storefront"
)

// ErrInvalid wraps a setting that cannot be kept.
var ErrInvalid = errors.New("discover: invalid")

// ErrWorkspace refuses Discover to a workflows workspace: only businesses
// are listed.
var ErrWorkspace = errors.New("discover: a workflows workspace is never listed in Discover")

// What a listing can be missing (Settings.Missing), in the order they are
// asked for.
const (
	MissingCategory = "category"
	MissingArea     = "area"
	MissingPhoto    = "photo"
	MissingHours    = "hours"
)

// Why a listing is not shown although it is on and complete.
const (
	// HiddenRemoved: Taskiem's moderators took it out of Discover.
	HiddenRemoved = "removed"
	// HiddenReported: reported by several people, until a moderator looks.
	HiddenReported = "reported"
	// HiddenSuspended: its storefront is suspended.
	HiddenSuspended = "suspended"
)

// Why a listing has no Chat button.
const (
	ChatOffOrders = "orders_off"
	ChatOffChats  = "chats_off"
)

// MaxPhotos is how many photos a profile in Discover shows.
const MaxPhotos = 8

// maxWords bounds what the index keeps of a business's words.
const maxWords = 20000

// maxItems is how many of a page's items lend their words to the search.
const maxItems = 500

// Location is a place on the map, to three decimals (about 100 metres).
type Location struct {
	Lat float64 `json:"lat"`
	Lng float64 `json:"lng"`
}

// Photo is one of a listing's photos: its sizes, and its caption.
type Photo struct {
	URLs    map[string]string `json:"urls"`
	Caption string            `json:"caption,omitempty"`
}

// Listing is a business as Discover shows it.
type Listing struct {
	Business uuid.UUID        `json:"business"`
	Name     string           `json:"name"`
	Tagline  string           `json:"tagline,omitempty"`
	Category string           `json:"category,omitempty"`
	Area     string           `json:"area,omitempty"`
	Address  string           `json:"address,omitempty"`
	MapLink  string           `json:"map_link,omitempty"`
	Photo    *Photo           `json:"photo,omitempty"`
	Photos   []Photo          `json:"photos"`
	Hours    storefront.Hours `json:"hours"`
	Location *Location        `json:"location,omitempty"`
	// Shop is its published storefront's address, when it has one.
	Shop string `json:"shop,omitempty"`
	// What it offers a person from Discover: Chat (through the customer
	// door), Their shop and Book (in the chat, as its tools).
	CanChat bool `json:"can_chat"`
	HasShop bool `json:"has_shop"`
	HasBook bool `json:"has_book"`
	// words is what the search reads.
	words string
}

// Settings are a business's Discover settings, and what follows from them.
type Settings struct {
	// Listed is whether the business is listed now: its choice, else the
	// default.
	Listed bool `json:"listed"`
	// Chosen is the business's own choice; nil leaves it to the default.
	Chosen *bool `json:"chosen"`
	// Default is what applies with no choice: on with a published
	// storefront.
	Default bool `json:"default"`
	// Workspace: a workflows workspace, never listed.
	Workspace bool `json:"workspace,omitempty"`
	// The listing's own fields, where the business gave them (they win
	// over its profile's and storefront's).
	Category string            `json:"category,omitempty"`
	Area     string            `json:"area,omitempty"`
	Hours    *storefront.Hours `json:"hours,omitempty"`
	Location *Location         `json:"location,omitempty"`
	// Missing is what the listing lacks to be shown (category, area,
	// photo, hours); MissingLine says it in words.
	Missing     []string `json:"missing"`
	MissingLine string   `json:"missing_line,omitempty"`
	// Shown: listed, complete and not hidden, so the public find it.
	Shown bool `json:"shown"`
	// Hidden says why a listing that is on and complete is not shown.
	Hidden string `json:"hidden,omitempty"`
	// RemovedNote is the moderators' reason, when they took it out.
	RemovedNote string `json:"removed_note,omitempty"`
	// ChatOff says why the listing has no Chat button: the Orders area is
	// off, or the business takes no chats.
	ChatOff string `json:"chat_off,omitempty"`
	// Listing is the listing as it would show.
	Listing Listing `json:"listing"`
	// WasRemoved: RemoveTx found it taken out by the moderators already.
	WasRemoved bool `json:"-"`
}

// stored is the business's row of discover_settings.
type stored struct {
	listed      *bool
	category    string
	area        string
	hours       *storefront.Hours
	loc         *Location
	removedAt   *time.Time
	removedNote string
}

func storedTx(ctx context.Context, tx pgx.Tx, tenant uuid.UUID) (stored, error) {
	var s stored
	var cat, area, note *string
	var hours []byte
	var lat, lng *float64
	err := tx.QueryRow(ctx, `SELECT listed, category, area, hours, lat, lng, removed_at, removed_note FROM discover_settings WHERE tenant_id = $1`, tenant).
		Scan(&s.listed, &cat, &area, &hours, &lat, &lng, &s.removedAt, &note)
	if errors.Is(err, pgx.ErrNoRows) {
		return s, nil
	}
	if err != nil {
		return s, err
	}
	if cat != nil {
		s.category = *cat
	}
	if area != nil {
		s.area = *area
	}
	if note != nil {
		s.removedNote = *note
	}
	if len(hours) > 0 {
		var h storefront.Hours
		if json.Unmarshal(hours, &h) == nil {
			s.hours = &h
		}
	}
	if lat != nil && lng != nil {
		s.loc = &Location{Lat: *lat, Lng: *lng}
	}
	return s, nil
}

// hoursKnown is whether hours say when the business opens at all.
func hoursKnown(h storefront.Hours) bool {
	_, known := h.OpenNow(time.Now())
	return known
}

// SettingsTx reads the business's settings and its listing as it would
// show, in its own scope (tx).
func SettingsTx(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, previewDomain string) (Settings, error) {
	out := Settings{Missing: []string{}}
	var kind, status string
	if err := tx.QueryRow(ctx, `SELECT name, kind, status FROM tenants WHERE id = $1`, tenant).Scan(&out.Listing.Name, &kind, &status); err != nil {
		return out, err
	}
	out.Listing.Business = tenant
	out.Workspace = kind != "business"
	st, err := storedTx(ctx, tx, tenant)
	if err != nil {
		return out, err
	}
	out.Chosen, out.Category, out.Area, out.Hours, out.Location = st.listed, st.category, st.area, st.hours, st.loc
	l := &out.Listing
	l.Photos = []Photo{}

	p, err := links.ProfileTx(ctx, tx, tenant)
	if err != nil {
		return out, err
	}
	sf, err := storefront.Get(ctx, tx, tenant)
	if err != nil && !errors.Is(err, storefront.ErrNotFound) {
		return out, err
	}
	published := err == nil && sf.Status == "published" && sf.PublishedVersion > 0
	suspended := err == nil && sf.Status == "suspended"
	var model storefront.Model
	if published {
		if model, err = storefront.PublishedModel(ctx, tx, tenant); err != nil {
			return out, err
		}
	}
	out.Default = published
	out.Listed = out.Default
	if st.listed != nil {
		out.Listed = *st.listed
	}
	if out.Workspace || status != "active" {
		out.Listed = false
	}

	// The words: the profile's, else the published page's.
	card := model.Card
	if card.Name != "" {
		l.Name = card.Name
	}
	if p.Name != "" {
		l.Name = p.Name
	}
	l.Tagline = firstOf(p.Tagline, card.Tagline)
	l.Area = firstOf(st.area, p.Area, card.Area)
	l.Category = st.category
	if l.Category == "" && len(p.Categories) > 0 {
		l.Category = p.Categories[0]
	}
	l.Address = card.Address
	if published {
		l.MapLink = model.MapLink
		l.Shop = sf.URL(previewDomain)
	}
	switch {
	case st.hours != nil && hoursKnown(*st.hours):
		l.Hours = *st.hours
	case published && hoursKnown(card.Hours):
		l.Hours = card.Hours
	}
	l.Location = st.loc

	// The photos: the business's public photo (its profile's logo, else
	// its page's, through taskiem_business_photo); then the gallery. A
	// photo the moderators rejected is never shown.
	logo, err := PhotoTx(ctx, tx, tenant)
	if err != nil {
		return out, err
	}
	if len(logo) > 0 {
		l.Photos = append(l.Photos, Photo{URLs: logo})
	}
	gallery, err := links.GalleryTx(ctx, tx, tenant)
	if err != nil {
		return out, err
	}
	for _, g := range gallery {
		if len(l.Photos) >= MaxPhotos {
			break
		}
		if g.Photo.Moderation != "rejected" && len(g.Photo.URLs) > 0 {
			l.Photos = append(l.Photos, Photo{URLs: g.Photo.URLs, Caption: g.Caption})
		}
	}
	if len(l.Photos) > 0 {
		first := l.Photos[0]
		l.Photo = &Photo{URLs: first.URLs}
	}

	// What it offers: Chat while it takes chats (off with the Orders area);
	// Their shop and Book with its page's items or services, and the
	// storefront and Orders areas on (the customer door's own rule).
	cs, err := chatlinks.SettingsTx(ctx, tx, tenant)
	if err != nil {
		return out, err
	}
	l.CanChat = cs.TakesChats
	switch {
	case cs.OrdersOff:
		out.ChatOff = ChatOffOrders
	case !cs.TakesChats:
		out.ChatOff = ChatOffChats
	}
	shopOn, err := areas.Enabled(ctx, tx, tenant, areas.Storefront)
	if err != nil {
		return out, err
	}
	ordersOn := !cs.OrdersOff
	l.HasShop = published && model.HasCatalog && shopOn && ordersOn
	l.HasBook = published && model.HasServices && shopOn && ordersOn

	if l.Category == "" || !links.IsCategory(l.Category) {
		out.Missing = append(out.Missing, MissingCategory)
	}
	if strings.TrimSpace(l.Area) == "" {
		out.Missing = append(out.Missing, MissingArea)
	}
	if l.Photo == nil {
		out.Missing = append(out.Missing, MissingPhoto)
	}
	if !hoursKnown(l.Hours) {
		out.Missing = append(out.Missing, MissingHours)
	}
	out.MissingLine = MissingWords(out.Missing)

	switch {
	case st.removedAt != nil:
		out.Hidden, out.RemovedNote = HiddenRemoved, st.removedNote
	case suspended:
		out.Hidden = HiddenSuspended
	}
	if out.Listed && len(out.Missing) == 0 && out.Hidden == "" {
		l.words, err = wordsTx(ctx, tx, tenant, l, sf, published)
		if err != nil {
			return out, err
		}
	}
	return out, nil
}

// complete says the listing can go into the index.
func (s Settings) complete() bool { return s.Listed && len(s.Missing) == 0 && s.Hidden == "" }

// MissingWords says what a listing lacks, in plain words ("" for nothing).
func MissingWords(missing []string) string {
	if len(missing) == 0 {
		return ""
	}
	words := map[string]string{MissingCategory: "a category", MissingArea: "an area", MissingPhoto: "a photo", MissingHours: "opening hours"}
	parts := []string{}
	for _, m := range missing {
		parts = append(parts, words[m])
	}
	list := parts[0]
	if len(parts) > 1 {
		list = strings.Join(parts[:len(parts)-1], ", ") + " and " + parts[len(parts)-1]
	}
	return "Customers can't find you in Discover until you add " + list + "."
}

func firstOf(s ...string) string {
	for _, x := range s {
		if x = strings.TrimSpace(x); x != "" {
			return x
		}
	}
	return ""
}

// wordsTx is what the search reads of a listing: its name, line,
// category, area and the names and categories of what its page offers.
func wordsTx(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, l *Listing, sf storefront.Storefront, published bool) (string, error) {
	parts := []string{l.Name, l.Tagline, l.Category, CategoryName(l.Category), l.Area}
	if published {
		rows, err := tx.Query(ctx, `SELECT COALESCE(item->>'name', ''), COALESCE(item->>'category', '') FROM storefront_catalogue
			WHERE tenant_id = $1 AND storefront_id = $2 AND COALESCE((item->>'available')::boolean, true) ORDER BY position LIMIT $3`, tenant, sf.ID, maxItems)
		if err != nil {
			return "", err
		}
		for rows.Next() {
			var name, cat string
			if err := rows.Scan(&name, &cat); err != nil {
				rows.Close()
				return "", err
			}
			parts = append(parts, name, cat)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return "", err
		}
	}
	seen := map[string]bool{}
	out := []string{}
	n := 0
	for _, p := range parts {
		for _, w := range strings.FieldsFunc(strings.ToLower(p), func(r rune) bool { return !isWordRune(r) }) {
			if seen[w] || utf8.RuneCountInString(w) < 2 {
				continue
			}
			if n+len(w)+1 > maxWords {
				return strings.Join(out, " "), nil
			}
			seen[w] = true
			out = append(out, w)
			n += len(w) + 1
		}
	}
	return strings.Join(out, " "), nil
}

func isWordRune(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) }

// CategoryName is a category's name, as people read it ("" for none).
func CategoryName(key string) string {
	for _, c := range links.Categories {
		if c.Key == key {
			return c.Name
		}
	}
	return ""
}

// PhotoTx is a business's public photo, its URLs by width (nil for
// none): its profile's logo, else its published page's, never one the
// moderators rejected. It is the database's taskiem_business_photo, which
// a person's Orders use too.
func PhotoTx(ctx context.Context, tx pgx.Tx, tenant uuid.UUID) (map[string]string, error) {
	var raw []byte
	if err := tx.QueryRow(ctx, `SELECT taskiem_business_photo($1)`, tenant).Scan(&raw); err != nil {
		return nil, err
	}
	var urls map[string]string
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &urls)
	}
	return urls, nil
}

// lockTx takes the business's Discover lock until tx ends: every writer
// of its listing (RefreshTx, SaveTx, RemoveTx, the sweep and the notice)
// holds it from before it reads the settings, so one cannot write the
// index from a read older than another's write. Taken again in the same
// transaction, it is already held.
func lockTx(ctx context.Context, tx pgx.Tx, tenant uuid.UUID) error {
	_, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('discover:' || $1::text, 0))`, tenant)
	return err
}

// tryLockTx is lockTx without waiting: false when another transaction
// holds it.
func tryLockTx(ctx context.Context, tx pgx.Tx, tenant uuid.UUID) (bool, error) {
	var ok bool
	err := tx.QueryRow(ctx, `SELECT pg_try_advisory_xact_lock(hashtextextended('discover:' || $1::text, 0))`, tenant).Scan(&ok)
	return ok, err
}

// afterRead, set only by tests, runs in RefreshTx between reading a
// listing's sources and writing the index.
var afterRead func(tenant uuid.UUID) error

// RefreshTx writes the business's listing to the index, or takes it out
// when it is not listed, not complete or hidden, in its own scope (tx),
// holding its Discover lock. It answers the settings as they stand.
func RefreshTx(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, previewDomain string) (Settings, error) {
	if err := lockTx(ctx, tx, tenant); err != nil {
		return Settings{}, err
	}
	st, err := SettingsTx(ctx, tx, tenant, previewDomain)
	if err != nil {
		return st, err
	}
	if afterRead != nil {
		if err := afterRead(tenant); err != nil {
			return st, err
		}
	}
	if !st.complete() {
		_, err = tx.Exec(ctx, `DELETE FROM discover_index WHERE tenant_id = $1`, tenant)
		return st, err
	}
	l := st.Listing
	photo, _ := json.Marshal(l.Photo.URLs)
	photos, _ := json.Marshal(l.Photos)
	hours, _ := json.Marshal(l.Hours)
	var lat, lng *float64
	if l.Location != nil {
		lat, lng = &l.Location.Lat, &l.Location.Lng
	}
	_, err = tx.Exec(ctx, `INSERT INTO discover_index (tenant_id, name, tagline, category, area, address, map_link, photo, photos, hours, lat, lng, words, shop,
		can_chat, has_shop, has_book, refreshed_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, now())
		ON CONFLICT (tenant_id) DO UPDATE SET name = EXCLUDED.name, tagline = EXCLUDED.tagline, category = EXCLUDED.category, area = EXCLUDED.area,
		  address = EXCLUDED.address, map_link = EXCLUDED.map_link, photo = EXCLUDED.photo, photos = EXCLUDED.photos, hours = EXCLUDED.hours,
		  lat = EXCLUDED.lat, lng = EXCLUDED.lng, words = EXCLUDED.words, shop = EXCLUDED.shop, can_chat = EXCLUDED.can_chat,
		  has_shop = EXCLUDED.has_shop, has_book = EXCLUDED.has_book, refreshed_at = now()`,
		tenant, l.Name, l.Tagline, l.Category, l.Area, l.Address, l.MapLink, photo, photos, hours, lat, lng, l.words, l.Shop,
		l.CanChat, l.HasShop, l.HasBook)
	if err != nil {
		return st, err
	}
	// Indexed, but perhaps hidden: the database keeps why in the row
	// (00466), as it was just written.
	var hidden *string
	if err := tx.QueryRow(ctx, `SELECT hidden FROM discover_index WHERE tenant_id = $1`, tenant).Scan(&hidden); err != nil {
		return st, err
	}
	switch {
	case hidden == nil:
		st.Shown = true
	case *hidden == HiddenReported || *hidden == HiddenRemoved || *hidden == HiddenSuspended:
		st.Hidden = *hidden
	}
	return st, nil
}

// Reports (§15, the founder's rule; taskiem_discover_reports in 00466): a
// report counts only when the reporter's account was at least 7 days old
// when they made it, and not when two or more of their earlier Discover
// reports were dismissed and none was acted on. FlagAtReporters counting
// reports from different people put the listing first in the moderators'
// queue; they hide it only when one of them is a scam report.
const (
	FlagAtReporters = 3
	// SeasonedAfter is how old a reporter's account must be.
	SeasonedAfter = 7 * 24 * time.Hour
)

// SettingsIn is what a business may change; a field left out keeps its
// value.
type SettingsIn struct {
	// Listed turns the listing on or off; Default (true) goes back to the
	// default.
	Listed  *bool `json:"listed"`
	Default bool  `json:"default"`
	// Category is one of the categories' keys; "" clears it (the
	// profile's is used).
	Category *string `json:"category"`
	// Area: "" clears it (the profile's or the page's is used).
	Area *string `json:"area"`
	// Hours: the listing's own, for a business whose page gives none;
	// ClearHours goes back to the page's.
	Hours      *storefront.Hours `json:"hours"`
	ClearHours bool              `json:"clear_hours"`
	// Location: the business's place on the map; ClearLocation takes it
	// off.
	Location      *Location `json:"location"`
	ClearLocation bool      `json:"clear_location"`
}

// SaveTx changes the business's settings (only the fields given) and
// writes its listing again, in its own scope, holding its Discover lock.
// A workflows workspace may not turn it on.
func SaveTx(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, in SettingsIn, by, previewDomain string) (Settings, error) {
	if err := lockTx(ctx, tx, tenant); err != nil {
		return Settings{}, err
	}
	var kind string
	if err := tx.QueryRow(ctx, `SELECT kind FROM tenants WHERE id = $1`, tenant).Scan(&kind); err != nil {
		return Settings{}, err
	}
	if kind != "business" && in.Listed != nil && *in.Listed {
		return Settings{}, ErrWorkspace
	}
	var cat, area *string
	if in.Category != nil {
		c := strings.TrimSpace(*in.Category)
		if c != "" && !links.IsCategory(c) {
			return Settings{}, fmt.Errorf("%w: %q is not a category", ErrInvalid, c)
		}
		cat = &c
	}
	if in.Area != nil {
		a := strings.Join(strings.Fields(*in.Area), " ")
		if utf8.RuneCountInString(a) > links.MaxArea {
			return Settings{}, fmt.Errorf("%w: the area is at most %d letters", ErrInvalid, links.MaxArea)
		}
		area = &a
	}
	var hours []byte
	if in.Hours != nil {
		if err := checkHours(*in.Hours); err != nil {
			return Settings{}, err
		}
		hours, _ = json.Marshal(in.Hours)
	}
	var lat, lng *float64
	if in.Location != nil {
		l := *in.Location
		if math.IsNaN(l.Lat) || math.IsNaN(l.Lng) || l.Lat < -90 || l.Lat > 90 || l.Lng < -180 || l.Lng > 180 {
			return Settings{}, fmt.Errorf("%w: a place is a latitude (-90 to 90) and a longitude (-180 to 180)", ErrInvalid)
		}
		la, ln := math.Round(l.Lat*1000)/1000, math.Round(l.Lng*1000)/1000
		lat, lng = &la, &ln
	}
	if _, err := tx.Exec(ctx, `INSERT INTO discover_settings (tenant_id, listed, category, area, hours, lat, lng, updated_by)
		VALUES ($1, $2, NULLIF($3, ''), NULLIF($4, ''), $5, $6, $7, $8)
		ON CONFLICT (tenant_id) DO UPDATE SET
		  listed = CASE WHEN $9 THEN NULL WHEN $2::boolean IS NOT NULL THEN $2 ELSE discover_settings.listed END,
		  category = CASE WHEN $3::text IS NOT NULL THEN NULLIF($3, '') ELSE discover_settings.category END,
		  area = CASE WHEN $4::text IS NOT NULL THEN NULLIF($4, '') ELSE discover_settings.area END,
		  hours = CASE WHEN $10 THEN NULL WHEN $5::jsonb IS NOT NULL THEN $5 ELSE discover_settings.hours END,
		  lat = CASE WHEN $11 THEN NULL WHEN $6::double precision IS NOT NULL THEN $6 ELSE discover_settings.lat END,
		  lng = CASE WHEN $11 THEN NULL WHEN $7::double precision IS NOT NULL THEN $7 ELSE discover_settings.lng END,
		  updated_by = EXCLUDED.updated_by, updated_at = now()`,
		tenant, in.Listed, cat, area, hours, lat, lng, by, in.Default, in.ClearHours, in.ClearLocation); err != nil {
		return Settings{}, err
	}
	return RefreshTx(ctx, tx, tenant, previewDomain)
}

// checkHours refuses hours a page would not take: times as "HH:MM".
func checkHours(h storefront.Hours) error {
	for _, d := range h.Days {
		if len(d) > 4 {
			return fmt.Errorf("%w: at most four openings a day", ErrInvalid)
		}
		for _, sp := range d {
			if !validTime(sp.Open) || !validTime(sp.Close) || sp.Open == sp.Close {
				return fmt.Errorf("%w: opening hours are times like 08:00 to 18:30", ErrInvalid)
			}
		}
	}
	if utf8.RuneCountInString(h.Note) > 140 {
		return fmt.Errorf("%w: the note on the hours is at most 140 letters", ErrInvalid)
	}
	return nil
}

func validTime(s string) bool {
	if len(s) != 5 || s[2] != ':' {
		return false
	}
	for _, i := range []int{0, 1, 3, 4} {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return (s[0]-'0')*10+(s[1]-'0') < 24 && s[3] < '6'
}

// RemoveTx takes a business's listing out of Discover for Taskiem's
// moderators (note says why), or puts it back (note empty, restore true),
// in an operators' transaction widened to the business (the definer
// taskiem_discover_moderate refuses any other), holding its Discover
// lock; open reports on the listing are marked acted on, or dismissed
// when it is put back.
func RemoveTx(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, restore bool, note, by, previewDomain string) (Settings, error) {
	if err := lockTx(ctx, tx, tenant); err != nil {
		return Settings{}, err
	}
	was, err := storedTx(ctx, tx, tenant)
	if err != nil {
		return Settings{}, err
	}
	status := "actioned"
	if restore {
		status = "dismissed"
	}
	if _, err := tx.Exec(ctx, `SELECT taskiem_discover_moderate($1, $2, $3, $4)`, tenant, restore, note, by); err != nil {
		return Settings{}, err
	}
	if _, err := tx.Exec(ctx, `UPDATE page_reports SET status = $3, handled_by = $2, handled_at = now()
		WHERE tenant_id = $1 AND kind = 'discover' AND site_id = $1 AND status = 'open'`, tenant, by, status); err != nil {
		return Settings{}, err
	}
	st, err := RefreshTx(ctx, tx, tenant, previewDomain)
	st.WasRemoved = was.removedAt != nil
	return st, err
}

// Result is one business a search found.
type Result struct {
	Business     uuid.UUID         `json:"business"`
	Name         string            `json:"name"`
	Photo        map[string]string `json:"photo"`
	Category     string            `json:"category"`
	CategoryName string            `json:"category_name"`
	Area         string            `json:"area"`
	// OpenNow by its hours, and the line under its name.
	OpenNow bool   `json:"open_now"`
	Line    string `json:"line"`
	CanChat bool   `json:"can_chat"`
	// Km is how far it is, when the person gave their place and the
	// business its own.
	Km *float64 `json:"km,omitempty"`
	// hours, for the line.
	hours storefront.Hours
}

// Query is one search.
type Query struct {
	Words    string    `json:"q"`
	Area     string    `json:"area"`
	Category string    `json:"category"`
	Near     *Location `json:"near"`
	Offset   int       `json:"offset"`
}

// PageSize is how many results a search answers at once, and MaxOffset
// the furthest a search pages.
const (
	PageSize  = 20
	MaxOffset = 200
)

// Querier runs the definers: a pool or a transaction.
type Querier interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// Search finds listings (at most PageSize, and whether more wait) through
// the narrow definer; nothing of the search is kept.
func Search(ctx context.Context, q Querier, in Query, now time.Time) ([]Result, bool, error) {
	var lat, lng *float64
	if in.Near != nil && !math.IsNaN(in.Near.Lat) && !math.IsNaN(in.Near.Lng) {
		lat, lng = &in.Near.Lat, &in.Near.Lng
	}
	rows, err := q.Query(ctx, `SELECT business, name, category, area, photo, hours, can_chat, km FROM taskiem_discover_search($1, $2, $3, $4, $5, $6, $7)`,
		in.Words, in.Area, in.Category, lat, lng, PageSize+1, in.Offset)
	if err != nil {
		return nil, false, err
	}
	out, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (Result, error) {
		var x Result
		var photo, hours []byte
		if err := r.Scan(&x.Business, &x.Name, &x.Category, &x.Area, &photo, &hours, &x.CanChat, &x.Km); err != nil {
			return x, err
		}
		_ = json.Unmarshal(photo, &x.Photo)
		_ = json.Unmarshal(hours, &x.hours)
		x.CategoryName = CategoryName(x.Category)
		x.OpenNow, x.Line = HoursLine(x.hours, now)
		return x, nil
	})
	if err != nil {
		return nil, false, err
	}
	// More only while the next page is within MaxOffset.
	more := len(out) > PageSize && in.Offset+PageSize <= MaxOffset
	if len(out) > PageSize {
		out = out[:PageSize]
	}
	if out == nil {
		out = []Result{}
	}
	return out, more, nil
}

// Profile is one business's profile in Discover.
type Profile struct {
	Listing
	CategoryName string `json:"category_name"`
	OpenNow      bool   `json:"open_now"`
	Line         string `json:"line"`
}

// ErrNotFound: no listing that may be shown.
var ErrNotFound = errors.New("discover: no such listing")

// ProfileOf reads a business's listing through the narrow definer.
func ProfileOf(ctx context.Context, q Querier, business uuid.UUID, now time.Time) (Profile, error) {
	var p Profile
	var photo, photos, hours []byte
	err := q.QueryRow(ctx, `SELECT business, name, tagline, category, area, address, map_link, photo, photos, hours, shop, can_chat, has_shop, has_book
		FROM taskiem_discover_profile($1)`, business).Scan(&p.Business, &p.Name, &p.Tagline, &p.Category, &p.Area, &p.Address, &p.MapLink,
		&photo, &photos, &hours, &p.Shop, &p.CanChat, &p.HasShop, &p.HasBook)
	if errors.Is(err, pgx.ErrNoRows) {
		return p, ErrNotFound
	}
	if err != nil {
		return p, err
	}
	p.Photo = &Photo{}
	_ = json.Unmarshal(photo, &p.Photo.URLs)
	p.Photos = []Photo{}
	_ = json.Unmarshal(photos, &p.Photos)
	_ = json.Unmarshal(hours, &p.Hours)
	p.CategoryName = CategoryName(p.Category)
	p.OpenNow, p.Line = HoursLine(p.Hours, now)
	return p, nil
}

// HoursLine is whether hours have the business open at now, and the line
// under its name: "Open now, closes at 18:00", "Closed now. Opens
// tomorrow at 09:00", or "" when it keeps no hours.
func HoursLine(h storefront.Hours, now time.Time) (bool, string) {
	open, known := h.OpenNow(now)
	if !known {
		return false, ""
	}
	t := now.In(storefront.Lagos)
	if open {
		if c := closingAt(h, t); c != "" {
			return true, "Open now, closes at " + c
		}
		return true, "Open now"
	}
	if when := nextOpening(h, t); when != "" {
		return false, "Closed now. Opens " + when
	}
	return false, "Closed now"
}

func minutesOf(s string) int {
	if !validTime(s) {
		return -1
	}
	return int(s[0]-'0')*600 + int(s[1]-'0')*60 + int(s[3]-'0')*10 + int(s[4]-'0')
}

// closingAt is when the opening that holds t closes ("" for none):
// today's, or yesterday's that ran past midnight (Friday 20:00 to 02:00
// holds Saturday 01:00).
func closingAt(h storefront.Hours, t time.Time) string {
	mins := t.Hour()*60 + t.Minute()
	today := (int(t.Weekday()) + 6) % 7
	for _, sp := range h.Days[today] {
		o, c := minutesOf(sp.Open), minutesOf(sp.Close)
		if o >= 0 && c >= 0 && mins >= o && (c <= o || mins < c) {
			return sp.Close
		}
	}
	for _, sp := range h.Days[(today+6)%7] {
		o, c := minutesOf(sp.Open), minutesOf(sp.Close)
		if o >= 0 && c >= 0 && c < o && mins < c {
			return sp.Close
		}
	}
	return ""
}

var weekdays = [7]string{"Monday", "Tuesday", "Wednesday", "Thursday", "Friday", "Saturday", "Sunday"}

// nextOpening says when the business next opens after t, within a week
// (today's weekday a week on included): "at 09:00", "tomorrow at 09:00",
// "on Monday at 09:00" or, for a business open only on today's weekday,
// "next Saturday at 09:00".
func nextOpening(h storefront.Hours, t time.Time) string {
	mins := t.Hour()*60 + t.Minute()
	today := (int(t.Weekday()) + 6) % 7
	for add := 0; add <= 7; add++ {
		day := (today + add) % 7
		spans := slices.Clone(h.Days[day])
		slices.SortFunc(spans, func(a, b storefront.Span) int { return minutesOf(a.Open) - minutesOf(b.Open) })
		for _, sp := range spans {
			o := minutesOf(sp.Open)
			if o < 0 || (add == 0 && o <= mins) {
				continue
			}
			switch add {
			case 0:
				return "at " + sp.Open
			case 1:
				return "tomorrow at " + sp.Open
			case 7:
				return "next " + weekdays[day] + " at " + sp.Open
			default:
				return "on " + weekdays[day] + " at " + sp.Open
			}
		}
	}
	return ""
}
