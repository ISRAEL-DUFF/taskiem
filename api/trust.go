package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/media"
	"github.com/israel-duff/taskiem/engine/ops"
	"github.com/israel-duff/taskiem/engine/storefront"
	"github.com/israel-duff/taskiem/engine/whatsapp"
)

// The review queue, reports and suspension (storefronts spec 7.1, 7.4;
// docs/moderation.md): what owners see and ask for, what operators
// decide, and the scheduler's work (pages reported by several people
// suspended pending review; orders paused by the plan's month or the
// unverified daily limit).

const (
	// reportsToSuspend different people's open reports suspend a page.
	reportsToSuspend = 3
	// unverifiedDailyOrders is how many orders and bookings a day a
	// business not yet verified takes (spec 7.1).
	unverifiedDailyOrders = 50
)

// --- the owner's side ---

type reviewOut struct {
	ID           uuid.UUID       `json:"id"`
	Kind         string          `json:"kind"`
	Status       string          `json:"status"`
	Findings     json.RawMessage `json:"findings"`
	DecisionNote *string         `json:"decision_note"`
	CreatedAt    time.Time       `json:"created_at"`
	DecidedAt    *time.Time      `json:"decided_at"`
}

// getStorefrontTrust answers what the business has proved, what waits for
// Taskiem, and why the page is held, suspended or paused.
func (s *Server) getStorefrontTrust(w http.ResponseWriter, r *http.Request) {
	tenant := principalFrom(r.Context()).TenantID
	var out map[string]any
	err := s.tx(r, func(tx pgx.Tx) error {
		sf, err := storefront.Get(r.Context(), tx, tenant)
		if err != nil {
			return storefrontErr(err)
		}
		v, err := verificationOf(r.Context(), tx, tenant)
		if err != nil {
			return err
		}
		rows, err := tx.Query(r.Context(), `SELECT id, kind, status, findings, decision_note, created_at, decided_at FROM storefront_reviews
			WHERE tenant_id = $1 AND kind <> 'reports' ORDER BY created_at DESC LIMIT 20`, tenant)
		if err != nil {
			return err
		}
		reviews, err := pgx.CollectRows(rows, pgx.RowToStructByPos[reviewOut])
		if err != nil {
			return err
		}
		out = map[string]any{"status": sf.Status, "verification": v, "reviews": nonNil(reviews), "review_note": sf.ReviewNote,
			"suspended_reason": sf.SuspendedReason, "orders_paused": sf.OrdersPaused}
		return nil
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// requestBusinessVerification asks Taskiem to check the business: its
// legal name and address, and a CAC number if it has one. An operator
// decides.
func (s *Server) requestBusinessVerification(w http.ResponseWriter, r *http.Request) {
	var req struct {
		LegalName string `json:"legal_name"`
		Address   string `json:"address"`
		CAC       string `json:"cac_number"`
		Note      string `json:"note"`
	}
	if err := decodeBody(r, &req); err != nil {
		s.fail(w, r, err)
		return
	}
	req.LegalName, req.Address, req.CAC, req.Note = strings.TrimSpace(req.LegalName), strings.TrimSpace(req.Address), strings.TrimSpace(req.CAC), strings.TrimSpace(req.Note)
	if req.LegalName == "" || req.Address == "" || utf8.RuneCountInString(req.LegalName) > 200 || utf8.RuneCountInString(req.Address) > 300 ||
		len(req.CAC) > 20 || utf8.RuneCountInString(req.Note) > 500 {
		s.fail(w, r, fmt.Errorf("%w: the business's legal name and address are needed (a CAC number and a note are optional)", errBadRequest))
		return
	}
	p := principalFrom(r.Context())
	err := s.tx(r, func(tx pgx.Tx) error {
		sf, err := storefront.Get(r.Context(), tx, p.TenantID)
		if err != nil {
			return storefrontErr(err)
		}
		if v, err := verificationOf(r.Context(), tx, p.TenantID); err != nil || v.Business {
			if err == nil {
				err = fmt.Errorf("%w: the business is verified already", errConflict)
			}
			return err
		}
		if err := queueReview(r.Context(), tx, p.TenantID, sf, "business", nil, map[string]any{"legal_name": req.LegalName, "address": req.Address,
			"cac_number": req.CAC, "note": req.Note}, p.Actor()); err != nil {
			return err
		}
		return auditTx(r, tx, "storefront.verification_request", sf.ID.String(), map[string]any{"cac": req.CAC != ""})
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"status": "pending"})
}

// appealStorefront asks for a suspension, or a turned-down first publish,
// to be looked at again.
func (s *Server) appealStorefront(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Message string `json:"message"`
	}
	if err := decodeBody(r, &req); err != nil {
		s.fail(w, r, err)
		return
	}
	p := principalFrom(r.Context())
	err := s.tx(r, func(tx pgx.Tx) error {
		return s.appeal(r.Context(), tx, p.TenantID, req.Message, p.Actor(), func(target string) error {
			return auditTx(r, tx, "storefront.appeal", target, nil)
		})
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"status": "pending"})
}

// appeal records an owner's appeal (from the app or chat).
func (s *Server) appeal(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, message, actor string, audit func(target string) error) error {
	message = strings.TrimSpace(message)
	if message == "" || utf8.RuneCountInString(message) > 1000 {
		return fmt.Errorf("%w: say why in up to 1000 characters", errBadRequest)
	}
	sf, err := storefront.Get(ctx, tx, tenant)
	if err != nil {
		return storefrontErr(err)
	}
	if sf.Status != "suspended" && sf.ReviewNote == nil {
		return fmt.Errorf("%w: there is nothing to appeal: the page is not suspended or turned down", errConflict)
	}
	if err := queueReview(ctx, tx, tenant, sf, "appeal", nil, map[string]any{"message": message, "page_status": sf.Status}, actor); err != nil {
		return err
	}
	return audit(sf.ID.String())
}

// --- telling the owner ---

// tellOwners sends the owners and admins of a business a short notice on
// WhatsApp and Telegram (inside the conversation window as text, outside
// as the alert template), linking to the storefront page in the app.
func (s *Server) tellOwners(ctx context.Context, tenant uuid.UUID, title, body string) {
	var name string
	var users []uuid.UUID
	err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `SELECT name FROM tenants WHERE id = $1`, tenant).Scan(&name); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `SELECT DISTINCT user_id FROM memberships WHERE tenant_id = $1 AND role IN ('owner', 'admin')`, tenant)
		if err != nil {
			return err
		}
		users, err = pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
		return err
	})
	if err != nil {
		s.Logger.Warn("trust: telling the owners", "tenant", tenant, "err", err)
		return
	}
	link := ""
	if s.PublicURL != "" {
		link = strings.TrimRight(s.PublicURL, "/") + "/storefront"
	}
	msg := whatsapp.AlertMessage(name, "storefront", title, body, link, nil)
	if s.WhatsApp != nil {
		rows, err := s.Store.Pool.Query(ctx, `SELECT number FROM taskiem_wa_numbers($1)`, users)
		if err == nil {
			numbers, _ := pgx.CollectRows(rows, pgx.RowTo[string])
			for _, n := range numbers {
				if _, err := s.WhatsApp.Send(ctx, n, msg); err != nil {
					s.Logger.Warn("trust: WhatsApp notice", "tenant", tenant, "err", err)
				}
			}
		}
	}
	if s.Telegram != nil {
		for _, u := range users {
			var id *int64
			if err := s.Store.Pool.QueryRow(ctx, `SELECT telegram_id FROM taskiem_tg_binding($1)`, u).Scan(&id); err != nil || id == nil {
				continue
			}
			if _, err := s.Telegram.Send(ctx, fmt.Sprint(*id), msg); err != nil {
				s.Logger.Warn("trust: Telegram notice", "tenant", tenant, "err", err)
			}
		}
	}
}

