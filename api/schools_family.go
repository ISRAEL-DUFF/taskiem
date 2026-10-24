package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/matching"
	"github.com/israel-duff/taskiem/engine/reach"
	"github.com/israel-duff/taskiem/engine/schools"
	"github.com/israel-duff/taskiem/engine/sender"
	"github.com/israel-duff/taskiem/engine/telegram"
	"github.com/israel-duff/taskiem/engine/whatsapp"
)

// The family page's staff side (docs/spec/schools.md 10 and 11.3, B6;
// docs/schools.md#the-family-page): a family's "I have paid" claims
// (listed, beside the unmatched money in To match, and dismissed with a
// reason), payment plan requests (listed and decided), a link sent to a
// family by hand, the school's link life in Settings, and the chat
// outbox's family messages. What families send from the page arrives
// through the inbox the api role drains (schools.DrainFamilyInbox).

// schoolFamilyRoutes mounts the routes inside the Schools group.
func (s *Server) schoolFamilyRoutes(r chi.Router) {
	read, manage, approve := s.need(PermSchoolRead), s.need(PermSchoolManage), s.need(PermSchoolApprove)
	r.With(read).Get("/schools/claims", s.listSchoolClaims)
	r.With(manage, s.tenantWide).Post("/schools/claims/{id}/resolve", s.resolveSchoolClaim)
	r.With(read).Get("/schools/plans", s.listSchoolPlans)
	r.With(approve, s.tenantWide).Post("/schools/plans/{id}", s.decideSchoolPlan)
	r.With(manage, s.tenantWide).Post("/schools/families/{id}/link", s.sendSchoolFamilyLink)
}

func maskClaim(c *schools.Claim)             { c.Name = schoolName(c.Name) }
func maskPlanRequest(p *schools.PlanRequest) { p.Name = schoolName(p.Name) }

func optionalID(r *http.Request, name string) (*uuid.UUID, error) {
	v := r.URL.Query().Get(name)
	if v == "" {
		return nil, nil
	}
	id, err := uuid.Parse(v)
	if err != nil {
		return nil, fmt.Errorf("%w: %s is an id", errBadRequest, name)
	}
	return &id, nil
}

// listSchoolClaims serves GET /v1/schools/claims: by student, family or
// status (open by default), newest first.
func (s *Server) listSchoolClaims(w http.ResponseWriter, r *http.Request) {
	rev, ok := revealing(w, r)
	if !ok {
		return
	}
	f := schools.ClaimFilter{Status: r.URL.Query().Get("status")}
	switch f.Status {
	case "":
		f.Status = "open"
	case "any":
		f.Status = ""
	}
	var err error
	if f.Student, err = optionalID(r, "student"); err != nil {
		s.fail(w, r, err)
		return
	}
	if f.Family, err = optionalID(r, "family"); err != nil {
		s.fail(w, r, err)
		return
	}
	if v := r.URL.Query().Get("limit"); v != "" {
		f.Limit, _ = strconv.Atoi(v)
	}
	p := principalFrom(r.Context())
	var list []schools.Claim
	if err := s.tx(r, func(tx pgx.Tx) error {
		var err error
		if list, err = s.schoolsSvc().Claims(r.Context(), tx, p.TenantID, f); err != nil {
			return schoolErr(err)
		}
		if rev {
			return auditTx(r, tx, "pii.reveal", "school_claims", map[string]any{"claims": len(list)})
		}
		return nil
	}); err != nil {
		s.fail(w, r, err)
		return
	}
	if !rev {
		for i := range list {
			maskClaim(&list[i])
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"claims": list, "revealed": rev})
}

// resolveSchoolClaim serves POST /v1/schools/claims/{id}/resolve: the
// bursar dismisses an open claim with a reason.
func (s *Server) resolveSchoolClaim(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		s.fail(w, r, err)
		return
	}
	var req struct {
		Reason string `json:"reason"`
	}
	if err := decodeBody(r, &req); err != nil {
		s.fail(w, r, err)
		return
	}
	p := principalFrom(r.Context())
	var c schools.Claim
	if err := s.tx(r, func(tx pgx.Tx) error {
		var err error
		if c, err = s.schoolsSvc().ResolveClaim(r.Context(), tx, p.TenantID, id, req.Reason, p.Actor()); err != nil {
			return schoolErr(err)
		}
		return auditTx(r, tx, "school.claim.resolve", id.String(), map[string]any{"student": c.StudentID, "amount_kobo": c.AmountKobo, "reason": req.Reason})
	}); err != nil {
		s.fail(w, r, err)
		return
	}
	maskClaim(&c)
	writeJSON(w, http.StatusOK, map[string]any{"claim": c})
}

