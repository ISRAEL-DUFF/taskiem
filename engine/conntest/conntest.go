// Package conntest is the connector conformance kit (spec 6.4: connectors
// ship recorded fixtures, replayed against their handlers). A suite is a
// set of cases, each an action call with its input, the provider
// exchanges it must make (in the same fixture format built-in connectors
// use, testdata/fixtures/<name>.json), and the output or error kind it
// must end with. Run replays a suite against any connector, built-in or a
// WebAssembly module in the sandbox, with no network but the replay
// server: a request anywhere else is refused.
//
// Beyond pass or fail, a run reports what reviewers need to judge a
// manifest honest: actions no case covers, idempotent writes whose key
// never reached the provider, reads that sent something other than GET or
// HEAD, and outputs that depart from the declared schema (the drift
// monitor's check).
package conntest

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/israel-duff/taskiem/engine/connector"
	"github.com/israel-duff/taskiem/engine/drift"
	"github.com/israel-duff/taskiem/engine/effects"
	"github.com/israel-duff/taskiem/engine/egress"
)

// Exchange is one recorded request and its response.
type Exchange struct {
	Name     string   `json:"name"`
	Request  Request  `json:"request"`
	Response Response `json:"response"`
}

// Request is what the connector must send.
type Request struct {
	Method  string            `json:"method"`
	Path    string            `json:"path"`
	Query   map[string]string `json:"query,omitempty"`
	Headers map[string]string `json:"headers,omitempty"` // must be present with these values
	Body    any               `json:"body,omitempty"`    // compared as JSON when set
}

// Response is what the provider answers.
type Response struct {
	Status  int               `json:"status"`
	Headers map[string]string `json:"headers,omitempty"`
	Body    any               `json:"body"`
}

// Case is one conformance case: an action call and what must come of it.
type Case struct {
	Name        string            `json:"name"`
	Action      string            `json:"action"`
	Input       map[string]any    `json:"input,omitempty"`
	Credentials map[string]string `json:"credentials,omitempty"`
	// IdempotencyKey is the engine's key for a write; it is also put in
	// the manifest's idempotency field, as the engine does. Derived from
	// the case name when a write with an idempotency block leaves it out.
	IdempotencyKey string `json:"idempotency_key,omitempty"`
	// Exchanges are the requests the call must make, in order: fixture
	// names (testdata/fixtures/<name>.json) or inline exchanges.
	Exchanges []ExchangeRef `json:"exchanges"`
	Expect    Expect        `json:"expect"`
}

// Expect is a case's outcome: an output (every field given must match;
// others may be present) or an error kind.
type Expect struct {
	Output any    `json:"output,omitempty"`
	Error  string `json:"error,omitempty"`
}

// ExchangeRef is a fixture name or an inline exchange.
type ExchangeRef struct {
	Fixture string
	Inline  *Exchange
}

func (r *ExchangeRef) UnmarshalJSON(b []byte) error {
	var s string
	if json.Unmarshal(b, &s) == nil {
		r.Fixture = s
		return nil
	}
	var ex Exchange
	if err := json.Unmarshal(b, &ex); err != nil {
		return err
	}
	r.Inline = &ex
	return nil
}

func (r ExchangeRef) MarshalJSON() ([]byte, error) {
	if r.Inline != nil {
		return json.Marshal(r.Inline)
	}
	return json.Marshal(r.Fixture)
}

// Suite is a connector's cases with the fixtures they name.
type Suite struct {
	Cases    []Case              `json:"cases"`
	Fixtures map[string]Exchange `json:"fixtures,omitempty"`
}

// Error kinds a case may expect.
var ErrorKinds = []string{"retryable", "fatal", "unknown_outcome", "not_sent", "indeterminate", "not_found"}

// LoadDir reads a suite from a testdata directory: cases from
// conformance/*.json and fixtures from fixtures/*.json. A case's name is
// its file name unless it sets one.
func LoadDir(dir string) (*Suite, error) {
	s := &Suite{Fixtures: map[string]Exchange{}}
	fixtures, err := filepath.Glob(filepath.Join(dir, "fixtures", "*.json"))
	if err != nil {
		return nil, err
	}
	for _, f := range fixtures {
		raw, err := os.ReadFile(f) //nolint:gosec // files in the author's own testdata
		if err != nil {
			return nil, err
		}
		var ex Exchange
		if err := json.Unmarshal(raw, &ex); err != nil {
			return nil, fmt.Errorf("%s: %w", f, err)
		}
		ex.Name = strings.TrimSuffix(filepath.Base(f), ".json")
		s.Fixtures[ex.Name] = ex
	}
	cases, err := filepath.Glob(filepath.Join(dir, "conformance", "*.json"))
	if err != nil {
		return nil, err
	}
	for _, f := range cases {
		raw, err := os.ReadFile(f) //nolint:gosec // files in the author's own testdata
		if err != nil {
			return nil, err
		}
		var c Case
		if err := json.Unmarshal(raw, &c); err != nil {
			return nil, fmt.Errorf("%s: %w", f, err)
		}
		if c.Name == "" {
			c.Name = strings.TrimSuffix(filepath.Base(f), ".json")
		}
		s.Cases = append(s.Cases, c)
	}
	if len(s.Cases) == 0 {
		return nil, fmt.Errorf("%s: no conformance cases (conformance/*.json)", dir)
	}
	return s, s.Check()
}

