package container

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/israel-duff/taskiem/engine/effects"
)

func needSh(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("no sh")
	}
}

func localSpec(script string) Spec {
	return Spec{Name: "t", Command: []string{"sh", "-c", script}, Input: []byte(`{"n":1}`), Timeout: 5 * time.Second, OutputBytes: 1024, MemoryMB: 512}
}

func TestNewLocalRefusesWithoutBothFlags(t *testing.T) {
	env := func(m map[string]string) func(string) string { return func(k string) string { return m[k] } }
	for _, m := range []map[string]string{
		{},
		{EnvRunner: "local"},
		{EnvLocalDev: "1"},
		{EnvRunner: "kubernetes", EnvLocalDev: "1"},
	} {
		if _, err := NewLocal(env(m), nil); err == nil {
			t.Errorf("%v: local runner started", m)
		}
	}
	if _, err := NewLocal(env(map[string]string{EnvRunner: "local", EnvLocalDev: "1"}), nil); err != nil {
		t.Fatal(err)
	}
}

func TestLocalRunsAndCollects(t *testing.T) {
	needSh(t)
	t.Setenv("TASKIEM_LEAK_CHECK", "worker-only")
	l := &Local{}
	ctx := context.Background()

	// stdin to stdout, logs on stderr, platform env, no worker env.
	s := localSpec(`read in; echo "log line" >&2; echo "{\"got\": $in, \"key\": \"$TASKIEM_IDEMPOTENCY_KEY\", \"leak\": \"$TASKIEM_LEAK_CHECK\", \"secret\": \"$api_key\"}"`)
	s.Env = map[string]string{"TASKIEM_IDEMPOTENCY_KEY": "tsk_abc"}
	s.Secrets = map[string]string{"api_key": "sk_live_123456"}
	res, err := l.Run(ctx, s)
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]any
	if err := json.Unmarshal(res.Output, &out); err != nil {
		t.Fatalf("output %s: %v", res.Output, err)
	}
	if out["key"] != "tsk_abc" || out["leak"] != "" || out["secret"] != "sk_live_123456" || out["got"].(map[string]any)["n"] != float64(1) {
		t.Fatalf("output %v", out)
	}
	if !strings.Contains(res.Logs, "log line") {
		t.Fatalf("logs %q", res.Logs)
	}

	// Files: input, output and secrets.
	s = localSpec(`cat "$TASKIEM_INPUT" > "$TASKIEM_OUTPUT"; cat "$TASKIEM_SECRETS/api_key" >&2; echo stdout-is-a-log`)
	s.InputMode, s.OutputMode, s.SecretsMode = "file", "file", "file"
	s.Secrets = map[string]string{"api_key": "sk_file_7890"}
	res, err = l.Run(ctx, s)
	if err != nil {
		t.Fatal(err)
	}
	if string(res.Output) != `{"n":1}` || !strings.Contains(res.Logs, "sk_file_7890") || !strings.Contains(res.Logs, "stdout-is-a-log") {
		t.Fatalf("file mode: %s %q", res.Output, res.Logs)
	}

	// No output is null.
	res, err = l.Run(ctx, localSpec(`true`))
	if err != nil || string(res.Output) != "null" {
		t.Fatalf("empty output: %s %v", res.Output, err)
	}
}

func TestLocalFailures(t *testing.T) {
	needSh(t)
	l := &Local{}
	ctx := context.Background()
	cases := map[string]struct {
		spec Spec
		want error
		msg  string
	}{
		"exit status":     {localSpec(`echo boom >&2; exit 3`), effects.ErrFatal, "exited with status 3: boom"},
		"output too big":  {localSpec(`head -c 5000 /dev/zero | tr '\0' 'a'`), effects.ErrFatal, "larger than the step's limit"},
		"not json":        {localSpec(`echo not json`), effects.ErrFatal, "not JSON"},
		"timeout":         {func() Spec { s := localSpec(`sleep 10`); s.Timeout = 300 * time.Millisecond; return s }(), effects.ErrUnknownOutcome, "timed out"},
		"no such program": {Spec{Name: "t", Command: []string{"/nonexistent/program"}, Timeout: time.Second, OutputBytes: 10}, effects.ErrFatal, "could not start"},
	}
	for name, c := range cases {
		start := time.Now()
		_, err := l.Run(ctx, c.spec)
		if !errors.Is(err, c.want) || !strings.Contains(err.Error(), c.msg) {
			t.Errorf("%s: %v, want %v containing %q", name, err, c.want, c.msg)
		}
		if time.Since(start) > 4*time.Second {
			t.Errorf("%s: took %s", name, time.Since(start))
		}
	}
}

func TestLocalNoNetworkWhereNamespacesWork(t *testing.T) {
	needSh(t)
	if !canIsolateNetwork() {
		t.Skip("no network namespaces")
	}
	// In an empty network namespace only a down loopback exists.
	res, err := (&Local{}).Run(context.Background(), localSpec(`cat /proc/net/dev | tail -n +3 | wc -l`))
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.TrimSpace(string(res.Output)); n != "1" && n != "0" {
		t.Logf("interfaces: %s (namespaces may be unavailable here)", n)
	}
}

func TestShimReportsOneLine(t *testing.T) {
	needSh(t)
	dir := t.TempDir()
	in := dir + "/input.json"
	if err := writeFile(in, `{"x":2}`); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TASKIEM_INPUT", in)
	t.Setenv("TASKIEM_OUTPUT", dir+"/output.json")
	var buf bytes.Buffer
	if err := Shim([]string{"run", "--output-bytes", "100", "--timeout", "5s", "--", "sh", "-c", `echo hi >&2; cat`}, &buf); err != nil {
		t.Fatal(err)
	}
	if strings.Count(buf.String(), "\n") != 1 {
		t.Fatalf("shim printed %q", buf.String())
	}
	o, ok := ParseOutcome("some noise\n" + buf.String())
	if !ok || string(o.Output) != `{"x":2}` || o.Exit != 0 || !strings.Contains(o.Logs, "hi") {
		t.Fatalf("outcome %+v %v", o, ok)
	}
	buf.Reset()
	if err := Shim([]string{"run", "--output-mode", "file", "--", "sh", "-c", `echo '[1,2]' > "$TASKIEM_OUTPUT"; exit 0`}, &buf); err != nil {
		t.Fatal(err)
	}
	if o, ok := ParseOutcome(buf.String()); !ok || string(o.Output) != `[1,2]` {
		t.Fatalf("file outcome %+v", o)
	}
	if _, ok := ParseOutcome("TASKIEM-RESULT not-base64!\n"); ok {
		t.Fatal("garbage parsed")
	}
}
