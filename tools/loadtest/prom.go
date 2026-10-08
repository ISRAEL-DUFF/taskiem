package main

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"math"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/israel-duff/taskiem/engine/slo"
)

// sample is one line of the Prometheus text format.
type sample struct {
	name   string
	labels map[string]string
	value  float64
}

// snapshot is every sample scraped from every process at one moment.
type snapshot []sample

// scrape reads one /metrics page. Only what the SLIs need is parsed: no
// exemplars, timestamps are ignored.
func scrape(ctx context.Context, url string) (snapshot, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil) //nolint:gosec // metrics of processes this tool started
	resp, err := http.DefaultClient.Do(req)                             //nolint:gosec // as above
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	return parseMetrics(resp.Body)
}

func parseMetrics(r io.Reader) (snapshot, error) {
	var out snapshot
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		line := sc.Text()
		if line == "" || line[0] == '#' {
			continue
		}
		s := sample{labels: map[string]string{}}
		var rest string
		if i := strings.IndexAny(line, "{ "); i >= 0 && line[i] == '{' {
			s.name = line[:i]
			j := i + 1
			for j < len(line) && line[j] != '}' {
				eq := strings.IndexByte(line[j:], '=')
				if eq < 0 {
					return nil, fmt.Errorf("bad labels: %s", line)
				}
				key := strings.TrimLeft(line[j:j+eq], ", ")
				j += eq + 2 // past ="
				var v strings.Builder
				for j < len(line) && line[j] != '"' {
					if line[j] == '\\' && j+1 < len(line) {
						j++
						switch line[j] {
						case 'n':
							v.WriteByte('\n')
						default:
							v.WriteByte(line[j])
						}
					} else {
						v.WriteByte(line[j])
					}
					j++
				}
				s.labels[key] = v.String()
				j++ // closing quote
				if j < len(line) && line[j] == ',' {
					j++
				}
			}
			rest = line[j+1:]
		} else {
			sp := strings.IndexByte(line, ' ')
			if sp < 0 {
				continue
			}
			s.name, rest = line[:sp], line[sp:]
		}
		f := strings.Fields(rest)
		if len(f) == 0 {
			continue
		}
		v, err := strconv.ParseFloat(f[0], 64)
		if err != nil {
			continue
		}
		s.value = v
		out = append(out, s)
	}
	return out, sc.Err()
}

// sum adds every sample of name whose labels pass keep.
func (s snapshot) sum(name string, keep func(map[string]string) bool) float64 {
	var t float64
	for _, x := range s {
		if x.name == name && (keep == nil || keep(x.labels)) {
			t += x.value
		}
	}
	return t
}

// buckets returns the cumulative histogram of name (without _bucket) by
// upper bound, for samples passing keep.
func (s snapshot) buckets(name string, keep func(map[string]string) bool) map[float64]float64 {
	out := map[float64]float64{}
	for _, x := range s {
		if x.name != name+"_bucket" || (keep != nil && !keep(x.labels)) {
			continue
		}
		le, err := strconv.ParseFloat(x.labels["le"], 64)
		if err != nil {
			continue
		}
		out[le] += x.value
	}
	return out
}

// hist is the change in one histogram between two snapshots.
type hist struct {
	bounds []float64 // ascending, +Inf last
	cum    []float64 // cumulative counts
}

func histDelta(before, after snapshot, name string, keep func(map[string]string) bool) hist {
	a, b := after.buckets(name, keep), before.buckets(name, keep)
	var h hist
	for le := range a {
		h.bounds = append(h.bounds, le)
	}
	sort.Float64s(h.bounds)
	for _, le := range h.bounds {
		h.cum = append(h.cum, a[le]-b[le])
	}
	return h
}

func (h hist) count() float64 {
	if len(h.cum) == 0 {
		return 0
	}
	return h.cum[len(h.cum)-1]
}

