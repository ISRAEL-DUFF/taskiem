package schools

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/sender"
)

// Guardian is one of a family's guardians. Sealed at rest.
type Guardian struct {
	ID           uuid.UUID `json:"id"`
	Name         string    `json:"name"`
	Phone        string    `json:"phone"`
	Email        string    `json:"email,omitempty"`
	Relationship string    `json:"relationship,omitempty"`
}

// GuardianIn is a guardian to add or change.
type GuardianIn struct {
	Name         string `json:"name"`
	Phone        string `json:"phone"`
	Email        string `json:"email,omitempty"`
	Relationship string `json:"relationship,omitempty"`
}

type guardianSealed struct {
	Name         string `json:"name"`
	Phone        string `json:"phone"`
	Email        string `json:"email,omitempty"`
	Relationship string `json:"relationship,omitempty"`
}

// Family is the guardians of one or more students, who pay together.
type Family struct {
	ID        uuid.UUID    `json:"id"`
	Guardians []Guardian   `json:"guardians"`
	Students  []StudentRef `json:"students"`
	CreatedAt time.Time    `json:"created_at"`
}

// StudentRef is a student as their family lists them.
type StudentRef struct {
	ID              uuid.UUID `json:"id"`
	AdmissionNumber string    `json:"admission_number"`
	Name            string    `json:"name"`
	Class           string    `json:"class"`
	Status          string    `json:"status"`
}

// NormalisePhone writes a guardian's phone as E.164: a Nigerian number
// as +234…, a number abroad with its calling code.
func NormalisePhone(s string) (string, error) {
	p, err := sender.NormalisePhone(s, []string{"234"})
	if err == nil {
		return p, nil
	}
	d := strings.Map(func(r rune) rune {
		if r == ' ' || r == '-' || r == '(' || r == ')' || r == '.' {
			return -1
		}
		return r
	}, strings.TrimSpace(s))
	if strings.HasPrefix(d, "+") && !strings.HasPrefix(d, "+234") && len(d) >= 9 && len(d) <= 16 && strings.Trim(d[1:], "0123456789") == "" && d[1] != '0' {
		return d, nil
	}
	return "", invalid("%q is not a phone number (write 08031234567 or +2348031234567)", s)
}

func (g *GuardianIn) check() error {
	g.Name = strings.Join(strings.Fields(g.Name), " ")
	g.Relationship = strings.Join(strings.Fields(g.Relationship), " ")
	g.Email = strings.TrimSpace(g.Email)
	if g.Name == "" || len(g.Name) > 120 {
		return invalid("a guardian needs a name (up to 120 characters)")
	}
	p, err := NormalisePhone(g.Phone)
	if err != nil {
		return err
	}
	g.Phone = p
	if g.Email != "" {
		e, err := sender.NormaliseEmail(g.Email)
		if err != nil {
			return invalid("%q is not an email address", g.Email)
		}
		g.Email = e
	}
	if len(g.Relationship) > 40 {
		return invalid("a relationship has up to 40 characters, such as Mother or Uncle")
	}
	return nil
}

// guardianSubject is the subject a guardian is sealed under: their
// phone's, as invoices' customers are.
func (s *Schools) guardianSubject(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, phone string) (string, error) {
	return s.Vault.SubjectForTx(ctx, tx, tenant, "phone", phone)
}

// guardianFamily is the family a phone's guardian is in, uuid.Nil for
// none.
func (s *Schools) guardianFamily(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, phone string) (uuid.UUID, uuid.UUID, error) {
	subject, err := s.guardianSubject(ctx, tx, tenant, phone)
	if err != nil {
		return uuid.Nil, uuid.Nil, err
	}
	var fam, id uuid.UUID
	err = tx.QueryRow(ctx, `SELECT family_id, id FROM school_guardians WHERE tenant_id = $1 AND subject_id = $2`, tenant, subject).Scan(&fam, &id)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, uuid.Nil, nil
	}
	return fam, id, err
}

