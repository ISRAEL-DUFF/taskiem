package runtime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"github.com/israel-duff/taskiem/engine/connector"
	"github.com/israel-duff/taskiem/engine/container"
	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/drift"
	"github.com/israel-duff/taskiem/engine/effects"
	"github.com/israel-duff/taskiem/engine/egress"
	"github.com/israel-duff/taskiem/engine/expr"
	"github.com/israel-duff/taskiem/engine/history"
	"github.com/israel-duff/taskiem/engine/pii"
	"github.com/israel-duff/taskiem/engine/sandbox"
	"github.com/israel-duff/taskiem/engine/secrets"
	"github.com/israel-duff/taskiem/engine/telemetry"
	"github.com/israel-duff/taskiem/engine/wd"
)

var exprEngine = expr.MustNew()

// Secrets resolves secret values for a tenant and environment at execution
// time. Values never enter history (spec 3.3).
type Secrets interface {
	Get(ctx context.Context, tenant uuid.UUID, environment, name string) (string, error)
}

// ReservedSecret reports whether a secret name belongs to the platform, not
// to workflows: Git sync's credentials and webhook secret (kept in their own
// environment since migration 00028, refused here too), and webhook
// triggers' keys ("webhook_" plus the WD id, so "webhook_wf_..."), which
// tenants set but only deliveries are verified with. A workflow naming one
// fails rather than reading it.
func ReservedSecret(name string) bool {
	return name == "git_credentials" || name == "git_webhook_secret" || strings.HasPrefix(name, "webhook_wf_")
}

// secret reads a secret a workflow names. Each secret is decrypted at most
// once per attempt, so the vault records one read per secret per attempt.
func (w *Worker) secret(ctx context.Context, p *plan, name string) (string, error) {
	if ReservedSecret(name) {
		return "", fmt.Errorf("secret %q is reserved for the platform: %w", name, effects.ErrFatal)
	}
	if v, ok := p.secrets[name]; ok {
		return v, nil
	}
	v, err := w.Secrets.Get(secrets.WithUse(ctx, p.use(secrets.KindSecret)), p.c.tenant, p.env, name)
	if err != nil {
		return "", err
	}
	if p.secrets == nil {
		p.secrets = map[string]string{}
	}
	p.secrets[name] = v
	return v, nil
}

// use describes this attempt's reads to the vault (secret_reads).
func (p *plan) use(kind string) secrets.Use {
	purpose := "step." + p.stepType
	if p.mode == modeReconcile {
		purpose = "step.reconcile"
	}
	return secrets.Use{Kind: kind, Purpose: purpose, RunID: p.c.run, StepID: p.c.step, Attempt: p.c.attempt, Actor: "system"}
}

// MapSecrets is an in-memory Secrets for development and tests.
type MapSecrets map[string]string

func (m MapSecrets) Get(_ context.Context, _ uuid.UUID, _ string, name string) (string, error) {
	v, ok := m[name]
	if !ok {
		return "", fmt.Errorf("secret %q is not set", name)
	}
	return v, nil
}

// Connections resolves connector credentials for a tenant and environment.
type Connections interface {
	Credentials(ctx context.Context, tenant uuid.UUID, environment, connector, name string) (map[string]string, error)
}

// Hooks inject faults for the crash-recovery suite. A hook returning an
// error makes the worker abandon the task at that point, as if the process
// died: nothing more is written and the lease is left to expire.
type Hooks struct {
	AfterIntent func(inst string, attempt int) error
	AfterCall   func(inst string, attempt int) error
}

// Worker claims tasks from one queue and executes them.
type Worker struct {
	Store    *Store
	Registry *connector.Registry
	Secrets  Secrets
	// Egress guards every outbound connection (spec 14.2).
	Egress *egress.Guard
	// Connections supplies connector credentials; secrets.Vault implements it.
	Connections Connections
	ID          string
	Queue       string
	// Pool is the worker pool this worker serves (TASKIEM_WORKER_POOL,
	// decision 0024): it claims only tasks of tenants routed there.
	// Default "shared".
	Pool        string
	Concurrency int
	Lease       time.Duration
	// CallTimeout bounds every provider call, measured for writes from when
	// EffectIntent was recorded. A worker never starts a write after that
	// deadline, and reconcile concludes "not found" only once it has
	// passed, so a stalled worker cannot send after another has re-sent.
	CallTimeout time.Duration
	Hooks       *Hooks
	Logger      *slog.Logger
	// Drain is how long in-flight steps may run on after shutdown begins
	// before they are cancelled (spec 15.4); default 30s. Nothing new is
	// claimed once shutdown begins.
	Drain time.Duration
	// Containers runs container steps (the "container" queue, spec 7.5);
	// nil: they fail as not enabled.
	Containers container.Runner
	// Proxy is the egress proxy for container steps with network
	// "egress"; ProxyAddr is its address as the sandbox reaches it
	// (host:port).
	Proxy     *egress.Proxy
	ProxyAddr string
	// CancelPoll is how often a running container step checks whether
	// it was cancelled; default 2s.
	CancelPoll time.Duration
}

