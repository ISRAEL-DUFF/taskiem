package sandbox

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/imports/wasi_snapshot_preview1"
	"github.com/tetratelabs/wazero/sys"

	"github.com/israel-duff/taskiem/engine/effects"
	"github.com/israel-duff/taskiem/engine/expr"
	"github.com/israel-duff/taskiem/engine/sandbox/pywasm"
)

// Python code steps (spec 7.1) run CPython compiled to WebAssembly (WASI)
// on wazero, a fresh instance per execution. The module gets no preopened
// directories, no sockets and no environment; the WebAssembly sandbox is the
// boundary. A small runner, passed with -c, talks to the host over stdin
// and stdout in JSON lines: the step and its input come in, host.fetch,
// host.secret and the result go out. print() goes to the step's logs.

// PythonHeader marks a compiled Python script; it is a Python comment, so
// the script stays valid Python.
const PythonHeader = "# taskiem:python\n"

// protoPrefix starts each protocol line the runner writes.
const protoPrefix = "\x1etaskiem "

// PythonMemory is the process-wide memory cap for Python instances, set
// before the first Python step runs; a step's own limit cannot exceed it.
var PythonMemory = 256 << 20

// pythonBlocked are standard modules that cannot work in the sandbox (no
// sockets, processes or native code); importing one fails with a clear
// message instead of an obscure one.
var pythonBlocked = []string{"socket", "ssl", "select", "selectors", "subprocess", "multiprocessing", "ctypes", "asyncio",
	"http", "urllib.request", "ftplib", "smtplib", "imaplib", "poplib", "telnetlib", "socketserver", "xmlrpc", "webbrowser",
	"concurrent", "threading", "_thread", "signal", "mmap", "pty", "tty", "termios", "resource", "tkinter", "turtle", "sqlite3"}

const pythonRunner = `
import sys, os, json
def _out(obj):
    data = (%q + json.dumps(obj, ensure_ascii=False) + "\n").encode("utf-8")
    while data:
        n = os.write(1, data)
        data = data[n:]
_buf = b""
def _readline():
    global _buf
    while b"\n" not in _buf:
        chunk = os.read(0, 65536)
        if not chunk:
            raise SystemExit(70)
        _buf += chunk
    line, _, _buf = _buf.partition(b"\n")
    return json.loads(line)
def _call(op, **kw):
    kw["op"] = op
    _out(kw)
    return _readline()
class _Log:
    def __init__(self):
        self.buf = ""
    def write(self, s):
        self.buf += s
        if "\n" in self.buf:
            done, _, self.buf = self.buf.rpartition("\n")
            os.write(2, (done + "\n").encode("utf-8", "replace"))
        return len(s)
    def flush(self):
        if self.buf:
            os.write(2, (self.buf + "\n").encode("utf-8", "replace"))
            self.buf = ""
sys.stdout = _Log()
_BLOCKED = set(%s)
class _Block:
    def find_spec(self, name, path=None, target=None):
        if name in _BLOCKED or name.split(".")[0] in _BLOCKED:
            raise ImportError("module %%r is not available in code steps" %% name)
        return None
sys.meta_path.insert(0, _Block())
class Response:
    def __init__(self, r):
        self.status = r.get("status", 0)
        self.headers = r.get("headers") or {}
        self.body = r.get("body", "")
    def text(self):
        return self.body
    def json(self):
        return json.loads(self.body)
class Host:
    def __init__(self, now):
        self._now = now
    def log(self, *args):
        print(*args)
    def now(self):
        return self._now
    def secret(self, name):
        r = _call("secret", name=str(name))
        if r.get("error"):
            raise KeyError(r["error"])
        return r["value"]
    def fetch(self, url, method="GET", headers=None, body=""):
        if not isinstance(body, str):
            body = json.dumps(body)
        r = _call("fetch", url=str(url), method=str(method), headers=dict(headers or {}), body=body)
        if r.get("error"):
            raise OSError(r["error"])
        return Response(r)
def _default(v):
    import decimal, datetime
    if isinstance(v, decimal.Decimal):
        return str(v)
    if isinstance(v, (datetime.datetime, datetime.date, datetime.time)):
        return v.isoformat()
    if isinstance(v, (set, frozenset, tuple)):
        return list(v)
    raise TypeError("%%s is not JSON serialisable" %% type(v).__name__)
def _line(e):
    import traceback
    line = getattr(e, "lineno", None) if isinstance(e, SyntaxError) else None
    for f in traceback.extract_tb(e.__traceback__):
        if f.filename == "step.py":
            line = f.lineno
    return line
_env = _readline()
import atexit
atexit.register(sys.stdout.flush)
try:
    _code = compile(_env["source"], "step.py", "exec")
    if _env.get("check"):
        _out({"op": "ok"})
        raise SystemExit(0)
    _g = {"__name__": "step"}
    exec(_code, _g)
    _main = _g.get("main")
    if not callable(_main):
        _out({"op": "error", "kind": "noexport"})
        raise SystemExit(0)
    _args = (_env["input"], Host(_env["now"]))
    _n = getattr(getattr(_main, "__code__", None), "co_argcount", 2)
    _res = _main(*_args[:max(1, min(2, _n))])
    _out({"op": "result", "json": json.dumps(_res, default=_default, ensure_ascii=False, allow_nan=False)})
except SystemExit:
    raise
except MemoryError:
    _out({"op": "error", "kind": "memory"})
except BaseException as e:
    _out({"op": "error", "kind": "script", "message": "%%s: %%s" %% (type(e).__name__, e), "line": _line(e)})
`

