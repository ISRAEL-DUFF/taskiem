// Package wasmconn runs tenants' own connectors: a connector/v1 manifest
// and a WebAssembly module implementing ABI taskiem-connector/v1
// (docs/contracts/connector-wasm-v1.md), on wazero (spec 6).
//
// Every call gets a fresh instance with capped memory and the call's
// deadline, so a connector cannot crash the engine, keep state between
// calls, or see another tenant's data. Its only way out is the host's HTTP
// function, which goes through the caller's egress-guarded client: the
// hosts the manifest declares, for this tenant, nothing else.
package wasmconn

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
	"github.com/tetratelabs/wazero/imports/wasi_snapshot_preview1"

	"github.com/israel-duff/taskiem/engine/connector"
	"github.com/israel-duff/taskiem/engine/effects"
	"github.com/israel-duff/taskiem/engine/egress"
	"github.com/israel-duff/taskiem/engine/pii"
)

// ABI names the contract modules implement.
const ABI = "taskiem-connector/v1"

const (
	hostModule = "taskiem"
	wasiModule = wasi_snapshot_preview1.ModuleName
	execExport = "taskiem_execute_v1"
	// reactorInit is the WASI reactor's start function (Go, Rust cdylib).
	reactorInit = "_initialize" //nolint:misspell // a WASI export name
)

// The host functions a module may import.
var hostFuncs = map[string]bool{"input_read": true, "http_request": true, "http_response_read": true, "log": true}

// Limits bound one call.
type Limits struct {
	MemoryPages  uint32 // 64 KiB pages per instance; default 2048 (128 MiB)
	MaxModule    int    // bytes; default 32 MiB
	MaxOutput    int    // bytes of result JSON; default 1 MiB
	MaxResponse  int    // bytes of one provider response body; default 4 MiB
	MaxHTTPCalls int    // per call; default 20
	MaxLogs      int    // bytes of log lines kept per call; default 16 KiB
}

func (l Limits) withDefaults() Limits {
	def := func(v *int, d int) {
		if *v <= 0 {
			*v = d
		}
	}
	if l.MemoryPages == 0 {
		l.MemoryPages = 2048
	}
	def(&l.MaxModule, 32<<20)
	def(&l.MaxOutput, 1<<20)
	def(&l.MaxResponse, 4<<20)
	def(&l.MaxHTTPCalls, 20)
	def(&l.MaxLogs, 16<<10)
	return l
}

// Runtime compiles and runs connector modules.
type Runtime struct {
	rt  wazero.Runtime
	lim Limits

	mu       sync.Mutex
	compiled map[string]wazero.CompiledModule // by module digest
}

// New starts a runtime.
func New(ctx context.Context, lim Limits) (*Runtime, error) {
	lim = lim.withDefaults()
	rt := wazero.NewRuntimeWithConfig(ctx, wazero.NewRuntimeConfig().
		WithMemoryLimitPages(lim.MemoryPages).
		WithCloseOnContextDone(true))
	if _, err := wasi_snapshot_preview1.Instantiate(ctx, rt); err != nil {
		return nil, err
	}
	i32 := api.ValueTypeI32
	b := rt.NewHostModuleBuilder(hostModule)
	b.NewFunctionBuilder().WithGoModuleFunction(api.GoModuleFunc(inputRead), []api.ValueType{i32}, nil).Export("input_read")
	b.NewFunctionBuilder().WithGoModuleFunction(api.GoModuleFunc(httpRequest), []api.ValueType{i32, i32}, []api.ValueType{i32}).Export("http_request")
	b.NewFunctionBuilder().WithGoModuleFunction(api.GoModuleFunc(httpResponseRead), []api.ValueType{i32}, nil).Export("http_response_read")
	b.NewFunctionBuilder().WithGoModuleFunction(api.GoModuleFunc(logLine), []api.ValueType{i32, i32}, nil).Export("log")
	if _, err := b.Instantiate(ctx); err != nil {
		return nil, err
	}
	return &Runtime{rt: rt, lim: lim, compiled: map[string]wazero.CompiledModule{}}, nil
}

// Close releases every compiled module.
func (r *Runtime) Close(ctx context.Context) error { return r.rt.Close(ctx) }

// ErrInvalid marks a manifest or module that cannot be loaded.
var ErrInvalid = errors.New("invalid connector")

// Digest is the hex SHA-256 of a module.
func Digest(module []byte) string {
	sum := sha256.Sum256(module)
	return hex.EncodeToString(sum[:])
}

