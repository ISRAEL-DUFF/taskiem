package api

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/datatables"
	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/media"
	"github.com/israel-duff/taskiem/engine/stock"
	"github.com/israel-duff/taskiem/engine/storefront"
)

// The storefront (Phase 5, S2; docs/storefronts.md): the owner edits a
// draft, publishes it as a snapshot the public host serves, and maps a
// data table as its catalogue, whose public columns are copied for pages.

const storefrontPurpose = "storefront.catalogue"

type storefrontOut struct {
	storefront.Storefront
	PreviewURL string `json:"preview_url"`
	// URL is the page's public address: its own primary domain once
	// verified, else the preview address (storefront.Storefront.URL).
	URL       string            `json:"url"`
	Templates []string          `json:"templates"`
	Accents   map[string]string `json:"accents"`
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
	out.URL = sf.URL(s.previewDomain())
	return out
}

func storefrontErr(err error) error {
	switch {
	case errors.Is(err, storefront.ErrInvalid):
		return fmt.Errorf("%w: %s", errBadRequest, plainError(err))
	case errors.Is(err, storefront.ErrSlugTaken), errors.Is(err, storefront.ErrNotPublished), errors.Is(err, storefront.ErrSuspended),
		errors.Is(err, storefront.ErrNotInReview), errors.Is(err, errNeedsVerification):
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
			if t.Kept() {
				// A kept table is Taskiem's record (the money log): never a
				// public page's, and never written by the storefront.
				return fmt.Errorf("%w: the catalogue cannot be a table Taskiem keeps (%s)", errBadRequest, t.Name)
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
		res, err := s.publishGated(r.Context(), tx, tenant, principalFrom(r.Context()).Actor())
		if err != nil {
			return storefrontErr(err)
		}
		sf = res.Storefront
		return auditTx(r, tx, "storefront.publish", sf.Slug, map[string]any{"version": sf.PublishedVersion, "held_for_review": res.Held,
			"checks": res.Checks.Verdict})
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	out := s.storefrontOut(sf)
	if p := principalFrom(r.Context()); p.Agent != nil && p.Agent.Kind == "onboarding" && sf.Status != "in_review" {
		// Live: the onboarding is done (agents.go). Held for review, the
		// agent may still change it.
		s.agentPublished(r.Context(), p, sf.Draft.Name, out.URL)
	}
	writeJSON(w, http.StatusOK, out)
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
		if err := s.syncLists(ctx, tenant); err != nil {
			return err
		}
		return s.syncServices(ctx, tenant, sf)
	}
	// A catalogue keeping stock: counts changed in the table by hand are
	// recorded as outside movements, against the database's clock read
	// before the rows (docs/spec/stock.md §4).
	var mark *time.Time
	if s.Stock.Ready() && sf.CatalogueColumns.Stock != "" {
		if t, err := s.Stock.ReadMark(ctx); err == nil {
			mark = &t
		}
	}
	rows, rerr := s.readAllRows(ctx, tenant, table, storefront.MaxItems+1)
	if rerr == nil && mark != nil {
		if _, err := s.Stock.Outside(ctx, tenant, stock.SeenRows(rows, sf.CatalogueColumns), *mark); err != nil {
			s.Logger.Warn("stock: recording changes made in the table", "tenant", tenant, "err", err)
		}
	}
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
		// A live page's items are checked as they are copied (moderation.go).
		if items, err = s.moderateItems(ctx, tx, tenant, items); err != nil {
			return err
		}
		return storefront.SyncCatalogue(ctx, tx, tenant, items)
	})
	if err != nil {
		return err
	}
	if err := s.syncLists(ctx, tenant); err != nil {
		return err
	}
	return s.syncServices(ctx, tenant, sf)
}

