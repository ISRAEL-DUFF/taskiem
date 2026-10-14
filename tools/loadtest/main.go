// Command loadtest drives the real engine (engine/runtime) the way
// production does — StartRun with its first decision, one-claimer workers,
// decide inline in every completion, the scheduler's sweep — against a
// fresh database, with a no-op connector action standing in for providers.
// It measures what gate G1 asks: sustained steps per second and p95
// dispatch latency (from the event that scheduled a step to the moment a
// worker starts executing it).
//
//	go run ./tools/loadtest -rate 500 -duration 60s
//
// With -mode cluster it instead runs the built binary's roles as separate
// processes (api, edge, orchestrator, scheduler, workers), sends webhooks
// at a series of rates through the edge, and reads the SLIs of engine/slo
// from the processes' own /metrics; -mode chaos runs failure scenarios
// against the same set-up. See docs/performance.md.
//
//	go build -o /tmp/taskiem ./cmd/taskiem
//	go run ./tools/loadtest -mode cluster -bin /tmp/taskiem -rates 10,20,40 -stage 60s
package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/url"
	"os"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/israel-duff/taskiem/engine/connector"
	"github.com/israel-duff/taskiem/engine/db"
	rt "github.com/israel-duff/taskiem/engine/runtime"
)

const benchManifest = `
manifest: connector/v1
id: bench
version: 1.0.0
name: Bench
category: developer
auth: { type: none }
actions:
  noop:
    title: Do nothing
    class: read
    input: { type: object, properties: { run: { type: string }, step: { type: string } } }
`

// started records when each (run, step) began executing.
type started struct {
	mu sync.Mutex
	at map[string]time.Time
}

func (s *started) mark(run, step string) {
	now := time.Now()
	s.mu.Lock()
	if _, ok := s.at[run+"/"+step]; !ok {
		s.at[run+"/"+step] = now
	}
	s.mu.Unlock()
}

