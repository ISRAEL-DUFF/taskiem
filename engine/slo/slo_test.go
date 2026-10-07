package slo_test

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"sigs.k8s.io/yaml"

	"github.com/israel-duff/taskiem/engine/slo"
)

// The committed files are what go generate ./engine/slo writes now.
func TestGeneratedFilesAreCurrent(t *testing.T) {
	files, err := slo.Files()
	if err != nil {
		t.Fatal(err)
	}
	for path, want := range files {
		got, err := os.ReadFile(filepath.Join("..", "..", path))
		if err != nil || !bytes.Equal(got, want) {
			t.Errorf("%s is stale or missing (%v): run go generate ./engine/slo", path, err)
		}
	}
}

// GitHub's heading anchors: lower case, punctuation dropped, spaces to dashes.
func anchors(t *testing.T) map[string]bool {
	t.Helper()
	doc, err := os.ReadFile(filepath.Join("..", "..", "docs", "reliability.md"))
	if err != nil {
		t.Fatal(err)
	}
	drop := regexp.MustCompile(`[^a-z0-9 _-]`)
	out := map[string]bool{}
	for _, line := range strings.Split(string(doc), "\n") {
		if !strings.HasPrefix(line, "#") {
			continue
		}
		h := strings.ToLower(strings.TrimSpace(strings.TrimLeft(line, "#")))
		out[strings.ReplaceAll(drop.ReplaceAllString(h, ""), " ", "-")] = true
	}
	return out
}

// Every alert links a runbook section that exists, and every alert has one.
func TestEveryAlertHasARunbook(t *testing.T) {
	have := anchors(t)
	seen := map[string]bool{}
	for _, g := range slo.Rules().Groups {
		for _, r := range g.Rules {
			if r.Alert == "" {
				continue
			}
			seen[r.Alert] = true
			url := r.Annotations["runbook_url"]
			base, anchor, _ := strings.Cut(url, "#")
			if base != slo.RunbookBase || !have[anchor] {
				t.Errorf("%s: runbook %s has no section in docs/reliability.md", r.Alert, url)
			}
			if sev := r.Labels["severity"]; sev != "page" && sev != "ticket" {
				t.Errorf("%s: severity %q", r.Alert, sev)
			}
			if r.Annotations["summary"] == "" || r.Annotations["description"] == "" {
				t.Errorf("%s: no summary or description", r.Alert)
			}
		}
	}
	for _, a := range slo.AllAlerts() {
		if !seen[a] {
			t.Errorf("%s is not in the rules", a)
		}
	}
}

var metricRe = regexp.MustCompile(`\btaskiem_[a-z_]+`)

// Every metric the rules and the dashboard read is one the engine exports.
func TestRulesReadExportedMetrics(t *testing.T) {
	src, err := os.ReadFile(filepath.Join("..", "telemetry", "telemetry.go"))
	if err != nil {
		t.Fatal(err)
	}
	exported := map[string]bool{}
	for _, m := range regexp.MustCompile(`"(taskiem_[a-z_]+)"`).FindAllStringSubmatch(string(src), -1) {
		exported[m[1]] = true
	}
	check := func(where, expr string) {
		for _, m := range metricRe.FindAllString(expr, -1) {
			base := m
			for _, suffix := range []string{"_bucket", "_count", "_sum"} {
				if strings.HasSuffix(m, suffix) && exported[strings.TrimSuffix(m, suffix)] {
					base = strings.TrimSuffix(m, suffix)
				}
			}
			if !exported[base] {
				t.Errorf("%s reads %s, which the engine does not export", where, m)
			}
		}
		// Brackets balance (no promtool here; a cheap syntax check).
		for _, pair := range []string{"()", "{}", "[]"} {
			if strings.Count(expr, pair[:1]) != strings.Count(expr, pair[1:]) {
				t.Errorf("%s: unbalanced %s in %s", where, pair, expr)
			}
		}
	}
	recorded := map[string]bool{}
	for _, g := range slo.Rules().Groups {
		for _, r := range g.Rules {
			check(r.Record+r.Alert, r.Expr)
			if r.Record != "" {
				recorded[r.Record] = true
			}
		}
	}
	dash, err := slo.Dashboard()
	if err != nil {
		t.Fatal(err)
	}
	var d struct {
		Panels []struct {
			Title   string `json:"title"`
			Targets []struct {
				Expr string `json:"expr"`
			} `json:"targets"`
		} `json:"panels"`
	}
	if err := json.Unmarshal(dash, &d); err != nil {
		t.Fatal(err)
	}
	records := regexp.MustCompile(`taskiem:[a-z_:0-9]+`)
	for _, p := range d.Panels {
		for _, tg := range p.Targets {
			check("panel "+p.Title, tg.Expr)
			for _, r := range records.FindAllString(tg.Expr, -1) {
				if !recorded[r] {
					t.Errorf("panel %s reads %s, which no rule records", p.Title, r)
				}
			}
		}
	}
}

// The burn-rate pairs follow the SRE workbook: the 1h/5m page fires when 2%
// of a 30-day budget burns in an hour.
func TestBurnRates(t *testing.T) {
	var api slo.SLO
	for _, s := range slo.SLOs {
		if s.Name == "api_availability" {
			api = s
		}
		if s.Target <= 0.9 || s.Target >= 1 || s.Alert == "" || s.Total == "" || (s.Errors == "") == (s.Good == "") {
			t.Errorf("%s: malformed", s.Name)
		}
	}
	if api.Target != 0.999 {
		t.Fatalf("spec 15.3 sets API availability at 99.9%%, not %v", api.Target)
	}
	b, err := yaml.Marshal(slo.Rules())
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`taskiem:slo_errors:ratio_rate1h{slo="api_availability"} > (14.4 * 0.001)`,
		`taskiem:slo_errors:ratio_rate5m{slo="api_availability"} > (14.4 * 0.001)`,
		`taskiem:slo_errors:ratio_rate3d{slo="step_dispatch"} > (1 * 0.05)`,
	} {
		if !strings.Contains(string(b), want) {
			t.Errorf("rules lack %s", want)
		}
	}
	if n := strings.Count(string(b), "alert: TaskiemAPIAvailabilityBudgetBurn"); n != len(slo.Burns) {
		t.Errorf("%d burn-rate rules for API availability", n)
	}
}

// With promtool (TASKIEM_PROMTOOL or on PATH) the rules are checked and
// their unit tests run: fast burns page, a healthy API does not.
func TestPromtool(t *testing.T) {
	tool := os.Getenv("TASKIEM_PROMTOOL")
	if tool == "" {
		var err error
		if tool, err = exec.LookPath("promtool"); err != nil {
			t.Skip("promtool not found; set TASKIEM_PROMTOOL to run the rule tests")
		}
	}
	dir := filepath.Join("..", "..", "deploy", "prometheus")
	for _, args := range [][]string{{"check", "rules", "taskiem-rules.yaml"}, {"test", "rules", "taskiem-rules.test.yaml"}} {
		cmd := exec.Command(tool, args...) //nolint:gosec // the operator's promtool
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Errorf("promtool %s: %v\n%s", strings.Join(args, " "), err, out)
		}
	}
}
