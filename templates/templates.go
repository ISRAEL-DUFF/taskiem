// Package templates is the SME template library (spec 11.1, docs/templates.md):
// ready workflows for small businesses ("every Friday, text my customers
// who owe me"), each a wd/v1 definition with named, typed, described
// parameters and a plain-language description. They are files in this
// directory, embedded in the binary, served by GET /v1/templates,
// instantiated into draft workflows by POST /v1/templates/{id}/instantiate,
// offered to the AI builder as retrieval context (templates first, then
// free drafting), and named by embed apps' allowed_templates.
//
// A template's definition marks parameters with {{name}}. Instantiation
// substitutes values by where the marker stands:
//
//   - a whole JSON string "{{name}}" becomes the typed value (a number
//     stays a number);
//   - inside an expression (a string starting with "="), the marker
//     becomes a CEL literal: strings quoted and escaped, so a value can
//     never change the expression around it;
//   - inside any other string, the value's text.
//
// Modifiers derive values: {{time:hour}} and {{time:minute}} from a time
// (HH:MM), {{day:cron}} from a weekday (0–6), {{amount:kobo}} from a naira
// amount (×100, an integer). No value may start with "=": a parameter is
// data, never an expression.
//
// Personal data (an owner's phone number, an accountant's email) is not a
// parameter: templates read it from tenant variables (env.*) and list the
// variables to create, so no definition carries it.
package templates

import (
	"bytes"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"math"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"unicode"

	"github.com/israel-duff/taskiem/engine/wd"
)

//go:embed library/*.json
var files embed.FS

// Param is one parameter a person fills in.
type Param struct {
	Name        string   `json:"name"`
	Type        string   `json:"type"` // string | text | integer | number | boolean | time | weekday | connection
	Title       string   `json:"title"`
	Description string   `json:"description"`
	Required    bool     `json:"required"`
	Default     any      `json:"default,omitempty"`
	Example     any      `json:"example"`
	Enum        []string `json:"enum,omitempty"`
	Pattern     string   `json:"pattern,omitempty"`
	Minimum     *float64 `json:"minimum,omitempty"`
	Maximum     *float64 `json:"maximum,omitempty"`
	MaxLength   int      `json:"max_length,omitempty"`
	// Connector, for a connection parameter: the connector it names a
	// connection of.
	Connector string `json:"connector,omitempty"`

	re *regexp.Regexp
}

// Need is a connection or variable a template expects the tenant to have.
type Need struct {
	Name      string `json:"name"`
	Connector string `json:"connector,omitempty"`
	Why       string `json:"why"`
}

// Template is one library entry.
type Template struct {
	ID          string   `json:"id"`
	Title       string   `json:"title"`
	Summary     string   `json:"summary"`     // one line
	Description string   `json:"description"` // a paragraph, plain language
	Category    string   `json:"category"`
	Tags        []string `json:"tags"`
	// Keywords help retrieval match a goal's wording.
	Keywords   []string `json:"keywords,omitempty"`
	Connectors []string `json:"connectors"`
	// Variables are tenant variables the workflow reads (personal data
	// such as an owner's phone number lives there, not in parameters).
	Variables  []Need          `json:"variables,omitempty"`
	Params     []Param         `json:"params"`
	Definition json.RawMessage `json:"definition"`
}

// Library is the set of templates.
type Library struct {
	list []*Template
	byID map[string]*Template
}

var (
	defaultOnce sync.Once
	defaultLib  *Library
	defaultErr  error
)

// Default is the embedded library. It panics if the embedded files are
// broken, which the package's tests rule out.
func Default() *Library {
	defaultOnce.Do(func() {
		defaultLib, defaultErr = Load(files)
	})
	if defaultErr != nil {
		panic(defaultErr)
	}
	return defaultLib
}

