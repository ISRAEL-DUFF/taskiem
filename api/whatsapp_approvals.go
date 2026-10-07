package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/pii"
	"github.com/israel-duff/taskiem/engine/runtime"
	"github.com/israel-duff/taskiem/engine/whatsapp"
)

// Approvals over WhatsApp (spec 11.1, 9.1). When an approval opens, each
// eligible approver with a bound number gets its subject (masked) with
// Approve and Reject buttons, each carrying a signed, single-use decision
// token bound to the run, step, level, approver and decision. A tap is
// checked twice (the token, and that the number it came from is that
// approver's) and then decided by runtime.Store.VoteApproval, the web
// app's path: roles, delegation, separation of duties, distinct approvers
// and step-up all apply. A policy needing step-up gets a short-lived link
// instead, where the approver confirms that exact decision in the web app
// with a passkey or authenticator code.

const waDecisionTTL = 24 * time.Hour

// RunWhatsApp sends approval requests and run outcomes until ctx ends
// (role api, with the platform number configured).
func (s *Server) RunWhatsApp(ctx context.Context) error {
	if s.WhatsApp == nil {
		return nil
	}
	for {
		if err := s.WhatsAppTick(ctx); err != nil && ctx.Err() == nil {
			s.Logger.Error("whatsapp notifier", "err", err)
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(5 * time.Second):
		}
	}
}

// WhatsAppTick sends what is due: approval requests to approvers with a
// bound number who have not had them, and how runs started from WhatsApp
// ended.
func (s *Server) WhatsAppTick(ctx context.Context) error {
	rows, err := s.Store.Pool.Query(ctx, `SELECT tenant_id FROM taskiem_wa_tenants()`)
	if err != nil {
		return err
	}
	tenants, err := pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
	if err != nil {
		return err
	}
	var errs []error
	for _, t := range tenants {
		if err := s.waNotifyApprovals(ctx, t); err != nil {
			errs = append(errs, fmt.Errorf("tenant %s: approvals: %w", t, err))
		}
		if err := s.waNotifyRuns(ctx, t); err != nil {
			errs = append(errs, fmt.Errorf("tenant %s: runs: %w", t, err))
		}
		if err := s.waDrainOutbox(ctx, t); err != nil {
			errs = append(errs, fmt.Errorf("tenant %s: outbox: %w", t, err))
		}
	}
	return errors.Join(errs...)
}

type approvalTarget struct {
	run    uuid.UUID
	step   string
	level  int
	user   uuid.UUID
	number string
}

