package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/dispatch"
	"github.com/israel-duff/taskiem/engine/invoicing"
	"github.com/israel-duff/taskiem/engine/runtime"
)

// Riders are paid per delivery (docs/spec/chat-riders.md, phase 4): the
// business confirms a delivered delivery from its card, its deliveries or
// WhatsApp, and the fee goes out through the pay-riders workflow (one
// payout, one approval where the policy asks for one), or is recorded as
// handed over in cash.

// confirmOrder is POST /v1/storefront/orders/{reference}/confirm {"how":
// "bank" | "cash"}: the fee for the order's delivery is paid.
func (s *Server) confirmOrder(w http.ResponseWriter, r *http.Request) {
	if !s.dispatchReady(w, r) {
		return
	}
	var req struct {
		How string `json:"how"`
	}
	if r.ContentLength != 0 {
		if err := decodeBody(r, &req); err != nil {
			s.fail(w, r, err)
			return
		}
	}
	p := principalFrom(r.Context())
	out, run, err := s.confirmDelivery(r.Context(), p, chi.URLParam(r, "reference"), req.How)
	if err != nil {
		s.dispatchFail(w, r, err)
		return
	}
	res := map[string]any{"delivery": out.Delivery, "net_kobo": out.NetKobo}
	if run != "" {
		res["run_id"] = run
	}
	writeJSON(w, http.StatusOK, res)
}

// confirmDelivery confirms the delivery that holds an order and, when a
// payment is left to make, starts the pay-riders run for it. The run's
// id is answered when one started.
func (s *Server) confirmDelivery(ctx context.Context, p *Principal, reference, how string) (dispatch.Confirmed, string, error) {
	var out dispatch.Confirmed
	var wf *uuid.UUID
	var version int
	err := db0(ctx, s, p.TenantID, func(tx pgx.Tx) error {
		ord, err := s.Orders.GetOrder(ctx, tx, p.TenantID, reference)
		if err != nil {
			return err
		}
		x, err := s.Dispatch.HeldTx(ctx, tx, p.TenantID, ord.ID)
		if err != nil {
			return err
		}
		if out, err = s.Dispatch.ConfirmTx(ctx, tx, p.TenantID, x.ID, how, p.Actor()); err != nil {
			return err
		}
		if out.Batch == uuid.Nil {
			return nil
		}
		// The payment goes through the pay-riders workflow: set up once
		// under Riders > Pay, and published.
		err = tx.QueryRow(ctx, `SELECT payout_workflow_id FROM dispatch_settings WHERE tenant_id = $1`, p.TenantID).Scan(&wf)
		if errors.Is(err, pgx.ErrNoRows) || err == nil && wf == nil {
			return fmt.Errorf("%w: set up paying riders first (Riders > Pay), or mark the fee paid in cash", dispatch.ErrMove)
		}
		if err != nil {
			return err
		}
		version, err = deployedVersion(ctx, tx, *wf, "prod")
		if errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("%w: paying riders waits to be published", dispatch.ErrMove)
		}
		return err
	})
	if err != nil {
		return out, "", err
	}
	run := ""
	if out.Batch != uuid.Nil {
		ref, _, err := s.Store.StartRun(ctx, runtime.StartRequest{TenantID: p.TenantID, WorkflowID: *wf, Version: version, Environment: "prod",
			Trigger: map[string]any{"type": "manual", "body": map[string]any{"batch_id": out.Batch.String()}}, StartedBy: p.Actor(), TriggerID: "riders/confirm",
			DedupKey: "riders-pay:" + out.Batch.String()})
		if err != nil {
			// Confirmed and the payout made; the owner pays it from Riders >
			// Pay, which reads the batch back.
			s.Logger.Warn("dispatch: starting the fee's payout", "tenant", p.TenantID, "batch", out.Batch, "err", err)
		} else {
			run = ref.ID.String()
		}
	}
	s.diaryChange(ctx, p, confirmWords(out, how))
	return out, run, nil
}

// confirmWords is the diary line for a confirm.
func confirmWords(c dispatch.Confirmed, how string) string {
	x := c.Delivery
	switch {
	case how == dispatch.FeeInCash:
		return fmt.Sprintf("Confirmed %s: %s paid %s in cash", x.Reference, x.Rider, invoicing.Naira(x.FeeKobo))
	case c.Batch == uuid.Nil:
		return fmt.Sprintf("Confirmed %s: %s's fee of %s taken from the cash they held", x.Reference, x.Rider, invoicing.Naira(x.FeeKobo))
	}
	return fmt.Sprintf("Confirmed %s: paying %s %s", x.Reference, x.Rider, invoicing.Naira(c.NetKobo))
}

// confirmAlerts are the deliveries delivered and not yet confirmed, so
// the rider's fee waits: a card with Confirm.
func (s *Server) confirmAlerts(ctx context.Context, p *Principal) ([]chatAlert, error) {
	if !p.Can(PermStorefrontManage) || s.Dispatch == nil || !s.Dispatch.Ready() {
		return nil, nil
	}
	var out []chatAlert
	err := db0(ctx, s, p.TenantID, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT d.id, o.reference, d.fee_kobo, d.delivered_at, r.id FROM deliveries d
			JOIN storefront_orders o ON o.id = d.order_id JOIN riders r ON r.id = d.rider_id
			WHERE d.tenant_id = $1 AND d.status = 'delivered' AND d.confirmed_at IS NULL AND d.payout_id IS NULL
			  AND d.delivered_at > now() - interval '14 days'
			ORDER BY d.delivered_at LIMIT 20`, p.TenantID)
		if err != nil {
			return err
		}
		defer rows.Close()
		type row struct {
			id    uuid.UUID
			ref   string
			fee   int64
			at    time.Time
			rider uuid.UUID
		}
		var list []row
		for rows.Next() {
			var x row
			if err := rows.Scan(&x.id, &x.ref, &x.fee, &x.at, &x.rider); err != nil {
				return err
			}
			list = append(list, x)
		}
		if err := rows.Err(); err != nil {
			return err
		}
		for _, x := range list {
			name := "the rider"
			if rd, err := s.Riders.GetTx(ctx, tx, p.TenantID, x.rider); err == nil {
				name = rd.Name
			}
			fee := x.fee
			out = append(out, chatAlert{Key: "confirm:" + x.id.String(), Kind: "confirm", At: x.at, ID: x.id.String(), Reference: x.ref,
				Title: "Confirm " + x.ref + ", delivered by " + name, Line: invoicing.Naira(fee) + " fee waits on your word", AmountKobo: &fee})
		}
		return nil
	})
	return out, err
}
