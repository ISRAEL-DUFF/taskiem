package api

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/israel-duff/taskiem/connectors/notify"
	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/saved"
)

// Links to a business's saved files in the messages its workflows send
// (build plan H6; saved_file_links, 00644). A run reads a saved file as a
// reference ("saved-file:<id>"); a message sending it is given a short
// link when it is sent, good for a week, its token kept only as a hash.
// Opening the link asks the business's storage for the file then, for a
// few minutes, so nothing in a run's history or a message opens a file
// for longer than its link lasts, and a removed thing's link opens nothing.

// savedFileLinkTTL is how long a link in a message opens its file.
const savedFileLinkTTL = 7 * 24 * time.Hour

// savedFileOpenTTL is how long the storage's own link, behind it, lasts.
const savedFileOpenTTL = 5 * time.Minute

// SavedFileLinks makes the links (notify.FileLinks): for the notifier,
// which sends by SMS and email, and for the chat outbox.
type SavedFileLinks struct {
	Pool *pgxpool.Pool
	// BaseURL is where Taskiem answers: the links are under it.
	BaseURL string
}

// FileLink is the business's saved file ref's name and a new link to it.
func (f SavedFileLinks) FileLink(ctx context.Context, tenant uuid.UUID, ref string) (string, string, error) {
	id, err := uuid.Parse(strings.TrimPrefix(ref, notify.FileRefPrefix))
	if err != nil || !strings.HasPrefix(ref, notify.FileRefPrefix) {
		return "", "", notify.ErrNoFile
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", "", err
	}
	token := base64.RawURLEncoding.EncodeToString(raw)
	hash := sha256.Sum256([]byte(token))
	var name string
	err = db.InTenantTx(ctx, f.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		// A business's own file, not removed, kept in its storage.
		if err := tx.QueryRow(ctx, `SELECT COALESCE(o.details->>'file_name', o.title) FROM saved_objects o
			JOIN saved_files f ON f.object_id = o.id AND f.field = '' AND f.tenant_id = $1 AND f.person_id IS NULL AND f.path IS NOT NULL
			WHERE o.id = $2 AND o.tenant_id = $1 AND o.person_id IS NULL AND o.kind = $3 AND o.archived_at IS NULL`, tenant, id, saved.KindFile).Scan(&name); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `DELETE FROM saved_file_links WHERE tenant_id = $1 AND expires_at < now()`, tenant); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `INSERT INTO saved_file_links (token_hash, tenant_id, object_id, expires_at) VALUES ($1, $2, $3, now() + make_interval(secs => $4))`,
			hash[:], tenant, id, savedFileLinkTTL.Seconds())
		return err
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return "", "", notify.ErrNoFile
	}
	if err != nil {
		return "", "", err
	}
	return name, strings.TrimRight(f.BaseURL, "/") + "/v1/saved-files/" + token, nil
}

// savedFileLinks are the links the chat outbox gives a saved file.
func (s *Server) savedFileLinks() SavedFileLinks {
	return SavedFileLinks{Pool: s.Store.Pool, BaseURL: s.PublicURL}
}

// savedFileLine is the line a step's saved file adds to a message sent
// now ("" without one).
func (s *Server) savedFileLine(ctx context.Context, tenant uuid.UUID, in map[string]any) (name, link string, err error) {
	ref, err := notify.FileRef(in)
	if err != nil || ref == "" {
		return "", "", err
	}
	return s.savedFileLinks().FileLink(ctx, tenant, ref)
}

// openSavedFileLink is GET /v1/saved-files/{token}: a link a message
// carried, opened by whoever got it, signed in or not. While it lasts, a
// redirect to the business's storage for a few minutes; else not found.
func (s *Server) openSavedFileLink(w http.ResponseWriter, r *http.Request) {
	token := chi.URLParam(r, "token")
	if len(token) < 40 || len(token) > 64 {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	if !s.limiter("saved-file-link:"+clientIP(r), time.Minute, 30).Allow() {
		writeErr(w, http.StatusTooManyRequests, "slow down: a moment between requests")
		return
	}
	hash := sha256.Sum256([]byte(token))
	var tenant, object uuid.UUID
	if err := s.Store.Pool.QueryRow(r.Context(), `SELECT tenant_id, object_id FROM taskiem_saved_file_link($1)`, hash[:]).Scan(&tenant, &object); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeErr(w, http.StatusNotFound, "This link has expired, or the file is not there any more. Ask the business to send it again.")
			return
		}
		s.fail(w, r, err)
		return
	}
	var path string
	err := db.InTenantTx(r.Context(), s.Store.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		return tx.QueryRow(r.Context(), `SELECT f.path FROM saved_files f JOIN saved_objects o ON o.id = f.object_id AND o.archived_at IS NULL AND o.person_id IS NULL
			WHERE f.object_id = $1 AND f.field = '' AND f.tenant_id = $2 AND f.person_id IS NULL AND f.path IS NOT NULL`, object, tenant).Scan(&path)
	})
	if errors.Is(err, pgx.ErrNoRows) || err == nil && s.Media == nil {
		writeErr(w, http.StatusNotFound, "This link has expired, or the file is not there any more. Ask the business to send it again.")
		return
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	url, _, err := s.Media.SignURL(r.Context(), tenant, path, savedFileOpenTTL)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	http.Redirect(w, r, url, http.StatusFound)
}
