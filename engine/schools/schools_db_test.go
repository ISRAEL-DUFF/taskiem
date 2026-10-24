package schools_test

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/connectors/xenibase"
	"github.com/israel-duff/taskiem/connectors/xenibase/xenibasetest"
	"github.com/israel-duff/taskiem/engine/datatables"
	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/moneylog"
	rt "github.com/israel-duff/taskiem/engine/runtime/runtimetest"
	"github.com/israel-duff/taskiem/engine/schools"
)

var ctx = context.Background()

type world struct {
	t   *testing.T
	env *rt.Env
	s   *schools.Schools
}

func newWorld(t *testing.T) *world {
	env := rt.New(t)
	w := &world{t: t, env: env, s: &schools.Schools{Pool: env.DB.App, Vault: env.Vault}}
	w.in(func(tx pgx.Tx) error {
		_, err := schools.SetEnabled(ctx, tx, env.Tenant, true, "user:ada")
		return err
	})
	return w
}

func (w *world) in(fn func(pgx.Tx) error) {
	w.t.Helper()
	if err := w.try(fn); err != nil {
		w.t.Fatal(err)
	}
}

func (w *world) try(fn func(pgx.Tx) error) error {
	return db.InTenantTx(ctx, w.env.DB.App, []uuid.UUID{w.env.Tenant}, fn)
}

func (w *world) class(name, arm string, order int) schools.Class {
	w.t.Helper()
	var c schools.Class
	w.in(func(tx pgx.Tx) error {
		var err error
		c, err = w.s.AddClass(ctx, tx, w.env.Tenant, schools.ClassIn{Name: name, Arm: arm, Order: order}, "user:ada")
		return err
	})
	return c
}

func (w *world) count(q string, args ...any) int {
	w.t.Helper()
	var n int
	w.in(func(tx pgx.Tx) error { return tx.QueryRow(ctx, q, args...).Scan(&n) })
	return n
}

// Off, the area refuses everything; classes are unique by name and arm;
// a class with history is kept.
func TestClassesAndTheSwitch(t *testing.T) {
	w := newWorld(t)
	jss1a := w.class("JSS 1", "A", 7)
	if jss1a.Label != "JSS 1 A" {
		t.Fatalf("label %q", jss1a.Label)
	}
	err := w.try(func(tx pgx.Tx) error {
		_, err := w.s.AddClass(ctx, tx, w.env.Tenant, schools.ClassIn{Name: "jss 1", Arm: "a", Order: 7}, "user:ada")
		return err
	})
	if !errors.Is(err, schools.ErrState) {
		t.Fatalf("a second JSS 1 A: %v", err)
	}
	w.in(func(tx pgx.Tx) error {
		_, err := schools.SetEnabled(ctx, tx, w.env.Tenant, false, "user:ada")
		return err
	})
	if err := w.try(func(tx pgx.Tx) error { _, err := w.s.Classes(ctx, tx, w.env.Tenant); return err }); !errors.Is(err, schools.ErrOff) {
		t.Fatalf("off: %v", err)
	}
	w.in(func(tx pgx.Tx) error {
		_, err := schools.SetEnabled(ctx, tx, w.env.Tenant, true, "user:ada")
		return err
	})
	// Nothing was deleted while it was off.
	var list []schools.Class
	w.in(func(tx pgx.Tx) error { var err error; list, err = w.s.Classes(ctx, tx, w.env.Tenant); return err })
	if len(list) != 1 {
		t.Fatalf("classes %v", list)
	}
	typo := w.class("JSS 9", "", 9)
	w.in(func(tx pgx.Tx) error { _, err := w.s.RemoveClass(ctx, tx, w.env.Tenant, typo.ID); return err })
	w.in(func(tx pgx.Tx) error {
		_, err := w.s.AddStudent(ctx, tx, w.env.Tenant, schools.StudentIn{Name: "Ebube Eze", AdmissionNumber: "GCA/001", ClassID: jss1a.ID}, "user:ada")
		return err
	})
	if err := w.try(func(tx pgx.Tx) error { _, err := w.s.RemoveClass(ctx, tx, w.env.Tenant, jss1a.ID); return err }); !errors.Is(err, schools.ErrState) {
		t.Fatalf("removing a class with students: %v", err)
	}
}

