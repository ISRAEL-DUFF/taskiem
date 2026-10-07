// Package openapi holds the OpenAPI 3.1 document for Taskiem's public API
// (openapi.yaml), a reader for the parts the docs site renders, and a
// validator that checks real answers against its schemas.
//
// The document is written by hand. api/openapi_test.go keeps it honest:
// every public route is in it and nothing else, the permissions, plan
// features and authentication it names match the router's source, and the
// API tests' answers match its schemas.
package openapi

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"slices"
	"sort"
	"strings"

	"sigs.k8s.io/yaml"
)

//go:embed openapi.yaml
var source []byte

// Source is the document as written (YAML).
func Source() []byte { return source }

// Document is the subset of an OpenAPI 3.1 document Taskiem uses.
type Document struct {
	OpenAPI    string                           `json:"openapi"`
	Info       Info                             `json:"info"`
	Tags       []Tag                            `json:"tags"`
	Paths      map[string]map[string]*Operation `json:"paths"`
	Components Components                       `json:"components"`
}

// Info is the document's title and introduction.
type Info struct {
	Title       string `json:"title"`
	Version     string `json:"version"`
	Summary     string `json:"summary"`
	Description string `json:"description"` // Markdown
}

// Tag groups operations; Guide names the docs page that explains them.
type Tag struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Guide       string `json:"x-taskiem-guide"`
}

// Operation is one method on one path.
type Operation struct {
	OperationID string                `json:"operationId"`
	Tags        []string              `json:"tags"`
	Summary     string                `json:"summary"`
	Description string                `json:"description"`
	Security    []map[string][]string `json:"security"`
	// Permission is the permission the caller needs, if any.
	Permission string `json:"x-taskiem-permission"`
	// Features are the plan features the operation needs when billing is on.
	Features []string `json:"x-taskiem-plan-feature"`
	// AllEnvironments marks operations an API key limited to one
	// environment may not call: they reach every environment.
	AllEnvironments bool                 `json:"x-taskiem-all-environments"`
	Parameters      []Parameter          `json:"parameters"`
	RequestBody     *RequestBody         `json:"requestBody"`
	Responses       map[string]*Response `json:"responses"`
}

// Parameter is a path, query or header parameter.
type Parameter struct {
	Name        string         `json:"name"`
	In          string         `json:"in"`
	Required    bool           `json:"required"`
	Description string         `json:"description"`
	Schema      map[string]any `json:"schema"`
}

// RequestBody is what an operation takes.
type RequestBody struct {
	Required bool                 `json:"required"`
	Content  map[string]MediaType `json:"content"`
}

// Response is one answer; Ref points into components.responses.
type Response struct {
	Ref         string               `json:"$ref"`
	Description string               `json:"description"`
	Content     map[string]MediaType `json:"content"`
}

// MediaType holds a body's schema.
type MediaType struct {
	Schema map[string]any `json:"schema"`
}

// SecurityScheme is one way to authenticate.
type SecurityScheme struct {
	Type         string `json:"type"`
	Scheme       string `json:"scheme"`
	BearerFormat string `json:"bearerFormat"`
	In           string `json:"in"`
	Name         string `json:"name"`
	Description  string `json:"description"`
}

// Components are the document's shared parts.
type Components struct {
	SecuritySchemes map[string]SecurityScheme `json:"securitySchemes"`
	Responses       map[string]*Response      `json:"responses"`
	Schemas         map[string]map[string]any `json:"schemas"`
}

// Methods are the HTTP methods an operation may use, in display order.
var Methods = []string{"get", "post", "put", "patch", "delete"}

// Load reads the embedded document.
func Load() (*Document, error) { return Parse(source) }

// Parse reads a document from YAML or JSON.
func Parse(src []byte) (*Document, error) {
	raw, err := yaml.YAMLToJSON(src)
	if err != nil {
		return nil, fmt.Errorf("openapi: %w", err)
	}
	var d Document
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	if err := dec.Decode(&d); err != nil {
		return nil, fmt.Errorf("openapi: %w", err)
	}
	for path, item := range d.Paths {
		for m := range item {
			if !slices.Contains(Methods, m) {
				return nil, fmt.Errorf("openapi: %s: unsupported key %q", path, m)
			}
		}
	}
	return &d, nil
}

// Op is an operation with its method and path.
type Op struct {
	Method string // upper case
	Path   string
	*Operation
}

// Operations lists every operation, by path and then method.
func (d *Document) Operations() []Op {
	var out []Op
	for path, item := range d.Paths {
		for m, op := range item {
			out = append(out, Op{Method: strings.ToUpper(m), Path: path, Operation: op})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Path != out[j].Path {
			return out[i].Path < out[j].Path
		}
		return slices.Index(Methods, strings.ToLower(out[i].Method)) < slices.Index(Methods, strings.ToLower(out[j].Method))
	})
	return out
}

// Response resolves a response, following a reference to components.
func (d *Document) Response(r *Response) *Response {
	if r == nil || r.Ref == "" {
		return r
	}
	return d.Components.Responses[strings.TrimPrefix(r.Ref, "#/components/responses/")]
}