// putGuardian adds a guardian to a family, or changes the one with that
// phone when it is the family's already. A phone of another family's
// guardian is refused: a guardian is in one family.
func (s *Schools) putGuardian(ctx context.Context, tx pgx.Tx, tenant, family uuid.UUID, g GuardianIn, by string) (uuid.UUID, error) {
	env, err := s.Vault.SealTx(ctx, tx, tenant, "phone", g.Phone)
	if err != nil {
		return uuid.Nil, err
	}
	subject, _ := env["subject"].(string)
	sealed, err := s.Vault.SealUnderTx(ctx, tx, tenant, subject, "guardian", guardianSealed(g))
	if err != nil {
		return uuid.Nil, err
	}
	var id, fam uuid.UUID
	err = tx.QueryRow(ctx, `SELECT id, family_id FROM school_guardians WHERE tenant_id = $1 AND subject_id = $2 FOR UPDATE`, tenant, subject).Scan(&id, &fam)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		id = newID()
		if _, err := tx.Exec(ctx, `INSERT INTO school_guardians (id, tenant_id, family_id, subject_id, details, created_by) VALUES ($1, $2, $3, $4, $5, $6)`,
			id, tenant, family, subject, sealed, by); err != nil {
			if code, _ := pgCode(err); code == "23505" {
				return uuid.Nil, refused("the guardian with the phone %s is in another family already: a guardian belongs to one family", maskPhone(g.Phone))
			}
			return uuid.Nil, err
		}
	case err != nil:
		return uuid.Nil, err
	case fam != family:
		return uuid.Nil, refused("the guardian with the phone %s is in another family already: a guardian belongs to one family", maskPhone(g.Phone))
	default:
		if _, err := tx.Exec(ctx, `UPDATE school_guardians SET details = $2, updated_at = now() WHERE id = $1`, id, sealed); err != nil {
			return uuid.Nil, err
		}
	}
	_, err = tx.Exec(ctx, `UPDATE school_families SET updated_at = now() WHERE id = $1`, family)
	return id, err
}

func maskPhone(p string) string {
	if len(p) > 4 {
		return "•••" + p[len(p)-4:]
	}
	return p
}

// AddFamily makes a family with its guardians (at least one).
func (s *Schools) AddFamily(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, guardians []GuardianIn, by string) (Family, error) {
	if err := need(ctx, tx, tenant); err != nil {
		return Family{}, err
	}
	if len(guardians) == 0 || len(guardians) > 6 {
		return Family{}, invalid("a family has 1 to 6 guardians")
	}
	seen := map[string]bool{}
	for i := range guardians {
		if err := guardians[i].check(); err != nil {
			return Family{}, err
		}
		if seen[guardians[i].Phone] {
			return Family{}, invalid("two guardians have the phone %s", maskPhone(guardians[i].Phone))
		}
		seen[guardians[i].Phone] = true
	}
	id, err := newFamily(ctx, tx, tenant, by)
	if err != nil {
		return Family{}, err
	}
	for _, g := range guardians {
		if _, err := s.putGuardian(ctx, tx, tenant, id, g, by); err != nil {
			return Family{}, err
		}
	}
	return s.Family(ctx, tx, tenant, id)
}

func newFamily(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, by string) (uuid.UUID, error) {
	id := newID()
	_, err := tx.Exec(ctx, `INSERT INTO school_families (id, tenant_id, created_by) VALUES ($1, $2, $3)`, id, tenant, by)
	return id, err
}

// AddGuardian adds a guardian to a family.
func (s *Schools) AddGuardian(ctx context.Context, tx pgx.Tx, tenant, family uuid.UUID, g GuardianIn, by string) (Family, uuid.UUID, error) {
	if _, err := s.Family(ctx, tx, tenant, family); err != nil {
		return Family{}, uuid.Nil, err
	}
	if err := g.check(); err != nil {
		return Family{}, uuid.Nil, err
	}
	if fam, _, err := s.guardianFamily(ctx, tx, tenant, g.Phone); err != nil {
		return Family{}, uuid.Nil, err
	} else if fam == family {
		return Family{}, uuid.Nil, refused("the family has a guardian with the phone %s already", maskPhone(g.Phone))
	}
	id, err := s.putGuardian(ctx, tx, tenant, family, g, by)
	if err != nil {
		return Family{}, uuid.Nil, err
	}
	f, err := s.Family(ctx, tx, tenant, family)
	return f, id, err
}

