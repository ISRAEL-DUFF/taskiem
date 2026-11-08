package api

import (
	"encoding/json"
	"maps"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/israel-duff/taskiem/engine/expr"
	"github.com/israel-duff/taskiem/engine/wd"
	"github.com/israel-duff/taskiem/templates"
)

// Tables a step names by expression (the ticket kit's, which choose the
// customers', HR's or HR's confidential tables by the desk or by the
// reference's prefix). Readiness checks each table such a step can touch:
// the expression is read through the transforms it depends on, which a
// template step's input fills (literal parameters, such as desk: hr), and
// what only a run knows (a form's answer, an AI step's choice) is tried
// with every value the expression itself compares it with. A table name
// no value gives is not one the step can touch. An expression this cannot
// read gives the tables of the template the step came from.

// tableNameRe is a data table's name (datatables.CheckName).
var tableNameRe = regexp.MustCompile(`^[a-z][a-z0-9_]{0,62}$`)

var (
	// stepField is a read of a step's output field: steps.x.output.f, with
	// optional selects (steps.?x, .?f).
	stepField = regexp.MustCompile(`(^|[^.\w])steps\s*\.\s*\??\s*([A-Za-z_]\w*)\s*\.\s*output\s*\.\s*(\??)\s*([A-Za-z_]\w*)`)
	// branchField is a read of a step's output inside a branch's results
	// (a template step's body, whose results are its steps' outputs by
	// their ids): steps.b.output.steps.x.f.
	branchField = regexp.MustCompile(`(^|[^.\w])steps\s*\.\s*\??\s*([A-Za-z_]\w*)\s*\.\s*output\s*\.\s*steps\s*\.\s*\??\s*([A-Za-z_]\w*)\s*\.\s*(\??)\s*([A-Za-z_]\w*)`)
	// leafPath is a read of what only a run knows.
	leafPath = regexp.MustCompile(`(^|[^.\w])((?:steps|trigger|run|env)(?:\s*\.\s*\??\s*[A-Za-z_]\w*|\s*\[[^\]]*\])+)`)
	// literal is a quoted string in an expression.
	literal = regexp.MustCompile(`'((?:[^'\\]|\\.)*)'|"((?:[^"\\]|\\.)*)"`)
)

const (
	maxInline = 8 // rounds of reading through transforms
	maxSource = 1 << 15
	maxTries  = 4096 // evaluations of one expression
)

// tablesOf are the tables a step whose table is the expression src can
// touch, in d (expanded).
func tablesOf(e *expr.Engine, d *wd.Definition, st *wd.Step, src string) []string {
	if got, ok := expressionTables(e, d, src); ok {
		return got
	}
	if st == nil || st.Origin == nil {
		return nil
	}
	t, ok := templates.Default().Get(st.Origin.Template)
	if !ok {
		return nil
	}
	var out []string
	for _, tb := range t.TablesFor(nil) {
		if tableNameRe.MatchString(tb.Name) {
			out = append(out, tb.Name)
		}
	}
	return out
}

// expressionTables reads src through d's transforms, one level more each
// time, and tries it with every value it compares an unknown with: the
// tables of the deepest reading that still gives some (each level only
// narrows them; past some, the expression grows too large to try). ok is
// false when none does.
func expressionTables(e *expr.Engine, d *wd.Definition, src string) ([]string, bool) {
	src = strings.TrimPrefix(src, "=")
	var best []string
	for range maxInline {
		if got, ok := tryTables(e, src); ok {
			best = got
		}
		next := inlineTransforms(d, src)
		if next == src || len(next) > maxSource {
			break
		}
		src = next
	}
	if got, ok := tryTables(e, src); ok && len(src) <= maxSource {
		best = got
	}
	return best, len(best) > 0
}

