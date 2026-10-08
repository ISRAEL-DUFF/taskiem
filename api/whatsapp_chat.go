package api

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/lang"
	"github.com/israel-duff/taskiem/engine/pii"
	"github.com/israel-duff/taskiem/engine/runtime"
	"github.com/israel-duff/taskiem/engine/whatsapp"
)

// The platform number's conversation (spec 11.1–11.3, 11.5). Meta posts
// every inbound message to /channels/whatsapp on the edge, signed with the
// app secret. Each message is handled once (by its id), from a number
// bound to a person, in the tenant the number currently speaks to, with
// that person's roles there: the same permissions, policies, plan limits
// and audit as the web app.

// Chat session states (chat_sessions.state).
const (
	stateIdle       = "idle"
	stateCollecting = "collecting_input"
	stateConfirming = "awaiting_confirmation"
	stateStepUp     = "awaiting_approval_stepup"
)

const (
	waPendingTTL = 30 * time.Minute // a command being confirmed or filled in
	waHandoffTTL = 10 * time.Minute // a step-up link
)

// WhatsAppHooks receives Meta's webhooks: GET for the subscription
// handshake, POST for messages. "/" is Taskiem's app: the shared number
// and own numbers connected through it; "/n/{pnid}" is an own number on
// the tenant's own Meta app, verified with that app's secret; "/flows" and
// "/flows/{pnid}" are the WhatsApp Flows data endpoint (whatsapp_flows.go).
func (s *Server) WhatsAppHooks() http.Handler {
	r := chi.NewRouter()
	r.Get("/", s.waHandshake)
	r.Post("/", s.waReceive)
	r.Get("/n/{pnid}", s.waHandshake)
	r.Post("/n/{pnid}", s.waReceive)
	r.Post("/flows", s.waFlowsEndpoint)
	r.Post("/flows/{pnid}", s.waFlowsEndpoint)
	r.Post("/embedded-signup", s.waEmbeddedSignup)
	return r
}

// waHookPlatform is the number a webhook path is for: the shared number's
// app for "/", an own number on its own app for "/n/{pnid}"; nil if none.
func (s *Server) waHookPlatform(r *http.Request) *whatsapp.Platform {
	if s.WhatsApp == nil {
		return nil
	}
	pnid := chi.URLParam(r, "pnid")
	if pnid == "" {
		return s.WhatsApp
	}
	wa, err := s.WhatsApp.ForPhoneNumberID(r.Context(), pnid)
	if err != nil {
		s.Logger.Error("whatsapp: routing a webhook", "err", err)
		return nil
	}
	if wa == nil || !wa.OwnApp {
		return nil
	}
	return wa
}

func (s *Server) waHandshake(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	wa := s.waHookPlatform(r)
	if wa == nil || q.Get("hub.mode") != "subscribe" || wa.Config.VerifyToken == "" ||
		subtle.ConstantTimeCompare([]byte(q.Get("hub.verify_token")), []byte(wa.Config.VerifyToken)) != 1 {
		writeErr(w, http.StatusForbidden, "forbidden")
		return
	}
	// Meta's challenge is a number; nothing else is echoed.
	challenge := q.Get("hub.challenge")
	if !challengeRe.MatchString(challenge) {
		writeErr(w, http.StatusBadRequest, "bad challenge")
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = io.WriteString(w, challenge) //nolint:gosec // digits only, as text/plain
}

var challengeRe = regexp.MustCompile(`^[0-9A-Za-z_-]{1,128}$`)

func (s *Server) waReceive(w http.ResponseWriter, r *http.Request) {
	hook := s.waHookPlatform(r)
	if hook == nil {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
	if err != nil {
		writeErr(w, http.StatusRequestEntityTooLarge, "too large")
		return
	}
	if !whatsapp.VerifySignature(hook.Config.AppSecret, body, r.Header.Get("X-Hub-Signature-256")) {
		writeErr(w, http.StatusUnauthorized, "bad signature")
		return
	}
	msgs, err := whatsapp.Parse(body)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "bad payload")
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 30*time.Second)
	defer cancel()
	for _, m := range msgs {
		if m.ID == "" {
			continue
		}
		// Messages are routed by the number they were sent to. A delivery
		// signed with one app's secret speaks only for that app's numbers:
		// Taskiem's app for the shared number and own numbers connected
		// through it; an own app for its own number.
		wa := hook
		if m.PhoneNumberID != hook.Config.PhoneNumberID {
			if hook.Own() {
				continue
			}
			if wa, err = s.WhatsApp.ForPhoneNumberID(ctx, m.PhoneNumberID); err != nil {
				s.Logger.Error("whatsapp: routing a message", "err", err)
				writeErr(w, http.StatusServiceUnavailable, "try again")
				return
			}
			if wa == nil || wa.OwnApp {
				continue // another number on the same app, or one that must come signed by its own app
			}
		}
		// At most once: a message is claimed before it is acted on, so a
		// retried delivery never repeats a command.
		fresh, err := wa.ClaimInbound(ctx, m.ID)
		if err != nil {
			s.Logger.Error("whatsapp: claiming a message", "err", err)
			writeErr(w, http.StatusServiceUnavailable, "try again")
			return
		}
		if !fresh {
			continue
		}
		if err := s.waHandle(ctx, wa, m); err != nil {
			s.Logger.Error("whatsapp: handling a message", "err", err)
		}
	}
	w.WriteHeader(http.StatusOK)
}