// Starting a term enrols each active student once; the term before
// becomes past and read-only, with its enrollments.
func TestTermsAndEnrollments(t *testing.T) {
	w := newWorld(t)
	tn := w.env.Tenant
	jss1 := w.class("JSS 1", "", 7)
	jss2 := w.class("JSS 2", "", 8)
	var sess schools.Session
	var first, second schools.Term
	w.in(func(tx pgx.Tx) error {
		var err error
		if sess, err = w.s.AddSession(ctx, tx, tn, "2026/2027", "user:ada"); err != nil {
			return err
		}
		if first, err = w.s.AddTerm(ctx, tx, tn, sess.ID, schools.TermIn{Name: "First term", StartsOn: "2026-09-14", EndsOn: "2026-12-11", FeesDueOn: "2026-09-07"}, "user:ada"); err != nil {
			return err
		}
		second, err = w.s.AddTerm(ctx, tx, tn, sess.ID, schools.TermIn{Name: "Second term", StartsOn: "2027-01-11", EndsOn: "2027-04-02", FeesDueOn: "2027-01-04"}, "user:ada")
		return err
	})
	for _, bad := range []string{"2026/2028", "2026-2027"} {
		if err := w.try(func(tx pgx.Tx) error { _, err := w.s.AddSession(ctx, tx, tn, bad, "user:ada"); return err }); !errors.Is(err, schools.ErrInvalid) {
			t.Errorf("session %q: %v", bad, err)
		}
	}
	var a, b schools.StudentDetail
	w.in(func(tx pgx.Tx) error {
		var err error
		if a, err = w.s.AddStudent(ctx, tx, tn, schools.StudentIn{Name: "Ebube Eze", AdmissionNumber: "GCA/001", ClassID: jss1.ID}, "user:ada"); err != nil {
			return err
		}
		if b, err = w.s.AddStudent(ctx, tx, tn, schools.StudentIn{Name: "Chidi Eze", AdmissionNumber: "GCA/002", ClassID: jss2.ID}, "user:ada"); err != nil {
			return err
		}
		_, err = w.s.AddStudent(ctx, tx, tn, schools.StudentIn{Name: "Gone", AdmissionNumber: "GCA/003", ClassID: jss2.ID}, "user:ada")
		return err
	})
	if err := w.try(func(tx pgx.Tx) error {
		_, err := w.s.AddStudent(ctx, tx, tn, schools.StudentIn{Name: "Again", AdmissionNumber: "gca/001", ClassID: jss1.ID}, "user:ada")
		return err
	}); !errors.Is(err, schools.ErrState) {
		t.Fatalf("an admission number twice: %v", err)
	}
	w.in(func(tx pgx.Tx) error {
		st := schools.Withdrawn
		_, _, err := w.s.ChangeStudent(ctx, tx, tn, mustStudent(t, w, "GCA/003").ID, schools.StudentChange{Status: &st}, "user:ada")
		return err
	})
	var started schools.Started
	w.in(func(tx pgx.Tx) error {
		var err error
		started, err = w.s.StartTerm(ctx, tx, tn, first.ID, "user:ada")
		return err
	})
	if started.Enrolled != 2 || started.Term.State != schools.TermCurrent {
		t.Fatalf("started %+v", started)
	}
	// Again: nobody is enrolled twice.
	w.in(func(tx pgx.Tx) error {
		var err error
		started, err = w.s.StartTerm(ctx, tx, tn, first.ID, "user:ada")
		return err
	})
	if started.Enrolled != 0 || w.count(`SELECT count(*) FROM school_enrollments WHERE term_id = $1`, first.ID) != 2 {
		t.Fatalf("started again %+v", started)
	}
	// A student added while the term runs is enrolled at once; a class
	// change moves their enrollment.
	var c schools.StudentDetail
	w.in(func(tx pgx.Tx) error {
		var err error
		c, err = w.s.AddStudent(ctx, tx, tn, schools.StudentIn{Name: "Ada Obi", AdmissionNumber: "GCA/004", ClassID: jss1.ID}, "user:ada")
		return err
	})
	if len(c.Enrollments) != 1 || c.Enrollments[0].Class != "JSS 1" {
		t.Fatalf("enrolled %+v", c.Enrollments)
	}
	var changed schools.Changed
	w.in(func(tx pgx.Tx) error {
		var err error
		c, changed, err = w.s.ChangeStudent(ctx, tx, tn, c.ID, schools.StudentChange{ClassID: &jss2.ID}, "user:ada")
		return err
	})
	if c.Enrollments[0].Class != "JSS 2" || changed["enrollment"] == nil {
		t.Fatalf("moved %+v %v", c.Enrollments, changed)
	}
	// The second term: the first becomes past and read-only.
	w.in(func(tx pgx.Tx) error {
		var err error
		started, err = w.s.StartTerm(ctx, tx, tn, second.ID, "user:ada")
		return err
	})
	if started.Previous == nil || started.Previous.ID != first.ID || started.Enrolled != 3 {
		t.Fatalf("second %+v", started)
	}
	err := w.try(func(tx pgx.Tx) error {
		_, _, err := w.s.ChangeTerm(ctx, tx, tn, first.ID, schools.TermIn{Name: "First", StartsOn: "2026-09-14", EndsOn: "2026-12-11", FeesDueOn: "2026-09-07"})
		return err
	})
	if !errors.Is(err, schools.ErrState) {
		t.Fatalf("changing a past term: %v", err)
	}
	if err := w.try(func(tx pgx.Tx) error { _, err := w.s.StartTerm(ctx, tx, tn, first.ID, "user:ada"); return err }); !errors.Is(err, schools.ErrState) {
		t.Fatalf("starting a past term: %v", err)
	}
	// The database refuses it too, whoever writes.
	for _, q := range []string{`UPDATE school_terms SET name = 'x' WHERE id = $1`, `UPDATE school_enrollments SET class_id = class_id WHERE term_id = $1`, `DELETE FROM school_enrollments WHERE term_id = $1`} {
		err := w.try(func(tx pgx.Tx) error { _, err := tx.Exec(ctx, q, first.ID); return err })
		if err == nil || !strings.Contains(err.Error(), "read-only") {
			t.Errorf("%s: %v", q, err)
		}
	}
	// A class change now leaves the first term's enrollment as it was.
	w.in(func(tx pgx.Tx) error {
		_, _, err := w.s.ChangeStudent(ctx, tx, tn, a.ID, schools.StudentChange{ClassID: &jss2.ID}, "user:ada")
		return err
	})
	if n := w.count(`SELECT count(*) FROM school_enrollments WHERE term_id = $1 AND student_id = $2 AND class_id = $3`, first.ID, a.ID, jss1.ID); n != 1 {
		t.Errorf("first term's enrollment changed")
	}
	_ = b
}

