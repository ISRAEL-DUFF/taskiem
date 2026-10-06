package api

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
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
	"github.com/israel-duff/taskiem/engine/wd"
	"github.com/israel-duff/taskiem/engine/wdcheck"
	"github.com/israel-duff/taskiem/engine/wdmerge"
)

// problem is one validation failure, as returned to builders.
type problem = wdcheck.Problem

// check validates a WD document against the contract and against this
// platform: connectors and actions must exist, code must compile.
func (s *Server) check(ctx context.Context, tenant uuid.UUID, doc []byte) []problem {
	reg, err := s.Registry.For(ctx, tenant.String())
	if err != nil {
		return []problem{{Path: "/", Message: "the tenant's connectors could not be loaded: " + err.Error()}}
	}
	return wdcheck.Check(doc, reg)
}

// checkFor is check for the caller's tenant.
func (s *Server) checkFor(r *http.Request, doc []byte) []problem {
	return s.check(r.Context(), principalFrom(r.Context()).TenantID, doc)
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
	// Key is the definition's own id (wf_...) in the latest version: what
	// the CLI matches local files by.
	Key string `json:"key"`
	// GitPath is the file a Git-led repository keeps this workflow in; the
	// workflow is then read-only here.
	GitPath *string `json:"git_path"`
}

const latestKey = `COALESCE((SELECT definition->>'id' FROM workflow_versions WHERE workflow_id = w.id ORDER BY version DESC LIMIT 1), '')`

