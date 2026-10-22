package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/brand"
	"github.com/israel-duff/taskiem/engine/publicweb"
	"github.com/israel-duff/taskiem/engine/storefront"
)

// How a business's public pages look (N13, decision 0042): a theme, a
// colour, a logo and a cover photo, used by every public page of the
// business: the storefront, its forms and status pages, hosted forms.
// Images are uploaded as storefront media (POST /v1/storefront/media) and
// named here by id; their URLs are copied in when set, as a published
// storefront copies its logo's.

type brandOut struct {
	brand.Brand
	// Palette is the colours the pages are drawn with, made from Color.
	Palette brand.Palette `json:"palette"`
	Themes  []string      `json:"themes"`
}

func viewBrand(b brand.Brand) brandOut {
	if err := b.Check(); err != nil {
		b = brand.Brand{Theme: brand.Theme(b.Theme), Color: brand.Color(b.Color), Logo: b.Logo, LogoURLs: b.LogoURLs, Cover: b.Cover, CoverURLs: b.CoverURLs}
	}
	return brandOut{Brand: b, Palette: brand.PaletteOf(b.Color), Themes: brand.Themes}
}

func brandOf(ctx context.Context, tx pgx.Tx, tenant uuid.UUID) (brand.Brand, error) {
	var raw []byte
	if err := tx.QueryRow(ctx, `SELECT brand FROM tenants WHERE id = $1`, tenant).Scan(&raw); err != nil {
		return brand.Brand{}, err
	}
	var b brand.Brand
	if err := json.Unmarshal(raw, &b); err != nil {
		return brand.Brand{}, err
	}
	return b, nil
}

// getBrand answers GET /v1/settings/brand.
func (s *Server) getBrand(w http.ResponseWriter, r *http.Request) {
	var b brand.Brand
	if err := s.tx(r, func(tx pgx.Tx) (err error) {
		b, err = brandOf(r.Context(), tx, principalFrom(r.Context()).TenantID)
		return err
	}); err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, viewBrand(b))
}

// brandRequest is a look as the web app sends it.
type brandRequest struct {
	Theme string `json:"theme"`
	Color string `json:"color"`
	Logo  string `json:"logo"`
	Cover string `json:"cover"`
}

// resolve checks a look and copies its images' URLs in.
func (req brandRequest) resolve(r *http.Request, tx pgx.Tx, tenant uuid.UUID) (brand.Brand, error) {
	b := brand.Brand{Theme: req.Theme, Color: req.Color, Logo: req.Logo, Cover: req.Cover}
	if err := b.Check(); err != nil {
		return b, fmt.Errorf("%w: %s", errBadRequest, plainError(err))
	}
	for _, img := range []struct {
		id   string
		urls *map[string]string
		what string
	}{{b.Logo, &b.LogoURLs, "logo"}, {b.Cover, &b.CoverURLs, "cover photo"}} {
		if img.id == "" {
			continue
		}
		urls, err := storefront.MediaImage(r.Context(), tx, tenant, img.id)
		if errors.Is(err, storefront.ErrInvalid) {
			return b, fmt.Errorf("%w: the %s: %s", errBadRequest, img.what, plainError(err))
		}
		if err != nil {
			return b, err
		}
		*img.urls = urls
	}
	return b, nil
}

// previewBrand answers POST /v1/settings/brand/preview: the addresses of
// the public pages drawn in a look not yet saved, with sample content and
// the business's name, for the brand screen to frame. They work for an
// hour (publicweb.PreviewTTL).
func (s *Server) previewBrand(w http.ResponseWriter, r *http.Request) {
	var req brandRequest
	if err := decodeBody(r, &req); err != nil {
		s.fail(w, r, err)
		return
	}
	if s.Forms == nil || s.Forms.FormHost == "" {
		s.fail(w, r, fmt.Errorf("%w: previews need the public host (TASKIEM_PUBLIC_DOMAINS)", errConflict))
		return
	}
	p := principalFrom(r.Context())
	var b brand.Brand
	var name string
	if err := s.tx(r, func(tx pgx.Tx) (err error) {
		if b, err = req.resolve(r, tx, p.TenantID); err != nil {
			return err
		}
		return tx.QueryRow(r.Context(), `SELECT name FROM tenants WHERE id = $1`, p.TenantID).Scan(&name)
	}); err != nil {
		s.fail(w, r, err)
		return
	}
	pv := publicweb.Preview{Theme: b.Theme, Color: b.Color, Name: name, Expires: time.Now().Add(publicweb.PreviewTTL)}
	if b.Logo != "" {
		pv.Logo, pv.LogoLarge = firstURL(b.LogoURLs, "320", "800"), firstURL(b.LogoURLs, "800", "2000")
	}
	if b.Cover != "" {
		pv.Cover, pv.CoverLarge = firstURL(b.CoverURLs, "800", "2000"), firstURL(b.CoverURLs, "2000", "800")
	}
	pages := map[string]string{}
	for _, page := range publicweb.PreviewPages {
		path, err := publicweb.PreviewPath(s.Forms.Keys, pv, page)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		pages[page] = "https://" + s.Forms.FormHost + path
	}
	writeJSON(w, http.StatusOK, map[string]any{"pages": pages})
}

func firstURL(urls map[string]string, keys ...string) string {
	for _, k := range keys {
		if u := urls[k]; u != "" {
			return u
		}
	}
	return ""
}

// putBrand answers PUT /v1/settings/brand with {"theme", "color", "logo",
// "cover"}: the whole look; an empty logo or cover takes it away. Pages
// show it within the public host's cache (30 seconds).
func (s *Server) putBrand(w http.ResponseWriter, r *http.Request) {
	var req brandRequest
	if err := decodeBody(r, &req); err != nil {
		s.fail(w, r, err)
		return
	}
	p := principalFrom(r.Context())
	var b brand.Brand
	if err := s.tx(r, func(tx pgx.Tx) (err error) {
		if b, err = req.resolve(r, tx, p.TenantID); err != nil {
			return err
		}
		raw, err := json.Marshal(b)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(r.Context(), `UPDATE tenants SET brand = $2 WHERE id = $1`, p.TenantID, raw); err != nil {
			return err
		}
		return auditTx(r, tx, "settings.brand", p.TenantID.String(), map[string]any{"theme": b.Theme, "color": b.Color, "logo": b.Logo, "cover": b.Cover})
	}); err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, viewBrand(b))
}
