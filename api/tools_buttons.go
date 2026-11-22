package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/whatsapp"
)

// Buttons on answers and on Tell me messages (made tools M2, §10). The
// server mints each one as a row (tool_buttons) when it draws the answer
// or sends the message: a press names the row by its id, which nobody can
// guess and which is read only inside its business. A press fills the form
// of the tool the button names, as whoever pressed it, held to that tool's
// roles and care: it never runs anything by itself. A button is held by the
// target workflow, so renaming a command keeps it working, and it lasts
// seven days. A once button is spent by the first run started from it; the
// next person is told who took it and when.

// buttonFor is how long a button works.
const buttonFor = 7 * 24 * time.Hour

// buttonReplyPrefix is a chat reply button's payload: "tb:<id>".
const buttonReplyPrefix = "tb:"

// buttonNS makes a button's id from where it was drawn (a run and its
// place in the answer), so drawing an answer again mints no new rows.
var buttonNS = uuid.MustParse("5d3b8f0e-6a8c-4f7e-9c1d-2b4a6e8f0a1c")

// mintButton records a button drawn from run (at place) naming command in
// env, and gives its id; uuid.Nil when no tool has that command there (the
// button is then left out: "This is not available").
func mintButton(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, env string, run uuid.UUID, place string, b answerBtn) (uuid.UUID, error) {
	var wf uuid.UUID
	err := tx.QueryRow(ctx, `SELECT workflow_id FROM workflow_tools WHERE environment = $1 AND command = $2`, env, b.Tool).Scan(&wf)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, nil
	}
	if err != nil {
		return uuid.Nil, err
	}
	id := uuid.NewSHA1(buttonNS, []byte(tenant.String()+"/"+run.String()+"/"+place))
	inputs, _ := json.Marshal(b.Inputs)
	if b.Inputs == nil {
		inputs = []byte("{}")
	}
	_, err = tx.Exec(ctx, `INSERT INTO tool_buttons (id, tenant_id, environment, workflow_id, label, inputs, once, from_run, expires_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9) ON CONFLICT (id) DO NOTHING`,
		id, tenant, env, wf, b.Label, inputs, b.Once, run, time.Now().Add(buttonFor))
	return id, err
}

// mintAnswerButtons gives each of an answer's buttons (a card's, each
// row's) its id; a button whose tool is not there is left out.
func (s *Server) mintAnswerButtons(ctx context.Context, tenant uuid.UUID, env string, run uuid.UUID, a *madeAnswer) error {
	return db0(ctx, s, tenant, func(tx pgx.Tx) error {
		mint := func(place string, bs []answerBtn) ([]answerBtn, error) {
			var out []answerBtn
			for i, b := range bs {
				id, err := mintButton(ctx, tx, tenant, env, run, place+"/"+strconv.Itoa(i), b)
				if err != nil {
					return nil, err
				}
				if id == uuid.Nil {
					continue
				}
				b.Token = id.String()
				out = append(out, b)
			}
			return out, nil
		}
		var err error
		if a.Buttons, err = mint("card", a.Buttons); err != nil {
			return err
		}
		for i := range a.Rows {
			if a.Rows[i].Actions, err = mint("row/"+strconv.Itoa(i), a.Rows[i].Actions); err != nil {
				return err
			}
		}
		return nil
	})
}

// pressed is a button as its presser gets it: the tool to open, and the
// values to fill its form with.
type pressed struct {
	Button  uuid.UUID      `json:"button"`
	Command string         `json:"command"`
	Name    string         `json:"name"`
	Inputs  map[string]any `json:"inputs"`
	Once    bool           `json:"once"`
	tool    toolRow
}

// errButtonTaken is a once button someone has used.
type errButtonTaken struct {
	By string
	At time.Time
}

func (e errButtonTaken) Error() string {
	return e.By + " took it at " + e.At.In(lagosTZ).Format("15:04 on 2 Jan")
}

