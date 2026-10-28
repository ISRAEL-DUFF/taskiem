package api

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/billing"
	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/expansion"
	"github.com/israel-duff/taskiem/engine/runtime"
	"github.com/israel-duff/taskiem/engine/wd"
	"github.com/israel-duff/taskiem/templates"
)

// AI steps (decision 0050) ask the deployment's model at run time. Saving
// a version that has one checks what the runtime would refuse: a
// deployment without a model, a model it does not offer, a plan without
// the ai feature, or a business that has turned AI steps off. Publishing
// one that lets personal data reach the model is audited. Both look at
// the ai steps inside the callable templates a version uses too: they
// are expanded into the run when it starts, and run like its own.

// aiSteps is the definition with its template steps expanded (find gives
// their templates), so the ai steps inside them are seen. Templates that
// cannot be expanded are reported by the definition's own checks; their
// steps are left out here.
func aiSteps(doc []byte, find expansion.Templates) (*wd.Definition, error) {
	def, err := wd.Load(doc)
	if err != nil || find == nil || !def.HasTemplates() {
		return def, err
	}
	raw, _, err := expansion.ExpandTemplates(doc, find)
	if err != nil {
		return def, nil
	}
	if expanded, err := wd.Load(raw); err == nil {
		return expanded, nil
	}
	return def, nil
}

// aiStepProblems checks a definition's ai steps, its templates' among
// them, against the deployment, the plan and the business's settings.
func (s *Server) aiStepProblems(ctx context.Context, tenant uuid.UUID, doc []byte, find expansion.Templates) []problem {
	def, err := aiSteps(doc, find)
	if err != nil || !def.HasAI() {
		return nil
	}
	if s.AI == nil || s.AI.Provider == nil {
		return []problem{{Path: "/steps", Message: "AI steps need an AI model, and this deployment has none"}}
	}
	var out []problem
	def.Walk(func(st *wd.Step) {
		if st.Type != "ai" || st.AI == nil || st.AI.Model == "" {
			return
		}
		if _, ok := s.AI.Config.StepModel(st.AI.Model); !ok {
			out = append(out, problem{Path: "/steps/" + st.ID + "/config/model",
				Message: fmt.Sprintf("the model %s is not offered for AI steps here; offered: %v", st.AI.Model, s.AI.Config.OfferedStepModels())})
		}
	})
	if err := s.Billing.Require(ctx, tenant, billing.FeatureAI); err != nil {
		out = append(out, problem{Path: "/steps", Message: "AI steps need a plan with AI: " + err.Error()})
	}
	var allowed bool
	if err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		var err error
		allowed, err = aiStepsAllowed(ctx, tx)
		return err
	}); err != nil {
		return append(out, problem{Path: "/", Message: "the business's AI settings could not be read: " + err.Error()})
	}
	if !allowed {
		out = append(out, problem{Path: "/steps", Message: "the business has turned AI steps off (Secrets & settings, AI steps)"})
	}
	return out
}

// auditAIPersonalData records, when a version is published, each ai step
// (its pinned templates' among them) that lets personal data reach the
// model, and the fields it lets through.
func auditAIPersonalData(ctx context.Context, tx pgx.Tx, tenant, wf uuid.UUID, v int, doc []byte, by *uuid.UUID) error {
	find, _, err := runtime.PinnedTemplates(ctx, tx, wf, v, runtime.Callables(ctx, tx, templates.Default()))
	if err != nil {
		return err
	}
	def, err := aiSteps(doc, find)
	if err != nil {
		return err
	}
	steps := def.AIPersonalData()
	if len(steps) == 0 {
		return nil
	}
	ids := make([]string, 0, len(steps))
	for id := range steps {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	detail, _ := json.Marshal(map[string]any{"workflow_id": wf, "version": v, "steps": steps, "step_ids": ids})
	actorType, actor := "system", "system"
	if by != nil {
		actorType, actor = "user", by.String()
	}
	_, err = tx.Exec(ctx, `SELECT taskiem_audit_append($1, $2, $3, $4, $5, $6)`, tenant, actorType, actor, "ai.personal_data.allow",
		fmt.Sprintf("workflow:%s/v%d", wf, v), detail)
	return err
}
