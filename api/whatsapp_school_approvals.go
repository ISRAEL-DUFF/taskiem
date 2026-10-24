package api

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/invoicing"
	"github.com/israel-duff/taskiem/engine/lang"
	"github.com/israel-duff/taskiem/engine/schools"
	"github.com/israel-duff/taskiem/engine/whatsapp"
)

// A school's requests for a second person over WhatsApp and Telegram
// (docs/spec/schools.md 8, B5): a cash posting, a discount, a discount
// amendment or a held term invoice at or above the school's thresholds
// is sent to each approver (school.approve) with a linked chat app, with
// Approve and Decline buttons, as a run's approval is
// (whatsapp_approvals.go): a signed single-use decision token bound to
// the request, the approver and the decision, recorded in whatsapp_tokens
// with the request's id where a run's id goes and the step "school". A
// tap is checked (the token, the sender) and decided by schools.Decide,
// the web app's path: the requester never decides their own, and a
// request is decided once. The requester is told in their notifications.

// outboxSchoolApproval is the outbox kind of a school request sent to an
// approver.
const outboxSchoolApproval = "school_approval"

// schoolStep is what whatsapp_tokens and whatsapp_notices carry as the
// step of a school request (there is no run).
const schoolStep = "school"

// waNotifySchoolApprovals queues each pending request (asked in the last
// week) to the approvers linked on each channel who have not had it: not
// the requester.
func (s *Server) waNotifySchoolApprovals(ctx context.Context, tenant uuid.UUID) error {
	var errs []error
	for _, channel := range s.chatChannelsOn() {
		if err := s.notifySchoolApprovalsOn(ctx, tenant, channel); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", channel, err))
		}
	}
	return errors.Join(errs...)
}

func (s *Server) notifySchoolApprovalsOn(ctx context.Context, tenant uuid.UUID, channel string) error {
	type target struct {
		request, user uuid.UUID
	}
	var targets []target
	err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		on, err := schools.Enabled(ctx, tx, tenant)
		if err != nil || !on {
			return err
		}
		approvers, err := schools.Approvers(ctx, tx, tenant)
		if err != nil || len(approvers) == 0 {
			return err
		}
		addrs, err := chatAddrsTx(ctx, tx, channel, approvers)
		if err != nil || len(addrs) == 0 {
			return err
		}
		linked := make([]uuid.UUID, 0, len(addrs))
		for u := range addrs {
			linked = append(linked, u)
		}
		rows, err := tx.Query(ctx, `SELECT p.id, u.user_id FROM school_pending_postings p CROSS JOIN unnest($2::uuid[]) AS u(user_id)
			WHERE p.tenant_id = $1 AND p.status = 'pending' AND p.requested_at > now() - interval '7 days'
			  AND (p.requested_user IS NULL OR p.requested_user <> u.user_id)
			  AND NOT EXISTS (SELECT 1 FROM whatsapp_notices n WHERE n.run_id = p.id AND n.step_id = $3 AND n.level = 0 AND n.user_id = u.user_id AND n.channel = $4)
			ORDER BY p.requested_at LIMIT 200`, tenant, linked, schoolStep, channel)
		if err != nil {
			return err
		}
		targets, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (target, error) {
			var t target
			return t, r.Scan(&t.request, &t.user)
		})
		return err
	})
	if err != nil {
		return err
	}
	var errs []error
	for _, t := range targets {
		err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
			tag, err := tx.Exec(ctx, `INSERT INTO whatsapp_notices (tenant_id, run_id, step_id, level, user_id, channel) VALUES ($1, $2, $3, 0, $4, $5) ON CONFLICT DO NOTHING`,
				tenant, t.request, schoolStep, t.user, channel)
			if err != nil || tag.RowsAffected() == 0 {
				return err
			}
			run, step, level := t.request, schoolStep, 0
			_, err = whatsapp.Enqueue(ctx, tx, whatsapp.OutboxItem{Tenant: tenant, Channel: channel, Kind: outboxSchoolApproval, User: t.user, Run: &run, Step: &step, Level: &level},
				outboxKey(channel, fmt.Sprintf("school_approval:%s:%s", t.request, t.user)))
			return err
		})
		if err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// sendSchoolApprovalItem sends a queued request with fresh decision
