package expr

import (
	"slices"
	"strings"

	"cel.dev/cel-go/cel"
	celast "cel.dev/cel-go/common/ast"
)

func refsOf(a *cel.Ast) Refs {
	r := Refs{Roots: map[string]bool{}, Steps: map[string]bool{}, Secrets: map[string]bool{}, Env: map[string]bool{}}
	root := a.NativeRep().Expr()
	celast.PreOrderVisit(root, celast.NewExprVisitor(func(e celast.Expr) {
		switch e.Kind() {
		case celast.IdentKind:
			r.Roots[e.AsIdent()] = true
		case celast.SelectKind:
			s := e.AsSelect()
			if op := s.Operand(); op.Kind() == celast.IdentKind {
				switch op.AsIdent() {
				case "steps":
					r.Steps[s.FieldName()] = true
				case "secrets":
					r.Secrets[s.FieldName()] = true
				case "env":
					r.Env[s.FieldName()] = true
				}
			}
		case celast.CallKind:
			// steps["id"] index form
			c := e.AsCall()
			if c.FunctionName() == "_[_]" && len(c.Args()) == 2 {
				if op := c.Args()[0]; op.Kind() == celast.IdentKind {
					if lit := c.Args()[1]; lit.Kind() == celast.LiteralKind {
						if s, ok := lit.AsLiteral().Value().(string); ok {
							switch op.AsIdent() {
							case "steps":
								r.Steps[s] = true
							case "secrets":
								r.Secrets[s] = true
							case "env":
								r.Env[s] = true
							}
						}
					}
				}
			}
		}
	}))
	// Comprehension variables (e.g. b in list.filter(b, ...)) appear as
	// identifiers; keep only real roots.
	for k := range r.Roots {
		if !isRoot(k) {
			delete(r.Roots, k)
		}
	}
	return r
}

func isRoot(s string) bool {
	for _, r := range Roots {
		if r == s {
			return true
		}
	}
	return false
}

// Paths parses an expression and reports the field paths it reads from
// the given roots: input.customer.email is [input customer email]. An
// optional select (input.?source) and an index by a string literal
// (input["source"]) are fields too. Where one path extends another only
// the longer is kept, so output.balances.filter(...) reads
// [output balances].
func (e *Engine) Paths(src string, roots ...string) ([][]string, error) {
	src = strings.TrimPrefix(src, "=")
	ast, iss := e.env.Compile(src)
	if iss.Err() != nil {
		return nil, &Error{Expr: src, Err: iss.Err()}
	}
	var all [][]string
	celast.PreOrderVisit(ast.NativeRep().Expr(), celast.NewExprVisitor(func(x celast.Expr) {
		if p := pathOf(x); len(p) > 0 && slices.Contains(roots, p[0]) {
			all = append(all, p)
		}
	}))
	var out [][]string
	for i, p := range all {
		longer := false
		for j, q := range all {
			if i != j && len(q) > len(p) && slices.Equal(q[:len(p)], p) {
				longer = true
				break
			}
		}
		if !longer && !slices.ContainsFunc(out, func(o []string) bool { return slices.Equal(o, p) }) {
			out = append(out, p)
		}
	}
	return out, nil
}

// pathOf is the chain of fields an expression selects from an identifier,
// or nil when it is not one.
func pathOf(x celast.Expr) []string {
	switch x.Kind() {
	case celast.IdentKind:
		return []string{x.AsIdent()}
	case celast.SelectKind:
		s := x.AsSelect()
		if s.IsTestOnly() {
			return nil
		}
		if p := pathOf(s.Operand()); p != nil {
			return append(p, s.FieldName())
		}
	case celast.CallKind:
		c := x.AsCall()
		if (c.FunctionName() == "_[_]" || c.FunctionName() == "_?._" || c.FunctionName() == "_[?_]") && len(c.Args()) == 2 {
			if lit := c.Args()[1]; lit.Kind() == celast.LiteralKind {
				if f, ok := lit.AsLiteral().Value().(string); ok {
					if p := pathOf(c.Args()[0]); p != nil {
						return append(p, f)
					}
				}
			}
		}
	}
	return nil
}
