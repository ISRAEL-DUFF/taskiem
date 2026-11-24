package datatables_test

import (
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/datatables"
	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/runtime"
)

// sealedStep is a step's StepCompleted payload as run_events keeps it.
func (w *world) sealedStep(t *testing.T, ref runtime.RunRef, step string) string {
	t.Helper()
	var raw string
	if err := w.DB.Admin.QueryRow(ctx, `SELECT payload::text FROM run_events WHERE run_id = $1 AND type = 'StepCompleted' AND step_id = $2`, ref.ID, step).Scan(&raw); err != nil {
		t.Fatalf("%s: %v", step, err)
	}
	return raw
}

// A step's rows from an ordinary table of the data tables project are
// sealed column by column (docs/privacy.md#data-tables-in-a-run): the
// personal columns, and fields the definition does not name. A table
// Taskiem keeps, and one it has no definition of, are sealed whole, as
// the connector declares.
func TestRowsSealedByTheirColumns(t *testing.T) {
	w := newWorld(t)
	p, err := w.svc.EnsureProject(ctx, w.Tenant)
	if err != nil {
		t.Fatal(err)
	}
	customers := []datatables.Column{{Name: "name", Type: "text"}, {Name: "phone", Type: "phone", Personal: true}, {Name: "balance_kobo", Type: "integer"}}
	for _, name := range []string{"customers", "loose"} {
		if err := w.svc.Apply(ctx, w.Tenant, "test", datatables.CreateTableChange(name, customers)); err != nil {
			t.Fatal(err)
		}
	}
	// Only customers is recorded: loose is a table of the project Taskiem
	// has no definition of.
	if err := db.InTenantTx(ctx, w.DB.App, []uuid.UUID{w.Tenant}, func(tx pgx.Tx) error {
		return datatables.InsertTable(ctx, tx, w.Tenant, &datatables.Table{Name: "customers", Columns: customers, CreatedBy: "test"})
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := w.svc.EnsureKept(ctx, w.Tenant, "stock_items", "stock", []datatables.Column{{Name: "name", Type: "text"}, {Name: "quantity", Type: "integer"}}); err != nil {
		t.Fatal(err)
	}
	w.pg.Begin(p.ProjectID).
		Insert("customers", map[string]any{"id": 1, "name": "Ada Obi", "phone": "+2348031112222", "balance_kobo": 500000}).
		Insert("loose", map[string]any{"id": 1, "name": "Bisi Ade", "phone": "+2348032223333", "balance_kobo": 0}).
		Insert("stock_items", map[string]any{"id": 1, "name": "Rice 50kg", "quantity": 4}).Commit()
	wf := w.Publish(t, `{"schema":"wd/v1","id":"wf_rows","version":1,"name":"rows","trigger":{"type":"manual"},
	  "steps":[
	    {"id":"ours","type":"connector","connector":"xenibase@1","action":"read_rows","connection":"data_tables","input":{"table":"customers"}},
	    {"id":"one","type":"connector","connector":"xenibase@1","action":"read_rows","connection":"data_tables","input":{"table":"public.customers","select":"id,name,phone,balance_kobo"}},
	    {"id":"loose","type":"connector","connector":"xenibase@1","action":"read_rows","connection":"data_tables","input":{"table":"loose"}},
	    {"id":"kept","type":"connector","connector":"xenibase@1","action":"read_rows","connection":"data_tables","input":{"table":"stock_items"}}]}`)
	ref := w.Start(t, wf, map[string]any{})
	w.Drain(t)
	if st := w.Status(t, ref); st != "completed" {
		h, _ := w.Store.OpenedHistory(ctx, ref)
		for _, e := range h {
			t.Logf("%s %s %s", e.Type, e.StepID, e.Payload)
		}
		t.Fatalf("run %s", st)
	}
	for _, step := range []string{"ours", "one"} {
		got := w.sealedStep(t, ref, step)
		if !strings.Contains(got, `"name": "Ada Obi"`) || !strings.Contains(got, `"balance_kobo": 500000`) || strings.Contains(got, "1112222") || !strings.Contains(got, `"phone": {"ct"`) {
			t.Errorf("%s: %s", step, got)
		}
	}
	if got := w.sealedStep(t, ref, "loose"); strings.Contains(got, "Bisi") || strings.Contains(got, "3333") {
		t.Errorf("a table with no definition: %s", got)
	}
	if got := w.sealedStep(t, ref, "kept"); strings.Contains(got, "Rice") {
		t.Errorf("a table Taskiem keeps: %s", got)
	}
	// Opened, every value is as it was read.
	if out := w.lastStepEvent(t, ref); !strings.Contains(out, "Rice 50kg") {
		t.Errorf("opened: %s", out)
	}

	// Which steps are sealed column by column.
	_ = db.InTenantTx(ctx, w.DB.App, []uuid.UUID{w.Tenant}, func(tx pgx.Tx) error {
		for _, c := range []struct {
			conn, env, action string
			input             map[string]any
			out               bool
			want              bool
		}{
			{"data_tables", "prod", "read_rows", map[string]any{"table": "customers"}, true, true},
			{"data_tables", "prod", "read_rows", map[string]any{"table": "customers", "select": "name, balance_kobo"}, true, true},
			{"data_tables", "prod", "insert_rows", map[string]any{"table": "customers"}, false, true},
			{"data_tables", "prod", "read_rows", map[string]any{"table": "customers", "select": "name:phone"}, true, false},
			{"data_tables", "prod", "read_rows", map[string]any{"table": "customers", "select": "*,orders(total)"}, true, false},
			{"data_tables", "prod", "read_rows", map[string]any{"table": "other.customers"}, true, false},
			{"data_tables", "prod", "read_rows", map[string]any{"table": "Customers"}, true, false},
			{"data_tables", "prod", "call_function", map[string]any{"function": "f"}, true, false},
			{"data_tables", "prod", "delete_rows", map[string]any{"table": "customers"}, false, false},
			{"data_tables", "dev", "read_rows", map[string]any{"table": "customers"}, true, false},
			{"mine", "prod", "read_rows", map[string]any{"table": "customers"}, true, false},
			{"data_tables", "prod", "read_rows", map[string]any{"table": "stock_items"}, true, false},
			{"data_tables", "prod", "read_rows", map[string]any{"table": "loose"}, true, false},
		} {
			places, ok, err := datatables.StepRows(ctx, tx, c.conn, c.env, c.action, c.input, c.out)
			if err != nil || ok != c.want {
				t.Errorf("%+v: %v %v", c, ok, err)
			}
			if ok && (places[0].Personal("name") || !places[0].Personal("phone") || places[0].Personal("id") || !places[0].Personal("unknown")) {
				t.Errorf("%+v: the wrong columns", c)
			}
		}
		return nil
	})
}