var py struct {
	once   sync.Once
	rt     wazero.Runtime
	mod    wazero.CompiledModule
	runner string
	err    error
}

// InitPython compiles the Python module (seconds the first time; cached
// on disk in TASKIEM_WASM_CACHE, or the user cache directory, afterwards).
// Run and Compile call it when needed.
func InitPython() error {
	py.once.Do(func() {
		ctx := context.Background()
		wasm, err := pywasm.Module()
		if err != nil {
			py.err = err
			return
		}
		cfg := wazero.NewRuntimeConfig().WithCloseOnContextDone(true).WithMemoryLimitPages(uint32(PythonMemory / 65536)) //nolint:gosec // bounded by configuration
		if dir := wasmCacheDir(); dir != "" {
			if cache, err := wazero.NewCompilationCacheWithDir(dir); err == nil {
				cfg = cfg.WithCompilationCache(cache)
			}
		}
		py.rt = wazero.NewRuntimeWithConfig(ctx, cfg)
		if _, err := wasi_snapshot_preview1.Instantiate(ctx, py.rt); err != nil {
			py.err = err
			return
		}
		py.mod, py.err = py.rt.CompileModule(ctx, wasm)
		blocked, _ := json.Marshal(pythonBlocked)
		py.runner = fmt.Sprintf(pythonRunner, protoPrefix, blocked)
	})
	return py.err
}

func wasmCacheDir() string {
	if d := os.Getenv("TASKIEM_WASM_CACHE"); d != "" {
		return d
	}
	if d, err := os.UserCacheDir(); err == nil {
		return filepath.Join(d, "taskiem", "wazero")
	}
	return ""
}

