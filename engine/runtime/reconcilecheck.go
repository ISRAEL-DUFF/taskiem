package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/connector"
	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/effects"
	"github.com/israel-duff/taskiem/engine/history"
	"github.com/israel-duff/taskiem/engine/pii"
)

// ReconcileReport is what a parked write's reconcile action reports now.
// Asking changes nothing: the step stays parked until a person resolves
// it (ResolveStep), with this as evidence.
type ReconcileReport struct {
	Step      string `json:"step"`
	Connector string `json:"connector"`
	Action    string `json:"action"`
	Reconcile string `json:"reconcile_action"`
	// Found: the provider has the effect (Output is its record, with
	// personal data masked); false: the provider reports no such effect.
	Found  bool   `json:"found"`
	Output any    `json:"output,omitempty"`
	Error  string `json:"error,omitempty"`
}

// ErrNoReconcile means the parked step has no read-only reconcile action.
var ErrNoReconcile = errors.New("the step's action has no read-only reconcile action")

// CheckReconcile runs the reconcile action of a write parked in
// needs_reconciliation, with the key of its last attempt, and returns what
// the provider says. Only a reconcile action of class read is run. Nothing
// is written to the run.
func (w *Worker) CheckReconcile(ctx context.Context, ref RunRef, step string) (*ReconcileReport, error) {
	w.defaults()
	p := &plan{c: claim{tenant: ref.TenantID, run: ref.ID, step: step}, mode: modeReconcile}
	var key string
	err := db.InTenantTx(ctx, w.Store.Pool, []uuid.UUID{ref.TenantID}, func(tx pgx.Tx) error {
		var status string
		if err := tx.QueryRow(ctx, `SELECT environment, status FROM runs WHERE id = $1`, ref.ID).Scan(&p.env, &status); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrNotFound
			}
			return err
		}
		if status != "needs_reconciliation" {
			return fmt.Errorf("run %s is %s, not waiting for reconciliation: %w", ref.ID, status, ErrNotFound)
		}
		raw, err := History(ctx, tx, ref.ID)
		if err != nil {
			return err
		}
		hist, _, err := w.Store.openHistory(ctx, tx, ref.TenantID, raw)
		if err != nil {
			return err
		}
		for _, e := range hist {
			if e.StepID != step {
				continue
			}
			switch e.Type {
			case history.StepScheduled:
				if err := json.Unmarshal(e.Payload, &p.sched); err != nil {
					return err
				}
				p.c.attempt = e.Attempt
			case history.EffectIntent:
				var ip history.IntentPayload
				if err := json.Unmarshal(e.Payload, &ip); err != nil {
					return err
				}
				key = ip.Key
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if p.sched.Connector == "" || key == "" {
		return nil, ErrNoReconcile
	}
	if w.Registry == nil {
		return nil, errors.New("no connector registry")
	}
	reg, err := w.Registry.For(ctx, ref.TenantID.String())
	if err != nil {
		return nil, err
	}
	conn, ok := reg.Get(p.sched.Connector)
	if !ok {
		return nil, fmt.Errorf("connector %s is not installed", p.sched.Connector)
	}
	spec, ok := conn.Manifest.Actions[p.sched.Action]
	if !ok || spec.Reconcile == "" {
		return nil, ErrNoReconcile
	}
	rspec, ok := conn.Manifest.Actions[spec.Reconcile]
	if !ok || rspec.Class != effects.Read || conn.Actions[spec.Reconcile] == nil {
		return nil, ErrNoReconcile
	}
	p.conn, p.action, p.class, p.stepType = conn, p.sched.Action, spec.Class, "connector"
	if spec.Idempotency != nil {
		p.field = spec.Idempotency.Field
	}
	field := p.field
	if field == "" || strings.HasPrefix(field, "header:") {
		field = "reference"
	}
	rep := &ReconcileReport{Step: step, Connector: conn.Ref(), Action: p.sched.Action, Reconcile: spec.Reconcile}
	creds, err := w.credentials(ctx, p)
	if err != nil {
		rep.Error = pii.Redact(err.Error())
		return rep, nil
	}
	hc, err := w.client(ctx, p, w.CallTimeout)
	if err != nil {
		return nil, err
	}
	rctx, cancel := context.WithTimeout(ctx, w.CallTimeout)
	defer cancel()
	resp, err := conn.Actions[spec.Reconcile].Execute(rctx, connector.Request{Input: map[string]any{field: key}, Credentials: creds, IdempotencyKey: key,
		Attempt: p.c.attempt, Logger: w.Logger, HTTP: hc})
	switch {
	case err == nil:
		rep.Found, rep.Output = true, MaskPersonal(resp.Output)
	case errors.Is(err, connector.ErrNotFound):
		rep.Found = false
	default:
		rep.Error = pii.Redact(scrubText(err.Error(), p.scrub))
	}
	return rep, nil
}

// MaskPersonal returns a copy of v with the personal values the detectors
// recognise replaced by their category, and free text redacted.
func MaskPersonal(v any) any {
	raw, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	var c any
	if json.Unmarshal(raw, &c) != nil {
		return nil
	}
	found := map[string]string{}
	for _, d := range pii.Detect(c) {
		found[d.Path] = d.Category
	}
	return maskAt(c, "", found)
}

func maskAt(v any, path string, found map[string]string) any {
	if cat, ok := found[path]; ok && path != "" {
		return "[" + cat + "]"
	}
	switch t := v.(type) {
	case map[string]any:
		for k, x := range t {
			t[k] = maskAt(x, path+"/"+k, found)
		}
		return t
	case []any:
		for i, x := range t {
			t[i] = maskAt(x, fmt.Sprintf("%s/%d", path, i), found)
		}
		return t
	case string:
		return pii.Redact(t)
	}
	return v
}

func scrubText(s string, secrets []string) string {
	for _, v := range secrets {
		if len(v) >= 4 {
			s = strings.ReplaceAll(s, v, "[secret]")
		}
	}
	return s
}
