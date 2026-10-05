package api

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/connector"
	"github.com/israel-duff/taskiem/engine/sandbox"
	"github.com/israel-duff/taskiem/engine/wd"
)

// problem is one validation failure, as returned to builders.
type problem struct {
	Path    string `json:"path"`
	Message string `json:"message"`
}

// check validates a WD document against the contract and against this
// platform: connectors and actions must exist, code must compile.
func (s *Server) check(doc []byte) []problem {
	var out []problem
	for _, p := range wd.Validate(doc) {
		out = append(out, problem{p.Path, p.Message})
	}
	if len(out) > 0 {
		return out
	}
	def, err := wd.Load(doc)
	if err != nil {
		return []problem{{"/", err.Error()}}
	}
	var walk func(steps []*wd.Step)
	walk = func(steps []*wd.Step) {
		for _, st := range steps {
			path := "/steps/" + st.ID
			switch st.Type {
			case "connector":
				c, ok := s.Registry.Get(st.Connector)
				switch {
				case !ok:
					out = append(out, problem{path, fmt.Sprintf("connector %q is not available", st.Connector)})
				case !hasAction(c, st.Action):
					out = append(out, problem{path, fmt.Sprintf("connector %q has no action %q", st.Connector, st.Action)})
				}
			case "code":
				if st.Code != nil {
					if _, err := sandbox.Compile(st.Code.Source, st.Code.Language); err != nil {
						out = append(out, problem{path, "code: " + err.Error()})
					}
				}
			}
			for _, sub := range st.Children() {
				walk(sub)
			}
		}
	}
	walk(def.Steps)
	return out
}

// canonical compacts a JSON document; its SHA-256 is the version digest.
func canonical(doc json.RawMessage) ([]byte, error) {
	var buf bytes.Buffer
	if err := json.Compact(&buf, doc); err != nil {
		return nil, fmt.Errorf("%w: definition is not JSON: %w", errBadRequest, err)
	}
	return buf.Bytes(), nil
}

func (p *Principal) id() uuid.UUID {
	if p.KeyID != uuid.Nil {
		return p.KeyID
	}
	return p.UserID
}

type workflowSummary struct {
	ID            uuid.UUID `json:"id"`
	Name          string    `json:"name"`
	ActiveVersion *int      `json:"active_version"`
	LatestVersion int       `json:"latest_version"`
	CreatedAt     time.Time `json:"created_at"`
}

func (s *Server) listWorkflows(w http.ResponseWriter, r *http.Request) {
	var out []workflowSummary
	err := s.tx(r, func(tx pgx.Tx) error {
		rows, err := tx.Query(r.Context(), `SELECT w.id, w.name, w.active_version, COALESCE(max(v.version), 0), w.created_at
			FROM workflows w LEFT JOIN workflow_versions v ON v.workflow_id = w.id GROUP BY w.id ORDER BY w.name`)
		if err != nil {
			return err
		}
		out, err = pgx.CollectRows(rows, pgx.RowToStructByPos[workflowSummary])
		return err
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"workflows": nonNil(out)})
}

type versionReq struct {
	Name       string          `json:"name,omitempty"`
	Definition json.RawMessage `json:"definition"`
	Layout     json.RawMessage `json:"layout,omitempty"`
}

// createWorkflow creates a workflow with its first draft. Drafts may be
// incomplete; the response lists their problems. Only valid versions publish.
func (s *Server) createWorkflow(w http.ResponseWriter, r *http.Request) {
	var req versionReq
	if err := decodeBody(r, &req); err != nil {
		s.fail(w, r, err)
		return
	}
	if req.Name == "" {
		s.fail(w, r, fmt.Errorf("%w: name is required", errBadRequest))
		return
	}
	doc, err := canonical(req.Definition)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	p := principalFrom(r.Context())
	id := uuid.Must(uuid.NewV7())
	err = s.tx(r, func(tx pgx.Tx) error {
		ctx := r.Context()
		if _, err := tx.Exec(ctx, `INSERT INTO workflows (id, tenant_id, name, created_by, workspace_id)
			VALUES ($1, $2, $3, $4, (SELECT id FROM workspaces WHERE tenant_id = $2 ORDER BY created_at LIMIT 1))`, id, p.TenantID, req.Name, p.id()); err != nil {
			return err
		}
		if err := insertVersion(r, tx, id, 1, doc, req.Layout); err != nil {
			return err
		}
		return auditTx(r, tx, "workflow.create", id.String(), map[string]any{"name": req.Name})
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"id": id, "version": 1, "problems": nonNil(s.check(doc))})
}

