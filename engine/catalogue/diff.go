package catalogue

import (
	"encoding/json"
	"slices"
	"sort"
	"strings"

	"github.com/israel-duff/taskiem/engine/connector"
)

// Consent is what a tenant agrees to when it installs a catalogue
// connector: where its data may go and which actions change things at
// the provider, with their classes. An upgrade that widens it needs the
// tenant's consent again.
type Consent struct {
	Hosts  []string          `json:"hosts"`
	Writes map[string]string `json:"writes"` // action -> class
}

// ConsentFor is the consent a manifest asks for.
func ConsentFor(m *connector.Manifest) Consent {
	c := Consent{Hosts: append([]string(nil), m.Hosts()...), Writes: map[string]string{}}
	sort.Strings(c.Hosts)
	for name, a := range m.Actions {
		if a.Class.IsWrite() {
			c.Writes[name] = string(a.Class)
		}
	}
	return c
}

// Covers reports whether consent c already allows everything in o.
func (c Consent) Covers(o Consent) bool {
	for _, h := range o.Hosts {
		if !slices.Contains(c.Hosts, h) {
			return false
		}
	}
	for a, cls := range o.Writes {
		if c.Writes[a] != cls {
			return false
		}
	}
	return true
}

// ClassChange is an action whose class differs between two versions.
type ClassChange struct {
	Action string `json:"action"`
	From   string `json:"from"`
	To     string `json:"to"`
}

// Diff is what changes between two versions of a connector, as an
// installing tenant sees it before an upgrade.
type Diff struct {
	From           string        `json:"from"`
	To             string        `json:"to"`
	AddedHosts     []string      `json:"added_hosts,omitempty"`
	RemovedHosts   []string      `json:"removed_hosts,omitempty"`
	AddedActions   []string      `json:"added_actions,omitempty"`
	RemovedActions []string      `json:"removed_actions,omitempty"`
	ClassChanges   []ClassChange `json:"class_changes,omitempty"`
	AddedWrites    []string      `json:"added_writes,omitempty"`   // new actions that change things
	RemovedFields  []string      `json:"removed_fields,omitempty"` // action/input.field, action/output.field
	AddedPII       []string      `json:"added_pii,omitempty"`
	RemovedPII     []string      `json:"removed_pii,omitempty"`
	// NeedsConsent: new hosts, new writes or class changes; the tenant
	// must consent to the new version's hosts and writes.
	NeedsConsent bool `json:"needs_consent"`
}

// Compare diffs two manifests of the same connector.
func Compare(a, b *connector.Manifest) Diff {
	d := Diff{From: a.Version, To: b.Version}
	ha, hb := a.Hosts(), b.Hosts()
	for _, h := range hb {
		if !slices.Contains(ha, h) {
			d.AddedHosts = append(d.AddedHosts, h)
		}
	}
	for _, h := range ha {
		if !slices.Contains(hb, h) {
			d.RemovedHosts = append(d.RemovedHosts, h)
		}
	}
	for _, name := range sortedActions(b) {
		nb := b.Actions[name]
		na, ok := a.Actions[name]
		if !ok {
			d.AddedActions = append(d.AddedActions, name)
			if nb.Class.IsWrite() {
				d.AddedWrites = append(d.AddedWrites, name)
			}
			continue
		}
		if na.Class != nb.Class {
			d.ClassChanges = append(d.ClassChanges, ClassChange{Action: name, From: string(na.Class), To: string(nb.Class)})
		}
		for _, f := range fields(na.Input) {
			if !slices.Contains(fields(nb.Input), f) {
				d.RemovedFields = append(d.RemovedFields, name+"/input."+f)
			}
		}
		for _, f := range fields(na.Output) {
			if !slices.Contains(fields(nb.Output), f) {
				d.RemovedFields = append(d.RemovedFields, name+"/output."+f)
			}
		}
		pa, pb := piiOf(na), piiOf(nb)
		for _, f := range pb {
			if !slices.Contains(pa, f) {
				d.AddedPII = append(d.AddedPII, name+"/"+f)
			}
		}
		for _, f := range pa {
			if !slices.Contains(pb, f) {
				d.RemovedPII = append(d.RemovedPII, name+"/"+f)
			}
		}
	}
	for _, name := range sortedActions(a) {
		if _, ok := b.Actions[name]; !ok {
			d.RemovedActions = append(d.RemovedActions, name)
		}
	}
	d.NeedsConsent = !ConsentFor(a).Covers(ConsentFor(b))
	return d
}

func sortedActions(m *connector.Manifest) []string {
	out := make([]string, 0, len(m.Actions))
	for n := range m.Actions {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// fields are a schema's top-level properties.
func fields(schema json.RawMessage) []string {
	var s struct {
		Properties map[string]json.RawMessage `json:"properties"`
	}
	_ = json.Unmarshal(schema, &s)
	out := make([]string, 0, len(s.Properties))
	for k := range s.Properties {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func piiOf(a connector.ActionSpec) []string {
	out := make([]string, 0, len(a.PII))
	for _, p := range a.PII {
		out = append(out, normPII(p.Field))
	}
	sort.Strings(out)
	return out
}

// Summary describes a manifest for the catalogue and for reviewers.
type Summary struct {
	ID          string            `json:"id"`
	Version     string            `json:"version"`
	Ref         string            `json:"ref"`
	Name        string            `json:"name"`
	Description string            `json:"description"`
	Category    string            `json:"category"`
	Hosts       []string          `json:"hosts"`
	Actions     map[string]string `json:"actions"` // name -> class
	PII         []string          `json:"pii"`
	Triggers    []string          `json:"triggers"`
}

// Summarise describes a manifest.
func Summarise(m *connector.Manifest) Summary {
	major, _, _ := strings.Cut(m.Version, ".")
	s := Summary{ID: m.ID, Version: m.Version, Ref: m.ID + "@" + major, Name: m.Name, Description: m.Description, Category: m.Category,
		Hosts: append([]string{}, m.Hosts()...), Actions: map[string]string{}, PII: []string{}, Triggers: []string{}}
	for _, name := range sortedActions(m) {
		a := m.Actions[name]
		s.Actions[name] = string(a.Class)
		for _, p := range piiOf(a) {
			s.PII = append(s.PII, name+"/"+p)
		}
	}
	for t := range m.Triggers {
		s.Triggers = append(s.Triggers, t)
	}
	sort.Strings(s.Triggers)
	return s
}