// Check reports a case that names an unknown fixture or error kind, or
// expects neither an output nor an error.
func (s *Suite) Check() error {
	var probs []string
	seen := map[string]bool{}
	for _, c := range s.Cases {
		if seen[c.Name] {
			probs = append(probs, fmt.Sprintf("case %q appears twice", c.Name))
		}
		seen[c.Name] = true
		if c.Action == "" {
			probs = append(probs, fmt.Sprintf("case %q: no action", c.Name))
		}
		for _, r := range c.Exchanges {
			if r.Inline == nil {
				if _, ok := s.Fixtures[r.Fixture]; !ok {
					probs = append(probs, fmt.Sprintf("case %q: no fixture %q", c.Name, r.Fixture))
				}
			}
		}
		switch {
		case c.Expect.Error != "" && c.Expect.Output != nil:
			probs = append(probs, fmt.Sprintf("case %q: expects both an output and an error", c.Name))
		case c.Expect.Error != "" && !contains(ErrorKinds, c.Expect.Error):
			probs = append(probs, fmt.Sprintf("case %q: unknown error kind %q (one of %s)", c.Name, c.Expect.Error, strings.Join(ErrorKinds, ", ")))
		case c.Expect.Error == "" && c.Expect.Output == nil:
			probs = append(probs, fmt.Sprintf("case %q: expects neither an output nor an error", c.Name))
		}
	}
	if len(probs) > 0 {
		return errors.New(strings.Join(probs, "; "))
	}
	return nil
}

// Inline returns the suite with every exchange inline and no fixtures:
// self-contained, as a package carries it.
func (s *Suite) Inline() *Suite {
	out := &Suite{Cases: make([]Case, len(s.Cases))}
	for i, c := range s.Cases {
		c.Exchanges = append([]ExchangeRef(nil), c.Exchanges...)
		for j, r := range c.Exchanges {
			if r.Inline == nil {
				ex := s.Fixtures[r.Fixture]
				ex.Name = r.Fixture
				c.Exchanges[j] = ExchangeRef{Inline: &ex}
			}
		}
		out.Cases[i] = c
	}
	return out
}

func (s *Suite) exchanges(c Case) []Exchange {
	out := make([]Exchange, 0, len(c.Exchanges))
	for i, r := range c.Exchanges {
		ex := s.Fixtures[r.Fixture]
		if r.Inline != nil {
			ex = *r.Inline
		}
		if ex.Name == "" {
			ex.Name = r.Fixture
		}
		if ex.Name == "" {
			ex.Name = fmt.Sprintf("exchange %d", i+1)
		}
		out = append(out, ex)
	}
	return out
}

// Result is one case's outcome.
type Result struct {
	Case     string          `json:"case"`
	Action   string          `json:"action"`
	Pass     bool            `json:"pass"`
	Problems []string        `json:"problems,omitempty"`
	Drift    []drift.Finding `json:"drift,omitempty"`
	Methods  []string        `json:"methods,omitempty"` // of the requests it made
	KeySent  bool            `json:"key_sent,omitempty"`
}

// Report is a suite's outcome against a connector.
type Report struct {
	Results []Result `json:"results"`
	// Uncovered are actions no case calls.
	Uncovered []string `json:"uncovered,omitempty"`
	// KeyNotSent are idempotent writes whose key reached the provider in
	// no passing case: the class claims a deduplication the module may not
	// do.
	KeyNotSent []string `json:"key_not_sent,omitempty"`
	// ReadWrites are read actions that sent a method other than GET or
	// HEAD: often fine (a search by POST), always worth a reviewer's look.
	ReadWrites []string `json:"read_writes,omitempty"`
}

// Passed reports whether every case passed.
func (r Report) Passed() bool {
	for _, res := range r.Results {
		if !res.Pass {
			return false
		}
	}
	return len(r.Results) > 0
}

// Failed counts failing cases.
func (r Report) Failed() int {
	n := 0
	for _, res := range r.Results {
		if !res.Pass {
			n++
		}
	}
	return n
}

