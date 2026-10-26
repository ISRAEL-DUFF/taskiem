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
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/connector"
	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/embed"
	"github.com/israel-duff/taskiem/engine/expansion"
	"github.com/israel-duff/taskiem/engine/remote"
	"github.com/israel-duff/taskiem/engine/runtime"
	"github.com/israel-duff/taskiem/engine/wd"
	"github.com/israel-duff/taskiem/engine/wdcheck"
	"github.com/israel-duff/taskiem/engine/wddiff"
	"github.com/israel-duff/taskiem/engine/wdmerge"
	"github.com/israel-duff/taskiem/templates"
)

// problem is one validation failure, as returned to builders.
type problem = wdcheck.Problem

// check validates a WD document against the contract and against this
// platform: connectors and actions must exist, code must compile.
func (s *Server) check(ctx context.Context, tenant uuid.UUID, doc []byte) []problem {
	// Checking reads connectors' manifests: nothing is compiled for it.
	reg, err := s.Registry.For(connector.ManifestsOnly(ctx), tenant.String())
	if err != nil {
		return []problem{{Path: "/", Message: "the tenant's connectors could not be loaded: " + err.Error()}}
	}
	out := append(s.checkWith(ctx, tenant, doc, reg), s.keptProblems(ctx, tenant, doc)...)
	out = append(out, s.subflowProblems(ctx, tenant, doc)...)
	return append(out, s.aiStepProblems(ctx, tenant, doc)...)
}

// callables finds a template step's template for a definition not saved
// yet: a library template, or one of the tenant's callable workflows,
// each read in a transaction of its own.
func (s *Server) callables(ctx context.Context, tenant uuid.UUID) expansion.Templates {
	library := expansion.Library(templates.Default())
	return func(id string) (*templates.Template, error) {
		if !runtime.IsWorkflowID(id) {
			return library(id)
		}
		var t *templates.Template
		err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
			var err error
			t, err = runtime.Callables(ctx, tx, templates.Default())(id)
			return err
		})
		return t, err
	}
}

// checkVersion is check for a saved version: its template steps are
// checked with the templates it pinned when it was saved (decision 0047).
func (s *Server) checkVersion(ctx context.Context, tx pgx.Tx, tenant, wf uuid.UUID, v int, doc []byte) []problem {
	if !strings.Contains(string(doc), `"template"`) {
		return s.check(ctx, tenant, doc)
	}
	if p := usesItself(wf, doc); p != nil {
		return []problem{*p}
	}
	find, _, err := runtime.PinnedTemplates(ctx, tx, wf, v, runtime.Callables(ctx, tx, templates.Default()))
	if err != nil {
		return []problem{{Path: "/", Message: "the version's templates could not be loaded: " + err.Error()}}
	}
	reg, err := s.Registry.For(connector.ManifestsOnly(ctx), tenant.String())
	if err != nil {
		return []problem{{Path: "/", Message: "the tenant's connectors could not be loaded: " + err.Error()}}
	}
	var out []problem
	if err := s.withCodeSlot(ctx, tenant, func() { out = wdcheck.CheckWith(doc, reg, find) }); err != nil {
		return []problem{{Path: "/", Message: "the definition could not be checked: " + err.Error()}}
	}
	out = append(out, s.keptProblems(ctx, tenant, doc)...)
	out = append(out, s.subflowProblems(ctx, tenant, doc)...)
	return append(out, s.aiStepProblems(ctx, tenant, doc)...)
}

// checkWith is wdcheck.Check holding a tenant-code slot: checking code
// steps compiles them, and a Python check runs the interpreter.
func (s *Server) checkWith(ctx context.Context, tenant uuid.UUID, doc []byte, reg connector.Lookup) []problem {
	var out []problem
	find := s.callables(ctx, tenant)
	if err := s.withCodeSlot(ctx, tenant, func() { out = wdcheck.CheckWith(doc, reg, find) }); err != nil {
		return []problem{{Path: "/", Message: "the definition could not be checked: " + err.Error()}}
	}
	return out
}

// checkFor is check for the caller's tenant; on a request about a
// workflow, it also refuses the workflow using itself as a step.
func (s *Server) checkFor(r *http.Request, doc []byte) []problem {
	if wf, err := uuid.Parse(chi.URLParam(r, "wf")); err == nil {
		if p := usesItself(wf, doc); p != nil {
			return []problem{*p}
		}
	}
	return s.check(r.Context(), principalFrom(r.Context()).TenantID, doc)
}

