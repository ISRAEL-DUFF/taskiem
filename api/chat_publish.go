package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/ai/builder"
	"github.com/israel-duff/taskiem/engine/connector"
	"github.com/israel-duff/taskiem/engine/datatables"
	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/sender"
	"github.com/israel-duff/taskiem/engine/storefront"
	"github.com/israel-duff/taskiem/engine/whatsapp"
	"github.com/israel-duff/taskiem/templates"
)

// Chat goes all the way (no-technical-steps N6, decision N0-7): a draft
// built in chat gets its tables, its settings and its publish there, with
// the same checks as the web app.

// Publish outcomes.
const (
	publishedNow     = "published"
	publishWaiting   = "pending_approval"
	publishNotReady  = "not_ready"
	publishInTheApp  = "in_the_app"
	publishForbidden = "forbidden"
)

// publishFromChat publishes version v of wf for the chat's person, as the
// web app's publish does: workflow.publish, readiness, four-eyes, the
// publishing checks, audited on the chat's channel. A business that
// reviews changes in Git publishes in the app.
func (s *Server) publishFromChat(ctx context.Context, c *chat, wf uuid.UUID, v int) (string, []readyNeed, error) {
	if !c.p.Can(PermWorkflowPublish) {
		return publishForbidden, nil, nil
	}
	state := publishedNow
	var needs []readyNeed
	err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{c.tenant.ID}, func(tx pgx.Tx) error {
		if err := s.refuseGitManaged(ctx, tx, wf); err != nil {
			return err
		}
		var gitLed bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM git_connections WHERE mode = 'platform_led')`).Scan(&gitLed); err != nil {
			return err
		}
		if gitLed {
			state = publishInTheApp
			return nil
		}
		if err := s.checkReady(ctx, tx, c.tenant.ID, wf, v); err != nil {
			var nr *errNotReady
			if errors.As(err, &nr) {
				state, needs = publishNotReady, nr.needs
				return nil
			}
			return err
		}
		g, err := governanceTx(ctx, tx)
		if err != nil {
			return err
		}
		if g.FourEyesPublish {
			state = publishWaiting
			if _, err := tx.Exec(ctx, `INSERT INTO publish_requests (tenant_id, workflow_id, version, requested_by) VALUES ($1, $2, $3, $4)
				ON CONFLICT (workflow_id, version) DO UPDATE SET requested_by = EXCLUDED.requested_by, requested_at = now(), status = 'pending', decided_by = NULL, decided_at = NULL`,
				c.tenant.ID, wf, v, c.p.Human()); err != nil {
				return err
			}
			return waAudit(ctx, tx, c.ch.Name(), c.p, "publish_request.create", fmt.Sprintf("%s/%d", wf, v), nil)
		}
		by := c.p.UserID
		probs, digest, _, err := s.publishTx(ctx, tx, c.tenant.ID, wf, v, &by, "")
		if errors.Is(err, errInvalid) && len(probs) > 0 {
			return fmt.Errorf("%w: %s", errConflict, problemText(probs[0]))
		}
		if err != nil {
			return err
		}
		return waAudit(ctx, tx, c.ch.Name(), c.p, "workflow.publish", fmt.Sprintf("%s/%d", wf, v), map[string]any{"digest": digest})
	})
	if err != nil {
		return "", nil, err
	}
	if state == publishedNow && s.Remote != nil {
		if err := s.Remote.ReconcileWorkflow(context.WithoutCancel(ctx), c.tenant.ID, wf); err != nil {
			s.Logger.Warn("remote subscriptions: applying after a chat publish", "workflow", wf, "err", err)
		}
	}
	return state, needs, nil
}

// publishReply says how a publish from chat went.
func (s *Server) publishReply(ctx context.Context, c *chat, state, name string, needs []readyNeed, wf uuid.UUID) string {
	switch state {
	case publishedNow:
		msg := c.t("wa.publish.done", "name", whatsapp.SafeText(name))
		if addr := s.emailAddressOf(ctx, c.tenant.ID, wf); addr != "" {
			// One that starts from email: where to send it, and how to
			// forward mail there (decision 0038).
			msg += "\n\n" + c.t("wa.publish.email_address", "address", addr, "link", s.waLink(c, "/workflows/"+wf.String()+"?tab=settings"))
		}
		return msg
	case publishWaiting:
		return c.t("wa.publish.waiting", "name", whatsapp.SafeText(name))
	case publishInTheApp:
		return c.t("wa.publish.in_app", "link", s.waLink(c, "/workflows/"+wf.String()))
	case publishForbidden:
		return c.t("wa.publish.denied", "org", c.tenant.Name)
	}
	return c.t("wa.publish.not_ready", "name", whatsapp.SafeText(name)) + "\n" + s.needLines(c, needs)
}

// emailAddressOf is a workflow's email address in prod, or "" when it
// does not start from email.
func (s *Server) emailAddressOf(ctx context.Context, tenant, wf uuid.UUID) string {
	if s.MailDomain == "" {
		return ""
	}
	var public string
	if err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT public_id FROM email_endpoints WHERE workflow_id = $1 AND environment = 'prod' AND status = 'live'`, wf).Scan(&public)
	}); err != nil {
		return ""
	}
	return public + "@" + s.MailDomain
}

