package telemetry

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// Database availability (decision 0024): failover retries, the read
// replica's lag and where reads went.
var (
	DBRetries = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "taskiem_db_retries_total", Help: "Transactions retried through a database outage or failover, by outcome (retried: one more attempt; recovered: succeeded after retrying; gave_up: the retry window ran out).",
	}, []string{"outcome"})
	DBReplicaLag = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "taskiem_db_replica_lag_seconds", Help: "How far the read replica is behind the primary, measured by a heartbeat row (-1: the replica cannot be reached).",
	})
	DBReplicaInUse = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "taskiem_db_replica_in_use", Help: "1 while read-only queries go to the read replica; 0 while they fall back to the primary (lagging beyond the bound, unreachable, or not configured).",
	})
	DBReads = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "taskiem_db_reads_total", Help: "Staleness-tolerant read-only transactions, by where they ran (replica, primary) and, for the primary, why (fallback, no_replica).",
	}, []string{"target"})
)

// PoolCollector reports queue depth by worker pool and queue (decision
// 0024), read when Prometheus scrapes. Pools are named by operators, so
// the series stay few.
type PoolCollector struct{ Pool *pgxpool.Pool }

var (
	poolReady  = prometheus.NewDesc("taskiem_pool_ready", "Tasks ready to run and not leased, by worker pool and queue.", []string{"pool", "queue"}, nil)
	poolLeased = prometheus.NewDesc("taskiem_pool_leased", "Tasks leased by a worker, by worker pool and queue.", []string{"pool", "queue"}, nil)
	poolAge    = prometheus.NewDesc("taskiem_pool_oldest_ready_seconds", "Age of the oldest ready task, by worker pool and queue.", []string{"pool", "queue"}, nil)
)

func (c PoolCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- poolReady
	ch <- poolLeased
	ch <- poolAge
}

func (c PoolCollector) Collect(ch chan<- prometheus.Metric) {
	rows, err := c.Pool.Query(context.Background(), `SELECT pool, queue, ready, leased, oldest_ready_seconds FROM taskiem_pool_stats()`)
	if err != nil {
		return
	}
	defer rows.Close()
	for rows.Next() {
		var pool, q string
		var ready, leased int64
		var age float64
		if rows.Scan(&pool, &q, &ready, &leased, &age) != nil {
			return
		}
		ch <- prometheus.MustNewConstMetric(poolReady, prometheus.GaugeValue, float64(ready), pool, q)
		ch <- prometheus.MustNewConstMetric(poolLeased, prometheus.GaugeValue, float64(leased), pool, q)
		ch <- prometheus.MustNewConstMetric(poolAge, prometheus.GaugeValue, age, pool, q)
	}
}
