package runtime

import (
	"bytes"
	"context"
	"encoding/json"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

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
	return pii.Seal(ctx, s.PII, tx, tenant, v, paths, taint)
}

// connectorPIIPaths are the input fields a connector action declares as PII.
func (s *Store) connectorPIIPaths(p history.ScheduledPayload) []pii.Path {
	if s.Registry == nil || p.Connector == "" {
		return nil
	}
	c, ok := s.Registry.Get(p.Connector)
	if !ok {
		return nil
	}
	var out []pii.Path
	for _, f := range c.Manifest.Actions[p.Action].PII {
		cat := f.Category
		if cat == "" {
			cat = "other"
		}
		out = append(out, pii.Path{Segments: []string{"input", f.Field}, Category: cat})
	}
	return out
}

// openHistory decrypts sealed values for decide and the worker, inside the
// transaction, and returns the personal values seen.
func (s *Store) openHistory(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, hist []history.Event) ([]history.Event, pii.Taint, error) {
	taint := pii.Taint{}
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
