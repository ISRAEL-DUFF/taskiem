package wd

import (
	"strings"

	"github.com/israel-duff/taskiem/engine/pii"
	"github.com/israel-duff/taskiem/engine/ussd"
)

// checkUSSD applies the menu rules of a ussd trigger (docs/ussd.md):
// screens within the size limit, every screen reachable, patterns and
// conditions that compile, placeholders naming collected inputs, and
// inputs that fit the workflow's inputs schema.
func checkUSSD(d *Definition) []Problem {
	if d.Trigger.Type != "ussd" {
		return nil
	}
	m, err := ussd.Parse(d.Trigger.RawCfg)
	if err != nil {
		return []Problem{{Path: "/trigger/config", Message: err.Error()}}
	}
	var out []Problem
	for _, p := range ussd.Check(m, d.USSDFields()) {
		out = append(out, Problem{Path: "/trigger/config" + p.Path, Message: p.Message})
	}
	return out
}

// USSDMenu parses a ussd trigger's menu, with the inputs the schema marks
// personal (x-pii) masked on screens.
func (d *Definition) USSDMenu() (*ussd.Menu, error) {
	m, err := ussd.Parse(d.Trigger.RawCfg)
	if err != nil {
		return nil, err
	}
	m.Personal = map[string]bool{}
	schema, types := d.InputsSchema()
	for _, p := range pii.SchemaPaths(schema, types) {
		if len(p.Segments) == 1 {
			m.Personal[p.Segments[0]] = true
		}
	}
	return m, nil
}

// USSDFields is what a menu needs to know of the inputs schema: its
// top-level properties, their types and which are required. A schema
// that is not a plain object (or a reference to one) is not checked.
func (d *Definition) USSDFields() ussd.Fields {
	schema, types := d.InputsSchema()
	obj := resolveLocal(schema, types)
	if obj == nil {
		return ussd.Fields{}
	}
	props, ok := obj["properties"].(map[string]any)
	if !ok {
		return ussd.Fields{}
	}
	f := ussd.Fields{Known: true, Names: map[string]string{}}
	for k, v := range props {
		t := ""
		if pm := resolveLocal(v, types); pm != nil {
			t, _ = pm["type"].(string)
		}
		f.Names[k] = t
	}
	if req, ok := obj["required"].([]any); ok {
		for _, r := range req {
			if s, ok := r.(string); ok {
				f.Required = append(f.Required, s)
			}
		}
	}
	return f
}

func resolveLocal(v any, types map[string]any) map[string]any {
	for range 8 {
		m, ok := v.(map[string]any)
		if !ok {
			return nil
		}
		ref, ok := m["$ref"].(string)
		if !ok {
			return m
		}
		name, ok := strings.CutPrefix(ref, "#/types/")
		if !ok {
			return nil
		}
		v = types[name]
	}
	return nil
}