// --- the scheduler's work ---

// TrustTick suspends pages reported by several people pending review, and
// pauses or resumes orders against the plan and the unverified daily
// limit. RunStorefronts calls it every few minutes.
func (s *Server) TrustTick(ctx context.Context) error {
	rows, err := s.Store.Pool.Query(ctx, `SELECT tenant_id, storefront_id, reporters FROM taskiem_reported_pages($1)`, reportsToSuspend)
	if err != nil {
		return err
	}
	type reported struct {
		Tenant, Storefront uuid.UUID
		Reporters          int64
	}
	list, err := pgx.CollectRows(rows, pgx.RowToStructByPos[reported])
	if err != nil {
		return err
	}
	for _, p := range list {
		var suspended bool
		err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{p.Tenant}, func(tx pgx.Tx) error {
			sf, ok, err := storefront.Suspend(ctx, tx, p.Tenant, "Reported by several people; Taskiem is looking at it", "system:reports")
			if err != nil || !ok {
				return err
			}
			suspended = true
			if err := queueReview(ctx, tx, p.Tenant, sf, "reports", nil, map[string]any{"reporters": p.Reporters}, "system:reports"); err != nil {
				return err
			}
			_, err = tx.Exec(ctx, `SELECT taskiem_audit_append($1, 'system', 'system:reports', 'storefront.suspend', $2, $3)`, p.Tenant, sf.ID.String(),
				fmt.Sprintf(`{"reason": "reports", "reporters": %d}`, p.Reporters))
			return err
		})
		if err != nil {
			s.Logger.Error("trust: suspending a reported page", "tenant", p.Tenant, "err", err)
			continue
		}
		if suspended {
			s.tellOwners(ctx, p.Tenant, "Your page is suspended while Taskiem checks it",
				"Several people reported your page, so it is hidden until Taskiem looks at it, usually within a working day. To explain, reply APPEAL and your message.")
		}
	}
	return s.pauseOrders(ctx)
}

