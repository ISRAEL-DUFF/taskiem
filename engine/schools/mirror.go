package schools

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/datatables"
	"github.com/israel-duff/taskiem/engine/moneylog"
)

// The kept copies (docs/spec/schools.md 4.3): the school's students and
// classes copied to kept data tables, so templates, workflows and the AI
// builder can read them as they read the money log. Taskiem alone writes
// them; people and workflows read, search and export them.
//
// A copy follows its record: each student or class is copied when it is
// added and again after every change (updated_at moves on; mirrored_at
// says which version is in the table), and a class removed is removed
// from the table. Names are copied into a personal column, as the money
// log's payers are.

// Kept tables.
const (
	StudentsTable = "school_students"
	ClassesTable  = "school_classes"
	// TermFeesTable is each student's invoice for a term, following it as
	// it is sent and paid.
	TermFeesTable = "school_term_fees"
	// KeptBy is the feature keeping them.
	KeptBy = "schools"
)

// StudentColumns are school_students' columns.
var StudentColumns = []datatables.Column{
	{Name: "student_id", Title: "Student", Type: "text", Required: true},
	{Name: "admission_number", Title: "Admission number", Type: "text", Required: true},
	{Name: "name", Title: "Name", Type: "text", Personal: true},
	{Name: "class_id", Title: "Class id", Type: "text", Required: true},
	{Name: "class", Title: "Class", Type: "text", Required: true},
	{Name: "status", Title: "Status", Type: "text", Required: true},
	{Name: "family_id", Title: "Family", Type: "text"},
	{Name: "changed_at", Title: "Changed", Type: "datetime", Required: true},
}

// ClassColumns are school_classes' columns.
var ClassColumns = []datatables.Column{
	{Name: "class_id", Title: "Class", Type: "text", Required: true},
	{Name: "name", Title: "Name", Type: "text", Required: true},
	{Name: "arm", Title: "Arm", Type: "text"},
	{Name: "label", Title: "Label", Type: "text", Required: true},
	{Name: "class_order", Title: "Order", Type: "integer", Required: true},
	{Name: "changed_at", Title: "Changed", Type: "datetime", Required: true},
}

// TermFeeColumns are school_term_fees' columns: amounts in kobo.
var TermFeeColumns = []datatables.Column{
	{Name: "invoice_id", Title: "Invoice id", Type: "text", Required: true},
	{Name: "invoice_number", Title: "Invoice", Type: "text", Required: true},
	{Name: "student_id", Title: "Student", Type: "text", Required: true},
	{Name: "admission_number", Title: "Admission number", Type: "text", Required: true},
	{Name: "name", Title: "Name", Type: "text", Personal: true},
	{Name: "class_id", Title: "Class id", Type: "text", Required: true},
	{Name: "class", Title: "Class", Type: "text", Required: true},
	{Name: "term_id", Title: "Term id", Type: "text", Required: true},
	{Name: "term", Title: "Term", Type: "text", Required: true},
	{Name: "total_kobo", Title: "Total (kobo)", Type: "integer", Required: true},
	{Name: "paid_kobo", Title: "Paid (kobo)", Type: "integer", Required: true},
	{Name: "status", Title: "Status", Type: "text", Required: true},
	{Name: "changed_at", Title: "Changed", Type: "datetime", Required: true},
}

// Mirror copies the record to the kept tables.
type Mirror struct {
	Schools *Schools
	Tables  *datatables.Service
	Rows    moneylog.Rows
	Logger  *slog.Logger

	mu   sync.Mutex
	kept map[uuid.UUID]bool
}

func (m *Mirror) log() *slog.Logger {
	if m.Logger != nil {
		return m.Logger
	}
	return slog.Default()
}

// Run copies every 30 seconds until ctx ends.
func (m *Mirror) Run(ctx context.Context) error {
	for {
		if err := m.Tick(ctx); err != nil && ctx.Err() == nil {
			m.log().Error("schools: copying to the kept tables", "err", err)
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(30 * time.Second):
		}
	}
}

