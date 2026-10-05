package sandbox

import (
	"context"
	"errors"
	"os"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/israel-duff/taskiem/engine/effects"
)

func run(t *testing.T, src string, input any, host Host, lim Limits) (Result, error) {
	t.Helper()
	script, err := Compile(src, "javascript")
	if err != nil {
		return Result{}, err
	}
	return Run(context.Background(), script, input, host, lim)
}

func mustRun(t *testing.T, src string, input any) any {
	t.Helper()
	r, err := run(t, src, input, Host{}, DefaultLimits)
	if err != nil {
		t.Fatalf("%v", err)
	}
	return r.Output
}

func TestBasicTransform(t *testing.T) {
	out := mustRun(t, `export default function (input) {
	  const total = input.items.reduce((s, i) => s + i.kobo, 0);
	  return { total, count: input.items.length, names: input.items.map(i => i.name.toUpperCase()) };
	}`, map[string]any{"items": []any{map[string]any{"name": "ada", "kobo": 1500}, map[string]any{"name": "bo", "kobo": 2500}}})
	want := map[string]any{"total": int64(4000), "count": int64(2), "names": []any{"ADA", "BO"}}
	if !reflect.DeepEqual(out, want) {
		t.Errorf("got %#v", out)
	}
}

func TestTypeScriptAndAsync(t *testing.T) {
	script, err := Compile(`
	interface Employee { id: string; net: number }
	export default async function (input: { employees: Employee[] }): Promise<number> {
	  await null;
	  return input.employees.filter((e: Employee) => e.net > 0).length;
	}`, "typescript")
	if err != nil {
		t.Fatal(err)
	}
	r, err := Run(context.Background(), script, map[string]any{"employees": []any{map[string]any{"id": "a", "net": 1}, map[string]any{"id": "b", "net": 0}}}, Host{}, DefaultLimits)
	if err != nil || r.Output != int64(1) {
		t.Errorf("%v %v", r.Output, err)
	}
}

func TestHostSecretNowAndLogs(t *testing.T) {
	r, err := run(t, `export default function (input, host) {
	  console.log("start", {n: 1});
	  host.log("via host");
	  let undeclared = "threw";
	  try { host.secret("other") } catch (e) { undeclared = e.message }
	  return { key: host.secret("api_key").slice(0, 3), now: host.now(), undeclared };
	}`, nil, Host{Secrets: map[string]string{"api_key": "sk_live_123"}, Now: time.Date(2026, 10, 5, 9, 0, 0, 0, time.UTC)}, DefaultLimits)
	if err != nil {
		t.Fatal(err)
	}
	out := r.Output.(map[string]any)
	if out["key"] != "sk_" || out["now"] != "2026-10-05T09:00:00Z" || !strings.Contains(out["undeclared"].(string), "not declared") {
		t.Errorf("output %v", out)
	}
	if !reflect.DeepEqual(r.Logs, []string{`start {"n":1}`, "via host"}) {
		t.Errorf("logs %q", r.Logs)
	}
}

func TestFetch(t *testing.T) {
	_, err := run(t, `export default async function (i, host) { return await host.fetch("https://api.example.com/x") }`, nil, Host{}, DefaultLimits)
	if err == nil || !strings.Contains(err.Error(), "fetch is not enabled") {
		t.Errorf("fetch without a host function: %v", err)
	}
	var got FetchRequest
	host := Host{Fetch: func(_ context.Context, r FetchRequest) (FetchResponse, error) {
		got = r
		return FetchResponse{Status: 200, Body: `{"rate": 1530.5}`}, nil
	}}
	r, err := run(t, `export default async function (i, host) {
	  const res = await host.fetch("https://fx.example.com/ngn", { method: "POST", headers: {"X-A": "1"}, body: "q" });
	  return { status: res.status, rate: res.json().rate };
	}`, nil, host, DefaultLimits)
	if err != nil {
		t.Fatal(err)
	}
	if got.URL != "https://fx.example.com/ngn" || got.Method != "POST" || got.Headers["X-A"] != "1" || got.Body != "q" {
		t.Errorf("request %+v", got)
	}
	if r.Output.(map[string]any)["rate"] != 1530.5 {
		t.Errorf("output %v", r.Output)
	}
}

