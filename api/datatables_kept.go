package api

import (
	"bytes"
	"context"
	"encoding/json"
	"slices"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/datatables"
	"github.com/israel-duff/taskiem/engine/db"
)

// Kept tables (datatables.ErrKept; docs/data-tables.md#kept-tables): the
// rows API, the schema routes, chat and the storefront refuse them where
// they act (datatables.go, datatables_rows.go, whatsapp_tables.go,
// storefront.go); a workflow step is refused by the worker when it runs
// (engine/runtime, datatables.GuardStep). keptProblems adds the editor's
// warning for a step that names a kept table outright, so the person
// learns before publishing rather than from a failed run.

// keptWrite is a step that writes a data table, as written in the
// definition.
type keptWrite struct {
	path, action, table string
}

// keptProblems are the problems of the xenibase@1 steps on the
// data_tables connection that would write a table Taskiem keeps: a row
// write naming it outright, or call_function while the tenant keeps any.
func (s *Server) keptProblems(ctx context.Context, tenant uuid.UUID, doc []byte) []problem {
	if s.Store == nil || s.Store.Pool == nil || !bytes.Contains(doc, []byte("xenibase@1")) {
		return nil
	}
	var v any
	if json.Unmarshal(doc, &v) != nil {
		return nil
	}
	writes := keptWrites(v, "")
	if len(writes) == 0 {
		return nil
	}
	var kept []string
	err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT name FROM data_tables WHERE kept_by IS NOT NULL ORDER BY name`)
		if err != nil {
			return err
		}
		kept, err = pgx.CollectRows(rows, pgx.RowTo[string])
		return err
	})
	if err != nil || len(kept) == 0 {
		return nil // the worker refuses at run time either way
	}
	var out []problem
	for _, w := range writes {
		switch {
		case w.action == "call_function":
			out = append(out, problem{Path: w.path + "/action", Message: "call_function on the data_tables connection is refused while Taskiem keeps tables there (" +
				strings.Join(kept, ", ") + "); write rows with insert_rows, upsert_rows, update_rows or delete_rows"})
		case slices.Contains(kept, w.table):
			out = append(out, problem{Path: w.path + "/input/table", Message: "the table " + w.table + " is kept by Taskiem: rows are added by Taskiem and cannot be changed or deleted, so " +
				w.action + " on it is refused. Read it with read_rows, query_rows or get_row; corrections are new rows that Taskiem adds."})
		}
	}
	return out
}

// keptWrites are the steps under v (at JSON pointer path) that write a
// data table: xenibase@1 on data_tables with a row write or
// call_function, the table as written (empty when it is an expression).
func keptWrites(v any, path string) []keptWrite {
	var out []keptWrite
	switch n := v.(type) {
	case map[string]any:
		action, _ := n["action"].(string)
		if n["connector"] == "xenibase@1" && n["connection"] == datatables.ConnectionName && datatables.WritesRows(action) {
			table := ""
			if in, ok := n["input"].(map[string]any); ok {
				if t, ok := in["table"].(string); ok && !strings.HasPrefix(t, "=") {
					table = strings.TrimPrefix(strings.ToLower(strings.TrimSpace(t)), "public.")
				}
			}
			out = append(out, keptWrite{path: path, action: action, table: table})
		}
		keys := make([]string, 0, len(n))
		for k := range n {
			keys = append(keys, k)
		}
		slices.Sort(keys)
		for _, k := range keys {
			out = append(out, keptWrites(n[k], path+"/"+strings.NewReplacer("~", "~0", "/", "~1").Replace(k))...)
		}
	case []any:
		for i, c := range n {
			out = append(out, keptWrites(c, path+"/"+strconv.Itoa(i))...)
		}
	}
	return out
}
