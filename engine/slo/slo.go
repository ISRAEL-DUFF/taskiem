// Package slo holds Taskiem's service-level objectives as code (spec 15.3,
// Phase 4 P4-4, decision 0023). From one list it generates the Prometheus
// recording and multi-window, multi-burn-rate alerting rules and the
// Grafana dashboard shipped under deploy/ and in the Helm chart; a test
// keeps the committed files, the runbooks in docs/reliability.md and the
// engine's metrics in step with it. Regenerate with go generate ./engine/slo.
package slo

//go:generate go run ./gen

import (
	"fmt"
	"strings"
)

// RunbookBase is where alerts' runbook_url points; the Helm chart lets an
// operator replace it (prometheusRule.runbookBaseURL).
const RunbookBase = "https://github.com/israel-duff/taskiem/blob/main/docs/reliability.md"

// Period is the SLO window: error budgets are per rolling 30 days.
const Period = "30d"

// SLO is one objective. Its SLI is the share of good events: Errors and
// Total are PromQL selectors (or expressions) of counters whose rate()
// gives bad and all events. For latency SLOs, Good is a histogram bucket
// selector and errors are Total minus Good.
type SLO struct {
	// Name is the slo label and the runbook anchor (runbook-<name with dashes>).
	Name, Title string
	// Description says exactly what is measured, for the dashboard.
	Description string
	// Target is the objective, e.g. 0.999.
	Target float64
	// Errors, Good and Total: see SLO.
	Errors, Good, Total string
	// Alert is the alert's name; it pages on fast burn and opens a ticket
	// on slow burn.
	Alert string
}

// excluded are routes outside the API SLIs: probes, the status page itself,
// and streams that stay open by design.
const excluded = `route!~"/healthz|/readyz|/status|/status.json|/status/feed.atom|/v1/runs/\\{run\\}/stream"`

// SLOs are Taskiem's objectives. Spec 15.3 sets the first (99.9% monthly
// API and webhook availability) and the dispatch latency (p95 under 50
// ms); the others are ours (decision 0023).
var SLOs = []SLO{
	{
		Name: "api_availability", Title: "API availability", Target: 0.999, Alert: "TaskiemAPIAvailabilityBudgetBurn",
		Description: "Share of API requests (every route but probes, the status page and run streams) answered without a 5xx.",
		Errors:      `taskiem_http_requests_total{` + excluded + `,code=~"5.."}`,
		Total:       `taskiem_http_requests_total{` + excluded + `}`,
	},
	{
		Name: "api_latency", Title: "API latency", Target: 0.99, Alert: "TaskiemAPILatencyBudgetBurn",
		Description: "Share of API requests (same routes) answered within 1 second.",
		Good:        `taskiem_http_request_duration_seconds_bucket{` + excluded + `,le="1"}`,
		Total:       `taskiem_http_request_duration_seconds_count{` + excluded + `}`,
	},
	{
		Name: "webhook_ingest", Title: "Webhook ingest", Target: 0.999, Alert: "TaskiemWebhookIngestBudgetBurn",
		Description: "Share of webhook and connector-event deliveries answered without a 5xx (a 4xx is the sender's problem: bad signature, limits).",
		Errors:      `taskiem_ingest_deliveries_total{kind=~"webhook|connector",result=~"5.."}`,
		Total:       `taskiem_ingest_deliveries_total{kind=~"webhook|connector"}`,
	},
	{
		Name: "run_start", Title: "Run start latency", Target: 0.99, Alert: "TaskiemRunStartBudgetBurn",
		Description: "Share of runs whose first worker step is claimed within 5 seconds of the trigger being accepted.",
		Good:        `taskiem_run_start_seconds_bucket{le="5"}`,
		Total:       `taskiem_run_start_seconds_count`,
	},
	{
		Name: "step_dispatch", Title: "Step dispatch latency", Target: 0.95, Alert: "TaskiemStepDispatchBudgetBurn",
		Description: "Share of connector and sandbox steps claimed within 50 ms of becoming ready (spec 15.3: p95 under 50 ms).",
		Good:        `taskiem_step_dispatch_delay_seconds_bucket{queue=~"connector|sandbox",le="0.05"}`,
		Total:       `taskiem_step_dispatch_delay_seconds_count{queue=~"connector|sandbox"}`,
	},
	{
		Name: "scheduler_timeliness", Title: "Scheduler timeliness", Target: 0.99, Alert: "TaskiemSchedulerTimelinessBudgetBurn",
		Description: "Share of timers and schedules fired within 5 seconds of being due.",
		Good:        `taskiem_scheduler_lateness_seconds_bucket{le="5"}`,
		Total:       `taskiem_scheduler_lateness_seconds_count`,
	},
}

// Windows are the rate windows recorded for every SLO.
var Windows = []string{"5m", "30m", "1h", "2h", "6h", "1d", "3d"}

