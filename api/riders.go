package api

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/riders"
	"github.com/israel-duff/taskiem/engine/storefront"
)

// The business's own riders (docs/dispatch.md D1, decision 0055):
// Storefront > Riders lists them with where they are reached and their
// state, adds them by name and phone (sending the tap-in link), changes
// their areas and fee, and retires them.

type ridersView struct {
	Riders []riders.Rider `json:"riders"`
	// Zones are the order form's delivery areas, which riders cover.
	Zones []storefront.Zone `json:"zones"`
	// TapIn is the page a rider opens to get deliveries on WhatsApp or
	// Telegram; the same link the SMS sends.
	TapIn string `json:"tap_in_url,omitempty"`
}

func (s *Server) ridersReady(w http.ResponseWriter, r *http.Request) bool {
	if !s.Riders.Ready() {
		s.fail(w, r, fmt.Errorf("%w: riders need messages set up on this Taskiem", errConflict))
		return false
	}
	return true
}

// riderFail maps riders' refusals to plain answers.
func (s *Server) riderFail(w http.ResponseWriter, r *http.Request, err error) {
	msg := err.Error()
	for _, p := range []string{"riders: invalid: ", "riders: already a rider: ", "riders: the link was sent a moment ago: "} {
		msg = strings.ReplaceAll(msg, p, "")
	}
	switch {
	case errors.Is(err, riders.ErrNoRider):
		err = pgx.ErrNoRows
	case errors.Is(err, riders.ErrInvalid):
		err = fmt.Errorf("%w: %s", errBadRequest, msg)
	case errors.Is(err, riders.ErrExists), errors.Is(err, riders.ErrTooSoon):
		err = fmt.Errorf("%w: %s", errConflict, msg)
	}
	s.fail(w, r, err)
}

// listRiders is GET /v1/riders (?retired=true for those retired).
func (s *Server) listRiders(w http.ResponseWriter, r *http.Request) {
	if !s.ridersReady(w, r) {
		return
	}
	p := principalFrom(r.Context())
	out := ridersView{}
	err := s.tx(r, func(tx pgx.Tx) error {
		var err error
		if out.Riders, err = s.Riders.ListTx(r.Context(), tx, p.TenantID, riders.Filter{Retired: r.URL.Query().Get("retired") == "true"}); err != nil {
			return err
		}
		if out.Zones, err = storefront.Zones(r.Context(), tx, p.TenantID); err != nil {
			return err
		}
		out.TapIn, err = s.Riders.TapIn(r.Context(), tx, p.TenantID)
		return err
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// addRider is POST /v1/riders: a name and phone, their areas and fee.
func (s *Server) addRider(w http.ResponseWriter, r *http.Request) {
	if !s.ridersReady(w, r) {
		return
	}
	var in riders.In
	if err := decodeBody(r, &in); err != nil {
		s.fail(w, r, err)
		return
	}
	p := principalFrom(r.Context())
	var x riders.Rider
	err := s.tx(r, func(tx pgx.Tx) error {
		var err error
		x, err = s.Riders.AddTx(r.Context(), tx, p.TenantID, in, p.Actor())
		return err
	})
	if err != nil {
		s.riderFail(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"rider": x})
}

// riderAction runs one change to a rider named in the path.
func (s *Server) riderAction(w http.ResponseWriter, r *http.Request, do func(pgx.Tx, uuid.UUID, uuid.UUID, string) (riders.Rider, error)) {
	if !s.ridersReady(w, r) {
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	p := principalFrom(r.Context())
	var x riders.Rider
	err = s.tx(r, func(tx pgx.Tx) error {
		var err error
		x, err = do(tx, p.TenantID, id, p.Actor())
		return err
	})
	if err != nil {
		s.riderFail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"rider": x})
}

// updateRider is PATCH /v1/riders/{id}: their name, areas, fee, or
// whether they are active.
func (s *Server) updateRider(w http.ResponseWriter, r *http.Request) {
	var in riders.In
	if err := decodeBody(r, &in); err != nil {
		s.fail(w, r, err)
		return
	}
	s.riderAction(w, r, func(tx pgx.Tx, tenant, id uuid.UUID, actor string) (riders.Rider, error) {
		return s.Riders.UpdateTx(r.Context(), tx, tenant, id, in, actor)
	})
}

// retireRider is POST /v1/riders/{id}/retire.
func (s *Server) retireRider(w http.ResponseWriter, r *http.Request) {
	s.riderAction(w, r, func(tx pgx.Tx, tenant, id uuid.UUID, actor string) (riders.Rider, error) {
		return s.Riders.RetireTx(r.Context(), tx, tenant, id, actor)
	})
}

// inviteRider is POST /v1/riders/{id}/invite: the tap-in link by SMS
// again.
func (s *Server) inviteRider(w http.ResponseWriter, r *http.Request) {
	s.riderAction(w, r, func(tx pgx.Tx, tenant, id uuid.UUID, actor string) (riders.Rider, error) {
		return s.Riders.InviteTx(r.Context(), tx, tenant, id, actor)
	})
}
