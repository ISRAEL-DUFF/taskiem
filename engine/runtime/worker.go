package runtime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/connector"
	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/effects"
	"github.com/israel-duff/taskiem/engine/expr"
	"github.com/israel-duff/taskiem/engine/history"
)

var exprEngine = expr.MustNew()

// Secrets resolves secret values for a tenant and environment at execution
// time. Values never enter history (spec 3.3).
type Secrets interface {
	Get(ctx context.Context, tenant uuid.UUID, environment, name string) (string, error)
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

// Hooks inject faults for the crash-recovery suite. A hook returning an
// error makes the worker abandon the task at that point, as if the process
// died: nothing more is written and the lease is left to expire.
type Hooks struct {
	AfterIntent func(inst string, attempt int) error
	AfterCall   func(inst string, attempt int) error
}

// Worker claims tasks from one queue and executes them.
type Worker struct {
	Store       *Store
	Registry    *connector.Registry
	Secrets     Secrets
	HTTP        *http.Client
	ID          string
	Queue       string
	Concurrency int
	Lease       time.Duration
	Hooks       *Hooks
	Logger      *slog.Logger
}

func (w *Worker) defaults() {
	if w.Queue == "" {
		w.Queue = "connector"
	}
	if w.Concurrency <= 0 {
		w.Concurrency = 16
	}
	if w.Lease <= 0 {
		w.Lease = 60 * time.Second
	}
	if w.HTTP == nil {
		w.HTTP = &http.Client{Timeout: 30 * time.Second}
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
	wake, stop, err := listen(ctx, w.Store, "taskiem_tasks")
	if err != nil {
		return err
	}
	defer stop()
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
				if err := w.execute(ctx, c); err != nil && ctx.Err() == nil {
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
	rows, err := w.Store.Pool.Query(ctx, `SELECT task_id, tenant_id, run_id, step_id, attempt, lease_epoch FROM taskiem_claim_tasks($1, $2, $3, $4::interval)`,
		w.Queue, w.ID, n, w.Lease.String())
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

// errFenced means this worker no longer holds the task's lease.
var errFenced = errors.New("fenced: lease lost")

// errAbandoned is a simulated crash from a hook.
var errAbandoned = errors.New("abandoned")

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
}

// execute runs one claimed task: prepare (and record intent), call, record
// the outcome, decide inline.
func (w *Worker) execute(ctx context.Context, c claim) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	stopBeat := w.heartbeat(ctx, c, cancel)
	defer stopBeat()

	p, err := w.prepare(ctx, c)
	if err != nil {
		if errors.Is(err, errFenced) {
			return nil
		}
		return err
	}
	if p.mode == modeSend && p.class.IsWrite() && w.Hooks != nil && w.Hooks.AfterIntent != nil {
		if err := w.Hooks.AfterIntent(c.step, c.attempt); err != nil {
			return nil
		}
	}

	var result history.Event
	switch p.mode {
	case modeDone:
		return w.finish(ctx, p, nil)
	case modePark:
		result = failed(c, "unknown_outcome", "an earlier attempt may have reached the provider and the action cannot be safely retried", "park")
	case modeReconcile:
		result, err = w.reconcile(ctx, p)
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
		if err := tx.QueryRow(ctx, `SELECT workflow_id, version, environment FROM runs WHERE id = $1`, c.run).Scan(&wfID, &version, &p.env); err != nil {
			return err
		}
		def, err := w.Store.definition(ctx, tx, wfID, version)
		if err != nil {
			return err
		}
		hist, err := History(ctx, tx, c.run)
		if err != nil {
			return err
		}
		found := false
		var intents []history.IntentPayload
		var intentAttempts []int
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
			}
		}
		if !found {
			return fmt.Errorf("no StepScheduled for %s attempt %d", c.step, c.attempt)
		}
		if p.mode == modeDone {
			return nil
		}
		_, id := history.SplitInstance(c.step)
		if s := def.Step(id); s != nil {
			p.stepType = s.Type
		}
		if err := w.resolveExecutor(p); err != nil {
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
		}
		p.keyIn = effects.KeyInput{TenantID: c.tenant.String(), Seed: seed, StepID: p.sched.KeyStep}
		if n := len(intents); n > 0 {
			last, lastAttempt := intents[n-1], intentAttempts[n-1]
			p.group = last.AttemptGroup
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
	}
	return effects.KindUnknownOutcome
}

// defaultHTTPKey is how an http step's idempotency key is encoded.
var defaultHTTPKey = effects.Spec{Encoding: effects.Base32Lower, Length: 32, Prefix: "tsk_"}

func (w *Worker) resolveExecutor(p *plan) error {
	switch {
	case p.sched.Connector != "":
		if w.Registry == nil {
			return fmt.Errorf("no connector registry")
		}
		conn, ok := w.Registry.Get(p.sched.Connector)
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
	digest := sha256.New()
	_ = json.NewEncoder(digest).Encode(p.sched.Input)
	payload := history.IntentPayload{Key: p.key, Seed: p.keyIn.Seed, KeyStep: p.keyIn.StepID, AttemptGroup: p.group,
		Class: string(p.class), Digest: hex.EncodeToString(digest.Sum(nil))}
	if _, err := appendEvent(ctx, tx, p.c.run, history.StepStarted, p.c.step, p.c.attempt, map[string]any{"worker": w.ID}, history.OriginWorker); err != nil {
		return err
	}
	_, err := appendEvent(ctx, tx, p.c.run, history.EffectIntent, p.c.step, p.c.attempt, payload, history.OriginWorker)
	return err
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

// finish releases the task and records the outcome, if any, under the fence.
func (w *Worker) finish(ctx context.Context, p *plan, result *history.Event) error {
	return db.InTenantTx(ctx, w.Store.Pool, []uuid.UUID{p.c.tenant}, func(tx pgx.Tx) error {
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
		if _, err := appendEvent(ctx, tx, p.c.run, result.Type, result.StepID, result.Attempt, json.RawMessage(result.Payload), history.OriginWorker); err != nil {
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
	field := p.field
	if field == "" || strings.HasPrefix(field, "header:") {
		field = "reference"
	}
	creds, err := w.credentials(ctx, p)
	if err != nil {
		return failed(p.c, "fatal", err.Error(), "fail"), nil
	}
	resp, err := action.Execute(ctx, connector.Request{Input: map[string]any{field: p.key}, Credentials: creds, IdempotencyKey: p.key, Attempt: p.c.attempt, Logger: w.Logger, HTTP: w.HTTP})
	switch {
	case err == nil:
		return completedEvent(p.c, resp.Output, true), nil
	case errors.Is(err, connector.ErrNotFound):
		// Safe to send again, under a new key (contract: idempotency.md).
		p.group++
		p.keyIn.AttemptGroup = p.group
		if p.key, err = p.spec.Key(p.keyIn); err != nil {
			return history.Event{}, err
		}
		if err := db.InTenantTx(ctx, w.Store.Pool, []uuid.UUID{p.c.tenant}, func(tx pgx.Tx) error { return w.recordIntent(ctx, tx, p) }); err != nil {
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
		return failed(p.c, "fatal", err.Error(), "fail")
	}
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
			return failed(p.c, "fatal", err.Error(), "fail")
		}
		var resp connector.Response
		resp, err = p.conn.Actions[p.action].Execute(ctx, connector.Request{Input: input, Credentials: creds, IdempotencyKey: p.key, Attempt: p.c.attempt, Logger: w.Logger, HTTP: w.HTTP})
		if err != nil {
			return w.classify(p, err)
		}
		out = resp.Output
	default:
		out, err = w.doHTTP(ctx, p, in)
		if err != nil {
			return w.classify(p, err)
		}
	}
	return completedEvent(p.c, out, false)
}

func (w *Worker) classify(p *plan, err error) history.Event {
	kind := effects.Classify(err)
	next := map[effects.Next]string{effects.Retry: "retry", effects.Reconcile: "reconcile", effects.Park: "park", effects.Fail: "fail"}[effects.AfterError(p.class, kind)]
	return failed(p.c, kind.String(), err.Error(), next)
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
		v, err := w.Secrets.Get(ctx, p.c.tenant, p.env, n)
		if err != nil {
			return nil, err
		}
		vals[n] = v
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

// credentials loads a connection's credential fields as secrets named
// "<connector>.<connection>.<field>".
func (w *Worker) credentials(ctx context.Context, p *plan) (map[string]string, error) {
	out := map[string]string{}
	if p.conn == nil {
		return out, nil
	}
	for _, f := range p.conn.Manifest.Auth.Fields {
		v, err := w.Secrets.Get(ctx, p.c.tenant, p.env, p.conn.Manifest.ID+".default."+f.Key)
		if err != nil {
			if f.Required != nil && !*f.Required {
				continue
			}
			return nil, err
		}
		out[f.Key] = v
	}
	return out, nil
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
