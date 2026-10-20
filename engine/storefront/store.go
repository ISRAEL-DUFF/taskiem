package storefront

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/israel-duff/taskiem/engine/media"
)

// MaxItems caps the catalogue until the plans set their own (S8).
const MaxItems = 500

var (
	// ErrNotFound is a tenant with no storefront yet.
	ErrNotFound = errors.New("storefront: none yet")
	// ErrSlugTaken refuses a slug another storefront has.
	ErrSlugTaken = errors.New("storefront: the address is taken")
	// ErrNotPublished refuses closing or reopening a draft.
	ErrNotPublished = errors.New("storefront: not published")
)

// Storefront is the owner's view.
type Storefront struct {
	ID               uuid.UUID  `json:"id"`
	Slug             string     `json:"slug"`
	Status           string     `json:"status"`
	Draft            Card       `json:"draft"`
	CatalogueTable   *uuid.UUID `json:"catalogue_table"`
	CatalogueColumns Columns    `json:"catalogue_columns"`
	// The services table and its mapping, and when bookings are taken.
	ServicesTable    *uuid.UUID   `json:"services_table"`
	ServicesColumns  Columns      `json:"services_columns"`
	Availability     Availability `json:"availability"`
	PublishedVersion int          `json:"published_version"`
	// PublishedSlug is the slug the published version serves at (the
	// draft's may differ until it is published).
	PublishedSlug *string `json:"published_slug"`
	// LogoURLs are the draft's logo's URLs by width, unless rejected.
	LogoURLs        Image      `json:"logo_urls,omitempty"`
	PublishedAt     *time.Time `json:"published_at"`
	ClosedMessage   *string    `json:"closed_message"`
	CatalogueSynced *time.Time `json:"catalogue_synced_at"`
	CatalogueError  *string    `json:"catalogue_error"`
	UpdatedAt       time.Time  `json:"updated_at"`
	// Domain is the primary of the business's own verified domains
	// (domains.go), where the page is served once published.
	Domain *string `json:"domain"`
}

// URL is the published page's public address: its own primary domain
// when it has one, else the preview address (PreviewURL); "" before it
// is published. Every link, QR code and card uses it.
func (s Storefront) URL(previewDomain string) string {
	if s.Domain != nil && s.PublishedSlug != nil && *s.PublishedSlug != "" {
		return "https://" + *s.Domain
	}
	return PreviewURL(s.PublishedSlug, previewDomain)
}

// Image is a published image's URLs by width.
type Image map[string]string

// Model is what a published snapshot holds: everything the page shows
// but the catalogue's rows, which are read from the copy.
type Model struct {
	Version    int    `json:"version"`
	Slug       string `json:"slug"`
	Card       Card   `json:"card"`
	Logo       Image  `json:"logo,omitempty"`
	MapLink    string `json:"map_link,omitempty"`
	WhatsLink  string `json:"whatsapp_link,omitempty"`
	HasCatalog bool   `json:"has_catalogue"`
	// HasServices: the page lists services to book.
	HasServices bool `json:"has_services,omitempty"`
	// Actions are the forms the page offers.
	Actions     []PublicAction `json:"actions,omitempty"`
	PublishedAt time.Time      `json:"published_at"`
}

// Item is one catalogue item as pages show it.
type Item struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	PriceKobo   *int64 `json:"price_kobo,omitempty"`
	Description string `json:"description,omitempty"`
	Photo       Image  `json:"photo,omitempty"`
	Available   bool   `json:"available"`
	Category    string `json:"category,omitempty"`
	// Stock is how many are left, when the catalogue tracks it.
	Stock *int64 `json:"stock,omitempty"`
	// A service's length, customers at once and time kept free after.
	DurationMinutes int `json:"duration_minutes,omitempty"`
	Capacity        int `json:"capacity,omitempty"`
	BufferMinutes   int `json:"buffer_minutes,omitempty"`
}