// chat is one inbound message being handled.
type chat struct {
	wa      *whatsapp.Platform // the number it was sent to; replies go from it
	number  string
	in      whatsapp.Inbound
	p       *Principal // nil until resolved
	tenant  tenantRef
	tenants []tenantRef
	state   string
	data    chatData
	// lang is the language replies are in, and the commands understood.
	lang langChoice
	// note goes before the next reply (a voice note's transcript).
	note string
}

// t is a message in the conversation's language.
func (c *chat) t(id string, kv ...string) string { return tr(c.lang.Tag, id, kv...) }

// chatData is a session's tenant-scoped state.
type chatData struct {
	Workflow    uuid.UUID      `json:"workflow,omitempty"`
	Name        string         `json:"name,omitempty"`
	Version     int            `json:"version,omitempty"`
	Environment string         `json:"environment,omitempty"`
	Index       int            `json:"index,omitempty"`
	Inputs      map[string]any `json:"inputs,omitempty"` // personal fields sealed
	Nonce       string         `json:"nonce,omitempty"`  // the start's idempotency key
	Run         uuid.UUID      `json:"run,omitempty"`
	Step        string         `json:"step,omitempty"`
	Decision    string         `json:"decision,omitempty"`
	// Flow is the hex SHA-256 of the flow token of the inputs Flow sent,
	// while the inputs are collected by a WhatsApp form.
	Flow string `json:"flow,omitempty"`
	// Build is an AI build being shown, completed or confirmed
	// (whatsapp_build.go).
	Build *waBuild `json:"build,omitempty"`
}

// WhatsAppPublic, when set, answers a message to the shared number from a
// number bound to no one. It reports whether it handled the message.
// Tenants' own numbers have their public menus built in
// (whatsapp_public.go).
type WhatsAppPublic func(ctx context.Context, number string, in whatsapp.Inbound) (bool, error)

var sixDigits = regexp.MustCompile(`^\s*(\d{6})\s*$`)

func (s *Server) waHandle(ctx context.Context, wa *whatsapp.Platform, in whatsapp.Inbound) error {
	number, err := whatsapp.FromWaID(in.From)
	if err != nil {
		return nil //nolint:nilerr // a sender id that is not a phone number: nothing to answer
	}
	if err := wa.Touch(ctx, number, true, false, nil); err != nil {
		return err
	}
	// Per number (spec 11.5): a compromised phone cannot flood.
	if !s.limiter("wa:"+number, 3*time.Second, 20).Allow() {
		if s.limiter("wa-warn:"+number, time.Minute, 1).Allow() {
			return s.waSay(ctx, wa, number, "", tr(s.chooseLang(ctx, wa.Tenant, number, "").Tag, "wa.too_fast"))
		}
		return nil
	}
	user, err := s.WhatsApp.UserOf(ctx, number)
	if err != nil {
		return err
	}
	if user == uuid.Nil {
		return s.waUnbound(ctx, wa, number, in)
	}
	c := &chat{wa: wa, number: number, in: in}
	c.lang = s.chooseLang(ctx, wa.Tenant, number, "")
	if c.tenants, err = s.tenantsOf(ctx, user); err != nil {
		return err
	}
	if wa.Own() {
		// A tenant's own number speaks for that tenant only.
		i := slices.IndexFunc(c.tenants, func(t tenantRef) bool { return t.ID == wa.Tenant })
		if i < 0 {
			return s.waSay(ctx, wa, number, "", c.t("wa.not_member"))
		}
		c.tenant, c.tenants = c.tenants[i], c.tenants[i:i+1]
	} else {
		if len(c.tenants) == 0 {
			return s.waSay(ctx, wa, number, "", c.t("wa.no_org"))
		}
		contact, err := wa.Contact(ctx, number)
		if err != nil {
			return err
		}
		c.tenant = c.tenants[0]
		if contact.Tenant != nil {
			if i := slices.IndexFunc(c.tenants, func(t tenantRef) bool { return t.ID == *contact.Tenant }); i >= 0 {
				c.tenant = c.tenants[i]
			}
		}
		if contact.Tenant == nil || *contact.Tenant != c.tenant.ID {
			if err := wa.Touch(ctx, number, false, true, &c.tenant.ID); err != nil {
				return err
			}
		}
	}
	if c.p, err = s.principalOf(ctx, c.tenant.ID, user); err != nil {
		return s.waSay(ctx, wa, number, "", c.t("wa.cannot_act", "org", c.tenant.Name))
	}
	// The language of this tenant's conversation: chosen, detected from
	// what is written, or the tenant's default (languages.go).
	text := ""
	if in.Type == "text" {
		text = in.Text
	}
	c.lang = s.chooseLang(ctx, c.tenant.ID, number, text)
	if c.lang.Detected {
		c.note = c.t("lang.detected", "language", langName(c.lang.Tag))
	}
	// A tapped approval button carries a signed decision token.
	if strings.HasPrefix(in.Reply, "tk1.") {
		return s.waDecide(ctx, c, in.Reply)
	}
	if err := s.loadSession(ctx, c); err != nil {
		return err
	}
	// A completed WhatsApp form carries its flow token.
	if in.Flow != "" {
		return s.waFlowCompleted(ctx, c)
	}
	// A voice note is transcribed (a beta) and then read as if typed.
	if in.Media != nil {
		return s.waVoice(ctx, c)
	}
	return s.waCommand(ctx, c)
}

