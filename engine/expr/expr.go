// Package expr evaluates WD expressions: strings starting with "=" are CEL
// (decision 0003). Evaluation is deterministic and cost-bounded, so the
// orchestrator can run expressions inside decide().
package expr

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"cel.dev/cel-go/cel"
	"cel.dev/cel-go/common/types"
	"cel.dev/cel-go/common/types/ref"
	"cel.dev/cel-go/common/types/traits"
	"cel.dev/cel-go/ext"
)

// Roots are the variables an expression may reference (wd-v1 contract rule 10).
var Roots = []string{"trigger", "steps", "run", "env", "secrets", "item", "index"}

// DefaultCostLimit bounds the work one expression may do.
const DefaultCostLimit = 1_000_000

// Engine compiles and caches expressions. It is safe for concurrent use.
type Engine struct {
	env       *cel.Env
	costLimit uint64
	cache     sync.Map // source -> cel.Program
}

// New returns an engine with the standard roots and extensions.
func New() (*Engine, error) { return NewWithRoots(Roots...) }

// NewWithRoots returns an engine whose expressions may reference only the
// given variables (connector manifests evaluate over "body" and "headers").
func NewWithRoots(roots ...string) (*Engine, error) {
	opts := []cel.EnvOption{ext.Strings(), ext.Math(), ext.Lists(), cel.OptionalTypes()}
	for _, r := range roots {
		opts = append(opts, cel.Variable(r, cel.DynType))
	}
	env, err := cel.NewEnv(opts...)
	if err != nil {
		return nil, err
	}
	return &Engine{env: env, costLimit: DefaultCostLimit}, nil
}

// MustNew is New for package-level engines.
func MustNew() *Engine {
	e, err := New()
	if err != nil {
		panic(err)
	}
	return e
}

// MustNewWithRoots is NewWithRoots for package-level engines.
func MustNewWithRoots(roots ...string) *Engine {
	e, err := NewWithRoots(roots...)
	if err != nil {
		panic(err)
	}
	return e
}

// IsExpr reports whether v is an expression string.
func IsExpr(v any) bool {
	s, ok := v.(string)
	return ok && len(s) > 1 && s[0] == '='
}

// Error is an expression compile or evaluation failure.
type Error struct {
	Expr string
	Err  error
}

func (e *Error) Error() string { return fmt.Sprintf("expression %q: %v", e.Expr, e.Err) }
func (e *Error) Unwrap() error { return e.Err }

func (e *Engine) program(src string) (cel.Program, error) {
	if p, ok := e.cache.Load(src); ok {
		return p.(cel.Program), nil
	}
	ast, iss := e.env.Compile(src)
	if iss.Err() != nil {
		return nil, iss.Err()
	}
	p, err := e.env.Program(ast, cel.CostLimit(e.costLimit), cel.InterruptCheckFrequency(100))
	if err != nil {
		return nil, err
	}
	e.cache.Store(src, p)
	return p, nil
}

// Eval evaluates one expression (with or without its leading "=") against
// the activation and returns a JSON-compatible Go value.
func (e *Engine) Eval(src string, act map[string]any) (any, error) {
	src = strings.TrimPrefix(src, "=")
	p, err := e.program(src)
	if err != nil {
		return nil, &Error{Expr: src, Err: err}
	}
	if act == nil {
		act = map[string]any{}
	}
	out, _, err := p.Eval(act)
	if err != nil {
		return nil, &Error{Expr: src, Err: err}
	}
	v, err := native(out)
	if err != nil {
		return nil, &Error{Expr: src, Err: err}
	}
	return v, nil
}

// EvalBool evaluates a condition.
func (e *Engine) EvalBool(src string, act map[string]any) (bool, error) {
	v, err := e.Eval(src, act)
	if err != nil {
		return false, err
	}
	b, ok := v.(bool)
	if !ok {
		return false, &Error{Expr: src, Err: fmt.Errorf("want bool, got %T", v)}
	}
	return b, nil
}

