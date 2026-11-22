package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/connector"
	"github.com/israel-duff/taskiem/engine/runtime"
	"github.com/israel-duff/taskiem/engine/wd"
	"github.com/israel-duff/taskiem/engine/wdcheck"
	"github.com/israel-duff/taskiem/templates"
)

// toolDeployable refuses deploying a tool whose command another workflow
// deployed in the same environment already has (decision 0070: one
// meaning for a word, per business and environment).
func toolDeployable(ctx context.Context, tx pgx.Tx, wf uuid.UUID, env string, d *wd.Definition) error {
	if d.Tool == nil {
		return nil
	}
	var other string
	err := tx.QueryRow(ctx, `SELECT w.name FROM deployments dp
		JOIN workflows w ON w.id = dp.workflow_id
		JOIN workflow_versions wv ON wv.workflow_id = dp.workflow_id AND wv.version = dp.version
		WHERE dp.environment = $1 AND dp.workflow_id <> $2 AND wv.definition->'tool'->>'command' = $3
		LIMIT 1`, env, wf, d.Tool.Command).Scan(&other)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	return fmt.Errorf("%w: in %s, the command /%s is already %s's; choose another", errConflict, env, d.Tool.Command, other)
}

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
