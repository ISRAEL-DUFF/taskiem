package api

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/datatables"
	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/media"
	"github.com/israel-duff/taskiem/engine/storefront"
)

// The storefront (Phase 5, S2; docs/storefronts.md): the owner edits a
// draft, publishes it as a snapshot the public host serves, and maps a
// data table as its catalogue, whose public columns are copied for pages.

const storefrontPurpose = "storefront.catalogue"

type storefrontOut struct {
	storefront.Storefront
	PreviewURL string            `json:"preview_url"`
	Templates  []string          `json:"templates"`
	Accents    map[string]string `json:"accents"`
}

func (s *Server) previewDomain() string {
	if len(s.PublicHosts) > 0 {
		return s.PublicHosts[0]
	}
	return ""
}

func (s *Server) storefrontOut(sf storefront.Storefront) storefrontOut {
	out := storefrontOut{Storefront: sf, Templates: storefront.Templates, Accents: storefront.Accents}
	// Where the published version serves: a slug changed in the draft
	// serves only once published.
	out.PreviewURL = storefront.PreviewURL(sf.PublishedSlug, s.previewDomain())
	return out
}

func storefrontErr(err error) error {
	switch {
	case errors.Is(err, storefront.ErrInvalid):
		return fmt.Errorf("%w: %s", errBadRequest, plainError(err))
	case errors.Is(err, storefront.ErrSlugTaken), errors.Is(err, storefront.ErrNotPublished):
		return fmt.Errorf("%w: %s", errConflict, plainError(err))
	case errors.Is(err, storefront.ErrNotFound):
		return pgx.ErrNoRows
	}
	return err
}

func (s *Server) getStorefront(w http.ResponseWriter, r *http.Request) {
	var sf storefront.Storefront
	if err := s.tx(r, func(tx pgx.Tx) error {
		var err error
		sf, err = storefront.Get(r.Context(), tx, principalFrom(r.Context()).TenantID)
		return storefrontErr(err)
	}); err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, s.storefrontOut(sf))
}

// putStorefront saves the draft. The card is checked on publishing; the
// slug and the catalogue mapping are checked now.
func (s *Server) putStorefront(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Slug             string             `json:"slug"`
		Draft            storefront.Card    `json:"draft"`
		CatalogueTable   *uuid.UUID         `json:"catalogue_table"`
		CatalogueColumns storefront.Columns `json:"catalogue_columns"`
	}
	if err := decodeBody(r, &req); err != nil {
		s.fail(w, r, err)
		return
	}
	p := principalFrom(r.Context())
	var sf storefront.Storefront
	err := s.tx(r, func(tx pgx.Tx) error {
		if req.CatalogueTable != nil {
			t, err := datatables.GetTable(r.Context(), tx, *req.CatalogueTable)
			if err != nil {
				return fmt.Errorf("%w: the catalogue table: %w", errBadRequest, err)
			}
			known := func(name string) (string, bool) {
				for _, c := range t.Columns {
					if c.Name == name {
						if c.Personal {
							return "personal", true // never shown on a public page
						}
						return c.Type, true
					}
				}
				return "", false
			}
			if err := req.CatalogueColumns.Check(known); err != nil {
				return storefrontErr(err)
			}
		} else {
			req.CatalogueColumns = storefront.Columns{}
		}
		var err error
		if sf, err = storefront.Save(r.Context(), tx, p.TenantID, req.Slug, req.Draft, req.CatalogueTable, req.CatalogueColumns, p.Actor()); err != nil {
			return storefrontErr(err)
		}
		return auditTx(r, tx, "storefront.save", sf.Slug, nil)
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, s.storefrontOut(sf))
}

// publishStorefront refreshes the catalogue copy, then publishes the
// draft as the next version.
func (s *Server) publishStorefront(w http.ResponseWriter, r *http.Request) {
	tenant := principalFrom(r.Context()).TenantID
	if err := s.SyncStorefront(r.Context(), tenant); err != nil && !errors.Is(err, storefront.ErrNotFound) {
		s.fail(w, r, storefrontErr(fmt.Errorf("the catalogue could not be read: %w", err)))
		return
	}
	var sf storefront.Storefront
	err := s.tx(r, func(tx pgx.Tx) error {
		var err error
		if sf, err = storefront.Publish(r.Context(), tx, tenant, s.previewDomain(), principalFrom(r.Context()).Actor()); err != nil {
			return storefrontErr(err)
		}
		return auditTx(r, tx, "storefront.publish", sf.Slug, map[string]any{"version": sf.PublishedVersion})
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, s.storefrontOut(sf))
}

func (s *Server) closeStorefront(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Message string `json:"message"`
	}
	if err := decodeBody(r, &req); err != nil {
		s.fail(w, r, err)
		return
	}
	s.storefrontState(w, r, "storefront.close", func(tx pgx.Tx, tenant uuid.UUID) (storefront.Storefront, error) {
		return storefront.Close(r.Context(), tx, tenant, req.Message)
	})
}