// waUnbound answers a number bound to no one: the code it was sent, if it
// sends that back; a tenant's public menu on its own number; or how to
// link it.
func (s *Server) waUnbound(ctx context.Context, wa *whatsapp.Platform, number string, in whatsapp.Inbound) error {
	text := ""
	if in.Type == "text" {
		text = in.Text
	}
	l := s.chooseLang(ctx, wa.Tenant, number, text).Tag
	if m := sixDigits.FindStringSubmatch(in.Text); m != nil {
		var user *uuid.UUID
		if err := s.Store.Pool.QueryRow(ctx, `SELECT taskiem_wa_otp_user_for($1, $2)`, number, m[1]).Scan(&user); err != nil {
			return err
		}
		if user != nil {
			if _, err := s.waVerifyCode(ctx, *user, m[1], "whatsapp", ""); err != nil {
				return s.waSay(ctx, wa, number, "", tr(l, "wa.code_failed", "reason", err.Error()))
			}
			return s.waSay(ctx, wa, number, "", tr(l, "wa.linked"))
		}
	}
	if wa.Own() {
		if done, err := s.waPublic(ctx, wa, number, in); done || err != nil {
			return err
		}
	} else if s.WhatsAppPublic != nil {
		if done, err := s.WhatsAppPublic(ctx, number, in); done || err != nil {
			return err
		}
	}
	if !s.limiter("wa-unbound:"+number, 10*time.Minute, 2).Allow() {
		return nil
	}
	where := "Account"
	if s.PublicURL != "" {
		where = s.PublicURL + "/account"
	}
	return s.waSay(ctx, wa, number, "", tr(l, "wa.unbound", "where", where))
}

// waSay replies inside the conversation window from wa, in the tenant's
// name (on the shared number).
func (s *Server) waSay(ctx context.Context, wa *whatsapp.Platform, number, tenant, text string, buttons ...whatsapp.Button) error {
	_, err := wa.Send(ctx, number, whatsapp.Message{Text: wa.Prefix(tenant) + text, Buttons: buttons})
	return err
}

func (c *chat) say(ctx context.Context, s *Server, text string, buttons ...whatsapp.Button) error {
	if c.note != "" {
		text, c.note = c.note+"\n\n"+text, ""
	}
	return s.waSay(ctx, c.wa, c.number, c.tenant.Name, text, buttons...)
}

// loadSession reads the number's session in its current tenant; an
// expired one is idle.
func (s *Server) loadSession(ctx context.Context, c *chat) error {
	c.state = stateIdle
	return db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{c.tenant.ID}, func(tx pgx.Tx) error {
		var raw []byte
		err := tx.QueryRow(ctx, `SELECT state, data FROM chat_sessions WHERE tenant_id = $1 AND number = $2 AND user_id = $3 AND expires_at > now()`,
			c.tenant.ID, c.number, c.p.UserID).Scan(&c.state, &raw)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		return json.Unmarshal(raw, &c.data)
	})
}

