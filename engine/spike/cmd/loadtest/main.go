// Command loadtest drives the Phase 0 engine spike against a fresh database
// and reports step throughput and dispatch latency. See ../../RESULTS.md.
//
//	loadtest -dsn postgres://postgres@127.0.0.1:55432/postgres -mode throughput
//	loadtest -dsn ... -mode latency -rate 500
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/url"
	"os"
	"runtime"
	"runtime/pprof"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/spike"
)

func main() {
	dsn := flag.String("dsn", os.Getenv("TASKIEM_TEST_DATABASE_URL"), "superuser DSN; a fresh database is created next to it")
	mode := flag.String("mode", "throughput", "throughput (all runs at once) or latency (open-loop arrival at -rate)")
	rate := flag.Float64("rate", 500, "latency mode: target steps/s")
	duration := flag.Duration("duration", 30*time.Second, "latency mode: how long to generate load")
	tenants := flag.Int("tenants", 20, "tenants")
	runs := flag.Int("runs", 4000, "throughput mode: runs")
	steps := flag.Int("steps", 5, "steps per run")
	workers := flag.Int("workers", 16, "worker executors per process")
	orchestrators := flag.Int("orchestrators", 8, "orchestrator deciders per process")
	procs := flag.Int("procs", 1, "simulated processes, each with its own claimers and LISTEN connections")
	batch := flag.Int("batch", 8, "claim batch size")
	conns := flag.Int("conns", 64, "pool size")
	cpuprofile := flag.String("cpuprofile", "", "write a Go CPU profile")
	keep := flag.Bool("keep", false, "keep the database afterwards")
	inline := flag.Bool("inline", false, "run decide() inside the worker's completion transaction")
	flag.Parse()
	if *dsn == "" {
		log.Fatal("-dsn or TASKIEM_TEST_DATABASE_URL required")
	}
	ctx := context.Background()

	dbDSN, drop := freshDB(ctx, *dsn, *keep)
	defer drop()
	app := pool(ctx, dbDSN, "taskiem_app", *conns)
	admin := pool(ctx, dbDSN, "", 4)

	tenantIDs, wfIDs := seed(ctx, app, *tenants)
	stepNames := make([]string, *steps)
	for i := range stepNames {
		stepNames[i] = "s" + strconv.Itoa(i+1)
	}
	eng := &spike.Engine{Pool: app, Steps: stepNames, Queue: "connector", Batch: *batch,
		Latency: &spike.Recorder{}, Decide: &spike.Recorder{}, Fenced: &spike.Counter{}, Inline: *inline}

	if *cpuprofile != "" {
		f, err := os.Create(*cpuprofile)
		if err != nil {
			log.Fatal(err)
		}
		_ = pprof.StartCPUProfile(f)
		defer pprof.StopCPUProfile()
	}

	runCtx, stop := context.WithCancel(ctx)
	var wg sync.WaitGroup
	errs := make(chan error, 2**procs)
	for p := 0; p < *procs; p++ {
		wg.Add(2)
		go func(p int) { defer wg.Done(); errs <- eng.Workers(runCtx, fmt.Sprintf("w%d", p), *workers) }(p)
		go func(p int) { defer wg.Done(); errs <- eng.Orchestrators(runCtx, fmt.Sprintf("o%d", p), *orchestrators) }(p)
	}
	go func() {
		for err := range errs {
			if err != nil {
				log.Fatal(err)
			}
		}
	}()
	time.Sleep(500 * time.Millisecond) // let loops connect and LISTEN

	cpu0, ru0, tx0 := cpuSample(), rusage(), commits(ctx, admin)
	start := time.Now()
	var total int
	switch *mode {
	case "throughput":
		total = *runs
		startRuns(ctx, app, tenantIDs, wfIDs, total, 200)
	case "latency":
		perSec := *rate / float64(*steps)
		interval := time.Duration(float64(time.Second) / perSec)
		tick := time.NewTicker(interval)
		deadline := time.Now().Add(*duration)
		for time.Now().Before(deadline) {
			<-tick.C
			startRuns(ctx, app, tenantIDs, wfIDs, 1, 1)
			total++
		}
		tick.Stop()
	default:
		log.Fatalf("unknown mode %q", *mode)
	}
	waitDone(ctx, admin, total)
	elapsed := time.Since(start)
	cpu1, ru1, tx1 := cpuSample(), rusage(), commits(ctx, admin)
	stop()
	wg.Wait()

	stepsDone := total * *steps
	busy, cores := cpu1.busyFraction(cpu0)
	goCPU := (ru1 - ru0).Seconds() / elapsed.Seconds()
	lat, n := eng.Latency.Percentiles(50, 95, 99, 100)
	dec, _ := eng.Decide.Percentiles(50, 95, 99)
	fmt.Printf("mode=%s inline=%v tenants=%d runs=%d steps/run=%d procs=%d workers=%d orchestrators=%d batch=%d conns=%d\n",
		*mode, *inline, *tenants, total, *steps, *procs, *workers, *orchestrators, *batch, *conns)
	fmt.Printf("elapsed=%s steps=%d throughput=%.0f steps/s\n", elapsed.Round(time.Millisecond), stepsDone, float64(stepsDone)/elapsed.Seconds())
	fmt.Printf("dispatch latency (task insert -> claimed) n=%d p50=%s p95=%s p99=%s max=%s\n", n, r(lat[0]), r(lat[1]), r(lat[2]), r(lat[3]))
	fmt.Printf("decide latency (StepCompleted -> next StepScheduled) p50=%s p95=%s p99=%s\n", r(dec[0]), r(dec[1]), r(dec[2]))
	fmt.Printf("postgres commits=%d (%.0f/s, %.1f per step)\n", tx1-tx0, float64(tx1-tx0)/elapsed.Seconds(), float64(tx1-tx0)/float64(stepsDone))
	fmt.Printf("cpu: machine busy=%.0f%% of %d cores; loadtest process=%.2f cores; fenced=%d; go=%s\n",
		busy*100, cores, goCPU, eng.Fenced.Load(), runtime.Version())
}

