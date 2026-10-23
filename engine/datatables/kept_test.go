package datatables_test

import (
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/israel-duff/taskiem/engine/datatables"
	"github.com/israel-duff/taskiem/engine/db"
)

var logColumns = []datatables.Column{
	{Name: "entry_id", Type: "text", Required: true},
	{Name: "amount_kobo", Type: "integer"},
	{Name: "direction", Type: "text"},
}

func columnNamesOf(t datatables.Table) []string {
	out := make([]string, len(t.Columns))
	for i, c := range t.Columns {
		out[i] = c.Name
	}
	return out
}

// EnsureKept makes the table in Xenibase and records it kept, with its
// columns; called again it sends nothing, and a column added to the
// declaration later is added to the table.
func TestEnsureKeptMakesAndKeepsTheTable(t *testing.T) {
	w := newWorld(t)
	tbl, err := w.svc.EnsureKept(ctx, w.Tenant, "money_log", "money_log", logColumns)
	if err != nil {
		t.Fatal(err)
	}
	if tbl.KeptBy != "money_log" || !tbl.Kept() || !slices.Equal(columnNamesOf(tbl), []string{"entry_id", "amount_kobo", "direction"}) || tbl.Title != "Money log" {
		t.Fatalf("table %+v", tbl)
	}
	p := w.project(t)
	x := w.pg.Table(p.ProjectID, "money_log")
	if x == nil || !slices.Equal(x.Columns, []string{"id", "created_at", "updated_at", "entry_id", "amount_kobo", "direction"}) {
		t.Fatalf("xenibase table %+v", x)
	}
	n := len(w.pg.Requests)
	again, err := w.svc.EnsureKept(ctx, w.Tenant, "money_log", "money_log", logColumns)
	if err != nil || again.ID != tbl.ID || len(w.pg.Requests) != n {
		t.Fatalf("again: %v %+v, requests %v", err, again, w.pg.Requests[n:])
	}
	more := append(slices.Clone(logColumns), datatables.Column{Name: "corrects", Type: "text"})
	grown, err := w.svc.EnsureKept(ctx, w.Tenant, "money_log", "money_log", more)
	if err != nil || grown.ID != tbl.ID || !slices.Equal(columnNamesOf(grown), []string{"entry_id", "amount_kobo", "direction", "corrects"}) {
		t.Fatalf("grown: %v %+v", err, grown)
	}
	if x := w.pg.Table(p.ProjectID, "money_log"); !slices.Contains(x.Columns, "corrects") {
		t.Fatalf("xenibase columns %v", x.Columns)
	}
	// Listed like any table, with kept_by.
	var tables []datatables.Table
	_ = db.InTenantTx(ctx, w.DB.App, []uuid.UUID{w.Tenant}, func(tx pgx.Tx) error {
		tables, err = datatables.ListTables(ctx, tx)
		return err
	})
	if len(tables) != 1 || tables[0].KeptBy != "money_log" {
		t.Fatalf("listed %+v", tables)
	}
	if kept, err := datatables.IsKept(ctx, w.DB.App, w.Tenant, "money_log"); err != nil || kept != "money_log" {
		t.Fatalf("IsKept: %q %v", kept, err)
	}
	if kept, err := datatables.IsKept(ctx, w.DB.App, w.Tenant, "debtors"); err != nil || kept != "" {
		t.Fatalf("IsKept(debtors): %q %v", kept, err)
	}
}

