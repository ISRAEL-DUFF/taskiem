package spike

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/israel-duff/taskiem/engine/db"
)

// Engine runs orchestrator and worker loops against one database.
type Engine struct {
	Pool    *pgxpool.Pool // connects as taskiem_app
	Steps   []string
	Queue   string
	Batch   int
	Latency *Recorder // queue-to-worker dispatch latency
	Decide  *Recorder // StepCompleted committed -> next StepScheduled committed
	Fenced  *Counter
	// Inline runs decide() inside the worker's completion transaction, as
	// ingest already does for RunStarted (spec 2.2). Orchestrators then only
	// sweep runs left undecided.
	Inline bool
}

var errFenced = errors.New("fenced")

// Workers runs one claimer and n executors, the shape a worker process will
// have: a single LISTEN connection and claimer per process avoids waking
// every executor on every notification (a thundering herd measured in
// RESULTS.md). The claimer only claims as many tasks as executors are free.
func (e *Engine) Workers(ctx context.Context, proc string, n int) error {
	wake, stop, err := listen(ctx, e.Pool, "taskiem_tasks")
	if err != nil {
		return err
	}
	defer stop()
	work := make(chan claimed, n)
	free := make(chan struct{}, n)
	for i := 0; i < n; i++ {
		free <- struct{}{}
	}
	errs := make(chan error, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for c := range work {
				if err := e.execute(ctx, proc, c); err != nil && ctx.Err() == nil {
					errs <- err
				}
				free <- struct{}{}
			}
		}()
	}
	defer func() { close(work); wg.Wait() }()
	for ctx.Err() == nil {
		select {
		case err := <-errs:
			return err
		default:
		}
		// Wait for at least one free executor, then count the rest.
		select {
		case <-free:
		case <-ctx.Done():
			return nil
		}
		slots := 1
		for slots < e.Batch {
			select {
			case <-free:
				slots++
				continue
			default:
			}
			break
		}
		claims, err := e.claimTasks(ctx, proc, slots)
		if err != nil {
			return err
		}
		at := time.Now()
		for _, c := range claims {
			work <- claimed{c, at}
		}
		for i := len(claims); i < slots; i++ {
			free <- struct{}{}
		}
		if len(claims) < slots {
			waitFor(ctx, wake, time.Second) // queue drained: sleep until notified
		}
	}
	return nil
}

type claimed struct {
	claim
	at time.Time
}

func (e *Engine) execute(ctx context.Context, worker string, c claimed) error {
	err := db.InTenantTx(ctx, e.Pool, []uuid.UUID{c.tenant}, func(tx pgx.Tx) error {
		var created time.Time
		if err := tx.QueryRow(ctx, `SELECT created_at FROM tasks WHERE id = $1`, c.task).Scan(&created); err != nil {
			return err
		}
		e.Latency.Add(c.at.Sub(created))
		var ok bool
		if err := tx.QueryRow(ctx, `SELECT taskiem_task_finish($1, $2, $3)`, c.task, worker, c.epoch).Scan(&ok); err != nil {
			return err
		}
		if !ok {
			return errFenced
		}
		if _, err := tx.Exec(ctx, `SELECT taskiem_append_event($1, 'StepCompleted', $2, $3, '{"ok":true}')`, c.run, c.step, c.attempt); err != nil {
			return err
		}
		if e.Inline {
			return e.decideRun(ctx, tx, c.run, c.tenant)
		}
		_, err := tx.Exec(ctx, `SELECT pg_notify('taskiem_runs', '')`)
		return err
	})
	if errors.Is(err, errFenced) {
		e.Fenced.Inc()
		return nil
	}
	return err
}

type claim struct {
	task, tenant, run uuid.UUID
	step              string
	attempt           int
	epoch             int64
}

