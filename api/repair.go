package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/ai"
	"github.com/israel-duff/taskiem/engine/ai/builder"
	"github.com/israel-duff/taskiem/engine/ai/repair"
	"github.com/israel-duff/taskiem/engine/connector"
	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/history"
	"github.com/israel-duff/taskiem/engine/pii"
	"github.com/israel-duff/taskiem/engine/runtime"
	"github.com/israel-duff/taskiem/engine/shadow"
	"github.com/israel-duff/taskiem/engine/wd"
	"github.com/israel-duff/taskiem/engine/wddiff"
	"github.com/israel-duff/taskiem/engine/wdtest"
)

// Self-repair (spec 12.2, docs/ai.md#repairing-failed-runs, decision 0016).
//
// A run that fails, parks a step, or meets a provider's changed response
// queues a repair job (by trigger, in the transaction that recorded it).
// RunRepairs works the queue in the background: it opens the run's history
// under the tenant's scope, classifies the failure (rules first, the model
// only when they are unsure), and for a failure the workflow itself causes
// asks the model for a patch, which must pass the publishing checks, a
// shadow run over the run's recorded data with every write mocked, the
// regression test for the failing case and the workflow's existing tests.
// The model sees a redacted view only; what the shadow reports back to it
// is sanitised against every personal value the history held.
//
// The repair service never publishes, approves or resumes on its own:
// accepting a proposal is a person's request, under their permissions and
// through the normal publish path (four-eyes and promotion gates apply).

// repairRoutes are the repair API's routes.
func (s *Server) repairRoutes(r chi.Router) {
	r.With(s.need(PermRunRead)).Get("/runs/{run}/repairs", s.listRunRepairs)
	r.With(s.need(PermWorkflowRead)).Get("/workflows/{wf}/repairs", s.listWorkflowRepairs)
	r.With(s.need(PermWorkflowRead)).Get("/repairs/settings", s.getRepairSettings)
	r.With(s.need(PermPolicyManage)).Put("/repairs/settings", s.putRepairSettings)
	r.With(s.need(PermRunRead)).Get("/repairs/{id}", s.getRepair)
	r.With(s.need(PermAuditRead)).Get("/repairs/{id}/interactions", s.repairInteractions)
	r.With(s.need(PermRunResolve)).Post("/repairs/{id}/accept", s.acceptRepair)
	r.With(s.need(PermRunResolve)).Post("/repairs/{id}/dismiss", s.dismissRepair)
}

// repairActor is who the system acts as in the audit chain, and whom
// repair drafts are created by.
const (
	repairActor     = "ai-repair"
	repairCreatedBy = "system:ai-repair"
)

// --- the background loop ---

// RunRepairs works the repair queue until ctx ends. It runs only where a
// model provider is configured.
func (s *Server) RunRepairs(ctx context.Context, every time.Duration) {
	if every <= 0 {
		every = 5 * time.Second
	}
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		if _, err := s.RepairOnce(ctx); err != nil && ctx.Err() == nil {
			s.Logger.Error("repair queue", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

type repairJob struct {
	id, tenant, run uuid.UUID
	reason          string
	attempts        int
}

// RepairOnce analyses every repair job due now, one tenant at a time, and
// reports how many it finished.
func (s *Server) RepairOnce(ctx context.Context) (int, error) {
	rows, err := s.Store.Pool.Query(ctx, `SELECT tenant_id FROM taskiem_repair_due_tenants(50)`)
	if err != nil {
		return 0, err
	}
	tenants, err := pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
	if err != nil {
		return 0, err
	}
	n := 0
	for _, tenant := range tenants {
		for ctx.Err() == nil {
			job, ok, err := s.claimRepairJob(ctx, tenant)
			if err != nil {
				return n, err
			}
			if !ok {
				break
			}
			status, detail := s.processRepairJob(ctx, job)
			if err := s.finishRepairJob(ctx, job, status, detail); err != nil {
				return n, err
			}
			n++
		}
	}
	return n, nil
}

func (s *Server) claimRepairJob(ctx context.Context, tenant uuid.UUID) (repairJob, bool, error) {
	j := repairJob{tenant: tenant}
	err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `UPDATE repair_jobs SET status = 'running', attempts = attempts + 1, started_at = now()
			WHERE id = (SELECT id FROM repair_jobs WHERE (status = 'queued' AND available_at <= now())
			   OR (status = 'running' AND started_at < now() - interval '30 minutes' AND attempts < 3)
			 ORDER BY available_at LIMIT 1 FOR UPDATE SKIP LOCKED)
			RETURNING id, run_id, reason, attempts`).Scan(&j.id, &j.run, &j.reason, &j.attempts)
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return j, false, nil
	}
	return j, err == nil, err
}

func (s *Server) finishRepairJob(ctx context.Context, j repairJob, status, detail string) error {
	ctx = context.WithoutCancel(ctx)
	return db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{j.tenant}, func(tx pgx.Tx) error {
		if status == "requeue" {
			_, err := tx.Exec(ctx, `UPDATE repair_jobs SET status = 'queued', available_at = now() + interval '30 seconds', detail = $2 WHERE id = $1`, j.id, detail)
			return err
		}
		_, err := tx.Exec(ctx, `UPDATE repair_jobs SET status = $2, detail = NULLIF($3, ''), finished_at = now() WHERE id = $1`, j.id, status, detail)
		return err
	})
}

// repairEnabled reports whether a tenant has repair on.
func (s *Server) repairEnabled(ctx context.Context, tenant uuid.UUID) (bool, error) {
	enabled := true
	err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		err := tx.QueryRow(ctx, `SELECT enabled FROM ai_repair_settings WHERE tenant_id = $1`, tenant).Scan(&enabled)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		return err
	})
	return enabled, err
}

// repairTimeout bounds one analysis.
const repairTimeout = 15 * time.Minute

