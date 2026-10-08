package main

import (
	"context"
	"fmt"
	"log"
	"math"
	"math/rand/v2"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// stage is one rate held for a while, then drained.
type stage struct {
	Rate          float64 // offered webhooks (runs) per second
	Offered       int
	Accepted      int
	Refused       map[int]int // status -> count (4xx, 5xx)
	ClientErrors  int         // no answer
	AcceptP50     time.Duration
	AcceptP95     time.Duration
	AcceptP99     time.Duration
	APICalls      int
	APIP95        time.Duration
	OfferedFor    time.Duration
	DrainedIn     time.Duration // from the end of offering to the last run ending
	Completed     int
	NotEnded      int
	Failed        int
	AchievedRuns  float64 // runs ended / (offer + drain)
	E2EP50        time.Duration
	E2EP95        time.Duration
	E2EP99        time.Duration
	SLIs          []sliResult
	HostBusy      float64
	LoadBefore    string
	LoadAfter     string
	ProcCPU       map[string]float64 // cores used, by process
	PGCPU         float64            // cores used by the test database's backends
	Activity      string
	SeqScans      string
	Functions     string
	Commits       int64
	MaxLeased     int
	MaxReady      int
	Duplicates    int
	TransfersSeen int
}

func durPct(xs []time.Duration, p float64) time.Duration {
	if len(xs) == 0 {
		return 0
	}
	sort.Slice(xs, func(i, j int) bool { return xs[i] < xs[j] })
	return xs[int(float64(len(xs)-1)*p/100)]
}

// runStage offers webhooks at rate for d, with API reads alongside, waits
// for the accepted runs to end, and measures everything.
func (c *cluster) runStage(ctx context.Context, admin *pgxpool.Pool, dbName string, rate float64, d, drainLimit time.Duration, apiRate float64, profileDir string) stage {
	st := stage{Rate: rate, Refused: map[int]int{}, ProcCPU: map[string]float64{}}
	client := &http.Client{Timeout: 30 * time.Second, Transport: &http.Transport{MaxIdleConnsPerHost: 512, MaxConnsPerHost: 0}}

	act := newActivity()
	sampleCtx, stopSample := context.WithCancel(ctx)
	go act.run(sampleCtx, admin, dbName, 100*time.Millisecond)
	var maxLeased, maxReady atomic.Int64
	go func() {
		t := time.NewTicker(250 * time.Millisecond)
		defer t.Stop()
		for {
			select {
			case <-sampleCtx.Done():
				return
			case <-t.C:
			}
			var leased, ready int64
			_ = admin.QueryRow(sampleCtx, `SELECT count(*) FILTER (WHERE lease_owner IS NOT NULL), count(*) FILTER (WHERE lease_owner IS NULL AND available_at <= now()) FROM tasks`).Scan(&leased, &ready)
			if leased > maxLeased.Load() {
				maxLeased.Store(leased)
			}
			if ready > maxReady.Load() {
				maxReady.Store(ready)
			}
		}
	}()

	before := c.scrapeAll(ctx)
	tbl0 := readTableStats(ctx, admin)
	fn0 := readFunctionStats(ctx, admin)
	cpu0 := cpuSample()
	commit0 := dbCommits(ctx, admin, dbName)
	st.LoadBefore = loadavg()
	procTicks0 := map[string]uint64{}
	for _, p := range c.procs {
		procTicks0[p.name] = cpuTicks(c.pid(p))
	}
	pg0 := map[int]uint64{}
	for _, pid := range backendPIDs(ctx, admin, dbName) {
		pg0[pid] = cpuTicks(pid)
	}

	// Profiles while the stage runs.
	var profWG sync.WaitGroup
	if profileDir != "" {
		secs := int(d.Seconds()*0.8) - 2
		if secs < 3 {
			secs = 3
		}
		for _, p := range c.procs {
			if p.role == "scheduler" {
				continue
			}
			profWG.Add(1)
			go func(p *proc) {
				defer profWG.Done()
				time.Sleep(time.Second)
				f := filepath.Join(profileDir, fmt.Sprintf("%s-cpu-%g.pprof", p.name, rate))
				if err := c.profile(p, "profile", secs, f); err != nil {
					log.Printf("profile %s: %v", p.name, err)
				}
			}(p)
		}
	}

	var mu sync.Mutex
	var ids []string
	byTenant := map[string][]string{} // tenant -> its runs, for run reads with its token
	var accept, apiLat []time.Duration
	var wg sync.WaitGroup
	start := time.Now()
	interval := time.Duration(float64(time.Second) / rate)
	next := start
	var apiNext time.Time
	if apiRate > 0 {
		apiNext = start
	}
	for i := 0; time.Since(start) < d; i++ {
		now := time.Now()
		if now.Before(next) {
			time.Sleep(next.Sub(now))
		}
		next = next.Add(interval)
		st.Offered++
		t := c.tenants[i%len(c.tenants)]
		wg.Add(1)
		go func(i int, t tenant) {
			defer wg.Done()
			id, code, took, err := c.hook(client, t, "/load", map[string]any{"n": c.seq.Add(1)})
			mu.Lock()
			defer mu.Unlock()
			switch {
			case err == nil:
				ids = append(ids, id)
				byTenant[t.id] = append(byTenant[t.id], id)
				accept = append(accept, took)
				if code != http.StatusAccepted {
					st.Refused[code]++ // 200: a duplicate delivery, answered with the first run
				}
			case code == 0:
				st.ClientErrors++
			default:
				st.Refused[code]++
			}
		}(i, t)
		for apiRate > 0 && !time.Now().Before(apiNext) {
			apiNext = apiNext.Add(time.Duration(float64(time.Second) / apiRate))
			wg.Add(1)
			go func(k int) {
				defer wg.Done()
				t := c.tenants[k%len(c.tenants)]
				path := "/v1/runs?limit=20"
				mu.Lock()
				if mine := byTenant[t.id]; k%2 == 1 && len(mine) > 0 {
					path = "/v1/runs/" + mine[rand.IntN(len(mine))] //nolint:gosec // picking a run to read
				}
				mu.Unlock()
				s := time.Now()
				_, _, err := call("GET", "http://"+c.api.listen+path, t.token, nil)
				took := time.Since(s)
				mu.Lock()
				st.APICalls++
				apiLat = append(apiLat, took)
				mu.Unlock()
				if err != nil {
					log.Printf("api: %v", err)
				}
			}(st.Offered)
		}
	}
	wg.Wait()
	st.OfferedFor = time.Since(start)
	offerEnd := time.Now()
	st.Accepted = len(ids)

	// Drain: wait for every accepted run to end.
	deadline := time.Now().Add(drainLimit)
	for {
		var found, notEnded int
		err := admin.QueryRow(ctx, `SELECT count(*), count(*) FILTER (WHERE ended_at IS NULL) FROM runs WHERE id = ANY($1::uuid[])`, ids).Scan(&found, &notEnded)
		if err != nil {
			log.Printf("drain: %v", err)
			notEnded = len(ids) // unknown: keep waiting
		} else if found < len(ids) {
			log.Printf("drain: %d of %d accepted runs not found", len(ids)-found, len(ids))
		}
		st.NotEnded = notEnded
		if notEnded == 0 || time.Now().After(deadline) {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	st.DrainedIn = time.Since(offerEnd)
	profWG.Wait()
	stopSample()

	after := c.scrapeAll(ctx)
	cpu1 := cpuSample()
	elapsed := time.Since(start)
	for _, p := range c.procs {
		st.ProcCPU[p.name] = float64(cpuTicks(c.pid(p))-procTicks0[p.name]) / 100 / elapsed.Seconds()
	}
	var pgTicks uint64
	for _, pid := range backendPIDs(ctx, admin, dbName) {
		pgTicks += cpuTicks(pid) - pg0[pid]
	}
	st.PGCPU = float64(pgTicks) / 100 / elapsed.Seconds()
	st.HostBusy, _ = cpu1.busyFraction(cpu0)
	st.LoadAfter = loadavg()
	st.Commits = dbCommits(ctx, admin, dbName) - commit0
	st.SLIs = slis(before, after)
	st.Activity = act.report(12)
	st.SeqScans = seqScanReport(tbl0, readTableStats(ctx, admin), 8)
	fn1 := readFunctionStats(ctx, admin)
	st.MaxLeased, st.MaxReady = int(maxLeased.Load()), int(maxReady.Load())
	st.AcceptP50, st.AcceptP95, st.AcceptP99 = durPct(accept, 50), durPct(accept, 95), durPct(accept, 99)
	st.APIP95 = durPct(apiLat, 95)

	var e2e []time.Duration
	rows, err := admin.Query(ctx, `SELECT status, extract(epoch FROM ended_at - started_at) FROM runs WHERE id = ANY($1::uuid[]) AND ended_at IS NOT NULL`, ids)
	if err == nil {
		for rows.Next() {
			var s string
			var secs float64
			if rows.Scan(&s, &secs) == nil {
				e2e = append(e2e, time.Duration(secs*float64(time.Second)))
				if s == "completed" {
					st.Completed++
				} else {
					st.Failed++
				}
			}
		}
		rows.Close()
	}
	st.E2EP50, st.E2EP95, st.E2EP99 = durPct(e2e, 50), durPct(e2e, 95), durPct(e2e, 99)
	st.AchievedRuns = float64(st.Completed+st.Failed) / elapsed.Seconds()
	st.Functions = functionReport(fn0, fn1, (st.Completed+st.Failed)*3, 10)
	eff := c.fake.effects()
	st.Duplicates, st.TransfersSeen = eff.DuplicateRuns, eff.Transfers
	return st
}

func fmtSec(s float64) string {
	switch {
	case math.IsNaN(s):
		return "-"
	case math.IsInf(s, 1):
		return "beyond buckets"
	case s < 1:
		return strconv.FormatFloat(s*1000, 'f', 1, 64) + " ms"
	}
	return strconv.FormatFloat(s, 'f', 2, 64) + " s"
}

func (st stage) print(w *os.File) {
	fmt.Fprintf(w, "\n=== stage %.0f runs/s (%.0f worker steps/s offered) ===\n", st.Rate, st.Rate*3)
	fmt.Fprintf(w, "offered %d in %s; accepted %d; answers other than 202 %v; no answer %d; accept p50/p95/p99 %s/%s/%s; API calls %d p95 %s\n",
		st.Offered, st.OfferedFor.Round(time.Millisecond), st.Accepted, st.Refused, st.ClientErrors,
		st.AcceptP50.Round(100*time.Microsecond), st.AcceptP95.Round(100*time.Microsecond), st.AcceptP99.Round(100*time.Microsecond), st.APICalls, st.APIP95.Round(100*time.Microsecond))
	fmt.Fprintf(w, "runs ended %d (completed %d, failed %d), not ended %d; drained %s after offering; achieved %.1f runs/s; run duration p50/p95/p99 %s/%s/%s\n",
		st.Completed+st.Failed, st.Completed, st.Failed, st.NotEnded, st.DrainedIn.Round(time.Millisecond), st.AchievedRuns,
		st.E2EP50.Round(time.Millisecond), st.E2EP95.Round(time.Millisecond), st.E2EP99.Round(time.Millisecond))
	fmt.Fprintf(w, "SLIs from the processes' metrics:\n")
	for _, s := range st.SLIs {
		verdict := "met"
		if !s.Met() {
			verdict = "BREACHED"
		}
		fmt.Fprintf(w, "  %-17s %8.0f events  good %7.3f%% (target %.1f%%) %-8s p50 %s p95 %s p99 %s\n", s.Name, s.Total, 100*s.Ratio(), 100*s.Target, verdict, fmtSec(s.P50), fmtSec(s.P95), fmtSec(s.P99))
	}
	names := make([]string, 0, len(st.ProcCPU))
	for k := range st.ProcCPU {
		names = append(names, k)
	}
	sort.Strings(names)
	fmt.Fprintf(w, "machine: host CPU busy %.0f%% of 4; load avg before %s, after %s; Postgres backends of this DB %.2f cores;", 100*st.HostBusy, st.LoadBefore, st.LoadAfter, st.PGCPU)
	for _, n := range names {
		fmt.Fprintf(w, " %s %.2f", n, st.ProcCPU[n])
	}
	fmt.Fprintf(w, " cores\npostgres commits %d (%.1f per worker step); max tasks leased %d, max ready %d; duplicate transfers %d of %d\n",
		st.Commits, float64(st.Commits)/math.Max(1, float64(st.Completed*3)), st.MaxLeased, st.MaxReady, st.Duplicates, st.TransfersSeen)
	fmt.Fprint(w, st.Activity)
	fmt.Fprint(w, st.Functions)
	fmt.Fprint(w, st.SeqScans)
}

func (st stage) breached() []string {
	var out []string
	for _, s := range st.SLIs {
		if !s.Met() {
			out = append(out, s.Name)
		}
	}
	return out
}

// summaryRow is the stage as one markdown table row.
func (st stage) summaryRow() string {
	sli := map[string]sliResult{}
	for _, s := range st.SLIs {
		sli[s.Name] = s
	}
	pct := func(n string) string {
		s := sli[n]
		if s.Total == 0 {
			return "-"
		}
		return strconv.FormatFloat(100*s.Ratio(), 'f', 2, 64) + "%"
	}
	br := strings.Join(st.breached(), ", ")
	if br == "" {
		br = "none"
	}
	return fmt.Sprintf("| %.0f | %.0f | %.1f | %d/%d | %s | %s (p95 %s) | %s (p95 %s) | %s | %s (p95 %s) | %s | %.0f%% | %s → %s | %s |",
		st.Rate, st.Rate*3, st.AchievedRuns, st.Accepted, st.Offered, pct("webhook_ingest"),
		pct("run_start"), fmtSec(sli["run_start"].P95), pct("step_dispatch"), fmtSec(sli["step_dispatch"].P95),
		st.AcceptP95.Round(100*time.Microsecond), pct("api_latency"), fmtSec(sli["api_latency"].P95),
		st.E2EP95.Round(time.Millisecond), 100*st.HostBusy, st.LoadBefore, st.LoadAfter, br)
}

const summaryHeader = "| Runs/s offered | Worker steps/s | Runs/s achieved | Accepted | Webhook ingest | Run start ≤ 5 s | Dispatch ≤ 50 ms | Accept p95 (client) | API ≤ 1 s | Run p95 | Host CPU | Load avg | SLOs breached |\n| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |"
