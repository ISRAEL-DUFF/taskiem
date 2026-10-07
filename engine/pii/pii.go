// Package pii seals declared personal data in run history (spec 4.9, 9.3).
// A sealed value is an envelope encrypted under its data subject's own key,
// so destroying that key erases the subject from every event, archive, and
// backup at once (crypto-shredding, spec 9.4).
package pii

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Erased replaces a value whose subject key was destroyed.
const Erased = "[erased]"

// ErrErased means a subject has been erased.
var ErrErased = errors.New("subject erased")

// Cipher seals and opens values inside the caller's tenant transaction (no
// second connection is taken). secrets.Vault implements it.
type Cipher interface {
	SealTx(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, category string, value any) (map[string]any, error)
	OpenTx(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, envelope map[string]any) (any, error)
}

// IsEnvelope reports whether v is a sealed value.
func IsEnvelope(v any) bool {
	m, ok := v.(map[string]any)
	if !ok {
		return false
	}
	_, a := m["$pii"]
	_, b := m["ct"]
	return a && b
}

// Path is a location in a JSON value: object keys and "*" for every array
// element, with the PII category found there.
type Path struct {
	Segments []string
	Category string
}

// SchemaPaths finds fields marked "x-pii": "<category>" in a JSON Schema,
// resolving local $ref against defs (the WD's types).
func SchemaPaths(schema any, defs map[string]any) []Path {
	var out []Path
	var walk func(s any, prefix []string, depth int)
	walk = func(s any, prefix []string, depth int) {
		m, ok := s.(map[string]any)
		if !ok || depth > 32 {
			return
		}
		if ref, ok := m["$ref"].(string); ok {
			if name, ok := strings.CutPrefix(ref, "#/types/"); ok {
				walk(defs[name], prefix, depth+1)
			}
			return
		}
		if cat, ok := m["x-pii"].(string); ok && cat != "" {
			out = append(out, Path{Segments: append([]string(nil), prefix...), Category: cat})
		}
		if props, ok := m["properties"].(map[string]any); ok {
			for k, sub := range props {
				walk(sub, append(prefix, k), depth+1)
			}
		}
		if items, ok := m["items"]; ok {
			walk(items, append(prefix, "*"), depth+1)
		}
	}
	walk(schema, nil, 0)
	return out
}

// Taint records plaintext values known to be personal data in a run, so
// copies of them elsewhere (an approval subject, a transform) are sealed too.
type Taint map[string]string // canonical scalar -> category

func scalarKey(v any) (string, bool) {
	switch t := v.(type) {
	case string:
		if t == "" {
			return "", false
		}
		return "s:" + t, true
	case int64:
		return "n:" + strconv.FormatInt(t, 10), true
	case float64:
		return "n:" + strconv.FormatFloat(t, 'g', -1, 64), true
	}
	return "", false
}

// Add records a personal value.
func (t Taint) Add(v any, category string) {
	if k, ok := scalarKey(v); ok {
		t[k] = category
	}
}

// Seal replaces values at the declared paths, and anywhere a tainted value
// appears, with envelopes. It returns the new value; v is not modified.
func Seal(ctx context.Context, c Cipher, tx pgx.Tx, tenant uuid.UUID, v any, paths []Path, taint Taint) (any, error) {
	if c == nil {
		return v, nil
	}
	v = deepCopy(v)
	for _, p := range paths {
		var err error
		v, err = sealAt(ctx, c, tx, tenant, v, p.Segments, p.Category, taint)
		if err != nil {
			return nil, err
		}
	}
	if len(taint) == 0 {
		return v, nil
	}
	return sealTainted(ctx, c, tx, tenant, v, taint)
}