// compilePython checks a Python step's syntax and returns its script.
func compilePython(source string) (string, error) {
	if strings.TrimSpace(source) == "" {
		return "", fmt.Errorf("%w: the step has no code: %w", ErrScript, effects.ErrFatal)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if _, err := runPython(ctx, source, map[string]any{"source": source, "check": true}, Host{}, Limits{Timeout: 30 * time.Second, MaxOutput: 1 << 10}); err != nil {
		if errors.Is(err, errPythonOK) {
			return PythonHeader + source, nil
		}
		return "", err
	}
	return PythonHeader + source, nil
}

var errPythonOK = errors.New("python: syntax ok")

// pyIO is the instance's stdin and stdout: it answers the runner's host
// calls as they are written, and keeps the outcome.
type pyIO struct {
	ctx     context.Context
	host    Host
	maxLine int
	in      bytes.Buffer
	pending []byte
	result  *string
	fail    error
	ok      bool
}

func (p *pyIO) Read(b []byte) (int, error) {
	if p.in.Len() == 0 {
		return 0, io.EOF // the runner asked without a host call pending
	}
	return p.in.Read(b)
}

func (p *pyIO) reply(v any) {
	raw, _ := json.Marshal(v)
	p.in.Write(raw)
	p.in.WriteByte('\n')
}

func (p *pyIO) Write(b []byte) (int, error) {
	p.pending = append(p.pending, b...)
	for {
		i := bytes.IndexByte(p.pending, '\n')
		if i < 0 {
			if len(p.pending) > p.maxLine {
				p.fail = fmt.Errorf("%w: more than %d bytes", ErrOutput, p.maxLine)
				return 0, p.fail
			}
			return len(b), nil
		}
		line := p.pending[:i]
		p.pending = p.pending[i+1:]
		p.handle(line)
	}
}

func (p *pyIO) handle(line []byte) {
	msg, ok := bytes.CutPrefix(line, []byte(protoPrefix))
	if !ok {
		return // not ours: nothing else writes to stdout
	}
	var m struct {
		Op      string            `json:"op"`
		Name    string            `json:"name"`
		URL     string            `json:"url"`
		Method  string            `json:"method"`
		Headers map[string]string `json:"headers"`
		Body    string            `json:"body"`
		JSON    string            `json:"json"`
		Kind    string            `json:"kind"`
		Message string            `json:"message"`
		Line    *int              `json:"line"`
	}
	if err := json.Unmarshal(msg, &m); err != nil {
		p.fail = fmt.Errorf("%w: sandbox protocol: %w", ErrScript, err)
		return
	}
	switch m.Op {
	case "secret":
		if v, ok := p.host.Secrets[m.Name]; ok {
			p.reply(map[string]string{"value": v})
		} else {
			p.reply(map[string]string{"error": "secret " + m.Name + " is not declared by this step"})
		}
	case "fetch":
		if p.host.Fetch == nil {
			p.reply(map[string]string{"error": "fetch is not enabled for this step"})
			return
		}
		out, err := p.host.Fetch(p.ctx, FetchRequest{URL: m.URL, Method: m.Method, Headers: m.Headers, Body: m.Body})
		if err != nil {
			p.reply(map[string]string{"error": "fetch: " + err.Error()})
			return
		}
		p.reply(out)
	case "ok":
		p.ok = true
	case "result":
		p.result = &m.JSON
	case "error":
		switch m.Kind {
		case "noexport":
			p.fail = errNoMain
		case "memory":
			p.fail = ErrMemory
		default:
			where := ""
			if m.Line != nil {
				where = fmt.Sprintf(" (line %d)", *m.Line)
			}
			p.fail = fmt.Errorf("%w: %s%s", ErrScript, firstLine(m.Message), where)
		}
	}
}

var errNoMain = errors.New("python code step must define main(input, host)")

// runPython runs the runner with env as its first stdin line.
func runPython(ctx context.Context, source string, env map[string]any, host Host, lim Limits) (res Result, err error) {
	if err := InitPython(); err != nil {
		return res, fmt.Errorf("%w: python is unavailable: %w", ErrScript, err)
	}
	if lim.Timeout <= 0 {
		lim = DefaultLimits
	}
	ctx, cancel := context.WithTimeout(ctx, lim.Timeout)
	defer cancel()
	logs := &logBuffer{max: lim.MaxLogs}
	defer func() {
		res.Logs = logs.lines
		if err != nil && !errors.Is(err, effects.ErrFatal) {
			err = fmt.Errorf("%w: %w", err, effects.ErrFatal)
		}
	}()
	maxLine := lim.MaxOutput + 64<<10
	if lim.MaxOutput <= 0 {
		maxLine = 4 << 20
	}
	pio := &pyIO{ctx: ctx, host: host, maxLine: maxLine}
	envRaw, err := json.Marshal(env)
	if err != nil {
		return res, fmt.Errorf("%w: input is not JSON: %w", ErrScript, err)
	}
	pio.in.Write(envRaw)
	pio.in.WriteByte('\n')
	now := host.Now
	cfg := wazero.NewModuleConfig().WithName("").WithArgs("python", "-I", "-S", "-c", py.runner).
		WithStdin(pio).WithStdout(pio).WithStderr(logs).
		WithSysNanotime().WithSysNanosleep().WithRandSource(rand.Reader)
	if !now.IsZero() {
		// time.time() is the run's logical time, as host.now() is.
		cfg = cfg.WithWalltime(func() (int64, int32) { return now.Unix(), int32(now.Nanosecond()) }, 1) //nolint:gosec // nanoseconds fit
	} else {
		cfg = cfg.WithSysWalltime()
	}
	mod, runErr := py.rt.InstantiateModule(ctx, py.mod, cfg)
	if mod != nil {
		_ = mod.Close(context.Background())
	}
	var exit *sys.ExitError
	switch {
	case ctx.Err() != nil:
		return res, ErrTimeout
	case pio.fail != nil:
		return res, pio.fail
	case runErr != nil && (!errors.As(runErr, &exit) || exit.ExitCode() != 0):
		if msg := runErr.Error(); strings.Contains(msg, "out of bounds memory") || strings.Contains(msg, "memory") && strings.Contains(msg, "grow") {
			return res, ErrMemory
		}
		if tail := lastLine(logs.lines); strings.Contains(tail, "MemoryError") {
			return res, ErrMemory
		}
		return res, fmt.Errorf("%w: interpreter stopped: %s", ErrScript, firstLine(runErr.Error()))
	case pio.ok:
		return res, errPythonOK
	case pio.result == nil:
		return res, fmt.Errorf("%w: the step ended without a result", ErrScript)
	}
	s := *pio.result
	if lim.MaxOutput > 0 && len(s) > lim.MaxOutput {
		return res, fmt.Errorf("%w: %d bytes, limit %d", ErrOutput, len(s), lim.MaxOutput)
	}
	res.JSON = s
	if res.Output, err = expr.DecodeJSON([]byte(s)); err != nil {
		return res, fmt.Errorf("%w: output is not JSON: %w", ErrScript, err)
	}
	return res, nil
}

func lastLine(lines []string) string {
	if len(lines) == 0 {
		return ""
	}
	return lines[len(lines)-1]
}
