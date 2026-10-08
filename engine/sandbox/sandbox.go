// Package sandbox runs code steps: JavaScript (and TypeScript, stripped at
// save time) in QuickJS, and Python in CPython, both compiled to
// WebAssembly on wazero (spec 7). A script gets no network, filesystem, or
// clock except through the host object.
package sandbox

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/evanw/esbuild/pkg/api"
	"github.com/fastschema/qjs"

	"github.com/israel-duff/taskiem/engine/effects"
	"github.com/israel-duff/taskiem/engine/expr"
	"github.com/israel-duff/taskiem/engine/lru"
)

// Limits bound one execution (spec 7.2).
type Limits struct {
	Memory    int           // bytes for QuickJS's heap; the WebAssembly memory cap is process-wide
	Timeout   time.Duration // wall-clock budget
	MaxOutput int           // bytes of JSON output
	MaxLogs   int           // bytes of log output kept
}

// DefaultLimits match the spec's defaults.
var DefaultLimits = Limits{Memory: 64 << 20, Timeout: 10 * time.Second, MaxOutput: 256 << 10, MaxLogs: 16 << 10}

// FetchRequest and FetchResponse are host.fetch's arguments and result.
type FetchRequest struct {
	URL     string            `json:"url"`
	Method  string            `json:"method"`
	Headers map[string]string `json:"headers"`
	Body    string            `json:"body"`
}

type FetchResponse struct {
	Status  int               `json:"status"`
	Headers map[string]string `json:"headers"`
	Body    string            `json:"body"`
}

// Host is what a script may reach (spec 7.3).
type Host struct {
	Secrets map[string]string // only the secrets the step declares
	Now     time.Time         // the run's logical time
	// Fetch performs an egress-guarded HTTP request; nil disables fetch.
	Fetch func(ctx context.Context, r FetchRequest) (FetchResponse, error)
}

// Result is a completed execution.
type Result struct {
	Output any
	JSON   string // Output as the script serialised it, key order kept
	Logs   []string
}

// Errors returned by Run. Script errors and limit breaches are fatal for
// the step: re-running the same code on the same input fails the same way.
var (
	ErrScript   = errors.New("script error")
	ErrTimeout  = errors.New("script exceeded its time limit")
	ErrMemory   = errors.New("script exceeded its memory limit")
	ErrOutput   = errors.New("script output too large")
	ErrNoExport = errors.New("code step must export a default function")
)

// compiled keeps successful compiles by language and source hash, so a
// version checked on every read (and a Python syntax check, which runs the
// interpreter) is compiled once per process. Bounded: sources come from
// tenants (self-review S34).
var compiled = lru.New[[32]byte, string](2048, 0)

// Compile turns step source into the script the sandbox runs. TypeScript
// types are stripped and the module is bundled into one expression; imports
// are not allowed (packages are curated and bundled by the platform).
func Compile(source, language string) (string, error) {
	key := sha256.Sum256([]byte(language + "\x00" + source))
	if s, ok := compiled.Get(key); ok {
		return s, nil
	}
	s, err := compile(source, language)
	if err == nil {
		compiled.Put(key, s)
	}
	return s, err
}

func compile(source, language string) (string, error) {
	if language == "python" {
		return compilePython(source)
	}
	loader := api.LoaderJS
	if language == "typescript" {
		loader = api.LoaderTS
	}
	res := api.Transform(source, api.TransformOptions{
		Loader:     loader,
		Format:     api.FormatIIFE,
		GlobalName: "__taskiem_module",
		Target:     api.ES2022,
		Sourcefile: "step." + map[bool]string{true: "ts", false: "js"}[language == "typescript"],
	})
	if len(res.Errors) > 0 {
		msgs := make([]string, len(res.Errors))
		for i, m := range res.Errors {
			loc := ""
			if m.Location != nil {
				loc = fmt.Sprintf("%d:%d: ", m.Location.Line, m.Location.Column)
			}
			msgs[i] = loc + m.Text
		}
		return "", fmt.Errorf("%w: %s: %w", ErrScript, strings.Join(msgs, "; "), effects.ErrFatal)
	}
	code := string(res.Code)
	if strings.Contains(code, "require(") {
		return "", fmt.Errorf("%w: imports are not available in code steps: %w", ErrScript, effects.ErrFatal)
	}
	return code, nil
}

