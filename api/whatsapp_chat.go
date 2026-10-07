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

// WhatsAppHooks receives the platform number's webhooks: GET for Meta's
// subscription handshake, POST for messages.
func (s *Server) WhatsAppHooks() http.Handler {
	r := chi.NewRouter()
	r.Get("/", s.waHandshake)
	r.Post("/", s.waReceive)
	return r
}

func (s *Server) waHandshake(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	if s.WhatsApp == nil || q.Get("hub.mode") != "subscribe" ||
		subtle.ConstantTimeCompare([]byte(q.Get("hub.verify_token")), []byte(s.WhatsApp.Config.VerifyToken)) != 1 {
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
	if s.WhatsApp == nil {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
	if err != nil {
		writeErr(w, http.StatusRequestEntityTooLarge, "too large")
		return
	}
	if !whatsapp.VerifySignature(s.WhatsApp.Config.AppSecret, body, r.Header.Get("X-Hub-Signature-256")) {
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
		if m.PhoneNumberID != s.WhatsApp.Config.PhoneNumberID || m.ID == "" {
			continue // another number on the same app
		}
		// At most once: a message is claimed before it is acted on, so a
		// retried delivery never repeats a command.
		fresh, err := s.WhatsApp.ClaimInbound(ctx, m.ID)
		if err != nil {
			s.Logger.Error("whatsapp: claiming a message", "err", err)
			writeErr(w, http.StatusServiceUnavailable, "try again")
			return
		}
		if !fresh {
			continue
		}
		if err := s.waHandle(ctx, m); err != nil {
			s.Logger.Error("whatsapp: handling a message", "err", err)
		}
	}
	w.WriteHeader(http.StatusOK)
}

// chat is one inbound message being handled.
type chat struct {
	number  string
	in      whatsapp.Inbound
	p       *Principal // nil until resolved
	tenant  tenantRef
	tenants []tenantRef
	state   string
	data    chatData
}

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
}

// WhatsAppPublic, when set, answers a message from a number bound to no
// one, such as a tenant's public self-service menu (spec 11.2; a later
// milestone). It reports whether it handled the message.
type WhatsAppPublic func(ctx context.Context, number string, in whatsapp.Inbound) (bool, error)

var sixDigits = regexp.MustCompile(`^\s*(\d{6})\s*$`)

func (s *Server) waHandle(ctx context.Context, in whatsapp.Inbound) error {
	number, err := whatsapp.FromWaID(in.From)
	if err != nil {
		return nil //nolint:nilerr // a sender id that is not a phone number: nothing to answer
	}
	if err := s.WhatsApp.Touch(ctx, number, true, false, nil); err != nil {
		return err
	}
	// Per number (spec 11.5): a compromised phone cannot flood.
	if !s.limiter("wa:"+number, 3*time.Second, 20).Allow() {
		if s.limiter("wa-warn:"+number, time.Minute, 1).Allow() {
			return s.waSay(ctx, number, "", "You are sending messages too quickly. Wait a minute, then try again.")
		}
		return nil
	}
	user, err := s.WhatsApp.UserOf(ctx, number)
	if err != nil {
		return err
	}
	if user == uuid.Nil {
		return s.waUnbound(ctx, number, in)
	}
	c := &chat{number: number, in: in}
	if c.tenants, err = s.tenantsOf(ctx, user); err != nil {
		return err
	}
	if len(c.tenants) == 0 {
		return s.waSay(ctx, number, "", "Your Taskiem account has no active organisation.")
	}
	contact, err := s.WhatsApp.Contact(ctx, number)
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
		if err := s.WhatsApp.Touch(ctx, number, false, true, &c.tenant.ID); err != nil {
			return err
		}
	}
	if c.p, err = s.principalOf(ctx, c.tenant.ID, user); err != nil {
		return s.waSay(ctx, number, "", "Your account cannot act in "+c.tenant.Name+" any more.")
	}
	// A tapped approval button carries a signed decision token.
	if strings.HasPrefix(in.Reply, "tk1.") {
		return s.waDecide(ctx, c, in.Reply)
	}
	if err := s.loadSession(ctx, c); err != nil {
		return err
	}
	return s.waCommand(ctx, c)
}

