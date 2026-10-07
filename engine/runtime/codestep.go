package runtime

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/israel-duff/taskiem/engine/connector"
	"github.com/israel-duff/taskiem/engine/effects"
	"github.com/israel-duff/taskiem/engine/history"
	"github.com/israel-duff/taskiem/engine/sandbox"
	"github.com/israel-duff/taskiem/engine/wd"
)

// compiled caches transpiled code by source hash; versions are immutable,
// so a compiled script never goes stale.
var compiled sync.Map

func compileStep(c *wd.CodeConfig) (string, error) {
	if c.Language != "javascript" && c.Language != "typescript" && c.Language != "python" {
		return "", fmt.Errorf("%s code steps are not available in this engine version: %w", c.Language, effects.ErrFatal)
	}
	key := sha256.Sum256([]byte(c.Language + "\x00" + c.Source))
	if s, ok := compiled.Load(key); ok {
		return s.(string), nil
	}
	s, err := sandbox.Compile(c.Source, c.Language)
	if err != nil {
		return "", err
	}
	compiled.Store(key, s)
	return s, nil
}

// maxFetchBody bounds a host.fetch response handed to a script.
const maxFetchBody = 1 << 20

// maxFetches bounds host.fetch calls in one step.
const maxFetches = 50

// runCode executes a code step in the sandbox.
func (w *Worker) runCode(ctx context.Context, p *plan, input any) (sandbox.Result, error) {
	if p.step == nil || p.step.Code == nil {
		return sandbox.Result{}, fmt.Errorf("code step has no config: %w", effects.ErrFatal)
	}
	cfg := p.step.Code
	script, err := compileStep(cfg)
	if err != nil {
		return sandbox.Result{}, err
	}
	secrets := map[string]string{}
	for _, name := range cfg.Secrets {
		v, err := w.secret(ctx, p, name)
		if err != nil {
			return sandbox.Result{}, fmt.Errorf("secret %q: %w: %w", name, err, effects.ErrFatal)
		}
		secrets[name] = v
	}
	now, _ := history.ParseTime(p.sched.AvailableAt)
	lim := sandbox.DefaultLimits
	if cfg.Limits != nil {
		// Validation refuses more than the ceilings; definitions published
		// before it are held to them here.
		if cfg.Limits.MemoryMB > 0 {
			lim.Memory = min(cfg.Limits.MemoryMB, wd.MaxCodeMemoryMB) << 20
		}
		if d, err := wd.ParseDuration(cfg.Limits.CPU); err == nil && d > 0 {
			lim.Timeout = min(d, wd.MaxCodeCPU)
		}
	}
	var fetches atomic.Int32
	host := sandbox.Host{Secrets: secrets, Now: now, Fetch: func(ctx context.Context, r sandbox.FetchRequest) (sandbox.FetchResponse, error) {
		if fetches.Add(1) > maxFetches {
			return sandbox.FetchResponse{}, fmt.Errorf("more than %d fetches in one step", maxFetches)
		}
		hc, err := w.client(ctx, p, lim.Timeout)
		if err != nil {
			return sandbox.FetchResponse{}, err
		}
		method := strings.ToUpper(r.Method)
		if method == "" {
			method = http.MethodGet
		}
		req, err := http.NewRequestWithContext(ctx, method, r.URL, strings.NewReader(r.Body))
		if err != nil {
			return sandbox.FetchResponse{}, connector.RedactURLError(err)
		}
		for k, v := range r.Headers {
			req.Header.Set(k, v)
		}
		resp, err := hc.Do(req)
		if err != nil {
			return sandbox.FetchResponse{}, connector.RedactURLError(err)
		}
		defer func() { _ = resp.Body.Close() }()
		body, err := io.ReadAll(io.LimitReader(resp.Body, maxFetchBody))
		if err != nil {
			return sandbox.FetchResponse{}, err
		}
		h := map[string]string{}
		for k := range resp.Header {
			h[strings.ToLower(k)] = resp.Header.Get(k)
		}
		return sandbox.FetchResponse{Status: resp.StatusCode, Headers: h, Body: string(body)}, nil
	}}
	return sandbox.Run(ctx, script, input, host, lim)
}
