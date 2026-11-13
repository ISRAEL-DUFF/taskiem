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
	// env is added to taskiem-vps's environment.
	env []string
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
  "image inspect"*) case "${@: -1}" in taskiem:good) echo revA ;; taskiem:broken) echo revB ;; taskiem:good2|taskiem:other-channel|taskiem:unstated) echo revC ;; taskiem:old) echo revA ;; esac ;;
  inspect*) [ "$img" = taskiem:broken ] && echo unhealthy || echo healthy ;;
esac
`)
	// curl: the kit tarballs codeload would serve; everything else answers.
	stub("curl", `for a in "$@"; do case $a in https://codeload.github.com/*) echo "kit ${a##*/}" >>"$CALLS"; cat "$KITS/${a##*/}.tgz"; exit 0 ;;
  https://alerts.test/*) [ -z "${ALERTS_DOWN:-}" ] || exit 22; echo "alert $(tr -d '\n')" >>"$CALLS"; exit 0 ;; esac; done
exit 0
`)
	// cosign: CI's signed statement of the channel a release was promoted
	// to and its release number, as verify-attestation prints it (a DSSE
	// envelope whose payload is the statement, base64).
	stub("cosign", `[ "$1" = verify-attestation ] || exit 0
echo "cosign $*" >>"$CALLS"
for a in "$@"; do case $a in
  taskiem:good) st='{"channel":"staging","release":10}' ;;
  taskiem:good2) st='{"channel":"staging","release":12}' ;;
  taskiem:old) st='{"channel":"staging","release":5}' ;;
  taskiem:other-channel) st='{"channel":"production","release":99}' ;;
esac; done
[ -n "${st:-}" ] || { echo "Error: no matching attestations" >&2; exit 1; }
printf '{"payloadType":"application/vnd.in-toto+json","payload":"%s"}\n' "$(printf '{"predicate":%s}' "$st" | base64 -w0)"
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
	cmd.Env = append(cmd.Env, v.env...)
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

// TestVPSRollbackRestoresTheKit: going back to a release, after a failed
// upgrade or with taskiem-vps rollback, also brings back that release's
// deployment kit (Compose file, configs and taskiem-vps itself), so the old
// image does not run under the newer release's Compose file.
func TestVPSRollbackRestoresTheKit(t *testing.T) {
	v := newVPS(t)
	kit := func() string { return v.read(filepath.Join(v.kit, "docker-compose.yml")) }
	tool := func() string { return v.read(filepath.Join(v.bin, "taskiem-vps")) }

	if out, err := v.run("taskiem:broken", "update", "--now"); err == nil {
		t.Fatalf("the broken release was accepted: %s", out)
	}
	if kit() != "# kit revA" || tool() != "# kit revA" {
		t.Errorf("after the failed upgrade: compose file %q, taskiem-vps %q; want revA's", kit(), tool())
	}
	if ups := v.ups(); len(ups) == 0 || ups[len(ups)-1] != "taskiem:good # kit revA" {
		t.Errorf("the rolled-back release started as %q, want taskiem:good with revA's kit", ups)
	}

	if out, err := v.run("taskiem:good2", "update", "--now"); err != nil {
		t.Fatalf("upgrade: %v: %s", err, out)
	}
	if kit() != "# kit revC" {
		t.Fatalf("after the upgrade: compose file %q, want revC's", kit())
	}
	if out, err := v.run("", "rollback"); err != nil {
		t.Fatalf("rollback: %v: %s", err, out)
	}
	if got := v.read(filepath.Join(v.state, "current")); got != "taskiem:good" {
		t.Errorf("current after rollback = %q, want taskiem:good", got)
	}
	if kit() != "# kit revA" || tool() != "# kit revA" {
		t.Errorf("after rollback: compose file %q, taskiem-vps %q; want revA's", kit(), tool())
	}
	if ups := v.ups(); ups[len(ups)-1] != "taskiem:good # kit revA" {
		t.Errorf("rollback started %q, want taskiem:good with revA's kit", ups[len(ups)-1])
	}
}