// pauseOrders sets or lifts each live page's orders_paused.
func (s *Server) pauseOrders(ctx context.Context) error {
	rows, err := s.Store.Pool.Query(ctx, `SELECT tenant_id, storefront_id, orders_paused FROM taskiem_live_storefronts()`)
	if err != nil {
		return err
	}
	type live struct {
		Tenant, Storefront uuid.UUID
		Paused             *string
	}
	list, err := pgx.CollectRows(rows, pgx.RowToStructByPos[live])
	if err != nil {
		return err
	}
	for _, p := range list {
		lim, err := s.Store.LimitsFor(ctx, p.Tenant)
		if err != nil {
			return err
		}
		var why string
		var changed bool
		err = db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{p.Tenant}, func(tx pgx.Tx) error {
			var month, monthBookings, today int64
			if err := tx.QueryRow(ctx, `SELECT
				count(*) FILTER (WHERE kind = 'order' AND created_at >= date_trunc('month', now() AT TIME ZONE 'UTC') AT TIME ZONE 'UTC'),
				count(*) FILTER (WHERE kind = 'booking' AND created_at >= date_trunc('month', now() AT TIME ZONE 'UTC') AT TIME ZONE 'UTC'),
				count(*) FILTER (WHERE kind IN ('order', 'booking') AND created_at >= date_trunc('day', now() AT TIME ZONE 'Africa/Lagos') AT TIME ZONE 'Africa/Lagos')
				FROM storefront_orders WHERE tenant_id = $1 AND created_at >= now() - interval '32 days'`, p.Tenant).Scan(&month, &monthBookings, &today); err != nil {
				return err
			}
			v, err := verificationOf(ctx, tx, p.Tenant)
			if err != nil {
				return err
			}
			switch {
			case lim.StorefrontOrdersMonthly > 0 && month >= lim.StorefrontOrdersMonthly:
				why = fmt.Sprintf("this month's %d orders, the plan's limit", lim.StorefrontOrdersMonthly)
			case lim.StorefrontBookingsMonthly > 0 && monthBookings >= lim.StorefrontBookingsMonthly:
				why = fmt.Sprintf("this month's %d bookings, the plan's limit", lim.StorefrontBookingsMonthly)
			case !v.Business && today >= unverifiedDailyOrders:
				why = fmt.Sprintf("today's %d orders and bookings, the limit until the business is verified", unverifiedDailyOrders)
			}
			changed, err = storefront.SetOrdersPaused(ctx, tx, p.Tenant, why)
			return err
		})
		if err != nil {
			return err
		}
		if changed && why != "" {
			s.tellOwners(ctx, p.Tenant, "Orders on your page are paused",
				"Your page has taken "+why+", so its order and booking forms are paused. Change plan, or ask Taskiem to verify your business, to take more.")
		}
	}
	return nil
}

