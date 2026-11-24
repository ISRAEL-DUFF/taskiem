package datatables

import (
	"context"
	"errors"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Sealing a data table's rows column by column (decided by the founder on
// 24 November 2026; docs/privacy.md#data-tables-in-a-run): a workflow step
// that reads or writes a business's own table through the data_tables
// connection seals only the table's personal columns in the run's history,
// so a product's name or a ticket's title shows in the run and in a tool's
// answer, while a customer's phone stays sealed and masked. Which columns
// are personal is read from the table's definition when the step's input
// or outcome is written, so a column made personal later is sealed in
// every row read after that.
//
// Everything else keeps the connector's declaration, which seals the rows
// whole: a table Taskiem keeps (its own records), a table it has no
// definition of, a schema other than public, a select that renames, casts
// or embeds, another xenibase connection, a run outside prod,
// call_function, and every other connector's rows.

// RowPlace is where a step's rows are in its input or output (from the
// input or output itself, a list of rows or one row), and which of a
// row's fields are sealed there: a personal column, or one the table's
// definition does not name.
type RowPlace struct {
	Segments []string
	Personal func(field string) bool
}

// alwaysThere are the columns every table has (reservedNames), none of
// them personal.
var alwaysThere = []string{"id", "created_at", "updated_at"}

// rowInputs and rowOutputs are where each xenibase@1 action carries rows,
// the places its manifest declares personal.
var (
	rowInputs = map[string][][]string{
		"insert_rows": {{"rows"}, {"row"}},
		"upsert_rows": {{"rows"}, {"row"}},
		"update_rows": {{"set"}},
	}
	rowOutputs = map[string][][]string{
		"query_rows":  {{"rows"}},
		"read_rows":   {{"rows"}},
		"get_row":     {{"row"}},
		"insert_rows": {{"rows"}},
		"upsert_rows": {{"rows"}},
		"update_rows": {{"rows"}},
	}
)

// StepRows says where a xenibase@1 step's rows are sealed column by
// column: in its input (output false) or in its output. ok is false when
// the step's rows are sealed whole, as the connector declares (see
// above). connection and environment are the step's; input is its
// resolved input. tx must have the tenant in scope.
func StepRows(ctx context.Context, tx pgx.Tx, connection, environment, action string, input any, output bool) (places []RowPlace, ok bool, err error) {
	wanted := rowInputs[action]
	if output {
		wanted = rowOutputs[action]
	}
	if len(wanted) == 0 || connection != ConnectionName || environment != Environment {
		return nil, false, nil
	}
	in, _ := input.(map[string]any)
	name, isText := in["table"].(string)
	if !isText {
		return nil, false, nil
	}
	name = strings.TrimPrefix(name, "public.")
	if !nameRe.MatchString(name) || !plainSelect(in["select"]) {
		return nil, false, nil
	}
	// The connection must be the one the data tables project is reached
	// through.
	var ours bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM connections c JOIN data_table_projects p ON p.connection_id = c.id
		WHERE c.environment = $1 AND c.connector = $2 AND c.name = $3)`, environment, Connector, connection).Scan(&ours); err != nil || !ours {
		return nil, false, err
	}
	id, err := tableID(ctx, tx, name)
	if err != nil || id == uuid.Nil {
		return nil, false, err
	}
	t, err := GetTable(ctx, tx, id)
	if errors.Is(err, ErrNotFound) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	if t.Kept() {
		return nil, false, nil // Taskiem's own records keep their sealing
	}
	open := map[string]bool{}
	for _, k := range alwaysThere {
		open[k] = true
	}
	for _, c := range t.Columns {
		if !c.Personal {
			open[c.Name] = true
		}
	}
	personal := func(field string) bool { return !open[field] }
	for _, segs := range wanted {
		places = append(places, RowPlace{Segments: segs, Personal: personal})
	}
	return places, true, nil
}

// plainSelect reports whether a step's select names only columns, or is
// absent: a select that renames a column ("name:phone"), casts it or
// embeds another table's rows would put values under names the table's
// definition does not describe.
func plainSelect(v any) bool {
	if v == nil {
		return true
	}
	s, ok := v.(string)
	if !ok {
		return false
	}
	if strings.TrimSpace(s) == "" {
		return true
	}
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		if part != "*" && !nameRe.MatchString(part) {
			return false
		}
	}
	return true
}