// waNotifyApprovals finds open approvals (requested in the last week) and
// the approvers who could decide them now: holders of the level's role or
// those it is delegated to, who have not voted, are not its makers, hold
// approval.decide and have a bound number.
func (s *Server) waNotifyApprovals(ctx context.Context, tenant uuid.UUID) error {
	var targets []approvalTarget
	var name string
	err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `SELECT name FROM tenants WHERE id = $1`, tenant).Scan(&name); err != nil {
			return err
		}
		_, _ = tx.Exec(ctx, `DELETE FROM whatsapp_tokens WHERE expires_at < now() - interval '7 days'`)
		rows, err := tx.Query(ctx, `WITH open AS (
			  SELECT a.run_id, a.step_id, a.level, a.role,
			         COALESCE((a.constraints->>'forbid_self_approval')::boolean, true) AS forbid_self,
			         COALESCE((a.constraints->>'distinct_approvers')::boolean, true) AS distinct_ap
			    FROM approvals a WHERE a.status = 'open' AND a.requested_at > now() - interval '7 days'),
			cand AS (
			  SELECT o.run_id, o.step_id, o.level, o.forbid_self, o.distinct_ap, m.user_id FROM open o JOIN memberships m ON o.role IS NULL OR m.role = o.role
			  UNION
			  SELECT o.run_id, o.step_id, o.level, o.forbid_self, o.distinct_ap, g.to_user FROM open o JOIN delegations g ON o.role = ANY (g.roles)
			     AND g.revoked_at IS NULL AND g.starts_at <= now() AND g.ends_at > now()
			     AND EXISTS (SELECT 1 FROM memberships m WHERE m.user_id = g.from_user AND m.role = o.role))
			SELECT DISTINCT c.run_id, c.step_id, c.level, c.user_id FROM cand c
			  JOIN runs r ON r.id = c.run_id JOIN workflow_versions wv ON wv.workflow_id = r.workflow_id AND wv.version = r.version
			 WHERE NOT EXISTS (SELECT 1 FROM whatsapp_notices n WHERE n.run_id = c.run_id AND n.step_id = c.step_id AND n.level = c.level AND n.user_id = c.user_id)
			   AND NOT EXISTS (SELECT 1 FROM approval_decisions d WHERE d.run_id = c.run_id AND d.step_id = c.step_id
			                     AND (d.level = c.level OR c.distinct_ap) AND (d.user_id = c.user_id OR d.on_behalf_of = c.user_id))
			   AND NOT (c.forbid_self AND c.user_id::text IN (COALESCE(taskiem_actor_human(r.started_by), ''),
			                COALESCE(taskiem_actor_human(wv.created_by), ''), COALESCE(taskiem_actor_human(wv.published_by::text), '')))
			 LIMIT 200`)
		if err != nil {
			return err
		}
		targets, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (approvalTarget, error) {
			var t approvalTarget
			return t, r.Scan(&t.run, &t.step, &t.level, &t.user)
		})
		if err != nil || len(targets) == 0 {
			return err
		}
		users := make([]uuid.UUID, 0, len(targets))
		for _, t := range targets {
			users = append(users, t.user)
		}
		rows, err = tx.Query(ctx, `SELECT user_id, number FROM taskiem_wa_numbers($1)`, users)
		if err != nil {
			return err
		}
		numbers := map[uuid.UUID]string{}
		for rows.Next() {
			var u uuid.UUID
			var n string
			if err := rows.Scan(&u, &n); err != nil {
				rows.Close()
				return err
			}
			numbers[u] = n
		}
		rows.Close()
		kept := targets[:0]
		for _, t := range targets {
			if n, ok := numbers[t.user]; ok {
				t.number = n
				kept = append(kept, t)
			}
		}
		targets = kept
		return rows.Err()
	})
	if err != nil {
		return err
	}
	allowed := map[uuid.UUID]*Principal{}
	var errs []error
	for _, t := range targets {
		p, seen := allowed[t.user]
		if !seen {
			p, _ = s.principalOf(ctx, tenant, t.user)
			if p != nil && !p.Can(PermApprovalDecide) {
				p = nil
			}
			allowed[t.user] = p
		}
		if p == nil {
			continue
		}
		if err := s.waQueueApproval(ctx, tenant, t); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// waQueueApproval queues an approval request for an approver once: the
// outbox sends it, and retries it if sending fails.
func (s *Server) waQueueApproval(ctx context.Context, tenant uuid.UUID, t approvalTarget) error {
	return db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `INSERT INTO whatsapp_notices (tenant_id, run_id, step_id, level, user_id) VALUES ($1, $2, $3, $4, $5) ON CONFLICT DO NOTHING`,
			tenant, t.run, t.step, t.level, t.user)
		if err != nil || tag.RowsAffected() == 0 {
			return err // another notifier has it
		}
		run, step, level := t.run, t.step, t.level
		_, err = whatsapp.Enqueue(ctx, tx, whatsapp.OutboxItem{Tenant: tenant, Kind: whatsapp.OutboxApproval, User: t.user, Run: &run, Step: &step, Level: &level},
			fmt.Sprintf("approval:%s:%s:%d:%s", t.run, t.step, t.level, t.user))
		return err
	})
}

