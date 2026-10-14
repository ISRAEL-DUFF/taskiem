package main

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// activity samples pg_stat_activity for the test database: what active
// backends are running and what they wait on. Without pg_stat_statements
// (it needs shared_preload_libraries, and the shared server cannot be
// restarted) this is where Postgres time goes, by sample count.
type activity struct {
	mu      sync.Mutex
	samples int
	active  int
	byQuery map[string]int // normalised query -> samples active
	byWait  map[string]int // wait_event_type:wait_event -> samples
	lock    int            // samples waiting on a heavyweight lock
	lockQ   map[string]int // query -> samples waiting on a lock
}

func newActivity() *activity {
	return &activity{byQuery: map[string]int{}, byWait: map[string]int{}, lockQ: map[string]int{}}
}

func normaliseQuery(q string) string {
	q = strings.Join(strings.Fields(q), " ")
	if len(q) > 110 {
		q = q[:110]
	}
	return q
}

func (a *activity) run(ctx context.Context, admin *pgxpool.Pool, db string, every time.Duration) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		rows, err := admin.Query(ctx, `SELECT coalesce(wait_event_type, ''), coalesce(wait_event, ''), query
			FROM pg_stat_activity WHERE datname = $1 AND state = 'active' AND application_name <> 'loadtest' AND backend_type = 'client backend'`, db)
		if err != nil {
			continue
		}
		a.mu.Lock()
		a.samples++
		for rows.Next() {
			var wt, we, q string
			if rows.Scan(&wt, &we, &q) != nil {
				continue
			}
			if strings.HasPrefix(q, "LISTEN") {
				continue
			}
			nq := normaliseQuery(q)
			a.active++
			a.byQuery[nq]++
			if wt != "" {
				a.byWait[wt+":"+we]++
			} else {
				a.byWait["CPU"]++
			}
			if wt == "Lock" {
				a.lock++
				a.lockQ[nq]++
			}
		}
		rows.Close()
		a.mu.Unlock()
	}
}

type kv struct {
	k string
	v int
}

func top(m map[string]int, n int) []kv {
	var out []kv
	for k, v := range m {
		out = append(out, kv{k, v})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].v > out[j].v || (out[i].v == out[j].v && out[i].k < out[j].k) })
	if len(out) > n {
		out = out[:n]
	}
	return out
}

func (a *activity) report(n int) string {
	a.mu.Lock()
	defer a.mu.Unlock()
	var b strings.Builder
	if a.samples == 0 {
		return "no samples\n"
	}
	fmt.Fprintf(&b, "  pg_stat_activity: %d samples, mean %.2f active backends, %.1f%% of active samples waiting on locks\n",
		a.samples, float64(a.active)/float64(a.samples), 100*float64(a.lock)/float64(max(a.active, 1)))
	fmt.Fprintf(&b, "  waits:")
	for _, x := range top(a.byWait, 8) {
		fmt.Fprintf(&b, " %s=%.0f%%", x.k, 100*float64(x.v)/float64(max(a.active, 1)))
	}
	b.WriteString("\n  top queries (share of active samples):\n")
	for _, x := range top(a.byQuery, n) {
		fmt.Fprintf(&b, "    %5.1f%%  %s\n", 100*float64(x.v)/float64(max(a.active, 1)), x.k)
	}
	if a.lock > 0 {
		b.WriteString("  lock waits by query:\n")
		for _, x := range top(a.lockQ, 5) {
			fmt.Fprintf(&b, "    %5d  %s\n", x.v, x.k)
		}
	}
	return b.String()
}

// tableStats is pg_stat_user_tables at a moment.
type tableStats map[string][4]int64 // seq_scan, seq_tup_read, idx_scan, n_tup_ins+upd+del

func readTableStats(ctx context.Context, admin *pgxpool.Pool) tableStats {
	out := tableStats{}
	rows, err := admin.Query(ctx, `SELECT relname, seq_scan, seq_tup_read, coalesce(idx_scan, 0), n_tup_ins + n_tup_upd + n_tup_del FROM pg_stat_user_tables`)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var n string
		var a, b, c, d int64
		if rows.Scan(&n, &a, &b, &c, &d) == nil {
			out[n] = [4]int64{a, b, c, d}
		}
	}
	return out
}