// processRepairJob analyses one job and reports the job's final status
// (done, skipped, failed, requeue) with a short detail.
func (s *Server) processRepairJob(ctx context.Context, j repairJob) (string, string) {
	ctx, cancel := context.WithTimeout(ctx, repairTimeout)
	defer cancel()
	if s.AI == nil || s.AI.Provider == nil {
		return "skipped", "no model provider is configured"
	}
	on, err := s.repairEnabled(ctx, j.tenant)
	if err != nil {
		return "failed", err.Error()
	}
	if !on {
		return "skipped", "repair is off for this tenant"
	}
	ref := runtime.RunRef{ID: j.run, TenantID: j.tenant}
	var wf uuid.UUID
	var version int
	var env, status string
	var open bool
	err = db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{j.tenant}, func(tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `SELECT workflow_id, version, environment, status FROM runs WHERE id = $1`, j.run).Scan(&wf, &version, &env, &status); err != nil {
			return err
		}
		// An analysis a stopped process left unfinished no longer counts.
		if _, err := tx.Exec(ctx, `UPDATE repair_proposals SET status = 'failed', error = 'the analysis was interrupted', finished_at = now()
			WHERE run_id = $1 AND status = 'analysing' AND created_at < now() - $2::interval`, j.run, (repairTimeout + time.Minute).String()); err != nil {
			return err
		}
		return tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM repair_proposals WHERE run_id = $1
			AND status IN ('analysing', 'proposed', 'action', 'awaiting_publish', 'awaiting_promotion'))`, j.run).Scan(&open)
	})
	if err != nil {
		return "failed", err.Error()
	}
	switch {
	case open:
		return "skipped", "the run already has an open proposal"
	case status == "queued" || status == "running" || status == "waiting":
		if j.reason == "drift" && j.attempts < 120 {
			return "requeue", "waiting for the run to settle"
		}
		return "skipped", "the run is " + status
	case status == "cancelled":
		return "skipped", "the run was cancelled"
	case status == "completed" && j.reason != "drift":
		return "skipped", "the run completed"
	}

	reg, err := s.Registry.For(ctx, j.tenant.String())
	if err != nil {
		return "failed", err.Error()
	}
	var doc []byte
	if err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{j.tenant}, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT definition FROM workflow_versions WHERE workflow_id = $1 AND version = $2`, wf, version).Scan(&doc)
	}); err != nil {
		return "failed", err.Error()
	}
	def, err := wd.Load(doc)
	if err != nil {
		return "failed", "the failed version does not load: " + err.Error()
	}
	// The run, twice: opened (for the shadow run, on this side only) and
	// sealed (for everything the model or a stored test may see).
	opened, taint, err := s.Store.OpenedHistoryWithTaint(ctx, ref)
	if err != nil {
		return "failed", err.Error()
	}
	sealed, err := s.Store.RunHistory(ctx, ref)
	if err != nil {
		return "failed", err.Error()
	}
	rec, err := shadow.Record(opened)
	if err != nil {
		return "failed", err.Error()
	}
	srec, err := shadow.Record(sealed)
	if err != nil {
		return "failed", err.Error()
	}
	red := srec.Redacted(def, reg)
	scrub := scrubber(taint)

	failure := s.repairFailure(ctx, j, status, def, reg, red, scrub)
	cl := repair.Classify(failure, def)

	if cl.Class.Patches() || !cl.Certain {
		limit, used, err := s.aiBudget(ctx, j.tenant)
		if err != nil {
			return "failed", err.Error()
		}
		if limit > 0 && used >= limit {
			s.Store.LimitHit(ctx, j.tenant, "ai_monthly_tokens")
			return "skipped", "this month's AI budget is used up"
		}
	}

	id := uuid.Must(uuid.NewV7())
	if err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{j.tenant}, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO repair_proposals (id, tenant_id, run_id, job_id, workflow_id, version, environment, reason, class, classified_by, step_id, provider, model)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, 'rules', NULLIF($10, ''), $11, $12)`,
			id, j.tenant, j.run, j.id, wf, version, env, j.reason, cl.Class, failure.Step, s.AI.Provider.Name(), s.AI.Provider.Model())
		return err
	}); err != nil {
		return "failed", err.Error()
	}

	ev := &repairEvidence{Classification: cl}
	var tests []wdtest.Case
	existing, err := s.workflowTests(ctx, j.tenant, wf)
	if err != nil {
		return "failed", err.Error()
	}
	check := func(patched []byte, modelTest *wdtest.Case) repair.Check {
		return s.shadowCheck(def, patched, reg, rec, red, existing, modelTest, scrub, ev, &tests, j.run)
	}
	r := &repair.Repairer{
		Provider:   s.AI.Provider,
		Connectors: reg,
		Validate: func(d []byte) []builder.Problem {
			var out []builder.Problem
			for _, p := range s.check(ctx, j.tenant, d) {
				out = append(out, builder.Problem{Path: p.Path, Message: p.Message})
			}
			return out
		},
		Meter:     aiMeter{s: s, tenant: j.tenant},
		Recorder:  repairRecorder{s: s, repair: id, tenant: j.tenant, provider: s.AI.Provider.Name()},
		Effort:    s.AI.Effort,
		MaxTokens: s.AI.MaxTokens,
	}
	res, rerr := r.Repair(ctx, repair.Request{
		Failure: failure, Classification: cl, Definition: doc, Run: scrub(modelView(red)), Check: check,
	})
	if res == nil {
		res = &repair.Result{Class: cl.Class, ClassifiedBy: "rules"}
	}
	ev.Attempts, ev.Problems, ev.Feedback = res.Attempts, res.Problems, res.Feedback
	p := proposalOutcome{id: id, tenant: j.tenant, res: res, ev: ev}
	switch {
	case errors.Is(rerr, ai.ErrBudgetExhausted):
		p.status, p.errMsg = "withheld", "this month's AI budget ran out before a fix was proven"
		s.Store.LimitHit(ctx, j.tenant, "ai_monthly_tokens")
	case rerr != nil && !errors.Is(rerr, repair.ErrNoModel):
		s.Logger.Error("repair failed", "repair", id, "err", rerr)
		p.status, p.errMsg = "failed", "the repair could not be analysed; check the run by hand"
	case !res.Class.Patches():
		p.status = "action"
		p.action = s.repairAction(ctx, ref, res.Class, failure, def, scrub)
	case res.Passed:
		p.status = "proposed"
		p.definition = res.Definition
		p.diff = wddiff.Diff(doc, res.Definition)
		if len(tests) > 0 {
			p.test = tests
		}
	default:
		p.status = "withheld"
		if res.Definition != nil {
			p.diff = wddiff.Diff(doc, res.Definition)
		}
	}
	if err := s.storeProposal(ctx, p); err != nil {
		s.Logger.Error("recording a repair", "repair", id, "err", err)
		return "failed", err.Error()
	}
	return "done", p.status
}

// repairEvidence is what a proposal shows besides its diff.
type repairEvidence struct {
	Classification repair.Classification `json:"classification"`
	// Shadow is the patched workflow's run over the recorded data, with
	// every task mocked (completed steps answered from the recording).
	Shadow    *shadow.Report `json:"shadow,omitempty"`
	Resumable bool           `json:"resumable"`
	// Regression is the test for the failing case, built from redacted
	// data; ReproducesFailure says it fails on the failed version.
	Regression        *testOutcome      `json:"regression,omitempty"`
	ReproducesFailure bool              `json:"reproduces_failure"`
	ModelTest         *testOutcome      `json:"model_test,omitempty"`
	Existing          []testOutcome     `json:"existing_tests"`
	Attempts          int               `json:"attempts"`
	Problems          []builder.Problem `json:"problems,omitempty"`
	Feedback          []string          `json:"feedback,omitempty"`
}

type testOutcome struct {
	Name     string   `json:"name"`
	Passed   bool     `json:"passed"`
	Status   string   `json:"status"`
	Failures []string `json:"failures,omitempty"`
}

func outcome(res wdtest.Result, scrub func(any) any) testOutcome {
	fails, _ := scrub(res.Failures).([]any)
	o := testOutcome{Name: res.Case, Passed: res.Passed(), Status: res.Status}
	for _, f := range fails {
		if s, ok := f.(string); ok {
			o.Failures = append(o.Failures, truncateText(s, 300))
		}
	}
	return o
}

// shadowCheck runs a candidate patch: the shadow run over the recorded
// data, the regression test (redacted data), the model's own test and the
// workflow's stored tests. It fills ev and returns what the model may be
// told.
func (s *Server) shadowCheck(orig *wd.Definition, patched []byte, reg connector.Lookup, rec, red *shadow.Recording, existing []storedTest,
	modelTest *wdtest.Case, scrub func(any) any, ev *repairEvidence, tests *[]wdtest.Case, run uuid.UUID) repair.Check {
	def, err := wd.Load(patched)
	if err != nil {
		return repair.Check{Feedback: []string{"the patch does not load: " + err.Error()}}
	}
	var fb []string
	rep := shadow.Run(def, reg, rec, rec.Case("shadow run of the recorded data", def, reg))
	clean := rep
	if fs, ok := scrub(rep.Failures).([]any); ok {
		clean.Failures = nil
		for _, f := range fs {
			if s, ok := f.(string); ok {
				clean.Failures = append(clean.Failures, truncateText(s, 300))
			}
		}
	}
	ev.Shadow, ev.Resumable = &clean, rep.Resumable()
	passed := rep.Passed
	if !rep.Passed {
		line := fmt.Sprintf("the shadow run of the failed run's recorded data (every write mocked) ended %s", rep.Status)
		var failedSteps []string
		for id, st := range rep.Steps {
			if st == "failed" || st == "parked" {
				failedSteps = append(failedSteps, id)
			}
		}
		sort.Strings(failedSteps)
		if len(failedSteps) > 0 {
			line += "; failed steps: " + strings.Join(failedSteps, ", ")
		}
		fb = append(fb, line)
		fb = append(fb, clean.Failures...)
		fb = append(fb, clean.InputProblems...)
	}

	name := fmt.Sprintf("repair of run %s: the failing case", run.String()[:8])
	reg2 := red.Case(name, def, reg)
	rr := shadow.RunCase(def, reg, red.Policies, reg2)
	o := outcome(rr, scrub)
	ev.Regression = &o
	if !rr.Passed() {
		passed = false
		fb = append(fb, "the regression test for the failing case (redacted data) did not pass:")
		fb = append(fb, o.Failures...)
		for id, msg := range rr.Errors {
			if m, ok := scrub(msg).(string); ok {
				fb = append(fb, fmt.Sprintf("step %s: %s", id, truncateText(m, 300)))
			}
		}
	}
	ev.ReproducesFailure = !shadow.RunCase(orig, reg, red.Policies, red.Case(name, orig, reg)).Passed()
	*tests = []wdtest.Case{reg2}

	ev.ModelTest = nil
	if modelTest != nil {
		mr := shadow.RunCase(def, reg, red.Policies, *modelTest)
		mo := outcome(mr, scrub)
		ev.ModelTest = &mo
		if !mr.Passed() {
			passed = false
			fb = append(fb, "your test did not pass on your patch:")
			fb = append(fb, mo.Failures...)
		} else {
			*tests = append(*tests, *modelTest)
		}
	}
	ev.Existing = []testOutcome{}
	for _, t := range existing {
		er := shadow.RunCase(def, reg, t.policies, t.test)
		eo := outcome(er, scrub)
		ev.Existing = append(ev.Existing, eo)
		if !er.Passed() {
			passed = false
			fb = append(fb, fmt.Sprintf("the workflow's existing test %q no longer passes:", t.test.Name))
			fb = append(fb, eo.Failures...)
		}
	}
	return repair.Check{Passed: passed, Feedback: fb, Report: ev}
}

// repairFailure describes the failure for classification and the model,
// from the redacted recording.
func (s *Server) repairFailure(ctx context.Context, j repairJob, status string, def *wd.Definition, reg connector.Lookup, red *shadow.Recording, scrub func(any) any) repair.Failure {
	f := repair.Failure{Reason: j.reason, RunStatus: status, Step: red.Failed, RunError: red.RunError}
	if st, ok := red.Steps[red.Failed]; ok {
		f.Error = st.Error
		f.Connector, f.Action = st.Connector, st.Action
	}
	if red.Failed != "" {
		_, sid := history.SplitInstance(red.Failed)
		if step := def.Step(sid); step != nil {
			f.StepType = step.Type
			if f.Connector == "" {
				f.Connector, f.Action = step.Connector, step.Action
			}
		}
	}
	if f.Connector != "" {
		if c, ok := reg.Get(f.Connector); ok {
			if a, ok := c.Manifest.Actions[f.Action]; ok {
				f.ActionClass = string(a.Class)
			}
		}
	}
	if f.Error != nil {
		e := *f.Error
		if m, ok := scrub(e.Message).(string); ok {
			e.Message = m
		}
		f.Error = &e
	}
	if f.RunError != nil {
		e := *f.RunError
		if m, ok := scrub(e.Message).(string); ok {
			e.Message = m
		}
		f.RunError = &e
	}
	// Drift findings for the connector actions this run used.
	var used []string
	for _, st := range red.Steps {
		if st.Connector != "" {
			used = append(used, st.Connector+"/"+st.Action)
		}
	}
	_ = db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{j.tenant}, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT connector, action, path, kind, expected, observed FROM connector_drift
			WHERE last_run_id = $1 OR (acknowledged_at IS NULL AND connector || '/' || action = ANY ($2))
			ORDER BY last_seen DESC LIMIT 20`, j.run, used)
		if err != nil {
			return err
		}
		f.Drift, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (repair.Drift, error) {
			var d repair.Drift
			return d, r.Scan(&d.Connector, &d.Action, &d.Path, &d.Kind, &d.Expected, &d.Observed)
		})
		return err
	})
	return f
}