// Several callers at once make one table, each column once, and all
// succeed.
func TestEnsureKeptConcurrently(t *testing.T) {
	w := newWorld(t)
	if _, err := w.svc.EnsureProject(ctx, w.Tenant); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	errs := make([]error, 6)
	ids := make([]uuid.UUID, 6)
	for i := range errs {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			tbl, err := w.svc.EnsureKept(ctx, w.Tenant, "money_log", "money_log", logColumns)
			errs[i], ids[i] = err, tbl.ID
		}(i)
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("caller %d: %v", i, err)
		}
		if ids[i] != ids[0] {
			t.Fatalf("caller %d got table %s, not %s", i, ids[i], ids[0])
		}
	}
	var tables, cols int
	_ = db.InTenantTx(ctx, w.DB.App, []uuid.UUID{w.Tenant}, func(tx pgx.Tx) error {
		_ = tx.QueryRow(ctx, `SELECT count(*) FROM data_tables`).Scan(&tables)
		return tx.QueryRow(ctx, `SELECT count(*) FROM data_table_columns`).Scan(&cols)
	})
	if tables != 1 || cols != len(logColumns) {
		t.Fatalf("%d tables, %d columns", tables, cols)
	}
	p := w.project(t)
	if x := w.pg.Table(p.ProjectID, "money_log"); x == nil || len(x.Columns) != 3+len(logColumns) {
		t.Fatalf("xenibase table %+v", x)
	}
}

// An ordinary table of the name is never taken over, nor one kept by
// another feature.
func TestEnsureKeptRefusesAnOrdinaryTable(t *testing.T) {
	w := newWorld(t)
	if _, err := w.svc.EnsureProject(ctx, w.Tenant); err != nil {
		t.Fatal(err)
	}
	err := db.InTenantTx(ctx, w.DB.App, []uuid.UUID{w.Tenant}, func(tx pgx.Tx) error {
		return datatables.InsertTable(ctx, tx, w.Tenant, &datatables.Table{Name: "money_log", Columns: []datatables.Column{{Name: "note", Type: "text"}}, CreatedBy: "user:x"})
	})
	if err != nil {
		t.Fatal(err)
	}
	n := len(w.pg.Requests)
	_, err = w.svc.EnsureKept(ctx, w.Tenant, "money_log", "money_log", logColumns)
	if !errors.Is(err, datatables.ErrConflict) || !strings.Contains(err.Error(), "ordinary table named \"money_log\" already exists") {
		t.Fatalf("took over an ordinary table: %v", err)
	}
	if len(w.pg.Requests) != n {
		t.Fatalf("sent %v", w.pg.Requests[n:])
	}
	if kept, _ := datatables.IsKept(ctx, w.DB.App, w.Tenant, "money_log"); kept != "" {
		t.Fatalf("the ordinary table is now kept by %q", kept)
	}
	if _, err := w.svc.EnsureKept(ctx, w.Tenant, "money_matches", "money_log", logColumns); err != nil {
		t.Fatal(err)
	}
	if _, err := w.svc.EnsureKept(ctx, w.Tenant, "money_matches", "other_feature", logColumns); !errors.Is(err, datatables.ErrConflict) || !strings.Contains(err.Error(), "kept by money_log") {
		t.Fatalf("another feature: %v", err)
	}
}