// within is the count at or under bound (bound must be a bucket edge).
func (h hist) within(bound float64) float64 {
	for i, le := range h.bounds {
		if le >= bound-1e-12 {
			return h.cum[i]
		}
	}
	return h.count()
}

// quantile interpolates linearly inside the bucket, as histogram_quantile
// does. It is only as good as the buckets: report it with them.
func (h hist) quantile(q float64) float64 {
	n := h.count()
	if n == 0 {
		return math.NaN()
	}
	rank := q * n
	prevLe, prevCum := 0.0, 0.0
	for i, le := range h.bounds {
		if h.cum[i] >= rank {
			if math.IsInf(le, 1) {
				return math.Inf(1) // beyond the last finite bucket
			}
			if h.cum[i] == prevCum {
				return le
			}
			return prevLe + (le-prevLe)*(rank-prevCum)/(h.cum[i]-prevCum)
		}
		prevLe, prevCum = le, h.cum[i]
	}
	return prevLe
}

// sliResult is one SLO measured over a stage, from the processes' own
// metrics, judged against the target in engine/slo.
type sliResult struct {
	Name   string
	Target float64
	Good   float64
	Total  float64
	// P50, P95, P99 for latency SLIs (bucket-interpolated), NaN otherwise.
	P50, P95, P99 float64
}

func (r sliResult) Ratio() float64 {
	if r.Total == 0 {
		return math.NaN()
	}
	return r.Good / r.Total
}

// Met is true when the stage's SLI is at or above the target, or nothing
// was measured.
func (r sliResult) Met() bool { return r.Total == 0 || r.Ratio() >= r.Target }

var excludedRoutes = map[string]bool{"/healthz": true, "/readyz": true, "/status": true, "/status.json": true, "/status/feed.atom": true, "/v1/runs/{run}/stream": true}

func target(name string) float64 {
	for _, s := range slo.SLOs {
		if s.Name == name {
			return s.Target
		}
	}
	panic("no SLO " + name)
}

// slis computes the SLIs of engine/slo the way its recording rules do,
// from the change between two snapshots summed across every process.
func slis(before, after snapshot) []sliResult {
	apiRoute := func(l map[string]string) bool { return !excludedRoutes[l["route"]] }
	lat := func(name, metric string, bound float64, keep func(map[string]string) bool) sliResult {
		h := histDelta(before, after, metric, keep)
		return sliResult{Name: name, Target: target(name), Good: h.within(bound), Total: h.count(), P50: h.quantile(.5), P95: h.quantile(.95), P99: h.quantile(.99)}
	}
	ratio := func(name, metric string, keep, bad func(map[string]string) bool) sliResult {
		total := after.sum(metric, keep) - before.sum(metric, keep)
		errs := after.sum(metric, func(l map[string]string) bool { return keep(l) && bad(l) }) - before.sum(metric, func(l map[string]string) bool { return keep(l) && bad(l) })
		return sliResult{Name: name, Target: target(name), Good: total - errs, Total: total, P50: math.NaN(), P95: math.NaN(), P99: math.NaN()}
	}
	is5xx := func(k string) func(map[string]string) bool {
		return func(l map[string]string) bool { return strings.HasPrefix(l[k], "5") }
	}
	return []sliResult{
		ratio("api_availability", "taskiem_http_requests_total", apiRoute, is5xx("code")),
		lat("api_latency", "taskiem_http_request_duration_seconds", 1, apiRoute),
		ratio("webhook_ingest", "taskiem_ingest_deliveries_total", func(l map[string]string) bool { return l["kind"] == "webhook" || l["kind"] == "connector" }, is5xx("result")),
		lat("run_start", "taskiem_run_start_seconds", 5, nil),
		lat("step_dispatch", "taskiem_step_dispatch_delay_seconds", 0.05, func(l map[string]string) bool { return l["queue"] == "connector" || l["queue"] == "sandbox" }),
	}
}
