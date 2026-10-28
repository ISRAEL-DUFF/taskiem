package deploy

import (
	"os"
	"strings"
	"testing"
	"time"

	"sigs.k8s.io/yaml"
)

// TestDevComposeStaysLocal: the dev Compose file publishes Postgres (whose
// password is postgres), metrics and object storage on loopback only,
// since Docker's published ports bypass a host firewall such as ufw, and
// gives Taskiem longer to stop than workers take to drain.
func TestDevComposeStaysLocal(t *testing.T) {
	b, err := os.ReadFile("docker-compose.yml")
	if err != nil {
		t.Fatal(err)
	}
	var f struct {
		Services map[string]struct {
			Ports           []string `json:"ports"`
			StopGracePeriod string   `json:"stop_grace_period"`
		} `json:"services"`
	}
	if err := yaml.Unmarshal(b, &f); err != nil {
		t.Fatal(err)
	}
	// The web app on 8080 is what the file is for; everything else is local.
	for name, s := range f.Services {
		for _, p := range s.Ports {
			if strings.HasPrefix(p, "8080:") {
				continue
			}
			if !strings.HasPrefix(p, "127.0.0.1:") {
				t.Errorf("%s publishes %q on every interface", name, p)
			}
		}
	}
	// TASKIEM_WORKER_DRAIN (30s) plus the HTTP servers' shutdown, inside
	// Docker's grace before SIGKILL (10s unless set).
	d, err := time.ParseDuration(f.Services["taskiem"].StopGracePeriod)
	if err != nil || d < 45*time.Second {
		t.Errorf("taskiem stop_grace_period %q: want at least 45s", f.Services["taskiem"].StopGracePeriod)
	}
}
