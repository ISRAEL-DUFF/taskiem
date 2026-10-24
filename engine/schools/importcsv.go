package schools

import (
	"context"
	"crypto/sha256"
	"encoding/csv"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Importing students (docs/spec/schools.md 4.4): a CSV of students and
// their guardians, checked whole before anything is saved, as a bank
// statement is (engine/moneylog.ParseStatement): a file with any line
// that cannot be taken adds nothing, and the answer lists each line and
// why. A good file is first shown as a plan (who is added, who updated,
// and the families grouped by guardians' phones); saving it takes the
// plan's hash back, so what is saved is the file that was shown.
// Uploading the same students again updates them by admission number and
// never adds them twice.

// Limits on an import.
const (
	MaxImportLines = 5000
	MaxImportBytes = 5 << 20
	// MaxProblems is how many problems an answer lists; the count says
	// how many there are in all.
	MaxProblems = 200
)

// ImportColumns are the columns an import reads, with the other names a
// file may give them. Name, admission number, class, guardian name and
// phone are required; email, relationship and date of birth are not.
var ImportColumns = []struct {
	Field    string
	Names    []string
	Required bool
}{
	{"name", []string{"name", "student name", "student", "full name"}, true},
	{"admission_number", []string{"admission number", "admission no", "admission", "adm no", "admission_number"}, true},
	{"class", []string{"class", "class name"}, true},
	{"guardian_name", []string{"guardian name", "guardian", "parent name", "parent", "guardian_name"}, true},
	{"phone", []string{"phone", "guardian phone", "parent phone", "phone number"}, true},
	{"email", []string{"email", "guardian email", "parent email"}, false},
	{"relationship", []string{"relationship", "relation"}, false},
	{"date_of_birth", []string{"date of birth", "dob", "birthday", "date_of_birth"}, false},
}

// Problem is a line that cannot be imported.
type Problem struct {
	Row int    `json:"row"` // in the file, counting the header as 1
	Why string `json:"why"`
}

// ErrImport is a file that cannot be imported; ImportError says why.
var ErrImport = errors.New("schools: the file cannot be imported")

// ImportError lists what is wrong with a file: nothing of it was saved.
type ImportError struct {
	Missing  []string  // columns the file does not have
	Problems []Problem // lines that cannot be taken (the first MaxProblems)
	Total    int       // how many lines cannot be taken
	Message  string
}

func (e *ImportError) Error() string { return e.Message }
func (e *ImportError) Unwrap() error { return ErrImport }

func badFile(msg string) error { return &ImportError{Message: msg} }

// ImportLine is one line of a file.
type ImportLine struct {
	Row             int
	Name            string
	AdmissionNumber string
	Class           string
	DateOfBirth     string
	Guardian        GuardianIn
}

func normHeader(s string) string {
	s = strings.ToLower(strings.TrimPrefix(s, "\ufeff"))
	s = strings.NewReplacer("_", " ", ".", " ", ":", " ").Replace(s)
	return strings.Join(strings.Fields(s), " ")
}

// ParseImport reads a file's lines and checks each on its own: what every
// line needs, phones and emails written so they can be used. It does not
// look at the school's record (Plan does).
func ParseImport(text string) ([]ImportLine, error) {
	if len(text) > MaxImportBytes {
		return nil, badFile(fmt.Sprintf("the file is larger than %d MB; import it in parts", MaxImportBytes>>20))
	}
	cr := csv.NewReader(strings.NewReader(text))
	cr.FieldsPerRecord = -1
	cr.TrimLeadingSpace = true
	cr.LazyQuotes = true
	header, err := cr.Read()
	if err != nil {
		return nil, badFile("the file is empty or not a CSV")
	}
	at := map[string]int{}
	for i, h := range header {
		at[normHeader(h)] = i
	}
	col := map[string]int{}
	var missing []string
	for _, c := range ImportColumns {
		col[c.Field] = -1
		for _, n := range c.Names {
			if i, ok := at[normHeader(n)]; ok {
				col[c.Field] = i
				break
			}
		}
		if col[c.Field] < 0 && c.Required {
			missing = append(missing, c.Names[0])
		}
	}
	if len(missing) > 0 {
		q := make([]string, len(missing))
		for i, m := range missing {
			q[i] = strconv.Quote(m)
		}
		return nil, &ImportError{Missing: missing, Message: "the file has no column " + strings.Join(q, ", ") +
			"; its first line names the columns: name, admission number, class, guardian name, phone, and optionally email, relationship and date of birth"}
	}
	var lines []ImportLine
	var problems []Problem
	row := 1
	for {
		rec, err := cr.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		row++
		if err != nil {
			problems = append(problems, Problem{Row: row, Why: "not a CSV line"})
			continue
		}
		if blank(rec) {
			continue
		}
		if len(lines) >= MaxImportLines {
			return nil, badFile(fmt.Sprintf("the file has more than %d lines; import it in parts", MaxImportLines))
		}
		get := func(f string) string {
			i := col[f]
			if i < 0 || i >= len(rec) {
				return ""
			}
			return strings.TrimSpace(rec[i])
		}
		l := ImportLine{Row: row, Class: strings.Join(strings.Fields(get("class")), " ")}
		var why []string
		if l.Name, err = checkName(get("name")); err != nil {
			why = append(why, "the student's name is missing or longer than 120 characters")
		}
		if l.AdmissionNumber, err = checkAdmission(get("admission_number")); err != nil {
			if get("admission_number") == "" {
				why = append(why, "the admission number is missing")
			} else {
				why = append(why, fmt.Sprintf("the admission number %q has characters other than letters, digits, '/', '_', '.' and '-'", get("admission_number")))
			}
		}
		if l.Class == "" {
			why = append(why, "the class is missing")
		}
		if l.DateOfBirth, err = checkBirth(get("date_of_birth")); err != nil {
			why = append(why, fmt.Sprintf("the date of birth %q is not a past date (write 2015-03-21 or 21/03/2015)", get("date_of_birth")))
		}
		l.Guardian = GuardianIn{Name: get("guardian_name"), Phone: get("phone"), Email: get("email"), Relationship: get("relationship")}
		if err := l.Guardian.check(); err != nil {
			switch {
			case strings.TrimSpace(get("guardian_name")) == "":
				why = append(why, "the guardian's name is missing")
			case get("phone") == "":
				why = append(why, "the guardian's phone is missing")
			default:
				why = append(why, Plain(err))
			}
		}
		if len(why) > 0 {
			problems = append(problems, Problem{Row: row, Why: strings.Join(why, "; ")})
			continue
		}
		lines = append(lines, l)
	}
	if len(problems) > 0 {
		return nil, tooMany(problems)
	}
	if len(lines) == 0 {
		return nil, badFile("the file has no students under its first line")
	}
	return lines, nil
}

func tooMany(problems []Problem) error {
	slices.SortStableFunc(problems, func(a, b Problem) int { return a.Row - b.Row })
	n := len(problems)
	if n > MaxProblems {
		problems = problems[:MaxProblems]
	}
	word := "lines cannot"
	if n == 1 {
		word = "line cannot"
	}
	return &ImportError{Problems: problems, Total: n, Message: fmt.Sprintf("%d %s be imported; nothing was saved", n, word)}
}

func blank(rec []string) bool {
	for _, f := range rec {
		if strings.TrimSpace(f) != "" {
			return false
		}
	}
	return true
}

// Hash is a file's hash: saving an import names the file it was shown.
func Hash(text string) string {
	h := sha256.Sum256([]byte(text))
	return hex.EncodeToString(h[:])
}

// ImportPlan is what an import does: shown first, then saved.
type ImportPlan struct {
	Hash     string          `json:"hash"`
	Added    int             `json:"added"`
	Updated  int             `json:"updated"`
	Students []ImportStudent `json:"students"`
	Families []ImportFamily  `json:"families"`
	Saved    bool            `json:"saved"`
}

// ImportStudent is a student of the file and what happens to them.
type ImportStudent struct {
	Row             int    `json:"row"`
	AdmissionNumber string `json:"admission_number"`
	Name            string `json:"name"`
	Class           string `json:"class"`
	Action          string `json:"action"` // add or update
	Family          int    `json:"family"` // index in Families
	classID         uuid.UUID
	dob             string
	existing        uuid.UUID
}

// ImportFamily is a family of the file: its guardians (grouped by
// phone) and students; an existing family when one of its guardians, or
// else its students, are in one already.
type ImportFamily struct {
	Index     int          `json:"index"`
	FamilyID  *uuid.UUID   `json:"family_id"`
	Existing  bool         `json:"existing"`
	Guardians []GuardianIn `json:"guardians"`
	Students  []string     `json:"students"`
}

// Import checks a file against the school's record and answers its plan;
// with confirm (the plan's hash) it saves it, in one transaction: all of
// it or, when any line cannot be taken, nothing.
func (s *Schools) Import(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, text, confirm, by string) (ImportPlan, error) {
	if err := lock(ctx, tx, tenant); err != nil {
		return ImportPlan{}, err
	}
	lines, err := ParseImport(text)
	if err != nil {
		return ImportPlan{}, err
	}
	plan, err := s.plan(ctx, tx, tenant, lines)
	if err != nil {
		return ImportPlan{}, err
	}
	plan.Hash = Hash(text)
	if confirm == "" {
		return plan, nil
	}
	if confirm != plan.Hash {
		return ImportPlan{}, refused("the file is not the one shown: check it again before saving")
	}
	if err := s.save(ctx, tx, tenant, &plan, by); err != nil {
		return ImportPlan{}, err
	}
	plan.Saved = true
	return plan, nil
}

// plan resolves the file's classes, students and families against the
// record. Every line that cannot be taken is listed.
func (s *Schools) plan(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, lines []ImportLine) (ImportPlan, error) {
	cls, err := classes(ctx, tx, tenant)
	if err != nil {
		return ImportPlan{}, err
	}
	byLabel := map[string][]Class{}
	for _, c := range cls {
		k := labelKey(c.Label)
		byLabel[k] = append(byLabel[k], c)
	}
	var problems []Problem
	bad := func(row int, format string, a ...any) {
		problems = append(problems, Problem{Row: row, Why: fmt.Sprintf(format, a...)})
	}

	// Students, by admission number: lines naming one again must agree.
	type stu struct {
		ImportStudent
		phones []string
	}
	var order []string
	students := map[string]*stu{}
	guardians := map[string]GuardianIn{}
	guardianRow := map[string]int{}
	for _, l := range lines {
		matches := byLabel[labelKey(l.Class)]
		if len(matches) == 0 {
			bad(l.Row, "the school has no class %q: add it under Classes first", l.Class)
			continue
		}
		if len(matches) > 1 {
			bad(l.Row, "the class %q could be %s or %s: write it as the Classes page does", l.Class, matches[0].Label, matches[1].Label)
			continue
		}
		c := matches[0]
		key := strings.ToUpper(l.AdmissionNumber)
		if st, ok := students[key]; ok {
			switch {
			case st.Name != l.Name:
				bad(l.Row, "the admission number %s is on line %d with another name", l.AdmissionNumber, st.Row)
				continue
			case st.classID != c.ID:
				bad(l.Row, "the admission number %s is on line %d in another class", l.AdmissionNumber, st.Row)
				continue
			case l.DateOfBirth != "" && st.dob != "" && l.DateOfBirth != st.dob:
				bad(l.Row, "the admission number %s is on line %d with another date of birth", l.AdmissionNumber, st.Row)
				continue
			}
			if st.dob == "" {
				st.dob = l.DateOfBirth
			}
			if slices.Contains(st.phones, l.Guardian.Phone) {
				bad(l.Row, "line %d gives the same student and guardian", st.Row)
				continue
			}
			st.phones = append(st.phones, l.Guardian.Phone)
		} else {
			students[key] = &stu{ImportStudent: ImportStudent{Row: l.Row, AdmissionNumber: l.AdmissionNumber, Name: l.Name, Class: c.Label, classID: c.ID, dob: l.DateOfBirth},
				phones: []string{l.Guardian.Phone}}
			order = append(order, key)
		}
		if g, ok := guardians[l.Guardian.Phone]; ok {
			if !strings.EqualFold(g.Name, l.Guardian.Name) {
				bad(l.Row, "the phone %s is the guardian %q's on line %d: one phone is one guardian", maskPhone(l.Guardian.Phone), g.Name, guardianRow[l.Guardian.Phone])
				continue
			}
			if g.Email == "" {
				g.Email = l.Guardian.Email
			}
			if g.Relationship == "" {
				g.Relationship = l.Guardian.Relationship
			}
			guardians[l.Guardian.Phone] = g
		} else {
			guardians[l.Guardian.Phone] = l.Guardian
			guardianRow[l.Guardian.Phone] = l.Row
		}
	}

	// Families: students sharing a guardian are one family (union-find
	// over students and phones).
	parent := map[string]string{}
	var find func(string) string
	find = func(x string) string {
		if p, ok := parent[x]; ok && p != x {
			r := find(p)
			parent[x] = r
			return r
		}
		parent[x] = x
		return x
	}
	union := func(a, b string) { parent[find(a)] = find(b) }
	for _, key := range order {
		for _, p := range students[key].phones {
			union("s:"+key, "p:"+p)
		}
	}
	// What the record has: each phone's family, each student.
	phoneFamily := map[string]uuid.UUID{}
	for p := range guardians {
		fam, _, err := s.guardianFamily(ctx, tx, tenant, p)
		if err != nil {
			return ImportPlan{}, err
		}
		if fam != uuid.Nil {
			phoneFamily[p] = fam
		}
	}
	studentFamily := map[string]*uuid.UUID{}
	for _, key := range order {
		st := students[key]
		var id uuid.UUID
		var fam *uuid.UUID
		err := tx.QueryRow(ctx, `SELECT id, family_id FROM school_students WHERE tenant_id = $1 AND upper(admission_number) = $2`, tenant, key).Scan(&id, &fam)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return ImportPlan{}, err
		}
		if err == nil {
			st.existing, studentFamily[key] = id, fam
		}
	}
	type group struct {
		students []string
		phones   []string
	}
	groups := map[string]*group{}
	var groupOrder []string
	for _, key := range order {
		r := find("s:" + key)
		g, ok := groups[r]
		if !ok {
			g = &group{}
			groups[r] = g
			groupOrder = append(groupOrder, r)
		}
		g.students = append(g.students, key)
		for _, p := range students[key].phones {
			if !slices.Contains(g.phones, p) {
				g.phones = append(g.phones, p)
			}
		}
	}
	plan := ImportPlan{Students: []ImportStudent{}, Families: []ImportFamily{}}
	for _, r := range groupOrder {
		g := groups[r]
		var fams []uuid.UUID
		for _, p := range g.phones {
			if f, ok := phoneFamily[p]; ok && !slices.Contains(fams, f) {
				fams = append(fams, f)
			}
		}
		if len(fams) == 0 {
			// No guardian is known: the family the students are in, if they
			// are all in one.
			for _, key := range g.students {
				if f := studentFamily[key]; f != nil && !slices.Contains(fams, *f) {
					fams = append(fams, *f)
				}
			}
			if len(fams) > 1 {
				for _, key := range g.students {
					bad(students[key].Row, "the students %s share a guardian here but are in different families already", strings.Join(g.students, ", "))
				}
				continue
			}
		}
		if len(fams) > 1 {
			for _, key := range g.students {
				bad(students[key].Row, "the guardians of %s are in different families already: a guardian belongs to one family", students[key].AdmissionNumber)
			}
			continue
		}
		f := ImportFamily{Index: len(plan.Families), Guardians: []GuardianIn{}, Students: []string{}}
		if len(fams) == 1 {
			id := fams[0]
			f.FamilyID, f.Existing = &id, true
		}
		for _, p := range g.phones {
			f.Guardians = append(f.Guardians, guardians[p])
		}
		for _, key := range g.students {
			st := students[key]
			st.Family = f.Index
			st.Action = "add"
			if st.existing != uuid.Nil {
				st.Action = "update"
				plan.Updated++
			} else {
				plan.Added++
			}
			f.Students = append(f.Students, st.AdmissionNumber)
			plan.Students = append(plan.Students, st.ImportStudent)
		}
		plan.Families = append(plan.Families, f)
	}
	if len(problems) > 0 {
		return ImportPlan{}, tooMany(problems)
	}
	slices.SortStableFunc(plan.Students, func(a, b ImportStudent) int { return a.Row - b.Row })
	return plan, nil
}