func (s *Server) listWorkflows(w http.ResponseWriter, r *http.Request) {
	var out []workflowSummary
	err := s.tx(r, func(tx pgx.Tx) error {
		rows, err := tx.Query(r.Context(), `SELECT w.id, w.name, w.active_version, COALESCE(max(v.version), 0), w.created_at, `+latestKey+`, w.git_path
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
	// ParentDigest is the digest of the version this edit started from.
	// When another version was saved since, the two edits are merged
	// three ways (spec 10.2). Without it the definition is saved as is.
	ParentDigest string `json:"parent_digest,omitempty"`
	// Resolutions settle conflicts a previous attempt reported:
	// conflict path -> "ours" | "theirs".
	Resolutions map[string]wdmerge.Resolution `json:"resolutions,omitempty"`
}

// mergeConflict is a save whose edits conflict with a newer version.
type mergeConflict struct {
	conflicts []wdmerge.Conflict
	latest    int
	digest    string
}

func (m *mergeConflict) Error() string { return "edits conflict with a newer version" }

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
	sum := sha256.Sum256(doc)
	writeJSON(w, http.StatusCreated, map[string]any{"id": id, "version": 1, "digest": hex.EncodeToString(sum[:]), "problems": nonNil(s.checkFor(r, doc))})
}

func insertVersion(r *http.Request, tx pgx.Tx, wf uuid.UUID, v int, doc []byte, layout json.RawMessage) error {
	p := principalFrom(r.Context())
	return insertVersionTx(r.Context(), tx, p.TenantID, wf, v, doc, layout, p.Actor(), "")
}

// insertVersionTx stores a new immutable version; commit is the Git commit
// it came from, if any.
func insertVersionTx(ctx context.Context, tx pgx.Tx, tenant, wf uuid.UUID, v int, doc []byte, layout json.RawMessage, by, commit string) error {
	sum := sha256.Sum256(doc)
	var lay any
	if len(layout) > 0 {
		lay = layout
	}
	_, err := tx.Exec(ctx, `INSERT INTO workflow_versions (workflow_id, version, tenant_id, definition, layout, digest, created_by, git_commit)
		VALUES ($1, $2, $3, $4, $5, $6, $7, NULLIF($8, ''))`, wf, v, tenant, doc, lay, sum[:], by, commit)
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
	merged := false
	err = s.tx(r, func(tx pgx.Tx) error {
		ctx := r.Context()
		// The row lock serialises version numbering per workflow.
		if err := tx.QueryRow(ctx, `SELECT 1 FROM workflows WHERE id = $1 FOR UPDATE`, wf).Scan(new(int)); err != nil {
			return err
		}
		if err := s.refuseGitManaged(ctx, tx, wf); err != nil {
			return err
		}
		var latest int
		var latestDigest string
		var latestDef []byte
		if err := tx.QueryRow(ctx, `SELECT version, encode(digest, 'hex'), definition FROM workflow_versions WHERE workflow_id = $1 ORDER BY version DESC LIMIT 1`, wf).
			Scan(&latest, &latestDigest, &latestDef); err != nil {
			return err
		}
		v = latest + 1
		if req.ParentDigest != "" && req.ParentDigest != latestDigest {
			var base []byte
			err := tx.QueryRow(ctx, `SELECT definition FROM workflow_versions WHERE workflow_id = $1 AND digest = decode($2, 'hex') ORDER BY version DESC LIMIT 1`,
				wf, req.ParentDigest).Scan(&base)
			if errors.Is(err, pgx.ErrNoRows) {
				return fmt.Errorf("%w: parent_digest %s is not a version of this workflow", errConflict, req.ParentDigest)
			}
			if err != nil {
				return err
			}
			out, conflicts, err := wdmerge.Merge(base, doc, latestDef, req.Resolutions)
			if err != nil {
				return fmt.Errorf("%w: %w", errBadRequest, err)
			}
			if len(conflicts) > 0 {
				return &mergeConflict{conflicts: conflicts, latest: latest, digest: latestDigest}
			}
			if doc, err = canonical(out); err != nil {
				return err
			}
			merged = true
		}
		return insertVersion(r, tx, wf, v, doc, req.Layout)
	})
	var mc *mergeConflict
	if errors.As(err, &mc) {
		writeJSON(w, http.StatusConflict, map[string]any{"error": mc.Error(), "conflicts": mc.conflicts, "latest_version": mc.latest, "latest_digest": mc.digest})
		return
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	sum := sha256.Sum256(doc)
	writeJSON(w, http.StatusCreated, map[string]any{"id": wf, "version": v, "digest": hex.EncodeToString(sum[:]), "merged": merged, "problems": nonNil(s.checkFor(r, doc))})
}

type versionInfo struct {
	Version     int        `json:"version"`
	State       string     `json:"state"`
	Digest      string     `json:"digest"`
	CreatedBy   *string    `json:"created_by"`
	CreatedAt   time.Time  `json:"created_at"`
	PublishedBy *uuid.UUID `json:"published_by"`
	PublishedAt *time.Time `json:"published_at"`
	GitCommit   *string    `json:"git_commit"`  // the commit a Git-led sync deployed it from
	GitRequest  *string    `json:"git_request"` // the pull or merge request a platform-led publish opened
}

func (s *Server) getWorkflow(w http.ResponseWriter, r *http.Request) {
	wf, err := uuid.Parse(chi.URLParam(r, "wf"))
	if err != nil {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	var sum workflowSummary
	var versions []versionInfo
	var deployments []deployment
	err = s.tx(r, func(tx pgx.Tx) error {
		ctx := r.Context()
		if err := tx.QueryRow(ctx, `SELECT w.id, w.name, w.active_version, COALESCE((SELECT max(version) FROM workflow_versions WHERE workflow_id = w.id), 0), w.created_at, `+latestKey+`, w.git_path
			FROM workflows w WHERE w.id = $1`, wf).Scan(&sum.ID, &sum.Name, &sum.ActiveVersion, &sum.LatestVersion, &sum.CreatedAt, &sum.Key, &sum.GitPath); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `SELECT version, state, encode(digest, 'hex'), created_by, created_at, published_by, published_at, git_commit, git_pr
			FROM workflow_versions WHERE workflow_id = $1 ORDER BY version DESC`, wf)
		if err != nil {
			return err
		}
		versions, err = pgx.CollectRows(rows, pgx.RowToStructByPos[versionInfo])
		if err != nil {
			return err
		}
		deployments, err = deploymentsOf(ctx, tx, wf)
		return err
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"workflow": sum, "versions": versions, "deployments": deployments})
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
		return tx.QueryRow(r.Context(), `SELECT version, state, encode(digest, 'hex'), created_by, created_at, published_by, published_at, git_commit, git_pr, definition, layout
			FROM workflow_versions WHERE workflow_id = $1 AND version = $2`, wf, v).
			Scan(&info.Version, &info.State, &info.Digest, &info.CreatedBy, &info.CreatedAt, &info.PublishedBy, &info.PublishedAt, &info.GitCommit, &info.GitRequest, &def, &layout)
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	out := map[string]any{"version": info, "definition": json.RawMessage(def), "problems": nonNil(s.checkFor(r, def))}
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
// published version is deprecated; its in-flight runs finish on it. With a
// platform-led Git connection, publishing also opens a pull request with
// the definition and its code (spec 10.3).
func (s *Server) publish(w http.ResponseWriter, r *http.Request) {
	wf, v, err := versionParams(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	p := principalFrom(r.Context())
	var probs []problem
	published, pending := false, false
	err = s.tx(r, func(tx pgx.Tx) error {
		if err := s.refuseGitManaged(r.Context(), tx, wf); err != nil {
			return err
		}
		// Four-eyes: a publish waits for a second person.
		g, err := governanceTx(r.Context(), tx)
		if err != nil {
			return err
		}
		if g.FourEyesPublish {
			var state string
			if err := tx.QueryRow(r.Context(), `SELECT state FROM workflow_versions WHERE workflow_id = $1 AND version = $2`, wf, v).Scan(&state); err != nil {
				return err
			}
			if state == "published" {
				return nil
			}
			if p.UserID == uuid.Nil {
				return fmt.Errorf("%w: with four-eyes publishing, a person asks to publish and another approves", errForbidden)
			}
			pending = true
			if _, err := tx.Exec(r.Context(), `INSERT INTO publish_requests (tenant_id, workflow_id, version, requested_by) VALUES ($1, $2, $3, $4)
				ON CONFLICT (workflow_id, version) DO UPDATE SET requested_by = EXCLUDED.requested_by, requested_at = now(), status = 'pending', decided_by = NULL, decided_at = NULL`,
				p.TenantID, wf, v, p.UserID); err != nil {
				return err
			}
			return auditTx(r, tx, "publish_request.create", fmt.Sprintf("%s/%d", wf, v), nil)
		}
		by := p.id()
		var digest string
		if probs, digest, published, err = s.publishTx(r.Context(), tx, p.TenantID, wf, v, &by, ""); err != nil || !published {
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
	if pending {
		writeJSON(w, http.StatusAccepted, map[string]any{"id": wf, "version": v, "state": "pending_approval"})
		return
	}
	out := map[string]any{"id": wf, "version": v, "state": "published"}
	if published {
		if g := s.proposeToGit(r, wf, v); g != nil {
			out["git"] = g
		}
	}
	writeJSON(w, http.StatusOK, out)
}

// publishTx publishes version v of wf: it checks the definition, deprecates
// the version it replaces, and deploys it, with its triggers, to every
// ungated environment, and to gitEnv when a Git sync for that environment
// publishes. A version already published (and deployed to gitEnv) reports
// published=false; one that fails its checks returns errInvalid with the
// problems.
func (s *Server) publishTx(ctx context.Context, tx pgx.Tx, tenant, wf uuid.UUID, v int, by *uuid.UUID, gitEnv string) (probs []problem, digest string, published bool, err error) {
	if err := tx.QueryRow(ctx, `SELECT 1 FROM workflows WHERE id = $1 FOR UPDATE`, wf).Scan(new(int)); err != nil {
		return nil, "", false, err
	}
	var def []byte
	var state string
	if err := tx.QueryRow(ctx, `SELECT definition, state, encode(digest, 'hex') FROM workflow_versions WHERE workflow_id = $1 AND version = $2`, wf, v).
		Scan(&def, &state, &digest); err != nil {
		return nil, "", false, err
	}
	if state == "published" {
		if gitEnv == "" {
			return nil, digest, false, nil
		}
		var at *int
		if err := tx.QueryRow(ctx, `SELECT version FROM deployments WHERE workflow_id = $1 AND environment = $2`, wf, gitEnv).Scan(&at); err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return nil, "", false, err
		}
		if at != nil && *at == v {
			return nil, digest, false, nil
		}
		d, err := wd.Load(def)
		if err != nil {
			return nil, "", false, err
		}
		return nil, digest, true, s.deployTx(ctx, tx, tenant, wf, gitEnv, v, d, deployer(by), "")
	}
	if state != "draft" {
		return nil, "", false, fmt.Errorf("%w: version %d is %s", errConflict, v, state)
	}
	if probs = s.check(ctx, tenant, def); len(probs) > 0 {
		return probs, "", false, errInvalid
	}
	if probs, err = missingPolicies(ctx, tx, def); err != nil || len(probs) > 0 {
		if err == nil {
			err = errInvalid
		}
		return probs, "", false, err
	}
	if _, err := tx.Exec(ctx, `UPDATE workflow_versions SET state = 'deprecated' WHERE workflow_id = $1 AND state = 'published'`, wf); err != nil {
		return nil, "", false, err
	}
	if _, err := tx.Exec(ctx, `UPDATE workflow_versions SET state = 'published', published_by = $3, published_at = now() WHERE workflow_id = $1 AND version = $2`,
		wf, v, by); err != nil {
		return nil, "", false, err
	}
	if _, err := tx.Exec(ctx, `UPDATE workflows SET active_version = $2 WHERE id = $1`, wf, v); err != nil {
		return nil, "", false, err
	}
	d, err := wd.Load(def)
	if err != nil {
		return nil, "", false, err
	}
	targets, err := deployTargets(ctx, tx, gitEnv)
	if err != nil {
		return nil, "", false, err
	}
	for _, env := range targets {
		if err := s.deployTx(ctx, tx, tenant, wf, env, v, d, deployer(by), ""); err != nil {
			return nil, "", false, err
		}
	}
	return nil, digest, true, nil
}

func deployer(by *uuid.UUID) *string {
	if by == nil {
		return nil
	}
	s := by.String()
	return &s
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
	probs := s.checkFor(r, req.Definition)
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
func (s *Server) listConnectors(w http.ResponseWriter, r *http.Request) {
	reg, err := s.Registry.For(r.Context(), principalFrom(r.Context()).TenantID.String())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	var out []connectorInfo
	for _, c := range reg.List() {
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

func nonNil[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}

type triggerInfo struct {
	ID          uuid.UUID  `json:"id"`
	Environment string     `json:"environment"`
	Type        string     `json:"type"`
	Version     int        `json:"version"`
	Path        *string    `json:"path,omitempty"`
	Auth        *string    `json:"auth,omitempty"`
	SecretName  *string    `json:"secret_name,omitempty"`
	Connector   *string    `json:"connector,omitempty"`
	Trigger     *string    `json:"trigger,omitempty"`
	Cron        *string    `json:"cron,omitempty"`
	Timezone    *string    `json:"timezone,omitempty"`
	NextFireAt  *time.Time `json:"next_fire_at,omitempty"`
	URL         string     `json:"url,omitempty"`
}

// listTriggers shows where a published workflow listens: webhook and
// connector URLs (relative to the edge host), and the next scheduled fire.
func (s *Server) listTriggers(w http.ResponseWriter, r *http.Request) {
	wf, err := uuid.Parse(chi.URLParam(r, "wf"))
	if err != nil {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	p := principalFrom(r.Context())
	var out []triggerInfo
	err = s.tx(r, func(tx pgx.Tx) error {
		rows, err := tx.Query(r.Context(), `SELECT id, environment, type, version, path, auth, secret_name, connector, trigger_name, cron, timezone, next_fire_at
			FROM triggers WHERE workflow_id = $1 ORDER BY environment`, wf)
		if err != nil {
			return err
		}
		out, err = pgx.CollectRows(rows, func(row pgx.CollectableRow) (triggerInfo, error) {
			var t triggerInfo
			err := row.Scan(&t.ID, &t.Environment, &t.Type, &t.Version, &t.Path, &t.Auth, &t.SecretName, &t.Connector, &t.Trigger, &t.Cron, &t.Timezone, &t.NextFireAt)
			return t, err
		})
		return err
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	for i, t := range out {
		base := "/hooks/" + p.TenantID.String()
		switch {
		case t.Path != nil:
			out[i].URL = base + *t.Path + "?env=" + t.Environment
		case t.Connector != nil:
			out[i].URL = base + "/connectors/" + *t.Connector + "/" + *t.Trigger + "?env=" + t.Environment
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"triggers": nonNil(out)})
}