// saveSession stores the session's state; idle removes it.
func (s *Server) saveSession(ctx context.Context, c *chat, state string, data chatData, ttl time.Duration) error {
	c.state, c.data = state, data
	return db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{c.tenant.ID}, func(tx pgx.Tx) error {
		if state == stateIdle {
			_, err := tx.Exec(ctx, `DELETE FROM chat_sessions WHERE tenant_id = $1 AND number = $2`, c.tenant.ID, c.number)
			return err
		}
		raw, _ := json.Marshal(data)
		// Never past the conversation window: after it, only templates.
		ttl = min(ttl, whatsapp.Window)
		_, err := tx.Exec(ctx, `INSERT INTO chat_sessions (tenant_id, number, user_id, state, data, expires_at) VALUES ($1, $2, $3, $4, $5, now() + $6::interval)
			ON CONFLICT (tenant_id, number) DO UPDATE SET user_id = EXCLUDED.user_id, state = EXCLUDED.state, data = EXCLUDED.data,
			  expires_at = EXCLUDED.expires_at, updated_at = now()`, c.tenant.ID, c.number, c.p.UserID, state, raw, ttl.String())
		return err
	})
}

// normalizeCommand lowercases a message and drops punctuation at its ends.
func normalizeCommand(s string) string {
	return strings.Trim(strings.ToLower(strings.Join(strings.Fields(s), " ")), " ?!.")
}

func (s *Server) waCommand(ctx context.Context, c *chat) error {
	text := c.in.Text
	if c.in.Reply != "" {
		text = c.in.Reply
	}
	cmd := normalizeCommand(text)
	// Commands in the person's language and in English (engine/lang);
	// yes, no and cancel are only ever matched from the word lists.
	m, matched := lang.MatchIntent(text, c.lang.Match())
	if matched {
		switch m.Intent {
		case lang.IntentCancel:
			cmd = "cancel"
		case lang.IntentYes:
			cmd = "yes"
		case lang.IntentNo:
			cmd = "no"
		}
	}
	if cmd == "cancel" || cmd == "stop" {
		if c.state == stateIdle {
			return c.say(ctx, s, c.t("wa.nothing_to_cancel"))
		}
		if err := s.saveSession(ctx, c, stateIdle, chatData{}, 0); err != nil {
			return err
		}
		return c.say(ctx, s, c.t("wa.cancelled"))
	}
	if c.data.Build != nil && c.state != stateIdle {
		return s.waBuildReply(ctx, c, text, cmd)
	}
	switch c.state {
	case stateCollecting:
		return s.waCollect(ctx, c, text)
	case stateConfirming:
		switch cmd {
		case "yes", "y", "confirm", "run it", "go":
			return s.waConfirm(ctx, c)
		case "no", "n":
			if err := s.saveSession(ctx, c, stateIdle, chatData{}, 0); err != nil {
				return err
			}
			return c.say(ctx, s, c.t("wa.cancelled_nothing_started"))
		}
		return c.say(ctx, s, c.t("wa.confirm_reminder", "workflow", c.data.Name), confirmButtons(c)...)
	}
	if matched {
		if done, err := s.waIntent(ctx, c, m); done || err != nil {
			return err
		}
	}
	if goal, ok := waBuildGoal(text, cmd); ok && c.state == stateIdle {
		return s.waStartBuild(ctx, c, goal)
	}
	if c.state == stateStepUp {
		return c.say(ctx, s, c.t("wa.stepup_waiting", "step", c.data.Step))
	}
	// Not recognised: in a language other than English, the model may
	// route it (engine/ai/intent); it never confirms anything.
	if c.state == stateIdle && !matched {
		if m := s.waModelIntent(ctx, c, text); m.Intent != lang.IntentNone {
			if done, err := s.waIntent(ctx, c, m); done || err != nil {
				return err
			}
		}
	}
	return c.say(ctx, s, c.t("wa.not_understood")+" "+s.waHelp(c))
}

// waIntent does what a recognised command asks; false when it is not one
// to act on here (yes or no with nothing to confirm, run without a name).
func (s *Server) waIntent(ctx context.Context, c *chat, m lang.Match) (bool, error) {
	arg := strings.TrimSpace(m.Arg)
	switch m.Intent {
	case lang.IntentHelp:
		return true, c.say(ctx, s, s.waHelp(c))
	case lang.IntentStatus:
		return true, s.waStatus(ctx, c)
	case lang.IntentApprovals:
		return true, s.waResendApprovals(ctx, c)
	case lang.IntentSwitch:
		return true, s.waSwitch(ctx, c, strings.ToLower(arg))
	case lang.IntentLanguage:
		return true, s.waLanguage(ctx, c, arg)
	case lang.IntentRun:
		if arg == "" {
			return false, nil
		}
		return true, s.waTrigger(ctx, c, strings.ToLower(arg))
	case lang.IntentBuild:
		// The English build words are read by waBuildGoal, as before.
		if c.state != stateIdle || m.Language == lang.EN {
			return false, nil
		}
		return true, s.waStartBuild(ctx, c, arg)
	}
	return false, nil
}

