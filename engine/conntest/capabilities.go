package conntest

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/israel-duff/taskiem/engine/capability"
	"github.com/israel-duff/taskiem/engine/connector"
	"github.com/israel-duff/taskiem/engine/effects"
	"github.com/israel-duff/taskiem/engine/expr"
)

// CapabilityCase is one capability conformance case (connector/v1 rule
// 16): either a capability call, replayed through the rail's mapping and
// actions with the exchanges given, whose output must match the
// capability's schema and Expect; or a recorded webhook delivery to
// Trigger, which must be the normalised Event with the fields in Expect.
type CapabilityCase struct {
	Name string `json:"name"`

	Capability  string            `json:"capability,omitempty"`
	Input       map[string]any    `json:"input,omitempty"`
	Credentials map[string]string `json:"credentials,omitempty"`
	// Keys are the engine's idempotency keys by step id (the action's
	// name for a single action), where the fixtures expect given ones.
	Keys      map[string]string `json:"keys,omitempty"`
	Exchanges []ExchangeRef     `json:"exchanges,omitempty"`

	Trigger string            `json:"trigger,omitempty"`
	Body    any               `json:"body,omitempty"`
	Headers map[string]string `json:"headers,omitempty"`
	// Event is the normalised event the delivery is, or "" for none.
	Event string `json:"event,omitempty"`

	// Expect is the capability output or event fields: every field given
	// must match; others may be present. Error, instead, is the kind of
	// error the call must end with.
	Expect map[string]any `json:"expect,omitempty"`
	Error  string         `json:"error,omitempty"`
}

// CapabilitySuite is a rail's capability cases with the fixtures they name.
type CapabilitySuite struct {
	Cases    []CapabilityCase
	Fixtures map[string]Exchange
}

// LoadCapabilities reads capability cases from testdata's
// capabilities/*.json and fixtures from fixtures/*.json.
func LoadCapabilities(dir string) (*CapabilitySuite, error) {
	base, err := loadFixtures(dir)
	if err != nil {
		return nil, err
	}
	s := &CapabilitySuite{Fixtures: base}
	files, err := filepath.Glob(filepath.Join(dir, "capabilities", "*.json"))
	if err != nil {
		return nil, err
	}
	for _, f := range files {
		raw, err := os.ReadFile(f) //nolint:gosec // files in the author's own testdata
		if err != nil {
			return nil, err
		}
		var c CapabilityCase
		if err := json.Unmarshal(raw, &c); err != nil {
			return nil, fmt.Errorf("%s: %w", f, err)
		}
		if c.Name == "" {
			c.Name = strings.TrimSuffix(filepath.Base(f), ".json")
		}
		s.Cases = append(s.Cases, c)
	}
	if len(s.Cases) == 0 {
		return nil, fmt.Errorf("%s: no capability cases (capabilities/*.json)", dir)
	}
	return s, s.check()
}

func loadFixtures(dir string) (map[string]Exchange, error) {
	out := map[string]Exchange{}
	files, err := filepath.Glob(filepath.Join(dir, "fixtures", "*.json"))
	if err != nil {
		return nil, err
	}
	for _, f := range files {
		raw, err := os.ReadFile(f) //nolint:gosec // files in the author's own testdata
		if err != nil {
			return nil, err
		}
		var ex Exchange
		if err := json.Unmarshal(raw, &ex); err != nil {
			return nil, fmt.Errorf("%s: %w", f, err)
		}
		ex.Name = strings.TrimSuffix(filepath.Base(f), ".json")
		out[ex.Name] = ex
	}
	return out, nil
}

func (s *CapabilitySuite) check() error {
	var probs []string
	seen := map[string]bool{}
	for _, c := range s.Cases {
		if seen[c.Name] {
			probs = append(probs, fmt.Sprintf("case %q appears twice", c.Name))
		}
		seen[c.Name] = true
		switch {
		case (c.Capability == "") == (c.Trigger == ""):
			probs = append(probs, fmt.Sprintf("case %q: name a capability or a trigger", c.Name))
		case c.Error != "" && !contains(ErrorKinds, c.Error):
			probs = append(probs, fmt.Sprintf("case %q: unknown error kind %q", c.Name, c.Error))
		case c.Capability != "" && c.Error == "" && c.Expect == nil:
			probs = append(probs, fmt.Sprintf("case %q: expects neither an output nor an error", c.Name))
		}
		for _, r := range c.Exchanges {
			if r.Inline == nil {
				if _, ok := s.Fixtures[r.Fixture]; !ok {
					probs = append(probs, fmt.Sprintf("case %q: no fixture %q", c.Name, r.Fixture))
				}
			}
		}
	}
	if len(probs) > 0 {
		return errors.New(strings.Join(probs, "; "))
	}
	return nil
}