// Resolve walks a value and evaluates every expression in it. With
// keepSecrets, expressions that reference secrets are left as strings for the
// worker to resolve at execution time, so secret values never enter history.
func (e *Engine) Resolve(v any, act map[string]any, keepSecrets bool) (any, error) {
	switch t := v.(type) {
	case string:
		if !IsExpr(t) {
			return t, nil
		}
		if keepSecrets {
			refs, err := e.References(t)
			if err != nil {
				return nil, err
			}
			if refs.Roots["secrets"] {
				return t, nil
			}
		}
		return e.Eval(t, act)
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, c := range t {
			r, err := e.Resolve(c, act, keepSecrets)
			if err != nil {
				return nil, err
			}
			out[k] = r
		}
		return out, nil
	case []any:
		out := make([]any, len(t))
		for i, c := range t {
			r, err := e.Resolve(c, act, keepSecrets)
			if err != nil {
				return nil, err
			}
			out[i] = r
		}
		return out, nil
	}
	return v, nil
}

// native converts a CEL value into JSON-compatible Go: nil, bool, int64,
// uint64, float64, string, []any, map[string]any. Timestamps and durations
// become RFC 3339 and Go duration strings.
func native(v ref.Val) (any, error) {
	switch t := v.(type) {
	case types.Null:
		return nil, nil
	case types.Bool:
		return bool(t), nil
	case types.Int:
		return int64(t), nil
	case types.Uint:
		return uint64(t), nil
	case types.Double:
		return float64(t), nil
	case types.String:
		return string(t), nil
	case types.Bytes:
		return []byte(t), nil
	case types.Timestamp:
		return t.Time.UTC().Format(time.RFC3339Nano), nil
	case types.Duration:
		return t.String(), nil
	}
	if v.Type() == types.OptionalType {
		o := v.(*types.Optional)
		if !o.HasValue() {
			return nil, nil
		}
		return native(o.GetValue())
	}
	if m, ok := v.(traits.Mapper); ok {
		out := map[string]any{}
		it := m.Iterator()
		for it.HasNext() == types.True {
			k := it.Next()
			ks, ok := k.(types.String)
			if !ok {
				return nil, fmt.Errorf("map key %v is not a string", k)
			}
			nv, err := native(m.Get(k))
			if err != nil {
				return nil, err
			}
			out[string(ks)] = nv
		}
		return out, nil
	}
	if l, ok := v.(traits.Lister); ok {
		var out []any
		it := l.Iterator()
		for it.HasNext() == types.True {
			nv, err := native(it.Next())
			if err != nil {
				return nil, err
			}
			out = append(out, nv)
		}
		if out == nil {
			out = []any{}
		}
		return out, nil
	}
	if types.IsError(v) {
		return nil, fmt.Errorf("%v", v)
	}
	return nil, fmt.Errorf("unsupported result type %s", v.Type().TypeName())
}

// DecodeJSON decodes JSON into JSON-compatible Go values with integral
// numbers as int64 (so kobo amounts stay exact) and others as float64.
func DecodeJSON(b []byte) (any, error) {
	if len(bytes.TrimSpace(b)) == 0 {
		return nil, nil
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, err
	}
	return Normalise(v), nil
}

// Normalise converts json.Number values to int64 or float64, recursively.
func Normalise(v any) any {
	switch t := v.(type) {
	case json.Number:
		if i, err := t.Int64(); err == nil {
			return i
		}
		f, _ := t.Float64()
		return f
	case map[string]any:
		for k, c := range t {
			t[k] = Normalise(c)
		}
	case []any:
		for i, c := range t {
			t[i] = Normalise(c)
		}
	}
	return v
}

// Refs are the variables an expression reads.
type Refs struct {
	Roots   map[string]bool // top-level identifiers used
	Steps   map[string]bool // ids selected directly from steps (steps.<id>)
	Secrets map[string]bool // names selected directly from secrets (secrets.<name>)
}

// References parses an expression and reports which roots and step ids it
// reads. Unknown identifiers are reported as errors.
func (e *Engine) References(src string) (Refs, error) {
	src = strings.TrimPrefix(src, "=")
	ast, iss := e.env.Compile(src)
	if iss.Err() != nil {
		return Refs{}, &Error{Expr: src, Err: iss.Err()}
	}
	return refsOf(ast), nil
}