// --- operators ---

type opsReview struct {
	ID           uuid.UUID       `json:"id"`
	TenantID     uuid.UUID       `json:"tenant_id"`
	Business     string          `json:"business"`
	StorefrontID uuid.UUID       `json:"storefront_id"`
	Slug         string          `json:"slug"`
	PageStatus   string          `json:"page_status"`
	Kind         string          `json:"kind"`
	Version      *int            `json:"version"`
	Status       string          `json:"status"`
	Findings     json.RawMessage `json:"findings"`
	Detail       json.RawMessage `json:"detail"`
	CreatedBy    string          `json:"created_by"`
	CreatedAt    time.Time       `json:"created_at"`
	DecidedBy    *string         `json:"decided_by"`
	DecisionNote *string         `json:"decision_note"`
	DecidedAt    *time.Time      `json:"decided_at"`
	Verified     bool            `json:"verified"`
	AgentCode    *string         `json:"agent_code"`
	OpenReports  int64           `json:"open_reports"`
}

func (s *Server) opsReviews(w http.ResponseWriter, r *http.Request) {
	status := r.URL.Query().Get("status")
	if status == "" {
		status = "pending"
	}
	rows, err := s.Store.Pool.Query(r.Context(), `SELECT * FROM taskiem_ops_storefront_reviews($1)`, status)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	out, err := pgx.CollectRows(rows, pgx.RowToStructByPos[opsReview])
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"reviews": nonNil(out)})
}

func (s *Server) opsReviewOf(r *http.Request, id uuid.UUID) (opsReview, error) {
	rows, err := s.Store.Pool.Query(r.Context(), `SELECT * FROM taskiem_ops_storefront_reviews('all') WHERE id = $1`, id)
	if err != nil {
		return opsReview{}, err
	}
	return pgx.CollectExactlyOneRow(rows, pgx.RowToStructByPos[opsReview])
}