// waDrainOutbox sends a tenant's due outbox messages (approval requests,
// run outcomes, alerts), each claimed once. A failure is recorded on its
// message and retried with backoff (whatsapp.FinishOutbox), never
// returned: only database errors are.
func (s *Server) waDrainOutbox(ctx context.Context, tenant uuid.UUID) error {
	items, err := whatsapp.ClaimOutbox(ctx, s.Store.Pool, tenant, 50, nil)
	if err != nil {
		return err
	}
	var errs []error
	for _, it := range items {
		var sendErr error
		switch it.Kind {
		case whatsapp.OutboxApproval:
			sendErr = s.waSendApprovalItem(ctx, it)
		case whatsapp.OutboxRunOutcome:
			sendErr = s.waSendRunOutcome(ctx, it)
		case whatsapp.OutboxAlert:
			sendErr = s.WhatsApp.SendAlertItem(ctx, it)
		default:
			sendErr = fmt.Errorf("%w: unknown kind %q", whatsapp.ErrNothingToSend, it.Kind)
		}
		if sendErr != nil && !errors.Is(sendErr, whatsapp.ErrNothingToSend) {
			s.Logger.Warn("whatsapp: send failed", "tenant", tenant, "kind", it.Kind, "message", it.ID, "attempt", it.Attempts, "err", sendErr)
		}
		if err := whatsapp.FinishOutbox(ctx, s.Store.Pool, it, sendErr, time.Now()); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// waSendApprovalItem sends a queued approval request, with fresh decision
// tokens, if it is still open and the person may still decide it.
func (s *Server) waSendApprovalItem(ctx context.Context, it whatsapp.OutboxItem) error {
	if it.Run == nil || it.Step == nil || it.Level == nil {
		return fmt.Errorf("%w: incomplete", whatsapp.ErrNothingToSend)
	}
	var name string
	if err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{it.Tenant}, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT name FROM tenants WHERE id = $1`, it.Tenant).Scan(&name)
	}); err != nil {
		return err
	}
	if p, _ := s.principalOf(ctx, it.Tenant, it.User); p == nil || !p.Can(PermApprovalDecide) {
		return fmt.Errorf("%w: the person no longer decides approvals", whatsapp.ErrNothingToSend)
	}
	number, err := s.WhatsApp.NumberOf(ctx, it.Tenant, it.User)
	if err != nil {
		return err
	}
	t := approvalTarget{run: *it.Run, step: *it.Step, level: *it.Level, user: it.User, number: number}
	return s.waSendApproval(ctx, tenantRef{it.Tenant, name}, t, false)
}

// waSendRunOutcome tells the person who started a run from WhatsApp how it
// ended.
func (s *Server) waSendRunOutcome(ctx context.Context, it whatsapp.OutboxItem) error {
	if it.Run == nil {
		return fmt.Errorf("%w: no run", whatsapp.ErrNothingToSend)
	}
	var name, status, wf, env string
	err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{it.Tenant}, func(tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `SELECT name FROM tenants WHERE id = $1`, it.Tenant).Scan(&name); err != nil {
			return err
		}
		return tx.QueryRow(ctx, `SELECT r.status, w.name, r.environment FROM runs r JOIN workflows w ON w.id = r.workflow_id WHERE r.id = $1`, *it.Run).Scan(&status, &wf, &env)
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("%w: the run is gone", whatsapp.ErrNothingToSend)
	}
	if err != nil {
		return err
	}
	number, err := s.WhatsApp.NumberOf(ctx, it.Tenant, it.User)
	if err != nil {
		return err
	}
	link := ""
	if s.PublicURL != "" {
		link = s.PublicURL + "/runs/" + it.Run.String()
	}
	kind, title := "run_completed", wf+" completed in "+env+"."
	switch status {
	case "failed":
		kind, title = "run_failed", wf+" failed in "+env+"."
	case "needs_reconciliation":
		kind, title = "needs_reconciliation", wf+" needs reconciliation in "+env+"."
	case "cancelled":
		kind, title = "run_cancelled", wf+" was cancelled in "+env+"."
	}
	m := whatsapp.AlertMessage(name, kind, title, "The run you started from WhatsApp has ended.", link, map[string]any{"workflow": wf, "environment": env})
	m.Tenant = it.Tenant
	wa, err := s.WhatsApp.ForTenant(ctx, it.Tenant)
	if err != nil {
		return err
	}
	if wa.Own() {
		m.Text = strings.TrimPrefix(m.Text, "["+name+"] ")
	}
	if _, err := wa.Send(ctx, number, m); err != nil {
		return fmt.Errorf("to %s: %w", whatsapp.MaskNumber(number), err)
	}
	return nil
}

// waSendApproval sends one approval request with fresh decision tokens.
// resend sends it again when the approver asks ("approvals").
func (s *Server) waSendApproval(ctx context.Context, tenant tenantRef, t approvalTarget, resend bool) error {
	var msg whatsapp.Message
	sent := false
	// From the tenant's own number when it has one.
	wa, err := s.WhatsApp.ForTenant(ctx, tenant.ID)
	if err != nil {
		return err
	}
	err = db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{tenant.ID}, func(tx pgx.Tx) error {
		// The queued path recorded the notice when it queued the request;
		// a resend (the approver asked) records it now.
		if resend {
			if _, err := tx.Exec(ctx, `INSERT INTO whatsapp_notices (tenant_id, run_id, step_id, level, user_id) VALUES ($1, $2, $3, $4, $5)
				ON CONFLICT (run_id, step_id, level, user_id) DO UPDATE SET sent_at = now(), error = NULL`, tenant.ID, t.run, t.step, t.level, t.user); err != nil {
				return err
			}
		}
		var wf, env string
		var levels int
		var subject []byte
		var timeout *time.Time
		err = tx.QueryRow(ctx, `SELECT w.name, r.environment, COALESCE(jsonb_array_length(a.levels), 1), a.subject, a.timeout_at
			FROM approvals a JOIN runs r ON r.id = a.run_id JOIN workflows w ON w.id = r.workflow_id
			WHERE a.run_id = $1 AND a.step_id = $2 AND a.status = 'open' AND a.level = $3`, t.run, t.step, t.level).Scan(&wf, &env, &levels, &subject, &timeout)
		if errors.Is(err, pgx.ErrNoRows) {
			return errApprovalClosed // decided meanwhile
		}
		if err != nil {
			return err
		}
		exp := time.Now().Add(waDecisionTTL)
		if timeout != nil && timeout.Before(exp) {
			exp = *timeout
		}
		exp = time.Unix(exp.Unix(), 0)
		if !exp.After(time.Now()) {
			return errApprovalClosed
		}
		var v any
		if len(subject) > 0 {
			if err := json.Unmarshal(subject, &v); err != nil {
				return err
			}
		}
		lines := whatsapp.SummaryLines(v, func(env map[string]any) (any, error) { return s.Store.PII.OpenTx(ctx, tx, tenant.ID, env) }, 10)
		notice := uuid.New()
		tokens := map[string]string{}
		for _, d := range []string{"approved", "rejected"} {
			c := whatsapp.Claims{Tenant: tenant.ID, Nonce: whatsapp.NewNonce(), Purpose: whatsapp.PurposeDecide, Decision: d, Expires: exp,
				Run: t.run, Step: t.step, Level: t.level, User: t.user}
			if _, err := tx.Exec(ctx, `INSERT INTO whatsapp_tokens (nonce, tenant_id, purpose, run_id, step_id, level, user_id, decision, notice, expires_at)
				VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`, c.Nonce[:], tenant.ID, c.Purpose, t.run, t.step, t.level, t.user, d, notice, exp); err != nil {
				return err
			}
			tokens[d] = s.WhatsApp.Signer.Sign(c)
		}
		title := whatsapp.SafeText(wf) + " · " + t.step
		where := env
		if levels > 1 {
			where = fmt.Sprintf("%s, level %d of %d", env, t.level+1, levels)
		}
		text := wa.Prefix(tenant.Name) + "Approval needed: " + title + " (" + where + ")"
		if len(lines) > 0 {
			text += "\n\n" + strings.Join(lines, "\n")
		}
		text += "\n\nThis request expires " + exp.UTC().Format("2 Jan 15:04") + " UTC."
		summary := strings.Join(lines, "; ")
		if summary == "" {
			summary = "No details."
		}
		tpl := whatsapp.TplApprovalRequest
		msg = whatsapp.Message{Text: text, Buttons: []whatsapp.Button{{ID: tokens["approved"], Title: "Approve"}, {ID: tokens["rejected"], Title: "Reject"}},
			Template: &tpl, Vars: map[string]string{"tenant": tenant.Name, "title": title, "summary": summary, "environment": where},
			Payloads: []string{tokens["approved"], tokens["rejected"]}, Tenant: tenant.ID}
		sent = true
		return nil
	})
	if errors.Is(err, errApprovalClosed) {
		if resend {
			return nil
		}
		return fmt.Errorf("%w: %w", whatsapp.ErrNothingToSend, err)
	}
	if err != nil || !sent {
		return err
	}
	_, sendErr := wa.Send(ctx, t.number, msg)
	if sendErr != nil {
		_ = db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{tenant.ID}, func(tx pgx.Tx) error {
			_, err := tx.Exec(ctx, `UPDATE whatsapp_notices SET error = $5 WHERE run_id = $1 AND step_id = $2 AND level = $3 AND user_id = $4`,
				t.run, t.step, t.level, t.user, truncateErr(sendErr))
			return err
		})
		return fmt.Errorf("to %s: %w", whatsapp.MaskNumber(t.number), sendErr)
	}
	return nil
}

var errApprovalClosed = errors.New("the approval was decided or has expired")

func truncateErr(err error) string {
	s := err.Error()
	if len(s) > 500 {
		s = s[:500]
	}
	return s
}

// waResendApprovals answers "approvals": what is waiting for the person,
// sent again with fresh buttons (at most three).
func (s *Server) waResendApprovals(ctx context.Context, c *chat) error {
	if !c.p.Can(PermApprovalDecide) {
		return c.say(ctx, s, "You do not decide approvals in "+c.tenant.Name+".")
	}
	var targets []approvalTarget
	err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{c.tenant.ID}, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT a.run_id, a.step_id, a.level FROM approvals a WHERE a.status = 'open'
			AND (a.role IS NULL OR a.role = ANY ($1) OR EXISTS (SELECT 1 FROM delegations g WHERE g.to_user = $2 AND a.role = ANY (g.roles)
			    AND g.revoked_at IS NULL AND g.starts_at <= now() AND g.ends_at > now()
			    AND EXISTS (SELECT 1 FROM memberships m WHERE m.user_id = g.from_user AND m.role = a.role)))
			AND NOT EXISTS (SELECT 1 FROM approval_decisions d WHERE d.run_id = a.run_id AND d.step_id = a.step_id AND d.level = a.level AND d.user_id = $2)
			ORDER BY a.requested_at LIMIT 3`, c.p.Roles, c.p.UserID)
		if err != nil {
			return err
		}
		targets, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (approvalTarget, error) {
			t := approvalTarget{user: c.p.UserID, number: c.number}
			return t, r.Scan(&t.run, &t.step, &t.level)
		})
		return err
	})
	if err != nil {
		return err
	}
	if len(targets) == 0 {
		return c.say(ctx, s, "Nothing is waiting for you.")
	}
	for _, t := range targets {
		if err := s.waSendApproval(ctx, c.tenant, t, true); err != nil {
			return err
		}
	}
	return nil
}

