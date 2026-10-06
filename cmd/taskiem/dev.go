package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/wdtest"
)

// devState is what `taskiem dev` keeps between sessions, in its directory.
type devState struct {
	KMSKey   string `json:"kms_key"`
	Email    string `json:"email"`
	Password string `json:"password"`
	APIKey   string `json:"api_key,omitempty"`
	DSN      string `json:"dsn,omitempty"` // the database bootstrapped, so a new one is bootstrapped again
}

const devEmail = "dev@taskiem.local"

// devCmd runs a local engine for building workflows (spec 10.4): every role
// in one process against a private Postgres cluster (or --dsn), with the web
// app, and hot reload: whenever a *.wd.json or *.test.json under --flows
// changes, the workflow tests run and changed workflows are deployed.
func devCmd(ctx context.Context, args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("dev", flag.ContinueOnError)
	dir := fs.String("dir", ".taskiem/dev", "where the local database, keys and archive live")
	flows := fs.String("flows", "flows", "directory of workflows and tests to watch")
	listen := fs.String("listen", "127.0.0.1:8080", "API, web app and webhook address")
	dsn := fs.String("dsn", os.Getenv("TASKIEM_DATABASE_URL"), "use this database instead of a private cluster (schema owner)")
	every := fs.Duration("poll", time.Second, "how often to look for changed files")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if err := os.MkdirAll(*dir, 0o700); err != nil {
		return err
	}
	st, err := loadDevState(*dir)
	if err != nil {
		return err
	}
	if *dsn == "" {
		url, stop, err := startPostgres(ctx, *dir, stdout)
		if err != nil {
			return err
		}
		defer stop()
		*dsn = url
	}
	if _, err := db.Migrate(ctx, *dsn); err != nil {
		return fmt.Errorf("dev: migrate: %w", err)
	}
	for k, v := range map[string]string{
		"TASKIEM_DATABASE_URL":   *dsn,
		"TASKIEM_LOCAL_KMS_KEY":  st.KMSKey,
		"TASKIEM_LISTEN":         *listen,
		"TASKIEM_METRICS_LISTEN": "127.0.0.1:0",
		"TASKIEM_SECURE_COOKIES": "false",
		"TASKIEM_ARCHIVE_DIR":    filepath.Join(*dir, "archive"),
		"TASKIEM_KMS":            "local",
	} {
		if err := os.Setenv(k, v); err != nil {
			return err
		}
	}
	if os.Getenv("TASKIEM_WEB_DIR") == "" {
		if _, err := os.Stat("web/dist/index.html"); err == nil {
			_ = os.Setenv("TASKIEM_WEB_DIR", "web/dist")
		}
	}
	if st.DSN != *dsn {
		_ = os.Setenv("TASKIEM_BOOTSTRAP_PASSWORD", st.Password)
		var out bytes.Buffer
		if err := run([]string{"bootstrap", "--tenant", "Local development", "--email", st.Email, "--name", "Developer"}, &out, &out); err != nil {
			return fmt.Errorf("dev: bootstrap: %w (%s)", err, strings.TrimSpace(out.String()))
		}
		st.DSN, st.APIKey = *dsn, ""
		if err := st.save(*dir); err != nil {
			return err
		}
	}

	serveCtx, stopServe := context.WithCancel(ctx)
	served := make(chan error, 1)
	go func() { served <- serve(serveCtx, []string{"--role", "all"}) }()
	defer func() {
		stopServe()
		<-served
	}()
	base := "http://" + *listen
	if err := waitReady(ctx, base, served); err != nil {
		return err
	}
	if st.APIKey == "" {
		if st.APIKey, err = devKey(ctx, base, st); err != nil {
			return err
		}
		if err := st.save(*dir); err != nil {
			return err
		}
	}
	fmt.Fprintf(stdout, "taskiem dev: %s  (sign in as %s, password in %s)\n", base, st.Email, filepath.Join(*dir, "state.json"))
	fmt.Fprintf(stdout, "             TASKIEM_URL=%s TASKIEM_API_KEY=%s\n", base, st.APIKey)
	fmt.Fprintf(stdout, "             watching %s\n", *flows)

	c := &client{base: base, key: st.APIKey, http: &http.Client{Timeout: 30 * time.Second}}
	w := &watcher{dir: *flows, seen: map[string]time.Time{}}
	for {
		if changed := w.scan(); len(changed) > 0 {
			reload(ctx, c, *flows, changed, stdout)
		}
		select {
		case <-ctx.Done():
			return nil
		case err := <-served:
			return err
		case <-time.After(*every):
		}
	}
}

