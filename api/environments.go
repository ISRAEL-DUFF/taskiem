package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"slices"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/ingest"
	"github.com/israel-duff/taskiem/engine/wd"
)

// Environments (spec 3.4, 15.1). Each environment runs its own deployed
// version of a workflow, with its own secrets, connections, variables and
// triggers. Publishing deploys to every ungated environment; a gated
// environment (say prod, gated on staging) takes only what was promoted
// from the environment it names, as the same immutable version.

var envNameRe = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,31}$`)

// deployTargets are the environments a publish deploys to: the ungated
// ones, and the Git environment syncing it.
func deployTargets(ctx context.Context, tx pgx.Tx, gitEnv string) ([]string, error) {
	rows, err := tx.Query(ctx, `SELECT name FROM environments WHERE promotion_from IS NULL OR name = $1 ORDER BY name`, gitEnv)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowTo[string])
}

// deployTx makes version v what env runs, and registers its triggers there.
func (s *Server) deployTx(ctx context.Context, tx pgx.Tx, tenant, wf uuid.UUID, env string, v int, d *wd.Definition, by *string, from string) error {
	if _, err := tx.Exec(ctx, `INSERT INTO deployments (tenant_id, workflow_id, environment, version, deployed_by, promoted_from) VALUES ($1, $2, $3, $4, $5, NULLIF($6, ''))
		ON CONFLICT (workflow_id, environment) DO UPDATE SET version = $4, deployed_by = $5, promoted_from = NULLIF($6, ''), deployed_at = now()`,
		tenant, wf, env, v, by, from); err != nil {
		return err
	}
	reg, err := s.Registry.For(ctx, tenant.String())
	if err != nil {
		return err
	}
	if err := ingest.SyncEnvironments(ctx, tx, tenant, wf, []string{env}, v, d, reg, time.Now()); err != nil {
		var pgErr interface{ SQLState() string }
		if errors.As(err, &pgErr) && pgErr.SQLState() == "23505" {
			return fmt.Errorf("%w: another workflow already uses this webhook path in %s", errConflict, env)
		}
		return err
	}
	return nil
}

type deployment struct {
	Environment  string    `json:"environment"`
	Version      int       `json:"version"`
	DeployedBy   *string   `json:"deployed_by"`
	PromotedFrom *string   `json:"promoted_from"`
	DeployedAt   time.Time `json:"deployed_at"`
}

func deploymentsOf(ctx context.Context, tx pgx.Tx, wf uuid.UUID) ([]deployment, error) {
	rows, err := tx.Query(ctx, `SELECT environment, version, deployed_by, promoted_from, deployed_at FROM deployments WHERE workflow_id = $1 ORDER BY environment`, wf)
	if err != nil {
		return nil, err
	}
	out, err := pgx.CollectRows(rows, pgx.RowToStructByPos[deployment])
	if out == nil {
		out = []deployment{}
	}
	return out, err
}

// deployedVersion is the version env runs, or 0.
func deployedVersion(ctx context.Context, tx pgx.Tx, wf uuid.UUID, env string) (int, error) {
	var v int
	err := tx.QueryRow(ctx, `SELECT version FROM deployments WHERE workflow_id = $1 AND environment = $2`, wf, env).Scan(&v)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, nil
	}
	return v, err
}

type environmentInfo struct {
	Name          string    `json:"name"`
	PromotionFrom *string   `json:"promotion_from"`
	Git           bool      `json:"git"`
	Workflows     int       `json:"workflows"`
	CreatedAt     time.Time `json:"created_at"`
}

func (s *Server) listEnvironments(w http.ResponseWriter, r *http.Request) {
	var out []environmentInfo
	err := s.tx(r, func(tx pgx.Tx) error {
		rows, err := tx.Query(r.Context(), `SELECT e.name, e.promotion_from,
			EXISTS (SELECT 1 FROM git_connections g WHERE g.environment = e.name),
			(SELECT count(*) FROM deployments d WHERE d.environment = e.name), e.created_at
			FROM environments e ORDER BY e.created_at, e.name`)
		if err != nil {
			return err
		}
		out, err = pgx.CollectRows(rows, pgx.RowToStructByPos[environmentInfo])
		return err
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"environments": out})
}

type environmentReq struct {
	Name          string `json:"name"`
	PromotionFrom string `json:"promotion_from"`
}

// checkPromotionFrom refuses an unknown source or a cycle.
func checkPromotionFrom(ctx context.Context, tx pgx.Tx, name, from string) error {
	seen := map[string]bool{name: true}
	for at := from; at != ""; {
		if seen[at] {
			return fmt.Errorf("%w: promotion would go round in a circle through %s", errBadRequest, at)
		}
		seen[at] = true
		var next *string
		if err := tx.QueryRow(ctx, `SELECT promotion_from FROM environments WHERE name = $1`, at).Scan(&next); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return fmt.Errorf("%w: no environment %q", errBadRequest, at)
			}
			return err
		}
		at = ""
		if next != nil {
			at = *next
		}
	}
	return nil
}

// createEnvironment adds an environment. An ungated one starts with every
// workflow's published version; a gated one starts empty, filled by
// promotion.
func (s *Server) createEnvironment(w http.ResponseWriter, r *http.Request) {
	var req environmentReq
	if err := decodeBody(r, &req); err != nil {
		s.fail(w, r, err)
		return
	}
	if !envNameRe.MatchString(req.Name) {
		s.fail(w, r, fmt.Errorf("%w: an environment name is up to 32 lowercase letters, digits, - or _", errBadRequest))
		return
	}
	p := principalFrom(r.Context())
	deployed := 0
	err := s.tx(r, func(tx pgx.Tx) error {
		ctx := r.Context()
		var exists bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM environments WHERE name = $1)`, req.Name).Scan(&exists); err != nil {
			return err
		}
		if exists {
			return fmt.Errorf("%w: %s exists", errConflict, req.Name)
		}
		if err := checkPromotionFrom(ctx, tx, req.Name, req.PromotionFrom); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO environments (tenant_id, name, promotion_from) VALUES ($1, $2, NULLIF($3, ''))`, p.TenantID, req.Name, req.PromotionFrom); err != nil {
			return err
		}
		if req.PromotionFrom == "" {
			rows, err := tx.Query(ctx, `SELECT w.id, w.active_version, v.definition FROM workflows w
				JOIN workflow_versions v ON v.workflow_id = w.id AND v.version = w.active_version`)
			if err != nil {
				return err
			}
			type active struct {
				ID      uuid.UUID
				Version int
				Def     []byte
			}
			list, err := pgx.CollectRows(rows, pgx.RowToStructByPos[active])
			if err != nil {
				return err
			}
			by := p.Actor()
			for _, a := range list {
				d, err := wd.Load(a.Def)
				if err != nil {
					return err
				}
				if err := s.deployTx(ctx, tx, p.TenantID, a.ID, req.Name, a.Version, d, &by, ""); err != nil {
					return err
				}
				deployed++
			}
		}
		return auditTx(r, tx, "environment.create", req.Name, map[string]any{"promotion_from": req.PromotionFrom, "deployed": deployed})
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"name": req.Name, "promotion_from": req.PromotionFrom, "deployed": deployed})
}

// putEnvironment gates an environment on another, or lifts the gate. What
// it runs does not change until the next publish or promotion. Adding a
// gate takes workflow.publish; lifting or moving one, which lets publishes
// reach the environment directly, takes an owner.
func (s *Server) putEnvironment(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "env")
	var req struct {
		PromotionFrom string `json:"promotion_from"`
	}
	if err := decodeBody(r, &req); err != nil {
		s.fail(w, r, err)
		return
	}
	p := principalFrom(r.Context())
	err := s.tx(r, func(tx pgx.Tx) error {
		ctx := r.Context()
		var before *string
		if err := tx.QueryRow(ctx, `SELECT promotion_from FROM environments WHERE name = $1 FOR UPDATE`, name).Scan(&before); err != nil {
			return err
		}
		if before != nil && *before != req.PromotionFrom && !slices.Contains(p.Roles, "owner") {
			return fmt.Errorf("%w: only an owner lifts or moves the gate on %s", errForbidden, name)
		}
		if err := checkPromotionFrom(ctx, tx, name, req.PromotionFrom); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE environments SET promotion_from = NULLIF($2, '') WHERE name = $1`, name, req.PromotionFrom); err != nil {
			return err
		}
		return auditTx(r, tx, "environment.update", name, map[string]any{"promotion_from": req.PromotionFrom, "before": before})
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"name": name, "promotion_from": req.PromotionFrom})
}

