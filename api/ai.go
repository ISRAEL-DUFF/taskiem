package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/ai"
	"github.com/israel-duff/taskiem/engine/ai/builder"
	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/pii"
	"github.com/israel-duff/taskiem/engine/policy"
	"github.com/israel-duff/taskiem/engine/runtime"
)

// AI workflow building (spec 12.1, docs/ai.md).
//
// The model proposes and people dispose. A build runs in the background
// with no principal at all: it reads the tenant's context through the
// queries below (names and summaries, never a secret or a variable's
// value), asks the model through engine/ai (which cannot reach the vault,
// the database or a provider), and stores the proposal. Saving it is a
// separate request by a person with workflow.edit, and creates a draft
// version only. Nothing under /v1/ai publishes, approves, starts a run or
// reads a secret; TestAIRoutes holds the route list to that.

// AISettings configure AI building; a nil Server.AI turns it off.
type AISettings struct {
	Provider  ai.Provider
	Effort    string // default high
	MaxTokens int    // default ai.DefaultMaxTokens
}

// aiRoutes is every route under /v1/ai, with the one permission each
// needs. TestAIRoutes compares the router against it.
func (s *Server) aiRoutes(r chi.Router) {
	r.With(s.need(PermWorkflowRead)).Get("/status", s.aiStatus)
	r.With(s.need(PermWorkflowEdit)).Post("/build", s.aiBuild)
	r.With(s.need(PermWorkflowEdit)).Get("/builds/{id}", s.aiGetBuild)
	r.With(s.need(PermWorkflowEdit)).Post("/builds/{id}/save", s.aiSave)
	r.With(s.need(PermAuditRead)).Get("/builds/{id}/interactions", s.aiInteractions)
}

const aiGoalMax = 4000

func (s *Server) aiDisabled(w http.ResponseWriter) {
	writeJSON(w, http.StatusServiceUnavailable, map[string]string{"code": "ai_disabled",
		"error": "AI building is not configured on this deployment; build the workflow on the canvas instead"})
}

// aiBudget returns the tenant's monthly token budget (0: none) and use.
func (s *Server) aiBudget(ctx context.Context, tenant uuid.UUID) (limit, used int64, err error) {
	lim, err := s.Store.LimitsFor(ctx, tenant)
	if err != nil {
		return 0, 0, err
	}
	err = db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		used, err = runtime.AITokensThisMonth(ctx, tx, tenant)
		return err
	})
	return lim.AIMonthlyTokens, used, err
}

func budgetExhausted(w http.ResponseWriter) {
	writeJSON(w, http.StatusTooManyRequests, map[string]string{"code": "ai_budget_exhausted", "limit": "ai_monthly_tokens",
		"error": "this month's AI budget is used up; build the workflow on the canvas instead (runs are not affected)"})
}

func (s *Server) aiStatus(w http.ResponseWriter, r *http.Request) {
	out := map[string]any{"enabled": s.AI != nil && s.AI.Provider != nil}
	if s.AI != nil && s.AI.Provider != nil {
		limit, used, err := s.aiBudget(r.Context(), principalFrom(r.Context()).TenantID)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		out["provider"], out["model"] = s.AI.Provider.Name(), s.AI.Provider.Model()
		out["budget"] = map[string]int64{"monthly_tokens": limit, "used_tokens": used}
	}
	writeJSON(w, http.StatusOK, out)
}

type aiBuildReq struct {
	Goal        string     `json:"goal"`
	Workflow    *uuid.UUID `json:"workflow,omitempty"`
	Environment string     `json:"environment,omitempty"`
}

