package wasmconn

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/israel-duff/taskiem/engine/connector"
)

// tinyModule is a hand-written module implementing the ABI: it calls
// taskiem.http_request once with a fixed GET, ignores the answer, and
// returns {"output":{"n":N}} from its data. Each n makes a different
// module (digest).
func tinyModule(n int) []byte {
	result := fmt.Sprintf(`{"output":{"n":%d}}`, n)
	request := `{"method":"GET","url":"http://blocker.test/"}`
	const reqAt = 256
	vec := func(items ...[]byte) []byte {
		out := uleb(uint64(len(items)))
		for _, it := range items {
			out = append(out, it...)
		}
		return out
	}
	name := func(s string) []byte { return append(uleb(uint64(len(s))), s...) }
	section := func(id byte, body []byte) []byte {
		return append(append([]byte{id}, uleb(uint64(len(body)))...), body...)
	}
	cat := func(parts ...[]byte) []byte {
		var out []byte
		for _, p := range parts {
			out = append(out, p...)
		}
		return out
	}
	types := vec(
		[]byte{0x60, 1, 0x7f, 1, 0x7e},       // 0: (i32) -> i64
		[]byte{0x60, 2, 0x7f, 0x7f, 1, 0x7f}, // 1: (i32, i32) -> i32
	)
	imports := vec(cat(name("taskiem"), name("http_request"), []byte{0x00, 1}))
	funcs := vec([]byte{0})
	memory := vec([]byte{0x00, 1})
	exports := vec(cat(name("memory"), []byte{0x02, 0}), cat(name(execExport), []byte{0x00, 1}))
	body := cat([]byte{0}, // no locals
		[]byte{0x41}, sleb(reqAt), []byte{0x41}, sleb(int64(len(request))), []byte{0x10, 0}, []byte{0x1a}, // http_request(reqAt, len); drop
		[]byte{0x42}, sleb(int64(len(result))), []byte{0x0b}) // i64.const (0 << 32 | len); end
	code := vec(cat(uleb(uint64(len(body))), body))
	data := vec(
		cat([]byte{0x00, 0x41, 0x00, 0x0b}, name(result)),
		cat([]byte{0x00, 0x41}, sleb(reqAt), []byte{0x0b}, name(request)),
	)
	return cat([]byte{0x00, 0x61, 0x73, 0x6d, 1, 0, 0, 0},
		section(1, types), section(2, imports), section(3, funcs), section(5, memory),
		section(7, exports), section(10, code), section(11, data))
}

func uleb(v uint64) []byte {
	var out []byte
	for {
		b := byte(v & 0x7f)
		v >>= 7
		if v != 0 {
			out = append(out, b|0x80)
			continue
		}
		return append(out, b)
	}
}

func sleb(v int64) []byte {
	var out []byte
	for {
		b := byte(v & 0x7f)
		v >>= 7
		if (v == 0 && b&0x40 == 0) || (v == -1 && b&0x40 != 0) {
			return append(out, b)
		}
		out = append(out, b|0x80)
	}
}

const tinyManifest = `
manifest: connector/v1
id: x_tiny
version: 1.0.0
name: Tiny
description: Calls once and answers.
category: other
auth: { type: none, fields: [] }
base_url: https://blocker.test
egress_hosts: [blocker.test]
actions:
  run:
    title: Run
    class: read
    input: { type: object }
    output: { type: object }
`

// gateTransport holds each request until the test lets it go.
type gateTransport struct {
	entered chan struct{}
	release chan struct{}
}

func (g *gateTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	g.entered <- struct{}{}
	select {
	case <-g.release:
	case <-r.Context().Done():
		return nil, r.Context().Err()
	}
	return &http.Response{StatusCode: 200, Body: http.NoBody, Header: http.Header{}, Request: r}, nil
}

func (r *Runtime) cached() (digests []string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for e := r.lru.Front(); e != nil; e = e.Next() {
		digests = append(digests, e.Value.(*compiledModule).digest)
	}
	return digests
}

func tinyLoad(t *testing.T, rt *Runtime, n int) *connector.Connector {
	t.Helper()
	c, err := rt.Load(context.Background(), []byte(tinyManifest), tinyModule(n))
	if err != nil {
		t.Fatalf("module %d: %v", n, err)
	}
	return c
}

