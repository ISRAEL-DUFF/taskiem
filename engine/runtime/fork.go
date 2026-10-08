package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/decide"
	"github.com/israel-duff/taskiem/engine/history"
)

// Fork from step (spec 4.10, decision 0016). A failed run is resumed as a
// new run, on the version given (usually a repaired one), linked to the
// run it continues. The new run's orchestrator is the new version's own:
// it schedules steps as usual, and when it schedules a task the parent
// completed, with the same resolved input, the parent's recorded outcome is
// appended instead of a task being queued. Nothing the parent completed is
// executed again: a completed write is never re-sent, and a write whose
// input the new version changed is parked for a person rather than
// replayed or sent. Signals the parent received are delivered again and
// waits the parent finished fire at once; approvals are asked again, since
// the version they approved has changed.
//
// The fork keeps the parent's idempotency seed (spec 4.4: "forks inherit
// the parent run's seed"), so any effect it sends carries the key the
// parent would have used for that step; a step the parent attempted and
// that failed definitively starts a new attempt group, so the provider
// does not answer it from a cached failure.

// Fork names the run a new run continues.
type Fork struct {
	Parent uuid.UUID
}

// NotResumableError says why a run cannot be resumed.
type NotResumableError struct{ Reason string }

func (e *NotResumableError) Error() string { return "the run cannot be resumed: " + e.Reason }

// IsNotResumable reports whether err is a *NotResumableError.
func IsNotResumable(err error) (*NotResumableError, bool) {
	var e *NotResumableError
	ok := errors.As(err, &e)
	return e, ok
}

// forkPlan is what a new run copies from its parent.
type forkPlan struct {
	parent     uuid.UUID
	seed       string
	trigger    any
	env        map[string]any
	failedStep string
	lastSeq    int64
	replays    []replayRow
}

type replayRow struct {
	step, kind string
	payload    json.RawMessage
	digest     string
	write      bool
	group      int
}

type instFacts struct {
	sched     map[int]history.ScheduledPayload
	last      int
	completed json.RawMessage // the raw (sealed) StepCompleted payload
	outcomes  map[int]bool
	intents   map[int]history.IntentPayload
	failures  []history.Error
	signal    json.RawMessage
	waited    bool
}