func (s *Server) reopenStorefront(w http.ResponseWriter, r *http.Request) {
	s.storefrontState(w, r, "storefront.reopen", func(tx pgx.Tx, tenant uuid.UUID) (storefront.Storefront, error) {
		return storefront.Reopen(r.Context(), tx, tenant)
	})
}

func (s *Server) storefrontState(w http.ResponseWriter, r *http.Request, action string, fn func(pgx.Tx, uuid.UUID) (storefront.Storefront, error)) {
	var sf storefront.Storefront
	err := s.tx(r, func(tx pgx.Tx) error {
		var err error
		if sf, err = fn(tx, principalFrom(r.Context()).TenantID); err != nil {
			return storefrontErr(err)
		}
		return auditTx(r, tx, action, sf.Slug, nil)
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, s.storefrontOut(sf))
}

func (s *Server) getStorefrontCatalogue(w http.ResponseWriter, r *http.Request) {
	var sf storefront.Storefront
	var items []storefront.Item
	err := s.tx(r, func(tx pgx.Tx) error {
		var err error
		tenant := principalFrom(r.Context()).TenantID
		if sf, err = storefront.Get(r.Context(), tx, tenant); err != nil {
			return storefrontErr(err)
		}
		items, err = storefront.Catalogue(r.Context(), tx, tenant)
		return err
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if items == nil {
		items = []storefront.Item{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "synced_at": sf.CatalogueSynced, "error": sf.CatalogueError})
}

func (s *Server) refreshStorefrontCatalogue(w http.ResponseWriter, r *http.Request) {
	if err := s.SyncStorefront(r.Context(), principalFrom(r.Context()).TenantID); err != nil {
		s.fail(w, r, storefrontErr(fmt.Errorf("%w: the catalogue could not be read: %w", errConflict, err)))
		return
	}
	s.getStorefrontCatalogue(w, r)
}

// SyncStorefront refreshes the tenant's catalogue and services copies from
// their data tables. A failure to read a table keeps the old copy and
// records why.
func (s *Server) SyncStorefront(ctx context.Context, tenant uuid.UUID) error {
	var sf storefront.Storefront
	var table datatables.Table
	err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		var err error
		if sf, err = storefront.Get(ctx, tx, tenant); err != nil {
			return err
		}
		if sf.CatalogueTable == nil {
			return nil
		}
		table, err = datatables.GetTable(ctx, tx, *sf.CatalogueTable)
		return err
	})
	if err != nil {
		return err
	}
	if sf.CatalogueTable == nil {
		if err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
			return storefront.SyncCatalogue(ctx, tx, tenant, nil)
		}); err != nil {
			return err
		}
		return s.syncServices(ctx, tenant, sf)
	}
	rows, rerr := s.readAllRows(ctx, tenant, table, storefront.MaxItems+1)
	err = db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		if rerr != nil {
			if err := storefront.CatalogueFailed(ctx, tx, tenant, rerr); err != nil {
				return err
			}
			return rerr
		}
		items, err := storefront.Items(ctx, tx, tenant, rows, sf.CatalogueColumns)
		if err != nil {
			_ = storefront.CatalogueFailed(ctx, tx, tenant, err)
			return err
		}
		return storefront.SyncCatalogue(ctx, tx, tenant, items)
	})
	if err != nil {
		return err
	}
	return s.syncServices(ctx, tenant, sf)
}

