package runtime_test

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"os"
	"os/exec"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/israel-duff/taskiem/engine/decide"
	"github.com/israel-duff/taskiem/engine/history"
	"github.com/israel-duff/taskiem/engine/runtime"
	rt "github.com/israel-duff/taskiem/engine/runtime/runtimetest"
	"github.com/israel-duff/taskiem/engine/wd"
)

func envInt(name string, def int) int {
	if v, err := strconv.Atoi(os.Getenv(name)); err == nil && v > 0 {
		return v
	}
	return def
}

// paymentShaped exercises every write path: an idempotent transfer, a
// reconcilable bank transfer against a provider that ignores keys, and a
// foreach of transfers keyed by instance.
const paymentShaped = `{"schema":"wd/v1","id":"wf_chaos","version":1,"name":"chaos","trigger":{"type":"manual"},"steps":[
  {"id":"pay","type":"connector","connector":"fakepay@1","action":"transfer",
   "input":{"amount":"=trigger.amount","logical_id":"=run.id + ':pay'"},"retry":{"max":40,"initial":"20ms","backoff":"fixed"}},
  {"id":"bank","type":"connector","connector":"fakepay@1","action":"bank_transfer","needs":["pay"],
   "input":{"amount":"=trigger.amount","logical_id":"=run.id + ':bank'"},"retry":{"max":40,"initial":"20ms","backoff":"fixed"}},
  {"id":"fan","type":"foreach","needs":["pay"],"config":{"items":"=[0,1,2]","max_concurrency":2,"steps":[
    {"id":"item_pay","type":"connector","connector":"fakepay@1","action":"transfer",
     "input":{"amount":1,"logical_id":"=run.id + ':item:' + string(index)"},"retry":{"max":40,"initial":"20ms","backoff":"fixed"}}]}},
  {"id":"done","type":"transform","needs":["bank","fan"],"config":{"output":"=size(steps.fan.output)"}}]}`

var logicalSuffixes = []string{":pay", ":bank", ":item:0", ":item:1", ":item:2"}

// chaosWorkers runs n worker processes that crash, stall, and get killed.
type chaosWorkers struct {
	e               *rt.Env
	rng             *rand.Rand
	mu              sync.Mutex
	stops           []context.CancelFunc
	wg              sync.WaitGroup
	crashP          float64
	stallP          float64
	lease           time.Duration
	nextID          atomic.Int64
	kills           atomic.Int64
	crashes, stalls atomic.Int64
}

func (c *chaosWorkers) roll(p float64) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.rng.Float64() < p
}

func (c *chaosWorkers) start(ctx context.Context) {
	wctx, cancel := context.WithCancel(ctx)
	w := c.e.Worker(fmt.Sprintf("chaos-%d", c.nextID.Add(1)))
	w.Concurrency = 4
	w.Lease = c.lease
	w.CallTimeout = c.lease / 2
	w.Hooks = &runtime.Hooks{
		AfterIntent: func(string, int) error {
			if c.roll(c.stallP) {
				c.stalls.Add(1)
				time.Sleep(3 * c.lease) // a GC pause or a frozen VM: the lease expires underneath us
				return nil
			}
			if c.roll(c.crashP) {
				c.crashes.Add(1)
				return errors.New("crash after intent")
			}
			return nil
		},
		AfterCall: func(string, int) error {
			if c.roll(c.crashP) {
				c.crashes.Add(1)
				return errors.New("crash after call")
			}
			return nil
		},
	}
	c.mu.Lock()
	c.stops = append(c.stops, cancel)
	c.mu.Unlock()
	c.wg.Add(1)
	go func() { defer c.wg.Done(); _ = w.Run(wctx) }()
}

// killOne cancels a random worker process mid-flight and starts a new one.
func (c *chaosWorkers) killOne(ctx context.Context) {
	c.mu.Lock()
	i := c.rng.IntN(len(c.stops))
	stop := c.stops[i]
	c.stops = append(c.stops[:i], c.stops[i+1:]...)
	c.mu.Unlock()
	stop()
	c.kills.Add(1)
	c.start(ctx)
}

func (c *chaosWorkers) stopAll() {
	c.mu.Lock()
	for _, s := range c.stops {
		s()
	}
	c.mu.Unlock()
	c.wg.Wait()
}