// IDPattern is what a template id looks like; it is the pattern embed
// apps' allowed_templates accept.
var IDPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,63}$`)

var paramName = regexp.MustCompile(`^[a-z][a-z0-9_]{0,39}$`)

var paramTypes = map[string]bool{"string": true, "text": true, "integer": true, "number": true, "boolean": true, "time": true, "weekday": true, "connection": true}

// Load reads every library/*.json file of fsys.
func Load(fsys fs.FS) (*Library, error) {
	names, err := fs.Glob(fsys, "library/*.json")
	if err != nil {
		return nil, err
	}
	sort.Strings(names)
	lib := &Library{byID: map[string]*Template{}}
	for _, n := range names {
		raw, err := fs.ReadFile(fsys, n)
		if err != nil {
			return nil, err
		}
		var t Template
		dec := json.NewDecoder(bytes.NewReader(raw))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&t); err != nil {
			return nil, fmt.Errorf("%s: %w", n, err)
		}
		if err := t.check(); err != nil {
			return nil, fmt.Errorf("%s: %w", n, err)
		}
		if want := strings.TrimSuffix(path.Base(n), ".json"); t.ID != want {
			return nil, fmt.Errorf("%s: id %q must match the file name", n, t.ID)
		}
		if lib.byID[t.ID] != nil {
			return nil, fmt.Errorf("%s: duplicate id %q", n, t.ID)
		}
		lib.byID[t.ID] = &t
		lib.list = append(lib.list, &t)
	}
	return lib, nil
}

var marker = regexp.MustCompile(`\{\{\s*([a-z][a-z0-9_]*)(?::([a-z]+))?\s*\}\}`)

func (t *Template) check() error {
	if !IDPattern.MatchString(t.ID) {
		return fmt.Errorf("bad id %q", t.ID)
	}
	if t.Title == "" || t.Summary == "" || t.Description == "" || t.Category == "" || len(t.Definition) == 0 {
		return errors.New("title, summary, description, category and definition are required")
	}
	seen := map[string]bool{}
	for i := range t.Params {
		p := &t.Params[i]
		if !paramName.MatchString(p.Name) || seen[p.Name] {
			return fmt.Errorf("parameter %q: bad or duplicate name", p.Name)
		}
		seen[p.Name] = true
		if !paramTypes[p.Type] {
			return fmt.Errorf("parameter %q: unknown type %q", p.Name, p.Type)
		}
		if p.Title == "" || p.Description == "" {
			return fmt.Errorf("parameter %q: title and description are required", p.Name)
		}
		if p.Pattern != "" {
			re, err := regexp.Compile(p.Pattern)
			if err != nil {
				return fmt.Errorf("parameter %q: %w", p.Name, err)
			}
			p.re = re
		}
		if p.Example == nil {
			return fmt.Errorf("parameter %q: an example is required", p.Name)
		}
		if _, err := p.Coerce(p.Example); err != nil {
			return fmt.Errorf("parameter %q: example: %w", p.Name, err)
		}
		if p.Default != nil {
			if _, err := p.Coerce(p.Default); err != nil {
				return fmt.Errorf("parameter %q: default: %w", p.Name, err)
			}
		}
	}
	used := map[string]bool{}
	for _, m := range marker.FindAllStringSubmatch(string(t.Definition), -1) {
		if !seen[m[1]] {
			return fmt.Errorf("the definition uses {{%s}}, which is not a parameter", m[1])
		}
		used[m[1]] = true
	}
	for _, p := range t.Params {
		if !used[p.Name] {
			return fmt.Errorf("parameter %q is not used by the definition", p.Name)
		}
	}
	return nil
}

// List returns every template, in id order.
func (l *Library) List() []*Template { return l.list }

// Get returns a template by id.
func (l *Library) Get(id string) (*Template, bool) {
	t, ok := l.byID[id]
	return t, ok
}

// Has reports whether id is a template.
func (l *Library) Has(id string) bool { _, ok := l.byID[id]; return ok }

// ParamError is a parameter value that does not fit.
type ParamError struct {
	Param   string `json:"param"`
	Message string `json:"message"`
}

func (e ParamError) Error() string { return e.Param + ": " + e.Message }

// ErrParams wraps the problems with a set of values.
type ErrParams []ParamError

func (e ErrParams) Error() string {
	msgs := make([]string, len(e))
	for i, p := range e {
		msgs[i] = p.Error()
	}
	return "template parameters: " + strings.Join(msgs, "; ")
}

var reTime = regexp.MustCompile(`^([01]?[0-9]|2[0-3]):([0-5][0-9])$`)

var weekdayNames = map[string]int{"sunday": 0, "sun": 0, "monday": 1, "mon": 1, "tuesday": 2, "tue": 2, "tues": 2, "wednesday": 3, "wed": 3,
	"thursday": 4, "thu": 4, "thur": 4, "thurs": 4, "friday": 5, "fri": 5, "saturday": 6, "sat": 6}

var connName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,62}$`)