// readAllRows reads a table's rows in pages, up to limit.
func (s *Server) readAllRows(ctx context.Context, tenant uuid.UUID, t datatables.Table, limit int) ([]map[string]any, error) {
	tr, err := s.tableRowsFor(ctx, tenant, t, storefrontPurpose)
	if err != nil {
		return nil, err
	}
	var out []map[string]any
	cursor := ""
	for len(out) < limit {
		in := map[string]any{"table": t.Name, "limit": min(200, limit-len(out)), "order": []any{map[string]any{"column": "id"}}}
		if cursor != "" {
			in["cursor"] = cursor
		}
		page, err := tr.call(ctx, s, "read_rows", in, "")
		if err != nil {
			return nil, err
		}
		for _, row := range rowsOf(page) {
			if m, ok := row.(map[string]any); ok {
				out = append(out, m)
			}
		}
		next, _ := page["next_cursor"].(string)
		if next == "" {
			break
		}
		cursor = next
	}
	return out, nil
}

// RunStorefronts refreshes published storefronts' catalogue copies every
// 15 minutes, catching changes made to the tables outside Taskiem.
func (s *Server) RunStorefronts(ctx context.Context) error {
	for {
		rows, err := s.Store.Pool.Query(ctx, `SELECT tenant_id FROM taskiem_storefronts_due($1)`, time.Now().Add(-15*time.Minute))
		if err == nil {
			tenants, cerr := pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
			err = cerr
			for _, t := range tenants {
				if serr := s.SyncStorefront(ctx, t); serr != nil && ctx.Err() == nil {
					s.Logger.Warn("storefront catalogue refresh", "tenant", t, "err", serr)
				}
			}
		}
		if err != nil && ctx.Err() == nil {
			s.Logger.Error("storefront catalogue refreshes", "err", err)
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(5 * time.Minute):
		}
	}
}

// plainError is an error's message for people: without the packages'
// prefixes ("storefront: invalid: publicnames: reserved name: \"www\""
// becomes "reserved name: \"www\"").
func plainError(err error) string {
	msg := err.Error()
	for _, p := range []string{"storefront: invalid: ", "storefront: ", "publicnames: ", "embedsites: invalid site: ", "embedsites: ", "media: ", "sender: invalid message: "} {
		msg = strings.ReplaceAll(msg, p, "")
	}
	return msg
}

// storefrontImage serves one of the organisation's storefront photos to
// the web app, whose CSP allows images from its own origin only
// (media.FetchPublic checks the URL is the organisation's).
func (s *Server) storefrontImage(w http.ResponseWriter, r *http.Request) {
	if s.Media == nil {
		s.fail(w, r, pgx.ErrNoRows)
		return
	}
	data, ctype, err := s.Media.FetchPublic(r.Context(), principalFrom(r.Context()).TenantID, r.URL.Query().Get("src"))
	if errors.Is(err, media.ErrNotOurs) {
		s.fail(w, r, fmt.Errorf("%w: %s", errBadRequest, plainError(err)))
		return
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	w.Header().Set("Content-Type", ctype)
	w.Header().Set("Cache-Control", "private, max-age=86400")
	_, _ = w.Write(data) //nolint:gosec // a JPEG or PNG by its bytes (FetchPublic), sent as that type with nosniff
}

// uploadStorefrontMedia stores an image (the request's body) for the
// storefront: a logo or a product photo.
func (s *Server) uploadStorefrontMedia(w http.ResponseWriter, r *http.Request) {
	m, err := s.storeStorefrontImage(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, m)
}

func (s *Server) storeStorefrontImage(r *http.Request) (storefront.Media, error) {
	if s.Media == nil {
		return storefront.Media{}, fmt.Errorf("%w: storage for photos is not configured", errConflict)
	}
	data, err := io.ReadAll(http.MaxBytesReader(nil, r.Body, media.MaxImageBytes+1))
	if err != nil || len(data) > media.MaxImageBytes {
		return storefront.Media{}, fmt.Errorf("%w: an image is at most %d MB", errBadRequest, media.MaxImageBytes>>20)
	}
	m, err := s.putStorefrontImage(r.Context(), principalFrom(r.Context()), data, func(tx pgx.Tx, m storefront.Media) error {
		return auditTx(r, tx, "storefront.media_add", m.ID.String(), map[string]any{"moderation": m.Moderation})
	})
	switch {
	case errors.Is(err, media.ErrImage):
		return m, fmt.Errorf("%w: %s", errBadRequest, plainError(err))
	case errors.Is(err, errImageRejected):
		return m, fmt.Errorf("%w: this image was not accepted for a public page", errBadRequest)
	}
	return m, err
}

// errImageRejected: the moderator refused an image for a public page.
var errImageRejected = errors.New("the image was not accepted for a public page")

// putStorefrontImage stores an image for the storefront, from the web app
// or a chat (a logo or a product photo): resized and stripped
// (media.PutImage, media.ErrImage when it is not a usable image), checked
// by the moderator when there is one, and recorded, audited by audit. A
// rejected image is recorded, and errImageRejected returned with it.
func (s *Server) putStorefrontImage(ctx context.Context, p *Principal, data []byte, audit func(pgx.Tx, storefront.Media) error) (storefront.Media, error) {
	if s.Media == nil {
		return storefront.Media{}, errors.New("storage for photos is not configured")
	}
	img, err := s.Media.PutImage(ctx, p.TenantID, data)
	if err != nil {
		return storefront.Media{}, err
	}
	verdict, reason := "", ""
	if s.Moderator != nil {
		if verdict, reason, err = s.Moderator.Check(ctx, p.TenantID, img); err != nil {
			return storefront.Media{}, err
		}
	}
	var m storefront.Media
	err = db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{p.TenantID}, func(tx pgx.Tx) error {
		var err error
		if m, err = storefront.AddMedia(ctx, tx, p.TenantID, img, verdict, reason, p.Actor()); err != nil {
			return err
		}
		return audit(tx, m)
	})
	if err == nil && m.Moderation == "rejected" {
		err = errImageRejected
	}
	return m, err
}