// SoldOut reports whether the item tracks stock and has none left.
func (it Item) SoldOut() bool { return it.Stock != nil && *it.Stock <= 0 }

const cols = `id, slug, status, draft, catalogue_table, catalogue_columns, published_version, published_slug, published_at, closed_message,
	catalogue_synced_at, catalogue_error, updated_at, services_table, services_columns, availability,
	(SELECT m.urls FROM storefront_media m WHERE m.tenant_id = storefronts.tenant_id AND m.id::text = storefronts.draft->>'logo' AND m.moderation <> 'rejected'),
	(SELECT d.host FROM storefront_domains d WHERE d.storefront_id = storefronts.id AND d.verified_at IS NOT NULL
	  ORDER BY d.is_primary DESC, d.verified_at, d.host LIMIT 1)`

func scan(r pgx.Row) (Storefront, error) {
	var s Storefront
	var draft, columns, scols, avail, logo []byte
	err := r.Scan(&s.ID, &s.Slug, &s.Status, &draft, &s.CatalogueTable, &columns, &s.PublishedVersion, &s.PublishedSlug, &s.PublishedAt,
		&s.ClosedMessage, &s.CatalogueSynced, &s.CatalogueError, &s.UpdatedAt, &s.ServicesTable, &scols, &avail, &logo, &s.Domain)
	if errors.Is(err, pgx.ErrNoRows) {
		return Storefront{}, ErrNotFound
	}
	if err != nil {
		return Storefront{}, err
	}
	_ = json.Unmarshal(draft, &s.Draft)
	_ = json.Unmarshal(columns, &s.CatalogueColumns)
	_ = json.Unmarshal(scols, &s.ServicesColumns)
	_ = json.Unmarshal(avail, &s.Availability)
	if logo != nil {
		_ = json.Unmarshal(logo, &s.LogoURLs)
	}
	return s, nil
}

// Get reads the tenant's storefront.
func Get(ctx context.Context, tx pgx.Tx, tenant uuid.UUID) (Storefront, error) {
	return scan(tx.QueryRow(ctx, `SELECT `+cols+` FROM storefronts WHERE tenant_id = $1`, tenant))
}

// Save creates or updates the tenant's draft. The card is checked when
// published, not here, so an owner can save half a page.
func Save(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, slug string, card Card, table *uuid.UUID, columns Columns, actor string) (Storefront, error) {
	slug, err := CheckSlug(slug)
	if err != nil {
		return Storefront{}, err
	}
	d, _ := json.Marshal(card)
	c, _ := json.Marshal(columns)
	s, err := scan(tx.QueryRow(ctx, `INSERT INTO storefronts (id, tenant_id, slug, draft, catalogue_table, catalogue_columns, created_by)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		ON CONFLICT (tenant_id) DO UPDATE SET slug = EXCLUDED.slug, draft = EXCLUDED.draft, catalogue_table = EXCLUDED.catalogue_table,
		  catalogue_columns = EXCLUDED.catalogue_columns, updated_at = now()
		RETURNING `+cols, uuid.Must(uuid.NewV7()), tenant, slug, d, table, c, actor))
	var pe *pgconn.PgError
	if errors.As(err, &pe) && pe.Code == "23505" {
		return Storefront{}, fmt.Errorf("%w: %s", ErrSlugTaken, slug)
	}
	return s, err
}