// pressButton reads a button for p: its tool as deployed now (held to the
// workflow's roles), and its values. A once button already used is
// errButtonTaken; an expired or unknown one is not found.
func (s *Server) pressButton(ctx context.Context, p *Principal, id uuid.UUID) (pressed, error) {
	var out pressed
	err := db0(ctx, s, p.TenantID, func(tx pgx.Tx) error {
		var env string
		var wf uuid.UUID
		var inputs []byte
		var expires time.Time
		var usedBy *uuid.UUID
		var usedAt *time.Time
		err := tx.QueryRow(ctx, `SELECT environment, workflow_id, inputs, once, expires_at, used_by, used_at FROM tool_buttons WHERE id = $1`, id).
			Scan(&env, &wf, &inputs, &out.Once, &expires, &usedBy, &usedAt)
		if errors.Is(err, pgx.ErrNoRows) || err == nil && time.Now().After(expires) {
			return fmt.Errorf("%w: this button is not available any more", errNotFound)
		}
		if err != nil {
			return err
		}
		if out.Once && usedAt != nil {
			by := "someone"
			if usedBy != nil {
				_ = tx.QueryRow(ctx, `SELECT COALESCE(NULLIF(name, ''), split_part(email, '@', 1)) FROM users WHERE id = $1`, *usedBy).Scan(&by)
			}
			return errButtonTaken{By: by, At: *usedAt}
		}
		t, err := scanTool(tx.QueryRow(ctx, `SELECT `+toolCols+` FROM workflow_tools WHERE environment = $1 AND workflow_id = $2`, env, wf))
		if errors.Is(err, pgx.ErrNoRows) || err == nil && t.Audience != "members" {
			return fmt.Errorf("%w: this button's tool is not available any more", errNotFound)
		}
		if err != nil {
			return err
		}
		if !mayStart(p, t.Roles) {
			return errNotYours(t.Name, t.Roles)
		}
		out.tool, out.Button, out.Command, out.Name = t, id, t.Command, t.Name
		return json.Unmarshal(inputs, &out.Inputs)
	})
	return out, err
}

// spendButton marks a once button used by p for run, if no one has; a
// button that is not once is not spent. errButtonTaken when someone was
// first.
func (s *Server) spendButton(ctx context.Context, tx pgx.Tx, p *Principal, id, run uuid.UUID) error {
	tag, err := tx.Exec(ctx, `UPDATE tool_buttons SET used_by = $2, used_at = now(), used_run = $3 WHERE id = $1 AND once AND used_at IS NULL`, id, p.UserID, run)
	if err != nil || tag.RowsAffected() == 1 {
		return err
	}
	var once bool
	var usedBy *uuid.UUID
	var usedAt *time.Time
	if err := tx.QueryRow(ctx, `SELECT once, used_by, used_at FROM tool_buttons WHERE id = $1`, id).Scan(&once, &usedBy, &usedAt); err != nil || !once {
		return err
	}
	by := "someone"
	if usedBy != nil {
		_ = tx.QueryRow(ctx, `SELECT COALESCE(NULLIF(name, ''), split_part(email, '@', 1)) FROM users WHERE id = $1`, *usedBy).Scan(&by)
	}
	return errButtonTaken{By: by, At: *usedAt}
}

// claimButton spends a once button before its run starts: the run's id
// is recorded after (recordButtonRun). A button that is not once passes.
func (s *Server) claimButton(ctx context.Context, p *Principal, id uuid.UUID) error {
	if id == uuid.Nil {
		return nil
	}
	return db0(ctx, s, p.TenantID, func(tx pgx.Tx) error { return s.spendButton(ctx, tx, p, id, uuid.Nil) })
}

// recordButtonRun records the run a once button started.
func (s *Server) recordButtonRun(ctx context.Context, p *Principal, id, run uuid.UUID) {
	if id == uuid.Nil {
		return
	}
	_ = db0(ctx, s, p.TenantID, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `UPDATE tool_buttons SET used_run = $2 WHERE id = $1 AND used_by = $3`, id, run, p.UserID)
		return err
	})
}

// pressButtonRoute is POST /v1/tools/buttons/{button}: the tool a button
// opens and the values it fills in, for whoever pressed it.
func (s *Server) pressButtonRoute(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "button"))
	if err != nil {
		writeErr(w, http.StatusNotFound, "this button is not available any more")
		return
	}
	out, err := s.pressButton(r.Context(), principalFrom(r.Context()), id)
	var taken errButtonTaken
	if errors.As(err, &taken) {
		writeJSON(w, http.StatusConflict, map[string]any{"error": "Already done: " + taken.Error() + ".", "taken_by": taken.By, "taken_at": taken.At})
		return
	}
	if err != nil {
		s.toolRefused(w, r, err, "button")
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// chatButtons are an answer's card buttons as a chat's reply buttons
// (three at most, labels cut to the chat's twenty characters).
func chatButtons(bs []answerBtn) []whatsapp.Button {
	var out []whatsapp.Button
	for _, b := range bs {
		if b.Token == "" || len(out) == 3 {
			continue
		}
		label := b.Label
		if r := []rune(label); len(r) > 20 {
			label = string(r[:20])
		}
		out = append(out, whatsapp.Button{ID: buttonReplyPrefix + b.Token, Title: strings.TrimSpace(label)})
	}
	return out
}