// modelView is the redacted run as the model sees it.
func modelView(red *shadow.Recording) map[string]any {
	steps := []map[string]any{}
	for _, id := range red.Order {
		st := red.Steps[id]
		m := map[string]any{"id": id, "kind": st.Kind, "completed": st.Completed}
		if st.Connector != "" {
			m["connector"], m["action"] = st.Connector, st.Action
		}
		if st.Input != nil {
			m["input"] = st.Input
		}
		if st.Output != nil {
			m["output"] = st.Output
		}
		if st.Error != nil {
			m["error"] = st.Error
		}
		if st.Decision != "" {
			m["decision"] = st.Decision
		}
		steps = append(steps, m)
	}
	out := map[string]any{"status": red.Status, "trigger": red.Trigger, "steps": steps, "failed_step": red.Failed}
	if red.RunError != nil {
		out["run_error"] = red.RunError
	}
	return out
}

// scrubber returns a function that replaces every personal value the run's
// history held (as opened) wherever it appears in v's text: a last line of
// defence for anything derived from opened data on its way to the model or
// into stored evidence.
func scrubber(taint pii.Taint) func(any) any {
	var vals []string
	for k := range taint {
		if s, ok := strings.CutPrefix(k, "s:"); ok && len(s) >= 4 {
			vals = append(vals, s)
		}
	}
	// Longer first, so a value containing another is replaced whole.
	sort.Slice(vals, func(i, j int) bool { return len(vals[i]) > len(vals[j]) })
	var walk func(v any) any
	walk = func(v any) any {
		switch t := v.(type) {
		case string:
			for _, x := range vals {
				t = strings.ReplaceAll(t, x, "[redacted]")
			}
			return pii.Redact(t)
		case []string:
			out := make([]any, len(t))
			for i, x := range t {
				out[i] = walk(x)
			}
			return out
		case map[string]any:
			out := make(map[string]any, len(t))
			for k, x := range t {
				out[k] = walk(x)
			}
			return out
		case []any:
			out := make([]any, len(t))
			for i, x := range t {
				out[i] = walk(x)
			}
			return out
		}
		return v
	}
	return walk
}