// listSchoolPlans serves GET /v1/schools/plans: plan requests by
// student, family or status (requested by default; any for all).
func (s *Server) listSchoolPlans(w http.ResponseWriter, r *http.Request) {
	rev, ok := revealing(w, r)
	if !ok {
		return
	}
	f := schools.PlanFilter{Status: r.URL.Query().Get("status")}
	switch f.Status {
	case "":
		f.Status = "requested"
	case "any":
		f.Status = ""
	}
	var err error
	if f.Student, err = optionalID(r, "student"); err != nil {
		s.fail(w, r, err)
		return
	}
	if f.Family, err = optionalID(r, "family"); err != nil {
		s.fail(w, r, err)
		return
	}
	if v := r.URL.Query().Get("limit"); v != "" {
		f.Limit, _ = strconv.Atoi(v)
	}
	p := principalFrom(r.Context())
	var list []schools.PlanRequest
	if err := s.tx(r, func(tx pgx.Tx) error {
		var err error
		if list, err = s.schoolsSvc().Plans(r.Context(), tx, p.TenantID, f); err != nil {
			return schoolErr(err)
		}
		if rev {
			return auditTx(r, tx, "pii.reveal", "school_plans", map[string]any{"plans": len(list)})
		}
		return nil
	}); err != nil {
		s.fail(w, r, err)
		return
	}
	if !rev {
		for i := range list {
			maskPlanRequest(&list[i])
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"plans": list, "revealed": rev})
}

// decideSchoolPlan serves POST /v1/schools/plans/{id}: a person with
// school.approve approves (with the instalments as decided) or declines
// with a note; the family is told with a fresh link.
func (s *Server) decideSchoolPlan(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r.Context())
	if p.UserID == uuid.Nil {
		writeErr(w, http.StatusForbidden, "plans are decided by people, not API keys")
		return
	}
	id, err := pathID(r, "id")
	if err != nil {
		s.fail(w, r, err)
		return
	}
	var req struct {
		Decision    string               `json:"decision"` // approved | declined
		Instalments []schools.Instalment `json:"instalments,omitempty"`
		Note        string               `json:"note,omitempty"`
	}
	if err := decodeBody(r, &req); err != nil {
		s.fail(w, r, err)
		return
	}
	if req.Decision != "approved" && req.Decision != "declined" {
		s.fail(w, r, fmt.Errorf("%w: decision is approved or declined", errBadRequest))
		return
	}
	user := p.Human()
	var d schools.PlanRequest
	if err := s.tx(r, func(tx pgx.Tx) error {
		var err error
		if d, err = s.schoolsSvc().DecidePlan(r.Context(), tx, p.TenantID, id, schools.PlanDecision{Approve: req.Decision == "approved", Instalments: req.Instalments, Note: req.Note}, p.Actor(), &user); err != nil {
			return schoolErr(err)
		}
		return auditTx(r, tx, "school.plan.decide", id.String(), map[string]any{"decision": req.Decision, "student": d.StudentID, "instalments": d.DecidedInstalments, "note": req.Note})
	}); err != nil {
		s.fail(w, r, err)
		return
	}
	sent, err := s.schoolsSvc().PlanDecidedAfter(r.Context(), p.TenantID, d)
	if err != nil {
		s.Logger.Warn("schools: telling the family of a plan decision", "plan", id, "err", err)
	}
	maskPlanRequest(&d)
	writeJSON(w, http.StatusOK, map[string]any{"plan": d, "sent": sent})
}

// sendSchoolFamilyLink serves POST /v1/schools/families/{id}/link: mints
// a fresh link to the family's page and sends it to its guardians on
// their chat app or by SMS. The link is answered too, for the bursar to
// pass on by hand (audited).
func (s *Server) sendSchoolFamilyLink(w http.ResponseWriter, r *http.Request) {
	if !s.invoicesOn(w, r) {
		return
	}
	id, err := pathID(r, "id")
	if err != nil {
		s.fail(w, r, err)
		return
	}
	var req struct {
		// Message, optional, goes before the link.
		Message string `json:"message,omitempty"`
	}
	if r.ContentLength != 0 {
		if err := decodeBody(r, &req); err != nil {
			s.fail(w, r, err)
			return
		}
	}
	if len(req.Message) > 300 {
		s.fail(w, r, fmt.Errorf("%w: message is up to 300 characters", errBadRequest))
		return
	}
	p := principalFrom(r.Context())
	var sent schools.FamilySent
	if err := s.tx(r, func(tx pgx.Tx) error {
		var business string
		if err := tx.QueryRow(r.Context(), `SELECT name FROM tenants WHERE id = $1`, p.TenantID).Scan(&business); err != nil {
			return err
		}
		text := func(link string) string {
			if req.Message != "" {
				return req.Message + " " + link
			}
			return business + ": your family's school fees page, with each child's fees, payments and account number: " + link
		}
		var err error
		if sent, err = s.schoolsSvc().SendFamily(r.Context(), tx, p.TenantID, id, schools.FamilyMessage{Purpose: "link", Key: "staff:" + id.String() + ":" + time.Now().UTC().Format(time.RFC3339Nano), Text: text}, p.Actor()); err != nil {
			return schoolErr(err)
		}
		return auditTx(r, tx, "school.family.link", id.String(), map[string]any{"channels": sent.Channels, "expires_at": sent.ExpiresAt})
	}); err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"link": sent.Link.URL, "expires_at": sent.ExpiresAt, "channels": sent.Channels, "nobody": sent.Nobody})
}