// Coerce checks a value against the parameter and returns it in its
// canonical form: numbers as float64 or int64, a weekday as its lower-case
// English name, a time as HH:MM. Text a person typed (WhatsApp, a form)
// is accepted for every type.
func (p *Param) Coerce(v any) (any, error) {
	if s, ok := v.(string); ok {
		v = strings.TrimSpace(s)
		if strings.HasPrefix(v.(string), "=") {
			return nil, errors.New("a value cannot start with '='")
		}
	}
	switch p.Type {
	case "string", "text":
		s, ok := v.(string)
		if !ok {
			return nil, errors.New("must be text")
		}
		if s == "" {
			return nil, errors.New("must not be empty")
		}
		limit := p.MaxLength
		if limit == 0 {
			limit = 200
			if p.Type == "text" {
				limit = 900
			}
		}
		if len([]rune(s)) > limit {
			return nil, fmt.Errorf("must be at most %d characters", limit)
		}
		if p.Type == "string" && strings.ContainsAny(s, "\n\r") {
			return nil, errors.New("must be one line")
		}
		if strings.Contains(s, "{{") {
			return nil, errors.New("must not contain {{")
		}
		if len(p.Enum) > 0 {
			for _, e := range p.Enum {
				if strings.EqualFold(e, s) {
					return e, nil
				}
			}
			return nil, fmt.Errorf("must be one of %s", strings.Join(p.Enum, ", "))
		}
		if p.re != nil && !p.re.MatchString(s) {
			return nil, errors.New("is not in the expected format")
		}
		return s, nil
	case "connection":
		s, ok := v.(string)
		if !ok || !connName.MatchString(s) {
			return nil, errors.New("must be a connection name (letters, digits, '.', '_' or '-')")
		}
		return s, nil
	case "integer", "number":
		var f float64
		switch x := v.(type) {
		case float64:
			f = x
		case int:
			f = float64(x)
		case int64:
			f = float64(x)
		case json.Number:
			n, err := x.Float64()
			if err != nil {
				return nil, errors.New("must be a number")
			}
			f = n
		case string:
			clean := strings.NewReplacer(",", "", "₦", "", "NGN", "", "ngn", "", " ", "").Replace(x)
			n, err := strconv.ParseFloat(clean, 64)
			if err != nil {
				return nil, errors.New("must be a number")
			}
			f = n
		default:
			return nil, errors.New("must be a number")
		}
		if math.IsNaN(f) || math.IsInf(f, 0) {
			return nil, errors.New("must be a number")
		}
		if p.Type == "integer" && f != math.Trunc(f) {
			return nil, errors.New("must be a whole number")
		}
		if p.Minimum != nil && f < *p.Minimum {
			return nil, fmt.Errorf("must be at least %s", strconv.FormatFloat(*p.Minimum, 'f', -1, 64))
		}
		if p.Maximum != nil && f > *p.Maximum {
			return nil, fmt.Errorf("must be at most %s", strconv.FormatFloat(*p.Maximum, 'f', -1, 64))
		}
		if math.Abs(f) > 1e15 {
			return nil, errors.New("is too large")
		}
		if p.Type == "integer" {
			return int64(f), nil
		}
		return f, nil
	case "boolean":
		switch x := v.(type) {
		case bool:
			return x, nil
		case string:
			switch strings.ToLower(x) {
			case "yes", "y", "true", "on", "1":
				return true, nil
			case "no", "n", "false", "off", "0":
				return false, nil
			}
		}
		return nil, errors.New("must be yes or no")
	case "time":
		s, ok := v.(string)
		if !ok {
			return nil, errors.New("must be a time like 09:30")
		}
		s = normalizeTime(s)
		m := reTime.FindStringSubmatch(s)
		if m == nil {
			return nil, errors.New("must be a time like 09:30 (24-hour clock)")
		}
		h, _ := strconv.Atoi(m[1])
		return fmt.Sprintf("%02d:%s", h, m[2]), nil
	case "weekday":
		s, ok := v.(string)
		if !ok {
			return nil, errors.New("must be a day of the week")
		}
		d, ok := weekdayNames[strings.ToLower(strings.TrimSuffix(s, "s"))]
		if !ok {
			d, ok = weekdayNames[strings.ToLower(s)]
		}
		if !ok {
			return nil, errors.New("must be a day of the week, like Friday")
		}
		for name, n := range weekdayNames {
			if n == d && len(name) > 4 && name != "tues" && name != "thurs" {
				return name, nil
			}
		}
	}
	return nil, fmt.Errorf("unknown type %q", p.Type)
}