// prelude removes QuickJS's built-in std/os objects and defines the host.
const prelude = `
delete globalThis.std; delete globalThis.os; delete globalThis.scriptArgs; delete globalThis.print;
globalThis.console = { log: (...a) => __host_log(a.map(x => typeof x === 'string' ? x : JSON.stringify(x)).join(' ')) };
console.info = console.warn = console.error = console.debug = console.log;
globalThis.host = Object.freeze({
  log: (...a) => console.log(...a),
  secret: (name) => { const v = __host_secret(String(name)); if (v === null) throw new Error('secret ' + name + ' is not declared by this step'); return v; },
  now: () => __host_now(),
  fetch: async (url, opts) => {
    const r = JSON.parse(await __host_fetch(JSON.stringify(Object.assign({ url: String(url), method: 'GET', headers: {}, body: '' }, opts || {}))));
    if (r.error) throw new Error(r.error);
    return Object.assign(r, { json: () => JSON.parse(r.body), text: () => r.body });
  },
});
delete globalThis.__taskiem_host_installed;
`

var initOnce sync.Once

// Init sets the process-wide WebAssembly memory cap and compiles the QuickJS
// module (about a second), so no script's time limit pays for it. Call it
// at worker start; Run calls it with the default cap otherwise.
func Init(maxMemoryBytes int) {
	initOnce.Do(func() {
		if pages := maxMemoryBytes / 65536; pages > 0 && pages <= 65536 {
			qjs.MemoryLimitPages = uint32(pages) //nolint:gosec // bounded above
		}
		if rt, err := qjs.New(qjs.Option{CloseOnContextDone: true}); err == nil {
			rt.Close()
		}
	})
}