// save writes a plan: families made or kept, guardians added or changed,
// students added (enrolled in the current term when one runs) or updated.
func (s *Schools) save(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, plan *ImportPlan, by string) error {
	ids := make([]uuid.UUID, len(plan.Families))
	for i := range plan.Families {
		f := &plan.Families[i]
		if f.FamilyID != nil {
			ids[i] = *f.FamilyID
		} else {
			id, err := newFamily(ctx, tx, tenant, by)
			if err != nil {
				return err
			}
			ids[i], f.FamilyID = id, &id
		}
		for _, g := range f.Guardians {
			if _, err := s.putGuardian(ctx, tx, tenant, ids[i], g, by); err != nil {
				return err
			}
		}
	}
	for _, st := range plan.Students {
		fam := ids[st.Family]
		if st.existing == uuid.Nil {
			id := newID()
			sealed, subject, err := s.sealStudent(ctx, tx, tenant, id, studentSealed{Name: st.Name, DateOfBirth: st.dob})
			if err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `INSERT INTO school_students (id, tenant_id, admission_number, subject_id, details, class_id, family_id, created_by)
				VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`, id, tenant, st.AdmissionNumber, subject, sealed, st.classID, fam, by); err != nil {
				return admissionTaken(err, st.AdmissionNumber)
			}
			if _, err := enrollOne(ctx, tx, tenant, id, st.classID, by); err != nil {
				return err
			}
			continue
		}
		cur, err := s.student(ctx, tx, tenant, st.existing.String())
		if err != nil {
			return err
		}
		c := StudentChange{Name: &st.Name, ClassID: &st.classID, FamilyID: &fam}
		if st.dob != "" {
			c.DateOfBirth = &st.dob
		}
		if cur.AdmissionNumber != st.AdmissionNumber {
			c.AdmissionNumber = &st.AdmissionNumber
		}
		if _, _, err := s.ChangeStudent(ctx, tx, tenant, st.existing, c, by); err != nil {
			return err
		}
	}
	return nil
}