// Load checks a manifest and module and returns the connector they make.
// Compiled modules are cached by digest.
func (r *Runtime) Load(ctx context.Context, manifest, module []byte) (*connector.Connector, error) {
	m, problems := connector.Parse(manifest)
	if len(problems) > 0 {
		return nil, fmt.Errorf("%w: manifest: %s", ErrInvalid, strings.Join(problems, "; "))
	}
	if !strings.HasPrefix(m.ID, connector.TenantPrefix) {
		return nil, fmt.Errorf("%w: a tenant connector's id must start with %q, not %q", ErrInvalid, connector.TenantPrefix, m.ID)
	}
	for name, t := range m.Triggers {
		// Anyone could deliver to an unverified trigger of a tenant's own connector.
		if t.Verify == nil || t.Verify.Scheme == "none" {
			return nil, fmt.Errorf("%w: trigger %q must verify its deliveries with a signature or secret scheme", ErrInvalid, name)
		}
	}
	if len(module) > r.lim.MaxModule {
		return nil, fmt.Errorf("%w: module is %d bytes; the limit is %d", ErrInvalid, len(module), r.lim.MaxModule)
	}
	cm, err := r.compile(ctx, module)
	if err != nil {
		return nil, err
	}
	start := []string{}
	if _, ok := cm.ExportedFunctions()[reactorInit]; ok {
		start = append(start, reactorInit)
	}
	c := &connector.Connector{Manifest: m, Actions: map[string]connector.Action{}}
	for name := range m.Actions {
		c.Actions[name] = &action{r: r, cm: cm, start: start, manifest: m, name: name}
	}
	return c, nil
}