// Run executes a compiled script's default export with input and returns
// its JSON-compatible result. Each run gets a fresh runtime: no state is
// shared between runs or tenants.
func Run(ctx context.Context, script string, input any, host Host, lim Limits) (res Result, err error) {
	if strings.HasPrefix(script, PythonHeader) {
		now := ""
		if !host.Now.IsZero() {
			now = host.Now.UTC().Format(time.RFC3339Nano)
		}
		return runPython(ctx, script, map[string]any{"source": strings.TrimPrefix(script, PythonHeader), "input": input, "now": now}, host, lim)
	}
	Init(0)
	if lim.Timeout <= 0 {
		lim = DefaultLimits
	}
	inJSON, err := json.Marshal(input)
	if err != nil {
		return res, fmt.Errorf("%w: input is not JSON: %w", ErrScript, err)
	}
	ctx, cancel := context.WithTimeout(ctx, lim.Timeout)
	defer cancel()

	logs := &logBuffer{max: lim.MaxLogs}
	defer func() {
		res.Logs = logs.lines
		if r := recover(); r != nil {
			// The library panics when a deadline interrupts the module.
			switch {
			case ctx.Err() != nil:
				err = ErrTimeout
			default:
				err = fmt.Errorf("%w: sandbox failure: %v", ErrScript, firstLine(fmt.Sprint(r)))
			}
		}
		if err != nil && !errors.Is(err, effects.ErrFatal) {
			err = fmt.Errorf("%w: %w", err, effects.ErrFatal)
		}
	}()

	rt, err := qjs.New(qjs.Option{Context: ctx, CloseOnContextDone: true, MemoryLimit: lim.Memory, MaxStackSize: 1 << 20, Stdout: logs, Stderr: logs})
	if err != nil {
		return res, fmt.Errorf("%w: %w", ErrScript, err)
	}
	defer func() {
		defer func() { _ = recover() }()
		rt.Close()
	}()
	c := rt.Context()
	c.SetFunc("__host_log", func(this *qjs.This) (*qjs.Value, error) {
		if a := this.Args(); len(a) > 0 {
			logs.add(a[0].String())
		}
		return this.Context().NewUndefined(), nil
	})
	c.SetFunc("__host_secret", func(this *qjs.This) (*qjs.Value, error) {
		a := this.Args()
		if len(a) == 0 {
			return this.Context().NewNull(), nil
		}
		v, ok := host.Secrets[a[0].String()]
		if !ok {
			return this.Context().NewNull(), nil
		}
		return this.Context().NewString(v), nil
	})
	now := host.Now.UTC().Format(time.RFC3339Nano)
	c.SetFunc("__host_now", func(this *qjs.This) (*qjs.Value, error) {
		return this.Context().NewString(now), nil
	})
	c.SetAsyncFunc("__host_fetch", func(this *qjs.This) {
		out := FetchResponse{}
		var errMsg string
		var req FetchRequest
		if a := this.Args(); len(a) == 0 || json.Unmarshal([]byte(a[0].String()), &req) != nil {
			errMsg = "fetch: bad arguments"
		} else if host.Fetch == nil {
			errMsg = "fetch is not enabled for this step"
		} else if out, err = host.Fetch(ctx, req); err != nil {
			errMsg = "fetch: " + err.Error()
		}
		var raw []byte
		if errMsg != "" {
			raw, _ = json.Marshal(map[string]string{"error": errMsg})
		} else {
			raw, _ = json.Marshal(out)
		}
		_ = this.Promise().Resolve(this.Context().NewString(string(raw)))
	})
	if _, err := c.Eval("prelude.js", qjs.Code(prelude)); err != nil {
		return res, fmt.Errorf("%w: prelude: %w", ErrScript, err)
	}
	c.Global().SetPropertyStr("__input", c.ParseJSON(string(inJSON)))
	if _, err := c.Eval("step.js", qjs.Code(script)); err != nil {
		return res, classify(ctx, err)
	}
	v, err := c.Eval("main.js", qjs.Code(`(async () => {
		const m = globalThis.__taskiem_module;
		const fn = m && (m.default || m);
		if (typeof fn !== 'function') throw new Error('__noexport__');
		const out = await fn(globalThis.__input, globalThis.host);
		return JSON.stringify(out === undefined ? null : out);
	})()`))
	if err == nil && v.IsPromise() {
		v, err = v.Await()
	}
	if err != nil {
		if strings.Contains(err.Error(), "__noexport__") {
			return res, ErrNoExport
		}
		return res, classify(ctx, err)
	}
	s := v.String()
	if lim.MaxOutput > 0 && len(s) > lim.MaxOutput {
		return res, fmt.Errorf("%w: %d bytes, limit %d", ErrOutput, len(s), lim.MaxOutput)
	}
	res.JSON = s
	if res.Output, err = expr.DecodeJSON([]byte(s)); err != nil {
		return res, fmt.Errorf("%w: output is not JSON: %w", ErrScript, err)
	}
	return res, nil
}

func classify(ctx context.Context, err error) error {
	msg := err.Error()
	switch {
	case ctx.Err() != nil:
		return ErrTimeout
	case strings.Contains(msg, "out of memory"):
		return ErrMemory
	}
	return fmt.Errorf("%w: %s", ErrScript, firstLine(msg))
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

type logBuffer struct {
	mu    sync.Mutex
	max   int
	size  int
	lines []string
}

func (b *logBuffer) add(s string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.max > 0 && b.size+len(s) > b.max {
		if b.size < b.max {
			b.lines = append(b.lines, "[logs truncated]")
			b.size = b.max
		}
		return
	}
	b.size += len(s)
	b.lines = append(b.lines, s)
}

func (b *logBuffer) Write(p []byte) (int, error) {
	for _, l := range strings.Split(strings.TrimRight(string(p), "\n"), "\n") {
		b.add(l)
	}
	return len(p), nil
}
