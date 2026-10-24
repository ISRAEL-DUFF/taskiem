package runtime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/capability"
	"github.com/israel-duff/taskiem/engine/expansion"
	"github.com/israel-duff/taskiem/engine/expr"
	"github.com/israel-duff/taskiem/engine/history"
	"github.com/israel-duff/taskiem/engine/pii"
	"github.com/israel-duff/taskiem/engine/wd"
	"github.com/israel-duff/taskiem/templates"
)

// projected reports whether a completed step stands for a capability whose
// output the rail's mapping gives (wd-v1 capability steps): the last step
// of an expansion. A composite's earlier steps keep the action's output.
func projected(p *plan) bool {
	return p.step != nil && p.step.Capability != nil && len(p.step.Capability.Output) > 0 && p.sched.Capability != nil
}

// project turns an expanded step's completion into the capability's
// output, keeping what the rail answered as provider_output. An answer
// the mapping cannot read, or that is not the capability's shape, is not
// guessed at: a write is parked for a person to check (the money may have
// moved), a read fails.
func project(p *plan, result history.Event) history.Event {
	if result.Type != history.StepCompleted || !projected(p) {
		return result
	}
	v, err := expr.DecodeJSON(result.Payload)
	if err != nil {
		return result
	}
	done, _ := v.(map[string]any)
	raw := done["output"]
	uses := p.step.Capability.Uses
	out, err := capability.ResolveMapping(p.step.Capability.Output, p.sched.Capability.Input, raw, p.sched.Capability.Steps)
	if err == nil {
		if spec, ok := capability.Get(uses); ok {
			if msgs := spec.CheckOutput(out); len(msgs) > 0 {
				err = fmt.Errorf("not a %s output: %s", uses, strings.Join(msgs, "; "))
			}
		}
	}
	if err != nil {
		msg := fmt.Sprintf("%s's answer could not be read as %s: %v", p.sched.Connector, uses, err)
		if p.class.IsWrite() {
			return failed(p.c, "unknown_outcome", msg+"; check it with the provider", "park")
		}
		return failed(p.c, "fatal", msg, "fail")
	}
	done["output"], done["provider_output"] = out, raw
	b, _ := json.Marshal(done)
	result.Payload = b
	return result
}

// capabilityOutputPaths are the personal places of a projected completion:
// the capability's x-pii outputs, and the action's declared personal
// outputs where the rail's answer is kept.
func capabilityOutputPaths(p *plan) []pii.Path {
	var out []pii.Path
	if spec, ok := capability.Get(p.step.Capability.Uses); ok {
		for _, f := range capability.Fields(spec.Output) {
			if f.PII != "" {
				out = append(out, pii.Path{Segments: append([]string{"output"}, f.Path...), Category: f.PII})
			}
		}
	}
	for _, path := range outputPIIPaths(p.conn, p.action) {
		out = append(out, pii.Path{Segments: append([]string{"provider_output"}, path.Segments[1:]...), Category: path.Category})
	}
	return out
}

// capabilityInputPaths are the personal places of an expanded step's
// scheduled capability input.
func capabilityInputPaths(uses string) []pii.Path {
	spec, ok := capability.Get(uses)
	if !ok {
		return nil
	}
	var out []pii.Path
	for _, f := range capability.Fields(spec.Input) {
		if f.PII != "" {
			out = append(out, pii.Path{Segments: append([]string{"capability", "input"}, f.Path...), Category: f.PII})
		}
	}
	return out
}

// CapabilityError is a run refused because its capability steps cannot run
// on the environment's connections: said in plain words.
type CapabilityError struct{ Err error }

func (e *CapabilityError) Error() string { return e.Err.Error() }
func (e *CapabilityError) Unwrap() error { return e.Err }