// opsStorefrontReview shows a review with what the reviewer needs: the page's
// version (or draft), its catalogue, its photos and open reports.
func (s *Server) opsStorefrontReview(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeErr(w, http.StatusNotFound, "no such review")
		return
	}
	rev, err := s.opsReviewOf(r, id)
	if err != nil {
		s.opsDBFail(w, r, err)
		return
	}
	out := map[string]any{"review": rev}
	err = s.opsTx(r, func(tx pgx.Tx, widen func(uuid.UUID) error) error {
		if err := widen(rev.TenantID); err != nil {
			return err
		}
		ctx := r.Context()
		sf, err := storefront.Get(ctx, tx, rev.TenantID)
		if err != nil {
			return err
		}
		out["storefront"] = sf
		if sf.PublishedVersion > 0 {
			var model json.RawMessage
			if err := tx.QueryRow(ctx, `SELECT model FROM storefront_snapshots WHERE storefront_id = $1 AND version = $2`, sf.ID, sf.PublishedVersion).Scan(&model); err != nil {
				return err
			}
			out["published"] = model
		}
		items, err := storefront.Catalogue(ctx, tx, rev.TenantID)
		if err != nil {
			return err
		}
		out["items"] = nonNil(items)
		rows, err := tx.Query(ctx, `SELECT id, urls, moderation FROM storefront_media WHERE tenant_id = $1 ORDER BY created_at DESC LIMIT 100`, rev.TenantID)
		if err != nil {
			return err
		}
		type photo struct {
			ID         uuid.UUID       `json:"id"`
			URLs       json.RawMessage `json:"urls"`
			Moderation string          `json:"moderation"`
		}
		photos, err := pgx.CollectRows(rows, pgx.RowToStructByPos[photo])
		if err != nil {
			return err
		}
		out["photos"] = nonNil(photos)
		rows, err = tx.Query(ctx, `SELECT id, reason, message, contact IS NOT NULL, status, created_at FROM page_reports WHERE site_id = $1 ORDER BY created_at DESC LIMIT 50`, sf.ID)
		if err != nil {
			return err
		}
		type report struct {
			ID         uuid.UUID `json:"id"`
			Reason     string    `json:"reason"`
			Message    string    `json:"message"`
			HasContact bool      `json:"has_contact"`
			Status     string    `json:"status"`
			CreatedAt  time.Time `json:"created_at"`
		}
		reports, err := pgx.CollectRows(rows, pgx.RowToStructByPos[report])
		if err != nil {
			return err
		}
		out["reports"] = nonNil(reports)
		v, err := verificationOf(ctx, tx, rev.TenantID)
		out["verification"] = v
		return err
	})
	if err != nil {
		s.opsDBFail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// opsDecideReview approves or rejects a review. What that does depends on
// its kind (docs/moderation.md#the-review-queue).
func (s *Server) opsDecideReview(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	decision := chi.URLParam(r, "decision")
	if err != nil || (decision != "approve" && decision != "reject") {
		writeErr(w, http.StatusNotFound, "the decision is approve or reject")
		return
	}
	var req struct {
		Note string `json:"note"`
		// RejectPhotos are photos not to show, whatever the decision.
		RejectPhotos []uuid.UUID `json:"reject_photos"`
		StepUp       *opsStepUp  `json:"step_up,omitempty"`
	}
	if err := decodeBody(r, &req); err != nil {
		s.fail(w, r, err)
		return
	}
	req.Note = strings.TrimSpace(req.Note)
	if decision == "reject" && req.Note == "" {
		writeErr(w, http.StatusUnprocessableEntity, "say why in a note: the owner is told")
		return
	}
	if utf8.RuneCountInString(req.Note) > 1000 {
		writeErr(w, http.StatusUnprocessableEntity, "the note is at most 1000 characters")
		return
	}
	rev, err := s.opsReviewOf(r, id)
	if err != nil {
		s.opsDBFail(w, r, err)
		return
	}
	if rev.Status != "pending" {
		writeErr(w, http.StatusConflict, "this review is decided already")
		return
	}
	if !s.checkOpsStepUp(w, r, req.StepUp, "ops.storefront.review", id.String()) {
		return
	}
	o := operatorFrom(r.Context())
	var tell [2]string // title, body for the owner
	err = s.opsTx(r, func(tx pgx.Tx, widen func(uuid.UUID) error) error {
		ctx := r.Context()
		if err := widen(rev.TenantID); err != nil {
			return err
		}
		status := map[string]string{"approve": "approved", "reject": "rejected"}[decision]
		tag, err := tx.Exec(ctx, `UPDATE storefront_reviews SET status = $2, decided_by = $3, decision_note = NULLIF($4, ''), decided_at = now()
			WHERE id = $1 AND status = 'pending'`, id, status, o.Actor(), req.Note)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return fmt.Errorf("%w: this review is decided already", errConflict)
		}
		if len(req.RejectPhotos) > 0 {
			if _, err := tx.Exec(ctx, `UPDATE storefront_media SET moderation = 'rejected', moderation_reason = 'a reviewer' WHERE tenant_id = $1 AND id = ANY ($2)`,
				rev.TenantID, req.RejectPhotos); err != nil {
				return err
			}
		}
		approve := decision == "approve"
		switch {
		case rev.Kind == "first_publish" && approve:
			if _, err := storefront.Approve(ctx, tx, rev.TenantID); err != nil {
				return err
			}
			tell = [2]string{"Your page is live", "Taskiem checked your page and it is now live. Share its link and QR code with your customers."}
		case rev.Kind == "first_publish":
			if _, err := storefront.Reject(ctx, tx, rev.TenantID, req.Note); err != nil {
				return err
			}
			tell = [2]string{"Your page needs changes", "Taskiem could not put your page live: " + req.Note + " Change it and publish again, or reply APPEAL and your message."}
		case (rev.Kind == "reports" || rev.Kind == "appeal") && approve:
			if rev.PageStatus == "suspended" {
				if _, err := storefront.Restore(ctx, tx, rev.TenantID); err != nil {
					return err
				}
				if _, err := tx.Exec(ctx, `UPDATE page_reports SET status = 'dismissed', handled_by = $2, handled_at = now() WHERE site_id = $1 AND status = 'open'`,
					rev.StorefrontID, o.Actor()); err != nil {
					return err
				}
				tell = [2]string{"Your page is back", "Taskiem checked your page and it is live again."}
			}
		case rev.Kind == "reports":
			if _, err := tx.Exec(ctx, `UPDATE page_reports SET status = 'actioned', handled_by = $2, handled_at = now() WHERE site_id = $1 AND status = 'open'`,
				rev.StorefrontID, o.Actor()); err != nil {
				return err
			}
			tell = [2]string{"Your page stays suspended", "Taskiem checked the reports about your page: " + req.Note + " Reply APPEAL and your message to explain."}
		case rev.Kind == "appeal":
			tell = [2]string{"Your appeal was turned down", req.Note}
		case rev.Kind == "business" && approve:
			detail, _ := json.Marshal(map[string]any{"review": id, "by": o.Actor()})
			if _, err := tx.Exec(ctx, `INSERT INTO tenant_verifications (id, tenant_id, level, method, actor, detail) VALUES ($1, $2, 'business', 'operator_check', $3, $4)`,
				uuid.Must(uuid.NewV7()), rev.TenantID, o.Actor(), detail); err != nil {
				return err
			}
			tell = [2]string{"Your business is verified", "Taskiem checked your business. Your page shows a verified badge, and you can take payment when customers order."}
		case rev.Kind == "business":
			tell = [2]string{"Your business could not be verified", req.Note}
		case (rev.Kind == "edit" || rev.Kind == "catalogue") && !approve:
			// What was flagged after going live breaks the rules: the page
			// comes down until it is fixed and an appeal is accepted.
			if _, _, err := storefront.Suspend(ctx, tx, rev.TenantID, req.Note, o.Actor()); err != nil {
				return err
			}
			tell = [2]string{"Your page is suspended", req.Note + " Change it, then reply APPEAL and your message."}
		}
		if approve && (rev.Kind == "first_publish" || rev.Kind == "edit" || rev.Kind == "catalogue") {
			if _, err := tx.Exec(ctx, `UPDATE storefront_media SET moderation = 'approved' WHERE tenant_id = $1 AND moderation = 'unreviewed'`, rev.TenantID); err != nil {
				return err
			}
		}
		detail := map[string]any{"kind": rev.Kind, "note": req.Note, "rejected_photos": len(req.RejectPhotos)}
		if err := tenantAudit(r, tx, rev.TenantID, o.Actor(), "storefront.review_"+status, id.String(), detail); err != nil {
			return err
		}
		return ops.AuditTx(ctx, tx, o.Actor(), "ops.storefront.review_"+status, id.String(), map[string]any{"tenant": rev.TenantID, "kind": rev.Kind})
	})
	if err != nil {
		if errors.Is(err, storefront.ErrNotInReview) || errors.Is(err, storefront.ErrNotPublished) {
			writeErr(w, http.StatusConflict, plainError(err))
			return
		}
		s.opsDBFail(w, r, err)
		return
	}
	if tell[0] != "" {
		s.tellOwners(r.Context(), rev.TenantID, tell[0], tell[1])
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": id, "status": map[string]string{"approve": "approved", "reject": "rejected"}[decision]})
}

// opsReviewImage serves one of the reviewed business's stored photos to
// the console, whose CSP allows images from its own origin only. Only that
// business's own storage is fetched (media.FetchPublic).
func (s *Server) opsReviewImage(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil || s.Media == nil {
		writeErr(w, http.StatusNotFound, "no such review")
		return
	}
	rev, err := s.opsReviewOf(r, id)
	if err != nil {
		s.opsDBFail(w, r, err)
		return
	}
	data, ctype, err := s.Media.FetchPublic(r.Context(), rev.TenantID, r.URL.Query().Get("src"))
	if errors.Is(err, media.ErrNotOurs) {
		writeErr(w, http.StatusBadRequest, plainError(err))
		return
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	w.Header().Set("Content-Type", ctype)
	w.Header().Set("Cache-Control", "private, max-age=3600")
	_, _ = w.Write(data) //nolint:gosec // a JPEG or PNG by its bytes (FetchPublic), sent as that type with nosniff
}

func (s *Server) opsReports(w http.ResponseWriter, r *http.Request) {
	status := r.URL.Query().Get("status")
	if status == "" {
		status = "open"
	}
	type report struct {
		ID            uuid.UUID  `json:"id"`
		TenantID      uuid.UUID  `json:"tenant_id"`
		Business      string     `json:"business"`
		Kind          string     `json:"kind"`
		SiteID        uuid.UUID  `json:"site_id"`
		Slug          *string    `json:"slug"`
		Reason        string     `json:"reason"`
		Message       string     `json:"message"`
		HasContact    bool       `json:"has_contact"`
		Status        string     `json:"status"`
		CreatedAt     time.Time  `json:"created_at"`
		HandledBy     *string    `json:"handled_by"`
		HandledAt     *time.Time `json:"handled_at"`
		ReportsOnSite int64      `json:"reporters_on_site"`
		AgentCode     *string    `json:"agent_code"`
	}
	rows, err := s.Store.Pool.Query(r.Context(), `SELECT * FROM taskiem_ops_page_reports($1)`, status)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	out, err := pgx.CollectRows(rows, pgx.RowToStructByPos[report])
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"reports": nonNil(out)})
}