// needLines are needs in the chat's language, each with where to fix it
// when chat cannot: a link to the one screen for a connection.
func (s *Server) needLines(c *chat, needs []readyNeed) string {
	var lines []string
	for _, n := range needsShown(needs) {
		if n.Capability != "" {
			// Any rail that can: named, and connected on the Connections page.
			line := "• " + n.Message
			if len(n.Rails) > 0 {
				line += " (" + strings.Join(s.railNames(c, n.Rails), ", ") + ")"
			}
			lines = append(lines, line+" "+s.waLink(c, "/connections"))
			continue
		}
		line := "• " + needMessage(c.lang.Tag, n.Kind, n.Problem, n.Service, n.Name)
		switch n.Kind {
		case "connection":
			line += " " + s.waLink(c, "/connections?connect="+n.Connector)
		case "secret":
			line += " " + s.waLink(c, "/settings")
		}
		lines = append(lines, line)
	}
	return strings.Join(dedupe(lines), "\n")
}

// needsShown are the needs chat mentions: prod's, once each.
func needsShown(needs []readyNeed) []readyNeed {
	var out []readyNeed
	seen := map[string]bool{}
	for _, n := range needs {
		if n.Environment != "prod" && !n.Blocking {
			continue
		}
		k := n.Kind + "/" + n.Name + "/" + n.Connector
		if !seen[k] {
			seen[k] = true
			out = append(out, n)
		}
	}
	return out
}

// waBuildFinish takes a saved draft on: the tables it proposed are made,
// the settings it reads asked for one at a time, and publishing offered.
func (s *Server) waBuildFinish(ctx context.Context, c *chat, b *waBuild, specs []builder.TableSpec) (string, error) {
	var notes []string
	if len(specs) > 0 {
		var made, failed []string
		if !c.p.Can(PermTableEdit) {
			for _, t := range specs {
				failed = append(failed, t.Name)
			}
		}
		for _, spec := range specs {
			if !c.p.Can(PermTableEdit) {
				break
			}
			t := datatables.Table{Name: spec.Name, Title: spec.Title, Columns: tableReqOf(spec).columns()}
			err := s.chatMakeTable(ctx, c, &t, spec.Name)
			switch {
			case err == nil:
				made = append(made, spec.Name)
			case errors.Is(err, datatables.ErrConflict):
				// It exists already: the draft uses it as it is.
			default:
				s.Logger.Warn("chat: making a draft's table", "tenant", c.tenant.ID, "table", spec.Name, "err", err)
				failed = append(failed, spec.Name)
			}
		}
		if len(made) > 0 {
			notes = append(notes, c.t("wa.build.tables_made", "tables", whatsapp.SafeText(humanName(strings.Join(made, ", ")))))
		}
		if len(failed) > 0 {
			notes = append(notes, c.t("wa.build.tables_needed", "tables", whatsapp.SafeText(strings.Join(failed, ", ")), "link", s.waLink(c, "/tables")))
		}
	}
	// The settings it reads, asked for one at a time.
	needs, err := s.versionNeeds(ctx, c.tenant.ID, b.Workflow, b.Version, c)
	if err != nil {
		return "", err
	}
	b.Settings = nil
	for _, n := range needsShown(needs) {
		if n.Kind == "variable" && c.p.Can(PermSecretManage) {
			b.Settings = append(b.Settings, n.Name)
		}
	}
	msg := strings.Join(notes, "\n")
	next, err := s.waBuildNext(ctx, c, b)
	if err != nil {
		return "", err
	}
	if msg != "" {
		return msg + "\n\n" + next, nil
	}
	return next, nil
}

