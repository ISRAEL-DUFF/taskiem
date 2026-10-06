package sandbox

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func runPy(t *testing.T, src string, input any, host Host, lim Limits) (Result, error) {
	t.Helper()
	script, err := Compile(src, "python")
	if err != nil {
		return Result{}, err
	}
	return Run(context.Background(), script, input, host, lim)
}

func TestPythonStep(t *testing.T) {
	if testing.Short() {
		t.Skip("compiles CPython")
	}
	now := time.Date(2026, 10, 6, 9, 30, 0, 0, time.UTC)
	var fetched FetchRequest
	host := Host{Now: now, Secrets: map[string]string{"api_key": "s3cret"}, Fetch: func(_ context.Context, r FetchRequest) (FetchResponse, error) {
		fetched = r
		return FetchResponse{Status: 200, Headers: map[string]string{"content-type": "application/json"}, Body: `{"rate": "1650.25"}`}, nil
	}}
	src := `
from decimal import Decimal, ROUND_HALF_EVEN
import time

def main(input, host):
    print("converting", len(input["items"]), "items")
    rate = Decimal(host.fetch("https://fx.example/ngn", headers={"Authorization": host.secret("api_key")}).json()["rate"])
    total = sum(Decimal(str(i["usd"])) for i in input["items"])
    return {
        "ngn": (total * rate).quantize(Decimal("0.01"), rounding=ROUND_HALF_EVEN),
        "at": host.now(),
        "clock": int(time.time()),
        "names": sorted({i["name"] for i in input["items"]}),
    }
`
	res, err := runPy(t, src, map[string]any{"items": []any{map[string]any{"name": "b", "usd": 10.5}, map[string]any{"name": "a", "usd": 2}}}, host, DefaultLimits)
	if err != nil {
		t.Fatal(err)
	}
	if res.JSON != `{"ngn": "20628.12", "at": "2026-10-06T09:30:00Z", "clock": 1791279000, "names": ["a", "b"]}` {
		t.Errorf("output %s", res.JSON)
	}
	if fetched.URL != "https://fx.example/ngn" || fetched.Headers["Authorization"] != "s3cret" {
		t.Errorf("fetch %+v", fetched)
	}
	if strings.Join(res.Logs, "|") != "converting 2 items" {
		t.Errorf("logs %q", res.Logs)
	}

	// main(input) alone is fine too. (12.5 × 1650.25 = 20628.125: half-even.)
	res, err = runPy(t, "def main(input):\n    return input['n'] * 2\n", map[string]any{"n": 21}, Host{}, DefaultLimits)
	if err != nil || res.JSON != "42" {
		t.Errorf("one-argument main: %v %s", err, res.JSON)
	}
}

func TestPythonFailures(t *testing.T) {
	if testing.Short() {
		t.Skip("compiles CPython")
	}
	cases := []struct {
		name, src string
		lim       Limits
		want      error
		msg       string
	}{
		{"syntax", "def main(input):\n    return (\n", DefaultLimits, ErrScript, "line 2"},
		{"exception", "def main(input):\n    x = {}\n    return x['missing']\n", DefaultLimits, ErrScript, "KeyError: 'missing' (line 3)"},
		{"no main", "x = 1\n", DefaultLimits, errNoMain, ""},
		{"blocked import", "import socket\ndef main(input):\n    return 1\n", DefaultLimits, ErrScript, "module 'socket' is not available"},
		{"no files", "def main(input):\n    return open('/etc/passwd').read()\n", DefaultLimits, ErrScript, "FileNotFoundError"},
		{"undeclared secret", "def main(input, host):\n    return host.secret('other')\n", DefaultLimits, ErrScript, "not declared"},
		{"no fetch", "def main(input, host):\n    return host.fetch('https://x.test').status\n", DefaultLimits, ErrScript, "fetch is not enabled"},
		{"not json", "def main(input):\n    return object()\n", DefaultLimits, ErrScript, "not JSON serialisable"},
		{"timeout", "def main(input):\n    while True:\n        pass\n", Limits{Timeout: time.Second, MaxOutput: 1 << 10}, ErrTimeout, ""},
		{"memory", "def main(input):\n    return len(bytearray(1 << 30))\n", DefaultLimits, ErrMemory, ""},
		{"output", "def main(input):\n    return 'x' * 5000\n", Limits{Timeout: 10 * time.Second, MaxOutput: 1000}, ErrOutput, ""},
	}
	if err := InitPython(); err != nil {
		t.Fatal(err)
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			_, err := runPy(t, c.src, map[string]any{}, Host{}, c.lim)
			if !errors.Is(err, c.want) || !strings.Contains(err.Error(), c.msg) {
				t.Fatalf("got %v, want %v containing %q", err, c.want, c.msg)
			}
		})
	}
}