func mustStudent(t *testing.T, w *world, number string) schools.StudentDetail {
	t.Helper()
	var d schools.StudentDetail
	w.in(func(tx pgx.Tx) error { var err error; d, err = w.s.Student(ctx, tx, w.env.Tenant, number); return err })
	return d
}

// A guardian belongs to one family; their details are sealed.
func TestFamilies(t *testing.T) {
	w := newWorld(t)
	tn := w.env.Tenant
	jss1 := w.class("JSS 1", "", 7)
	var f schools.Family
	w.in(func(tx pgx.Tx) error {
		var err error
		f, err = w.s.AddFamily(ctx, tx, tn, []schools.GuardianIn{{Name: "Ngozi Eze", Phone: "0803 123 4567", Email: "ngozi@example.com", Relationship: "Mother"}}, "user:ada")
		return err
	})
	if len(f.Guardians) != 1 || f.Guardians[0].Phone != "+2348031234567" || f.Guardians[0].Name != "Ngozi Eze" {
		t.Fatalf("family %+v", f)
	}
	err := w.try(func(tx pgx.Tx) error {
		_, err := w.s.AddFamily(ctx, tx, tn, []schools.GuardianIn{{Name: "Ngozi", Phone: "+2348031234567"}}, "user:ada")
		return err
	})
	if !errors.Is(err, schools.ErrState) {
		t.Fatalf("a guardian in two families: %v", err)
	}
	w.in(func(tx pgx.Tx) error {
		_, err := w.s.AddStudent(ctx, tx, tn, schools.StudentIn{Name: "Ebube Eze", AdmissionNumber: "GCA/001", ClassID: jss1.ID, FamilyID: &f.ID, DateOfBirth: "2014-02-03"}, "user:ada")
		return err
	})
	if n := w.count(`SELECT count(*) FROM school_guardians WHERE details::text LIKE '%Ngozi%' OR details::text LIKE '%803%'`); n != 0 {
		t.Error("a guardian's details in the clear")
	}
	if n := w.count(`SELECT count(*) FROM school_students WHERE details::text LIKE '%Ebube%' OR details::text LIKE '%2014%'`); n != 0 {
		t.Error("a student's details in the clear")
	}
	d := mustStudent(t, w, "gca/001")
	if d.Name != "Ebube Eze" || d.DateOfBirth != "2014-02-03" || d.Family == nil || len(d.Family.Students) != 1 {
		t.Fatalf("student %+v", d)
	}
}