// Publish writes a snapshot of the draft as the next version, claims the
// preview host previewDomain gives (<slug>.<previewDomain>) and makes the
// storefront published. The catalogue must have been synced first
// (SyncCatalogue), in the same transaction or before.
func Publish(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, previewDomain, actor string) (Storefront, error) {
	s, err := Get(ctx, tx, tenant)
	if err != nil {
		return Storefront{}, err
	}
	card := s.Draft
	if err := card.Check(); err != nil {
		return Storefront{}, err
	}
	if previewDomain == "" {
		return Storefront{}, fmt.Errorf("%w: the public host has no preview domain (TASKIEM_PUBLIC_DOMAINS)", ErrInvalid)
	}
	m := Model{Version: s.PublishedVersion + 1, Slug: s.Slug, Card: card, MapLink: card.MapLink(), WhatsLink: card.WhatsAppLink(),
		HasCatalog: s.CatalogueTable != nil, HasServices: s.ServicesTable != nil, PublishedAt: time.Now().UTC()}
	if card.Logo != "" {
		img, err := mediaImage(ctx, tx, tenant, card.Logo)
		if err != nil {
			return Storefront{}, err
		}
		m.Logo = img
	}
	if m.Actions, err = publicActions(ctx, tx, tenant); err != nil {
		return Storefront{}, err
	}
	raw, _ := json.Marshal(m)
	sum := sha256.Sum256(raw)
	var ok bool
	if err := tx.QueryRow(ctx, `SELECT taskiem_storefront_claim_host($1, $2)`, s.ID, s.Slug+"."+strings.ToLower(previewDomain)).Scan(&ok); err != nil {
		return Storefront{}, err
	}
	if !ok {
		return Storefront{}, fmt.Errorf("%w: %s.%s", ErrSlugTaken, s.Slug, previewDomain)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO storefront_snapshots (storefront_id, version, tenant_id, model, hash, created_by) VALUES ($1, $2, $3, $4, $5, $6)`,
		s.ID, m.Version, tenant, raw, sum[:], actor); err != nil {
		return Storefront{}, err
	}
	return scan(tx.QueryRow(ctx, `UPDATE storefronts SET status = CASE WHEN status = 'closed' THEN 'closed' ELSE 'published' END,
		published_version = $2, published_slug = slug, published_at = now(), updated_at = now() WHERE tenant_id = $1 RETURNING `+cols, tenant, m.Version))
}

// DefaultClosedMessage is what a page closed without a message says.
const DefaultClosedMessage = "We are closed for now. Please check back soon."

// CheckClosedMessage refuses a closed message longer than 200 characters
// or holding a link.
func CheckClosedMessage(message string) error {
	if len([]rune(message)) > 200 || hasLink(message) {
		return fmt.Errorf("%w: a closed message is up to 200 characters, with no links", ErrInvalid)
	}
	return nil
}

// Close shows message instead of the catalogue until reopened.
func Close(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, message string) (Storefront, error) {
	message = strings.TrimSpace(message)
	if err := CheckClosedMessage(message); err != nil {
		return Storefront{}, err
	}
	if message == "" {
		message = DefaultClosedMessage
	}
	s, err := scan(tx.QueryRow(ctx, `UPDATE storefronts SET status = 'closed', closed_message = $2, updated_at = now()
		WHERE tenant_id = $1 AND status IN ('published', 'closed') RETURNING `+cols, tenant, message))
	if errors.Is(err, ErrNotFound) {
		return Storefront{}, ErrNotPublished
	}
	return s, err
}

// Reopen undoes Close.
func Reopen(ctx context.Context, tx pgx.Tx, tenant uuid.UUID) (Storefront, error) {
	s, err := scan(tx.QueryRow(ctx, `UPDATE storefronts SET status = 'published', closed_message = NULL, updated_at = now()
		WHERE tenant_id = $1 AND status IN ('published', 'closed') RETURNING `+cols, tenant))
	if errors.Is(err, ErrNotFound) {
		return Storefront{}, ErrNotPublished
	}
	return s, err
}

// Moderator checks an image before it may be shown (storefronts 7.4).
// The moderation pipeline is S8's; until then none is set and images are
// recorded as unreviewed.
type Moderator interface {
	Check(ctx context.Context, tenant uuid.UUID, img media.Image) (verdict, reason string, err error)
}

// Media is a stored storefront image.
type Media struct {
	ID         uuid.UUID `json:"id"`
	Width      int       `json:"width"`
	Height     int       `json:"height"`
	URLs       Image     `json:"urls"`
	Moderation string    `json:"moderation"`
}

// AddMedia records an image stored with media.PutImage, after the
// moderator's verdict. The same image twice is one record.
func AddMedia(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, img media.Image, verdict, reason, actor string) (Media, error) {
	if verdict == "" {
		verdict = "unreviewed"
	}
	urls := Image{}
	for w, u := range img.URLs {
		urls[strconv.Itoa(w)] = u
	}
	raw, _ := json.Marshal(urls)
	m := Media{Width: img.Width, Height: img.Height, URLs: urls, Moderation: verdict}
	err := tx.QueryRow(ctx, `INSERT INTO storefront_media (id, tenant_id, hash, width, height, type, urls, paths, moderation, moderation_reason, created_by)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, NULLIF($10, ''), $11)
		ON CONFLICT (tenant_id, hash) DO UPDATE SET moderation = CASE WHEN storefront_media.moderation = 'rejected' THEN 'rejected' ELSE EXCLUDED.moderation END
		RETURNING id, moderation`, uuid.Must(uuid.NewV7()), tenant, img.Hash, img.Width, img.Height, img.Type, raw, img.Paths, verdict, reason, actor).
		Scan(&m.ID, &m.Moderation)
	return m, err
}

// mediaImage is a media record's URLs, refusing rejected or unknown ids.
func mediaImage(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, id string) (Image, error) {
	mid, err := uuid.Parse(id)
	if err != nil {
		return nil, fmt.Errorf("%w: %q is not a media id", ErrInvalid, id)
	}
	var raw []byte
	var mod string
	err = tx.QueryRow(ctx, `SELECT urls, moderation FROM storefront_media WHERE tenant_id = $1 AND id = $2`, tenant, mid).Scan(&raw, &mod)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("%w: no image %s", ErrInvalid, id)
	}
	if err != nil {
		return nil, err
	}
	if mod == "rejected" {
		return nil, fmt.Errorf("%w: image %s was not accepted", ErrInvalid, id)
	}
	var img Image
	_ = json.Unmarshal(raw, &img)
	return img, nil
}

// --- the catalogue copy ---

// Rows reads every row of a data table (in pages, up to MaxItems + 1).
type Rows func(ctx context.Context, tenant uuid.UUID, table string, limit int) ([]map[string]any, error)

// Items maps a table's rows to catalogue items by the mapping, resolving
// photos through the tenant's media. Rows without a name are left out;
// rejected or unknown photos are dropped, never the item.
func Items(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, rows []map[string]any, m Columns) ([]Item, error) {
	if len(rows) > MaxItems {
		return nil, fmt.Errorf("%w: the catalogue has more than %d items", ErrInvalid, MaxItems)
	}
	type sorted struct {
		item Item
		key  float64
		pos  int
	}
	var out []sorted
	for i, r := range rows {
		it := Item{ID: fmt.Sprint(r["id"]), Name: str(r[m.Name]), Available: true}
		if it.Name == "" {
			continue
		}
		if m.Description != "" {
			it.Description = str(r[m.Description])
		}
		if m.Category != "" {
			it.Category = str(r[m.Category])
		}
		if m.Available != "" {
			if b, ok := r[m.Available].(bool); ok {
				it.Available = b
			}
		}
		if m.Stock != "" {
			if f, ok := num(r[m.Stock]); ok {
				n := max(0, int64(f))
				it.Stock = &n
			}
		}
		for _, f := range []struct {
			col      string
			v        *int
			min, max int
		}{{m.Duration, &it.DurationMinutes, 5, 480}, {m.Capacity, &it.Capacity, 1, 1000}, {m.Buffer, &it.BufferMinutes, 0, 480}} {
			if f.col == "" {
				continue
			}
			if n, ok := num(r[f.col]); ok && int(n) >= f.min && int(n) <= f.max {
				*f.v = int(n)
			}
		}
		if m.Price != "" {
			if f, ok := num(r[m.Price]); ok {
				k := f
				if m.PriceUnit != "kobo" {
					k = f * 100
				}
				kobo := int64(math.Round(k))
				it.PriceKobo = &kobo
			}
		}
		if m.Photo != "" {
			if id := str(r[m.Photo]); id != "" {
				if img, err := mediaImage(ctx, tx, tenant, id); err == nil {
					it.Photo = img
				} else if !errors.Is(err, ErrInvalid) {
					return nil, err
				}
			}
		}
		key := float64(i)
		if m.Sort != "" {
			if f, ok := num(r[m.Sort]); ok {
				key = f
			}
		}
		out = append(out, sorted{it, key, i})
	}
	sort.SliceStable(out, func(a, b int) bool { return out[a].key < out[b].key })
	items := make([]Item, len(out))
	for i, s := range out {
		items[i] = s.item
	}
	return items, nil
}

// SyncCatalogue replaces the storefront's copy with items, and records
// when. On failure the old copy stays and the error is recorded.
func SyncCatalogue(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, items []Item) error {
	s, err := Get(ctx, tx, tenant)
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM storefront_catalogue WHERE storefront_id = $1`, s.ID); err != nil {
		return err
	}
	for i, it := range items {
		raw, _ := json.Marshal(it)
		if _, err := tx.Exec(ctx, `INSERT INTO storefront_catalogue (storefront_id, row_id, tenant_id, position, item) VALUES ($1, $2, $3, $4, $5)
			ON CONFLICT (storefront_id, row_id) DO NOTHING`, s.ID, it.ID, tenant, i, raw); err != nil {
			return err
		}
	}
	_, err = tx.Exec(ctx, `UPDATE storefronts SET catalogue_synced_at = now(), catalogue_error = NULL WHERE tenant_id = $1`, tenant)
	return err
}