// Expand expands a version's template steps (with the templates pinned
// when the version was saved) and its capability steps on an
// environment's connections (engine/expansion), and keeps the result:
// deploys check with it, and a run pins what it returns. Nil, nil for a
// version with neither.
func (s *Store) Expand(ctx context.Context, tx pgx.Tx, tenant, workflow uuid.UUID, version int, env string) ([]byte, *wd.Definition, []expansion.Choice, error) {
	var raw []byte
	if err := tx.QueryRow(ctx, `SELECT definition FROM workflow_versions WHERE workflow_id = $1 AND version = $2`, workflow, version).Scan(&raw); err != nil {
		return nil, nil, nil, err
	}
	d, err := wd.Load(raw)
	if err != nil || !expansion.Has(d) {
		return nil, nil, nil, err
	}
	refused := func(err error) error {
		var ref *expansion.Refusal
		if errors.As(err, &ref) {
			return &CapabilityError{Err: err}
		}
		return err
	}
	// Template steps first: a template's capability steps are expanded
	// with the workflow's own.
	if d.HasTemplates() {
		find, err := s.templates(ctx, tx, workflow, version)
		if err != nil {
			return nil, nil, nil, err
		}
		if raw, _, err = expansion.ExpandTemplates(raw, find); err != nil {
			return nil, nil, nil, refused(err)
		}
		if d, err = wd.Load(raw); err != nil {
			return nil, nil, nil, err
		}
	}
	out, choices := raw, []expansion.Choice(nil)
	if expansion.HasCapabilities(d) {
		if s.Registry == nil {
			return nil, nil, nil, &CapabilityError{Err: errors.New("capability steps need the platform's connectors")}
		}
		reg, err := s.Registry.For(ctx, tenant.String())
		if err != nil {
			return nil, nil, nil, err
		}
		rows, err := tx.Query(ctx, `SELECT name, connector FROM connections WHERE environment = $1 AND status = 'active' ORDER BY name`, env)
		if err != nil {
			return nil, nil, nil, err
		}
		conns, err := pgx.CollectRows(rows, pgx.RowToStructByPos[expansion.Connection])
		if err != nil {
			return nil, nil, nil, err
		}
		if out, choices, err = expansion.Expand(raw, expansion.ResolverFor(conns, reg)); err != nil {
			return nil, nil, nil, refused(err)
		}
	}
	sum := sha256.Sum256(out)
	if _, err := tx.Exec(ctx, `INSERT INTO workflow_expansions (tenant_id, workflow_id, version, digest, definition, choices) VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT DO NOTHING`, tenant, workflow, version, sum[:], out, choices); err != nil {
		return nil, nil, nil, err
	}
	exp, err := wd.Load(out)
	if err != nil {
		return nil, nil, nil, err
	}
	s.defs.Put(expansionKey(workflow, version, sum[:]), exp)
	return sum[:], exp, choices, nil
}

func expansionKey(workflow uuid.UUID, version int, digest []byte) string {
	return workflow.String() + "/" + strconv.Itoa(version) + "/" + hex.EncodeToString(digest)
}

// runDefinition is the definition a run runs: its version, or the
// expansion of it the run pinned when it started.
func (s *Store) runDefinition(ctx context.Context, tx pgx.Tx, workflow uuid.UUID, version int, digest []byte) (*wd.Definition, error) {
	if len(digest) == 0 {
		return s.definition(ctx, tx, workflow, version)
	}
	key := expansionKey(workflow, version, digest)
	if d, ok := s.defs.Get(key); ok {
		return d, nil
	}
	var raw []byte
	err := tx.QueryRow(ctx, `SELECT definition FROM workflow_expansions WHERE workflow_id = $1 AND version = $2 AND digest = $3`, workflow, version, digest).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("workflow %s version %d expansion %x: %w", workflow, version, digest, ErrNotFound)
	}
	if err != nil {
		return nil, err
	}
	d, err := wd.Load(raw)
	if err != nil {
		return nil, err
	}
	s.defs.Put(key, d)
	return d, nil
}

// RunDefinition is the definition a run runs (its pinned expansion, when
// its version has capability steps), for the API's run views and replay.
func (s *Store) RunDefinition(ctx context.Context, tenant, run uuid.UUID) (*wd.Definition, error) {
	var d *wd.Definition
	err := dbTx(ctx, s, tenant, func(tx pgx.Tx) error {
		var wf uuid.UUID
		var version int
		var digest []byte
		if err := tx.QueryRow(ctx, `SELECT workflow_id, version, expansion FROM runs WHERE id = $1`, run).Scan(&wf, &version, &digest); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return fmt.Errorf("run %s: %w", run, ErrNotFound)
			}
			return err
		}
		var err error
		d, err = s.runDefinition(ctx, tx, wf, version, digest)
		return err
	})
	return d, err
}

// templates finds the templates a version's template steps use.
func (s *Store) templates(_ context.Context, _ pgx.Tx, _ uuid.UUID, _ int) (expansion.Templates, error) {
	lib := s.Templates
	if lib == nil {
		lib = templates.Default()
	}
	return expansion.Library(lib), nil
}
