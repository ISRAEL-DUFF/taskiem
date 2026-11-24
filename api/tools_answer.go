package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/history"
	"github.com/israel-duff/taskiem/engine/pii"
	"github.com/israel-duff/taskiem/engine/wd"
)

// A tool's answer (made tools §7): drawn by the API when it is asked for,
// over the completed run's outputs only (and a list's item), never in the
// run. Outputs are opened, an output declared x-pii is masked whatever its
// name, and every word shown is masked as messages are. Amounts are given
// in kobo for the screen to draw as naira.

// madeAnswer is a run's answer as data.
type madeAnswer struct {
	// Status is the run's: running, waiting, queued, completed, failed,
	// cancelled.
	Status string `json:"status"`
	// Type is text, card or list for a tool's answer; outcome when the run
	// has none (it is not a tool, has no answer, or has not completed).
	Type    string       `json:"type"`
	Tool    string       `json:"tool,omitempty"`
	Text    string       `json:"text,omitempty"`
	Title   string       `json:"title,omitempty"`
	Ref     string       `json:"ref,omitempty"`
	State   *answerState `json:"state,omitempty"`
	Lines   []answerLine `json:"lines,omitempty"`
	Amount  *int64       `json:"amount_kobo,omitempty"`
	Buttons []answerBtn  `json:"buttons,omitempty"`
	Rows    []answerRow  `json:"rows,omitempty"`
	Empty   string       `json:"empty,omitempty"`
	// Why a failed run failed, in plain words.
	Error string `json:"error,omitempty"`
	// WaitingFor is who a run on its way waits for (made tools §7, "Waiting
	// for Ada to approve"): an open approval's approvers, an open form's
	// recipient, in names or roles only.
	WaitingFor []answerWait `json:"waiting_for,omitempty"`
}

// answerWait is one step a run waits on, and who it waits for.
type answerWait struct {
	// Kind is approval or form.
	Kind string `json:"kind"`
	// Who in words: "Ada", "you", "an owner", "someone with the role
	// manager", "the cashiers", "Bayo Stores".
	Who string `json:"who"`
	// You: it waits for the person asking (a form put to them, or an
	// approval only they can give).
	You bool `json:"you,omitempty"`
	// Request is a form put to the person asking: its id, to open under To
	// fill in.
	Request string `json:"request,omitempty"`
}

// The answer's parts, drawn by wd.DrawAnswer (as wd-test/v1 draws them).
type (
	answerState = wd.DrawnState
	answerLine  = wd.DrawnLine
	answerBtn   = wd.DrawnButton
	answerRow   = wd.DrawnRow
)

// runAnswer is GET /v1/runs/{run}/answer: the answer of a tool's run, for
// whoever may read runs or started it.
func (s *Server) runAnswer(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "run"))
	if err != nil {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	p := principalFrom(r.Context())
	a, err := s.answerOf(r.Context(), p.TenantID, id, func(startedBy, env string) bool {
		// An environment-scoped key reads only its environment's runs.
		if p.Environment != "" && env != p.Environment {
			return false
		}
		return p.Can(PermRunRead) || startedBy == p.Actor()
	}, buttonRows)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if stillGoing(a.Status) {
		viewer := uuid.Nil
		if p.EndUser == nil {
			viewer = p.UserID
		}
		if a.WaitingFor, err = s.waitingFor(r.Context(), p.TenantID, id, viewer); err != nil {
			s.fail(w, r, err)
			return
		}
	}
	writeJSON(w, http.StatusOK, a)
}

// answerOf draws run id's answer; may says whether the asker may see the
// run (by who started it). rows is how many of a list's first rows get
// their buttons (none past them: they are pressed from a narrower list).
func (s *Server) answerOf(ctx context.Context, tenant, id uuid.UUID, may func(startedBy, env string) bool, rows int) (madeAnswer, error) {
	return s.answerAs(ctx, tenant, id, may, rows, false)
}