// CatalogueFailed records why the copy could not be refreshed.
func CatalogueFailed(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, cause error) error {
	msg := cause.Error()
	if len(msg) > 300 {
		msg = msg[:300]
	}
	_, err := tx.Exec(ctx, `UPDATE storefronts SET catalogue_synced_at = now(), catalogue_error = $2 WHERE tenant_id = $1`, tenant, msg)
	return err
}

// Catalogue reads the copy, for the owner's preview.
func Catalogue(ctx context.Context, tx pgx.Tx, tenant uuid.UUID) ([]Item, error) {
	rows, err := tx.Query(ctx, `SELECT c.item FROM storefront_catalogue c JOIN storefronts s ON s.id = c.storefront_id
		WHERE s.tenant_id = $1 ORDER BY c.position`, tenant)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (Item, error) {
		var raw []byte
		var it Item
		if err := r.Scan(&raw); err != nil {
			return it, err
		}
		return it, json.Unmarshal(raw, &it)
	})
}

func str(v any) string {
	switch x := v.(type) {
	case string:
		return strings.TrimSpace(x)
	case nil:
		return ""
	}
	return strings.TrimSpace(fmt.Sprint(v))
}

func num(v any) (float64, bool) {
	switch x := v.(type) {
	case float64:
		return x, true
	case int64:
		return float64(x), true
	case int:
		return float64(x), true
	case json.Number:
		f, err := x.Float64()
		return f, err == nil
	case string:
		f, err := strconv.ParseFloat(strings.TrimSpace(x), 64)
		return f, err == nil
	}
	return 0, false
}

// FormatNaira writes kobo as ₦1,234 (or ₦1,234.50).
func FormatNaira(kobo int64) string {
	neg := kobo < 0
	if neg {
		kobo = -kobo
	}
	whole, frac := kobo/100, kobo%100
	s := strconv.FormatInt(whole, 10)
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	if frac != 0 {
		s += fmt.Sprintf(".%02d", frac)
	}
	if neg {
		s = "-" + s
	}
	return "₦" + s
}