func truncateText(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// repairAction is what a person can do for a class that changes nothing in
// the workflow.
func (s *Server) repairAction(ctx context.Context, ref runtime.RunRef, class repair.Class, f repair.Failure, def *wd.Definition, scrub func(any) any) map[string]any {
	switch class {
	case repair.Transient:
		return map[string]any{"kind": "retry", "label": "Retry from the failed step", "step": f.Step}
	case repair.Credential:
		conn := ""
		if f.Step != "" {
			_, sid := history.SplitInstance(f.Step)
			if st := def.Step(sid); st != nil {
				conn = st.Connection
			}
		}
		var env string
		_ = db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{ref.TenantID}, func(tx pgx.Tx) error {
			return tx.QueryRow(ctx, `SELECT environment FROM runs WHERE id = $1`, ref.ID).Scan(&env)
		})
		return map[string]any{"kind": "reconnect", "label": "Reconnect, then retry from the failed step", "step": f.Step,
			"connector": f.Connector, "connection": conn, "environment": env, "link": "/connections?environment=" + env}
	case repair.UnknownOutcome:
		out := map[string]any{"kind": "reconcile", "label": "Check with the provider, then resolve the step", "step": f.Step}
		w := &runtime.Worker{Store: s.Store, Registry: s.Registry, Egress: s.Egress, Logger: s.Logger, ID: "repair"}
		if s.Vault != nil {
			w.Connections = s.Vault
		}
		rep, err := w.CheckReconcile(ctx, ref, f.Step)
		switch {
		case errors.Is(err, runtime.ErrNoReconcile):
			out["reconcile"] = map[string]any{"available": false, "note": "the action has no read-only reconcile action: check the provider's dashboard"}
		case err != nil:
			out["reconcile"] = map[string]any{"available": false, "note": "the reconcile action could not be run"}
			s.Logger.Warn("repair reconcile check", "run", ref.ID, "step", f.Step, "err", err)
		default:
			out["reconcile"] = map[string]any{"available": true, "result": scrub(toAny(rep))}
		}
		return out
	}
	return nil
}

func toAny(v any) any {
	raw, _ := json.Marshal(v)
	var out any
	_ = json.Unmarshal(raw, &out)
	return out
}