func sealAt(ctx context.Context, c Cipher, tx pgx.Tx, tenant uuid.UUID, v any, segs []string, cat string, taint Taint) (any, error) {
	if len(segs) == 0 {
		if v == nil || IsEnvelope(v) {
			return v, nil
		}
		if list, ok := v.([]any); ok {
			// A declared list of values (recipients' numbers): each is sealed.
			for i := range list {
				s, err := sealAt(ctx, c, tx, tenant, list[i], nil, cat, taint)
				if err != nil {
					return nil, err
				}
				list[i] = s
			}
			return list, nil
		}
		if _, ok := scalarKey(v); !ok {
			return v, nil // only scalars are sealed; objects keep their shape
		}
		taint.Add(v, cat)
		return c.SealTx(ctx, tx, tenant, cat, v)
	}
	switch t := v.(type) {
	case map[string]any:
		child, ok := t[segs[0]]
		if !ok {
			return v, nil
		}
		s, err := sealAt(ctx, c, tx, tenant, child, segs[1:], cat, taint)
		if err != nil {
			return nil, err
		}
		t[segs[0]] = s
	case []any:
		if segs[0] != "*" {
			return v, nil
		}
		for i := range t {
			s, err := sealAt(ctx, c, tx, tenant, t[i], segs[1:], cat, taint)
			if err != nil {
				return nil, err
			}
			t[i] = s
		}
	}
	return v, nil
}

func sealTainted(ctx context.Context, c Cipher, tx pgx.Tx, tenant uuid.UUID, v any, taint Taint) (any, error) {
	switch t := v.(type) {
	case map[string]any:
		if IsEnvelope(t) {
			return t, nil
		}
		for k, x := range t {
			s, err := sealTainted(ctx, c, tx, tenant, x, taint)
			if err != nil {
				return nil, err
			}
			t[k] = s
		}
		return t, nil
	case []any:
		for i, x := range t {
			s, err := sealTainted(ctx, c, tx, tenant, x, taint)
			if err != nil {
				return nil, err
			}
			t[i] = s
		}
		return t, nil
	}
	if k, ok := scalarKey(v); ok {
		if cat, hit := taint[k]; hit {
			return c.SealTx(ctx, tx, tenant, cat, v)
		}
	}
	return v, nil
}

// Open replaces every envelope with its plaintext (or Erased), recording
// the plaintext values in taint.
func Open(ctx context.Context, c Cipher, tx pgx.Tx, tenant uuid.UUID, v any, taint Taint) (any, error) {
	switch t := v.(type) {
	case map[string]any:
		if IsEnvelope(t) {
			if c == nil {
				return nil, fmt.Errorf("sealed value but no cipher configured")
			}
			pt, err := c.OpenTx(ctx, tx, tenant, t)
			if errors.Is(err, ErrErased) {
				return Erased, nil
			}
			if err != nil {
				return nil, err
			}
			cat, _ := t["$pii"].(string)
			taint.Add(pt, cat)
			return pt, nil
		}
		out := make(map[string]any, len(t))
		for k, x := range t {
			o, err := Open(ctx, c, tx, tenant, x, taint)
			if err != nil {
				return nil, err
			}
			out[k] = o
		}
		return out, nil
	case []any:
		out := make([]any, len(t))
		for i, x := range t {
			o, err := Open(ctx, c, tx, tenant, x, taint)
			if err != nil {
				return nil, err
			}
			out[i] = o
		}
		return out, nil
	}
	return v, nil
}

// HasEnvelopes reports whether v contains any sealed value.
func HasEnvelopes(v any) bool {
	switch t := v.(type) {
	case map[string]any:
		if IsEnvelope(t) {
			return true
		}
		for _, x := range t {
			if HasEnvelopes(x) {
				return true
			}
		}
	case []any:
		for _, x := range t {
			if HasEnvelopes(x) {
				return true
			}
		}
	}
	return false
}

func deepCopy(v any) any {
	switch t := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, x := range t {
			out[k] = deepCopy(x)
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, x := range t {
			out[i] = deepCopy(x)
		}
		return out
	}
	return v
}
