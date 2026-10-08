// Package telemetry holds the engine's Prometheus metrics and OpenTelemetry
// tracing (spec 15.2). Metrics register on the default registry; tracing is
// a no-op until Setup finds an OTLP endpoint.
package telemetry

import (
	"context"
	"errors"
	"os"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

// Tracer starts engine spans; tenant_id, run_id and step_id are attributes.
func Tracer() trace.Tracer { return otel.Tracer("github.com/israel-duff/taskiem") }

// Attribute keys.
var (
	TenantID = attribute.Key("tenant_id")
	RunID    = attribute.Key("run_id")
	StepID   = attribute.Key("step_id")
)

var (
	HTTPRequests = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "taskiem_http_requests_total", Help: "API requests by route, method and status.",
	}, []string{"route", "method", "code"})
	HTTPSeconds = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name: "taskiem_http_request_duration_seconds", Help: "API request latency by route.", Buckets: prometheus.DefBuckets,
	}, []string{"route"})
	Ingest = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "taskiem_ingest_deliveries_total", Help: "Inbound deliveries by kind (webhook, connector, schedule) and result.",
	}, []string{"kind", "result"})
	Steps = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "taskiem_steps_total", Help: "Steps executed by workers, by queue, connector (or step type) and outcome.",
	}, []string{"queue", "target", "outcome"})
	StepSeconds = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name: "taskiem_step_duration_seconds", Help: "Worker step execution time, by queue and connector (or step type).",
		Buckets: []float64{.005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5, 10, 30, 60},
	}, []string{"queue", "target"})
	TimersFired = promauto.NewCounter(prometheus.CounterOpts{
		Name: "taskiem_timers_fired_total", Help: "Timers fired by the scheduler.",
	})
	LeasesRecovered = promauto.NewCounter(prometheus.CounterOpts{
		Name: "taskiem_lease_expiries_total", Help: "Task and timer leases recovered after expiring.",
	})
	ConnectorDrift = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "taskiem_connector_drift_total", Help: "Connector outputs that departed from their declared schema, by connector, action and kind (type, enum, missing).",
	}, []string{"connector", "action", "kind"})
	RunsSwept = promauto.NewCounter(prometheus.CounterOpts{
		Name: "taskiem_runs_swept_total", Help: "Runs decided by the orchestrator sweep rather than inline.",
	})
	// LimitHits has no tenant label, so its series stay few; which tenant
	// hit a limit is in tenant_limit_hits and the tenant's alerts.
	LimitHits = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "taskiem_tenant_limit_hits_total", Help: "Times a tenant reached a plan limit, by limit (ingest_rate and max_running_runs hold runs back; others refuse).",
	}, []string{"limit"})
	RunsAdmitted = promauto.NewCounter(prometheus.CounterOpts{
		Name: "taskiem_runs_admitted_total", Help: "Runs held back by their tenant's limits and admitted later by the scheduler.",
	})
	// FirstRunSeconds is gate G4's measure: the share of observations at
	// or under 900 seconds is the share of signups that reached a first
	// successful run within 15 minutes of signing up.
	FirstRunSeconds = promauto.NewHistogram(prometheus.HistogramOpts{
		Name: "taskiem_onboarding_first_run_seconds", Help: "Time from a self-serve signup to the tenant's first successful run, observed once per tenant.",
		Buckets: []float64{60, 120, 300, 600, 900, 1800, 3600, 4 * 3600, 24 * 3600, 7 * 24 * 3600},
	})
	Signups = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "taskiem_signups_total", Help: "Self-serve signups by outcome (created, rate_limited, blocked_domain, invalid, exists, honeypot).",
	}, []string{"outcome"})
	// KeyChecksFailed counts key job checks where a tenant's key did not
	// work, by whether a customer key (BYOK) is involved; which tenant is in
	// the logs, the tenant's audit log and its key_health alerts.
	KeyChecksFailed = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "taskiem_tenant_key_checks_failed_total", Help: "Key job checks where a tenant key could not be unwrapped (customer: BYOK revoked, disabled or unreachable; platform: the KMS).",
	}, []string{"wrapped_by"})
	// TenantCodeRefused counts requests that would run tenant code in the
	// API process (flow compile, code checks, catalogue checks) refused
	// because the tenant held its share or the process was full.
	TenantCodeRefused = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "taskiem_tenant_code_refused_total", Help: "Requests to compile or check tenant code in the API process refused, by reason (tenant: its share in use; process: every slot in use).",
	}, []string{"reason"})

	// Service-level indicators (spec 15.3, decision 0023, docs/reliability.md).

	// DispatchDelay is how long a task waited ready in the queue before a
	// worker claimed it, on the database clock: step dispatch latency.
	DispatchDelay = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name: "taskiem_step_dispatch_delay_seconds", Help: "Time a ready task waited in its queue before a worker claimed it (database clock), by queue.",
		Buckets: []float64{.005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5, 10, 30, 60, 300},
	}, []string{"queue"})
	// RunStart is how long after its trigger was accepted a run's first
	// worker step was claimed. Runs whose first steps need no worker
	// (transforms, waits) are not observed.
	RunStart = promauto.NewHistogram(prometheus.HistogramOpts{
		Name: "taskiem_run_start_seconds", Help: "Time from a run being accepted to its first worker step being claimed (database clock).",
		Buckets: []float64{.05, .1, .25, .5, 1, 2, 5, 10, 30, 60, 300},
	})
	// SchedulerLateness is how late timers and schedules fire.
	SchedulerLateness = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name: "taskiem_scheduler_lateness_seconds", Help: "How long after it was due a timer or schedule fired, by kind (timer, schedule).",
		Buckets: []float64{.1, .25, .5, 1, 2, 5, 10, 30, 60, 300, 900},
	}, []string{"kind"})
	// SchedulerTick is when the scheduler (or orchestrator sweep) last
	// finished a round without error; an alert fires when it goes stale.
	SchedulerTick = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "taskiem_scheduler_last_tick_timestamp_seconds", Help: "Unix time the scheduler last completed a tick without error, by mode (scheduler, sweep).",
	}, []string{"mode"})
	// Draining is 1 from SIGTERM until the process exits: readiness has
	// flipped and in-flight work is finishing.
	Draining = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "taskiem_draining", Help: "1 while the process is shutting down: not ready, finishing in-flight work.",
	})
	// LeasesReleased counts leases handed back at shutdown, after draining.
	LeasesReleased = promauto.NewCounter(prometheus.CounterOpts{
		Name: "taskiem_leases_released_total", Help: "Task, timer and orchestration leases released at shutdown instead of left to expire.",
	})
	// Canary probes: the synthetic end-to-end check (engine/canary).
	CanaryProbes = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "taskiem_canary_probes_total", Help: "Synthetic canary probes by result (ok, or the stage that failed: accept, complete, output).",
	}, []string{"result"})
	CanarySeconds = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name: "taskiem_canary_duration_seconds", Help: "Synthetic canary timings, by phase (accept: webhook answered; complete: run finished).",
		Buckets: []float64{.05, .1, .25, .5, 1, 2, 5, 10, 20, 30, 60},
	}, []string{"phase"})
	CanaryLastSuccess = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "taskiem_canary_last_success_timestamp_seconds", Help: "Unix time of the last successful canary probe.",
	})
)