// versionNeeds is what a saved version still needs, in the chat's language.
func (s *Server) versionNeeds(ctx context.Context, tenant, wf uuid.UUID, v int, c *chat) ([]readyNeed, error) {
	var needs []readyNeed
	err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		var def []byte
		if err := tx.QueryRow(ctx, `SELECT definition FROM workflow_versions WHERE workflow_id = $1 AND version = $2`, wf, v).Scan(&def); err != nil {
			return err
		}
		envs, blocking, err := readyEnvs(ctx, tx)
		if err != nil {
			return err
		}
		needs, err = s.readiness(ctx, tx, tenant, def, envs, blocking, c.lang.Tag)
		return err
	})
	return needs, err
}

// waBuildNext asks the next setting, or offers to publish: the session
// stays on the build until it is published or left.
func (s *Server) waBuildNext(ctx context.Context, c *chat, b *waBuild) (string, error) {
	if len(b.Settings) > 0 {
		b.Phase = buildSettings
		if err := s.saveSession(ctx, c, stateCollecting, chatData{Build: b}, waPendingTTL); err != nil {
			return "", err
		}
		return c.t("wa.build.ask_setting", "name", humanName(b.Settings[0])), nil
	}
	if !c.p.Can(PermWorkflowPublish) {
		if err := s.saveSession(ctx, c, stateIdle, chatData{}, 0); err != nil {
			return "", err
		}
		return c.t("wa.publish.ask_someone"), nil
	}
	needs, err := s.versionNeeds(ctx, c.tenant.ID, b.Workflow, b.Version, c)
	if err != nil {
		return "", err
	}
	if blocking := blockingNeeds(needs); len(blocking) > 0 {
		if err := s.saveSession(ctx, c, stateIdle, chatData{}, 0); err != nil {
			return "", err
		}
		return c.t("wa.build.before_publish") + "\n" + s.needLines(c, blocking) + "\n\n" + c.t("wa.build.publish_later", "name", whatsapp.SafeText(b.Name)), nil
	}
	b.Phase = buildPublish
	if err := s.saveSession(ctx, c, stateConfirming, chatData{Build: b}, waPendingTTL); err != nil {
		return "", err
	}
	return c.t("wa.build.offer_publish", "name", whatsapp.SafeText(b.Name)), nil
}

// waBuildSetting takes the answer for the setting being asked: saved as
// the business's variable in prod, typed as meant (N5's inference).
func (s *Server) waBuildSetting(ctx context.Context, c *chat, text string) error {
	b := c.data.Build
	if len(b.Settings) == 0 {
		msg, err := s.waBuildNext(ctx, c, b)
		if err != nil {
			return err
		}
		return c.say(ctx, s, msg)
	}
	name := b.Settings[0]
	value := strings.TrimSpace(text)
	if value == "" {
		return c.say(ctx, s, c.t("wa.build.ask_setting", "name", humanName(name)))
	}
	raw, _ := json.Marshal(plainValue(value))
	if err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{c.tenant.ID}, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `INSERT INTO variables (tenant_id, environment, name, value) VALUES ($1, 'prod', $2, $3)
			ON CONFLICT (tenant_id, environment, name) DO UPDATE SET value = EXCLUDED.value, updated_at = now()`, c.tenant.ID, name, raw); err != nil {
			return err
		}
		return waAudit(ctx, tx, c.ch.Name(), c.p, "variable.put", "prod/"+name, nil)
	}); err != nil {
		return err
	}
	b.Settings = b.Settings[1:]
	msg, err := s.waBuildNext(ctx, c, b)
	if err != nil {
		return err
	}
	return c.say(ctx, s, c.t("wa.build.setting_saved", "name", humanName(name))+"\n\n"+msg)
}

// waBuildPublishAnswer takes yes or no to publishing the saved draft.
func (s *Server) waBuildPublishAnswer(ctx context.Context, c *chat, cmd string) error {
	b := c.data.Build
	switch cmd {
	case "yes", "y", "publish", "ok", "okay":
		if err := s.saveSession(ctx, c, stateIdle, chatData{}, 0); err != nil {
			return err
		}
		state, needs, err := s.publishFromChat(ctx, c, b.Workflow, b.Version)
		if err != nil {
			return c.say(ctx, s, c.t("wa.publish.failed", "reason", whatsapp.SafeText(plainError(err))))
		}
		return c.say(ctx, s, s.publishReply(ctx, c, state, b.Name, needs, b.Workflow))
	case "no", "n":
		if err := s.saveSession(ctx, c, stateIdle, chatData{}, 0); err != nil {
			return err
		}
		return c.say(ctx, s, c.t("wa.build.publish_later", "name", whatsapp.SafeText(b.Name)))
	}
	return c.say(ctx, s, c.t("wa.build.offer_publish", "name", whatsapp.SafeText(b.Name)), yesNoButtons(c)...)
}

