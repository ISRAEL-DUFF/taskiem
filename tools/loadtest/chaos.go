package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func runChaos(ctx context.Context, c *cluster, admin *pgxpool.Pool, scenario, frozen string) {
	switch scenario {
	case "kill-worker":
		chaosKillWorker(ctx, c, admin)
	case "restart-orchestrator":
		chaosRestartOrchestrator(ctx, c, admin)
	case "replica-lag":
		chaosReplicaLag(ctx, c, admin, frozen)
	default:
		log.Fatalf("unknown scenario %q", scenario)
	}
}

// startRuns sends n webhooks to path, spread over tenants, at rate per
// second, and returns the accepted run ids.
func (c *cluster) startRuns(path string, n int, rate float64) []string {
	client := &http.Client{Timeout: 30 * time.Second}
	var mu sync.Mutex
	var ids []string
	var wg sync.WaitGroup
	tick := time.NewTicker(time.Duration(float64(time.Second) / rate))
	defer tick.Stop()
	for i := 0; i < n; i++ {
		<-tick.C
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			id, _, _, err := c.hook(client, c.tenants[i%len(c.tenants)], path, map[string]any{"n": c.seq.Add(1)})
			if err != nil {
				log.Printf("hook: %v", err)
				return
			}
			mu.Lock()
			ids = append(ids, id)
			mu.Unlock()
		}(i)
	}
	wg.Wait()
	return ids
}

// settled counts runs that ended or parked.
func settled(ctx context.Context, admin *pgxpool.Pool, ids []string) (ended, parked int) {
	_ = admin.QueryRow(ctx, `SELECT count(*) FILTER (WHERE ended_at IS NOT NULL), count(*) FILTER (WHERE status = 'needs_reconciliation') FROM runs WHERE id = ANY($1::uuid[])`, ids).Scan(&ended, &parked)
	return
}

func statusCounts(ctx context.Context, admin *pgxpool.Pool, ids []string) map[string]int {
	out := map[string]int{}
	rows, err := admin.Query(ctx, `SELECT status, count(*) FROM runs WHERE id = ANY($1::uuid[]) GROUP BY 1`, ids)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var s string
		var n int
		if rows.Scan(&s, &n) == nil {
			out[s] = n
		}
	}
	return out
}