type proposalOutcome struct {
	id         uuid.UUID
	tenant     uuid.UUID
	status     string
	errMsg     string
	res        *repair.Result
	ev         *repairEvidence
	action     map[string]any
	definition json.RawMessage
	diff       []string
	test       []wdtest.Case
}

func (s *Server) storeProposal(ctx context.Context, p proposalOutcome) error {
	ctx = context.WithoutCancel(ctx)
	var action, def, diff, ev, test []byte
	if p.action != nil {
		action, _ = json.Marshal(p.action)
	}
	if p.definition != nil {
		def = p.definition
	}
	if p.diff != nil {
		diff, _ = json.Marshal(p.diff)
	}
	if p.ev != nil {
		ev, _ = json.Marshal(p.ev)
	}
	if p.test != nil {
		test, _ = json.Marshal(p.test)
	}
	model := p.res.Model
	detail, _ := json.Marshal(map[string]any{"proposal": p.id.String(), "status": p.status, "class": p.res.Class, "classified_by": p.res.ClassifiedBy,
		"attempts": p.res.Attempts, "tokens": p.res.Usage.Total(), "model": model, "co_author": "ai:" + model})
	return db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{p.tenant}, func(tx pgx.Tx) error {
		var run uuid.UUID
		if err := tx.QueryRow(ctx, `UPDATE repair_proposals SET status = $2, class = $3, classified_by = $4, explanation = $5, action = $6, definition = $7,
			diff = $8, evidence = $9, test = $10, attempts = $11, model = COALESCE(NULLIF($12, ''), model), total_tokens = $13, error = NULLIF($14, ''),
			finished_at = now() WHERE id = $1 RETURNING run_id`,
			p.id, p.status, p.res.Class, p.res.ClassifiedBy, pii.Redact(p.res.Explanation), action, def, diff, ev, test, p.res.Attempts, model,
			p.res.Usage.Total(), p.errMsg).Scan(&run); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `SELECT taskiem_audit_append($1, 'ai', $2, 'ai.repair.propose', $3, $4)`, p.tenant, repairActor, "run:"+run.String(), detail)
		return err
	})
}

// storedTest is one of a workflow's regression tests.
type storedTest struct {
	test     wdtest.Case
	policies map[string]json.RawMessage
}

func (s *Server) workflowTests(ctx context.Context, tenant, wf uuid.UUID) ([]storedTest, error) {
	var out []storedTest
	err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT test, policies FROM workflow_tests WHERE workflow_id = $1 ORDER BY created_at`, wf)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var raw, pol []byte
			if err := rows.Scan(&raw, &pol); err != nil {
				return err
			}
			var t storedTest
			if err := json.Unmarshal(raw, &t.test); err != nil {
				continue
			}
			if len(pol) > 0 {
				_ = json.Unmarshal(pol, &t.policies)
			}
			out = append(out, t)
		}
		return rows.Err()
	})
	return out, err
}

// repairRecorder writes a repair's model calls to ai_interactions.
type repairRecorder struct {
	s        *Server
	repair   uuid.UUID
	tenant   uuid.UUID
	provider string
}

func (rec repairRecorder) Record(ctx context.Context, it builder.Interaction) error {
	ctx = context.WithoutCancel(ctx)
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
	return db.InTenantTx(ctx, rec.s.Store.Pool, []uuid.UUID{rec.tenant}, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO ai_interactions (id, tenant_id, repair_id, round, kind, actor, provider, model, system_digest, messages,
			response, stop_reason, outcome, error, input_tokens, output_tokens, cache_creation_tokens, cache_read_tokens, total_tokens)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, NULLIF($12, ''), $13, NULLIF($14, ''), $15, $16, $17, $18, $19)`,
			uuid.Must(uuid.NewV7()), rec.tenant, rec.repair, it.Round, it.Kind, repairCreatedBy, rec.provider, model, it.SystemDigest, msgs,
			resp, stop, it.Outcome, pii.Redact(it.Err), u.InputTokens, u.OutputTokens, u.CacheCreationTokens, u.CacheReadTokens, u.Total())
		return err
	})
}

// --- the API ---

type repairView struct {
	ID           uuid.UUID       `json:"id"`
	RunID        uuid.UUID       `json:"run_id"`
	WorkflowID   uuid.UUID       `json:"workflow_id"`
	Version      int             `json:"version"`
	Environment  string          `json:"environment"`
	Reason       string          `json:"reason"`
	Class        *string         `json:"class"`
	ClassifiedBy *string         `json:"classified_by"`
	Step         *string         `json:"step"`
	Status       string          `json:"status"`
	Explanation  string          `json:"explanation"`
	Action       json.RawMessage `json:"action"`
	Definition   json.RawMessage `json:"definition"`
	Diff         json.RawMessage `json:"diff"`
	Evidence     json.RawMessage `json:"evidence"`
	Test         json.RawMessage `json:"test"`
	Attempts     int             `json:"attempts"`
	Provider     *string         `json:"provider"`
	Model        *string         `json:"model"`
	TotalTokens  int64           `json:"total_tokens"`
	Error        *string         `json:"error"`
	CreatedBy    string          `json:"created_by"`
	CreatedAt    time.Time       `json:"created_at"`
	FinishedAt   *time.Time      `json:"finished_at"`
	DraftVersion *int            `json:"draft_version"`
	AcceptedBy   *string         `json:"accepted_by"`
	AcceptedAt   *time.Time      `json:"accepted_at"`
	DismissedBy  *string         `json:"dismissed_by"`
	DismissedAt  *time.Time      `json:"dismissed_at"`
	ResumedRunID *uuid.UUID      `json:"resumed_run_id"`
}

const repairColumns = `id, run_id, workflow_id, version, environment, reason, class, classified_by, step_id, status, explanation, action, definition, diff,
	evidence, test, attempts, provider, model, total_tokens, error, created_by, created_at, finished_at, draft_version, accepted_by, accepted_at,
	dismissed_by, dismissed_at, resumed_run_id`

