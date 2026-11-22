package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/expr"
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
}

type answerState struct {
	Text string `json:"text"`
	Tone string `json:"tone,omitempty"`
}

type answerLine struct {
	Label string `json:"label"`
	Value string `json:"value"`
}

type answerBtn struct {
	Label  string         `json:"label"`
	Tool   string         `json:"tool"`
	Inputs map[string]any `json:"inputs,omitempty"`
	Once   bool           `json:"once,omitempty"`
}

type answerRow struct {
	Title   string       `json:"title"`
	Sub     string       `json:"sub,omitempty"`
	State   *answerState `json:"state,omitempty"`
	Actions []answerBtn  `json:"actions,omitempty"`
}

var answerEngine = expr.MustNewWithRoots("outputs", "item")

// runAnswer is GET /v1/runs/{run}/answer: the answer of a tool's run, for
// whoever may read runs or started it.
func (s *Server) runAnswer(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "run"))
	if err != nil {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	p := principalFrom(r.Context())
	a, err := s.answerOf(r.Context(), p.TenantID, id, func(startedBy string) bool { return p.Can(PermRunRead) || startedBy == p.Actor() })
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, a)
}

// answerOf draws run id's answer; may says whether the asker may see the
// run (by who started it).
func (s *Server) answerOf(ctx context.Context, tenant, id uuid.UUID, may func(startedBy string) bool) (madeAnswer, error) {
	var a madeAnswer
	var wf uuid.UUID
	var version int
	var startedBy *string
	var def []byte
	var outputs map[string]any
	err := db0(ctx, s, tenant, func(tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `SELECT r.workflow_id, r.version, r.status, r.started_by, wv.definition FROM runs r
			JOIN workflow_versions wv ON wv.workflow_id = r.workflow_id AND wv.version = r.version WHERE r.id = $1`, id).
			Scan(&wf, &version, &a.Status, &startedBy, &def); err != nil {
			return err
		}
		if !may(strOr(startedBy)) {
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
		opened, err := pii.Open(ctx, s.Store.PII, tx, tenant, done.Outputs, pii.Taint{})
		if err != nil {
			return err
		}
		outputs, _ = opened.(map[string]any)
		return nil
	})
	if err != nil {
		return a, err
	}
	a.Type = "outcome"
	if a.Status == "failed" {
		a.Error = maskWords(s.runFailureText(ctx, tenant, id))
	}
	d, err := s.definitionFor(wf, version, def)
	if err != nil || d.Tool == nil {
		return a, nil //nolint:nilerr // a run of no tool answers with its outcome
	}
	a.Tool = d.Tool.Command
	if a.Status != "completed" || d.Tool.Answer == nil {
		return a, nil
	}
	if outputs == nil {
		outputs = map[string]any{}
	}
	maskDeclared(d, outputs)
	drawAnswer(&a, d.Tool.Answer, outputs)
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

// drawAnswer fills a from the tool's answer over outputs. A value that
// cannot be worked out is left out rather than failing the answer.
func drawAnswer(a *madeAnswer, ans *wd.Answer, outputs map[string]any) {
	a.Type = ans.Type
	act := map[string]any{"outputs": outputs}
	word := func(v string, act map[string]any) string { return maskWords(answerWord(v, act)) }
	state := func(st *wd.AnswerState, act map[string]any) *answerState {
		if st == nil {
			return nil
		}
		return &answerState{Text: word(st.Text, act), Tone: st.Tone}
	}
	buttons := func(bs []wd.Button, act map[string]any) []answerBtn {
		var out []answerBtn
		for _, b := range bs {
			in := map[string]any{}
			for k, v := range b.Inputs {
				if got, err := answerEngine.Resolve(v, act, false); err == nil {
					in[k] = got
				}
			}
			out = append(out, answerBtn{Label: b.Label, Tool: b.Tool, Inputs: in, Once: b.Once})
		}
		return out
	}
	switch ans.Type {
	case "text":
		text := word(ans.Text, act)
		if r := []rune(text); len(r) > wd.AnswerTextMax {
			text = string(r[:wd.AnswerTextMax-1]) + "…"
		}
		a.Text = text
	case "card":
		a.Title, a.Ref, a.State = word(ans.Title, act), word(ans.Ref, act), state(ans.State, act)
		for _, l := range ans.Lines {
			a.Lines = append(a.Lines, answerLine{Label: l.Label, Value: word(l.Value, act)})
		}
		if ans.Amount != "" {
			if v, err := answerEngine.Resolve(ans.Amount, act, false); err == nil {
				if k, ok := toKobo(v); ok {
					a.Amount = &k
				}
			}
		}
		a.Buttons = buttons(ans.Buttons, act)
	case "list":
		a.Title, a.Empty = word(ans.Title, act), ans.Empty
		items, _ := outputs[ans.From].([]any)
		a.Rows = []answerRow{}
		for _, it := range items {
			ia := map[string]any{"outputs": outputs, "item": it}
			row := answerRow{Actions: buttons(ans.Actions, ia)}
			if ans.Item != nil {
				row.Title, row.Sub, row.State = word(ans.Item.Title, ia), word(ans.Item.Sub, ia), state(ans.Item.State, ia)
			}
			a.Rows = append(a.Rows, row)
		}
	}
}

// answerWord works out one of the answer's values as words: text as it
// is, an expression's result written plainly.
func answerWord(v string, act map[string]any) string {
	if !strings.HasPrefix(v, "=") {
		return v
	}
	got, err := answerEngine.Eval(v, act)
	if err != nil {
		return ""
	}
	switch x := got.(type) {
	case nil:
		return ""
	case string:
		return x
	case float64:
		if x == float64(int64(x)) {
			return fmt.Sprintf("%d", int64(x))
		}
		return fmt.Sprintf("%g", x)
	case int64:
		return fmt.Sprintf("%d", x)
	case bool:
		if x {
			return "yes"
		}
		return "no"
	}
	b, _ := json.Marshal(got)
	return string(b)
}

func toKobo(v any) (int64, bool) {
	switch x := v.(type) {
	case int64:
		return x, true
	case float64:
		return int64(x), true
	case int:
		return int64(x), true
	}
	return 0, false
}

func strOr(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
