// Package schemacheck compiles the embedded contract schemas once and turns
// validation failures into flat, readable messages.
package schemacheck

import (
	"bytes"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"golang.org/x/text/language"
	"golang.org/x/text/message"
)

var printer = message.NewPrinter(language.English)

// Schema is a compiled contract schema.
type Schema struct {
	once sync.Once
	id   string
	raw  []byte
	s    *jsonschema.Schema
	err  error
}

// New returns a lazily compiled schema.
func New(id string, raw []byte) *Schema { return &Schema{id: id, raw: raw} }

func (s *Schema) compile() {
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(s.raw))
	if err != nil {
		s.err = fmt.Errorf("schemacheck: parse %s: %w", s.id, err)
		return
	}
	c := jsonschema.NewCompiler()
	if err := c.AddResource(s.id, doc); err != nil {
		s.err = err
		return
	}
	s.s, s.err = c.Compile(s.id)
}

// Validate checks a JSON document and returns one message per failing leaf.
func (s *Schema) Validate(jsonDoc []byte) []string {
	s.once.Do(s.compile)
	if s.err != nil {
		return []string{s.err.Error()}
	}
	v, err := jsonschema.UnmarshalJSON(bytes.NewReader(jsonDoc))
	if err != nil {
		return []string{"invalid JSON: " + err.Error()}
	}
	err = s.s.Validate(v)
	if err == nil {
		return nil
	}
	ve, ok := err.(*jsonschema.ValidationError)
	if !ok {
		return []string{err.Error()}
	}
	var out []string
	var walk func(e *jsonschema.ValidationError)
	walk = func(e *jsonschema.ValidationError) {
		if len(e.Causes) == 0 {
			loc := "/" + strings.Join(e.InstanceLocation, "/")
			out = append(out, fmt.Sprintf("%s: %s", loc, e.ErrorKind.LocalizedString(printer)))
			return
		}
		for _, c := range e.Causes {
			walk(c)
		}
	}
	walk(ve)
	sort.Strings(out)
	return dedupe(out)
}

func dedupe(in []string) []string {
	out := in[:0]
	for i, s := range in {
		if i == 0 || s != in[i-1] {
			out = append(out, s)
		}
	}
	return out
}