// ChangeGuardian changes a guardian's details. A new phone must be no
// other guardian's.
func (s *Schools) ChangeGuardian(ctx context.Context, tx pgx.Tx, tenant, family, guardian uuid.UUID, g GuardianIn, by string) (Family, error) {
	if _, err := s.Family(ctx, tx, tenant, family); err != nil {
		return Family{}, err
	}
	if err := g.check(); err != nil {
		return Family{}, err
	}
	var subject string
	err := tx.QueryRow(ctx, `SELECT subject_id FROM school_guardians WHERE tenant_id = $1 AND family_id = $2 AND id = $3 FOR UPDATE`, tenant, family, guardian).Scan(&subject)
	if errors.Is(err, pgx.ErrNoRows) {
		return Family{}, ErrNotFound
	}
	if err != nil {
		return Family{}, err
	}
	now, err := s.guardianSubject(ctx, tx, tenant, g.Phone)
	if err != nil {
		return Family{}, err
	}
	if now != subject {
		// A new phone is a new subject: the guardian's row is replaced.
		if _, err := tx.Exec(ctx, `DELETE FROM school_guardians WHERE id = $1`, guardian); err != nil {
			return Family{}, err
		}
	}
	if _, err := s.putGuardian(ctx, tx, tenant, family, g, by); err != nil {
		return Family{}, err
	}
	return s.Family(ctx, tx, tenant, family)
}

// RemoveGuardian takes a guardian out of a family.
func (s *Schools) RemoveGuardian(ctx context.Context, tx pgx.Tx, tenant, family, guardian uuid.UUID) (Family, error) {
	if _, err := s.Family(ctx, tx, tenant, family); err != nil {
		return Family{}, err
	}
	tag, err := tx.Exec(ctx, `DELETE FROM school_guardians WHERE tenant_id = $1 AND family_id = $2 AND id = $3`, tenant, family, guardian)
	if err != nil {
		return Family{}, err
	}
	if tag.RowsAffected() == 0 {
		return Family{}, ErrNotFound
	}
	return s.Family(ctx, tx, tenant, family)
}

// Family reads one family with its guardians and students.
func (s *Schools) Family(ctx context.Context, tx pgx.Tx, tenant, id uuid.UUID) (Family, error) {
	if err := need(ctx, tx, tenant); err != nil {
		return Family{}, err
	}
	list, err := s.families(ctx, tx, tenant, &id)
	if err != nil {
		return Family{}, err
	}
	if len(list) == 0 {
		return Family{}, ErrNotFound
	}
	return list[0], nil
}

// Families lists the school's families, newest first.
func (s *Schools) Families(ctx context.Context, tx pgx.Tx, tenant uuid.UUID) ([]Family, error) {
	if err := need(ctx, tx, tenant); err != nil {
		return nil, err
	}
	return s.families(ctx, tx, tenant, nil)
}

func (s *Schools) families(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, one *uuid.UUID) ([]Family, error) {
	rows, err := tx.Query(ctx, `SELECT id, created_at FROM school_families WHERE tenant_id = $1 AND ($2::uuid IS NULL OR id = $2) ORDER BY created_at DESC, id`, tenant, one)
	if err != nil {
		return nil, err
	}
	out, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (Family, error) {
		var f Family
		err := r.Scan(&f.ID, &f.CreatedAt)
		f.Guardians, f.Students = []Guardian{}, []StudentRef{}
		return f, err
	})
	if err != nil {
		return nil, err
	}
	if out == nil {
		out = []Family{}
	}
	at := map[uuid.UUID]int{}
	for i, f := range out {
		at[f.ID] = i
	}
	type gRow struct {
		id, family uuid.UUID
		env        map[string]any
	}
	rows, err = tx.Query(ctx, `SELECT id, family_id, details FROM school_guardians WHERE tenant_id = $1 AND ($2::uuid IS NULL OR family_id = $2) ORDER BY created_at, id`, tenant, one)
	if err != nil {
		return nil, err
	}
	gs, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (gRow, error) {
		var g gRow
		return g, r.Scan(&g.id, &g.family, &g.env)
	})
	if err != nil {
		return nil, err
	}
	for _, g := range gs {
		i, ok := at[g.family]
		if !ok {
			continue
		}
		var d guardianSealed
		erased, err := s.open(ctx, tx, tenant, g.env, &d)
		if err != nil {
			return nil, err
		}
		if erased {
			d = guardianSealed{Name: Erased}
		}
		out[i].Guardians = append(out[i].Guardians, Guardian{ID: g.id, Name: d.Name, Phone: d.Phone, Email: d.Email, Relationship: d.Relationship})
	}
	list, err := s.listStudents(ctx, tx, tenant, `s.family_id IS NOT NULL AND ($2::uuid IS NULL OR s.family_id = $2)`, one)
	if err != nil {
		return nil, err
	}
	for _, st := range list {
		if i, ok := at[*st.FamilyID]; ok {
			out[i].Students = append(out[i].Students, StudentRef{ID: st.ID, AdmissionNumber: st.AdmissionNumber, Name: st.Name, Class: st.Class, Status: st.Status})
		}
	}
	return out, nil
}
