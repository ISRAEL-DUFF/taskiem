package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/connector"
	"github.com/israel-duff/taskiem/engine/expansion"
	"github.com/israel-duff/taskiem/engine/wd"
	"github.com/israel-duff/taskiem/templates"
)

// A business's own workflows as callable components (decision 0048): a
// workflow with outputs and a published version can be a template step of
// another workflow of the business, as a library template can. It is
// read as a template (WorkflowTemplate), found by its id (uuidPattern tells
// one from a library id), and pinned with the calling version as a
// template is.

var uuidPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// IsWorkflowID reports whether a template step's id names a workflow of
// the business rather than a library template.
func IsWorkflowID(id string) bool { return uuidPattern.MatchString(id) }

// CallableWorkflow is a workflow another workflow may use as a step: its
// published version with its interface.
type CallableWorkflow struct {
	ID      uuid.UUID        `json:"id"`
	Name    string           `json:"name"`
	Version int              `json:"version"`
	Inputs  json.RawMessage  `json:"inputs"`
	Outputs []wd.Output      `json:"outputs"`
	Effect  templates.Effect `json:"effect"`
}

// WorkflowTemplate reads a workflow's published version as a template:
// callable when it declares outputs. ErrNotFound when the workflow does
// not exist or has no published version.
func WorkflowTemplate(ctx context.Context, tx pgx.Tx, id uuid.UUID) (*templates.Template, error) {
	var name string
	var version int
	var raw []byte
	err := tx.QueryRow(ctx, `SELECT w.name, v.version, v.definition FROM workflows w JOIN workflow_versions v ON v.workflow_id = w.id AND v.version = w.active_version
		WHERE w.id = $1`, id).Scan(&name, &version, &raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("the workflow %s has no published version: %w", id, ErrNotFound)
	}
	if err != nil {
		return nil, err
	}
	def, err := wd.Load(raw)
	if err != nil {
		return nil, fmt.Errorf("the workflow %s (%s): %w", name, id, err)
	}
	return templateOf(id, name, version, raw, def), nil
}

// templateOf is a workflow version as a template: no parameters, its
// outputs, and its definition as it is.
func templateOf(id uuid.UUID, name string, version int, raw []byte, def *wd.Definition) *templates.Template {
	desc := def.Description
	if desc == "" {
		desc = "A workflow of the business, used as a step."
	}
	return &templates.Template{ID: id.String(), Title: name, Summary: fmt.Sprintf("%s (version %d)", name, version), Description: desc,
		Category: "workflow", Tags: []string{}, Connectors: connectorsOf(def), Params: []templates.Param{},
		Definition: raw, Callable: def.Callable(), Outputs: def.Outputs}
}

// connectorsOf lists a definition's connectors as a template declares
// them: connector refs and capability:<uses>.
func connectorsOf(def *wd.Definition) []string {
	seen := map[string]bool{}
	var out []string
	add := func(s string) {
		if s != "" && !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	if ref, ok := def.Trigger.Config["connector"].(string); ok {
		add(ref)
	}
	def.Walk(func(st *wd.Step) {
		add(st.Connector)
		if st.Type == "capability" && !wd.IsWait(st.Uses) {
			add(templates.CapabilityPrefix + st.Uses)
		}
	})
	if out == nil {
		out = []string{}
	}
	return out
}

// Callables finds a template step's template: a library template by id,
// or one of the business's workflows by its id, read in tx.
func Callables(ctx context.Context, tx pgx.Tx, lib *templates.Library) expansion.Templates {
	library := expansion.Library(lib)
	return func(id string) (*templates.Template, error) {
		if !IsWorkflowID(id) {
			return library(id)
		}
		wf, err := uuid.Parse(id)
		if err != nil {
			return nil, fmt.Errorf("there is no workflow %q", id)
		}
		t, err := WorkflowTemplate(ctx, tx, wf)
		if errors.Is(err, ErrNotFound) {
			return nil, fmt.Errorf("the workflow %s has no published version to use", id)
		}
		if err != nil {
			return nil, err
		}
		if !t.Callable {
			return nil, fmt.Errorf("the workflow %s cannot be a step: it declares no outputs", t.Title)
		}
		return t, nil
	}
}

// CallableWorkflows lists the business's workflows another workflow may
// use as a step: those with a published version that declares outputs,
// in name order, with their interface.
func CallableWorkflows(ctx context.Context, tx pgx.Tx, reg connector.Lookup) ([]CallableWorkflow, error) {
	rows, err := tx.Query(ctx, `SELECT w.id, w.name, v.version, v.definition FROM workflows w JOIN workflow_versions v ON v.workflow_id = w.id AND v.version = w.active_version
		WHERE v.definition ? 'outputs' ORDER BY w.name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []CallableWorkflow{}
	for rows.Next() {
		var c CallableWorkflow
		var raw []byte
		if err := rows.Scan(&c.ID, &c.Name, &c.Version, &raw); err != nil {
			return nil, err
		}
		def, err := wd.Load(raw)
		if err != nil || !def.Callable() || def.RawInputs == nil {
			continue
		}
		c.Inputs, c.Outputs, c.Effect = def.RawInputs.Schema, def.Outputs, templates.EffectOf(def, reg)
		out = append(out, c)
	}
	return out, rows.Err()
}
