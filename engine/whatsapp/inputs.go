package whatsapp

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/israel-duff/taskiem/engine/internal/schemacheck"
	"github.com/israel-duff/taskiem/engine/lang"
)

// Collecting a workflow's inputs in chat (spec 11.1): field by field, each
// answer checked against the field's schema from the workflow's inputs
// schema. Where Flows are set up, a WhatsApp Flows form (flows.go) takes
// their place, and chat remains the fallback.

// Field is one input asked for in chat.
type Field struct {
	Name        string
	Title       string
	Description string
	Type        string // string, integer, number or boolean
	Enum        []any
	PII         string // the field's x-pii category, if any
	schema      *schemacheck.Schema
}

// ErrInputsUnsupported: the inputs need something chat cannot collect yet
// (nested objects, lists).
var ErrInputsUnsupported = errors.New("this workflow's inputs cannot be collected in chat yet")

// InputFields lists the required fields of an inputs schema, in the order
// it lists them. types are the definition's types, for $ref.
func InputFields(schema json.RawMessage, types map[string]json.RawMessage) ([]Field, error) {
	if len(schema) == 0 {
		return nil, nil
	}
	resolve := func(raw json.RawMessage) (map[string]any, error) {
		var m map[string]any
		if err := json.Unmarshal(raw, &m); err != nil {
			return nil, err
		}
		for range 8 {
			ref, _ := m["$ref"].(string)
			name, ok := strings.CutPrefix(ref, "#/types/")
			if !ok {
				break
			}
			t, ok := types[name]
			if !ok {
				return nil, fmt.Errorf("unknown type %q", name)
			}
			m = nil
			if err := json.Unmarshal(t, &m); err != nil {
				return nil, err
			}
		}
		return m, nil
	}
	root, err := resolve(schema)
	if err != nil {
		return nil, err
	}
	if t, _ := root["type"].(string); t != "" && t != "object" {
		return nil, ErrInputsUnsupported
	}
	props, _ := root["properties"].(map[string]any)
	req, _ := root["required"].([]any)
	var out []Field
	for _, r := range req {
		name, _ := r.(string)
		raw, _ := json.Marshal(props[name])
		ps, err := resolve(raw)
		if err != nil {
			return nil, err
		}
		f := Field{Name: name, Title: str(ps, "title"), Description: str(ps, "description"), Type: str(ps, "type"), PII: str(ps, "x-pii")}
		if e, ok := ps["enum"].([]any); ok {
			f.Enum = e
		}
		switch f.Type {
		case "string", "integer", "number", "boolean":
		case "":
			if f.Enum == nil {
				return nil, ErrInputsUnsupported
			}
		default:
			return nil, ErrInputsUnsupported
		}
		// The field's own schema, with the definition's types for $ref.
		own := map[string]any{}
		for k, v := range ps {
			own[k] = v
		}
		if len(types) > 0 {
			own["types"] = types
		}
		doc, _ := json.Marshal(own)
		f.schema = schemacheck.New("https://schemas.taskiem.dev/chat-input/"+name+".json", doc)
		out = append(out, f)
	}
	return out, nil
}

func str(m map[string]any, k string) string {
	s, _ := m[k].(string)
	return s
}

// Prompt asks for the field in English.
func (f Field) Prompt() string { return f.PromptIn(lang.EN) }

// PromptIn asks for the field in a language (engine/lang's wa.field.*
// messages). The field's title, description and options are the
// tenant's own words and stay as written.
func (f Field) PromptIn(t lang.Tag) string {
	label := f.Title
	if label == "" {
		label = f.Name
	}
	tr := lang.Default().Text
	var s string
	switch {
	case len(f.Enum) > 0:
		opts := make([]string, len(f.Enum))
		for i, e := range f.Enum {
			opts[i] = fmt.Sprint(e)
		}
		s = tr(t, "wa.field.enter_one_of", "label", label, "options", strings.Join(opts, ", "))
	case f.Type == "integer":
		s = tr(t, "wa.field.enter_whole", "label", label)
	case f.Type == "number":
		s = tr(t, "wa.field.enter_number", "label", label)
	case f.Type == "boolean":
		s = tr(t, "wa.field.enter_yes_no", "label", label)
	default:
		s = tr(t, "wa.field.enter", "label", label)
	}
	if f.Description != "" {
		s += "\n" + f.Description
	}
	return s
}

// Parse reads an answer as the field's type and checks it against the
// field's schema; the error says what is wrong, in English.
func (f Field) Parse(text string) (any, error) { return f.ParseIn(text, lang.EN) }

// ParseIn is Parse for a person whose language is t: yes and no are also
// taken from t's word lists, and the errors Taskiem writes are in t (a
// schema's own refusals stay in English).
func (f Field) ParseIn(text string, t lang.Tag) (any, error) {
	text = strings.TrimSpace(text)
	var v any
	switch f.Type {
	case "integer":
		n, err := strconv.ParseInt(strings.ReplaceAll(text, ",", ""), 10, 64)
		if err != nil {
			return nil, errors.New(lang.Default().Text(t, "wa.field.not_whole"))
		}
		v = n
	case "number":
		n, err := strconv.ParseFloat(strings.ReplaceAll(text, ",", ""), 64)
		if err != nil {
			return nil, errors.New(lang.Default().Text(t, "wa.field.not_number"))
		}
		v = n
	case "boolean":
		switch strings.ToLower(text) {
		case "yes", "y", "true":
			v = true
		case "no", "n", "false":
			v = false
		default:
			if t != lang.EN {
				if m, ok := lang.MatchIntent(text, []lang.Tag{t}); ok && (m.Intent == lang.IntentYes || m.Intent == lang.IntentNo) {
					v = m.Intent == lang.IntentYes
					break
				}
			}
			return nil, errors.New(lang.Default().Text(t, "wa.field.yes_no"))
		}
	default:
		v = text
		if len(f.Enum) > 0 {
			i := slices.IndexFunc(f.Enum, func(e any) bool { return strings.EqualFold(fmt.Sprint(e), text) })
			if i >= 0 {
				v = f.Enum[i]
			}
		}
	}
	if err := f.Check(v); err != nil {
		return nil, err
	}
	return v, nil
}

// Check checks a value against the field's schema; the error says what is
// wrong.
func (f Field) Check(v any) error {
	doc, _ := json.Marshal(v)
	if probs := f.schema.Validate(doc); len(probs) > 0 {
		msg := probs[0]
		if _, after, ok := strings.Cut(msg, ": "); ok {
			msg = after
		}
		return errors.New(msg)
	}
	return nil
}