// tokenRow is a decision token's stored claims.
type tokenRow struct {
	claims whatsapp.Claims
	notice uuid.UUID
	used   bool
}

var errTokenRefused = errors.New("this button or link is not valid any more")

// loadToken reads and verifies a token in its tenant: the MAC under the
// platform key over the header and the stored claims, and the expiry.
func (s *Server) loadToken(ctx context.Context, tx pgx.Tx, tok string, purpose string) (tokenRow, error) {
	var row tokenRow
	c, mac, err := whatsapp.ParseToken(tok)
	if err != nil {
		return row, errTokenRefused
	}
	var p string
	err = tx.QueryRow(ctx, `SELECT purpose, run_id, step_id, level, user_id, notice, used_at IS NOT NULL FROM whatsapp_tokens WHERE nonce = $1 AND tenant_id = $2`,
		c.Nonce[:], c.Tenant).Scan(&p, &c.Run, &c.Step, &c.Level, &c.User, &row.notice, &row.used)
	if errors.Is(err, pgx.ErrNoRows) {
		return row, errTokenRefused
	}
	if err != nil {
		return row, err
	}
	if p != c.Purpose || p != purpose || s.WhatsApp.Signer.Verify(c, mac, time.Now()) != nil {
		return row, errTokenRefused
	}
	row.claims = c
	return row, nil
}

