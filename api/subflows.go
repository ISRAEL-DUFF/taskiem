package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/expansion"
	"github.com/israel-duff/taskiem/engine/wd"
)

// Subflow steps (decision 0049) name a workflow of the business to run as
// a child run. Saving or publishing checks that the workflow exists, has
// a published version (or the version named), starts on demand (a manual
// or subflow trigger), and takes the step's input; the runtime checks
// again when the step starts, since the workflow may have changed.

// subflowProblems checks a definition's subflow steps against the
// business's workflows.
func (s *Server) subflowProblems(ctx context.Context, tenant uuid.UUID, doc []byte) []problem {
	def, err := wd.Load(doc)
	if err != nil || !def.HasSubflows() {
		return nil
	}
	var out []problem
	err = db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		var walkErr error
		def.Walk(func(st *wd.Step) {
			if st.Type != "subflow" || st.Subflow == nil || walkErr != nil {
				return
			}
			path := "/steps/" + st.ID + "/config/workflow"
			add := func(format string, args ...any) {
				out = append(out, problem{Path: path, Message: fmt.Sprintf(format, args...)})
			}
			wf, err := uuid.Parse(st.Subflow.Workflow)
			if err != nil {
				add("%q is not a workflow id", st.Subflow.Workflow)
				return
			}
			var name string
			var active *int
			switch err := tx.QueryRow(ctx, `SELECT name, active_version FROM workflows WHERE id = $1`, wf).Scan(&name, &active); {
			case errors.Is(err, pgx.ErrNoRows):
				add("there is no workflow %s", wf)
				return
			case err != nil:
				walkErr = err
				return
			}
			version := st.Subflow.Version
			var raw []byte
			if version == 0 {
				if active == nil {
					add("the workflow %s has no published version to run yet", name)
					return
				}
				version = *active
			}
			var state string
			switch err := tx.QueryRow(ctx, `SELECT state, definition FROM workflow_versions WHERE workflow_id = $1 AND version = $2`, wf, version).Scan(&state, &raw); {
			case errors.Is(err, pgx.ErrNoRows):
				add("the workflow %s has no version %d", name, version)
				return
			case err != nil:
				walkErr = err
				return
			case state != "published" && state != "deprecated":
				add("version %d of the workflow %s is %s, not published", version, name, state)
				return
			}
			var target map[string]any
			if json.Unmarshal(raw, &target) != nil {
				return
			}
			trig, _ := target["trigger"].(map[string]any)
			if t, _ := trig["type"].(string); t != "manual" && t != "subflow" {
				add("the workflow %s starts on its own (%s); a workflow another runs has a manual or subflow trigger", name, t)
				return
			}
			if err := expansion.InputFits(target, st.Input); err != nil {
				out = append(out, problem{Path: "/steps/" + st.ID + "/input", Message: fmt.Sprintf("the workflow %s: %v", name, err)})
			}
		})
		return walkErr
	})
	if err != nil {
		return []problem{{Path: "/", Message: "the workflows subflow steps run could not be read: " + err.Error()}}
	}
	return out
}

// subflowsOf lists the workflows the subflow steps in a decoded
// definition's steps name, at any depth, each once.
func subflowsOf(v any) []string {
	var out []string
	var find func(v any)
	find = func(v any) {
		switch x := v.(type) {
		case map[string]any:
			if x["type"] == "subflow" {
				if c, ok := x["config"].(map[string]any); ok {
					if id, ok := c["workflow"].(string); ok && !slices.Contains(out, id) {
						out = append(out, id)
					}
				}
			}
			for _, c := range x {
				find(c)
			}
		case []any:
			for _, c := range x {
				find(c)
			}
		}
	}
	find(v)
	return out
}