// normalizeTime accepts "9am", "9:30 pm", "21.30" as well as "21:30".
func normalizeTime(s string) string {
	s = strings.ToLower(strings.ReplaceAll(strings.TrimSpace(s), " ", ""))
	s = strings.ReplaceAll(s, ".", ":")
	pm := strings.HasSuffix(s, "pm")
	am := strings.HasSuffix(s, "am")
	if !pm && !am {
		return s
	}
	s = strings.TrimSuffix(strings.TrimSuffix(s, "pm"), "am")
	h, m, _ := strings.Cut(s, ":")
	if m == "" {
		m = "00"
	}
	n, err := strconv.Atoi(h)
	if err != nil || n < 1 || n > 12 {
		return s
	}
	if pm && n != 12 {
		n += 12
	}
	if am && n == 12 {
		n = 0
	}
	return fmt.Sprintf("%d:%s", n, m)
}

// Resolve checks values against the template's parameters, fills
// defaults, and returns the canonical values with the required
// parameters still missing. Unknown names are refused.
func (t *Template) Resolve(values map[string]any) (map[string]any, []string, error) {
	out := map[string]any{}
	var errs ErrParams
	known := map[string]bool{}
	for i := range t.Params {
		p := &t.Params[i]
		known[p.Name] = true
		v, ok := values[p.Name]
		if !ok || v == nil || v == "" {
			if p.Default != nil {
				d, _ := p.Coerce(p.Default)
				out[p.Name] = d
			}
			continue
		}
		c, err := p.Coerce(v)
		if err != nil {
			errs = append(errs, ParamError{Param: p.Name, Message: err.Error()})
			continue
		}
		out[p.Name] = c
	}
	names := make([]string, 0, len(values))
	for k := range values {
		names = append(names, k)
	}
	sort.Strings(names)
	for _, k := range names {
		if !known[k] {
			errs = append(errs, ParamError{Param: k, Message: "is not a parameter of this template"})
		}
	}
	var missing []string
	for _, p := range t.Params {
		if _, ok := out[p.Name]; !ok && p.Required {
			missing = append(missing, p.Name)
		}
	}
	if len(errs) > 0 {
		return out, missing, errs
	}
	return out, missing, nil
}

// Examples returns every parameter's example value.
func (t *Template) Examples() map[string]any {
	out := map[string]any{}
	for _, p := range t.Params {
		out[p.Name] = p.Example
	}
	return out
}

// Param returns a parameter by name.
func (t *Template) Param(name string) (*Param, bool) {
	for i := range t.Params {
		if t.Params[i].Name == name {
			return &t.Params[i], true
		}
	}
	return nil, false
}

// Instantiate fills the template with values (checked by Resolve) and
// returns the definition, loaded by wd.Load. Every required parameter
// must have a value; name, when not empty, replaces the definition's
// name.
func (t *Template) Instantiate(values map[string]any, name string) ([]byte, error) {
	vals, missing, err := t.Resolve(values)
	if err != nil {
		return nil, err
	}
	if len(missing) > 0 {
		errs := make(ErrParams, len(missing))
		for i, m := range missing {
			errs[i] = ParamError{Param: m, Message: "is required"}
		}
		return nil, errs
	}
	// Optional parameters with neither a value nor a default take their
	// example, so the definition stays complete.
	for _, p := range t.Params {
		if _, ok := vals[p.Name]; !ok {
			vals[p.Name], _ = p.Coerce(p.Example)
		}
	}
	doc, err := substitute(t.Definition, func(name, mod string, inExpr, whole bool) (any, error) {
		p, _ := t.Param(name)
		return render(p, vals[name], mod, inExpr, whole)
	})
	if err != nil {
		return nil, err
	}
	if name = strings.TrimSpace(name); name != "" {
		if len(name) > 200 || strings.ContainsAny(name, "\n\r") {
			return nil, ErrParams{{Param: "name", Message: "must be one line of at most 200 characters"}}
		}
		if doc, err = setTop(doc, "name", name); err != nil {
			return nil, err
		}
	}
	if _, err := wd.Load(doc); err != nil {
		return nil, fmt.Errorf("the template did not produce a valid workflow: %w", err)
	}
	return doc, nil
}

type renderFunc func(name, mod string, inExpr, whole bool) (any, error)

// jsonString matches a JSON string literal in valid JSON.
var jsonString = regexp.MustCompile(`"(?:[^"\\]|\\.)*"`)