// answerAs draws run id's answer for a member, or for a customer (made
// tools §11): only the outputs the workflow marks public are drawn (the
// rest dropped before the answer's expressions see them, not masked),
// its buttons open only customers' tools, and a failure is not said in
// the business's words.
func (s *Server) answerAs(ctx context.Context, tenant, id uuid.UUID, may func(startedBy, env string) bool, rows int, customer bool) (madeAnswer, error) {
	var a madeAnswer
	var wf uuid.UUID
	var version int
	var startedBy *string
	var env string
	var def []byte
	var outputs map[string]any
	err := db0(ctx, s, tenant, func(tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `SELECT r.workflow_id, r.version, r.status, r.started_by, r.environment, wv.definition FROM runs r
			JOIN workflow_versions wv ON wv.workflow_id = r.workflow_id AND wv.version = r.version WHERE r.id = $1`, id).
			Scan(&wf, &version, &a.Status, &startedBy, &env, &def); err != nil {
			return err
		}
		if !may(strOr(startedBy), env) {
			return pgx.ErrNoRows
		}
		if a.Status != "completed" {
			return nil
		}
		var payload []byte
		err := tx.QueryRow(ctx, `SELECT payload FROM run_events WHERE run_id = $1 AND type = $2 ORDER BY seq DESC LIMIT 1`, id, history.RunCompleted).Scan(&payload)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		var done history.RunCompletedPayload
		_ = json.Unmarshal(payload, &done)
		// Personal data the run sealed is never opened for an answer: it
		// shows masked, and the full value only to whoever may open the run.
		outputs, _ = maskEnvelopes(any(done.Outputs)).(map[string]any)
		return nil
	})
	if err != nil {
		return a, err
	}
	a.Type = "outcome"
	if a.Status == "failed" && !customer {
		a.Error = maskWords(s.runFailureText(ctx, tenant, id))
	}
	d, err := s.definitionFor(wf, version, def)
	if err != nil || d.Tool == nil {
		return a, nil //nolint:nilerr // a run of no tool answers with its outcome
	}
	if !customer {
		// A customer is not told the business's own words for its tools.
		a.Tool = d.Tool.Command
	}
	if a.Status != "completed" || d.Tool.Answer == nil {
		return a, nil
	}
	if outputs == nil {
		outputs = map[string]any{}
	}
	audience := wd.ToolForMembers
	if customer {
		outputs = wd.PublicOutputs(d, outputs)
		audience = wd.ToolForCustomers
	}
	maskDeclared(d, outputs)
	drawAnswer(&a, d.Tool.Answer, outputs)
	if err := s.mintAnswerButtons(ctx, tenant, env, id, &a, rows, audience); err != nil {
		return a, err
	}
	return a, nil
}

// maskDeclared masks the outputs declared personal data whatever their
// names: a phone or an account to its last four, anything else hidden.
func maskDeclared(d *wd.Definition, outputs map[string]any) {
	for _, o := range d.Outputs {
		if o.PII == "" {
			continue
		}
		if v, ok := outputs[o.Name]; ok {
			outputs[o.Name] = maskValue(v)
		}
	}
}

func maskValue(v any) any {
	s, ok := v.(string)
	if !ok {
		if v == nil {
			return nil
		}
		return "•••"
	}
	r := []rune(s)
	if len(r) > 6 {
		return "••" + string(r[len(r)-4:])
	}
	return "•••"
}

// drawAnswer fills a from the tool's answer over outputs (wd.DrawAnswer),
// its words masked as messages are.
func drawAnswer(a *madeAnswer, ans *wd.Answer, outputs map[string]any) {
	d := wd.DrawAnswer(ans, outputs, maskWords)
	a.Type, a.Text, a.Title, a.Ref, a.State, a.Lines = d.Type, d.Text, d.Title, d.Ref, d.State, d.Lines
	a.Amount, a.Buttons, a.Rows, a.Empty = d.Amount, d.Buttons, d.Rows, d.Empty
}

func strOr(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// maskEnvelopes replaces every value the run sealed as personal data with a
// mask, at any depth, without opening it.
func maskEnvelopes(v any) any {
	if pii.IsEnvelope(v) {
		return "•••"
	}
	switch x := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, e := range x {
			out[k] = maskEnvelopes(e)
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, e := range x {
			out[i] = maskEnvelopes(e)
		}
		return out
	}
	return v
}