// syncLists refreshes the copies of the other fronts' lists (fronts F3,
// §5.2), one per table, each item checked as the catalogue's are, and
// drops the copies no list shows. A table that cannot be read keeps its
// old copy.
func (s *Server) syncLists(ctx context.Context, tenant uuid.UUID) error {
	var lists []storefront.ListConfig
	if err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		var err error
		lists, err = storefront.Lists(ctx, tx, tenant)
		return err
	}); err != nil {
		return err
	}
	keep := []string{}
	var errs []error
	for _, l := range lists {
		keep = append(keep, l.TableName)
		var table datatables.Table
		if err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
			var err error
			table, err = datatables.GetTable(ctx, tx, *l.Table)
			return err
		}); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", l.TableName, err))
			continue
		}
		rows, err := s.readAllRows(ctx, tenant, table, storefront.MaxItems+1)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", l.TableName, err))
			continue
		}
		if err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
			items, err := storefront.Items(ctx, tx, tenant, rows, l.Columns)
			if err != nil {
				return err
			}
			if items, err = s.moderateItems(ctx, tx, tenant, items); err != nil {
				return err
			}
			return storefront.SyncList(ctx, tx, tenant, l.TableName, items)
		}); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", l.TableName, err))
		}
	}
	if err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		return storefront.PruneLists(ctx, tx, tenant, keep)
	}); err != nil {
		errs = append(errs, err)
	}
	return errors.Join(errs...)
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
	s.chatHooks()
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
		// Reported pages and paused orders (trust.go).
		if terr := s.TrustTick(ctx); terr != nil && ctx.Err() == nil {
			s.Logger.Error("storefront trust checks", "err", terr)
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
		s.failImage(w, r, err)
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

// failImage answers an image that was not stored: a problem with the
// business's data project, where photos live, is said as tables say it
// (being set up, or what Xenibase refused), not as an internal error.
func (s *Server) failImage(w http.ResponseWriter, r *http.Request, err error) {
	var xe *datatables.Error
	if errors.Is(err, datatables.ErrUnavailable) || errors.Is(err, datatables.ErrProvisioning) || errors.Is(err, datatables.ErrProjectFailed) || errors.As(err, &xe) {
		s.failTables(w, r, err)
		return
	}
	s.fail(w, r, err)
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
	// Photos live in the business's data project: the first one makes it,
	// as the first table does.
	if s.Tables != nil {
		if _, err := s.Tables.EnsureProject(ctx, p.TenantID); err != nil && !errors.Is(err, datatables.ErrUnavailable) {
			return storefront.Media{}, err
		}
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

// photoIDs are the media ids a photo column holds: one, or several
// separated by commas (the first is the photo, the rest its gallery).
func photoIDs(v any) []string {
	var out []string
	for _, p := range strings.Split(cell(v), ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// photoCell reads a row's photo column as the table has it now: the value
// itself (to change it only while it still is) and the ids in it.
func (s *Server) photoCell(ctx context.Context, tr tableRows, t datatables.Table, key int64, col string) (any, []string, error) {
	page, err := tr.call(ctx, s, "read_rows", map[string]any{"table": t.Name, "limit": 1, "select": "id," + col,
		"filters": []any{map[string]any{"column": "id", "op": "eq", "value": key}}}, "")
	if err != nil {
		return nil, nil, err
	}
	rows := rowsOf(page)
	if len(rows) == 0 {
		return nil, nil, pgx.ErrNoRows
	}
	m, _ := rows[0].(map[string]any)
	return m[col], photoIDs(m[col]), nil
}

// photoAttempts bounds how often a row's photos are read again when
// another change to them lands first.
const photoAttempts = 4

// errPhotoMissing answers taking out a photo the item does not have.
var errPhotoMissing = errors.New("that photo is not on this item")

// errPhotosFull refuses a photo past an item's last.
var errPhotosFull = fmt.Errorf("%w: an item has up to %d photos; remove one first", errConflict, storefront.MaxGallery+1)

// changePhotos changes a row's photos from what its column holds now,
// writing only while it still holds that, so two changes at once (two
// phones adding photos) both land and neither undoes the other. change
// answers the new list, or an error to refuse with.
func (s *Server) changePhotos(ctx context.Context, tenant uuid.UUID, t datatables.Table, key int64, col, idem string, change func([]string) ([]string, error)) error {
	tr, err := s.tableRowsFor(ctx, tenant, t, storefrontPurpose)
	if err != nil {
		return err
	}
	for attempt := range photoAttempts {
		was, have, err := s.photoCell(ctx, tr, t, key, col)
		if err != nil {
			return err
		}
		next, err := change(have)
		if err != nil {
			return err
		}
		var value any
		if len(next) > 0 {
			value = strings.Join(next, ",")
		}
		still := map[string]any{"column": col, "op": "eq", "value": was}
		if was == nil {
			still = map[string]any{"column": col, "op": "is_null"}
		}
		out, err := tr.call(ctx, s, "update_rows", map[string]any{"table": t.Name, "set": map[string]any{col: value}, "max_affected": 1, "return": "minimal",
			"filters": []any{map[string]any{"column": "id", "op": "eq", "value": key}, still}}, idem+":"+strconv.Itoa(attempt))
		if err != nil {
			return err
		}
		if affectedOf(out) > 0 {
			return nil
		}
	}
	return fmt.Errorf("%w: the item's photos kept changing; try again", errConflict)
}

// photoAdded is how a new photo joins a row's: with ?add=1 at the end of
// its gallery, at most MaxGallery+1 in all; otherwise in place of the
// first, the main photo, keeping the rest. A full gallery first lets go
// of what the page cannot show (a photo refused by review, or text that
// is not a photo), which the owner's screens do not list to take out.
func (s *Server) photoAdded(r *http.Request, tenant uuid.UUID, id string) func([]string) ([]string, error) {
	add := r.URL.Query().Get("add") == "1"
	return func(have []string) ([]string, error) {
		if !add {
			if len(have) == 0 {
				return []string{id}, nil
			}
			return append([]string{id}, have[1:]...), nil
		}
		if slices.Contains(have, id) {
			return have, nil
		}
		if len(have) > storefront.MaxGallery {
			if err := s.tx(r, func(tx pgx.Tx) error {
				var err error
				have, err = storefront.Shown(r.Context(), tx, tenant, have)
				return err
			}); err != nil {
				return nil, err
			}
		}
		if len(have) > storefront.MaxGallery {
			return nil, errPhotosFull
		}
		return append(slices.Clone(have), id), nil
	}
}

// photoFull refuses an added photo before its image is stored, when the
// row has its last already (changePhotos checks again as it writes).
func (s *Server) photoFull(w http.ResponseWriter, r *http.Request, tenant uuid.UUID, t datatables.Table, key int64, col string) bool {
	if r.URL.Query().Get("add") != "1" {
		return false
	}
	tr, err := s.tableRowsFor(r.Context(), tenant, t, storefrontPurpose)
	if err == nil {
		var have []string
		if _, have, err = s.photoCell(r.Context(), tr, t, key, col); err == nil && len(have) > storefront.MaxGallery {
			_, err = s.photoAdded(r, tenant, "")(have)
		}
	}
	switch {
	case err == nil:
		return false
	case errors.Is(err, pgx.ErrNoRows), errors.Is(err, errConflict):
		s.fail(w, r, err)
	default:
		s.failTables(w, r, err)
	}
	return true
}

// failPhotos answers a refused change to a row's photos.
func (s *Server) failPhotos(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, errPhotoMissing) {
		writeErr(w, http.StatusNotFound, err.Error())
		return
	}
	if errors.Is(err, pgx.ErrNoRows) || errors.Is(err, errConflict) {
		s.fail(w, r, err)
		return
	}
	s.failTables(w, r, err)
}

// dropPhoto takes the photo ?media= out of a row's photo column (the next
// becomes the photo when it was the first), and says whether it did. Any
// entry the column holds can be taken out, a media id or not, so one the
// page cannot show does not hold a place for good.
func (s *Server) dropPhoto(w http.ResponseWriter, r *http.Request, tenant uuid.UUID, t datatables.Table, key int64, col string) bool {
	media := strings.TrimSpace(r.URL.Query().Get("media"))
	if media == "" || strings.Contains(media, ",") || len(media) > 300 {
		s.fail(w, r, fmt.Errorf("%w: media is one of the item's photos", errBadRequest))
		return false
	}
	err := s.changePhotos(r.Context(), tenant, t, key, col, "photo-drop:"+media+":"+strconv.FormatInt(key, 10)+":"+uuid.NewString(), func(have []string) ([]string, error) {
		left := slices.DeleteFunc(slices.Clone(have), func(x string) bool { return x == media })
		if len(left) == len(have) {
			return nil, errPhotoMissing
		}
		return left, nil
	})
	if err != nil {
		s.failPhotos(w, r, err)
		return false
	}
	return true
}

// firstPhoto makes the photo ?media= a row's main photo: first in its
// photo column, the others after it in their order.
func (s *Server) firstPhoto(w http.ResponseWriter, r *http.Request, tenant uuid.UUID, t datatables.Table, key int64, col string) bool {
	media := strings.TrimSpace(r.URL.Query().Get("media"))
	if media == "" || strings.Contains(media, ",") || len(media) > 300 {
		s.fail(w, r, fmt.Errorf("%w: media is one of the item's photos", errBadRequest))
		return false
	}
	err := s.changePhotos(r.Context(), tenant, t, key, col, "photo-main:"+media+":"+strconv.FormatInt(key, 10)+":"+uuid.NewString(), func(have []string) ([]string, error) {
		rest := slices.DeleteFunc(slices.Clone(have), func(x string) bool { return x == media })
		if len(rest) == len(have) {
			return nil, errPhotoMissing
		}
		return append([]string{media}, rest...), nil
	})
	if err != nil {
		s.failPhotos(w, r, err)
		return false
	}
	return true
}

// catalogueItemChanged audits a change to a catalogue item's photos and
// refreshes the copies the page reads (a failure to refresh is kept for
// the next refresh, not a failure of the change).
func (s *Server) catalogueItemChanged(r *http.Request, action string, t datatables.Table, row int64) {
	p := principalFrom(r.Context())
	if err := s.tx(r, func(tx pgx.Tx) error {
		return auditTx(r, tx, action, t.Name, map[string]any{"table_id": t.ID, "row": strconv.FormatInt(row, 10)})
	}); err != nil {
		s.Logger.Warn("storefront: audit", "tenant", p.TenantID, "err", err)
	}
	if err := s.SyncStorefront(r.Context(), p.TenantID); err != nil && !errors.Is(err, r.Context().Err()) && !errors.Is(err, storefront.ErrNotFound) {
		s.Logger.Warn("storefront: refreshing the copies", "tenant", p.TenantID, "err", err)
	}
}

// cataloguePhotoRow reads the request's catalogue row and the table its
// photos are in, writing the refusal itself.
func (s *Server) cataloguePhotoRow(w http.ResponseWriter, r *http.Request) (datatables.Table, string, int64, bool) {
	row, err := strconv.ParseInt(chi.URLParam(r, "row"), 10, 64)
	if err != nil || row < 1 {
		s.fail(w, r, pgx.ErrNoRows)
		return datatables.Table{}, "", 0, false
	}
	var sf storefront.Storefront
	var table datatables.Table
	if err := s.tx(r, func(tx pgx.Tx) error {
		var err error
		if sf, err = storefront.Get(r.Context(), tx, principalFrom(r.Context()).TenantID); err != nil {
			return storefrontErr(err)
		}
		if sf.CatalogueTable == nil || sf.CatalogueColumns.Photo == "" {
			return fmt.Errorf("%w: the catalogue has no photo column", errBadRequest)
		}
		table, err = datatables.GetTable(r.Context(), tx, *sf.CatalogueTable)
		return err
	}); err != nil {
		s.fail(w, r, err)
		return datatables.Table{}, "", 0, false
	}
	return table, sf.CatalogueColumns.Photo, row, true
}

// removeStorefrontItemPhoto answers DELETE /v1/storefront/catalogue/rows/{row}/photo?media=<id>.
func (s *Server) removeStorefrontItemPhoto(w http.ResponseWriter, r *http.Request) {
	table, col, row, ok := s.cataloguePhotoRow(w, r)
	if ok && s.dropPhoto(w, r, principalFrom(r.Context()).TenantID, table, row, col) {
		s.catalogueItemChanged(r, "storefront.item_photo_remove", table, row)
		w.WriteHeader(http.StatusNoContent)
	}
}

// mainStorefrontItemPhoto answers PUT /v1/storefront/catalogue/rows/{row}/photo/main?media=<id>.
func (s *Server) mainStorefrontItemPhoto(w http.ResponseWriter, r *http.Request) {
	table, col, row, ok := s.cataloguePhotoRow(w, r)
	if ok && s.firstPhoto(w, r, principalFrom(r.Context()).TenantID, table, row, col) {
		s.catalogueItemChanged(r, "storefront.item_photo_main", table, row)
		w.WriteHeader(http.StatusNoContent)
	}
}

// setStorefrontItemPhoto stores an image and writes its id into the
// catalogue row's photo column: the way an owner adds a product photo.
// With ?add=1 the image joins the row's other photos (its gallery).
func (s *Server) setStorefrontItemPhoto(w http.ResponseWriter, r *http.Request) {
	table, col, row, ok := s.cataloguePhotoRow(w, r)
	if !ok {
		return
	}
	tenant := principalFrom(r.Context()).TenantID
	if s.photoFull(w, r, tenant, table, row, col) {
		return
	}
	m, err := s.storeStorefrontImage(r)
	if err != nil {
		s.failImage(w, r, err)
		return
	}
	if err := s.changePhotos(r.Context(), tenant, table, row, col, "storefront-photo:"+m.ID.String()+":"+strconv.FormatInt(row, 10), s.photoAdded(r, tenant, m.ID.String())); err != nil {
		s.failPhotos(w, r, err)
		return
	}
	s.catalogueItemChanged(r, "storefront.item_photo", table, row)
	writeJSON(w, http.StatusOK, m)
}

// storefrontQR is the page's address (its own domain, else the preview
// address) as a QR code, SVG by default or PNG.
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
	u := s.storefrontOut(sf).URL
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