// tryTables evaluates src with every value it compares an unknown with,
// and returns the table names it gives.
func tryTables(e *expr.Engine, src string) ([]string, bool) {
	leaves := map[string]bool{}
	for _, m := range leafPath.FindAllStringSubmatchIndex(src, -1) {
		path := src[m[4]:m[5]]
		// A method call is not a field: x.upperAscii() reads x.
		if rest := strings.TrimLeft(src[m[5]:], " "); strings.HasPrefix(rest, "(") {
			if i := strings.LastIndexByte(path, '.'); i > 0 {
				path = path[:i]
			}
		}
		path = strings.Join(strings.Fields(path), "")
		if strings.Contains(path, "[") && !indexed.MatchString(path) {
			return nil, false
		}
		leaves[path] = true
	}
	paths := make([]string, 0, len(leaves))
	for p := range leaves {
		// A leaf another one reads inside is a map holding it.
		if !slices.ContainsFunc(slices.Collect(maps.Keys(leaves)), func(o string) bool { return o != p && strings.HasPrefix(o, p+".") }) {
			paths = append(paths, p)
		}
	}
	sort.Strings(paths)
	values := []any{"", int64(0)}
	for _, m := range literal.FindAllStringSubmatch(src, -1) {
		v := m[1] + m[2]
		if !slices.Contains(values, any(v)) {
			values = append(values, v)
		}
	}
	tries := 1
	for range paths {
		if tries *= len(values); tries > maxTries {
			return nil, false
		}
	}
	var out []string
	pick := make([]int, len(paths))
	for {
		act := map[string]any{}
		for i, p := range paths {
			setPath(act, pathOf(p), values[pick[i]])
		}
		if v, err := e.Eval(src, act); err == nil {
			if s, ok := v.(string); ok && tableNameRe.MatchString(s) && !slices.Contains(out, s) {
				out = append(out, s)
			}
		}
		i := 0
		for ; i < len(pick); i++ {
			if pick[i]++; pick[i] < len(values) {
				break
			}
			pick[i] = 0
		}
		if i == len(pick) {
			break
		}
	}
	sort.Strings(out)
	return out, len(out) > 0
}

// indexed is a leaf path whose brackets are list indexes: rows[0].id.
var indexed = regexp.MustCompile(`^[^\[]*(\[\d+\][^\[]*)*$`)

// pathOf splits a leaf path into keys and list indexes (ints).
func pathOf(p string) []any {
	var out []any
	for _, seg := range strings.Split(strings.ReplaceAll(p, "?", ""), ".") {
		name, rest, _ := strings.Cut(seg, "[")
		out = append(out, name)
		for rest != "" {
			n, after, _ := strings.Cut(rest, "]")
			i, _ := strconv.Atoi(n)
			out = append(out, i)
			_, rest, _ = strings.Cut(after, "[")
		}
	}
	return out
}

// inlineTransforms replaces each read of a transform's output field with
// what the transform gives it, also inside a template step's body branch.
func inlineTransforms(d *wd.Definition, src string) string {
	src = branchField.ReplaceAllStringFunc(src, func(m string) string {
		sub := branchField.FindStringSubmatch(m)
		b := d.Step(sub[2])
		if b == nil || b.Branch == nil {
			return m
		}
		for _, path := range b.Branch.Paths {
			for _, st := range path.Steps {
				if st.Key() == sub[3] {
					return inlineField(st, m, sub[1], sub[4] == "?", sub[5])
				}
			}
		}
		return m
	})
	return stepField.ReplaceAllStringFunc(src, func(m string) string {
		sub := stepField.FindStringSubmatch(m)
		return inlineField(d.Step(sub[2]), m, sub[1], sub[3] == "?", sub[4])
	})
}

// inlineField is what transform st gives its output field f, read as m
// (after lead), or m when st is not a transform.
func inlineField(st *wd.Step, m, lead string, optional bool, f string) string {
	if st == nil || st.Transform == nil {
		return m
	}
	out, ok := st.Transform.Output.(map[string]any)
	if !ok {
		return m
	}
	v, has := out[f]
	var lit string
	switch x := v.(type) {
	case nil:
		if !has && optional {
			return lead + "optional.none()"
		}
		return m
	case string:
		if strings.HasPrefix(x, "=") {
			lit = "(" + x[1:] + ")"
		} else {
			lit = strconv.Quote(x)
		}
	case bool, float64, int, int64, json.Number:
		b, _ := json.Marshal(x)
		lit = string(b)
	default:
		return m
	}
	if optional {
		lit = "optional.of(" + lit + ")"
	}
	return lead + lit
}

// setPath sets v at path (keys and list indexes) in m.
func setPath(m map[string]any, path []any, v any) {
	var put func(cur any, i int) any
	put = func(cur any, i int) any {
		if i == len(path) {
			return v
		}
		switch k := path[i].(type) {
		case int:
			list, _ := cur.([]any)
			for len(list) <= k {
				list = append(list, nil)
			}
			list[k] = put(list[k], i+1)
			return list
		default:
			mm, _ := cur.(map[string]any)
			if mm == nil {
				mm = map[string]any{}
			}
			mm[k.(string)] = put(mm[k.(string)], i+1)
			return mm
		}
	}
	for k, x := range put(m, 0).(map[string]any) {
		m[k] = x
	}
}