// QueueCollector reports queue depth and the age of the oldest ready task,
// read when Prometheus scrapes.
type QueueCollector struct{ Pool *pgxpool.Pool }

var (
	queueReady  = prometheus.NewDesc("taskiem_queue_ready", "Tasks ready to run and not leased.", []string{"queue"}, nil)
	queueLeased = prometheus.NewDesc("taskiem_queue_leased", "Tasks leased by a worker.", []string{"queue"}, nil)
	queueAge    = prometheus.NewDesc("taskiem_queue_oldest_ready_seconds", "Age of the oldest ready task.", []string{"queue"}, nil)
)

var (
	backlogRuns    = prometheus.NewDesc("taskiem_queued_runs", "Queued runs, by why they wait (tenant: plan limits; workflow: concurrency settings).", []string{"reason"}, nil)
	backlogTenants = prometheus.NewDesc("taskiem_queued_tenants", "Tenants with queued runs, by reason.", []string{"reason"}, nil)
	backlogMax     = prometheus.NewDesc("taskiem_queued_runs_max_tenant", "The largest one tenant's queued runs, by reason.", []string{"reason"}, nil)
)

func (c QueueCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- queueReady
	ch <- queueLeased
	ch <- queueAge
	ch <- backlogRuns
	ch <- backlogTenants
	ch <- backlogMax
}

