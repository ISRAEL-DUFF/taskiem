package api

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"errors"
	"fmt"
	"math"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/discover"
	"github.com/israel-duff/taskiem/engine/links"
	"github.com/israel-duff/taskiem/engine/ops"
)

// Discover (docs/spec/taskiem-for-everyone.md §10, engine/discover): the
// public finding businesses in the Taskiem app, kept apart from Explore.
//
// A person signed in (a personal session, or a business session for its
// person; never an API key) searches the listings by name, what they want
// and area, opens a business's profile, starts a chat with it through the
// customer door (POST /v1/me/chats {business_id, from: "discover"}) and
// reports a listing. Everything is read through the narrow definers, so
// nothing of a business beyond its listing is reached, and nothing of a
// search is kept: a person's place comes in the request body, for that
// search only, and is never written or logged.
//
// A business turns its listing on or off and gives what it lacks in
// GET/PUT /v1/settings/discover. Taskiem's moderators take a listing out
// or put it back (POST /v1/ops/discover/{tenant}/{remove,restore}).

func (s *Server) discoverRoutes(r chi.Router) {
	r.Post("/discover/search", s.discoverSearch)
	r.Get("/discover/categories", s.discoverCategories)
	r.Get("/discover/{business}", s.discoverProfile)
	r.Post("/discover/{business}/report", s.discoverReport)
}

// discoverPerson is the person a Discover route is for: any user session
// for its own person; API keys, embed users and agents are refused.
func discoverPerson(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	person, err := personOf(r)
	if err != nil {
		writeErr(w, http.StatusForbidden, "Discover is for a person signed in to their own account.")
		return uuid.Nil, false
	}
	return person, true
}

// discoverSearch is POST /v1/discover/search {q, area, category, near:
// {lat, lng}, offset}: listings, nearest first when near is given. A POST,
// so the person's place travels in the body, where no address bar,
// history or access log keeps it.
func (s *Server) discoverSearch(w http.ResponseWriter, r *http.Request) {
	person, ok := discoverPerson(w, r)
	if !ok {
		return
	}
	var in discover.Query
	if err := decodeBody(r, &in); err != nil {
		s.fail(w, r, err)
		return
	}
	in.Words, in.Area, in.Category = strings.TrimSpace(in.Words), strings.TrimSpace(in.Area), strings.TrimSpace(in.Category)
	if utf8.RuneCountInString(in.Words) > 100 || utf8.RuneCountInString(in.Area) > 80 {
		writeErr(w, http.StatusBadRequest, "A search is up to 100 letters, and an area up to 80.")
		return
	}
	if in.Category != "" && !links.IsCategory(in.Category) {
		writeErr(w, http.StatusBadRequest, "That is not one of Discover's categories.")
		return
	}
	if in.Near != nil && (math.IsNaN(in.Near.Lat) || math.IsNaN(in.Near.Lng) || in.Near.Lat < -90 || in.Near.Lat > 90 || in.Near.Lng < -180 || in.Near.Lng > 180) {
		writeErr(w, http.StatusBadRequest, "near is a latitude (-90 to 90) and a longitude (-180 to 180).")
		return
	}
	if in.Offset < 0 || in.Offset > 200 {
		in.Offset = 0
	}
	if !s.limiter("discover-search:"+person.String(), 2*time.Second, 30).Allow() {
		writeErr(w, http.StatusTooManyRequests, "You are searching too fast. Wait a moment and try again.")
		return
	}
	results, more, err := discover.Search(r.Context(), s.Store.Pool, in, time.Now())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]any{"results": results, "more": more})
}

// discoverCategories is GET /v1/discover/categories: the categories a
// listing has, in the order Discover shows them.
func (s *Server) discoverCategories(w http.ResponseWriter, r *http.Request) {
	if _, ok := discoverPerson(w, r); !ok {
		return
	}
	type category struct {
		Key  string `json:"key"`
		Name string `json:"name"`
	}
	out := []category{}
	for _, c := range links.Categories {
		out = append(out, category{Key: c.Key, Name: c.Name})
	}
	writeJSON(w, http.StatusOK, map[string]any{"categories": out})
}

