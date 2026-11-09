package runtime

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/effects"
)

// Sandbox runs (decision 0062 V7, Talk to it here): a test conversation
// runs the assistant's workflow for real (its reads, the model, hearing
// and speaking) except for what it would change: every write a step would
// send (a WhatsApp message, a row, a hand-over) is simulated, answered
// with an output shaped by the action's schema, and kept in the run's
// history as simulated. Child runs of a sandbox run are sandbox runs.

// sandboxed reports whether the run is a sandbox run.
func (w *Worker) sandboxed(ctx context.Context, tenant, run uuid.UUID) (bool, error) {
	var sb bool
	err := db.InTenantTx(ctx, w.Store.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT sandbox FROM runs WHERE id = $1`, run).Scan(&sb)
	})
	return sb, err
}

// simulate answers a write in a sandbox run without sending it: ok is
// false for a read (it runs for real) or a run that is not a sandbox.
func (w *Worker) simulate(ctx context.Context, p *plan) (out map[string]any, ok bool, err error) {
	if p.conn == nil || p.class == effects.Read {
		return nil, false, nil
	}
	sb, err := w.sandboxed(ctx, p.c.tenant, p.c.run)
	if err != nil || !sb {
		return nil, false, err
	}
	m := p.conn.Manifest
	resolve := func(ref string) json.RawMessage {
		// '#/actions/<action>/output', as manifests share an output.
		var a string
		if _, err := fmt.Sscanf(ref, "#/actions/%s", &a); err == nil {
			if i := len(a) - len("/output"); i > 0 && a[i:] == "/output" {
				return m.Actions[a[:i]].Output
			}
		}
		return nil
	}
	out, _ = shaped(m.Actions[p.action].Output, resolve, 0).(map[string]any)
	if out == nil {
		out = map[string]any{}
	}
	out["simulated"] = true
	return out, true, nil
}

// shaped is a value of a JSON Schema's shape: its required properties
// (and every property one level down) with placeholder values, so later
// steps' expressions find what they read.
func shaped(raw json.RawMessage, resolve func(string) json.RawMessage, depth int) any {
	var s map[string]any
	if len(raw) == 0 || json.Unmarshal(raw, &s) != nil {
		return map[string]any{}
	}
	return shape(s, resolve, depth)
}

func shape(s map[string]any, resolve func(string) json.RawMessage, depth int) any {
	if depth > 6 {
		return nil
	}
	if ref, ok := s["$ref"].(string); ok {
		return shaped(resolve(ref), resolve, depth+1)
	}
	t := s["type"]
	if list, ok := t.([]any); ok && len(list) > 0 {
		t = list[0]
	}
	switch t {
	case "object", nil:
		out := map[string]any{}
		props, _ := s["properties"].(map[string]any)
		for k, v := range props {
			if m, ok := v.(map[string]any); ok {
				out[k] = shape(m, resolve, depth+1)
			}
		}
		return out
	case "array":
		return []any{}
	case "string":
		if enum, ok := s["enum"].([]any); ok && len(enum) > 0 {
			return enum[0]
		}
		return "simulated"
	case "integer", "number":
		return 0
	case "boolean":
		return true
	}
	return nil
}

// simulateStep answers a code, container or writing http step of a sandbox
// run without running it.
func (w *Worker) simulateStep(ctx context.Context, p *plan) (map[string]any, bool, error) {
	sb, err := w.sandboxed(ctx, p.c.tenant, p.c.run)
	if err != nil || !sb {
		return nil, false, err
	}
	out := map[string]any{"simulated": true}
	if p.stepType == "http" {
		out["status"], out["headers"], out["body"] = 200, map[string]any{}, map[string]any{}
	}
	return out, true, nil
}