func (s *Server) aiBuild(w http.ResponseWriter, r *http.Request) {
	if s.AI == nil || s.AI.Provider == nil {
		s.aiDisabled(w)
		return
	}
	p := principalFrom(r.Context())
	if !s.limiter("ai-build:"+p.TenantID.String(), 20*time.Second, 5).Allow() {
		w.Header().Set("Retry-After", "20")
		writeJSON(w, http.StatusTooManyRequests, map[string]string{"code": "rate_limited", "error": "too many AI builds at once; try again shortly"})
		return
	}
	var req aiBuildReq
	if err := decodeBody(r, &req); err != nil {
		s.fail(w, r, err)
		return
	}
	req.Goal = strings.TrimSpace(req.Goal)
	if req.Goal == "" || len(req.Goal) > aiGoalMax {
		s.fail(w, r, fmt.Errorf("%w: goal is required, at most %d characters", errBadRequest, aiGoalMax))
		return
	}
	ctx := r.Context()
	limit, used, err := s.aiBudget(ctx, p.TenantID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if limit > 0 && used >= limit {
		s.Store.LimitHit(ctx, p.TenantID, "ai_monthly_tokens")
		budgetExhausted(w)
		return
	}
	breq := builder.Request{Goal: req.Goal, Environment: req.Environment}
	id := uuid.Must(uuid.NewV7())
	err = s.tx(r, func(tx pgx.Tx) error {
		if req.Workflow != nil {
			if err := tx.QueryRow(ctx, `SELECT definition FROM workflow_versions WHERE workflow_id = $1 ORDER BY version DESC LIMIT 1`, *req.Workflow).Scan(&breq.Base); err != nil {
				return err
			}
		}
		var err error
		if breq.Context, err = aiContext(ctx, tx, req.Environment, req.Workflow); err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `INSERT INTO ai_builds (id, tenant_id, actor, goal, workflow_id, environment, provider, model)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`, id, p.TenantID, p.Actor(), pii.Redact(req.Goal), req.Workflow, req.Environment,
			s.AI.Provider.Name(), s.AI.Provider.Model())
		return err
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	who := aiActor{tenant: p.TenantID, actor: p.Actor(), actorType: p.ActorType(), ip: clientIP(r)}
	s.bg.Add(1)
	go func() {
		defer s.bg.Done()
		s.runBuild(id, who, breq)
	}()
	writeJSON(w, http.StatusAccepted, map[string]any{"id": id, "status": "running"})
}

// aiActor is who asked for a build, carried into the background.
type aiActor struct {
	tenant           uuid.UUID
	actor, actorType string
	ip               string
}

// aiBuildTimeout bounds a build: drafting and up to three corrections.
const aiBuildTimeout = 15 * time.Minute

func (s *Server) runBuild(id uuid.UUID, who aiActor, req builder.Request) {
	ctx, cancel := context.WithTimeout(context.Background(), aiBuildTimeout)
	defer cancel()
	if s.Done != nil {
		go func() {
			select {
			case <-s.Done:
				cancel()
			case <-ctx.Done():
			}
		}()
	}
	inTx := func(fn func(pgx.Tx) error) error {
		return db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{who.tenant}, fn)
	}
	reg, err := s.Registry.For(ctx, who.tenant.String())
	if err != nil {
		s.finishBuild(ctx, id, who, nil, err)
		return
	}
	b := &builder.Builder{
		Provider:   s.AI.Provider,
		Connectors: reg,
		Validate: func(doc []byte) []builder.Problem {
			var out []builder.Problem
			for _, p := range s.check(ctx, who.tenant, doc) {
				out = append(out, builder.Problem{Path: p.Path, Message: p.Message})
			}
			return out
		},
		Meter:     aiMeter{s: s, tenant: who.tenant},
		Recorder:  aiRecorder{s: s, build: id, who: who, provider: s.AI.Provider.Name()},
		Effort:    s.AI.Effort,
		MaxTokens: s.AI.MaxTokens,
		Progress: func(stage string) {
			_ = inTx(func(tx pgx.Tx) error {
				_, err := tx.Exec(ctx, `UPDATE ai_builds SET stage = $2 WHERE id = $1`, id, stage)
				return err
			})
		},
	}
	prop, err := b.Build(ctx, req)
	s.finishBuild(ctx, id, who, prop, err)
}

// finishBuild stores the outcome and audits the proposal.
func (s *Server) finishBuild(ctx context.Context, id uuid.UUID, who aiActor, prop *builder.Proposal, buildErr error) {
	ctx = context.WithoutCancel(ctx)
	status, msg := "proposed", ""
	switch {
	case errors.Is(buildErr, ai.ErrBudgetExhausted):
		status, msg = "failed", "this month's AI budget is used up; build the workflow on the canvas instead"
		s.Store.LimitHit(ctx, who.tenant, "ai_monthly_tokens")
	case errors.Is(buildErr, builder.ErrRefused):
		status, msg = "failed", "the model declined this request; rephrase the goal or build the workflow on the canvas"
	case buildErr != nil:
		status, msg = "failed", "the AI build failed; try again or build the workflow on the canvas"
		s.Logger.Error("ai build failed", "build", id, "err", buildErr)
	}
	var raw []byte
	detail := map[string]any{"build": id.String(), "status": status, "ip": who.ip, "provider": s.AI.Provider.Name()}
	rounds, total := 0, int64(0)
	var valid *bool
	if prop != nil {
		raw, _ = json.Marshal(prop)
		rounds, total = prop.Rounds, prop.Usage.Total()
		v := prop.Valid()
		valid = &v
		detail["model"] = prop.Model
		detail["co_author"] = "ai:" + prop.Model
		detail["rounds"] = prop.Rounds
		detail["valid"] = v
		detail["valid_first_try"] = prop.ValidFirstTry
		detail["tokens"] = total
		detail["policy_violations"] = prop.PolicyViolations()
		detail["tests_passed"] = prop.TestsPassed()
		detail["connectors"] = prop.Connectors
	}
	audit, _ := json.Marshal(detail)
	err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{who.tenant}, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `UPDATE ai_builds SET status = $2, stage = 'done', error = NULLIF($3, ''), proposal = $4, rounds = $5, valid = $6,
			total_tokens = $7, model = COALESCE(NULLIF($8, ''), model), finished_at = now() WHERE id = $1`,
			id, status, msg, raw, rounds, valid, total, modelOf(prop)); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `SELECT taskiem_audit_append($1, $2, $3, $4, $5, $6)`, who.tenant, who.actorType, who.actor, "ai.propose", id.String(), audit)
		return err
	})
	if err != nil {
		s.Logger.Error("recording an ai build", "build", id, "err", err)
	}
}