// chaosKillWorker kills a worker with SIGKILL while provider calls are in
// flight: transfers (idempotent writes) and SMS (unsafe writes), each held
// by the fake provider for a few seconds after it recorded the effect, as
// if the answer were lost with the worker.
func chaosKillWorker(ctx context.Context, c *cluster, admin *pgxpool.Pool) {
	c.fake.transferDelay.Store(int64(6 * time.Second))
	c.fake.smsDelay.Store(int64(6 * time.Second))
	before := c.scrapeAll(ctx)
	var pays, sms []string
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); pays = c.startRuns("/load", 60, 30) }()
	go func() { defer wg.Done(); sms = c.startRuns("/sms", 30, 15) }()
	wg.Wait()
	// Wait until calls are being held, then kill the first worker.
	deadline := time.Now().Add(30 * time.Second)
	for c.fake.inflight.Load() < 8 && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	time.Sleep(500 * time.Millisecond)
	victim := c.procs[len(c.procs)-2] // worker1
	var held int
	_ = admin.QueryRow(ctx, `SELECT count(*) FROM tasks WHERE lease_owner LIKE $1`, "%/"+fmt.Sprint(c.pid(victim))).Scan(&held)
	inflight := c.fake.inflight.Load()
	killedAt := time.Now()
	c.kill(victim)
	fmt.Printf("killed %s (pid gone) at %s holding %d task leases; %d provider calls in flight\n", victim.name, killedAt.Format(time.TimeOnly), held, inflight)
	c.fake.transferDelay.Store(0)
	c.fake.smsDelay.Store(0)
	// A supervisor restarts it a moment later, as Kubernetes would.
	time.Sleep(2 * time.Second)
	c.restart(victim)
	fmt.Printf("restarted %s after %s\n", victim.name, time.Since(killedAt).Round(time.Millisecond))

	all := append(append([]string{}, pays...), sms...)
	var lastProgress time.Time
	prevEnded, prevParked := -1, -1
	for time.Since(killedAt) < 4*time.Minute {
		ended, parked := settled(ctx, admin, all)
		if ended != prevEnded || parked != prevParked {
			lastProgress = time.Now()
			prevEnded, prevParked = ended, parked
		}
		if ended+parked >= len(all) {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	after := c.scrapeAll(ctx)
	eff := c.fake.effects()
	payStatus, smsStatus := statusCounts(ctx, admin, pays), statusCounts(ctx, admin, sms)
	var missingTransfer, oneTransfer int
	for _, id := range pays {
		switch eff.transferRuns[id] {
		case 0:
			missingTransfer++
		case 1:
			oneTransfer++
		}
	}
	var smsOnce, smsNone, smsMore, parkedSent, parkedUnsent int
	parkedIDs := map[string]bool{}
	rows, err := admin.Query(ctx, `SELECT id::text FROM runs WHERE id = ANY($1::uuid[]) AND status = 'needs_reconciliation'`, sms)
	if err == nil {
		for rows.Next() {
			var id string
			if rows.Scan(&id) == nil {
				parkedIDs[id] = true
			}
		}
		rows.Close()
	}
	for _, id := range sms {
		n := eff.smsRuns[id]
		switch n {
		case 0:
			smsNone++
		case 1:
			smsOnce++
		default:
			smsMore++
		}
		if parkedIDs[id] {
			if n > 0 {
				parkedSent++
			} else {
				parkedUnsent++
			}
		}
	}
	expiries := after.sum("taskiem_lease_expiries_total", nil) - before.sum("taskiem_lease_expiries_total", nil)
	fmt.Printf("\n=== kill -9 a worker mid-step ===\n")
	fmt.Printf("transfer runs (idempotent write): %d started; statuses %v; runs that moved money exactly once %d, never %d, more than once %d; references POSTed again and deduplicated by the provider %d\n",
		len(pays), payStatus, oneTransfer, missingTransfer, eff.DuplicateRuns, eff.RetriedRefs)
	fmt.Printf("SMS runs (unsafe write): %d started; statuses %v; SMS sent once %d, never %d, more than once %d; parked after sending %d, parked without sending %d\n",
		len(sms), smsStatus, smsOnce, smsNone, smsMore, parkedSent, parkedUnsent)
	fmt.Printf("lease expiries recovered (taskiem_lease_expiries_total) %.0f; last run settled %s after the kill\n", expiries, lastProgress.Sub(killedAt).Round(time.Millisecond))
	verdict := "PASS"
	if eff.DuplicateRuns > 0 || smsMore > 0 || payStatus["completed"] != len(pays) || smsStatus["completed"]+smsStatus["needs_reconciliation"] != len(sms) {
		verdict = "FAIL"
	}
	fmt.Println("verdict:", verdict)
}

// chaosRestartOrchestrator holds a steady rate while the orchestrator is
// killed (and restarted 3 s later), then stopped gracefully and restarted.
func chaosRestartOrchestrator(ctx context.Context, c *cluster, admin *pgxpool.Pool) {
	orch := c.procs[2]
	before := c.scrapeAll(ctx)
	start := time.Now()
	// Runs ended per second, from the start.
	var tl sync.Mutex
	var timeline []int
	stopTL := make(chan struct{})
	go func() {
		var prev int
		_ = admin.QueryRow(ctx, `SELECT count(*) FROM runs WHERE ended_at IS NOT NULL`).Scan(&prev)
		t := time.NewTicker(time.Second)
		defer t.Stop()
		for {
			select {
			case <-stopTL:
				return
			case <-t.C:
			}
			var ended int
			_ = admin.QueryRow(ctx, `SELECT count(*) FROM runs WHERE ended_at IS NOT NULL`).Scan(&ended)
			tl.Lock()
			timeline = append(timeline, ended-prev)
			tl.Unlock()
			prev = ended
		}
	}()
	var ids []string
	done := make(chan struct{})
	go func() { ids = c.startRuns("/load", 600, 10); close(done) }() // 60 s
	type mark struct {
		at   time.Duration
		what string
	}
	var marks []mark
	time.Sleep(20 * time.Second)
	marks = append(marks, mark{time.Since(start), "kill -9 orchestrator"})
	c.kill(orch)
	time.Sleep(3 * time.Second)
	c.restart(orch)
	marks = append(marks, mark{time.Since(start), "orchestrator ready again"})
	time.Sleep(15 * time.Second)
	marks = append(marks, mark{time.Since(start), "SIGTERM orchestrator"})
	c.stopGracefully(orch)
	marks = append(marks, mark{time.Since(start), "orchestrator stopped"})
	c.restart(orch)
	marks = append(marks, mark{time.Since(start), "orchestrator ready again"})
	<-done
	for time.Since(start) < 4*time.Minute {
		if ended, _ := settled(ctx, admin, ids); ended >= len(ids) {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	time.Sleep(1100 * time.Millisecond)
	close(stopTL)
	after := c.scrapeAll(ctx)
	fmt.Printf("\n=== orchestrator restarts under 10 runs/s (30 worker steps/s) ===\n")
	for _, m := range marks {
		fmt.Printf("  at %6s  %s\n", m.at.Round(100*time.Millisecond), m.what)
	}
	st := statusCounts(ctx, admin, ids)
	eff := c.fake.effects()
	fmt.Printf("runs %d; statuses %v; runs that moved money more than once %d; runs decided by the sweep %.0f\n", len(ids), st, eff.DuplicateRuns,
		after.sum("taskiem_runs_swept_total", nil)-before.sum("taskiem_runs_swept_total", nil))
	tl.Lock()
	defer tl.Unlock()
	var longest, cur int
	for _, n := range timeline[:max(len(timeline)-1, 0)] {
		if n == 0 {
			cur++
			longest = max(longest, cur)
		} else {
			cur = 0
		}
	}
	fmt.Printf("runs ended per second: %v\nlongest stretch with no run ending while runs were arriving: %d s\n", timeline, longest)
	for _, s := range slis(before, after) {
		fmt.Printf("  %-17s %6.0f events good %7.3f%% (target %.1f%%) p95 %s\n", s.Name, s.Total, 100*s.Ratio(), 100*s.Target, fmtSec(s.P95))
	}
}

// chaosReplicaLag shows the api falling back to the primary while its read
// replica lags beyond the bound, and using it again once it catches up.
func chaosReplicaLag(ctx context.Context, c *cluster, admin *pgxpool.Pool, frozen string) {
	ids := c.startRuns("/load", 20, 10)
	for i := 0; i < 300; i++ {
		if ended, _ := settled(ctx, admin, ids); ended >= len(ids) {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	list := func() int {
		total := 0
		for _, t := range c.tenants {
			m, _, err := call("GET", "http://"+c.api.listen+"/v1/runs?limit=100", t.token, nil)
			if err != nil {
				log.Print(err)
				continue
			}
			if rs, ok := m["runs"].([]any); ok {
				total += len(rs)
			}
		}
		return total
	}
	gauges := func() string {
		s, _ := scrape(ctx, "http://"+c.api.metrics+"/metrics")
		var b strings.Builder
		fmt.Fprintf(&b, "lag %.1f s, replica in use %.0f, reads on replica %.0f, on primary (fallback) %.0f",
			s.sum("taskiem_db_replica_lag_seconds", nil), s.sum("taskiem_db_replica_in_use", nil),
			s.sum("taskiem_db_reads_total", func(l map[string]string) bool { return l["target"] == "replica" }),
			s.sum("taskiem_db_reads_total", func(l map[string]string) bool { return l["target"] == "primary" }))
		return b.String()
	}
	time.Sleep(3 * time.Second)
	fmt.Printf("\n=== read replica beyond the lag bound (5 s) ===\n")
	fmt.Printf("stand-in replica frozen an hour behind: run list shows %d of %d runs; %s\n", list(), len(ids), gauges())
	if frozen == "" {
		return
	}
	// "Catch up": keep the stand-in's heartbeat current. It still has no
	// runs, so a list served from it is empty: that is how we know where
	// the read ran.
	p, err := pgxpool.New(ctx, frozen)
	if err != nil {
		log.Fatal(err)
	}
	defer p.Close()
	stop := make(chan struct{})
	go func() {
		for {
			select {
			case <-stop:
				return
			case <-time.After(200 * time.Millisecond):
				_, _ = p.Exec(ctx, `UPDATE db_heartbeat SET beat_at = now()`)
			}
		}
	}()
	time.Sleep(5 * time.Second)
	fmt.Printf("stand-in caught up: run list shows %d runs (served by the stand-in, which has none); %s\n", list(), gauges())
	close(stop)
	_, _ = p.Exec(ctx, `UPDATE db_heartbeat SET beat_at = now() - interval '1 hour'`)
	time.Sleep(5 * time.Second)
	fmt.Printf("stand-in behind again: run list shows %d of %d runs; %s\n", list(), len(ids), gauges())
}
