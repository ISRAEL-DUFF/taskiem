package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

type clusterOpts struct {
	bin, rates, out, scenario, readDSN, profileFrom *string
	stage, drain, providerLatency                   *time.Duration
	workers                                         *int
	apiRate                                         *float64
	stopAfterBreach                                 *bool
	extraEnv                                        *string
}

func clusterFlags() clusterOpts {
	return clusterOpts{
		bin:             flag.String("bin", "", "cluster/chaos: the taskiem binary (go build -o ... ./cmd/taskiem)"),
		rates:           flag.String("rates", "5,10,20,40", "cluster: offered webhooks (runs) per second, one stage each"),
		stage:           flag.Duration("stage", 60*time.Second, "cluster: how long each rate is offered"),
		drain:           flag.Duration("drain", 2*time.Minute, "cluster: how long to wait for a stage's runs to end"),
		workers:         flag.Int("workers", 2, "cluster/chaos: worker processes"),
		apiRate:         flag.Float64("api-rate", 5, "cluster: API reads per second alongside the webhooks (run list and run detail)"),
		out:             flag.String("out", "", "cluster/chaos: directory for logs, profiles and the summary (default: a new temporary directory)"),
		profileFrom:     flag.String("profile-from", "", "cluster: take CPU and heap profiles at every rate at or above this (needs nothing else: TASKIEM_PPROF=true is set)"),
		providerLatency: flag.Duration("provider-latency", 20*time.Millisecond, "cluster/chaos: latency the fake provider adds to every request"),
		stopAfterBreach: flag.Bool("stop-after-breach", false, "cluster: stop after the first stage above the one that breached an SLO"),
		scenario:        flag.String("scenario", "kill-worker", "chaos: kill-worker, restart-orchestrator or replica-lag"),
		readDSN:         flag.String("read-dsn", "", "chaos replica-lag: TASKIEM_DATABASE_READ_URL for the api (default: a second, never-updated database standing in for a replica that stopped replaying)"),
		extraEnv:        flag.String("env", "", "cluster/chaos: extra KEY=VALUE settings for every process, comma-separated"),
	}
}

func runCluster(ctx context.Context, mode, dsn string, tenants int, o clusterOpts) {
	if *o.bin == "" {
		log.Fatal("-bin is required: go build -o /some/dir/taskiem ./cmd/taskiem")
	}
	out := *o.out
	if out == "" {
		var err error
		if out, err = os.MkdirTemp("", "taskiem-load-"); err != nil {
			log.Fatal(err)
		}
	}
	_ = os.MkdirAll(out, 0o750)
	u, _ := url.Parse(dsn)
	dbName := strings.TrimPrefix(u.Path, "/")
	// The probe's own queries are left out of the activity samples by name.
	admin := pool(ctx, withAppName(dsn, "loadtest"), "", 4)
	defer admin.Close()
	// Per-function call counts and times for this database only (new
	// sessions pick it up; the processes start after this).
	if _, err := admin.Exec(ctx, "ALTER DATABASE "+dbName+" SET track_functions = 'all'"); err != nil {
		log.Fatal(err)
	}
	var extra []string
	for _, kv := range strings.Split(*o.extraEnv, ",") {
		if kv = strings.TrimSpace(kv); kv != "" {
			extra = append(extra, kv)
		}
	}
	var frozen string
	if mode == "chaos" && *o.scenario == "replica-lag" {
		var env []string
		var drop func()
		env, frozen, drop = replicaEnv(ctx, dsn, *o.readDSN)
		defer drop()
		extra = append(extra, env...)
	}
	profile := *o.profileFrom != ""
	fmt.Printf("starting cluster: %d tenants, %d workers; logs in %s\n", tenants, *o.workers, out)
	c := newCluster(ctx, *o.bin, dsn, out, *o.workers, tenants, *o.providerLatency, extra, profile)
	defer c.close()
	fmt.Printf("go=%s cpus=%d database=%s load avg %s\n", runtime.Version(), runtime.NumCPU(), dbName, loadavg())

	if mode == "chaos" {
		runChaos(ctx, c, admin, *o.scenario, frozen)
		return
	}
	var from float64
	if profile {
		from, _ = strconv.ParseFloat(*o.profileFrom, 64)
	}
	var rows []string
	breachedAt := -1.0
	for _, r := range strings.Split(*o.rates, ",") {
		rate, err := strconv.ParseFloat(strings.TrimSpace(r), 64)
		if err != nil || rate <= 0 {
			log.Fatalf("bad rate %q", r)
		}
		dir := ""
		if profile && rate >= from {
			dir = out
		}
		st := c.runStage(ctx, admin, dbName, rate, *o.stage, *o.drain, *o.apiRate, dir)
		if dir != "" {
			for _, p := range c.procs {
				if p.role != "scheduler" {
					_ = c.profile(p, "heap", 0, filepath.Join(out, fmt.Sprintf("%s-heap-%g.pprof", p.name, rate)))
				}
			}
		}
		st.print(os.Stdout)
		rows = append(rows, st.summaryRow())
		if len(st.breached()) > 0 && breachedAt < 0 {
			breachedAt = rate
		} else if breachedAt >= 0 && *o.stopAfterBreach {
			break
		}
		time.Sleep(5 * time.Second) // let autovacuum and the sweep settle between stages
	}
	sum := summaryHeader + "\n" + strings.Join(rows, "\n") + "\n"
	fmt.Printf("\n%s", sum)
	if breachedAt >= 0 {
		fmt.Printf("\nknee: the first SLO breach was at %.0f runs/s\n", breachedAt)
	} else {
		fmt.Println("\nno SLO breached at the rates offered")
	}
	_ = os.WriteFile(filepath.Join(out, "summary.md"), []byte(sum), 0o600)
}

func withAppName(dsn, name string) string {
	u, err := url.Parse(dsn)
	if err != nil {
		return dsn
	}
	q := u.Query()
	q.Set("application_name", name)
	u.RawQuery = q.Encode()
	return u.String()
}

// replicaEnv points the api at a read "replica" that never advances: a
// separately migrated database whose heartbeat stays where migration left
// it. Its lag grows past the bound at once, so reads must fall back to the
// primary; a read served from it would find no runs at all.
func replicaEnv(ctx context.Context, dsn, readDSN string) (env []string, frozen string, drop func()) {
	drop = func() {}
	if readDSN == "" {
		frozen, drop = freshDB(ctx, dsn, false)
		readDSN = frozen
		fmt.Println("replica stand-in:", frozen)
		p, err := pgxpool.New(ctx, frozen)
		if err == nil {
			_, _ = p.Exec(ctx, `UPDATE db_heartbeat SET beat_at = now() - interval '1 hour'`)
			p.Close()
		}
	}
	return []string{"TASKIEM_DATABASE_READ_URL=" + readDSN, "TASKIEM_DATABASE_READ_MAX_LAG=5s"}, frozen, drop
}