func loadDevState(dir string) (*devState, error) {
	path := filepath.Join(dir, "state.json")
	raw, err := os.ReadFile(path)
	if err == nil {
		var st devState
		if err := json.Unmarshal(raw, &st); err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		return &st, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	key := make([]byte, 32)
	pw := make([]byte, 18)
	if _, err := rand.Read(key); err != nil {
		return nil, err
	}
	if _, err := rand.Read(pw); err != nil {
		return nil, err
	}
	st := &devState{KMSKey: base64.StdEncoding.EncodeToString(key), Email: devEmail, Password: base64.RawURLEncoding.EncodeToString(pw)}
	return st, st.save(dir)
}

func (st *devState) save(dir string) error {
	raw, _ := json.MarshalIndent(st, "", "  ") //nolint:gosec // the local developer's own credentials, written 0600 under .taskiem
	return os.WriteFile(filepath.Join(dir, "state.json"), raw, 0o600)
}

// startPostgres runs a private cluster in dir/pg with the Postgres binaries
// installed on this machine, on a free loopback port, and returns its DSN.
func startPostgres(ctx context.Context, dir string, stdout io.Writer) (string, func(), error) {
	initdb, pgctl, err := findPostgres()
	if err != nil {
		return "", nil, err
	}
	data, err := filepath.Abs(filepath.Join(dir, "pg"))
	if err != nil {
		return "", nil, err
	}
	if _, err := os.Stat(filepath.Join(data, "PG_VERSION")); errors.Is(err, os.ErrNotExist) {
		fmt.Fprintf(stdout, "taskiem dev: creating a Postgres cluster in %s\n", data)
		// The binaries come from findPostgres; the arguments are fixed.
		cmd := exec.CommandContext(ctx, initdb, "-D", data, "-U", "taskiem", "--auth=trust", "-E", "UTF8", "--no-instructions") //nolint:gosec // see above
		out, err := cmd.CombinedOutput()
		if err != nil {
			return "", nil, fmt.Errorf("dev: initdb: %w\n%s", err, out)
		}
	}
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", nil, err
	}
	port := l.Addr().(*net.TCPAddr).Port
	_ = l.Close()
	opts := fmt.Sprintf("-p %d -c listen_addresses=127.0.0.1 -c unix_socket_directories=''", port)
	cmd := exec.CommandContext(ctx, pgctl, "-D", data, "-l", filepath.Join(data, "..", "postgres.log"), "-o", opts, "-w", "start") //nolint:gosec // binaries from findPostgres
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", nil, fmt.Errorf("dev: pg_ctl start: %w\n%s", err, out)
	}
	stop := func() {
		_ = exec.Command(pgctl, "-D", data, "-m", "fast", "-w", "stop").Run() //nolint:gosec // binaries from findPostgres
	}
	return fmt.Sprintf("postgres://taskiem@127.0.0.1:%d/postgres?sslmode=disable", port), stop, nil
}

// findPostgres finds initdb and pg_ctl on PATH or in the usual Debian and
// Homebrew locations, newest version first.
func findPostgres() (initdb, pgctl string, err error) {
	if p, err := exec.LookPath("pg_ctl"); err == nil {
		if i, err := exec.LookPath("initdb"); err == nil {
			return i, p, nil
		}
	}
	var dirs []string
	for _, pattern := range []string{"/usr/lib/postgresql/*/bin", "/opt/homebrew/opt/postgresql@*/bin", "/usr/local/opt/postgresql@*/bin"} {
		m, _ := filepath.Glob(pattern)
		dirs = append(dirs, m...)
	}
	sort.Sort(sort.Reverse(sort.StringSlice(dirs)))
	for _, d := range dirs {
		i, p := filepath.Join(d, "initdb"), filepath.Join(d, "pg_ctl")
		if _, err := os.Stat(i); err == nil {
			if _, err := os.Stat(p); err == nil {
				return i, p, nil
			}
		}
	}
	return "", "", errors.New("dev: Postgres 16 is not installed (no initdb/pg_ctl found); install it, or pass --dsn / TASKIEM_DATABASE_URL, or run `make up` for the Compose stack")
}

func waitReady(ctx context.Context, base string, served <-chan error) error {
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := http.Get(base + "/readyz")
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return nil
			}
		}
		select {
		case err := <-served:
			return fmt.Errorf("dev: serve stopped: %w", err)
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
	return errors.New("dev: the engine did not become ready")
}