// discoverProfile is GET /v1/discover/{business}: a business's profile in
// Discover, with the person's own chat with it when they have one (Their
// shop and Book open in that chat, as its tools). 404 for a business that
// is not listed, or whose listing may not be shown now.
func (s *Server) discoverProfile(w http.ResponseWriter, r *http.Request) {
	person, ok := discoverPerson(w, r)
	if !ok {
		return
	}
	business, err := uuid.Parse(chi.URLParam(r, "business"))
	if err != nil {
		writeErr(w, http.StatusNotFound, "This business is not in Discover.")
		return
	}
	if !s.limiter("discover-profile:"+person.String(), time.Second, 60).Allow() {
		writeErr(w, http.StatusTooManyRequests, "Too many tries. Wait a moment and try again.")
		return
	}
	type profileOut struct {
		discover.Profile
		// Chat is the person's own conversation with the business, when
		// they have one.
		Chat *uuid.UUID `json:"chat,omitempty"`
	}
	var out profileOut
	err = db.InPersonTx(r.Context(), s.Store.Pool, person, func(tx pgx.Tx) error {
		var err error
		if out.Profile, err = discover.ProfileOf(r.Context(), tx, business, time.Now()); err != nil {
			return err
		}
		var chat uuid.UUID
		err = tx.QueryRow(r.Context(), `SELECT id FROM person_conversations WHERE person_id = $1 AND tenant_id = $2`, person, business).Scan(&chat)
		if err == nil {
			out.Chat = &chat
		}
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		return err
	})
	if errors.Is(err, discover.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "This business is not in Discover.")
		return
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, out)
}

// discoverReport is POST /v1/discover/{business}/report {reason, note}: a
// report on a listing to Taskiem's moderators, beside the reports on
// pages. Three different people with open reports hide the listing until
// a moderator looks.
func (s *Server) discoverReport(w http.ResponseWriter, r *http.Request) {
	person, ok := discoverPerson(w, r)
	if !ok {
		return
	}
	business, err := uuid.Parse(chi.URLParam(r, "business"))
	if err != nil {
		writeErr(w, http.StatusNotFound, "This business is not in Discover.")
		return
	}
	var in struct {
		Reason string `json:"reason"`
		Note   string `json:"note"`
	}
	if err := decodeBody(r, &in); err != nil {
		s.fail(w, r, err)
		return
	}
	switch in.Reason {
	case "scam", "impersonation", "prohibited", "offensive", "other":
	default:
		writeErr(w, http.StatusBadRequest, "reason is scam, impersonation, prohibited, offensive or other")
		return
	}
	in.Note = strings.TrimSpace(in.Note)
	if utf8.RuneCountInString(in.Note) > 500 {
		writeErr(w, http.StatusBadRequest, "A note is up to 500 characters.")
		return
	}
	if !s.limiter("person-report:"+person.String(), 2*time.Hour, 12).Allow() {
		writeErr(w, http.StatusTooManyRequests, "You have sent many reports. Try again later.")
		return
	}
	var taken bool
	err = db.InPersonTx(r.Context(), s.Store.Pool, person, func(tx pgx.Tx) error {
		return tx.QueryRow(r.Context(), `SELECT taskiem_discover_report($1, $2, $3, $4, $5)`, person, business, in.Reason, in.Note, s.reporterDigest(person)).Scan(&taken)
	})
	var pe *pgconn.PgError
	if errors.As(err, &pe) && pe.Code == "P0002" {
		writeErr(w, http.StatusNotFound, "This business is not in Discover.")
		return
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if !taken {
		writeErr(w, http.StatusTooManyRequests, "You have reported this business several times today. Taskiem's moderators have your reports.")
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"reported": true, "words": "Thank you. Taskiem's moderators will look at this business."})
}

// reporterDigest names a reporter to the reports, only to count different
// people: keyed with the chat links' key where there is one.
func (s *Server) reporterDigest(person uuid.UUID) []byte {
	msg := []byte("discover reporter:" + person.String())
	if s.ChatLinks.Ready() {
		m := hmac.New(sha256.New, s.ChatLinks.Key)
		m.Write(msg)
		return m.Sum(nil)
	}
	h := sha256.Sum256(msg)
	return h[:]
}

// --- the business's settings ---