// opsReportAction dismisses a report, marks it acted on, or reveals the
// reporter's contact (to ask them more).
func (s *Server) opsReportAction(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	action := chi.URLParam(r, "action")
	status := map[string]string{"dismiss": "dismissed", "action": "actioned"}[action]
	if err != nil || (status == "" && action != "contact") {
		writeErr(w, http.StatusNotFound, "the action is dismiss, action or contact")
		return
	}
	var req struct {
		StepUp *opsStepUp `json:"step_up,omitempty"`
	}
	if err := decodeBody(r, &req); err != nil {
		s.fail(w, r, err)
		return
	}
	if !s.checkOpsStepUp(w, r, req.StepUp, "ops.report."+action, id.String()) {
		return
	}
	var tenant uuid.UUID
	if err := s.Store.Pool.QueryRow(r.Context(), `SELECT tenant_id FROM taskiem_ops_page_reports('all') WHERE id = $1`, id).Scan(&tenant); err != nil {
		s.opsDBFail(w, r, err)
		return
	}
	o := operatorFrom(r.Context())
	out := map[string]any{"id": id}
	err = s.opsTx(r, func(tx pgx.Tx, widen func(uuid.UUID) error) error {
		ctx := r.Context()
		if err := widen(tenant); err != nil {
			return err
		}
		if action == "contact" {
			var sealed []byte
			if err := tx.QueryRow(ctx, `SELECT contact FROM page_reports WHERE id = $1`, id).Scan(&sealed); err != nil {
				return err
			}
			if sealed == nil || s.Forms == nil {
				return fmt.Errorf("%w: the reporter left no contact", errConflict)
			}
			var contact string
			if err := s.Forms.Keys.Open("report contact", sealed, &contact); err != nil {
				return err
			}
			out["contact"] = contact
		} else {
			if _, err := tx.Exec(ctx, `UPDATE page_reports SET status = $2, handled_by = $3, handled_at = now() WHERE id = $1`, id, status, o.Actor()); err != nil {
				return err
			}
			out["status"] = status
		}
		if err := tenantAudit(r, tx, tenant, o.Actor(), "page_report."+action, id.String(), nil); err != nil {
			return err
		}
		return ops.AuditTx(ctx, tx, o.Actor(), "ops.report."+action, id.String(), map[string]any{"tenant": tenant})
	})
	if err != nil {
		s.opsDBFail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// opsStorefrontAction suspends a business's page, or restores it.
func (s *Server) opsStorefrontAction(w http.ResponseWriter, r *http.Request) {
	tenant, err := uuid.Parse(chi.URLParam(r, "tenant"))
	action := chi.URLParam(r, "action")
	if err != nil || (action != "suspend" && action != "restore") {
		writeErr(w, http.StatusNotFound, "the action is suspend or restore")
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
	if action == "suspend" && (req.Reason == "" || utf8.RuneCountInString(req.Reason) > 500) {
		writeErr(w, http.StatusUnprocessableEntity, "a suspension needs a reason (up to 500 characters): the owner is told")
		return
	}
	if !s.checkOpsStepUp(w, r, req.StepUp, "ops.storefront."+action, tenant.String()) {
		return
	}
	o := operatorFrom(r.Context())
	var sf storefront.Storefront
	err = s.opsTx(r, func(tx pgx.Tx, widen func(uuid.UUID) error) error {
		ctx := r.Context()
		if err := widen(tenant); err != nil {
			return err
		}
		if action == "suspend" {
			var ok bool
			if sf, ok, err = storefront.Suspend(ctx, tx, tenant, req.Reason, o.Actor()); err != nil {
				return err
			} else if !ok {
				return fmt.Errorf("%w: only a live page can be suspended", errConflict)
			}
		} else if sf, err = storefront.Restore(ctx, tx, tenant); err != nil {
			return err
		}
		if err := tenantAudit(r, tx, tenant, o.Actor(), "storefront."+action, sf.ID.String(), map[string]any{"reason": req.Reason}); err != nil {
			return err
		}
		return ops.AuditTx(ctx, tx, o.Actor(), "ops.storefront."+action, tenant.String(), map[string]any{"reason": req.Reason})
	})
	if errors.Is(err, storefront.ErrNotPublished) || errors.Is(err, storefront.ErrNotFound) {
		writeErr(w, http.StatusConflict, "the page is not suspended")
		return
	}
	if err != nil {
		if errors.Is(err, errConflict) {
			writeErr(w, http.StatusConflict, strings.TrimPrefix(err.Error(), "conflict: "))
			return
		}
		s.opsDBFail(w, r, err)
		return
	}
	if action == "suspend" {
		s.tellOwners(r.Context(), tenant, "Your page is suspended", req.Reason+" To explain, reply APPEAL and your message.")
	} else {
		s.tellOwners(r.Context(), tenant, "Your page is back", "Taskiem restored your page.")
	}
	writeJSON(w, http.StatusOK, map[string]any{"tenant_id": tenant, "status": sf.Status})
}
