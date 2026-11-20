package deploy

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
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
			body := "# kit " + rev + "\n"
			// revC's taskiem-vps knows external-proxy mode; revA's and revB's do not.
			if f == "taskiem-vps" && rev == "revC" {
				body += "# docker-compose.external-proxy.yml\n"
			}
			if err := os.WriteFile(filepath.Join(src, f), []byte(body), 0o644); err != nil {
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
	// never healthy, and each image's revision label names its kit. A
	// network exists once created; the other Caddy (docker exec) has
	// loaded app.example.com.
	stub("docker", `img=$(sed -n "s/^TASKIEM_IMAGE='\(.*\)'$/\1/p" "$TASKIEM_VPS_KIT/.env" 2>/dev/null)
case "$*" in
  *" up "*|*" up") echo "up $img $(head -1 "$TASKIEM_VPS_KIT/docker-compose.yml")" >>"$CALLS"; echo "compose $*" >>"$CALLS"; [ "$img" != taskiem:broken ] ;;
  "network inspect "*) grep -qx "network create $3" "$CALLS" 2>/dev/null ;;
  "network create "*) echo "$*" >>"$CALLS" ;;
  "exec "*wget*) echo '{"match":[{"host":["app.example.com"]}]}' ;;
  "exec "*) echo "$*" >>"$CALLS"; case "$*" in *"caddy reload"*) [ -z "${RELOAD_FAIL:-}" ] ;; esac ;;
  inspect*Networks*) echo '{"taskiem_default":{},"cohost":{}}' ;;
  *" stop openbao"*) echo "stop openbao" >>"$CALLS" ;;
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

// TestVPSBackupWaitsForAnUpdate: the backup stops OpenBao and copies its
// storage; an update restarts the stack. The production upgrade window
// (01:00-05:00) covers the 02:30 backup, so they share one lock: a backup
// does not touch OpenBao while an update holds it, and an update does not
// start while a backup runs.
func TestVPSBackupWaitsForAnUpdate(t *testing.T) {
	v := newVPS(t)
	v.write(filepath.Join(v.dir, "etc", "backup.env"), "RESTIC_REPOSITORY='/tmp/none'\n")
	// Something holds the lock, as a running update does.
	lock, err := os.OpenFile(filepath.Join(v.state, "update.lock"), os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = lock.Close() }()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
		t.Fatal(err)
	}
	v.env = []string{"TASKIEM_VPS_LOCK_WAIT=1"}
	out, err := v.run("taskiem:good", "backup")
	if err == nil || !strings.Contains(out, "update") {
		t.Errorf("backup during an update: %v: %s", err, out)
	}
	if b, _ := os.ReadFile(v.log); strings.Contains(string(b), "stop openbao") {
		t.Errorf("the backup stopped OpenBao while an update held the lock: %s", b)
	}
	// And the other way: an update does not start while the lock is held.
	if out, err := v.run("taskiem:good2", "update", "--now"); err != nil || len(v.ups()) != 0 {
		t.Errorf("update while the lock is held: %v, restarts %q: %s", err, v.ups(), out)
	}
	_ = lock.Close()
	// Released: the backup runs, and releases the lock when done.
	if out, err := v.run("taskiem:good", "backup"); err != nil {
		t.Fatalf("backup with the lock free: %v: %s", err, out)
	}
	if b, _ := os.ReadFile(v.log); !strings.Contains(string(b), "stop openbao") {
		t.Errorf("the backup did not copy OpenBao: %s", b)
	}
}

// TestVPSFirstPasswordGoesToARootOnlyFile: on an unattended install with no
// SMTP there is nobody at a terminal and no email to carry a link. The
// owner's first password used to be printed to standard output, which
// cloud-init and CI keep in their logs; it is written to a file only root
// can read, and only the file's path is said.
func TestVPSFirstPasswordGoesToARootOnlyFile(t *testing.T) {
	for _, tool := range []string{"bash", "setsid"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s is not installed", tool)
		}
	}
	script, err := filepath.Abs("vm/taskiem-vps")
	if err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	// The function alone, with the script's log: no terminal (setsid).
	cmd := exec.Command("setsid", "bash", "-c", `set -Eeuo pipefail
source <(sed -n '/^log() /p; /^hand_over_password() /,/^}/p' "$0")
hand_over_password owner@acme.test s3cret-first-password`, script)
	cmd.Env = append(os.Environ(), "TASKIEM_VPS_ROOT_HOME="+home)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	if strings.Contains(string(out), "s3cret-first-password") {
		t.Errorf("the password is in the output: %s", out)
	}
	files, _ := filepath.Glob(filepath.Join(home, "taskiem-owner-password-*.txt"))
	if len(files) != 1 || !strings.Contains(string(out), files[0]) {
		t.Fatalf("files %q; output does not name the file: %s", files, out)
	}
	info, err := os.Stat(files[0])
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Errorf("mode %v, want 0600 (%v)", info.Mode().Perm(), err)
	}
	if b, _ := os.ReadFile(files[0]); !strings.Contains(string(b), "s3cret-first-password") || !strings.Contains(string(b), "owner@acme.test") {
		t.Errorf("file: %s", b)
	}
	// And the password is never an argument of a command.
	src, _ := os.ReadFile(script)
	if strings.Contains(string(src), `TASKIEM_BOOTSTRAP_PASSWORD="$pw" taskiem`) || strings.Contains(string(src), `-e TASKIEM_BOOTSTRAP_PASSWORD="$pw"`) {
		t.Error("the first password is passed on a command line")
	}
}

// TestInstallerChecksWhatItInstalls: the installer runs as root. It takes
// Docker from Docker's signed apt repository (not the get.docker.com
// script), refuses a kit that does not match its checksum, and the timers'
// oneshot units cannot hang for ever holding the update lock.
func TestInstallerChecksWhatItInstalls(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash is not installed")
	}
	src, err := os.ReadFile("vm/install.sh")
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(string(src), "\n") {
		if !strings.HasPrefix(strings.TrimSpace(line), "#") && strings.Contains(line, "get.docker.com") {
			t.Errorf("the installer pipes the Docker convenience script into a shell: %s", line)
		}
	}
	d := t.TempDir()
	kit := filepath.Join(d, "taskiem-main", "deploy", "vm")
	if err := os.MkdirAll(kit, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(kit, "taskiem-vps"), []byte("# the kit\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	tgz := filepath.Join(d, "kit.tgz")
	if out, err := exec.Command("tar", "-czf", tgz, "-C", d, "taskiem-main").CombinedOutput(); err != nil {
		t.Fatalf("tar: %v: %s", err, out)
	}
	sum, err := exec.Command("sha256sum", tgz).Output()
	if err != nil {
		t.Fatal(err)
	}
	good := strings.Fields(string(sum))[0]
	if err := os.MkdirAll(filepath.Join(d, "stubs"), 0o755); err != nil {
		t.Fatal(err)
	}
	// curl -fsSL -o FILE URL: serves the tarball.
	if err := os.WriteFile(filepath.Join(d, "stubs", "curl"), []byte("#!/usr/bin/env bash\nwhile [ $# -gt 0 ]; do [ \"$1\" = -o ] && { cp \"$KIT_TGZ\" \"$2\"; exit 0; }; shift; done\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	script, _ := filepath.Abs("vm/install.sh")
	fetch := func(sum string) (string, string, error) {
		dest := t.TempDir()
		cmd := exec.Command("bash", "-c", `set -Eeuo pipefail
REPO_SLUG=x/y
source <(sed -n '/^log() /p; /^die() /p; /^fetch_kit() /,/^}/p' "$0")
fetch_kit main "$1" "$2"`, script, sum, dest)
		cmd.Env = append(os.Environ(), "PATH="+filepath.Join(d, "stubs")+":"+os.Getenv("PATH"), "KIT_TGZ="+tgz)
		out, err := cmd.CombinedOutput()
		return dest, string(out), err
	}
	if dest, out, err := fetch(good); err != nil {
		t.Errorf("the right checksum: %v: %s", err, out)
	} else if _, err := os.Stat(filepath.Join(dest, "taskiem-vps")); err != nil {
		t.Errorf("the kit was not unpacked: %v", err)
	}
	dest, out, err := fetch(strings.Repeat("0", 64))
	if err == nil || !strings.Contains(out, "does not match its checksum") {
		t.Errorf("a wrong checksum: %v: %s", err, out)
	}
	if _, err := os.Stat(filepath.Join(dest, "taskiem-vps")); err == nil {
		t.Error("a kit with the wrong checksum was unpacked")
	}
	units, _ := filepath.Glob("vm/systemd/*.service")
	if len(units) == 0 {
		t.Fatal("no units")
	}
	for _, u := range units {
		b, _ := os.ReadFile(u)
		if strings.Contains(string(b), "Type=oneshot") && !strings.Contains(string(b), "\nTimeoutStartSec=") {
			t.Errorf("%s: a oneshot with no TimeoutStartSec can hang for ever", u)
		}
	}
}

// TestVPSExternalProxyKeepsItsSetup: beside Xenibase, Xenibase's Caddy
// holds ports 80 and 443 and serves Taskiem's sites (external-proxy mode,
// docs/vm.md#sharing-the-server-with-xenibase). Taskiem's own Caddy is left
// out by a Compose file of the server's own, the web containers join the
// shared network, and taskiem.caddy and taskiem.global go into Xenibase's
// caddy.d, which is then reloaded. An update replaces the release's Compose
// file but keeps all of that; a release whose kit predates the mode is
// refused, since it would start Taskiem's Caddy on ports Xenibase holds.
// The default (own Caddy) uses the release's Compose file alone.
func TestVPSExternalProxyKeepsItsSetup(t *testing.T) {
	v := newVPS(t)
	etc := filepath.Join(v.dir, "etc", "vps.conf")
	override := filepath.Join(v.kit, "docker-compose.external-proxy.yml")
	calls := func() string { b, _ := os.ReadFile(v.log); return string(b) }

	// Own Caddy: nothing changes.
	v.write(etc, "DOMAIN='app.example.com'\n")
	if out, err := v.run("taskiem:old", "update", "--now"); err != nil {
		t.Fatalf("update with its own Caddy: %v: %s", err, out)
	}
	if strings.Contains(calls(), "external-proxy") || strings.Contains(calls(), "network create") || strings.Contains(calls(), "caddy reload") {
		t.Errorf("own Caddy, but the external proxy was set up: %s", calls())
	}
	if _, err := os.Stat(override); err == nil {
		t.Error("own Caddy, but the external-proxy Compose file was written")
	}
	if strings.Contains(v.read(filepath.Join(v.kit, ".env")), "COMPOSE_FILE") {
		t.Error("own Caddy, but .env names Compose files")
	}

	caddyD := filepath.Join(v.dir, "xenibase", "caddy.d")
	if err := os.MkdirAll(caddyD, 0o755); err != nil {
		t.Fatal(err)
	}
	v.write(etc, "DOMAIN='App.example.com'\nPUBLIC='yes'\nPREVIEW_DOMAIN='shops.example.net'\nPROXY='external'\nPROXY_DIR='"+caddyD+"'\nPROXY_CONTAINER='xenibase-caddy-1'\nPROXY_NETWORK='cohost'\n")
	if out, err := v.run("taskiem:good2", "update", "--now"); err != nil {
		t.Fatalf("update in external-proxy mode: %v: %s", err, out)
	}
	if got := v.read(filepath.Join(v.kit, "docker-compose.yml")); got != "# kit revC" {
		t.Errorf("the release's Compose file was not installed: %q", got)
	}
	o := v.read(override)
	for _, want := range []string{"  caddy:\n    profiles: [own-caddy]", "  taskiem:\n    networks: [default, proxy]", "  taskiem-public:\n    networks: [default, proxy]", "  proxy:\n    name: cohost\n    external: true"} {
		if !strings.Contains(o, want) {
			t.Errorf("the external-proxy Compose file lacks %q:\n%s", want, o)
		}
	}
	// A person's own docker compose in /opt/taskiem uses both files too.
	if env := v.read(filepath.Join(v.kit, ".env")); !strings.Contains(env, "COMPOSE_FILE='"+v.kit+"/docker-compose.yml:"+override+"'") {
		t.Errorf(".env does not name both Compose files:\n%s", env)
	}
	c := calls()
	if !strings.Contains(c, "network create cohost") {
		t.Errorf("the shared network was not created: %s", c)
	}
	if !strings.Contains(c, "-f "+v.kit+"/docker-compose.yml -f "+override+" up") {
		t.Errorf("the stack was not started with the external-proxy Compose file: %s", c)
	}
	if strings.Count(c, "exec xenibase-caddy-1 caddy reload --config /etc/caddy/Caddyfile --adapter caddyfile") != 1 {
		t.Errorf("Xenibase's Caddy was not reloaded once: %s", c)
	}
	site := v.read(filepath.Join(caddyD, "taskiem.caddy"))
	for _, want := range []string{"\napp.example.com {\n\tencode zstd gzip\n\treverse_proxy taskiem:8080\n}", "\nhttps:// {\n\ttls {\n\t\ton_demand\n\t}\n\tencode zstd gzip\n\treverse_proxy taskiem-public:8082\n}", "\nhttp://*.shops.example.net {\n\tredir https://{host}{uri} 308\n}"} {
		if !strings.Contains(site, want) {
			t.Errorf("taskiem.caddy lacks %q:\n%s", want, site)
		}
	}
	if g := v.read(filepath.Join(caddyD, "taskiem.global")); !strings.Contains(g, "on_demand_tls {\n\task http://taskiem-public:9090/tls/ask\n}") {
		t.Errorf("taskiem.global:\n%s", g)
	}

	// Back to a release from before the mode: refused, nothing changes.
	out, err := v.run("", "rollback")
	if err == nil || !strings.Contains(out, "external-proxy") {
		t.Fatalf("rollback to a kit without external-proxy mode: %v: %s", err, out)
	}
	if got := v.read(filepath.Join(v.state, "current")); got != "taskiem:good2" {
		t.Errorf("current after the refused rollback = %q", got)
	}
	if got := v.read(filepath.Join(v.kit, "docker-compose.yml")); got != "# kit revC" {
		t.Errorf("the refused rollback changed the kit: %q", got)
	}

	if out, err := v.run("", "check"); err != nil || !strings.Contains(out, "all checks pass") {
		t.Errorf("check in external-proxy mode: %v: %s", err, out)
	}
	if err := os.Remove(filepath.Join(caddyD, "taskiem.caddy")); err != nil {
		t.Fatal(err)
	}
	if out, err := v.run("", "check"); err == nil || !strings.Contains(out, "taskiem.caddy is missing") {
		t.Errorf("check with taskiem.caddy gone: %v: %s", err, out)
	}
	// The next update writes it again and reloads; the override stays.
	if out, err := v.run("taskiem:unstated", "update", "--now"); err != nil {
		t.Fatalf("update: %v: %s", err, out)
	}
	if _, err := os.Stat(filepath.Join(caddyD, "taskiem.caddy")); err != nil {
		t.Errorf("the update did not write taskiem.caddy again: %v", err)
	}
	if n := strings.Count(calls(), "caddy reload"); n != 2 {
		t.Errorf("Xenibase's Caddy reloaded %d times, want 2 (only when the files changed)", n)
	}
	if v.read(override) != o {
		t.Error("the update changed the external-proxy Compose file")
	}

	// A reload that fails puts the files Xenibase's Caddy had back, and
	// reloads it again: on its next start (every Xenibase upgrade) it would
	// otherwise load files it refuses, and fail to start.
	siteFile, globalFile := filepath.Join(caddyD, "taskiem.caddy"), filepath.Join(caddyD, "taskiem.global")
	before := v.read(siteFile)
	beforeGlobal := v.read(globalFile)
	conf := v.read(etc)
	v.write(etc, strings.Replace(conf, "shops.example.net", "stores.example.net", 1)+"\n")
	v.env = []string{"RELOAD_FAIL=1"}
	reloads := strings.Count(calls(), "caddy reload")
	if out, err := v.run("taskiem:other-channel", "update", "--now"); err != nil || !strings.Contains(out, "putting back") {
		t.Fatalf("update with a reload that fails: %v: %s", err, out)
	}
	if v.read(siteFile) != before || v.read(globalFile) != beforeGlobal {
		t.Errorf("after a failed reload, taskiem.caddy is\n%s", v.read(siteFile))
	}
	if n := strings.Count(calls(), "caddy reload") - reloads; n != 2 {
		t.Errorf("%d reloads, want 2: the new files, then the ones put back", n)
	}
	if left, _ := os.ReadDir(caddyD); len(left) != 2 {
		t.Errorf("files left in Xenibase's caddy.d: %v", left)
	}
	v.env = nil
	v.write(etc, conf+"\n")
	if out, err := v.run("", "check"); err != nil || !strings.Contains(out, "all checks pass") {
		t.Errorf("check against the files put back: %v: %s", err, out)
	}
	// With none before, a failed reload leaves none.
	for _, f := range []string{siteFile, globalFile} {
		if err := os.Remove(f); err != nil {
			t.Fatal(err)
		}
	}
	v.env = []string{"RELOAD_FAIL=1"}
	if out, err := v.run("taskiem:good2", "update", "--now"); err != nil {
		t.Fatalf("update: %v: %s", err, out)
	}
	if left, _ := os.ReadDir(caddyD); len(left) != 0 {
		t.Errorf("a failed reload left files where there were none: %v", left)
	}
}

// TestVPSWaitsForANewLoginToReachThePooler: on a Xenibase dedicated
// project the logins setup makes reach the poolers only when Xenibase syncs
// them, every 30 seconds. The first sign-in as each is retried, saying why,
// and gives up with what to check. With a Xenibase API token, setup asks
// for the sync at once; the token is not on a command line. psql is told
// to check a verify-full certificate against the system's roots.
func TestVPSWaitsForANewLoginToReachThePooler(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash is not installed")
	}
	script, err := filepath.Abs("vm/taskiem-vps")
	if err != nil {
		t.Fatal(err)
	}
	d := t.TempDir()
	calls := filepath.Join(d, "calls")
	if err := os.MkdirAll(filepath.Join(d, "stubs"), 0o755); err != nil {
		t.Fatal(err)
	}
	// docker run: the login is refused until the third try.
	stubs := map[string]string{
		"docker": `n=$(( $(cat "$D/n" 2>/dev/null || echo 0) + 1 )); echo $n >"$D/n"
for a in "$@"; do case $a in DSN=*) echo "docker $a" >>"$D/calls" ;; esac; done
[ "$n" -ge "${OK_AT:-3}" ]
`,
		"curl": `echo "curl $* stdin=$(cat)" >>"$D/calls"
`,
		"sleep": `:
`,
	}
	for name, body := range stubs {
		if err := os.WriteFile(filepath.Join(d, "stubs", name), []byte("#!/usr/bin/env bash\n"+body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	run := func(env ...string) (string, error) {
		_ = os.Remove(filepath.Join(d, "n"))
		_ = os.Remove(calls)
		cmd := exec.Command("bash", "-c", `set -Eeuo pipefail
PSQL_IMAGE=psql
source <(sed -n '/^log() /p; /^die() /p; /^psql_dsn() /,/^}/p; /^xenibase_sync() /,/^}/p; /^wait_for_login() /,/^}/p' "$0")
xenibase_sync
wait_for_login db1_app 'postgres://db1_app:pw@db.example.com:5432/db1?sslmode=verify-full'`, script)
		cmd.Env = append(os.Environ(), append([]string{"PATH=" + filepath.Join(d, "stubs") + ":" + os.Getenv("PATH"), "D=" + d}, env...)...)
		out, err := cmd.CombinedOutput()
		return string(out), err
	}
	out, err := run()
	if err != nil || !strings.Contains(out, "waiting for the database to accept db1_app") || !strings.Contains(out, "within 30 seconds") || !strings.Contains(out, "db1_app signs in") {
		t.Fatalf("a login synced on the third try: %v: %s", err, out)
	}
	b, _ := os.ReadFile(calls)
	if strings.Count(string(b), "docker DSN=postgres://db1_app:pw@db.example.com:5432/db1?sslmode=verify-full&sslrootcert=system\n") != 3 {
		t.Errorf("tries: %s", b)
	}
	if strings.Contains(string(b), "curl") {
		t.Errorf("Xenibase was asked to sync without a token: %s", b)
	}

	out, err = run("OK_AT=99", "TASKIEM_VPS_LOGIN_WAIT=0")
	if err == nil || !strings.Contains(out, "db1_app cannot sign in to the database") || !strings.Contains(out, "role management") {
		t.Errorf("a login never synced: %v: %s", err, out)
	}

	out, err = run("OK_AT=1", "TASKIEM_VPS_XENIBASE_URL=https://xenibase.example.com/", "TASKIEM_VPS_XENIBASE_TOKEN=xb_secret", "TASKIEM_VPS_XENIBASE_PROJECT=prj1")
	b, _ = os.ReadFile(calls)
	if err != nil || strings.Contains(out, "waiting") {
		t.Errorf("a synced login: %v: %s", err, out)
	}
	if !strings.Contains(string(b), "-X POST -H @- https://xenibase.example.com/api/v1/projects/prj1/manage-roles/sync stdin=Authorization: Bearer xb_secret") {
		t.Errorf("the sync call: %s", b)
	}
	if strings.Contains(strings.SplitN(string(b), "stdin=", 2)[0], "xb_secret") {
		t.Errorf("the token is on curl's command line: %s", b)
	}
}
