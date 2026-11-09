package datatables_test

import (
	"errors"
	"slices"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/israel-duff/taskiem/engine/datatables"
	"github.com/israel-duff/taskiem/engine/db"
)

func TestCheckReaders(t *testing.T) {
	got, err := datatables.CheckReaders([]string{"hr_lead", "hr", "hr"})
	if err != nil || !slices.Equal(got, []string{"hr", "hr_lead"}) {
		t.Fatalf("CheckReaders: %v %v", got, err)
	}
	for name, in := range map[string][]string{
		"empty (nobody)": {},
		"nil":            nil,
		"not a role":     {"HR people"},
		"blank":          {""},
	} {
		if _, err := datatables.CheckReaders(in); !errors.Is(err, datatables.ErrInvalid) {
			t.Errorf("%s: %v, want ErrInvalid", name, err)
		}
	}
	many := make([]string, datatables.MaxReaders+1)
	for i := range many {
		many[i] = "r" + string(rune('a'+i))
	}
	if _, err := datatables.CheckReaders(many); !errors.Is(err, datatables.ErrInvalid) {
		t.Errorf("too many readers: %v", err)
	}
}

func TestMayRead(t *testing.T) {
	open := datatables.Table{Name: "customers"}
	hr := datatables.Table{Name: "hr_tickets", Readers: []string{"hr", "hr_lead"}}
	for _, c := range []struct {
		table datatables.Table
		roles []string
		want  bool
	}{
		{open, []string{"viewer"}, true},
		{open, nil, true},
		{hr, []string{"viewer"}, false},
		{hr, []string{"admin", "builder", "operator", "auditor"}, false},
		{hr, []string{"viewer", "hr"}, true},
		{hr, []string{"hr_lead"}, true},
		{hr, []string{datatables.Owner}, true},
		{hr, nil, false},
	} {
		if got := datatables.MayRead(c.table, c.roles); got != c.want {
			t.Errorf("MayRead(%s, %v) = %v, want %v", c.table.Name, c.roles, got, c.want)
		}
		if err := datatables.CheckRead(c.table, c.roles); (err == nil) != c.want || err != nil && !errors.Is(err, datatables.ErrRestricted) {
			t.Errorf("CheckRead(%s, %v) = %v", c.table.Name, c.roles, err)
		}
	}
}

// Readers are recorded with the table, changed and cleared; the database
// refuses an empty list, and a kept table's readers do not change.
func TestReadersAreRecorded(t *testing.T) {
	w := newWorld(t)
	in := func(f func(tx pgx.Tx) error) error {
		return db.InTenantTx(ctx, w.DB.App, []uuid.UUID{w.Tenant}, f)
	}
	tbl := datatables.Table{Name: "hr_tickets", Columns: []datatables.Column{{Name: "reference", Type: "text"}}, CreatedBy: "user:x", Readers: []string{"hr", "hr_lead"}}
	if err := in(func(tx pgx.Tx) error { return datatables.InsertTable(ctx, tx, w.Tenant, &tbl) }); err != nil {
		t.Fatal(err)
	}
	plain := datatables.Table{Name: "customers", Columns: []datatables.Column{{Name: "name", Type: "text"}}, CreatedBy: "user:x"}
	if err := in(func(tx pgx.Tx) error { return datatables.InsertTable(ctx, tx, w.Tenant, &plain) }); err != nil {
		t.Fatal(err)
	}
	read := func(id uuid.UUID) datatables.Table {
		t.Helper()
		var got datatables.Table
		if err := in(func(tx pgx.Tx) error {
			var err error
			got, err = datatables.GetTable(ctx, tx, id)
			return err
		}); err != nil {
			t.Fatal(err)
		}
		return got
	}
	if got := read(tbl.ID); !slices.Equal(got.Readers, []string{"hr", "hr_lead"}) || !got.Restricted() {
		t.Fatalf("readers %v", got.Readers)
	}
	if got := read(plain.ID); got.Readers != nil || got.Restricted() {
		t.Fatalf("an ordinary table names no readers: %v", got.Readers)
	}
	var list []datatables.Table
	_ = in(func(tx pgx.Tx) error {
		var err error
		list, err = datatables.ListTables(ctx, tx)
		return err
	})
	if len(list) != 2 || list[0].Name != "customers" || list[0].Readers != nil || !slices.Equal(list[1].Readers, []string{"hr", "hr_lead"}) {
		t.Fatalf("list %+v", list)
	}
	if err := in(func(tx pgx.Tx) error { return datatables.SetReaders(ctx, tx, plain.ID, []string{"sales"}) }); err != nil {
		t.Fatal(err)
	}
	if got := read(plain.ID); !slices.Equal(got.Readers, []string{"sales"}) {
		t.Fatalf("set: %v", got.Readers)
	}
	if err := in(func(tx pgx.Tx) error { return datatables.SetReaders(ctx, tx, plain.ID, nil) }); err != nil {
		t.Fatal(err)
	}
	if got := read(plain.ID); got.Readers != nil {
		t.Fatalf("cleared: %v", got.Readers)
	}
	if err := in(func(tx pgx.Tx) error { return datatables.SetReaders(ctx, tx, uuid.New(), []string{"hr"}) }); !errors.Is(err, datatables.ErrNotFound) {
		t.Fatalf("missing table: %v", err)
	}
	err := in(func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `UPDATE data_tables SET readers = '{}' WHERE id = $1`, tbl.ID)
		return err
	})
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "23514" {
		t.Fatalf("an empty readers list: %v, want a check violation", err)
	}
	kept, err := w.svc.EnsureKept(ctx, w.Tenant, "money_log", "money_log", logColumns)
	if err != nil {
		t.Fatal(err)
	}
	if err := in(func(tx pgx.Tx) error { return datatables.SetReaders(ctx, tx, kept.ID, []string{"accounts"}) }); !errors.Is(err, datatables.ErrKept) {
		t.Fatalf("a kept table's readers: %v", err)
	}
}