// TestCrashRecoverySuite is the build plan's crash suite at test scale. The
// G1 gate runs it with TASKIEM_CHAOS_RUNS=10000.
func TestCrashRecoverySuite(t *testing.T) {
	runs := envInt("TASKIEM_CHAOS_RUNS", 120)
	e := rt.New(t)
	e.Provider.Faults = rt.RandomFaults(uint64(envInt("TASKIEM_CHAOS_SEED", 7)), 0.08, 0.08)
	wf := e.Publish(t, paymentShaped)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cw := &chaosWorkers{e: e, rng: rand.New(rand.NewPCG(1, 2)), crashP: 0.04, stallP: 0.01, lease: 400 * time.Millisecond}
	for i := 0; i < 4; i++ {
		cw.start(ctx)
	}
	sched := e.Scheduler()
	go func() { _ = sched.Run(ctx) }()

	refs := make([]runtime.RunRef, runs)
	for i := range refs {
		refs[i] = e.Start(t, wf, map[string]any{"amount": 1000 + i})
	}
	killer := time.NewTicker(300 * time.Millisecond)
	defer killer.Stop()
	deadline := time.Now().Add(time.Duration(runs)*100*time.Millisecond + 60*time.Second)
	for {
		var open int
		if err := e.DB.Admin.QueryRow(context.Background(), `SELECT count(*) FROM runs WHERE status NOT IN ('completed', 'failed', 'cancelled', 'needs_reconciliation')`).Scan(&open); err != nil {
			t.Fatal(err)
		}
		if open == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("%d runs still open at the deadline", open)
		}
		select {
		case <-killer.C:
			cw.killOne(ctx)
		case <-time.After(50 * time.Millisecond):
		}
	}
	cancel()
	cw.stopAll()

	def, err := wd.Load([]byte(paymentShaped))
	if err != nil {
		t.Fatal(err)
	}
	executions := e.Provider.Snapshot()
	var duplicates, lost, notCompleted, replayErrors int
	for _, ref := range refs {
		if st := e.Status(t, ref); st != "completed" {
			notCompleted++
			t.Errorf("run %s ended %s", ref.ID, st)
		}
		for _, suffix := range logicalSuffixes {
			switch n := executions[ref.ID.String()+suffix]; {
			case n > 1:
				duplicates++
				t.Errorf("duplicate effect: %s%s executed %d times", ref.ID, suffix, n)
			case n == 0:
				lost++
				t.Errorf("lost effect: %s%s never executed", ref.ID, suffix)
			}
		}
		h := events(t, e, ref)
		if err := decide.Verify(def, h); err != nil {
			replayErrors++
			t.Errorf("run %s does not replay: %v", ref.ID, err)
		}
		done := map[string]int{}
		for _, ev := range h {
			if ev.Type == history.StepCompleted {
				done[ev.StepID]++
			}
		}
		for inst, n := range done {
			if n != 1 {
				t.Errorf("run %s: %s completed %d times", ref.ID, inst, n)
			}
		}
	}
	t.Logf("runs=%d effects=%d duplicates=%d lost=%d not-completed=%d replay-errors=%d | injected: crashes=%d stalls=%d process-kills=%d",
		runs, runs*len(logicalSuffixes), duplicates, lost, notCompleted, replayErrors, cw.crashes.Load(), cw.stalls.Load(), cw.kills.Load())
}

// TestUnsafeWritesNeverRepeat: with no key and no status API, an unknown
// outcome parks the run; the effect must never happen twice.
func TestUnsafeWritesNeverRepeat(t *testing.T) {
	runs := envInt("TASKIEM_CHAOS_RUNS", 120) / 3
	e := rt.New(t)
	e.Provider.Faults = rt.RandomFaults(11, 0.15, 0.15)
	wf := e.Publish(t, `{"schema":"wd/v1","id":"wf_n","version":1,"name":"n","trigger":{"type":"manual"},"steps":[
	  {"id":"sms","type":"connector","connector":"fakepay@1","action":"notify","input":{"logical_id":"=run.id"},"retry":{"max":40,"initial":"10ms","backoff":"fixed"}}]}`)
	refs := make([]runtime.RunRef, runs)
	for i := range refs {
		refs[i] = e.Start(t, wf, map[string]any{})
	}
	rt.WaitFor(t, 60*time.Second, "all runs settled", func() bool {
		e.Drain(t)
		var open int
		_ = e.DB.Admin.QueryRow(context.Background(), `SELECT count(*) FROM runs WHERE status = 'running'`).Scan(&open)
		return open == 0
	})
	parked := 0
	for _, ref := range refs {
		n := e.Provider.Executions(ref.ID.String())
		switch st := e.Status(t, ref); st {
		case "completed":
			if n != 1 {
				t.Errorf("completed run executed %d times", n)
			}
		case "needs_reconciliation":
			parked++
			if n > 1 {
				t.Errorf("parked run executed %d times", n)
			}
		default:
			t.Errorf("run ended %s", st)
		}
	}
	if parked == 0 {
		t.Error("expected some runs to park under injected unknown outcomes")
	}
	t.Logf("runs=%d parked=%d", runs, parked)
}

