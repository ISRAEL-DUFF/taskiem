package schools

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Promotion at the end of a session (docs/spec/schools.md 4.4): every
// active student moves up one class in one step, with the exceptions
// chosen first (a student who repeats, leaves or graduates early); a
// student in the last class graduates. Each session is promoted once.
//
// A class's next class is the class of the next order up with the same
// arm, else the first arm of that order. Classes of the same order are
// arms of one level (JSS 1 A and JSS 1 B).

// Exceptions to a promotion.
const (
	Promote  = "promote"  // up one class (the default)
	Repeat   = "repeat"   // stays in their class
	Leave    = "leave"    // withdrawn
	Graduate = "graduate" // graduated
)

// PromotionClass is one class's move.
type PromotionClass struct {
	ClassID  uuid.UUID  `json:"class_id"`
	Class    string     `json:"class"`
	NextID   *uuid.UUID `json:"next_id"` // none: its students graduate
	Next     string     `json:"next"`
	Students int        `json:"students"`
}

// PromotionStudent is one student's move.
type PromotionStudent struct {
	ID              uuid.UUID  `json:"id"`
	AdmissionNumber string     `json:"admission_number"`
	Name            string     `json:"name"`
	Class           string     `json:"class"`
	Action          string     `json:"action"`
	NextID          *uuid.UUID `json:"next_id"`
	Next            string     `json:"next"`
}

// Promotion is a session's promotion: the plan, or what was done.
type Promotion struct {
	SessionID  uuid.UUID          `json:"session_id"`
	Session    string             `json:"session"`
	Classes    []PromotionClass   `json:"classes"`
	Students   []PromotionStudent `json:"students"`
	Counts     map[string]int     `json:"counts"`
	PromotedAt *time.Time         `json:"promoted_at"`
	Done       bool               `json:"done"`
}

// nextClasses maps each class to the one its students move up to; a class
// with none is the last.
func nextClasses(list []Class) map[uuid.UUID]*Class {
	out := map[uuid.UUID]*Class{}
	for _, c := range list {
		next := -1
		for _, d := range list {
			if d.Order > c.Order && (next < 0 || d.Order < next) {
				next = d.Order
			}
		}
		if next < 0 {
			out[c.ID] = nil
			continue
		}
		var pick *Class
		for i := range list {
			d := &list[i]
			if d.Order != next {
				continue
			}
			if d.Arm == c.Arm {
				pick = d
				break
			}
			if pick == nil {
				pick = d // classes are in order: the first arm
			}
		}
		out[c.ID] = pick
	}
	return out
}

// Promotion plans (or, when the session was promoted, shows) a session's
// promotion, with the exceptions given.
func (s *Schools) Promotion(ctx context.Context, tx pgx.Tx, tenant, session uuid.UUID, exceptions map[uuid.UUID]string) (Promotion, error) {
	if err := need(ctx, tx, tenant); err != nil {
		return Promotion{}, err
	}
	p := Promotion{SessionID: session, Classes: []PromotionClass{}, Students: []PromotionStudent{}, Counts: map[string]int{}}
	err := tx.QueryRow(ctx, `SELECT label FROM school_sessions WHERE tenant_id = $1 AND id = $2`, tenant, session).Scan(&p.Session)
	if errors.Is(err, pgx.ErrNoRows) {
		return Promotion{}, ErrNotFound
	}
	if err != nil {
		return Promotion{}, err
	}
	var summary []byte
	err = tx.QueryRow(ctx, `SELECT summary, created_at FROM school_promotions WHERE tenant_id = $1 AND session_id = $2`, tenant, session).Scan(&summary, &p.PromotedAt)
	if err == nil {
		var done Promotion
		_ = json.Unmarshal(summary, &done)
		p.Counts, p.Done = done.Counts, true
		if p.Counts == nil {
			p.Counts = map[string]int{}
		}
		return p, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return Promotion{}, err
	}
	cls, err := classes(ctx, tx, tenant)
	if err != nil {
		return Promotion{}, err
	}
	next := nextClasses(cls)
	for _, c := range cls {
		pc := PromotionClass{ClassID: c.ID, Class: c.Label, Students: c.Students}
		if n := next[c.ID]; n != nil {
			id := n.ID
			pc.NextID, pc.Next = &id, n.Label
		}
		p.Classes = append(p.Classes, pc)
	}
	list, err := s.listStudents(ctx, tx, tenant, `s.status = 'active'`)
	if err != nil {
		return Promotion{}, err
	}
	known := map[uuid.UUID]bool{}
	for _, st := range list {
		known[st.ID] = true
		ps := PromotionStudent{ID: st.ID, AdmissionNumber: st.AdmissionNumber, Name: st.Name, Class: st.Class, Action: Promote}
		if a, ok := exceptions[st.ID]; ok {
			switch a {
			case Promote, Repeat, Leave, Graduate:
				ps.Action = a
			default:
				return Promotion{}, invalid("an exception is promote, repeat, leave or graduate")
			}
		}
		switch ps.Action {
		case Promote:
			if n := next[st.ClassID]; n != nil {
				id := n.ID
				ps.NextID, ps.Next = &id, n.Label
			} else {
				ps.Action = Graduate // the last class
			}
		case Repeat:
			id := st.ClassID
			ps.NextID, ps.Next = &id, st.Class
		}
		p.Counts[ps.Action]++
		p.Students = append(p.Students, ps)
	}
	for id := range exceptions {
		if !known[id] {
			return Promotion{}, invalid("an exception names a student who is not active")
		}
	}
	return p, nil
}