func (s *Server) waHelp(c *chat) string {
	lines := []string{c.t("wa.help.intro"), c.t("wa.help.status")}
	if c.p.Can(PermRunStart) {
		lines = append(lines, c.t("wa.help.run"))
	}
	if c.p.Can(PermApprovalDecide) {
		lines = append(lines, c.t("wa.help.approvals"))
	}
	if c.p.Can(PermWorkflowEdit) && s.AI != nil {
		lines = append(lines, c.t("wa.help.build"))
	}
	if len(c.tenants) > 1 {
		lines = append(lines, c.t("wa.help.switch"))
	}
	if len(c.lang.On) > 1 {
		lines = append(lines, c.t("wa.help.language", "languages", langNames(c.lang.On)))
	}
	if s.voiceOn(c.lang.Channel) {
		lines = append(lines, c.t("wa.help.voice"))
	}
	lines = append(lines, c.t("wa.help.cancel"))
	return strings.Join(lines, "\n")
}

// waSwitch changes the tenant the number speaks to.
func (s *Server) waSwitch(ctx context.Context, c *chat, name string) error {
	list := func() string {
		names := make([]string, len(c.tenants))
		for i, t := range c.tenants {
			names[i] = "• " + t.Name
		}
		return strings.Join(names, "\n")
	}
	if name == "" {
		return c.say(ctx, s, c.t("wa.switch.current", "org", c.tenant.Name, "list", list()))
	}
	var match []tenantRef
	for _, t := range c.tenants {
		n := strings.ToLower(t.Name)
		if n == name {
			match = []tenantRef{t}
			break
		}
		if strings.HasPrefix(n, name) {
			match = append(match, t)
		}
	}
	if len(match) != 1 {
		return c.say(ctx, s, c.t("wa.switch.which", "list", list()))
	}
	// What was in progress belonged to the old tenant.
	if err := s.saveSession(ctx, c, stateIdle, chatData{}, 0); err != nil {
		return err
	}
	t := match[0]
	if err := c.wa.Touch(ctx, c.number, false, true, &t.ID); err != nil {
		return err
	}
	c.tenant = t
	return c.say(ctx, s, c.t("wa.switch.done", "org", t.Name))
}