func (r *Runtime) compile(ctx context.Context, module []byte) (wazero.CompiledModule, error) {
	digest := Digest(module)
	r.mu.Lock()
	cm, ok := r.compiled[digest]
	r.mu.Unlock()
	if ok {
		return cm, nil
	}
	cm, err := r.rt.CompileModule(ctx, module)
	if err != nil {
		return nil, fmt.Errorf("%w: the module does not compile: %w", ErrInvalid, err)
	}
	for _, f := range cm.ImportedFunctions() {
		mod, name, _ := f.Import()
		if mod == wasiModule || (mod == hostModule && hostFuncs[name]) {
			continue
		}
		_ = cm.Close(ctx)
		return nil, fmt.Errorf("%w: imports %s.%s, which the host does not provide", ErrInvalid, mod, name)
	}
	ex, ok := cm.ExportedFunctions()[execExport]
	if !ok || !sameTypes(ex.ParamTypes(), api.ValueTypeI32) || !sameTypes(ex.ResultTypes(), api.ValueTypeI64) {
		_ = cm.Close(ctx)
		return nil, fmt.Errorf("%w: the module must export %s(i32) -> i64 (%s)", ErrInvalid, execExport, ABI)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if prev, ok := r.compiled[digest]; ok {
		_ = cm.Close(ctx)
		return prev, nil
	}
	r.compiled[digest] = cm
	return cm, nil
}

func sameTypes(got []api.ValueType, want ...api.ValueType) bool {
	return bytes.Equal(got, want)
}

// action runs one manifest action in a fresh instance.
type action struct {
	r        *Runtime
	cm       wazero.CompiledModule
	start    []string
	manifest *connector.Manifest
	name     string
}

// call is one execution's state, reached by host functions through ctx.
type call struct {
	lim      Limits
	req      connector.Request
	input    []byte
	pending  []byte // the last HTTP response, until the guest reads it
	httpN    int
	sent     bool // a request may have reached the provider
	logBytes int
	ctx      context.Context // for outbound requests
}

type callKey struct{}

type execInput struct {
	Action         string            `json:"action"`
	BaseURL        string            `json:"base_url"`
	Input          map[string]any    `json:"input"`
	Credentials    map[string]string `json:"credentials"`
	IdempotencyKey string            `json:"idempotency_key,omitempty"`
	Attempt        int               `json:"attempt"`
	KeyFirstSent   *time.Time        `json:"key_first_sent,omitempty"`
}

type wireError struct {
	Kind    string `json:"kind"`
	Message string `json:"message"`
}

func (a *action) Execute(ctx context.Context, req connector.Request) (connector.Response, error) {
	in := execInput{Action: a.name, BaseURL: strings.TrimRight(a.manifest.BaseURL, "/"), Input: req.Input, Credentials: req.Credentials,
		IdempotencyKey: req.IdempotencyKey, Attempt: req.Attempt}
	if in.Input == nil {
		in.Input = map[string]any{}
	}
	if in.Credentials == nil {
		in.Credentials = map[string]string{}
	}
	if !req.KeyFirstSent.IsZero() {
		t := req.KeyFirstSent.UTC()
		in.KeyFirstSent = &t
	}
	raw, err := json.Marshal(in)
	if err != nil {
		return connector.Response{}, fmt.Errorf("%s: %w: %w", a.manifest.ID, err, effects.ErrFatal)
	}
	c := &call{lim: a.r.lim, req: req, input: raw, ctx: ctx}
	ctx = context.WithValue(ctx, callKey{}, c)
	out, err := a.run(ctx, c)
	if err != nil {
		return connector.Response{}, c.failure(ctx, a.manifest.ID, err)
	}
	var res struct {
		Output json.RawMessage `json:"output"`
		Error  *wireError      `json:"error"`
	}
	if err := json.Unmarshal(out, &res); err != nil {
		return connector.Response{}, c.classify(a.manifest.ID, "unknown_outcome", "unreadable result: "+err.Error())
	}
	if res.Error != nil {
		return connector.Response{}, c.classify(a.manifest.ID, res.Error.Kind, res.Error.Message)
	}
	var v any
	if len(res.Output) > 0 {
		if err := json.Unmarshal(res.Output, &v); err != nil {
			return connector.Response{}, c.classify(a.manifest.ID, "unknown_outcome", "unreadable output: "+err.Error())
		}
	}
	return connector.Response{Output: v}, nil
}

func (a *action) run(ctx context.Context, c *call) ([]byte, error) {
	cfg := wazero.NewModuleConfig().WithName("").WithStartFunctions(a.start...).
		WithSysWalltime().WithSysNanotime().WithRandSource(rand.Reader).
		WithStdout(&logWriter{c: c}).WithStderr(&logWriter{c: c})
	mod, err := a.r.rt.InstantiateModule(ctx, a.cm, cfg)
	if err != nil {
		return nil, err
	}
	defer func() { _ = mod.Close(context.WithoutCancel(ctx)) }()
	res, err := mod.ExportedFunction(execExport).Call(ctx, uint64(len(c.input)))
	if err != nil {
		return nil, err
	}
	ptr, n := uint32(res[0]>>32), uint32(res[0]) //nolint:gosec // the ABI packs two u32s into one i64
	if int(n) > c.lim.MaxOutput {
		return nil, fmt.Errorf("result is %d bytes; the limit is %d: %w", n, c.lim.MaxOutput, errLimit)
	}
	b, ok := mod.Memory().Read(ptr, n)
	if !ok {
		return nil, errors.New("result is outside the module's memory")
	}
	return bytes.Clone(b), nil
}

var errLimit = errors.New("limit exceeded")

// failure classifies a trap, timeout or limit. Before any request left,
// nothing happened: a timeout may be retried, anything else will fail the
// same way. After one may have left, the outcome is unknown.
func (c *call) failure(ctx context.Context, id string, err error) error {
	if c.sent {
		return fmt.Errorf("connector %s: %w: %w", id, err, effects.ErrUnknownOutcome)
	}
	if ctx.Err() != nil {
		return fmt.Errorf("connector %s: %w: %w", id, err, effects.ErrNotSent)
	}
	return fmt.Errorf("connector %s: %w: %w", id, err, effects.ErrFatal)
}

// classify maps the kind a module reports to the engine's errors. All of a
// module's I/O goes through the host, so the host overrules it on whether
// anything was sent: "not sent" after a request left is an unknown
// outcome, and an unknown outcome when nothing left is "not sent".
func (c *call) classify(id, kind, msg string) error {
	msg = pii.Redact(msg)
	var sentinel error
	switch kind {
	case "retryable":
		sentinel = effects.ErrRetryable
	case "fatal":
		sentinel = effects.ErrFatal
	case "not_sent":
		sentinel = effects.ErrNotSent
		if c.sent {
			sentinel = effects.ErrUnknownOutcome
		}
	case "indeterminate":
		sentinel = effects.ErrIndeterminate
	case "not_found":
		return fmt.Errorf("connector %s: %s: %w", id, msg, connector.ErrNotFound)
	default:
		sentinel = effects.ErrUnknownOutcome
		if !c.sent {
			sentinel = effects.ErrNotSent
		}
	}
	return fmt.Errorf("connector %s: %s: %w", id, msg, sentinel)
}

func callFrom(ctx context.Context) *call {
	c, _ := ctx.Value(callKey{}).(*call)
	return c
}

func inputRead(ctx context.Context, m api.Module, stack []uint64) {
	c := callFrom(ctx)
	if c == nil || !m.Memory().Write(api.DecodeU32(stack[0]), c.input) {
		panic("taskiem.input_read: bad pointer")
	}
}

func httpResponseRead(ctx context.Context, m api.Module, stack []uint64) {
	c := callFrom(ctx)
	if c == nil || !m.Memory().Write(api.DecodeU32(stack[0]), c.pending) {
		panic("taskiem.http_response_read: bad pointer")
	}
	c.pending = nil
}

func logLine(ctx context.Context, m api.Module, stack []uint64) {
	c := callFrom(ctx)
	b, ok := m.Memory().Read(api.DecodeU32(stack[0]), api.DecodeU32(stack[1]))
	if c == nil || !ok {
		panic("taskiem.log: bad pointer")
	}
	c.log(string(b))
}

func (c *call) log(s string) {
	s = strings.TrimRight(s, "\n")
	if s == "" || c.logBytes >= c.lim.MaxLogs || c.req.Logger == nil {
		return
	}
	c.logBytes += len(s)
	c.req.Logger.Info("connector log", "line", pii.Redact(s))
}

type logWriter struct{ c *call }

func (w *logWriter) Write(p []byte) (int, error) {
	for _, l := range strings.Split(string(p), "\n") {
		w.c.log(l)
	}
	return len(p), nil
}

type wireRequest struct {
	Method  string            `json:"method"`
	URL     string            `json:"url"`
	Headers map[string]string `json:"headers"`
	Body    string            `json:"body"`
}

type wireResponse struct {
	Status  int               `json:"status,omitempty"`
	Headers map[string]string `json:"headers,omitempty"`
	Body    string            `json:"body,omitempty"`
	Error   *wireError        `json:"error,omitempty"`
}

func httpRequest(ctx context.Context, m api.Module, stack []uint64) {
	c := callFrom(ctx)
	b, ok := m.Memory().Read(api.DecodeU32(stack[0]), api.DecodeU32(stack[1]))
	if c == nil || !ok {
		panic("taskiem.http_request: bad pointer")
	}
	resp := c.do(bytes.Clone(b))
	c.pending, _ = json.Marshal(resp)
	stack[0] = api.EncodeU32(uint32(len(c.pending))) //nolint:gosec // bounded by MaxResponse, far below 4 GiB
}

func refused(kind, msg string) wireResponse {
	return wireResponse{Error: &wireError{Kind: kind, Message: msg}}
}

// do sends one request with the caller's egress-guarded client.
func (c *call) do(raw []byte) wireResponse {
	var w wireRequest
	if err := json.Unmarshal(raw, &w); err != nil {
		return refused("fatal", "bad request: "+err.Error())
	}
	if c.httpN >= c.lim.MaxHTTPCalls {
		return refused("fatal", fmt.Sprintf("more than %d requests in one call", c.lim.MaxHTTPCalls))
	}
	c.httpN++
	if c.req.HTTP == nil {
		return refused("fatal", "this call has no network")
	}
	body, err := base64.StdEncoding.DecodeString(w.Body)
	if err != nil {
		return refused("fatal", "body is not base64")
	}
	if w.Method == "" {
		w.Method = http.MethodGet
	}
	req, err := http.NewRequestWithContext(c.ctx, strings.ToUpper(w.Method), w.URL, bytes.NewReader(body))
	if err != nil {
		return refused("fatal", err.Error())
	}
	for k, v := range w.Headers {
		req.Header.Set(k, v)
	}
	resp, err := c.req.HTTP.Do(req)
	if err != nil {
		switch err = connector.ClassifyTransport(err); {
		case errors.Is(err, egress.ErrDenied):
			return refused("fatal", err.Error())
		case errors.Is(err, effects.ErrNotSent):
			return refused("not_sent", err.Error())
		}
		c.sent = true
		return refused("unknown_outcome", err.Error())
	}
	c.sent = true
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(io.LimitReader(resp.Body, int64(c.lim.MaxResponse)+1))
	if err != nil {
		return refused("unknown_outcome", "read response: "+err.Error())
	}
	if len(data) > c.lim.MaxResponse {
		return refused("unknown_outcome", fmt.Sprintf("response larger than %d bytes", c.lim.MaxResponse))
	}
	h := map[string]string{}
	for k := range resp.Header {
		h[strings.ToLower(k)] = resp.Header.Get(k)
	}
	return wireResponse{Status: resp.StatusCode, Headers: h, Body: base64.StdEncoding.EncodeToString(data)}
}
