package deploy

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// pinned is a third-party image reference with its tag and digest, as the
// Dockerfile pins its bases: name:tag@sha256:<64 hex>.
var pinned = regexp.MustCompile(`^[a-z0-9./-]+:[A-Za-z0-9._-]+@sha256:[0-9a-f]{64}$`)

// TestVMImagesArePinnedByDigest: the VM kit pulls each third-party image
// it runs (Caddy holds every TLS key, the psql helper the owner's login,
// restic the backup credentials) by digest, so a setup or pull installs
// what was reviewed, not whatever a tag points at that day. Taskiem's own
// image is verified by signature and pinned by digest elsewhere.
func TestVMImagesArePinnedByDigest(t *testing.T) {
	compose, err := os.ReadFile("vm/docker-compose.yml")
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, m := range regexp.MustCompile(`(?m)^\s*image:\s*(\S+)\s*$`).FindAllStringSubmatch(string(compose), -1) {
		ref := m[1]
		if strings.Contains(ref, "TASKIEM_IMAGE") {
			continue
		}
		// ${VAR:-default}: an operator may override it; the default is pinned.
		if v := regexp.MustCompile(`^\$\{[A-Z_]+:-(.+)\}$`).FindStringSubmatch(ref); v != nil {
			ref = v[1]
		}
		n++
		if !pinned.MatchString(ref) {
			t.Errorf("docker-compose.yml: image %s is not pinned by digest", m[1])
		}
	}
	if n < 3 {
		t.Errorf("found %d third-party images in docker-compose.yml, want caddy, openbao and clamav", n)
	}
	script, err := os.ReadFile("vm/taskiem-vps")
	if err != nil {
		t.Fatal(err)
	}
	vars := regexp.MustCompile(`(?m)^([A-Z_]+_IMAGE)=(\S+)$`).FindAllStringSubmatch(string(script), -1)
	if len(vars) < 3 {
		t.Errorf("found %d *_IMAGE settings in taskiem-vps, want psql, restic and openbao", len(vars))
	}
	for _, v := range vars {
		if !pinned.MatchString(v[2]) {
			t.Errorf("taskiem-vps: %s=%s is not pinned by digest", v[1], v[2])
		}
		// The backup copies OpenBao's storage with the OpenBao that runs it.
		if v[1] == "OPENBAO_IMAGE" && !strings.Contains(string(compose), "${OPENBAO_IMAGE:-"+v[2]+"}") {
			t.Errorf("docker-compose.yml does not run taskiem-vps's OpenBao, %s", v[2])
		}
	}
}
