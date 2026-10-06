package api

import (
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"sort"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/wasmconn"
)

// Tenants' own connectors (spec 6): a connector/v1 manifest and a
// WebAssembly module, uploaded together and immutable once uploaded.

type tenantConnector struct {
	ID         string     `json:"id"`
	Version    string     `json:"version"`
	Ref        string     `json:"ref"`
	Digest     string     `json:"digest"`
	UploadedBy string     `json:"uploaded_by"`
	UploadedAt time.Time  `json:"uploaded_at"`
	Disabled   *time.Time `json:"disabled_at,omitempty"`
	Active     bool       `json:"active"` // the version new runs use for its major
}

func (s *Server) listTenantConnectors(w http.ResponseWriter, r *http.Request) {
	var out []tenantConnector
	err := s.tx(r, func(tx pgx.Tx) error {
		rows, err := tx.Query(r.Context(), `SELECT connector_id, version, encode(digest, 'hex'), uploaded_by, uploaded_at, disabled_at
			FROM tenant_connectors ORDER BY connector_id, uploaded_at`)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var c tenantConnector
			if err := rows.Scan(&c.ID, &c.Version, &c.Digest, &c.UploadedBy, &c.UploadedAt, &c.Disabled); err != nil {
				return err
			}
			out = append(out, c)
		}
		return rows.Err()
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	newest := map[string]int{}
	for i := range out {
		c := &out[i]
		c.Ref = c.ID + "@" + majorOf(c.Version)
		if c.Disabled != nil {
			continue
		}
		if j, ok := newest[c.Ref]; !ok || wasmconn.Newer(c.Version, out[j].Version) {
			newest[c.Ref] = i
		}
	}
	for _, i := range newest {
		out[i].Active = true
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	writeJSON(w, http.StatusOK, map[string]any{"connectors": nonNil(out)})
}

func majorOf(v string) string {
	for i := range len(v) {
		if v[i] == '.' {
			return v[:i]
		}
	}
	return v
}

// uploadTenantConnector takes multipart fields "manifest" (YAML or JSON)
// and "module" (.wasm), checks that they load, and stores them.
func (s *Server) uploadTenantConnector(w http.ResponseWriter, r *http.Request) {
	if s.Connectors == nil {
		writeErr(w, http.StatusNotImplemented, "this deployment does not run tenant connectors")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 40<<20)
	if err := r.ParseMultipartForm(1 << 20); err != nil { //nolint:gosec // the body is capped above by MaxBytesReader
		s.fail(w, r, fmt.Errorf("%w: send multipart fields manifest and module: %w", errBadRequest, err))
		return
	}
	defer func() { _ = r.MultipartForm.RemoveAll() }()
	manifest, err := formFile(r.MultipartForm, "manifest", 1<<20)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	module, err := formFile(r.MultipartForm, "module", 33<<20)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	c, err := s.Connectors.Runtime.Load(r.Context(), manifest, module)
	if errors.Is(err, wasmconn.ErrInvalid) {
		s.fail(w, r, fmt.Errorf("%w: %w", errBadRequest, err))
		return
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	m, p := c.Manifest, principalFrom(r.Context())
	digest := wasmconn.Digest(module)
	sum, _ := hex.DecodeString(digest)
	actions := make([]string, 0, len(m.Actions))
	for a := range m.Actions {
		actions = append(actions, a)
	}
	sort.Strings(actions)
	err = s.tx(r, func(tx pgx.Tx) error {
		if _, err := tx.Exec(r.Context(), `INSERT INTO tenant_connectors (tenant_id, connector_id, version, manifest, module, digest, uploaded_by)
			VALUES ($1, $2, $3, $4, $5, $6, $7)`, p.TenantID, m.ID, m.Version, string(manifest), module, sum, p.Actor()); err != nil {
			var pgErr interface{ SQLState() string }
			if errors.As(err, &pgErr) && pgErr.SQLState() == "23505" {
				return fmt.Errorf("%w: %s %s is already uploaded; versions never change, so raise the version", errConflict, m.ID, m.Version)
			}
			return err
		}
		return auditTx(r, tx, "connector.upload", m.ID+"@"+m.Version, map[string]any{"digest": digest, "actions": actions, "hosts": m.Hosts()})
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.Connectors.Forget(p.TenantID.String())
	writeJSON(w, http.StatusCreated, map[string]any{"id": m.ID, "version": m.Version, "ref": c.Ref(), "digest": digest, "actions": actions, "hosts": m.Hosts()})
}

func formFile(f *multipart.Form, name string, limit int64) ([]byte, error) {
	fh := f.File[name]
	if len(fh) != 1 {
		if v := f.Value[name]; len(v) == 1 {
			return []byte(v[0]), nil
		}
		return nil, fmt.Errorf("%w: field %q is required", errBadRequest, name)
	}
	if fh[0].Size > limit {
		return nil, fmt.Errorf("%w: %s is larger than %d bytes", errBadRequest, name, limit)
	}
	rd, err := fh[0].Open()
	if err != nil {
		return nil, err
	}
	defer func() { _ = rd.Close() }()
	return io.ReadAll(io.LimitReader(rd, limit))
}

// disableTenantConnector withdraws a version from new runs. When it was
// the newest of its major, the previous enabled version takes over.
func (s *Server) disableTenantConnector(w http.ResponseWriter, r *http.Request) {
	id, version := chi.URLParam(r, "id"), chi.URLParam(r, "version")
	p := principalFrom(r.Context())
	err := s.tx(r, func(tx pgx.Tx) error {
		tag, err := tx.Exec(r.Context(), `UPDATE tenant_connectors SET disabled_at = now(), disabled_by = $3
			WHERE connector_id = $1 AND version = $2 AND disabled_at IS NULL`, id, version, p.Actor())
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return pgx.ErrNoRows
		}
		return auditTx(r, tx, "connector.disable", id+"@"+version, nil)
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if s.Connectors != nil {
		s.Connectors.Forget(p.TenantID.String())
	}
	w.WriteHeader(http.StatusNoContent)
}
