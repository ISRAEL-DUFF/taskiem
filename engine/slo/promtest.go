package slo

import (
	"fmt"

	"sigs.k8s.io/yaml"
)

var sprintf = fmt.Sprintf

// The rules' unit tests for promtool test rules: scenarios that must page
// and one that must not. Expected annotations are copied from the rules,
// so the scenarios test the expressions, not the wording.

type promSeries struct {
	Series string `json:"series"`
	Values string `json:"values"`
}

type promAlert struct {
	ExpLabels      map[string]string `json:"exp_labels"`
	ExpAnnotations map[string]string `json:"exp_annotations"`
}

type promAlertTest struct {
	EvalTime  string      `json:"eval_time"`
	Alertname string      `json:"alertname"`
	ExpAlerts []promAlert `json:"exp_alerts"`
}

type promSample struct {
	Labels string  `json:"labels"`
	Value  float64 `json:"value"`
}

type promExprTest struct {
	Expr       string       `json:"expr"`
	EvalTime   string       `json:"eval_time"`
	ExpSamples []promSample `json:"exp_samples"`
}

type promTest struct {
	Name           string          `json:"name,omitempty"`
	Interval       string          `json:"interval"`
	InputSeries    []promSeries    `json:"input_series"`
	AlertRuleTests []promAlertTest `json:"alert_rule_test,omitempty"`
	ExprTests      []promExprTest  `json:"promql_expr_test,omitempty"`
}

type promTestFile struct {
	RuleFiles          []string   `json:"rule_files"`
	EvaluationInterval string     `json:"evaluation_interval"`
	Tests              []promTest `json:"tests"`
}

func annotations(alert string, labels map[string]string) map[string]string {
	for _, g := range Rules().Groups {
		for _, r := range g.Rules {
			if r.Alert != alert {
				continue
			}
			match := true
			for k, v := range r.Labels {
				if labels[k] != v {
					match = false
				}
			}
			if match {
				return r.Annotations
			}
		}
	}
	return nil
}

func expect(alert string, labels map[string]string) promAlert {
	return promAlert{ExpLabels: labels, ExpAnnotations: annotations(alert, labels)}
}

// allBurns is every burn-rate window of an SLO's alert firing, in the
// rules' order.
func allBurns(alert, name string) []promAlert {
	out := make([]promAlert, 0, len(Burns))
	for _, b := range Burns {
		out = append(out, expect(alert, map[string]string{"severity": b.Severity, "slo": name, "window": b.Long}))
	}
	return out
}

