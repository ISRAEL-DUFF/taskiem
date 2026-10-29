package datatables

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/israel-duff/taskiem/engine/db"
)

// Kept tables (decision 0045 point 6; docs/spec/invoices-and-money-log.md
// 7.1; docs/data-tables.md#kept-tables): a data table whose rows only
// Taskiem's own writers add, such as the money log. People and workflows
// read, search and export it like any table; the page, the rows API,
// chat, the storefront and workflow steps refuse to add, change or delete
// its rows, and its columns cannot be changed nor the table deleted.
//
// Postgres keeps the record (migration 00201): the app role can neither
// set nor clear kept_by, and a kept table's record and columns change only
// through the keeping functions EnsureKept calls. The rows live in
// Xenibase, so every write path checks Writable (or GuardStep) before it
// sends anything. A business that brought its own Xenibase project can
// still edit the rows there, outside Taskiem.

// ErrKept is wrapped by every refusal of a write or a schema change on a
// kept table, and of making a table under a name Taskiem keeps.
var ErrKept = errors.New("this table is kept by Taskiem")

// KeptMessage is the sentence the page, the API and chat show for a kept
// table.
const KeptMessage = "This table is kept by Taskiem: rows are added by Taskiem and cannot be changed or deleted. Corrections are new rows."

// KeptNames are the names of the tables Taskiem's own features keep. No
// one else may make a table of one of these names, so a business cannot
// make an ordinary table that a feature would later find in its way.
var KeptNames = []string{"money_log", "money_matches", "school_students", "school_classes", "school_term_fees", "stock_movements"}

// IsKeptName reports whether name is one Taskiem keeps for itself.
func IsKeptName(name string) bool { return slices.Contains(KeptNames, name) }

// CheckTableName checks a new table's name: CheckName, and not a name
// Taskiem keeps for its own tables.
func CheckTableName(name string) error {
	if err := CheckName("table", name); err != nil {
		return err
	}
	if IsKeptName(name) {
		return fmt.Errorf("%w: the name %q is kept for a table Taskiem keeps itself; choose another", ErrKept, name)
	}
	return nil
}

// Kept reports whether the table is kept by Taskiem.
func (t Table) Kept() bool { return t.KeptBy != "" }

// Writable refuses a write or a schema change on a kept table.
func Writable(t Table) error {
	if t.Kept() {
		return keptError(t.Name)
	}
	return nil
}

func keptError(name string) error {
	return fmt.Errorf("%w: %s (table %s)", ErrKept, KeptMessage, name)
}

// KeptByName is the feature keeping the tenant's table of that name, ""
// for an ordinary table or none. tx must have the tenant in scope.
func KeptByName(ctx context.Context, tx pgx.Tx, name string) (string, error) {
	var kept string
	err := tx.QueryRow(ctx, `SELECT COALESCE(kept_by, '') FROM data_tables WHERE name = $1`, name).Scan(&kept)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	return kept, err
}

// IsKept is KeptByName in its own transaction scoped to the tenant.
func IsKept(ctx context.Context, pool *pgxpool.Pool, tenant uuid.UUID, name string) (keptBy string, err error) {
	err = db.InTenantTx(ctx, pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		keptBy, err = KeptByName(ctx, tx, name)
		return err
	})
	return keptBy, err
}

// rowWrites are the xenibase@1 actions that change rows: the four row
// writes, and call_function, which can write anything.
var rowWrites = []string{"insert_rows", "upsert_rows", "update_rows", "delete_rows", "call_function"}

// WritesRows reports whether a xenibase@1 action may change rows.
func WritesRows(action string) bool { return slices.Contains(rowWrites, action) }

// stepTable is the table name a step's input names, as the tenant's
// tables record it: an optional public. dropped, folded to lower case
// (refusing more, never less).
func stepTable(input map[string]any) string {
	name, _ := input["table"].(string)
	name = strings.ToLower(strings.TrimSpace(name))
	name = strings.TrimPrefix(name, "public.")
	return strings.Trim(name, `"`)
}