func (c QueueCollector) Collect(ch chan<- prometheus.Metric) {
	rows, err := c.Pool.Query(context.Background(), `SELECT queue, ready, leased, oldest_ready_seconds FROM taskiem_queue_stats()`)
	if err != nil {
		return
	}
	defer rows.Close()
	for rows.Next() {
		var q string
		var ready, leased int64
		var age float64
		if rows.Scan(&q, &ready, &leased, &age) != nil {
			return
		}
		ch <- prometheus.MustNewConstMetric(queueReady, prometheus.GaugeValue, float64(ready), q)
		ch <- prometheus.MustNewConstMetric(queueLeased, prometheus.GaugeValue, float64(leased), q)
		ch <- prometheus.MustNewConstMetric(queueAge, prometheus.GaugeValue, age, q)
	}
	rows.Close()
	// One series per tenant would grow with the tenants; the total, how
	// many tenants have a backlog and the largest one stay bounded.
	brows, err := c.Pool.Query(context.Background(), `SELECT reason, runs, tenants, max_per_tenant FROM taskiem_backlog_stats()`)
	if err != nil {
		return
	}
	defer brows.Close()
	seen := map[string]bool{}
	for brows.Next() {
		var reason string
		var runs, tenants, most int64
		if brows.Scan(&reason, &runs, &tenants, &most) != nil {
			return
		}
		seen[reason] = true
		ch <- prometheus.MustNewConstMetric(backlogRuns, prometheus.GaugeValue, float64(runs), reason)
		ch <- prometheus.MustNewConstMetric(backlogTenants, prometheus.GaugeValue, float64(tenants), reason)
		ch <- prometheus.MustNewConstMetric(backlogMax, prometheus.GaugeValue, float64(most), reason)
	}
	for _, reason := range []string{"tenant", "workflow"} {
		if !seen[reason] {
			ch <- prometheus.MustNewConstMetric(backlogRuns, prometheus.GaugeValue, 0, reason)
			ch <- prometheus.MustNewConstMetric(backlogTenants, prometheus.GaugeValue, 0, reason)
			ch <- prometheus.MustNewConstMetric(backlogMax, prometheus.GaugeValue, 0, reason)
		}
	}
}

// Setup exports traces over OTLP/HTTP when OTEL_EXPORTER_OTLP_ENDPOINT (or
// the traces-specific variable) is set; otherwise tracing stays a no-op.
// The returned function flushes and stops the exporter.
func Setup(ctx context.Context, service, version string) (func(context.Context) error, error) {
	if os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT") == "" && os.Getenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT") == "" {
		return func(context.Context) error { return nil }, nil
	}
	exp, err := otlptracehttp.New(ctx)
	if err != nil {
		return nil, err
	}
	res, err := resource.Merge(resource.Default(), resource.NewSchemaless(
		attribute.String("service.name", service), attribute.String("service.version", version)))
	if err != nil && !errors.Is(err, resource.ErrSchemaURLConflict) {
		return nil, err
	}
	tp := sdktrace.NewTracerProvider(sdktrace.WithBatcher(exp), sdktrace.WithResource(res))
	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(propagation.TraceContext{}, propagation.Baggage{}))
	return tp.Shutdown, nil
}