// planFork reads a failed run and decides what its fork may replay. It
// refuses when resuming could repeat or hide an effect.
func (s *Store) planFork(ctx context.Context, tx pgx.Tx, tenant, parent uuid.UUID) (*forkPlan, error) {
	run, err := lockRun(ctx, tx, parent)
	if err != nil {
		return nil, err
	}
	if run.status != "failed" {
		return nil, &NotResumableError{Reason: fmt.Sprintf("run %s is %s; only failed runs are resumed", parent, run.status)}
	}
	fp := &forkPlan{parent: parent}
	if err := tx.QueryRow(ctx, `SELECT COALESCE(idempotency_seed, id::text) FROM runs WHERE id = $1`, parent).Scan(&fp.seed); err != nil {
		return nil, err
	}
	raw, err := History(ctx, tx, parent)
	if err != nil {
		return nil, err
	}
	opened, _, err := s.openHistory(ctx, tx, tenant, raw)
	if err != nil {
		return nil, err
	}
	facts := map[string]*instFacts{}
	var order []string
	get := func(id string) *instFacts {
		f, ok := facts[id]
		if !ok {
			f = &instFacts{sched: map[int]history.ScheduledPayload{}, outcomes: map[int]bool{}, intents: map[int]history.IntentPayload{}}
			facts[id] = f
			order = append(order, id)
		}
		return f
	}
	for i, e := range opened {
		fp.lastSeq = e.Seq
		switch e.Type {
		case history.RunStarted:
			var p history.RunStartedPayload
			if err := json.Unmarshal(e.Payload, &p); err != nil {
				return nil, err
			}
			fp.trigger, fp.env = p.Trigger, p.Env
			continue
		case history.CompensationStarted, history.CompensationCompleted:
			return nil, &NotResumableError{Reason: "the run compensated its completed steps when it failed; start a new run instead"}
		}
		if e.StepID == "" {
			continue
		}
		if strings.HasPrefix(e.StepID, history.CompensationPrefix) {
			return nil, &NotResumableError{Reason: "the run compensated its completed steps when it failed; start a new run instead"}
		}
		f := get(e.StepID)
		switch e.Type {
		case history.StepScheduled:
			var p history.ScheduledPayload
			if err := json.Unmarshal(e.Payload, &p); err != nil {
				return nil, err
			}
			a := max(e.Attempt, 1)
			f.sched[a] = p
			f.last = max(f.last, a)
		case history.EffectIntent:
			var p history.IntentPayload
			if err := json.Unmarshal(e.Payload, &p); err != nil {
				return nil, err
			}
			f.intents[e.Attempt] = p
		case history.StepCompleted:
			if f.completed == nil {
				f.completed = raw[i].Payload // as recorded: sealed values stay sealed
			}
			f.outcomes[e.Attempt] = true
		case history.StepFailed:
			var p history.FailedPayload
			_ = json.Unmarshal(e.Payload, &p)
			f.failures = append(f.failures, p.Error)
			f.outcomes[e.Attempt] = true
			if p.Error.Next == "fail" && p.Error.Kind != "child_failed" {
				fp.failedStep = e.StepID
			}
		case history.SignalReceived:
			if f.signal == nil {
				f.signal = raw[i].Payload
			}
		case history.TimerFired:
			var p history.TimerPayload
			_ = json.Unmarshal(e.Payload, &p)
			if p.Kind == "wait" {
				f.waited = true
			}
		}
	}
	for _, id := range order {
		f := facts[id]
		for _, e := range f.failures {
			if e.MaybeApplied {
				return nil, &NotResumableError{Reason: fmt.Sprintf("step %s may have taken effect (%s); settle it before resuming", id, e.Kind)}
			}
		}
		write, maxGroup := false, -1
		for a, ip := range f.intents {
			if !f.outcomes[a] {
				return nil, &NotResumableError{Reason: fmt.Sprintf("step %s has a write whose outcome was never recorded; it may still be in flight", id)}
			}
			if ip.Class != "" && ip.Class != "read" {
				write = true
			}
			maxGroup = max(maxGroup, ip.AttemptGroup)
		}
		p, scheduled := f.sched[f.last]
		switch {
		case f.completed != nil && scheduled && p.Kind == history.KindTask:
			fp.replays = append(fp.replays, replayRow{step: id, kind: "task", payload: f.completed, digest: history.InputDigest(p.Input), write: write})
		case f.signal != nil:
			fp.replays = append(fp.replays, replayRow{step: id, kind: "signal", payload: f.signal})
		case f.waited && scheduled && p.Kind == history.KindTimer:
			fp.replays = append(fp.replays, replayRow{step: id, kind: "timer"})
		case f.completed == nil && maxGroup >= 0:
			// Attempted and failed for good: a new key for the next attempt.
			fp.replays = append(fp.replays, replayRow{step: id, kind: "key_group", group: maxGroup + 1})
		}
	}
	return fp, nil
}

// recordFork links a new run to its parent and stores what it replays.
func recordFork(ctx context.Context, tx pgx.Tx, tenant, run uuid.UUID, fp *forkPlan) error {
	if _, err := tx.Exec(ctx, `UPDATE runs SET parent_run_id = $2, forked_from_seq = $3, idempotency_seed = $4, resumed_step = NULLIF($5, '') WHERE id = $1`,
		run, fp.parent, fp.lastSeq, fp.seed, fp.failedStep); err != nil {
		return err
	}
	for _, r := range fp.replays {
		var payload any
		if r.payload != nil {
			payload = r.payload
		}
		if _, err := tx.Exec(ctx, `INSERT INTO run_replays (tenant_id, run_id, step_id, kind, payload, input_digest, write, attempt_group)
			VALUES ($1, $2, $3, $4, $5, NULLIF($6, ''), $7, $8)`, tenant, run, r.step, r.kind, payload, r.digest, r.write, r.group); err != nil {
			return err
		}
	}
	return nil
}

// takeReplay claims the replay recorded for a step instance, once.
func takeReplay(ctx context.Context, tx pgx.Tx, run uuid.UUID, step, kind string) (payload []byte, digest string, write, ok bool, err error) {
	var d *string
	err = tx.QueryRow(ctx, `UPDATE run_replays SET used_at = now() WHERE run_id = $1 AND step_id = $2 AND kind = $3 AND used_at IS NULL
		RETURNING payload, input_digest, write`, run, step, kind).Scan(&payload, &d, &write)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, "", false, false, nil
	}
	if err != nil {
		return nil, "", false, false, err
	}
	if d != nil {
		digest = *d
	}
	return payload, digest, write, true, nil
}

