package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Every pod the Helm chart runs `serve` in shuts down as serve expects
// (docs/reliability.md#graceful-shutdown): it is told the readiness delay
// and the worker drain (TASKIEM_SHUTDOWN_DELAY, TASKIEM_WORKER_DRAIN), and
// its grace period is the chart's checked one, which covers both. A
// hard-coded grace period can be shorter than delay + drain, and then
// Kubernetes kills steps mid-flight.
func TestChartServePodsShutDownGracefully(t *testing.T) {
	files, err := filepath.Glob(filepath.Join("..", "..", "deploy", "helm", "taskiem", "templates", "*.yaml"))
	if err != nil || len(files) == 0 {
		t.Fatalf("chart templates: %v", err)
	}
	literal := regexp.MustCompile(`terminationGracePeriodSeconds:\s*\d`)
	serving := 0
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		src, name := string(b), filepath.Base(f)
		if literal.MatchString(src) {
			t.Errorf("%s hard-codes terminationGracePeriodSeconds: use include \"taskiem.gracePeriod\"", name)
		}
		if !strings.Contains(src, "args: [serve,") {
			continue
		}
		serving++
		for _, want := range []string{`terminationGracePeriodSeconds: {{ include "taskiem.gracePeriod"`, `include "taskiem.shutdownEnv"`} {
			if !strings.Contains(src, want) {
				t.Errorf("%s runs serve without %s", name, want)
			}
		}
	}
	if serving < 5 {
		t.Errorf("found serve in %d templates, want the roles, public, mail, container steps and worker pools", serving)
	}
}