// usesItself is the problem of a workflow whose template steps name the
// workflow itself (decision 0048): its published version would be a copy
// of what is being saved. A subflow step naming the workflow itself
// (decision 0049) would run it without end.
func usesItself(wf uuid.UUID, doc []byte) *problem {
	var root map[string]any
	if json.Unmarshal(doc, &root) != nil {
		return nil
	}
	if slices.Contains(embed.WorkflowsOf(root["steps"]), wf.String()) {
		return &problem{Path: "/steps", Message: "a workflow may not use itself as a step"}
	}
	if slices.Contains(subflowsOf(root["steps"]), wf.String()) {
		return &problem{Path: "/steps", Message: "a workflow may not run itself as a subflow"}
	}
	return nil
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
	if p.EndUser != nil {
		return p.EndUser.ID
	}
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
	// Trigger is what starts it: the published version's trigger, else the
	// latest draft's (the list says "Every day at 08:00").
	Trigger json.RawMessage `json:"trigger"`
	// Recent counts the runs started on each of the last 14 days, oldest
	// first, in the time zone the list asks for (?tz=, default Africa/Lagos).
	Recent []int `json:"recent" db:"-"`
}

// recentDays is how many days a workflow summary's Recent covers.
const recentDays = 14

const latestKey = `COALESCE((SELECT definition->>'id' FROM workflow_versions WHERE workflow_id = w.id ORDER BY version DESC LIMIT 1), '')`

// liveTrigger selects a summary's Trigger: the published version's, else
// the latest draft's.
const liveTrigger = `COALESCE((SELECT definition->'trigger' FROM workflow_versions WHERE workflow_id = w.id ORDER BY version = w.active_version DESC, version DESC LIMIT 1), 'null')`

func (s *Server) listWorkflows(w http.ResponseWriter, r *http.Request) {
	tz := r.URL.Query().Get("tz")
	if tz == "" {
		tz = "Africa/Lagos"
	}
	if _, err := time.LoadLocation(tz); err != nil {
		s.fail(w, r, fmt.Errorf("%w: unknown time zone %q", errBadRequest, tz))
		return
	}
	var out []workflowSummary
	err := s.tx(r, func(tx pgx.Tx) error {
		rows, err := tx.Query(r.Context(), `SELECT w.id, w.name, w.active_version, COALESCE(max(v.version), 0), w.created_at, `+latestKey+`, w.git_path, `+liveTrigger+`
			FROM workflows w LEFT JOIN workflow_versions v ON v.workflow_id = w.id GROUP BY w.id ORDER BY w.name`)
		if err != nil {
			return err
		}
		if out, err = pgx.CollectRows(rows, pgx.RowToStructByPos[workflowSummary]); err != nil {
			return err
		}
		return recentRuns(r.Context(), tx, out, tz)
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"workflows": nonNil(out)})
}