// replayTask answers a scheduled task of a forked run from its parent's
// record. It reports whether it did (the task is then not queued).
func (s *Store) replayTask(ctx context.Context, tx pgx.Tx, run runRow, ev decide.NewEvent, p history.ScheduledPayload) (func() error, bool, error) {
	payload, digest, write, ok, err := takeReplay(ctx, tx, run.ref.ID, ev.StepID, "task")
	if err != nil || !ok {
		return nil, false, err
	}
	if digest == history.InputDigest(p.Input) {
		var m map[string]any
		if err := json.Unmarshal(payload, &m); err != nil {
			return nil, false, err
		}
		var parent uuid.UUID
		if err := tx.QueryRow(ctx, `SELECT parent_run_id FROM runs WHERE id = $1`, run.ref.ID).Scan(&parent); err != nil {
			return nil, false, err
		}
		m["replayed_from"] = parent.String()
		return func() error {
			_, err := appendEvent(ctx, tx, run.ref.ID, history.StepCompleted, ev.StepID, ev.Attempt, m, history.OriginWorker)
			return err
		}, true, nil
	}
	if !write {
		return nil, false, nil // a read with another input: execute it
	}
	// A write the parent completed, with another input now: sending it could
	// pay twice, replaying it would pretend. A person decides.
	msg := "the parent run completed this write with a different input; check with the provider, then resolve the step"
	return func() error {
		if _, err := appendEvent(ctx, tx, run.ref.ID, history.StepFailed, ev.StepID, ev.Attempt,
			history.FailedPayload{Error: history.Error{Kind: "fork_mismatch", Message: msg, Next: "park", MaybeApplied: true}}, history.OriginWorker); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `UPDATE runs SET status = 'needs_reconciliation' WHERE id = $1`, run.ref.ID)
		return err
	}, true, nil
}

// replaySignal delivers a signal the parent received.
func (s *Store) replaySignal(ctx context.Context, tx pgx.Tx, run runRow, ev decide.NewEvent) (func() error, bool, error) {
	payload, _, _, ok, err := takeReplay(ctx, tx, run.ref.ID, ev.StepID, "signal")
	if err != nil || !ok {
		return nil, false, err
	}
	return func() error {
		_, err := appendEvent(ctx, tx, run.ref.ID, history.SignalReceived, ev.StepID, 0, json.RawMessage(payload), history.OriginSignal)
		return err
	}, true, nil
}

// replayTimer reports whether a wait the parent finished should fire now.
func (s *Store) replayTimer(ctx context.Context, tx pgx.Tx, run runRow, ev decide.NewEvent) (bool, error) {
	_, _, _, ok, err := takeReplay(ctx, tx, run.ref.ID, ev.StepID, "timer")
	return ok, err
}

// forkKeyGroup is the first attempt group a forked run uses for a step its
// parent attempted (0 when it did not).
func forkKeyGroup(ctx context.Context, tx pgx.Tx, run uuid.UUID, step string) (int, error) {
	var g int
	err := tx.QueryRow(ctx, `SELECT attempt_group FROM run_replays WHERE run_id = $1 AND step_id = $2 AND kind = 'key_group'`, run, step).Scan(&g)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, nil
	}
	return g, err
}

// ForkInfo describes a run's fork link.
type ForkInfo struct {
	Parent      *uuid.UUID  `json:"parent_run_id"`
	ResumedStep *string     `json:"resumed_step"`
	Children    []uuid.UUID `json:"resumed_by"`
}

// Forks reports what a run continues and what continues it.
func (s *Store) Forks(ctx context.Context, ref RunRef) (ForkInfo, error) {
	var out ForkInfo
	err := dbTx(ctx, s, ref.TenantID, func(tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `SELECT parent_run_id, resumed_step FROM runs WHERE id = $1`, ref.ID).Scan(&out.Parent, &out.ResumedStep); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `SELECT id FROM runs WHERE parent_run_id = $1 ORDER BY started_at`, ref.ID)
		if err != nil {
			return err
		}
		out.Children, err = pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
		return err
	})
	return out, err
}
