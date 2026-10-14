package main

import (
	"math"
	"os"
	"strings"
	"testing"
)

const page = `# HELP taskiem_run_start_seconds x
# TYPE taskiem_run_start_seconds histogram
taskiem_run_start_seconds_bucket{le="0.05"} 8
taskiem_run_start_seconds_bucket{le="0.1"} 9
taskiem_run_start_seconds_bucket{le="5"} 10
taskiem_run_start_seconds_bucket{le="+Inf"} 10
taskiem_run_start_seconds_sum 1.2
taskiem_run_start_seconds_count 10
taskiem_http_requests_total{code="200",method="GET",route="/v1/runs"} 7
taskiem_http_requests_total{code="503",method="GET",route="/v1/runs"} 1
taskiem_http_requests_total{code="200",method="GET",route="/healthz"} 100
taskiem_ingest_deliveries_total{kind="webhook",result="202"} 4
go_info{version="go1.26.0"} 1
`

func TestParseAndSLIs(t *testing.T) {
	before, err := parseMetrics(strings.NewReader(""))
	if err != nil {
		t.Fatal(err)
	}
	after, err := parseMetrics(strings.NewReader(page))
	if err != nil {
		t.Fatal(err)
	}
	if n := after.sum("taskiem_http_requests_total", func(l map[string]string) bool { return l["route"] == "/v1/runs" }); n != 8 {
		t.Errorf("requests %v", n)
	}
	h := histDelta(before, after, "taskiem_run_start_seconds", nil)
	if h.count() != 10 || h.within(5) != 10 || h.within(0.05) != 8 {
		t.Errorf("hist %+v", h)
	}
	if q := h.quantile(0.5); q <= 0 || q > 0.05 {
		t.Errorf("p50 %v", q)
	}
	got := map[string]sliResult{}
	for _, s := range slis(before, after) {
		got[s.Name] = s
	}
	if a := got["api_availability"]; a.Total != 8 || a.Good != 7 || a.Met() {
		t.Errorf("availability %+v", a)
	}
	if r := got["run_start"]; r.Total != 10 || !r.Met() {
		t.Errorf("run start %+v", r)
	}
	if !math.IsNaN(got["step_dispatch"].Ratio()) || !got["step_dispatch"].Met() {
		t.Errorf("no dispatch events should be met: %+v", got["step_dispatch"])
	}
}

func TestParseRealPage(t *testing.T) {
	f := os.Getenv("LOADTEST_PAGE")
	if f == "" {
		t.Skip()
	}
	b, _ := os.ReadFile(f)
	s, err := parseMetrics(strings.NewReader(string(b)))
	t.Logf("%d samples, err %v, run start count %v", len(s), err, histDelta(nil, s, "taskiem_run_start_seconds", nil).count())
}

func TestDebugScrape(t *testing.T) {
	urls := os.Getenv("LOADTEST_URLS")
	if urls == "" {
		t.Skip()
	}
	var all snapshot
	for _, u := range strings.Split(urls, ",") {
		s, err := scrape(t.Context(), u)
		t.Logf("%s: %d samples err %v count %v", u, len(s), err, histDelta(nil, s, "taskiem_run_start_seconds", nil).count())
		all = append(all, s...)
	}
	t.Logf("all: %v", histDelta(nil, all, "taskiem_run_start_seconds", nil).count())
}
