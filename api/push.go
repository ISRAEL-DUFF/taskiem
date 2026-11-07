package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/lang"
	"github.com/israel-duff/taskiem/engine/links"
	"github.com/israel-duff/taskiem/engine/push"
	"github.com/israel-duff/taskiem/engine/telemetry"
	"github.com/israel-duff/taskiem/engine/whatsapp"
)

// Chat push (docs/spec/chat-push.md): the browsers a person allowed to be
// notified when the app is closed, and the notification itself. A person
// allows each browser for the business they are in; a push goes to the members
// whose role holds chat.trade when a message comes from a linked business,
// unless the conversation is muted, as the messages to their phones do.

func (s *Server) pushRoutes(r chi.Router) {
	r.Get("/me/push/key", s.pushKey)
	r.Get("/me/push", s.listPush)
	r.Put("/me/push", s.putPush)
	r.Delete("/me/push", s.deletePushEndpoint)
	r.Patch("/me/push/{id}", s.patchPush)
	r.Delete("/me/push/{id}", s.deletePush)
}

var errPushOff = errors.New("push is not set up on this server")

func pushErr(err error) error {
	switch {
	case errors.Is(err, push.ErrInvalid):
		return fmt.Errorf("%w: %s", errBadRequest, strings.TrimPrefix(err.Error(), "push: invalid: "))
	case errors.Is(err, push.ErrNotFound):
		return pgx.ErrNoRows
	}
	return err
}

// pushPerson is the signed-in person, for a route that is theirs alone; the
// feature being off answers as nothing there.
func (s *Server) pushPerson(w http.ResponseWriter, r *http.Request) (*Principal, bool) {
	if !s.Push.Enabled() {
		s.fail(w, r, pgx.ErrNoRows)
		return nil, false
	}
	p := principalFrom(r.Context())
	if p.UserID == uuid.Nil {
		s.fail(w, r, fmt.Errorf("%w: a push belongs to a person, not an API key", errForbidden))
		return nil, false
	}
	return p, true
}

func (s *Server) pushKey(w http.ResponseWriter, r *http.Request) {
	if !s.Push.Enabled() {
		s.fail(w, r, pgx.ErrNoRows)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"public_key": s.Push.Config.PublicKey})
}

type pushOut struct {
	Devices []push.Device `json:"devices"`
	// Max is how many browsers a person may allow in a business.
	Max int `json:"max"`
	// Added is the browser a PUT just added or updated (so the browser knows
	// which of the list it is).
	Added *uuid.UUID `json:"added,omitempty"`
}

func (s *Server) pushView(r *http.Request, p *Principal) (pushOut, error) {
	out := pushOut{Max: push.MaxDevices}
	err := s.tx(r, func(tx pgx.Tx) error {
		var err error
		out.Devices, err = push.ListTx(r.Context(), tx, p.TenantID, p.UserID)
		return err
	})
	return out, err
}

func (s *Server) listPush(w http.ResponseWriter, r *http.Request) {
	p, ok := s.pushPerson(w, r)
	if !ok {
		return
	}
	out, err := s.pushView(r, p)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) putPush(w http.ResponseWriter, r *http.Request) {
	p, ok := s.pushPerson(w, r)
	if !ok {
		return
	}
	var in push.Subscription
	if err := decodeBody(r, &in); err != nil {
		s.fail(w, r, err)
		return
	}
	if err := s.Push.Check(in.Endpoint); err != nil {
		s.fail(w, r, pushErr(err))
		return
	}
	var added uuid.UUID
	if err := s.tx(r, func(tx pgx.Tx) error {
		d, err := push.AddTx(r.Context(), tx, p.TenantID, p.UserID, in)
		if err != nil {
			return err
		}
		added = d.ID
		return auditTx(r, tx, "push.add", d.ID.String(), map[string]any{"label": d.Label})
	}); err != nil {
		s.fail(w, r, pushErr(err))
		return
	}
	out, err := s.pushView(r, p)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	out.Added = &added
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) patchPush(w http.ResponseWriter, r *http.Request) {
	p, ok := s.pushPerson(w, r)
	if !ok {
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		s.fail(w, r, pgx.ErrNoRows)
		return
	}
	var in struct {
		Label   *string `json:"label"`
		Preview *bool   `json:"preview"`
	}
	if err := decodeBody(r, &in); err != nil {
		s.fail(w, r, err)
		return
	}
	if err := s.tx(r, func(tx pgx.Tx) error {
		_, err := push.UpdateTx(r.Context(), tx, p.TenantID, p.UserID, id, in.Label, in.Preview)
		return err
	}); err != nil {
		s.fail(w, r, pushErr(err))
		return
	}
	s.listPush(w, r)
}