// RulesTestYAML is deploy/prometheus/taskiem-rules.test.yaml.
func RulesTestYAML() ([]byte, error) {
	const api = `taskiem_http_requests_total{route="/v1/workflows",method="GET",code="%s"}`
	burn := func(window, severity string) promAlert {
		return expect("TaskiemAPIAvailabilityBudgetBurn", map[string]string{"severity": severity, "slo": "api_availability", "window": window})
	}
	f := promTestFile{
		RuleFiles:          []string{"taskiem-rules.yaml"},
		EvaluationInterval: "1m",
		Tests: []promTest{
			{
				Name:     "2% of API requests fail for an hour: every burn-rate window fires",
				Interval: "1m",
				InputSeries: []promSeries{
					{Series: sprintf(api, "200"), Values: "0+98x80"},
					{Series: sprintf(api, "500"), Values: "0+2x80"},
					// Health checks fail too, and must not count.
					{Series: `taskiem_http_requests_total{route="/readyz",method="GET",code="503"}`, Values: "0+1000x80"},
				},
				AlertRuleTests: []promAlertTest{{EvalTime: "70m", Alertname: "TaskiemAPIAvailabilityBudgetBurn", ExpAlerts: []promAlert{
					burn("1h", "page"), burn("6h", "page"), burn("1d", "ticket"), burn("3d", "ticket"),
				}}},
			},
			{
				Name:     "a healthy API and a draining pod's readiness failures do not alert",
				Interval: "1m",
				InputSeries: []promSeries{
					{Series: sprintf(api, "200"), Values: "0+100x80"},
					{Series: sprintf(api, "500"), Values: "0+0x80"},
					{Series: `taskiem_http_requests_total{route="/readyz",method="GET",code="503"}`, Values: "0+1000x80"},
				},
				AlertRuleTests: []promAlertTest{{EvalTime: "70m", Alertname: "TaskiemAPIAvailabilityBudgetBurn", ExpAlerts: []promAlert{}}},
			},
			{
				Name:     "slow step dispatch on any queue burns the dispatch budget",
				Interval: "1m",
				InputSeries: []promSeries{
					{Series: `taskiem_step_dispatch_delay_seconds_bucket{queue="container",le="0.05"}`, Values: "0+20x80"},
					{Series: `taskiem_step_dispatch_delay_seconds_count{queue="container"}`, Values: "0+100x80"},
				},
				AlertRuleTests: []promAlertTest{{EvalTime: "70m", Alertname: "TaskiemStepDispatchBudgetBurn", ExpAlerts: []promAlert{
					expect("TaskiemStepDispatchBudgetBurn", map[string]string{"severity": "page", "slo": "step_dispatch", "window": "1h"}),
					expect("TaskiemStepDispatchBudgetBurn", map[string]string{"severity": "page", "slo": "step_dispatch", "window": "6h"}),
					expect("TaskiemStepDispatchBudgetBurn", map[string]string{"severity": "ticket", "slo": "step_dispatch", "window": "1d"}),
					expect("TaskiemStepDispatchBudgetBurn", map[string]string{"severity": "ticket", "slo": "step_dispatch", "window": "3d"}),
				}}},
			},
			{
				// An hour's own ratio is NaN when nothing happened in it; the
				// 30-day figures must not be.
				Name:     "a quiet hour leaves the 30-day budget readable",
				Interval: "1m",
				InputSeries: []promSeries{
					{Series: sprintf(api, "200"), Values: "0+100x60 6000x60"},
					{Series: sprintf(api, "500"), Values: "0x120"},
				},
				ExprTests: []promExprTest{{
					Expr: `taskiem:slo_error_budget_remaining:ratio{slo="api_availability"}`, EvalTime: "120m",
					ExpSamples: []promSample{{Labels: `taskiem:slo_error_budget_remaining:ratio{slo="api_availability"}`, Value: 1}},
				}},
			},
			{
				Name:     "the scheduler stops ticking",
				Interval: "1m",
				InputSeries: []promSeries{
					{Series: `taskiem_scheduler_last_tick_timestamp_seconds{mode="scheduler"}`, Values: "0+60x5 300x10"},
				},
				AlertRuleTests: []promAlertTest{
					{EvalTime: "5m", Alertname: "TaskiemSchedulerStalled", ExpAlerts: []promAlert{}},
					{EvalTime: "12m", Alertname: "TaskiemSchedulerStalled", ExpAlerts: []promAlert{expect("TaskiemSchedulerStalled", map[string]string{"severity": "page"})}},
				},
			},
			{
				Name:     "the canary keeps failing",
				Interval: "1m",
				InputSeries: []promSeries{
					{Series: `taskiem_canary_probes_total{result="ok"}`, Values: "0+1x10 10x20"},
					{Series: `taskiem_canary_probes_total{result="complete"}`, Values: "0x10 0+1x20"},
				},
				AlertRuleTests: []promAlertTest{
					{EvalTime: "10m", Alertname: "TaskiemCanaryFailing", ExpAlerts: []promAlert{}},
					{EvalTime: "28m", Alertname: "TaskiemCanaryFailing", ExpAlerts: []promAlert{expect("TaskiemCanaryFailing", map[string]string{"severity": "page"})}},
				},
			},
			{
				Name:     "a queue nobody serves, in any pool",
				Interval: "1m",
				InputSeries: []promSeries{
					{Series: `taskiem_pool_oldest_ready_seconds{pool="shared",queue="sandbox"}`, Values: "0+60x20"},
					{Series: `taskiem_pool_oldest_ready_seconds{pool="shared",queue="container"}`, Values: "0+60x20"},
					{Series: `taskiem_pool_oldest_ready_seconds{pool="acme",queue="ai"}`, Values: "0+60x20"},
					// Served: tasks are claimed within seconds.
					{Series: `taskiem_pool_oldest_ready_seconds{pool="acme",queue="connector"}`, Values: "0+0x20"},
				},
				AlertRuleTests: []promAlertTest{{EvalTime: "18m", Alertname: "TaskiemQueueBacklog", ExpAlerts: []promAlert{
					expect("TaskiemQueueBacklog", map[string]string{"severity": "page", "pool": "acme", "queue": "ai"}),
					expect("TaskiemQueueBacklog", map[string]string{"severity": "page", "pool": "shared", "queue": "container"}),
					expect("TaskiemQueueBacklog", map[string]string{"severity": "page", "pool": "shared", "queue": "sandbox"}),
				}}},
			},
			{
				Name:     "2% of webhook deliveries are answered with a 5xx: every burn-rate window fires",
				Interval: "1m",
				InputSeries: []promSeries{
					{Series: `taskiem_ingest_deliveries_total{kind="webhook",result="200"}`, Values: "0+98x80"},
					{Series: `taskiem_ingest_deliveries_total{kind="connector",result="502"}`, Values: "0+2x80"},
					// Schedules are not deliveries from outside, and do not count.
					{Series: `taskiem_ingest_deliveries_total{kind="schedule",result="500"}`, Values: "0+1000x80"},
				},
				AlertRuleTests: []promAlertTest{{EvalTime: "70m", Alertname: "TaskiemWebhookIngestBudgetBurn", ExpAlerts: allBurns("TaskiemWebhookIngestBudgetBurn", "webhook_ingest")}},
			},
			{
				// A 4xx is the sender's problem (a bad signature, a limit).
				Name:     "refused webhook deliveries do not burn the ingest budget",
				Interval: "1m",
				InputSeries: []promSeries{
					{Series: `taskiem_ingest_deliveries_total{kind="webhook",result="200"}`, Values: "0+50x80"},
					{Series: `taskiem_ingest_deliveries_total{kind="webhook",result="401"}`, Values: "0+50x80"},
					{Series: `taskiem_ingest_deliveries_total{kind="webhook",result="500"}`, Values: "0x80"},
				},
				AlertRuleTests: []promAlertTest{{EvalTime: "70m", Alertname: "TaskiemWebhookIngestBudgetBurn", ExpAlerts: []promAlert{}}},
			},
			{
				Name:     "half of API requests take over a second: the latency budget burns",
				Interval: "1m",
				InputSeries: []promSeries{
					{Series: `taskiem_http_request_duration_seconds_bucket{route="/v1/workflows",method="GET",le="1"}`, Values: "0+50x80"},
					{Series: `taskiem_http_request_duration_seconds_count{route="/v1/workflows",method="GET"}`, Values: "0+100x80"},
					// A run's stream stays open by design, and does not count.
					{Series: `taskiem_http_request_duration_seconds_bucket{route="/v1/runs/{run}/stream",method="GET",le="1"}`, Values: "0x80"},
					{Series: `taskiem_http_request_duration_seconds_count{route="/v1/runs/{run}/stream",method="GET"}`, Values: "0+1000x80"},
				},
				AlertRuleTests: []promAlertTest{{EvalTime: "70m", Alertname: "TaskiemAPILatencyBudgetBurn", ExpAlerts: allBurns("TaskiemAPILatencyBudgetBurn", "api_latency")}},
			},
			{
				Name:     "most runs wait over 5 seconds for their first step: the run-start budget burns",
				Interval: "1m",
				InputSeries: []promSeries{
					{Series: `taskiem_run_start_seconds_bucket{le="5"}`, Values: "0+20x80"},
					{Series: `taskiem_run_start_seconds_count`, Values: "0+100x80"},
				},
				AlertRuleTests: []promAlertTest{{EvalTime: "70m", Alertname: "TaskiemRunStartBudgetBurn", ExpAlerts: allBurns("TaskiemRunStartBudgetBurn", "run_start")}},
			},
			{
				Name:     "most timers fire over 5 seconds late: the scheduler budget burns",
				Interval: "1m",
				InputSeries: []promSeries{
					{Series: `taskiem_scheduler_lateness_seconds_bucket{le="5"}`, Values: "0+20x80"},
					{Series: `taskiem_scheduler_lateness_seconds_count`, Values: "0+100x80"},
				},
				AlertRuleTests: []promAlertTest{{EvalTime: "70m", Alertname: "TaskiemSchedulerTimelinessBudgetBurn", ExpAlerts: allBurns("TaskiemSchedulerTimelinessBudgetBurn", "scheduler_timeliness")}},
			},
			{
				Name:     "timers on time do not alert",
				Interval: "1m",
				InputSeries: []promSeries{
					{Series: `taskiem_scheduler_lateness_seconds_bucket{le="5"}`, Values: "0+100x80"},
					{Series: `taskiem_scheduler_lateness_seconds_count`, Values: "0+100x80"},
				},
				AlertRuleTests: []promAlertTest{{EvalTime: "70m", Alertname: "TaskiemSchedulerTimelinessBudgetBurn", ExpAlerts: []promAlert{}}},
			},
			{
				// One a minute: 15 in 15 minutes, over the 10 allowed, and it
				// must last 15 minutes before anyone is told.
				Name:     "leases keep expiring",
				Interval: "1m",
				InputSeries: []promSeries{
					{Series: `taskiem_lease_expiries_total{kind="task"}`, Values: "0+1x50"},
				},
				AlertRuleTests: []promAlertTest{
					{EvalTime: "8m", Alertname: "TaskiemLeaseExpiries", ExpAlerts: []promAlert{}},
					{EvalTime: "45m", Alertname: "TaskiemLeaseExpiries", ExpAlerts: []promAlert{expect("TaskiemLeaseExpiries", map[string]string{"severity": "ticket"})}},
				},
			},
			{
				Name:     "a few leases expiring in a deploy do not alert",
				Interval: "1m",
				InputSeries: []promSeries{
					{Series: `taskiem_lease_expiries_total{kind="task"}`, Values: "0+1x5 5x45"},
				},
				AlertRuleTests: []promAlertTest{{EvalTime: "45m", Alertname: "TaskiemLeaseExpiries", ExpAlerts: []promAlert{}}},
			},
			{
				Name:     "the canary stops probing",
				Interval: "1m",
				InputSeries: []promSeries{
					{Series: `taskiem_canary_probes_total{result="ok"}`, Values: "0+1x10 10x50"},
				},
				AlertRuleTests: []promAlertTest{
					{EvalTime: "20m", Alertname: "TaskiemCanaryStopped", ExpAlerts: []promAlert{}},
					{EvalTime: "50m", Alertname: "TaskiemCanaryStopped", ExpAlerts: []promAlert{expect("TaskiemCanaryStopped", map[string]string{"severity": "ticket"})}},
				},
			},
			{
				Name:     "a Taskiem pod cannot be scraped",
				Interval: "1m",
				InputSeries: []promSeries{
					{Series: `up{job="taskiem-api",instance="a"}`, Values: "1x5 0x30"},
					{Series: `up{job="taskiem-api",instance="b"}`, Values: "1x35"},
					// Someone else's target is not Taskiem's alert.
					{Series: `up{job="postgres",instance="c"}`, Values: "0x35"},
				},
				AlertRuleTests: []promAlertTest{
					{EvalTime: "10m", Alertname: "TaskiemTargetDown", ExpAlerts: []promAlert{}},
					{EvalTime: "25m", Alertname: "TaskiemTargetDown", ExpAlerts: []promAlert{
						expect("TaskiemTargetDown", map[string]string{"severity": "ticket", "job": "taskiem-api", "instance": "a"}),
					}},
				},
			},
			{
				Name:     "the retention purge falls behind on one scheduler",
				Interval: "1m",
				InputSeries: []promSeries{
					// Overdue from 5h50m, growing; the other scheduler last
					// measured an empty backlog.
					{Series: `taskiem_retention_purge_oldest_overdue_seconds{instance="a"}`, Values: "21000+60x60"},
					{Series: `taskiem_retention_purge_oldest_overdue_seconds{instance="b"}`, Values: "0x60"},
				},
				AlertRuleTests: []promAlertTest{
					{EvalTime: "20m", Alertname: "TaskiemRetentionPurgeBehind", ExpAlerts: []promAlert{}},
					{EvalTime: "50m", Alertname: "TaskiemRetentionPurgeBehind", ExpAlerts: []promAlert{expect("TaskiemRetentionPurgeBehind", map[string]string{"severity": "ticket"})}},
				},
			},
		},
	}
	b, err := yaml.Marshal(f)
	if err != nil {
		return nil, err
	}
	head := "# Generated by go generate ./engine/slo. Do not edit.\n# promtool test rules deploy/prometheus/taskiem-rules.test.yaml\n"
	return append([]byte(head), b...), nil
}