// devKey signs in as the developer and creates an API key for the CLI and
// for hot reload.
func devKey(ctx context.Context, base string, st *devState) (string, error) {
	post := func(path, token string, body any, out any) error {
		raw, _ := json.Marshal(body)
		req, _ := http.NewRequestWithContext(ctx, "POST", base+path, bytes.NewReader(raw))
		req.Header.Set("Content-Type", "application/json")
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return err
		}
		defer func() { _ = resp.Body.Close() }()
		b, _ := io.ReadAll(resp.Body)
		if resp.StatusCode >= 300 {
			return fmt.Errorf("%s: %d %s", path, resp.StatusCode, b)
		}
		return json.Unmarshal(b, out)
	}
	var login struct {
		Token string `json:"token"`
	}
	if err := post("/v1/auth/login", "", map[string]string{"email": st.Email, "password": st.Password}, &login); err != nil {
		return "", fmt.Errorf("dev: sign in: %w", err)
	}
	var key struct {
		Key string `json:"key"`
	}
	err := post("/v1/api-keys", login.Token, map[string]any{"name": "taskiem dev", "expires_days": 365,
		"permissions": []string{"workflow.read", "workflow.edit", "workflow.publish", "run.read", "run.start", "run.cancel"}}, &key)
	if err != nil {
		return "", fmt.Errorf("dev: API key: %w", err)
	}
	return key.Key, nil
}

// watcher notices changed workflow and test files by modification time
// (polling: no platform file-event APIs needed).
type watcher struct {
	dir  string
	seen map[string]time.Time
}

func (w *watcher) scan() []string {
	var changed []string
	now := map[string]time.Time{}
	_ = filepath.WalkDir(w.dir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil //nolint:nilerr // an unreadable entry is skipped, not fatal to the watch
		}
		if !strings.HasSuffix(path, ".wd.json") && !strings.HasSuffix(path, ".test.json") && !strings.HasSuffix(path, ".flow.ts") {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil //nolint:nilerr // deleted between listing and stat
		}
		now[path] = info.ModTime()
		if prev, ok := w.seen[path]; !ok || !prev.Equal(info.ModTime()) {
			changed = append(changed, path)
		}
		return nil
	})
	w.seen = now
	return changed
}

// reload builds changed workflow code, runs the workflow tests, then deploys
// the workflows that changed if their tests pass (a failing test keeps the
// running version).
func reload(ctx context.Context, c *client, dir string, changed []string, stdout io.Writer) {
	fmt.Fprintf(stdout, "\n[%s] %d file(s) changed\n", time.Now().Format("15:04:05"), len(changed))
	for _, f := range changed {
		if !strings.HasSuffix(f, ".flow.ts") {
			continue
		}
		wrote, err := buildFlow(ctx, f, true)
		switch {
		case err != nil:
			fmt.Fprintf(stdout, "  build  %s: %v\n", f, err)
		case wrote:
			fmt.Fprintf(stdout, "  build  wrote %s\n", strings.TrimSuffix(f, ".flow.ts")+".wd.json")
		}
	}
	failing := map[string]bool{} // workflow paths with a failing test
	if files, err := wdtest.Discover([]string{dir}); err == nil && len(files) > 0 {
		if reg, err := builtinRegistry(); err == nil {
			passed, failed := 0, 0
			for _, path := range files {
				f, err := wdtest.Load(path)
				if err != nil {
					fmt.Fprintf(stdout, "  test   %v\n", err)
					continue
				}
				results, err := f.Run(reg)
				if err != nil {
					fmt.Fprintf(stdout, "  test   %s: %v\n", path, err)
					failing[filepath.Clean(f.WorkflowPath())] = true
					continue
				}
				for _, r := range results {
					if r.Passed() {
						passed++
						continue
					}
					failed++
					failing[filepath.Clean(f.WorkflowPath())] = true
					fmt.Fprintf(stdout, "  FAIL   %s: %s\n", path, r.Case)
					for _, m := range r.Failures {
						fmt.Fprintf(stdout, "           %s\n", m)
					}
				}
			}
			fmt.Fprintf(stdout, "  tests  %d passed, %d failed\n", passed, failed)
		}
	}
	locals, err := findWorkflows([]string{dir})
	if err != nil {
		fmt.Fprintf(stdout, "  deploy %v\n", err)
		return
	}
	var deploy []localWorkflow
	for _, l := range locals {
		if failing[filepath.Clean(l.path)] {
			fmt.Fprintf(stdout, "  skip   %s: its tests fail\n", l.key)
			continue
		}
		deploy = append(deploy, l)
	}
	plans, err := c.plan(ctx, deploy)
	if err != nil {
		fmt.Fprintf(stdout, "  deploy %v\n", err)
		return
	}
	for _, p := range plans {
		if p.action == "unchanged" {
			continue
		}
		fmt.Fprintln(stdout, "  "+describe(p))
		if err := c.apply(ctx, p, stdout); err != nil {
			fmt.Fprintf(stdout, "    FAILED: %v\n", err)
		}
	}
}
