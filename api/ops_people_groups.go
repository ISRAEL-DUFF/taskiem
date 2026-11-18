package api

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/ops"
	"github.com/israel-duff/taskiem/engine/peoplesafety"
	"github.com/israel-duff/taskiem/engine/push"
)

// Moderators and groups (people-chatting §8.3; 5b B; "As built: stage 5b,
// group safety (server)"): a group's page (who is in it, the reports of
// it, what was decided), opened only saying why and recorded; turning off
// a group's invite link and closing a group, each with a passkey; and the
// safety job carrying out a closure whose day came. Members are acted on
// as people are (ops_people.go). A group's name and messages are seen
// only in a report of it.

func (s *Server) opsPeopleGroupRoutes(r chi.Router) {
	r.Get("/people/groups/{id}", s.opsPeopleGroup)
	r.Post("/people/groups/{id}/actions", s.opsModerateGroup)
}

type opsGroupMember struct {
	PersonID  uuid.UUID  `json:"person_id"`
	Name      string     `json:"name"`
	Role      string     `json:"role"`
	How       string     `json:"how"`
	JoinedAt  time.Time  `json:"joined_at"`
	LeftAt    *time.Time `json:"left_at"`
	LeftHow   *string    `json:"left_how"`
	Deleted   bool       `json:"deleted"`
	Suspended bool       `json:"suspended"`
}

type opsGroupReport struct {
	ID           uuid.UUID  `json:"id"`
	ReporterID   *uuid.UUID `json:"reporter_id"`
	ReportedID   *uuid.UUID `json:"reported_id"`
	ReportedName string     `json:"reported_name"`
	Reason       string     `json:"reason"`
	Status       string     `json:"status"`
	CreatedAt    time.Time  `json:"created_at"`
}

// opsPeopleGroup is GET /v1/ops/people/groups/{id}?why=: a group for
// moderators: when it was made and by whom, its members (who is in it is
// a person's data: opened saying why, recorded first), its link and
// standing, the reports of it and what moderators decided, with appeals.
// Never its name or messages. A group already deleted answers its
// decisions alone (gone).
func (s *Server) opsPeopleGroup(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id, ok := opsID(w, r, "id", "group")
	if !ok {
		return
	}
	why, ok := opsWhy(w, r)
	if !ok {
		return
	}
	o := operatorFrom(ctx)
	type group struct {
		ID            uuid.UUID  `json:"id"`
		CreatedAt     time.Time  `json:"created_at"`
		CreatedBy     *uuid.UUID `json:"created_by"`
		CreatorName   string     `json:"creator_name"`
		Members       int        `json:"members"`
		Admins        int        `json:"admins"`
		LinkOn        bool       `json:"link_on"`
		LastMessageAt time.Time  `json:"last_message_at"`
		ClosedAt      *time.Time `json:"closed_at"`
		CloseUntil    *time.Time `json:"close_until"`
		LinkStoppedAt *time.Time `json:"link_stopped_at"`
		Reports       int64      `json:"reports"`
		Held          int64      `json:"held"`
	}
	var g *group
	var members []opsGroupMember
	var reports []opsGroupReport
	var acts []opsAction
	err := s.opsTx(r, func(tx pgx.Tx, _ func(uuid.UUID) error) error {
		rows, err := tx.Query(ctx, `SELECT * FROM taskiem_ops_group($1)`, id)
		if err != nil {
			return err
		}
		list, err := pgx.CollectRows(rows, pgx.RowToStructByPos[group])
		if err != nil {
			return err
		}
		if len(list) == 1 {
			g = &list[0]
		}
		if rows, err = tx.Query(ctx, `SELECT * FROM taskiem_ops_group_members($1)`, id); err != nil {
			return err
		}
		if members, err = pgx.CollectRows(rows, pgx.RowToStructByPos[opsGroupMember]); err != nil {
			return err
		}
		if rows, err = tx.Query(ctx, `SELECT * FROM taskiem_ops_group_reports($1)`, id); err != nil {
			return err
		}
		if reports, err = pgx.CollectRows(rows, pgx.RowToStructByPos[opsGroupReport]); err != nil {
			return err
		}
		if rows, err = tx.Query(ctx, `SELECT * FROM taskiem_ops_group_actions($1)`, id); err != nil {
			return err
		}
		if acts, err = pgx.CollectRows(rows, pgx.RowToStructByPos[opsAction]); err != nil {
			return err
		}
		if g == nil && len(acts) == 0 && len(reports) == 0 {
			return errOpsNotFound
		}
		return ops.AuditTx(ctx, tx, o.Actor(), "ops.people.group.open", id.String(), map[string]any{"why": why, "members": len(members)})
	})
	if errors.Is(err, errOpsNotFound) {
		writeErr(w, http.StatusNotFound, "no such group")
		return
	}
	if err != nil {
		s.opsDBFail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"group": g, "gone": g == nil, "members": nonNil(members), "reports": nonNil(reports), "actions": nonNil(acts)})
}

