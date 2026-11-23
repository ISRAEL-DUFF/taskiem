package api

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/links"
)

// "Waiting for Ada to approve" (made tools §7): who a tool's run on its
// way waits for, so its starter never meets silence. An open approval
// step's approvers, an open form step's recipient; in names and roles
// only, never a phone, an email or anything the run holds.

// stillGoing reports whether a run has not ended.
func stillGoing(status string) bool {
	switch status {
	case "running", "waiting", "queued", "pending":
		return true
	}
	return false
}

// waitingFor is who run waits for, as seen by viewer (a member's user id;
// uuid.Nil for a key): nil when it waits for no person.
func (s *Server) waitingFor(ctx context.Context, tenant, run, viewer uuid.UUID) ([]answerWait, error) {
	var out []answerWait
	err := db0(ctx, s, tenant, func(tx pgx.Tx) error {
		type appr struct {
			step  string
			role  *string
			level int
		}
		rows, err := tx.Query(ctx, `SELECT step_id, role, level FROM approvals WHERE run_id = $1 AND status = 'open' ORDER BY requested_at, step_id`, run)
		if err != nil {
			return err
		}
		as, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (appr, error) {
			var a appr
			return a, r.Scan(&a.step, &a.role, &a.level)
		})
		if err != nil {
			return err
		}
		for _, a := range as {
			w, err := approversWho(ctx, tx, run, a.step, a.level, a.role, viewer)
			if err != nil {
				return err
			}
			out = append(out, w)
		}
		type form struct {
			id uuid.UUID
			to []byte
		}
		rows, err = tx.Query(ctx, `SELECT id, recipient FROM form_requests WHERE run_id = $1 AND status = 'pending' ORDER BY created_at, id`, run)
		if err != nil {
			return err
		}
		fs, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (form, error) {
			var f form
			return f, r.Scan(&f.id, &f.to)
		})
		if err != nil {
			return err
		}
		for _, f := range fs {
			var to map[string]any
			_ = json.Unmarshal(f.to, &to)
			w, ok, err := formWho(ctx, tx, to, viewer)
			if err != nil {
				return err
			}
			if !ok {
				continue
			}
			if w.You {
				w.Request = f.id.String()
			}
			out = append(out, w)
		}
		return nil
	})
	return out, err
}

// approversWho is who may decide an open approval, in words: the one
// person who holds its role (or has it delegated to them) and has not
// decided yet, by name, or "you"; else an owner, or someone with the role.
func approversWho(ctx context.Context, tx pgx.Tx, run uuid.UUID, step string, level int, role *string, viewer uuid.UUID) (answerWait, error) {
	w := answerWait{Kind: "approval"}
	if role == nil || *role == "" {
		w.Who = "an approver"
		return w, nil
	}
	rows, err := tx.Query(ctx, `SELECT DISTINCT c.user_id, COALESCE(u.name, '') FROM (
		  SELECT m.user_id FROM memberships m WHERE m.role = $1
		  UNION
		  SELECT g.to_user FROM delegations g WHERE $1 = ANY (g.roles) AND g.revoked_at IS NULL AND g.starts_at <= now() AND g.ends_at > now()
		     AND EXISTS (SELECT 1 FROM memberships m WHERE m.user_id = g.from_user AND m.role IN ($1, 'owner'))) c
		JOIN users u ON u.id = c.user_id
		WHERE NOT EXISTS (SELECT 1 FROM approval_decisions d WHERE d.run_id = $2 AND d.step_id = $3 AND d.level = $4
		                    AND (d.user_id = c.user_id OR d.on_behalf_of = c.user_id))
		ORDER BY 1 LIMIT 3`, *role, run, step, level)
	if err != nil {
		return w, err
	}
	people, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (named, error) {
		var n named
		return n, r.Scan(&n.id, &n.name)
	})
	if err != nil {
		return w, err
	}
	w.Who, w.You = roleWho(*role, people, viewer, false)
	return w, nil
}

// named is a member and their name ("" when they gave none).
type named struct {
	id   uuid.UUID
	name string
}

// roleWho says who holds role, given the people who do: one person by
// name ("you" for the viewer); nobody, an owner (who may do what any role
// may); several, "the cashiers" for a role that names a person (plural)
// or "someone with the role manager" (else).
func roleWho(role string, people []named, viewer uuid.UUID, plural bool) (string, bool) {
	words := strings.ReplaceAll(role, "_", " ")
	if len(people) == 1 {
		if people[0].id == viewer && viewer != uuid.Nil {
			return "you", true
		}
		if n := strings.TrimSpace(people[0].name); n != "" {
			return n, false
		}
	}
	if role == "owner" || len(people) == 0 {
		return "an owner", false
	}
	if plural {
		if p, ok := personPlural(words); ok {
			return "the " + p, false
		}
	}
	return "someone with the role " + words, false
}

// personPlural is a role's plural when the role names a person (cashier,
// operator, accountant): "cashiers"; false for one that does not
// (finance), which is said as a role.
func personPlural(role string) (string, bool) {
	last := role
	if i := strings.LastIndex(role, " "); i >= 0 {
		last = role[i+1:]
	}
	for _, end := range []string{"er", "or", "ist", "ant", "ent", "ian", "ee"} {
		if strings.HasSuffix(last, end) {
			return role + "s", true
		}
	}
	return "", false
}

// formWho is who an open form step was put to, in words: "you", a
// member's name, the holders of a role, a linked business's name, or
// someone outside the business (by a phone's or an email's link, never
// said); false for one nobody can answer.
func formWho(ctx context.Context, tx pgx.Tx, to map[string]any, viewer uuid.UUID) (answerWait, bool, error) {
	w := answerWait{Kind: "form"}
	if m, _ := to["member"].(string); m != "" {
		id, err := uuid.Parse(m)
		if err != nil {
			return w, false, nil //nolint:nilerr // not a member: nobody can answer it
		}
		if id == viewer && viewer != uuid.Nil {
			w.Who, w.You = "you", true
			return w, true, nil
		}
		var name string
		err = tx.QueryRow(ctx, `SELECT COALESCE(u.name, '') FROM memberships m JOIN users u ON u.id = m.user_id WHERE m.user_id = $1 LIMIT 1`, id).Scan(&name)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return w, false, err
		}
		w.Who = strings.TrimSpace(name)
		if w.Who == "" {
			w.Who = "a member"
		}
		return w, true, nil
	}
	if starter, _ := to["starter"].(bool); starter {
		// Nobody started it by hand: nobody is asked.
		return w, false, nil
	}
	if role, _ := to["role"].(string); role != "" {
		rows, err := tx.Query(ctx, `SELECT DISTINCT m.user_id, COALESCE(u.name, '') FROM memberships m JOIN users u ON u.id = m.user_id WHERE m.role = $1 ORDER BY 1 LIMIT 3`, role)
		if err != nil {
			return w, false, err
		}
		people, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (named, error) {
			var n named
			return n, r.Scan(&n.id, &n.name)
		})
		if err != nil {
			return w, false, err
		}
		w.Who, w.You = roleWho(role, people, viewer, true)
		return w, true, nil
	}
	if l, _ := to["link"].(string); l != "" {
		w.Who = "a linked business"
		if id, err := uuid.Parse(strings.TrimSpace(l)); err == nil {
			list, _, err := links.ListTx(ctx, tx)
			if err != nil {
				return w, false, err
			}
			for _, x := range list {
				if (x.ID == id || x.Business == id) && x.Name != "" {
					w.Who = x.Name
					break
				}
			}
		}
		return w, true, nil
	}
	w.Who = "someone outside the business"
	return w, true, nil
}