func insertVersion(r *http.Request, tx pgx.Tx, wf uuid.UUID, v int, doc []byte, layout json.RawMessage) error {
	p := principalFrom(r.Context())
	sum := sha256.Sum256(doc)
	var lay any
	if len(layout) > 0 {
		lay = layout
	}
	_, err := tx.Exec(r.Context(), `INSERT INTO workflow_versions (workflow_id, version, tenant_id, definition, layout, digest, created_by)
		VALUES ($1, $2, $3, $4, $5, $6, $7)`, wf, v, p.TenantID, doc, lay, sum[:], p.Actor())
	return err
}

func (s *Server) createVersion(w http.ResponseWriter, r *http.Request) {
	wf, err := uuid.Parse(chi.URLParam(r, "wf"))
	if err != nil {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	var req versionReq
	if err := decodeBody(r, &req); err != nil {
		s.fail(w, r, err)
		return
	}
	doc, err := canonical(req.Definition)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	var v int
	err = s.tx(r, func(tx pgx.Tx) error {
		ctx := r.Context()
		// The row lock serialises version numbering per workflow.
		if err := tx.QueryRow(ctx, `SELECT 1 FROM workflows WHERE id = $1 FOR UPDATE`, wf).Scan(new(int)); err != nil {
			return err
		}
		if err := tx.QueryRow(ctx, `SELECT COALESCE(max(version), 0) + 1 FROM workflow_versions WHERE workflow_id = $1`, wf).Scan(&v); err != nil {
			return err
		}
		return insertVersion(r, tx, wf, v, doc, req.Layout)
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"id": wf, "version": v, "problems": nonNil(s.check(doc))})
}

type versionInfo struct {
	Version     int        `json:"version"`
	State       string     `json:"state"`
	Digest      string     `json:"digest"`
	CreatedBy   *string    `json:"created_by"`
	CreatedAt   time.Time  `json:"created_at"`
	PublishedBy *uuid.UUID `json:"published_by"`
	PublishedAt *time.Time `json:"published_at"`
}

func (s *Server) getWorkflow(w http.ResponseWriter, r *http.Request) {
	wf, err := uuid.Parse(chi.URLParam(r, "wf"))
	if err != nil {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	var sum workflowSummary
	var versions []versionInfo
	err = s.tx(r, func(tx pgx.Tx) error {
		ctx := r.Context()
		if err := tx.QueryRow(ctx, `SELECT w.id, w.name, w.active_version, COALESCE((SELECT max(version) FROM workflow_versions WHERE workflow_id = w.id), 0), w.created_at
			FROM workflows w WHERE w.id = $1`, wf).Scan(&sum.ID, &sum.Name, &sum.ActiveVersion, &sum.LatestVersion, &sum.CreatedAt); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `SELECT version, state, encode(digest, 'hex'), created_by, created_at, published_by, published_at
			FROM workflow_versions WHERE workflow_id = $1 ORDER BY version DESC`, wf)
		if err != nil {
			return err
		}
		versions, err = pgx.CollectRows(rows, pgx.RowToStructByPos[versionInfo])
		return err
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"workflow": sum, "versions": versions})
}

func versionParams(r *http.Request) (uuid.UUID, int, error) {
	wf, err := uuid.Parse(chi.URLParam(r, "wf"))
	if err != nil {
		return uuid.Nil, 0, pgx.ErrNoRows
	}
	v, err := strconv.Atoi(chi.URLParam(r, "v"))
	if err != nil || v < 1 {
		return uuid.Nil, 0, pgx.ErrNoRows
	}
	return wf, v, nil
}

func (s *Server) getVersion(w http.ResponseWriter, r *http.Request) {
	wf, v, err := versionParams(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	var info versionInfo
	var def, layout []byte
	err = s.tx(r, func(tx pgx.Tx) error {
		return tx.QueryRow(r.Context(), `SELECT version, state, encode(digest, 'hex'), created_by, created_at, published_by, published_at, definition, layout
			FROM workflow_versions WHERE workflow_id = $1 AND version = $2`, wf, v).
			Scan(&info.Version, &info.State, &info.Digest, &info.CreatedBy, &info.CreatedAt, &info.PublishedBy, &info.PublishedAt, &def, &layout)
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	out := map[string]any{"version": info, "definition": json.RawMessage(def), "problems": nonNil(s.check(def))}
	if layout != nil {
		out["layout"] = json.RawMessage(layout)
	}
	writeJSON(w, http.StatusOK, out)
}

// putLayout saves canvas positions; they are not part of the definition.
func (s *Server) putLayout(w http.ResponseWriter, r *http.Request) {
	wf, v, err := versionParams(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	var layout json.RawMessage
	if err := decodeBody(r, &layout); err != nil {
		s.fail(w, r, err)
		return
	}
	err = s.tx(r, func(tx pgx.Tx) error {
		tag, err := tx.Exec(r.Context(), `UPDATE workflow_versions SET layout = $3 WHERE workflow_id = $1 AND version = $2`, wf, v, []byte(layout))
		if err == nil && tag.RowsAffected() == 0 {
			err = pgx.ErrNoRows
		}
		return err
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// publish makes a valid version the one new runs use. The previously
// published version is deprecated; its in-flight runs finish on it.
func (s *Server) publish(w http.ResponseWriter, r *http.Request) {
	wf, v, err := versionParams(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	p := principalFrom(r.Context())
	var probs []problem
	err = s.tx(r, func(tx pgx.Tx) error {
		ctx := r.Context()
		if err := tx.QueryRow(ctx, `SELECT 1 FROM workflows WHERE id = $1 FOR UPDATE`, wf).Scan(new(int)); err != nil {
			return err
		}
		var def []byte
		var state, digest string
		if err := tx.QueryRow(ctx, `SELECT definition, state, encode(digest, 'hex') FROM workflow_versions WHERE workflow_id = $1 AND version = $2`, wf, v).
			Scan(&def, &state, &digest); err != nil {
			return err
		}
		if state == "published" {
			return nil
		}
		if state != "draft" {
			return fmt.Errorf("%w: version %d is %s", errConflict, v, state)
		}
		if probs = s.check(def); len(probs) > 0 {
			return errInvalid
		}
		if _, err := tx.Exec(ctx, `UPDATE workflow_versions SET state = 'deprecated' WHERE workflow_id = $1 AND state = 'published'`, wf); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE workflow_versions SET state = 'published', published_by = $3, published_at = now() WHERE workflow_id = $1 AND version = $2`,
			wf, v, p.id()); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE workflows SET active_version = $2 WHERE id = $1`, wf, v); err != nil {
			return err
		}
		return auditTx(r, tx, "workflow.publish", fmt.Sprintf("%s/%d", wf, v), map[string]any{"digest": digest})
	})
	if errors.Is(err, errInvalid) {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": "definition is not valid", "problems": probs})
		return
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": wf, "version": v, "state": "published"})
}

var errInvalid = errors.New("invalid definition")

// validate checks a definition without saving it (the canvas and the CLI).
func (s *Server) validate(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Definition json.RawMessage `json:"definition"`
	}
	if err := decodeBody(r, &req); err != nil {
		s.fail(w, r, err)
		return
	}
	probs := s.check(req.Definition)
	writeJSON(w, http.StatusOK, map[string]any{"valid": len(probs) == 0, "problems": nonNil(probs)})
}

type connectorInfo struct {
	Ref      string                     `json:"ref"`
	ID       string                     `json:"id"`
	Version  string                     `json:"version"`
	Name     string                     `json:"name"`
	Auth     connector.Auth             `json:"auth"`
	Actions  map[string]connectorAction `json:"actions"`
	Triggers []string                   `json:"triggers"`
}

type connectorAction struct {
	Title  string          `json:"title"`
	Class  string          `json:"class"`
	Input  json.RawMessage `json:"input,omitempty"`
	Output json.RawMessage `json:"output,omitempty"`
}

// listConnectors describes the connectors this deployment runs, for the
// canvas palette and schema forms.
func (s *Server) listConnectors(w http.ResponseWriter, _ *http.Request) {
	var out []connectorInfo
	for _, c := range s.Registry.List() {
		m := c.Manifest
		info := connectorInfo{Ref: c.Ref(), ID: m.ID, Version: m.Version, Name: m.Name, Auth: m.Auth, Actions: map[string]connectorAction{}, Triggers: []string{}}
		for name, a := range m.Actions {
			info.Actions[name] = connectorAction{Title: a.Title, Class: string(a.Class), Input: a.Input, Output: a.Output}
		}
		for name := range m.Triggers {
			info.Triggers = append(info.Triggers, name)
		}
		slices.Sort(info.Triggers)
		out = append(out, info)
	}
	writeJSON(w, http.StatusOK, map[string]any{"connectors": nonNil(out)})
}

func hasAction(c *connector.Connector, action string) bool {
	_, ok := c.Manifest.Actions[action]
	return ok
}

func nonNil[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}