// recentRuns fills each summary's Recent: runs started per day over the
// last recentDays days in time zone tz, today last.
func recentRuns(ctx context.Context, tx pgx.Tx, out []workflowSummary, tz string) error {
	at := make(map[uuid.UUID]int, len(out))
	for i := range out {
		out[i].Recent = make([]int, recentDays)
		at[out[i].ID] = i
	}
	rows, err := tx.Query(ctx, `SELECT workflow_id, (now() AT TIME ZONE $1)::date - (started_at AT TIME ZONE $1)::date, count(*)
		FROM runs WHERE started_at > now() - make_interval(days => $2 + 1) GROUP BY 1, 2`, tz, recentDays)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var wf uuid.UUID
		var ago, n int
		if err := rows.Scan(&wf, &ago, &n); err != nil {
			return err
		}
		if i, ok := at[wf]; ok && ago >= 0 && ago < recentDays {
			out[i].Recent[recentDays-1-ago] = n
		}
	}
	return rows.Err()
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
		if err := s.checkCount(ctx, tx, p.TenantID, "max_workflows"); err != nil {
			return err
		}
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
	if _, err := tx.Exec(ctx, `INSERT INTO workflow_versions (workflow_id, version, tenant_id, definition, layout, digest, created_by, git_commit)
		VALUES ($1, $2, $3, $4, $5, $6, $7, NULLIF($8, ''))`, wf, v, tenant, doc, lay, sum[:], by, commit); err != nil {
		return err
	}
	// The templates its template steps use, as the library has them now:
	// a later library update is offered, never taken (decision 0047).
	return runtime.PinTemplates(ctx, tx, tenant, wf, v, doc, runtime.Callables(ctx, tx, templates.Default()))
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
	// AIBuild is the AI build a version was saved from: the AI is its
	// co-author, the person in created_by its author.
	AIBuild *uuid.UUID `json:"ai_build"`
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
	var from string // the template its first version was made from
	var updates []runtime.TemplateUpdate
	err = s.tx(r, func(tx pgx.Tx) error {
		ctx := r.Context()
		if err := tx.QueryRow(ctx, `SELECT w.id, w.name, w.active_version, COALESCE((SELECT max(version) FROM workflow_versions WHERE workflow_id = w.id), 0), w.created_at, `+latestKey+`, w.git_path, `+liveTrigger+`
			FROM workflows w WHERE w.id = $1`, wf).Scan(&sum.ID, &sum.Name, &sum.ActiveVersion, &sum.LatestVersion, &sum.CreatedAt, &sum.Key, &sum.GitPath, &sum.Trigger); err != nil {
			return err
		}
		one := []workflowSummary{sum}
		if err := recentRuns(ctx, tx, one, "Africa/Lagos"); err != nil {
			return err
		}
		sum = one[0]
		rows, err := tx.Query(ctx, `SELECT version, state, encode(digest, 'hex'), created_by, created_at, published_by, published_at, git_commit, git_pr, ai_build_id
			FROM workflow_versions WHERE workflow_id = $1 ORDER BY version DESC`, wf)
		if err != nil {
			return err
		}
		versions, err = pgx.CollectRows(rows, pgx.RowToStructByPos[versionInfo])
		if err != nil {
			return err
		}
		deployments, err = deploymentsOf(ctx, tx, wf)
		if err != nil {
			return err
		}
		if err := tx.QueryRow(ctx, `SELECT template_id FROM workflow_versions WHERE workflow_id = $1 AND template_id IS NOT NULL ORDER BY version LIMIT 1`, wf).Scan(&from); err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		// The templates the latest version's template steps pinned that
		// the library has changed since: the next version saved takes them.
		callables := runtime.Callables(ctx, tx, templates.Default())
		_, pinned, err := runtime.PinnedTemplates(ctx, tx, wf, sum.LatestVersion, callables)
		if err != nil {
			return err
		}
		updates = runtime.TemplateUpdates(pinned, callables, func(a, b []byte) []string { return wddiff.Diff(a, b) })
		return nil
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	out := map[string]any{"workflow": sum, "versions": versions, "deployments": deployments}
	if len(updates) > 0 {
		out["template_updates"] = updates
	}
	// Made from a template another now replaces: offer the new one.
	if t, ok := templates.Default().Get(from); ok && t.SupersededBy != "" {
		if next, ok := templates.Default().Get(t.SupersededBy); ok {
			out["upgrade"] = map[string]string{"template": next.ID, "title": next.Title}
		}
	}
	writeJSON(w, http.StatusOK, out)
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
	var probs []problem
	err = s.tx(r, func(tx pgx.Tx) error {
		if err := tx.QueryRow(r.Context(), `SELECT version, state, encode(digest, 'hex'), created_by, created_at, published_by, published_at, git_commit, git_pr, ai_build_id, definition, layout
			FROM workflow_versions WHERE workflow_id = $1 AND version = $2`, wf, v).
			Scan(&info.Version, &info.State, &info.Digest, &info.CreatedBy, &info.CreatedAt, &info.PublishedBy, &info.PublishedAt, &info.GitCommit, &info.GitRequest, &info.AIBuild, &def, &layout); err != nil {
			return err
		}
		probs = s.checkVersion(r.Context(), tx, principalFrom(r.Context()).TenantID, wf, v, def)
		return nil
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	out := map[string]any{"version": info, "definition": json.RawMessage(def), "problems": nonNil(probs)}
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
		// Ready: nothing a run would fail on at once is missing.
		if err := s.checkReady(r.Context(), tx, p.TenantID, wf, v); err != nil {
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
			// A person asks, or an embed app's end user (docs/embedding.md):
			// a platform person approves. API keys do not ask.
			if p.UserID == uuid.Nil && p.EndUser == nil {
				return fmt.Errorf("%w: with four-eyes publishing, a person asks to publish and another approves", errForbidden)
			}
			pending = true
			if _, err := tx.Exec(r.Context(), `INSERT INTO publish_requests (tenant_id, workflow_id, version, requested_by) VALUES ($1, $2, $3, $4)
				ON CONFLICT (workflow_id, version) DO UPDATE SET requested_by = EXCLUDED.requested_by, requested_at = now(), status = 'pending', decided_by = NULL, decided_at = NULL`,
				p.TenantID, wf, v, p.Human()); err != nil {
				return err
			}
			return auditTx(r, tx, "publish_request.create", fmt.Sprintf("%s/%d", wf, v), nil)
		}
		by := p.id()
		var digest string
		if probs, digest, published, err = s.publishTx(r.Context(), tx, p.TenantID, wf, v, &by, ""); err != nil {
			return err
		}
		if !published {
			// Publishing a published version again repairs its remote
			// subscriptions (a webhook paused, broken or deleted at the
			// provider).
			return remote.Repair(r.Context(), tx, wf)
		}
		return auditTx(r, tx, "workflow.publish", fmt.Sprintf("%s/%d", wf, v), map[string]any{"digest": digest})
	})
	if errors.Is(err, errInvalid) {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": "definition is not valid", "problems": probs})
		return
	}
	var notReady *errNotReady
	if errors.As(err, &notReady) {
		writeNotReady(w, notReady.needs)
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
	if subs := s.applyRemote(r, wf); len(subs) > 0 {
		out["remote_subscriptions"] = subs
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
	return s.publishToTx(ctx, tx, tenant, wf, v, by, gitEnv, nil)
}

// publishToTx is publishTx deploying to the environments in only (which
// must be ungated) rather than every ungated one, when only is not nil.
func (s *Server) publishToTx(ctx context.Context, tx pgx.Tx, tenant, wf uuid.UUID, v int, by *uuid.UUID, gitEnv string, only []string) (probs []problem, digest string, published bool, err error) {
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
	if probs = s.checkVersion(ctx, tx, tenant, wf, v, def); len(probs) > 0 {
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
	if err := auditAIPersonalData(ctx, tx, tenant, wf, v, d, by); err != nil {
		return nil, "", false, err
	}
	targets, err := deployTargets(ctx, tx, gitEnv)
	if err != nil {
		return nil, "", false, err
	}
	if only != nil {
		targets = slices.DeleteFunc(targets, func(env string) bool { return !slices.Contains(only, env) })
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
	Ref     string `json:"ref"`
	ID      string `json:"id"`
	Version string `json:"version"`
	Name    string `json:"name"`
	// Kind is rail, product or tool (decision 0044); Category and
	// Description are the manifest's, for the catalogue.
	Kind        string                     `json:"kind"`
	Category    string                     `json:"category"`
	Description string                     `json:"description"`
	Auth        connector.Auth             `json:"auth"`
	Actions     map[string]connectorAction `json:"actions"`
	Triggers    []string                   `json:"triggers"`
	// Capabilities are a rail's (rule 16), sorted; absent for the others.
	Capabilities []string `json:"capabilities,omitempty"`
	// OAuthLinkable says whether a connector that declares auth.oauth2
	// can be connected by OAuth on this platform (a client is registered
	// and the provider's server known); absent for the others.
	OAuthLinkable *bool `json:"oauth_linkable,omitempty"`
	// OAuthModes are the modes it can be linked in here (live, test), for
	// a connector whose modes are hosts.
	OAuthModes []string `json:"oauth_modes,omitempty"`
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
		info := connectorInfo{Ref: c.Ref(), ID: m.ID, Version: m.Version, Name: m.Name, Kind: m.KindOf(), Category: m.Category, Description: m.Description, Auth: m.Auth, Actions: map[string]connectorAction{}, Triggers: []string{}, OAuthLinkable: s.oauthLinkable(c), OAuthModes: s.oauthModes(c)}
		for name, a := range m.Actions {
			info.Actions[name] = connectorAction{Title: a.Title, Class: string(a.Class), Input: a.Input, Output: a.Output}
		}
		for name := range m.Triggers {
			info.Triggers = append(info.Triggers, name)
		}
		slices.Sort(info.Triggers)
		for name := range m.Capabilities {
			info.Capabilities = append(info.Capabilities, name)
		}
		slices.Sort(info.Capabilities)
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
	ServiceCode *string    `json:"service_code,omitempty"` // ussd: the code the edge routes by
	URL         string     `json:"url,omitempty"`
	// Email is an email trigger's address.
	Email string `json:"email,omitempty"`
	// Remote is the subscription Taskiem keeps at the provider for a
	// remotely registered trigger (decision 0021); its URL is Taskiem's to
	// manage.
	Remote *remote.Status `json:"remote,omitempty"`
	// Polled: Taskiem also asks the provider every few minutes (in prod),
	// so setting the address up in its dashboard is optional (N4).
	Polled bool `json:"polled,omitempty"`
	// Service is the connector's name (Paystack), for a connector event.
	Service string `json:"service,omitempty"`
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
	var subs []remote.Status
	addrs := map[string]string{}
	err = s.tx(r, func(tx pgx.Tx) error {
		rows, err := tx.Query(r.Context(), `SELECT id, environment, type, version, path, auth, secret_name, connector, trigger_name, cron, timezone, NULLIF(next_fire_at, 'infinity'), service_code
			FROM triggers WHERE workflow_id = $1 ORDER BY environment`, wf)
		if err != nil {
			return err
		}
		out, err = pgx.CollectRows(rows, func(row pgx.CollectableRow) (triggerInfo, error) {
			var t triggerInfo
			err := row.Scan(&t.ID, &t.Environment, &t.Type, &t.Version, &t.Path, &t.Auth, &t.SecretName, &t.Connector, &t.Trigger, &t.Cron, &t.Timezone, &t.NextFireAt, &t.ServiceCode)
			return t, err
		})
		if err != nil {
			return err
		}
		if subs, err = remote.List(r.Context(), tx, `workflow_id = $1`, wf); err != nil {
			return err
		}
		rows, err = tx.Query(r.Context(), `SELECT environment, public_id FROM email_endpoints WHERE workflow_id = $1 AND status = 'live'`, wf)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var env, public string
			if err := rows.Scan(&env, &public); err != nil {
				return err
			}
			addrs[env] = public
		}
		return rows.Err()
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	reg, _ := s.Registry.For(r.Context(), p.TenantID.String())
	for i, t := range out {
		for j, sub := range subs {
			if sub.Desired == "present" && sub.Environment == t.Environment && t.Connector != nil && sub.Connector == *t.Connector {
				out[i].Remote = &subs[j]
			}
		}
		base := "/hooks/" + p.TenantID.String()
		if t.Type == "email" && s.MailDomain != "" && addrs[t.Environment] != "" {
			out[i].Email = addrs[t.Environment] + "@" + s.MailDomain
		}
		switch {
		case t.Path != nil:
			out[i].URL = base + *t.Path + "?env=" + t.Environment
		case t.Connector != nil:
			out[i].URL = base + "/connectors/" + *t.Connector + "/" + *t.Trigger + "?env=" + t.Environment
			if reg != nil {
				if c, ok := reg.Get(*t.Connector); ok {
					out[i].Service = c.Manifest.Name
					if spec, ok := c.Manifest.Triggers[*t.Trigger]; ok && spec.Poll != nil && t.Environment == "prod" {
						out[i].Polled = true
					}
				}
			}
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"triggers": nonNil(out), "remote_subscriptions": subs})
}

// listCallableWorkflows lists the business's workflows another workflow
// may use as a template step (decision 0048): those whose published
// version declares outputs, with their interface.
func (s *Server) listCallableWorkflows(w http.ResponseWriter, r *http.Request) {
	reg, err := s.Registry.For(r.Context(), principalFrom(r.Context()).TenantID.String())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	var out []runtime.CallableWorkflow
	err = s.tx(r, func(tx pgx.Tx) error {
		out, err = runtime.CallableWorkflows(r.Context(), tx, reg)
		return err
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"workflows": nonNil(out)})
}
