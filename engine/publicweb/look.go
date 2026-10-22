package publicweb

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/israel-duff/taskiem/engine/brand"
	"github.com/israel-duff/taskiem/engine/lru"
)

// A business's look (N13, decision 0042): every public page of it is
// drawn in its theme, from that theme's own templates, in its colour and
// with its logo and cover photo.

// Brands reads a business's brand; ok is false for an inactive tenant.
type Brands func(ctx context.Context, tenant uuid.UUID) (b brand.Brand, ok bool, err error)

// DBBrands reads brands with taskiem_public_brand.
func DBBrands(pool *pgxpool.Pool) Brands {
	return func(ctx context.Context, tenant uuid.UUID) (brand.Brand, bool, error) {
		var raw []byte
		err := pool.QueryRow(ctx, `SELECT taskiem_public_brand($1)`, tenant).Scan(&raw)
		if errors.Is(err, pgx.ErrNoRows) || err == nil && raw == nil {
			return brand.Brand{}, false, nil
		}
		if err != nil {
			return brand.Brand{}, false, err
		}
		var b brand.Brand
		if err := json.Unmarshal(raw, &b); err != nil {
			return brand.Brand{}, false, err
		}
		return b, true, nil
	}
}

type brandEntry struct {
	b  brand.Brand
	ok bool
}

// look is what every template is given about the business's look.
type look struct {
	// Theme names the template set the page is drawn from.
	Theme string
	// CSS is the stylesheet holding the business's colours.
	CSS string
	// Logo and Cover are image URLs (320 and 800 wide for a logo, 800 and
	// 2000 for a cover); empty when there is none. Initials stand in for
	// a missing logo.
	Logo, LogoLarge   string
	Cover, CoverLarge string
	Initials          string
}

// ThemeCSS is the theme's stylesheet.
func (l look) ThemeCSS() string { return "theme-" + l.theme() + ".css" }

func (l look) theme() string { return brand.Theme(l.Theme) }

// lookOf makes a look from a brand, for a business by name.
func lookOf(b brand.Brand, business string) look {
	l := look{Theme: brand.Theme(b.Theme), CSS: brandCSSPath(b.Color), Initials: brand.Initials(business)}
	if b.Logo != "" {
		l.Logo, l.LogoLarge = first(b.LogoURLs, "320", "800"), first(b.LogoURLs, "800", "2000")
	}
	if b.Cover != "" {
		l.Cover, l.CoverLarge = first(b.CoverURLs, "800", "2000"), first(b.CoverURLs, "2000", "800")
	}
	return l
}

func first(urls map[string]string, keys ...string) string {
	for _, k := range keys {
		if u := urls[k]; u != "" {
			return u
		}
	}
	return ""
}

// defaultLook is Taskiem's own: pages that belong to no business.
func defaultLook() look { return lookOf(brand.Brand{}, "") }

// lookFor reads a business's look through a cache as short as the host
// cache's; an unknown business, or a failed read, gets the default look
// rather than no page.
func (s *Server) lookFor(ctx context.Context, tenant uuid.UUID, business string) look {
	if s.Brands == nil || tenant == uuid.Nil {
		return lookOf(brand.Brand{}, business)
	}
	if s.brands == nil {
		s.brands = lru.New[uuid.UUID, brandEntry](HostCacheSize, HostTTL)
	}
	e, ok := s.brands.Get(tenant)
	if !ok {
		b, found, err := s.Brands(ctx, tenant)
		if err != nil {
			if !errors.Is(err, context.Canceled) {
				s.Logger.Error("public host: brand", "tenant", tenant, "err", err)
			}
			return lookOf(brand.Brand{}, business)
		}
		e = brandEntry{b, found}
		s.brands.Put(tenant, e)
	}
	return lookOf(e.b, business)
}

// brandPrefix is where a colour's stylesheet is served: /_brand/1/<rrggbb>.css.
// The 1 is the palette's version, so a change to how palettes are made
// is a new name and nothing cached goes stale.
const brandPrefix = "/_brand/1/"

var brandFile = regexp.MustCompile(`^[0-9a-f]{6}\.css$`)

func brandCSSPath(color string) string {
	return brandPrefix + strings.TrimPrefix(brand.Color(color), "#") + ".css"
}

// serveBrandCSS answers a colour's stylesheet. It is made from the name
// alone, so it is the same for every business and cached for good.
func serveBrandCSS(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimPrefix(r.URL.Path, brandPrefix)
	if !brandFile.MatchString(name) || (r.Method != http.MethodGet && r.Method != http.MethodHead) {
		http.NotFound(w, r)
		return
	}
	h := w.Header()
	h.Set("Content-Type", "text/css; charset=utf-8")
	h.Set("Cache-Control", "public, max-age=31536000, immutable")
	h.Set("Cross-Origin-Resource-Policy", "same-origin")
	if r.Method == http.MethodGet {
		_, _ = w.Write([]byte(brand.PaletteOf("#" + strings.TrimSuffix(name, ".css")).CSS()))
	}
}