// waPublishWorkflow answers "publish <workflow>": the newest version of
// the workflow of that name, when it is a draft.
func (s *Server) waPublishWorkflow(ctx context.Context, c *chat, name string) (bool, error) {
	var wf uuid.UUID
	var v int
	var real string
	err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{c.tenant.ID}, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT w.id, w.name, (SELECT max(version) FROM workflow_versions WHERE workflow_id = w.id) FROM workflows w
			WHERE lower(w.name) = lower($1) ORDER BY w.created_at DESC LIMIT 1`, strings.TrimSpace(name)).Scan(&wf, &real, &v)
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return true, err
	}
	state, needs, err := s.publishFromChat(ctx, c, wf, v)
	if err != nil {
		return true, c.say(ctx, s, c.t("wa.publish.failed", "reason", whatsapp.SafeText(plainError(err))))
	}
	return true, c.say(ctx, s, s.publishReply(ctx, c, state, real, needs, wf))
}

// plainValue is a value typed as it is meant (no-technical-steps N5, as
// the web app's plainValue): true or false; a phone number in any common
// form, as +234…; a plain number (not one with a leading zero, an id or a
// phone); otherwise the text as typed.
func plainValue(text string) any {
	t := strings.TrimSpace(text)
	switch strings.ToLower(t) {
	case "true":
		return true
	case "false":
		return false
	}
	digits := strings.NewReplacer(" ", "", "-", "", "(", "", ")", "", ".", "").Replace(t)
	if strings.HasPrefix(digits, "+234") || strings.HasPrefix(digits, "234") && len(digits) == 13 || strings.HasPrefix(digits, "0") && len(digits) == 11 {
		if p, err := sender.NormalisePhone(t, []string{"234"}); err == nil {
			return p
		}
	}
	if plainNumber.MatchString(t) {
		if f, err := strconv.ParseFloat(t, 64); err == nil {
			return f
		}
	}
	return t
}

var plainNumber = regexp.MustCompile(`^-?(0|[1-9]\d{0,14})(\.\d+)?$`)

// yesNoButtons are yes, publish, and no.
func yesNoButtons(c *chat) []whatsapp.Button {
	return []whatsapp.Button{{ID: "yes", Title: c.t("button.yes_publish")}, {ID: "no", Title: c.t("button.no")}}
}

// shopTurnOnOrders turns the page's ordering on from chat (N6): the order
// form's default workflow (and the booking form's, for a business with
// services) is made from its template and published with the same checks
// as the web app's Forms page, the form is switched on, and the page
// republished. Payment after acceptance when Paystack is connected,
// otherwise none (paid on delivery or collection).
func (s *Server) shopTurnOnOrders(ctx context.Context, c *chat) error {
	if !c.p.Can(PermWorkflowEdit) || !c.p.Can(PermWorkflowPublish) {
		return c.say(ctx, s, c.t("wa.publish.denied", "org", c.tenant.Name))
	}
	type form struct{ kind, template, name string }
	forms := []form{{"order", "storefront-order", "Storefront orders"}}
	var actions []storefront.Action
	var paystack bool
	err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{c.tenant.ID}, func(tx pgx.Tx) error {
		sf, err := storefront.Get(ctx, tx, c.tenant.ID)
		if err != nil {
			return err
		}
		if sf.ServicesTable != nil {
			forms = append(forms, form{"booking", "storefront-booking", "Storefront bookings"})
		}
		if actions, err = storefront.Actions(ctx, tx, c.tenant.ID); err != nil {
			return err
		}
		return tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM connections WHERE environment = 'prod' AND connector = 'paystack' AND status = 'active')`).Scan(&paystack)
	})
	if err != nil {
		return err
	}
	var on []string
	for _, f := range forms {
		var have *uuid.UUID
		for _, a := range actions {
			if a.Kind == f.kind {
				have = a.Workflow
			}
		}
		if have == nil {
			tpl, ok := templates.Default().Get(f.template)
			if !ok {
				continue
			}
			var wf uuid.UUID
			if err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{c.tenant.ID}, func(tx pgx.Tx) error {
				var err error
				wf, _, err = s.instantiateTx(ctx, tx, c.p, tpl, map[string]any{}, f.name, func(action, target string, detail map[string]any) error {
					return waAudit(ctx, tx, c.ch.Name(), c.p, action, target, detail)
				})
				return err
			}); err != nil {
				return err
			}
			state, needs, err := s.publishFromChat(ctx, c, wf, 1)
			if err != nil {
				return c.say(ctx, s, c.t("wa.publish.failed", "reason", whatsapp.SafeText(plainError(err))))
			}
			if state != publishedNow {
				return c.say(ctx, s, s.publishReply(ctx, c, state, f.name, needs, wf))
			}
		}
		payment := "none"
		if paystack {
			payment = "after_accept"
		}
		if err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{c.tenant.ID}, func(tx pgx.Tx) error {
			a, err := storefront.SaveAction(ctx, tx, c.tenant.ID, storefront.Action{Kind: f.kind, Enabled: true, Payment: payment,
				Options: storefront.ActionOptions{Pickup: true}})
			if err != nil {
				return err
			}
			if sf, err := storefront.Get(ctx, tx, c.tenant.ID); err == nil && sf.PublishedVersion > 0 {
				if _, err := s.publishGated(ctx, tx, c.tenant.ID, c.p.Actor()); err != nil {
					return err
				}
			}
			return waAudit(ctx, tx, c.ch.Name(), c.p, "storefront.action_save", a.Kind, map[string]any{"enabled": true, "payment": a.Payment})
		}); err != nil {
			if reason, ok := publishRefusal(err); ok {
				return c.say(ctx, s, c.t("wa.shop.publish_failed", "reason", whatsapp.SafeText(reason)))
			}
			return err
		}
		on = append(on, f.kind)
	}
	msg := c.t("wa.shop.orders_on")
	if slices.Contains(on, "booking") {
		msg += " " + c.t("wa.shop.bookings_on")
	}
	if !paystack {
		msg += "\n" + c.t("wa.shop.orders_no_paystack", "link", s.waLink(c, "/connections?connect=paystack"))
	}
	return c.say(ctx, s, msg)
}

