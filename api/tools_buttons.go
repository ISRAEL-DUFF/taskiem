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

	"github.com/israel-duff/taskiem/engine/wd"
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

// mintButton records a button drawn from run (at place) naming command in
// env, and gives its id: the one minted before for that place, while it
// lasts. uuid.Nil leaves the button out: no tool has that command there,
// or the button's seven days are over. The id is random. Inputs are
// masked as messages are, at any depth, so a button carries nothing its
// presser could not read in the answer.
func mintButton(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, env string, run uuid.UUID, place string, b answerBtn) (uuid.UUID, error) {
	return mintButtonFor(ctx, tx, tenant, env, run, place, b, wd.ToolForMembers)
}

// mintButtonFor is mintButton for the tools of an audience: a button on a
// member's answer opens only members' tools, one on a customer's answer
// only customers' tools (made tools §11); any other is left out.
func mintButtonFor(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, env string, run uuid.UUID, place string, b answerBtn, audience string) (uuid.UUID, error) {
	var id uuid.UUID
	var expires time.Time
	err := tx.QueryRow(ctx, `SELECT id, expires_at FROM tool_buttons WHERE from_run = $1 AND place = $2`, run, place).Scan(&id, &expires)
	switch {
	case err == nil && time.Now().After(expires):
		return uuid.Nil, nil
	case err == nil:
		return id, nil
	case !errors.Is(err, pgx.ErrNoRows):
		return uuid.Nil, err
	}
	var wf uuid.UUID
	err = tx.QueryRow(ctx, `SELECT workflow_id FROM workflow_tools WHERE environment = $1 AND command = $2 AND audience = $3`, env, b.Tool, audience).Scan(&wf)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, nil
	}
	if err != nil {
		return uuid.Nil, err
	}
	in, _ := maskDeep(maskEnvelopes(any(b.Inputs))).(map[string]any)
	inputs, _ := json.Marshal(in)
	if in == nil {
		inputs = []byte("{}")
	}
	id = uuid.New()
	err = tx.QueryRow(ctx, `INSERT INTO tool_buttons (id, tenant_id, environment, workflow_id, label, inputs, once, from_run, place, expires_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		ON CONFLICT (tenant_id, from_run, place) WHERE from_run IS NOT NULL DO UPDATE SET place = EXCLUDED.place
		RETURNING id`, id, tenant, env, wf, b.Label, inputs, b.Once, run, place, time.Now().Add(buttonFor)).Scan(&id)
	return id, err
}

