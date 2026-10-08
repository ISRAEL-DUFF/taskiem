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

type promTest struct {
	Name           string          `json:"name,omitempty"`
	Interval       string          `json:"interval"`
	InputSeries    []promSeries    `json:"input_series"`
	AlertRuleTests []promAlertTest `json:"alert_rule_test"`
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
				Name:     "slow step dispatch burns the dispatch budget",
				Interval: "1m",
				InputSeries: []promSeries{
					{Series: `taskiem_step_dispatch_delay_seconds_bucket{queue="connector",le="0.05"}`, Values: "0+20x80"},
					{Series: `taskiem_step_dispatch_delay_seconds_count{queue="connector"}`, Values: "0+100x80"},
				},
				AlertRuleTests: []promAlertTest{{EvalTime: "70m", Alertname: "TaskiemStepDispatchBudgetBurn", ExpAlerts: []promAlert{
					expect("TaskiemStepDispatchBudgetBurn", map[string]string{"severity": "page", "slo": "step_dispatch", "window": "1h"}),
					expect("TaskiemStepDispatchBudgetBurn", map[string]string{"severity": "page", "slo": "step_dispatch", "window": "6h"}),
					expect("TaskiemStepDispatchBudgetBurn", map[string]string{"severity": "ticket", "slo": "step_dispatch", "window": "1d"}),
					expect("TaskiemStepDispatchBudgetBurn", map[string]string{"severity": "ticket", "slo": "step_dispatch", "window": "3d"}),
				}}},
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
				Name:     "a queue nobody serves",
				Interval: "1m",
				InputSeries: []promSeries{
					{Series: `taskiem_queue_oldest_ready_seconds{queue="sandbox"}`, Values: "0+60x20"},
					{Series: `taskiem_queue_oldest_ready_seconds{queue="container"}`, Values: "0+60x20"},
				},
				AlertRuleTests: []promAlertTest{{EvalTime: "18m", Alertname: "TaskiemQueueBacklog", ExpAlerts: []promAlert{
					expect("TaskiemQueueBacklog", map[string]string{"severity": "page", "queue": "sandbox"}),
				}}},
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