// opsModerateGroup is POST /v1/ops/people/groups/{id}/actions {action:
// link_off|close, reason, report_id?, step_up}: turns the group's invite
// link off (its admins can't make a new one while it is in force), or
// closes the group: no one sends in it or joins it, its members are told
// in the group in plain words, and it is deleted with all its messages
// (§14) AppealWindow later unless its admins appeal and win. Its admins
// get a push. Recorded with the reason, the operator and the time.
func (s *Server) opsModerateGroup(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id, ok := opsID(w, r, "id", "group")
	if !ok {
		return
	}
	var req struct {
		Action   string     `json:"action"`
		Reason   string     `json:"reason"`
		ReportID *uuid.UUID `json:"report_id,omitempty"`
		StepUp   *opsStepUp `json:"step_up,omitempty"`
	}
	if err := decodeBody(r, &req); err != nil {
		s.fail(w, r, err)
		return
	}
	req.Reason = strings.TrimSpace(req.Reason)
	action := map[string]string{"link_off": peoplesafety.ActionGroupLinkOff, "close": peoplesafety.ActionGroupClose}[req.Action]
	if action == "" {
		writeErr(w, http.StatusUnprocessableEntity, "the action is link_off or close")
		return
	}
	if req.Reason == "" || utf8.RuneCountInString(req.Reason) > 1000 {
		writeErr(w, http.StatusUnprocessableEntity, "a reason is needed (up to 1,000 characters): the group's admins are told it")
		return
	}
	var until *time.Time
	if action == peoplesafety.ActionGroupClose {
		u := time.Now().Add(peoplesafety.AppealWindow)
		until = &u
	}
	op := "ops.people.group." + req.Action
	if !s.checkOpsStepUp(w, r, req.StepUp, op, id.String()) {
		return
	}
	o := operatorFrom(ctx)
	var actionID uuid.UUID
	err := s.opsTx(r, func(tx pgx.Tx, _ func(uuid.UUID) error) error {
		if err := tx.QueryRow(ctx, `SELECT taskiem_ops_group_moderate($1, $2, $3, $4, $5, $6)`, id, action, req.Reason, o.Actor(), req.ReportID, until).
			Scan(&actionID); err != nil {
			return err
		}
		return ops.AuditTx(ctx, tx, o.Actor(), op, id.String(), map[string]any{"reason": req.Reason, "until": until, "report": req.ReportID, "action": actionID})
	})
	if err != nil {
		s.opsDBFail(w, r, err)
		return
	}
	if action == peoplesafety.ActionGroupClose {
		s.tellGroupAdmins(r, id, "Taskiem closed a group you run", req.Reason+" "+groupClosedWords(until))
	} else {
		s.tellGroupAdmins(r, id, "Taskiem turned off your group's invite link", req.Reason)
	}
	writeJSON(w, http.StatusCreated, map[string]any{"id": actionID, "action": req.Action, "until": until})
}

// tellGroupAdmins tells a group's admins now of a moderator's decision on
// their own devices (a push on You opening the group), as tellStanding
// tells a person.
func (s *Server) tellGroupAdmins(r *http.Request, group uuid.UUID, title, body string) {
	ctx := r.Context()
	var admins []uuid.UUID
	err := s.opsTx(r, func(tx pgx.Tx, _ func(uuid.UUID) error) error {
		rows, err := tx.Query(ctx, `SELECT person_id FROM taskiem_ops_group_members($1) WHERE role = 'admin' AND left_at IS NULL AND NOT deleted`, group)
		if err != nil {
			return err
		}
		admins, err = pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
		return err
	})
	if err != nil {
		s.Logger.Warn("people safety: a group's admins", "err", err)
		return
	}
	for _, a := range admins {
		s.tellYou(ctx, a, title, body, "/you/groups/"+group.String(), "group-standing:"+group.String())
	}
}

// tellYou is a push of kind you to a person's own devices, with a link.
func (s *Server) tellYou(ctx context.Context, person uuid.UUID, title, body, link, tag string) {
	if s.Push == nil || !s.Push.Enabled() {
		return
	}
	err := db.InPersonTx(ctx, s.Store.Pool, person, func(tx pgx.Tx) error {
		live, err := push.LiveYouTx(ctx, tx, person, s.Push.WebPushOn(), s.Push.FCMOn())
		if err != nil || !live {
			return err
		}
		if len([]rune(body)) > 160 {
			body = string([]rune(body)[:160]) + "…"
		}
		return push.EnqueueTx(ctx, tx, uuid.Nil, []uuid.UUID{person}, push.Queued{Kind: push.KindYou, Title: title, Body: body, Link: link, Tag: tag})
	})
	if err != nil {
		s.Logger.Warn("people safety: push", "err", err)
	}
}

// closeGroupsDue carries out the group closures whose day came with no
// appeal open, each in its own transaction (taskiem_group_close_carry
// locks the closure and checks it again), audited people.group.close with
// what became of it.
func (s *Server) closeGroupsDue(ctx context.Context) error {
	var due [][2]uuid.UUID
	err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{ops.PlatformChain}, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT action_id, group_id FROM taskiem_group_closures_due()`)
		if err != nil {
			return err
		}
		due, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) ([2]uuid.UUID, error) {
			var a [2]uuid.UUID
			return a, r.Scan(&a[0], &a[1])
		})
		return err
	})
	if err != nil {
		return err
	}
	var errs []error
	for _, d := range due {
		err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{ops.PlatformChain}, func(tx pgx.Tx) error {
			var outcome string
			if err := tx.QueryRow(ctx, `SELECT taskiem_group_close_carry($1)`, d[0]).Scan(&outcome); err != nil || outcome == "not_due" {
				return err
			}
			return ops.AuditTx(ctx, tx, "taskiem:safety", "people.group.close", d[1].String(), map[string]any{"action": d[0], "outcome": outcome})
		})
		if err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}