// TestVPSTakesOnlyNewerReleasesOfItsChannel: a signature alone says CI
// built an image, not that it was released to this server's channel or
// that it is newer than what runs. Whoever can move the channel tag in the
// registry could otherwise make a production server install any main build
// or an old release. The server takes a release only with CI's signed
// statement naming its channel and a release number above the running
// one; a person may go back with --allow-downgrade, which is logged.
func TestVPSTakesOnlyNewerReleasesOfItsChannel(t *testing.T) {
	v := newVPS(t)
	v.env = []string{"TASKIEM_VPS_TEST_RELEASES=1"}
	v.write(filepath.Join(v.dir, "etc", "vps.conf"), "CHANNEL='staging'\n")
	current := func() string { return v.read(filepath.Join(v.state, "current")) }

	if out, err := v.run("taskiem:good2", "update", "--now"); err != nil || current() != "taskiem:good2" {
		t.Fatalf("a newer release of the channel: %v: %s", err, out)
	}
	if got := v.read(filepath.Join(v.state, "current-release")); got != "12" {
		t.Errorf("recorded release = %q, want 12", got)
	}
	for image, want := range map[string]string{
		"taskiem:old":           "not newer",
		"taskiem:other-channel": "staging channel",
		"taskiem:unstated":      "staging channel",
	} {
		out, err := v.run(image, "update", "--now")
		if err == nil || current() != "taskiem:good2" || !strings.Contains(out, want) {
			t.Errorf("%s: err %v, now running %s, said: %s", image, err, current(), out)
		}
	}
	// The timer does not try a refused release again on every tick.
	_, _ = v.run("taskiem:old", "update", "--auto") // refused, and remembered
	if current() != "taskiem:good2" {
		t.Errorf("the timer took an older release: now running %s", current())
	}
	before, _ := os.ReadFile(v.log)
	if out, err := v.run("taskiem:old", "update", "--auto"); err != nil {
		t.Errorf("a refused release on the timer: %v: %s", err, out)
	}
	if after, _ := os.ReadFile(v.log); len(after) != len(before) {
		t.Errorf("the refused release was checked again: %s", after[len(before):])
	}
	// The timer may not downgrade; a person may, and it is logged.
	if out, err := v.run("taskiem:old", "update", "--auto", "--allow-downgrade"); err == nil {
		t.Errorf("--auto --allow-downgrade was accepted: %s", out)
	}
	out, err := v.run("taskiem:old", "update", "--now", "--allow-downgrade")
	if err != nil || current() != "taskiem:old" || !strings.Contains(out, "allow-downgrade") {
		t.Fatalf("--allow-downgrade: %v, now running %s: %s", err, current(), out)
	}
	if got := v.read(filepath.Join(v.state, "current-release")); got != "5" {
		t.Errorf("recorded release after the downgrade = %q, want 5", got)
	}
	// Rolling back restores the release number with the release.
	if out, err := v.run("", "rollback"); err != nil || current() != "taskiem:good2" {
		t.Fatalf("rollback: %v: %s", err, out)
	}
	if got := v.read(filepath.Join(v.state, "current-release")); got != "12" {
		t.Errorf("recorded release after rollback = %q, want 12", got)
	}
}

// alerts is the alerts that reached the webhook.
func (v *vps) alerts() []string {
	b, _ := os.ReadFile(v.log)
	var out []string
	for _, l := range strings.Split(string(b), "\n") {
		if strings.HasPrefix(l, "alert ") {
			out = append(out, l)
		}
	}
	return out
}

// TestVPSResendsAnAlertThatDidNotGoOut: the check alerts when the set of
// problems changes and remembers the set. It used to remember it even when
// the alert could not be sent, so an outage that took the alert channel
// down with it was never reported. The set is now remembered only once an
// alert got through; until then every check tries again.
func TestVPSResendsAnAlertThatDidNotGoOut(t *testing.T) {
	v := newVPS(t)
	v.write(filepath.Join(v.dir, "etc", "vps.conf"), "ALERT_WEBHOOK='https://alerts.test/hook'\n")
	v.write(filepath.Join(v.state, "update-failed"), "taskiem:broken\n")

	v.env = []string{"ALERTS_DOWN=1"}
	out, err := v.run("", "check")
	if err == nil || !strings.Contains(out, "could not be sent") {
		t.Fatalf("check with the alert channel down: %v: %s", err, out)
	}
	if b, _ := os.ReadFile(filepath.Join(v.state, "alerts")); len(b) != 0 {
		t.Fatalf("the problems were remembered though nobody was told: %q", b)
	}
	v.env = nil
	if _, err := v.run("", "check"); err == nil {
		t.Fatal("the check passed with a problem outstanding")
	}
	if got := v.alerts(); len(got) != 1 || !strings.Contains(got[0], "taskiem:broken") {
		t.Fatalf("alerts once the channel is back = %q, want the one that did not go out", got)
	}
	// Told once: the same problems do not alert again.
	_, _ = v.run("", "check")
	if got := v.alerts(); len(got) != 1 {
		t.Errorf("the same problems alerted again: %q", got)
	}
}