// waUnbound answers a number bound to no one: the code it was sent, if it
// sends that back, or how to link it.
func (s *Server) waUnbound(ctx context.Context, number string, in whatsapp.Inbound) error {
	if m := sixDigits.FindStringSubmatch(in.Text); m != nil {
		var user *uuid.UUID
		if err := s.Store.Pool.QueryRow(ctx, `SELECT taskiem_wa_otp_user_for($1, $2)`, number, m[1]).Scan(&user); err != nil {
			return err
		}
		if user != nil {
			if _, err := s.waVerifyCode(ctx, *user, m[1], "whatsapp", ""); err != nil {
				return s.waSay(ctx, number, "", "That code did not link this number: "+err.Error()+".")
			}
			return s.waSay(ctx, number, "", "This number is now linked to your Taskiem account. Send *help* to see what you can do here.")
		}
	}
	if s.WhatsAppPublic != nil {
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
	return s.waSay(ctx, number, "", "This number is not linked to a Taskiem account. To use Taskiem here, sign in, open "+where+
		", add this number under WhatsApp, then type or send back the code we send you.")
}

// waSay replies inside the conversation window, in the tenant's name.
func (s *Server) waSay(ctx context.Context, number, tenant, text string, buttons ...whatsapp.Button) error {
	if tenant != "" {
		text = "[" + tenant + "] " + text
	}
	_, err := s.WhatsApp.Send(ctx, number, whatsapp.Message{Text: text, Buttons: buttons})
	return err
}

func (c *chat) say(ctx context.Context, s *Server, text string, buttons ...whatsapp.Button) error {
	return s.waSay(ctx, c.number, c.tenant.Name, text, buttons...)
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
	if cmd == "cancel" || cmd == "stop" {
		if c.state == stateIdle {
			return c.say(ctx, s, "Nothing to cancel.")
		}
		if err := s.saveSession(ctx, c, stateIdle, chatData{}, 0); err != nil {
			return err
		}
		return c.say(ctx, s, "Cancelled.")
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
			return c.say(ctx, s, "Cancelled. Nothing was started.")
		}
		return c.say(ctx, s, "Reply *yes* to start "+c.data.Name+", or *cancel*.", confirmButtons()...)
	}
	verb, arg, _ := strings.Cut(cmd, " ")
	switch {
	case cmd == "help" || cmd == "menu" || cmd == "hi" || cmd == "hello" || cmd == "start":
		return c.say(ctx, s, s.waHelp(c))
	case cmd == "status" || cmd == "what failed today" || cmd == "what failed" || cmd == "failed" || cmd == "failures":
		return s.waStatus(ctx, c)
	case cmd == "approvals":
		return s.waResendApprovals(ctx, c)
	case verb == "switch" || cmd == "organisations" || cmd == "organizations" || cmd == "tenants": //nolint:misspell // people type either
		return s.waSwitch(ctx, c, strings.TrimSpace(arg))
	case (verb == "run" || verb == "start") && arg != "":
		return s.waTrigger(ctx, c, arg)
	}
	if c.state == stateStepUp {
		return c.say(ctx, s, "Your decision on "+c.data.Step+" is waiting for you to confirm it in Taskiem with the link sent to you. Send *cancel* to drop it.")
	}
	return c.say(ctx, s, "Sorry, I did not understand that. "+s.waHelp(c))
}

func (s *Server) waHelp(c *chat) string {
	lines := []string{"You can send:", "• *status*: what ran and what failed today"}
	if c.p.Can(PermRunStart) {
		lines = append(lines, "• *run* and a workflow's name: start it (you confirm first)")
	}
	if c.p.Can(PermApprovalDecide) {
		lines = append(lines, "• *approvals*: requests waiting for you")
	}
	if len(c.tenants) > 1 {
		lines = append(lines, "• *switch* and an organisation's name: work in another one")
	}
	lines = append(lines, "• *cancel*: drop what is in progress")
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
		return c.say(ctx, s, "You are working in "+c.tenant.Name+". Your organisations:\n"+list()+"\nSend *switch* and a name to change.")
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
		return c.say(ctx, s, "Which organisation? Yours are:\n"+list())
	}
	// What was in progress belonged to the old tenant.
	if err := s.saveSession(ctx, c, stateIdle, chatData{}, 0); err != nil {
		return err
	}
	t := match[0]
	if err := s.WhatsApp.Touch(ctx, c.number, false, true, &t.ID); err != nil {
		return err
	}
	c.tenant = t
	return c.say(ctx, s, "You are now working in "+t.Name+".")
}

