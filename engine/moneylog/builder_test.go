package moneylog

import (
	"slices"
	"strings"
	"testing"

	"github.com/israel-duff/taskiem/engine/ai/builder"
	"github.com/israel-duff/taskiem/engine/datatables"
)

// The AI builder names the money log's tables and their columns without
// importing this package; they must stay the same.
func TestBuilderKnowsTheMoneyLog(t *testing.T) {
	if !slices.Equal(builder.MoneyLogTables, []string{Table, MatchesTable}) {
		t.Fatalf("builder.MoneyLogTables %v, want %s and %s", builder.MoneyLogTables, Table, MatchesTable)
	}
	colNames := func(cols []datatables.Column) []string {
		var out []string
		for _, c := range cols {
			out = append(out, c.Name)
		}
		return out
	}
	text := builder.Instructions()
	for _, cols := range [][]string{colNames(Columns), colNames(MatchColumns)} {
		for _, c := range cols {
			if !strings.Contains(text, c) {
				t.Errorf("the builder's instructions do not name the column %s", c)
			}
		}
	}
}