// The app role can neither set nor clear kept_by, nor change or delete a
// kept table's record or columns; the keeping functions refuse an
// ordinary table.
func TestKeptTableRecordIsGuarded(t *testing.T) {
	w := newWorld(t)
	tbl, err := w.svc.EnsureKept(ctx, w.Tenant, "money_log", "money_log", logColumns)
	if err != nil {
		t.Fatal(err)
	}
	var ordinary datatables.Table
	if err := db.InTenantTx(ctx, w.DB.App, []uuid.UUID{w.Tenant}, func(tx pgx.Tx) error {
		ordinary = datatables.Table{Name: "debtors", Columns: []datatables.Column{{Name: "name", Type: "text"}}, CreatedBy: "user:x"}
		return datatables.InsertTable(ctx, tx, w.Tenant, &ordinary)
	}); err != nil {
		t.Fatal(err)
	}
	refused := func(what, sql string, args ...any) {
		t.Helper()
		err := db.InTenantTx(ctx, w.DB.App, []uuid.UUID{w.Tenant}, func(tx pgx.Tx) error {
			_, err := tx.Exec(ctx, sql, args...)
			return err
		})
		var pgErr *pgconn.PgError
		if !errors.As(err, &pgErr) || pgErr.Code != "42501" {
			t.Errorf("%s: %v, want refused (42501)", what, err)
		}
	}
	refused("clearing kept_by", `UPDATE data_tables SET kept_by = NULL WHERE id = $1`, tbl.ID)
	refused("setting kept_by", `UPDATE data_tables SET kept_by = 'money_log' WHERE id = $1`, ordinary.ID)
	refused("inserting a kept table", `INSERT INTO data_tables (id, tenant_id, name, created_by, kept_by) VALUES ($1, $2, 'sneaky', 'x', 'money_log')`, uuid.New(), w.Tenant)
	refused("renaming a kept table", `UPDATE data_tables SET title = 'Mine' WHERE id = $1`, tbl.ID)
	refused("deleting a kept table", `DELETE FROM data_tables WHERE id = $1`, tbl.ID)
	refused("adding a column", `INSERT INTO data_table_columns (tenant_id, table_id, name, type, position) VALUES ($1, $2, 'extra', 'text', 9)`, w.Tenant, tbl.ID)
	refused("dropping a column", `DELETE FROM data_table_columns WHERE table_id = $1 AND name = 'direction'`, tbl.ID)
	refused("changing a column", `UPDATE data_table_columns SET required = false WHERE table_id = $1`, tbl.ID)
	refused("keeping a column of an ordinary table", `SELECT taskiem_data_table_keep_column($1, $2, 'x', '', 'text', false, false)`, w.Tenant, ordinary.ID)
	refused("keeping for another tenant", `SELECT * FROM taskiem_data_table_keep($1, $2, 'other', '', '', 'money_log')`, uuid.New(), uuid.New())
	// Ordinary tables are as they were.
	if err := db.InTenantTx(ctx, w.DB.App, []uuid.UUID{w.Tenant}, func(tx pgx.Tx) error {
		title := "Debtors"
		if err := datatables.UpdateTable(ctx, tx, ordinary.ID, &title, nil); err != nil {
			return err
		}
		if err := datatables.InsertColumn(ctx, tx, w.Tenant, ordinary.ID, datatables.Column{Name: "phone", Type: "phone"}); err != nil {
			return err
		}
		if err := datatables.DeleteColumn(ctx, tx, ordinary.ID, "phone"); err != nil {
			return err
		}
		return datatables.DeleteTable(ctx, tx, ordinary.ID)
	}); err != nil {
		t.Fatal(err)
	}
	var kept datatables.Table
	_ = db.InTenantTx(ctx, w.DB.App, []uuid.UUID{w.Tenant}, func(tx pgx.Tx) error {
		kept, err = datatables.GetTable(ctx, tx, tbl.ID)
		return err
	})
	if kept.KeptBy != "money_log" || len(kept.Columns) != len(logColumns) || kept.Title != "Money log" {
		t.Fatalf("kept table now %+v", kept)
	}
	if err := datatables.Writable(kept); !errors.Is(err, datatables.ErrKept) || !strings.Contains(err.Error(), datatables.KeptMessage) {
		t.Fatalf("Writable: %v", err)
	}
	if err := datatables.CheckTableName("money_matches"); !errors.Is(err, datatables.ErrKept) {
		t.Fatalf("CheckTableName: %v", err)
	}
}

