package api

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/dispatch"
	"github.com/israel-duff/taskiem/engine/links"
	"github.com/israel-duff/taskiem/engine/media"
)

// Deliveries as a linked rider's orders (docs/spec/chat-riders.md, phase 1):
// a rider that is a business on Taskiem holds each delivery as an order
// in its own business and moves it from there, as its WhatsApp buttons
// would: accept, decline (Can't), picked_up, arrived, delivered (with a
// photo when the business asks for proof). The step runs on the business's
// delivery through the door, and the rider's order and both cards follow
// in the same transaction.

// deliveryOrderSteps are the steps a rider takes on a delivery order, by the
// step's name on the route, as dispatch names them.
var deliveryOrderSteps = map[string]string{
	links.StepAccept:  dispatch.Accept,
	links.StepDecline: dispatch.Cant,
	"cant":            dispatch.Cant,
	"picked_up":       dispatch.PickedUp,
	"arrived":         dispatch.Arrived,
	"delivered":       dispatch.Delivered,
}

// deliveryStep takes a rider's step on a delivery order of theirs
// (tradeOrderStep, when the order is a delivery): the photo first, when one
// came with Delivered, then the step. It answers the order as it then is.
func (s *Server) deliveryStep(w http.ResponseWriter, r *http.Request, ref links.DeliveryRef, step string) {
	ctx := r.Context()
	p := principalFrom(ctx)
	action := deliveryOrderSteps[step]
	if !s.Dispatch.Ready() {
		s.fail(w, r, errors.New("deliveries are not configured"))
		return
	}
	// The photo, when one came with Delivered, is kept once the step is
	// taken: a refused step (not paid at the door yet) keeps nothing.
	var photo []byte
	if action == dispatch.Delivered && strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/") {
		r.Body = http.MaxBytesReader(w, r.Body, media.MaxImageBytes+(1<<20))
		if err := r.ParseMultipartForm(1 << 20); err != nil { //nolint:gosec // the body is bounded by MaxBytesReader above
			s.fail(w, r, fmt.Errorf("%w: send the photo as a multipart form with a photo field", errBadRequest))
			return
		}
		defer func() { _ = r.MultipartForm.RemoveAll() }()
		if f, _, err := r.FormFile("photo"); err == nil {
			photo, err = io.ReadAll(f)
			_ = f.Close()
			if err != nil || len(photo) == 0 {
				s.fail(w, r, fmt.Errorf("%w: the photo could not be read", errBadRequest))
				return
			}
		}
	}
	tap, _, err := s.Dispatch.RiderStep(ctx, p.TenantID, ref.OrderID.String(), action)
	switch {
	case errors.Is(err, dispatch.ErrMove):
		s.fail(w, r, fmt.Errorf("%w: %s", errConflict, strings.TrimPrefix(err.Error(), dispatch.ErrMove.Error()+": ")))
		return
	case err != nil:
		s.fail(w, r, orderErr(err))
		return
	}
	if len(photo) > 0 && tap.Moved {
		// The proof is the business's, in its own storage, as a photo sent
		// from WhatsApp is.
		path, err := s.storeProof(ctx, ref.Business, photo)
		if err != nil {
			s.Logger.Warn("deliveries: keeping the proof photo", "err", err)
		} else if err := s.Dispatch.SetProof(ctx, ref.Business, ref.DeliveryID, path, ""); err != nil {
			s.Logger.Warn("deliveries: recording the proof photo", "err", err)
		}
	}
	var o links.Order
	if err := s.tx(r, func(tx pgx.Tx) error {
		var err error
		o, err = links.GetOrderTx(ctx, tx, ref.OrderID.String(), false)
		if err != nil {
			return err
		}
		return s.openHeld(ctx, tx, p, &o)
	}); err != nil {
		s.fail(w, r, orderErr(err))
		return
	}
	s.diaryChange(ctx, p, deliveryStepWords(action, o.Reference, o.BuyerName))
	writeJSON(w, http.StatusOK, map[string]any{"order": o, "short": []string{}})
}

// openHeld fills who a delivery goes to, for the rider while they hold it.
func (s *Server) openHeld(ctx context.Context, tx pgx.Tx, p *Principal, o *links.Order) error {
	if !o.Held() || s.Vault == nil {
		return nil
	}
	c, err := links.OpenDeliveryCustomerTx(ctx, tx, s.Vault, p.TenantID, o.ID)
	if err != nil {
		return err
	}
	o.Delivery.Customer = &c
	return nil
}

// deliveryStepWords is the rider's diary line for a step.
func deliveryStepWords(action, reference, business string) string {
	switch action {
	case dispatch.Accept:
		return fmt.Sprintf("Taking %s for %s", reference, business)
	case dispatch.Cant:
		return fmt.Sprintf("Passed on %s for %s", reference, business)
	case dispatch.PickedUp:
		return fmt.Sprintf("Picked up %s from %s", reference, business)
	case dispatch.Arrived:
		return fmt.Sprintf("At the door with %s", reference)
	case dispatch.Delivered:
		return fmt.Sprintf("Delivered %s for %s", reference, business)
	}
	return fmt.Sprintf("%s: %s", reference, strings.ReplaceAll(action, "_", " "))
}