// CapabilityReport is a capability suite's outcome against a rail.
type CapabilityReport struct {
	Results []Result `json:"results"`
	// Uncovered are declared capabilities and events no passing case
	// shows working.
	Uncovered []string `json:"uncovered,omitempty"`
}

// Passed reports whether every case passed and every declared capability
// and event is covered.
func (r CapabilityReport) Passed() bool {
	for _, res := range r.Results {
		if !res.Pass {
			return false
		}
	}
	return len(r.Results) > 0 && len(r.Uncovered) == 0
}

// RunCapabilities plays every case against c, as Run does.
func RunCapabilities(ctx context.Context, c *connector.Connector, s *CapabilitySuite, opt Options) CapabilityReport {
	if opt.Timeout <= 0 {
		opt.Timeout = 10 * time.Second
	}
	var rep CapabilityReport
	covered := map[string]bool{}
	base, egressHosts := c.Manifest.BaseURL, c.Manifest.Egress
	defer func() { c.Manifest.BaseURL, c.Manifest.Egress = base, egressHosts }()
	for _, cs := range s.Cases {
		var res Result
		if cs.Trigger != "" {
			res = runEvent(c.Manifest, cs)
			if res.Pass && cs.Event != "" {
				covered["event "+cs.Event] = true
			}
		} else {
			res = runCapability(ctx, c, s, cs, opt)
			if res.Pass && cs.Error == "" {
				covered[cs.Capability] = true
			}
		}
		rep.Results = append(rep.Results, res)
	}
	var want []string
	for n := range c.Manifest.Capabilities {
		want = append(want, n)
	}
	for n := range c.Manifest.Events {
		want = append(want, "event "+n)
	}
	sort.Strings(want)
	for _, n := range want {
		if !covered[n] {
			rep.Uncovered = append(rep.Uncovered, n)
		}
	}
	return rep
}

func runCapability(ctx context.Context, c *connector.Connector, s *CapabilitySuite, cs CapabilityCase, opt Options) Result {
	res := Result{Case: cs.Name, Action: cs.Capability}
	fail := func(format string, args ...any) { res.Problems = append(res.Problems, fmt.Sprintf(format, args...)) }
	m := c.Manifest
	spec, known := capability.Get(cs.Capability)
	cm, declared := m.Capabilities[cs.Capability]
	if !known || !declared {
		fail("%s does not declare the capability %q", m.ID, cs.Capability)
		return res
	}
	exchanges := (&Suite{Fixtures: s.Fixtures}).exchanges(Case{Exchanges: cs.Exchanges})
	rp := &replay{queue: exchanges}
	srv := httptest.NewServer(rp)
	defer srv.Close()
	m.OverrideBaseURL(srv.URL)
	u, _ := url.Parse(srv.URL)
	client := &http.Client{Transport: &onlyHost{host: u.Host, rt: &http.Transport{Proxy: nil, DialContext: (&net.Dialer{Timeout: 5 * time.Second}).DialContext}},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	if opt.Connect != nil {
		c = opt.Connect(srv.URL)
	}
	cctx, cancel := context.WithTimeout(ctx, opt.Timeout)
	defer cancel()

	out, err := callCapability(cctx, c, cs, cm, client)
	srv.Close()
	rp.mu.Lock()
	res.Problems = append(res.Problems, rp.problems...)
	if len(rp.queue) > 0 {
		fail("%d expected request(s) never arrived, next: %s", len(rp.queue), rp.queue[0].Name)
	}
	res.Methods = rp.methods
	rp.mu.Unlock()
	switch {
	case cs.Error != "":
		if err == nil {
			fail("expected a %s error, got output %s", cs.Error, compact(out))
		} else if got := Kind(err); got != cs.Error {
			fail("expected a %s error, got %s: %v", cs.Error, got, err)
		}
	case err != nil:
		fail("expected an output, got %s: %v", Kind(err), err)
	default:
		for _, msg := range spec.CheckOutput(out) {
			fail("output %s is not a %s output: %s", compact(out), cs.Capability, msg)
		}
		if !subset(norm(cs.Expect), norm(out)) {
			fail("output %s does not match the expected %s", compact(out), compact(cs.Expect))
		}
	}
	res.Pass = len(res.Problems) == 0
	return res
}

// callCapability runs a capability as expansion will (R2): the input
// checked and defaulted, each action called with its mapped input and its
// own idempotency key, and the last output mapped back.
func callCapability(ctx context.Context, c *connector.Connector, cs CapabilityCase, cm connector.CapabilityMap, client *http.Client) (map[string]any, error) {
	m := c.Manifest
	in, err := m.CapabilityInput(cs.Capability, cs.Input)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", err, effects.ErrFatal)
	}
	steps := map[string]any{}
	var last map[string]any
	for i, st := range cm.Plan() {
		act := c.Actions[st.Action]
		if act == nil {
			return nil, fmt.Errorf("no handler for %s: %w", st.Action, effects.ErrFatal)
		}
		input, err := m.CapabilityStepInput(cs.Capability, i, in, steps)
		if err != nil {
			return nil, fmt.Errorf("%s input: %w: %w", st.Action, err, effects.ErrFatal)
		}
		id := st.ID
		if id == "" {
			id = st.Action
		}
		key := cs.Keys[id]
		if spec := m.Actions[st.Action]; spec.Class.IsWrite() && spec.Idempotency != nil {
			if key == "" {
				if key, err = spec.Idempotency.Key(effects.KeyInput{TenantID: "00000000-0000-0000-0000-000000000000", Seed: "conformance", StepID: cs.Name + ":" + id}); err != nil {
					return nil, err
				}
			}
			input[spec.Idempotency.Field] = key
		}
		r, err := act.Execute(ctx, connector.Request{Input: input, Credentials: cs.Credentials, IdempotencyKey: key, Attempt: 1, HTTP: client})
		if err != nil {
			return nil, err
		}
		o, err := asObject(r.Output)
		if err != nil {
			return nil, fmt.Errorf("%s output: %w: %w", st.Action, err, effects.ErrFatal)
		}
		steps[id] = map[string]any{"output": o}
		last = o
	}
	out, err := m.CapabilityOutput(cs.Capability, in, last, steps)
	if err != nil {
		return nil, fmt.Errorf("output: %w: %w", err, effects.ErrFatal)
	}
	return out, nil
}