// BurnRule is one multi-window burn-rate condition (Google SRE workbook,
// "Alerting on SLOs", table 5-8): both windows must burn faster than Rate.
type BurnRule struct {
	Long, Short string
	Rate        float64
	Severity    string // page | ticket
}

// Burns are the alerting conditions every SLO gets: 2% of the 30-day
// budget in an hour or 5% in six hours pages; 10% in three days or a
// steady burn of the whole budget opens a ticket.
var Burns = []BurnRule{
	{"1h", "5m", 14.4, "page"},
	{"6h", "30m", 6, "page"},
	{"1d", "2h", 3, "ticket"},
	{"3d", "6h", 1, "ticket"},
}

// Anchor is the runbook section's anchor for an alert.
func Anchor(alert string) string { return "runbook-" + strings.ToLower(alert) }

// RunbookURL is an alert's runbook.
func RunbookURL(alert string) string { return RunbookBase + "#" + Anchor(alert) }

// ErrorRatio is the PromQL for an SLO's error ratio over a window.
func (s SLO) ErrorRatio(window string) string {
	total := fmt.Sprintf("sum(rate(%s[%s]))", s.Total, window)
	if s.Errors != "" {
		return fmt.Sprintf("sum(rate(%s[%s])) / %s", s.Errors, window, total)
	}
	return fmt.Sprintf("1 - sum(rate(%s[%s])) / %s", s.Good, window, total)
}

// Record is the recording rule name for an SLO's error ratio over a window.
func Record(window string) string { return "taskiem:slo_errors:ratio_rate" + window }

// Alert is a non-SLO alert: a symptom an SLO cannot see quickly enough or
// at all (nothing to measure when nothing runs).
type Alert struct {
	Name, Expr, For, Severity, Summary, Description string
}

// Alerts are the alerts beside the burn rates.
var Alerts = []Alert{
	{
		Name: "TaskiemCanaryFailing", Severity: "page", For: "0m",
		Summary:     "The synthetic canary has not completed a run in 15 minutes",
		Description: "Five or more canary probes failed in 15 minutes and none passed: a webhook through the public hooks URL did not become a finished run. Customers' runs are probably affected.",
		Expr:        `sum(increase(taskiem_canary_probes_total{result!="ok"}[15m])) >= 5 unless sum(increase(taskiem_canary_probes_total{result="ok"}[15m])) > 0`,
	},
	{
		Name: "TaskiemCanaryStopped", Severity: "ticket", For: "15m",
		Summary:     "The synthetic canary stopped probing",
		Description: "No canary probes ran in 15 minutes although the canary is configured. The availability signal it gives (and the status page's automatic check) is missing.",
		Expr:        `sum(increase(taskiem_canary_probes_total[15m])) == 0`,
	},
	{
		Name: "TaskiemSchedulerStalled", Severity: "page", For: "2m",
		Summary:     "No scheduler has completed a tick in 2 minutes",
		Description: "Timers, schedules, lease recovery, the orchestrator sweep and admission of queued runs have stopped (or no scheduler is running).",
		Expr:        `(time() - max(taskiem_scheduler_last_tick_timestamp_seconds{mode="scheduler"}) > 120) or absent(taskiem_scheduler_last_tick_timestamp_seconds{mode="scheduler"})`,
	},
	{
		Name: "TaskiemQueueBacklog", Severity: "page", For: "5m",
		Summary:     "Steps have waited over 10 minutes in a queue",
		Description: "The oldest ready task in a connector or sandbox queue is over 10 minutes old: workers are down, stuck or saturated. The dispatch SLO only sees steps that are claimed, so this catches a queue nobody serves.",
		Expr:        `max by (queue) (taskiem_queue_oldest_ready_seconds{queue=~"connector|sandbox"}) > 600`,
	},
	{
		Name: "TaskiemLeaseExpiries", Severity: "ticket", For: "15m",
		Summary:     "Task leases are expiring",
		Description: "More than 10 task or timer leases were recovered after expiring in 15 minutes: processes are dying or being killed before they drain.",
		Expr:        `sum(increase(taskiem_lease_expiries_total[15m])) > 10`,
	},
	{
		Name: "TaskiemTargetDown", Severity: "ticket", For: "10m",
		Summary:     "A Taskiem pod's metrics cannot be scraped",
		Description: "Prometheus cannot scrape a Taskiem pod. If it serves traffic, the API and ingest SLIs miss its requests.",
		Expr:        `up{job=~".*taskiem.*"} == 0`,
	},
}

// AllAlerts lists every alert name, burn-rate alerts first.
func AllAlerts() []string {
	var out []string
	for _, s := range SLOs {
		out = append(out, s.Alert)
	}
	for _, a := range Alerts {
		out = append(out, a.Name)
	}
	return out
}