// setStorefrontItemPhoto stores an image and writes its id into the
// catalogue row's photo column: the way an owner adds a product photo.
func (s *Server) setStorefrontItemPhoto(w http.ResponseWriter, r *http.Request) {
	row, err := strconv.ParseInt(chi.URLParam(r, "row"), 10, 64)
	if err != nil || row < 1 {
		s.fail(w, r, pgx.ErrNoRows)
		return
	}
	tenant := principalFrom(r.Context()).TenantID
	var sf storefront.Storefront
	var table datatables.Table
	if err := s.tx(r, func(tx pgx.Tx) error {
		var err error
		if sf, err = storefront.Get(r.Context(), tx, tenant); err != nil {
			return storefrontErr(err)
		}
		if sf.CatalogueTable == nil || sf.CatalogueColumns.Photo == "" {
			return fmt.Errorf("%w: the catalogue has no photo column", errBadRequest)
		}
		table, err = datatables.GetTable(r.Context(), tx, *sf.CatalogueTable)
		return err
	}); err != nil {
		s.fail(w, r, err)
		return
	}
	m, err := s.storeStorefrontImage(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	tr, err := s.tableRowsFor(r.Context(), tenant, table, storefrontPurpose)
	if err != nil {
		s.failTables(w, r, err)
		return
	}
	out, err := tr.call(r.Context(), s, "update_rows", map[string]any{"table": table.Name, "key": row,
		"set": map[string]any{sf.CatalogueColumns.Photo: m.ID.String()}, "max_affected": 1}, "storefront-photo:"+m.ID.String()+":"+strconv.FormatInt(row, 10))
	if err != nil {
		s.failTables(w, r, err)
		return
	}
	if n, _ := out["affected"].(int); n == 0 && len(rowsOf(out)) == 0 {
		writeErr(w, http.StatusNotFound, "no row "+strconv.FormatInt(row, 10))
		return
	}
	writeJSON(w, http.StatusOK, m)
}

// storefrontQR is the preview address as a QR code, SVG by default or PNG.
func (s *Server) storefrontQR(w http.ResponseWriter, r *http.Request) {
	var sf storefront.Storefront
	if err := s.tx(r, func(tx pgx.Tx) error {
		var err error
		sf, err = storefront.Get(r.Context(), tx, principalFrom(r.Context()).TenantID)
		return storefrontErr(err)
	}); err != nil {
		s.fail(w, r, err)
		return
	}
	u := s.storefrontOut(sf).PreviewURL
	if u == "" {
		s.fail(w, r, fmt.Errorf("%w: the public host has no preview domain (TASKIEM_PUBLIC_DOMAINS)", errConflict))
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	if r.URL.Query().Get("format") == "png" {
		png, err := storefront.QRPNG(u)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(png)
		return
	}
	svg, err := storefront.QRSVG(u)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "image/svg+xml")
	_, _ = io.WriteString(w, svg)
}