// An import with any bad line saves nothing and lists each line; a good
// one is shown first, then saved; uploading it again adds nobody.
func TestImport(t *testing.T) {
	w := newWorld(t)
	tn := w.env.Tenant
	w.class("JSS 1", "A", 7)
	w.class("JSS 2", "A", 8)
	bad := "Name,Admission number,Class,Guardian name,Phone,Email\n" +
		"Ebube Eze,GCA/001,JSS 1 A,Ngozi Eze,08031234567,ngozi@example.com\n" +
		"Chidi Eze,GCA/002,JSS 3,Ngozi Eze,08031234567,\n" +
		",GCA/003,JSS 2 A,Musa Bello,0803,\n"
	err := w.try(func(tx pgx.Tx) error { _, err := w.s.Import(ctx, tx, tn, bad, "", "user:ada"); return err })
	var ie *schools.ImportError
	if !errors.As(err, &ie) || len(ie.Problems) != 1 || ie.Problems[0].Row != 4 {
		t.Fatalf("bad file: %v %+v", err, ie)
	}
	// The format is fine, the record is not: still each line and why.
	bad = strings.Replace(bad, ",GCA/003,JSS 2 A,Musa Bello,0803,", "Musa Bello,GCA/003,JSS 2 A,Musa Bello,08051112222,", 1)
	err = w.try(func(tx pgx.Tx) error { _, err := w.s.Import(ctx, tx, tn, bad, "", "user:ada"); return err })
	if !errors.As(err, &ie) || len(ie.Problems) != 1 || ie.Problems[0].Row != 3 || !strings.Contains(ie.Problems[0].Why, "JSS 3") {
		t.Fatalf("unknown class: %v %+v", err, ie)
	}
	if n := w.count(`SELECT count(*) FROM school_students`); n != 0 {
		t.Fatalf("%d students saved from a bad file", n)
	}
	good := "Name,Admission number,Class,Guardian name,Phone,Email,Relationship\n" +
		"Ebube Eze,GCA/001,JSS 1 A,Ngozi Eze,08031234567,ngozi@example.com,Mother\n" +
		"Chidi Eze,GCA/002,jss2a,Ngozi Eze,+234 803 123 4567,,\n" +
		"Chidi Eze,GCA/002,JSS 2 A,Obi Eze,08098765432,,Father\n" +
		"Musa Bello,GCA/003,JSS 2 A,Aisha Bello,08051112222,,\n"
	var plan schools.ImportPlan
	w.in(func(tx pgx.Tx) error {
		var err error
		plan, err = w.s.Import(ctx, tx, tn, good, "", "user:ada")
		return err
	})
	if plan.Saved || plan.Added != 3 || len(plan.Families) != 2 || len(plan.Families[0].Guardians) != 2 || len(plan.Families[0].Students) != 2 {
		t.Fatalf("plan %+v", plan)
	}
	if n := w.count(`SELECT count(*) FROM school_students`); n != 0 {
		t.Fatal("the plan saved something")
	}
	if err := w.try(func(tx pgx.Tx) error {
		_, err := w.s.Import(ctx, tx, tn, good+"\n", plan.Hash+"x", "user:ada")
		return err
	}); !errors.Is(err, schools.ErrState) {
		t.Fatalf("another file's hash: %v", err)
	}
	w.in(func(tx pgx.Tx) error {
		var err error
		plan, err = w.s.Import(ctx, tx, tn, good, plan.Hash, "user:ada")
		return err
	})
	if !plan.Saved || w.count(`SELECT count(*) FROM school_students`) != 3 || w.count(`SELECT count(*) FROM school_families`) != 2 || w.count(`SELECT count(*) FROM school_guardians`) != 3 {
		t.Fatalf("saved %+v", plan)
	}
	// Again, with a class changed: updates, no one twice.
	again := strings.Replace(good, "Musa Bello,GCA/003,JSS 2 A", "Musa Bello,gca/003,JSS 1 A", 1)
	w.in(func(tx pgx.Tx) error {
		var err error
		plan, err = w.s.Import(ctx, tx, tn, again, "", "user:ada")
		return err
	})
	if plan.Added != 0 || plan.Updated != 3 || !plan.Families[1].Existing {
		t.Fatalf("re-upload plan %+v", plan)
	}
	w.in(func(tx pgx.Tx) error { _, err := w.s.Import(ctx, tx, tn, again, plan.Hash, "user:ada"); return err })
	if w.count(`SELECT count(*) FROM school_students`) != 3 || w.count(`SELECT count(*) FROM school_families`) != 2 || mustStudent(t, w, "GCA/003").Class != "JSS 1 A" {
		t.Fatal("re-upload duplicated or did not update")
	}
}

