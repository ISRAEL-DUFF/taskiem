package links

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// A business's profile and gallery (migration 00399): what it says about
// itself to the businesses that find it, with or without a storefront. The
// profile's name, tagline, area and logo win over the storefront's card.

// Limits.
const (
	// MaxGallery is the most photos a business's gallery holds.
	MaxGallery = 12
	// MaxName, MaxTagline, MaxArea, MaxAbout and MaxCaption are the longest
	// text each field takes, in letters.
	MaxName    = 80
	MaxTagline = 140
	MaxArea    = 80
	MaxAbout   = 1000
	MaxCaption = 120
)

// ErrGalleryFull refuses a photo past MaxGallery.
var ErrGalleryFull = fmt.Errorf("%w: a gallery holds %d photos at most", ErrInvalid, MaxGallery)

// Photo is a stored image: the sizes it has (as storefront media).
type Photo struct {
	ID         uuid.UUID         `json:"id"`
	Width      int               `json:"width"`
	Height     int               `json:"height"`
	URLs       map[string]string `json:"urls"`
	Moderation string            `json:"moderation"`
}

// Profile is a business's own words about itself.
type Profile struct {
	Name    string `json:"name"`
	Tagline string `json:"tagline"`
	Area    string `json:"area"`
	About   string `json:"about"`
	Logo    *Photo `json:"logo"`
}

// GalleryPhoto is one photo of the gallery, in order.
type GalleryPhoto struct {
	ID      uuid.UUID `json:"id"`
	Caption string    `json:"caption"`
	Photo   Photo     `json:"photo"`
}

// ProfileIn is what a business may set; empty text clears a field.
type ProfileIn struct {
	Name    string `json:"name"`
	Tagline string `json:"tagline"`
	Area    string `json:"area"`
	About   string `json:"about"`
}

func clean(field, s string, max int) (string, error) {
	s = strings.TrimSpace(s)
	if utf8.RuneCountInString(s) > max {
		return "", fmt.Errorf("%w: %s is at most %d letters", ErrInvalid, field, max)
	}
	return s, nil
}

// Validate trims each field and refuses one that is too long.
func (p ProfileIn) Validate() (ProfileIn, error) {
	var err error
	if p.Name, err = clean("the name", p.Name, MaxName); err != nil {
		return p, err
	}
	if p.Tagline, err = clean("the one-line description", p.Tagline, MaxTagline); err != nil {
		return p, err
	}
	if p.Area, err = clean("the area", p.Area, MaxArea); err != nil {
		return p, err
	}
	if p.About, err = clean("the text about the business", p.About, MaxAbout); err != nil {
		return p, err
	}
	return p, nil
}

func photoByID(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, id uuid.UUID) (*Photo, error) {
	var p Photo
	var raw []byte
	err := tx.QueryRow(ctx, `SELECT id, width, height, urls, moderation FROM storefront_media WHERE tenant_id = $1 AND id = $2`, tenant, id).
		Scan(&p.ID, &p.Width, &p.Height, &raw, &p.Moderation)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	p.URLs = map[string]string{}
	_ = json.Unmarshal(raw, &p.URLs)
	return &p, nil
}

// ProfileTx is the business's profile (empty text where it has said nothing).
func ProfileTx(ctx context.Context, tx pgx.Tx, tenant uuid.UUID) (Profile, error) {
	var out Profile
	var logo *uuid.UUID
	err := tx.QueryRow(ctx, `SELECT COALESCE(name, ''), COALESCE(tagline, ''), COALESCE(area, ''), COALESCE(about, ''), logo_media FROM business_profile WHERE tenant_id = $1`, tenant).
		Scan(&out.Name, &out.Tagline, &out.Area, &out.About, &logo)
	if errors.Is(err, pgx.ErrNoRows) {
		return out, nil
	}
	if err != nil {
		return out, err
	}
	if logo != nil {
		if out.Logo, err = photoByID(ctx, tx, tenant, *logo); err != nil {
			return out, err
		}
	}
	return out, nil
}