func (s *Server) deletePush(w http.ResponseWriter, r *http.Request) {
	p, ok := s.pushPerson(w, r)
	if !ok {
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		s.fail(w, r, pgx.ErrNoRows)
		return
	}
	if err := s.tx(r, func(tx pgx.Tx) error {
		if err := push.RemoveTx(r.Context(), tx, p.TenantID, p.UserID, id); err != nil {
			return err
		}
		return auditTx(r, tx, "push.remove", id.String(), nil)
	}); err != nil {
		s.fail(w, r, pushErr(err))
		return
	}
	s.listPush(w, r)
}

// deletePushEndpoint is a browser taking itself off (the switch, signing out):
// by its address, which only it knows. Nothing there is not an error.
func (s *Server) deletePushEndpoint(w http.ResponseWriter, r *http.Request) {
	p, ok := s.pushPerson(w, r)
	if !ok {
		return
	}
	var in struct {
		Endpoint string `json:"endpoint"`
	}
	if err := decodeBody(r, &in); err != nil {
		s.fail(w, r, err)
		return
	}
	if err := s.tx(r, func(tx pgx.Tx) error {
		if err := push.RemoveEndpointTx(r.Context(), tx, p.TenantID, p.UserID, in.Endpoint); err != nil {
			return err
		}
		return auditTx(r, tx, "push.remove", "this browser", nil)
	}); err != nil {
		s.fail(w, r, err)
		return
	}
	s.listPush(w, r)
}

// pushMaxParallel bounds the requests to push services for one message.
const pushMaxParallel = 8

// pushMessage pushes a message that came from a linked business (fromTenant, named from) to the
// browsers of the receiving business's members who write in its
// conversations: the title is the sender's business, the body the same line
// the phone messages carry (a browser that switched the preview off is sent
// "New message"), the link the conversation. It does what the sender's
// tellLinkMessage has decided is to be told (the mute is checked there).
func (s *Server) pushMessage(ctx context.Context, to, link, fromTenant uuid.UUID, from string, m links.Message) {
	if !s.Push.Enabled() {
		return
	}
	body := whatsapp.SafeText(linkMessageLine(lang.EN, m, false))
	if m.Kind == "card" {
		body = "Sent you a document" // a document says only what it is
	}
	var targets []push.Target
	var unread int
	var icon string
	if err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{to}, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT DISTINCT user_id FROM memberships WHERE role = ANY ($1)`, holdingAll(PermChatTrade))
		if err != nil {
			return err
		}
		users, err := pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
		if err != nil {
			return err
		}
		if targets, err = push.TargetsTx(ctx, tx, to, users); err != nil || len(targets) == 0 {
			return err
		}
		// The business as the chat list shows it: the name and logo it chose.
		if shown, err := links.ShownTx(ctx, tx); err == nil {
			if sh, ok := shown[fromTenant]; ok {
				if sh.Name != "" {
					from = sh.Name
				}
				if sh.HasLogo {
					icon = "/v1/links/" + fromTenant.String() + "/logo?size=320"
				}
			}
		}
		return tx.QueryRow(ctx, `SELECT count(DISTINCT link_id)::int FROM link_messages WHERE tenant_id = $1 AND NOT mine AND read_at IS NULL AND hidden_at IS NULL`, to).Scan(&unread)
	}); err != nil {
		s.Logger.Warn("push: finding the browsers", "tenant", to, "err", err)
		return
	}
	if len(targets) == 0 {
		return
	}
	payload := push.Payload{Title: from, Body: body, Link: "/chat?with=" + link.String(), Tag: link.String(), Unread: unread, Icon: icon}
	type result struct {
		id  uuid.UUID
		out push.Outcome
	}
	results := make([]result, len(targets))
	var wg sync.WaitGroup
	gate := make(chan struct{}, pushMaxParallel)
	for i, t := range targets {
		wg.Add(1)
		gate <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-gate }()
			start := time.Now()
			out, status, err := s.Push.Send(ctx, to, t, payload)
			telemetry.PushSeconds.Observe(time.Since(start).Seconds())
			telemetry.PushSent.WithLabelValues([]string{"sent", "gone", "failed"}[out]).Inc()
			if err != nil {
				// No address, no keys, no words: the status and what kind of error.
				s.Logger.Warn("push: not sent", "tenant", to, "device", t.ID, "status", status, "gone", out == push.Gone)
			}
			results[i] = result{t.ID, out}
		}()
	}
	wg.Wait()
	if err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{to}, func(tx pgx.Tx) error {
		for _, r := range results {
			if err := push.RecordTx(ctx, tx, to, r.id, r.out); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		s.Logger.Warn("push: recording what was sent", "tenant", to, "err", err)
	}
}