func (w *Worker) defaults() {
	if w.Queue == "" {
		w.Queue = "connector"
	}
	if w.Pool == "" {
		w.Pool = SharedPool
	}
	if w.Concurrency <= 0 {
		w.Concurrency = 16
		if w.Queue == "sandbox" || w.Queue == "container" {
			// Each script may use up to the sandbox memory cap; each
			// container holds a sandbox Pod.
			w.Concurrency = 4
		}
	}
	if w.Lease <= 0 {
		w.Lease = 60 * time.Second
	}
	if w.CallTimeout <= 0 {
		w.CallTimeout = 30 * time.Second
	}
	if w.Egress == nil {
		w.Egress = &egress.Guard{Logger: w.Logger}
	}
	if w.Logger == nil {
		w.Logger = slog.Default()
	}
	if w.ID == "" {
		w.ID = "worker-" + uuid.NewString()[:8]
	}
	if w.Secrets == nil {
		w.Secrets = MapSecrets{}
	}
}

type claim struct {
	task, tenant, run uuid.UUID
	step              string
	attempt           int
	epoch             int64
}

// Run claims and executes tasks until ctx ends. One claimer feeds the
// executors and claims only as many tasks as are free (decision 0002).
func (w *Worker) Run(ctx context.Context) error {
	w.defaults()
	if w.Queue == "sandbox" {
		sandbox.Init(0)
		// Compiling CPython takes seconds the first time; do it now, so no
		// step's time limit pays for it.
		go func() {
			if err := sandbox.InitPython(); err != nil {
				w.Logger.Error("python sandbox unavailable", "err", err)
			}
		}()
	}
	wake, stop, err := listen(ctx, w.Store, "taskiem_tasks")
	if err != nil {
		return err
	}
	defer stop()
	go w.reportLive(ctx)
	if sw, ok := w.Containers.(container.Sweeper); ok && w.Queue == "container" {
		go w.sweep(ctx, sw)
	}
	// In-flight steps outlive ctx by up to Drain, so a shutdown does not
	// abort provider calls midway and leave outcomes unknown.
	execCtx, cancelExec := context.WithCancel(context.WithoutCancel(ctx))
	defer cancelExec()
	drain := w.Drain
	if drain <= 0 {
		drain = 30 * time.Second
	}
	go func() {
		select {
		case <-ctx.Done():
		case <-execCtx.Done():
			return
		}
		t := time.NewTimer(drain)
		defer t.Stop()
		select {
		case <-t.C:
			cancelExec()
		case <-execCtx.Done():
		}
	}()
	work := make(chan claim)
	free := make(chan struct{}, w.Concurrency)
	for i := 0; i < w.Concurrency; i++ {
		free <- struct{}{}
	}
	var wg sync.WaitGroup
	for i := 0; i < w.Concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for c := range work {
				if err := w.execute(execCtx, c); err != nil && execCtx.Err() == nil {
					w.Logger.Error("task failed to execute", "run", c.run, "step", c.step, "attempt", c.attempt, "err", err)
				}
				free <- struct{}{}
			}
		}()
	}
	defer func() { close(work); wg.Wait() }()
	for ctx.Err() == nil {
		select {
		case <-free:
		case <-ctx.Done():
			return nil
		}
		slots := 1
	more:
		for slots < w.Concurrency {
			select {
			case <-free:
				slots++
			default:
				break more
			}
		}
		claims, err := w.claim(ctx, slots)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			w.Logger.Error("claim failed", "err", err)
			claims = nil
		}
		for _, c := range claims {
			work <- c
		}
		for i := len(claims); i < slots; i++ {
			free <- struct{}{}
		}
		if len(claims) < slots {
			waitFor(ctx, wake, time.Second)
		}
	}
	return nil
}

// RunOnce claims and executes every available task once, sequentially.
func (w *Worker) RunOnce(ctx context.Context) (int, error) {
	w.defaults()
	n := 0
	for {
		claims, err := w.claim(ctx, 1)
		if err != nil || len(claims) == 0 {
			return n, err
		}
		if err := w.execute(ctx, claims[0]); err != nil {
			return n, err
		}
		n++
	}
}

