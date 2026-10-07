package api

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/israel-duff/taskiem/engine/status"
)

// StatusSettings is the public status page and its admin API (Phase 4,
// P4-4; docs/reliability.md#status-page).
type StatusSettings struct {
	// Page serves /status, /status.json and /status/feed.atom; nil turns
	// the page off.
	Page *status.Handler
	// Tokens are the operators who may declare incidents through
	// /v1/status/admin (TASKIEM_STATUS_TOKENS); none turns the admin API
	// off (operators use taskiem status).
	Tokens []OperatorToken
}

// OperatorToken is an operator's admin token, kept as its SHA-256.
type OperatorToken struct {
	Name string
	Hash [sha256.Size]byte
}

// ParseOperatorTokens reads "name:sha256hex,..." (taskiem status token
// prints one).
func ParseOperatorTokens(v string) ([]OperatorToken, error) {
	var out []OperatorToken
	for _, part := range strings.Split(v, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		name, hexHash, ok := strings.Cut(part, ":")
		raw, err := hex.DecodeString(hexHash)
		if !ok || err != nil || len(raw) != sha256.Size || !nameRe.MatchString(name) {
			return nil, fmt.Errorf("each operator token is name:sha256-hex (taskiem status token NAME prints one), not %q", name)
		}
		var t OperatorToken
		t.Name = name
		copy(t.Hash[:], raw)
		out = append(out, t)
	}
	return out, nil
}

// operator is who presented a valid admin token, or "".
func (s *Server) operator(r *http.Request) string {
	tok, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !ok || tok == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(tok))
	name := ""
	for _, t := range s.Status.Tokens {
		if subtle.ConstantTimeCompare(sum[:], t.Hash[:]) == 1 {
			name = t.Name
		}
	}
	return name
}

type operatorKey struct{}

// statusAdmin admits operators with a token; anything else is 401, and
// failures are rate limited per address.
func (s *Server) statusAdmin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.Status == nil || len(s.Status.Tokens) == 0 {
			writeErr(w, http.StatusNotFound, "not found")
			return
		}
		lim := s.limiter("status-admin:"+clientIP(r), 6*time.Second, 10)
		name := s.operator(r)
		if name == "" {
			if !lim.Allow() {
				writeErr(w, http.StatusTooManyRequests, "too many attempts")
				return
			}
			writeErr(w, http.StatusUnauthorized, "an operator token is required")
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), operatorKey{}, "api:"+name)))
	})
}

func (s *Server) statusAdminRoutes(r chi.Router) {
	r.Use(s.statusAdmin)
	r.Get("/incidents", s.listStatusIncidents)
	r.Post("/incidents", s.openStatusIncident)
	r.Post("/incidents/{id}/updates", s.postStatusUpdate)
}

func (s *Server) listStatusIncidents(w http.ResponseWriter, r *http.Request) {
	list, err := status.Incidents(r.Context(), s.Store.Pool, time.Now().AddDate(0, 0, -90), true)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"incidents": nonNil(list)})
}

func (s *Server) statusFail(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, status.ErrInvalid):
		writeErr(w, http.StatusUnprocessableEntity, err.Error())
	case errors.Is(err, status.ErrNotFound):
		writeErr(w, http.StatusNotFound, err.Error())
	default:
		s.fail(w, r, err)
	}
}

func (s *Server) openStatusIncident(w http.ResponseWriter, r *http.Request) {
	var d status.Declaration
	if err := decodeBody(r, &d); err != nil {
		s.fail(w, r, err)
		return
	}
	actor, _ := r.Context().Value(operatorKey{}).(string)
	id, err := status.Open(r.Context(), s.Store.Pool, d, actor)
	if err != nil {
		s.statusFail(w, r, err)
		return
	}
	s.Logger.Info("status: incident opened", "incident", id, "kind", d.Kind, "by", actor)
	if s.Status.Page != nil {
		s.Status.Page.Invalidate()
	}
	writeJSON(w, http.StatusCreated, map[string]any{"id": id})
}

func (s *Server) postStatusUpdate(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeErr(w, http.StatusNotFound, "no such incident")
		return
	}
	var c status.Change
	if err := decodeBody(r, &c); err != nil {
		s.fail(w, r, err)
		return
	}
	actor, _ := r.Context().Value(operatorKey{}).(string)
	if err := status.Post(r.Context(), s.Store.Pool, id, c, actor); err != nil {
		s.statusFail(w, r, err)
		return
	}
	s.Logger.Info("status: incident updated", "incident", id, "status", c.Status, "by", actor)
	if s.Status.Page != nil {
		s.Status.Page.Invalidate()
	}
	writeJSON(w, http.StatusCreated, map[string]any{"id": id})
}