func runTiny(c *connector.Connector, tr http.RoundTripper) (any, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	resp, err := c.Actions["run"].Execute(ctx, connector.Request{HTTP: &http.Client{Transport: tr}})
	return resp.Output, err
}

// TestCompiledModulesAreEvicted: the runtime keeps at most MaxCompiled
// compiled modules, least recently used out first, and compiles an
// evicted one again when it is next called.
func TestCompiledModulesAreEvicted(t *testing.T) {
	rt, err := New(context.Background(), Limits{MaxCompiled: 2})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rt.Close(context.Background()) }()
	open := &gateTransport{entered: make(chan struct{}, 8), release: make(chan struct{})}
	close(open.release)
	c1, c2 := tinyLoad(t, rt, 1), tinyLoad(t, rt, 2)
	if _, err := runTiny(c1, open); err != nil { // 1 is now the most recent
		t.Fatal(err)
	}
	tinyLoad(t, rt, 3)
	got := rt.cached()
	if len(got) != 2 || got[0] != Digest(tinyModule(3)) || got[1] != Digest(tinyModule(1)) {
		t.Fatalf("cache holds %v; want modules 3 and 1", got)
	}
	// Module 2 was evicted; calling it compiles it again.
	out, err := runTiny(c2, open)
	if err != nil || fmt.Sprint(out) != "map[n:2]" {
		t.Fatalf("evicted module after recompiling: %v %v", out, err)
	}
	if got := rt.cached(); len(got) != 2 || got[0] != Digest(tinyModule(2)) {
		t.Errorf("cache holds %v after recompiling module 2", got)
	}
}

// TestRunningCallKeepsItsModule: a module evicted while a call runs is not
// closed under it; it is closed once the call returns.
func TestRunningCallKeepsItsModule(t *testing.T) {
	rt, err := New(context.Background(), Limits{MaxCompiled: 1})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rt.Close(context.Background()) }()
	c1 := tinyLoad(t, rt, 1)
	rt.mu.Lock()
	e1 := rt.compiled[Digest(tinyModule(1))]
	rt.mu.Unlock()

	gate := &gateTransport{entered: make(chan struct{}, 1), release: make(chan struct{})}
	type result struct {
		out any
		err error
	}
	done := make(chan result, 1)
	go func() {
		out, err := runTiny(c1, gate)
		done <- result{out, err}
	}()
	select {
	case <-gate.entered: // the call is inside module 1
	case <-time.After(20 * time.Second):
		t.Fatal("the call never reached its HTTP request")
	}
	tinyLoad(t, rt, 2) // evicts module 1 while it runs
	tinyLoad(t, rt, 3)
	rt.mu.Lock()
	evicted, closed, refs := e1.evicted, e1.closed, e1.refs
	rt.mu.Unlock()
	if !evicted || closed || refs != 1 {
		t.Fatalf("module 1 while running: evicted=%v closed=%v refs=%d", evicted, closed, refs)
	}
	close(gate.release)
	r := <-done
	if r.err != nil || fmt.Sprint(r.out) != "map[n:1]" {
		t.Fatalf("call across eviction: %v %v", r.out, r.err)
	}
	rt.mu.Lock()
	closed, refs = e1.closed, e1.refs
	rt.mu.Unlock()
	if !closed || refs != 0 {
		t.Errorf("module 1 after its call: closed=%v refs=%d", closed, refs)
	}
	// Not cached, but still callable: compiled again.
	open := &gateTransport{entered: make(chan struct{}, 1), release: make(chan struct{})}
	close(open.release)
	if out, err := runTiny(c1, open); err != nil || fmt.Sprint(out) != "map[n:1]" {
		t.Errorf("after eviction: %v %v", out, err)
	}
}

// TestInvalidModuleIsNotCached: a module that fails the checks is closed
// and leaves nothing in the cache.
func TestInvalidModuleIsNotCached(t *testing.T) {
	rt, err := New(context.Background(), Limits{MaxCompiled: 2})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rt.Close(context.Background()) }()
	if _, err := rt.Load(context.Background(), []byte(tinyManifest), []byte("\x00asm\x01\x00\x00\x00")); !errors.Is(err, ErrInvalid) {
		t.Fatalf("empty module: %v", err)
	}
	if got := rt.cached(); len(got) != 0 {
		t.Errorf("cache holds %v", got)
	}
}