// Options tune a run.
type Options struct {
	// Timeout bounds one case; default 10s.
	Timeout time.Duration
}

// Run plays every case against c. The connector's base URL is pointed at
// a replay server for each case and restored afterwards, so c must not be
// in use elsewhere while it runs.
func Run(ctx context.Context, c *connector.Connector, s *Suite, opt Options) Report {
	if opt.Timeout <= 0 {
		opt.Timeout = 10 * time.Second
	}
	var rep Report
	covered := map[string]bool{}
	keySent := map[string]bool{}
	readWrites := map[string]bool{}
	base, egressHosts := c.Manifest.BaseURL, c.Manifest.Egress
	defer func() { c.Manifest.BaseURL, c.Manifest.Egress = base, egressHosts }()
	for _, cs := range s.Cases {
		res := runCase(ctx, c, s, cs, opt)
		covered[cs.Action] = true
		spec := c.Manifest.Actions[cs.Action]
		if res.Pass && res.KeySent {
			keySent[cs.Action] = true
		}
		if spec.Class == effects.Read {
			for _, m := range res.Methods {
				if m != http.MethodGet && m != http.MethodHead {
					readWrites[cs.Action] = true
				}
			}
		}
		rep.Results = append(rep.Results, res)
	}
	names := make([]string, 0, len(c.Manifest.Actions))
	for n := range c.Manifest.Actions {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		spec := c.Manifest.Actions[n]
		if !covered[n] {
			rep.Uncovered = append(rep.Uncovered, n)
			continue
		}
		if spec.Class.IsWrite() && spec.Idempotency != nil && !keySent[n] {
			rep.KeyNotSent = append(rep.KeyNotSent, n)
		}
		if readWrites[n] {
			rep.ReadWrites = append(rep.ReadWrites, n)
		}
	}
	return rep
}

func runCase(ctx context.Context, c *connector.Connector, s *Suite, cs Case, opt Options) Result {
	res := Result{Case: cs.Name, Action: cs.Action}
	fail := func(format string, args ...any) { res.Problems = append(res.Problems, fmt.Sprintf(format, args...)) }
	spec, ok := c.Manifest.Actions[cs.Action]
	act := c.Actions[cs.Action]
	if !ok || act == nil {
		fail("the connector has no action %q", cs.Action)
		return res
	}
	input := map[string]any{}
	for k, v := range cs.Input {
		input[k] = v
	}
	key := cs.IdempotencyKey
	if spec.Class.IsWrite() && spec.Idempotency != nil {
		if key == "" {
			var err error
			if key, err = spec.Idempotency.Key(effects.KeyInput{TenantID: "00000000-0000-0000-0000-000000000000", Seed: "conformance", StepID: cs.Name}); err != nil {
				fail("idempotency key: %v", err)
				return res
			}
		}
		input[spec.Idempotency.Field] = key
	}
	rp := &replay{queue: s.exchanges(cs), key: key}
	srv := httptest.NewServer(rp)
	defer srv.Close()
	c.Manifest.OverrideBaseURL(srv.URL)
	u, _ := url.Parse(srv.URL)
	client := &http.Client{Transport: &onlyHost{host: u.Host, rt: &http.Transport{Proxy: nil, DialContext: (&net.Dialer{Timeout: 5 * time.Second}).DialContext}},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	cctx, cancel := context.WithTimeout(ctx, opt.Timeout)
	defer cancel()
	out, err := act.Execute(cctx, connector.Request{Input: input, Credentials: cs.Credentials, IdempotencyKey: key, Attempt: 1, HTTP: client})
	srv.Close()
	rp.mu.Lock()
	res.Problems = append(res.Problems, rp.problems...)
	if len(rp.queue) > 0 {
		fail("%d expected request(s) never arrived, next: %s", len(rp.queue), rp.queue[0].Name)
	}
	res.Methods, res.KeySent = rp.methods, rp.keySent
	rp.mu.Unlock()
	switch {
	case cs.Expect.Error != "":
		if err == nil {
			fail("expected a %s error, got output %s", cs.Expect.Error, compact(out.Output))
		} else if got := Kind(err); got != cs.Expect.Error {
			fail("expected a %s error, got %s: %v", cs.Expect.Error, got, err)
		}
	case err != nil:
		fail("expected an output, got %s: %v", Kind(err), err)
	default:
		if !subset(norm(cs.Expect.Output), norm(out.Output)) {
			fail("output %s does not match the expected %s", compact(out.Output), compact(cs.Expect.Output))
		}
		if res.Drift = drift.Check(c.Manifest, cs.Action, out.Output); len(res.Drift) > 0 {
			for _, f := range res.Drift {
				fail("output departs from the declared schema at %s: expected %s, got %s (%s)", f.Path, f.Expected, f.Observed, f.Kind)
			}
		}
	}
	res.Pass = len(res.Problems) == 0
	return res
}

// Kind names the class of a handler's error, as a case expects it.
func Kind(err error) string {
	switch {
	case errors.Is(err, connector.ErrNotFound):
		return "not_found"
	case errors.Is(err, effects.ErrIndeterminate):
		return "indeterminate"
	case errors.Is(err, effects.ErrNotSent):
		return "not_sent"
	case errors.Is(err, effects.ErrRetryable):
		return "retryable"
	case errors.Is(err, effects.ErrFatal):
		return "fatal"
	}
	return "unknown_outcome"
}

// onlyHost refuses every request but those to the replay server: a module
// that calls a host other than its base URL fails its case.
type onlyHost struct {
	host string
	rt   http.RoundTripper
}

func (o *onlyHost) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.URL.Host != o.host {
		return nil, fmt.Errorf("%w: %s is not the replayed provider: requests must be built from base_url", egress.ErrDenied, r.URL.Host)
	}
	return o.rt.RoundTrip(r)
}