func scanRepair(r pgx.CollectableRow) (repairView, error) {
	var v repairView
	var action, def, diff, ev, test []byte
	err := r.Scan(&v.ID, &v.RunID, &v.WorkflowID, &v.Version, &v.Environment, &v.Reason, &v.Class, &v.ClassifiedBy, &v.Step, &v.Status, &v.Explanation,
		&action, &def, &diff, &ev, &test, &v.Attempts, &v.Provider, &v.Model, &v.TotalTokens, &v.Error, &v.CreatedBy, &v.CreatedAt, &v.FinishedAt,
		&v.DraftVersion, &v.AcceptedBy, &v.AcceptedAt, &v.DismissedBy, &v.DismissedAt, &v.ResumedRunID)
	raw := func(b []byte) json.RawMessage {
		if b == nil {
			return json.RawMessage("null")
		}
		return b
	}
	v.Action, v.Definition, v.Diff, v.Evidence, v.Test = raw(action), raw(def), raw(diff), raw(ev), raw(test)
	return v, err
}

func (s *Server) listRepairs(w http.ResponseWriter, r *http.Request, where string, arg any) {
	var out []repairView
	err := s.tx(r, func(tx pgx.Tx) error {
		rows, err := tx.Query(r.Context(), `SELECT `+repairColumns+` FROM repair_proposals WHERE `+where+` ORDER BY created_at DESC LIMIT 50`, arg)
		if err != nil {
			return err
		}
		out, err = pgx.CollectRows(rows, scanRepair)
		return err
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"repairs": nonNil(out)})
}

func (s *Server) listRunRepairs(w http.ResponseWriter, r *http.Request) {
	ref, _, err := s.runRef(r) // checks an environment-scoped key's environment
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.listRepairs(w, r, "run_id = $1", ref.ID)
}

func (s *Server) listWorkflowRepairs(w http.ResponseWriter, r *http.Request) {
	wf, err := uuid.Parse(chi.URLParam(r, "wf"))
	if err != nil {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	s.listRepairs(w, r, "workflow_id = $1", wf)
}

func (s *Server) loadRepair(r *http.Request) (repairView, error) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		return repairView{}, pgx.ErrNoRows
	}
	var v repairView
	err = s.tx(r, func(tx pgx.Tx) error {
		rows, err := tx.Query(r.Context(), `SELECT `+repairColumns+` FROM repair_proposals WHERE id = $1`, id)
		if err != nil {
			return err
		}
		v, err = pgx.CollectExactlyOneRow(rows, scanRepair)
		return err
	})
	if p := principalFrom(r.Context()); err == nil && p.Environment != "" && v.Environment != p.Environment {
		err = pgx.ErrNoRows
	}
	return v, err
}