// asObject turns a handler's output into plain JSON values, as the engine
// stores it, so mappings see int64 amounts and maps, never Go structs.
func asObject(v any) (map[string]any, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	d, err := expr.DecodeJSON(raw)
	if err != nil {
		return nil, err
	}
	o, ok := d.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("not an object: %s", compact(v))
	}
	return o, nil
}

// runEvent checks a recorded delivery: it is the expected normalised
// event (or none), its fields are the event's, and the field a workflow
// waits on is the trigger's own correlation, so a wait for the payout_id a
// pay capability answered meets this delivery.
func runEvent(m *connector.Manifest, cs CapabilityCase) Result {
	res := Result{Case: cs.Name, Action: "trigger " + cs.Trigger}
	fail := func(format string, args ...any) { res.Problems = append(res.Problems, fmt.Sprintf(format, args...)) }
	t, ok := m.Triggers[cs.Trigger]
	if !ok {
		fail("%s has no trigger %q", m.ID, cs.Trigger)
		return res
	}
	raw, _ := json.Marshal(cs.Body)
	body, err := expr.DecodeJSON(raw)
	if err != nil {
		fail("body: %v", err)
		return res
	}
	headers := map[string]any{}
	for k, v := range cs.Headers {
		headers[strings.ToLower(k)] = v
	}
	act := map[string]any{"body": body, "headers": headers, "query": map[string]any{}, "item": nil}
	name, fields, err := m.EventOf(cs.Trigger, act)
	switch {
	case errors.Is(err, connector.ErrNoEvent):
		if cs.Event != "" {
			fail("the delivery is no event, want %s", cs.Event)
		}
	case err != nil:
		fail("%v", err)
	case name != cs.Event:
		fail("the delivery is %s, want %q", name, cs.Event)
	default:
		ev, _ := capability.GetEvent(name)
		for _, msg := range ev.CheckFields(fields) {
			fail("fields %s are not %s's: %s", compact(fields), name, msg)
		}
		if !subset(norm(cs.Expect), norm(fields)) {
			fail("fields %s do not match the expected %s", compact(fields), compact(cs.Expect))
		}
		if t.Correlation != "" {
			corr, err := triggerExprs.Eval(t.Correlation, act)
			if err != nil {
				fail("the trigger's correlation: %v", err)
			} else if fmt.Sprint(corr) != fmt.Sprint(fields[ev.Correlation]) {
				fail("%s is %v, but the trigger correlates the delivery on %v: a wait on it would never end", ev.Correlation, fields[ev.Correlation], corr)
			}
		}
	}
	res.Pass = len(res.Problems) == 0
	return res
}

var triggerExprs = expr.MustNewTriggerEngine("body", "headers", "query", "item")