// replay serves exchanges in order and records every mismatch.
type replay struct {
	mu       sync.Mutex
	queue    []Exchange
	key      string
	problems []string
	methods  []string
	keySent  bool
}

func (rp *replay) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	raw, _ := io.ReadAll(io.LimitReader(r.Body, 8<<20))
	rp.mu.Lock()
	rp.methods = append(rp.methods, r.Method)
	if rp.key != "" && (strings.Contains(r.URL.String(), rp.key) || bytes.Contains(raw, []byte(rp.key)) || headerHas(r.Header, rp.key)) {
		rp.keySent = true
	}
	if len(rp.queue) == 0 {
		rp.problems = append(rp.problems, fmt.Sprintf("unexpected request %s %s", r.Method, r.URL.Path))
		rp.mu.Unlock()
		http.Error(w, "unexpected", http.StatusTeapot)
		return
	}
	ex := rp.queue[0]
	rp.queue = rp.queue[1:]
	fail := func(format string, args ...any) {
		rp.problems = append(rp.problems, ex.Name+": "+fmt.Sprintf(format, args...))
	}
	want := ex.Request
	if !strings.EqualFold(r.Method, want.Method) || r.URL.Path != want.Path {
		fail("got %s %s, want %s %s", r.Method, r.URL.Path, want.Method, want.Path)
	}
	for k, v := range want.Query {
		if got := r.URL.Query().Get(k); got != v {
			fail("query %s = %q, want %q", k, got, v)
		}
	}
	for k, v := range want.Headers {
		if got := r.Header.Get(k); got != v {
			fail("header %s = %q, want %q", k, got, v)
		}
	}
	if want.Body != nil {
		var got any
		if err := json.Unmarshal(raw, &got); err != nil || !reflect.DeepEqual(norm(got), norm(want.Body)) {
			fail("body %s, want %s", strings.TrimSpace(string(raw)), compact(want.Body))
		}
	}
	rp.mu.Unlock()
	for k, v := range ex.Response.Headers {
		w.Header().Set(k, v)
	}
	if w.Header().Get("Content-Type") == "" {
		w.Header().Set("Content-Type", "application/json")
	}
	status := ex.Response.Status
	if status == 0 {
		status = http.StatusOK
	}
	w.WriteHeader(status)
	if s, ok := ex.Response.Body.(string); ok && !strings.Contains(w.Header().Get("Content-Type"), "json") {
		_, _ = io.WriteString(w, s)
		return
	}
	if ex.Response.Body != nil {
		_ = json.NewEncoder(w).Encode(ex.Response.Body)
	}
}

func headerHas(h http.Header, s string) bool {
	for _, vs := range h {
		for _, v := range vs {
			if strings.Contains(v, s) {
				return true
			}
		}
	}
	return false
}

// subset reports whether every field of want is in got with the same
// value; lists must match element for element.
func subset(want, got any) bool {
	switch w := want.(type) {
	case map[string]any:
		g, ok := got.(map[string]any)
		if !ok {
			return false
		}
		for k, v := range w {
			if !subset(v, g[k]) {
				return false
			}
		}
		return true
	case []any:
		g, ok := got.([]any)
		if !ok || len(g) != len(w) {
			return false
		}
		for i := range w {
			if !subset(w[i], g[i]) {
				return false
			}
		}
		return true
	}
	return reflect.DeepEqual(want, got)
}

func norm(v any) any {
	raw, _ := json.Marshal(v)
	var out any
	_ = json.Unmarshal(raw, &out)
	return out
}

func compact(v any) string {
	raw, _ := json.Marshal(v)
	if len(raw) > 300 {
		return string(raw[:300]) + "..."
	}
	return string(raw)
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}
