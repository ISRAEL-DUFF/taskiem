package expr

import (
	"cel.dev/cel-go/cel"
	celast "cel.dev/cel-go/common/ast"
)

func refsOf(a *cel.Ast) Refs {
	r := Refs{Roots: map[string]bool{}, Steps: map[string]bool{}, Secrets: map[string]bool{}}
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
				}
			}
		case celast.CallKind:
			// steps["id"] index form
			c := e.AsCall()
			if c.FunctionName() == "_[_]" && len(c.Args()) == 2 {
				if op := c.Args()[0]; op.Kind() == celast.IdentKind && (op.AsIdent() == "steps" || op.AsIdent() == "secrets") {
					if lit := c.Args()[1]; lit.Kind() == celast.LiteralKind {
						if s, ok := lit.AsLiteral().Value().(string); ok {
							if op.AsIdent() == "steps" {
								r.Steps[s] = true
							} else {
								r.Secrets[s] = true
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