func (w *Worker) claim(ctx context.Context, n int) ([]claim, error) {
	tenantCap := w.Store.defaults().WorkerConcurrency
	if w.Queue == "container" {
		tenantCap = w.Store.defaults().ContainerConcurrency
	}
	rows, err := w.Store.Pool.Query(ctx, `SELECT task_id, tenant_id, run_id, step_id, attempt, lease_epoch FROM taskiem_claim_tasks($1, $2, $3, $4::interval, $5, $6)`,
		w.Queue, w.ID, n, w.Lease.String(), tenantCap, w.Pool)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []claim
	for rows.Next() {
		var c claim
		if err := rows.Scan(&c.task, &c.tenant, &c.run, &c.step, &c.attempt, &c.epoch); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// reportLive records every 30 seconds that this worker serves its pool
// and queue, so operators never route work to a pool nobody serves.
func (w *Worker) reportLive(ctx context.Context) {
	t := time.NewTicker(30 * time.Second)
	defer t.Stop()
	for {
		if _, err := w.Store.Pool.Exec(ctx, `SELECT taskiem_worker_seen($1, $2, $3)`, w.ID, w.Queue, w.Pool); err != nil && ctx.Err() == nil {
			w.Logger.Debug("worker report failed", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// errFenced means this worker no longer holds the task's lease.
var errFenced = errors.New("fenced: lease lost")

// errAbandoned is a simulated crash from a hook.
var errAbandoned = errors.New("abandoned")

// errPostpone asks for the task to be requeued until p.intentAt + CallTimeout.
var errPostpone = errors.New("postpone")

// errCancelled: the step was cancelled (a losing parallel branch) before its
// write could start; the task is dropped without an outcome.
var errCancelled = errors.New("cancelled")

// openIntent reports whether some attempt recorded EffectIntent and never
// reported an outcome: its write may be under way or may have happened.
func openIntent(attempts []int, failures map[int]history.Error) bool {
	for _, a := range attempts {
		if _, ok := failures[a]; !ok {
			return true
		}
	}
	return false
}

type mode int

const (
	modeSend mode = iota
	modeReconcile
	modePark
	modeDone // an outcome is already recorded; just release the task
)

type plan struct {
	c        claim
	env      string
	sched    history.ScheduledPayload
	stepType string
	class    effects.Class
	conn     *connector.Connector
	action   string
	spec     *effects.Spec
	field    string // input field (or header) that carries the key
	mode     mode
	group    int
	key      string
	keyIn    effects.KeyInput
	intentAt time.Time // when the EffectIntent in force was recorded (database clock)
	keyFirst time.Time // when this attempt group's key was first about to be sent
	step     *wd.Step
	taint    pii.Taint         // personal values in this run; outputs repeating them are sealed
	drift    []drift.Finding   // how a successful output departed from its schema
	scrub    []string          // secret values used by this attempt; never written to history
	secrets  map[string]string // secrets read by this attempt, by name
	creds    map[string]string // the connection's credentials, once read
}

// execute runs one claimed task: prepare (and record intent), call, record
// the outcome, decide inline.
func (w *Worker) execute(ctx context.Context, c claim) error {
	start := time.Now()
	ctx, span := telemetry.Tracer().Start(ctx, "step "+c.step, trace.WithAttributes(
		telemetry.TenantID.String(c.tenant.String()), telemetry.RunID.String(c.run.String()), telemetry.StepID.String(c.step)))
	defer span.End()
	target, outcome := "unknown", "error"
	defer func() {
		telemetry.Steps.WithLabelValues(w.Queue, target, outcome).Inc()
		telemetry.StepSeconds.WithLabelValues(w.Queue, target).Observe(time.Since(start).Seconds())
		span.SetAttributes(attribute.String("outcome", outcome))
	}()
	err := w.run(ctx, c, &target, &outcome)
	if err != nil {
		span.RecordError(err)
	}
	return err
}

// run is execute's body; it reports the step's target and outcome.
func (w *Worker) run(ctx context.Context, c claim, target, outcome *string) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	stopBeat := w.heartbeat(ctx, c, cancel)
	defer stopBeat()

	p, err := w.prepare(ctx, c)
	if err != nil {
		if errors.Is(err, errFenced) {
			*outcome = "fenced"
			return nil
		}
		if errors.Is(err, errCancelled) {
			*outcome = "cancelled"
			return w.finish(ctx, p, nil)
		}
		return err
	}
	*target = p.stepType
	if p.conn != nil {
		*target = p.conn.Ref()
	}
	*outcome = "abandoned"
	if p.mode == modeSend && p.class.IsWrite() && w.Hooks != nil && w.Hooks.AfterIntent != nil {
		if err := w.Hooks.AfterIntent(c.step, c.attempt); err != nil {
			return nil
		}
	}

	var result history.Event
	switch p.mode {
	case modeDone:
		*outcome = "already_recorded"
		return w.finish(ctx, p, nil)
	case modePark:
		result = failed(c, "unknown_outcome", "an earlier attempt may have reached the provider and the action cannot be safely retried", "park")
	case modeReconcile:
		result, err = w.reconcile(ctx, p)
		if errors.Is(err, errPostpone) {
			*outcome = "postponed"
			return w.postpone(ctx, p, p.intentAt.Add(w.CallTimeout))
		}
		if errors.Is(err, errAbandoned) || errors.Is(err, errFenced) {
			return nil
		}
		if err != nil {
			return err
		}
	}
	if p.mode == modeSend {
		result = w.send(ctx, p)
	}
	if w.Hooks != nil && w.Hooks.AfterCall != nil {
		if err := w.Hooks.AfterCall(c.step, c.attempt); err != nil {
			return nil
		}
	}
	*outcome = "completed"
	if result.Type == history.StepFailed {
		var fp history.FailedPayload
		_ = json.Unmarshal(result.Payload, &fp)
		*outcome = "failed_" + fp.Error.Next
	}
	return w.finish(ctx, p, &result)
}

func failed(c claim, kind, msg, next string) history.Event {
	raw, _ := json.Marshal(history.FailedPayload{Error: history.Error{Kind: kind, Message: msg, Next: next}})
	return history.Event{Type: history.StepFailed, StepID: c.step, Attempt: c.attempt, Payload: raw}
}

func completedEvent(c claim, out any, reconciled bool) history.Event {
	raw, _ := json.Marshal(history.CompletedPayload{Output: out, Reconciled: reconciled})
	return history.Event{Type: history.StepCompleted, StepID: c.step, Attempt: c.attempt, Payload: raw}
}

// prepare reads what the task needs and, for a write that will be sent,
// records EffectIntent under the fencing check, all before any external call.
func (w *Worker) prepare(ctx context.Context, c claim) (*plan, error) {
	p := &plan{c: c}
	err := db.InTenantTx(ctx, w.Store.Pool, []uuid.UUID{c.tenant}, func(tx pgx.Tx) error {
		var wfID uuid.UUID
		var version int
		var runSeed string // a fork's inherited seed (fork.go)
		if err := tx.QueryRow(ctx, `SELECT workflow_id, version, environment, COALESCE(idempotency_seed, '') FROM runs WHERE id = $1`, c.run).Scan(&wfID, &version, &p.env, &runSeed); err != nil {
			return err
		}
		def, err := w.Store.definition(ctx, tx, wfID, version)
		if err != nil {
			return err
		}
		raw, err := History(ctx, tx, c.run)
		if err != nil {
			return err
		}
		hist, taint, err := w.Store.openHistory(ctx, tx, c.tenant, raw)
		if err != nil {
			return err
		}
		p.taint = taint
		found, cancelled := false, false
		var intents []history.IntentPayload
		var intentAttempts []int
		var intentTimes []time.Time
		outcomes := map[int]history.Error{}
		for _, e := range hist {
			if e.StepID != c.step {
				continue
			}
			switch e.Type {
			case history.StepScheduled:
				if e.Attempt == c.attempt {
					if err := json.Unmarshal(e.Payload, &p.sched); err != nil {
						return err
					}
					found = true
				}
			case history.StepCompleted:
				p.mode = modeDone
			case history.StepCancelled:
				cancelled = true
			case history.StepFailed:
				var fp history.FailedPayload
				_ = json.Unmarshal(e.Payload, &fp)
				outcomes[e.Attempt] = fp.Error
				if e.Attempt == c.attempt {
					p.mode = modeDone
				}
			case history.EffectIntent:
				var ip history.IntentPayload
				if err := json.Unmarshal(e.Payload, &ip); err != nil {
					return err
				}
				intents = append(intents, ip)
				intentAttempts = append(intentAttempts, e.Attempt)
				intentTimes = append(intentTimes, e.RecordedAt)
			}
		}
		if !found {
			return fmt.Errorf("no StepScheduled for %s attempt %d", c.step, c.attempt)
		}
		if cancelled && !openIntent(intentAttempts, outcomes) {
			p.mode = modeDone // nothing of it reached a provider; never start it now
		}
		if p.mode == modeDone {
			return nil
		}
		_, id := history.SplitInstance(c.step)
		if s := def.Step(id); s != nil {
			p.stepType = s.Type
			p.step = s
		}
		if err := w.resolveExecutor(ctx, p); err != nil {
			if errors.Is(err, errLookup) {
				return err // the database, not the step: try the task again
			}
			p.mode = modeDone
			return w.recordFailure(ctx, tx, p, "fatal", err.Error())
		}
		if !p.class.IsWrite() {
			return nil
		}

		// Choose how to proceed from what earlier attempts left behind.
		seed := p.sched.Seed
		if seed == "" {
			seed = c.run.String()
			if runSeed != "" {
				seed = runSeed
			}
		}
		p.keyIn = effects.KeyInput{TenantID: c.tenant.String(), Seed: seed, StepID: p.sched.KeyStep}
		if len(intents) == 0 && runSeed != "" {
			// A step the parent attempted and failed for good starts a new
			// attempt group: a new key, not the parent's cached failure.
			if p.group, err = forkKeyGroup(ctx, tx, c.run, c.step); err != nil {
				return err
			}
		}
		if n := len(intents); n > 0 {
			last, lastAttempt := intents[n-1], intentAttempts[n-1]
			p.group = last.AttemptGroup
			p.intentAt = intentTimes[n-1]
			var next effects.Next
			if prev, ok := outcomes[lastAttempt]; ok && lastAttempt != c.attempt {
				next = effects.AfterError(p.class, kindOf(prev.Kind))
			} else {
				next = effects.OnOpenIntent(p.class) // the attempt that wrote it never reported back
			}
			switch next {
			case effects.Reconcile:
				p.mode = modeReconcile
			case effects.Park:
				p.mode = modePark
			}
		}
		p.keyIn.AttemptGroup = p.group
		for i, ip := range intents {
			if ip.AttemptGroup == p.group && (p.keyFirst.IsZero() || intentTimes[i].Before(p.keyFirst)) {
				p.keyFirst = intentTimes[i]
			}
		}
		if p.keyFirst.IsZero() {
			p.keyFirst = time.Now()
		}
		if p.key, err = p.spec.Key(p.keyIn); err != nil {
			return err
		}
		if p.mode != modeSend {
			return nil
		}
		return w.recordIntent(ctx, tx, p)
	})
	return p, err
}

func kindOf(k string) effects.ErrorKind {
	switch k {
	case "retryable":
		return effects.KindRetryable
	case "fatal":
		return effects.KindFatal
	case "not_sent":
		return effects.KindNotSent
	case "indeterminate":
		return effects.KindIndeterminate
	case KindKeyRestored:
		return effects.KindNotSent // parked before anything was sent
	}
	return effects.KindUnknownOutcome
}

// errLookup is a failure to load a tenant's connectors.
var errLookup = errors.New("connector lookup")

// defaultHTTPKey is how an http step's idempotency key is encoded.
var defaultHTTPKey = effects.Spec{Encoding: effects.Base32Lower, Length: 32, Prefix: "tsk_"}

func (w *Worker) resolveExecutor(ctx context.Context, p *plan) error {
	switch {
	case p.sched.Connector != "":
		if w.Registry == nil {
			return fmt.Errorf("no connector registry")
		}
		reg, err := w.Registry.For(ctx, p.c.tenant.String())
		if err != nil {
			return fmt.Errorf("%w: %w", errLookup, err)
		}
		conn, ok := reg.Get(p.sched.Connector)
		if !ok {
			return fmt.Errorf("connector %s is not installed", p.sched.Connector)
		}
		spec, ok := conn.Manifest.Actions[p.sched.Action]
		if !ok {
			return fmt.Errorf("connector %s has no action %q", p.sched.Connector, p.sched.Action)
		}
		p.conn, p.action, p.class = conn, p.sched.Action, spec.Class
		if spec.Idempotency != nil {
			p.spec, p.field = spec.Idempotency, spec.Idempotency.Field
		} else {
			p.spec = &defaultHTTPKey
		}
	case p.stepType == "code":
		// Code has no external effects of its own; host.fetch goes through
		// the egress guard. It is treated as a read for retries.
		p.class = effects.Read
	case p.stepType == "container":
		// A container step is an unsafe_write unless it declares a class:
		// what an arbitrary program did cannot be assumed undone.
		if p.step == nil || p.step.Container == nil {
			return fmt.Errorf("container step has no config")
		}
		c, err := effects.ParseClass(p.step.Container.ContainerClass())
		if err != nil {
			return err
		}
		p.class, p.spec = c, &defaultHTTPKey
	case p.stepType == "http":
		in, _ := p.sched.Input.(map[string]any)
		method, _ := in["method"].(string)
		cls, _ := in["class"].(string)
		if cls == "" {
			if method != "GET" && method != "HEAD" {
				return fmt.Errorf("http %s step has no action class", method)
			}
			cls = "read"
		}
		c, err := effects.ParseClass(cls)
		if err != nil {
			return err
		}
		p.class, p.spec = c, &defaultHTTPKey
		if h, _ := in["idempotency_header"].(string); h != "" {
			p.field = "header:" + h
		}
	default:
		return fmt.Errorf("step type %q has no executor in this engine version", p.stepType)
	}
	return nil
}

func (w *Worker) fence(ctx context.Context, tx pgx.Tx, c claim) error {
	var ok bool
	if err := tx.QueryRow(ctx, `SELECT taskiem_task_heartbeat($1, $2, $3, $4::interval)`, c.task, w.ID, c.epoch, w.Lease.String()).Scan(&ok); err != nil {
		return err
	}
	if !ok {
		return errFenced
	}
	return nil
}

func (w *Worker) recordIntent(ctx context.Context, tx pgx.Tx, p *plan) error {
	if err := w.fence(ctx, tx, p.c); err != nil {
		return err
	}
	// Under the run lock, so a cancellation decided since prepare read the
	// history cannot slip in between the check and the intent.
	if _, err := lockRun(ctx, tx, p.c.run); err != nil {
		return err
	}
	var cancelled bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM run_events WHERE run_id = $1 AND step_id = $2 AND type = $3)`,
		p.c.run, p.c.step, history.StepCancelled).Scan(&cancelled); err != nil {
		return err
	}
	if cancelled {
		return errCancelled
	}
	digest := sha256.New()
	_ = json.NewEncoder(digest).Encode(p.sched.Input)
	payload := history.IntentPayload{Key: p.key, Seed: p.keyIn.Seed, KeyStep: p.keyIn.StepID, AttemptGroup: p.group,
		Class: string(p.class), Digest: hex.EncodeToString(digest.Sum(nil))}
	if _, err := appendEvent(ctx, tx, p.c.run, history.StepStarted, p.c.step, p.c.attempt, map[string]any{"worker": w.ID}, history.OriginWorker); err != nil {
		return err
	}
	if _, err := appendEvent(ctx, tx, p.c.run, history.EffectIntent, p.c.step, p.c.attempt, payload, history.OriginWorker); err != nil {
		return err
	}
	return tx.QueryRow(ctx, `SELECT now()`).Scan(&p.intentAt) // equals the intent's recorded_at
}

func (w *Worker) recordFailure(ctx context.Context, tx pgx.Tx, p *plan, kind, msg string) error {
	ok, err := w.finishTask(ctx, tx, p.c)
	if err != nil || !ok {
		return err
	}
	if _, err := appendEvent(ctx, tx, p.c.run, history.StepFailed, p.c.step, p.c.attempt,
		history.FailedPayload{Error: history.Error{Kind: kind, Message: msg, Next: "fail"}}, history.OriginWorker); err != nil {
		return err
	}
	return w.Store.decideInline(ctx, tx, RunRef{ID: p.c.run, TenantID: p.c.tenant})
}

func (w *Worker) finishTask(ctx context.Context, tx pgx.Tx, c claim) (bool, error) {
	var ok bool
	err := tx.QueryRow(ctx, `SELECT taskiem_task_finish($1, $2, $3)`, c.task, w.ID, c.epoch).Scan(&ok)
	return ok, err
}

// recordDrift notes how an output departed from its declared schema. The
// step still completes: the output is what the provider said.
func (w *Worker) recordDrift(ctx context.Context, tx pgx.Tx, p *plan) error {
	for _, f := range p.drift {
		var first bool
		err := tx.QueryRow(ctx, `INSERT INTO connector_drift (tenant_id, connector, version, action, path, kind, expected, observed, last_run_id)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
			ON CONFLICT (tenant_id, connector, version, action, path, kind) DO UPDATE
			SET observed = EXCLUDED.observed, last_seen = now(), occurrences = connector_drift.occurrences + 1, last_run_id = EXCLUDED.last_run_id
			RETURNING occurrences = 1`, p.c.tenant, p.conn.Ref(), p.conn.Manifest.Version, p.action, f.Path, f.Kind, f.Expected, f.Observed, p.c.run).Scan(&first)
		if err != nil {
			return err
		}
		telemetry.ConnectorDrift.WithLabelValues(p.conn.Ref(), p.action, f.Kind).Inc()
		if first && w.Logger != nil {
			w.Logger.Warn("connector output departs from its schema", "connector", p.conn.Ref(), "version", p.conn.Manifest.Version,
				"action", p.action, "path", f.Path, "kind", f.Kind, "expected", f.Expected, "observed", f.Observed, "tenant", p.c.tenant)
		}
	}
	return nil
}

// postpone releases the task back to the queue, available at a later time,
// without recording an outcome.
func (w *Worker) postpone(ctx context.Context, p *plan, at time.Time) error {
	// Fenced: repeating it after an unknown COMMIT changes nothing.
	return db.InTenantTxRetry(ctx, w.Store.Pool, []uuid.UUID{p.c.tenant}, true, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `UPDATE tasks SET lease_owner = NULL, lease_until = NULL, available_at = $4
			WHERE id = $1 AND lease_owner = $2 AND lease_epoch = $3`, p.c.task, w.ID, p.c.epoch, at)
		return err
	})
}

// finish releases the task and records the outcome, if any, under the fence.
//
// It rides out a database failover (decision 0024): the outcome of a call
// already made is kept and recorded once the new primary answers, as long
// as the lease still holds. Repeating it after an unknown COMMIT is safe:
// a committed finish released the task, so the repeat is fenced out.
func (w *Worker) finish(ctx context.Context, p *plan, result *history.Event) error {
	return db.InTenantTxRetry(ctx, w.Store.Pool, []uuid.UUID{p.c.tenant}, true, func(tx pgx.Tx) error {
		ok, err := w.finishTask(ctx, tx, p.c)
		if err != nil {
			return err
		}
		if !ok {
			return nil // fenced out: the task belongs to someone else now
		}
		if result == nil {
			return nil
		}
		if len(p.scrub) > 0 {
			// A provider error or a script may repeat a secret (an API key in
			// a URL, a token echoed back): it is replaced before it is written.
			raw, err := scrubSecrets(result.Payload, p.scrub)
			if err != nil {
				return err
			}
			result.Payload = raw
		}
		if len(p.drift) > 0 && result.Type == history.StepCompleted {
			if err := w.recordDrift(ctx, tx, p); err != nil {
				return err
			}
		}
		var payload any = result.Payload
		if w.Store.PII != nil {
			// Personal data in what the provider returned is sealed before it
			// is written: values seen earlier in the run, and values the
			// detectors recognise (spec 9.3). Free text is masked.
			v, err := expr.DecodeJSON(result.Payload)
			if err != nil {
				return err
			}
			redactText(v)
			var paths []pii.Path
			if result.Type == history.StepCompleted {
				paths = outputPIIPaths(p.conn, p.action) // what the manifest declares personal
			}
			if v, err = pii.Seal(ctx, w.Store.PII, tx, p.c.tenant, v, paths, p.taint); err != nil {
				return err
			}
			if payload, err = pii.SealDetected(ctx, w.Store.PII, tx, p.c.tenant, v, p.taint); err != nil {
				return err
			}
		}
		if _, err := appendEvent(ctx, tx, p.c.run, result.Type, result.StepID, result.Attempt, payload, history.OriginWorker); err != nil {
			return err
		}
		if result.Type == history.StepFailed {
			var fp history.FailedPayload
			_ = json.Unmarshal(result.Payload, &fp)
			if fp.Error.Next == "park" {
				if _, err := tx.Exec(ctx, `UPDATE runs SET status = 'needs_reconciliation' WHERE id = $1 AND status NOT IN ('completed', 'failed', 'cancelled')`, p.c.run); err != nil {
					return err
				}
			}
			if fp.Error.Kind == KindKeyUnavailable {
				mode := "send"
				if p.mode == modeReconcile {
					mode = "reconcile"
				}
				if _, err := tx.Exec(ctx, `INSERT INTO key_parked_steps (tenant_id, run_id, step_id, attempt, mode) VALUES ($1, $2, $3, $4, $5)
					ON CONFLICT DO NOTHING`, p.c.tenant, p.c.run, p.c.step, p.c.attempt, mode); err != nil {
					return err
				}
			}
		}
		return w.Store.decideInline(ctx, tx, RunRef{ID: p.c.run, TenantID: p.c.tenant})
	})
}

// reconcile asks the provider whether an earlier attempt's effect happened.
func (w *Worker) reconcile(ctx context.Context, p *plan) (history.Event, error) {
	spec := p.conn.Manifest.Actions[p.action]
	action := p.conn.Actions[spec.Reconcile]
	if action == nil {
		return failed(p.c, "unknown_outcome", "no reconcile action", "park"), nil
	}
	if time.Now().Before(p.intentAt.Add(w.CallTimeout)) {
		// The attempt that wrote the intent may still be talking to the
		// provider; "not found" now would not be trustworthy. Requeue the
		// task for after the deadline without spending a retry.
		return history.Event{}, errPostpone
	}
	field := p.field
	if field == "" || strings.HasPrefix(field, "header:") {
		field = "reference"
	}
	creds, err := w.credentials(ctx, p)
	if err != nil {
		return w.secretFailure(p, err), nil
	}
	rctx, cancel := context.WithTimeout(ctx, w.CallTimeout)
	defer cancel()
	hc, err := w.client(ctx, p, w.CallTimeout)
	if ev, ok := keyParked(p, err); ok {
		return ev, nil
	}
	if err != nil {
		return history.Event{}, err
	}
	resp, err := action.Execute(rctx, connector.Request{Input: map[string]any{field: p.key}, Credentials: creds, IdempotencyKey: p.key, Attempt: p.c.attempt, Logger: w.Logger, HTTP: hc})
	switch {
	case err == nil:
		return completedEvent(p.c, resp.Output, true), nil
	case errors.Is(err, connector.ErrNotFound):
		// Safe to send again, under a new key (contract: idempotency.md).
		p.group++
		p.keyIn.AttemptGroup = p.group
		p.keyFirst = time.Now() // a new key
		if p.key, err = p.spec.Key(p.keyIn); err != nil {
			return history.Event{}, err
		}
		// Retried only when it certainly did not commit: a second
		// EffectIntent must never be appended.
		if err := db.InTenantTxRetry(ctx, w.Store.Pool, []uuid.UUID{p.c.tenant}, false, func(tx pgx.Tx) error { return w.recordIntent(ctx, tx, p) }); err != nil {
			return history.Event{}, err
		}
		if w.Hooks != nil && w.Hooks.AfterIntent != nil {
			if err := w.Hooks.AfterIntent(p.c.step, p.c.attempt); err != nil {
				return history.Event{}, errAbandoned
			}
		}
		p.mode = modeSend
		return history.Event{}, nil
	case effects.Classify(err) == effects.KindRetryable || effects.Classify(err) == effects.KindNotSent:
		return failed(p.c, "retryable", "reconcile: "+err.Error(), "reconcile"), nil
	}
	return failed(p.c, "unknown_outcome", "reconcile: "+err.Error(), "park"), nil
}

// send performs the effect and classifies the result.
func (w *Worker) send(ctx context.Context, p *plan) history.Event {
	in, err := w.resolveSecrets(ctx, p)
	if err != nil {
		return w.secretFailure(p, err)
	}
	if p.stepType == "code" {
		w.scrubCodeSecrets(ctx, p)
		// The sandbox enforces the step's own time limit.
		res, err := w.runCode(ctx, p, in)
		if err != nil {
			return w.classify(p, err)
		}
		raw, _ := json.Marshal(history.CompletedPayload{Output: res.Output, Logs: res.Logs})
		return history.Event{Type: history.StepCompleted, StepID: p.c.step, Attempt: p.c.attempt, Payload: raw}
	}
	if p.stepType == "container" {
		// The runner enforces the step's own time limit, however long; the
		// heartbeat keeps the lease meanwhile.
		return w.containerEvent(ctx, p, in)
	}
	deadline := time.Now().Add(w.CallTimeout)
	if p.class.IsWrite() {
		deadline = p.intentAt.Add(w.CallTimeout)
		if !time.Now().Before(deadline) {
			return w.classify(p, fmt.Errorf("call deadline passed before sending: %w", effects.ErrNotSent))
		}
	}
	ctx, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()
	var out any
	switch {
	case p.conn != nil:
		input, _ := in.(map[string]any)
		if input == nil {
			input = map[string]any{}
		}
		if p.class.IsWrite() && p.field != "" && !strings.HasPrefix(p.field, "header:") {
			input[p.field] = p.key
		}
		creds, err := w.credentials(ctx, p)
		if err != nil {
			return w.secretFailure(p, err)
		}
		hc, err := w.client(ctx, p, w.CallTimeout)
		if err != nil {
			return w.secretFailure(p, err)
		}
		dial, err := w.dialer(ctx, p, creds)
		if err != nil {
			return w.secretFailure(p, err)
		}
		var resp connector.Response
		resp, err = p.conn.Actions[p.action].Execute(ctx, connector.Request{Input: input, Credentials: creds, IdempotencyKey: p.key, KeyFirstSent: p.keyFirst, Attempt: p.c.attempt, Logger: w.Logger, HTTP: hc, Dial: dial})
		if err != nil {
			return w.classify(p, err)
		}
		out = resp.Output
		p.drift = drift.Check(p.conn.Manifest, p.action, out)
	default:
		out, err = w.doHTTP(ctx, p, in)
		if err != nil {
			return w.classify(p, err)
		}
	}
	return completedEvent(p.c, out, false)
}

// KindKeyUnavailable is the failure kind of a step parked because the
// tenant's key could not be unwrapped (decision 0019, docs/byok.md). The
// key job resumes it once the key works: as KindKeyRestored (nothing was
// sent: retry) or KindKeyRestoredReconcile (an earlier attempt may have
// been: reconcile first). Neither spends the step's retry budget.
const (
	KindKeyUnavailable       = history.KindKeyUnavailable
	KindKeyRestored          = history.KindKeyRestored
	KindKeyRestoredReconcile = history.KindKeyRestoredReconcile
)

// keyParked is the failure for a step that could not get its tenant key:
// it parks rather than failing the run, and nothing is lost.
func keyParked(p *plan, err error) (history.Event, bool) {
	if err == nil || !errors.Is(err, secrets.ErrKeyUnavailable) {
		return history.Event{}, false
	}
	return failed(p.c, KindKeyUnavailable, "the tenant's encryption key is unavailable (a customer key revoked, disabled or unreachable, or the KMS down); "+
		"the step waits and resumes when it works again: "+err.Error(), "park"), true
}

// secretFailure is the failure for a step whose secrets or credentials
// could not be read before anything was sent.
func (w *Worker) secretFailure(p *plan, err error) history.Event {
	if ev, ok := keyParked(p, err); ok {
		return ev
	}
	return failed(p.c, "fatal", err.Error(), "fail")
}

func (w *Worker) classify(p *plan, err error) history.Event {
	if ev, ok := keyParked(p, err); ok {
		return ev // code and container steps read their secrets before running
	}
	kind := effects.Classify(err)
	next := map[effects.Next]string{effects.Retry: "retry", effects.Reconcile: "reconcile", effects.Park: "park", effects.Fail: "fail"}[effects.AfterError(p.class, kind)]
	e := history.Error{Kind: kind.String(), Message: err.Error(), Next: next, MaybeApplied: p.class.MayHaveApplied(kind)}
	// A provider that said when to come back (Retry-After) is not asked sooner.
	var ra interface{ RetryAfterDelay() time.Duration }
	if errors.As(err, &ra) && kind == effects.KindRetryable {
		e.RetryAfterMS = ra.RetryAfterDelay().Milliseconds()
	}
	raw, _ := json.Marshal(history.FailedPayload{Error: e})
	return history.Event{Type: history.StepFailed, StepID: p.c.step, Attempt: p.c.attempt, Payload: raw}
}

// resolveSecrets evaluates the secret-only expressions decide left in the input.
func (w *Worker) resolveSecrets(ctx context.Context, p *plan) (any, error) {
	names := map[string]bool{}
	collectSecrets(p.sched.Input, names)
	if len(names) == 0 {
		return p.sched.Input, nil
	}
	vals := map[string]any{}
	for n := range names {
		v, err := w.secret(ctx, p, n)
		if err != nil {
			return nil, err
		}
		vals[n] = v
		p.scrub = append(p.scrub, v)
	}
	return exprEngine.Resolve(p.sched.Input, map[string]any{"secrets": vals}, false)
}

func collectSecrets(v any, into map[string]bool) {
	switch t := v.(type) {
	case string:
		if expr.IsExpr(t) {
			if r, err := exprEngine.References(t); err == nil {
				for n := range r.Secrets {
					into[n] = true
				}
			}
		}
	case map[string]any:
		for _, c := range t {
			collectSecrets(c, into)
		}
	case []any:
		for _, c := range t {
			collectSecrets(c, into)
		}
	}
}

// credentials loads the step's connection credentials and checks the
// manifest's required fields are present.
func (w *Worker) credentials(ctx context.Context, p *plan) (map[string]string, error) {
	if p.conn == nil || p.conn.Manifest.Auth.Type == "none" {
		return map[string]string{}, nil
	}
	if p.creds != nil {
		return p.creds, nil // read once per attempt: one recorded read
	}
	if w.Connections == nil {
		return nil, fmt.Errorf("no connection store configured")
	}
	creds, err := w.Connections.Credentials(secrets.WithUse(ctx, p.use(secrets.KindConnection)), p.c.tenant, p.env, p.conn.Manifest.ID, p.sched.Connection)
	if err != nil {
		return nil, err
	}
	declared := map[string]bool{}
	for _, f := range p.conn.Manifest.Auth.Fields {
		if creds[f.Key] == "" && (f.Required == nil || *f.Required) {
			return nil, fmt.Errorf("connection is missing %q", f.Key)
		}
		declared[f.Key] = !f.Secret
	}
	for k, v := range creds {
		if public, ok := declared[k]; !ok || !public {
			p.scrub = append(p.scrub, v) // secret fields, and tokens the manifest does not list
		}
	}
	p.creds = creds
	return creds, nil
}

// policy is the egress policy for this task: a connector may reach only its
// manifest's hosts; an http step or sandbox fetch only the tenant's
// allow-list for the environment.
func (w *Worker) policy(ctx context.Context, p *plan, creds map[string]string) (egress.Policy, error) {
	pol := egress.Policy{Tenant: p.c.tenant.String()}
	if p.conn != nil {
		pol.Purpose = "connector:" + p.conn.Manifest.ID
		pol.Hosts = p.conn.Manifest.HostsFor(creds)
		return pol, nil
	}
	hosts, err := w.Store.EgressHosts(ctx, p.c.tenant, p.env)
	if err != nil {
		return pol, err
	}
	pol.Hosts, pol.Purpose = hosts, p.stepType+"_step"
	return pol, nil
}

// client returns an egress-guarded HTTP client for this task.
func (w *Worker) client(ctx context.Context, p *plan, timeout time.Duration) (*http.Client, error) {
	creds := map[string]string{}
	if p.conn != nil {
		var err error
		if creds, err = w.credentials(ctx, p); err != nil {
			return nil, err
		}
	}
	pol, err := w.policy(ctx, p, creds)
	if err != nil {
		return nil, err
	}
	return w.Egress.Client(pol, timeout), nil
}

// dialer returns an egress-guarded dial function for this task.
func (w *Worker) dialer(ctx context.Context, p *plan, creds map[string]string) (func(context.Context, string, string) (net.Conn, error), error) {
	pol, err := w.policy(ctx, p, creds)
	if err != nil {
		return nil, err
	}
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		return w.Egress.DialContext(ctx, pol, network, addr)
	}, nil
}

// heartbeat extends the lease while the task runs and cancels the task's
// context if the lease is lost.
func (w *Worker) heartbeat(ctx context.Context, c claim, lost context.CancelFunc) func() {
	done := make(chan struct{})
	go func() {
		t := time.NewTicker(w.Lease / 3)
		defer t.Stop()
		for {
			select {
			case <-done:
				return
			case <-ctx.Done():
				return
			case <-t.C:
				err := db.InTenantTx(ctx, w.Store.Pool, []uuid.UUID{c.tenant}, func(tx pgx.Tx) error { return w.fence(ctx, tx, c) })
				if errors.Is(err, errFenced) {
					lost()
					return
				}
			}
		}
	}()
	return func() { close(done) }
}