func TestScriptErrorsAreFatal(t *testing.T) {
	cases := map[string]struct {
		src  string
		want error
	}{
		"throw":     {`export default function () { throw new Error("bad input") }`, ErrScript},
		"no export": {`const x = 1`, ErrNoExport},
		"import":    {`import fs from "fs"; export default () => fs`, ErrScript},
		"syntax":    {`export default function ( {`, ErrScript},
	}
	for name, c := range cases {
		_, err := run(t, c.src, nil, Host{}, DefaultLimits)
		if !errors.Is(err, c.want) || effects.Classify(err) != effects.KindFatal {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestInfiniteLoopIsStopped(t *testing.T) {
	start := time.Now()
	lim := DefaultLimits
	lim.Timeout = 300 * time.Millisecond
	_, err := run(t, `export default function () { while (true) {} }`, nil, Host{}, lim)
	if !errors.Is(err, ErrTimeout) || time.Since(start) > 3*time.Second {
		t.Errorf("err %v after %s", err, time.Since(start))
	}
	// The next run is unaffected.
	if out := mustRun(t, `export default () => "fine"`, nil); out != "fine" {
		t.Errorf("after timeout: %v", out)
	}
}

func TestMemoryIsBounded(t *testing.T) {
	lim := DefaultLimits
	lim.Timeout = 20 * time.Second
	for name, src := range map[string]string{
		"one big string": `export default () => "x".repeat(512 << 20).length`,
		"many pushes":    `export default () => { const a = []; for (let i = 0; i < 100000; i++) a.push("y".repeat(100000) + i); return a.length }`,
	} {
		start := time.Now()
		_, err := run(t, src, nil, Host{}, lim)
		if err == nil {
			t.Errorf("%s: unbounded allocation succeeded", name)
		}
		if !errors.Is(err, ErrMemory) && !errors.Is(err, ErrTimeout) {
			t.Errorf("%s: want a memory or time limit error, got %v", name, err)
		}
		// Measured alone: peak RSS is about 75 MiB plus the WebAssembly cap
		// (204 MiB at 128 MiB). Runs earlier in this process add garbage not
		// yet collected, so this is only a sanity bound.
		if peak := peakRSS(); peak > 768<<20 {
			t.Errorf("%s: peak resident memory %d MiB", name, peak>>20)
		}
		t.Logf("%s: %v after %s, peak RSS %d MiB", name, err, time.Since(start).Round(time.Millisecond), peakRSS()>>20)
	}
}

func TestNoFilesystemNetworkOrStdlib(t *testing.T) {
	out := mustRun(t, `export default () => ({
	  std: typeof std, os: typeof os, print: typeof print, scriptArgs: typeof scriptArgs,
	  fetch: typeof fetch, require: typeof require, process: typeof process, XHR: typeof XMLHttpRequest,
	})`, nil)
	for k, v := range out.(map[string]any) {
		if v != "undefined" {
			t.Errorf("%s is reachable from scripts (%v)", k, v)
		}
	}
	_, err := run(t, `export default async () => { const m = await import("std"); return m.loadFile("/etc/passwd") }`, nil, Host{}, DefaultLimits)
	if err == nil {
		t.Error("dynamic import of std succeeded")
	}
}

func TestRunsAreIsolated(t *testing.T) {
	mustRun(t, `export default () => { globalThis.leak = "secret"; Object.prototype.polluted = true; return 1 }`, nil)
	out := mustRun(t, `export default () => ({ leak: typeof globalThis.leak, polluted: ({}).polluted === true })`, nil)
	if out.(map[string]any)["leak"] != "undefined" || out.(map[string]any)["polluted"] != false {
		t.Errorf("state leaked between runs: %v", out)
	}
}

func TestOutputLimit(t *testing.T) {
	_, err := run(t, `export default () => "z".repeat(300000)`, nil, Host{}, DefaultLimits)
	if !errors.Is(err, ErrOutput) {
		t.Errorf("want output limit, got %v", err)
	}
}

func BenchmarkColdRun(b *testing.B) {
	script, _ := Compile(`export default (i) => i.n * 2`, "javascript")
	for i := 0; i < b.N; i++ {
		if _, err := Run(context.Background(), script, map[string]any{"n": 21}, Host{}, DefaultLimits); err != nil {
			b.Fatal(err)
		}
	}
}

// peakRSS reads the process's peak resident set size (Linux).
func peakRSS() int64 {
	raw, err := os.ReadFile("/proc/self/status")
	if err != nil {
		return 0
	}
	for _, line := range strings.Split(string(raw), "\n") {
		if f := strings.Fields(line); len(f) >= 2 && f[0] == "VmHWM:" {
			kb, _ := strconv.ParseInt(f[1], 10, 64)
			return kb << 10
		}
	}
	return 0
}
