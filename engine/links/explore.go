package links

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Explore: finding a business by browsing (migration 00401). Every answer is
// about a business that chose to be found, or one we are actively linked with,
// and only what it said in public.

// Sections of the browse page.
const (
	SectionSearch = "search"
	SectionNear   = "near"
	SectionNew    = "new"
)

// ExploreCard is a business as Explore shows it: its profile (or storefront
// card), whether it has a logo, how many photos it shows (at most eight; each
// is at GET /v1/explore/{business}/photos/{photo}), and our link with it.
type ExploreCard struct {
	Business uuid.UUID `json:"business"`
	Name     string    `json:"name"`
	Slug     string    `json:"slug,omitempty"`
	Area     string    `json:"area,omitempty"`
	Tagline  string    `json:"tagline,omitempty"`
	About    string    `json:"about,omitempty"`
	Logo     bool      `json:"logo"`
	Photos   int       `json:"photos"`
	State    string    `json:"state"`
}

// ExploreTx lists businesses for a section: what a search for q finds
// (SectionSearch, at most 20), the findable businesses in our own area
// (SectionNear) or the newest (SectionNew); the last two page by offset and
// leave out the businesses already linked with.
func ExploreTx(ctx context.Context, tx pgx.Tx, section, q string, limit, offset int) ([]ExploreCard, error) {
	rows, err := tx.Query(ctx, `SELECT business, name, COALESCE(slug, ''), COALESCE(area, ''), COALESCE(tagline, ''), COALESCE(about, ''), has_logo, photos, state
		FROM taskiem_link_explore($1, $2, $3, $4)`, section, q, limit, offset)
	if err != nil {
		return nil, pgErr(err)
	}
	out, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (ExploreCard, error) {
		var c ExploreCard
		return c, r.Scan(&c.Business, &c.Name, &c.Slug, &c.Area, &c.Tagline, &c.About, &c.Logo, &c.Photos, &c.State)
	})
	if out == nil {
		out = []ExploreCard{}
	}
	return out, pgErr(err)
}

// PhotoURLsTx is the nth photo shown for a business, the sizes it was stored
// at, and its caption; ErrNotFound when there is no such photo, the business
// is not findable (or linked) or it is us: the same for each.
func PhotoURLsTx(ctx context.Context, tx pgx.Tx, business uuid.UUID, n int) (map[string]string, string, error) {
	var raw []byte
	var caption string
	err := tx.QueryRow(ctx, `SELECT urls, caption FROM taskiem_link_photo_list($1) WHERE n = $2`, business, n).Scan(&raw, &caption)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, "", ErrNotFound
		}
		return nil, "", pgErr(err)
	}
	urls := map[string]string{}
	if json.Unmarshal(raw, &urls) != nil || len(urls) == 0 {
		return nil, "", ErrNotFound
	}
	return urls, caption, nil
}

// ExploreLogoURLsTx is the logo of a business that is findable or linked.
func ExploreLogoURLsTx(ctx context.Context, tx pgx.Tx, business uuid.UUID) (map[string]string, error) {
	var raw []byte
	if err := tx.QueryRow(ctx, `SELECT taskiem_link_explore_logo($1)`, business).Scan(&raw); err != nil {
		return nil, pgErr(err)
	}
	urls := map[string]string{}
	if len(raw) == 0 || json.Unmarshal(raw, &urls) != nil || len(urls) == 0 {
		return nil, ErrNotFound
	}
	return urls, nil
}

// ValidSection reports whether s is a section Explore lists.
func ValidSection(s string) bool {
	return s == SectionSearch || s == SectionNear || s == SectionNew
}
