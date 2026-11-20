package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/media"
	"github.com/israel-duff/taskiem/engine/peoplechats"
	"github.com/israel-duff/taskiem/engine/saved"
)

// Saved files (saved objects S1; saved-objects §4.1, §4.2): a file object
// (a photo or a document: the CAC certificate, the logo, the menu) and a
// made-up kind's file fields. A file is uploaded first (POST
// /v1/saved/uploads, /v1/me/saved/uploads), checked as a chat's file is
// (scanned, a photo's metadata cleaned, a PDF without scripts), and kept:
// a person's here, a business's in its own storage. Saving the thing
// claims it (its id given as details.file, or as a file field's value);
// one never claimed goes after a day (purgeSavedUploads, and when the
// owner next uploads). An owner uploads at most savedUploadsWaiting not
// yet claimed, and keeps at most savedFilesBytes in all; a person's file
// is sealed under their own key, as their chats' files are. In a
// chat, the clients send it as itself, through the chat's own files (so
// each chat's checks and copies are its own).

// savedFileRoutes are a business's; personSavedFileRoutes a person's own.
func (s *Server) savedFileRoutes(r chi.Router) {
	r.With(s.need(PermSavedManage), s.tenantWide).Post("/saved/uploads", s.savedHandler(false, s.uploadSavedFile))
	r.Get("/saved/{id}/file", s.savedHandler(false, s.savedFile))
}

func (s *Server) personSavedFileRoutes(r chi.Router) {
	r.Post("/me/saved/uploads", s.savedHandler(true, s.uploadSavedFile))
	r.Get("/me/saved/{id}/file", s.savedHandler(true, s.savedFile))
}

// The limits on an owner's saved files: uploads waiting to be claimed,
// all their files' bytes (a person's are kept in the database), and how
// fast they upload.
const (
	savedUploadsWaiting = 20
	savedFilesBytes     = 100 << 20
	savedFilesBytesBiz  = 500 << 20
	savedUploadsEvery   = 6 * time.Second
	savedUploadsBurst   = 30
)

// savedFilesRoom refuses an upload of size more than the owner may keep:
// too many waiting to be claimed, or too many bytes in all.
func savedFilesRoomTx(ctx context.Context, tx pgx.Tx, o savedOwner, size int) error {
	where, owner := fileWhere(o)
	var waiting int
	var bytes int64
	if err := tx.QueryRow(ctx, `SELECT count(*) FILTER (WHERE object_id IS NULL AND created_at >= now() - interval '1 day'), coalesce(sum(size), 0)
		FROM saved_files WHERE `+where, owner).Scan(&waiting, &bytes); err != nil {
		return err
	}
	if waiting >= savedUploadsWaiting {
		return saved.Refuse(fmt.Sprintf("%d files are waiting to be saved: save or let them go first.", savedUploadsWaiting))
	}
	most := int64(savedFilesBytes)
	if !o.mine() {
		most = savedFilesBytesBiz
	}
	if bytes+int64(size) > most {
		return saved.Refuse(fmt.Sprintf("Your saved files take up the %d MB they may: remove some first.", most>>20))
	}
	return nil
}

// savedFileRow is the row a person's saved file is sealed for.
func savedFileRow(id uuid.UUID) string { return "sf:" + id.String() }

// purgeSavedUploads lets go of uploads never claimed, a day on, and their
// copies in each business's storage.
func (s *Server) purgeSavedUploads(ctx context.Context) error {
	rows, err := s.Store.Pool.Query(ctx, `SELECT tenant_id, path FROM taskiem_saved_uploads_purge()`)
	if err != nil {
		return err
	}
	type gone struct {
		tenant uuid.UUID
		path   string
	}
	files, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (gone, error) {
		var g gone
		return g, r.Scan(&g.tenant, &g.path)
	})
	if err != nil {
		return err
	}
	for _, f := range files {
		s.dropSavedPaths(ctx, savedOwner{tenant: f.tenant}, []string{f.path})
	}
	return nil
}

func fileWhere(o savedOwner) (string, uuid.UUID) {
	if o.mine() {
		return `person_id = $1`, o.person
	}
	return `tenant_id = $1`, o.tenant
}