func r(d time.Duration) string { return d.Round(100 * time.Microsecond).String() }

func freshDB(ctx context.Context, base string, keep bool) (string, func()) {
	name := "taskiem_spike_" + uuid.NewString()[:8]
	c, err := pgx.Connect(ctx, base)
	if err != nil {
		log.Fatal(err)
	}
	if _, err := c.Exec(ctx, "CREATE DATABASE "+name); err != nil {
		log.Fatal(err)
	}
	c.Close(ctx)
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
			c.Close(ctx)
		}
	}
}

func pool(ctx context.Context, dsn, role string, size int) *pgxpool.Pool {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		log.Fatal(err)
	}
	cfg.MaxConns = int32(size)
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

func seed(ctx context.Context, app *pgxpool.Pool, n int) ([]uuid.UUID, []uuid.UUID) {
	var ts, ws []uuid.UUID
	for i := 0; i < n; i++ {
		t, w := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
		err := db.InTenantTx(ctx, app, []uuid.UUID{t}, func(tx pgx.Tx) error {
			if _, err := tx.Exec(ctx, `INSERT INTO tenants (id, name, plan_id) VALUES ($1, 'spike', $2)`, t, uuid.New()); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `INSERT INTO workflows (id, tenant_id, name, created_by) VALUES ($1, $2, 'spike', $2)`, w, t); err != nil {
				return err
			}
			_, err := tx.Exec(ctx, `INSERT INTO workflow_versions (workflow_id, version, tenant_id, definition, digest, state) VALUES ($1, 1, $2, '{}', sha256('x'), 'published')`, w, t)
			return err
		})
		if err != nil {
			log.Fatal(err)
		}
		ts, ws = append(ts, t), append(ws, w)
	}
	return ts, ws
}

var next int

// startRuns creates runs round-robin across tenants, chunk runs per transaction.
func startRuns(ctx context.Context, app *pgxpool.Pool, ts, ws []uuid.UUID, n, chunk int) {
	for done := 0; done < n; {
		i := next % len(ts)
		k := min(chunk, n-done)
		err := db.InTenantTx(ctx, app, []uuid.UUID{ts[i]}, func(tx pgx.Tx) error {
			for j := 0; j < k; j++ {
				id := uuid.Must(uuid.NewV7())
				if _, err := tx.Exec(ctx, `INSERT INTO runs (id, tenant_id, workflow_id, version, environment, started_at) VALUES ($1, $2, $3, 1, 'prod', now())`, id, ts[i], ws[i]); err != nil {
					return err
				}
				if _, err := tx.Exec(ctx, `SELECT taskiem_append_event($1, 'RunStarted', NULL, NULL, '{}')`, id); err != nil {
					return err
				}
			}
			_, err := tx.Exec(ctx, `SELECT pg_notify('taskiem_runs', '')`)
			return err
		})
		if err != nil {
			log.Fatal(err)
		}
		next++
		done += k
	}
}

func waitDone(ctx context.Context, admin *pgxpool.Pool, total int) {
	for {
		var n int
		if err := admin.QueryRow(ctx, `SELECT count(*) FROM runs WHERE status = 'completed'`).Scan(&n); err != nil {
			log.Fatal(err)
		}
		if n >= total {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
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