// waStatus answers "status" / "what failed today?": a read-only summary of
// the last 24 hours, as the person may see runs (run.read), redacted.
func (s *Server) waStatus(ctx context.Context, c *chat) error {
	if !c.p.Can(PermRunRead) {
		return c.say(ctx, s, c.t("wa.status.denied", "org", c.tenant.Name))
	}
	counts := map[string]int{}
	type failure struct {
		wf, env, status string
		at              time.Time
	}
	var fails []failure
	var waiting int
	err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{c.tenant.ID}, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT status, count(*) FROM runs WHERE started_at > now() - interval '24 hours'
			AND ($1 = '' OR environment = $1) GROUP BY status`, c.p.Environment)
		if err != nil {
			return err
		}
		for rows.Next() {
			var st string
			var n int
			if err := rows.Scan(&st, &n); err != nil {
				rows.Close()
				return err
			}
			counts[st] = n
		}
		rows.Close()
		rows, err = tx.Query(ctx, `SELECT w.name, r.environment, r.status, COALESCE(r.ended_at, r.started_at) FROM runs r JOIN workflows w ON w.id = r.workflow_id
			WHERE r.status IN ('failed', 'needs_reconciliation') AND COALESCE(r.ended_at, r.started_at) > now() - interval '24 hours'
			AND ($1 = '' OR r.environment = $1) ORDER BY 4 DESC LIMIT 5`, c.p.Environment)
		if err != nil {
			return err
		}
		fails, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (failure, error) {
			var f failure
			return f, r.Scan(&f.wf, &f.env, &f.status, &f.at)
		})
		if err != nil {
			return err
		}
		if c.p.Can(PermApprovalDecide) {
			waiting, err = openApprovalsFor(ctx, tx, c.p)
		}
		return err
	})
	if err != nil {
		return err
	}
	total := 0
	for _, n := range counts {
		total += n
	}
	var b strings.Builder
	if total == 0 {
		b.WriteString(c.t("wa.status.none"))
	} else {
		var parts []string
		for _, st := range []string{"completed", "failed", "needs_reconciliation", "running", "waiting", "queued", "cancelled"} {
			if n := counts[st]; n > 0 {
				parts = append(parts, fmt.Sprintf("%d %s", n, c.t("run.status."+st)))
			}
		}
		id := "wa.status.summary"
		if total == 1 {
			id = "wa.status.summary_one"
		}
		b.WriteString(c.t(id, "count", strconv.Itoa(total), "parts", strings.Join(parts, ", ")))
	}
	if len(fails) > 0 {
		b.WriteString("\n\n" + c.t("wa.status.failed_head"))
		for _, f := range fails {
			what := c.t("run.status.failed")
			if f.status == "needs_reconciliation" {
				what = c.t("run.status.needs_reconciliation")
			}
			b.WriteString("\n" + c.t("wa.status.failed_line", "workflow", whatsapp.SafeText(f.wf), "env", f.env, "what", what, "time", f.at.UTC().Format("15:04")))
		}
	} else if total > 0 {
		b.WriteString("\n" + c.t("wa.status.nothing_failed"))
	}
	if waiting > 0 {
		b.WriteString("\n\n" + c.t("wa.status.waiting", "count", strconv.Itoa(waiting)))
	}
	if s.PublicURL != "" && total > 0 {
		b.WriteString("\n\n" + s.PublicURL + "/runs")
	}
	return c.say(ctx, s, b.String())
}

// openApprovalsFor counts open approvals the principal may decide.
func openApprovalsFor(ctx context.Context, tx pgx.Tx, p *Principal) (int, error) {
	var n int
	err := tx.QueryRow(ctx, `SELECT count(*) FROM approvals a WHERE a.status = 'open'
		AND (a.role IS NULL OR a.role = ANY ($1) OR EXISTS (SELECT 1 FROM delegations g WHERE g.to_user = $2 AND a.role = ANY (g.roles)
		    AND g.revoked_at IS NULL AND g.starts_at <= now() AND g.ends_at > now()
		    AND EXISTS (SELECT 1 FROM memberships m WHERE m.user_id = g.from_user AND m.role = a.role)))
		AND NOT EXISTS (SELECT 1 FROM approval_decisions d WHERE d.run_id = a.run_id AND d.step_id = a.step_id AND d.level = a.level AND d.user_id = $2)`,
		p.Roles, p.UserID).Scan(&n)
	return n, err
}

var nonWord = regexp.MustCompile(`[^a-z0-9]+`)

func matchKey(s string) string {
	return strings.TrimSpace(nonWord.ReplaceAllString(strings.ToLower(s), " "))
}

type runnable struct {
	id      uuid.UUID
	name    string
	defID   string
	version int
	def     []byte
}

// waTrigger answers "run <workflow>" (spec 11.1): the name is matched
// deterministically to a workflow deployed in prod that the person may
// start, its inputs collected field by field, then confirmed.
func (s *Server) waTrigger(ctx context.Context, c *chat, name string) error {
	if !c.p.Can(PermRunStart) {
		return c.say(ctx, s, c.t("wa.run.denied", "org", c.tenant.Name))
	}
	env, err := environment(c.p, "")
	if err != nil {
		return err
	}
	var all []runnable
	err = db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{c.tenant.ID}, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT w.id, w.name, wv.version, wv.definition FROM workflows w
			JOIN deployments d ON d.workflow_id = w.id AND d.environment = $1
			JOIN workflow_versions wv ON wv.workflow_id = w.id AND wv.version = d.version AND wv.state IN ('published', 'deprecated')
			ORDER BY w.name`, env)
		if err != nil {
			return err
		}
		all, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (runnable, error) {
			var x runnable
			return x, r.Scan(&x.id, &x.name, &x.version, &x.def)
		})
		return err
	})
	if err != nil {
		return err
	}
	key := matchKey(name)
	var exact, partial []runnable
	for _, x := range all {
		var head struct {
			ID string `json:"id"`
		}
		_ = json.Unmarshal(x.def, &head)
		x.defID = head.ID
		n, id := matchKey(x.name), matchKey(x.defID)
		switch {
		case n == key || id == key:
			exact = append(exact, x)
		case strings.Contains(n, key) || strings.Contains(id, key):
			partial = append(partial, x)
		}
	}
	if len(exact) == 0 {
		exact = partial
	}
	switch {
	case len(exact) == 0:
		return c.say(ctx, s, c.t("wa.run.no_match", "env", env, "name", fmt.Sprintf("%q", name)))
	case len(exact) > 1:
		names := make([]string, 0, 5)
		for _, x := range exact[:min(5, len(exact))] {
			names = append(names, "• "+x.name)
		}
		return c.say(ctx, s, c.t("wa.run.which", "list", strings.Join(names, "\n")))
	}
	x := exact[0]
	d, err := s.definitionFor(x.id, x.version, x.def)
	if err != nil {
		return err
	}
	data := chatData{Workflow: x.id, Name: x.name, Version: x.version, Environment: env, Inputs: map[string]any{}}
	var raw json.RawMessage
	if d.RawInputs != nil {
		raw = d.RawInputs.Schema
	}
	fields, err := whatsapp.InputFields(raw, d.RawTypes)
	if errors.Is(err, whatsapp.ErrInputsUnsupported) {
		return c.say(ctx, s, c.t("wa.run.unsupported_inputs", "workflow", x.name))
	}
	if err != nil {
		return err
	}
	if len(fields) > 0 {
		// A WhatsApp form where Flows are set up and the fields fit one;
		// otherwise field by field in chat.
		if sent, err := s.waSendInputsFlow(ctx, c, data, fields); err != nil || sent {
			return err
		}
		if err := s.saveSession(ctx, c, stateCollecting, data, waPendingTTL); err != nil {
			return err
		}
		return c.say(ctx, s, c.t("wa.run.needs_inputs", "workflow", x.name, "count", strconv.Itoa(len(fields)), "prompt", fields[0].Prompt()))
	}
	return s.waAskConfirm(ctx, c, data)
}

