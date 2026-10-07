// Package connector validates connector manifests against the connector/v1
// contract: the JSON Schema plus the registration rules in
// docs/contracts/connector-v1.md.
package connector

import (
	"encoding/json"
	"fmt"
	"mime"
	"net/url"
	"sort"
	"strings"

	"sigs.k8s.io/yaml"

	"github.com/israel-duff/taskiem/engine/effects"
	"github.com/israel-duff/taskiem/engine/internal/schemacheck"
	"github.com/israel-duff/taskiem/schemas"
)

var schema = schemacheck.New("https://schemas.taskiem.dev/connector/v1.json", schemas.ConnectorV1)

// Manifest is the subset of a connector/v1 manifest the engine reads.
type Manifest struct {
	ID       string `json:"id"`
	Version  string `json:"version"`
	Name     string `json:"name"`
	Category string `json:"category"`
	// Description is the manifest's own summary, shown to builders.
	Description string                 `json:"description"`
	Auth        Auth                   `json:"auth"`
	BaseURL     string                 `json:"base_url"`
	Egress      []string               `json:"egress_hosts"`
	Actions     map[string]ActionSpec  `json:"actions"`
	Triggers    map[string]TriggerSpec `json:"triggers"`
}

type Auth struct {
	Type   string      `json:"type"`
	Fields []AuthField `json:"fields"`
	Test   *struct {
		Action string `json:"action"`
	} `json:"test"`
}

// AuthField is one credential a connection supplies.
type AuthField struct {
	Key      string `json:"key"`
	Label    string `json:"label"`
	Secret   bool   `json:"secret"`
	Required *bool  `json:"required"`
}

type ActionSpec struct {
	Title       string          `json:"title"`
	Description string          `json:"description"`
	Class       effects.Class   `json:"class"`
	Idempotency *effects.Spec   `json:"idempotency"`
	Reconcile   string          `json:"reconcile"`
	Compensate  *string         `json:"compensate"`
	Input       json.RawMessage `json:"input"`
	Output      json.RawMessage `json:"output"`
	PII         []PIIField      `json:"pii"`
}

// PIIField accepts either "field" or {field, category}.
type PIIField struct {
	Field    string `json:"field"`
	Category string `json:"category"`
}

func (p *PIIField) UnmarshalJSON(b []byte) error {
	var s string
	if json.Unmarshal(b, &s) == nil {
		p.Field = s
		return nil
	}
	type plain PIIField
	return json.Unmarshal(b, (*plain)(p))
}

// Parse validates a YAML or JSON manifest and returns it, or every problem found.
func Parse(src []byte) (*Manifest, []string) {
	j, err := yaml.YAMLToJSON(src)
	if err != nil {
		return nil, []string{"yaml: " + err.Error()}
	}
	if msgs := schema.Validate(j); len(msgs) > 0 {
		for i := range msgs {
			msgs[i] = "schema: " + msgs[i]
		}
		return nil, msgs
	}
	var m Manifest
	if err := json.Unmarshal(j, &m); err != nil {
		return nil, []string{err.Error()}
	}
	if probs := m.check(); len(probs) > 0 {
		return nil, probs
	}
	return &m, nil
}