// Promote promotes a session, once: the students move as the plan with
// these exceptions says, and the promotion is recorded. A session whose
// next session has a term started already is not promoted (its students
// were enrolled in their old classes).
func (s *Schools) Promote(ctx context.Context, tx pgx.Tx, tenant, session uuid.UUID, exceptions map[uuid.UUID]string, by string) (Promotion, error) {
	if err := lock(ctx, tx, tenant); err != nil {
		return Promotion{}, err
	}
	p, err := s.Promotion(ctx, tx, tenant, session, exceptions)
	if err != nil {
		return Promotion{}, err
	}
	if p.Done {
		return Promotion{}, refused("%s was promoted on %s: a session is promoted once", p.Session, p.PromotedAt.Format("2 January 2006"))
	}
	var later bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM school_terms t JOIN school_sessions ss ON ss.id = t.session_id
		WHERE t.tenant_id = $1 AND t.started_at IS NOT NULL AND ss.label > $2)`, tenant, p.Session).Scan(&later); err != nil {
		return Promotion{}, err
	}
	if later {
		return Promotion{}, refused("a term of a later session has started: promote %s before the next session's first term starts", p.Session)
	}
	if len(p.Students) == 0 {
		return Promotion{}, refused("there are no active students to promote")
	}
	summary, _ := json.Marshal(map[string]any{"counts": p.Counts})
	if _, err := tx.Exec(ctx, `INSERT INTO school_promotions (id, tenant_id, session_id, summary, created_by) VALUES ($1, $2, $3, $4, $5)`,
		newID(), tenant, session, summary, by); err != nil {
		if code, _ := pgCode(err); code == "23505" {
			return Promotion{}, refused("%s was promoted already: a session is promoted once", p.Session)
		}
		return Promotion{}, err
	}
	// Students move in one statement per kind; the current term's
	// enrollments (if the session's last term still runs) stay as they
	// were: they are that term's history.
	byKind := map[string][]uuid.UUID{}
	var moved, to []uuid.UUID
	for _, st := range p.Students {
		switch st.Action {
		case Promote:
			moved, to = append(moved, st.ID), append(to, *st.NextID)
		case Leave, Graduate:
			byKind[st.Action] = append(byKind[st.Action], st.ID)
		}
	}
	if len(moved) > 0 {
		if _, err := tx.Exec(ctx, `UPDATE school_students s SET class_id = x.class, updated_at = now()
			FROM unnest($2::uuid[], $3::uuid[]) AS x (id, class) WHERE s.tenant_id = $1 AND s.id = x.id`, tenant, moved, to); err != nil {
			return Promotion{}, err
		}
	}
	for kind, status := range map[string]string{Leave: Withdrawn, Graduate: Graduated} {
		if ids := byKind[kind]; len(ids) > 0 {
			if _, err := tx.Exec(ctx, `UPDATE school_students SET status = $3, updated_at = now() WHERE tenant_id = $1 AND id = ANY ($2)`, tenant, ids, status); err != nil {
				return Promotion{}, err
			}
		}
	}
	now := s.now()
	p.PromotedAt, p.Done = &now, true
	return p, nil
}
