package runtime

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/container"
	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/effects"
	"github.com/israel-duff/taskiem/engine/egress"
	"github.com/israel-duff/taskiem/engine/expr"
	"github.com/israel-duff/taskiem/engine/history"
)

// ContainerSecondsThisMonth is what a tenant's container steps ran this
// UTC month, in seconds.
func ContainerSecondsThisMonth(ctx context.Context, tx pgx.Tx, tenant uuid.UUID) (int64, error) {
	var n int64
	err := tx.QueryRow(ctx, `SELECT COALESCE(sum(seconds), 0)::bigint FROM container_usage
		WHERE tenant_id = $1 AND month = date_trunc('month', now() AT TIME ZONE 'UTC')::date`, tenant).Scan(&n)
	return n, err
}

// countContainer adds a run's time (rounded up to the second) or a
// refusal to the tenant's month.
func (w *Worker) countContainer(ctx context.Context, tenant uuid.UUID, elapsed time.Duration, refused bool) {
	secs := int64(math.Ceil(elapsed.Seconds()))
	runs, ref := int64(1), int64(0)
	if refused {
		runs, ref = 0, 1
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	err := db.InTenantTx(ctx, w.Store.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO container_usage (tenant_id, month, seconds, runs, refused)
			VALUES ($1, date_trunc('month', now() AT TIME ZONE 'UTC')::date, $2, $3, $4)
			ON CONFLICT (tenant_id, month) DO UPDATE SET seconds = container_usage.seconds + EXCLUDED.seconds,
			  runs = container_usage.runs + EXCLUDED.runs, refused = container_usage.refused + EXCLUDED.refused, updated_at = now()`,
			tenant, secs, runs, ref)
		return err
	})
	if err != nil {
		w.Logger.Error("container usage not recorded", "tenant", tenant, "seconds", secs, "err", err)
	}
}

// checkContainerPlan refuses a container step the tenant's plan does not
// allow: container steps off (0 minutes), or this month's minutes used up.
func (w *Worker) checkContainerPlan(ctx context.Context, p *plan) error {
	lim, err := w.Store.LimitsFor(ctx, p.c.tenant)
	if err != nil {
		return fmt.Errorf("limits: %w: %w", err, effects.ErrNotSent)
	}
	if lim.ContainerMinutesMonthly <= 0 {
		w.Store.LimitHit(ctx, p.c.tenant, "container_minutes_monthly")
		return fmt.Errorf("container steps are not part of this plan (container_minutes_monthly is 0): %w", effects.ErrFatal)
	}
	var used int64
	if err := db.InTenantTx(ctx, w.Store.Pool, []uuid.UUID{p.c.tenant}, func(tx pgx.Tx) error {
		var err error
		used, err = ContainerSecondsThisMonth(ctx, tx, p.c.tenant)
		return err
	}); err != nil {
		return fmt.Errorf("container usage: %w: %w", err, effects.ErrNotSent)
	}
	if used >= lim.ContainerMinutesMonthly*60 {
		w.Store.LimitHit(ctx, p.c.tenant, "container_minutes_monthly")
		return fmt.Errorf("this month's container minutes (%d) are used up; container steps run again next month or when the plan's allowance is raised: %w",
			lim.ContainerMinutesMonthly, effects.ErrFatal)
	}
	return nil
}

// containerName names one claim of a task (the Pod): unique per lease, so
// a re-claimed attempt never collides with what an earlier holder started.
func containerName(c claim) string {
	h := sha256.Sum256([]byte(c.task.String() + "/" + strconv.FormatInt(c.epoch, 10)))
	return fmt.Sprintf("%x-%d", h[:8], c.attempt)
}

// runContainer executes a container step on the worker's runner.
func (w *Worker) runContainer(ctx context.Context, p *plan, input any) (container.Result, error) {
	if p.step == nil || p.step.Container == nil {
		return container.Result{}, fmt.Errorf("container step has no config: %w", effects.ErrFatal)
	}
	if w.Containers == nil {
		return container.Result{}, fmt.Errorf("container steps are not enabled on this platform: %w", effects.ErrFatal)
	}
	cfg := p.step.Container
	if err := w.checkContainerPlan(ctx, p); err != nil {
		if errors.Is(err, effects.ErrFatal) { // refused by the plan, not a failed lookup
			w.countContainer(ctx, p.c.tenant, 0, true)
		}
		return container.Result{}, err
	}
	secrets := map[string]string{}
	for _, name := range cfg.Secrets {
		v, err := w.secret(ctx, p, name)
		if err != nil {
			return container.Result{}, fmt.Errorf("secret %q: %w: %w", name, err, effects.ErrFatal)
		}
		secrets[name] = v
	}
	in, err := json.Marshal(input)
	if err != nil {
		return container.Result{}, fmt.Errorf("input: %w: %w", err, effects.ErrFatal)
	}
	lim := cfg.Effective()
	spec := container.Spec{
		Name: containerName(p.c), Tenant: p.c.tenant.String(), Run: p.c.run.String(), Step: p.c.step, Attempt: p.c.attempt,
		Image: cfg.Image, Command: cfg.Command, Args: cfg.Args, Input: in,
		InputMode: cfg.InputMode, OutputMode: cfg.OutputMode, Secrets: secrets, SecretsMode: cfg.SecretsMode,
		CPUMillis: lim.CPUMillis, MemoryMB: lim.MemoryMB, Timeout: lim.Timeout, OutputBytes: lim.OutputBytes,
		Env: map[string]string{
			"TASKIEM_RUN_ID":  p.c.run.String(),
			"TASKIEM_STEP_ID": p.c.step,
			"TASKIEM_ATTEMPT": strconv.Itoa(p.c.attempt),
		},
	}
	if p.class.IsWrite() && p.key != "" {
		// The same on every attempt of a write: an idempotent_write passes
		// it on to whatever it changes.
		spec.Env["TASKIEM_IDEMPOTENCY_KEY"] = p.key
	}
	if cfg.Network == "egress" {
		if w.Proxy == nil || w.ProxyAddr == "" {
			return container.Result{}, fmt.Errorf("network egress for container steps is not configured on this platform: %w", effects.ErrFatal)
		}
		envPol, err := w.policy(ctx, p, nil)
		if err != nil {
			return container.Result{}, fmt.Errorf("allow-list: %w: %w", err, effects.ErrNotSent)
		}
		stepPol := egress.Policy{Tenant: p.c.tenant.String(), Purpose: "container_step", Hosts: cfg.Hosts}
		token, revoke := w.Proxy.Grant(stepPol, envPol)
		defer revoke()
		p.scrub = append(p.scrub, token)
		spec.Proxy = "http://taskiem:" + token + "@" + w.ProxyAddr
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	stop := w.watchCancel(ctx, p, cancel)
	defer stop()
	res, err := w.Containers.Run(ctx, spec)
	if res.Elapsed > 0 {
		w.countContainer(ctx, p.c.tenant, res.Elapsed, false)
	}
	return res, err
}

// containerEvent is the outcome event of a container step.
func (w *Worker) containerEvent(ctx context.Context, p *plan, in any) history.Event {
	for _, name := range p.step.Container.Secrets {
		if v, err := w.secret(ctx, p, name); err == nil {
			p.scrub = append(p.scrub, v)
		}
	}
	res, err := w.runContainer(ctx, p, in)
	if err != nil {
		return w.classify(p, err)
	}
	out, err := expr.DecodeJSON(res.Output)
	if err != nil {
		return w.classify(p, fmt.Errorf("container output: %w: %w", err, effects.ErrFatal))
	}
	var logs []string
	if l := strings.TrimRight(res.Logs, "\n"); l != "" {
		logs = strings.Split(l, "\n")
	}
	raw, _ := json.Marshal(history.CompletedPayload{Output: out, Logs: logs})
	return history.Event{Type: history.StepCompleted, StepID: p.c.step, Attempt: p.c.attempt, Payload: raw}
}

// watchCancel cancels a long-running step when its run is cancelled or the
// step itself is (a losing parallel branch), so the runner removes what it
// started. It returns a function that stops watching.
func (w *Worker) watchCancel(ctx context.Context, p *plan, cancel context.CancelFunc) func() {
	every := w.CancelPoll
	if every <= 0 {
		every = 2 * time.Second
	}
	done := make(chan struct{})
	go func() {
		t := time.NewTicker(every)
		defer t.Stop()
		for {
			select {
			case <-done:
				return
			case <-ctx.Done():
				return
			case <-t.C:
				var stop bool
				err := db.InTenantTx(ctx, w.Store.Pool, []uuid.UUID{p.c.tenant}, func(tx pgx.Tx) error {
					return tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM runs WHERE id = $1 AND status = 'cancelled')
						OR EXISTS (SELECT 1 FROM run_events WHERE run_id = $1 AND step_id = $2 AND type = $3)`,
						p.c.run, p.c.step, history.StepCancelled).Scan(&stop)
				})
				if err == nil && stop {
					w.Logger.Info("container step cancelled", "run", p.c.run, "step", p.c.step, "attempt", p.c.attempt)
					cancel()
					return
				}
			}
		}
	}()
	return func() { close(done) }
}

// sweep asks the runner to remove what dead workers left behind, every
// minute while the worker runs.
func (w *Worker) sweep(ctx context.Context, sw container.Sweeper) {
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	for {
		if err := sw.Sweep(ctx); err != nil && ctx.Err() == nil {
			w.Logger.Warn("container sweep failed", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}