// waDecide handles a tapped Approve or Reject button.
func (s *Server) waDecide(ctx context.Context, c *chat, tok string) error {
	h, _, err := whatsapp.ParseToken(tok)
	if err != nil {
		return c.say(ctx, s, "That button is not valid.")
	}
	// The decision is in the tenant the token names, which may not be the
	// one the number is working in now; the person must belong to it.
	tenant := c.tenant
	p := c.p
	if h.Tenant != c.tenant.ID {
		i := -1
		for j, t := range c.tenants {
			if t.ID == h.Tenant {
				i = j
			}
		}
		if i < 0 {
			return c.say(ctx, s, "That button is not valid.")
		}
		tenant = c.tenants[i]
		if p, err = s.principalOf(ctx, tenant.ID, c.p.UserID); err != nil {
			return s.waSay(ctx, c.wa, c.number, tenant.Name, "You cannot act in "+tenant.Name+" any more.")
		}
	}
	say := func(text string) error { return s.waSay(ctx, c.wa, c.number, tenant.Name, text) }
	var row tokenRow
	refusal := ""
	var level int
	var status string
	err = db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{tenant.ID}, func(tx pgx.Tx) error {
		var err error
		row, err = s.loadToken(ctx, tx, tok, whatsapp.PurposeDecide)
		switch {
		case errors.Is(err, errTokenRefused):
			refusal = "invalid"
			return nil
		case err != nil:
			return err
		case row.claims.User != p.UserID:
			// Sent to one approver, tapped from another's number.
			refusal = "wrong_sender"
			return nil
		case row.used:
			refusal = "used"
			return nil
		}
		if err := tx.QueryRow(ctx, `SELECT status, level FROM approvals WHERE run_id = $1 AND step_id = $2 FOR UPDATE`, row.claims.Run, row.claims.Step).Scan(&status, &level); err != nil {
			return err
		}
		// Single use: spending one button spends its pair.
		_, err = tx.Exec(ctx, `UPDATE whatsapp_tokens SET used_at = now() WHERE notice = $1 AND used_at IS NULL`, row.notice)
		return err
	})
	if err != nil {
		return err
	}
	if refusal != "" {
		if refusal != "invalid" {
			_ = db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{tenant.ID}, func(tx pgx.Tx) error {
				return waAudit(ctx, tx, p, "approval.token.refused", row.claims.Run.String()+"/"+row.claims.Step, map[string]any{"reason": refusal})
			})
		}
		switch refusal {
		case "wrong_sender":
			return say("That request was sent to someone else; it cannot be decided from this number.")
		case "used":
			return say("That request was already answered from this chat.")
		}
		return say("That button is not valid any more. Send *approvals* for what is waiting for you.")
	}
	switch {
	case status != "open":
		return say("That request is already " + status + ".")
	case level != row.claims.Level:
		return say("That request has moved to its next level. Send *approvals* for what is waiting for you.")
	case !p.Can(PermApprovalDecide):
		return say("You cannot decide approvals in " + tenant.Name + " (it takes approval.decide).")
	}
	return s.waVote(ctx, c, tenant, p, row.claims, "", "button")
}