// A session is promoted once: up a class, exceptions as chosen, the last
// class graduating.
func TestPromotion(t *testing.T) {
	w := newWorld(t)
	tn := w.env.Tenant
	j1a, j1b := w.class("JSS 1", "A", 7), w.class("JSS 1", "B", 7)
	j2a := w.class("JSS 2", "A", 8)
	ss3 := w.class("SS 3", "", 12)
	var sess schools.Session
	ids := map[string]uuid.UUID{}
	w.in(func(tx pgx.Tx) error {
		var err error
		if sess, err = w.s.AddSession(ctx, tx, tn, "2026/2027", "user:ada"); err != nil {
			return err
		}
		for n, c := range map[string]uuid.UUID{"A1": j1a.ID, "B1": j1b.ID, "R1": j1a.ID, "L1": j1b.ID, "S3": ss3.ID} {
			d, err := w.s.AddStudent(ctx, tx, tn, schools.StudentIn{Name: "Student " + n, AdmissionNumber: n, ClassID: c}, "user:ada")
			if err != nil {
				return err
			}
			ids[n] = d.ID
		}
		return nil
	})
	ex := map[uuid.UUID]string{ids["R1"]: schools.Repeat, ids["L1"]: schools.Leave}
	var p schools.Promotion
	w.in(func(tx pgx.Tx) error { var err error; p, err = w.s.Promotion(ctx, tx, tn, sess.ID, ex); return err })
	if p.Counts[schools.Promote] != 2 || p.Counts[schools.Graduate] != 1 || p.Counts[schools.Repeat] != 1 || p.Counts[schools.Leave] != 1 {
		t.Fatalf("plan %+v", p.Counts)
	}
	w.in(func(tx pgx.Tx) error {
		var err error
		p, err = w.s.Promote(ctx, tx, tn, sess.ID, ex, "user:ada")
		return err
	})
	for n, want := range map[string]string{"A1": "JSS 2 A", "B1": "JSS 2 A", "R1": "JSS 1 A", "L1": "JSS 1 B", "S3": "SS 3"} {
		if got := mustStudent(t, w, n).Class; got != want {
			t.Errorf("%s in %s, want %s", n, got, want)
		}
	}
	if mustStudent(t, w, "S3").Status != schools.Graduated || mustStudent(t, w, "L1").Status != schools.Withdrawn {
		t.Error("statuses")
	}
	if err := w.try(func(tx pgx.Tx) error { _, err := w.s.Promote(ctx, tx, tn, sess.ID, nil, "user:ada"); return err }); !errors.Is(err, schools.ErrState) {
		t.Fatalf("promoted twice: %v", err)
	}
	_ = j2a
}