// Tick copies what is waiting, for every school with the area on.
func (m *Mirror) Tick(ctx context.Context) error {
	if m.Tables == nil {
		return nil
	}
	rows, err := m.Schools.Pool.Query(ctx, `SELECT tenant_id FROM taskiem_schools_unmirrored_tenants()`)
	if err != nil {
		return err
	}
	tenants, err := pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
	if err != nil {
		return err
	}
	for _, t := range tenants {
		err := m.Tenant(ctx, t)
		if errors.Is(err, datatables.ErrUnavailable) {
			continue // no data tables for this business yet
		}
		if err != nil && ctx.Err() == nil {
			m.log().Warn("schools: copying to the kept tables", "tenant", t, "err", err)
		}
	}
	return nil
}

func (m *Mirror) ensure(ctx context.Context, tenant uuid.UUID) error {
	m.mu.Lock()
	done := m.kept[tenant]
	m.mu.Unlock()
	if done {
		return nil
	}
	if _, err := m.Tables.EnsureKept(ctx, tenant, ClassesTable, KeptBy, ClassColumns); err != nil {
		return fmt.Errorf("the %s table: %w", ClassesTable, err)
	}
	if _, err := m.Tables.EnsureKept(ctx, tenant, StudentsTable, KeptBy, StudentColumns); err != nil {
		return fmt.Errorf("the %s table: %w", StudentsTable, err)
	}
	if _, err := m.Tables.EnsureKept(ctx, tenant, TermFeesTable, KeptBy, TermFeeColumns); err != nil {
		return fmt.Errorf("the %s table: %w", TermFeesTable, err)
	}
	m.mu.Lock()
	if m.kept == nil {
		m.kept = map[uuid.UUID]bool{}
	}
	m.kept[tenant] = true
	m.mu.Unlock()
	return nil
}

type copyRow struct {
	id      uuid.UUID
	changed time.Time
	gone    bool
	row     map[string]any
}