// waVote records a decision made over WhatsApp, or hands it off for
// step-up.
func (s *Server) waVote(ctx context.Context, c *chat, tenant tenantRef, p *Principal, cl whatsapp.Claims, stepUp, via string) error {
	say := func(text string) error { return s.waSay(ctx, c.wa, c.number, tenant.Name, text) }
	ref := runtime.RunRef{ID: cl.Run, TenantID: tenant.ID}
	res, err := s.Store.VoteApproval(ctx, ref, cl.Step, runtime.Vote{UserID: p.UserID, Roles: p.Roles, Decision: cl.Decision, Channel: "whatsapp", StepUp: stepUp})
	var su *runtime.StepUpError
	switch {
	case errors.As(err, &su):
		// A policy that takes the WhatsApp PIN gets a PIN form bound to
		// this decision; anything stronger, or no PIN set, the web link.
		if su.Method == runtime.StepUpWhatsAppPIN {
			if sent, err := s.waSendPinFlow(ctx, c, tenant, p, cl); err != nil || sent {
				return err
			}
		}
		return s.waHandoff(ctx, c, tenant, cl)
	case errors.Is(err, runtime.ErrNotAllowed):
		_ = db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{tenant.ID}, func(tx pgx.Tx) error {
			return waAudit(ctx, tx, p, "approval.decide.refused", cl.Run.String()+"/"+cl.Step, map[string]any{"decision": cl.Decision, "reason": err.Error(), "via": via})
		})
		reason := err.Error()
		if i := strings.LastIndex(reason, ": not allowed"); i > 0 {
			reason = reason[:i]
		}
		return say("Not recorded: " + reason + ".")
	case errors.Is(err, runtime.ErrAlreadyDecided):
		return say("You have already decided this request.")
	case errors.Is(err, runtime.ErrApprovalClosed):
		return say("That request is already closed.")
	case err != nil:
		return err
	}
	if err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{tenant.ID}, func(tx pgx.Tx) error {
		return waAudit(ctx, tx, p, "approval.decide", cl.Run.String()+"/"+cl.Step, map[string]any{"decision": cl.Decision, "result": res.Status,
			"level": res.Level + 1, "levels": res.Levels, "step_up": stepUp, "via": via})
	}); err != nil {
		return err
	}
	verb := "approved"
	if cl.Decision == "rejected" {
		verb = "rejected"
	}
	msg := "Recorded: you " + verb + " " + cl.Step + "."
	switch res.Status {
	case "approved":
		msg += " The request is approved and the run continues."
	case "rejected":
		msg += " The request is rejected."
	default:
		msg += " It is waiting for more approvals."
	}
	return say(msg)
}