// getDiscoverSettings is GET /v1/settings/discover: the switch, what the
// listing lacks, why it is hidden, and the listing as it would show.
func (s *Server) getDiscoverSettings(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r.Context())
	var out discover.Settings
	err := s.tx(r, func(tx pgx.Tx) error {
		var err error
		out, err = discover.RefreshTx(r.Context(), tx, p.TenantID, s.previewDomain())
		return err
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// putDiscoverSettings is PUT /v1/settings/discover: turns the listing on or
// off, and gives its own category, area, hours and place; the listing is
// written again at once. Audited as discover.settings.
func (s *Server) putDiscoverSettings(w http.ResponseWriter, r *http.Request) {
	var in discover.SettingsIn
	if err := decodeBody(r, &in); err != nil {
		s.fail(w, r, err)
		return
	}
	p := principalFrom(r.Context())
	var out discover.Settings
	err := s.tx(r, func(tx pgx.Tx) error {
		var err error
		out, err = discover.SaveTx(r.Context(), tx, p.TenantID, in, p.ActorType()+":"+p.Actor(), s.previewDomain())
		switch {
		case errors.Is(err, discover.ErrWorkspace):
			return fmt.Errorf("%w: A workflows workspace is not listed in Discover. Turn on an area of a business first.", errConflict)
		case errors.Is(err, discover.ErrInvalid):
			return fmt.Errorf("%w: %s", errBadRequest, strings.TrimPrefix(err.Error(), "discover: invalid: "))
		case err != nil:
			return err
		}
		return auditTx(r, tx, "discover.settings", "discover", map[string]any{"listed": out.Listed, "chosen": out.Chosen, "category": out.Category,
			"area": out.Area, "own_hours": out.Hours != nil, "location": out.Location != nil, "shown": out.Shown})
	})
	if errors.Is(err, errConflict) {
		writeErr(w, http.StatusConflict, strings.TrimPrefix(err.Error(), "conflict: "))
		return
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// --- the moderators ---

// opsDiscoverAction is POST /v1/ops/discover/{tenant}/{remove,restore}: a
// moderator takes a listing out of Discover (a reason is required; the
// owners are told it) or puts it back. Open reports on it are acted on, or
// dismissed. A passkey step-up; audited in both chains.
func (s *Server) opsDiscoverAction(w http.ResponseWriter, r *http.Request) {
	tenant, err := uuid.Parse(chi.URLParam(r, "tenant"))
	action := chi.URLParam(r, "action")
	if err != nil || (action != "remove" && action != "restore") {
		writeErr(w, http.StatusNotFound, "the action is remove or restore")
		return
	}
	var req struct {
		Reason string     `json:"reason"`
		StepUp *opsStepUp `json:"step_up,omitempty"`
	}
	if err := decodeBody(r, &req); err != nil {
		s.fail(w, r, err)
		return
	}
	req.Reason = strings.TrimSpace(req.Reason)
	if action == "remove" && (req.Reason == "" || utf8.RuneCountInString(req.Reason) > 500) {
		writeErr(w, http.StatusUnprocessableEntity, "taking a listing out needs a reason (up to 500 characters): the owner is told")
		return
	}
	if !s.checkOpsStepUp(w, r, req.StepUp, "ops.discover."+action, tenant.String()) {
		return
	}
	o := operatorFrom(r.Context())
	var out discover.Settings
	err = s.opsTx(r, func(tx pgx.Tx, widen func(uuid.UUID) error) error {
		ctx := r.Context()
		if err := widen(tenant); err != nil {
			return err
		}
		var err error
		if out, err = discover.RemoveTx(ctx, tx, tenant, action == "restore", req.Reason, o.Actor(), s.previewDomain()); err != nil {
			return err
		}
		if err := tenantAudit(r, tx, tenant, o.Actor(), "discover."+action, tenant.String(), map[string]any{"reason": req.Reason}); err != nil {
			return err
		}
		return ops.AuditTx(ctx, tx, o.Actor(), "ops.discover."+action, tenant.String(), map[string]any{"reason": req.Reason})
	})
	if err != nil {
		s.opsDBFail(w, r, err)
		return
	}
	if action == "remove" {
		s.tellOwnersAt(r.Context(), tenant, "Your listing was taken out of Discover", req.Reason+" Customers can still reach you through your chat link.", discover.SettingsPath)
	} else {
		s.tellOwnersAt(r.Context(), tenant, "Your listing is back in Discover", "Taskiem put your listing back in Discover.", discover.SettingsPath)
	}
	writeJSON(w, http.StatusOK, map[string]any{"tenant_id": tenant, "shown": out.Shown, "hidden": out.Hidden})
}

// --- the sweep ---

// discoverSweeper is the index's sweep, made from the server's settings.
func (s *Server) discoverSweeper() *discover.Sweeper {
	return &discover.Sweeper{Pool: s.Store.Pool, PreviewDomain: s.previewDomain(), PublicURL: s.PublicURL}
}

// DiscoverTick writes again the listings whose sources changed, and tells
// the businesses listed by default, once (§10).
func (s *Server) DiscoverTick(ctx context.Context) error {
	sw := s.discoverSweeper()
	_, err := sw.Sweep(ctx, 200)
	_, terr := sw.Tell(ctx, 50)
	return errors.Join(err, terr)
}

// RunDiscover runs DiscoverTick every minute until ctx ends.
func (s *Server) RunDiscover(ctx context.Context) error {
	for {
		if err := s.DiscoverTick(ctx); err != nil && ctx.Err() == nil {
			s.Logger.Error("discover", "err", err)
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(time.Minute):
		}
	}
}