// The kept copies follow the record: a student added, then changed, is
// one row with the latest; a class removed goes.
func TestMirror(t *testing.T) {
	w := newWorld(t)
	e := w.env
	x := xenibasetest.New(t)
	now := time.Now()
	x.Now = func() time.Time { return now }
	x.AddToken(xenibasetest.Token{Secret: "xb_platform", ID: "tok", Name: "platform", Scopes: []string{"read", "write", "admin"}, ExpiresAt: now.Add(10 * 365 * 24 * time.Hour)})
	if err := e.Registry.Register(xenibase.New(xenibase.Options{BaseURL: x.URL})); err != nil {
		t.Fatal(err)
	}
	w.in(func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO environments (tenant_id, name) VALUES ($1, 'prod')`, e.Tenant)
		return err
	})
	tables := &datatables.Service{Pool: e.DB.App, Vault: e.Vault, Egress: e.Egress,
		Platform: datatables.Platform{URL: x.URL, Token: "xb_platform", Org: x.OrgID, Region: "lagos"}, PollInterval: time.Millisecond}
	rows := &moneylog.XenibaseRows{Registry: e.Registry, Vault: e.Vault, Guard: e.Egress}
	m := &schools.Mirror{Schools: w.s, Tables: tables, Rows: rows, Logger: slog.New(slog.DiscardHandler)}
	j1 := w.class("JSS 1", "", 7)
	typo := w.class("JSS 9", "", 9)
	var st schools.StudentDetail
	w.in(func(tx pgx.Tx) error {
		var err error
		st, err = w.s.AddStudent(ctx, tx, e.Tenant, schools.StudentIn{Name: "Ebube Eze", AdmissionNumber: "GCA/001", ClassID: j1.ID}, "user:ada")
		return err
	})
	if err := m.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	w.in(func(tx pgx.Tx) error {
		gone := schools.Withdrawn
		if _, _, err := w.s.ChangeStudent(ctx, tx, e.Tenant, st.ID, schools.StudentChange{Status: &gone}, "user:ada"); err != nil {
			return err
		}
		_, err := w.s.RemoveClass(ctx, tx, e.Tenant, typo.ID)
		return err
	})
	for range 2 {
		if err := m.Tick(ctx); err != nil {
			t.Fatal(err)
		}
	}
	got, err := rows.Call(ctx, e.Tenant, "read_rows", map[string]any{"table": schools.StudentsTable, "limit": 10}, "")
	if err != nil {
		t.Fatal(err)
	}
	list, _ := got["rows"].([]any)
	if len(list) != 1 || list[0].(map[string]any)["status"] != "withdrawn" || list[0].(map[string]any)["name"] != "Ebube Eze" {
		t.Fatalf("students %v", list)
	}
	got, err = rows.Call(ctx, e.Tenant, "read_rows", map[string]any{"table": schools.ClassesTable, "limit": 10}, "")
	if err != nil {
		t.Fatal(err)
	}
	if list, _ := got["rows"].([]any); len(list) != 1 || list[0].(map[string]any)["label"] != "JSS 1" {
		t.Fatalf("classes %v", list)
	}
	if kept, err := datatables.IsKept(ctx, e.DB.App, e.Tenant, schools.StudentsTable); err != nil || kept != schools.KeptBy {
		t.Fatalf("kept %q %v", kept, err)
	}
	if n := w.count(`SELECT count(*) FROM school_students WHERE mirrored_at IS DISTINCT FROM updated_at`); n != 0 {
		t.Errorf("%d students not copied", n)
	}
}