// maskDeep masks every word of a value, at any depth, as messages are.
func maskDeep(v any) any {
	switch x := v.(type) {
	case string:
		return maskWords(x)
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, e := range x {
			out[k] = maskDeep(e)
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, e := range x {
			out[i] = maskDeep(e)
		}
		return out
	}
	return v
}

// masked reports whether a value holds a masked word (•), at any depth: a
// button's value that was masked is asked, not filled in.
func masked(v any) bool {
	switch x := v.(type) {
	case string:
		return strings.Contains(x, "•")
	case map[string]any:
		for _, e := range x {
			if masked(e) {
				return true
			}
		}
	case []any:
		for _, e := range x {
			if masked(e) {
				return true
			}
		}
	}
	return false
}

// buttonRows is how many of a list's rows get their buttons minted.
const buttonRows = 100

// mintAnswerButtons gives each of an answer's buttons (a card's, each of
// the first rows rows') its id; a button whose tool is not there is left
// out.
func (s *Server) mintAnswerButtons(ctx context.Context, tenant uuid.UUID, env string, run uuid.UUID, a *madeAnswer, rows int, audience string) error {
	// A customer's answer has buttons of its own, apart from those a
	// member drawing the same run is given.
	prefix := ""
	if audience != wd.ToolForMembers {
		prefix = audience + "/"
	}
	return db0(ctx, s, tenant, func(tx pgx.Tx) error {
		mint := func(place string, bs []answerBtn) ([]answerBtn, error) {
			var out []answerBtn
			for i, b := range bs {
				id, err := mintButtonFor(ctx, tx, tenant, env, run, prefix+place+"/"+strconv.Itoa(i), b, audience)
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
			if i >= min(rows, buttonRows) {
				a.Rows[i].Actions = nil // pressed from a narrower list
				continue
			}
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
		// A key kept to an environment presses only its environment's.
		if p.Environment != "" && env != p.Environment {
			return fmt.Errorf("%w: this button is not available any more", errNotFound)
		}
		t, err := scanTool(tx.QueryRow(ctx, `SELECT `+toolCols+` FROM workflow_tools WHERE environment = $1 AND workflow_id = $2`, env, wf))
		if errors.Is(err, pgx.ErrNoRows) || err == nil && t.Audience != "members" {
			return fmt.Errorf("%w: this button's tool is not available any more", errNotFound)
		}
		if err != nil {
			return err
		}
		out.tool, out.Button, out.Command, out.Name = t, id, t.Command, t.Name
		if !mayStart(p, t.Roles) {
			return errNotYours(t.Name, t.Roles)
		}
		if err := json.Unmarshal(inputs, &out.Inputs); err != nil {
			return err
		}
		// A value shown masked is asked, not filled in with its mask.
		for k, v := range out.Inputs {
			if masked(v) {
				delete(out.Inputs, k)
			}
		}
		return nil
	})
	return out, err
}

// spendButton marks a once button used by p for run, if no one else has,
// while it lasts; a button that is not once is not spent. The same person
// pressing again (a retry) passes: the run's own key starts it once.
// errButtonTaken when someone else was first.
func (s *Server) spendButton(ctx context.Context, tx pgx.Tx, p *Principal, id, run uuid.UUID) error {
	tag, err := tx.Exec(ctx, `UPDATE tool_buttons SET used_by = $2, used_at = now(), used_run = NULLIF($3, '00000000-0000-0000-0000-000000000000'::uuid)
		WHERE id = $1 AND once AND used_at IS NULL AND expires_at > now()`, id, p.UserID, run)
	if err != nil || tag.RowsAffected() == 1 {
		return err
	}
	var once bool
	var usedBy *uuid.UUID
	var usedAt *time.Time
	var expires time.Time
	if err := tx.QueryRow(ctx, `SELECT once, used_by, used_at, expires_at FROM tool_buttons WHERE id = $1`, id).Scan(&once, &usedBy, &usedAt, &expires); err != nil || !once {
		return err
	}
	if usedAt == nil && time.Now().After(expires) {
		return fmt.Errorf("%w: this button is not available any more", errNotFound)
	}
	if usedBy != nil && *usedBy == p.UserID {
		return nil
	}
	by := "someone"
	if usedBy != nil {
		_ = tx.QueryRow(ctx, `SELECT COALESCE(NULLIF(name, ''), split_part(email, '@', 1)) FROM users WHERE id = $1`, *usedBy).Scan(&by)
	}
	return errButtonTaken{By: by, At: *usedAt}
}

// claimButton spends a once button before its run starts: the run's id
// is recorded after (recordButtonRun), and the button is given back if the
// run does not start (releaseButton). A button that is not once passes;
// a key cannot press one.
func (s *Server) claimButton(ctx context.Context, p *Principal, id uuid.UUID) error {
	if id == uuid.Nil {
		return nil
	}
	if p.UserID == uuid.Nil || p.KeyID != uuid.Nil {
		return fmt.Errorf("%w: a button is pressed by a person", errForbidden)
	}
	return db0(ctx, s, p.TenantID, func(tx pgx.Tx) error { return s.spendButton(ctx, tx, p, id, uuid.Nil) })
}

// releaseButton gives a once button back when the run it was claimed for
// did not start (a plan limit, an error): nobody is told it was taken.
func (s *Server) releaseButton(ctx context.Context, p *Principal, id uuid.UUID) {
	if id == uuid.Nil {
		return
	}
	_ = db0(ctx, s, p.TenantID, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `UPDATE tool_buttons SET used_by = NULL, used_at = NULL WHERE id = $1 AND used_by = $2 AND used_run IS NULL`, id, p.UserID)
		return err
	})
}

// recordButtonRun records the run a once button started.
func (s *Server) recordButtonRun(ctx context.Context, p *Principal, id, run uuid.UUID) {
	if id == uuid.Nil {
		return
	}
	_ = db0(ctx, s, p.TenantID, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `UPDATE tool_buttons SET used_run = $2 WHERE id = $1 AND used_by = $3 AND used_run IS NULL`, id, run, p.UserID)
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
		s.toolRefused(w, r, err, out.Command)
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