// uploadSavedFile is POST /v1/saved/uploads and /v1/me/saved/uploads (a
// form: file): a photo or document to save, checked; answered with its id
// to save it by, its name, type and size.
func (s *Server) uploadSavedFile(w http.ResponseWriter, r *http.Request, o savedOwner) {
	ctx := r.Context()
	who := "person:" + o.person.String()
	if !o.mine() {
		who = "business:" + o.tenant.String()
	}
	if !s.limiter("saved-upload:"+who, savedUploadsEvery, savedUploadsBurst).Allow() {
		writeErr(w, http.StatusTooManyRequests, "Too many files at once: wait a minute, then try again.")
		return
	}
	f, err := s.readChatFile(w, r)
	if err != nil {
		s.personFail(w, r, err)
		return
	}
	defer func() { _ = r.MultipartForm.RemoveAll() }()
	// Refused before the file is stored anywhere.
	if err := s.savedTx(ctx, o, func(tx pgx.Tx) error { return savedFilesRoomTx(ctx, tx, o, len(f.Data)) }); err != nil {
		s.savedFail(w, r, err)
		return
	}
	id := uuid.Must(uuid.NewV7())
	var path *string
	if !o.mine() {
		bf, err := s.Media.PutChatFile(ctx, o.tenant, f.Data)
		if err != nil {
			s.personFail(w, r, chatFileErr(err))
			return
		}
		path = &bf.Path
	}
	var stale []string
	err = s.savedTx(ctx, o, func(tx pgx.Tx) error {
		// One upload at a time per owner, so the limits hold.
		if err := lockSavedTx(ctx, tx, o); err != nil {
			return err
		}
		var data []byte
		if o.mine() {
			key, err := s.keys().KeyTx(ctx, tx, o.person, true)
			if err != nil {
				return err
			}
			if data, err = peoplechats.Seal(key, o.person, savedFileRow(id), f.Data); err != nil {
				return err
			}
		}
		where, owner := fileWhere(o)
		// Uploads never claimed, a day on: gone.
		rows, err := tx.Query(ctx, `DELETE FROM saved_files WHERE `+where+` AND object_id IS NULL AND created_at < now() - interval '1 day' RETURNING coalesce(path, '')`, owner)
		if err != nil {
			return err
		}
		if stale, err = pgx.CollectRows(rows, pgx.RowTo[string]); err != nil {
			return err
		}
		if err := savedFilesRoomTx(ctx, tx, o, len(f.Data)); err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `INSERT INTO saved_files (id, person_id, tenant_id, type, size, name, data, path) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
			id, nullable(o.person), nullable(o.tenant), f.Type, len(f.Data), f.Name, data, path)
		return err
	})
	if err != nil {
		if path != nil {
			s.dropChatFile(ctx, o.tenant, *path)
		}
		s.savedFail(w, r, err)
		return
	}
	s.dropSavedPaths(ctx, o, stale)
	writeJSON(w, http.StatusCreated, map[string]any{"upload": saved.FileMeta{ID: id.String(), Name: f.Name, Type: f.Type, Size: int64(len(f.Data))}})
}

// dropSavedPaths removes a business's stored copies of files no longer
// kept.
func (s *Server) dropSavedPaths(ctx context.Context, o savedOwner, paths []string) {
	if o.mine() || s.Media == nil {
		return
	}
	for _, p := range paths {
		if p != "" {
			s.dropChatFile(ctx, o.tenant, p)
		}
	}
}

var errFileGone = saved.Refuse("That file is gone. Choose it again.")

// fileToClaimTx is an upload of the owner's that object may take for field:
// not taken by another saved thing (or already this one's, for field).
func fileToClaimTx(ctx context.Context, tx pgx.Tx, o savedOwner, upload string, object uuid.UUID, field string) (saved.FileMeta, error) {
	id, err := uuid.Parse(upload)
	if err != nil {
		return saved.FileMeta{}, errFileGone
	}
	where, owner := fileWhere(o)
	var f saved.FileMeta
	err = tx.QueryRow(ctx, `SELECT id::text, name, type, size FROM saved_files WHERE `+where+` AND id = $2
		AND (object_id IS NULL OR (object_id = $3 AND field = $4)) FOR UPDATE`, owner, id, object, field).Scan(&f.ID, &f.Name, &f.Type, &f.Size)
	if errors.Is(err, pgx.ErrNoRows) {
		return f, errFileGone
	}
	return f, err
}

// claimFilesTx gives object its files, one a field ("" a file object's
// own): each named upload claimed, any it had before for that field let
// go. It answers the stored copies let go, to remove once committed.
func claimFilesTx(ctx context.Context, tx pgx.Tx, o savedOwner, object uuid.UUID, uploads map[string]string) ([]string, error) {
	where, owner := fileWhere(o)
	var gone []string
	for field, upload := range uploads {
		rows, err := tx.Query(ctx, `DELETE FROM saved_files WHERE `+where+` AND object_id = $2 AND field = $3 AND id::text <> $4 RETURNING coalesce(path, '')`,
			owner, object, field, upload)
		if err != nil {
			return nil, err
		}
		paths, err := pgx.CollectRows(rows, pgx.RowTo[string])
		if err != nil {
			return nil, err
		}
		gone = append(gone, paths...)
		if upload == "" {
			continue
		}
		if _, err := tx.Exec(ctx, `UPDATE saved_files SET object_id = $2, field = $3 WHERE `+where+` AND id::text = $4`, owner, object, field, upload); err != nil {
			return nil, err
		}
	}
	return gone, nil
}

// dropObjectFilesTx lets go of every file of object (it was removed).
func dropObjectFilesTx(ctx context.Context, tx pgx.Tx, o savedOwner, object uuid.UUID) ([]string, error) {
	where, owner := fileWhere(o)
	rows, err := tx.Query(ctx, `DELETE FROM saved_files WHERE `+where+` AND object_id = $2 RETURNING coalesce(path, '')`, owner, object)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowTo[string])
}

// savedFile is GET /v1/saved/{id}/file and /v1/me/saved/{id}/file
// (?field= for a made-up kind's file field): the file itself, for whoever
// reaches the saved thing.
func (s *Server) savedFile(w http.ResponseWriter, r *http.Request, o savedOwner) {
	ctx := r.Context()
	id, ok := savedID(r)
	if !ok {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	field := r.URL.Query().Get("field")
	var data []byte
	var path, ctype, name string
	err := s.savedTx(ctx, o, func(tx pgx.Tx) error {
		if _, err := savedRowTx(ctx, tx, o, id, false); err != nil {
			return err
		}
		where, owner := fileWhere(o)
		var fid uuid.UUID
		if err := tx.QueryRow(ctx, `SELECT id, data, coalesce(path, ''), type, name FROM saved_files WHERE `+where+` AND object_id = $2 AND field = $3`, owner, id, field).
			Scan(&fid, &data, &path, &ctype, &name); err != nil {
			return err
		}
		if !o.mine() {
			return nil
		}
		key, err := s.keys().KeyTx(ctx, tx, o.person, false)
		if err != nil {
			return err
		}
		data, err = peoplechats.Open(key, o.person, savedFileRow(fid), data)
		return err
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeErr(w, http.StatusNotFound, "not found")
			return
		}
		s.savedFail(w, r, err)
		return
	}
	if path != "" {
		if s.Media == nil {
			writeErr(w, http.StatusNotFound, "not found")
			return
		}
		if data, _, err = s.Media.GetFile(ctx, o.tenant, path); err != nil {
			s.fail(w, r, err)
			return
		}
	}
	w.Header().Set("Cache-Control", "private, no-store")
	serveChatFile(w, data, ctype, name, media.ChatKind(ctype))
}

// fileDetails is a file object's details from its upload: what the engine
// keeps of it (saved.Clean).
func fileDetails(f saved.FileMeta, note any) map[string]any {
	d := map[string]any{"file": f.ID, "file_name": f.Name, "file_type": f.Type, "file_size": f.Size}
	if n, ok := note.(string); ok && strings.TrimSpace(n) != "" {
		d["note"] = n
	}
	return d
}

// addSavedFileObject adds a file object: its upload (details.file) claimed,
// what it is kept as its details.
func (s *Server) addSavedFileObject(ctx context.Context, who savedWho, o savedOwner, id uuid.UUID, name, title string, in savedIn) (savedRow, error) {
	var x savedRow
	upload, _ := in.Details["file"].(string)
	err := s.savedTx(ctx, o, func(tx pgx.Tx) error {
		meta, err := fileToClaimTx(ctx, tx, o, upload, id, "")
		if err != nil {
			return err
		}
		c, err := saved.Clean(saved.KindFile, fileDetails(meta, in.Details["note"]))
		if err != nil {
			return err
		}
		if err := lockSavedTx(ctx, tx, o); err != nil {
			return err
		}
		if err := s.savedRoomTx(ctx, tx, o, name); err != nil {
			return err
		}
		if x, err = scanSaved(tx.QueryRow(ctx, `INSERT INTO saved_objects (id, person_id, tenant_id, name, title, kind, details, sealed, hint, check_state, created_by, updated_by)
			VALUES ($1, $2, $3, $4, $5, $6, $7, '{}', $8, $9, $10, $10) RETURNING `+savedCols,
			id, nullable(o.person), nullable(o.tenant), name, title, saved.KindFile, c.Details, c.Hint, saved.NoCheck, o.by)); err != nil {
			return err
		}
		if _, err := claimFilesTx(ctx, tx, o, id, map[string]string{"": meta.ID}); err != nil {
			return err
		}
		return savedEventBy(ctx, tx, who, o, id, "added", map[string]any{"name": name, "kind": saved.KindFile, "type": meta.Type})
	})
	return x, err
}

// changeSavedFileObject changes a file object: its name, title and note,
// and its file when a new upload is given (the old one let go).
func (s *Server) changeSavedFileObject(w http.ResponseWriter, r *http.Request, o savedOwner, was savedRow, in savedIn) {
	ctx := r.Context()
	name, title := was.Name, was.Title
	var err error
	if in.Name != nil {
		if name, err = saved.Name(*in.Name); err != nil {
			s.savedFail(w, r, err)
			return
		}
	}
	if in.Title != nil {
		if title, err = saved.Title(*in.Title); err != nil {
			s.savedFail(w, r, err)
			return
		}
	}
	var x savedRow
	var gone []string
	err = s.savedTx(ctx, o, func(tx pgx.Tx) error {
		cur, err := savedRowTx(ctx, tx, o, was.ID, true)
		if err != nil {
			return err
		}
		if !cur.UpdatedAt.Equal(was.UpdatedAt) {
			return errSavedChanged
		}
		meta, err := saved.FileOf(cur.Details)
		if err != nil {
			return err
		}
		if upload, _ := in.Details["file"].(string); upload != "" && upload != meta.ID {
			if meta, err = fileToClaimTx(ctx, tx, o, upload, cur.ID, ""); err != nil {
				return err
			}
			if gone, err = claimFilesTx(ctx, tx, o, cur.ID, map[string]string{"": meta.ID}); err != nil {
				return err
			}
		}
		note := cur.Details["note"]
		if in.Note != nil {
			note = *in.Note
		}
		if v, ok := in.Details["note"]; ok {
			note = v
		}
		c, err := saved.Clean(saved.KindFile, fileDetails(meta, note))
		if err != nil {
			return err
		}
		where, owner := ownerWhere(o)
		if x, err = scanSaved(tx.QueryRow(ctx, `UPDATE saved_objects SET name = $3, title = $4, details = $5, hint = $6, updated_by = $7, updated_at = now()
			WHERE `+where+` AND id = $2 RETURNING `+savedCols, owner, cur.ID, name, title, c.Details, c.Hint, o.by)); err != nil {
			return err
		}
		return s.savedEvent(r, tx, o, cur.ID, "changed", map[string]any{"changed": changedFields(was, x)})
	})
	if err != nil {
		s.savedFail(w, r, err)
		return
	}
	s.dropSavedPaths(ctx, o, gone)
	writeJSON(w, http.StatusOK, map[string]any{"saved": x.view(o, true)})
}