// TestDeterminismSuite replays every history the runtime tests produce.
func TestDeterminismSuite(t *testing.T) {
	e := rt.New(t)
	e.Provider.Faults = rt.RandomFaults(3, 0.2, 0.2)
	wf := e.Publish(t, paymentShaped)
	var refs []runtime.RunRef
	for i := 0; i < 20; i++ {
		refs = append(refs, e.Start(t, wf, map[string]any{"amount": i}))
	}
	rt.WaitFor(t, 60*time.Second, "runs settled", func() bool {
		e.Drain(t)
		var open int
		_ = e.DB.Admin.QueryRow(context.Background(), `SELECT count(*) FROM runs WHERE status = 'running'`).Scan(&open)
		return open == 0
	})
	def, _ := wd.Load([]byte(paymentShaped))
	for _, ref := range refs {
		if err := decide.Verify(def, events(t, e, ref)); err != nil {
			t.Errorf("run %s: %v", ref.ID, err)
		}
	}
}

// TestDatabaseCrashSuite crashes Postgres while payment-shaped runs are in
// flight (G1: "the database primary killed mid-run") and checks the same
// invariants as the crash suite. It is opt-in: TASKIEM_CHAOS_PG_CRASH names
// a command that crashes the test database with an immediate shutdown and
// starts it again (e.g. pg_ctl -m immediate stop; pg_ctl start).
func TestDatabaseCrashSuite(t *testing.T) {
	crash := os.Getenv("TASKIEM_CHAOS_PG_CRASH")
	if crash == "" {
		t.Skip("set TASKIEM_CHAOS_PG_CRASH to a command that crashes and restarts the test database")
	}
	runs := envInt("TASKIEM_CHAOS_RUNS", 200)
	crashes := envInt("TASKIEM_CHAOS_PG_CRASHES", 3)
	e := rt.New(t)
	e.Provider.Faults = rt.RandomFaults(uint64(envInt("TASKIEM_CHAOS_SEED", 11)), 0.02, 0.02)
	wf := e.Publish(t, paymentShaped)
	refs := make([]runtime.RunRef, runs)
	for i := range refs {
		refs[i] = e.Start(t, wf, map[string]any{"amount": 1000 + i})
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cw := &chaosWorkers{e: e, rng: rand.New(rand.NewPCG(3, 4)), lease: 2 * time.Second}
	for i := 0; i < 4; i++ {
		cw.start(ctx)
	}
	sched := e.Scheduler()
	go func() { _ = sched.Run(ctx) }()

	open := func() (int, error) {
		var n int
		err := e.DB.Admin.QueryRow(context.Background(), `SELECT count(*) FROM runs WHERE status NOT IN ('completed', 'failed', 'cancelled', 'needs_reconciliation')`).Scan(&n)
		return n, err
	}
	for i := 0; i < crashes; i++ {
		// Crash at progress points, so recovery between crashes is tested too.
		target := runs - (i+1)*runs/(crashes+2)
		n, _ := open()
		for wait := time.Now().Add(60 * time.Second); n > target && time.Now().Before(wait); n, _ = open() {
			time.Sleep(20 * time.Millisecond)
		}
		out, err := exec.Command("sh", "-c", crash).CombinedOutput()
		if err != nil {
			t.Fatalf("crash command: %v: %s", err, out)
		}
		t.Logf("crashed the database with %d runs open", n)
	}
	deadline := time.Now().Add(3 * time.Minute)
	for {
		n, err := open()
		if err == nil && n == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("%d runs still open at the deadline (%v)", n, err)
		}
		time.Sleep(100 * time.Millisecond)
	}
	cancel()
	cw.stopAll()

	def, err := wd.Load([]byte(paymentShaped))
	if err != nil {
		t.Fatal(err)
	}
	executions := e.Provider.Snapshot()
	var duplicates, lost, notCompleted, replayErrors int
	for _, ref := range refs {
		if st := e.Status(t, ref); st != "completed" {
			notCompleted++
			t.Errorf("run %s ended %s", ref.ID, st)
		}
		for _, suffix := range logicalSuffixes {
			switch n := executions[ref.ID.String()+suffix]; {
			case n > 1:
				duplicates++
				t.Errorf("duplicate effect: %s%s executed %d times", ref.ID, suffix, n)
			case n == 0:
				lost++
				t.Errorf("lost effect: %s%s never executed", ref.ID, suffix)
			}
		}
		if err := decide.Verify(def, events(t, e, ref)); err != nil {
			replayErrors++
			t.Errorf("run %s does not replay: %v", ref.ID, err)
		}
	}
	t.Logf("runs=%d effects=%d database-crashes=%d duplicates=%d lost=%d not-completed=%d replay-errors=%d",
		runs, runs*len(logicalSuffixes), crashes, duplicates, lost, notCompleted, replayErrors)
}