// SaveProfileTx sets the profile's words (the logo is set apart).
func SaveProfileTx(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, in ProfileIn, by string) (Profile, error) {
	in, err := in.Validate()
	if err != nil {
		return Profile{}, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO business_profile (tenant_id, name, tagline, area, about, updated_by)
		VALUES ($1, NULLIF($2, ''), NULLIF($3, ''), NULLIF($4, ''), NULLIF($5, ''), $6)
		ON CONFLICT (tenant_id) DO UPDATE SET name = NULLIF($2, ''), tagline = NULLIF($3, ''), area = NULLIF($4, ''), about = NULLIF($5, ''), updated_by = $6, updated_at = now()`,
		tenant, in.Name, in.Tagline, in.Area, in.About, by); err != nil {
		return Profile{}, err
	}
	return ProfileTx(ctx, tx, tenant)
}

// SetLogoTx makes a stored image (a media id of the business's own) the
// logo, or takes the logo off with uuid.Nil. A rejected image is refused.
func SetLogoTx(ctx context.Context, tx pgx.Tx, tenant, media uuid.UUID, by string) (Profile, error) {
	var arg *uuid.UUID
	if media != uuid.Nil {
		p, err := photoByID(ctx, tx, tenant, media)
		if err != nil {
			return Profile{}, err
		}
		if p == nil {
			return Profile{}, fmt.Errorf("%w: no such image", ErrInvalid)
		}
		if p.Moderation == "rejected" {
			return Profile{}, fmt.Errorf("%w: this image was not accepted for a public page", ErrInvalid)
		}
		arg = &media
	}
	if _, err := tx.Exec(ctx, `INSERT INTO business_profile (tenant_id, logo_media, updated_by) VALUES ($1, $2, $3)
		ON CONFLICT (tenant_id) DO UPDATE SET logo_media = $2, updated_by = $3, updated_at = now()`, tenant, arg, by); err != nil {
		return Profile{}, err
	}
	return ProfileTx(ctx, tx, tenant)
}

// GalleryTx is the gallery in order.
func GalleryTx(ctx context.Context, tx pgx.Tx, tenant uuid.UUID) ([]GalleryPhoto, error) {
	rows, err := tx.Query(ctx, `SELECT g.id, COALESCE(g.caption, ''), m.id, m.width, m.height, m.urls, m.moderation
		FROM business_gallery g JOIN storefront_media m ON m.id = g.media_id AND m.tenant_id = g.tenant_id
		WHERE g.tenant_id = $1 ORDER BY g.position, g.created_at, g.id`, tenant)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []GalleryPhoto{}
	for rows.Next() {
		var g GalleryPhoto
		var raw []byte
		if err := rows.Scan(&g.ID, &g.Caption, &g.Photo.ID, &g.Photo.Width, &g.Photo.Height, &raw, &g.Photo.Moderation); err != nil {
			return nil, err
		}
		g.Photo.URLs = map[string]string{}
		_ = json.Unmarshal(raw, &g.Photo.URLs)
		out = append(out, g)
	}
	return out, rows.Err()
}

// AddPhotoTx puts a stored image (a media id of the business's own) at the
// end of the gallery. The same image twice is one photo; a rejected image
// and a thirteenth photo are refused.
func AddPhotoTx(ctx context.Context, tx pgx.Tx, tenant, media uuid.UUID, caption string) (GalleryPhoto, error) {
	caption, err := clean("a caption", caption, MaxCaption)
	if err != nil {
		return GalleryPhoto{}, err
	}
	p, err := photoByID(ctx, tx, tenant, media)
	if err != nil {
		return GalleryPhoto{}, err
	}
	if p == nil {
		return GalleryPhoto{}, fmt.Errorf("%w: no such image", ErrInvalid)
	}
	if p.Moderation == "rejected" {
		return GalleryPhoto{}, fmt.Errorf("%w: this image was not accepted for a public page", ErrInvalid)
	}
	// One at a time per business, so two uploads cannot both take the last place.
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('business_gallery:' || $1::text, 0))`, tenant); err != nil {
		return GalleryPhoto{}, err
	}
	var n int
	var dup bool
	if err := tx.QueryRow(ctx, `SELECT count(*), COALESCE(bool_or(media_id = $2), false) FROM business_gallery WHERE tenant_id = $1`, tenant, media).Scan(&n, &dup); err != nil {
		return GalleryPhoto{}, err
	}
	if !dup && n >= MaxGallery {
		return GalleryPhoto{}, ErrGalleryFull
	}
	if !dup {
		if _, err := tx.Exec(ctx, `INSERT INTO business_gallery (id, tenant_id, media_id, caption, position)
			VALUES ($1, $2, $3, NULLIF($4, ''), $5)`, uuid.Must(uuid.NewV7()), tenant, media, caption, n); err != nil {
			return GalleryPhoto{}, err
		}
	}
	all, err := GalleryTx(ctx, tx, tenant)
	if err != nil {
		return GalleryPhoto{}, err
	}
	for _, g := range all {
		if g.Photo.ID == media {
			return g, nil
		}
	}
	return GalleryPhoto{}, ErrNotFound
}