// sameURL compares two data API URLs, ignoring case and a trailing slash.
func sameURL(a, b string) bool {
	a, b = strings.TrimRight(strings.TrimSpace(a), "/"), strings.TrimRight(strings.TrimSpace(b), "/")
	return a != "" && strings.EqualFold(a, b)
}

// GuardStep is the workflow worker's check before a xenibase@1 step is
// sent: on the data tables project (the data_tables connection in prod,
// or any xenibase connection whose data API URL is the project's), a row
// write naming a kept table is refused, and so is call_function while
// the tenant keeps any table there, as a function can write anything.
// Reads, and steps on other projects, pass. tx must have the tenant in
// scope.
func GuardStep(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, connection, environment string, creds map[string]string, action string, input map[string]any) error {
	if !WritesRows(action) {
		return nil
	}
	if connection != ConnectionName || environment != Environment {
		p, err := loadProject(ctx, tx, tenant)
		if err != nil {
			return err
		}
		if !sameURL(creds["api_url"], p.APIURL) {
			return nil
		}
	}
	if action == "call_function" {
		var kept string
		err := tx.QueryRow(ctx, `SELECT name FROM data_tables WHERE kept_by IS NOT NULL ORDER BY name LIMIT 1`).Scan(&kept)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		return fmt.Errorf("%w: call_function is refused on the data tables project while Taskiem keeps tables in it (%s); write rows with insert_rows, upsert_rows, update_rows or delete_rows", ErrKept, kept)
	}
	name := stepTable(input)
	if name == "" {
		return nil
	}
	kept, err := KeptByName(ctx, tx, name)
	if err != nil || kept == "" {
		return err
	}
	return keptError(name)
}