func (s *Server) getRepair(w http.ResponseWriter, r *http.Request) {
	v, err := s.loadRepair(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

func (s *Server) repairInteractions(w http.ResponseWriter, r *http.Request) {
	v, err := s.loadRepair(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	type row struct {
		Round    int             `json:"round"`
		Kind     string          `json:"kind"`
		Model    string          `json:"model"`
		Messages json.RawMessage `json:"messages"`
		Response *string         `json:"response"`
		Outcome  string          `json:"outcome"`
		Tokens   int64           `json:"total_tokens"`
	}
	var out []row
	err = s.tx(r, func(tx pgx.Tx) error {
		rows, err := tx.Query(r.Context(), `SELECT round, kind, model, messages, response, outcome, total_tokens FROM ai_interactions WHERE repair_id = $1 ORDER BY created_at`, v.ID)
		if err != nil {
			return err
		}
		out, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (row, error) {
			var x row
			return x, r.Scan(&x.Round, &x.Kind, &x.Model, &x.Messages, &x.Response, &x.Outcome, &x.Tokens)
		})
		return err
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"interactions": nonNil(out)})
}

// acceptRepair is "publish and resume from the failed step" (a patch), or
// "retry from the failed step" (transient and credential failures). It is
// the person's request: a patch is saved as a new version created by the
// repair system and published through the normal path, under the
// person's workflow.publish permission, so four-eyes publishing and
// promotion gates apply; the failed run is resumed once the version runs
// in the run's environment. Accepting again retries a resume that waited.
func (s *Server) acceptRepair(w http.ResponseWriter, r *http.Request) {
	v, err := s.loadRepair(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	p := principalFrom(r.Context())
	ctx := r.Context()
	switch v.Status {
	case "proposed":
		if !p.Can(PermWorkflowPublish) {
			writeErr(w, http.StatusForbidden, "requires "+PermWorkflowPublish+" and "+PermRunResolve)
			return
		}
		probs, err := s.acceptPatch(r, v)
		if errors.Is(err, errInvalid) {
			writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": "the patched definition no longer passes the publishing checks", "problems": probs})
			return
		}
		if err != nil {
			s.fail(w, r, err)
			return
		}
		s.repairsAfterDeploy(ctx, p.TenantID, v.WorkflowID)
	case "action":
		cls := ""
		if v.Class != nil {
			cls = *v.Class
		}
		if cls != string(repair.Transient) && cls != string(repair.Credential) {
			s.fail(w, r, fmt.Errorf("%w: a %s failure is settled by resolving the parked step after checking with the provider", errConflict, cls))
			return
		}
		err := s.tx(r, func(tx pgx.Tx) error {
			tag, err := tx.Exec(ctx, `UPDATE repair_proposals SET accepted_by = $2, accepted_at = now() WHERE id = $1 AND status = 'action'`, v.ID, p.Actor())
			if err != nil {
				return err
			}
			if tag.RowsAffected() == 0 {
				return fmt.Errorf("%w: the proposal changed", errConflict)
			}
			return auditTx(r, tx, "ai.repair.accept", "run:"+v.RunID.String(), map[string]any{"proposal": v.ID.String(), "class": cls, "action": "retry"})
		})
		if err != nil {
			s.fail(w, r, err)
			return
		}
		s.resumeRepair(ctx, p.TenantID, v.ID, v.RunID, v.WorkflowID, v.Version, v.Environment, p.Actor())
	case "awaiting_promotion", "awaiting_publish":
		s.repairsAfterDeploy(ctx, p.TenantID, v.WorkflowID)
	default:
		s.fail(w, r, fmt.Errorf("%w: the proposal is %s", errConflict, v.Status))
		return
	}
	out, err := s.loadRepair(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// acceptPatch saves the patch as a draft version (created by the repair
// system; the person accepting is recorded on the proposal and asks to
// publish it), stores its tests, and publishes it through the normal path.
func (s *Server) acceptPatch(r *http.Request, v repairView) ([]problem, error) {
	p := principalFrom(r.Context())
	ctx := r.Context()
	var probs []problem
	published := false
	var version int
	err := s.tx(r, func(tx pgx.Tx) error {
		var status string
		if err := tx.QueryRow(ctx, `SELECT status FROM repair_proposals WHERE id = $1 FOR UPDATE`, v.ID).Scan(&status); err != nil {
			return err
		}
		if status != "proposed" {
			return fmt.Errorf("%w: the proposal is %s", errConflict, status)
		}
		if err := s.refuseGitManaged(ctx, tx, v.WorkflowID); err != nil {
			return err
		}
		if err := tx.QueryRow(ctx, `SELECT 1 FROM workflows WHERE id = $1 FOR UPDATE`, v.WorkflowID).Scan(new(int)); err != nil {
			return err
		}
		if err := tx.QueryRow(ctx, `SELECT COALESCE(max(version), 0) + 1 FROM workflow_versions WHERE workflow_id = $1`, v.WorkflowID).Scan(&version); err != nil {
			return err
		}
		doc, err := canonical(v.Definition)
		if err != nil {
			return err
		}
		if err := insertVersionTx(ctx, tx, p.TenantID, v.WorkflowID, version, doc, nil, repairCreatedBy, ""); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE workflow_versions SET repair_id = $3 WHERE workflow_id = $1 AND version = $2`, v.WorkflowID, version, v.ID); err != nil {
			return err
		}
		// The regression tests join the workflow's tests.
		var tests []wdtest.Case
		_ = json.Unmarshal(v.Test, &tests)
		var policies []byte
		if err := tx.QueryRow(ctx, `SELECT COALESCE(jsonb_object_agg(name, document), '{}'::jsonb) FROM approval_policies WHERE state = 'active'`).Scan(&policies); err != nil {
			return err
		}
		for _, t := range tests {
			raw, _ := json.Marshal(t)
			if _, err := tx.Exec(ctx, `INSERT INTO workflow_tests (id, tenant_id, workflow_id, name, test, policies, source, repair_id, created_by)
				VALUES ($1, $2, $3, $4, $5, $6, 'repair', $7, $8) ON CONFLICT (workflow_id, name) DO NOTHING`,
				uuid.Must(uuid.NewV7()), p.TenantID, v.WorkflowID, t.Name, raw, policies, v.ID, p.Actor()); err != nil {
				return err
			}
		}
		next := "awaiting_publish"
		g, err := governanceTx(ctx, tx)
		if err != nil {
			return err
		}
		if g.FourEyesPublish {
			if p.UserID == uuid.Nil {
				return fmt.Errorf("%w: with four-eyes publishing, a person accepts a repair and another approves its publication", errForbidden)
			}
			if _, err := tx.Exec(ctx, `INSERT INTO publish_requests (tenant_id, workflow_id, version, requested_by) VALUES ($1, $2, $3, $4)`,
				p.TenantID, v.WorkflowID, version, p.UserID); err != nil {
				return err
			}
			if err := auditTx(r, tx, "publish_request.create", fmt.Sprintf("%s/%d", v.WorkflowID, version), map[string]any{"repair": v.ID.String()}); err != nil {
				return err
			}
		} else {
			by := p.id()
			var digest string
			if probs, digest, published, err = s.publishTx(ctx, tx, p.TenantID, v.WorkflowID, version, &by, ""); err != nil {
				return err
			}
			if err := auditTx(r, tx, "workflow.publish", fmt.Sprintf("%s/%d", v.WorkflowID, version), map[string]any{"digest": digest, "repair": v.ID.String()}); err != nil {
				return err
			}
			next = "awaiting_promotion" // until the run's environment runs it (repairsAfterDeploy)
		}
		if _, err := tx.Exec(ctx, `UPDATE repair_proposals SET status = $2, draft_version = $3, accepted_by = $4, accepted_at = now() WHERE id = $1`,
			v.ID, next, version, p.Actor()); err != nil {
			return err
		}
		model := ""
		if v.Model != nil {
			model = *v.Model
		}
		return auditTx(r, tx, "ai.repair.accept", fmt.Sprintf("%s/%d", v.WorkflowID, version), map[string]any{"proposal": v.ID.String(),
			"run": v.RunID.String(), "co_author": "ai:" + model, "created_by": repairCreatedBy, "four_eyes": g.FourEyesPublish})
	})
	if err == nil && published {
		if g := s.proposeToGit(r, v.WorkflowID, version); g != nil {
			s.Logger.Info("repair publish proposed to git", "workflow", v.WorkflowID, "version", version)
		}
	}
	return probs, err
}

// repairsAfterDeploy moves a workflow's accepted repairs on: one whose
// version now runs in its run's environment resumes the run; one whose
// publish request was rejected is dismissed. It is called after a publish,
// a publish decision and a promotion.
func (s *Server) repairsAfterDeploy(ctx context.Context, tenant, wf uuid.UUID) {
	ctx = context.WithoutCancel(ctx)
	type waiting struct {
		id, run    uuid.UUID
		env        string
		version    int
		acceptedBy string
	}
	var list []waiting
	err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT id, run_id, environment, draft_version, COALESCE(accepted_by, '') FROM repair_proposals
			WHERE workflow_id = $1 AND status IN ('awaiting_publish', 'awaiting_promotion') AND draft_version IS NOT NULL`, wf)
		if err != nil {
			return err
		}
		list, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (waiting, error) {
			var x waiting
			return x, r.Scan(&x.id, &x.run, &x.env, &x.version, &x.acceptedBy)
		})
		return err
	})
	if err != nil {
		s.Logger.Error("repairs after deploy", "workflow", wf, "err", err)
		return
	}
	for _, x := range list {
		ready := false
		err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
			var state string
			if err := tx.QueryRow(ctx, `SELECT state FROM workflow_versions WHERE workflow_id = $1 AND version = $2`, wf, x.version).Scan(&state); err != nil {
				return err
			}
			if state == "draft" {
				var req string
				var by *uuid.UUID
				err := tx.QueryRow(ctx, `SELECT status, decided_by FROM publish_requests WHERE workflow_id = $1 AND version = $2`, wf, x.version).Scan(&req, &by)
				if err == nil && req == "rejected" {
					who := ""
					if by != nil {
						who = by.String()
					}
					_, err = tx.Exec(ctx, `UPDATE repair_proposals SET status = 'dismissed', dismissed_by = NULLIF($2, ''), dismissed_at = now(),
						error = 'the publish request was rejected' WHERE id = $1`, x.id, who)
					return err
				}
				if errors.Is(err, pgx.ErrNoRows) {
					return nil
				}
				return err
			}
			deployed, err := deployedVersion(ctx, tx, wf, x.env)
			if err != nil {
				return err
			}
			if deployed != x.version {
				_, err := tx.Exec(ctx, `UPDATE repair_proposals SET status = 'awaiting_promotion' WHERE id = $1`, x.id)
				return err
			}
			ready = true
			return nil
		})
		if err != nil {
			s.Logger.Error("repairs after deploy", "proposal", x.id, "err", err)
			continue
		}
		if ready {
			s.resumeRepair(ctx, tenant, x.id, x.run, wf, x.version, x.env, x.acceptedBy)
		}
	}
}

// resumeRepair resumes a failed run from its failed step on version, as a
// fork that replays what the run completed (decision 0016), started by the
// person who accepted. A run that did not fail (a drift repair of a run
// that completed) has nothing to resume: the proposal ends published.
func (s *Server) resumeRepair(ctx context.Context, tenant, id, run, wf uuid.UUID, version int, env, by string) {
	ctx = context.WithoutCancel(ctx)
	var status string
	if err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT status FROM runs WHERE id = $1`, run).Scan(&status)
	}); err != nil {
		s.Logger.Error("resume repair", "proposal", id, "err", err)
		return
	}
	next, note := "published", ""
	var resumed *uuid.UUID
	if status == "failed" {
		st, err := s.Store.Start(ctx, runtime.StartRequest{TenantID: tenant, WorkflowID: wf, Version: version, Environment: env, StartedBy: by,
			TriggerID: "repair/" + id.String(), DedupKey: "resume", Fork: &runtime.Fork{Parent: run}})
		if nr, ok := runtime.IsNotResumable(err); ok {
			note = nr.Reason
		} else if err != nil {
			s.Logger.Error("resume repair", "proposal", id, "err", err)
			note = "the run could not be resumed: " + err.Error()
			if le, ok := runtime.IsLimit(err); ok {
				note = "the run could not be resumed now (" + le.Message + "); accept again to retry"
				next = ""
			}
		} else {
			next, resumed = "resumed", &st.Ref.ID
		}
	} else {
		note = "the run is " + status + "; nothing to resume"
	}
	err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		if next != "" {
			if _, err := tx.Exec(ctx, `UPDATE repair_proposals SET status = $2, resumed_run_id = $3, error = NULLIF($4, '') WHERE id = $1`, id, next, resumed, note); err != nil {
				return err
			}
		} else if _, err := tx.Exec(ctx, `UPDATE repair_proposals SET error = $2 WHERE id = $1`, id, note); err != nil {
			return err
		}
		if resumed == nil {
			return nil
		}
		detail, _ := json.Marshal(map[string]any{"proposal": id.String(), "resumed_run": resumed.String(), "version": version})
		actorType := "user"
		if strings.HasPrefix(by, "key:") {
			actorType = "api_key"
		}
		_, err := tx.Exec(ctx, `SELECT taskiem_audit_append($1, $2, $3, 'run.resume', $4, $5)`, tenant, actorType, by, "run:"+run.String(), detail)
		return err
	})
	if err != nil {
		s.Logger.Error("resume repair", "proposal", id, "err", err)
	}
}

func (s *Server) dismissRepair(w http.ResponseWriter, r *http.Request) {
	v, err := s.loadRepair(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	p := principalFrom(r.Context())
	err = s.tx(r, func(tx pgx.Tx) error {
		tag, err := tx.Exec(r.Context(), `UPDATE repair_proposals SET status = 'dismissed', dismissed_by = $2, dismissed_at = now()
			WHERE id = $1 AND status IN ('proposed', 'action', 'withheld', 'failed', 'awaiting_publish', 'awaiting_promotion')`, v.ID, p.Actor())
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return fmt.Errorf("%w: the proposal is %s", errConflict, v.Status)
		}
		return auditTx(r, tx, "ai.repair.dismiss", "run:"+v.RunID.String(), map[string]any{"proposal": v.ID.String()})
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	out, err := s.loadRepair(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) getRepairSettings(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r.Context())
	on, err := s.repairEnabled(r.Context(), p.TenantID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	available := s.AI != nil && s.AI.Provider != nil
	writeJSON(w, http.StatusOK, map[string]any{"enabled": on, "available": available, "active": on && available})
}

func (s *Server) putRepairSettings(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Enabled bool `json:"enabled"`
	}
	if err := decodeBody(r, &req); err != nil {
		s.fail(w, r, err)
		return
	}
	p := principalFrom(r.Context())
	err := s.tx(r, func(tx pgx.Tx) error {
		if _, err := tx.Exec(r.Context(), `INSERT INTO ai_repair_settings (tenant_id, enabled, updated_by) VALUES ($1, $2, $3)
			ON CONFLICT (tenant_id) DO UPDATE SET enabled = EXCLUDED.enabled, updated_by = EXCLUDED.updated_by, updated_at = now()`,
			p.TenantID, req.Enabled, p.Actor()); err != nil {
			return err
		}
		return auditTx(r, tx, "ai.repair.settings", "tenant", map[string]any{"enabled": req.Enabled})
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.getRepairSettings(w, r)
}
