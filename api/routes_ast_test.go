package api_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"maps"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// routeMeta is what the router's source says about one route: who may
// call it, which plan feature it needs, and whether an API key limited to
// one environment is refused. It is read from the Go source (server.go and
// the route functions it calls) so the OpenAPI document's x-taskiem-*
// extensions cannot drift from the code.
type routeMeta struct {
	Auth        string   // "none", "member" (session or API key), "embed" (end-user token), "partner" (all-environment API key)
	Permissions []string // permissions the route needs (s.need)
	Features    []string // plan features the route needs (s.feature, s.partnerPlan)
	TenantWide  bool     // refused for an API key limited to one environment (s.tenantWide)
	Handler     string   // the handler's source, for messages
}

// routeSource extracts every route the API declares with its middleware,
// keyed "METHOD /path" with chi's patterns. It understands the shapes the
// router uses: r.Use, r.With(...).Method, r.Route, r.Group, r.Mount (not
// followed: mounted handlers are edge routes) and if blocks.
// Markers on a feature: needed only by writes, or only under /domains.
const (
	writesOnly  = "?writes"
	domainsOnly = "?domains"
)

func routeSource(t *testing.T) map[string]routeMeta {
	t.Helper()
	fset := token.NewFileSet()
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	consts := map[string]string{}
	funcs := map[string]*ast.FuncDecl{}
	addConsts := func(f *ast.File, pkg string) {
		for _, d := range f.Decls {
			gd, ok := d.(*ast.GenDecl)
			if !ok || gd.Tok != token.CONST {
				continue
			}
			for _, sp := range gd.Specs {
				vs := sp.(*ast.ValueSpec)
				for i, n := range vs.Names {
					if i < len(vs.Values) {
						if bl, ok := vs.Values[i].(*ast.BasicLit); ok && bl.Kind == token.STRING {
							v, _ := strconv.Unquote(bl.Value)
							consts[pkg+n.Name] = v
						}
					}
				}
			}
		}
	}
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		addConsts(f, "")
		for _, d := range f.Decls {
			if fd, ok := d.(*ast.FuncDecl); ok && fd.Recv != nil {
				funcs[fd.Name.Name] = fd
			}
		}
	}
	billingFiles, _ := filepath.Glob("../engine/billing/*.go")
	for _, name := range billingFiles {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		addConsts(f, "billing.")
	}

	out := map[string]routeMeta{}
	type scope struct {
		prefix string
		mw     routeMeta
		locals map[string]routeMeta // idents bound to middleware (read, manage := s.need(...), ...)
	}
	mwOf := func(sc *scope, e ast.Expr) routeMeta {
		var m routeMeta
		switch x := e.(type) {
		case *ast.Ident:
			return sc.locals[x.Name]
		case *ast.SelectorExpr: // s.authenticate, s.tenantWide, ...
			switch x.Sel.Name {
			case "authenticate":
				m.Auth = "member"
			case "embedAuth":
				m.Auth = "embed"
			case "partnerKey":
				m.Auth = "partner"
			case "tenantWide":
				m.TenantWide = true
			case "partnerPlan":
				// The embedded feature, and white-label for writes to an
				// app's custom domains.
				m.Features = []string{"embedded", "white_label" + writesOnly + domainsOnly}
			}
		case *ast.CallExpr:
			sel, ok := x.Fun.(*ast.SelectorExpr)
			if !ok {
				break
			}
			arg := func() string {
				switch a := x.Args[0].(type) {
				case *ast.Ident:
					if v, ok := consts[a.Name]; ok {
						return v
					}
					t.Fatalf("unknown constant %s", a.Name)
				case *ast.SelectorExpr:
					if v, ok := consts[a.X.(*ast.Ident).Name+"."+a.Sel.Name]; ok {
						return v
					}
					t.Fatalf("unknown constant %s.%s", a.X.(*ast.Ident).Name, a.Sel.Name)
				case *ast.BasicLit:
					v, _ := strconv.Unquote(a.Value)
					return v
				}
				t.Fatalf("unexpected middleware argument at %s", fset.Position(x.Pos()))
				return ""
			}
			switch sel.Sel.Name {
			case "need":
				m.Permissions = []string{arg()}
			case "feature":
				// feature(name, writesOnly): with writesOnly, reads pass.
				f := arg()
				if wo, ok := x.Args[1].(*ast.Ident); ok && wo.Name == "true" {
					f += writesOnly
				}
				m.Features = []string{f}
			}
		}
		return m
	}
	merge := func(a routeMeta, bs ...routeMeta) routeMeta {
		a.Permissions = slices.Clone(a.Permissions)
		a.Features = slices.Clone(a.Features)
		for _, b := range bs {
			if b.Auth != "" {
				a.Auth = b.Auth
			}
			a.Permissions = append(a.Permissions, b.Permissions...)
			a.Features = append(a.Features, b.Features...)
			a.TenantWide = a.TenantWide || b.TenantWide
		}
		return a
	}
	methods := map[string]string{"Get": "GET", "Post": "POST", "Put": "PUT", "Delete": "DELETE", "Patch": "PATCH"}
	var walk func(sc scope, body []ast.Stmt)
	follow := func(sc scope, fn ast.Expr) {
		switch f := fn.(type) {
		case *ast.FuncLit:
			walk(sc, f.Body.List)
		case *ast.SelectorExpr:
			fd, ok := funcs[f.Sel.Name]
			if !ok {
				t.Fatalf("route function %s not found", f.Sel.Name)
			}
			walk(sc, fd.Body.List)
		default:
			t.Fatalf("unexpected route function at %s", fset.Position(fn.Pos()))
		}
	}
	walk = func(sc scope, body []ast.Stmt) {
		sc.locals = maps.Clone(sc.locals)
		if sc.locals == nil {
			sc.locals = map[string]routeMeta{}
		}
		for _, st := range body {
			switch s := st.(type) {
			case *ast.IfStmt:
				walk(sc, s.Body.List)
				continue
			case *ast.AssignStmt:
				for i, lhs := range s.Lhs {
					if id, ok := lhs.(*ast.Ident); ok && i < len(s.Rhs) {
						if c, ok := s.Rhs[i].(*ast.CallExpr); ok {
							if sel, ok := c.Fun.(*ast.SelectorExpr); ok && (sel.Sel.Name == "need" || sel.Sel.Name == "feature") {
								sc.locals[id.Name] = mwOf(&sc, c)
							}
						}
					}
				}
				continue
			}
			es, ok := st.(*ast.ExprStmt)
			if !ok {
				continue
			}
			call, ok := es.X.(*ast.CallExpr)
			if !ok {
				continue
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				continue
			}
			// r.With(a, b).Get(...): middleware from With, then the call.
			mw := sc.mw
			if inner, ok := sel.X.(*ast.CallExpr); ok {
				if isel, ok := inner.Fun.(*ast.SelectorExpr); ok && isel.Sel.Name == "With" {
					for _, a := range inner.Args {
						mw = merge(mw, mwOf(&sc, a))
					}
				}
			} else if id, ok := sel.X.(*ast.Ident); !ok || id.Name != "r" {
				continue
			}
			path := func() string {
				bl, ok := call.Args[0].(*ast.BasicLit)
				if !ok {
					t.Fatalf("route path is not a literal at %s", fset.Position(call.Pos()))
				}
				p, _ := strconv.Unquote(bl.Value)
				return p
			}
			switch name := sel.Sel.Name; name {
			case "Use":
				for _, a := range call.Args {
					sc.mw = merge(sc.mw, mwOf(&sc, a))
				}
			case "Route":
				follow(scope{prefix: sc.prefix + path(), mw: mw, locals: sc.locals}, call.Args[1])
			case "Group":
				follow(scope{prefix: sc.prefix, mw: mw, locals: sc.locals}, call.Args[0])
			default:
				if m, ok := methods[name]; ok {
					meta := merge(mw)
					meta.Handler = types.ExprString(call.Args[1])
					if meta.Auth == "" {
						meta.Auth = "none"
					}
					p := sc.prefix + path()
					if strings.HasSuffix(p, "/") && p != "/" {
						p = strings.TrimSuffix(p, "/")
					}
					var features []string
					for _, f := range meta.Features {
						f, dom := strings.CutSuffix(f, domainsOnly)
						f, wo := strings.CutSuffix(f, writesOnly)
						if (wo && m == "GET") || (dom && !strings.Contains(p, "/domains")) {
							continue
						}
						features = append(features, f)
					}
					meta.Features = features
					out[m+" "+p] = meta
				}
			}
		}
	}
	walk(scope{}, funcs["Handler"].Body.List)
	return out
}