// seqScanReport lists the tables most read by sequential scans between
// two moments: a missing index shows here.
func seqScanReport(before, after tableStats, n int) string {
	type row struct {
		name               string
		seq, tup, idx, chg int64
	}
	var rs []row
	for k, a := range after {
		b := before[k]
		r := row{k, a[0] - b[0], a[1] - b[1], a[2] - b[2], a[3] - b[3]}
		if r.seq > 0 {
			rs = append(rs, r)
		}
	}
	sort.Slice(rs, func(i, j int) bool { return rs[i].tup > rs[j].tup })
	if len(rs) > n {
		rs = rs[:n]
	}
	var b strings.Builder
	b.WriteString("  sequential scans (table: scans, rows read, index scans, rows changed):\n")
	for _, r := range rs {
		fmt.Fprintf(&b, "    %-28s %8d %12d %10d %10d\n", r.name, r.seq, r.tup, r.idx, r.chg)
	}
	return b.String()
}

// cpuTicks is a process's user+system time in clock ticks (100 Hz).
func cpuTicks(pid int) uint64 {
	b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return 0
	}
	s := string(b)
	i := strings.LastIndexByte(s, ')')
	if i < 0 {
		return 0
	}
	f := strings.Fields(s[i+2:])
	if len(f) < 13 {
		return 0
	}
	u, _ := strconv.ParseUint(f[11], 10, 64)
	st, _ := strconv.ParseUint(f[12], 10, 64)
	return u + st
}

// backendPIDs are the Postgres backends serving the test database.
func backendPIDs(ctx context.Context, admin *pgxpool.Pool, db string) []int {
	rows, err := admin.Query(ctx, `SELECT pid FROM pg_stat_activity WHERE datname = $1`, db)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []int
	for rows.Next() {
		var p int
		if rows.Scan(&p) == nil {
			out = append(out, p)
		}
	}
	return out
}

// funcStats is pg_stat_user_functions (track_functions = all, set on the
// test database only): calls and time per SQL function. The engine's hot
// paths (claim, finish, append) are functions, so this stands in for
// pg_stat_statements, which needs a server restart to load.
type funcStats map[string][3]float64 // calls, total ms, self ms

func readFunctionStats(ctx context.Context, admin *pgxpool.Pool) funcStats {
	out := funcStats{}
	rows, err := admin.Query(ctx, `SELECT funcname, calls, total_time, self_time FROM pg_stat_user_functions`)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var n string
		var c int64
		var t, s float64
		if rows.Scan(&n, &c, &t, &s) == nil {
			v := out[n] // overloads add up
			out[n] = [3]float64{v[0] + float64(c), v[1] + t, v[2] + s}
		}
	}
	return out
}

func functionReport(before, after funcStats, steps, n int) string {
	type row struct {
		name               string
		calls, total, self float64
	}
	var rs []row
	for k, a := range after {
		b := before[k]
		if r := (row{k, a[0] - b[0], a[1] - b[1], a[2] - b[2]}); r.calls > 0 {
			rs = append(rs, r)
		}
	}
	sort.Slice(rs, func(i, j int) bool { return rs[i].total > rs[j].total })
	if len(rs) > n {
		rs = rs[:n]
	}
	var b strings.Builder
	b.WriteString("  SQL functions (calls, per worker step, total ms, mean ms):\n")
	for _, r := range rs {
		fmt.Fprintf(&b, "    %-36s %8.0f %6.2f %10.0f %8.2f\n", r.name, r.calls, r.calls/float64(max(steps, 1)), r.total, r.total/r.calls)
	}
	return b.String()
}

// dbCommits is the test database's committed transactions so far.
func dbCommits(ctx context.Context, admin *pgxpool.Pool, db string) int64 {
	var n int64
	_ = admin.QueryRow(ctx, `SELECT xact_commit FROM pg_stat_database WHERE datname = $1`, db).Scan(&n)
	return n
}

func loadavg() string {
	b, err := os.ReadFile("/proc/loadavg")
	if err != nil {
		return "?"
	}
	f := strings.Fields(string(b))
	return strings.Join(f[:3], " ")
}
