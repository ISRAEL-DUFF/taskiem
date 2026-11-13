package deploy

import (
	"os"
	"regexp"
	"strconv"
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

// mebibytes reads a Compose ("2g", "256m") or Kubernetes ("2Gi", "256Mi")
// memory size.
func mebibytes(t *testing.T, v string) int {
	t.Helper()
	m := regexp.MustCompile(`^(\d+)([mMgG])i?$`).FindStringSubmatch(v)
	if m == nil {
		t.Fatalf("memory size %q", v)
	}
	n, _ := strconv.Atoi(m[1])
	if m[2] == "g" || m[2] == "G" {
		n *= 1024
	}
	return n
}

// TestVMComposeCapsEveryService: on one VM every container shares the
// machine's memory, and without limits the kernel's out-of-memory killer
// picks its own victim: ClamAV's signatures alone take gigabytes, and the
// victim could be OpenBao or Taskiem. Every service has a memory and a
// process limit, the same as the chart's where the chart has one, and
// ClamAV is the one to go first.
func TestVMComposeCapsEveryService(t *testing.T) {
	b, err := os.ReadFile("vm/docker-compose.yml")
	if err != nil {
		t.Fatal(err)
	}
	var f struct {
		Services map[string]struct {
			MemLimit  string `json:"mem_limit"`
			PidsLimit int    `json:"pids_limit"`
			OOMScore  int    `json:"oom_score_adj"`
		} `json:"services"`
	}
	if err := yaml.Unmarshal(b, &f); err != nil {
		t.Fatal(err)
	}
	total := 0
	for name, s := range f.Services {
		if s.MemLimit == "" || s.PidsLimit <= 0 {
			t.Errorf("%s: mem_limit %q, pids_limit %d: both must be set", name, s.MemLimit, s.PidsLimit)
			continue
		}
		total += mebibytes(t, s.MemLimit)
	}
	v, err := os.ReadFile("helm/taskiem/values.yaml")
	if err != nil {
		t.Fatal(err)
	}
	type res struct {
		Resources struct {
			Limits struct {
				Memory string `json:"memory"`
			} `json:"limits"`
		} `json:"resources"`
	}
	var chart struct {
		Roles   map[string]res `json:"roles"`
		Public  res            `json:"public"`
		Mail    res            `json:"mail"`
		ClamAV  res            `json:"clamav"`
		Migrate res            `json:"migrations"`
	}
	if err := yaml.Unmarshal(v, &chart); err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]string{
		"taskiem": chart.Roles["all"].Resources.Limits.Memory, "taskiem-public": chart.Public.Resources.Limits.Memory,
		"taskiem-mail": chart.Mail.Resources.Limits.Memory, "clamav": chart.ClamAV.Resources.Limits.Memory, "migrate": chart.Migrate.Resources.Limits.Memory,
	} {
		if want == "" {
			t.Errorf("%s: the chart has no memory limit to compare with", name)
			continue
		}
		if got := f.Services[name].MemLimit; got == "" || mebibytes(t, got) != mebibytes(t, want) {
			t.Errorf("%s: mem_limit %q, the chart's limit is %s", name, got, want)
		}
	}
	// The scanner is the first to be killed, the key store the last.
	if f.Services["clamav"].OOMScore <= 0 || f.Services["openbao"].OOMScore >= 0 {
		t.Errorf("oom_score_adj: clamav %d (want above 0), openbao %d (want below 0)", f.Services["clamav"].OOMScore, f.Services["openbao"].OOMScore)
	}
	// docs/vm.md: 8 GiB is the comfortable size; everything at its limit fits in it.
	if total > 7*1024 {
		t.Errorf("the limits add up to %d MiB: more than an 8 GiB server can give", total)
	}
}