// waSignInLink answers "sign in" in chat (N6): a one-time link, good for
// an hour, to choose a password and sign in on the web. The owner of a
// business a field agent made has none yet; the chat's number or account
// is already bound to them, so the link goes only where they are.
func (s *Server) waSignInLink(ctx context.Context, c *chat) error {
	if s.PublicURL == "" {
		return c.say(ctx, s, c.t("wa.signin.unavailable"))
	}
	if ok, err := s.passwordUsable(ctx, c.p.UserID); err != nil || !ok {
		if err != nil {
			return err
		}
		return c.say(ctx, s, c.t("wa.signin.sso", "link", strings.TrimRight(s.PublicURL, "/")+"/login"))
	}
	if !s.limiter("wa-signin:"+c.p.UserID.String(), 10*time.Minute, 3).Allow() {
		return c.say(ctx, s, c.t("wa.too_fast"))
	}
	token, selector, secretHash := newResetToken()
	if _, err := s.Store.Pool.Exec(ctx, `SELECT taskiem_auth_password_reset_issue($1, $2, $3, $4::interval, $5)`, c.p.UserID, selector, secretHash, time.Hour.String(), c.ch.Name()); err != nil {
		return err
	}
	if err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{c.tenant.ID}, func(tx pgx.Tx) error {
		return waAudit(ctx, tx, c.ch.Name(), c.p, "auth.signin_link", c.p.UserID.String(), nil)
	}); err != nil {
		return err
	}
	return c.say(ctx, s, c.t("wa.signin.link", "link", strings.TrimRight(s.PublicURL, "/")+"/reset-password#token="+token))
}

// railNames are rails' display names, by connector id.
func (s *Server) railNames(c *chat, ids []string) []string {
	reg, err := s.Registry.For(connector.ManifestsOnly(context.Background()), c.tenant.ID.String())
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		name := id
		if err == nil {
			for _, k := range reg.List() {
				if k.Manifest.ID == id {
					name = k.Manifest.Name
				}
			}
		}
		out = append(out, name)
	}
	return out
}