// CaptionTx sets (or clears) a photo's caption.
func CaptionTx(ctx context.Context, tx pgx.Tx, tenant, id uuid.UUID, caption string) error {
	caption, err := clean("a caption", caption, MaxCaption)
	if err != nil {
		return err
	}
	tag, err := tx.Exec(ctx, `UPDATE business_gallery SET caption = NULLIF($3, '') WHERE tenant_id = $1 AND id = $2`, tenant, id, caption)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// RemovePhotoTx takes a photo out of the gallery (the image itself stays in
// the business's media) and closes the gap in the order.
func RemovePhotoTx(ctx context.Context, tx pgx.Tx, tenant, id uuid.UUID) error {
	tag, err := tx.Exec(ctx, `DELETE FROM business_gallery WHERE tenant_id = $1 AND id = $2`, tenant, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	_, err = tx.Exec(ctx, `UPDATE business_gallery g SET position = r.n FROM
		(SELECT id, (row_number() OVER (ORDER BY position, created_at, id) - 1)::int AS n FROM business_gallery WHERE tenant_id = $1) r
		WHERE g.id = r.id AND g.tenant_id = $1`, tenant)
	return err
}

// ReorderTx puts the gallery in the order of ids, which must be every
// photo in it, once each.
func ReorderTx(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, ids []uuid.UUID) error {
	have, err := GalleryTx(ctx, tx, tenant)
	if err != nil {
		return err
	}
	if len(ids) != len(have) {
		return fmt.Errorf("%w: the order names every photo once", ErrInvalid)
	}
	want := map[uuid.UUID]bool{}
	for _, g := range have {
		want[g.ID] = true
	}
	for _, id := range ids {
		if !want[id] {
			return fmt.Errorf("%w: the order names every photo once", ErrInvalid)
		}
		delete(want, id)
	}
	for i, id := range ids {
		if _, err := tx.Exec(ctx, `UPDATE business_gallery SET position = $3 WHERE tenant_id = $1 AND id = $2`, tenant, id, i); err != nil {
			return err
		}
	}
	return nil
}

// Shown is how a linked business shows in chat: the name it chose (empty when
// it has chosen none) and whether it has a logo.
type Shown struct {
	Name    string
	HasLogo bool
}

// ShownTx is the profile name and logo of each business the business in scope
// is actively linked with and that has said something (migration 00400).
func ShownTx(ctx context.Context, tx pgx.Tx) (map[uuid.UUID]Shown, error) {
	rows, err := tx.Query(ctx, `SELECT business, COALESCE(name, ''), has_logo FROM taskiem_link_profiles()`)
	if err != nil {
		return nil, pgErr(err)
	}
	defer rows.Close()
	out := map[uuid.UUID]Shown{}
	for rows.Next() {
		var b uuid.UUID
		var s Shown
		if err := rows.Scan(&b, &s.Name, &s.HasLogo); err != nil {
			return nil, err
		}
		out[b] = s
	}
	return out, rows.Err()
}

// LogoURLsTx is a linked business's logo, the sizes it was stored at; it is
// ErrNotFound for a business we are not linked with, one with no logo and one
// whose logo was refused, the same for each.
func LogoURLsTx(ctx context.Context, tx pgx.Tx, business uuid.UUID) (map[string]string, error) {
	var raw []byte
	if err := tx.QueryRow(ctx, `SELECT taskiem_link_logo($1)`, business).Scan(&raw); err != nil {
		return nil, pgErr(err)
	}
	if len(raw) == 0 {
		return nil, ErrNotFound
	}
	urls := map[string]string{}
	if err := json.Unmarshal(raw, &urls); err != nil || len(urls) == 0 {
		return nil, ErrNotFound
	}
	return urls, nil
}