// waHandoff sends a short-lived link to confirm the decision in the web
// app with a passkey or authenticator code (step-up; spec 11.2).
func (s *Server) waHandoff(ctx context.Context, c *chat, tenant tenantRef, cl whatsapp.Claims) error {
	if s.PublicURL == "" {
		return s.waSay(ctx, c.wa, c.number, tenant.Name, "This decision needs your passkey or authenticator code: decide it in Taskiem's web app, under Approvals.")
	}
	h := cl
	h.Purpose, h.Nonce = whatsapp.PurposeHandoff, whatsapp.NewNonce()
	h.Expires = time.Unix(time.Now().Add(waHandoffTTL).Unix(), 0)
	err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{tenant.ID}, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO whatsapp_tokens (nonce, tenant_id, purpose, run_id, step_id, level, user_id, decision, notice, expires_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`, h.Nonce[:], tenant.ID, h.Purpose, h.Run, h.Step, h.Level, h.User, h.Decision, uuid.New(), h.Expires)
		return err
	})
	if err != nil {
		return err
	}
	if tenant.ID == c.tenant.ID {
		if err := s.saveSession(ctx, c, stateStepUp, chatData{Run: h.Run, Step: h.Step, Decision: h.Decision}, waHandoffTTL); err != nil {
			return err
		}
	}
	// The token travels in the fragment: browsers do not send it to
	// servers, so it stays out of access logs and Referer headers.
	link := s.PublicURL + "/handoff#" + s.WhatsApp.Signer.Sign(h)
	verb := "approve"
	if h.Decision == "rejected" {
		verb = "reject"
	}
	what := verb + " " + h.Step
	tpl := whatsapp.TplStepUpLink
	_, err = c.wa.Send(ctx, c.number, whatsapp.Message{
		Text:     c.wa.Prefix(tenant.Name) + "To " + what + ", confirm with your passkey or authenticator code in Taskiem within 10 minutes:\n" + link,
		Template: &tpl, Vars: map[string]string{"tenant": tenant.Name, "what": what, "link": link}, Tenant: tenant.ID})
	return err
}

// --- step-up hand-off in the web app ---

// handoffFor loads a hand-off link's decision for the signed-in person.
func (s *Server) handoffFor(r *http.Request) (tokenRow, error) {
	p := principalFrom(r.Context())
	var row tokenRow
	if s.WhatsApp == nil {
		return row, errWhatsAppOff
	}
	if p.UserID == uuid.Nil {
		return row, fmt.Errorf("%w: approvals are decided by people, not API keys", errForbidden)
	}
	tok := chi.URLParam(r, "token")
	h, _, err := whatsapp.ParseToken(tok)
	if err != nil {
		return row, fmt.Errorf("%w: %w", errBadRequest, errTokenRefused)
	}
	if h.Tenant != p.TenantID {
		return row, fmt.Errorf("%w: this link is for another organisation: sign in to it (switch organisation) and open the link again", errConflict)
	}
	err = s.tx(r, func(tx pgx.Tx) error {
		var err error
		row, err = s.loadToken(r.Context(), tx, tok, whatsapp.PurposeHandoff)
		return err
	})
	switch {
	case errors.Is(err, errTokenRefused):
		return row, fmt.Errorf("%w: this link is not valid any more; tap the button in WhatsApp again", errBadRequest)
	case err != nil:
		return row, err
	case row.claims.User != p.UserID:
		return row, fmt.Errorf("%w: this link was sent to someone else", errForbidden)
	case row.used:
		return row, fmt.Errorf("%w: this link was already used", errConflict)
	}
	return row, nil
}

// getHandoff shows what the hand-off link decides.
func (s *Server) getHandoff(w http.ResponseWriter, r *http.Request) {
	row, err := s.handoffFor(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	p := principalFrom(r.Context())
	cl := row.claims
	out := map[string]any{"run_id": cl.Run, "step_id": cl.Step, "decision": cl.Decision, "level": cl.Level + 1, "expires_at": cl.Expires.UTC(), "tenant_id": p.TenantID}
	err = s.tx(r, func(tx pgx.Tx) error {
		var wf, env, status string
		var stepUp *string
		var subject []byte
		var levels int
		if err := tx.QueryRow(r.Context(), `SELECT w.name, r.environment, a.status, a.step_up, a.subject, COALESCE(jsonb_array_length(a.levels), 1)
			FROM approvals a JOIN runs r ON r.id = a.run_id JOIN workflows w ON w.id = r.workflow_id WHERE a.run_id = $1 AND a.step_id = $2`, cl.Run, cl.Step).
			Scan(&wf, &env, &status, &stepUp, &subject, &levels); err != nil {
			return err
		}
		var v any
		if len(subject) > 0 {
			if err := json.Unmarshal(subject, &v); err != nil {
				return err
			}
		}
		// The approver sees what they decide on, as in their inbox.
		opened, err := pii.Open(r.Context(), s.Store.PII, tx, p.TenantID, v, pii.Taint{})
		if err != nil {
			return err
		}
		out["workflow"], out["environment"], out["status"], out["step_up"], out["subject"], out["levels"] = wf, env, status, stepUp, opened, levels
		return nil
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// completeHandoff records the hand-off's decision with the step-up the
// approver just passed, through the same path as every decision.
func (s *Server) completeHandoff(w http.ResponseWriter, r *http.Request) {
	row, err := s.handoffFor(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	var req struct {
		TOTP    string          `json:"totp,omitempty"`
		Passkey *credentialJSON `json:"passkey,omitempty"`
	}
	if err := decodeBody(r, &req); err != nil {
		s.fail(w, r, err)
		return
	}
	p := principalFrom(r.Context())
	cl := row.claims
	// A passkey must have been asked for this decision on this approval.
	stepUp, ok := s.checkStepUp(w, r, stepUpProof{TOTP: req.TOTP, Passkey: req.Passkey}, approvalScope(cl.Run, cl.Step, cl.Decision))
	if !ok {
		return
	}
	if stepUp == "" {
		writeJSON(w, http.StatusForbidden, map[string]any{"error": "confirm with your passkey or authenticator code", "step_up": "required"})
		return
	}
	res, err := s.Store.VoteApproval(r.Context(), runtime.RunRef{ID: cl.Run, TenantID: p.TenantID}, cl.Step,
		runtime.Vote{UserID: p.UserID, Roles: p.Roles, Decision: cl.Decision, Channel: "whatsapp", IP: clientIP(r), StepUp: stepUp})
	var su *runtime.StepUpError
	switch {
	case errors.As(err, &su):
		writeJSON(w, http.StatusForbidden, map[string]any{"error": su.Error(), "step_up": su.Method})
		return
	case errors.Is(err, runtime.ErrNotAllowed):
		s.fail(w, r, fmt.Errorf("%w: %w", errForbidden, err))
		return
	case errors.Is(err, runtime.ErrAlreadyDecided), errors.Is(err, runtime.ErrApprovalClosed):
		s.fail(w, r, fmt.Errorf("%w: %w", errConflict, err))
		return
	case err != nil:
		s.fail(w, r, err)
		return
	}
	var number *string
	err = s.tx(r, func(tx pgx.Tx) error {
		if _, err := tx.Exec(r.Context(), `UPDATE whatsapp_tokens SET used_at = now() WHERE nonce = $1`, cl.Nonce[:]); err != nil {
			return err
		}
		if _, err := tx.Exec(r.Context(), `DELETE FROM chat_sessions WHERE tenant_id = $1 AND user_id = $2 AND state = $3`, p.TenantID, p.UserID, stateStepUp); err != nil {
			return err
		}
		if err := tx.QueryRow(r.Context(), `SELECT number FROM taskiem_wa_numbers($1)`, []uuid.UUID{p.UserID}).Scan(&number); err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		return auditTx(r, tx, "approval.decide", cl.Run.String()+"/"+cl.Step, map[string]any{"decision": cl.Decision, "result": res.Status,
			"level": res.Level + 1, "levels": res.Levels, "step_up": stepUp, "channel": "whatsapp", "via": "handoff"})
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if number != nil {
		var name string
		_ = s.tx(r, func(tx pgx.Tx) error {
			return tx.QueryRow(r.Context(), `SELECT name FROM tenants WHERE id = $1`, p.TenantID).Scan(&name)
		})
		// Inside the window only: a confirmation is not worth a template.
		if wa, err := s.WhatsApp.ForTenant(r.Context(), p.TenantID); err == nil {
			_, _ = wa.Send(r.Context(), *number, whatsapp.Message{Text: wa.Prefix(name) + "Recorded with step-up: your decision on " + cl.Step + " (" + res.Status + ")."})
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"run_id": cl.Run, "step_id": cl.Step, "decision": cl.Decision, "status": res.Status, "level": res.Level + 1, "levels": res.Levels})
}

// --- how runs started from WhatsApp ended ---

func (s *Server) waNotifyRuns(ctx context.Context, tenant uuid.UUID) error {
	// Each ended run is marked and its message queued in one transaction:
	// the outbox sends it and retries it if sending fails.
	return db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `UPDATE whatsapp_run_watches w SET notified_at = now() FROM runs r
			WHERE r.id = w.run_id AND w.notified_at IS NULL AND r.status IN ('completed', 'failed', 'cancelled', 'needs_reconciliation')
			RETURNING w.run_id, w.user_id`)
		if err != nil {
			return err
		}
		type ended struct{ run, user uuid.UUID }
		done, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (ended, error) {
			var e ended
			return e, r.Scan(&e.run, &e.user)
		})
		if err != nil {
			return err
		}
		for _, e := range done {
			run := e.run
			if _, err := whatsapp.Enqueue(ctx, tx, whatsapp.OutboxItem{Tenant: tenant, Kind: whatsapp.OutboxRunOutcome, User: e.user, Run: &run},
				"run:"+e.run.String()+":"+e.user.String()); err != nil {
				return err
			}
		}
		return nil
	})
}
