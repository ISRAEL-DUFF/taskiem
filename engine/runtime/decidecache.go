package runtime

import (
	"context"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/decide"
	"github.com/israel-duff/taskiem/engine/history"
	"github.com/israel-duff/taskiem/engine/pii"
)

// decideCacheSize bounds how many runs' folded histories an orchestrator
// keeps. A run not in the cache is folded from its full history, as before.
const decideCacheSize = 2048

// folded is a run's history folded up to State.Seq(), with the personal
// data it opened. It is cached while the deciding transaction is still
// open, and may hold events that transaction wrote (a worker appends a
// step's outcome, then decides, in one transaction). If that transaction
// does not commit, those events never existed, and another may later be
// written at the same seq; so before a cached State is used, the event it
// was last folded to is checked against the database (last), and a State
// whose last event is not there is folded again from scratch.
type folded struct {
	workflowID uuid.UUID
	version    int
	erasedAt   *time.Time // the tenant's latest erasure when folded
	state      *decide.State
	taint      pii.Taint
	last       history.Event // the last event folded, as stored (sealed)
	used       uint64
}

// sameEvent reports whether a stored event is the one a State was folded
// to: the same seq, kind and step, recorded by the same transaction.
func sameEvent(a, b history.Event) bool {
	return a.Seq == b.Seq && a.Type == b.Type && a.StepID == b.StepID && a.Attempt == b.Attempt && a.RecordedAt.Equal(b.RecordedAt)
}

type decideCache struct {
	mu   sync.Mutex
	runs map[uuid.UUID]*folded
	tick uint64
}

// take removes and returns a run's entry: a run is decided by one
// transaction at a time (it holds the run's row lock).
func (c *decideCache) take(run uuid.UUID) *folded {
	c.mu.Lock()
	defer c.mu.Unlock()
	f := c.runs[run]
	delete(c.runs, run)
	return f
}

func (c *decideCache) put(run uuid.UUID, f *folded) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.runs == nil {
		c.runs = map[uuid.UUID]*folded{}
	}
	if len(c.runs) >= decideCacheSize {
		var oldest uuid.UUID
		var min uint64
		for id, e := range c.runs {
			if min == 0 || e.used < min {
				oldest, min = id, e.used
			}
		}
		delete(c.runs, oldest)
	}
	c.tick++
	f.used = c.tick
	c.runs[run] = f
}

func (c *decideCache) drop(run uuid.UUID) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.runs, run)
}

// latestErasure is when the tenant last erased a subject: folded history
// holds opened personal data, so an erasure since makes it stale.
func latestErasure(ctx context.Context, tx pgx.Tx, tenant uuid.UUID) (*time.Time, error) {
	var at *time.Time
	err := tx.QueryRow(ctx, `SELECT max(shredded_at) FROM subject_keys WHERE tenant_id = $1 AND shredded_at IS NOT NULL`, tenant).Scan(&at)
	return at, err
}

// HistoryAfter loads a run's events after seq, in order.
func HistoryAfter(ctx context.Context, tx pgx.Tx, runID uuid.UUID, seq int64) ([]history.Event, error) {
	return queryHistory(ctx, tx, `SELECT seq, type, COALESCE(step_id, ''), COALESCE(attempt, 0), payload, recorded_at, origin
		FROM run_events WHERE run_id = $1 AND seq > $2 ORDER BY seq`, runID, seq)
}

// foldRun returns the run's history folded to its latest event, from the
// cache when it is still good, and caches a copy for the next decision.
func (s *Store) foldRun(ctx context.Context, tx pgx.Tx, run runRow) (*decide.State, pii.Taint, error) {
	erased, err := latestErasure(ctx, tx, run.ref.TenantID)
	if err != nil {
		return nil, nil, err
	}
	def, err := s.runDefinition(ctx, tx, run.workflowID, run.version, run.expansion)
	if err != nil {
		return nil, nil, err
	}
	f := s.folds.take(run.ref.ID)
	if f != nil && (f.workflowID != run.workflowID || f.version != run.version || !sameTime(f.erasedAt, erased)) {
		f = nil
	}
	if f != nil {
		// From the last event folded, to check that it is still there.
		raw, err := HistoryAfter(ctx, tx, run.ref.ID, f.state.Seq()-1)
		if err != nil {
			return nil, nil, err
		}
		if len(raw) == 0 || !sameEvent(raw[0], f.last) {
			f = nil // folded from a transaction that did not commit
		} else if raw = raw[1:]; len(raw) > 0 {
			hist, _, err := s.openHistoryInto(ctx, tx, run.ref.TenantID, raw, f.taint)
			if err == nil {
				err = f.state.Extend(hist)
			}
			if err != nil {
				f = nil // a gap (history purged and restarted) or bad event: fold from scratch
			} else {
				f.last = raw[len(raw)-1]
			}
		}
	}
	if f == nil {
		raw, err := History(ctx, tx, run.ref.ID)
		if err != nil {
			return nil, nil, err
		}
		hist, taint, err := s.openHistory(ctx, tx, run.ref.TenantID, raw)
		if err != nil {
			return nil, nil, err
		}
		st, err := decide.Fold(def, hist)
		if err != nil {
			return nil, nil, err
		}
		f = &folded{workflowID: run.workflowID, version: run.version, erasedAt: erased, state: st, taint: taint}
		if len(raw) > 0 {
			f.last = raw[len(raw)-1]
		}
	}
	s.folds.put(run.ref.ID, f)
	return f.state.Clone(), cloneTaint(f.taint), nil
}

func sameTime(a, b *time.Time) bool {
	if a == nil || b == nil {
		return a == b
	}
	return a.Equal(*b)
}

func cloneTaint(t pii.Taint) pii.Taint {
	c := make(pii.Taint, len(t))
	for k, v := range t {
		c[k] = v
	}
	return c
}