// schoolClaims is the matcher's claim source: the school's open claims
// beside the unmatched money.
func (s *Server) schoolClaims() matching.ClaimSource { return s.schoolsSvc() }

// --- the chat outbox: a message to a family's guardian ---

// sendSchoolFamilyItem sends a queued family message to the guardian's
// chat app; one that cannot go there goes by SMS to the guardian's phone
// instead, once.
func (s *Server) sendSchoolFamilyItem(ctx context.Context, it whatsapp.OutboxItem) error {
	if !s.Reach.Ready() {
		return fmt.Errorf("%w: updates are off", whatsapp.ErrNothingToSend)
	}
	if it.Run == nil || it.Contact == nil {
		return fmt.Errorf("%w: incomplete", whatsapp.ErrNothingToSend)
	}
	var business, text, short, phone string
	var ok bool
	if err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{it.Tenant}, func(tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `SELECT name FROM tenants WHERE id = $1`, it.Tenant).Scan(&business); err != nil {
			return err
		}
		var err error
		text, short, phone, ok, err = s.schoolsSvc().FamilyMessageText(ctx, tx, it.Tenant, *it.Run)
		return err
	}); err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("%w: the message or its guardian is gone", whatsapp.ErrNothingToSend)
	}
	fallback := func(why error) error {
		if s.Messages == nil || phone == "" {
			return fmt.Errorf("%w: %w", whatsapp.ErrNothingToSend, why)
		}
		err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{it.Tenant}, func(tx pgx.Tx) error {
			_, err := s.Messages.Enqueue(ctx, tx, sender.Message{Tenant: it.Tenant, Channel: sender.SMS, Purpose: sender.PurposeNotice, To: phone,
				SenderName: business, Text: short, Key: "family-fallback:" + it.ID.String()})
			return err
		})
		if err != nil && !errors.Is(err, sender.ErrAllowance) && !errors.Is(err, sender.ErrInvalid) && !errors.Is(err, sender.ErrRecipientLimit) {
			return err
		}
		return fmt.Errorf("%w: sent by SMS instead: %w", whatsapp.ErrNothingToSend, why)
	}
	ch, err := s.chatChannelFor(ctx, it.Channel, it.Tenant)
	if err != nil {
		return fallback(err)
	}
	var addr string
	if err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{it.Tenant}, func(tx pgx.Tx) error {
		addr, err = s.Reach.AddressTx(ctx, tx, it.Tenant, reach.Target{Channel: it.Channel, Contact: *it.Contact})
		return err
	}); err != nil {
		if errors.Is(err, reach.ErrGone) {
			return fallback(err)
		}
		return err
	}
	head := text
	if r := []rune(head); len(r) > 500 {
		head = string(r[:500]) + "…"
	}
	tpl := whatsapp.TplUpdate
	m := whatsapp.Message{Text: ch.Prefix(business) + text, Tenant: it.Tenant, Template: &tpl,
		Vars: map[string]string{"tenant": business, "about": "school fees", "message": head}}
	_, err = ch.Send(ctx, addr, m)
	var waErr *whatsapp.GraphError
	switch {
	case err == nil:
		return nil
	case errors.Is(err, telegram.ErrBlocked):
		if berr := s.Reach.Blocked(ctx, it.Channel, addr); berr != nil {
			return berr
		}
		return fallback(err)
	case errors.Is(err, whatsapp.ErrWindowClosed), errors.As(err, &waErr) && waErr.Code >= 132000 && waErr.Code <= 132016:
		return fallback(err)
	}
	return fmt.Errorf("to %s: %w", whatsapp.MaskNumber(addr), err)
}

// runSchoolFamilyInbox drains what families sent from their pages every
// two seconds (schools.DrainFamilyInbox); nothing without the inbox key.
func (s *Server) runSchoolFamilyInbox(ctx context.Context) {
	if len(s.SchoolInboxKey) == 0 {
		return
	}
	t := time.NewTicker(2 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if err := s.schoolsSvc().DrainFamilyInbox(ctx); err != nil && ctx.Err() == nil {
				s.Logger.Error("schools: family inbox", "err", err)
			}
		}
	}
}
