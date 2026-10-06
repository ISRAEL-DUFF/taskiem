package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/israel-duff/taskiem/engine/db/dbtest"
)

// syncBuffer is a bytes.Buffer safe to read while dev writes to it.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// TestDevReloads runs `taskiem dev` against a test database: it bootstraps,
// deploys the flows directory, redeploys an edited workflow, and refuses to
// deploy one whose tests fail.
func TestDevReloads(t *testing.T) {
	d := dbtest.New(t)
	dir, flows := t.TempDir(), t.TempDir()
	t.Setenv("TASKIEM_WEB_DIR", t.TempDir()) // no web build needed
	wf := func(factor string) string {
		return `{"schema":"wd/v1","id":"wf_double","version":1,"name":"Double","trigger":{"type":"manual"},
		  "steps":[{"id":"double","type":"transform","config":{"output":"=trigger.body.n * ` + factor + `"}}]}`
	}
	test := `{"schema":"wd-test/v1","workflow":"double.wd.json","cases":[{"name":"doubles","trigger":{"body":{"n":2}},
	  "expect":{"status":"completed","outputs":{"double":4}}}]}`
	write := func(name, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(flows, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("double.wd.json", wf("2"))
	write("double.test.json", test)

	ctx, cancel := context.WithCancel(context.Background())
	var out syncBuffer
	done := make(chan error, 1)
	go func() {
		done <- devCmd(ctx, []string{"--dir", dir, "--flows", flows, "--dsn", d.DSN, "--listen", freeAddr(t), "--poll", "50ms"}, &out)
	}()
	defer func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("dev: %v", err)
			}
		case <-time.After(40 * time.Second):
			t.Error("dev did not stop")
		}
	}()
	waitOut := func(want string) {
		t.Helper()
		for end := time.Now().Add(30 * time.Second); time.Now().Before(end); time.Sleep(50 * time.Millisecond) {
			if strings.Contains(out.String(), want) {
				return
			}
			select {
			case err := <-done:
				t.Fatalf("dev stopped: %v\n%s", err, out.String())
			default:
			}
		}
		t.Fatalf("no %q in:\n%s", want, out.String())
	}
	waitOut("published version 1")
	waitOut("tests  1 passed, 0 failed")

	// A change that breaks its test is not deployed.
	time.Sleep(20 * time.Millisecond) // a distinct modification time
	write("double.wd.json", wf("3"))
	waitOut("skip   wf_double: its tests fail")

	// Fixing the test deploys the change as version 2.
	write("double.test.json", strings.Replace(test, `"double":4`, `"double":6`, 1))
	waitOut("published version 2")
}