// Tenant copies up to 500 of a school's classes and students waiting.
func (m *Mirror) Tenant(ctx context.Context, tenant uuid.UUID) error {
	if err := m.ensure(ctx, tenant); err != nil {
		return err
	}
	var cls, sts, fees []copyRow
	err := m.Schools.In(ctx, tenant, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT id, name, arm, class_order, updated_at, deleted_at IS NOT NULL FROM school_classes
			WHERE tenant_id = $1 AND mirrored_at IS DISTINCT FROM updated_at ORDER BY updated_at, id LIMIT 500`, tenant)
		if err != nil {
			return err
		}
		cls, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (copyRow, error) {
			var x copyRow
			var name, arm string
			var order int
			err := r.Scan(&x.id, &name, &arm, &order, &x.changed, &x.gone)
			x.row = map[string]any{"class_id": x.id.String(), "name": name, "arm": arm, "label": Label(name, arm), "class_order": order,
				"changed_at": x.changed.UTC().Format(time.RFC3339Nano)}
			return x, err
		})
		if err != nil {
			return err
		}
		rows, err = tx.Query(ctx, `SELECT `+studentCols+studentFrom+` WHERE s.tenant_id = $1 AND s.mirrored_at IS DISTINCT FROM s.updated_at
			ORDER BY s.updated_at, s.id LIMIT 500`, tenant)
		if err != nil {
			return err
		}
		list, err := m.Schools.scanStudents(ctx, tx, tenant, rows)
		if err != nil {
			return err
		}
		for _, st := range list {
			r := map[string]any{"student_id": st.ID.String(), "admission_number": st.AdmissionNumber, "name": st.Name, "class_id": st.ClassID.String(),
				"class": st.Class, "status": st.Status, "changed_at": st.UpdatedAt.UTC().Format(time.RFC3339Nano)}
			if st.FamilyID != nil {
				r["family_id"] = st.FamilyID.String()
			}
			sts = append(sts, copyRow{id: st.ID, changed: st.UpdatedAt, row: r})
		}
		tis, err := m.Schools.termInvoicesBy(ctx, tx, tenant, `ti.mirrored_at IS DISTINCT FROM i.updated_at`, ` ORDER BY i.updated_at, i.id LIMIT 500`)
		if err != nil {
			return err
		}
		for _, x := range tis {
			fees = append(fees, copyRow{id: x.InvoiceID, changed: x.UpdatedAt, row: map[string]any{"invoice_id": x.InvoiceID.String(), "invoice_number": x.Number,
				"student_id": x.StudentID.String(), "admission_number": x.AdmissionNumber, "name": x.Name, "class_id": x.ClassID.String(), "class": x.Class,
				"term_id": x.TermID.String(), "term": x.Term, "total_kobo": x.TotalKobo, "paid_kobo": x.PaidKobo, "status": x.Status,
				"changed_at": x.UpdatedAt.UTC().Format(time.RFC3339Nano)}})
		}
		return nil
	})
	if err != nil {
		return err
	}
	for _, x := range cls {
		if err := m.copy(ctx, tenant, ClassesTable, "class_id", x); err != nil {
			return err
		}
		if err := m.mark(ctx, tenant, `UPDATE school_classes SET mirrored_at = $3 WHERE tenant_id = $1 AND id = $2 AND updated_at = $3`, x); err != nil {
			return err
		}
	}
	for _, x := range sts {
		if err := m.copy(ctx, tenant, StudentsTable, "student_id", x); err != nil {
			return err
		}
		if err := m.mark(ctx, tenant, `UPDATE school_students SET mirrored_at = $3 WHERE tenant_id = $1 AND id = $2 AND updated_at = $3`, x); err != nil {
			return err
		}
	}
	for _, x := range fees {
		if err := m.copy(ctx, tenant, TermFeesTable, "invoice_id", x); err != nil {
			return err
		}
		if err := m.mark(ctx, tenant, `UPDATE school_term_invoices ti SET mirrored_at = $3 FROM customer_invoices i
			WHERE ti.tenant_id = $1 AND ti.invoice_id = $2 AND i.id = ti.invoice_id AND i.updated_at = $3`, x); err != nil {
			return err
		}
	}
	return nil
}

func (m *Mirror) mark(ctx context.Context, tenant uuid.UUID, q string, x copyRow) error {
	return m.Schools.In(ctx, tenant, func(tx pgx.Tx) error {
		//sqlcheck:built q is a literal at every caller (Mirror.Tenant above)
		_, err := tx.Exec(ctx, q, tenant, x.id, x.changed)
		return err
	})
}

// copy writes one row's current version: inserted when the table has no
// row for it, else changed in place; removed when its record is.
func (m *Mirror) copy(ctx context.Context, tenant uuid.UUID, table, key string, x copyRow) error {
	filter := []any{map[string]any{"column": key, "op": "eq", "value": x.id.String()}}
	k := rowKey(table, x.id, x.changed)
	if x.gone {
		_, err := m.Rows.Call(ctx, tenant, "delete_rows", map[string]any{"table": table, "filters": filter, "max_affected": 1, "return": "minimal"}, k)
		return err
	}
	found, err := m.Rows.Call(ctx, tenant, "read_rows", map[string]any{"table": table, "limit": 1, "select": key, "filters": filter}, "")
	if err != nil {
		return err
	}
	if rows, _ := found["rows"].([]any); len(rows) == 0 {
		_, err = m.Rows.Call(ctx, tenant, "insert_rows", map[string]any{"table": table, "rows": []any{x.row}, "return": "minimal"}, k)
		return err
	}
	_, err = m.Rows.Call(ctx, tenant, "update_rows", map[string]any{"table": table, "set": x.row, "filters": filter, "max_affected": 1, "return": "minimal"}, k)
	return err
}

// rowKey is a write's idempotency key: the same version of the same row,
// the same key.
func rowKey(table string, id uuid.UUID, changed time.Time) string {
	h := sha256.Sum256([]byte("taskiem " + table + " " + id.String() + " " + changed.UTC().Format(time.RFC3339Nano)))
	return "tsk-" + base64.RawURLEncoding.EncodeToString(h[:])[:22]
}
