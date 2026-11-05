package sqlcheck_test

import (
	"path/filepath"
	"testing"

	"github.com/israel-duff/taskiem/tools/sqlcheck"
)

// The checker flags SQL built from a value, through Sprintf or a Batch,
// and passes constants, constant expressions and marked lines.
func TestCheckRules(t *testing.T) {
	found, err := sqlcheck.Check(".", "./testdata/demo")
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, f := range found {
		got = append(got, filepath.Base(f.Pos.Filename)+":"+f.Call)
		if f.Pos.Line < 17 || f.Pos.Line > 19 {
			t.Errorf("unexpected finding %s", f)
		}
	}
	if len(got) != 3 || got[0] != "demo.go:Exec" || got[1] != "demo.go:QueryRow" || got[2] != "demo.go:Batch.Queue" {
		t.Errorf("findings %v", got)
	}
}

// No SQL in the repository is built at run time, except where a marked
// line says why every piece is fixed in code.
func TestRepositorySQLIsConstant(t *testing.T) {
	if testing.Short() {
		t.Skip("type-checks the whole repository")
	}
	found, err := sqlcheck.Check("../..", "./...")
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range found {
		t.Error(f)
	}
}