// waFields are the inputs to collect for the session's workflow.
func (s *Server) waFields(ctx context.Context, c *chat) ([]whatsapp.Field, error) {
	return s.waFieldsOf(ctx, c.tenant.ID, c.data.Workflow, c.data.Version)
}

// waFieldsOf are the required inputs of a workflow version.
func (s *Server) waFieldsOf(ctx context.Context, tenant, wf uuid.UUID, version int) ([]whatsapp.Field, error) {
	var def []byte
	if err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT definition FROM workflow_versions WHERE workflow_id = $1 AND version = $2`, wf, version).Scan(&def)
	}); err != nil {
		return nil, err
	}
	d, err := s.definitionFor(wf, version, def)
	if err != nil {
		return nil, err
	}
	var raw json.RawMessage
	if d.RawInputs != nil {
		raw = d.RawInputs.Schema
	}
	return whatsapp.InputFields(raw, d.RawTypes)
}

// waCollect takes the answer for the next input field.
func (s *Server) waCollect(ctx context.Context, c *chat, text string) error {
	fields, err := s.waFields(ctx, c)
	if err != nil {
		return err
	}
	if c.data.Flow != "" {
		// Writing instead of using the form: the form is dropped, and the
		// inputs are asked here one by one.
		data := c.data
		s.waDropFlow(ctx, c.tenant.ID, data.Flow)
		data.Flow, data.Index, data.Inputs = "", 0, map[string]any{}
		if err := s.saveSession(ctx, c, stateCollecting, data, waPendingTTL); err != nil {
			return err
		}
		return c.say(ctx, s, c.t("wa.run.form_closed", "prompt", fields[0].Prompt()))
	}
	if c.data.Index >= len(fields) {
		return s.waAskConfirm(ctx, c, c.data)
	}
	f := fields[c.data.Index]
	v, perr := f.Parse(text)
	if perr != nil {
		return c.say(ctx, s, c.t("wa.run.does_not_fit", "reason", perr.Error(), "prompt", f.Prompt()))
	}
	data := c.data
	if data.Inputs == nil {
		data.Inputs = map[string]any{}
	}
	// Personal data waits sealed, like run history.
	if f.PII != "" {
		err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{c.tenant.ID}, func(tx pgx.Tx) error {
			env, err := s.Store.PII.SealTx(ctx, tx, c.tenant.ID, f.PII, v)
			v = env
			return err
		})
		if err != nil {
			return err
		}
	}
	data.Inputs[f.Name] = v
	data.Index++
	if data.Index < len(fields) {
		if err := s.saveSession(ctx, c, stateCollecting, data, waPendingTTL); err != nil {
			return err
		}
		return c.say(ctx, s, fields[data.Index].Prompt())
	}
	return s.waAskConfirm(ctx, c, data)
}

func confirmButtons(c *chat) []whatsapp.Button { return confirmButtonsIn(c.lang.Tag) }

func confirmButtonsIn(l lang.Tag) []whatsapp.Button {
	return []whatsapp.Button{{ID: "yes", Title: tr(l, "button.yes_run")}, {ID: "cancel", Title: tr(l, "button.cancel")}}
}

// waAskConfirm summarises exactly what will run (spec 11.5) and waits for
// an explicit yes.
func (s *Server) waAskConfirm(ctx context.Context, c *chat, data chatData) error {
	data.Nonce = uuid.NewString()
	if err := s.saveSession(ctx, c, stateConfirming, data, waPendingTTL); err != nil {
		return err
	}
	lines, err := s.maskedLines(ctx, c.tenant.ID, data.Inputs, 12)
	if err != nil {
		return err
	}
	kv := []string{"workflow", data.Name, "version", strconv.Itoa(data.Version), "env", data.Environment}
	msg := c.t("wa.run.confirm", kv...)
	if len(lines) > 0 {
		msg = c.t("wa.run.confirm_inputs", append(kv, "inputs", strings.Join(lines, "\n"))...)
	}
	return c.say(ctx, s, msg, confirmButtons(c)...)
}

// maskedLines renders a value for a message, sealed fields opened only to
// be masked.
func (s *Server) maskedLines(ctx context.Context, tenant uuid.UUID, v any, max int) ([]string, error) {
	var lines []string
	err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		lines = whatsapp.SummaryLines(v, func(env map[string]any) (any, error) { return s.Store.PII.OpenTx(ctx, tx, tenant, env) }, max)
		return nil
	})
	return lines, err
}

// waConfirm starts the confirmed run through the same checks as the API:
// run.start, the deployed version, the inputs schema and the plan limits.
func (s *Server) waConfirm(ctx context.Context, c *chat) error {
	data := c.data
	if err := s.saveSession(ctx, c, stateIdle, chatData{}, 0); err != nil {
		return err
	}
	if !c.p.Can(PermRunStart) {
		return c.say(ctx, s, c.t("wa.run.denied_now", "org", c.tenant.Name))
	}
	if !s.limiter("wa-run:"+c.number, 20*time.Second, 3).Allow() {
		return c.say(ctx, s, c.t("wa.run.too_many"))
	}
	var input any
	var def []byte
	deployed := 0
	err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{c.tenant.ID}, func(tx pgx.Tx) error {
		var err error
		if deployed, err = deployedVersion(ctx, tx, data.Workflow, data.Environment); err != nil {
			return err
		}
		if err := tx.QueryRow(ctx, `SELECT definition FROM workflow_versions WHERE workflow_id = $1 AND version = $2 AND state IN ('published', 'deprecated')`,
			data.Workflow, data.Version).Scan(&def); err != nil {
			return err
		}
		input, err = pii.Open(ctx, s.Store.PII, tx, c.tenant.ID, data.Inputs, pii.Taint{})
		return err
	})
	if errors.Is(err, pgx.ErrNoRows) || err == nil && deployed != data.Version {
		return c.say(ctx, s, c.t("wa.run.changed", "workflow", data.Name))
	}
	if err != nil {
		return err
	}
	if input == nil {
		input = map[string]any{}
	}
	raw, _ := json.Marshal(input)
	if probs := s.inputProblems(data.Workflow, data.Version, def, raw); len(probs) > 0 {
		return c.say(ctx, s, c.t("wa.run.bad_inputs", "workflow", data.Name, "problems", whatsapp.SafeText(strings.Join(probs, "; "))))
	}
	ref, created, err := s.Store.StartRun(ctx, runtime.StartRequest{
		TenantID: c.tenant.ID, WorkflowID: data.Workflow, Version: data.Version, Environment: data.Environment,
		Trigger:   map[string]any{"type": "manual", "channel": "whatsapp", "body": input},
		StartedBy: c.p.Actor(), TriggerID: "whatsapp/" + data.Workflow.String(), DedupKey: data.Nonce,
	})
	if le, ok := runtime.IsLimit(err); ok {
		return c.say(ctx, s, c.t("wa.run.limit", "reason", le.Message))
	}
	if err != nil {
		return err
	}
	err = db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{c.tenant.ID}, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `INSERT INTO whatsapp_run_watches (tenant_id, run_id, user_id) VALUES ($1, $2, $3) ON CONFLICT DO NOTHING`,
			c.tenant.ID, ref.ID, c.p.UserID); err != nil {
			return err
		}
		return waAudit(ctx, tx, c.p, "run.start", ref.ID.String(), map[string]any{"workflow": data.Workflow, "version": data.Version,
			"environment": data.Environment, "created": created})
	})
	if err != nil {
		return err
	}
	msg := c.t("wa.run.started", "workflow", data.Name)
	if s.PublicURL != "" {
		msg += "\n" + s.PublicURL + "/runs/" + ref.ID.String()
	}
	return c.say(ctx, s, msg)
}