// tokens, if it is still pending and the person may still decide it.
func (s *Server) sendSchoolApprovalItem(ctx context.Context, it whatsapp.OutboxItem) error {
	if it.Run == nil {
		return fmt.Errorf("%w: incomplete", whatsapp.ErrNothingToSend)
	}
	p, _ := s.principalOf(ctx, it.Tenant, it.User)
	if p == nil || !p.Can(PermSchoolApprove) {
		return fmt.Errorf("%w: the person no longer approves for the school", whatsapp.ErrNothingToSend)
	}
	wa, addr, err := s.chatAddrOf(ctx, it.Channel, it.Tenant, it.User)
	if err != nil {
		return err
	}
	var name string
	var req schools.Posting
	err = db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{it.Tenant}, func(tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `SELECT name FROM tenants WHERE id = $1`, it.Tenant).Scan(&name); err != nil {
			return err
		}
		req, err = s.schoolsSvc().Posting(ctx, tx, it.Tenant, *it.Run)
		return err
	})
	if errors.Is(err, schools.ErrNotFound) || err == nil && req.Status != schools.StatePending {
		return fmt.Errorf("%w: %w", whatsapp.ErrNothingToSend, errApprovalClosed)
	}
	if err != nil {
		return err
	}
	if req.RequestedUser != nil && *req.RequestedUser == it.User {
		return fmt.Errorf("%w: the requester does not decide their own request", whatsapp.ErrNothingToSend)
	}
	l := s.chooseLang(ctx, it.Tenant, addr, "").Tag
	exp := time.Unix(time.Now().Add(waDecisionTTL).Unix(), 0)
	notice := uuid.New()
	tokens := map[string]string{}
	err = db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{it.Tenant}, func(tx pgx.Tx) error {
		for _, d := range []string{"approved", "rejected"} {
			c := whatsapp.Claims{Tenant: it.Tenant, Nonce: whatsapp.NewNonce(), Purpose: whatsapp.PurposeDecide, Decision: d, Expires: exp,
				Run: req.ID, Step: schoolStep, Level: 0, User: it.User}
			if _, err := tx.Exec(ctx, `INSERT INTO whatsapp_tokens (nonce, tenant_id, purpose, run_id, step_id, level, user_id, decision, notice, expires_at, channel)
				VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)`, c.Nonce[:], it.Tenant, c.Purpose, req.ID, schoolStep, 0, it.User, d, notice, exp, it.Channel); err != nil {
				return err
			}
			tokens[d] = s.chatSigner().Sign(c)
		}
		return nil
	})
	if err != nil {
		return err
	}
	title := whatsapp.SafeText(tr(l, "wa.school.kind."+req.Kind) + " · " + invoicing.Naira(req.AmountKobo))
	summary := whatsapp.SafeText(schoolRequestLine(l, req))
	text := wa.Prefix(name) + tr(l, "wa.approval.needed", "title", title, "where", "Schools") + "\n\n" + summary +
		"\n\n" + tr(l, "wa.approval.expires", "time", exp.UTC().Format("2 Jan 15:04"))
	tpl := whatsapp.TplApprovalRequest
	msg := whatsapp.Message{Text: text, Buttons: []whatsapp.Button{{ID: tokens["approved"], Title: tr(l, "button.approve")}, {ID: tokens["rejected"], Title: tr(l, "button.reject")}},
		Template: &tpl, Vars: map[string]string{"tenant": name, "title": title, "summary": summary, "environment": "Schools"},
		Payloads: []string{tokens["approved"], tokens["rejected"]}, Tenant: it.Tenant}
	if _, err := wa.Send(ctx, addr, msg); err != nil {
		return fmt.Errorf("to %s: %w", whatsapp.MaskNumber(addr), err)
	}
	return nil
}

