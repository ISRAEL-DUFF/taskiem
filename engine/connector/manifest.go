// Package connector validates connector manifests against the connector/v1
// contract: the JSON Schema plus the registration rules in
// docs/contracts/connector-v1.md.
package connector

import (
	"encoding/json"
	"fmt"
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
	ID       string             `json:"id"`
	Version  string             `json:"version"`
	Name     string             `json:"name"`
	Auth     Auth               `json:"auth"`
	Actions  map[string]Action  `json:"actions"`
	Triggers map[string]Trigger `json:"triggers"`
}

type Auth struct {
	Type string `json:"type"`
	Test *struct {
		Action string `json:"action"`
	} `json:"test"`
}

type Action struct {
	Title       string          `json:"title"`
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

type Trigger struct {
	Type        string   `json:"type"`
	Events      []string `json:"events"`
	Correlation string   `json:"correlation"`
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
		if len(a.PII) > 0 {
			props := inputProperties(a.Input)
			for _, f := range a.PII {
				if !props[f.Field] {
					add("%s/pii: %q is not an input property", p, f.Field)
				}
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

func inputProperties(schema json.RawMessage) map[string]bool {
	var s struct {
		Properties map[string]json.RawMessage `json:"properties"`
	}
	_ = json.Unmarshal(schema, &s)
	out := make(map[string]bool, len(s.Properties))
	for k := range s.Properties {
		out[k] = true
	}
	return out
}
