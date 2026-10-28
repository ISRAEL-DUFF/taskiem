package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/storefront"
	"github.com/israel-duff/taskiem/templates"
)

// Turning on deliveries (docs/spec/dispatch.md D6): the order form's
// workflow becomes one made from dispatch-orders, which offers each
// confirmed order for delivery to the business's riders. The plain order
// workflow is taken out of prod in the same change, so the form never has
// none and never two; the form and its settings stay as they are.

const dispatchOrdersTemplate = "dispatch-orders"

// deliveriesView is whether the order form's workflow offers orders to
// riders.
type deliveriesView struct {
	On         bool       `json:"on"`
	WorkflowID *uuid.UUID `json:"workflow_id"`
}

// orderWorkflowTx is the workflow the order form starts in prod, and the
// template its deployed version was made from.
func orderWorkflowTx(ctx context.Context, tx pgx.Tx) (*uuid.UUID, string, error) {
	var wf uuid.UUID
	var tpl string
	err := tx.QueryRow(ctx, `SELECT t.workflow_id, COALESCE(v.template_id, '') FROM triggers t
		JOIN workflow_versions v ON v.workflow_id = t.workflow_id AND v.version = t.version
		WHERE t.type = 'storefront' AND t.environment = 'prod' AND t.storefront_action = 'order'`).Scan(&wf, &tpl)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, "", nil
	}
	if err != nil {
		return nil, "", err
	}
	return &wf, tpl, nil
}

// turnOnDeliveriesTx makes the order form's workflow from dispatch-orders
// and publishes it in place of the one it starts now. It needs the order
// form to deliver; under four-eyes publishing it is refused, since the
// form would start nothing while the new workflow waited.
func (s *Server) turnOnDeliveriesTx(ctx context.Context, tx pgx.Tx, p *Principal, audit func(action, target string, detail map[string]any) error) (deliveriesView, error) {
	actions, err := storefront.Actions(ctx, tx, p.TenantID)
	if err != nil {
		return deliveriesView{}, err
	}
	var delivers bool
	for _, a := range actions {
		if a.Kind == "order" {
			delivers = a.Options.Delivery
		}
	}
	if !delivers {
		return deliveriesView{}, fmt.Errorf("%w: turn on delivery on your order form first", errConflict)
	}
	old, tplID, err := orderWorkflowTx(ctx, tx)
	if err != nil {
		return deliveriesView{}, err
	}
	if tplID == dispatchOrdersTemplate {
		return deliveriesView{On: true, WorkflowID: old}, nil
	}
	g, err := governanceTx(ctx, tx)
	if err != nil {
		return deliveriesView{}, err
	}
	if g.FourEyesPublish {
		return deliveriesView{}, fmt.Errorf("%w: with four-eyes publishing, add the Storefront orders delivered by your riders template from Templates and have a second person publish it", errConflict)
	}
	tpl, ok := templates.Default().Get(dispatchOrdersTemplate)
	if !ok {
		return deliveriesView{}, fmt.Errorf("%w: deliveries are not available here", errConflict)
	}
	wf, _, err := s.instantiateTx(ctx, tx, p, tpl, map[string]any{}, "Storefront orders with deliveries", audit)
	if err != nil {
		return deliveriesView{}, err
	}
	if old != nil {
		v, err := deployedVersion(ctx, tx, *old, "prod")
		if err != nil {
			return deliveriesView{}, err
		}
		if err := undeployTx(ctx, tx, *old, "prod"); err != nil {
			return deliveriesView{}, err
		}
		if err := audit("workflow.undeploy", fmt.Sprintf("%s/%d", *old, v), map[string]any{"environment": "prod", "via": "dispatch", "replaced_by": wf.String()}); err != nil {
			return deliveriesView{}, err
		}
	}
	var by *uuid.UUID
	if p.UserID != uuid.Nil {
		by = &p.UserID
	}
	probs, _, published, err := s.publishTx(ctx, tx, p.TenantID, wf, 1, by, "")
	if err != nil {
		return deliveriesView{}, err
	}
	if len(probs) > 0 {
		msgs := make([]string, len(probs))
		for i, pr := range probs {
			msgs[i] = pr.Message
		}
		return deliveriesView{}, fmt.Errorf("%w: deliveries could not be turned on: %s", errConflict, strings.Join(msgs, "; "))
	}
	if !published {
		return deliveriesView{}, fmt.Errorf("%w: deliveries could not be turned on: publishing waits for an approval", errConflict)
	}
	if v, err := deployedVersion(ctx, tx, wf, "prod"); err != nil || v == 0 {
		return deliveriesView{}, fmt.Errorf("%w: deliveries could not be turned on: your live workflows need an approval to change", errConflict)
	}
	return deliveriesView{On: true, WorkflowID: &wf}, audit("dispatch.turned_on", wf.String(), map[string]any{"replaced": old != nil})
}

// getDeliveriesOn is GET /v1/dispatch/workflow.
func (s *Server) getDeliveriesOn(w http.ResponseWriter, r *http.Request) {
	var out deliveriesView
	if err := s.tx(r, func(tx pgx.Tx) error {
		wf, tpl, err := orderWorkflowTx(r.Context(), tx)
		out = deliveriesView{On: tpl == dispatchOrdersTemplate, WorkflowID: wf}
		return err
	}); err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// turnOnDeliveries is POST /v1/dispatch/workflow.
func (s *Server) turnOnDeliveries(w http.ResponseWriter, r *http.Request) {
	if !s.dispatchReady(w, r) {
		return
	}
	p := principalFrom(r.Context())
	var out deliveriesView
	if err := s.tx(r, func(tx pgx.Tx) error {
		var err error
		out, err = s.turnOnDeliveriesTx(r.Context(), tx, p, func(action, target string, detail map[string]any) error {
			return auditTx(r, tx, action, target, detail)
		})
		return err
	}); err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// shopTurnOnDeliveries turns deliveries on from chat, once the owner said
// yes.
func (s *Server) shopTurnOnDeliveries(ctx context.Context, c *chat) error {
	if !c.p.Can(PermWorkflowEdit) || !c.p.Can(PermWorkflowPublish) || !c.p.Can(PermStorefrontManage) {
		return c.say(ctx, s, c.t("wa.publish.denied", "org", c.tenant.Name))
	}
	err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{c.tenant.ID}, func(tx pgx.Tx) error {
		_, err := s.turnOnDeliveriesTx(ctx, tx, c.p, func(action, target string, detail map[string]any) error {
			return waAudit(ctx, tx, c.ch.Name(), c.p, action, target, detail)
		})
		return err
	})
	if errors.Is(err, errConflict) {
		return c.say(ctx, s, c.t("wa.dispatch.on_failed", "reason", strings.TrimPrefix(plainError(err), "conflict: ")))
	}
	if err != nil {
		return err
	}
	return c.say(ctx, s, c.t("wa.dispatch.on"))
}
