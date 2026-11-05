// Package sqlcheck finds SQL built at run time (spec 5.3): every
// query a pgx-style Exec, Query or QueryRow call, or a pgx.Batch, is given
// must be a compile-time constant, so no value can become SQL text. Values
// go in as $n arguments. A call that has to assemble its SQL from fixed
// pieces (a list of column names, a choice of WHERE clause) carries a
// comment on its line or the line above:
//
//	//sqlcheck:built <why every piece is fixed in code>
//
// Run by TestRepositorySQLIsConstant, or by hand:
//
//	go run ./tools/sqlcheck ./...
package sqlcheck

import (
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"sort"
	"strings"

	"golang.org/x/tools/go/packages"
)

// Marker exempts a call whose SQL is assembled from fixed pieces.
const Marker = "//sqlcheck:built"

// Finding is a call whose SQL is not a constant.
type Finding struct {
	Pos  token.Position
	Call string
}

func (f Finding) String() string {
	return fmt.Sprintf("%s: %s is given SQL that is not a constant; pass values as $n arguments, or mark the line %s <why>", f.Pos, f.Call, Marker)
}

// Skipped are packages that never run against Taskiem's database in
// production: throwaway measurement code and test harnesses. Test files
// are not loaded either.
var Skipped = []string{
	"github.com/israel-duff/taskiem/engine/spike",
	"github.com/israel-duff/taskiem/tools/loadtest",
	"github.com/israel-duff/taskiem/engine/db/dbtest",
}

func skipped(path string) bool {
	for _, s := range Skipped {
		if path == s || strings.HasPrefix(path, s+"/") {
			return true
		}
	}
	return false
}

// Check loads the packages matching patterns (relative to dir), without
// their tests, and returns the calls whose SQL is not a constant.
func Check(dir string, patterns ...string) ([]Finding, error) {
	cfg := &packages.Config{Dir: dir,
		Mode: packages.NeedName | packages.NeedFiles | packages.NeedSyntax | packages.NeedTypes | packages.NeedTypesInfo}
	pkgs, err := packages.Load(cfg, patterns...)
	if err != nil {
		return nil, err
	}
	var errs []string
	seen := map[token.Position]bool{}
	var out []Finding
	for _, p := range pkgs {
		for _, e := range p.Errors {
			errs = append(errs, e.Error())
		}
		if skipped(p.PkgPath) {
			continue
		}
		for _, f := range p.Syntax {
			marked := markedLines(p.Fset, f)
			ast.Inspect(f, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				name, sql := sqlArg(p.TypesInfo, call)
				if sql == nil {
					return true
				}
				if tv, ok := p.TypesInfo.Types[sql]; ok && tv.Value != nil {
					return true // a constant
				}
				pos := p.Fset.Position(call.Pos())
				if marked[pos.Filename][pos.Line] || marked[pos.Filename][pos.Line-1] || seen[pos] {
					return true
				}
				seen[pos] = true
				out = append(out, Finding{Pos: pos, Call: name})
				return true
			})
		}
	}
	if len(errs) > 0 {
		return nil, fmt.Errorf("loading packages: %s", strings.Join(errs, "; "))
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Pos.Filename != out[j].Pos.Filename {
			return out[i].Pos.Filename < out[j].Pos.Filename
		}
		return out[i].Pos.Line < out[j].Pos.Line
	})
	return out, nil
}

// sqlArg returns the call's name and its SQL argument when it is a query
// call: a method Exec, Query or QueryRow whose parameters start with a
// context.Context and a string (pgx.Tx, pgxpool.Pool, pgx.Conn and the
// interfaces over them), or pgx.Batch's Queue.
func sqlArg(info *types.Info, call *ast.CallExpr) (string, ast.Expr) {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return "", nil
	}
	fn, ok := info.Uses[sel.Sel].(*types.Func)
	if !ok {
		return "", nil
	}
	sig, ok := fn.Type().(*types.Signature)
	if !ok || sig.Recv() == nil {
		return "", nil
	}
	params := sig.Params()
	switch fn.Name() {
	case "Exec", "Query", "QueryRow":
		if params.Len() < 2 || !isContext(params.At(0).Type()) || !isString(params.At(1).Type()) || len(call.Args) < 2 {
			return "", nil
		}
		return fn.Name(), call.Args[1]
	case "Queue":
		if fn.Pkg() == nil || fn.Pkg().Path() != "github.com/jackc/pgx/v5" || params.Len() < 1 || !isString(params.At(0).Type()) || len(call.Args) < 1 {
			return "", nil
		}
		return "Batch.Queue", call.Args[0]
	}
	return "", nil
}

func isContext(t types.Type) bool {
	n, ok := t.(*types.Named)
	return ok && n.Obj().Pkg() != nil && n.Obj().Pkg().Path() == "context" && n.Obj().Name() == "Context"
}

func isString(t types.Type) bool {
	b, ok := t.Underlying().(*types.Basic)
	return ok && b.Kind() == types.String
}

// markedLines lists, per file, the lines that carry the marker.
func markedLines(fset *token.FileSet, f *ast.File) map[string]map[int]bool {
	out := map[string]map[int]bool{}
	for _, g := range f.Comments {
		for _, c := range g.List {
			if strings.HasPrefix(c.Text, Marker) {
				p := fset.Position(c.Pos())
				if out[p.Filename] == nil {
					out[p.Filename] = map[int]bool{}
				}
				out[p.Filename][p.Line] = true
			}
		}
	}
	return out
}