func main() {
	dsn := flag.String("dsn", os.Getenv("TASKIEM_TEST_DATABASE_URL"), "superuser DSN; a fresh database is created next to it")
	mode := flag.String("mode", "latency", "latency (open-loop arrivals at -rate), throughput (all -runs at once), cluster (the binary's roles in separate processes, rate stages) or chaos (failure scenarios against the same)")
	rate := flag.Float64("rate", 500, "latency mode: offered steps/s")
	duration := flag.Duration("duration", 30*time.Second, "latency mode: how long to offer load")
	runs := flag.Int("runs", 4000, "throughput mode: runs")
	tenants := flag.Int("tenants", 20, "tenants")
	steps := flag.Int("steps", 5, "sequential steps per run")
	procs := flag.Int("procs", 1, "worker processes (each one claimer, its own LISTEN connection)")
	concurrency := flag.Int("concurrency", 16, "executors per worker process")
	conns := flag.Int("conns", 64, "pool size")
	keep := flag.Bool("keep", false, "keep the database afterwards")
	co := clusterFlags()
	flag.Parse()
	if *dsn == "" {
		log.Fatal("-dsn or TASKIEM_TEST_DATABASE_URL required")
	}
	ctx := context.Background()
	dbDSN, drop := freshDB(ctx, *dsn, *keep)
	defer drop()
	if *mode == "cluster" || *mode == "chaos" {
		runCluster(ctx, *mode, dbDSN, *tenants, co)
		return
	}
	app := pool(ctx, dbDSN, "taskiem_app", *conns)
	admin := pool(ctx, dbDSN, "", 4)

	rec := &started{at: map[string]time.Time{}}
	reg := connector.NewRegistry()
	if err := reg.Register(&connector.Connector{Manifest: connector.MustParse([]byte(benchManifest)), Actions: map[string]connector.Action{
		"noop": connector.ActionFunc(func(_ context.Context, req connector.Request) (connector.Response, error) {
			run, _ := req.Input["run"].(string)
			step, _ := req.Input["step"].(string)
			rec.mark(run, step)
			return connector.Response{Output: map[string]any{"ok": true}}, nil
		}),
	}}); err != nil {
		log.Fatal(err)
	}
	store := &rt.Store{Pool: app, Registry: reg}
	ts, wfs := seed(ctx, app, *tenants, *steps)

	runCtx, stop := context.WithCancel(ctx)
	var wg sync.WaitGroup
	for p := 0; p < *procs; p++ {
		w := &rt.Worker{Store: store, Registry: reg, Secrets: rt.MapSecrets{}, ID: "load-" + strconv.Itoa(p), Queue: "connector",
			Concurrency: *concurrency, Drain: time.Second}
		wg.Add(1)
		go func() { defer wg.Done(); _ = w.Run(runCtx) }()
	}
	sched := &rt.Scheduler{Store: store, Interval: 100 * time.Millisecond}
	wg.Add(1)
	go func() { defer wg.Done(); _ = sched.Run(runCtx) }()
	time.Sleep(500 * time.Millisecond)

	cpu0, ru0, tx0 := cpuSample(), rusage(), commits(ctx, admin)
	start := time.Now()
	var total atomic.Int64
	var startErrs atomic.Int64
	startOne := func(i int) {
		k := i % len(ts)
		if _, _, err := store.StartRun(ctx, rt.StartRequest{TenantID: ts[k], WorkflowID: wfs[k], Version: 1, Environment: "prod", Trigger: map[string]any{}, Env: map[string]any{}}); err != nil {
			startErrs.Add(1)
			log.Print(err)
		}
		total.Add(1)
	}
	switch *mode {
	case "throughput":
		var sw sync.WaitGroup
		sem := make(chan struct{}, 16)
		for i := 0; i < *runs; i++ {
			sem <- struct{}{}
			sw.Add(1)
			go func(i int) { defer sw.Done(); startOne(i); <-sem }(i)
		}
		sw.Wait()
	case "latency":
		interval := time.Duration(float64(time.Second) / (*rate / float64(*steps)))
		tick := time.NewTicker(interval)
		deadline := time.Now().Add(*duration)
		var sw sync.WaitGroup
		for i := 0; time.Now().Before(deadline); i++ {
			<-tick.C
			sw.Add(1)
			go func(i int) { defer sw.Done(); startOne(i) }(i)
		}
		tick.Stop()
		sw.Wait()
	default:
		log.Fatalf("unknown mode %q", *mode)
	}
	offeredFor := time.Since(start)
	waitDone(ctx, admin, int(total.Load()), 2*time.Minute)
	elapsed := time.Since(start)
	cpu1, ru1, tx1 := cpuSample(), rusage(), commits(ctx, admin)
	stop()
	wg.Wait()

	lat := dispatchLatencies(ctx, admin, rec)
	stepsDone := int(total.Load()) * *steps
	busy, cores := cpu1.busyFraction(cpu0)
	var notDone int
	_ = admin.QueryRow(ctx, `SELECT count(*) FROM runs WHERE status <> 'completed'`).Scan(&notDone)
	fmt.Printf("engine=runtime mode=%s tenants=%d runs=%d steps/run=%d procs=%d concurrency=%d conns=%d go=%s cpus=%d\n",
		*mode, *tenants, total.Load(), *steps, *procs, *concurrency, *conns, runtime.Version(), cores)
	if *mode == "latency" {
		fmt.Printf("offered=%.0f steps/s for %s; achieved=%.0f steps/s (all steps / time to last completion)\n", *rate, offeredFor.Round(time.Millisecond), float64(stepsDone)/elapsed.Seconds())
	} else {
		fmt.Printf("throughput=%.0f steps/s (%d steps in %s)\n", float64(stepsDone)/elapsed.Seconds(), stepsDone, elapsed.Round(time.Millisecond))
	}
	fmt.Printf("dispatch latency (StepScheduled -> executing) n=%d p50=%s p95=%s p99=%s max=%s\n", len(lat), pct(lat, 50), pct(lat, 95), pct(lat, 99), pct(lat, 100))
	fmt.Printf("postgres commits=%d (%.1f per step); host busy=%.0f%%; loadtest process=%.2f cores; runs not completed=%d; start errors=%d\n",
		tx1-tx0, float64(tx1-tx0)/float64(stepsDone), busy*100, (ru1-ru0).Seconds()/elapsed.Seconds(), notDone, startErrs.Load())
}

func pct(xs []time.Duration, p float64) string {
	if len(xs) == 0 {
		return "-"
	}
	i := int(float64(len(xs)-1) * p / 100)
	return xs[i].Round(100 * time.Microsecond).String()
}

