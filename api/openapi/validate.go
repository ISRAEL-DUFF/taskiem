package openapi

import (
	"bytes"
	"errors"
	"fmt"
	"mime"
	"strconv"
	"strings"
	"sync"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"sigs.k8s.io/yaml"
)

// docURL is where the document is registered with the schema compiler.
const docURL = "https://taskiem.invalid/openapi.json"

// Validator checks answers against the document's response schemas.
type Validator struct {
	doc *Document
	c   *jsonschema.Compiler

	mu       sync.Mutex
	compiled map[string]*jsonschema.Schema
}

// NewValidator prepares a validator for the embedded document.
func NewValidator() (*Validator, error) {
	doc, err := Load()
	if err != nil {
		return nil, err
	}
	raw, err := yaml.YAMLToJSON(source)
	if err != nil {
		return nil, err
	}
	v, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	c := jsonschema.NewCompiler()
	c.DefaultDraft(jsonschema.Draft2020)
	if err := c.AddResource(docURL, v); err != nil {
		return nil, err
	}
	return &Validator{doc: doc, c: c, compiled: map[string]*jsonschema.Schema{}}, nil
}

// Document is the document the validator checks against.
func (v *Validator) Document() *Document { return v.doc }

// ErrUndeclared is a success status the operation does not declare.
var ErrUndeclared = errors.New("status not declared")

// ValidateResponse checks one answer of the operation method pattern (a
// path as the document writes it, e.g. /v1/workflows/{wf}). A JSON body
// is checked against the schema of its status, or of default for an
// error; a success status the operation does not declare is
// ErrUndeclared. Other media types are checked only for being declared.
func (v *Validator) ValidateResponse(method, pattern string, status int, contentType string, body []byte) error {
	item, ok := v.doc.Paths[pattern]
	if !ok {
		return fmt.Errorf("%s %s: path not in the document", method, pattern)
	}
	op, ok := item[strings.ToLower(method)]
	if !ok {
		return fmt.Errorf("%s %s: method not in the document", method, pattern)
	}
	key := strconv.Itoa(status)
	resp, ok := op.Responses[key]
	if !ok {
		if status < 400 {
			return fmt.Errorf("%s %s: %w: %d", method, pattern, ErrUndeclared, status)
		}
		key, resp = "default", op.Responses["default"]
		if resp == nil {
			return fmt.Errorf("%s %s: no default response for %d", method, pattern, status)
		}
	}
	resolved := v.doc.Response(resp)
	if resolved == nil {
		return fmt.Errorf("%s %s %s: unresolved response %s", method, pattern, key, resp.Ref)
	}
	if len(resolved.Content) == 0 {
		if len(bytes.TrimSpace(body)) > 0 && status != 302 {
			return fmt.Errorf("%s %s %d: the document declares no body, got %q", method, pattern, status, truncate(body))
		}
		return nil
	}
	mt, _, _ := mime.ParseMediaType(contentType)
	media, ok := resolved.Content[mt]
	if !ok {
		return fmt.Errorf("%s %s %d: media type %q not declared", method, pattern, status, mt)
	}
	if mt != "application/json" || media.Schema == nil {
		return nil
	}
	// The schema's location in the document, as a JSON pointer.
	loc := docURL + "#/paths/" + escape(pattern) + "/" + strings.ToLower(method) + "/responses/" + key + "/content/application~1json/schema"
	if resp.Ref != "" {
		loc = docURL + "#/components/responses/" + escape(strings.TrimPrefix(resp.Ref, "#/components/responses/")) + "/content/application~1json/schema"
	}
	sch, err := v.compile(loc)
	if err != nil {
		return err
	}
	val, err := jsonschema.UnmarshalJSON(bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("%s %s %d: body is not JSON: %w", method, pattern, status, err)
	}
	if err := sch.Validate(val); err != nil {
		return fmt.Errorf("%s %s %d: %w\nbody: %s", method, pattern, status, err, truncate(body))
	}
	return nil
}

func (v *Validator) compile(loc string) (*jsonschema.Schema, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if s, ok := v.compiled[loc]; ok {
		return s, nil
	}
	s, err := v.c.Compile(loc)
	if err != nil {
		return nil, err
	}
	v.compiled[loc] = s
	return s, nil
}

// escape makes a JSON pointer token.
func escape(s string) string {
	return strings.ReplaceAll(strings.ReplaceAll(s, "~", "~0"), "/", "~1")
}

func truncate(b []byte) string {
	if len(b) > 600 {
		return string(b[:600]) + "…"
	}
	return string(b)
}