// promote copies the version running in one environment into the
// environment gated on it. With four-eyes publishing, the person promoting
// is neither the version's author nor whoever deployed it to the source.
func (s *Server) promote(w http.ResponseWriter, r *http.Request) {
	wf, err := uuid.Parse(chi.URLParam(r, "wf"))
	if err != nil {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	var req struct {
		From    string `json:"from"`
		To      string `json:"to"`
		Version int    `json:"version,omitempty"`
	}
	if err := decodeBody(r, &req); err != nil {
		s.fail(w, r, err)
		return
	}
	p := principalFrom(r.Context())
	if p.Environment != "" && p.Environment != req.To {
		s.fail(w, r, fmt.Errorf("%w: this key is limited to %s", errForbidden, p.Environment))
		return
	}
	var probs []problem
	var v int
	err = s.tx(r, func(tx pgx.Tx) error {
		ctx := r.Context()
		if err := tx.QueryRow(ctx, `SELECT 1 FROM workflows WHERE id = $1 FOR UPDATE`, wf).Scan(new(int)); err != nil {
			return err
		}
		var gate *string
		if err := tx.QueryRow(ctx, `SELECT promotion_from FROM environments WHERE name = $1`, req.To).Scan(&gate); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return fmt.Errorf("%w: no environment %q", errBadRequest, req.To)
			}
			return err
		}
		if gate == nil || *gate != req.From {
			return fmt.Errorf("%w: %s does not take promotions from %q", errBadRequest, req.To, req.From)
		}
		var gitLed bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM git_connections WHERE environment = $1 AND mode = 'git_led')`, req.To).Scan(&gitLed); err != nil {
			return err
		}
		if gitLed {
			return fmt.Errorf("%w: %s deploys from Git: merge to its branch instead", errConflict, req.To)
		}
		var deployedBy *string
		if err := tx.QueryRow(ctx, `SELECT version, taskiem_actor_human(deployed_by) FROM deployments WHERE workflow_id = $1 AND environment = $2`, wf, req.From).Scan(&v, &deployedBy); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return fmt.Errorf("%w: nothing is deployed in %s", errConflict, req.From)
			}
			return err
		}
		if req.Version != 0 && req.Version != v {
			return fmt.Errorf("%w: %s runs version %d, not %d: only what ran there is promoted", errConflict, req.From, v, req.Version)
		}
		if cur, err := deployedVersion(ctx, tx, wf, req.To); err != nil {
			return err
		} else if cur == v {
			return nil
		}
		g, err := governanceTx(ctx, tx)
		if err != nil {
			return err
		}
		var def []byte
		var author *string
		if err := tx.QueryRow(ctx, `SELECT definition, taskiem_actor_human(created_by) FROM workflow_versions WHERE workflow_id = $1 AND version = $2`, wf, v).Scan(&def, &author); err != nil {
			return err
		}
		if g.FourEyesPublish {
			if p.UserID == uuid.Nil {
				return fmt.Errorf("%w: with four-eyes publishing, a person promotes", errForbidden)
			}
			// Authors and deployers count as the people behind any API key.
			if me := p.UserID.String(); (author != nil && *author == me) || (deployedBy != nil && *deployedBy == me) {
				return fmt.Errorf("%w: promotion needs a second person: not the version's author, nor whoever deployed it to %s", errForbidden, req.From)
			}
		}
		if probs = s.check(ctx, p.TenantID, def); len(probs) > 0 {
			return errInvalid
		}
		if probs, err = missingPolicies(ctx, tx, def); err != nil || len(probs) > 0 {
			if err == nil {
				err = errInvalid
			}
			return err
		}
		d, err := wd.Load(def)
		if err != nil {
			return err
		}
		by := p.Actor()
		if err := s.deployTx(ctx, tx, p.TenantID, wf, req.To, v, d, &by, req.From); err != nil {
			return err
		}
		return auditTx(r, tx, "workflow.promote", fmt.Sprintf("%s/%d", wf, v), map[string]any{"from": req.From, "to": req.To})
	})
	if errors.Is(err, errInvalid) {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": "definition is not valid", "problems": probs})
		return
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": wf, "version": v, "environment": req.To})
}