// EnsureKept makes the tenant's table name a kept table with at least
// these columns: it records and creates the table (provisioning the
// organisation's project first, as the first table does) when there is
// none, marks it kept by keptBy, and adds any column it lacks. Columns it
// has that are not listed are left alone. It is safe to call again and
// from several processes at once: what is already there is left as it
// is, and Xenibase's "already exists" answers are taken as done.
//
// An ordinary table of that name, or one kept by another feature, is
// never taken over: EnsureKept returns an error wrapping ErrConflict that
// says so, for the caller to log. A table record whose columns were never
// added (a crash between the steps, or another caller still at it) is
// finished here.
func (s *Service) EnsureKept(ctx context.Context, tenant uuid.UUID, name, keptBy string, columns []Column) (Table, error) {
	if err := CheckName("table", name); err != nil {
		return Table{}, err
	}
	if err := CheckName("feature", keptBy); err != nil {
		return Table{}, err
	}
	cols := slices.Clone(columns)
	if err := CheckColumns(cols, 0); err != nil {
		return Table{}, err
	}
	actor := "taskiem:" + keptBy
	inTx := func(fn func(pgx.Tx) error) error { return db.InTenantTx(ctx, s.Pool, []uuid.UUID{tenant}, fn) }
	refuse := func(t Table) error {
		if t.KeptBy == "" {
			return fmt.Errorf("%w: an ordinary table named %q already exists in this organisation; Taskiem will not take it over as the %s table (rename or delete it, then try again)", ErrConflict, name, keptBy)
		}
		return fmt.Errorf("%w: the table %q is kept by %s, not %s", ErrConflict, name, t.KeptBy, keptBy)
	}

	// What is there now.
	var t Table
	found := false
	if err := inTx(func(tx pgx.Tx) error {
		id, err := tableID(ctx, tx, name)
		if err != nil || id == uuid.Nil {
			return err
		}
		found = true
		t, err = GetTable(ctx, tx, id)
		return err
	}); err != nil {
		return Table{}, err
	}
	if found && t.KeptBy != keptBy {
		return Table{}, refuse(t)
	}
	missing := missingColumns(t.Columns, cols)
	if found && len(missing) == 0 {
		return t, nil
	}

	proj, err := s.EnsureProject(ctx, tenant)
	if err != nil {
		return Table{}, err
	}
	if !proj.Ready() {
		return Table{}, ErrProvisioning
	}

	// The record first, marked kept, so no one can make an ordinary table
	// of the name meanwhile; its columns once Xenibase has them.
	created := false
	if !found {
		if err := inTx(func(tx pgx.Tx) error {
			newID := uuid.Must(uuid.NewV7())
			var id uuid.UUID
			var kept *string
			if err := tx.QueryRow(ctx, `SELECT id, kept_by FROM taskiem_data_table_keep($1, $2, $3, $4, $5, $6)`,
				tenant, newID, name, keptTitle(name), "", keptBy).Scan(&id, &kept); err != nil {
				return err
			}
			var err error
			if t, err = GetTable(ctx, tx, id); err != nil {
				return err
			}
			if kept == nil || *kept != keptBy {
				return refuse(t)
			}
			if id == newID {
				created = true
				return audit(ctx, tx, tenant, "table.keep", name, map[string]any{"table_id": id, "kept_by": keptBy, "columns": columnNames(cols)})
			}
			return nil
		}); err != nil {
			return Table{}, err
		}
		missing = missingColumns(t.Columns, cols)
		if len(missing) == 0 {
			return t, nil
		}
	}

	if len(t.Columns) == 0 {
		// The table may not exist in Xenibase yet. If it does (another
		// caller made it, or an earlier attempt stopped after), each
		// column is added in turn, as some may be missing there too.
		err := s.Apply(ctx, tenant, actor, CreateTableChange(name, missing))
		switch {
		case err == nil:
			missing = nil
		case alreadyExists(err):
		default:
			s.log().Warn("data tables: making a kept table failed", "tenant", tenant, "table", name, "created", created, "err", err)
			return Table{}, err
		}
	}
	for _, c := range missing {
		if err := s.Apply(ctx, tenant, actor, AddColumnChange(name, c)); err != nil && !alreadyExists(err) {
			return Table{}, err
		}
	}
	// Every declared column is now in Xenibase: record those not recorded.
	if err := inTx(func(tx pgx.Tx) error {
		for _, c := range missingColumns(t.Columns, cols) {
			var added bool
			if err := tx.QueryRow(ctx, `SELECT taskiem_data_table_keep_column($1, $2, $3, $4, $5, $6, $7)`,
				tenant, t.ID, c.Name, c.Title, c.Type, c.Required, c.Personal).Scan(&added); err != nil {
				return err
			}
			if added && !created {
				if err := audit(ctx, tx, tenant, "table.keep.column", name+"."+c.Name, map[string]any{"table_id": t.ID, "kept_by": keptBy, "type": c.Type}); err != nil {
					return err
				}
			}
		}
		var err error
		t, err = GetTable(ctx, tx, t.ID)
		return err
	}); err != nil {
		return Table{}, err
	}
	return t, nil
}

// tableID is the id of the tenant's table of that name, uuid.Nil for none.
func tableID(ctx context.Context, tx pgx.Tx, name string) (uuid.UUID, error) {
	var id uuid.UUID
	err := tx.QueryRow(ctx, `SELECT id FROM data_tables WHERE name = $1`, name).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, nil
	}
	return id, err
}

// missingColumns are the wanted columns have lacks, in wanted's order.
func missingColumns(have, wanted []Column) []Column {
	var out []Column
	for _, c := range wanted {
		if !slices.ContainsFunc(have, func(h Column) bool { return h.Name == c.Name }) {
			out = append(out, c)
		}
	}
	return out
}

// alreadyExists reports that Xenibase refused a change because what it
// makes is there already.
func alreadyExists(err error) bool {
	xe, ok := AsError(err)
	return ok && xe.Code == "ddl_failed" && strings.Contains(xe.Message, "already exists")
}

// keptTitle is a kept table's title from its name: money_log is "Money log".
func keptTitle(name string) string {
	return strings.ToUpper(name[:1]) + strings.ReplaceAll(name[1:], "_", " ")
}

func columnNames(cols []Column) []string {
	out := make([]string, len(cols))
	for i, c := range cols {
		out[i] = c.Name + ":" + c.Type
	}
	return out
}
