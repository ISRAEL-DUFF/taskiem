package api

import (
	"context"
	"net/http"
	"slices"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/wd"
	"github.com/israel-duff/taskiem/engine/wdcheck"
)

// Pages H5, g-pages-editor (docs/spec/pages.md §10, 5): what the editor's
// plain-words path offers beside the tables, for page.edit holders: the
// business's members' tools in use (which answer at once, so may feed a
// part; their outputs and the values they ask), and its workflows in use
// (a run's status; the signals a page may send them; the approvals they
// wait at). Names and types only, never a run or a row (§8: the builder
// sees no rows). Check holds every choice again when the page is saved.

type pageChoiceOutput struct {
	Name  string `json:"name"`
	Title string `json:"title"`
	Type  string `json:"type"`
}

type pageChoiceInput struct {
	Name     string `json:"name"`
	Title    string `json:"title"`
	Personal bool   `json:"personal"`
}

type pageChoiceTool struct {
	Command string             `json:"command"`
	Name    string             `json:"name"`
	Answers bool               `json:"answers"`
	Outputs []pageChoiceOutput `json:"outputs"`
	Inputs  []pageChoiceInput  `json:"inputs"`
	// List is how the tool's own list answer reads each row (from, and
	// the item's title and line), so a list part over it starts the same.
	List *pageChoiceList `json:"list,omitempty"`
}

type pageChoiceList struct {
	From  string `json:"from"`
	Title string `json:"title"`
	Sub   string `json:"sub,omitempty"`
}

type pageChoiceStep struct {
	Step string `json:"step"`
	Name string `json:"name"`
}

type pageChoiceWorkflow struct {
	Name      string           `json:"name"`
	Signals   []string         `json:"signals"`
	Approvals []pageChoiceStep `json:"approvals"`
}

// listPageChoices is GET /v1/page-choices.
func (s *Server) listPageChoices(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	tools := []pageChoiceTool{}
	flows := []pageChoiceWorkflow{}
	if err := s.tx(r, func(tx pgx.Tx) error {
		var err error
		if tools, err = s.pageChoiceTools(ctx, tx); err != nil {
			return err
		}
		flows, err = s.pageChoiceWorkflows(ctx, tx)
		return err
	}); err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"tools": tools, "workflows": flows})
}

func (s *Server) pageChoiceTools(ctx context.Context, tx pgx.Tx) ([]pageChoiceTool, error) {
	rows, err := tx.Query(ctx, `SELECT `+toolCols+` FROM workflow_tools WHERE environment = $1 AND audience = 'members' ORDER BY name, command LIMIT 200`, pageToolEnv)
	if err != nil {
		return nil, err
	}
	all, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (toolRow, error) { return scanTool(row) })
	if err != nil {
		return nil, err
	}
	out := []pageChoiceTool{}
	for _, t := range all {
		var def []byte
		if err := tx.QueryRow(ctx, `SELECT definition FROM workflow_versions WHERE workflow_id = $1 AND version = $2`, t.Workflow, t.Version).Scan(&def); err != nil {
			return nil, err
		}
		d, err := s.definitionFor(t.Workflow, t.Version, def)
		if err != nil {
			continue
		}
		c := pageChoiceTool{Command: t.Command, Name: t.Name, Answers: t.Asks() == wdcheck.CareAnswers, Outputs: []pageChoiceOutput{}, Inputs: []pageChoiceInput{}}
		for _, o := range d.Outputs {
			c.Outputs = append(c.Outputs, pageChoiceOutput{Name: o.Name, Title: nonEmpty(o.Title, o.Name), Type: o.Type})
		}
		if schema, _ := d.InputsSchema(); schema != nil {
			if m, ok := schema.(map[string]any); ok {
				props, _ := m["properties"].(map[string]any)
				for k, v := range props {
					prop, _ := v.(map[string]any)
					title, _ := prop["title"].(string)
					c.Inputs = append(c.Inputs, pageChoiceInput{Name: k, Title: nonEmpty(title, k), Personal: personalInput(k, prop)})
				}
			}
		}
		slices.SortFunc(c.Inputs, func(a, b pageChoiceInput) int { return strings.Compare(a.Name, b.Name) })
		if d.Tool != nil && d.Tool.Answer != nil && d.Tool.Answer.Type == "list" && d.Tool.Answer.Item != nil {
			a := d.Tool.Answer
			c.List = &pageChoiceList{From: a.From, Title: a.Item.Title, Sub: a.Item.Sub}
		}
		out = append(out, c)
	}
	return out, nil
}

func (s *Server) pageChoiceWorkflows(ctx context.Context, tx pgx.Tx) ([]pageChoiceWorkflow, error) {
	rows, err := tx.Query(ctx, `SELECT w.id, w.name, d.version, v.definition FROM workflows w
		JOIN deployments d ON d.workflow_id = w.id AND d.environment = $1
		JOIN workflow_versions v ON v.workflow_id = w.id AND v.version = d.version
		ORDER BY w.name, w.created_at LIMIT 200`, pageToolEnv)
	if err != nil {
		return nil, err
	}
	type flow struct {
		id   uuid.UUID
		name string
		ver  int
		def  []byte
	}
	all, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (flow, error) {
		var f flow
		err := row.Scan(&f.id, &f.name, &f.ver, &f.def)
		return f, err
	})
	if err != nil {
		return nil, err
	}
	named := map[string]int{}
	for _, f := range all {
		named[f.name]++
	}
	out := []pageChoiceWorkflow{}
	for _, f := range all {
		if named[f.name] != 1 {
			continue // a page names a workflow by name: two of one name are neither
		}
		d, err := s.definitionFor(f.id, f.ver, f.def)
		if err != nil {
			continue
		}
		c := pageChoiceWorkflow{Name: f.name, Signals: []string{}, Approvals: []pageChoiceStep{}}
		d.Walk(func(st *wd.Step) {
			if st.Signal != nil && st.Signal.Pages && !slices.Contains(c.Signals, st.Signal.Event) {
				c.Signals = append(c.Signals, st.Signal.Event)
			}
			if st.Type == "approval" {
				c.Approvals = append(c.Approvals, pageChoiceStep{Step: st.ID, Name: nonEmpty(st.Name, st.ID)})
			}
		})
		out = append(out, c)
	}
	return out, nil
}