func (e *Engine) claimTasks(ctx context.Context, worker string, n int) ([]claim, error) {
	rows, err := e.Pool.Query(ctx, `SELECT task_id, tenant_id, run_id, step_id, attempt, lease_epoch FROM taskiem_claim_tasks($1, $2, $3)`, e.Queue, worker, n)
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

// Orchestrators runs one claimer and n deciders, mirroring Workers.
func (e *Engine) Orchestrators(ctx context.Context, proc string, n int) error {
	wake, stop, err := listen(ctx, e.Pool, "taskiem_runs")
	if err != nil {
		return err
	}
	defer stop()
	type rt struct{ run, tenant uuid.UUID }
	work := make(chan rt, n)
	errs := make(chan error, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for r := range work {
				if err := db.InTenantTx(ctx, e.Pool, []uuid.UUID{r.tenant}, func(tx pgx.Tx) error {
					return e.decideRun(ctx, tx, r.run, r.tenant)
				}); err != nil && ctx.Err() == nil {
					errs <- err
				}
			}
		}()
	}
	defer func() { close(work); wg.Wait() }()
	limit := n * 2
	for ctx.Err() == nil {
		select {
		case err := <-errs:
			return fmt.Errorf("orchestrator %s: %w", proc, err)
		default:
		}
		rows, err := e.Pool.Query(ctx, `SELECT run_id, tenant_id FROM taskiem_claim_runs_to_orchestrate($1, $2)`, proc, limit)
		if err != nil {
			return err
		}
		got := 0
		for rows.Next() {
			var r rt
			if err := rows.Scan(&r.run, &r.tenant); err != nil {
				return err
			}
			work <- r
			got++
		}
		if err := rows.Err(); err != nil {
			return err
		}
		if got < limit {
			waitFor(ctx, wake, time.Second)
		}
	}
	return nil
}

func (e *Engine) decideRun(ctx context.Context, tx pgx.Tx, run, tenant uuid.UUID) error {
	var last, decided int64
	if err := tx.QueryRow(ctx, `SELECT last_seq, decided_seq FROM runs WHERE id = $1 FOR UPDATE`, run).Scan(&last, &decided); err != nil {
		return err
	}
	if decided >= last {
		_, err := tx.Exec(ctx, `UPDATE runs SET orch_lease_owner = NULL, orch_lease_until = NULL WHERE id = $1`, run)
		return err
	}
	rows, err := tx.Query(ctx, `SELECT seq, type, COALESCE(step_id, ''), recorded_at FROM run_events WHERE run_id = $1 ORDER BY seq`, run)
	if err != nil {
		return err
	}
	var hist []Event
	var lastCompleted time.Time
	for rows.Next() {
		var ev Event
		var at time.Time
		if err := rows.Scan(&ev.Seq, &ev.Type, &ev.StepID, &at); err != nil {
			return err
		}
		if ev.Type == "StepCompleted" {
			lastCompleted = at
		}
		hist = append(hist, ev)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for _, cmd := range Decide(e.Steps, hist) {
		switch cmd.Kind {
		case "schedule":
			if _, err := tx.Exec(ctx, `SELECT taskiem_append_event($1, 'StepScheduled', $2, 1, NULL)`, run, cmd.StepID); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `INSERT INTO tasks (id, tenant_id, run_id, step_id, attempt, queue) VALUES ($1, $2, $3, $4, 1, $5)`,
				uuid.Must(uuid.NewV7()), tenant, run, cmd.StepID, e.Queue); err != nil {
				return err
			}
			if !lastCompleted.IsZero() {
				e.Decide.Add(time.Since(lastCompleted))
			}
		case "complete":
			if _, err := tx.Exec(ctx, `SELECT taskiem_append_event($1, 'RunCompleted', NULL, NULL, NULL)`, run); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `UPDATE runs SET status = 'completed', ended_at = now() WHERE id = $1`, run); err != nil {
				return err
			}
		}
	}
	_, err = tx.Exec(ctx, `UPDATE runs SET decided_seq = last_seq, orch_lease_owner = NULL, orch_lease_until = NULL WHERE id = $1`, run)
	return err
}

// listen holds a dedicated connection on LISTEN channel and signals wake.
func listen(ctx context.Context, pool *pgxpool.Pool, channel string) (<-chan struct{}, func(), error) {
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return nil, nil, err
	}
	if _, err := conn.Exec(ctx, "LISTEN "+channel); err != nil {
		conn.Release()
		return nil, nil, err
	}
	wake := make(chan struct{}, 1)
	lctx, cancel := context.WithCancel(ctx)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			if _, err := conn.Conn().WaitForNotification(lctx); err != nil {
				return
			}
			select {
			case wake <- struct{}{}:
			default:
			}
		}
	}()
	return wake, func() { cancel(); wg.Wait(); conn.Hijack().Close(context.Background()) }, nil
}

func waitFor(ctx context.Context, wake <-chan struct{}, d time.Duration) {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-wake:
	case <-t.C:
	case <-ctx.Done():
	}
}
