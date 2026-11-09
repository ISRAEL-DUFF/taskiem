package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/links"
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
		// A rider whose business is linked with since they were added is
		// named as that business (docs/spec/chat-riders.md, phase 2).
		if _, err := s.Riders.ClaimLinkedTx(r.Context(), tx, p.TenantID); err != nil {
			return err
		}
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
	// Someone who works the orders without the storefront's say (a
	// cashier) picks a rider by name and area: not their phone, pay or
	// bank, nor the link that adds riders.
	if !p.Can(PermStorefrontManage) {
		for i := range out.Riders {
			out.Riders[i].Phone = maskPhone(out.Riders[i].Phone)
			out.Riders[i].FeeKobo = 0
			out.Riders[i].Bank = ""
		}
		out.TapIn = ""
	}
	writeJSON(w, http.StatusOK, out)
}

// maskPhone keeps a phone number's last four digits.
func maskPhone(v string) string {
	if len(v) <= 4 {
		return v
	}
	return "•••" + v[len(v)-4:]
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
	var asked bool
	err := s.tx(r, func(tx pgx.Tx) error {
		var err error
		if x, err = s.Riders.AddTx(r.Context(), tx, p.TenantID, in, p.Actor()); err != nil {
			return err
		}
		x, asked, err = s.linkRiderTx(r.Context(), tx, p.TenantID, x, links.Actor{Type: p.ActorType(), ID: p.Actor()})
		return err
	})
	if err != nil {
		s.riderFail(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"rider": x, "link_requested": asked})
}

// linkRiderTx links a business with a rider just added whose phone is a
// business on Taskiem (docs/spec/chat-riders.md, phase 2): one already
// linked with is named on the rider at once; otherwise the rider's
// business is asked to link, as a supplier, from the phone (a blind
// request: nothing is told of who is behind the number until they answer).
// true when a request went out. A business past the day's requests, or
// one that cannot be asked, leaves the rider as added.
func (s *Server) linkRiderTx(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, x riders.Rider, by links.Actor) (riders.Rider, bool, error) {
	if x.LinkID != nil || x.Phone == "" {
		return x, false, nil
	}
	is, err := riders.PhoneIsBusinessTx(ctx, tx, x.Phone)
	if err != nil || !is {
		return x, false, err
	}
	if _, err := s.Riders.ClaimLinkedTx(ctx, tx, tenant); err != nil {
		return x, false, err
	}
	if x, err = s.Riders.GetTx(ctx, tx, tenant, x.ID); err != nil || x.LinkID != nil {
		return x, false, err
	}
	told, err := links.RequestBlindTx(ctx, tx, tenant, links.Blind{Phone: x.Phone}, links.RoleSupplier, by, s.linkKey(tenant))
	switch {
	case errors.Is(err, links.ErrTooMany), errors.Is(err, links.ErrState), errors.Is(err, links.ErrInvalid):
		s.Logger.Warn("riders: asking the rider's business to link", "tenant", tenant, "err", err)
		return x, false, nil
	case err != nil:
		return x, false, err
	}
	return x, told != uuid.Nil, nil
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