// schoolRequestLine says what a request asks for, in the approver's
// language: the student by admission number and class, never a name.
func schoolRequestLine(l lang.Tag, req schools.Posting) string {
	by := req.RequestedBy
	if req.RequestedEmail != "" {
		by = req.RequestedEmail
	}
	if i := strings.IndexByte(by, '@'); i > 0 {
		by = by[:i] + "@…"
	}
	amount := invoicing.Naira(req.AmountKobo)
	switch req.Kind {
	case schools.KindCashPosting:
		how, _ := req.Details["how"].(string)
		return tr(l, "wa.school.cash_posting", "amount", amount, "how", tr(l, "wa.school.how."+how), "student", req.AdmissionNumber, "class", req.Class, "by", by)
	case schools.KindDiscount:
		kind, _ := req.Details["kind"].(string)
		reason, _ := req.Details["reason"].(string)
		return tr(l, "wa.school.discount", "kind", strings.ReplaceAll(kind, "_", " "), "amount", amount, "student", req.AdmissionNumber, "class", req.Class, "reason", reason, "by", by)
	case schools.KindDiscountAmendment:
		reason, _ := req.Details["reason"].(string)
		return tr(l, "wa.school.discount_amendment", "amount", amount, "number", req.InvoiceNumber, "reason", reason, "by", by)
	}
	return tr(l, "wa.school.term_invoice", "student", req.AdmissionNumber, "class", req.Class, "amount", amount, "by", by)
}

// waSchoolDecide decides a school request from a tapped button, once the
// token was checked and spent (waDecide).
func (s *Server) waSchoolDecide(ctx context.Context, c *chat, tenant tenantRef, p *Principal, cl whatsapp.Claims) error {
	say := func(text string) error { return s.waSay(ctx, c.ch, c.addr, tenant.Name, text) }
	if !p.Can(PermSchoolApprove) {
		return say(c.t("wa.approval.denied", "org", tenant.Name))
	}
	approve := cl.Decision == "approved"
	reason := ""
	if !approve {
		reason = "Declined from " + c.ch.Name()
	}
	var d schools.Decision
	err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{tenant.ID}, func(tx pgx.Tx) error {
		var err error
		if d, err = s.schoolsSvc().Decide(ctx, tx, tenant.ID, cl.Run, approve, reason, p.Actor()); err != nil {
			return err
		}
		return waAudit(ctx, tx, c.ch.Name(), p, "school.approval.decide", cl.Run.String(), map[string]any{"decision": map[bool]string{true: "approved", false: "declined"}[approve],
			"kind": d.Request.Kind, "amount_kobo": d.Request.AmountKobo, "requested_by": d.Request.RequestedBy, "via": "button"})
	})
	switch {
	case errors.Is(err, schools.ErrNotFound):
		return say(c.t("wa.approval.stale"))
	case errors.Is(err, schools.ErrState):
		msg := schools.Plain(err)
		if strings.Contains(msg, "decided already") {
			return say(c.t("wa.approval.already_decided"))
		}
		return say(c.t("wa.approval.not_recorded", "reason", msg))
	case err != nil:
		return err
	}
	if err := s.schoolsSvc().DecidedAfter(ctx, tenant.ID, d); err != nil {
		s.Logger.Warn("schools: after a decision from chat", "request", cl.Run, "err", err)
	}
	step := c.t("wa.school.kind."+d.Request.Kind) + " · " + invoicing.Naira(d.Request.AmountKobo)
	if approve {
		return say(c.t("wa.approval.recorded_approved", "step", step) + " " + c.t("wa.approval.result_approved"))
	}
	return say(c.t("wa.approval.recorded_rejected", "step", step) + " " + c.t("wa.approval.result_rejected"))
}
