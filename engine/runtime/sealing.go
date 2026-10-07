package runtime

import (
	"bytes"
	"context"
	"encoding/json"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/connector"
	"github.com/israel-duff/taskiem/engine/expr"
	"github.com/israel-duff/taskiem/engine/history"
	"github.com/israel-duff/taskiem/engine/pii"
	"github.com/israel-duff/taskiem/engine/wd"
)

// triggerPaths are the trigger fields the WD's inputs schema marks as PII.
// The inputs schema describes trigger.body.
func triggerPaths(def *wd.Definition) []pii.Path {
	schema, types := def.InputsSchema()
	if schema == nil {
		return nil
	}
	var out []pii.Path
	for _, p := range pii.SchemaPaths(schema, types) {
		out = append(out, pii.Path{Segments: append([]string{"trigger", "body"}, p.Segments...), Category: p.Category})
	}
	return out
}

// sealPayload marshals payload with declared paths and tainted values sealed.
func (s *Store) sealPayload(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, payload any, paths []pii.Path, taint pii.Taint) (any, error) {
	if s.PII == nil || payload == nil {
		return payload, nil
	}
	v, err := jsonRoundTrip(payload)
	if err != nil {
		return nil, err
	}
	if v, err = pii.Seal(ctx, s.PII, tx, tenant, v, paths, taint); err != nil {
		return nil, err
	}
	return pii.SealDetected(ctx, s.PII, tx, tenant, v, taint)
}

// redactText masks personal data in the free text of a worker result:
// a failure's message and a code step's log lines.
func redactText(v any) {
	m, ok := v.(map[string]any)
	if !ok {
		return
	}
	if e, ok := m["error"].(map[string]any); ok {
		if msg, ok := e["message"].(string); ok {
			e["message"] = pii.Redact(msg)
		}
	}
	if logs, ok := m["logs"].([]any); ok {
		for i, l := range logs {
			if s, ok := l.(string); ok {
				logs[i] = pii.Redact(s)
			}
		}
	}
}

// connectorPIIPaths are the input fields a connector action declares as PII.
func (s *Store) connectorPIIPaths(ctx context.Context, tenant uuid.UUID, p history.ScheduledPayload) ([]pii.Path, error) {
	if s.Registry == nil || p.Connector == "" {
		return nil, nil
	}
	reg, err := s.Registry.For(ctx, tenant.String())
	if err != nil {
		return nil, err // never write a payload whose personal fields are unknown
	}
	c, ok := reg.Get(p.Connector)
	if !ok {
		return nil, nil
	}
	var out []pii.Path
	for _, f := range c.Manifest.Actions[p.Action].PII {
		if _, isOutput := connector.OutputPIIPath(f.Field); isOutput {
			continue // sealed when the result is written (outputPIIPaths)
		}
		out = append(out, pii.Path{Segments: append([]string{"input"}, connector.InputPIIPath(f.Field)...), Category: piiCategory(f.Category)})
	}
	return out, nil
}

// outputPIIPaths are the output places a connector action declares as PII,
// as they sit in a StepCompleted payload.
func outputPIIPaths(c *connector.Connector, action string) []pii.Path {
	if c == nil {
		return nil
	}
	var out []pii.Path
	for _, f := range c.Manifest.Actions[action].PII {
		if segs, ok := connector.OutputPIIPath(f.Field); ok {
			out = append(out, pii.Path{Segments: append([]string{"output"}, segs...), Category: piiCategory(f.Category)})
		}
	}
	return out
}

func piiCategory(c string) string {
	if c == "" {
		return "other"
	}
	return c
}

// openHistory decrypts sealed values for decide and the worker, inside the
// transaction, and returns the personal values seen.
func (s *Store) openHistory(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, hist []history.Event) ([]history.Event, pii.Taint, error) {
	return s.openHistoryInto(ctx, tx, tenant, hist, pii.Taint{})
}

// openHistoryInto is openHistory adding what it opens to taint.
func (s *Store) openHistoryInto(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, hist []history.Event, taint pii.Taint) ([]history.Event, pii.Taint, error) {
	if s.PII == nil {
		return hist, taint, nil
	}
	out := make([]history.Event, len(hist))
	for i, e := range hist {
		out[i] = e
		if !bytes.Contains(e.Payload, []byte(`"$pii"`)) {
			continue
		}
		v, err := expr.DecodeJSON(e.Payload)
		if err != nil {
			return nil, nil, err
		}
		opened, err := pii.Open(ctx, s.PII, tx, tenant, v, taint)
		if err != nil {
			return nil, nil, err
		}
		raw, err := json.Marshal(opened)
		if err != nil {
			return nil, nil, err
		}
		out[i].Payload = raw
	}
	return out, taint, nil
}

// OpenedHistory returns a run's history with personal data decrypted, for
// replay checks and for callers holding pii.reveal.
func (s *Store) OpenedHistory(ctx context.Context, ref RunRef) ([]history.Event, error) {
	var h []history.Event
	err := dbTx(ctx, s, ref.TenantID, func(tx pgx.Tx) error {
		raw, err := History(ctx, tx, ref.ID)
		if err != nil {
			return err
		}
		h, _, err = s.openHistory(ctx, tx, ref.TenantID, raw)
		return err
	})
	return h, err
}