// dispatchLatencies joins each step's StepScheduled time (database clock,
// same host) with the moment the no-op action began.
func dispatchLatencies(ctx context.Context, admin *pgxpool.Pool, rec *started) []time.Duration {
	rows, err := admin.Query(ctx, `SELECT run_id::text, step_id, recorded_at FROM run_events WHERE type = 'StepScheduled' AND attempt = 1`)
	if err != nil {
		log.Fatal(err)
	}
	defer rows.Close()
	var out []time.Duration
	rec.mu.Lock()
	defer rec.mu.Unlock()
	for rows.Next() {
		var run, step string
		var at time.Time
		if err := rows.Scan(&run, &step, &at); err != nil {
			log.Fatal(err)
		}
		if t, ok := rec.at[run+"/"+step]; ok {
			out = append(out, t.Sub(at))
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

func seed(ctx context.Context, app *pgxpool.Pool, n, steps int) ([]uuid.UUID, []uuid.UUID) {
	var list []map[string]any
	for i := 1; i <= steps; i++ {
		s := map[string]any{"id": "s" + strconv.Itoa(i), "type": "connector", "connector": "bench@1", "action": "noop",
			"input": map[string]any{"run": "=run.id", "step": "s" + strconv.Itoa(i)}}
		if i > 1 {
			s["needs"] = []string{"s" + strconv.Itoa(i-1)}
		}
		list = append(list, s)
	}
	def, _ := json.Marshal(map[string]any{"schema": "wd/v1", "id": "wf_load", "version": 1, "name": "load", "trigger": map[string]any{"type": "manual"}, "steps": list})
	sum := sha256.Sum256(def)
	var ts, ws []uuid.UUID
	for i := 0; i < n; i++ {
		t, w := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
		err := db.InTenantTx(ctx, app, []uuid.UUID{t}, func(tx pgx.Tx) error {
			if _, err := tx.Exec(ctx, `INSERT INTO tenants (id, name, plan_id) VALUES ($1, 'load', $2)`, t, uuid.New()); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `INSERT INTO workflows (id, tenant_id, name, created_by, active_version) VALUES ($1, $2, 'load', $2, 1)`, w, t); err != nil {
				return err
			}
			_, err := tx.Exec(ctx, `INSERT INTO workflow_versions (workflow_id, version, tenant_id, definition, digest, state) VALUES ($1, 1, $2, $3, $4, 'published')`, w, t, def, sum[:])
			return err
		})
		if err != nil {
			log.Fatal(err)
		}
		ts, ws = append(ts, t), append(ws, w)
	}
	return ts, ws
}

func freshDB(ctx context.Context, base string, keep bool) (string, func()) {
	name := "taskiem_load_" + uuid.NewString()[:8]
	c, err := pgx.Connect(ctx, base)
	if err != nil {
		log.Fatal(err)
	}
	if _, err := c.Exec(ctx, "CREATE DATABASE "+name); err != nil {
		log.Fatal(err)
	}
	_ = c.Close(ctx)
	u, _ := url.Parse(base)
	u.Path = "/" + name
	if _, err := db.Migrate(ctx, u.String()); err != nil {
		log.Fatal(err)
	}
	return u.String(), func() {
		if keep {
			fmt.Println("kept database", name)
			return
		}
		if c, err := pgx.Connect(ctx, base); err == nil {
			_, _ = c.Exec(ctx, "DROP DATABASE IF EXISTS "+name+" WITH (FORCE)")
			_ = c.Close(ctx)
		}
	}
}

func pool(ctx context.Context, dsn, role string, size int) *pgxpool.Pool {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		log.Fatal(err)
	}
	cfg.MaxConns = int32(size) //nolint:gosec // a flag-sized pool
	if role != "" {
		cfg.AfterConnect = func(ctx context.Context, c *pgx.Conn) error {
			_, err := c.Exec(ctx, "SET ROLE "+role)
			return err
		}
	}
	p, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		log.Fatal(err)
	}
	return p
}

func waitDone(ctx context.Context, admin *pgxpool.Pool, total int, limit time.Duration) {
	deadline := time.Now().Add(limit)
	for time.Now().Before(deadline) {
		var n int
		if err := admin.QueryRow(ctx, `SELECT count(*) FROM runs WHERE status = 'completed'`).Scan(&n); err != nil {
			log.Fatal(err)
		}
		if n >= total {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	log.Print("timed out waiting for runs to complete")
}

func commits(ctx context.Context, admin *pgxpool.Pool) int64 {
	var n int64
	_ = admin.QueryRow(ctx, `SELECT sum(xact_commit)::bigint FROM pg_stat_database`).Scan(&n)
	return n
}

type cpuStat struct{ busy, total uint64 }

func cpuSample() cpuStat {
	b, err := os.ReadFile("/proc/stat")
	if err != nil {
		return cpuStat{}
	}
	f := strings.Fields(strings.SplitN(string(b), "\n", 2)[0])[1:]
	var s cpuStat
	for i, v := range f {
		x, _ := strconv.ParseUint(v, 10, 64)
		s.total += x
		if i != 3 && i != 4 { // idle, iowait
			s.busy += x
		}
	}
	return s
}

func (c cpuStat) busyFraction(prev cpuStat) (float64, int) {
	if c.total == prev.total {
		return 0, runtime.NumCPU()
	}
	return float64(c.busy-prev.busy) / float64(c.total-prev.total), runtime.NumCPU()
}

func rusage() time.Duration {
	var ru syscall.Rusage
	_ = syscall.Getrusage(syscall.RUSAGE_SELF, &ru)
	return time.Duration(ru.Utime.Nano() + ru.Stime.Nano())
}