// substitute replaces markers inside the JSON text's string literals,
// keeping the document's key order and layout (compacted).
func substitute(def json.RawMessage, fn renderFunc) ([]byte, error) {
	var ferr error
	out := jsonString.ReplaceAllFunc(def, func(lit []byte) []byte {
		if ferr != nil || !marker.Match(lit) {
			return lit
		}
		var s string
		if err := json.Unmarshal(lit, &s); err != nil {
			ferr = err
			return lit
		}
		var v any
		if m := marker.FindStringSubmatch(s); m != nil && m[0] == s {
			v, ferr = fn(m[1], m[2], false, true)
		} else {
			inExpr := strings.HasPrefix(s, "=")
			v = marker.ReplaceAllStringFunc(s, func(x string) string {
				m := marker.FindStringSubmatch(x)
				r, err := fn(m[1], m[2], inExpr, false)
				if err != nil {
					ferr = err
					return ""
				}
				return fmt.Sprint(r)
			})
		}
		var buf bytes.Buffer
		enc := json.NewEncoder(&buf)
		enc.SetEscapeHTML(false)
		if err := enc.Encode(v); err != nil {
			ferr = err
		}
		return bytes.TrimRight(buf.Bytes(), "\n")
	})
	if ferr != nil {
		return nil, ferr
	}
	var buf bytes.Buffer
	if err := json.Compact(&buf, out); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// setTop replaces (or adds) a top-level key of a JSON object, keeping the
// order of the others.
func setTop(doc []byte, key string, value any) ([]byte, error) {
	dec := json.NewDecoder(bytes.NewReader(doc))
	if tok, err := dec.Token(); err != nil || tok != json.Delim('{') {
		return nil, errors.New("the definition is not an object")
	}
	val, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	buf.WriteByte('{')
	found := false
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return nil, err
		}
		k, _ := tok.(string)
		var raw json.RawMessage
		if err := dec.Decode(&raw); err != nil {
			return nil, err
		}
		if buf.Len() > 1 {
			buf.WriteByte(',')
		}
		kb, _ := json.Marshal(k)
		buf.Write(kb)
		buf.WriteByte(':')
		if k == key {
			raw, found = val, true
		}
		buf.Write(raw)
	}
	if !found {
		if buf.Len() > 1 {
			buf.WriteByte(',')
		}
		kb, _ := json.Marshal(key)
		buf.Write(kb)
		buf.WriteByte(':')
		buf.Write(val)
	}
	buf.WriteByte('}')
	return buf.Bytes(), nil
}

// render turns a canonical value into what stands in the definition.
func render(p *Param, v any, mod string, inExpr, whole bool) (any, error) {
	switch mod {
	case "":
	case "hour", "minute":
		s, _ := v.(string)
		h, m, ok := strings.Cut(s, ":")
		if !ok {
			return nil, fmt.Errorf("{{%s:%s}} needs a time", p.Name, mod)
		}
		part := h
		if mod == "minute" {
			part = m
		}
		n, _ := strconv.Atoi(part)
		v = int64(n)
	case "cron":
		s, _ := v.(string)
		d, ok := weekdayNames[s]
		if !ok {
			return nil, fmt.Errorf("{{%s:cron}} needs a weekday", p.Name)
		}
		v = int64(d)
	case "kobo":
		f, ok := toFloat(v)
		if !ok {
			return nil, fmt.Errorf("{{%s:kobo}} needs a number", p.Name)
		}
		v = int64(math.Round(f * 100))
	default:
		return nil, fmt.Errorf("unknown modifier %q", mod)
	}
	if whole {
		return v, nil
	}
	if inExpr {
		return celLiteral(v), nil
	}
	switch x := v.(type) {
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64), nil
	}
	return fmt.Sprint(v), nil
}

func toFloat(v any) (float64, bool) {
	switch x := v.(type) {
	case float64:
		return x, true
	case int64:
		return float64(x), true
	}
	return 0, false
}

// celLiteral is v as a CEL literal.
func celLiteral(v any) string {
	switch x := v.(type) {
	case bool:
		return strconv.FormatBool(x)
	case int64:
		return strconv.FormatInt(x, 10)
	case float64:
		s := strconv.FormatFloat(x, 'f', -1, 64)
		if !strings.ContainsAny(s, ".e") {
			s += ".0"
		}
		return s
	case string:
		var b strings.Builder
		b.WriteByte('\'')
		for _, r := range x {
			switch {
			case r == '\'' || r == '\\':
				b.WriteByte('\\')
				b.WriteRune(r)
			case r == '\n':
				b.WriteString(`\n`)
			case r == '\r':
				b.WriteString(`\r`)
			case r == '\t':
				b.WriteString(`\t`)
			case !unicode.IsPrint(r) && r > 0xFFFF:
				fmt.Fprintf(&b, `\U%08x`, r)
			case !unicode.IsPrint(r):
				fmt.Fprintf(&b, `\u%04x`, r)
			default:
				b.WriteRune(r)
			}
		}
		b.WriteByte('\'')
		return b.String()
	}
	return "null"
}