func modelOf(p *builder.Proposal) string {
	if p == nil {
		return ""
	}
	return p.Model
}

// aiMeter refuses a model call once the tenant's monthly budget is spent.
type aiMeter struct {
	s      *Server
	tenant uuid.UUID
}

func (m aiMeter) Allow(ctx context.Context) error {
	limit, used, err := m.s.aiBudget(ctx, m.tenant)
	if err != nil {
		return err
	}
	if limit > 0 && used >= limit {
		return ai.ErrBudgetExhausted
	}
	return nil
}

// aiRecorder writes each model call to ai_interactions.
type aiRecorder struct {
	s        *Server
	build    uuid.UUID
	who      aiActor
	provider string
}

func (rec aiRecorder) Record(ctx context.Context, it builder.Interaction) error {
	msgs, err := json.Marshal(it.Request.Messages)
	if err != nil {
		return err
	}
	var resp *string
	var stop, model string
	var u ai.Usage
	if it.Response != nil {
		r := pii.Redact(it.Response.Text)
		resp, stop, model, u = &r, it.Response.StopReason, it.Response.Model, it.Response.Usage
	}
	if model == "" {
		model = rec.s.AI.Provider.Model()
	}
	return db.InTenantTx(context.WithoutCancel(ctx), rec.s.Store.Pool, []uuid.UUID{rec.who.tenant}, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO ai_interactions (id, tenant_id, build_id, round, kind, actor, provider, model, system_digest, messages,
			response, stop_reason, outcome, error, input_tokens, output_tokens, cache_creation_tokens, cache_read_tokens, total_tokens)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, NULLIF($12, ''), $13, NULLIF($14, ''), $15, $16, $17, $18, $19)`,
			uuid.Must(uuid.NewV7()), rec.who.tenant, rec.build, it.Round, it.Kind, rec.who.actor, rec.provider, model, it.SystemDigest, msgs,
			resp, stop, it.Outcome, pii.Redact(it.Err), u.InputTokens, u.OutputTokens, u.CacheCreationTokens, u.CacheReadTokens, u.Total())
		return err
	})
}

// aiContext reads what a prompt may carry: connections by name and
// connector, variables by name, active policies by name and summary, and
// workflows' latest definitions (the builder keeps only their structure).
// It never selects secrets, credentials (secret_ref) or variable values.
func aiContext(ctx context.Context, tx pgx.Tx, env string, skip *uuid.UUID) (builder.Context, error) {
	var c builder.Context
	rows, err := tx.Query(ctx, `SELECT environment, connector, name, status FROM connections
		WHERE ($1 = '' OR environment = $1) ORDER BY environment, connector, name LIMIT 200`, env)
	if err != nil {
		return c, err
	}
	c.Connections, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (builder.Connection, error) {
		var x builder.Connection
		return x, r.Scan(&x.Environment, &x.Connector, &x.Name, &x.Status)
	})
	if err != nil {
		return c, err
	}
	rows, err = tx.Query(ctx, `SELECT environment, name FROM variables WHERE ($1 = '' OR environment = $1) ORDER BY environment, name LIMIT 500`, env)
	if err != nil {
		return c, err
	}
	c.Variables, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (builder.Variable, error) {
		var x builder.Variable
		return x, r.Scan(&x.Environment, &x.Name)
	})
	if err != nil {
		return c, err
	}
	rows, err = tx.Query(ctx, `SELECT name, document FROM approval_policies WHERE state = 'active' ORDER BY name LIMIT 100`)
	if err != nil {
		return c, err
	}
	c.Policies, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (builder.Policy, error) {
		var x builder.Policy
		err := r.Scan(&x.Name, &x.Document)
		x.Summary = policySummary(x.Document)
		return x, err
	})
	if err != nil {
		return c, err
	}
	rows, err = tx.Query(ctx, `SELECT w.name, v.definition FROM workflows w
		JOIN LATERAL (SELECT definition FROM workflow_versions WHERE workflow_id = w.id ORDER BY version DESC LIMIT 1) v ON true
		WHERE $1::uuid IS NULL OR w.id <> $1 ORDER BY w.created_at DESC LIMIT 100`, skip)
	if err != nil {
		return c, err
	}
	c.Workflows, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (builder.ExistingWorkflow, error) {
		var x builder.ExistingWorkflow
		return x, r.Scan(&x.Name, &x.Definition)
	})
	return c, err
}

// policySummary describes who approves under a policy, without its
// conditions' constants.
func policySummary(doc []byte) string {
	pol, err := policy.Parse(doc)
	if err != nil {
		return "unreadable policy"
	}
	if pol.Description != "" {
		return pii.Redact(pol.Description)
	}
	var parts []string
	for i, r := range pol.Rules {
		var lv []string
		for _, l := range r.Levels {
			n := max(l.Count, 1)
			lv = append(lv, fmt.Sprintf("%d %s", n, l.Role))
		}
		s := fmt.Sprintf("rule %d: %s", i+1, strings.Join(lv, " then "))
		if r.When != "" {
			s += " (conditional)"
		}
		if r.StepUp != "" {
			s += ", step-up " + r.StepUp
		}
		parts = append(parts, s)
	}
	return strings.Join(parts, "; ")
}

type aiBuildView struct {
	ID              uuid.UUID       `json:"id"`
	Status          string          `json:"status"`
	Stage           string          `json:"stage"`
	Error           *string         `json:"error"`
	Goal            string          `json:"goal"`
	Workflow        *uuid.UUID      `json:"workflow"`
	Environment     string          `json:"environment"`
	Provider        string          `json:"provider"`
	Model           string          `json:"model"`
	Proposal        json.RawMessage `json:"proposal"`
	CreatedBy       string          `json:"created_by"`
	CreatedAt       time.Time       `json:"created_at"`
	FinishedAt      *time.Time      `json:"finished_at"`
	SavedWorkflowID *uuid.UUID      `json:"saved_workflow_id"`
	SavedVersion    *int            `json:"saved_version"`
}

func (s *Server) aiGetBuild(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	var v aiBuildView
	var prop []byte
	err = s.tx(r, func(tx pgx.Tx) error {
		return tx.QueryRow(r.Context(), `SELECT id, status, stage, error, goal, workflow_id, environment, provider, model, proposal, actor, created_at,
			finished_at, saved_workflow_id, saved_version FROM ai_builds WHERE id = $1`, id).
			Scan(&v.ID, &v.Status, &v.Stage, &v.Error, &v.Goal, &v.Workflow, &v.Environment, &v.Provider, &v.Model, &prop, &v.CreatedBy,
				&v.CreatedAt, &v.FinishedAt, &v.SavedWorkflowID, &v.SavedVersion)
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if prop != nil {
		v.Proposal = prop
	} else {
		v.Proposal = json.RawMessage("null")
	}
	writeJSON(w, http.StatusOK, v)
}

func (s *Server) aiInteractions(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	type row struct {
		Round        int             `json:"round"`
		Kind         string          `json:"kind"`
		Actor        string          `json:"actor"`
		Provider     string          `json:"provider"`
		Model        string          `json:"model"`
		SystemDigest string          `json:"system_digest"`
		Messages     json.RawMessage `json:"messages"`
		Response     *string         `json:"response"`
		StopReason   *string         `json:"stop_reason"`
		Outcome      string          `json:"outcome"`
		Error        *string         `json:"error"`
		Usage        ai.Usage        `json:"usage"`
		CreatedAt    time.Time       `json:"created_at"`
	}
	var out []row
	err = s.tx(r, func(tx pgx.Tx) error {
		rows, err := tx.Query(r.Context(), `SELECT round, kind, actor, provider, model, system_digest, messages, response, stop_reason, outcome, error,
			input_tokens, output_tokens, cache_creation_tokens, cache_read_tokens, created_at FROM ai_interactions WHERE build_id = $1 ORDER BY created_at`, id)
		if err != nil {
			return err
		}
		out, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (row, error) {
			var x row
			return x, r.Scan(&x.Round, &x.Kind, &x.Actor, &x.Provider, &x.Model, &x.SystemDigest, &x.Messages, &x.Response, &x.StopReason, &x.Outcome,
				&x.Error, &x.Usage.InputTokens, &x.Usage.OutputTokens, &x.Usage.CacheCreationTokens, &x.Usage.CacheReadTokens, &x.CreatedAt)
		})
		return err
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"interactions": nonNil(out)})
}

// aiSave saves a proposal as a draft version, by the person asking: a new
// workflow, or a new version of the workflow the build modified. It never
// publishes; publishing goes through the normal flow and its four-eyes
// rules, where the person who saved counts as the author.
func (s *Server) aiSave(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	var req struct {
		Name string `json:"name,omitempty"`
	}
	if r.ContentLength != 0 {
		if err := decodeBody(r, &req); err != nil {
			s.fail(w, r, err)
			return
		}
	}
	p := principalFrom(r.Context())
	var wf uuid.UUID
	var version int
	var doc []byte
	err = s.tx(r, func(tx pgx.Tx) error {
		ctx := r.Context()
		var status, model string
		var target *uuid.UUID
		var prop []byte
		if err := tx.QueryRow(ctx, `SELECT status, workflow_id, proposal, model FROM ai_builds WHERE id = $1 FOR UPDATE`, id).Scan(&status, &target, &prop, &model); err != nil {
			return err
		}
		if status != "proposed" {
			return fmt.Errorf("%w: build is %s, not a proposal to save", errConflict, status)
		}
		var pr struct {
			Definition json.RawMessage `json:"definition"`
		}
		if err := json.Unmarshal(prop, &pr); err != nil || len(pr.Definition) == 0 {
			return fmt.Errorf("%w: the proposal has no definition to save", errConflict)
		}
		var err error
		if doc, err = canonical(pr.Definition); err != nil {
			return err
		}
		if target != nil {
			wf = *target
			if err := tx.QueryRow(ctx, `SELECT 1 FROM workflows WHERE id = $1 FOR UPDATE`, wf).Scan(new(int)); err != nil {
				return err
			}
			if err := s.refuseGitManaged(ctx, tx, wf); err != nil {
				return err
			}
			if err := tx.QueryRow(ctx, `SELECT COALESCE(max(version), 0) + 1 FROM workflow_versions WHERE workflow_id = $1`, wf).Scan(&version); err != nil {
				return err
			}
		} else {
			name := strings.TrimSpace(req.Name)
			if name == "" {
				var d struct {
					Name string `json:"name"`
				}
				_ = json.Unmarshal(doc, &d)
				name = d.Name
			}
			if name == "" {
				return fmt.Errorf("%w: name is required", errBadRequest)
			}
			if err := s.checkCount(ctx, tx, p.TenantID, "max_workflows"); err != nil {
				return err
			}
			wf, version = uuid.Must(uuid.NewV7()), 1
			if _, err := tx.Exec(ctx, `INSERT INTO workflows (id, tenant_id, name, created_by, workspace_id)
				VALUES ($1, $2, $3, $4, (SELECT id FROM workspaces WHERE tenant_id = $2 ORDER BY created_at LIMIT 1))`, wf, p.TenantID, name, p.id()); err != nil {
				return err
			}
			if err := auditTx(r, tx, "workflow.create", wf.String(), map[string]any{"name": name, "ai_build": id.String()}); err != nil {
				return err
			}
		}
		if err := insertVersionTx(ctx, tx, p.TenantID, wf, version, doc, nil, p.Actor(), ""); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE workflow_versions SET ai_build_id = $3 WHERE workflow_id = $1 AND version = $2`, wf, version, id); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE ai_builds SET status = 'saved', saved_workflow_id = $2, saved_version = $3, saved_at = now() WHERE id = $1`, id, wf, version); err != nil {
			return err
		}
		return auditTx(r, tx, "ai.save", fmt.Sprintf("%s/%d", wf, version), map[string]any{"build": id.String(), "ai_assisted": true,
			"co_author": "ai:" + model, "state": "draft"})
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"id": wf, "version": version, "state": "draft", "ai_assisted": true,
		"problems": nonNil(s.checkFor(r, doc))})
}
