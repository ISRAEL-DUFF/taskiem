package api

import (
	"context"
	"encoding/json"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/connector"
	"github.com/israel-duff/taskiem/engine/runtime"
	"github.com/israel-duff/taskiem/engine/wd"
	"github.com/israel-duff/taskiem/engine/wdcheck"
	"github.com/israel-duff/taskiem/templates"
)

// storeToolCare works out a tool's care from its steps when its version
// is published, and keeps it with the version (made tools §8).
func (s *Server) storeToolCare(ctx context.Context, tx pgx.Tx, tenant, wf uuid.UUID, v int, def []byte, d *wd.Definition) error {
	if d.Tool == nil {
		return nil
	}
	reg, err := s.Registry.For(connector.ManifestsOnly(ctx), tenant.String())
	if err != nil {
		return err
	}
	find, _, err := runtime.PinnedTemplates(ctx, tx, wf, v, runtime.Callables(ctx, tx, templates.Default()))
	if err != nil {
		return err
	}
	care, err := wdcheck.ToolCare(def, reg, find)
	if err != nil {
		return err
	}
	raw, _ := json.Marshal(care)
	_, err = tx.Exec(ctx, `UPDATE workflow_versions SET tool_care = $3 WHERE workflow_id = $1 AND version = $2`, wf, v, raw)
	return err
}