func (m *Manifest) check() []string {
	var out []string
	add := func(format string, args ...any) { out = append(out, fmt.Sprintf(format, args...)) }

	names := make([]string, 0, len(m.Actions))
	for n := range m.Actions {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, name := range names {
		a := m.Actions[name]
		p := "/actions/" + name
		if a.Class == effects.Read && a.Compensate != nil {
			add("%s: read actions cannot declare compensate", p)
		}
		if a.Reconcile != "" {
			if r, ok := m.Actions[a.Reconcile]; !ok {
				add("%s/reconcile: unknown action %q", p, a.Reconcile)
			} else if r.Class != effects.Read {
				add("%s/reconcile: %q must be a read action, is %s", p, a.Reconcile, r.Class)
			}
		}
		if a.Compensate != nil {
			if c, ok := m.Actions[*a.Compensate]; !ok {
				add("%s/compensate: unknown action %q", p, *a.Compensate)
			} else if !c.Class.IsWrite() {
				add("%s/compensate: %q must be a write action", p, *a.Compensate)
			}
		}
		if a.Idempotency != nil {
			if err := a.Idempotency.Validate(); err != nil {
				add("%s/idempotency: %v", p, strings.TrimPrefix(err.Error(), "effects: "))
			}
		}
		for _, f := range a.PII {
			if segs, ok := OutputPIIPath(f.Field); ok {
				if !schemaHasPath(a.Output, segs) {
					add("%s/pii: %q is not in the output schema", p, f.Field)
				}
			} else if !schemaHasPath(a.Input, InputPIIPath(f.Field)) {
				add("%s/pii: %q is not in the input schema", p, f.Field)
			}
		}
	}
	tnames := make([]string, 0, len(m.Triggers))
	for n := range m.Triggers {
		tnames = append(tnames, n)
	}
	sort.Strings(tnames)
	for _, name := range tnames {
		// An ack is served from the hooks origin: never as a page.
		if a := m.Triggers[name].Ack; a != nil && a.ContentType != "" {
			if mt, _, err := mime.ParseMediaType(a.ContentType); err != nil || (mt != "text/plain" && mt != "application/json") {
				add("/triggers/%s/ack/content_type: must be text/plain or application/json, not %q", name, a.ContentType)
			}
		}
	}
	if m.Auth.Test != nil {
		if a, ok := m.Actions[m.Auth.Test.Action]; !ok {
			add("/auth/test: unknown action %q", m.Auth.Test.Action)
		} else if a.Class != effects.Read {
			add("/auth/test: %q must be a read action", m.Auth.Test.Action)
		}
	}
	return out
}

// InputPIIPath reads a pii field naming a place in the action's input:
// "<key>[.<key>|.*]...", optionally prefixed "input.", "*" standing for
// every array element (transfers.*.account_name).
func InputPIIPath(field string) []string {
	return strings.Split(strings.TrimPrefix(field, "input."), ".")
}

// OutputPIIPath reads a pii field naming a place in the action's output:
// "output.<key>[.<key>|.*]...", "*" standing for every array element.
func OutputPIIPath(field string) ([]string, bool) {
	rest, ok := strings.CutPrefix(field, "output.")
	if !ok || rest == "" {
		return nil, false
	}
	return strings.Split(rest, "."), true
}

// schemaHasPath reports whether a JSON Schema describes the path: object
// keys through properties, "*" through items. A schema that leaves an
// object open (no properties) accepts any key below it.
func schemaHasPath(schema json.RawMessage, segs []string) bool {
	var s struct {
		Properties map[string]json.RawMessage `json:"properties"`
		Items      json.RawMessage            `json:"items"`
		Ref        string                     `json:"$ref"`
	}
	if len(segs) == 0 {
		return true
	}
	if json.Unmarshal(schema, &s) != nil {
		return false
	}
	if s.Ref != "" {
		return true // a reference to another action's output: trust it
	}
	if segs[0] == "*" {
		return len(s.Items) > 0 && schemaHasPath(s.Items, segs[1:])
	}
	if s.Properties == nil {
		return len(s.Items) == 0 // an open object
	}
	sub, ok := s.Properties[segs[0]]
	return ok && schemaHasPath(sub, segs[1:])
}

// MustParse parses a manifest or panics; for connectors compiled into the binary.
func MustParse(src []byte) *Manifest {
	m, probs := Parse(src)
	if len(probs) > 0 {
		panic("connector manifest: " + strings.Join(probs, "; "))
	}
	return m
}

// Hosts are the hosts this connector may reach: egress_hosts, or else the
// base_url host.
func (m *Manifest) Hosts() []string {
	if len(m.Egress) > 0 {
		return m.Egress
	}
	if u, err := url.Parse(m.BaseURL); err == nil && u.Hostname() != "" {
		return []string{u.Hostname()}
	}
	return nil
}

// OverrideBaseURL points the connector at another endpoint (a provider
// sandbox, a test server) and makes that endpoint's host the only one it may
// reach. It is platform configuration, never tenant input.
func (m *Manifest) OverrideBaseURL(base string) {
	if base == "" {
		return
	}
	m.BaseURL = base
	if u, err := url.Parse(base); err == nil && u.Hostname() != "" {
		m.Egress = []string{u.Hostname()}
	}
}
