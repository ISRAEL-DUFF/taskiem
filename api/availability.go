package api

import (
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/riders"
)

// A rider's availability (docs/spec/chat-riders.md, phase 3): the rider's
// business says it is free (on), off, or busy till a time, from its chat's
// Availability tool; every business that uses it as a rider reads that as
// it reads a rider's words. The words on, off and busy till 3 texted from
// the rider's own WhatsApp do the same (whatsapp_riders.go).

// getAvailability is GET /v1/availability.
func (s *Server) getAvailability(w http.ResponseWriter, r *http.Request) {
	var out riders.Availability
	if err := s.tx(r, func(tx pgx.Tx) error {
		var err error
		out, err = riders.AvailabilityTx(r.Context(), tx)
		return err
	}); err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// putAvailability is PUT /v1/availability {"state": "on" | "off" | "busy",
// "until": "15:30" (busy; a clock time today, Lagos) }.
func (s *Server) putAvailability(w http.ResponseWriter, r *http.Request) {
	var req struct {
		State string `json:"state"`
		Until string `json:"until"`
	}
	if err := decodeBody(r, &req); err != nil {
		s.fail(w, r, err)
		return
	}
	said := riders.Said{State: req.State}
	if req.State == "free" {
		said.State = "on"
	}
	if said.State == "busy" {
		word := "busy"
		if req.Until != "" {
			word = "busy till " + req.Until
		}
		var ok bool
		if said, ok = riders.ParseWord(word, time.Now()); !ok {
			s.fail(w, r, fmt.Errorf("%w: a time like 3pm or 15:30", errBadRequest))
			return
		}
	}
	p := principalFrom(r.Context())
	var out riders.Availability
	if err := s.tx(r, func(tx pgx.Tx) error {
		n, err := riders.SayOwnTx(r.Context(), tx, said)
		if err != nil {
			return err
		}
		if n == 0 {
			return fmt.Errorf("%w: no business has added you as its rider yet", errConflict)
		}
		if err := auditTx(r, tx, "rider.availability", p.TenantID.String(), map[string]any{"state": said.State}); err != nil {
			return err
		}
		out, err = riders.AvailabilityTx(r.Context(), tx)
		return err
	}); err != nil {
		if errors.Is(err, riders.ErrInvalid) {
			err = fmt.Errorf("%w: %s", errBadRequest, plainError(err))
		}
		s.fail(w, r, err)
		return
	}
	s.diaryChange(r.Context(), p, availabilityWords(out))
	writeJSON(w, http.StatusOK, out)
}

// availabilityWords is the diary line for a change of availability.
func availabilityWords(a riders.Availability) string {
	switch a.State {
	case "off":
		return "Off today: no deliveries offered"
	case "busy":
		if a.Until != nil {
			return "Busy till " + riders.Clock(*a.Until)
		}
		return "Busy"
	}
	return "Free for deliveries"
}
