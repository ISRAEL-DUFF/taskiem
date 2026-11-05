package deploy

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// vps runs the real deploy/vm/taskiem-vps against a kit, settings and
// state in a temporary directory, with docker and curl replaced by stubs:
// three releases, taskiem:good (kit revA), taskiem:broken (revB, which
// never starts) and taskiem:good2 (revC), each built from a commit whose
// kit says which one it is.
type vps struct {
	t                         *testing.T
	dir, kit, state, bin, log string
}

func newVPS(t *testing.T) *vps {
	t.Helper()
	for _, tool := range []string{"bash", "tar", "flock"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s is not installed", tool)
		}
	}
	d := t.TempDir()
	v := &vps{t: t, dir: d, kit: filepath.Join(d, "kit"), state: filepath.Join(d, "state"), bin: filepath.Join(d, "usr-local-bin"), log: filepath.Join(d, "calls.log")}
	for _, p := range []string{v.kit, v.state, v.bin, filepath.Join(d, "etc"), filepath.Join(d, "stubs"), filepath.Join(d, "kits")} {
		if err := os.MkdirAll(p, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, rev := range []string{"revA", "revB", "revC"} {
		src := filepath.Join(d, "kits", "taskiem-"+rev, "deploy", "vm")
		if err := os.MkdirAll(src, 0o755); err != nil {
			t.Fatal(err)
		}
		for _, f := range []string{"docker-compose.yml", "Caddyfile", "openbao.hcl", "openbao-taskiem-policy.hcl", "public.caddy", "taskiem-vps"} {
			if err := os.WriteFile(filepath.Join(src, f), []byte("# kit "+rev+"\n"), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		if out, err := exec.Command("tar", "-czf", filepath.Join(d, "kits", rev+".tgz"), "-C", filepath.Join(d, "kits"), "taskiem-"+rev).CombinedOutput(); err != nil {
			t.Fatalf("tar: %v: %s", err, out)
		}
	}
	stub := func(name, body string) {
		if err := os.WriteFile(filepath.Join(d, "stubs", name), []byte("#!/usr/bin/env bash\n"+body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	// docker: compose up fails for the broken release, its container is
	// never healthy, and each image's revision label names its kit.
	stub("docker", `img=$(sed -n "s/^TASKIEM_IMAGE='\(.*\)'$/\1/p" "$TASKIEM_VPS_KIT/.env" 2>/dev/null)
case "$*" in
  *" up "*|*" up") echo "up $img $(head -1 "$TASKIEM_VPS_KIT/docker-compose.yml")" >>"$CALLS"; [ "$img" != taskiem:broken ] ;;
  *"ps -q"*) echo cid ;;
  *"config --services"*) echo taskiem ;;
  "image inspect"*) case "${@: -1}" in taskiem:good) echo revA ;; taskiem:broken) echo revB ;; taskiem:good2) echo revC ;; esac ;;
  inspect*) [ "$img" = taskiem:broken ] && echo unhealthy || echo healthy ;;
esac
`)
	// curl: the kit tarballs codeload would serve; everything else answers.
	stub("curl", `for a in "$@"; do case $a in https://codeload.github.com/*) echo "kit ${a##*/}" >>"$CALLS"; cat "$KITS/${a##*/}.tgz"; exit 0 ;; esac; done
exit 0
`)
	stub("id", `[ "${1:-}" = -u ] && { echo 0; exit 0; }; exec /usr/bin/id "$@"
`)
	// Installed: taskiem:good with its own kit.
	if out, err := exec.Command("tar", "-xzf", filepath.Join(d, "kits", "revA.tgz"), "-C", v.kit, "--strip-components=3").CombinedOutput(); err != nil {
		t.Fatalf("tar: %v: %s", err, out)
	}
	v.write(filepath.Join(v.kit, ".env"), "TASKIEM_IMAGE='taskiem:good'\n")
	v.write(filepath.Join(v.state, "current"), "taskiem:good\n")
	v.write(filepath.Join(v.bin, "taskiem-vps"), "# kit revA\n")
	v.write(filepath.Join(d, "etc", "vps.conf"), "") // WINDOW empty: any time, as on staging
	return v
}

func (v *vps) write(path, s string) {
	v.t.Helper()
	if err := os.WriteFile(path, []byte(s), 0o600); err != nil {
		v.t.Fatal(err)
	}
}

func (v *vps) read(path string) string {
	v.t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		v.t.Fatal(err)
	}
	return strings.TrimSpace(string(b))
}

// run runs taskiem-vps with args; image is the release the channel offers.
func (v *vps) run(image string, args ...string) (string, error) {
	v.t.Helper()
	script, err := filepath.Abs("vm/taskiem-vps")
	if err != nil {
		v.t.Fatal(err)
	}
	cmd := exec.Command("bash", append([]string{script}, args...)...)
	cmd.Env = append(os.Environ(),
		"PATH="+filepath.Join(v.dir, "stubs")+":"+os.Getenv("PATH"),
		"TASKIEM_VPS_KIT="+v.kit, "TASKIEM_VPS_ETC="+filepath.Join(v.dir, "etc"), "TASKIEM_VPS_STATE="+v.state,
		"TASKIEM_VPS_BIN="+v.bin, "TASKIEM_VPS_NO_TIMERS=1", "TASKIEM_VPS_KIT_SOURCE=",
		"TASKIEM_VPS_IMAGE_OVERRIDE="+image, "CALLS="+v.log, "KITS="+filepath.Join(v.dir, "kits"))
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// ups is how many times the stack was (re)started, with which image and kit.
func (v *vps) ups() []string {
	b, _ := os.ReadFile(v.log)
	var ups []string
	for _, l := range strings.Split(string(b), "\n") {
		if strings.HasPrefix(l, "up ") {
			ups = append(ups, strings.TrimPrefix(l, "up "))
		}
	}
	return ups
}

// TestVPSDoesNotRetryAFailedRelease: a release that failed and was rolled
// back is not tried again by the timer (docs/vm.md: "does not try that
// release again"), each tick of which would replace the healthy container,
// wait for the broken one and alert again. --now still tries it.
func TestVPSDoesNotRetryAFailedRelease(t *testing.T) {
	v := newVPS(t)
	if out, err := v.run("taskiem:broken", "update", "--auto"); err == nil {
		t.Fatalf("the broken release was accepted: %s", out)
	}
	if got := v.read(filepath.Join(v.state, "current")); got != "taskiem:good" {
		t.Fatalf("current after the failed upgrade = %q, want taskiem:good", got)
	}
	before := len(v.ups())
	for tick := range 2 {
		out, err := v.run("taskiem:broken", "update", "--auto")
		if err != nil {
			t.Fatalf("tick %d: %v: %s", tick, err, out)
		}
	}
	if after := v.ups(); len(after) != before {
		t.Errorf("the failed release was tried again: the stack restarted %d more times (%q)", len(after)-before, after[before:])
	}
	if got := v.read(filepath.Join(v.state, "current")); got != "taskiem:good" {
		t.Errorf("current = %q, want taskiem:good", got)
	}
	out, _ := v.run("taskiem:broken", "update")
	if !strings.Contains(out, "--now") {
		t.Errorf("a manual update does not say how to retry: %s", out)
	}
	if out, err := v.run("taskiem:broken", "update", "--now"); err == nil {
		t.Fatalf("--now: the broken release was accepted: %s", out)
	}
	if len(v.ups()) == before {
		t.Error("--now did not try the failed release again")
	}
	// A newer release is taken as usual.
	if out, err := v.run("taskiem:good2", "update", "--auto"); err != nil {
		t.Fatalf("a newer release: %v: %s", err, out)
	}
	if got := v.read(filepath.Join(v.state, "current")); got != "taskiem:good2" {
		t.Errorf("current = %q, want taskiem:good2", got)
	}
}
