package runtime

import (
	"context"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/pii"
)

// tablePIIPaths are the places a Xenibase row action carries the
// personal columns of the business's data table it names (decision 0062
// V5, A5). The manifest can only declare whole rows personal, and only
// values are sealed, never objects; so the columns the table marks
// personal (and its phone and email columns) are sealed cell by cell:
// in rows written and read, a row's set, and filter values. Read back, a
// conversation's text is then known as personal in the run, and masked
// before a model sees it like any other personal value.
func tablePIIPaths(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, connectorRef, action string, input any, output bool) ([]pii.Path, error) {
	if !strings.HasPrefix(connectorRef, "xenibase@") && connectorRef != "xenibase" {
		return nil, nil
	}
	in, _ := input.(map[string]any)
	table, _ := in["table"].(string)
	if schema, _ := in["schema"].(string); table == "" || (schema != "" && schema != "public") {
		return nil, nil
	}
	cols, err := personalColumns(ctx, tx, tenant, table)
	if err != nil || len(cols) == 0 {
		return nil, err
	}
	var out []pii.Path
	add := func(prefix []string, col, cat string) {
		segs := append(append([]string{}, prefix...), col)
		out = append(out, pii.Path{Segments: segs, Category: cat})
	}
	side := "input"
	if output {
		side = "output"
	}
	for col, cat := range cols {
		switch {
		case output && (action == "read_rows" || action == "query_rows" || action == "insert_rows" || action == "upsert_rows" || action == "update_rows" || action == "delete_rows"):
			add([]string{side, "rows", "*"}, col, cat)
		case output && action == "get_row":
			add([]string{side, "row"}, col, cat)
		case !output && (action == "insert_rows" || action == "upsert_rows"):
			add([]string{side, "rows", "*"}, col, cat)
			add([]string{side, "row"}, col, cat)
		case !output && action == "update_rows":
			add([]string{side, "set"}, col, cat)
		}
	}
	if !output {
		// A filter on a personal column carries a personal value; which
		// filter is on which column cannot be told by place, so every
		// filter value of such a table is sealed.
		out = append(out, pii.Path{Segments: []string{"input", "filters", "*", "value"}, Category: "other"},
			pii.Path{Segments: []string{"input", "filters", "*", "values"}, Category: "other"})
	}
	return out, nil
}

// personalColumns are a data table's personal columns and their
// categories: those marked personal, and phone and email columns.
func personalColumns(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, table string) (map[string]string, error) {
	rows, err := tx.Query(ctx, `SELECT c.name, c.type FROM data_table_columns c JOIN data_tables t ON t.id = c.table_id
		WHERE t.tenant_id = $1 AND t.name = $2 AND (c.personal OR c.type IN ('phone', 'email'))`, tenant, table)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var name, typ string
		if err := rows.Scan(&name, &typ); err != nil {
			return nil, err
		}
		cat := "other"
		if typ == "phone" || typ == "email" {
			cat = typ
		}
		out[name] = cat
	}
	return out, rows.Err()
}
