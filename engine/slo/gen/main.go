// Command gen writes the files generated from engine/slo (go generate
// ./engine/slo): Prometheus rules and the Grafana dashboard, under deploy/
// and in the Helm chart.
package main

import (
	"log"
	"os"
	"path/filepath"

	"github.com/israel-duff/taskiem/engine/slo"
)

func main() {
	files, err := slo.Files()
	if err != nil {
		log.Fatal(err)
	}
	root := filepath.Join("..", "..") // go generate runs in engine/slo
	for path, b := range files {
		p := filepath.Join(root, path)
		if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
			log.Fatal(err)
		}
		if err := os.WriteFile(p, b, 0o644); err != nil { //nolint:gosec // committed, world-readable files
			log.Fatal(err)
		}
	}
}