// A workflow reads a kept table as any table, and every step that would
// change it fails before anything reaches Xenibase: on the data_tables
// connection, on another connection to the same project, and
// call_function there; an ordinary table is written as before.
func TestWorkflowCannotWriteAKeptTable(t *testing.T) {
	w := newWorld(t)
	if _, err := w.svc.EnsureKept(ctx, w.Tenant, "money_log", "money_log", logColumns); err != nil {
		t.Fatal(err)
	}
	p := w.project(t)
	if err := w.svc.Apply(ctx, w.Tenant, "test", datatables.CreateTableChange("debtors", []datatables.Column{{Name: "name", Type: "text"}})); err != nil {
		t.Fatal(err)
	}
	w.pg.Begin(p.ProjectID).Insert("money_log", map[string]any{"id": 1, "entry_id": "e1", "amount_kobo": 500000, "direction": "in"}).Commit()
	// The organisation's own connection to the same project.
	if _, err := w.Vault.CreateConnection(ctx, w.Tenant, "prod", "xenibase", "mine", "api_key", w.creds(t), "test"); err != nil {
		t.Fatal(err)
	}
	run := func(name, step string) (string, string) {
		t.Helper()
		wf := w.Publish(t, `{"schema":"wd/v1","id":"wf_`+name+`","version":1,"name":"`+name+`","trigger":{"type":"manual"},"steps":[`+step+`]}`)
		ref := w.Start(t, wf, map[string]any{"table": "money_log"})
		w.Drain(t)
		var payload string
		_ = db.InTenantTx(ctx, w.DB.App, []uuid.UUID{w.Tenant}, func(tx pgx.Tx) error {
			return tx.QueryRow(ctx, `SELECT payload::text FROM run_events WHERE run_id = $1 AND type IN ('StepCompleted', 'StepFailed') ORDER BY seq DESC LIMIT 1`, ref.ID).Scan(&payload)
		})
		return w.Status(t, ref), payload
	}
	st, out := run("reads", `{"id":"read","type":"connector","connector":"xenibase@1","action":"read_rows","connection":"data_tables","input":{"table":"money_log"}}`)
	if st != "completed" || !strings.Contains(out, "e1") {
		t.Fatalf("read: %s %s", st, out)
	}
	before := len(w.pg.Requests)
	for name, step := range map[string]string{
		"insert":       `{"id":"w","type":"connector","connector":"xenibase@1","action":"insert_rows","connection":"data_tables","input":{"table":"money_log","row":{"entry_id":"e2"}}}`,
		"upsert":       `{"id":"w","type":"connector","connector":"xenibase@1","action":"upsert_rows","connection":"data_tables","input":{"table":"public.money_log","rows":[{"entry_id":"e2"}],"on_conflict":["id"]}}`,
		"update":       `{"id":"w","type":"connector","connector":"xenibase@1","action":"update_rows","connection":"data_tables","input":{"table":"money_log","key":1,"set":{"amount_kobo":1}}}`,
		"delete":       `{"id":"w","type":"connector","connector":"xenibase@1","action":"delete_rows","connection":"data_tables","input":{"table":"money_log","key":1}}`,
		"expression":   `{"id":"w","type":"connector","connector":"xenibase@1","action":"delete_rows","connection":"data_tables","input":{"table":"=trigger.table","key":1}}`,
		"own":          `{"id":"w","type":"connector","connector":"xenibase@1","action":"delete_rows","connection":"mine","input":{"table":"money_log","key":1}}`,
		"callfunction": `{"id":"w","type":"connector","connector":"xenibase@1","action":"call_function","connection":"data_tables","input":{"function":"anything"}}`,
	} {
		st, out := run(name, step)
		if st != "failed" || !strings.Contains(out, "kept by Taskiem") {
			t.Errorf("%s: %s %s", name, st, out)
		}
	}
	for _, r := range w.pg.Requests[before:] {
		if strings.Contains(r, "money_log") || strings.Contains(r, "/rpc/") {
			t.Errorf("a refused step reached Xenibase: %s", r)
		}
	}
	if x := w.pg.Table(p.ProjectID, "money_log"); x == nil {
		t.Fatal("no money_log")
	}
	st, out = run("ordinary", `{"id":"w","type":"connector","connector":"xenibase@1","action":"insert_rows","connection":"data_tables","input":{"table":"debtors","row":{"name":"Ada"}}}`)
	if st != "completed" {
		t.Fatalf("ordinary insert: %s %s", st, out)
	}
	// Data API calls are recorded, so none above went unseen.
	if !slices.ContainsFunc(w.pg.Requests[before:], func(r string) bool { return strings.Contains(r, "debtors") }) {
		t.Fatalf("the ordinary insert is not among %v", w.pg.Requests[before:])
	}
}