// waStatus answers "status" / "what failed today?": a read-only summary of
// the last 24 hours, as the person may see runs (run.read), redacted.
func (s *Server) waStatus(ctx context.Context, c *chat) error {
	if !c.p.Can(PermRunRead) {
		return c.say(ctx, s, "You cannot see runs in "+c.tenant.Name+" (it takes run.read).")
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
		b.WriteString("No runs in the last 24 hours.")
	} else {
		fmt.Fprintf(&b, "Last 24 hours: %d run", total)
		if total != 1 {
			b.WriteString("s")
		}
		var parts []string
		for _, st := range []string{"completed", "failed", "needs_reconciliation", "running", "waiting", "queued", "cancelled"} {
			if n := counts[st]; n > 0 {
				parts = append(parts, fmt.Sprintf("%d %s", n, strings.ReplaceAll(st, "_", " ")))
			}
		}
		b.WriteString(" (" + strings.Join(parts, ", ") + ").")
	}
	if len(fails) > 0 {
		b.WriteString("\n\nWhat failed:")
		for _, f := range fails {
			what := "failed"
			if f.status == "needs_reconciliation" {
				what = "needs reconciliation"
			}
			fmt.Fprintf(&b, "\n• %s (%s) %s at %s UTC", whatsapp.SafeText(f.wf), f.env, what, f.at.UTC().Format("15:04"))
		}
	} else if total > 0 {
		b.WriteString("\nNothing failed.")
	}
	if waiting > 0 {
		fmt.Fprintf(&b, "\n\n%d approval(s) waiting for you: send *approvals*.", waiting)
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
		return c.say(ctx, s, "You cannot start runs in "+c.tenant.Name+" (it takes run.start).")
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
		return c.say(ctx, s, fmt.Sprintf("No workflow deployed in %s matches %q. Send *run* and the workflow's name.", env, name))
	case len(exact) > 1:
		names := make([]string, 0, 5)
		for _, x := range exact[:min(5, len(exact))] {
			names = append(names, "• "+x.name)
		}
		return c.say(ctx, s, "Which one?\n"+strings.Join(names, "\n")+"\nSend *run* and the full name.")
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
		return c.say(ctx, s, x.name+" needs inputs that cannot be entered here yet. Start it in Taskiem.")
	}
	if err != nil {
		return err
	}
	if len(fields) > 0 {
		if err := s.saveSession(ctx, c, stateCollecting, data, waPendingTTL); err != nil {
			return err
		}
		return c.say(ctx, s, fmt.Sprintf("%s needs %d input(s). Send *cancel* to stop.\n\n%s", x.name, len(fields), fields[0].Prompt()))
	}
	return s.waAskConfirm(ctx, c, data)
}

// waFields are the inputs to collect for the session's workflow.
func (s *Server) waFields(ctx context.Context, c *chat) ([]whatsapp.Field, error) {
	var def []byte
	if err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{c.tenant.ID}, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT definition FROM workflow_versions WHERE workflow_id = $1 AND version = $2`, c.data.Workflow, c.data.Version).Scan(&def)
	}); err != nil {
		return nil, err
	}
	d, err := s.definitionFor(c.data.Workflow, c.data.Version, def)
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
	if c.data.Index >= len(fields) {
		return s.waAskConfirm(ctx, c, c.data)
	}
	f := fields[c.data.Index]
	v, perr := f.Parse(text)
	if perr != nil {
		return c.say(ctx, s, "That does not fit: "+perr.Error()+".\n\n"+f.Prompt())
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

func confirmButtons() []whatsapp.Button {
	return []whatsapp.Button{{ID: "yes", Title: "Yes, run it"}, {ID: "cancel", Title: "Cancel"}}
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
	msg := fmt.Sprintf("Start %s (version %d) in %s", data.Name, data.Version, data.Environment)
	if len(lines) > 0 {
		msg += " with:\n" + strings.Join(lines, "\n")
	} else {
		msg += "?"
	}
	return c.say(ctx, s, msg+"\n\nReply *yes* to start it or *cancel*.", confirmButtons()...)
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
		return c.say(ctx, s, "You cannot start runs in "+c.tenant.Name+" any more.")
	}
	if !s.limiter("wa-run:"+c.number, 20*time.Second, 3).Allow() {
		return c.say(ctx, s, "You have started several runs just now. Wait a minute, then try again.")
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
		return c.say(ctx, s, data.Name+" changed while you were confirming. Send *run* again.")
	}
	if err != nil {
		return err
	}
	if input == nil {
		input = map[string]any{}
	}
	raw, _ := json.Marshal(input)
	if probs := s.inputProblems(data.Workflow, data.Version, def, raw); len(probs) > 0 {
		return c.say(ctx, s, "The inputs do not fit "+data.Name+": "+whatsapp.SafeText(strings.Join(probs, "; "))+". Send *run* again.")
	}
	ref, created, err := s.Store.StartRun(ctx, runtime.StartRequest{
		TenantID: c.tenant.ID, WorkflowID: data.Workflow, Version: data.Version, Environment: data.Environment,
		Trigger:   map[string]any{"type": "manual", "channel": "whatsapp", "body": input},
		StartedBy: c.p.Actor(), TriggerID: "whatsapp/" + data.Workflow.String(), DedupKey: data.Nonce,
	})
	if le, ok := runtime.IsLimit(err); ok {
		return c.say(ctx, s, "Not started: "+le.Message)
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
	msg := fmt.Sprintf("Started %s. I will tell you when it finishes.", data.Name)
	if s.PublicURL != "" {
		msg += "\n" + s.PublicURL + "/runs/" + ref.ID.String()
	}
	return c.say(ctx, s, msg)
}
